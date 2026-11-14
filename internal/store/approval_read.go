// File metadata (manifest mirror):
// Purpose: Contract approval read models for the workbench. They consume the round, task, assignment and decision tables owned by the sequential approval command (migrations 0023/0024).… Related `store` operations include summary, projection, normalizeApprovalInboxView, approvalInboxLimit.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-13
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	approvalapp "contractaudit/internal/approval/app"
	approvalmodel "contractaudit/internal/approval/model"
	approvalpostgres "contractaudit/internal/approval/postgres"
	contractpostgres "contractaudit/internal/contract/postgres"
	"contractaudit/internal/fault"
	platformpostgres "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

// Contract approval read models for the workbench. They consume the round,
// task, assignment and decision tables owned by the sequential approval
// command (migrations 0023/0024). Reading never mutates a round or a decision.
// Each contract row is re-authorized through the same contract policy as the
// write commands; an invisible contract is indistinguishable from a missing one.
type ApprovalInboxQuery = approvalmodel.ApprovalInboxQuery
type ApprovalInboxItem = approvalmodel.ApprovalInboxItem
type ApprovalInboxPage = approvalmodel.ApprovalInboxPage
type ApprovalRoundReadSummary = approvalmodel.ApprovalRoundReadSummary
type ApprovalReadTask = approvalmodel.ApprovalReadTask
type ApprovalReadDecision = approvalmodel.ApprovalReadDecision

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
// Materials is the intersection of the frozen revision materials and this
// task's submission-time whitelist; an empty list is the default-deny result,
// never a signal to fall back to all revision materials.
type ApprovalTaskReadDetail struct {
	SelectedTaskID string                    `json:"selected_task_id"`
	RoundDetail    ApprovalRoundReadDetail   `json:"round_detail"`
	Materials      []ContractVersionMaterial `json:"materials"`
}

type ApprovalRoundHistoryItem = approvalmodel.ApprovalRoundHistoryItem
type ApprovalRoundHistoryPage = approvalmodel.ApprovalRoundHistoryPage

type approvalRoundRead = approvalmodel.RoundRead
type approvalReadTaskRow = approvalmodel.ReadTaskRow

func normalizeApprovalInboxView(view string) (string, error) {
	return approvalmodel.NormalizeInboxView(view)
}
func approvalInboxLimit(limit int) (int, error)   { return approvalmodel.InboxLimit(limit) }
func approvalHistoryLimit(limit int) (int, error) { return approvalmodel.HistoryLimit(limit) }

// approvalReadAllowedActions is only a UI hint. The decision command rechecks
// live assignment, role binding and separation of duties inside the write
// transaction.
func approvalReadAllowedActions(record clmContractRecord, round approvalRoundRead, task *approvalReadTaskRow, current accounts.User) []string {
	return approvalmodel.AllowedReadActions(record, round, task, current.ID, authorizeContract(record, current, access.Decide))
}

func (s *Store) ListApprovalInbox(ctx context.Context, user accounts.User, query ApprovalInboxQuery) (ApprovalInboxPage, error) {
	ctx = commandContext(ctx, user)
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
	executor := platformpostgres.Bind(tx)
	page, err := approvalapp.ListInbox(ctx, current, view, limit, query.Cursor, approvalpostgres.NewWorkflowReader(executor), contractpostgres.NewApprovalCommands(executor), approvalReadAccess{user: current})
	if err != nil {
		return ApprovalInboxPage{}, err
	}
	if _, err := trustedUser(current); err != nil {
		return ApprovalInboxPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApprovalInboxPage{}, err
	}
	return page, nil
}

// loadApprovalRoundReadDetail leaves the separate version diff until after commit.
func loadApprovalRoundReadDetail(ctx context.Context, tx pgx.Tx, record clmContractRecord, roundID string, current accounts.User) (ApprovalRoundReadDetail, approvalRoundRead, error) {
	executor := platformpostgres.Bind(tx)
	detail, round, err := approvalapp.LoadRoundDetail(ctx, current, record, roundID, approvalpostgres.NewWorkflowReader(executor), contractpostgres.NewApproval(executor), approvalReadAccess{user: current})
	return approvalRoundDetailEnvelope(detail), round, err
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
	ctx = commandContext(ctx, user)
	id, err := normalizeContractUUID(taskID)
	if err != nil {
		return ApprovalTaskReadDetail{}, err
	}
	tx, current, err := s.beginContractLedgerRead(ctx, user)
	if err != nil {
		return ApprovalTaskReadDetail{}, err
	}
	defer rollbackCLM(tx)
	executor := platformpostgres.Bind(tx)
	result, err := approvalapp.LoadTaskDetail(ctx, current, id, approvalpostgres.NewWorkflowReader(executor), contractpostgres.NewApprovalCommands(executor), approvalReadAccess{user: current})
	if err != nil {
		return ApprovalTaskReadDetail{}, err
	}
	detail := approvalRoundDetailEnvelope(result.Round)
	if _, err := trustedUser(current); err != nil {
		return ApprovalTaskReadDetail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApprovalTaskReadDetail{}, err
	}
	if err := s.attachApprovalRevisionDiff(ctx, user, result.Contract, &detail); err != nil {
		return ApprovalTaskReadDetail{}, err
	}
	return ApprovalTaskReadDetail{SelectedTaskID: result.SelectedTaskID, RoundDetail: detail, Materials: result.Materials}, nil
}

// GetContractApprovalRound returns one visible contract's historical round.
func (s *Store) GetContractApprovalRound(ctx context.Context, user accounts.User, contractID, roundID string) (ApprovalRoundReadDetail, error) {
	ctx = commandContext(ctx, user)
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
	ctx = commandContext(ctx, user)
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
	page, err := approvalapp.ListHistory(ctx, current, contract, cursor, limit, approvalpostgres.NewWorkflowReader(platformpostgres.Bind(tx)))
	if err != nil {
		return ApprovalRoundHistoryPage{}, err
	}
	if _, err := trustedUser(current); err != nil {
		return ApprovalRoundHistoryPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApprovalRoundHistoryPage{}, err
	}
	return page, nil
}
