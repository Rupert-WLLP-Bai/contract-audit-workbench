package httpapi

import (
	"net/http"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

// Contract audit links bind an existing review attempt to the frozen revision
// that was submitted. The routes are behind an explicit server-side switch so
// a deployment can stop new bindings while keeping committed history readable.
func registerContractAuditRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"GET /api/contracts/{contractID}/revisions/{contractRevisionID}/audit-links", h.listContractAuditBindings},
		routeBinding{"POST /api/contracts/{contractID}/revisions/{contractRevisionID}/audit-links", h.bindContractAudit},
	)
}

func (h *handler) contractAuditBindingsConfigured() error {
	if !h.contractAuditBindingsEnabled {
		return fault.New(503, "contract_audit_bindings_disabled", "合同审计结果绑定未启用")
	}
	return nil
}

func (h *handler) listContractAuditBindings(w http.ResponseWriter, r *http.Request) {
	// Reading committed history stays available after the binding switch is
	// turned off; only creating a new binding is disabled.
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		return h.store.LoadContractAuditBindings(req.Context(), user,
			req.PathValue("contractID"), req.PathValue("contractRevisionID"))
	})
}

type contractAuditBindRequest struct {
	ReviewRunID        string `json:"review_run_id"`
	ReviewAttemptID    string `json:"review_attempt_id"`
	ExpectedRowVersion int64  `json:"expected_row_version"`
}

func (h *handler) bindContractAudit(w http.ResponseWriter, r *http.Request) {
	if err := h.contractAuditBindingsConfigured(); err != nil {
		writeErr(w, err)
		return
	}
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body contractAuditBindRequest) (any, error) {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			return nil, err
		}
		return h.store.BindContractAudit(req.Context(), user, store.BindContractAuditInput{
			ContractID:         req.PathValue("contractID"),
			ContractRevisionID: req.PathValue("contractRevisionID"),
			ReviewRunID:        body.ReviewRunID,
			ReviewAttemptID:    body.ReviewAttemptID,
			ExpectedRowVersion: body.ExpectedRowVersion,
			IdempotencyKey:     key,
		})
	})
}
