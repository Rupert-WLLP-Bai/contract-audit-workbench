// File metadata (manifest mirror):
// Purpose: Reads Approval-owned workflow, task, assignment, decision and whitelist facts and bounded in-review batches using the caller-owned executor.
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
	model "contractaudit/internal/approval/model"
	"contractaudit/internal/fault"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"strconv"
	"strings"
	"time"

	platform "contractaudit/internal/platform/postgres"
)

// WorkflowReader reads Approval-owned workflow facts using the caller's transaction.
type WorkflowReader struct{ tx platform.Executor }

func NewWorkflowReader(tx platform.Executor) *WorkflowReader { return &WorkflowReader{tx: tx} }

func (r *WorkflowReader) InReview(ctx context.Context, tenant string, contractIDs []string) (map[string]bool, error) {
	result := make(map[string]bool)
	if len(contractIDs) == 0 {
		return result, nil
	}
	rows, err := r.tx.Query(ctx, `SELECT DISTINCT contract_id::text FROM clm_approval_rounds WHERE tenant_id=$1 AND contract_id=ANY(ARRAY(SELECT unnest($2::text[])::uuid)) AND state='in_review'`, tenant, contractIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}

const readPreviousRoundColumn = `(SELECT p.approval_round_id FROM clm_approval_rounds p
 WHERE p.tenant_id=r.tenant_id AND p.contract_id=r.contract_id AND p.round_number<r.round_number
 ORDER BY p.round_number DESC LIMIT 1)`
const readRoundColumns = `r.approval_round_id,r.contract_revision_id,r.round_number,r.state,
 r.workflow_template_id,r.workflow_template_version,r.routing_snapshot,r.risk_disposition_snapshot,
 r.row_version,r.initiated_by,r.started_at,r.ended_at,` + readPreviousRoundColumn
const readAssigneeColumn = `COALESCE((SELECT min(a.user_id) FROM clm_approval_task_assignments a
 WHERE a.tenant_id=t.tenant_id AND a.contract_id=t.contract_id
   AND a.approval_round_id=t.approval_round_id AND a.approval_task_id=t.approval_task_id),'')`

func readTextPtr(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	v := value.String
	return &v
}
func readTimePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	v := value.Time.UTC()
	return &v
}
func (r *WorkflowReader) LoadRound(ctx context.Context, tenantID, contractID, roundID string) (model.RoundRead, error) {
	var round model.RoundRead
	var ended pgtype.Timestamptz
	var previous pgtype.Text
	err := r.tx.QueryRow(ctx, `SELECT `+readRoundColumns+` FROM clm_approval_rounds r
		WHERE r.tenant_id=$1 AND r.contract_id=$2 AND r.approval_round_id=$3`,
		tenantID, contractID, roundID).Scan(
		&round.ApprovalRoundID, &round.ContractRevisionID, &round.RoundNumber, &round.State,
		&round.WorkflowTemplateID, &round.WorkflowTemplateVersion, &round.RoutingSnapshot,
		&round.RiskDispositionSnapshot, &round.RowVersion, &round.InitiatedBy, &round.StartedAt,
		&ended, &previous,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.RoundRead{}, fault.NotFound("没有找到对应的审批轮次")
	}
	if err != nil {
		return model.RoundRead{}, err
	}
	round.TenantID, round.ContractID = tenantID, contractID
	round.EndedAt = readTimePtr(ended)
	round.PreviousRoundID = readTextPtr(previous)
	return round, nil
}

func (r *WorkflowReader) LoadTaskByID(ctx context.Context, tenantID, taskID string) (model.ReadTaskRow, string, string, error) {
	var task model.ReadTaskRow
	var contractID, roundID string
	var activated, decided, created pgtype.Timestamptz
	err := r.tx.QueryRow(ctx, `SELECT t.approval_task_id::text,t.node_order,t.node_code,t.role_code,
		`+readAssigneeColumn+`,t.state,t.row_version,t.activated_at,t.decided_at,t.created_at,
		t.contract_id::text,t.approval_round_id::text
		FROM clm_approval_tasks t WHERE t.tenant_id=$1 AND t.approval_task_id=$2`, tenantID, taskID).Scan(
		&task.TaskID, &task.NodeOrder, &task.NodeCode, &task.RoleCode, &task.AssigneeSubjectID,
		&task.State, &task.RowVersion, &activated, &decided, &created, &contractID, &roundID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ReadTaskRow{}, "", "", fault.NotFound("没有找到对应的审批任务")
	}
	if err != nil {
		return model.ReadTaskRow{}, "", "", err
	}
	task.ActivatedAt, task.DecidedAt = readTimePtr(activated), readTimePtr(decided)
	_ = created
	return task, contractID, roundID, nil
}

func (r *WorkflowReader) LoadTasks(ctx context.Context, tenantID, contractID, roundID, actorID string) ([]model.ReadTaskRow, error) {
	rows, err := r.tx.Query(ctx, `SELECT t.approval_task_id::text,t.node_order,t.node_code,t.role_code,
		`+readAssigneeColumn+`,t.state,t.row_version,t.activated_at,t.decided_at,t.created_at,
		EXISTS (SELECT 1 FROM clm_approval_task_assignments mine
		  WHERE mine.tenant_id=t.tenant_id AND mine.contract_id=t.contract_id
		    AND mine.approval_round_id=t.approval_round_id AND mine.approval_task_id=t.approval_task_id
		    AND mine.user_id=$4)
		FROM clm_approval_tasks t WHERE t.tenant_id=$1 AND t.contract_id=$2 AND t.approval_round_id=$3
		ORDER BY t.node_order`, tenantID, contractID, roundID, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []model.ReadTaskRow{}
	for rows.Next() {
		var task model.ReadTaskRow
		var activated, decided, created pgtype.Timestamptz
		if err := rows.Scan(&task.TaskID, &task.NodeOrder, &task.NodeCode, &task.RoleCode,
			&task.AssigneeSubjectID, &task.State, &task.RowVersion, &activated, &decided, &created,
			&task.AssignedToCurrent); err != nil {
			return nil, err
		}
		task.ActivatedAt, task.DecidedAt = readTimePtr(activated), readTimePtr(decided)
		_ = created
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (r *WorkflowReader) LoadDecisions(ctx context.Context, tenantID, contractID, roundID string) ([]model.ReadDecisionRow, error) {
	rows, err := r.tx.Query(ctx, `SELECT d.decision_id::text,d.approval_task_id::text,t.node_order,t.node_code,
		d.decision,d.decided_by,d.comment,d.decided_at
		FROM clm_approval_decisions d
		JOIN clm_approval_tasks t ON t.tenant_id=d.tenant_id AND t.contract_id=d.contract_id
		  AND t.approval_round_id=d.approval_round_id AND t.approval_task_id=d.approval_task_id
		WHERE d.tenant_id=$1 AND d.contract_id=$2 AND d.approval_round_id=$3
		ORDER BY t.node_order`, tenantID, contractID, roundID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	decisions := []model.ReadDecisionRow{}
	for rows.Next() {
		var decision model.ReadDecisionRow
		if err := rows.Scan(&decision.DecisionID, &decision.TaskID, &decision.NodeOrder,
			&decision.NodeCode, &decision.Action, &decision.ActorSubjectID, &decision.Opinion,
			&decision.DecidedAt); err != nil {
			return nil, err
		}
		decisions = append(decisions, decision)
	}
	return decisions, rows.Err()
}

func (r *WorkflowReader) LoadMaterialKeys(ctx context.Context, tenantID, contractID, roundID, taskID string) ([]model.MaterialKey, error) {
	rows, err := r.tx.Query(ctx, `SELECT contract_revision_id::text,material_id::text,material_version_id::text FROM clm_approval_task_materials WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3 AND approval_task_id=$4 ORDER BY material_id`, tenantID, contractID, roundID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []model.MaterialKey{}
	for rows.Next() {
		var key model.MaterialKey
		if err := rows.Scan(&key.RevisionID, &key.MaterialID, &key.MaterialVersionID); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

const readTaskColumns = `t.approval_task_id::text,t.node_order,t.node_code,t.role_code,` + readAssigneeColumn + `,
 t.state,t.row_version,t.activated_at,t.decided_at,t.created_at`
const readDecisionColumns = `d.decision_id::text,d.decision,d.decided_by,d.comment,d.decided_at,d.approval_task_id::text`

func scanInboxCandidates(rows pgx.Rows, tenantID string) ([]model.InboxCandidate, error) {
	items := []model.InboxCandidate{}
	for rows.Next() {
		var item model.InboxCandidate
		var ended, activated, decided, created, decisionAt pgtype.Timestamptz
		var previous, taskID, taskCode, taskRole, taskAssignee, taskState pgtype.Text
		var taskOrder pgtype.Int2
		var taskVersion pgtype.Int8
		var decisionID, action, actor, opinion, decisionTask pgtype.Text
		if err := rows.Scan(&item.Round.ContractID, &item.Round.ApprovalRoundID, &item.Round.ContractRevisionID, &item.Round.RoundNumber, &item.Round.State, &item.Round.WorkflowTemplateID, &item.Round.WorkflowTemplateVersion, &item.Round.RoutingSnapshot, &item.Round.RiskDispositionSnapshot, &item.Round.RowVersion, &item.Round.InitiatedBy, &item.Round.StartedAt, &ended, &previous, &taskID, &taskOrder, &taskCode, &taskRole, &taskAssignee, &taskState, &taskVersion, &activated, &decided, &created, &decisionID, &action, &actor, &opinion, &decisionAt, &decisionTask); err != nil {
			return nil, err
		}
		item.Round.TenantID = tenantID
		item.Round.EndedAt, item.Round.PreviousRoundID = readTimePtr(ended), readTextPtr(previous)
		if taskID.Valid && taskID.String != "" {
			item.Task = &model.ReadTaskRow{TaskID: taskID.String, NodeOrder: int(taskOrder.Int16), NodeCode: taskCode.String, RoleCode: taskRole.String, AssigneeSubjectID: taskAssignee.String, State: taskState.String, RowVersion: taskVersion.Int64, ActivatedAt: readTimePtr(activated), DecidedAt: readTimePtr(decided)}
		}
		if decisionID.Valid && decisionID.String != "" {
			item.Decision = &model.ReadDecisionRow{DecisionID: decisionID.String, TaskID: decisionTask.String, NodeOrder: int(taskOrder.Int16), NodeCode: taskCode.String, Action: action.String, ActorSubjectID: actor.String, Opinion: opinion.String, DecidedAt: decisionAt.Time.UTC()}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *WorkflowReader) LoadInboxCandidates(ctx context.Context, tenantID, actorID, view string,
	hasAfter bool, afterKey time.Time, afterID string, limit int) ([]model.InboxCandidate, error) {
	var query strings.Builder
	args := []any{tenantID, actorID}
	switch view {
	case "todo":
		query.WriteString(`SELECT ` + `r.contract_id::text,` + readRoundColumns + `,` +
			readTaskColumns + `,NULL::text,NULL::text,NULL::text,NULL::text,NULL::timestamptz,NULL::text
		 FROM clm_approval_tasks t
		 JOIN clm_approval_task_assignments a ON a.tenant_id=t.tenant_id AND a.contract_id=t.contract_id
		   AND a.approval_round_id=t.approval_round_id AND a.approval_task_id=t.approval_task_id
		   AND a.user_id=$2
		 JOIN clm_approval_rounds r ON r.tenant_id=t.tenant_id AND r.contract_id=t.contract_id
		   AND r.approval_round_id=t.approval_round_id
		 WHERE r.tenant_id=$1 AND t.state='active' AND r.state='in_review'`)
		if hasAfter {
			args = append(args, afterKey.UTC(), afterID)
			query.WriteString(` AND (t.activated_at,t.approval_task_id) > ($3::timestamptz,$4::uuid)`)
		}
		args = append(args, limit)
		query.WriteString(` ORDER BY t.activated_at ASC,t.approval_task_id ASC LIMIT $` + strconv.Itoa(len(args)))
	case "done":
		query.WriteString(`SELECT ` + `r.contract_id::text,` + readRoundColumns + `,` +
			readTaskColumns + `,` + readDecisionColumns + `
		 FROM clm_approval_decisions d
		 JOIN clm_approval_tasks t ON t.tenant_id=d.tenant_id AND t.contract_id=d.contract_id
		   AND t.approval_round_id=d.approval_round_id AND t.approval_task_id=d.approval_task_id
		 JOIN clm_approval_rounds r ON r.tenant_id=d.tenant_id AND r.contract_id=d.contract_id
		   AND r.approval_round_id=d.approval_round_id
		 WHERE r.tenant_id=$1 AND d.decided_by=$2`)
		if hasAfter {
			args = append(args, afterKey.UTC(), afterID)
			query.WriteString(` AND (d.decided_at,d.decision_id) < ($3::timestamptz,$4::uuid)`)
		}
		args = append(args, limit)
		query.WriteString(` ORDER BY d.decided_at DESC,d.decision_id DESC LIMIT $` + strconv.Itoa(len(args)))
	case "initiated":
		query.WriteString(`SELECT ` + `r.contract_id::text,` + readRoundColumns + `,
			COALESCE(t.approval_task_id::text,''),COALESCE(t.node_order,0)::smallint,COALESCE(t.node_code,''),
			COALESCE(t.role_code,''),COALESCE(t.assignee,''),COALESCE(t.state,''),
			COALESCE(t.row_version,0),t.activated_at,t.decided_at,t.created_at,
			NULL::text,NULL::text,NULL::text,NULL::text,NULL::timestamptz,NULL::text
		 FROM clm_approval_rounds r
		 LEFT JOIN LATERAL (
		   SELECT active.approval_task_id,active.node_order,active.node_code,active.role_code,active.state,active.row_version,
		          active.activated_at,active.decided_at,active.created_at,
		          COALESCE((SELECT min(a.user_id) FROM clm_approval_task_assignments a
		            WHERE a.tenant_id=active.tenant_id AND a.contract_id=active.contract_id
		              AND a.approval_round_id=active.approval_round_id
		              AND a.approval_task_id=active.approval_task_id),'') AS assignee
		   FROM clm_approval_tasks active WHERE active.tenant_id=r.tenant_id
		     AND active.contract_id=r.contract_id AND active.approval_round_id=r.approval_round_id
		     AND active.state='active' LIMIT 1
		 ) t ON true
		 WHERE r.tenant_id=$1 AND r.initiated_by=$2`)
		if hasAfter {
			args = append(args, afterKey.UTC(), afterID)
			query.WriteString(` AND (r.started_at,r.approval_round_id) < ($3::timestamptz,$4::uuid)`)
		}
		args = append(args, limit)
		query.WriteString(` ORDER BY r.started_at DESC,r.approval_round_id DESC LIMIT $` + strconv.Itoa(len(args)))
	default:
		return nil, fault.Invalid("view 必须是 todo、done 或 initiated")
	}
	rows, err := r.tx.Query(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := scanInboxCandidates(rows, tenantID)
	if err != nil {
		return nil, err
	}
	if view == "todo" {
		// The todo query joined this subject's own assignment, including nodes
		// whose frozen routing lists several eligible assignees.
		for index := range items {
			if items[index].Task != nil {
				items[index].Task.AssignedToCurrent = true
			}
		}
	}
	return items, nil
}

func (r *WorkflowReader) LoadHistory(ctx context.Context, tenantID, contractID string, before, limit int) ([]model.HistoryCandidate, error) {
	args := []any{tenantID, contractID}
	where := `r.tenant_id=$1 AND r.contract_id=$2`
	if before > 0 {
		args = append(args, before)
		where += ` AND r.round_number < $3`
	}
	args = append(args, limit)
	rows, err := r.tx.Query(ctx, `SELECT `+readRoundColumns+`,
		COALESCE((SELECT count(*) FROM clm_approval_tasks t
		  WHERE t.tenant_id=r.tenant_id AND t.contract_id=r.contract_id
		    AND t.approval_round_id=r.approval_round_id),0)::int,
		COALESCE((SELECT count(*) FROM clm_approval_decisions d
		  WHERE d.tenant_id=r.tenant_id AND d.contract_id=r.contract_id
		    AND d.approval_round_id=r.approval_round_id),0)::int,
		t.approval_task_id::text,t.node_order,t.node_code,t.role_code,t.assignee,t.state,
		t.row_version,t.activated_at,t.decided_at
		FROM clm_approval_rounds r
		LEFT JOIN LATERAL (
		  SELECT active.approval_task_id,active.node_order,active.node_code,active.role_code,active.state,active.row_version,
		         active.activated_at,active.decided_at,
		         COALESCE((SELECT min(a.user_id) FROM clm_approval_task_assignments a
		           WHERE a.tenant_id=active.tenant_id AND a.contract_id=active.contract_id
		             AND a.approval_round_id=active.approval_round_id
		             AND a.approval_task_id=active.approval_task_id),'') AS assignee
		  FROM clm_approval_tasks active WHERE active.tenant_id=r.tenant_id
		    AND active.contract_id=r.contract_id AND active.approval_round_id=r.approval_round_id
		    AND active.state='active' LIMIT 1
		) t ON true
		WHERE `+where+` ORDER BY r.round_number DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pending := []model.HistoryCandidate{}
	for rows.Next() {
		var item model.HistoryCandidate
		var ended, activated, decided pgtype.Timestamptz
		var previous pgtype.Text
		var taskID, taskCode, taskRole, taskAssignee, taskState pgtype.Text
		var taskOrder pgtype.Int2
		var taskRowVersion pgtype.Int8
		if err := rows.Scan(
			&item.Round.ApprovalRoundID, &item.Round.ContractRevisionID, &item.Round.RoundNumber,
			&item.Round.State, &item.Round.WorkflowTemplateID, &item.Round.WorkflowTemplateVersion,
			&item.Round.RoutingSnapshot, &item.Round.RiskDispositionSnapshot, &item.Round.RowVersion,
			&item.Round.InitiatedBy, &item.Round.StartedAt, &ended, &previous,
			&item.TaskCount, &item.DecisionCount,
			&taskID, &taskOrder, &taskCode, &taskRole, &taskAssignee, &taskState,
			&taskRowVersion, &activated, &decided,
		); err != nil {
			return nil, err
		}
		item.Round.TenantID, item.Round.ContractID = tenantID, contractID
		item.Round.EndedAt = readTimePtr(ended)
		item.Round.PreviousRoundID = readTextPtr(previous)
		if taskID.Valid && taskID.String != "" {
			item.Active = &model.ReadTaskRow{
				TaskID: taskID.String, NodeOrder: int(taskOrder.Int16), NodeCode: taskCode.String,
				RoleCode: taskRole.String, AssigneeSubjectID: taskAssignee.String,
				State: taskState.String, RowVersion: taskRowVersion.Int64,
				ActivatedAt: readTimePtr(activated), DecidedAt: readTimePtr(decided),
			}
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return pending, nil
}
