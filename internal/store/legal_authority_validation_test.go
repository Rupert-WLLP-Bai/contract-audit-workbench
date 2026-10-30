package store

import (
	"testing"

	"contractaudit/internal/rulepack"
)

func TestChangedNormativeRuleRequiresLegalAuthorities(t *testing.T) {
	base := rulepack.Rule{RuleID: "rule-a", AuthoritySources: []rulepack.AuthoritySource{{ID: "CivilCode", Version: "2021", Kind: "normative"}}}
	if err := validateNormativeRuleReferenceChanges(nil, []rulepack.Rule{base}); err == nil {
		t.Fatal("new normative rule without an exact legal article must be rejected")
	}
	if err := validateNormativeRuleReferenceChanges(map[string]rulepack.Rule{"rule-a": base}, []rulepack.Rule{base}); err != nil {
		t.Fatalf("unchanged historical normative rule without refs remains parseable: %v", err)
	}
	changed := base
	changed.Version = "2"
	if err := validateNormativeRuleReferenceChanges(map[string]rulepack.Rule{"rule-a": base}, []rulepack.Rule{changed}); err == nil {
		t.Fatal("changed normative rule without an exact legal article must be rejected")
	}
	changed.LegalAuthorities = []rulepack.LegalAuthorityReference{{KnowledgeID: "civil-496"}}
	if err := validateNormativeRuleReferenceChanges(map[string]rulepack.Rule{"rule-a": base}, []rulepack.Rule{changed}); err != nil {
		t.Fatalf("new normative rule with an authored legal ref proceeds to exact source validation: %v", err)
	}
}

func TestOnlyApprovedPublishedCurrentLegalInstrumentCanBackRule(t *testing.T) {
	ref := KnowledgeRef{ContentSHA256: "c"}
	version := GovernedKnowledgeVersion{
		Ref: ref, Content: GovernedKnowledgeContent{Kind: "legal", LegalMetadata: &LegalMetadata{DocumentType: "law"}},
		Status: "published", ApprovedDigest: "c", ApprovalActorID: "reviewer", ApprovalReason: "checked",
	}
	if !isPublishableLegalAuthority(version) {
		t.Fatal("approved, published law with matching digest should be eligible for exact-reference validation")
	}
	cases := []struct {
		name   string
		mutate func(*GovernedKnowledgeVersion)
	}{
		{"missing approval digest", func(v *GovernedKnowledgeVersion) { v.ApprovedDigest = "" }},
		{"approval digest mismatch", func(v *GovernedKnowledgeVersion) { v.ApprovedDigest = "other" }},
		{"missing approval actor", func(v *GovernedKnowledgeVersion) { v.ApprovalActorID = "" }},
		{"missing approval reason", func(v *GovernedKnowledgeVersion) { v.ApprovalReason = "" }},
		{"draft is not current law", func(v *GovernedKnowledgeVersion) { v.Content.LegalMetadata.DocumentType = "draft" }},
		{"legislative plan is not current law", func(v *GovernedKnowledgeVersion) { v.Content.LegalMetadata.DocumentType = "legislative_plan" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := version
			metadata := *version.Content.LegalMetadata
			candidate.Content.LegalMetadata = &metadata
			tc.mutate(&candidate)
			if isPublishableLegalAuthority(candidate) {
				t.Fatal("legal article without a valid approval or current-law document type was accepted")
			}
		})
	}
}
