package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

const (
	toolCallVersion           = "frozen-evidence/v1"
	toolCallAttemptLimit      = 32
	toolCallTenantMinuteLimit = 120
	toolCallOutputLimit       = 64 << 10
)

// ToolCallRequest identifies one bounded read against an exact frozen attempt.
type ToolCallRequest struct {
	CaseID, RunID, AttemptID, CapabilityID, ToolVersion string
	Deadline                                            time.Time
}

// ReserveToolCall atomically checks the current authorization and shared PG
// budgets before recording one consumed tool invocation.
func (s *Store) ReserveToolCall(ctx context.Context, user accounts.User, in ToolCallRequest) (string, error) {
	ctx = commandContext(ctx, user)
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s == nil || s.pool == nil {
		return "", fault.New(503, "store_unavailable", "储存服务暂不可用")
	}
	if s.ReadOnly() {
		return "", fault.New(503, "read_only", "只读模式下不能预留工具调用")
	}
	if err := validateToolCallRequest(in, time.Now().UTC()); err != nil {
		return "", err
	}
	current, caseItem, err := s.currentToolCase(user, in.CaseID, in.RunID, in.AttemptID, false)
	if err != nil {
		return "", err
	}
	if caseItem.BusinessLine != "ENT" || caseItem.Scenario != "telecom_dedicated_line" || caseItem.ContractType != "service" {
		return "", fault.Invalid("工具能力不适用于此类案件")
	}

	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadWrite})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return "", err
	}
	if err := lockToolCallBudgets(ctx, tx, accounts.Tenant(current), in.CaseID, in.RunID, in.AttemptID); err != nil {
		return "", err
	}
	caseItem, err = lockToolAttempt(ctx, tx, current, in.CaseID, in.RunID, in.AttemptID, false)
	if err != nil {
		return "", err
	}
	if caseItem.BusinessLine != "ENT" || caseItem.Scenario != "telecom_dedicated_line" || caseItem.ContractType != "service" {
		return "", fault.Invalid("工具能力不适用于此类案件")
	}
	var databaseNow time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		return "", err
	}
	if err := validateToolCallRequest(in, databaseNow); err != nil {
		return "", err
	}

	var attemptCount, tenantMinuteCount int64
	if err := tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM tool_call_ledger
		 WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND run_id=$4 AND attempt_id=$5),
		(SELECT count(*) FROM tool_call_ledger
		 WHERE tenant_id=$1 AND reserved_at >= clock_timestamp() - interval '60 seconds')`,
		accounts.Tenant(current), caseItem.OwnerOrgID, in.CaseID, in.RunID, in.AttemptID).Scan(&attemptCount, &tenantMinuteCount); err != nil {
		return "", err
	}
	if attemptCount >= toolCallAttemptLimit || tenantMinuteCount >= toolCallTenantMinuteLimit {
		s.Telemetry().RecordQuotaRejection("tool_budget")
		return "", fault.New(429, "tool_quota_exceeded", "工具调用预算已用尽")
	}
	id, err := newID("tool_call_")
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tool_call_ledger
		(id, tenant_id, org_id, case_id, actor_id, run_id, attempt_id, capability_id, tool_version, deadline, state, status, output_bytes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'reserved','reserved',0)`,
		id, accounts.Tenant(current), caseItem.OwnerOrgID, in.CaseID, current.ID, in.RunID, in.AttemptID,
		in.CapabilityID, in.ToolVersion, in.Deadline); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

// FinishToolCall terminally updates a reservation once, after rechecking the
// caller's current case access and the same frozen run/attempt binding.
func (s *Store) FinishToolCall(ctx context.Context, user accounts.User, callID, status string, outputBytes int64) error {
	ctx = commandContext(ctx, user)
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.pool == nil {
		return fault.New(503, "store_unavailable", "储存服务暂不可用")
	}
	if !validToolBudgetID(callID) {
		return fault.Invalid("工具调用编号无效")
	}
	if status != "completed" && status != "failed" && status != "cancelled" {
		return fault.Invalid("工具调用终态无效")
	}
	if outputBytes < 0 || outputBytes > toolCallOutputLimit {
		return fault.New(413, "tool_output_too_large", "工具调用输出大小超过限制")
	}
	if (status == "completed" && outputBytes == 0) || (status != "completed" && outputBytes != 0) {
		return fault.Invalid("工具调用输出大小与终态不一致")
	}
	current, err := trustedUser(user)
	if err != nil {
		return err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadWrite})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return err
	}

	var tenantID, orgID, caseID, actorID, runID, attemptID, state, currentStatus string
	err = tx.QueryRow(ctx, `SELECT tenant_id, org_id, case_id, actor_id, run_id, attempt_id, state, status
		FROM tool_call_ledger WHERE id=$1 AND tenant_id=$2 AND actor_id=$3 FOR UPDATE`,
		callID, accounts.Tenant(current), current.ID).Scan(&tenantID, &orgID, &caseID, &actorID, &runID, &attemptID, &state, &currentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.NotFound("没有找到待完成的工具调用")
	}
	if err != nil {
		return err
	}
	if state != "reserved" || currentStatus != "reserved" {
		return fault.Conflict("工具调用已经完成")
	}
	caseItem, err := lockToolAttempt(ctx, tx, current, caseID, runID, attemptID, status != "completed")
	if err != nil {
		return err
	}
	if caseItem.TenantID != tenantID || caseItem.OwnerOrgID != orgID || actorID != current.ID {
		return fault.NotFound("没有找到待完成的工具调用")
	}
	tag, err := tx.Exec(ctx, `UPDATE tool_call_ledger
		SET state='finished', status=$4, output_bytes=$5, finished_at=clock_timestamp()
		WHERE id=$1 AND tenant_id=$2 AND actor_id=$3 AND state='reserved' AND status='reserved'`,
		callID, tenantID, actorID, status, outputBytes)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict("工具调用已经完成")
	}
	return tx.Commit(ctx)
}

func validateToolCallRequest(in ToolCallRequest, now time.Time) error {
	if !validToolBudgetID(in.CaseID) || !validToolBudgetID(in.RunID) || !validToolBudgetID(in.AttemptID) {
		return fault.Invalid("案件、审查和执行编号的长度或格式不符合要求")
	}
	switch in.CapabilityID {
	case "dedicated_line.minimum_bandwidth", "dedicated_line.monthly_availability", "payment.advance_ratio.ceiling":
	default:
		return fault.Invalid("工具能力不受支持")
	}
	if in.ToolVersion != toolCallVersion {
		return fault.Invalid("工具版本不受支持")
	}
	if in.Deadline.IsZero() || !in.Deadline.After(now) || in.Deadline.After(now.Add(60*time.Second)) {
		return fault.Invalid("工具调用期限必须在未来 60 秒内")
	}
	return nil
}

func validToolBudgetID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for i := range value {
		c := value[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == ':') {
			return false
		}
	}
	return true
}

func (s *Store) currentToolCase(user accounts.User, caseID, runID, attemptID string, allowCancelled bool) (accounts.User, Case, error) {
	current, err := trustedUser(user)
	if err != nil {
		return accounts.User{}, Case{}, err
	}
	if accounts.Tenant(current) == "" {
		return accounts.User{}, Case{}, fault.Unauthenticated("当前身份没有有效租户")
	}
	st, err := s.snapshotAs(current)
	if err != nil {
		return accounts.User{}, Case{}, err
	}
	caseItem, ok := st.caseByID(caseID)
	if !ok || !authorizeCase(caseItem, current, access.Read) {
		return accounts.User{}, Case{}, fault.NotFound("没有找到对应的审查执行记录")
	}
	run, ok := st.runByID(runID)
	if !ok || run.CaseID != caseID {
		return accounts.User{}, Case{}, fault.NotFound("没有找到对应的审查执行记录")
	}
	attempt, ok := st.attemptByID(attemptID)
	if !ok || attempt.RunID != runID || attempt.HistoryKind != "frozen" || attempt.InputHash == nil || *attempt.InputHash == "" {
		return accounts.User{}, Case{}, fault.NotFound("没有找到对应的审查执行记录")
	}
	input, ok := st.attemptInput(attemptID)
	if !ok || input.AttemptID != attemptID || input.RunID != runID || input.InputHash != *attempt.InputHash ||
		!frozenInputMatchesSnapshot(st.snapshotOf(runID), input.Documents) ||
		!attemptSelectionsMatchFrozen(st.parseSelectionsForAttempt(attemptID), input.Documents) {
		return accounts.User{}, Case{}, fault.Conflict("本次执行保存的记录编号不完整或不一致")
	}
	if !allowCancelled && (run.State == "cancelled" || attempt.State == "cancelled") {
		return accounts.User{}, Case{}, fault.Conflict("本次审查执行已取消")
	}
	workspace, err := s.WorkspaceAttempt(current, caseID, runID, attemptID)
	if err != nil {
		return accounts.User{}, Case{}, err
	}
	if workspace.Case.ID != caseID || workspace.Case.TenantID != accounts.Tenant(current) || workspace.Snapshot.RunID != runID ||
		workspace.Execution == nil || workspace.Execution.Attempt == nil || workspace.Execution.Attempt.ID != attemptID ||
		workspace.Execution.Attempt.RunID != runID || workspace.Execution.Attempt.HistoryKind != "frozen" ||
		workspace.Execution.Attempt.InputHash == nil || *workspace.Execution.Attempt.InputHash == "" {
		return accounts.User{}, Case{}, fault.Conflict("本次执行的记录编号在读取期间发生变化")
	}
	return current, caseItem, nil
}

func lockToolAttempt(ctx context.Context, tx pgx.Tx, user accounts.User, caseID, runID, attemptID string, allowCancelled bool) (Case, error) {
	current, err := trustedUser(user)
	if err != nil {
		return Case{}, err
	}
	var tenantID, orgID, caseRowID, ownerID, memberJSON, businessLine, scenario, businessStatus, contractType string
	var runCaseID, runState, attemptRunID, historyKind, attemptState string
	var attemptInputHash, frozenInputHash sql.NullString
	// No FOR UPDATE here: the tool role may read core and execution rows but not
	// lock them (row locking needs UPDATE privileges it deliberately does not
	// hold). The tool call ledger write below is CAS-guarded on the reservation
	// state, and the read-only tool boundary revalidates this same binding
	// before it returns evidence.
	err = tx.QueryRow(ctx, `SELECT c.tenant_id, c.org_id, c.id, c.owner_id, c.member_ids, c.business_line, c.scenario, c.business_status, c.contract_type,
		r.case_id, r.state, a.run_id, a.history_kind, a.state, a.input_hash, i.input_hash
		FROM cases AS c
		JOIN review_runs AS r ON r.tenant_id=c.tenant_id AND r.org_id=c.org_id AND r.case_id=c.id
		JOIN review_attempts AS a ON a.tenant_id=r.tenant_id AND a.org_id=r.org_id AND a.run_id=r.id
		JOIN review_attempt_inputs AS i ON i.tenant_id=a.tenant_id AND i.org_id=a.org_id AND i.attempt_id=a.id AND i.run_id=r.id
		WHERE c.tenant_id=$1 AND c.id=$2 AND r.id=$3 AND a.id=$4`, accounts.Tenant(current), caseID, runID, attemptID).
		Scan(&tenantID, &orgID, &caseRowID, &ownerID, &memberJSON, &businessLine, &scenario, &businessStatus, &contractType,
			&runCaseID, &runState, &attemptRunID, &historyKind, &attemptState, &attemptInputHash, &frozenInputHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Case{}, fault.NotFound("没有找到对应的审查执行记录")
	}
	if err != nil {
		return Case{}, err
	}
	// The case must carry its real membership grants: authorizeCase binds a
	// legacy local-demo case-scope grant from MemberIDs, and an incomplete case
	// would deny a reviewer who does hold access.
	var memberIDs []string
	if err := json.Unmarshal([]byte(memberJSON), &memberIDs); err != nil {
		return Case{}, fault.NotFound("没有找到对应的审查执行记录")
	}
	caseItem := Case{
		ID: caseRowID, TenantID: tenantID, OwnerOrgID: orgID, OwnerID: ownerID, MemberIDs: memberIDs,
		BusinessLine: businessLine, Scenario: scenario, BusinessStatus: businessStatus, ContractType: contractType,
	}
	if tenantID != accounts.Tenant(current) || runCaseID != caseID || attemptRunID != runID ||
		historyKind != "frozen" || !attemptInputHash.Valid || attemptInputHash.String == "" ||
		!frozenInputHash.Valid || frozenInputHash.String != attemptInputHash.String || !authorizeCase(caseItem, current, access.Read) {
		return Case{}, fault.NotFound("没有找到对应的审查执行记录")
	}
	if !allowCancelled && (runState == "cancelled" || attemptState == "cancelled") {
		return Case{}, fault.Conflict("本次审查执行已取消")
	}
	return caseItem, nil
}

func lockToolCallBudgets(ctx context.Context, tx pgx.Tx, tenantID, caseID, runID, attemptID string) error {
	var tenantKey, attemptKey int64
	if err := tx.QueryRow(ctx, `SELECT
		hashtextextended(current_schema() || ':tool-call-tenant:' || $1, 0),
		hashtextextended(current_schema() || ':tool-call-attempt:' || jsonb_build_array($1::text,$2::text,$3::text,$4::text)::text, 0)`,
		tenantID, caseID, runID, attemptID).Scan(&tenantKey, &attemptKey); err != nil {
		return err
	}
	first, second := tenantKey, attemptKey
	if first > second {
		first, second = second, first
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, first); err != nil {
		return err
	}
	if second != first {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, second); err != nil {
			return err
		}
	}
	return nil
}
