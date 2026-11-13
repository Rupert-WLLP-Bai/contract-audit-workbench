package app

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"contractaudit/internal/engine"
	"contractaudit/internal/model"
)

type testIDs struct {
	n      int
	failAt int
}

func (i *testIDs) NewID(prefix string) (string, error) {
	i.n++
	if i.n == i.failAt {
		return "", errors.New("id failed")
	}
	return fmt.Sprintf("%s%d", prefix, i.n), nil
}

type testClock struct{}

func (testClock) Stamp() string { return "time" }

func TestCitationPlanUsesOrderedFreshViewAndOriginalDedupKey(t *testing.T) {
	attempt := FrozenAttempt{CaseID: "case", RunID: "run", AttemptID: "attempt"}
	docs := []Document{{ID: "doc", ParsedDocumentID: "parse", NormalizedDigest: "digest"}}
	old := Finding{ID: "first", AttemptID: "attempt", Origin: "rule", Domain: "warranty", Relation: "conflict", Claim: "claim", Version: 7, Anchors: []Anchor{{ID: "existing", DocumentID: "doc", ParsedDocumentID: "parse", Quote: "q", Status: "located", Start: 1, End: 2}}}
	later := old
	later.ID = "second"
	draft := engine.Draft{Domain: "warranty", Relation: "conflict", Claim: "claim", Disposition: "confirm_risk", Anchors: []engine.Anchor{{DocumentID: "doc", Quote: "q", Status: "located", Start: 1, End: 2, Reason: "different"}, {DocumentID: "doc", Quote: "new", Status: "located", Start: 3, End: 6}}}
	delta, err := PlanCitationMerge(attempt, docs, []Finding{old, later}, []engine.Draft{draft, draft}, &testIDs{}, testClock{})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Changes) != 2 || delta.Changes[0].Update.FindingID != "first" || len(delta.Changes[0].Update.AppendAnchors) != 1 || len(delta.Changes[1].Update.AppendAnchors) != 0 {
		t.Fatalf("unexpected changes: %+v", delta.Changes)
	}
	if len(delta.Added) != 0 || old.Version != 7 || len(old.Anchors) != 1 {
		t.Fatal("existing candidate was mutated or treated as added")
	}
	a := delta.Changes[0].Update.AppendAnchors[0]
	if a.ParsedDocumentID != "parse" || a.NormalizedDigest != "digest" {
		t.Fatalf("lost frozen identity: %+v", a)
	}
}
func TestCitationPlanNewFindingBecomesNextDraftCandidate(t *testing.T) {
	draft := engine.Draft{Domain: "warranty", Relation: "conflict", Claim: "claim", Anchors: []engine.Anchor{{DocumentID: "d", Quote: "q", Status: "located"}}}
	result, err := PlanCitationMerge(FrozenAttempt{AttemptID: "a"}, nil, nil, []engine.Draft{draft, draft}, &testIDs{}, testClock{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Added) != 1 || len(result.Changes) != 2 || result.Changes[0].Insert == nil || result.Changes[1].Update == nil || result.Changes[1].Update.FindingID != result.Added[0].ID || len(result.Changes[1].Update.AppendAnchors) != 0 || !HasVerifiedFinding(result.Added) {
		t.Fatalf("unexpected plan: %+v", result)
	}
}
func TestCitationPlanIDFailureReturnsNoPartialDelta(t *testing.T) {
	draft := engine.Draft{Anchors: []engine.Anchor{{DocumentID: "d"}}}
	delta, err := PlanCitationMerge(FrozenAttempt{}, nil, nil, []engine.Draft{draft}, &testIDs{failAt: 2}, testClock{})
	if err == nil || len(delta.Changes) != 0 || len(delta.Added) != 0 {
		t.Fatalf("delta=%+v err=%v", delta, err)
	}
}
func TestClassifyOutcomePreservesErrorPrecedence(t *testing.T) {
	missing := errors.New("model is not configured")
	cases := []struct {
		name        string
		input       OutcomeInput
		state, code string
	}{
		{"recorder wins", OutcomeInput{PipelineError: errors.New("pipeline"), RecorderError: errors.New("recorder"), ModelError: model.ErrRequest, RulesPublished: true}, "failed", "execution_persistence_failed"},
		{"request wraps deadline", OutcomeInput{PipelineError: errors.New("pipeline"), ModelError: fmt.Errorf("%w: %w", model.ErrRequest, context.DeadlineExceeded), RulesPublished: true}, "partial", "model_request_failed"},
		{"model missing", OutcomeInput{PipelineError: missing, ModelError: missing, ModelNotConfigured: missing, RulesPublished: true}, "partial", "model_not_configured"},
		{"rules absent", OutcomeInput{PipelineError: model.ErrUnusable, ModelError: model.ErrUnusable}, "failed", "execution_failed"},
		{"incomplete parse", OutcomeInput{ReadyCount: 1, DocumentCount: 2}, "partial", "document_parse_incomplete"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyOutcome(tc.input)
			if got.State != tc.state || got.ErrorCode != tc.code {
				t.Fatalf("got %+v", got)
			}
		})
	}
}
