// File metadata (manifest mirror):
// Purpose: Projects conservative business reasons and agent-stage observations from one selected frozen review attempt without changing stored state or authorizing retries.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package reviewstatus

// Event contains only the fields needed to summarize a selected attempt.
type Event struct {
	AttemptID string
	Seq       int
	NodeKind  string
	Phase     string
	Type      string
	Status    string
	ErrorCode string
}

type Input struct {
	RunID, AttemptID, AttemptRunID, HistoryKind, State, ErrorCode string
	Events                                                        []Event
}

// Outcome is descriptive. It neither changes the stored state nor authorizes a retry.
type Outcome struct {
	Reason     string `json:"reason"`
	AgentStage string `json:"agent_stage"`
	RetryCause string `json:"retry_cause"`
}

// Project uses recorded cancellation reasons from a selected preparation and
// classifies review results only for frozen attempts. Legacy data stay unknown.
func Project(in Input) Outcome {
	out := Outcome{Reason: "unknown", AgentStage: "unknown", RetryCause: "none"}
	if in.AttemptID == "" || in.RunID == "" || in.AttemptRunID != in.RunID || (in.HistoryKind != "frozen" && in.HistoryKind != "preparing") {
		return out
	}
	if in.State == "cancelled" {
		out.Reason = "cancelled"
		if in.ErrorCode == "cancelled_by_user" {
			out.Reason = "cancelled_by_user"
		} else if in.ErrorCode == "authorization_revoked" {
			out.Reason = "authorization_revoked"
		}
	}
	// A cancelled preparation has a reliable cancellation reason but no
	// frozen input or review result to classify.
	if in.HistoryKind != "frozen" {
		return out
	}
	if in.State == "partial" || in.State == "failed" || in.State == "interrupted" {
		switch in.ErrorCode {
		case "precheck_scope_limited":
			if in.State == "partial" {
				out.Reason = "scope_limited"
			}
		case "document_parse_incomplete":
			out.Reason = "material_unavailable"
		case "model_timeout":
			out.Reason, out.RetryCause = "transient_failure", "model_timeout"
		case "model_not_configured":
			out.Reason = "dependency_unavailable"
		case "model_output_unusable", "model_evidence_rejected", "model_incomplete", "agent_review_incomplete", "tool_evidence_incomplete":
			out.Reason = "review_incomplete"
		case "model_request_failed", "agent_review_failed", "execution_failed", "execution_persistence_failed":
			out.Reason = "execution_failed"
		}
	}
	lastSeq := -1
	for _, event := range in.Events {
		if event.AttemptID != in.AttemptID || event.NodeKind != "agent_review" || event.Phase != "agent_review_result" || event.Type != "agent_review_event" || event.Seq <= lastSeq {
			continue
		}
		lastSeq = event.Seq
		out.AgentStage = "unknown"
		switch {
		case event.Status == "completed" && event.ErrorCode == "":
			out.AgentStage = "completed"
		case event.Status == "skipped" && event.ErrorCode == "agent_review_unavailable":
			out.AgentStage = "skipped"
		case event.Status == "partial" && event.ErrorCode == "agent_review_failed":
			out.AgentStage = "failed"
		case event.Status == "partial" && event.ErrorCode == "agent_review_incomplete":
			out.AgentStage = "partial"
		}
	}
	return out
}
