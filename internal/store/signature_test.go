package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"contractaudit/internal/fault"
	"contractaudit/internal/model"
)

type countingModel struct {
	name   string
	calls  atomic.Int64
	onCall func()
}

func (m *countingModel) Name() string            { return m.name }
func (m *countingModel) ConfigurationID() string { return m.Name() }
func (m *countingModel) Warranty(ctx context.Context, docs []model.Material) (model.Warranty, error) {
	m.calls.Add(1)
	if m.onCall != nil {
		m.onCall()
	}
	return (revisionScript{}).Warranty(ctx, docs)
}
func (*countingModel) Transcribe(context.Context, string, []byte) (string, error) {
	return "", model.ErrUnusable
}

func TestSignatureIncludesEveryFrozenInput(t *testing.T) {
	s := &Store{models: &countingModel{name: "v1"}}
	base := s.currentSignature([]string{"doc_a", "doc_b"})
	if !sameRunSignature(base, s.currentSignature([]string{"doc_b", "doc_a"})) {
		t.Fatal("document order changed signature")
	}
	for name, change := range map[string]func(*RunSnapshot){
		"documents":  func(s *RunSnapshot) { s.DocumentIDs = []string{"different"} },
		"rules":      func(s *RunSnapshot) { s.RuleSetHash += "changed" },
		"playbook":   func(s *RunSnapshot) { s.PlaybookHash += "changed" },
		"comparator": func(s *RunSnapshot) { s.ComparatorRef += "changed" },
		"model":      func(s *RunSnapshot) { s.ModelRef += "changed" },
	} {
		changed := base
		change(&changed)
		if sameRunSignature(base, changed) {
			t.Fatalf("signature omitted %s", name)
		}
	}
}

func TestSubjectContextChangeCreatesNewRunAndKeepsPriorReviewFrozen(t *testing.T) {
	s, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	admin := revisionAdmin(t)
	model := &countingModel{name: "subject-context-v1"}
	s.UseModels(model)
	caseID, err := s.CreateCase(admin, "主体上下文变更需要新审查", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	for role, body := range map[string]string{"procurement": "质保36个月。", "draft_contract": "质保24个月。"} {
		if _, err := s.AddDocument(context.Background(), admin, caseID, role, role+".txt", "text/plain", []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	run1, err := s.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	oldReview := waitTerminalReview(t, s, admin, caseID, run1)
	created, replayed, err := s.CreateCaseSubject(context.Background(), admin, caseID, CaseSubjectCreateInput{
		Role: SubjectRoleSupplier, Subject: SubjectCreateInput{DisplayName: "新加入的供应商"},
	}, "subject-context-new-party")
	if err != nil || replayed || created.SubjectID == "" {
		t.Fatalf("add subject context: result=%+v replayed=%v err=%v", created, replayed, err)
	}
	afterSubjectChange, err := s.GetCase(admin, caseID)
	if err != nil || !afterSubjectChange.SubjectContextChangedAfterLatestRun {
		t.Fatalf("case did not expose stale subject context: detail=%+v err=%v", afterSubjectChange, err)
	}
	var conflict *fault.Error
	if err := s.Retry(context.Background(), admin, caseID, run1); !errors.As(err, &conflict) || conflict.Status != 409 || !strings.Contains(err.Error(), "主体信息已变更") || model.calls.Load() != 1 {
		t.Fatalf("retry did not explain the stale subject context: err=%v calls=%d", err, model.calls.Load())
	}
	run2, err := s.StartReview(context.Background(), admin, caseID)
	if err != nil || run2 == run1 {
		t.Fatalf("changed party context did not create a new review: run1=%s run2=%s err=%v", run1, run2, err)
	}
	newReview := waitTerminalReview(t, s, admin, caseID, run2)
	if newReview.Snapshot.PartyContextHash == oldReview.Snapshot.PartyContextHash || len(newReview.Snapshot.PartyContext) != 1 || newReview.Snapshot.PartyContext[0].SubjectID != created.SubjectID {
		t.Fatalf("new review did not freeze the current subject context: old=%+v new=%+v", oldReview.Snapshot, newReview.Snapshot)
	}
	afterNewReview, err := s.GetCase(admin, caseID)
	if err != nil || afterNewReview.SubjectContextChangedAfterLatestRun {
		t.Fatalf("current subject context remains pending after matching review: detail=%+v err=%v", afterNewReview, err)
	}
	if same, err := s.StartReview(context.Background(), admin, caseID); err != nil || same != run2 || model.calls.Load() != 2 {
		t.Fatalf("unchanged subject context failed to reuse latest run: run=%s err=%v calls=%d", same, err, model.calls.Load())
	}
	oldAfter, err := s.Workspace(admin, caseID, run1)
	oldAfter.Case = oldReview.Case
	if err != nil || !reflect.DeepEqual(oldReview, oldAfter) {
		t.Fatalf("subject update rewrote the frozen prior review/report: err=%v\nbefore=%+v\nafter=%+v", err, oldReview, oldAfter)
	}
}

func TestModelChangeRequiresNewRunAndPreservesHistory(t *testing.T) {
	s, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	m1 := &countingModel{name: "v1"}
	s.UseModels(m1)
	admin := revisionAdmin(t)
	caseID, err := s.CreateCase(admin, "配置版本不可漂移", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	for role, body := range map[string]string{"procurement": "质保36个月。", "draft_contract": "质保24个月。"} {
		if _, err := s.AddDocument(context.Background(), admin, caseID, role, role+".txt", "text/plain", []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	run1, err := s.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	before := waitTerminalReview(t, s, admin, caseID, run1)
	if same, err := s.StartReview(context.Background(), admin, caseID); err != nil || same != run1 || m1.calls.Load() != 1 {
		t.Fatalf("unchanged inputs not idempotent: %q %v calls=%d", same, err, m1.calls.Load())
	}
	m2 := &countingModel{name: "v2"}
	s.UseModels(m2)
	detail, err := s.GetCase(admin, caseID)
	if err != nil || !detail.ConfigurationPending || detail.RevisionPending {
		t.Fatalf("configuration change hidden: %+v %v", detail, err)
	}
	err = s.Retry(context.Background(), admin, caseID, run1)
	var fe *fault.Error
	if !errors.As(err, &fe) || fe.Status != 409 || m2.calls.Load() != 0 {
		t.Fatalf("drift was not rejected before model call: %v calls=%d", err, m2.calls.Load())
	}
	run2, err := s.StartReview(context.Background(), admin, caseID)
	if err != nil || run2 == run1 {
		t.Fatalf("changed config did not create run: %q %v", run2, err)
	}
	next := waitTerminalReview(t, s, admin, caseID, run2)
	if m2.calls.Load() != 1 {
		t.Fatalf("new run did not use its model: calls=%d", m2.calls.Load())
	}
	old, err := s.Workspace(admin, caseID, run1)
	if err != nil {
		t.Fatal(err)
	}
	if !sameRunSignature(old.Snapshot, before.Snapshot) || old.Findings[0].ID != before.Findings[0].ID || len(old.Audit) != len(before.Audit) {
		t.Fatal("changed config rewrote the previous run")
	}
	if next.Snapshot.ModelRef != "v2" || next.PreviousRunID != run1 || next.RevisionComparisonReason == "" || len(next.Revisions) != 0 {
		t.Fatalf("cross-version comparison was silently invented: %+v", next)
	}
	if same, err := s.StartReview(context.Background(), admin, caseID); err != nil || same != run2 || m2.calls.Load() != 1 {
		t.Fatalf("new config not idempotent: %q %v", same, err)
	}
	if err := s.Retry(context.Background(), admin, caseID, run2); err != nil {
		t.Fatalf("same-signature retry refused: %v", err)
	}
	retried := waitTerminalReview(t, s, admin, caseID, run2)
	if m2.calls.Load() != 2 || retried.Execution.Attempt.ID == next.Execution.Attempt.ID || retried.Snapshot.ModelRef != "v2" {
		t.Fatalf("retry did not retain the frozen model and append an attempt: %+v", retried.Execution)
	}
	oldAttempt, err := s.Workspace(admin, caseID, run2, next.Execution.Attempt.ID)
	if err != nil || oldAttempt.Execution.Attempt.OutputHash == nil || *oldAttempt.Execution.Attempt.OutputHash != *next.Execution.Attempt.OutputHash {
		t.Fatalf("retry rewrote the original model output: %v", err)
	}
	// A configuration replacement during execution cannot change the admitted model.
	// The hook runs in the model invocation; the test waits before reading the replacement.
	m3 := &countingModel{name: "v3"}
	m2.onCall = func() { s.UseModels(m3) }
	if err := s.Retry(context.Background(), admin, caseID, run2); err != nil {
		t.Fatal(err)
	}
	pinned := waitTerminalReview(t, s, admin, caseID, run2)
	if pinned.Snapshot.ModelRef != "v2" || m2.calls.Load() != 3 || m3.calls.Load() != 0 || *pinned.Execution.Attempt.InputHash != *retried.Execution.Attempt.InputHash {
		t.Fatalf("configuration replacement mixed an admitted attempt: %+v", pinned.Execution)
	}
	if err := s.Retry(context.Background(), admin, caseID, run2); !errors.As(err, &fe) || fe.Status != 409 || m3.calls.Load() != 0 {
		t.Fatalf("new retry accepted changed configuration: %v", err)
	}
	run3, err := s.StartReview(context.Background(), admin, caseID)
	if err != nil || run3 == run2 {
		t.Fatalf("replacement configuration did not create a new run: %s %v", run3, err)
	}
	configured := waitTerminalReview(t, s, admin, caseID, run3)
	if configured.Snapshot.ModelRef != "v3" || m3.calls.Load() != 1 {
		t.Fatalf("replacement run did not use its configured model: %+v", configured.Snapshot)
	}
}
