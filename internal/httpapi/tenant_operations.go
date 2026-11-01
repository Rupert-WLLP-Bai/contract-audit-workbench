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
	out := tenantOperationsView{
		SchemaVersion: "tenant-operations/v1", TenantID: user.TenantID,
		Directory: directory, LoginRateLimitScope: "process",
		BudgetState: "unconfigured", Usage: usage,
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
