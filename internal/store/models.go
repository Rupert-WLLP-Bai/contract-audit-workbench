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
