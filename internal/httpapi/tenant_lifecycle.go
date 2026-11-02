package httpapi

import (
	"net/http"
	"strconv"

	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

func (h *handler) platformTenantProvisionOperations(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeErr(w, fault.Invalid("租户操作分页上限无效"))
			return
		}
		limit = parsed
	}
	out, err := h.store.ListTenantProvisionOperations(r.Context(), user, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) startPlatformTenantProvisionOperation(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	key, err := requiredIdempotencyKey(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var input store.TenantProvisionInput
	if err := readJSON(w, r, &input); err != nil {
		writeErr(w, err)
		return
	}
	if input.IdempotencyKey != "" && input.IdempotencyKey != key {
		writeErr(w, fault.Invalid("body idempotency_key 必须与 Idempotency-Key 请求头一致"))
		return
	}
	input.IdempotencyKey = key
	out, err := h.store.StartTenantProvisionOperation(r.Context(), user, input)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, out)
}

func (h *handler) readPlatformTenantProvisionOperation(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	out, err := h.store.ReadTenantProvisionOperation(r.Context(), user, r.PathValue("operationID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) advancePlatformTenantProvisionOperation(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var input store.TenantProvisionStepInput
	if err := readJSON(w, r, &input); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.store.AdvanceTenantProvisionOperation(r.Context(), user, r.PathValue("operationID"), input)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
