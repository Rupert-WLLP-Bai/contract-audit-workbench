// File metadata (manifest mirror):
// Purpose: Characterizes durable-role citation field preservation, ordered deduplication and refusal of materially different writes from a superseded generation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"contractaudit/internal/engine"
	executionapp "contractaudit/internal/execution/app"
)

// The bridge applies a plan against live scoped state and cannot publish from
// the generation superseded by a different execution-role connection.
func TestStandardCitationBridgePreservesFieldsAndFencesTakeover(t *testing.T) {
	fixture, ok := newDurableFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	ctx := context.Background()
	oldCtx := withExecutionClaim(ctx, fixture.claim)
	severity := "high"
	original := Finding{ID: "find_standard_bridge", RunID: fixture.runID, AttemptID: fixture.attemptID, CaseID: fixture.caseID, Origin: "rule", Domain: "warranty", RuleID: "rule", CapabilityID: "capability", ObjectRef: "object", Claim: "claim", Relation: "conflict", Version: 7, CreatedAt: "original", Severity: &severity, SeveritySource: &FindingSeveritySource{PolicyID: "policy"}, Anchors: []Anchor{{ID: "existing", DocumentID: "doc", ParsedDocumentID: "parse", Quote: "old", Status: "located", Start: 1, End: 4}}}
	if err := fixture.execution.updateExecutionContext(oldCtx, fixture.attemptID, func(st *state) error { st.Findings = append(st.Findings, original); return nil }); err != nil {
		t.Fatal(err)
	}
	bridge := standardExecutionBridge{store: fixture.execution, parent: ctx}
	attempt := executionapp.FrozenAttempt{RunID: fixture.runID, AttemptID: fixture.attemptID}
	docs := []executionapp.Document{{ID: "doc", ParsedDocumentID: "parse", NormalizedDigest: "digest"}}
	draft := engine.Draft{Domain: "warranty", Claim: "claim", Relation: "conflict", Disposition: "confirm_risk", UnverifiedReason: "reason", Anchors: []engine.Anchor{{DocumentID: "doc", Quote: "old", Status: "located", Start: 1, End: 4}, {DocumentID: "doc", Quote: "new", Status: "located", Start: 5, End: 8}}}
	delta, err := bridge.MergeCitationDrafts(oldCtx, attempt, docs, []engine.Draft{draft, draft})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Added) != 0 {
		t.Fatalf("existing finding treated as new: %+v", delta.Added)
	}
	got := durableSnapshot(t, fixture.execution, fixture.claim).findingsForAttempt(fixture.runID, fixture.attemptID)
	if len(got) != 1 {
		t.Fatalf("findings=%+v", got)
	}
	expected := original
	expected.DispositionSuggestion = "confirm_risk"
	expected.UnverifiedReason = "reason"
	if len(got[0].Anchors) != 2 {
		t.Fatalf("anchors=%+v", got[0].Anchors)
	}
	anchor := got[0].Anchors[1]
	if anchor.DocumentID != "doc" || anchor.ParsedDocumentID != "parse" || anchor.NormalizedDigest != "digest" || anchor.Quote != "new" {
		t.Fatalf("new anchor=%+v", anchor)
	}
	expected.Anchors = got[0].Anchors
	if !reflect.DeepEqual(got[0], expected) {
		t.Fatalf("existing fields changed: got=%+v want=%+v", got[0], expected)
	}
	expireDurableClaim(t, fixture)
	takeoverStore := fixture.environment.openRole(t, "execution")
	takeover, err := takeoverStore.ClaimNextJob(ctx, "citation-takeover", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if takeover.Generation != fixture.claim.Generation+1 {
		t.Fatalf("unexpected takeover: %+v", takeover)
	}
	before := durableMutationSnapshot(t, fixture, takeover)
	findingsBefore := durableSnapshot(t, takeoverStore, takeover).findingsForAttempt(fixture.runID, fixture.attemptID)
	if len(findingsBefore) != 1 || !reflect.DeepEqual(findingsBefore[0], got[0]) {
		t.Fatalf("takeover did not retain the expected finding: %+v", findingsBefore)
	}
	staleDraft := draft
	staleDraft.Disposition = "insufficient_evidence"
	staleDraft.UnverifiedReason = "stale generation change"
	staleDraft.Anchors = []engine.Anchor{{DocumentID: "doc", Quote: "stale", Status: "located", Start: 9, End: 14}}
	if _, err = bridge.MergeCitationDrafts(oldCtx, attempt, docs, []engine.Draft{staleDraft}); !errors.Is(err, ErrLeaseLost) && !errors.Is(err, ErrToolScopeUnavailable) {
		t.Fatalf("stale generation merge: %v", err)
	}
	after := durableMutationSnapshot(t, fixture, takeover)
	findingsAfter := durableSnapshot(t, takeoverStore, takeover).findingsForAttempt(fixture.runID, fixture.attemptID)
	if !reflect.DeepEqual(findingsBefore, findingsAfter) {
		t.Fatalf("stale citation merge changed findings or anchors: before=%+v after=%+v", findingsBefore, findingsAfter)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("stale citation merge changed durable state")
	}
}
