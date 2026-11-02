// File metadata (manifest mirror):
// Purpose: 注册并处理 `tenantConfigRequest`, `tenantLiveControlRequest`, `tenantCostPolicyRequest`, `tenantCostPolicy` 等 Core HTTP 路由或会话命令，完成请求解析、服务端授权与响应映射。
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

	"contractaudit/internal/store"
)

type tenantConfigRequest struct {
	ExpectedRevision string                      `json:"expected_revision"`
	Selection        store.TenantConfigSelection `json:"selection"`
}

type tenantLiveControlRequest struct {
	ExpectedRevision string `json:"expected_revision"`
	Enabled          bool   `json:"enabled"`
}

type tenantCostPolicyRequest struct {
	ExpectedRevision string  `json:"expected_revision"`
	DayLimitMinor    *string `json:"day_limit_minor"`
	MonthLimitMinor  *string `json:"month_limit_minor"`
}

func (h *handler) tenantCostPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	out, err := h.store.ReadTenantMoneyPolicy(r.Context(), user)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) setTenantCostPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body tenantCostPolicyRequest
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.store.SetTenantMoneyLimits(r.Context(), user, body.ExpectedRevision, body.DayLimitMinor, body.MonthLimitMinor)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) setPlatformModelMoneyPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body store.PlatformMoneyPolicyInput
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.store.PublishPlatformMoneyPolicy(r.Context(), user, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) platformModelMoneyPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	out, err := h.store.ReadPlatformMoneyPolicy(r.Context(), user)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) createTenantCostAdjustment(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body store.TenantMoneyAdjustmentInput
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.store.AppendTenantMoneyAdjustment(r.Context(), user, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *handler) tenantConfig(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	out, err := h.store.ReadTenantConfig(r.Context(), user)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) previewTenantConfig(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body tenantConfigRequest
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	preview, err := h.store.PreviewTenantConfig(r.Context(), user, body.ExpectedRevision, body.Selection)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (h *handler) adoptTenantConfig(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body tenantConfigRequest
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.store.AdoptTenantConfig(r.Context(), user, body.ExpectedRevision, body.Selection)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) setTenantLiveControl(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var body tenantLiveControlRequest
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.store.SetTenantModelSendControl(r.Context(), user, body.ExpectedRevision, body.Enabled)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
