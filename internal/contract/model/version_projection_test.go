// File metadata (manifest mirror):
// Purpose: Exercises frozen material and revision-plan projections plus aggregate summary and draft command derivation, keeping invalid identities out of immutable indexes.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package contract

import (
	"strings"
	"testing"
	"time"
)

func TestFrozenMaterialsFromReferencesRejectsDuplicateMaterialsAndSorts(t *testing.T) {
	refs := []ResolvedVersionMaterial{
		{MaterialID: "material-b", MaterialVersionID: "version-2", MaterialVersion: 2, Filename: "b.pdf", Digest: "d2", Purpose: "draft"},
		{MaterialID: "material-a", MaterialVersionID: "version-3", MaterialVersion: 3, Filename: "a.pdf", Digest: "d3", Purpose: "signed_contract"},
	}
	materials, err := FrozenMaterialsFromReferences("org-a", "case-a", refs)
	if err != nil {
		t.Fatalf("frozen materials: %v", err)
	}
	if len(materials) != 2 || materials[0].MaterialID != "material-a" || materials[1].MaterialID != "material-b" ||
		materials[0].CaseOrgID != "org-a" || materials[0].CaseID != "case-a" ||
		materials[0].MaterialVersion != 3 || materials[0].Filename != "a.pdf" || materials[0].Digest != "d3" ||
		materials[0].Purpose != "signed_contract" {
		t.Fatalf("frozen materials are %+v", materials)
	}
	empty, err := FrozenMaterialsFromReferences("org-a", "case-a", nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty material projection: %+v %v", empty, err)
	}
	duplicates := []ResolvedVersionMaterial{
		{MaterialID: "material-a", MaterialVersionID: "version-1"},
		{MaterialID: "material-a", MaterialVersionID: "version-2"},
	}
	_, err = FrozenMaterialsFromReferences("org-a", "case-a", duplicates)
	requireDraftInvalid(t, "duplicate material versions", err)
}

func TestFrozenRevisionPlanItemsSkipInvalidAndRepeatedIdentities(t *testing.T) {
	uppercase := strings.ToUpper("55555555-5555-4555-8555-555555555555")
	items := []DraftItem{
		{ItemID: draftItemUUID, Quantity: " 1.500000 ", Unit: " 台 "},
		{ItemID: uppercase, Quantity: "0.000000", Unit: "   "},
		{ItemID: "item-1", Quantity: "2.000000", Unit: "台"},
		{ItemID: "33333333-3333-4333-8333-333333333333", Quantity: "not-a-quantity", Unit: strings.Repeat("a", 41)},
		{ItemID: "44444444-4444-4444-8444-444444444444", Quantity: "1.000000"},
		{ItemID: "44444444-4444-4444-8444-444444444444", Quantity: "2.000000"},
	}
	plan := FrozenRevisionPlanItems(items)
	if len(plan) != 3 {
		t.Fatalf("plan index has %d rows: %+v", len(plan), plan)
	}
	if plan[0].ItemID != draftItemUUID || plan[0].PlannedQuantity == nil || *plan[0].PlannedQuantity != " 1.500000 " ||
		plan[0].Unit == nil || *plan[0].Unit != "台" {
		t.Fatalf("first plan row is %+v", plan[0])
	}
	if plan[1].ItemID != strings.ToLower(uppercase) || plan[1].PlannedQuantity != nil || plan[1].Unit != nil {
		t.Fatalf("zero quantity row is %+v", plan[1])
	}
	if plan[2].ItemID != "33333333-3333-4333-8333-333333333333" || plan[2].PlannedQuantity != nil || plan[2].Unit != nil {
		t.Fatalf("invalid quantity row is %+v", plan[2])
	}
	if plan := FrozenRevisionPlanItems(nil); plan == nil || len(plan) != 0 {
		t.Fatalf("empty plan index is %+v", plan)
	}
}

func TestLegacyAmountPairRequiresBothValues(t *testing.T) {
	amount, currency := LegacyAmountPair(DraftContent{})
	if amount != nil || currency != nil {
		t.Fatalf("empty snapshot produced %v %v", amount, currency)
	}
	amount, currency = LegacyAmountPair(DraftContent{Contract: &ContractBasics{Amount: "10.00"}})
	if amount != nil || currency != nil {
		t.Fatalf("partial pair produced %v %v", amount, currency)
	}
	amount, currency = LegacyAmountPair(DraftContent{Contract: &ContractBasics{Amount: "10.00", Currency: "CNY"}})
	if amount == nil || *amount != "10.00" || currency == nil || *currency != "CNY" {
		t.Fatalf("known pair produced %v %v", amount, currency)
	}
}

func TestSummaryAndDraftCommandsFollowAuthorityAndLifecycle(t *testing.T) {
	number := "HT-001"
	amount := "10.00"
	currency := "CNY"
	created := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	native := Aggregate{
		ContractID: "contract-1", AuthorityMode: "native", ContractNumber: &number,
		LifecycleState: "draft", ValidityState: "active", RowVersion: 4, CreatedAt: created,
	}
	summary := SummaryFromDraft(native, "合同", &amount, &currency)
	if summary.ContractID != "contract-1" || summary.Name != "合同" || summary.Number == nil || *summary.Number != "HT-001" ||
		summary.ContractState != "draft" || summary.WorkflowState != "not_started" || summary.RowVersion != 4 ||
		!summary.CreatedAt.Equal(created) {
		t.Fatalf("native summary is %+v", summary)
	}
	external := native
	external.AuthorityMode = "external"
	externalSummary := SummaryFromDraft(external, "外部合同", nil, nil)
	if externalSummary.ContractState != "external_observed" || externalSummary.WorkflowState != "external_observed" {
		t.Fatalf("external summary is %+v", externalSummary)
	}

	if commands := DraftCommands(native, false); len(commands) != 0 {
		t.Fatalf("unmanaged draft exposed %v", commands)
	}
	commands := DraftCommands(native, true)
	if len(commands) != 2 || commands[0] != "replace_draft" || commands[1] != "freeze_version" {
		t.Fatalf("draft commands are %v", commands)
	}
	revision := "revision-1"
	native.CurrentRevisionID = &revision
	commands = DraftCommands(native, true)
	if len(commands) != 3 || commands[2] != "submit_for_approval" {
		t.Fatalf("draft with revision commands are %v", commands)
	}
	for name, record := range map[string]Aggregate{
		"external authority": func() Aggregate { value := native; value.AuthorityMode = "external"; return value }(),
		"frozen lifecycle":   func() Aggregate { value := native; value.LifecycleState = "frozen"; return value }(),
		"inactive validity":  func() Aggregate { value := native; value.ValidityState = "inactive"; return value }(),
	} {
		if commands := DraftCommands(record, true); len(commands) != 0 {
			t.Fatalf("%s exposed %v", name, commands)
		}
	}
}
