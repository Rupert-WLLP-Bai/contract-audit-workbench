// File metadata (manifest mirror):
// Purpose: Coordinates Legal fact replay and new version recording with exact basis, Subject pin rules, clock, head locking, and version CAS.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"encoding/json"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/legal/model"
)

type RecordIdentity struct{ TenantID, OrgID, CaseID, ActorID, Issuer, Subject string }
type RecordRepository interface {
	FindVersionByActorKey(context.Context, RecordIdentity, string, string) (int64, bool, error)
	ReadVersion(context.Context, RecordIdentity, string, int64) (model.LegalApplicabilityFactVersion, error)
	LockHead(context.Context, RecordIdentity, string) (string, int64, error)
	InsertHead(context.Context, RecordIdentity, string, string, time.Time) error
	AppendVersion(context.Context, model.LegalApplicabilityFactVersion) error
	AdvanceHead(context.Context, RecordIdentity, string, int64, int64) (bool, error)
}
type RecordBasis interface {
	VerifyBasis(context.Context, json.RawMessage) (json.RawMessage, *time.Time, error)
}
type RecordPartyBinding struct {
	Found                                    bool
	HeadVersion, SubjectVersion, SubjectHead int64
	SubjectID, Role, State                   string
}
type RecordPartyFacts interface {
	LoadRecordBinding(context.Context, model.LegalApplicabilityPartyPin) (RecordPartyBinding, error)
	CountRecordRole(context.Context, string) (int, error)
}
type RecordClockIDs interface {
	Now() time.Time
	NewFactID() (string, error)
}

type RecordVersionReader interface {
	ReadVersion(context.Context, RecordIdentity, string, int64) (model.LegalApplicabilityFactVersion, error)
}

func ReadRecordVersion(ctx context.Context, repo RecordVersionReader, id RecordIdentity, dimension string, version int64) (model.LegalApplicabilityFactVersion, error) {
	fact, err := repo.ReadVersion(ctx, id, dimension, version)
	if err != nil {
		return model.LegalApplicabilityFactVersion{}, err
	}
	canonical, err := model.VerifyApplicabilityBasisManifest(fact.BasisRefs, fact.BasisSHA256)
	if err != nil {
		return model.LegalApplicabilityFactVersion{}, fault.Conflict("法律适用事实材料依据摘要不匹配")
	}
	fact.BasisRefs = canonical
	return fact, nil
}
func FindRecordReplay(ctx context.Context, repo RecordRepository, id RecordIdentity, input model.LegalApplicabilityFactInput) (model.LegalApplicabilityFactVersion, string, bool, error) {
	digest, err := model.LegalApplicabilityRequestDigest(id.TenantID, id.OrgID, id.CaseID, id.ActorID, input.IdempotencyKey, input)
	if err != nil {
		return model.LegalApplicabilityFactVersion{}, "", false, err
	}
	version, found, err := repo.FindVersionByActorKey(ctx, id, input.Dimension, input.IdempotencyKey)
	if err != nil || !found {
		return model.LegalApplicabilityFactVersion{}, digest, false, err
	}
	fact, err := ReadRecordVersion(ctx, repo, id, input.Dimension, version)
	if err != nil {
		return model.LegalApplicabilityFactVersion{}, "", false, err
	}
	if fact.RequestDigest != digest {
		return model.LegalApplicabilityFactVersion{}, "", false, fault.Conflict("同一幂等键不能用于不同法律适用事实")
	}
	return fact, digest, true, nil
}
func RecordNewFact(ctx context.Context, repo RecordRepository, basis RecordBasis, party RecordPartyFacts, clock RecordClockIDs, id RecordIdentity, input model.LegalApplicabilityFactInput, digest string) (model.LegalApplicabilityFactVersion, error) {
	refs, obtained, err := basis.VerifyBasis(ctx, input.BasisRefs)
	if err != nil {
		return model.LegalApplicabilityFactVersion{}, err
	}
	now := clock.Now().UTC().Truncate(time.Microsecond)
	fact, err := model.NormalizeLegalApplicabilityInput(input, now, refs, obtained)
	if err != nil {
		return model.LegalApplicabilityFactVersion{}, fault.Invalid(err.Error())
	}
	if fact.Party.BindingID != "" {
		pin := fact.Party
		binding, err := party.LoadRecordBinding(ctx, pin)
		if err != nil {
			return model.LegalApplicabilityFactVersion{}, err
		}
		if !binding.Found || binding.HeadVersion != pin.BindingVersion || binding.SubjectID != pin.SubjectID || binding.SubjectVersion != pin.SubjectVersion || binding.SubjectHead != pin.SubjectVersion || binding.State != "active" {
			return model.LegalApplicabilityFactVersion{}, fault.Conflict("买卖方主体关联已变化，请重新确认事实")
		}
		if !model.PartyRoleMatches(pin.Side, binding.Role) {
			return model.LegalApplicabilityFactVersion{}, fault.Invalid("事实主体角色与当前案件关联不一致")
		}
		count, err := party.CountRecordRole(ctx, binding.Role)
		if err != nil {
			return model.LegalApplicabilityFactVersion{}, err
		}
		if model.PartyScopeAmbiguous(pin, count) {
			return model.LegalApplicabilityFactVersion{}, fault.Invalid("案件存在多个同角色主体，必须说明本事实的主体范围")
		}
	}
	factID, current, err := repo.LockHead(ctx, id, input.Dimension)
	if err != nil {
		return model.LegalApplicabilityFactVersion{}, err
	}
	if current != input.ExpectedVersion {
		return model.LegalApplicabilityFactVersion{}, fault.Conflict("法律适用事实已变化，请刷新后重试")
	}
	if factID == "" {
		factID, err = clock.NewFactID()
		if err != nil {
			return model.LegalApplicabilityFactVersion{}, err
		}
		if err = repo.InsertHead(ctx, id, input.Dimension, factID, now); err != nil {
			return model.LegalApplicabilityFactVersion{}, err
		}
		current = 0
	}
	fact.TenantID, fact.OrgID, fact.CaseID, fact.FactID = id.TenantID, id.OrgID, id.CaseID, factID
	fact.Version, fact.RequestDigest, fact.IdempotencyKey, fact.ActorUserID = current+1, digest, input.IdempotencyKey, id.ActorID
	fact.ActorIssuer, fact.ActorSubject = id.Issuer, id.Subject
	if err = repo.AppendVersion(ctx, fact); err != nil {
		return model.LegalApplicabilityFactVersion{}, err
	}
	if current > 0 {
		updated, err := repo.AdvanceHead(ctx, id, factID, current, fact.Version)
		if err != nil {
			return model.LegalApplicabilityFactVersion{}, err
		}
		if !updated {
			return model.LegalApplicabilityFactVersion{}, fault.Conflict("法律适用事实已变化，请刷新后重试")
		}
	}
	return fact, nil
}
