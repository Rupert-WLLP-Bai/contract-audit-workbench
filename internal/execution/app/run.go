package app

import (
	"context"
	"errors"
	"time"

	"contractaudit/internal/execution"
	"contractaudit/internal/model"
)

// StandardPorts implements only the authorised attempt operations needed here.
type StandardPorts interface {
	InputPort
	RuleResultsPort
	CitationPort
	ProgressPort
	OutcomePort
	RuleAttempt(context.Context, FrozenAttempt) (FrozenAttempt, error)
	RecordFirstVerifiedFinding(context.Context, FrozenAttempt, bool) error
	RecorderError() error
}

// RunStandard orchestrates the existing four-stage review. The caller owns
// timeout, scope, model observer and terminal persistence contexts.
func RunStandard(parent, ctx context.Context, attempt FrozenAttempt, modelClient WarrantyModel, missingModel error, cleanupTimeout time.Duration, ports StandardPorts, recorder EventPort, ids IDs, clock Clock) {
	var input PreparedInput
	var proposal model.Warranty
	var modelErr error
	rulesPublished := false
	pipelineErr := execution.Run(ctx, recorder,
		execution.Stage{Name: "input_preparation", Run: func(stageCtx context.Context) error {
			var err error
			input, err = ports.PrepareFrozenInput(stageCtx, attempt)
			return err
		}},
		execution.Stage{Name: "deterministic_rules", Run: func(stageCtx context.Context) error {
			if len(input.Documents) == 0 {
				return errors.New("frozen input is unavailable")
			}
			identity, err := ports.RuleAttempt(stageCtx, attempt)
			if err != nil {
				return err
			}
			results, err := PlanRuleResults(identity, input.Documents, ids, clock)
			if err != nil {
				return err
			}
			if err = ports.AppendRuleResults(stageCtx, identity, results); err != nil {
				return err
			}
			if HasVerifiedFinding(results.Findings) {
				if err = ports.RecordFirstVerifiedFinding(stageCtx, attempt, true); err != nil {
					return err
				}
			}
			rulesPublished = true
			return nil
		}},
		execution.Stage{Name: "model_review", Run: func(stageCtx context.Context) error {
			if ReadyCount(input.Documents) < 2 {
				modelErr = errors.New("insufficient_ready_documents")
				return modelErr
			}
			if modelClient == nil {
				modelErr = missingModel
				return modelErr
			}
			proposal, modelErr = modelClient.Warranty(stageCtx, Materials(input.Documents))
			if err := ports.RecorderError(); err != nil {
				return err
			}
			return modelErr
		}},
		execution.Stage{Name: "citation_verification", Run: func(stageCtx context.Context) error {
			drafts, err := AcceptWarranty(input.Documents, proposal)
			if err != nil {
				modelErr = err
				return err
			}
			delta, err := ports.MergeCitationDrafts(stageCtx, attempt, input.Documents, drafts)
			if err != nil {
				return err
			}
			if HasVerifiedFinding(delta.Added) {
				return ports.RecordFirstVerifiedFinding(stageCtx, attempt, false)
			}
			return nil
		}},
	)
	if parent.Err() != nil {
		return
	}
	if !rulesPublished || pipelineErr != nil {
		readCtx := ctx
		cancel := func() {}
		if ctx.Err() != nil {
			readCtx, cancel = context.WithTimeout(parent, cleanupTimeout)
		}
		progress, err := ports.ReadAttemptProgress(readCtx, attempt)
		cancel()
		if err != nil {
			_ = ports.FinishAttempt(ctx, attempt, Outcome{State: "failed", ErrorCode: "execution_persistence_failed"})
			return
		}
		if !rulesPublished {
			rulesPublished = progress.RulesPublished
		}
		if pipelineErr != nil && !progress.Active {
			return
		}
	}
	outcome := ClassifyOutcome(OutcomeInput{PipelineError: pipelineErr, RecorderError: ports.RecorderError(), ModelError: modelErr, ModelNotConfigured: missingModel, RulesPublished: rulesPublished, ReadyCount: ReadyCount(input.Documents), DocumentCount: len(input.Documents)})
	_ = ports.FinishAttempt(ctx, attempt, outcome)
}
