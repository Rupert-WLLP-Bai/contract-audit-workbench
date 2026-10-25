// Package decision provides a bounded, suggestion-only adapter for Laya System One.
// The deterministic rule engine remains authoritative: this package never emits a
// relation, removes a rule, or changes a rule's evidence.
package decision

import (
	"context"
	"net/http"
	"time"

	"contractaudit/internal/access"
)

const (
	// ModelAlias is pinned in requests so multilingual inputs do not depend on
	// upstream language routing.
	ModelAlias = "multilingual"
	// ExpectedMultilingualRevision is the reviewed root-bundle pin used by
	// laya-serve's built-in multilingual checkpoint at LayaSourceRevision. It is
	// expected provenance only; /v1/systemone does not report the loaded SHA.
	ExpectedMultilingualRevision = "55cf4c4ebb4ebe31b2550e8bdf3bd21b99753851"

	// LayaSourceRevision pins the upstream source reviewed for this adapter.
	LayaSourceRevision = "6f0496f797dc7127265d62e1543a62de507d943c"

	// ChoiceInstructions keeps the Laya and chat comparison task identical.
	ChoiceInstructions = "根据材料选择唯一应优先核查的重点；多个同等问题、材料含糊或超出范围时选择 manual_review。只做分流，不判断合同结论。"
)

type Mode string

const (
	ModeDisabled Mode = "disabled"
	ModeShadow   Mode = "shadow"
	ModeAssist   Mode = "assist"
)

type Capability string

const (
	// CapabilityChoice is bounded classification over caller-authorized labels.
	// It is always suggestion-only and never populates AcceptedRules.
	CapabilityChoice Capability = "choice"

	// CapabilityRuleRecommendation recommends the ordering of explicitly
	// allowed, non-mandatory deterministic rules.
	CapabilityRuleRecommendation Capability = "rule_recommendation"
)

type Status string

const (
	StatusDisabled          Status = "disabled"
	StatusDenied            Status = "denied"
	StatusInvalidInput      Status = "invalid_input"
	StatusApprovalRequired  Status = "approval_required"
	StatusAssistUnsupported Status = "assist_unsupported"
	StatusSuggested         Status = "suggested"
	StatusOrdered           Status = "ordered"
	StatusAbstained         Status = "abstained"
	StatusUnavailable       Status = "unavailable"
)

type Candidate struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// Input contains only caller-authorized bounded state and labels. Principal and
// Resource must be resolved by the trusted application layer, and the state must
// not contain evidence outside that authorization. The run references are
// correlation values, not claims made by Laya.
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
	MandatoryRuleIDs  []string
	RulePackVersion   string
	RulePackHash      string
	PolicyVersion     string
}

type AssistApproval struct {
	Capability    Capability
	PolicyVersion string
	MinConfidence float64
}

type Config struct {
	Mode Mode
	// Endpoint is a server-owned loopback base URL, normally
	// http://127.0.0.1:<port>/v1. Only literal loopback IPs and /v1 are accepted.
	Endpoint string
	Timeout  time.Duration
	// BearerToken is sent only as an Authorization header and never copied to
	// events or returned errors.
	BearerToken string
	// ModelRevision is the expected checkpoint revision configured by the
	// operator. It is distinct from observed runtime provenance.
	ModelRevision string
	// ShadowThreshold enables a local low-confidence abstention marker in shadow
	// mode. Nil means no local threshold was configured.
	ShadowThreshold *float64
	// AssistApproval is required for any assist-mode effect. Its capability and
	// policy version must match the request and its threshold is frozen at provider
	// construction.
	AssistApproval *AssistApproval
	Observer       Observer
	Clock          func() time.Time
}

type Suggestion struct {
	CandidateID       string             `json:"candidate_id"`
	Probabilities     map[string]float64 `json:"probabilities"`
	Confidence        *float64           `json:"confidence"`
	ConfidenceSource  string             `json:"confidence_source"`
	ResponseModel     string             `json:"response_model,omitempty"`
	RoutedCheckpoint  string             `json:"routed_checkpoint,omitempty"`
	UpstreamAbstained bool               `json:"upstream_abstained"`
}

type Result struct {
	Status     Status      `json:"status"`
	Suggestion *Suggestion `json:"suggestion,omitempty"`
	// AcceptedRules is an ordering overlay over the complete allowed,
	// non-mandatory candidate list. It is never a subset: deterministic callers
	// must still execute every configured rule, including mandatory rules.
	AcceptedRules []string      `json:"accepted_rules"`
	Event         DecisionEvent `json:"event"`
}

type DecisionEvent struct {
	RequestID             string     `json:"request_id"`
	DecisionID            string     `json:"decision_id"`
	RunID                 string     `json:"run_id"`
	AttemptID             string     `json:"attempt_id"`
	TraceID               string     `json:"trace_id"`
	TenantID              string     `json:"tenant_id"`
	CaseID                string     `json:"case_id"`
	ActorID               string     `json:"actor_id"`
	Capability            Capability `json:"capability"`
	Mode                  Mode       `json:"mode"`
	Status                Status     `json:"status"`
	RulePackVersion       string     `json:"rulepack_version"`
	RulePackHash          string     `json:"rulepack_hash"`
	ModelName             string     `json:"model_name"`
	ModelRevision         string     `json:"model_revision"`
	ModelRevisionSource   string     `json:"model_revision_source"`
	ObservedModelRevision *string    `json:"observed_model_revision"`
	ResponseModel         string     `json:"response_model"`
	RoutedCheckpoint      string     `json:"routed_checkpoint"`
	Recommendation        string     `json:"recommendation"`
	ConfidenceThreshold   *float64   `json:"confidence_threshold"`
	Confidence            *float64   `json:"confidence"`
	ConfidenceSource      string     `json:"confidence_source"`
	Abstention            string     `json:"abstention"`
	LatencyMS             float64    `json:"latency_ms"`
	FailureReason         string     `json:"failure_reason"`
	PolicyVersion         string     `json:"policy_version"`
}

// Observer receives the same event returned to the caller, with Eino/run/attempt
// correlation already attached. Implementations should avoid blocking inference.
type Observer interface {
	ObserveDecision(context.Context, DecisionEvent)
}

type ObserverFunc func(context.Context, DecisionEvent)

func (f ObserverFunc) ObserveDecision(ctx context.Context, event DecisionEvent) {
	f(ctx, event)
}

const (
	FailureNone                 = ""
	FailureAuthorization        = "authorization_denied"
	FailureInvalidInput         = "invalid_input"
	FailureApprovalRequired     = "approval_required"
	FailureAssistUnsupported    = "assist_capability_unsupported"
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
	MaxResponseBytes             = 128 * 1024
)

// SystemOneAdapter exposes the Laya-compatible HTTP transport independently of
// Provider so other bounded decision modules can reuse the same wire adapter.
type SystemOneAdapter struct {
	endpoint    string
	timeout     time.Duration
	bearerToken string
	client      *http.Client
}

// NewProvider validates configuration and creates the isolated HTTP adapter only
// for shadow or assist mode. An empty mode selects disabled mode.
func NewProvider(config Config) (*Provider, error) {
	return newProvider(config)
}

// Provider applies access checks, capability and candidate validation, confidence
// abstention, and the shadow/assist effect rules around the HTTP adapter.
type Provider struct {
	config  Config
	adapter *SystemOneAdapter
}

// Evaluate returns a safe status on every failure. It never returns an error that
// could suppress the deterministic caller's independent rule execution.
func (p *Provider) Evaluate(ctx context.Context, input Input) Result {
	return p.evaluate(ctx, input)
}

// NewSystemOneAdapter constructs an explicit loopback-only client for
// POST /v1/systemone. Redirects and environment proxies are disabled.
func NewSystemOneAdapter(endpoint string, timeout time.Duration, bearerToken string) (*SystemOneAdapter, error) {
	return newSystemOneAdapter(endpoint, timeout, bearerToken)
}
