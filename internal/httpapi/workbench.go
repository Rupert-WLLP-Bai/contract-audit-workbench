package httpapi

import (
	"net/http"
	"strings"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

func (h *handler) workbench(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	periods, exists := query["period"]
	if len(query) != 1 || !exists || len(periods) != 1 {
		writeErr(w, fault.Invalid("工作台请求需要唯一的period参数"))
		return
	}
	model := store.WorkbenchModel{Status: "unconfigured", Label: "未配置审查模型"}
	if h.models != nil {
		model.Name = strings.TrimSpace(h.models.Name())
		model.OCRName = strings.TrimSpace(h.models.OCRName())
		if model.Name != "" {
			model.Status = "configured"
			model.Label = "审查模型已配置：" + model.Name
		}
	}
	item, err := h.store.WorkbenchSnapshot(r.Context(), user, periods[0], model)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *handler) updateCaseProfile(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand[store.CaseProfileInput](h, w, r, http.StatusOK, func(user accounts.User, r *http.Request, body store.CaseProfileInput) (any, error) {
		return h.store.UpdateCaseProfile(user, r.PathValue("caseID"), body)
	})
}

func (h *handler) createWorkbenchTask(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand[store.WorkbenchTaskInput](h, w, r, http.StatusCreated, func(user accounts.User, r *http.Request, body store.WorkbenchTaskInput) (any, error) {
		return h.store.CreateWorkbenchTask(user, r.PathValue("caseID"), body)
	})
}

func (h *handler) updateWorkbenchTask(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand[store.WorkbenchTaskInput](h, w, r, http.StatusOK, func(user accounts.User, r *http.Request, body store.WorkbenchTaskInput) (any, error) {
		return h.store.UpdateWorkbenchTask(user, r.PathValue("caseID"), r.PathValue("taskID"), body)
	})
}

func (h *handler) createCaseRelation(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand[store.CaseRelationInput](h, w, r, http.StatusCreated, func(user accounts.User, r *http.Request, body store.CaseRelationInput) (any, error) {
		return h.store.CreateCaseRelation(user, r.PathValue("caseID"), body)
	})
}

func (h *handler) deleteCaseRelation(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteCaseRelation(user, r.PathValue("caseID"), r.PathValue("relationID")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) createKnowledge(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body store.KnowledgeInput
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	item, err := h.store.CreateKnowledge(user, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h *handler) getKnowledge(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	item, err := h.store.GetKnowledge(user, r.PathValue("knowledgeID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *handler) getKnowledgeVersion(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	item, err := h.store.GetKnowledgeVersion(user, r.PathValue("knowledgeID"), r.PathValue("versionID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
