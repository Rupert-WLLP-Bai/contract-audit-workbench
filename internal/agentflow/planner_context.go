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
	if !validPlannerContextCounts(request) {
		return ErrInput
	}
	if err := validateKnowledgeReferences(ctx, request.Knowledge); err != nil {
		return err
	}
	if err := validateKnowledgeSelection(request); err != nil {
		return err
	}
	if err := validateCanonicalEvaluations(request.CanonicalEvaluations); err != nil {
		return err
	}
	return validateVerifiedFacts(ctx, request)
}

func validPlannerContextCounts(request Request) bool {
	return len(request.Knowledge) <= MaxKnowledgeRecords && len(request.VerifiedFacts) <= maxContextReferences &&
		len(request.CanonicalEvaluations) <= maxContextReferences
}

func validateKnowledgeReferences(ctx context.Context, references []KnowledgeReference) error {
	seen := make(map[string]bool, len(references))
	for _, ref := range references {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !validKnowledgeReference(ref) {
			return ErrInput
		}
		key := ref.KnowledgeID + "\x00" + ref.VersionID
		if seen[key] {
			return ErrInput
		}
		seen[key] = true
	}
	return nil
}

func validKnowledgeReference(ref KnowledgeReference) bool {
	if !boundedIdentity(ref.KnowledgeID) || !boundedIdentity(ref.VersionID) || !normalizedDigest.MatchString(ref.ContentSHA256) ||
		!validKnowledgeReferenceKind(ref.Kind, ref.RequiresFactEvaluation) {
		return false
	}
	return validKnowledgeReferenceFields(ref)
}

func validKnowledgeReferenceKind(kind string, requiresFactEvaluation bool) bool {
	if !slices.Contains([]string{"policy", "guidance", "exception", "experience"}, kind) {
		return false
	}
	return (kind != "experience" && kind != "exception") || requiresFactEvaluation
}

func validKnowledgeReferenceFields(ref KnowledgeReference) bool {
	for _, field := range []string{ref.Summary, ref.Applicability, ref.Exclusions, ref.CheckAction,
		ref.MinimumEvidence, ref.Counterexamples, ref.SemanticChecks, ref.SourceSpans} {
		if !boundedContextField(field) {
			return false
		}
	}
	return true
}

func validateCanonicalEvaluations(evaluations []CanonicalEvaluation) error {
	seen := make(map[string]bool, len(evaluations))
	for _, evaluation := range evaluations {
		if !validCanonicalEvaluation(evaluation) || seen[evaluation.ID] {
			return ErrInput
		}
		for _, id := range evaluation.EvidenceIDs {
			if !boundedIdentity(id) {
				return ErrInput
			}
		}
		seen[evaluation.ID] = true
	}
	return nil
}

func validCanonicalEvaluation(evaluation CanonicalEvaluation) bool {
	return boundedIdentity(evaluation.ID) && validDomain(evaluation.Domain) && capabilityID.MatchString(evaluation.CapabilityID) &&
		len(evaluation.ReasonCode) <= 128 && utf8.ValidString(evaluation.ReasonCode) && len(evaluation.EvidenceIDs) <= 40 &&
		slices.Contains([]string{"compatible", "conflict", "insufficient", "unsupported", "unknown", "not_applicable"}, evaluation.Relation)
}

func validateVerifiedFacts(ctx context.Context, request Request) error {
	gaps := make(map[string]Gap, len(request.Gaps))
	for _, gap := range request.Gaps {
		gaps[gap.ID] = gap
	}
	evidence := make(map[string]Evidence, len(request.Evidence))
	for _, item := range request.Evidence {
		evidence[item.ID] = item
	}
	runesByID := make(map[string][]rune)
	seen := make(map[string]bool)
	for _, verified := range request.VerifiedFacts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateVerifiedFact(verified, gaps, evidence, runesByID, seen); err != nil {
			return err
		}
	}
	return nil
}

func validateVerifiedFact(verified VerifiedFact, gaps map[string]Gap, evidence map[string]Evidence, runesByID map[string][]rune, seen map[string]bool) error {
	gap, found := gaps[verified.GapID]
	fact, citation := verified.Fact, verified.Fact.Citation
	item, exists := evidence[citation.EvidenceID]
	if !validVerifiedFactBinding(verified, gap, found, fact, citation, exists, seen) {
		return ErrInput
	}
	if !citationMatchesFrozenText(citation, item, runesByID) {
		return ErrInput
	}
	finding := Finding{Domain: verified.Domain, Capability: gap.Capability, Citations: []Citation{citation}}
	if validateSemanticFact(fact, finding, evidence) != nil {
		return ErrInput
	}
	seen[fact.ID] = true
	return nil
}

func validVerifiedFactBinding(verified VerifiedFact, gap Gap, gapFound bool, fact SemanticFact, citation Citation, evidenceFound bool, seen map[string]bool) bool {
	return gapFound && gap.Domain == verified.Domain && evidenceFound && !seen[fact.ID] && len(citation.Quote) <= maxQuoteBytes
}

func citationMatchesFrozenText(citation Citation, item Evidence, runesByID map[string][]rune) bool {
	text, loaded := runesByID[item.ID]
	if !loaded {
		text = []rune(item.Text)
		runesByID[item.ID] = text
	}
	return citation.Quote != "" && citation.Start >= 0 && citation.End > citation.Start && citation.End <= len(text) &&
		string(text[citation.Start:citation.End]) == citation.Quote
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
	input := basePlannerInput(state, remainingCalls)
	appendPlannerGaps(&input, state)
	appendMandatoryChecks(&input, state)
	appendPlannerKnowledgeReferences(&input, state)
	appendPlannerFacts(&input, state)
	appendPlannerObservations(&input, state)
	return input
}

func basePlannerInput(state *execution, remainingCalls int) PlanInput {
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
	return input
}

func appendPlannerGaps(input *PlanInput, state *execution) {
	for i := range input.Gaps {
		question, truncated := clipPlannerField(input.Gaps[i].Question, maxPlannerFieldBytes)
		input.Gaps[i].Question = question
		input.Gaps[i].QuestionTruncated = truncated
		input.Truncation.FieldsTruncated = input.Truncation.FieldsTruncated || truncated
	}
}

func appendMandatoryChecks(input *PlanInput, state *execution) {
	for _, gap := range state.request.Gaps {
		if !gap.Mandatory {
			continue
		}
		status, reason := projectedGapState(state.gapStates[gap.ID])
		input.MandatoryChecks = append(input.MandatoryChecks, MandatoryCheck{
			GapID: gap.ID, Domain: gap.Domain, Capability: gap.Capability, Status: status, Reason: reason,
		})
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
}

func appendPlannerKnowledgeReferences(input *PlanInput, state *execution) {
	// Required references precede optional ones; the input order within each
	// group is frozen and therefore provides a deterministic tie-break.
	for _, required := range []bool{true, false} {
		for _, ref := range state.request.Knowledge {
			if ref.Required != required {
				continue
			}
			if omitOptionalReference(input, required) {
				continue
			}
			input.Knowledge = append(input.Knowledge, clipKnowledgeReference(ref, &input.Truncation))
		}
	}
}

func omitOptionalReference(input *PlanInput, required bool) bool {
	if required || len(input.Knowledge) < maxPlannerReferences {
		return false
	}
	input.Truncation.KnowledgeOmitted++
	return true
}

func clipKnowledgeReference(ref KnowledgeReference, truncation *PlannerContextTruncation) KnowledgeReference {
	fields := []*string{&ref.Summary, &ref.Applicability, &ref.Exclusions, &ref.CheckAction, &ref.MinimumEvidence,
		&ref.Counterexamples, &ref.SemanticChecks, &ref.SourceSpans}
	ref.Truncated = clipPlannerReferenceFields(fields) || ref.Truncated
	truncation.FieldsTruncated = truncation.FieldsTruncated || ref.Truncated
	return ref
}

func clipPlannerReferenceFields(fields []*string) bool {
	truncated := false
	for _, field := range fields {
		value, clipped := clipPlannerField(*field, maxPlannerFieldBytes)
		*field = value
		truncated = truncated || clipped
	}
	return truncated
}

func appendPlannerFacts(input *PlanInput, state *execution) {
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
}

func appendPlannerObservations(input *PlanInput, state *execution) {
	for _, observation := range state.toolObservations {
		observation.FactIDs = slices.Clone(observation.FactIDs)
		observation.EvidenceIDs = slices.Clone(observation.EvidenceIDs)
		input.Observations = append(input.Observations, observation)
	}
	input.Truncation.ObservationsOmitted = state.omittedObservations
	for _, observation := range input.Observations {
		input.Truncation.FieldsTruncated = input.Truncation.FieldsTruncated || observation.Truncated
	}
}

func (state *execution) recordToolObservations(call Call, callID string, findings []Finding) {
	for _, finding := range findings {
		if len(state.toolObservations) == maxPlannerObservations {
			state.omittedObservations++
			continue
		}
		state.toolObservations = append(state.toolObservations, buildToolObservation(state.round, call, callID, finding))
	}
}

func buildToolObservation(round int, call Call, callID string, finding Finding) ToolObservation {
	summary, clipped := clipPlannerField(finding.Summary, maxPlannerFieldBytes)
	observation := ToolObservation{Round: round, CallID: callID, GapID: call.GapID, ToolID: call.ToolID,
		Relation: finding.Relation, ReasonCode: finding.ReasonCode, Summary: summary,
		ClaimStatus: "tool_reported_not_independently_verified", FactIDs: []string{}, EvidenceIDs: []string{}, Truncated: clipped}
	appendObservationFacts(&observation, finding.Facts)
	appendObservationEvidence(&observation, finding.Citations)
	return observation
}

func appendObservationFacts(observation *ToolObservation, facts []SemanticFact) {
	for _, fact := range facts {
		if len(observation.FactIDs) == maxObservationIDs {
			observation.Truncated = true
			return
		}
		observation.FactIDs = append(observation.FactIDs, fact.ID)
	}
}

func appendObservationEvidence(observation *ToolObservation, citations []Citation) {
	for _, citation := range citations {
		if slices.Contains(observation.EvidenceIDs, citation.EvidenceID) {
			continue
		}
		if len(observation.EvidenceIDs) == maxObservationIDs {
			observation.Truncated = true
			return
		}
		observation.EvidenceIDs = append(observation.EvidenceIDs, citation.EvidenceID)
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
	if canonicalResultNeedsEvidence(status) && !state.canonicalEvidenceAvailable(evaluation.EvidenceIDs) {
		return "unknown", "canonical_evidence_unavailable"
	}
	if status != "completed" && status != "not_applicable" && evaluation.ReasonCode == "" {
		return status, "canonical_result_incomplete"
	}
	return status, evaluation.ReasonCode
}

func canonicalResultNeedsEvidence(status string) bool {
	return status == "completed" || status == "not_applicable"
}

func (state *execution) canonicalEvidenceAvailable(ids []string) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		found := false
		for _, evidence := range state.request.Evidence {
			if evidence.ID == id {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
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
	if !validPriorObservationSummary(request, r.config.MaxCalls) {
		return ErrInput
	}
	if len(request.PriorObservations) == 0 {
		return nil
	}
	gaps := make(map[string]Gap, len(request.Gaps))
	for _, gap := range request.Gaps {
		gaps[gap.ID] = gap
	}
	lastRound := 0
	minimumEvents := 0
	seen := map[string]bool{}
	for _, result := range request.PriorObservations {
		previousRound, eventCount, err := r.validateOnePriorObservation(ctx, request, result, gaps, lastRound, seen)
		if err != nil {
			return err
		}
		lastRound = previousRound
		minimumEvents += eventCount
	}
	if request.PriorEventCount < minimumEvents {
		return ErrInput
	}
	return nil
}

func validPriorObservationSummary(request Request, maxCalls int) bool {
	if len(request.PriorObservations) > maxCalls {
		return false
	}
	if len(request.PriorObservations) == 0 {
		return request.PriorEventCount >= 0 && request.PriorEventCount <= 1024 &&
			(request.PriorElapsedMS == nil || *request.PriorElapsedMS >= 0)
	}
	return request.PriorEventCount > 0 && request.PriorEventCount <= 1024 && request.PriorElapsedMS != nil && *request.PriorElapsedMS >= 0
}

func (r *Runtime) validateOnePriorObservation(ctx context.Context, request Request, result PriorToolResult, gaps map[string]Gap, lastRound int, seen map[string]bool) (int, int, error) {
	if err := ctx.Err(); err != nil {
		return lastRound, 0, err
	}
	gap, exists := gaps[result.GapID]
	if !validPriorObservationIdentity(r, result, gap, exists, lastRound) {
		return lastRound, 0, ErrInput
	}
	call := Call{ToolID: result.ToolID, GapID: result.GapID}
	id := stableCallID(request.RequestID, result.Round, call)
	if seen[id] {
		return lastRound, 0, ErrInput
	}
	seen[id] = true
	events := 2 // tool_started and a terminal event
	if result.Round != lastRound {
		events += 2
	}
	observationEvents, err := validatePriorObservationOutcome(ctx, request, result, gap)
	if err != nil {
		return lastRound, 0, err
	}
	return result.Round, events + observationEvents, nil
}

func validatePriorObservationOutcome(ctx context.Context, request Request, result PriorToolResult, gap Gap) (int, error) {
	if result.FailureCode != "" {
		if !capabilityID.MatchString(result.FailureCode) || len(result.Findings) != 0 {
			return 0, ErrInput
		}
		return 0, nil
	}
	if err := validateFindings(ctx, result.Findings, gap.Domain, gap.Capability, request.Evidence); err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, ErrInput
	}
	return 1, nil // successful calls also emit tool_observed
}

func validPriorObservationIdentity(runtime *Runtime, result PriorToolResult, gap Gap, exists bool, lastRound int) bool {
	spec, registered := runtime.specByID[result.ToolID]
	return exists && registered && result.ToolVersion == spec.Version && spec.Domain == gap.Domain &&
		slices.Contains(spec.Capabilities, gap.Capability) && result.Round >= 1 && result.Round <= runtime.config.MaxRounds &&
		result.Round >= lastRound && result.Round <= lastRound+1
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
