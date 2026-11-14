// File metadata (manifest mirror):
// Purpose: Characterizes canonical app append and head selection using existing hash verification without changing frozen history.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app_test

import (
	"testing"

	"contractaudit/internal/audit"
	"contractaudit/internal/audit/app"
)

func TestAppendUsesSelectedHeadAndCanonicalChain(t *testing.T) {
	first, err := app.PrepareAppend(audit.Event{}, false, app.Draft{
		EventID: "evt_first", RunID: "run_1", Actor: "reviewer", EventType: "review_started",
		CorrelationIDs: []string{"case_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.PrepareAppend(first, true, app.Draft{
		EventID: "evt_second", RunID: "run_1", Actor: "reviewer", EventType: "human_decision",
		CorrelationIDs: []string{"case_1", "finding_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	selected, found := app.SelectHead([]audit.Event{
		{EventID: "evt_other_run", RunID: "run_2", Seq: 9}, first, second,
	}, app.RunRef{ID: "run_1"})
	if !found || selected.EventID != second.EventID {
		t.Fatalf("selected head = %+v, found=%v", selected, found)
	}
	third, err := app.PrepareAppend(selected, found, app.Draft{
		EventID: "evt_third", RunID: "run_1", Actor: "reviewer", EventType: "review_cancelled",
		CorrelationIDs: []string{"case_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if third.Seq != 3 || third.PrevHash != second.Hash {
		t.Fatalf("third event does not extend selected head: %+v", third)
	}
	if err := audit.Verify([]audit.Event{first, second, third}); err != nil {
		t.Fatalf("canonical audit chain failed verification: %v", err)
	}
}
