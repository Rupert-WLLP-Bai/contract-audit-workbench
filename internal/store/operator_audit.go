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
	"contractaudit/internal/rulepack"
	"contractaudit/internal/telecom"

	"github.com/jackc/pgx/v5"
)

// OperatorContractObject is the human-selected contract side of a mapping.
type OperatorContractObject struct {
	ObjectType string  `json:"object_type"`
	ObjectID   string  `json:"object_id"`
	Label      *string `json:"label"`
}

// OperatorExternalObject is the deployment-registered external side of a
// mapping. ConnectorID is an identifier, never a URL or credential.
type OperatorExternalObject struct {
	ConnectorID      string `json:"connector_id"`
	ExternalObjectID string `json:"external_object_id"`
}

// OperatorObjectBindingInput is a human confirmation. Similar names are never
// matched automatically; Confirmed must be true.
type OperatorObjectBindingInput struct {
	ContractObject OperatorContractObject `json:"contract_object"`
	ExternalObject OperatorExternalObject `json:"external_object"`
	EffectiveFrom  time.Time              `json:"effective_from"`
	EffectiveUntil time.Time              `json:"effective_until"`
	Confirmed      bool                   `json:"confirmed"`
}

// OperatorObjectBinding is the immutable persisted mapping.
type OperatorObjectBinding struct {
	CaseID         string                 `json:"case_id"`
	BindingID      string                 `json:"binding_id"`
	Version        string                 `json:"version"`
	Status         string                 `json:"status"`
	ContractObject OperatorContractObject `json:"contract_object"`
	ExternalObject OperatorExternalObject `json:"external_object"`
	SourceSystem   string                 `json:"source_system"`
	ConfirmedBy    string                 `json:"confirmed_by"`
	ConfirmedAt    time.Time              `json:"confirmed_at"`
	EffectiveFrom  time.Time              `json:"effective_from"`
	EffectiveUntil time.Time              `json:"effective_until"`
	CreatedAt      time.Time              `json:"created_at"`
	requestSHA256  string
}

// OperatorSnapshotSet is one immutable collection result. A failed read is
// persisted as unavailable/rejected evidence, never as a successful snapshot.
type OperatorSnapshotSet struct {
	CaseID           string                 `json:"case_id"`
	SnapshotSetID    string                 `json:"snapshot_set_id"`
	BindingVersion   string                 `json:"binding_version"`
	ConnectorID      string                 `json:"connector_id"`
	ExternalObjectID string                 `json:"external_object_id"`
	SourceSystem     string                 `json:"source_system"`
	BusinessAt       time.Time              `json:"business_at"`
	Status           string                 `json:"status"`
	ReasonCode       string                 `json:"reason_code"`
	CollectedBy      string                 `json:"collected_by"`
	CollectedAt      time.Time              `json:"collected_at"`
	Observations     []auditext.Observation `json:"observations"`
}

// OperatorEvaluationInput is the frozen identity accepted for one deterministic
// evaluation. The server resolves every referenced version; none is trusted
// from another tenant.
type OperatorEvaluationInput struct {
	PackID               string    `json:"pack_id"`
	PackVersion          string    `json:"pack_version"`
	SourceReviewID       string    `json:"source_review_id"`
	SourceAttemptID      string    `json:"source_attempt_id"`
	ContractVersionID    string    `json:"contract_version_id"`
	ObjectBindingVersion string    `json:"object_binding_version"`
	SnapshotSetID        string    `json:"snapshot_set_id"`
	BusinessAt           time.Time `json:"business_at"`
}

// OperatorEvaluation is the persisted deterministic result plus its frozen
// input. Human decisions remain in the existing decision flow.
type OperatorEvaluation struct {
	CaseID               string                 `json:"case_id"`
	EvaluationID         string                 `json:"evaluation_id"`
	PackID               string                 `json:"pack_id"`
	PackVersion          string                 `json:"pack_version"`
	SourceReviewID       string                 `json:"source_review_id"`
	SourceAttemptID      string                 `json:"source_attempt_id"`
	ContractVersionID    string                 `json:"contract_version_id"`
	RulePackVersion      string                 `json:"rule_pack_version"`
	ObjectBindingVersion string                 `json:"object_binding_version"`
	SnapshotSetID        string                 `json:"snapshot_set_id"`
	BusinessAt           time.Time              `json:"business_at"`
	MachineStatus        string                 `json:"machine_status"`
	Checks               []auditext.CheckResult `json:"checks"`
	CreatedAt            time.Time              `json:"created_at"`
	Input                auditext.FrozenInput   `json:"-"`
	ResultSHA256         string                 `json:"-"`
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
	tx, err := s.pool.Begin(ctx)
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
	key := strings.TrimSpace(idempotencyKey)
	if key == "" {
		return OperatorObjectBinding{}, fault.Invalid("缺少幂等键")
	}
	current, _, item, _, _, err := s.operatorCaseState(ctx, user, caseID, access.Manage)
	if err != nil {
		return OperatorObjectBinding{}, err
	}
	if item.Scenario != "telecom_dedicated_line" {
		return OperatorObjectBinding{}, fault.Conflict("当前案件场景不支持专线跨系统核验")
	}
	if !input.Confirmed {
		return OperatorObjectBinding{}, fault.Invalid("必须显式确认合同对象与外部对象的映射")
	}
	if strings.TrimSpace(input.ContractObject.ObjectType) == "" ||
		auditext.ValidateID(input.ContractObject.ObjectID) != nil ||
		auditext.ValidateID(input.ExternalObject.ConnectorID) != nil ||
		auditext.ValidateID(input.ExternalObject.ExternalObjectID) != nil {
		return OperatorObjectBinding{}, fault.Invalid("合同对象或外部对象标识不合法")
	}
	if input.ContractObject.Label != nil && len(*input.ContractObject.Label) > 256 {
		return OperatorObjectBinding{}, fault.Invalid("合同对象名称过长")
	}
	from, until := input.EffectiveFrom, input.EffectiveUntil
	if from.IsZero() {
		from = time.Now().UTC()
	}
	if until.IsZero() {
		until = from.AddDate(1, 0, 0)
	}
	if !from.Before(until) {
		return OperatorObjectBinding{}, fault.Invalid("映射有效区间不合法")
	}
	bindingID, err := newID("bind_")
	if err != nil {
		return OperatorObjectBinding{}, err
	}
	requestSHA, err := operatorHash(struct {
		CaseID         string
		ContractObject OperatorContractObject
		ExternalObject OperatorExternalObject
		From, Until    time.Time
		Confirmed      bool
	}{caseID, input.ContractObject, input.ExternalObject, from, until, true})
	if err != nil {
		return OperatorObjectBinding{}, err
	}
	var out OperatorObjectBinding
	err = s.withOperatorTx(ctx, user, caseID, true, func(tx pgx.Tx) error {
		existing, err := loadOperatorBindingByKey(ctx, tx, item.TenantID, item.OwnerOrgID, caseID, current.ID, key)
		if err == nil {
			if existing.requestSHA256 != requestSHA {
				return fault.Conflict("同一幂等键对应了不同的对象映射")
			}
			out = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		contractObj, err := json.Marshal(input.ContractObject)
		if err != nil {
			return err
		}
		externalObj, err := json.Marshal(input.ExternalObject)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		_, err = tx.Exec(ctx, `INSERT INTO operator_object_bindings
			(tenant_id,org_id,case_id,binding_id,binding_version,status,contract_object,external_object,
			 connector_id,external_object_id,source_system,confirmed_by,confirmed_at,effective_from,effective_until,
			 actor_id,idempotency_key,request_sha256,created_at)
			VALUES ($1,$2,$3,$4,$4,'confirmed',$5,$6,$7,$8,'',$9,$10,$11,$12,$9,$13,$14,$10)`,
			item.TenantID, item.OwnerOrgID, caseID, bindingID, contractObj, externalObj,
			input.ExternalObject.ConnectorID, input.ExternalObject.ExternalObjectID, current.ID, now, from, until, key, requestSHA)
		if err != nil {
			return err
		}
		out = OperatorObjectBinding{
			CaseID: caseID, BindingID: bindingID, Version: bindingID, Status: "confirmed",
			ContractObject: input.ContractObject, ExternalObject: input.ExternalObject,
			ConfirmedBy: current.ID, ConfirmedAt: now, EffectiveFrom: from, EffectiveUntil: until,
			CreatedAt: now, requestSHA256: requestSHA,
		}
		return nil
	})
	return out, err
}

// CollectOperatorExternalSnapshot performs one bounded read and persists the
// normalized observation as an immutable snapshot set. Collection is separate
// from evaluation acceptance: a failed read is not a conclusion.
func (s *Store) CollectOperatorExternalSnapshot(ctx context.Context, user accounts.User, caseID, bindingVersion string, businessAt time.Time, connectorID, externalObjectID string, connector auditext.ReadConnector, idempotencyKey string) (OperatorSnapshotSet, error) {
	key := strings.TrimSpace(idempotencyKey)
	if key == "" {
		return OperatorSnapshotSet{}, fault.Invalid("缺少幂等键")
	}
	if connector == nil {
		return OperatorSnapshotSet{}, fault.New(503, "external_evidence_unavailable", "外部证据连接器未配置")
	}
	if !operatorValidTimestamp(businessAt) {
		return OperatorSnapshotSet{}, fault.Invalid("业务时间不合法")
	}
	current, _, item, _, _, err := s.operatorCaseState(ctx, user, caseID, access.Review)
	if err != nil {
		return OperatorSnapshotSet{}, err
	}
	if item.Scenario != "telecom_dedicated_line" {
		return OperatorSnapshotSet{}, fault.Conflict("当前案件场景不支持专线跨系统核验")
	}
	binding, err := s.operatorReadBinding(ctx, user, caseID, bindingVersion)
	if err != nil {
		return OperatorSnapshotSet{}, err
	}
	if binding.Status != "confirmed" {
		return OperatorSnapshotSet{}, fault.Conflict("对象映射尚未确认，不能采集外部证据")
	}
	if connectorID != binding.ExternalObject.ConnectorID || externalObjectID != binding.ExternalObject.ExternalObjectID {
		return OperatorSnapshotSet{}, fault.Conflict("采集请求与已确认的对象映射不一致")
	}
	scope := auditext.AuthorizedScope{
		TenantID: item.TenantID, CaseID: caseID, ActorID: current.ID,
		GrantVersion: operatorGrantVersion(item, current),
	}
	observation, err := connector.Read(ctx, auditext.ReadRequest{
		Scope: scope, ConnectorID: binding.ExternalObject.ConnectorID,
		BindingVersion: binding.Version, ExternalObjectID: binding.ExternalObject.ExternalObjectID,
		BusinessAt: businessAt,
	})
	if err != nil {
		switch {
		case errors.Is(err, auditext.ErrUnauthorized):
			return OperatorSnapshotSet{}, fault.Forbidden("外部来源拒绝了本次读取")
		case errors.Is(err, auditext.ErrInvalidRequest):
			return OperatorSnapshotSet{}, fault.Invalid("外部读取请求不合法")
		default:
			return OperatorSnapshotSet{}, fault.New(503, "external_evidence_unavailable", "外部证据读取未完成")
		}
	}
	if err := observation.Validate(scope); err != nil {
		if errors.Is(err, auditext.ErrUnauthorized) {
			return OperatorSnapshotSet{}, fault.Forbidden("外部证据不属于当前租户或案件")
		}
		return OperatorSnapshotSet{}, fault.New(502, "external_evidence_invalid", "外部证据格式未通过校验")
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
	requestSHA, err := operatorHash(struct {
		CaseID, BindingVersion, ConnectorID, ExternalObjectID string
		BusinessAt                                            time.Time
	}{caseID, bindingVersion, binding.ExternalObject.ConnectorID, binding.ExternalObject.ExternalObjectID, businessAt})
	if err != nil {
		return OperatorSnapshotSet{}, err
	}
	observationJSON, err := json.Marshal(observation)
	if err != nil {
		return OperatorSnapshotSet{}, err
	}
	sum := sha256.Sum256(observationJSON)
	observationSHA := hex.EncodeToString(sum[:])
	setSum := sha256.Sum256(append(observationJSON, []byte(requestSHA)...))
	setSHA := hex.EncodeToString(setSum[:])
	fieldsJSON, err := json.Marshal(observation.Fields)
	if err != nil {
		return OperatorSnapshotSet{}, err
	}
	var out OperatorSnapshotSet
	err = s.withOperatorTx(ctx, user, caseID, true, func(tx pgx.Tx) error {
		existing, err := loadOperatorSnapshotSetByKey(ctx, tx, item.TenantID, item.OwnerOrgID, caseID, current.ID, key)
		if err == nil {
			if existing.Status != string(observation.Availability) {
				return fault.Conflict("同一幂等键对应了不同的采集结果")
			}
			out = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		now := time.Now().UTC()
		if _, err := tx.Exec(ctx, `INSERT INTO operator_external_snapshot_sets
			(tenant_id,org_id,case_id,snapshot_set_id,binding_version,connector_id,external_object_id,source_system,
			 business_at,status,reason_code,collected_by,collected_at,observation_count,set_sha256,actor_id,idempotency_key,request_sha256,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,1,$14,$12,$15,$16,$13)`,
			item.TenantID, item.OwnerOrgID, caseID, snapshotSetID, binding.Version, observation.ConnectorID,
			observation.ExternalObjectID, observation.SourceSystem, businessAt, string(observation.Availability),
			observation.ReasonCode, current.ID, now, setSHA, key, requestSHA); err != nil {
			return err
		}
		var recordVersion, payloadRef any
		if observation.RecordVersion != nil {
			recordVersion = *observation.RecordVersion
		}
		if observation.PayloadRef != nil {
			payloadRef = *observation.PayloadRef
		}
		var sourceUpdated any
		if observation.SourceUpdatedAt != nil {
			sourceUpdated = *observation.SourceUpdatedAt
		}
		if _, err := tx.Exec(ctx, `INSERT INTO operator_external_observations
			(tenant_id,org_id,case_id,snapshot_set_id,snapshot_id,record_id,record_version,observed_at,source_updated_at,
			 effective_from,effective_until,availability,reason_code,payload_ref,fields,data_class,observation_json,observation_sha256,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
			item.TenantID, item.OwnerOrgID, caseID, snapshotSetID, observation.SnapshotID, observation.RecordID,
			recordVersion, observation.ObservedAt, sourceUpdated, observation.EffectiveFrom, observation.EffectiveUntil,
			string(observation.Availability), observation.ReasonCode, payloadRef, fieldsJSON, observation.DataClass,
			observationJSON, observationSHA, now); err != nil {
			return err
		}
		out = OperatorSnapshotSet{
			CaseID: caseID, SnapshotSetID: snapshotSetID, BindingVersion: binding.Version,
			ConnectorID: observation.ConnectorID, ExternalObjectID: observation.ExternalObjectID,
			SourceSystem: observation.SourceSystem, BusinessAt: businessAt,
			Status: string(observation.Availability), ReasonCode: observation.ReasonCode,
			CollectedBy: current.ID, CollectedAt: now, Observations: []auditext.Observation{observation},
		}
		return nil
	})
	return out, err
}

func (s *Store) operatorReadBinding(ctx context.Context, user accounts.User, caseID, bindingVersion string) (OperatorObjectBinding, error) {
	var out OperatorObjectBinding
	err := s.withOperatorTx(ctx, user, caseID, false, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT case_id,binding_id,binding_version,status,contract_object,external_object,
			source_system,confirmed_by,confirmed_at,effective_from,effective_until,created_at,request_sha256
			FROM operator_object_bindings WHERE tenant_id=current_setting('app.tenant_id',true)
			AND org_id=current_setting('app.org_id',true) AND case_id=$1 AND binding_version=$2`,
			caseID, bindingVersion).Scan(&out.CaseID, &out.BindingID, &out.Version, &out.Status,
			&out.ContractObject, &out.ExternalObject, &out.SourceSystem, &out.ConfirmedBy, &out.ConfirmedAt,
			&out.EffectiveFrom, &out.EffectiveUntil, &out.CreatedAt, &out.requestSHA256)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.NotFound("没有找到对象映射")
	}
	return out, err
}

func (s *Store) ListOperatorExternalSnapshots(ctx context.Context, user accounts.User, caseID string) ([]OperatorSnapshotSet, error) {
	if err := s.operatorReady(false); err != nil {
		return nil, err
	}
	if _, _, _, _, _, err := s.operatorCaseState(ctx, user, caseID, access.Read); err != nil {
		return nil, err
	}
	var out []OperatorSnapshotSet
	err := s.withOperatorTx(ctx, user, caseID, false, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT case_id,snapshot_set_id,binding_version,connector_id,external_object_id,source_system,
			business_at,status,reason_code,collected_by,collected_at
			FROM operator_external_snapshot_sets WHERE tenant_id=current_setting('app.tenant_id',true)
			AND org_id=current_setting('app.org_id',true) AND case_id=$1
			ORDER BY created_at DESC, snapshot_set_id`, caseID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var set OperatorSnapshotSet
			if err := rows.Scan(&set.CaseID, &set.SnapshotSetID, &set.BindingVersion, &set.ConnectorID,
				&set.ExternalObjectID, &set.SourceSystem, &set.BusinessAt, &set.Status, &set.ReasonCode,
				&set.CollectedBy, &set.CollectedAt); err != nil {
				return err
			}
			out = append(out, set)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for index := range out {
			observations, err := loadOperatorObservations(ctx, tx, caseID, out[index].SnapshotSetID)
			if err != nil {
				return err
			}
			out[index].Observations = observations
		}
		return nil
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
	key := strings.TrimSpace(idempotencyKey)
	if key == "" {
		return OperatorEvaluation{}, fault.Invalid("缺少幂等键")
	}
	if evaluator == nil {
		return OperatorEvaluation{}, fault.New(503, "fulfillment_unavailable", "履约核验器未配置")
	}
	if input.PackID != pack.PackID || input.PackVersion != pack.Version {
		return OperatorEvaluation{}, fault.Conflict("能力包版本与请求不一致")
	}
	if !operatorValidTimestamp(input.BusinessAt) {
		return OperatorEvaluation{}, fault.Invalid("业务时间不合法")
	}
	current, st, item, principal, resource, err := s.operatorCaseState(ctx, user, caseID, access.Review)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	if item.Scenario != pack.Scenario {
		return OperatorEvaluation{}, fault.Conflict("能力包场景与案件不一致")
	}
	binding, err := s.operatorReadBinding(ctx, user, caseID, input.ObjectBindingVersion)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	if binding.Status != "confirmed" {
		return OperatorEvaluation{}, fault.Conflict("对象映射尚未确认")
	}
	snapshot, err := s.operatorReadSnapshotSet(ctx, user, caseID, input.SnapshotSetID)
	if err != nil {
		return OperatorEvaluation{}, err
	}
	if snapshot.BindingVersion != binding.Version {
		return OperatorEvaluation{}, fault.Conflict("快照集不属于该对象映射")
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
	rulePackVersion := attempt.RulePackVersion
	if rulePackVersion == "" {
		rulePackVersion = pack.RulePackRef.Version
	}
	if rulePackVersion != pack.RulePackRef.Version ||
		(attempt.RulePackID != "" && attempt.RulePackID != pack.RulePackRef.ID) {
		return OperatorEvaluation{}, fault.Conflict("来源审查使用的规则包与能力包不一致")
	}
	sources, err := telecomSources(interpretedFromFrozen(attemptInput.Documents))
	if err != nil {
		return OperatorEvaluation{}, fault.Conflict("冻结合同输入未通过完整性校验")
	}
	request := rulepack.Request{
		Principal: principal, Resource: resource, BusinessLine: item.BusinessLine,
		Scenario: item.Scenario, ContractType: item.ContractType, At: input.BusinessAt, Sources: sources,
	}
	facts, err := telecom.Extract(request)
	if err != nil {
		return OperatorEvaluation{}, fault.Conflict("合同阈值无法从冻结原文中确定性提取")
	}
	frozenFacts, err := operatorFacts(facts)
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
		Facts:                  frozenFacts,
	}
	if err := frozen.Validate(); err != nil {
		return OperatorEvaluation{}, fault.Conflict("冻结核验输入不完整")
	}
	checks, err := evaluator.Evaluate(ctx, frozen, snapshot.Observations)
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
			if existing.ResultSHA256 != resultSHA {
				return fault.Conflict("同一幂等键对应了不同的核验结果")
			}
			out = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		now := time.Now().UTC()
		if _, err := tx.Exec(ctx, `INSERT INTO operator_fulfillment_evaluations
			(tenant_id,org_id,case_id,evaluation_id,pack_id,pack_version,source_run_id,source_attempt_id,
			 contract_version_id,rule_pack_version,binding_version,snapshot_set_id,business_at,input_json,input_sha256,
			 result_json,result_sha256,machine_status,created_by,actor_id,idempotency_key,request_sha256,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$19,$20,$21,clock_timestamp())`,
			item.TenantID, item.OwnerOrgID, caseID, evaluationID, pack.PackID, pack.Version,
			input.SourceReviewID, input.SourceAttemptID, input.ContractVersionID, rulePackVersion,
			binding.Version, snapshot.SnapshotSetID, input.BusinessAt, inputJSON, requestSHA, resultJSON, resultSHA,
			machineStatus, current.ID, key, requestSHA); err != nil {
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
			BusinessAt: input.BusinessAt, MachineStatus: machineStatus, Checks: checks,
			CreatedAt: now, Input: frozen, ResultSHA256: resultSHA,
		}
		return nil
	})
	return out, err
}

func (s *Store) GetOperatorFulfillmentEvaluation(ctx context.Context, user accounts.User, caseID, evaluationID string) (OperatorEvaluation, error) {
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
		if frozen.Document.ID == contractVersion && frozen.Document.Role == "draft_contract" {
			return true
		}
	}
	return false
}

func operatorFacts(facts []rulepack.Fact) ([]auditext.Fact, error) {
	var out []auditext.Fact
	for _, fact := range facts {
		if fact.Dimension != "minimum_bandwidth" {
			continue
		}
		evidence := make([]auditext.EvidenceRef, 0, len(fact.Citations))
		for _, citation := range fact.Citations {
			evidence = append(evidence, auditext.EvidenceRef{
				Kind: auditext.EvidenceDocument, DocumentVersionID: citation.SourceID,
				ParsedVersionID: citation.SourceVersion, Start: citation.Start, End: citation.End, Quote: citation.Quote,
			})
		}
		out = append(out, auditext.Fact{
			Name: "minimum_bandwidth", Value: auditext.ExactQuantity{Value: fact.Value, Unit: fact.Unit},
			Evidence: evidence, SemanticKind: "configured_value",
		})
	}
	if len(out) > 1 {
		return nil, fault.Conflict("冻结合同中有多个带宽约定，无法唯一核验")
	}
	return out, nil
}

func loadOperatorBindingByKey(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, actorID, key string) (OperatorObjectBinding, error) {
	var out OperatorObjectBinding
	err := tx.QueryRow(ctx, `SELECT case_id,binding_id,binding_version,status,contract_object,external_object,
		source_system,confirmed_by,confirmed_at,effective_from,effective_until,created_at,request_sha256
		FROM operator_object_bindings WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`,
		tenantID, orgID, caseID, actorID, key).Scan(&out.CaseID, &out.BindingID, &out.Version, &out.Status,
		&out.ContractObject, &out.ExternalObject, &out.SourceSystem, &out.ConfirmedBy, &out.ConfirmedAt,
		&out.EffectiveFrom, &out.EffectiveUntil, &out.CreatedAt, &out.requestSHA256)
	return out, err
}

func loadOperatorSnapshotSetByKey(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, actorID, key string) (OperatorSnapshotSet, error) {
	var out OperatorSnapshotSet
	err := tx.QueryRow(ctx, `SELECT case_id,snapshot_set_id,binding_version,connector_id,external_object_id,source_system,
		business_at,status,reason_code,collected_by,collected_at
		FROM operator_external_snapshot_sets WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`,
		tenantID, orgID, caseID, actorID, key).Scan(&out.CaseID, &out.SnapshotSetID, &out.BindingVersion,
		&out.ConnectorID, &out.ExternalObjectID, &out.SourceSystem, &out.BusinessAt, &out.Status, &out.ReasonCode,
		&out.CollectedBy, &out.CollectedAt)
	if err != nil {
		return out, err
	}
	out.Observations, err = loadOperatorObservations(ctx, tx, caseID, out.SnapshotSetID)
	return out, err
}

func (s *Store) operatorReadSnapshotSet(ctx context.Context, user accounts.User, caseID, snapshotSetID string) (OperatorSnapshotSet, error) {
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
	var out OperatorSnapshotSet
	err := tx.QueryRow(ctx, `SELECT case_id,snapshot_set_id,binding_version,connector_id,external_object_id,source_system,
		business_at,status,reason_code,collected_by,collected_at
		FROM operator_external_snapshot_sets WHERE tenant_id=current_setting('app.tenant_id',true)
		AND org_id=current_setting('app.org_id',true) AND case_id=$1 AND snapshot_set_id=$2`,
		caseID, snapshotSetID).Scan(&out.CaseID, &out.SnapshotSetID, &out.BindingVersion,
		&out.ConnectorID, &out.ExternalObjectID, &out.SourceSystem, &out.BusinessAt, &out.Status, &out.ReasonCode,
		&out.CollectedBy, &out.CollectedAt)
	if err != nil {
		return out, err
	}
	out.Observations, err = loadOperatorObservations(ctx, tx, caseID, out.SnapshotSetID)
	return out, err
}

func loadOperatorObservations(ctx context.Context, tx pgx.Tx, caseID, snapshotSetID string) ([]auditext.Observation, error) {
	rows, err := tx.Query(ctx, `SELECT observation_json FROM operator_external_observations
		WHERE tenant_id=current_setting('app.tenant_id',true) AND org_id=current_setting('app.org_id',true)
		AND case_id=$1 AND snapshot_set_id=$2 ORDER BY snapshot_id`, caseID, snapshotSetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auditext.Observation
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var observation auditext.Observation
		if err := json.Unmarshal(raw, &observation); err != nil {
			return nil, err
		}
		out = append(out, observation)
	}
	return out, rows.Err()
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
		contract_version_id,rule_pack_version,binding_version,snapshot_set_id,business_at,input_json,result_json,
		result_sha256,machine_status,created_at
		FROM operator_fulfillment_evaluations WHERE tenant_id=current_setting('app.tenant_id',true)
		AND org_id=current_setting('app.org_id',true) AND case_id=$1 AND evaluation_id=$2`,
		caseID, evaluationID).Scan(&out.CaseID, &out.EvaluationID, &out.PackID, &out.PackVersion,
		&out.SourceReviewID, &out.SourceAttemptID, &out.ContractVersionID, &out.RulePackVersion,
		&out.ObjectBindingVersion, &out.SnapshotSetID, &out.BusinessAt, &inputJSON, &resultJSON,
		&out.ResultSHA256, &out.MachineStatus, &out.CreatedAt)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(inputJSON, &out.Input); err != nil {
		return out, err
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
	return out, nil
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
