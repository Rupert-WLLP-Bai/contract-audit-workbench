package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/apidocs"
	"contractaudit/internal/auditext"
	"contractaudit/internal/capabilitypack"
	"contractaudit/internal/documentservice"
	"contractaudit/internal/fault"
	"contractaudit/internal/identity"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
	"contractaudit/internal/systemhealth"
)

const sessionCookie = "session"

// Options configures the HTTP surface.
type Options struct {
	SystemHealth               systemhealth.Reader
	CORSOrigins                []string
	Models                     *model.Client
	OIDC                       *identity.Manager
	Durable                    bool
	SecureCookies              bool
	ExperienceWorkerCredential string
	experienceDeliveryEnabled  bool

	// OperatorAuditEnabled is the server-side feature switch. The extension is
	// absent from the default review path and returns 503 until an operator
	// explicitly enables it.
	OperatorAuditEnabled  bool
	CapabilityPacks       *capabilitypack.Registry
	ExternalConnectors    map[string]auditext.ReadConnector
	ExternalConnectorInfo []OperatorConnectorInfo
}

// Handler serves the review API the web client calls.
func Handler(st *store.Store, opt Options) http.Handler {
	if len(opt.CORSOrigins) == 0 {
		opt.CORSOrigins = []string{"http://localhost:5173", "http://127.0.0.1:5173"}
	}
	if opt.SystemHealth == nil {
		opt.SystemHealth = systemhealth.New(systemhealth.Config{DatabaseReady: st.Ready})
	}
	h := &handler{store: st, cors: opt.CORSOrigins, models: opt.Models, oidc: opt.OIDC, durable: opt.Durable, secureCookies: opt.SecureCookies, systemHealth: opt.SystemHealth, experienceDeliveryEnabled: opt.experienceDeliveryEnabled, operatorAuditEnabled: opt.OperatorAuditEnabled, capabilityPacks: opt.CapabilityPacks, externalConnectors: opt.ExternalConnectors, externalConnectorInfo: opt.ExternalConnectorInfo}
	mux := http.NewServeMux()
	// Every binary in this repository serves the same probe surface so a manifest
	// can never point at a path no binary answers. Readiness is the shared
	// database connection only; liveness checks no dependency.
	MountHealth(mux, func(ctx context.Context) error { return st.Ready(ctx) })
	mux.HandleFunc("GET /openapi.json", apidocs.Serve)
	mux.HandleFunc("POST /api/session", h.login)
	mux.HandleFunc("GET /api/session", h.session)
	mux.HandleFunc("DELETE /api/session", h.logout)
	mux.HandleFunc("GET /api/settings", h.settings)
	mux.HandleFunc("GET /api/admin/settings", h.adminSettings)
	mux.HandleFunc("GET /api/admin/system-health", h.adminSystemHealth)
	mux.HandleFunc("GET /api/admin/rulepacks", h.listRulePacks)
	mux.HandleFunc("POST /api/admin/rulepacks", h.createRulePack)
	mux.HandleFunc("GET /api/admin/rulepacks/{packID}/versions/{version}", h.getRulePackVersion)
	mux.HandleFunc("PUT /api/admin/rulepacks/{packID}/versions/{version}", h.editRulePackVersion)
	mux.HandleFunc("POST /api/admin/rulepacks/{packID}/versions/{version}/validate", h.validateRulePackVersion)
	mux.HandleFunc("POST /api/admin/rulepacks/{packID}/versions/{version}/approve", h.approveRulePackVersion)
	mux.HandleFunc("POST /api/admin/rulepacks/{packID}/versions/{version}/publish", h.publishRulePackVersion)
	mux.HandleFunc("POST /api/admin/rulepacks/{packID}/versions/{version}/disable", h.disableRulePackVersion)
	mux.HandleFunc("POST /api/admin/rulepacks/{packID}/versions/{version}/test", h.testRulePackVersion)
	mux.HandleFunc("POST /api/admin/rulepacks/{packID}/rollback", h.rollbackRulePackVersion)
	mux.HandleFunc("GET /api/admin/rulepacks/{packID}/history", h.rulePackHistory)
	registerGovernanceRoutes(mux, h)
	registerEvidenceRoutes(mux, h)
	mux.HandleFunc("GET /api/auth/oidc/login", h.oidcLogin)
	mux.HandleFunc("GET /api/auth/oidc/callback", h.oidcCallback)
	mux.HandleFunc("GET /api/templates", h.templates)
	mux.HandleFunc("GET /api/capability-packs", h.listCapabilityPacks)
	mux.HandleFunc("GET /api/workbench", h.workbench)
	mux.HandleFunc("GET /api/system-health", h.healthSnapshot)
	mux.HandleFunc("PUT /api/cases/{caseID}/profile", h.updateCaseProfile)
	mux.HandleFunc("POST /api/cases/{caseID}/tasks", h.createWorkbenchTask)
	mux.HandleFunc("PUT /api/cases/{caseID}/tasks/{taskID}", h.updateWorkbenchTask)
	mux.HandleFunc("POST /api/cases/{caseID}/relations", h.createCaseRelation)
	mux.HandleFunc("DELETE /api/cases/{caseID}/relations/{relationID}", h.deleteCaseRelation)
	mux.HandleFunc("POST /api/knowledge", h.createKnowledge)
	mux.HandleFunc("GET /api/knowledge/{knowledgeID}", h.getKnowledge)
	mux.HandleFunc("GET /api/knowledge/{knowledgeID}/versions/{versionID}", h.getKnowledgeVersion)
	mux.HandleFunc("GET /api/cases", h.listCases)
	mux.HandleFunc("POST /api/cases", h.createCase)
	mux.HandleFunc("GET /api/cases/{caseID}", h.getCase)
	mux.HandleFunc("POST /api/cases/{caseID}/members", h.addMember)
	mux.HandleFunc("POST /api/cases/{caseID}/documents", h.upload)
	mux.HandleFunc("GET /api/cases/{caseID}/documents/{docID}/original", h.original)
	mux.HandleFunc("POST /api/cases/{caseID}/documents/{docID}/parse", h.reparse)
	mux.HandleFunc("POST /api/cases/{caseID}/object-bindings", h.createObjectBinding)
	mux.HandleFunc("POST /api/cases/{caseID}/external-collection-jobs", h.collectExternalSnapshot)
	mux.HandleFunc("GET /api/cases/{caseID}/external-snapshots", h.listExternalSnapshots)
	mux.HandleFunc("POST /api/cases/{caseID}/fulfillment-evaluations", h.createFulfillmentEvaluation)
	mux.HandleFunc("GET /api/cases/{caseID}/fulfillment-evaluations/{evaluationID}", h.getFulfillmentEvaluation)
	mux.HandleFunc("GET /api/cases/{caseID}/fulfillment-evaluations/{evaluationID}/impact", h.getImpactPlan)
	mux.HandleFunc("POST /api/cases/{caseID}/generations", h.generate)
	mux.HandleFunc("POST /api/cases/{caseID}/remediations", h.createRemediation)
	mux.HandleFunc("POST /api/cases/{caseID}/remediations/{itemID}/submit", h.submitRemediation)
	mux.HandleFunc("POST /api/cases/{caseID}/remediations/{itemID}/verify", h.verifyRemediation)
	mux.HandleFunc("POST /api/cases/{caseID}/reports", h.createReport)
	mux.HandleFunc("GET /api/cases/{caseID}/reports/{reportID}", h.readReport)
	mux.HandleFunc("POST /api/cases/{caseID}/archive", h.archive)
	mux.HandleFunc("POST /api/cases/{caseID}/reviews", h.startReview)
	mux.HandleFunc("GET /api/cases/{caseID}/reviews/{runID}", h.workspace)
	mux.HandleFunc("GET /api/cases/{caseID}/reviews/{runID}/diagnostics", h.reviewDiagnostics)
	mux.HandleFunc("POST /api/cases/{caseID}/reviews/{runID}/retry", h.retry)
	mux.HandleFunc("POST /api/cases/{caseID}/reviews/{runID}/cancel", h.cancelReview)
	mux.HandleFunc("POST /api/cases/{caseID}/reviews/{runID}/findings/{findingID}/decisions", h.decide)
	mux.HandleFunc("GET /api/cases/{caseID}/reviews/{runID}/events", h.events)
	return cors(opt.CORSOrigins, mux)
}

type handler struct {
	systemHealth              systemhealth.Reader
	store                     *store.Store
	cors                      []string
	models                    *model.Client
	oidc                      *identity.Manager
	durable                   bool
	secureCookies             bool
	experienceDeliveryEnabled bool
	operatorAuditEnabled      bool
	capabilityPacks           *capabilitypack.Registry
	externalConnectors        map[string]auditext.ReadConnector
	externalConnectorInfo     []OperatorConnectorInfo
}

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Audience string `json:"audience"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	audience, err := validAudience(body.Audience)
	if err != nil {
		writeErr(w, err)
		return
	}
	var token string
	var user accounts.User
	if audience == "client" {
		token, user, err = h.store.Login(body.Username, body.Password)
	} else {
		var authenticated bool
		user, authenticated = accounts.Authenticate(strings.TrimSpace(body.Username), body.Password)
		if !authenticated {
			writeErr(w, fault.Unauthenticated("账号或密码不正确"))
			return
		}
		if user.Role != "admin" {
			writeErr(w, fault.Forbidden("需要平台管理权限"))
			return
		}
		token, err = h.store.EstablishSession(user, audience)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	h.setSessionCookie(w, r, token, audience, 7*24*3600)
	writeJSON(w, http.StatusOK, map[string]any{"actor": actorJSON(user), "audience": audience})
}

func (h *handler) session(w http.ResponseWriter, r *http.Request) {
	audience, err := validAudience(r.URL.Query().Get("audience"))
	if err != nil {
		writeErr(w, err)
		return
	}
	user, ok := h.actorForAudience(w, r, audience)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actor": actorJSON(user), "audience": audience})
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	audience, err := validAudience(r.URL.Query().Get("audience"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.store.Logout(audienceCookieValue(r, audience)); err != nil {
		writeErr(w, err)
		return
	}
	h.setSessionCookie(w, r, "", audience, -1)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) settings(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	h.settingsForUser(w, r, user)
}

func (h *handler) settingsForUser(w http.ResponseWriter, r *http.Request, user accounts.User) {
	users := accounts.List()
	out := make([]map[string]string, 0, len(users))
	for _, candidate := range users {
		if candidate.Enabled && accounts.Tenant(candidate) == accounts.Tenant(user) {
			out = append(out, actorJSON(candidate))
		}
	}
	status, label := "unreachable", "未配置审查模型"
	if h.models != nil {
		if h.store.ReadOnly() {
			status, label = "unknown", "只读维护，未探测模型："+h.models.Name()+" / "+h.models.OCRName()
		} else {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			status, label = h.models.Reachable(ctx)
		}
	}
	connectors := h.externalConnectorInfo
	if connectors == nil {
		connectors = []OperatorConnectorInfo{}
	}
	body := map[string]any{
		"model":            map[string]string{"status": status, "label": label},
		"queue":            map[string]any{"depth": nil, "oldest_wait_ms": nil},
		"redaction_policy": "原文只返回给已登录且能看到该案件的人。审查和图片转写会把正文或图片发给已配置的本地模型，密钥不返回给浏览器。",
		"users":            out,
		"operator_audit":   map[string]any{"enabled": h.operatorAuditEnabled, "connectors": connectors},
	}
	if user.Role == "admin" {
		metadata, err := h.store.OperationalMetadata(user)
		if err != nil {
			writeErr(w, err)
			return
		}
		body["execution"] = metadata
	}
	writeJSON(w, http.StatusOK, body)
}

func (h *handler) listCases(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	items, err := h.store.ListCasesChecked(user)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = summaryJSON(item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *handler) createCase(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct {
		Title        string `json:"title"`
		ContractType string `json:"contract_type"`
		BusinessLine string `json:"business_line"`
		Scenario     string `json:"scenario"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	id, err := h.store.CreateCaseWithBusiness(user, body.Title, body.ContractType, body.BusinessLine, body.Scenario)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (h *handler) getCase(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	detail, err := h.store.GetCase(user, r.PathValue("caseID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detailJSON(detail))
}

func (h *handler) addMember(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct {
		ActorID string `json:"actor_id"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.store.AddMember(user, r.PathValue("caseID"), body.ActorID); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": strings.TrimSpace(body.ActorID)})
}

func (h *handler) upload(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 33<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		if strings.Contains(err.Error(), "too large") || errors.Is(err, io.ErrUnexpectedEOF) {
			writeErr(w, fault.TooLarge("上传请求超过 33 MiB"))
			return
		}
		writeErr(w, fault.Invalid("材料上传格式不正确"))
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, fault.Invalid("缺少文件"))
		return
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, 32<<20+1))
	if err != nil {
		writeErr(w, err)
		return
	}
	if len(body) > 32<<20 {
		writeErr(w, fault.TooLarge("文件超过 32 MiB"))
		return
	}
	media := http.DetectContentType(body)
	caseID := r.PathValue("caseID")
	var id string
	err = documentservice.WithPublicParse(r.Context(), func(ctx context.Context) (string, error) {
		return h.store.DocumentUploadKind(ctx, user, caseID, header.Filename, media)
	}, func(ctx context.Context) error {
		var err error
		id, err = h.store.AddDocument(ctx, user, caseID, r.FormValue("role"), header.Filename, media, body)
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (h *handler) original(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	doc, body, err := h.store.Original(user, r.PathValue("caseID"), r.PathValue("docID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	// Only passive formats may render inline on the authenticated API origin.
	// In particular, a caller-supplied text/html or image/svg+xml must not execute.
	media, inline := originalMedia(doc.MediaType)
	w.Header().Set("Content-Type", media)
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": doc.Filename}))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func originalMedia(raw string) (string, bool) {
	media, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return "application/octet-stream", false
	}
	switch strings.ToLower(media) {
	case "text/plain":
		return "text/plain; charset=utf-8", true
	case "image/png", "image/jpeg":
		return strings.ToLower(media), true
	default:
		return "application/octet-stream", false
	}
}

func (h *handler) reparse(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct{}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	caseID, docID := r.PathValue("caseID"), r.PathValue("docID")
	err := documentservice.WithPublicParse(r.Context(), func(ctx context.Context) (string, error) {
		return h.store.DocumentParseKindContext(ctx, user, caseID, docID)
	}, func(ctx context.Context) error {
		return h.store.Reparse(ctx, user, caseID, docID)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": docID})
}

func (h *handler) startReview(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct {
		BusinessAt string `json:"business_at,omitempty"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if h.durable {
		var accepted store.JobAcceptance
		var err error
		if body.BusinessAt == "" {
			accepted, err = h.store.StartReviewAsync(r.Context(), user, r.PathValue("caseID"))
		} else {
			accepted, err = h.store.StartReviewAsyncAt(r.Context(), user, r.PathValue("caseID"), body.BusinessAt)
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, accepted)
		return
	}
	var id string
	var err error
	if body.BusinessAt == "" {
		id, err = h.store.StartReview(r.Context(), user, r.PathValue("caseID"))
	} else {
		id, err = h.store.StartReviewAt(r.Context(), user, r.PathValue("caseID"), body.BusinessAt)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	ws, err := h.store.Workspace(user, r.PathValue("caseID"), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, reviewIdentityJSON(ws))
}

func (h *handler) workspace(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	ws, err := h.store.Workspace(user, r.PathValue("caseID"), r.PathValue("runID"), r.URL.Query().Get("attempt_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, workspaceJSON(h.store, ws))
}

func (h *handler) retry(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct{}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if h.durable {
		accepted, err := h.store.RetryAsync(r.Context(), user, r.PathValue("caseID"), r.PathValue("runID"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, accepted)
		return
	}
	runID := r.PathValue("runID")
	if err := h.store.Retry(r.Context(), user, r.PathValue("caseID"), runID); err != nil {
		writeErr(w, err)
		return
	}
	ws, err := h.store.Workspace(user, r.PathValue("caseID"), runID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reviewIdentityJSON(ws))
}

func (h *handler) cancelReview(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct{}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	caseID, runID := r.PathValue("caseID"), r.PathValue("runID")
	if err := h.store.CancelReview(r.Context(), user, caseID, runID); err != nil {
		writeErr(w, err)
		return
	}
	ws, err := h.store.Workspace(user, caseID, runID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reviewIdentityJSON(ws))
}

func (h *handler) decide(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct {
		Action          string `json:"action"`
		Reason          string `json:"reason"`
		ExpectedVersion int    `json:"expected_version"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if err := h.store.Decide(user, r.PathValue("caseID"), r.PathValue("runID"), r.PathValue("findingID"), key, body.Action, body.Reason, body.ExpectedVersion); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) events(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	after := 0
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err == nil && n > 0 {
			after = n
		}
	}
	items, err := h.store.EventsAfter(user, r.PathValue("caseID"), r.PathValue("runID"), after)
	if err != nil {
		writeErr(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, fault.New(http.StatusInternalServerError, "stream_unavailable", "当前服务不支持事件流"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func(items []store.Event) bool {
		for _, event := range items {
			payload, _ := json.Marshal(map[string]any{"seq": event.Seq, "type": event.Type, "state": event.State})
			if _, err := io.WriteString(w, "id: "+strconv.Itoa(event.Seq)+"\ndata: "+string(payload)+"\n\n"); err != nil {
				return false
			}
			after = event.Seq
		}
		flusher.Flush()
		return true
	}
	if !send(items) {
		return
	}
	// Read only committed events; each poll queries the Store and rechecks the current session.
	// A reconnect resumes strictly after Last-Event-ID, including later decisions.
	poll := time.NewTicker(250 * time.Millisecond)
	defer poll.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
			if _, err := h.store.User(cookieValue(r)); err != nil {
				return // A logout/expiry also revokes an already-open stream.
			}
			items, err := h.store.EventsAfter(user, r.PathValue("caseID"), r.PathValue("runID"), after)
			if err != nil || (len(items) > 0 && !send(items)) {
				return
			}
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (h *handler) actor(w http.ResponseWriter, r *http.Request) (accounts.User, bool) {
	return h.actorForAudience(w, r, "client")
}

func cookieValue(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func sessionCookieOf(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: sessionCookie, Value: value, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: maxAge,
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return fault.TooLarge("请求正文超过 1MB")
		}
		return fault.Invalid("请求格式不正确")
	}
	raw = bytes.TrimSpace(raw)
	// All API write contracts require exactly one object (use {} for no fields).
	if len(raw) == 0 || raw[0] != '{' {
		return fault.Invalid("请求正文必须是一个 JSON 对象")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fault.Invalid("请求格式不正确")
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fault.Invalid("请求格式不正确")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, err error) {
	var fe *fault.Error
	if errors.As(err, &fe) {
		writeJSON(w, fe.Status, map[string]any{
			"error": map[string]any{"code": fe.Code, "message": fe.Message, "retryable": fe.Retryable},
		})
		return
	}
	log.Printf("request failed: %v", err)
	writeJSON(w, http.StatusInternalServerError, map[string]any{
		"error": map[string]any{"code": "internal", "message": "审查服务暂时不能完成这个操作", "retryable": true},
	})
}

func actorJSON(user accounts.User) map[string]string {
	return map[string]string{"id": user.ID, "display_name": user.DisplayName, "role": user.Role, "tenant_id": accounts.Tenant(user), "org_id": user.OrgID}
}

func cors(origins []string, next http.Handler) http.Handler {
	allow := map[string]bool{}
	for _, origin := range origins {
		allow[origin] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		trusted := allow[origin] || origin == scheme+"://"+r.Host
		// CORS alone only hides responses. Reject untrusted browser writes so
		// another same-site origin cannot use the authenticated cookie for CSRF.
		if origin != "" && !trusted && r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeErr(w, fault.Forbidden("请求来源不在允许列表内"))
			return
		}
		if allow[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key, Last-Event-ID")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func heading(text string, n int) string {
	line, _, _ := strings.Cut(text, "\n")
	line = strings.TrimSpace(line)
	rs := []rune(line)
	if len(rs) > 40 {
		return string(rs[:40]) + "…"
	}
	if line == "" {
		return "段落 " + strconv.Itoa(n)
	}
	return line
}
