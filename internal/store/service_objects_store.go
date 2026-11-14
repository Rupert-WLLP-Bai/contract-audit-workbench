// File metadata (manifest mirror):
// Purpose: Preserves service-object authorization, transactions and necessary frozen proof or compatibility adapters to the Operator owner.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-14
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	operatorApp "contractaudit/internal/operator/app"
	operatorModel "contractaudit/internal/operator/model"
	operatorPostgres "contractaudit/internal/operator/postgres"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

// CreateServiceObject creates only the pending identity head/version. The first
// contract association is a separate explicit confirmation against a frozen
// review attempt.
func (s *Store) CreateServiceObject(ctx context.Context, user accounts.User, caseID string, input ServiceObjectCreateInput, key string) (ServiceObjectMutationResult, bool, error) {
	ctx = commandContext(ctx, user)
	var empty ServiceObjectMutationResult
	caseID = strings.TrimSpace(caseID)
	if err := s.requireServiceObjectCoreWritable(ctx); err != nil {
		return empty, false, err
	}
	current, caseItem, tx, err := s.serviceObjectMutationTx(ctx, user, caseID)
	if err != nil {
		return empty, false, err
	}
	defer tx.Rollback(ctx)
	if err := validateIdempotencyKey(key); err != nil {
		return empty, false, err
	}
	repo := operatorPostgres.NewCreate(platformpostgres.Bind(tx))
	identity := operatorApp.CreateIdentity{TenantID: accounts.Tenant(current), OrgID: caseItem.OwnerOrgID, CaseID: caseID, ActorID: current.ID}
	prepared, result, replayed, err := operatorApp.PrepareCreate(ctx, repo, serviceObjectCreateClock{}, identity, input, key)
	if err != nil {
		return empty, false, err
	}
	if replayed {
		if err := tx.Commit(ctx); err != nil {
			return empty, false, err
		}
		return result, true, nil
	}
	if err := businessWritable(caseItem); err != nil {
		return empty, false, err
	}
	result, err = operatorApp.CreateAfterMiss(ctx, repo, serviceObjectCreateClock{}, prepared)
	if err != nil {
		return empty, false, err
	}
	if err := recheckServiceObjectManage(ctx, tx, current, caseItem); err != nil {
		return empty, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return empty, false, err
	}
	return result, false, nil
}

type serviceObjectCreateClock struct{}

func (serviceObjectCreateClock) Now() time.Time               { return time.Now() }
func (serviceObjectCreateClock) NewObjectID() (string, error) { return newID("svc_") }

func (s *Store) ListServiceObjects(ctx context.Context, user accounts.User, caseID, cursor string, limit int) (ServiceObjectListResult, error) {
	ctx = commandContext(ctx, user)
	caseID = strings.TrimSpace(caseID)
	limit, err := serviceObjectPageLimit(limit)
	if err != nil {
		return ServiceObjectListResult{}, err
	}
	lastID, err := decodeServiceObjectCursor(cursor, "objects", caseID, "")
	if err != nil {
		return ServiceObjectListResult{}, err
	}
	current, caseItem, tx, err := s.serviceObjectReadTx(ctx, user, caseID)
	if err != nil {
		return ServiceObjectListResult{}, err
	}
	defer tx.Rollback(ctx)
	st, err := loadState(ctx, tx, false)
	if err != nil {
		return ServiceObjectListResult{}, err
	}
	facts := serviceObjectMaterialSelectionFacts(st, caseID)
	repo := operatorPostgres.NewReads(platformpostgres.Bind(tx))
	result, err := operatorApp.ReadServiceObjectSummaries(ctx, repo, serviceObjectReadIdentity(current, caseItem), lastID, limit,
		serviceObjectCanManage(s, current, caseItem), facts)
	if err != nil {
		return ServiceObjectListResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceObjectListResult{}, err
	}
	return result, nil
}

func (s *Store) GetServiceObjectHistory(ctx context.Context, user accounts.User, caseID, objectID string) (ServiceObjectIdentityHistory, error) {
	ctx = commandContext(ctx, user)
	caseID = strings.TrimSpace(caseID)
	current, caseItem, tx, err := s.serviceObjectReadTx(ctx, user, caseID)
	if err != nil {
		return ServiceObjectIdentityHistory{}, err
	}
	defer tx.Rollback(ctx)
	repo := operatorPostgres.NewReads(platformpostgres.Bind(tx))
	identity := serviceObjectReadIdentity(current, caseItem)
	head, err := operatorApp.ReadServiceObjectHead(ctx, repo, identity, strings.TrimSpace(objectID))
	if err != nil {
		return ServiceObjectIdentityHistory{}, err
	}
	result, err := operatorApp.ReadServiceObjectHistory(ctx, repo, identity, head)
	if err != nil {
		return ServiceObjectIdentityHistory{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceObjectIdentityHistory{}, err
	}
	return result, nil
}

func (s *Store) GetServiceObject(ctx context.Context, user accounts.User, caseID, objectID string) (ServiceObjectDetail, error) {
	ctx = commandContext(ctx, user)
	caseID, objectID = strings.TrimSpace(caseID), strings.TrimSpace(objectID)
	current, caseItem, tx, err := s.serviceObjectReadTx(ctx, user, caseID)
	if err != nil {
		return ServiceObjectDetail{}, err
	}
	defer tx.Rollback(ctx)
	repo := operatorPostgres.NewReads(platformpostgres.Bind(tx))
	identity := serviceObjectReadIdentity(current, caseItem)
	head, err := operatorApp.ReadServiceObjectHead(ctx, repo, identity, objectID)
	if err != nil {
		return ServiceObjectDetail{}, err
	}
	version, err := operatorApp.ReadServiceObjectVersionExact(ctx, repo, identity, head.ID, head.CurrentVersion)
	if err != nil {
		return ServiceObjectDetail{}, err
	}
	var association *ServiceObjectContractAssociation
	if head.CurrentAssociationVersion > 0 {
		currentAssociation, err := operatorApp.ReadServiceObjectAssociationExact(ctx, repo, identity, objectID, head.CurrentAssociationVersion)
		if err != nil {
			return ServiceObjectDetail{}, err
		}
		association = &currentAssociation
	}
	mapping, err := operatorApp.ReadServiceObjectMappingPin(ctx, repo, identity, objectID)
	if err != nil {
		return ServiceObjectDetail{}, err
	}
	st, err := loadState(ctx, tx, false)
	if err != nil {
		return ServiceObjectDetail{}, err
	}
	facts := serviceObjectMaterialSelectionFacts(st, caseID)
	result := operatorApp.ProjectServiceObjectDetail(head, version, association, mapping, facts, serviceObjectCanManage(s, current, caseItem))
	if err := tx.Commit(ctx); err != nil {
		return ServiceObjectDetail{}, err
	}
	return result, nil
}

func (s *Store) ListServiceObjectVersions(ctx context.Context, user accounts.User, caseID, objectID, cursor string, limit int) (ServiceObjectVersionPage, error) {
	ctx = commandContext(ctx, user)
	limit, err := serviceObjectPageLimit(limit)
	if err != nil {
		return ServiceObjectVersionPage{}, err
	}
	caseID, objectID = strings.TrimSpace(caseID), strings.TrimSpace(objectID)
	lastVersion, err := decodeServiceObjectIntegerCursor(cursor, "versions", caseID, objectID)
	if err != nil {
		return ServiceObjectVersionPage{}, err
	}
	current, caseItem, tx, err := s.serviceObjectReadTx(ctx, user, strings.TrimSpace(caseID))
	if err != nil {
		return ServiceObjectVersionPage{}, err
	}
	defer tx.Rollback(ctx)
	repo := operatorPostgres.NewReads(platformpostgres.Bind(tx))
	identity := serviceObjectReadIdentity(current, caseItem)
	head, err := operatorApp.ReadServiceObjectHead(ctx, repo, identity, strings.TrimSpace(objectID))
	if err != nil {
		return ServiceObjectVersionPage{}, err
	}
	result, err := operatorApp.ReadServiceObjectVersions(ctx, repo, identity, head.ID, lastVersion, limit)
	if err != nil {
		return ServiceObjectVersionPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceObjectVersionPage{}, err
	}
	return result, nil
}

func (s *Store) GetServiceObjectVersion(ctx context.Context, user accounts.User, caseID, objectID string, version int) (ServiceObjectVersion, error) {
	ctx = commandContext(ctx, user)
	if version < 1 {
		return ServiceObjectVersion{}, fault.Invalid("服务对象版本无效")
	}
	current, caseItem, tx, err := s.serviceObjectReadTx(ctx, user, strings.TrimSpace(caseID))
	if err != nil {
		return ServiceObjectVersion{}, err
	}
	defer tx.Rollback(ctx)
	repo := operatorPostgres.NewReads(platformpostgres.Bind(tx))
	identity := serviceObjectReadIdentity(current, caseItem)
	head, err := operatorApp.ReadServiceObjectHead(ctx, repo, identity, strings.TrimSpace(objectID))
	if err != nil {
		return ServiceObjectVersion{}, err
	}
	item, err := operatorApp.ReadServiceObjectVersionExact(ctx, repo, identity, head.ID, version)
	if err != nil {
		return ServiceObjectVersion{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceObjectVersion{}, err
	}
	return item, nil
}

func (s *Store) ListServiceObjectAssociations(ctx context.Context, user accounts.User, caseID, objectID, cursor string, limit int) (ServiceObjectAssociationPage, error) {
	ctx = commandContext(ctx, user)
	limit, err := serviceObjectPageLimit(limit)
	if err != nil {
		return ServiceObjectAssociationPage{}, err
	}
	caseID, objectID = strings.TrimSpace(caseID), strings.TrimSpace(objectID)
	lastVersion, err := decodeServiceObjectIntegerCursor(cursor, "associations", caseID, objectID)
	if err != nil {
		return ServiceObjectAssociationPage{}, err
	}
	current, caseItem, tx, err := s.serviceObjectReadTx(ctx, user, strings.TrimSpace(caseID))
	if err != nil {
		return ServiceObjectAssociationPage{}, err
	}
	defer tx.Rollback(ctx)
	repo := operatorPostgres.NewReads(platformpostgres.Bind(tx))
	identity := serviceObjectReadIdentity(current, caseItem)
	head, err := operatorApp.ReadServiceObjectHead(ctx, repo, identity, strings.TrimSpace(objectID))
	if err != nil {
		return ServiceObjectAssociationPage{}, err
	}
	result, err := operatorApp.ReadServiceObjectAssociations(ctx, repo, identity, head.ID, lastVersion, limit)
	if err != nil {
		return ServiceObjectAssociationPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceObjectAssociationPage{}, err
	}
	return result, nil
}

func (s *Store) GetServiceObjectAssociation(ctx context.Context, user accounts.User, caseID, objectID string, associationVersion int) (ServiceObjectContractAssociation, error) {
	ctx = commandContext(ctx, user)
	if associationVersion < 1 {
		return ServiceObjectContractAssociation{}, fault.Invalid("服务对象关联版本无效")
	}
	current, caseItem, tx, err := s.serviceObjectReadTx(ctx, user, strings.TrimSpace(caseID))
	if err != nil {
		return ServiceObjectContractAssociation{}, err
	}
	defer tx.Rollback(ctx)
	repo := operatorPostgres.NewReads(platformpostgres.Bind(tx))
	identity := serviceObjectReadIdentity(current, caseItem)
	head, err := operatorApp.ReadServiceObjectHead(ctx, repo, identity, strings.TrimSpace(objectID))
	if err != nil {
		return ServiceObjectContractAssociation{}, err
	}
	item, err := operatorApp.ReadServiceObjectAssociationExact(ctx, repo, identity, head.ID, associationVersion)
	if err != nil {
		return ServiceObjectContractAssociation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceObjectContractAssociation{}, err
	}
	return item, nil
}

func (s *Store) PreviewServiceObjectAssociation(ctx context.Context, user accounts.User, caseID, objectID string, input ServiceObjectAssociationPreviewInput) (ServiceObjectAssociationPreview, error) {
	ctx = commandContext(ctx, user)
	caseID = strings.TrimSpace(caseID)
	current, caseItem, tx, err := s.serviceObjectReadTx(ctx, user, caseID)
	if err != nil {
		return ServiceObjectAssociationPreview{}, err
	}
	defer tx.Rollback(ctx)
	st, err := loadState(ctx, tx, false)
	if err != nil {
		return ServiceObjectAssociationPreview{}, err
	}
	head, err := loadServiceObjectHead(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, strings.TrimSpace(objectID))
	if err != nil {
		return ServiceObjectAssociationPreview{}, err
	}
	preview, err := deriveServiceObjectAssociationPreview(ctx, tx, s, st, current, caseItem, head, input)
	if err != nil {
		return ServiceObjectAssociationPreview{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceObjectAssociationPreview{}, err
	}
	return preview, nil
}

func (s *Store) ConfirmServiceObjectAssociation(ctx context.Context, user accounts.User, caseID, objectID, key string, input ServiceObjectAssociationConfirmInput) (ServiceObjectMutationResult, bool, error) {
	ctx = commandContext(ctx, user)
	var empty ServiceObjectMutationResult
	caseID = strings.TrimSpace(caseID)
	if err := s.requireServiceObjectCoreWritable(ctx); err != nil {
		return empty, false, err
	}
	current, caseItem, tx, err := s.serviceObjectMutationTx(ctx, user, caseID)
	if err != nil {
		return empty, false, err
	}
	defer tx.Rollback(ctx)
	if err := validateIdempotencyKey(key); err != nil {
		return empty, false, err
	}
	repo := operatorPostgres.NewConfirm(platformpostgres.Bind(tx))
	identity := operatorApp.CreateIdentity{TenantID: accounts.Tenant(current), OrgID: caseItem.OwnerOrgID, CaseID: caseID, ActorID: current.ID}
	prepared, result, replayed, err := operatorApp.PrepareConfirm(ctx, repo, identity, objectID, key, input)
	if err != nil {
		return empty, false, err
	}
	if replayed {
		if err := tx.Commit(ctx); err != nil {
			return empty, false, err
		}
		return result, true, nil
	}
	if err := businessWritable(caseItem); err != nil {
		return empty, false, err
	}
	st, err := loadState(ctx, tx, true)
	if err != nil {
		return empty, false, err
	}
	facts := operatorPreviewFacts{store: s, tx: tx, state: st, item: caseItem, user: current}
	scope := operatorApp.PreviewScope{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseItem.ID, BusinessLine: caseItem.BusinessLine, Scenario: caseItem.Scenario, ContractType: caseItem.ContractType}
	result, err = operatorApp.ConfirmAfterMiss[Document](ctx, repo, facts, scope, prepared)
	if err != nil {
		return empty, false, err
	}
	if err := recheckServiceObjectManage(ctx, tx, current, caseItem); err != nil {
		return empty, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return empty, false, err
	}
	return result, false, nil
}

func deriveServiceObjectAssociationPreview(ctx context.Context, tx pgx.Tx, s *Store, st state, user accounts.User, caseItem Case, head ServiceObjectHead, input ServiceObjectAssociationPreviewInput) (ServiceObjectAssociationPreview, error) {
	facts := operatorPreviewFacts{store: s, tx: tx, state: st, item: caseItem, user: user}
	scope := operatorApp.PreviewScope{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseItem.ID, BusinessLine: caseItem.BusinessLine, Scenario: caseItem.Scenario, ContractType: caseItem.ContractType}
	return operatorApp.Preview[Document](ctx, facts, scope, head, input)
}

func (s *Store) serviceObjectMutationTx(ctx context.Context, user accounts.User, caseID string) (accounts.User, Case, pgx.Tx, error) {
	ctx = commandContext(ctx, user)
	if ctx == nil {
		return accounts.User{}, Case{}, nil, fault.Invalid("服务对象写入需要请求上下文")
	}
	current, err := trustedUser(user)
	if err != nil {
		return accounts.User{}, Case{}, nil, err
	}
	caseID = strings.TrimSpace(caseID)
	if caseID == "" {
		return accounts.User{}, Case{}, nil, fault.NotFound("没有找到对应案件")
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return accounts.User{}, Case{}, nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(),0))`); err != nil {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, err
	}
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, err
	}
	if err := scopedCaseLock(ctx, tx, caseID); err != nil {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, err
	}
	caseItem, err := loadKnowledgeCase(ctx, tx, current, caseID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, err
	}
	if !authorizeCase(caseItem, current, access.Manage) {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, fault.Forbidden("当前授权不能维护服务对象")
	}
	if err := businessWritable(caseItem); err != nil {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, err
	}
	return current, caseItem, tx, nil
}

func (s *Store) serviceObjectReadTx(ctx context.Context, user accounts.User, caseID string) (accounts.User, Case, pgx.Tx, error) {
	ctx = commandContext(ctx, user)
	if ctx == nil {
		return accounts.User{}, Case{}, nil, fault.Invalid("服务对象读取需要请求上下文")
	}
	current, err := trustedUser(user)
	if err != nil {
		return accounts.User{}, Case{}, nil, err
	}
	caseID = strings.TrimSpace(caseID)
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return accounts.User{}, Case{}, nil, err
	}
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, err
	}
	caseItem, err := loadKnowledgeCase(ctx, tx, current, caseID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, err
	}
	if !authorizeCase(caseItem, current, access.Read) {
		_ = tx.Rollback(ctx)
		return accounts.User{}, Case{}, nil, fault.Forbidden("当前授权不能读取服务对象")
	}
	return current, caseItem, tx, nil
}

func (s *Store) requireServiceObjectCoreWritable(ctx context.Context) error {
	if ctx == nil {
		return fault.Invalid("服务对象写入需要请求上下文")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.ReadOnly() {
		return fault.New(503, "read_only", "只读模式下不能写入服务对象")
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return fault.Forbidden("只有 Core 服务可以写入服务对象")
	}
	return nil
}

func recheckServiceObjectManage(ctx context.Context, tx pgx.Tx, user accounts.User, before Case) error {
	current, err := trustedUser(user)
	if err != nil {
		return err
	}
	caseItem, err := loadKnowledgeCase(ctx, tx, current, before.ID)
	if err != nil {
		return err
	}
	if caseItem.OwnerOrgID != before.OwnerOrgID || !authorizeCase(caseItem, current, access.Manage) {
		return fault.Forbidden("当前授权不能维护服务对象")
	}
	if err := businessWritable(caseItem); err != nil {
		return err
	}
	return nil
}

func serviceObjectRequestSHA256(tenantID, orgID, caseID, actorID, operation string, payload any) (string, error) {
	return operatorApp.ServiceObjectRequestSHA256(operatorApp.CreateIdentity{TenantID: tenantID, OrgID: orgID, CaseID: caseID, ActorID: actorID}, operation, payload)
}

func loadServiceObjectVersionValue(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID string, head ServiceObjectHead) (ServiceObjectVersion, error) {
	return operatorApp.ReadServiceObjectVersionExact(ctx, operatorPostgres.NewReads(platformpostgres.Bind(tx)),
		operatorApp.ReadIdentity{TenantID: tenantID, OrgID: orgID, CaseID: caseID}, head.ID, head.CurrentVersion)
}

func loadServiceObjectVersionExact(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, objectID string, number int) (ServiceObjectVersion, error) {
	return loadServiceObjectVersionValue(ctx, tx, tenantID, orgID, caseID, ServiceObjectHead{ID: objectID, CurrentVersion: number})
}

func loadServiceObjectHead(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, objectID string) (ServiceObjectHead, error) {
	return operatorApp.ReadServiceObjectHead(ctx, operatorPostgres.NewReads(platformpostgres.Bind(tx)),
		operatorApp.ReadIdentity{TenantID: tenantID, OrgID: orgID, CaseID: caseID}, objectID)
}

func loadServiceObjectAssociation(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, objectID string, number int) (ServiceObjectContractAssociation, error) {
	return operatorApp.ReadServiceObjectAssociationExact(ctx, operatorPostgres.NewReads(platformpostgres.Bind(tx)),
		operatorApp.ReadIdentity{TenantID: tenantID, OrgID: orgID, CaseID: caseID}, objectID, number)
}

func serviceObjectPageLimit(limit int) (int, error) {
	return operatorApp.ServiceObjectPageLimit(limit)
}

func decodeServiceObjectCursor(value, kind, caseID, objectID string) (string, error) {
	return operatorApp.DecodeServiceObjectCursor(value, kind, caseID, objectID)
}

func decodeServiceObjectIntegerCursor(value, kind, caseID, objectID string) (int, error) {
	return operatorApp.DecodeServiceObjectIntegerCursor(value, kind, caseID, objectID)
}

func serviceObjectCanManage(s *Store, user accounts.User, item Case) bool {
	return authorizeCase(item, user, access.Manage) && businessWritable(item) == nil && !s.ReadOnly()
}

func serviceObjectMaterialSelectionFacts(st state, caseID string) operatorModel.ServiceObjectMaterialSelectionFacts {
	selected := selectedMaterialDocuments(st, caseID)
	selectedIDs := make([]string, 0, len(selected))
	for _, document := range selected {
		selectedIDs = append(selectedIDs, document.ID)
	}
	materialHeads := make([]operatorModel.ServiceObjectMaterialHead, 0)
	for _, material := range st.Materials {
		if material.CaseID == caseID {
			materialHeads = append(materialHeads, operatorModel.ServiceObjectMaterialHead{
				ID: material.ID, CurrentVersion: material.CurrentVersion, CurrentDocumentID: material.CurrentDocumentID,
			})
		}
	}
	return operatorModel.ServiceObjectMaterialSelectionFacts{Pending: materialSelectionPending(st, caseID),
		SelectedDocumentIDs: selectedIDs, MaterialHeads: materialHeads}
}

func serviceObjectReadIdentity(user accounts.User, item Case) operatorApp.ReadIdentity {
	return operatorApp.ReadIdentity{TenantID: accounts.Tenant(user), OrgID: item.OwnerOrgID, CaseID: item.ID}
}

func insertServiceObjectVersion(ctx context.Context, tx pgx.Tx, user accounts.User, item Case, version ServiceObjectVersion) error {
	return operatorPostgres.NewConfirm(platformpostgres.Bind(tx)).AppendVersion(ctx, operatorApp.CreateIdentity{TenantID: accounts.Tenant(user), OrgID: item.OwnerOrgID, CaseID: item.ID, ActorID: user.ID}, version)
}
func insertServiceObjectAssociation(ctx context.Context, tx pgx.Tx, user accounts.User, item Case, association ServiceObjectContractAssociation) error {
	return operatorPostgres.NewConfirm(platformpostgres.Bind(tx)).AppendAssociation(ctx, operatorApp.CreateIdentity{TenantID: accounts.Tenant(user), OrgID: item.OwnerOrgID, CaseID: item.ID, ActorID: user.ID}, association)
}

func canonicalServiceObjectAssociationSHA256(item ServiceObjectContractAssociation) (string, error) {
	return operatorModel.CanonicalServiceObjectAssociationSHA256(item)
}
