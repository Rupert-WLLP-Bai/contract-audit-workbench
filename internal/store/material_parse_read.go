package store

import (
	"context"
	"sort"
	"strings"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

type MaterialParseSummary struct {
	ParsedDocumentID string `json:"parsed_document_id"`
	Status           string `json:"status"`
	ParserID         string `json:"parser_id"`
	ParserVersion    string `json:"parser_version"`
	CreatedAt        string `json:"created_at"`
	OriginalDigest   string `json:"original_digest"`
	NormalizedDigest string `json:"normalized_digest"`
}

type MaterialParseListResult struct {
	MaterialID      string                 `json:"material_id"`
	MaterialVersion int                    `json:"material_version"`
	DocumentID      string                 `json:"document_id"`
	Items           []MaterialParseSummary `json:"items"`
}

type MaterialParsedChunk struct {
	MaterialID       string `json:"material_id"`
	MaterialVersion  int    `json:"material_version"`
	DocumentID       string `json:"document_id"`
	ParsedDocumentID string `json:"parsed_document_id"`
	Filename         string `json:"filename"`
	OriginalDigest   string `json:"original_digest"`
	ParseDigest      string `json:"parse_digest"`
	TotalRunes       int    `json:"total_runes"`
	Offset           int    `json:"offset"`
	Text             string `json:"text"`
	NextOffset       *int   `json:"next_offset"`
}

func pinnedMaterialVersion(st state, user accounts.User, caseID, materialID string, version int) (MaterialVersion, Document, error) {
	var materialVersion *MaterialVersion
	for i := range st.MaterialVersions {
		item := &st.MaterialVersions[i]
		if item.TenantID == user.TenantID && item.CaseID == caseID && item.MaterialID == materialID && item.Version == version {
			materialVersion = item
			break
		}
	}
	if materialVersion == nil {
		return MaterialVersion{}, Document{}, fault.NotFound("没有找到对应材料版本")
	}
	for _, document := range st.Documents {
		if document.TenantID == user.TenantID && document.CaseID == caseID && document.ID == materialVersion.DocumentID {
			return *materialVersion, document, nil
		}
	}
	return MaterialVersion{}, Document{}, fault.NotFound("没有找到对应材料版本")
}

func authorizePinnedMaterialRead(ctx context.Context, s *Store, user accounts.User, caseID string) (accounts.User, state, error) {
	if err := ctx.Err(); err != nil {
		return accounts.User{}, state{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return accounts.User{}, state{}, err
	}
	st, err := s.snapshotScoped(ctx, userScope(current))
	if err != nil {
		return accounts.User{}, state{}, err
	}
	caseItem, err := s.visibleCase(&st, current, strings.TrimSpace(caseID))
	if err != nil {
		return accounts.User{}, state{}, err
	}
	if err := requireCaseAction(caseItem, current, access.Read); err != nil {
		return accounts.User{}, state{}, err
	}
	return current, st, nil
}

func (s *Store) ListMaterialVersionParses(ctx context.Context, user accounts.User, caseID, materialID string, version int) (MaterialParseListResult, error) {
	current, st, err := authorizePinnedMaterialRead(ctx, s, user, caseID)
	if err != nil {
		return MaterialParseListResult{}, err
	}
	materialVersion, document, err := pinnedMaterialVersion(st, current, caseID, strings.TrimSpace(materialID), version)
	if err != nil {
		return MaterialParseListResult{}, err
	}
	parses := make([]ParsedDocument, 0)
	for _, parsed := range st.ParsedDocuments {
		if parsed.DocumentID == document.ID && parsed.OriginalDigest == document.Digest {
			parses = append(parses, parsed)
		}
	}
	sort.Slice(parses, func(i, j int) bool { return parses[i].SelectionOrder < parses[j].SelectionOrder })
	result := MaterialParseListResult{MaterialID: materialVersion.MaterialID, MaterialVersion: materialVersion.Version, DocumentID: document.ID, Items: make([]MaterialParseSummary, 0, len(parses))}
	for _, parsed := range parses {
		if err := verifyParsed(parsed, document); err != nil {
			return MaterialParseListResult{}, fault.New(500, "integrity_error", "材料解析记录校验失败")
		}
		result.Items = append(result.Items, MaterialParseSummary{ParsedDocumentID: parsed.ID, Status: parsed.Status, ParserID: parsed.ParserID,
			ParserVersion: parsed.ParserVersion, CreatedAt: parsed.CreatedAt, OriginalDigest: parsed.OriginalDigest, NormalizedDigest: parsed.NormalizedDigest})
	}
	return result, nil
}

func (s *Store) GetMaterialVersionParsedChunk(ctx context.Context, user accounts.User, caseID, materialID string, version int, parsedID string, offset, limit int) (MaterialParsedChunk, error) {
	current, st, err := authorizePinnedMaterialRead(ctx, s, user, caseID)
	if err != nil {
		return MaterialParsedChunk{}, err
	}
	materialVersion, document, err := pinnedMaterialVersion(st, current, caseID, strings.TrimSpace(materialID), version)
	if err != nil {
		return MaterialParsedChunk{}, err
	}
	parsed, ok := st.parsedByID(strings.TrimSpace(parsedID))
	if !ok || parsed.DocumentID != document.ID || parsed.OriginalDigest != document.Digest {
		return MaterialParsedChunk{}, fault.NotFound("没有找到对应材料解析版本")
	}
	if err := verifyParsed(parsed, document); err != nil {
		return MaterialParsedChunk{}, fault.New(500, "integrity_error", "材料解析记录校验失败")
	}
	if parsed.Status != "ready" {
		return MaterialParsedChunk{}, fault.Conflict("所选材料解析版本没有可定位正文")
	}
	if offset < 0 || limit < 0 {
		return MaterialParsedChunk{}, fault.Invalid("offset和limit必须是非负整数")
	}
	if limit == 0 {
		limit = 32768
	}
	if limit > 65536 {
		return MaterialParsedChunk{}, fault.Invalid("每页材料正文最多为65536个字符")
	}
	runes := []rune(parsed.Text)
	if offset > len(runes) {
		return MaterialParsedChunk{}, fault.Invalid("offset超出正文范围")
	}
	end := min(offset+limit, len(runes))
	var next *int
	if end < len(runes) {
		value := end
		next = &value
	}
	return MaterialParsedChunk{MaterialID: materialVersion.MaterialID, MaterialVersion: materialVersion.Version, DocumentID: document.ID,
		ParsedDocumentID: parsed.ID, Filename: document.Filename, OriginalDigest: document.Digest, ParseDigest: parsed.ArtifactDigest,
		TotalRunes: len(runes), Offset: offset, Text: string(runes[offset:end]), NextOffset: next}, nil
}
