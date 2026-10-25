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

// UseModels installs the caller. Call it before the server accepts traffic.
func (s *Store) UseModels(m Models) { s.models = m }
