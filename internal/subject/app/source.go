// File metadata (manifest mirror):
// Purpose: Freezes controlled Subject source snapshots and exact-match proposals with canonical integrity, idempotent receipts and preserved partial error results.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/subject/model"
)

type SourceClockIDs interface {
	Now() time.Time
	NewSnapshotID() (string, error)
	NewRequestID() (string, error)
}

func ValidateSourceSnapshotInput(input model.SubjectSourceSnapshotCreateInput) error {
	if input.SourceID != model.SyntheticSubjectSourceID || input.ExpectedSourceVersion < 1 {
		return fault.Invalid("受控来源及预期版本无效")
	}
	return nil
}
func ReplaySourceSnapshot(snapshot Snapshot, actor Actor, caseID, key, hash string) (model.SubjectSourceSnapshotResult, bool, error) {
	var out model.SubjectSourceSnapshotResult
	found, err := replay(snapshot, actor, "create_subject_source_snapshot", caseID, key, hash, &out)
	return out, found, err
}
func ReplaySourceVerification(snapshot Snapshot, actor Actor, target, key, hash string) (model.SubjectSourceVerifyResult, bool, error) {
	var out model.SubjectSourceVerifyResult
	found, err := replay(snapshot, actor, "verify_subject_source", target, key, hash, &out)
	return out, found, err
}
func sourceReceipt(actor Actor, operation, target, key, hash string, status int, result any, now time.Time) (model.SubjectIdempotencyRecord, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return model.SubjectIdempotencyRecord{}, err
	}
	return model.SubjectIdempotencyRecord{TenantID: actor.TenantID, ActorUserID: actor.Principal.UserID, Operation: operation, TargetID: target, Key: key, PayloadHash: hash, Response: raw, HTTPStatus: status, CreatedAt: now}, nil
}
func CreateSourceSnapshot(snapshot Snapshot, actor Actor, caseID CaseIdentity, input model.SubjectSourceSnapshotCreateInput, live *model.SubjectSourceManifest, key, hash string, clock SourceClockIDs) (Delta, model.SubjectSourceSnapshotResult, error) {
	if live == nil || live.SourceID != input.SourceID || live.SourceVersion != input.ExpectedSourceVersion {
		return Delta{}, model.SubjectSourceSnapshotResult{}, fault.Conflict("受控来源版本已变化，请刷新后重试")
	}
	payload, err := json.Marshal(live)
	if err != nil {
		return Delta{}, model.SubjectSourceSnapshotResult{}, err
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	for _, existing := range snapshot.Sources {
		if existing.TenantID == actor.TenantID && existing.SourceCatalogID == live.SourceID && existing.SourceVersion == live.SourceVersion && existing.SourceDigest != digest {
			return Delta{}, model.SubjectSourceSnapshotResult{}, fault.Conflict("同一受控来源版本已有不同内容，请提升来源版本")
		}
	}
	result := model.SubjectSourceSnapshotResult{SourceID: live.SourceID, SourceVersion: live.SourceVersion, SourceDigest: digest, SourceScope: live.VerificationScope, CapturedAt: clock.Now().UTC(), RecordCount: len(live.Records), Label: live.DisplayName}
	id, err := clock.NewSnapshotID()
	if err != nil {
		return Delta{}, result, err
	}
	result.SnapshotID = id
	source := model.SubjectSourceSnapshot{TenantID: actor.TenantID, OrgID: caseID.OrgID, CaseID: caseID.CaseID, SnapshotID: id, SourceCatalogID: live.SourceID, SourceVersion: live.SourceVersion, SourcePayload: string(payload), SourceDigest: digest, SourceScope: live.VerificationScope, CapturedAt: result.CapturedAt, CreatedByUser: actor.Principal.UserID, CreatedByIssuer: actor.Principal.Issuer, CreatedBySubject: actor.Principal.Subject}
	saved, err := sourceReceipt(actor, "create_subject_source_snapshot", caseID.CaseID, key, hash, 201, result, clock.Now().UTC())
	if err != nil {
		return Delta{}, result, err
	}
	return Delta{Sources: []model.SubjectSourceSnapshot{source}, Receipts: []model.SubjectIdempotencyRecord{saved}}, result, nil
}
func DecodeFrozenSource(item model.SubjectSourceSnapshot) (model.SubjectSourceManifest, error) {
	digest := sha256.Sum256([]byte(item.SourcePayload))
	if hex.EncodeToString(digest[:]) != item.SourceDigest {
		return model.SubjectSourceManifest{}, fault.New(500, "integrity_error", "来源快照完整性校验失败")
	}
	var manifest model.SubjectSourceManifest
	decoder := json.NewDecoder(strings.NewReader(item.SourcePayload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil || manifest.SourceID != item.SourceCatalogID || manifest.SourceVersion != item.SourceVersion || manifest.VerificationScope != item.SourceScope || item.SourceScope != "synthetic_controlled" {
		return model.SubjectSourceManifest{}, fault.New(500, "integrity_error", "来源快照内容与元数据不一致")
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || string(canonical) != item.SourcePayload {
		return model.SubjectSourceManifest{}, fault.New(500, "integrity_error", "来源快照不是规范序列化数据")
	}
	return manifest, nil
}

type VerifiedSource struct {
	Source   model.SubjectSourceSnapshot
	Manifest model.SubjectSourceManifest
}

func PrepareSourceVerification(snapshot Snapshot, actor Actor, caseID CaseIdentity, snapshotID string) (VerifiedSource, error) {
	var source *model.SubjectSourceSnapshot
	for i := range snapshot.Sources {
		item := &snapshot.Sources[i]
		if item.TenantID == actor.TenantID && item.OrgID == caseID.OrgID && item.CaseID == caseID.CaseID && item.SnapshotID == snapshotID {
			source = item
			break
		}
	}
	if source == nil {
		return VerifiedSource{}, fault.NotFound("没有找到案件来源快照")
	}
	manifest, err := DecodeFrozenSource(*source)
	if err != nil {
		return VerifiedSource{}, err
	}
	return VerifiedSource{Source: *source, Manifest: manifest}, nil
}
func VerifySource(snapshot Snapshot, actor Actor, caseID CaseIdentity, prepared VerifiedSource, input model.SubjectSourceVerifyInput, readable map[string]bool, key, hash string, clock SourceClockIDs) (Delta, model.SubjectSourceVerifyResult, error) {
	source, manifest := &prepared.Source, prepared.Manifest
	snapshotID := source.SnapshotID
	subject, version, visible := VisibleSubject(snapshot, actor, input.SubjectID, readable)
	c := snapshot.Collections
	if !visible || subject.TenantID != actor.TenantID || subject.CurrentVersion != input.ExpectedSubjectVersion || !model.CaseHasSubject(c.CaseSubjectRoles, c.CaseSubjectRoleVersions, caseID.CaseID, subject.ID) {
		return Delta{}, model.SubjectSourceVerifyResult{}, fault.NotFound("没有找到对应案件主体")
	}
	claimed := ""
	for _, claim := range version.IdentityClaims {
		if claim.Namespace == "cn.gov.samr.uscc" && claim.IdentifierType == "unified_social_credit_code" {
			if claim.VerificationState == "verified" && claim.VerificationScope != "synthetic_controlled" {
				return Delta{}, model.SubjectSourceVerifyResult{}, fault.Conflict("当前主体已有其他来源核验，不能由synthetic受控来源替换")
			}
			claimed = strings.ToUpper(strings.TrimSpace(claim.Value))
			break
		}
	}
	matches := make([]model.SubjectSourceRecord, 0, 1)
	for _, record := range manifest.Records {
		if claimed != "" && record.USCC == claimed {
			matches = append(matches, record)
		}
	}
	result := model.SubjectSourceVerifyResult{State: "not_proposed", MatchOutcome: "no_match", SnapshotID: source.SnapshotID, SourceCatalogID: source.SourceCatalogID, SourceVersion: source.SourceVersion, SourceDigest: source.SourceDigest, SourceScope: source.SourceScope}
	status := 200
	var requests []model.SubjectChangeRequest
	if len(matches) > 1 {
		result.MatchOutcome = "ambiguous"
	} else if len(matches) == 1 && (matches[0].DisplayName == version.DisplayName || (version.LegalName != "" && matches[0].DisplayName == version.LegalName)) {
		record := matches[0]
		asOf, validFrom, validUntil := record.SourceAsOf.UTC(), record.ValidFrom.UTC(), (*time.Time)(nil)
		if record.ValidUntil != nil {
			value := record.ValidUntil.UTC()
			validUntil = &value
		}
		result.SourceAsOf, result.ValidFrom, result.ValidUntil = &asOf, &validFrom, validUntil
		result.MatchOutcome = "exact_match"
		result.MatchedRecord = &model.SubjectSourceMatchedRecord{DisplayName: record.DisplayName, MaskedIdentifier: model.MaskUSCC(record.USCC)}
		version.Version = subject.CurrentVersion + 1
		version.VerificationState, version.VerificationScope, version.SourceKind = "verified", "synthetic_controlled", "authorized_correction"
		version.BasisSummary = "按版本化synthetic_controlled受控来源精确比对名称和统一社会信用代码"
		version.CreatedByUser, version.CreatedByIssuer, version.CreatedBySubject = actor.Principal.UserID, actor.Principal.Issuer, actor.Principal.Subject
		version.CreatedAt = clock.Now().UTC()
		version.IdentityClaims = []model.SubjectIdentityClaim{{Namespace: "cn.gov.samr.uscc", IdentifierType: "unified_social_credit_code", Value: record.USCC, VerificationState: "verified", VerificationScope: "synthetic_controlled", SourceKind: "synthetic_controlled_snapshot", SourceTime: &asOf, SourceCatalogID: source.SourceCatalogID, SourceSnapshotID: source.SnapshotID, SourceDigest: source.SourceDigest, SourceValidFrom: &validFrom, SourceValidUntil: validUntil}}
		version.IdentityClaims[0].PayloadHash = model.HashSubjectClaim(version.IdentityClaims[0])
		version.PayloadHash = model.HashSubjectVersion(version)
		canonical, err := json.Marshal(model.SubjectAfterPayload{SubjectVersion: version})
		if err != nil {
			return Delta{}, result, err
		}
		basis, err := json.Marshal([]model.ControlledSourceBasisRef{{Kind: "controlled_source", SnapshotID: source.SnapshotID, SourceCatalogID: source.SourceCatalogID, SourceVersion: source.SourceVersion, SourceDigest: source.SourceDigest, SourceScope: source.SourceScope, SourceAsOf: asOf, ValidFrom: validFrom, ValidUntil: validUntil, CapturedAt: source.CapturedAt}})
		if err != nil {
			return Delta{}, result, err
		}
		bindings := model.AffectedSubjectBindings(c.CaseSubjectRoles, c.CaseSubjectRoleVersions, actor.TenantID, subject.ID, "subject", "")
		bindingsHash, err := model.CanonicalSubjectHash(bindings)
		if err != nil {
			return Delta{}, result, err
		}
		id, err := clock.NewRequestID()
		if err != nil {
			return Delta{}, result, err
		}
		digest := sha256.Sum256(canonical)
		requests = []model.SubjectChangeRequest{{TenantID: actor.TenantID, OrgID: caseID.OrgID, RequestID: id, SourceCaseID: caseID.CaseID, TargetKind: "subject", TargetSubjectID: subject.ID, ExpectedSubjectVersion: subject.CurrentVersion, CanonicalPayload: canonical, CanonicalPayloadHash: hex.EncodeToString(digest[:]), BasisNote: "按版本化受控来源精确比对名称与统一社会信用代码；来源范围为synthetic_controlled。", BasisRefs: basis, ExpectedBindingsHash: bindingsHash, ProposedBy: actor.Principal, CreatedAt: clock.Now().UTC()}}
		result.RequestID, result.State = &id, "pending_decision"
		status = 202
	}
	target := caseID.CaseID + "/" + snapshotID + "/" + input.SubjectID
	saved, err := sourceReceipt(actor, "verify_subject_source", target, key, hash, status, result, clock.Now().UTC())
	if err != nil {
		return Delta{}, result, err
	}
	return Delta{Requests: requests, Receipts: []model.SubjectIdempotencyRecord{saved}}, result, nil
}
