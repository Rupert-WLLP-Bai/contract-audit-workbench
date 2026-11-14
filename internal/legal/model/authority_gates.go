// File metadata (manifest mirror):
// Purpose: Validates Legal reuse fingerprints and aggregates frozen authority outcomes into ordered conservative RulePack gates.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"errors"
	"sort"

	"contractaudit/internal/rulepack"
)

// ValidLegalReuseFingerprint checks the exact immutable Legal fields using the
// same canonical preparation path used when the frozen value was sealed.
func ValidLegalReuseFingerprint(fields *LegalReuseFields) bool {
	if fields == nil || fields.LegalReuseFingerprint == "" {
		return false
	}
	copy := *fields
	copy.LegalAuthorities = append([]FrozenLegalAuthority(nil), fields.LegalAuthorities...)
	if err := PrepareLegalReuse(&copy); err != nil {
		return false
	}
	return copy.LegalReuseFingerprint == fields.LegalReuseFingerprint
}

// RulepackAuthorityGates aggregates exact frozen Legal outcomes for RulePack's
// all_of gate. The input is Legal-owned snapshot content, without caller auth.
func RulepackAuthorityGates(fields LegalReuseFields) ([]rulepack.AuthorityGate, error) {
	if len(fields.LegalAuthorities) == 0 {
		return nil, nil
	}
	if !ValidLegalReuseFingerprint(&fields) {
		return nil, errors.New("frozen legal authority snapshot seal is invalid")
	}
	type key struct{ ruleID, sourceID, sourceVersion string }
	type aggregate struct {
		gate             rulepack.AuthorityGate
		unknown          bool
		unknownWhy       string
		notApplicable    bool
		referenceMissing bool
	}
	byKey := make(map[key]*aggregate)
	for _, authority := range fields.LegalAuthorities {
		k := key{authority.RuleID, authority.AuthoritySourceID, authority.AuthoritySourceVersion}
		item := byKey[k]
		if item == nil {
			item = &aggregate{gate: rulepack.AuthorityGate{RuleID: k.ruleID, SourceID: k.sourceID, SourceVersion: k.sourceVersion, Outcome: "applicable"}}
			byKey[k] = item
		}
		if authority.LegacyRuleGap {
			item.referenceMissing = true
			item.unknownWhy = "legal_reference_missing"
			continue
		}
		switch authority.Outcome {
		case "applicable":
		case "not_applicable":
			item.notApplicable = true
		case "unknown":
			item.unknown = true
			reason := authority.OutcomeReason
			if reason == "" {
				reason = "legal_authority_unknown"
			}
			if item.unknownWhy == "" || reason < item.unknownWhy {
				item.unknownWhy = reason
			}
		default:
			return nil, errors.New("frozen legal authority has an invalid outcome")
		}
	}
	keys := make([]key, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ruleID != keys[j].ruleID {
			return keys[i].ruleID < keys[j].ruleID
		}
		if keys[i].sourceID != keys[j].sourceID {
			return keys[i].sourceID < keys[j].sourceID
		}
		return keys[i].sourceVersion < keys[j].sourceVersion
	})
	out := make([]rulepack.AuthorityGate, 0, len(keys))
	for _, k := range keys {
		item := byKey[k]
		if item.referenceMissing {
			item.gate.Outcome = "unknown"
			item.gate.Reason = "legal_reference_missing"
			item.gate.ReferenceMissing = true
		} else if item.notApplicable {
			item.gate.Outcome = "not_applicable"
			item.gate.Reason = "legal_authority_not_applicable"
		} else if item.unknown {
			item.gate.Outcome = "unknown"
			item.gate.Reason = item.unknownWhy
			if item.gate.Reason == "" {
				item.gate.Reason = "legal_authority_unknown"
			}
		}
		out = append(out, item.gate)
	}
	return out, nil
}
