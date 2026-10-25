package rulepack

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func reviewExpectRelation(t *testing.T, pack *Pack, request Request, ruleID string, want Relation) {
	t.Helper()
	report, err := runRequest(t, pack, request)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultFor(t, report, ruleID).Relation; got != want {
		t.Fatalf("rule %s relation %s, want %s", ruleID, got, want)
	}
}

func TestReviewEvidenceBindsObjectAndValueWithinOneAssertion(t *testing.T) {
	pack := demoPublished(t)
	request := demoRequest(t)
	replaceEvidence(t, &request, "fact-bandwidth", "专线甲用于接入；专线乙最低带宽承诺为150 Mbit/s。", "150", "Mbit/s", "专线甲")
	reviewExpectRelation(t, pack, request, "ENT-TECH-BW-001", RelationInsufficient)
}

func TestReviewEvidenceRejectsCompetingCitationsForOneFact(t *testing.T) {
	pack := demoPublished(t)
	request := demoRequest(t)
	firstQuote := "专线甲最低带宽承诺为100 Mbit/s。"
	secondQuote := "专线甲最低带宽承诺为50 Mbit/s。"
	replaceEvidence(t, &request, "fact-bandwidth", firstQuote+secondQuote, "100", "Mbit/s", "专线甲")
	fact := &request.Facts[1]
	first := fact.Citations[0]
	first.End = len([]rune(firstQuote))
	first.Quote = firstQuote
	second := first
	second.Start = first.End
	second.End = len([]rune(firstQuote + secondQuote))
	second.Quote = secondQuote
	fact.Citations = []Citation{first, second}
	reviewExpectRelation(t, pack, request, "ENT-TECH-BW-001", RelationInsufficient)
}

func TestReviewEvidenceRequiresAffirmativeNumericAssertions(t *testing.T) {
	pack := demoPublished(t)
	for _, scenario := range []struct {
		name, quote string
	}{
		{"explicit-negation", "专线甲最低带宽并非100 Mbit/s。"},
		{"negation-outside-nearby-window", "专线甲带宽未承诺，" + strings.Repeat("待书面确认，", 3) + "100 Mbit/s。"},
		{"scientific-notation-suffix", "专线甲最低带宽承诺为1e2 Mbit/s。"},
		{"range-endpoint", "专线甲最低带宽承诺为50至100 Mbit/s。"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := demoRequest(t)
			value := "100"
			if scenario.name == "scientific-notation-suffix" {
				value = "2"
			}
			replaceEvidence(t, &request, "fact-bandwidth", scenario.quote, value, "Mbit/s", "专线甲")
			reviewExpectRelation(t, pack, request, "ENT-TECH-BW-001", RelationInsufficient)
		})
	}
}

func TestReviewApplicabilityRequiresTargetedPositiveExclusion(t *testing.T) {
	pack := demoPublished(t)
	for _, quote := range []string{
		"专线甲最低带宽承诺为100 Mbit/s；专线乙带宽不适用。",
		"专线甲带宽并非不适用。",
	} {
		t.Run(quote, func(t *testing.T) {
			request := demoRequest(t)
			replaceEvidence(t, &request, "fact-bandwidth", quote, "", "Mbit/s", "专线甲")
			for i := range request.Facts {
				if request.Facts[i].ID == "fact-bandwidth" {
					inapplicable := false
					request.Facts[i].Applicable = &inapplicable
				}
			}
			reviewExpectRelation(t, pack, request, "ENT-TECH-BW-001", RelationInsufficient)
		})
	}
}

func TestReviewSLARequiresExactOutageDefinitionAndConsistentExclusions(t *testing.T) {
	pack := demoPublished(t)
	for _, scenario := range []struct {
		name, quote string
	}{
		{
			name:  "different-outage-definition",
			quote: "专线甲自然月可用率为99.9%，时区Asia/Shanghai，日历Gregorian，故障口径仅统计部分服务不可用，无排除项。",
		},
		{
			name:  "empty-and-nonempty-exclusions",
			quote: "专线甲自然月可用率为99.9%，时区Asia/Shanghai，日历Gregorian，故障口径all_service_unavailability，无排除项；排除项：planned_maintenance。",
		},
		{
			name:  "different-month-window",
			quote: "专线甲非自然月可用率为99.9%，时区Asia/Shanghai，日历Gregorian，故障口径all_service_unavailability，无排除项。",
		},
		{
			name:  "mixed-measurement-periods",
			quote: "专线甲自然月及rolling month可用率为99.9%，时区Asia/Shanghai，日历Gregorian，故障口径all_service_unavailability，无排除项。",
		},
		{
			name:  "timezone-prefix-impostor",
			quote: "专线甲自然月可用率为99.9%，时区Asia/ShanghaiBackup，日历Gregorian，故障口径all_service_unavailability，无排除项。",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := demoRequest(t)
			replaceEvidence(t, &request, "fact-sla", scenario.quote, "99.9", "percent", "专线甲")
			reviewExpectRelation(t, pack, request, "ENT-TECH-SLA-001", RelationInsufficient)
		})
	}
}

func TestReviewAdvanceRatioRequiresItsConfiguredBasis(t *testing.T) {
	pack := demoPublished(t)
	request := demoRequest(t)
	replaceEvidence(t, &request, "fact-advance", "合同总额仅供参考；专线甲预付款按设备价款的20% 计算。", "0.2", "ratio", "专线甲")
	reviewExpectRelation(t, pack, request, "ENT-COM-ADV-001", RelationInsufficient)
}

func TestReviewRuleThresholdChangeAltersOutcome(t *testing.T) {
	request := demoRequest(t)
	reviewExpectRelation(t, demoPublished(t), request, "ENT-TECH-BW-001", RelationConflict)
	raw := mutateDemoManifest(t, func(root map[string]any) {
		thresholdMap(ruleMap(root, "ENT-TECH-BW-001"))["value"] = "40"
	})
	draft, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	reviewExpectRelation(t, publishDraft(t, draft), request, "ENT-TECH-BW-001", RelationCompatible)
}

func TestReviewCanonicalRationalSnapshotRemainsExecutable(t *testing.T) {
	value := "1" + strings.Repeat("0", 58) + "." + strings.Repeat("0", 17) + "1"
	raw := mutateDemoManifest(t, func(root map[string]any) {
		thresholdMap(ruleMap(root, "ENT-TECH-BW-001"))["value"] = value
	})
	draft, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := draft.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := Parse(snapshot)
	if err != nil {
		t.Fatalf("normalized snapshot cannot be parsed: %v", err)
	}
	published := publishDraft(t, reloaded)
	report, err := runRequest(t, published, demoRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := resultFor(t, report, "ENT-TECH-BW-001").Relation; got != RelationConflict {
		t.Fatalf("large exact threshold relation %s, want %s", got, RelationConflict)
	}
}

func TestReviewFactRationalBitBudgetBoundary(t *testing.T) {
	pack := demoPublished(t)
	for _, scenario := range []struct {
		name, value string
		want        Relation
	}{
		{name: "largest-bit-width", value: "1" + strings.Repeat("0", 77), want: RelationCompatible},
		{name: "one-bit-over-limit", value: "2" + strings.Repeat("0", 77), want: RelationInsufficient},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := demoRequest(t)
			quote := "专线甲最低带宽承诺为" + scenario.value + " Mbit/s。"
			replaceEvidence(t, &request, "fact-bandwidth", quote, scenario.value, "Mbit/s", "专线甲")
			reviewExpectRelation(t, pack, request, "ENT-TECH-BW-001", scenario.want)
		})
	}
}

func TestReviewFactRationalByteBudgetBoundary(t *testing.T) {
	request := demoRequest(t)
	request.Facts[1].Value = strings.Repeat("9", 48) + "/" + strings.Repeat("9", 48)
	report, err := runRequest(t, demoPublished(t), request)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultFor(t, report, "ENT-TECH-BW-001").Relation; got != RelationInsufficient {
		t.Fatalf("over-budget rational relation %s, want %s", got, RelationInsufficient)
	}
}

func TestReviewStrictJSONRejectsCaseInsensitiveFieldAliases(t *testing.T) {
	base := string(fixture(t, "demo.json"))
	for _, raw := range []string{
		strings.Replace(base, `"schema_version": "rulepack/v1"`, `"SCHEMA_VERSION": "rulepack/v1"`, 1),
		strings.Replace(base, `"mandatory": true,`, `"mandatory": true, "MANDATORY": false,`, 1),
	} {
		if _, err := Parse([]byte(raw)); !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("case-insensitive field alias accepted: %v", err)
		}
	}
}

func TestReviewRegistryReloadCannotAddVersionBeforeCurrentLatest(t *testing.T) {
	versionedPack := func(version string) *Pack {
		t.Helper()
		manifest := demoDraft(t).Manifest()
		manifest.Version = version
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		draft, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return publishDraft(t, draft)
	}
	v2, v3 := versionedPack("2"), versionedPack("3")
	current := NewRegistry()
	if err := current.Publish(v3); err != nil {
		t.Fatal(err)
	}
	incoming := NewRegistry()
	if err := incoming.Publish(v2); err != nil {
		t.Fatal(err)
	}
	if err := incoming.Publish(v3); err != nil {
		t.Fatal(err)
	}
	snapshot, err := incoming.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Reload(snapshot); !errors.Is(err, ErrVersionRollback) {
		t.Fatalf("reload inserted a retroactive version: %v", err)
	}
	latest, ok := current.Latest("tenant-demo", "line-demo")
	if !ok || latest.Hash() != v3.Hash() {
		t.Fatal("rejected reload changed the current latest release")
	}
}

func TestReviewRegistryRejectsUnsupportedSchema(t *testing.T) {
	registry := NewRegistry()
	err := registry.Reload([]byte(`{"schema_version":"rulepack-registry/v9","packs":[]}`))
	if !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("unsupported registry schema error %v", err)
	}
}

func TestReviewNilRegistryOperationsFailClosed(t *testing.T) {
	var registry *Registry
	if _, ok := registry.Lookup("tenant-demo", "line-demo", "1"); ok {
		t.Fatal("nil registry returned a release")
	}
	if _, ok := registry.Latest("tenant-demo", "line-demo"); ok {
		t.Fatal("nil registry returned a latest release")
	}
	if _, err := registry.Snapshot(); err == nil {
		t.Fatal("nil registry snapshot succeeded")
	}
	if err := registry.Publish(nil); err == nil {
		t.Fatal("nil registry publish succeeded")
	}
	if err := registry.Reload(nil); err == nil {
		t.Fatal("nil registry reload succeeded")
	}
}
