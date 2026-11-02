// File metadata (manifest mirror):
// Purpose: 注册并处理 `DevelopmentHandler` 等 Core HTTP 路由或会话命令，完成请求解析、服务端授权与响应映射。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"contractaudit/internal/store"
)

// DevelopmentHandler adds only the authenticated Model authority callback to
// the single-process compatibility API. It never mounts other service routes.
func DevelopmentHandler(st *store.Store, opt Options, mode string, otherCredentials ...string) (http.Handler, error) {
	if mode != "development" && mode != "test" {
		return nil, errors.New("development authority handler requires development or test mode")
	}
	base := Handler(st, opt)
	secret := opt.ModelAuthorityCredential
	if secret == "" {
		return base, nil
	}
	if len(secret) < 32 || strings.TrimSpace(secret) != secret {
		return nil, errors.New("model authority credential must contain at least 32 characters")
	}
	for _, other := range append(otherCredentials, opt.ExperienceWorkerCredential, opt.ToolClaimAuthorityCredential, opt.DocumentIntakeCredential) {
		if other != "" && secret == other {
			return nil, errors.New("model authority credential must be distinct from other credentials")
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/v1/model/operation-authority", operationAuthorityHandler(st, secret, "model"))
	mux.Handle("/", base)
	return mux, nil
}
