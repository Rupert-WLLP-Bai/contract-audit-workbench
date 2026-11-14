// File metadata (manifest mirror):
// Purpose: Checks that persisted Agent tool events preserve their call span, tool identity and execution status.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"testing"

	"contractaudit/internal/agentflow"
)

func TestAgentReviewToolEventCarriesMatchingCallIdentity(t *testing.T) {
	for _, tc := range []struct{ kind, status string }{{"tool_started", "running"}, {"tool_completed", "completed"}, {"tool_failed", "failed"}} {
		event := agentReviewExecutionEvent(context.Background(), agentflow.Event{Kind: tc.kind, CallID: "call-123", ToolID: "technical.compare"})
		if event.NodeKind != "tool_call" || event.SpanID != "call-123" || event.ModelRef != "technical.compare" || event.Status != tc.status {
			t.Fatalf("%s: %+v", tc.kind, event)
		}
	}
}

func TestAgentReviewMeasuredZeroDurationIsRecorded(t *testing.T) {
	for _, kind := range []string{"tool_completed", "tool_failed", "tool_observed", "knowledge_read_completed", "knowledge_read_failed"} {
		event := agentReviewExecutionEvent(context.Background(), agentflow.Event{Kind: kind})
		if event.DurationMS == nil || *event.DurationMS != 0 {
			t.Errorf("%s: zero duration lost", kind)
		}
	}
	for _, kind := range []string{"tool_started", "knowledge_read_started", "plan"} {
		event := agentReviewExecutionEvent(context.Background(), agentflow.Event{Kind: kind})
		if event.DurationMS != nil {
			t.Errorf("%s: unmeasured duration recorded", kind)
		}
	}
}
