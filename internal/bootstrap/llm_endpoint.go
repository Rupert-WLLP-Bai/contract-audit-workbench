package bootstrap

import (
	"context"
	"log"
	"time"

	"contractaudit/internal/llmendpoint"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

const llmEndpointPollInterval = 2 * time.Second

// applyStoredLLMEndpoint merges the platform endpoint with the deployment
// identity. Worker and document clients keep the gateway URL and caller
// credential; only their upstream identity (used for signatures) and selected
// model ids change. Direct single-process clients also adopt the provider URL.
func applyStoredLLMEndpoint(base model.Config, service string, settings store.LLMEndpointSettings) model.Config {
	if !settings.Active() {
		return base
	}
	out := base
	out.ProviderURL = settings.BaseURL
	out.ChatModel = settings.ReviewModel
	out.OCRModel = settings.OCRModel
	if !base.Gateway {
		out.BaseURL = settings.BaseURL
		out.Gateway = false
		if service == "" {
			if key, err := llmendpoint.Open(settings.SecretCiphertext); err == nil {
				out.APIKey = key
			} else if len(settings.SecretCiphertext) > 0 {
				log.Printf("stored LLM credential cannot be opened; single-process model calls keep the environment credential")
			}
		} else {
			// Core and execution never call the provider directly. Keeping the
			// key out of their processes preserves the gateway boundary.
			out.APIKey = ""
		}
	}
	return out
}

// loadStoredLLMEndpoint returns the deployment configuration with a complete
// platform endpoint applied. A missing or incomplete row leaves the deployment
// identity untouched and reports its revision so the watcher can detect a
// later selection.
func loadStoredLLMEndpoint(ctx context.Context, st *store.Store, service string, base model.Config) (model.Config, int64) {
	settings, found, err := st.LLMEndpoint(ctx)
	if err != nil {
		log.Printf("LLM endpoint settings unavailable: %v", err)
		return base, 0
	}
	if !found {
		return base, 0
	}
	return applyStoredLLMEndpoint(base, service, settings), settings.Revision
}

// watchStoredLLMEndpoint applies endpoint changes without restarting every
// process. Callers capture the current model client at the start of an
// operation, so a running attempt keeps the frozen identity it validated.
func watchStoredLLMEndpoint(ctx context.Context, st *store.Store, service string, base model.Config, revision int64) (cancel context.CancelFunc, done <-chan struct{}) {
	watchCtx, stop := context.WithCancel(ctx)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(llmEndpointPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
			}
			settings, found, err := st.LLMEndpoint(watchCtx)
			if err != nil {
				continue
			}
			next := int64(0)
			if found {
				next = settings.Revision
			}
			if next == revision {
				continue
			}
			config := base
			if found {
				config = applyStoredLLMEndpoint(base, service, settings)
			}
			st.UseModels(model.New(config))
			revision = next
			log.Printf("applied LLM endpoint revision %d (active=%t)", revision, found && settings.Active())
		}
	}()
	return stop, finished
}
