package agentflow

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

// Guard every requested read before any call in the plan executes. The model may
// name only references actually delivered in this round and bounded body spans.
func (r *Runtime) validateKnowledgeReads(state *execution) error {
	reads := state.report.Plan.KnowledgeReads
	if len(reads) == 0 {
		return nil
	}
	if r.config.ReadKnowledge == nil || state.request.KnowledgeSelection == nil || state.request.KnowledgeSelection.SnapshotSHA256 == "" ||
		state.knowledgeReadCalls+len(reads) > KnowledgeReadMaxCalls {
		return ErrPlan
	}
	seen := make(map[KnowledgeReadRequest]bool, len(reads))
	for _, read := range reads {
		identity := KnowledgeIdentity{KnowledgeID: read.KnowledgeID, VersionID: read.VersionID, ContentSHA256: read.ContentSHA256}
		if !state.plannerKnowledge[identity] || read.Start < 0 || read.End <= read.Start || read.End-read.Start > KnowledgeReadMaxRunes ||
			seen[read] || state.knowledgeReadAttempts[read] {
			return ErrPlan
		}
		seen[read] = true
	}
	return nil
}

// A started event is committed before the read. The store uses these events to
// conservatively reserve the same read budget after a worker generation changes.
func (r *Runtime) executeKnowledgeReads(ctx context.Context, state *execution) error {
	for _, read := range state.report.Plan.KnowledgeReads {
		if err := ctx.Err(); err != nil {
			return err
		}
		callID := "knowledge_" + TextHash(fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%s\x00%d\x00%d", state.request.RequestID,
			state.round, read.KnowledgeID, read.VersionID, read.ContentSHA256, read.Start, read.End))[:24]
		if err := state.eventCall("knowledge_read_started", "knowledge.read", "", "frozen_knowledge_read_started", 0, callID, ""); err != nil {
			return err
		}
		state.knowledgeReadAttempts[read] = true
		state.knowledgeReadCalls++
		state.report.ToolCalls++
		started := time.Now()
		readCtx, cancel := context.WithTimeout(ctx, r.config.ToolTimeout)
		text, err := r.config.ReadKnowledge(readCtx, read)
		if readCtx.Err() != nil {
			err = readCtx.Err()
		}
		cancel()
		if err == nil && (!utf8.ValidString(text) || utf8.RuneCountInString(text) != read.End-read.Start || len(text) > KnowledgeReadMaxBytes) {
			err = ErrEvidence
		}
		if err == nil && len(text) > state.evidenceBytesLeft {
			err = ErrEvidence
			state.termination = "evidence_budget_exhausted"
		}
		record := KnowledgeReadRecord{KnowledgeID: read.KnowledgeID, VersionID: read.VersionID, ContentSHA256: read.ContentSHA256,
			Round: state.round, Start: read.Start, End: read.End, Status: "failed"}
		if err != nil {
			state.request.KnowledgeSelection.Reads = append(state.request.KnowledgeSelection.Reads, record)
			if sinkErr := state.eventCall("knowledge_read_failed", "knowledge.read", "", "frozen_knowledge_read_failed", time.Since(started).Milliseconds(), callID, ""); sinkErr != nil {
				return sinkErr
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return fmt.Errorf("%w: %w", ErrKnowledgeRead, err)
		}
		state.evidenceBytesLeft -= len(text)
		digest := TextHash(text)
		record.Status, record.Bytes, record.TextSHA256 = "completed", len(text), digest
		state.request.KnowledgeSelection.Reads = append(state.request.KnowledgeSelection.Reads, record)
		state.knowledgeObservations = append(state.knowledgeObservations, KnowledgeReadObservation{KnowledgeReadIdentity: KnowledgeReadIdentity{
			KnowledgeID: read.KnowledgeID, VersionID: read.VersionID, ContentSHA256: read.ContentSHA256, Start: read.Start, End: read.End, TextSHA256: digest}, Text: text})
		state.roundProgress = true
		if err := state.eventCall("knowledge_read_completed", "knowledge.read", "", "frozen_knowledge_span_read", time.Since(started).Milliseconds(), callID, ""); err != nil {
			return err
		}
	}
	return nil
}
