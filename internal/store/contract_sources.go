// File metadata (manifest mirror):
// Purpose: 实现 `ContractSourceCaseScope`, `ContractSourceAuthority`, `ContractSourceObservationInput`, `ContractSourceObservationResult` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"encoding/json"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/clmsource"
	contractapp "contractaudit/internal/contract/app"
	clmcontract "contractaudit/internal/contract/model"
	contractpostgres "contractaudit/internal/contract/postgres"
	"contractaudit/internal/fault"
	platformpostgres "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

const clmEventExternalSourceObserved = contractapp.ExternalSourceObservedEvent

// Source observation types remain Store-compatible aliases while ownership
// moves to Contract model and app.
type ContractSourceCaseScope = clmcontract.ContractSourceCaseScope
type ContractSourceAuthority = clmcontract.ContractSourceAuthority
type ContractSourceObservationInput = clmcontract.ContractSourceObservationInput
type ContractSourceObservationResult = clmcontract.ContractSourceObservationResult

type ContractSourceCapability struct {
	ContractID       string `json:"contract_id"`
	SourceID         string `json:"source_id"`
	SourceKind       string `json:"source_kind"`
	AuthorityMode    string `json:"authority_mode"`
	ReadOnly         bool   `json:"read_only"`
	CanReadContract  bool   `json:"can_read_contract"`
	VersionOrdering  string `json:"version_ordering"`
	ConnectionStatus string `json:"connection_status"`
}

// RecordExternalContractObservation keeps trusted service identity, the Case
// row lock, scope, write gate, transaction fences and commit in Store. Contract
// app owns normalization, idempotency/version decisions and event orchestration.
func (s *Store) RecordExternalContractObservation(ctx context.Context, authority ContractSourceAuthority,
	input ContractSourceObservationInput) (ContractSourceObservationResult, error) {
	if s.pool == nil {
		return ContractSourceObservationResult{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	if err := s.checkCLMWriteAllowed("只读模式下不能同步外部合同来源"); err != nil {
		return ContractSourceObservationResult{}, err
	}
	prepared, err := contractapp.PrepareContractSourceObservation(authority, input)
	if err != nil {
		return ContractSourceObservationResult{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ContractSourceObservationResult{}, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "clm-source:" + authority.SourceID,
		TenantID: authority.TenantID, OrgID: input.CaseOrgID, Role: "service"}); err != nil {
		return ContractSourceObservationResult{}, err
	}
	result, err := contractapp.RecordExternalContractObservation(ctx, prepared,
		storeSourceObservationCaseAccess{tx: tx},
		contractpostgres.NewSourceObservations(platformpostgres.Bind(tx)),
		storeDraftCodec{}, storeSourceObservationRawCodec{}, storeVersionEventAdapter{tx: tx})
	if err != nil {
		return ContractSourceObservationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractSourceObservationResult{}, err
	}
	return result, nil
}

func checkExternalContractCaseRead(ctx context.Context, tx pgx.Tx, record clmContractRecord, current accounts.User) bool {
	if record.AuthorityMode != string(clmsource.AuthorityExternal) {
		return true
	}
	rows, err := tx.Query(ctx, `SELECT c.id,c.tenant_id,c.org_id,c.owner_id,c.member_ids,c.business_status,c.archived_at
		FROM clm_contract_cases cc JOIN cases c ON c.tenant_id=cc.tenant_id AND c.org_id=cc.case_org_id AND c.id=cc.case_id
		WHERE cc.tenant_id=$1 AND cc.contract_id=$2`, record.TenantID, record.ContractID)
	if err != nil {
		return false
	}
	defer rows.Close()
	var linked *Case
	for rows.Next() {
		item := new(Case)
		var members string
		if rows.Scan(&item.ID, &item.TenantID, &item.OwnerOrgID, &item.OwnerID, &members, &item.BusinessStatus, &item.ArchivedAt) != nil {
			return false
		}
		if json.Unmarshal([]byte(members), &item.MemberIDs) != nil {
			return false
		}
		if linked != nil {
			return false
		}
		linked = item
	}
	if rows.Err() != nil || linked == nil {
		return false
	}
	return authorizeCase(*linked, current, access.Read)
}
