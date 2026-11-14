// File metadata (manifest mirror):
// Purpose: Adapts authorized frozen attempt evidence, live execution claims, existing step-journal and observer capabilities, and one hash-bound facts projection to the domain facts application.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"encoding/json"
	"errors"

	"contractaudit/internal/agentflow"
	"contractaudit/internal/model"
)

func (s *Store) executeSingleContractFacts(ctx context.Context, runID, attemptID string, docs []Interpreted, models Models, recorder attemptRecorder) (string, error) {
	state, err := s.snapshotForAttemptContext(ctx, attemptID)
	if err != nil {
		return "", err
	}
	snapshot := state.snapshotOf(runID)
	if !isSingleContractSnapshot(snapshot) || snapshot.ReviewScope == nil || snapshot.ReviewScope.ID != ReviewScopeSingleContract {
		return "", nil
	}
	run, ok := state.runByID(runID)
	if !ok {
		return "", errors.New("single-contract facts run is unavailable")
	}
	caseItem, ok := state.caseByID(run.CaseID)
	if !ok || isGovernedReviewCase(caseItem) {
		return "", nil
	}
	attempt, ok := state.attemptByID(attemptID)
	if !ok || attempt.RunID != runID {
		return "", errors.New("single-contract facts attempt is unavailable")
	}
	planner, err := FrozenPlannerConfiguration(attempt)
	if errors.Is(err, ErrFrozenPlannerAbsent) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if planner.ContractFacts == nil {
		return "", nil
	}
	configuration := *planner.ContractFacts
	if err := configuration.Validate(); err != nil {
		return "", err
	}
	claim, durable := s.claimForAttempt(attemptID)
	if !durable || claim.JobID == "" {
		return "", nil
	}
	input, ok := state.attemptInput(attemptID)
	if !ok || input.RunID != runID || input.AttemptID != attemptID || input.InputHash == "" {
		return "", errors.New("single-contract facts frozen input is unavailable")
	}
	source, primaryText, err := frozenContractFactsPrimary(input, docs)
	if err != nil {
		return "", nil
	}
	factsRecorder := recorder
	factsRecorder.runID = runID
	journal := newAgentStepJournal(s, claim, input.InputHash, factsRecorder)
	caller, _ := models.(model.ContractFactsCaller)
	callCtx := model.WithCallObserver(ctx, attemptModelObserver{recorder: factsRecorder})
	projection, err := agentflow.ExtractContractFacts(callCtx, agentflow.ContractFactsInput{
		AttemptID: attemptID, AttemptInputSHA256: input.InputHash,
		PlannerConfigurationID: planner.ConfigurationID, Source: source, Text: primaryText,
	}, configuration, caller, journal)
	if err != nil {
		return "", err
	}
	if err := s.syncContractFactsProjection(ctx, claim, input.InputHash, projection); err != nil {
		return "", errors.Join(err, errAgentReviewPersistence)
	}
	return projection.Status, nil
}

// frozenContractFactsPrimary builds candidates only from the authorized
// interpreted documents and verifies each one against this attempt's frozen
// input before the ContractFacts application selects the unique primary.
func frozenContractFactsPrimary(input AttemptInput, docs []Interpreted) (agentflow.ContractFactsSource, string, error) {
	candidates := make([]agentflow.ContractFactsMaterialCandidate, 0, len(docs))
	for i := range docs {
		item := &docs[i]
		candidate := agentflow.ContractFactsMaterialCandidate{Role: item.Document.Role, Text: item.Text}
		if item.ParsedDocument != nil {
			candidate.Source = agentflow.ContractFactsSource{
				DocumentID: item.Document.ID, ParsedDocumentID: item.ParsedDocument.ID,
				OriginalDigest: item.Document.Digest, NormalizedDigest: item.ParsedDocument.NormalizedDigest,
			}
			candidate.Ready = item.Ready && item.ParsedDocument.Status == "ready" && item.ParsedDocument.ID != "" &&
				item.ParsedDocument.DocumentID == item.Document.ID && item.ParsedDocument.OriginalDigest == item.Document.Digest &&
				item.ParsedDocument.NormalizedDigest != "" && item.Text != "" && item.Text == item.ParsedDocument.Text
		}
		matches := 0
		frozenMatch := false
		for j := range input.Documents {
			frozen := &input.Documents[j]
			if frozen.Document.ID != item.Document.ID || frozen.Document.Role != item.Document.Role {
				continue
			}
			matches++
			frozenMatch = item.ParsedDocument != nil && frozen.Ready && frozen.ParsedDocumentID == item.ParsedDocument.ID &&
				frozen.NormalizedHash != nil && *frozen.NormalizedHash == item.ParsedDocument.NormalizedDigest &&
				frozen.Document.Digest == item.Document.Digest && frozen.Text == item.Text
		}
		candidate.Frozen = matches == 1 && frozenMatch
		candidates = append(candidates, candidate)
	}
	return agentflow.SelectContractFactsPrimary(candidates)
}

func (s *Store) syncContractFactsProjection(ctx context.Context, claim JobClaim, inputHash string, projection agentflow.ContractFactsProjection) error {
	payload, err := json.Marshal(projection)
	if err != nil {
		return err
	}
	record := AgentReviewRecord{
		TenantID: claim.TenantID, OrgID: claim.OrgID, CaseID: claim.CaseID, RunID: claim.RunID,
		AttemptID: claim.AttemptID, Generation: claim.Generation, AttemptInputSHA256: inputHash,
		AgentReviewSHA256: digestBytes(payload), Review: payload,
	}
	return s.SyncAgentReviewRecord(ctx, claim, record)
}
