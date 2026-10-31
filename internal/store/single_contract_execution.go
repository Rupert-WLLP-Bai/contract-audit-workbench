package store

import (
	"context"
	"errors"

	"contractaudit/internal/engine"
	"contractaudit/internal/execution"
	"contractaudit/internal/model"
)

// executeSingleContractPrecheck uses only the frozen primary contract. Governance
// dependencies are still resolved at admission and revalidated for execution;
// the full governed comparators and model review are explicitly out of scope.
func (s *Store) executeSingleContractPrecheck(parent, ctx context.Context, runID, attemptID string, models Models, recorder attemptRecorder) {
	agentNotice := "智能审查未在此范围执行。"
	var docs []Interpreted
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
			if _, err := engine.PublishedSingleContractMapping(snapshot.RuleSetHash); err != nil {
				return err
			}
			if s.telecomAttempt(state, runID) {
				item, _, _, err := s.authorizeTelecomStage(stageCtx, runID, attemptID)
				if err != nil {
					return err
				}
				if isGovernedReviewCase(item) {
					actor, err := authorizeAttemptExecution(state, runID, attemptID)
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
			source := sourcesOf([]Interpreted{*primary})
			evaluation := engine.EvaluateSingleContractPaymentSum(source[0])
			stored, err := s.snapshotForAttemptContext(stageCtx, attemptID)
			if err != nil {
				return err
			}
			made, err := materialize(runIDCase(stored, runID), runID, attemptID, stamp(), engine.Findings(evaluation), []Interpreted{*primary})
			if err != nil {
				return err
			}
			evaluationID, err := newID("evl_")
			if err != nil {
				return err
			}
			findingID := ""
			if len(made) > 0 {
				findingID = made[0].ID
			}
			err = s.updateExecutionContext(stageCtx, attemptID, func(next *state) error {
				for _, prior := range next.evaluationsForAttempt(runID, attemptID) {
					if prior.RuleVersion != engine.SingleContractPrecheckRule {
						return errors.New("precheck contains an unexpected evaluation")
					}
					return nil
				}
				next.Findings = append(next.Findings, made...)
				next.Evaluations = append(next.Evaluations, ConstraintEval{ID: evaluationID, RunID: runID, AttemptID: attemptID,
					CaseID: runIDCase(*next, runID), Domain: evaluation.Domain, Relation: evaluation.Relation,
					RuleVersion: evaluation.RuleVersion, Reason: evaluation.Reason, FindingID: findingID})
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
		execution.Stage{Name: "agent_review", Run: func(stageCtx context.Context) error {
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
				if errors.Is(err, errAttemptAuthorizationRevoked) || errors.Is(err, errAgentReviewPersistence) || errors.Is(err, errAgentReviewScopeChanged) || stageCtx.Err() != nil || recorder.err() != nil {
					return err
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
	_ = s.finishAttemptAfterExecution(parent, ctx, runID, attemptID, "partial", "单合同预审仅核对已识别的付款比例；跨材料比对、正式制度审查和其他风险尚未执行。请补齐材料后发起完整审查。"+agentNotice, "precheck_scope_limited")
}
