package rulepack

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func demoPublished(t *testing.T) *Pack {
	t.Helper()
	pack, err := Parse(fixture(t, "published-demo.json"))
	if err != nil {
		t.Fatal(err)
	}
	return pack
}

func demoDraft(t *testing.T) *Pack {
	t.Helper()
	pack, err := Parse(fixture(t, "demo.json"))
	if err != nil {
		t.Fatal(err)
	}
	return pack
}

func demoRequest(t *testing.T) Request {
	t.Helper()
	var request Request
	if err := json.Unmarshal(fixture(t, "demo-request.json"), &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func publishDraft(t *testing.T, pack *Pack) *Pack {
	t.Helper()
	validated, err := pack.Validate()
	if err != nil {
		t.Fatal(err)
	}
	approved, err := validated.Approve("test-approver", validated.Hash())
	if err != nil {
		t.Fatal(err)
	}
	published, err := approved.Publish()
	if err != nil {
		t.Fatal(err)
	}
	return published
}

func resultFor(t *testing.T, report Report, ruleID string) Result {
	t.Helper()
	for _, result := range report.Results {
		if result.RuleID == ruleID {
			return result
		}
	}
	t.Fatalf("result for %s not found in %+v", ruleID, report.Results)
	return Result{}
}

func replaceEvidence(t *testing.T, request *Request, factID, text, value, unit, objectRef string) {
	t.Helper()
	factIndex := -1
	for i := range request.Facts {
		if request.Facts[i].ID == factID {
			factIndex = i
			break
		}
	}
	if factIndex < 0 {
		t.Fatalf("fact %q missing", factID)
	}
	fact := &request.Facts[factIndex]
	if len(fact.Citations) == 0 {
		t.Fatalf("fact %q has no citation to replace", factID)
	}
	sourceID := fact.Citations[0].SourceID
	sourceIndex := -1
	for i := range request.Sources {
		if request.Sources[i].ID == sourceID {
			sourceIndex = i
			break
		}
	}
	if sourceIndex < 0 {
		t.Fatalf("source %q missing", sourceID)
	}
	source := &request.Sources[sourceIndex]
	source.Text = text
	source.Digest = digestHex([]byte(text))
	fact.Value, fact.Unit, fact.ObjectRef = value, unit, objectRef
	for i := range fact.Citations {
		fact.Citations[i].SourceDigest = source.Digest
		fact.Citations[i].Start = 0
		fact.Citations[i].End = len([]rune(text))
		fact.Citations[i].Quote = text
	}
}

func runRequest(t *testing.T, pack *Pack, request Request) (Report, error) {
	t.Helper()
	return pack.Execute(context.Background(), request)
}

func TestPublishedDemoRunsEveryMandatoryRuleAndReparsesDeterministically(t *testing.T) {
	pack := demoPublished(t)
	request := demoRequest(t)
	first, err := runRequest(t, pack, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.PackID != "line-demo" || first.PackVersion != "1" || first.PackHash != "2e9d0351d6043ee18b8df629bf67973b5706f3fe0ce84464fc547d148f51c95d" || first.PackHash != pack.Hash() {
		t.Fatalf("pack identity %+v", first)
	}
	if !reflect.DeepEqual(first.Relations, []Relation{RelationCompatible, RelationConflict, RelationInsufficient, RelationNotApplicable}) {
		t.Fatalf("relation vocabulary %+v", first.Relations)
	}
	if len(first.Results) != 3 {
		t.Fatalf("mandatory scoped rule count %d", len(first.Results))
	}
	wantRules := []string{"ENT-COM-ADV-001", "ENT-TECH-BW-001", "ENT-TECH-SLA-001"}
	for i, id := range wantRules {
		if first.Results[i].RuleID != id || !first.Results[i].Mandatory || first.Results[i].Relation != RelationConflict {
			t.Fatalf("rule result %+v", first.Results[i])
		}
	}
	snapshot, err := pack.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := Parse(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runRequest(t, reloaded, request)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Hash() != pack.Hash() || !reflect.DeepEqual(first, second) {
		t.Fatalf("reloaded result drift: first=%+v second=%+v", first, second)
	}
}

func TestParseRejectsUnknownDuplicateTrailingAndIncompleteJSON(t *testing.T) {
	base := string(fixture(t, "demo.json"))
	cases := []struct{ name, raw string }{
		{"unknown-field", strings.TrimSuffix(strings.TrimSpace(base), "}") + `,"unexpected":true}`},
		{"duplicate-key", strings.Replace(base, "{\n", `{"schema_version":"other",`, 1)},
		{"trailing-value", base + ` {}`},
		{"unsupported-schema", strings.Replace(base, `"rulepack/v1"`, `"rulepack/v9"`, 1)},
		{"unsupported-comparator", strings.Replace(base, `"monthly_sla"`, `"arbitrary_expression"`, 1)},
		{"missing-mandatory", strings.Replace(base, `"mandatory": true,`, ``, 1)},
		{"unsupported-domain", strings.Replace(base, `"domain": "technical"`, `"domain": "technical_service"`, 1)},
		{"null-mandatory", strings.Replace(base, `"mandatory": true,`, `"mandatory": null,`, 1)},
		{"json-number", strings.Replace(base, `"value": "100"`, `"value": 100`, 1)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Parse([]byte(test.raw)); !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("Parse error %v", err)
			}
		})
	}
}

func TestManifestHashCanonicalizesOrderAndExactRationals(t *testing.T) {
	base := fixture(t, "demo.json")
	pack, err := Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	var reordered map[string]any
	if err := json.Unmarshal(base, &reordered); err != nil {
		t.Fatal(err)
	}
	rules := reordered["rules"].([]any)
	rules[0], rules[2] = rules[2], rules[0]
	changedOrder, err := json.Marshal(reordered)
	if err != nil {
		t.Fatal(err)
	}
	orderedPack, err := Parse(changedOrder)
	if err != nil {
		t.Fatal(err)
	}
	decimalPack, err := Parse([]byte(strings.Replace(string(base), `"value": "99.9"`, `"value": "999/10"`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if pack.Hash() != orderedPack.Hash() || pack.Hash() != decimalPack.Hash() {
		t.Fatalf("canonical hashes differ: %s %s %s", pack.Hash(), orderedPack.Hash(), decimalPack.Hash())
	}
}

func TestManifestHashCanonicalizesAuthoritySourceOrder(t *testing.T) {
	makePack := func(reverse bool) *Pack {
		raw := mutateDemoManifest(t, func(root map[string]any) {
			authorities := []any{
				map[string]any{"id": "authority-a", "version": "1", "kind": "contractual"},
				map[string]any{"id": "authority-z", "version": "2", "kind": "normative"},
			}
			if reverse {
				authorities[0], authorities[1] = authorities[1], authorities[0]
			}
			ruleMap(root, "ENT-TECH-BW-001")["authority_sources"] = authorities
		})
		pack, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return pack
	}
	first, second := makePack(false), makePack(true)
	if first.Hash() != second.Hash() {
		t.Fatalf("authority order changed canonical hash: %s != %s", first.Hash(), second.Hash())
	}
	firstSnapshot, err := first.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	secondSnapshot, err := second.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if string(firstSnapshot) != string(secondSnapshot) {
		t.Fatal("equivalent authority ordering produced different normalized snapshots")
	}
}

func TestLifecycleIsHashBoundAndManifestCopyIsDetached(t *testing.T) {
	draft := demoDraft(t)
	if _, err := runRequest(t, draft, demoRequest(t)); !errors.Is(err, ErrNotPublished) {
		t.Fatalf("draft execution error %v", err)
	}
	if _, err := draft.Approve("test-approver", draft.Hash()); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("draft approval error %v", err)
	}
	if _, err := draft.Publish(); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("draft publish error %v", err)
	}
	validated, err := draft.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if draft.Manifest().Lifecycle.State != StateDraft || validated.Manifest().Lifecycle.State != StateValidated {
		t.Fatal("Validate mutated its receiver")
	}
	if _, err := validated.Approve("test-approver", strings.Repeat("0", 64)); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("stale approval hash: %v", err)
	}
	if _, err := validated.Approve("", validated.Hash()); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("invalid approval actor error %v", err)
	}
	approved, err := validated.Approve("test-approver", validated.Hash())
	if err != nil {
		t.Fatal(err)
	}
	published, err := approved.Publish()
	if err != nil {
		t.Fatal(err)
	}
	if published.Manifest().Lifecycle.State != StatePublished || approved.Manifest().Lifecycle.State != StateApproved {
		t.Fatal("Publish mutated its receiver")
	}
	manifest := published.Manifest()
	manifest.Rules[0].Threshold.Value = "999"
	manifest.Rules[0].RequiredFact.SourceRoles[0] = "forged"
	manifest.Lifecycle.Approval.ActorID = "forged"
	if published.Manifest().Rules[0].Threshold.Value == "999" || published.Manifest().Rules[0].RequiredFact.SourceRoles[0] == "forged" || published.Manifest().Lifecycle.Approval.ActorID == "forged" {
		t.Fatal("manifest exposed mutable pack state")
	}
	snapshot, err := published.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := Parse(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Hash() != published.Hash() || reloaded.Manifest().Lifecycle.State != StatePublished {
		t.Fatal("published lifecycle did not round trip")
	}
	forged := strings.Replace(string(snapshot), `"value":"100"`, `"value":"101"`, 1)
	if _, err := Parse([]byte(forged)); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("tampered published manifest accepted: %v", err)
	}
	if _, err := published.Validate(); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("published pack regressed to validated: %v", err)
	}
}

func TestRegistryRetainsVersionsAcrossSnapshotReloadAndRejectsRollback(t *testing.T) {
	v1 := demoPublished(t)
	v2Raw := strings.Replace(string(fixture(t, "demo.json")), `"version": "1"`, `"version": "2"`, 1)
	v2Raw = strings.Replace(v2Raw, `"value": "100"`, `"value": "110"`, 1)
	v2Draft, err := Parse([]byte(v2Raw))
	if err != nil {
		t.Fatal(err)
	}
	v2 := publishDraft(t, v2Draft)
	registry := NewRegistry()
	if err := registry.Publish(v2); err != nil {
		t.Fatal(err)
	}
	if err := registry.Publish(v1); !errors.Is(err, ErrVersionRollback) {
		t.Fatalf("older version publish: %v", err)
	}

	other := NewRegistry()
	if err := other.Publish(v1); err != nil {
		t.Fatal(err)
	}
	if err := other.Publish(v2); err != nil {
		t.Fatal(err)
	}
	snapshot, err := other.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	reloaded := NewRegistry()
	if err := reloaded.Reload(snapshot); err != nil {
		t.Fatal(err)
	}
	latest, ok := reloaded.Latest("tenant-demo", "line-demo")
	if !ok || latest.Manifest().Version != "2" {
		t.Fatalf("latest after reload: %+v %v", latest, ok)
	}
	if got, ok := reloaded.Lookup("tenant-demo", "line-demo", "1"); !ok || got.Hash() != v1.Hash() {
		t.Fatal("historical v1 was not retained")
	}
	if got, ok := reloaded.Lookup("tenant-demo", "line-demo", "3"); ok || got != nil {
		t.Fatalf("unknown version lookup returned a release: %+v %v", got, ok)
	}
	if err := reloaded.Reload(fixture(t, "published-demo.json")); err == nil {
		t.Fatal("registry accepted a single-pack file as registry snapshot")
	}
	oldSnapshot := registrySnapshot{SchemaVersion: RegistrySchema, Packs: []json.RawMessage{fixture(t, "published-demo.json")}}
	oldBytes, err := json.Marshal(oldSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Reload(oldBytes); !errors.Is(err, ErrVersionRollback) {
		t.Fatalf("reload dropped published v2: %v", err)
	}
	current, ok := other.Latest("tenant-demo", "line-demo")
	if !ok || current.Hash() != v2.Hash() {
		t.Fatal("failed rollback attempt changed registry state")
	}
}

func TestRegistryRejectsSameVersionContentOrReleaseReuse(t *testing.T) {
	v1 := demoPublished(t)
	changedRaw := strings.Replace(string(fixture(t, "demo.json")), `"value": "100"`, `"value": "101"`, 1)
	changedDraft, err := Parse([]byte(changedRaw))
	if err != nil {
		t.Fatal(err)
	}
	changed := publishDraft(t, changedDraft)
	registry := NewRegistry()
	if err := registry.Publish(v1); err != nil {
		t.Fatal(err)
	}
	if err := registry.Publish(changed); !errors.Is(err, ErrVersionReuse) {
		t.Fatalf("same version changed content: %v", err)
	}
	otherApproval := publishDraft(t, demoDraft(t))
	registry = NewRegistry()
	if err := registry.Publish(v1); err != nil {
		t.Fatal(err)
	}
	if err := registry.Publish(otherApproval); !errors.Is(err, ErrVersionReuse) {
		t.Fatalf("same content with altered release attestation: %v", err)
	}

}

func TestExecuteAuthorizationTenantScopeAndEffectiveWindow(t *testing.T) {
	pack, request := demoPublished(t), demoRequest(t)
	request.Principal.Grants[0].Role = "viewer"
	request.Sources[0].Digest = "forged"
	if _, err := runRequest(t, pack, request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unauthorized request reached evidence checks: %v", err)
	}
	request = demoRequest(t)
	request.Principal.Grants[0].ExpiresAt = time.Now().Add(-time.Second)
	if _, err := runRequest(t, pack, request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired grant accepted: %v", err)
	}
	request = demoRequest(t)
	request.Resource.TenantID = "tenant-other"
	if _, err := runRequest(t, pack, request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("principal/resource tenant mismatch: %v", err)
	}
	request = demoRequest(t)
	request.Principal.TenantID, request.Resource.TenantID = "tenant-other", "tenant-other"
	if _, err := runRequest(t, pack, request); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("pack tenant mismatch: %v", err)
	}
	request = demoRequest(t)
	request.At = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := runRequest(t, pack, request); !errors.Is(err, ErrNotEffective) {
		t.Fatalf("exclusive effective_until ignored: %v", err)
	}
	request = demoRequest(t)
	request.Scenario = "other-scenario"
	report, err := runRequest(t, pack, request)
	if err != nil || len(report.Results) != 0 {
		t.Fatalf("out-of-scope rules executed: %+v %v", report, err)
	}
	request = demoRequest(t)
	request.At = time.Time{}
	if _, err := runRequest(t, pack, request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("zero business time accepted: %v", err)
	}
	request = demoRequest(t)
	request.BusinessLine = ""
	if _, err := runRequest(t, pack, request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing business line error %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pack.Execute(ctx, demoRequest(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled execution error %v", err)
	}
	if _, err := pack.Execute(nil, demoRequest(t)); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil context error %v", err)
	}
}

func TestMissingAmbiguousAndUnboundFactsReturnInsufficient(t *testing.T) {
	pack := demoPublished(t)
	request := demoRequest(t)
	filtered := make([]Fact, 0, len(request.Facts))
	for _, fact := range request.Facts {
		if fact.RuleID != "ENT-TECH-BW-001" {
			filtered = append(filtered, fact)
		}
	}
	request.Facts = filtered
	report, err := runRequest(t, pack, request)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultFor(t, report, "ENT-TECH-BW-001").Relation; got != RelationInsufficient {
		t.Fatalf("missing mandatory fact relation %s", got)
	}

	request = demoRequest(t)
	for _, fact := range request.Facts {
		if fact.RuleID == "ENT-TECH-BW-001" {
			duplicate := fact
			duplicate.ID = "fact-bandwidth-second"
			request.Facts = append(request.Facts, duplicate)
			break
		}
	}
	report, err = runRequest(t, pack, request)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultFor(t, report, "ENT-TECH-BW-001").ReasonCode; got != "required_fact_ambiguous" {
		t.Fatalf("ambiguous fact reason %q", got)
	}

	request = demoRequest(t)
	for i := range request.Facts {
		if request.Facts[i].RuleID == "ENT-TECH-BW-001" {
			request.Facts[i].Citations = nil
		}
	}
	report, err = runRequest(t, pack, request)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultFor(t, report, "ENT-TECH-BW-001").Relation; got != RelationInsufficient {
		t.Fatalf("unbound fact relation %s", got)
	}

	request = demoRequest(t)
	for i := range request.Facts {
		if request.Facts[i].RuleID == "ENT-TECH-BW-001" {
			request.Facts[i].ObjectRef = "wrong-line"
		}
	}
	report, err = runRequest(t, pack, request)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultFor(t, report, "ENT-TECH-BW-001").Relation; got != RelationInsufficient {
		t.Fatalf("wrong object relation %s", got)
	}
}

func TestEvidenceIdentityTamperingFailsAndUnicodeCodePointsBind(t *testing.T) {
	pack := demoPublished(t)
	cases := []struct {
		name   string
		mutate func(*Request)
	}{
		{"citation-digest", func(req *Request) {
			for i := range req.Facts {
				if req.Facts[i].RuleID == "ENT-TECH-BW-001" {
					req.Facts[i].Citations[0].SourceDigest = "bad"
				}
			}
		}},
		{"citation-case", func(req *Request) {
			for i := range req.Facts {
				if req.Facts[i].RuleID == "ENT-TECH-BW-001" {
					req.Facts[i].Citations[0].CaseID = "another-case"
				}
			}
		}},
		{"citation-quote", func(req *Request) {
			for i := range req.Facts {
				if req.Facts[i].RuleID == "ENT-TECH-BW-001" {
					req.Facts[i].Citations[0].Quote = "伪造引用"
				}
			}
		}},
		{"source-digest", func(req *Request) { req.Sources[0].Digest = "bad" }},
		{"source-case", func(req *Request) { req.Sources[0].CaseID = "another-case" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := demoRequest(t)
			test.mutate(&request)
			if _, err := runRequest(t, pack, request); !errors.Is(err, ErrIdentity) {
				t.Fatalf("tampered identity error %v", err)
			}
		})
	}
	request := demoRequest(t)
	fact := &request.Facts[1]
	quote := fact.Citations[0].Quote
	source := &request.Sources[0]
	prefix := "前言："
	source.Text = prefix + quote
	source.Digest = digestHex([]byte(source.Text))
	fact.Citations[0].SourceDigest = source.Digest
	fact.Citations[0].Start = len([]rune(prefix))
	fact.Citations[0].End = len([]rune(source.Text))
	report, err := runRequest(t, pack, request)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultFor(t, report, "ENT-TECH-BW-001").Relation; got != RelationConflict {
		t.Fatalf("code-point citation relation %s", got)
	}
}

func TestNumericEvidenceRejectsSubstringsRangesNegationAndRatioSpoofs(t *testing.T) {
	pack := demoPublished(t)
	cases := []struct {
		name, factID, text, value, unit string
		want                            Relation
	}{
		{"bandwidth-150-is-not-50", "fact-bandwidth", "专线甲最低带宽承诺为150 Mbit/s。", "50", "Mbit/s", RelationInsufficient},
		{"bandwidth-range", "fact-bandwidth", "专线甲最低带宽承诺为50至100 Mbit/s。", "100", "Mbit/s", RelationInsufficient},
		{"bandwidth-english-range", "fact-bandwidth", "专线甲 bandwidth between 50 and 100 Mbit/s", "100", "Mbit/s", RelationInsufficient},
		{"bandwidth-negated", "fact-bandwidth", "专线甲最低带宽未承诺50 Mbit/s。", "50", "Mbit/s", RelationInsufficient},
		{"monthly-competing-values", "fact-sla", "专线甲自然月可用率为99.5%或99.9%，时区Asia/Shanghai，日历Gregorian，故障口径all_service_unavailability，无排除项。", "99.5", "percent", RelationInsufficient},
		{"advance-140point4", "fact-advance", "专线甲合同总额为预付款计算基础，预付款比例为140.4。", "0.4", "ratio", RelationInsufficient},
		{"advance-0point4-percent", "fact-advance", "专线甲合同总额为预付款计算基础，预付款比例为0.4%。", "0.4", "ratio", RelationInsufficient},
		{"advance-no-ratio-marker", "fact-advance", "专线甲合同总额为预付款计算基础，约定金额为0.4。", "0.4", "ratio", RelationInsufficient},
		{"advance-range", "fact-advance", "专线甲合同总额为预付款计算基础，预付款比例为0.3至0.4。", "0.4", "ratio", RelationInsufficient},
		{"advance-percent-normalizes", "fact-advance", "专线甲合同总额为预付款计算基础，预付款比例为40%。", "0.4", "ratio", RelationConflict},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := demoRequest(t)
			replaceEvidence(t, &request, test.factID, test.text, test.value, test.unit, "专线甲")
			report, err := runRequest(t, pack, request)
			if err != nil {
				t.Fatal(err)
			}
			var ruleID string
			switch test.factID {
			case "fact-bandwidth":
				ruleID = "ENT-TECH-BW-001"
			case "fact-sla":
				ruleID = "ENT-TECH-SLA-001"
			default:
				ruleID = "ENT-COM-ADV-001"
			}
			if got := resultFor(t, report, ruleID).Relation; got != test.want {
				t.Fatalf("relation %s, want %s", got, test.want)
			}
		})
	}
}

func TestMonthlySLARequiresMatchedMetadataAndCompleteExclusions(t *testing.T) {
	pack := demoPublished(t)
	request := demoRequest(t)
	for i := range request.Facts {
		if request.Facts[i].RuleID == "ENT-TECH-SLA-001" {
			request.Facts[i].TimeZone = "UTC"
		}
	}
	report, err := runRequest(t, pack, request)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultFor(t, report, "ENT-TECH-SLA-001").Relation; got != RelationInsufficient {
		t.Fatalf("timezone mismatch relation %s", got)
	}

	request = demoRequest(t)
	replaceEvidence(t, &request, "fact-sla", "专线甲自然月可用率为99.5%，日历Gregorian，故障口径all_service_unavailability，无排除项。", "99.5", "percent", "专线甲")
	report, err = runRequest(t, pack, request)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultFor(t, report, "ENT-TECH-SLA-001").Relation; got != RelationInsufficient {
		t.Fatalf("missing evidence timezone relation %s", got)
	}

	request = demoRequest(t)
	replaceEvidence(t, &request, "fact-sla", "专线甲自然月可用率为99.5%，时区Asia/Shanghai，日历Gregorian，故障口径all_service_unavailability，无排除项，但计划维护除外。", "99.5", "percent", "专线甲")
	report, err = runRequest(t, pack, request)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultFor(t, report, "ENT-TECH-SLA-001").Relation; got != RelationInsufficient {
		t.Fatalf("contradictory exclusions relation %s", got)
	}

	for _, scenario := range []struct {
		name, list string
		want       Relation
	}{
		{"complete-list", "排除项：planned_maintenance、public_holiday", RelationConflict},
		{"missing-item", "排除项：planned_maintenance", RelationInsufficient},
		{"extra-item", "排除项：planned_maintenance、public_holiday、regional_event", RelationInsufficient},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			custom := demoDraft(t).Manifest()
			for i := range custom.Rules {
				if custom.Rules[i].Comparator == ComparatorMonthlySLA {
					custom.Rules[i].RequiredFact.Exclusions = []string{"planned_maintenance", "public_holiday"}
				}
			}
			raw, err := json.Marshal(custom)
			if err != nil {
				t.Fatal(err)
			}
			changed, err := Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			published := publishDraft(t, changed)
			request := demoRequest(t)
			for i := range request.Facts {
				if request.Facts[i].RuleID == "ENT-TECH-SLA-001" {
					request.Facts[i].Exclusions = []string{"planned_maintenance", "public_holiday"}
				}
			}
			text := "专线甲自然月可用率为99.5%，时区Asia/Shanghai，日历Gregorian，故障口径all_service_unavailability，" + scenario.list + "。"
			replaceEvidence(t, &request, "fact-sla", text, "99.5", "percent", "专线甲")
			report, err := runRequest(t, published, request)
			if err != nil {
				t.Fatal(err)
			}
			if got := resultFor(t, report, "ENT-TECH-SLA-001").Relation; got != scenario.want {
				t.Fatalf("exclusion relation %s, want %s", got, scenario.want)
			}
		})
	}
}

func TestApplicabilityNeedsSpecificObjectAndRuleDimension(t *testing.T) {
	pack := demoPublished(t)
	for _, scenario := range []struct {
		name, quote, object, dimension string
		want                           Relation
	}{
		{"generic-denial", "专线甲服务不适用。", "专线甲", "minimum_bandwidth", RelationInsufficient},
		{"different-object", "专线乙带宽不适用。", "专线甲", "minimum_bandwidth", RelationInsufficient},
		{"wrong-dimension", "专线甲带宽不适用。", "专线甲", "monthly_availability", RelationInsufficient},
		{"specific-denial", "专线甲带宽不适用。", "专线甲", "minimum_bandwidth", RelationNotApplicable},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := demoRequest(t)
			replaceEvidence(t, &request, "fact-bandwidth", scenario.quote, "", "Mbit/s", scenario.object)
			for i := range request.Facts {
				if request.Facts[i].ID == "fact-bandwidth" {
					request.Facts[i].Dimension = scenario.dimension
					inapplicable := false
					request.Facts[i].Applicable = &inapplicable
				}
			}
			report, err := runRequest(t, pack, request)
			if err != nil {
				t.Fatal(err)
			}
			if got := resultFor(t, report, "ENT-TECH-BW-001").Relation; got != scenario.want {
				t.Fatalf("applicability relation %s, want %s", got, scenario.want)
			}
		})
	}
}

func TestExactRationalComparatorBoundaries(t *testing.T) {
	pack := demoPublished(t)
	cases := []struct {
		name, factID, text, value, unit, ruleID string
		want                                    Relation
	}{
		{"bandwidth-equal-after-unit-conversion", "fact-bandwidth", "专线甲最低带宽承诺为0.1 Gbit/s。", "0.1", "Gbit/s", "ENT-TECH-BW-001", RelationCompatible},
		{"bandwidth-just-below", "fact-bandwidth", "专线甲最低带宽承诺为0.099999999999999999 Gbit/s。", "0.099999999999999999", "Gbit/s", "ENT-TECH-BW-001", RelationConflict},
		{"sla-equal", "fact-sla", "专线甲自然月可用率为99.9%，时区Asia/Shanghai，日历Gregorian，故障口径all_service_unavailability，无排除项。", "99.9", "percent", "ENT-TECH-SLA-001", RelationCompatible},
		{"sla-just-below", "fact-sla", "专线甲自然月可用率为99.899999999999999%，时区Asia/Shanghai，日历Gregorian，故障口径all_service_unavailability，无排除项。", "99.899999999999999", "percent", "ENT-TECH-SLA-001", RelationConflict},
		{"ratio-equal", "fact-advance", "专线甲合同总额为预付款计算基础，预付款比例为1/3。", "1/3", "ratio", "ENT-COM-ADV-001", RelationCompatible},
		{"ratio-just-above", "fact-advance", "专线甲合同总额为预付款计算基础，预付款比例为0.3333333333333334。", "0.3333333333333334", "ratio", "ENT-COM-ADV-001", RelationConflict},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := demoRequest(t)
			replaceEvidence(t, &request, test.factID, test.text, test.value, test.unit, "专线甲")
			report, err := runRequest(t, pack, request)
			if err != nil {
				t.Fatal(err)
			}
			if got := resultFor(t, report, test.ruleID).Relation; got != test.want {
				t.Fatalf("relation %s, want %s", got, test.want)
			}
		})
	}
}
