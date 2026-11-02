// File metadata (manifest mirror):
// Purpose: 注册并处理 `TestDevelopmentHandlerOnlyMountsPrivateModelAuthority` 等 Core HTTP 路由或会话命令，完成请求解析、服务端授权与响应映射。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"contractaudit/internal/store"
)

func TestDevelopmentHandlerOnlyMountsPrivateModelAuthority(t *testing.T) {
	secret := strings.Repeat("m", 32)
	if _, err := DevelopmentHandler(&store.Store{}, Options{ModelAuthorityCredential: secret}, "production"); err == nil {
		t.Fatal("production enabled development authority")
	}
	if _, err := DevelopmentHandler(&store.Store{}, Options{ModelAuthorityCredential: secret}, "test", secret); err == nil {
		t.Fatal("reused credential accepted")
	}
	h, err := DevelopmentHandler(&store.Store{}, Options{ModelAuthorityCredential: secret}, "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/internal/v1/model/operation-authority", "/internal/v1/worker/operation-authority", "/internal/v1/document/operation-authority", "/internal/v1/session-operation-ticket"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		h.ServeHTTP(w, r)
		if path == "/internal/v1/model/operation-authority" && w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated model callback status %d", w.Code)
		}
		if path != "/internal/v1/model/operation-authority" && w.Code != http.StatusNotFound {
			t.Fatalf("unexpected private route %s: %d", path, w.Code)
		}
	}
}
