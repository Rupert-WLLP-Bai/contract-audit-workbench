// File metadata (manifest mirror):
// Purpose: Adapts trusted attempt preparation and original scoped result/terminal transactions to Execution app plans, applying citation deltas locally under existing locks and proof.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"

	"contractaudit/internal/engine"
	executionapp "contractaudit/internal/execution/app"
)

type standardExecutionCodec struct{}

func (standardExecutionCodec) NewID(prefix string) (string, error) { return newID(prefix) }
func (standardExecutionCodec) Stamp() string                       { return stamp() }

type standardExecutionBridge struct {
	store    *Store
	parent   context.Context
	models   Models
	recorder attemptRecorder
}

func (b standardExecutionBridge) PrepareFrozenInput(ctx context.Context, attempt executionapp.FrozenAttempt) (executionapp.PreparedInput, error) {
	prepared, err := b.store.prepareAttemptResult(ctx, attempt.RunID, attempt.AttemptID, b.models)
	if err != nil {
		return executionapp.PreparedInput{}, err
	}
	out := make([]executionapp.Document, len(prepared.documents))
	for i, d := range prepared.documents {
		out[i] = executionapp.Document{ID: d.Document.ID, Role: d.Document.Role, OriginalDigest: d.Document.Digest, Text: d.Text, ParseError: d.ParseError, Ready: d.Ready, FromOCR: d.FromOCR, IR: d.IR}
		if d.ParsedDocument != nil {
			out[i].ParsedDocumentID = d.ParsedDocument.ID
			out[i].NormalizedDigest = d.ParsedDocument.NormalizedDigest
		}
	}
	return executionapp.PreparedInput{InputHash: prepared.inputHash, Documents: out}, nil
}
func (b standardExecutionBridge) RuleAttempt(ctx context.Context, attempt executionapp.FrozenAttempt) (executionapp.FrozenAttempt, error) {
	st, err := b.store.snapshotForAttemptMetadataContext(ctx, attempt.AttemptID)
	if err != nil {
		return attempt, err
	}
	attempt.CaseID = runIDCase(st, attempt.RunID)
	return attempt, nil
}
func fromAppAnchor(a executionapp.Anchor) Anchor {
	return Anchor{ID: a.ID, DocumentID: a.DocumentID, ParsedDocumentID: a.ParsedDocumentID, NormalizedDigest: a.NormalizedDigest, Quote: a.Quote, Status: a.Status, Start: a.Start, End: a.End, Reason: a.Reason, Page: a.Page, ElementID: a.ElementID}
}
func toAppAnchor(a Anchor) executionapp.Anchor {
	return executionapp.Anchor{ID: a.ID, DocumentID: a.DocumentID, ParsedDocumentID: a.ParsedDocumentID, NormalizedDigest: a.NormalizedDigest, Quote: a.Quote, Status: a.Status, Start: a.Start, End: a.End, Reason: a.Reason, Page: a.Page, ElementID: a.ElementID}
}
func fromAppFinding(f executionapp.Finding) Finding {
	anchors := make([]Anchor, 0, len(f.Anchors))
	for _, a := range f.Anchors {
		anchors = append(anchors, fromAppAnchor(a))
	}
	return Finding{ID: f.ID, RunID: f.RunID, AttemptID: f.AttemptID, CaseID: f.CaseID, Origin: f.Origin, Domain: f.Domain, Claim: f.Claim, Relation: f.Relation, DispositionSuggestion: f.DispositionSuggestion, UnverifiedReason: f.UnverifiedReason, Version: f.Version, CreatedAt: f.CreatedAt, Anchors: anchors}
}
func toAppFinding(f Finding) executionapp.Finding {
	anchors := make([]executionapp.Anchor, 0, len(f.Anchors))
	for _, a := range f.Anchors {
		anchors = append(anchors, toAppAnchor(a))
	}
	return executionapp.Finding{ID: f.ID, RunID: f.RunID, AttemptID: f.AttemptID, CaseID: f.CaseID, Origin: f.Origin, Domain: f.Domain, Claim: f.Claim, Relation: f.Relation, DispositionSuggestion: f.DispositionSuggestion, UnverifiedReason: f.UnverifiedReason, Version: f.Version, CreatedAt: f.CreatedAt, Anchors: anchors}
}
func (b standardExecutionBridge) AppendRuleResults(ctx context.Context, attempt executionapp.FrozenAttempt, results executionapp.RuleResults) error {
	return b.store.updateExecutionContext(ctx, attempt.AttemptID, func(st *state) error {
		for _, f := range results.Findings {
			st.Findings = append(st.Findings, fromAppFinding(f))
		}
		for _, e := range results.Evaluations {
			st.Evaluations = append(st.Evaluations, ConstraintEval{ID: e.ID, RunID: e.RunID, AttemptID: e.AttemptID, CaseID: e.CaseID, Domain: e.Domain, Relation: e.Relation, RuleVersion: e.RuleVersion, Reason: e.Reason, FindingID: e.FindingID})
		}
		return nil
	})
}
func (b standardExecutionBridge) MergeCitationDrafts(ctx context.Context, attempt executionapp.FrozenAttempt, docs []executionapp.Document, drafts []engine.Draft) (executionapp.CitationDelta, error) {
	var delta executionapp.CitationDelta
	err := b.store.updateExecutionContext(ctx, attempt.AttemptID, func(st *state) error {
		attempt.CaseID = runIDCase(*st, attempt.RunID)
		candidates := make([]executionapp.Finding, len(st.Findings))
		for i, f := range st.Findings {
			candidates[i] = toAppFinding(f)
		}
		var err error
		delta, err = executionapp.PlanCitationMerge(attempt, docs, candidates, drafts, standardExecutionCodec{}, standardExecutionCodec{})
		if err != nil {
			return err
		}
		for _, change := range delta.Changes {
			if change.Insert != nil {
				st.Findings = append(st.Findings, fromAppFinding(*change.Insert))
				continue
			}
			if change.Update != nil {
				for i := range st.Findings {
					if st.Findings[i].ID == change.Update.FindingID {
						f := &st.Findings[i]
						f.DispositionSuggestion = change.Update.DispositionSuggestion
						f.UnverifiedReason = change.Update.UnverifiedReason
						for _, a := range change.Update.AppendAnchors {
							f.Anchors = append(f.Anchors, fromAppAnchor(a))
						}
						break
					}
				}
			}
		}
		return nil
	})
	return delta, err
}
func (b standardExecutionBridge) RecordFirstVerifiedFinding(ctx context.Context, attempt executionapp.FrozenAttempt, rules bool) error {
	recorder := b.recorder
	if rules {
		recorder = attemptRecorder{store: b.store, attemptID: attempt.AttemptID}
	}
	return b.store.recordFirstVerifiedFinding(ctx, recorder, attempt.AttemptID)
}
func (b standardExecutionBridge) RecorderError() error { return b.recorder.err() }
func (b standardExecutionBridge) ReadAttemptProgress(ctx context.Context, attempt executionapp.FrozenAttempt) (executionapp.Progress, error) {
	st, err := b.store.snapshotForAttemptMetadataContext(ctx, attempt.AttemptID)
	if err != nil {
		return executionapp.Progress{}, err
	}
	current, ok := st.attemptByID(attempt.AttemptID)
	return executionapp.Progress{RulesPublished: len(st.findingsForAttempt(attempt.RunID, attempt.AttemptID)) > 0 || len(st.evaluationsForAttempt(attempt.RunID, attempt.AttemptID)) > 0, Active: ok && attemptActive(current.State)}, nil
}
func (b standardExecutionBridge) FinishAttempt(ctx context.Context, attempt executionapp.FrozenAttempt, outcome executionapp.Outcome) error {
	return b.store.finishAttemptAfterExecution(b.parent, ctx, attempt.RunID, attempt.AttemptID, outcome.State, outcome.PartialReason, outcome.ErrorCode)
}
