// File metadata (manifest mirror):
// Purpose: Contract drafts, receipts and legacy imports are owned by the core service. They never enter the legacy loadState/clone writer and never nest an old schema write inside a contract… Related `store` operations include validateCLMIdempotencyKey, bindLocalDemoContractGrant, authorizeContract, scopedContractLock.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	contractapp "contractaudit/internal/contract/app"
	clmcontract "contractaudit/internal/contract/model"
	contractpostgres "contractaudit/internal/contract/postgres"
	"contractaudit/internal/contractmoney"
	"contractaudit/internal/fault"
	platformpostgres "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Contract drafts, receipts and legacy imports are owned by the core service.
// They never enter the legacy loadState/clone writer and never nest an old
// schema write inside a contract lock (docs/11 事务边界).
const (
	clmRouteCreateDraft  = contractapp.CreateDraftRoute
	clmRouteLegacyImport = "clm.legacy_import"

	clmEventLegacyImported = "contract.legacy_imported.v1"
)

// Contract summary and aggregate models are owned by internal/contract; these
// aliases preserve Store consumers while this domain migrates incrementally.
type clmContractRecord = clmcontract.Aggregate
type ContractSummary = clmcontract.Summary

// ContractDetail remains the Store compatibility envelope because it also
// contains response types owned by the signing slice.
type ContractDetail struct {
	Summary                      ContractSummary              `json:"summary"`
	CurrentRevisionID            *string                      `json:"current_revision_id"`
	Draft                        *DraftContent                `json:"draft,omitempty"`
	ActiveRoundID                *string                      `json:"active_round_id"`
	AllowedCommands              []string                     `json:"allowed_commands"`
	SourceRef                    *SourceRef                   `json:"source_ref,omitempty"`
	SourceID                     string                       `json:"source_id,omitempty"`
	SourceObservedAt             *time.Time                   `json:"source_observed_at,omitempty"`
	SourceObservedState          string                       `json:"source_observed_state,omitempty"`
	SourceObservationDisposition string                       `json:"source_observation_disposition,omitempty"`
	SigningRegistration          *ContractSigningRegistration `json:"signing_registration,omitempty"`
	Warnings                     []string                     `json:"warnings"`
}

// ContractDraftSummary remains available from Store as a compatibility alias.
type ContractDraftSummary = clmcontract.ContractDraftSummary

// CreateContractDraftInput remains available from Store as a compatibility alias.
type CreateContractDraftInput = clmcontract.CreateContractDraftInput

// ReplaceContractDraftInput remains available from Store as a compatibility alias.
type ReplaceContractDraftInput = clmcontract.ReplaceContractDraftInput

// LegacyImportInput registers one already-signed historical contract. It binds
// an authorized real case and material version, keeps unknown history unknown
// and never fabricates local approval rounds or signatures.
type LegacyImportInput struct {
	ExpectedRowVersion          int64
	AuthorityMode               string
	SourceRef                   SourceRef
	SourceEvidence              MaterialRef
	Snapshot                    DraftContent
	ApprovalHistoryStatus       string
	HistoricalApprovalMaterials []MaterialRef
	Reason                      string
	IdempotencyKey              string
}

// resolvedMaterialVersion is a verified material version with its owning case.
type resolvedMaterialVersion struct {
	CaseOrgID    string
	CaseID       string
	MaterialID   string
	Version      int
	DocumentID   string
	Digest       string
	MaterialRole string
	Filename     string
	MediaType    string
}

type clmWriteReceipt = clmcontract.WriteReceipt

// validateCLMIdempotencyKey follows the design parameter: 8 to 128 visible
// ASCII characters. The key is scoped to tenant, actor and route scope.
func validateCLMIdempotencyKey(key string) error { return clmcontract.ValidateCLMIdempotencyKey(key) }

// bindLocalDemoContractGrant binds an unbound demo case grant to a contract
// only when the same identity created that contract. Enterprise identities are
// returned unchanged, and no administrator gains implied contract rights.
func bindLocalDemoContractGrant(record clmContractRecord, current accounts.User, principal access.Principal) access.Principal {
	if current.Issuer != "" || record.TenantID != current.TenantID ||
		(current.TenantID != accounts.LocalDemoTenant && current.TenantID != accounts.LocalDemoTenant2) {
		return principal
	}
	if record.CreatedBy != current.ID {
		return principal
	}
	for i := range principal.Grants {
		if principal.Grants[i].Scope == "case" && principal.Grants[i].CaseID == "" {
			principal.Grants[i].CaseID = record.ContractID
		}
	}
	return principal
}

// authorizeContract maps one contract aggregate onto the case-scope policy.
// Owner is the creator, so a self-scope grant reaches only own contracts.
func authorizeContract(record clmContractRecord, user accounts.User, action access.Action) bool {
	current, err := trustedUser(user)
	if err != nil {
		return false
	}
	principal := bindLocalDemoContractGrant(record, current, accounts.Principal(current))
	resource := access.Resource{
		TenantID: record.TenantID, CaseID: record.ContractID,
		OrgID: record.OwnerOrgID, OwnerID: record.CreatedBy,
	}
	return access.Check(action, principal, resource, time.Now().UTC()).Allowed
}

func authorizeContractRead(ctx context.Context, tx pgx.Tx, record clmContractRecord, user accounts.User) bool {
	if record.AuthorityMode != "external" {
		return authorizeContract(record, user, access.Read)
	}
	current, err := trustedUser(user)
	if err != nil {
		return false
	}
	return checkExternalContractCaseRead(ctx, tx, record, current)
}

// scopedContractLock serialises contract writers without touching the legacy
// case lock; the key space is disjoint from ':case:'.
func scopedContractLock(ctx context.Context, tx pgx.Tx, contractID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':clm-contract:' || $1, 0))`, contractID)
	return err
}

// clmCreatorGate limits contract creation and legacy import to the roles that
// may create business records; draft replacement is gated by contract manage
// authority instead.
func clmCreatorGate(current accounts.User) error {
	if !accounts.CanCreate(current.Role) {
		return fault.Forbidden("当前角色不能创建合同记录")
	}
	return nil
}

func (s *Store) beginCLMWrite(ctx context.Context, user accounts.User, readOnlyMessage string, gate func(accounts.User) error) (pgx.Tx, accounts.User, error) {
	ctx = commandContext(ctx, user)
	return s.beginCLMWriteIsolated(ctx, user, readOnlyMessage, gate, pgx.TxOptions{})
}

// beginCLMWriteIsolated is the explicit-isolation form of beginCLMWrite. The
// ledger export uses RepeatableRead because it scans the frozen row set in
// batches: ReadCommitted would let concurrent writes produce a row set that
// never existed at any single instant.
func (s *Store) beginCLMWriteIsolated(ctx context.Context, user accounts.User, readOnlyMessage string, gate func(accounts.User) error, options pgx.TxOptions) (pgx.Tx, accounts.User, error) {
	ctx = commandContext(ctx, user)
	if s.pool == nil {
		return nil, accounts.User{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	if err := s.checkCLMWriteAllowed(readOnlyMessage); err != nil {
		return nil, accounts.User{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return nil, accounts.User{}, err
	}
	if gate != nil {
		if err := gate(current); err != nil {
			return nil, accounts.User{}, err
		}
	}
	tx, err := s.beginContextTx(ctx, options)
	if err != nil {
		return nil, accounts.User{}, err
	}
	fail := func(err error) (pgx.Tx, accounts.User, error) {
		_ = tx.Rollback(context.Background())
		return nil, accounts.User{}, err
	}
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return fail(err)
	}
	return tx, current, nil
}

func (s *Store) checkCLMWriteAllowed(readOnlyMessage string) error {
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return fault.Forbidden("当前服务不能执行合同写操作")
	}
	if s.ReadOnly() {
		return fault.New(503, "read_only", readOnlyMessage)
	}
	return nil
}

func rollbackCLM(tx pgx.Tx) {
	if tx != nil {
		_ = tx.Rollback(context.Background())
	}
}

const clmContractColumns = `tenant_id,contract_id,owner_org_id,authority_mode,contract_number,
 source_system,external_contract_id,source_version,lifecycle_state,validity_state,
 current_revision_id,row_version,created_by,created_at,updated_at`

func scanCLMContract(row pgx.Row) (clmContractRecord, error) {
	var record clmContractRecord
	err := row.Scan(&record.TenantID, &record.ContractID, &record.OwnerOrgID, &record.AuthorityMode,
		&record.ContractNumber, &record.SourceSystem, &record.ExternalContractID, &record.SourceVersion,
		&record.LifecycleState, &record.ValidityState, &record.CurrentRevisionID, &record.RowVersion,
		&record.CreatedBy, &record.CreatedAt, &record.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmContractRecord{}, fault.NotFound("没有找到对应记录")
	}
	return record, err
}

func loadCLMContract(ctx context.Context, tx pgx.Tx, tenantID, contractID string) (clmContractRecord, error) {
	return scanCLMContract(tx.QueryRow(ctx, `SELECT `+clmContractColumns+` FROM clm_contracts
		WHERE tenant_id=$1 AND contract_id=$2`, tenantID, contractID))
}

func loadCLMWriteReceipt(ctx context.Context, tx pgx.Tx, tenantID, actorID, routeScope, key string) (clmWriteReceipt, bool, error) {
	var receipt clmWriteReceipt
	err := tx.QueryRow(ctx, `SELECT request_digest, contract_id, response_status, response_json
		FROM clm_contract_write_receipts WHERE tenant_id=$1 AND actor_id=$2 AND route_scope=$3 AND idempotency_key=$4`,
		tenantID, actorID, routeScope, key).Scan(&receipt.RequestDigest, &receipt.ContractID, &receipt.ResponseStatus, &receipt.Response)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmWriteReceipt{}, false, nil
	}
	if err != nil {
		return clmWriteReceipt{}, false, err
	}
	return receipt, true, nil
}

func insertCLMWriteReceipt(ctx context.Context, tx pgx.Tx, tenantID, actorID, routeScope, key, digest, contractID string, status int, response []byte) error {
	_, err := tx.Exec(ctx, `INSERT INTO clm_contract_write_receipts
		(tenant_id,actor_id,route_scope,idempotency_key,request_digest,contract_id,response_status,response_json)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb)`,
		tenantID, actorID, routeScope, key, digest, contractID, status, string(response))
	return err
}

// replayCLMWriteReceipt returns a committed receipt only after re-checking the
// caller's current identity and object visibility; a revoked or invisible
// object replays nothing. Same key with a different verified payload is a
// conflict.
func replayCLMWriteReceipt(ctx context.Context, tx pgx.Tx, current accounts.User, routeScope, key, digest, requestedContractID string) (clmWriteReceipt, bool, error) {
	if key == "" {
		return clmWriteReceipt{}, false, nil
	}
	receipt, found, err := loadCLMWriteReceipt(ctx, tx, accounts.Tenant(current), current.ID, routeScope, key)
	if err != nil || !found {
		return clmWriteReceipt{}, false, err
	}
	if receipt.RequestDigest != digest {
		return clmWriteReceipt{}, false, fault.Conflict("Idempotency-Key已用于不同请求载荷")
	}
	if requestedContractID != "" && receipt.ContractID != requestedContractID {
		return clmWriteReceipt{}, false, fault.Conflict("Idempotency-Key已用于其他合同")
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), receipt.ContractID)
	if err != nil {
		return clmWriteReceipt{}, false, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return clmWriteReceipt{}, false, fault.NotFound("没有找到对应记录")
	}
	return receipt, true, nil
}

// projectReceiptDetail restores the stored sealed response for the current
// caller: plaintext restricted fields only while manage authority holds.
func projectReceiptDetail(ctx context.Context, receipt clmWriteReceipt, current accounts.User, record clmContractRecord) (ContractDetail, error) {
	var stored ContractDetail
	if err := unmarshalStoredDraft(receipt.Response, &stored); err != nil {
		return ContractDetail{}, err
	}
	if stored.Draft == nil {
		return stored, nil
	}
	plaintext, err := unsealStoredDraftBound(ctx, marshalDraftContent(stored.Draft), record.TenantID, record.ContractID)
	if err != nil {
		return ContractDetail{}, err
	}
	stored.Draft = new(projectDraftForViewer(plaintext, authorizeContract(record, current, access.Manage)))
	return stored, nil
}

func marshalDraftContent(draft *DraftContent) []byte {
	if draft == nil {
		return nil
	}
	encoded, err := json.Marshal(draft)
	if err != nil {
		// DraftContent only contains JSON-native typed fields; marshalling
		// cannot fail for values that passed validation.
		return nil
	}
	return encoded
}

// draftContentSummaryFacts extracts unrestricted summary facts from stored
// content bytes without ever selecting restricted fields from JSONB.
func draftContentSummaryFacts(contentJSON []byte) (string, *string, *string, error) {
	name, amount, currency, err := draftSummaryFacts(contentJSON)
	return name, amount, currency, err
}

func clmSummary(record clmContractRecord, name string, amount, currency *string) ContractSummary {
	return clmcontract.SummaryFromDraft(record, name, amount, currency)
}

func contractSourceRef(record clmContractRecord) *SourceRef {
	if record.SourceSystem == nil || record.ExternalContractID == nil || record.SourceVersion == nil {
		return nil
	}
	return &SourceRef{System: *record.SourceSystem, ID: *record.ExternalContractID, SourceVersion: *record.SourceVersion}
}

// resolveMaterialVersion verifies one material reference against the real
// material_versions/documents rows under the caller's tenant scope, including
// the stored documents.digest. The document ID is globally unique, so a
// matched pair resolves to at most one case.
func resolveMaterialVersion(ctx context.Context, tx pgx.Tx, tenantID string, ref MaterialRef) (resolvedMaterialVersion, error) {
	var resolved resolvedMaterialVersion
	err := tx.QueryRow(ctx, `SELECT mv.org_id, mv.case_id, mv.material_id, mv.version, mv.document_id, d.digest,
			me.role, d.filename, d.media_type
		FROM material_versions mv
		JOIN material_entities me
		  ON me.tenant_id=mv.tenant_id AND me.org_id=mv.org_id AND me.case_id=mv.case_id AND me.material_id=mv.material_id
		JOIN documents d ON d.tenant_id=mv.tenant_id AND d.org_id=mv.org_id AND d.case_id=mv.case_id AND d.id=mv.document_id
		WHERE mv.tenant_id=$1 AND mv.material_id=$2 AND mv.document_id=$3`,
		tenantID, ref.MaterialID, ref.MaterialVersionID).Scan(
		&resolved.CaseOrgID, &resolved.CaseID, &resolved.MaterialID, &resolved.Version, &resolved.DocumentID,
		&resolved.Digest, &resolved.MaterialRole, &resolved.Filename, &resolved.MediaType)
	if errors.Is(err, pgx.ErrNoRows) {
		return resolvedMaterialVersion{}, fault.NotFound("没有找到可核实的材料版本")
	}
	if err != nil {
		return resolvedMaterialVersion{}, err
	}
	if resolved.Digest != ref.SHA256 {
		return resolvedMaterialVersion{}, fault.Invalid("材料引用摘要与已存原件不一致")
	}
	return resolved, nil
}

// loadCLMCase resolves one source case inside the transaction and re-checks
// real case grants; the local demo binding keeps its membership semantics.
func loadCLMCase(ctx context.Context, tx pgx.Tx, current accounts.User, orgID, caseID string) (Case, error) {
	var item Case
	var members string
	err := tx.QueryRow(ctx, `SELECT id,tenant_id,org_id,owner_id,member_ids,business_status,archived_at
		FROM cases WHERE tenant_id=$1 AND org_id=$2 AND id=$3`,
		accounts.Tenant(current), orgID, caseID).Scan(&item.ID, &item.TenantID, &item.OwnerOrgID, &item.OwnerID, &members, &item.BusinessStatus, &item.ArchivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Case{}, fault.NotFound("没有找到对应记录")
	}
	if err != nil {
		return Case{}, err
	}
	if err := json.Unmarshal([]byte(members), &item.MemberIDs); err != nil {
		return Case{}, err
	}
	if !authorizeCase(item, current, access.Read) {
		return Case{}, fault.NotFound("没有找到对应记录")
	}
	return item, nil
}

// CreateContractDraft opens one resumable native draft. The draft may stay
// incomplete; only formats are enforced. Creation carries expected_row_version
// 0 and stores an idempotent receipt for replay.
func (s *Store) CreateContractDraft(ctx context.Context, user accounts.User, input CreateContractDraftInput) (ContractDetail, error) {
	ctx = commandContext(ctx, user)
	prepared, err := contractapp.PrepareCreateDraft(input)
	if err != nil {
		return ContractDetail{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能创建合同草稿", clmCreatorGate)
	if err != nil {
		return ContractDetail{}, err
	}
	defer rollbackCLM(tx)
	repository := contractpostgres.NewDraftCommands(platformpostgres.Bind(tx))
	result, err := contractapp.CreateDraft(ctx, contractapp.CreateDraftPrincipal{
		TenantID: accounts.Tenant(current), ActorID: current.ID, OrgID: current.OrgID,
	}, prepared, repository, storeDraftCodec{}, storeDraftAccess{tx: tx, user: current})
	if err != nil {
		return ContractDetail{}, err
	}
	detail := contractDetailFromAppResult(result)
	if err := tx.Commit(ctx); err != nil {
		return ContractDetail{}, err
	}
	return detail, nil
}

func optionalTextOrPointer(value *string) *string {
	if value == nil {
		return nil
	}
	return optionalText(*value)
}

func clmUniqueViolation(err error, message string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fault.Conflict(message)
	}
	return err
}

// clmDraftCommands lists the commands the caller may actually issue. A viewer
// without manage authority must not see replace_draft: the masked restricted
// fields are not writable, so offering the command would only invite a
// conflict with the stored sealed values.
func clmDraftCommands(record clmContractRecord, canManage bool) []string {
	return clmcontract.DraftCommands(record, canManage)
}

// sealedDraftOf decodes persisted sealed bytes back into the wire shape
// without ever unsealing restricted fields.
func sealedDraftOf(sealedBytes []byte) *DraftContent {
	var sealed DraftContent
	if err := unmarshalStoredDraft(sealedBytes, &sealed); err != nil {
		return nil
	}
	return &sealed
}

// LoadContractDetail returns one visible contract with its resumable draft.
// Invisible and unknown contracts answer identically.
func (s *Store) LoadContractDetail(ctx context.Context, user accounts.User, contractID string) (ContractDetail, error) {
	ctx = commandContext(ctx, user)
	if !s.HasDatabase() {
		return ContractDetail{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	id, err := normalizeContractUUID(contractID)
	if err != nil {
		return ContractDetail{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return ContractDetail{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ContractDetail{}, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ContractDetail{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ContractDetail{}, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return ContractDetail{}, fault.NotFound("没有找到对应记录")
	}
	if err := setLocalScope(ctx, tx, dbScope{ActorID: current.ID, TenantID: accounts.Tenant(current), OrgID: record.OwnerOrgID, Role: "api"}); err != nil {
		return ContractDetail{}, err
	}
	result, err := contractapp.LoadDraftDetail(ctx, record,
		contractpostgres.NewDraftQueries(platformpostgres.Bind(tx)),
		storeDraftWorkflowReader{tx: tx, user: current}, storeDraftCodec{},
		storeDraftAccess{tx: tx, user: current})
	if err != nil {
		return ContractDetail{}, err
	}
	detail := contractDetailFromAppResult(result)
	registration, err := s.loadContractSigningSummary(ctx, tx, current, record)
	if err != nil {
		return ContractDetail{}, err
	}
	detail.SigningRegistration = registration
	if err := tx.Commit(ctx); err != nil {
		return ContractDetail{}, err
	}
	return detail, nil
}

// ListMyContractDrafts returns only the caller's own active native drafts, for
// recovery after an interruption. The full ledger belongs to a later milestone.
func (s *Store) ListMyContractDrafts(ctx context.Context, user accounts.User, limit int) ([]ContractDraftSummary, error) {
	ctx = commandContext(ctx, user)
	if !s.HasDatabase() {
		return nil, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return nil, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return nil, err
	}
	items, err := contractapp.ListMyDrafts(ctx, accounts.Tenant(current), current.ID, limit,
		contractpostgres.NewDraftQueries(platformpostgres.Bind(tx)))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

// ReplaceContractDraft fully replaces one editable draft. The replacement
// carries expected_row_version against the contract aggregate; any concurrent
// change answers 409 and the client must re-read and confirm.
func (s *Store) ReplaceContractDraft(ctx context.Context, user accounts.User, input ReplaceContractDraftInput) (ContractDetail, error) {
	ctx = commandContext(ctx, user)
	prepared, err := contractapp.PrepareReplaceDraft(input)
	if err != nil {
		return ContractDetail{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能修改合同草稿", nil)
	if err != nil {
		return ContractDetail{}, err
	}
	defer rollbackCLM(tx)
	result, err := contractapp.ReplaceDraft(ctx, contractapp.CreateDraftPrincipal{
		TenantID: accounts.Tenant(current), ActorID: current.ID, OrgID: current.OrgID,
	}, prepared, contractpostgres.NewDraftCommands(platformpostgres.Bind(tx)), storeDraftCodec{},
		storeDraftAccess{tx: tx, user: current}, storeDraftWorkflowReader{tx: tx, user: current})
	if err != nil {
		return ContractDetail{}, err
	}
	detail := contractDetailFromAppResult(result)
	if err := tx.Commit(ctx); err != nil {
		return ContractDetail{}, err
	}
	return detail, nil
}

// ImportLegacySignedContract registers one historical signed contract with a
// frozen revision, a verified source case and material version, and an
// append-only provenance record. Unknown approval history stays unknown; no
// local rounds or signatures are fabricated.
func (s *Store) ImportLegacySignedContract(ctx context.Context, user accounts.User, input LegacyImportInput) (ContractDetail, error) {
	ctx = commandContext(ctx, user)
	if input.ExpectedRowVersion != 0 {
		return ContractDetail{}, fault.Invalid("存量导入必须携带 expected_row_version=0")
	}
	if input.AuthorityMode != "native" {
		return ContractDetail{}, fault.Invalid("本版只能导入 native 权威模式的存量合同")
	}
	if err := validateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return ContractDetail{}, err
	}
	if err := validateSourceRef(&input.SourceRef); err != nil {
		return ContractDetail{}, err
	}
	if err := validateMaterialRef(&input.SourceEvidence); err != nil {
		return ContractDetail{}, err
	}
	if input.ApprovalHistoryStatus != "unknown" && input.ApprovalHistoryStatus != "evidence_attached" {
		return ContractDetail{}, fault.Invalid("approval_history_status 必须是 unknown 或 evidence_attached")
	}
	if input.ApprovalHistoryStatus == "unknown" && len(input.HistoricalApprovalMaterials) > 0 {
		return ContractDetail{}, fault.Invalid("审批历史未知时不能登记历史审批材料")
	}
	if input.ApprovalHistoryStatus == "evidence_attached" && len(input.HistoricalApprovalMaterials) == 0 {
		return ContractDetail{}, fault.Invalid("登记历史审批材料时必须提供至少一项材料引用")
	}
	if len(input.HistoricalApprovalMaterials) > 100 {
		return ContractDetail{}, fault.Invalid("历史审批材料最多 100 项")
	}
	for index := range input.HistoricalApprovalMaterials {
		if err := validateMaterialRef(&input.HistoricalApprovalMaterials[index]); err != nil {
			return ContractDetail{}, err
		}
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" || runeCount(reason) > 2000 {
		return ContractDetail{}, fault.Invalid("请填写存量导入原因（1 到 2000 字）")
	}
	input.Reason = reason
	sanitizeDraftReferences(&input.Snapshot)
	if err := validateDraftContent(&input.Snapshot); err != nil {
		return ContractDetail{}, err
	}
	amountsReconciled := false
	if legacyAmountsKnown(&input.Snapshot) {
		// Legacy registration is not the #81 freeze command: it stays a
		// gated path so an unknown historical amount remains legal (#77).
		// The revision records whether StageFinal actually ran, so an
		// unreconciled unknown-amount import can never be mistaken for a
		// reconciled frozen version.
		if err := validateDraftAmounts(&input.Snapshot, contractmoney.StageFinal); err != nil {
			return ContractDetail{}, err
		}
		amountsReconciled = true
	}
	digest, err := clmRequestDigest(clmRouteLegacyImport, "POST", "/api/contracts/legacy-imports", input.ExpectedRowVersion, input)
	if err != nil {
		return ContractDetail{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能导入存量合同", clmCreatorGate)
	if err != nil {
		return ContractDetail{}, err
	}
	defer rollbackCLM(tx)
	if err := lockWriteReceipt(ctx, tx, current, clmRouteLegacyImport, input.IdempotencyKey); err != nil {
		return ContractDetail{}, err
	}
	receipt, found, err := replayCLMWriteReceipt(ctx, tx, current, clmRouteLegacyImport, input.IdempotencyKey, digest, "")
	if err != nil {
		return ContractDetail{}, err
	}
	if found {
		record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), receipt.ContractID)
		if err != nil {
			return ContractDetail{}, err
		}
		detail, err := projectReceiptDetail(ctx, receipt, current, record)
		if err != nil {
			return ContractDetail{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ContractDetail{}, err
		}
		return detail, nil
	}
	source, err := resolveMaterialVersion(ctx, tx, accounts.Tenant(current), input.SourceEvidence)
	if err != nil {
		return ContractDetail{}, err
	}
	sourceCase, err := loadCLMCase(ctx, tx, current, source.CaseOrgID, source.CaseID)
	if err != nil {
		return ContractDetail{}, err
	}
	// The source case is the anchor of the frozen reference chain. Resolve
	// every subject, material and procurement result against tables owned by
	// the subject/document services before sealing the snapshot; the pins are
	// written into the JSONB snapshot, never into upstream tables.
	if _, err := resolveContractReferences(ctx, tx, current, sourceCase, &input.Snapshot, referenceModeImport); err != nil {
		return ContractDetail{}, err
	}
	historical, err := s.resolveHistoricalMaterials(ctx, tx, current, input.HistoricalApprovalMaterials)
	if err != nil {
		return ContractDetail{}, err
	}
	contractID, err := newContractUUID()
	if err != nil {
		return ContractDetail{}, err
	}
	_, sealedSnapshot, _, err := sealDraftContentBound(ctx, input.Snapshot, accounts.Tenant(current), contractID)
	if err != nil {
		return ContractDetail{}, err
	}
	// The revision stores jsonb, not the raw Go bytes. Digest the exact
	// canonical text the row will return so later reads can verify it.
	sealedSnapshot, err = canonicalStoredJSON(ctx, tx, sealedSnapshot)
	if err != nil {
		return ContractDetail{}, err
	}
	if err := scopedContractLock(ctx, tx, contractID); err != nil {
		return ContractDetail{}, err
	}
	revisionID, err := newContractUUID()
	if err != nil {
		return ContractDetail{}, err
	}
	contractNumber := optionalTextOrPointer(input.Snapshot.Contract.Number)
	amount, currency := legacyAmountPair(input.Snapshot)
	var createdAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO clm_contracts
		(tenant_id,contract_id,owner_org_id,authority_mode,contract_number,source_system,external_contract_id,source_version,lifecycle_state,row_version,created_by)
		VALUES ($1,$2,$3,'native',$4,$5,$6,$7,'registered',1,$8) RETURNING created_at`,
		accounts.Tenant(current), contractID, current.OrgID, contractNumber,
		input.SourceRef.System, input.SourceRef.ID, input.SourceRef.SourceVersion, current.ID).Scan(&createdAt)
	if err != nil {
		return ContractDetail{}, clmUniqueViolation(err, "合同编号或来源合同已存在")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_contract_revisions
		(tenant_id,contract_id,contract_revision_id,revision_number,amount,currency,snapshot,snapshot_digest,frozen_by,amounts_reconciled)
		VALUES ($1,$2,$3,1,$4,$5,$6::jsonb,$7,$8,$9)`,
		accounts.Tenant(current), contractID, revisionID, amount, currency,
		string(sealedSnapshot), sha256Bytes(sealedSnapshot), current.ID, amountsReconciled); err != nil {
		return ContractDetail{}, err
	}
	if err := insertFrozenContractRevisionItems(ctx, tx, accounts.Tenant(current), contractID, revisionID, input.Snapshot.Items); err != nil {
		return ContractDetail{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE clm_contracts SET current_revision_id=$3, updated_at=clock_timestamp()
		WHERE tenant_id=$1 AND contract_id=$2`, accounts.Tenant(current), contractID, revisionID); err != nil {
		return ContractDetail{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_contract_cases (tenant_id,contract_id,case_org_id,case_id,linked_by)
		VALUES ($1,$2,$3,$4,$5)`, accounts.Tenant(current), contractID, sourceCase.OwnerOrgID, sourceCase.ID, current.ID); err != nil {
		return ContractDetail{}, err
	}
	historicalJSON, err := json.Marshal(historical)
	if err != nil {
		return ContractDetail{}, err
	}
	var importedAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO clm_legacy_imports
		(tenant_id,contract_id,contract_revision_id,source_system,source_id,source_version,
		 case_org_id,case_id,material_id,material_version,material_digest,
		 approval_history_status,historical_approval_materials,reason,imported_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::jsonb,$14,$15)
		RETURNING imported_at`,
		accounts.Tenant(current), contractID, revisionID, input.SourceRef.System, input.SourceRef.ID, input.SourceRef.SourceVersion,
		source.CaseOrgID, source.CaseID, source.MaterialID, source.Version, source.Digest,
		input.ApprovalHistoryStatus, string(historicalJSON), input.Reason, current.ID).Scan(&importedAt)
	if err != nil {
		return ContractDetail{}, err
	}
	eventID, err := newContractUUID()
	if err != nil {
		return ContractDetail{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"contract_id":               contractID,
		"contract_revision_id":      revisionID,
		"source_system":             input.SourceRef.System,
		"source_id":                 input.SourceRef.ID,
		"source_version":            input.SourceRef.SourceVersion,
		"case_org_id":               source.CaseOrgID,
		"case_id":                   source.CaseID,
		"material_id":               source.MaterialID,
		"material_version":          source.Version,
		"material_digest":           source.Digest,
		"approval_history_status":   input.ApprovalHistoryStatus,
		"historical_material_count": len(historical),
	})
	if err != nil {
		return ContractDetail{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_business_events
		(tenant_id,event_id,contract_id,aggregate_version,event_type,schema_version,payload)
		VALUES ($1,$2,$3,1,$4,1,$5::jsonb)`,
		accounts.Tenant(current), eventID, contractID, clmEventLegacyImported, string(payload)); err != nil {
		return ContractDetail{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_outbox_delivery (tenant_id,event_id) VALUES ($1,$2)`,
		accounts.Tenant(current), eventID); err != nil {
		return ContractDetail{}, err
	}
	record := clmContractRecord{
		TenantID: accounts.Tenant(current), ContractID: contractID, OwnerOrgID: current.OrgID,
		AuthorityMode: "native", ContractNumber: contractNumber, SourceSystem: new(input.SourceRef.System),
		ExternalContractID: new(input.SourceRef.ID), SourceVersion: new(input.SourceRef.SourceVersion),
		LifecycleState: "registered", ValidityState: "active", CurrentRevisionID: new(revisionID),
		RowVersion: 1, CreatedBy: current.ID, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	detailName, detailAmount, detailCurrency, err := draftContentSummaryFacts(sealedSnapshot)
	if err != nil {
		return ContractDetail{}, err
	}
	sealedDetail := ContractDetail{
		Summary:           clmSummary(record, detailName, detailAmount, detailCurrency),
		CurrentRevisionID: new(revisionID),
		ActiveRoundID:     nil,
		AllowedCommands:   clmDraftCommands(record, true),
		SourceRef:         contractSourceRef(record),
		SourceObservedAt:  new(importedAt),
		Warnings:          clmImportWarnings(input.ApprovalHistoryStatus),
	}
	response, err := json.Marshal(sealedDetail)
	if err != nil {
		return ContractDetail{}, err
	}
	if err := insertCLMWriteReceipt(ctx, tx, accounts.Tenant(current), current.ID, clmRouteLegacyImport,
		input.IdempotencyKey, digest, contractID, 201, response); err != nil {
		return ContractDetail{}, err
	}
	detail := sealedDetail
	plaintext, err := unsealStoredDraftBound(ctx, sealedSnapshot, record.TenantID, record.ContractID)
	if err != nil {
		return ContractDetail{}, err
	}
	detail.Draft = new(projectDraftForViewer(plaintext, authorizeContract(record, current, access.Manage)))
	if err := tx.Commit(ctx); err != nil {
		return ContractDetail{}, err
	}
	return detail, nil
}

func clmImportWarnings(status string) []string {
	if status == "unknown" {
		return []string{"approval_history_unknown"}
	}
	return []string{}
}

// legacyAmountPair keeps the SQL pair constraint honest: unknown amount and
// unknown currency stay NULL together.
func legacyAmountPair(snapshot DraftContent) (*string, *string) {
	if snapshot.Contract == nil {
		return nil, nil
	}
	if snapshot.Contract.Amount == "" || snapshot.Contract.Currency == "" {
		return nil, nil
	}
	amount := snapshot.Contract.Amount
	currency := snapshot.Contract.Currency
	return &amount, &currency
}

// resolvedHistoricalMaterial records a verified historical approval material
// with the case identity that made the verification possible.
type resolvedHistoricalMaterial struct {
	MaterialID        string `json:"material_id"`
	MaterialVersionID string `json:"material_version_id"`
	SHA256            string `json:"sha256"`
	Purpose           string `json:"purpose"`
	CaseOrgID         string `json:"case_org_id"`
	CaseID            string `json:"case_id"`
	MaterialVersion   int    `json:"material_version"`
}

func (s *Store) resolveHistoricalMaterials(ctx context.Context, tx pgx.Tx, current accounts.User, refs []MaterialRef) ([]resolvedHistoricalMaterial, error) {
	ctx = commandContext(ctx, current)
	resolved := make([]resolvedHistoricalMaterial, 0, len(refs))
	for _, ref := range refs {
		version, err := resolveMaterialVersion(ctx, tx, accounts.Tenant(current), ref)
		if err != nil {
			return nil, err
		}
		if _, err := loadCLMCase(ctx, tx, current, version.CaseOrgID, version.CaseID); err != nil {
			return nil, err
		}
		resolved = append(resolved, resolvedHistoricalMaterial{
			MaterialID: ref.MaterialID, MaterialVersionID: ref.MaterialVersionID, SHA256: ref.SHA256,
			Purpose: ref.Purpose, CaseOrgID: version.CaseOrgID, CaseID: version.CaseID, MaterialVersion: version.Version,
		})
	}
	return resolved, nil
}

// clmRequestDigest fingerprints the verified command: route scope, normalized
// method and path, the optimistic version and the typed payload. Trace headers
// and server receive time never participate.
func clmRequestDigest(routeScope, method, path string, expectedRowVersion int64, payload any) (string, error) {
	return clmcontract.RequestDigest(routeScope, method, path, expectedRowVersion, payload)
}
