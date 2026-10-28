// Package agentflow provides a bounded Eino composition for operator audit
// planning. It is a C0 component, not the legacy HTTP application's runtime.
package agentflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/docir"
)

const RuntimeVersion = "operator-flow-v3/eino-0.9.22"

type Domain string

const (
	Compliance   Domain = "compliance"
	Commercial   Domain = "commercial"
	Technical    Domain = "technical"
	Fulfillment  Domain = "fulfillment"
	Counterparty Domain = "counterparty"
)

func Domains() []Domain {
	return []Domain{Compliance, Commercial, Technical, Fulfillment, Counterparty}
}

func validDomain(domain Domain) bool {
	for _, value := range Domains() {
		if domain == value {
			return true
		}
	}
	return false
}

var (
	ErrDenied                = errors.New("case review denied")
	ErrInput                 = errors.New("invalid frozen review input")
	ErrPlan                  = errors.New("plan rejected before tool execution")
	ErrEvidence              = errors.New("tool result failed evidence validation")
	ErrEventSink             = errors.New("agent review event sink failed")
	ErrPlanningAuthorization = errors.New("planner authorization failed")
	ErrKnowledgeRead         = errors.New("frozen knowledge read failed")
)

type Evidence struct {
	ID                   string `json:"id"`
	TenantID             string `json:"tenant_id"`
	CaseID               string `json:"case_id"`
	Version              string `json:"version"`
	Role                 string `json:"role"`
	Text                 string `json:"text"`
	SHA256               string `json:"sha256"`
	DocumentID           string `json:"document_id,omitempty"`
	ParsedDocumentID     string `json:"parsed_document_id,omitempty"`
	OriginalDigest       string `json:"original_digest,omitempty"`
	NormalizedDigest     string `json:"normalized_digest,omitempty"`
	ParserID             string `json:"parser_id,omitempty"`
	ParserVersion        string `json:"parser_version,omitempty"`
	ConfigHash           string `json:"config_hash,omitempty"`
	NormalizationVersion string `json:"normalization_version,omitempty"`
}

func TextHash(text string) string {
	digest := sha256.Sum256([]byte(text))
	return hex.EncodeToString(digest[:])
}

// NewEvidence records an in-memory normalized snapshot for component execution.
// Persisting immutable ParsedDocument history is still a C1 integration task.
func NewEvidence(id, version, role, raw string, resource access.Resource) Evidence {
	text := docir.Build(raw).NormalizedText
	return Evidence{ID: id, Version: version, Role: role, Text: text,
		TenantID: resource.TenantID, CaseID: resource.CaseID, SHA256: TextHash(text)}
}

type Gap struct {
	ID                string `json:"id"`
	Domain            Domain `json:"domain"`
	Capability        string `json:"capability"`
	Question          string `json:"question"`
	Mandatory         bool   `json:"mandatory,omitempty"`
	IncompleteReason  string `json:"incomplete_reason,omitempty"`
	QuestionTruncated bool   `json:"question_truncated,omitempty"`
}

// Principal and Case must be resolved by a trusted application boundary. Never
// bind this whole structure directly from an unauthenticated HTTP JSON body.
type Request struct {
	RequestID               string                    `json:"request_id"`
	AttemptID               string                    `json:"attempt_id,omitempty"`
	Principal               access.Principal          `json:"principal"`
	Case                    access.Resource           `json:"case"`
	Scope                   []Domain                  `json:"scope"`
	Gaps                    []Gap                     `json:"gaps"`
	Evidence                []Evidence                `json:"evidence"`
	SourceSnapshot          *SourceSnapshot           `json:"source_snapshot,omitempty"`
	RulePack                *RulePackIdentity         `json:"rulepack,omitempty"`
	Knowledge               []KnowledgeReference      `json:"knowledge,omitempty"`
	KnowledgeSelection      *KnowledgeSelectionRecord `json:"knowledge_selection,omitempty"`
	VerifiedFacts           []VerifiedFact            `json:"verified_facts,omitempty"`
	PriorObservations       []PriorToolResult         `json:"prior_observations,omitempty"`
	PriorKnowledgeReadCount int                       `json:"prior_knowledge_read_count,omitempty"`
	PriorEventCount         int                       `json:"prior_event_count,omitempty"`
	PriorElapsedMS          *int64                    `json:"prior_elapsed_ms,omitempty"`
	CanonicalEvaluations    []CanonicalEvaluation     `json:"canonical_evaluations,omitempty"`
}

// KnowledgeReference is supplied only by the trusted attempt-snapshot resolver.
// A summary is context, not an authorization grant or a substitute for the
// immutable source. Truncated is true if any supplied field is incomplete.
type KnowledgeReference struct {
	KnowledgeID            string `json:"knowledge_id"`
	VersionID              string `json:"version_id"`
	ContentSHA256          string `json:"content_sha256"`
	Kind                   string `json:"kind"`
	Required               bool   `json:"required"`
	Summary                string `json:"summary,omitempty"`
	Applicability          string `json:"applicability,omitempty"`
	Exclusions             string `json:"exclusions,omitempty"`
	CheckAction            string `json:"check_action,omitempty"`
	MinimumEvidence        string `json:"minimum_evidence,omitempty"`
	Counterexamples        string `json:"counterexamples,omitempty"`
	SemanticChecks         string `json:"semantic_checks,omitempty"`
	SourceSpans            string `json:"source_spans,omitempty"`
	RequiresFactEvaluation bool   `json:"requires_fact_evaluation"`
	Truncated              bool   `json:"truncated"`
}

// KnowledgeSelectionRecord describes selection and delivery, not knowledge use
// or a conclusion. Only approved frozen identities and input hashes are kept.
type KnowledgeSelectionRecord struct {
	SnapshotSHA256 string                   `json:"snapshot_sha256"`
	Algorithm      string                   `json:"algorithm"`
	PolicyStatus   string                   `json:"policy_status"`
	PolicyReason   string                   `json:"policy_reason"`
	Input          KnowledgeSelectionInput  `json:"input"`
	Candidates     []KnowledgeCandidate     `json:"candidates"`
	Budget         KnowledgeSelectionBudget `json:"budget"`
	PlannerRounds  []KnowledgePlanningRound `json:"planner_rounds"`
	Reads          []KnowledgeReadRecord    `json:"reads"`
	Advisory       *KnowledgeAdvisoryRecord `json:"advisory,omitempty"`
}

type KnowledgeSelectionInput struct {
	BusinessLine string                   `json:"business_line"`
	Scenario     string                   `json:"scenario"`
	ContractType string                   `json:"contract_type"`
	Gaps         []KnowledgeInputGap      `json:"gaps"`
	Evidence     []KnowledgeInputEvidence `json:"evidence"`
}

type KnowledgeInputGap struct {
	GapID      string `json:"gap_id"`
	Domain     Domain `json:"domain"`
	Capability string `json:"capability"`
	Question   string `json:"question"`
}

type KnowledgeInputEvidence struct {
	EvidenceID   string `json:"evidence_id"`
	Role         string `json:"role"`
	SHA256       string `json:"sha256"`
	ScannedRunes int    `json:"scanned_runes"`
	TotalRunes   int    `json:"total_runes"`
}

type KnowledgeIdentity struct {
	KnowledgeID   string `json:"knowledge_id"`
	VersionID     string `json:"version_id"`
	ContentSHA256 string `json:"content_sha256"`
}

type KnowledgeCandidate struct {
	KnowledgeID            string                      `json:"knowledge_id"`
	VersionID              string                      `json:"version_id"`
	ContentSHA256          string                      `json:"content_sha256"`
	Kind                   string                      `json:"kind"`
	Required               bool                        `json:"required"`
	Score                  int                         `json:"score"`
	Decision               string                      `json:"decision"`
	Reason                 string                      `json:"reason"`
	Truncated              bool                        `json:"truncated"`
	RequiresFactEvaluation bool                        `json:"requires_fact_evaluation"`
	Advisory               *KnowledgeAdvisoryCandidate `json:"advisory,omitempty"`
}

// KnowledgeAdvisoryRecord is the audit trace of one optional rerank. It never
// carries state or provider answers; only bounded probabilities, bands and
// code-owned consequences are kept.
type KnowledgeAdvisoryRecord struct {
	Mode               string                       `json:"mode"`
	Feature            string                       `json:"feature"`
	Provider           string                       `json:"provider"`
	ConfigurationID    string                       `json:"configuration_id"`
	ModelRequested     string                       `json:"model_requested,omitempty"`
	ModelObserved      string                       `json:"model_observed,omitempty"`
	QuestionSetVersion string                       `json:"question_set_version"`
	Egress             string                       `json:"egress"`
	RequestSHA256      string                       `json:"request_sha256,omitempty"`
	JudgmentID         string                       `json:"judgment_id,omitempty"`
	Status             string                       `json:"status"`
	ErrorCode          string                       `json:"error_code,omitempty"`
	Truncated          bool                         `json:"truncated,omitempty"`
	Candidates         []KnowledgeAdvisoryCandidate `json:"candidates"`
}

// KnowledgeAdvisoryCandidate describes the suggestion for one optional
// candidate. Probability is nil when no usable judgment exists.
type KnowledgeAdvisoryCandidate struct {
	KnowledgeID   string   `json:"knowledge_id"`
	VersionID     string   `json:"version_id"`
	ContentSHA256 string   `json:"content_sha256"`
	Alias         string   `json:"alias"`
	Probability   *float64 `json:"probability,omitempty"`
	Band          string   `json:"band"`
	RankBefore    int      `json:"rank_before"`
	RankAfter     int      `json:"rank_after"`
	Reason        string   `json:"reason,omitempty"`
}

type KnowledgeSelectionBudget struct {
	ReferenceLimit           int  `json:"reference_limit"`
	FieldBytes               int  `json:"field_bytes"`
	EvidenceRunesPerDocument int  `json:"evidence_runes_per_document"`
	RequiredCount            int  `json:"required_count"`
	OptionalCandidates       int  `json:"optional_candidates"`
	OptionalSelected         int  `json:"optional_selected"`
	OptionalOmitted          int  `json:"optional_omitted"`
	AcceptanceOmitted        int  `json:"acceptance_omitted"`
	AcceptanceTruncated      bool `json:"acceptance_truncated"`
	ContentBytesLimit        int  `json:"content_bytes_limit"`
	ContentBytesUsed         int  `json:"content_bytes_used"`
	ReadCallsLimit           int  `json:"read_calls_limit"`
	ReadMaxRunes             int  `json:"read_max_runes"`
	ReadMaxBytes             int  `json:"read_max_bytes"`
	PriorReadCalls           int  `json:"prior_read_calls"`
	PriorReadReservedBytes   int  `json:"prior_read_reserved_bytes"`
}

type KnowledgePlanningRound struct {
	Round                    int                     `json:"round"`
	ReferencesProvided       []KnowledgeIdentity     `json:"references_provided"`
	ReadObservationsProvided []KnowledgeReadIdentity `json:"read_observations_provided"`
	Outcome                  string                  `json:"outcome"`
}

const (
	KnowledgeReadMaxCalls = 4
	KnowledgeReadMaxRunes = 512
	KnowledgeReadMaxBytes = 2048
)

// Offsets are Unicode codepoint positions in the immutable approved body.
type KnowledgeReadRequest struct {
	KnowledgeID   string `json:"knowledge_id"`
	VersionID     string `json:"version_id"`
	ContentSHA256 string `json:"content_sha256"`
	Start         int    `json:"start"`
	End           int    `json:"end"`
}

type KnowledgeReadIdentity struct {
	KnowledgeID   string `json:"knowledge_id"`
	VersionID     string `json:"version_id"`
	ContentSHA256 string `json:"content_sha256"`
	Start         int    `json:"start"`
	End           int    `json:"end"`
	TextSHA256    string `json:"text_sha256"`
}

type KnowledgeReadObservation struct {
	KnowledgeReadIdentity
	Text string `json:"text"`
}

type KnowledgeReadRecord struct {
	KnowledgeID   string `json:"knowledge_id"`
	VersionID     string `json:"version_id"`
	ContentSHA256 string `json:"content_sha256"`
	Round         int    `json:"round"`
	Start         int    `json:"start"`
	End           int    `json:"end"`
	Bytes         int    `json:"bytes"`
	Status        string `json:"status"`
	TextSHA256    string `json:"text_sha256"`
}

type KnowledgeReadBudget struct {
	Remaining int `json:"remaining"`
	MaxRunes  int `json:"max_runes"`
	MaxBytes  int `json:"max_bytes"`
}

// VerifiedFact is a typed value revalidated against this attempt's frozen text.
// It does not by itself prove a comparison or a broader audit claim.
type VerifiedFact struct {
	GapID  string       `json:"gap_id"`
	Domain Domain       `json:"domain"`
	Fact   SemanticFact `json:"fact"`
}

// PriorToolResult is an earlier call from this same attempt, supplied only by
// the trusted execution owner. The list must include every earlier call,
// including failures, so the runtime can preserve the call and round budgets.
// Successful findings are revalidated against the frozen sources before use.
type PriorToolResult struct {
	Round       int       `json:"round"`
	ToolID      string    `json:"tool_id"`
	ToolVersion string    `json:"tool_version"`
	GapID       string    `json:"gap_id"`
	Findings    []Finding `json:"findings,omitempty"`
	FailureCode string    `json:"failure_code,omitempty"`
}

type PlannerFact struct {
	GapID      string `json:"gap_id"`
	Domain     Domain `json:"domain"`
	FactID     string `json:"fact_id"`
	Type       string `json:"semantic_type"`
	Value      string `json:"value"`
	Unit       string `json:"unit,omitempty"`
	Currency   string `json:"currency,omitempty"`
	EvidenceID string `json:"evidence_id"`
	Version    string `json:"version"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
	Truncated  bool   `json:"truncated"`
}

type MandatoryCheck struct {
	GapID      string `json:"gap_id"`
	Domain     Domain `json:"domain"`
	Capability string `json:"capability"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
}

// ToolObservation is a validated tool output, not an independently verified
// claim. Facts and located citations are separately identified.
type ToolObservation struct {
	Round       int      `json:"round"`
	CallID      string   `json:"call_id"`
	GapID       string   `json:"gap_id"`
	ToolID      string   `json:"tool_id"`
	Relation    string   `json:"reported_relation"`
	ReasonCode  string   `json:"reason_code,omitempty"`
	Summary     string   `json:"summary"`
	ClaimStatus string   `json:"claim_status"`
	FactIDs     []string `json:"fact_ids"`
	EvidenceIDs []string `json:"located_evidence_ids"`
	Truncated   bool     `json:"truncated"`
}

type PlannerContextTruncation struct {
	KnowledgeOmitted    int  `json:"knowledge_omitted"`
	FactsOmitted        int  `json:"facts_omitted"`
	ObservationsOmitted int  `json:"observations_omitted"`
	FieldsTruncated     bool `json:"fields_truncated"`
}

type CanonicalEvaluation struct {
	ID           string   `json:"id"`
	Domain       Domain   `json:"domain"`
	CapabilityID string   `json:"capability_id"`
	Mandatory    bool     `json:"mandatory,omitempty"`
	Relation     string   `json:"relation"`
	ReasonCode   string   `json:"reason_code"`
	EvidenceIDs  []string `json:"evidence_ids"`
}

type ToolSpec struct {
	ID           string   `json:"id"`
	Domain       Domain   `json:"domain"`
	Version      string   `json:"version"`
	Effect       string   `json:"effect"`
	Capabilities []string `json:"capabilities"`
}

type PriorCall struct {
	ToolID               string `json:"tool_id"`
	GapID                string `json:"gap_id"`
	NewEvidenceAvailable bool   `json:"new_evidence_available"`
}

type PlanInput struct {
	Scope                 []Domain                   `json:"scope"`
	Gaps                  []Gap                      `json:"gaps"`
	Tools                 []ToolSpec                 `json:"tools"`
	PreviouslyCalled      []PriorCall                `json:"previously_called"`
	RemainingCalls        int                        `json:"remaining_calls"`
	Knowledge             []KnowledgeReference       `json:"knowledge"`
	KnowledgePolicyStatus string                     `json:"knowledge_policy_status,omitempty"`
	KnowledgePolicyReason string                     `json:"knowledge_policy_reason,omitempty"`
	KnowledgeReadBudget   *KnowledgeReadBudget       `json:"knowledge_read_budget,omitempty"`
	KnowledgeObservations []KnowledgeReadObservation `json:"knowledge_observations,omitempty"`
	VerifiedFacts         []PlannerFact              `json:"verified_facts"`
	MandatoryChecks       []MandatoryCheck           `json:"mandatory_checks"`
	Observations          []ToolObservation          `json:"observations"`
	Truncation            PlannerContextTruncation   `json:"truncation"`
}

type Call struct {
	ToolID string `json:"tool_id"`
	Domain Domain `json:"domain"`
	GapID  string `json:"gap_id"`
	Reason string `json:"reason"`
}

type Plan struct {
	Calls          []Call                 `json:"calls"`
	KnowledgeReads []KnowledgeReadRequest `json:"knowledge_reads,omitempty"`
}

type Planner interface {
	Name() string
	Plan(context.Context, PlanInput) (Plan, error)
}

type Citation struct {
	EvidenceID       string `json:"evidence_id"`
	Version          string `json:"version"`
	Quote            string `json:"quote"`
	Start            int    `json:"start"`
	End              int    `json:"end"`
	DocumentID       string `json:"document_id,omitempty"`
	ParsedDocumentID string `json:"parsed_document_id,omitempty"`
	NormalizedDigest string `json:"normalized_digest,omitempty"`
	Status           string `json:"status,omitempty"`
	Page             *int   `json:"page,omitempty"`
	ElementID        string `json:"element_id,omitempty"`
}

type SemanticFact struct {
	ID       string   `json:"id"`
	Type     string   `json:"semantic_type"`
	Value    string   `json:"value"`
	Unit     string   `json:"unit,omitempty"`
	Currency string   `json:"currency,omitempty"`
	Citation Citation `json:"citation"`
}

type Finding struct {
	Domain     Domain         `json:"domain"`
	Capability string         `json:"capability"`
	RuleID     string         `json:"rule_id"`
	Relation   string         `json:"relation"`
	Summary    string         `json:"summary"`
	ReasonCode string         `json:"reason_code,omitempty"`
	Facts      []SemanticFact `json:"facts,omitempty"`
	Citations  []Citation     `json:"citations"`
}

type ToolInput struct {
	TenantID      string
	CaseID        string
	ActorID       string
	Gap           Gap
	Evidence      []Evidence
	PriorFindings []Finding
}

type Tool interface {
	Spec() ToolSpec
	Execute(context.Context, ToolInput) ([]Finding, error)
}

type Event struct {
	EventID    string    `json:"event_id"`
	Sequence   int       `json:"sequence"`
	Round      int       `json:"round"`
	Kind       string    `json:"kind"`
	ToolID     string    `json:"tool_id,omitempty"`
	GapID      string    `json:"gap_id,omitempty"`
	CallID     string    `json:"call_id,omitempty"`
	Domain     Domain    `json:"domain,omitempty"`
	Code       string    `json:"code"`
	OccurredAt time.Time `json:"occurred_at"`
	DurationMS int64     `json:"duration_ms,omitempty"`
}

type DomainResult struct {
	Domain   Domain    `json:"domain"`
	Status   string    `json:"status"`
	Findings []Finding `json:"findings"`
}

type Report struct {
	SchemaVersion    string         `json:"schema_version"`
	RuntimeVersion   string         `json:"runtime_version"`
	Planner          string         `json:"planner"`
	RequestID        string         `json:"request_id"`
	InputSHA256      string         `json:"input_sha256"`
	PolicyVersion    string         `json:"policy_version"`
	Status           string         `json:"status"`
	FallbackUsed     bool           `json:"fallback_used"`
	Plan             Plan           `json:"plan"`
	Rounds           []PlanRound    `json:"rounds"`
	ToolCalls        int            `json:"tool_calls"`
	UnresolvedGapIDs []string       `json:"unresolved_gap_ids"`
	Domains          []DomainResult `json:"domains"`
	Events           []Event        `json:"events"`
	Limitations      []string       `json:"limitations"`
	Projection       *AgentReview   `json:"-"`
}

type Config struct {
	Planner          Planner
	Tools            []Tool
	MaxCalls         int
	MaxRounds        int
	MaxEvidenceBytes int
	Timeout          time.Duration
	ToolTimeout      time.Duration
	// AuthorizePlanning rechecks current actor and frozen knowledge before each round.
	AuthorizePlanning func(context.Context) error
	// ReadKnowledge must reauthorize the stored snapshot and return only the requested span.
	ReadKnowledge        func(context.Context, KnowledgeReadRequest) (string, error)
	PlannerConfiguration PlannerConfiguration
	// EventSink runs synchronously at occurrence; an error stops the run.
	EventSink func(context.Context, Event) error
	Clock     func() time.Time
}
