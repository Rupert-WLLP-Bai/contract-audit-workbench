// File metadata (manifest mirror):
// Purpose: Guard every requested read before any call in the plan executes. The model may name only references actually delivered in this round and bounded body spans. Related `agentflow` operations include validateKnowledgeReads, knowledgeReadConfigurationValid, validKnowledgeReadRequest, executeKnowledgeReads.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-28
// Updated: 2026-11-12
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package agentflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"contractaudit/internal/execution/steps"
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
	started := r.config.Clock()
	var identity steps.Identity
	if r.config.StepJournal != nil {
		var err error
		identity, err = r.knowledgeReadStepIdentity(state, read, callID)
		if err != nil {
			return err
		}
		record, execute, err := r.config.StepJournal.Begin(ctx, identity)
		if err != nil {
			return err
		}
		if err := record.Validate(); err != nil {
			return errors.Join(steps.ErrCorrupt, err)
		}
		switch record.Status {
		case steps.StatusSucceeded:
			if execute || record.ObservedAt == nil {
				return steps.ErrCorrupt
			}
			var persisted KnowledgeReadObservation
			if err := json.Unmarshal(record.Payload, &persisted); err != nil || !sameKnowledgeRead(persisted.KnowledgeReadIdentity, read, persisted.Text) {
				return errors.Join(steps.ErrCorrupt, err)
			}
			// Re-read only to verify the authorized frozen span still resolves to
			// the exact bytes already observed. The checkpoint text and original
			// timestamp remain the planner observation.
			current, err := readKnowledgeSpan(ctx, r, read)
			if err != nil {
				return fmt.Errorf("%w: reauthorize persisted frozen knowledge span: %w", ErrKnowledgeRead, err)
			}
			if current != persisted.Text {
				return errors.Join(steps.ErrIdentityMismatch, ErrKnowledgeRead)
			}
			state.knowledgeReadAttempts[read] = true
			state.knowledgeReadCalls++
			state.report.ToolCalls += record.CallsConsumed
			return r.acceptKnowledgeRead(state, read, callID, persisted.Text, record.ElapsedMS, *record.ObservedAt, true)
		case steps.StatusFailed:
			if execute || record.ObservedAt == nil {
				return steps.ErrCorrupt
			}
			state.knowledgeReadAttempts[read] = true
			state.knowledgeReadCalls++
			state.report.ToolCalls += record.CallsConsumed
			state.request.KnowledgeSelection.Reads = append(state.request.KnowledgeSelection.Reads, KnowledgeReadRecord{
				KnowledgeID: read.KnowledgeID, VersionID: read.VersionID, ContentSHA256: read.ContentSHA256,
				Round: state.round, Start: read.Start, End: read.End, Status: "failed"})
			if err := r.eventKnowledgeReadFailure(state, read, callID, record.FailureCode, 0, *record.ObservedAt); err != nil {
				return err
			}
			return errors.Join(steps.ErrPriorFailure, fmt.Errorf("%w: prior frozen knowledge read failed (%s)", ErrKnowledgeRead, record.FailureCode))
		case steps.StatusAmbiguous:
			if execute {
				return steps.ErrCorrupt
			}
			state.knowledgeReadAttempts[read] = true
			state.knowledgeReadCalls++
			state.report.ToolCalls += record.CallsConsumed
			state.termination = "step_result_ambiguous"
			if err := state.eventCall("step_ambiguous", "knowledge.read", "", record.FailureCode, 0, callID, ""); err != nil {
				return err
			}
			return steps.ErrAmbiguous
		case steps.StatusStarted:
			if !execute {
				return steps.ErrAmbiguous
			}
		default:
			return steps.ErrCorrupt
		}
	}
	if err := state.eventCall("knowledge_read_started", "knowledge.read", "", "frozen_knowledge_read_started", 0, callID, ""); err != nil {
		return err
	}
	state.knowledgeReadAttempts[read] = true
	state.knowledgeReadCalls++
	state.report.ToolCalls++
	text, err := readKnowledgeSpan(ctx, r, read)
	if err == nil {
		err = validateKnowledgeSpan(text, read, state)
	}
	if err != nil {
		observedAt := r.config.Clock().UTC()
		if r.config.StepJournal != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) &&
			!errors.Is(err, ErrDenied) && !errors.Is(err, ErrPlanningAuthorization) && ctx.Err() == nil {
			outcome := steps.Outcome{Status: steps.StatusFailed, FailureCode: "knowledge_read_failed", ObservedAt: observedAt,
				ElapsedMS: elapsedMS(started, observedAt), CallsConsumed: 1, Usage: steps.Usage{Status: steps.UsageNotApplicable}}
			if persistErr := r.finishStep(ctx, identity, outcome); persistErr != nil {
				return persistErr
			}
		}
		return r.recordFailedKnowledgeRead(state, read, callID, started, observedAt, err)
	}
	observedAt := r.config.Clock().UTC()
	if r.config.StepJournal != nil {
		observation := KnowledgeReadObservation{KnowledgeReadIdentity: KnowledgeReadIdentity{
			KnowledgeID: read.KnowledgeID, VersionID: read.VersionID, ContentSHA256: read.ContentSHA256,
			Start: read.Start, End: read.End, TextSHA256: TextHash(text)}, Text: text}
		payload, err := json.Marshal(observation)
		if err != nil {
			return err
		}
		outcome := steps.Outcome{Status: steps.StatusSucceeded, Payload: payload, ObservedAt: observedAt,
			ElapsedMS: elapsedMS(started, observedAt), CallsConsumed: 1, Usage: steps.Usage{Status: steps.UsageNotApplicable}}
		if err := r.finishStep(ctx, identity, outcome); err != nil {
			return err
		}
	}
	return r.acceptKnowledgeRead(state, read, callID, text, elapsedMS(started, observedAt), observedAt, false)
}

func sameKnowledgeRead(identity KnowledgeReadIdentity, read KnowledgeReadRequest, text string) bool {
	return identity.KnowledgeID == read.KnowledgeID && identity.VersionID == read.VersionID &&
		identity.ContentSHA256 == read.ContentSHA256 && identity.Start == read.Start && identity.End == read.End &&
		identity.TextSHA256 == TextHash(text)
}

func (r *Runtime) acceptKnowledgeRead(state *execution, read KnowledgeReadRequest, callID, text string, elapsed int64, observedAt time.Time, replayed bool) error {
	if err := validateKnowledgeSpan(text, read, state); err != nil {
		return errors.Join(steps.ErrCorrupt, err)
	}
	state.evidenceBytesLeft -= len(text)
	digest := TextHash(text)
	record := KnowledgeReadRecord{KnowledgeID: read.KnowledgeID, VersionID: read.VersionID, ContentSHA256: read.ContentSHA256,
		Round: state.round, Start: read.Start, End: read.End, Status: "completed", Bytes: len(text), TextSHA256: digest}
	state.request.KnowledgeSelection.Reads = append(state.request.KnowledgeSelection.Reads, record)
	state.knowledgeObservations = append(state.knowledgeObservations, KnowledgeReadObservation{KnowledgeReadIdentity: KnowledgeReadIdentity{
		KnowledgeID: read.KnowledgeID, VersionID: read.VersionID, ContentSHA256: read.ContentSHA256,
		Start: read.Start, End: read.End, TextSHA256: digest}, Text: text})
	state.roundProgress = true
	kind, code, duration := "knowledge_read_completed", "frozen_knowledge_span_read", elapsed
	if replayed {
		kind, code, duration = "knowledge_read_reused", "persisted_frozen_knowledge_span_reused", 0
	}
	return state.eventAt(kind, "knowledge.read", "", code, duration, callID, "", observedAt)
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

func (r *Runtime) recordFailedKnowledgeRead(state *execution, read KnowledgeReadRequest, callID string, started, observedAt time.Time, err error) error {
	state.request.KnowledgeSelection.Reads = append(state.request.KnowledgeSelection.Reads, KnowledgeReadRecord{KnowledgeID: read.KnowledgeID,
		VersionID: read.VersionID, ContentSHA256: read.ContentSHA256, Round: state.round, Start: read.Start, End: read.End, Status: "failed"})
	if sinkErr := r.eventKnowledgeReadFailure(state, read, callID, "frozen_knowledge_read_failed", elapsedMS(started, observedAt), observedAt); sinkErr != nil {
		return sinkErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrKnowledgeRead, err)
}

func (r *Runtime) eventKnowledgeReadFailure(state *execution, read KnowledgeReadRequest, callID, code string, duration int64, observedAt time.Time) error {
	return state.eventAt("knowledge_read_failed", "knowledge.read", "", code, duration, callID, "", observedAt)
}
