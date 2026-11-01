package httpapi

import (
	"net/http"
	"strconv"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

// CLM contract draft routes are registered by the shared server wiring. The
// gateway only supports PUT for full replacement, so draft replacement uses
// PUT /api/contracts/{contractID}/draft; the design's PATCH intent maps onto
// it without JSON Patch semantics. GET /api/contracts/drafts serves the
// caller's own draft recovery list; the full ledger is a later milestone.
func registerContractDraftRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"POST /api/contracts", h.createContractDraft},
		routeBinding{"GET /api/contracts/drafts", h.listMyContractDrafts},
		routeBinding{"GET /api/contracts/{contractID}", h.getContractDraft},
		routeBinding{"PUT /api/contracts/{contractID}/draft", h.replaceContractDraft},
		routeBinding{"POST /api/contracts/legacy-imports", h.importLegacyContract},
	)
}

// requiredIdempotencyKey reads the mandatory Idempotency-Key header. CLM write
// commands refuse replay-ambiguous requests instead of inventing a key.
func requiredIdempotencyKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) > 1 {
		return "", fault.Invalid("Idempotency-Key必须是唯一的请求头")
	}
	if len(values) == 0 || values[0] == "" {
		return "", fault.Invalid("写命令必须携带 Idempotency-Key 请求头")
	}
	return values[0], nil
}

type contractDraftCreateRequest struct {
	ExpectedRowVersion int64              `json:"expected_row_version"`
	AuthorityMode      string             `json:"authority_mode"`
	Draft              store.DraftContent `json:"draft"`
}

func (h *handler) createContractDraft(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body contractDraftCreateRequest) (any, error) {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			return nil, err
		}
		return h.store.CreateContractDraft(req.Context(), user, store.CreateContractDraftInput{
			ExpectedRowVersion: body.ExpectedRowVersion,
			AuthorityMode:      body.AuthorityMode,
			Draft:              body.Draft,
			IdempotencyKey:     key,
		})
	})
}

func (h *handler) getContractDraft(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		return h.store.LoadContractDetail(req.Context(), user, req.PathValue("contractID"))
	})
}

func (h *handler) listMyContractDrafts(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		limit := 25
		if raw := req.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 100 {
				return nil, fault.Invalid("limit 必须在 1 到 100 之间")
			}
			limit = parsed
		}
		items, err := h.store.ListMyContractDrafts(req.Context(), user, limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": items}, nil
	})
}

type contractDraftReplaceRequest struct {
	ExpectedRowVersion int64              `json:"expected_row_version"`
	ReplacementDraft   store.DraftContent `json:"replacement_draft"`
}

func (h *handler) replaceContractDraft(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusOK, func(user accounts.User, req *http.Request, body contractDraftReplaceRequest) (any, error) {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			return nil, err
		}
		return h.store.ReplaceContractDraft(req.Context(), user, store.ReplaceContractDraftInput{
			ContractID:         req.PathValue("contractID"),
			ExpectedRowVersion: body.ExpectedRowVersion,
			Draft:              body.ReplacementDraft,
			IdempotencyKey:     key,
		})
	})
}

type contractLegacyImportRequest struct {
	ExpectedRowVersion          int64               `json:"expected_row_version"`
	AuthorityMode               string              `json:"authority_mode"`
	SourceRef                   store.SourceRef     `json:"source_ref"`
	SourceEvidence              store.MaterialRef   `json:"source_evidence"`
	Snapshot                    store.DraftContent  `json:"snapshot"`
	ApprovalHistoryStatus       string              `json:"approval_history_status"`
	HistoricalApprovalMaterials []store.MaterialRef `json:"historical_approval_materials"`
	Reason                      string              `json:"reason"`
}

func (h *handler) importLegacyContract(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body contractLegacyImportRequest) (any, error) {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			return nil, err
		}
		return h.store.ImportLegacySignedContract(req.Context(), user, store.LegacyImportInput{
			ExpectedRowVersion:          body.ExpectedRowVersion,
			AuthorityMode:               body.AuthorityMode,
			SourceRef:                   body.SourceRef,
			SourceEvidence:              body.SourceEvidence,
			Snapshot:                    body.Snapshot,
			ApprovalHistoryStatus:       body.ApprovalHistoryStatus,
			HistoricalApprovalMaterials: body.HistoricalApprovalMaterials,
			Reason:                      body.Reason,
			IdempotencyKey:              key,
		})
	})
}
