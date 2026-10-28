// Package advisory builds and evaluates the default-on Jev suggestion layer.
//
// Jev only reranks optional governed knowledge. It never selects the candidate
// set, changes a deterministic score, removes a required policy, or writes an
// audit conclusion. Every probability is mapped to a code-owned band, and any
// provider failure is an error that the caller must resolve with deterministic
// order rather than a guessed answer.
package advisory

import (
	"context"
	"errors"
	"fmt"

	"contractaudit/internal/agentflow"
	"contractaudit/internal/jev"
)

const (
	FeatureKnowledgeRerank = agentflow.AdvisoryFeatureKnowledgeRerank
	QuestionSetVersion     = agentflow.AdvisoryQuestionSetVersion
	EgressMinimized        = agentflow.AdvisoryEgressMinimized
)

var (
	ErrNotApplicable = errors.New("advisory feature is not applicable")
	ErrUnavailable   = errors.New("advisory provider is unavailable")
	ErrModelMismatch = errors.New("advisory observed model does not match the pinned model")
)

// Band is the code-owned consequence of one Noul. It is not a confidence and
// never a conclusion.
type Band string

const (
	BandRelevant    Band = "relevant"
	BandUncertain   Band = "uncertain"
	BandNotRelevant Band = "not_relevant"
	BandUnknown     Band = "unknown"
)

// BandFor applies the frozen 0.70/0.30 truth table. The uncertain band always
// has its own outcome and is never rounded to yes or no.
func BandFor(probability, thresholdHigh, thresholdLow float64) (Band, error) {
	if !(thresholdLow > 0 && thresholdHigh < 1 && thresholdLow < thresholdHigh) {
		return BandUnknown, errors.New("advisory thresholds are invalid")
	}
	if probability != probability || probability < 0 || probability > 1 {
		return BandUnknown, errors.New("advisory probability is outside [0,1]")
	}
	switch {
	case probability >= thresholdHigh:
		return BandRelevant, nil
	case probability <= thresholdLow:
		return BandNotRelevant, nil
	default:
		return BandUncertain, nil
	}
}

// BandRank controls the stable optional-candidate order. uncertain and unknown
// intentionally share a rank so they retain deterministic relative order.
func BandRank(band Band) int {
	switch band {
	case BandRelevant:
		return 0
	case BandUncertain, BandUnknown:
		return 1
	case BandNotRelevant:
		return 2
	default:
		return 1
	}
}

// ProviderRequest is one batched System One request. The provider is a pure
// transport: state, questions, model and egress have already been frozen.
type ProviderRequest struct {
	Feature   string
	Egress    string
	Model     string
	State     string
	Questions map[string]jev.NoulQuestion
}

// ProviderResponse is a validated answer envelope. A provider must return an
// error, never a synthetic answer, when the upstream response is incomplete.
type ProviderResponse struct {
	Model   string
	Usage   jev.Usage
	Answers map[string]jev.NoulAnswer
}

// Provider is the only external surface used by the suggestion layer. It is
// injected by the worker/bootstrap and can be replaced by a fake in tests.
type Provider interface {
	AskNouls(context.Context, ProviderRequest) (ProviderResponse, error)
}

// KnowledgeScope is the allowlisted frozen scope sent to Jev.
type KnowledgeScope struct {
	BusinessLine string
	Scenario     string
	ContractType string
}

// KnowledgeGap is one allowlisted gap label. The free-text question is never
// included in minimized egress.
type KnowledgeGap struct {
	Domain     string
	Capability string
	Reason     string
}

// KnowledgeCandidate is one governed optional item. Only the allowlisted
// descriptive fields are eligible for egress; identity, hashes and source text
// remain local.
type KnowledgeCandidate struct {
	Alias           string
	Kind            string
	Summary         string
	CheckAction     string
	MinimumEvidence string
}

// KnowledgeOmission records a candidate that could not be sent safely. The
// caller keeps its deterministic score and decision; the omission is evidence,
// not a judgment.
type KnowledgeOmission struct {
	Alias  string
	Reason string
}

// KnowledgeRequest is the complete frozen input and question batch. Its hash
// joins the reuse key so a wording, state, model or threshold change cannot
// reuse an older answer.
type KnowledgeRequest struct {
	Feature            string
	QuestionSetVersion string
	Egress             string
	Model              string
	State              string
	StateSHA256        string
	Questions          map[string]jev.NoulQuestion
	Candidates         []KnowledgeCandidate
	Omitted            []KnowledgeOmission
	Truncated          bool
	RequestSHA256      string
}

// KnowledgeResult is one validated batch. Probabilities are retained only for
// audit; consequences are read from Bands.
type KnowledgeResult struct {
	Model         string
	Usage         jev.Usage
	Probabilities map[string]float64
	Bands         map[string]Band
	RequestSHA256 string
}

// EvaluateKnowledge calls the provider and applies the truth table. It rejects
// a response whose observed model differs from the frozen model; a caller must
// fall back rather than mix two model versions in one run.
func EvaluateKnowledge(ctx context.Context, provider Provider, configuration agentflow.AdvisoryConfiguration, request KnowledgeRequest) (KnowledgeResult, error) {
	if provider == nil {
		return KnowledgeResult{}, ErrUnavailable
	}
	if err := configuration.Validate(); err != nil || !configuration.Supports(request.Feature) {
		return KnowledgeResult{}, fmt.Errorf("%w: frozen advisory configuration is invalid", ErrNotApplicable)
	}
	if request.Model != configuration.Model || request.RequestSHA256 == "" || len(request.Candidates) == 0 ||
		len(request.Candidates) > configuration.MaxCandidates {
		return KnowledgeResult{}, fmt.Errorf("%w: frozen advisory request is invalid", ErrNotApplicable)
	}
	response, err := provider.AskNouls(ctx, ProviderRequest{
		Feature: request.Feature, Egress: request.Egress, Model: request.Model,
		State: request.State, Questions: request.Questions,
	})
	if err != nil {
		return KnowledgeResult{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if response.Model != request.Model {
		return KnowledgeResult{}, ErrModelMismatch
	}
	if len(response.Answers) != len(request.Candidates) {
		return KnowledgeResult{}, fmt.Errorf("%w: answer count does not match candidates", ErrUnavailable)
	}
	result := KnowledgeResult{
		Model: response.Model, Usage: response.Usage,
		Probabilities: make(map[string]float64, len(request.Candidates)),
		Bands:         make(map[string]Band, len(request.Candidates)),
		RequestSHA256: request.RequestSHA256,
	}
	for _, candidate := range request.Candidates {
		answer, ok := response.Answers[candidate.Alias+"_relevance"]
		if !ok || answer.Model != response.Model {
			return KnowledgeResult{}, fmt.Errorf("%w: answer for one candidate is missing", ErrUnavailable)
		}
		band, err := BandFor(answer.Noul, configuration.ThresholdHigh, configuration.ThresholdLow)
		if err != nil {
			return KnowledgeResult{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		result.Probabilities[candidate.Alias] = answer.Noul
		result.Bands[candidate.Alias] = band
	}
	return result, nil
}
