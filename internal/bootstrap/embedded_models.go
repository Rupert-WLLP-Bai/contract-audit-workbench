package bootstrap

import (
	"context"
	"os"
	"strings"

	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

// embeddedGatewayModels keeps worker and document credentials on their own
// model hops while preserving the worker client's planner and review methods.
type embeddedGatewayModels struct {
	*model.Client
	document *model.Client
}

func (m embeddedGatewayModels) Transcribe(ctx context.Context, media string, body []byte) (string, error) {
	return m.document.Transcribe(ctx, media, body)
}

func embeddedModelsFor(config model.Config, service string, worker *model.Client) store.Models {
	if service != "" || !config.Gateway {
		return worker
	}
	document := config
	document.APIKey = strings.TrimSpace(os.Getenv("DOCUMENT_MODEL_TOKEN"))
	return embeddedGatewayModels{Client: worker, document: newModelClient(document, false)}
}
