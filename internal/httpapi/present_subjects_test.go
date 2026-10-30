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
