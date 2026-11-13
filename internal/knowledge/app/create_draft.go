// Package app coordinates Knowledge use cases with caller-owned repositories
// and exact, domain-specific source facts.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/knowledge/model"
	"contractaudit/internal/legalnorm"
)

// CreateDraftIdentity is supplied from the server's trusted admin resolution.
type CreateDraftIdentity struct {
	TenantID string
	ActorID  string
}

type SourceCaseFacts struct {
	CaseID   string
	TenantID string
	OrgID    string
}

// ExperienceCandidateFacts contains only the exact persisted identity needed
// to bind a governed experience version to its immutable source revision.
type ExperienceCandidateFacts struct {
	TenantID              string
	OrgID                 string
	CaseID                string
	FeedbackID            string
	CandidateID           string
	VersionID             string
	BaseVersionID         string
	ContentSHA256         string
	JobID                 string
	Generation            int64
	ResultSHA256          string
	ValidFrom, ValidUntil string
	PolicyRefs            []model.KnowledgeRef
}

// LegalCaptureFacts contains one exact immutable capture. App rules verify the
// stored source digests and compare metadata/article facts with Knowledge
// content; the Store bridge only retrieves the exact Legal-owned row.
type LegalCaptureFacts struct {
	SnapshotID            string
	SourceID              string
	VersionID             string
	NormTitle             string
	DocumentType          string
	Publisher             string
	Issuer                string
	OfficialURL           string
	Provenance            string
	PromulgatedOn         string
	EffectiveFrom         string
	EffectiveUntil        string
	SourceCapturedAt      time.Time
	ResponseStatus        int
	MediaType             string
	RawResponse           []byte
	RawSHA256             string
	ExtractedFullText     string
	ExtractedSHA256       string
	ExtractorID           string
	ExtractorVersion      string
	CanonicalVersion      string
	StatusID              string
	Status                string
	StatusRegistryURL     string
	StatusMethod          string
	StatusRequestBody     []byte
	StatusObservedAt      time.Time
	StatusResponseStatus  int
	StatusMediaType       string
	StatusResponse        []byte
	StatusSHA256          string
	ArticleManifest       []byte
	ArticleManifestSHA256 string

	digestsChecked  bool
	digestsValid    bool
	articlesChecked bool
	articlesErr     error
	articles        []legalnorm.VerifiedArticle
}

// LegalFactsReader reads exact Legal-owned captures; batch loading lets list
// use cases validate every version without one capture query per row.
type LegalFactsReader interface {
	LoadLegalCapture(context.Context, string) (*LegalCaptureFacts, error)
	LoadLegalCaptures(context.Context, []string) (map[string]*LegalCaptureFacts, error)
	LoadLegalArticleIndex(context.Context, *LegalCaptureFacts) ([]legalnorm.VerifiedArticle, error)
}

// CreateDraftFacts supplies exact dependencies not owned by Knowledge. Every
// case/candidate/legal method is scoped to one identity from the request or a
// previously returned fact; implementations retain the caller's transaction.
type CreateDraftFacts interface {
	RecheckAdmin(context.Context) error
	ResolveSourceCase(context.Context, string) (SourceCaseFacts, error)
	AuthorizeSource(context.Context, model.GovernedKnowledgeContent) error
	LoadExperienceCandidate(context.Context, model.GovernedKnowledgeContent) (ExperienceCandidateFacts, error)
	CandidateFeedbackExists(context.Context, ExperienceCandidateFacts) (bool, error)
	AuthorizeExperienceAncestors(context.Context, ExperienceCandidateFacts) error
	LegalFactsReader
	VersionSlotOccupied(context.Context, string, string) (bool, error)
}

// CreateVersionRecord carries the canonical bytes and derived identity for one
// immutable version insert.
type CreateVersionRecord struct {
	Identity       CreateDraftIdentity
	Content        model.GovernedKnowledgeContent
	VersionID      string
	ScopeKey       string
	ContentSHA256  string
	ContentBytes   []byte
	CreatedBy      string
	LegalCaptureID string
}

// KnowledgePointer is the existing pointer state observed during draft create.
type KnowledgePointer struct {
	VersionID       string
	Active          bool
	PointerRevision int64
}

// CreateDraftRepository owns only Knowledge governance rows and accepts the
// caller-owned transaction through its constructor.
type CreateDraftRepository interface {
	IdempotencyReceipt(context.Context, CreateDraftIdentity, string) (digest string, response []byte, found bool, err error)
	GovernedFamilyHasOtherKind(context.Context, string, string, string) (bool, error)
	InsertVersion(context.Context, CreateVersionRecord) (time.Time, error)
	InsertInitialState(context.Context, CreateDraftIdentity, string, string) error
	InsertPolicyBinding(context.Context, CreateDraftIdentity, model.GovernedKnowledgeContent, string, []byte) error
	CurrentPointer(context.Context, CreateDraftIdentity, string, string) (KnowledgePointer, error)
	AppendHistory(context.Context, CreateDraftIdentity, model.GovernedKnowledgeHistoryEntry) error
	InsertIdempotencyReceipt(context.Context, CreateDraftIdentity, string, string, []byte) error
}

// CreateGovernedKnowledge normalizes and validates one draft, then persists its
// immutable version and receipt through the same caller-owned transaction.
func CreateGovernedKnowledge(ctx context.Context, repository CreateDraftRepository, facts CreateDraftFacts, identity CreateDraftIdentity, input model.CreateGovernedKnowledgeInput) (model.GovernedKnowledgeVersion, error) {
	if !model.ValidText(input.IdempotencyKey, 128) || input.IdempotencyKey != strings.TrimSpace(input.IdempotencyKey) {
		return model.GovernedKnowledgeVersion{}, fault.Invalid("需要有效的请求标识（幂等键）")
	}
	digest, err := createRequestDigest(input.Content)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	storedDigest, response, found, err := repository.IdempotencyReceipt(ctx, identity, input.IdempotencyKey)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	if found {
		if storedDigest != digest {
			return model.GovernedKnowledgeVersion{}, fault.Conflict("这个请求标识已用于其他操作")
		}
		var out model.GovernedKnowledgeVersion
		if err := json.Unmarshal(response, &out); err != nil {
			return model.GovernedKnowledgeVersion{}, err
		}
		if err := facts.RecheckAdmin(ctx); err != nil {
			return model.GovernedKnowledgeVersion{}, err
		}
		if err := facts.AuthorizeSource(ctx, out.Content); err != nil {
			return model.GovernedKnowledgeVersion{}, err
		}
		if out.Content.Kind == "experience" {
			candidate, err := loadCandidateFacts(ctx, facts, out.Content)
			if err != nil {
				return model.GovernedKnowledgeVersion{}, err
			}
			if err := facts.AuthorizeExperienceAncestors(ctx, candidate); err != nil {
				return model.GovernedKnowledgeVersion{}, err
			}
		}
		return out, nil
	}

	content := input.Content
	content.Encoding = model.ContentEncoding
	if content.Kind == "policy" {
		content.Binding.ExtractionCompilerRef = model.PolicyThresholdCompilerRef
	}
	content.SourceOrgID = "" // Source organization is derived from the authorized case.
	if content.SourceCaseID != "" {
		sourceCase, err := facts.ResolveSourceCase(ctx, content.SourceCaseID)
		if err != nil {
			return model.GovernedKnowledgeVersion{}, err
		}
		content.SourceOrgID = sourceCase.OrgID
	}
	if content.Kind == "experience" {
		content.SourceFeedbackID = ""
		candidate, err := loadCandidateFacts(ctx, facts, content)
		if err != nil {
			return model.GovernedKnowledgeVersion{}, err
		}
		if err := facts.AuthorizeExperienceAncestors(ctx, candidate); err != nil {
			return model.GovernedKnowledgeVersion{}, err
		}
		content.SourceFeedbackID = candidate.FeedbackID
	}
	if err := model.ValidateContent(content); err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	if content.Kind == "legal" {
		capture, err := facts.LoadLegalCapture(ctx, content.LegalMetadata.SourceSnapshotID)
		if err != nil {
			return model.GovernedKnowledgeVersion{}, err
		}
		if err := validateLegalCaptureReference(ctx, facts, content, capture); err != nil {
			return model.GovernedKnowledgeVersion{}, err
		}
	}
	if err := facts.AuthorizeSource(ctx, content); err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	occupied, err := facts.VersionSlotOccupied(ctx, content.KnowledgeID, content.Version)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	if occupied {
		return model.GovernedKnowledgeVersion{}, fault.Conflict("制度版本已经存在；旧制度不会自动升级为已治理内容")
	}
	differentKind, err := repository.GovernedFamilyHasOtherKind(ctx, identity.TenantID, content.KnowledgeID, content.Kind)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	if differentKind {
		return model.GovernedKnowledgeVersion{}, fault.Conflict("制度族的类型不可改变")
	}
	raw, contentDigest, err := model.ContentDigest(content)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	binding, err := json.Marshal(content.Binding)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	scopeKey, err := model.ScopeKey(content.Scope)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	versionID := model.VersionID(identity.TenantID, content.KnowledgeID, content.Version)
	ref := model.KnowledgeRef{KnowledgeID: content.KnowledgeID, VersionID: versionID, ContentSHA256: contentDigest}
	legalCaptureID := ""
	if content.Kind == "legal" {
		legalCaptureID = content.LegalMetadata.SourceSnapshotID
	}
	created, err := repository.InsertVersion(ctx, CreateVersionRecord{
		Identity: identity, Content: content, VersionID: versionID, ScopeKey: scopeKey,
		ContentSHA256: contentDigest, ContentBytes: raw, CreatedBy: identity.ActorID, LegalCaptureID: legalCaptureID,
	})
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	if err := repository.InsertInitialState(ctx, identity, content.KnowledgeID, versionID); err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	if content.Kind != "legal" {
		if err := repository.InsertPolicyBinding(ctx, identity, content, versionID, binding); err != nil {
			return model.GovernedKnowledgeVersion{}, err
		}
	}
	pointer, err := repository.CurrentPointer(ctx, identity, content.KnowledgeID, scopeKey)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	out := model.GovernedKnowledgeVersion{
		Ref: ref, Content: content, Status: "draft", Revision: 1,
		CreatedBy: identity.ActorID, CreatedAt: created.UTC().Format(time.RFC3339Nano),
		PointerRevision: pointer.PointerRevision,
	}
	if err := repository.AppendHistory(ctx, identity, model.GovernedKnowledgeHistoryEntry{
		Ref: ref, Action: "create", Revision: 1, PointerRevision: pointer.PointerRevision, ActorID: identity.ActorID,
		PreviousVersionID: pointer.VersionID, PreviousActive: pointer.Active,
		ResultingVersionID: pointer.VersionID, ResultingActive: pointer.Active,
	}); err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	if err := facts.RecheckAdmin(ctx); err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	if err := facts.AuthorizeSource(ctx, out.Content); err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	responseBytes, err := json.Marshal(out)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	if err := repository.InsertIdempotencyReceipt(ctx, identity, input.IdempotencyKey, digest, responseBytes); err != nil {
		return out, err
	}
	return out, nil
}

func createRequestDigest(content model.GovernedKnowledgeContent) (string, error) {
	raw, err := json.Marshal(struct {
		Action  string                         `json:"action"`
		Content model.GovernedKnowledgeContent `json:"content"`
	}{"create", content})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

type candidateFactsReader interface {
	LoadExperienceCandidate(context.Context, model.GovernedKnowledgeContent) (ExperienceCandidateFacts, error)
	CandidateFeedbackExists(context.Context, ExperienceCandidateFacts) (bool, error)
}

func loadCandidateFacts(ctx context.Context, facts candidateFactsReader, content model.GovernedKnowledgeContent) (ExperienceCandidateFacts, error) {
	if err := model.ValidateExperienceCandidateReference(content); err != nil {
		return ExperienceCandidateFacts{}, err
	}
	candidate, err := facts.LoadExperienceCandidate(ctx, content)
	if err != nil {
		return ExperienceCandidateFacts{}, err
	}
	identity, err := json.Marshal([]any{"experience-candidate-version/v1", candidate.TenantID, candidate.CandidateID, candidate.JobID, candidate.Generation, candidate.ResultSHA256})
	if err != nil || model.SHA256(identity) != candidate.VersionID || candidate.CandidateID != content.CandidateID ||
		candidate.VersionID != content.CandidateVersionID || candidate.ContentSHA256 != content.CandidateSHA256 ||
		(content.SourceFeedbackID != "" && content.SourceFeedbackID != candidate.FeedbackID) {
		return ExperienceCandidateFacts{}, fault.Conflict("经验候选的编号或来源摘要不一致")
	}
	feedbackExists, err := facts.CandidateFeedbackExists(ctx, candidate)
	if err != nil {
		return ExperienceCandidateFacts{}, err
	}
	if !feedbackExists {
		return ExperienceCandidateFacts{}, fault.Conflict("经验候选的反馈来源不存在")
	}
	return candidate, nil
}

func validateLegalCaptureMetadata(content model.GovernedKnowledgeContent, capture LegalCaptureFacts) error {
	metadata := content.LegalMetadata
	if metadata.SourceID != capture.SourceID || metadata.NormVersion != capture.VersionID || metadata.NormTitle != capture.NormTitle || metadata.DocumentType != capture.DocumentType ||
		metadata.Publisher != capture.Publisher || metadata.Issuer != capture.Issuer || metadata.OfficialURL != capture.OfficialURL || metadata.SourceProvenance != capture.Provenance ||
		metadata.PromulgatedOn != capture.PromulgatedOn || metadata.EffectiveFrom != capture.EffectiveFrom || metadata.EffectiveUntil != capture.EffectiveUntil ||
		!sameTime(metadata.SourceCapturedAt, capture.SourceCapturedAt) || metadata.SourceResponseStatus != capture.ResponseStatus || metadata.SourceContentType != capture.MediaType ||
		metadata.SourceSHA256 != capture.RawSHA256 || metadata.OfficialStatusObservationID != capture.StatusID || metadata.OfficialStatus != capture.Status ||
		metadata.StatusRegistryURL != capture.StatusRegistryURL || !sameTime(metadata.StatusObservedAt, capture.StatusObservedAt) ||
		metadata.StatusResponseStatus != capture.StatusResponseStatus || metadata.StatusContentType != capture.StatusMediaType || metadata.StatusEvidenceSHA256 != capture.StatusSHA256 ||
		metadata.ExtractorID != capture.ExtractorID || metadata.ExtractorVersion != capture.ExtractorVersion || metadata.CanonicalTextVersion != capture.CanonicalVersion {
		return fault.Conflict("法律条文元数据与不可变来源快照不一致")
	}
	return nil
}

func validateLegalCaptureReference(ctx context.Context, facts LegalFactsReader, content model.GovernedKnowledgeContent, capture *LegalCaptureFacts) error {
	if capture == nil {
		return fault.Conflict("法律条文引用的原始来源快照不存在")
	}
	if !capture.digestsChecked {
		capture.digestsChecked = true
		capture.digestsValid = model.SHA256(capture.RawResponse) == capture.RawSHA256 &&
			model.SHA256([]byte(capture.ExtractedFullText)) == capture.ExtractedSHA256 && model.SHA256(capture.StatusResponse) == capture.StatusSHA256
	}
	if !capture.digestsValid {
		return fault.Conflict("法律来源快照内容摘要不匹配")
	}
	if err := validateLegalCaptureMetadata(content, *capture); err != nil {
		return err
	}
	if !capture.articlesChecked {
		capture.articlesChecked = true
		capture.articles, capture.articlesErr = facts.LoadLegalArticleIndex(ctx, capture)
	}
	if capture.articlesErr != nil {
		return capture.articlesErr
	}
	return validateLegalArticleBinding(content, *capture, capture.articles)
}

func sameTime(raw string, stored time.Time) bool {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	return err == nil && parsed.Equal(stored)
}

func validateLegalArticleBinding(content model.GovernedKnowledgeContent, capture LegalCaptureFacts, articles []legalnorm.VerifiedArticle) error {
	metadata := content.LegalMetadata
	for _, article := range articles {
		if article.Ref.ArticleNumber != metadata.ArticleNumber {
			continue
		}
		runes := []rune(capture.ExtractedFullText)
		if article.Ref.SourceID != capture.SourceID || article.Ref.SourceSnapshotID != metadata.SourceSnapshotID || article.Ref.VersionID != capture.VersionID ||
			article.FullText != content.Body || article.Ref.FullText != article.FullText || article.TextSHA256 != metadata.NormTextSHA256 ||
			article.Start != metadata.SourceTextStart || article.End != metadata.SourceTextEnd || article.Start < 0 || article.End > len(runes) ||
			string(runes[article.Start:article.End]) != article.FullText || article.Ref.PromulgatedOn != metadata.PromulgatedOn ||
			article.Ref.EffectiveFrom != metadata.EffectiveFrom || article.Ref.EffectiveUntil != metadata.EffectiveUntil || article.Ref.ArticleLabel != metadata.ArticleLabel {
			return fault.Conflict("法律条文正文、定位或版本与原始来源提取结果不一致")
		}
		return nil
	}
	return fault.Conflict("法律来源快照没有引用的条文号")
}
