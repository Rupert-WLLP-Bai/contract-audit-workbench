package app

import (
	"context"
	"errors"
	"time"

	"contractaudit/internal/contracttemplate"
	"contractaudit/internal/fault"
	"contractaudit/internal/knowledge/model"
)

type TemplateRepository interface {
	VersionReader
	ActiveTemplates(context.Context, string, model.ReviewCaseFacts, string, string, string) ([]ActiveKnowledgeIdentity, error)
}
type TemplateFacts interface {
	LegalFactsReader
	AuthorizeReadSource(context.Context, model.GovernedKnowledgeContent) error
}

// ResolveTemplates filters exact, active Knowledge-owned versions for one
// already-authorized case. A requested exact identity may never fall back.
func ResolveTemplates(ctx context.Context, repo TemplateRepository, facts TemplateFacts, tenant string, item model.ReviewCaseFacts, id, version, digest string, at time.Time) ([]contracttemplate.Resolved, error) {
	ids, err := repo.ActiveTemplates(ctx, tenant, item, id, version, digest)
	if err != nil {
		return nil, err
	}
	out := make([]contracttemplate.Resolved, 0, len(ids))
	for _, ref := range ids {
		loaded, e := LoadExactVersion(ctx, repo, facts, tenant, ref.KnowledgeID, ref.VersionID)
		if e != nil {
			return nil, e
		}
		v := loaded.Version
		applies, e := model.KnowledgeApplies(v.Content, item, at)
		if e != nil {
			return nil, e
		}
		if !applies {
			continue
		}
		if !v.Active || v.Status != "published" || v.ApprovedDigest != v.Ref.ContentSHA256 || v.ApprovalActorID == "" || v.ApprovalReason == "" || v.Content.Template == nil {
			return nil, fault.Conflict("正文范本批准信息无效")
		}
		if e = facts.AuthorizeReadSource(ctx, v.Content); e != nil {
			var problem *fault.Error
			if errors.As(e, &problem) && problem.Status == 404 {
				continue
			}
			return nil, e
		}
		content := *v.Content.Template
		if id == "" {
			content.Body = ""
		}
		out = append(out, contracttemplate.Resolved{ID: v.Ref.KnowledgeID, Name: v.Content.Title, Version: v.Ref.VersionID, Digest: v.Ref.ContentSHA256, Content: content})
	}
	if id != "" && len(out) != 1 {
		return nil, fault.NotFound("正文范本不可用")
	}
	return out, nil
}
