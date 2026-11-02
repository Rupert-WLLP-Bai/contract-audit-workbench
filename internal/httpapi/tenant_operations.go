// File metadata (manifest mirror):
// Purpose: Returns tenant-scoped usage, budget and capacity projections for the authenticated organization; the API does not accept a client-selected tenant as authority.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package httpapi

import (
	"context"
	"net/http"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/store"
	"contractaudit/internal/systemhealth"
)

func unavailableTenantCost(now time.Time) tenantCostView {
	day := now.Format("2006-01-02")
	month := now.Format("2006-01")
	return tenantCostView{
		Currency: "USD", MinorUnitScale: 6, Timezone: "UTC",
		Day: tenantCostWindowView{PeriodKey: day}, Month: tenantCostWindowView{PeriodKey: month},
	}
}

type tenantOperationsView struct {
	SchemaVersion       string                                 `json:"schema_version"`
	TenantID            string                                 `json:"tenant_id"`
	Directory           accounts.DirectoryStatusSnapshot       `json:"directory"`
	LoginRateLimitScope string                                 `json:"login_rate_limit_scope"`
	BudgetState         string                                 `json:"budget_state"`
	Budget              *systemhealth.ModelBudgetGatewayStatus `json:"budget"`
	Usage               store.TenantModelUsage                 `json:"usage"`
	TenantControl       tenantControlView                      `json:"tenant_control"`
	ExecutionQueue      executionQueueView                     `json:"execution_queue"`
	Cost                tenantCostView                         `json:"cost"`
}

type tenantCostWindowView struct {
	PeriodKey        string  `json:"period_key"`
	LimitMinor       *string `json:"limit_minor"`
	ReservedMinor    *string `json:"reserved_minor"`
	SettledMinor     *string `json:"settled_minor"`
	UnknownHoldMinor *string `json:"unknown_hold_minor"`
	Known            bool    `json:"known"`
}

type tenantCostView struct {
	Currency       string               `json:"currency"`
	MinorUnitScale int                  `json:"minor_unit_scale"`
	Timezone       string               `json:"timezone"`
	Day            tenantCostWindowView `json:"day"`
	Month          tenantCostWindowView `json:"month"`
}

type tenantControlView struct {
	Status    string `json:"status"`
	LiveEpoch string `json:"live_epoch"`
}

type executionQueueView struct {
	PolicyHash          string `json:"policy_hash"`
	PolicyVersion       string `json:"policy_version"`
	TenantWeight        int    `json:"tenant_weight"`
	TenantInFlightLimit int    `json:"tenant_in_flight_limit"`
	TenantPendingLimit  int    `json:"tenant_pending_limit"`
	TenantLive          bool   `json:"tenant_live"`
}

func (h *handler) tenantOperations(w http.ResponseWriter, r *http.Request) {
	directory := accounts.DirectoryStatus()
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	usage, err := h.store.ReadTenantModelUsage(r.Context(), user)
	if err != nil {
		writeErr(w, err)
		return
	}
	observedAt, err := time.Parse(time.RFC3339Nano, usage.ObservedAt)
	if err != nil {
		observedAt = time.Now().UTC()
	}
	runtime, err := h.store.ReadTenantRuntimeControl(r.Context(), user.TenantID)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := tenantOperationsView{
		SchemaVersion: "tenant-operations/v3", TenantID: user.TenantID,
		Directory: directory, LoginRateLimitScope: "process",
		BudgetState: "unconfigured", Usage: usage,
		TenantControl: tenantControlView{Status: runtime.Status, LiveEpoch: runtime.LiveEpoch},
		ExecutionQueue: executionQueueView{PolicyHash: runtime.PolicyHash, PolicyVersion: runtime.PolicyVersion,
			TenantWeight: runtime.TenantWeight, TenantInFlightLimit: runtime.InFlightLimit,
			TenantPendingLimit: runtime.PendingLimit, TenantLive: runtime.TenantLive},
		Cost: unavailableTenantCost(observedAt.UTC()),
	}
	if h.modelBudgetGateway != "" {
		out.BudgetState = "unavailable"
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if budget, ok := systemhealth.FetchModelBudgetGatewayStatus(ctx, h.modelBudgetGateway); ok {
			out.BudgetState, out.Budget = "ready", &budget
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
