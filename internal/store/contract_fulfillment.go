package store

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

const (
	clmRouteCreateDelivery        = "clm.create_delivery"
	clmRouteCreateAcceptance      = "clm.create_acceptance"
	clmRouteCreateRectification   = "clm.create_rectification"
	clmEventDeliveryRecorded      = "contract.delivery_recorded.v1"
	clmEventAcceptanceRecorded    = "contract.acceptance_recorded.v1"
	clmEventRectificationRecorded = "contract.rectification_recorded.v1"
)

var fulfillmentDecimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,11})(?:\.[0-9]{1,6})?$`)

type ContractDeliveryInput struct {
	ContractID           string        `json:"-"`
	ExpectedRowVersion   int64         `json:"expected_row_version"`
	ContractRevisionID   string        `json:"contract_revision_id"`
	DeliveryPlanSequence *int          `json:"delivery_plan_sequence,omitempty"`
	ItemID               string        `json:"item_id"`
	BatchReference       string        `json:"batch_reference"`
	Quantity             string        `json:"quantity"`
	ActualAt             string        `json:"actual_at"`
	Location             string        `json:"location"`
	Description          string        `json:"description,omitempty"`
	Evidence             []MaterialRef `json:"evidence"`
	IdempotencyKey       string        `json:"-"`
}

type ContractDelivery struct {
	DeliveryID           string                    `json:"delivery_id"`
	ContractID           string                    `json:"contract_id"`
	ContractRevisionID   string                    `json:"contract_revision_id"`
	DeliveryPlanSequence *int                      `json:"delivery_plan_sequence,omitempty"`
	ItemID               string                    `json:"item_id"`
	ItemName             string                    `json:"item_name"`
	BatchReference       string                    `json:"batch_reference"`
	Quantity             string                    `json:"quantity"`
	Unit                 string                    `json:"unit"`
	ActualAt             time.Time                 `json:"actual_at"`
	Location             string                    `json:"location"`
	Description          string                    `json:"description"`
	Evidence             []ContractVersionMaterial `json:"evidence"`
	EvidenceRedacted     bool                      `json:"evidence_redacted,omitempty"`
	RecordedBy           string                    `json:"recorded_by"`
	RecordedAt           time.Time                 `json:"recorded_at"`
}

type ContractFulfillmentItemSummary struct {
	ContractRevisionID          string `json:"contract_revision_id"`
	ItemID                      string `json:"item_id"`
	ItemName                    string `json:"item_name"`
	Unit                        string `json:"unit,omitempty"`
	PlannedQuantity             string `json:"planned_quantity,omitempty"`
	DeliveredQuantity           string `json:"delivered_quantity"`
	QualifiedQuantity           string `json:"qualified_quantity"`
	UnallocatedQuantity         string `json:"unallocated_quantity"`
	PendingReinspectionQuantity string `json:"pending_reinspection_quantity"`
}

type ContractDeliveryList struct {
	Items      []ContractDelivery               `json:"items"`
	Summary    []ContractFulfillmentItemSummary `json:"summary"`
	RowVersion int64                            `json:"row_version"`
}

type ContractAcceptanceDefectInput struct {
	PriorDefectID  string `json:"prior_defect_id,omitempty"`
	Description    string `json:"description"`
	Responsible    string `json:"responsible"`
	RemediationDue string `json:"remediation_due"`
	FailedQuantity string `json:"failed_quantity"`
}

type ContractAcceptanceDefect struct {
	DefectID       string    `json:"defect_id"`
	PriorDefectID  string    `json:"prior_defect_id,omitempty"`
	Description    string    `json:"description"`
	Responsible    string    `json:"responsible"`
	RemediationDue string    `json:"remediation_due"`
	FailedQuantity string    `json:"failed_quantity"`
	RecordedBy     string    `json:"recorded_by"`
	RecordedAt     time.Time `json:"recorded_at"`
}

type ContractAcceptanceInput struct {
	ContractID             string                          `json:"-"`
	ExpectedRowVersion     int64                           `json:"expected_row_version"`
	ContractRevisionID     string                          `json:"contract_revision_id"`
	DeliveryID             string                          `json:"delivery_id"`
	AcceptancePlanSequence *int                            `json:"acceptance_plan_sequence,omitempty"`
	ScopeStart             string                          `json:"scope_start"`
	ScopeEnd               string                          `json:"scope_end"`
	AcceptedQuantity       string                          `json:"accepted_quantity"`
	ReinspectionOf         string                          `json:"reinspection_of,omitempty"`
	RectificationIDs       []string                        `json:"rectification_ids,omitempty"`
	ActualAt               string                          `json:"actual_at"`
	Standard               string                          `json:"standard"`
	InspectorNote          string                          `json:"inspector_note,omitempty"`
	Defects                []ContractAcceptanceDefectInput `json:"defects,omitempty"`
	Evidence               []MaterialRef                   `json:"evidence"`
	IdempotencyKey         string                          `json:"-"`
}

type ContractAcceptance struct {
	AcceptanceID           string                     `json:"acceptance_id"`
	ContractID             string                     `json:"contract_id"`
	ContractRevisionID     string                     `json:"contract_revision_id"`
	DeliveryID             string                     `json:"delivery_id"`
	ItemID                 string                     `json:"item_id"`
	ItemName               string                     `json:"item_name"`
	AcceptancePlanSequence *int                       `json:"acceptance_plan_sequence,omitempty"`
	ScopeStart             string                     `json:"scope_start"`
	ScopeEnd               string                     `json:"scope_end"`
	Quantity               string                     `json:"quantity"`
	AcceptedQuantity       string                     `json:"accepted_quantity"`
	FailedQuantity         string                     `json:"failed_quantity"`
	Unit                   string                     `json:"unit"`
	Result                 string                     `json:"result"`
	ReinspectionOf         string                     `json:"reinspection_of,omitempty"`
	RectificationIDs       []string                   `json:"rectification_ids,omitempty"`
	ActualAt               time.Time                  `json:"actual_at"`
	Standard               string                     `json:"standard"`
	InspectorNote          string                     `json:"inspector_note"`
	Evidence               []ContractVersionMaterial  `json:"evidence"`
	EvidenceRedacted       bool                       `json:"evidence_redacted,omitempty"`
	Defects                []ContractAcceptanceDefect `json:"defects"`
	Rectifications         []ContractRectification    `json:"rectifications"`
	RecordedBy             string                     `json:"recorded_by"`
	RecordedAt             time.Time                  `json:"recorded_at"`
}

type ContractAcceptanceList struct {
	Items      []ContractAcceptance             `json:"items"`
	Summary    []ContractFulfillmentItemSummary `json:"summary"`
	RowVersion int64                            `json:"row_version"`
}

type ContractRectificationInput struct {
	ContractID         string        `json:"-"`
	ExpectedRowVersion int64         `json:"expected_row_version"`
	ContractRevisionID string        `json:"contract_revision_id"`
	AcceptanceID       string        `json:"acceptance_id"`
	DefectID           string        `json:"defect_id"`
	Description        string        `json:"description"`
	Evidence           []MaterialRef `json:"evidence"`
	IdempotencyKey     string        `json:"-"`
}

type ContractRectification struct {
	RectificationID    string                    `json:"rectification_id"`
	ContractID         string                    `json:"contract_id"`
	ContractRevisionID string                    `json:"contract_revision_id"`
	AcceptanceID       string                    `json:"acceptance_id"`
	DefectID           string                    `json:"defect_id"`
	Description        string                    `json:"description"`
	Evidence           []ContractVersionMaterial `json:"evidence"`
	EvidenceRedacted   bool                      `json:"evidence_redacted,omitempty"`
	RecordedBy         string                    `json:"recorded_by"`
	RecordedAt         time.Time                 `json:"recorded_at"`
}

type ContractRectificationList struct {
	Items []ContractRectification `json:"items"`
}

type revisionItemInfo struct {
	ItemID   string `json:"item_id"`
	Name     string `json:"name,omitempty"`
	Quantity string `json:"quantity,omitempty"`
	Unit     string `json:"unit,omitempty"`
}

func insertFrozenContractRevisionItems(ctx context.Context, tx pgx.Tx, tenantID, contractID, revisionID string, items []DraftItem) error {
	counts := make(map[string]int, len(items))
	for _, item := range items {
		if itemID, err := normalizeContractUUID(item.ItemID); err == nil {
			counts[itemID]++
		}
	}
	for _, item := range items {
		itemID, err := normalizeContractUUID(item.ItemID)
		if err != nil {
			continue
		}
		// Repeated IDs cannot identify a unique frozen line. Preserve the
		// original snapshot and let fulfillment report the plan as unknown.
		if counts[itemID] != 1 {
			continue
		}
		var quantity any
		if parsed, err := fulfillmentDecimal("items[].quantity", strings.TrimSpace(item.Quantity), false); err == nil && parsed.Sign() > 0 {
			quantity = item.Quantity
		}
		var unit any
		trimmedUnit := strings.TrimSpace(item.Unit)
		if trimmedUnit != "" && len([]rune(trimmedUnit)) <= 40 {
			unit = trimmedUnit
		}
		if _, err := tx.Exec(ctx, `INSERT INTO clm_contract_revision_items
			(tenant_id,contract_id,contract_revision_id,item_id,planned_quantity,unit)
			VALUES ($1,$2,$3,$4,$5,$6)`, tenantID, contractID, revisionID, itemID, quantity, unit); err != nil {
			return err
		}
	}
	return nil
}

func parseFulfillmentTime(field, raw string) (time.Time, error) {
	value, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, fault.Invalid(field + "必须是含时区的 RFC3339 时间")
	}
	return value.UTC(), nil
}

func fulfillmentDecimal(field, raw string, allowZero bool) (*big.Rat, error) {
	if !fulfillmentDecimalPattern.MatchString(raw) {
		return nil, fault.Invalid(field + "必须是18位以内、6位小数的非负十进制字符串")
	}
	value, ok := new(big.Rat).SetString(raw)
	if !ok || (!allowZero && value.Sign() <= 0) {
		return nil, fault.Invalid(field + "必须大于0")
	}
	return value, nil
}

func ratString(value *big.Rat) string {
	if value == nil {
		return "0.000000"
	}
	return value.FloatString(6)
}

func fulfillmentResult(quantity, accepted string) (string, *big.Rat, *big.Rat, error) {
	q, err := fulfillmentDecimal("quantity", quantity, false)
	if err != nil {
		return "", nil, nil, err
	}
	a, err := fulfillmentDecimal("accepted_quantity", accepted, true)
	if err != nil {
		return "", nil, nil, err
	}
	if a.Cmp(q) > 0 {
		return "", nil, nil, fault.Invalid("accepted_quantity不能超过本次验收数量")
	}
	failed := new(big.Rat).Sub(q, a)
	if a.Sign() == 0 {
		return "failed", q, a, nil
	}
	if failed.Sign() == 0 {
		return "passed", q, a, nil
	}
	return "partial", q, a, nil
}

func validateFulfillmentText(field, value string, max int, required bool) error {
	value = strings.TrimSpace(value)
	if (required && value == "") || len([]rune(value)) > max {
		return fault.Invalid(field + "不能为空且不能超过限制长度")
	}
	return nil
}

func resolveFulfillmentEvidence(ctx context.Context, tx pgx.Tx, current accounts.User, refs []MaterialRef, purpose string) ([]ContractVersionMaterial, error) {
	if len(refs) == 0 || len(refs) > 50 {
		return nil, fault.Invalid("履约事实必须引用1至50个案件材料版本")
	}
	seen := map[string]struct{}{}
	result := make([]ContractVersionMaterial, 0, len(refs))
	for _, ref := range refs {
		key := ref.MaterialID + "\x00" + ref.MaterialVersionID
		if _, ok := seen[key]; ok {
			return nil, fault.Invalid("履约证据不能重复引用同一材料版本")
		}
		seen[key] = struct{}{}
		if ref.Purpose != purpose && ref.Purpose != "supporting" && ref.Purpose != "other" {
			return nil, fault.Invalid("履约证据用途不匹配")
		}
		resolved, err := resolveMaterialVersion(ctx, tx, accounts.Tenant(current), ref)
		if err != nil {
			return nil, err
		}
		if err := requireSigningCaseRead(ctx, tx, current, resolved.CaseOrgID, resolved.CaseID); err != nil {
			return nil, err
		}
		result = append(result, ContractVersionMaterial{MaterialID: resolved.MaterialID, MaterialVersionID: resolved.DocumentID,
			MaterialVersion: resolved.Version, Filename: resolved.Filename, Digest: resolved.Digest, Purpose: purpose,
			CaseID: resolved.CaseID, CaseOrgID: resolved.CaseOrgID})
	}
	return result, nil
}

func requireCLMFulfillmentManage(record clmContractRecord, current accounts.User) error {
	if !authorizeContract(record, current, access.Read) {
		return fault.NotFound("没有找到对应记录")
	}
	if !authorizeContract(record, current, access.Manage) {
		return fault.Forbidden("当前授权不能管理合同履约事实")
	}
	if record.AuthorityMode != "native" {
		return fault.Forbidden("外部权威合同不能在本地登记履约事实")
	}
	if record.ValidityState != "active" || (record.LifecycleState != "registered" && record.LifecycleState != "performing") {
		return fault.Conflict("只有有效的已登记或履行中合同可以记录履约事实")
	}
	return nil
}

func loadFrozenFulfillmentDraft(ctx context.Context, tx pgx.Tx, tenantID, contractID, revisionID string) (DraftContent, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT snapshot FROM clm_contract_revisions
		WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3`, tenantID, contractID, revisionID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return DraftContent{}, fault.Conflict("合同冻结修订不存在")
	}
	if err != nil {
		return DraftContent{}, err
	}
	var draft DraftContent
	if err := json.Unmarshal(raw, &draft); err != nil {
		return DraftContent{}, err
	}
	return draft, nil
}

func loadFrozenRevisionItem(ctx context.Context, tx pgx.Tx, tenantID, contractID, revisionID, itemID string) (revisionItemInfo, error) {
	var item revisionItemInfo
	var planned *string
	var unit *string
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT item.planned_quantity::text,item.unit,rev.snapshot
		FROM clm_contract_revision_items item JOIN clm_contract_revisions rev
		USING (tenant_id,contract_id,contract_revision_id)
		WHERE item.tenant_id=$1 AND item.contract_id=$2 AND item.contract_revision_id=$3 AND item.item_id=$4`,
		tenantID, contractID, revisionID, itemID).Scan(&planned, &unit, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, fault.Invalid("商品行不属于指定冻结修订")
	}
	if err != nil {
		return item, err
	}
	metadata, err := parseRevisionItemMetadata(raw)
	if err != nil {
		return item, err
	}
	if candidate, ok := metadata[itemID]; ok {
		item = candidate
	}
	if item.ItemID == "" {
		return item, fault.Invalid("冻结修订商品行快照缺失")
	}
	// The revision-item projection is authoritative, including NULL. Falling
	// back to source JSON here could make an invalid legacy plan registrable.
	item.Quantity, item.Unit = "", ""
	if planned != nil {
		item.Quantity = *planned
	}
	if unit != nil {
		item.Unit = *unit
	}
	return item, nil
}

func planForItem(draft DraftContent, sequence *int, itemID string) (*DraftDeliveryPlan, error) {
	if len(draft.DeliveryPlans) == 0 && sequence == nil {
		return nil, nil
	}
	if sequence == nil {
		return nil, fault.Invalid("必须选择冻结交付计划期次")
	}
	for i := range draft.DeliveryPlans {
		plan := &draft.DeliveryPlans[i]
		if plan.Sequence != *sequence {
			continue
		}
		if plan.ItemID != itemID {
			return nil, fault.Invalid("交付计划商品行与冻结 item_id 不匹配")
		}
		return plan, nil
	}
	return nil, fault.Invalid("交付计划期次不属于冻结修订")
}

func acceptancePlanForItem(draft DraftContent, sequence *int, itemID string) (*DraftAcceptancePlan, error) {
	if len(draft.AcceptancePlans) == 0 && sequence == nil {
		return nil, nil
	}
	if sequence == nil {
		return nil, fault.Invalid("必须选择冻结验收计划期次")
	}
	for i := range draft.AcceptancePlans {
		plan := &draft.AcceptancePlans[i]
		if plan.Sequence != *sequence {
			continue
		}
		if len(plan.ItemIDs) > 0 {
			for _, candidate := range plan.ItemIDs {
				if candidate == itemID {
					return plan, nil
				}
			}
			return nil, fault.Invalid("验收计划不覆盖该冻结商品行")
		}
		return plan, nil
	}
	return nil, fault.Invalid("验收计划期次不属于冻结修订")
}

func sortedEvidence(in []ContractVersionMaterial) []ContractVersionMaterial {
	out := append([]ContractVersionMaterial(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].MaterialID == out[j].MaterialID {
			return out[i].MaterialVersionID < out[j].MaterialVersionID
		}
		return out[i].MaterialID < out[j].MaterialID
	})
	return out
}

func fulfillmentRequestDigest(route, method, path string, expected int64, input any) (string, error) {
	return clmRequestDigest(route, method, path, expected, input)
}

func fulfillmentWriteRecord(ctx context.Context, tx pgx.Tx, current accounts.User, contractID string) (clmContractRecord, error) {
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), contractID)
	if err != nil {
		return clmContractRecord{}, err
	}
	if err := requireCLMFulfillmentManage(record, current); err != nil {
		return clmContractRecord{}, err
	}
	return record, nil
}

func bumpFulfillmentRowVersion(ctx context.Context, tx pgx.Tx, record clmContractRecord, expected int64) (int64, error) {
	var next int64
	err := tx.QueryRow(ctx, `UPDATE clm_contracts SET row_version=row_version+1,
		lifecycle_state=CASE WHEN lifecycle_state='registered' THEN 'performing' ELSE lifecycle_state END,
		updated_at=clock_timestamp()
		WHERE tenant_id=$1 AND contract_id=$2 AND row_version=$3
		  AND authority_mode='native' AND validity_state='active'
		RETURNING row_version`, record.TenantID, record.ContractID, expected).Scan(&next)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fault.Conflict("合同已被修改，请刷新后重新确认")
	}
	return next, err
}

func fulfillmentMaterialSnapshot(ctx context.Context, tx pgx.Tx, current accounts.User, raw []byte) ([]ContractVersionMaterial, bool, error) {
	var all []ContractVersionMaterial
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, false, err
	}
	visible := make([]ContractVersionMaterial, 0, len(all))
	redacted := false
	for _, material := range all {
		if err := requireSigningCaseRead(ctx, tx, current, material.CaseOrgID, material.CaseID); err != nil {
			var known *fault.Error
			if errors.As(err, &known) && (known.Status == 403 || known.Status == 404) {
				redacted = true
				continue
			}
			return nil, false, err
		}
		visible = append(visible, material)
	}
	return visible, redacted, nil
}

func parseRevisionItemNames(ctx context.Context, tx pgx.Tx, tenantID, contractID, revisionID string) (map[string]revisionItemInfo, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT snapshot FROM clm_contract_revisions WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3`, tenantID, contractID, revisionID).Scan(&raw); err != nil {
		return nil, err
	}
	return parseRevisionItemMetadata(raw)
}

func parseRevisionItemMetadata(snapshot []byte) (map[string]revisionItemInfo, error) {
	result := map[string]revisionItemInfo{}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(snapshot, &root); err != nil {
		return result, nil
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(root["items"], &rawItems); err != nil {
		return result, nil
	}
	counts := map[string]int{}
	entries := make([]revisionItemInfo, 0, len(rawItems))
	for _, raw := range rawItems {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
			continue
		}
		var rawID string
		if err := json.Unmarshal(fields["item_id"], &rawID); err != nil {
			continue
		}
		id, err := normalizeContractUUID(rawID)
		if err != nil {
			continue
		}
		decodeString := func(key string) string { var value string; _ = json.Unmarshal(fields[key], &value); return value }
		entry := revisionItemInfo{ItemID: id, Name: decodeString("name"), Quantity: decodeString("quantity"), Unit: decodeString("unit")}
		counts[id]++
		entries = append(entries, entry)
	}
	for _, entry := range entries {
		if counts[entry.ItemID] == 1 {
			result[entry.ItemID] = entry
		}
	}
	return result, nil
}

func (s *Store) CreateContractDelivery(ctx context.Context, user accounts.User, input ContractDeliveryInput) (ContractDelivery, error) {
	contractID, err := normalizeContractUUID(input.ContractID)
	if err != nil {
		return ContractDelivery{}, err
	}
	input.ContractID = contractID
	if input.ExpectedRowVersion < 1 {
		return ContractDelivery{}, fault.Invalid("expected_row_version 必须为正数")
	}
	if err := normalizeContractUUIDField("contract_revision_id", &input.ContractRevisionID); err != nil {
		return ContractDelivery{}, err
	}
	if err := normalizeContractUUIDField("item_id", &input.ItemID); err != nil {
		return ContractDelivery{}, err
	}
	input.BatchReference = strings.TrimSpace(input.BatchReference)
	if err := validateFulfillmentText("batch_reference", input.BatchReference, 200, true); err != nil {
		return ContractDelivery{}, err
	}
	if _, err := fulfillmentDecimal("quantity", input.Quantity, false); err != nil {
		return ContractDelivery{}, err
	}
	if err := validateFulfillmentText("location", input.Location, 1000, true); err != nil {
		return ContractDelivery{}, err
	}
	if err := validateFulfillmentText("description", input.Description, 4000, false); err != nil {
		return ContractDelivery{}, err
	}
	actualAt, err := parseFulfillmentTime("actual_at", input.ActualAt)
	if err != nil {
		return ContractDelivery{}, err
	}
	if err := validateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return ContractDelivery{}, err
	}
	digest, err := fulfillmentRequestDigest(clmRouteCreateDelivery, "POST", "/api/contracts/"+contractID+"/deliveries", input.ExpectedRowVersion, input)
	if err != nil {
		return ContractDelivery{}, err
	}
	if !s.HasDatabase() {
		return ContractDelivery{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能记录合同交付", nil)
	if err != nil {
		return ContractDelivery{}, err
	}
	defer rollbackCLM(tx)
	if err := lockWriteReceipt(ctx, tx, current, clmRouteCreateDelivery, input.IdempotencyKey); err != nil {
		return ContractDelivery{}, err
	}
	receipt, found, err := replayCLMWriteReceipt(ctx, tx, current, clmRouteCreateDelivery, input.IdempotencyKey, digest, contractID)
	if err != nil {
		return ContractDelivery{}, err
	}
	if found {
		var result ContractDelivery
		if err := json.Unmarshal(receipt.Response, &result); err != nil {
			return ContractDelivery{}, err
		}
		for _, evidence := range result.Evidence {
			if err := requireSigningCaseRead(ctx, tx, current, evidence.CaseOrgID, evidence.CaseID); err != nil {
				return ContractDelivery{}, fault.NotFound("没有找到对应记录")
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return ContractDelivery{}, err
		}
		return result, nil
	}
	if err := scopedContractLock(ctx, tx, contractID); err != nil {
		return ContractDelivery{}, err
	}
	record, err := fulfillmentWriteRecord(ctx, tx, current, contractID)
	if err != nil {
		return ContractDelivery{}, err
	}
	if record.RowVersion != input.ExpectedRowVersion {
		return ContractDelivery{}, fault.Conflict("合同已被修改，请刷新后重试")
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != input.ContractRevisionID {
		return ContractDelivery{}, fault.Conflict("交付必须对应合同当前冻结修订")
	}
	snapshot, err := loadFrozenFulfillmentDraft(ctx, tx, record.TenantID, contractID, input.ContractRevisionID)
	if err != nil {
		return ContractDelivery{}, err
	}
	plan, err := planForItem(snapshot, input.DeliveryPlanSequence, input.ItemID)
	if err != nil {
		return ContractDelivery{}, err
	}
	item, err := loadFrozenRevisionItem(ctx, tx, record.TenantID, contractID, input.ContractRevisionID, input.ItemID)
	if err != nil {
		return ContractDelivery{}, err
	}
	if item.Quantity == "" || item.Unit == "" {
		return ContractDelivery{}, fault.Conflict("冻结商品行未提供可核验的计划数量和单位")
	}
	quantity, err := fulfillmentDecimal("quantity", input.Quantity, false)
	if err != nil {
		return ContractDelivery{}, err
	}
	var totalText string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(quantity),0)::text FROM clm_contract_deliveries
		WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND item_id=$4`,
		record.TenantID, contractID, input.ContractRevisionID, input.ItemID).Scan(&totalText); err != nil {
		return ContractDelivery{}, err
	}
	planned, _ := new(big.Rat).SetString(item.Quantity)
	delivered, _ := new(big.Rat).SetString(totalText)
	if new(big.Rat).Add(delivered, quantity).Cmp(planned) > 0 {
		return ContractDelivery{}, fault.Conflict("累计实际交付数量超过冻结商品行计划数量")
	}
	if plan != nil && plan.Quantity != "" {
		var planTotalText string
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(quantity),0)::text FROM clm_contract_deliveries
			WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND item_id=$4 AND delivery_plan_sequence=$5`,
			record.TenantID, contractID, input.ContractRevisionID, input.ItemID, *input.DeliveryPlanSequence).Scan(&planTotalText); err != nil {
			return ContractDelivery{}, err
		}
		planLimit, ok := new(big.Rat).SetString(plan.Quantity)
		prior, priorOK := new(big.Rat).SetString(planTotalText)
		if !ok || !priorOK || new(big.Rat).Add(prior, quantity).Cmp(planLimit) > 0 {
			return ContractDelivery{}, fault.Conflict("累计实际交付数量超过所选冻结交付计划数量")
		}
	}
	evidence, err := resolveFulfillmentEvidence(ctx, tx, current, input.Evidence, "supporting")
	if err != nil {
		return ContractDelivery{}, err
	}
	evidenceJSON, err := json.Marshal(sortedEvidence(evidence))
	if err != nil {
		return ContractDelivery{}, err
	}
	deliveryID, err := newContractUUID()
	if err != nil {
		return ContractDelivery{}, err
	}
	var recordedAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO clm_contract_deliveries
		(tenant_id,contract_id,contract_revision_id,delivery_id,delivery_plan_sequence,item_id,batch_reference,quantity,unit,actual_at,location,description,evidence_snapshot,recorded_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::numeric,$9,$10,$11,$12,$13::jsonb,$14) RETURNING recorded_at`,
		record.TenantID, contractID, input.ContractRevisionID, deliveryID, input.DeliveryPlanSequence, input.ItemID,
		input.BatchReference, input.Quantity, item.Unit, actualAt, strings.TrimSpace(input.Location), strings.TrimSpace(input.Description), string(evidenceJSON), current.ID).Scan(&recordedAt)
	if err != nil {
		if strings.Contains(err.Error(), "clm_contract_deliveries") {
			return ContractDelivery{}, fault.Conflict("该批次引用已用于此合同商品行")
		}
		return ContractDelivery{}, err
	}
	rowVersion, err := bumpFulfillmentRowVersion(ctx, tx, record, input.ExpectedRowVersion)
	if err != nil {
		return ContractDelivery{}, err
	}
	result := ContractDelivery{DeliveryID: deliveryID, ContractID: contractID, ContractRevisionID: input.ContractRevisionID,
		DeliveryPlanSequence: input.DeliveryPlanSequence, ItemID: input.ItemID, ItemName: item.Name, BatchReference: input.BatchReference,
		Quantity: input.Quantity, Unit: item.Unit, ActualAt: actualAt, Location: strings.TrimSpace(input.Location), Description: strings.TrimSpace(input.Description),
		Evidence: sortedEvidence(evidence), RecordedBy: current.ID, RecordedAt: recordedAt}
	response, err := json.Marshal(result)
	if err != nil {
		return ContractDelivery{}, err
	}
	if err := insertCLMApprovalEvent(ctx, tx, record.TenantID, contractID, rowVersion, clmEventDeliveryRecorded, map[string]any{
		"contract_revision_id": input.ContractRevisionID, "delivery_id": deliveryID, "item_id": input.ItemID,
		"quantity": input.Quantity, "unit": item.Unit, "recorded_by": current.ID,
	}); err != nil {
		return ContractDelivery{}, err
	}
	if err := insertCLMWriteReceipt(ctx, tx, record.TenantID, current.ID, clmRouteCreateDelivery, input.IdempotencyKey, digest, contractID, 201, response); err != nil {
		return ContractDelivery{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractDelivery{}, err
	}
	return result, nil
}

func (s *Store) ListContractDeliveries(ctx context.Context, user accounts.User, contractID string) (ContractDeliveryList, error) {
	id, err := normalizeContractUUID(contractID)
	if err != nil {
		return ContractDeliveryList{}, err
	}
	if s.pool == nil {
		return ContractDeliveryList{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return ContractDeliveryList{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ContractDeliveryList{}, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ContractDeliveryList{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ContractDeliveryList{}, err
	}
	if !authorizeContract(record, current, access.Read) {
		return ContractDeliveryList{}, fault.NotFound("没有找到对应记录")
	}
	rows, err := tx.Query(ctx, `SELECT delivery.delivery_id::text,delivery.contract_revision_id::text,delivery.delivery_plan_sequence,
		delivery.item_id::text,delivery.batch_reference,delivery.quantity::text,delivery.unit,delivery.actual_at,
		delivery.location,delivery.description,delivery.evidence_snapshot,delivery.recorded_by,delivery.recorded_at
		FROM clm_contract_deliveries delivery WHERE delivery.tenant_id=$1 AND delivery.contract_id=$2
		ORDER BY delivery.actual_at,delivery.delivery_id`, record.TenantID, id)
	if err != nil {
		return ContractDeliveryList{}, err
	}
	items := []ContractDelivery{}
	type deliveryRow struct {
		item     ContractDelivery
		evidence []byte
	}
	loaded := []deliveryRow{}
	for rows.Next() {
		var item ContractDelivery
		var evidenceRaw []byte
		if err := rows.Scan(&item.DeliveryID, &item.ContractRevisionID, &item.DeliveryPlanSequence, &item.ItemID, &item.BatchReference, &item.Quantity, &item.Unit, &item.ActualAt, &item.Location, &item.Description, &evidenceRaw, &item.RecordedBy, &item.RecordedAt); err != nil {
			rows.Close()
			return ContractDeliveryList{}, err
		}
		loaded = append(loaded, deliveryRow{item: item, evidence: evidenceRaw})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ContractDeliveryList{}, err
	}
	nameCache := map[string]map[string]revisionItemInfo{}
	for _, row := range loaded {
		item, evidenceRaw := row.item, row.evidence
		if _, ok := nameCache[item.ContractRevisionID]; !ok {
			names, err := parseRevisionItemNames(ctx, tx, record.TenantID, id, item.ContractRevisionID)
			if err != nil {
				return ContractDeliveryList{}, err
			}
			nameCache[item.ContractRevisionID] = names
		}
		if named, ok := nameCache[item.ContractRevisionID][item.ItemID]; ok {
			item.ItemName = named.Name
		}
		visible, redacted, err := fulfillmentMaterialSnapshot(ctx, tx, current, evidenceRaw)
		if err != nil {
			return ContractDeliveryList{}, err
		}
		item.Evidence, item.EvidenceRedacted = visible, redacted
		items = append(items, item)
	}
	summary, err := loadFulfillmentSummary(ctx, tx, record.TenantID, id)
	if err != nil {
		return ContractDeliveryList{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractDeliveryList{}, err
	}
	return ContractDeliveryList{Items: items, Summary: summary, RowVersion: record.RowVersion}, nil
}

func loadFulfillmentSummary(ctx context.Context, tx pgx.Tx, tenantID, contractID string) ([]ContractFulfillmentItemSummary, error) {
	rows, err := tx.Query(ctx, `WITH delivered AS (
		SELECT tenant_id,contract_id,contract_revision_id,item_id,sum(quantity) AS quantity
		FROM clm_contract_deliveries WHERE tenant_id=$1 AND contract_id=$2 GROUP BY tenant_id,contract_id,contract_revision_id,item_id
	), allocated AS (
		SELECT tenant_id,contract_id,contract_revision_id,item_id,sum(scope_end-scope_start) AS quantity
		FROM clm_contract_acceptances WHERE tenant_id=$1 AND contract_id=$2 AND reinspection_of IS NULL GROUP BY tenant_id,contract_id,contract_revision_id,item_id
	), qualified AS (
		SELECT tenant_id,contract_id,contract_revision_id,item_id,sum(accepted_quantity) AS quantity
		FROM clm_contract_acceptances WHERE tenant_id=$1 AND contract_id=$2 GROUP BY tenant_id,contract_id,contract_revision_id,item_id
	), pending_reinspection AS (
		SELECT acceptance.tenant_id,acceptance.contract_id,acceptance.contract_revision_id,acceptance.item_id,
			sum(acceptance.scope_end-acceptance.scope_start-acceptance.accepted_quantity) AS quantity
		FROM clm_contract_acceptances acceptance
		LEFT JOIN clm_contract_acceptances child ON child.tenant_id=acceptance.tenant_id
			AND child.contract_id=acceptance.contract_id AND child.reinspection_of=acceptance.acceptance_id
		WHERE acceptance.tenant_id=$1 AND acceptance.contract_id=$2 AND child.acceptance_id IS NULL
			AND acceptance.result <> 'passed'
		GROUP BY acceptance.tenant_id,acceptance.contract_id,acceptance.contract_revision_id,acceptance.item_id
	)
	SELECT item.contract_revision_id::text,item.item_id::text,rev.snapshot,
		COALESCE(item.unit,''),COALESCE(item.planned_quantity::text,''),COALESCE(delivered.quantity,0)::text,
		COALESCE(qualified.quantity,0)::text,
		GREATEST(COALESCE(delivered.quantity,0)-COALESCE(allocated.quantity,0),0)::text,
		COALESCE(pending_reinspection.quantity,0)::text
	FROM clm_contract_revision_items item
	JOIN clm_contract_revisions rev USING (tenant_id,contract_id,contract_revision_id)
	LEFT JOIN delivered USING (tenant_id,contract_id,contract_revision_id,item_id)
	LEFT JOIN allocated USING (tenant_id,contract_id,contract_revision_id,item_id)
	LEFT JOIN qualified USING (tenant_id,contract_id,contract_revision_id,item_id)
	LEFT JOIN pending_reinspection USING (tenant_id,contract_id,contract_revision_id,item_id)
	WHERE item.tenant_id=$1 AND item.contract_id=$2
	ORDER BY item.contract_revision_id,item.item_id`, tenantID, contractID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ContractFulfillmentItemSummary{}
	for rows.Next() {
		var summary ContractFulfillmentItemSummary
		var itemID, revisionID, name string
		var rawItems []byte
		if err := rows.Scan(&revisionID, &itemID, &rawItems, &summary.Unit, &summary.PlannedQuantity, &summary.DeliveredQuantity, &summary.QualifiedQuantity, &summary.UnallocatedQuantity, &summary.PendingReinspectionQuantity); err != nil {
			return nil, err
		}
		metadata, err := parseRevisionItemMetadata(rawItems)
		if err != nil {
			return nil, err
		}
		if item, ok := metadata[itemID]; ok {
			name = item.Name
		}
		summary.ContractRevisionID, summary.ItemID, summary.ItemName = revisionID, itemID, name
		result = append(result, summary)
	}
	return result, rows.Err()
}

func (s *Store) CreateContractAcceptance(ctx context.Context, user accounts.User, input ContractAcceptanceInput) (ContractAcceptance, error) {
	contractID, err := normalizeContractUUID(input.ContractID)
	if err != nil {
		return ContractAcceptance{}, err
	}
	input.ContractID = contractID
	if input.ExpectedRowVersion < 1 {
		return ContractAcceptance{}, fault.Invalid("expected_row_version 必须为正数")
	}
	if err := normalizeContractUUIDField("contract_revision_id", &input.ContractRevisionID); err != nil {
		return ContractAcceptance{}, err
	}
	if err := normalizeContractUUIDField("delivery_id", &input.DeliveryID); err != nil {
		return ContractAcceptance{}, err
	}
	if input.ReinspectionOf != "" {
		if err := normalizeContractUUIDField("reinspection_of", &input.ReinspectionOf); err != nil {
			return ContractAcceptance{}, err
		}
	}
	if err := validateFulfillmentText("standard", input.Standard, 4000, true); err != nil {
		return ContractAcceptance{}, err
	}
	if err := validateFulfillmentText("inspector_note", input.InspectorNote, 4000, false); err != nil {
		return ContractAcceptance{}, err
	}
	if len(input.Defects) > 100 {
		return ContractAcceptance{}, fault.Invalid("每次验收缺陷不能超过100项")
	}
	actualAt, err := parseFulfillmentTime("actual_at", input.ActualAt)
	if err != nil {
		return ContractAcceptance{}, err
	}
	// Validate the acceptance prefix grammar independently; its total is derived
	// from exact endpoints after the delivery range is locked.
	if _, err := fulfillmentDecimal("scope_start", input.ScopeStart, true); err != nil {
		return ContractAcceptance{}, err
	}
	if _, err := fulfillmentDecimal("scope_end", input.ScopeEnd, false); err != nil {
		return ContractAcceptance{}, err
	}
	if _, err := fulfillmentDecimal("accepted_quantity", input.AcceptedQuantity, true); err != nil {
		return ContractAcceptance{}, err
	}
	if err := validateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return ContractAcceptance{}, err
	}
	digest, err := fulfillmentRequestDigest(clmRouteCreateAcceptance, "POST", "/api/contracts/"+contractID+"/acceptances", input.ExpectedRowVersion, input)
	if err != nil {
		return ContractAcceptance{}, err
	}
	if !s.HasDatabase() {
		return ContractAcceptance{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能登记合同验收", nil)
	if err != nil {
		return ContractAcceptance{}, err
	}
	defer rollbackCLM(tx)
	if err := lockWriteReceipt(ctx, tx, current, clmRouteCreateAcceptance, input.IdempotencyKey); err != nil {
		return ContractAcceptance{}, err
	}
	receipt, found, err := replayCLMWriteReceipt(ctx, tx, current, clmRouteCreateAcceptance, input.IdempotencyKey, digest, contractID)
	if err != nil {
		return ContractAcceptance{}, err
	}
	if found {
		var saved ContractAcceptance
		if err := json.Unmarshal(receipt.Response, &saved); err != nil {
			return ContractAcceptance{}, err
		}
		for _, evidence := range saved.Evidence {
			if err := requireSigningCaseRead(ctx, tx, current, evidence.CaseOrgID, evidence.CaseID); err != nil {
				return ContractAcceptance{}, fault.NotFound("没有找到对应记录")
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return ContractAcceptance{}, err
		}
		return saved, nil
	}
	if err := scopedContractLock(ctx, tx, contractID); err != nil {
		return ContractAcceptance{}, err
	}
	record, err := fulfillmentWriteRecord(ctx, tx, current, contractID)
	if err != nil {
		return ContractAcceptance{}, err
	}
	if record.RowVersion != input.ExpectedRowVersion {
		return ContractAcceptance{}, fault.Conflict("合同已被修改，请刷新后重试")
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != input.ContractRevisionID {
		return ContractAcceptance{}, fault.Conflict("验收必须对应合同当前冻结修订")
	}
	snapshot, err := loadFrozenFulfillmentDraft(ctx, tx, record.TenantID, contractID, input.ContractRevisionID)
	if err != nil {
		return ContractAcceptance{}, err
	}
	var itemID, itemName, unit, deliveredQuantity string
	var deliveryPlanSequence *int
	err = tx.QueryRow(ctx, `SELECT delivery.item_id::text,revision_item.unit,delivery.quantity::text,delivery.delivery_plan_sequence
		FROM clm_contract_deliveries delivery
		JOIN clm_contract_revision_items revision_item USING (tenant_id,contract_id,contract_revision_id,item_id)
		WHERE delivery.tenant_id=$1 AND delivery.contract_id=$2 AND delivery.contract_revision_id=$3 AND delivery.delivery_id=$4
		FOR UPDATE OF delivery`, record.TenantID, contractID, input.ContractRevisionID, input.DeliveryID).Scan(&itemID, &unit, &deliveredQuantity, &deliveryPlanSequence)
	if errors.Is(err, pgx.ErrNoRows) {
		return ContractAcceptance{}, fault.NotFound("没有找到对应交付批次")
	}
	if err != nil {
		return ContractAcceptance{}, err
	}
	names, err := parseRevisionItemNames(ctx, tx, record.TenantID, contractID, input.ContractRevisionID)
	if err != nil {
		return ContractAcceptance{}, err
	}
	if name, ok := names[itemID]; ok {
		itemName = name.Name
	}
	plan, err := acceptancePlanForItem(snapshot, input.AcceptancePlanSequence, itemID)
	if err != nil {
		return ContractAcceptance{}, err
	}
	standard := strings.TrimSpace(input.Standard)
	if plan != nil && strings.TrimSpace(plan.Criteria) != "" {
		if standard != strings.TrimSpace(plan.Criteria) {
			return ContractAcceptance{}, fault.Conflict("验收标准必须与指定冻结计划一致")
		}
		standard = strings.TrimSpace(plan.Criteria)
	}
	start, err := fulfillmentDecimal("scope_start", input.ScopeStart, true)
	if err != nil {
		return ContractAcceptance{}, err
	}
	end, err := fulfillmentDecimal("scope_end", input.ScopeEnd, false)
	if err != nil {
		return ContractAcceptance{}, err
	}
	span := new(big.Rat).Sub(end, start)
	if span.Sign() <= 0 {
		return ContractAcceptance{}, fault.Invalid("验收范围必须满足 start < end")
	}
	accepted, err := fulfillmentDecimal("accepted_quantity", input.AcceptedQuantity, true)
	if err != nil {
		return ContractAcceptance{}, err
	}
	if accepted.Cmp(span) > 0 {
		return ContractAcceptance{}, fault.Invalid("accepted_quantity不能超过本次验收范围")
	}
	failed := new(big.Rat).Sub(span, accepted)
	result := "partial"
	if accepted.Sign() == 0 {
		result = "failed"
	}
	if failed.Sign() == 0 {
		result = "passed"
	}
	deliveryQty, ok := new(big.Rat).SetString(deliveredQuantity)
	if !ok || end.Cmp(deliveryQty) > 0 {
		return ContractAcceptance{}, fault.Conflict("验收范围超出此实际交付数量")
	}
	var parentStart, parentEnd, parentAccepted string
	var parentResult string
	var parentID *string
	if input.ReinspectionOf == "" {
		var overlaps bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_contract_acceptances
			WHERE tenant_id=$1 AND contract_id=$2 AND delivery_id=$3 AND reinspection_of IS NULL
			AND numrange(scope_start,scope_end,'[)') && numrange($4::numeric,$5::numeric,'[)'))`,
			record.TenantID, contractID, input.DeliveryID, input.ScopeStart, input.ScopeEnd).Scan(&overlaps); err != nil {
			return ContractAcceptance{}, err
		}
		if overlaps {
			return ContractAcceptance{}, fault.Conflict("该验收范围已被分配，不能重复统计")
		}
	} else {
		parentID = &input.ReinspectionOf
		err := tx.QueryRow(ctx, `SELECT scope_start::text,scope_end::text,accepted_quantity::text,result
			FROM clm_contract_acceptances WHERE tenant_id=$1 AND contract_id=$2 AND acceptance_id=$3
			AND contract_revision_id=$4 AND delivery_id=$5 AND item_id=$6 FOR UPDATE`,
			record.TenantID, contractID, input.ReinspectionOf, input.ContractRevisionID, input.DeliveryID, itemID).Scan(&parentStart, &parentEnd, &parentAccepted, &parentResult)
		if errors.Is(err, pgx.ErrNoRows) {
			return ContractAcceptance{}, fault.NotFound("没有找到可复验的验收事实")
		}
		if err != nil {
			return ContractAcceptance{}, err
		}
		if parentResult == "passed" {
			return ContractAcceptance{}, fault.Conflict("通过的验收范围不能复验")
		}
		pStart, _ := new(big.Rat).SetString(parentStart)
		pEnd, _ := new(big.Rat).SetString(parentEnd)
		pAccepted, _ := new(big.Rat).SetString(parentAccepted)
		expectedStart := new(big.Rat).Add(pStart, pAccepted)
		if expectedStart.Cmp(start) != 0 || pEnd.Cmp(end) != 0 {
			return ContractAcceptance{}, fault.Conflict("复验必须精确覆盖父验收未通过的后缀范围")
		}
		if failedParent := new(big.Rat).Sub(pEnd, expectedStart); failedParent.Sign() <= 0 {
			return ContractAcceptance{}, fault.Conflict("父验收没有待复验数量")
		}
		if err := validateRectificationsForRetest(ctx, tx, record.TenantID, contractID, input.ReinspectionOf, input.RectificationIDs, current); err != nil {
			return ContractAcceptance{}, err
		}
	}
	var failedTotal = new(big.Rat)
	seenPrior := map[string]struct{}{}
	for index := range input.Defects {
		defect := &input.Defects[index]
		if err := validateFulfillmentText("defects[].description", defect.Description, 4000, true); err != nil {
			return ContractAcceptance{}, err
		}
		if err := validateFulfillmentText("defects[].responsible", defect.Responsible, 300, true); err != nil {
			return ContractAcceptance{}, err
		}
		if _, err := time.Parse("2006-01-02", defect.RemediationDue); err != nil {
			return ContractAcceptance{}, fault.Invalid("defects[].remediation_due 必须为YYYY-MM-DD")
		}
		qty, err := fulfillmentDecimal("defects[].failed_quantity", defect.FailedQuantity, false)
		if err != nil {
			return ContractAcceptance{}, err
		}
		failedTotal.Add(failedTotal, qty)
		if defect.PriorDefectID != "" {
			if input.ReinspectionOf == "" {
				return ContractAcceptance{}, fault.Invalid("首次验收缺陷不能引用prior_defect_id")
			}
			if err := normalizeContractUUIDField("defects[].prior_defect_id", &defect.PriorDefectID); err != nil {
				return ContractAcceptance{}, err
			}
			if _, ok := seenPrior[defect.PriorDefectID]; ok {
				return ContractAcceptance{}, fault.Invalid("一次复验不能重复引用同一既有缺陷")
			}
			seenPrior[defect.PriorDefectID] = struct{}{}
			var found bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_contract_acceptance_defects WHERE tenant_id=$1 AND contract_id=$2 AND acceptance_id=$3 AND defect_id=$4)`, record.TenantID, contractID, input.ReinspectionOf, defect.PriorDefectID).Scan(&found); err != nil {
				return ContractAcceptance{}, err
			}
			if !found {
				return ContractAcceptance{}, fault.Invalid("prior_defect_id 不属于被复验的父验收")
			}
		}
	}
	if failed.Sign() == 0 && len(input.Defects) > 0 {
		return ContractAcceptance{}, fault.Invalid("全量通过的验收不能新报缺陷")
	}
	if failed.Sign() > 0 && (len(input.Defects) == 0 || failedTotal.Cmp(failed) != 0) {
		return ContractAcceptance{}, fault.Invalid("缺陷数量之和必须等于本次未通过数量")
	}
	evidence, err := resolveFulfillmentEvidence(ctx, tx, current, input.Evidence, "acceptance")
	if err != nil {
		return ContractAcceptance{}, err
	}
	evidenceJSON, err := json.Marshal(sortedEvidence(evidence))
	if err != nil {
		return ContractAcceptance{}, err
	}
	acceptanceID, err := newContractUUID()
	if err != nil {
		return ContractAcceptance{}, err
	}
	var recordedAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO clm_contract_acceptances
		(tenant_id,contract_id,contract_revision_id,acceptance_id,delivery_id,item_id,acceptance_plan_sequence,scope_start,scope_end,accepted_quantity,result,reinspection_of,actual_at,standard,inspector_note,evidence_snapshot,recorded_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::numeric,$9::numeric,$10::numeric,$11,$12,$13,$14,$15,$16::jsonb,$17) RETURNING recorded_at`,
		record.TenantID, contractID, input.ContractRevisionID, acceptanceID, input.DeliveryID, itemID, input.AcceptancePlanSequence,
		input.ScopeStart, input.ScopeEnd, input.AcceptedQuantity, result, parentID, actualAt, standard, strings.TrimSpace(input.InspectorNote), string(evidenceJSON), current.ID).Scan(&recordedAt)
	if err != nil {
		if strings.Contains(err.Error(), "clm_contract_acceptances") {
			return ContractAcceptance{}, fault.Conflict("验收关系重复或不匹配")
		}
		return ContractAcceptance{}, err
	}
	for _, rectificationID := range input.RectificationIDs {
		if err := normalizeContractUUIDField("rectification_ids[]", &rectificationID); err != nil {
			return ContractAcceptance{}, err
		}
		var defectAcceptanceID, defectID string
		err := tx.QueryRow(ctx, `SELECT acceptance_id::text,defect_id::text FROM clm_contract_rectifications
			WHERE tenant_id=$1 AND contract_id=$2 AND rectification_id=$3`, record.TenantID, contractID, rectificationID).Scan(&defectAcceptanceID, &defectID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ContractAcceptance{}, fault.Invalid("rectification_id 不存在")
		}
		if err != nil {
			return ContractAcceptance{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO clm_contract_acceptance_rectifications
			(tenant_id,contract_id,acceptance_id,contract_revision_id,delivery_id,item_id,rectification_id,defect_acceptance_id,defect_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, record.TenantID, contractID, acceptanceID, input.ContractRevisionID, input.DeliveryID, itemID, rectificationID, defectAcceptanceID, defectID); err != nil {
			return ContractAcceptance{}, err
		}
	}
	defects := make([]ContractAcceptanceDefect, 0, len(input.Defects))
	for _, defect := range input.Defects {
		defectID, err := newContractUUID()
		if err != nil {
			return ContractAcceptance{}, err
		}
		var priorAcceptance any
		var priorDefect any
		if defect.PriorDefectID != "" {
			priorAcceptance = input.ReinspectionOf
			priorDefect = defect.PriorDefectID
		}
		var defectRecordedAt time.Time
		err = tx.QueryRow(ctx, `INSERT INTO clm_contract_acceptance_defects
			(tenant_id,contract_id,contract_revision_id,acceptance_id,defect_id,prior_acceptance_id,prior_defect_id,description,responsible,remediation_due,failed_quantity,recorded_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::date,$11::numeric,$12) RETURNING recorded_at`,
			record.TenantID, contractID, input.ContractRevisionID, acceptanceID, defectID, priorAcceptance, priorDefect,
			strings.TrimSpace(defect.Description), strings.TrimSpace(defect.Responsible), defect.RemediationDue, defect.FailedQuantity, current.ID).Scan(&defectRecordedAt)
		if err != nil {
			return ContractAcceptance{}, err
		}
		defects = append(defects, ContractAcceptanceDefect{DefectID: defectID, PriorDefectID: defect.PriorDefectID, Description: strings.TrimSpace(defect.Description), Responsible: strings.TrimSpace(defect.Responsible), RemediationDue: defect.RemediationDue, FailedQuantity: defect.FailedQuantity, RecordedBy: current.ID, RecordedAt: defectRecordedAt})
	}
	rowVersion, err := bumpFulfillmentRowVersion(ctx, tx, record, input.ExpectedRowVersion)
	if err != nil {
		return ContractAcceptance{}, err
	}
	spanText := ratString(span)
	failedText := ratString(failed)
	output := ContractAcceptance{AcceptanceID: acceptanceID, ContractID: contractID, ContractRevisionID: input.ContractRevisionID, DeliveryID: input.DeliveryID, ItemID: itemID, ItemName: itemName,
		AcceptancePlanSequence: input.AcceptancePlanSequence, ScopeStart: input.ScopeStart, ScopeEnd: input.ScopeEnd, Quantity: spanText, AcceptedQuantity: input.AcceptedQuantity, FailedQuantity: failedText, Unit: unit, Result: result,
		ReinspectionOf: input.ReinspectionOf, RectificationIDs: append([]string(nil), input.RectificationIDs...), ActualAt: actualAt, Standard: standard, InspectorNote: strings.TrimSpace(input.InspectorNote), Evidence: sortedEvidence(evidence), Defects: defects, Rectifications: []ContractRectification{}, RecordedBy: current.ID, RecordedAt: recordedAt}
	response, err := json.Marshal(output)
	if err != nil {
		return ContractAcceptance{}, err
	}
	if err := insertCLMApprovalEvent(ctx, tx, record.TenantID, contractID, rowVersion, clmEventAcceptanceRecorded, map[string]any{
		"contract_revision_id": input.ContractRevisionID, "acceptance_id": acceptanceID, "delivery_id": input.DeliveryID, "item_id": itemID,
		"scope_start": input.ScopeStart, "scope_end": input.ScopeEnd, "accepted_quantity": input.AcceptedQuantity, "result": result, "recorded_by": current.ID,
	}); err != nil {
		return ContractAcceptance{}, err
	}
	if err := insertCLMWriteReceipt(ctx, tx, record.TenantID, current.ID, clmRouteCreateAcceptance, input.IdempotencyKey, digest, contractID, 201, response); err != nil {
		return ContractAcceptance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractAcceptance{}, err
	}
	return output, nil
}

func validateRectificationsForRetest(ctx context.Context, tx pgx.Tx, tenantID, contractID, parentID string, rectificationIDs []string, current accounts.User) error {
	rows, err := tx.Query(ctx, `SELECT defect_id::text FROM clm_contract_acceptance_defects WHERE tenant_id=$1 AND contract_id=$2 AND acceptance_id=$3 ORDER BY defect_id`, tenantID, contractID, parentID)
	if err != nil {
		return err
	}
	defects := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		defects = append(defects, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(defects) == 0 {
		return fault.Conflict("待复验失败事实没有可追踪缺陷记录")
	}
	provided := map[string]struct{}{}
	for _, value := range rectificationIDs {
		id := value
		if err := normalizeContractUUIDField("rectification_ids[]", &id); err != nil {
			return err
		}
		if _, exists := provided[id]; exists {
			return fault.Invalid("rectification_ids 不能重复")
		}
		provided[id] = struct{}{}
	}
	valid := map[string]string{}
	type rectificationEvidence struct{ materials []ContractVersionMaterial }
	selectedEvidence := []rectificationEvidence{}
	rows, err = tx.Query(ctx, `SELECT rectification_id::text,defect_id::text,evidence_snapshot FROM clm_contract_rectifications
		WHERE tenant_id=$1 AND contract_id=$2 AND acceptance_id=$3`, tenantID, contractID, parentID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var rectificationID, defectID string
		var raw []byte
		if err := rows.Scan(&rectificationID, &defectID, &raw); err != nil {
			rows.Close()
			return err
		}
		valid[rectificationID] = defectID
		if _, selected := provided[rectificationID]; !selected {
			continue
		}
		var materials []ContractVersionMaterial
		if err := json.Unmarshal(raw, &materials); err != nil {
			rows.Close()
			return err
		}
		selectedEvidence = append(selectedEvidence, rectificationEvidence{materials: materials})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, evidence := range selectedEvidence {
		for _, material := range evidence.materials {
			if err := requireSigningCaseRead(ctx, tx, current, material.CaseOrgID, material.CaseID); err != nil {
				return fault.NotFound("没有找到对应记录")
			}
		}
	}
	used := map[string]struct{}{}
	for _, defectID := range defects {
		found := false
		for rectificationID := range provided {
			if valid[rectificationID] == defectID {
				used[rectificationID] = struct{}{}
				found = true
				break
			}
		}
		if !found {
			return fault.Conflict("复验前必须引用每项失败的授权整改事实")
		}
	}
	if len(used) != len(provided) {
		return fault.Invalid("复验只能引用被复验父事实中每项问题的一条整改记录")
	}
	return nil
}

func (s *Store) RecordContractRectification(ctx context.Context, user accounts.User, input ContractRectificationInput) (ContractRectification, error) {
	contractID, err := normalizeContractUUID(input.ContractID)
	if err != nil {
		return ContractRectification{}, err
	}
	input.ContractID = contractID
	if input.ExpectedRowVersion < 1 {
		return ContractRectification{}, fault.Invalid("expected_row_version 必须为正数")
	}
	for field, value := range map[string]*string{"contract_revision_id": &input.ContractRevisionID, "acceptance_id": &input.AcceptanceID, "defect_id": &input.DefectID} {
		if err := normalizeContractUUIDField(field, value); err != nil {
			return ContractRectification{}, err
		}
	}
	if err := validateFulfillmentText("description", input.Description, 4000, true); err != nil {
		return ContractRectification{}, err
	}
	if err := validateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return ContractRectification{}, err
	}
	digest, err := fulfillmentRequestDigest(clmRouteCreateRectification, "POST", "/api/contracts/"+contractID+"/acceptances/"+input.AcceptanceID+"/rectifications", input.ExpectedRowVersion, input)
	if err != nil {
		return ContractRectification{}, err
	}
	if !s.HasDatabase() {
		return ContractRectification{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能登记合同整改事实", nil)
	if err != nil {
		return ContractRectification{}, err
	}
	defer rollbackCLM(tx)
	if err := lockWriteReceipt(ctx, tx, current, clmRouteCreateRectification, input.IdempotencyKey); err != nil {
		return ContractRectification{}, err
	}
	receipt, found, err := replayCLMWriteReceipt(ctx, tx, current, clmRouteCreateRectification, input.IdempotencyKey, digest, contractID)
	if err != nil {
		return ContractRectification{}, err
	}
	if found {
		var saved ContractRectification
		if err := json.Unmarshal(receipt.Response, &saved); err != nil {
			return ContractRectification{}, err
		}
		for _, material := range saved.Evidence {
			if err := requireSigningCaseRead(ctx, tx, current, material.CaseOrgID, material.CaseID); err != nil {
				return ContractRectification{}, fault.NotFound("没有找到对应记录")
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return ContractRectification{}, err
		}
		return saved, nil
	}
	if err := scopedContractLock(ctx, tx, contractID); err != nil {
		return ContractRectification{}, err
	}
	record, err := fulfillmentWriteRecord(ctx, tx, current, contractID)
	if err != nil {
		return ContractRectification{}, err
	}
	if record.RowVersion != input.ExpectedRowVersion {
		return ContractRectification{}, fault.Conflict("合同已被修改，请刷新后重试")
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != input.ContractRevisionID {
		return ContractRectification{}, fault.Conflict("整改事实必须对应合同当前冻结修订")
	}
	var result string
	err = tx.QueryRow(ctx, `SELECT result FROM clm_contract_acceptances WHERE tenant_id=$1 AND contract_id=$2 AND acceptance_id=$3 AND contract_revision_id=$4 FOR UPDATE`, record.TenantID, contractID, input.AcceptanceID, input.ContractRevisionID).Scan(&result)
	if errors.Is(err, pgx.ErrNoRows) {
		return ContractRectification{}, fault.NotFound("没有找到对应的验收事实")
	}
	if err != nil {
		return ContractRectification{}, err
	}
	if result == "passed" {
		return ContractRectification{}, fault.Conflict("通过的验收事实不能新增整改记录")
	}
	var child bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_contract_acceptances WHERE tenant_id=$1 AND contract_id=$2 AND reinspection_of=$3)`, record.TenantID, contractID, input.AcceptanceID).Scan(&child); err != nil {
		return ContractRectification{}, err
	}
	if child {
		return ContractRectification{}, fault.Conflict("复验已经开始，不能再补写该失败事实的整改记录")
	}
	var defectExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_contract_acceptance_defects WHERE tenant_id=$1 AND contract_id=$2 AND acceptance_id=$3 AND defect_id=$4)`, record.TenantID, contractID, input.AcceptanceID, input.DefectID).Scan(&defectExists); err != nil {
		return ContractRectification{}, err
	}
	if !defectExists {
		return ContractRectification{}, fault.NotFound("没有找到对应的验收缺陷")
	}
	evidence, err := resolveFulfillmentEvidence(ctx, tx, current, input.Evidence, "acceptance")
	if err != nil {
		return ContractRectification{}, err
	}
	evidenceJSON, err := json.Marshal(sortedEvidence(evidence))
	if err != nil {
		return ContractRectification{}, err
	}
	rectificationID, err := newContractUUID()
	if err != nil {
		return ContractRectification{}, err
	}
	var recordedAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO clm_contract_rectifications (tenant_id,contract_id,contract_revision_id,rectification_id,acceptance_id,defect_id,description,evidence_snapshot,recorded_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9) RETURNING recorded_at`, record.TenantID, contractID, input.ContractRevisionID, rectificationID, input.AcceptanceID, input.DefectID, strings.TrimSpace(input.Description), string(evidenceJSON), current.ID).Scan(&recordedAt)
	if err != nil {
		return ContractRectification{}, err
	}
	rowVersion, err := bumpFulfillmentRowVersion(ctx, tx, record, input.ExpectedRowVersion)
	if err != nil {
		return ContractRectification{}, err
	}
	output := ContractRectification{RectificationID: rectificationID, ContractID: contractID, ContractRevisionID: input.ContractRevisionID, AcceptanceID: input.AcceptanceID, DefectID: input.DefectID, Description: strings.TrimSpace(input.Description), Evidence: sortedEvidence(evidence), RecordedBy: current.ID, RecordedAt: recordedAt}
	response, err := json.Marshal(output)
	if err != nil {
		return ContractRectification{}, err
	}
	if err := insertCLMApprovalEvent(ctx, tx, record.TenantID, contractID, rowVersion, clmEventRectificationRecorded, map[string]any{"contract_revision_id": input.ContractRevisionID, "rectification_id": rectificationID, "acceptance_id": input.AcceptanceID, "defect_id": input.DefectID, "recorded_by": current.ID}); err != nil {
		return ContractRectification{}, err
	}
	if err := insertCLMWriteReceipt(ctx, tx, record.TenantID, current.ID, clmRouteCreateRectification, input.IdempotencyKey, digest, contractID, 201, response); err != nil {
		return ContractRectification{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractRectification{}, err
	}
	return output, nil
}

func (s *Store) ListContractAcceptances(ctx context.Context, user accounts.User, contractID string) (ContractAcceptanceList, error) {
	id, err := normalizeContractUUID(contractID)
	if err != nil {
		return ContractAcceptanceList{}, err
	}
	if s.pool == nil {
		return ContractAcceptanceList{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return ContractAcceptanceList{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ContractAcceptanceList{}, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ContractAcceptanceList{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ContractAcceptanceList{}, err
	}
	if !authorizeContract(record, current, access.Read) {
		return ContractAcceptanceList{}, fault.NotFound("没有找到对应记录")
	}
	rows, err := tx.Query(ctx, `SELECT acceptance.acceptance_id::text,acceptance.contract_revision_id::text,acceptance.delivery_id::text,acceptance.item_id::text,
		acceptance.acceptance_plan_sequence,acceptance.scope_start::text,acceptance.scope_end::text,acceptance.accepted_quantity::text,
		acceptance.result,COALESCE(acceptance.reinspection_of::text,''),acceptance.actual_at,acceptance.standard,acceptance.inspector_note,
		acceptance.evidence_snapshot,acceptance.recorded_by,acceptance.recorded_at,delivery.unit
		FROM clm_contract_acceptances acceptance JOIN clm_contract_deliveries delivery USING (tenant_id,contract_id,contract_revision_id,delivery_id,item_id)
		WHERE acceptance.tenant_id=$1 AND acceptance.contract_id=$2 ORDER BY acceptance.actual_at,acceptance.acceptance_id`, record.TenantID, id)
	if err != nil {
		return ContractAcceptanceList{}, err
	}
	items := []ContractAcceptance{}
	type acceptanceRow struct {
		item     ContractAcceptance
		evidence []byte
	}
	loaded := []acceptanceRow{}
	for rows.Next() {
		var item ContractAcceptance
		var evidenceRaw []byte
		if err := rows.Scan(&item.AcceptanceID, &item.ContractRevisionID, &item.DeliveryID, &item.ItemID, &item.AcceptancePlanSequence, &item.ScopeStart, &item.ScopeEnd, &item.AcceptedQuantity, &item.Result, &item.ReinspectionOf, &item.ActualAt, &item.Standard, &item.InspectorNote, &evidenceRaw, &item.RecordedBy, &item.RecordedAt, &item.Unit); err != nil {
			rows.Close()
			return ContractAcceptanceList{}, err
		}
		loaded = append(loaded, acceptanceRow{item: item, evidence: evidenceRaw})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ContractAcceptanceList{}, err
	}
	namesCache := map[string]map[string]revisionItemInfo{}
	for _, row := range loaded {
		item, evidenceRaw := row.item, row.evidence
		quantity := new(big.Rat)
		start, _ := new(big.Rat).SetString(item.ScopeStart)
		end, _ := new(big.Rat).SetString(item.ScopeEnd)
		quantity.Sub(end, start)
		item.Quantity = ratString(quantity)
		accepted, _ := new(big.Rat).SetString(item.AcceptedQuantity)
		item.FailedQuantity = ratString(new(big.Rat).Sub(quantity, accepted))
		if _, ok := namesCache[item.ContractRevisionID]; !ok {
			names, err := parseRevisionItemNames(ctx, tx, record.TenantID, id, item.ContractRevisionID)
			if err != nil {
				return ContractAcceptanceList{}, err
			}
			namesCache[item.ContractRevisionID] = names
		}
		if frozen, ok := namesCache[item.ContractRevisionID][item.ItemID]; ok {
			item.ItemName = frozen.Name
		}
		visible, redacted, err := fulfillmentMaterialSnapshot(ctx, tx, current, evidenceRaw)
		if err != nil {
			return ContractAcceptanceList{}, err
		}
		item.Evidence, item.EvidenceRedacted = visible, redacted
		item.Defects, err = loadAcceptanceDefects(ctx, tx, record.TenantID, id, item.AcceptanceID)
		if err != nil {
			return ContractAcceptanceList{}, err
		}
		item.Rectifications, err = loadAcceptanceRectifications(ctx, tx, current, record.TenantID, id, item.AcceptanceID)
		if err != nil {
			return ContractAcceptanceList{}, err
		}
		item.RectificationIDs, err = loadAcceptanceRectificationIDs(ctx, tx, record.TenantID, id, item.AcceptanceID)
		if err != nil {
			return ContractAcceptanceList{}, err
		}
		items = append(items, item)
	}
	summary, err := loadFulfillmentSummary(ctx, tx, record.TenantID, id)
	if err != nil {
		return ContractAcceptanceList{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractAcceptanceList{}, err
	}
	return ContractAcceptanceList{Items: items, Summary: summary, RowVersion: record.RowVersion}, nil
}

func loadAcceptanceDefects(ctx context.Context, tx pgx.Tx, tenantID, contractID, acceptanceID string) ([]ContractAcceptanceDefect, error) {
	rows, err := tx.Query(ctx, `SELECT defect_id::text,COALESCE(prior_defect_id::text,''),description,responsible,remediation_due::text,failed_quantity::text,recorded_by,recorded_at
		FROM clm_contract_acceptance_defects WHERE tenant_id=$1 AND contract_id=$2 AND acceptance_id=$3 ORDER BY defect_id`, tenantID, contractID, acceptanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ContractAcceptanceDefect{}
	for rows.Next() {
		var item ContractAcceptanceDefect
		if err := rows.Scan(&item.DefectID, &item.PriorDefectID, &item.Description, &item.Responsible, &item.RemediationDue, &item.FailedQuantity, &item.RecordedBy, &item.RecordedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadAcceptanceRectificationIDs(ctx context.Context, tx pgx.Tx, tenantID, contractID, acceptanceID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT rectification_id::text FROM clm_contract_acceptance_rectifications
		WHERE tenant_id=$1 AND contract_id=$2 AND acceptance_id=$3 ORDER BY rectification_id`, tenantID, contractID, acceptanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func loadAcceptanceRectifications(ctx context.Context, tx pgx.Tx, current accounts.User, tenantID, contractID, acceptanceID string) ([]ContractRectification, error) {
	rows, err := tx.Query(ctx, `SELECT rectification.rectification_id::text,rectification.contract_revision_id::text,rectification.acceptance_id::text,
		rectification.defect_id::text,rectification.description,rectification.evidence_snapshot,rectification.recorded_by,rectification.recorded_at
		FROM clm_contract_rectifications rectification
		WHERE rectification.tenant_id=$1 AND rectification.contract_id=$2 AND rectification.acceptance_id=$3
		ORDER BY rectification.recorded_at,rectification.rectification_id`, tenantID, contractID, acceptanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type rectificationRow struct {
		item ContractRectification
		raw  []byte
	}
	loaded := []rectificationRow{}
	for rows.Next() {
		var item ContractRectification
		var raw []byte
		if err := rows.Scan(&item.RectificationID, &item.ContractRevisionID, &item.AcceptanceID, &item.DefectID, &item.Description, &raw, &item.RecordedBy, &item.RecordedAt); err != nil {
			return nil, err
		}
		loaded = append(loaded, rectificationRow{item: item, raw: raw})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items := []ContractRectification{}
	for _, row := range loaded {
		item := row.item
		visible, redacted, err := fulfillmentMaterialSnapshot(ctx, tx, current, row.raw)
		if err != nil {
			return nil, err
		}
		item.Evidence, item.EvidenceRedacted = visible, redacted
		items = append(items, item)
	}
	return items, rows.Err()
}
