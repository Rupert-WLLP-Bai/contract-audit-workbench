// File metadata (manifest mirror):
// Purpose: Coordinates immutable workflow template publication and role binding normalization through typed repository and directory ports.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"contractaudit/internal/accounts"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"contractaudit/internal/approval/model"
	"contractaudit/internal/fault"
)

type TemplateRepository interface {
	LoadTemplate(context.Context, string, string, string) (model.WorkflowTemplateSummary, bool, error)
	InsertTemplate(context.Context, string, string, model.PublishWorkflowTemplateInput, string) (model.WorkflowTemplateSummary, error)
}

func PublishTemplate(ctx context.Context, tenantID, actorID string, input model.PublishWorkflowTemplateInput, repo TemplateRepository) (model.WorkflowTemplateSummary, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return model.WorkflowTemplateSummary{}, err
	}
	h := sha256.Sum256(raw)
	digest := hex.EncodeToString(h[:])
	existing, found, err := repo.LoadTemplate(ctx, tenantID, input.TemplateID, input.TemplateVersion)
	if err != nil {
		return model.WorkflowTemplateSummary{}, err
	}
	if found {
		if existing.TemplateDigest != digest {
			return model.WorkflowTemplateSummary{}, fault.Conflict("该会签模板版本已存在且内容不同，请发布新版本")
		}
		return existing, nil
	}
	return repo.InsertTemplate(ctx, tenantID, actorID, input, digest)
}

type Directory interface {
	LookupUser(string) (accounts.User, bool)
}
func NormalizeRoleBindings(tenantID string, input []model.WorkflowRoleBindingInput, directory Directory) ([]model.WorkflowRoleBindingSummary, error) {
	seenScopes := map[string]bool{}
	out := make([]model.WorkflowRoleBindingSummary, 0, len(input))
	for _, b := range input {
		scope := strings.TrimSpace(b.OrgScope)
		if seenScopes[scope] {
			return nil, fault.Invalid("同一角色的绑定范围不能重复")
		}
		seenScopes[scope] = true
		seen := map[string]bool{}
		users := make([]string, 0, len(b.UserIDs))
		for _, raw := range b.UserIDs {
			id := strings.TrimSpace(raw)
			if id == "" || seen[id] {
				continue
			}
			u, ok := directory.LookupUser(id)
			if !ok || !u.Enabled || u.TenantID != tenantID {
				return nil, fault.Invalid("角色绑定只能指向本租户内已启用的目录账号")
			}
			seen[id] = true
			users = append(users, id)
		}
		sort.Strings(users)
		out = append(out, model.WorkflowRoleBindingSummary{OrgScope: scope, UserIDs: users})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OrgScope < out[j].OrgScope })
	return out, nil
}

type ActiveTemplateRepository interface {
	TemplateRepository
	LoadDefaultPointer(context.Context, string) (string, string, bool, error)
	LoadRoleBindings(context.Context, string) ([]model.RoleBindingRow, error)
}
type ActiveTemplate struct {
	Summary model.WorkflowTemplateSummary
	Nodes   []model.WorkflowTemplateNodeInput
}

func LoadActiveTemplate(ctx context.Context, repo ActiveTemplateRepository, tenantID, requestedID string) (ActiveTemplate, error) {
	id, version, found, err := repo.LoadDefaultPointer(ctx, tenantID)
	if err != nil {
		return ActiveTemplate{}, err
	}
	if !found {
		return ActiveTemplate{}, fault.Conflict("尚未发布会签模板，请先由管理员发布")
	}
	if requestedID != "" && requestedID != id {
		return ActiveTemplate{}, fault.Conflict("指定模板不是当前生效的会签模板")
	}
	s, found, err := repo.LoadTemplate(ctx, tenantID, id, version)
	if err != nil {
		return ActiveTemplate{}, err
	}
	if !found {
		return ActiveTemplate{}, fault.Conflict("当前生效的会签模板版本不存在")
	}
	input := model.PublishWorkflowTemplateInput{TemplateID: s.TemplateID, TemplateVersion: s.TemplateVersion, Name: s.Name, StartAction: s.StartAction, StartActionIsApprovalNode: s.StartActionIsApprovalNode, Execution: s.Execution, Nodes: make([]model.WorkflowTemplateNodeInput, 0, len(s.Nodes))}
	for _, n := range s.Nodes {
		input.Nodes = append(input.Nodes, model.WorkflowTemplateNodeInput{NodeCode: n.NodeCode, Title: n.Title, RoleCode: n.RoleCode, Final: n.Final})
	}
	if err := model.ValidateWorkflowTemplate(model.NormalizeWorkflowTemplateInput(input)); err != nil {
		return ActiveTemplate{}, fault.Conflict("当前生效的会签模板内容不合法")
	}
	return ActiveTemplate{Summary: s, Nodes: input.Nodes}, nil
}
