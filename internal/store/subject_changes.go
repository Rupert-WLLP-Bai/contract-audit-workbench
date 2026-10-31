package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5/pgconn"
)

type SubjectChangeCreateInput struct {
	Target                 SubjectChangeTarget `json:"target"`
	ExpectedSubjectVersion int64               `json:"expected_subject_version"`
	ExpectedBindingVersion *int64              `json:"expected_binding_version,omitempty"`
	Patch                  json.RawMessage     `json:"patch"`
	BasisNote              string              `json:"basis_note"`
	BasisRefs              json.RawMessage     `json:"basis_refs"`
}

type SubjectChangeCreateResult struct {
	RequestID              string  `json:"request_id"`
	TargetKind             string  `json:"target_kind"`
	ExpectedSubjectVersion int64   `json:"expected_subject_version"`
	ExpectedBindingVersion *int64  `json:"expected_binding_version"`
	PayloadHash            string  `json:"payload_hash"`
	State                  string  `json:"state"`
	ProposedByDisplayName  string  `json:"proposed_by_display_name"`
	CanDecide              bool    `json:"can_decide"`
	DecisionBlockedReason  *string `json:"decision_blocked_reason"`
}

type SubjectChangeSummary struct {
	RequestID              string                     `json:"request_id"`
	TargetKind             string                     `json:"target_kind"`
	TargetSubjectID        string                     `json:"target_subject_id"`
	TargetBindingID        *string                    `json:"target_binding_id"`
	ExpectedSubjectVersion int64                      `json:"expected_subject_version"`
	ExpectedBindingVersion *int64                     `json:"expected_binding_version"`
	PayloadHash            string                     `json:"payload_hash"`
	State                  string                     `json:"state"`
	ProposedBy             TrustedPrincipalProjection `json:"proposed_by"`
	ProposedByDisplayName  string                     `json:"proposed_by_display_name"`
	CanDecide              bool                       `json:"can_decide"`
	DecisionBlockedReason  *string                    `json:"decision_blocked_reason"`
	CreatedAt              time.Time                  `json:"created_at"`
}

type SubjectChangeListResult struct {
	Items      []SubjectChangeSummary `json:"items"`
	NextCursor *string                `json:"next_cursor"`
}

type SubjectChangeDetailResult struct {
	Summary               SubjectChangeSummary `json:"summary"`
	Before                any                  `json:"before"`
	After                 any                  `json:"after"`
	BasisNote             string               `json:"basis_note"`
	BasisRefs             json.RawMessage      `json:"basis_refs"`
	ProposedByDisplayName string               `json:"proposed_by_display_name"`
	CanDecide             bool                 `json:"can_decide"`
	DecisionBlockedReason *string              `json:"decision_blocked_reason"`
}

type SubjectChangeDecisionInput struct {
	Decision               string `json:"decision"`
	ExpectedSubjectVersion int64  `json:"expected_subject_version"`
	ExpectedBindingVersion *int64 `json:"expected_binding_version,omitempty"`
	ExpectedPayloadHash    string `json:"expected_payload_hash"`
	ExpectedImpactETag     string `json:"expected_impact_etag"`
	Reason                 string `json:"reason"`
}

type SubjectChangeDecisionResult struct {
	RequestID               string `json:"request_id"`
	Decision                string `json:"decision"`
	AppliedSubjectVersion   *int64 `json:"applied_subject_version"`
	BindingVersionsAppended int    `json:"binding_versions_appended"`
}

type SubjectChangeDecisionResultWithReplay struct {
	Result   SubjectChangeDecisionResult
	Replayed bool
}

type SubjectChangeImpactResult struct {
	RequestID             string                        `json:"request_id"`
	TargetVersion         int64                         `json:"target_version"`
	PayloadHash           string                        `json:"payload_hash"`
	Before                any                           `json:"before"`
	After                 any                           `json:"after"`
	BasisRefs             json.RawMessage               `json:"basis_refs"`
	ImpactETag            string                        `json:"impact_etag"`
	VisibleAffectedCount  int                           `json:"visible_affected_count"`
	AuthorizedImpactCases []SubjectImpactCaseProjection `json:"authorized_impact_cases"`
	ProposedByDisplayName string                        `json:"proposed_by_display_name"`
	CanDecide             bool                          `json:"can_decide"`
	DecisionBlockedReason *string                       `json:"decision_blocked_reason"`
}

type SubjectImpactCaseProjection struct {
	CaseID string `json:"case_id"`
	Title  string `json:"case_title"`
	Status string `json:"business_status"`
}

type materialAnchorInput struct {
	Kind             string `json:"kind"`
	MaterialID       string `json:"material_id"`
	MaterialVersion  int    `json:"material_version"`
	DocumentID       string `json:"document_id"`
	ParsedDocumentID string `json:"parsed_document_id"`
	Start            int    `json:"start"`
	End              int    `json:"end"`
	QuoteDigest      string `json:"quote_digest"`
}

type materialAnchorProjection struct {
	Kind             string `json:"kind"`
	MaterialID       string `json:"material_id"`
	MaterialVersion  int    `json:"material_version"`
	DocumentID       string `json:"document_id"`
	ParsedDocumentID string `json:"parsed_document_id"`
	Filename         string `json:"filename"`
	Start            int    `json:"start"`
	End              int    `json:"end"`
	Quote            string `json:"quote"`
	QuoteDigest      string `json:"quote_digest"`
}

type subjectAfterPayload struct {
	SubjectVersion SubjectVersion `json:"subject_version"`
}

type bindingAfterPayload struct {
	SubjectID      string `json:"subject_id"`
	SubjectVersion int64  `json:"subject_version"`
	Role           string `json:"role"`
	BindingState   string `json:"binding_state"`
	SourceKind     string `json:"source_kind"`
	LegacyLabel    string `json:"legacy_label,omitempty"`
}

func (s *Store) ProposeSubjectChange(ctx context.Context, user accounts.User, caseID string, input SubjectChangeCreateInput, key string) (SubjectChangeCreateResult, bool, error) {
	if err := ctx.Err(); err != nil {
		return SubjectChangeCreateResult{}, false, err
	}
	if s.ReadOnly() {
		return SubjectChangeCreateResult{}, false, fault.New(503, "read_only", "只读模式下不能提交主体更正")
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectChangeCreateResult{}, false, err
	}
	if err := validateIdempotencyKey(key); err != nil {
		return SubjectChangeCreateResult{}, false, err
	}
	input.Target.Kind = strings.TrimSpace(input.Target.Kind)
	input.Target.SubjectID = strings.TrimSpace(input.Target.SubjectID)
	input.Target.BindingID = strings.TrimSpace(input.Target.BindingID)
	input.BasisNote = strings.TrimSpace(input.BasisNote)
	if input.Target.SubjectID == "" || input.ExpectedSubjectVersion < 1 || input.BasisNote == "" || len(input.Patch) == 0 || len(input.BasisRefs) == 0 {
		return SubjectChangeCreateResult{}, false, fault.Invalid("更正目标、版本、理由、修改内容和依据均为必填项")
	}
	input.Patch, err = canonicalRawJSON(input.Patch)
	if err != nil {
		return SubjectChangeCreateResult{}, false, fault.Invalid("patch必须是合法JSON值")
	}
	input.BasisRefs, err = canonicalRawJSON(input.BasisRefs)
	if err != nil {
		return SubjectChangeCreateResult{}, false, fault.Invalid("basis_refs必须是合法JSON值")
	}
	requestHash, err := canonicalSubjectHash(input)
	if err != nil {
		return SubjectChangeCreateResult{}, false, err
	}
	var result SubjectChangeCreateResult
	replayed := false
	err = s.updateCaseAsContext(ctx, current, caseID, func(st *state) error {
		caseItem, err := s.writableCase(st, current, caseID, access.Manage)
		if err != nil {
			return err
		}
		if prior, ok, err := readSubjectIdempotency(*st, current, "propose_subject_change", caseID, key, requestHash); err != nil {
			return err
		} else if ok {
			replayed = true
			return json.Unmarshal(prior.Response, &result)
		}
		subject, version, visible := visibleSubject(*st, current, input.Target.SubjectID)
		if !visible && input.Target.Kind == "case_subject_role" && input.Target.BindingID != "" {
			if head, binding, exists := currentSubjectBinding(*st, input.Target.BindingID); exists && head.TenantID == caseItem.TenantID && head.OrgID == caseItem.OwnerOrgID && head.CaseID == caseID && binding.SubjectID == input.Target.SubjectID {
				for _, candidate := range st.Subjects {
					if candidate.TenantID == caseItem.TenantID && candidate.ID == input.Target.SubjectID {
						if currentVersion, ok := subjectVersionAt(st.SubjectVersions, candidate.ID, candidate.CurrentVersion); ok {
							subject, version, visible = candidate, currentVersion, true
						}
						break
					}
				}
			}
		}
		if !visible || subject.TenantID != caseItem.TenantID || subject.CurrentVersion != input.ExpectedSubjectVersion {
			if !visible {
				return fault.NotFound("没有找到对应记录")
			}
			return fault.Conflict("主体版本已变化，请刷新后重试")
		}
		var after any
		switch input.Target.Kind {
		case "subject":
			if input.Target.BindingID != "" || input.ExpectedBindingVersion != nil || !caseHasSubject(st, caseID, subject.ID) {
				return fault.NotFound("没有找到对应记录")
			}
			version, err = applySubjectPatch(version, input.Patch)
			if err != nil {
				return fault.Invalid(err.Error())
			}
			version.Version = subject.CurrentVersion + 1
			version.SourceKind = "authorized_correction"
			version.BasisSummary = input.BasisNote
			version.CreatedByUser, version.CreatedByIssuer, version.CreatedBySubject = current.ID, current.Issuer, current.Subject
			version.CreatedAt = time.Now().UTC()
			version.PayloadHash = hashSubjectVersion(version)
			after = subjectAfterPayload{SubjectVersion: version}
		case "case_subject_role":
			if input.Target.BindingID == "" || input.ExpectedBindingVersion == nil || *input.ExpectedBindingVersion < 1 {
				return fault.Invalid("绑定更正必须指定绑定 ID 和当前绑定版本")
			}
			head, currentBinding, ok := currentSubjectBinding(*st, input.Target.BindingID)
			if !ok || head.TenantID != caseItem.TenantID || head.OrgID != caseItem.OwnerOrgID || head.CaseID != caseID || currentBinding.SubjectID != subject.ID {
				return fault.NotFound("没有找到对应记录")
			}
			if currentBinding.Version != *input.ExpectedBindingVersion {
				return fault.Conflict("主体关联版本已变化，请刷新后重试")
			}
			patch, err := parseCaseSubjectRolePatch(input.Patch, currentBinding.SubjectID)
			if err != nil {
				return fault.Invalid(err.Error())
			}
			afterBinding := bindingAfterPayload{SubjectID: currentBinding.SubjectID, SubjectVersion: currentBinding.SubjectVersion, Role: currentBinding.Role, BindingState: currentBinding.BindingState, SourceKind: "authorized_correction", LegacyLabel: currentBinding.LegacyLabel}
			if currentBinding.BindingState == "inactive" {
				// Explicit restoration re-pins the current subject identity. An inactive
				// binding may legitimately lag subject-wide corrections while inactive.
				afterBinding.SubjectVersion = version.Version
			}
			if patch.SubjectID != nil {
				targetSubject, targetVersion, targetVisible := visibleSubject(*st, current, *patch.SubjectID)
				if !targetVisible || targetSubject.CurrentVersion != *patch.ExpectedNewSubjectVersion {
					return fault.NotFound("没有找到对应记录")
				}
				afterBinding.SubjectID, afterBinding.SubjectVersion = targetSubject.ID, targetVersion.Version
			}
			if patch.Role != nil {
				afterBinding.Role = *patch.Role
			}
			if patch.BindingState != nil {
				afterBinding.BindingState = *patch.BindingState
			}
			after = afterBinding
		default:
			return fault.Invalid("target.kind无效")
		}
		basisRefs, err := validateSubjectBasisRefs(*st, caseItem, input.BasisRefs)
		if err != nil {
			return fault.Invalid(err.Error())
		}
		canonicalPayload, err := json.Marshal(after)
		if err != nil {
			return err
		}
		payloadHash := hashJSON(canonicalPayload)
		bindings := affectedSubjectBindings(*st, caseItem.TenantID, subject.ID, input.Target.Kind, input.Target.BindingID)
		bindingsHash, err := canonicalSubjectHash(bindings)
		if err != nil {
			return err
		}
		requestID, err := newID("sch_")
		if err != nil {
			return err
		}
		request := SubjectChangeRequest{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, RequestID: requestID, SourceCaseID: caseID, TargetKind: input.Target.Kind, TargetSubjectID: subject.ID, TargetBindingID: input.Target.BindingID, ExpectedSubjectVersion: subject.CurrentVersion, ExpectedBindingVersion: input.ExpectedBindingVersion, CanonicalPayload: canonicalPayload, CanonicalPayloadHash: payloadHash, BasisNote: input.BasisNote, BasisRefs: basisRefs, ExpectedBindingsHash: bindingsHash, ProposedBy: TrustedPrincipalProjection{UserID: current.ID, Issuer: current.Issuer, Subject: current.Subject}, CreatedAt: time.Now().UTC()}
		st.SubjectChangeRequests = append(st.SubjectChangeRequests, request)
		summary := subjectChangeSummary(*st, current, request)
		result = SubjectChangeCreateResult{RequestID: requestID, TargetKind: request.TargetKind, ExpectedSubjectVersion: request.ExpectedSubjectVersion, ExpectedBindingVersion: request.ExpectedBindingVersion, PayloadHash: payloadHash, State: "pending_decision", ProposedByDisplayName: summary.ProposedByDisplayName, CanDecide: summary.CanDecide, DecisionBlockedReason: summary.DecisionBlockedReason}
		return appendSubjectIdempotency(st, current, "propose_subject_change", caseID, key, requestHash, 201, result, request.CreatedAt)
	})
	return result, replayed, err
}

func (s *Store) ListSubjectChanges(ctx context.Context, user accounts.User, caseID, cursor string, limit int) (SubjectChangeListResult, error) {
	if err := ctx.Err(); err != nil {
		return SubjectChangeListResult{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectChangeListResult{}, err
	}
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectChangeListResult{}, err
	}
	caseItem, err := s.visibleCase(&st, current, caseID)
	if err != nil {
		return SubjectChangeListResult{}, err
	}
	if err := requireCaseAction(caseItem, current, access.Read); err != nil {
		return SubjectChangeListResult{}, err
	}
	items := make([]SubjectChangeSummary, 0)
	for _, request := range st.SubjectChangeRequests {
		if request.TenantID != current.TenantID || request.SourceCaseID != caseID {
			continue
		}
		items = append(items, subjectChangeSummary(st, current, request))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].RequestID < items[j].RequestID })
	return paginateSubjectChangeSummaries(items, cursor, limit)
}

func paginateSubjectChangeSummaries(items []SubjectChangeSummary, cursor string, limit int) (SubjectChangeListResult, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		return SubjectChangeListResult{}, fault.Invalid("每页主体更正最多为100")
	}
	start := 0
	if cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return SubjectChangeListResult{}, fault.Invalid("主体更正游标无效")
		}
		start = sort.Search(len(items), func(i int) bool { return items[i].RequestID > string(decoded) })
	}
	if start >= len(items) {
		return SubjectChangeListResult{Items: []SubjectChangeSummary{}, NextCursor: nil}, nil
	}
	end := min(start+limit, len(items))
	var next *string
	if end < len(items) {
		value := base64.RawURLEncoding.EncodeToString([]byte(items[end-1].RequestID))
		next = &value
	}
	return SubjectChangeListResult{Items: append([]SubjectChangeSummary(nil), items[start:end]...), NextCursor: next}, nil
}

func (s *Store) GetSubjectChange(ctx context.Context, user accounts.User, caseID, requestID string) (SubjectChangeDetailResult, error) {
	if err := ctx.Err(); err != nil {
		return SubjectChangeDetailResult{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectChangeDetailResult{}, err
	}
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectChangeDetailResult{}, err
	}
	caseItem, err := s.visibleCase(&st, current, caseID)
	if err != nil {
		return SubjectChangeDetailResult{}, err
	}
	if err := requireCaseAction(caseItem, current, access.Read); err != nil {
		return SubjectChangeDetailResult{}, err
	}
	for _, request := range st.SubjectChangeRequests {
		if request.TenantID == current.TenantID && request.SourceCaseID == caseID && request.RequestID == requestID {
			impact, err := subjectChangeImpact(st, current, request)
			if err != nil {
				return SubjectChangeDetailResult{}, err
			}
			summary := subjectChangeSummary(st, current, request)
			return SubjectChangeDetailResult{Summary: summary, Before: impact.Before, After: impact.After, BasisNote: request.BasisNote, BasisRefs: append(json.RawMessage(nil), request.BasisRefs...), ProposedByDisplayName: summary.ProposedByDisplayName, CanDecide: summary.CanDecide, DecisionBlockedReason: summary.DecisionBlockedReason}, nil
		}
	}
	return SubjectChangeDetailResult{}, fault.NotFound("没有找到对应记录")
}

func (s *Store) GetSubjectChangeImpact(ctx context.Context, user accounts.User, caseID, requestID string) (SubjectChangeImpactResult, error) {
	if err := ctx.Err(); err != nil {
		return SubjectChangeImpactResult{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectChangeImpactResult{}, err
	}
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectChangeImpactResult{}, err
	}
	request, ok := subjectChangeRequest(st, current.TenantID, caseID, requestID)
	if !ok {
		return SubjectChangeImpactResult{}, fault.NotFound("没有找到对应记录")
	}
	return subjectChangeImpact(st, current, request)
}

func (s *Store) DecideSubjectChange(ctx context.Context, user accounts.User, caseID, requestID string, input SubjectChangeDecisionInput, key string) (SubjectChangeDecisionResult, bool, error) {
	if err := ctx.Err(); err != nil {
		return SubjectChangeDecisionResult{}, false, err
	}
	if s.ReadOnly() {
		return SubjectChangeDecisionResult{}, false, fault.New(503, "read_only", "只读模式下不能决定主体更正")
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectChangeDecisionResult{}, false, err
	}
	if !accounts.CanDecide(current.Role) {
		return SubjectChangeDecisionResult{}, false, fault.Forbidden("当前角色不能批准或拒绝主体更正")
	}
	if input.Decision != "approve" && input.Decision != "reject" || input.ExpectedSubjectVersion < 1 || strings.TrimSpace(input.ExpectedPayloadHash) == "" || strings.TrimSpace(input.ExpectedImpactETag) == "" || strings.TrimSpace(input.Reason) == "" {
		return SubjectChangeDecisionResult{}, false, fault.Invalid("更正决定和预期版本、摘要、影响标识及理由均为必填项")
	}
	if err := validateIdempotencyKey(key); err != nil {
		return SubjectChangeDecisionResult{}, false, err
	}
	seen, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectChangeDecisionResult{}, false, err
	}
	request, ok := subjectChangeRequest(seen, current.TenantID, caseID, requestID)
	if !ok {
		return SubjectChangeDecisionResult{}, false, fault.NotFound("没有找到对应记录")
	}
	caseIDs := affectedSubjectCaseIDs(seen, request)
	subjectIDs := []string{request.TargetSubjectID}
	if request.TargetKind == "case_subject_role" {
		var payload bindingAfterPayload
		if err := json.Unmarshal(request.CanonicalPayload, &payload); err == nil && payload.SubjectID != "" {
			subjectIDs = append(subjectIDs, payload.SubjectID)
		}
	}
	requestHash, err := canonicalSubjectHash(struct {
		CaseID, RequestID string
		Input             SubjectChangeDecisionInput
	}{caseID, requestID, input})
	if err != nil {
		return SubjectChangeDecisionResult{}, false, err
	}
	var result SubjectChangeDecisionResult
	replayed := false
	err = s.updateSubjectCasesAs(ctx, current, subjectIDs, caseIDs, func(st *state) error {
		caseItem, err := s.visibleCase(st, current, caseID)
		if err != nil {
			return err
		}
		if err := requireCaseAction(caseItem, current, access.Decide); err != nil {
			return err
		}
		prior, ok := subjectChangeRequest(*st, current.TenantID, caseID, requestID)
		if !ok {
			return fault.NotFound("没有找到对应记录")
		}
		if priorRecord, found, err := readSubjectIdempotency(*st, current, "decide_subject_change", requestID, key, requestHash); err != nil {
			return err
		} else if found {
			replayed = true
			return json.Unmarshal(priorRecord.Response, &result)
		}
		if prior.ExpectedSubjectVersion != input.ExpectedSubjectVersion || prior.CanonicalPayloadHash != input.ExpectedPayloadHash || !sameOptionalInt(prior.ExpectedBindingVersion, input.ExpectedBindingVersion) {
			return fault.Conflict("主体更正版本或内容已变化，请刷新影响预览")
		}
		for _, decision := range st.SubjectChangeDecisions {
			if decision.RequestID == requestID {
				return fault.Conflict("该更正已由其他决定完成")
			}
		}
		proposer := prior.ProposedBy
		actor := TrustedPrincipalProjection{UserID: current.ID, Issuer: current.Issuer, Subject: current.Subject}
		if subjectPrincipalSame(proposer, actor) {
			return fault.Forbidden("更正提出人不能批准或拒绝同一更正")
		}
		currentAffected := affectedSubjectBindings(*st, current.TenantID, prior.TargetSubjectID, prior.TargetKind, prior.TargetBindingID)
		currentHash, err := canonicalSubjectHash(currentAffected)
		if err != nil {
			return err
		}
		if currentHash != prior.ExpectedBindingsHash {
			return fault.Conflict("主体关联影响范围已变化，请重新查看")
		}
		for _, binding := range currentAffected {
			boundCase, ok := st.caseByID(binding.CaseID)
			if !ok || businessWritable(boundCase) != nil || !authorizeCase(boundCase, current, access.Decide) {
				return fault.Conflict("当前授权范围不足以决定全部受影响案件，请重新查看")
			}
		}
		impact, err := subjectChangeImpact(*st, current, prior)
		if err != nil {
			return err
		}
		if impact.ImpactETag != input.ExpectedImpactETag {
			return fault.Conflict("主体更正影响预览已变化，请刷新后重试")
		}
		decisionID, err := newID("schd_")
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		var appliedVersion *int64
		bindingAppends := 0
		if input.Decision == "approve" {
			appliedVersion, bindingAppends, err = applySubjectChange(st, current, prior, currentAffected, now)
			if err != nil {
				return err
			}
		}
		decision := SubjectChangeDecision{TenantID: current.TenantID, DecisionID: decisionID, RequestID: requestID, Decision: input.Decision, Actor: actor,
			ExpectedSubjectVersion: input.ExpectedSubjectVersion, ExpectedBindingVersion: input.ExpectedBindingVersion, CanonicalPayloadHash: input.ExpectedPayloadHash,
			AffectedBindingsHash: currentHash, Reason: strings.TrimSpace(input.Reason), AppliedSubjectVersion: appliedVersion, BindingVersionsAppended: bindingAppends, CreatedAt: now}
		st.SubjectChangeDecisions = append(st.SubjectChangeDecisions, decision)
		stateName := "rejected"
		if input.Decision == "approve" {
			stateName = "approved"
		}
		result = SubjectChangeDecisionResult{RequestID: requestID, Decision: stateName, AppliedSubjectVersion: appliedVersion, BindingVersionsAppended: bindingAppends}
		return appendSubjectIdempotency(st, current, "decide_subject_change", requestID, key, requestHash, 200, result, now)
	})
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" && pgError.ConstraintName == "subject_synthetic_identifier_unique" {
		return SubjectChangeDecisionResult{}, false, fault.Conflict("该身份标识已由另一主体确认；请在有权范围内显式选择已有主体")
	}
	return result, replayed, err
}

func subjectChangeRequest(st state, tenantID, caseID, requestID string) (SubjectChangeRequest, bool) {
	for _, request := range st.SubjectChangeRequests {
		if request.TenantID == tenantID && request.SourceCaseID == caseID && request.RequestID == requestID {
			return request, true
		}
	}
	return SubjectChangeRequest{}, false
}

func affectedSubjectCaseIDs(st state, request SubjectChangeRequest) []string {
	bindings := affectedSubjectBindings(st, request.TenantID, request.TargetSubjectID, request.TargetKind, request.TargetBindingID)
	ids := []string{request.SourceCaseID}
	for _, binding := range bindings {
		ids = append(ids, binding.CaseID)
	}
	sort.Strings(ids)
	return ids
}

func sameOptionalInt(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func subjectChangeImpact(st state, user accounts.User, request SubjectChangeRequest) (SubjectChangeImpactResult, error) {
	caseItem, ok := st.caseByID(request.SourceCaseID)
	if !ok || caseItem.TenantID != user.TenantID || !caseReadAllowed(caseItem, user) {
		return SubjectChangeImpactResult{}, fault.NotFound("没有找到对应记录")
	}
	full := subjectChangeCanSeeFull(st, user, request)
	before, after, err := subjectChangePayloads(st, request, full)
	if err != nil {
		return SubjectChangeImpactResult{}, err
	}
	bindings := affectedSubjectBindings(st, request.TenantID, request.TargetSubjectID, request.TargetKind, request.TargetBindingID)
	visible := make([]SubjectImpactCaseProjection, 0)
	visibleBindings := make([]CaseSubjectRoleVersion, 0)
	for _, binding := range bindings {
		linked, exists := st.caseByID(binding.CaseID)
		if !exists || !caseReadAllowed(linked, user) {
			continue
		}
		visible = append(visible, SubjectImpactCaseProjection{CaseID: linked.ID, Title: linked.Title, Status: linked.BusinessStatus})
		visibleBindings = append(visibleBindings, binding)
	}
	if !full {
		before = maskSubjectChangeProjection(before)
		after = maskSubjectChangeProjection(after)
	}
	encoded, err := json.Marshal(struct {
		RequestID     string                        `json:"request_id"`
		TargetVersion int64                         `json:"target_version"`
		PayloadHash   string                        `json:"payload_hash"`
		Before        any                           `json:"before"`
		After         any                           `json:"after"`
		Bindings      []CaseSubjectRoleVersion      `json:"bindings"`
		Cases         []SubjectImpactCaseProjection `json:"cases"`
	}{request.RequestID, request.ExpectedSubjectVersion, request.CanonicalPayloadHash, before, after, visibleBindings, visible})
	if err != nil {
		return SubjectChangeImpactResult{}, err
	}
	summary := subjectChangeSummary(st, user, request)
	return SubjectChangeImpactResult{RequestID: request.RequestID, TargetVersion: request.ExpectedSubjectVersion, PayloadHash: request.CanonicalPayloadHash, Before: before, After: after, BasisRefs: append(json.RawMessage(nil), request.BasisRefs...), ImpactETag: hashJSON(encoded), VisibleAffectedCount: len(visibleBindings), AuthorizedImpactCases: visible, ProposedByDisplayName: summary.ProposedByDisplayName, CanDecide: summary.CanDecide, DecisionBlockedReason: summary.DecisionBlockedReason}, nil
}

func maskSubjectChangeProjection(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var result any
	if json.Unmarshal(encoded, &result) != nil {
		return value
	}
	var mask func(any)
	mask = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if rawClaims, ok := v["identity_claims"].([]any); ok {
				for _, rawClaim := range rawClaims {
					claim, ok := rawClaim.(map[string]any)
					if !ok || claim["namespace"] != "cn.gov.samr.uscc" || claim["identifier_type"] != "unified_social_credit_code" {
						continue
					}
					if raw, ok := claim["value"].(string); ok {
						claim["masked_value"] = maskUSCC(raw)
						delete(claim, "value")
					}
				}
			}
			for _, child := range v {
				mask(child)
			}
		case []any:
			for _, child := range v {
				mask(child)
			}
		}
	}
	mask(result)
	return result
}

func applySubjectChange(st *state, user accounts.User, request SubjectChangeRequest, bindings []CaseSubjectRoleVersion, now time.Time) (*int64, int, error) {
	if request.TargetKind == "subject" {
		subjectIndex := -1
		for i := range st.Subjects {
			if st.Subjects[i].TenantID == user.TenantID && st.Subjects[i].ID == request.TargetSubjectID {
				subjectIndex = i
				break
			}
		}
		if subjectIndex < 0 || st.Subjects[subjectIndex].CurrentVersion != request.ExpectedSubjectVersion {
			return nil, 0, fault.Conflict("主体版本已变化")
		}
		var after subjectAfterPayload
		if err := json.Unmarshal(request.CanonicalPayload, &after); err != nil {
			return nil, 0, err
		}
		version := after.SubjectVersion
		if version.Version != request.ExpectedSubjectVersion+1 || version.SubjectID != request.TargetSubjectID || version.PayloadHash != hashSubjectVersion(version) {
			return nil, 0, errors.New("更正主体版本摘要无效")
		}
		st.SubjectVersions = append(st.SubjectVersions, version)
		st.Subjects[subjectIndex].CurrentVersion = version.Version
		st.Subjects[subjectIndex].Status = version.VerificationState
		applied := version.Version
		for _, current := range bindings {
			for i := range st.CaseSubjectRoles {
				head := &st.CaseSubjectRoles[i]
				if head.BindingID != current.BindingID || head.CaseID != current.CaseID || head.CurrentVersion != current.Version {
					continue
				}
				head.CurrentVersion++
				next := current
				next.Version = head.CurrentVersion
				next.SubjectVersion = version.Version
				next.CreatedByUser = user.ID
				next.CreatedByIssuer = user.Issuer
				next.CreatedBySubject = user.Subject
				next.CreatedAt = now
				st.CaseSubjectRoleVersions = append(st.CaseSubjectRoleVersions, next)
			}
		}
		return &applied, len(bindings), nil
	}
	if len(bindings) != 1 {
		return nil, 0, errors.New("binding correction target cardinality changed")
	}
	current := bindings[0]
	var after bindingAfterPayload
	if err := json.Unmarshal(request.CanonicalPayload, &after); err != nil {
		return nil, 0, err
	}
	if after.SubjectID != current.SubjectID {
		var target *Subject
		for i := range st.Subjects {
			if st.Subjects[i].TenantID == user.TenantID && st.Subjects[i].ID == after.SubjectID {
				target = &st.Subjects[i]
				break
			}
		}
		if target == nil || target.CurrentVersion != after.SubjectVersion {
			return nil, 0, fault.Conflict("目标主体版本已变化")
		}
	}
	for i := range st.CaseSubjectRoles {
		head := &st.CaseSubjectRoles[i]
		if head.BindingID != current.BindingID || head.CaseID != current.CaseID || head.CurrentVersion != current.Version {
			continue
		}
		head.CurrentVersion++
		next := current
		next.Version = head.CurrentVersion
		next.SubjectID = after.SubjectID
		next.SubjectVersion = after.SubjectVersion
		next.Role = after.Role
		next.BindingState = after.BindingState
		next.SourceKind = after.SourceKind
		next.LegacyLabel = after.LegacyLabel
		next.CreatedByUser = user.ID
		next.CreatedByIssuer = user.Issuer
		next.CreatedBySubject = user.Subject
		next.CreatedAt = now
		st.CaseSubjectRoleVersions = append(st.CaseSubjectRoleVersions, next)
		return nil, 1, nil
	}
	return nil, 0, fault.Conflict("主体关联版本已变化")
}

func subjectChangeSummary(st state, user accounts.User, request SubjectChangeRequest) SubjectChangeSummary {
	stateName := "pending_decision"
	for _, decision := range st.SubjectChangeDecisions {
		if decision.RequestID == request.RequestID {
			stateName = "rejected"
			if decision.Decision == "approve" {
				stateName = "approved"
			}
		}
	}
	var bindingID *string
	if request.TargetBindingID != "" {
		value := request.TargetBindingID
		bindingID = &value
	}
	canDecide, blockedReason := subjectDecisionability(st, user, request, stateName)
	displayName := request.ProposedBy.UserID
	if proposer, ok := accounts.ByID(request.ProposedBy.UserID); ok && proposer.DisplayName != "" {
		displayName = proposer.DisplayName
	}
	return SubjectChangeSummary{RequestID: request.RequestID, TargetKind: request.TargetKind, TargetSubjectID: request.TargetSubjectID, TargetBindingID: bindingID, ExpectedSubjectVersion: request.ExpectedSubjectVersion, ExpectedBindingVersion: request.ExpectedBindingVersion, PayloadHash: request.CanonicalPayloadHash, State: stateName, ProposedBy: request.ProposedBy, ProposedByDisplayName: displayName, CanDecide: canDecide, DecisionBlockedReason: blockedReason, CreatedAt: request.CreatedAt}
}

func subjectDecisionability(st state, user accounts.User, request SubjectChangeRequest, stateName string) (bool, *string) {
	blocked := func(reason string) (bool, *string) { return false, &reason }
	if stateName != "pending_decision" {
		return blocked("该更正已完成决定")
	}
	if !accounts.CanDecide(user.Role) {
		return blocked("当前角色没有更正决定权限")
	}
	if subjectPrincipalSame(request.ProposedBy, TrustedPrincipalProjection{UserID: user.ID, Issuer: user.Issuer, Subject: user.Subject}) {
		return blocked("更正提出人不能批准或拒绝同一更正")
	}
	if !hasSubjectChangeDecisionScope(st, user, request) {
		return blocked("当前授权不足以决定全部受影响案件")
	}
	return true, nil
}

func hasSubjectChangeDecisionScope(st state, user accounts.User, request SubjectChangeRequest) bool {
	if !accounts.CanDecide(user.Role) {
		return false
	}
	for _, caseID := range affectedSubjectCaseIDs(st, request) {
		item, ok := st.caseByID(caseID)
		if !ok || businessWritable(item) != nil || !authorizeCase(item, user, access.Decide) {
			return false
		}
	}
	return true
}

func subjectChangeCanSeeFull(st state, user accounts.User, request SubjectChangeRequest) bool {
	if source, ok := st.caseByID(request.SourceCaseID); ok && authorizeCase(source, user, access.Manage) {
		return true
	}
	return !subjectPrincipalSame(request.ProposedBy, TrustedPrincipalProjection{UserID: user.ID, Issuer: user.Issuer, Subject: user.Subject}) && hasSubjectChangeDecisionScope(st, user, request)
}

func currentSubjectBinding(st state, bindingID string) (CaseSubjectRole, CaseSubjectRoleVersion, bool) {
	for _, head := range st.CaseSubjectRoles {
		if head.BindingID == bindingID {
			current := caseRoleCurrent(st, head)
			return head, current, current.BindingID != ""
		}
	}
	return CaseSubjectRole{}, CaseSubjectRoleVersion{}, false
}

func caseHasSubject(st *state, caseID, subjectID string) bool {
	for _, head := range st.CaseSubjectRoles {
		if head.CaseID != caseID {
			continue
		}
		version := caseRoleCurrent(*st, head)
		if version.SubjectID == subjectID && version.BindingState != "inactive" {
			return true
		}
	}
	return false
}

func affectedSubjectBindings(st state, tenantID, subjectID, kind, bindingID string) []CaseSubjectRoleVersion {
	items := make([]CaseSubjectRoleVersion, 0)
	for _, head := range st.CaseSubjectRoles {
		if head.TenantID != tenantID {
			continue
		}
		if kind == "case_subject_role" && head.BindingID != bindingID {
			continue
		}
		if kind == "subject" && head.TenantID == "" {
			continue
		}
		version := caseRoleCurrent(st, head)
		if (kind == "subject" && (version.BindingState == "inactive" || version.SubjectID != subjectID)) ||
			(kind == "case_subject_role" && version.BindingID != bindingID) {
			continue
		}
		items = append(items, version)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CaseID != items[j].CaseID {
			return items[i].CaseID < items[j].CaseID
		}
		return items[i].BindingID < items[j].BindingID
	})
	return items
}

func hashJSON(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func canonicalRawJSON(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	encoded, err := json.Marshal(value)
	return encoded, err
}

func subjectChangePayloads(st state, request SubjectChangeRequest, full bool) (any, any, error) {
	if request.TargetKind == "subject" {
		before, ok := subjectVersionAt(st.SubjectVersions, request.TargetSubjectID, request.ExpectedSubjectVersion)
		if !ok {
			return nil, nil, errors.New("proposal before subject version is missing")
		}
		var payload subjectAfterPayload
		if err := json.Unmarshal(request.CanonicalPayload, &payload); err != nil {
			return nil, nil, err
		}
		return projectSubjectVersion(before, full), projectSubjectVersion(payload.SubjectVersion, full), nil
	}
	head, currentBinding, ok := currentSubjectBinding(st, request.TargetBindingID)
	if !ok {
		return nil, nil, errors.New("proposal binding is missing")
	}
	if request.ExpectedBindingVersion == nil {
		return nil, nil, errors.New("proposal expected binding version is missing")
	}
	binding, ok := subjectBindingVersionAt(st, head, *request.ExpectedBindingVersion)
	if !ok {
		return nil, nil, errors.New("proposal before binding version is missing")
	}
	var payload bindingAfterPayload
	if err := json.Unmarshal(request.CanonicalPayload, &payload); err != nil {
		return nil, nil, err
	}
	before := projectSubjectBinding(st, head, binding, full)
	afterVersion, ok := subjectVersionAt(st.SubjectVersions, payload.SubjectID, payload.SubjectVersion)
	if !ok {
		return nil, nil, errors.New("proposal target subject version is missing")
	}
	_ = currentBinding
	var label *string
	if payload.LegacyLabel != "" {
		value := payload.LegacyLabel
		label = &value
	}
	after := CaseSubjectBindingProjection{BindingID: head.BindingID, BindingVersion: binding.Version + 1, CaseID: head.CaseID, Role: payload.Role,
		BindingState: payload.BindingState, SourceKind: payload.SourceKind, LegacyLabel: label, Subject: projectSubjectVersion(afterVersion, full),
		CreatedBy: request.ProposedBy, CreatedAt: time.Time{}}
	return before, after, nil
}

func subjectBindingVersionAt(st state, head CaseSubjectRole, version int64) (CaseSubjectRoleVersion, bool) {
	for _, item := range st.CaseSubjectRoleVersions {
		if item.TenantID == head.TenantID && item.OrgID == head.OrgID && item.CaseID == head.CaseID && item.BindingID == head.BindingID && item.Version == version {
			return item, true
		}
	}
	return CaseSubjectRoleVersion{}, false
}

func projectSubjectBinding(st state, head CaseSubjectRole, binding CaseSubjectRoleVersion, full bool) CaseSubjectBindingProjection {
	version, _ := subjectVersionAt(st.SubjectVersions, binding.SubjectID, binding.SubjectVersion)
	var label *string
	if binding.LegacyLabel != "" {
		value := binding.LegacyLabel
		label = &value
	}
	return CaseSubjectBindingProjection{BindingID: head.BindingID, BindingVersion: binding.Version, CaseID: head.CaseID, Role: binding.Role,
		BindingState: binding.BindingState, SourceKind: binding.SourceKind, LegacyLabel: label, Subject: projectSubjectVersion(version, full),
		CreatedBy: TrustedPrincipalProjection{UserID: binding.CreatedByUser, Issuer: binding.CreatedByIssuer, Subject: binding.CreatedBySubject}, CreatedAt: binding.CreatedAt}
}

func validateSubjectBasisRefs(st state, caseItem *Case, raw json.RawMessage) (json.RawMessage, error) {
	var inputs []materialAnchorInput
	if err := decodeStrictJSON(raw, &inputs); err != nil || len(inputs) == 0 || len(inputs) > 8 {
		return nil, errors.New("basis_refs must contain 1 to 8 validated material anchors")
	}
	outputs := make([]materialAnchorProjection, 0, len(inputs))
	for _, input := range inputs {
		if input.Kind != "material_anchor" || input.MaterialVersion < 1 || input.Start < 0 || input.End <= input.Start || input.QuoteDigest == "" {
			return nil, errors.New("material anchor identity or span is invalid")
		}
		var version *MaterialVersion
		for i := range st.MaterialVersions {
			item := &st.MaterialVersions[i]
			if item.TenantID == caseItem.TenantID && item.OrgID == caseItem.OwnerOrgID && item.CaseID == caseItem.ID && item.MaterialID == input.MaterialID && item.Version == input.MaterialVersion && item.DocumentID == input.DocumentID {
				version = item
				break
			}
		}
		if version == nil {
			return nil, errors.New("material anchor does not identify a pinned material version in this case")
		}
		var document *Document
		for i := range st.Documents {
			item := &st.Documents[i]
			if item.ID == input.DocumentID && item.CaseID == caseItem.ID && item.TenantID == caseItem.TenantID && item.OrgID == caseItem.OwnerOrgID {
				document = item
				break
			}
		}
		if document == nil {
			return nil, errors.New("material anchor document is not authorized in this case")
		}
		parsed, ok := st.parsedByID(input.ParsedDocumentID)
		if !ok || parsed.DocumentID != document.ID || parsed.Status != "ready" || verifyParsed(parsed, *document) != nil {
			return nil, errors.New("material anchor parse identity is unavailable or invalid")
		}
		runes := []rune(parsed.Text)
		if input.End > len(runes) {
			return nil, errors.New("material anchor span exceeds the pinned parse")
		}
		quote := string(runes[input.Start:input.End])
		digest := sha256.Sum256([]byte(quote))
		if hex.EncodeToString(digest[:]) != strings.ToLower(input.QuoteDigest) {
			return nil, errors.New("material anchor quote digest does not match the pinned text")
		}
		outputs = append(outputs, materialAnchorProjection{Kind: input.Kind, MaterialID: input.MaterialID, MaterialVersion: input.MaterialVersion, DocumentID: input.DocumentID, ParsedDocumentID: input.ParsedDocumentID, Filename: document.Filename, Start: input.Start, End: input.End, Quote: quote, QuoteDigest: hex.EncodeToString(digest[:])})
	}
	encoded, err := json.Marshal(outputs)
	return encoded, err
}

func subjectPrincipalSame(a, b TrustedPrincipalProjection) bool {
	return a.UserID == b.UserID || (a.Issuer != "" && a.Issuer == b.Issuer && a.Subject == b.Subject)
}
