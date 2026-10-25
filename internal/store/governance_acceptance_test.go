package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/rulepack"
	"contractaudit/internal/telecom"
)

// TestAcceptanceFreezesGovernedPackIdentity proves GOV-03: when an applicable
// active published pack exists, acceptance freezes its exact bytes, content
// hash, snapshot digest, ID/version and compiler marker into the run snapshot
// and the accepted attempt, and a retry reuses those exact bytes.
func TestAcceptanceFreezesGovernedPackIdentity(t *testing.T) {
	st, admin := openExecutionStore(t)
	st.UseModels(&frozenOCRModel{})
	caseID, err := st.CreateCaseWithBusiness(admin, "治理冻结身份", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	for role, body := range map[string]string{
		"procurement":    "采购文件：电路 LINE-001 最低保障带宽 100 Mbit/s，合同总额 100 万元。",
		"draft_contract": "拟签合同：电路 LINE-001 最低保障带宽 100 Mbit/s，合同总额 100 万元。",
	} {
		if _, err := st.AddDocument(context.Background(), admin, caseID, role, role+".txt", "text/plain", []byte(body)); err != nil {
			t.Fatal(err)
		}
	}

	published := publishGovernedPackStore(t, st, admin, "1", "100")

	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatalf("start review with a governed pack: %v", err)
	}
	snapshot := snapshotState(st).snapshotOf(runID)
	if snapshot.RulePackID != "telecom-governed" || snapshot.RulePackVersion != "1" {
		t.Fatalf("run snapshot did not freeze the governed pack identity: %+v", snapshot)
	}
	if snapshot.RulePackCompilerRef != governedCompilerMarker {
		t.Fatalf("run snapshot compiler marker = %q, want %q", snapshot.RulePackCompilerRef, governedCompilerMarker)
	}
	if snapshot.RulePackHash != published.Pack.ContentHash {
		t.Fatalf("run snapshot did not freeze the exact content hash: %+v", snapshot)
	}
	// The frozen digest is the digest of the exact bytes the attempt carries.
	if snapshot.RulePackDigest != sha256HexOf(snapshot.RulePackSnapshot) {
		t.Fatalf("run snapshot digest does not cover its frozen bytes: %+v", snapshot)
	}
	if !bytes.Equal(snapshot.RulePackSnapshot, published.Manifest) {
		t.Fatal("run snapshot did not freeze the exact published bytes")
	}

	workspace, err := st.Workspace(admin, caseID, runID, "")
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	if workspace.Execution == nil || workspace.Execution.Attempt == nil {
		t.Fatal("accepted attempt missing")
	}
	attempt := workspace.Execution.Attempt
	if attempt.RulePackID != "telecom-governed" || attempt.RulePackVersion != "1" {
		t.Fatalf("attempt did not freeze the governed pack identity: %+v", attempt)
	}
	if attempt.RulePackHash != published.Pack.ContentHash ||
		attempt.RulePackSnapshotDigest != sha256HexOf(attempt.RulePackSnapshot) {
		t.Fatalf("attempt did not freeze the exact content identity: %+v", attempt)
	}
	if !bytes.Equal(attempt.RulePackSnapshot, published.Manifest) {
		t.Fatal("attempt did not freeze the exact published bytes")
	}
	caseItem, ok := snapshotState(st).caseByID(caseID)
	if !ok {
		t.Fatal("case missing after acceptance")
	}
	parsedPack, err := ParseFrozenGovernedRulePack(*attempt, caseItem)
	if err != nil {
		t.Fatalf("parse acceptance-frozen governed pack: %v", err)
	}
	if parsedPack.Hash() != attempt.RulePackHash {
		t.Fatal("frozen pack loader returned a different content hash")
	}
	for name, mutate := range map[string]func(*Attempt){
		"compiler mismatch":        func(candidate *Attempt) { candidate.RulePackCompilerRef = telecom.CompilerRef },
		"missing compiler":         func(candidate *Attempt) { candidate.RulePackCompilerRef = "" },
		"unknown compiler":         func(candidate *Attempt) { candidate.RulePackCompilerRef = "governed-rulepack/v2" },
		"pack ID mismatch":         func(candidate *Attempt) { candidate.RulePackID = "another-pack" },
		"version mismatch":         func(candidate *Attempt) { candidate.RulePackVersion = "2" },
		"missing pointer revision": func(candidate *Attempt) { candidate.RulePackPointerRevision = 0 },
		"content hash mismatch": func(candidate *Attempt) {
			candidate.RulePackHash = "0000000000000000000000000000000000000000000000000000000000000000"
		},
		"raw bytes mismatch": func(candidate *Attempt) {
			candidate.RulePackSnapshot = append(append([]byte(nil), candidate.RulePackSnapshot...), '\n')
		},
		"byte digest mismatch": func(candidate *Attempt) {
			candidate.RulePackSnapshotDigest = "0000000000000000000000000000000000000000000000000000000000000000"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := *attempt
			mutate(&candidate)
			if _, err := ParseFrozenGovernedRulePack(candidate, caseItem); err == nil {
				t.Fatal("frozen pack loader accepted a corrupted identity")
			}
		})
	}
	for name, legacy := range map[string]Attempt{
		"accepted but not compiled":   {},
		"synthetic baseline compiler": {RulePackCompilerRef: telecom.CompilerRef},
	} {
		t.Run(name, func(t *testing.T) {
			pack, err := ParseFrozenGovernedRulePack(legacy, caseItem)
			if err != nil || pack != nil {
				t.Fatalf("synthetic legacy parse = %v, %v; want nil, nil", pack, err)
			}
		})
	}
	for name, corrupt := range map[string]Attempt{
		"partial identity":                 {RulePackID: attempt.RulePackID},
		"synthetic with governed metadata": {RulePackCompilerRef: telecom.CompilerRef, RulePackHash: attempt.RulePackHash},
		"unknown compiler":                 {RulePackCompilerRef: "governed-rulepack/v2"},
	} {
		t.Run(name, func(t *testing.T) {
			pack, err := ParseFrozenGovernedRulePack(corrupt, caseItem)
			if err == nil || pack != nil {
				t.Fatalf("corrupt legacy identity = %v, %v; want nil and error", pack, err)
			}
		})
	}
	foreignCase := caseItem
	foreignCase.TenantID = "tenant-other"
	if _, err := ParseFrozenGovernedRulePack(*attempt, foreignCase); err == nil {
		t.Fatal("frozen pack loader accepted bytes for another tenant")
	}
	wrongScope := caseItem
	wrongScope.Scenario = "another_scenario"
	if _, err := ParseFrozenGovernedRulePack(*attempt, wrongScope); err == nil {
		t.Fatal("frozen pack loader accepted bytes outside the case scope")
	}

	wrongContractType := caseItem
	wrongContractType.ContractType = "another_contract_type"
	if _, err := ParseFrozenGovernedRulePack(*attempt, wrongContractType); err == nil {
		t.Fatal("frozen pack loader accepted bytes outside the contract type")
	}
	// Even a valid published lifecycle and matching digests cannot replace the
	// three required telecom capabilities.
	draft, err := rulepack.Parse(governedPackManifest(t, caseItem.TenantID, "2", "100"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := draft.Manifest()
	filteredRules := make([]rulepack.Rule, 0, len(manifest.Rules)-1)
	for _, rule := range manifest.Rules {
		if rule.RuleID != "ENT-COM-ADV-001" {
			filteredRules = append(filteredRules, rule)
		}
	}
	manifest.Rules = filteredRules
	incompleteDraftBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	draft, err = rulepack.Parse(incompleteDraftBytes)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := draft.Validate()
	if err != nil {
		t.Fatal(err)
	}
	approved, err := validated.Approve("test-admin", validated.Hash())
	if err != nil {
		t.Fatal(err)
	}
	publishedWithoutMandatory, err := approved.Publish()
	if err != nil {
		t.Fatal(err)
	}
	incompleteBytes, err := publishedWithoutMandatory.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	incompleteAttempt := Attempt{
		RulePackCompilerRef: governedCompilerMarker, RulePackID: manifest.ID, RulePackVersion: manifest.Version,
		RulePackPointerRevision: 1, RulePackHash: publishedWithoutMandatory.Hash(),
		RulePackSnapshotDigest: sha256HexOf(incompleteBytes), RulePackSnapshot: incompleteBytes,
	}
	if _, err := ParseFrozenGovernedRulePack(incompleteAttempt, caseItem); err == nil {
		t.Fatal("frozen pack loader accepted a published pack missing a mandatory telecom rule")
	}
	// Retrying an active attempt is idempotent and must not create a new attempt,
	// so the frozen governed identity cannot change under the live run.
	if err := st.Retry(context.Background(), admin, caseID, runID); err != nil {
		t.Fatalf("idempotent retry of an active run failed: %v", err)
	}
	if got := snapshotState(st).snapshotOf(runID); got.RulePackVersion != "1" || got.RulePackHash != published.Pack.ContentHash {
		t.Fatalf("idempotent retry changed the frozen governed identity: %+v", got)
	}
}

// TestAcceptanceKeepsSyntheticBaselineWhenNoPackApplies proves the baseline
// synthetic adapter still runs when no governed pack matches, so governance can
// never be required for the existing deterministic chain.
func TestAcceptanceKeepsSyntheticBaselineWhenNoPackApplies(t *testing.T) {
	st, admin := openExecutionStore(t)
	st.UseModels(&frozenOCRModel{})
	caseID, err := st.CreateCaseWithBusiness(admin, "无适用治理包", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	for role, body := range map[string]string{
		"procurement":    "采购文件：电路 LINE-001 最低保障带宽 100 Mbit/s。",
		"draft_contract": "拟签合同：电路 LINE-001 最低保障带宽 100 Mbit/s。",
	} {
		if _, err := st.AddDocument(context.Background(), admin, caseID, role, role+".txt", "text/plain", []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatalf("baseline start review: %v", err)
	}
	snapshot := snapshotState(st).snapshotOf(runID)
	if snapshot.RulePackID != "" || snapshot.RulePackVersion != "" || len(snapshot.RulePackSnapshot) != 0 {
		t.Fatalf("baseline run froze a governed pack: %+v", snapshot)
	}
	// The baseline path leaves the compiler identity empty at acceptance: the
	// existing synthetic compiler records its own identity when the attempt runs,
	// and acceptance must not pre-empt it.
	if snapshot.RulePackCompilerRef != "" {
		t.Fatalf("baseline acceptance pre-set a compiler ref %q", snapshot.RulePackCompilerRef)
	}
	workspace, err := st.Workspace(admin, caseID, runID, "")
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Execution == nil || workspace.Execution.Attempt == nil || workspace.Execution.Attempt.RulePackID != "" {
		t.Fatalf("baseline attempt froze a governed pack: %+v", workspace.Execution)
	}
}

// TestGovernedPackCannotRemoveMandatoryCapability proves governance cannot weaken
// the mandatory three-capability coverage: an applicable pack missing a mandatory
// rule fails acceptance closed.
func TestGovernedPackCannotRemoveMandatoryCapability(t *testing.T) {
	st, admin := openExecutionStore(t)
	caseID, err := st.CreateCaseWithBusiness(admin, "治理削弱反例", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	for role, body := range map[string]string{
		"procurement":    "采购文件：电路 LINE-001 最低保障带宽 100 Mbit/s。",
		"draft_contract": "拟签合同：电路 LINE-001 最低保障带宽 100 Mbit/s。",
	} {
		if _, err := st.AddDocument(context.Background(), admin, caseID, role, role+".txt", "text/plain", []byte(body)); err != nil {
			t.Fatal(err)
		}
	}

	// Publish a pack that only carries the bandwidth rule.
	manifest := governedPackManifest(t, accounts.Tenant(admin), "1", "100")
	var parsed map[string]any
	if err := json.Unmarshal(manifest, &parsed); err != nil {
		t.Fatal(err)
	}
	rules := parsed["rules"].([]any)
	parsed["rules"] = rules[:1]
	partial, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	publishGovernedPackStore(t, st, admin, "1", "100", partial)

	if _, err := st.StartReview(context.Background(), admin, caseID); err == nil {
		t.Fatal("acceptance used a governed pack that removed mandatory capabilities")
	}
}

// governedPackManifest builds a strict manifest for the store package tests.
func governedPackManifest(t *testing.T, tenantID, version, bandwidth string) []byte {
	t.Helper()
	manifest := map[string]any{
		"schema_version":  "rulepack/v1",
		"id":              "telecom-governed",
		"version":         version,
		"tenant_id":       tenantID,
		"effective_from":  "2020-01-01T00:00:00Z",
		"effective_until": "2100-01-01T00:00:00Z",
		"rules": []any{
			map[string]any{
				"rule_id": "ENT-TECH-BW-001", "version": "1", "domain": "technical",
				"capability_id": "dedicated_line.minimum_bandwidth", "mandatory": true,
				"business_scope": map[string]any{"business_line": "ENT", "scenario": "telecom_dedicated_line", "contract_type": "service"},
				"tenant_id":      tenantID, "comparator": "minimum_bandwidth",
				"threshold":         map[string]any{"value": bandwidth, "unit": "Mbit/s"},
				"required_fact":     map[string]any{"dimension": "minimum_bandwidth", "source_roles": []string{"draft_contract"}, "object_required": true, "unit_family": "bandwidth_decimal"},
				"authority_sources": []any{map[string]any{"id": "operator-policy", "version": "1", "kind": "internal_policy"}},
			},
			map[string]any{
				"rule_id": "ENT-TECH-SLA-001", "version": "1", "domain": "technical",
				"capability_id": "dedicated_line.monthly_availability", "mandatory": true,
				"business_scope": map[string]any{"business_line": "ENT", "scenario": "telecom_dedicated_line", "contract_type": "service"},
				"tenant_id":      tenantID, "comparator": "monthly_sla",
				"threshold": map[string]any{"value": "99.9", "unit": "percent"},
				"required_fact": map[string]any{"dimension": "monthly_availability", "source_roles": []string{"draft_contract"}, "object_required": true,
					"unit_family": "percentage", "measurement_window": "calendar_month", "time_zone": "Asia/Shanghai",
					"calendar": "gregorian", "outage_definition": "all_service_unavailability", "exclusions": []string{}},
				"authority_sources": []any{map[string]any{"id": "operator-policy", "version": "1", "kind": "internal_policy"}},
			},
			map[string]any{
				"rule_id": "ENT-COM-ADV-001", "version": "1", "domain": "commercial",
				"capability_id": "payment.advance_ratio.ceiling", "mandatory": true,
				"business_scope": map[string]any{"business_line": "ENT", "scenario": "telecom_dedicated_line", "contract_type": "service"},
				"tenant_id":      tenantID, "comparator": "advance_payment_ceiling",
				"threshold":         map[string]any{"value": "1/3", "unit": "ratio", "basis": "contract_total"},
				"required_fact":     map[string]any{"dimension": "advance_payment_ratio", "source_roles": []string{"draft_contract"}, "object_required": true, "unit_family": "ratio", "basis": "contract_total"},
				"authority_sources": []any{map[string]any{"id": "operator-policy", "version": "1", "kind": "internal_policy"}},
			},
		},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// publishGovernedPackStore drives the full lifecycle and returns the published
// detail. A caller-supplied manifest overrides the default content.
func publishGovernedPackStore(t *testing.T, st *Store, admin accounts.User, version, bandwidth string, override ...[]byte) RulePackDetail {
	t.Helper()
	manifest := governedPackManifest(t, accounts.Tenant(admin), version, bandwidth)
	if len(override) > 0 && len(override[0]) > 0 {
		manifest = override[0]
	}
	created, err := st.CreateRulePack(admin, manifest)
	if err != nil {
		t.Fatalf("create %s: %v", version, err)
	}
	validated, err := st.ValidateRulePackVersion(admin, GovernanceInput{
		PackID: "telecom-governed", Version: version, ExpectedRevision: created.Pack.Revision,
	})
	if err != nil {
		t.Fatalf("validate %s: %v", version, err)
	}
	approved, err := st.ApproveRulePackVersion(admin, GovernanceInput{
		PackID: "telecom-governed", Version: version, ExpectedRevision: validated.Pack.Revision,
		ExpectedContentHash: validated.Pack.ContentHash,
	})
	if err != nil {
		t.Fatalf("approve %s: %v", version, err)
	}
	published, err := st.PublishRulePackVersion(admin, GovernanceInput{
		PackID: "telecom-governed", Version: version, ExpectedRevision: approved.Pack.Revision,
		ExpectedPointerRevision: approved.Pack.ActivePointerRevision,
	})
	if err != nil {
		t.Fatalf("publish %s: %v", version, err)
	}
	if published.Pack.Status != GovernancePublished {
		t.Fatalf("publish did not reach published: %+v", published.Pack)
	}
	return published
}

var _ = rulepack.SchemaVersion

// sha256HexOf returns the lowercase SHA-256 hex of the given bytes.
func sha256HexOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// snapshotState returns an addressable copy of the store's in-memory state so
// the pointer-receiver accessors can be called from a test.
func snapshotState(st *Store) *state {
	snapshot := st.snapshot()
	return &snapshot
}

// TestAcceptanceFreezesFullPlannerPayload proves the approved planner payload —
// not only its hash — is frozen at acceptance, survives a reopen, and is copied
// to a retry attempt, so a worker never has to re-read process environment.
func TestAcceptanceFreezesFullPlannerPayload(t *testing.T) {
	st, admin := openExecutionStore(t)
	st.UseModels(&frozenOCRModel{})
	caseID, err := st.CreateCaseWithBusiness(admin, "规划器配置冻结", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	for role, body := range map[string]string{
		"procurement":    "采购文件：电路 LINE-001 最低保障带宽 100 Mbit/s。",
		"draft_contract": "拟签合同：电路 LINE-001 最低保障带宽 100 Mbit/s。",
	} {
		if _, err := st.AddDocument(context.Background(), admin, caseID, role, role+".txt", "text/plain", []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	acceptedWorkspace, err := st.Workspace(admin, caseID, runID)
	if err != nil {
		t.Fatalf("read accepted planner snapshot: %v", err)
	}
	snapshot := acceptedWorkspace.Snapshot
	if len(snapshot.PlannerConfiguration) == 0 || snapshot.PlannerConfigurationID == "" {
		t.Fatalf("acceptance did not freeze the planner payload: %+v", snapshot)
	}
	var frozen map[string]any
	if err := json.Unmarshal(snapshot.PlannerConfiguration, &frozen); err != nil {
		t.Fatalf("frozen planner payload is not JSON: %v", err)
	}
	// The approved payload must carry every field a worker needs to rebuild it.
	for _, field := range []string{"mode", "configuration_id", "prompt_version", "prompt_sha256", "schema_version", "runtime_version", "tool_catalog_version", "limits"} {
		if _, ok := frozen[field]; !ok {
			t.Fatalf("frozen planner payload omitted %q: %v", field, frozen)
		}
	}
	limits, ok := frozen["limits"].(map[string]any)
	if !ok || limits["max_rounds"] == nil || limits["max_calls"] == nil {
		t.Fatalf("frozen planner payload omitted limits: %v", frozen["limits"])
	}

	// The payload is durable: the exact bytes and identity are stored, not only
	// their hash, so a worker can rebuild the approved configuration.
	var storedPayload []byte
	var storedID string
	if err := st.pool.QueryRow(context.Background(),
		`SELECT planner_configuration, planner_configuration_id FROM review_run_snapshots WHERE run_id=$1`,
		runID).Scan(&storedPayload, &storedID); err != nil {
		t.Fatalf("read persisted planner payload: %v", err)
	}
	if !bytes.Equal(storedPayload, snapshot.PlannerConfiguration) {
		t.Fatal("persisted planner payload differs from the frozen snapshot")
	}
	if storedID != snapshot.PlannerConfigurationID {
		t.Fatal("persisted planner identity differs from the frozen snapshot")
	}
	// The accepted attempt carries the same bytes for the worker.
	var attemptPayload []byte
	if err := st.pool.QueryRow(context.Background(),
		`SELECT planner_configuration FROM review_attempts WHERE run_id=$1 ORDER BY ordinal LIMIT 1`,
		runID).Scan(&attemptPayload); err != nil {
		t.Fatalf("read attempt planner payload: %v", err)
	}
	if !bytes.Equal(attemptPayload, snapshot.PlannerConfiguration) {
		t.Fatal("attempt planner payload differs from the run snapshot")
	}
	waitTerminalWorkspace(t, st, admin, caseID, runID)
	reopened, err := st.Reopen()
	if err != nil {
		t.Fatalf("reopen accepted planner payload: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopened.UseModels(&frozenOCRModel{})
	workspace, err := reopened.Workspace(admin, caseID, runID)
	if err != nil {
		t.Fatalf("read accepted run after reopen: %v", err)
	}
	if workspace.Execution == nil || workspace.Execution.Attempt == nil ||
		!bytes.Equal(workspace.Execution.Attempt.PlannerConfiguration, snapshot.PlannerConfiguration) ||
		workspace.Execution.Attempt.PlannerConfigurationID != snapshot.PlannerConfigurationID {
		t.Fatalf("consumer reopen lost frozen planner payload: got=%+v want_id=%q", workspace.Execution, snapshot.PlannerConfigurationID)
	}
	reopenedState := snapshotState(reopened)
	reopenedSnapshot := reopenedState.snapshotOf(runID)
	reopenedAttempt, foundAttempt := reopenedState.latestAttempt(runID)
	if !foundAttempt || !bytes.Equal(reopenedSnapshot.PlannerConfiguration, snapshot.PlannerConfiguration) ||
		reopenedSnapshot.PlannerConfigurationID != snapshot.PlannerConfigurationID ||
		!bytes.Equal(reopenedAttempt.PlannerConfiguration, snapshot.PlannerConfiguration) ||
		reopenedAttempt.PlannerConfigurationID != snapshot.PlannerConfigurationID {
		t.Fatalf("store snapshot clone lost frozen planner payload: snapshot=%+v attempt=%+v", reopenedSnapshot, reopenedAttempt)
	}
	if _, err := FrozenPlannerConfiguration(reopenedAttempt); err != nil {
		t.Fatalf("reopened planner payload no longer reconstructs: %v", err)
	}
	if err := reopened.Retry(context.Background(), admin, caseID, runID); err != nil {
		t.Fatalf("retry reopened run: %v", err)
	}
	retried, err := reopened.Workspace(admin, caseID, runID)
	if err != nil {
		t.Fatalf("read retried run: %v", err)
	}
	if retried.Execution == nil || retried.Execution.Attempt == nil || retried.Execution.Attempt.Ordinal != 2 ||
		!bytes.Equal(retried.Execution.Attempt.PlannerConfiguration, snapshot.PlannerConfiguration) ||
		retried.Execution.Attempt.PlannerConfigurationID != snapshot.PlannerConfigurationID {
		t.Fatalf("retry did not copy frozen planner payload: got=%+v want_id=%q", retried.Execution, snapshot.PlannerConfigurationID)
	}
}
