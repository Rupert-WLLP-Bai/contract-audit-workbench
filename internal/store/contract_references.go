package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

// Contract references are resolved inside the contract's own transaction. The
// subject, material and document tables are owned by their services; this file
// only reads them and pins the resolved identity into the contract JSONB. It
// never calls the legacy state writer and never holds a contract lock while
// writing upstream tables.

type referenceMode int

const (
	// referenceModeDraft resolves the current source-case versions without
	// pinning anything. Incomplete drafts may not resolve at all; strict
	// callers use a mode that rejects unresolved references.
	referenceModeDraft referenceMode = iota
	// referenceModeImport resolves current versions and writes the resolved
	// pins into the draft struct before the caller seals it.
	referenceModeImport
	// referenceModeFrozen only verifies the pins already frozen in a revision.
	// It never resolves "current" versions and never backfills missing pins.
	referenceModeFrozen
)

// ContractReferenceCheck is the read-only result of resolving one draft or
// frozen revision against its source case. It is a projection only; nothing in
// this struct is persisted by the check endpoint.
type ContractReferenceCheck struct {
	ContractID         string                      `json:"contract_id"`
	Source             string                      `json:"source"`
	ContractRevisionID *string                     `json:"contract_revision_id"`
	SourceCaseID       string                      `json:"source_case_id"`
	CheckedAt          time.Time                   `json:"checked_at"`
	Subjects           []ContractSubjectReference  `json:"subjects"`
	Materials          []ContractMaterialReference `json:"materials"`
	ProcurementResult  *ProcurementSourceSnapshot  `json:"procurement_result,omitempty"`
	Warnings           []string                    `json:"warnings"`
}

// ContractSubjectReference is one resolved contract party. original_path is
// absent here because subjects have no attachment.
type ContractSubjectReference struct {
	PartyID            string `json:"party_id"`
	Role               string `json:"role"`
	NameSnapshot       string `json:"name_snapshot"`
	BindingID          string `json:"binding_id"`
	BindingVersion     int64  `json:"binding_version"`
	CaseRole           string `json:"case_role"`
	BindingState       string `json:"binding_state"`
	SubjectVersion     int64  `json:"subject_version"`
	SubjectPayloadHash string `json:"subject_payload_hash"`
	VerificationState  string `json:"verification_state"`
	Frozen             bool   `json:"frozen"`
}

// ContractMaterialReference is one resolved material version. original_path
// points at the existing document-service original endpoint; it is not a new
// download route and confers no authorization by itself.
type ContractMaterialReference struct {
	MaterialID        string `json:"material_id"`
	MaterialVersionID string `json:"material_version_id"`
	SHA256            string `json:"sha256"`
	Purpose           string `json:"purpose"`
	MaterialVersion   int    `json:"material_version"`
	MaterialRole      string `json:"material_role"`
	Filename          string `json:"filename"`
	MediaType         string `json:"media_type"`
	Frozen            bool   `json:"frozen"`
	OriginalPath      string `json:"original_path"`
}

type resolvedSubjectBinding struct {
	BindingID          string
	BindingVersion     int64
	CaseRole           string
	BindingState       string
	SubjectID          string
	SubjectVersion     int64
	SubjectPayloadHash string
	VerificationState  string
	DisplayName        string
}

// CheckContractReferences resolves the current draft or frozen revision of one
// visible contract against its source case. The optional case_id is only used
// for native drafts, which have no clm_contract_cases link yet; an imported
// contract must use its linked case.
func (s *Store) CheckContractReferences(ctx context.Context, user accounts.User, contractID, caseID string) (ContractReferenceCheck, error) {
	if !s.HasDatabase() {
		return ContractReferenceCheck{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	id, err := normalizeContractUUID(contractID)
	if err != nil {
		return ContractReferenceCheck{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return ContractReferenceCheck{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ContractReferenceCheck{}, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ContractReferenceCheck{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ContractReferenceCheck{}, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return ContractReferenceCheck{}, fault.NotFound("没有找到对应记录")
	}
	contentBytes, source, revisionID, err := loadContractReferenceContent(ctx, tx, current, record)
	if err != nil {
		return ContractReferenceCheck{}, err
	}
	anchor, err := loadContractReferenceAnchor(ctx, tx, current, record, caseID)
	if err != nil {
		return ContractReferenceCheck{}, err
	}
	plaintext, err := unsealStoredDraft(contentBytes)
	if err != nil {
		return ContractReferenceCheck{}, err
	}
	mode := referenceModeDraft
	if source == "revision" {
		mode = referenceModeFrozen
	}
	check, err := resolveContractReferences(ctx, tx, current, anchor, &plaintext, mode)
	if err != nil {
		return ContractReferenceCheck{}, err
	}
	check.ContractID = record.ContractID
	check.Source = source
	check.ContractRevisionID = revisionID
	check.SourceCaseID = anchor.ID
	if err := tx.Commit(ctx); err != nil {
		return ContractReferenceCheck{}, err
	}
	return check, nil
}

func loadContractReferenceContent(ctx context.Context, tx pgx.Tx, current accounts.User, record clmContractRecord) ([]byte, string, *string, error) {
	// The draft stays editable after a freeze (design #81 §3.4), so a
	// reference check must inspect the same source the editor shows: while the
	// contract is still in draft lifecycle, that is the draft row, not the last
	// frozen revision.
	if record.CurrentRevisionID != nil && record.LifecycleState != "draft" {
		var content []byte
		err := tx.QueryRow(ctx, `SELECT snapshot FROM clm_contract_revisions
			WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3`,
			accounts.Tenant(current), record.ContractID, *record.CurrentRevisionID).Scan(&content)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", nil, fault.NotFound("没有找到对应记录")
		}
		if err != nil {
			return nil, "", nil, err
		}
		return content, "revision", record.CurrentRevisionID, nil
	}
	var content []byte
	err := tx.QueryRow(ctx, `SELECT content FROM clm_contract_drafts
		WHERE tenant_id=$1 AND contract_id=$2`, accounts.Tenant(current), record.ContractID).Scan(&content)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil, fault.New(422, "contract_content_missing", "合同尚无可核对的草稿或冻结版本")
	}
	if err != nil {
		return nil, "", nil, err
	}
	return content, "draft", nil, nil
}

func loadContractReferenceAnchor(ctx context.Context, tx pgx.Tx, current accounts.User, record clmContractRecord, requestedCaseID string) (Case, error) {
	requestedCaseID = strings.TrimSpace(requestedCaseID)
	rows, err := tx.Query(ctx, `SELECT case_org_id, case_id FROM clm_contract_cases
		WHERE tenant_id=$1 AND contract_id=$2 ORDER BY case_org_id,case_id`,
		accounts.Tenant(current), record.ContractID)
	if err != nil {
		return Case{}, err
	}
	defer rows.Close()
	type contractCaseLink struct{ orgID, caseID string }
	links := []contractCaseLink{}
	for rows.Next() {
		var link contractCaseLink
		if err := rows.Scan(&link.orgID, &link.caseID); err != nil {
			return Case{}, err
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return Case{}, err
	}
	rows.Close()
	if requestedCaseID != "" {
		if len(links) > 0 {
			for _, link := range links {
				if link.caseID == requestedCaseID {
					return loadCLMCase(ctx, tx, current, link.orgID, link.caseID)
				}
			}
			return Case{}, fault.Invalid("指定的来源案件不是该合同的来源案件")
		}
		return loadCLMCaseByID(ctx, tx, current, requestedCaseID)
	}
	if len(links) == 0 {
		return Case{}, fault.New(422, "anchor_case_required", "草稿尚未关联来源案件，请提供 case_id 后重新核对")
	}
	if len(links) > 1 {
		return Case{}, fault.New(422, "multiple_source_cases", "合同关联了多个来源案件，不能自动选择")
	}
	return loadCLMCase(ctx, tx, current, links[0].orgID, links[0].caseID)
}

// loadCLMCaseByID is the draft-only path: the caller names a readable case
// before the draft has a frozen link. It applies the same case read policy as
// every other case lookup and never accepts a tenant from the request.
func loadCLMCaseByID(ctx context.Context, tx pgx.Tx, current accounts.User, caseID string) (Case, error) {
	var item Case
	var members string
	err := tx.QueryRow(ctx, `SELECT id,tenant_id,org_id,owner_id,member_ids,business_status,archived_at
		FROM cases WHERE tenant_id=$1 AND id=$2`,
		accounts.Tenant(current), caseID).Scan(&item.ID, &item.TenantID, &item.OwnerOrgID, &item.OwnerID, &members, &item.BusinessStatus, &item.ArchivedAt)
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

// resolveContractReferences is the single strict resolver shared by the import
// freeze path and the read-only check endpoint. The caller owns the transaction
// and must have already authorized the anchor case.
func resolveContractReferences(ctx context.Context, tx pgx.Tx, current accounts.User, anchor Case, draft *DraftContent, mode referenceMode) (ContractReferenceCheck, error) {
	if draft == nil || draft.Contract == nil {
		return ContractReferenceCheck{}, fault.Invalid("合同内容必须包含 contract 对象")
	}
	now, err := contractReferenceNow(ctx, tx)
	if err != nil {
		return ContractReferenceCheck{}, err
	}
	tenantID := accounts.Tenant(current)
	result := ContractReferenceCheck{
		SourceCaseID: anchor.ID,
		CheckedAt:    now,
		Subjects:     []ContractSubjectReference{},
		Materials:    []ContractMaterialReference{},
		Warnings:     []string{},
	}
	warnings := map[string]bool{}
	addWarning := func(value string) {
		if !warnings[value] {
			warnings[value] = true
			result.Warnings = append(result.Warnings, value)
		}
	}

	for i := range draft.Contract.RelatedParties {
		party := &draft.Contract.RelatedParties[i]
		var binding resolvedSubjectBinding
		if mode == referenceModeFrozen {
			binding, err = resolveFrozenContractSubject(ctx, tx, tenantID, anchor, party)
		} else {
			binding, err = resolveCurrentContractSubject(ctx, tx, tenantID, anchor, party)
		}
		if err != nil {
			return ContractReferenceCheck{}, err
		}
		result.Subjects = append(result.Subjects, ContractSubjectReference{
			PartyID: party.PartyID, Role: party.Role, NameSnapshot: party.NameSnapshot,
			BindingID: binding.BindingID, BindingVersion: binding.BindingVersion,
			CaseRole: binding.CaseRole, BindingState: binding.BindingState,
			SubjectVersion: binding.SubjectVersion, SubjectPayloadHash: binding.SubjectPayloadHash,
			VerificationState: binding.VerificationState, Frozen: mode == referenceModeFrozen,
		})
		if binding.VerificationState != "verified" {
			addWarning("subject_pending_verification")
		}
		if mode == referenceModeImport {
			party.BindingID = binding.BindingID
			party.ReferenceSnapshot = &PartyReferenceSnapshot{
				BindingID: binding.BindingID, BindingVersion: binding.BindingVersion,
				CaseRole: binding.CaseRole, BindingState: binding.BindingState,
				SubjectID: binding.SubjectID, SubjectVersion: binding.SubjectVersion,
				SubjectPayloadHash: binding.SubjectPayloadHash, VerificationState: binding.VerificationState,
				DisplayName: binding.DisplayName, CapturedAt: now,
			}
		}
	}

	seenMaterials := map[string]bool{}
	materialByDocument := map[string]resolvedMaterialVersion{}
	for i := range draft.Materials {
		ref := &draft.Materials[i]
		key := ref.MaterialID + "\x00" + ref.MaterialVersionID
		if seenMaterials[key] {
			return ContractReferenceCheck{}, fault.Invalid("同一材料版本不能重复引用")
		}
		seenMaterials[key] = true
		material, err := resolveContractMaterial(ctx, tx, tenantID, anchor, ref, mode)
		if err != nil {
			return ContractReferenceCheck{}, err
		}
		materialByDocument[ref.MaterialVersionID] = material
		result.Materials = append(result.Materials, ContractMaterialReference{
			MaterialID: ref.MaterialID, MaterialVersionID: ref.MaterialVersionID,
			SHA256: ref.SHA256, Purpose: ref.Purpose, MaterialVersion: material.Version,
			MaterialRole: material.MaterialRole, Filename: material.Filename, MediaType: material.MediaType,
			Frozen: mode == referenceModeFrozen, OriginalPath: contractOriginalPath(anchor.ID, material.DocumentID),
		})
	}

	if ref := draft.Contract.ProcurementResultPackage; ref != nil {
		if strings.TrimSpace(ref.System) == "" || strings.TrimSpace(ref.ResultID) == "" ||
			strings.TrimSpace(ref.PackageID) == "" || strings.TrimSpace(ref.SnapshotRef) == "" {
			return ContractReferenceCheck{}, fault.Invalid("采购结果来源快照字段不完整")
		}
		material, ok := materialByDocument[ref.SnapshotRef]
		if !ok {
			return ContractReferenceCheck{}, fault.Invalid("采购结果来源快照必须引用同一合同快照中的材料版本")
		}
		if !procurementMaterialRole(material.MaterialRole) {
			return ContractReferenceCheck{}, fault.Invalid("采购结果来源材料必须是采购或中标材料")
		}
		pin := &ProcurementSourceSnapshot{
			System: ref.System, ResultID: ref.ResultID, PackageID: ref.PackageID, SnapshotRef: ref.SnapshotRef,
			MaterialID: material.MaterialID, MaterialVersionID: material.DocumentID,
			MaterialVersion: material.Version, MaterialRole: material.MaterialRole,
			SHA256: material.Digest, CapturedAt: now,
		}
		if mode == referenceModeFrozen {
			if ref.SourceSnapshot == nil {
				return ContractReferenceCheck{}, fault.Invalid("冻结版本缺少采购结果来源快照，不能自动补写")
			}
			if err := verifyProcurementSourceSnapshot(*ref.SourceSnapshot, *ref, material); err != nil {
				return ContractReferenceCheck{}, err
			}
			result.ProcurementResult = ref.SourceSnapshot
		} else {
			if mode == referenceModeImport {
				ref.SourceSnapshot = pin
			}
			result.ProcurementResult = pin
		}
	}

	return result, nil
}

func contractReferenceNow(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, err
	}
	return now.UTC(), nil
}

// resolveCurrentContractSubject selects the active current binding prepared by
// the subject service. Inactive and pending-confirmation bindings are not
// ready versions and never become frozen contract references.
func resolveCurrentContractSubject(ctx context.Context, tx pgx.Tx, tenantID string, anchor Case, party *PartyRef) (resolvedSubjectBinding, error) {
	rows, err := tx.Query(ctx, `SELECT r.binding_id, r.version, r.subject_id,
			r.subject_version, r.role, r.binding_state,
			sv.payload->>'display_name', sv.payload->>'verification_state', sv.payload_hash
		FROM case_subject_roles head
		JOIN case_subject_role_versions r
		  ON r.tenant_id=head.tenant_id AND r.org_id=head.org_id AND r.case_id=head.case_id
		 AND r.binding_id=head.binding_id AND r.version=head.current_version
		JOIN subject_versions sv
		  ON sv.tenant_id=r.tenant_id AND sv.subject_id=r.subject_id AND sv.version=r.subject_version
		WHERE head.tenant_id=$1 AND head.org_id=$2 AND head.case_id=$3
		  AND r.subject_id=$4 AND ($5='' OR head.binding_id=$5)
		ORDER BY head.binding_id`,
		tenantID, anchor.OwnerOrgID, anchor.ID, party.PartyID, party.BindingID)
	if err != nil {
		return resolvedSubjectBinding{}, err
	}
	defer rows.Close()
	candidates := []resolvedSubjectBinding{}
	for rows.Next() {
		var binding resolvedSubjectBinding
		if err := rows.Scan(&binding.BindingID, &binding.BindingVersion, &binding.SubjectID,
			&binding.SubjectVersion, &binding.CaseRole, &binding.BindingState,
			&binding.DisplayName, &binding.VerificationState, &binding.SubjectPayloadHash); err != nil {
			return resolvedSubjectBinding{}, err
		}
		candidates = append(candidates, binding)
	}
	if err := rows.Err(); err != nil {
		return resolvedSubjectBinding{}, err
	}
	ready := candidates[:0]
	for _, binding := range candidates {
		if binding.BindingState != "active" {
			continue
		}
		if !caseRoleAllowsContractRole(party.Role, binding.CaseRole) {
			continue
		}
		ready = append(ready, binding)
	}
	if len(ready) == 0 {
		return resolvedSubjectBinding{}, fault.Invalid("主体引用无法在来源案件内核对")
	}
	if len(ready) > 1 {
		return resolvedSubjectBinding{}, fault.Invalid("主体引用存在多个有效绑定，请明确 binding_id")
	}
	binding := ready[0]
	if strings.TrimSpace(party.NameSnapshot) != binding.DisplayName {
		return resolvedSubjectBinding{}, fault.Invalid("主体名称快照与来源案件主体不一致")
	}
	return binding, nil
}

// resolveFrozenContractSubject verifies the exact binding version frozen in the
// revision. A subject re-bound to a newer version does not invalidate the old
// contract, but a missing or altered frozen row is a hard integrity failure.
func resolveFrozenContractSubject(ctx context.Context, tx pgx.Tx, tenantID string, anchor Case, party *PartyRef) (resolvedSubjectBinding, error) {
	pin := party.ReferenceSnapshot
	if pin == nil {
		return resolvedSubjectBinding{}, fault.Invalid("冻结版本缺少主体引用快照，不能自动补写")
	}
	if strings.TrimSpace(party.PartyID) != pin.SubjectID || pin.BindingID == "" || pin.BindingVersion < 1 ||
		pin.SubjectVersion < 1 || pin.SubjectPayloadHash == "" || pin.CapturedAt.IsZero() ||
		(party.BindingID != "" && party.BindingID != pin.BindingID) ||
		strings.TrimSpace(party.NameSnapshot) != pin.DisplayName {
		return resolvedSubjectBinding{}, frozenReferenceConflict()
	}
	var binding resolvedSubjectBinding
	err := tx.QueryRow(ctx, `SELECT r.binding_id, r.version, r.subject_id, r.subject_version,
			r.role, r.binding_state, sv.payload_hash,
			sv.payload->>'display_name', sv.payload->>'verification_state'
		FROM case_subject_role_versions r
		JOIN subject_versions sv
		  ON sv.tenant_id=r.tenant_id AND sv.subject_id=r.subject_id AND sv.version=r.subject_version
		WHERE r.tenant_id=$1 AND r.org_id=$2 AND r.case_id=$3
		  AND r.binding_id=$4 AND r.version=$5`,
		tenantID, anchor.OwnerOrgID, anchor.ID, pin.BindingID, pin.BindingVersion).Scan(
		&binding.BindingID, &binding.BindingVersion, &binding.SubjectID, &binding.SubjectVersion,
		&binding.CaseRole, &binding.BindingState, &binding.SubjectPayloadHash,
		&binding.DisplayName, &binding.VerificationState)
	if errors.Is(err, pgx.ErrNoRows) {
		return resolvedSubjectBinding{}, frozenReferenceConflict()
	}
	if err != nil {
		return resolvedSubjectBinding{}, err
	}
	if binding.SubjectID != pin.SubjectID || binding.BindingState != pin.BindingState ||
		binding.CaseRole != pin.CaseRole || binding.SubjectVersion != pin.SubjectVersion ||
		binding.SubjectPayloadHash != pin.SubjectPayloadHash ||
		binding.DisplayName != pin.DisplayName || binding.VerificationState != pin.VerificationState ||
		!caseRoleAllowsContractRole(party.Role, binding.CaseRole) {
		return resolvedSubjectBinding{}, frozenReferenceConflict()
	}
	return binding, nil
}

func resolveContractMaterial(ctx context.Context, tx pgx.Tx, tenantID string, anchor Case, ref *MaterialRef, mode referenceMode) (resolvedMaterialVersion, error) {
	material, err := resolveMaterialVersion(ctx, tx, tenantID, *ref)
	if err != nil {
		// Only a missing or mismatched reference is rejected with the uniform
		// source-case message. Database/context failures must stay visible as
		// 5xx instead of being disguised as a client validation error.
		var fe *fault.Error
		if !errors.As(err, &fe) || (fe.Status != 404 && fe.Status != 422) {
			return resolvedMaterialVersion{}, err
		}
		if mode == referenceModeFrozen {
			return resolvedMaterialVersion{}, frozenReferenceConflict()
		}
		return resolvedMaterialVersion{}, fault.Invalid("材料引用无法在来源案件内核对")
	}
	if material.CaseID != anchor.ID || material.CaseOrgID != anchor.OwnerOrgID {
		if mode == referenceModeFrozen {
			return resolvedMaterialVersion{}, frozenReferenceConflict()
		}
		return resolvedMaterialVersion{}, fault.Invalid("材料引用无法在来源案件内核对")
	}
	return material, nil
}

func verifyProcurementSourceSnapshot(pin ProcurementSourceSnapshot, ref ProcurementRef, material resolvedMaterialVersion) error {
	if pin.System != ref.System || pin.ResultID != ref.ResultID || pin.PackageID != ref.PackageID ||
		pin.SnapshotRef != ref.SnapshotRef || pin.MaterialID != material.MaterialID ||
		pin.MaterialVersionID != material.DocumentID || pin.MaterialVersion != material.Version ||
		pin.SHA256 != material.Digest || pin.MaterialRole != material.MaterialRole ||
		!procurementMaterialRole(pin.MaterialRole) || pin.CapturedAt.IsZero() {
		return frozenReferenceConflict()
	}
	return nil
}

func frozenReferenceConflict() error {
	return fault.New(409, "frozen_reference_mismatch", "冻结版本的引用与冻结原件不一致，不能自动补写或覆盖")
}

// contractOriginalPath returns the existing document-service route. The route
// is not registered here and no authorization is implied by the path: every
// request re-checks the source case and the stored original digest.
func contractOriginalPath(caseID, documentID string) string {
	return "/api/cases/" + url.PathEscape(caseID) + "/documents/" + url.PathEscape(documentID) + "/original"
}
