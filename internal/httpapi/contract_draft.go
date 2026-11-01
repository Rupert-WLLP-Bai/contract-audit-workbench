package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

// CLM contract routes are registered by the shared server wiring. The gateway
// supports PUT for full replacement, so draft replacement uses
// PUT /api/contracts/{contractID}/draft; the design's PATCH intent maps onto
// it without JSON Patch semantics. GET /api/contracts/drafts serves the
// caller's own draft recovery list, while GET /api/contracts serves the
// authorized ledger. Saved-view replacement uses POST because a PUT pattern
// would collide with the existing draft route in Go's ServeMux.
func registerContractDraftRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"GET /api/contracts", h.listContractLedger},
		// Saved views and exports live under the literal /ledger segment. Without
		// it, POST /api/contracts/views/{viewID} and
		// GET /api/contracts/exports/{exportID}/download overlap
		// /api/contracts/{contractID}/revisions* and Go's ServeMux rejects the
		// pair at registration: neither pattern is more specific, so Handler
		// panics before the server ever starts.
		routeBinding{"GET /api/contracts/ledger/views", h.listContractSavedViews},
		routeBinding{"POST /api/contracts/ledger/views", h.createContractSavedView},
		routeBinding{"POST /api/contracts/ledger/views/{viewID}", h.replaceContractSavedView},
		routeBinding{"DELETE /api/contracts/ledger/views/{viewID}", h.deleteContractSavedView},
		routeBinding{"POST /api/contracts/ledger/exports", h.createContractExport},
		routeBinding{"GET /api/contracts/ledger/exports/{exportID}/download", h.downloadContractExport},
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

func contractLedgerQueryValue(r *http.Request, key string) string {
	return strings.TrimSpace(r.URL.Query().Get(key))
}

func contractLedgerOptionalString(r *http.Request, key string) *string {
	value := contractLedgerQueryValue(r, key)
	if value == "" {
		return nil
	}
	return &value
}

func contractLedgerOptionalTime(r *http.Request, key string) (*time.Time, error) {
	raw := contractLedgerQueryValue(r, key)
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fault.Invalid(key + "必须是 RFC 3339 时间")
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func contractLedgerQueryFromRequest(r *http.Request) (store.ContractLedgerQuery, error) {
	filter := store.ContractLedgerFilter{
		Text:              contractLedgerQueryValue(r, "text"),
		SupplierName:      contractLedgerQueryValue(r, "supplier_name"),
		DepartmentID:      contractLedgerQueryValue(r, "department_id"),
		ContractState:     contractLedgerQueryValue(r, "contract_state"),
		WorkflowState:     contractLedgerQueryValue(r, "workflow_state"),
		AuthorityMode:     contractLedgerQueryValue(r, "authority_mode"),
		ValidityState:     contractLedgerQueryValue(r, "validity_state"),
		ProjectType:       contractLedgerQueryValue(r, "project_type"),
		ProcurementMethod: contractLedgerQueryValue(r, "procurement_method"),
		AmountMin:         contractLedgerOptionalString(r, "amount_min"),
		AmountMax:         contractLedgerOptionalString(r, "amount_max"),
		SigningDateFrom:   contractLedgerOptionalString(r, "signing_date_from"),
		SigningDateTo:     contractLedgerOptionalString(r, "signing_date_to"),
	}
	var err error
	if filter.CreatedFrom, err = contractLedgerOptionalTime(r, "created_from"); err != nil {
		return store.ContractLedgerQuery{}, err
	}
	if filter.CreatedTo, err = contractLedgerOptionalTime(r, "created_to"); err != nil {
		return store.ContractLedgerQuery{}, err
	}
	limit := 0
	if raw := contractLedgerQueryValue(r, "limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return store.ContractLedgerQuery{}, fault.Invalid("limit 必须在 1 到 100 之间")
		}
		limit = parsed
	}
	return store.ContractLedgerQuery{
		Filter: filter,
		Sort:   contractLedgerQueryValue(r, "sort"),
		Cursor: contractLedgerQueryValue(r, "cursor"),
		Limit:  limit,
	}, nil
}

func (h *handler) listContractLedger(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		query, err := contractLedgerQueryFromRequest(req)
		if err != nil {
			return nil, err
		}
		return h.store.ListContractLedger(req.Context(), user, query)
	})
}

func (h *handler) listContractSavedViews(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		items, err := h.store.ListContractSavedViews(req.Context(), user)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": items}, nil
	})
}

func (h *handler) createContractSavedView(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body store.ContractSavedViewInput) (any, error) {
		return h.store.CreateContractSavedView(req.Context(), user, body)
	})
}

func (h *handler) replaceContractSavedView(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusOK, func(user accounts.User, req *http.Request, body store.ContractSavedViewUpdateInput) (any, error) {
		body.ViewID = req.PathValue("viewID")
		return h.store.ReplaceContractSavedView(req.Context(), user, body)
	})
}

func (h *handler) deleteContractSavedView(w http.ResponseWriter, r *http.Request) {
	serveClientJSONLookup(h, w, r, func(user accounts.User, req *http.Request) (any, error) {
		raw := contractLedgerQueryValue(req, "expected_row_version")
		version, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || version < 1 {
			return nil, fault.Invalid("expected_row_version 必须是正整数")
		}
		if err := h.store.DeleteContractSavedView(req.Context(), user, req.PathValue("viewID"), version); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true}, nil
	})
}

func (h *handler) createContractExport(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body store.ContractExportInput) (any, error) {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			return nil, err
		}
		body.IdempotencyKey = key
		return h.store.CreateContractExport(req.Context(), user, body)
	})
}

func (h *handler) downloadContractExport(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	download, err := h.store.LoadContractExportForDownload(r.Context(), user, r.PathValue("exportID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	document, err := store.RenderContractExportCSV(download.Columns, download.Rows)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := store.ReauthorizeContractExportDownload(user); err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="contracts-`+download.ExportID+`.csv"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(document)
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

// registerContractReferenceRoutes mounts the read-only strict resolver. It is
// a separate mount from the draft routes so the reference-chain patch and the
// contract-ledger patch can each extend their own route block.
func registerContractReferenceRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"GET /api/contracts/{contractID}/reference-checks", h.checkContractReferences},
	)
}
