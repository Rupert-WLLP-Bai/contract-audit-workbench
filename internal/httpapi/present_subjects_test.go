// File metadata (manifest mirror):
// Purpose: Exercises the `httpapi` package behavior through TestCaseDetailJSONIncludesSubjectContextReviewState; these checks pin observable state, error handling, or wire/database compatibility for this module.
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

	"contractaudit/internal/store"
)

func TestCaseDetailJSONIncludesSubjectContextReviewState(t *testing.T) {
	for _, changed := range []bool{false, true} {
		body := detailJSON(store.CaseDetail{SubjectContextChangedAfterLatestRun: changed})
		got, ok := body["subject_context_changed_after_latest_run"].(bool)
		if !ok || got != changed {
			t.Fatalf("subject context state missing or incorrect: changed=%v body=%v", changed, body)
		}
	}
}
