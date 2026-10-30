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
	if !knowledgeReadConfigurationValid(r, state, len(reads)) {
		return ErrPlan
	}
	seen := make(map[KnowledgeReadRequest]bool, len(reads))
	for _, read := range reads {
		if !validKnowledgeReadRequest(read, state, seen) {
			return ErrPlan
		}
		seen[read] = true
	}
	return nil
}

func knowledgeReadConfigurationValid(runtime *Runtime, state *execution, count int) bool {
	return runtime.config.ReadKnowledge != nil && state.request.KnowledgeSelection != nil &&
		state.request.KnowledgeSelection.SnapshotSHA256 != "" && state.knowledgeReadCalls+count <= KnowledgeReadMaxCalls
}

func validKnowledgeReadRequest(read KnowledgeReadRequest, state *execution, seen map[KnowledgeReadRequest]bool) bool {
	identity := KnowledgeIdentity{KnowledgeID: read.KnowledgeID, VersionID: read.VersionID, ContentSHA256: read.ContentSHA256}
	return state.plannerKnowledge[identity] && read.Start >= 0 && read.End > read.Start && read.End-read.Start <= KnowledgeReadMaxRunes &&
		!seen[read] && !state.knowledgeReadAttempts[read]
}

// A started event is committed before the read. The store uses these events to
// conservatively reserve the same read budget after a worker generation changes.
func (r *Runtime) executeKnowledgeReads(ctx context.Context, state *execution) error {
	for _, read := range state.report.Plan.KnowledgeReads {
		if err := r.executeOneKnowledgeRead(ctx, state, read); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) executeOneKnowledgeRead(ctx context.Context, state *execution, read KnowledgeReadRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	callID := knowledgeReadCallID(state, read)
	if err := state.eventCall("knowledge_read_started", "knowledge.read", "", "frozen_knowledge_read_started", 0, callID, ""); err != nil {
		return err
	}
	state.knowledgeReadAttempts[read] = true
	state.knowledgeReadCalls++
	state.report.ToolCalls++
	started := time.Now()
	text, err := readKnowledgeSpan(ctx, r, read)
	if err == nil {
		err = validateKnowledgeSpan(text, read, state)
	}
	if err != nil {
		return recordFailedKnowledgeRead(state, read, callID, started, err)
	}
	return recordSuccessfulKnowledgeRead(state, read, callID, started, text)
}

func knowledgeReadCallID(state *execution, read KnowledgeReadRequest) string {
	return "knowledge_" + TextHash(fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%s\x00%d\x00%d", state.request.RequestID,
		state.round, read.KnowledgeID, read.VersionID, read.ContentSHA256, read.Start, read.End))[:24]
}

func readKnowledgeSpan(ctx context.Context, runtime *Runtime, read KnowledgeReadRequest) (string, error) {
	readCtx, cancel := context.WithTimeout(ctx, runtime.config.ToolTimeout)
	defer cancel()
	text, err := runtime.config.ReadKnowledge(readCtx, read)
	if readCtx.Err() != nil {
		return text, readCtx.Err()
	}
	return text, err
}

func validateKnowledgeSpan(text string, read KnowledgeReadRequest, state *execution) error {
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) != read.End-read.Start || len(text) > KnowledgeReadMaxBytes {
		return ErrEvidence
	}
	if len(text) > state.evidenceBytesLeft {
		state.termination = "evidence_budget_exhausted"
		return ErrEvidence
	}
	return nil
}

func recordFailedKnowledgeRead(state *execution, read KnowledgeReadRequest, callID string, started time.Time, err error) error {
	state.request.KnowledgeSelection.Reads = append(state.request.KnowledgeSelection.Reads, KnowledgeReadRecord{KnowledgeID: read.KnowledgeID,
		VersionID: read.VersionID, ContentSHA256: read.ContentSHA256, Round: state.round, Start: read.Start, End: read.End, Status: "failed"})
	if sinkErr := state.eventCall("knowledge_read_failed", "knowledge.read", "", "frozen_knowledge_read_failed", time.Since(started).Milliseconds(), callID, ""); sinkErr != nil {
		return sinkErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrKnowledgeRead, err)
}

func recordSuccessfulKnowledgeRead(state *execution, read KnowledgeReadRequest, callID string, started time.Time, text string) error {
	state.evidenceBytesLeft -= len(text)
	digest := TextHash(text)
	record := KnowledgeReadRecord{KnowledgeID: read.KnowledgeID, VersionID: read.VersionID, ContentSHA256: read.ContentSHA256,
		Round: state.round, Start: read.Start, End: read.End, Status: "completed", Bytes: len(text), TextSHA256: digest}
	state.request.KnowledgeSelection.Reads = append(state.request.KnowledgeSelection.Reads, record)
	state.knowledgeObservations = append(state.knowledgeObservations, KnowledgeReadObservation{KnowledgeReadIdentity: KnowledgeReadIdentity{
		KnowledgeID: read.KnowledgeID, VersionID: read.VersionID, ContentSHA256: read.ContentSHA256, Start: read.Start, End: read.End, TextSHA256: digest}, Text: text})
	state.roundProgress = true
	return state.eventCall("knowledge_read_completed", "knowledge.read", "", "frozen_knowledge_span_read", time.Since(started).Milliseconds(), callID, "")
}
