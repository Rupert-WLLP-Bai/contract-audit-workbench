// File metadata (manifest mirror):
// Purpose: Adapts existing direct Contract Read and Decide checks to credential issuance ports.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

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
