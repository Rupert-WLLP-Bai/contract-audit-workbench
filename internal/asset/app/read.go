// File metadata (manifest mirror):
// Purpose: Combines immutable raw Contract revisions with Asset projections and authorized handover/receipt histories while retaining unknown legacy states and the caller snapshot.
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
	contract "contractaudit/internal/contract/model"
	fulfillment "contractaudit/internal/fulfillment/app"
	"sort"
)

func ListAssets(ctx context.Context, record contract.Aggregate, revisionsReader RevisionReader, repository ListRepository, access Access) (ContractAssetList, error) {
	out := ContractAssetList{ContractID: record.ContractID, RowVersion: record.RowVersion, FixedAssetStatus: "unknown", Items: []ContractAssetPlanSummary{}}
	revisions, err := revisionsReader.ListRevisionSnapshots(ctx, record.TenantID, record.ContractID)
	if err != nil {
		return out, err
	}
	for _, revision := range revisions {
		fixed, rawAssets, assetsAreArray := ParseAssetSnapshot(revision.Snapshot)
		if record.CurrentRevisionID != nil && *record.CurrentRevisionID == revision.RevisionID {
			out.FixedAssetStatus = fixed
		}
		projected, e := repository.LoadProjectedPlans(ctx, record.TenantID, record.ContractID, revision.RevisionID)
		if e != nil {
			return out, e
		}
		seenPlans := make(map[string]bool, len(projected))
		rowCounts := make(map[string]int, len(rawAssets))
		rows := make([]AssetSnapshotRow, 0, len(rawAssets))
		if assetsAreArray {
			for _, raw := range rawAssets {
				row := ParseAssetSnapshotRow(raw)
				rows = append(rows, row)
				if id, idErr := contract.NormalizeContractUUID(row.AssetPlanID); idErr == nil {
					rowCounts[id]++
				}
			}
		}
		for _, row := range rows {
			planID, idErr := contract.NormalizeContractUUID(row.AssetPlanID)
			item := ContractAssetPlanSummary{ContractRevisionID: revision.RevisionID, AssetPlanID: row.AssetPlanID, ItemID: row.ItemID, Name: row.Name, PlannedQuantity: row.Quantity, DepartmentCode: row.DepartmentCode, DepartmentName: row.DepartmentName, DepartmentStatus: "unknown", CustodianUserID: row.CustodianUser, FixedAssetStatus: fixed, PlanStatus: "unknown", Handovers: []ContractAssetHandover{}}
			if idErr == nil {
				seenPlans[planID] = true
				if plan, ok := projected[planID]; ok {
					normalizedItemID, normalizeErr := contract.NormalizeContractUUID(row.ItemID)
					if row.StructurallyValid && rowCounts[planID] == 1 && normalizeErr == nil && plan.ItemID == normalizedItemID && plan.Quantity == row.Quantity && plan.DepartmentCode == row.DepartmentCode && plan.DepartmentName == row.DepartmentName && plan.CustodianUser == row.CustodianUser {
						item.PlanStatus = "known"
						item.Unit = plan.Unit
					}
				}
				hs, loadErr := listHandovers(ctx, repository, access, record.TenantID, record.ContractID, revision.RevisionID, planID)
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
			item := ContractAssetPlanSummary{ContractRevisionID: revision.RevisionID, AssetPlanID: id, ItemID: plan.ItemID, PlannedQuantity: plan.Quantity, Unit: plan.Unit, DepartmentCode: plan.DepartmentCode, DepartmentName: plan.DepartmentName, DepartmentStatus: "unknown", CustodianUserID: plan.CustodianUser, FixedAssetStatus: fixed, PlanStatus: "unknown", Handovers: []ContractAssetHandover{}}
			hs, loadErr := listHandovers(ctx, repository, access, record.TenantID, record.ContractID, revision.RevisionID, id)
			if loadErr != nil {
				return out, loadErr
			}
			item.Handovers = hs
			out.Items = append(out.Items, item)
		}
	}
	return out, nil
}

func listHandovers(ctx context.Context, repository ListRepository, access Access, tenant, contractID, revision, plan string) ([]ContractAssetHandover, error) {
	loaded, err := repository.ListHandovers(ctx, tenant, contractID, revision, plan)
	if err != nil {
		return nil, err
	}
	out := make([]ContractAssetHandover, 0, len(loaded))
	for _, row := range loaded {
		h := row.Handover
		h.Evidence, h.EvidenceRedacted, err = fulfillment.ProjectEvidenceSnapshot(ctx, row.Evidence, access)
		if err != nil {
			return nil, err
		}
		h.ContractID, h.ContractRevisionID, h.AssetPlanID = contractID, revision, plan
		h.AssignmentVerification = "unknown"
		if h.DepartmentVerification == "verified" && h.CustodianVerification == "verified" {
			h.AssignmentVerification = "verified"
		}
		h.Receipts, err = repository.ListReceipts(ctx, tenant, contractID, h.HandoverID)
		if err != nil {
			return nil, err
		}
		h.BookingStatus = BookingStatus(h.Quantity, h.Receipts)
		out = append(out, h)
	}
	return out, nil
}
