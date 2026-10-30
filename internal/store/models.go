package store

import (
	"context"

	"contractaudit/internal/model"
)

// Models is the chat and image caller for one review. It is set before serving.
type Models interface {
	Name() string
	ConfigurationID() string
	Warranty(ctx context.Context, docs []model.Material) (model.Warranty, error)
	Transcribe(ctx context.Context, media string, body []byte) (string, error)
}

// UseModels installs the caller. Call it before serving, or from the endpoint
// watcher: each operation captures the current caller once so an in-flight
// attempt keeps the frozen identity it validated.
func (s *Store) UseModels(m Models) {
	s.modelsMu.Lock()
	s.models = m
	s.modelsMu.Unlock()
}

// currentModels returns the caller captured for one operation.
func (s *Store) currentModels() Models {
	s.modelsMu.RLock()
	defer s.modelsMu.RUnlock()
	return s.models
}

// modelScope pins the frozen configuration identity onto a delegated scope.
// A model client without an identity (fixtures, tests) leaves the scope as-is,
// so the gateway keeps accepting it.
func modelScope(scope model.DelegatedScope, models Models) model.DelegatedScope {
	if models != nil {
		scope.ConfigurationID = models.ConfigurationID()
		scope.ProviderURL = providerEndpointOf(models)
	}
	return scope
}

// providerEndpointOf returns the provider address a real client dispatches to.
// Test doubles and fixtures expose none, so their scopes stay unconstrained.
func providerEndpointOf(models Models) string {
	provider, ok := models.(interface{ ProviderEndpoint() string })
	if !ok {
		return ""
	}
	return provider.ProviderEndpoint()
}

// frozenConfigurationID is the identity recorded for a review. It only exists
// for callers that name a provider configuration; fixtures and test doubles
// keep their previous signature instead of inventing a provider address.
func frozenConfigurationID(models Models) string {
	if models == nil {
		return ""
	}
	if providerEndpointOf(models) == "" {
		return ""
	}
	return models.ConfigurationID()
}
