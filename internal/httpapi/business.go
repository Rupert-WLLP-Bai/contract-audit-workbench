package httpapi

import (
	"context"
	"mime"
	"net/http"

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
		TemplateID string            `json:"template_id"`
		Fields     map[string]string `json:"fields"`
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
		item, err = h.store.Generate(ctx, user, caseID, body.TemplateID, body.Fields)
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h *handler) createRemediation(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body store.RemediationInput
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	item, err := h.store.CreateRemediation(user, r.PathValue("caseID"), body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h *handler) submitRemediation(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body store.RemediationSubmitInput
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	item, err := h.store.SubmitRemediation(user, r.PathValue("caseID"), r.PathValue("itemID"), body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *handler) verifyRemediation(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body store.RemediationVerifyInput
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	item, err := h.store.VerifyRemediation(user, r.PathValue("caseID"), r.PathValue("itemID"), body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
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
	item, err := h.store.CreateReportContext(r.Context(), user, r.PathValue("caseID"), body.RunID, body.AttemptID)
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
