// File metadata (manifest mirror):
// Purpose: Resolves Legal applicability by business time and evidence cutoff using the single dimension source and exact current Subject pin facts.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package app coordinates Legal evidence using exact, authorized facts.
package app

import (
	"context"
	legal "contractaudit/internal/legal/model"
	"time"
)

type FactFamily struct {
	Dimension string
	Head      int64
	Versions  []legal.LegalApplicabilityFactVersion
}
type SubjectBinding struct {
	Found                          bool
	BindingVersion, SubjectVersion int64
	SubjectID, Role, State         string
}
type ApplicabilityFacts interface {
	LoadFactFamilies(context.Context) ([]FactFamily, error)
	LoadSubjectBinding(context.Context, legal.LegalApplicabilityPartyPin) (SubjectBinding, error)
	LoadSubjectVersion(context.Context, string) (int64, bool, error)
	CountActiveRole(context.Context, string) (int, error)
}

func ResolveApplicability(ctx context.Context, facts ApplicabilityFacts, businessAt, cutoff time.Time) ([]legal.LegalApplicabilityResolvedFact, error) {
	families, err := facts.LoadFactFamilies(ctx)
	if err != nil {
		return nil, err
	}
	byDimension := make(map[string]FactFamily, len(families))
	for _, f := range families {
		byDimension[f.Dimension] = f
	}
	dimensions := legal.ApplicabilityDimensions()
	out := make([]legal.LegalApplicabilityResolvedFact, 0, len(dimensions))
	for _, dimension := range dimensions {
		family, ok := byDimension[dimension]
		if !ok {
			out = append(out, legal.LegalApplicabilityResolvedFact{Dimension: dimension, Status: "unknown", UnknownReason: "missing_fact"})
			continue
		}
		selection := legal.SelectLegalApplicabilityFactVersion(family.Head, family.Versions, businessAt, cutoff)
		selected := selection.Version
		if selected.Version <= 0 {
			out = append(out, legal.LegalApplicabilityResolvedFact{Dimension: dimension, Status: "unknown", UnknownReason: "missing_fact"})
			continue
		}
		selected.Dimension = dimension
		resolved := legal.LegalApplicabilityResolvedFact{Dimension: dimension, Status: selected.Status, Value: selected.Value, UnknownReason: selection.UnknownReason, Fact: &selected}
		if selection.UnknownReason != "" {
			resolved.Status, resolved.Value = "unknown", ""
		}
		if resolved.Status == "established" && selected.Party.BindingID != "" {
			binding, err := facts.LoadSubjectBinding(ctx, selected.Party)
			if err != nil {
				return nil, err
			}
			stale := !binding.Found
			ambiguous := false
			if binding.Found {
				current, found, err := facts.LoadSubjectVersion(ctx, binding.SubjectID)
				if err != nil {
					return nil, err
				}
				if !found {
					stale = true
				} else {
					stale = legal.PartyPinStale(selected.Party, legal.PartyBindingFacts{BindingVersion: binding.BindingVersion, SubjectVersion: binding.SubjectVersion, CurrentSubjectVersion: current, SubjectID: binding.SubjectID, Role: binding.Role, State: binding.State})
					if !stale {
						count, err := facts.CountActiveRole(ctx, binding.Role)
						if err != nil {
							return nil, err
						}
						ambiguous = legal.PartyScopeAmbiguous(selected.Party, count)
					}
				}
			}
			if stale {
				resolved.Status, resolved.Value, resolved.UnknownReason = "unknown", "", "stale_party_context"
			} else if ambiguous {
				resolved.Status, resolved.Value, resolved.UnknownReason = "unknown", "", "party_scope_ambiguous"
			}
		}
		out = append(out, resolved)
	}
	return out, nil
}
