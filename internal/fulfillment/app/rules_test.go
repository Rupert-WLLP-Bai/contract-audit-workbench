// File metadata (manifest mirror):
// Purpose: Exercises fulfillment rule primitives: time and exact-decimal parsing, acceptance classification, frozen plan selection, item metadata lookup and evidence ordering.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

func appIntPtr(value int) *int { return &value }

func requireAppInvalid(t *testing.T, name string, err error) {
	t.Helper()
	var fe *fault.Error
	if err == nil {
		t.Fatalf("%s was accepted", name)
	}
	if !errors.As(err, &fe) || fe.Status != 422 {
		t.Fatalf("%s returned %v", name, err)
	}
}

func TestParseTimeAndExactDecimalGrammar(t *testing.T) {
	parsed, err := ParseTime("actual_at", "  2026-11-15T10:30:00+08:00  ")
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	want := time.Date(2026, 11, 15, 2, 30, 0, 0, time.UTC)
	if !parsed.Equal(want) || parsed.Location() != time.UTC {
		t.Fatalf("parsed time is %v", parsed)
	}
	for name, raw := range map[string]string{
		"empty":       "",
		"date only":   "2026-11-15",
		"no timezone": "2026-11-15T10:30:00",
		"garbage":     "tomorrow",
	} {
		if _, err := ParseTime("actual_at", raw); err == nil {
			t.Fatalf("%s time was accepted", name)
		}
	}

	value, err := ParseDecimal("quantity", "123456789012.123456", false)
	if err != nil || value.FloatString(6) != "123456789012.123456" {
		t.Fatalf("max decimal: %v %v", value, err)
	}
	zero, err := ParseDecimal("accepted_quantity", "0", true)
	if err != nil || zero.Sign() != 0 {
		t.Fatalf("accepted zero: %v %v", zero, err)
	}
	requireAppInvalid(t, "zero quantity", secondValue(ParseDecimal("quantity", "0", false)))
	for name, raw := range map[string]string{
		"empty":             "",
		"negative":          "-1.000000",
		"leading zero":      "01.000000",
		"too many decimals": "1.1234567",
		"too large":         "1000000000000.000000",
		"exponent":          "1e3",
	} {
		requireAppInvalid(t, name+" decimal", secondValue(ParseDecimal("quantity", raw, true)))
	}
	if RationalString(nil) != "0.000000" || RationalString(big.NewRat(1, 3)) != "0.333333" {
		t.Fatalf("rational formatting: %q %q", RationalString(nil), RationalString(big.NewRat(1, 3)))
	}
}

func secondValue[T any](_ T, err error) error { return err }

func TestResultForQuantityClassifiesAcceptance(t *testing.T) {
	for name, testCase := range map[string]struct {
		quantity string
		accepted string
		want     string
	}{
		"fully accepted":     {"10.000000", "10.000000", "passed"},
		"partially accepted": {"10.000000", "4.500000", "partial"},
		"nothing accepted":   {"10.000000", "0", "failed"},
	} {
		result, quantity, accepted, err := ResultForQuantity(testCase.quantity, testCase.accepted)
		if err != nil || result != testCase.want || quantity == nil || accepted == nil {
			t.Fatalf("%s: %q %v %v %v", name, result, quantity, accepted, err)
		}
	}
	for name, testCase := range map[string][2]string{
		"over-accepted":    {"10.000000", "11.000000"},
		"zero quantity":    {"0", "0"},
		"invalid accepted": {"10.000000", "1.5.0"},
		"invalid quantity": {"ten", "1"},
	} {
		if result, quantity, accepted, err := ResultForQuantity(testCase[0], testCase[1]); err == nil ||
			result != "" || quantity != nil || accepted != nil {
			t.Fatalf("%s returned %q %v %v %v", name, result, quantity, accepted, err)
		}
	}
}

func TestValidateTextAndUUIDNormalization(t *testing.T) {
	if err := ValidateText("field", "  合同  ", 2, true); err != nil {
		t.Fatalf("trimmed text: %v", err)
	}
	if err := ValidateText("field", "   ", 10, false); err != nil {
		t.Fatalf("optional blank text: %v", err)
	}
	requireAppInvalid(t, "required blank text", ValidateText("field", " ", 10, true))
	requireAppInvalid(t, "text over the limit", ValidateText("field", strings.Repeat("汉", 3), 2, false))

	uuid := strings.ToUpper("11111111-1111-4111-8111-111111111111")
	if err := normalizeUUIDField("item_id", &uuid); err != nil || uuid != strings.ToLower(uuid) {
		t.Fatalf("normalized UUID %q (%v)", uuid, err)
	}
	invalid := "item-1"
	requireAppInvalid(t, "invalid UUID", normalizeUUIDField("item_id", &invalid))
	if invalid != "item-1" {
		t.Fatalf("failed normalization rewrote the value: %q", invalid)
	}
	if pointerValue(nil) != "" || pointerValue(appStringPtr("")) != "" || pointerValue(appStringPtr("x")) != "x" {
		t.Fatal("pointer value projection is wrong")
	}
}

func appStringPtr(value string) *string { return &value }

func TestRevisionItemMetadataAndEvidenceOrdering(t *testing.T) {
	snapshot, err := json.Marshal(map[string]any{"items": []any{
		map[string]any{"item_id": "11111111-1111-4111-8111-111111111111", "name": "服务器", "quantity": "1.000000", "unit": "台"},
		map[string]any{"item_id": "11111111-1111-4111-8111-111111111111", "name": "重复", "quantity": "2.000000", "unit": "台"},
		map[string]any{"item_id": "not-a-uuid", "name": "忽略"},
		map[string]any{"item_id": "22222222-2222-4222-8222-222222222222", "name": 5, "quantity": 1},
		map[string]any{"item_id": "33333333-3333-4333-8333-333333333333"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := parseRevisionItemMetadata(snapshot)
	if len(metadata) != 2 {
		t.Fatalf("metadata kept %d items: %+v", len(metadata), metadata)
	}
	if _, duplicated := metadata["11111111-1111-4111-8111-111111111111"]; duplicated {
		t.Fatal("duplicate item identity entered the metadata map")
	}
	unique := metadata["22222222-2222-4222-8222-222222222222"]
	if unique.Name != "" || unique.Quantity != "" || unique.Unit != "" {
		t.Fatalf("non-string fields were materialized: %+v", unique)
	}
	item, err := frozenItemMetadata(snapshot, "33333333-3333-4333-8333-333333333333")
	if err != nil || item.ItemID != "33333333-3333-4333-8333-333333333333" {
		t.Fatalf("frozen item metadata: %+v %v", item, err)
	}
	requireAppInvalid(t, "missing item metadata", secondValue(frozenItemMetadata(snapshot, "44444444-4444-4444-8444-444444444444")))
	requireAppInvalid(t, "malformed snapshot metadata", secondValue(frozenItemMetadata([]byte("not json"), "x")))
	if metadata := parseRevisionItemMetadata([]byte("not json")); len(metadata) != 0 {
		t.Fatalf("malformed snapshot produced %+v", metadata)
	}
	if metadata := parseRevisionItemMetadata([]byte(`{"items":{}}`)); len(metadata) != 0 {
		t.Fatalf("non-array items produced %+v", metadata)
	}
	if metadata := parseRevisionItemMetadata([]byte(`{"items":[1]}`)); len(metadata) != 0 {
		t.Fatalf("non-object item produced %+v", metadata)
	}

	evidence := []clmcontract.ContractVersionMaterial{
		{MaterialID: "b", MaterialVersionID: "2"},
		{MaterialID: "a", MaterialVersionID: "2"},
		{MaterialID: "b", MaterialVersionID: "1"},
	}
	ordered := sortedEvidence(evidence)
	if ordered[0].MaterialID != "a" || ordered[1].MaterialID != "b" || ordered[1].MaterialVersionID != "1" ||
		ordered[2].MaterialID != "b" || ordered[2].MaterialVersionID != "2" {
		t.Fatalf("evidence order is %+v", ordered)
	}
	if evidence[0].MaterialID != "b" || evidence[1].MaterialID != "a" {
		t.Fatal("evidence ordering mutated its input")
	}
	if len(sortedEvidence(nil)) != 0 {
		t.Fatal("nil evidence produced rows")
	}
}

func TestFrozenPlanSelectionRespectsSequenceAndItem(t *testing.T) {
	itemA := "11111111-1111-4111-8111-111111111111"
	itemB := "22222222-2222-4222-8222-222222222222"
	draft := clmcontract.DraftContent{
		DeliveryPlans: []clmcontract.DraftDeliveryPlan{
			{Sequence: 1, ItemID: itemA},
			{Sequence: 2, ItemID: itemB},
			{Sequence: 3},
		},
		AcceptancePlans: []clmcontract.DraftAcceptancePlan{
			{Sequence: 1, ItemIDs: []string{itemA}},
			{Sequence: 2, ItemIDs: []string{itemB}},
			{Sequence: 3},
		},
	}
	if plan, err := deliveryPlanForItem(clmcontract.DraftContent{}, nil, itemA); err != nil || plan != nil {
		t.Fatalf("empty delivery plans: %+v %v", plan, err)
	}
	requireAppInvalid(t, "delivery sequence on an empty revision", secondValue(deliveryPlanForItem(clmcontract.DraftContent{}, appIntPtr(1), itemA)))
	if plan, err := deliveryPlanForItem(draft, appIntPtr(1), itemA); err != nil || plan == nil || plan.Sequence != 1 {
		t.Fatalf("delivery plan: %+v %v", plan, err)
	}
	requireAppInvalid(t, "delivery sequence without selection", secondValue(deliveryPlanForItem(draft, nil, itemA)))
	requireAppInvalid(t, "delivery item mismatch", secondValue(deliveryPlanForItem(draft, appIntPtr(1), itemB)))
	requireAppInvalid(t, "unknown delivery sequence", secondValue(deliveryPlanForItem(draft, appIntPtr(9), itemA)))
	if plan, err := deliveryPlanForItem(draft, appIntPtr(3), ""); err != nil || plan == nil || plan.Sequence != 3 {
		t.Fatalf("unscoped delivery plan: %+v %v", plan, err)
	}

	if plan, err := acceptancePlanForItem(clmcontract.DraftContent{}, nil, itemA); err != nil || plan != nil {
		t.Fatalf("empty acceptance plans: %+v %v", plan, err)
	}
	requireAppInvalid(t, "acceptance sequence on an empty revision", secondValue(acceptancePlanForItem(clmcontract.DraftContent{}, appIntPtr(1), itemA)))
	if plan, err := acceptancePlanForItem(draft, appIntPtr(1), itemA); err != nil || plan == nil || plan.Sequence != 1 {
		t.Fatalf("acceptance plan: %+v %v", plan, err)
	}
	requireAppInvalid(t, "acceptance sequence without selection", secondValue(acceptancePlanForItem(draft, nil, itemA)))
	requireAppInvalid(t, "acceptance item not covered", secondValue(acceptancePlanForItem(draft, appIntPtr(1), itemB)))
	requireAppInvalid(t, "unknown acceptance sequence", secondValue(acceptancePlanForItem(draft, appIntPtr(9), itemA)))
	if plan, err := acceptancePlanForItem(draft, appIntPtr(3), itemB); err != nil || plan == nil || plan.Sequence != 3 {
		t.Fatalf("unscoped acceptance plan: %+v %v", plan, err)
	}
}
