// File metadata (manifest mirror):
// Purpose: 注册并处理 `jurisdictionOperations` 等 Core HTTP 路由或会话命令，完成请求解析、服务端授权与响应映射。
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
	"strings"
)

func (h *handler) jurisdictionOperations(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	fromDate, toDate := strings.TrimSpace(r.URL.Query().Get("from_date")), strings.TrimSpace(r.URL.Query().Get("to_date"))
	out, err := h.store.ReadJurisdictionOperations(r.Context(), user, fromDate, toDate)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
