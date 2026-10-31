package store

import (
	"bytes"
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
	// ServiceObjectID and the two expected versions pin a new operator mapping
	// to the exact confirmed object identity and contract association. They are
	// optional only so an already persisted legacy idempotency request can be
	// replayed byte-for-byte; fresh writes require all three values.
	ServiceObjectID            string    `json:"service_object_id,omitempty"`
	ExpectedObjectVersion      int       `json:"expected_object_version,omitempty"`
	ExpectedAssociationVersion int       `json:"expected_association_version,omitempty"`
	EffectiveFrom              time.Time `json:"effective_from"`
	EffectiveUntil             time.Time `json:"effective_until"`
	Confirmed                  bool      `json:"confirmed"`
}

// OperatorObjectBinding is the immutable persisted mapping.
type OperatorObjectBinding struct {
	CaseID             string                 `json:"case_id"`
	BindingID          string                 `json:"binding_id"`
	Version            string                 `json:"version"`
	Status             string                 `json:"status"`
	ContractObject     OperatorContractObject `json:"contract_object"`
	ExternalObject     OperatorExternalObject `json:"external_object"`
	SourceSystem       string                 `json:"source_system"`
	ConfirmedBy        string                 `json:"confirmed_by"`
	ConfirmedAt        time.Time              `json:"confirmed_at"`
	EffectiveFrom      time.Time              `json:"effective_from"`
	EffectiveUntil     time.Time              `json:"effective_until"`
	CreatedAt          time.Time              `json:"created_at"`
	MappingID          string                 `json:"mapping_id,omitempty"`
	MappingVersion     int64                  `json:"mapping_version,omitempty"`
	ServiceObjectID    string                 `json:"service_object_id,omitempty"`
	ObjectVersion      int                    `json:"object_version,omitempty"`
	AssociationVersion int                    `json:"association_version,omitempty"`
	requestSHA256      string
}

// OperatorSnapshotSet is one immutable collection result. A failed read is
// persisted as unavailable/rejected evidence, never as a successful snapshot.
type OperatorSnapshotSet struct {
	CaseID           string                 `json:"case_id"`
	CollectionID     string                 `json:"-"`
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
	requestSHA256    string                 `json:"-"`
}

// OperatorCollectionJob exposes only the caller's own durable collection
// claim. It supports refresh/recovery without making the claim table a general
// cross-service workflow API.
type OperatorCollectionJob struct {
	CollectionID  string               `json:"collection_id"`
	CaseID        string               `json:"case_id"`
	State         string               `json:"state"`
	RetryAfter    *time.Time           `json:"retry_after,omitempty"`
	SnapshotSetID *string              `json:"snapshot_set_id,omitempty"`
	SnapshotSet   *OperatorSnapshotSet `json:"snapshot_set,omitempty"`
}

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
	ObjectRef          string                    `json:"object_ref"`
	Source             *OperatorEvaluationSource `json:"source"`
	ServiceObjectID    *string                   `json:"service_object_id"`
	ObjectVersion      *int                      `json:"object_version"`
	AssociationVersion *int                      `json:"association_version"`
	AssociationRunID     string                  `json:"association_run_id,omitempty"`
	AssociationAttemptID string                  `json:"association_attempt_id,omitempty"`
	MappingID          *string                   `json:"mapping_id"`
	MappingVersion     *int64                    `json:"mapping_version"`
	BindingVersion     *string                   `json:"binding_version"`
	SnapshotSetID      *string                   `json:"snapshot_set_id"`
	Status             string                    `json:"status"`
	ReasonCode         string                    `json:"reason_code"`
	FactsSHA256        string                    `json:"facts_sha256,omitempty"`
	FactRefs            []ServiceObjectFactRef    `json:"fact_refs"`
	SnapshotCollectedAt *time.Time                `json:"snapshot_collected_at,omitempty"`
	Facts              []auditext.Fact           `json:"facts"`
	Checks             []auditext.CheckResult    `json:"checks"`
}

type OperatorEvaluationSource struct {
	RunID            string `json:"run_id"`
	AttemptID        string `json:"attempt_id"`
	AssociationRunID     string `json:"association_run_id,omitempty"`
	AssociationAttemptID string `json:"association_attempt_id,omitempty"`
	DocumentID       string `json:"document_id"`
	ParsedDocumentID string `json:"parsed_document_id"`
	MaterialID       string `json:"material_id"`
	MaterialVersion  int    `json:"material_version"`
	Purpose          string `json:"purpose"`
	Role             string `json:"role"`
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
	legacyRequestSHA, err := operatorHash(struct {
		CaseID         string
		ContractObject OperatorContractObject
		ExternalObject OperatorExternalObject
		From, Until    time.Time
		Confirmed      bool
	}{caseID, input.ContractObject, input.ExternalObject, from, until, true})
	if err != nil {
		return OperatorObjectBinding{}, err
	}
	requestSHA := legacyRequestSHA
	if input.ServiceObjectID != "" || input.ExpectedObjectVersion != 0 || input.ExpectedAssociationVersion != 0 {
		requestSHA, err = operatorHash(struct {
			CaseID                     string
			ContractObject             OperatorContractObject
			ExternalObject             OperatorExternalObject
			From, Until                time.Time
			Confirmed                  bool
			ServiceObjectID            string
			ExpectedObjectVersion      int
			ExpectedAssociationVersion int
		}{caseID, input.ContractObject, input.ExternalObject, from, until, true,
			input.ServiceObjectID, input.ExpectedObjectVersion, input.ExpectedAssociationVersion})
		if err != nil {
			return OperatorObjectBinding{}, err
		}
	}
	var out OperatorObjectBinding
	err = s.withOperatorTx(ctx, user, caseID, true, func(tx pgx.Tx) error {
		existing, err := loadOperatorBindingByKey(ctx, tx, item.TenantID, item.OwnerOrgID, caseID, current.ID, key)
		if err == nil {
			// Old unpinned requests remain replayable only when this exact legacy
			// idempotency record already exists. They cannot create new mappings.
			matches := existing.requestSHA256 == requestSHA ||
				(input.ServiceObjectID == "" && input.ExpectedObjectVersion == 0 && input.ExpectedAssociationVersion == 0 && existing.requestSHA256 == legacyRequestSHA)
			if !matches {
				return fault.Conflict("同一幂等键对应了不同的对象映射")
			}
			out = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if strings.TrimSpace(input.ServiceObjectID) == "" || input.ExpectedObjectVersion < 1 || input.ExpectedAssociationVersion < 1 {
			return fault.Conflict("新建对象映射必须绑定已确认的服务对象和合同关联版本")
		}
		head, err := loadServiceObjectHead(ctx, tx, item.TenantID, item.OwnerOrgID, caseID, input.ServiceObjectID)
		if err != nil {
			return err
		}
		if head.CurrentVersion != input.ExpectedObjectVersion || head.CurrentAssociationVersion != input.ExpectedAssociationVersion {
			return fault.Conflict("服务对象身份或合同关联已变化，请刷新后重试")
		}
		identity, err := loadServiceObjectVersionValue(ctx, tx, item.TenantID, item.OwnerOrgID, caseID, head)
		if err != nil {
			return err
		}
		association, err := loadServiceObjectAssociation(ctx, tx, item.TenantID, item.OwnerOrgID, caseID, input.ServiceObjectID, head.CurrentAssociationVersion)
		if err != nil {
			return err
		}
		if identity.State != ServiceObjectConfirmed || association.ObjectVersion != input.ExpectedObjectVersion ||
			association.AssociationVersion != input.ExpectedAssociationVersion || association.ObjectRef != input.ContractObject.ObjectID {
			return fault.Conflict("合同对象与已确认的服务对象关联不一致")
		}
		mappingID, err := newID("omap_")
		if err != nil {
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
		// PostgreSQL timestamptz persists microseconds. Normalize before both
		// constructing the first response and storing it so replay returns the
		// exact same JSON bytes.
		now := time.Now().UTC().Truncate(time.Microsecond)
		out = OperatorObjectBinding{
			CaseID: caseID, BindingID: bindingID, Version: bindingID, Status: "confirmed",
			ContractObject: input.ContractObject, ExternalObject: input.ExternalObject,
			ConfirmedBy: current.ID, ConfirmedAt: now, EffectiveFrom: from, EffectiveUntil: until,
			CreatedAt: now, MappingID: mappingID, MappingVersion: 1, ServiceObjectID: input.ServiceObjectID,
			ObjectVersion: input.ExpectedObjectVersion, AssociationVersion: input.ExpectedAssociationVersion, requestSHA256: requestSHA,
		}
		responseBody, err := json.Marshal(out)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO operator_object_bindings
			(tenant_id,org_id,case_id,binding_id,binding_version,status,contract_object,external_object,
			 connector_id,external_object_id,source_system,confirmed_by,confirmed_at,effective_from,effective_until,
			 actor_id,idempotency_key,request_sha256,created_at,mapping_id,mapping_version,service_object_id,object_version,association_version,
			 response_status,response_body)
			VALUES ($1,$2,$3,$4,$4,'confirmed',$5,$6,$7,$8,'',$9,$10,$11,$12,$9,$13,$14,$10,$15,1,$16,$17,$18,201,$19)`,
			item.TenantID, item.OwnerOrgID, caseID, bindingID, contractObj, externalObj,
			input.ExternalObject.ConnectorID, input.ExternalObject.ExternalObjectID, current.ID, now, from, until, key, requestSHA,
			mappingID, input.ServiceObjectID, input.ExpectedObjectVersion, input.ExpectedAssociationVersion, responseBody)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO operator_mapping_lineages
			(tenant_id,org_id,case_id,mapping_id,service_object_id,current_version,current_binding_version,created_by,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,1,$6,$7,$8,$8)`,
			item.TenantID, item.OwnerOrgID, caseID, mappingID, input.ServiceObjectID, bindingID, current.ID, now); err != nil {
			return err
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
	if !operatorValidTimestamp(businessAt) {
		return OperatorSnapshotSet{}, fault.Invalid("业务时间不合法")
	}
	businessAt = businessAt.UTC().Truncate(time.Microsecond)
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
	// A confirmed binding is still time-bounded evidence. Do not let callers
	// use today's mapping to imply that the external object was associated at
	// an unrelated historical business time.
	if businessAt.Before(binding.EffectiveFrom) || !businessAt.Before(binding.EffectiveUntil) {
		return OperatorSnapshotSet{}, fault.Conflict("业务时间不在已确认对象映射的有效期间内")
	}
	if connectorID != binding.ExternalObject.ConnectorID || externalObjectID != binding.ExternalObject.ExternalObjectID {
		return OperatorSnapshotSet{}, fault.Conflict("采集请求与已确认的对象映射不一致")
	}
	requestSHA, err := operatorHash(struct {
		TenantID, OrgID, CaseID, ActorID, BindingVersion, ConnectorID, ExternalObjectID string
		BusinessAt                                                                      time.Time
	}{item.TenantID, item.OwnerOrgID, caseID, current.ID, binding.Version,
		binding.ExternalObject.ConnectorID, binding.ExternalObject.ExternalObjectID, businessAt})
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
		var storedSHA, status string
		var storedCollectionID string
		var owner *string
		var leaseUntil *time.Time
		var resultID *string
		err = tx.QueryRow(ctx, `SELECT collection_id,request_sha256,generation,lease_owner,lease_until,status,result_snapshot_set_id
			FROM operator_external_collection_claims WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3
			AND actor_id=$4 AND idempotency_key=$5 FOR UPDATE`, item.TenantID, item.OwnerOrgID, caseID, current.ID, key).
			Scan(&storedCollectionID, &storedSHA, &generation, &owner, &leaseUntil, &status, &resultID)
		if errors.Is(err, pgx.ErrNoRows) {
			// Preserve replay behavior for rows written before the fenced-claim
			// table existed. The old immutable result is authoritative.
			existing, legacyErr := loadOperatorSnapshotSetByKey(ctx, tx, item.TenantID, item.OwnerOrgID, caseID, current.ID, key)
			if legacyErr == nil {
				legacySHA, hashErr := operatorHash(struct {
					CaseID, BindingVersion, ConnectorID, ExternalObjectID string
					BusinessAt                                            time.Time
				}{caseID, binding.Version, binding.ExternalObject.ConnectorID, binding.ExternalObject.ExternalObjectID, businessAt})
				if hashErr != nil {
					return hashErr
				}
				if existing.requestSHA256 != legacySHA {
					return fault.Conflict("同一幂等键对应了不同的采集请求")
				}
				body, marshalErr := json.Marshal(existing)
				if marshalErr != nil {
					return marshalErr
				}
				generation = 1
				out, alreadyComplete = existing, true
				out.CollectionID = collectionID
				_, err = tx.Exec(ctx, `INSERT INTO operator_external_collection_claims
					(tenant_id,org_id,case_id,actor_id,idempotency_key,collection_id,request_sha256,generation,status,
					 result_snapshot_set_id,response_status,response_body)
					VALUES ($1,$2,$3,$4,$5,$6,$7,1,'completed',$8,201,$9)`,
					item.TenantID, item.OwnerOrgID, caseID, current.ID, key, collectionID, requestSHA, existing.SnapshotSetID, body)
				return err
			}
			if !errors.Is(legacyErr, pgx.ErrNoRows) {
				return legacyErr
			}
			if connector == nil {
				return fault.New(503, "external_evidence_unavailable", "外部证据连接器未配置")
			}
			if err := businessWritable(item); err != nil {
				return err
			}
			generation = 1
			_, err = tx.Exec(ctx, `INSERT INTO operator_external_collection_claims
				(tenant_id,org_id,case_id,actor_id,idempotency_key,collection_id,request_sha256,generation,lease_owner,lease_until,status)
				VALUES ($1,$2,$3,$4,$5,$6,$7,1,$8,clock_timestamp()+interval '5 minutes','in_progress')`,
				item.TenantID, item.OwnerOrgID, caseID, current.ID, key, collectionID, requestSHA, leaseOwner)
			return err
		}
		if err != nil {
			return err
		}
		collectionID = storedCollectionID
		if storedSHA != requestSHA {
			return fault.Conflict("同一幂等键对应了不同的采集请求")
		}
		if status == "completed" {
			if resultID == nil {
				return fault.New(500, "integrity_error", "采集幂等回执缺少快照结果")
			}
			out, err = loadOperatorSnapshotSetByID(ctx, tx, caseID, *resultID)
			if err != nil {
				return fault.New(500, "integrity_error", "采集幂等回执引用的快照不存在")
			}
			out.CollectionID = collectionID
			alreadyComplete = true
			return nil
		}
		now := time.Now().UTC()
		if status == "in_progress" && owner != nil && *owner != "" && leaseUntil != nil && leaseUntil.After(now) {
			return fault.Conflict("相同采集请求仍在执行，请稍后重试")
		}
		if connector == nil {
			return fault.New(503, "external_evidence_unavailable", "外部证据连接器未配置")
		}
		if err := businessWritable(item); err != nil {
			return err
		}
		generation++
		_, err = tx.Exec(ctx, `UPDATE operator_external_collection_claims SET generation=$6,lease_owner=$7,
			lease_until=clock_timestamp()+interval '5 minutes',status='in_progress',updated_at=clock_timestamp()
			WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`,
			item.TenantID, item.OwnerOrgID, caseID, current.ID, key, generation, leaseOwner)
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
	observation, err := connector.Read(ctx, auditext.ReadRequest{
		Scope: scope, ConnectorID: binding.ExternalObject.ConnectorID,
		BindingVersion: binding.Version, ExternalObjectID: binding.ExternalObject.ExternalObjectID,
		BusinessAt: businessAt,
	})
	if err != nil {
		_ = s.releaseOperatorCollectionLease(ctx, user, item.TenantID, item.OwnerOrgID, caseID, current.ID, key, generation, leaseOwner)
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
		_ = s.releaseOperatorCollectionLease(ctx, user, item.TenantID, item.OwnerOrgID, caseID, current.ID, key, generation, leaseOwner)
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
	err = s.withOperatorTx(ctx, user, caseID, true, func(tx pgx.Tx) error {
		var storedGeneration int64
		var storedOwner *string
		var storedStatus string
		var leaseStillValid bool
		if err := tx.QueryRow(ctx, `SELECT generation,lease_owner,status,COALESCE(lease_until > clock_timestamp(),false) FROM operator_external_collection_claims
			WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5 FOR UPDATE`,
			item.TenantID, item.OwnerOrgID, caseID, current.ID, key).Scan(&storedGeneration, &storedOwner, &storedStatus, &leaseStillValid); err != nil {
			return err
		}
		if storedStatus != "in_progress" || !leaseStillValid || storedGeneration != generation || storedOwner == nil || *storedOwner != leaseOwner {
			return fault.Conflict("采集租约已被更新的请求接管")
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
		// timestamptz persists microseconds. Normalize before constructing the
		// response receipt so an idempotent replay returns identical JSON bytes.
		now := time.Now().UTC().Truncate(time.Microsecond)
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
			CaseID: caseID, CollectionID: collectionID, SnapshotSetID: snapshotSetID, BindingVersion: binding.Version,
			ConnectorID: observation.ConnectorID, ExternalObjectID: observation.ExternalObjectID,
			SourceSystem: observation.SourceSystem, BusinessAt: businessAt,
			Status: string(observation.Availability), ReasonCode: observation.ReasonCode,
			CollectedBy: current.ID, CollectedAt: now, Observations: []auditext.Observation{observation},
		}
		responseBody, err := json.Marshal(out)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE operator_external_collection_claims SET status='completed',lease_owner=NULL,lease_until=NULL,
			result_snapshot_set_id=$6,response_status=201,response_body=$7,updated_at=$8
			WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5
			AND generation=$9 AND lease_owner=$10 AND status='in_progress' AND lease_until>clock_timestamp()`,
			item.TenantID, item.OwnerOrgID, caseID, current.ID, key, snapshotSetID, responseBody, now, generation, leaseOwner)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fault.Conflict("采集租约已被更新的请求接管")
		}
		return nil
	})
	return out, err
}

func (s *Store) releaseOperatorCollectionLease(ctx context.Context, user accounts.User, tenantID, orgID, caseID, actorID, key string, generation int64, owner string) error {
	return s.withOperatorTx(ctx, user, caseID, true, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE operator_external_collection_claims SET status='retryable',lease_owner=NULL,
			lease_until=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4
			AND idempotency_key=$5 AND generation=$6 AND lease_owner=$7 AND status='in_progress'`,
			tenantID, orgID, caseID, actorID, key, generation, owner)
		return err
	})
}

func (s *Store) operatorReadBinding(ctx context.Context, user accounts.User, caseID, bindingVersion string) (OperatorObjectBinding, error) {
	var out OperatorObjectBinding
	err := s.withOperatorTx(ctx, user, caseID, false, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT case_id,binding_id,binding_version,status,contract_object,external_object,
			source_system,confirmed_by,confirmed_at,effective_from,effective_until,created_at,request_sha256,
			COALESCE(mapping_id,''),COALESCE(mapping_version,0),COALESCE(service_object_id,''),
			COALESCE(object_version,0),COALESCE(association_version,0)
			FROM operator_object_bindings WHERE tenant_id=current_setting('app.tenant_id',true)
			AND org_id=current_setting('app.org_id',true) AND case_id=$1 AND binding_version=$2`,
			caseID, bindingVersion).Scan(&out.CaseID, &out.BindingID, &out.Version, &out.Status,
			&out.ContractObject, &out.ExternalObject, &out.SourceSystem, &out.ConfirmedBy, &out.ConfirmedAt,
			&out.EffectiveFrom, &out.EffectiveUntil, &out.CreatedAt, &out.requestSHA256, &out.MappingID,
			&out.MappingVersion, &out.ServiceObjectID, &out.ObjectVersion, &out.AssociationVersion)
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
		out = append(out, auditext.Fact{
			Name: fact.Dimension, Value: auditext.ExactQuantity{Value: fact.Value, Unit: fact.Unit},
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
		Facts: append([]auditext.Fact{}, facts...), Checks: append([]auditext.CheckResult{}, checks...),
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

func loadOperatorBindingByKey(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, actorID, key string) (OperatorObjectBinding, error) {
	var out OperatorObjectBinding
	var receipt []byte
	err := tx.QueryRow(ctx, `SELECT case_id,binding_id,binding_version,status,contract_object,external_object,
		source_system,confirmed_by,confirmed_at,effective_from,effective_until,created_at,request_sha256,
		COALESCE(mapping_id,''),COALESCE(mapping_version,0),COALESCE(service_object_id,''),
		COALESCE(object_version,0),COALESCE(association_version,0),response_body
		FROM operator_object_bindings WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`,
		tenantID, orgID, caseID, actorID, key).Scan(&out.CaseID, &out.BindingID, &out.Version, &out.Status,
		&out.ContractObject, &out.ExternalObject, &out.SourceSystem, &out.ConfirmedBy, &out.ConfirmedAt,
		&out.EffectiveFrom, &out.EffectiveUntil, &out.CreatedAt, &out.requestSHA256, &out.MappingID,
		&out.MappingVersion, &out.ServiceObjectID, &out.ObjectVersion, &out.AssociationVersion, &receipt)
	if err == nil && len(receipt) > 0 {
		requestSHA := out.requestSHA256
		if err := json.Unmarshal(receipt, &out); err != nil {
			return OperatorObjectBinding{}, fault.New(500, "integrity_error", "对象映射幂等回执损坏")
		}
		out.requestSHA256 = requestSHA
	}
	return out, err
}

func loadOperatorSnapshotSetByKey(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, actorID, key string) (OperatorSnapshotSet, error) {
	var out OperatorSnapshotSet
	err := tx.QueryRow(ctx, `SELECT case_id,snapshot_set_id,binding_version,connector_id,external_object_id,source_system,
		business_at,status,reason_code,collected_by,collected_at,request_sha256
		FROM operator_external_snapshot_sets WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`,
		tenantID, orgID, caseID, actorID, key).Scan(&out.CaseID, &out.SnapshotSetID, &out.BindingVersion,
		&out.ConnectorID, &out.ExternalObjectID, &out.SourceSystem, &out.BusinessAt, &out.Status, &out.ReasonCode,
		&out.CollectedBy, &out.CollectedAt, &out.requestSHA256)
	if err != nil {
		return out, err
	}
	normalizeOperatorSnapshotSetTimes(&out)
	out.Observations, err = loadOperatorObservations(ctx, tx, caseID, out.SnapshotSetID)
	return out, err
}

func (s *Store) GetOperatorCollectionJob(ctx context.Context, user accounts.User, caseID, key string) (OperatorCollectionJob, error) {
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
	collectionID = strings.TrimSpace(collectionID)
	if !validOperatorCollectionID(collectionID) {
		return OperatorCollectionJob{}, fault.NotFound("没有找到采集请求")
	}
	return s.getOperatorCollectionJob(ctx, user, caseID, collectionID, false)
}

func validOperatorCollectionID(value string) bool {
	if !strings.HasPrefix(value, "col_") || (len(value) != 20 && len(value) != 36) {
		return false
	}
	for _, c := range value[4:] {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (s *Store) getOperatorCollectionJob(ctx context.Context, user accounts.User, caseID, selector string, byKey bool) (OperatorCollectionJob, error) {
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
		var leaseUntil *time.Time
		var resultID *string
		var leaseExpired bool
		var query string
		args := []any{freshCase.TenantID, freshCase.OwnerOrgID, caseID, selector}
		if byKey {
			query = `SELECT collection_id,status,lease_until,result_snapshot_set_id,COALESCE(lease_until <= clock_timestamp(),false) FROM operator_external_collection_claims
				WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`
			args = append(args[:3], freshActor.ID, selector)
		} else {
			query = `SELECT collection_id,status,lease_until,result_snapshot_set_id,COALESCE(lease_until <= clock_timestamp(),false) FROM operator_external_collection_claims
				WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND collection_id=$4`
		}
		if err := tx.QueryRow(ctx, query, args...).
			Scan(&out.CollectionID, &out.State, &leaseUntil, &resultID, &leaseExpired); err != nil {
			return err
		}
		if out.State == "in_progress" && leaseExpired {
			out.State = "retryable"
		}
		out.CaseID = caseID
		if out.State == "in_progress" && leaseUntil != nil {
			retryAfter := leaseUntil.UTC()
			out.RetryAfter = &retryAfter
		}
		if out.State == "completed" {
			if resultID == nil {
				return fault.New(500, "integrity_error", "采集幂等回执缺少快照结果")
			}
			set, err := loadOperatorSnapshotSetByID(ctx, tx, caseID, *resultID)
			if err != nil {
				return fault.New(500, "integrity_error", "采集幂等回执引用的快照不存在")
			}
			set.CollectionID = out.CollectionID
			out.SnapshotSetID = resultID
			out.SnapshotSet = &set
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return OperatorCollectionJob{}, fault.NotFound("没有找到采集请求")
	}
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
	normalizeOperatorSnapshotSetTimes(&out)
	out.Observations, err = loadOperatorObservations(ctx, tx, caseID, out.SnapshotSetID)
	return out, err
}

func normalizeOperatorSnapshotSetTimes(set *OperatorSnapshotSet) {
	if set == nil {
		return
	}
	set.BusinessAt = set.BusinessAt.UTC()
	set.CollectedAt = set.CollectedAt.UTC()
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
