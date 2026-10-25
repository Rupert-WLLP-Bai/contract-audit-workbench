package store

import "encoding/json"

// Template is one immutable, versioned document generator.
type Template struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Version string          `json:"version"`
	Digest  string          `json:"digest"`
	Fields  []TemplateField `json:"fields"`
}

type TemplateField struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
	Type     string `json:"type"`
}

type GenerationRecord struct {
	ID              string `json:"id"`
	DocumentID      string `json:"document_id"`
	TemplateID      string `json:"template_id"`
	TemplateVersion string `json:"template_version"`
	Digest          string `json:"digest"`
	CreatedAt       string `json:"created_at"`
}

type Remediation struct {
	ID                    string   `json:"id"`
	CaseID                string   `json:"case_id"`
	RunID                 string   `json:"run_id"`
	AttemptID             string   `json:"attempt_id"`
	FindingIDs            []string `json:"finding_ids"`
	AssigneeID            string   `json:"assignee_id,omitempty"`
	DueAt                 string   `json:"due_at,omitempty"`
	Measure               string   `json:"measure"`
	Status                string   `json:"status"`
	Version               int      `json:"version"`
	DocumentIDs           []string `json:"document_ids"`
	Reason                string   `json:"reason"`
	VerificationRunID     *string  `json:"verification_run_id,omitempty"`
	VerificationAttemptID *string  `json:"verification_attempt_id,omitempty"`
	CreatedAt             string   `json:"created_at"`
	UpdatedAt             string   `json:"updated_at"`
}

type RemediationInput struct {
	RunID      string   `json:"run_id"`
	AttemptID  string   `json:"attempt_id"`
	FindingIDs []string `json:"finding_ids"`
	AssigneeID string   `json:"assignee_id"`
	DueAt      string   `json:"due_at"`
	Measure    string   `json:"measure"`
}

type RemediationSubmitInput struct {
	DocumentIDs     []string `json:"document_ids"`
	Reason          string   `json:"reason"`
	ExpectedVersion int      `json:"expected_version"`
}

type RemediationVerifyInput struct {
	RunID           string `json:"run_id"`
	AttemptID       string `json:"attempt_id"`
	Reason          string `json:"reason"`
	ExpectedVersion int    `json:"expected_version"`
}

type ReportRecord struct {
	ID                   string `json:"id"`
	Version              int    `json:"version"`
	RunID                string `json:"run_id"`
	AttemptID            string `json:"attempt_id"`
	InputHash            string `json:"input_hash"`
	OutputHash           string `json:"output_hash"`
	DecisionSnapshotHash string `json:"decision_snapshot_hash"`
	Digest               string `json:"digest"`
	CreatedAt            string `json:"created_at"`
}

type ArchiveRecord struct {
	ID         string `json:"id"`
	CaseID     string `json:"case_id"`
	ReportID   string `json:"report_id"`
	Reason     string `json:"reason"`
	ArchivedAt string `json:"archived_at"`
}

// These persistence envelopes keep tenant scope and immutable report bytes out
// of the public DTOs while letting PostgreSQL enforce scoped incremental writes.
type generationRecord struct {
	Record   GenerationRecord `json:"record"`
	TenantID string           `json:"tenant_id"`
	OrgID    string           `json:"org_id"`
	CaseID   string           `json:"case_id"`
}

type remediationRecord struct {
	Record   Remediation           `json:"record"`
	TenantID string                `json:"tenant_id"`
	OrgID    string                `json:"org_id"`
	History  []remediationRevision `json:"history"`
}

type remediationRevision struct {
	Record     Remediation `json:"record"`
	RecordedAt string      `json:"recorded_at"`
}

type frozenReport struct {
	Record    ReportRecord `json:"record"`
	TenantID  string       `json:"tenant_id"`
	OrgID     string       `json:"org_id"`
	CaseID    string       `json:"case_id"`
	JSONBytes []byte       `json:"json_bytes"`
	HTMLBytes []byte       `json:"html_bytes"`
}

type archiveRecord struct {
	Record   ArchiveRecord `json:"record"`
	TenantID string        `json:"tenant_id"`
	OrgID    string        `json:"org_id"`
	ActorID  string        `json:"actor_id"`
}

// reportEnvelope is the frozen, full-data representation returned as JSON.
type reportEnvelope struct {
	SchemaVersion        string           `json:"schema_version"`
	ReportID             string           `json:"report_id"`
	ReportVersion        int              `json:"report_version"`
	CreatedAt            string           `json:"created_at"`
	Notice               string           `json:"notice"`
	Case                 reportCase       `json:"case"`
	Run                  Run              `json:"run"`
	Attempt              Attempt          `json:"attempt"`
	Execution            reportExecution  `json:"execution"`
	RunSnapshot          RunSnapshot      `json:"run_snapshot"`
	Input                AttemptInput     `json:"input"`
	Documents            []reportDocument `json:"documents"`
	Findings             []Finding        `json:"findings"`
	Evaluations          []ConstraintEval `json:"evaluations"`
	Decisions            []Decision       `json:"decisions"`
	RulePack             reportRulePack   `json:"rulepack"`
	InputHash            string           `json:"input_hash"`
	OutputHash           string           `json:"output_hash"`
	DecisionSnapshotHash string           `json:"decision_snapshot_hash"`
	// AgentReview embeds the projection as JSON for consumers. AgentReviewBytes
	// preserves the exact persisted bytes so its digest remains independently
	// verifiable even when JSON encoding compacts the embedded value.
	AgentReview      json.RawMessage `json:"agent_review"`
	AgentReviewBytes []byte          `json:"agent_review_bytes,omitempty"`
	// AgentReviewSHA256 commits the exact bytes above and lets consumers re-derive
	// the canonical attempt output hash. Both remain absent for legacy attempts.
	AgentReviewSHA256 string `json:"agent_review_sha256,omitempty"`
}

type reportExecution struct {
	SchemaVersion          string           `json:"schema_version"`
	Events                 []ExecutionEvent `json:"events"`
	Usage                  UsageSummary     `json:"usage"`
	DurationMS             *int64           `json:"duration_ms"`
	FirstVerifiedFindingMS *int64           `json:"first_verified_finding_ms"`
	QueueWaitMS            *int64           `json:"queue_wait_ms"`
}

type reportCase struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	ContractType string `json:"contract_type"`
	BusinessLine string `json:"business_line"`
	Scenario     string `json:"scenario"`
}

type reportDocument struct {
	Document Document       `json:"document"`
	Parsed   ParsedDocument `json:"parsed"`
}

type reportRulePack struct {
	Availability      string          `json:"availability"`
	CompilerRef       string          `json:"compiler_ref"`
	Hash              string          `json:"manifest_hash"`
	SnapshotDigest    string          `json:"snapshot_sha256"`
	Snapshot          json.RawMessage `json:"snapshot"`
	UnavailableReason string          `json:"unavailable_reason,omitempty"`
}

type CaseProfile struct {
	CaseID           string  `json:"case_id"`
	Number           string  `json:"number"`
	Customer         string  `json:"customer"`
	Supplier         string  `json:"supplier"`
	ContractCategory string  `json:"contract_category"`
	OwnerID          string  `json:"owner_id"`
	DueAt            *string `json:"due_at"`
	Version          int     `json:"version"`
	UpdatedAt        string  `json:"updated_at"`
}

type CaseProfileInput struct {
	Number           string  `json:"number"`
	Customer         string  `json:"customer"`
	Supplier         string  `json:"supplier"`
	ContractCategory string  `json:"contract_category"`
	OwnerID          string  `json:"owner_id"`
	DueAt            *string `json:"due_at"`
	ExpectedVersion  int     `json:"expected_version"`
}

type WorkbenchTask struct {
	ID         string  `json:"id"`
	CaseID     string  `json:"case_id"`
	Title      string  `json:"title"`
	Kind       string  `json:"kind"`
	AssigneeID string  `json:"assignee_id"`
	DueAt      *string `json:"due_at"`
	Status     string  `json:"status"`
	Priority   string  `json:"priority"`
	FindingID  *string `json:"finding_id"`
	CreatedBy  *string `json:"created_by"`
	Version    int     `json:"version"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
}

type WorkbenchTaskInput struct {
	Title           string  `json:"title"`
	Kind            string  `json:"kind"`
	AssigneeID      string  `json:"assignee_id"`
	DueAt           *string `json:"due_at"`
	Status          string  `json:"status"`
	Priority        string  `json:"priority"`
	FindingID       *string `json:"finding_id"`
	ExpectedVersion int     `json:"expected_version"`
}

type CaseRelation struct {
	ID            string `json:"id"`
	CaseID        string `json:"case_id"`
	RelatedCaseID string `json:"related_case_id"`
	Reason        string `json:"reason"`
	CreatedBy     string `json:"created_by"`
	CreatedAt     string `json:"created_at"`
}

type CaseRelationInput struct {
	RelatedCaseID string `json:"related_case_id"`
	Reason        string `json:"reason"`
}

type Knowledge struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Version     string  `json:"version"`
	Body        string  `json:"body"`
	SourceType  string  `json:"source_type"`
	EffectiveAt *string `json:"effective_at"`
	CreatedBy   string  `json:"created_by"`
	CreatedAt   string  `json:"created_at"`
}

type KnowledgeInput struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Version     string  `json:"version"`
	Body        string  `json:"body"`
	SourceType  string  `json:"source_type"`
	EffectiveAt *string `json:"effective_at"`
}

type WorkbenchAudit struct {
	ID        string  `json:"id"`
	CaseID    *string `json:"case_id"`
	CaseTitle *string `json:"case_title"`
	ActorID   string  `json:"actor_id"`
	ActorName string  `json:"actor_name"`
	Action    string  `json:"action"`
	At        string  `json:"at"`
	Summary   string  `json:"summary"`
}

type WorkbenchCase struct {
	ID               string       `json:"id"`
	Title            string       `json:"title"`
	ContractType     string       `json:"contract_type"`
	BusinessLine     string       `json:"business_line"`
	Scenario         string       `json:"scenario"`
	BusinessStatus   string       `json:"business_status"`
	ArchivedAt       *string      `json:"archived_at"`
	DocumentCount    int          `json:"document_count"`
	PendingDecisions int          `json:"pending_decisions"`
	LatestRun        any          `json:"latest_run"`
	UpdatedAt        string       `json:"updated_at"`
	Profile          *CaseProfile `json:"profile"`
	Severity         *string      `json:"severity"`
	Progress         *float64     `json:"progress"`
}

type WorkbenchDashboard struct {
	VisibleCases       int              `json:"visible_cases"`
	MyPendingTasks     int              `json:"my_pending_tasks"`
	OverdueTasks       int              `json:"overdue_tasks"`
	PendingReviewCases int              `json:"pending_review_cases"`
	RemediationCases   int              `json:"remediation_cases"`
	CompletedReviews   int              `json:"completed_reviews"`
	AverageDurationMS  *int64           `json:"average_duration_ms"`
	Progress           []WorkbenchCount `json:"progress"`
	Risks              []WorkbenchCount `json:"risks"`
}

type WorkbenchCount struct {
	Status   string `json:"status,omitempty"`
	Severity string `json:"severity,omitempty"`
	Count    int    `json:"count"`
}

type WorkbenchParsedDocument struct {
	ID                   string  `json:"id"`
	DocumentID           string  `json:"document_id"`
	OriginalDigest       string  `json:"original_digest"`
	ParserID             string  `json:"parser_id"`
	ParserVersion        string  `json:"parser_version"`
	ConfigHash           string  `json:"config_hash"`
	NormalizationVersion string  `json:"normalization_version"`
	NormalizedDigest     *string `json:"normalized_digest"`
	IRDigest             *string `json:"ir_digest"`
	ArtifactDigest       string  `json:"artifact_digest"`
	CreatedAt            string  `json:"created_at"`
	Status               string  `json:"status"`
}

type WorkbenchDocument struct {
	ID             string                   `json:"id"`
	Role           string                   `json:"role"`
	Filename       string                   `json:"filename"`
	MediaType      string                   `json:"media_type"`
	Digest         string                   `json:"digest"`
	Size           int64                    `json:"size"`
	CaseID         string                   `json:"case_id"`
	CaseTitle      string                   `json:"case_title"`
	ParseStatus    string                   `json:"parse_status"`
	ParseError     *string                  `json:"parse_error"`
	Superseded     bool                     `json:"superseded"`
	ParsedDocument *WorkbenchParsedDocument `json:"parsed_document"`
	CreatedAt      string                   `json:"created_at"`
}

type WorkbenchRemediation struct {
	Remediation
	CaseTitle string `json:"case_title"`
}

type WorkbenchReport struct {
	ReportRecord
	CaseID    string `json:"case_id"`
	CaseTitle string `json:"case_title"`
}

type WorkbenchRun struct {
	ID         string        `json:"id"`
	CaseID     string        `json:"case_id"`
	CaseTitle  string        `json:"case_title"`
	State      string        `json:"state"`
	CreatedAt  string        `json:"created_at"`
	UpdatedAt  string        `json:"updated_at"`
	AttemptID  *string       `json:"attempt_id"`
	StartedAt  *string       `json:"started_at"`
	FinishedAt *string       `json:"finished_at"`
	Usage      *UsageSummary `json:"usage"`
}

type WorkbenchModel struct {
	Name    string `json:"name"`
	OCRName string `json:"ocr_name"`
	Status  string `json:"status"`
	Label   string `json:"label"`
}

type WorkbenchTool struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Capabilities []string `json:"capabilities"`
	Calls        int      `json:"calls"`
	LastStatus   *string  `json:"last_status"`
}

type WorkbenchHealth struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Detail     string `json:"detail"`
	ObservedAt string `json:"observed_at"`
}

type WorkbenchSnapshot struct {
	SchemaVersion string                 `json:"schema_version"`
	GeneratedAt   string                 `json:"generated_at"`
	Period        string                 `json:"period"`
	Dashboard     WorkbenchDashboard     `json:"dashboard"`
	Cases         []WorkbenchCase        `json:"cases"`
	Profiles      []CaseProfile          `json:"profiles"`
	Tasks         []WorkbenchTask        `json:"tasks"`
	Relations     []CaseRelation         `json:"relations"`
	Knowledge     []Knowledge            `json:"knowledge"`
	Members       []Actor                `json:"members"`
	Remediations  []WorkbenchRemediation `json:"remediations"`
	Reports       []WorkbenchReport      `json:"reports"`
	Documents     []WorkbenchDocument    `json:"documents"`
	Runs          []WorkbenchRun         `json:"runs"`
	Audit         []WorkbenchAudit       `json:"audit"`
	Templates     []Template             `json:"templates"`
	Model         WorkbenchModel         `json:"model"`
	Tools         []WorkbenchTool        `json:"tools"`
	Health        []WorkbenchHealth      `json:"health"`
}

type Actor struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	TenantID    string `json:"tenant_id"`
	OrgID       string `json:"org_id"`
}

type caseProfileRecord struct {
	Profile  CaseProfile `json:"profile"`
	TenantID string      `json:"tenant_id"`
	OrgID    string      `json:"org_id"`
}

type workbenchTaskRecord struct {
	Task     WorkbenchTask `json:"task"`
	TenantID string        `json:"tenant_id"`
	OrgID    string        `json:"org_id"`
}

type caseRelationRecord struct {
	Relation     CaseRelation `json:"relation"`
	TenantID     string       `json:"tenant_id"`
	OrgID        string       `json:"org_id"`
	RelatedOrgID string       `json:"related_org_id"`
	PairKey      string       `json:"pair_key"`
}

type knowledgeRecord struct {
	Item     Knowledge `json:"item"`
	TenantID string    `json:"tenant_id"`
	OrgID    string    `json:"org_id"`
}

type workbenchAuditRecord struct {
	Item     WorkbenchAudit `json:"item"`
	TenantID string         `json:"tenant_id"`
	OrgID    string         `json:"org_id"`
}

type reportHTMLModel struct {
	CaseTitle  string
	ReportJSON string
}
