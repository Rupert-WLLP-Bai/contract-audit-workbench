package app

import (
	"context"
	contractapp "contractaudit/internal/contract/app"
	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

type PreparedReceipt struct {
	Input  ContractPaymentReceiptInput
	Digest string
}

func PrepareReceipt(source TrustedPaymentSource, in ContractPaymentReceiptInput) (PreparedReceipt, error) {
	var p PreparedReceipt
	if strings.TrimSpace(source.TenantID) == "" || strings.TrimSpace(source.SourceID) == "" || len(source.SourceID) > 200 || !BoundedVisibleText(source.KeyID, 200) {
		return p, fault.Forbidden("付款来源未授权")
	}
	if err := normalizeUUIDField("contract_id", &in.ContractID); err != nil {
		return p, err
	}
	if err := normalizeUUIDField("contract_revision_id", &in.RevisionID); err != nil {
		return p, err
	}
	in.EventID = strings.TrimSpace(in.EventID)
	in.EventType = strings.TrimSpace(in.EventType)
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Sequence < 1 || !BoundedVisibleText(in.EventID, 300) || !in.OccurredAt.After(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)) || in.OccurredAt.After(time.Now().Add(5*time.Minute)) {
		return p, fault.Invalid("付款来源事实字段无效")
	}
	if in.EventType != "payment" && in.EventType != "reversal" {
		return p, fault.Invalid("event_type 必须是 payment 或 reversal")
	}
	in.OriginalEventID = strings.TrimSpace(in.OriginalEventID)
	if in.EventType == "payment" && in.OriginalEventID != "" || in.EventType == "reversal" && !BoundedVisibleText(in.OriginalEventID, 300) {
		return p, fault.Invalid("冲正必须且仅可引用原来源交易号")
	}
	amount, ok := canonicalPaymentAmount(in.Amount)
	cents, centsOK := paymentCents(amount)
	if !ok || !currencyValid(in.Currency) || !centsOK || cents <= 0 {
		return p, fault.Invalid("付款金额或币种格式无效")
	}
	in.Amount = amount
	return PreparedReceipt{Input: in}, nil
}

func DigestReceipt(source TrustedPaymentSource, p PreparedReceipt) (PreparedReceipt, error) {
	in := p.Input
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
	}{source.TenantID, source.SourceID, in.ContractID, in.RevisionID, in.Sequence, in.EventID, in.EventType, in.OriginalEventID, in.Amount, in.Currency, in.OccurredAt.UTC()}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return p, err
	}
	h := sha256.Sum256(raw)
	p.Digest = hex.EncodeToString(h[:])
	return p, nil
}
func RecordReceipt(ctx context.Context, source TrustedPaymentSource, p PreparedReceipt, c Contracts, r Repository, events contractapp.VersionBusinessEventAppender, outbox contractapp.VersionOutboxEnqueuer) (ContractPaymentReceipt, bool, error) {
	var result ContractPaymentReceipt
	in := p.Input
	if err := r.LockSourceEvent(ctx, source.TenantID, source.SourceID, in.EventID); err != nil {
		return result, false, err
	}
	if err := c.LockContract(ctx, in.ContractID); err != nil {
		return result, false, err
	}
	old, found, err := r.LoadReceipt(ctx, source.TenantID, source.SourceID, in.EventID)
	if err != nil {
		return result, false, err
	}
	if found {
		if old.PayloadDigest != p.Digest {
			return result, false, fault.Conflict("来源事件ID已用于不同载荷")
		}
		return old, true, nil
	}
	record, err := c.LoadContract(ctx, source.TenantID, in.ContractID)
	if err != nil {
		return result, false, err
	}
	if record.AuthorityMode != "native" {
		return result, false, fault.Conflict("付款来源事实必须绑定本系统权威合同")
	}
	revision, err := c.LoadFulfillmentRevision(ctx, source.TenantID, in.ContractID, in.RevisionID)
	if err != nil {
		return result, false, err
	}
	draft, err := contractapp.DecodeFulfillmentSnapshot(revision)
	if err != nil {
		return result, false, err
	}
	_, signed, err := c.LoadSigningRegistration(ctx, source.TenantID, in.ContractID, in.RevisionID)
	if err != nil {
		return result, false, err
	}
	if !signed {
		return result, false, fault.Conflict("付款来源事实必须绑定已登记签署的不可变修订")
	}
	foundPlan := false
	for _, plan := range draft.Payments {
		if plan.Sequence == in.Sequence {
			foundPlan = true
			break
		}
	}
	if !foundPlan {
		return result, false, fault.Invalid("来源事件付款期次不属于冻结计划")
	}
	if draft.Contract == nil || draft.Contract.Currency != in.Currency {
		return result, false, fault.Conflict("来源事件币种与冻结合同币种不一致")
	}
	if in.EventType == "reversal" {
		orig, found, err := r.LoadReceipt(ctx, source.TenantID, source.SourceID, in.OriginalEventID)
		if err != nil || !found || orig.EventType != "payment" || orig.ContractID != in.ContractID || orig.ContractRevisionID != in.RevisionID || orig.PaymentSequence != in.Sequence || orig.Currency != in.Currency {
			return result, false, fault.Invalid("冲正必须引用同来源、同合同修订、同付款期次和币种的原付款")
		}
		reversed, err := r.SumReversals(ctx, source.TenantID, source.SourceID, in.OriginalEventID)
		if err != nil {
			return result, false, err
		}
		if exceedsOriginal(orig.Amount, reversed, in.Amount) {
			return result, false, fault.Invalid("累计冲正金额超过原付款金额")
		}
	}
	version, err := c.AdvanceFactVersion(ctx, source.TenantID, in.ContractID, record.RowVersion)
	if err != nil {
		return result, false, err
	}
	id, err := contract.NewContractUUID()
	if err != nil {
		return result, false, err
	}
	result = ContractPaymentReceipt{ReceiptID: id, ContractID: in.ContractID, ContractRevisionID: in.RevisionID, PaymentSequence: in.Sequence, SourceID: source.SourceID, SourceKeyID: source.KeyID, SourceEventID: in.EventID, EventType: in.EventType, OriginalSourceEventID: in.OriginalEventID, Amount: in.Amount, Currency: in.Currency, OccurredAt: in.OccurredAt.UTC(), PayloadDigest: p.Digest}
	result.RecordedAt, err = r.InsertReceipt(ctx, source.TenantID, result)
	if err != nil {
		return result, false, err
	}
	err = appendEvent(ctx, source.TenantID, in.ContractID, version, ReceiptEvent, map[string]any{"receipt_id": id, "contract_revision_id": in.RevisionID, "payment_sequence": in.Sequence, "source_id": source.SourceID, "source_key_id": source.KeyID, "source_event_id": in.EventID, "event_type": in.EventType, "original_source_event_id": in.OriginalEventID, "amount": in.Amount, "currency": in.Currency, "occurred_at": in.OccurredAt.UTC(), "payload_digest": p.Digest}, events, outbox)
	if err != nil {
		return ContractPaymentReceipt{}, false, err
	}
	return result, false, nil
}
