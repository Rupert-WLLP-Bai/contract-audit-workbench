// File metadata (manifest mirror):
// Purpose: Persists attempt-bound Agent step identities, conservative starts and generation-fenced completion or ambiguity through an authorized transaction.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package postgres contains the transaction-bound PostgreSQL adapter for
// execution step checkpoints. It cannot open, commit, or roll back a transaction.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"contractaudit/internal/execution/steps"
	platform "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

const maxStoredPayload = steps.MaxPayloadBytes

// StepCheckpoints uses only the non-owning executor supplied by the authorized
// Store transaction. Callers retain all transaction and authorization control.
type StepCheckpoints struct{ tx platform.Executor }

func NewStepCheckpoints(tx platform.Executor) *StepCheckpoints { return &StepCheckpoints{tx: tx} }

func (store *StepCheckpoints) RecoverySummary(ctx context.Context, scope steps.Scope) (steps.RecoverySummary, error) {
	if store == nil || store.tx == nil {
		return steps.RecoverySummary{}, errors.New("Agent step checkpoint transaction is unavailable")
	}
	if err := scope.Validate(); err != nil {
		return steps.RecoverySummary{}, err
	}
	var summary steps.RecoverySummary
	err := store.tx.QueryRow(ctx, `SELECT COALESCE(sum(elapsed_ms) FILTER (WHERE elapsed_known),0),
			count(*) FILTER (WHERE step_kind='knowledge_read')
		FROM execution_agent_step_checkpoints
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND run_id=$4 AND attempt_id=$5
			AND attempt_input_sha256=$6`,
		scope.TenantID, scope.OrgID, scope.CaseID, scope.RunID, scope.AttemptID, scope.AttemptInputSHA256).
		Scan(&summary.ElapsedMS, &summary.KnowledgeReadCount)
	if err != nil {
		return steps.RecoverySummary{}, err
	}
	if summary.ElapsedMS < 0 || summary.KnowledgeReadCount < 0 {
		return steps.RecoverySummary{}, steps.ErrCorrupt
	}
	return summary, nil
}

// Begin inserts a started record before any physical planner or tool call. A
// previously successful exact identity is returned for revalidation and replay.
// An unresolved started call is moved to ambiguous, never retried in place.
func (store *StepCheckpoints) Begin(ctx context.Context, scope steps.Scope, identity steps.Identity) (steps.Record, bool, error) {
	if store == nil || store.tx == nil {
		return steps.Record{}, false, errors.New("Agent step checkpoint transaction is unavailable")
	}
	if err := scope.Validate(); err != nil {
		return steps.Record{}, false, err
	}
	if err := identity.Validate(); err != nil {
		return steps.Record{}, false, err
	}
	if identity.InputSHA256 == "" || scope.AttemptInputSHA256 == "" {
		return steps.Record{}, false, steps.ErrIdentityMismatch
	}
	tag, err := store.tx.Exec(ctx, `INSERT INTO execution_agent_step_checkpoints
		(tenant_id,org_id,case_id,run_id,attempt_id,step_id,generation,attempt_input_sha256,
		 runtime_version,planner_id,planner_configuration_id,model_configuration_id,tool_catalog_version,tool_catalog_sha256,
		 step_kind,round_number,tool_id,tool_version,tool_configuration_sha256,input_sha256,
			 status,calls_consumed,usage_known,usage_status,elapsed_known)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,
		 'started',1,false,'unknown',false)
		ON CONFLICT (tenant_id,org_id,attempt_id,step_id) DO NOTHING`,
		scope.TenantID, scope.OrgID, scope.CaseID, scope.RunID, scope.AttemptID, identity.StepID, scope.Generation,
		scope.AttemptInputSHA256, identity.RuntimeVersion, identity.PlannerID, identity.PlannerConfigurationID,
		identity.ModelConfigurationID, identity.ToolCatalogVersion, identity.ToolCatalogSHA256,
		identity.Kind, identity.Round, identity.ToolID, identity.ToolVersion, identity.ToolConfigurationSHA256, identity.InputSHA256)
	if err != nil {
		return steps.Record{}, false, err
	}
	created := tag.RowsAffected() == 1
	record, err := store.load(ctx, scope, identity)
	if err != nil {
		return steps.Record{}, false, err
	}
	if created {
		if record.Status != steps.StatusStarted || record.Scope.Generation != scope.Generation {
			return steps.Record{}, false, steps.ErrCorrupt
		}
		return record, true, nil
	}
	if record.Status == steps.StatusStarted {
		reason := "duplicate_start_without_result"
		if record.Scope.Generation < scope.Generation {
			reason = "prior_generation_result_unresolved"
		}
		if record.Scope.Generation > scope.Generation {
			return steps.Record{}, false, steps.ErrIdentityMismatch
		}
		tag, err := store.tx.Exec(ctx, `UPDATE execution_agent_step_checkpoints
			SET status='ambiguous',generation=$8,failure_code=$9,updated_at=clock_timestamp()
			WHERE tenant_id=$1 AND org_id=$2 AND attempt_id=$3 AND step_id=$4
				AND case_id=$5 AND run_id=$6 AND attempt_input_sha256=$7 AND generation=$10 AND status='started'`,
			scope.TenantID, scope.OrgID, scope.AttemptID, identity.StepID, scope.CaseID, scope.RunID,
			scope.AttemptInputSHA256, scope.Generation, reason, record.Scope.Generation)
		if err != nil {
			return steps.Record{}, false, err
		}
		if tag.RowsAffected() != 1 {
			return steps.Record{}, false, steps.ErrIdentityMismatch
		}
		record, err = store.load(ctx, scope, identity)
		if err != nil {
			return steps.Record{}, false, err
		}
	}
	return record, false, nil
}

func (store *StepCheckpoints) Finish(ctx context.Context, scope steps.Scope, identity steps.Identity, outcome steps.Outcome) error {
	if store == nil || store.tx == nil {
		return errors.New("Agent step checkpoint transaction is unavailable")
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	if err := identity.Validate(); err != nil {
		return err
	}
	if err := outcome.Validate(); err != nil {
		return err
	}
	if len(outcome.Payload) > maxStoredPayload {
		return steps.ErrCorrupt
	}
	outputDigest := ""
	var payload any
	if outcome.Status == steps.StatusSucceeded {
		outputDigest = digest(outcome.Payload)
		payload = outcome.Payload
	}
	var inputTokens, outputTokens, totalTokens any
	if outcome.Usage.Status == steps.UsageObserved {
		inputTokens, outputTokens, totalTokens = *outcome.Usage.InputTokens, *outcome.Usage.OutputTokens, *outcome.Usage.TotalTokens
	}
	tag, err := store.tx.Exec(ctx, `UPDATE execution_agent_step_checkpoints SET
		status=$1,output_sha256=$2,output_payload=$3,failure_code=$4,observed_at=$5,elapsed_ms=$6,calls_consumed=$7,
		usage_known=$8,usage_status=$9,input_tokens=$10,output_tokens=$11,total_tokens=$12,elapsed_known=true,updated_at=clock_timestamp()
		WHERE tenant_id=$13 AND org_id=$14 AND case_id=$15 AND run_id=$16 AND attempt_id=$17 AND step_id=$18
			AND generation=$19 AND attempt_input_sha256=$20 AND input_sha256=$21 AND runtime_version=$22 AND planner_id=$23
			AND planner_configuration_id=$24 AND model_configuration_id=$25 AND tool_catalog_version=$26
			AND tool_catalog_sha256=$27 AND step_kind=$28 AND round_number=$29 AND tool_id=$30 AND tool_version=$31
			AND tool_configuration_sha256=$32 AND status='started'`,
		outcome.Status, outputDigest, payload, outcome.FailureCode, outcome.ObservedAt.UTC(), outcome.ElapsedMS, outcome.CallsConsumed,
		outcome.Usage.Status != steps.UsageUnknown, outcome.Usage.Status, inputTokens, outputTokens, totalTokens,
		scope.TenantID, scope.OrgID, scope.CaseID, scope.RunID, scope.AttemptID, identity.StepID, scope.Generation,
		scope.AttemptInputSHA256, identity.InputSHA256, identity.RuntimeVersion, identity.PlannerID,
		identity.PlannerConfigurationID, identity.ModelConfigurationID, identity.ToolCatalogVersion,
		identity.ToolCatalogSHA256, identity.Kind, identity.Round, identity.ToolID, identity.ToolVersion,
		identity.ToolConfigurationSHA256)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return steps.ErrIdentityMismatch
	}
	return nil
}

func (store *StepCheckpoints) load(ctx context.Context, scope steps.Scope, identity steps.Identity) (steps.Record, error) {
	var record steps.Record
	var tenantID, orgID, caseID, runID, attemptID, attemptInputSHA256 string
	var generation int64
	var storedIdentity steps.Identity
	var status, outputDigest, failureCode, usageStatus string
	var payload []byte
	var startedAt time.Time
	var observedAt *time.Time
	var elapsedMS int64
	var elapsedKnown bool
	var callsConsumed int
	var usageKnown bool
	var inputTokens, outputTokens, totalTokens *int64
	err := store.tx.QueryRow(ctx, `SELECT tenant_id,org_id,case_id,run_id,attempt_id,generation,attempt_input_sha256,
		step_id,step_kind,round_number,runtime_version,planner_id,planner_configuration_id,model_configuration_id,
		tool_catalog_version,tool_catalog_sha256,tool_id,tool_version,tool_configuration_sha256,input_sha256,
		status,output_payload,output_sha256,failure_code,started_at,observed_at,elapsed_ms,elapsed_known,calls_consumed,
		usage_known,usage_status,input_tokens,output_tokens,total_tokens
		FROM execution_agent_step_checkpoints
		WHERE tenant_id=$1 AND org_id=$2 AND attempt_id=$3 AND step_id=$4
		FOR UPDATE`, scope.TenantID, scope.OrgID, scope.AttemptID, identity.StepID).Scan(
		&tenantID, &orgID, &caseID, &runID, &attemptID, &generation, &attemptInputSHA256,
		&storedIdentity.StepID, &storedIdentity.Kind, &storedIdentity.Round, &storedIdentity.RuntimeVersion,
		&storedIdentity.PlannerID, &storedIdentity.PlannerConfigurationID, &storedIdentity.ModelConfigurationID,
		&storedIdentity.ToolCatalogVersion, &storedIdentity.ToolCatalogSHA256, &storedIdentity.ToolID,
		&storedIdentity.ToolVersion, &storedIdentity.ToolConfigurationSHA256, &storedIdentity.InputSHA256,
		&status, &payload, &outputDigest, &failureCode, &startedAt, &observedAt, &elapsedMS, &elapsedKnown, &callsConsumed,
		&usageKnown, &usageStatus, &inputTokens, &outputTokens, &totalTokens)
	if errors.Is(err, pgx.ErrNoRows) {
		return steps.Record{}, steps.ErrIdentityMismatch
	}
	if err != nil {
		return steps.Record{}, err
	}
	record.Scope = steps.Scope{TenantID: tenantID, OrgID: orgID, CaseID: caseID, RunID: runID,
		AttemptID: attemptID, Generation: generation, AttemptInputSHA256: attemptInputSHA256}
	record.Identity = storedIdentity
	record.Status = status
	record.Payload = payload
	record.OutputSHA256 = outputDigest
	record.FailureCode = failureCode
	record.StartedAt = startedAt
	record.ObservedAt = observedAt
	record.ElapsedMS = elapsedMS
	record.ElapsedKnown = elapsedKnown
	record.CallsConsumed = callsConsumed
	record.Usage = steps.Usage{Status: usageStatus, InputTokens: inputTokens, OutputTokens: outputTokens, TotalTokens: totalTokens}
	if record.Scope.CaseID != scope.CaseID || record.Scope.RunID != scope.RunID || record.Scope.AttemptID != scope.AttemptID ||
		record.Scope.AttemptInputSHA256 != scope.AttemptInputSHA256 || !sameIdentity(record.Identity, identity) {
		return steps.Record{}, steps.ErrIdentityMismatch
	}
	if usageKnown != (usageStatus != steps.UsageUnknown) {
		return steps.Record{}, steps.ErrCorrupt
	}
	if err := record.Validate(); err != nil {
		return steps.Record{}, errors.Join(steps.ErrCorrupt, err)
	}
	return record, nil
}

func sameIdentity(left, right steps.Identity) bool {
	return left == right
}

func digest(payload []byte) string {
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}
