// File metadata (manifest mirror):
// Purpose: OperatorContractObject is the human-selected contract side of a mapping. Related `store` operations include normalizedOperatorEvaluationScope, operatorReady, operatorCaseState, operatorGrantVersion.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-29
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/auditext"
	"contractaudit/internal/capabilitypack"
	"contractaudit/internal/fault"
	operatorApp "contractaudit/internal/operator/app"
	operatorModel "contractaudit/internal/operator/model"
	operatorPostgres "contractaudit/internal/operator/postgres"
	platformpostgres "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

type OperatorContractObject = operatorModel.OperatorContractObject

// OperatorExternalObject is the deployment-registered external side of a
// mapping. ConnectorID is an identifier, never a URL or credential.
type OperatorExternalObject = operatorModel.OperatorExternalObject

type OperatorObjectBindingInput = operatorModel.OperatorObjectBindingInput
type OperatorObjectBinding = operatorModel.OperatorObjectBinding
type operatorObjectBindingRecord = operatorModel.OperatorObjectBindingRecord

// Operator snapshot and job values belong to the Operator model.
type OperatorSnapshotSet = operatorModel.OperatorSnapshotSet
type OperatorCollectionJob = operatorModel.OperatorCollectionJob

// OperatorEvaluationInput is the frozen identity accepted for one deterministic
// evaluation. The server resolves every referenced version; none is trusted
// from another tenant.
type OperatorEvaluationInput = operatorModel.OperatorEvaluationInput
type OperatorEvaluationScope = operatorModel.OperatorEvaluationScope

// OperatorEvaluation is the persisted deterministic result plus its frozen
// input. Human decisions remain in the existing decision flow.
type OperatorEvaluation = operatorModel.OperatorEvaluation
type operatorEvaluationRecord = operatorModel.EvaluationRecord

type OperatorEvaluationCoverage = operatorModel.OperatorEvaluationCoverage
type OperatorEvaluationObject = operatorModel.OperatorEvaluationObject
type OperatorEvaluationSource = operatorModel.OperatorEvaluationSource

func (s *Store) operatorReady(write bool) error {
	if s.pool == nil {
		return fault.New(503, "operator_audit_unavailable", "跨系统核验需要 PostgreSQL 持久化")
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return fault.New(503, "operator_audit_unavailable", "当前服务不拥有跨系统核验写入")
	}
	if write && s.ReadOnly() {
		return fault.New(503, "read_only", "只读模式下不能执行跨系统核验")
	}
	return nil
}

// operatorCaseState resolves the current trusted user, case snapshot and
// authorization. It is called before a read and again before final persistence.
func (s *Store) operatorCaseState(ctx context.Context, user accounts.User, caseID string, action access.Action) (accounts.User, state, Case, access.Principal, access.Resource, error) {
	ctx = commandContext(ctx, user)
	current, err := trustedUser(user)
	if err != nil {
		return accounts.User{}, state{}, Case{}, access.Principal{}, access.Resource{}, err
	}
	st, err := s.snapshotScoped(ctx, userScope(current))
	if err != nil {
		return accounts.User{}, state{}, Case{}, access.Principal{}, access.Resource{}, err
	}
	item, err := s.visibleCase(&st, current, caseID)
	if err != nil {
		return accounts.User{}, state{}, Case{}, access.Principal{}, access.Resource{}, err
	}
	if !authorizeCase(item, current, action) {
		return accounts.User{}, state{}, Case{}, access.Principal{}, access.Resource{}, fault.Forbidden("当前授权不能执行此案件操作")
	}
	principal := bindLocalDemoCaseGrant(item, current, accounts.Principal(current))
	resource := access.Resource{TenantID: item.TenantID, CaseID: item.ID, OrgID: item.OwnerOrgID, OwnerID: item.OwnerID}
	return current, st, item, principal, resource, nil
}

// operatorGrantVersion changes when the case membership or role grant changes.
// It is recorded in the frozen evaluation and lets the impact planner deny a
// revoked replay instead of falling back to a full read.
func operatorGrantVersion(item Case, user accounts.User) string {
	members := append([]string(nil), item.MemberIDs...)
	sort.Strings(members)
	h := sha256.New()
	for _, part := range []string{
		access.PolicyVersion, item.TenantID, item.OwnerOrgID, item.ID,
		user.ID, user.Role, strings.Join(members, "\x00"),
	} {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Store) withOperatorTx(ctx context.Context, user accounts.User, caseID string, write bool, fn func(pgx.Tx) error) error {
	ctx = commandContext(ctx, user)
	if err := s.operatorReady(write); err != nil {
		return err
	}
	current, err := trustedUser(user)
	if err != nil {
		return err
	}
	scope := userScope(current)
	scope.CaseID = caseID
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if write {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(), 0))`); err != nil {
			return err
		}
		if err := scopedCaseLock(ctx, tx, caseID); err != nil {
			return err
		}
	}
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CreateOperatorObjectBinding records one explicit human mapping. The binding
// version is the binding id, so every version is unique and immutable.
func (s *Store) CreateOperatorObjectBinding(ctx context.Context, user accounts.User, caseID string, input OperatorObjectBindingInput, idempotencyKey string) (OperatorObjectBinding, error) {
	ctx = commandContext(ctx, user)
	key := strings.TrimSpace(idempotencyKey)
	if key == "" {
		return OperatorObjectBinding{}, fault.Invalid("缺少幂等键")
	}
	current, _, item, _, _, err := s.operatorCaseState(ctx, user, caseID, access.Manage)
	if err != nil {
		return OperatorObjectBinding{}, err
	}
	if err := operatorApp.ValidateCollectionScenario(item.Scenario); err != nil {
		return OperatorObjectBinding{}, err
	}
	id := operatorApp.CreateIdentity{TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: caseID, ActorID: current.ID}
	prepared, err := operatorApp.PrepareBinding(id, input, key, operatorBindingClock{})
	if err != nil {
		return OperatorObjectBinding{}, err
	}
	var out OperatorObjectBinding
	err = s.withOperatorTx(ctx, user, caseID, true, func(tx pgx.Tx) error {
		var inner error
		out, inner = operatorApp.ApplyBinding(ctx, operatorPostgres.NewBinding(platformpostgres.Bind(tx)), operatorBindingClock{}, prepared)
		return inner
	})
	return out, err
}

type operatorBindingClock struct{}

func (operatorBindingClock) Now() time.Time                { return time.Now() }
func (operatorBindingClock) NewBindingID() (string, error) { return newID("bind_") }
func (operatorBindingClock) NewMappingID() (string, error) { return newID("omap_") }

// CollectOperatorExternalSnapshot performs one bounded read and persists the
// normalized observation as an immutable snapshot set. Collection is separate
// from evaluation acceptance: a failed read is not a conclusion.
func (s *Store) CollectOperatorExternalSnapshot(ctx context.Context, user accounts.User, caseID, bindingVersion string, businessAt time.Time, connectorID, externalObjectID string, connector auditext.ReadConnector, idempotencyKey string) (OperatorSnapshotSet, error) {
	ctx = commandContext(ctx, user)
	key, businessAt, err := operatorApp.PrepareCollectionInput(idempotencyKey, businessAt)
	if err != nil {
		return OperatorSnapshotSet{}, err
	}
	current, _, item, _, _, err := s.operatorCaseState(ctx, user, caseID, access.Review)
	if err != nil {
		return OperatorSnapshotSet{}, err
	}
	if err := operatorApp.ValidateCollectionScenario(item.Scenario); err != nil {
		return OperatorSnapshotSet{}, err
	}
	binding, err := s.operatorReadBinding(ctx, user, caseID, bindingVersion)
	if err != nil {
		return OperatorSnapshotSet{}, err
	}
	if err := operatorApp.ValidateCollectionBinding(binding, businessAt, connectorID, externalObjectID); err != nil {
		return OperatorSnapshotSet{}, err
	}
	requestSHA, err := operatorApp.CollectionDigest(item.TenantID, item.OwnerOrgID, caseID, current.ID, binding, businessAt)
	if err != nil {
		return OperatorSnapshotSet{}, err
	}
	leaseOwner, err := newID("collect_")
	if err != nil {
		return OperatorSnapshotSet{}, err
	}
	collectionID, err := newID("col_")
	if err != nil {
		return OperatorSnapshotSet{}, err
	}
	var generation int64
	var out OperatorSnapshotSet
	var alreadyComplete bool
	err = s.withOperatorTx(ctx, user, caseID, true, func(tx pgx.Tx) error {
		freshState, err := loadState(ctx, tx, true)
		if err != nil {
			return err
		}
		freshCase, err := s.visibleCase(&freshState, current, caseID)
		if err != nil {
			return err
		}
		freshActor, err := trustedUser(user)
		if err != nil {
			return err
		}
		if !authorizeCase(freshCase, freshActor, access.Review) {
			return fault.Forbidden("当前授权不能读取此案件的外部证据")
		}
		current, item = freshActor, freshCase
		identity := operatorModel.CollectionIdentity{TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: caseID, ActorID: current.ID, Key: key}
		request := operatorApp.CollectionRequest{Identity: identity, Binding: binding, BusinessAt: businessAt, RequestSHA: requestSHA, CollectionID: collectionID, Owner: leaseOwner, ConnectorAvailable: connector != nil}
		acquired, err := operatorApp.AcquireCollection(ctx, operatorPostgres.NewCollection(platformpostgres.Bind(tx)), collectionWritable{item: item}, request, mappingClock{})
		out, alreadyComplete = acquired.Snapshot, acquired.Replay
		generation, collectionID = acquired.Token.Generation, acquired.Token.CollectionID
		return err
	})
	if err != nil {
		return OperatorSnapshotSet{}, err
	}
	if alreadyComplete {
		return out, nil
	}
	scope := auditext.AuthorizedScope{
		TenantID: item.TenantID, CaseID: caseID, ActorID: current.ID,
		GrantVersion: operatorGrantVersion(item, current),
	}
	observation, release, err := operatorApp.ReadCollectionExternal(ctx, connector, scope, binding, businessAt)
	if err != nil {
		if release {
			_ = s.releaseOperatorCollectionLease(ctx, user, item.TenantID, item.OwnerOrgID, caseID, current.ID, key, generation, leaseOwner)
		}
		return OperatorSnapshotSet{}, err
	}
	// Recheck authorization after the potentially slow read, before persistence.
	currentAfter, _, itemAfter, _, _, err := s.operatorCaseState(ctx, user, caseID, access.Review)
	if err != nil {
		return OperatorSnapshotSet{}, err
	}
	if operatorGrantVersion(itemAfter, currentAfter) != scope.GrantVersion {
		return OperatorSnapshotSet{}, fault.Forbidden("采集期间案件授权已变化")
	}

	snapshotSetID, err := newID("snapset_")
	if err != nil {
		return OperatorSnapshotSet{}, err
	}
	prepared, err := operatorApp.PrepareObservation(observation, requestSHA)
	if err != nil {
		return OperatorSnapshotSet{}, err
	}
	token := operatorModel.CollectionLeaseToken{Identity: operatorModel.CollectionIdentity{TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: caseID, ActorID: current.ID, Key: key}, CollectionID: collectionID, RequestSHA256: requestSHA, Owner: leaseOwner, Generation: generation}
	err = s.withOperatorTx(ctx, user, caseID, true, func(tx pgx.Tx) error {
		repo := operatorPostgres.NewCollection(platformpostgres.Bind(tx))
		if err := operatorApp.LockCollectionCompletion(ctx, repo, token); err != nil {
			return err
		}
		freshState, err := loadState(ctx, tx, true)
		if err != nil {
			return err
		}
		freshCase, err := s.visibleCase(&freshState, current, caseID)
		if err != nil {
			return err
		}
		freshActor, err := trustedUser(user)
		if err != nil {
			return err
		}
		if err := businessWritable(freshCase); err != nil {
			return err
		}
		if !authorizeCase(freshCase, freshActor, access.Review) {
			return fault.Forbidden("采集期间案件授权已变化")
		}
		freshGrantVersion := operatorGrantVersion(freshCase, freshActor)
		if freshGrantVersion != scope.GrantVersion {
			return fault.Forbidden("采集期间案件授权已变化")
		}
		out, err = operatorApp.CompleteCollectionAfterLock(ctx, repo, token, binding, businessAt, snapshotSetID, prepared, mappingClock{})
		return err
	})
	return out, err
}

func (s *Store) releaseOperatorCollectionLease(ctx context.Context, user accounts.User, tenantID, orgID, caseID, actorID, key string, generation int64, owner string) error {
	ctx = commandContext(ctx, user)
	return s.withOperatorTx(ctx, user, caseID, true, func(tx pgx.Tx) error {
		token := operatorModel.CollectionLeaseToken{Identity: operatorModel.CollectionIdentity{TenantID: tenantID, OrgID: orgID, CaseID: caseID, ActorID: actorID, Key: key}, Generation: generation, Owner: owner}
		return operatorPostgres.NewCollection(platformpostgres.Bind(tx)).ReleaseClaim(ctx, token)
	})
}

func (s *Store) operatorReadBinding(ctx context.Context, user accounts.User, caseID, bindingVersion string) (OperatorObjectBinding, error) {
	ctx = commandContext(ctx, user)
	var out OperatorObjectBinding
	err := s.withOperatorTx(ctx, user, caseID, false, func(tx pgx.Tx) error {
		var err error
		out, err = operatorApp.ReadRawCollectionBinding(ctx, operatorPostgres.NewCollection(platformpostgres.Bind(tx)), caseID, bindingVersion)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.NotFound("没有找到对象映射")
	}
	return out, err
}

func (s *Store) ListOperatorExternalSnapshots(ctx context.Context, user accounts.User, caseID string) ([]OperatorSnapshotSet, error) {
	ctx = commandContext(ctx, user)
	if err := s.operatorReady(false); err != nil {
		return nil, err
	}
	if _, _, _, _, _, err := s.operatorCaseState(ctx, user, caseID, access.Read); err != nil {
		return nil, err
	}
	var out []OperatorSnapshotSet
	err := s.withOperatorTx(ctx, user, caseID, false, func(tx pgx.Tx) error {
		var err error
		out, err = operatorApp.ListCollectionSnapshots(ctx, operatorPostgres.NewCollection(platformpostgres.Bind(tx)), caseID)
		return err
	})
	if out == nil {
		out = []OperatorSnapshotSet{}
	}
	return out, err
}

// CreateOperatorFulfillmentEvaluation accepts a complete frozen input, runs the
// deterministic evaluator and persists the result. It never uses a model output
// as a conclusion and never appends material to an old review attempt.
func (s *Store) CreateOperatorFulfillmentEvaluation(ctx context.Context, user accounts.User, caseID string, input OperatorEvaluationInput, pack capabilitypack.Manifest, evaluator auditext.Evaluator, idempotencyKey string) (OperatorEvaluation, error) {
	ctx = commandContext(ctx, user)
	key, input, scope, err := operatorApp.PrepareEvaluationRequest(idempotencyKey, input, pack, evaluator, evaluationClock{})
	if err != nil {
		return OperatorEvaluation{}, err
	}
	current, st, item, _, _, err := s.operatorCaseState(ctx, user, caseID, access.Review)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	if err := operatorApp.ValidateEvaluationScenario(item.Scenario, pack); err != nil {
		return OperatorEvaluation{}, err
	}
	requestDigest, err := operatorApp.EvaluationRequestSHA(input)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	var replay operatorEvaluationRecord
	err = s.withOperatorTx(ctx, user, caseID, false, func(tx pgx.Tx) error {
		var loadErr error
		replay, loadErr = loadOperatorEvaluationByKey(ctx, tx, item.TenantID, item.OwnerOrgID, caseID, current.ID, key)
		return loadErr
	})
	if err == nil {
		return operatorApp.CheckInitialEvaluationReplay(replay, requestDigest)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return OperatorEvaluation{}, err
	}
	if operatorApp.UseAggregateEvaluation(scope, input) {
		return s.createAggregateOperatorFulfillmentEvaluation(ctx, user, current, st, item, input, scope, pack, evaluator, key)
	}
	id := operatorApp.CreateIdentity{TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: caseID, ActorID: current.ID}
	facts := singleEvaluationFacts{outerEvaluationFacts: outerEvaluationFacts{operatorPreviewFacts{store: s, state: st, item: item}}, user: user, caseID: caseID}
	write, err := operatorApp.PrepareSingleFlow[Document](ctx, facts, id, mappingScope(item), operatorGrantVersion(item, current), input, scope, pack, evaluator, key, evaluationClock{})
	if err != nil {
		return OperatorEvaluation{}, err
	}
	requestSHA, resultSHA := write.Prepared.RequestSHA, write.Prepared.ResultSHA
	var out OperatorEvaluation
	err = s.withOperatorTx(ctx, user, caseID, true, func(tx pgx.Tx) error {
		existing, err := loadOperatorEvaluationByKey(ctx, tx, item.TenantID, item.OwnerOrgID, caseID, current.ID, key)
		if err == nil {
			out, err = operatorApp.CheckSingleEvaluationReplay(existing, requestSHA, resultSHA)
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		repo := operatorPostgres.NewEvaluation(platformpostgres.Bind(tx))

		out, err = operatorApp.WriteSingleAfterReplay(ctx, repo, write, evaluationClock{})
		return err
	})
	return out, err
}

func (s *Store) GetOperatorFulfillmentEvaluation(ctx context.Context, user accounts.User, caseID, evaluationID string) (OperatorEvaluation, error) {
	ctx = commandContext(ctx, user)
	if err := s.operatorReady(false); err != nil {
		return OperatorEvaluation{}, err
	}
	if _, _, _, _, _, err := s.operatorCaseState(ctx, user, caseID, access.Read); err != nil {
		return OperatorEvaluation{}, err
	}
	var out OperatorEvaluation
	err := s.withOperatorTx(ctx, user, caseID, false, func(tx pgx.Tx) error {
		var err error
		out, err = loadOperatorEvaluation(ctx, tx, caseID, evaluationID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.NotFound("没有找到履约核验记录")
	}
	return out, err
}

func (s *Store) GetOperatorImpactPlan(ctx context.Context, user accounts.User, caseID, evaluationID string, mode auditext.ImpactMode, planner auditext.ImpactPlanner) (auditext.ImpactPlan, error) {
	ctx = commandContext(ctx, user)
	if err := s.operatorReady(false); err != nil {
		return auditext.ImpactPlan{}, err
	}
	if !mode.Valid() {
		return auditext.ImpactPlan{}, fault.Invalid("影响计划模式不合法")
	}
	if planner == nil {
		return auditext.ImpactPlan{}, fault.New(503, "impact_planner_unavailable", "影响计划器未配置")
	}
	if _, _, _, _, _, err := s.operatorCaseState(ctx, user, caseID, access.Read); err != nil {
		return auditext.ImpactPlan{}, err
	}
	var target, base OperatorEvaluation
	var baseFound bool
	err := s.withOperatorTx(ctx, user, caseID, false, func(tx pgx.Tx) error {
		var err error
		target, err = loadOperatorEvaluation(ctx, tx, caseID, evaluationID)
		if err != nil {
			return err
		}
		base, err = loadOperatorPreviousEvaluation(ctx, tx, caseID, target)
		if err == nil {
			baseFound = true
			return nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return auditext.ImpactPlan{}, fault.NotFound("没有找到履约核验记录")
	}
	if err != nil {
		return auditext.ImpactPlan{}, err
	}
	plan, err := operatorApp.PlanImpact(ctx, planner, mode, operatorApp.ImpactEvaluation{ID: target.EvaluationID, Input: target.Input, Checks: target.Checks}, operatorApp.ImpactEvaluation{ID: base.EvaluationID, Input: base.Input, Checks: base.Checks}, baseFound)
	if err != nil {
		return auditext.ImpactPlan{}, err
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return auditext.ImpactPlan{}, err
	}
	sum := sha256.Sum256(planJSON)
	planSHA := hex.EncodeToString(sum[:])
	err = s.withOperatorTx(ctx, user, caseID, true, func(tx pgx.Tx) error {
		return operatorPostgres.NewImpact(platformpostgres.Bind(tx)).Append(ctx, caseID, user.ID, plan, planJSON, planSHA)
	})
	if err != nil {
		return auditext.ImpactPlan{}, err
	}
	return plan, nil
}

func operatorFactsForAssociation(association ServiceObjectContractAssociation) ([]auditext.Fact, error) {
	return operatorApp.FactsForAssociation(association)
}

// operatorObservationsForEvaluation derives a safe evaluation view without
// rewriting the collected snapshot. A collection can be immutable and still
// be unusable for a requested historical instant if its evidence was
// collected after the cutoff or lacks an immutable temporal proof.
func operatorObservationsForEvaluation(snapshot OperatorSnapshotSet, businessAt, cutoff time.Time) []auditext.Observation {
	return operatorApp.ObservationsForEvaluation(snapshot, businessAt, cutoff)
}

func loadOperatorBindingRecordByKey(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, actorID, key string) (operatorObjectBindingRecord, error) {
	row, found, err := operatorPostgres.NewBinding(platformpostgres.Bind(tx)).BindingByKey(ctx, operatorApp.CreateIdentity{TenantID: tenantID, OrgID: orgID, CaseID: caseID, ActorID: actorID}, key)
	if err != nil {
		return operatorObjectBindingRecord{}, err
	}
	if !found {
		return operatorObjectBindingRecord{}, pgx.ErrNoRows
	}
	return operatorApp.DecodeBindingRecord(row, "对象映射幂等回执损坏")
}

func loadOperatorBindingByKey(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, actorID, key string) (OperatorObjectBinding, error) {
	record, err := loadOperatorBindingRecordByKey(ctx, tx, tenantID, orgID, caseID, actorID, key)
	return record.Value, err
}

func (s *Store) GetOperatorCollectionJob(ctx context.Context, user accounts.User, caseID, key string) (OperatorCollectionJob, error) {
	ctx = commandContext(ctx, user)
	key = strings.TrimSpace(key)
	if err := validateIdempotencyKey(key); err != nil {
		return OperatorCollectionJob{}, err
	}
	return s.getOperatorCollectionJob(ctx, user, caseID, key, true)
}

// GetOperatorCollectionJobByID is the user-facing recovery route. The
// generated collection ID is safe to put in a URL; the caller's raw
// Idempotency-Key remains a POST-only recovery credential and is never exposed
// in the path.
func (s *Store) GetOperatorCollectionJobByID(ctx context.Context, user accounts.User, caseID, collectionID string) (OperatorCollectionJob, error) {
	ctx = commandContext(ctx, user)
	collectionID = strings.TrimSpace(collectionID)
	if !validOperatorCollectionID(collectionID) {
		return OperatorCollectionJob{}, fault.NotFound("没有找到采集请求")
	}
	return s.getOperatorCollectionJob(ctx, user, caseID, collectionID, false)
}

func validOperatorCollectionID(value string) bool { return operatorApp.ValidCollectionID(value) }

func (s *Store) getOperatorCollectionJob(ctx context.Context, user accounts.User, caseID, selector string, byKey bool) (OperatorCollectionJob, error) {
	ctx = commandContext(ctx, user)
	var out OperatorCollectionJob
	permission := access.Read
	if byKey {
		permission = access.Review
	}
	current, _, _, _, _, err := s.operatorCaseState(ctx, user, caseID, permission)
	if err != nil {
		return out, err
	}
	err = s.withOperatorTx(ctx, current, caseID, false, func(tx pgx.Tx) error {
		freshState, err := loadState(ctx, tx, false)
		if err != nil {
			return err
		}
		freshCase, err := s.visibleCase(&freshState, current, caseID)
		if err != nil {
			return err
		}
		freshActor, err := trustedUser(user)
		if err != nil {
			return err
		}
		if !authorizeCase(freshCase, freshActor, permission) {
			return fault.Forbidden("当前授权不能读取此案件的外部采集状态")
		}
		id := operatorModel.CollectionIdentity{TenantID: freshCase.TenantID, OrgID: freshCase.OwnerOrgID, CaseID: caseID, ActorID: freshActor.ID}
		out, err = operatorApp.ReadCollectionJob(ctx, operatorPostgres.NewCollection(platformpostgres.Bind(tx)), id, selector, byKey)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return OperatorCollectionJob{}, fault.NotFound("没有找到采集请求")
	}
	return out, err
}

func (s *Store) operatorReadSnapshotSet(ctx context.Context, user accounts.User, caseID, snapshotSetID string) (OperatorSnapshotSet, error) {
	ctx = commandContext(ctx, user)
	var out OperatorSnapshotSet
	err := s.withOperatorTx(ctx, user, caseID, false, func(tx pgx.Tx) error {
		var err error
		out, err = loadOperatorSnapshotSetByID(ctx, tx, caseID, snapshotSetID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.NotFound("没有找到外部快照集")
	}
	return out, err
}

func loadOperatorSnapshotSetByID(ctx context.Context, tx pgx.Tx, caseID, snapshotSetID string) (OperatorSnapshotSet, error) {
	return operatorApp.ReadCollectionSnapshot(ctx, operatorPostgres.NewCollection(platformpostgres.Bind(tx)), caseID, snapshotSetID)
}

func loadOperatorObservations(ctx context.Context, tx pgx.Tx, caseID, snapshotSetID string) ([]auditext.Observation, error) {
	return operatorApp.ReadCollectionObservations(ctx, operatorPostgres.NewCollection(platformpostgres.Bind(tx)), caseID, snapshotSetID)
}

func loadOperatorEvaluationByKey(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, actorID, key string) (operatorEvaluationRecord, error) {
	return operatorApp.ReadEvaluationByKey(ctx, operatorPostgres.NewEvaluation(platformpostgres.Bind(tx)), operatorApp.CreateIdentity{TenantID: tenantID, OrgID: orgID, CaseID: caseID, ActorID: actorID}, key)
}
func loadOperatorEvaluation(ctx context.Context, tx pgx.Tx, caseID, evaluationID string) (OperatorEvaluation, error) {
	record, err := operatorApp.ReadEvaluation(ctx, operatorPostgres.NewEvaluation(platformpostgres.Bind(tx)), caseID, evaluationID)
	return record.Value, err
}

func loadOperatorPreviousEvaluation(ctx context.Context, tx pgx.Tx, caseID string, target OperatorEvaluation) (OperatorEvaluation, error) {
	return operatorApp.ReadPreviousEvaluation(ctx, operatorPostgres.NewEvaluation(platformpostgres.Bind(tx)), caseID, target)
}
