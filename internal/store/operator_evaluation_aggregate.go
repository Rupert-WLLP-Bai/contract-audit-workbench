// File metadata (manifest mirror):
// Purpose: Retains original aggregate transaction, current Case authority and frozen fact projection while invoking the actual Operator application.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-14
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"errors"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/auditext"
	"contractaudit/internal/capabilitypack"
	"contractaudit/internal/fault"
	operatorApp "contractaudit/internal/operator/app"
	"github.com/jackc/pgx/v5"
)

// createAggregateOperatorFulfillmentEvaluation is the explicit aggregate path.
// All object identities and evidence are read under the same case transaction;
// it never falls through to the legacy single-binding evaluator.
func (s *Store) createAggregateOperatorFulfillmentEvaluation(ctx context.Context, user, current accounts.User, st state,
	caseItem Case, input OperatorEvaluationInput, scope OperatorEvaluationScope, pack capabilitypack.Manifest,
	evaluator auditext.Evaluator, key string) (OperatorEvaluation, error) {
	facts := outerAggregateFacts{outerEvaluationFacts: outerEvaluationFacts{operatorPreviewFacts{store: s, state: st, item: caseItem}}}
	attempt, rulePackVersion, requestSHA, err := operatorApp.PrepareAggregateProof[Document](ctx, facts, mappingScope(caseItem), input, pack)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	var out OperatorEvaluation
	err = s.withOperatorTx(ctx, user, caseItem.ID, true, func(tx pgx.Tx) error {
		existing, err := loadOperatorEvaluationByKey(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseItem.ID, current.ID, key)
		if err == nil {
			out, err = operatorApp.CheckAggregateEvaluationReplay(existing, requestSHA)
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		fresh, err := loadState(ctx, tx, false)
		if err != nil {
			return err
		}
		freshCase, err := s.visibleCase(&fresh, current, caseItem.ID)
		if err != nil || !authorizeCase(freshCase, current, access.Review) {
			return fault.Forbidden("当前授权不能核验该案件")
		}
		if err := businessWritable(freshCase); err != nil {
			return err
		}
		proofFacts := operatorPreviewFacts{store: s, tx: tx, state: fresh, item: freshCase}
		freshInput, err := operatorApp.ValidateFreshAggregate[Document](ctx, proofFacts, mappingScope(freshCase), input, pack, attempt)
		if err != nil {
			return err
		}
		runSnapshot := fresh.snapshotOf(input.SourceReviewID)
		id := operatorApp.CreateIdentity{TenantID: accounts.Tenant(current), OrgID: freshCase.OwnerOrgID, CaseID: caseItem.ID, ActorID: current.ID}
		config := operatorApp.AggregateConfiguration{Model: runSnapshot.ModelConfigurationID, Planner: runSnapshot.PlannerConfigurationID, Parser: runSnapshot.ParserConfigurationID}
		out, err = operatorApp.RunAggregateAfterProof[Document](ctx, newEvaluationAggregateRepo(tx), aggregateEvidenceFacts{state: fresh}, id, mappingScope(freshCase), operatorGrantVersion(freshCase, current), freshInput.Documents, fresh.Evaluations, config, input, scope, auditext.PackRef{ID: pack.PackID, Version: pack.Version}, rulePackVersion, evaluator, key, requestSHA, evaluationClock{})
		return err
	})
	return out, err
}
