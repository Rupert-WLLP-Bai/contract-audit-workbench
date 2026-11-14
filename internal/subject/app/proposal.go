package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/subject/model"
)

type ProposalBasis interface {
	VerifyProposalBasis(context.Context, json.RawMessage) (json.RawMessage, error)
}
type ProposalClock interface {
	Now() time.Time
	NewRequestID() (string, error)
}
type ProposalPresentation interface{ ProposalActorPresentation() (string, bool) }

func canonicalProposalJSON(raw json.RawMessage) (json.RawMessage, error) {
	var v any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&v); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return json.Marshal(v)
}
func NormalizeProposal(input model.SubjectChangeCreateInput) (model.SubjectChangeCreateInput, error) {
	input.Target.Kind = strings.TrimSpace(input.Target.Kind)
	input.Target.SubjectID = strings.TrimSpace(input.Target.SubjectID)
	input.Target.BindingID = strings.TrimSpace(input.Target.BindingID)
	input.BasisNote = strings.TrimSpace(input.BasisNote)
	if input.Target.SubjectID == "" || input.ExpectedSubjectVersion < 1 || input.BasisNote == "" || len(input.Patch) == 0 || len(input.BasisRefs) == 0 {
		return model.SubjectChangeCreateInput{}, fault.Invalid("更正目标、版本、理由、修改内容和依据均为必填项")
	}
	var err error
	input.Patch, err = canonicalProposalJSON(input.Patch)
	if err != nil {
		return model.SubjectChangeCreateInput{}, fault.Invalid("patch必须是合法JSON值")
	}
	input.BasisRefs, err = canonicalProposalJSON(input.BasisRefs)
	if err != nil {
		return model.SubjectChangeCreateInput{}, fault.Invalid("basis_refs必须是合法JSON值")
	}
	return input, nil
}
func ReplayProposal(snapshot Snapshot, actor Actor, caseID, key, hash string) (model.SubjectChangeCreateResult, bool, error) {
	var result model.SubjectChangeCreateResult
	found, err := replay(snapshot, actor, "propose_subject_change", caseID, key, hash, &result)
	return result, found, err
}
func Propose(ctx context.Context, snapshot Snapshot, actor Actor, caseID CaseIdentity, input model.SubjectChangeCreateInput, readable map[string]bool, key, hash string, basis ProposalBasis, clock ProposalClock, presentation ProposalPresentation) (Delta, model.SubjectChangeCreateResult, error) {
	c := snapshot.Collections
	subject, version, visible := VisibleSubject(snapshot, actor, input.Target.SubjectID, readable)
	if !visible && input.Target.Kind == "case_subject_role" && input.Target.BindingID != "" {
		head, binding, exists := model.CurrentSubjectBinding(c.CaseSubjectRoles, c.CaseSubjectRoleVersions, input.Target.BindingID)
		if exists && head.TenantID == caseID.TenantID && head.OrgID == caseID.OrgID && head.CaseID == caseID.CaseID && binding.SubjectID == input.Target.SubjectID {
			for _, candidate := range c.Subjects {
				if candidate.TenantID == caseID.TenantID && candidate.ID == input.Target.SubjectID {
					if v, ok := model.SubjectVersionAt(c.SubjectVersions, candidate.ID, candidate.CurrentVersion); ok {
						subject, version, visible = candidate, v, true
					}
					break
				}
			}
		}
	}
	if !visible || subject.TenantID != caseID.TenantID || subject.CurrentVersion != input.ExpectedSubjectVersion {
		if !visible {
			return Delta{}, model.SubjectChangeCreateResult{}, fault.NotFound("没有找到对应记录")
		}
		return Delta{}, model.SubjectChangeCreateResult{}, fault.Conflict("主体版本已变化，请刷新后重试")
	}
	var after any
	switch input.Target.Kind {
	case "subject":
		if input.Target.BindingID != "" || input.ExpectedBindingVersion != nil || !model.CaseHasSubject(c.CaseSubjectRoles, c.CaseSubjectRoleVersions, caseID.CaseID, subject.ID) {
			return Delta{}, model.SubjectChangeCreateResult{}, fault.NotFound("没有找到对应记录")
		}
		changed, err := model.ApplySubjectPatch(version, input.Patch)
		if err != nil {
			return Delta{}, model.SubjectChangeCreateResult{}, fault.Invalid(err.Error())
		}
		version = changed
		version.Version = subject.CurrentVersion + 1
		version.SourceKind = "authorized_correction"
		version.BasisSummary = input.BasisNote
		version.CreatedByUser, version.CreatedByIssuer, version.CreatedBySubject = actor.Principal.UserID, actor.Principal.Issuer, actor.Principal.Subject
		version.CreatedAt = clock.Now().UTC()
		version.PayloadHash = model.HashSubjectVersion(version)
		after = model.SubjectAfterPayload{SubjectVersion: version}
	case "case_subject_role":
		if input.Target.BindingID == "" || input.ExpectedBindingVersion == nil || *input.ExpectedBindingVersion < 1 {
			return Delta{}, model.SubjectChangeCreateResult{}, fault.Invalid("绑定更正必须指定绑定 ID 和当前绑定版本")
		}
		head, binding, ok := model.CurrentSubjectBinding(c.CaseSubjectRoles, c.CaseSubjectRoleVersions, input.Target.BindingID)
		if !ok || head.TenantID != caseID.TenantID || head.OrgID != caseID.OrgID || head.CaseID != caseID.CaseID || binding.SubjectID != subject.ID {
			return Delta{}, model.SubjectChangeCreateResult{}, fault.NotFound("没有找到对应记录")
		}
		if binding.Version != *input.ExpectedBindingVersion {
			return Delta{}, model.SubjectChangeCreateResult{}, fault.Conflict("主体关联版本已变化，请刷新后重试")
		}
		patch, err := model.ParseCaseSubjectRolePatch(input.Patch, binding.SubjectID)
		if err != nil {
			return Delta{}, model.SubjectChangeCreateResult{}, fault.Invalid(err.Error())
		}
		next := model.BindingAfterPayload{SubjectID: binding.SubjectID, SubjectVersion: binding.SubjectVersion, Role: binding.Role, BindingState: binding.BindingState, SourceKind: "authorized_correction", LegacyLabel: binding.LegacyLabel}
		if binding.BindingState == "inactive" {
			next.SubjectVersion = version.Version
		}
		if patch.SubjectID != nil {
			target, targetVersion, visible := VisibleSubject(snapshot, actor, *patch.SubjectID, readable)
			if !visible || target.CurrentVersion != *patch.ExpectedNewSubjectVersion {
				return Delta{}, model.SubjectChangeCreateResult{}, fault.NotFound("没有找到对应记录")
			}
			next.SubjectID, next.SubjectVersion = target.ID, targetVersion.Version
		}
		if patch.Role != nil {
			next.Role = *patch.Role
		}
		if patch.BindingState != nil {
			next.BindingState = *patch.BindingState
		}
		after = next
	default:
		return Delta{}, model.SubjectChangeCreateResult{}, fault.Invalid("target.kind无效")
	}
	refs, err := basis.VerifyProposalBasis(ctx, input.BasisRefs)
	if err != nil {
		return Delta{}, model.SubjectChangeCreateResult{}, fault.Invalid(err.Error())
	}
	canonical, err := json.Marshal(after)
	if err != nil {
		return Delta{}, model.SubjectChangeCreateResult{}, err
	}
	digest := sha256.Sum256(canonical)
	payloadHash := hex.EncodeToString(digest[:])
	bindings := model.AffectedSubjectBindings(c.CaseSubjectRoles, c.CaseSubjectRoleVersions, caseID.TenantID, subject.ID, input.Target.Kind, input.Target.BindingID)
	bindingsHash, err := model.CanonicalSubjectHash(bindings)
	if err != nil {
		return Delta{}, model.SubjectChangeCreateResult{}, err
	}
	requestID, err := clock.NewRequestID()
	if err != nil {
		return Delta{}, model.SubjectChangeCreateResult{}, err
	}
	request := model.SubjectChangeRequest{TenantID: caseID.TenantID, OrgID: caseID.OrgID, RequestID: requestID, SourceCaseID: caseID.CaseID, TargetKind: input.Target.Kind, TargetSubjectID: subject.ID, TargetBindingID: input.Target.BindingID, ExpectedSubjectVersion: subject.CurrentVersion, ExpectedBindingVersion: input.ExpectedBindingVersion, CanonicalPayload: canonical, CanonicalPayloadHash: payloadHash, BasisNote: input.BasisNote, BasisRefs: refs, ExpectedBindingsHash: bindingsHash, ProposedBy: actor.Principal, CreatedAt: clock.Now().UTC()}
	display, canDecideRole := presentation.ProposalActorPresentation()
	summary := DecisionSummary(snapshot, actor, request, DecisionFacts{CanDecideRole: canDecideRole, ProposerDisplayName: display})
	result := model.SubjectChangeCreateResult{RequestID: requestID, TargetKind: request.TargetKind, ExpectedSubjectVersion: request.ExpectedSubjectVersion, ExpectedBindingVersion: request.ExpectedBindingVersion, PayloadHash: payloadHash, State: summary.State, ProposedByDisplayName: summary.ProposedByDisplayName, CanDecide: summary.CanDecide, DecisionBlockedReason: summary.DecisionBlockedReason}
	saved, err := receipt(actor, "propose_subject_change", caseID.CaseID, key, hash, result, request.CreatedAt)
	if err != nil {
		return Delta{}, result, err
	}
	return Delta{Requests: []model.SubjectChangeRequest{request}, Receipts: []model.SubjectIdempotencyRecord{saved}}, result, nil
}
