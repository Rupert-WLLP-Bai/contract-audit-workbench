// File metadata (manifest mirror):
// Purpose: Defines trusted frozen review inputs, narrow ports and standard rule/material/citation plans using existing engine and model contracts.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package app contains the standard review's typed decisions and plans.
package app

import (
	"context"
	"errors"

	"contractaudit/internal/docir"
	"contractaudit/internal/engine"
	"contractaudit/internal/execution"
	"contractaudit/internal/model"
)

// FrozenAttempt is constructed by the trusted execution boundary from server-owned
// attempt and snapshot data, never from a client scope. Store bridges remain
// responsible for current authorization and execution proof.
type FrozenAttempt struct{ TenantID, OrgID, CaseID, RunID, AttemptID, RuleSetHash, PlaybookHash string }
type Document struct {
	ID, Role, OriginalDigest, Text, ParseError, ParsedDocumentID, NormalizedDigest string
	Ready, FromOCR                                                                 bool
	IR                                                                             docir.IR
}
type PreparedInput struct {
	InputHash string
	Documents []Document
}

// IDs and Clock preserve the existing allocation and timestamp boundaries.
type IDs interface {
	NewID(prefix string) (string, error)
}
type Clock interface{ Stamp() string }
type WarrantyModel interface {
	Warranty(context.Context, []model.Material) (model.Warranty, error)
}
type InputPort interface {
	PrepareFrozenInput(context.Context, FrozenAttempt) (PreparedInput, error)
}
type RuleResultsPort interface {
	AppendRuleResults(context.Context, FrozenAttempt, RuleResults) error
}
type CitationPort interface {
	MergeCitationDrafts(context.Context, FrozenAttempt, []Document, []engine.Draft) (CitationDelta, error)
}
type Progress struct{ RulesPublished, Active bool }
type ProgressPort interface {
	ReadAttemptProgress(context.Context, FrozenAttempt) (Progress, error)
}
type OutcomePort interface {
	FinishAttempt(context.Context, FrozenAttempt, Outcome) error
}
type EventPort = execution.Recorder

type Anchor struct {
	ID, DocumentID, ParsedDocumentID, NormalizedDigest, Quote, Status string
	Start, End                                                        int
	Reason                                                            string
	Page                                                              *int
	ElementID                                                         string
}
type Finding struct {
	ID, RunID, AttemptID, CaseID, Origin, Domain, Claim, Relation string
	DispositionSuggestion, UnverifiedReason                       string
	Version                                                       int
	CreatedAt                                                     string
	Anchors                                                       []Anchor
}
type Evaluation struct{ ID, RunID, AttemptID, CaseID, Domain, Relation, RuleVersion, Reason, FindingID string }
type RuleResults struct {
	Findings    []Finding
	Evaluations []Evaluation
}

func Sources(docs []Document) []engine.Source {
	out := make([]engine.Source, len(docs))
	for i, d := range docs {
		status := "failed"
		if d.Ready {
			status = "ready"
		}
		out[i] = engine.Source{ID: d.ID, Role: d.Role, ParseStatus: status, Text: d.Text, FromOCR: d.FromOCR, IR: d.IR}
	}
	return out
}
func ReadyCount(docs []Document) int {
	n := 0
	for _, d := range docs {
		if d.Ready {
			n++
		}
	}
	return n
}
func Materials(docs []Document) []model.Material {
	out := make([]model.Material, len(docs))
	for i, d := range docs {
		out[i] = model.Material{ID: d.ID, Role: d.Role, Text: d.Text, Ready: d.Ready, ParseError: d.ParseError}
	}
	return out
}
func AcceptWarranty(docs []Document, proposal model.Warranty) ([]engine.Draft, error) {
	quotes := make([]engine.QuoteRef, len(proposal.Quotes))
	for i, q := range proposal.Quotes {
		quotes[i] = engine.QuoteRef{DocumentID: q.DocumentID, Quote: q.Quote}
	}
	_, drafts, err := engine.AcceptWarranty(Sources(docs), engine.Proposal{Claim: proposal.Claim, Relation: proposal.Relation, Disposition: proposal.Disposition, Quotes: quotes})
	return drafts, err
}
func parsedIdentity(docs []Document, id string) (string, string) {
	for _, d := range docs {
		if d.ID == id && d.ParsedDocumentID != "" {
			return d.ParsedDocumentID, d.NormalizedDigest
		}
	}
	return "", ""
}
func anchorFrom(a engine.Anchor, docs []Document, ids IDs) (Anchor, error) {
	id, err := ids.NewID("ev_")
	if err != nil {
		return Anchor{}, err
	}
	parsed, digest := parsedIdentity(docs, a.DocumentID)
	return Anchor{ID: id, DocumentID: a.DocumentID, ParsedDocumentID: parsed, NormalizedDigest: digest, Quote: a.Quote, Status: a.Status, Start: a.Start, End: a.End, Reason: a.Reason, Page: a.Page, ElementID: a.ElementID}, nil
}
func materialize(attempt FrozenAttempt, now string, drafts []engine.Draft, docs []Document, ids IDs) ([]Finding, error) {
	out := make([]Finding, 0, len(drafts))
	for _, d := range drafts {
		id, err := ids.NewID("find_")
		if err != nil {
			return nil, err
		}
		origin := d.Origin
		if origin == "" {
			origin = "rule"
		}
		f := Finding{ID: id, RunID: attempt.RunID, AttemptID: attempt.AttemptID, CaseID: attempt.CaseID, Origin: origin, Domain: d.Domain, Claim: d.Claim, Relation: d.Relation, DispositionSuggestion: d.Disposition, UnverifiedReason: d.UnverifiedReason, Version: 1, CreatedAt: now, Anchors: []Anchor{}}
		for _, a := range d.Anchors {
			anchor, err := anchorFrom(a, docs, ids)
			if err != nil {
				return nil, err
			}
			f.Anchors = append(f.Anchors, anchor)
		}
		out = append(out, f)
	}
	return out, nil
}
func PlanRuleResults(attempt FrozenAttempt, docs []Document, ids IDs, clock Clock) (RuleResults, error) {
	if len(docs) == 0 {
		return RuleResults{}, errors.New("frozen input is unavailable")
	}
	sources := Sources(docs)
	first := engine.Evaluate(sources)
	extra := engine.CrossFile(sources)
	drafts := engine.Findings(first)
	for _, ev := range extra {
		drafts = append(drafts, engine.Findings(ev)...)
	}
	findings, err := materialize(attempt, clock.Stamp(), drafts, docs, ids)
	if err != nil {
		return RuleResults{}, err
	}
	byDomain := make(map[string]string, len(findings))
	for _, f := range findings {
		byDomain[f.Domain] = f.ID
	}
	var evaluations []Evaluation
	for _, ev := range append([]engine.Evaluation{first}, extra...) {
		id, err := ids.NewID("evl_")
		if err != nil {
			return RuleResults{}, err
		}
		evaluations = append(evaluations, Evaluation{ID: id, RunID: attempt.RunID, AttemptID: attempt.AttemptID, CaseID: attempt.CaseID, Domain: ev.Domain, Relation: ev.Relation, RuleVersion: ev.RuleVersion, Reason: ev.Reason, FindingID: byDomain[ev.Domain]})
	}
	return RuleResults{Findings: findings, Evaluations: evaluations}, nil
}
func HasVerifiedFinding(findings []Finding) bool {
	for _, f := range findings {
		for _, a := range f.Anchors {
			if a.Status == "located" {
				return true
			}
		}
	}
	return false
}
