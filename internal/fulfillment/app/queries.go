// File metadata (manifest mirror):
// Purpose: Projects authorized fulfillment histories and per-item acceptance facts, preserving legacy empty-aggregate string values and frozen quantity/unit sources.
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
	"math/big"

	contractapp "contractaudit/internal/contract/app"
	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

func ListContractDeliveries(ctx context.Context, record clmcontract.Aggregate, contracts contractapp.FulfillmentContractCommands,
	repository FulfillmentRepository, access FulfillmentAccess) (ContractDeliveryList, error) {
	deliveries, err := repository.ListDeliveries(ctx, record.TenantID, record.ContractID)
	if err != nil {
		return ContractDeliveryList{}, err
	}
	acceptances, err := repository.ListAcceptances(ctx, record.TenantID, record.ContractID)
	if err != nil {
		return ContractDeliveryList{}, err
	}
	names := map[string]map[string]revisionItemMetadata{}
	items := make([]ContractDelivery, 0, len(deliveries))
	for _, fact := range deliveries {
		metadata, err := revisionMetadata(ctx, contracts, record.TenantID, record.ContractID, fact.ContractRevisionID, names)
		if err != nil {
			return ContractDeliveryList{}, err
		}
		visible, redacted, err := projectEvidence(ctx, fact.EvidenceSnapshot, access)
		if err != nil {
			return ContractDeliveryList{}, err
		}
		item := ContractDelivery{
			DeliveryID: fact.DeliveryID, ContractID: record.ContractID, ContractRevisionID: fact.ContractRevisionID,
			DeliveryPlanSequence: fact.DeliveryPlanSequence, ItemID: fact.ItemID,
			ItemName: metadata[fact.ItemID].Name, BatchReference: fact.BatchReference, Quantity: fact.Quantity,
			Unit: fact.Unit, ActualAt: fact.ActualAt, Location: fact.Location, Description: fact.Description,
			Evidence: visible, EvidenceRedacted: redacted, RecordedBy: fact.RecordedBy, RecordedAt: fact.RecordedAt,
		}
		items = append(items, item)
	}
	summary, err := fulfillmentSummary(ctx, record.TenantID, record.ContractID, contracts, deliveries, acceptances)
	if err != nil {
		return ContractDeliveryList{}, err
	}
	return ContractDeliveryList{Items: items, Summary: summary, RowVersion: record.RowVersion}, nil
}

func ListContractAcceptances(ctx context.Context, record clmcontract.Aggregate, contracts contractapp.FulfillmentContractCommands,
	repository FulfillmentRepository, access FulfillmentAccess) (ContractAcceptanceList, error) {
	acceptances, err := repository.ListAcceptances(ctx, record.TenantID, record.ContractID)
	if err != nil {
		return ContractAcceptanceList{}, err
	}
	deliveries, err := repository.ListDeliveries(ctx, record.TenantID, record.ContractID)
	if err != nil {
		return ContractAcceptanceList{}, err
	}
	unitByDelivery := make(map[string]string, len(deliveries))
	for _, delivery := range deliveries {
		unitByDelivery[delivery.DeliveryID] = delivery.Unit
	}
	names := map[string]map[string]revisionItemMetadata{}
	items := make([]ContractAcceptance, 0, len(acceptances))
	for _, fact := range acceptances {
		metadata, err := revisionMetadata(ctx, contracts, record.TenantID, record.ContractID, fact.ContractRevisionID, names)
		if err != nil {
			return ContractAcceptanceList{}, err
		}
		visible, redacted, err := projectEvidence(ctx, fact.EvidenceSnapshot, access)
		if err != nil {
			return ContractAcceptanceList{}, err
		}
		start, _ := new(big.Rat).SetString(fact.ScopeStart)
		end, _ := new(big.Rat).SetString(fact.ScopeEnd)
		quantity := new(big.Rat).Sub(end, start)
		accepted, _ := new(big.Rat).SetString(fact.AcceptedQuantity)
		item := ContractAcceptance{
			AcceptanceID: fact.AcceptanceID, ContractID: record.ContractID,
			ContractRevisionID: fact.ContractRevisionID, DeliveryID: fact.DeliveryID, ItemID: fact.ItemID,
			ItemName: metadata[fact.ItemID].Name, AcceptancePlanSequence: fact.AcceptancePlanSequence,
			ScopeStart: fact.ScopeStart, ScopeEnd: fact.ScopeEnd, Quantity: RationalString(quantity),
			AcceptedQuantity: fact.AcceptedQuantity, FailedQuantity: RationalString(new(big.Rat).Sub(quantity, accepted)),
			Unit: unitByDelivery[fact.DeliveryID], Result: fact.Result, ReinspectionOf: fact.ReinspectionOf,
			ActualAt: fact.ActualAt, Standard: fact.Standard, InspectorNote: fact.InspectorNote,
			Evidence: visible, EvidenceRedacted: redacted, Defects: []ContractAcceptanceDefect{},
			Rectifications: []ContractRectification{}, RecordedBy: fact.RecordedBy, RecordedAt: fact.RecordedAt,
		}
		item.Defects, err = listAcceptanceDefects(ctx, repository, record.TenantID, record.ContractID, fact.AcceptanceID)
		if err != nil {
			return ContractAcceptanceList{}, err
		}
		item.Rectifications, err = listAcceptanceRectifications(ctx, repository, access, record.TenantID, record.ContractID, fact.AcceptanceID)
		if err != nil {
			return ContractAcceptanceList{}, err
		}
		item.RectificationIDs, err = repository.ListAcceptanceRectificationIDs(ctx, record.TenantID, record.ContractID, fact.AcceptanceID)
		if err != nil {
			return ContractAcceptanceList{}, err
		}
		items = append(items, item)
	}
	summary, err := fulfillmentSummary(ctx, record.TenantID, record.ContractID, contracts, deliveries, acceptances)
	if err != nil {
		return ContractAcceptanceList{}, err
	}
	return ContractAcceptanceList{Items: items, Summary: summary, RowVersion: record.RowVersion}, nil
}

// ReadAcceptanceFacts returns the exact accepted total for a revision item.
// It includes partial acceptances and every linear reinspection, matching the
// current Payment condition semantics; an empty set is the literal "0".
func ReadAcceptanceFacts(ctx context.Context, tenantID, contractID, revisionID, itemID string,
	repository FulfillmentRepository) (AcceptanceFacts, error) {
	all, err := repository.ListAcceptances(ctx, tenantID, contractID)
	if err != nil {
		return AcceptanceFacts{}, err
	}
	result := AcceptanceFacts{
		ContractRevisionID: revisionID, ItemID: itemID, AcceptedQuantity: "0",
		Acceptances: []AcceptanceFact{},
	}
	total := new(big.Rat)
	for _, fact := range all {
		if fact.ContractRevisionID != revisionID || fact.ItemID != itemID {
			continue
		}
		accepted, ok := new(big.Rat).SetString(fact.AcceptedQuantity)
		if !ok {
			return AcceptanceFacts{}, fault.Conflict("验收数量无法读取")
		}
		total.Add(total, accepted)
		result.Acceptances = append(result.Acceptances, fact)
	}
	if len(result.Acceptances) > 0 {
		result.AcceptedQuantity = RationalString(total)
	}
	return result, nil
}

func fulfillmentSummary(ctx context.Context, tenantID, contractID string, contracts contractapp.FulfillmentContractCommands,
	deliveries []DeliveryFact, acceptances []AcceptanceFact) ([]ContractFulfillmentItemSummary, error) {
	items, err := contracts.ListFulfillmentRevisionItems(ctx, tenantID, contractID)
	if err != nil {
		return nil, err
	}
	names := map[string]map[string]revisionItemMetadata{}
	delivered := map[string]*big.Rat{}
	allocated := map[string]*big.Rat{}
	qualified := map[string]*big.Rat{}
	pending := map[string]*big.Rat{}
	children := map[string]struct{}{}
	for _, fact := range acceptances {
		if fact.ReinspectionOf != "" {
			children[fact.ReinspectionOf] = struct{}{}
		}
	}
	for _, fact := range deliveries {
		key := summaryKey(fact.ContractRevisionID, fact.ItemID)
		quantity, ok := new(big.Rat).SetString(fact.Quantity)
		if !ok {
			return nil, fault.Conflict("交付数量无法读取")
		}
		addRat(delivered, key, quantity)
	}
	for _, fact := range acceptances {
		key := summaryKey(fact.ContractRevisionID, fact.ItemID)
		start, okStart := new(big.Rat).SetString(fact.ScopeStart)
		end, okEnd := new(big.Rat).SetString(fact.ScopeEnd)
		accepted, okAccepted := new(big.Rat).SetString(fact.AcceptedQuantity)
		if !okStart || !okEnd || !okAccepted {
			return nil, fault.Conflict("验收数量范围无法读取")
		}
		span := new(big.Rat).Sub(end, start)
		addRat(qualified, key, accepted)
		if fact.ReinspectionOf == "" {
			addRat(allocated, key, span)
		}
		if fact.Result != "passed" {
			if _, hasChild := children[fact.AcceptanceID]; !hasChild {
				addRat(pending, key, new(big.Rat).Sub(span, accepted))
			}
		}
	}
	summary := make([]ContractFulfillmentItemSummary, 0, len(items))
	for _, row := range items {
		if _, ok := names[row.RevisionID]; !ok {
			metadata, err := revisionMetadata(ctx, contracts, tenantID, contractID, row.RevisionID, names)
			if err != nil {
				return nil, err
			}
			names[row.RevisionID] = metadata
		}
		key := summaryKey(row.RevisionID, row.Item.ItemID)
		deliveredQuantity := ratOrZero(delivered[key])
		allocatedQuantity := ratOrZero(allocated[key])
		unallocated := new(big.Rat).Sub(deliveredQuantity, allocatedQuantity)
		if unallocated.Sign() < 0 {
			unallocated.SetInt64(0)
		}
		summary = append(summary, ContractFulfillmentItemSummary{
			ContractRevisionID: row.RevisionID, ItemID: row.Item.ItemID,
			ItemName: names[row.RevisionID][row.Item.ItemID].Name,
			Unit:     pointerValue(row.Item.Unit), PlannedQuantity: pointerValue(row.Item.PlannedQuantity),
			DeliveredQuantity: summaryQuantity(delivered[key]), QualifiedQuantity: summaryQuantity(qualified[key]),
			UnallocatedQuantity: summaryDifference(unallocated, delivered[key], allocated[key]), PendingReinspectionQuantity: summaryQuantity(pending[key]),
		})
	}
	return summary, nil
}

func revisionMetadata(ctx context.Context, contracts contractapp.FulfillmentContractCommands,
	tenantID, contractID, revisionID string, cache map[string]map[string]revisionItemMetadata) (map[string]revisionItemMetadata, error) {
	if values, ok := cache[revisionID]; ok {
		return values, nil
	}
	revision, err := contracts.LoadFulfillmentRevision(ctx, tenantID, contractID, revisionID)
	if err != nil {
		return nil, err
	}
	values := parseRevisionItemMetadata(revision.Snapshot)
	cache[revisionID] = values
	return values, nil
}

func listAcceptanceDefects(ctx context.Context, repository FulfillmentRepository, tenantID, contractID, acceptanceID string) ([]ContractAcceptanceDefect, error) {
	facts, err := repository.ListAcceptanceDefects(ctx, tenantID, contractID, acceptanceID)
	if err != nil {
		return nil, err
	}
	items := make([]ContractAcceptanceDefect, 0, len(facts))
	for _, fact := range facts {
		items = append(items, ContractAcceptanceDefect{
			DefectID: fact.DefectID, PriorDefectID: fact.PriorDefectID, Description: fact.Description,
			Responsible: fact.Responsible, RemediationDue: fact.RemediationDue,
			FailedQuantity: fact.FailedQuantity, RecordedBy: fact.RecordedBy, RecordedAt: fact.RecordedAt,
		})
	}
	return items, nil
}

func listAcceptanceRectifications(ctx context.Context, repository FulfillmentRepository, access FulfillmentAccess,
	tenantID, contractID, acceptanceID string) ([]ContractRectification, error) {
	facts, err := repository.ListAcceptanceRectifications(ctx, tenantID, contractID, acceptanceID)
	if err != nil {
		return nil, err
	}
	items := make([]ContractRectification, 0, len(facts))
	for _, fact := range facts {
		visible, redacted, err := projectEvidence(ctx, fact.EvidenceSnapshot, access)
		if err != nil {
			return nil, err
		}
		items = append(items, ContractRectification{
			RectificationID: fact.RectificationID, ContractRevisionID: fact.ContractRevisionID,
			AcceptanceID: fact.AcceptanceID, DefectID: fact.DefectID, Description: fact.Description,
			Evidence: visible, EvidenceRedacted: redacted, RecordedBy: fact.RecordedBy, RecordedAt: fact.RecordedAt,
		})
	}
	return items, nil
}

func addRat(values map[string]*big.Rat, key string, value *big.Rat) {
	if values[key] == nil {
		values[key] = new(big.Rat)
	}
	values[key].Add(values[key], value)
}

func ratOrZero(value *big.Rat) *big.Rat {
	if value == nil {
		return new(big.Rat)
	}
	return value
}

func summaryKey(revisionID, itemID string) string { return revisionID + "\x00" + itemID }

// SQL aggregates with no matching rows used COALESCE(...,0)::text, while
// numeric facts retain their six-place scale, including a calculated zero.
func summaryQuantity(value *big.Rat) string {
	if value == nil {
		return "0"
	}
	return RationalString(value)
}

func summaryDifference(value, delivered, allocated *big.Rat) string {
	if delivered == nil && allocated == nil {
		return "0"
	}
	return RationalString(value)
}
