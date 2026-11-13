package model

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"contractaudit/internal/accounts"
	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

const (
	approvalInboxDefaultLimit   = 25
	approvalInboxMaxLimit       = 100
	approvalInboxCursorVersion  = 1
	approvalInboxCursorMaxBytes = 2048
	approvalHistoryDefaultLimit = 20
	approvalHistoryMaxLimit     = 50
	approvalRoundCursorVersion  = 1
	approvalRoundCursorMaxBytes = 512
	approvalInboxViewTodo       = "todo"
	approvalInboxViewDone       = "done"
	approvalInboxViewInitiated  = "initiated"
)

type InboxCursor struct {
	Version  int       `json:"v"`
	TenantID string    `json:"t"`
	ActorID  string    `json:"a"`
	View     string    `json:"w"`
	Key      time.Time `json:"k"`
	ID       string    `json:"i"`
}

type RoundCursor struct {
	Version      int    `json:"v"`
	TenantID     string `json:"t"`
	ActorID      string `json:"a"`
	ContractID   string `json:"c"`
	LastRoundNum int    `json:"r"`
}

func NormalizeInboxView(view string) (string, error) {
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

func InboxLimit(limit int) (int, error) {
	if limit == 0 {
		return approvalInboxDefaultLimit, nil
	}
	if limit < 1 || limit > approvalInboxMaxLimit {
		return 0, fault.Invalid("limit 必须在 1 到 100 之间")
	}
	return limit, nil
}

func HistoryLimit(limit int) (int, error) {
	if limit == 0 {
		return approvalHistoryDefaultLimit, nil
	}
	if limit < 1 || limit > approvalHistoryMaxLimit {
		return 0, fault.Invalid("limit 必须在 1 到 50 之间")
	}
	return limit, nil
}

func EncodeInboxCursor(current accounts.User, view string, key time.Time, id string) string {
	raw, _ := json.Marshal(InboxCursor{
		Version: InboxCursorVersion, TenantID: accounts.Tenant(current), ActorID: current.ID,
		View: view, Key: key.UTC(), ID: id,
	})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func DecodeInboxCursor(value string, current accounts.User, view string) (*InboxCursor, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > InboxCursorMaxBytes {
		return nil, fault.Invalid("待办分页游标无效")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	var cursor InboxCursor
	if err != nil || json.Unmarshal(raw, &cursor) != nil ||
		cursor.Version != InboxCursorVersion || cursor.TenantID != accounts.Tenant(current) ||
		cursor.ActorID != current.ID || cursor.View != view || cursor.Key.IsZero() ||
		!contract.ValidContractUUID(cursor.ID) {
		return nil, fault.Invalid("待办分页游标无效")
	}
	return &cursor, nil
}

func EncodeRoundCursor(current accounts.User, contractID string, lastRound int) string {
	raw, _ := json.Marshal(RoundCursor{
		Version: RoundCursorVersion, TenantID: accounts.Tenant(current), ActorID: current.ID,
		ContractID: contractID, LastRoundNum: lastRound,
	})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func DecodeRoundCursor(value string, current accounts.User, contractID string) (*RoundCursor, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > RoundCursorMaxBytes {
		return nil, fault.Invalid("轮次分页游标无效")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	var cursor RoundCursor
	if err != nil || json.Unmarshal(raw, &cursor) != nil ||
		cursor.Version != RoundCursorVersion || cursor.TenantID != accounts.Tenant(current) ||
		cursor.ActorID != current.ID || cursor.ContractID != contractID || cursor.LastRoundNum < 1 {
		return nil, fault.Invalid("轮次分页游标无效")
	}
	return &cursor, nil
}

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

func ActorDisplay(tenantID, actorID string) string {
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

func ContractName(content []byte) string {
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

func RiskDisposition(raw []byte) (string, string) {
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

type RoundRead struct {
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

func (round RoundRead) Summary(tenantID string) ApprovalRoundReadSummary {
	return ApprovalRoundReadSummary{
		ApprovalRoundID: round.ApprovalRoundID, RoundNumber: round.RoundNumber,
		ContractRevisionID: round.ContractRevisionID, State: round.State,
		WorkflowTemplateID: round.WorkflowTemplateID, WorkflowTemplateVersion: round.WorkflowTemplateVersion,
		InitiatedBy: round.InitiatedBy, InitiatedByDisplay: ActorDisplay(tenantID, round.InitiatedBy),
		StartedAt: round.StartedAt, EndedAt: round.EndedAt,
		PreviousRoundID: round.PreviousRoundID, RowVersion: round.RowVersion,
	}
}

type ReadTaskRow struct {
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

func (task ReadTaskRow) Projection(tenantID string) ApprovalReadTask {
	return ApprovalReadTask{
		TaskID: task.TaskID, NodeOrder: task.NodeOrder, NodeCode: task.NodeCode,
		RoleCode: task.RoleCode, AssigneeSubjectID: task.AssigneeSubjectID,
		AssigneeDisplay: ActorDisplay(tenantID, task.AssigneeSubjectID),
		State:           task.State, RowVersion: task.RowVersion,
		ActivatedAt: task.ActivatedAt, DecidedAt: task.DecidedAt,
	}
}

type ReadDecisionRow struct {
	DecisionID     string
	TaskID         string
	NodeOrder      int
	NodeCode       string
	Action         string
	ActorSubjectID string
	Opinion        string
	DecidedAt      time.Time
}

func (decision ReadDecisionRow) Projection(tenantID string) ApprovalReadDecision {
	return ApprovalReadDecision{
		DecisionID: decision.DecisionID, TaskID: decision.TaskID,
		NodeOrder: decision.NodeOrder, NodeCode: decision.NodeCode,
		Action: decision.Action, ActorSubjectID: decision.ActorSubjectID,
		ActorDisplay: ActorDisplay(tenantID, decision.ActorSubjectID),
		Opinion:      decision.Opinion, DecidedAt: decision.DecidedAt,
	}
}

// MaterialKey identifies one frozen material explicitly granted to a task.
type MaterialKey struct{ RevisionID, MaterialID, MaterialVersionID string }

// InboxCandidate contains only Approval-owned facts; Contract facts are loaded separately.
type InboxCandidate struct {
	Round    RoundRead
	Task     *ReadTaskRow
	Decision *ReadDecisionRow
}

type HistoryCandidate struct {
	Round         RoundRead
	TaskCount     int
	DecisionCount int
	Active        *ReadTaskRow
}

// InboxKey preserves each view's historical keyset direction and tie-breaker.
func InboxKey(view string, row InboxCandidate) (time.Time, string) {
	switch view {
	case "todo":
		if row.Task != nil && row.Task.ActivatedAt != nil {
			return *row.Task.ActivatedAt, row.Task.TaskID
		}
	case "done":
		if row.Decision != nil {
			return row.Decision.DecidedAt, row.Decision.DecisionID
		}
	case "initiated":
		return row.Round.StartedAt, row.Round.ApprovalRoundID
	}
	return time.Time{}, ""
}

func AllowedReadActions(record contract.Aggregate, round RoundRead, task *ReadTaskRow, actorID string, canDecide bool) []string {
	actions := []string{}
	if task == nil || task.State != "active" || round.State != "in_review" || record.AuthorityMode != "native" || record.ValidityState != "active" || !canDecide {
		return actions
	}
	if task.AssigneeSubjectID != actorID && !task.AssignedToCurrent {
		return actions
	}
	if task.NodeOrder <= 6 && round.InitiatedBy == actorID {
		return actions
	}
	return []string{"approve", "return"}
}

func InboxProjection(view string, row InboxCandidate, record contract.Aggregate, content []byte, actorID string, canDecide bool) ApprovalInboxItem {
	item := ApprovalInboxItem{View: view, ContractID: record.ContractID, ContractName: ContractName(content), ContractNumber: record.ContractNumber, ContractState: record.LifecycleState, ContractRevisionID: row.Round.ContractRevisionID, ApprovalRoundID: row.Round.ApprovalRoundID, RoundNumber: row.Round.RoundNumber, RoundState: row.Round.State, RoundStartedAt: row.Round.StartedAt, RoundEndedAt: row.Round.EndedAt, AllowedActions: []string{}}
	if row.Task != nil {
		task := row.Task
		item.TaskID = &task.TaskID
		item.NodeOrder = &task.NodeOrder
		item.NodeCode = &task.NodeCode
		item.RoleCode = &task.RoleCode
		item.AssigneeSubjectID = &task.AssigneeSubjectID
		display := ActorDisplay(record.TenantID, task.AssigneeSubjectID)
		item.AssigneeDisplay = &display
		item.TaskState = &task.State
		item.TaskRowVersion = &task.RowVersion
		item.TaskActivatedAt = task.ActivatedAt
		item.TaskDecidedAt = task.DecidedAt
	}
	if row.Decision != nil {
		decision := row.Decision
		item.DecisionID = &decision.DecisionID
		item.DecisionAction = &decision.Action
		item.DecisionActorSubjectID = &decision.ActorSubjectID
		display := ActorDisplay(record.TenantID, decision.ActorSubjectID)
		item.DecisionActorDisplay = &display
		item.DecisionOpinion = &decision.Opinion
		item.DecisionDecidedAt = &decision.DecidedAt
	}
	if view == "todo" && row.Task != nil {
		item.AllowedActions = AllowedReadActions(record, row.Round, row.Task, actorID, canDecide)
	}
	return item
}

type RoundDetail struct {
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
	AllowedActions        []string                  `json:"allowed_actions"`
}

func BuildRoundDetail(record contract.Aggregate, round RoundRead, tasks []ReadTaskRow, decisions []ReadDecisionRow, previous *ApprovalRoundReadSummary, content []byte, actorID string, canDecide bool) RoundDetail {
	state, reason := RiskDisposition(round.RiskDispositionSnapshot)
	detail := RoundDetail{ContractID: record.ContractID, ContractName: ContractName(content), ContractNumber: record.ContractNumber, ContractState: record.LifecycleState, ContractRowVersion: record.RowVersion, CurrentRevisionID: record.CurrentRevisionID, Round: round.Summary(record.TenantID), Tasks: make([]ApprovalReadTask, 0, len(tasks)), Decisions: make([]ApprovalReadDecision, 0, len(decisions)), RiskDispositionState: state, RiskDispositionReason: reason, PreviousRound: previous, AllowedActions: []string{}}
	for _, task := range tasks {
		detail.Tasks = append(detail.Tasks, task.Projection(record.TenantID))
	}
	for _, decision := range decisions {
		detail.Decisions = append(detail.Decisions, decision.Projection(record.TenantID))
	}
	for i := range tasks {
		if tasks[i].State == "active" {
			detail.ActiveTaskID = &tasks[i].TaskID
			detail.AllowedActions = AllowedReadActions(record, round, &tasks[i], actorID, canDecide)
			break
		}
	}
	detail.RevisionChanged = previous != nil && previous.ContractRevisionID != round.ContractRevisionID
	return detail
}
