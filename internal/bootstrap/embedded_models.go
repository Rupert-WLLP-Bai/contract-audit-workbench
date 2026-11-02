// File metadata (manifest mirror):
// Purpose: 实现 `embeddedGatewayModels`, `Transcribe`, `embeddedModelsFor` 等单进程服务装配与集成入口，连接已配置的服务组件和权限边界。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

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
