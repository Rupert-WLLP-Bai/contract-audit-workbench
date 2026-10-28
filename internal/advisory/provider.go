package advisory

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"contractaudit/internal/agentflow"
	"contractaudit/internal/jev"
	"contractaudit/internal/model"
)

// DirectProvider is used only by single-process local development and the
// synthetic evaluation command. A deployed worker must use GatewayProvider so
// every call is budgeted and scoped by the model gateway.
type DirectProvider struct {
	client *jev.Client
}

func NewDirectProvider(config jev.Config) (*DirectProvider, error) {
	client, err := jev.New(config)
	if err != nil {
		return nil, err
	}
	return &DirectProvider{client: client}, nil
}

// IdentifiedProvider reports the transport identity frozen in the planner
// configuration. The store refuses to apply a judgment when the running
// transport and the frozen provider disagree.
type IdentifiedProvider interface {
	ProviderIdentity() string
}

func (p *DirectProvider) ProviderIdentity() string { return agentflow.AdvisoryProviderDirect }

func (p *DirectProvider) AskNouls(ctx context.Context, request ProviderRequest) (ProviderResponse, error) {
	if p == nil || p.client == nil {
		return ProviderResponse{}, ErrUnavailable
	}
	if request.Model != p.client.Model() {
		return ProviderResponse{}, ErrModelMismatch
	}
	if err := ValidateRequestShape(request); err != nil {
		return ProviderResponse{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	answers, meta, err := p.client.AskNouls(ctx, request.State, request.Questions)
	if err != nil {
		return ProviderResponse{}, err
	}
	return ProviderResponse{Model: meta.Model, Usage: meta.Usage, Answers: answers}, nil
}

// GatewayProvider sends the same batch through the model gateway. The gateway
// authenticates the worker, enforces the minimized classification and records
// the call in the existing budget ledger.
type GatewayProvider struct {
	client *model.Client
}

func NewGatewayProvider(client *model.Client) (*GatewayProvider, error) {
	if client == nil {
		return nil, errors.New("model gateway client is required")
	}
	return &GatewayProvider{client: client}, nil
}

func (p *GatewayProvider) ProviderIdentity() string { return agentflow.AdvisoryProviderGateway }

func (p *GatewayProvider) AskNouls(ctx context.Context, request ProviderRequest) (ProviderResponse, error) {
	if p == nil || p.client == nil {
		return ProviderResponse{}, ErrUnavailable
	}
	if err := ValidateRequestShape(request); err != nil {
		return ProviderResponse{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	answers, meta, err := p.client.SystemOne(ctx, model.SystemOneRequest{
		Model: request.Model, State: request.State, Questions: request.Questions,
		Feature: request.Feature, Egress: request.Egress,
	})
	if err != nil {
		return ProviderResponse{}, err
	}
	return ProviderResponse{Model: meta.Model, Usage: meta.Usage, Answers: answers}, nil
}

// FixtureProvider answers deterministically without a network call. It proves
// the integration and the fallback, never model quality.
type FixtureProvider struct{}

func (FixtureProvider) ProviderIdentity() string { return agentflow.AdvisoryProviderFixture }

func (FixtureProvider) AskNouls(_ context.Context, request ProviderRequest) (ProviderResponse, error) {
	if request.Model != agentflow.AdvisoryFixtureModel {
		return ProviderResponse{}, ErrModelMismatch
	}
	if err := ValidateRequestShape(request); err != nil {
		return ProviderResponse{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	answers := make(map[string]jev.NoulAnswer, len(request.Questions))
	for questionID := range request.Questions {
		index := fixtureIndex(questionID)
		probability := 0.50
		switch index % 3 {
		case 0:
			probability = 0.82
		case 1:
			probability = 0.12
		}
		answers[questionID] = jev.NoulAnswer{Noul: probability, Model: agentflow.AdvisoryFixtureModel}
	}
	return ProviderResponse{Model: agentflow.AdvisoryFixtureModel, Answers: answers}, nil
}

func fixtureIndex(questionID string) int {
	trimmed := strings.TrimSuffix(questionID, "_relevance")
	trimmed = strings.TrimPrefix(trimmed, "candidate_")
	index, err := strconv.Atoi(trimmed)
	if err != nil || index < 0 {
		return 0
	}
	return index
}
