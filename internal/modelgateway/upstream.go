package modelgateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"contractaudit/internal/llmendpoint"
	"contractaudit/internal/model"
)

const (
	upstreamCacheTTL   = 2 * time.Second
	upstreamReadBudget = 2 * time.Second
)

// upstreamConfig is the provider identity a single gateway call is pinned to.
// Revision 0 means the deployment environment; stored rows start at 1.
type upstreamConfig struct {
	BaseURL   string
	APIKey    string
	ChatModel string
	OCRModel  string
	Revision  int64
}

func (s *Service) staticUpstream() upstreamConfig {
	return upstreamConfig{
		BaseURL: s.config.UpstreamBaseURL, APIKey: s.config.UpstreamAPIKey,
		ChatModel: s.config.ChatModel, OCRModel: s.config.OCRModel,
	}
}

func (u upstreamConfig) allows(requested string) bool {
	requested = model.GatewayID(requested)
	return requested != "" &&
		(requested == model.GatewayID(u.ChatModel) || requested == model.GatewayID(u.OCRModel))
}

// upstreamFor resolves the active platform endpoint, falling back to the
// deployment configuration when no complete row exists. A stored row that
// cannot be decrypted is an error rather than a silent fallback: routing a
// review to a different provider than the frozen signature names would be
// worse than failing the call.
func (s *Service) upstreamFor(ctx context.Context) (upstreamConfig, error) {
	if s.config.Mode == "fixture" {
		return s.staticUpstream(), nil
	}
	if s.store == nil || !s.store.HasDatabase() {
		return s.staticUpstream(), nil
	}
	s.upstreamMu.Lock()
	if !s.upstreamAt.IsZero() && time.Since(s.upstreamAt) < upstreamCacheTTL {
		config, err := s.upstreamCfg, s.upstreamErr
		s.upstreamMu.Unlock()
		return config, err
	}
	s.upstreamMu.Unlock()

	config, err := s.loadStoredUpstream(ctx)
	s.upstreamMu.Lock()
	if err == nil || (!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)) {
		s.upstreamCfg, s.upstreamErr = config, err
		// A canceled caller never becomes the cached answer for the next
		// request. Leaving upstreamAt untouched also forces a fresh read.
		s.upstreamAt = time.Now()
	}
	s.upstreamMu.Unlock()
	return config, err
}

func (s *Service) loadStoredUpstream(ctx context.Context) (upstreamConfig, error) {
	fallback := s.staticUpstream()
	readCtx, cancel := context.WithTimeout(ctx, upstreamReadBudget)
	defer cancel()
	settings, found, err := s.store.LLMEndpoint(readCtx)
	if err != nil {
		return fallback, fmt.Errorf("read stored LLM endpoint: %w", err)
	}
	if !found || !settings.Active() {
		return fallback, nil
	}
	key := ""
	if settings.HasAPIKey() {
		key, err = llmendpoint.Open(settings.SecretCiphertext)
		if err != nil {
			return fallback, errors.New("stored LLM endpoint credential cannot be opened")
		}
	}
	return upstreamConfig{
		BaseURL: settings.BaseURL, APIKey: key,
		ChatModel: settings.ReviewModel, OCRModel: settings.OCRModel,
		Revision: settings.Revision,
	}, nil
}
