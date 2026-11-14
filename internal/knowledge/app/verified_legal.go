package app

import (
	"context"
	"encoding/json"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/knowledge/model"
	"contractaudit/internal/legalnorm"
)

type LegalDraftSlots interface {
	VersionSlotOccupied(context.Context, string, string) (bool, error)
}
type LegalDraftPrepareRepository interface {
	GovernedFamilyHasOtherKind(context.Context, string, string, string) (bool, error)
}
type LegalDraftRepository interface {
	InsertVersion(context.Context, CreateVersionRecord) (time.Time, error)
	InsertInitialState(context.Context, CreateDraftIdentity, string, string) error
	AppendHistory(context.Context, CreateDraftIdentity, model.GovernedKnowledgeHistoryEntry) error
}
type ExactLegalDraftFacts interface {
	LoadLegalCapture(context.Context, string) (*LegalCaptureFacts, error)
	LoadLegalArticleIndex(context.Context, *LegalCaptureFacts) ([]legalnorm.VerifiedArticle, error)
}

// PrepareVerifiedLegalDrafts preserves the original per-article validation order.
// The calling Legal use case supplies the already verified contents in order.
func PrepareVerifiedLegalDrafts(ctx context.Context, repo LegalDraftPrepareRepository, slots LegalDraftSlots, tenant string, contents []model.GovernedKnowledgeContent) ([]model.PreparedVerifiedLegalDraft, error) {
	out := make([]model.PreparedVerifiedLegalDraft, 0, len(contents))
	seen := make(map[string]bool)
	for _, content := range contents {
		identity := content.KnowledgeID + "\x00" + content.Version
		if seen[identity] {
			return nil, fault.Invalid("法律条文版本编号重复")
		}
		seen[identity] = true
		if err := model.ValidateContent(content); err != nil {
			return nil, err
		}
		occupied, err := slots.VersionSlotOccupied(ctx, content.KnowledgeID, content.Version)
		if err != nil {
			return nil, err
		}
		if occupied {
			return nil, fault.Conflict("制度版本已经存在；旧制度不会自动升级为已治理内容")
		}
		other, err := repo.GovernedFamilyHasOtherKind(ctx, tenant, content.KnowledgeID, content.Kind)
		if err != nil {
			return nil, err
		}
		if other {
			return nil, fault.Conflict("制度族的类型不可改变")
		}
		raw, err := json.Marshal(content)
		if err != nil {
			return nil, err
		}
		ref := model.KnowledgeRef{KnowledgeID: content.KnowledgeID, VersionID: model.VersionID(tenant, content.KnowledgeID, content.Version), ContentSHA256: model.SHA256(raw)}
		out = append(out, model.PreparedVerifiedLegalDraft{Ref: ref, Content: content, ContentBytes: raw})
	}
	return out, nil
}

// PersistVerifiedLegalDrafts runs only after the parent Legal capture insert.
func PersistVerifiedLegalDrafts(ctx context.Context, repo LegalDraftRepository, facts ExactLegalDraftFacts, tenant, actor, captureID string, drafts []model.PreparedVerifiedLegalDraft) error {
	identity := CreateDraftIdentity{TenantID: tenant, ActorID: actor}
	for _, draft := range drafts {
		content := draft.Content
		if err := model.ValidateLegalMetadata(content.LegalMetadata, content.Body); err != nil {
			return fault.Conflict("已保存的法律条文元数据无效")
		}
		capture, err := facts.LoadLegalCapture(ctx, content.LegalMetadata.SourceSnapshotID)
		if err != nil {
			return err
		}
		if err := validateLegalCaptureReference(ctx, facts, content, capture); err != nil {
			return err
		}
		raw, err := json.Marshal(content)
		if err != nil {
			return err
		}
		scopeKey, err := model.ScopeKey(content.Scope)
		if err != nil {
			return err
		}
		_, err = repo.InsertVersion(ctx, CreateVersionRecord{Identity: identity, Content: content, VersionID: draft.Ref.VersionID, ScopeKey: scopeKey, ContentSHA256: draft.Ref.ContentSHA256, ContentBytes: raw, CreatedBy: actor, LegalCaptureID: captureID})
		if err != nil {
			return err
		}
		if err := repo.InsertInitialState(ctx, identity, content.KnowledgeID, draft.Ref.VersionID); err != nil {
			return err
		}
		if err := repo.AppendHistory(ctx, identity, model.GovernedKnowledgeHistoryEntry{Ref: draft.Ref, Action: "create", Revision: 1, ActorID: actor, Reason: "导入已核验的官方法律来源"}); err != nil {
			return err
		}
	}
	return nil
}

type ImportedLegalReferenceReader interface {
	ImportedLegalContentSHA(context.Context, string, string, model.KnowledgeRef) (string, error)
}

func ValidateImportedLegalReferences(ctx context.Context, repo ImportedLegalReferenceReader, tenant, captureID string, refs []model.KnowledgeRef) error {
	for _, ref := range refs {
		digest, err := repo.ImportedLegalContentSHA(ctx, tenant, captureID, ref)
		if err != nil || digest != ref.ContentSHA256 {
			return fault.Conflict("法律来源幂等结果与已保存条文不一致")
		}
	}
	return nil
}

type PublishedLegalCaptureReader interface {
	HasPublishedLegalCapture(context.Context, string, string) (bool, error)
}

func LegalCaptureReferenced(ctx context.Context, repo PublishedLegalCaptureReader, tenant, captureID string) (bool, error) {
	return repo.HasPublishedLegalCapture(ctx, tenant, captureID)
}
