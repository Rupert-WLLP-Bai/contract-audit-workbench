// File metadata (manifest mirror):
// Purpose: Checks playbook rule/source binding rejection and omission-compatible legacy canonical knowledge encoding.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPlaybookRequiresBoundRuleAndSource(t *testing.T) {
	base := GovernedKnowledgeContent{
		Binding:  PolicyRuleBinding{RuleIDs: []string{"rule-1"}, SourceSpans: []KnowledgeSourceSpan{{RuleID: "rule-1"}}},
		Playbook: &PlaybookContent{Template: PlaybookTemplateRef{TemplateID: "template", TemplateVersion: "1", TemplateDigest: strings.Repeat("a", 64)}, Clauses: []PlaybookClause{{RuleID: "rule-1", StandardClause: "标准", AcceptableDeviation: "偏差", HardConstraint: "底线", ProposedWording: "建议", EscalationCondition: "升级"}}},
	}
	if err := ValidatePlaybook(base); err != nil {
		t.Fatal(err)
	}
	otherRule := base
	otherRule.Binding.RuleIDs = []string{"rule-2"}
	missingSource := base
	missingSource.Binding.SourceSpans = nil
	for _, tc := range []struct {
		name    string
		content GovernedKnowledgeContent
	}{{"unbound rule", otherRule}, {"missing source", missingSource}} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePlaybook(tc.content); err == nil {
				t.Fatal("invalid playbook accepted")
			}
		})
	}
}

func TestLegacyKnowledgeOmitPlaybook(t *testing.T) {
	content := GovernedKnowledgeContent{Encoding: "governed-knowledge/v1", KnowledgeID: "legacy", Kind: "guidance"}
	raw, err := CanonicalContentBytes(content)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["playbook"]; ok {
		t.Fatal("nil playbook changed legacy canonical JSON")
	}
	var decoded GovernedKnowledgeContent
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	again, err := CanonicalContentBytes(decoded)
	if err != nil || string(raw) != string(again) {
		t.Fatal("legacy canonical bytes changed on round trip")
	}
}
