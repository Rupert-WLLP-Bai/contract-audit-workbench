package httpapi

import (
	"context"
	"mime"
	"net/http"

	"contractaudit/internal/accounts"
	"contractaudit/internal/documentservice"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

func (h *handler) templates(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": h.store.Templates()})
}

func (h *handler) generate(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct {
		TemplateID         string            `json:"template_id"`
		Fields             map[string]string `json:"fields"`
		Intent             string            `json:"intent,omitempty"`
		MaterialID         string            `json:"material_id,omitempty"`
		ReplacesDocumentID string            `json:"replaces_document_id,omitempty"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	caseID := r.PathValue("caseID")
	var item store.GenerationRecord
	err := documentservice.WithPublicParse(r.Context(), func(ctx context.Context) (string, error) {
		return h.store.GenerationParseKind(ctx, user, caseID)
	}, func(ctx context.Context) error {
		var err error
		item, err = h.store.GenerateWithOptions(ctx, user, caseID, body.TemplateID, body.Fields, store.MaterialUploadOptions{Intent: body.Intent, MaterialID: body.MaterialID, ReplacesDocumentID: body.ReplacesDocumentID, IdempotencyKey: r.Header.Get("Idempotency-Key")})
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	status := http.StatusCreated
	if item.Replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, item)
}

func (h *handler) createRemediation(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand[store.RemediationInput](h, w, r, http.StatusCreated, func(user accounts.User, r *http.Request, body store.RemediationInput) (any, error) {
		return h.store.CreateRemediation(user, r.PathValue("caseID"), body)
	})
}

func (h *handler) submitRemediation(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand[store.RemediationSubmitInput](h, w, r, http.StatusOK, func(user accounts.User, r *http.Request, body store.RemediationSubmitInput) (any, error) {
		return h.store.SubmitRemediation(user, r.PathValue("caseID"), r.PathValue("itemID"), body)
	})
}

func (h *handler) verifyRemediation(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand[store.RemediationVerifyInput](h, w, r, http.StatusOK, func(user accounts.User, r *http.Request, body store.RemediationVerifyInput) (any, error) {
		return h.store.VerifyRemediation(user, r.PathValue("caseID"), r.PathValue("itemID"), body)
	})
}

func (h *handler) createReport(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct {
		RunID     string `json:"run_id"`
		AttemptID string `json:"attempt_id"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	key, err := writeIdempotencyKey(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	item, err := h.store.CreateReportWithIdempotency(r.Context(), user, r.PathValue("caseID"), body.RunID, body.AttemptID, key)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h *handler) readReport(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "html" {
		writeErr(w, fault.Invalid("报告格式必须为 json 或 html"))
		return
	}
	record, body, err := h.store.ReadReport(user, r.PathValue("caseID"), r.PathValue("reportID"), format)
	if err != nil {
		writeErr(w, err)
		return
	}
	contentType := "application/json; charset=utf-8"
	if format == "html" {
		contentType = "text/html; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "audit-" + record.ID + "." + format}))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *handler) archive(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct {
		ReportID string `json:"report_id"`
		Reason   string `json:"reason"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	item, err := h.store.Archive(user, r.PathValue("caseID"), body.ReportID, body.Reason)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
