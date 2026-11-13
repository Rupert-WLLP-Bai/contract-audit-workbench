package app

import (
	"context"
	"encoding/json"
	"errors"

	"contractaudit/internal/fault"
	"contractaudit/internal/knowledge/model"
	"contractaudit/internal/rulepack"
)

// KnowledgeReadRepository exposes fixed, typed governance-row queries.
type KnowledgeReadRepository interface {
	VersionReader
	ListVersionPage(context.Context, string, string, string, int) ([]RawVersion, error)
	LoadVersions(context.Context, string, string, []string) ([]RawVersion, error)
	ListHistory(context.Context, string, string) ([]model.GovernedKnowledgeHistoryEntry, error)
}

// KnowledgeReadFacts supplies current source authorization and Legal facts.
type KnowledgeReadFacts interface {
	LegalFactsReader
	AuthorizeReadSource(context.Context, model.GovernedKnowledgeContent) error
	LoadReadCandidate(context.Context, model.GovernedKnowledgeContent) (ExperienceCandidateFacts, error)
	CandidateFeedbackExists(context.Context, ExperienceCandidateFacts) (bool, error)
	AuthorizeReadAncestors(context.Context, ExperienceCandidateFacts) error
}

type readCandidateAdapter struct{ facts KnowledgeReadFacts }

func (a readCandidateAdapter) LoadExperienceCandidate(ctx context.Context, c model.GovernedKnowledgeContent) (ExperienceCandidateFacts, error) {
	return a.facts.LoadReadCandidate(ctx, c)
}
func (a readCandidateAdapter) CandidateFeedbackExists(ctx context.Context, c ExperienceCandidateFacts) (bool, error) {
	return a.facts.CandidateFeedbackExists(ctx, c)
}
func authorizeKnowledgeRead(ctx context.Context, facts KnowledgeReadFacts, content model.GovernedKnowledgeContent) error {
	if err := facts.AuthorizeReadSource(ctx, content); err != nil {
		return err
	}
	if content.Kind != "experience" {
		return nil
	}
	candidate, err := loadCandidateFacts(ctx, readCandidateAdapter{facts}, content)
	if err != nil {
		return err
	}
	return facts.AuthorizeReadAncestors(ctx, candidate)
}

// RulePackManifestFacts is the unfiltered first-stage manifest read used by
// policy preview before its separate full binding check.
type RulePackManifestFacts struct {
	Manifest       []byte
	ContentHash    string
	SnapshotDigest string
}

// PolicyPreviewFacts combines current source authorization with the two
// intentionally separate RulePack reads performed by preview.
type PolicyPreviewFacts interface {
	KnowledgeReadFacts
	LoadRulePackManifest(context.Context, model.GovernedKnowledgeContent) (RulePackManifestFacts, error)
	LoadRulePack(context.Context, model.GovernedKnowledgeContent) (RulePackBindingFacts, error)
}

// PublishedGlobalPolicySummary is the exact projection used by Platform's
// tenant-config allow-list; it does not imply full Knowledge validation.
type PublishedGlobalPolicySummary struct {
	KnowledgeID    string
	VersionID      string
	ContentSHA256  string
	DisplayVersion string
}

// PublishedGlobalPolicyReader reads only global, published, active policy
// summary rows for one tenant.
type PublishedGlobalPolicyReader interface {
	ListPublishedGlobalPolicies(context.Context, string) ([]PublishedGlobalPolicySummary, error)
}

func ListGovernedKnowledgeVersions(ctx context.Context, repository KnowledgeReadRepository, facts KnowledgeReadFacts, tenant string) ([]model.GovernedKnowledgeSummary, error) {
	out := []model.GovernedKnowledgeSummary{}
	const batchSize = 128
	lastKnowledge, lastVersion := "", ""
	for {
		raw, err := repository.ListVersionPage(ctx, tenant, lastKnowledge, lastVersion, batchSize)
		if err != nil {
			return out, err
		}
		captures, err := legalCapturesForVersions(ctx, facts, raw)
		if err != nil {
			return out, err
		}
		for _, item := range raw {
			lastKnowledge, lastVersion = item.Version.Ref.KnowledgeID, item.Version.Ref.VersionID
			loaded, err := ValidateRawVersion(ctx, facts, tenant, item, captures)
			if err != nil {
				return out, err
			}
			if err := authorizeKnowledgeRead(ctx, facts, loaded.Version.Content); err != nil {
				var problem *fault.Error
				if errors.As(err, &problem) && problem.Status == 404 {
					continue
				}
				return out, err
			}
			version := loaded.Version
			out = append(out, model.GovernedKnowledgeSummary{
				Ref: version.Ref, Version: version.Content.Version, Title: version.Content.Title, Kind: version.Content.Kind,
				SourceType: version.Content.SourceType, Scope: version.Content.Scope, ValidFrom: version.Content.ValidFrom,
				ValidUntil: version.Content.ValidUntil, Status: version.Status, Revision: version.Revision,
				PointerRevision: version.PointerRevision, Active: version.Active,
			})
		}
		if len(raw) < batchSize {
			break
		}
	}
	return out, nil
}

func GetGovernedKnowledgeVersion(ctx context.Context, repository VersionReader, facts KnowledgeReadFacts, tenant, knowledgeID, versionID string) (model.GovernedKnowledgeVersion, error) {
	loaded, err := LoadExactVersion(ctx, repository, facts, tenant, knowledgeID, versionID)
	if err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	if err := authorizeKnowledgeRead(ctx, facts, loaded.Version.Content); err != nil {
		return model.GovernedKnowledgeVersion{}, err
	}
	return loaded.Version, nil
}

func ListGovernedKnowledgeHistory(ctx context.Context, repository KnowledgeReadRepository, facts KnowledgeReadFacts, tenant, knowledgeID string) ([]model.GovernedKnowledgeHistoryEntry, error) {
	entries, err := repository.ListHistory(ctx, tenant, knowledgeID)
	if err != nil {
		return []model.GovernedKnowledgeHistoryEntry{}, err
	}
	if entries == nil {
		entries = []model.GovernedKnowledgeHistoryEntry{}
	}
	const batchSize = 128
	checked := make(map[string]bool)
	for start := 0; start < len(entries); start += batchSize {
		end := start + batchSize
		if end > len(entries) {
			end = len(entries)
		}
		ids := make([]string, 0, end-start)
		pending := make(map[string]bool)
		for _, entry := range entries[start:end] {
			id := entry.Ref.VersionID
			if !checked[id] && !pending[id] {
				pending[id] = true
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			continue
		}
		raw, err := repository.LoadVersions(ctx, tenant, knowledgeID, ids)
		if err != nil {
			return entries, err
		}
		byID := make(map[string]RawVersion, len(raw))
		for _, item := range raw {
			byID[item.Version.Ref.VersionID] = item
		}
		captures, err := legalCapturesForVersions(ctx, facts, raw)
		if err != nil {
			return entries, err
		}
		for _, entry := range entries[start:end] {
			id := entry.Ref.VersionID
			if checked[id] {
				continue
			}
			item, ok := byID[id]
			if !ok {
				return entries, fault.NotFound("没有找到对应制度版本")
			}
			loaded, err := ValidateRawVersion(ctx, facts, tenant, item, captures)
			if err != nil {
				return entries, err
			}
			if err := authorizeKnowledgeRead(ctx, facts, loaded.Version.Content); err != nil {
				return entries, err
			}
			checked[id] = true
		}
	}
	return entries, nil
}

func PreviewGovernedPolicy(ctx context.Context, repository VersionReader, facts PolicyPreviewFacts, tenant, knowledgeID, versionID string) (model.GovernedPolicyPreview, error) {
	loaded, err := LoadExactVersion(ctx, repository, facts, tenant, knowledgeID, versionID)
	if err != nil {
		return model.GovernedPolicyPreview{}, err
	}
	version := loaded.Version
	if err := authorizeKnowledgeRead(ctx, facts, version.Content); err != nil {
		return model.GovernedPolicyPreview{}, err
	}
	if version.Content.Kind != "policy" {
		return model.GovernedPolicyPreview{}, fault.Invalid("只有政策版本可以提取规则候选")
	}
	snapshot, err := facts.LoadRulePackManifest(ctx, version.Content)
	if err != nil {
		return model.GovernedPolicyPreview{}, err
	}
	pack, err := rulepack.Parse(snapshot.Manifest)
	if err != nil || model.SHA256(snapshot.Manifest) != snapshot.SnapshotDigest || pack.Hash() != snapshot.ContentHash ||
		snapshot.ContentHash != version.Content.Binding.PackContentHash || snapshot.SnapshotDigest != version.Content.Binding.PackSnapshotDigest {
		return model.GovernedPolicyPreview{}, fault.Conflict("制度引用的规则包编号无效")
	}
	preview := model.CompareGovernedPolicyThresholds(version.Content, pack.Manifest())
	preview.Ref = version.Ref
	binding, err := facts.LoadRulePack(ctx, version.Content)
	if err == nil {
		err = ValidateRulePackBinding(tenant, version.Content, binding)
	}
	if err != nil {
		var problem *fault.Error
		if !errors.As(err, &problem) || problem.Status != 409 {
			return model.GovernedPolicyPreview{}, err
		}
		preview.Approvable = false
		preview.Issues = append(preview.Issues, model.PolicyTextIssue{Reason: "binding_or_policy_invalid"})
	}
	return preview, nil
}

func ListPublishedGlobalPolicySummaries(ctx context.Context, repository PublishedGlobalPolicyReader, tenant string) ([]PublishedGlobalPolicySummary, error) {
	items, err := repository.ListPublishedGlobalPolicies(ctx, tenant)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []PublishedGlobalPolicySummary{}
	}
	return items, nil
}

func legalCapturesForVersions(ctx context.Context, facts LegalFactsReader, versions []RawVersion) (map[string]*LegalCaptureFacts, error) {
	ids := make([]string, 0)
	seen := make(map[string]bool)
	for _, raw := range versions {
		var content model.GovernedKnowledgeContent
		if json.Unmarshal(raw.ContentBytes, &content) != nil || content.Kind != "legal" || content.LegalMetadata == nil {
			continue
		}
		id := content.LegalMetadata.SourceSnapshotID
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return map[string]*LegalCaptureFacts{}, nil
	}
	return facts.LoadLegalCaptures(ctx, ids)
}
