package store

import (
	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	contract "contractaudit/internal/contract/model"
)

type approvalCredentialAccess struct{ user accounts.User }

func (a approvalCredentialAccess) CanReadDirect(record contract.Aggregate) bool {
	return authorizeContract(record, a.user, access.Read)
}
func (a approvalCredentialAccess) CanDecide(record contract.Aggregate) bool {
	return authorizeContract(record, a.user, access.Decide)
}
