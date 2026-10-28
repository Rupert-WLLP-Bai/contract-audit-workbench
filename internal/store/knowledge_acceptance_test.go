package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"contractaudit/internal/accounts"
)

const acceptanceBusinessTime = "2026-02-03T09:05:06.987654321+08:00"

// publishSyntheticAcceptancePack gives acceptance tests an explicit synthetic
// authority. The companion policy must still go through approval and publication.
func publishSyntheticAcceptancePack(t *testing.T, st *Store, admin accounts.User) RulePackDetail {
	t.Helper()
	var manifest map[string]any
	if err := json.Unmarshal(governedPackManifest(t, admin.TenantID, "1", "100"), &manifest); err != nil {
		t.Fatal(err)
	}
	for _, value := range manifest["rules"].([]any) {
		rule := value.(map[string]any)
		rule["authority_sources"] = []any{map[string]any{"id": "acceptance-demo-policy", "version": "1", "kind": "synthetic_demo"}}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return publishGovernedPackStore(t, st, admin, "1", "100", raw)
}

func publishSyntheticAcceptancePolicy(t *testing.T, st *Store, admin accounts.User, pack RulePackDetail) GovernedKnowledgeVersion {
	t.Helper()
	content := knowledgeFixtureContent(pack, "acceptance-demo-policy", "1", "policy")
	content.SourceType = "synthetic_policy"
	content.SourceReference = "approved synthetic acceptance fixture"
	return publishKnowledge(t, st, admin, createKnowledgeDraft(t, st, admin, content, "acceptance-policy-create"), "acceptance-policy")
}

func acceptanceKnowledgeCase(t *testing.T, st, documents *Store, user accounts.User) string {
	t.Helper()
	id := governanceTestCase(t, st, user)
	for _, item := range []struct{ role, body string }{
		{"procurement", "采购文件要求电路 LINE-001 的最低带宽为100 Mbit/s。"},
		{"draft_contract", "拟签合同约定电路 LINE-001 的最低带宽为100 Mbit/s。"},
	} {
		if _, err := documents.AddDocument(context.Background(), user, id, item.role, item.role+".txt", "text/plain", []byte(item.body)); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func acceptanceCounts(t *testing.T, st *Store, caseID string) [5]int64 {
	t.Helper()
	var counts [5]int64
	err := st.pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM review_runs WHERE case_id=$1),
		(SELECT count(*) FROM review_attempts a JOIN review_runs r ON r.id=a.run_id WHERE r.case_id=$1),
		(SELECT count(*) FROM review_run_snapshots s JOIN review_runs r ON r.id=s.run_id WHERE r.case_id=$1),
		(SELECT count(*) FROM worker_jobs WHERE case_id=$1),
		(SELECT count(*) FROM outbox_events WHERE case_id=$1)`, caseID).
		Scan(&counts[0], &counts[1], &counts[2], &counts[3], &counts[4])
	if err != nil {
		t.Fatal(err)
	}
	return counts
}

func runAcceptedKnowledgeJob(t *testing.T, st *Store, accepted JobAcceptance) {
	t.Helper()
	claim, err := st.ClaimNextJob(context.Background(), "knowledge-acceptance-worker", time.Minute)
	if err != nil || claim.JobID != accepted.JobID {
		t.Fatalf("claim accepted job: %+v %v", claim, err)
	}
	if err := st.RunClaimedJob(context.Background(), claim); err != nil {
		t.Fatalf("run accepted job: %v", err)
	}
}

func TestLegacyCloudInputHashDoesNotChange(t *testing.T) {
	legacy := RunSnapshot{
		RunID: "historical-run", DocumentIDs: []string{"doc_a"}, RuleSetHash: "rule", PlaybookHash: "play",
		ModelRef: "model", ComparatorRef: "comparator", CloudPolicySnapshot: json.RawMessage(`{"id":"legacy"}`),
		CloudPolicyDigest: "legacy-digest",
	}
	const historicalJSON = `{"snapshot":{"run_id":"","document_ids":["doc_a"],"rule_set_hash":"rule","playbook_hash":"play","model_ref":"model","comparator_ref":"comparator","cloud_policy_snapshot":{"id":"legacy"},"cloud_policy_digest":"legacy-digest"},"documents":[]}`
	got, err := frozenInputHash(legacy, []FrozenDocument{})
	if err != nil || got != sha256Bytes([]byte(historicalJSON)) {
		t.Fatalf("legacy frozen input hash changed: %q %v", got, err)
	}
}

func TestKnowledgeAcceptanceFreezesAndRestoresRetryIdentity(t *testing.T) {
	st, admin := openExecutionStore(t)
	st.UseModels(&frozenOCRModel{})
	pack := publishSyntheticAcceptancePack(t, st, admin)
	policy := publishSyntheticAcceptancePolicy(t, st, admin, pack)
	guideContent := knowledgeFixtureContent(pack, "acceptance-guidance", "1", "guidance")
	guide := publishKnowledge(t, st, admin, createKnowledgeDraft(t, st, admin, guideContent, "acceptance-guide-create"), "acceptance-guide")
	caseID := acceptanceKnowledgeCase(t, st, st, admin)

	accepted, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	workspace := readWorkspace(t, st, admin, caseID, accepted.RunID)
	frozen := workspace.Snapshot.GovernedKnowledge
	if frozen == nil || frozen.BusinessAt != "2026-02-03T01:05:06.987654321Z" || frozen.AuthorizationRevision == "" || frozen.SnapshotSHA256 == "" {
		t.Fatalf("acceptance omitted business time, authorization or digest: %+v", frozen)
	}
	if len(frozen.RequiredPolicies) != 1 || frozen.RequiredPolicies[0].Ref != policy.Ref ||
		len(frozen.RequiredPolicies[0].Binding.RuleIDs) != 3 || len(frozen.RequiredPolicies[0].Binding.SourceSpans) != 3 ||
		len(frozen.OptionalCandidates) != 1 || frozen.OptionalCandidates[0].Ref != guide.Ref || frozen.OptionalTruncated {
		t.Fatalf("acceptance omitted approved policies, bindings or candidate: %+v", frozen)
	}
	if frozen.RulePack.ContentHash != pack.Pack.ContentHash || frozen.RulePack.PointerRevision != pack.Pack.ActivePointerRevision {
		t.Fatalf("acceptance did not bind the published pack pointer: %+v", frozen.RulePack)
	}
	if _, err := st.pool.Exec(context.Background(), `UPDATE review_run_snapshots SET governed_knowledge=governed_knowledge WHERE run_id=$1`, accepted.RunID); err == nil {
		t.Fatal("database allowed an accepted knowledge snapshot to be rewritten")
	}
	replayed, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, "2026-02-03T01:05:06.987654321Z")
	if err != nil || replayed != accepted {
		t.Fatalf("same instant did not reuse the accepted job: %+v %v", replayed, err)
	}

	runAcceptedKnowledgeJob(t, st, accepted)
	terminal := readWorkspace(t, st, admin, caseID, accepted.RunID)
	if terminal.Execution == nil || terminal.Execution.Attempt == nil || terminal.Execution.Attempt.InputHash == nil {
		t.Fatalf("completed attempt did not freeze its input identity: %+v", terminal.Execution)
	}
	firstHash := *terminal.Execution.Attempt.InputHash
	stState, err := st.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	input, ok := stState.attemptInput(accepted.AttemptID)
	if !ok {
		t.Fatal("accepted attempt has no frozen document input")
	}
	changedKnowledge := *frozen
	changedKnowledge.BusinessAt = "2026-02-03T01:05:06.987654322Z"
	changedKnowledge, err = sealKnowledgeSnapshot(changedKnowledge)
	if err != nil {
		t.Fatal(err)
	}
	changedSnapshot := terminal.Snapshot
	changedSnapshot.GovernedKnowledge = &changedKnowledge
	changedHash, err := frozenInputHash(changedSnapshot, input.Documents)
	if err != nil || changedHash == firstHash {
		t.Fatalf("frozen input identity did not include governed knowledge: %q %v", changedHash, err)
	}
	reopened, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.UseModels(&frozenOCRModel{})
	restored := readWorkspace(t, reopened, admin, caseID, accepted.RunID)
	if !reflect.DeepEqual(restored.Snapshot.GovernedKnowledge, frozen) || restored.Execution.Attempt.InputHash == nil || *restored.Execution.Attempt.InputHash != firstHash {
		t.Fatalf("restart changed frozen governance or input identity: %+v", restored)
	}
	retry, err := reopened.RetryAsync(context.Background(), admin, caseID, accepted.RunID)
	if err != nil || retry.AttemptID == accepted.AttemptID {
		t.Fatalf("retry did not append an attempt: %+v %v", retry, err)
	}
	retried, err := reopened.Workspace(admin, caseID, accepted.RunID, retry.AttemptID)
	if err != nil || retried.Execution == nil || retried.Execution.Attempt == nil || retried.Execution.Attempt.InputHash == nil ||
		*retried.Execution.Attempt.InputHash != firstHash || !reflect.DeepEqual(retried.Snapshot.GovernedKnowledge, frozen) {
		t.Fatalf("retry did not retain the frozen governance and input: %+v %v", retried, err)
	}
}

func TestKnowledgeAcceptanceRequiresTimeApprovalAndRollsBack(t *testing.T) {
	st, admin := openExecutionStore(t)
	pack := publishSyntheticAcceptancePack(t, st, admin)
	caseID := acceptanceKnowledgeCase(t, st, st, admin)
	before := acceptanceCounts(t, st, caseID)
	_, err := st.StartReviewAsync(context.Background(), admin, caseID)
	requireKnowledgeFault(t, err, 422)
	_, err = st.StartReviewAsyncAt(context.Background(), admin, caseID, "2026-02-03")
	requireKnowledgeFault(t, err, 422)
	_, err = st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	requireKnowledgeFault(t, err, 409)
	if after := acceptanceCounts(t, st, caseID); after != before {
		t.Fatalf("failed acceptance persisted part of a run: before=%v after=%v", before, after)
	}
	publishSyntheticAcceptancePolicy(t, st, admin, pack)
	forged := admin
	forged.TenantID = "forged-tenant"
	_, err = st.StartReviewAsyncAt(context.Background(), forged, caseID, acceptanceBusinessTime)
	requireKnowledgeFault(t, err, 401)
	foreign, _ := accounts.ByID("demo-tenant2-admin")
	_, err = st.StartReviewAsyncAt(context.Background(), foreign, caseID, acceptanceBusinessTime)
	if err == nil {
		t.Fatal("foreign tenant accepted another tenant's case")
	}

	_, err = st.pool.Exec(context.Background(), `CREATE FUNCTION reject_knowledge_test_job() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'forced queue failure'; END $$;
		CREATE TRIGGER reject_knowledge_test_job BEFORE INSERT ON worker_jobs
		FOR EACH ROW EXECUTE FUNCTION reject_knowledge_test_job()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime); err == nil {
		t.Fatal("acceptance succeeded when its durable job could not be inserted")
	}
	if after := acceptanceCounts(t, st, caseID); after != before {
		t.Fatalf("job failure committed a partial acceptance: before=%v after=%v", before, after)
	}
}

func TestKnowledgeAcceptanceRechecksWithdrawalRollbackAndActiveRetry(t *testing.T) {
	st, admin := openExecutionStore(t)
	pack := publishSyntheticAcceptancePack(t, st, admin)
	policy := publishSyntheticAcceptancePolicy(t, st, admin, pack)
	caseID := acceptanceKnowledgeCase(t, st, st, admin)
	accepted, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	withdrawn := advanceKnowledge(t, st, admin, policy, "withdraw", "acceptance-policy-withdraw")
	if _, err := st.RetryAsync(context.Background(), admin, caseID, accepted.RunID); err == nil {
		t.Fatal("active retry bypassed withdrawn mandatory policy")
	}
	if _, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime); err == nil {
		t.Fatal("idempotent start bypassed withdrawn mandatory policy")
	}
	advanceKnowledge(t, st, admin, withdrawn, "rollback", "acceptance-policy-rollback")
	if _, err := st.RetryAsync(context.Background(), admin, caseID, accepted.RunID); err == nil {
		t.Fatal("retry accepted an ABA policy pointer revision")
	}
	next, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil || next.RunID == accepted.RunID {
		t.Fatalf("new policy pointer did not require a new run: %+v %v", next, err)
	}
}

func TestKnowledgeAcceptanceFreezesBusinessInstantAndOptionalRevocation(t *testing.T) {
	st, admin := openExecutionStore(t)
	pack := publishSyntheticAcceptancePack(t, st, admin)
	publishSyntheticAcceptancePolicy(t, st, admin, pack)
	content := knowledgeFixtureContent(pack, "optional-review-guide", "1", "guidance")
	guide := publishKnowledge(t, st, admin, createKnowledgeDraft(t, st, admin, content, "optional-guide-create"), "optional-guide")
	caseID := acceptanceKnowledgeCase(t, st, st, admin)
	first, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	firstWorkspace := readWorkspace(t, st, admin, caseID, first.RunID)
	second, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, "2026-02-03T01:05:06.987654322Z")
	if err != nil || second.RunID == first.RunID {
		t.Fatalf("different business instant reused the old run: %+v %v", second, err)
	}
	secondWorkspace := readWorkspace(t, st, admin, caseID, second.RunID)
	if firstWorkspace.Snapshot.GovernedKnowledge.SnapshotSHA256 == secondWorkspace.Snapshot.GovernedKnowledge.SnapshotSHA256 {
		t.Fatal("distinct business instants produced the same frozen knowledge identity")
	}
	advanceKnowledge(t, st, admin, guide, "withdraw", "optional-guide-withdraw")
	if _, err := st.RetryAsync(context.Background(), admin, caseID, second.RunID); err == nil {
		t.Fatal("active retry accepted a withdrawn frozen optional candidate")
	}
	newRun, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, "2026-02-03T01:05:06.987654322Z")
	if err != nil || newRun.RunID == second.RunID {
		t.Fatalf("withdrawn optional candidate reused its old run: %+v %v", newRun, err)
	}
	current := readWorkspace(t, st, admin, caseID, newRun.RunID)
	if len(current.Snapshot.GovernedKnowledge.OptionalCandidates) != 0 || len(secondWorkspace.Snapshot.GovernedKnowledge.OptionalCandidates) != 1 {
		t.Fatal("optional withdrawal changed historical candidates or entered the new freeze")
	}
}

func TestKnowledgeAcceptanceRechecksAuthorizationRevision(t *testing.T) {
	st, admin := openExecutionStore(t)
	pack := publishSyntheticAcceptancePack(t, st, admin)
	publishSyntheticAcceptancePolicy(t, st, admin, pack)
	caseID := acceptanceKnowledgeCase(t, st, st, admin)
	accepted, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("reviewer missing")
	}
	if err := st.AddMember(admin, caseID, reviewer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RetryAsync(context.Background(), admin, caseID, accepted.RunID); err == nil {
		t.Fatal("active retry ignored the changed case authorization revision")
	}
	next, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil || next.RunID == accepted.RunID {
		t.Fatalf("changed authorization reused its old run: %+v %v", next, err)
	}
	if _, err := st.pool.Exec(context.Background(), `UPDATE cases SET member_ids='[]' WHERE tenant_id=$1 AND id=$2`, admin.TenantID, caseID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime); err == nil {
		t.Fatal("revoked actor reused an accepted run")
	}
	if _, err := st.RetryAsync(context.Background(), admin, caseID, next.RunID); err == nil {
		t.Fatal("revoked actor retried an accepted run")
	}
}

func TestKnowledgeAcceptanceRejectsCorruptSnapshotOnRestart(t *testing.T) {
	st, admin := openExecutionStore(t)
	pack := publishSyntheticAcceptancePack(t, st, admin)
	publishSyntheticAcceptancePolicy(t, st, admin, pack)
	caseID := acceptanceKnowledgeCase(t, st, st, admin)
	accepted, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	workspace := readWorkspace(t, st, admin, caseID, accepted.RunID)
	tampered := *workspace.Snapshot.GovernedKnowledge
	tampered.BusinessAt = "2026-02-03T01:05:06.987654322Z"
	raw, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := st.pool.Exec(ctx, `ALTER TABLE review_run_snapshots DISABLE TRIGGER review_run_snapshots_append_only`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = st.pool.Exec(ctx, `ALTER TABLE review_run_snapshots ENABLE TRIGGER review_run_snapshots_append_only`)
	}()
	if _, err := st.pool.Exec(ctx, `UPDATE review_run_snapshots SET governed_knowledge=$2 WHERE run_id=$1`, accepted.RunID, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(ctx, `ALTER TABLE review_run_snapshots ENABLE TRIGGER review_run_snapshots_append_only`); err != nil {
		t.Fatal(err)
	}
	if reopened, err := st.Reopen(); err == nil {
		_ = reopened.Close()
		t.Fatal("restart accepted a governance snapshot with a stale digest")
	}
}

func TestKnowledgeAcceptanceExecutionRoleReadsButCannotWriteGovernance(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated role checks")
	}
	core, documents, execution := env.openRole(t, "core"), env.openRole(t, "document"), env.openRole(t, "execution")
	admin, _ := accounts.ByID("demo-admin")
	pack := publishSyntheticAcceptancePack(t, core, admin)
	policy := publishSyntheticAcceptancePolicy(t, core, admin, pack)
	caseID := acceptanceKnowledgeCase(t, core, documents, admin)
	execution.UseDocumentPreparer(documents)
	execution.UseModels(&frozenOCRModel{})
	accepted, err := execution.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatalf("execution role could not freeze approved governance: %v", err)
	}
	if _, err := execution.pool.Exec(context.Background(), `UPDATE governed_knowledge_pointers SET active=active WHERE tenant_id=$1 AND knowledge_id=$2`, admin.TenantID, policy.Ref.KnowledgeID); err == nil {
		t.Fatal("execution role could mutate core-owned governance")
	}
	advanceKnowledge(t, core, admin, policy, "withdraw", "role-policy-withdraw")
	if _, err := execution.RetryAsync(context.Background(), admin, caseID, accepted.RunID); err == nil {
		t.Fatal("execution role reused a run after core withdrew its policy")
	}
}
