package httpapi

import (
	"net/http"

	"contractaudit/internal/accounts"
	"contractaudit/internal/store"
)

// CLM-07 routes: a manager submits the frozen current revision, while platform
// administrators publish versioned templates and replace role bindings.
// Templates and bindings are inputs to the routing snapshot; they are never
// consulted by an already-submitted round.

func (h *handler) submitContractApproval(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body store.SubmitContractApprovalInput) (any, error) {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			return nil, err
		}
		body.ContractID = req.PathValue("contractID")
		body.IdempotencyKey = key
		return h.store.SubmitContractApproval(req.Context(), user, body)
	})
}

// decideApprovalTask is the CLM-08 command. A decision always targets the
// frozen task ID and carries the round row_version observed by the approver;
// the server resolves tenant, actor and role from identity, never from JSON.
func (h *handler) decideApprovalTask(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body store.DecideApprovalTaskInput) (any, error) {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			return nil, err
		}
		body.TaskID = req.PathValue("taskID")
		body.IdempotencyKey = key
		return h.store.DecideApprovalTask(req.Context(), user, body)
	})
}

func (h *handler) publishWorkflowTemplate(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body store.PublishWorkflowTemplateInput) (any, error) {
		return h.store.PublishWorkflowTemplate(req.Context(), user, body)
	})
}

func (h *handler) replaceWorkflowRoleBindings(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusOK, func(user accounts.User, req *http.Request, body store.ReplaceWorkflowRoleInput) (any, error) {
		body.RoleCode = req.PathValue("roleCode")
		return h.store.ReplaceWorkflowRoleBindings(req.Context(), user, body)
	})
}
