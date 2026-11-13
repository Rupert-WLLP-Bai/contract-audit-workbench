package postgres

import (
	"context"
	"contractaudit/internal/approval/model"
	"contractaudit/internal/fault"
	platform "contractaudit/internal/platform/postgres"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"time"
)

type Submission struct{ tx platform.Executor }

func NewSubmission(tx platform.Executor) *Submission { return &Submission{tx: tx} }
func (r *Submission) HasActiveRound(ctx context.Context, tenantID, contractID string) (bool, error) {
	var active bool
	err := r.tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM clm_approval_rounds WHERE tenant_id=$1 AND contract_id=$2 AND state='in_review')`, tenantID, contractID).Scan(&active)
	return active, err
}
func (r *Submission) NextRoundNumber(ctx context.Context, tenantID, contractID string) (int, error) {
	var n int
	err := r.tx.QueryRow(ctx, `SELECT COALESCE(MAX(round_number),0)+1 FROM clm_approval_rounds WHERE tenant_id=$1 AND contract_id=$2`, tenantID, contractID).Scan(&n)
	return n, err
}
func (r *Submission) InsertRound(ctx context.Context, tenantID, contractID, roundID, revisionID string, number int, templateID, version string, routing []byte, digest string, risk []byte, actorID string) (int64, time.Time, error) {
	var v int64
	var started time.Time
	err := r.tx.QueryRow(ctx, `INSERT INTO clm_approval_rounds (tenant_id,contract_id,approval_round_id,contract_revision_id,round_number,state,workflow_template_id,workflow_template_version,routing_snapshot,routing_digest,risk_disposition_snapshot,initiated_by) VALUES ($1,$2,$3,$4,$5,'in_review',$6,$7,$8::jsonb,$9,$10::jsonb,$11) RETURNING row_version,started_at`, tenantID, contractID, roundID, revisionID, number, templateID, version, string(routing), digest, string(risk), actorID).Scan(&v, &started)
	if err != nil {
		var e *pgconn.PgError
		if errors.As(err, &e) && e.Code == "23505" {
			return 0, time.Time{}, fault.Conflict("合同已有进行中的会签轮次")
		}
	}
	return v, started, err
}
func (r *Submission) InsertTask(ctx context.Context, tenantID, contractID, roundID, taskID string, node model.WorkflowNodeSnapshot, state string) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO clm_approval_tasks (tenant_id,contract_id,approval_round_id,approval_task_id,node_code,node_order,title,role_code,is_final,state,activated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,CASE WHEN $10='active' THEN clock_timestamp() ELSE NULL END)`, tenantID, contractID, roundID, taskID, node.NodeCode, node.NodeOrder, node.Title, node.RoleCode, node.Final, state)
	return err
}
func (r *Submission) InsertAssignment(ctx context.Context, tenantID, contractID, roundID, taskID, roleCode string, assignee model.RoutingAssignee) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO clm_approval_task_assignments (tenant_id,contract_id,approval_round_id,approval_task_id,user_id,display_name,role_code,binding_org_scope) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, tenantID, contractID, roundID, taskID, assignee.UserID, assignee.DisplayName, roleCode, assignee.BindingOrgScope)
	return err
}
func (r *Submission) InsertMaterial(ctx context.Context, tenantID, contractID, roundID, taskID, revisionID, materialID, versionID string) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO clm_approval_task_materials (tenant_id,contract_id,approval_round_id,approval_task_id,contract_revision_id,material_id,material_version_id) VALUES ($1,$2,$3,$4,$5,$6,$7)`, tenantID, contractID, roundID, taskID, revisionID, materialID, versionID)
	return err
}
