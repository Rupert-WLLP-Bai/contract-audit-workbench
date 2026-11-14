// File metadata (manifest mirror):
// Purpose: Freezing a draft appends one immutable clm_contract_revisions row. The draft stays editable so a later freeze can create the next version; frozen rows, their material summaries… Related `store` operations include FreezeContractVersion, loadContractDraftBytes, frozenMaterialsFromReferences, ListContractVersions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-12
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"

	"contractaudit/internal/accounts"
	contractapp "contractaudit/internal/contract/app"
	clmcontract "contractaudit/internal/contract/model"
	contractpostgres "contractaudit/internal/contract/postgres"
	"contractaudit/internal/fault"
	platformpostgres "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

const (
	clmRouteFreezeVersion   = contractapp.FreezeVersionRoute
	clmEventVersionFrozen   = contractapp.VersionFrozenEvent
	clmCommandFreezeVersion = "freeze_version"
)

type FreezeContractVersionInput = clmcontract.FreezeContractVersionInput
type ContractVersionSummary = clmcontract.ContractVersionSummary
type FreezeContractVersionResult = clmcontract.FreezeContractVersionResult
type ContractVersionList = clmcontract.ContractVersionList
type ContractVersionMaterial = clmcontract.ContractVersionMaterial
type ContractVersionDetail = clmcontract.ContractVersionDetail
type frozenVersionMaterial = clmcontract.FrozenVersionMaterial

// FreezeContractVersion keeps identity, write authorization, RLS scope,
// transaction lifetime and the original commit fence in Store. The application
// owns command ordering and Contract version rules.
func (s *Store) FreezeContractVersion(ctx context.Context, user accounts.User, input FreezeContractVersionInput) (FreezeContractVersionResult, error) {
	ctx = commandContext(ctx, user)
	prepared, err := contractapp.PrepareFreezeVersion(input)
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能冻结合同版本", nil)
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	defer rollbackCLM(tx)
	result, err := contractapp.FreezeVersion(ctx, contractapp.VersionPrincipal{
		TenantID: accounts.Tenant(current), ActorID: current.ID, OrgID: current.OrgID,
	}, prepared, contractpostgres.NewVersionCommands(platformpostgres.Bind(tx)), storeVersionCodec{},
		storeDraftAccess{tx: tx, user: current}, storeDraftWorkflowReader{tx: tx, user: current},
		storeVersionReferenceResolver{tx: tx, user: current}, storeVersionEventAdapter{tx: tx},
		storeVersionEventAdapter{tx: tx})
	if err != nil {
		return FreezeContractVersionResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FreezeContractVersionResult{}, err
	}
	return result, nil
}

// ListContractVersions returns the immutable ledger for one currently visible
// contract; current audit bindings remain an explicit read from Audit.
func (s *Store) ListContractVersions(ctx context.Context, user accounts.User, contractID string) (ContractVersionList, error) {
	ctx = commandContext(ctx, user)
	id, err := normalizeContractUUID(contractID)
	if err != nil {
		return ContractVersionList{}, err
	}
	if !s.HasDatabase() {
		return ContractVersionList{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return ContractVersionList{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ContractVersionList{}, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ContractVersionList{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ContractVersionList{}, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return ContractVersionList{}, fault.NotFound("没有找到对应记录")
	}
	if err := setLocalScope(ctx, tx, dbScope{ActorID: current.ID, TenantID: accounts.Tenant(current), OrgID: record.OwnerOrgID, Role: "api"}); err != nil {
		return ContractVersionList{}, err
	}
	result, err := contractapp.ListContractVersions(ctx, record,
		contractpostgres.NewVersionQueries(platformpostgres.Bind(tx)), storeVersionAuditReader{tx: tx},
		storeDraftAccess{tx: tx, user: current})
	if err != nil {
		return ContractVersionList{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractVersionList{}, err
	}
	return result, nil
}

// LoadContractVersion projects the immutable snapshot under the current
// contract permission, with restricted fields masked for non-managers.
func (s *Store) LoadContractVersion(ctx context.Context, user accounts.User, contractID, revisionID string) (ContractVersionDetail, error) {
	ctx = commandContext(ctx, user)
	id, err := normalizeContractUUID(contractID)
	if err != nil {
		return ContractVersionDetail{}, err
	}
	revision, err := normalizeContractUUID(revisionID)
	if err != nil {
		return ContractVersionDetail{}, err
	}
	if !s.HasDatabase() {
		return ContractVersionDetail{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return ContractVersionDetail{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ContractVersionDetail{}, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ContractVersionDetail{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ContractVersionDetail{}, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return ContractVersionDetail{}, fault.NotFound("没有找到对应记录")
	}
	if err := setLocalScope(ctx, tx, dbScope{ActorID: current.ID, TenantID: accounts.Tenant(current), OrgID: record.OwnerOrgID, Role: "api"}); err != nil {
		return ContractVersionDetail{}, err
	}
	result, err := contractapp.LoadContractVersion(ctx, record, revision,
		contractpostgres.NewVersionQueries(platformpostgres.Bind(tx)), storeVersionAuditReader{tx: tx},
		storeVersionCodec{}, storeDraftAccess{tx: tx, user: current})
	if err != nil {
		return ContractVersionDetail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractVersionDetail{}, err
	}
	return result, nil
}

// frozenContractVersion and seal helpers remain narrow compatibility bridges
// for Approval, Audit, Signing and immutable-version comparison readers.
type frozenContractVersion = clmcontract.FrozenContractRevision

func loadFrozenContractVersion(ctx context.Context, tx pgx.Tx, tenantID, contractID, revisionID string) (frozenContractVersion, error) {
	return contractpostgres.NewVersionQueries(platformpostgres.Bind(tx)).LoadFrozenRevision(ctx, tenantID, contractID, revisionID)
}

type contractVersionSeal struct {
	SnapshotDigest         string
	MaterialsDigest        string
	ContentFingerprint     string
	RestrictedFingerprints clmRestrictedFingerprintSet
}

func loadContractVersionSeal(ctx context.Context, tx pgx.Tx, tenantID, contractID, revisionID string) (*contractVersionSeal, error) {
	loaded, err := contractpostgres.NewVersionQueries(platformpostgres.Bind(tx)).LoadVersionSeal(ctx, tenantID, contractID, revisionID)
	if err != nil || loaded == nil {
		return nil, err
	}
	fingerprints, err := parseRestrictedFingerprints(loaded.RestrictedFingerprints)
	if err != nil {
		return nil, err
	}
	return &contractVersionSeal{
		SnapshotDigest: loaded.SnapshotDigest, MaterialsDigest: loaded.MaterialsDigest,
		ContentFingerprint: loaded.ContentFingerprint, RestrictedFingerprints: fingerprints,
	}, nil
}

func loadContractVersionMaterials(ctx context.Context, tx pgx.Tx, tenantID, contractID, revisionID string) ([]frozenVersionMaterial, error) {
	return contractpostgres.NewVersionQueries(platformpostgres.Bind(tx)).LoadVersionMaterials(ctx, tenantID, contractID, revisionID)
}

// These source-compatible helpers delegate to Contract model/projection and
// PostgreSQL adapters; source-observation and neighboring readers still use
// them while their own migrations proceed.
func frozenMaterialsFromReferences(anchor Case, refs []ContractMaterialReference) ([]frozenVersionMaterial, error) {
	resolved := make([]clmcontract.ResolvedVersionMaterial, 0, len(refs))
	for _, ref := range refs {
		resolved = append(resolved, clmcontract.ResolvedVersionMaterial{
			MaterialID: ref.MaterialID, MaterialVersionID: ref.MaterialVersionID,
			MaterialVersion: ref.MaterialVersion, Filename: ref.Filename,
			Digest: ref.SHA256, Purpose: ref.Purpose,
		})
	}
	return clmcontract.FrozenMaterialsFromReferences(anchor.OwnerOrgID, anchor.ID, resolved)
}

func loadContractDraftBytes(ctx context.Context, tx pgx.Tx, tenantID, contractID string) ([]byte, error) {
	return contractpostgres.NewVersionCommands(platformpostgres.Bind(tx)).LoadDraftContent(ctx, tenantID, contractID)
}
