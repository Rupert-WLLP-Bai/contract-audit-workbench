package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

// Read-only approval workbench routes. The write command for a decision is
// registered by the approval command module; these routes never mutate a
// round or a decision.
func registerContractApprovalReadRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"GET /api/approval-tasks", h.listApprovalInbox},
		routeBinding{"GET /api/approval-tasks/{taskID}", h.getApprovalTask},
		routeBinding{"GET /api/contracts/{contractID}/approval-rounds", h.listContractApprovalRounds},
		routeBinding{"GET /api/contracts/{contractID}/approval-rounds/{roundID}", h.getContractApprovalRound},
	)
}

func approvalReadQueryLimit(r *http.Request, maximum int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maximum {
		return 0, fault.Invalid("limit 超出允许范围")
	}
	return limit, nil
}

func (h *handler) listApprovalInbox(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		limit, err := approvalReadQueryLimit(req, 100)
		if err != nil {
			return nil, err
		}
		return h.store.ListApprovalInbox(req.Context(), user, store.ApprovalInboxQuery{
			View:   strings.TrimSpace(req.URL.Query().Get("view")),
			Cursor: strings.TrimSpace(req.URL.Query().Get("cursor")),
			Limit:  limit,
		})
	})
}

func (h *handler) getApprovalTask(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		return h.store.GetApprovalTask(req.Context(), user, req.PathValue("taskID"))
	})
}

func (h *handler) listContractApprovalRounds(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		limit, err := approvalReadQueryLimit(req, 50)
		if err != nil {
			return nil, err
		}
		return h.store.ListContractApprovalRounds(req.Context(), user, req.PathValue("contractID"),
			strings.TrimSpace(req.URL.Query().Get("cursor")), limit)
	})
}

func (h *handler) getContractApprovalRound(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		return h.store.GetContractApprovalRound(req.Context(), user, req.PathValue("contractID"),
			req.PathValue("roundID"))
	})
}
