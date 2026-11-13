package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"
	"time"

	contractapp "contractaudit/internal/contract/app"
	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
	fulfillment "contractaudit/internal/fulfillment/app"
)

const ReceiptEvent = "contract.asset_erp_receipt_recorded.v1"

type PreparedReceipt struct {
	Input    ContractAssetReceiptInput
	Digest   string
	Quantity *big.Rat
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

func PrepareReceipt(source ContractAssetSource, input ContractAssetReceiptInput) (PreparedReceipt, error) {
	var prepared PreparedReceipt
	if strings.TrimSpace(source.TenantID) == "" || strings.TrimSpace(source.SourceID) == "" || !boundedVisibleText(source.SourceID, 200) || !boundedVisibleText(source.KeyID, 200) {
		return prepared, fault.Forbidden("资产来源未授权")
	}
	contractID, err := contract.NormalizeContractUUID(input.ContractID)
	if err != nil {
		return prepared, err
	}
	input.ContractID = contractID
	handoverID, err := contract.NormalizeContractUUID(input.HandoverID)
	if err != nil {
		return prepared, fault.Invalid("handover_id 必须是有效 UUID")
	}
	input.HandoverID = handoverID
	input.EventID, input.EventType, input.AssetCode, input.Unit = strings.TrimSpace(input.EventID), strings.TrimSpace(input.EventType), strings.TrimSpace(input.AssetCode), strings.TrimSpace(input.Unit)
	input.OriginalEventID = strings.TrimSpace(input.OriginalEventID)
	if !boundedVisibleText(input.EventID, 300) || !boundedVisibleText(input.AssetCode, 200) || !boundedVisibleText(input.Unit, 40) || !input.OccurredAt.After(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)) || input.OccurredAt.After(time.Now().Add(5*time.Minute)) {
		return prepared, fault.Invalid("资产回执字段无效")
	}
	if input.EventType != "receipt" && input.EventType != "reversal" {
		return prepared, fault.Invalid("event_type 必须是 receipt 或 reversal")
	}
	if input.EventType == "receipt" && input.OriginalEventID != "" || input.EventType == "reversal" && !boundedVisibleText(input.OriginalEventID, 300) {
		return prepared, fault.Invalid("冲正必须且仅可引用原来源事件")
	}
	quantity, err := fulfillment.ParseDecimal("quantity", strings.TrimSpace(input.Quantity), false)
	if err != nil {
		return prepared, err
	}
	input.Quantity = fulfillment.RationalString(quantity)
	canonical := struct {
		Tenant, Source, Contract, Handover, EventID, EventType, Original, AssetCode, Quantity, Unit string
		OccurredAt                                                                                  time.Time
	}{source.TenantID, source.SourceID, input.ContractID, input.HandoverID, input.EventID, input.EventType, input.OriginalEventID, input.AssetCode, input.Quantity, input.Unit, input.OccurredAt.UTC()}
	body, err := json.Marshal(canonical)
	if err != nil {
		return prepared, err
	}
	sum := sha256.Sum256(body)
	return PreparedReceipt{Input: input, Digest: hex.EncodeToString(sum[:]), Quantity: quantity}, nil
}

func receiptRat(raw string) *big.Rat {
	value, _ := fulfillment.ParseDecimal("quantity", raw, true)
	if value == nil {
		return new(big.Rat)
	}
	return value
}

func RecordReceipt(ctx context.Context, source ContractAssetSource, p PreparedReceipt, c ReceiptContracts, r ReceiptRepository, events contractapp.VersionBusinessEventAppender, outbox contractapp.VersionOutboxEnqueuer) (ContractAssetReceipt, bool, error) {
	var result ContractAssetReceipt
	input := p.Input
	if err := r.LockSourceEvent(ctx, source.TenantID, source.SourceID, input.EventID); err != nil {
		return result, false, err
	}
	if err := c.LockContract(ctx, input.ContractID); err != nil {
		return result, false, err
	}
	oldDigest, found, err := r.LoadReceiptDigest(ctx, source.TenantID, source.SourceID, input.EventID)
	if err != nil {
		return result, false, err
	}
	if found {
		if oldDigest != p.Digest {
			return result, false, fault.Conflict("来源事件ID已用于不同资产回执")
		}
		result, err = r.LoadReceipt(ctx, source.TenantID, source.SourceID, input.EventID)
		if err != nil {
			return result, false, err
		}
		return result, true, nil
	}
	record, err := c.LoadContract(ctx, source.TenantID, input.ContractID)
	if err != nil {
		return result, false, err
	}
	if record.AuthorityMode != "native" {
		return result, false, fault.Conflict("资产ERP回执必须绑定本地权威合同")
	}
	handover, found, err := r.LoadReceiptHandover(ctx, source.TenantID, input.ContractID, input.HandoverID)
	if err != nil {
		return result, false, err
	}
	if !found {
		return result, false, fault.NotFound("没有找到对应资产移交")
	}
	if handover.Unit != input.Unit {
		return result, false, fault.Conflict("资产回执单位与移交范围不一致")
	}
	if input.EventType == "reversal" {
		original, e := r.LoadReceipt(ctx, source.TenantID, source.SourceID, input.OriginalEventID)
		if e != nil || original.EventType != "receipt" || original.ContractID != input.ContractID || original.HandoverID != input.HandoverID || original.AssetCode != input.AssetCode || original.Unit != input.Unit {
			return result, false, fault.Invalid("冲正必须引用同来源、同移交及同资产编码/单位的原回执")
		}
		reversed, e := r.SumReceiptReversals(ctx, source.TenantID, source.SourceID, input.OriginalEventID)
		if e != nil {
			return result, false, e
		}
		rev, _ := fulfillment.ParseDecimal("reversed", reversed, true)
		if rev == nil {
			rev = new(big.Rat)
		}
		if new(big.Rat).Add(rev, p.Quantity).Cmp(receiptRat(original.Quantity)) > 0 {
			return result, false, fault.Invalid("累计冲正数量超过原资产回执")
		}
	}
	net, err := r.NetHandoverReceipts(ctx, source.TenantID, input.ContractID, input.HandoverID)
	if err != nil {
		return result, false, err
	}
	netRat, _ := fulfillment.ParseDecimal("net receipt", net, true)
	if netRat == nil {
		netRat = new(big.Rat)
	}
	if input.EventType == "receipt" && new(big.Rat).Add(netRat, p.Quantity).Cmp(receiptRat(handover.Quantity)) > 0 {
		return result, false, fault.Invalid("累计ERP入账数量超过移交数量")
	}
	if input.EventType == "reversal" && new(big.Rat).Sub(netRat, p.Quantity).Sign() < 0 {
		return result, false, fault.Invalid("冲正数量超过当前净入账数量")
	}
	version, err := c.AdvanceFactVersion(ctx, record.TenantID, input.ContractID, record.RowVersion)
	if err != nil {
		return result, false, err
	}
	receiptID, err := contract.NewContractUUID()
	if err != nil {
		return result, false, err
	}
	result = ContractAssetReceipt{ReceiptID: receiptID, ContractID: input.ContractID, HandoverID: input.HandoverID, SourceID: source.SourceID, SourceKeyID: source.KeyID, SourceEventID: input.EventID, EventType: input.EventType, OriginalSourceEventID: input.OriginalEventID, AssetCode: input.AssetCode, Quantity: input.Quantity, Unit: input.Unit, OccurredAt: input.OccurredAt.UTC(), PayloadDigest: p.Digest}
	result.RecordedAt, err = r.InsertReceipt(ctx, source.TenantID, result)
	if err != nil {
		return ContractAssetReceipt{RecordedAt: result.RecordedAt}, false, err
	}
	eventID, err := contract.NewContractUUID()
	if err != nil {
		return ContractAssetReceipt{}, false, err
	}
	body, err := json.Marshal(map[string]any{"receipt_id": receiptID, "handover_id": input.HandoverID, "asset_plan_id": handover.AssetPlanID, "contract_revision_id": handover.RevisionID, "source_id": source.SourceID, "source_key_id": source.KeyID, "source_event_id": input.EventID, "event_type": input.EventType, "asset_code": input.AssetCode, "quantity": input.Quantity, "unit": input.Unit, "payload_digest": p.Digest})
	if err != nil {
		return ContractAssetReceipt{}, false, err
	}
	if err = events.AppendContractEvent(ctx, record.TenantID, eventID, input.ContractID, version, ReceiptEvent, body); err != nil {
		return ContractAssetReceipt{}, false, err
	}
	if err = outbox.EnqueueContractEvent(ctx, record.TenantID, eventID); err != nil {
		return ContractAssetReceipt{}, false, err
	}
	return result, false, nil
}
