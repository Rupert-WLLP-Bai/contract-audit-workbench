package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"contractaudit/internal/accounts"
	"contractaudit/internal/advisory"
	"contractaudit/internal/config"
	"contractaudit/internal/docir"
	"contractaudit/internal/docversion"
	"contractaudit/internal/execution"
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
	telemetry              *telemetry.Exporter
	serviceOwner           string
	queuePolicy            *queueConfiguration
	sel                    config.Selection
	schema                 string
	dropSchema             bool
	pool                   *pgxpool.Pool
	readOnly               bool
}

// UseReadOnly disables business writes. Session maintenance and forward migrations remain available.
func (s *Store) UseReadOnly(readOnly bool) {
	s.mu.Lock()
	s.readOnly = readOnly
	s.mu.Unlock()
}

func (s *Store) ReadOnly() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readOnly
}

// HasDatabase reports whether this store was opened against PostgreSQL. Tests
// and fixture-only services can hold a zero Store; callers that need durable
// platform settings must not assume a pool.
func (s *Store) HasDatabase() bool { return s != nil && s.pool != nil }

type state struct {
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

// AttemptParseSelection pins parser identity and a reserved artifact ID at acceptance.
type AttemptParseSelection struct {
	AttemptID            string `json:"attempt_id"`
	RunID                string `json:"run_id"`
	DocumentID           string `json:"document_id"`
	OriginalDigest       string `json:"original_digest"`
	ParsedDocumentID     string `json:"parsed_document_id"`
	SelectionOrder       int64  `json:"selection_order"`
	ParserID             string `json:"parser_id"`
	ParserVersion        string `json:"parser_version"`
	ConfigHash           string `json:"config_hash"`
	NormalizationVersion string `json:"normalization_version"`
	ArtifactDigest       string `json:"artifact_digest"`
}

// FrozenDocument stores parser output bound to an immutable original and parse identity.
type FrozenDocument struct {
	Document             Document                 `json:"document"`
	Material             *FrozenMaterialSelection `json:"material,omitempty"`
	Ready                bool                     `json:"ready"`
	FromOCR              bool                     `json:"from_ocr"`
	ParseError           string                   `json:"parse_error"`
	Text                 string                   `json:"text"`
	IR                   docir.IR                 `json:"ir"`
	OriginalHash         string                   `json:"original_hash"`
	NormalizedHash       *string                  `json:"normalized_hash"`
	ParserRef            string                   `json:"parser_ref"`
	ParsedDocumentID     string                   `json:"parsed_document_id,omitempty"`
	SelectionOrder       int64                    `json:"selection_order,omitempty"`
	ParsedArtifactDigest string                   `json:"parsed_artifact_digest,omitempty"`
	ConfigHash           string                   `json:"config_hash,omitempty"`
	ParserID             string                   `json:"parser_id,omitempty"`
	ParserVersion        string                   `json:"parser_version,omitempty"`
	NormalizationVersion string                   `json:"normalization_version,omitempty"`
	IRDigest             string                   `json:"ir_digest,omitempty"`
	ParsedCreatedAt      string                   `json:"parsed_created_at,omitempty"`
	ParsedStatus         string                   `json:"parsed_status,omitempty"`
}

type FrozenMaterialSelection struct {
	MaterialID string `json:"material_id"`
	Role       string `json:"role"`
	Purpose    string `json:"purpose"`
	Version    int    `json:"version"`
}

// AttemptInput is the immutable input record reused by retries.
type AttemptInput struct {
	AttemptID string           `json:"attempt_id"`
	RunID     string           `json:"run_id"`
	InputHash string           `json:"input_hash"`
	FrozenAt  string           `json:"frozen_at"`
	Documents []FrozenDocument `json:"documents"`
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
	}
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

// Run is one review of the documents then in the case.
type Run struct {
	ID             string `json:"id"`
	CaseID         string `json:"case_id"`
	State          string `json:"state"`
	ModelRef       string `json:"model_ref,omitempty"`
	PartialReason  string `json:"partial_reason,omitempty"`
	FailureMessage string `json:"failure_message,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// Attempt is one immutable execution identity within a run.
type Attempt struct {
	ID                     string          `json:"id"`
	RunID                  string          `json:"run_id"`
	ActorID                string          `json:"actor_id,omitempty"`
	Ordinal                int             `json:"ordinal"`
	RetryOfAttemptID       *string         `json:"retry_of_attempt_id"`
	State                  string          `json:"state"`
	AcceptedAt             string          `json:"accepted_at"`
	StartedAt              *string         `json:"started_at"`
	FinishedAt             *string         `json:"finished_at"`
	InputHash              *string         `json:"input_hash"`
	OutputHash             *string         `json:"output_hash"`
	ErrorCode              *string         `json:"error_code"`
	HistoryKind            string          `json:"history_kind"`
	ParsedDocumentIDs      []string        `json:"parsed_document_ids"`
	RulePackSnapshot       json.RawMessage `json:"-"`
	RulePackHash           string          `json:"-"`
	RulePackSnapshotDigest string          `json:"-"`
	RulePackCompilerRef    string          `json:"-"`
	// RulePackID and RulePackVersion name the governed pack frozen at
	// acceptance; both stay empty for the synthetic baseline compiler.
	RulePackID              string `json:"-"`
	RulePackVersion         string `json:"-"`
	RulePackPointerRevision int64  `json:"-"`
	// PlannerConfigurationID freezes the agent planner configuration resolved at
	// acceptance so a retry can never switch prompt or limits. The full frozen
	// payload is kept alongside it so the worker reconstructs the approved
	// configuration instead of re-reading process environment.
	PlannerConfigurationID string          `json:"-"`
	PlannerConfiguration   json.RawMessage `json:"-"`
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

// Anchor cites one document version. A quote that does not match its span is
// unlocatable and keeps no page or offsets.
// Anchor cites the exact frozen document parse used by its attempt.
type Anchor struct {
	ID               string `json:"id"`
	DocumentID       string `json:"document_id"`
	ParsedDocumentID string `json:"parsed_document_id,omitempty"`
	NormalizedDigest string `json:"normalized_digest,omitempty"`
	Quote            string `json:"quote,omitempty"`
	Status           string `json:"status"`
	Start            int    `json:"start,omitempty"`
	End              int    `json:"end,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Page             *int   `json:"page,omitempty"`
	ElementID        string `json:"element_id,omitempty"`
}

// EvaluationEvidence records an exact citation used by a canonical evaluation.
// FactID identifies a cited result fact when one exists; compiler-added
// authority and annex citations are direct result citations without a FactID.
// Ref points into that attempt's verified frozen parse.
type EvaluationEvidence struct {
	FactID string                `json:"fact_id,omitempty"`
	Ref    ExperienceEvidenceRef `json:"ref"`
}

// ConstraintEval is the rule result for one run. It is not a human decision.
type ConstraintEval struct {
	ID           string               `json:"id"`
	RunID        string               `json:"run_id"`
	AttemptID    string               `json:"attempt_id"`
	CaseID       string               `json:"case_id"`
	Domain       string               `json:"domain"`
	RuleID       string               `json:"rule_id,omitempty"`
	RuleVersion  string               `json:"rule_version"`
	CapabilityID string               `json:"capability_id,omitempty"`
	Mandatory    bool                 `json:"mandatory,omitempty"`
	Relation     string               `json:"relation"`
	ReasonCode   string               `json:"reason_code,omitempty"`
	Reason       string               `json:"reason"`
	FactIDs      []string             `json:"fact_ids,omitempty"`
	ObjectRefs   []string             `json:"object_refs,omitempty"`
	Evidence     []EvaluationEvidence `json:"evidence,omitempty"`
	FindingID    string               `json:"finding_id,omitempty"`
}

// Finding is a proposal, not a human decision.
type Finding struct {
	ID                    string                 `json:"id"`
	RunID                 string                 `json:"run_id"`
	AttemptID             string                 `json:"attempt_id"`
	CaseID                string                 `json:"case_id"`
	Origin                string                 `json:"origin"`
	Domain                string                 `json:"domain"`
	RuleID                string                 `json:"rule_id,omitempty"`
	CapabilityID          string                 `json:"capability_id,omitempty"`
	ObjectRef             string                 `json:"object_ref,omitempty"`
	Claim                 string                 `json:"claim"`
	Relation              string                 `json:"relation"`
	DispositionSuggestion string                 `json:"disposition_suggestion,omitempty"`
	UnverifiedReason      string                 `json:"unverified_reason,omitempty"`
	Severity              *string                `json:"severity"`
	SeveritySource        *FindingSeveritySource `json:"severity_source"`
	Version               int                    `json:"version"`
	Anchors               []Anchor               `json:"anchors"`
	CreatedAt             string                 `json:"created_at"`
}

type FindingSeveritySource struct {
	PolicyID      string `json:"policy_id"`
	PolicyVersion string `json:"policy_version"`
	Clause        string `json:"clause"`
	Reason        string `json:"reason"`
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

// RunSnapshot is the immutable input of one review run.
type RunSnapshot struct {
	RunID                    string                      `json:"run_id"`
	DocumentIDs              []string                    `json:"document_ids"`
	PartyContext             []PartyContextItem          `json:"party_context,omitempty"`
	PartyContextHash         string                      `json:"party_context_hash,omitempty"`
	ServiceObjectContext     *ServiceObjectReviewContext `json:"service_object_context,omitempty"`
	ServiceObjectContextHash string                      `json:"service_object_context_hash,omitempty"`
	ParsedDocumentIDs        []string                    `json:"parsed_document_ids,omitempty"`
	ParseSelectionHash       string                      `json:"parse_selection_hash,omitempty"`
	ParserConfigurationID    string                      `json:"parser_configuration_id,omitempty"`
	RuleSetHash              string                      `json:"rule_set_hash"`
	PlaybookHash             string                      `json:"playbook_hash"`
	ModelRef                 string                      `json:"model_ref"`
	ModelConfigurationID     string                      `json:"model_configuration_id,omitempty"`
	ComparatorRef            string                      `json:"comparator_ref"`
	RulePackCompilerRef      string                      `json:"rule_pack_compiler_ref,omitempty"`
	// RulePack identity frozen at acceptance. PackID and PackVersion are empty
	// when no governed pack applied and the synthetic baseline compiler ran.
	RulePackID          string          `json:"rule_pack_id,omitempty"`
	RulePackVersion     string          `json:"rule_pack_version,omitempty"`
	RulePackHash        string          `json:"rule_pack_hash,omitempty"`
	RulePackDigest      string          `json:"rule_pack_digest,omitempty"`
	RulePackSnapshot    []byte          `json:"-"`
	CloudPolicySnapshot json.RawMessage `json:"cloud_policy_snapshot,omitempty"`
	CloudPolicyDigest   string          `json:"cloud_policy_digest,omitempty"`
	// GovernedKnowledge is the server-resolved acceptance snapshot. Older runs
	// omit it entirely, preserving their historical frozen input identity.
	GovernedKnowledge      *ReviewKnowledgeSnapshot `json:"governed_knowledge,omitempty"`
	PlannerConfigurationID string                   `json:"planner_configuration_id,omitempty"`
	PlannerConfiguration   json.RawMessage          `json:"-"`
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
	Token     string `json:"token"`
	ActorID   string `json:"actor_id"`
	Issuer    string `json:"issuer"`
	Subject   string `json:"subject"`
	TenantID  string `json:"tenant_id"`
	OrgID     string `json:"org_id"`
	Audience  string `json:"audience"`
	ExpiresAt string `json:"expires_at"`
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

func (s *Store) update(fn func(*state) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateState(context.Background(), localDemoScope("system"), "", fn)
}

func (s *Store) snapshot() state {
	st, err := s.snapshotScoped(context.Background(), localDemoScope("system"))
	if err != nil {
		return state{}
	}
	s.mu.Lock()
	s.st = st
	s.mu.Unlock()
	out, err := st.clone()
	if err != nil {
		return st
	}
	return out
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
