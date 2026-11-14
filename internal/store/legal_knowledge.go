// File metadata (manifest mirror):
// Purpose: LegalMetadata binds one governed article body to an immutable captured official source and point-in-time registry observation. Legal review is deliberately fixed at NOT_RUN for… Related `store` operations include validateLegalMetadata, legalArticleManifest, legalRequestDigest, legalRequestVersions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	knowledgeApp "contractaudit/internal/knowledge/app"
	knowledgeModel "contractaudit/internal/knowledge/model"
	knowledgePostgres "contractaudit/internal/knowledge/postgres"
	legalApp "contractaudit/internal/legal/app"
	legalModel "contractaudit/internal/legal/model"
	legalPostgres "contractaudit/internal/legal/postgres"
	"contractaudit/internal/legalnorm"
	platformpostgres "contractaudit/internal/platform/postgres"
	"contractaudit/internal/rulepack"

	"github.com/jackc/pgx/v5"
)

// LegalMetadata is a Knowledge-owned governed article value. Legal capture
// records and their persistence remain owned by Legal.
type LegalMetadata = knowledgeModel.LegalMetadata

type LegalArticleVersionInput = legalModel.LegalArticleVersionInput
type LegalSourceImportInput = legalModel.LegalSourceImportInput
type LegalSourceImportResult = legalModel.LegalSourceImportResult

type LegalSourceCapture = legalModel.LegalSourceCapture

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
	return knowledgeModel.ValidateLegalMetadata(metadata, body)
}

func legalArticleManifest(articles []legalnorm.VerifiedArticle) ([]byte, error) {
	return legalModel.ArticleManifest(articles)
}

// knowledgeVersionSlotOccupied preserves the legacy and governed namespaces
// as one exact, tenant-scoped fact query. Knowledge decides the conflict; this
// bridge does not infer ownership of legacy business_knowledge rows.
func knowledgeVersionSlotOccupied(ctx context.Context, tx pgx.Tx, tenant, knowledgeID, displayVersion string) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM business_knowledge WHERE tenant_id=$1 AND id=$2 AND version=$3)
		OR EXISTS(SELECT 1 FROM governed_knowledge_versions WHERE tenant_id=$1 AND knowledge_id=$2 AND display_version=$3)`,
		tenant, knowledgeID, displayVersion).Scan(&exists)
	return exists, err
}

type legalCaptureReference struct {
	id, sourceID, versionID, normTitle, documentType, publisher, issuer, officialURL, provenance                 string
	promulgatedOn, effectiveFrom, effectiveUntil                                                                 string
	sourceCapturedAt, statusObservedAt                                                                           time.Time
	responseStatus, statusResponseStatus                                                                         int
	mediaType, statusMediaType, rawSHA, extracted, extractedSHA, extractorID, extractorVersion, canonicalVersion string
	statusID, status, statusRegistryURL, statusMethod, statusSHA                                                 string
	articleManifestSHA                                                                                           string
	raw, statusRaw, statusRequestBody, articleRaw                                                                []byte
	digestsChecked, digestsValid                                                                                 bool
	articlesChecked                                                                                              bool
	articlesErr                                                                                                  error
	articles                                                                                                     []legalnorm.VerifiedArticle
	extractedRunes                                                                                               []rune
}

func mapLegalCaptureReference(c legalModel.ReferenceCapture) legalCaptureReference {
	return legalCaptureReference{id: c.ID, sourceID: c.SourceID, versionID: c.VersionID, normTitle: c.NormTitle, documentType: c.DocumentType, publisher: c.Publisher, issuer: c.Issuer, officialURL: c.OfficialURL, provenance: c.Provenance, promulgatedOn: c.PromulgatedOn, effectiveFrom: c.EffectiveFrom, effectiveUntil: c.EffectiveUntil, sourceCapturedAt: c.SourceCapturedAt, statusObservedAt: c.StatusObservedAt, responseStatus: c.ResponseStatus, statusResponseStatus: c.StatusResponseStatus, mediaType: c.MediaType, statusMediaType: c.StatusMediaType, rawSHA: c.RawSHA, extracted: c.Extracted, extractedSHA: c.ExtractedSHA, extractorID: c.ExtractorID, extractorVersion: c.ExtractorVersion, canonicalVersion: c.CanonicalVersion, statusID: c.StatusID, status: c.Status, statusRegistryURL: c.StatusRegistryURL, statusMethod: c.StatusMethod, statusSHA: c.StatusSHA, articleManifestSHA: c.ArticleManifestSHA, raw: c.Raw, statusRaw: c.StatusRaw, statusRequestBody: c.StatusRequestBody, articleRaw: c.ArticleRaw}
}

func validateLegalCaptureReference(ctx context.Context, tx pgx.Tx, tenant string, content GovernedKnowledgeContent) error {
	if err := validateLegalMetadata(content.LegalMetadata, content.Body); err != nil {
		return fault.Conflict("已保存的法律条文元数据无效")
	}
	raw, found, err := legalPostgres.NewCaptures(platformpostgres.Bind(tx)).ReadReference(ctx, tenant, content.LegalMetadata.SourceSnapshotID)
	if err != nil {
		return err
	}
	if !found {
		return fault.Conflict("法律条文引用的原始来源快照不存在")
	}
	capture := mapLegalCaptureReference(raw)
	return verifyLegalCaptureReference(content, &capture)
}

func verifyLegalCaptureReference(content GovernedKnowledgeContent, capture *legalCaptureReference) error {
	metadata := content.LegalMetadata
	sourceID, versionID, normTitle, documentType := capture.sourceID, capture.versionID, capture.normTitle, capture.documentType
	publisher, issuer, officialURL, provenance := capture.publisher, capture.issuer, capture.officialURL, capture.provenance
	promulgatedOn, effectiveFrom, effectiveUntil := capture.promulgatedOn, capture.effectiveFrom, capture.effectiveUntil
	sourceCapturedAt, statusObservedAt := capture.sourceCapturedAt, capture.statusObservedAt
	responseStatus, statusResponseStatus := capture.responseStatus, capture.statusResponseStatus
	mediaType, statusMediaType, rawSHA, extracted, extractedSHA := capture.mediaType, capture.statusMediaType, capture.rawSHA, capture.extracted, capture.extractedSHA
	extractorID, extractorVersion, canonicalVersion := capture.extractorID, capture.extractorVersion, capture.canonicalVersion
	statusID, status, statusRegistryURL, statusSHA := capture.statusID, capture.status, capture.statusRegistryURL, capture.statusSHA
	articleManifestSHA := capture.articleManifestSHA
	if !capture.digestsChecked {
		capture.digestsValid = sha256Bytes(capture.raw) == rawSHA && sha256Bytes([]byte(extracted)) == extractedSHA && sha256Bytes(capture.statusRaw) == statusSHA
		capture.digestsChecked = true
	}
	if !capture.digestsValid {
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
	if !capture.articlesChecked {
		capture.articlesChecked = true
		if err := json.Unmarshal(capture.articleRaw, &capture.articles); err != nil || len(capture.articles) == 0 {
			capture.articlesErr = fault.Conflict("法律来源快照的条文索引无效")
		} else if canonical, err := legalArticleManifest(capture.articles); err != nil || sha256Bytes(canonical) != articleManifestSHA {
			capture.articlesErr = fault.Conflict("法律来源快照的条文索引不是规范编码")
		}
	}
	if capture.articlesErr != nil {
		return capture.articlesErr
	}
	for _, article := range capture.articles {
		if article.Ref.ArticleNumber != metadata.ArticleNumber {
			continue
		}
		if capture.extractedRunes == nil {
			capture.extractedRunes = []rune(extracted)
		}
		runes := capture.extractedRunes
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
	return legalApp.IsPublishableLegalAuthority(version)
}

func sameTimestamp(raw string, stored time.Time) bool {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	return err == nil && parsed.Equal(stored)
}

func legalBusinessDate(raw string) (string, error) {
	return legalApp.BusinessDate(raw)
}

func legalEffectiveOnDate(metadata LegalMetadata, date string) bool {
	return metadata.EffectiveFrom <= date && (metadata.EffectiveUntil == "" || date < metadata.EffectiveUntil)
}

func loadLegalSourceCaptureTx(ctx context.Context, tx pgx.Tx, tenant, captureID string) (LegalSourceCapture, error) {
	return legalApp.LoadCapture(ctx, legalPostgres.NewCaptures(platformpostgres.Bind(tx)), tenant, captureID)
}

func (s *Store) GetLegalSourceCapture(ctx context.Context, user accounts.User, captureID string) (LegalSourceCapture, error) {
	ctx = commandContext(ctx, user)
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
	ctx = commandContext(ctx, user)
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
		referenced, err := knowledgeApp.LegalCaptureReferenced(ctx, knowledgePostgres.NewGovernance(platformpostgres.Bind(tx)), accounts.Tenant(current), captureID)
		if err != nil {
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
	ctx = commandContext(ctx, admin)
	if err := s.legalReady(false); err != nil {
		return err
	}
	if ctx == nil {
		return fault.Invalid("法律知识读取需要请求上下文")
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return fault.Forbidden("当前服务不能读取法律知识")
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(admin)); err != nil {
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
	ctx = commandContext(ctx, user)
	if err := s.legalReady(false); err != nil {
		return err
	}
	if ctx == nil {
		return fault.Invalid("法律知识读取需要请求上下文")
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return fault.Forbidden("当前服务不能读取法律知识")
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
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
	ctx = commandContext(ctx, user)
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
	ctx = commandContext(ctx, user)
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
	ctx = commandContext(ctx, user)
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
	if len(ids) == 0 {
		return page, nil
	}
	knowledgeIDs, versionIDs := make([]string, 0, len(ids)), make([]string, 0, len(ids))
	for _, id := range ids {
		knowledgeIDs = append(knowledgeIDs, id.knowledgeID)
		versionIDs = append(versionIDs, id.versionID)
	}
	versions, captures, err := queryGovernedKnowledgeRows(ctx, tx, tenant,
		`AND (v.knowledge_id,v.version_id) IN (SELECT * FROM unnest($2::text[],$3::text[]))`, knowledgeIDs, versionIDs)
	if err != nil {
		return LegalKnowledgePage{}, err
	}
	byID := make(map[identity]governedKnowledgeRow, len(versions))
	for _, version := range versions {
		byID[identity{version.version.Ref.KnowledgeID, version.version.Ref.VersionID}] = version
	}
	for _, id := range ids {
		stored, ok := byID[id]
		if !ok {
			return LegalKnowledgePage{}, fault.NotFound("没有找到对应制度版本")
		}
		version, _, _, _, err := validateGovernedKnowledgeRow(ctx, tx, tenant, stored, captures)
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
	ctx = commandContext(ctx, user)
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
	requestDigest, err := legalModel.RequestDigest(input)
	if err != nil {
		return LegalSourceImportResult{}, err
	}
	var replay LegalSourceImportResult
	var found bool
	err = s.readLegalAdmin(ctx, admin, func(tx pgx.Tx) error {
		var inner error
		replay, found, inner = legalApp.ReplayImport(ctx, legalPostgres.NewCaptures(platformpostgres.Bind(tx)), legalImportKnowledge{tx: tx, actor: admin}, accounts.Tenant(admin), admin.ID, input.IdempotencyKey, requestDigest)
		return inner
	})
	if err != nil {
		return LegalSourceImportResult{}, err
	}
	if found {
		return replay, nil
	}
	prepared, err := legalApp.PrepareImport(input)
	if err != nil {
		return LegalSourceImportResult{}, err
	}
	return s.legalAdminWrite(ctx, admin, func(tx pgx.Tx) (LegalSourceImportResult, error) {
		return legalApp.CommitImport(ctx, legalPostgres.NewCaptures(platformpostgres.Bind(tx)), legalImportKnowledge{tx: tx, actor: admin}, legalCaptureIDs{}, accounts.Tenant(admin), admin.ID, input.IdempotencyKey, requestDigest, prepared)
	})
}

func (s *Store) legalAdminWrite(ctx context.Context, admin accounts.User, fn func(pgx.Tx) (LegalSourceImportResult, error)) (LegalSourceImportResult, error) {
	ctx = commandContext(ctx, admin)
	if err := s.legalReady(true); err != nil {
		return LegalSourceImportResult{}, err
	}
	if ctx == nil {
		return LegalSourceImportResult{}, fault.Invalid("法律来源操作需要请求上下文")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
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
