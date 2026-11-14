// File metadata (manifest mirror):
// Purpose: Parses legacy asset snapshot fields tolerantly, preserving malformed, missing and duplicated plan evidence as unknown rather than rewriting frozen data.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	clmcontract "contractaudit/internal/contract/model"
	"encoding/json"
	"strings"
)

type AssetSnapshotRow struct {
	AssetPlanID       string
	ItemID            string
	Name              string
	Quantity          string
	DepartmentCode    string
	DepartmentName    string
	CustodianUser     string
	StructurallyValid bool
}

func ParseAssetSnapshot(snapshot []byte) (string, []json.RawMessage, bool) {
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

func ParseAssetSnapshotRow(raw json.RawMessage) AssetSnapshotRow {
	var asset clmcontract.DraftAssetPlan
	valid := json.Unmarshal(raw, &asset) == nil
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return AssetSnapshotRow{StructurallyValid: false}
	}
	read := func(name string) string {
		var value string
		if json.Unmarshal(fields[name], &value) != nil {
			return ""
		}
		return value
	}
	return AssetSnapshotRow{AssetPlanID: read("asset_plan_id"), ItemID: read("item_id"), Name: read("name"), Quantity: read("quantity"), DepartmentCode: read("department_code"), DepartmentName: read("department_name"), CustodianUser: read("custodian_user_id"), StructurallyValid: valid}
}
