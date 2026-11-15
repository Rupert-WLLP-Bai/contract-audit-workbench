// File metadata (manifest mirror):
// Purpose: Exercises tenant-config canonicalization and catalog matching, experience-usage identity and hashing, decision-outcome classification and environment allowlist parsing without a database.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

type fakeSQLState string

func (f fakeSQLState) Error() string    { return "fake sql state" }
func (f fakeSQLState) SQLState() string { return string(f) }

func TestCanonicalTenantConfigSortsAndCopiesPolicyRefs(t *testing.T) {
	selection := TenantConfigSelection{
		PublicTemplateRef: TenantConfigRef{ID: "public-contract-review", Version: "1", Digest: "sha256:aa"},
		PrivatePolicyRefs: []TenantConfigRef{
			{ID: "policy-b", Version: "1", Digest: "sha256:bb"},
			{ID: "policy-a", Version: "2", Digest: "sha256:aa"},
			{ID: "policy-a", Version: "1", Digest: "sha256:cc"},
		},
		ModelCatalogRef: "model-1",
		EgressMode:      "private",
	}
	first, err := canonicalTenantConfig(selection)
	if err != nil {
		t.Fatal(err)
	}
	if selection.PrivatePolicyRefs[0].ID != "policy-b" {
		t.Fatal("canonicalization mutated the caller's policy refs")
	}
	want := `{"public_template_ref":{"id":"public-contract-review","version":"1","digest":"sha256:aa"},` +
		`"private_policy_refs":[{"id":"policy-a","version":"1","digest":"sha256:cc"},` +
		`{"id":"policy-a","version":"2","digest":"sha256:aa"},{"id":"policy-b","version":"1","digest":"sha256:bb"}],` +
		`"model_catalog_ref":"model-1","credential_ref":null,"egress_mode":"private"}`
	if string(first) != want {
		t.Fatalf("canonical selection was %s", first)
	}
	selection.PrivatePolicyRefs = selection.PrivatePolicyRefs[:1]
	second, err := canonicalTenantConfig(selection)
	if err != nil || string(second) == string(first) {
		t.Fatalf("changed selection produced the same canonical bytes: %s", second)
	}
}

func TestPublicTemplateCandidateIdentityCoversExactBytes(t *testing.T) {
	entry := publicTemplateCandidate()
	raw := []byte(`{"id":"public-contract-review","version":"1","template":"contract-review-template/v1"}`)
	sum := sha256.Sum256(raw)
	if entry.ID != "public-contract-review" || entry.Version != "1" || entry.Digest != hex.EncodeToString(sum[:]) || entry.Kind != "public_template" || entry.Title == "" {
		t.Fatalf("public template candidate lost its identity: %+v", entry)
	}
}

func TestCatalogEntryRequiresExactIdentity(t *testing.T) {
	catalog := []TenantConfigCatalogEntry{
		{TenantConfigRef: TenantConfigRef{ID: "policy-a", Version: "1", Digest: "sha256:aa"}, Title: "政策 A", Kind: "private_policy"},
		{TenantConfigRef: TenantConfigRef{ID: "policy-b", Version: "2", Digest: "sha256:bb"}, Title: "政策 B", Kind: "private_policy"},
	}
	found, ok := catalogEntry(catalog, TenantConfigRef{ID: "policy-b", Version: "2", Digest: "sha256:bb"})
	if !ok || found.Title != "政策 B" {
		t.Fatalf("exact catalog entry was not found: %+v %v", found, ok)
	}
	for _, ref := range []TenantConfigRef{
		{ID: "policy-b", Version: "1", Digest: "sha256:bb"},
		{ID: "policy-b", Version: "2", Digest: "sha256:cc"},
		{ID: "policy-c", Version: "2", Digest: "sha256:bb"},
	} {
		if _, ok := catalogEntry(catalog, ref); ok {
			t.Fatalf("catalog matched an inexact ref %+v", ref)
		}
	}
	if _, ok := catalogEntry(nil, TenantConfigRef{ID: "policy-a"}); ok {
		t.Fatal("empty catalog matched")
	}
}

func TestSelectionSummaryNamesEverySelection(t *testing.T) {
	credential := "credential-1"
	summary := selectionSummary(TenantConfigSelection{
		PublicTemplateRef: TenantConfigRef{ID: "public-contract-review", Version: "1", Digest: "sha256:aa"},
		PrivatePolicyRefs: []TenantConfigRef{
			{ID: "policy-b", Version: "1", Digest: "sha256:bb"},
			{ID: "policy-a", Version: "2", Digest: "sha256:aa"},
		},
		ModelCatalogRef: "model-1",
		CredentialRef:   &credential,
		EgressMode:      "private",
	})
	want := []string{"public-contract-review@1#sha256:aa", "policy-a@2#sha256:aa,policy-b@1#sha256:bb", "model-1", "credential-1", "private"}
	if len(summary) != len(want) {
		t.Fatalf("summary has %d fields: %+v", len(summary), summary)
	}
	for i := range want {
		if summary[i] != want[i] {
			t.Fatalf("summary field %d is %q, want %q", i, summary[i], want[i])
		}
	}
	withoutCredential := selectionSummary(TenantConfigSelection{PublicTemplateRef: TenantConfigRef{ID: "p", Version: "1"}, EgressMode: "cloud"})
	if withoutCredential[1] != "" || withoutCredential[3] != "" || withoutCredential[4] != "cloud" {
		t.Fatalf("empty selections were not summarized: %+v", withoutCredential)
	}
}

func TestTenantConfigFunctionErrorMapsRevisionConflicts(t *testing.T) {
	requireFeedbackFault(t, tenantConfigFunctionError(fakeSQLState("P7C01"), "config_revision_conflict"), 409)
	requireFeedbackFault(t, tenantConfigFunctionError(fakeSQLState("P7C02"), "live_control_revision_conflict"), 409)
	original := fakeSQLState("XX000")
	if got := tenantConfigFunctionError(original, "config_revision_conflict"); got != original {
		t.Fatalf("unrelated SQL state was replaced by %v", got)
	}
	if got := tenantConfigFunctionError(errors.New("plain"), "config_revision_conflict"); got == nil {
		t.Fatal("non-SQL error was dropped")
	}
}

func TestValidExperienceUsageIdentityAndHash(t *testing.T) {
	valid := ExperienceUsageInput{CaseID: "case_1", RunID: "run_1", AttemptID: "att_1", FindingID: "finding_1"}
	if !validExperienceUsageIdentity(valid) {
		t.Fatal("valid usage identity was rejected")
	}
	invalid := []ExperienceUsageInput{
		{CaseID: "", RunID: "run_1", AttemptID: "att_1", FindingID: "finding_1"},
		{CaseID: "case_1", RunID: "", AttemptID: "att_1", FindingID: "finding_1"},
		{CaseID: "case_1", RunID: "run_1", AttemptID: "", FindingID: "finding_1"},
		{CaseID: "case_1", RunID: "run_1", AttemptID: "att_1", FindingID: ""},
		{CaseID: " case_1", RunID: "run_1", AttemptID: "att_1", FindingID: "finding_1"},
	}
	for _, input := range invalid {
		if validExperienceUsageIdentity(input) {
			t.Fatalf("invalid usage identity %+v was accepted", input)
		}
	}
	usage := ExperienceUsage{
		TenantID: "tenant-demo", OrgID: "org-demo", CaseID: "case_1", RunID: "run_1", AttemptID: "att_1",
		FindingID: "finding_1", Ref: KnowledgeRef{KnowledgeID: "knowledge-1", VersionID: "version-1", ContentSHA256: "sha256:aa"},
		SnapshotSHA256: "sha256:bb", InputSHA256: "sha256:cc", ExpectedAction: "confirm_risk", RecordedBy: "user-1",
	}
	first, err := experienceUsageHash(usage)
	if err != nil || len(first) != 64 {
		t.Fatalf("usage hash was %q (%v)", first, err)
	}
	replay, err := experienceUsageHash(usage)
	if err != nil || replay != first {
		t.Fatalf("usage hash is not deterministic: %q %q (%v)", first, replay, err)
	}
	changed := usage
	changed.RecordedBy = "user-2"
	other, err := experienceUsageHash(changed)
	if err != nil || other == first {
		t.Fatalf("usage hash ignored the recording actor: %q (%v)", other, err)
	}
}

func TestUsageSelectionFindsOnlyMatchingExperienceCandidate(t *testing.T) {
	ref := KnowledgeRef{KnowledgeID: "knowledge-1", VersionID: "version-1", ContentSHA256: "sha256:aa"}
	other := KnowledgeRef{KnowledgeID: "knowledge-2", VersionID: "version-1", ContentSHA256: "sha256:bb"}
	snapshot := ReviewKnowledgeSnapshot{OptionalCandidates: []GovernedKnowledgeSelection{
		{Ref: other, Kind: "experience"},
		{Ref: ref, Kind: "experience", Summary: "匹配经验"},
		{Ref: ref, Kind: "policy"},
	}}
	selected, ok := usageSelection(&snapshot, ref)
	if !ok || selected.Summary != "匹配经验" {
		t.Fatalf("matching experience candidate was not selected: %+v %v", selected, ok)
	}
	if _, ok := usageSelection(&snapshot, other); !ok {
		t.Fatal("second experience candidate was not selected")
	}
	if _, ok := usageSelection(&snapshot, KnowledgeRef{KnowledgeID: "knowledge-3"}); ok {
		t.Fatal("unknown ref was selected")
	}
	if _, ok := usageSelection(nil, ref); ok {
		t.Fatal("nil snapshot selected a candidate")
	}
}

func TestExperienceDecisionOutcomeClassifiesHumanDecisions(t *testing.T) {
	cases := []struct {
		name           string
		expectedAction string
		decisionID     string
		action         string
		disposition    string
		want           string
	}{
		{"pending", "confirm_risk", "", "confirm_risk", "confirmed", "pending"},
		{"adopted", "confirm_risk", "decision-1", "confirm_risk", "confirmed", "adopted"},
		{"overturned", "confirm_risk", "decision-1", "false_positive", "false_positive", "overturned"},
		{"unknown action", "confirm_risk", "decision-1", "unknown", "confirmed", "other"},
		{"disposition mismatch", "confirm_risk", "decision-1", "confirm_risk", "false_positive", "other"},
		{"procedural expected action", "accept_residual", "decision-1", "accept_residual", "accepted_residual", "other"},
		{"procedural actual action", "false_positive", "decision-1", "accept_residual", "accepted_residual", "other"},
	}
	for _, check := range cases {
		got := experienceDecisionOutcome(check.expectedAction, check.decisionID, check.action, check.disposition)
		if got != check.want {
			t.Fatalf("%s produced %q, want %q", check.name, got, check.want)
		}
	}
}

func TestConfiguredAllowlistContainsTrimsEntries(t *testing.T) {
	t.Setenv("CAW_TEST_ALLOWLIST", " alpha, beta ,,gamma ")
	if !configuredAllowlistContains("CAW_TEST_ALLOWLIST", "beta") {
		t.Fatal("trimmed allowlist entry was not found")
	}
	if configuredAllowlistContains("CAW_TEST_ALLOWLIST", " gamma ") {
		t.Fatal("allowlist did not compare values exactly")
	}
	for _, value := range []string{"delta", "", "  "} {
		if configuredAllowlistContains("CAW_TEST_ALLOWLIST", value) {
			t.Fatalf("value %q was accepted", value)
		}
	}
	t.Setenv("CAW_TEST_ALLOWLIST", "")
	if configuredAllowlistContains("CAW_TEST_ALLOWLIST", "alpha") {
		t.Fatal("empty allowlist matched")
	}
}
