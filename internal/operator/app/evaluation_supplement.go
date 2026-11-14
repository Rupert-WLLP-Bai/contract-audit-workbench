// File metadata (manifest mirror):
// Purpose: Extracts candidates and validates original contract sources from exact frozen documents with the original source-count and request rules.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"

	executionmodel "contractaudit/internal/execution/model"
	"contractaudit/internal/fault"
	"contractaudit/internal/rulepack"
	"contractaudit/internal/telecom"
)

type SupplementSources[D executionmodel.OriginalDocument] interface {
	Sources(context.Context, executionmodel.FrozenDocument[D]) ([]rulepack.Source, error)
}

func SupplementCandidates[D executionmodel.OriginalDocument](ctx context.Context, f SupplementSources[D], scope PreviewScope, documents []executionmodel.FrozenDocument[D]) ([]EvaluationCandidate[D], error) {
	request := rulepack.Request{BusinessLine: scope.BusinessLine, Scenario: scope.Scenario, ContractType: scope.ContractType}
	var candidates []EvaluationCandidate[D]
	for _, frozen := range documents {
		if !IsContractSource(frozen) || frozen.Material.Purpose != "supplement" {
			continue
		}
		sources, err := f.Sources(ctx, frozen)
		if err != nil || len(sources) != 1 {
			return nil, fault.Conflict("冻结补充协议未通过完整性校验")
		}
		facts, err := telecom.ExtractContractMaterial(request, sources[0])
		if err != nil {
			return nil, fault.Conflict("冻结补充协议的对象事实无法提取")
		}
		seen := make(map[string]bool)
		for _, fact := range facts {
			if fact.ObjectRef != "" && !seen[fact.ObjectRef] {
				seen[fact.ObjectRef] = true
				candidates = append(candidates, EvaluationCandidate[D]{ObjectRef: fact.ObjectRef, Source: frozen})
			}
		}
	}
	return candidates, nil
}

type AggregateSources[D executionmodel.OriginalDocument] interface {
	SourcesBatch(context.Context, []executionmodel.FrozenDocument[D]) ([]rulepack.Source, error)
}

func ValidateAggregateSources[D executionmodel.OriginalDocument](ctx context.Context, f AggregateSources[D], documents []executionmodel.FrozenDocument[D]) error {
	var selected []executionmodel.FrozenDocument[D]
	for _, doc := range documents {
		if IsContractSource(doc) {
			selected = append(selected, doc)
		}
	}
	if len(selected) == 0 {
		return nil
	}
	_, err := f.SourcesBatch(ctx, selected)
	return err
}
