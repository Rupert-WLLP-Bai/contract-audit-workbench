// File metadata (manifest mirror):
// Purpose: Verifies that the README demo contract fixtures under testdata/demo-contract still trigger the documented amount, payment, schedule and warranty rule detections.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-20
// Updated: 2026-11-20
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package engine_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"contractaudit/internal/engine"
)

func demoContractSources(t *testing.T) []engine.Source {
	t.Helper()
	docs := make([]engine.Source, 0, 2)
	for _, item := range []struct {
		id   string
		role string
		file string
	}{
		{"procurement", "procurement", "procurement-spec.txt"},
		{"draft", "draft_contract", "draft-contract.txt"},
	} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "demo-contract", item.file))
		if err != nil {
			t.Fatalf("read demo fixture %s: %v", item.file, err)
		}
		docs = append(docs, engine.Source{ID: item.id, Role: item.role, ParseStatus: "ready", Text: string(raw)})
	}
	return docs
}

func TestDemoContractFixturesTriggerDocumentedRules(t *testing.T) {
	docs := demoContractSources(t)
	crossFile := map[string]engine.Evaluation{}
	for _, ev := range engine.CrossFile(docs) {
		crossFile[ev.Domain] = ev
	}
	for _, domain := range []string{"amount", "payment_sum", "payment_condition", "schedule"} {
		if _, ok := crossFile[domain]; !ok {
			t.Fatalf("demo fixtures produced no %s evaluation; got %d cross-file evaluations", domain, len(crossFile))
		}
	}

	if ev := crossFile["amount"]; ev.Relation != engine.RelConflict {
		t.Fatalf("amount = %s, want conflict: %s", ev.Relation, ev.Reason)
	} else if first, second := formatFact(ev.Facts, "procurement"), formatFact(ev.Facts, "draft"); first != "1800000" || second != "1920000" {
		t.Fatalf("amount facts = %s and %s yuan, want 1800000 and 1920000", first, second)
	}

	if ev := crossFile["payment_condition"]; ev.Relation != engine.RelConflict {
		t.Fatalf("payment_condition = %s, want conflict: %s", ev.Relation, ev.Reason)
	}
	if ev := crossFile["payment_sum"]; ev.Relation != engine.RelCompatible {
		t.Fatalf("payment_sum = %s, want compatible: %s", ev.Relation, ev.Reason)
	} else if first, second := paymentTotal(ev.Facts, "procurement"), paymentTotal(ev.Facts, "draft"); first != 100 || second != 100 {
		t.Fatalf("payment totals = %d%% and %d%%, want 100%% and 100%%", first, second)
	}

	if ev := crossFile["schedule"]; ev.Relation != engine.RelConflict {
		t.Fatalf("schedule = %s, want conflict: %s", ev.Relation, ev.Reason)
	}

	warranty := engine.Evaluate(docs)
	if warranty.Relation != engine.RelInsufficient {
		t.Fatalf("warranty = %s, want insufficient: %s", warranty.Relation, warranty.Reason)
	}
	if len(warranty.Uncertain) != 1 {
		t.Fatalf("warranty uncertain anchors = %d, want 1: %+v", len(warranty.Uncertain), warranty.Uncertain)
	}
	if quote := warranty.Uncertain[0].Quote; !strings.HasPrefix(quote, "除") || !strings.Contains(quote, "外") {
		t.Fatalf("warranty uncertain quote does not quote the exception clause: %q", quote)
	}
}

func formatFact(facts []engine.Fact, documentID string) string {
	for _, fact := range facts {
		if fact.DocumentID == documentID {
			return strconv.Itoa(fact.Value)
		}
	}
	return ""
}

func paymentTotal(facts []engine.Fact, documentID string) int {
	total := 0
	for _, fact := range facts {
		if fact.DocumentID == documentID {
			total += fact.Value
		}
	}
	return total
}
