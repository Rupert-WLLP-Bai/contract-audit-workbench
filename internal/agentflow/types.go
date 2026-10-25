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

const RuntimeVersion = "operator-flow-v1/eino-0.9.22"

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
	ErrDenied   = errors.New("case review denied")
	ErrInput    = errors.New("invalid frozen review input")
	ErrPlan     = errors.New("plan rejected before tool execution")
	ErrEvidence = errors.New("tool result failed evidence validation")
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
	ID         string `json:"id"`
	Domain     Domain `json:"domain"`
	Capability string `json:"capability"`
	Question   string `json:"question"`
}

// Principal and Case must be resolved by a trusted application boundary. Never
// bind this whole structure directly from an unauthenticated HTTP JSON body.
type Request struct {
	RequestID            string                `json:"request_id"`
	AttemptID            string                `json:"attempt_id,omitempty"`
	Principal            access.Principal      `json:"principal"`
	Case                 access.Resource       `json:"case"`
	Scope                []Domain              `json:"scope"`
	Gaps                 []Gap                 `json:"gaps"`
	Evidence             []Evidence            `json:"evidence"`
	SourceSnapshot       *SourceSnapshot       `json:"source_snapshot,omitempty"`
	RulePack             *RulePackIdentity     `json:"rulepack,omitempty"`
	CanonicalEvaluations []CanonicalEvaluation `json:"canonical_evaluations,omitempty"`
}

type CanonicalEvaluation struct {
	ID           string   `json:"id"`
	Domain       Domain   `json:"domain"`
	CapabilityID string   `json:"capability_id"`
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
	Scope            []Domain    `json:"scope"`
	Gaps             []Gap       `json:"gaps"`
	Tools            []ToolSpec  `json:"tools"`
	PreviouslyCalled []PriorCall `json:"previously_called"`
	RemainingCalls   int         `json:"remaining_calls"`
}

type Call struct {
	ToolID string `json:"tool_id"`
	Domain Domain `json:"domain"`
	GapID  string `json:"gap_id"`
	Reason string `json:"reason"`
}

type Plan struct {
	Calls []Call `json:"calls"`
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
	Sequence   int    `json:"sequence"`
	Round      int    `json:"round"`
	Kind       string `json:"kind"`
	ToolID     string `json:"tool_id,omitempty"`
	CallID     string `json:"call_id,omitempty"`
	Domain     Domain `json:"domain,omitempty"`
	Code       string `json:"code"`
	DurationMS int64  `json:"duration_ms,omitempty"`
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
	Planner              Planner
	Tools                []Tool
	MaxCalls             int
	MaxRounds            int
	MaxEvidenceBytes     int
	Timeout              time.Duration
	ToolTimeout          time.Duration
	PlannerConfiguration PlannerConfiguration
	Clock                func() time.Time
}
