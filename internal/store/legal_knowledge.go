package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/legalnorm"
	"contractaudit/internal/rulepack"

	"github.com/jackc/pgx/v5"
)

// LegalMetadata binds one governed article body to an immutable captured
// official source and point-in-time registry observation. Legal review is
// deliberately fixed at NOT_RUN for this product slice.
type LegalMetadata struct {
	SourceID                    string               `json:"source_id"`
	NormTitle                   string               `json:"norm_title"`
	DocumentType                string               `json:"document_type"`
	NormVersion                 string               `json:"norm_version"`
	ArticleNumber               int                  `json:"article_number"`
	ArticleLabel                string               `json:"article_label"`
	Issuer                      string               `json:"issuer"`
	Publisher                   string               `json:"publisher"`
	OfficialURL                 string               `json:"official_url"`
	PromulgatedOn               string               `json:"promulgated_on"`
	EffectiveFrom               string               `json:"effective_from"`
	EffectiveUntil              string               `json:"effective_until,omitempty"`
	OfficialStatusObservationID string               `json:"official_status_observation_id"`
	OfficialStatus              string               `json:"official_status"`
	StatusRegistryURL           string               `json:"status_registry_url"`
	StatusObservedAt            string               `json:"status_observed_at"`
	StatusEvidenceSHA256        string               `json:"status_evidence_sha256"`
	StatusResponseStatus        int                  `json:"status_response_status"`
	StatusContentType           string               `json:"status_content_type"`
	SourceSnapshotID            string               `json:"source_snapshot_id"`
	SourceProvenance            string               `json:"source_provenance"`
	SourceCapturedAt            string               `json:"source_captured_at"`
	SourceResponseStatus        int                  `json:"source_response_status"`
	SourceContentType           string               `json:"source_content_type"`
	SourceSHA256                string               `json:"source_sha256"`
	NormTextSHA256              string               `json:"norm_text_sha256"`
	CanonicalTextVersion        string               `json:"canonical_text_version"`
	SourceTextStart             int                  `json:"source_text_start"`
	SourceTextEnd               int                  `json:"source_text_end"`
	ExtractorID                 string               `json:"extractor_id"`
	ExtractorVersion            string               `json:"extractor_version"`
	TextCheckStatus             string               `json:"text_check_status"`
	LegalReviewStatus           string               `json:"legal_review_status"`
	Reviewer                    *string              `json:"reviewer"`
	ApplicabilityConditions     []KnowledgeCondition `json:"applicability_conditions,omitempty"`
}

type LegalArticleVersionInput struct {
	ArticleNumber  int    `json:"article_number"`
	KnowledgeID    string `json:"knowledge_id"`
	DisplayVersion string `json:"display_version"`
}

// LegalSourceImportInput is an assertion-bearing request. Capture article
// bodies are checked by legalnorm against raw bytes; governed content is built
// from VerifiedArticle.FullText, never from the asserted text.
type LegalSourceImportInput struct {
	Capture        legalnorm.CaptureInput     `json:"capture"`
	Articles       []LegalArticleVersionInput `json:"articles"`
	IdempotencyKey string                     `json:"idempotency_key"`
}

type LegalSourceImportResult struct {
	CaptureSnapshotID string         `json:"capture_snapshot_id"`
	SourceID          string         `json:"source_id"`
	NormVersion       string         `json:"norm_version"`
	RawSHA256         string         `json:"raw_sha256"`
	ExtractedSHA256   string         `json:"extracted_full_text_sha256"`
	StatusSHA256      string         `json:"status_evidence_sha256"`
	Provenance        string         `json:"provenance"`
	Articles          []KnowledgeRef `json:"articles"`
}

type LegalSourceCapture struct {
	CaptureSnapshotID string                      `json:"capture_snapshot_id"`
	SourceID          string                      `json:"source_id"`
	NormVersion       string                      `json:"norm_version"`
	NormTitle         string                      `json:"norm_title"`
	DocumentType      string                      `json:"document_type"`
	Publisher         string                      `json:"publisher"`
	Issuer            string                      `json:"issuer"`
	OfficialURL       string                      `json:"official_url"`
	Provenance        string                      `json:"provenance"`
	PromulgatedOn     string                      `json:"promulgated_on"`
	EffectiveFrom     string                      `json:"effective_from"`
	EffectiveUntil    string                      `json:"effective_until,omitempty"`
	SourceCapturedAt  string                      `json:"source_captured_at"`
	ResponseStatus    int                         `json:"response_status"`
	MediaType         string                      `json:"media_type"`
	RawResponse       []byte                      `json:"raw_response,omitempty"`
	RawSHA256         string                      `json:"raw_sha256"`
	ExtractedFullText string                      `json:"extracted_full_text"`
	ExtractedSHA256   string                      `json:"extracted_full_text_sha256"`
	ExtractorID       string                      `json:"extractor_id"`
	ExtractorVersion  string                      `json:"extractor_version"`
	CanonicalVersion  string                      `json:"canonicalization_version"`
	StatusID          string                      `json:"status_observation_id"`
	Status            string                      `json:"observed_status"`
	StatusRegistryURL string                      `json:"status_registry_url"`
	StatusMethod      string                      `json:"status_request_method"`
	StatusRequestBody []byte                      `json:"status_request_body,omitempty"`
	StatusObservedAt  string                      `json:"status_observed_at"`
	StatusHTTPStatus  int                         `json:"status_response_status"`
	StatusMediaType   string                      `json:"status_media_type"`
	StatusResponse    []byte                      `json:"status_response,omitempty"`
	StatusSHA256      string                      `json:"status_response_sha256"`
	StatusMapping     json.RawMessage             `json:"status_mapping_evidence"`
	Articles          []legalnorm.VerifiedArticle `json:"articles"`
	ImportedArticles  []KnowledgeRef              `json:"imported_articles"`
	RequestDigest     string                      `json:"request_digest"`
	IdempotencyKey    string                      `json:"idempotency_key"`
	CreatedBy         string                      `json:"created_by"`
	CreatedAt         string                      `json:"created_at"`
}

type LegalSearch struct {
	Query         string
	SourceID      string
	ArticleNumber int
	BusinessAt    string
	Limit         int
	Cursor        string
}

type LegalKnowledgeHit struct {
	Ref           KnowledgeRef  `json:"ref"`
	Title         string        `json:"title"`
	Body          string        `json:"body"`
	Snippet       string        `json:"snippet,omitempty"`
	Summary       string        `json:"summary"`
	Status        string        `json:"governance_status"`
	Active        bool          `json:"active"`
	Legal         LegalMetadata `json:"legal_metadata"`
	Applicability string        `json:"applicability"`
	NextCursor    string        `json:"next_cursor,omitempty"`
}

type LegalKnowledgePage struct {
	Items      []LegalKnowledgeHit `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

func validateLegalMetadata(metadata *LegalMetadata, body string) error {
	if metadata == nil || metadata.SourceID == "" || metadata.NormTitle == "" || metadata.NormVersion == "" ||
		metadata.ArticleNumber <= 0 || metadata.ArticleLabel == "" || metadata.Issuer == "" || metadata.Publisher == "" ||
		metadata.OfficialURL == "" || metadata.OfficialStatusObservationID == "" || metadata.StatusRegistryURL == "" ||
		metadata.SourceSnapshotID == "" || metadata.SourceCapturedAt == "" || metadata.StatusObservedAt == "" ||
		!validKnowledgeHash(metadata.SourceSHA256) || !validKnowledgeHash(metadata.NormTextSHA256) ||
		!validKnowledgeHash(metadata.StatusEvidenceSHA256) || metadata.SourceTextStart < 0 || metadata.SourceTextEnd <= metadata.SourceTextStart ||
		metadata.SourceResponseStatus != 200 || metadata.StatusResponseStatus != 200 ||
		metadata.TextCheckStatus != "PASS" || metadata.LegalReviewStatus != "NOT_RUN" || metadata.Reviewer != nil ||
		metadata.SourceProvenance != "verified_official_capture" || metadata.CanonicalTextVersion != legalnorm.CanonicalTextVersion ||
		metadata.ExtractorID == "" || metadata.ExtractorVersion == "" || !validKnowledgeHash(sha256Bytes([]byte(body))) ||
		metadata.NormTextSHA256 != sha256Bytes([]byte(body)) {
		return fault.Invalid("法律条文缺少服务端核验的来源、正文或效力元数据")
	}
	switch metadata.DocumentType {
	case "law", "administrative_regulation", "judicial_interpretation", "normative_notice", "draft", "legislative_plan":
	default:
		return fault.Invalid("法律来源文件类型无效")
	}
	switch metadata.OfficialStatus {
	case legalnorm.StatusEffective, legalnorm.StatusRepealed, legalnorm.StatusNotYetEffective:
	default:
		return fault.Invalid("官方登记效力状态无效")
	}
	if _, err := time.Parse(time.RFC3339, metadata.SourceCapturedAt); err != nil {
		return fault.Invalid("法律来源抓取时间无效")
	}
	if _, err := time.Parse(time.RFC3339, metadata.StatusObservedAt); err != nil {
		return fault.Invalid("法律效力观察时间无效")
	}
	if _, err := time.Parse("2006-01-02", metadata.PromulgatedOn); err != nil {
		return fault.Invalid("法律公布日期无效")
	}
	if _, err := time.Parse("2006-01-02", metadata.EffectiveFrom); err != nil {
		return fault.Invalid("法律生效日期无效")
	}
	return nil
}

func legalArticleManifest(articles []legalnorm.VerifiedArticle) ([]byte, error) {
	canonical := make([]legalnorm.VerifiedArticle, len(articles))
	for i, article := range articles {
		canonical[i] = article
		canonical[i].Ref.FullText = article.FullText
		canonical[i].Ref.TextSHA256 = article.TextSHA256
	}
	return json.Marshal(canonical)
}

func legalRequestDigest(input LegalSourceImportInput) (string, error) {
	capture := input.Capture
	capture.SourceSnapshotID = ""
	capture.Articles = append([]legalnorm.ArticleRef(nil), input.Capture.Articles...)
	for i := range capture.Articles {
		capture.Articles[i].SourceSnapshotID = ""
	}
	canonical, err := json.Marshal(struct {
		Capture  legalnorm.CaptureInput     `json:"capture"`
		Articles []LegalArticleVersionInput `json:"articles"`
	}{capture, input.Articles})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func legalRequestVersions(articles []LegalArticleVersionInput, verified []legalnorm.VerifiedArticle) (map[int]LegalArticleVersionInput, error) {
	if len(articles) != len(verified) || len(articles) == 0 || len(articles) > 100 {
		return nil, fault.Invalid("法律条文版本清单与服务端提取结果数量不一致")
	}
	byNumber := make(map[int]LegalArticleVersionInput, len(articles))
	for _, article := range articles {
		if article.ArticleNumber <= 0 || !validResourceID(article.KnowledgeID) || !validResourceID(article.DisplayVersion) {
			return nil, fault.Invalid("法律条文治理编号无效")
		}
		if _, exists := byNumber[article.ArticleNumber]; exists {
			return nil, fault.Invalid("法律条文治理编号重复")
		}
		byNumber[article.ArticleNumber] = article
	}
	for _, article := range verified {
		if _, exists := byNumber[article.Ref.ArticleNumber]; !exists {
			return nil, fault.Invalid("法律条文治理清单缺少已提取条号")
		}
	}
	return byNumber, nil
}

func legalMetadataFor(verified legalnorm.VerifiedBaseline, article legalnorm.VerifiedArticle) *LegalMetadata {
	capture := verified.Capture
	return &LegalMetadata{
		SourceID: capture.SourceID, NormTitle: capture.Title, DocumentType: verified.DocumentType,
		NormVersion: capture.VersionID, ArticleNumber: article.Ref.ArticleNumber, ArticleLabel: article.Ref.ArticleLabel,
		Issuer: verified.Issuer, Publisher: verified.Publisher, OfficialURL: capture.OfficialURL,
		PromulgatedOn: capture.PromulgatedOn, EffectiveFrom: capture.EffectiveFrom, EffectiveUntil: capture.EffectiveUntil,
		OfficialStatusObservationID: capture.StatusEvidenceID, OfficialStatus: capture.CurrencyStatus,
		StatusRegistryURL: verified.StatusRegistryURL, StatusObservedAt: capture.StatusObservedAt,
		StatusEvidenceSHA256: capture.StatusEvidenceSHA256, StatusResponseStatus: verified.StatusResponseStatus,
		StatusContentType: verified.StatusContentType, SourceSnapshotID: capture.SourceSnapshotID,
		SourceProvenance: verified.Provenance, SourceCapturedAt: capture.SourceCapturedAt,
		SourceResponseStatus: verified.SourceResponseStatus, SourceContentType: capture.CapturedContentType,
		SourceSHA256: capture.RawSHA256, NormTextSHA256: article.TextSHA256,
		CanonicalTextVersion: capture.CanonicalTextVersion, SourceTextStart: article.Start, SourceTextEnd: article.End,
		ExtractorID: capture.ExtractorID, ExtractorVersion: capture.ExtractorVersion,
		TextCheckStatus: "PASS", LegalReviewStatus: "NOT_RUN", Reviewer: nil,
	}
}

func validateKnowledgeVersionFamily(ctx context.Context, tx pgx.Tx, tenant string, content GovernedKnowledgeContent) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM business_knowledge WHERE tenant_id=$1 AND id=$2 AND version=$3)
		OR EXISTS(SELECT 1 FROM governed_knowledge_versions WHERE tenant_id=$1 AND knowledge_id=$2 AND display_version=$3)`,
		tenant, content.KnowledgeID, content.Version).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return fault.Conflict("制度版本已经存在；旧制度不会自动升级为已治理内容")
	}
	var differentKind bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM governed_knowledge_versions WHERE tenant_id=$1 AND knowledge_id=$2 AND kind<>$3)`,
		tenant, content.KnowledgeID, content.Kind).Scan(&differentKind); err != nil {
		return err
	}
	if differentKind {
		return fault.Conflict("制度族的类型不可改变")
	}
	return nil
}

func validateLegalCaptureReference(ctx context.Context, tx pgx.Tx, tenant string, content GovernedKnowledgeContent) error {
	metadata := content.LegalMetadata
	if err := validateLegalMetadata(metadata, content.Body); err != nil {
		return fault.Conflict("已保存的法律条文元数据无效")
	}
	var sourceID, versionID, normTitle, documentType, publisher, issuer, officialURL, provenance string
	var promulgatedOn, effectiveFrom, effectiveUntil string
	var sourceCapturedAt, statusObservedAt time.Time
	var responseStatus, statusResponseStatus int
	var mediaType, statusMediaType, rawSHA, extracted, extractedSHA, extractorID, extractorVersion, canonicalVersion string
	var statusID, status, statusRegistryURL, statusMethod, statusSHA string
	var articleManifestSHA string
	var raw, statusRaw, statusRequestBody, articleRaw []byte
	err := tx.QueryRow(ctx, `SELECT source_id,norm_version,norm_title,document_type,publisher,issuer,official_url,provenance,
		source_captured_at,promulgated_on::text,effective_from::text,COALESCE(effective_until::text,''),response_status,media_type,raw_response_bytes,raw_response_sha256,extracted_full_text,extracted_full_text_sha256,
		extractor_id,extractor_version,canonicalization_version,status_observation_id,observed_status,status_registry_url,
		status_request_method,status_request_body,status_observed_at,status_response_status,status_media_type,status_response_bytes,status_response_sha256,article_manifest,article_manifest_sha256
		FROM legal_source_captures WHERE tenant_id=$1 AND capture_snapshot_id=$2`, tenant, metadata.SourceSnapshotID).Scan(
		&sourceID, &versionID, &normTitle, &documentType, &publisher, &issuer, &officialURL, &provenance,
		&sourceCapturedAt, &promulgatedOn, &effectiveFrom, &effectiveUntil, &responseStatus, &mediaType, &raw, &rawSHA, &extracted, &extractedSHA,
		&extractorID, &extractorVersion, &canonicalVersion, &statusID, &status, &statusRegistryURL,
		&statusMethod, &statusRequestBody, &statusObservedAt, &statusResponseStatus, &statusMediaType, &statusRaw, &statusSHA, &articleRaw, &articleManifestSHA)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.Conflict("法律条文引用的原始来源快照不存在")
	}
	if err != nil {
		return err
	}
	if sha256Bytes(raw) != rawSHA || sha256Bytes([]byte(extracted)) != extractedSHA || sha256Bytes(statusRaw) != statusSHA {
		return fault.Conflict("法律来源快照内容摘要不匹配")
	}
	if metadata.SourceID != sourceID || metadata.NormVersion != versionID || metadata.NormTitle != normTitle || metadata.DocumentType != documentType ||
		metadata.Publisher != publisher || metadata.Issuer != issuer || metadata.OfficialURL != officialURL || metadata.SourceProvenance != provenance ||
		metadata.PromulgatedOn != promulgatedOn || metadata.EffectiveFrom != effectiveFrom || metadata.EffectiveUntil != effectiveUntil ||
		!sameTimestamp(metadata.SourceCapturedAt, sourceCapturedAt) || metadata.SourceResponseStatus != responseStatus || metadata.SourceContentType != mediaType ||
		metadata.SourceSHA256 != rawSHA || metadata.OfficialStatusObservationID != statusID || metadata.OfficialStatus != status ||
		metadata.StatusRegistryURL != statusRegistryURL || !sameTimestamp(metadata.StatusObservedAt, statusObservedAt) ||
		metadata.StatusResponseStatus != statusResponseStatus || metadata.StatusContentType != statusMediaType || metadata.StatusEvidenceSHA256 != statusSHA ||
		metadata.ExtractorID != extractorID || metadata.ExtractorVersion != extractorVersion || metadata.CanonicalTextVersion != canonicalVersion {
		return fault.Conflict("法律条文元数据与不可变来源快照不一致")
	}
	var articles []legalnorm.VerifiedArticle
	if err := json.Unmarshal(articleRaw, &articles); err != nil || len(articles) == 0 {
		return fault.Conflict("法律来源快照的条文索引无效")
	}
	canonical, err := legalArticleManifest(articles)
	if err != nil || sha256Bytes(canonical) != articleManifestSHA {
		return fault.Conflict("法律来源快照的条文索引不是规范编码")
	}
	for _, article := range articles {
		if article.Ref.ArticleNumber != metadata.ArticleNumber {
			continue
		}
		runes := []rune(extracted)
		if article.Ref.SourceID != sourceID || article.Ref.SourceSnapshotID != metadata.SourceSnapshotID || article.Ref.VersionID != versionID ||
			article.FullText != content.Body || article.Ref.FullText != article.FullText || article.TextSHA256 != metadata.NormTextSHA256 ||
			article.Start != metadata.SourceTextStart || article.End != metadata.SourceTextEnd || article.Start < 0 || article.End > len(runes) ||
			string(runes[article.Start:article.End]) != article.FullText || article.Ref.PromulgatedOn != metadata.PromulgatedOn ||
			article.Ref.EffectiveFrom != metadata.EffectiveFrom || article.Ref.EffectiveUntil != metadata.EffectiveUntil ||
			article.Ref.ArticleLabel != metadata.ArticleLabel {
			return fault.Conflict("法律条文正文、定位或版本与原始来源提取结果不一致")
		}
		return nil
	}
	return fault.Conflict("法律来源快照没有引用的条文号")
}

// validateLegalAuthorityReferences proves each authored RulePack citation
// against the exact published governed article and its immutable capture.
func validateLegalAuthorityReferences(ctx context.Context, tx pgx.Tx, tenant string, pack *rulepack.Pack) error {
	if pack == nil {
		return fault.Invalid("规则包无效")
	}
	for _, rule := range pack.Manifest().Rules {
		for _, ref := range rule.LegalAuthorities {
			normative := false
			for _, source := range rule.AuthoritySources {
				if source.ID == ref.AuthoritySourceID && source.Kind == "normative" && source.Version == ref.NormVersion {
					normative = true
					break
				}
			}
			if !normative || ref.SourceID != ref.AuthoritySourceID {
				return fault.Conflict("法律引用没有匹配的规范性规则来源")
			}
			version, _, _, _, err := loadGovernedKnowledge(ctx, tx, tenant, ref.KnowledgeID, ref.VersionID)
			if err != nil {
				return fault.Conflict("规则引用的法律条文不存在或来源身份无效")
			}
			metadata := version.Content.LegalMetadata
			if !isPublishableLegalAuthority(version) ||
				version.Ref.ContentSHA256 != ref.ContentSHA256 || metadata.SourceID != ref.SourceID ||
				metadata.SourceSnapshotID != ref.SourceSnapshotID || metadata.NormTitle != ref.NormTitle || metadata.NormVersion != ref.NormVersion ||
				metadata.ArticleNumber != ref.ArticleNumber || metadata.DocumentType != ref.DocumentType || metadata.SourceSHA256 != ref.SourceSHA256 ||
				metadata.NormTextSHA256 != ref.ArticleTextSHA256 || metadata.EffectiveFrom != ref.EffectiveFrom || metadata.EffectiveUntil != ref.EffectiveUntil {
				return fault.Conflict("规则引用与已发布法律条文的精确版本不一致")
			}
			runes := []rune(version.Content.Body)
			if ref.QuoteSpan.Start < 0 || ref.QuoteSpan.End <= ref.QuoteSpan.Start || ref.QuoteSpan.End > len(runes) ||
				string(runes[ref.QuoteSpan.Start:ref.QuoteSpan.End]) != ref.QuoteSpan.Quote {
				return fault.Conflict("规则法律引用的原文引句定位不匹配")
			}
		}
	}
	return nil
}

func isPublishableLegalAuthority(version GovernedKnowledgeVersion) bool {
	metadata := version.Content.LegalMetadata
	return version.Content.Kind == "legal" && version.Status == "published" && metadata != nil &&
		version.ApprovedDigest != "" && version.ApprovedDigest == version.Ref.ContentSHA256 &&
		strings.TrimSpace(version.ApprovalActorID) != "" && strings.TrimSpace(version.ApprovalReason) != "" &&
		metadata.DocumentType != "draft" && metadata.DocumentType != "legislative_plan"
}

func sameTimestamp(raw string, stored time.Time) bool {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	return err == nil && parsed.Equal(stored)
}

func legalBusinessDate(raw string) (string, error) {
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return "", fault.Invalid("法律检索业务时间必须是带时区的 RFC3339 时间")
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return "", fmt.Errorf("load legal business timezone: %w", err)
	}
	return at.In(location).Format("2006-01-02"), nil
}

func legalEffectiveOnDate(metadata LegalMetadata, date string) bool {
	return metadata.EffectiveFrom <= date && (metadata.EffectiveUntil == "" || date < metadata.EffectiveUntil)
}

func legalImportResultFromStored(ctx context.Context, tx pgx.Tx, tenant, captureID string, refsRaw []byte) (LegalSourceImportResult, error) {
	var result LegalSourceImportResult
	var sourceID, normVersion, rawSHA, extractedSHA, statusSHA, provenance string
	if err := tx.QueryRow(ctx, `SELECT capture_snapshot_id,source_id,norm_version,raw_response_sha256,extracted_full_text_sha256,status_response_sha256,provenance
		FROM legal_source_captures WHERE tenant_id=$1 AND capture_snapshot_id=$2`, tenant, captureID).
		Scan(&result.CaptureSnapshotID, &sourceID, &normVersion, &rawSHA, &extractedSHA, &statusSHA, &provenance); err != nil {
		return LegalSourceImportResult{}, err
	}
	var refs []KnowledgeRef
	if err := json.Unmarshal(refsRaw, &refs); err != nil || len(refs) == 0 {
		return LegalSourceImportResult{}, fault.Conflict("法律来源幂等结果损坏")
	}
	result.SourceID, result.NormVersion, result.RawSHA256, result.ExtractedSHA256 = sourceID, normVersion, rawSHA, extractedSHA
	result.StatusSHA256, result.Provenance = statusSHA, provenance
	result.Articles = make([]KnowledgeRef, 0, len(refs))
	for _, ref := range refs {
		var storedHash string
		err := tx.QueryRow(ctx, `SELECT content_sha256 FROM governed_knowledge_versions WHERE tenant_id=$1 AND knowledge_id=$2 AND version_id=$3 AND kind='legal' AND legal_capture_snapshot_id=$4`,
			tenant, ref.KnowledgeID, ref.VersionID, captureID).Scan(&storedHash)
		if err != nil || storedHash != ref.ContentSHA256 {
			return LegalSourceImportResult{}, fault.Conflict("法律来源幂等结果与已保存条文不一致")
		}
		result.Articles = append(result.Articles, ref)
	}
	return result, nil
}

func loadLegalSourceCaptureTx(ctx context.Context, tx pgx.Tx, tenant, captureID string) (LegalSourceCapture, error) {
	var capture LegalSourceCapture
	var sourceAt, statusAt, createdAt time.Time
	var articleRaw, refsRaw, mappingRaw []byte
	var articleManifestSHA string
	err := tx.QueryRow(ctx, `SELECT capture_snapshot_id,source_id,norm_version,norm_title,document_type,publisher,issuer,official_url,provenance,
		source_captured_at,promulgated_on::text,effective_from::text,COALESCE(effective_until::text,''),response_status,media_type,raw_response_bytes,raw_response_sha256,extracted_full_text,extracted_full_text_sha256,
		extractor_id,extractor_version,canonicalization_version,status_observation_id,observed_status,status_registry_url,status_request_method,status_request_body,
		status_observed_at,status_response_status,status_media_type,status_response_bytes,status_response_sha256,status_mapping_evidence,article_manifest,article_manifest_sha256,
		imported_article_refs,request_digest,idempotency_key,created_by,created_at
		FROM legal_source_captures WHERE tenant_id=$1 AND capture_snapshot_id=$2`, tenant, captureID).Scan(
		&capture.CaptureSnapshotID, &capture.SourceID, &capture.NormVersion, &capture.NormTitle, &capture.DocumentType, &capture.Publisher, &capture.Issuer, &capture.OfficialURL, &capture.Provenance,
		&sourceAt, &capture.PromulgatedOn, &capture.EffectiveFrom, &capture.EffectiveUntil, &capture.ResponseStatus, &capture.MediaType, &capture.RawResponse, &capture.RawSHA256, &capture.ExtractedFullText, &capture.ExtractedSHA256,
		&capture.ExtractorID, &capture.ExtractorVersion, &capture.CanonicalVersion, &capture.StatusID, &capture.Status, &capture.StatusRegistryURL, &capture.StatusMethod, &capture.StatusRequestBody,
		&statusAt, &capture.StatusHTTPStatus, &capture.StatusMediaType, &capture.StatusResponse, &capture.StatusSHA256, &mappingRaw, &articleRaw, &articleManifestSHA,
		&refsRaw, &capture.RequestDigest, &capture.IdempotencyKey, &capture.CreatedBy, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return LegalSourceCapture{}, fault.NotFound("没有找到法律来源快照")
	}
	if err != nil {
		return LegalSourceCapture{}, err
	}
	capture.SourceCapturedAt = sourceAt.UTC().Format(time.RFC3339Nano)
	capture.StatusObservedAt = statusAt.UTC().Format(time.RFC3339Nano)
	capture.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	if sha256Bytes(capture.RawResponse) != capture.RawSHA256 || sha256Bytes([]byte(capture.ExtractedFullText)) != capture.ExtractedSHA256 ||
		sha256Bytes(capture.StatusResponse) != capture.StatusSHA256 || !validKnowledgeHash(capture.RequestDigest) {
		return LegalSourceCapture{}, fault.Conflict("法律来源快照的摘要校验失败")
	}
	if err := json.Unmarshal(articleRaw, &capture.Articles); err != nil || len(capture.Articles) == 0 {
		return LegalSourceCapture{}, fault.Conflict("法律来源快照的条文索引无效")
	}
	canonicalArticles, err := legalArticleManifest(capture.Articles)
	if err != nil || sha256Bytes(canonicalArticles) != articleManifestSHA {
		return LegalSourceCapture{}, fault.Conflict("法律来源快照的条文索引不是规范编码")
	}
	if err := json.Unmarshal(refsRaw, &capture.ImportedArticles); err != nil {
		return LegalSourceCapture{}, fault.Conflict("法律来源导入版本索引无效")
	}
	var mapping map[string]any
	if err := json.Unmarshal(mappingRaw, &mapping); err != nil || len(mapping) == 0 {
		return LegalSourceCapture{}, fault.Conflict("法律效力状态映射证据无效")
	}
	capture.StatusMapping = append(json.RawMessage(nil), mappingRaw...)
	fullText := []rune(capture.ExtractedFullText)
	for _, article := range capture.Articles {
		if article.Start < 0 || article.End <= article.Start || article.End > len(fullText) ||
			string(fullText[article.Start:article.End]) != article.FullText || article.Ref.FullText != article.FullText ||
			sha256Bytes([]byte(article.FullText)) != article.TextSHA256 {
			return LegalSourceCapture{}, fault.Conflict("法律来源快照中的条文正文或位置损坏")
		}
	}
	return capture, nil
}

func (s *Store) GetLegalSourceCapture(ctx context.Context, user accounts.User, captureID string) (LegalSourceCapture, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return LegalSourceCapture{}, err
	}
	if !validResourceID(captureID) {
		return LegalSourceCapture{}, fault.NotFound("没有找到法律来源快照")
	}
	var capture LegalSourceCapture
	err = s.readLegalAdmin(ctx, admin, func(tx pgx.Tx) error {
		var inner error
		capture, inner = loadLegalSourceCaptureTx(ctx, tx, accounts.Tenant(admin), captureID)
		return inner
	})
	return capture, err
}

func (s *Store) GetLegalSourceCaptureForCase(ctx context.Context, user accounts.User, caseID, captureID string) (LegalSourceCapture, error) {
	current, err := trustedUser(user)
	if err != nil {
		return LegalSourceCapture{}, err
	}
	if !validResourceID(caseID) || !validResourceID(captureID) {
		return LegalSourceCapture{}, fault.NotFound("没有找到法律来源快照")
	}
	var capture LegalSourceCapture
	err = s.readLegalUser(ctx, current, func(tx pgx.Tx) error {
		if _, err := loadKnowledgeCase(ctx, tx, current, caseID); err != nil {
			return err
		}
		var referenced bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM governed_knowledge_versions v JOIN governed_knowledge_state s USING (tenant_id,knowledge_id,version_id)
			WHERE v.tenant_id=$1 AND v.kind='legal' AND v.legal_capture_snapshot_id=$2 AND s.status IN ('published','withdrawn'))`, accounts.Tenant(current), captureID).Scan(&referenced); err != nil {
			return err
		}
		if !referenced {
			return fault.NotFound("没有找到法律来源快照")
		}
		var inner error
		capture, inner = loadLegalSourceCaptureTx(ctx, tx, accounts.Tenant(current), captureID)
		return inner
	})
	return capture, err
}

func (s *Store) readLegalAdmin(ctx context.Context, admin accounts.User, fn func(pgx.Tx) error) error {
	if err := s.legalReady(false); err != nil {
		return err
	}
	if ctx == nil {
		return fault.Invalid("法律知识读取需要请求上下文")
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return fault.Forbidden("当前服务不能读取法律知识")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(),0))`); err != nil {
		return err
	}
	if err := setLocalScope(ctx, tx, governanceScope(admin)); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if _, err := trustedAdmin(admin); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) readLegalUser(ctx context.Context, user accounts.User, fn func(pgx.Tx) error) error {
	if err := s.legalReady(false); err != nil {
		return err
	}
	if ctx == nil {
		return fault.Invalid("法律知识读取需要请求上下文")
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return fault.Forbidden("当前服务不能读取法律知识")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(user)); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if _, err := trustedUser(user); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SearchLegalKnowledgeAdmin(ctx context.Context, user accounts.User, filter LegalSearch) (LegalKnowledgePage, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return LegalKnowledgePage{}, err
	}
	var page LegalKnowledgePage
	err = s.readLegalAdmin(ctx, admin, func(tx pgx.Tx) error {
		var inner error
		page, inner = searchLegalKnowledgeTx(ctx, tx, accounts.Tenant(admin), filter, true)
		return inner
	})
	return page, err
}

func (s *Store) SearchLegalKnowledgeForCase(ctx context.Context, user accounts.User, caseID string, filter LegalSearch) (LegalKnowledgePage, error) {
	current, err := trustedUser(user)
	if err != nil {
		return LegalKnowledgePage{}, err
	}
	if !validResourceID(caseID) {
		return LegalKnowledgePage{}, fault.NotFound("没有找到对应记录")
	}
	var page LegalKnowledgePage
	err = s.readLegalUser(ctx, current, func(tx pgx.Tx) error {
		if _, err := loadKnowledgeCase(ctx, tx, current, caseID); err != nil {
			return err
		}
		var inner error
		page, inner = searchLegalKnowledgeTx(ctx, tx, accounts.Tenant(current), filter, false)
		return inner
	})
	return page, err
}

func (s *Store) GetLegalKnowledgeForCase(ctx context.Context, user accounts.User, caseID, knowledgeID, versionID string) (GovernedKnowledgeVersion, error) {
	current, err := trustedUser(user)
	if err != nil {
		return GovernedKnowledgeVersion{}, err
	}
	if !validResourceID(caseID) || !validResourceID(knowledgeID) || !validKnowledgeHash(versionID) {
		return GovernedKnowledgeVersion{}, fault.NotFound("没有找到法律条文版本")
	}
	var version GovernedKnowledgeVersion
	err = s.readLegalUser(ctx, current, func(tx pgx.Tx) error {
		if _, err := loadKnowledgeCase(ctx, tx, current, caseID); err != nil {
			return err
		}
		var inner error
		version, _, _, _, inner = loadGovernedKnowledge(ctx, tx, accounts.Tenant(current), knowledgeID, versionID)
		if inner != nil {
			return inner
		}
		if version.Content.Kind != "legal" || version.Status != "published" {
			return fault.NotFound("没有找到法律条文版本")
		}
		return nil
	})
	return version, err
}

func searchLegalKnowledgeTx(ctx context.Context, tx pgx.Tx, tenant string, filter LegalSearch, admin bool) (LegalKnowledgePage, error) {
	filter.Query = strings.TrimSpace(filter.Query)
	if len(filter.Query) > 200 || filter.Limit < 0 || filter.Limit > 50 || filter.ArticleNumber < 0 ||
		(filter.SourceID != "" && !validResourceID(filter.SourceID)) {
		return LegalKnowledgePage{}, fault.Invalid("法律检索条件无效")
	}
	limit := filter.Limit
	if limit == 0 {
		limit = 20
	}
	var businessDate string
	if filter.BusinessAt != "" {
		var err error
		businessDate, err = legalBusinessDate(filter.BusinessAt)
		if err != nil {
			return LegalKnowledgePage{}, err
		}
	}
	cursorID, cursorVersion, err := decodeLegalCursor(filter.Cursor)
	if err != nil {
		return LegalKnowledgePage{}, fault.Invalid("法律检索游标无效")
	}
	rows, err := tx.Query(ctx, `SELECT v.knowledge_id,v.version_id FROM governed_knowledge_versions v
		JOIN governed_knowledge_state s USING (tenant_id,knowledge_id,version_id)
		WHERE v.tenant_id=$1 AND v.kind='legal' AND ($2 OR s.status IN ('published','withdrawn'))
		AND ($3='' OR convert_from(v.content,'UTF8')::jsonb->'legal_metadata'->>'source_id'=$3)
		AND ($4=0 OR (convert_from(v.content,'UTF8')::jsonb->'legal_metadata'->>'article_number')::integer=$4)
		AND ($5='' OR strpos(lower(convert_from(v.content,'UTF8')::jsonb->>'title'),lower($5))>0 OR strpos(lower(convert_from(v.content,'UTF8')::jsonb->>'body'),lower($5))>0)
		AND (NULLIF($6,'') IS NULL OR ((convert_from(v.content,'UTF8')::jsonb->'legal_metadata'->>'effective_from')::date <= NULLIF($6,'')::date AND
			(NULLIF(convert_from(v.content,'UTF8')::jsonb->'legal_metadata'->>'effective_until','') IS NULL OR (convert_from(v.content,'UTF8')::jsonb->'legal_metadata'->>'effective_until')::date > NULLIF($6,'')::date)))
		AND ($7='' OR v.knowledge_id>$7 OR (v.knowledge_id=$7 AND v.version_id>$8))
		ORDER BY v.knowledge_id,v.version_id LIMIT $9`, tenant, admin, filter.SourceID, filter.ArticleNumber, filter.Query, businessDate, cursorID, cursorVersion, limit+1)
	if err != nil {
		return LegalKnowledgePage{}, err
	}
	type identity struct{ knowledgeID, versionID string }
	ids := make([]identity, 0, limit+1)
	for rows.Next() {
		var id identity
		if err := rows.Scan(&id.knowledgeID, &id.versionID); err != nil {
			rows.Close()
			return LegalKnowledgePage{}, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return LegalKnowledgePage{}, err
	}
	rows.Close()
	hasMore := len(ids) > limit
	if hasMore {
		ids = ids[:limit]
	}
	page := LegalKnowledgePage{Items: make([]LegalKnowledgeHit, 0, len(ids))}
	for _, id := range ids {
		version, _, _, _, err := loadGovernedKnowledge(ctx, tx, tenant, id.knowledgeID, id.versionID)
		if err != nil {
			return LegalKnowledgePage{}, err
		}
		if version.Content.Kind != "legal" || version.Content.LegalMetadata == nil {
			return LegalKnowledgePage{}, fault.Conflict("法律检索结果类别或元数据损坏")
		}
		hit := LegalKnowledgeHit{
			Ref: version.Ref, Title: version.Content.Title, Summary: version.Content.Summary,
			Status: version.Status, Active: version.Active, Legal: *version.Content.LegalMetadata,
			Applicability: "unknown",
		}
		if admin {
			runes := []rune(version.Content.Body)
			if len(runes) > 240 {
				runes = runes[:240]
			}
			hit.Snippet = string(runes)
		} else {
			hit.Body = version.Content.Body
		}
		page.Items = append(page.Items, hit)
	}
	if hasMore && len(page.Items) > 0 {
		last := ids[len(ids)-1]
		page.NextCursor = encodeLegalCursor(last.knowledgeID, last.versionID)
	}
	return page, nil
}

func encodeLegalCursor(knowledgeID, versionID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(knowledgeID + "\n" + versionID))
}

func decodeLegalCursor(cursor string) (string, string, error) {
	if cursor == "" {
		return "", "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", err
	}
	parts := strings.Split(string(raw), "\n")
	if len(parts) != 2 || !validResourceID(parts[0]) || !validKnowledgeHash(parts[1]) {
		return "", "", fmt.Errorf("invalid legal search cursor")
	}
	return parts[0], parts[1], nil
}

func (s *Store) ImportLegalSource(ctx context.Context, user accounts.User, input LegalSourceImportInput) (LegalSourceImportResult, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return LegalSourceImportResult{}, err
	}
	if ctx == nil {
		return LegalSourceImportResult{}, fault.Invalid("法律来源导入需要请求上下文")
	}
	if err := s.legalReady(true); err != nil {
		return LegalSourceImportResult{}, err
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return LegalSourceImportResult{}, fault.Forbidden("当前服务不能导入法律来源")
	}
	if !validKnowledgeText(input.IdempotencyKey, 128) || input.IdempotencyKey != strings.TrimSpace(input.IdempotencyKey) {
		return LegalSourceImportResult{}, fault.Invalid("需要有效的请求标识（幂等键）")
	}
	requestDigest, err := legalRequestDigest(input)
	if err != nil {
		return LegalSourceImportResult{}, err
	}
	if replay, found, err := s.legalImportReplay(ctx, admin, input.IdempotencyKey, requestDigest); err != nil {
		return LegalSourceImportResult{}, err
	} else if found {
		return replay, nil
	}
	if len(input.Articles) != len(input.Capture.Articles) || len(input.Articles) == 0 || len(input.Articles) > 100 {
		return LegalSourceImportResult{}, fault.Invalid("法律条文治理清单与正文断言数量不一致")
	}
	// Snapshot identities are generated server-side. Normalize the supplied
	// identity fields before invoking the source verifier; article text, hashes,
	// labels, dates, and raw bytes remain assertions and are checked there.
	captureInput := input.Capture
	captureInput.SourceSnapshotID = "legal-capture-pending"
	captureInput.Articles = append([]legalnorm.ArticleRef(nil), input.Capture.Articles...)
	for i := range captureInput.Articles {
		if input.Capture.SourceSnapshotID != "" && captureInput.Articles[i].SourceSnapshotID != "" &&
			captureInput.Articles[i].SourceSnapshotID != input.Capture.SourceSnapshotID {
			return LegalSourceImportResult{}, fault.Invalid("法条引用快照身份彼此不一致")
		}
		captureInput.Articles[i].SourceSnapshotID = captureInput.SourceSnapshotID
	}
	verified, err := legalnorm.VerifyBaseline(captureInput)
	if err != nil {
		return LegalSourceImportResult{}, fault.Invalid("法律原文或状态证据未通过官方基线核验")
	}
	articleVersions, err := legalRequestVersions(input.Articles, verified.Capture.Articles)
	if err != nil {
		return LegalSourceImportResult{}, err
	}
	return s.legalAdminWrite(ctx, admin, func(tx pgx.Tx) (LegalSourceImportResult, error) {
		var oldDigest string
		var oldSnapshotID string
		var oldRefs []byte
		err := tx.QueryRow(ctx, `SELECT request_digest,capture_snapshot_id,imported_article_refs FROM legal_source_captures
			WHERE tenant_id=$1 AND created_by=$2 AND idempotency_key=$3`, accounts.Tenant(admin), admin.ID, input.IdempotencyKey).
			Scan(&oldDigest, &oldSnapshotID, &oldRefs)
		if err == nil {
			if oldDigest != requestDigest {
				return LegalSourceImportResult{}, fault.Conflict("同一幂等键不能用于不同法律来源内容")
			}
			return legalImportResultFromStored(ctx, tx, accounts.Tenant(admin), oldSnapshotID, oldRefs)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return LegalSourceImportResult{}, err
		}
		captureID, err := newID("lsc_")
		if err != nil {
			return LegalSourceImportResult{}, err
		}
		verified.Capture.SourceSnapshotID = captureID
		for i := range verified.Capture.Articles {
			verified.Capture.Articles[i].Ref.SourceSnapshotID = captureID
		}
		articleManifest, err := legalArticleManifest(verified.Capture.Articles)
		if err != nil {
			return LegalSourceImportResult{}, err
		}
		refs := make([]KnowledgeRef, 0, len(verified.Capture.Articles))
		versions := make([]GovernedKnowledgeVersion, 0, len(verified.Capture.Articles))
		seenVersions := make(map[string]bool)
		for _, article := range verified.Capture.Articles {
			versionInput := articleVersions[article.Ref.ArticleNumber]
			identityKey := versionInput.KnowledgeID + "\x00" + versionInput.DisplayVersion
			if seenVersions[identityKey] {
				return LegalSourceImportResult{}, fault.Invalid("法律条文版本编号重复")
			}
			seenVersions[identityKey] = true
			content := GovernedKnowledgeContent{
				Encoding: knowledgeContentEncoding, KnowledgeID: versionInput.KnowledgeID, Version: versionInput.DisplayVersion,
				Kind: "legal", Title: verified.Capture.Title + " " + article.Ref.ArticleLabel,
				Body: article.FullText, Summary: supplementalLegalSummary(verified.Capture.SourceID, verified.Capture.VersionID, article.Ref.ArticleNumber, article.Ref.ArticleLabel), SourceType: "reference",
				SourceReference: verified.Capture.OfficialURL, LegalMetadata: legalMetadataFor(verified, article),
			}
			if err := validateKnowledgeContent(content); err != nil {
				return LegalSourceImportResult{}, err
			}
			if err := validateKnowledgeVersionFamily(ctx, tx, accounts.Tenant(admin), content); err != nil {
				return LegalSourceImportResult{}, err
			}
			raw, err := json.Marshal(content)
			if err != nil {
				return LegalSourceImportResult{}, err
			}
			versionID := knowledgeVersionID(accounts.Tenant(admin), content.KnowledgeID, content.Version)
			ref := KnowledgeRef{KnowledgeID: content.KnowledgeID, VersionID: versionID, ContentSHA256: sha256Bytes(raw)}
			refs = append(refs, ref)
			versions = append(versions, GovernedKnowledgeVersion{Ref: ref, Content: content, Status: "draft", Revision: 1, CreatedBy: admin.ID})
		}
		refsJSON, err := json.Marshal(refs)
		if err != nil {
			return LegalSourceImportResult{}, err
		}
		mappingEvidence, err := json.Marshal(map[string]string{
			"status":      verified.Capture.CurrencyStatus,
			"evidence_id": verified.Capture.StatusEvidenceID,
			"mapping":     "source-specific legalnorm adapter verified the exact registry title, version dates, and status code",
		})
		if err != nil {
			return LegalSourceImportResult{}, err
		}
		capture := verified.Capture
		statusRequestBody := verified.StatusRequestBody
		if statusRequestBody == nil {
			statusRequestBody = []byte{}
		}
		articleManifestSHA := sha256Bytes(articleManifest)
		_, err = tx.Exec(ctx, `INSERT INTO legal_source_captures
			(tenant_id,capture_snapshot_id,source_id,norm_version,norm_title,document_type,publisher,issuer,official_url,provenance,
			source_captured_at,promulgated_on,effective_from,effective_until,response_status,media_type,raw_response_bytes,raw_response_sha256,
			extracted_full_text,extracted_full_text_sha256,extractor_id,extractor_version,canonicalization_version,
			status_observation_id,observed_status,status_registry_url,status_request_method,status_request_body,status_observed_at,
			status_response_status,status_media_type,status_response_bytes,status_response_sha256,status_mapping_evidence,article_manifest,article_manifest_sha256,
			request_digest,idempotency_key,imported_article_refs,created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::date,$13::date,NULLIF($14,'')::date,$15,$16,$17,$18,
			$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,$40)`,
			accounts.Tenant(admin), captureID, capture.SourceID, capture.VersionID, capture.Title, verified.DocumentType, verified.Publisher, verified.Issuer, capture.OfficialURL, verified.Provenance,
			capture.SourceCapturedAt, capture.PromulgatedOn, capture.EffectiveFrom, capture.EffectiveUntil, verified.SourceResponseStatus, capture.CapturedContentType, captureInput.RawBytes, capture.RawSHA256,
			capture.FullText, capture.FullTextSHA256, capture.ExtractorID, capture.ExtractorVersion, capture.CanonicalTextVersion,
			capture.StatusEvidenceID, capture.CurrencyStatus, verified.StatusRegistryURL, verified.StatusRequestMethod, statusRequestBody, capture.StatusObservedAt,
			verified.StatusResponseStatus, verified.StatusContentType, captureInput.StatusEvidenceRaw, capture.StatusEvidenceSHA256, mappingEvidence, articleManifest, articleManifestSHA,
			requestDigest, input.IdempotencyKey, refsJSON, admin.ID)
		if err != nil {
			return LegalSourceImportResult{}, err
		}
		for i := range versions {
			version := &versions[i]
			content := version.Content
			if err := validateLegalCaptureReference(ctx, tx, accounts.Tenant(admin), content); err != nil {
				return LegalSourceImportResult{}, err
			}
			raw, err := json.Marshal(content)
			if err != nil {
				return LegalSourceImportResult{}, err
			}
			scopeKey, err := knowledgeScopeKey(content.Scope)
			if err != nil {
				return LegalSourceImportResult{}, err
			}
			var created time.Time
			if err := tx.QueryRow(ctx, `INSERT INTO governed_knowledge_versions
				(tenant_id,knowledge_id,version_id,display_version,scope_key,kind,scope_org_id,scope_case_id,source_org_id,source_case_id,
				candidate_id,candidate_version_id,candidate_content_sha256,content_sha256,content,created_by,created_at,legal_capture_snapshot_id)
				VALUES ($1,$2,$3,$4,$5,'legal',NULL,NULL,NULL,NULL,NULL,NULL,NULL,$6,$7,$8,clock_timestamp(),$9) RETURNING created_at`,
				accounts.Tenant(admin), content.KnowledgeID, version.Ref.VersionID, content.Version, scopeKey,
				version.Ref.ContentSHA256, raw, admin.ID, captureID).Scan(&created); err != nil {
				return LegalSourceImportResult{}, err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO governed_knowledge_state (tenant_id,knowledge_id,version_id,status,revision) VALUES ($1,$2,$3,'draft',1)`, accounts.Tenant(admin), content.KnowledgeID, version.Ref.VersionID); err != nil {
				return LegalSourceImportResult{}, err
			}
			if err := insertKnowledgeHistory(ctx, tx, accounts.Tenant(admin), GovernedKnowledgeHistoryEntry{
				Ref: version.Ref, Action: "create", Revision: 1, ActorID: admin.ID, Reason: "导入已核验的官方法律来源",
			}); err != nil {
				return LegalSourceImportResult{}, err
			}
			version.CreatedAt = created.UTC().Format(time.RFC3339Nano)
		}
		return LegalSourceImportResult{
			CaptureSnapshotID: captureID, SourceID: capture.SourceID, NormVersion: capture.VersionID,
			RawSHA256: capture.RawSHA256, ExtractedSHA256: capture.FullTextSHA256,
			StatusSHA256: capture.StatusEvidenceSHA256, Provenance: verified.Provenance, Articles: refs,
		}, nil
	})
}

func (s *Store) legalImportReplay(ctx context.Context, admin accounts.User, key, requestDigest string) (LegalSourceImportResult, bool, error) {
	var result LegalSourceImportResult
	found := false
	err := s.readLegalAdmin(ctx, admin, func(tx pgx.Tx) error {
		var oldDigest, oldSnapshotID string
		var oldRefs []byte
		err := tx.QueryRow(ctx, `SELECT request_digest,capture_snapshot_id,imported_article_refs FROM legal_source_captures
			WHERE tenant_id=$1 AND created_by=$2 AND idempotency_key=$3`, accounts.Tenant(admin), admin.ID, key).
			Scan(&oldDigest, &oldSnapshotID, &oldRefs)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if oldDigest != requestDigest {
			return fault.Conflict("同一幂等键不能用于不同法律来源内容")
		}
		result, err = legalImportResultFromStored(ctx, tx, accounts.Tenant(admin), oldSnapshotID, oldRefs)
		if err == nil {
			found = true
		}
		return err
	})
	return result, found, err
}

func (s *Store) legalAdminWrite(ctx context.Context, admin accounts.User, fn func(pgx.Tx) (LegalSourceImportResult, error)) (LegalSourceImportResult, error) {
	if err := s.legalReady(true); err != nil {
		return LegalSourceImportResult{}, err
	}
	if ctx == nil {
		return LegalSourceImportResult{}, fault.Invalid("法律来源操作需要请求上下文")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return LegalSourceImportResult{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(),0))`); err != nil {
		return LegalSourceImportResult{}, err
	}
	if err := setLocalScope(ctx, tx, governanceScope(admin)); err != nil {
		return LegalSourceImportResult{}, err
	}
	result, err := fn(tx)
	if err != nil {
		return LegalSourceImportResult{}, err
	}
	if _, err := trustedAdmin(admin); err != nil {
		return LegalSourceImportResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LegalSourceImportResult{}, err
	}
	return result, nil
}

func (s *Store) legalReady(write bool) error {
	if s == nil || s.pool == nil {
		return fault.New(503, "legal_knowledge_unavailable", "法律知识需要 PostgreSQL 持久化")
	}
	if write && s.ReadOnly() {
		return fault.New(503, "read_only", "只读模式下不能修改法律知识")
	}
	return nil
}
