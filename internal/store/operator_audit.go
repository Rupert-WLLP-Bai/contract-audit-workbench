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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
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
type OperatorEvaluationInput struct {
	PackID               string                   `json:"pack_id"`
	PackVersion          string                   `json:"pack_version"`
	SourceReviewID       string                   `json:"source_review_id"`
	SourceAttemptID      string                   `json:"source_attempt_id"`
	ContractVersionID    string                   `json:"contract_version_id"`
	ObjectBindingVersion string                   `json:"object_binding_version"`
	SnapshotSetID        string                   `json:"snapshot_set_id"`
	BusinessAt           time.Time                `json:"business_at"`
	EvidenceCutoffAt     *time.Time               `json:"evidence_cutoff_at,omitempty"`
	Scope                *OperatorEvaluationScope `json:"scope,omitempty"`
}

type OperatorEvaluationScope struct {
	Kind             string   `json:"kind"`
	ServiceObjectIDs []string `json:"service_object_ids,omitempty"`
}

func normalizedOperatorEvaluationScope(input OperatorEvaluationInput) (OperatorEvaluationScope, error) {
	scope := OperatorEvaluationScope{Kind: "whole_case"}
	if input.Scope != nil {
		scope = *input.Scope
	}
	if len(scope.ServiceObjectIDs) > 100 {
		return OperatorEvaluationScope{}, fault.TooLarge("单次核验最多包含100个服务对象")
	}
	scope.Kind = strings.TrimSpace(scope.Kind)
	ids := make([]string, 0, len(scope.ServiceObjectIDs))
	seen := make(map[string]struct{}, len(scope.ServiceObjectIDs))
	for _, raw := range scope.ServiceObjectIDs {
		id := strings.TrimSpace(raw)
		if auditext.ValidateID(id) != nil {
			return OperatorEvaluationScope{}, fault.Invalid("核验范围内的服务对象标识无效")
		}
		if _, exists := seen[id]; exists {
			return OperatorEvaluationScope{}, fault.Invalid("核验范围不能重复选择服务对象")
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	scope.ServiceObjectIDs = ids
	switch scope.Kind {
	case "whole_case":
		if len(ids) != 0 {
			return OperatorEvaluationScope{}, fault.Invalid("整案核验不能携带单独的服务对象列表")
		}
		if input.ObjectBindingVersion != "" || input.SnapshotSetID != "" || input.ContractVersionID != "" {
			return OperatorEvaluationScope{}, fault.Invalid("整案核验由服务端解析对象与证据，不能只提交单个对象映射")
		}
	case "explicit_subset":
		if len(ids) == 0 {
			return OperatorEvaluationScope{}, fault.Invalid("明确范围至少需要一个服务对象")
		}
		hasLegacyPins := input.ObjectBindingVersion != "" || input.SnapshotSetID != "" || input.ContractVersionID != ""
		if hasLegacyPins && (len(ids) != 1 || input.ObjectBindingVersion == "" || input.SnapshotSetID == "" || input.ContractVersionID == "") {
			return OperatorEvaluationScope{}, fault.Invalid("单对象兼容映射必须同时提供来源合同、映射和快照")
		}
	default:
		return OperatorEvaluationScope{}, fault.Invalid("核验范围类型无效")
	}
	return scope, nil
}

// OperatorEvaluation is the persisted deterministic result plus its frozen
// input. Human decisions remain in the existing decision flow.
type OperatorEvaluation struct {
	CaseID               string                      `json:"case_id"`
	EvaluationID         string                      `json:"evaluation_id"`
	PackID               string                      `json:"pack_id"`
	PackVersion          string                      `json:"pack_version"`
	SourceReviewID       string                      `json:"source_review_id"`
	SourceAttemptID      string                      `json:"source_attempt_id"`
	ContractVersionID    string                      `json:"contract_version_id,omitempty"`
	RulePackVersion      string                      `json:"rule_pack_version"`
	ObjectBindingVersion string                      `json:"object_binding_version,omitempty"`
	SnapshotSetID        string                      `json:"snapshot_set_id,omitempty"`
	BusinessAt           time.Time                   `json:"business_at"`
	EvidenceCutoffAt     *time.Time                  `json:"evidence_cutoff_at,omitempty"`
	MachineStatus        string                      `json:"machine_status"`
	Checks               []auditext.CheckResult      `json:"checks"`
	CreatedAt            time.Time                   `json:"created_at"`
	Input                auditext.FrozenInput        `json:"-"`
	ResultSHA256         string                      `json:"-"`
	Scope                *OperatorEvaluationScope    `json:"scope,omitempty"`
	Coverage             *OperatorEvaluationCoverage `json:"coverage,omitempty"`
	Objects              []OperatorEvaluationObject  `json:"objects,omitempty"`
	requestSHA256        string
}

type OperatorEvaluationCoverage struct {
	ScopeKind         string `json:"scope_kind"`
	CandidateCount    int    `json:"candidate_count"`
	EvaluatedCount    int    `json:"evaluated_count"`
	UnresolvedCount   int    `json:"unresolved_count"`
	InsufficientCount int    `json:"insufficient_count"`
	Status            string `json:"status"`
	ReasonCode        string `json:"reason_code,omitempty"`
}

type OperatorEvaluationObject struct {
	ObjectRef            string                    `json:"object_ref"`
	Source               *OperatorEvaluationSource `json:"source"`
	ServiceObjectID      *string                   `json:"service_object_id"`
	ObjectVersion        *int                      `json:"object_version"`
	AssociationVersion   *int                      `json:"association_version"`
	AssociationRunID     string                    `json:"association_run_id,omitempty"`
	AssociationAttemptID string                    `json:"association_attempt_id,omitempty"`
	MappingID            *string                   `json:"mapping_id"`
	MappingVersion       *int64                    `json:"mapping_version"`
	BindingVersion       *string                   `json:"binding_version"`
	SnapshotSetID        *string                   `json:"snapshot_set_id"`
	Status               string                    `json:"status"`
	ReasonCode           string                    `json:"reason_code"`
	FactsSHA256          string                    `json:"facts_sha256,omitempty"`
	FactRefs             []ServiceObjectFactRef    `json:"fact_refs"`
	SnapshotCollectedAt  *time.Time                `json:"snapshot_collected_at,omitempty"`
	Facts                []auditext.Fact           `json:"facts"`
	Checks               []auditext.CheckResult    `json:"checks"`
}

type OperatorEvaluationSource struct {
	RunID                string `json:"run_id"`
	AttemptID            string `json:"attempt_id"`
	AssociationRunID     string `json:"association_run_id,omitempty"`
	AssociationAttemptID string `json:"association_attempt_id,omitempty"`
	DocumentID           string `json:"document_id"`
	ParsedDocumentID     string `json:"parsed_document_id"`
	MaterialID           string `json:"material_id"`
	MaterialVersion      int    `json:"material_version"`
	Purpose              string `json:"purpose"`
	Role                 string `json:"role"`
}

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

func operatorHash(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func operatorValidTimestamp(value time.Time) bool { return !value.IsZero() }

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
	key := strings.TrimSpace(idempotencyKey)
	if key == "" {
		return OperatorEvaluation{}, fault.Invalid("缺少幂等键")
	}
	if evaluator == nil {
		return OperatorEvaluation{}, fault.New(503, "fulfillment_unavailable", "履约核验器未配置")
	}
	scope, err := normalizedOperatorEvaluationScope(input)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	input.Scope = &scope
	if input.PackID != pack.PackID || input.PackVersion != pack.Version {
		return OperatorEvaluation{}, fault.Conflict("能力包版本与请求不一致")
	}
	if !operatorValidTimestamp(input.BusinessAt) {
		return OperatorEvaluation{}, fault.Invalid("业务时间不合法")
	}
	input.BusinessAt = input.BusinessAt.UTC().Truncate(time.Microsecond)
	evidenceCutoffAt := input.BusinessAt.UTC().Truncate(time.Microsecond)
	if input.EvidenceCutoffAt != nil {
		evidenceCutoffAt = input.EvidenceCutoffAt.UTC().Truncate(time.Microsecond)
	}
	input.EvidenceCutoffAt = &evidenceCutoffAt
	if evidenceCutoffAt.After(time.Now().UTC()) {
		return OperatorEvaluation{}, fault.Invalid("证据截止时间不能晚于当前受理时间")
	}
	current, st, item, _, _, err := s.operatorCaseState(ctx, user, caseID, access.Review)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	if item.Scenario != pack.Scenario {
		return OperatorEvaluation{}, fault.Conflict("能力包场景与案件不一致")
	}
	requestDigest, err := operatorHash(input)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	var replay OperatorEvaluation
	err = s.withOperatorTx(ctx, user, caseID, false, func(tx pgx.Tx) error {
		var loadErr error
		replay, loadErr = loadOperatorEvaluationByKey(ctx, tx, item.TenantID, item.OwnerOrgID, caseID, current.ID, key)
		return loadErr
	})
	if err == nil {
		if replay.requestSHA256 != requestDigest {
			return OperatorEvaluation{}, fault.Conflict("同一幂等键对应了不同的核验请求")
		}
		return replay, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return OperatorEvaluation{}, err
	}
	if scope.Kind == "whole_case" || input.ObjectBindingVersion == "" || input.SnapshotSetID == "" || input.ContractVersionID == "" {
		return s.createAggregateOperatorFulfillmentEvaluation(ctx, user, current, st, item, input, scope, pack, evaluator, key)
	}
	binding, err := s.operatorReadBinding(ctx, user, caseID, input.ObjectBindingVersion)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	if binding.Status != "confirmed" {
		return OperatorEvaluation{}, fault.Conflict("对象映射尚未确认")
	}
	if input.BusinessAt.Before(binding.EffectiveFrom) || !input.BusinessAt.Before(binding.EffectiveUntil) {
		return OperatorEvaluation{}, fault.Conflict("业务时间不在已确认对象映射的有效期间内")
	}
	snapshot, err := s.operatorReadSnapshotSet(ctx, user, caseID, input.SnapshotSetID)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	if snapshot.BindingVersion != binding.Version {
		return OperatorEvaluation{}, fault.Conflict("快照集不属于该对象映射")
	}
	if !snapshot.BusinessAt.Equal(input.BusinessAt) {
		return OperatorEvaluation{}, fault.Conflict("快照集业务时间与核验请求不一致")
	}
	if len(snapshot.Observations) == 0 {
		return OperatorEvaluation{}, fault.Conflict("快照集没有可用观测")
	}
	run, attempt, attemptInput, _, err := s.validateFrozenTerminalAttempt(st, item, input.SourceReviewID, input.SourceAttemptID)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	if run.CaseID != caseID {
		return OperatorEvaluation{}, fault.Conflict("来源审查不属于当前案件")
	}
	if !operatorFrozenHasContract(attemptInput, input.ContractVersionID) {
		return OperatorEvaluation{}, fault.Conflict("合同版本不在冻结输入中")
	}
	if binding.ServiceObjectID == "" || binding.ObjectVersion < 1 || binding.AssociationVersion < 1 {
		return OperatorEvaluation{}, fault.Conflict("历史对象映射尚未绑定已确认的服务对象，不能发起新的核验")
	}
	association, err := s.GetServiceObjectAssociation(ctx, user, caseID, binding.ServiceObjectID, binding.AssociationVersion)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	if !operatorAssociationMatchesFrozenAttempt(association, binding, input, attemptInput) {
		return OperatorEvaluation{}, fault.Conflict("对象映射未固定到请求所选的合同材料、解析和审查尝试")
	}
	if association.CreatedAt.After(evidenceCutoffAt) || association.ConfirmedAt.After(evidenceCutoffAt) ||
		binding.CreatedAt.After(evidenceCutoffAt) || binding.ConfirmedAt.After(evidenceCutoffAt) {
		return OperatorEvaluation{}, fault.Conflict("对象关联或映射晚于证据截止时间")
	}
	rulePackVersion := attempt.RulePackVersion
	if rulePackVersion == "" {
		rulePackVersion = pack.RulePackRef.Version
	}
	if rulePackVersion != pack.RulePackRef.Version ||
		(attempt.RulePackID != "" && attempt.RulePackID != pack.RulePackRef.ID) {
		return OperatorEvaluation{}, fault.Conflict("来源审查使用的规则包与能力包不一致")
	}
	var selectedSource []Interpreted
	for _, source := range interpretedFromFrozen(attemptInput.Documents) {
		if source.Document.ID == association.DocumentID && source.ParsedDocument != nil && source.ParsedDocument.ID == association.ParsedDocumentID {
			selectedSource = append(selectedSource, source)
		}
	}
	if len(selectedSource) != 1 {
		return OperatorEvaluation{}, fault.Conflict("服务对象合同关联未固定到唯一的审查解析")
	}
	selectedPurpose := ""
	for _, frozenDocument := range attemptInput.Documents {
		if frozenDocument.Document.ID == association.DocumentID && frozenDocument.ParsedDocumentID == association.ParsedDocumentID && frozenDocument.Material != nil {
			originalAt, originalErr := time.Parse(time.RFC3339Nano, frozenDocument.Document.CreatedAt)
			parsedAt, parsedErr := time.Parse(time.RFC3339Nano, frozenDocument.ParsedCreatedAt)
			if originalErr != nil || parsedErr != nil || originalAt.After(evidenceCutoffAt) || parsedAt.After(evidenceCutoffAt) {
				return OperatorEvaluation{}, fault.Conflict("合同或解析取得时间晚于证据截止时间，或无法核实")
			}
			selectedPurpose = frozenDocument.Material.Purpose
			break
		}
	}
	if _, err := telecomSources(selectedSource); err != nil {
		return OperatorEvaluation{}, fault.Conflict("冻结合同输入未通过完整性校验")
	}
	frozenFacts, err := operatorFactsForAssociation(association)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	runSnapshot := st.snapshotOf(input.SourceReviewID)
	modelID := runSnapshot.ModelConfigurationID
	if modelID == "" {
		modelID = "not_used"
	}
	plannerID := runSnapshot.PlannerConfigurationID
	if plannerID == "" {
		plannerID = "not_used"
	}
	parserID := runSnapshot.ParserConfigurationID
	if parserID == "" {
		parserID = "not_used"
	}
	frozen := auditext.FrozenInput{
		Scope: auditext.AuthorizedScope{
			TenantID: item.TenantID, CaseID: caseID, ActorID: current.ID,
			GrantVersion: operatorGrantVersion(item, current),
		},
		Pack:                   auditext.PackRef{ID: pack.PackID, Version: pack.Version},
		ContractVersion:        input.ContractVersionID,
		RulePackVersion:        rulePackVersion,
		BindingStatus:          auditext.BindingConfirmed,
		BindingVersion:         binding.Version,
		SnapshotSetID:          snapshot.SnapshotSetID,
		ExternalObjectID:       binding.ExternalObject.ExternalObjectID,
		SourceSystem:           snapshot.SourceSystem,
		ModelConfigurationID:   modelID,
		PlannerConfigurationID: plannerID,
		ParserConfigurationID:  parserID,
		BusinessAt:             input.BusinessAt,
		EvidenceCutoffAt:       &evidenceCutoffAt,
		Facts:                  frozenFacts,
		EvaluationScope:        &auditext.EvaluationScope{Kind: scope.Kind, ServiceObjectIDs: append([]string(nil), scope.ServiceObjectIDs...)},
	}
	if err := frozen.Validate(); err != nil {
		return OperatorEvaluation{}, fault.Conflict("冻结核验输入不完整")
	}
	observations := operatorObservationsForEvaluation(snapshot, input.BusinessAt, evidenceCutoffAt)
	checks, err := evaluator.Evaluate(ctx, frozen, observations)
	if err != nil {
		if errors.Is(err, auditext.ErrUnauthorized) {
			return OperatorEvaluation{}, fault.Forbidden("当前授权不能核验该案件")
		}
		return OperatorEvaluation{}, fault.Conflict("确定性核验未完成")
	}
	if len(checks) == 0 {
		return OperatorEvaluation{}, fault.New(502, "fulfillment_empty", "核验器没有返回检查结果")
	}
	machineStatus := "completed"
	for _, check := range checks {
		if err := check.Validate(); err != nil {
			return OperatorEvaluation{}, fault.New(502, "fulfillment_invalid", "核验器返回了不合法的结果")
		}
		if check.MachineStatus != auditext.MachineCompleted {
			machineStatus = "partial"
		}
	}
	requestSHA, err := operatorHash(input)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	inputJSON, err := json.Marshal(frozen)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	resultJSON, err := json.Marshal(checks)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	resultSum := sha256.Sum256(resultJSON)
	resultSHA := hex.EncodeToString(resultSum[:])
	evaluationID, err := newID("eval_")
	if err != nil {
		return OperatorEvaluation{}, err
	}
	var out OperatorEvaluation
	err = s.withOperatorTx(ctx, user, caseID, true, func(tx pgx.Tx) error {
		existing, err := loadOperatorEvaluationByKey(ctx, tx, item.TenantID, item.OwnerOrgID, caseID, current.ID, key)
		if err == nil {
			if existing.ResultSHA256 != resultSHA || existing.requestSHA256 != requestSHA {
				return fault.Conflict("同一幂等键对应了不同的核验结果")
			}
			out = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		if _, err := tx.Exec(ctx, `INSERT INTO operator_fulfillment_evaluations
			(tenant_id,org_id,case_id,evaluation_id,pack_id,pack_version,source_run_id,source_attempt_id,
			 contract_version_id,rule_pack_version,binding_version,snapshot_set_id,business_at,input_json,input_sha256,
			 result_json,result_sha256,machine_status,created_by,actor_id,idempotency_key,request_sha256,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$19,$20,$21,$22)`,
			item.TenantID, item.OwnerOrgID, caseID, evaluationID, pack.PackID, pack.Version,
			input.SourceReviewID, input.SourceAttemptID, input.ContractVersionID, rulePackVersion,
			binding.Version, snapshot.SnapshotSetID, input.BusinessAt, inputJSON, requestSHA, resultJSON, resultSHA,
			machineStatus, current.ID, key, requestSHA, now); err != nil {
			return err
		}
		for _, check := range checks {
			evidenceJSON, _ := json.Marshal(check.Evidence)
			limitationsJSON, _ := json.Marshal(check.Limitations)
			dependenciesJSON, _ := json.Marshal(check.Dependencies)
			checkJSON, _ := json.Marshal(check)
			if _, err := tx.Exec(ctx, `INSERT INTO operator_fulfillment_checks
				(tenant_id,org_id,case_id,evaluation_id,check_id,relation,reason_code,machine_status,
				 requires_human_review,evidence,limitations,dependencies,result_json,created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
				item.TenantID, item.OwnerOrgID, caseID, evaluationID, check.CheckID, string(check.Relation),
				check.ReasonCode, string(check.MachineStatus), check.RequiresHumanReview, evidenceJSON,
				limitationsJSON, dependenciesJSON, checkJSON, now); err != nil {
				return err
			}
		}
		out = OperatorEvaluation{
			CaseID: caseID, EvaluationID: evaluationID, PackID: pack.PackID, PackVersion: pack.Version,
			SourceReviewID: input.SourceReviewID, SourceAttemptID: input.SourceAttemptID,
			ContractVersionID: input.ContractVersionID, RulePackVersion: rulePackVersion,
			ObjectBindingVersion: binding.Version, SnapshotSetID: snapshot.SnapshotSetID,
			BusinessAt: input.BusinessAt, EvidenceCutoffAt: &evidenceCutoffAt,
			MachineStatus: machineStatus, Checks: checks,
			CreatedAt: now, Input: frozen, ResultSHA256: resultSHA, requestSHA256: requestSHA,
			Scope: &scope,
		}
		evaluationObject, err := operatorEvaluationObjectForAssociation(scope, input, binding, association, snapshot, selectedPurpose, selectedSource[0].Document.Role, frozenFacts, checks, machineStatus)
		if err != nil {
			return err
		}
		out.Objects = []OperatorEvaluationObject{evaluationObject}
		coverage := summarizeOperatorEvaluationObjects(scope.Kind, out.Objects)
		out.Coverage = &coverage
		objectJSON, err := json.Marshal(evaluationObject)
		if err != nil {
			return err
		}
		objectSum := sha256.Sum256(objectJSON)
		if _, err := tx.Exec(ctx, `INSERT INTO operator_fulfillment_evaluation_objects
			(tenant_id,org_id,case_id,evaluation_id,ordinal,object_ref,source_run_id,source_attempt_id,material_id,material_version,
			 document_id,parsed_document_id,purpose,source_role,facts_canonical_json,facts_sha256,service_object_id,object_version,
			 association_version,mapping_id,mapping_version,binding_version,snapshot_set_id,status,reason_code,result_json,result_sha256,created_at)
			VALUES ($1,$2,$3,$4,1,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,clock_timestamp())`,
			item.TenantID, item.OwnerOrgID, caseID, evaluationID, association.ObjectRef, input.SourceReviewID, input.SourceAttemptID,
			association.MaterialID, association.MaterialVersion, association.DocumentID, association.ParsedDocumentID,
			selectedPurpose, selectedSource[0].Document.Role, association.FactsCanonicalJSON, association.FactsSHA256,
			binding.ServiceObjectID, binding.ObjectVersion, binding.AssociationVersion, binding.MappingID, binding.MappingVersion,
			binding.Version, snapshot.SnapshotSetID, evaluationObject.Status, evaluationObject.ReasonCode, objectJSON,
			hex.EncodeToString(objectSum[:])); err != nil {
			return err
		}
		return nil
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
	var plan auditext.ImpactPlan
	if !baseFound {
		checks := make([]auditext.ImpactCheck, 0, len(target.Checks))
		for _, check := range target.Checks {
			checks = append(checks, auditext.ImpactCheck{CheckID: check.CheckID, Action: auditext.ImpactRecompute, Reasons: []string{"baseline_missing"}})
		}
		plan = auditext.ImpactPlan{
			SchemaVersion: auditext.ImpactPlanSchema, BaseEvaluationID: "none", TargetEvaluationID: target.EvaluationID,
			Mode: mode, DependencyComplete: false, Fallback: auditext.FallbackFull, FallbackReason: "baseline_missing",
			ChangedInputs: []string{}, Checks: checks, BusinessResultSource: "full", DataClass: auditext.DataClassSynthetic,
		}
	} else {
		var err error
		plan, err = planner.Plan(ctx, auditext.ImpactRequest{
			BaseEvaluationID: base.EvaluationID, TargetEvaluationID: target.EvaluationID,
			Base: base.Input, Target: target.Input, BaseChecks: base.Checks, TargetChecks: target.Checks, Mode: mode,
		})
		if err != nil {
			if errors.Is(err, auditext.ErrUnauthorized) {
				return auditext.ImpactPlan{}, fault.Forbidden("当前授权不能读取影响计划")
			}
			return auditext.ImpactPlan{}, fault.Conflict("影响计划未完成")
		}
	}
	if err := plan.Validate(); err != nil {
		return auditext.ImpactPlan{}, fault.New(502, "impact_plan_invalid", "影响计划格式不合法")
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return auditext.ImpactPlan{}, err
	}
	sum := sha256.Sum256(planJSON)
	planSHA := hex.EncodeToString(sum[:])
	err = s.withOperatorTx(ctx, user, caseID, true, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO operator_impact_plans
			(tenant_id,org_id,case_id,base_evaluation_id,target_evaluation_id,mode,plan_json,plan_sha256,created_by,created_at)
			VALUES (current_setting('app.tenant_id',true),current_setting('app.org_id',true),$1,$2,$3,$4,$5,$6,$7,clock_timestamp())
			ON CONFLICT DO NOTHING`, caseID, plan.BaseEvaluationID, plan.TargetEvaluationID, string(mode), planJSON, planSHA, user.ID)
		return err
	})
	if err != nil {
		return auditext.ImpactPlan{}, err
	}
	return plan, nil
}

func operatorFrozenHasContract(input AttemptInput, contractVersion string) bool {
	for _, frozen := range input.Documents {
		if frozen.Document.ID == contractVersion && frozen.Material != nil &&
			((frozen.Material.Purpose == "primary_contract" && frozen.Document.Role == "draft_contract") ||
				(frozen.Material.Purpose == "supplement" && frozen.Document.Role == "attachment")) {
			return true
		}
	}
	return false
}

func operatorAssociationMatchesFrozenAttempt(association ServiceObjectContractAssociation, binding OperatorObjectBinding,
	input OperatorEvaluationInput, attemptInput AttemptInput) bool {
	if association.ServiceObjectID != binding.ServiceObjectID || association.ObjectVersion != binding.ObjectVersion ||
		association.AssociationVersion != binding.AssociationVersion || association.DocumentID != input.ContractVersionID ||
		association.SourceRunID == "" || association.SourceAttemptID == "" ||
		association.MaterialID == "" || association.MaterialVersion < 1 || association.ParsedDocumentID == "" {
		return false
	}
	for _, frozen := range attemptInput.Documents {
		isContractSource := frozen.Material != nil &&
			((frozen.Material.Purpose == "primary_contract" && frozen.Document.Role == "draft_contract") ||
				(frozen.Material.Purpose == "supplement" && frozen.Document.Role == "attachment"))
		if frozen.Document.ID != association.DocumentID || frozen.ParsedDocumentID != association.ParsedDocumentID ||
			!isContractSource ||
			frozen.Material.MaterialID != association.MaterialID || frozen.Material.Version != association.MaterialVersion || !frozen.Ready {
			continue
		}
		return true
	}
	return false
}

func operatorFactsForAssociation(association ServiceObjectContractAssociation) ([]auditext.Fact, error) {
	canonical, digest, err := canonicalServiceObjectFacts(association.FactRefs)
	if err != nil || len(association.FactsCanonicalJSON) == 0 || !bytes.Equal(canonical, association.FactsCanonicalJSON) ||
		digest != association.FactsSHA256 || !validServiceObjectSHA256(association.FactsSHA256) {
		return nil, fault.Conflict("服务对象合同关联事实未通过完整性校验")
	}
	if err := validateServiceObjectFactRefs(association); err != nil {
		return nil, fault.Conflict("服务对象合同关联事实与材料解析不一致")
	}
	out := make([]auditext.Fact, 0, len(association.FactRefs))
	for _, reference := range association.FactRefs {
		fact := reference.Fact
		evidence := make([]auditext.EvidenceRef, 0, len(fact.Citations))
		for _, citation := range fact.Citations {
			if citation.SourceID != association.DocumentID || citation.SourceVersion != association.ParsedDocumentID {
				return nil, fault.Conflict("服务对象合同关联引用了不同的冻结材料或解析")
			}
			evidence = append(evidence, auditext.EvidenceRef{
				Kind: auditext.EvidenceDocument, DocumentVersionID: citation.SourceID,
				ParsedVersionID: citation.SourceVersion, Start: citation.Start, End: citation.End,
				Quote: citation.Quote, SourceDigest: citation.SourceDigest,
			})
		}
		var applicable *bool
		outOfScope := false
		if fact.Applicable != nil {
			value := *fact.Applicable
			applicable = &value
			outOfScope = !value
		}
		semanticKind := ""
		if fact.Dimension == "minimum_bandwidth" {
			semanticKind = "configured_value"
		}
		quantity := auditext.ExactQuantity{Value: fact.Value, Unit: fact.Unit}
		if !outOfScope && strings.ContainsRune(quantity.Value, '/') {
			rational, ok := new(big.Rat).SetString(quantity.Value)
			if !ok || rational.Sign() < 0 {
				return nil, fault.Conflict("服务对象合同关联事实的精确数值不合法")
			}
			precision, exact := rational.FloatPrec()
			if !exact || precision > 64 {
				return nil, fault.Conflict("服务对象合同关联事实不能无损表示为精确十进制")
			}
			quantity.Value = rational.FloatString(precision)
			if auditext.ValidateQuantity(quantity) != nil {
				return nil, fault.Conflict("服务对象合同关联事实的精确数值不合法")
			}
		}
		out = append(out, auditext.Fact{
			Name: fact.Dimension, Value: quantity,
			Evidence: evidence, OutOfScope: outOfScope, SemanticKind: semanticKind,
			FactID: fact.ID, RuleID: fact.RuleID, Dimension: fact.Dimension, ObjectRef: fact.ObjectRef,
			Applicable: applicable, Basis: fact.Basis, MeasurementWindow: fact.MeasurementWindow,
			TimeZone: fact.TimeZone, Calendar: fact.Calendar, OutageDefinition: fact.OutageDefinition,
			Exclusions: append([]string(nil), fact.Exclusions...),
		})
	}
	return out, nil
}

func operatorEvaluationObjectForAssociation(scope OperatorEvaluationScope, input OperatorEvaluationInput, binding OperatorObjectBinding,
	association ServiceObjectContractAssociation, snapshot OperatorSnapshotSet, purpose, role string,
	facts []auditext.Fact, checks []auditext.CheckResult, machineStatus string) (OperatorEvaluationObject, error) {
	inScope := scope.Kind == "whole_case"
	for _, id := range scope.ServiceObjectIDs {
		if id == binding.ServiceObjectID {
			inScope = true
		}
	}
	if (scope.Kind != "whole_case" && !inScope) ||
		purpose == "" || role == "" || association.DocumentID == "" || association.ParsedDocumentID == "" {
		return OperatorEvaluationObject{}, fault.Conflict("单对象结果未固定到明确的服务对象与合同来源")
	}
	serviceObjectID := binding.ServiceObjectID
	objectVersion := binding.ObjectVersion
	associationVersion := binding.AssociationVersion
	mappingID := binding.MappingID
	mappingVersion := binding.MappingVersion
	bindingVersion := binding.Version
	snapshotSetID := snapshot.SnapshotSetID
	status := "evaluated"
	reason := ""
	allNotApplicable := len(checks) > 0
	for _, check := range checks {
		if check.Relation != auditext.RelationNotApplicable {
			allNotApplicable = false
		}
		if check.Relation == auditext.RelationInsufficient {
			status = "insufficient"
			if reason == "" {
				reason = check.ReasonCode
			}
		}
	}
	if allNotApplicable {
		status = "not_applicable"
	}
	if machineStatus != "completed" || len(checks) == 0 || len(facts) == 0 {
		status = "insufficient"
		if reason == "" {
			reason = "evaluation_partial"
		}
	}

	return OperatorEvaluationObject{
		ObjectRef: association.ObjectRef,
		Source: &OperatorEvaluationSource{RunID: input.SourceReviewID, AttemptID: input.SourceAttemptID,
			DocumentID: association.DocumentID, ParsedDocumentID: association.ParsedDocumentID,
			MaterialID: association.MaterialID, MaterialVersion: association.MaterialVersion, Purpose: purpose, Role: role,
			AssociationRunID: association.SourceRunID, AssociationAttemptID: association.SourceAttemptID},
		ServiceObjectID: &serviceObjectID, ObjectVersion: &objectVersion, AssociationVersion: &associationVersion,
		AssociationRunID: association.SourceRunID, AssociationAttemptID: association.SourceAttemptID,
		MappingID: &mappingID, MappingVersion: &mappingVersion, BindingVersion: &bindingVersion, SnapshotSetID: &snapshotSetID,
		SnapshotCollectedAt: &snapshot.CollectedAt, Status: status, ReasonCode: reason, FactsSHA256: association.FactsSHA256,
		FactRefs: append([]ServiceObjectFactRef{}, association.FactRefs...),
		Facts:    append([]auditext.Fact{}, facts...), Checks: append([]auditext.CheckResult{}, checks...),
	}, nil
}

// operatorObservationsForEvaluation derives a safe evaluation view without
// rewriting the collected snapshot. A collection can be immutable and still
// be unusable for a requested historical instant if its evidence was
// collected after the cutoff or lacks an immutable temporal proof.
func operatorObservationsForEvaluation(snapshot OperatorSnapshotSet, businessAt, cutoff time.Time) []auditext.Observation {
	observations := append([]auditext.Observation(nil), snapshot.Observations...)
	businessAt = businessAt.UTC().Truncate(time.Microsecond)
	cutoff = cutoff.UTC().Truncate(time.Microsecond)
	for i := range observations {
		observation := &observations[i]
		if observation.Availability != auditext.AvailabilityAvailable {
			continue
		}
		if snapshot.CollectedAt.After(cutoff) || observation.ObservedAt.After(cutoff) {
			operatorMarkObservationInsufficient(observation, "evidence_after_cutoff")
			continue
		}
		proof := observation.TemporalProof
		if proof == nil {
			operatorMarkObservationInsufficient(observation, "historical_proof_missing")
			continue
		}
		if proof.SourceAsOf == nil || proof.SourceAsOf.After(cutoff) {
			operatorMarkObservationInsufficient(observation, "temporal_proof_unavailable")
			continue
		}
		switch proof.Kind {
		case auditext.TemporalProofHistoricalInterval:
			if proof.ValidFrom == nil || proof.ValidUntil == nil ||
				proof.ValidFrom.After(businessAt) || !businessAt.Before(*proof.ValidUntil) ||
				!proof.ValidFrom.Equal(observation.EffectiveFrom) || !proof.ValidUntil.Equal(observation.EffectiveUntil) {
				operatorMarkObservationInsufficient(observation, "historical_proof_unavailable")
			}
		case auditext.TemporalProofCurrentState:
			if !proof.SourceAsOf.Equal(businessAt) {
				operatorMarkObservationInsufficient(observation, "historical_proof_missing")
			}
		default:
			operatorMarkObservationInsufficient(observation, "temporal_proof_unavailable")
		}
	}
	return observations
}

func operatorMarkObservationInsufficient(observation *auditext.Observation, reason string) {
	observation.Availability = auditext.AvailabilityRejected
	observation.ReasonCode = reason
	observation.PayloadRef = nil
	observation.Fields = nil
	// The derived evaluation view does not carry a proof that was insufficient
	// for this business instant. The immutable collected snapshot is untouched.
	observation.TemporalProof = nil
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

func loadOperatorEvaluationByKey(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, actorID, key string) (OperatorEvaluation, error) {
	var evaluationID string
	if err := tx.QueryRow(ctx, `SELECT evaluation_id FROM operator_fulfillment_evaluations
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`,
		tenantID, orgID, caseID, actorID, key).Scan(&evaluationID); err != nil {
		return OperatorEvaluation{}, err
	}
	return loadOperatorEvaluation(ctx, tx, caseID, evaluationID)
}

func loadOperatorEvaluation(ctx context.Context, tx pgx.Tx, caseID, evaluationID string) (OperatorEvaluation, error) {
	var out OperatorEvaluation
	var inputJSON, resultJSON []byte
	err := tx.QueryRow(ctx, `SELECT case_id,evaluation_id,pack_id,pack_version,source_run_id,source_attempt_id,
		COALESCE(contract_version_id,''),rule_pack_version,COALESCE(binding_version,''),COALESCE(snapshot_set_id,''),business_at,input_json,result_json,
		result_sha256,machine_status,created_at,request_sha256
		FROM operator_fulfillment_evaluations WHERE tenant_id=current_setting('app.tenant_id',true)
		AND org_id=current_setting('app.org_id',true) AND case_id=$1 AND evaluation_id=$2`,
		caseID, evaluationID).Scan(&out.CaseID, &out.EvaluationID, &out.PackID, &out.PackVersion,
		&out.SourceReviewID, &out.SourceAttemptID, &out.ContractVersionID, &out.RulePackVersion,
		&out.ObjectBindingVersion, &out.SnapshotSetID, &out.BusinessAt, &inputJSON, &resultJSON,
		&out.ResultSHA256, &out.MachineStatus, &out.CreatedAt, &out.requestSHA256)
	if err != nil {
		return out, err
	}
	out.BusinessAt = out.BusinessAt.UTC()
	out.CreatedAt = out.CreatedAt.UTC()
	if err := json.Unmarshal(inputJSON, &out.Input); err != nil {
		return out, err
	}
	out.EvidenceCutoffAt = out.Input.EvidenceCutoffAt
	if out.Input.EvaluationScope != nil {
		scope := OperatorEvaluationScope{Kind: out.Input.EvaluationScope.Kind, ServiceObjectIDs: append([]string(nil), out.Input.EvaluationScope.ServiceObjectIDs...)}
		out.Scope = &scope
	}
	checks, err := loadOperatorChecks(ctx, tx, caseID, evaluationID)
	if err != nil {
		return out, err
	}
	if len(checks) == 0 {
		if err := json.Unmarshal(resultJSON, &checks); err != nil {
			return out, err
		}
	}
	out.Checks = checks
	if out.Input.EvaluationScope != nil {
		objects, err := loadOperatorEvaluationObjects(ctx, tx, caseID, evaluationID)
		if err != nil {
			return out, err
		}
		out.Objects = objects
		coverage := summarizeOperatorEvaluationObjects(scopeKind(out.Input.EvaluationScope), objects)
		out.Coverage = &coverage
		if len(checks) == 0 {
			canonicalObjects, err := json.Marshal(objects)
			if err != nil {
				return out, err
			}
			sum := sha256.Sum256(canonicalObjects)
			if hex.EncodeToString(sum[:]) != out.ResultSHA256 {
				return out, fault.New(500, "integrity_error", "整案核验结果摘要不匹配")
			}
		}
	}
	return out, nil
}

func loadOperatorEvaluationObjects(ctx context.Context, tx pgx.Tx, caseID, evaluationID string) ([]OperatorEvaluationObject, error) {
	rows, err := tx.Query(ctx, `SELECT object_ref,facts_canonical_json,facts_sha256,result_json,result_sha256
		FROM operator_fulfillment_evaluation_objects WHERE tenant_id=current_setting('app.tenant_id',true)
		AND org_id=current_setting('app.org_id',true) AND case_id=$1 AND evaluation_id=$2 ORDER BY ordinal`, caseID, evaluationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	objects := make([]OperatorEvaluationObject, 0)
	for rows.Next() {
		var objectRef, factsSHA, resultSHA string
		var factsRaw, resultRaw []byte
		if err := rows.Scan(&objectRef, &factsRaw, &factsSHA, &resultRaw, &resultSHA); err != nil {
			return nil, err
		}
		factsSum := sha256.Sum256(factsRaw)
		resultSum := sha256.Sum256(resultRaw)
		if hex.EncodeToString(factsSum[:]) != factsSHA || hex.EncodeToString(resultSum[:]) != resultSHA {
			return nil, fault.New(500, "integrity_error", "核验对象冻结事实或结果摘要不匹配")
		}
		var object OperatorEvaluationObject
		if err := json.Unmarshal(resultRaw, &object); err != nil || object.ObjectRef != objectRef || object.FactsSHA256 != factsSHA {
			return nil, fault.New(500, "integrity_error", "核验对象冻结结果损坏")
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return objects, nil
}

func scopeKind(scope *auditext.EvaluationScope) string {
	if scope == nil {
		return ""
	}
	return scope.Kind
}

func loadOperatorChecks(ctx context.Context, tx pgx.Tx, caseID, evaluationID string) ([]auditext.CheckResult, error) {
	rows, err := tx.Query(ctx, `SELECT result_json,dependencies FROM operator_fulfillment_checks
		WHERE tenant_id=current_setting('app.tenant_id',true) AND org_id=current_setting('app.org_id',true)
		AND case_id=$1 AND evaluation_id=$2 ORDER BY check_id`, caseID, evaluationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auditext.CheckResult
	for rows.Next() {
		var checkJSON, dependenciesJSON []byte
		if err := rows.Scan(&checkJSON, &dependenciesJSON); err != nil {
			return nil, err
		}
		var check auditext.CheckResult
		if err := json.Unmarshal(checkJSON, &check); err != nil {
			return nil, err
		}
		if len(dependenciesJSON) > 0 {
			if err := json.Unmarshal(dependenciesJSON, &check.Dependencies); err != nil {
				return nil, err
			}
		}
		out = append(out, check)
	}
	return out, rows.Err()
}

func loadOperatorPreviousEvaluation(ctx context.Context, tx pgx.Tx, caseID string, target OperatorEvaluation) (OperatorEvaluation, error) {
	var evaluationID string
	err := tx.QueryRow(ctx, `SELECT evaluation_id FROM operator_fulfillment_evaluations
		WHERE tenant_id=current_setting('app.tenant_id',true) AND org_id=current_setting('app.org_id',true)
		AND case_id=$1 AND pack_id=$2 AND pack_version=$3 AND binding_version=$4 AND created_at < $5
		ORDER BY created_at DESC, evaluation_id DESC LIMIT 1`,
		caseID, target.PackID, target.PackVersion, target.ObjectBindingVersion, target.CreatedAt).Scan(&evaluationID)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	return loadOperatorEvaluation(ctx, tx, caseID, evaluationID)
}
