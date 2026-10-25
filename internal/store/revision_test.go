package store

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/engine"
	"contractaudit/internal/model"
)

func TestRevisedRunLeavesOldSnapshotRow(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenTest(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	st.UseModels(revisionScript{})
	admin := revisionAdmin(t)

	caseID, err := st.CreateCase(admin, "修改稿复审", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	procurement := []byte("采购文件要求质保36个月。")
	draft := []byte("拟签合同约定质保24个月。")
	docA, err := st.AddDocument(context.Background(), admin, caseID, "procurement", "purchase.txt", "text/plain", procurement)
	if err != nil {
		t.Fatal(err)
	}
	docB, err := st.AddDocument(context.Background(), admin, caseID, "draft_contract", "draft.txt", "text/plain", draft)
	if err != nil {
		t.Fatal(err)
	}
	oldID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	oldWS := waitTerminalReview(t, st, admin, caseID, oldID)
	if len(oldWS.Findings) != 1 || oldWS.Findings[0].Relation != "conflict" {
		t.Fatalf("first findings %+v", oldWS.Findings)
	}
	findingID := oldWS.Findings[0].ID
	if err := st.Decide(admin, caseID, oldID, findingID, "keep-1", "confirm_risk", "两份材料质保月数不同", 1); err != nil {
		t.Fatal(err)
	}
	beforeRow := snapshotRow(t, st, oldID)
	beforeFinding := findingRow(t, st, oldID)
	beforeDecision := decisionRow(t, st, oldID)

	revised := []byte("根据谈判，拟签合同约定质保24个月。其余义务不变。")
	docC, err := st.AddDocument(context.Background(), admin, caseID, "draft_contract", "draft-v2.txt", "text/plain", revised)
	if err != nil {
		t.Fatal(err)
	}
	if docC == docB {
		t.Fatal("revised document reused the old id")
	}
	newID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if newID == oldID {
		t.Fatal("re-review updated the old run")
	}
	newWS := waitTerminalReview(t, st, admin, caseID, newID)
	if snapshotRow(t, st, oldID) != beforeRow {
		t.Fatalf("snapshot row changed\nbefore %s\nafter %s", beforeRow, snapshotRow(t, st, oldID))
	}
	if findingRow(t, st, oldID) != beforeFinding {
		t.Fatalf("finding row changed\nbefore %s\nafter %s", beforeFinding, findingRow(t, st, oldID))
	}
	if decisionRow(t, st, oldID) != beforeDecision {
		t.Fatalf("decision row changed\nbefore %s\nafter %s", beforeDecision, decisionRow(t, st, oldID))
	}

	oldWS, err = st.Workspace(admin, caseID, oldID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasIDs(oldWS.Snapshot.DocumentIDs, docA, docB) {
		t.Fatalf("old snapshot ids %+v", oldWS.Snapshot.DocumentIDs)
	}
	if !hasIDs(docIDs(oldWS.Documents), docA, docB) {
		t.Fatalf("old workspace documents %+v", oldWS.Documents)
	}
	var draftName, draftDigest string
	for _, doc := range oldWS.Documents {
		if doc.Document.ID == docB {
			draftName = doc.Document.Filename
			draftDigest = doc.Document.Digest
		}
	}
	if draftName != "draft.txt" || draftDigest == "" {
		t.Fatalf("old draft summary %s %s", draftName, draftDigest)
	}
	if len(oldWS.Findings) != 1 || oldWS.Findings[0].ID != findingID {
		t.Fatalf("old findings %+v", oldWS.Findings)
	}
	kept := st.DecisionsFor(findingID)
	if len(kept) != 1 || kept[0].Action != "confirm_risk" || kept[0].RunID != oldID {
		t.Fatalf("old decisions %+v", kept)
	}

	if !hasIDs(newWS.Snapshot.DocumentIDs, docA, docC) || containsID(newWS.Snapshot.DocumentIDs, docB) {
		t.Fatalf("new snapshot ids %+v", newWS.Snapshot.DocumentIDs)
	}
	if newWS.PreviousRunID != oldID {
		t.Fatalf("previous %s", newWS.PreviousRunID)
	}
	if len(newWS.Findings) != 1 || newWS.Findings[0].ID == findingID || newWS.Findings[0].Relation != "conflict" {
		t.Fatalf("new findings %+v", newWS.Findings)
	}
	if len(st.DecisionsFor(newWS.Findings[0].ID)) != 0 {
		t.Fatal("new run copied the old decision")
	}
	if len(newWS.Revisions) != 1 || newWS.Revisions[0].Mark != "持续" {
		t.Fatalf("revisions %+v", newWS.Revisions)
	}
	if newWS.Revisions[0].Prior == nil || newWS.Revisions[0].Prior.Action != "confirm_risk" || newWS.Revisions[0].Prior.ID != kept[0].ID {
		t.Fatalf("prior %+v", newWS.Revisions[0].Prior)
	}
	again, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil || again != newID {
		t.Fatalf("repeat start %s %v", again, err)
	}
}

func snapshotRow(t *testing.T, st *Store, runID string) string {
	t.Helper()
	var docs, rule, play, modelRef, comp string
	err := st.pool.QueryRow(context.Background(),
		`SELECT document_ids, rule_set_hash, playbook_hash, model_ref, comparator_ref FROM review_run_snapshots WHERE run_id = $1`,
		runID).Scan(&docs, &rule, &play, &modelRef, &comp)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join([]string{docs, rule, play, modelRef, comp}, "\n")
}

func findingRow(t *testing.T, st *Store, runID string) string {
	t.Helper()
	rows, err := st.pool.Query(context.Background(),
		`SELECT id, domain, claim, relation, anchors, version FROM findings WHERE run_id = $1 ORDER BY id`, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, domain, claim, relation, anchors string
		var version int
		if err := rows.Scan(&id, &domain, &claim, &relation, &anchors, &version); err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.Join([]string{id, domain, claim, relation, anchors, strconv.Itoa(version)}, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(out, "\n")
}

func decisionRow(t *testing.T, st *Store, runID string) string {
	t.Helper()
	rows, err := st.pool.Query(context.Background(),
		`SELECT id, finding_id, action, reason, new_disposition FROM decisions WHERE run_id = $1 ORDER BY id`, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, findingID, action, reason, disposition string
		if err := rows.Scan(&id, &findingID, &action, &reason, &disposition); err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.Join([]string{id, findingID, action, reason, disposition}, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(out, "\n")
}

func docIDs(docs []Interpreted) []string {
	ids := make([]string, len(docs))
	for i, doc := range docs {
		ids[i] = doc.Document.ID
	}
	return ids
}

func hasIDs(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	left := append([]string(nil), got...)
	right := append([]string(nil), want...)
	sort.Strings(left)
	sort.Strings(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func revisionAdmin(t *testing.T) accounts.User {
	t.Helper()
	user, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("missing admin")
	}
	return user
}

func waitTerminalReview(t *testing.T, st *Store, user accounts.User, caseID, runID string) Workspace {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		workspace, err := st.Workspace(user, caseID, runID)
		if err != nil {
			t.Fatal(err)
		}
		if workspace.Execution != nil && workspace.Execution.Attempt != nil && !attemptActive(workspace.Execution.Attempt.State) {
			return workspace
		}
		select {
		case <-deadline.C:
			t.Fatalf("review %s did not reach a terminal attempt", runID)
		case <-ticker.C:
		}
	}
}

type revisionScript struct{}

func (revisionScript) Name() string              { return "script-model" }
func (m revisionScript) ConfigurationID() string { return m.Name() }

func (revisionScript) Warranty(_ context.Context, docs []model.Material) (model.Warranty, error) {
	sources := make([]engine.Source, len(docs))
	for i, doc := range docs {
		status := "failed"
		if doc.Ready {
			status = "ready"
		}
		sources[i] = engine.Source{ID: doc.ID, Role: doc.Role, ParseStatus: status, Text: doc.Text}
	}
	drafts := engine.Compare(sources)
	if len(drafts) == 0 {
		return model.Warranty{Claim: "质保月数一致", Relation: "compatible", Disposition: "false_positive"}, nil
	}
	var quotes []model.Quote
	for _, anchor := range drafts[0].Anchors {
		if anchor.Status == "located" && anchor.Quote != "" {
			quotes = append(quotes, model.Quote{DocumentID: anchor.DocumentID, Quote: anchor.Quote})
		}
	}
	quotes = append(quotes, model.Quote{DocumentID: docs[0].ID, Quote: "这句话不在原文中"})
	disposition := "insufficient_evidence"
	if drafts[0].Relation == "conflict" {
		disposition = "confirm_risk"
	}
	return model.Warranty{
		Claim: "模型写成没有差异", Relation: drafts[0].Relation,
		Disposition: disposition, Quotes: quotes,
	}, nil
}

func (revisionScript) Transcribe(context.Context, string, []byte) (string, error) {
	return "", model.ErrUnusable
}

func TestTelecomRevisionComparesAgainstTheCaseCompiler(t *testing.T) {
	store := &Store{}
	caseItem := Case{ID: "case", BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service"}
	before, after := Run{ID: "before", CaseID: caseItem.ID}, Run{ID: "after", CaseID: caseItem.ID}
	matched := currentSignatureFor(nil, nil, nil, telecomCompilerRefForCase(caseItem))
	matched.RulePackCompilerRef = governedCompilerMarker
	drifted := store.currentSignature(nil)
	for _, snapshot := range []*RunSnapshot{&matched, &drifted} {
		snapshot.RulePackID = "telecom-governed"
		snapshot.RulePackVersion = "1"
		snapshot.RulePackHash = "hash"
		snapshot.RulePackDigest = "digest"
	}
	for _, tc := range []struct {
		name    string
		snap    RunSnapshot
		matches bool
	}{
		{"live compiler", matched, true},
		{"compiler dropped from comparator", drifted, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prior, next := tc.snap, tc.snap
			prior.RunID, next.RunID = before.ID, after.ID
			data := state{Cases: []Case{caseItem}, Runs: []Run{before, after}, Snapshots: []RunSnapshot{prior, next}}
			previousID, revisions, _, reason := store.compareRun(&data, after, nil)
			if previousID != before.ID || len(revisions) != 0 {
				t.Fatalf("comparison identity previous=%s revisions=%+v reason=%q", previousID, revisions, reason)
			}
			configMismatch := strings.Contains(reason, "规则或模型配置与当前版本不一致")
			if configMismatch == tc.matches {
				t.Fatalf("matches=%t reason=%q", tc.matches, reason)
			}
		})
	}
}

func TestFrozenRulePackRevisionRequiresTerminalCommit(t *testing.T) {
	for _, tc := range []struct {
		name, state                string
		terminal, output, resolved bool
	}{
		{"completed historical worker", "completed", true, true, true},
		{"partial model with committed rules", "partial", true, true, true},
		{"no terminal receipt", "completed", false, true, false},
		{"no frozen output", "completed", true, false, false},
		{"cancelled after rule calculation", "cancelled", true, true, false},
		{"failed after rule calculation", "failed", true, true, false},
		{"still running", "running", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &Store{}
			before, after := Run{ID: "before", CaseID: "case"}, Run{ID: "after", CaseID: "case"}
			priorSnapshot, nextSnapshot := store.currentSignature(nil), store.currentSignature(nil)
			priorSnapshot.RunID, nextSnapshot.RunID = before.ID, after.ID
			priorAttempt := Attempt{ID: "attempt-before", RunID: before.ID, State: "completed",
				RulePackCompilerRef: "compiler/v1", RulePackHash: "pack", RulePackSnapshotDigest: "snapshot"}
			nextAttempt := priorAttempt
			nextAttempt.ID, nextAttempt.RunID, nextAttempt.State = "attempt-after", after.ID, tc.state
			priorEval := ConstraintEval{RunID: before.ID, AttemptID: priorAttempt.ID,
				RuleID: telecomBandwidthRule, RuleVersion: "1", CapabilityID: "dedicated_line.minimum_bandwidth",
				Domain: "technical", ObjectRefs: []string{"LINE-001"}, FindingID: "risk", Relation: "conflict"}
			nextEval := priorEval
			nextEval.RunID, nextEval.AttemptID, nextEval.FindingID, nextEval.Relation = after.ID, nextAttempt.ID, "", "compatible"
			data := state{Runs: []Run{before, after}, Snapshots: []RunSnapshot{priorSnapshot, nextSnapshot},
				Attempts: []Attempt{priorAttempt, nextAttempt}, Evaluations: []ConstraintEval{priorEval, nextEval},
				Findings: []Finding{{ID: "risk", RunID: before.ID, AttemptID: priorAttempt.ID, Domain: "technical", Relation: "conflict"}},
				ExecutionEvents: []ExecutionEvent{{AttemptID: priorAttempt.ID, Phase: "terminal_persistence", EventType: "attempt_finished", Status: "completed"},
					{AttemptID: nextAttempt.ID, Phase: "rulepack", EventType: "node_completed", Status: "completed"}},
			}
			for i := range data.Attempts {
				if i == 1 && !tc.output {
					continue
				}
				hash, err := attemptOutputHash(data, data.Attempts[i].RunID, data.Attempts[i].ID, "")
				if err != nil {
					t.Fatal(err)
				}
				data.Attempts[i].OutputHash = &hash
			}
			if tc.terminal {
				data.ExecutionEvents = append(data.ExecutionEvents, ExecutionEvent{AttemptID: nextAttempt.ID,
					Phase: "terminal_persistence", EventType: "attempt_finished", Status: tc.state})
			}
			previousID, revisions, _, reason := store.compareRun(&data, after, nil, nextAttempt.ID)
			if !tc.resolved {
				if reason == "" || len(revisions) != 0 {
					t.Fatalf("uncommitted result resolved historical risk: reason=%q revisions=%+v", reason, revisions)
				}
				return
			}
			if previousID != before.ID || reason != "" || len(revisions) != 1 ||
				revisions[0].Mark != "消除" || revisions[0].PreviousFindingID != "risk" || revisions[0].Relation != "compatible" {
				t.Fatalf("committed compatible result did not resolve historical risk: previous=%s reason=%q revisions=%+v", previousID, reason, revisions)
			}
		})
	}
}
