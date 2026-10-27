package agentflow

import (
	"context"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	maxContextReferences            = 128
	MaxKnowledgeRecords             = 4096
	PlannerKnowledgeReferenceBudget = 32
	PlannerKnowledgeFieldBytes      = 512
	maxPlannerReferences            = PlannerKnowledgeReferenceBudget
	maxPlannerFacts                 = 32
	maxPlannerObservations          = 64
	maxContextFieldBytes            = 16 << 10
	maxPlannerFieldBytes            = PlannerKnowledgeFieldBytes
	maxPlannerValueBytes            = 256
	maxObservationIDs               = 32
)

// The caller is responsible for authorization, publication, applicability and
// withdrawal checks on the frozen list. This validation only checks its shape
// and revalidates supplied typed facts against this attempt's frozen sources.
func validatePlannerContext(ctx context.Context, request Request) error {
	if len(request.Knowledge) > MaxKnowledgeRecords || len(request.VerifiedFacts) > maxContextReferences || len(request.CanonicalEvaluations) > maxContextReferences {
		return ErrInput
	}
	seenReferences := map[string]bool{}
	for _, ref := range request.Knowledge {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !boundedIdentity(ref.KnowledgeID) || !boundedIdentity(ref.VersionID) ||
			!normalizedDigest.MatchString(ref.ContentSHA256) ||
			!slices.Contains([]string{"policy", "guidance", "exception", "experience"}, ref.Kind) ||
			((ref.Kind == "experience" || ref.Kind == "exception") && !ref.RequiresFactEvaluation) ||
			!boundedContextField(ref.Summary) || !boundedContextField(ref.Applicability) || !boundedContextField(ref.Exclusions) ||
			!boundedContextField(ref.CheckAction) || !boundedContextField(ref.MinimumEvidence) || !boundedContextField(ref.Counterexamples) || !boundedContextField(ref.SemanticChecks) || !boundedContextField(ref.SourceSpans) {
			return ErrInput
		}
		key := ref.KnowledgeID + "\x00" + ref.VersionID
		if seenReferences[key] {
			return ErrInput
		}
		seenReferences[key] = true
	}
	if err := validateKnowledgeSelection(request); err != nil {
		return err
	}
	seenEvaluations := map[string]bool{}
	for _, evaluation := range request.CanonicalEvaluations {
		if !boundedIdentity(evaluation.ID) || seenEvaluations[evaluation.ID] || !validDomain(evaluation.Domain) ||
			!capabilityID.MatchString(evaluation.CapabilityID) || len(evaluation.ReasonCode) > 128 || !utf8.ValidString(evaluation.ReasonCode) ||
			len(evaluation.EvidenceIDs) > 40 || !slices.Contains([]string{"compatible", "conflict", "insufficient", "unsupported", "unknown", "not_applicable"}, evaluation.Relation) {
			return ErrInput
		}
		for _, id := range evaluation.EvidenceIDs {
			if !boundedIdentity(id) {
				return ErrInput
			}
		}
		seenEvaluations[evaluation.ID] = true
	}
	gaps := make(map[string]Gap, len(request.Gaps))
	for _, gap := range request.Gaps {
		gaps[gap.ID] = gap
	}
	byID := make(map[string]Evidence, len(request.Evidence))
	for _, evidence := range request.Evidence {
		byID[evidence.ID] = evidence
	}
	runesByID := map[string][]rune{}
	seenFacts := map[string]bool{}
	for _, verified := range request.VerifiedFacts {
		if err := ctx.Err(); err != nil {
			return err
		}
		gap, found := gaps[verified.GapID]
		fact := verified.Fact
		citation := fact.Citation
		item, exists := byID[citation.EvidenceID]
		if !found || gap.Domain != verified.Domain || !exists || seenFacts[fact.ID] || len(citation.Quote) > maxQuoteBytes {
			return ErrInput
		}
		text, loaded := runesByID[item.ID]
		if !loaded {
			text = []rune(item.Text)
			runesByID[item.ID] = text
		}
		if citation.Quote == "" || citation.Start < 0 || citation.End <= citation.Start || citation.End > len(text) ||
			string(text[citation.Start:citation.End]) != citation.Quote {
			return ErrInput
		}
		finding := Finding{Domain: verified.Domain, Capability: gap.Capability, Citations: []Citation{citation}}
		if validateSemanticFact(fact, finding, byID) != nil {
			return ErrInput
		}
		seenFacts[fact.ID] = true
	}
	return nil
}

func boundedIdentity(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 128 && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func boundedContextField(value string) bool {
	return len(value) <= maxContextFieldBytes && utf8.ValidString(value)
}

func clipPlannerField(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	end := limit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end], true
}

func (state *execution) planningGaps() []Gap {
	remaining := state.remainingGaps()
	out := make([]Gap, 0, min(len(remaining), maxPlanningGaps))
	for _, mandatory := range []bool{true, false} {
		for _, gap := range remaining {
			if gap.Mandatory == mandatory && len(out) < maxPlanningGaps {
				out = append(out, gap)
			}
		}
	}
	return out
}

func (state *execution) plannerInput(remainingCalls int) PlanInput {
	input := PlanInput{Scope: slices.Clone(state.request.Scope), Gaps: slices.Clone(state.activeGaps),
		PreviouslyCalled: state.plannerHistory(), RemainingCalls: remainingCalls,
		Knowledge: []KnowledgeReference{}, VerifiedFacts: []PlannerFact{}, MandatoryChecks: []MandatoryCheck{}, Observations: []ToolObservation{}}
	if selection := state.request.KnowledgeSelection; selection != nil {
		input.KnowledgePolicyStatus = selection.PolicyStatus
		input.KnowledgePolicyReason = selection.PolicyReason
		input.Truncation.KnowledgeOmitted = selection.Budget.OptionalOmitted + selection.Budget.AcceptanceOmitted
	}
	if state.knowledgeReaderEnabled && state.request.KnowledgeSelection != nil && state.request.KnowledgeSelection.SnapshotSHA256 != "" {
		input.KnowledgeReadBudget = &KnowledgeReadBudget{Remaining: min(remainingCalls, KnowledgeReadMaxCalls-state.knowledgeReadCalls),
			MaxRunes: KnowledgeReadMaxRunes, MaxBytes: KnowledgeReadMaxBytes}
		input.KnowledgeObservations = slices.Clone(state.knowledgeObservations)
	}
	for i := range input.Gaps {
		var truncated bool
		input.Gaps[i].Question, truncated = clipPlannerField(input.Gaps[i].Question, maxPlannerFieldBytes)
		input.Gaps[i].QuestionTruncated = truncated
		input.Truncation.FieldsTruncated = input.Truncation.FieldsTruncated || truncated
	}
	for _, gap := range state.request.Gaps {
		if !gap.Mandatory {
			continue
		}
		status, reason := projectedGapState(state.gapStates[gap.ID])
		input.MandatoryChecks = append(input.MandatoryChecks, MandatoryCheck{GapID: gap.ID, Domain: gap.Domain, Capability: gap.Capability, Status: status, Reason: reason})
	}
	for _, evaluation := range state.request.CanonicalEvaluations {
		if !evaluation.Mandatory || !slices.Contains(state.request.Scope, evaluation.Domain) {
			continue
		}
		status, reason := state.canonicalEvaluationStatus(evaluation)
		input.MandatoryChecks = append(input.MandatoryChecks, MandatoryCheck{GapID: evaluation.ID, Domain: evaluation.Domain,
			Capability: evaluation.CapabilityID, Status: status, Reason: reason})
	}
	if slices.Contains(state.request.Scope, Compliance) {
		input.MandatoryChecks = append(input.MandatoryChecks, knowledgePolicyChecks(state.request.KnowledgeSelection)...)
	}
	// Required references precede optional ones; the input order within each
	// group is frozen and therefore provides a deterministic tie-break.
	for _, required := range []bool{true, false} {
		for _, ref := range state.request.Knowledge {
			if ref.Required != required {
				continue
			}
			if !required && len(input.Knowledge) >= maxPlannerReferences {
				input.Truncation.KnowledgeOmitted++
				continue
			}
			var clipped bool
			ref.Summary, clipped = clipPlannerField(ref.Summary, maxPlannerFieldBytes)
			ref.Truncated = ref.Truncated || clipped
			ref.Applicability, clipped = clipPlannerField(ref.Applicability, maxPlannerFieldBytes)
			ref.Truncated = ref.Truncated || clipped
			ref.Exclusions, clipped = clipPlannerField(ref.Exclusions, maxPlannerFieldBytes)
			ref.Truncated = ref.Truncated || clipped
			ref.CheckAction, clipped = clipPlannerField(ref.CheckAction, maxPlannerFieldBytes)
			ref.Truncated = ref.Truncated || clipped
			ref.MinimumEvidence, clipped = clipPlannerField(ref.MinimumEvidence, maxPlannerFieldBytes)
			ref.Truncated = ref.Truncated || clipped
			ref.Counterexamples, clipped = clipPlannerField(ref.Counterexamples, maxPlannerFieldBytes)
			ref.Truncated = ref.Truncated || clipped
			ref.SemanticChecks, clipped = clipPlannerField(ref.SemanticChecks, maxPlannerFieldBytes)
			ref.Truncated = ref.Truncated || clipped
			ref.SourceSpans, clipped = clipPlannerField(ref.SourceSpans, maxPlannerFieldBytes)
			ref.Truncated = ref.Truncated || clipped
			input.Truncation.FieldsTruncated = input.Truncation.FieldsTruncated || ref.Truncated
			input.Knowledge = append(input.Knowledge, ref)
		}
	}
	factIDs := make([]string, 0, len(state.verifiedFactsByID))
	for id := range state.verifiedFactsByID {
		factIDs = append(factIDs, id)
	}
	slices.Sort(factIDs)
	for _, id := range factIDs {
		if len(input.VerifiedFacts) == maxPlannerFacts {
			input.Truncation.FactsOmitted++
			continue
		}
		verified := state.verifiedFactsByID[id]
		fact, citation := verified.Fact, verified.Fact.Citation
		value, clipped := clipPlannerField(fact.Value, maxPlannerValueBytes)
		input.Truncation.FieldsTruncated = input.Truncation.FieldsTruncated || clipped
		input.VerifiedFacts = append(input.VerifiedFacts, PlannerFact{GapID: verified.GapID, Domain: verified.Domain,
			FactID: fact.ID, Type: fact.Type, Value: value, Unit: fact.Unit, Currency: fact.Currency,
			EvidenceID: citation.EvidenceID, Version: citation.Version, Start: citation.Start, End: citation.End, Truncated: clipped})
	}
	for _, observation := range state.toolObservations {
		observation.FactIDs = slices.Clone(observation.FactIDs)
		observation.EvidenceIDs = slices.Clone(observation.EvidenceIDs)
		input.Observations = append(input.Observations, observation)
	}
	input.Truncation.ObservationsOmitted = state.omittedObservations
	for _, observation := range input.Observations {
		input.Truncation.FieldsTruncated = input.Truncation.FieldsTruncated || observation.Truncated
	}
	return input
}

func (state *execution) recordToolObservations(call Call, callID string, findings []Finding) {
	for _, finding := range findings {
		if len(state.toolObservations) == maxPlannerObservations {
			state.omittedObservations++
			continue
		}
		summary, clipped := clipPlannerField(finding.Summary, maxPlannerFieldBytes)
		observation := ToolObservation{Round: state.round, CallID: callID, GapID: call.GapID, ToolID: call.ToolID,
			Relation: finding.Relation, ReasonCode: finding.ReasonCode, Summary: summary,
			ClaimStatus: "tool_reported_not_independently_verified", FactIDs: []string{}, EvidenceIDs: []string{}, Truncated: clipped}
		for _, fact := range finding.Facts {
			if len(observation.FactIDs) == maxObservationIDs {
				observation.Truncated = true
				break
			}
			observation.FactIDs = append(observation.FactIDs, fact.ID)
		}
		for _, citation := range finding.Citations {
			if slices.Contains(observation.EvidenceIDs, citation.EvidenceID) {
				continue
			}
			if len(observation.EvidenceIDs) == maxObservationIDs {
				observation.Truncated = true
				break
			}
			observation.EvidenceIDs = append(observation.EvidenceIDs, citation.EvidenceID)
		}
		state.toolObservations = append(state.toolObservations, observation)
	}
}

// Page and element locations require a frozen document IR mapping. This runtime
// receives normalized text only, so it retains text offsets but cannot attest
// source-page or element metadata returned by a tool.
func clearUnverifiedLocations(findings []Finding) {
	for i := range findings {
		for j := range findings[i].Citations {
			findings[i].Citations[j].Page = nil
			findings[i].Citations[j].ElementID = ""
		}
		for j := range findings[i].Facts {
			findings[i].Facts[j].Citation.Page = nil
			findings[i].Facts[j].Citation.ElementID = ""
		}
	}
}

func (state *execution) canonicalEvaluationStatus(evaluation CanonicalEvaluation) (string, string) {
	status := canonicalProjectionStatus(evaluation.Relation)
	if status == "completed" || status == "not_applicable" {
		if len(evaluation.EvidenceIDs) == 0 {
			return "unknown", "canonical_evidence_unavailable"
		}
		for _, id := range evaluation.EvidenceIDs {
			found := false
			for _, evidence := range state.request.Evidence {
				if evidence.ID == id {
					found = true
					break
				}
			}
			if !found {
				return "unknown", "canonical_evidence_unavailable"
			}
		}
	}
	if status != "completed" && status != "not_applicable" && evaluation.ReasonCode == "" {
		return status, "canonical_result_incomplete"
	}
	return status, evaluation.ReasonCode
}

// Only an unambiguous canonical result with frozen source identities can
// discharge an exactly matching gap without a duplicate read-only tool call.
func (state *execution) reuseCanonicalChecks() {
	for _, gap := range state.request.Gaps {
		var selected *CanonicalEvaluation
		matches := 0
		for i := range state.request.CanonicalEvaluations {
			evaluation := &state.request.CanonicalEvaluations[i]
			if evaluation.Domain == gap.Domain && evaluation.CapabilityID == gap.Capability {
				matches++
				selected = evaluation
			}
		}
		if matches != 1 {
			continue
		}
		status, _ := state.canonicalEvaluationStatus(*selected)
		if status != "completed" && status != "not_applicable" {
			continue
		}
		state.gapStates[gap.ID] = "resolved"
		state.resolved[gap.ID] = true
	}
}

// Revalidate earlier outputs and account for every prior call. The execution
// owner supplies the complete earlier-round list and its original order.
func (r *Runtime) validatePriorToolResults(ctx context.Context, request Request) error {
	if len(request.PriorObservations) > r.config.MaxCalls {
		return ErrInput
	}
	if len(request.PriorObservations) == 0 {
		if request.PriorEventCount != 0 || request.PriorElapsedMS != nil {
			return ErrInput
		}
		return nil
	}
	if request.PriorEventCount <= 0 || request.PriorEventCount > 1024 || request.PriorElapsedMS == nil || *request.PriorElapsedMS < 0 {
		return ErrInput
	}
	gaps := make(map[string]Gap, len(request.Gaps))
	for _, gap := range request.Gaps {
		gaps[gap.ID] = gap
	}
	lastRound := 0
	minimumEvents := 0
	seen := map[string]bool{}
	for _, result := range request.PriorObservations {
		if err := ctx.Err(); err != nil {
			return err
		}
		gap, exists := gaps[result.GapID]
		spec, registered := r.specByID[result.ToolID]
		if !exists || !registered || result.ToolVersion != spec.Version || spec.Domain != gap.Domain ||
			!slices.Contains(spec.Capabilities, gap.Capability) || result.Round < 1 || result.Round > r.config.MaxRounds ||
			result.Round < lastRound || result.Round > lastRound+1 {
			return ErrInput
		}
		if result.Round != lastRound {
			minimumEvents += 2
		}
		minimumEvents += 2 // tool_started and a terminal event
		call := Call{ToolID: result.ToolID, GapID: result.GapID}
		id := stableCallID(request.RequestID, result.Round, call)
		if seen[id] {
			return ErrInput
		}
		seen[id] = true
		lastRound = result.Round
		if result.FailureCode != "" {
			if !capabilityID.MatchString(result.FailureCode) || len(result.Findings) != 0 {
				return ErrInput
			}
			continue
		}
		minimumEvents++ // a successful result also emitted tool_observed
		if err := validateFindings(ctx, result.Findings, gap.Domain, gap.Capability, request.Evidence); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrInput
		}
	}
	if request.PriorEventCount < minimumEvents {
		return ErrInput
	}
	return nil
}

func (state *execution) acceptPriorToolResults() (int, error) {
	lastRound := 0
	for _, result := range state.request.PriorObservations {
		lastRound = result.Round
		state.round = result.Round
		gap := state.gaps[result.GapID]
		call := Call{ToolID: result.ToolID, GapID: result.GapID, Domain: gap.Domain}
		callID := stableCallID(state.request.RequestID, result.Round, call)
		pair := result.GapID + "\x00" + result.ToolID
		if result.FailureCode != "" {
			state.gapStates[result.GapID] = "unknown:" + result.FailureCode
		} else {
			findings := cloneFindings(result.Findings)
			clearUnverifiedLocations(findings)
			if err := state.validateFactConsistency(findings); err != nil {
				return 0, ErrInput
			}
			state.recordToolObservations(call, callID, findings)
			// A supplied result has no fresh comparator execution in this runtime.
			markClaimsUnverified(findings)
			if err := state.recordFindings(result.GapID, findings); err != nil {
				return 0, err
			}
			state.gapStates[result.GapID], _ = gapOutcome(findings)
		}
		state.callEvidenceCounts[pair] = len(state.gapEvidenceIDs[result.GapID])
		state.previousCallByPair[pair] = PriorCall{ToolID: result.ToolID, GapID: result.GapID}
	}
	state.report.ToolCalls = len(state.request.PriorObservations)
	state.round = 0
	return lastRound, nil
}

func (state *execution) reconcileCanonicalChecks() {
	for _, gap := range state.request.Gaps {
		var selected *CanonicalEvaluation
		matches := 0
		for i := range state.request.CanonicalEvaluations {
			evaluation := &state.request.CanonicalEvaluations[i]
			if evaluation.Domain == gap.Domain && evaluation.CapabilityID == gap.Capability {
				matches++
				selected = evaluation
			}
		}
		if matches == 0 {
			continue
		}
		if matches > 1 {
			state.resolved[gap.ID] = false
			state.gapStates[gap.ID] = "unknown:multiple_canonical_results_require_review"
			continue
		}
		status, reason := state.canonicalEvaluationStatus(*selected)
		if status != "completed" && status != "not_applicable" {
			state.resolved[gap.ID] = false
			state.gapStates[gap.ID] = status + ":" + reason
		}
	}
}
