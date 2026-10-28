// Package decision defines the bounded, suggestion-only choice contract shared
// by shadow backends. The deterministic rule engine remains authoritative: this
// package never emits a relation, removes a rule, or changes a rule's evidence.
// It has no model transport and cannot bypass authorization; callers authorize,
// call their configured backend, and keep every answer suggestion-only.
package decision

import "contractaudit/internal/access"

const (
	// ChoiceInstructions keeps shadow backends on the same bounded task.
	ChoiceInstructions = "根据材料选择唯一应优先核查的重点；多个同等问题、材料含糊或超出范围时选择 manual_review。只做分流，不判断合同结论。"
)

type Mode string

const (
	ModeDisabled Mode = "disabled"
	ModeShadow   Mode = "shadow"
)

type Capability string

const (
	// CapabilityChoice is bounded classification over caller-authorized labels.
	// It is always suggestion-only and never fills AcceptedRules.
	CapabilityChoice Capability = "choice"
)

type Status string

const (
	StatusDisabled     Status = "disabled"
	StatusDenied       Status = "denied"
	StatusInvalidInput Status = "invalid_input"
	StatusSuggested    Status = "suggested"
	StatusAbstained    Status = "abstained"
	StatusUnavailable  Status = "unavailable"
)

type Candidate struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// Input contains only caller-authorized bounded state and labels. Principal and
// Resource must be resolved by the trusted application layer, and the state must
// not contain evidence outside that authorization. The run references are
// correlation values, not claims made by any model.
type Input struct {
	RequestID         string
	DecisionID        string
	RunID             string
	AttemptID         string
	TraceID           string
	Principal         access.Principal
	Resource          access.Resource
	Capability        Capability
	State             string
	AllowedCandidates []Candidate
	RulePackVersion   string
	RulePackHash      string
	PolicyVersion     string
}

// Suggestion is a bounded answer, never an audit conclusion.
type Suggestion struct {
	CandidateID       string             `json:"candidate_id"`
	Probabilities     map[string]float64 `json:"probabilities"`
	Confidence        *float64           `json:"confidence"`
	ConfidenceSource  string             `json:"confidence_source"`
	ResponseModel     string             `json:"response_model,omitempty"`
	UpstreamAbstained bool               `json:"upstream_abstained"`
}

type Result struct {
	Status     Status      `json:"status"`
	Suggestion *Suggestion `json:"suggestion,omitempty"`
	// AcceptedRules is always empty for a shadow answer. Deterministic callers
	// must execute every configured rule regardless of this field.
	AcceptedRules []string      `json:"accepted_rules"`
	Event         DecisionEvent `json:"event"`
}

type DecisionEvent struct {
	RequestID           string     `json:"request_id"`
	DecisionID          string     `json:"decision_id"`
	RunID               string     `json:"run_id"`
	AttemptID           string     `json:"attempt_id"`
	TraceID             string     `json:"trace_id"`
	TenantID            string     `json:"tenant_id"`
	CaseID              string     `json:"case_id"`
	ActorID             string     `json:"actor_id"`
	Capability          Capability `json:"capability"`
	Mode                Mode       `json:"mode"`
	Status              Status     `json:"status"`
	RulePackVersion     string     `json:"rulepack_version"`
	RulePackHash        string     `json:"rulepack_hash"`
	ModelName           string     `json:"model_name"`
	ModelRevisionSource string     `json:"model_revision_source"`
	ResponseModel       string     `json:"response_model"`
	Recommendation      string     `json:"recommendation"`
	Confidence          *float64   `json:"confidence"`
	ConfidenceSource    string     `json:"confidence_source"`
	Abstention          string     `json:"abstention"`
	LatencyMS           float64    `json:"latency_ms"`
	FailureReason       string     `json:"failure_reason"`
	PolicyVersion       string     `json:"policy_version"`
}

const (
	FailureNone                 = ""
	FailureAuthorization        = "authorization_denied"
	FailureInvalidInput         = "invalid_input"
	FailureTimeout              = "timeout"
	FailureCanceled             = "canceled"
	FailureTransport            = "transport_error"
	FailureRedirect             = "redirect_refused"
	FailureHTTPStatus           = "upstream_http_status"
	FailureOversizeResponse     = "response_too_large"
	FailureInvalidJSON          = "invalid_json"
	FailureInvalidAnswer        = "invalid_answer"
	FailureInvalidProbabilities = "invalid_probabilities"
)

const (
	MaxStateBytes                = 32 * 1024
	MaxCandidates                = 64
	MaxCandidateIDBytes          = 80
	MaxCandidateDescriptionBytes = 512
)

// DisabledResult preserves correlation in a rules-only report without calling a
// model. It is not a suggestion and never carries accepted rules.
func DisabledResult(input Input) Result {
	return Result{Status: StatusDisabled, AcceptedRules: []string{}, Event: DecisionEvent{
		RequestID: input.RequestID, DecisionID: input.DecisionID, RunID: input.RunID,
		AttemptID: input.AttemptID, TraceID: input.TraceID,
		TenantID: input.Resource.TenantID, CaseID: input.Resource.CaseID,
		ActorID: input.Principal.UserID, Capability: input.Capability, Mode: ModeDisabled,
		Status: StatusDisabled, RulePackVersion: input.RulePackVersion,
		RulePackHash: input.RulePackHash, PolicyVersion: input.PolicyVersion,
	}}
}
