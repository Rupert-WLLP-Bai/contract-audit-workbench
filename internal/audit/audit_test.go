package audit_test

import (
	"errors"
	"testing"

	"contractaudit/internal/audit"
)

func TestAuditChainUsesTheFrozenDigest(t *testing.T) {
	first, err := audit.Append(audit.Event{}, audit.Event{
		EventID: "evt_1", RunID: "run_1", Actor: "demo-admin", Seq: 1,
		EventType: "review_started", CorrelationIDs: []string{"case_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Hash != "febdeb20ae85bee740c333b6b4065a5402d38b24e32597172d938785305ad36a" {
		t.Fatalf("hash %s", first.Hash)
	}
	if first.PrevHash != "" {
		t.Fatalf("prev %q", first.PrevHash)
	}
	second, err := audit.Append(first, audit.Event{
		EventID: "evt_2", RunID: "run_1", Actor: "demo-admin", Seq: 2,
		EventType: "human_decision", CorrelationIDs: []string{"case_1", "find_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Hash != "4f3e068bb7a59d9778ce056cf4d32ff90e5a5f7d62477de45054612a5c8000db" {
		t.Fatalf("hash %s", second.Hash)
	}
	if second.PrevHash != first.Hash {
		t.Fatal("chain did not keep the previous digest")
	}
	if err := audit.Verify([]audit.Event{first, second}); err != nil {
		t.Fatal(err)
	}
	_, err = audit.Append(second, audit.Event{
		EventID: "evt_9", RunID: "run_1", Actor: "demo-admin", Seq: 4,
		EventType: "human_decision", CorrelationIDs: []string{"case_1"},
	})
	if !errors.Is(err, audit.ErrBrokenChain) {
		t.Fatalf("gap err %v", err)
	}
	tampered := first
	tampered.Actor = "other"
	if err := audit.Verify([]audit.Event{tampered, second}); !errors.Is(err, audit.ErrBrokenChain) {
		t.Fatalf("tamper err %v", err)
	}
}

func TestSnapshotRejectsARewrite(t *testing.T) {
	frozen, err := audit.Freeze(audit.Snapshot{
		DocumentIDs:   []string{"doc_b", "doc_a"},
		RuleSetHash:   "7ed47fa87ca7fcfe9ff253dd33c00336c4d22f740260e427601ee0c1147c65fc",
		PlaybookHash:  "3fa7e2e0127eb44193a9a2121bec0425340308aeb7f915e347a3f077d9330758",
		ModelRef:      "script-model",
		ComparatorRef: "typed-constraint-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(frozen.DocumentIDs) != 2 || frozen.DocumentIDs[0] != "doc_a" || frozen.DocumentIDs[1] != "doc_b" {
		t.Fatalf("ids %+v", frozen.DocumentIDs)
	}
	rewritten := frozen
	rewritten.DocumentIDs = []string{"doc_a"}
	if err := audit.RejectRewrite(frozen, rewritten); !errors.Is(err, audit.ErrImmutable) {
		t.Fatalf("rewrite err %v", err)
	}
	plannerChanged := frozen
	plannerChanged.PlannerConfigurationID = "different-planner-configuration-sha256"
	if err := audit.RejectRewrite(frozen, plannerChanged); !errors.Is(err, audit.ErrImmutable) {
		t.Fatalf("planner configuration rewrite err %v", err)
	}
	if err := audit.RejectRewrite(frozen, frozen); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotPinsParsedGenerationsAndParserConfiguration(t *testing.T) {
	frozen, err := audit.Freeze(audit.Snapshot{
		DocumentIDs: []string{"doc_b", "doc_a"}, ParsedDocumentIDs: []string{"parsed_b", "parsed_a"},
		ParseSelectionHash: "selection-v1", ParserConfigurationID: "parser-config-v1",
		RuleSetHash: "rules", PlaybookHash: "playbook", ComparatorRef: "typed-constraint-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if frozen.ParsedDocumentIDs[0] != "parsed_a" || frozen.ParsedDocumentIDs[1] != "parsed_b" {
		t.Fatalf("parsed IDs were not frozen in canonical order: %v", frozen.ParsedDocumentIDs)
	}
	selectionChanged := frozen
	selectionChanged.ParseSelectionHash = "selection-v2"
	if err := audit.RejectRewrite(frozen, selectionChanged); !errors.Is(err, audit.ErrImmutable) {
		t.Fatalf("parse generation rewrite err %v", err)
	}
	configurationChanged := frozen
	configurationChanged.ParserConfigurationID = "parser-config-v2"
	if err := audit.RejectRewrite(frozen, configurationChanged); !errors.Is(err, audit.ErrImmutable) {
		t.Fatalf("parser configuration rewrite err %v", err)
	}
	artifactChanged := frozen
	artifactChanged.ParsedDocumentIDs = []string{"parsed_a", "parsed_c"}
	if err := audit.RejectRewrite(frozen, artifactChanged); !errors.Is(err, audit.ErrImmutable) {
		t.Fatalf("parsed artifact identity rewrite err %v", err)
	}
}

func TestSnapshotRejectRewritePinsEveryScalarAndSliceField(t *testing.T) {
	base := audit.Snapshot{
		DocumentIDs: []string{"doc-a", "doc-b"}, ParsedDocumentIDs: []string{"parsed-a"},
		ParseSelectionHash: "selection", ParserConfigurationID: "parser",
		RuleSetHash: "rules", PlaybookHash: "playbook", ModelRef: "model",
		ModelConfigurationID: "model-config", ComparatorRef: "comparator",
		RulePackCompilerRef: "compiler", RulePackID: "pack", RulePackVersion: "pack-v1",
		RulePackHash: "pack-hash", RulePackDigest: "pack-digest",
		PlannerConfigurationID: "planner",
	}
	tests := []struct {
		name   string
		mutate func(*audit.Snapshot)
	}{
		{"rule set hash", func(s *audit.Snapshot) { s.RuleSetHash = "other" }},
		{"playbook hash", func(s *audit.Snapshot) { s.PlaybookHash = "other" }},
		{"model reference", func(s *audit.Snapshot) { s.ModelRef = "other" }},
		{"model configuration", func(s *audit.Snapshot) { s.ModelConfigurationID = "other" }},
		{"comparator", func(s *audit.Snapshot) { s.ComparatorRef = "other" }},
		{"rule pack compiler", func(s *audit.Snapshot) { s.RulePackCompilerRef = "other" }},
		{"rule pack ID", func(s *audit.Snapshot) { s.RulePackID = "other" }},
		{"rule pack version", func(s *audit.Snapshot) { s.RulePackVersion = "other" }},
		{"rule pack hash", func(s *audit.Snapshot) { s.RulePackHash = "other" }},
		{"rule pack digest", func(s *audit.Snapshot) { s.RulePackDigest = "other" }},
		{"planner configuration", func(s *audit.Snapshot) { s.PlannerConfigurationID = "other" }},
		{"parse selection", func(s *audit.Snapshot) { s.ParseSelectionHash = "other" }},
		{"parser configuration", func(s *audit.Snapshot) { s.ParserConfigurationID = "other" }},
		{"documents", func(s *audit.Snapshot) { s.DocumentIDs = []string{"doc-a"} }},
		{"parsed documents", func(s *audit.Snapshot) { s.ParsedDocumentIDs = []string{"parsed-b"} }},
		{"document order", func(s *audit.Snapshot) { s.DocumentIDs = []string{"doc-b", "doc-a"} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			next := base
			tc.mutate(&next)
			if err := audit.RejectRewrite(base, next); !errors.Is(err, audit.ErrImmutable) {
				t.Fatalf("RejectRewrite() error = %v, want ErrImmutable", err)
			}
		})
	}

	withEmptySlice := base
	withEmptySlice.DocumentIDs = nil
	baseEmpty := base
	baseEmpty.DocumentIDs = []string{}
	if err := audit.RejectRewrite(withEmptySlice, baseEmpty); err != nil {
		t.Fatalf("nil and empty zero-length snapshots differ: %v", err)
	}
}

func TestAuditAppendPinsRequiredFieldsSequenceAndRecomputedHashes(t *testing.T) {
	valid := audit.Event{EventID: "event", RunID: "run", Actor: "actor", Seq: 1, EventType: "review"}
	for _, tc := range []struct {
		name   string
		mutate func(*audit.Event)
	}{
		{"missing event ID", func(e *audit.Event) { e.EventID = "" }},
		{"missing run ID", func(e *audit.Event) { e.RunID = "" }},
		{"missing actor", func(e *audit.Event) { e.Actor = "" }},
		{"missing event type", func(e *audit.Event) { e.EventType = "" }},
		{"nonpositive sequence", func(e *audit.Event) { e.Seq = 0 }},
		{"negative sequence", func(e *audit.Event) { e.Seq = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := valid
			tc.mutate(&next)
			if _, err := audit.Append(audit.Event{}, next); !errors.Is(err, audit.ErrBrokenChain) {
				t.Fatalf("Append() error = %v, want ErrBrokenChain", err)
			}
		})
	}
	if _, err := audit.Append(audit.Event{}, audit.Event{EventID: "event", RunID: "run", Actor: "actor", Seq: 2, EventType: "review"}); !errors.Is(err, audit.ErrBrokenChain) {
		t.Fatalf("first sequence error = %v, want ErrBrokenChain", err)
	}
	previous := audit.Event{RunID: "run", Seq: 4, Hash: "previous"}
	for _, tc := range []struct {
		name   string
		mutate func(*audit.Event)
	}{
		{"sequence gap", func(e *audit.Event) { e.Seq = 6 }},
		{"different run", func(e *audit.Event) { e.RunID = "other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := valid
			next.Seq = 5
			tc.mutate(&next)
			if _, err := audit.Append(previous, next); !errors.Is(err, audit.ErrBrokenChain) {
				t.Fatalf("Append() error = %v, want ErrBrokenChain", err)
			}
		})
	}
	negativePrevious := audit.Event{RunID: "run", Seq: -1}
	negativeNext := valid
	negativeNext.Seq = 0
	if _, err := audit.Append(negativePrevious, negativeNext); !errors.Is(err, audit.ErrBrokenChain) {
		t.Fatalf("negative sequence chain error = %v, want ErrBrokenChain", err)
	}
	next := valid
	next.PrevHash, next.Hash = "stale-prev", "caller-hash"
	got, err := audit.Append(audit.Event{}, next)
	if err != nil {
		t.Fatal(err)
	}
	if got.PrevHash != "" || got.Hash == "caller-hash" || got.Hash == "" {
		t.Fatalf("Append() did not recompute chain fields: %+v", got)
	}
}
