package httpapi

import (
	"net/http"

	"contractaudit/internal/accounts"
	"contractaudit/internal/store"
)

// Contract version routes freeze a native draft into an immutable revision and
// expose the frozen ledger plus the field-level diff. Freezing does not by
// itself start an approval round; a later submission command will reference the
// frozen revision without rewriting it.
func registerContractVersionRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"POST /api/contracts/{contractID}/revisions", h.freezeContractVersion},
		routeBinding{"GET /api/contracts/{contractID}/revisions", h.listContractVersions},
		routeBinding{"GET /api/contracts/{contractID}/revisions/{contractRevisionID}", h.getContractVersion},
		routeBinding{"GET /api/contracts/{contractID}/revision-diff", h.diffContractVersions},
		routeBinding{"POST /api/contracts/{contractID}/submissions", h.submitContractApproval},
		routeBinding{"POST /api/approval-tasks/{taskID}/decisions", h.decideApprovalTask},
	)
}

type contractVersionFreezeRequest struct {
	ExpectedRowVersion int64  `json:"expected_row_version"`
	CaseID             string `json:"case_id,omitempty"`
}

func (h *handler) freezeContractVersion(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body contractVersionFreezeRequest) (any, error) {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			return nil, err
		}
		return h.store.FreezeContractVersion(req.Context(), user, store.FreezeContractVersionInput{
			ContractID:         req.PathValue("contractID"),
			ExpectedRowVersion: body.ExpectedRowVersion,
			CaseID:             body.CaseID,
			IdempotencyKey:     key,
		})
	})
}

func (h *handler) listContractVersions(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		return h.store.ListContractVersions(req.Context(), user, req.PathValue("contractID"))
	})
}

func (h *handler) getContractVersion(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		return h.store.LoadContractVersion(req.Context(), user, req.PathValue("contractID"), req.PathValue("contractRevisionID"))
	})
}

func (h *handler) diffContractVersions(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		return h.store.DiffContractVersions(req.Context(), user, req.PathValue("contractID"),
			req.URL.Query().Get("from_revision_id"), req.URL.Query().Get("to_revision_id"))
	})
}
