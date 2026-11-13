package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	knowledgeApp "contractaudit/internal/knowledge/app"
	knowledgeModel "contractaudit/internal/knowledge/model"
	"contractaudit/internal/legalnorm"

	"github.com/jackc/pgx/v5"
)

// governedKnowledgeFacts adapts exact source-owned facts to Knowledge
// use cases. It never starts a transaction and keeps every query on the caller's
// updateAdminAs or readKnowledgeAs transaction.
type governedKnowledgeFacts struct {
	tx         pgx.Tx
	admin      accounts.User
	candidates map[string]ExperienceCandidate
}

func (facts governedKnowledgeFacts) RecheckAdmin(context.Context) error {
	_, err := trustedAdmin(facts.admin)
	return err
}

func (facts governedKnowledgeFacts) ResolveSourceCase(ctx context.Context, caseID string) (knowledgeApp.SourceCaseFacts, error) {
	if err := scopedCaseLock(ctx, facts.tx, caseID); err != nil {
		return knowledgeApp.SourceCaseFacts{}, err
	}
	item, err := loadKnowledgeCase(ctx, facts.tx, facts.admin, caseID)
	if err != nil {
		return knowledgeApp.SourceCaseFacts{}, err
	}
	return knowledgeApp.SourceCaseFacts{CaseID: item.ID, TenantID: item.TenantID, OrgID: item.OwnerOrgID}, nil
}

func (facts governedKnowledgeFacts) AuthorizeSource(ctx context.Context, content knowledgeModel.GovernedKnowledgeContent) error {
	return authorizeKnowledgeSourceWithCaseLocks(ctx, facts.tx, facts.admin, content, true)
}

func (facts governedKnowledgeFacts) LoadExperienceCandidate(ctx context.Context, content knowledgeModel.GovernedKnowledgeContent) (knowledgeApp.ExperienceCandidateFacts, error) {
	return facts.loadExperienceCandidate(ctx, content, true)
}

func (facts governedKnowledgeFacts) loadExperienceCandidate(ctx context.Context, content knowledgeModel.GovernedKnowledgeContent, lockCases bool) (knowledgeApp.ExperienceCandidateFacts, error) {
	if err := authorizeKnowledgeSourceWithCaseLocks(ctx, facts.tx, facts.admin, content, lockCases); err != nil {
		return knowledgeApp.ExperienceCandidateFacts{}, err
	}
	source, err := loadKnowledgeCase(ctx, facts.tx, facts.admin, content.SourceCaseID)
	if err != nil {
		return knowledgeApp.ExperienceCandidateFacts{}, err
	}
	candidate, err := scanExperienceCandidate(facts.tx.QueryRow(ctx, `SELECT `+candidateSelectColumns+` FROM experience_candidate_versions
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND candidate_id=$4 AND version_id=$5`,
		source.TenantID, source.OwnerOrgID, source.ID, content.CandidateID, content.CandidateVersionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return knowledgeApp.ExperienceCandidateFacts{}, fault.NotFound("没有找到对应经验候选")
	}
	if err != nil {
		return knowledgeApp.ExperienceCandidateFacts{}, err
	}
	if facts.candidates != nil {
		facts.candidates[candidate.VersionID] = candidate
	}
	return knowledgeApp.ExperienceCandidateFacts{
		TenantID: candidate.TenantID, OrgID: candidate.OrgID, CaseID: candidate.CaseID, FeedbackID: candidate.FeedbackID,
		CandidateID: candidate.CandidateID, VersionID: candidate.VersionID, BaseVersionID: candidate.BaseVersionID,
		ContentSHA256: candidate.ContentSHA256, JobID: candidate.JobID, Generation: candidate.Generation, ResultSHA256: candidate.ResultSHA256,
		ValidFrom: candidate.Content.ValidFrom, ValidUntil: candidate.Content.ValidUntil, PolicyRefs: candidateRefs(candidate.Content.PolicyRefs),
	}, nil
}

func (facts governedKnowledgeFacts) CandidateFeedbackExists(ctx context.Context, candidate knowledgeApp.ExperienceCandidateFacts) (bool, error) {
	var exists bool
	err := facts.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM experience_feedback WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND feedback_id=$4)`,
		candidate.TenantID, candidate.OrgID, candidate.CaseID, candidate.FeedbackID).Scan(&exists)
	return exists, err
}

func (facts governedKnowledgeFacts) AuthorizeExperienceAncestors(ctx context.Context, candidate knowledgeApp.ExperienceCandidateFacts) error {
	return authorizeExperienceAncestorsWithCaseLocks(ctx, facts.tx, facts.admin,
		Case{ID: candidate.CaseID, TenantID: candidate.TenantID}, candidate.CandidateID, candidate.BaseVersionID, true)
}

func (facts governedKnowledgeFacts) LoadLegalCapture(ctx context.Context, snapshotID string) (*knowledgeApp.LegalCaptureFacts, error) {
	var capture legalCaptureReference
	err := capture.scan(facts.tx.QueryRow(ctx, `SELECT `+legalCaptureReferenceColumns+` FROM legal_source_captures WHERE tenant_id=$1 AND capture_snapshot_id=$2`,
		accounts.Tenant(facts.admin), snapshotID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fault.Conflict("法律条文引用的原始来源快照不存在")
	}
	if err != nil {
		return nil, err
	}
	return knowledgeLegalCaptureFacts(capture), nil
}

func (facts governedKnowledgeFacts) LoadLegalCaptures(ctx context.Context, snapshotIDs []string) (map[string]*knowledgeApp.LegalCaptureFacts, error) {
	out := make(map[string]*knowledgeApp.LegalCaptureFacts, len(snapshotIDs))
	if len(snapshotIDs) == 0 {
		return out, nil
	}
	rows, err := facts.tx.Query(ctx, `SELECT `+legalCaptureReferenceColumns+` FROM legal_source_captures WHERE tenant_id=$1 AND capture_snapshot_id=ANY($2::text[])`,
		accounts.Tenant(facts.admin), snapshotIDs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var capture legalCaptureReference
		if err := capture.scan(rows); err != nil {
			rows.Close()
			return nil, err
		}
		out[capture.id] = knowledgeLegalCaptureFacts(capture)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return out, nil
}

func knowledgeLegalCaptureFacts(capture legalCaptureReference) *knowledgeApp.LegalCaptureFacts {
	return &knowledgeApp.LegalCaptureFacts{
		SnapshotID: capture.id, SourceID: capture.sourceID, VersionID: capture.versionID, NormTitle: capture.normTitle,
		DocumentType: capture.documentType, Publisher: capture.publisher, Issuer: capture.issuer, OfficialURL: capture.officialURL,
		Provenance: capture.provenance, PromulgatedOn: capture.promulgatedOn, EffectiveFrom: capture.effectiveFrom,
		EffectiveUntil: capture.effectiveUntil, SourceCapturedAt: capture.sourceCapturedAt, ResponseStatus: capture.responseStatus,
		MediaType: capture.mediaType, RawResponse: capture.raw, RawSHA256: capture.rawSHA, ExtractedFullText: capture.extracted,
		ExtractedSHA256: capture.extractedSHA, ExtractorID: capture.extractorID, ExtractorVersion: capture.extractorVersion,
		CanonicalVersion: capture.canonicalVersion, StatusID: capture.statusID, Status: capture.status,
		StatusRegistryURL: capture.statusRegistryURL, StatusMethod: capture.statusMethod, StatusRequestBody: capture.statusRequestBody,
		StatusObservedAt: capture.statusObservedAt, StatusResponseStatus: capture.statusResponseStatus,
		StatusMediaType: capture.statusMediaType, StatusResponse: capture.statusRaw, StatusSHA256: capture.statusSHA,
		ArticleManifest: capture.articleRaw, ArticleManifestSHA256: capture.articleManifestSHA,
	}
}

func (governedKnowledgeFacts) LoadLegalArticleIndex(_ context.Context, capture *knowledgeApp.LegalCaptureFacts) ([]legalnorm.VerifiedArticle, error) {
	var articles []legalnorm.VerifiedArticle
	if err := json.Unmarshal(capture.ArticleManifest, &articles); err != nil || len(articles) == 0 {
		return nil, fault.Conflict("法律来源快照的条文索引无效")
	}
	canonical, err := legalArticleManifest(articles)
	if err != nil || sha256Bytes(canonical) != capture.ArticleManifestSHA256 {
		return nil, fault.Conflict("法律来源快照的条文索引不是规范编码")
	}
	return articles, nil
}

func (facts governedKnowledgeFacts) AuthorizeReadSource(ctx context.Context, content knowledgeModel.GovernedKnowledgeContent) error {
	return authorizeKnowledgeSourceWithCaseLocks(ctx, facts.tx, facts.admin, content, false)
}
func (facts governedKnowledgeFacts) LoadReadCandidate(ctx context.Context, content knowledgeModel.GovernedKnowledgeContent) (knowledgeApp.ExperienceCandidateFacts, error) {
	return facts.loadExperienceCandidate(ctx, content, false)
}
func (facts governedKnowledgeFacts) AuthorizeReadAncestors(ctx context.Context, candidate knowledgeApp.ExperienceCandidateFacts) error {
	return authorizeExperienceAncestorsWithCaseLocks(ctx, facts.tx, facts.admin, Case{ID: candidate.CaseID, TenantID: candidate.TenantID}, candidate.CandidateID, candidate.BaseVersionID, false)
}

func (facts governedKnowledgeFacts) VersionSlotOccupied(ctx context.Context, knowledgeID, displayVersion string) (bool, error) {
	return knowledgeVersionSlotOccupied(ctx, facts.tx, accounts.Tenant(facts.admin), knowledgeID, displayVersion)
}

var _ knowledgeApp.CreateDraftFacts = governedKnowledgeFacts{}
var _ knowledgeApp.KnowledgeReadFacts = governedKnowledgeFacts{}
var _ knowledgeApp.PolicyPreviewFacts = governedKnowledgeFacts{}

func candidateRefs(refs []ExperiencePolicyRef) []knowledgeModel.KnowledgeRef {
	out := make([]knowledgeModel.KnowledgeRef, len(refs))
	for i, r := range refs {
		out[i] = knowledgeModel.KnowledgeRef{KnowledgeID: r.KnowledgeID, VersionID: r.VersionID, ContentSHA256: r.ContentSHA256}
	}
	return out
}

func (facts governedKnowledgeFacts) LoadRulePackManifest(ctx context.Context, content knowledgeModel.GovernedKnowledgeContent) (knowledgeApp.RulePackManifestFacts, error) {
	var result knowledgeApp.RulePackManifestFacts
	err := facts.tx.QueryRow(ctx, `SELECT manifest,content_hash,snapshot_digest FROM rule_pack_versions WHERE tenant_id=$1 AND pack_id=$2 AND version=$3`,
		accounts.Tenant(facts.admin), content.Binding.PackID, content.Binding.PackVersion).Scan(&result.Manifest, &result.ContentHash, &result.SnapshotDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, fault.Conflict("制度引用的规则包不存在")
	}
	return result, err
}

func (facts governedKnowledgeFacts) LoadRulePack(ctx context.Context, content knowledgeModel.GovernedKnowledgeContent) (knowledgeApp.RulePackBindingFacts, error) {
	var result knowledgeApp.RulePackBindingFacts
	err := facts.tx.QueryRow(ctx, `SELECT v.manifest,v.content_hash,v.snapshot_digest,v.status,COALESCE(p.version,''),COALESCE(p.active,false) FROM rule_pack_versions v LEFT JOIN rule_pack_pointers p ON p.tenant_id=v.tenant_id AND p.pack_id=v.pack_id WHERE v.tenant_id=$1 AND v.pack_id=$2 AND v.version=$3`, accounts.Tenant(facts.admin), content.Binding.PackID, content.Binding.PackVersion).Scan(&result.Manifest, &result.ContentHash, &result.SnapshotDigest, &result.Status, &result.PointerVersion, &result.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, fault.Conflict("制度没有对应的已发布规则包")
	}
	return result, err
}
func (facts governedKnowledgeFacts) exactCandidate(c knowledgeApp.ExperienceCandidateFacts) (ExperienceCandidate, error) {
	candidate, ok := facts.candidates[c.VersionID]
	if !ok || candidate.CandidateID != c.CandidateID || candidate.TenantID != c.TenantID || candidate.OrgID != c.OrgID || candidate.CaseID != c.CaseID {
		return ExperienceCandidate{}, errors.New("candidate facts unavailable")
	}
	return candidate, nil
}
func (facts governedKnowledgeFacts) CheckCandidateSource(ctx context.Context, c knowledgeApp.ExperienceCandidateFacts) error {
	candidate, err := facts.exactCandidate(c)
	if err != nil {
		return err
	}
	return validateExperienceCandidateSource(ctx, facts.tx, Case{ID: c.CaseID, TenantID: c.TenantID, OwnerOrgID: c.OrgID}, candidate)
}
func (facts governedKnowledgeFacts) CheckCandidateCompleteness(ctx context.Context, c knowledgeApp.ExperienceCandidateFacts, now time.Time) error {
	candidate, err := facts.exactCandidate(c)
	if err != nil {
		return err
	}
	return candidate.ApprovalReady(now)
}
