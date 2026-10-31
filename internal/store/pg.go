package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"contractaudit/internal/accounts"
	"contractaudit/internal/audit"
	"contractaudit/internal/config"
	"contractaudit/internal/execution"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var identRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// OpenTest opens an isolated schema in the test database.
// A DSN whose database name is the development or production database is refused
// before any connection is made.
func OpenTest(dir, testDSN string) (*Store, error) {
	env := config.Environ{
		AppEnv:          config.ModeTest,
		DevDatabaseURL:  os.Getenv("DEV_DATABASE_URL"),
		TestDatabaseURL: strings.TrimSpace(testDSN),
		ProdDatabaseURL: os.Getenv("PROD_DATABASE_URL"),
	}
	if env.TestDatabaseURL == "" {
		env.TestDatabaseURL = os.Getenv("TEST_DATABASE_URL")
	}
	sel, err := config.Select(env)
	if err != nil {
		return nil, err
	}
	id, err := newID("")
	if err != nil {
		return nil, err
	}
	return openStore(dir, sel, "it_"+id, true)
}

// Reopen loads the same schema into a new store. It does not drop that schema.
func (s *Store) Reopen() (*Store, error) {
	reopened, err := openStore(s.dir, s.sel, s.schema, false)
	if err != nil {
		return nil, err
	}
	reopened.readOnly.Store(s.ReadOnly())
	return reopened, nil
}

func (s *Store) Close() error {
	if s == nil || s.pool == nil {
		return nil
	}
	s.executionMu.Lock()
	s.closing = true
	s.executionMu.Unlock()
	s.executionWG.Wait()
	s.closeTelemetry()
	var err error
	if s.dropSchema && s.schema != "" && s.schema != "public" {
		quoted, qerr := quoteIdent(s.schema)
		if qerr != nil {
			err = qerr
		} else if _, derr := s.pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quoted+" CASCADE"); derr != nil {
			err = derr
		}
	}
	s.pool.Close()
	return err
}

// RewriteAudit attempts to change a stored audit row. The database rejects it.
func (s *Store) RewriteAudit(runID, eventID, actor string) error {
	_, err := s.pool.Exec(context.Background(),
		`UPDATE audit_events SET actor = $1 WHERE run_id = $2 AND event_id = $3`,
		actor, runID, eventID)
	if err == nil {
		return errors.New("audit update was accepted")
	}
	if strings.Contains(err.Error(), "append-only") {
		return audit.ErrBrokenChain
	}
	return err
}

func openStore(dir string, sel config.Selection, schema string, drop bool) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		return nil, err
	}
	ctx := context.Background()
	if err := ensureDatabase(ctx, sel); err != nil {
		return nil, err
	}
	if err := createSchema(ctx, sel.DSN, schema); err != nil {
		return nil, err
	}
	pool, err := newPool(ctx, sel.DSN, schema)
	if err != nil {
		return nil, err
	}
	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	s := &Store{
		dir: dir, sel: sel, schema: schema, dropSchema: drop, pool: pool,
		executionSlots: make(chan struct{}, maxConcurrentReviews), running: make(map[string]func()), claims: make(map[string]JobClaim),
	}
	if err := configureSubjectSourceFromEnvironment(s); err != nil {
		pool.Close()
		return nil, err
	}
	if err := s.load(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	s.st.normalize()
	if err := s.validateLoadedParseHistory(); err != nil {
		pool.Close()
		return nil, err
	}
	if err := s.markUnfinishedInterrupted(); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func ensureDatabase(ctx context.Context, sel config.Selection) error {
	name := sel.Database.Name
	if sel.Mode == config.ModeProduction || name == config.ProdDatabaseName {
		return nil
	}
	if sel.Mode == config.ModeTest && (name == config.DevDatabaseName || name == config.ProdDatabaseName) {
		return config.ErrRefusedDatabase
	}
	if sel.Mode == config.ModeDevelopment && (name == config.TestDatabaseName || name == config.ProdDatabaseName) {
		return config.ErrRefusedDatabase
	}
	quoted, err := quoteIdent(name)
	if err != nil {
		return err
	}
	admin, err := withDatabase(sel.DSN, "postgres")
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, createErr := conn.Exec(ctx, "CREATE DATABASE "+quoted); createErr != nil {
		if !duplicateDatabase(createErr) {
			return createErr
		}
		// Concurrent package tests can race on pg_database's unique index before
		// PostgreSQL emits duplicate_database. Accept neither code blindly.
		if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return createErr
		}
	}
	return nil
}

func createSchema(ctx context.Context, dsn, schema string) error {
	if schema == "public" {
		return nil
	}
	quoted, err := quoteIdent(schema)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+quoted)
	return err
}

func newPool(ctx context.Context, dsn, schema string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 4
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SELECT
			set_config('search_path', $1, false),
			set_config('app.tenant_id', $2, false),
			set_config('app.org_id', $3, false),
			set_config('app.actor_id', 'system', false),
			set_config('app.actor_role', 'api', false)`, schema, accounts.LocalDemoTenant, "demo-org")
		return err
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	for _, stmt := range schemaStatements {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	if err := migrateMaterials(ctx, pool); err != nil {
		return err
	}
	// Service-object association constraints pin a material version and parse
	// selection, so install them only after those immutable tables exist.
	if _, err := pool.Exec(ctx, serviceObjectSchemaSQL); err != nil {
		return fmt.Errorf("migrate service objects: %w", err)
	}
	if _, err := pool.Exec(ctx, operatorMappingChangesSchemaSQL); err != nil {
		return fmt.Errorf("migrate operator mapping lineages: %w", err)
	}
	if err := migrateSubjects(ctx, pool); err != nil {
		return err
	}
	if err := migrateLegalApplicability(ctx, pool); err != nil {
		return err
	}
	if err := migrateModelBudgetFunctions(ctx, pool); err != nil {
		return err
	}
	return migrateExperienceDistillFunctions(ctx, pool)
}

// Migrate applies the schema using MIGRATOR_DATABASE_URL. The URL must select
// the same database as the APP_ENV-selected application URL.
func Migrate(ctx context.Context) error {
	if ctx == nil {
		return errors.New("migration context is required")
	}
	selection, err := config.FromProcess()
	if err != nil {
		return err
	}
	schema, err := applicationSchema()
	if err != nil {
		return err
	}
	migratorDSN := strings.TrimSpace(os.Getenv("MIGRATOR_DATABASE_URL"))
	if migratorDSN == "" {
		return errors.New("MIGRATOR_DATABASE_URL is required for migrations")
	}
	identity, err := config.ParseDSN(migratorDSN)
	if err != nil || identity != selection.Database {
		return errors.New("MIGRATOR_DATABASE_URL must select the APP_ENV database")
	}
	if err := createSchema(ctx, migratorDSN, schema); err != nil {
		return err
	}
	cfg, err := pgxpool.ParseConfig(migratorDSN)
	if err != nil {
		return err
	}
	cfg.MaxConns = 2
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SELECT set_config('search_path', $1, false)`, schema)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	return migrate(ctx, pool)
}

func applicationSchema() (string, error) {
	schema := strings.TrimSpace(os.Getenv("DATABASE_SCHEMA"))
	if schema == "" {
		schema = "public"
	}
	if _, err := quoteIdent(schema); err != nil {
		return "", err
	}
	return schema, nil
}

func openApplicationStore(dir string, sel config.Selection, schema string) (*Store, error) {
	if _, err := quoteIdent(schema); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		return nil, err
	}
	ctx := context.Background()
	pool, err := newPool(ctx, sel.DSN, schema)
	if err != nil {
		return nil, err
	}
	if err := rejectPrivilegedApplicationRole(ctx, pool, schema); err != nil {
		pool.Close()
		return nil, err
	}
	s := &Store{
		dir: dir, sel: sel, schema: schema, pool: pool,
		executionSlots: make(chan struct{}, maxConcurrentReviews), running: make(map[string]func()), claims: make(map[string]JobClaim),
	}
	if err := configureSubjectSourceFromEnvironment(s); err != nil {
		pool.Close()
		return nil, err
	}
	if err := s.load(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	s.st.normalize()
	if err := s.validateLoadedParseHistory(); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func rejectPrivilegedApplicationRole(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	var privileged bool
	err := pool.QueryRow(ctx, `SELECT r.rolsuper OR r.rolbypassrls OR EXISTS (
		SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname=$1 AND c.relname='cases' AND c.relowner=r.oid
	) FROM pg_roles r WHERE r.rolname=current_user`, schema).Scan(&privileged)
	if err != nil {
		return err
	}
	if privileged {
		return errors.New("application database role must not own tables, be superuser, or bypass row-level security")
	}
	return nil
}

func (s *Store) load(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, dbScope{}); err != nil {
		return err
	}
	st, err := loadState(ctx, tx, true)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.st = st
	return nil
}

// loadState reads the whole durable state. strict selects whether a persisted
// artifact that fails integrity verification aborts the load (startup, writes) or
// is dropped in favour of per-request degradation (reads).
// canonicalizeLoadedRows rewrites the few row fields whose JSON round trip is
// lossy, so a freshly loaded state is structurally equal to its own clone when
// nothing changed. Without it every later write would see these rows as changed
// and attempt to persist them, which a service that does not own the table
// must not do. Only fields tagged omitempty need this: the clone drops them when
// empty and unmarshals them back as nil.
func canonicalizeLoadedRows(st *state) {
	for i := range st.Materials {
		st.Materials[i].CreatedAt = st.Materials[i].CreatedAt.UTC().Round(0)
	}
	for i := range st.MaterialVersions {
		st.MaterialVersions[i].CreatedAt = st.MaterialVersions[i].CreatedAt.UTC().Round(0)
	}
	for i := range st.MaterialChains {
		st.MaterialChains[i].CreatedAt = st.MaterialChains[i].CreatedAt.UTC().Round(0)
		st.MaterialChains[i].UpdatedAt = st.MaterialChains[i].UpdatedAt.UTC().Round(0)
	}
	for i := range st.UploadIdempotency {
		st.UploadIdempotency[i].CreatedAt = st.UploadIdempotency[i].CreatedAt.UTC().Round(0)
	}
	for i := range st.Evaluations {
		st.Evaluations[i].FactIDs = emptyStringsToNil(st.Evaluations[i].FactIDs)
		st.Evaluations[i].ObjectRefs = emptyStringsToNil(st.Evaluations[i].ObjectRefs)
	}
	for i := range st.Snapshots {
		st.Snapshots[i].ParsedDocumentIDs = emptyStringsToNil(st.Snapshots[i].ParsedDocumentIDs)
	}
}

func emptyStringsToNil(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return values
}

func loadState(ctx context.Context, q queryer, strict bool) (state, error) {
	var st state
	var err error
	if st.Sessions, err = loadSessions(ctx, q); err != nil {
		return state{}, err
	}
	if st.Cases, err = loadCases(ctx, q); err != nil {
		return state{}, err
	}
	if st.Documents, err = loadDocuments(ctx, q); err != nil {
		return state{}, err
	}
	if st.Materials, st.MaterialVersions, st.MaterialChains, err = loadMaterialState(ctx, q); err != nil {
		return state{}, err
	}
	if st.UploadIdempotency, err = loadUploadIdempotency(ctx, q); err != nil {
		return state{}, err
	}
	subjectState, err := loadSubjectState(ctx, q)
	if err != nil {
		return state{}, err
	}
	st.Subjects = subjectState.Subjects
	st.SubjectVersions = subjectState.SubjectVersions
	st.CaseSubjectRoles = subjectState.CaseSubjectRoles
	st.CaseSubjectRoleVersions = subjectState.CaseSubjectRoleVersions
	st.SubjectChangeRequests = subjectState.SubjectChangeRequests
	st.SubjectChangeDecisions = subjectState.SubjectChangeDecisions
	st.SubjectIdempotency = subjectState.SubjectIdempotency
	st.SubjectSourceSnapshots = subjectState.SubjectSourceSnapshots
	if st.ParsedDocuments, err = loadParsedDocuments(ctx, q, st.Documents, strict); err != nil {
		return state{}, err
	}
	if st.Runs, err = loadRuns(ctx, q); err != nil {
		return state{}, err
	}
	if st.Attempts, err = loadAttempts(ctx, q); err != nil {
		return state{}, err
	}
	if st.AttemptInputs, err = loadAttemptInputs(ctx, q); err != nil {
		return state{}, err
	}
	if st.AttemptParseSelections, err = loadAttemptParseSelections(ctx, q); err != nil {
		return state{}, err
	}
	if st.ExecutionEvents, err = loadExecutionEvents(ctx, q); err != nil {
		return state{}, err
	}
	if st.Findings, err = loadFindings(ctx, q); err != nil {
		return state{}, err
	}
	if st.Evaluations, err = loadEvaluations(ctx, q); err != nil {
		return state{}, err
	}
	if st.Decisions, err = loadDecisions(ctx, q); err != nil {
		return state{}, err
	}
	if st.Events, err = loadEvents(ctx, q); err != nil {
		return state{}, err
	}
	if st.Activity, err = loadActivity(ctx, q); err != nil {
		return state{}, err
	}
	if st.Idempotency, err = loadIdempotency(ctx, q); err != nil {
		return state{}, err
	}
	if st.Snapshots, err = loadSnapshots(ctx, q); err != nil {
		return state{}, err
	}
	for _, attempt := range st.Attempts {
		if err := validateGovernedAttemptSnapshot(attempt, st.snapshotOf(attempt.RunID)); err != nil {
			return state{}, fmt.Errorf("load governed attempt %q: %w", attempt.ID, err)
		}
	}
	if st.Audits, err = loadAudits(ctx, q); err != nil {
		return state{}, err
	}
	if st.Jobs, err = loadJobs(ctx, q); err != nil {
		return state{}, err
	}
	if st.Outbox, err = loadOutbox(ctx, q); err != nil {
		return state{}, err
	}
	if err := loadBusinessState(ctx, q, &st); err != nil {
		return state{}, err
	}
	// Canonicalize the rows the state clone cannot round-trip: a slice field
	// tagged omitempty comes back as nil from the clone's JSON round trip, so a
	// row loaded from PostgreSQL with an empty array would otherwise look
	// changed on every later write and turn a no-op into a spurious delta that a
	// non-owning service must not persist.
	canonicalizeLoadedRows(&st)
	st.normalize()
	applyReadProjections(&st)
	return st, nil
}

func (s *Store) updateState(ctx context.Context, scope dbScope, attemptID string, fn func(*state) error) error {
	return s.updateStateWith(ctx, scope, attemptID, nil, fn)
}

// updateStateWith is updateState plus an optional hook that runs inside the same
// transaction, after the advisory locks and before the state snapshot is read.
// A caller that must freeze an authoritative row (for example the governance
// active pointer) selects it in the hook, so the selection and the write it
// justifies are linearised in one transaction instead of being compared against
// itself afterwards.
func (s *Store) updateStateWith(ctx context.Context, scope dbScope, attemptID string, hook func(pgx.Tx) error, fn func(*state) error) error {
	caseIDs := []string{}
	if scope.CaseID != "" {
		caseIDs = append(caseIDs, scope.CaseID)
	}
	return s.updateStateWithLocks(ctx, scope, attemptID, nil, caseIDs, hook, fn)
}

// updateStateWithLocks extends the ordinary schema-scoped writer with ordered
// subject and case locks. Subject mutations that affect bindings in several
// cases take the schema lock first, then sorted subject locks, then sorted case
// locks before loading or changing state.
func (s *Store) updateStateWithLocks(ctx context.Context, scope dbScope, attemptID string, subjectIDs, caseIDs []string, hook func(pgx.Tx) error, fn func(*state) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(), 0))`); err != nil {
		return err
	}
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return err
	}
	// Lock order: schema-wide key, sorted subjects, sorted cases, attempt row,
	// then claim row. This preserves the established schema/case ordering while
	// avoiding subject-versus-case inversion for multi-case correction writes.
	if err := scopedSubjectLocks(ctx, tx, subjectIDs); err != nil {
		return err
	}
	if len(caseIDs) == 0 && scope.CaseID != "" {
		caseIDs = []string{scope.CaseID}
	}
	if err := scopedCaseLocks(ctx, tx, caseIDs); err != nil {
		return err
	}
	var expectedAttemptVersion *int64
	if attemptID != "" {
		if err := scopedAttemptLock(ctx, tx, attemptID); err != nil {
			return err
		}
		var currentState string
		var attemptVersion int64
		if err := tx.QueryRow(ctx, `SELECT state, entity_version FROM review_attempts WHERE id=$1 FOR UPDATE`, attemptID).Scan(&currentState, &attemptVersion); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errAttemptMissing
			}
			return err
		}
		if !attemptActive(currentState) {
			return errAttemptTerminal
		}
		expectedAttemptVersion = &attemptVersion
		if scope.JobID != "" {
			var jobID string
			err := tx.QueryRow(ctx, `SELECT id FROM worker_jobs WHERE id=$1 AND tenant_id=$2 AND org_id=$3 AND attempt_id=$4 AND worker_id=$5 AND generation=$6 AND state='running' AND lease_until > clock_timestamp() FOR UPDATE`, scope.JobID, scope.TenantID, scope.OrgID, attemptID, scope.WorkerID, scope.Generation).Scan(&jobID)
			if errors.Is(err, pgx.ErrNoRows) {
				return errLeaseLost
			}
			if err != nil {
				return err
			}
		}
	}
	previous, err := loadState(ctx, tx, true)
	if err != nil {
		return err
	}
	if hook != nil {
		if err := hook(tx); err != nil {
			return err
		}
	}
	if scope.CaseID != "" {
		caseItem, ok := previous.caseByID(scope.CaseID)
		tenantID := scope.TenantID
		if tenantID == "" {
			tenantID = accounts.LocalDemoTenant
		}
		if ok && caseItem.TenantID == tenantID {
			scope.OrgID = caseItem.OwnerOrgID
			if err := setLocalScope(ctx, tx, scope); err != nil {
				return err
			}
		}
	}
	next, err := previous.clone()
	if err != nil {
		return err
	}
	if err := fn(&next); err != nil {
		return err
	}
	if err := validateAppendOnly(previous, next); err != nil {
		return err
	}
	if err := s.persist(ctx, tx, previous, next, expectedAttemptVersion); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.st = next
	return nil
}

func (s *Store) persist(ctx context.Context, tx pgx.Tx, previous, next state, expectedAttemptVersion *int64) error {
	old, changed, err := stateDelta(previous, next)
	if err != nil {
		return err
	}
	if err := s.validateServiceDelta(old, changed); err != nil {
		return err
	}
	if err := replaceRows(ctx, tx, previous, changed, s.serviceOwner, expectedAttemptVersion); err != nil {
		return err
	}
	if err := syncBusinessRows(ctx, tx, previous, old, changed); err != nil {
		return err
	}
	if err := syncSnapshots(ctx, tx, changed.Snapshots); err != nil {
		return err
	}
	if err := syncAudits(ctx, tx, changed.Audits, s.serviceOwner); err != nil {
		return err
	}
	if err := syncJobs(ctx, tx, changed.Jobs); err != nil {
		return err
	}
	if err := s.admitNewJobs(ctx, tx, old.Jobs, changed.Jobs); err != nil {
		return err
	}
	return syncOutbox(ctx, tx, changed.Outbox)
}

func replaceRows(ctx context.Context, tx pgx.Tx, previous, st state, owner string, expectedAttemptVersion *int64) error {
	// Sessions are written only by the session APIs (EstablishSession/Logout),
	// which are token-keyed single-row writes with their own authority checks. A
	// session delta reaching the generic persistence path would let a stale
	// snapshot resurrect a revoked session, so it fails closed instead.
	if len(st.Sessions) > 0 {
		return errors.New("session changes must use the session APIs")
	}
	for _, item := range st.Cases {
		raw, err := jsonText(item.MemberIDs)
		if err != nil {
			return err
		}
		var expectedCaseVersion any
		if _, exists := previous.caseByID(item.ID); exists {
			var version int64
			if err := tx.QueryRow(ctx, `SELECT entity_version FROM cases WHERE id=$1`, item.ID).Scan(&version); err != nil {
				return err
			}
			expectedCaseVersion = version
		}
		tag, err := tx.Exec(ctx, `INSERT INTO cases (id, tenant_id, org_id, business_line, scenario, business_status, archived_at, title, contract_type, owner_id, member_ids, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT (id) DO UPDATE SET title=EXCLUDED.title, contract_type=EXCLUDED.contract_type, owner_id=EXCLUDED.owner_id, member_ids=EXCLUDED.member_ids,
			business_line=EXCLUDED.business_line, scenario=EXCLUDED.scenario, business_status=EXCLUDED.business_status, archived_at=EXCLUDED.archived_at, updated_at=EXCLUDED.updated_at, entity_version=cases.entity_version+1
			WHERE cases.entity_version=$14`,
			item.ID, item.TenantID, item.OwnerOrgID, item.BusinessLine, item.Scenario, item.BusinessStatus, item.ArchivedAt, item.Title, item.ContractType, item.OwnerID, raw, item.CreatedAt, item.UpdatedAt, expectedCaseVersion)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("case entity version changed during persistence")
		}
	}
	for _, item := range st.Documents {
		if _, err := tx.Exec(ctx, `INSERT INTO documents (id, tenant_id, org_id, case_id, role, filename, media_type, digest, size, origin, storage_backend, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
			ON CONFLICT (id) DO NOTHING`, item.ID, item.TenantID, item.OrgID, item.CaseID, item.Role, item.Filename, item.MediaType, item.Digest, item.Size, item.Origin, item.StorageBackend, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.Materials {
		if _, err := tx.Exec(ctx, `INSERT INTO material_entities (tenant_id,org_id,case_id,material_id,role,purpose,legacy_status,legacy_selected,current_document_id,current_version,created_by,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
			ON CONFLICT (tenant_id,org_id,case_id,material_id) DO UPDATE SET legacy_status=EXCLUDED.legacy_status, legacy_selected=EXCLUDED.legacy_selected,
			current_document_id=EXCLUDED.current_document_id,current_version=EXCLUDED.current_version`,
			item.TenantID, item.OrgID, item.CaseID, item.ID, item.Role, item.Purpose, item.LegacyStatus, item.LegacySelected, item.CurrentDocumentID, item.CurrentVersion, item.CreatedBy, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.MaterialVersions {
		if _, err := tx.Exec(ctx, `INSERT INTO material_versions (tenant_id,org_id,case_id,material_id,version,document_id,previous_document_id,created_by,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9) ON CONFLICT (tenant_id,org_id,case_id,material_id,version) DO NOTHING`,
			item.TenantID, item.OrgID, item.CaseID, item.MaterialID, item.Version, item.DocumentID, item.PreviousDocument, item.CreatedBy, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.MaterialChains {
		selected, err := jsonText(item.SelectedDocumentIDs)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO case_contract_chains (tenant_id,org_id,case_id,primary_material_id,legacy_selected_document_id,legacy_selection_status,selection_version,selected_document_ids,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (tenant_id,org_id,case_id) DO UPDATE SET primary_material_id=EXCLUDED.primary_material_id,legacy_selected_document_id=EXCLUDED.legacy_selected_document_id,legacy_selection_status=EXCLUDED.legacy_selection_status,selection_version=EXCLUDED.selection_version,selected_document_ids=EXCLUDED.selected_document_ids,updated_at=EXCLUDED.updated_at`,
			item.TenantID, item.OrgID, item.CaseID, item.PrimaryMaterialID, item.LegacySelectedDocumentID, item.LegacySelectionStatus, item.SelectionVersion, selected, item.CreatedAt, item.UpdatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.UploadIdempotency {
		if _, err := tx.Exec(ctx, `INSERT INTO document_upload_idempotency (tenant_id,org_id,case_id,actor_id,idempotency_key,request_hash,document_id,material_id,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (tenant_id,org_id,case_id,actor_id,idempotency_key) DO NOTHING`, item.TenantID, item.OrgID, item.CaseID, item.ActorID, item.Key, item.RequestHash, item.DocumentID, item.MaterialID, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.Subjects {
		if _, err := tx.Exec(ctx, `INSERT INTO subjects (tenant_id,subject_id,current_version,status,created_by_user_id,created_by_issuer,created_by_subject,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (tenant_id,subject_id) DO UPDATE SET current_version=EXCLUDED.current_version,status=EXCLUDED.status`,
			item.TenantID, item.ID, item.CurrentVersion, item.Status, item.CreatedByUser, item.CreatedByIssuer, item.CreatedBySubject, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.SubjectVersions {
		payload, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO subject_versions (tenant_id,subject_id,version,payload,payload_hash,created_at)
			VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (tenant_id,subject_id,version) DO NOTHING`, item.TenantID, item.SubjectID, item.Version, payload, item.PayloadHash, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.Subjects {
		current, ok := subjectVersionAt(st.SubjectVersions, item.ID, item.CurrentVersion)
		if !ok {
			return fmt.Errorf("subject %s points to a missing immutable version", item.ID)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM subject_current_identifier_claims WHERE tenant_id=$1 AND subject_id=$2`, item.TenantID, item.ID); err != nil {
			return err
		}
		for _, claim := range current.IdentityClaims {
			if _, err := tx.Exec(ctx, `INSERT INTO subject_current_identifier_claims
				(tenant_id,subject_id,namespace,identifier_type,value,verification_state,verification_scope,source_catalog_id,source_snapshot_id,source_digest,source_time,source_valid_from,source_valid_until)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, item.TenantID, item.ID, claim.Namespace, claim.IdentifierType, claim.Value,
				claim.VerificationState, claim.VerificationScope, claim.SourceCatalogID, claim.SourceSnapshotID, claim.SourceDigest, claim.SourceTime, claim.SourceValidFrom, claim.SourceValidUntil); err != nil {
				return err
			}
		}
	}
	for _, item := range st.CaseSubjectRoles {
		if _, err := tx.Exec(ctx, `INSERT INTO case_subject_roles (tenant_id,org_id,case_id,binding_id,current_version,created_by_user_id,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (tenant_id,org_id,case_id,binding_id) DO UPDATE SET current_version=EXCLUDED.current_version`,
			item.TenantID, item.OrgID, item.CaseID, item.BindingID, item.CurrentVersion, item.CreatedByUser, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.CaseSubjectRoleVersions {
		payload, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO case_subject_role_versions
			(tenant_id,org_id,case_id,binding_id,version,subject_id,subject_version,role,binding_state,source_kind,payload,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT (tenant_id,org_id,case_id,binding_id,version) DO NOTHING`,
			item.TenantID, item.OrgID, item.CaseID, item.BindingID, item.Version, item.SubjectID, item.SubjectVersion, item.Role, item.BindingState, item.SourceKind, payload, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.SubjectChangeRequests {
		payload, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO subject_change_requests
			(tenant_id,org_id,source_case_id,request_id,target_kind,target_subject_id,target_binding_id,expected_subject_version,expected_binding_version,payload,payload_hash,affected_bindings_hash,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11,$12,$13) ON CONFLICT (tenant_id,request_id) DO NOTHING`,
			item.TenantID, item.OrgID, item.SourceCaseID, item.RequestID, item.TargetKind, item.TargetSubjectID, item.TargetBindingID, item.ExpectedSubjectVersion, item.ExpectedBindingVersion, payload, item.CanonicalPayloadHash, item.ExpectedBindingsHash, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.SubjectChangeDecisions {
		payload, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO subject_change_decisions (tenant_id,request_id,decision_id,decision,actor_user_id,actor_issuer,actor_subject,payload,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (tenant_id,decision_id) DO NOTHING`,
			item.TenantID, item.RequestID, item.DecisionID, item.Decision, item.Actor.UserID, item.Actor.Issuer, item.Actor.Subject, payload, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.SubjectIdempotency {
		if _, err := tx.Exec(ctx, `INSERT INTO subject_write_idempotency (tenant_id,actor_user_id,operation,target_id,idempotency_key,payload_hash,http_status,response,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (tenant_id,actor_user_id,operation,target_id,idempotency_key) DO NOTHING`,
			item.TenantID, item.ActorUserID, item.Operation, item.TargetID, item.Key, item.PayloadHash, item.HTTPStatus, item.Response, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.SubjectSourceSnapshots {
		if _, err := tx.Exec(ctx, `INSERT INTO subject_source_snapshots
			(tenant_id,org_id,case_id,snapshot_id,source_catalog_id,source_version,source_payload,source_digest,source_scope,captured_at,created_by_user_id,created_by_issuer,created_by_subject)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT (tenant_id,org_id,case_id,snapshot_id) DO NOTHING`,
			item.TenantID, item.OrgID, item.CaseID, item.SnapshotID, item.SourceCatalogID, item.SourceVersion, item.SourcePayload, item.SourceDigest, item.SourceScope, item.CapturedAt, item.CreatedByUser, item.CreatedByIssuer, item.CreatedBySubject); err != nil {
			return err
		}
	}
	for _, item := range st.ParsedDocuments {
		payload, err := item.canonicalJSON()
		if err != nil {
			return err
		}
		var normalized, irDigest any
		if item.NormalizedDigest != "" {
			normalized = item.NormalizedDigest
		}
		if item.IRDigest != "" {
			irDigest = item.IRDigest
		}
		if _, err := tx.Exec(ctx, `INSERT INTO parsed_documents (selection_order, id, document_id, original_digest, parser_id, parser_version, config_hash, normalization_version, normalized_digest, ir_digest, artifact_digest, created_at, status, payload)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT (id) DO NOTHING`,
			item.SelectionOrder, item.ID, item.DocumentID, item.OriginalDigest, item.ParserID, item.ParserVersion, item.ConfigHash, item.NormalizationVersion,
			normalized, irDigest, item.ArtifactDigest, item.CreatedAt, item.Status, payload); err != nil {
			return err
		}
	}
	for _, item := range st.Runs {
		if _, err := tx.Exec(ctx, `INSERT INTO review_runs (id, case_id, state, model_ref, partial_reason, failure_message, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (id) DO UPDATE SET case_id=EXCLUDED.case_id, state=EXCLUDED.state, model_ref=EXCLUDED.model_ref, partial_reason=EXCLUDED.partial_reason, failure_message=EXCLUDED.failure_message, updated_at=EXCLUDED.updated_at`,
			item.ID, item.CaseID, item.State, item.ModelRef, item.PartialReason, item.FailureMessage, item.CreatedAt, item.UpdatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.Attempts {
		var expectedVersion any
		if expectedAttemptVersion != nil {
			expectedVersion = *expectedAttemptVersion
		}
		tag, err := tx.Exec(ctx, `INSERT INTO review_attempts (id, run_id, ordinal, retry_of_attempt_id, state, accepted_at, started_at, finished_at, input_hash, output_hash, error_code, history_kind, actor_id, rule_pack_snapshot, rule_pack_hash, rule_pack_snapshot_digest, rule_pack_compiler_ref, rule_pack_id, rule_pack_version, rule_pack_pointer_revision, planner_configuration_id, planner_configuration)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)
			ON CONFLICT (id) DO UPDATE SET state=EXCLUDED.state, started_at=EXCLUDED.started_at, finished_at=EXCLUDED.finished_at, input_hash=EXCLUDED.input_hash, output_hash=EXCLUDED.output_hash, error_code=EXCLUDED.error_code, history_kind=EXCLUDED.history_kind,
			rule_pack_snapshot=EXCLUDED.rule_pack_snapshot, rule_pack_hash=EXCLUDED.rule_pack_hash, rule_pack_snapshot_digest=EXCLUDED.rule_pack_snapshot_digest, rule_pack_compiler_ref=EXCLUDED.rule_pack_compiler_ref,
			rule_pack_id=EXCLUDED.rule_pack_id, rule_pack_version=EXCLUDED.rule_pack_version, rule_pack_pointer_revision=EXCLUDED.rule_pack_pointer_revision,
			planner_configuration_id=EXCLUDED.planner_configuration_id, planner_configuration=EXCLUDED.planner_configuration,
			entity_version=review_attempts.entity_version+1
			WHERE review_attempts.state IN ('accepted','running')
				AND ($23::bigint IS NULL OR review_attempts.entity_version=$23)
				AND ((review_attempts.rule_pack_snapshot IS NULL AND review_attempts.rule_pack_hash='' AND review_attempts.rule_pack_snapshot_digest='' AND review_attempts.rule_pack_compiler_ref='') OR
					(review_attempts.rule_pack_snapshot IS NOT DISTINCT FROM EXCLUDED.rule_pack_snapshot AND review_attempts.rule_pack_hash=EXCLUDED.rule_pack_hash AND review_attempts.rule_pack_snapshot_digest=EXCLUDED.rule_pack_snapshot_digest AND review_attempts.rule_pack_compiler_ref=EXCLUDED.rule_pack_compiler_ref))
				AND review_attempts.planner_configuration_id=EXCLUDED.planner_configuration_id
				AND review_attempts.planner_configuration IS NOT DISTINCT FROM EXCLUDED.planner_configuration`,
			item.ID, item.RunID, item.Ordinal, item.RetryOfAttemptID, item.State, item.AcceptedAt, item.StartedAt, item.FinishedAt, item.InputHash, item.OutputHash, item.ErrorCode, item.HistoryKind, item.ActorID, []byte(item.RulePackSnapshot), item.RulePackHash, item.RulePackSnapshotDigest, item.RulePackCompilerRef,
			item.RulePackID, item.RulePackVersion, item.RulePackPointerRevision, item.PlannerConfigurationID, plannerPayloadValue(item.PlannerConfiguration), expectedVersion)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("attempt state or entity version changed during persistence")
		}
	}
	for _, item := range st.AttemptParseSelections {
		if _, err := tx.Exec(ctx, `INSERT INTO review_attempt_parse_selections (attempt_id, run_id, document_id, original_digest, parsed_document_id, selection_order, parser_id, parser_version, config_hash, normalization_version, artifact_digest)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (attempt_id,document_id) DO NOTHING`,
			item.AttemptID, item.RunID, item.DocumentID, item.OriginalDigest, item.ParsedDocumentID, item.SelectionOrder,
			item.ParserID, item.ParserVersion, item.ConfigHash, item.NormalizationVersion, item.ArtifactDigest); err != nil {
			return err
		}
	}

	for _, item := range st.AttemptInputs {
		raw, err := jsonText(item.Documents)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO review_attempt_inputs (attempt_id, run_id, input_hash, frozen_at, documents) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (attempt_id) DO NOTHING`, item.AttemptID, item.RunID, item.InputHash, item.FrozenAt, raw); err != nil {
			return err
		}
	}
	for _, item := range st.ExecutionEvents {
		var usage, trace any
		if item.Usage != nil {
			encoded, err := jsonText(item.Usage)
			if err != nil {
				return err
			}
			usage = encoded
		}
		if item.Trace != nil {
			encoded, err := jsonText(item.Trace)
			if err != nil {
				return err
			}
			trace = encoded
		}
		if _, err := tx.Exec(ctx, `INSERT INTO execution_events (attempt_id, event_seq, span_id, parent_span_id, node_kind, phase, event_type, occurred_at, duration_ms, status, error_code, model_ref, usage, trace, recorded_at, event_key, generation)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) ON CONFLICT (attempt_id,event_seq) DO NOTHING`,
			item.AttemptID, item.Seq, item.SpanID, item.ParentSpanID, item.NodeKind, item.Phase, item.EventType, item.At, item.DurationMS, item.Status, item.ErrorCode, item.ModelRef, usage, trace, item.RecordedAt, item.EventKey, item.Generation); err != nil {
			return err
		}
	}
	for _, item := range st.Findings {
		anchorsJSON, err := jsonText(item.Anchors)
		if err != nil {
			return err
		}
		var severitySource any
		if item.SeveritySource != nil {
			encoded, err := jsonText(item.SeveritySource)
			if err != nil {
				return err
			}
			severitySource = encoded
		}
		if _, err := tx.Exec(ctx, `INSERT INTO findings (id, run_id, attempt_id, case_id, origin, domain, rule_id, capability_id, object_ref, claim, relation, disposition_suggestion, unverified_reason, version, severity, severity_source, anchors, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
			ON CONFLICT (id) DO UPDATE SET rule_id=EXCLUDED.rule_id, capability_id=EXCLUDED.capability_id, object_ref=EXCLUDED.object_ref, version=EXCLUDED.version, disposition_suggestion=EXCLUDED.disposition_suggestion, unverified_reason=EXCLUDED.unverified_reason, severity=EXCLUDED.severity, severity_source=EXCLUDED.severity_source, anchors=EXCLUDED.anchors`,
			item.ID, item.RunID, item.AttemptID, item.CaseID, item.Origin, item.Domain, item.RuleID, item.CapabilityID, item.ObjectRef, item.Claim, item.Relation, item.DispositionSuggestion, item.UnverifiedReason, item.Version, item.Severity, severitySource, anchorsJSON, item.CreatedAt); err != nil {
			return err
		}
	}
	for _, item := range st.Evaluations {
		factIDs := item.FactIDs
		if factIDs == nil {
			factIDs = []string{}
		}
		objectRefs := item.ObjectRefs
		if objectRefs == nil {
			objectRefs = []string{}
		}
		factsJSON, err := jsonText(factIDs)
		if err != nil {
			return err
		}
		objectsJSON, err := jsonText(objectRefs)
		if err != nil {
			return err
		}
		var evidenceJSON any
		if len(item.Evidence) > 0 {
			encoded, err := jsonText(item.Evidence)
			if err != nil {
				return err
			}
			evidenceJSON = encoded
		}
		if _, err := tx.Exec(ctx, `INSERT INTO constraint_evaluations (id, run_id, attempt_id, case_id, domain, relation, rule_version, reason, finding_id, rule_id, capability_id, reason_code, fact_ids, object_refs, mandatory, evidence)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT (id) DO NOTHING`,
			item.ID, item.RunID, item.AttemptID, item.CaseID, item.Domain, item.Relation, item.RuleVersion, item.Reason, item.FindingID, item.RuleID, item.CapabilityID, item.ReasonCode, factsJSON, objectsJSON, item.Mandatory, evidenceJSON); err != nil {
			return err
		}
	}
	for _, item := range st.Decisions {
		if _, err := tx.Exec(ctx, `INSERT INTO decisions (id, finding_id, run_id, case_id, actor_id, actor_name, action, reason, old_disposition, new_disposition, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (id) DO NOTHING`,
			item.ID, item.FindingID, item.RunID, item.CaseID, item.ActorID, item.ActorName, item.Action, item.Reason, item.OldDisposition, item.NewDisposition, item.CreatedAt); err != nil {
			return err
		}
	}
	eventTable := runEventTableFor(owner)
	for _, item := range st.Events {
		if _, err := tx.Exec(ctx, `INSERT INTO `+eventTable+` (run_id, event_seq, event_type, state) VALUES ($1,$2,$3,$4)`, item.RunID, item.Seq, item.Type, item.State); err != nil {
			return err
		}
	}
	activityTable := activityTableFor(owner)
	for _, item := range st.Activity {
		if _, err := tx.Exec(ctx, `INSERT INTO `+activityTable+` (id, case_id, occurred_at, summary) VALUES ($1,$2,$3,$4)`, item.ID, item.CaseID, item.At, item.Summary); err != nil {
			return err
		}
	}
	for _, item := range st.Idempotency {
		if _, err := tx.Exec(ctx, `INSERT INTO idempotency (actor_id, idem_key, finding_id, decision_id, action, reason, expected_version) VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (actor_id,idem_key) DO NOTHING`,
			item.ActorID, item.Key, item.FindingID, item.DecisionID, item.Action, item.Reason, item.ExpectedVersion); err != nil {
			return err
		}
	}
	return nil
}

func syncSnapshots(ctx context.Context, tx pgx.Tx, items []RunSnapshot) error {
	for _, item := range items {
		if err := validatePartyContextSnapshot(item); err != nil {
			return err
		}
		raw, err := jsonText(item.DocumentIDs)
		if err != nil {
			return err
		}
		parsedRaw, err := jsonText(item.ParsedDocumentIDs)
		if err != nil {
			return err
		}
		var knowledge []byte
		if item.GovernedKnowledge != nil {
			if _, err := copyGovernedKnowledge(item); err != nil {
				return err
			}
			knowledge, err = json.Marshal(item.GovernedKnowledge)
			if err != nil {
				return err
			}
		}
		var partyContext []byte
		if len(item.PartyContext) > 0 {
			partyContext, err = json.Marshal(item.PartyContext)
			if err != nil {
				return err
			}
		}
		serviceObjectContext, err := encodeServiceObjectReviewContext(item)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO review_run_snapshots (run_id, document_ids, rule_set_hash, playbook_hash, model_ref, comparator_ref, model_configuration_id, parsed_document_ids, parse_selection_hash, parser_configuration_id, rule_pack_compiler_ref, rule_pack_id, rule_pack_version, rule_pack_hash, rule_pack_digest, rule_pack_snapshot, planner_configuration_id, planner_configuration, cloud_policy_snapshot, cloud_policy_digest, governed_knowledge, party_context, party_context_hash, service_object_context, service_object_context_hash) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25)`,
			item.RunID, raw, item.RuleSetHash, item.PlaybookHash, item.ModelRef, item.ComparatorRef, item.ModelConfigurationID,
			parsedRaw, item.ParseSelectionHash, item.ParserConfigurationID, item.RulePackCompilerRef,
			item.RulePackID, item.RulePackVersion, item.RulePackHash, item.RulePackDigest,
			[]byte(item.RulePackSnapshot), item.PlannerConfigurationID, plannerPayloadValue(item.PlannerConfiguration),
			[]byte(item.CloudPolicySnapshot), item.CloudPolicyDigest, knowledge, partyContext, item.PartyContextHash,
			serviceObjectContext, item.ServiceObjectContextHash); err != nil {
			return err
		}
	}
	return nil
}

func syncAudits(ctx context.Context, tx pgx.Tx, items []AuditRecord, owner string) error {
	table := auditTableFor(owner)
	for _, item := range items {
		raw, err := jsonText(item.CorrelationIDs)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+table+` (event_id, run_id, actor, seq, event_type, prev_hash, hash, correlation_ids) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			item.EventID, item.RunID, item.Actor, item.Seq, item.EventType, item.PrevHash, item.Hash, raw); err != nil {
			return err
		}
	}
	return nil
}

type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func loadSessions(ctx context.Context, q queryer) ([]session, error) {
	rows, err := q.Query(ctx, `SELECT token, actor_id, issuer, subject, tenant_id, org_id, audience, expires_at FROM sessions ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []session
	for rows.Next() {
		var item session
		if err := rows.Scan(&item.Token, &item.ActorID, &item.Issuer, &item.Subject, &item.TenantID, &item.OrgID, &item.Audience, &item.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadCases(ctx context.Context, q queryer) ([]Case, error) {
	rows, err := q.Query(ctx, `SELECT id, tenant_id, org_id, business_line, scenario, business_status, archived_at, title, contract_type, owner_id, member_ids, created_at, updated_at FROM cases ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Case
	for rows.Next() {
		var item Case
		var raw string
		if err := rows.Scan(&item.ID, &item.TenantID, &item.OwnerOrgID, &item.BusinessLine, &item.Scenario, &item.BusinessStatus,
			&item.ArchivedAt, &item.Title, &item.ContractType, &item.OwnerID, &raw, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &item.MemberIDs); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadDocuments(ctx context.Context, q queryer) ([]Document, error) {
	rows, err := q.Query(ctx, `SELECT id, tenant_id, org_id, case_id, role, filename, media_type, digest, size, origin, storage_backend, created_at FROM documents ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Document
	for rows.Next() {
		var item Document
		if err := rows.Scan(&item.ID, &item.TenantID, &item.OrgID, &item.CaseID, &item.Role, &item.Filename, &item.MediaType, &item.Digest, &item.Size, &item.Origin, &item.StorageBackend, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// parsedRow is one persisted parse artifact as recorded in its columns.
type parsedRow struct {
	id                   string
	selectionOrder       int64
	documentID           string
	originalDigest       string
	parserID             string
	parserVersion        string
	configHash           string
	normalizationVersion string
	normalizedDigest     *string
	irDigest             *string
	artifactDigest       string
	createdAt            string
	status               string
	payload              string
}

// decode returns the artifact only when its canonical payload agrees with every
// recorded column and with the original it belongs to.
func (row parsedRow) decode(byID map[string]Document) (ParsedDocument, error) {
	parsed, err := parsePayload(row.payload)
	if err != nil {
		return ParsedDocument{}, fmt.Errorf("parsed artifact %s: %w", row.id, err)
	}
	wantNormalized, wantIR := "", ""
	if row.normalizedDigest != nil {
		wantNormalized = *row.normalizedDigest
	}
	if row.irDigest != nil {
		wantIR = *row.irDigest
	}
	if parsed.ID != row.id || parsed.SelectionOrder != row.selectionOrder || parsed.DocumentID != row.documentID || parsed.OriginalDigest != row.originalDigest ||
		parsed.ParserID != row.parserID || parsed.ParserVersion != row.parserVersion || parsed.ConfigHash != row.configHash ||
		parsed.NormalizationVersion != row.normalizationVersion || parsed.NormalizedDigest != wantNormalized ||
		parsed.IRDigest != wantIR || parsed.ArtifactDigest != row.artifactDigest || parsed.CreatedAt != row.createdAt || parsed.Status != row.status {
		return ParsedDocument{}, fmt.Errorf("parsed artifact %s metadata does not match its canonical payload", row.id)
	}
	doc, ok := byID[row.documentID]
	if !ok {
		return ParsedDocument{}, fmt.Errorf("parsed artifact %s references a missing document", row.id)
	}
	if err := verifyParsed(parsed, doc); err != nil {
		return ParsedDocument{}, fmt.Errorf("parsed artifact %s integrity check: %w", row.id, err)
	}
	return parsed, nil
}

// unverified is the placeholder kept for an artifact whose payload could not be
// trusted. It carries only the recorded identity and never the stored text, so it
// can neither pass verifyParsed nor be mistaken for a usable artifact.
func (row parsedRow) unverified() ParsedDocument {
	parsed := ParsedDocument{
		ID: row.id, SelectionOrder: row.selectionOrder, DocumentID: row.documentID,
		OriginalDigest: row.originalDigest, ParserID: row.parserID, ParserVersion: row.parserVersion,
		ConfigHash: row.configHash, NormalizationVersion: row.normalizationVersion,
		ArtifactDigest: row.artifactDigest, CreatedAt: row.createdAt, Status: row.status,
	}
	if row.normalizedDigest != nil {
		parsed.NormalizedDigest = *row.normalizedDigest
	}
	if row.irDigest != nil {
		parsed.IRDigest = *row.irDigest
	}
	return parsed
}

// loadParsedDocuments reads every persisted parse artifact. A strict load is the
// startup and write boundary and rejects an artifact whose payload disagrees with
// its recorded identity, so corrupted bytes can never be served or written over. A
// lenient load backs per-request reads: it keeps such an artifact as an unverified
// placeholder instead of failing the whole request, so the owning document reports a
// parse failure while every identity check downstream keeps rejecting the artifact.
func loadParsedDocuments(ctx context.Context, q queryer, documents []Document, strict bool) ([]ParsedDocument, error) {
	byID := make(map[string]Document, len(documents))
	for _, doc := range documents {
		byID[doc.ID] = doc
	}
	rows, err := q.Query(ctx, `SELECT id, selection_order, document_id, original_digest, parser_id, parser_version, config_hash, normalization_version, normalized_digest, ir_digest, artifact_digest, created_at, status, payload FROM parsed_documents ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ParsedDocument
	for rows.Next() {
		var row parsedRow
		if err := rows.Scan(&row.id, &row.selectionOrder, &row.documentID, &row.originalDigest, &row.parserID, &row.parserVersion, &row.configHash,
			&row.normalizationVersion, &row.normalizedDigest, &row.irDigest, &row.artifactDigest, &row.createdAt, &row.status, &row.payload); err != nil {
			return nil, err
		}
		parsed, err := row.decode(byID)
		if err != nil {
			if strict {
				return nil, err
			}
			parsed = row.unverified()
		}
		out = append(out, parsed)
	}
	return out, rows.Err()
}

func loadRuns(ctx context.Context, q queryer) ([]Run, error) {
	rows, err := q.Query(ctx, `SELECT id, case_id, state, model_ref, partial_reason, failure_message, created_at, updated_at FROM review_runs ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var item Run
		if err := rows.Scan(&item.ID, &item.CaseID, &item.State, &item.ModelRef, &item.PartialReason, &item.FailureMessage, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadAttempts(ctx context.Context, q queryer) ([]Attempt, error) {
	rows, err := q.Query(ctx, `SELECT id, run_id, ordinal, retry_of_attempt_id, state, accepted_at, started_at, finished_at, input_hash, output_hash, error_code, history_kind, actor_id, rule_pack_snapshot, rule_pack_hash, rule_pack_snapshot_digest, rule_pack_compiler_ref, rule_pack_id, rule_pack_version, rule_pack_pointer_revision, planner_configuration_id, planner_configuration FROM review_attempts ORDER BY run_id, ordinal`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		var item Attempt
		var plannerPayload []byte
		if err := rows.Scan(&item.ID, &item.RunID, &item.Ordinal, &item.RetryOfAttemptID, &item.State, &item.AcceptedAt, &item.StartedAt, &item.FinishedAt, &item.InputHash, &item.OutputHash, &item.ErrorCode, &item.HistoryKind, &item.ActorID, &item.RulePackSnapshot, &item.RulePackHash, &item.RulePackSnapshotDigest, &item.RulePackCompilerRef,
			&item.RulePackID, &item.RulePackVersion, &item.RulePackPointerRevision, &item.PlannerConfigurationID, &plannerPayload); err != nil {
			return nil, err
		}
		if len(plannerPayload) > 0 {
			item.PlannerConfiguration = append(json.RawMessage(nil), plannerPayload...)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadAttemptInputs(ctx context.Context, q queryer) ([]AttemptInput, error) {
	rows, err := q.Query(ctx, `SELECT attempt_id, run_id, input_hash, frozen_at, documents FROM review_attempt_inputs ORDER BY attempt_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttemptInput
	for rows.Next() {
		var item AttemptInput
		var raw string
		if err := rows.Scan(&item.AttemptID, &item.RunID, &item.InputHash, &item.FrozenAt, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &item.Documents); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadAttemptParseSelections(ctx context.Context, q queryer) ([]AttemptParseSelection, error) {
	rows, err := q.Query(ctx, `SELECT attempt_id, run_id, document_id, original_digest, parsed_document_id, selection_order, parser_id, parser_version, config_hash, normalization_version, artifact_digest FROM review_attempt_parse_selections ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttemptParseSelection
	for rows.Next() {
		var item AttemptParseSelection
		if err := rows.Scan(&item.AttemptID, &item.RunID, &item.DocumentID, &item.OriginalDigest, &item.ParsedDocumentID,
			&item.SelectionOrder, &item.ParserID, &item.ParserVersion, &item.ConfigHash, &item.NormalizationVersion, &item.ArtifactDigest); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadExecutionEvents(ctx context.Context, q queryer) ([]ExecutionEvent, error) {
	rows, err := q.Query(ctx, `SELECT attempt_id, event_seq, span_id, parent_span_id, node_kind, phase, event_type, occurred_at, duration_ms, status, error_code, model_ref, usage, trace, recorded_at, event_key, generation FROM execution_events ORDER BY attempt_id, event_seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExecutionEvent
	for rows.Next() {
		var item ExecutionEvent
		var raw, traceJSON *string
		if err := rows.Scan(&item.AttemptID, &item.Seq, &item.SpanID, &item.ParentSpanID, &item.NodeKind, &item.Phase, &item.EventType, &item.At, &item.DurationMS, &item.Status, &item.ErrorCode, &item.ModelRef, &raw, &traceJSON, &item.RecordedAt, &item.EventKey, &item.Generation); err != nil {
			return nil, err
		}
		if raw != nil {
			var usage UsageObservation
			if err := json.Unmarshal([]byte(*raw), &usage); err != nil {
				return nil, err
			}
			item.Usage = &usage
		}
		if traceJSON != nil {
			var trace execution.ModelCallTrace
			if err := json.Unmarshal([]byte(*traceJSON), &trace); err != nil {
				return nil, err
			}
			item.Trace = &trace
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadFindings(ctx context.Context, q queryer) ([]Finding, error) {
	rows, err := q.Query(ctx, `SELECT id, run_id, attempt_id, case_id, origin, domain, rule_id, capability_id, object_ref, claim, relation, disposition_suggestion, unverified_reason, version, severity, severity_source, anchors, created_at FROM findings ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Finding
	for rows.Next() {
		var item Finding
		var severitySourceJSON, anchorsJSON []byte
		if err := rows.Scan(&item.ID, &item.RunID, &item.AttemptID, &item.CaseID, &item.Origin, &item.Domain, &item.RuleID, &item.CapabilityID, &item.ObjectRef, &item.Claim, &item.Relation, &item.DispositionSuggestion, &item.UnverifiedReason, &item.Version, &item.Severity, &severitySourceJSON, &anchorsJSON, &item.CreatedAt); err != nil {
			return nil, err
		}
		if len(severitySourceJSON) > 0 {
			var source FindingSeveritySource
			if err := json.Unmarshal(severitySourceJSON, &source); err != nil {
				return nil, err
			}
			item.SeveritySource = &source
		}
		if err := json.Unmarshal(anchorsJSON, &item.Anchors); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func loadEvaluations(ctx context.Context, q queryer) ([]ConstraintEval, error) {
	rows, err := q.Query(ctx, `SELECT id, run_id, attempt_id, case_id, domain, relation, rule_version, reason, finding_id, rule_id, capability_id, reason_code, fact_ids, object_refs, mandatory, evidence FROM constraint_evaluations ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConstraintEval
	for rows.Next() {
		var item ConstraintEval
		var factIDs, objectRefs string
		var evidenceJSON []byte
		if err := rows.Scan(&item.ID, &item.RunID, &item.AttemptID, &item.CaseID, &item.Domain, &item.Relation, &item.RuleVersion, &item.Reason, &item.FindingID, &item.RuleID, &item.CapabilityID, &item.ReasonCode, &factIDs, &objectRefs, &item.Mandatory, &evidenceJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(factIDs), &item.FactIDs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(objectRefs), &item.ObjectRefs); err != nil {
			return nil, err
		}
		if len(evidenceJSON) > 0 {
			if err := json.Unmarshal(evidenceJSON, &item.Evidence); err != nil {
				return nil, err
			}
		}
		if len(item.Evidence) == 0 {
			item.Evidence = nil
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadDecisions(ctx context.Context, q queryer) ([]Decision, error) {
	rows, err := q.Query(ctx, `SELECT id, finding_id, run_id, case_id, actor_id, actor_name, action, reason, old_disposition, new_disposition, created_at FROM decisions ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Decision
	for rows.Next() {
		var item Decision
		if err := rows.Scan(&item.ID, &item.FindingID, &item.RunID, &item.CaseID, &item.ActorID, &item.ActorName, &item.Action, &item.Reason, &item.OldDisposition, &item.NewDisposition, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadEvents(ctx context.Context, q queryer) ([]Event, error) {
	rows, err := q.Query(ctx, `SELECT run_id,event_seq,event_type,state FROM run_events UNION ALL SELECT run_id,event_seq,event_type,state FROM business_run_events ORDER BY run_id,event_seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var item Event
		if err := rows.Scan(&item.RunID, &item.Seq, &item.Type, &item.State); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadActivity(ctx context.Context, q queryer) ([]Activity, error) {
	rows, err := q.Query(ctx, `SELECT id,case_id,occurred_at,summary FROM activity UNION ALL SELECT id,case_id,occurred_at,summary FROM document_activity UNION ALL SELECT id,case_id,occurred_at,summary FROM execution_activity ORDER BY occurred_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Activity
	for rows.Next() {
		var item Activity
		if err := rows.Scan(&item.ID, &item.CaseID, &item.At, &item.Summary); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadIdempotency(ctx context.Context, q queryer) ([]Idem, error) {
	rows, err := q.Query(ctx, `SELECT actor_id, idem_key, finding_id, decision_id, action, reason, expected_version FROM idempotency ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Idem
	for rows.Next() {
		var item Idem
		if err := rows.Scan(&item.ActorID, &item.Key, &item.FindingID, &item.DecisionID, &item.Action, &item.Reason, &item.ExpectedVersion); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadSnapshots(ctx context.Context, q queryer) ([]RunSnapshot, error) {
	rows, err := q.Query(ctx, `SELECT run_id, document_ids, rule_set_hash, playbook_hash, model_ref, comparator_ref, model_configuration_id, parsed_document_ids, parse_selection_hash, parser_configuration_id, rule_pack_compiler_ref, rule_pack_id, rule_pack_version, rule_pack_hash, rule_pack_digest, rule_pack_snapshot, planner_configuration_id, planner_configuration, cloud_policy_snapshot, cloud_policy_digest, governed_knowledge, party_context, party_context_hash, service_object_context, service_object_context_hash FROM review_run_snapshots ORDER BY run_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunSnapshot
	for rows.Next() {
		var item RunSnapshot
		var raw, parsedRaw string
		var snapshot, plannerPayload, policySnapshot, knowledge, partyContext, serviceObjectContext []byte
		if err := rows.Scan(&item.RunID, &raw, &item.RuleSetHash, &item.PlaybookHash, &item.ModelRef,
			&item.ComparatorRef, &item.ModelConfigurationID, &parsedRaw, &item.ParseSelectionHash, &item.ParserConfigurationID, &item.RulePackCompilerRef,
			&item.RulePackID, &item.RulePackVersion, &item.RulePackHash, &item.RulePackDigest, &snapshot, &item.PlannerConfigurationID, &plannerPayload,
			&policySnapshot, &item.CloudPolicyDigest, &knowledge, &partyContext, &item.PartyContextHash,
			&serviceObjectContext, &item.ServiceObjectContextHash); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &item.DocumentIDs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(parsedRaw), &item.ParsedDocumentIDs); err != nil {
			return nil, err
		}
		if len(snapshot) > 0 {
			item.RulePackSnapshot = append([]byte(nil), snapshot...)
		}
		if len(plannerPayload) > 0 {
			item.PlannerConfiguration = append(json.RawMessage(nil), plannerPayload...)
		}
		if len(policySnapshot) > 0 {
			item.CloudPolicySnapshot = append(json.RawMessage(nil), policySnapshot...)
		}
		item.PartyContext, err = decodePartyContext(partyContext, item.PartyContextHash)
		if err != nil {
			return nil, fmt.Errorf("invalid party context snapshot for run %s: %w", item.RunID, err)
		}
		item.ServiceObjectContext, err = decodeServiceObjectReviewContext(serviceObjectContext, item.ServiceObjectContextHash)
		if err != nil {
			return nil, fmt.Errorf("invalid service object context snapshot for run %s: %w", item.RunID, err)
		}
		item.GovernedKnowledge, err = decodeGovernedKnowledge(knowledge)
		if err != nil {
			return nil, err
		}
		if _, err := copyGovernedKnowledge(item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadAudits(ctx context.Context, q queryer) ([]AuditRecord, error) {
	rows, err := q.Query(ctx, `SELECT event_id,run_id,actor,seq,event_type,prev_hash,hash,correlation_ids FROM audit_events UNION ALL SELECT event_id,run_id,actor,seq,event_type,prev_hash,hash,correlation_ids FROM business_audit_events ORDER BY seq,event_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditRecord
	for rows.Next() {
		var item AuditRecord
		var raw string
		if err := rows.Scan(&item.EventID, &item.RunID, &item.Actor, &item.Seq, &item.EventType, &item.PrevHash, &item.Hash, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &item.CorrelationIDs); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// plannerPayloadValue persists the exact frozen planner configuration as bytea,
// or nil when a legacy run predates the field.
func plannerPayloadValue(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

func snapshotValue(item RunSnapshot) audit.Snapshot {
	return audit.Snapshot{
		DocumentIDs:              append([]string(nil), item.DocumentIDs...),
		ParsedDocumentIDs:        append([]string(nil), item.ParsedDocumentIDs...),
		PartyContextHash:         item.PartyContextHash,
		ServiceObjectContextHash: item.ServiceObjectContextHash,
		ParseSelectionHash:       item.ParseSelectionHash, ParserConfigurationID: item.ParserConfigurationID,
		RuleSetHash: item.RuleSetHash, PlaybookHash: item.PlaybookHash,
		ModelRef: item.ModelRef, ModelConfigurationID: item.ModelConfigurationID, ComparatorRef: item.ComparatorRef,
		RulePackCompilerRef: item.RulePackCompilerRef,
		RulePackID:          item.RulePackID, RulePackVersion: item.RulePackVersion,
		RulePackHash: item.RulePackHash, RulePackDigest: item.RulePackDigest,
		PlannerConfigurationID: item.PlannerConfigurationID,
	}
}

func jsonText(v any) (string, error) {
	if v == nil {
		return "[]", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func quoteIdent(name string) (string, error) {
	if !identRE.MatchString(name) {
		return "", fmt.Errorf("invalid sql identifier %q", name)
	}
	return `"` + name + `"`, nil
}

func withDatabase(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}

func duplicateDatabase(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "42P04" ||
		(pgErr.Code == "23505" && pgErr.ConstraintName == "pg_database_datname_index"))
}
