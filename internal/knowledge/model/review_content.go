// File metadata (manifest mirror):
// Purpose: Defines the whitelisted governed Knowledge content value returned to an authorized review.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package model

// GovernedKnowledgeReviewContent is the reviewed content allowed into an
// authorized attempt. Source-case and feedback identifiers are intentionally
// absent; source-sensitive content requires the separate source-case grant.
type GovernedKnowledgeReviewContent struct {
	Ref                   KnowledgeRef `json:"ref"`
	Kind                  string       `json:"kind"`
	Title                 string       `json:"title"`
	Body                  string       `json:"body"`
	Summary               string       `json:"summary"`
	ValidFrom             string       `json:"valid_from"`
	ValidUntil            string       `json:"valid_until"`
	ExceptionBasis        string       `json:"exception_basis,omitempty"`
	ReviewedApplicability []string     `json:"reviewed_applicability,omitempty"`
	ReviewedExclusions    []string     `json:"reviewed_exclusions,omitempty"`
	CheckAction           string       `json:"check_action,omitempty"`
	MinimumEvidence       string       `json:"minimum_evidence,omitempty"`
	Counterexamples       []string     `json:"counterexamples,omitempty"`
}
