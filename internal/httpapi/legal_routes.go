package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

// registerLegalKnowledgeRoutes is called by the core route registry. The admin
// governance list route should dispatch kind=legal to adminLegalKnowledgeSearch
// so existing no-filter responses remain byte-compatible.
func registerLegalKnowledgeRoutes(mux *http.ServeMux, h *handler) {
	mountRoutes(mux,
		routeBinding{"POST /api/admin/legal-sources/imports", h.importLegalSource},
		routeBinding{"GET /api/admin/legal-sources/{sourceID}/snapshots/{captureSnapshotID}", h.getLegalSourceCapture},
		routeBinding{"GET /api/cases/{caseID}/legal-knowledge", h.searchLegalKnowledgeForCase},
		routeBinding{"GET /api/cases/{caseID}/legal-knowledge/{knowledgeID}/versions/{versionID}", h.getLegalKnowledgeForCase},
		routeBinding{"GET /api/cases/{caseID}/legal-sources/snapshots/{captureSnapshotID}", h.getLegalSourceCaptureForCase},
		routeBinding{"GET /api/cases/{caseID}/runs/{runID}/attempts/{attemptID}/legal-citations/{citationID}", h.getFrozenLegalAuthority},
		routeBinding{"GET /api/cases/{caseID}/legal-applicability-facts", h.resolveLegalApplicabilityFacts},
		routeBinding{"GET /api/cases/{caseID}/legal-applicability-facts/{dimension}/history", h.listLegalApplicabilityFactHistory},
		routeBinding{"PUT /api/cases/{caseID}/legal-applicability-facts/{dimension}", h.recordLegalApplicabilityFact},
		routeBinding{"GET /api/admin/company-policy-sources", h.adminCompanyPolicySourceStatus},
		routeBinding{"GET /api/cases/{caseID}/company-policy-sources", h.caseCompanyPolicySourceStatus},
	)
}

func (h *handler) getFrozenLegalAuthority(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	authority, err := h.store.ReadFrozenLegalAuthority(r.Context(), user, r.PathValue("caseID"), r.PathValue("runID"), r.PathValue("attemptID"), r.PathValue("citationID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, authority)
}

func (h *handler) recordLegalApplicabilityFact(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var input store.LegalApplicabilityFactInput
	if err := readJSON(w, r, &input); err != nil {
		writeErr(w, err)
		return
	}
	if err := bindLegalApplicabilityRequest(r.PathValue("dimension"), r.Header.Get("Idempotency-Key"), &input); err != nil {
		writeErr(w, err)
		return
	}
	fact, err := h.store.RecordLegalApplicabilityFact(r.Context(), user, r.PathValue("caseID"), input)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, fact)
}

func (h *handler) resolveLegalApplicabilityFacts(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	input := store.LegalApplicabilityResolutionInput{
		BusinessAt: r.URL.Query().Get("business_at"), EvidenceCutoffAt: r.URL.Query().Get("evidence_cutoff_at"),
	}
	facts, err := h.store.ResolveLegalApplicabilityFacts(r.Context(), user, r.PathValue("caseID"), input)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, facts)
}

func (h *handler) listLegalApplicabilityFactHistory(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	limit, err := parseLegalApplicabilityHistoryLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeErr(w, err)
		return
	}
	history, err := h.store.ListLegalApplicabilityFactHistory(r.Context(), user, r.PathValue("caseID"), r.PathValue("dimension"), store.LegalApplicabilityHistoryInput{
		Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func parseLegalApplicabilityHistoryLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fault.Invalid("法律适用事实历史limit必须是整数")
	}
	if parsed < 1 || parsed > 100 {
		return 0, fault.Invalid("法律适用事实历史limit必须在1到100之间")
	}
	return parsed, nil
}

func bindLegalApplicabilityRequest(dimension, header string, input *store.LegalApplicabilityFactInput) error {
	if input == nil || dimension == "" {
		return fault.Invalid("法律适用事实请求无效")
	}
	if input.Dimension != "" && input.Dimension != dimension {
		return fault.Invalid("事实维度与路径不一致")
	}
	header = strings.TrimSpace(header)
	if header == "" || input.IdempotencyKey != "" && input.IdempotencyKey != header {
		return fault.Invalid("必须提供 Idempotency-Key；正文中的幂等键如有提供必须与请求头一致")
	}
	input.Dimension, input.IdempotencyKey = dimension, header
	return nil
}

type companyPolicySourceStatus struct {
	CatalogKey                     string              `json:"catalog_key"`
	DisplayLabel                   string              `json:"display_label"`
	State                          string              `json:"state"`
	RequestedOwner                 *string             `json:"requested_owner"`
	HasBody                        bool                `json:"has_body"`
	HasPublishedVersion            bool                `json:"has_published_version"`
	HasContentHash                 bool                `json:"has_content_hash"`
	KnowledgeRef                   *store.KnowledgeRef `json:"knowledge_ref"`
	CanBeCited                     bool                `json:"can_be_cited"`
	SatisfiesMandatoryDependencies bool                `json:"satisfies_mandatory_dependencies"`
}

func awaitingCompanyPolicySourceStatus() companyPolicySourceStatus {
	return companyPolicySourceStatus{
		CatalogKey: "company-policy-source-pending", DisplayLabel: "公司制度资料待提供", State: "awaiting_source",
		HasBody: false, HasPublishedVersion: false, HasContentHash: false, KnowledgeRef: nil,
		CanBeCited: false, SatisfiesMandatoryDependencies: false,
	}
}

func (h *handler) adminCompanyPolicySourceStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actorForAudience(w, r, "admin"); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": []companyPolicySourceStatus{awaitingCompanyPolicySourceStatus()}})
}

func (h *handler) caseCompanyPolicySourceStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	if _, err := h.store.GetCase(user, r.PathValue("caseID")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": []companyPolicySourceStatus{awaitingCompanyPolicySourceStatus()}})
}

func (h *handler) importLegalSource(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	var input store.LegalSourceImportInput
	if err := readJSON(w, r, &input); err != nil {
		writeErr(w, err)
		return
	}
	key, err := governanceIdempotencyKey(r, input.IdempotencyKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	input.IdempotencyKey = key
	result, err := h.store.ImportLegalSource(r.Context(), user, input)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (h *handler) adminLegalKnowledgeSearch(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	filter, err := legalSearchFilter(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	page, err := h.store.SearchLegalKnowledgeAdmin(r.Context(), user, filter)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *handler) getLegalSourceCapture(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	capture, err := h.store.GetLegalSourceCapture(r.Context(), user, r.PathValue("captureSnapshotID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if !legalSourceIdentityMatches(r.PathValue("sourceID"), capture) {
		writeErr(w, fault.NotFound("没有找到法律来源快照"))
		return
	}
	writeJSON(w, http.StatusOK, capture)
}

func legalSourceIdentityMatches(pathSourceID string, capture store.LegalSourceCapture) bool {
	return pathSourceID != "" && pathSourceID == capture.SourceID
}

func (h *handler) searchLegalKnowledgeForCase(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	filter, err := legalSearchFilter(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	page, err := h.store.SearchLegalKnowledgeForCase(r.Context(), user, r.PathValue("caseID"), filter)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *handler) getLegalKnowledgeForCase(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	version, err := h.store.GetLegalKnowledgeForCase(r.Context(), user, r.PathValue("caseID"), r.PathValue("knowledgeID"), r.PathValue("versionID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, version)
}

func (h *handler) getLegalSourceCaptureForCase(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	capture, err := h.store.GetLegalSourceCaptureForCase(r.Context(), user, r.PathValue("caseID"), r.PathValue("captureSnapshotID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, capture)
}

func legalSearchFilter(r *http.Request) (store.LegalSearch, error) {
	values := r.URL.Query()
	filter := store.LegalSearch{
		Query: strings.TrimSpace(values.Get("q")), SourceID: strings.TrimSpace(values.Get("source_id")),
		BusinessAt: strings.TrimSpace(values.Get("business_at")), Cursor: values.Get("cursor"),
	}
	if raw := values.Get("article_number"); raw != "" {
		number, err := strconv.Atoi(raw)
		if err != nil || number <= 0 {
			return store.LegalSearch{}, fault.Invalid("article_number 必须是正整数")
		}
		filter.ArticleNumber = number
	}
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 50 {
			return store.LegalSearch{}, fault.Invalid("limit 必须在 1 到 50 之间")
		}
		filter.Limit = limit
	}
	if len(filter.Cursor) > 256 {
		return store.LegalSearch{}, fault.Invalid("法律检索游标过长")
	}
	return filter, nil
}
