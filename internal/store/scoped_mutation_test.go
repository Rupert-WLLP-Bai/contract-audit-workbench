package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/config"
	"contractaudit/internal/fault"
)

type durableRoleEnvironment struct {
	harness   *Store
	dir       string
	roleDSNs  map[string]string
	roleNames []string
	stores    []*Store
}

func newDurableRoleEnvironment(t *testing.T) (*durableRoleEnvironment, bool) {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	migratorDSN := strings.TrimSpace(os.Getenv("MIGRATOR_DATABASE_URL"))
	if baseDSN == "" || migratorDSN == "" {
		return nil, false
	}
	harness, err := OpenTest(t.TempDir(), baseDSN)
	if err != nil {
		t.Fatalf("open isolated durable test schema: %v", err)
	}
	env := &durableRoleEnvironment{harness: harness, dir: harness.dir, roleDSNs: make(map[string]string, 5)}
	cleanupRegistered := false
	t.Cleanup(func() {
		if !cleanupRegistered {
			if err := harness.Close(); err != nil {
				t.Errorf("close incomplete isolated schema setup: %v", err)
			}
		}
	})
	t.Setenv("APP_ENV", config.ModeTest)
	t.Setenv("TEST_DATABASE_URL", harness.sel.TestDSN)
	t.Setenv("MIGRATOR_DATABASE_URL", migratorDSN)
	t.Setenv("DATABASE_SCHEMA", harness.schema)
	for _, service := range []string{"core", "document", "execution", "model", "tool"} {
		roleID, err := newID("")
		if err != nil {
			t.Fatal(err)
		}
		passwordA, err := newID("")
		if err != nil {
			t.Fatal(err)
		}
		passwordB, err := newID("")
		if err != nil {
			t.Fatal(err)
		}
		roleName := "it_" + service + "_" + roleID
		password := passwordA + passwordB
		roleDSN, err := dsnWithCredentials(baseDSN, roleName, password)
		if err != nil {
			t.Fatalf("build isolated %s role DSN: %v", service, err)
		}
		env.roleNames = append(env.roleNames, roleName)
		env.roleDSNs[service] = roleDSN
		t.Setenv(strings.ToUpper(service)+"_DATABASE_URL", roleDSN)
	}
	cleanupRegistered = true
	t.Cleanup(func() {
		for i := len(env.stores) - 1; i >= 0; i-- {
			if err := env.stores[i].Close(); err != nil {
				t.Errorf("close isolated %s store: %v", env.stores[i].serviceOwner, err)
			}
		}
		if schema, err := quoteIdent(harness.schema); err == nil {
			if _, err := harness.pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
				t.Errorf("drop isolated durable schema: %v", err)
			}
		}
		for _, role := range env.roleNames {
			if identifier, err := quoteIdent(role); err == nil {
				if _, err := harness.pool.Exec(context.Background(), "DROP ROLE IF EXISTS "+identifier); err != nil {
					t.Errorf("drop isolated database role: %v", err)
				}
			}
		}
		if err := harness.Close(); err != nil {
			t.Errorf("close isolated schema harness: %v", err)
		}
	})
	if err := MigrateServiceRoles(context.Background()); err != nil {
		t.Fatalf("provision isolated service roles: %v", err)
	}
	return env, true
}

func dsnWithCredentials(raw, username, password string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	parsed.User = url.UserPassword(username, password)
	return parsed.String(), nil
}

func (env *durableRoleEnvironment) openRole(t *testing.T, role string) *Store {
	t.Helper()
	service := role
	if role == "worker" {
		service = "execution"
	}
	raw, ok := env.roleDSNs[service]
	if !ok {
		t.Fatalf("isolated database role %q is unavailable", service)
	}
	selection, err := config.Select(config.Environ{AppEnv: config.ModeTest, TestDatabaseURL: raw})
	if err != nil {
		t.Fatalf("select isolated %s role: %v", service, err)
	}
	store, err := OpenApplicationSelection(env.dir, selection)
	if err != nil {
		t.Fatalf("open isolated %s role: %v", service, err)
	}
	env.stores = append(env.stores, store)
	if err := store.UseServiceOwner(role); err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateServiceDatabaseRole(context.Background(), role); err != nil {
		t.Fatalf("validate isolated %s role: %v", service, err)
	}
	return store
}

// durableCaseFixture uses the real Core and Document owners for case setup.
type durableCaseFixture struct {
	environment *durableRoleEnvironment
	core        *Store
	document    *Store
	admin       accounts.User
	caseID      string
	dir         string
}

func newDurableCaseFixture(t *testing.T) (durableCaseFixture, bool) {
	t.Helper()
	environment, ok := newDurableRoleEnvironment(t)
	if !ok {
		return durableCaseFixture{}, false
	}
	core := environment.openRole(t, "core")
	document := environment.openRole(t, "document")
	admin, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo admin missing")
	}
	caseID, err := core.CreateCase(admin, "独立服务审查", "procurement_ict")
	if err != nil {
		t.Fatalf("core case creation failed: %v", err)
	}
	for _, item := range []struct{ role, body string }{
		{"procurement", "采购文件要求质保36个月。合同总价为100万元。"},
		{"draft_contract", "拟签合同约定质保24个月。合同总价为120万元。"},
	} {
		if _, err := document.AddDocument(context.Background(), admin, caseID, item.role, item.role+".txt", "text/plain", []byte(item.body)); err != nil {
			t.Fatalf("document service write failed for %s: %v", item.role, err)
		}
	}
	return durableCaseFixture{environment: environment, core: core, document: document, admin: admin, caseID: caseID, dir: environment.dir}, true
}

// durableFixture is the real independent-service fixture: Core owns the case,
// Document owns materials, and Execution owns acceptance and attempts.
type durableFixture struct {
	environment *durableRoleEnvironment
	core        *Store
	document    *Store
	execution   *Store
	admin       accounts.User
	actor       accounts.User
	caseID      string
	runID       string
	attemptID   string
	jobID       string
	claim       JobClaim
	dir         string
}

func newDurableFixture(t *testing.T) (durableFixture, bool) {
	t.Helper()
	admin, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo admin missing")
	}
	return newDurableFixtureAs(t, admin)
}

func newDurableFixtureAs(t *testing.T, actor accounts.User) (durableFixture, bool) {
	t.Helper()
	setup, ok := newDurableCaseFixture(t)
	if !ok {
		return durableFixture{}, false
	}
	execution := setup.environment.openRole(t, "execution")
	if actor.ID != setup.admin.ID {
		if err := setup.core.AddMember(setup.admin, setup.caseID, actor.ID); err != nil {
			t.Fatalf("core membership setup failed: %v", err)
		}
	}
	execution.UseDocumentPreparer(setup.document)
	execution.UseModels(&frozenOCRModel{})
	accepted, err := execution.StartReviewAsync(context.Background(), actor, setup.caseID)
	if err != nil {
		t.Fatalf("durable acceptance failed: %v", err)
	}
	claim, err := execution.ClaimNextJob(context.Background(), "durable-worker", time.Minute)
	if err != nil {
		t.Fatalf("durable claim failed: %v", err)
	}
	if claim.JobID != accepted.JobID {
		t.Fatalf("claimed %q, want %q", claim.JobID, accepted.JobID)
	}
	if _, installed := execution.claimForAttempt(accepted.AttemptID); !installed {
		t.Fatal("the durable claim was not registered for the attempt")
	}
	return durableFixture{
		environment: setup.environment, core: setup.core, document: setup.document, execution: execution,
		admin: setup.admin, actor: actor, caseID: setup.caseID, runID: accepted.RunID,
		attemptID: accepted.AttemptID, jobID: accepted.JobID, claim: claim, dir: setup.dir,
	}, true
}

func newDurableTelecomFixture(t *testing.T) (durableFixture, bool) {
	t.Helper()
	environment, ok := newDurableRoleEnvironment(t)
	if !ok {
		return durableFixture{}, false
	}
	core := environment.openRole(t, "core")
	document := environment.openRole(t, "document")
	admin, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo admin missing")
	}
	caseID, err := core.CreateCaseWithBusiness(admin, "隔离的电信报告归档", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatalf("core service could not create the telecom case: %v", err)
	}
	if _, err := document.Generate(context.Background(), admin, caseID, telecomTemplateID, validTelecomTemplateFields()); err != nil {
		t.Fatalf("document service could not generate the draft contract: %v", err)
	}
	if _, err := document.AddDocument(context.Background(), admin, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件未提供任何合同阈值或审批依据。")); err != nil {
		t.Fatalf("document service could not add the procurement source: %v", err)
	}
	execution := environment.openRole(t, "execution")
	execution.UseDocumentPreparer(document)
	execution.UseModels(&frozenOCRModel{})
	accepted, err := execution.StartReviewAsync(context.Background(), admin, caseID)
	if err != nil {
		t.Fatalf("durable telecom acceptance failed: %v", err)
	}
	claim, err := execution.ClaimNextJob(context.Background(), "durable-telecom-worker", time.Minute)
	if err != nil {
		t.Fatalf("durable telecom claim failed: %v", err)
	}
	if claim.JobID != accepted.JobID {
		t.Fatalf("claimed telecom job %q, want %q", claim.JobID, accepted.JobID)
	}
	return durableFixture{
		environment: environment, core: core, document: document, execution: execution,
		admin: admin, actor: admin, caseID: caseID, runID: accepted.RunID,
		attemptID: accepted.AttemptID, jobID: accepted.JobID, claim: claim, dir: environment.dir,
	}, true
}

type durableMutationState struct {
	Attempt         Attempt
	Run             Run
	Job             Job
	ExecutionEvents []ExecutionEvent
	RunEvents       []Event
	Audits          []AuditRecord
}

func durableSnapshot(t *testing.T, store *Store, claim JobClaim) state {
	t.Helper()
	snapshot, err := store.snapshotScoped(context.Background(), claim.scope())
	if err != nil {
		t.Fatalf("read durable service state: %v", err)
	}
	return snapshot
}

func durableMutationSnapshot(t *testing.T, fixture durableFixture, claim JobClaim) durableMutationState {
	t.Helper()
	snapshot := durableSnapshot(t, fixture.execution, claim)
	attempt, ok := snapshot.attemptByID(claim.AttemptID)
	if !ok {
		t.Fatalf("attempt %q was not persisted", claim.AttemptID)
	}
	run, ok := snapshot.runByID(claim.RunID)
	if !ok {
		t.Fatalf("run %q was not persisted", claim.RunID)
	}
	var job Job
	jobFound := false
	for _, candidate := range snapshot.Jobs {
		if candidate.ID == claim.JobID {
			job, jobFound = candidate, true
			break
		}
	}
	if !jobFound {
		t.Fatalf("job %q was not persisted", claim.JobID)
	}
	out := durableMutationState{Attempt: attempt, Run: run, Job: job, RunEvents: snapshot.runEvents(claim.RunID)}
	for _, event := range snapshot.ExecutionEvents {
		if event.AttemptID == claim.AttemptID {
			out.ExecutionEvents = append(out.ExecutionEvents, event)
		}
	}
	for _, audit := range snapshot.Audits {
		if audit.RunID == claim.RunID {
			out.Audits = append(out.Audits, audit)
		}
	}
	return out
}

func durableAttemptVersion(t *testing.T, store *Store, claim JobClaim) int64 {
	t.Helper()
	ctx := context.Background()
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, claim.scope()); err != nil {
		t.Fatal(err)
	}
	var version int64
	if err := tx.QueryRow(ctx, `SELECT entity_version FROM review_attempts WHERE id=$1 AND run_id=$2`, claim.AttemptID, claim.RunID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return version
}

func expireDurableClaim(t *testing.T, fixture durableFixture) {
	t.Helper()
	ctx := context.Background()
	tx, err := fixture.execution.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, fixture.claim.scope()); err != nil {
		t.Fatal(err)
	}
	tag, err := tx.Exec(ctx, `UPDATE worker_jobs SET lease_until=clock_timestamp()-interval '1 minute', ready_at=clock_timestamp()-interval '1 second', updated_at=$3
		WHERE id=$1 AND generation=$2 AND state='running'`, fixture.claim.JobID, fixture.claim.Generation, stamp())
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expired %d job rows, want one", tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestDurableScopedMutationStartsAndFinalizesWithinClaim drives the service-owner
// path for real: the start transition and the terminal transition run through the
// bounded adapter under the installed durable claim, and the persisted output
// hash equals the canonical hash over the whole state.
func TestDurableScopedMutationStartsAndFinalizesWithinClaim(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	ctx := context.Background()
	versionBefore := durableAttemptVersion(t, fixture.execution, fixture.claim)
	if err := fixture.execution.RunClaimedJob(ctx, fixture.claim); err != nil {
		t.Fatalf("claimed execution failed: %v", err)
	}
	full := durableSnapshot(t, fixture.execution, fixture.claim)
	finished, found := full.attemptByID(fixture.attemptID)
	if !found || finished.State != "completed" || finished.StartedAt == nil || finished.FinishedAt == nil || finished.OutputHash == nil {
		t.Fatalf("terminal transition did not persist exactly: %+v", finished)
	}
	if finished.HistoryKind != "frozen" || finished.InputHash == nil {
		t.Fatalf("attempt input history is not frozen: %+v", finished)
	}
	input, found := full.attemptInput(fixture.attemptID)
	if !found || input.RunID != fixture.runID || input.InputHash != *finished.InputHash || len(input.Documents) != 2 {
		t.Fatalf("frozen attempt input was not persisted completely: %+v", input)
	}
	runSnapshot := full.snapshotOf(fixture.runID)
	wantInputHash, err := frozenInputHash(runSnapshot, input.Documents)
	if err != nil {
		t.Fatal(err)
	}
	if input.InputHash != wantInputHash || !frozenInputMatchesSnapshot(runSnapshot, input.Documents) ||
		!attemptSelectionsMatchFrozen(full.parseSelectionsForAttempt(fixture.attemptID), input.Documents) {
		t.Fatalf("persisted frozen input does not match its snapshot or parse selections: %+v", input)
	}
	findings := full.findingsForAttempt(fixture.runID, fixture.attemptID)
	evaluations := full.evaluationsForAttempt(fixture.runID, fixture.attemptID)
	if len(findings) == 0 || len(evaluations) == 0 {
		t.Fatalf("deterministic review results were not persisted: findings=%d evaluations=%d", len(findings), len(evaluations))
	}
	wantOutputHash, err := attemptOutputHash(full, fixture.runID, fixture.attemptID, "")
	if err != nil {
		t.Fatal(err)
	}
	if *finished.OutputHash != wantOutputHash {
		t.Fatalf("persisted output hash %q differs from canonical %q", *finished.OutputHash, wantOutputHash)
	}
	run, _ := full.runByID(fixture.runID)
	job := durableMutationSnapshot(t, fixture, fixture.claim).Job
	if job.State != "completed" {
		t.Fatalf("terminal job state was not persisted: %+v", job)
	}
	versionAfter := durableAttemptVersion(t, fixture.execution, fixture.claim)
	if versionAfter != versionBefore+3 {
		t.Fatalf("attempt version did not advance once for start, freeze, and finish: before=%d after=%d", versionBefore, versionAfter)
	}
	executionEvents := full.executionEvents(fixture.attemptID)
	if len(executionEvents) == 0 || executionEvents[len(executionEvents)-1].EventType != "attempt_finished" ||
		executionEvents[len(executionEvents)-1].Status != finished.State {
		t.Fatalf("canonical attempt event history is incomplete: %+v", executionEvents)
	}
	for i := 1; i < len(executionEvents); i++ {
		if executionEvents[i].Seq != executionEvents[i-1].Seq+1 {
			t.Fatalf("attempt event history is not in canonical contiguous order: %+v", executionEvents)
		}
	}
	runEvents := full.runEvents(fixture.runID)
	if len(runEvents) == 0 || runEvents[len(runEvents)-1].State != run.State {
		t.Fatalf("run event history does not match persisted run state: run=%+v events=%+v", run, runEvents)
	}
	for i := 1; i < len(runEvents); i++ {
		if runEvents[i].Seq != runEvents[i-1].Seq+1 {
			t.Fatalf("run event history is not in canonical contiguous order: %+v", runEvents)
		}
	}
	frozenAudit := false
	for _, audit := range full.Audits {
		if audit.RunID == fixture.runID && audit.EventType == "attempt_input_frozen" {
			frozenAudit = true
			break
		}
	}
	if !frozenAudit {
		t.Fatalf("frozen input audit history is missing: %+v", full.Audits)
	}
}
func TestCompletedJobDeltaIgnoresLocationDifferences(t *testing.T) {
	zone := time.FixedZone("host-local", 8*60*60)
	heartbeat := time.Date(2026, 10, 22, 19, 17, 44, 123000000, zone)
	previous := state{Jobs: []Job{{ID: "job_completed", State: "completed", Heartbeat: &heartbeat}}}
	previous.normalize()

	next, err := previous.clone()
	if err != nil {
		t.Fatalf("clone persisted state: %v", err)
	}
	if reflect.DeepEqual(previous.Jobs[0], next.Jobs[0]) {
		t.Fatal("test fixture did not produce a different time.Location during JSON cloning")
	}
	if next.Jobs[0].Heartbeat == nil || !next.Jobs[0].Heartbeat.Equal(heartbeat) || next.Jobs[0].LeaseUntil != nil {
		t.Fatalf("clone changed completed job timestamps: %+v", next.Jobs[0])
	}
	old, changed, err := stateDelta(previous, next)
	if err != nil {
		t.Fatalf("calculate state delta: %v", err)
	}
	if len(old.Jobs) != 0 || len(changed.Jobs) != 0 {
		t.Fatalf("JSON clone created a false worker_jobs delta: old=%+v changed=%+v", old.Jobs, changed.Jobs)
	}
}

// TestDurableCoreCreateCaseLeavesCompletedWorkerJobUnchanged keeps a real
// completed worker row in the shared database while Core performs an unrelated
// write. A false delta would either trip the service-owner guard or rewrite the
// job row, so the test checks both the API result and PostgreSQL row version.
func TestDurableCoreCreateCaseLeavesCompletedWorkerJobUnchanged(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	ctx := context.Background()
	if err := fixture.execution.RunClaimedJob(ctx, fixture.claim); err != nil {
		t.Fatalf("claimed execution failed: %v", err)
	}
	job := durableMutationSnapshot(t, fixture, fixture.claim).Job
	if job.State != "completed" || job.Heartbeat == nil {
		t.Fatalf("expected a completed job with its persisted heartbeat: %+v", job)
	}
	var before, beforeXmin string
	if err := fixture.environment.harness.pool.QueryRow(ctx,
		"SELECT to_jsonb(job)::text, xmin::text FROM worker_jobs AS job WHERE id=$1", fixture.jobID,
	).Scan(&before, &beforeXmin); err != nil {
		t.Fatalf("read completed job before Core write: %v", err)
	}
	if _, err := fixture.core.CreateCaseWithBusiness(fixture.admin, "worker完成后由Core新增案件", "service", "ENT", "telecom_dedicated_line"); err != nil {
		t.Fatalf("Core could not create a case while an unrelated completed worker job existed: %v", err)
	}
	var after, afterXmin string
	if err := fixture.environment.harness.pool.QueryRow(ctx,
		"SELECT to_jsonb(job)::text, xmin::text FROM worker_jobs AS job WHERE id=$1", fixture.jobID,
	).Scan(&after, &afterXmin); err != nil {
		t.Fatalf("read completed job after Core write: %v", err)
	}
	if before != after || beforeXmin != afterXmin {
		t.Fatalf("Core's unrelated case creation changed the completed worker job: before=%s (xmin %s), after=%s (xmin %s)",
			before, beforeXmin, after, afterXmin)
	}
}

// TestDurableCancelAuditFailureRollsBack proves the cancellation is rolled back
// when its audit insert cannot commit: a trigger rejects the audit row, the
// cancelled attempt and its live claim survive unchanged.
func TestDurableCancelAuditFailureRollsBack(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	ctx := context.Background()
	table := auditTableFor(fixture.execution.serviceOwner)
	inject := fixture.environment.harness
	dropTrigger := `DROP TRIGGER IF EXISTS test_block_cancel_audit ON ` + table
	dropFunction := `DROP FUNCTION IF EXISTS reject_test_audit()`
	if _, err := inject.pool.Exec(ctx, dropTrigger); err != nil {
		t.Fatal(err)
	}
	if _, err := inject.pool.Exec(ctx, dropFunction); err != nil {
		t.Fatal(err)
	}
	if _, err := inject.pool.Exec(ctx, `CREATE FUNCTION reject_test_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test audit failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	trigger := `CREATE TRIGGER test_block_cancel_audit BEFORE INSERT ON ` + table + ` FOR EACH ROW WHEN (NEW.event_type = 'review_cancelled') EXECUTE FUNCTION reject_test_audit()`
	if _, err := inject.pool.Exec(ctx, trigger); err != nil {
		t.Fatal(err)
	}
	before := durableMutationSnapshot(t, fixture, fixture.claim)
	err := fixture.execution.cancelAttemptNarrow(ctx, fixture.admin, fixture.caseID, fixture.runID, fixture.attemptID)
	if err == nil || !strings.Contains(err.Error(), "test audit failure") {
		t.Fatalf("cancellation did not fail at the injected audit write: %v", err)
	}
	afterFailure := durableMutationSnapshot(t, fixture, fixture.claim)
	if !reflect.DeepEqual(afterFailure, before) {
		t.Fatalf("audit failure partially committed cancellation state:\nbefore=%+v\nafter=%+v", before, afterFailure)
	}
	if _, err := inject.pool.Exec(ctx, dropTrigger); err != nil {
		t.Fatal(err)
	}
	if _, err := inject.pool.Exec(ctx, dropFunction); err != nil {
		t.Fatal(err)
	}
	if err := fixture.execution.cancelAttemptNarrow(ctx, fixture.admin, fixture.caseID, fixture.runID, fixture.attemptID); err != nil {
		t.Fatalf("clean cancellation failed: %v", err)
	}
	after := durableMutationSnapshot(t, fixture, fixture.claim)
	if after.Attempt.State != "cancelled" || after.Run.State != "cancelled" || after.Job.State != "cancelled" ||
		after.Attempt.ErrorCode == nil || *after.Attempt.ErrorCode != "cancelled_by_user" {
		t.Fatalf("successful cancellation did not commit all terminal rows: %+v", after)
	}
	if len(after.ExecutionEvents) != len(before.ExecutionEvents)+1 || after.ExecutionEvents[len(after.ExecutionEvents)-1].EventType != "attempt_cancelled" ||
		len(after.RunEvents) != len(before.RunEvents)+1 || after.RunEvents[len(after.RunEvents)-1].State != "cancelled" {
		t.Fatalf("successful cancellation event history is incomplete: %+v", after)
	}
	if len(after.Audits) != len(before.Audits)+1 {
		t.Fatalf("successful cancellation did not append exactly one audit row: before=%+v after=%+v", before.Audits, after.Audits)
	}
	var previousHash string
	for _, audit := range after.Audits {
		if audit.PrevHash != previousHash || audit.Hash == "" {
			t.Fatalf("cancellation audit chain is not canonical: %+v", after.Audits)
		}
		previousHash = audit.Hash
	}
	if audit := after.Audits[len(after.Audits)-1]; audit.EventType != "review_cancelled" {
		t.Fatalf("cancellation audit event is missing: %+v", audit)
	}
}

// TestDurableAddMemberAddsNewMember drives the case adapter with a real identity
// that is not yet a member and reads the result back through the case view.
func TestDurableAddMemberAddsNewMember(t *testing.T) {
	fixture, ok := newDurableCaseFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	member, found := accounts.ByID("demo-reviewer")
	if !found {
		t.Fatal("demo reviewer missing")
	}
	snapshot, err := fixture.core.snapshotAs(fixture.admin)
	if err != nil {
		t.Fatal(err)
	}
	visible, err := fixture.core.visibleCase(&snapshot, fixture.admin, fixture.caseID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range visible.MemberIDs {
		if id == member.ID {
			t.Fatalf("test setup: reviewer is already a member")
		}
	}
	if err := fixture.document.AddMember(fixture.admin, fixture.caseID, member.ID); err == nil {
		t.Fatal("document service changed core-owned case membership")
	}
	snapshot, err = fixture.core.snapshotAs(fixture.admin)
	if err != nil {
		t.Fatal(err)
	}
	visible, err = fixture.core.visibleCase(&snapshot, fixture.admin, fixture.caseID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range visible.MemberIDs {
		if id == member.ID {
			t.Fatal("wrong-owner member mutation changed the persisted case")
		}
	}
	if err := fixture.core.AddMember(fixture.admin, fixture.caseID, member.ID); err != nil {
		t.Fatalf("core owner could not add the member: %v", err)
	}
	snapshot, err = fixture.core.snapshotAs(fixture.admin)
	if err != nil {
		t.Fatal(err)
	}
	visible, err = fixture.core.visibleCase(&snapshot, fixture.admin, fixture.caseID)
	if err != nil {
		t.Fatal(err)
	}
	added := false
	for _, id := range visible.MemberIDs {
		if id == member.ID {
			added = true
		}
	}
	if !added {
		t.Fatalf("new member was not persisted: %+v", visible.MemberIDs)
	}
}

func TestDurableArchiveBlocksWritesAndKeepsReportReadable(t *testing.T) {
	fixture, ok := newDurableTelecomFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	ctx := context.Background()
	if err := fixture.execution.RunClaimedJob(ctx, fixture.claim); err != nil {
		t.Fatalf("durable review did not complete: %v", err)
	}
	report, err := fixture.core.CreateReport(fixture.admin, fixture.caseID, fixture.runID, fixture.attemptID)
	if err != nil {
		t.Fatalf("core service could not freeze the completed report: %v", err)
	}
	_, frozenJSON, err := fixture.core.ReadReport(fixture.admin, fixture.caseID, report.ID, "json")
	if err != nil || len(frozenJSON) == 0 {
		t.Fatalf("report was not readable before archive: bytes=%d err=%v", len(frozenJSON), err)
	}
	archive, err := fixture.core.Archive(fixture.admin, fixture.caseID, report.ID, "durable scoped archive")
	if err != nil {
		t.Fatalf("core service could not archive its case: %v", err)
	}
	if archive.ReportID != report.ID {
		t.Fatalf("archive points to a different report: %+v", archive)
	}
	detail, err := fixture.document.GetCase(fixture.admin, fixture.caseID)
	if err != nil || detail.BusinessStatus != "archived" || detail.ArchivedAt == nil || len(detail.Archives) != 1 || detail.Archives[0] != archive {
		t.Fatalf("archive was not visible to an independent service: detail=%+v err=%v", detail, err)
	}
	readback, archivedJSON, err := fixture.core.ReadReport(fixture.admin, fixture.caseID, report.ID, "json")
	if err != nil || readback.ID != report.ID || string(archivedJSON) != string(frozenJSON) {
		t.Fatalf("archive changed or hid the frozen report: record=%+v err=%v", readback, err)
	}
	_, err = fixture.document.AddDocument(context.Background(), fixture.admin, fixture.caseID, "procurement", "after-archive.txt", "text/plain", []byte("must be refused"))
	var apiErr *fault.Error
	if !errors.As(err, &apiErr) || apiErr.Status != 409 {
		t.Fatalf("document service write after archive returned %v, want conflict", err)
	}
}

func TestDurableRevokedActorIsCancelledWithoutPublishing(t *testing.T) {
	actor, found := accounts.ByID("demo-reviewer")
	if !found {
		t.Fatal("demo reviewer missing")
	}
	fixture, ok := newDurableFixtureAs(t, actor)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	actor.Enabled = false
	actor.Issuer = "test-issuer"
	actor.Subject = "revoked-reviewer"
	body, err := json.Marshal(struct {
		Users []accounts.User `json:"users"`
	}{Users: []accounts.User{actor}})
	if err != nil {
		t.Fatal(err)
	}
	path := fixture.dir + "/revoked-directory.json"
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := accounts.ConfigureDirectory(""); err != nil {
			t.Errorf("restore local identity directory: %v", err)
		}
	})
	if err := fixture.execution.RunClaimedJob(context.Background(), fixture.claim); err != nil {
		t.Fatalf("revoked worker did not finish its durable cancellation: %v", err)
	}
	after := durableMutationSnapshot(t, fixture, fixture.claim)
	if after.Attempt.State != "cancelled" || after.Run.State != "cancelled" || after.Job.State != "cancelled" ||
		after.Attempt.ErrorCode == nil || *after.Attempt.ErrorCode != "authorization_revoked" {
		t.Fatalf("revoked actor did not receive an explicit cancelled terminal state: %+v", after)
	}
	snapshot := durableSnapshot(t, fixture.execution, fixture.claim)
	if _, found := snapshot.attemptInput(fixture.attemptID); found || len(snapshot.findingsForAttempt(fixture.runID, fixture.attemptID)) != 0 ||
		len(snapshot.evaluationsForAttempt(fixture.runID, fixture.attemptID)) != 0 {
		t.Fatal("revoked actor published frozen input or official results")
	}
}
