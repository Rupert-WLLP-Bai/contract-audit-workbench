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

type llmEndpointStore interface {
	LLMEndpoint(context.Context) (store.LLMEndpointSettings, bool, error)
	UseModels(store.Models)
	HasDatabase() bool
}

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
				// The stored endpoint and the stored credential are one
				// identity. Pairing the new address with the environment key
				// would send the deployment credential to a provider that never
				// received it, so the whole stored endpoint is ignored instead.
				log.Printf("stored LLM credential cannot be opened; single-process model calls keep the deployment endpoint")
				return base
			}
		} else {
			// Core and execution never call the provider directly. Keeping the
			// key out of their processes preserves the gateway boundary.
			out.APIKey = ""
		}
	}
	return out
}

// newModelClient builds the model client for a resolved endpoint
// configuration. A stored operator endpoint driving direct provider calls is
// pinned to the guarded outbound client, so its connections land only on
// policy-verified addresses and never follow redirects. Deployment and
// gateway configurations keep the default client: environment URLs are not
// operator-supplied endpoints.
func newModelClient(config model.Config, storedDirect bool) *model.Client {
	if storedDirect {
		return model.NewWithHTTPClient(config, llmendpoint.GuardedClient(modelCallTimeout))
	}
	return model.New(config)
}

// modelCallTimeout mirrors the request timeout of the default model client.
const modelCallTimeout = 120 * time.Second

// loadStoredLLMEndpoint returns the deployment configuration with a complete
// platform endpoint applied. A missing or incomplete row leaves the deployment
// identity untouched and reports its revision so the watcher can detect a
// later selection. The boolean reports that the stored endpoint was adopted
// for direct provider calls.
func loadStoredLLMEndpoint(ctx context.Context, st llmEndpointStore, service string, base model.Config) (model.Config, int64, bool) {
	settings, found, err := st.LLMEndpoint(ctx)
	if err != nil {
		log.Printf("LLM endpoint settings unavailable: %v", err)
		return base, 0, false
	}
	if !found {
		return base, 0, false
	}
	applied := applyStoredLLMEndpoint(base, service, settings)
	return applied, settings.Revision, !base.Gateway && applied != base
}

// watchStoredLLMEndpoint applies endpoint changes without restarting every
// process. Callers capture the current model client at the start of an
// operation, so a running attempt keeps the frozen identity it validated.
func watchStoredLLMEndpoint(ctx context.Context, st llmEndpointStore, service string, base model.Config, revision int64) (cancel context.CancelFunc, done <-chan struct{}) {
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
			storedDirect := false
			if found {
				config = applyStoredLLMEndpoint(base, service, settings)
				storedDirect = !base.Gateway && config != base
			}
			st.UseModels(newModelClient(config, storedDirect))
			revision = next
			log.Printf("applied LLM endpoint revision %d (active=%t)", revision, found && settings.Active())
		}
	}()
	return stop, finished
}
