package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

const (
	clmRouteCreateAssetHandover = "clm.create_asset_handover"
	clmEventAssetHandover       = "contract.asset_handover_recorded.v1"
	clmEventAssetReceipt        = "contract.asset_erp_receipt_recorded.v1"
)

type ContractAssetHandoverInput struct {
	ContractID         string        `json:"-"`
	ExpectedRowVersion int64         `json:"expected_row_version"`
	ContractRevisionID string        `json:"contract_revision_id"`
	AssetPlanID        string        `json:"asset_plan_id"`
	DeliveryID         string        `json:"delivery_id"`
	AcceptanceID       string        `json:"acceptance_id"`
	ScopeStart         string        `json:"scope_start"`
	ScopeEnd           string        `json:"scope_end"`
	HandedOverAt       string        `json:"handed_over_at"`
	Location           string        `json:"location"`
	TransferNote       string        `json:"transfer_note"`
	Evidence           []MaterialRef `json:"evidence"`
	IdempotencyKey     string        `json:"-"`
}

type ContractAssetSource struct{ TenantID, SourceID, KeyID string }

type ContractAssetReceiptInput struct {
	ContractID      string    `json:"contract_id"`
	HandoverID      string    `json:"handover_id"`
	EventID         string    `json:"event_id"`
	EventType       string    `json:"event_type"`
	OriginalEventID string    `json:"original_event_id,omitempty"`
	AssetCode       string    `json:"asset_code"`
	Quantity        string    `json:"quantity"`
	Unit            string    `json:"unit"`
	OccurredAt      time.Time `json:"occurred_at"`
}

type ContractAssetReceipt struct {
	ReceiptID             string    `json:"receipt_id"`
	ContractID            string    `json:"contract_id"`
	HandoverID            string    `json:"handover_id"`
	SourceID              string    `json:"source_id"`
	SourceKeyID           string    `json:"source_key_id"`
	SourceEventID         string    `json:"source_event_id"`
	EventType             string    `json:"event_type"`
	OriginalSourceEventID string    `json:"original_source_event_id,omitempty"`
	AssetCode             string    `json:"asset_code"`
	Quantity              string    `json:"quantity"`
	Unit                  string    `json:"unit"`
	OccurredAt            time.Time `json:"occurred_at"`
	RecordedAt            time.Time `json:"recorded_at"`
	PayloadDigest         string    `json:"payload_digest"`
}

type ContractAssetHandover struct {
	HandoverID                 string                    `json:"handover_id"`
	ContractID                 string                    `json:"contract_id"`
	ContractRevisionID         string                    `json:"contract_revision_id"`
	AssetPlanID                string                    `json:"asset_plan_id"`
	DeliveryID                 string                    `json:"delivery_id"`
	ItemID                     string                    `json:"item_id"`
	AcceptanceID               string                    `json:"acceptance_id"`
	ScopeStart                 string                    `json:"scope_start"`
	ScopeEnd                   string                    `json:"scope_end"`
	Quantity                   string                    `json:"quantity"`
	Unit                       string                    `json:"unit"`
	HandedOverAt               time.Time                 `json:"handed_over_at"`
	Location                   string                    `json:"location"`
	TransferNote               string                    `json:"transfer_note"`
	Evidence                   []ContractVersionMaterial `json:"evidence"`
	EvidenceRedacted           bool                      `json:"evidence_redacted,omitempty"`
	DepartmentCodeClaim        string                    `json:"department_code_claim,omitempty"`
	DepartmentNameClaim        string                    `json:"department_name_claim,omitempty"`
	DepartmentVerification     string                    `json:"department_verification"`
	CustodianUserID            string                    `json:"custodian_user_id,omitempty"`
	CustodianDisplayName       string                    `json:"custodian_display_name,omitempty"`
	CustodianOrgID             string                    `json:"custodian_org_id,omitempty"`
	CustodianDirectorySource   string                    `json:"custodian_directory_source,omitempty"`
	CustodianDirectoryRevision string                    `json:"custodian_directory_revision,omitempty"`
	CustodianDirectoryChecked  *time.Time                `json:"custodian_directory_checked_at,omitempty"`
	CustodianVerification      string                    `json:"custodian_verification"`
	AssignmentVerification     string                    `json:"assignment_verification"`
	BookingStatus              string                    `json:"booking_status"`
	RecordedBy                 string                    `json:"recorded_by"`
	RecordedAt                 time.Time                 `json:"recorded_at"`
	ContractRowVersion         int64                     `json:"contract_row_version"`
	Receipts                   []ContractAssetReceipt    `json:"receipts"`
}

type ContractAssetPlanSummary struct {
	ContractRevisionID string                  `json:"contract_revision_id"`
	AssetPlanID        string                  `json:"asset_plan_id"`
	ItemID             string                  `json:"item_id,omitempty"`
	Name               string                  `json:"name,omitempty"`
	PlannedQuantity    string                  `json:"planned_quantity,omitempty"`
	Unit               string                  `json:"unit,omitempty"`
	DepartmentCode     string                  `json:"department_code,omitempty"`
	DepartmentName     string                  `json:"department_name,omitempty"`
	DepartmentStatus   string                  `json:"department_verification"`
	CustodianUserID    string                  `json:"custodian_user_id,omitempty"`
	FixedAssetStatus   string                  `json:"fixed_asset_status"`
	PlanStatus         string                  `json:"plan_status"`
	Handovers          []ContractAssetHandover `json:"handovers"`
}

type ContractAssetList struct {
	ContractID       string                     `json:"contract_id"`
	RowVersion       int64                      `json:"row_version"`
	FixedAssetStatus string                     `json:"fixed_asset_status"`
	Items            []ContractAssetPlanSummary `json:"items"`
}

type frozenContractAssetPlan struct {
	DraftAssetPlan
	Unit string
}

func (s *Store) CreateContractAssetHandover(ctx context.Context, user accounts.User, input ContractAssetHandoverInput) (ContractAssetHandover, error) {
	ctx = commandContext(ctx, user)
	if !s.HasDatabase() {
		return ContractAssetHandover{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	contractID, err := normalizeContractUUID(input.ContractID)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	for field, value := range map[string]*string{"contract_revision_id": &input.ContractRevisionID, "asset_plan_id": &input.AssetPlanID, "delivery_id": &input.DeliveryID, "acceptance_id": &input.AcceptanceID} {
		if err = normalizeContractUUIDField(field, value); err != nil {
			return ContractAssetHandover{}, err
		}
	}
	start, err := fulfillmentDecimal("scope_start", strings.TrimSpace(input.ScopeStart), true)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	end, err := fulfillmentDecimal("scope_end", strings.TrimSpace(input.ScopeEnd), false)
	if err != nil || start.Cmp(end) >= 0 {
		return ContractAssetHandover{}, fault.Invalid("scope_end must exceed scope_start")
	}
	input.ScopeStart, input.ScopeEnd = ratString(start), ratString(end)
	handedOverAt, err := parseFulfillmentTime("handed_over_at", input.HandedOverAt)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	if handedOverAt.After(time.Now().UTC().Add(5 * time.Minute)) {
		return ContractAssetHandover{}, fault.Invalid("handed_over_at cannot be in the future")
	}
	input.HandedOverAt = handedOverAt.Format(time.RFC3339Nano)
	if input.ExpectedRowVersion < 1 || strings.TrimSpace(input.IdempotencyKey) == "" {
		return ContractAssetHandover{}, fault.Invalid("expected_row_version and idempotency key are required")
	}
	if err = validateFulfillmentText("transfer_note", input.TransferNote, 2000, true); err != nil {
		return ContractAssetHandover{}, err
	}
	input.TransferNote = strings.TrimSpace(input.TransferNote)
	if err = validateFulfillmentText("location", input.Location, 1000, true); err != nil {
		return ContractAssetHandover{}, err
	}
	input.Location = strings.TrimSpace(input.Location)
	digest, err := clmRequestDigest(clmRouteCreateAssetHandover, "POST", "/api/contracts/"+contractID+"/asset-handovers", input.ExpectedRowVersion, input)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能登记资产移交", nil)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	defer rollbackCLM(tx)
	if _, err = fulfillmentWriteRecord(ctx, tx, current, contractID); err != nil {
		return ContractAssetHandover{}, err
	}
	if err = lockWriteReceipt(ctx, tx, current, clmRouteCreateAssetHandover, input.IdempotencyKey); err != nil {
		return ContractAssetHandover{}, err
	}
	prior, found, err := replayCLMWriteReceipt(ctx, tx, current, clmRouteCreateAssetHandover, input.IdempotencyKey, digest, contractID)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	if found {
		var saved ContractAssetHandover
		if err = json.Unmarshal(prior.Response, &saved); err != nil {
			return ContractAssetHandover{}, err
		}
		for _, evidence := range saved.Evidence {
			if err = requireSigningCaseRead(ctx, tx, current, evidence.CaseOrgID, evidence.CaseID); err != nil {
				return ContractAssetHandover{}, fault.NotFound("没有找到对应记录")
			}
		}
		if err = tx.Commit(ctx); err != nil {
			return ContractAssetHandover{}, err
		}
		return saved, nil
	}
	if err = scopedContractLock(ctx, tx, contractID); err != nil {
		return ContractAssetHandover{}, err
	}
	record, err := fulfillmentWriteRecord(ctx, tx, current, contractID)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	if err = requireCLMFulfillmentManage(record, current); err != nil {
		return ContractAssetHandover{}, err
	}
	if record.RowVersion != input.ExpectedRowVersion {
		return ContractAssetHandover{}, fault.Conflict("合同已被修改，请刷新后重试")
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != input.ContractRevisionID {
		return ContractAssetHandover{}, fault.Conflict("资产移交必须对应当前冻结修订")
	}
	var signed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_contract_signing_registrations WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3)`, record.TenantID, contractID, input.ContractRevisionID).Scan(&signed); err != nil {
		return ContractAssetHandover{}, err
	}
	if !signed {
		return ContractAssetHandover{}, fault.Conflict("资产移交必须对应已登记签署的冻结修订")
	}
	draft, err := loadFrozenFulfillmentDraft(ctx, tx, record.TenantID, contractID, input.ContractRevisionID)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	if draft.Contract == nil || draft.Contract.IsFixedAsset == nil {
		return ContractAssetHandover{}, fault.Conflict("固定资产属性未知，不能登记资产移交")
	}
	if !*draft.Contract.IsFixedAsset {
		return ContractAssetHandover{}, fault.Conflict("该合同明确不适用固定资产移交")
	}
	plan, err := lockedAssetPlan(ctx, tx, record, input.ContractRevisionID, input.AssetPlanID, draft)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	var itemID, itemUnit, itemQuantity string
	err = tx.QueryRow(ctx, `SELECT item_id::text,COALESCE(unit,''),COALESCE(planned_quantity::text,'') FROM clm_contract_revision_items
		WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND item_id=$4`, record.TenantID, contractID, input.ContractRevisionID, plan.ItemID).Scan(&itemID, &itemUnit, &itemQuantity)
	if errors.Is(err, pgx.ErrNoRows) {
		return ContractAssetHandover{}, fault.Conflict("资产计划对应的冻结商品或单位未知")
	}
	if err != nil {
		return ContractAssetHandover{}, err
	}
	if itemUnit == "" || itemUnit != plan.Unit || itemQuantity == "" {
		return ContractAssetHandover{}, fault.Conflict("冻结商品单位或数量不能核验")
	}
	planQty, err := fulfillmentDecimal("asset plan quantity", plan.Quantity, false)
	if err != nil {
		return ContractAssetHandover{}, fault.Conflict("冻结资产计划数量不能核验")
	}
	proposal := new(big.Rat).Sub(end, start)
	if proposal.Sign() <= 0 || proposal.Cmp(planQty) > 0 {
		return ContractAssetHandover{}, fault.Invalid("handover range exceeds frozen asset plan quantity")
	}
	var acceptanceStart, acceptanceEnd, accepted, deliveryUnit string
	var result, acceptanceDelivery string
	err = tx.QueryRow(ctx, `SELECT acceptance.scope_start::text,acceptance.scope_end::text,acceptance.accepted_quantity::text,
		acceptance.result,acceptance.delivery_id::text,revision_item.unit
		FROM clm_contract_acceptances acceptance JOIN clm_contract_revision_items revision_item
		USING (tenant_id,contract_id,contract_revision_id,item_id)
		WHERE acceptance.tenant_id=$1 AND acceptance.contract_id=$2 AND acceptance.contract_revision_id=$3 AND acceptance.acceptance_id=$4 AND acceptance.delivery_id=$5 AND acceptance.item_id=$6`,
		record.TenantID, contractID, input.ContractRevisionID, input.AcceptanceID, input.DeliveryID, itemID).Scan(&acceptanceStart, &acceptanceEnd, &accepted, &result, &acceptanceDelivery, &deliveryUnit)
	if errors.Is(err, pgx.ErrNoRows) {
		return ContractAssetHandover{}, fault.Conflict("验收事实不存在或不匹配")
	}
	if err != nil {
		return ContractAssetHandover{}, err
	}
	if deliveryUnit != plan.Unit || acceptanceDelivery != input.DeliveryID || result == "failed" || accepted == "0.000000" {
		return ContractAssetHandover{}, fault.Conflict("指定验收范围没有合格数量")
	}
	qualStart, _ := fulfillmentDecimal("acceptance scope start", acceptanceStart, true)
	qualEnd, _ := fulfillmentDecimal("acceptance scope end", acceptanceEnd, false)
	qualified, _ := fulfillmentDecimal("accepted quantity", accepted, true)
	qualEnd = new(big.Rat).Add(qualStart, qualified)
	if start.Cmp(qualStart) < 0 || end.Cmp(qualEnd) > 0 {
		return ContractAssetHandover{}, fault.Conflict("移交范围必须完全位于该验收事实的已通过前缀")
	}
	var overlap bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_contract_asset_handovers
		WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND item_id=$4 AND delivery_id=$5
		AND scope_start < $7::numeric AND scope_end > $6::numeric)`, record.TenantID, contractID, input.ContractRevisionID, itemID, input.DeliveryID, input.ScopeStart, input.ScopeEnd).Scan(&overlap)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	if overlap {
		return ContractAssetHandover{}, fault.Conflict("资产移交范围已被其他资产计划分配")
	}
	var allocated string
	err = tx.QueryRow(ctx, `SELECT COALESCE(sum(quantity),0)::text FROM clm_contract_asset_handovers
		WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND asset_plan_id=$4`, record.TenantID, contractID, input.ContractRevisionID, input.AssetPlanID).Scan(&allocated)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	allocatedRat, _ := fulfillmentDecimal("allocated quantity", allocated, true)
	if allocatedRat == nil {
		allocatedRat = new(big.Rat)
	}
	if new(big.Rat).Add(allocatedRat, proposal).Cmp(planQty) > 0 {
		return ContractAssetHandover{}, fault.Conflict("移交累计数量超过冻结资产计划数量")
	}
	evidence, err := resolveFulfillmentEvidence(ctx, tx, current, input.Evidence, "asset_handover")
	if err != nil {
		return ContractAssetHandover{}, err
	}
	var custodian accounts.User
	custodianStatus := "unknown"
	var checkedAt *time.Time
	directoryRevision, directorySource := "", ""
	if plan.CustodianUser != "" {
		snapshot := accounts.LookupUserSnapshot(plan.CustodianUser)
		if snapshot.Found && snapshot.Status.State != "unavailable" && snapshot.User.Enabled && accounts.Tenant(snapshot.User) == record.TenantID {
			custodian = snapshot.User
			custodianStatus = "verified"
			resolvedAt := snapshot.ResolvedAt.UTC()
			checkedAt = &resolvedAt
			directoryRevision, directorySource = snapshot.Status.Revision, snapshot.Status.Mode+":user_directory"
		}
	}
	handoverID, err := newContractUUID()
	if err != nil {
		return ContractAssetHandover{}, err
	}
	departmentStatus := "unknown" // There is no authoritative department-code/name directory in this service.
	rowVersion, err := bumpFulfillmentRowVersion(ctx, tx, record, input.ExpectedRowVersion)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	var recordedAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO clm_contract_asset_handovers
		(tenant_id,contract_id,contract_revision_id,handover_id,asset_plan_id,delivery_id,item_id,acceptance_id,scope_start,scope_end,unit,handed_over_at,location,transfer_note,evidence_snapshot,department_code_claim,department_name_claim,department_verification,custodian_user_id,custodian_display_name_snapshot,custodian_tenant_id_snapshot,custodian_org_id_snapshot,custodian_directory_source,custodian_directory_revision,custodian_directory_checked_at,custodian_verification,recorded_by,contract_row_version)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::numeric,$10::numeric,$11,$12,$13,$14,$15::jsonb,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28)
		RETURNING recorded_at`, record.TenantID, contractID, input.ContractRevisionID, handoverID, input.AssetPlanID, input.DeliveryID, itemID, input.AcceptanceID,
		input.ScopeStart, input.ScopeEnd, plan.Unit, handedOverAt, input.Location, input.TransferNote, string(evidenceJSON), plan.DepartmentCode, plan.DepartmentName, departmentStatus,
		custodian.ID, custodian.DisplayName, accounts.Tenant(custodian), custodian.OrgID, directorySource, directoryRevision, checkedAt, custodianStatus, current.ID, rowVersion).Scan(&recordedAt)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	assignmentStatus := "unknown"
	if departmentStatus == "verified" && custodianStatus == "verified" {
		assignmentStatus = "verified"
	}
	output := ContractAssetHandover{HandoverID: handoverID, ContractID: contractID, ContractRevisionID: input.ContractRevisionID, AssetPlanID: input.AssetPlanID,
		DeliveryID: input.DeliveryID, ItemID: itemID, AcceptanceID: input.AcceptanceID, ScopeStart: input.ScopeStart, ScopeEnd: input.ScopeEnd, Quantity: ratString(proposal), Unit: plan.Unit,
		HandedOverAt: handedOverAt, Location: input.Location, TransferNote: input.TransferNote, Evidence: evidence, DepartmentCodeClaim: plan.DepartmentCode, DepartmentNameClaim: plan.DepartmentName,
		DepartmentVerification: departmentStatus, CustodianUserID: custodian.ID, CustodianDisplayName: custodian.DisplayName, CustodianOrgID: custodian.OrgID,
		CustodianDirectorySource: directorySource, CustodianDirectoryRevision: directoryRevision, CustodianDirectoryChecked: checkedAt, CustodianVerification: custodianStatus, AssignmentVerification: assignmentStatus,
		BookingStatus: "pending_booking", RecordedBy: current.ID, RecordedAt: recordedAt, ContractRowVersion: rowVersion, Receipts: []ContractAssetReceipt{}}
	if err = insertCLMApprovalEvent(ctx, tx, record.TenantID, contractID, rowVersion, clmEventAssetHandover, map[string]any{"handover_id": handoverID, "asset_plan_id": input.AssetPlanID, "acceptance_id": input.AcceptanceID, "scope_start": input.ScopeStart, "scope_end": input.ScopeEnd, "assignment_verification": assignmentStatus}); err != nil {
		return ContractAssetHandover{}, err
	}
	response, err := json.Marshal(output)
	if err != nil {
		return ContractAssetHandover{}, err
	}
	if err = insertCLMWriteReceipt(ctx, tx, record.TenantID, current.ID, clmRouteCreateAssetHandover, input.IdempotencyKey, digest, contractID, 201, response); err != nil {
		return ContractAssetHandover{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ContractAssetHandover{}, err
	}
	return output, nil
}

func lockedAssetPlan(ctx context.Context, tx pgx.Tx, record clmContractRecord, revisionID, planID string, draft DraftContent) (frozenContractAssetPlan, error) {
	var plan frozenContractAssetPlan
	var itemID string
	var quantity string
	var unit, departmentCode, departmentName, custodian string
	err := tx.QueryRow(ctx, `SELECT item_id::text,planned_quantity::text,unit,department_code,department_name,custodian_user_id
		FROM clm_contract_revision_asset_plans WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND asset_plan_id=$4`, record.TenantID, record.ContractID, revisionID, planID).
		Scan(&itemID, &quantity, &unit, &departmentCode, &departmentName, &custodian)
	if errors.Is(err, pgx.ErrNoRows) {
		return plan, fault.Conflict("冻结资产计划不存在、重复或无法核验")
	}
	if err != nil {
		return plan, err
	}
	count := 0
	var draftPlan DraftAssetPlan
	for _, candidate := range draft.Assets {
		id, normalizeErr := normalizeContractUUID(candidate.AssetPlanID)
		if normalizeErr == nil && id == planID {
			draftPlan = candidate
			count++
		}
	}
	normalizedItemID, itemErr := normalizeContractUUID(draftPlan.ItemID)
	if count != 1 || itemErr != nil || normalizedItemID != itemID || draftPlan.Quantity != quantity || draftPlan.DepartmentCode != departmentCode || draftPlan.DepartmentName != departmentName || draftPlan.CustodianUser != custodian {
		return frozenContractAssetPlan{}, fault.Conflict("冻结资产计划快照与安全投影不一致")
	}
	plan.DraftAssetPlan = draftPlan
	plan.AssetPlanID, plan.ItemID, plan.Quantity, plan.DepartmentCode, plan.DepartmentName, plan.CustodianUser = planID, itemID, quantity, departmentCode, departmentName, custodian
	plan.Unit = unit
	return plan, nil
}

func (s *Store) RecordTrustedContractAssetReceipt(ctx context.Context, source ContractAssetSource, input ContractAssetReceiptInput) (ContractAssetReceipt, bool, error) {
	var result ContractAssetReceipt
	if !s.HasDatabase() {
		return result, false, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	if err := s.checkCLMWriteAllowed("资产来源事实当前只读"); err != nil {
		return result, false, err
	}
	if strings.TrimSpace(source.TenantID) == "" || strings.TrimSpace(source.SourceID) == "" || !boundedVisibleText(source.SourceID, 200) || !boundedVisibleText(source.KeyID, 200) {
		return result, false, fault.Forbidden("资产来源未授权")
	}
	contractID, err := normalizeContractUUID(input.ContractID)
	if err != nil {
		return result, false, err
	}
	input.ContractID = contractID
	if err = normalizeContractUUIDField("handover_id", &input.HandoverID); err != nil {
		return result, false, err
	}
	input.EventID, input.EventType, input.AssetCode, input.Unit = strings.TrimSpace(input.EventID), strings.TrimSpace(input.EventType), strings.TrimSpace(input.AssetCode), strings.TrimSpace(input.Unit)
	input.OriginalEventID = strings.TrimSpace(input.OriginalEventID)
	if !boundedVisibleText(input.EventID, 300) || !boundedVisibleText(input.AssetCode, 200) || !boundedVisibleText(input.Unit, 40) || !input.OccurredAt.After(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)) || input.OccurredAt.After(time.Now().Add(5*time.Minute)) {
		return result, false, fault.Invalid("资产回执字段无效")
	}
	if input.EventType != "receipt" && input.EventType != "reversal" {
		return result, false, fault.Invalid("event_type 必须是 receipt 或 reversal")
	}
	if input.EventType == "receipt" && input.OriginalEventID != "" || input.EventType == "reversal" && !boundedVisibleText(input.OriginalEventID, 300) {
		return result, false, fault.Invalid("冲正必须且仅可引用原来源事件")
	}
	quantity, err := fulfillmentDecimal("quantity", strings.TrimSpace(input.Quantity), false)
	if err != nil {
		return result, false, err
	}
	input.Quantity = ratString(quantity)
	canonical := struct {
		Tenant, Source, Contract, Handover, EventID, EventType, Original, AssetCode, Quantity, Unit string
		OccurredAt                                                                                  time.Time
	}{source.TenantID, source.SourceID, input.ContractID, input.HandoverID, input.EventID, input.EventType, input.OriginalEventID, input.AssetCode, input.Quantity, input.Unit, input.OccurredAt.UTC()}
	body, err := json.Marshal(canonical)
	if err != nil {
		return result, false, err
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return result, false, err
	}
	defer rollbackCLM(tx)
	if err = setLocalScope(ctx, tx, dbScope{ActorID: "asset-source:" + source.SourceID, TenantID: source.TenantID, OrgID: "", Role: "service"}); err != nil {
		return result, false, err
	}
	if err = scopedAssetEventLock(ctx, tx, source.TenantID, source.SourceID, input.EventID); err != nil {
		return result, false, err
	}
	if err = scopedContractLock(ctx, tx, input.ContractID); err != nil {
		return result, false, err
	}
	var oldDigest string
	err = tx.QueryRow(ctx, `SELECT payload_digest FROM clm_contract_asset_receipts WHERE tenant_id=$1 AND source_id=$2 AND source_event_id=$3`, source.TenantID, source.SourceID, input.EventID).Scan(&oldDigest)
	if err == nil {
		if oldDigest != digest {
			return result, false, fault.Conflict("来源事件ID已用于不同资产回执")
		}
		result, err = loadContractAssetReceipt(ctx, tx, source.TenantID, source.SourceID, input.EventID)
		if err != nil {
			return result, false, err
		}
		if err = tx.Commit(ctx); err != nil {
			return ContractAssetReceipt{}, false, err
		}
		return result, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return result, false, err
	}
	record, err := loadCLMContract(ctx, tx, source.TenantID, input.ContractID)
	if err != nil {
		return result, false, err
	}
	if record.AuthorityMode != "native" {
		return result, false, fault.Conflict("资产ERP回执必须绑定本地权威合同")
	}
	var revisionID, assetPlanID, itemID, unit string
	var handoverQuantity string
	err = tx.QueryRow(ctx, `SELECT contract_revision_id::text,asset_plan_id::text,item_id::text,unit,quantity::text FROM clm_contract_asset_handovers WHERE tenant_id=$1 AND contract_id=$2 AND handover_id=$3`, source.TenantID, input.ContractID, input.HandoverID).Scan(&revisionID, &assetPlanID, &itemID, &unit, &handoverQuantity)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, false, fault.NotFound("没有找到对应资产移交")
	}
	if err != nil {
		return result, false, err
	}
	if unit != input.Unit {
		return result, false, fault.Conflict("资产回执单位与移交范围不一致")
	}
	if input.EventType == "reversal" {
		var original ContractAssetReceipt
		original, err = loadContractAssetReceipt(ctx, tx, source.TenantID, source.SourceID, input.OriginalEventID)
		if err != nil || original.EventType != "receipt" || original.ContractID != input.ContractID || original.HandoverID != input.HandoverID || original.AssetCode != input.AssetCode || original.Unit != input.Unit {
			return result, false, fault.Invalid("冲正必须引用同来源、同移交及同资产编码/单位的原回执")
		}
		var reversed string
		err = tx.QueryRow(ctx, `SELECT COALESCE(sum(quantity),0)::text FROM clm_contract_asset_receipts WHERE tenant_id=$1 AND source_id=$2 AND original_source_event_id=$3 AND event_type='reversal'`, source.TenantID, source.SourceID, input.OriginalEventID).Scan(&reversed)
		if err != nil {
			return result, false, err
		}
		rev, _ := fulfillmentDecimal("reversed", reversed, true)
		if rev == nil {
			rev = new(big.Rat)
		}
		if new(big.Rat).Add(rev, quantity).Cmp(mustFulfillmentRat(original.Quantity)) > 0 {
			return result, false, fault.Invalid("累计冲正数量超过原资产回执")
		}
	}
	var net string
	err = tx.QueryRow(ctx, `SELECT COALESCE(sum(CASE WHEN event_type='receipt' THEN quantity ELSE -quantity END),0)::text FROM clm_contract_asset_receipts WHERE tenant_id=$1 AND contract_id=$2 AND handover_id=$3`, source.TenantID, input.ContractID, input.HandoverID).Scan(&net)
	if err != nil {
		return result, false, err
	}
	netRat, _ := fulfillmentDecimal("net receipt", net, true)
	if netRat == nil {
		netRat = new(big.Rat)
	}
	if input.EventType == "receipt" && new(big.Rat).Add(netRat, quantity).Cmp(mustFulfillmentRat(handoverQuantity)) > 0 {
		return result, false, fault.Invalid("累计ERP入账数量超过移交数量")
	}
	if input.EventType == "reversal" && new(big.Rat).Sub(netRat, quantity).Sign() < 0 {
		return result, false, fault.Invalid("冲正数量超过当前净入账数量")
	}
	var rowVersion int64
	err = tx.QueryRow(ctx, `UPDATE clm_contracts SET row_version=row_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND contract_id=$2 AND row_version=$3 RETURNING row_version`, record.TenantID, input.ContractID, record.RowVersion).Scan(&rowVersion)
	if err != nil {
		return result, false, err
	}
	receiptID, err := newContractUUID()
	if err != nil {
		return result, false, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO clm_contract_asset_receipts(tenant_id,contract_id,handover_id,receipt_id,source_id,source_key_id,source_event_id,event_type,original_source_event_id,asset_code,quantity,unit,occurred_at,payload_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11::numeric,$12,$13,$14) RETURNING recorded_at`, source.TenantID, input.ContractID, input.HandoverID, receiptID, source.SourceID, source.KeyID, input.EventID, input.EventType, input.OriginalEventID, input.AssetCode, input.Quantity, input.Unit, input.OccurredAt.UTC(), digest).Scan(&result.RecordedAt)
	if err != nil {
		return result, false, err
	}
	result = ContractAssetReceipt{ReceiptID: receiptID, ContractID: input.ContractID, HandoverID: input.HandoverID, SourceID: source.SourceID, SourceKeyID: source.KeyID, SourceEventID: input.EventID, EventType: input.EventType, OriginalSourceEventID: input.OriginalEventID, AssetCode: input.AssetCode, Quantity: input.Quantity, Unit: input.Unit, OccurredAt: input.OccurredAt.UTC(), RecordedAt: result.RecordedAt, PayloadDigest: digest}
	if err = insertCLMApprovalEvent(ctx, tx, record.TenantID, input.ContractID, rowVersion, clmEventAssetReceipt, map[string]any{"receipt_id": receiptID, "handover_id": input.HandoverID, "asset_plan_id": assetPlanID, "contract_revision_id": revisionID, "source_id": source.SourceID, "source_key_id": source.KeyID, "source_event_id": input.EventID, "event_type": input.EventType, "asset_code": input.AssetCode, "quantity": input.Quantity, "unit": input.Unit, "payload_digest": digest}); err != nil {
		return ContractAssetReceipt{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ContractAssetReceipt{}, false, err
	}
	_ = itemID
	return result, false, nil
}

func (s *Store) ListContractAssets(ctx context.Context, user accounts.User, contractID string) (ContractAssetList, error) {
	ctx = commandContext(ctx, user)
	var out ContractAssetList
	if !s.HasDatabase() {
		return out, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	id, err := normalizeContractUUID(contractID)
	if err != nil {
		return out, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return out, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer rollbackCLM(tx)
	if err = setLocalScope(ctx, tx, userScope(current)); err != nil {
		return out, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return out, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return out, fault.NotFound("没有找到对应记录")
	}
	out = ContractAssetList{ContractID: record.ContractID, RowVersion: record.RowVersion, FixedAssetStatus: "unknown", Items: []ContractAssetPlanSummary{}}
	rows, err := tx.Query(ctx, `SELECT contract_revision_id::text,snapshot FROM clm_contract_revisions WHERE tenant_id=$1 AND contract_id=$2 ORDER BY revision_number DESC`, record.TenantID, record.ContractID)
	if err != nil {
		return out, err
	}
	type rev struct {
		id       string
		snapshot []byte
	}
	var revisions []rev
	for rows.Next() {
		var r rev
		if err = rows.Scan(&r.id, &r.snapshot); err != nil {
			rows.Close()
			return out, err
		}
		revisions = append(revisions, r)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	for _, revision := range revisions {
		fixed, rawAssets, assetsAreArray := parseAssetSnapshot(revision.snapshot)
		if record.CurrentRevisionID != nil && *record.CurrentRevisionID == revision.id {
			out.FixedAssetStatus = fixed
		}
		projected, e := loadProjectedAssetPlans(ctx, tx, record.TenantID, record.ContractID, revision.id)
		if e != nil {
			return out, e
		}
		seenPlans := make(map[string]bool, len(projected))
		rowCounts := make(map[string]int, len(rawAssets))
		rows := make([]assetSnapshotRow, 0, len(rawAssets))
		if assetsAreArray {
			for _, raw := range rawAssets {
				row := parseAssetSnapshotRow(raw)
				rows = append(rows, row)
				if id, idErr := normalizeContractUUID(row.AssetPlanID); idErr == nil {
					rowCounts[id]++
				}
			}
		}
		for _, row := range rows {
			planID, idErr := normalizeContractUUID(row.AssetPlanID)
			item := ContractAssetPlanSummary{ContractRevisionID: revision.id, AssetPlanID: row.AssetPlanID, ItemID: row.ItemID, Name: row.Name, PlannedQuantity: row.Quantity, DepartmentCode: row.DepartmentCode, DepartmentName: row.DepartmentName, DepartmentStatus: "unknown", CustodianUserID: row.CustodianUser, FixedAssetStatus: fixed, PlanStatus: "unknown", Handovers: []ContractAssetHandover{}}
			if idErr == nil {
				seenPlans[planID] = true
				if plan, ok := projected[planID]; ok {
					normalizedItemID, normalizeErr := normalizeContractUUID(row.ItemID)
					if row.StructurallyValid && rowCounts[planID] == 1 && normalizeErr == nil && plan.ItemID == normalizedItemID && plan.Quantity == row.Quantity && plan.DepartmentCode == row.DepartmentCode && plan.DepartmentName == row.DepartmentName && plan.CustodianUser == row.CustodianUser {
						item.PlanStatus = "known"
						item.Unit = plan.Unit
					}
				}
				hs, loadErr := loadAssetHandovers(ctx, tx, current, record.TenantID, record.ContractID, revision.id, planID)
				if loadErr != nil {
					return out, loadErr
				}
				item.Handovers = hs
			}
			out.Items = append(out.Items, item)
		}
		// A malformed/non-array legacy field cannot identify rows. Preserve
		// independently projected frozen facts and their histories, but expose
		// them as unknown until the snapshot can be reconciled.
		projectedIDs := make([]string, 0, len(projected))
		for id := range projected {
			projectedIDs = append(projectedIDs, id)
		}
		sort.Strings(projectedIDs)
		for _, id := range projectedIDs {
			plan := projected[id]
			if seenPlans[id] {
				continue
			}
			item := ContractAssetPlanSummary{ContractRevisionID: revision.id, AssetPlanID: id, ItemID: plan.ItemID, PlannedQuantity: plan.Quantity, Unit: plan.Unit, DepartmentCode: plan.DepartmentCode, DepartmentName: plan.DepartmentName, DepartmentStatus: "unknown", CustodianUserID: plan.CustodianUser, FixedAssetStatus: fixed, PlanStatus: "unknown", Handovers: []ContractAssetHandover{}}
			hs, loadErr := loadAssetHandovers(ctx, tx, current, record.TenantID, record.ContractID, revision.id, id)
			if loadErr != nil {
				return out, loadErr
			}
			item.Handovers = hs
			out.Items = append(out.Items, item)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return ContractAssetList{}, err
	}
	return out, nil
}

type assetSnapshotRow struct {
	AssetPlanID       string
	ItemID            string
	Name              string
	Quantity          string
	DepartmentCode    string
	DepartmentName    string
	CustodianUser     string
	StructurallyValid bool
}

func parseAssetSnapshot(snapshot []byte) (string, []json.RawMessage, bool) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(snapshot, &root); err != nil || root == nil {
		return "unknown", nil, false
	}
	fixed := "unknown"
	var contract map[string]json.RawMessage
	if json.Unmarshal(root["contract"], &contract) == nil {
		var isFixed *bool
		if json.Unmarshal(contract["is_fixed_asset"], &isFixed) == nil && isFixed != nil {
			if *isFixed {
				fixed = "true"
			} else {
				fixed = "not_applicable"
			}
		}
	}
	assetsRaw, exists := root["assets"]
	if !exists || string(assetsRaw) == "null" {
		return fixed, []json.RawMessage{}, true
	}
	if !strings.HasPrefix(strings.TrimSpace(string(assetsRaw)), "[") {
		return fixed, nil, false
	}
	var assets []json.RawMessage
	if err := json.Unmarshal(assetsRaw, &assets); err != nil {
		return fixed, nil, false
	}
	return fixed, assets, true
}

func parseAssetSnapshotRow(raw json.RawMessage) assetSnapshotRow {
	var asset DraftAssetPlan
	valid := json.Unmarshal(raw, &asset) == nil
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return assetSnapshotRow{StructurallyValid: false}
	}
	read := func(name string) string {
		var value string
		if json.Unmarshal(fields[name], &value) != nil {
			return ""
		}
		return value
	}
	return assetSnapshotRow{AssetPlanID: read("asset_plan_id"), ItemID: read("item_id"), Name: read("name"), Quantity: read("quantity"), DepartmentCode: read("department_code"), DepartmentName: read("department_name"), CustodianUser: read("custodian_user_id"), StructurallyValid: valid}
}

func loadProjectedAssetPlans(ctx context.Context, tx pgx.Tx, tenant, contract, revision string) (map[string]frozenContractAssetPlan, error) {
	rows, err := tx.Query(ctx, `SELECT asset_plan_id::text,item_id::text,planned_quantity::text,unit,department_code,department_name,custodian_user_id FROM clm_contract_revision_asset_plans WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 ORDER BY asset_plan_id`, tenant, contract, revision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plans := map[string]frozenContractAssetPlan{}
	for rows.Next() {
		var plan frozenContractAssetPlan
		if err = rows.Scan(&plan.AssetPlanID, &plan.ItemID, &plan.Quantity, &plan.Unit, &plan.DepartmentCode, &plan.DepartmentName, &plan.CustodianUser); err != nil {
			return nil, err
		}
		plans[plan.AssetPlanID] = plan
	}
	return plans, rows.Err()
}

func loadAssetHandovers(ctx context.Context, tx pgx.Tx, current accounts.User, tenant, contract, revision, plan string) ([]ContractAssetHandover, error) {
	rows, err := tx.Query(ctx, `SELECT handover_id::text,delivery_id::text,item_id::text,acceptance_id::text,scope_start::text,scope_end::text,quantity::text,unit,handed_over_at,location,transfer_note,evidence_snapshot,department_code_claim,department_name_claim,department_verification,custodian_user_id,custodian_display_name_snapshot,custodian_org_id_snapshot,custodian_directory_source,custodian_directory_revision,custodian_directory_checked_at,custodian_verification,recorded_by,recorded_at,contract_row_version FROM clm_contract_asset_handovers WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND asset_plan_id=$4 ORDER BY recorded_at,handover_id`, tenant, contract, revision, plan)
	if err != nil {
		return nil, err
	}
	type handoverRow struct {
		item     ContractAssetHandover
		evidence []byte
	}
	loaded := []handoverRow{}
	for rows.Next() {
		var h ContractAssetHandover
		var evidence []byte
		if err = rows.Scan(&h.HandoverID, &h.DeliveryID, &h.ItemID, &h.AcceptanceID, &h.ScopeStart, &h.ScopeEnd, &h.Quantity, &h.Unit, &h.HandedOverAt, &h.Location, &h.TransferNote, &evidence, &h.DepartmentCodeClaim, &h.DepartmentNameClaim, &h.DepartmentVerification, &h.CustodianUserID, &h.CustodianDisplayName, &h.CustodianOrgID, &h.CustodianDirectorySource, &h.CustodianDirectoryRevision, &h.CustodianDirectoryChecked, &h.CustodianVerification, &h.RecordedBy, &h.RecordedAt, &h.ContractRowVersion); err != nil {
			return nil, err
		}
		loaded = append(loaded, handoverRow{item: h, evidence: evidence})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]ContractAssetHandover, 0, len(loaded))
	for _, row := range loaded {
		h := row.item
		h.Evidence, h.EvidenceRedacted, err = fulfillmentMaterialSnapshot(ctx, tx, current, row.evidence)
		if err != nil {
			return nil, err
		}
		h.ContractID = contract
		h.ContractRevisionID = revision
		h.AssetPlanID = plan
		h.AssignmentVerification = "unknown"
		if h.DepartmentVerification == "verified" && h.CustodianVerification == "verified" {
			h.AssignmentVerification = "verified"
		}
		h.Receipts, err = loadAssetReceiptsForHandover(ctx, tx, tenant, contract, h.HandoverID)
		if err != nil {
			return nil, err
		}
		h.BookingStatus = assetBookingStatus(h.Quantity, h.Receipts)
		out = append(out, h)
	}
	return out, nil
}

func loadAssetReceiptsForHandover(ctx context.Context, tx pgx.Tx, tenant, contract, handover string) ([]ContractAssetReceipt, error) {
	rows, err := tx.Query(ctx, `SELECT receipt_id::text,source_id,source_key_id,source_event_id,event_type,COALESCE(original_source_event_id,''),asset_code,quantity::text,unit,occurred_at,recorded_at,payload_digest FROM clm_contract_asset_receipts WHERE tenant_id=$1 AND contract_id=$2 AND handover_id=$3 ORDER BY occurred_at,receipt_id`, tenant, contract, handover)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContractAssetReceipt{}
	for rows.Next() {
		var r ContractAssetReceipt
		r.ContractID = contract
		r.HandoverID = handover
		if err = rows.Scan(&r.ReceiptID, &r.SourceID, &r.SourceKeyID, &r.SourceEventID, &r.EventType, &r.OriginalSourceEventID, &r.AssetCode, &r.Quantity, &r.Unit, &r.OccurredAt, &r.RecordedAt, &r.PayloadDigest); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func assetBookingStatus(planned string, receipts []ContractAssetReceipt) string {
	net := new(big.Rat)
	for _, r := range receipts {
		q, err := fulfillmentDecimal("receipt quantity", r.Quantity, true)
		if err != nil {
			return "unknown"
		}
		if r.EventType == "reversal" {
			net.Sub(net, q)
		} else {
			net.Add(net, q)
		}
	}
	if net.Sign() <= 0 {
		return "pending_booking"
	}
	p, err := fulfillmentDecimal("handover quantity", planned, false)
	if err != nil {
		return "unknown"
	}
	if net.Cmp(p) < 0 {
		return "partial_booking"
	}
	if net.Cmp(p) == 0 {
		return "booked"
	}
	return "unknown"
}

func scopedAssetEventLock(ctx context.Context, tx pgx.Tx, tenant, source, event string) error {
	key, err := canonicalHash([]string{tenant, source, event})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':clm-asset-event:' || $1,0))`, key)
	return err
}

func loadContractAssetReceipt(ctx context.Context, tx pgx.Tx, tenant, source, event string) (ContractAssetReceipt, error) {
	var r ContractAssetReceipt
	err := tx.QueryRow(ctx, `SELECT receipt_id::text,contract_id::text,handover_id::text,source_id,source_key_id,source_event_id,event_type,COALESCE(original_source_event_id,''),asset_code,quantity::text,unit,occurred_at,recorded_at,payload_digest FROM clm_contract_asset_receipts WHERE tenant_id=$1 AND source_id=$2 AND source_event_id=$3`, tenant, source, event).Scan(&r.ReceiptID, &r.ContractID, &r.HandoverID, &r.SourceID, &r.SourceKeyID, &r.SourceEventID, &r.EventType, &r.OriginalSourceEventID, &r.AssetCode, &r.Quantity, &r.Unit, &r.OccurredAt, &r.RecordedAt, &r.PayloadDigest)
	return r, err
}

func mustFulfillmentRat(raw string) *big.Rat {
	value, _ := fulfillmentDecimal("quantity", raw, true)
	if value == nil {
		return new(big.Rat)
	}
	return value
}
