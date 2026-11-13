package store

import (
	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	contract "contractaudit/internal/contract/model"
	"github.com/jackc/pgx/v5"
)

type approvalDecisionAccess struct {
	approvalSubmissionAccess
	user accounts.User
}

func (a approvalDecisionAccess) CanDecide(record contract.Aggregate) bool {
	return authorizeContract(record, a.user, access.Decide)
}
func decisionAccess(tx pgx.Tx, user accounts.User) approvalDecisionAccess {
	return approvalDecisionAccess{approvalSubmissionAccess: approvalSubmissionAccess{tx: tx, user: user}, user: user}
}
