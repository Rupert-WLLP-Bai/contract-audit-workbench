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

// issueApprovalCredential is the CLM-11 command. The body is empty: subject,
// role, organization, task scope and row versions are all resolved and signed
// server-side. The returned token is short-lived and is not a CA signature.
func (h *handler) issueApprovalCredential(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, _ struct{}) (any, error) {
		return h.store.IssueApprovalCredential(req.Context(), user, req.PathValue("taskID"))
	})
}

type transferApprovalTaskRequest struct {
	ToUserID               string `json:"to_user_id"`
	Reason                 string `json:"reason"`
	ExpectedTaskRowVersion int64  `json:"expected_task_row_version"`
}

// transferApprovalTask is the CLM-11 reassignment command. Only a tenant
// administrator may transfer; the transfer fact is append-only and the new
// assignee must still hold the frozen role binding.
func (h *handler) transferApprovalTask(w http.ResponseWriter, r *http.Request) {
	serveClientJSONCommand(h, w, r, http.StatusCreated, func(user accounts.User, req *http.Request, body transferApprovalTaskRequest) (any, error) {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			return nil, err
		}
		return h.store.TransferApprovalTask(req.Context(), user, store.TransferApprovalTaskInput{
			TaskID:                 req.PathValue("taskID"),
			ToUserID:               body.ToUserID,
			Reason:                 body.Reason,
			ExpectedTaskRowVersion: body.ExpectedTaskRowVersion,
			IdempotencyKey:         key,
		})
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
