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
