//go:build browser

package e2e_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/store"
)

func TestBrowserTelecomC1BFullJourney(t *testing.T) {
	chrome := chromiumBinary(t)
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("Bun unavailable: install Bun 1.4.3 and dependencies in web")
	}

	dir := t.TempDir()
	st, err := store.OpenTest(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	st.UseModels(e2eModels{})
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	// The frozen C1-B dataset carries rule identities, so the governed telecom
	// case can only be accepted when this test publishes the matching pack and
	// its bound policy. This mirrors the business HTTP acceptance fixture.
	publishTelecomGovernance(t, st, admin)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	webPort := freePort(t)
	webURL := "http://127.0.0.1:" + webPort
	api := &http.Server{Handler: httpapi.Handler(st, httpapi.Options{CORSOrigins: []string{webURL}})}
	go func() { _ = api.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = api.Shutdown(ctx)
	})

	webRoot := filepath.Clean(filepath.Join("..", "web"))
	vite := exec.Command(bun, "run", "dev", "--", "--port", webPort, "--strictPort", "--host", "127.0.0.1")
	vite.Dir = webRoot
	vite.Env = append(os.Environ(), "VITE_DEV_API_PROXY=http://"+listener.Addr().String())
	vite.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := vite.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopGroup(vite) })
	waitHTTP(t, webURL+"/")

	dataset, err := filepath.Abs(filepath.Join("..", "testdata", "telecom-c1b-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	script := exec.CommandContext(ctx, bun, "e2e/c1b-journey.ts")
	script.Dir = webRoot
	script.Env = append(os.Environ(),
		"CHROMIUM_BIN="+chrome,
		"E2E_WEB="+webURL,
		"E2E_TELECOM_DATASET="+dataset,
		"E2E_TELECOM_SCENARIO=conflict",
	)
	out, err := script.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		if ctx.Err() != nil {
			t.Fatalf("C1-B Playwright journey timed out after 300s: %s", out)
		}
		t.Fatalf("C1-B Playwright journey failed: %v\n%s", err, out)
	}
}

// publishTelecomGovernance makes the frozen telecom dataset reviewable: a
// published rule pack alone is not enough because its mandatory
// internal/synthetic authorities must also resolve to one exact published
// policy bound to that pack snapshot.
func publishTelecomGovernance(t *testing.T, st *store.Store, admin accounts.User) {
	t.Helper()
	tenant := accounts.Tenant(admin)
	scope := map[string]any{"business_line": "ENT", "scenario": "telecom_dedicated_line", "contract_type": "service"}
	authority := []any{map[string]any{"id": "telecom-c1b-policy", "version": "1", "kind": "synthetic_demo"}}
	manifest := map[string]any{
		"schema_version":  "rulepack/v1",
		"id":              "telecom-governed",
		"version":         "1",
		"tenant_id":       tenant,
		"effective_from":  "2020-01-01T00:00:00Z",
		"effective_until": "2100-01-01T00:00:00Z",
		"rules": []any{
			map[string]any{
				"rule_id": "ENT-TECH-BW-001", "version": "1", "domain": "technical",
				"capability_id": "dedicated_line.minimum_bandwidth", "mandatory": true,
				"business_scope": scope, "tenant_id": tenant, "comparator": "minimum_bandwidth",
				"threshold":         map[string]any{"value": "100", "unit": "Mbit/s"},
				"required_fact":     map[string]any{"dimension": "minimum_bandwidth", "source_roles": []string{"draft_contract"}, "object_required": true, "unit_family": "bandwidth_decimal"},
				"authority_sources": authority,
			},
			map[string]any{
				"rule_id": "ENT-TECH-SLA-001", "version": "1", "domain": "technical",
				"capability_id": "dedicated_line.monthly_availability", "mandatory": true,
				"business_scope": scope, "tenant_id": tenant, "comparator": "monthly_sla",
				"threshold": map[string]any{"value": "99.9", "unit": "percent"},
				"required_fact": map[string]any{"dimension": "monthly_availability", "source_roles": []string{"draft_contract"}, "object_required": true,
					"unit_family": "percentage", "measurement_window": "calendar_month", "time_zone": "Asia/Shanghai",
					"calendar": "gregorian", "outage_definition": "all_service_unavailability", "exclusions": []string{}},
				"authority_sources": authority,
			},
			map[string]any{
				"rule_id": "ENT-COM-ADV-001", "version": "1", "domain": "commercial",
				"capability_id": "payment.advance_ratio.ceiling", "mandatory": true,
				"business_scope": scope, "tenant_id": tenant, "comparator": "advance_payment_ceiling",
				"threshold":         map[string]any{"value": "1/3", "unit": "ratio", "basis": "contract_total"},
				"required_fact":     map[string]any{"dimension": "advance_payment_ratio", "source_roles": []string{"draft_contract"}, "object_required": true, "unit_family": "ratio", "basis": "contract_total"},
				"authority_sources": authority,
			},
		},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.CreateRulePack(admin, raw)
	if err != nil {
		t.Fatalf("create telecom rule pack: %v", err)
	}
	validated, err := st.ValidateRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedRevision: created.Pack.Revision,
	})
	if err != nil {
		t.Fatalf("validate telecom rule pack: %v", err)
	}
	approved, err := st.ApproveRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedRevision: validated.Pack.Revision,
		ExpectedContentHash: validated.Pack.ContentHash,
	})
	if err != nil {
		t.Fatalf("approve telecom rule pack: %v", err)
	}
	published, err := st.PublishRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedRevision: approved.Pack.Revision,
		ExpectedPointerRevision: approved.Pack.ActivePointerRevision,
	})
	if err != nil {
		t.Fatalf("publish telecom rule pack: %v", err)
	}
	if published.Pack.Status != store.GovernancePublished {
		t.Fatalf("telecom rule pack did not publish: %+v", published.Pack)
	}

	var body string
	ruleIDs := make([]string, 0, 3)
	spans := make([]store.KnowledgeSourceSpan, 0, 3)
	for _, rule := range []struct{ id, quote, suffix string }{
		{"ENT-TECH-BW-001", "带宽不低于100 Mbit/s", ""},
		{"ENT-TECH-SLA-001", "月可用率不低于99.9%", "，统计周期为自然月，时区Asia/Shanghai，公历gregorian，故障口径all_service_unavailability，无排除项"},
		{"ENT-COM-ADV-001", "预付款比例不超过1/3", "，预付款以合同总额为基础"},
	} {
		start := len([]rune(body))
		body += rule.quote + rule.suffix + "。"
		ruleIDs = append(ruleIDs, rule.id)
		spans = append(spans, store.KnowledgeSourceSpan{RuleID: rule.id, Start: start, End: start + len([]rune(rule.quote)), Quote: rule.quote})
	}
	knowledge, err := st.CreateGovernedKnowledge(admin, store.CreateGovernedKnowledgeInput{
		Content: store.GovernedKnowledgeContent{
			KnowledgeID: "telecom-c1b-policy", Version: "1", Kind: "policy", Title: "专线合成治理制度",
			Body: body, Summary: "核验带宽、可用率和预付款比例", SourceType: "synthetic_policy",
			SourceReference: "合成浏览器验收制度",
			Scope:           store.KnowledgeScope{BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service"},
			ValidFrom:       "2020-01-01T00:00:00Z", ValidUntil: "2100-01-01T00:00:00Z",
			Binding: store.PolicyRuleBinding{
				PackID: "telecom-governed", PackVersion: "1",
				PackContentHash: published.Pack.ContentHash, PackSnapshotDigest: published.Pack.SnapshotSHA256,
				CompilerRef: "governed-rulepack/v1", RuleIDs: ruleIDs, SourceSpans: spans,
				CandidateDiff: "规则差异已审阅", ValidationEvidence: "规则包校验通过",
			},
		},
		IdempotencyKey: "c1b-browser-policy-create",
	})
	if err != nil {
		t.Fatalf("create telecom policy: %v", err)
	}
	current := knowledge
	for _, action := range []string{"submit", "validate", "approve", "publish"} {
		current, err = st.GovernedKnowledgeCommand(admin, store.GovernedKnowledgeCommandInput{
			KnowledgeID: "telecom-c1b-policy", VersionID: knowledge.Ref.VersionID, Action: action,
			ExpectedRevision: current.Revision, ExpectedContentSHA256: knowledge.Ref.ContentSHA256,
			ExpectedPointerRevision: current.PointerRevision, Reason: "已核对合成制度依据",
			IdempotencyKey: "c1b-browser-policy-" + action,
		})
		if err != nil {
			t.Fatalf("%s telecom policy: %v", action, err)
		}
	}
	if current.Status != "published" || !current.Active {
		t.Fatalf("telecom policy did not publish: %+v", current)
	}
}
