package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

// Binding an audit result never copies or rewrites a review. It appends one
// immutable fact that cites an existing run/attempt/frozen input and the exact
// frozen contract revision. The gate is a release decision input, not an
// approval: even a complete, fully cited audit stays needs_human.
const (
	clmRouteBindContractAudit  = "clm.bind_contract_audit"
	clmEventContractAuditBound = "contract.audit_bound.v1"

	auditStateUnbound = "unbound"
	auditStateCurrent = "current"
	auditStateStale   = "stale"

	auditGateNotApplicable = "not_applicable"
	auditGateBlocked       = "blocked"
	auditGateNeedsHuman    = "needs_human"
)

// ContractAuditGateReason is one machine-readable reason a gate is blocked.
// Detail is additive context such as the offending document or anchor id; it
// never contains document text.
type ContractAuditGateReason struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// ContractAuditBinding is the append-only link between one review attempt and
// one frozen contract revision. It stores the gate snapshot observed when the
// binding was created so a later re-run cannot silently change that history.
type ContractAuditBinding struct {
	BindingID            string                    `json:"binding_id"`
	ContractRevisionID   string                    `json:"contract_revision_id"`
	ReviewRunID          string                    `json:"review_run_id"`
	ReviewAttemptID      string                    `json:"review_attempt_id"`
	AttemptInputHash     string                    `json:"attempt_input_hash"`
	SnapshotDigest       string                    `json:"snapshot_digest"`
	MaterialsDigest      string                    `json:"materials_digest"`
	RunState             string                    `json:"run_state"`
	AttemptState         string                    `json:"attempt_state"`
	GateState            string                    `json:"gate_state"`
	GateReasons          []ContractAuditGateReason `json:"gate_reasons"`
	FindingCount         int                       `json:"finding_count"`
	VerifiedFindingCount int                       `json:"verified_finding_count"`
	ModelFailure         bool                      `json:"model_failure"`
	BoundBy              string                    `json:"bound_by"`
	BoundAt              time.Time                 `json:"bound_at"`
}

// ContractAuditBindingList is the revision-scoped binding history.
type ContractAuditBindingList struct {
	ContractRevisionID string                 `json:"contract_revision_id"`
	Items              []ContractAuditBinding `json:"items"`
}

type auditGateReasonList []ContractAuditGateReason

func (items auditGateReasonList) has(code string) bool {
	for _, item := range items {
		if item.Code == code {
			return true
		}
	}
	return false
}

func (items *auditGateReasonList) add(code, detail string) {
	if items.has(code) && detail == "" {
		return
	}
	*items = append(*items, ContractAuditGateReason{Code: code, Detail: detail})
}

// auditEvidence is the server-resolved material the gate inspects. Every field
// comes from the frozen revision seal/materials and the frozen attempt input;
// none is supplied by the request body.
type auditEvidence struct {
	RunState         string
	AttemptState     string
	AttemptErrorCode string
	PartialReason    string
	RunFailure       string
	AttemptInputHash string
	InputHash        string
	HasInput         bool
	Documents        []FrozenDocument
	Materials        []frozenVersionMaterial
	Findings         []auditFindingEvidence
}

type auditFindingEvidence struct {
	FindingID string
	Anchors   []Anchor
}

type auditAssessment struct {
	GateState            string
	Reasons              []ContractAuditGateReason
	FindingCount         int
	VerifiedFindingCount int
	ModelFailure         bool
}

// assessAuditEvidence is the pure release gate. Missing citation, a foreign
// document, a digest mismatch, a partial/failed attempt or a model failure all
// produce blocked; only a completed attempt whose findings all resolve to the
// frozen revision becomes needs_human. There is deliberately no value that
// means approved.
func assessAuditEvidence(evidence auditEvidence) auditAssessment {
	assessment := auditAssessment{
		FindingCount: len(evidence.Findings),
		Reasons:      []ContractAuditGateReason{},
	}
	reasons := auditGateReasonList(assessment.Reasons)

	materialsByDocument := map[string]frozenVersionMaterial{}
	submittedMaterial := false
	for _, material := range evidence.Materials {
		materialsByDocument[material.MaterialVersionID] = material
		if material.Purpose == "draft" || material.Purpose == "signed_contract" {
			submittedMaterial = true
		}
	}
	if !submittedMaterial {
		reasons.add("submitted_material_missing", "")
	}
	if !evidence.HasInput || len(evidence.Documents) == 0 {
		reasons.add("attempt_input_missing", "")
	}
	if evidence.InputHash != "" && evidence.AttemptInputHash != "" && evidence.InputHash != evidence.AttemptInputHash {
		reasons.add("attempt_input_hash_mismatch", "")
	}
	if evidence.AttemptState != "completed" {
		reasons.add("attempt_not_completed", evidence.AttemptState)
	}
	if evidence.PartialReason != "" {
		reasons.add("attempt_partial", evidence.PartialReason)
	}
	if evidence.AttemptErrorCode != "" {
		assessment.ModelFailure = true
		reasons.add("model_failure", evidence.AttemptErrorCode)
	}
	if evidence.RunState == "failed" || evidence.RunState == "cancelled" {
		reasons.add("run_not_completed", evidence.RunState)
	}
	if evidence.RunFailure != "" {
		assessment.ModelFailure = true
		reasons.add("run_failure", "")
	}

	inputDocuments := map[string]FrozenDocument{}
	submittedInAttempt := false
	for _, document := range evidence.Documents {
		documentID := document.Document.ID
		if documentID == "" {
			reasons.add("attempt_document_identity_missing", "")
			continue
		}
		material, ok := materialsByDocument[documentID]
		if !ok {
			reasons.add("foreign_document", documentID)
			continue
		}
		if document.OriginalHash != material.Digest && document.Document.Digest != material.Digest {
			reasons.add("document_digest_mismatch", documentID)
		}
		if !document.Ready || document.ParseError != "" {
			reasons.add("attempt_document_not_ready", documentID)
		}
		inputDocuments[documentID] = document
		if material.Purpose == "draft" || material.Purpose == "signed_contract" {
			submittedInAttempt = true
		}
	}
	if len(evidence.Documents) > 0 && !submittedInAttempt {
		reasons.add("attempt_omits_submitted_material", "")
	}

	for _, finding := range evidence.Findings {
		if len(finding.Anchors) == 0 {
			reasons.add("finding_without_anchor", finding.FindingID)
			continue
		}
		verified := true
		for _, anchor := range finding.Anchors {
			if anchor.Status != "located" {
				reasons.add("anchor_not_located", finding.FindingID)
				verified = false
				continue
			}
			document, ok := inputDocuments[anchor.DocumentID]
			if !ok {
				reasons.add("anchor_document_not_in_attempt", finding.FindingID)
				verified = false
				continue
			}
			if strings.TrimSpace(anchor.Quote) == "" {
				reasons.add("anchor_quote_missing", finding.FindingID)
				verified = false
			}
			if anchor.NormalizedDigest != "" {
				if document.NormalizedHash == nil || *document.NormalizedHash != anchor.NormalizedDigest {
					reasons.add("anchor_digest_mismatch", finding.FindingID)
					verified = false
				}
			}
			if anchor.ParsedDocumentID != "" && document.ParsedDocumentID != "" &&
				anchor.ParsedDocumentID != document.ParsedDocumentID {
				reasons.add("anchor_parse_mismatch", finding.FindingID)
				verified = false
			}
		}
		if verified {
			assessment.VerifiedFindingCount++
		}
	}

	if len(reasons) > 0 {
		assessment.GateState = auditGateBlocked
	} else {
		assessment.GateState = auditGateNeedsHuman
	}
	sort.SliceStable(reasons, func(i, j int) bool {
		if reasons[i].Code != reasons[j].Code {
			return reasons[i].Code < reasons[j].Code
		}
		return reasons[i].Detail < reasons[j].Detail
	})
	assessment.Reasons = reasons
	return assessment
}

// BindContractAuditInput is the typed command. The handler derives the
// idempotency key from the header; all ids are validated as server identities.
type BindContractAuditInput struct {
	ContractID         string
	ContractRevisionID string
	ReviewRunID        string
	ReviewAttemptID    string
	ExpectedRowVersion int64
	IdempotencyKey     string
}

type auditRunAttemptRecord struct {
	RunID            string
	RunState         string
	CaseOrgID        string
	CaseID           string
	PartialReason    string
	FailureMessage   string
	AttemptID        string
	AttemptState     string
	AttemptErrorCode string
	AttemptInputHash string
	FrozenInputHash  string
	FrozenDocuments  *string
}

func loadAuditRunAttempt(ctx context.Context, tx pgx.Tx, tenantID, runID, attemptID string) (auditRunAttemptRecord, error) {
	var record auditRunAttemptRecord
	err := tx.QueryRow(ctx, `SELECT r.id,r.state,r.org_id,r.case_id,r.partial_reason,r.failure_message,
			a.id,a.state,COALESCE(a.error_code,''),COALESCE(a.input_hash,''),
			COALESCE(i.input_hash,''),i.documents
		FROM review_runs r
		JOIN review_attempts a ON a.run_id=r.id
		LEFT JOIN review_attempt_inputs i ON i.run_id=r.id AND i.attempt_id=a.id
		WHERE r.tenant_id=$1 AND r.id=$2 AND a.id=$3`,
		tenantID, runID, attemptID).Scan(&record.RunID, &record.RunState, &record.CaseOrgID, &record.CaseID,
		&record.PartialReason, &record.FailureMessage, &record.AttemptID, &record.AttemptState,
		&record.AttemptErrorCode, &record.AttemptInputHash, &record.FrozenInputHash, &record.FrozenDocuments)
	if errors.Is(err, pgx.ErrNoRows) {
		return auditRunAttemptRecord{}, fault.NotFound("没有找到对应的审查尝试")
	}
	return record, err
}

func loadAuditCase(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID string) (Case, error) {
	var item Case
	var memberIDs string
	err := tx.QueryRow(ctx, `SELECT id,tenant_id,org_id,business_line,scenario,business_status,archived_at,
			title,contract_type,owner_id,member_ids,created_at,updated_at
		FROM cases WHERE tenant_id=$1 AND org_id=$2 AND id=$3`, tenantID, orgID, caseID).
		Scan(&item.ID, &item.TenantID, &item.OwnerOrgID, &item.BusinessLine, &item.Scenario,
			&item.BusinessStatus, &item.ArchivedAt, &item.Title, &item.ContractType,
			&item.OwnerID, &memberIDs, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Case{}, fault.NotFound("没有找到对应记录")
	}
	if err != nil {
		return Case{}, err
	}
	if err := json.Unmarshal([]byte(memberIDs), &item.MemberIDs); err != nil {
		return Case{}, err
	}
	return item, nil
}

func contractCaseLinked(ctx context.Context, tx pgx.Tx, tenantID, contractID, caseOrgID, caseID string) (bool, error) {
	var linked bool
	// Native drafts keep their case identity in the frozen material rows rather
	// than a clm_contract_cases row; imported contracts have the explicit link.
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_contract_cases
		WHERE tenant_id=$1 AND contract_id=$2 AND case_org_id=$3 AND case_id=$4)
		OR EXISTS(SELECT 1 FROM clm_contract_version_materials
		WHERE tenant_id=$1 AND contract_id=$2 AND case_org_id=$3 AND case_id=$4)`,
		tenantID, contractID, caseOrgID, caseID).Scan(&linked)
	return linked, err
}

func loadAuditFindingEvidence(ctx context.Context, tx pgx.Tx, runID, attemptID, caseID string) ([]auditFindingEvidence, error) {
	rows, err := tx.Query(ctx, `SELECT id,anchors FROM findings
		WHERE run_id=$1 AND attempt_id=$2 AND case_id=$3 ORDER BY seq`, runID, attemptID, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []auditFindingEvidence{}
	for rows.Next() {
		var finding auditFindingEvidence
		var raw []byte
		if err := rows.Scan(&finding.FindingID, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &finding.Anchors); err != nil {
			return nil, err
		}
		items = append(items, finding)
	}
	return items, rows.Err()
}

// BindContractAudit appends one audit-to-revision binding. It locks the
// contract aggregate, rechecks the current frozen revision, the linked case,
// the frozen attempt input and every finding anchor, then stores the gate
// snapshot. The contract row_version CAS makes a concurrent freeze observable
// instead of binding a stale submitted version.
func (s *Store) BindContractAudit(ctx context.Context, user accounts.User, input BindContractAuditInput) (ContractAuditBinding, error) {
	contractID, err := normalizeContractUUID(input.ContractID)
	if err != nil {
		return ContractAuditBinding{}, err
	}
	revisionID, err := normalizeContractUUID(input.ContractRevisionID)
	if err != nil {
		return ContractAuditBinding{}, err
	}
	if strings.TrimSpace(input.ReviewRunID) == "" || strings.TrimSpace(input.ReviewAttemptID) == "" {
		return ContractAuditBinding{}, fault.Invalid("必须指定审查运行与尝试")
	}
	if input.ExpectedRowVersion < 1 {
		return ContractAuditBinding{}, fault.Invalid("绑定审计结果必须携带当前合同 row_version")
	}
	if err := validateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return ContractAuditBinding{}, err
	}
	digest, err := clmRequestDigest(clmRouteBindContractAudit, "POST",
		"/api/contracts/"+contractID+"/revisions/"+revisionID+"/audit-links", input.ExpectedRowVersion,
		struct {
			ReviewRunID     string `json:"review_run_id"`
			ReviewAttemptID string `json:"review_attempt_id"`
		}{ReviewRunID: input.ReviewRunID, ReviewAttemptID: input.ReviewAttemptID})
	if err != nil {
		return ContractAuditBinding{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能绑定审计结果", nil)
	if err != nil {
		return ContractAuditBinding{}, err
	}
	defer rollbackCLM(tx)
	if err := lockWriteReceipt(ctx, tx, current, clmRouteBindContractAudit, input.IdempotencyKey); err != nil {
		return ContractAuditBinding{}, err
	}
	receipt, found, err := replayCLMWriteReceipt(ctx, tx, current, clmRouteBindContractAudit, input.IdempotencyKey, digest, contractID)
	if err != nil {
		return ContractAuditBinding{}, err
	}
	if found {
		record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), contractID)
		if err != nil {
			return ContractAuditBinding{}, err
		}
		if !authorizeContract(record, current, access.Review) {
			return ContractAuditBinding{}, fault.NotFound("没有找到对应记录")
		}
		var result ContractAuditBinding
		if err := json.Unmarshal(receipt.Response, &result); err != nil {
			return ContractAuditBinding{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ContractAuditBinding{}, err
		}
		return result, nil
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), contractID)
	if err != nil {
		return ContractAuditBinding{}, err
	}
	if !authorizeContract(record, current, access.Review) {
		return ContractAuditBinding{}, fault.NotFound("没有找到对应记录")
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != revisionID {
		return ContractAuditBinding{}, fault.Conflict("只能绑定当前送审的冻结版本")
	}
	if record.RowVersion != input.ExpectedRowVersion {
		return ContractAuditBinding{}, fault.Conflict("合同已被他人更新，请刷新后确认")
	}
	seal, err := loadContractVersionSeal(ctx, tx, accounts.Tenant(current), contractID, revisionID)
	if err != nil {
		return ContractAuditBinding{}, err
	}
	if seal == nil {
		return ContractAuditBinding{}, fault.Conflict("该版本没有可核验的冻结封印，不能绑定审计结果")
	}
	materials, err := loadContractVersionMaterials(ctx, tx, accounts.Tenant(current), contractID, revisionID)
	if err != nil {
		return ContractAuditBinding{}, err
	}
	if len(materials) == 0 {
		return ContractAuditBinding{}, fault.Invalid("冻结版本没有材料，不能绑定审计结果")
	}
	run, err := loadAuditRunAttempt(ctx, tx, accounts.Tenant(current), input.ReviewRunID, input.ReviewAttemptID)
	if err != nil {
		return ContractAuditBinding{}, err
	}
	linked, err := contractCaseLinked(ctx, tx, accounts.Tenant(current), contractID, run.CaseOrgID, run.CaseID)
	if err != nil {
		return ContractAuditBinding{}, err
	}
	if !linked {
		return ContractAuditBinding{}, fault.NotFound("没有找到对应记录")
	}
	caseItem, err := loadAuditCase(ctx, tx, accounts.Tenant(current), run.CaseOrgID, run.CaseID)
	if err != nil {
		return ContractAuditBinding{}, err
	}
	if !authorizeCase(caseItem, current, access.Review) {
		return ContractAuditBinding{}, fault.NotFound("没有找到对应记录")
	}
	if err := businessWritable(caseItem); err != nil {
		return ContractAuditBinding{}, err
	}
	evidence := auditEvidence{
		RunState: run.RunState, AttemptState: run.AttemptState, AttemptErrorCode: run.AttemptErrorCode,
		PartialReason: run.PartialReason, RunFailure: run.FailureMessage,
		AttemptInputHash: run.AttemptInputHash, InputHash: run.FrozenInputHash,
		HasInput: run.FrozenDocuments != nil, Materials: materials,
	}
	if strings.TrimSpace(run.FrozenInputHash) == "" {
		return ContractAuditBinding{}, fault.Conflict("审查尝试缺少冻结输入摘要，不能绑定")
	}
	if run.FrozenDocuments == nil || strings.TrimSpace(*run.FrozenDocuments) == "" {
		return ContractAuditBinding{}, fault.Conflict("审查尝试缺少冻结输入，不能绑定")
	}
	if err := json.Unmarshal([]byte(*run.FrozenDocuments), &evidence.Documents); err != nil {
		return ContractAuditBinding{}, fault.Conflict("审查尝试的冻结输入损坏，不能绑定")
	}
	evidence.Findings, err = loadAuditFindingEvidence(ctx, tx, run.RunID, run.AttemptID, run.CaseID)
	if err != nil {
		return ContractAuditBinding{}, err
	}
	assessment := assessAuditEvidence(evidence)
	bindingID, err := newContractUUID()
	if err != nil {
		return ContractAuditBinding{}, err
	}
	reasonsJSON, err := json.Marshal(assessment.Reasons)
	if err != nil {
		return ContractAuditBinding{}, err
	}
	var boundAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO clm_contract_audit_bindings
		(tenant_id,contract_id,contract_revision_id,binding_id,review_run_id,review_attempt_id,
		 case_org_id,case_id,attempt_input_hash,snapshot_digest,materials_digest,run_state,attempt_state,
		 gate_state,gate_reasons,finding_count,verified_finding_count,model_failure,contract_row_version,actor_id,actor_role)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15::jsonb,$16,$17,$18,$19,$20,$21)
		RETURNING bound_at`,
		accounts.Tenant(current), contractID, revisionID, bindingID, run.RunID, run.AttemptID,
		run.CaseOrgID, run.CaseID, run.FrozenInputHash, seal.SnapshotDigest, seal.MaterialsDigest,
		run.RunState, run.AttemptState, assessment.GateState, string(reasonsJSON),
		assessment.FindingCount, assessment.VerifiedFindingCount, assessment.ModelFailure,
		record.RowVersion, current.ID, current.Role).Scan(&boundAt)
	if err != nil {
		return ContractAuditBinding{}, clmUniqueViolation(err, "该审查尝试已绑定过这个版本")
	}
	var rowVersion int64
	err = tx.QueryRow(ctx, `UPDATE clm_contracts SET row_version=row_version+1, updated_at=clock_timestamp()
		WHERE tenant_id=$1 AND contract_id=$2 AND row_version=$3
		RETURNING row_version`, accounts.Tenant(current), contractID, record.RowVersion).Scan(&rowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ContractAuditBinding{}, fault.Conflict("合同已被他人更新，请刷新后确认")
	}
	if err != nil {
		return ContractAuditBinding{}, err
	}
	result := ContractAuditBinding{
		BindingID: bindingID, ContractRevisionID: revisionID, ReviewRunID: run.RunID,
		ReviewAttemptID: run.AttemptID, AttemptInputHash: run.FrozenInputHash,
		SnapshotDigest: seal.SnapshotDigest, MaterialsDigest: seal.MaterialsDigest,
		RunState: run.RunState, AttemptState: run.AttemptState, GateState: assessment.GateState,
		GateReasons: assessment.Reasons, FindingCount: assessment.FindingCount,
		VerifiedFindingCount: assessment.VerifiedFindingCount, ModelFailure: assessment.ModelFailure,
		BoundBy: current.ID, BoundAt: boundAt,
	}
	eventID, err := newContractUUID()
	if err != nil {
		return ContractAuditBinding{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"contract_id":          contractID,
		"contract_revision_id": revisionID,
		"binding_id":           bindingID,
		"review_run_id":        run.RunID,
		"review_attempt_id":    run.AttemptID,
		"gate_state":           assessment.GateState,
		"actor_id":             current.ID,
	})
	if err != nil {
		return ContractAuditBinding{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_business_events
		(tenant_id,event_id,contract_id,aggregate_version,event_type,schema_version,payload)
		VALUES ($1,$2,$3,$4,$5,1,$6::jsonb)`,
		accounts.Tenant(current), eventID, contractID, rowVersion, clmEventContractAuditBound, string(payload)); err != nil {
		return ContractAuditBinding{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clm_outbox_delivery (tenant_id,event_id) VALUES ($1,$2)`,
		accounts.Tenant(current), eventID); err != nil {
		return ContractAuditBinding{}, err
	}
	response, err := json.Marshal(result)
	if err != nil {
		return ContractAuditBinding{}, err
	}
	if err := insertCLMWriteReceipt(ctx, tx, accounts.Tenant(current), current.ID, clmRouteBindContractAudit,
		input.IdempotencyKey, digest, contractID, 201, response); err != nil {
		return ContractAuditBinding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractAuditBinding{}, err
	}
	return result, nil
}

const contractAuditBindingColumns = `binding_id,contract_revision_id,review_run_id,review_attempt_id,
 attempt_input_hash,snapshot_digest,materials_digest,run_state,attempt_state,gate_state,gate_reasons,
 finding_count,verified_finding_count,model_failure,actor_id,bound_at`

func scanContractAuditBinding(row pgx.Row) (ContractAuditBinding, error) {
	var item ContractAuditBinding
	var reasons []byte
	err := row.Scan(&item.BindingID, &item.ContractRevisionID, &item.ReviewRunID, &item.ReviewAttemptID,
		&item.AttemptInputHash, &item.SnapshotDigest, &item.MaterialsDigest, &item.RunState,
		&item.AttemptState, &item.GateState, &reasons, &item.FindingCount,
		&item.VerifiedFindingCount, &item.ModelFailure, &item.BoundBy, &item.BoundAt)
	if err != nil {
		return ContractAuditBinding{}, err
	}
	if err := json.Unmarshal(reasons, &item.GateReasons); err != nil {
		return ContractAuditBinding{}, err
	}
	if item.GateReasons == nil {
		item.GateReasons = []ContractAuditGateReason{}
	}
	return item, nil
}

// LoadContractAuditBindings returns the append-only binding history for one
// frozen revision. It is a read-only lookup and re-checks contract read scope.
func (s *Store) LoadContractAuditBindings(ctx context.Context, user accounts.User, contractID, revisionID string) (ContractAuditBindingList, error) {
	if !s.HasDatabase() {
		return ContractAuditBindingList{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	normalizedContractID, err := normalizeContractUUID(contractID)
	if err != nil {
		return ContractAuditBindingList{}, err
	}
	contractID = normalizedContractID
	normalizedRevisionID, err := normalizeContractUUID(revisionID)
	if err != nil {
		return ContractAuditBindingList{}, err
	}
	revisionID = normalizedRevisionID
	if err != nil {
		return ContractAuditBindingList{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return ContractAuditBindingList{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ContractAuditBindingList{}, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ContractAuditBindingList{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), contractID)
	if err != nil {
		return ContractAuditBindingList{}, err
	}
	if !authorizeContract(record, current, access.Read) {
		return ContractAuditBindingList{}, fault.NotFound("没有找到对应记录")
	}
	if _, err := loadFrozenContractVersion(ctx, tx, accounts.Tenant(current), contractID, revisionID); err != nil {
		return ContractAuditBindingList{}, err
	}
	rows, err := tx.Query(ctx, `SELECT `+contractAuditBindingColumns+`
		FROM clm_contract_audit_bindings
		WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3
		ORDER BY bound_at DESC, binding_id DESC`, accounts.Tenant(current), contractID, revisionID)
	if err != nil {
		return ContractAuditBindingList{}, err
	}
	defer rows.Close()
	items := []ContractAuditBinding{}
	for rows.Next() {
		item, err := scanContractAuditBinding(rows)
		if err != nil {
			return ContractAuditBindingList{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ContractAuditBindingList{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractAuditBindingList{}, err
	}
	return ContractAuditBindingList{ContractRevisionID: revisionID, Items: items}, nil
}

// contractAuditStateFor is the pure stale rule: a binding is current only while
// its revision is the contract's current_revision_id. A new revision therefore
// starts unbound and an older audit is stale; no old disposition is inherited.
func contractAuditStateFor(revisionID string, currentRevisionID *string, binding *ContractAuditBinding) (string, string) {
	if binding == nil {
		return auditStateUnbound, auditGateNotApplicable
	}
	if currentRevisionID == nil || *currentRevisionID != revisionID {
		return auditStateStale, binding.GateState
	}
	return auditStateCurrent, binding.GateState
}

// loadLatestContractAuditBindings returns the newest binding per revision. The
// binding table itself is append-only, so this projection is history-safe.
func loadLatestContractAuditBindings(ctx context.Context, tx pgx.Tx, tenantID, contractID string) (map[string]ContractAuditBinding, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON (contract_revision_id) `+contractAuditBindingColumns+`
		FROM clm_contract_audit_bindings
		WHERE tenant_id=$1 AND contract_id=$2
		ORDER BY contract_revision_id, bound_at DESC, binding_id DESC`, tenantID, contractID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := map[string]ContractAuditBinding{}
	for rows.Next() {
		item, err := scanContractAuditBinding(rows)
		if err != nil {
			return nil, err
		}
		items[item.ContractRevisionID] = item
	}
	return items, rows.Err()
}

func loadLatestContractAuditBinding(ctx context.Context, tx pgx.Tx, tenantID, contractID, revisionID string) (*ContractAuditBinding, error) {
	item, err := scanContractAuditBinding(tx.QueryRow(ctx, `SELECT `+contractAuditBindingColumns+`
		FROM clm_contract_audit_bindings
		WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3
		ORDER BY bound_at DESC, binding_id DESC LIMIT 1`, tenantID, contractID, revisionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}
