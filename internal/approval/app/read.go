package app

import (
	"context"
	"errors"
	"time"

	"contractaudit/internal/accounts"
	model "contractaudit/internal/approval/model"
	contractapp "contractaudit/internal/contract/app"
	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

type InboxReader interface {
	LoadInboxCandidates(context.Context, string, string, string, bool, time.Time, string, int) ([]model.InboxCandidate, error)
}
type InboxContracts interface {
	LoadContract(context.Context, string, string) (contract.Aggregate, error)
	LoadApprovalSnapshotOptional(context.Context, string, string, string) ([]byte, error)
}
type ReadAccess interface {
	CanRead(contract.Aggregate) bool
	CanDecide(contract.Aggregate) bool
}

func ListInbox(ctx context.Context, current accounts.User, view string, limit int, cursor string, reader InboxReader, contracts InboxContracts, access ReadAccess) (model.ApprovalInboxPage, error) {
	before, err := model.DecodeInboxCursor(cursor, current, view)
	if err != nil {
		return model.ApprovalInboxPage{}, err
	}
	page := model.ApprovalInboxPage{Items: []model.ApprovalInboxItem{}}
	var afterSet bool
	var afterKey, lastKey time.Time
	var afterID, lastID string
	if before != nil {
		afterSet, afterKey, afterID = true, before.Key, before.ID
	}
	for {
		rows, err := reader.LoadInboxCandidates(ctx, accounts.Tenant(current), current.ID, view, afterSet, afterKey, afterID, limit+1)
		if err != nil {
			return model.ApprovalInboxPage{}, err
		}
		foundExtra := false
		for _, row := range rows {
			key, id := model.InboxKey(view, row)
			afterSet, afterKey, afterID = true, key, id
			record, err := contracts.LoadContract(ctx, accounts.Tenant(current), row.Round.ContractID)
			if err != nil {
				var f *fault.Error
				if errors.As(err, &f) && f.Status == 404 {
					continue
				}
				return model.ApprovalInboxPage{}, err
			}
			if key.IsZero() || id == "" || !access.CanRead(record) {
				continue
			}
			if len(page.Items) == limit {
				foundExtra = true
				break
			}
			content, err := contracts.LoadApprovalSnapshotOptional(ctx, accounts.Tenant(current), record.ContractID, row.Round.ContractRevisionID)
			if err != nil {
				return model.ApprovalInboxPage{}, err
			}
			page.Items = append(page.Items, model.InboxProjection(view, row, record, content, current.ID, access.CanDecide(record)))
			lastKey, lastID = key, id
		}
		if foundExtra {
			encoded := model.EncodeInboxCursor(current, view, lastKey, lastID)
			page.NextCursor = &encoded
			page.HasMore = true
			break
		}
		if len(rows) < limit+1 {
			break
		}
	}
	return page, nil
}

type HistoryReader interface {
	LoadHistory(context.Context, string, string, int, int) ([]model.HistoryCandidate, error)
}

func ListHistory(ctx context.Context, current accounts.User, contractID, cursor string, limit int, reader HistoryReader) (model.ApprovalRoundHistoryPage, error) {
	before, err := model.DecodeRoundCursor(cursor, current, contractID)
	if err != nil {
		return model.ApprovalRoundHistoryPage{}, err
	}
	beforeRound := 0
	if before != nil {
		beforeRound = before.LastRoundNum
	}
	pending, err := reader.LoadHistory(ctx, accounts.Tenant(current), contractID, beforeRound, limit+1)
	if err != nil {
		return model.ApprovalRoundHistoryPage{}, err
	}
	page := model.ApprovalRoundHistoryPage{Items: []model.ApprovalRoundHistoryItem{}}
	for index, item := range pending {
		if index == limit {
			encoded := model.EncodeRoundCursor(current, contractID, page.Items[len(page.Items)-1].Round.RoundNumber)
			page.NextCursor = &encoded
			page.HasMore = true
			break
		}
		var active *model.ApprovalReadTask
		if item.Active != nil {
			projected := item.Active.Projection(accounts.Tenant(current))
			active = &projected
		}
		page.Items = append(page.Items, model.ApprovalRoundHistoryItem{Round: item.Round.Summary(accounts.Tenant(current)), ActiveTask: active, TaskCount: item.TaskCount, DecisionCount: item.DecisionCount})
	}
	return page, nil
}

type DetailReader interface {
	LoadRound(context.Context, string, string, string) (model.RoundRead, error)
	LoadTasks(context.Context, string, string, string, string) ([]model.ReadTaskRow, error)
	LoadDecisions(context.Context, string, string, string) ([]model.ReadDecisionRow, error)
}
type RevisionReader interface {
	LoadApprovalRevisionSnapshot(context.Context, string, string, string) ([]byte, error)
}

func LoadRoundDetail(ctx context.Context, current accounts.User, record contract.Aggregate, roundID string, reader DetailReader, versions RevisionReader, access ReadAccess) (model.RoundDetail, model.RoundRead, error) {
	round, err := reader.LoadRound(ctx, record.TenantID, record.ContractID, roundID)
	if err != nil {
		return model.RoundDetail{}, model.RoundRead{}, err
	}
	tasks, err := reader.LoadTasks(ctx, record.TenantID, record.ContractID, roundID, current.ID)
	if err != nil {
		return model.RoundDetail{}, model.RoundRead{}, err
	}
	decisions, err := reader.LoadDecisions(ctx, record.TenantID, record.ContractID, roundID)
	if err != nil {
		return model.RoundDetail{}, model.RoundRead{}, err
	}
	var previous *model.ApprovalRoundReadSummary
	if round.PreviousRoundID != nil && *round.PreviousRoundID != "" {
		prior, err := reader.LoadRound(ctx, record.TenantID, record.ContractID, *round.PreviousRoundID)
		if err != nil {
			return model.RoundDetail{}, model.RoundRead{}, err
		}
		summary := prior.Summary(record.TenantID)
		previous = &summary
	}
	content, err := versions.LoadApprovalRevisionSnapshot(ctx, record.TenantID, record.ContractID, round.ContractRevisionID)
	if err != nil {
		return model.RoundDetail{}, model.RoundRead{}, err
	}
	return model.BuildRoundDetail(record, round, tasks, decisions, previous, content, current.ID, access.CanDecide(record)), round, nil
}

type MaterialWhitelistReader interface {
	LoadMaterialKeys(context.Context, string, string, string, string) ([]model.MaterialKey, error)
}
type FrozenMaterialsReader interface {
	LoadApprovalMaterials(context.Context, string, string, []contractapp.ApprovalMaterialKey) ([]contract.ContractVersionMaterial, error)
}

func LoadTaskMaterials(ctx context.Context, tenantID, contractID, roundID, taskID string, whitelist MaterialWhitelistReader, materials FrozenMaterialsReader) ([]contract.ContractVersionMaterial, error) {
	keys, err := whitelist.LoadMaterialKeys(ctx, tenantID, contractID, roundID, taskID)
	if err != nil {
		return nil, err
	}
	exact := make([]contractapp.ApprovalMaterialKey, 0, len(keys))
	for _, key := range keys {
		exact = append(exact, contractapp.ApprovalMaterialKey{RevisionID: key.RevisionID, MaterialID: key.MaterialID, MaterialVersionID: key.MaterialVersionID})
	}
	return materials.LoadApprovalMaterials(ctx, tenantID, contractID, exact)
}

type TaskReader interface {
	DetailReader
	MaterialWhitelistReader
	LoadTaskByID(context.Context, string, string) (model.ReadTaskRow, string, string, error)
}
type TaskContracts interface {
	RevisionReader
	FrozenMaterialsReader
	LoadContract(context.Context, string, string) (contract.Aggregate, error)
}
type TaskDetail struct {
	SelectedTaskID string
	Round          model.RoundDetail
	Materials      []contract.ContractVersionMaterial
	Contract       contract.Aggregate
}

func LoadTaskDetail(ctx context.Context, current accounts.User, taskID string, reader TaskReader, contracts TaskContracts, access ReadAccess) (TaskDetail, error) {
	task, contractID, roundID, err := reader.LoadTaskByID(ctx, accounts.Tenant(current), taskID)
	if err != nil {
		return TaskDetail{}, err
	}
	record, err := contracts.LoadContract(ctx, accounts.Tenant(current), contractID)
	if err != nil {
		return TaskDetail{}, err
	}
	if !access.CanRead(record) {
		return TaskDetail{}, fault.NotFound("没有找到对应记录")
	}
	detail, _, err := LoadRoundDetail(ctx, current, record, roundID, reader, contracts, access)
	if err != nil {
		return TaskDetail{}, err
	}
	if detail.ActiveTaskID == nil || *detail.ActiveTaskID != task.TaskID {
		detail.AllowedActions = []string{}
	}
	materials, err := LoadTaskMaterials(ctx, accounts.Tenant(current), contractID, roundID, task.TaskID, reader, contracts)
	if err != nil {
		return TaskDetail{}, err
	}
	return TaskDetail{SelectedTaskID: task.TaskID, Round: detail, Materials: materials, Contract: record}, nil
}
