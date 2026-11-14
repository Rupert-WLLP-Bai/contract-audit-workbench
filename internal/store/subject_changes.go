// File metadata (manifest mirror):
// Purpose: Preserves Subject correction command authorization and lock order while delegating proposal, decision and impact rules to the Subject application.
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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	subjectApp "contractaudit/internal/subject/app"
	subjectModel "contractaudit/internal/subject/model"
	"github.com/jackc/pgx/v5/pgconn"
)

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

func (s *Store) ProposeSubjectChange(ctx context.Context, user accounts.User, caseID string, input SubjectChangeCreateInput, key string) (SubjectChangeCreateResult, bool, error) {
	ctx = commandContext(ctx, user)
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
	input, err = subjectApp.NormalizeProposal(input)
	if err != nil {
		return SubjectChangeCreateResult{}, false, err
	}
	requestHash, err := subjectApp.RequestHash(input)
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
		snapshot, actor := subjectCommandSnapshot(st), subjectCommandActor(current)
		result, replayed, err = subjectApp.ReplayProposal(snapshot, actor, caseID, key, requestHash)
		if err != nil || replayed {
			return err
		}
		facts := subjectProposalFacts{st: st, caseItem: *caseItem, actor: current}
		delta, created, err := subjectApp.Propose(ctx, snapshot, actor, subjectApp.CaseIdentity{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID}, input, subjectReadableBindingCases(st, current), key, requestHash, facts, subjectCommandClock{}, facts)
		result = created
		if err != nil {
			return err
		}
		applySubjectCommandDelta(st, delta)
		return nil
	})
	return result, replayed, err
}

func (s *Store) ListSubjectChanges(ctx context.Context, user accounts.User, caseID, cursor string, limit int) (SubjectChangeListResult, error) {
	ctx = commandContext(ctx, user)
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
	return subjectApp.ListSubjectChanges(subjectCommandSnapshot(&st), subjectCommandActor(current), caseID,
		subjectReadFacts(&st, current, s.ReadOnly()), cursor, limit)
}

func paginateSubjectChangeSummaries(items []SubjectChangeSummary, cursor string, limit int) (SubjectChangeListResult, error) {
	return subjectApp.PaginateSubjectChanges(items, cursor, limit)
}

func (s *Store) GetSubjectChange(ctx context.Context, user accounts.User, caseID, requestID string) (SubjectChangeDetailResult, error) {
	ctx = commandContext(ctx, user)
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
	return subjectApp.GetSubjectChange(subjectCommandSnapshot(&st), subjectCommandActor(current), caseID, requestID, subjectReadFacts(&st, current, s.ReadOnly()))
}

func (s *Store) GetSubjectChangeImpact(ctx context.Context, user accounts.User, caseID, requestID string) (SubjectChangeImpactResult, error) {
	ctx = commandContext(ctx, user)
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
	ctx = commandContext(ctx, user)
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
	if err := subjectApp.ValidateDecisionInput(input); err != nil {
		return SubjectChangeDecisionResult{}, false, err
	}
	if err := validateIdempotencyKey(key); err != nil {
		return SubjectChangeDecisionResult{}, false, err
	}
	seen, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectChangeDecisionResult{}, false, err
	}
	request, ok := subjectModel.FindSubjectChangeRequest(seen.SubjectChangeRequests, current.TenantID, caseID, requestID)
	if !ok {
		return SubjectChangeDecisionResult{}, false, fault.NotFound("没有找到对应记录")
	}
	subjectIDs, caseIDs := subjectApp.DecisionLockTargets(subjectCommandSnapshot(&seen), request)
	requestHash, err := subjectApp.DecisionRequestHash(caseID, requestID, input)
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
		snapshot, actor := subjectCommandSnapshot(st), subjectCommandActor(current)
		prior, old, found, err := subjectApp.ReplayDecision(snapshot, actor, caseID, requestID, key, requestHash)
		result, replayed = old, found
		if err != nil || found {
			return err
		}
		facts := subjectDecisionFacts(st, current, prior)
		delta, decided, err := subjectApp.Decide(snapshot, actor, prior, input, key, requestHash, facts, subjectCommandClock{})
		result = decided
		if err != nil {
			return err
		}
		applySubjectCommandDelta(st, delta)
		return nil
	})
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" && pgError.ConstraintName == "subject_synthetic_identifier_unique" {
		return SubjectChangeDecisionResult{}, false, fault.Conflict("该身份标识已由另一主体确认；请在有权范围内显式选择已有主体")
	}
	return result, replayed, err
}

func subjectChangeRequest(st state, tenantID, caseID, requestID string) (SubjectChangeRequest, bool) {
	return subjectModel.FindSubjectChangeRequest(st.SubjectChangeRequests, tenantID, caseID, requestID)
}

func affectedSubjectCaseIDs(st state, request SubjectChangeRequest) []string {
	return subjectModel.AffectedSubjectCaseIDs(subjectModel.SubjectCollections{
		Subjects: st.Subjects, SubjectVersions: st.SubjectVersions,
		CaseSubjectRoles: st.CaseSubjectRoles, CaseSubjectRoleVersions: st.CaseSubjectRoleVersions,
	}, request)
}

func subjectChangeImpact(st state, user accounts.User, request SubjectChangeRequest) (SubjectChangeImpactResult, error) {
	return subjectApp.DecisionImpact(subjectCommandSnapshot(&st), subjectCommandActor(user), request, subjectDecisionFacts(&st, user, request))
}

func subjectChangeSummary(st state, user accounts.User, request SubjectChangeRequest) SubjectChangeSummary {
	return subjectApp.DecisionSummary(subjectCommandSnapshot(&st), subjectCommandActor(user), request, subjectDecisionFacts(&st, user, request))
}

func currentSubjectBinding(st state, bindingID string) (CaseSubjectRole, CaseSubjectRoleVersion, bool) {
	return subjectModel.CurrentSubjectBinding(st.CaseSubjectRoles, st.CaseSubjectRoleVersions, bindingID)
}

func caseHasSubject(st *state, caseID, subjectID string) bool {
	return subjectModel.CaseHasSubject(st.CaseSubjectRoles, st.CaseSubjectRoleVersions, caseID, subjectID)
}

func affectedSubjectBindings(st state, tenantID, subjectID, kind, bindingID string) []CaseSubjectRoleVersion {
	return subjectModel.AffectedSubjectBindings(st.CaseSubjectRoles, st.CaseSubjectRoleVersions, tenantID, subjectID, kind, bindingID)
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

func subjectBindingVersionAt(st state, head CaseSubjectRole, version int64) (CaseSubjectRoleVersion, bool) {
	return subjectModel.SubjectBindingVersionAt(st.CaseSubjectRoleVersions, head, version)
}

func projectSubjectBinding(st state, head CaseSubjectRole, binding CaseSubjectRoleVersion, full bool) CaseSubjectBindingProjection {
	return subjectModel.ProjectSubjectBinding(subjectModel.SubjectCollections{
		Subjects: st.Subjects, SubjectVersions: st.SubjectVersions,
		CaseSubjectRoles: st.CaseSubjectRoles, CaseSubjectRoleVersions: st.CaseSubjectRoleVersions,
	}, head, binding, full)
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
	return subjectModel.SamePrincipal(a, b)
}
