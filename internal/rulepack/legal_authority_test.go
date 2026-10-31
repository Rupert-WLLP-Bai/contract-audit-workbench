package rulepack

import (
	"encoding/json"
	"reflect"
	"testing"
)

func legalAuthorityManifest(t *testing.T, authorities []any) []byte {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(fixture(t, "demo.json"), &root); err != nil {
		t.Fatal(err)
	}
	rules := root["rules"].([]any)
	rule := rules[0].(map[string]any)
	rule["authority_sources"] = []any{map[string]any{"id": "civil-code", "version": "2020-05-28", "kind": "normative"}}
	if authorities != nil {
		rule["legal_authorities"] = authorities
	}
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func legalAuthorityRef(articleNumber int, knowledgeID string, quote string) map[string]any {
	return map[string]any{
		"authority_source_id": "civil-code",
		"knowledge_id":        knowledgeID,
		"version_id":          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"content_sha256":      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"source_id":           "civil-code",
		"source_snapshot_id":  "capture-civil-2026-10-30",
		"norm_title":          "中华人民共和国民法典",
		"norm_version":        "2020-05-28",
		"article_number":      articleNumber,
		"document_type":       "law",
		"source_sha256":       "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		"article_text_sha256": "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		"effective_from":      "2021-01-01",
		"effective_until":     "",
		"quote_span":          map[string]any{"start": 0, "end": len([]rune(quote)), "quote": quote},
		"applicability": map[string]any{"all_of": []any{
			map[string]any{"field": "transaction_kind", "one_of": []any{"civil_contract"}},
		}},
	}
}

func TestLegalAuthorityReferenceNormalizesAndDeepClones(t *testing.T) {
	// These short quotes match the reviewed source fixture; this is a shape-only
	// RulePack fixture and does not verify source provenance or legal meaning.
	first := legalAuthorityRef(496, "legal-civil-code-496", "格式条款是当事人为了重复使用而预先拟定")
	second := legalAuthorityRef(497, "legal-civil-code-497", "该格式条款无效")
	firstAllOf := first["applicability"].(map[string]any)["all_of"].([]any)
	firstCondition := firstAllOf[0].(map[string]any)
	firstCondition["one_of"] = []any{"civil_contract", "non_civil", "civil_contract"}
	firstBuyerCondition := map[string]any{"field": "buyer_kind", "one_of": []any{"enterprise"}}
	first["applicability"].(map[string]any)["all_of"] = append(firstAllOf, firstBuyerCondition, firstBuyerCondition)
	other := legalAuthorityRef(496, "legal-civil-code-496", "格式条款是当事人为了重复使用而预先拟定")
	otherAllOf := other["applicability"].(map[string]any)["all_of"].([]any)
	otherAllOf[0].(map[string]any)["one_of"] = []any{"non_civil", "civil_contract"}
	other["applicability"].(map[string]any)["all_of"] = append(otherAllOf, map[string]any{"field": "buyer_kind", "one_of": []any{"enterprise"}})
	pack, err := Parse(legalAuthorityManifest(t, []any{second, first, other}))
	if err != nil {
		t.Fatalf("parse valid legal authority references: %v", err)
	}
	rule := legalRule(t, pack.Manifest().Rules)
	if len(rule.LegalAuthorities) != 2 {
		t.Fatalf("exact duplicate references were not deduplicated: %d", len(rule.LegalAuthorities))
	}
	if rule.LegalAuthorities[0].ArticleNumber != 496 || rule.LegalAuthorities[1].ArticleNumber != 497 {
		t.Fatalf("references not in canonical order: %+v", rule.LegalAuthorities)
	}
	gotValues := rule.LegalAuthorities[0].Applicability.AllOf[0].OneOf
	if len(rule.LegalAuthorities[0].Applicability.AllOf) != 2 || rule.LegalAuthorities[0].Applicability.AllOf[0].Field != "buyer_kind" {
		t.Fatalf("conditions were not sorted/deduplicated: %+v", rule.LegalAuthorities[0].Applicability.AllOf)
	}
	gotValues = rule.LegalAuthorities[0].Applicability.AllOf[1].OneOf
	if !reflect.DeepEqual(gotValues, []string{"civil_contract", "non_civil"}) {
		t.Fatalf("condition values not canonicalized and deduplicated: %v", gotValues)
	}
	changed := pack.Manifest()
	changedRule := legalRule(t, changed.Rules)
	changedRule.LegalAuthorities[0].Applicability.AllOf[0].OneOf[0] = "mutated"
	changedRule.LegalAuthorities[0].QuoteSpan.Quote = "mutated"
	secondCopy := legalRule(t, pack.Manifest().Rules).LegalAuthorities[0]
	if secondCopy.Applicability.AllOf[0].OneOf[0] == "mutated" || secondCopy.QuoteSpan.Quote == "mutated" {
		t.Fatal("manifest returned a shallow legal-authority copy")
	}
}

func legalRule(t *testing.T, rules []Rule) *Rule {
	t.Helper()
	for i := range rules {
		if rules[i].RuleID == "ENT-TECH-BW-001" {
			return &rules[i]
		}
	}
	t.Fatal("legal authority test rule missing")
	return nil
}

func TestLegalAuthorityOptionalFieldPreservesHistoricalManifestBytesAndHash(t *testing.T) {
	legacy := fixture(t, "demo.json")
	var root map[string]any
	if err := json.Unmarshal(legacy, &root); err != nil {
		t.Fatal(err)
	}
	withoutField, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	rules := root["rules"].([]any)
	rules[0].(map[string]any)["legal_authorities"] = []any{}
	withEmptyField, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	without, err := Parse(withoutField)
	if err != nil {
		t.Fatalf("parse historical normative manifest without legal authorities: %v", err)
	}
	empty, err := Parse(withEmptyField)
	if err != nil {
		t.Fatalf("parse historical normative manifest with empty optional authorities: %v", err)
	}
	withoutSnapshot, _ := without.Snapshot()
	emptySnapshot, _ := empty.Snapshot()
	if without.Hash() != "2e9d0351d6043ee18b8df629bf67973b5706f3fe0ce84464fc547d148f51c95d" ||
		without.Hash() != empty.Hash() || string(withoutSnapshot) != string(emptySnapshot) {
		t.Fatal("nil/empty legal authorities changed legacy manifest hash or canonical snapshot bytes")
	}
}

func TestLegalAuthorityReferenceRejectsMalformedStructure(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "authority source id differs", mutate: func(ref map[string]any) { ref["authority_source_id"] = "other-law" }},
		{name: "source id differs from authority source", mutate: func(ref map[string]any) { ref["source_id"] = "other-law" }},
		{name: "version differs from normative source", mutate: func(ref map[string]any) { ref["norm_version"] = "2020-01-01" }},
		{name: "hash malformed", mutate: func(ref map[string]any) { ref["source_sha256"] = "xyz" }},
		{name: "empty identity", mutate: func(ref map[string]any) { ref["knowledge_id"] = "" }},
		{name: "unknown document type", mutate: func(ref map[string]any) { ref["document_type"] = "blog_post" }},
		{name: "span rune length differs", mutate: func(ref map[string]any) { ref["quote_span"].(map[string]any)["end"] = 1 }},
		{name: "unsupported condition field", mutate: func(ref map[string]any) {
			ref["applicability"].(map[string]any)["all_of"].([]any)[0].(map[string]any)["field"] = "caller_selected_json_path"
		}},
		{name: "unsupported enum value", mutate: func(ref map[string]any) {
			ref["applicability"].(map[string]any)["all_of"].([]any)[0].(map[string]any)["one_of"] = []any{"unknown"}
		}},
		{name: "empty condition values", mutate: func(ref map[string]any) {
			ref["applicability"].(map[string]any)["all_of"].([]any)[0].(map[string]any)["one_of"] = []any{}
		}},
		{name: "missing applicability scope", mutate: func(ref map[string]any) { ref["applicability"].(map[string]any)["all_of"] = []any{} }},
		{name: "conflicting conditions for one field", mutate: func(ref map[string]any) {
			allOf := ref["applicability"].(map[string]any)["all_of"].([]any)
			allOf = append(allOf, map[string]any{"field": "transaction_kind", "one_of": []any{"non_civil"}})
			ref["applicability"].(map[string]any)["all_of"] = allOf
		}},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			ref := legalAuthorityRef(496, "legal-civil-code-496", "格式条款是当事人为了重复使用而预先拟定")
			test.mutate(ref)
			if _, err := Parse(legalAuthorityManifest(t, []any{ref})); err == nil {
				t.Fatal("malformed legal authority reference was accepted")
			}
		})
	}
	t.Run("no matching normative source", func(t *testing.T) {
		ref := legalAuthorityRef(496, "legal-civil-code-496", "格式条款是当事人为了重复使用而预先拟定")
		raw := legalAuthorityManifest(t, []any{ref})
		var root map[string]any
		if err := json.Unmarshal(raw, &root); err != nil {
			t.Fatal(err)
		}
		root["rules"].([]any)[0].(map[string]any)["authority_sources"] = []any{map[string]any{"id": "civil-code", "version": "2020-05-28", "kind": "internal_policy"}}
		wrongKind, _ := json.Marshal(root)
		if _, err := Parse(wrongKind); err == nil {
			t.Fatal("legal reference without matching normative authority source was accepted")
		}
	})
}
