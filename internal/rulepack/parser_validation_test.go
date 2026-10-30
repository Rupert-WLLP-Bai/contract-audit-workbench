package rulepack

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func mutateDemoManifest(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(fixture(t, "demo.json"), &root); err != nil {
		t.Fatal(err)
	}
	mutate(root)
	data, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func ruleMap(root map[string]any, id string) map[string]any {
	rules := root["rules"].([]any)
	for _, value := range rules {
		rule := value.(map[string]any)
		if rule["rule_id"] == id {
			return rule
		}
	}
	panic("test rule missing: " + id)
}

func factRequirements(rule map[string]any) map[string]any {
	return rule["required_fact"].(map[string]any)
}
func thresholdMap(rule map[string]any) map[string]any { return rule["threshold"].(map[string]any) }

func TestParseRejectsUnsupportedManifestValues(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"empty-pack-id", func(root map[string]any) { root["id"] = "" }},
		{"empty-tenant", func(root map[string]any) { root["tenant_id"] = "" }},
		{"zero-pack-version", func(root map[string]any) { root["version"] = "0" }},
		{"invalid-pack-version", func(root map[string]any) { root["version"] = "01" }},
		{"invalid-effective-time", func(root map[string]any) { root["effective_from"] = "2026-01-01" }},
		{"reversed-effective-range", func(root map[string]any) {
			root["effective_from"], root["effective_until"] = "2027-01-01T00:00:00Z", "2026-01-01T00:00:00Z"
		}},
		{"empty-rules", func(root map[string]any) { root["rules"] = []any{} }},
		{"duplicate-rule-id", func(root map[string]any) { rules := root["rules"].([]any); rules[1] = rules[0] }},
		{"empty-rule-id", func(root map[string]any) { ruleMap(root, "ENT-TECH-BW-001")["rule_id"] = "" }},
		{"noncanonical-rule-version", func(root map[string]any) { ruleMap(root, "ENT-TECH-BW-001")["version"] = "01" }},
		{"empty-capability", func(root map[string]any) { ruleMap(root, "ENT-TECH-BW-001")["capability_id"] = "" }},
		{"unsupported-domain", func(root map[string]any) { ruleMap(root, "ENT-TECH-BW-001")["domain"] = "technical_service" }},
		{"empty-business-scope", func(root map[string]any) {
			ruleMap(root, "ENT-TECH-BW-001")["business_scope"].(map[string]any)["scenario"] = ""
		}},
		{"rule-tenant-mismatch", func(root map[string]any) { ruleMap(root, "ENT-TECH-BW-001")["tenant_id"] = "tenant-other" }},
		{"no-authority", func(root map[string]any) { ruleMap(root, "ENT-TECH-BW-001")["authority_sources"] = []any{} }},
		{"invalid-authority-id", func(root map[string]any) {
			ruleMap(root, "ENT-TECH-BW-001")["authority_sources"].([]any)[0].(map[string]any)["id"] = ""
		}},
		{"unsupported-authority-kind", func(root map[string]any) {
			ruleMap(root, "ENT-TECH-BW-001")["authority_sources"].([]any)[0].(map[string]any)["kind"] = "unverified"
		}},
		{"duplicate-authority", func(root map[string]any) {
			rule := ruleMap(root, "ENT-TECH-BW-001")
			sources := rule["authority_sources"].([]any)
			rule["authority_sources"] = append(sources, sources[0])
		}},
		{"malformed-threshold", func(root map[string]any) { thresholdMap(ruleMap(root, "ENT-TECH-BW-001"))["value"] = "1e2" }},
		{"unbounded-threshold", func(root map[string]any) {
			thresholdMap(ruleMap(root, "ENT-TECH-BW-001"))["value"] = strings.Repeat("9", 90)
		}},
		{"wrong-bandwidth-unit", func(root map[string]any) { thresholdMap(ruleMap(root, "ENT-TECH-BW-001"))["unit"] = "MiB/s" }},
		{"no-source-role", func(root map[string]any) {
			factRequirements(ruleMap(root, "ENT-TECH-BW-001"))["source_roles"] = []any{}
		}},
		{"duplicate-source-role", func(root map[string]any) {
			factRequirements(ruleMap(root, "ENT-TECH-BW-001"))["source_roles"] = []any{"draft_contract", "draft_contract"}
		}},
		{"object-not-required", func(root map[string]any) {
			factRequirements(ruleMap(root, "ENT-TECH-BW-001"))["object_required"] = false
		}},
		{"monthly-over-100", func(root map[string]any) { thresholdMap(ruleMap(root, "ENT-TECH-SLA-001"))["value"] = "100.01" }},
		{"monthly-wrong-unit", func(root map[string]any) { thresholdMap(ruleMap(root, "ENT-TECH-SLA-001"))["unit"] = "ratio" }},
		{"monthly-no-window", func(root map[string]any) {
			factRequirements(ruleMap(root, "ENT-TECH-SLA-001"))["measurement_window"] = "annual"
		}},
		{"monthly-no-timezone", func(root map[string]any) { factRequirements(ruleMap(root, "ENT-TECH-SLA-001"))["time_zone"] = "" }},
		{"monthly-wrong-calendar", func(root map[string]any) { factRequirements(ruleMap(root, "ENT-TECH-SLA-001"))["calendar"] = "fiscal" }},
		{"monthly-null-exclusions", func(root map[string]any) { factRequirements(ruleMap(root, "ENT-TECH-SLA-001"))["exclusions"] = nil }},
		{"monthly-duplicate-exclusion", func(root map[string]any) {
			factRequirements(ruleMap(root, "ENT-TECH-SLA-001"))["exclusions"] = []any{"planned_maintenance", "planned_maintenance"}
		}},
		{"advance-ceiling-over-one", func(root map[string]any) { thresholdMap(ruleMap(root, "ENT-COM-ADV-001"))["value"] = "2" }},
		{"advance-wrong-basis", func(root map[string]any) { thresholdMap(ruleMap(root, "ENT-COM-ADV-001"))["basis"] = "unbounded" }},
		{"advance-fact-basis-mismatch", func(root map[string]any) { factRequirements(ruleMap(root, "ENT-COM-ADV-001"))["basis"] = "net_total" }},
		{"unknown-comparator", func(root map[string]any) { ruleMap(root, "ENT-COM-ADV-001")["comparator"] = "expression" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			raw := mutateDemoManifest(t, test.mutate)
			if _, err := Parse(raw); !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("Parse error %v", err)
			}
		})
	}
}

func TestParseKeepsRuleValidationErrorPrecedence(t *testing.T) {
	raw := mutateDemoManifest(t, func(root map[string]any) {
		rule := ruleMap(root, "ENT-TECH-BW-001")
		rule["rule_id"] = ""
		rule["comparator"] = "unsupported"
	})
	_, err := Parse(raw)
	if err == nil || !strings.Contains(err.Error(), "rule_id, version and capability_id") || strings.Contains(err.Error(), "unsupported comparator") {
		t.Fatalf("Parse changed rule validation priority: %v", err)
	}
}

func TestManifestHashNormalizesEquivalentInstants(t *testing.T) {
	base, err := Parse(fixture(t, "demo.json"))
	if err != nil {
		t.Fatal(err)
	}
	equivalent := mutateDemoManifest(t, func(root map[string]any) {
		root["effective_from"] = "2026-01-01T08:00:00+08:00"
		root["effective_until"] = "2027-01-01T08:00:00+08:00"
	})
	other, err := Parse(equivalent)
	if err != nil {
		t.Fatal(err)
	}
	if base.Hash() != other.Hash() {
		t.Fatalf("equivalent UTC instants changed hash: %s != %s", base.Hash(), other.Hash())
	}
}

func TestPublishedSnapshotsRejectLifecycleTampering(t *testing.T) {
	original := string(fixture(t, "published-demo.json"))
	cases := []struct{ name, from, to string }{
		{"verification-result", `"passed": true`, `"passed": false`},
		{"verification-hash", `"verification_hash": "f9c69c1f50bb13921b43d88fa3a4c4d9ada1e597aae781a3374682d08a2c4461"`, `"verification_hash": "bad"`},
		{"approval-actor", `"actor_id": "demo-approver"`, `"actor_id": "other-approver"`},
		{"release-hash", `"release_hash": "3f8c8cd0b27e23713caf2394a43ed4cffe25e88a6a07d783117cc86901cc199b"`, `"release_hash": "bad"`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			tampered := strings.Replace(original, test.from, test.to, 1)
			if tampered == original {
				t.Fatal("test mutation did not match fixture")
			}
			if _, err := Parse([]byte(tampered)); !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("tampered lifecycle accepted: %v", err)
			}
		})
	}
}
