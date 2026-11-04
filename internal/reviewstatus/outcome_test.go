// File metadata (manifest mirror):
// Purpose: Verifies selected-attempt review outcome classification, contradictory and legacy inputs, stage failures, and narrow transient retry hints.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package reviewstatus

import "testing"

func TestProjectSelectedFrozenAttempt(t *testing.T) {
	base := Input{RunID: "run", AttemptID: "attempt", AttemptRunID: "run", HistoryKind: "frozen", State: "partial", ErrorCode: "precheck_scope_limited"}
	tests := []struct {
		name string
		edit func(*Input)
		want Outcome
	}{
		{"scope alone is not an agent success", func(*Input) {}, Outcome{"scope_limited", "unknown", "none"}},
		{"failed scope code is contradictory", func(in *Input) { in.State = "failed" }, Outcome{"unknown", "unknown", "none"}},
		{"interrupted scope code is contradictory", func(in *Input) { in.State = "interrupted" }, Outcome{"unknown", "unknown", "none"}},
		{"agent failed within limited scope", func(in *Input) {
			in.Events = []Event{{"attempt", 2, "agent_review", "agent_review_result", "agent_review_event", "partial", "agent_review_failed"}}
		}, Outcome{"scope_limited", "failed", "none"}},
		{"agent incomplete within limited scope", func(in *Input) {
			in.Events = []Event{{"attempt", 2, "agent_review", "agent_review_result", "agent_review_event", "partial", "agent_review_incomplete"}}
		}, Outcome{"scope_limited", "partial", "none"}},
		{"foreign attempt is ignored", func(in *Input) {
			in.Events = []Event{{"other", 3, "agent_review", "agent_review_result", "agent_review_event", "completed", ""}}
		}, Outcome{"scope_limited", "unknown", "none"}},
		{"unrecognized newer stage is unknown", func(in *Input) {
			in.Events = []Event{{"attempt", 2, "agent_review", "agent_review_result", "agent_review_event", "completed", ""}, {"attempt", 3, "agent_review", "agent_review_result", "agent_review_event", "future", ""}}
		}, Outcome{"scope_limited", "unknown", "none"}},
		{"other node is ignored", func(in *Input) {
			in.Events = []Event{{"attempt", 3, "model_call", "agent_review_result", "agent_review_event", "completed", ""}}
		}, Outcome{"scope_limited", "unknown", "none"}},
		{"timeout is a retry hint", func(in *Input) { in.ErrorCode = "model_timeout" }, Outcome{"transient_failure", "unknown", "model_timeout"}},
		{"missing parse is not a retry", func(in *Input) { in.ErrorCode = "document_parse_incomplete" }, Outcome{"material_unavailable", "unknown", "none"}},
		{"missing model is not a retry", func(in *Input) { in.ErrorCode = "model_not_configured" }, Outcome{"dependency_unavailable", "unknown", "none"}},
		{"general failure is not assumed transient", func(in *Input) { in.State = "failed"; in.ErrorCode = "execution_failed" }, Outcome{"execution_failed", "unknown", "none"}},
		{"cancelled stays distinct", func(in *Input) { in.State = "cancelled"; in.ErrorCode = "model_timeout" }, Outcome{"cancelled", "unknown", "none"}},
		{"user cancellation is distinguished", func(in *Input) { in.State = "cancelled"; in.ErrorCode = "cancelled_by_user" }, Outcome{"cancelled_by_user", "unknown", "none"}},
		{"authorization cancellation is not a user restart", func(in *Input) { in.State = "cancelled"; in.ErrorCode = "authorization_revoked" }, Outcome{"authorization_revoked", "unknown", "none"}},
		{"cancelled preparation exposes reason only", func(in *Input) {
			in.HistoryKind = "preparing"
			in.State = "cancelled"
			in.ErrorCode = "cancelled_by_user"
			in.Events = []Event{{"attempt", 1, "agent_review", "agent_review_result", "agent_review_event", "completed", ""}}
		}, Outcome{"cancelled_by_user", "unknown", "none"}},
		{"revoked preparation is distinct", func(in *Input) {
			in.HistoryKind = "preparing"
			in.State = "cancelled"
			in.ErrorCode = "authorization_revoked"
		}, Outcome{"authorization_revoked", "unknown", "none"}},
		{"unknown preparation cancellation stays unknown", func(in *Input) { in.HistoryKind = "preparing"; in.State = "cancelled"; in.ErrorCode = "" }, Outcome{"cancelled", "unknown", "none"}},
		{"legacy is unknown", func(in *Input) { in.HistoryKind = "legacy_unfrozen" }, Outcome{"unknown", "unknown", "none"}},
		{"foreign run is unknown", func(in *Input) { in.AttemptRunID = "other" }, Outcome{"unknown", "unknown", "none"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := base
			tt.edit(&input)
			if got := Project(input); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
