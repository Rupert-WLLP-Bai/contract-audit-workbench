package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

type OperatorMappingChangePreview struct {
	MappingID        string                           `json:"mapping_id"`
	ServiceObjectID  string                           `json:"service_object_id"`
	CAS              OperatorMappingChangeCAS         `json:"cas"`
	Before           OperatorMappingVersion           `json:"before"`
	After            OperatorMappingVersion           `json:"after"`
	IdentityAfter    *ServiceObjectVersion            `json:"identity_after,omitempty"`
	AssociationAfter *operatorMappingAssociationAfter `json:"association_after,omitempty"`
	BeforeSHA256     string                           `json:"before_sha256"`
	AfterSHA256      string                           `json:"after_sha256"`
	ImpactSHA256     string                           `json:"impact_sha256"`
	BasisRefs        json.RawMessage                  `json:"basis_refs"`
}

func (s *Store) mappingAssociationTarget(ctx context.Context, tx pgx.Tx, st state, user accounts.User, item Case,
	head OperatorMappingHead, version OperatorMappingVersion, identity ServiceObjectVersion, input *ServiceObjectAssociationConfirmInput) (*ServiceObjectContractAssociation, *operatorMappingAssociationAfter, error) {
	if input == nil {
		return nil, nil, nil
	}
	objectHead, err := loadServiceObjectHead(ctx, tx, item.TenantID, item.OwnerOrgID, item.ID, head.ServiceObjectID)
	if err != nil {
		return nil, nil, err
	}
	if objectHead.CurrentVersion != version.ObjectVersion || objectHead.CurrentAssociationVersion != version.AssociationVersion ||
		input.ExpectedObjectVersion != objectHead.CurrentVersion || input.ExpectedAssociationVersion != objectHead.CurrentAssociationVersion || !input.Confirmed {
		return nil, nil, fault.Conflict("服务对象身份或关联版本已变化")
	}
	preview, err := deriveServiceObjectAssociationPreview(ctx, tx, s, st, user, item, objectHead, input.Source)
	if err != nil {
		return nil, nil, err
	}
	var selected *ServiceObjectAssociationOption
	for i := range preview.Options {
		if preview.Options[i].ObjectRef == input.ObjectRef {
			selected = &preview.Options[i]
			break
		}
	}
	if selected == nil || selected.FactsSHA256 != input.FactsSHA256 || preview.CandidatesSHA256 != input.CandidatesSHA256 {
		return nil, nil, fault.Conflict("目标合同对象或冻结事实已变化，请重新预览")
	}
	association := ServiceObjectContractAssociation{MaterialID: preview.MaterialID, MaterialVersion: preview.MaterialVersion,
		DocumentID: preview.DocumentID, ParsedDocumentID: preview.ParsedDocumentID, SourceRunID: preview.RunID,
		SourceAttemptID: preview.AttemptID, ObjectRef: selected.ObjectRef, FactRefs: selected.Facts, BasisRefs: selected.BasisRefs}
	plan, err := PlanServiceObjectAssociation(objectHead, identity, ServiceObjectAssociationCandidate{
		ExpectedObjectVersion: input.ExpectedObjectVersion, ExpectedAssociationVersion: input.ExpectedAssociationVersion,
		Confirmed: true, Association: association}, user.ID, time.Now().UTC())
	if err != nil || plan.ObjectVersion.Version != identity.Version {
		return nil, nil, fault.Conflict("目标合同关联不能应用于当前服务对象")
	}
	association = plan.Association
	projection := &operatorMappingAssociationAfter{ServiceObjectID: association.ServiceObjectID, ObjectVersion: association.ObjectVersion,
		AssociationVersion: association.AssociationVersion, MaterialID: association.MaterialID, MaterialVersion: association.MaterialVersion,
		DocumentID: association.DocumentID, ParsedDocumentID: association.ParsedDocumentID, SourceRunID: association.SourceRunID,
		SourceAttemptID: association.SourceAttemptID, ObjectRef: association.ObjectRef, FactRefs: association.FactRefs,
		FactsSHA256: association.FactsSHA256, BasisRefs: append(json.RawMessage(nil), association.BasisRefs...)}
	return &association, projection, nil
}

func bindMappingAssociation(plan *OperatorMappingChangePlan, association *operatorMappingAssociationAfter) error {
	if association == nil {
		return nil
	}
	digest, err := operatorMappingApprovedDigest(canonicalOperatorMappingState(plan.NextVersion), nil, association)
	if err == nil {
		plan.AfterSHA256 = digest
	}
	return err
}

func mappingImpactWithAssociation(head OperatorMappingHead, version OperatorMappingVersion, identity ServiceObjectVersion,
	current ServiceObjectContractAssociation, target *operatorMappingAssociationAfter) (string, error) {
	base, err := operatorMappingImpactSHA256(head, version, identity, current)
	if err != nil || target == nil {
		return base, err
	}
	return operatorHash(struct {
		CurrentImpact string                           `json:"current_impact"`
		Target        *operatorMappingAssociationAfter `json:"target_association"`
	}{base, target})
}

// PreviewOperatorMappingChange returns the exact current tuple and proposed
// after state. The mutation method recomputes every field under the case lock.
func (s *Store) PreviewOperatorMappingChange(ctx context.Context, user accounts.User, caseID, mappingID string,
	cas OperatorMappingChangeCAS, patch OperatorMappingChangePatch, basisRefs json.RawMessage) (OperatorMappingChangePreview, error) {
	var out OperatorMappingChangePreview
	current, _, _, _, _, err := s.operatorCaseState(ctx, user, caseID, access.Manage)
	if err != nil {
		return out, err
	}
	basisRefs, err = canonicalRawJSON(basisRefs)
	if err != nil || len(basisRefs) == 0 || string(basisRefs) == "null" {
		return out, fault.Invalid("必须提供可验证的更正依据")
	}
	err = s.withOperatorTx(ctx, current, caseID, false, func(tx pgx.Tx) error {
		fresh, err := loadState(ctx, tx, false)
		if err != nil {
			return err
		}
		freshCase, err := s.visibleCase(&fresh, current, caseID)
		if err != nil {
			return err
		}
		if !authorizeCase(freshCase, current, access.Manage) {
			return fault.Forbidden("当前授权不能维护对象映射")
		}
		if err := businessWritable(freshCase); err != nil {
			return err
		}
		head, version, identity, association, err := loadOperatorMappingCurrent(ctx, tx, accounts.Tenant(current), freshCase.OwnerOrgID, caseID, mappingID)
		if err != nil {
			return err
		}
		if !operatorMappingCASMatches(head, version, cas) {
			return fault.Conflict("对象映射版本已变化，请刷新后重试")
		}
		if err := validateMappingChangeBasis(fresh, &freshCase, basisRefs, association); err != nil {
			return err
		}
		patch, err := normalizeOperatorMappingPatch(patch, version.Binding)
		if err != nil {
			return err
		}
		if patch.Identity != nil {
			identityBasis, err := canonicalRawJSON(patch.Identity.BasisRefs)
			if err != nil || !bytes.Equal(identityBasis, basisRefs) {
				return fault.Invalid("身份更正依据必须与已验证的材料依据一致")
			}
		}
		plan, err := PlanOperatorMappingChange(head, version, cas, patch, "binding_preview", current.ID, time.Now().UTC())
		if err != nil {
			return fault.Conflict("对象映射更正内容或版本不匹配")
		}
		_, targetAssociation, err := s.mappingAssociationTarget(ctx, tx, fresh, current, freshCase, head, version, identity, patch.ContractAssociation)
		if err != nil {
			return err
		}
		if err := bindMappingAssociation(&plan, targetAssociation); err != nil {
			return err
		}
		impactSHA, err := mappingImpactWithAssociation(head, version, identity, association, targetAssociation)
		if err != nil {
			return err
		}
		var identityAfter *ServiceObjectVersion
		if patch.Identity != nil {
			proposed, err := proposedOperatorIdentityVersion(head.ServiceObjectID, identity.Version, *patch.Identity, patch.EffectiveFrom)
			if err != nil {
				return fault.Invalid("身份更正内容无效")
			}
			identityAfter = &proposed
		}
		out = OperatorMappingChangePreview{MappingID: mappingID, ServiceObjectID: head.ServiceObjectID, CAS: cas,
			Before: version, After: plan.NextVersion, BeforeSHA256: plan.BeforeSHA256, AfterSHA256: plan.AfterSHA256,
			IdentityAfter: identityAfter, AssociationAfter: targetAssociation, ImpactSHA256: impactSHA, BasisRefs: append(json.RawMessage(nil), basisRefs...)}
		return nil
	})
	return out, err
}

// ProposeOperatorMappingChange stores an immutable two-person proposal. It
// never mutates the lineage head; approval is a separate CAS-bound decision.
func (s *Store) ProposeOperatorMappingChange(ctx context.Context, user accounts.User, caseID, mappingID string,
	input OperatorMappingChangeCreateInput, key string) (OperatorMappingChangeRequest, bool, error) {
	var empty OperatorMappingChangeRequest
	if s.ReadOnly() {
		return empty, false, fault.New(503, "read_only", "只读模式下不能提交对象映射更正")
	}
	current, err := trustedUser(user)
	if err != nil {
		return empty, false, err
	}
	if _, _, _, _, _, err := s.operatorCaseState(ctx, current, caseID, access.Manage); err != nil {
		return empty, false, err
	}
	if err := validateIdempotencyKey(key); err != nil {
		return empty, false, err
	}
	caseID, mappingID = strings.TrimSpace(caseID), strings.TrimSpace(mappingID)
	input.BasisRefs, err = canonicalRawJSON(input.BasisRefs)
	if err != nil || len(input.BasisRefs) == 0 || string(input.BasisRefs) == "null" {
		return empty, false, fault.Invalid("必须提供可验证的更正依据")
	}
	input.Patch, err = normalizeOperatorMappingPatch(input.Patch, OperatorObjectBinding{})
	if err != nil {
		return empty, false, fault.Invalid("对象映射更正内容无效")
	}
	if !validOperatorDigest(input.ImpactSHA256) {
		return empty, false, fault.Invalid("影响预览摘要无效")
	}
	requestSHA, err := operatorHash(struct {
		CaseID, MappingID string
		Input             OperatorMappingChangeCreateInput
	}{caseID, mappingID, input})
	if err != nil {
		return empty, false, err
	}
	var result OperatorMappingChangeRequest
	replayed := false
	err = s.withOperatorTx(ctx, current, caseID, true, func(tx pgx.Tx) error {
		fresh, err := loadState(ctx, tx, true)
		if err != nil {
			return err
		}
		caseItem, err := s.visibleCase(&fresh, current, caseID)
		if err != nil {
			return err
		}
		if !authorizeCase(caseItem, current, access.Manage) {
			return fault.Forbidden("当前授权不能维护对象映射")
		}
		if err := businessWritable(caseItem); err != nil {
			return err
		}
		var receipt []byte
		var storedHash string
		err = tx.QueryRow(ctx, `SELECT response_body,request_sha256 FROM operator_mapping_change_requests
			WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`,
			current.TenantID, caseItem.OwnerOrgID, caseID, current.ID, key).Scan(&receipt, &storedHash)
		if err == nil {
			if storedHash != requestSHA {
				return fault.Conflict("同一幂等键对应了不同的映射更正")
			}
			if err := json.Unmarshal(receipt, &result); err != nil {
				return fault.New(500, "integrity_error", "对象映射更正幂等回执损坏")
			}
			replayed = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		head, version, identity, association, err := loadOperatorMappingCurrent(ctx, tx, current.TenantID, caseItem.OwnerOrgID, caseID, mappingID)
		if err != nil {
			return err
		}
		if !operatorMappingCASMatches(head, version, input.CAS) {
			return fault.Conflict("对象映射版本已变化，请刷新后重试")
		}
		_, targetAssociation, err := s.mappingAssociationTarget(ctx, tx, fresh, current, caseItem, head, version, identity, input.Patch.ContractAssociation)
		if err != nil {
			return err
		}
		impactSHA, err := mappingImpactWithAssociation(head, version, identity, association, targetAssociation)
		if err != nil {
			return err
		}
		if input.ImpactSHA256 != impactSHA {
			return fault.Conflict("对象映射影响预览已变化，请重新查看")
		}
		if err := validateMappingChangeBasis(fresh, &caseItem, input.BasisRefs, association); err != nil {
			return err
		}
		if input.Patch.Identity != nil {
			identityBasis, err := canonicalRawJSON(input.Patch.Identity.BasisRefs)
			if err != nil || !bytes.Equal(identityBasis, input.BasisRefs) {
				return fault.Invalid("身份更正依据必须与已验证的材料依据一致")
			}
		}
		plan, err := PlanOperatorMappingChange(head, version, input.CAS, input.Patch, "binding_proposal", current.ID, time.Now().UTC())
		if err != nil {
			return fault.Conflict("对象映射更正内容或版本不匹配")
		}
		if err := bindMappingAssociation(&plan, targetAssociation); err != nil {
			return err
		}
		patchJSON, err := json.Marshal(input.Patch)
		if err != nil {
			return err
		}
		beforeJSON, err := json.Marshal(canonicalOperatorMappingState(version))
		if err != nil {
			return err
		}
		var afterJSON []byte
		if targetAssociation != nil {
			afterJSON, err = json.Marshal(operatorMappingApprovedAfter{Mapping: canonicalOperatorMappingState(plan.NextVersion), Association: targetAssociation})
		} else if input.Patch.Identity == nil {
			afterJSON, err = json.Marshal(canonicalOperatorMappingState(plan.NextVersion))
		} else {
			proposedIdentity, identityErr := proposedOperatorIdentityVersion(head.ServiceObjectID, identity.Version, *input.Patch.Identity, input.Patch.EffectiveFrom)
			if identityErr != nil {
				return fault.Invalid("身份更正内容无效")
			}
			afterJSON, err = json.Marshal(operatorMappingApprovedAfter{Mapping: canonicalOperatorMappingState(plan.NextVersion), Identity: operatorIdentityAfterProjection(proposedIdentity)})
		}
		if err != nil {
			return err
		}
		patchDigest := sha256.Sum256(patchJSON)
		requestID, err := newID("omcr_")
		if err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		result = OperatorMappingChangeRequest{RequestID: requestID, CaseID: caseID, MappingID: mappingID,
			ServiceObjectID: head.ServiceObjectID, Kind: input.Patch.Kind,
			ExpectedMappingVersion: input.CAS.ExpectedMappingVersion, ExpectedBindingVersion: input.CAS.ExpectedBindingVersion,
			ExpectedObjectVersion: input.CAS.ExpectedObjectVersion, ExpectedAssociationVersion: input.CAS.ExpectedAssociationVersion,
			Before: beforeJSON, BeforeSHA256: plan.BeforeSHA256, After: afterJSON, AfterSHA256: plan.AfterSHA256,
			Patch: input.Patch, PatchSHA256: hex.EncodeToString(patchDigest[:]), BasisRefs: append(json.RawMessage(nil), input.BasisRefs...),
			ImpactSHA256: input.ImpactSHA256, State: "pending_decision",
			ProposedBy: TrustedPrincipalProjection{UserID: current.ID, Issuer: current.Issuer, Subject: current.Subject}, CreatedAt: now}
		responseBody, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO operator_mapping_change_requests
			(tenant_id,org_id,case_id,request_id,mapping_id,service_object_id,change_kind,expected_mapping_version,expected_binding_version,
			expected_object_version,expected_association_version,before_json,before_sha256,after_json,after_sha256,patch_json,patch_sha256,basis_refs,
			impact_sha256,proposer_user_id,proposer_issuer,proposer_subject,actor_id,idempotency_key,request_sha256,response_status,response_body,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13,$14::jsonb,$15,$16::jsonb,$17,$18::jsonb,$19,$20,$21,$22,$20,$23,$24,201,$25,$26)`,
			current.TenantID, caseItem.OwnerOrgID, caseID, requestID, mappingID, head.ServiceObjectID, input.Patch.Kind,
			input.CAS.ExpectedMappingVersion, input.CAS.ExpectedBindingVersion, input.CAS.ExpectedObjectVersion, input.CAS.ExpectedAssociationVersion,
			beforeJSON, plan.BeforeSHA256, afterJSON, plan.AfterSHA256, patchJSON, plan.PatchSHA256, input.BasisRefs,
			input.ImpactSHA256, current.ID, current.Issuer, current.Subject, key, requestSHA, responseBody, now)
		return err
	})
	return result, replayed, err
}

func (s *Store) GetOperatorMappingChange(ctx context.Context, user accounts.User, caseID, requestID string) (OperatorMappingChangeRequest, *OperatorMappingChangeDecision, error) {
	ctx = commandContext(ctx, user)
	current, err := trustedUser(user)
	if err != nil {
		return OperatorMappingChangeRequest{}, nil, err
	}
	if _, _, _, _, _, err := s.operatorCaseState(ctx, current, caseID, access.Read); err != nil {
		return OperatorMappingChangeRequest{}, nil, err
	}
	var request OperatorMappingChangeRequest
	var decision *OperatorMappingChangeDecision
	err = s.withOperatorTx(ctx, current, caseID, false, func(tx pgx.Tx) error {
		fresh, err := loadState(ctx, tx, false)
		if err != nil {
			return err
		}
		caseItem, err := s.visibleCase(&fresh, current, caseID)
		if err != nil {
			return err
		}
		if !authorizeCase(caseItem, current, access.Read) {
			return fault.NotFound("没有找到对象映射更正")
		}
		request, err = loadOperatorMappingChangeRequest(ctx, tx, current.TenantID, caseItem.OwnerOrgID, caseID, requestID)
		if err != nil {
			return err
		}
		var item OperatorMappingChangeDecision
		err = tx.QueryRow(ctx, `SELECT decision_id,request_id,decision,reason,decider_user_id,decider_issuer,decider_subject,created_at
			FROM operator_mapping_change_decisions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND request_id=$4`,
			current.TenantID, caseItem.OwnerOrgID, caseID, requestID).Scan(&item.DecisionID, &item.RequestID, &item.Decision,
			&item.Reason, &item.DecidedBy.UserID, &item.DecidedBy.Issuer, &item.DecidedBy.Subject, &item.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		decision = &item
		request.State = item.Decision
		return nil
	})
	return request, decision, err
}

// DecideOperatorMappingChange appends exactly one trusted-principal decision.
// Approval appends the next immutable mapping version and advances the lineage
// head with the same CAS; rejection leaves that head untouched.
func (s *Store) DecideOperatorMappingChange(ctx context.Context, user accounts.User, caseID, requestID string,
	input OperatorMappingChangeDecisionInput, key string) (OperatorMappingChangeDecisionResult, bool, error) {
	var empty OperatorMappingChangeDecisionResult
	if s.ReadOnly() {
		return empty, false, fault.New(503, "read_only", "只读模式下不能决定对象映射更正")
	}
	current, err := trustedUser(user)
	if err != nil {
		return empty, false, err
	}
	if !accounts.CanDecide(current.Role) {
		return empty, false, fault.Forbidden("当前角色没有对象映射更正决定权限")
	}
	if _, _, _, _, _, err := s.operatorCaseState(ctx, current, caseID, access.Decide); err != nil {
		return empty, false, err
	}
	if err := validateIdempotencyKey(key); err != nil {
		return empty, false, err
	}
	input.Decision = strings.TrimSpace(input.Decision)
	input.Reason = strings.TrimSpace(input.Reason)
	if (input.Decision != "approve" && input.Decision != "reject") || input.Reason == "" || len(input.Reason) > 2000 ||
		input.ExpectedMappingVersion < 1 || input.ExpectedBindingVersion == "" || input.ExpectedObjectVersion < 1 || input.ExpectedAssociationVersion < 1 ||
		!validOperatorDigest(input.ExpectedAfterSHA256) || !validOperatorDigest(input.ExpectedImpactSHA256) {
		return empty, false, fault.Invalid("决定内容或预期版本无效")
	}
	decisionStatus := "rejected"
	if input.Decision == "approve" {
		decisionStatus = "approved"
	}
	requestSHA, err := operatorHash(struct {
		CaseID, RequestID string
		Input             OperatorMappingChangeDecisionInput
	}{caseID, requestID, input})
	if err != nil {
		return empty, false, err
	}
	var result OperatorMappingChangeDecisionResult
	replayed := false
	err = s.withOperatorTx(ctx, current, caseID, true, func(tx pgx.Tx) error {
		fresh, err := loadState(ctx, tx, true)
		if err != nil {
			return err
		}
		caseItem, err := s.visibleCase(&fresh, current, caseID)
		if err != nil {
			return err
		}
		if !authorizeCase(caseItem, current, access.Decide) {
			return fault.NotFound("没有找到对象映射更正")
		}
		if err := businessWritable(caseItem); err != nil {
			return err
		}
		var receipt []byte
		var storedHash string
		err = tx.QueryRow(ctx, `SELECT response_body,decision_request_sha256 FROM operator_mapping_change_decisions
			WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`,
			current.TenantID, caseItem.OwnerOrgID, caseID, current.ID, key).Scan(&receipt, &storedHash)
		if err == nil {
			if storedHash != requestSHA {
				return fault.Conflict("同一幂等键对应了不同的映射决定")
			}
			if err := json.Unmarshal(receipt, &result); err != nil {
				return fault.New(500, "integrity_error", "对象映射决定回执损坏")
			}
			replayed = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		request, err := loadOperatorMappingChangeRequest(ctx, tx, current.TenantID, caseItem.OwnerOrgID, caseID, requestID)
		if err != nil {
			return err
		}
		var existingDecision string
		err = tx.QueryRow(ctx, `SELECT decision FROM operator_mapping_change_decisions
			WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND request_id=$4`,
			current.TenantID, caseItem.OwnerOrgID, caseID, requestID).Scan(&existingDecision)
		if err == nil {
			return fault.Conflict("该对象映射更正已由其他决定完成")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		actor := TrustedPrincipalProjection{UserID: current.ID, Issuer: current.Issuer, Subject: current.Subject}
		if !operatorMappingDecisionIndependent(request, actor) {
			return fault.Forbidden("更正提出人不能批准或拒绝同一更正")
		}
		head, version, identity, association, err := loadOperatorMappingCurrent(ctx, tx, current.TenantID, caseItem.OwnerOrgID, caseID, request.MappingID)
		if err != nil {
			return err
		}
		cas := OperatorMappingChangeCAS{ExpectedMappingVersion: request.ExpectedMappingVersion,
			ExpectedBindingVersion: request.ExpectedBindingVersion, ExpectedObjectVersion: request.ExpectedObjectVersion,
			ExpectedAssociationVersion: request.ExpectedAssociationVersion}
		target, targetAssociation, err := s.mappingAssociationTarget(ctx, tx, fresh, current, caseItem, head, version, identity, request.Patch.ContractAssociation)
		if err != nil {
			return err
		}
		impactSHA, err := mappingImpactWithAssociation(head, version, identity, association, targetAssociation)
		if err != nil {
			return err
		}
		if !operatorMappingCASMatches(head, version, cas) || request.AfterSHA256 != input.ExpectedAfterSHA256 ||
			request.ImpactSHA256 != input.ExpectedImpactSHA256 || impactSHA != request.ImpactSHA256 {
			return fault.Conflict("对象映射版本或影响范围已变化，请重新查看")
		}
		if err := validateMappingChangeBasis(fresh, &caseItem, request.BasisRefs, association); err != nil {
			return err
		}
		if request.Patch.Identity != nil {
			identityBasis, err := canonicalRawJSON(request.Patch.Identity.BasisRefs)
			requestBasis, requestBasisErr := canonicalRawJSON(request.BasisRefs)
			if err != nil || requestBasisErr != nil || !bytes.Equal(identityBasis, requestBasis) {
				return fault.Conflict("身份更正依据与提案不一致")
			}
		}
		proposalPlan, err := PlanOperatorMappingChange(head, version, cas, request.Patch, "binding_proposal", request.ProposedBy.UserID, time.Now().UTC())
		if err != nil {
			return fault.Conflict("对象映射更正摘要与提案内容不一致")
		}
		if err := bindMappingAssociation(&proposalPlan, targetAssociation); err != nil || proposalPlan.AfterSHA256 != request.AfterSHA256 {
			return fault.Conflict("对象映射更正摘要与提案内容不一致")
		}
		if targetAssociation != nil {
			var saved operatorMappingApprovedAfter
			if err := json.Unmarshal(request.After, &saved); err != nil {
				return fault.Conflict("目标合同关联提案记录损坏")
			}
			savedDigest, err := operatorMappingApprovedDigest(saved.Mapping, saved.Identity, saved.Association)
			if err != nil || savedDigest != request.AfterSHA256 {
				return fault.Conflict("目标合同关联提案摘要不一致")
			}
		}
		decisionID, err := newID("omcd_")
		if err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		bindingID := version.Binding.Version
		mappingVersion := head.CurrentVersion
		if decisionStatus == "approved" {
			bindingID, err = newID("bind_")
			if err != nil {
				return err
			}
			mappingVersion++
		}
		result = OperatorMappingChangeDecisionResult{RequestID: requestID, DecisionID: decisionID, Decision: decisionStatus,
			MappingID: request.MappingID, MappingVersion: mappingVersion, BindingVersion: bindingID}
		responseBody, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO operator_mapping_change_decisions
			(tenant_id,org_id,case_id,request_id,decision_id,mapping_id,service_object_id,expected_mapping_version,expected_binding_version,
			expected_object_version,expected_association_version,decision,reason,decider_user_id,decider_issuer,decider_subject,request_sha256,
			actor_id,idempotency_key,decision_request_sha256,response_status,response_body,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$14,$18,$19,201,$20,$21)`,
			current.TenantID, caseItem.OwnerOrgID, caseID, requestID, decisionID, request.MappingID, request.ServiceObjectID,
			cas.ExpectedMappingVersion, cas.ExpectedBindingVersion, cas.ExpectedObjectVersion, cas.ExpectedAssociationVersion,
			decisionStatus, input.Reason, current.ID, current.Issuer, current.Subject, request.requestSHA256, key, requestSHA, responseBody, now)
		if err != nil {
			return err
		}
		if decisionStatus != "approved" {
			return nil
		}
		plan, err := PlanOperatorMappingChange(head, version, cas, request.Patch, bindingID, current.ID, now)
		if err != nil {
			return fault.Conflict("映射更正内容已失效")
		}
		if err := bindMappingAssociation(&plan, targetAssociation); err != nil {
			return err
		}
		newBinding := plan.NextVersion.Binding
		objectVersion, associationVersion := request.ExpectedObjectVersion, request.ExpectedAssociationVersion
		if target != nil {
			target.ConfirmedBy, target.ConfirmedAt, target.CreatedAt = current.ID, now, now
			target.IdentityChangeDecisionID = decisionID
			target.PayloadSHA256, err = canonicalServiceObjectAssociationSHA256(*target)
			if err != nil {
				return err
			}
			if err := insertServiceObjectAssociation(ctx, tx, current, caseItem, *target); err != nil {
				return err
			}
			associationVersion = target.AssociationVersion
		}
		if request.Patch.Identity != nil {
			proposed, err := proposedOperatorIdentityVersion(request.ServiceObjectID, identity.Version, *request.Patch.Identity, request.Patch.EffectiveFrom)
			if err != nil {
				return fault.Conflict("身份更正内容已失效")
			}
			approval := verifiedServiceObjectIdentityChange{decisionID: decisionID, expectedObjectVersion: request.ExpectedObjectVersion,
				expectedAssociation: request.ExpectedAssociationVersion, beforePayloadSHA256: identity.PayloadSHA256,
				afterPayloadSHA256: proposed.PayloadSHA256, effectiveFrom: proposed.EffectiveFrom,
				basisRefs: append(json.RawMessage(nil), proposed.BasisRefs...), validatedByDecisionSvc: true}
			identityPlan, err := PlanServiceObjectIdentityChange(ServiceObjectHead{ID: request.ServiceObjectID, CurrentVersion: identity.Version,
				CurrentAssociationVersion: request.ExpectedAssociationVersion}, identity, association, proposed, approval, current.ID, now)
			if err != nil {
				return fault.Conflict("服务对象身份版本已变化")
			}
			identityPlan.ObjectVersion.ChangeDecisionID = decisionID
			if err := insertServiceObjectVersion(ctx, tx, current, caseItem, identityPlan.ObjectVersion); err != nil {
				return err
			}
			identityPlan.Association.IdentityChangeDecisionID = decisionID
			identityPlan.Association.PayloadSHA256, err = canonicalServiceObjectAssociationSHA256(identityPlan.Association)
			if err != nil {
				return err
			}
			if err := insertServiceObjectAssociation(ctx, tx, current, caseItem, identityPlan.Association); err != nil {
				return err
			}
			updatedObject, err := tx.Exec(ctx, `UPDATE service_objects SET current_version=$1,current_association_version=$2
				WHERE tenant_id=$3 AND org_id=$4 AND case_id=$5 AND service_object_id=$6 AND current_version=$7 AND current_association_version=$8`,
				identityPlan.Head.CurrentVersion, identityPlan.Head.CurrentAssociationVersion, current.TenantID, caseItem.OwnerOrgID, caseID,
				request.ServiceObjectID, request.ExpectedObjectVersion, request.ExpectedAssociationVersion)
			if err != nil {
				return err
			}
			if updatedObject.RowsAffected() != 1 {
				return fault.Conflict("服务对象身份版本已被并发更新")
			}
			objectVersion, associationVersion = identityPlan.ObjectVersion.Version, identityPlan.Association.AssociationVersion
		}
		newBinding.MappingID, newBinding.MappingVersion = request.MappingID, plan.NextVersion.MappingVersion
		newBinding.ServiceObjectID, newBinding.ObjectVersion, newBinding.AssociationVersion = request.ServiceObjectID, objectVersion, associationVersion
		newBinding.requestSHA256 = request.AfterSHA256
		newBindingJSON, err := json.Marshal(newBinding)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO operator_object_bindings
			(tenant_id,org_id,case_id,binding_id,binding_version,status,contract_object,external_object,connector_id,external_object_id,
			source_system,confirmed_by,confirmed_at,effective_from,effective_until,actor_id,idempotency_key,request_sha256,created_at,
			mapping_id,mapping_version,service_object_id,object_version,association_version,mapping_change_decision_id,mapping_change_decision,response_status,response_body)
			VALUES ($1,$2,$3,$4,$4,$5,$6::jsonb,$7::jsonb,$8,$9,$10,$11,$12,$13,$14,$11,$15,$16,$12,
			$17,$18,$19,$20,$21,$22,'approved',201,$23)`,
			current.TenantID, caseItem.OwnerOrgID, caseID, bindingID, newBinding.Status, marshalOperatorMappingJSON(newBinding.ContractObject), marshalOperatorMappingJSON(newBinding.ExternalObject),
			newBinding.ExternalObject.ConnectorID, newBinding.ExternalObject.ExternalObjectID, newBinding.SourceSystem, current.ID, now,
			newBinding.EffectiveFrom, newBinding.EffectiveUntil, "mapping-change:"+decisionID, plan.AfterSHA256,
			request.MappingID, mappingVersion, request.ServiceObjectID, objectVersion, associationVersion,
			decisionID, newBindingJSON)
		if err != nil {
			return err
		}
		if target != nil {
			updatedObject, err := tx.Exec(ctx, `UPDATE service_objects SET current_association_version=$1
				WHERE tenant_id=$2 AND org_id=$3 AND case_id=$4 AND service_object_id=$5 AND current_version=$6 AND current_association_version=$7`,
				associationVersion, current.TenantID, caseItem.OwnerOrgID, caseID, request.ServiceObjectID,
				request.ExpectedObjectVersion, request.ExpectedAssociationVersion)
			if err != nil {
				return err
			}
			if updatedObject.RowsAffected() != 1 {
				return fault.Conflict("服务对象合同关联已被并发更新")
			}
		}
		command, err := tx.Exec(ctx, `UPDATE operator_mapping_lineages SET current_version=$1,current_binding_version=$2,updated_at=$3
			WHERE tenant_id=$4 AND org_id=$5 AND case_id=$6 AND mapping_id=$7 AND current_version=$8 AND current_binding_version=$9`,
			mappingVersion, bindingID, now, current.TenantID, caseItem.OwnerOrgID, caseID, request.MappingID,
			cas.ExpectedMappingVersion, cas.ExpectedBindingVersion)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return fault.Conflict("对象映射版本已被并发更新")
		}
		return nil
	})
	return result, replayed, err
}

func loadOperatorMappingChangeRequest(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, requestID string) (OperatorMappingChangeRequest, error) {
	var item OperatorMappingChangeRequest
	var beforeRaw, afterRaw, patchRaw, basisRaw []byte
	err := tx.QueryRow(ctx, `SELECT request_id,case_id,mapping_id,service_object_id,change_kind,expected_mapping_version,expected_binding_version,
		expected_object_version,expected_association_version,before_json,before_sha256,after_json,after_sha256,patch_json,patch_sha256,basis_refs,
		impact_sha256,proposer_user_id,proposer_issuer,proposer_subject,request_sha256,created_at
		FROM operator_mapping_change_requests WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND request_id=$4`,
		tenantID, orgID, caseID, requestID).Scan(&item.RequestID, &item.CaseID, &item.MappingID, &item.ServiceObjectID, &item.Kind,
		&item.ExpectedMappingVersion, &item.ExpectedBindingVersion, &item.ExpectedObjectVersion, &item.ExpectedAssociationVersion,
		&beforeRaw, &item.BeforeSHA256, &afterRaw, &item.AfterSHA256, &patchRaw, &item.PatchSHA256, &basisRaw,
		&item.ImpactSHA256, &item.ProposedBy.UserID, &item.ProposedBy.Issuer, &item.ProposedBy.Subject, &item.requestSHA256, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, fault.NotFound("没有找到对象映射更正")
	}
	if err != nil {
		return item, err
	}
	item.Before, item.After = append(json.RawMessage(nil), beforeRaw...), append(json.RawMessage(nil), afterRaw...)
	item.BasisRefs = append(json.RawMessage(nil), basisRaw...)
	if json.Unmarshal(patchRaw, &item.Patch) != nil || json.Unmarshal(item.Before, new(map[string]any)) != nil || json.Unmarshal(item.After, new(map[string]any)) != nil {
		return OperatorMappingChangeRequest{}, fault.New(500, "integrity_error", "对象映射更正记录损坏")
	}
	var decision string
	err = tx.QueryRow(ctx, `SELECT decision FROM operator_mapping_change_decisions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND request_id=$4`,
		tenantID, orgID, caseID, requestID).Scan(&decision)
	if err == nil {
		item.State = decision
	} else if errors.Is(err, pgx.ErrNoRows) {
		item.State = "pending_decision"
	} else {
		return OperatorMappingChangeRequest{}, err
	}
	return item, nil
}

func marshalOperatorMappingJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func loadOperatorMappingCurrent(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, mappingID string) (OperatorMappingHead, OperatorMappingVersion, ServiceObjectVersion, ServiceObjectContractAssociation, error) {
	var head OperatorMappingHead
	err := tx.QueryRow(ctx, `SELECT case_id,mapping_id,service_object_id,current_version,current_binding_version,created_by,created_at,updated_at
		FROM operator_mapping_lineages WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND mapping_id=$4`,
		tenantID, orgID, caseID, mappingID).Scan(&head.CaseID, &head.MappingID, &head.ServiceObjectID, &head.CurrentVersion,
		&head.CurrentBindingVersion, &head.CreatedBy, &head.CreatedAt, &head.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return head, OperatorMappingVersion{}, ServiceObjectVersion{}, ServiceObjectContractAssociation{}, fault.NotFound("没有找到对象映射")
	}
	if err != nil {
		return head, OperatorMappingVersion{}, ServiceObjectVersion{}, ServiceObjectContractAssociation{}, err
	}
	binding, err := loadOperatorBindingByVersion(ctx, tx, tenantID, orgID, caseID, head.CurrentBindingVersion)
	if err != nil {
		return head, OperatorMappingVersion{}, ServiceObjectVersion{}, ServiceObjectContractAssociation{}, err
	}
	version := OperatorMappingVersion{MappingID: head.MappingID, MappingVersion: head.CurrentVersion,
		ServiceObjectID: head.ServiceObjectID, ObjectVersion: binding.ObjectVersion, AssociationVersion: binding.AssociationVersion, Binding: binding}
	identity, err := loadServiceObjectVersionExact(ctx, tx, tenantID, orgID, caseID, head.ServiceObjectID, binding.ObjectVersion)
	if err != nil {
		return head, version, identity, ServiceObjectContractAssociation{}, err
	}
	association, err := loadServiceObjectAssociation(ctx, tx, tenantID, orgID, caseID, head.ServiceObjectID, binding.AssociationVersion)
	return head, version, identity, association, err
}

func loadOperatorBindingByVersion(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, bindingVersion string) (OperatorObjectBinding, error) {
	var out OperatorObjectBinding
	var receipt []byte
	err := tx.QueryRow(ctx, `SELECT case_id,binding_id,binding_version,status,contract_object,external_object,
		source_system,confirmed_by,confirmed_at,effective_from,effective_until,created_at,request_sha256,
		COALESCE(mapping_id,''),COALESCE(mapping_version,0),COALESCE(service_object_id,''),COALESCE(object_version,0),COALESCE(association_version,0),response_body
		FROM operator_object_bindings WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND binding_version=$4`,
		tenantID, orgID, caseID, bindingVersion).Scan(&out.CaseID, &out.BindingID, &out.Version, &out.Status,
		&out.ContractObject, &out.ExternalObject, &out.SourceSystem, &out.ConfirmedBy, &out.ConfirmedAt,
		&out.EffectiveFrom, &out.EffectiveUntil, &out.CreatedAt, &out.requestSHA256, &out.MappingID, &out.MappingVersion,
		&out.ServiceObjectID, &out.ObjectVersion, &out.AssociationVersion, &receipt)
	if err == nil && len(receipt) > 0 {
		requestSHA := out.requestSHA256
		if err := json.Unmarshal(receipt, &out); err != nil {
			return OperatorObjectBinding{}, fault.New(500, "integrity_error", "对象映射响应回执损坏")
		}
		out.requestSHA256 = requestSHA
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.NotFound("没有找到对象映射版本")
	}
	return out, err
}

func operatorMappingCASMatches(head OperatorMappingHead, current OperatorMappingVersion, cas OperatorMappingChangeCAS) bool {
	return head.CurrentVersion == cas.ExpectedMappingVersion && head.CurrentBindingVersion == cas.ExpectedBindingVersion &&
		current.ObjectVersion == cas.ExpectedObjectVersion && current.AssociationVersion == cas.ExpectedAssociationVersion
}

func operatorMappingImpactSHA256(head OperatorMappingHead, current OperatorMappingVersion, identity ServiceObjectVersion, association ServiceObjectContractAssociation) (string, error) {
	return operatorHash(struct {
		CaseID      string
		Head        OperatorMappingHead
		Current     operatorMappingCanonicalState
		IdentitySHA string
		AssocSHA    string
	}{head.CaseID, head, canonicalOperatorMappingState(current), identity.PayloadSHA256, association.PayloadSHA256})
}

func mustOperatorMappingImpactSHA256(head OperatorMappingHead, current OperatorMappingVersion, identity ServiceObjectVersion, association ServiceObjectContractAssociation) string {
	digest, _ := operatorMappingImpactSHA256(head, current, identity, association)
	return digest
}

func validateMappingChangeBasis(st state, caseItem *Case, raw json.RawMessage, association ServiceObjectContractAssociation) error {
	validated, err := validateSubjectBasisRefs(st, caseItem, raw)
	if err != nil {
		return fault.Invalid("更正依据必须引用授权案件中可见的冻结材料")
	}
	var refs []materialAnchorInput
	if err := json.Unmarshal(validated, &refs); err != nil || len(refs) == 0 {
		return fault.Invalid("对象映射更正需要精确的材料依据")
	}
	for _, ref := range refs {
		if ref.MaterialID != association.MaterialID || ref.MaterialVersion != association.MaterialVersion || ref.DocumentID != association.DocumentID || ref.ParsedDocumentID != association.ParsedDocumentID {
			return fault.Invalid("更正依据必须固定当前服务对象关联的材料版本")
		}
	}
	return nil
}

func normalizeOperatorMappingPatch(patch OperatorMappingChangePatch, current OperatorObjectBinding) (OperatorMappingChangePatch, error) {
	patch.Kind = strings.TrimSpace(patch.Kind)
	if patch.EffectiveFrom.IsZero() {
		patch.EffectiveFrom = current.EffectiveFrom
	}
	if patch.EffectiveUntil.IsZero() {
		patch.EffectiveUntil = current.EffectiveUntil
	}
	patch.EffectiveFrom = patch.EffectiveFrom.UTC().Truncate(time.Microsecond)
	patch.EffectiveUntil = patch.EffectiveUntil.UTC().Truncate(time.Microsecond)
	switch patch.Kind {
	case OperatorMappingReplace:
		if patch.ExternalObject == nil || validateOperatorExternalObject(*patch.ExternalObject) != nil {
			return patch, errors.New("mapping replacement needs a valid external identity")
		}
	case OperatorMappingRetire, OperatorMappingReactivate:
		if patch.ExternalObject != nil {
			if validateOperatorExternalObject(*patch.ExternalObject) != nil {
				return patch, errors.New("mapping external identity is invalid")
			}
		}
	case OperatorMappingCorrectIdentity:
		if patch.Identity == nil || patch.ExternalObject != nil {
			return patch, errors.New("identity correction requires an identity payload only")
		}
		identity, err := normalizeOperatorIdentityPatch(*patch.Identity, patch.EffectiveFrom)
		if err != nil {
			return patch, err
		}
		if identity.State != ServiceObjectConfirmed {
			return patch, errors.New("identity correction must retain a confirmed object")
		}
		patch.Identity = operatorIdentityPatchFromVersion(identity)
	case OperatorMappingCorrectIdentityAndMapping:
		if patch.Identity == nil || patch.ExternalObject == nil || validateOperatorExternalObject(*patch.ExternalObject) != nil {
			return patch, errors.New("combined correction requires identity and external mapping payloads")
		}
		identity, err := normalizeOperatorIdentityPatch(*patch.Identity, patch.EffectiveFrom)
		if err != nil {
			return patch, err
		}
		if identity.State != ServiceObjectConfirmed {
			return patch, errors.New("identity correction must retain a confirmed object")
		}
		patch.Identity = operatorIdentityPatchFromVersion(identity)
	case OperatorMappingRetireServiceObject:
		if patch.Identity == nil || patch.ExternalObject != nil || patch.Identity.State != ServiceObjectInactive {
			return patch, errors.New("service object retirement requires an inactive identity payload")
		}
		if _, err := normalizeOperatorIdentityPatch(*patch.Identity, patch.EffectiveFrom); err != nil {
			return patch, err
		}
		identity, _ := normalizeOperatorIdentityPatch(*patch.Identity, patch.EffectiveFrom)
		patch.Identity = operatorIdentityPatchFromVersion(identity)
	default:
		return patch, errors.New("mapping correction kind is invalid")
	}
	if !validMappingInstant(patch.EffectiveFrom) || !validMappingInstant(patch.EffectiveUntil) || !patch.EffectiveFrom.Before(patch.EffectiveUntil) {
		return patch, errors.New("mapping effective interval is invalid")
	}
	return patch, nil
}

func operatorIdentityPatchFromVersion(version ServiceObjectVersion) *OperatorServiceIdentityPatch {
	return &OperatorServiceIdentityPatch{ObjectType: version.ObjectType, DisplayName: version.DisplayName,
		BusinessCode: version.BusinessCode, Aliases: append([]ServiceObjectAlias(nil), version.Aliases...),
		BasisSummary: version.BasisSummary, BasisRefs: append(json.RawMessage(nil), version.BasisRefs...), State: version.State}
}

func proposedOperatorIdentityVersion(serviceObjectID string, currentVersion int, patch OperatorServiceIdentityPatch, effectiveFrom time.Time) (ServiceObjectVersion, error) {
	version, err := normalizeOperatorIdentityPatch(patch, effectiveFrom)
	if err != nil || strings.TrimSpace(serviceObjectID) == "" || currentVersion < 1 {
		return ServiceObjectVersion{}, errors.New("identity correction payload is invalid")
	}
	version.ServiceObjectID = serviceObjectID
	version.Version = currentVersion + 1
	version.EffectiveFrom = effectiveFrom.UTC().Truncate(time.Microsecond)
	version.PayloadSHA256, err = canonicalServiceObjectIdentitySHA256(version)
	if err != nil {
		return ServiceObjectVersion{}, err
	}
	return version, nil
}

func operatorIdentityAfterProjection(version ServiceObjectVersion) *operatorMappingIdentityAfter {
	return &operatorMappingIdentityAfter{ObjectType: version.ObjectType, DisplayName: version.DisplayName,
		BusinessCode: version.BusinessCode, Aliases: version.Aliases, State: version.State, EffectiveAt: version.EffectiveFrom.UTC().Truncate(time.Microsecond),
		BasisRefs: append(json.RawMessage(nil), version.BasisRefs...), BasisSummary: version.BasisSummary, PayloadSHA: version.PayloadSHA256}
}

func normalizeOperatorIdentityPatch(patch OperatorServiceIdentityPatch, effectiveFrom time.Time) (ServiceObjectVersion, error) {
	if len(patch.BasisRefs) == 0 {
		return ServiceObjectVersion{}, errors.New("identity correction requires pinned basis references")
	}
	canonicalBasis, err := canonicalRawJSON(patch.BasisRefs)
	if err != nil || string(canonicalBasis) == "null" {
		return ServiceObjectVersion{}, errors.New("identity correction basis is invalid")
	}
	version, err := normalizeServiceObjectVersion(ServiceObjectVersion{
		ObjectType: patch.ObjectType, DisplayName: patch.DisplayName, BusinessCode: patch.BusinessCode,
		Aliases: patch.Aliases, State: patch.State, BasisRefs: canonicalBasis, BasisSummary: patch.BasisSummary,
		EffectiveFrom: effectiveFrom.UTC().Truncate(time.Microsecond),
	})
	if err != nil || (version.State != ServiceObjectConfirmed && version.State != ServiceObjectInactive) {
		return ServiceObjectVersion{}, errors.New("corrected identity payload is invalid")
	}
	return version, nil
}

func validOperatorDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
