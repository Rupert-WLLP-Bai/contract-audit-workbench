// File metadata (manifest mirror):
// Purpose: Runs version and pointer CAS lifecycle commands and validates exact raw Knowledge rows through typed source facts, preserving replay and dependency order.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/knowledge/model"
)

type RawVersion struct {
	Version                                                                                      model.GovernedKnowledgeVersion
	ContentBytes, Binding                                                                        []byte
	ScopeKey, Kind, DisplayVersion, PointerID                                                    string
	ScopeOrg, ScopeCase, SourceOrg, SourceCase, CandidateID, CandidateVersionID, CandidateDigest *string
	BindingPackID, BindingPackVersion, LegalCaptureID                                            string
	PointerActive                                                                                bool
	CreatedAt                                                                                    time.Time
}
type LoadedVersion struct {
	Version             model.GovernedKnowledgeVersion
	ScopeKey, PointerID string
	PointerActive       bool
}
type VersionReader interface {
	LoadVersion(context.Context, string, string, string) (RawVersion, error)
}
type CommandRepository interface {
	VersionReader
	IdempotencyReceipt(context.Context, CreateDraftIdentity, string) (string, []byte, bool, error)
	InsertIdempotencyReceipt(context.Context, CreateDraftIdentity, string, string, []byte) error
	UpdateState(context.Context, CreateDraftIdentity, model.GovernedKnowledgeCommandInput, model.CommandPlan) (bool, error)
	UpdatePointer(context.Context, CreateDraftIdentity, string, string, string, bool, int64) (bool, error)
	AppendHistory(context.Context, CreateDraftIdentity, model.GovernedKnowledgeHistoryEntry) error
}
type CommandFacts interface {
	CreateDraftFacts
	LoadRulePack(context.Context, model.GovernedKnowledgeContent) (RulePackBindingFacts, error)
	CheckCandidateSource(context.Context, ExperienceCandidateFacts) error
	CheckCandidateCompleteness(context.Context, ExperienceCandidateFacts, time.Time) error
}

func nullable(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func LoadExactVersion(ctx context.Context, repo VersionReader, facts LegalFactsReader, tenant, id, version string) (LoadedVersion, error) {
	raw, err := repo.LoadVersion(ctx, tenant, id, version)
	if err != nil {
		return LoadedVersion{}, err
	}
	return ValidateRawVersion(ctx, facts, tenant, raw, nil)
}

// ValidateRawVersion verifies one exact persisted row without depending on
// Store scope, authorization or transaction setup. A preloaded capture map is
// supplied by batched readers to preserve one Legal read per page.
func ValidateRawVersion(ctx context.Context, facts LegalFactsReader, tenant string, raw RawVersion, captures map[string]*LegalCaptureFacts) (LoadedVersion, error) {
	out := raw.Version
	knowledgeID, versionID := out.Ref.KnowledgeID, out.Ref.VersionID
	if out.Status == "" {
		return LoadedVersion{}, fault.NotFound("没有找到对应制度版本")
	}
	if json.Unmarshal(raw.ContentBytes, &out.Content) != nil || model.SHA256(raw.ContentBytes) != out.Ref.ContentSHA256 {
		return LoadedVersion{}, fault.Conflict("制度正文或摘要损坏")
	}
	canonical, err := model.CanonicalContentBytes(out.Content)
	if err != nil || !bytes.Equal(raw.ContentBytes, canonical) {
		return LoadedVersion{}, fault.Conflict("制度规范编码损坏")
	}
	if out.Content.Kind == "legal" {
		if len(raw.Binding) != 0 || raw.BindingPackID != "" || raw.BindingPackVersion != "" {
			return LoadedVersion{}, fault.Conflict("法律来源不得存在伪造的规则绑定")
		}
		if err := model.ValidateLegalMetadata(out.Content.LegalMetadata, out.Content.Body); err != nil {
			return LoadedVersion{}, fault.Conflict("已保存的法律条文元数据无效")
		}
		var capture *LegalCaptureFacts
		if captures == nil {
			capture, err = facts.LoadLegalCapture(ctx, out.Content.LegalMetadata.SourceSnapshotID)
			if err != nil {
				return LoadedVersion{}, err
			}
		} else {
			capture = captures[out.Content.LegalMetadata.SourceSnapshotID]
		}
		if err := validateLegalCaptureReference(ctx, facts, out.Content, capture); err != nil {
			return LoadedVersion{}, err
		}
	} else {
		b, err := json.Marshal(out.Content.Binding)
		if err != nil || !bytes.Equal(raw.Binding, b) || raw.BindingPackID != out.Content.Binding.PackID || raw.BindingPackVersion != out.Content.Binding.PackVersion {
			return LoadedVersion{}, fault.Conflict("制度规则绑定损坏")
		}
	}
	c := out.Content
	key, err := model.ScopeKey(c.Scope)
	if err != nil || key != raw.ScopeKey || c.KnowledgeID != knowledgeID || c.Version != raw.DisplayVersion || c.Kind != raw.Kind || model.VersionID(tenant, knowledgeID, raw.DisplayVersion) != versionID ||
		c.Scope.OrgID != nullable(raw.ScopeOrg) || c.Scope.CaseID != nullable(raw.ScopeCase) || c.SourceOrgID != nullable(raw.SourceOrg) || c.SourceCaseID != nullable(raw.SourceCase) || c.CandidateID != nullable(raw.CandidateID) || c.CandidateVersionID != nullable(raw.CandidateVersionID) || c.CandidateSHA256 != nullable(raw.CandidateDigest) {
		return LoadedVersion{}, fault.Conflict("制度编号或适用范围记录已损坏")
	}
	if (c.Kind == "legal" && raw.LegalCaptureID != c.LegalMetadata.SourceSnapshotID) || (c.Kind != "legal" && raw.LegalCaptureID != "") {
		return LoadedVersion{}, fault.Conflict("法律来源快照外键与制度内容不一致")
	}
	if err := model.ValidateContent(c); err != nil {
		return LoadedVersion{}, fault.Conflict("已保存的制度内容无效")
	}
	out.CreatedAt = raw.CreatedAt.UTC().Format(time.RFC3339Nano)
	out.Active = raw.PointerActive && raw.PointerID == versionID && out.Status == "published"
	return LoadedVersion{Version: out, ScopeKey: raw.ScopeKey, PointerID: raw.PointerID, PointerActive: raw.PointerActive}, nil
}
func validateLegalContentBinding(ctx context.Context, facts CommandFacts, content model.GovernedKnowledgeContent) error {
	if err := model.ValidateLegalMetadata(content.LegalMetadata, content.Body); err != nil {
		return fault.Conflict("已保存的法律条文元数据无效")
	}
	capture, err := facts.LoadLegalCapture(ctx, content.LegalMetadata.SourceSnapshotID)
	if err != nil {
		return err
	}
	return validateLegalCaptureReference(ctx, facts, content, capture)
}
func checkBinding(ctx context.Context, facts CommandFacts, tenant string, c model.GovernedKnowledgeContent) error {
	if c.Kind == "legal" {
		return validateLegalContentBinding(ctx, facts, c)
	}
	raw, err := facts.LoadRulePack(ctx, c)
	if err != nil {
		return err
	}
	return ValidateRulePackBinding(tenant, c, raw)
}
func checkCandidate(ctx context.Context, facts CommandFacts, c model.GovernedKnowledgeContent) error {
	if c.Kind != "experience" {
		return nil
	}
	candidate, err := loadCandidateFacts(ctx, facts, c)
	if err != nil {
		return err
	}
	if err = facts.AuthorizeExperienceAncestors(ctx, candidate); err != nil {
		return err
	}
	if err = facts.CheckCandidateSource(ctx, candidate); err != nil {
		return err
	}
	if err = facts.CheckCandidateCompleteness(ctx, candidate, time.Now().UTC()); err != nil {
		return err
	}
	from, e1 := model.ParseBusinessTime(c.ValidFrom)
	until, e2 := model.ParseBusinessTime(c.ValidUntil)
	cf, e3 := model.ParseBusinessTime(candidate.ValidFrom)
	cu, e4 := model.ParseBusinessTime(candidate.ValidUntil)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || from.Before(cf) || until.After(cu) {
		return fault.Conflict("经验有效期不能超出候选的有效期")
	}
	refs := make(map[model.KnowledgeRef]int)
	for _, ref := range c.PolicyRefs {
		refs[ref]++
	}
	for _, ref := range candidate.PolicyRefs {
		refs[ref]--
	}
	for _, n := range refs {
		if n != 0 {
			return fault.Conflict("经验政策依赖与候选不一致")
		}
	}
	return nil
}
func checkDependencies(ctx context.Context, repo CommandRepository, facts CommandFacts, tenant string, c model.GovernedKnowledgeContent) error {
	seen := model.NewPolicyRefSet()
	for _, ref := range c.PolicyRefs {
		if err := seen.Add(ref); err != nil {
			return err
		}
		item, err := LoadExactVersion(ctx, repo, facts, tenant, ref.KnowledgeID, ref.VersionID)
		if err != nil {
			return fault.Conflict("依赖的政策版本不存在")
		}
		if err = model.ValidatePolicyDependency(c, ref, item.Version); err != nil {
			return err
		}
		if err = checkBinding(ctx, facts, tenant, item.Version.Content); err != nil {
			return err
		}
	}
	return nil
}
func Command(ctx context.Context, repo CommandRepository, facts CommandFacts, identity CreateDraftIdentity, input model.GovernedKnowledgeCommandInput) (model.GovernedKnowledgeVersion, error) {
	if !model.ValidText(input.IdempotencyKey, 128) || input.IdempotencyKey != strings.TrimSpace(input.IdempotencyKey) {
		return model.GovernedKnowledgeVersion{}, fault.Invalid("需要有效的请求标识（幂等键）")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	digest := model.SHA256(payload)
	saved, response, found, err := repo.IdempotencyReceipt(ctx, identity, input.IdempotencyKey)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	if found {
		if saved != digest {
			return model.GovernedKnowledgeVersion{}, fault.Conflict("这个请求标识已用于其他操作")
		}
		var out model.GovernedKnowledgeVersion
		if err = json.Unmarshal(response, &out); err != nil {
			return model.GovernedKnowledgeVersion{}, err
		}
		if err = facts.RecheckAdmin(ctx); err != nil {
			return model.GovernedKnowledgeVersion{}, err
		}
		if err = facts.AuthorizeSource(ctx, out.Content); err != nil {
			return model.GovernedKnowledgeVersion{}, err
		}
		if out.Content.Kind == "experience" {
			candidate, e := loadCandidateFacts(ctx, facts, out.Content)
			if e != nil {
				return model.GovernedKnowledgeVersion{}, e
			}
			if e = facts.AuthorizeExperienceAncestors(ctx, candidate); e != nil {
				return model.GovernedKnowledgeVersion{}, e
			}
		}
		return out, nil
	}
	if err = model.ValidateCommandInput(input); err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	loaded, err := LoadExactVersion(ctx, repo, facts, identity.TenantID, input.KnowledgeID, input.VersionID)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	current := loaded.Version
	if err = facts.AuthorizeSource(ctx, current.Content); err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	plan, err := model.PlanCommand(current, input)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	if plan.RequiresRulePackBinding {
		if err = checkBinding(ctx, facts, identity.TenantID, current.Content); err != nil {
			return model.GovernedKnowledgeVersion{}, err
		}
	}
	if plan.RequiresExperienceCandidate {
		if err = checkCandidate(ctx, facts, current.Content); err != nil {
			return model.GovernedKnowledgeVersion{}, err
		}
	}
	if plan.RequiresPolicyDependencies {
		if err = checkDependencies(ctx, repo, facts, identity.TenantID, current.Content); err != nil {
			return model.GovernedKnowledgeVersion{}, err
		}
	}
	plan, err = model.CompleteCommand(current, input, identity.ActorID, plan)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	ok, err := repo.UpdateState(ctx, identity, input, plan)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	if !ok {
		return model.GovernedKnowledgeVersion{}, fault.Conflict("制度修订已改变")
	}
	pointerRevision := current.PointerRevision
	resultID, resultActive := loaded.PointerID, loaded.PointerActive
	if plan.PointerAction {
		pointerRevision++
		resultID, resultActive = input.VersionID, input.Action != "withdraw"
		ok, err = repo.UpdatePointer(ctx, identity, input.KnowledgeID, loaded.ScopeKey, resultID, resultActive, current.PointerRevision)
		if err != nil {
			return model.GovernedKnowledgeVersion{}, err
		}
		if !ok {
			return model.GovernedKnowledgeVersion{}, fault.Conflict("制度生效指针已改变")
		}
	}
	err = repo.AppendHistory(ctx, identity, model.GovernedKnowledgeHistoryEntry{Ref: current.Ref, Action: input.Action, Revision: current.Revision + 1, PointerRevision: pointerRevision, ActorID: identity.ActorID, Reason: input.Reason, PreviousVersionID: loaded.PointerID, PreviousActive: loaded.PointerActive, ResultingVersionID: resultID, ResultingActive: resultActive})
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	updated, err := LoadExactVersion(ctx, repo, facts, identity.TenantID, input.KnowledgeID, input.VersionID)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	out := updated.Version
	if err = facts.RecheckAdmin(ctx); err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	if err = facts.AuthorizeSource(ctx, out.Content); err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	response, err = json.Marshal(out)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	if err = repo.InsertIdempotencyReceipt(ctx, identity, input.IdempotencyKey, digest, response); err != nil {
		return out, err
	}
	return out, nil
}
