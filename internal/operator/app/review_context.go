package app

import (
	"context"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
	"contractaudit/internal/rulepack"
)

type ContextReader interface {
	ReadRepository
	ListAllContextHeads(context.Context, ReadIdentity) ([]model.ServiceObjectHead, error)
}
type SelectedFrozenMaterial struct {
	MaterialID                   string
	MaterialVersion              int
	HasMaterial                  bool
	DocumentID, ParsedDocumentID string
	Ready                        bool
	ParseError                   string
}

func LoadContextSources(ctx context.Context, repo ContextReader, id ReadIdentity) ([]model.ContextSource, error) {
	heads, err := repo.ListAllContextHeads(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(heads) > 100 {
		return nil, fault.Invalid("每个案件最多登记100个服务对象")
	}
	sources := make([]model.ContextSource, 0, len(heads))
	for _, head := range heads {
		version, err := ReadServiceObjectVersionExact(ctx, repo, id, head.ID, head.CurrentVersion)
		if err != nil {
			return nil, err
		}
		source := model.ContextSource{Head: head, Version: version}
		if head.CurrentAssociationVersion > 0 {
			v, err := ReadServiceObjectAssociationExact(ctx, repo, id, head.ID, head.CurrentAssociationVersion)
			if err != nil {
				return nil, err
			}
			source.Association = &v
		}
		mapping, found, err := readResolvedMapping(ctx, repo, id, head.ID)
		if err != nil {
			return nil, err
		}
		if found {
			mh, binding, identity, assoc := mapping.head, mapping.binding, mapping.identity, mapping.association
			if mh.ServiceObjectID != head.ID || binding.ObjectVersion != head.CurrentVersion || binding.AssociationVersion != head.CurrentAssociationVersion || identity.Version != head.CurrentVersion || source.Association == nil || assoc.AssociationVersion != source.Association.AssociationVersion {
				return nil, fault.New(500, "integrity_error", "服务对象与当前映射版本指针不一致")
			}
			source.Mapping = &model.ContextMappingPin{MappingID: mh.MappingID, MappingVersion: mh.CurrentVersion, ServiceObjectID: mh.ServiceObjectID, ObjectVersion: binding.ObjectVersion, AssociationVersion: binding.AssociationVersion, BindingVersion: binding.Version, Status: binding.Status, EffectiveFrom: binding.EffectiveFrom, EffectiveUntil: binding.EffectiveUntil}
		}
		sources = append(sources, source)
	}
	return sources, nil
}
func FreezeContext(ctx context.Context, repo ContextReader, id ReadIdentity, businessAt time.Time, documents []SelectedFrozenMaterial, hasGoverned bool) (*model.ServiceObjectReviewContext, string, error) {
	sources, err := LoadContextSources(ctx, repo, id)
	if err != nil {
		return nil, "", err
	}
	if len(sources) == 0 && !hasGoverned {
		return nil, "", nil
	}
	if businessAt.IsZero() {
		return nil, "", fault.Invalid("该案件已登记服务对象，必须提供明确的业务时间")
	}
	businessAt = businessAt.UTC().Truncate(time.Microsecond)
	ready := len(documents) > 0
	for _, doc := range documents {
		if !doc.HasMaterial || doc.MaterialID == "" || doc.MaterialVersion < 1 || doc.DocumentID == "" || doc.ParsedDocumentID == "" || !doc.Ready || doc.ParseError != "" {
			ready = false
		}
	}
	var candidates []model.ServiceObjectCandidateEvidence
	if ready {
		for _, source := range sources {
			assoc := source.Association
			if assoc == nil {
				continue
			}
			matched := false
			for _, doc := range documents {
				if doc.HasMaterial && doc.MaterialID == assoc.MaterialID && doc.MaterialVersion == assoc.MaterialVersion && doc.DocumentID == assoc.DocumentID && doc.ParsedDocumentID == assoc.ParsedDocumentID {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			facts := make([]rulepack.Fact, len(assoc.FactRefs))
			for i := range assoc.FactRefs {
				facts[i] = assoc.FactRefs[i].Fact
			}
			items, err := model.ServiceObjectCandidatesFromFrozenFacts(assoc.MaterialID, assoc.MaterialVersion, assoc.DocumentID, assoc.ParsedDocumentID, assoc.SourceRunID, assoc.SourceAttemptID, facts)
			if err != nil {
				return nil, "", err
			}
			candidates = append(candidates, items...)
		}
	}
	result, err := model.PlanServiceObjectReviewContext(businessAt, sources, candidates)
	if err != nil {
		return nil, "", err
	}
	result.Items = append(result.Items, model.ServiceObjectReviewContextItem{UnresolvedReason: "recognized_object_inventory_pending_review"})
	result.Complete = false
	_, hash, err := model.CanonicalServiceObjectReviewContext(result)
	if err != nil {
		return nil, "", err
	}
	result.SHA256 = hash
	return &result, hash, nil
}
