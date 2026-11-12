package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/contractmoney"
	"contractaudit/internal/fault"
)

const (
	FreezeVersionRoute  = "clm.freeze_version"
	LegacyImportRoute   = "clm.legacy_import"
	VersionFrozenEvent  = "contract.version_frozen.v1"
	LegacyImportedEvent = "contract.legacy_imported.v1"
)

type PreparedFreezeVersion struct {
	ContractID    string
	Input         clmcontract.FreezeContractVersionInput
	RequestDigest string
}

func PrepareFreezeVersion(input clmcontract.FreezeContractVersionInput) (PreparedFreezeVersion, error) {
	contractID, err := clmcontract.NormalizeContractUUID(input.ContractID)
	if err != nil {
		return PreparedFreezeVersion{}, err
	}
	if input.ExpectedRowVersion < 1 {
		return PreparedFreezeVersion{}, fault.Invalid("冻结合同版本必须携带当前合同 row_version")
	}
	if err := clmcontract.ValidateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return PreparedFreezeVersion{}, err
	}
	caseID := strings.TrimSpace(input.CaseID)
	input.CaseID = caseID
	digest, err := clmcontract.RequestDigest(FreezeVersionRoute, "POST", "/api/contracts/"+contractID+"/revisions",
		input.ExpectedRowVersion, struct {
			CaseID string `json:"case_id"`
		}{CaseID: caseID})
	if err != nil {
		return PreparedFreezeVersion{}, err
	}
	return PreparedFreezeVersion{ContractID: contractID, Input: input, RequestDigest: digest}, nil
}

type PreparedLegacyImport struct {
	Input         clmcontract.LegacyImportInput
	RequestDigest string
}

func PrepareLegacyImport(input clmcontract.LegacyImportInput) (PreparedLegacyImport, error) {
	if input.ExpectedRowVersion != 0 {
		return PreparedLegacyImport{}, fault.Invalid("存量导入必须携带 expected_row_version=0")
	}
	if input.AuthorityMode != "native" {
		return PreparedLegacyImport{}, fault.Invalid("本版只能导入 native 权威模式的存量合同")
	}
	if err := clmcontract.ValidateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return PreparedLegacyImport{}, err
	}
	if err := clmcontract.ValidateSourceRef(&input.SourceRef); err != nil {
		return PreparedLegacyImport{}, err
	}
	if err := clmcontract.ValidateMaterialRef(&input.SourceEvidence); err != nil {
		return PreparedLegacyImport{}, err
	}
	if input.ApprovalHistoryStatus != "unknown" && input.ApprovalHistoryStatus != "evidence_attached" {
		return PreparedLegacyImport{}, fault.Invalid("approval_history_status 必须是 unknown 或 evidence_attached")
	}
	if input.ApprovalHistoryStatus == "unknown" && len(input.HistoricalApprovalMaterials) > 0 {
		return PreparedLegacyImport{}, fault.Invalid("审批历史未知时不能登记历史审批材料")
	}
	if input.ApprovalHistoryStatus == "evidence_attached" && len(input.HistoricalApprovalMaterials) == 0 {
		return PreparedLegacyImport{}, fault.Invalid("登记历史审批材料时必须提供至少一项材料引用")
	}
	if len(input.HistoricalApprovalMaterials) > 100 {
		return PreparedLegacyImport{}, fault.Invalid("历史审批材料最多 100 项")
	}
	for index := range input.HistoricalApprovalMaterials {
		if err := clmcontract.ValidateMaterialRef(&input.HistoricalApprovalMaterials[index]); err != nil {
			return PreparedLegacyImport{}, err
		}
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" || utf8.RuneCountInString(reason) > 2000 {
		return PreparedLegacyImport{}, fault.Invalid("请填写存量导入原因（1 到 2000 字）")
	}
	input.Reason = reason
	clmcontract.SanitizeDraftReferences(&input.Snapshot)
	if err := clmcontract.ValidateDraftContent(&input.Snapshot); err != nil {
		return PreparedLegacyImport{}, err
	}
	if clmcontract.LegacyAmountsKnown(&input.Snapshot) {
		if err := clmcontract.ValidateDraftAmounts(&input.Snapshot, contractmoney.StageFinal); err != nil {
			return PreparedLegacyImport{}, err
		}
	}
	digest, err := clmcontract.RequestDigest(LegacyImportRoute, "POST", "/api/contracts/legacy-imports",
		input.ExpectedRowVersion, input)
	if err != nil {
		return PreparedLegacyImport{}, err
	}
	return PreparedLegacyImport{Input: input, RequestDigest: digest}, nil
}

type VersionCommandRepository interface {
	DraftCommandRepository
	LockContract(context.Context, string) error
	LoadDraftContent(context.Context, string, string) ([]byte, error)
	CanonicalizeSnapshot(context.Context, []byte) ([]byte, error)
	NextRevisionNumber(context.Context, string, string) (int, error)
	InsertFrozenRevision(context.Context, string, string, clmcontract.FrozenContractRevision) (clmcontract.FrozenContractRevision, error)
	InsertFrozenRevisionItems(context.Context, string, string, string, []clmcontract.RevisionPlanItem) error
	InsertFrozenMaterials(context.Context, string, string, string, []clmcontract.FrozenVersionMaterial) error
	InsertVersionSeal(context.Context, string, string, string, clmcontract.ContractVersionSeal) error
	AdvanceCurrentRevision(context.Context, clmcontract.Aggregate, string) (clmcontract.Aggregate, error)
	CreateLegacyAggregate(context.Context, clmcontract.Aggregate) (clmcontract.Aggregate, error)
	InsertLegacyRevision(context.Context, string, string, clmcontract.FrozenContractRevision) error
	SetLegacyCurrentRevision(context.Context, string, string, string) error
	InsertContractCaseLink(context.Context, string, string, string, string, string) error
	InsertLegacyImport(context.Context, string, string, clmcontract.LegacyImportInput, clmcontract.LegacyImportFacts, string, string) (time.Time, error)
}

type VersionQueryRepository interface {
	ListVersionSummaries(context.Context, string, string) ([]clmcontract.ContractVersionSummary, error)
	LoadFrozenRevision(context.Context, string, string, string) (clmcontract.FrozenContractRevision, error)
	LoadVersionSeal(context.Context, string, string, string) (*clmcontract.ContractVersionSeal, error)
	LoadVersionMaterials(context.Context, string, string, string) ([]clmcontract.FrozenVersionMaterial, error)
}

type VersionCodec interface {
	DraftContentCodec
	OpenForFreeze(context.Context, string, string, []byte) (clmcontract.DraftContent, error)
	FingerprintVersion(context.Context, string, string, clmcontract.DraftContent) ([]byte, string, error)
	ValidateVersionSeal([]byte) error
}

// VersionReferenceResolver is the explicit cross-domain seam for current
// source-case, subject, and material facts. Implementations must use the
// caller's already-authorized transaction and the unique strict #80 resolver.
type VersionReferenceResolver interface {
	ResolveFreezeReferences(context.Context, clmcontract.Aggregate, string, *clmcontract.DraftContent) (clmcontract.VersionReferenceFacts, error)
	ResolveLegacyImportReferences(context.Context, *clmcontract.LegacyImportInput) (clmcontract.LegacyImportFacts, error)
}

type VersionWorkflowReader = ContractWorkflowReader

type VersionAccess = DraftAccess

type VersionBusinessEventAppender interface {
	AppendContractEvent(context.Context, string, string, string, int64, string, []byte) error
}

type VersionOutboxEnqueuer interface {
	EnqueueContractEvent(context.Context, string, string) error
}

type VersionAuditBinding struct {
	GateState string
	Bound     bool
}

type VersionAuditReader interface {
	LatestVersionAuditBindings(context.Context, string, string) (map[string]VersionAuditBinding, error)
	LatestVersionAuditBinding(context.Context, string, string, string) (*VersionAuditBinding, error)
}

type VersionPrincipal = CreateDraftPrincipal

// FreezeVersion applies the complete StageFinal rule and writes the immutable
// revision graph through one caller-owned authorized transaction.
func FreezeVersion(ctx context.Context, principal VersionPrincipal, prepared PreparedFreezeVersion,
	repository VersionCommandRepository, codec VersionCodec,
	access VersionAccess, workflow VersionWorkflowReader, references VersionReferenceResolver,
	events VersionBusinessEventAppender, outbox VersionOutboxEnqueuer) (clmcontract.FreezeContractVersionResult, error) {
	input := prepared.Input
	if err := repository.LockWriteReceipt(ctx, principal.TenantID, principal.ActorID, FreezeVersionRoute, input.IdempotencyKey); err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	receipt, found, err := repository.LoadWriteReceipt(ctx, principal.TenantID, principal.ActorID, FreezeVersionRoute, input.IdempotencyKey)
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	if found {
		if receipt.RequestDigest != prepared.RequestDigest {
			return clmcontract.FreezeContractVersionResult{}, fault.Conflict("Idempotency-Key已用于不同请求载荷")
		}
		if receipt.ContractID != prepared.ContractID {
			return clmcontract.FreezeContractVersionResult{}, fault.Conflict("Idempotency-Key已用于其他合同")
		}
		record, err := repository.LoadContract(ctx, principal.TenantID, receipt.ContractID)
		if err != nil {
			return clmcontract.FreezeContractVersionResult{}, err
		}
		if !access.CanRead(ctx, record) {
			return clmcontract.FreezeContractVersionResult{}, fault.NotFound("没有找到对应记录")
		}
		var result clmcontract.FreezeContractVersionResult
		if err := clmcontract.UnmarshalStoredDraft(receipt.Response, &result); err != nil {
			return clmcontract.FreezeContractVersionResult{}, err
		}
		return result, nil
	}
	if err := repository.LockContract(ctx, prepared.ContractID); err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	record, err := repository.LoadContract(ctx, principal.TenantID, prepared.ContractID)
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	if !access.CanRead(ctx, record) {
		return clmcontract.FreezeContractVersionResult{}, fault.NotFound("没有找到对应记录")
	}
	if !access.CanManage(record) {
		return clmcontract.FreezeContractVersionResult{}, fault.Forbidden("当前授权不能冻结合同版本")
	}
	if record.AuthorityMode != "native" {
		return clmcontract.FreezeContractVersionResult{}, fault.Forbidden("外部权威合同不允许在本地冻结合同版本")
	}
	if record.LifecycleState != "draft" || record.ValidityState != "active" {
		return clmcontract.FreezeContractVersionResult{}, fault.Conflict("合同当前状态不允许冻结合同版本")
	}
	if record.RowVersion != input.ExpectedRowVersion {
		return clmcontract.FreezeContractVersionResult{}, fault.Conflict("合同内容已被他人修改，请刷新后确认")
	}
	workflowFacts, err := workflow.ReadContractWorkflow(ctx, principal.TenantID, prepared.ContractID)
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	if workflowFacts.InReview {
		return clmcontract.FreezeContractVersionResult{}, fault.Conflict("合同已有进行中的会签轮次，不能重新冻结版本")
	}
	contentBytes, err := repository.LoadDraftContent(ctx, principal.TenantID, prepared.ContractID)
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	plaintext, err := codec.OpenForFreeze(ctx, record.TenantID, record.ContractID, contentBytes)
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	if err := clmcontract.ValidateDraftContent(&plaintext); err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	if err := clmcontract.ValidateDraftAmounts(&plaintext, contractmoney.StageFinal); err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	resolved, err := references.ResolveFreezeReferences(ctx, record, input.CaseID, &plaintext)
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	materials, err := clmcontract.FrozenMaterialsFromReferences(resolved.SourceCaseOrgID, resolved.SourceCaseID, resolved.Materials)
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	sealedSnapshot, err := codec.Seal(ctx, record.TenantID, record.ContractID, plaintext)
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	sealedSnapshot, err = repository.CanonicalizeSnapshot(ctx, sealedSnapshot)
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	materialsDigest, err := jsonDigest(materials)
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	fingerprints, contentFingerprint, err := codec.FingerprintVersion(ctx, record.TenantID, record.ContractID, plaintext)
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	amount, currency := clmcontract.LegacyAmountPair(plaintext)
	revisionID, err := clmcontract.NewContractUUID()
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	revisionNumber, err := repository.NextRevisionNumber(ctx, principal.TenantID, prepared.ContractID)
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	frozen, err := repository.InsertFrozenRevision(ctx, principal.TenantID, prepared.ContractID, clmcontract.FrozenContractRevision{
		RevisionID: revisionID, RevisionNumber: revisionNumber, Amount: amount, Currency: currency,
		Snapshot: sealedSnapshot, SnapshotDigest: bytesDigest(sealedSnapshot), AmountsReconciled: true, FrozenBy: principal.ActorID,
	})
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	if err := repository.InsertFrozenRevisionItems(ctx, principal.TenantID, prepared.ContractID, revisionID,
		clmcontract.FrozenRevisionPlanItems(plaintext.Items)); err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	if err := repository.InsertFrozenMaterials(ctx, principal.TenantID, prepared.ContractID, revisionID, materials); err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	if err := repository.InsertVersionSeal(ctx, principal.TenantID, prepared.ContractID, revisionID, clmcontract.ContractVersionSeal{
		SnapshotDigest: frozen.SnapshotDigest, MaterialsDigest: materialsDigest,
		ContentFingerprint: contentFingerprint, RestrictedFingerprints: fingerprints,
	}); err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	updated, err := repository.AdvanceCurrentRevision(ctx, record, revisionID)
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	eventID, err := clmcontract.NewContractUUID()
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"contract_id": prepared.ContractID, "contract_revision_id": revisionID,
		"revision_number": revisionNumber, "snapshot_digest": frozen.SnapshotDigest,
		"materials_digest": materialsDigest, "material_count": len(materials),
	})
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	if err := events.AppendContractEvent(ctx, principal.TenantID, eventID, prepared.ContractID, updated.RowVersion, VersionFrozenEvent, payload); err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	if err := outbox.EnqueueContractEvent(ctx, principal.TenantID, eventID); err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	updated.CurrentRevisionID = &revisionID
	result := clmcontract.FreezeContractVersionResult{
		Version: clmcontract.ContractVersionSummary{
			ContractRevisionID: revisionID, RevisionNumber: revisionNumber, Amount: amount, Currency: currency,
			SnapshotDigest: frozen.SnapshotDigest, MaterialsDigest: materialsDigest, ContentFingerprint: contentFingerprint,
			AmountsReconciled: true, MaterialCount: len(materials), FrozenBy: principal.ActorID, FrozenAt: frozen.FrozenAt,
			AuditState: "unbound", AuditGateState: "not_applicable",
		},
		ContractState: updated.LifecycleState, CurrentRevisionID: revisionID, SourceCaseID: resolved.SourceCaseID,
		RowVersion: updated.RowVersion, AllowedCommands: clmcontract.DraftCommands(updated, true),
	}
	response, err := json.Marshal(result)
	if err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	if err := repository.InsertWriteReceipt(ctx, principal.TenantID, principal.ActorID, FreezeVersionRoute,
		input.IdempotencyKey, prepared.RequestDigest, prepared.ContractID, 201, response); err != nil {
		return clmcontract.FreezeContractVersionResult{}, err
	}
	return result, nil
}

func ListContractVersions(ctx context.Context, record clmcontract.Aggregate, repository VersionQueryRepository,
	audit VersionAuditReader, access VersionAccess) (clmcontract.ContractVersionList, error) {
	if !access.CanRead(ctx, record) {
		return clmcontract.ContractVersionList{}, fault.NotFound("没有找到对应记录")
	}
	items, err := repository.ListVersionSummaries(ctx, record.TenantID, record.ContractID)
	if err != nil {
		return clmcontract.ContractVersionList{}, err
	}
	bindings, err := audit.LatestVersionAuditBindings(ctx, record.TenantID, record.ContractID)
	if err != nil {
		return clmcontract.ContractVersionList{}, err
	}
	for i := range items {
		applyAuditState(&items[i], record.CurrentRevisionID, bindings[items[i].ContractRevisionID])
	}
	return clmcontract.ContractVersionList{Items: items}, nil
}

func LoadContractVersion(ctx context.Context, record clmcontract.Aggregate, revisionID string,
	repository VersionQueryRepository, audit VersionAuditReader, codec VersionCodec, access VersionAccess) (clmcontract.ContractVersionDetail, error) {
	if !access.CanRead(ctx, record) {
		return clmcontract.ContractVersionDetail{}, fault.NotFound("没有找到对应记录")
	}
	frozen, err := repository.LoadFrozenRevision(ctx, record.TenantID, record.ContractID, revisionID)
	if err != nil {
		return clmcontract.ContractVersionDetail{}, err
	}
	seal, err := repository.LoadVersionSeal(ctx, record.TenantID, record.ContractID, revisionID)
	if err != nil {
		return clmcontract.ContractVersionDetail{}, err
	}
	materials, err := repository.LoadVersionMaterials(ctx, record.TenantID, record.ContractID, revisionID)
	if err != nil {
		return clmcontract.ContractVersionDetail{}, err
	}
	if seal != nil {
		if err := codec.ValidateVersionSeal(seal.RestrictedFingerprints); err != nil {
			return clmcontract.ContractVersionDetail{}, err
		}
		materialsDigest, err := jsonDigest(materials)
		if err != nil {
			return clmcontract.ContractVersionDetail{}, err
		}
		if seal.SnapshotDigest != frozen.SnapshotDigest || bytesDigest(frozen.Snapshot) != frozen.SnapshotDigest ||
			seal.MaterialsDigest != materialsDigest {
			return clmcontract.ContractVersionDetail{}, fault.Conflict("冻结版本的摘要与封印不一致")
		}
	}
	detail := clmcontract.ContractVersionDetail{
		Version: clmcontract.ContractVersionSummary{
			ContractRevisionID: frozen.RevisionID, RevisionNumber: frozen.RevisionNumber,
			Amount: frozen.Amount, Currency: frozen.Currency, SnapshotDigest: frozen.SnapshotDigest,
			AmountsReconciled: frozen.AmountsReconciled, MaterialCount: len(materials),
			FrozenBy: frozen.FrozenBy, FrozenAt: frozen.FrozenAt,
		}, Materials: make([]clmcontract.ContractVersionMaterial, 0, len(materials)),
	}
	if seal != nil {
		detail.Version.MaterialsDigest = seal.MaterialsDigest
		detail.Version.ContentFingerprint = seal.ContentFingerprint
	}
	binding, err := audit.LatestVersionAuditBinding(ctx, record.TenantID, record.ContractID, revisionID)
	if err != nil {
		return clmcontract.ContractVersionDetail{}, err
	}
	applyAuditState(&detail.Version, record.CurrentRevisionID, bindingValue(binding))
	for _, material := range materials {
		detail.Materials = append(detail.Materials, clmcontract.ContractVersionMaterial{
			MaterialID: material.MaterialID, MaterialVersionID: material.MaterialVersionID,
			MaterialVersion: material.MaterialVersion, Filename: material.Filename,
			Digest: material.Digest, Purpose: material.Purpose,
			CaseID: material.CaseID, CaseOrgID: material.CaseOrgID,
		})
	}
	plaintext, err := codec.Open(ctx, record.TenantID, record.ContractID, frozen.Snapshot)
	if err != nil {
		return clmcontract.ContractVersionDetail{}, err
	}
	projected := codec.Project(plaintext, access.CanManage(record))
	detail.Draft = &projected
	return detail, nil
}

func ImportLegacySignedContract(ctx context.Context, principal VersionPrincipal, prepared PreparedLegacyImport,
	repository VersionCommandRepository, codec VersionCodec, access VersionAccess, references VersionReferenceResolver,
	events VersionBusinessEventAppender, outbox VersionOutboxEnqueuer) (clmcontract.DraftDetail, error) {
	input := prepared.Input
	if err := repository.LockWriteReceipt(ctx, principal.TenantID, principal.ActorID, LegacyImportRoute, input.IdempotencyKey); err != nil {
		return clmcontract.DraftDetail{}, err
	}
	receipt, found, err := repository.LoadWriteReceipt(ctx, principal.TenantID, principal.ActorID, LegacyImportRoute, input.IdempotencyKey)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	if found {
		if receipt.RequestDigest != prepared.RequestDigest {
			return clmcontract.DraftDetail{}, fault.Conflict("Idempotency-Key已用于不同请求载荷")
		}
		record, err := repository.LoadContract(ctx, principal.TenantID, receipt.ContractID)
		if err != nil {
			return clmcontract.DraftDetail{}, err
		}
		if !access.CanRead(ctx, record) {
			return clmcontract.DraftDetail{}, fault.NotFound("没有找到对应记录")
		}
		var result clmcontract.DraftDetail
		if err := clmcontract.UnmarshalStoredDraft(receipt.Response, &result); err != nil {
			return clmcontract.DraftDetail{}, err
		}
		if result.Draft != nil {
			sealedBytes, err := json.Marshal(result.Draft)
			if err != nil {
				return clmcontract.DraftDetail{}, err
			}
			plaintext, err := codec.Open(ctx, record.TenantID, record.ContractID, sealedBytes)
			if err != nil {
				return clmcontract.DraftDetail{}, err
			}
			projected := codec.Project(plaintext, access.CanManage(record))
			result.Draft = &projected
		}
		return result, nil
	}
	facts, err := references.ResolveLegacyImportReferences(ctx, &input)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	contractID, err := clmcontract.NewContractUUID()
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	sealedSnapshot, err := codec.Seal(ctx, principal.TenantID, contractID, input.Snapshot)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	sealedSnapshot, err = repository.CanonicalizeSnapshot(ctx, sealedSnapshot)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	if err := repository.LockContract(ctx, contractID); err != nil {
		return clmcontract.DraftDetail{}, err
	}
	revisionID, err := clmcontract.NewContractUUID()
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	var contractNumber *string
	if input.Snapshot.Contract != nil && input.Snapshot.Contract.Number != nil {
		contractNumber = clmcontract.OptionalText(*input.Snapshot.Contract.Number)
	}
	amount, currency := clmcontract.LegacyAmountPair(input.Snapshot)
	record, err := repository.CreateLegacyAggregate(ctx, clmcontract.Aggregate{
		TenantID: principal.TenantID, ContractID: contractID, OwnerOrgID: principal.OrgID,
		AuthorityMode: "native", ContractNumber: contractNumber, SourceSystem: new(input.SourceRef.System),
		ExternalContractID: new(input.SourceRef.ID), SourceVersion: new(input.SourceRef.SourceVersion),
		LifecycleState: "registered", ValidityState: "active", CurrentRevisionID: new(revisionID),
		RowVersion: 1, CreatedBy: principal.ActorID,
	})
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	revision := clmcontract.FrozenContractRevision{
		RevisionID: revisionID, RevisionNumber: 1, Amount: amount, Currency: currency,
		Snapshot: sealedSnapshot, SnapshotDigest: bytesDigest(sealedSnapshot), FrozenBy: principal.ActorID,
		AmountsReconciled: clmcontract.LegacyAmountsKnown(&input.Snapshot),
	}
	if err := repository.InsertLegacyRevision(ctx, principal.TenantID, contractID, revision); err != nil {
		return clmcontract.DraftDetail{}, err
	}
	if err := repository.InsertFrozenRevisionItems(ctx, principal.TenantID, contractID, revisionID,
		clmcontract.FrozenRevisionPlanItems(input.Snapshot.Items)); err != nil {
		return clmcontract.DraftDetail{}, err
	}
	if err := repository.SetLegacyCurrentRevision(ctx, principal.TenantID, contractID, revisionID); err != nil {
		return clmcontract.DraftDetail{}, err
	}
	if err := repository.InsertContractCaseLink(ctx, principal.TenantID, contractID,
		facts.SourceCaseOrgID, facts.SourceCaseID, principal.ActorID); err != nil {
		return clmcontract.DraftDetail{}, err
	}
	importedAt, err := repository.InsertLegacyImport(ctx, principal.TenantID, contractID, input, facts, revisionID, principal.ActorID)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	eventID, err := clmcontract.NewContractUUID()
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"contract_id": contractID, "contract_revision_id": revisionID,
		"source_system": input.SourceRef.System, "source_id": input.SourceRef.ID,
		"source_version": input.SourceRef.SourceVersion, "case_org_id": facts.SourceEvidence.CaseOrgID,
		"case_id": facts.SourceEvidence.CaseID, "material_id": facts.SourceEvidence.MaterialID,
		"material_version": facts.SourceEvidence.MaterialVersion, "material_digest": facts.SourceEvidence.Digest,
		"approval_history_status":   input.ApprovalHistoryStatus,
		"historical_material_count": len(facts.Historical),
	})
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	if err := events.AppendContractEvent(ctx, principal.TenantID, eventID, contractID, 1, LegacyImportedEvent, payload); err != nil {
		return clmcontract.DraftDetail{}, err
	}
	if err := outbox.EnqueueContractEvent(ctx, principal.TenantID, eventID); err != nil {
		return clmcontract.DraftDetail{}, err
	}
	name, detailAmount, detailCurrency, err := clmcontract.DraftSummaryFacts(sealedSnapshot)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	detail := clmcontract.DraftDetail{
		Summary:           clmcontract.SummaryFromDraft(record, name, detailAmount, detailCurrency),
		CurrentRevisionID: new(revisionID), AllowedCommands: clmcontract.DraftCommands(record, true),
		SourceRef:        &clmcontract.SourceRef{System: input.SourceRef.System, ID: input.SourceRef.ID, SourceVersion: input.SourceRef.SourceVersion},
		SourceObservedAt: &importedAt, Warnings: legacyImportWarnings(input.ApprovalHistoryStatus),
	}
	response, err := json.Marshal(detail)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	if err := repository.InsertWriteReceipt(ctx, principal.TenantID, principal.ActorID, LegacyImportRoute,
		input.IdempotencyKey, prepared.RequestDigest, contractID, 201, response); err != nil {
		return clmcontract.DraftDetail{}, err
	}
	plaintext, err := codec.Open(ctx, record.TenantID, record.ContractID, sealedSnapshot)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	projected := codec.Project(plaintext, access.CanManage(record))
	detail.Draft = &projected
	return detail, nil
}

func legacyImportWarnings(status string) []string {
	if status == "unknown" {
		return []string{"approval_history_unknown"}
	}
	return []string{}
}

func applyAuditState(summary *clmcontract.ContractVersionSummary, currentRevisionID *string, binding VersionAuditBinding) {
	if !binding.Bound {
		summary.AuditState, summary.AuditGateState = "unbound", "not_applicable"
		return
	}
	if currentRevisionID == nil || *currentRevisionID != summary.ContractRevisionID {
		summary.AuditState, summary.AuditGateState = "stale", binding.GateState
		return
	}
	summary.AuditState, summary.AuditGateState = "current", binding.GateState
}

func bindingValue(binding *VersionAuditBinding) VersionAuditBinding {
	if binding == nil {
		return VersionAuditBinding{}
	}
	return *binding
}

func jsonDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func bytesDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
