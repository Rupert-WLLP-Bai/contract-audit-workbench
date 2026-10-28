package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/rulepack"
)

// agentReviewFixture opens one durable execution-owned attempt ready to receive
// an agent-review projection and returns the claim plus the frozen input hash.
func agentReviewFixture(t *testing.T) (*Store, JobClaim, string, string) {
	t.Helper()
	st, admin := openExecutionStore(t)
	st.UseModels(&frozenOCRModel{})
	pack := publishSyntheticAcceptancePack(t, st, admin)
	publishSyntheticAcceptancePolicy(t, st, admin, pack)
	caseID, err := st.CreateCaseWithBusiness(admin, "智能审查记录", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	for role, body := range map[string]string{
		"procurement":    "采购文件：电路 LINE-001 最低保障带宽 100 Mbit/s。采购预算：100万元。",
		"draft_contract": "拟签合同：电路 LINE-001 最低保障带宽 100 Mbit/s。合同总价：100万元。",
	} {
		if _, err := st.AddDocument(context.Background(), admin, caseID, role, role+".txt", "text/plain", []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	accepted, err := st.StartReviewAsyncAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := st.ClaimNextJob(context.Background(), "agent-review-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim job: %v", err)
	}
	if claim.JobID != accepted.JobID {
		t.Fatalf("claimed the wrong job: %+v", claim)
	}
	// The worker freezes the attempt input before it writes any projection, so the
	// fixture performs the same preparation step.
	if _, err := st.prepareAttempt(context.Background(), claim.RunID, claim.AttemptID, st.models); err != nil {
		t.Fatalf("freeze attempt input: %v", err)
	}
	input, ok := st.snapshot().attemptInput(claim.AttemptID)
	if !ok || input.InputHash == "" {
		t.Fatal("accepted attempt has no frozen input hash")
	}
	return st, claim, caseID, input.InputHash
}

func agentReviewRecord(claim JobClaim, inputHash string, generation int64, payload []byte) AgentReviewRecord {
	sum := sha256.Sum256(payload)
	return AgentReviewRecord{
		TenantID: claim.TenantID, OrgID: claim.OrgID, CaseID: claim.CaseID,
		RunID: claim.RunID, AttemptID: claim.AttemptID, Generation: generation,
		AttemptInputSHA256: inputHash, AgentReviewSHA256: hex.EncodeToString(sum[:]),
		Review: payload,
	}
}

// TestAgentReviewSyncAndReadBackPreservesExactBytes proves a real write/read
// roundtrip: the persisted projection is readable for an authorized case reader
// with byte-exact content, so its hash still verifies.
func TestAgentReviewSyncAndReadBackPreservesExactBytes(t *testing.T) {
	st, claim, caseID, inputHash := agentReviewFixture(t)
	admin, _ := accounts.ByID("demo-admin")
	// Deliberately non-canonical JSON: key order and spacing must survive.
	payload := []byte(`{ "status" : "completed",  "domains" : [ {"domain":"technical"} ] }`)

	if err := st.SyncAgentReviewRecord(context.Background(), claim, agentReviewRecord(claim, inputHash, claim.Generation, payload)); err != nil {
		t.Fatalf("sync agent review: %v", err)
	}
	got, present, err := st.AgentReviewForAttempt(context.Background(), admin, caseID, claim.RunID, claim.AttemptID)
	if err != nil {
		t.Fatalf("read agent review: %v", err)
	}
	if !present {
		t.Fatal("persisted agent review was not readable")
	}
	if string(got) != string(payload) {
		t.Fatalf("agent review bytes changed on roundtrip:\n got %q\nwant %q", got, payload)
	}
	if sha256Bytes(got) != agentReviewRecord(claim, inputHash, claim.Generation, payload).AgentReviewSHA256 {
		t.Fatal("read-back bytes no longer match the recorded hash")
	}
}

// TestAgentReviewGenerationFence proves a newer live generation replaces an
// unfinalized row, an older generation is refused, and the same generation is
// idempotent only for byte-identical JSON.
func TestAgentReviewGenerationFence(t *testing.T) {
	st, claim, caseID, inputHash := agentReviewFixture(t)
	admin, _ := accounts.ByID("demo-admin")
	first := []byte(`{"round":1}`)
	second := []byte(`{"round":2}`)

	if err := st.SyncAgentReviewRecord(context.Background(), claim, agentReviewRecord(claim, inputHash, claim.Generation, first)); err != nil {
		t.Fatal(err)
	}
	// Same generation, identical bytes: idempotent.
	if err := st.SyncAgentReviewRecord(context.Background(), claim, agentReviewRecord(claim, inputHash, claim.Generation, first)); err != nil {
		t.Fatalf("identical replay was not idempotent: %v", err)
	}
	// Same generation, different bytes: conflict.
	if err := st.SyncAgentReviewRecord(context.Background(), claim, agentReviewRecord(claim, inputHash, claim.Generation, second)); err == nil {
		t.Fatal("same generation with different bytes was accepted")
	}

	// A newer generation replaces the unfinalized row. The job row and the local
	// claim must agree on the new generation, exactly as a reclaim does.
	newer := claim
	newer.Generation = claim.Generation + 1
	if _, err := st.pool.Exec(context.Background(),
		`UPDATE worker_jobs SET generation=$1 WHERE id=$2 AND generation=$3`,
		newer.Generation, claim.JobID, claim.Generation); err != nil {
		t.Fatalf("advance job generation: %v", err)
	}
	st.setClaim(newer)
	if err := st.SyncAgentReviewRecord(context.Background(), newer, agentReviewRecord(newer, inputHash, newer.Generation, second)); err != nil {
		t.Fatalf("newer generation did not replace the unfinalized row: %v", err)
	}
	got, present, err := st.AgentReviewForAttempt(context.Background(), admin, caseID, claim.RunID, claim.AttemptID)
	if err != nil || !present || string(got) != string(second) {
		t.Fatalf("newer generation not visible: %q present=%v err=%v", got, present, err)
	}
	// The older generation can no longer overwrite it. Restore the older claim
	// locally so only the stored generation can refuse the write.
	st.setClaim(claim)
	if err := st.SyncAgentReviewRecord(context.Background(), claim, agentReviewRecord(claim, inputHash, claim.Generation, first)); err == nil {
		t.Fatal("an older generation overwrote a newer row")
	}
}

// TestAgentReviewRejectsWrongFrozenInputAndForeignIdentity proves the projection
// cannot be attached to different frozen input or another claim's identity.
func TestAgentReviewRejectsWrongFrozenInputAndForeignIdentity(t *testing.T) {
	st, claim, _, inputHash := agentReviewFixture(t)
	payload := []byte(`{"round":1}`)

	wrongInput := claim
	if err := st.SyncAgentReviewRecord(context.Background(), wrongInput, AgentReviewRecord{
		TenantID: claim.TenantID, OrgID: claim.OrgID, CaseID: claim.CaseID, RunID: claim.RunID, AttemptID: claim.AttemptID,
		Generation: claim.Generation, AttemptInputSHA256: strings.Repeat("a", 64),
		AgentReviewSHA256: sha256HexString(payload), Review: payload,
	}); err == nil {
		t.Fatal("a projection with a wrong frozen input hash was accepted")
	}
	if err := st.SyncAgentReviewRecord(context.Background(), claim, AgentReviewRecord{
		TenantID: claim.TenantID, OrgID: claim.OrgID, CaseID: claim.CaseID, RunID: claim.RunID, AttemptID: claim.AttemptID,
		Generation: claim.Generation, AttemptInputSHA256: inputHash,
		AgentReviewSHA256: sha256HexString([]byte(`{"other":1}`)), Review: payload,
	}); err == nil {
		t.Fatal("a projection whose hash does not cover its payload was accepted")
	}
	foreign := claim
	foreign.CaseID = "case_foreign"
	if err := st.SyncAgentReviewRecord(context.Background(), foreign, agentReviewRecord(foreign, inputHash, foreign.Generation, payload)); err == nil {
		t.Fatal("a projection for a foreign claim identity was accepted")
	}
}

// TestAgentReviewReadUsesTrustedEnterpriseScope proves the read is authorized and
// tenant-scoped: a non-member cannot read it, and a cross-tenant identity cannot
// see the row.
func TestAgentReviewReadUsesTrustedEnterpriseScope(t *testing.T) {
	st, claim, caseID, inputHash := agentReviewFixture(t)
	payload := []byte(`{"status":"completed"}`)
	if err := st.SyncAgentReviewRecord(context.Background(), claim, agentReviewRecord(claim, inputHash, claim.Generation, payload)); err != nil {
		t.Fatal(err)
	}
	viewer, _ := accounts.ByID("demo-viewer")
	if _, _, err := st.AgentReviewForAttempt(context.Background(), viewer, caseID, claim.RunID, claim.AttemptID); err == nil {
		t.Fatal("a non-member read the agent review")
	}
	foreign := accounts.User{ID: "demo-admin", TenantID: "other-tenant", OrgID: "other-org", Role: "admin", Enabled: true}
	if _, _, err := st.AgentReviewForAttempt(context.Background(), foreign, caseID, claim.RunID, claim.AttemptID); err == nil {
		t.Fatal("a cross-tenant identity read the agent review")
	}
	admin, _ := accounts.ByID("demo-admin")
	if _, present, err := st.AgentReviewForAttempt(context.Background(), admin, caseID, claim.RunID, claim.AttemptID); err != nil || !present {
		t.Fatalf("the authorized case member could not read the projection: present=%v err=%v", present, err)
	}
}

// TestAgentReviewJoinsOutputHashAndRejectsAfterTerminal proves the projection is
// part of the frozen output identity and that a terminal attempt refuses a new
// record.
func TestAgentReviewJoinsOutputHashAndRejectsAfterTerminal(t *testing.T) {
	st, claim, caseID, inputHash := agentReviewFixture(t)
	admin, _ := accounts.ByID("demo-admin")
	payload := []byte(`{"status":"completed"}`)
	if err := st.SyncAgentReviewRecord(context.Background(), claim, agentReviewRecord(claim, inputHash, claim.Generation, payload)); err != nil {
		t.Fatal(err)
	}
	if err := st.RunClaimedJob(context.Background(), claim); err != nil {
		t.Fatalf("run claimed job: %v", err)
	}
	workspace, err := st.Workspace(admin, caseID, claim.RunID, claim.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Execution == nil || workspace.Execution.Attempt == nil || workspace.Execution.Attempt.OutputHash == nil {
		t.Fatal("terminal attempt has no output hash")
	}
	snapshot := snapshotState(st)
	reviewDigest, err := st.agentReviewDigestFor(claim.TenantID, claim.OrgID, claim.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if reviewDigest == "" {
		t.Fatal("the persisted projection did not join the output hash")
	}
	want, err := attemptOutputHash(*snapshot, claim.RunID, claim.AttemptID, reviewDigest)
	if err != nil {
		t.Fatal(err)
	}
	if want != *workspace.Execution.Attempt.OutputHash {
		t.Fatal("the terminal output hash does not include the agent review digest")
	}
	withoutReview, err := attemptOutputHash(*snapshot, claim.RunID, claim.AttemptID, "")
	if err != nil {
		t.Fatal(err)
	}
	if withoutReview == want {
		t.Fatal("the agent review digest did not change the output hash")
	}

	// A terminal attempt refuses a new projection.
	if err := st.SyncAgentReviewRecord(context.Background(), claim, agentReviewRecord(claim, inputHash, claim.Generation+1, []byte(`{"late":true}`))); err == nil {
		t.Fatal("a terminal attempt accepted a new agent review record")
	}
}

func sha256HexString(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// TestFrozenPlannerConfigurationReconstructsApprovedPayload proves the exported
// worker helper rebuilds the approved configuration from the frozen payload and
// refuses a mismatched identity instead of falling back to current env.
func TestFrozenPlannerConfigurationReconstructsApprovedPayload(t *testing.T) {
	st, admin := openExecutionStore(t)
	st.UseModels(&frozenOCRModel{})
	pack := publishSyntheticAcceptancePack(t, st, admin)
	publishSyntheticAcceptancePolicy(t, st, admin, pack)
	caseID, err := st.CreateCaseWithBusiness(admin, "冻结规划器重建", "service", "ENT", "telecom_dedicated_line")
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
	runID, err := st.StartReviewAt(context.Background(), admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshotState(st)
	attempt, ok := snapshot.latestAttempt(runID)
	if !ok {
		t.Fatal("accepted attempt missing")
	}
	configuration, err := FrozenPlannerConfiguration(attempt)
	if err != nil {
		t.Fatalf("reconstruct frozen planner configuration: %v", err)
	}
	if configuration.ConfigurationID != attempt.PlannerConfigurationID || configuration.ConfigurationID == "" {
		t.Fatalf("reconstructed identity does not match the frozen one: %+v", configuration)
	}
	if configuration.Limits.MaxRounds <= 0 || configuration.Limits.MaxCalls <= 0 {
		t.Fatalf("reconstructed payload lost its limits: %+v", configuration.Limits)
	}

	// A tampered identity must fail closed rather than silently re-resolve.
	tampered := attempt
	tampered.PlannerConfigurationID = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := FrozenPlannerConfiguration(tampered); !errors.Is(err, ErrFrozenPlannerMismatch) {
		t.Fatalf("mismatched frozen identity was accepted: %v", err)
	}
	empty := attempt
	empty.PlannerConfiguration = nil
	if _, err := FrozenPlannerConfiguration(empty); !errors.Is(err, ErrFrozenPlannerMismatch) {
		t.Fatalf("missing frozen payload was accepted: %v", err)
	}
	legacy, err := FrozenPlannerConfiguration(Attempt{})
	if !errors.Is(err, ErrFrozenPlannerAbsent) || legacy.ConfigurationID != "" {
		t.Fatalf("legacy attempt did not remain explicitly planner-free: config=%+v err=%v", legacy, err)
	}
	identityOnly := Attempt{PlannerConfigurationID: attempt.PlannerConfigurationID}
	if _, err := FrozenPlannerConfiguration(identityOnly); !errors.Is(err, ErrFrozenPlannerMismatch) {
		t.Fatalf("attempt with planner identity but no payload was accepted: %v", err)
	}
	payloadOnly := attempt
	payloadOnly.PlannerConfigurationID = ""
	if _, err := FrozenPlannerConfiguration(payloadOnly); !errors.Is(err, ErrFrozenPlannerMismatch) {
		t.Fatalf("attempt with planner payload but no identity was accepted: %v", err)
	}
	alteredAttempt := attempt
	alteredAttempt.PlannerConfiguration = append(json.RawMessage(nil), attempt.PlannerConfiguration...)
	alteredAttempt.PlannerConfiguration[0] = ' '
	if err := validateAttemptPlannerHistory([]Attempt{attempt}, []Attempt{alteredAttempt}); err == nil {
		t.Fatal("accepted attempt planner payload changed without an identity transition")
	}
}

func TestSamePlannerConfigurationRequiresCompleteFrozenPair(t *testing.T) {
	legacy := RunSnapshot{}
	if !samePlannerConfiguration(legacy, RunSnapshot{}) {
		t.Fatal("historical absent planner identity and payload stopped being comparable")
	}
	frozen := RunSnapshot{
		PlannerConfigurationID: "planner-sha256",
		PlannerConfiguration:   json.RawMessage(`{"mode":"deterministic"}`),
	}
	if !samePlannerConfiguration(frozen, frozen) {
		t.Fatal("identical planner identity and payload were not comparable")
	}
	if samePlannerConfiguration(RunSnapshot{PlannerConfigurationID: frozen.PlannerConfigurationID}, frozen) {
		t.Fatal("planner identity without its payload was treated as valid")
	}
	changed := frozen
	changed.PlannerConfiguration = json.RawMessage(`{"mode":"fixed_chain"}`)
	if samePlannerConfiguration(frozen, changed) {
		t.Fatal("different planner payload bytes retained the same run identity")
	}
}

// TestReportEmbedsPersistedAgentReview proves a frozen report carries the exact
// persisted five-domain projection and its digest, so its exported JSON can
// reproduce the canonical output hash.
func TestReportEmbedsPersistedAgentReview(t *testing.T) {
	t.Setenv("AGENT_PLANNER_MODE", "deterministic")
	st, claim, caseID, _ := agentReviewFixture(t)
	admin, _ := accounts.ByID("demo-admin")
	if err := st.RunClaimedJob(context.Background(), claim); err != nil {
		t.Fatalf("run claimed job: %v", err)
	}
	terminalWorkspace := waitTerminalWorkspace(t, st, admin, caseID, claim.RunID)
	payload, present, err := st.AgentReviewForAttempt(context.Background(), admin, caseID, claim.RunID, claim.AttemptID)
	if err != nil {
		t.Fatalf("read persisted agent review: %v", err)
	}
	if !present {
		type stageState struct {
			Phase, EventType, Status, ErrorCode string
		}
		stages := make([]stageState, 0, len(terminalWorkspace.Execution.Events))
		for _, event := range terminalWorkspace.Execution.Events {
			code := ""
			if event.ErrorCode != nil {
				code = *event.ErrorCode
			}
			stages = append(stages, stageState{Phase: event.Phase, EventType: event.EventType, Status: event.Status, ErrorCode: code})
		}
		t.Fatalf("agent review runtime did not persist its five-domain projection: attempt_state=%q run_state=%q partial_reason=%q failure=%q stage_events=%+v",
			terminalWorkspace.Execution.Attempt.State, terminalWorkspace.View.State, terminalWorkspace.View.PartialReason, terminalWorkspace.View.FailureMessage, stages)
	}
	workspace, err := st.Workspace(admin, caseID, claim.RunID, claim.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Execution == nil || workspace.Execution.Attempt == nil || workspace.Execution.Attempt.OutputHash == nil ||
		workspace.Execution.Attempt.FinishedAt == nil || workspace.Execution.Attempt.State != "completed" && workspace.Execution.Attempt.State != "partial" {
		t.Fatalf("agent-review attempt did not reach a reportable terminal state: %+v", workspace.Execution)
	}
	report, err := st.CreateReport(admin, caseID, claim.RunID, claim.AttemptID)
	if err != nil {
		t.Fatalf("create report: %v", err)
	}
	_, raw, err := st.ReadReport(admin, caseID, report.ID, "json")
	if err != nil {
		t.Fatal(err)
	}
	var envelope reportEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(envelope.AgentReviewBytes, payload) {
		t.Fatalf("report did not preserve exact AgentReview bytes: got=%q want=%q", envelope.AgentReviewBytes, payload)
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, payload); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(envelope.AgentReview, compacted.Bytes()) {
		t.Fatalf("report projection differs from the exact JSON payload: got=%s want=%s", envelope.AgentReview, compacted.Bytes())
	}
	if envelope.AgentReviewSHA256 != sha256Bytes(payload) || sha256Bytes(envelope.AgentReviewBytes) != envelope.AgentReviewSHA256 {
		t.Fatalf("report AgentReview digest=%q, payload digest=%q", envelope.AgentReviewSHA256, sha256Bytes(envelope.AgentReviewBytes))
	}
	var projection struct {
		RulePack *struct {
			ID            string `json:"id"`
			Version       string `json:"version"`
			ContentSHA256 string `json:"content_sha256"`
		} `json:"rulepack"`
		SourceSnapshot json.RawMessage   `json:"source_snapshot"`
		Plan           json.RawMessage   `json:"plan"`
		Gaps           []json.RawMessage `json:"gaps"`
		Domains        []json.RawMessage `json:"domains"`
		Evidence       []struct {
			EvidenceID   string `json:"evidence_id"`
			SemanticType string `json:"semantic_type"`
			Value        string `json:"value"`
			Verification string `json:"verification"`
			Anchor       struct {
				DocumentID string `json:"document_id"`
				Quote      string `json:"quote"`
				Status     string `json:"status"`
			} `json:"anchor"`
		} `json:"evidence"`
	}
	if err := json.Unmarshal(envelope.AgentReview, &projection); err != nil {
		t.Fatal(err)
	}
	frozenAttempt := workspace.Execution.Attempt
	if frozenAttempt.RulePackCompilerRef != governedCompilerMarker || frozenAttempt.RulePackID != "telecom-governed" ||
		frozenAttempt.RulePackVersion != "1" || frozenAttempt.RulePackPointerRevision != 1 {
		t.Fatalf("fixture did not retain the published governed RulePack identity: %+v", frozenAttempt)
	}
	frozenPack, err := rulepack.Parse(frozenAttempt.RulePackSnapshot)
	if err != nil {
		t.Fatalf("parse frozen governed RulePack manifest: %v", err)
	}
	manifest := frozenPack.Manifest()
	if frozenPack.Hash() != frozenAttempt.RulePackHash || sha256Bytes(frozenAttempt.RulePackSnapshot) != frozenAttempt.RulePackSnapshotDigest {
		t.Fatalf("attempt RulePack identity does not match frozen manifest bytes: %+v", frozenAttempt)
	}
	if projection.RulePack == nil || projection.RulePack.ID != manifest.ID || projection.RulePack.Version != manifest.Version ||
		projection.RulePack.ContentSHA256 != frozenPack.Hash() {
		t.Fatalf("persisted AgentReview RulePack identity does not match frozen manifest: got=%+v manifest=%+v hash=%s",
			projection.RulePack, manifest, frozenPack.Hash())
	}
	if len(projection.SourceSnapshot) == 0 || len(projection.Plan) == 0 || len(projection.Gaps) == 0 ||
		len(projection.Domains) != 5 || len(projection.Evidence) == 0 {
		t.Fatalf("report omitted five-domain sources, plan, gaps or typed facts: %+v", projection)
	}
	verifiedAmount := false
	for _, fact := range projection.Evidence {
		if fact.EvidenceID != "" && fact.Value == "1000000" &&
			fact.Verification == "typed_semantic_and_frozen_quote_verified" &&
			fact.Anchor.DocumentID != "" && fact.Anchor.Status == "located" &&
			((fact.SemanticType == "amount_ceiling" && fact.Anchor.Quote == "采购预算：100万元") ||
				(fact.SemanticType == "amount_total" && fact.Anchor.Quote == "合同总价：100万元")) {
			verifiedAmount = true
			break
		}
	}
	if !verifiedAmount {
		t.Fatalf("report omitted a located typed amount fact with its frozen-source quote: %+v", projection.Evidence)
	}
	proofAttempt := envelope.Attempt
	proofAttempt.RulePackHash = envelope.RulePack.Hash
	proofAttempt.RulePackSnapshotDigest = envelope.RulePack.SnapshotDigest
	proofAttempt.RulePackCompilerRef = envelope.RulePack.CompilerRef
	proofState := state{Findings: envelope.Findings, Evaluations: envelope.Evaluations, Attempts: []Attempt{proofAttempt}}
	reproducedOutputHash, err := attemptOutputHash(proofState, envelope.Run.ID, proofAttempt.ID, envelope.AgentReviewSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if reproducedOutputHash != envelope.OutputHash || envelope.OutputHash != *workspace.Execution.Attempt.OutputHash {
		t.Fatalf("exported report cannot reproduce output hash: report=%s reproduced=%s attempt=%s", envelope.OutputHash, reproducedOutputHash, *workspace.Execution.Attempt.OutputHash)
	}

	reopened, err := st.Reopen()
	if err != nil {
		t.Fatalf("reopen frozen report: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopenedRecord, reopenedRaw, err := reopened.ReadReport(admin, caseID, report.ID, "json")
	if err != nil {
		t.Fatalf("read report after reopen: %v", err)
	}
	if !bytes.Equal(reopenedRaw, raw) {
		t.Fatal("reopened report did not return the exact frozen JSON bytes")
	}
	if reopenedRecord.Digest != report.Digest {
		t.Fatalf("reopened report digest changed: got=%q want=%q", reopenedRecord.Digest, report.Digest)
	}
}
