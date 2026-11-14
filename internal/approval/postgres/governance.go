// File metadata (manifest mirror):
// Purpose: Reads and writes Approval-owned workflow templates, nodes, default pointers and current role bindings using a caller-owned transaction executor.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package postgres

import (
	"context"
	"contractaudit/internal/approval/model"
	"contractaudit/internal/fault"
	platform "contractaudit/internal/platform/postgres"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Governance struct{ tx platform.Executor }

func NewGovernance(tx platform.Executor) *Governance { return &Governance{tx: tx} }
func (r *Governance) LoadTemplate(ctx context.Context, tenantID, templateID, version string) (model.WorkflowTemplateSummary, bool, error) {
	var s model.WorkflowTemplateSummary
	err := r.tx.QueryRow(ctx, `SELECT name,start_action,start_action_is_approval_node,execution,template_digest,published_by,published_at FROM clm_workflow_templates WHERE tenant_id=$1 AND template_id=$2 AND template_version=$3`, tenantID, templateID, version).Scan(&s.Name, &s.StartAction, &s.StartActionIsApprovalNode, &s.Execution, &s.TemplateDigest, &s.PublishedBy, &s.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	s.TemplateID = templateID
	s.TemplateVersion = version
	rows, err := r.tx.Query(ctx, `SELECT node_code,node_order,title,role_code,is_final FROM clm_workflow_template_nodes WHERE tenant_id=$1 AND template_id=$2 AND template_version=$3 ORDER BY node_order`, tenantID, templateID, version)
	if err != nil {
		return s, false, err
	}
	defer rows.Close()
	s.Nodes = []model.WorkflowTemplateNode{}
	for rows.Next() {
		var n model.WorkflowTemplateNode
		if err := rows.Scan(&n.NodeCode, &n.NodeOrder, &n.Title, &n.RoleCode, &n.Final); err != nil {
			return s, false, err
		}
		s.Nodes = append(s.Nodes, n)
	}
	if err := rows.Err(); err != nil {
		return s, false, err
	}
	return s, true, nil
}
func (r *Governance) InsertTemplate(ctx context.Context, tenantID, actorID string, input model.PublishWorkflowTemplateInput, digest string) (model.WorkflowTemplateSummary, error) {
	s := model.WorkflowTemplateSummary{TemplateID: input.TemplateID, TemplateVersion: input.TemplateVersion, Name: input.Name, StartAction: input.StartAction, StartActionIsApprovalNode: input.StartActionIsApprovalNode, Execution: input.Execution, TemplateDigest: digest, PublishedBy: actorID, Nodes: make([]model.WorkflowTemplateNode, 0, len(input.Nodes))}
	err := r.tx.QueryRow(ctx, `INSERT INTO clm_workflow_templates (tenant_id,template_id,template_version,name,start_action,start_action_is_approval_node,execution,node_count,template_digest,published_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING published_at`, tenantID, input.TemplateID, input.TemplateVersion, input.Name, input.StartAction, input.StartActionIsApprovalNode, input.Execution, len(input.Nodes), digest, actorID).Scan(&s.PublishedAt)
	if err != nil {
		var e *pgconn.PgError
		if errors.As(err, &e) && e.Code == "23505" {
			return model.WorkflowTemplateSummary{}, fault.Conflict("该会签模板版本已被他人发布")
		}
		return model.WorkflowTemplateSummary{}, err
	}
	for i, n := range input.Nodes {
		if _, err := r.tx.Exec(ctx, `INSERT INTO clm_workflow_template_nodes (tenant_id,template_id,template_version,node_code,node_order,title,role_code,is_final) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, tenantID, input.TemplateID, input.TemplateVersion, n.NodeCode, i+1, n.Title, n.RoleCode, n.Final); err != nil {
			return model.WorkflowTemplateSummary{}, err
		}
		s.Nodes = append(s.Nodes, model.WorkflowTemplateNode{NodeCode: n.NodeCode, NodeOrder: i + 1, Title: n.Title, RoleCode: n.RoleCode, Final: n.Final})
	}
	_, err = r.tx.Exec(ctx, `INSERT INTO clm_workflow_template_pointers (tenant_id,pointer_key,template_id,template_version,updated_by) VALUES ($1,'default',$2,$3,$4) ON CONFLICT (tenant_id,pointer_key) DO UPDATE SET template_id=EXCLUDED.template_id, template_version=EXCLUDED.template_version,updated_by=EXCLUDED.updated_by,updated_at=clock_timestamp()`, tenantID, input.TemplateID, input.TemplateVersion, actorID)
	if err != nil {
		return model.WorkflowTemplateSummary{}, err
	}
	return s, nil
}

func (r *Governance) ReplaceRole(ctx context.Context, tenantID, actorID, roleCode string, bindings []model.WorkflowRoleBindingSummary) (model.WorkflowRoleSummary, error) {
	var v int64
	err := r.tx.QueryRow(ctx, `SELECT row_version FROM clm_workflow_roles WHERE tenant_id=$1 AND role_code=$2 FOR UPDATE`, tenantID, roleCode).Scan(&v)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		v = 1
		_, err = r.tx.Exec(ctx, `INSERT INTO clm_workflow_roles (tenant_id,role_code,row_version,updated_by) VALUES ($1,$2,1,$3)`, tenantID, roleCode, actorID)
	case err != nil:
		return model.WorkflowRoleSummary{}, err
	default:
		v++
		_, err = r.tx.Exec(ctx, `UPDATE clm_workflow_roles SET row_version=$3, updated_by=$4, updated_at=clock_timestamp() WHERE tenant_id=$1 AND role_code=$2`, tenantID, roleCode, v, actorID)
	}
	if err != nil {
		return model.WorkflowRoleSummary{}, err
	}
	if _, err = r.tx.Exec(ctx, `DELETE FROM clm_workflow_role_bindings WHERE tenant_id=$1 AND role_code=$2`, tenantID, roleCode); err != nil {
		return model.WorkflowRoleSummary{}, err
	}
	for _, b := range bindings {
		for _, id := range b.UserIDs {
			if _, err = r.tx.Exec(ctx, `INSERT INTO clm_workflow_role_bindings (tenant_id,role_code,org_scope,user_id) VALUES ($1,$2,$3,$4)`, tenantID, roleCode, b.OrgScope, id); err != nil {
				return model.WorkflowRoleSummary{}, err
			}
		}
	}
	s := model.WorkflowRoleSummary{RoleCode: roleCode, RowVersion: v, Bindings: bindings, UpdatedBy: actorID}
	err = r.tx.QueryRow(ctx, `SELECT updated_at FROM clm_workflow_roles WHERE tenant_id=$1 AND role_code=$2`, tenantID, roleCode).Scan(&s.UpdatedAt)
	return s, err
}

func (r *Governance) LoadDefaultPointer(ctx context.Context, tenantID string) (string, string, bool, error) {
	var id, version string
	err := r.tx.QueryRow(ctx, `SELECT template_id,template_version FROM clm_workflow_template_pointers WHERE tenant_id=$1 AND pointer_key='default'`, tenantID).Scan(&id, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	return id, version, err == nil, err
}
func (r *Governance) LoadRoleBindings(ctx context.Context, tenantID string) ([]model.RoleBindingRow, error) {
	rows, err := r.tx.Query(ctx, `SELECT role_code,org_scope,user_id FROM clm_workflow_role_bindings WHERE tenant_id=$1 ORDER BY role_code,org_scope,user_id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.RoleBindingRow{}
	for rows.Next() {
		var b model.RoleBindingRow
		if err := rows.Scan(&b.RoleCode, &b.OrgScope, &b.UserID); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
