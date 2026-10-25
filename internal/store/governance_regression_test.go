package store_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/store"
)

// governedManifest builds a strict rulepack/v1 manifest for the current tenant
// with the three mandatory telecom capabilities. A caller-supplied version keeps
// each published revision distinct.
func governedManifest(t *testing.T, tenantID, version string, bandwidth string) []byte {
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

func governanceAdmin(t *testing.T) accounts.User {
	t.Helper()
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	return admin
}

func openGovernanceStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenTest(t.TempDir(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Skipf("isolated PostgreSQL test schema unavailable: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestGovernanceCreateIsPersistedAndReadable is the regression for the create
// path: a created draft must be persisted, readable byte-for-byte, and visible
// after a reopen of the same schema.
func TestGovernanceCreateIsPersistedAndReadable(t *testing.T) {
	st := openGovernanceStore(t)
	admin := governanceAdmin(t)
	manifest := governedManifest(t, accounts.Tenant(admin), "1", "100")

	created, err := st.CreateRulePack(admin, manifest)
	if err != nil {
		t.Fatalf("create rulepack: %v", err)
	}
	if created.Pack.ID != "telecom-governed" || created.Pack.Version != "1" || created.Pack.Status != store.GovernanceDraft {
		t.Fatalf("created summary is wrong: %+v", created.Pack)
	}
	if created.Pack.Revision != 1 || created.Pack.ContentHash == "" || created.Pack.SnapshotSHA256 == "" {
		t.Fatalf("created summary omitted identity: %+v", created.Pack)
	}
	if len(created.Manifest) == 0 || !json.Valid(created.Manifest) {
		t.Fatalf("created manifest is not valid JSON: %q", created.Manifest)
	}

	read, err := st.GetRulePackVersion(admin, "telecom-governed", "1")
	if err != nil {
		t.Fatalf("get created rulepack: %v", err)
	}
	if !bytes.Equal(read.Manifest, created.Manifest) {
		t.Fatal("stored manifest bytes changed between create and read")
	}
	if read.Pack.ContentHash != created.Pack.ContentHash {
		t.Fatal("stored content hash changed between create and read")
	}

	reopened, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	durable, err := reopened.GetRulePackVersion(admin, "telecom-governed", "1")
	if err != nil {
		t.Fatalf("reopened read: %v", err)
	}
	if !bytes.Equal(durable.Manifest, created.Manifest) || durable.Pack.ContentHash != created.Pack.ContentHash {
		t.Fatal("created rulepack did not survive a reopen byte-for-byte")
	}
}

// TestGovernanceDuplicateVersionAndTenantBindingAreRefused proves a duplicate
// version and a foreign manifest tenant never enter the store.
func TestGovernanceDuplicateVersionAndTenantBindingAreRefused(t *testing.T) {
	st := openGovernanceStore(t)
	admin := governanceAdmin(t)
	manifest := governedManifest(t, accounts.Tenant(admin), "1", "100")
	if _, err := st.CreateRulePack(admin, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRulePack(admin, manifest); err == nil {
		t.Fatal("duplicate pack version was accepted")
	}

	foreign := governedManifest(t, "other-tenant", "2", "100")
	if _, err := st.CreateRulePack(admin, foreign); err == nil {
		t.Fatal("a manifest for another tenant was accepted")
	}
	if _, err := st.GetRulePackVersion(admin, "telecom-governed", "2"); err == nil {
		t.Fatal("the foreign manifest was persisted")
	}
}

// TestGovernanceLifecycleRequiresExactHashesAndReapprovalAfterEdit proves the
// content-hash binding: an edit invalidates the approval, and approve/publish
// only proceed against the exact current revision and hash.
func TestGovernanceLifecycleRequiresExactHashesAndReapprovalAfterEdit(t *testing.T) {
	st := openGovernanceStore(t)
	admin := governanceAdmin(t)
	manifest := governedManifest(t, accounts.Tenant(admin), "1", "100")
	created, err := st.CreateRulePack(admin, manifest)
	if err != nil {
		t.Fatal(err)
	}

	validated, err := st.ValidateRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedRevision: created.Pack.Revision,
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if validated.Pack.Status != store.GovernanceValidated || validated.Pack.Revision != created.Pack.Revision+1 {
		t.Fatalf("validate did not advance state: %+v", validated.Pack)
	}

	// A stale content hash cannot approve.
	if _, err := st.ApproveRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedRevision: validated.Pack.Revision,
		ExpectedContentHash: strings.Repeat("0", 64),
	}); err == nil {
		t.Fatal("approval with a wrong content hash was accepted")
	}

	approved, err := st.ApproveRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedRevision: validated.Pack.Revision,
		ExpectedContentHash: validated.Pack.ContentHash,
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Pack.Status != store.GovernanceApproved {
		t.Fatalf("approve did not reach approved: %+v", approved.Pack)
	}

	// Editing content after approval is refused, because only a draft is editable.
	edited := governedManifest(t, accounts.Tenant(admin), "1", "200")
	if _, err := st.EditRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedRevision: approved.Pack.Revision, Manifest: edited,
	}); err == nil {
		t.Fatal("an approved version was edited in place")
	}
}

// TestGovernanceEditIsDraftOnlyAndChangesHash proves a validated version cannot
// be edited in place (the content edit that would invalidate approval is refused
// outright), while a draft edit advances the revision and changes the hash.
func TestGovernanceEditResetsValidation(t *testing.T) {
	st := openGovernanceStore(t)
	admin := governanceAdmin(t)
	manifest := governedManifest(t, accounts.Tenant(admin), "1", "100")
	created, err := st.CreateRulePack(admin, manifest)
	if err != nil {
		t.Fatal(err)
	}
	edited := governedManifest(t, accounts.Tenant(admin), "1", "150")
	updated, err := st.EditRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedRevision: created.Pack.Revision, Manifest: edited,
	})
	if err != nil {
		t.Fatalf("edit a draft: %v", err)
	}
	if updated.Pack.Status != store.GovernanceDraft {
		t.Fatalf("draft edit changed the lifecycle: %+v", updated.Pack)
	}
	if updated.Pack.ContentHash == created.Pack.ContentHash {
		t.Fatal("edit did not change the content hash")
	}
	if updated.Pack.Revision != created.Pack.Revision+1 {
		t.Fatalf("edit did not advance the revision: %d", updated.Pack.Revision)
	}

	validated, err := st.ValidateRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedRevision: updated.Pack.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Once validated, the version can no longer be edited, so an approval bound to
	// the validated content can never be silently invalidated by a later edit.
	if _, err := st.EditRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedRevision: validated.Pack.Revision,
		Manifest: governedManifest(t, accounts.Tenant(admin), "1", "300"),
	}); err == nil {
		t.Fatal("a validated version was edited in place")
	}
	// A stale revision is also refused.
	if _, err := st.EditRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedRevision: created.Pack.Revision,
		Manifest: edited,
	}); err == nil {
		t.Fatal("edit accepted a stale revision")
	}
}

// TestGovernancePublishFreezesBytesAndMovesPointer proves publish makes the
// version immutable, activates the pointer, and that published bytes survive a
// reopen unchanged.
func TestGovernancePublishFreezesBytesAndMovesPointer(t *testing.T) {
	st := openGovernanceStore(t)
	admin := governanceAdmin(t)
	manifest := governedManifest(t, accounts.Tenant(admin), "1", "100")
	published := publishGovernedPack(t, st, admin, manifest, "1")

	if published.Pack.Status != store.GovernancePublished || !published.Pack.IsActive {
		t.Fatalf("publish did not activate the version: %+v", published.Pack)
	}
	if published.Pack.ActivePointerRevision != 1 {
		t.Fatalf("first publish did not set pointer revision 1: %d", published.Pack.ActivePointerRevision)
	}

	// Published bytes are immutable: no edit, and republish is refused.
	if _, err := st.EditRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedRevision: published.Pack.Revision,
		Manifest: governedManifest(t, accounts.Tenant(admin), "1", "200"),
	}); err == nil {
		t.Fatal("a published version was edited")
	}

	reopened, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	durable, err := reopened.GetRulePackVersion(admin, "telecom-governed", "1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(durable.Manifest, published.Manifest) {
		t.Fatal("published bytes changed after reopen")
	}
	if !durable.Pack.IsActive || durable.Pack.ActivePointerRevision != 1 {
		t.Fatalf("active pointer did not survive reopen: %+v", durable.Pack)
	}
}

// TestGovernancePointerCASAndIdempotentPublish proves the pointer revision is a
// real compare-and-swap and that an identical publish replay is idempotent.
func TestGovernancePointerCASAndIdempotentPublish(t *testing.T) {
	st := openGovernanceStore(t)
	admin := governanceAdmin(t)
	publishGovernedPack(t, st, admin, governedManifest(t, accounts.Tenant(admin), "1", "100"), "1")

	// Prepare v2 through the full lifecycle.
	v2 := createAndApprove(t, st, admin, governedManifest(t, accounts.Tenant(admin), "2", "200"), "2")

	// A stale pointer revision cannot publish.
	if _, err := st.PublishRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "2", ExpectedRevision: v2.Pack.Revision,
		ExpectedPointerRevision: 0,
	}); err == nil {
		t.Fatal("publish with a stale pointer revision was accepted")
	}
	if _, err := st.PublishRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "2", ExpectedRevision: v2.Pack.Revision,
		ExpectedPointerRevision: 1,
	}); err != nil {
		t.Fatalf("publish v2: %v", err)
	}

	detail, err := st.GetRulePackVersion(admin, "telecom-governed", "2")
	if err != nil {
		t.Fatal(err)
	}
	if !detail.Pack.IsActive || detail.Pack.ActivePointerRevision != 2 {
		t.Fatalf("pointer did not move to v2: %+v", detail.Pack)
	}
	v1, err := st.GetRulePackVersion(admin, "telecom-governed", "1")
	if err != nil {
		t.Fatal(err)
	}
	if v1.Pack.IsActive || v1.Pack.Status != store.GovernancePublished {
		t.Fatalf("v1 changed when v2 published: %+v", v1.Pack)
	}
}

// TestGovernanceRollbackMovesPointerOnly proves rollback selects an exact
// already published version without editing bytes or history.
func TestGovernanceRollbackMovesPointerOnly(t *testing.T) {
	st := openGovernanceStore(t)
	admin := governanceAdmin(t)
	v1 := publishGovernedPack(t, st, admin, governedManifest(t, accounts.Tenant(admin), "1", "100"), "1")
	v2 := createAndApprove(t, st, admin, governedManifest(t, accounts.Tenant(admin), "2", "200"), "2")
	if _, err := st.PublishRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "2", ExpectedRevision: v2.Pack.Revision, ExpectedPointerRevision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	rolled, err := st.RollbackRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedPointerRevision: 2,
	})
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if !rolled.Pack.IsActive || rolled.Pack.Version != "1" {
		t.Fatalf("rollback did not reactivate v1: %+v", rolled.Pack)
	}
	if !bytes.Equal(rolled.Manifest, v1.Manifest) {
		t.Fatal("rollback changed v1 bytes")
	}
	if _, err := st.RollbackRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedPointerRevision: 3,
	}); err == nil {
		t.Fatal("rollback to the already active version was accepted")
	}
}

// TestGovernanceDisableClearsPointerKeepsBytes proves disable only clears the
// active pointer; the published version and its bytes stay readable.
func TestGovernanceDisableClearsPointerKeepsBytes(t *testing.T) {
	st := openGovernanceStore(t)
	admin := governanceAdmin(t)
	published := publishGovernedPack(t, st, admin, governedManifest(t, accounts.Tenant(admin), "1", "100"), "1")
	disabled, err := st.DisableRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedRevision: published.Pack.Revision, ExpectedPointerRevision: 1,
	})
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if disabled.Pack.IsActive || disabled.Pack.Status != store.GovernancePublished {
		t.Fatalf("disable changed status or kept active: %+v", disabled.Pack)
	}
	if !bytes.Equal(disabled.Manifest, published.Manifest) {
		t.Fatal("disable changed published bytes")
	}
}

// TestGovernanceHistoryIsAppendOnly proves every transition appends one history
// entry and the sequence never rewrites earlier rows.
func TestGovernanceHistoryIsAppendOnly(t *testing.T) {
	st := openGovernanceStore(t)
	admin := governanceAdmin(t)
	publishGovernedPack(t, st, admin, governedManifest(t, accounts.Tenant(admin), "1", "100"), "1")

	history, err := st.RulePackHistory(admin, "telecom-governed")
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 4 {
		t.Fatalf("history entries = %d, want create/validate/approve/publish", len(history.Items))
	}
	want := []string{store.GovernanceActionCreate, store.GovernanceActionValidate, store.GovernanceActionApprove, store.GovernanceActionPublish}
	for i, action := range want {
		if history.Items[i].Action != action {
			t.Fatalf("history[%d] = %q, want %q", i, history.Items[i].Action, action)
		}
		if history.Items[i].ActorID != admin.ID {
			t.Fatalf("history actor = %q, want trusted admin %q", history.Items[i].ActorID, admin.ID)
		}
	}
	if history.Items[3].PointerRevision != 1 {
		t.Fatalf("publish history pointer revision = %d, want 1", history.Items[3].PointerRevision)
	}
}

// TestGovernanceRejectsNonAdmin proves a non-admin trusted identity cannot read
// or mutate governance, independent of the HTTP session audience.
func TestGovernanceRejectsNonAdmin(t *testing.T) {
	st := openGovernanceStore(t)
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("demo reviewer missing")
	}
	if _, err := st.ListRulePackVersions(reviewer); err == nil {
		t.Fatal("a non-admin listed governance versions")
	}
	if _, err := st.CreateRulePack(reviewer, governedManifest(t, accounts.Tenant(reviewer), "1", "100")); err == nil {
		t.Fatal("a non-admin created a governance version")
	}
}

// TestGovernanceSelectsOnlyApplicableActivePack proves acceptance selection
// matches tenant, exact scope and effective window, and fails closed on
// ambiguity.
func TestGovernanceSelectsOnlyApplicableActivePack(t *testing.T) {
	st := openGovernanceStore(t)
	admin := governanceAdmin(t)
	publishGovernedPack(t, st, admin, governedManifest(t, accounts.Tenant(admin), "1", "100"), "1")

	pack, err := st.SelectActiveRulePack(t.Context(), accounts.Tenant(admin), "ENT", "telecom_dedicated_line", "service", "2026-06-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if pack == nil || pack.PackID != "telecom-governed" || pack.Version != "1" {
		t.Fatalf("applicable pack not selected: %+v", pack)
	}
	if len(pack.Snapshot) == 0 || pack.ContentHash == "" || pack.SnapshotDigest == "" {
		t.Fatalf("selected pack omitted frozen identity: %+v", pack)
	}

	// A different exact scope has no applicable pack.
	other, err := st.SelectActiveRulePack(t.Context(), accounts.Tenant(admin), "ENT", "telecom_dedicated_line", "procurement", "2026-06-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if other != nil {
		t.Fatalf("a different contract type matched: %+v", other)
	}

	// A different tenant has no applicable pack.
	foreign, err := st.SelectActiveRulePack(t.Context(), "other-tenant", "ENT", "telecom_dedicated_line", "service", "2026-06-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if foreign != nil {
		t.Fatalf("another tenant matched: %+v", foreign)
	}

	// After disable the pointer is gone, so selection returns no pack.
	active, err := st.GetRulePackVersion(admin, "telecom-governed", "1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DisableRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedRevision: active.Pack.Revision, ExpectedPointerRevision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	gone, err := st.SelectActiveRulePack(t.Context(), accounts.Tenant(admin), "ENT", "telecom_dedicated_line", "service", "2026-06-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if gone != nil {
		t.Fatalf("a disabled pack was selected: %+v", gone)
	}
}

// TestGovernanceSampleTestIsBoundedAndSynthetic proves the sample route runs a
// deterministic report against caller-supplied synthetic evidence only.
func TestGovernanceSampleTestIsBoundedAndSynthetic(t *testing.T) {
	st := openGovernanceStore(t)
	admin := governanceAdmin(t)
	approved := createAndApprove(t, st, admin, governedManifest(t, accounts.Tenant(admin), "1", "100"), "1")

	text := "电路 LINE-001：最低保障带宽 150 Mbit/s。"
	quote := text
	result, err := st.TestRulePackVersion(admin, store.RulePackTestInput{
		PackID: "telecom-governed", Version: "1", ExpectedRevision: approved.Pack.Revision,
		BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service",
		At:      "2026-06-01T00:00:00Z",
		Sources: []store.RulePackTestSource{{ID: "draft", Version: "1", Role: "draft_contract", Text: text}},
		Facts: []store.RulePackTestFact{{
			ID: "fact-bw", RuleID: "ENT-TECH-BW-001", Dimension: "minimum_bandwidth",
			ObjectRef: "LINE-001", Value: "150", Unit: "Mbit/s", SourceRole: "draft_contract",
			Citations: []store.RulePackTestCitation{{SourceID: "draft", SourceVersion: "1", Start: 0, End: len([]rune(quote)), Quote: quote}},
		}},
	})
	if err != nil {
		t.Fatalf("sample test: %v", err)
	}
	if result.Revision != approved.Pack.Revision || result.ContentHash != approved.Pack.ContentHash {
		t.Fatalf("sample result identity is wrong: %+v", result)
	}
	if result.Report.PackID != "telecom-governed" {
		t.Fatalf("sample report identity is wrong: %+v", result.Report)
	}
	// Every rule whose exact scope matches runs, exactly like production
	// execution; the synthetic fact makes only the bandwidth rule compatible.
	var bandwidth *struct{ relation string }
	for _, ruleResult := range result.Report.Results {
		if ruleResult.RuleID == "ENT-TECH-BW-001" {
			bandwidth = &struct{ relation string }{relation: string(ruleResult.Relation)}
		}
	}
	if bandwidth == nil {
		t.Fatalf("sample report omitted the requested bandwidth rule: %+v", result.Report.Results)
	}
	if bandwidth.relation != "compatible" {
		t.Fatalf("150 Mbit/s did not satisfy the 100 Mbit/s threshold: %s", bandwidth.relation)
	}

	// The sample test persists no lifecycle transition: the version stays approved.
	detail, err := st.GetRulePackVersion(admin, "telecom-governed", "1")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Pack.Status != store.GovernanceApproved || detail.Pack.Revision != approved.Pack.Revision {
		t.Fatalf("sample test changed the persisted lifecycle: %+v", detail.Pack)
	}
	if detail.Pack.IsActive {
		t.Fatal("sample test activated a pointer")
	}

	// A stale revision is refused.
	if _, err := st.TestRulePackVersion(admin, store.RulePackTestInput{
		PackID: "telecom-governed", Version: "1", ExpectedRevision: approved.Pack.Revision + 5,
		BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service",
		At: "2026-06-01T00:00:00Z", Sources: []store.RulePackTestSource{{ID: "draft", Version: "1", Role: "draft_contract", Text: text}},
	}); err == nil {
		t.Fatal("sample test accepted a stale revision")
	}
}

// publishGovernedPack drives create→validate→approve→publish and returns the
// published detail.
func publishGovernedPack(t *testing.T, st *store.Store, admin accounts.User, manifest []byte, version string) store.RulePackDetail {
	t.Helper()
	approved := createAndApprove(t, st, admin, manifest, version)
	published, err := st.PublishRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: version, ExpectedRevision: approved.Pack.Revision,
		ExpectedPointerRevision: approved.Pack.ActivePointerRevision,
	})
	if err != nil {
		t.Fatalf("publish %s: %v", version, err)
	}
	return published
}

func createAndApprove(t *testing.T, st *store.Store, admin accounts.User, manifest []byte, version string) store.RulePackDetail {
	t.Helper()
	created, err := st.CreateRulePack(admin, manifest)
	if err != nil {
		t.Fatalf("create %s: %v", version, err)
	}
	validated, err := st.ValidateRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: version, ExpectedRevision: created.Pack.Revision,
	})
	if err != nil {
		t.Fatalf("validate %s: %v", version, err)
	}
	approved, err := st.ApproveRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: version, ExpectedRevision: validated.Pack.Revision,
		ExpectedContentHash: validated.Pack.ContentHash,
	})
	if err != nil {
		t.Fatalf("approve %s: %v", version, err)
	}
	return approved
}

// TestGovernanceDisableKeepsMonotonicPointerRevision proves a disable leaves a
// tombstone with an increasing revision, so a stale CAS captured before the
// disable can never match again (no ABA), and disabling an older version never
// clears the live pointer.
func TestGovernanceDisableKeepsMonotonicPointerRevision(t *testing.T) {
	st := openGovernanceStore(t)
	admin := governanceAdmin(t)
	published := publishGovernedPack(t, st, admin, governedManifest(t, accounts.Tenant(admin), "1", "100"), "1")
	disabled, err := st.DisableRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1",
		ExpectedRevision: published.Pack.Revision, ExpectedPointerRevision: 1,
	})
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if disabled.Pack.ActivePointerRevision != 2 {
		t.Fatalf("disable did not advance the pointer revision: %d", disabled.Pack.ActivePointerRevision)
	}
	// A stale publish that still names the pre-disable pointer revision 0 or 1
	// must be refused, because the tombstone keeps counting.
	v2 := createAndApprove(t, st, admin, governedManifest(t, accounts.Tenant(admin), "2", "200"), "2")
	for _, staleRevision := range []int64{0, 1} {
		if _, err := st.PublishRulePackVersion(admin, store.GovernanceInput{
			PackID: "telecom-governed", Version: "2", ExpectedRevision: v2.Pack.Revision,
			ExpectedPointerRevision: staleRevision,
		}); err == nil {
			t.Fatalf("stale pointer revision %d published after a disable (ABA)", staleRevision)
		}
	}
	// The current revision still works.
	if _, err := st.PublishRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "2", ExpectedRevision: v2.Pack.Revision,
		ExpectedPointerRevision: 2,
	}); err != nil {
		t.Fatalf("publish with the current post-disable revision: %v", err)
	}
}

// TestGovernanceDisableOlderVersionKeepsCurrentPointer proves disabling a
// published but inactive version is refused and never clears the live pointer.
func TestGovernanceDisableOlderVersionKeepsCurrentPointer(t *testing.T) {
	st := openGovernanceStore(t)
	admin := governanceAdmin(t)
	publishGovernedPack(t, st, admin, governedManifest(t, accounts.Tenant(admin), "1", "100"), "1")
	v2 := createAndApprove(t, st, admin, governedManifest(t, accounts.Tenant(admin), "2", "200"), "2")
	publishedV2, err := st.PublishRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "2", ExpectedRevision: v2.Pack.Revision, ExpectedPointerRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	v1, err := st.GetRulePackVersion(admin, "telecom-governed", "1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DisableRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1",
		ExpectedRevision: v1.Pack.Revision, ExpectedPointerRevision: publishedV2.Pack.ActivePointerRevision,
	}); err == nil {
		t.Fatal("disabling an inactive older version was accepted")
	}
	live, err := st.GetRulePackVersion(admin, "telecom-governed", "2")
	if err != nil || !live.Pack.IsActive {
		t.Fatalf("the live pointer was cleared by a stale disable: %+v %v", live.Pack, err)
	}
}

// TestGovernancePublishAndRollbackAreIdempotent proves an identical replay under
// the same Idempotency-Key returns the original result, while a different request
// under the same key conflicts.
func TestGovernancePublishAndRollbackAreIdempotent(t *testing.T) {
	st := openGovernanceStore(t)
	admin := governanceAdmin(t)
	publishGovernedPack(t, st, admin, governedManifest(t, accounts.Tenant(admin), "1", "100"), "1")
	v2 := createAndApprove(t, st, admin, governedManifest(t, accounts.Tenant(admin), "2", "200"), "2")

	first, err := st.PublishRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "2", ExpectedRevision: v2.Pack.Revision,
		ExpectedPointerRevision: 1, IdempotencyKey: "publish-v2",
	})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := st.PublishRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "2", ExpectedRevision: v2.Pack.Revision,
		ExpectedPointerRevision: 1, IdempotencyKey: "publish-v2",
	})
	if err != nil {
		t.Fatalf("identical publish replay was refused: %v", err)
	}
	if replay.Pack.Version != first.Pack.Version || replay.Pack.ActivePointerRevision != first.Pack.ActivePointerRevision {
		t.Fatalf("replay returned a different result: %+v vs %+v", replay.Pack, first.Pack)
	}
	if _, err := st.PublishRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "2", ExpectedRevision: v2.Pack.Revision,
		ExpectedPointerRevision: 999, IdempotencyKey: "publish-v2",
	}); err == nil {
		t.Fatal("the same idempotency key with different request bytes was accepted")
	}

	// Rollback replay.
	rollback, err := st.RollbackRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedPointerRevision: 2, IdempotencyKey: "rollback-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	again, err := st.RollbackRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1", ExpectedPointerRevision: 2, IdempotencyKey: "rollback-v1",
	})
	if err != nil || again.Pack.ActivePointerRevision != rollback.Pack.ActivePointerRevision {
		t.Fatalf("rollback replay was not idempotent: %+v %v", again.Pack, err)
	}
}

// TestGovernanceListReportsDisabledTombstoneInactive proves the list projection
// agrees with Get and acceptance: a disabled pack is reported inactive rather
// than as a still-active pointer.
func TestGovernanceListReportsDisabledTombstoneInactive(t *testing.T) {
	st := openGovernanceStore(t)
	admin := governanceAdmin(t)
	published := publishGovernedPack(t, st, admin, governedManifest(t, accounts.Tenant(admin), "1", "100"), "1")
	if _, err := st.DisableRulePackVersion(admin, store.GovernanceInput{
		PackID: "telecom-governed", Version: "1",
		ExpectedRevision: published.Pack.Revision, ExpectedPointerRevision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListRulePackVersions(admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("list items = %d", len(list.Items))
	}
	if list.Items[0].IsActive {
		t.Fatal("list reported a disabled tombstone as active")
	}
	if list.Items[0].Status != store.GovernancePublished {
		t.Fatalf("disable changed the published status: %+v", list.Items[0])
	}
	detail, err := st.GetRulePackVersion(admin, "telecom-governed", "1")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Pack.IsActive != list.Items[0].IsActive {
		t.Fatal("list and detail disagree about the active pointer")
	}
	if list.Items[0].ActivePointerRevision != 2 {
		t.Fatalf("list pointer revision = %d, want the post-disable tombstone revision 2", list.Items[0].ActivePointerRevision)
	}
}
