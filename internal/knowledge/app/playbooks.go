package app

import (
	"context"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/knowledge/model"
)

type PlaybookFacts interface {
	ReviewFacts
	VerifyPlaybookTemplate(context.Context, model.PlaybookTemplateRef) error
}

// ResolvePlaybook must run within the caller's authorized acceptance transaction.
func ResolvePlaybook(ctx context.Context, repo VersionReader, facts PlaybookFacts, tenant string, item model.ReviewCaseFacts, ref model.KnowledgeRef, pack model.GovernedRulePackRef, at time.Time) (model.AdoptedPlaybook, error) {
	if ref.KnowledgeID == "" || !model.ValidHash(ref.VersionID) || !model.ValidHash(ref.ContentSHA256) {
		return model.AdoptedPlaybook{}, fault.Invalid("手册引用无效")
	}
	loaded, err := LoadExactVersion(ctx, repo, facts, tenant, ref.KnowledgeID, ref.VersionID)
	if err != nil {
		return model.AdoptedPlaybook{}, err
	}
	v := loaded.Version
	if v.Ref != ref || v.Content.Kind != "playbook" || v.Content.Playbook == nil || !v.Active || v.Status != "published" || v.ApprovedDigest != ref.ContentSHA256 || v.ApprovalActorID == "" || v.ApprovalReason == "" {
		return model.AdoptedPlaybook{}, fault.Conflict("审查手册不可用或批准身份不符")
	}
	applies, err := model.KnowledgeApplies(v.Content, item, at)
	if err != nil {
		return model.AdoptedPlaybook{}, err
	}
	if !applies {
		return model.AdoptedPlaybook{}, fault.NotFound("审查手册不适用于当前范围")
	}
	if err = facts.AuthorizeReadSource(ctx, v.Content); err != nil {
		return model.AdoptedPlaybook{}, err
	}
	b := v.Content.Binding
	if b.PackID != pack.PackID || b.PackVersion != pack.Version || b.PackContentHash != pack.ContentHash || b.PackSnapshotDigest != pack.SnapshotDigest || b.CompilerRef != pack.CompilerRef {
		return model.AdoptedPlaybook{}, fault.Conflict("手册规则包与当前范围不匹配")
	}
	binding, err := facts.LoadRulePack(ctx, v.Content)
	if err != nil {
		return model.AdoptedPlaybook{}, err
	}
	if err = ValidateRulePackBinding(tenant, v.Content, binding); err != nil {
		return model.AdoptedPlaybook{}, err
	}
	if err = facts.VerifyPlaybookTemplate(ctx, v.Content.Playbook.Template); err != nil {
		return model.AdoptedPlaybook{}, err
	}
	return model.AdoptedPlaybook{Ref: ref, Content: v.Content}, nil
}
