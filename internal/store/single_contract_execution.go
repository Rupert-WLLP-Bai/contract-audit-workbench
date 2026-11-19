// File metadata (manifest mirror):
// Purpose: Loads the selected frozen source, invokes the published single-contract precheck mapping and persists its evaluation, evidence and suggested findings.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-19
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"errors"

	"contractaudit/internal/engine"
	"contractaudit/internal/execution"
	"contractaudit/internal/execution/steps"
	"contractaudit/internal/model"
)

// executeSingleContractPrecheck uses only the frozen primary contract. Governance
// dependencies are still resolved at admission and revalidated for execution;
// the full governed comparators and model review are explicitly out of scope.
func (s *Store) executeSingleContractPrecheck(parent, ctx context.Context, runID, attemptID string, models Models, recorder attemptRecorder) {
	agentNotice := "智能审查未在此范围执行。"
	factsNotice := ""
	var docs []Interpreted
	var mapping engine.SingleContractMapping
	agentStepFailureCode := ""
	runCtx := execution.WithSpan(ctx, attemptID)
	err := execution.Run(runCtx, recorder,
		execution.Stage{Name: "input_preparation", Run: func(stageCtx context.Context) error {
			state, err := s.snapshotForAttemptContext(stageCtx, attemptID)
			if err != nil {
				return err
			}
			snapshot := state.snapshotOf(runID)
			if !isSingleContractSnapshot(snapshot) {
				return errors.New("frozen precheck mapping is unavailable")
			}
			mapping, err = engine.PublishedSingleContractMapping(snapshot.RuleSetHash)
			if err != nil {
				return err
			}
			if s.telecomAttempt(state, runID) {
				item, _, _, err := s.authorizeTelecomStage(stageCtx, runID, attemptID)
				if err != nil {
					return err
				}
				if isGovernedReviewCase(item) {
					actor, err := s.authorizeAttemptExecution(state, runID, attemptID)
					if err != nil {
						return err
					}
					if snapshot.GovernedKnowledge == nil {
						return errors.New("frozen governed knowledge is unavailable")
					}
					if err := s.ValidateFrozenGovernedKnowledgeSnapshot(stageCtx, actor, item.ID, *snapshot.GovernedKnowledge); err != nil {
						return err
					}
				}
			}
			docs, err = s.prepareAttempt(stageCtx, runID, attemptID, models)
			return err
		}},
		execution.Stage{Name: "single_contract_rules", Run: func(stageCtx context.Context) error {
			var primary *Interpreted
			for i := range docs {
				if docs[i].Document.Role == "draft_contract" {
					if primary != nil {
						return errors.New("frozen primary contract is ambiguous")
					}
					primary = &docs[i]
				}
			}
			if primary == nil {
				return errors.New("frozen primary contract is unavailable")
			}
			stored, err := s.snapshotForAttemptContext(stageCtx, attemptID)
			if err != nil {
				return err
			}
			evaluation, made, err := singleContractRuleOutput(mapping, *primary, runIDCase(stored, runID), runID, attemptID)
			if err != nil {
				return err
			}
			err = s.updateExecutionContext(stageCtx, attemptID, func(next *state) error {
				for _, prior := range next.evaluationsForAttempt(runID, attemptID) {
					if prior.RuleVersion != evaluation.RuleVersion {
						return errors.New("precheck contains an unexpected evaluation")
					}
					return nil
				}
				next.Findings = append(next.Findings, made...)
				next.Evaluations = append(next.Evaluations, evaluation)
				return nil
			})
			if err != nil {
				return err
			}
			if hasVerifiedFinding(made) {
				return s.recordFirstVerifiedFinding(stageCtx, recorder, attemptID)
			}
			return nil
		}},
		execution.Stage{Name: "single_contract_facts", Run: func(stageCtx context.Context) error {
			status, err := s.executeSingleContractFacts(stageCtx, runID, attemptID, docs, models, recorder)
			if err != nil {
				return err
			}
			switch status {
			case "suggested":
				factsNotice = "已生成带原文引用的合同事实建议，仍需人工核对。"
			case "partial":
				factsNotice = "合同事实建议不完整或未能完成，未知项仍保留为未知。"
			}
			return nil
		}},
		execution.Stage{Name: "agent_review", Run: func(stageCtx context.Context) error {
			if !mapping.SupportsAgentReview() {
				return recordTelecomAgentReviewResult(stageCtx, recorder, "skipped", "agent_review_unavailable")
			}
			stored, err := s.snapshotForAttemptContext(stageCtx, attemptID)
			if err != nil {
				return err
			}
			run, ok := stored.runByID(runID)
			if !ok {
				return errors.New("precheck run is unavailable")
			}
			item, ok := stored.caseByID(run.CaseID)
			if !ok {
				return errors.New("precheck case is unavailable")
			}
			claim, durable := s.claimForAttempt(attemptID)
			if !isGovernedReviewCase(item) || !durable || claim.JobID == "" {
				return recordTelecomAgentReviewResult(stageCtx, recorder, "skipped", "agent_review_unavailable")
			}
			if _, done := telecomStageTerminal(stored.executionEvents(attemptID), "agent_review"); done {
				status, _ := telecomAgentReviewResult(stored.executionEvents(attemptID))
				if status == "completed" {
					agentNotice = "智能审查仅限已声明的付款比例能力。"
				} else {
					agentNotice = "智能审查未完成，已保留确定性检查结果。"
				}
				return nil
			}
			agentRecorder := recorder
			agentRecorder.runID = runID
			stageCtx = model.WithCallObserver(stageCtx, attemptModelObserver{recorder: agentRecorder})
			incomplete, err := s.runTelecomAgentReview(stageCtx, runID, attemptID, docs, models, agentRecorder)
			if err != nil {
				if isFatalAgentReviewBoundaryError(err) || stageCtx.Err() != nil || agentRecorder.err() != nil {
					return err
				}
				if errors.Is(err, steps.ErrAmbiguous) || errors.Is(err, steps.ErrPriorFailure) {
					agentStepFailureCode = "agent_step_failed"
					if errors.Is(err, steps.ErrAmbiguous) {
						agentStepFailureCode = "agent_step_ambiguous"
					}
					agentNotice = "智能审查步骤未能安全恢复，已保留确定性检查结果。"
					return recordTelecomAgentReviewResult(stageCtx, agentRecorder, "partial", agentStepFailureCode)
				}
				agentNotice = "智能审查未完成，已保留确定性检查结果。"
				return recordTelecomAgentReviewResult(stageCtx, agentRecorder, "partial", "agent_review_failed")
			}
			if incomplete {
				agentNotice = "智能审查未完成，已保留确定性检查结果。"
				return recordTelecomAgentReviewResult(stageCtx, agentRecorder, "partial", "agent_review_incomplete")
			}
			agentNotice = "智能审查仅限已声明的付款比例能力。"
			return recordTelecomAgentReviewResult(stageCtx, agentRecorder, "completed", "")
		}},
	)
	if parent.Err() != nil {
		return
	}
	if err != nil {
		code := "execution_failed"
		if errors.Is(err, errAttemptAuthorizationRevoked) {
			code = "authorization_revoked"
		}
		if recorder.err() != nil {
			code = "execution_persistence_failed"
		}
		_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "failed", "", code)
		return
	}
	terminalCode := "precheck_scope_limited"
	if agentStepFailureCode != "" {
		terminalCode = agentStepFailureCode
	}
	_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "partial", mapping.ScopeNotice()+"跨材料比对、正式制度审查和其他风险尚未执行。请补齐材料后发起完整审查。"+factsNotice+agentNotice, terminalCode)
}

func singleContractRuleOutput(mapping engine.SingleContractMapping, primary Interpreted, caseID, runID, attemptID string) (ConstraintEval, []Finding, error) {
	id, err := newID("evl_")
	if err != nil {
		return ConstraintEval{}, nil, err
	}
	docs := []Interpreted{primary}
	result := mapping.Evaluate(sourcesOf(docs)[0])
	evaluation := ConstraintEval{ID: id, RunID: runID, AttemptID: attemptID, CaseID: caseID,
		Domain: result.Domain, RuleID: result.RuleID, RuleVersion: result.RuleID, Relation: result.Relation,
		CapabilityID: result.CapabilityID, ReasonCode: result.ReasonCode, Reason: result.Reason, FactIDs: result.FactIDs}
	var made []Finding
	if len(result.Findings) > 0 {
		made, err = materialize(caseID, runID, attemptID, stamp(), result.Findings, docs)
		if err != nil {
			return ConstraintEval{}, nil, err
		}
		evaluation.FindingID = made[0].ID
	}
	if len(result.Coverage) == 0 {
		return evaluation, made, nil
	}
	parsedID, digest := parsedIdentity(docs, primary.Document.ID)
	if parsedID == "" || digest == "" {
		return ConstraintEval{}, nil, errors.New("payment terms have no frozen parse identity")
	}
	for _, anchor := range result.Coverage {
		ref := ExperienceEvidenceRef{
			DocumentID: anchor.DocumentID, ParsedDocumentID: parsedID, NormalizedDigest: digest,
			Quote: anchor.Quote, Start: anchor.Start, End: anchor.End,
		}
		for _, kind := range anchor.TermKinds {
			evaluation.Evidence = append(evaluation.Evidence, EvaluationEvidence{FactID: kind, Ref: ref})
		}
	}
	return evaluation, made, nil
}
