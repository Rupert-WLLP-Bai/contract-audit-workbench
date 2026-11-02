package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/contractmoney"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

const (
	clmRouteRegisterSigning    = "clm.register_signing"
	clmRouteCreateSupplement   = "clm.create_supplement"
	clmEventSigningRegistered  = "contract.signing_registered.v1"
	clmEventSupplementCreated  = "contract.supplement_created.v1"
	clmSigningComparisonMethod = "structured_signed_terms/v1"
	clmSigningComparisonScope  = "all_structured_business_fields_and_restricted_fields"
)

type ContractSigningParty struct {
	Role string `json:"role"`
	Name string `json:"name"`
}

type ContractSigningCheckInput struct {
	ContractID          string                  `json:"-"`
	BaselineRevisionID  string                  `json:"baseline_revision_id"`
	SignedEvidence      MaterialRef             `json:"signed_evidence"`
	SignedContent       DraftContent            `json:"signed_content"`
	ManualComparison    ManualSigningComparison `json:"manual_comparison"`
	LegacyApprovalBasis string                  `json:"legacy_approval_basis,omitempty"`
}

// ManualSigningComparison is an explicit human attestation that the listed
// structured transcription was checked against this exact source digest and
// immutable approved revision. It is not an OCR or signature verification.
type ManualSigningComparison struct {
	Confirmed            bool   `json:"confirmed"`
	SignedMaterialDigest string `json:"signed_material_digest"`
	ContractRevisionID   string `json:"contract_revision_id"`
	Scope                string `json:"scope"`
}

type ContractSigningCheckResult struct {
	ContractID         string                            `json:"contract_id"`
	BaselineRevisionID string                            `json:"baseline_revision_id"`
	ComparisonMethod   string                            `json:"comparison_method"`
	ComparisonResult   string                            `json:"comparison_result"`
	CanRegister        bool                              `json:"can_register"`
	VerificationStatus string                            `json:"verification_status"`
	ApprovalStatus     string                            `json:"approval_status"`
	Fields             []ContractVersionFieldChange      `json:"fields"`
	RestrictedFields   []ContractVersionRestrictedChange `json:"restricted_fields"`
	Warnings           []string                          `json:"warnings"`
}

type ContractSigningRegistrationInput struct {
	ContractID              string                  `json:"-"`
	ExpectedRowVersion      int64                   `json:"expected_row_version"`
	BaselineRevisionID      string                  `json:"baseline_revision_id"`
	SigningDate             string                  `json:"signing_date"`
	SignedEvidence          MaterialRef             `json:"signed_evidence"`
	SignedContent           DraftContent            `json:"signed_content"`
	ManualComparison        ManualSigningComparison `json:"manual_comparison"`
	LegacyApprovalBasis     string                  `json:"legacy_approval_basis,omitempty"`
	Parties                 []ContractSigningParty  `json:"parties"`
	SignatureEvidenceStatus string                  `json:"signature_evidence_status,omitempty"`
	SignatureEvidenceRef    string                  `json:"signature_evidence_ref,omitempty"`
	IdempotencyKey          string                  `json:"-"`
}

type ContractSigningRegistration struct {
	RegistrationID          string                  `json:"registration_id"`
	ContractID              string                  `json:"contract_id"`
	ContractRevisionID      string                  `json:"contract_revision_id"`
	SigningDate             string                  `json:"signing_date"`
	SignedMaterial          ContractVersionMaterial `json:"signed_material"`
	Parties                 []ContractSigningParty  `json:"parties"`
	SignatureEvidenceStatus string                  `json:"signature_evidence_status"`
	SignatureEvidenceRef    string                  `json:"signature_evidence_ref,omitempty"`
	ApprovalBasis           string                  `json:"approval_basis"`
	ApprovalBasisNote       string                  `json:"approval_basis_note,omitempty"`
	ApprovalEvidenceStatus  string                  `json:"approval_evidence_status"`
	ApprovalRoundID         *string                 `json:"approval_round_id"`
	ComparisonMethod        string                  `json:"comparison_method"`
	ComparisonResult        string                  `json:"comparison_result"`
	VerificationStatus      string                  `json:"verification_status"`
	ComparisonConfirmedBy   string                  `json:"comparison_confirmed_by"`
	ComparisonConfirmedAt   time.Time               `json:"comparison_confirmed_at"`
	ComparisonScope         string                  `json:"comparison_scope"`
	Warnings                []string                `json:"warnings"`
	RegisteredBy            string                  `json:"registered_by"`
	RegisteredAt            time.Time               `json:"registered_at"`
	ContractState           string                  `json:"contract_state,omitempty"`
	RowVersion              int64                   `json:"row_version,omitempty"`
}

type ContractSigningRegistrationList struct {
	Items []ContractSigningRegistration `json:"items"`
}

type CreateContractSupplementInput struct {
	ParentContractID   string       `json:"-"`
	ExpectedRowVersion int64        `json:"expected_row_version"`
	ParentRevisionID   string       `json:"parent_revision_id"`
	RelationType       string       `json:"relation_type"`
	Reason             string       `json:"reason"`
	Draft              DraftContent `json:"draft"`
	IdempotencyKey     string       `json:"-"`
}

type ContractSupplement struct {
	ParentContractID     string    `json:"parent_contract_id"`
	SupplementContractID string    `json:"supplement_contract_id"`
	ParentRevisionID     string    `json:"parent_revision_id"`
	RelationType         string    `json:"relation_type"`
	Reason               string    `json:"reason"`
	LinkedBy             string    `json:"linked_by"`
	LinkedAt             time.Time `json:"linked_at"`
	ContractState        string    `json:"contract_state"`
	SigningDate          *string   `json:"signing_date,omitempty"`
}

type ContractSupplementResult struct {
	Supplement ContractSupplement `json:"supplement"`
	Contract   ContractDetail     `json:"contract"`
}

type ContractSupplementList struct {
	Parent *ContractSupplement  `json:"parent,omitempty"`
	Items  []ContractSupplement `json:"items"`
}

func validateSigningDate(value string, now time.Time) error {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Format("2006-01-02") != value {
		return fault.Invalid("signing_date 必须为 YYYY-MM-DD")
	}
	if parsed.Before(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)) || parsed.After(now.UTC().Truncate(24*time.Hour)) {
		return fault.Invalid("signing_date 必须在 1900-01-01 至服务器当天之间")
	}
	return nil
}

func normalizeSigningInput(input *ContractSigningRegistrationInput) error {
	if input.ExpectedRowVersion < 1 {
		return fault.Invalid("签署登记必须携带当前合同 row_version")
	}
	if err := validateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return err
	}
	if err := normalizeContractUUIDField("baseline_revision_id", &input.BaselineRevisionID); err != nil {
		return err
	}
	if err := validateSigningDate(input.SigningDate, time.Now()); err != nil {
		return err
	}
	if err := validateMaterialRef(&input.SignedEvidence); err != nil {
		return err
	}
	if input.SignedEvidence.Purpose != "signed_contract" {
		return fault.Invalid("签署原件材料 purpose 必须为 signed_contract")
	}
	if err := validateManualSigningComparison(input.ManualComparison, input.BaselineRevisionID); err != nil {
		return err
	}
	input.LegacyApprovalBasis = strings.TrimSpace(input.LegacyApprovalBasis)
	if runeCount(input.LegacyApprovalBasis) > 2000 {
		return fault.Invalid("legacy_approval_basis 最多 2000 个字符")
	}
	if input.SignatureEvidenceStatus == "" {
		input.SignatureEvidenceStatus = "document_only"
	}
	switch input.SignatureEvidenceStatus {
	case "unknown", "document_only":
		if strings.TrimSpace(input.SignatureEvidenceRef) != "" {
			return fault.Invalid("signature_evidence_ref 仅可用于 external_evidence")
		}
		input.SignatureEvidenceRef = ""
	case "external_evidence":
		input.SignatureEvidenceRef = strings.TrimSpace(input.SignatureEvidenceRef)
		if input.SignatureEvidenceRef == "" || runeCount(input.SignatureEvidenceRef) > 500 {
			return fault.Invalid("external_evidence 必须提供不超过 500 字的外部证据引用")
		}
	default:
		return fault.Invalid("signature_evidence_status 必须为 unknown、document_only 或 external_evidence")
	}
	if len(input.Parties) < 1 || len(input.Parties) > 20 {
		return fault.Invalid("签署各方必须为 1 到 20 项")
	}
	for index := range input.Parties {
		party := &input.Parties[index]
		party.Role = strings.TrimSpace(party.Role)
		party.Name = strings.TrimSpace(party.Name)
		if party.Role != "buyer" && party.Role != "supplier" && party.Role != "witness" && party.Role != "other" {
			return fault.Invalid("签署方 role 必须为 buyer、supplier、witness 或 other")
		}
		if party.Name == "" || runeCount(party.Name) > 200 {
			return fault.Invalid("签署方 name 必须为 1 到 200 个字符")
		}
	}
	sanitizeDraftReferences(&input.SignedContent)
	if err := validateDraftContent(&input.SignedContent); err != nil {
		return err
	}
	return validateDraftAmounts(&input.SignedContent, contractmoneyStageForSigning())
}

func validateManualSigningComparison(confirmation ManualSigningComparison, revisionID string) error {
	if !confirmation.Confirmed {
		return fault.Invalid("必须由授权人员确认已对照签署原件逐项核对结构化转录")
	}
	if err := normalizeContractUUIDField("manual_comparison.contract_revision_id", &confirmation.ContractRevisionID); err != nil {
		return err
	}
	if confirmation.ContractRevisionID != revisionID {
		return fault.Invalid("人工核对确认必须绑定同一不可变合同修订")
	}
	confirmation.SignedMaterialDigest = strings.ToLower(strings.TrimSpace(confirmation.SignedMaterialDigest))
	if len(confirmation.SignedMaterialDigest) != 64 || !lowerHexDigest(confirmation.SignedMaterialDigest) {
		return fault.Invalid("人工核对确认必须绑定签署原件 SHA-256")
	}
	if confirmation.Scope != clmSigningComparisonScope {
		return fault.Invalid("人工核对范围必须覆盖全部结构化业务字段及受限字段")
	}
	return nil
}

func lowerHexDigest(value string) bool {
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func contractmoneyStageForSigning() contractmoney.Stage {
	return contractmoney.StageFinal
}

func normalizeContractUUIDField(field string, value *string) error {
	normalized, err := normalizeContractUUID(*value)
	if err != nil {
		return fault.Invalid(field + " 必须是有效 UUID")
	}
	*value = normalized
	return nil
}

func signingComparableFields(draft DraftContent) map[string]any {
	// The same canonical flattener used by immutable-version diffing covers all
	// structured business fields, including performance, assets, funding,
	// guarantees, attachments and party details. Server-pinned snapshots and
	// restricted fields retain their existing dedicated handling.
	return flattenContractDraft(draft)
}

func compareSignedTerms(ctx context.Context, tx pgx.Tx, current accounts.User, record clmContractRecord,
	revisionID string, signed DraftContent) (ContractSigningCheckResult, error) {
	frozen, err := loadFrozenContractVersion(ctx, tx, accounts.Tenant(current), record.ContractID, revisionID)
	if err != nil {
		return ContractSigningCheckResult{}, err
	}
	seal, err := loadContractVersionSeal(ctx, tx, accounts.Tenant(current), record.ContractID, revisionID)
	if err != nil {
		return ContractSigningCheckResult{}, err
	}
	if seal == nil {
		return ContractSigningCheckResult{}, fault.Conflict("冻结版本缺少完整封印，无法安全核对签署稿")
	}
	if sha256Bytes(frozen.Snapshot) != frozen.SnapshotDigest || seal.SnapshotDigest != frozen.SnapshotDigest {
		return ContractSigningCheckResult{}, fault.Conflict("冻结版本摘要与封印不一致")
	}
	materials, err := loadContractVersionMaterials(ctx, tx, accounts.Tenant(current), record.ContractID, revisionID)
	if err != nil {
		return ContractSigningCheckResult{}, err
	}
	materialsDigest, err := canonicalHash(materials)
	if err != nil {
		return ContractSigningCheckResult{}, err
	}
	if seal.MaterialsDigest != materialsDigest {
		return ContractSigningCheckResult{}, fault.Conflict("冻结版本材料摘要与封印不一致")
	}
	var baseline DraftContent
	if err := unmarshalStoredDraft(frozen.Snapshot, &baseline); err != nil {
		return ContractSigningCheckResult{}, err
	}
	fields := diffFrozenFields(signingComparableFields(baseline), signingComparableFields(signed))
	signedFingerprints, err := computeRestrictedFingerprints(&signed)
	if err != nil {
		return ContractSigningCheckResult{}, err
	}
	signedSeal := &contractVersionSeal{RestrictedFingerprints: signedFingerprints}
	restricted := diffRestrictedFields(true, seal, signedSeal)
	result := ContractSigningCheckResult{
		ContractID: record.ContractID, BaselineRevisionID: revisionID,
		ComparisonMethod: clmSigningComparisonMethod, ComparisonResult: "transcription_matches_revision", CanRegister: true,
		VerificationStatus: "not_verified_against_source",
		Fields:             fields, RestrictedFields: restricted, Warnings: []string{},
	}
	for _, change := range restricted {
		if change.Changed == nil {
			result.ComparisonResult = "comparison_unavailable"
			result.CanRegister = false
			break
		}
		if *change.Changed {
			result.ComparisonResult = "material_difference"
			result.CanRegister = false
		}
	}
	if len(fields) > 0 && result.CanRegister {
		result.ComparisonResult = "material_difference"
		result.CanRegister = false
	}
	return result, nil
}

func (s *Store) CheckContractSigning(ctx context.Context, user accounts.User, input ContractSigningCheckInput) (ContractSigningCheckResult, error) {
	ctx = commandContext(ctx, user)
	id, err := normalizeContractUUID(input.ContractID)
	if err != nil {
		return ContractSigningCheckResult{}, err
	}
	if err := normalizeContractUUIDField("baseline_revision_id", &input.BaselineRevisionID); err != nil {
		return ContractSigningCheckResult{}, err
	}
	if err := validateMaterialRef(&input.SignedEvidence); err != nil {
		return ContractSigningCheckResult{}, err
	}
	if input.SignedEvidence.Purpose != "signed_contract" {
		return ContractSigningCheckResult{}, fault.Invalid("签署原件材料 purpose 必须为 signed_contract")
	}
	if err := validateManualSigningComparison(input.ManualComparison, input.BaselineRevisionID); err != nil {
		return ContractSigningCheckResult{}, err
	}
	input.LegacyApprovalBasis = strings.TrimSpace(input.LegacyApprovalBasis)
	if runeCount(input.LegacyApprovalBasis) > 2000 {
		return ContractSigningCheckResult{}, fault.Invalid("legacy_approval_basis 最多 2000 个字符")
	}
	sanitizeDraftReferences(&input.SignedContent)
	if err := validateDraftContent(&input.SignedContent); err != nil {
		return ContractSigningCheckResult{}, err
	}
	if err := validateDraftAmounts(&input.SignedContent, contractmoneyStageForSigning()); err != nil {
		return ContractSigningCheckResult{}, err
	}
	if !s.HasDatabase() {
		return ContractSigningCheckResult{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return ContractSigningCheckResult{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ContractSigningCheckResult{}, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ContractSigningCheckResult{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ContractSigningCheckResult{}, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return ContractSigningCheckResult{}, fault.NotFound("没有找到对应记录")
	}
	if !authorizeContract(record, current, access.Manage) {
		return ContractSigningCheckResult{}, fault.Forbidden("当前授权不能核对合同签署内容")
	}
	if record.AuthorityMode != "native" {
		return ContractSigningCheckResult{}, fault.Forbidden("外部权威合同不能在本地核对签署内容")
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != input.BaselineRevisionID {
		return ContractSigningCheckResult{}, fault.Conflict("基线必须是合同当前冻结版本")
	}
	evidence, err := resolveMaterialVersion(ctx, tx, accounts.Tenant(current), input.SignedEvidence)
	if err != nil {
		return ContractSigningCheckResult{}, err
	}
	if err := requireSigningCaseRead(ctx, tx, current, evidence.CaseOrgID, evidence.CaseID); err != nil {
		return ContractSigningCheckResult{}, err
	}
	if input.ManualComparison.SignedMaterialDigest != evidence.Digest {
		return ContractSigningCheckResult{}, fault.Conflict("人工核对确认中的原件摘要与授权材料版本不一致")
	}
	result, err := compareSignedTerms(ctx, tx, current, record, input.BaselineRevisionID, input.SignedContent)
	if err != nil {
		return ContractSigningCheckResult{}, err
	}
	if record.ValidityState != "active" || (record.LifecycleState != "draft" && record.LifecycleState != "ready_to_sign" && record.LifecycleState != "registered") {
		result.CanRegister = false
		result.Warnings = append(result.Warnings, "contract_state_not_registerable")
	}
	var inReview bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_approval_rounds
		WHERE tenant_id=$1 AND contract_id=$2 AND state='in_review')`, accounts.Tenant(current), id).Scan(&inReview); err != nil {
		return ContractSigningCheckResult{}, err
	}
	if inReview {
		result.CanRegister = false
		result.Warnings = append(result.Warnings, "approval_round_in_review")
	}
	var latestState string
	approvalErr := tx.QueryRow(ctx, `SELECT state FROM clm_approval_rounds WHERE tenant_id=$1 AND contract_id=$2
		AND contract_revision_id=$3 ORDER BY round_number DESC LIMIT 1`, accounts.Tenant(current), id, input.BaselineRevisionID).Scan(&latestState)
	switch {
	case approvalErr == nil && latestState == "approved":
		result.ApprovalStatus = "approved"
	case approvalErr == nil:
		result.ApprovalStatus = "negative_history"
		result.CanRegister = false
		result.Warnings = append(result.Warnings, "approval_negative_history")
	case errors.Is(approvalErr, pgx.ErrNoRows):
		var legacyExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_legacy_imports WHERE tenant_id=$1 AND contract_id=$2
			AND contract_revision_id=$3 AND approval_history_status='unknown')`, accounts.Tenant(current), id,
			input.BaselineRevisionID).Scan(&legacyExists); err != nil {
			return ContractSigningCheckResult{}, err
		}
		if legacyExists && strings.TrimSpace(input.LegacyApprovalBasis) != "" {
			result.ApprovalStatus = "legacy_history_unknown_with_basis"
		} else {
			result.ApprovalStatus = "no_history"
			result.CanRegister = false
			result.Warnings = append(result.Warnings, "approval_history_required")
		}
	default:
		return ContractSigningCheckResult{}, approvalErr
	}
	if record.LifecycleState == "registered" && result.ApprovalStatus != "legacy_history_unknown_with_basis" {
		result.CanRegister = false
		result.Warnings = append(result.Warnings, "registered_state_requires_legacy_import")
	}
	if result.CanRegister {
		result.VerificationStatus = "human_transcription_confirmed"
		result.Warnings = append(result.Warnings, "source_content_not_machine_verified", "signature_authenticity_not_verified")
	} else {
		result.VerificationStatus = "not_verified_against_source"
		result.Warnings = append(result.Warnings, "manual_source_comparison_required")
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractSigningCheckResult{}, err
	}
	return result, nil
}

func (s *Store) RegisterContractSigning(ctx context.Context, user accounts.User, input ContractSigningRegistrationInput) (ContractSigningRegistration, error) {
	ctx = commandContext(ctx, user)
	id, err := normalizeContractUUID(input.ContractID)
	if err != nil {
		return ContractSigningRegistration{}, err
	}
	input.ContractID = id
	if err := normalizeSigningInput(&input); err != nil {
		return ContractSigningRegistration{}, err
	}
	digest, err := clmRequestDigest(clmRouteRegisterSigning, "POST", "/api/contracts/"+id+"/registrations", input.ExpectedRowVersion, input)
	if err != nil {
		return ContractSigningRegistration{}, err
	}
	_, sealedTerms, _, err := sealDraftContent(input.SignedContent)
	if err != nil {
		return ContractSigningRegistration{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能登记签署合同", nil)
	if err != nil {
		return ContractSigningRegistration{}, err
	}
	defer rollbackCLM(tx)
	if err := lockWriteReceipt(ctx, tx, current, clmRouteRegisterSigning, input.IdempotencyKey); err != nil {
		return ContractSigningRegistration{}, err
	}
	receipt, found, err := replayCLMWriteReceipt(ctx, tx, current, clmRouteRegisterSigning, input.IdempotencyKey, digest, id)
	if err != nil {
		return ContractSigningRegistration{}, err
	}
	if found {
		var result ContractSigningRegistration
		if err := json.Unmarshal(receipt.Response, &result); err != nil {
			return ContractSigningRegistration{}, err
		}
		record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
		if err != nil || !authorizeContractRead(ctx, tx, record, current) {
			return ContractSigningRegistration{}, fault.NotFound("没有找到对应记录")
		}
		if err := requireSigningCaseRead(ctx, tx, current, result.SignedMaterial.CaseOrgID, result.SignedMaterial.CaseID); err != nil {
			return ContractSigningRegistration{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ContractSigningRegistration{}, err
		}
		return result, nil
	}
	if err := scopedContractLock(ctx, tx, id); err != nil {
		return ContractSigningRegistration{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ContractSigningRegistration{}, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return ContractSigningRegistration{}, fault.NotFound("没有找到对应记录")
	}
	if !authorizeContract(record, current, access.Manage) {
		return ContractSigningRegistration{}, fault.Forbidden("当前授权不能登记签署合同")
	}
	if record.AuthorityMode != "native" {
		return ContractSigningRegistration{}, fault.Forbidden("外部权威合同不能在本地登记")
	}
	if record.ValidityState != "active" || (record.LifecycleState != "draft" && record.LifecycleState != "ready_to_sign" && record.LifecycleState != "registered") {
		return ContractSigningRegistration{}, fault.Conflict("合同当前状态不允许登记签署")
	}
	if record.RowVersion != input.ExpectedRowVersion {
		return ContractSigningRegistration{}, fault.Conflict("合同已被修改，请刷新后重试")
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != input.BaselineRevisionID {
		return ContractSigningRegistration{}, fault.Conflict("基线必须是合同当前冻结版本")
	}
	var inReview bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_approval_rounds
		WHERE tenant_id=$1 AND contract_id=$2 AND state='in_review')`, accounts.Tenant(current), id).Scan(&inReview); err != nil {
		return ContractSigningRegistration{}, err
	}
	if inReview {
		return ContractSigningRegistration{}, fault.Conflict("会签尚未结束，不能登记签署")
	}
	comparison, err := compareSignedTerms(ctx, tx, current, record, input.BaselineRevisionID, input.SignedContent)
	if err != nil {
		return ContractSigningRegistration{}, err
	}
	if !comparison.CanRegister {
		paths := make([]string, 0, len(comparison.Fields)+len(comparison.RestrictedFields))
		for _, field := range comparison.Fields {
			paths = append(paths, field.Path)
		}
		for _, field := range comparison.RestrictedFields {
			if field.Changed == nil || *field.Changed {
				paths = append(paths, field.Path)
			}
		}
		sort.Strings(paths)
		if comparison.ComparisonResult == "comparison_unavailable" {
			return ContractSigningRegistration{}, fault.Conflict("受限字段无法安全比较，不能登记签署合同")
		}
		return ContractSigningRegistration{}, fault.Conflict("签署内容存在实质差异：" + strings.Join(paths, ", "))
	}
	evidence, err := resolveMaterialVersion(ctx, tx, accounts.Tenant(current), input.SignedEvidence)
	if err != nil {
		return ContractSigningRegistration{}, err
	}
	caseRecord, err := loadCLMCase(ctx, tx, current, evidence.CaseOrgID, evidence.CaseID)
	if err != nil {
		return ContractSigningRegistration{}, err
	}
	if input.ManualComparison.SignedMaterialDigest != evidence.Digest {
		return ContractSigningRegistration{}, fault.Conflict("人工核对确认中的原件摘要与授权材料版本不一致")
	}
	sealedTerms, err = canonicalStoredJSON(ctx, tx, sealedTerms)
	if err != nil {
		return ContractSigningRegistration{}, err
	}
	var approvalRoundID *string
	var approvalBasis, approvalStatus string
	var approvedRound, latestApprovalState string
	err = tx.QueryRow(ctx, `SELECT approval_round_id,state FROM clm_approval_rounds
		WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3
		ORDER BY round_number DESC LIMIT 1`, accounts.Tenant(current), id, input.BaselineRevisionID).Scan(&approvedRound, &latestApprovalState)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ContractSigningRegistration{}, err
	}
	if err == nil && latestApprovalState == "approved" {
		approvalRoundID, approvalBasis, approvalStatus = &approvedRound, "approved_round", "approved_round"
	} else if err == nil {
		return ContractSigningRegistration{}, fault.Conflict("该不可变修订存在退回、撤回或终止会签历史，不能登记签署")
	} else {
		var importedRevision, importedStatus string
		importErr := tx.QueryRow(ctx, `SELECT contract_revision_id::text,approval_history_status FROM clm_legacy_imports
			WHERE tenant_id=$1 AND contract_id=$2`, accounts.Tenant(current), id).Scan(&importedRevision, &importedStatus)
		if errors.Is(importErr, pgx.ErrNoRows) || importedRevision != input.BaselineRevisionID ||
			importedStatus != "unknown" || input.LegacyApprovalBasis == "" {
			return ContractSigningRegistration{}, fault.Conflict("仅可登记已批准修订；历史未知的旧合同须有真实导入记录及明确人工依据")
		}
		if importErr != nil && !errors.Is(importErr, pgx.ErrNoRows) {
			return ContractSigningRegistration{}, importErr
		}
		approvalBasis, approvalStatus = "legacy_manual_basis", "legacy_unknown_with_manual_basis"
	}
	if record.LifecycleState == "registered" && approvalBasis != "legacy_manual_basis" {
		return ContractSigningRegistration{}, fault.Conflict("已登记合同仅允许真实历史导入且历史审批未知的签署补录")
	}
	registrationID, err := newContractUUID()
	if err != nil {
		return ContractSigningRegistration{}, err
	}
	var rowVersion int64
	err = tx.QueryRow(ctx, `UPDATE clm_contracts SET lifecycle_state='registered', row_version=row_version+1,
		updated_at=clock_timestamp() WHERE tenant_id=$1 AND contract_id=$2 AND row_version=$3
		AND authority_mode='native' AND validity_state='active' AND lifecycle_state IN ('draft','ready_to_sign','registered')
		AND current_revision_id=$4 RETURNING row_version`, accounts.Tenant(current), id,
		input.ExpectedRowVersion, input.BaselineRevisionID).Scan(&rowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ContractSigningRegistration{}, fault.Conflict("合同状态或版本已变化，请刷新后确认")
	}
	if err != nil {
		return ContractSigningRegistration{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_contract_cases (tenant_id,contract_id,case_org_id,case_id,linked_by)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT (tenant_id,contract_id,case_org_id,case_id) DO NOTHING`,
		accounts.Tenant(current), id, caseRecord.OwnerOrgID, caseRecord.ID, current.ID); err != nil {
		return ContractSigningRegistration{}, err
	}
	warnings := []string{"source_content_not_machine_verified", "signature_authenticity_not_verified"}
	if approvalStatus == "legacy_unknown_with_manual_basis" {
		warnings = append(warnings, "approval_history_unknown")
	}
	result := ContractSigningRegistration{
		RegistrationID: registrationID, ContractID: id, ContractRevisionID: input.BaselineRevisionID,
		SigningDate: input.SigningDate,
		SignedMaterial: ContractVersionMaterial{MaterialID: evidence.MaterialID, MaterialVersionID: evidence.DocumentID,
			MaterialVersion: evidence.Version, Filename: evidence.Filename, Digest: evidence.Digest,
			Purpose: "signed_contract", CaseID: evidence.CaseID, CaseOrgID: evidence.CaseOrgID},
		Parties: input.Parties, SignatureEvidenceStatus: input.SignatureEvidenceStatus,
		SignatureEvidenceRef: input.SignatureEvidenceRef, ApprovalBasis: approvalBasis,
		ApprovalBasisNote:      input.LegacyApprovalBasis,
		ApprovalEvidenceStatus: approvalStatus, ApprovalRoundID: approvalRoundID,
		ComparisonMethod: clmSigningComparisonMethod, ComparisonResult: "human_transcription_confirmed",
		VerificationStatus: "human_transcription_confirmed", ComparisonConfirmedBy: current.ID,
		ComparisonConfirmedAt: time.Now().UTC(), ComparisonScope: clmSigningComparisonScope, Warnings: warnings,
		RegisteredBy: current.ID, RegisteredAt: time.Now().UTC(), ContractState: "registered", RowVersion: rowVersion,
	}
	partiesJSON, err := json.Marshal(input.Parties)
	if err != nil {
		return ContractSigningRegistration{}, err
	}
	varianceJSON, err := json.Marshal(map[string]any{"fields": []any{}, "restricted_fields": comparison.RestrictedFields,
		"verification_status": result.VerificationStatus, "confirmed_by": current.ID,
		"confirmed_at": result.ComparisonConfirmedAt, "scope": result.ComparisonScope,
		"legacy_approval_basis": input.LegacyApprovalBasis})
	if err != nil {
		return ContractSigningRegistration{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO clm_contract_signing_registrations
		(tenant_id,contract_id,registration_id,contract_revision_id,signing_date,
		 signed_material_id,signed_material_version_id,signed_case_org_id,signed_case_id,signed_material_version,
		 signed_filename,signed_digest,parties,signature_evidence_status,signature_evidence_ref,
		 approval_basis,approval_evidence_status,approval_round_id,comparison_method,comparison_result,
		 verification_status,comparison_confirmed_by,comparison_confirmed_at,comparison_scope,
		 approval_basis_note,variance,signed_terms,request_digest,registered_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::jsonb,$14,$15,$16,$17,$18,$19,'human_transcription_confirmed',
		'human_transcription_confirmed',$20,$21,$22,$23,$24::jsonb,$25::jsonb,$26,$27)`, accounts.Tenant(current), id, registrationID, input.BaselineRevisionID,
		input.SigningDate, evidence.MaterialID, evidence.DocumentID, evidence.CaseOrgID, evidence.CaseID,
		evidence.Version, evidence.Filename, evidence.Digest, string(partiesJSON), input.SignatureEvidenceStatus,
		nullableString(input.SignatureEvidenceRef), approvalBasis, approvalStatus, approvalRoundID,
		clmSigningComparisonMethod, current.ID, result.ComparisonConfirmedAt, clmSigningComparisonScope,
		input.LegacyApprovalBasis, string(varianceJSON), string(sealedTerms), digest, current.ID)
	if err != nil {
		return ContractSigningRegistration{}, clmUniqueViolation(err, "合同已登记过签署事实")
	}
	if err := insertCLMApprovalEvent(ctx, tx, accounts.Tenant(current), id, rowVersion, clmEventSigningRegistered,
		map[string]any{"contract_id": id, "registration_id": registrationID,
			"contract_revision_id": input.BaselineRevisionID, "signing_date": input.SigningDate,
			"signed_digest": evidence.Digest, "approval_evidence_status": approvalStatus,
			"signature_evidence_status": input.SignatureEvidenceStatus}); err != nil {
		return ContractSigningRegistration{}, err
	}
	response, err := json.Marshal(result)
	if err != nil {
		return ContractSigningRegistration{}, err
	}
	if err := insertCLMWriteReceipt(ctx, tx, accounts.Tenant(current), current.ID, clmRouteRegisterSigning,
		input.IdempotencyKey, digest, id, 201, response); err != nil {
		return ContractSigningRegistration{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractSigningRegistration{}, err
	}
	return result, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (s *Store) ListContractSigningRegistrations(ctx context.Context, user accounts.User, contractID string) (ContractSigningRegistrationList, error) {
	ctx = commandContext(ctx, user)
	id, err := normalizeContractUUID(contractID)
	if err != nil {
		return ContractSigningRegistrationList{}, err
	}
	if !s.HasDatabase() {
		return ContractSigningRegistrationList{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return ContractSigningRegistrationList{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ContractSigningRegistrationList{}, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ContractSigningRegistrationList{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ContractSigningRegistrationList{}, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return ContractSigningRegistrationList{}, fault.NotFound("没有找到对应记录")
	}
	rows, err := tx.Query(ctx, `SELECT registration_id,contract_revision_id,signing_date::text,
		signed_material_id,signed_material_version_id,signed_case_org_id,signed_case_id,
		signed_material_version,signed_filename,signed_digest,parties,
		signature_evidence_status,signature_evidence_ref,approval_basis,approval_evidence_status,
		approval_basis_note,approval_round_id,comparison_method,comparison_result,verification_status,
		comparison_confirmed_by,comparison_confirmed_at,comparison_scope,registered_by,registered_at
		FROM clm_contract_signing_registrations WHERE tenant_id=$1 AND contract_id=$2 ORDER BY registered_at DESC`,
		accounts.Tenant(current), id)
	if err != nil {
		return ContractSigningRegistrationList{}, err
	}
	defer rows.Close()
	result := ContractSigningRegistrationList{Items: []ContractSigningRegistration{}}
	for rows.Next() {
		var item ContractSigningRegistration
		var parties []byte
		var approvalID *string
		var signatureRef *string
		if err := rows.Scan(&item.RegistrationID, &item.ContractRevisionID, &item.SigningDate,
			&item.SignedMaterial.MaterialID, &item.SignedMaterial.MaterialVersionID, &item.SignedMaterial.CaseOrgID, &item.SignedMaterial.CaseID,
			&item.SignedMaterial.MaterialVersion, &item.SignedMaterial.Filename, &item.SignedMaterial.Digest, &parties,
			&item.SignatureEvidenceStatus, &signatureRef, &item.ApprovalBasis, &item.ApprovalEvidenceStatus,
			&item.ApprovalBasisNote, &approvalID, &item.ComparisonMethod, &item.ComparisonResult, &item.VerificationStatus,
			&item.ComparisonConfirmedBy, &item.ComparisonConfirmedAt, &item.ComparisonScope,
			&item.RegisteredBy, &item.RegisteredAt); err != nil {
			return ContractSigningRegistrationList{}, err
		}
		item.ContractID = id
		if signatureRef != nil {
			item.SignatureEvidenceRef = *signatureRef
		}
		item.SignedMaterial.Purpose = "signed_contract"
		item.ApprovalRoundID = approvalID
		if err := json.Unmarshal(parties, &item.Parties); err != nil {
			return ContractSigningRegistrationList{}, fault.Conflict("签署登记各方记录无法读取")
		}
		item.Warnings = registrationWarnings(item.ApprovalEvidenceStatus)
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return ContractSigningRegistrationList{}, err
	}
	rows.Close()
	for _, item := range result.Items {
		if err := requireSigningCaseRead(ctx, tx, current, item.SignedMaterial.CaseOrgID, item.SignedMaterial.CaseID); err != nil {
			return ContractSigningRegistrationList{Items: []ContractSigningRegistration{}}, nil
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractSigningRegistrationList{}, err
	}
	return result, nil
}

func registrationWarnings(approvalStatus string) []string {
	warnings := []string{"source_content_not_machine_verified", "signature_authenticity_not_verified"}
	if approvalStatus == "legacy_unknown_with_manual_basis" {
		warnings = append(warnings, "approval_history_unknown")
	}
	return warnings
}

func requireSigningCaseRead(ctx context.Context, tx pgx.Tx, current accounts.User, orgID, caseID string) error {
	if _, err := loadCLMCase(ctx, tx, current, orgID, caseID); err != nil {
		return fault.NotFound("没有找到对应记录")
	}
	return nil
}

func (s *Store) CreateContractSupplement(ctx context.Context, user accounts.User, input CreateContractSupplementInput) (ContractSupplementResult, error) {
	ctx = commandContext(ctx, user)
	parentID, err := normalizeContractUUID(input.ParentContractID)
	if err != nil {
		return ContractSupplementResult{}, err
	}
	input.ParentContractID = parentID
	if input.ExpectedRowVersion < 1 {
		return ContractSupplementResult{}, fault.Invalid("创建补充协议必须携带主合同 row_version")
	}
	if err := normalizeContractUUIDField("parent_revision_id", &input.ParentRevisionID); err != nil {
		return ContractSupplementResult{}, err
	}
	if err := validateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return ContractSupplementResult{}, err
	}
	if input.RelationType != "supplementary_agreement" && input.RelationType != "change_order" {
		return ContractSupplementResult{}, fault.Invalid("relation_type 必须为 supplementary_agreement 或 change_order")
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" || runeCount(input.Reason) > 2000 {
		return ContractSupplementResult{}, fault.Invalid("reason 必须为 1 到 2000 个字符")
	}
	sanitizeDraftReferences(&input.Draft)
	if err := validateDraftContent(&input.Draft); err != nil {
		return ContractSupplementResult{}, err
	}
	if err := validateDraftAmounts(&input.Draft, contractmoney.StageDraft); err != nil {
		return ContractSupplementResult{}, err
	}
	_, sealedDraft, _, err := sealDraftContent(input.Draft)
	if err != nil {
		return ContractSupplementResult{}, err
	}
	digest, err := clmRequestDigest(clmRouteCreateSupplement, "POST", "/api/contracts/"+parentID+"/supplements", input.ExpectedRowVersion, input)
	if err != nil {
		return ContractSupplementResult{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能创建补充协议", nil)
	if err != nil {
		return ContractSupplementResult{}, err
	}
	defer rollbackCLM(tx)
	if err := lockWriteReceipt(ctx, tx, current, clmRouteCreateSupplement, input.IdempotencyKey); err != nil {
		return ContractSupplementResult{}, err
	}
	receipt, found, err := loadCLMWriteReceipt(ctx, tx, accounts.Tenant(current), current.ID, clmRouteCreateSupplement, input.IdempotencyKey)
	if err != nil {
		return ContractSupplementResult{}, err
	}
	if found {
		if receipt.RequestDigest != digest || receipt.ContractID != parentID {
			return ContractSupplementResult{}, fault.Conflict("Idempotency-Key已用于不同补充协议请求")
		}
		parent, err := loadCLMContract(ctx, tx, accounts.Tenant(current), parentID)
		if err != nil {
			return ContractSupplementResult{}, err
		}
		if !authorizeContract(parent, current, access.Read) {
			return ContractSupplementResult{}, fault.NotFound("没有找到对应记录")
		}
		if !authorizeContract(parent, current, access.Manage) {
			return ContractSupplementResult{}, fault.Forbidden("当前授权不能创建补充协议")
		}
		var result ContractSupplementResult
		if err := json.Unmarshal(receipt.Response, &result); err != nil {
			return ContractSupplementResult{}, err
		}
		child, err := loadCLMContract(ctx, tx, accounts.Tenant(current), result.Supplement.SupplementContractID)
		if err != nil {
			return ContractSupplementResult{}, err
		}
		if !authorizeContract(child, current, access.Read) {
			return ContractSupplementResult{}, fault.NotFound("没有找到对应记录")
		}
		if !authorizeContract(child, current, access.Manage) {
			return ContractSupplementResult{}, fault.Forbidden("当前授权不能读取补充协议草稿")
		}
		storedDetail, marshalErr := json.Marshal(result.Contract)
		if marshalErr != nil {
			return ContractSupplementResult{}, marshalErr
		}
		result.Contract, err = projectReceiptDetail(clmWriteReceipt{Response: storedDetail}, current, child)
		if err != nil {
			return ContractSupplementResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ContractSupplementResult{}, err
		}
		return result, nil
	}
	if err := scopedContractLock(ctx, tx, parentID); err != nil {
		return ContractSupplementResult{}, err
	}
	parent, err := loadCLMContract(ctx, tx, accounts.Tenant(current), parentID)
	if err != nil {
		return ContractSupplementResult{}, err
	}
	if !authorizeContract(parent, current, access.Read) {
		return ContractSupplementResult{}, fault.NotFound("没有找到对应记录")
	}
	if !authorizeContract(parent, current, access.Manage) {
		return ContractSupplementResult{}, fault.Forbidden("当前授权不能创建补充协议")
	}
	if parent.AuthorityMode != "native" || parent.ValidityState != "active" || (parent.LifecycleState != "registered" && parent.LifecycleState != "performing") {
		return ContractSupplementResult{}, fault.Conflict("主合同必须是有效的本地已签合同")
	}
	if parent.RowVersion != input.ExpectedRowVersion {
		return ContractSupplementResult{}, fault.Conflict("主合同已被修改，请刷新后重试")
	}
	if parent.CurrentRevisionID == nil || *parent.CurrentRevisionID != input.ParentRevisionID {
		return ContractSupplementResult{}, fault.Conflict("parent_revision_id 必须是主合同当前冻结版本")
	}
	if _, err := loadFrozenContractVersion(ctx, tx, accounts.Tenant(current), parentID, input.ParentRevisionID); err != nil {
		return ContractSupplementResult{}, err
	}
	var inReview bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_approval_rounds WHERE tenant_id=$1 AND contract_id=$2 AND state='in_review')`, accounts.Tenant(current), parentID).Scan(&inReview); err != nil {
		return ContractSupplementResult{}, err
	}
	if inReview {
		return ContractSupplementResult{}, fault.Conflict("主合同存在进行中的会签，不能创建补充协议")
	}
	supplementID, err := newContractUUID()
	if err != nil {
		return ContractSupplementResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_contracts (tenant_id,contract_id,owner_org_id,authority_mode,contract_number,lifecycle_state,row_version,created_by)
		VALUES ($1,$2,$3,'native',$4,'draft',1,$5)`, accounts.Tenant(current), supplementID, parent.OwnerOrgID,
		optionalTextOrPointer(input.Draft.Contract.Number), current.ID); err != nil {
		return ContractSupplementResult{}, clmUniqueViolation(err, "补充协议合同编号已被使用")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_contract_drafts (tenant_id,contract_id,content,updated_by) VALUES ($1,$2,$3::jsonb,$4)`,
		accounts.Tenant(current), supplementID, string(sealedDraft), current.ID); err != nil {
		return ContractSupplementResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_contract_supplements (tenant_id,parent_contract_id,supplement_contract_id,parent_revision_id,relation_type,reason,linked_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, accounts.Tenant(current), parentID, supplementID, input.ParentRevisionID, input.RelationType, input.Reason, current.ID); err != nil {
		return ContractSupplementResult{}, clmUniqueViolation(err, "该补充协议已关联主合同")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_contract_cases (tenant_id,contract_id,case_org_id,case_id,linked_by)
		SELECT tenant_id,$3,case_org_id,case_id,$4 FROM clm_contract_cases WHERE tenant_id=$1 AND contract_id=$2
		ON CONFLICT (tenant_id,contract_id,case_org_id,case_id) DO NOTHING`, accounts.Tenant(current), parentID, supplementID, current.ID); err != nil {
		return ContractSupplementResult{}, err
	}
	if err := insertCLMApprovalEvent(ctx, tx, accounts.Tenant(current), supplementID, 1, clmEventSupplementCreated,
		map[string]any{"contract_id": supplementID, "parent_contract_id": parentID,
			"parent_revision_id": input.ParentRevisionID, "relation_type": input.RelationType}); err != nil {
		return ContractSupplementResult{}, err
	}
	var linkedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT linked_at FROM clm_contract_supplements WHERE tenant_id=$1 AND parent_contract_id=$2 AND supplement_contract_id=$3`, accounts.Tenant(current), parentID, supplementID).Scan(&linkedAt); err != nil {
		return ContractSupplementResult{}, err
	}
	record := clmContractRecord{TenantID: accounts.Tenant(current), ContractID: supplementID, OwnerOrgID: parent.OwnerOrgID,
		AuthorityMode: "native", ContractNumber: optionalTextOrPointer(input.Draft.Contract.Number), LifecycleState: "draft",
		ValidityState: "active", RowVersion: 1, CreatedBy: current.ID, CreatedAt: linkedAt, UpdatedAt: linkedAt}
	name, amount, currency, err := draftContentSummaryFacts(sealedDraft)
	if err != nil {
		return ContractSupplementResult{}, err
	}
	detail := ContractDetail{Summary: clmSummary(record, name, amount, currency), AllowedCommands: clmDraftCommands(record, true), Warnings: []string{}}
	plaintext, err := unsealStoredDraft(sealedDraft)
	if err != nil {
		return ContractSupplementResult{}, err
	}
	detail.Draft = new(projectDraftForViewer(plaintext, true))
	item := ContractSupplement{ParentContractID: parentID, SupplementContractID: supplementID, ParentRevisionID: input.ParentRevisionID,
		RelationType: input.RelationType, Reason: input.Reason, LinkedBy: current.ID, LinkedAt: linkedAt, ContractState: "draft"}
	result := ContractSupplementResult{Supplement: item, Contract: detail}
	storedResult := result
	storedResult.Contract.Draft = sealedDraftOf(sealedDraft)
	response, err := json.Marshal(storedResult)
	if err != nil {
		return ContractSupplementResult{}, err
	}
	if err := insertCLMWriteReceipt(ctx, tx, accounts.Tenant(current), current.ID, clmRouteCreateSupplement,
		input.IdempotencyKey, digest, parentID, 201, response); err != nil {
		return ContractSupplementResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractSupplementResult{}, err
	}
	return result, nil
}

func (s *Store) ListContractSupplements(ctx context.Context, user accounts.User, contractID string) (ContractSupplementList, error) {
	ctx = commandContext(ctx, user)
	id, err := normalizeContractUUID(contractID)
	if err != nil {
		return ContractSupplementList{}, err
	}
	if !s.HasDatabase() {
		return ContractSupplementList{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return ContractSupplementList{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ContractSupplementList{}, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ContractSupplementList{}, err
	}
	base, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ContractSupplementList{}, err
	}
	if !authorizeContract(base, current, access.Read) {
		return ContractSupplementList{}, fault.NotFound("没有找到对应记录")
	}
	result := ContractSupplementList{Items: []ContractSupplement{}}
	rows, err := tx.Query(ctx, `SELECT parent_contract_id,supplement_contract_id,parent_revision_id,relation_type,reason,linked_by,linked_at
		FROM clm_contract_supplements WHERE tenant_id=$1 AND (parent_contract_id=$2 OR supplement_contract_id=$2)
		ORDER BY linked_at DESC,supplement_contract_id`, accounts.Tenant(current), id)
	if err != nil {
		return ContractSupplementList{}, err
	}
	relations := []ContractSupplement{}
	for rows.Next() {
		var item ContractSupplement
		if err := rows.Scan(&item.ParentContractID, &item.SupplementContractID, &item.ParentRevisionID, &item.RelationType, &item.Reason, &item.LinkedBy, &item.LinkedAt); err != nil {
			rows.Close()
			return ContractSupplementList{}, err
		}
		relations = append(relations, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ContractSupplementList{}, err
	}
	rows.Close()
	for _, item := range relations {
		if item.ParentContractID == id {
			child, err := loadCLMContract(ctx, tx, accounts.Tenant(current), item.SupplementContractID)
			if err != nil {
				return ContractSupplementList{}, err
			}
			if !authorizeContract(child, current, access.Read) {
				continue
			}
			item.ContractState = child.LifecycleState
			signErr := tx.QueryRow(ctx, `SELECT signing_date::text FROM clm_contract_signing_registrations WHERE tenant_id=$1 AND contract_id=$2`, accounts.Tenant(current), child.ContractID).Scan(&item.SigningDate)
			if signErr != nil && !errors.Is(signErr, pgx.ErrNoRows) {
				return ContractSupplementList{}, signErr
			}
			result.Items = append(result.Items, item)
		} else {
			parent, err := loadCLMContract(ctx, tx, accounts.Tenant(current), item.ParentContractID)
			if err != nil {
				return ContractSupplementList{}, err
			}
			if !authorizeContract(parent, current, access.Read) {
				continue
			}
			item.ContractState = base.LifecycleState
			result.Parent = &item
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractSupplementList{}, err
	}
	return result, nil
}

func (s *Store) loadContractSigningSummary(ctx context.Context, tx pgx.Tx, current accounts.User, record clmContractRecord) (*ContractSigningRegistration, error) {
	ctx = commandContext(ctx, current)
	var item ContractSigningRegistration
	var parties []byte
	var approvalID *string
	var signatureRef *string
	err := tx.QueryRow(ctx, `SELECT registration_id,contract_revision_id,signing_date::text,
		signed_material_id,signed_material_version_id,signed_case_org_id,signed_case_id,signed_material_version,
		signed_filename,signed_digest,parties,signature_evidence_status,signature_evidence_ref,
		approval_basis,approval_evidence_status,approval_basis_note,approval_round_id,comparison_method,comparison_result,
		verification_status,comparison_confirmed_by,comparison_confirmed_at,comparison_scope,registered_by,registered_at
		FROM clm_contract_signing_registrations WHERE tenant_id=$1 AND contract_id=$2`, record.TenantID, record.ContractID).Scan(
		&item.RegistrationID, &item.ContractRevisionID, &item.SigningDate, &item.SignedMaterial.MaterialID, &item.SignedMaterial.MaterialVersionID,
		&item.SignedMaterial.CaseOrgID, &item.SignedMaterial.CaseID, &item.SignedMaterial.MaterialVersion, &item.SignedMaterial.Filename,
		&item.SignedMaterial.Digest, &parties, &item.SignatureEvidenceStatus, &signatureRef, &item.ApprovalBasis,
		&item.ApprovalEvidenceStatus, &item.ApprovalBasisNote, &approvalID, &item.ComparisonMethod, &item.ComparisonResult,
		&item.VerificationStatus, &item.ComparisonConfirmedBy, &item.ComparisonConfirmedAt, &item.ComparisonScope,
		&item.RegisteredBy, &item.RegisteredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := requireSigningCaseRead(ctx, tx, current, item.SignedMaterial.CaseOrgID, item.SignedMaterial.CaseID); err != nil {
		return nil, nil
	}
	item.ContractID = record.ContractID
	if signatureRef != nil {
		item.SignatureEvidenceRef = *signatureRef
	}
	item.SignedMaterial.Purpose = "signed_contract"
	item.ApprovalRoundID = approvalID
	if err := json.Unmarshal(parties, &item.Parties); err != nil {
		return nil, fault.Conflict("签署登记各方记录无法读取")
	}
	item.Warnings = registrationWarnings(item.ApprovalEvidenceStatus)
	return &item, nil
}
