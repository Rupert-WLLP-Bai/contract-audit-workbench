// File metadata (manifest mirror):
// Purpose: Owns immutable execution run, attempt, snapshot, document provenance and output value types with statically bound owner slots and compatible wire fields.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package model owns immutable execution values and their canonical identities.
package model

import (
	"encoding/json"

	"contractaudit/internal/docir"
)

// OriginalDocumentProof is the exact immutable original-file identity needed by
// frozen execution rules. It is not an authorization decision or a persisted
// replacement for the owning Document value.
type OriginalDocumentProof struct {
	ID        string
	Digest    string
	TenantID  string
	OrgID     string
	CaseID    string
	Role      string
	CreatedAt string
}

// OriginalDocument exposes only fields read by frozen execution rules. The
// concrete value remains owned and serialized by its source domain.
type OriginalDocument interface {
	OriginalProof() OriginalDocumentProof
}

// Run is one review of the documents then in the case.
type Run struct {
	ID             string `json:"id"`
	CaseID         string `json:"case_id"`
	State          string `json:"state"`
	ModelRef       string `json:"model_ref,omitempty"`
	PartialReason  string `json:"partial_reason,omitempty"`
	FailureMessage string `json:"failure_message,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// Attempt is one immutable execution identity within a run.
type Attempt struct {
	TenantLiveEpoch        *int64               `json:"-"`
	ID                     string               `json:"id"`
	RunID                  string               `json:"run_id"`
	ActorID                string               `json:"actor_id,omitempty"`
	Ordinal                int                  `json:"ordinal"`
	RetryOfAttemptID       *string              `json:"retry_of_attempt_id"`
	State                  string               `json:"state"`
	AcceptedAt             string               `json:"accepted_at"`
	StartedAt              *string              `json:"started_at"`
	FinishedAt             *string              `json:"finished_at"`
	InputHash              *string              `json:"input_hash"`
	OutputHash             *string              `json:"output_hash"`
	ErrorCode              *string              `json:"error_code"`
	HistoryKind            string               `json:"history_kind"`
	ReviewScope            *ReviewScopeIdentity `json:"review_scope,omitempty"`
	ParsedDocumentIDs      []string             `json:"parsed_document_ids"`
	RulePackSnapshot       json.RawMessage      `json:"-"`
	RulePackHash           string               `json:"-"`
	RulePackSnapshotDigest string               `json:"-"`
	RulePackCompilerRef    string               `json:"-"`
	// RulePackID and RulePackVersion name the governed pack frozen at
	// acceptance; both stay empty for the synthetic baseline compiler.
	RulePackID              string `json:"-"`
	RulePackVersion         string `json:"-"`
	RulePackPointerRevision int64  `json:"-"`
	// PlannerConfigurationID freezes the agent planner configuration resolved at
	// acceptance so a retry can never switch prompt or limits. The full frozen
	// payload is kept alongside it so the worker reconstructs the approved
	// configuration instead of re-reading process environment.
	PlannerConfigurationID string          `json:"-"`
	PlannerConfiguration   json.RawMessage `json:"-"`
}

// ReviewScopeIdentity is reconstructible from the immutable rule-set hash.
type ReviewScopeIdentity struct {
	ID             string `json:"id"`
	RuleSetID      string `json:"rule_set_id"`
	RuleSetVersion string `json:"rule_set_version"`
	RuleSetHash    string `json:"rule_set_hash"`
}

// AttemptParseSelection pins parser identity and a reserved artifact ID at acceptance.
type AttemptParseSelection struct {
	AttemptID            string `json:"attempt_id"`
	RunID                string `json:"run_id"`
	DocumentID           string `json:"document_id"`
	OriginalDigest       string `json:"original_digest"`
	ParsedDocumentID     string `json:"parsed_document_id"`
	SelectionOrder       int64  `json:"selection_order"`
	ParserID             string `json:"parser_id"`
	ParserVersion        string `json:"parser_version"`
	ConfigHash           string `json:"config_hash"`
	NormalizationVersion string `json:"normalization_version"`
	ArtifactDigest       string `json:"artifact_digest"`
}

// FrozenMaterialSelection pins a selected material version for this attempt.
type FrozenMaterialSelection struct {
	MaterialID string `json:"material_id"`
	Role       string `json:"role"`
	Purpose    string `json:"purpose"`
	Version    int    `json:"version"`
}

// FrozenDocument stores parser output bound to an immutable original and parse identity.
type FrozenDocument[Original OriginalDocument] struct {
	Document             Original                 `json:"document"`
	Material             *FrozenMaterialSelection `json:"material,omitempty"`
	Ready                bool                     `json:"ready"`
	FromOCR              bool                     `json:"from_ocr"`
	ParseError           string                   `json:"parse_error"`
	Text                 string                   `json:"text"`
	IR                   docir.IR                 `json:"ir"`
	OriginalHash         string                   `json:"original_hash"`
	NormalizedHash       *string                  `json:"normalized_hash"`
	ParserRef            string                   `json:"parser_ref"`
	ParsedDocumentID     string                   `json:"parsed_document_id,omitempty"`
	SelectionOrder       int64                    `json:"selection_order,omitempty"`
	ParsedArtifactDigest string                   `json:"parsed_artifact_digest,omitempty"`
	ConfigHash           string                   `json:"config_hash,omitempty"`
	ParserID             string                   `json:"parser_id,omitempty"`
	ParserVersion        string                   `json:"parser_version,omitempty"`
	NormalizationVersion string                   `json:"normalization_version,omitempty"`
	IRDigest             string                   `json:"ir_digest,omitempty"`
	ParsedCreatedAt      string                   `json:"parsed_created_at,omitempty"`
	ParsedStatus         string                   `json:"parsed_status,omitempty"`
}

// AttemptInput is the immutable input record reused by retries.
type AttemptInput[Original OriginalDocument] struct {
	AttemptID   string                     `json:"attempt_id"`
	RunID       string                     `json:"run_id"`
	InputHash   string                     `json:"input_hash"`
	FrozenAt    string                     `json:"frozen_at"`
	ReviewScope *ReviewScopeIdentity       `json:"review_scope,omitempty"`
	Documents   []FrozenDocument[Original] `json:"documents"`
}

// RunSnapshot is the immutable input of one review run. The three context
// parameters are fixed to their domain-owned values by Store aliases.
type RunSnapshot[PartyItem, KnowledgeSnapshot, OperatorContext any] struct {
	RunID                    string               `json:"run_id"`
	DocumentIDs              []string             `json:"document_ids"`
	PartyContext             []PartyItem          `json:"party_context,omitempty"`
	PartyContextHash         string               `json:"party_context_hash,omitempty"`
	ServiceObjectContext     *OperatorContext     `json:"service_object_context,omitempty"`
	ServiceObjectContextHash string               `json:"service_object_context_hash,omitempty"`
	ParsedDocumentIDs        []string             `json:"parsed_document_ids,omitempty"`
	ParseSelectionHash       string               `json:"parse_selection_hash,omitempty"`
	ParserConfigurationID    string               `json:"parser_configuration_id,omitempty"`
	ReviewScope              *ReviewScopeIdentity `json:"review_scope,omitempty"`
	RuleSetHash              string               `json:"rule_set_hash"`
	PlaybookHash             string               `json:"playbook_hash"`
	ModelRef                 string               `json:"model_ref"`
	ModelConfigurationID     string               `json:"model_configuration_id,omitempty"`
	ComparatorRef            string               `json:"comparator_ref"`
	RulePackCompilerRef      string               `json:"rule_pack_compiler_ref,omitempty"`
	// RulePack identity is frozen at acceptance. PackID and PackVersion are
	// empty when no governed pack applied and the synthetic baseline compiler ran.
	RulePackID          string          `json:"rule_pack_id,omitempty"`
	RulePackVersion     string          `json:"rule_pack_version,omitempty"`
	RulePackHash        string          `json:"rule_pack_hash,omitempty"`
	RulePackDigest      string          `json:"rule_pack_digest,omitempty"`
	RulePackSnapshot    []byte          `json:"-"`
	CloudPolicySnapshot json.RawMessage `json:"cloud_policy_snapshot,omitempty"`
	CloudPolicyDigest   string          `json:"cloud_policy_digest,omitempty"`
	// GovernedKnowledge is the server-resolved acceptance snapshot. Older runs
	// omit it entirely, preserving their historical frozen input identity.
	GovernedKnowledge            *KnowledgeSnapshot `json:"governed_knowledge,omitempty"`
	PlannerConfigurationID       string             `json:"-"`
	PlannerConfiguration         json.RawMessage    `json:"-"`
	InputHashVersion             int                `json:"-"`
	AdoptedTenantConfigVersionID string             `json:"-"`
	AdoptedTenantConfigDigest    string             `json:"-"`
}

// Anchor cites the exact frozen document parse used by its attempt.
type Anchor struct {
	ID               string `json:"id"`
	DocumentID       string `json:"document_id"`
	ParsedDocumentID string `json:"parsed_document_id,omitempty"`
	NormalizedDigest string `json:"normalized_digest,omitempty"`
	Quote            string `json:"quote,omitempty"`
	Status           string `json:"status"`
	Start            int    `json:"start,omitempty"`
	End              int    `json:"end,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Page             *int   `json:"page,omitempty"`
	ElementID        string `json:"element_id,omitempty"`
}

// EvaluationEvidenceRef points into one selected, ready parse of the attempt.
// Offsets are Unicode code points in [Start, End), not byte indices.
type EvaluationEvidenceRef struct {
	DocumentID       string `json:"document_id"`
	ParsedDocumentID string `json:"parsed_document_id"`
	NormalizedDigest string `json:"normalized_digest"`
	Quote            string `json:"quote"`
	Start            int    `json:"start"`
	End              int    `json:"end"`
}

// EvaluationEvidence records an exact citation used by a canonical evaluation.
type EvaluationEvidence struct {
	FactID string                `json:"fact_id,omitempty"`
	Ref    EvaluationEvidenceRef `json:"ref"`
}

// ConstraintEval is the rule result for one run. It is not a human decision.
type ConstraintEval struct {
	ID           string               `json:"id"`
	RunID        string               `json:"run_id"`
	AttemptID    string               `json:"attempt_id"`
	CaseID       string               `json:"case_id"`
	Domain       string               `json:"domain"`
	RuleID       string               `json:"rule_id,omitempty"`
	RuleVersion  string               `json:"rule_version"`
	CapabilityID string               `json:"capability_id,omitempty"`
	Mandatory    bool                 `json:"mandatory,omitempty"`
	Relation     string               `json:"relation"`
	ReasonCode   string               `json:"reason_code,omitempty"`
	Reason       string               `json:"reason"`
	FactIDs      []string             `json:"fact_ids,omitempty"`
	ObjectRefs   []string             `json:"object_refs,omitempty"`
	Evidence     []EvaluationEvidence `json:"evidence,omitempty"`
	FindingID    string               `json:"finding_id,omitempty"`
}

// FindingSeveritySource identifies the governed policy clause that contributed
// the proposed severity.
type FindingSeveritySource struct {
	PolicyID      string `json:"policy_id"`
	PolicyVersion string `json:"policy_version"`
	Clause        string `json:"clause"`
	Reason        string `json:"reason"`
}

// Finding is a proposal, not a human decision.
type Finding struct {
	ID                    string                 `json:"id"`
	RunID                 string                 `json:"run_id"`
	AttemptID             string                 `json:"attempt_id"`
	CaseID                string                 `json:"case_id"`
	Origin                string                 `json:"origin"`
	Domain                string                 `json:"domain"`
	RuleID                string                 `json:"rule_id,omitempty"`
	CapabilityID          string                 `json:"capability_id,omitempty"`
	ObjectRef             string                 `json:"object_ref,omitempty"`
	Claim                 string                 `json:"claim"`
	Relation              string                 `json:"relation"`
	DispositionSuggestion string                 `json:"disposition_suggestion,omitempty"`
	UnverifiedReason      string                 `json:"unverified_reason,omitempty"`
	Severity              *string                `json:"severity"`
	SeveritySource        *FindingSeveritySource `json:"severity_source"`
	Version               int                    `json:"version"`
	Anchors               []Anchor               `json:"anchors"`
	CreatedAt             string                 `json:"created_at"`
}
