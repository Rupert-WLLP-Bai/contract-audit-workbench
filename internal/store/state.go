// File metadata (manifest mirror):
// Purpose: Store is the case file. Relational rows live in PostgreSQL; original bytes stay under dir. Related `store` operations include UseReadOnly, ReadOnly, HasDatabase, parseSelectionsForAttempt.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"contractaudit/internal/accounts"
	"contractaudit/internal/advisory"
	"contractaudit/internal/approval"
	"contractaudit/internal/config"
	"contractaudit/internal/docir"
	"contractaudit/internal/docversion"
	"contractaudit/internal/execution"
	"contractaudit/internal/model"
	"contractaudit/internal/telemetry"

	"github.com/jackc/pgx/v5/pgxpool"
)

const timeLayout = "2006-01-02T15:04:05.000Z"

// Store is the case file. Relational rows live in PostgreSQL; original bytes stay under dir.
type Store struct {
	mu                     sync.Mutex
	parseMu                chan struct{}
	parseMuOnce            sync.Once
	executionMu            sync.Mutex
	executionWG            sync.WaitGroup
	closing                bool
	executionSlots         chan struct{}
	runningMu              sync.Mutex
	running                map[string]func()
	claimMu                sync.Mutex
	claims                 map[string]JobClaim
	operationAuthority     OperationAuthorityResolver
	operationContexts      map[string]model.DelegatedScope
	dir                    string
	st                     state
	modelsMu               sync.RWMutex
	models                 Models
	subjectSourcesMu       sync.RWMutex
	subjectSources         *SubjectSourceManifest
	advisoryProvider       advisory.Provider
	objects                ObjectStore
	documentPreparer       DocumentPreparer
	requireRemoteDocuments bool
	toolEvidence           ToolEvidenceReader
	experienceAcceptor     ExperienceResultAcceptor
	telemetry              atomic.Pointer[telemetry.Exporter]
	metricWorkerID         atomic.Value
	clmMetrics             atomic.Pointer[CLMMetricsSnapshot]
	serviceOwner           string
	queuePolicy            *queueConfiguration
	sel                    config.Selection
	schema                 string
	dropSchema             bool
	pool                   *pgxpool.Pool
	approvalKeyring        *approval.Keyring
	readOnly               atomic.Bool
}

// UseReadOnly disables business writes. Session maintenance and forward migrations remain available.
func (s *Store) UseReadOnly(readOnly bool) {
	s.mu.Lock()
	s.readOnly.Store(readOnly)
	s.mu.Unlock()
}

func (s *Store) ReadOnly() bool {
	return s.readOnly.Load()
}

// HasDatabase reports whether this store was opened against PostgreSQL. Tests
// and fixture-only services can hold a zero Store; callers that need durable
// platform settings must not assume a pool.
func (s *Store) HasDatabase() bool { return s != nil && s.pool != nil }

type state struct {
	authorityUser           *accounts.User
	Sessions                []session                  `json:"sessions"`
	Cases                   []Case                     `json:"cases"`
	Documents               []Document                 `json:"documents"`
	Materials               []MaterialEntity           `json:"materials"`
	MaterialVersions        []MaterialVersion          `json:"material_versions"`
	MaterialChains          []CaseMaterialChain        `json:"material_chains"`
	UploadIdempotency       []UploadIdempotencyRecord  `json:"upload_idempotency"`
	ParsedDocuments         []ParsedDocument           `json:"parsed_documents"`
	Runs                    []Run                      `json:"runs"`
	Attempts                []Attempt                  `json:"attempts"`
	AttemptInputs           []AttemptInput             `json:"attempt_inputs"`
	AttemptParseSelections  []AttemptParseSelection    `json:"attempt_parse_selections"`
	ExecutionEvents         []ExecutionEvent           `json:"execution_events"`
	Findings                []Finding                  `json:"findings"`
	Evaluations             []ConstraintEval           `json:"evaluations"`
	Decisions               []Decision                 `json:"decisions"`
	Events                  []Event                    `json:"events"`
	Activity                []Activity                 `json:"activity"`
	Idempotency             []Idem                     `json:"idempotency"`
	Snapshots               []RunSnapshot              `json:"snapshots"`
	Audits                  []AuditRecord              `json:"audits"`
	Jobs                    []Job                      `json:"jobs"`
	Outbox                  []OutboxRecord             `json:"outbox"`
	Generations             []generationRecord         `json:"generations"`
	Remediations            []remediationRecord        `json:"remediations"`
	Reports                 []frozenReport             `json:"reports"`
	Archives                []archiveRecord            `json:"archives"`
	Profiles                []caseProfileRecord        `json:"profiles"`
	Tasks                   []workbenchTaskRecord      `json:"tasks"`
	Relations               []caseRelationRecord       `json:"relations"`
	Knowledge               []knowledgeRecord          `json:"knowledge"`
	WorkbenchAudit          []workbenchAuditRecord     `json:"workbench_audit"`
	Subjects                []Subject                  `json:"subjects"`
	SubjectVersions         []SubjectVersion           `json:"subject_versions"`
	CaseSubjectRoles        []CaseSubjectRole          `json:"case_subject_roles"`
	CaseSubjectRoleVersions []CaseSubjectRoleVersion   `json:"case_subject_role_versions"`
	SubjectChangeRequests   []SubjectChangeRequest     `json:"subject_change_requests"`
	SubjectChangeDecisions  []SubjectChangeDecision    `json:"subject_change_decisions"`
	SubjectIdempotency      []SubjectIdempotencyRecord `json:"subject_idempotency"`
	SubjectSourceSnapshots  []SubjectSourceSnapshot    `json:"subject_source_snapshots"`
}

func (st state) parseSelectionsForAttempt(attemptID string) []AttemptParseSelection {
	var out []AttemptParseSelection
	for _, selection := range st.AttemptParseSelections {
		if selection.AttemptID == attemptID {
			out = append(out, selection)
		}
	}
	return out
}

func (st *state) normalize() {
	if st.Sessions == nil {
		st.Sessions = []session{}
	}
	if st.Cases == nil {
		st.Cases = []Case{}
	}
	if st.Documents == nil {
		st.Documents = []Document{}
	}
	if st.ParsedDocuments == nil {
		st.ParsedDocuments = []ParsedDocument{}
	}
	if st.Runs == nil {
		st.Runs = []Run{}
	}
	if st.Attempts == nil {
		st.Attempts = []Attempt{}
	}
	if st.AttemptInputs == nil {
		st.AttemptInputs = []AttemptInput{}
	}
	if st.AttemptParseSelections == nil {
		st.AttemptParseSelections = []AttemptParseSelection{}
	}
	if st.ExecutionEvents == nil {
		st.ExecutionEvents = []ExecutionEvent{}
	}
	if st.Findings == nil {
		st.Findings = []Finding{}
	}
	if st.Evaluations == nil {
		st.Evaluations = []ConstraintEval{}
	}
	if st.Decisions == nil {
		st.Decisions = []Decision{}
	}
	if st.Events == nil {
		st.Events = []Event{}
	}
	if st.Activity == nil {
		st.Activity = []Activity{}
	}
	if st.Idempotency == nil {
		st.Idempotency = []Idem{}
	}
	if st.Snapshots == nil {
		st.Snapshots = []RunSnapshot{}
	}
	if st.Audits == nil {
		st.Audits = []AuditRecord{}
	}
	if st.Jobs == nil {
		st.Jobs = []Job{}
	}
	if st.Outbox == nil {
		st.Outbox = []OutboxRecord{}
	}
	if st.Generations == nil {
		st.Generations = []generationRecord{}
	}
	if st.Remediations == nil {
		st.Remediations = []remediationRecord{}
	}
	if st.Reports == nil {
		st.Reports = []frozenReport{}
	}
	if st.Archives == nil {
		st.Archives = []archiveRecord{}
	}
	if st.Profiles == nil {
		st.Profiles = []caseProfileRecord{}
	}
	if st.Tasks == nil {
		st.Tasks = []workbenchTaskRecord{}
	}
	if st.Relations == nil {
		st.Relations = []caseRelationRecord{}
	}
	if st.Knowledge == nil {
		st.Knowledge = []knowledgeRecord{}
	}
	if st.WorkbenchAudit == nil {
		st.WorkbenchAudit = []workbenchAuditRecord{}
	}
	if st.Subjects == nil {
		st.Subjects = []Subject{}
	}
	if st.SubjectVersions == nil {
		st.SubjectVersions = []SubjectVersion{}
	}
	if st.CaseSubjectRoles == nil {
		st.CaseSubjectRoles = []CaseSubjectRole{}
	}
	if st.CaseSubjectRoleVersions == nil {
		st.CaseSubjectRoleVersions = []CaseSubjectRoleVersion{}
	}
	if st.SubjectChangeRequests == nil {
		st.SubjectChangeRequests = []SubjectChangeRequest{}
	}
	if st.SubjectChangeDecisions == nil {
		st.SubjectChangeDecisions = []SubjectChangeDecision{}
	}
	if st.SubjectIdempotency == nil {
		st.SubjectIdempotency = []SubjectIdempotencyRecord{}
	}
	if st.SubjectSourceSnapshots == nil {
		st.SubjectSourceSnapshots = []SubjectSourceSnapshot{}
	}
}

func (st state) clone() (state, error) {
	b, err := json.Marshal(st)
	if err != nil {
		return state{}, err
	}
	var out state
	if err := json.Unmarshal(b, &out); err != nil {
		return state{}, err
	}
	for i := range out.Documents {
		if i >= len(st.Documents) {
			break
		}
		out.Documents[i].StorageBackend = st.Documents[i].StorageBackend
	}
	for i := range out.AttemptInputs {
		for j := range out.AttemptInputs[i].Documents {
			out.AttemptInputs[i].Documents[j].Document.StorageBackend = st.AttemptInputs[i].Documents[j].Document.StorageBackend
		}
	}
	// Restore immutable RulePack/planner fields that JSON intentionally hides.
	for i := range out.Attempts {
		if i >= len(st.Attempts) {
			break
		}
		out.Attempts[i].TenantLiveEpoch = st.Attempts[i].TenantLiveEpoch
		out.Attempts[i].RulePackSnapshot = append(json.RawMessage(nil), st.Attempts[i].RulePackSnapshot...)
		out.Attempts[i].RulePackHash = st.Attempts[i].RulePackHash
		out.Attempts[i].RulePackCompilerRef = st.Attempts[i].RulePackCompilerRef
		out.Attempts[i].RulePackSnapshotDigest = st.Attempts[i].RulePackSnapshotDigest
		out.Attempts[i].RulePackID = st.Attempts[i].RulePackID
		out.Attempts[i].RulePackVersion = st.Attempts[i].RulePackVersion
		out.Attempts[i].RulePackPointerRevision = st.Attempts[i].RulePackPointerRevision
		out.Attempts[i].PlannerConfigurationID = st.Attempts[i].PlannerConfigurationID
		out.Attempts[i].PlannerConfiguration = append(json.RawMessage(nil), st.Attempts[i].PlannerConfiguration...)
	}
	for i := range out.Snapshots {
		if i >= len(st.Snapshots) {
			break
		}
		out.Snapshots[i].RulePackSnapshot = append([]byte(nil), st.Snapshots[i].RulePackSnapshot...)
		out.Snapshots[i].PlannerConfiguration = append(json.RawMessage(nil), st.Snapshots[i].PlannerConfiguration...)
		out.Snapshots[i].AdoptedTenantConfigVersionID = st.Snapshots[i].AdoptedTenantConfigVersionID
		out.Snapshots[i].AdoptedTenantConfigDigest = st.Snapshots[i].AdoptedTenantConfigDigest
		out.Snapshots[i].InputHashVersion = st.Snapshots[i].InputHashVersion
	}
	// JSON encodes UTC instants with a Z suffix. When the process timezone is
	// UTC, decoding may attach time.Local instead of time.UTC; normalize rows
	// before reflect-based delta checks so a clone cannot appear to mutate them.
	canonicalizeLoadedRows(&out)
	out.authorityUser = st.authorityUser
	out.normalize()
	return out, nil
}

// Case is one review matter.
type Case struct {
	ID             string   `json:"id"`
	TenantID       string   `json:"tenant_id"`
	OwnerOrgID     string   `json:"owner_org_id"`
	BusinessLine   string   `json:"business_line"`
	Scenario       string   `json:"scenario"`
	BusinessStatus string   `json:"business_status"`
	ArchivedAt     *string  `json:"archived_at,omitempty"`
	Title          string   `json:"title"`
	ContractType   string   `json:"contract_type"`
	OwnerID        string   `json:"owner_id"`
	MemberIDs      []string `json:"member_ids"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

func (c Case) allows(user accounts.User) bool {
	return caseReadAllowed(c, user)
}

// Document is the immutable original-file snapshot.
type Document struct {
	ID             string `json:"id"`
	TenantID       string `json:"tenant_id"`
	OrgID          string `json:"org_id"`
	CaseID         string `json:"case_id"`
	Role           string `json:"role"`
	Filename       string `json:"filename"`
	MediaType      string `json:"media_type"`
	Digest         string `json:"digest"`
	Size           int64  `json:"size"`
	Origin         string `json:"origin"`
	StorageBackend string `json:"-"`
	CreatedAt      string `json:"created_at"`
}

type UsageObservation struct {
	Source       string `json:"source"`
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
	TotalTokens  *int64 `json:"total_tokens"`
}

// ExecutionEvent is an append-only business observation for one attempt.
type ExecutionEvent struct {
	Seq          int                       `json:"seq"`
	AttemptID    string                    `json:"attempt_id"`
	SpanID       *string                   `json:"span_id"`
	ParentSpanID *string                   `json:"parent_span_id"`
	NodeKind     string                    `json:"node_kind"`
	Phase        string                    `json:"phase"`
	EventType    string                    `json:"event_type"`
	At           string                    `json:"at"`
	RecordedAt   *string                   `json:"recorded_at,omitempty"`
	EventKey     *string                   `json:"event_key,omitempty"`
	Generation   *int64                    `json:"generation,omitempty"`
	DurationMS   *int64                    `json:"duration_ms"`
	Status       string                    `json:"status"`
	ErrorCode    *string                   `json:"error_code"`
	ModelRef     *string                   `json:"model_ref"`
	Usage        *UsageObservation         `json:"usage"`
	Trace        *execution.ModelCallTrace `json:"trace,omitempty"`
}

type KnownTokenSubtotals struct {
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
	TotalTokens  *int64 `json:"total_tokens"`
}

type UsageSummary struct {
	InputTokens    *int64              `json:"input_tokens"`
	OutputTokens   *int64              `json:"output_tokens"`
	TotalTokens    *int64              `json:"total_tokens"`
	Calls          int                 `json:"calls"`
	UnknownCalls   int                 `json:"unknown_calls"`
	Sources        []string            `json:"sources"`
	KnownSubtotals KnownTokenSubtotals `json:"known_subtotals"`
	Cost           *float64            `json:"cost"`
}

type Execution struct {
	SchemaVersion          string           `json:"schema_version"`
	Attempt                *Attempt         `json:"attempt"`
	Attempts               []Attempt        `json:"attempts"`
	Events                 []ExecutionEvent `json:"events"`
	Usage                  UsageSummary     `json:"usage"`
	AllAttemptsUsage       UsageSummary     `json:"all_attempts_usage"`
	DurationMS             *int64           `json:"duration_ms"`
	FirstVerifiedFindingMS *int64           `json:"first_verified_finding_ms"`
	QueueWaitMS            *int64           `json:"queue_wait_ms"`
	// AgentReview is the persisted five-domain projection of this exact attempt.
	// It is nil when the attempt has no agent-flow record, including every
	// historical attempt, and it is never recomputed from the latest run state.
	AgentReview json.RawMessage `json:"agent_review"`
}

// Decision is an append-only human record.
type Decision struct {
	ID             string `json:"id"`
	FindingID      string `json:"finding_id"`
	RunID          string `json:"run_id"`
	CaseID         string `json:"case_id"`
	ActorID        string `json:"actor_id"`
	ActorName      string `json:"actor_name"`
	Action         string `json:"action"`
	Reason         string `json:"reason"`
	OldDisposition string `json:"old_disposition"`
	NewDisposition string `json:"new_disposition"`
	CreatedAt      string `json:"created_at"`
}

// Event is one review-stream notice.
type Event struct {
	RunID string `json:"run_id"`
	Seq   int    `json:"seq"`
	Type  string `json:"type"`
	State string `json:"state"`
}

// Activity is a case timeline line.
type Activity struct {
	ID      string `json:"id"`
	CaseID  string `json:"case_id"`
	At      string `json:"at"`
	Summary string `json:"summary"`
}

// AuditRecord is one append-only business event. It is not the SSE notice.
type AuditRecord struct {
	EventID        string   `json:"event_id"`
	RunID          string   `json:"run_id"`
	Actor          string   `json:"actor"`
	Seq            int      `json:"seq"`
	EventType      string   `json:"event_type"`
	PrevHash       string   `json:"prev_hash"`
	Hash           string   `json:"hash"`
	CorrelationIDs []string `json:"correlation_ids"`
}

// Idem remembers one decision submission.
type Idem struct {
	Key             string `json:"key"`
	ActorID         string `json:"actor_id"`
	FindingID       string `json:"finding_id"`
	DecisionID      string `json:"decision_id"`
	Action          string `json:"action"`
	Reason          string `json:"reason"`
	ExpectedVersion int    `json:"expected_version"`
}

type session struct {
	Token        string  `json:"token"`
	ActorID      string  `json:"actor_id"`
	Issuer       string  `json:"issuer"`
	Subject      string  `json:"subject"`
	TenantID     string  `json:"tenant_id"`
	OrgID        string  `json:"org_id"`
	Audience     string  `json:"audience"`
	ExpiresAt    string  `json:"expires_at"`
	PrincipalID  *string `json:"principal_id,omitempty"`
	MembershipID *string `json:"membership_id,omitempty"`
}

// Interpreted is a document plus a verified immutable parse artifact.
type Interpreted struct {
	Document       Document
	Ready          bool
	FromOCR        bool
	ParseError     string
	Text           string
	IR             docir.IR
	ParsedDocument *ParsedDocument
	ParsePending   bool
}

// Counts is the latest run's finding tally.
type Counts struct {
	Total, Undecided, Conflict, Insufficient int
}

// Open opens the database selected by APP_ENV. The default is the development
// database, never the test or production database. Original bytes stay in dir.
func Open(dir string) (*Store, error) {
	sel, err := config.FromProcess()
	if err != nil {
		return nil, err
	}
	schema, err := applicationSchema()
	if err != nil {
		return nil, err
	}
	return openStore(dir, sel, schema, false)
}

// OpenApplication connects with the application database identity and never runs migrations.
func OpenApplication(dir string) (*Store, error) {
	sel, err := config.FromProcess()
	if err != nil {
		return nil, err
	}
	schema, err := applicationSchema()
	if err != nil {
		return nil, err
	}
	return openApplicationStore(dir, sel, schema)
}

func (s *Store) acquireParseLock(ctx context.Context) error {
	s.parseMuOnce.Do(func() { s.parseMu = make(chan struct{}, 1) })
	select {
	case s.parseMu <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-s.parseMu
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Store) releaseParseLock() {
	<-s.parseMu
}

func newID(prefix string) (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buf[:]), nil
}

func stamp() string { return time.Now().UTC().Format(timeLayout) }

func runeCount(s string) int { return utf8.RuneCountInString(s) }

func writeBlob(dir, digest string, content []byte) error {
	if _, err := docversion.ParseDigest(digest); err != nil {
		return err
	}
	path := filepath.Join(dir, "blobs", digest)
	// Identical simultaneous uploads must not share a temporary filename.
	f, err := os.CreateTemp(filepath.Dir(path), ".blob-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readBlob(dir, digest string) ([]byte, error) {
	if _, err := docversion.ParseDigest(digest); err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(dir, "blobs", digest))
}
