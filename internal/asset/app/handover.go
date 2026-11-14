// File metadata (manifest mirror):
// Purpose: Coordinates frozen asset handovers through current authorization and exact Contract/Fulfillment facts, preserving range allocation, evidence/directory provenance and atomic lifecycle/event/outbox/receipt writes.
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
	"math/big"
	"strings"
	"time"

	"contractaudit/internal/accounts"
	contractapp "contractaudit/internal/contract/app"
	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
	fulfillment "contractaudit/internal/fulfillment/app"
)

const HandoverRoute = "clm.create_asset_handover"
const HandoverEvent = "contract.asset_handover_recorded.v1"

type PreparedHandover struct {
	Input        ContractAssetHandoverInput
	ContractID   string
	Digest       string
	HandedOverAt time.Time
	Start, End   *big.Rat
}

func PrepareHandover(input ContractAssetHandoverInput) (PreparedHandover, error) {
	var p PreparedHandover
	id, err := contract.NormalizeContractUUID(input.ContractID)
	if err != nil {
		return p, err
	}
	for field, value := range map[string]*string{"contract_revision_id": &input.ContractRevisionID, "asset_plan_id": &input.AssetPlanID, "delivery_id": &input.DeliveryID, "acceptance_id": &input.AcceptanceID} {
		normalized, e := contract.NormalizeContractUUID(*value)
		if e != nil {
			return p, fault.Invalid(field + " 必须是有效 UUID")
		}
		*value = normalized
	}
	start, err := fulfillment.ParseDecimal("scope_start", strings.TrimSpace(input.ScopeStart), true)
	if err != nil {
		return p, err
	}
	end, err := fulfillment.ParseDecimal("scope_end", strings.TrimSpace(input.ScopeEnd), false)
	if err != nil || start.Cmp(end) >= 0 {
		return p, fault.Invalid("scope_end must exceed scope_start")
	}
	input.ScopeStart, input.ScopeEnd = fulfillment.RationalString(start), fulfillment.RationalString(end)
	at, err := fulfillment.ParseTime("handed_over_at", input.HandedOverAt)
	if err != nil {
		return p, err
	}
	if at.After(time.Now().UTC().Add(5 * time.Minute)) {
		return p, fault.Invalid("handed_over_at cannot be in the future")
	}
	input.HandedOverAt = at.Format(time.RFC3339Nano)
	if input.ExpectedRowVersion < 1 || strings.TrimSpace(input.IdempotencyKey) == "" {
		return p, fault.Invalid("expected_row_version and idempotency key are required")
	}
	if err = fulfillment.ValidateText("transfer_note", input.TransferNote, 2000, true); err != nil {
		return p, err
	}
	input.TransferNote = strings.TrimSpace(input.TransferNote)
	if err = fulfillment.ValidateText("location", input.Location, 1000, true); err != nil {
		return p, err
	}
	input.Location = strings.TrimSpace(input.Location)
	digest, err := contract.RequestDigest(HandoverRoute, "POST", "/api/contracts/"+id+"/asset-handovers", input.ExpectedRowVersion, input)
	if err != nil {
		return p, err
	}
	return PreparedHandover{Input: input, ContractID: id, Digest: digest, HandedOverAt: at, Start: start, End: end}, nil
}

func CreateHandover(ctx context.Context, tenant, actor string, p PreparedHandover, c Contracts, r HandoverRepository, a Access, acceptance AcceptanceReader, directory Directory, events contractapp.VersionBusinessEventAppender, outbox contractapp.VersionOutboxEnqueuer) (ContractAssetHandover, error) {
	var empty ContractAssetHandover
	input, id := p.Input, p.ContractID
	record, err := c.LoadContract(ctx, tenant, id)
	if err != nil {
		return empty, err
	}
	if err = fulfillment.RequireFulfillmentManage(ctx, record, a); err != nil {
		return empty, err
	}
	if err = c.LockWriteReceipt(ctx, tenant, actor, HandoverRoute, input.IdempotencyKey); err != nil {
		return empty, err
	}
	prior, found, err := c.LoadWriteReceipt(ctx, tenant, actor, HandoverRoute, input.IdempotencyKey)
	if err != nil {
		return empty, err
	}
	if found {
		if prior.RequestDigest != p.Digest {
			return empty, fault.Conflict("Idempotency-Key已用于不同请求载荷")
		}
		if prior.ContractID != id {
			return empty, fault.Conflict("Idempotency-Key已用于其他合同")
		}
		current, e := c.LoadContract(ctx, tenant, prior.ContractID)
		if e != nil {
			return empty, e
		}
		if !a.CanRead(ctx, current) {
			return empty, fault.NotFound("没有找到对应记录")
		}
		var saved ContractAssetHandover
		if e = json.Unmarshal(prior.Response, &saved); e != nil {
			return empty, e
		}
		for _, evidence := range saved.Evidence {
			if e = a.CanReadCase(ctx, evidence.CaseOrgID, evidence.CaseID); e != nil {
				return empty, fault.NotFound("没有找到对应记录")
			}
		}
		return saved, nil
	}
	if err = c.LockContract(ctx, id); err != nil {
		return empty, err
	}
	record, err = c.LoadContract(ctx, tenant, id)
	if err != nil {
		return empty, err
	}
	if err = fulfillment.RequireFulfillmentManage(ctx, record, a); err != nil {
		return empty, err
	}
	// The prior Store implementation repeated this check after its writable load.
	if err = fulfillment.RequireFulfillmentManage(ctx, record, a); err != nil {
		return empty, err
	}
	if record.RowVersion != input.ExpectedRowVersion {
		return empty, fault.Conflict("合同已被修改，请刷新后重试")
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != input.ContractRevisionID {
		return empty, fault.Conflict("资产移交必须对应当前冻结修订")
	}
	_, signed, err := c.LoadSigningRegistration(ctx, tenant, id, input.ContractRevisionID)
	if err != nil {
		return empty, err
	}
	if !signed {
		return empty, fault.Conflict("资产移交必须对应已登记签署的冻结修订")
	}
	revision, err := c.LoadFulfillmentRevision(ctx, tenant, id, input.ContractRevisionID)
	if err != nil {
		return empty, err
	}
	draft, err := contractapp.DecodeFulfillmentSnapshot(revision)
	if err != nil {
		return empty, err
	}
	if draft.Contract == nil || draft.Contract.IsFixedAsset == nil {
		return empty, fault.Conflict("固定资产属性未知，不能登记资产移交")
	}
	if !*draft.Contract.IsFixedAsset {
		return empty, fault.Conflict("该合同明确不适用固定资产移交")
	}
	plan, found, err := r.LoadPlan(ctx, tenant, id, input.ContractRevisionID, input.AssetPlanID)
	if err != nil {
		return empty, err
	}
	if !found {
		return empty, fault.Conflict("冻结资产计划不存在、重复或无法核验")
	}
	count := 0
	var draftPlan contract.DraftAssetPlan
	for _, candidate := range draft.Assets {
		planID, e := contract.NormalizeContractUUID(candidate.AssetPlanID)
		if e == nil && planID == input.AssetPlanID {
			draftPlan = candidate
			count++
		}
	}
	itemID, e := contract.NormalizeContractUUID(draftPlan.ItemID)
	if count != 1 || e != nil || itemID != plan.ItemID || draftPlan.Quantity != plan.Quantity || draftPlan.DepartmentCode != plan.DepartmentCode || draftPlan.DepartmentName != plan.DepartmentName || draftPlan.CustodianUser != plan.CustodianUser {
		return empty, fault.Conflict("冻结资产计划快照与安全投影不一致")
	}
	item, found, err := c.LoadFulfillmentRevisionItem(ctx, tenant, id, input.ContractRevisionID, plan.ItemID)
	if err != nil {
		return empty, err
	}
	if !found {
		return empty, fault.Conflict("资产计划对应的冻结商品或单位未知")
	}
	if item.Unit == nil || *item.Unit == "" || *item.Unit != plan.Unit || item.PlannedQuantity == nil || *item.PlannedQuantity == "" {
		return empty, fault.Conflict("冻结商品单位或数量不能核验")
	}
	planQty, err := fulfillment.ParseDecimal("asset plan quantity", plan.Quantity, false)
	if err != nil {
		return empty, fault.Conflict("冻结资产计划数量不能核验")
	}
	proposal := new(big.Rat).Sub(p.End, p.Start)
	if proposal.Sign() <= 0 || proposal.Cmp(planQty) > 0 {
		return empty, fault.Invalid("handover range exceeds frozen asset plan quantity")
	}
	fact, found, err := acceptance.LoadAcceptance(ctx, tenant, id, input.AcceptanceID)
	if err != nil {
		return empty, err
	}
	if !found || fact.AcceptanceID != input.AcceptanceID || fact.ContractRevisionID != input.ContractRevisionID || fact.DeliveryID != input.DeliveryID || fact.ItemID != item.ItemID {
		return empty, fault.Conflict("验收事实不存在或不匹配")
	}
	if *item.Unit != plan.Unit || fact.Result == "failed" || fact.AcceptedQuantity == "0.000000" {
		return empty, fault.Conflict("指定验收范围没有合格数量")
	}
	qualStart, _ := fulfillment.ParseDecimal("acceptance scope start", fact.ScopeStart, true)
	qualified, _ := fulfillment.ParseDecimal("accepted quantity", fact.AcceptedQuantity, true)
	qualEnd := new(big.Rat).Add(qualStart, qualified)
	if p.Start.Cmp(qualStart) < 0 || p.End.Cmp(qualEnd) > 0 {
		return empty, fault.Conflict("移交范围必须完全位于该验收事实的已通过前缀")
	}
	overlap, err := r.HasOverlap(ctx, tenant, id, input.ContractRevisionID, item.ItemID, input.DeliveryID, input.ScopeStart, input.ScopeEnd)
	if err != nil {
		return empty, err
	}
	if overlap {
		return empty, fault.Conflict("资产移交范围已被其他资产计划分配")
	}
	allocated, err := r.AllocatedQuantity(ctx, tenant, id, input.ContractRevisionID, input.AssetPlanID)
	if err != nil {
		return empty, err
	}
	allocatedRat, _ := fulfillment.ParseDecimal("allocated quantity", allocated, true)
	if allocatedRat == nil {
		allocatedRat = new(big.Rat)
	}
	if new(big.Rat).Add(allocatedRat, proposal).Cmp(planQty) > 0 {
		return empty, fault.Conflict("移交累计数量超过冻结资产计划数量")
	}
	evidence, err := fulfillment.ResolveEvidence(ctx, input.Evidence, "asset_handover", a)
	if err != nil {
		return empty, err
	}
	var custodian accounts.User
	custodianStatus := "unknown"
	var checkedAt *time.Time
	directoryRevision, directorySource := "", ""
	if plan.CustodianUser != "" {
		snapshot := directory.LookupUserSnapshot(plan.CustodianUser)
		if snapshot.Found && snapshot.Status.State != "unavailable" && snapshot.User.Enabled && accounts.Tenant(snapshot.User) == tenant {
			custodian = snapshot.User
			custodianStatus = "verified"
			checked := snapshot.ResolvedAt.UTC()
			checkedAt = &checked
			directoryRevision, directorySource = snapshot.Status.Revision, snapshot.Status.Mode+":user_directory"
		}
	}
	handoverID, err := contract.NewContractUUID()
	if err != nil {
		return empty, err
	}
	advanced, err := c.AdvanceFulfillmentAggregate(ctx, record, input.ExpectedRowVersion)
	if err != nil {
		return empty, err
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return empty, err
	}
	assignmentStatus, departmentStatus := "unknown", "unknown"
	if departmentStatus == "verified" && custodianStatus == "verified" {
		assignmentStatus = "verified"
	}
	output := ContractAssetHandover{HandoverID: handoverID, ContractID: id, ContractRevisionID: input.ContractRevisionID, AssetPlanID: input.AssetPlanID,
		DeliveryID: input.DeliveryID, ItemID: item.ItemID, AcceptanceID: input.AcceptanceID, ScopeStart: input.ScopeStart, ScopeEnd: input.ScopeEnd, Quantity: fulfillment.RationalString(proposal), Unit: plan.Unit,
		HandedOverAt: p.HandedOverAt, Location: input.Location, TransferNote: input.TransferNote, Evidence: evidence, DepartmentCodeClaim: plan.DepartmentCode, DepartmentNameClaim: plan.DepartmentName,
		DepartmentVerification: departmentStatus, CustodianUserID: custodian.ID, CustodianDisplayName: custodian.DisplayName, CustodianOrgID: custodian.OrgID,
		CustodianDirectorySource: directorySource, CustodianDirectoryRevision: directoryRevision, CustodianDirectoryChecked: checkedAt, CustodianVerification: custodianStatus, AssignmentVerification: assignmentStatus,
		BookingStatus: "pending_booking", RecordedBy: actor, ContractRowVersion: advanced.RowVersion, Receipts: []ContractAssetReceipt{}}
	output.RecordedAt, err = r.InsertHandover(ctx, tenant, accounts.Tenant(custodian), output, actor, evidenceJSON)
	if err != nil {
		return empty, err
	}
	eventID, err := contract.NewContractUUID()
	if err != nil {
		return empty, err
	}
	body, err := json.Marshal(map[string]any{"handover_id": handoverID, "asset_plan_id": input.AssetPlanID, "acceptance_id": input.AcceptanceID, "scope_start": input.ScopeStart, "scope_end": input.ScopeEnd, "assignment_verification": assignmentStatus})
	if err != nil {
		return empty, err
	}
	if err = events.AppendContractEvent(ctx, tenant, eventID, id, advanced.RowVersion, HandoverEvent, body); err != nil {
		return empty, err
	}
	if err = outbox.EnqueueContractEvent(ctx, tenant, eventID); err != nil {
		return empty, err
	}
	response, err := json.Marshal(output)
	if err != nil {
		return empty, err
	}
	if err = c.InsertWriteReceipt(ctx, tenant, actor, HandoverRoute, input.IdempotencyKey, p.Digest, id, 201, response); err != nil {
		return empty, err
	}
	return output, nil
}
