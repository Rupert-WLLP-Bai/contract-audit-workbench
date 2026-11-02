package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

const (
	clmRoutePaymentEligibility = "clm.payment_eligibility"
	clmEventPaymentReceipt     = "contract.payment_receipt_recorded.v1"
)

type ContractPaymentEligibilityInput struct {
	ContractID         string `json:"-"`
	RevisionID         string `json:"contract_revision_id"`
	Sequence           int    `json:"payment_sequence"`
	ExpectedRowVersion int64  `json:"expected_row_version"`
	IdempotencyKey     string `json:"-"`
}

type ContractPaymentEligibility struct {
	CheckID            string    `json:"check_id"`
	ContractID         string    `json:"contract_id"`
	ContractRevisionID string    `json:"contract_revision_id"`
	PaymentSequence    int       `json:"payment_sequence"`
	Status             string    `json:"status"`
	Reasons            []string  `json:"reasons"`
	Evidence           []string  `json:"evidence"`
	CheckedAt          time.Time `json:"checked_at"`
	ContractRowVersion int64     `json:"contract_row_version"`
	InitiatesPayment   bool      `json:"initiates_payment"`
}

type ContractPaymentReceiptInput struct {
	ContractID      string    `json:"contract_id"`
	RevisionID      string    `json:"contract_revision_id"`
	Sequence        int       `json:"payment_sequence"`
	EventID         string    `json:"event_id"`
	EventType       string    `json:"event_type"`
	OriginalEventID string    `json:"original_event_id,omitempty"`
	Amount          string    `json:"amount"`
	Currency        string    `json:"currency"`
	OccurredAt      time.Time `json:"occurred_at"`
}

type TrustedPaymentSource struct{ TenantID, SourceID, KeyID string }

type ContractPaymentReceipt struct {
	ReceiptID             string    `json:"receipt_id"`
	ContractID            string    `json:"contract_id"`
	ContractRevisionID    string    `json:"contract_revision_id"`
	PaymentSequence       int       `json:"payment_sequence"`
	SourceID              string    `json:"source_id"`
	SourceKeyID           string    `json:"source_key_id"`
	SourceEventID         string    `json:"source_event_id"`
	EventType             string    `json:"event_type"`
	OriginalSourceEventID string    `json:"original_source_event_id,omitempty"`
	Amount                string    `json:"amount"`
	Currency              string    `json:"currency"`
	OccurredAt            time.Time `json:"occurred_at"`
	RecordedAt            time.Time `json:"recorded_at"`
	PayloadDigest         string    `json:"payload_digest"`
}

type ContractPaymentSummary struct {
	ContractID         string                   `json:"contract_id"`
	ContractRevisionID string                   `json:"contract_revision_id"`
	PaymentSequence    int                      `json:"payment_sequence"`
	PlannedAmount      string                   `json:"planned_amount,omitempty"`
	Currency           string                   `json:"currency,omitempty"`
	NetPaid            string                   `json:"net_paid"`
	State              string                   `json:"state"`
	Receipts           []ContractPaymentReceipt `json:"receipts"`
}

func (s *Store) CheckContractPaymentEligibility(ctx context.Context, user accounts.User, in ContractPaymentEligibilityInput) (ContractPaymentEligibility, error) {
	ctx = commandContext(ctx, user)
	var result ContractPaymentEligibility
	if !s.HasDatabase() {
		return result, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	if err := normalizeContractUUIDField("contract_revision_id", &in.RevisionID); err != nil {
		return result, err
	}
	if in.Sequence < 1 || in.ExpectedRowVersion < 1 || strings.TrimSpace(in.IdempotencyKey) == "" {
		return result, fault.Invalid("payment sequence、expected_row_version 和幂等键必填")
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "付款核验功能当前只读", nil)
	if err != nil {
		return result, err
	}
	defer rollbackCLM(tx)
	if err = scopedContractLock(ctx, tx, in.ContractID); err != nil {
		return result, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), in.ContractID)
	if err != nil {
		return result, err
	}
	if err := requireCLMFulfillmentManage(record, current); err != nil {
		return result, err
	}
	digest, err := clmRequestDigest(clmRoutePaymentEligibility, "POST", "/api/contracts/"+record.ContractID+"/payment-eligibility-checks", in.ExpectedRowVersion, struct {
		Revision string `json:"revision"`
		Sequence int    `json:"sequence"`
	}{in.RevisionID, in.Sequence})
	if err != nil {
		return result, err
	}
	if err = lockWriteReceipt(ctx, tx, current, clmRoutePaymentEligibility, in.IdempotencyKey); err != nil {
		return result, err
	}
	if receipt, found, e := replayCLMWriteReceipt(ctx, tx, current, clmRoutePaymentEligibility, in.IdempotencyKey, digest, record.ContractID); e != nil {
		return result, e
	} else if found {
		if e = json.Unmarshal(receipt.Response, &result); e != nil {
			return result, e
		}
		if e = tx.Commit(ctx); e != nil {
			return ContractPaymentEligibility{}, e
		}
		return result, nil
	}
	if record.RowVersion != in.ExpectedRowVersion {
		return result, fault.Conflict("合同已被修改，请刷新后重新确认")
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != in.RevisionID {
		return result, fault.Conflict("付款条件必须对应当前不可变修订")
	}
	draft, err := loadFrozenFulfillmentDraft(ctx, tx, record.TenantID, record.ContractID, in.RevisionID)
	if err != nil {
		return result, err
	}
	var plan *DraftPaymentPlan
	for i := range draft.Payments {
		if draft.Payments[i].Sequence == in.Sequence {
			plan = &draft.Payments[i]
			break
		}
	}
	if plan == nil {
		return result, fault.Invalid("payment_sequence 不属于冻结付款计划")
	}
	status, reasons, evidence := evaluatePaymentCondition(ctx, tx, record, draft, *plan)
	if err = tx.QueryRow(ctx, `UPDATE clm_contracts SET row_version=row_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND contract_id=$2 AND row_version=$3 RETURNING row_version`, record.TenantID, record.ContractID, record.RowVersion).Scan(&record.RowVersion); err != nil {
		return result, err
	}
	checkID, err := newContractUUID()
	if err != nil {
		return result, err
	}
	var checkedAt time.Time
	if err = tx.QueryRow(ctx, `INSERT INTO clm_contract_payment_eligibility_checks
		(tenant_id,contract_id,contract_revision_id,check_id,payment_sequence,status,reasons,evidence,checked_by,contract_row_version)
		VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9,$10) RETURNING checked_at`, record.TenantID, record.ContractID, in.RevisionID, checkID, in.Sequence, status, jsonArray(reasons), jsonArray(evidence), current.ID, record.RowVersion).Scan(&checkedAt); err != nil {
		return result, err
	}
	result = ContractPaymentEligibility{CheckID: checkID, ContractID: record.ContractID, ContractRevisionID: in.RevisionID, PaymentSequence: in.Sequence, Status: status, Reasons: reasons, Evidence: evidence, CheckedAt: checkedAt, ContractRowVersion: record.RowVersion, InitiatesPayment: false}
	if err = insertCLMApprovalEvent(ctx, tx, record.TenantID, record.ContractID, record.RowVersion, "contract.payment_eligibility_checked.v1", map[string]any{"check_id": checkID, "contract_revision_id": in.RevisionID, "payment_sequence": in.Sequence, "status": status}); err != nil {
		return result, err
	}
	response, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if err = insertCLMWriteReceipt(ctx, tx, record.TenantID, current.ID, clmRoutePaymentEligibility, in.IdempotencyKey, digest, record.ContractID, 201, response); err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func evaluatePaymentCondition(ctx context.Context, tx pgx.Tx, record clmContractRecord, draft DraftContent, plan DraftPaymentPlan) (string, []string, []string) {
	unknown := func(reason string) (string, []string, []string) { return "unknown", []string{reason}, []string{} }
	if plan.Condition == nil {
		return unknown("frozen_payment_condition_missing")
	}
	if _, ok := canonicalPaymentAmount(plan.Amount); !ok || draft.Contract == nil || !currencyValid(draft.Contract.Currency) {
		return unknown("frozen_payment_amount_or_currency_unknown")
	}
	condition := plan.Condition
	if strings.TrimSpace(condition.PolicyRef) != "" {
		return unknown("payment_condition_policy_ref_requires_policy_evaluation")
	}
	if len(condition.EvidenceRequirements) > 0 {
		return unknown("required_evidence_needs_authorized_review")
	}
	if condition.DueRule == nil {
		return unknown("due_rule_missing")
	}
	if condition.DueRule.Trigger != "fixed_date" && condition.DueRule.Trigger != "contract_signed" && condition.DueRule.Trigger != "acceptance_passed" {
		return unknown("due_trigger_not_supported_without_frozen_mapping")
	}
	if condition.DueRule.DaysAfter != nil || condition.DueRule.Calendar != "" || condition.DueRule.PolicyRef != "" {
		return unknown("due_rule_requires_unimplemented_policy_evaluation")
	}
	if condition.DueRule.Trigger == "fixed_date" {
		date, err := time.Parse("2006-01-02", plan.PlannedDate)
		if err != nil {
			return unknown("fixed_payment_date_unverifiable")
		}
		if date.After(time.Now().UTC()) {
			return "blocked", []string{"payment_due_date_not_reached"}, []string{}
		}
	}
	evidence := []string{}
	switch condition.Type {
	case "signed_contract":
		var registration string
		err := tx.QueryRow(ctx, `SELECT registration_id::text FROM clm_contract_signing_registrations WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3`, record.TenantID, record.ContractID, record.CurrentRevisionID).Scan(&registration)
		if errors.Is(err, pgx.ErrNoRows) {
			return "blocked", []string{"signing_registration_missing"}, evidence
		}
		if err != nil {
			return "unknown", []string{"signing_fact_unavailable"}, evidence
		}
		evidence = append(evidence, "signing_registration:"+registration)
	case "all_goods_accepted":
		if len(draft.Items) == 0 {
			return unknown("frozen_item_scope_missing")
		}
		for _, item := range draft.Items {
			var planned, unit *string
			var positive bool
			err := tx.QueryRow(ctx, `SELECT planned_quantity::text,unit,COALESCE(planned_quantity>0,false) FROM clm_contract_revision_items WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND item_id=$4`, record.TenantID, record.ContractID, record.CurrentRevisionID, item.ItemID).Scan(&planned, &unit, &positive)
			if err != nil || planned == nil || unit == nil || strings.TrimSpace(*unit) == "" || !positive {
				return unknown("frozen_item_quantity_or_unit_unknown")
			}
			var qualified string
			err = tx.QueryRow(ctx, `SELECT COALESCE(sum(accepted_quantity),0)::text FROM clm_contract_acceptances WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND item_id=$4`, record.TenantID, record.ContractID, record.CurrentRevisionID, item.ItemID).Scan(&qualified)
			if err != nil {
				return unknown("acceptance_facts_unavailable")
			}
			if qualified != *planned {
				return "blocked", []string{"all_goods_not_fully_accepted"}, append(evidence, "qualified_quantity:"+item.ItemID+":"+qualified)
			}
			evidence = append(evidence, "qualified_quantity:"+item.ItemID+":"+qualified)
		}
	case "partial_acceptance":
		return unknown("partial_acceptance_scope_not_explicitly_linked_in_frozen_condition")
	case "custom_policy", "service_period_verified":
		return unknown("condition_requires_unsupported_policy_evidence")
	default:
		return unknown("condition_type_unknown")
	}
	if condition.DueRule.Trigger == "acceptance_passed" && condition.Type != "all_goods_accepted" {
		return unknown("acceptance_mapping_not_explicit")
	}
	if condition.DueRule.Trigger == "contract_signed" && condition.Type != "signed_contract" {
		var exists bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_contract_signing_registrations WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3)`, record.TenantID, record.ContractID, record.CurrentRevisionID).Scan(&exists)
		if err != nil {
			return unknown("signing_fact_unavailable")
		}
		if !exists {
			return "blocked", []string{"signing_registration_missing"}, evidence
		}
		evidence = append(evidence, "signing_registration_present")
	}
	return "eligible", []string{"frozen_condition_satisfied"}, evidence
}

func jsonArray(values []string) string { b, _ := json.Marshal(values); return string(b) }

func (s *Store) RecordTrustedContractPaymentReceipt(ctx context.Context, source TrustedPaymentSource, in ContractPaymentReceiptInput) (ContractPaymentReceipt, bool, error) {
	var result ContractPaymentReceipt
	if !s.HasDatabase() {
		return result, false, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	if strings.TrimSpace(source.TenantID) == "" || strings.TrimSpace(source.SourceID) == "" || len(source.SourceID) > 200 || !boundedVisibleText(source.KeyID, 200) {
		return result, false, fault.Forbidden("付款来源未授权")
	}
	if err := normalizeContractUUIDField("contract_id", &in.ContractID); err != nil {
		return result, false, err
	}
	if err := normalizeContractUUIDField("contract_revision_id", &in.RevisionID); err != nil {
		return result, false, err
	}
	in.EventID = strings.TrimSpace(in.EventID)
	in.EventType = strings.TrimSpace(in.EventType)
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Sequence < 1 || !boundedVisibleText(in.EventID, 300) || !in.OccurredAt.After(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)) || in.OccurredAt.After(time.Now().Add(5*time.Minute)) {
		return result, false, fault.Invalid("付款来源事实字段无效")
	}
	if in.EventType != "payment" && in.EventType != "reversal" {
		return result, false, fault.Invalid("event_type 必须是 payment 或 reversal")
	}
	in.OriginalEventID = strings.TrimSpace(in.OriginalEventID)
	if in.EventType == "payment" && in.OriginalEventID != "" || in.EventType == "reversal" && !boundedVisibleText(in.OriginalEventID, 300) {
		return result, false, fault.Invalid("冲正必须且仅可引用原来源交易号")
	}
	amount, amountOK := canonicalPaymentAmount(in.Amount)
	amountCents, centsOK := paymentCents(amount)
	if !amountOK || !currencyValid(in.Currency) || !centsOK || amountCents <= 0 {
		return result, false, fault.Invalid("付款金额或币种格式无效")
	}
	in.Amount = amount
	if err := s.checkCLMWriteAllowed("付款来源事实当前只读"); err != nil {
		return result, false, err
	}
	canonical := struct {
		Tenant     string    `json:"tenant_id"`
		Source     string    `json:"source_id"`
		Contract   string    `json:"contract_id"`
		Revision   string    `json:"contract_revision_id"`
		Sequence   int       `json:"payment_sequence"`
		EventID    string    `json:"event_id"`
		Type       string    `json:"event_type"`
		Original   string    `json:"original_event_id,omitempty"`
		Amount     string    `json:"amount"`
		Currency   string    `json:"currency"`
		OccurredAt time.Time `json:"occurred_at"`
	}{source.TenantID, source.SourceID, in.ContractID, in.RevisionID, in.Sequence, in.EventID, in.EventType, strings.TrimSpace(in.OriginalEventID), in.Amount, in.Currency, in.OccurredAt.UTC()}
	bytes, err := json.Marshal(canonical)
	if err != nil {
		return result, false, err
	}
	sum := sha256.Sum256(bytes)
	digest := hex.EncodeToString(sum[:])
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return result, false, err
	}
	defer rollbackCLM(tx)
	if err = setLocalScope(ctx, tx, dbScope{ActorID: "payment-source:" + source.SourceID, TenantID: source.TenantID, OrgID: "", Role: "service"}); err != nil {
		return result, false, err
	}
	if err = scopedPaymentEventLock(ctx, tx, source.TenantID, source.SourceID, in.EventID); err != nil {
		return result, false, err
	}
	if err = scopedContractLock(ctx, tx, in.ContractID); err != nil {
		return result, false, err
	}
	var oldDigest string
	err = tx.QueryRow(ctx, `SELECT payload_digest FROM clm_contract_payment_receipts WHERE tenant_id=$1 AND source_id=$2 AND source_event_id=$3`, source.TenantID, source.SourceID, in.EventID).Scan(&oldDigest)
	if err == nil {
		if oldDigest != digest {
			return result, false, fault.Conflict("来源事件ID已用于不同载荷")
		}
		result, err = loadPaymentReceipt(ctx, tx, source.TenantID, source.SourceID, in.EventID)
		if err != nil {
			return result, false, err
		}
		if err = tx.Commit(ctx); err != nil {
			return ContractPaymentReceipt{}, false, err
		}
		return result, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return result, false, err
	}
	record, err := loadCLMContract(ctx, tx, source.TenantID, in.ContractID)
	if err != nil {
		return result, false, err
	}
	if record.AuthorityMode != "native" {
		return result, false, fault.Conflict("付款来源事实必须绑定本系统权威合同")
	}
	draft, err := loadFrozenFulfillmentDraft(ctx, tx, source.TenantID, in.ContractID, in.RevisionID)
	if err != nil {
		return result, false, err
	}
	var signedRevision bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clm_contract_signing_registrations WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3)`, source.TenantID, in.ContractID, in.RevisionID).Scan(&signedRevision); err != nil {
		return result, false, err
	}
	if !signedRevision {
		return result, false, fault.Conflict("付款来源事实必须绑定已登记签署的不可变修订")
	}
	var plan *DraftPaymentPlan
	for i := range draft.Payments {
		if draft.Payments[i].Sequence == in.Sequence {
			plan = &draft.Payments[i]
			break
		}
	}
	if plan == nil {
		return result, false, fault.Invalid("来源事件付款期次不属于冻结计划")
	}
	if draft.Contract == nil || draft.Contract.Currency != in.Currency {
		return result, false, fault.Conflict("来源事件币种与冻结合同币种不一致")
	}
	if in.EventType == "reversal" {
		var orig ContractPaymentReceipt
		orig, err = loadPaymentReceipt(ctx, tx, source.TenantID, source.SourceID, in.OriginalEventID)
		if err != nil || orig.EventType != "payment" || orig.ContractID != in.ContractID || orig.ContractRevisionID != in.RevisionID || orig.PaymentSequence != in.Sequence || orig.Currency != in.Currency {
			return result, false, fault.Invalid("冲正必须引用同来源、同合同修订、同付款期次和币种的原付款")
		}
		var reversed string
		if err = tx.QueryRow(ctx, `SELECT COALESCE(sum(amount),0)::text FROM clm_contract_payment_receipts WHERE tenant_id=$1 AND source_id=$2 AND original_source_event_id=$3 AND event_type='reversal'`, source.TenantID, source.SourceID, in.OriginalEventID).Scan(&reversed); err != nil {
			return result, false, err
		}
		if exceedsOriginal(orig.Amount, reversed, in.Amount) {
			return result, false, fault.Invalid("累计冲正金额超过原付款金额")
		}
	}
	err = tx.QueryRow(ctx, `UPDATE clm_contracts SET row_version=row_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND contract_id=$2 AND row_version=$3 RETURNING row_version`, source.TenantID, in.ContractID, record.RowVersion).Scan(&record.RowVersion)
	if err != nil {
		return result, false, err
	}
	receiptID, err := newContractUUID()
	if err != nil {
		return result, false, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO clm_contract_payment_receipts(tenant_id,contract_id,contract_revision_id,receipt_id,payment_sequence,source_id,source_key_id,source_event_id,event_type,original_source_event_id,amount,currency,occurred_at,payload_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11::numeric,$12,$13,$14) RETURNING recorded_at`, source.TenantID, in.ContractID, in.RevisionID, receiptID, in.Sequence, source.SourceID, source.KeyID, in.EventID, in.EventType, strings.TrimSpace(in.OriginalEventID), in.Amount, in.Currency, in.OccurredAt.UTC(), digest).Scan(&result.RecordedAt)
	if err != nil {
		return result, false, err
	}
	result = ContractPaymentReceipt{ReceiptID: receiptID, ContractID: in.ContractID, ContractRevisionID: in.RevisionID, PaymentSequence: in.Sequence, SourceID: source.SourceID, SourceKeyID: source.KeyID, SourceEventID: in.EventID, EventType: in.EventType, OriginalSourceEventID: strings.TrimSpace(in.OriginalEventID), Amount: in.Amount, Currency: in.Currency, OccurredAt: in.OccurredAt.UTC(), RecordedAt: result.RecordedAt, PayloadDigest: digest}
	payload, _ := json.Marshal(map[string]any{"receipt_id": receiptID, "contract_revision_id": in.RevisionID, "payment_sequence": in.Sequence, "source_id": source.SourceID, "source_key_id": source.KeyID, "source_event_id": in.EventID, "event_type": in.EventType, "original_source_event_id": strings.TrimSpace(in.OriginalEventID), "amount": in.Amount, "currency": in.Currency, "occurred_at": in.OccurredAt.UTC(), "payload_digest": digest})
	eventID, err := newContractUUID()
	if err != nil {
		return ContractPaymentReceipt{}, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO clm_business_events(tenant_id,event_id,contract_id,aggregate_version,event_type,schema_version,payload) VALUES($1,$2,$3,$4,$5,1,$6::jsonb)`, source.TenantID, eventID, in.ContractID, record.RowVersion, clmEventPaymentReceipt, string(payload)); err != nil {
		return ContractPaymentReceipt{}, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO clm_outbox_delivery(tenant_id,event_id) VALUES($1,$2)`, source.TenantID, eventID); err != nil {
		return ContractPaymentReceipt{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ContractPaymentReceipt{}, false, err
	}
	return result, false, nil
}

func loadPaymentReceipt(ctx context.Context, tx pgx.Tx, tenant, source, event string) (ContractPaymentReceipt, error) {
	var r ContractPaymentReceipt
	err := tx.QueryRow(ctx, `SELECT receipt_id::text,contract_id::text,contract_revision_id::text,payment_sequence,source_id,source_key_id,source_event_id,event_type,COALESCE(original_source_event_id,''),amount::text,currency,occurred_at,recorded_at,payload_digest FROM clm_contract_payment_receipts WHERE tenant_id=$1 AND source_id=$2 AND source_event_id=$3`, tenant, source, event).Scan(&r.ReceiptID, &r.ContractID, &r.ContractRevisionID, &r.PaymentSequence, &r.SourceID, &r.SourceKeyID, &r.SourceEventID, &r.EventType, &r.OriginalSourceEventID, &r.Amount, &r.Currency, &r.OccurredAt, &r.RecordedAt, &r.PayloadDigest)
	return r, err
}

func scopedPaymentEventLock(ctx context.Context, tx pgx.Tx, tenant, source, event string) error {
	key, err := canonicalHash([]string{tenant, source, event})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':clm-payment-event:' || $1,0))`, key)
	return err
}

func (s *Store) ListContractPayments(ctx context.Context, user accounts.User, contractID string) ([]ContractPaymentSummary, error) {
	ctx = commandContext(ctx, user)
	var out []ContractPaymentSummary
	if !s.HasDatabase() {
		return nil, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return nil, err
	}
	id, err := normalizeContractUUID(contractID)
	if err != nil {
		return nil, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollbackCLM(tx)
	if err = setLocalScope(ctx, tx, userScope(current)); err != nil {
		return nil, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return nil, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return nil, fault.NotFound("没有找到对应记录")
	}
	revisions, err := tx.Query(ctx, `SELECT contract_revision_id::text,snapshot FROM clm_contract_revisions WHERE tenant_id=$1 AND contract_id=$2 ORDER BY revision_number DESC`, record.TenantID, record.ContractID)
	if err != nil {
		return nil, err
	}
	type revisionSnapshot struct {
		id       string
		snapshot []byte
	}
	var snapshots []revisionSnapshot
	for revisions.Next() {
		var item revisionSnapshot
		if err = revisions.Scan(&item.id, &item.snapshot); err != nil {
			revisions.Close()
			return nil, err
		}
		snapshots = append(snapshots, item)
	}
	if err = revisions.Err(); err != nil {
		revisions.Close()
		return nil, err
	}
	revisions.Close()
	for _, frozen := range snapshots {
		var draft DraftContent
		if err = json.Unmarshal(frozen.snapshot, &draft); err != nil {
			return nil, err
		}
		currency := ""
		if draft.Contract != nil {
			currency = draft.Contract.Currency
		}
		for _, plan := range draft.Payments {
			item := ContractPaymentSummary{ContractID: record.ContractID, ContractRevisionID: frozen.id, PaymentSequence: plan.Sequence, PlannedAmount: plan.Amount, Currency: currency, NetPaid: "0.00", State: "unknown", Receipts: []ContractPaymentReceipt{}}
			rows, queryErr := tx.Query(ctx, `SELECT receipt_id::text,contract_id::text,contract_revision_id::text,payment_sequence,source_id,source_key_id,source_event_id,event_type,COALESCE(original_source_event_id,''),amount::text,currency,occurred_at,recorded_at,payload_digest FROM clm_contract_payment_receipts WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND payment_sequence=$4 ORDER BY occurred_at,receipt_id`, record.TenantID, record.ContractID, frozen.id, plan.Sequence)
			if queryErr != nil {
				return nil, queryErr
			}
			net := new(big.Int)
			for rows.Next() {
				var receipt ContractPaymentReceipt
				if queryErr = rows.Scan(&receipt.ReceiptID, &receipt.ContractID, &receipt.ContractRevisionID, &receipt.PaymentSequence, &receipt.SourceID, &receipt.SourceKeyID, &receipt.SourceEventID, &receipt.EventType, &receipt.OriginalSourceEventID, &receipt.Amount, &receipt.Currency, &receipt.OccurredAt, &receipt.RecordedAt, &receipt.PayloadDigest); queryErr != nil {
					rows.Close()
					return nil, queryErr
				}
				item.Receipts = append(item.Receipts, receipt)
				cents, ok := paymentCents(receipt.Amount)
				if !ok {
					rows.Close()
					return nil, fault.Conflict("付款事实金额格式无效")
				}
				if receipt.EventType == "payment" {
					net.Add(net, big.NewInt(cents))
				} else {
					net.Sub(net, big.NewInt(cents))
				}
			}
			if queryErr = rows.Err(); queryErr != nil {
				rows.Close()
				return nil, queryErr
			}
			rows.Close()
			item.NetPaid = formatPaymentCents(net)
			if len(item.Receipts) > 0 {
				if _, ok := canonicalPaymentAmount(plan.Amount); ok {
					item.State = "unpaid"
				}
			}
			if net.Sign() > 0 {
				if planned, ok := paymentCents(plan.Amount); ok {
					item.State = "partial"
					if net.Cmp(big.NewInt(planned)) == 0 {
						item.State = "paid"
					}
					if net.Cmp(big.NewInt(planned)) > 0 {
						item.State = "overpaid"
					}
				} else {
					item.State = "unknown"
				}
			}
			out = append(out, item)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func boundedVisibleText(value string, max int) bool {
	if value == "" || len(value) > max || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

func canonicalPaymentAmount(v string) (string, bool) {
	if v == "" {
		return "", false
	}
	parts := strings.Split(v, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts[0]) > 16 || (len(parts[0]) > 1 && parts[0][0] == '0') {
		return "", false
	}
	for _, r := range parts[0] {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) == 0 || len(fraction) > 2 {
			return "", false
		}
		for _, r := range fraction {
			if r < '0' || r > '9' {
				return "", false
			}
		}
	}
	for len(fraction) < 2 {
		fraction += "0"
	}
	return parts[0] + "." + fraction, true
}

func currencyValid(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, r := range value {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
func paymentCents(v string) (int64, bool) {
	var whole, frac string
	parts := strings.Split(v, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, false
	}
	whole = parts[0]
	if len(parts) == 2 {
		frac = parts[1]
	}
	if len(frac) > 2 || len(whole) > 16 {
		return 0, false
	}
	for _, r := range whole + frac {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	for len(frac) < 2 {
		frac += "0"
	}
	if frac == "" {
		frac = "00"
	}
	var w int64
	for _, r := range whole {
		w = w*10 + int64(r-'0')
	}
	f := int64(frac[0]-'0')*10 + int64(frac[1]-'0')
	return w*100 + f, true
}
func formatPaymentCents(v *big.Int) string {
	negative := v.Sign() < 0
	value := new(big.Int).Abs(new(big.Int).Set(v))
	whole, remainder := new(big.Int).QuoRem(value, big.NewInt(100), new(big.Int))
	formatted := fmt.Sprintf("%s.%02d", whole.String(), remainder.Int64())
	if negative {
		return "-" + formatted
	}
	return formatted
}
func exceedsOriginal(original, reversed, amount string) bool {
	o, ok1 := paymentCents(original)
	r, ok2 := paymentCents(reversed)
	a, ok3 := paymentCents(amount)
	return !ok1 || !ok2 || !ok3 || r+a > o
}
