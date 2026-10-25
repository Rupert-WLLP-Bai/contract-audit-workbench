package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"contractaudit/internal/rulepack"
)

// These mirror the frozen-evidence transport contract. The tool gateway keeps
// its payload types private, so the worker decodes the same documented JSON into
// its own types; any drift fails the identity guard instead of being accepted.
const (
	toolEvidenceSchemaVersion = "frozen-evidence/v1"
	toolEvidenceToolVersion   = "frozen-evidence/v1"
	toolEvidenceStatusFound   = "found"
	toolEvidenceStatusEmpty   = "no_supported_statement"
	maxToolEvidenceFacts      = 32
	maxToolEvidenceQuoteRunes = 4096
	toolEvidenceCallTimeout   = 30 * time.Second
)

var errToolEvidenceContract = errors.New("tool evidence payload does not match the frozen attempt")

type toolEvidencePayload struct {
	SchemaVersion string             `json:"schema_version"`
	ToolVersion   string             `json:"tool_version"`
	CaseID        string             `json:"case_id"`
	RunID         string             `json:"run_id"`
	AttemptID     string             `json:"attempt_id"`
	CapabilityID  string             `json:"capability_id"`
	Status        string             `json:"status"`
	Facts         []toolEvidenceFact `json:"facts"`
}

type toolEvidenceFact struct {
	RuleID            string                 `json:"rule_id"`
	ObjectRef         string                 `json:"object_ref"`
	Value             string                 `json:"value"`
	Unit              string                 `json:"unit"`
	MeasurementWindow string                 `json:"measurement_window"`
	TimeZone          string                 `json:"time_zone"`
	Calendar          string                 `json:"calendar"`
	OutageDefinition  string                 `json:"outage_definition"`
	Exclusions        []string               `json:"exclusions"`
	Basis             string                 `json:"basis"`
	Citations         []toolEvidenceCitation `json:"citations"`
}

type toolEvidenceCitation struct {
	DocumentID           string `json:"document_id"`
	Filename             string `json:"filename"`
	DocumentRole         string `json:"document_role"`
	OriginalDigest       string `json:"original_digest"`
	ParsedDocumentID     string `json:"parsed_document_id"`
	ParserID             string `json:"parser_id"`
	ParserVersion        string `json:"parser_version"`
	ConfigHash           string `json:"config_hash"`
	NormalizationVersion string `json:"normalization_version"`
	NormalizedDigest     string `json:"normalized_digest"`
	ArtifactDigest       string `json:"artifact_digest"`
	SourceVersion        string `json:"source_version"`
	SourceDigest         string `json:"source_digest"`
	Start                int    `json:"start"`
	End                  int    `json:"end"`
	Quote                string `json:"quote"`
}

// mandatoryToolCapability is one frozen-evidence capability and the rule whose
// draft-side evidence it may contribute. The slice is ordered so the calls,
// events and budget accounting are deterministic.
type mandatoryToolCapability struct {
	CapabilityID string
	RuleID       string
}

var mandatoryToolCapabilities = []mandatoryToolCapability{
	{CapabilityID: "dedicated_line.minimum_bandwidth", RuleID: "ENT-TECH-BW-001"},
	{CapabilityID: "dedicated_line.monthly_availability", RuleID: "ENT-TECH-SLA-001"},
	{CapabilityID: "payment.advance_ratio.ceiling", RuleID: "ENT-COM-ADV-001"},
}

// Tool evidence statuses. Every mandatory capability is reported separately so a
// mixed run can never be summarized as a blanket success.
const (
	toolEvidenceAccepted    = "accepted"
	toolEvidenceNoStatement = "no_statement"
	toolEvidenceFailed      = "failed"
	toolEvidenceRejected    = "rejected"
)

// toolEvidenceCapability is the per-capability outcome of one tool round.
type toolEvidenceCapability struct {
	CapabilityID string
	Status       string
	ReasonCode   string
	Accepted     int
	Rejected     int
}

// toolEvidenceOutcome aggregates the mandatory capabilities. Incomplete is true
// when any mandatory capability failed or returned an ungrounded fact: the
// deterministic rules still run, but the attempt must not look complete.
type toolEvidenceOutcome struct {
	AdapterPresent bool
	Accepted       []rulepack.Fact
	Capabilities   []toolEvidenceCapability
	Incomplete     bool
}

// toolEvidenceFactKey is the comparable identity of one admitted fact: the
// canonical fact ID plus the citation it was admitted under. It is typed rather
// than concatenated so unbounded quote text never becomes part of a map key.
type toolEvidenceFactKey struct {
	ID           string
	SourceID     string
	SourceDigest string
	Start        int
	End          int
}

// consumeToolEvidence asks the installed reader for the draft-side evidence of
// every mandatory capability under the current durable claim, admits only facts
// that the attempt's own full-source extraction already produced at the same
// citation position, and returns them for the RulePack. It never removes
// deterministic facts: an absent reader, a failed call, or a rejected payload
// leaves the local extraction untouched.
func (s *Store) consumeToolEvidence(ctx context.Context, runID, attemptID string, claim JobClaim, caseItem Case, request rulepack.Request, docs []Interpreted) (toolEvidenceOutcome, error) {
	reader := s.toolEvidenceReader()
	if reader == nil {
		return toolEvidenceOutcome{}, nil
	}
	outcome := toolEvidenceOutcome{AdapterPresent: true}
	if claim.JobID == "" || claim.AttemptID != attemptID || claim.RunID != runID || claim.WorkerID == "" || claim.Generation <= 0 {
		return outcome, errors.New("tool evidence requires the current durable job claim")
	}
	if !isTelecomDedicatedLineCase(caseItem) {
		return outcome, nil
	}
	deadline := time.Now().UTC().Add(toolEvidenceCallTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	frozen := frozenDraftSources(docs, request)
	canonical := indexToolEvidenceFacts(request.Facts)
	for _, capability := range mandatoryToolCapabilities {
		if err := ctx.Err(); err != nil {
			return outcome, err
		}
		entry := toolEvidenceCapability{CapabilityID: capability.CapabilityID}
		if len(frozen) == 0 {
			entry.Status, entry.ReasonCode = toolEvidenceFailed, "tool_evidence_no_frozen_draft"
			outcome.Incomplete = true
			outcome.Capabilities = append(outcome.Capabilities, entry)
			continue
		}
		raw, err := reader.FrozenEvidence(ctx, ToolEvidenceRequest{
			TenantID: claim.TenantID, OrgID: claim.OrgID, JobID: claim.JobID, WorkerID: claim.WorkerID,
			Generation: claim.Generation, CaseID: claim.CaseID, RunID: runID, AttemptID: attemptID,
			CapabilityID: capability.CapabilityID, Deadline: deadline,
		})
		if err != nil {
			entry.Status, entry.ReasonCode = toolEvidenceFailed, "tool_evidence_call_failed"
			outcome.Incomplete = true
			outcome.Capabilities = append(outcome.Capabilities, entry)
			continue
		}
		facts, status, reason, rejected, err := validateToolEvidencePayload(raw, claim, runID, attemptID, capability, frozen, canonical)
		entry.Status, entry.ReasonCode, entry.Rejected = status, reason, rejected
		entry.Accepted = len(facts)
		outcome.Capabilities = append(outcome.Capabilities, entry)
		if err != nil {
			outcome.Incomplete = true
			continue
		}
		outcome.Accepted = append(outcome.Accepted, facts...)
	}
	return outcome, nil
}

// frozenDraftSource is one immutable draft document of the selected attempt, in
// the form the validator needs to re-check a citation against the frozen text.
type frozenDraftSource struct {
	document Document
	parsed   *ParsedDocument
	text     string
}

// frozenDraftSources keeps only sources whose frozen metadata is complete and
// internally consistent: a payload citation must never be able to match an
// empty digest, an empty tenant, or a text that differs from the parsed text.
func frozenDraftSources(docs []Interpreted, request rulepack.Request) map[string]frozenDraftSource {
	pinned := make(map[string]rulepack.Source, len(request.Sources))
	for _, source := range request.Sources {
		pinned[source.ID] = source
	}
	out := make(map[string]frozenDraftSource, len(docs))
	for _, item := range docs {
		source, selected := pinned[item.Document.ID]
		if !selected {
			continue
		}
		parsed := item.ParsedDocument
		if parsed == nil || item.Document.Role != "draft_contract" || !item.Ready || parsed.Status != "ready" {
			continue
		}
		if item.Document.ID == "" || item.Document.TenantID == "" || item.Document.CaseID == "" || item.Document.Digest == "" ||
			source.Role != "draft_contract" || source.Version == "" || source.Digest == "" || source.TenantID != item.Document.TenantID ||
			source.CaseID != item.Document.CaseID || source.Text != item.Text || item.Text == "" ||
			parsed.ID == "" || parsed.DocumentID != item.Document.ID || parsed.OriginalDigest != item.Document.Digest ||
			parsed.ParserID == "" || parsed.ParserVersion == "" || parsed.ConfigHash == "" || parsed.NormalizationVersion == "" ||
			parsed.NormalizedDigest == "" || parsed.ArtifactDigest == "" || parsed.Text != item.Text || parsed.Text == "" {
			continue
		}
		out[item.Document.ID] = frozenDraftSource{document: item.Document, parsed: parsed, text: item.Text}
	}
	return out
}

// indexToolEvidenceFacts indexes the canonical facts the attempt already
// extracted from the frozen full sources. Those facts carry the rule, the real
// codepoint offset and a stable ID derived from that offset, so an admitted
// fact keeps the identity of the statement it came from.
func indexToolEvidenceFacts(facts []rulepack.Fact) map[string][]rulepack.Fact {
	out := make(map[string][]rulepack.Fact, len(facts))
	for _, fact := range facts {
		if fact.RuleID == "" || fact.SourceRole != "draft_contract" || len(fact.Citations) == 0 || fact.ID == "" {
			continue
		}
		out[fact.RuleID] = append(out[fact.RuleID], fact)
	}
	return out
}

// validateToolEvidencePayload enforces the transport contract (versions and the
// exact case/run/attempt/capability binding) and admits only facts whose
// citation matches a canonical fact of this attempt at the same position.
func validateToolEvidencePayload(raw json.RawMessage, claim JobClaim, runID, attemptID string, capability mandatoryToolCapability, frozen map[string]frozenDraftSource, canonical map[string][]rulepack.Fact) ([]rulepack.Fact, string, string, int, error) {
	var payload toolEvidencePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, toolEvidenceRejected, "tool_evidence_payload_invalid", 0, errToolEvidenceContract
	}
	if payload.SchemaVersion != toolEvidenceSchemaVersion || payload.ToolVersion != toolEvidenceToolVersion ||
		payload.CaseID != claim.CaseID || payload.RunID != runID || payload.AttemptID != attemptID ||
		payload.CapabilityID != capability.CapabilityID {
		return nil, toolEvidenceRejected, "tool_evidence_binding_mismatch", 0, errToolEvidenceContract
	}
	canonicalFacts := canonical[capability.RuleID]
	switch payload.Status {
	case toolEvidenceStatusEmpty:
		if len(payload.Facts) != 0 {
			return nil, toolEvidenceRejected, "tool_evidence_status_mismatch", 0, errToolEvidenceContract
		}
		// An empty tool answer is only honest when this attempt found no draft
		// statement for the rule at all; dropping known statements is not.
		if len(canonicalFacts) == 0 {
			return nil, toolEvidenceNoStatement, "tool_evidence_no_statement", 0, nil
		}
		return nil, toolEvidenceRejected, "tool_evidence_missing_canonical_set", 0, errToolEvidenceContract
	case toolEvidenceStatusFound:
	default:
		return nil, toolEvidenceRejected, "tool_evidence_status_unknown", 0, errToolEvidenceContract
	}
	if len(payload.Facts) > maxToolEvidenceFacts {
		return nil, toolEvidenceRejected, "tool_evidence_too_many_facts", 0, errToolEvidenceContract
	}
	accepted := make([]rulepack.Fact, 0, len(payload.Facts))
	admittedIDs := make(map[string]struct{}, len(payload.Facts))
	rejected := 0
	for _, fact := range payload.Facts {
		admitted, ok := admitToolEvidenceFact(fact, capability.RuleID, frozen, canonical)
		if !ok {
			rejected++
			continue
		}
		if _, duplicate := admittedIDs[admitted.ID]; duplicate {
			rejected++
			continue
		}
		admittedIDs[admitted.ID] = struct{}{}
		accepted = append(accepted, admitted)
	}
	// The tool may only corroborate the complete draft evidence of the rule. A
	// subset, a duplicate or any ungrounded fact would change which statements
	// the comparison sees, so it is refused and the deterministic evidence stays
	// authoritative.
	if rejected > 0 {
		return nil, toolEvidenceRejected, "tool_evidence_not_grounded", rejected, errToolEvidenceContract
	}
	if len(accepted) != len(canonicalFacts) {
		return nil, toolEvidenceRejected, "tool_evidence_incomplete_set", rejected, errToolEvidenceContract
	}
	for _, fact := range canonicalFacts {
		if _, ok := admittedIDs[fact.ID]; !ok {
			return nil, toolEvidenceRejected, "tool_evidence_incomplete_set", rejected, errToolEvidenceContract
		}
	}
	return accepted, toolEvidenceAccepted, "", rejected, nil
}

// admitToolEvidenceFact admits one tool fact only when every citation quotes a
// frozen draft document at valid Unicode codepoint offsets and the attempt's own
// extraction produced the same rule at the same position with the same
// semantics. The admitted fact is that canonical fact, never a rebuilt one.
func admitToolEvidenceFact(fact toolEvidenceFact, ruleID string, frozen map[string]frozenDraftSource, canonical map[string][]rulepack.Fact) (rulepack.Fact, bool) {
	if fact.RuleID != ruleID || fact.Value == "" || len(fact.Citations) == 0 {
		return rulepack.Fact{}, false
	}
	for _, cite := range fact.Citations {
		source, ok := frozen[cite.DocumentID]
		if !ok {
			return rulepack.Fact{}, false
		}
		if cite.DocumentID == "" || cite.DocumentRole != "draft_contract" || cite.OriginalDigest == "" ||
			cite.OriginalDigest != source.document.Digest || cite.ParsedDocumentID == "" || cite.ParsedDocumentID != source.parsed.ID ||
			cite.ParserID == "" || cite.ParserID != source.parsed.ParserID || cite.ParserVersion == "" || cite.ParserVersion != source.parsed.ParserVersion ||
			cite.ConfigHash == "" || cite.ConfigHash != source.parsed.ConfigHash ||
			cite.NormalizationVersion == "" || cite.NormalizationVersion != source.parsed.NormalizationVersion ||
			cite.NormalizedDigest == "" || cite.NormalizedDigest != source.parsed.NormalizedDigest ||
			cite.ArtifactDigest == "" || cite.ArtifactDigest != source.parsed.ArtifactDigest ||
			cite.SourceVersion == "" || cite.SourceVersion != source.parsed.ID ||
			cite.SourceDigest == "" || cite.SourceDigest != source.parsed.NormalizedDigest {
			return rulepack.Fact{}, false
		}
		runes := []rune(source.text)
		if strings.TrimSpace(cite.Quote) == "" || cite.Start < 0 || cite.End <= cite.Start ||
			cite.End > len(runes) || cite.End-cite.Start > maxToolEvidenceQuoteRunes ||
			string(runes[cite.Start:cite.End]) != cite.Quote {
			return rulepack.Fact{}, false
		}
	}
	for _, candidate := range canonical[ruleID] {
		if !sameToolEvidenceCitations(fact.Citations, candidate.Citations) || !sameToolEvidenceSemantics(fact, candidate) {
			continue
		}
		return candidate, true
	}
	return rulepack.Fact{}, false
}

// sameToolEvidenceCitations requires the same sources, the same codepoint
// positions and the same quotes, in the same order.
func sameToolEvidenceCitations(transport []toolEvidenceCitation, local []rulepack.Citation) bool {
	if len(transport) != len(local) {
		return false
	}
	for i := range transport {
		if transport[i].DocumentID != local[i].SourceID || transport[i].Start != local[i].Start ||
			transport[i].End != local[i].End || transport[i].Quote != local[i].Quote {
			return false
		}
	}
	return true
}

func sameToolEvidenceSemantics(left toolEvidenceFact, right rulepack.Fact) bool {
	if left.RuleID != right.RuleID || left.ObjectRef != right.ObjectRef || left.Value != right.Value || left.Unit != right.Unit ||
		left.MeasurementWindow != right.MeasurementWindow || left.TimeZone != right.TimeZone || left.Calendar != right.Calendar ||
		left.OutageDefinition != right.OutageDefinition || left.Basis != right.Basis {
		return false
	}
	if len(left.Exclusions) != len(right.Exclusions) {
		return false
	}
	for i := range left.Exclusions {
		if left.Exclusions[i] != right.Exclusions[i] {
			return false
		}
	}
	return true
}

// appendToolEvidenceFacts adds the corroborated facts that are not already part
// of the deterministic evidence. It never removes or rewrites a fact: the tool
// can only confirm the draft statements the attempt already extracted, so the
// evaluator always sees the same statements and the same rule outcome.
func appendToolEvidenceFacts(existing, accepted []rulepack.Fact) []rulepack.Fact {
	if len(accepted) == 0 {
		return existing
	}
	seen := make(map[toolEvidenceFactKey]struct{}, len(existing)+len(accepted))
	for _, fact := range existing {
		seen[toolEvidenceKey(fact)] = struct{}{}
	}
	merged := append([]rulepack.Fact(nil), existing...)
	for _, fact := range accepted {
		key := toolEvidenceKey(fact)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, fact)
	}
	return merged
}

func toolEvidenceKey(fact rulepack.Fact) toolEvidenceFactKey {
	key := toolEvidenceFactKey{ID: fact.ID}
	if len(fact.Citations) > 0 {
		key.SourceID, key.SourceDigest = fact.Citations[0].SourceID, fact.Citations[0].SourceDigest
		key.Start, key.End = fact.Citations[0].Start, fact.Citations[0].End
	}
	return key
}
