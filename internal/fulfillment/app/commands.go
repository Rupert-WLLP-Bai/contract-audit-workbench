// File metadata (manifest mirror):
// Purpose: Coordinates normalized fulfillment writes, current authorization, idempotent replay, exact allocations, linear retests, evidence and same-transaction event/outbox/receipt writes.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"

	contractapp "contractaudit/internal/contract/app"
	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

func PrepareDelivery(input ContractDeliveryInput) (PreparedDelivery, error) {
	contractID, err := clmcontract.NormalizeContractUUID(input.ContractID)
	if err != nil {
		return PreparedDelivery{}, err
	}
	input.ContractID = contractID
	if input.ExpectedRowVersion < 1 {
		return PreparedDelivery{}, fault.Invalid("expected_row_version 必须为正数")
	}
	if err := normalizeUUIDField("contract_revision_id", &input.ContractRevisionID); err != nil {
		return PreparedDelivery{}, err
	}
	if err := normalizeUUIDField("item_id", &input.ItemID); err != nil {
		return PreparedDelivery{}, err
	}
	input.BatchReference = strings.TrimSpace(input.BatchReference)
	if err := ValidateText("batch_reference", input.BatchReference, 200, true); err != nil {
		return PreparedDelivery{}, err
	}
	if _, err := ParseDecimal("quantity", input.Quantity, false); err != nil {
		return PreparedDelivery{}, err
	}
	if err := ValidateText("location", input.Location, 1000, true); err != nil {
		return PreparedDelivery{}, err
	}
	if err := ValidateText("description", input.Description, 4000, false); err != nil {
		return PreparedDelivery{}, err
	}
	actualAt, err := ParseTime("actual_at", input.ActualAt)
	if err != nil {
		return PreparedDelivery{}, err
	}
	if err := clmcontract.ValidateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return PreparedDelivery{}, err
	}
	digest, err := clmcontract.RequestDigest(CreateDeliveryRoute, "POST", "/api/contracts/"+contractID+"/deliveries", input.ExpectedRowVersion, input)
	if err != nil {
		return PreparedDelivery{}, err
	}
	return PreparedDelivery{Input: input, ActualAt: actualAt, RequestDigest: digest}, nil
}

func PrepareAcceptance(input ContractAcceptanceInput) (PreparedAcceptance, error) {
	contractID, err := clmcontract.NormalizeContractUUID(input.ContractID)
	if err != nil {
		return PreparedAcceptance{}, err
	}
	input.ContractID = contractID
	if input.ExpectedRowVersion < 1 {
		return PreparedAcceptance{}, fault.Invalid("expected_row_version 必须为正数")
	}
	if err := normalizeUUIDField("contract_revision_id", &input.ContractRevisionID); err != nil {
		return PreparedAcceptance{}, err
	}
	if err := normalizeUUIDField("delivery_id", &input.DeliveryID); err != nil {
		return PreparedAcceptance{}, err
	}
	if input.ReinspectionOf != "" {
		if err := normalizeUUIDField("reinspection_of", &input.ReinspectionOf); err != nil {
			return PreparedAcceptance{}, err
		}
	}
	if err := ValidateText("standard", input.Standard, 4000, true); err != nil {
		return PreparedAcceptance{}, err
	}
	if err := ValidateText("inspector_note", input.InspectorNote, 4000, false); err != nil {
		return PreparedAcceptance{}, err
	}
	if len(input.Defects) > 100 {
		return PreparedAcceptance{}, fault.Invalid("每次验收缺陷不能超过100项")
	}
	actualAt, err := ParseTime("actual_at", input.ActualAt)
	if err != nil {
		return PreparedAcceptance{}, err
	}
	if _, err := ParseDecimal("scope_start", input.ScopeStart, true); err != nil {
		return PreparedAcceptance{}, err
	}
	if _, err := ParseDecimal("scope_end", input.ScopeEnd, false); err != nil {
		return PreparedAcceptance{}, err
	}
	if _, err := ParseDecimal("accepted_quantity", input.AcceptedQuantity, true); err != nil {
		return PreparedAcceptance{}, err
	}
	if err := clmcontract.ValidateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return PreparedAcceptance{}, err
	}
	digest, err := clmcontract.RequestDigest(CreateAcceptanceRoute, "POST", "/api/contracts/"+contractID+"/acceptances", input.ExpectedRowVersion, input)
	if err != nil {
		return PreparedAcceptance{}, err
	}
	return PreparedAcceptance{Input: input, ActualAt: actualAt, RequestDigest: digest}, nil
}

func PrepareRectification(input ContractRectificationInput) (PreparedRectification, error) {
	contractID, err := clmcontract.NormalizeContractUUID(input.ContractID)
	if err != nil {
		return PreparedRectification{}, err
	}
	input.ContractID = contractID
	if input.ExpectedRowVersion < 1 {
		return PreparedRectification{}, fault.Invalid("expected_row_version 必须为正数")
	}
	for field, value := range map[string]*string{
		"contract_revision_id": &input.ContractRevisionID,
		"acceptance_id":        &input.AcceptanceID,
		"defect_id":            &input.DefectID,
	} {
		if err := normalizeUUIDField(field, value); err != nil {
			return PreparedRectification{}, err
		}
	}
	if err := ValidateText("description", input.Description, 4000, true); err != nil {
		return PreparedRectification{}, err
	}
	if err := clmcontract.ValidateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return PreparedRectification{}, err
	}
	digest, err := clmcontract.RequestDigest(CreateRectificationRoute, "POST", "/api/contracts/"+contractID+"/acceptances/"+input.AcceptanceID+"/rectifications", input.ExpectedRowVersion, input)
	if err != nil {
		return PreparedRectification{}, err
	}
	return PreparedRectification{Input: input, RequestDigest: digest}, nil
}

func CreateContractDelivery(ctx context.Context, principal Principal, prepared PreparedDelivery,
	contracts contractapp.FulfillmentContractCommands, repository FulfillmentRepository,
	access FulfillmentAccess, events contractapp.VersionBusinessEventAppender,
	outbox contractapp.VersionOutboxEnqueuer) (ContractDelivery, error) {
	input := prepared.Input
	receipt, found, err := loadReplayReceipt(ctx, principal, CreateDeliveryRoute, input.IdempotencyKey, prepared.RequestDigest, input.ContractID, contracts, access)
	if err != nil {
		return ContractDelivery{}, err
	}
	if found {
		var result ContractDelivery
		if err := json.Unmarshal(receipt.Response, &result); err != nil {
			return ContractDelivery{}, err
		}
		if err := requireReplayEvidenceRead(ctx, access, result.Evidence); err != nil {
			return ContractDelivery{}, err
		}
		return result, nil
	}
	if err := contracts.LockContract(ctx, input.ContractID); err != nil {
		return ContractDelivery{}, err
	}
	record, err := loadWritableContract(ctx, principal.TenantID, input.ContractID, input.ExpectedRowVersion, contracts, access)
	if err != nil {
		return ContractDelivery{}, err
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != input.ContractRevisionID {
		return ContractDelivery{}, fault.Conflict("交付必须对应合同当前冻结修订")
	}
	revision, err := contracts.LoadFulfillmentRevision(ctx, record.TenantID, input.ContractID, input.ContractRevisionID)
	if err != nil {
		return ContractDelivery{}, err
	}
	draft, err := contractapp.DecodeFulfillmentSnapshot(revision)
	if err != nil {
		return ContractDelivery{}, err
	}
	plan, err := deliveryPlanForItem(draft, input.DeliveryPlanSequence, input.ItemID)
	if err != nil {
		return ContractDelivery{}, err
	}
	index, ok, err := contracts.LoadFulfillmentRevisionItem(ctx, record.TenantID, input.ContractID, input.ContractRevisionID, input.ItemID)
	if err != nil {
		return ContractDelivery{}, err
	}
	if !ok {
		return ContractDelivery{}, fault.Invalid("商品行不属于指定冻结修订")
	}
	item, err := frozenItemMetadata(revision.Snapshot, input.ItemID)
	if err != nil {
		return ContractDelivery{}, err
	}
	item.Quantity, item.Unit = pointerValue(index.PlannedQuantity), pointerValue(index.Unit)
	if item.Quantity == "" || item.Unit == "" {
		return ContractDelivery{}, fault.Conflict("冻结商品行未提供可核验的计划数量和单位")
	}
	quantity, err := ParseDecimal("quantity", input.Quantity, false)
	if err != nil {
		return ContractDelivery{}, err
	}
	totalText, err := repository.SumDelivered(ctx, record.TenantID, input.ContractID, input.ContractRevisionID, input.ItemID)
	if err != nil {
		return ContractDelivery{}, err
	}
	planned, _ := new(big.Rat).SetString(item.Quantity)
	delivered, _ := new(big.Rat).SetString(totalText)
	if new(big.Rat).Add(delivered, quantity).Cmp(planned) > 0 {
		return ContractDelivery{}, fault.Conflict("累计实际交付数量超过冻结商品行计划数量")
	}
	if plan != nil && plan.Quantity != "" {
		planTotalText, err := repository.SumPlanDelivery(ctx, record.TenantID, input.ContractID,
			input.ContractRevisionID, input.ItemID, *input.DeliveryPlanSequence)
		if err != nil {
			return ContractDelivery{}, err
		}
		planLimit, ok := new(big.Rat).SetString(plan.Quantity)
		prior, priorOK := new(big.Rat).SetString(planTotalText)
		if !ok || !priorOK || new(big.Rat).Add(prior, quantity).Cmp(planLimit) > 0 {
			return ContractDelivery{}, fault.Conflict("累计实际交付数量超过所选冻结交付计划数量")
		}
	}
	evidence, err := resolveFulfillmentEvidence(ctx, input.Evidence, "supporting", access)
	if err != nil {
		return ContractDelivery{}, err
	}
	evidenceJSON, err := json.Marshal(sortedEvidence(evidence))
	if err != nil {
		return ContractDelivery{}, err
	}
	deliveryID, err := clmcontract.NewContractUUID()
	if err != nil {
		return ContractDelivery{}, err
	}
	insertedAt, err := repository.InsertDelivery(ctx, DeliveryInsert{
		TenantID: record.TenantID, ContractID: input.ContractID, ContractRevisionID: input.ContractRevisionID,
		DeliveryID: deliveryID, DeliveryPlanSequence: input.DeliveryPlanSequence, ItemID: input.ItemID,
		BatchReference: input.BatchReference, Quantity: input.Quantity, Unit: item.Unit, ActualAt: prepared.ActualAt,
		Location: strings.TrimSpace(input.Location), Description: strings.TrimSpace(input.Description),
		EvidenceSnapshot: evidenceJSON, RecordedBy: principal.ActorID,
	})
	if err != nil {
		if strings.Contains(err.Error(), "clm_contract_deliveries") {
			return ContractDelivery{}, fault.Conflict("该批次引用已用于此合同商品行")
		}
		return ContractDelivery{}, err
	}
	advanced, err := contracts.AdvanceFulfillmentAggregate(ctx, record, input.ExpectedRowVersion)
	if err != nil {
		return ContractDelivery{}, err
	}
	result := ContractDelivery{
		DeliveryID: deliveryID, ContractID: input.ContractID, ContractRevisionID: input.ContractRevisionID,
		DeliveryPlanSequence: input.DeliveryPlanSequence, ItemID: input.ItemID, ItemName: item.Name,
		BatchReference: input.BatchReference, Quantity: input.Quantity, Unit: item.Unit, ActualAt: prepared.ActualAt,
		Location: strings.TrimSpace(input.Location), Description: strings.TrimSpace(input.Description),
		Evidence: sortedEvidence(evidence), RecordedBy: principal.ActorID, RecordedAt: insertedAt,
	}
	response, err := json.Marshal(result)
	if err != nil {
		return ContractDelivery{}, err
	}
	if err := appendFulfillmentEvent(ctx, record, advanced.RowVersion, DeliveryRecordedEvent,
		map[string]any{"contract_revision_id": input.ContractRevisionID, "delivery_id": deliveryID,
			"item_id": input.ItemID, "quantity": input.Quantity, "unit": item.Unit, "recorded_by": principal.ActorID}, events, outbox); err != nil {
		return ContractDelivery{}, err
	}
	if err := contracts.InsertWriteReceipt(ctx, record.TenantID, principal.ActorID, CreateDeliveryRoute,
		input.IdempotencyKey, prepared.RequestDigest, input.ContractID, 201, response); err != nil {
		return ContractDelivery{}, err
	}
	return result, nil
}

func CreateContractAcceptance(ctx context.Context, principal Principal, prepared PreparedAcceptance,
	contracts contractapp.FulfillmentContractCommands, repository FulfillmentRepository,
	access FulfillmentAccess, events contractapp.VersionBusinessEventAppender,
	outbox contractapp.VersionOutboxEnqueuer) (ContractAcceptance, error) {
	input := prepared.Input
	receipt, found, err := loadReplayReceipt(ctx, principal, CreateAcceptanceRoute, input.IdempotencyKey, prepared.RequestDigest, input.ContractID, contracts, access)
	if err != nil {
		return ContractAcceptance{}, err
	}
	if found {
		var result ContractAcceptance
		if err := json.Unmarshal(receipt.Response, &result); err != nil {
			return ContractAcceptance{}, err
		}
		if err := requireReplayEvidenceRead(ctx, access, result.Evidence); err != nil {
			return ContractAcceptance{}, err
		}
		return result, nil
	}
	if err := contracts.LockContract(ctx, input.ContractID); err != nil {
		return ContractAcceptance{}, err
	}
	record, err := loadWritableContract(ctx, principal.TenantID, input.ContractID, input.ExpectedRowVersion, contracts, access)
	if err != nil {
		return ContractAcceptance{}, err
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != input.ContractRevisionID {
		return ContractAcceptance{}, fault.Conflict("验收必须对应合同当前冻结修订")
	}
	revision, err := contracts.LoadFulfillmentRevision(ctx, record.TenantID, input.ContractID, input.ContractRevisionID)
	if err != nil {
		return ContractAcceptance{}, err
	}
	draft, err := contractapp.DecodeFulfillmentSnapshot(revision)
	if err != nil {
		return ContractAcceptance{}, err
	}
	delivery, found, err := repository.LoadDelivery(ctx, record.TenantID, input.ContractID, input.ContractRevisionID, input.DeliveryID)
	if err != nil {
		return ContractAcceptance{}, err
	}
	if !found {
		return ContractAcceptance{}, fault.NotFound("没有找到对应交付批次")
	}
	itemProjection, itemFound, err := contracts.LoadFulfillmentRevisionItem(ctx, record.TenantID,
		input.ContractID, input.ContractRevisionID, delivery.ItemID)
	if err != nil {
		return ContractAcceptance{}, err
	}
	if !itemFound {
		return ContractAcceptance{}, fault.NotFound("没有找到对应交付批次")
	}
	if itemProjection.Unit == nil {
		return ContractAcceptance{}, fault.Conflict("冻结商品行未提供可核验的计划数量和单位")
	}
	item := parseRevisionItemMetadata(revision.Snapshot)[delivery.ItemID]
	item.Unit = pointerValue(itemProjection.Unit)
	plan, err := acceptancePlanForItem(draft, input.AcceptancePlanSequence, delivery.ItemID)
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
	start, err := ParseDecimal("scope_start", input.ScopeStart, true)
	if err != nil {
		return ContractAcceptance{}, err
	}
	end, err := ParseDecimal("scope_end", input.ScopeEnd, false)
	if err != nil {
		return ContractAcceptance{}, err
	}
	span := new(big.Rat).Sub(end, start)
	if span.Sign() <= 0 {
		return ContractAcceptance{}, fault.Invalid("验收范围必须满足 start < end")
	}
	accepted, err := ParseDecimal("accepted_quantity", input.AcceptedQuantity, true)
	if err != nil {
		return ContractAcceptance{}, err
	}
	if accepted.Cmp(span) > 0 {
		return ContractAcceptance{}, fault.Invalid("accepted_quantity不能超过本次验收范围")
	}
	failed := new(big.Rat).Sub(span, accepted)
	result := acceptanceResult(span, accepted)
	deliveryQty, ok := new(big.Rat).SetString(delivery.Quantity)
	if !ok || end.Cmp(deliveryQty) > 0 {
		return ContractAcceptance{}, fault.Conflict("验收范围超出此实际交付数量")
	}
	parentID := ""
	if input.ReinspectionOf == "" {
		overlaps, err := repository.HasOverlappingRootAcceptance(ctx, record.TenantID, input.ContractID,
			input.DeliveryID, input.ScopeStart, input.ScopeEnd)
		if err != nil {
			return ContractAcceptance{}, err
		}
		if overlaps {
			return ContractAcceptance{}, fault.Conflict("该验收范围已被分配，不能重复统计")
		}
	} else {
		parentID = input.ReinspectionOf
		parent, exists, err := repository.LoadAcceptance(ctx, record.TenantID, input.ContractID, parentID)
		if err != nil {
			return ContractAcceptance{}, err
		}
		if !exists || parent.ContractRevisionID != input.ContractRevisionID || parent.DeliveryID != input.DeliveryID || parent.ItemID != delivery.ItemID {
			return ContractAcceptance{}, fault.NotFound("没有找到可复验的验收事实")
		}
		if parent.Result == "passed" {
			return ContractAcceptance{}, fault.Conflict("通过的验收范围不能复验")
		}
		parentStart, _ := new(big.Rat).SetString(parent.ScopeStart)
		parentEnd, _ := new(big.Rat).SetString(parent.ScopeEnd)
		parentAccepted, _ := new(big.Rat).SetString(parent.AcceptedQuantity)
		expectedStart := new(big.Rat).Add(parentStart, parentAccepted)
		if expectedStart.Cmp(start) != 0 || parentEnd.Cmp(end) != 0 {
			return ContractAcceptance{}, fault.Conflict("复验必须精确覆盖父验收未通过的后缀范围")
		}
		if failedParent := new(big.Rat).Sub(parentEnd, expectedStart); failedParent.Sign() <= 0 {
			return ContractAcceptance{}, fault.Conflict("父验收没有待复验数量")
		}
		if err := validateRectificationsForRetest(ctx, record.TenantID, input.ContractID, parentID,
			input.RectificationIDs, repository, access); err != nil {
			return ContractAcceptance{}, err
		}
	}
	failedTotal := new(big.Rat)
	seenPrior := map[string]struct{}{}
	for index := range input.Defects {
		defect := &input.Defects[index]
		if err := ValidateText("defects[].description", defect.Description, 4000, true); err != nil {
			return ContractAcceptance{}, err
		}
		if err := ValidateText("defects[].responsible", defect.Responsible, 300, true); err != nil {
			return ContractAcceptance{}, err
		}
		if _, err := time.Parse("2006-01-02", defect.RemediationDue); err != nil {
			return ContractAcceptance{}, fault.Invalid("defects[].remediation_due 必须为YYYY-MM-DD")
		}
		quantity, err := ParseDecimal("defects[].failed_quantity", defect.FailedQuantity, false)
		if err != nil {
			return ContractAcceptance{}, err
		}
		failedTotal.Add(failedTotal, quantity)
		if defect.PriorDefectID != "" {
			if input.ReinspectionOf == "" {
				return ContractAcceptance{}, fault.Invalid("首次验收缺陷不能引用prior_defect_id")
			}
			if err := normalizeUUIDField("defects[].prior_defect_id", &defect.PriorDefectID); err != nil {
				return ContractAcceptance{}, err
			}
			if _, exists := seenPrior[defect.PriorDefectID]; exists {
				return ContractAcceptance{}, fault.Invalid("一次复验不能重复引用同一既有缺陷")
			}
			seenPrior[defect.PriorDefectID] = struct{}{}
			found, err := repository.HasDefect(ctx, record.TenantID, input.ContractID, input.ReinspectionOf, defect.PriorDefectID)
			if err != nil {
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
	evidence, err := resolveFulfillmentEvidence(ctx, input.Evidence, "acceptance", access)
	if err != nil {
		return ContractAcceptance{}, err
	}
	evidenceJSON, err := json.Marshal(sortedEvidence(evidence))
	if err != nil {
		return ContractAcceptance{}, err
	}
	acceptanceID, err := clmcontract.NewContractUUID()
	if err != nil {
		return ContractAcceptance{}, err
	}
	var parentForInsert string
	if input.ReinspectionOf != "" {
		parentForInsert = input.ReinspectionOf
	}
	recordedAt, err := repository.InsertAcceptance(ctx, AcceptanceInsert{
		TenantID: record.TenantID, ContractID: input.ContractID, ContractRevisionID: input.ContractRevisionID,
		AcceptanceID: acceptanceID, DeliveryID: input.DeliveryID, ItemID: delivery.ItemID,
		PlanSequence: input.AcceptancePlanSequence, ScopeStart: input.ScopeStart, ScopeEnd: input.ScopeEnd,
		AcceptedQuantity: input.AcceptedQuantity, Result: result, ReinspectionOf: parentForInsert,
		ActualAt: prepared.ActualAt, Standard: standard, InspectorNote: strings.TrimSpace(input.InspectorNote),
		EvidenceSnapshot: evidenceJSON, RecordedBy: principal.ActorID,
	})
	if err != nil {
		if strings.Contains(err.Error(), "clm_contract_acceptances") {
			return ContractAcceptance{}, fault.Conflict("验收关系重复或不匹配")
		}
		return ContractAcceptance{}, err
	}
	for _, rawRectificationID := range input.RectificationIDs {
		rectificationID := rawRectificationID
		if err := normalizeUUIDField("rectification_ids[]", &rectificationID); err != nil {
			return ContractAcceptance{}, err
		}
		rectification, exists, err := repository.LoadRectification(ctx, record.TenantID, input.ContractID, rectificationID)
		if err != nil {
			return ContractAcceptance{}, err
		}
		if !exists {
			return ContractAcceptance{}, fault.Invalid("rectification_id 不存在")
		}
		if err := repository.InsertAcceptanceRectification(ctx, AcceptanceRectificationInsert{
			TenantID: record.TenantID, ContractID: input.ContractID, AcceptanceID: acceptanceID,
			RevisionID: input.ContractRevisionID, DeliveryID: input.DeliveryID, ItemID: delivery.ItemID,
			RectificationID: rectificationID, DefectAcceptanceID: rectification.AcceptanceID,
			DefectID: rectification.DefectID,
		}); err != nil {
			return ContractAcceptance{}, err
		}
	}
	defects := make([]ContractAcceptanceDefect, 0, len(input.Defects))
	for _, inputDefect := range input.Defects {
		defectID, err := clmcontract.NewContractUUID()
		if err != nil {
			return ContractAcceptance{}, err
		}
		priorAcceptanceID, priorDefectID := "", ""
		if inputDefect.PriorDefectID != "" {
			priorAcceptanceID, priorDefectID = input.ReinspectionOf, inputDefect.PriorDefectID
		}
		defectRecordedAt, err := repository.InsertDefect(ctx, DefectInsert{
			TenantID: record.TenantID, ContractID: input.ContractID, ContractRevisionID: input.ContractRevisionID,
			AcceptanceID: acceptanceID, DefectID: defectID, PriorAcceptanceID: priorAcceptanceID,
			PriorDefectID: priorDefectID, Description: strings.TrimSpace(inputDefect.Description),
			Responsible: strings.TrimSpace(inputDefect.Responsible), RemediationDue: inputDefect.RemediationDue,
			FailedQuantity: inputDefect.FailedQuantity, RecordedBy: principal.ActorID,
		})
		if err != nil {
			return ContractAcceptance{}, err
		}
		defects = append(defects, ContractAcceptanceDefect{
			DefectID: defectID, PriorDefectID: inputDefect.PriorDefectID,
			Description: strings.TrimSpace(inputDefect.Description), Responsible: strings.TrimSpace(inputDefect.Responsible),
			RemediationDue: inputDefect.RemediationDue, FailedQuantity: inputDefect.FailedQuantity,
			RecordedBy: principal.ActorID, RecordedAt: defectRecordedAt,
		})
	}
	advanced, err := contracts.AdvanceFulfillmentAggregate(ctx, record, input.ExpectedRowVersion)
	if err != nil {
		return ContractAcceptance{}, err
	}
	output := ContractAcceptance{
		AcceptanceID: acceptanceID, ContractID: input.ContractID, ContractRevisionID: input.ContractRevisionID,
		DeliveryID: input.DeliveryID, ItemID: delivery.ItemID, ItemName: item.Name,
		AcceptancePlanSequence: input.AcceptancePlanSequence, ScopeStart: input.ScopeStart, ScopeEnd: input.ScopeEnd,
		Quantity: RationalString(span), AcceptedQuantity: input.AcceptedQuantity, FailedQuantity: RationalString(failed),
		Unit: item.Unit, Result: result, ReinspectionOf: input.ReinspectionOf,
		RectificationIDs: append([]string(nil), input.RectificationIDs...), ActualAt: prepared.ActualAt,
		Standard: standard, InspectorNote: strings.TrimSpace(input.InspectorNote), Evidence: sortedEvidence(evidence),
		Defects: defects, Rectifications: []ContractRectification{}, RecordedBy: principal.ActorID, RecordedAt: recordedAt,
	}
	response, err := json.Marshal(output)
	if err != nil {
		return ContractAcceptance{}, err
	}
	if err := appendFulfillmentEvent(ctx, record, advanced.RowVersion, AcceptanceRecordedEvent,
		map[string]any{"contract_revision_id": input.ContractRevisionID, "acceptance_id": acceptanceID,
			"delivery_id": input.DeliveryID, "item_id": delivery.ItemID, "scope_start": input.ScopeStart,
			"scope_end": input.ScopeEnd, "accepted_quantity": input.AcceptedQuantity,
			"result": result, "recorded_by": principal.ActorID}, events, outbox); err != nil {
		return ContractAcceptance{}, err
	}
	if err := contracts.InsertWriteReceipt(ctx, record.TenantID, principal.ActorID, CreateAcceptanceRoute,
		input.IdempotencyKey, prepared.RequestDigest, input.ContractID, 201, response); err != nil {
		return ContractAcceptance{}, err
	}
	return output, nil
}

func validateRectificationsForRetest(ctx context.Context, tenantID, contractID, parentID string,
	rectificationIDs []string, repository FulfillmentRepository, access FulfillmentAccess) error {
	defects, err := repository.ListAcceptanceDefects(ctx, tenantID, contractID, parentID)
	if err != nil {
		return err
	}
	if len(defects) == 0 {
		return fault.Conflict("待复验失败事实没有可追踪缺陷记录")
	}
	provided := map[string]struct{}{}
	for _, value := range rectificationIDs {
		id := value
		if err := normalizeUUIDField("rectification_ids[]", &id); err != nil {
			return err
		}
		if _, exists := provided[id]; exists {
			return fault.Invalid("rectification_ids 不能重复")
		}
		provided[id] = struct{}{}
	}
	rectifications, err := repository.ListAcceptanceRectifications(ctx, tenantID, contractID, parentID)
	if err != nil {
		return err
	}
	valid := map[string]string{}
	selectedEvidence := [][]clmcontract.ContractVersionMaterial{}
	for _, rectification := range rectifications {
		valid[rectification.RectificationID] = rectification.DefectID
		if _, selected := provided[rectification.RectificationID]; !selected {
			continue
		}
		var materials []clmcontract.ContractVersionMaterial
		if err := json.Unmarshal(rectification.EvidenceSnapshot, &materials); err != nil {
			return err
		}
		selectedEvidence = append(selectedEvidence, materials)
	}
	for _, materials := range selectedEvidence {
		for _, material := range materials {
			if err := access.CanReadCase(ctx, material.CaseOrgID, material.CaseID); err != nil {
				return fault.NotFound("没有找到对应记录")
			}
		}
	}
	used := map[string]struct{}{}
	for _, defect := range defects {
		found := false
		for rectificationID := range provided {
			if valid[rectificationID] == defect.DefectID {
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

func RecordContractRectification(ctx context.Context, principal Principal, prepared PreparedRectification,
	contracts contractapp.FulfillmentContractCommands, repository FulfillmentRepository,
	access FulfillmentAccess, events contractapp.VersionBusinessEventAppender,
	outbox contractapp.VersionOutboxEnqueuer) (ContractRectification, error) {
	input := prepared.Input
	receipt, found, err := loadReplayReceipt(ctx, principal, CreateRectificationRoute, input.IdempotencyKey, prepared.RequestDigest, input.ContractID, contracts, access)
	if err != nil {
		return ContractRectification{}, err
	}
	if found {
		var saved ContractRectification
		if err := json.Unmarshal(receipt.Response, &saved); err != nil {
			return ContractRectification{}, err
		}
		if err := requireReplayEvidenceRead(ctx, access, saved.Evidence); err != nil {
			return ContractRectification{}, err
		}
		return saved, nil
	}
	if err := contracts.LockContract(ctx, input.ContractID); err != nil {
		return ContractRectification{}, err
	}
	record, err := loadWritableContract(ctx, principal.TenantID, input.ContractID, input.ExpectedRowVersion, contracts, access)
	if err != nil {
		return ContractRectification{}, err
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != input.ContractRevisionID {
		return ContractRectification{}, fault.Conflict("整改事实必须对应合同当前冻结修订")
	}
	result, found, err := repository.LoadAcceptanceResult(ctx, record.TenantID, input.ContractID, input.AcceptanceID, input.ContractRevisionID)
	if err != nil {
		return ContractRectification{}, err
	}
	if !found {
		return ContractRectification{}, fault.NotFound("没有找到对应的验收事实")
	}
	if result == "passed" {
		return ContractRectification{}, fault.Conflict("通过的验收事实不能新增整改记录")
	}
	child, err := repository.HasReinspection(ctx, record.TenantID, input.ContractID, input.AcceptanceID)
	if err != nil {
		return ContractRectification{}, err
	}
	if child {
		return ContractRectification{}, fault.Conflict("复验已经开始，不能再补写该失败事实的整改记录")
	}
	defectExists, err := repository.HasDefect(ctx, record.TenantID, input.ContractID, input.AcceptanceID, input.DefectID)
	if err != nil {
		return ContractRectification{}, err
	}
	if !defectExists {
		return ContractRectification{}, fault.NotFound("没有找到对应的验收缺陷")
	}
	evidence, err := resolveFulfillmentEvidence(ctx, input.Evidence, "acceptance", access)
	if err != nil {
		return ContractRectification{}, err
	}
	evidenceJSON, err := json.Marshal(sortedEvidence(evidence))
	if err != nil {
		return ContractRectification{}, err
	}
	rectificationID, err := clmcontract.NewContractUUID()
	if err != nil {
		return ContractRectification{}, err
	}
	recordedAt, err := repository.InsertRectification(ctx, RectificationInsert{
		TenantID: record.TenantID, ContractID: input.ContractID, ContractRevisionID: input.ContractRevisionID,
		RectificationID: rectificationID, AcceptanceID: input.AcceptanceID, DefectID: input.DefectID,
		Description: strings.TrimSpace(input.Description), EvidenceSnapshot: evidenceJSON, RecordedBy: principal.ActorID,
	})
	if err != nil {
		return ContractRectification{}, err
	}
	advanced, err := contracts.AdvanceFulfillmentAggregate(ctx, record, input.ExpectedRowVersion)
	if err != nil {
		return ContractRectification{}, err
	}
	output := ContractRectification{
		RectificationID: rectificationID, ContractID: input.ContractID, ContractRevisionID: input.ContractRevisionID,
		AcceptanceID: input.AcceptanceID, DefectID: input.DefectID, Description: strings.TrimSpace(input.Description),
		Evidence: sortedEvidence(evidence), RecordedBy: principal.ActorID, RecordedAt: recordedAt,
	}
	response, err := json.Marshal(output)
	if err != nil {
		return ContractRectification{}, err
	}
	if err := appendFulfillmentEvent(ctx, record, advanced.RowVersion, RectificationRecordedEvent,
		map[string]any{"contract_revision_id": input.ContractRevisionID, "rectification_id": rectificationID,
			"acceptance_id": input.AcceptanceID, "defect_id": input.DefectID, "recorded_by": principal.ActorID}, events, outbox); err != nil {
		return ContractRectification{}, err
	}
	if err := contracts.InsertWriteReceipt(ctx, record.TenantID, principal.ActorID, CreateRectificationRoute,
		input.IdempotencyKey, prepared.RequestDigest, input.ContractID, 201, response); err != nil {
		return ContractRectification{}, err
	}
	return output, nil
}

// loadReplayReceipt preserves the shared receipt lock and current-read checks.
func loadReplayReceipt(ctx context.Context, principal Principal, route, key, digest, contractID string,
	contracts contractapp.FulfillmentContractCommands, access FulfillmentAccess) (clmcontract.WriteReceipt, bool, error) {
	if err := contracts.LockWriteReceipt(ctx, principal.TenantID, principal.ActorID, route, key); err != nil {
		return clmcontract.WriteReceipt{}, false, err
	}
	receipt, found, err := contracts.LoadWriteReceipt(ctx, principal.TenantID, principal.ActorID, route, key)
	if err != nil || !found {
		return clmcontract.WriteReceipt{}, false, err
	}
	if receipt.RequestDigest != digest {
		return clmcontract.WriteReceipt{}, false, fault.Conflict("Idempotency-Key已用于不同请求载荷")
	}
	if receipt.ContractID != contractID {
		return clmcontract.WriteReceipt{}, false, fault.Conflict("Idempotency-Key已用于其他合同")
	}
	record, err := contracts.LoadContract(ctx, principal.TenantID, receipt.ContractID)
	if err != nil {
		return clmcontract.WriteReceipt{}, false, err
	}
	if !access.CanRead(ctx, record) {
		return clmcontract.WriteReceipt{}, false, fault.NotFound("没有找到对应记录")
	}
	return receipt, true, nil
}

func loadWritableContract(ctx context.Context, tenantID, contractID string, expected int64,
	contracts contractapp.FulfillmentContractCommands, access FulfillmentAccess) (clmcontract.Aggregate, error) {
	record, err := contracts.LoadContract(ctx, tenantID, contractID)
	if err != nil {
		return clmcontract.Aggregate{}, err
	}
	if err := RequireFulfillmentManage(ctx, record, access); err != nil {
		return clmcontract.Aggregate{}, err
	}
	if record.RowVersion != expected {
		return clmcontract.Aggregate{}, fault.Conflict("合同已被修改，请刷新后重试")
	}
	return record, nil
}

func RequireFulfillmentManage(ctx context.Context, record clmcontract.Aggregate, access FulfillmentAccess) error {
	if !access.CanReadForManagement(ctx, record) {
		return fault.NotFound("没有找到对应记录")
	}
	if !access.CanManage(ctx, record) {
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

func appendFulfillmentEvent(ctx context.Context, record clmcontract.Aggregate, version int64, eventType string, payload any,
	events contractapp.VersionBusinessEventAppender, outbox contractapp.VersionOutboxEnqueuer) error {
	eventID, err := clmcontract.NewContractUUID()
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := events.AppendContractEvent(ctx, record.TenantID, eventID, record.ContractID, version, eventType, body); err != nil {
		return err
	}
	return outbox.EnqueueContractEvent(ctx, record.TenantID, eventID)
}

func requireReplayEvidenceRead(ctx context.Context, access FulfillmentAccess, materials []clmcontract.ContractVersionMaterial) error {
	for _, material := range materials {
		if err := access.CanReadCase(ctx, material.CaseOrgID, material.CaseID); err != nil {
			return fault.NotFound("没有找到对应记录")
		}
	}
	return nil
}

func resolveFulfillmentEvidence(ctx context.Context, refs []clmcontract.MaterialRef, purpose string,
	access FulfillmentAccess) ([]clmcontract.ContractVersionMaterial, error) {
	if len(refs) == 0 || len(refs) > 50 {
		return nil, fault.Invalid("履约事实必须引用1至50个案件材料版本")
	}
	seen := map[string]struct{}{}
	result := make([]clmcontract.ContractVersionMaterial, 0, len(refs))
	for _, ref := range refs {
		key := ref.MaterialID + "\x00" + ref.MaterialVersionID
		if _, ok := seen[key]; ok {
			return nil, fault.Invalid("履约证据不能重复引用同一材料版本")
		}
		seen[key] = struct{}{}
		if ref.Purpose != purpose && ref.Purpose != "supporting" && ref.Purpose != "other" {
			return nil, fault.Invalid("履约证据用途不匹配")
		}
		resolved, err := access.ResolveMaterial(ctx, ref, purpose)
		if err != nil {
			return nil, err
		}
		result = append(result, resolved)
	}
	return result, nil
}

func projectEvidence(ctx context.Context, raw []byte, access FulfillmentAccess) ([]clmcontract.ContractVersionMaterial, bool, error) {
	var all []clmcontract.ContractVersionMaterial
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, false, err
	}
	visible := make([]clmcontract.ContractVersionMaterial, 0, len(all))
	redacted := false
	for _, material := range all {
		if err := access.CanReadCase(ctx, material.CaseOrgID, material.CaseID); err != nil {
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

func ResolveEvidence(ctx context.Context, refs []clmcontract.MaterialRef, purpose string,
	access FulfillmentAccess) ([]clmcontract.ContractVersionMaterial, error) {
	return resolveFulfillmentEvidence(ctx, refs, purpose, access)
}

func ProjectEvidenceSnapshot(ctx context.Context, raw []byte, access FulfillmentAccess) ([]clmcontract.ContractVersionMaterial, bool, error) {
	return projectEvidence(ctx, raw, access)
}
