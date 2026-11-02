package httpapi

import (
	"context"
	"net/http"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/store"
	"contractaudit/internal/systemhealth"
)

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
	runtime, err := h.store.ReadTenantRuntimeControl(r.Context(), user.TenantID)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := tenantOperationsView{
		SchemaVersion: "tenant-operations/v2", TenantID: user.TenantID,
		Directory: directory, LoginRateLimitScope: "process",
		BudgetState: "unconfigured", Usage: usage,
		TenantControl: tenantControlView{Status: runtime.Status, LiveEpoch: runtime.LiveEpoch},
		ExecutionQueue: executionQueueView{PolicyHash: runtime.PolicyHash, PolicyVersion: runtime.PolicyVersion,
			TenantWeight: runtime.TenantWeight, TenantInFlightLimit: runtime.InFlightLimit,
			TenantPendingLimit: runtime.PendingLimit, TenantLive: runtime.TenantLive},
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
