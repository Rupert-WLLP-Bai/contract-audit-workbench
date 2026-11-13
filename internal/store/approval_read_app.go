package store

import (
	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	approvalmodel "contractaudit/internal/approval/model"
	contract "contractaudit/internal/contract/model"
)

type approvalReadAccess struct{ user accounts.User }

func (a approvalReadAccess) CanRead(record contract.Aggregate) bool {
	return authorizeContract(record, a.user, access.Read)
}
func (a approvalReadAccess) CanDecide(record contract.Aggregate) bool {
	return authorizeContract(record, a.user, access.Decide)
}

// approvalRoundDetailEnvelope preserves the Store DTO's public direct fields.
func approvalRoundDetailEnvelope(detail approvalmodel.RoundDetail) ApprovalRoundReadDetail {
	return ApprovalRoundReadDetail{
		ContractID: detail.ContractID, ContractName: detail.ContractName, ContractNumber: detail.ContractNumber,
		ContractState: detail.ContractState, ContractRowVersion: detail.ContractRowVersion, CurrentRevisionID: detail.CurrentRevisionID,
		Round: detail.Round, Tasks: detail.Tasks, Decisions: detail.Decisions, ActiveTaskID: detail.ActiveTaskID,
		RiskDispositionState: detail.RiskDispositionState, RiskDispositionReason: detail.RiskDispositionReason,
		PreviousRound: detail.PreviousRound, RevisionChanged: detail.RevisionChanged, AllowedActions: detail.AllowedActions,
	}
}
