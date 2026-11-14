// File metadata (manifest mirror):
// Purpose: Adapts current Contract Read and Decide authorization to approval decision ports within the caller transaction.
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
