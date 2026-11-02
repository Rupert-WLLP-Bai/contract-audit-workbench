// File metadata (manifest mirror):
// Purpose: Exercises the `httpapi` package behavior through TestOperatorEvaluationJSONAddsCutoffOnlyForNewResults; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-10-31
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi

import (
	"testing"
	"time"

	"contractaudit/internal/store"
)

func TestOperatorEvaluationJSONAddsCutoffOnlyForNewResults(t *testing.T) {
	legacy := operatorEvaluationJSON(store.OperatorEvaluation{EvaluationID: "eval-legacy"})
	if _, exists := legacy["evidence_cutoff_at"]; exists {
		t.Fatalf("legacy evaluation wire unexpectedly gained an inferred cutoff: %v", legacy)
	}
	cutoff := time.Date(2026, 10, 31, 9, 0, 0, 123000000, time.FixedZone("UTC+8", 8*60*60))
	current := operatorEvaluationJSON(store.OperatorEvaluation{EvaluationID: "eval-current", EvidenceCutoffAt: &cutoff})
	if got, ok := current["evidence_cutoff_at"].(string); !ok || got != "2026-10-31T01:00:00.123Z" {
		t.Fatalf("evaluation wire cutoff was not normalized to UTC: got=%v want=2026-10-31T01:00:00.123Z", current["evidence_cutoff_at"])
	}
}
