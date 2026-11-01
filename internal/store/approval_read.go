package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Contract approval read models for the workbench. They consume the round,
// task, assignment and decision tables owned by the sequential approval
// command (migrations 0023/0024). Reading never mutates a round or a decision.
// Each contract row is re-authorized through the same contract policy as the
// write commands; an invisible contract is indistinguishable from a missing one.
const (
	approvalInboxDefaultLimit    = 25
	approvalInboxMaxLimit        = 100
	approvalInboxCursorVersion   = 1
	approvalInboxCursorMaxBytes  = 2048
	approvalHistoryDefaultLimit  = 20
	approvalHistoryMaxLimit      = 50
	approvalRoundCursorVersion   = 1
	approvalRoundCursorMaxBytes  = 512
	approvalInboxViewTodo        = "todo"
	approvalInboxViewDone        = "done"
	approvalInboxViewInitiated   = "initiated"
	approvalIndependentNodeLimit = 6
)

// ApprovalInboxQuery is one validated workbench list request. View is one of
// todo, done or initiated and is bound into the opaque cursor.
type ApprovalInboxQuery struct {
	View   string
	Cursor string
	Limit  int
}

// ApprovalInboxItem is the common projection of the three workbench views.
// Task and decision fields are nil when the view does not contain them. The
// response never includes draft content or restricted supplier values.
type ApprovalInboxItem struct {
	View                   string     `json:"view"`
	ContractID             string     `json:"contract_id"`
	ContractName           string     `json:"contract_name"`
	ContractNumber         *string    `json:"contract_number"`
	ContractState          string     `json:"contract_state"`
	ContractRevisionID     string     `json:"contract_revision_id"`
	ApprovalRoundID        string     `json:"approval_round_id"`
	RoundNumber            int        `json:"round_number"`
	RoundState             string     `json:"round_state"`
	RoundStartedAt         time.Time  `json:"round_started_at"`
	RoundEndedAt           *time.Time `json:"round_ended_at"`
	TaskID                 *string    `json:"task_id"`
	NodeOrder              *int       `json:"node_order"`
	NodeCode               *string    `json:"node_code"`
	RoleCode               *string    `json:"role_code"`
	AssigneeSubjectID      *string    `json:"assignee_subject_id"`
	AssigneeDisplay        *string    `json:"assignee_display"`
	TaskState              *string    `json:"task_state"`
	TaskRowVersion         *int64     `json:"task_row_version"`
	TaskActivatedAt        *time.Time `json:"task_activated_at"`
	TaskDecidedAt          *time.Time `json:"task_decided_at"`
	DecisionID             *string    `json:"decision_id"`
	DecisionAction         *string    `json:"decision_action"`
	DecisionActorSubjectID *string    `json:"decision_actor_subject_id"`
	DecisionActorDisplay   *string    `json:"decision_actor_display"`
	DecisionOpinion        *string    `json:"decision_opinion"`
	DecisionDecidedAt      *time.Time `json:"decision_decided_at"`
	AllowedActions         []string   `json:"allowed_actions"`
}

// ApprovalInboxPage is one stable keyset page. The cursor is opaque and binds
// tenant, actor and view, so it cannot be replayed to read another view.
type ApprovalInboxPage struct {
	Items      []ApprovalInboxItem `json:"items"`
	NextCursor *string             `json:"next_cursor"`
	HasMore    bool                `json:"has_more"`
}

// ApprovalRoundReadSummary is a historical round without opinions or routing
// snapshot internals.
type ApprovalRoundReadSummary struct {
	ApprovalRoundID         string     `json:"approval_round_id"`
	RoundNumber             int        `json:"round_number"`
	ContractRevisionID      string     `json:"contract_revision_id"`
	State                   string     `json:"state"`
	WorkflowTemplateID      string     `json:"workflow_template_id"`
	WorkflowTemplateVersion string     `json:"workflow_template_version"`
	InitiatedBy             string     `json:"initiated_by"`
	InitiatedByDisplay      string     `json:"initiated_by_display"`
	StartedAt               time.Time  `json:"started_at"`
	EndedAt                 *time.Time `json:"ended_at"`
	PreviousRoundID         *string    `json:"previous_round_id"`
	RowVersion              int64      `json:"row_version"`
}

// ApprovalReadTask is one node in a round. The assignment is a historical
// routing fact, not a live permission grant.
type ApprovalReadTask struct {
	TaskID            string     `json:"task_id"`
	NodeOrder         int        `json:"node_order"`
	NodeCode          string     `json:"node_code"`
	RoleCode          string     `json:"role_code"`
	AssigneeSubjectID string     `json:"assignee_subject_id"`
	AssigneeDisplay   string     `json:"assignee_display"`
	State             string     `json:"state"`
	RowVersion        int64      `json:"row_version"`
	ActivatedAt       *time.Time `json:"activated_at"`
	DecidedAt         *time.Time `json:"decided_at"`
}

// ApprovalReadDecision is an append-only decision with its opinion.
type ApprovalReadDecision struct {
	DecisionID     string    `json:"decision_id"`
	TaskID         string    `json:"task_id"`
	NodeOrder      int       `json:"node_order"`
	NodeCode       string    `json:"node_code"`
	Action         string    `json:"action"`
	ActorSubjectID string    `json:"actor_subject_id"`
	ActorDisplay   string    `json:"actor_display"`
	Opinion        string    `json:"opinion"`
	DecidedAt      time.Time `json:"decided_at"`
}

// ApprovalRoundReadDetail is one round with its task timeline and opinions.
// RevisionDiff compares the round's frozen revision with the previous round's
// frozen revision; a same-revision resubmission explicitly reports no change.
type ApprovalRoundReadDetail struct {
	ContractID            string                    `json:"contract_id"`
	ContractName          string                    `json:"contract_name"`
	ContractNumber        *string                   `json:"contract_number"`
	ContractState         string                    `json:"contract_state"`
	ContractRowVersion    int64                     `json:"contract_row_version"`
	CurrentRevisionID     *string                   `json:"current_revision_id"`
	Round                 ApprovalRoundReadSummary  `json:"round"`
	Tasks                 []ApprovalReadTask        `json:"tasks"`
	Decisions             []ApprovalReadDecision    `json:"decisions"`
	ActiveTaskID          *string                   `json:"active_task_id"`
	RiskDispositionState  string                    `json:"risk_disposition_state"`
	RiskDispositionReason string                    `json:"risk_disposition_reason,omitempty"`
	PreviousRound         *ApprovalRoundReadSummary `json:"previous_round"`
	RevisionChanged       bool                      `json:"revision_changed"`
	RevisionDiff          *ContractVersionDiff      `json:"revision_diff"`
	AllowedActions        []string                  `json:"allowed_actions"`
}

// ApprovalTaskReadDetail answers the task detail route: the selected task plus
// the complete round history needed to locate its opinions and revision diff.
type ApprovalTaskReadDetail struct {
	SelectedTaskID string                  `json:"selected_task_id"`
	RoundDetail    ApprovalRoundReadDetail `json:"round_detail"`
}

// ApprovalRoundHistoryItem is one round in a contract's history. TaskCount and
// DecisionCount describe the stored facts, not an inferred approval result.
type ApprovalRoundHistoryItem struct {
	Round         ApprovalRoundReadSummary `json:"round"`
	ActiveTask    *ApprovalReadTask        `json:"active_task"`
	TaskCount     int                      `json:"task_count"`
	DecisionCount int                      `json:"decision_count"`
}

// ApprovalRoundHistoryPage is one stable round-number keyset page.
type ApprovalRoundHistoryPage struct {
	Items      []ApprovalRoundHistoryItem `json:"items"`
	NextCursor *string                    `json:"next_cursor"`
	HasMore    bool                       `json:"has_more"`
}

type approvalInboxCursor struct {
	Version  int       `json:"v"`
	TenantID string    `json:"t"`
	ActorID  string    `json:"a"`
	View     string    `json:"w"`
	Key      time.Time `json:"k"`
	ID       string    `json:"i"`
}

type approvalRoundCursor struct {
	Version      int    `json:"v"`
	TenantID     string `json:"t"`
	ActorID      string `json:"a"`
	ContractID   string `json:"c"`
	LastRoundNum int    `json:"r"`
}

type approvalRoundRead struct {
	TenantID                string
	ContractID              string
	ApprovalRoundID         string
	ContractRevisionID      string
	RoundNumber             int
	State                   string
	WorkflowTemplateID      string
	WorkflowTemplateVersion string
	RoutingSnapshot         []byte
	RiskDispositionSnapshot []byte
	RowVersion              int64
	InitiatedBy             string
	StartedAt               time.Time
	EndedAt                 *time.Time
	PreviousRoundID         *string
}

func (round approvalRoundRead) summary(tenantID string) ApprovalRoundReadSummary {
	return ApprovalRoundReadSummary{
		ApprovalRoundID: round.ApprovalRoundID, RoundNumber: round.RoundNumber,
		ContractRevisionID: round.ContractRevisionID, State: round.State,
		WorkflowTemplateID: round.WorkflowTemplateID, WorkflowTemplateVersion: round.WorkflowTemplateVersion,
		InitiatedBy: round.InitiatedBy, InitiatedByDisplay: approvalActorDisplay(tenantID, round.InitiatedBy),
		StartedAt: round.StartedAt, EndedAt: round.EndedAt,
		PreviousRoundID: round.PreviousRoundID, RowVersion: round.RowVersion,
	}
}

type approvalReadTaskRow struct {
	TaskID            string
	NodeOrder         int
	NodeCode          string
	RoleCode          string
	AssigneeSubjectID string
	State             string
	RowVersion        int64
	ActivatedAt       *time.Time
	DecidedAt         *time.Time
	AssignedToCurrent bool
}

func (task approvalReadTaskRow) projection(tenantID string) ApprovalReadTask {
	return ApprovalReadTask{
		TaskID: task.TaskID, NodeOrder: task.NodeOrder, NodeCode: task.NodeCode,
		RoleCode: task.RoleCode, AssigneeSubjectID: task.AssigneeSubjectID,
		AssigneeDisplay: approvalActorDisplay(tenantID, task.AssigneeSubjectID),
		State:           task.State, RowVersion: task.RowVersion,
		ActivatedAt: task.ActivatedAt, DecidedAt: task.DecidedAt,
	}
}

type approvalReadDecisionRow struct {
	DecisionID     string
	TaskID         string
	NodeOrder      int
	NodeCode       string
	Action         string
	ActorSubjectID string
	Opinion        string
	DecidedAt      time.Time
}

func (decision approvalReadDecisionRow) projection(tenantID string) ApprovalReadDecision {
	return ApprovalReadDecision{
		DecisionID: decision.DecisionID, TaskID: decision.TaskID,
		NodeOrder: decision.NodeOrder, NodeCode: decision.NodeCode,
		Action: decision.Action, ActorSubjectID: decision.ActorSubjectID,
		ActorDisplay: approvalActorDisplay(tenantID, decision.ActorSubjectID),
		Opinion:      decision.Opinion, DecidedAt: decision.DecidedAt,
	}
}

type approvalInboxRow struct {
	record   clmContractRecord
	round    approvalRoundRead
	content  []byte
	task     *approvalReadTaskRow
	decision *approvalReadDecisionRow
	key      time.Time
	id       string
}

func normalizeApprovalInboxView(view string) (string, error) {
	switch strings.TrimSpace(view) {
	case "", approvalInboxViewTodo:
		return approvalInboxViewTodo, nil
	case approvalInboxViewDone:
		return approvalInboxViewDone, nil
	case approvalInboxViewInitiated:
		return approvalInboxViewInitiated, nil
	default:
		return "", fault.Invalid("view 必须是 todo、done 或 initiated")
	}
}

func approvalInboxLimit(limit int) (int, error) {
	if limit == 0 {
		return approvalInboxDefaultLimit, nil
	}
	if limit < 1 || limit > approvalInboxMaxLimit {
		return 0, fault.Invalid("limit 必须在 1 到 100 之间")
	}
	return limit, nil
}

func approvalHistoryLimit(limit int) (int, error) {
	if limit == 0 {
		return approvalHistoryDefaultLimit, nil
	}
	if limit < 1 || limit > approvalHistoryMaxLimit {
		return 0, fault.Invalid("limit 必须在 1 到 50 之间")
	}
	return limit, nil
}

func encodeApprovalInboxCursor(current accounts.User, view string, key time.Time, id string) string {
	raw, _ := json.Marshal(approvalInboxCursor{
		Version: approvalInboxCursorVersion, TenantID: accounts.Tenant(current), ActorID: current.ID,
		View: view, Key: key.UTC(), ID: id,
	})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeApprovalInboxCursor(value string, current accounts.User, view string) (*approvalInboxCursor, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > approvalInboxCursorMaxBytes {
		return nil, fault.Invalid("待办分页游标无效")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	var cursor approvalInboxCursor
	if err != nil || json.Unmarshal(raw, &cursor) != nil ||
		cursor.Version != approvalInboxCursorVersion || cursor.TenantID != accounts.Tenant(current) ||
		cursor.ActorID != current.ID || cursor.View != view || cursor.Key.IsZero() ||
		!validContractUUID(cursor.ID) {
		return nil, fault.Invalid("待办分页游标无效")
	}
	return &cursor, nil
}

func encodeApprovalRoundCursor(current accounts.User, contractID string, lastRound int) string {
	raw, _ := json.Marshal(approvalRoundCursor{
		Version: approvalRoundCursorVersion, TenantID: accounts.Tenant(current), ActorID: current.ID,
		ContractID: contractID, LastRoundNum: lastRound,
	})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeApprovalRoundCursor(value string, current accounts.User, contractID string) (*approvalRoundCursor, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > approvalRoundCursorMaxBytes {
		return nil, fault.Invalid("轮次分页游标无效")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	var cursor approvalRoundCursor
	if err != nil || json.Unmarshal(raw, &cursor) != nil ||
		cursor.Version != approvalRoundCursorVersion || cursor.TenantID != accounts.Tenant(current) ||
		cursor.ActorID != current.ID || cursor.ContractID != contractID || cursor.LastRoundNum < 1 {
		return nil, fault.Invalid("轮次分页游标无效")
	}
	return &cursor, nil
}

func approvalActorDisplay(tenantID, actorID string) string {
	if actorID == "" {
		return ""
	}
	if user, ok := accounts.ByID(actorID); ok && user.TenantID == tenantID {
		if name := strings.TrimSpace(user.DisplayName); name != "" {
			return name
		}
	}
	return actorID
}

func approvalContractName(content []byte) string {
	var payload struct {
		Contract *struct {
			Name string `json:"name"`
		} `json:"contract"`
	}
	if json.Unmarshal(content, &payload) == nil && payload.Contract != nil {
		if name := strings.TrimSpace(payload.Contract.Name); name != "" {
			return name
		}
	}
	return "未命名合同"
}

func approvalRiskDisposition(raw []byte) (string, string) {
	var payload struct {
		Status string `json:"status"`
		State  string `json:"state"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return "unknown", ""
	}
	// CLM-08 snapshots use status; the state key is accepted for snapshots
	// written by earlier drafts.
	state := strings.TrimSpace(payload.Status)
	if state == "" {
		state = strings.TrimSpace(payload.State)
	}
	if state == "" {
		return "unknown", ""
	}
	return state, payload.Reason
}

// approvalReadAllowedActions is only a UI hint. The decision command rechecks
// live assignment, role binding and separation of duties inside the write
// transaction.
func approvalReadAllowedActions(record clmContractRecord, round approvalRoundRead,
	task *approvalReadTaskRow, current accounts.User) []string {
	actions := []string{}
	if task == nil || task.State != "active" || round.State != "in_review" ||
		record.AuthorityMode != "native" || record.ValidityState != "active" {
		return actions
	}
	if !authorizeContract(record, current, access.Decide) {
		return actions
	}
	if task.AssigneeSubjectID != current.ID && !task.AssignedToCurrent {
		return actions
	}
	if task.NodeOrder <= approvalIndependentNodeLimit && round.InitiatedBy == current.ID {
		return actions
	}
	return []string{"approve", "return"}
}

func approvalTextPtr(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}

func approvalInt2Ptr(value pgtype.Int2) *int {
	if !value.Valid {
		return nil
	}
	number := int(value.Int16)
	return &number
}

func approvalInt8Ptr(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	number := value.Int64
	return &number
}

func approvalTimePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	at := value.Time.UTC()
	return &at
}

func approvalInboxItemFrom(view string, row approvalInboxRow, current accounts.User) ApprovalInboxItem {
	item := ApprovalInboxItem{
		View: view, ContractID: row.record.ContractID,
		ContractName: approvalContractName(row.content), ContractNumber: row.record.ContractNumber,
		ContractState: row.record.LifecycleState, ContractRevisionID: row.round.ContractRevisionID,
		ApprovalRoundID: row.round.ApprovalRoundID, RoundNumber: row.round.RoundNumber,
		RoundState: row.round.State, RoundStartedAt: row.round.StartedAt, RoundEndedAt: row.round.EndedAt,
		AllowedActions: []string{},
	}
	if row.task != nil {
		item.TaskID = &row.task.TaskID
		item.NodeOrder = &row.task.NodeOrder
		item.NodeCode = &row.task.NodeCode
		item.RoleCode = &row.task.RoleCode
		item.AssigneeSubjectID = &row.task.AssigneeSubjectID
		display := approvalActorDisplay(row.record.TenantID, row.task.AssigneeSubjectID)
		item.AssigneeDisplay = &display
		item.TaskState = &row.task.State
		item.TaskRowVersion = &row.task.RowVersion
		item.TaskActivatedAt = row.task.ActivatedAt
		item.TaskDecidedAt = row.task.DecidedAt
	}
	if row.decision != nil {
		item.DecisionID = &row.decision.DecisionID
		item.DecisionAction = &row.decision.Action
		item.DecisionActorSubjectID = &row.decision.ActorSubjectID
		display := approvalActorDisplay(row.record.TenantID, row.decision.ActorSubjectID)
		item.DecisionActorDisplay = &display
		item.DecisionOpinion = &row.decision.Opinion
		item.DecisionDecidedAt = &row.decision.DecidedAt
	}
	if view == approvalInboxViewTodo && row.task != nil {
		item.AllowedActions = approvalReadAllowedActions(row.record, row.round, row.task, current)
	}
	return item
}

const approvalReadContractColumns = `c.tenant_id,c.contract_id,c.owner_org_id,c.authority_mode,c.contract_number,
 c.source_system,c.external_contract_id,c.source_version,c.lifecycle_state,c.validity_state,
 c.current_revision_id,c.row_version,c.created_by,c.created_at,c.updated_at`

// approvalReadPreviousRoundColumn derives lineage from round_number instead of
// storing a second copy of the same fact. Rounds are numbered per contract and
// never renumbered, so the immediately lower number is the previous round.
const approvalReadPreviousRoundColumn = `(SELECT p.approval_round_id FROM clm_approval_rounds p
 WHERE p.tenant_id=r.tenant_id AND p.contract_id=r.contract_id AND p.round_number<r.round_number
 ORDER BY p.round_number DESC LIMIT 1)`

// approvalReadAssigneeColumn projects the frozen routing snapshot's first
// assignee. A node may have several eligible assignees; the decision command
// still authorizes the live actor against the full assignment table.
const approvalReadAssigneeColumn = `COALESCE((SELECT min(a.user_id) FROM clm_approval_task_assignments a
 WHERE a.tenant_id=t.tenant_id AND a.contract_id=t.contract_id
   AND a.approval_round_id=t.approval_round_id AND a.approval_task_id=t.approval_task_id),'')`

const approvalReadRoundColumns = `r.approval_round_id,r.contract_revision_id,r.round_number,r.state,
 r.workflow_template_id,r.workflow_template_version,r.routing_snapshot,r.risk_disposition_snapshot,
 r.row_version,r.initiated_by,r.started_at,r.ended_at,` + approvalReadPreviousRoundColumn

const approvalReadTaskColumns = `t.approval_task_id::text,t.node_order,t.node_code,t.role_code,` +
	approvalReadAssigneeColumn + `,
 t.state,t.row_version,t.activated_at,t.decided_at,t.created_at`

const approvalReadDecisionColumns = `d.decision_id::text,d.decision,d.decided_by,d.comment,d.decided_at,d.approval_task_id::text`

func scanApprovalInboxRows(rows pgx.Rows) ([]approvalInboxRow, error) {
	items := []approvalInboxRow{}
	for rows.Next() {
		var item approvalInboxRow
		var contractNumber, sourceSystem, externalID, sourceVersion, currentRevision pgtype.Text
		var roundEnded, taskActivated, taskDecided, taskCreated pgtype.Timestamptz
		var previousRound pgtype.Text
		var taskID, taskCode, taskRole, taskAssignee, taskState pgtype.Text
		var taskOrder pgtype.Int2
		var taskRowVersion pgtype.Int8
		var decisionID, decisionAction, decisionActor, decisionOpinion, decisionTaskID pgtype.Text
		var decisionAt pgtype.Timestamptz
		if err := rows.Scan(
			&item.record.TenantID, &item.record.ContractID, &item.record.OwnerOrgID,
			&item.record.AuthorityMode, &contractNumber, &sourceSystem, &externalID, &sourceVersion,
			&item.record.LifecycleState, &item.record.ValidityState, &currentRevision,
			&item.record.RowVersion, &item.record.CreatedBy, &item.record.CreatedAt, &item.record.UpdatedAt,
			&item.round.ApprovalRoundID, &item.round.ContractRevisionID, &item.round.RoundNumber,
			&item.round.State, &item.round.WorkflowTemplateID, &item.round.WorkflowTemplateVersion,
			&item.round.RoutingSnapshot, &item.round.RiskDispositionSnapshot, &item.round.RowVersion,
			&item.round.InitiatedBy, &item.round.StartedAt, &roundEnded, &previousRound,
			&item.content,
			&taskID, &taskOrder, &taskCode, &taskRole, &taskAssignee, &taskState,
			&taskRowVersion, &taskActivated, &taskDecided, &taskCreated,
			&decisionID, &decisionAction, &decisionActor, &decisionOpinion, &decisionAt, &decisionTaskID,
		); err != nil {
			return nil, err
		}
		item.record.ContractNumber = approvalTextPtr(contractNumber)
		item.record.SourceSystem = approvalTextPtr(sourceSystem)
		item.record.ExternalContractID = approvalTextPtr(externalID)
		item.record.SourceVersion = approvalTextPtr(sourceVersion)
		item.record.CurrentRevisionID = approvalTextPtr(currentRevision)
		item.round.TenantID = item.record.TenantID
		item.round.ContractID = item.record.ContractID
		item.round.EndedAt = approvalTimePtr(roundEnded)
		item.round.PreviousRoundID = approvalTextPtr(previousRound)
		if taskID.Valid && taskID.String != "" {
			item.task = &approvalReadTaskRow{
				TaskID: taskID.String, NodeOrder: int(taskOrder.Int16), NodeCode: taskCode.String,
				RoleCode: taskRole.String, AssigneeSubjectID: taskAssignee.String,
				State: taskState.String, RowVersion: taskRowVersion.Int64,
				ActivatedAt: approvalTimePtr(taskActivated), DecidedAt: approvalTimePtr(taskDecided),
			}
			_ = taskCreated
		}
		if decisionID.Valid && decisionID.String != "" {
			item.decision = &approvalReadDecisionRow{
				DecisionID: decisionID.String, TaskID: decisionTaskID.String,
				NodeOrder: int(taskOrder.Int16), NodeCode: taskCode.String,
				Action: decisionAction.String, ActorSubjectID: decisionActor.String,
				Opinion: decisionOpinion.String, DecidedAt: decisionAt.Time.UTC(),
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func queryApprovalInboxRows(ctx context.Context, tx pgx.Tx, tenantID, actorID, view string,
	hasAfter bool, afterKey time.Time, afterID string, limit int) ([]approvalInboxRow, error) {
	var query strings.Builder
	args := []any{tenantID, actorID}
	switch view {
	case approvalInboxViewTodo:
		query.WriteString(`SELECT ` + approvalReadContractColumns + `,` + approvalReadRoundColumns + `,rev.snapshot,` +
			approvalReadTaskColumns + `,NULL::text,NULL::text,NULL::text,NULL::text,NULL::timestamptz,NULL::text
		 FROM clm_approval_tasks t
		 JOIN clm_approval_task_assignments a ON a.tenant_id=t.tenant_id AND a.contract_id=t.contract_id
		   AND a.approval_round_id=t.approval_round_id AND a.approval_task_id=t.approval_task_id
		   AND a.user_id=$2
		 JOIN clm_approval_rounds r ON r.tenant_id=t.tenant_id AND r.contract_id=t.contract_id
		   AND r.approval_round_id=t.approval_round_id
		 JOIN clm_contracts c ON c.tenant_id=t.tenant_id AND c.contract_id=t.contract_id
		 LEFT JOIN clm_contract_revisions rev ON rev.tenant_id=c.tenant_id AND rev.contract_id=c.contract_id
		   AND rev.contract_revision_id=r.contract_revision_id
		 WHERE c.tenant_id=$1 AND t.state='active' AND r.state='in_review'`)
		if hasAfter {
			args = append(args, afterKey.UTC(), afterID)
			query.WriteString(` AND (t.activated_at,t.approval_task_id) > ($3::timestamptz,$4::uuid)`)
		}
		args = append(args, limit)
		query.WriteString(` ORDER BY t.activated_at ASC,t.approval_task_id ASC LIMIT $` + itoa(len(args)))
	case approvalInboxViewDone:
		query.WriteString(`SELECT ` + approvalReadContractColumns + `,` + approvalReadRoundColumns + `,rev.snapshot,` +
			approvalReadTaskColumns + `,` + approvalReadDecisionColumns + `
		 FROM clm_approval_decisions d
		 JOIN clm_approval_tasks t ON t.tenant_id=d.tenant_id AND t.contract_id=d.contract_id
		   AND t.approval_round_id=d.approval_round_id AND t.approval_task_id=d.approval_task_id
		 JOIN clm_approval_rounds r ON r.tenant_id=d.tenant_id AND r.contract_id=d.contract_id
		   AND r.approval_round_id=d.approval_round_id
		 JOIN clm_contracts c ON c.tenant_id=d.tenant_id AND c.contract_id=d.contract_id
		 LEFT JOIN clm_contract_revisions rev ON rev.tenant_id=c.tenant_id AND rev.contract_id=c.contract_id
		   AND rev.contract_revision_id=r.contract_revision_id
		 WHERE c.tenant_id=$1 AND d.decided_by=$2`)
		if hasAfter {
			args = append(args, afterKey.UTC(), afterID)
			query.WriteString(` AND (d.decided_at,d.decision_id) < ($3::timestamptz,$4::uuid)`)
		}
		args = append(args, limit)
		query.WriteString(` ORDER BY d.decided_at DESC,d.decision_id DESC LIMIT $` + itoa(len(args)))
	case approvalInboxViewInitiated:
		query.WriteString(`SELECT ` + approvalReadContractColumns + `,` + approvalReadRoundColumns + `,rev.snapshot,
			COALESCE(t.approval_task_id::text,''),COALESCE(t.node_order,0)::smallint,COALESCE(t.node_code,''),
			COALESCE(t.role_code,''),COALESCE(t.assignee,''),COALESCE(t.state,''),
			COALESCE(t.row_version,0),t.activated_at,t.decided_at,t.created_at,
			NULL::text,NULL::text,NULL::text,NULL::text,NULL::timestamptz,NULL::text
		 FROM clm_approval_rounds r
		 JOIN clm_contracts c ON c.tenant_id=r.tenant_id AND c.contract_id=r.contract_id
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
		 LEFT JOIN clm_contract_revisions rev ON rev.tenant_id=c.tenant_id AND rev.contract_id=c.contract_id
		   AND rev.contract_revision_id=r.contract_revision_id
		 WHERE c.tenant_id=$1 AND r.initiated_by=$2`)
		if hasAfter {
			args = append(args, afterKey.UTC(), afterID)
			query.WriteString(` AND (r.started_at,r.approval_round_id) < ($3::timestamptz,$4::uuid)`)
		}
		args = append(args, limit)
		query.WriteString(` ORDER BY r.started_at DESC,r.approval_round_id DESC LIMIT $` + itoa(len(args)))
	default:
		return nil, fault.Invalid("view 必须是 todo、done 或 initiated")
	}
	rows, err := tx.Query(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := scanApprovalInboxRows(rows)
	if err != nil {
		return nil, err
	}
	if view == approvalInboxViewTodo {
		// The todo query joined this subject's own assignment, including nodes
		// whose frozen routing lists several eligible assignees.
		for index := range items {
			if items[index].task != nil {
				items[index].task.AssignedToCurrent = true
			}
		}
	}
	return items, nil
}

func approvalRowKey(view string, row approvalInboxRow) (time.Time, string) {
	switch view {
	case approvalInboxViewTodo:
		if row.task != nil && row.task.ActivatedAt != nil {
			return *row.task.ActivatedAt, row.task.TaskID
		}
	case approvalInboxViewDone:
		if row.decision != nil {
			return row.decision.DecidedAt, row.decision.DecisionID
		}
	case approvalInboxViewInitiated:
		return row.round.StartedAt, row.round.ApprovalRoundID
	}
	return time.Time{}, ""
}

func (s *Store) ListApprovalInbox(ctx context.Context, user accounts.User, query ApprovalInboxQuery) (ApprovalInboxPage, error) {
	view, err := normalizeApprovalInboxView(query.View)
	if err != nil {
		return ApprovalInboxPage{}, err
	}
	limit, err := approvalInboxLimit(query.Limit)
	if err != nil {
		return ApprovalInboxPage{}, err
	}
	tx, current, err := s.beginContractLedgerRead(ctx, user)
	if err != nil {
		return ApprovalInboxPage{}, err
	}
	defer rollbackCLM(tx)
	cursor, err := decodeApprovalInboxCursor(query.Cursor, current, view)
	if err != nil {
		return ApprovalInboxPage{}, err
	}
	page := ApprovalInboxPage{Items: []ApprovalInboxItem{}}
	var afterSet bool
	var afterKey time.Time
	var afterID string
	if cursor != nil {
		afterSet, afterKey, afterID = true, cursor.Key, cursor.ID
	}
	for {
		rows, err := queryApprovalInboxRows(ctx, tx, accounts.Tenant(current), current.ID, view,
			afterSet, afterKey, afterID, limit+1)
		if err != nil {
			return ApprovalInboxPage{}, err
		}
		foundExtra := false
		for _, row := range rows {
			key, id := approvalRowKey(view, row)
			afterSet, afterKey, afterID = true, key, id
			if key.IsZero() || id == "" || !authorizeContract(row.record, current, access.Read) {
				continue
			}
			if len(page.Items) == limit {
				foundExtra = true
				break
			}
			page.Items = append(page.Items, approvalInboxItemFrom(view, row, current))
		}
		if foundExtra {
			// Bind the cursor to the last scanned position, not the last visible
			// item. A page whose extra row is invisible must still resume after
			// that row instead of encoding a zero key.
			encoded := encodeApprovalInboxCursor(current, view, afterKey, afterID)
			page.NextCursor = &encoded
			page.HasMore = true
			break
		}
		if len(rows) < limit+1 {
			break
		}
	}
	if _, err := trustedUser(current); err != nil {
		return ApprovalInboxPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApprovalInboxPage{}, err
	}
	return page, nil
}

func loadApprovalReadRound(ctx context.Context, tx pgx.Tx, tenantID, contractID, roundID string) (approvalRoundRead, error) {
	var round approvalRoundRead
	var ended pgtype.Timestamptz
	var previous pgtype.Text
	err := tx.QueryRow(ctx, `SELECT `+approvalReadRoundColumns+` FROM clm_approval_rounds r
		WHERE r.tenant_id=$1 AND r.contract_id=$2 AND r.approval_round_id=$3`,
		tenantID, contractID, roundID).Scan(
		&round.ApprovalRoundID, &round.ContractRevisionID, &round.RoundNumber, &round.State,
		&round.WorkflowTemplateID, &round.WorkflowTemplateVersion, &round.RoutingSnapshot,
		&round.RiskDispositionSnapshot, &round.RowVersion, &round.InitiatedBy, &round.StartedAt,
		&ended, &previous,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return approvalRoundRead{}, fault.NotFound("没有找到对应的审批轮次")
	}
	if err != nil {
		return approvalRoundRead{}, err
	}
	round.TenantID, round.ContractID = tenantID, contractID
	round.EndedAt = approvalTimePtr(ended)
	round.PreviousRoundID = approvalTextPtr(previous)
	return round, nil
}

func loadApprovalReadTaskByID(ctx context.Context, tx pgx.Tx, tenantID, taskID string) (approvalReadTaskRow, string, string, error) {
	var task approvalReadTaskRow
	var contractID, roundID string
	var activated, decided, created pgtype.Timestamptz
	err := tx.QueryRow(ctx, `SELECT t.approval_task_id::text,t.node_order,t.node_code,t.role_code,
		`+approvalReadAssigneeColumn+`,t.state,t.row_version,t.activated_at,t.decided_at,t.created_at,
		t.contract_id::text,t.approval_round_id::text
		FROM clm_approval_tasks t WHERE t.tenant_id=$1 AND t.approval_task_id=$2`, tenantID, taskID).Scan(
		&task.TaskID, &task.NodeOrder, &task.NodeCode, &task.RoleCode, &task.AssigneeSubjectID,
		&task.State, &task.RowVersion, &activated, &decided, &created, &contractID, &roundID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return approvalReadTaskRow{}, "", "", fault.NotFound("没有找到对应的审批任务")
	}
	if err != nil {
		return approvalReadTaskRow{}, "", "", err
	}
	task.ActivatedAt, task.DecidedAt = approvalTimePtr(activated), approvalTimePtr(decided)
	_ = created
	return task, contractID, roundID, nil
}

func loadApprovalReadTasks(ctx context.Context, tx pgx.Tx, tenantID, contractID, roundID, actorID string) ([]approvalReadTaskRow, error) {
	rows, err := tx.Query(ctx, `SELECT t.approval_task_id::text,t.node_order,t.node_code,t.role_code,
		`+approvalReadAssigneeColumn+`,t.state,t.row_version,t.activated_at,t.decided_at,t.created_at,
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
	tasks := []approvalReadTaskRow{}
	for rows.Next() {
		var task approvalReadTaskRow
		var activated, decided, created pgtype.Timestamptz
		if err := rows.Scan(&task.TaskID, &task.NodeOrder, &task.NodeCode, &task.RoleCode,
			&task.AssigneeSubjectID, &task.State, &task.RowVersion, &activated, &decided, &created,
			&task.AssignedToCurrent); err != nil {
			return nil, err
		}
		task.ActivatedAt, task.DecidedAt = approvalTimePtr(activated), approvalTimePtr(decided)
		_ = created
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func loadApprovalReadDecisions(ctx context.Context, tx pgx.Tx, tenantID, contractID, roundID string) ([]approvalReadDecisionRow, error) {
	rows, err := tx.Query(ctx, `SELECT d.decision_id::text,d.approval_task_id::text,t.node_order,t.node_code,
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
	decisions := []approvalReadDecisionRow{}
	for rows.Next() {
		var decision approvalReadDecisionRow
		if err := rows.Scan(&decision.DecisionID, &decision.TaskID, &decision.NodeOrder,
			&decision.NodeCode, &decision.Action, &decision.ActorSubjectID, &decision.Opinion,
			&decision.DecidedAt); err != nil {
			return nil, err
		}
		decisions = append(decisions, decision)
	}
	return decisions, rows.Err()
}

func approvalReadActiveTask(tasks []approvalReadTaskRow) *approvalReadTaskRow {
	for index := range tasks {
		if tasks[index].State == "active" {
			return &tasks[index]
		}
	}
	return nil
}

func buildApprovalRoundReadDetail(record clmContractRecord, round approvalRoundRead,
	tasks []approvalReadTaskRow, decisions []approvalReadDecisionRow, previous *ApprovalRoundReadSummary,
	content []byte, current accounts.User) ApprovalRoundReadDetail {
	active := approvalReadActiveTask(tasks)
	riskState, riskReason := approvalRiskDisposition(round.RiskDispositionSnapshot)
	detail := ApprovalRoundReadDetail{
		ContractID: record.ContractID, ContractName: approvalContractName(content),
		ContractNumber: record.ContractNumber, ContractState: record.LifecycleState,
		ContractRowVersion: record.RowVersion, CurrentRevisionID: record.CurrentRevisionID,
		Round: round.summary(record.TenantID), Tasks: make([]ApprovalReadTask, 0, len(tasks)),
		Decisions:            make([]ApprovalReadDecision, 0, len(decisions)),
		RiskDispositionState: riskState, RiskDispositionReason: riskReason,
		PreviousRound: previous, AllowedActions: []string{},
	}
	for _, task := range tasks {
		detail.Tasks = append(detail.Tasks, task.projection(record.TenantID))
	}
	for _, decision := range decisions {
		detail.Decisions = append(detail.Decisions, decision.projection(record.TenantID))
	}
	if active != nil {
		detail.ActiveTaskID = &active.TaskID
		detail.AllowedActions = approvalReadAllowedActions(record, round, active, current)
	}
	if previous != nil && previous.ContractRevisionID != round.ContractRevisionID {
		detail.RevisionChanged = true
	}
	return detail
}

// loadApprovalRoundReadDetail loads one round and its facts inside the caller's
// read transaction. The revision diff is deliberately computed after commit by
// the existing version comparison path.
func loadApprovalRoundReadDetail(ctx context.Context, tx pgx.Tx, record clmContractRecord,
	roundID string, current accounts.User) (ApprovalRoundReadDetail, approvalRoundRead, error) {
	round, err := loadApprovalReadRound(ctx, tx, record.TenantID, record.ContractID, roundID)
	if err != nil {
		return ApprovalRoundReadDetail{}, approvalRoundRead{}, err
	}
	tasks, err := loadApprovalReadTasks(ctx, tx, record.TenantID, record.ContractID, roundID, current.ID)
	if err != nil {
		return ApprovalRoundReadDetail{}, approvalRoundRead{}, err
	}
	decisions, err := loadApprovalReadDecisions(ctx, tx, record.TenantID, record.ContractID, roundID)
	if err != nil {
		return ApprovalRoundReadDetail{}, approvalRoundRead{}, err
	}
	var previous *ApprovalRoundReadSummary
	if round.PreviousRoundID != nil && *round.PreviousRoundID != "" {
		prior, err := loadApprovalReadRound(ctx, tx, record.TenantID, record.ContractID, *round.PreviousRoundID)
		if err != nil {
			return ApprovalRoundReadDetail{}, approvalRoundRead{}, err
		}
		summary := prior.summary(record.TenantID)
		previous = &summary
	}
	content, err := loadApprovalReadRevisionContent(ctx, tx, record.TenantID, record.ContractID, round.ContractRevisionID)
	if err != nil {
		return ApprovalRoundReadDetail{}, approvalRoundRead{}, err
	}
	detail := buildApprovalRoundReadDetail(record, round, tasks, decisions, previous, content, current)
	return detail, round, nil
}

func loadApprovalReadRevisionContent(ctx context.Context, tx pgx.Tx, tenantID, contractID, revisionID string) ([]byte, error) {
	var content []byte
	err := tx.QueryRow(ctx, `SELECT snapshot FROM clm_contract_revisions
		WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3`,
		tenantID, contractID, revisionID).Scan(&content)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fault.NotFound("没有找到对应的合同版本")
	}
	return content, err
}

func (s *Store) attachApprovalRevisionDiff(ctx context.Context, user accounts.User,
	record clmContractRecord, detail *ApprovalRoundReadDetail) error {
	if detail.PreviousRound == nil || !detail.RevisionChanged {
		return nil
	}
	diff, err := s.DiffContractVersions(ctx, user, record.ContractID,
		detail.PreviousRound.ContractRevisionID, detail.Round.ContractRevisionID)
	if err != nil {
		return err
	}
	detail.RevisionDiff = &diff
	return nil
}

// GetApprovalTask returns the selected task and its full round timeline. Any
// visible contract reader can inspect a historical task; action hints are
// limited to the live assignee.
func (s *Store) GetApprovalTask(ctx context.Context, user accounts.User, taskID string) (ApprovalTaskReadDetail, error) {
	id, err := normalizeContractUUID(taskID)
	if err != nil {
		return ApprovalTaskReadDetail{}, err
	}
	tx, current, err := s.beginContractLedgerRead(ctx, user)
	if err != nil {
		return ApprovalTaskReadDetail{}, err
	}
	defer rollbackCLM(tx)
	task, contractID, roundID, err := loadApprovalReadTaskByID(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ApprovalTaskReadDetail{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), contractID)
	if err != nil {
		return ApprovalTaskReadDetail{}, err
	}
	if !authorizeContract(record, current, access.Read) {
		return ApprovalTaskReadDetail{}, fault.NotFound("没有找到对应记录")
	}
	detail, _, err := loadApprovalRoundReadDetail(ctx, tx, record, roundID, current)
	if err != nil {
		return ApprovalTaskReadDetail{}, err
	}
	if detail.ActiveTaskID == nil || *detail.ActiveTaskID != task.TaskID {
		detail.AllowedActions = []string{}
	}
	if _, err := trustedUser(current); err != nil {
		return ApprovalTaskReadDetail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApprovalTaskReadDetail{}, err
	}
	if err := s.attachApprovalRevisionDiff(ctx, user, record, &detail); err != nil {
		return ApprovalTaskReadDetail{}, err
	}
	return ApprovalTaskReadDetail{SelectedTaskID: task.TaskID, RoundDetail: detail}, nil
}

// GetContractApprovalRound returns one visible contract's historical round.
func (s *Store) GetContractApprovalRound(ctx context.Context, user accounts.User, contractID, roundID string) (ApprovalRoundReadDetail, error) {
	contract, err := normalizeContractUUID(contractID)
	if err != nil {
		return ApprovalRoundReadDetail{}, err
	}
	round, err := normalizeContractUUID(roundID)
	if err != nil {
		return ApprovalRoundReadDetail{}, err
	}
	tx, current, err := s.beginContractLedgerRead(ctx, user)
	if err != nil {
		return ApprovalRoundReadDetail{}, err
	}
	defer rollbackCLM(tx)
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), contract)
	if err != nil {
		return ApprovalRoundReadDetail{}, err
	}
	if !authorizeContract(record, current, access.Read) {
		return ApprovalRoundReadDetail{}, fault.NotFound("没有找到对应记录")
	}
	detail, _, err := loadApprovalRoundReadDetail(ctx, tx, record, round, current)
	if err != nil {
		return ApprovalRoundReadDetail{}, err
	}
	if _, err := trustedUser(current); err != nil {
		return ApprovalRoundReadDetail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApprovalRoundReadDetail{}, err
	}
	if err := s.attachApprovalRevisionDiff(ctx, user, record, &detail); err != nil {
		return ApprovalRoundReadDetail{}, err
	}
	return detail, nil
}

// ListContractApprovalRounds returns all rounds of one visible contract in
// descending round-number order. New rounds are inserted above the cursor, so
// walking older pages never duplicates or skips a stored round.
func (s *Store) ListContractApprovalRounds(ctx context.Context, user accounts.User, contractID, cursor string, limit int) (ApprovalRoundHistoryPage, error) {
	contract, err := normalizeContractUUID(contractID)
	if err != nil {
		return ApprovalRoundHistoryPage{}, err
	}
	limit, err = approvalHistoryLimit(limit)
	if err != nil {
		return ApprovalRoundHistoryPage{}, err
	}
	tx, current, err := s.beginContractLedgerRead(ctx, user)
	if err != nil {
		return ApprovalRoundHistoryPage{}, err
	}
	defer rollbackCLM(tx)
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), contract)
	if err != nil {
		return ApprovalRoundHistoryPage{}, err
	}
	if !authorizeContract(record, current, access.Read) {
		return ApprovalRoundHistoryPage{}, fault.NotFound("没有找到对应记录")
	}
	before, err := decodeApprovalRoundCursor(cursor, current, contract)
	if err != nil {
		return ApprovalRoundHistoryPage{}, err
	}
	args := []any{accounts.Tenant(current), contract}
	where := `r.tenant_id=$1 AND r.contract_id=$2`
	if before != nil {
		args = append(args, before.LastRoundNum)
		where += ` AND r.round_number < $3`
	}
	args = append(args, limit+1)
	rows, err := tx.Query(ctx, `SELECT `+approvalReadRoundColumns+`,
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
		WHERE `+where+` ORDER BY r.round_number DESC LIMIT $`+itoa(len(args)), args...)
	if err != nil {
		return ApprovalRoundHistoryPage{}, err
	}
	defer rows.Close()
	page := ApprovalRoundHistoryPage{Items: []ApprovalRoundHistoryItem{}}
	type pendingRound struct {
		round         approvalRoundRead
		taskCount     int
		decisionCount int
		active        *approvalReadTaskRow
	}
	pending := []pendingRound{}
	for rows.Next() {
		var item pendingRound
		var ended, activated, decided pgtype.Timestamptz
		var previous pgtype.Text
		var taskID, taskCode, taskRole, taskAssignee, taskState pgtype.Text
		var taskOrder pgtype.Int2
		var taskRowVersion pgtype.Int8
		if err := rows.Scan(
			&item.round.ApprovalRoundID, &item.round.ContractRevisionID, &item.round.RoundNumber,
			&item.round.State, &item.round.WorkflowTemplateID, &item.round.WorkflowTemplateVersion,
			&item.round.RoutingSnapshot, &item.round.RiskDispositionSnapshot, &item.round.RowVersion,
			&item.round.InitiatedBy, &item.round.StartedAt, &ended, &previous,
			&item.taskCount, &item.decisionCount,
			&taskID, &taskOrder, &taskCode, &taskRole, &taskAssignee, &taskState,
			&taskRowVersion, &activated, &decided,
		); err != nil {
			return ApprovalRoundHistoryPage{}, err
		}
		item.round.TenantID, item.round.ContractID = accounts.Tenant(current), contract
		item.round.EndedAt = approvalTimePtr(ended)
		item.round.PreviousRoundID = approvalTextPtr(previous)
		if taskID.Valid && taskID.String != "" {
			item.active = &approvalReadTaskRow{
				TaskID: taskID.String, NodeOrder: int(taskOrder.Int16), NodeCode: taskCode.String,
				RoleCode: taskRole.String, AssigneeSubjectID: taskAssignee.String,
				State: taskState.String, RowVersion: taskRowVersion.Int64,
				ActivatedAt: approvalTimePtr(activated), DecidedAt: approvalTimePtr(decided),
			}
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		return ApprovalRoundHistoryPage{}, err
	}
	for index, item := range pending {
		if index == limit {
			encoded := encodeApprovalRoundCursor(current, contract, page.Items[len(page.Items)-1].Round.RoundNumber)
			page.NextCursor = &encoded
			page.HasMore = true
			break
		}
		page.Items = append(page.Items, ApprovalRoundHistoryItem{
			Round: item.round.summary(accounts.Tenant(current)),
			ActiveTask: func() *ApprovalReadTask {
				if item.active == nil {
					return nil
				}
				task := item.active.projection(accounts.Tenant(current))
				return &task
			}(),
			TaskCount: item.taskCount, DecisionCount: item.decisionCount,
		})
	}
	if _, err := trustedUser(current); err != nil {
		return ApprovalRoundHistoryPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApprovalRoundHistoryPage{}, err
	}
	return page, nil
}

// itoa keeps SQL placeholder construction local and avoids importing strconv
// into a file whose main concern is read authorization.
func itoa(value int) string {
	return strconv.Itoa(value)
}
