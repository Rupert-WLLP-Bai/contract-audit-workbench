package app

import (
	"context"
	contractapp "contractaudit/internal/contract/app"
	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
	fulfillmentapp "contractaudit/internal/fulfillment/app"
	"encoding/json"
	"math/big"
	"strings"
	"time"
)

const EligibilityRoute = "clm.payment_eligibility"
const ReceiptEvent = "contract.payment_receipt_recorded.v1"

func normalizeUUIDField(field string, value *string) error {
	v, err := contract.NormalizeContractUUID(*value)
	if err != nil {
		return fault.Invalid(field + " 必须是有效 UUID")
	}
	*value = v
	return nil
}
func PrepareEligibility(in ContractPaymentEligibilityInput) (ContractPaymentEligibilityInput, error) {
	if err := normalizeUUIDField("contract_revision_id", &in.RevisionID); err != nil {
		return in, err
	}
	if in.Sequence < 1 || in.ExpectedRowVersion < 1 || strings.TrimSpace(in.IdempotencyKey) == "" {
		return in, fault.Invalid("payment sequence、expected_row_version 和幂等键必填")
	}
	return in, nil
}
func CheckEligibility(ctx context.Context, tenant, actor string, in ContractPaymentEligibilityInput, c Contracts, r Repository, a Access, accepted AcceptanceReader, events contractapp.VersionBusinessEventAppender, outbox contractapp.VersionOutboxEnqueuer) (ContractPaymentEligibility, error) {
	var result ContractPaymentEligibility
	if err := c.LockContract(ctx, in.ContractID); err != nil {
		return result, err
	}
	record, err := c.LoadContract(ctx, tenant, in.ContractID)
	if err != nil {
		return result, err
	}
	if err := fulfillmentapp.RequireFulfillmentManage(ctx, record, a); err != nil {
		return result, err
	}
	digest, err := contract.RequestDigest(EligibilityRoute, "POST", "/api/contracts/"+record.ContractID+"/payment-eligibility-checks", in.ExpectedRowVersion, struct {
		Revision string `json:"revision"`
		Sequence int    `json:"sequence"`
	}{in.RevisionID, in.Sequence})
	if err != nil {
		return result, err
	}
	if err = c.LockWriteReceipt(ctx, tenant, actor, EligibilityRoute, in.IdempotencyKey); err != nil {
		return result, err
	}
	receipt, found, err := c.LoadWriteReceipt(ctx, tenant, actor, EligibilityRoute, in.IdempotencyKey)
	if err != nil {
		return result, err
	}
	if found {
		if receipt.RequestDigest != digest {
			return result, fault.Conflict("Idempotency-Key已用于不同请求载荷")
		}
		if receipt.ContractID != record.ContractID {
			return result, fault.Conflict("Idempotency-Key已用于其他合同")
		}
		current, err := c.LoadContract(ctx, tenant, receipt.ContractID)
		if err != nil {
			return result, err
		}
		if !a.CanRead(ctx, current) {
			return result, fault.NotFound("没有找到对应记录")
		}
		err = json.Unmarshal(receipt.Response, &result)
		return result, err
	}
	if record.RowVersion != in.ExpectedRowVersion {
		return result, fault.Conflict("合同已被修改，请刷新后重新确认")
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != in.RevisionID {
		return result, fault.Conflict("付款条件必须对应当前不可变修订")
	}
	revision, err := c.LoadFulfillmentRevision(ctx, tenant, record.ContractID, in.RevisionID)
	if err != nil {
		return result, err
	}
	draft, err := contractapp.DecodeFulfillmentSnapshot(revision)
	if err != nil {
		return result, err
	}
	var plan *contract.DraftPaymentPlan
	for i := range draft.Payments {
		if draft.Payments[i].Sequence == in.Sequence {
			plan = &draft.Payments[i]
			break
		}
	}
	if plan == nil {
		return result, fault.Invalid("payment_sequence 不属于冻结付款计划")
	}
	status, reasons, evidence := evaluateCondition(ctx, record, draft, *plan, c, accepted)
	version, err := c.AdvanceFactVersion(ctx, tenant, record.ContractID, record.RowVersion)
	if err != nil {
		return result, err
	}
	checkID, err := contract.NewContractUUID()
	if err != nil {
		return result, err
	}
	result = ContractPaymentEligibility{CheckID: checkID, ContractID: record.ContractID, ContractRevisionID: in.RevisionID, PaymentSequence: in.Sequence, Status: status, Reasons: reasons, Evidence: evidence, ContractRowVersion: version, InitiatesPayment: false}
	result.CheckedAt, err = r.InsertEligibility(ctx, tenant, actor, result)
	if err != nil {
		return result, err
	}
	if err = appendEvent(ctx, tenant, record.ContractID, version, "contract.payment_eligibility_checked.v1", map[string]any{"check_id": checkID, "contract_revision_id": in.RevisionID, "payment_sequence": in.Sequence, "status": status}, events, outbox); err != nil {
		return result, err
	}
	response, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if err = c.InsertWriteReceipt(ctx, tenant, actor, EligibilityRoute, in.IdempotencyKey, digest, record.ContractID, 201, response); err != nil {
		return result, err
	}
	return result, nil
}
func appendEvent(ctx context.Context, tenant, id string, version int64, event string, payload any, events contractapp.VersionBusinessEventAppender, outbox contractapp.VersionOutboxEnqueuer) error {
	eventID, err := contract.NewContractUUID()
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err = events.AppendContractEvent(ctx, tenant, eventID, id, version, event, body); err != nil {
		return err
	}
	return outbox.EnqueueContractEvent(ctx, tenant, eventID)
}

func evaluateCondition(ctx context.Context, record contract.Aggregate, draft contract.DraftContent, plan contract.DraftPaymentPlan, c Contracts, accepted AcceptanceReader) (string, []string, []string) {
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
		registration, found, err := c.LoadSigningRegistration(ctx, record.TenantID, record.ContractID, *record.CurrentRevisionID)
		if err == nil && !found {
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
			itemFact, found, err := c.LoadFulfillmentRevisionItem(ctx, record.TenantID, record.ContractID, *record.CurrentRevisionID, item.ItemID)
			var positive bool
			if itemFact.PlannedQuantity != nil {
				n, ok := new(big.Rat).SetString(*itemFact.PlannedQuantity)
				positive = ok && n.Sign() > 0
			}
			if err != nil || !found || itemFact.PlannedQuantity == nil || itemFact.Unit == nil || strings.TrimSpace(*itemFact.Unit) == "" || !positive {
				return unknown("frozen_item_quantity_or_unit_unknown")
			}
			qualified, err := accepted.SumAcceptedQuantity(ctx, record.TenantID, record.ContractID, *record.CurrentRevisionID, item.ItemID)
			if err != nil {
				return unknown("acceptance_facts_unavailable")
			}
			if qualified != *itemFact.PlannedQuantity {
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
		_, exists, err := c.LoadSigningRegistration(ctx, record.TenantID, record.ContractID, *record.CurrentRevisionID)
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
