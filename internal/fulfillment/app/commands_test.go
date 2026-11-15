// File metadata (manifest mirror):
// Purpose: Exercises fulfillment command preparation: identity normalization, row-version and decimal grammar, bounded text, evidence purpose resolution and idempotent request digests.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

const (
	commandContractUUID = "11111111-1111-4111-8111-111111111111"
	commandRevisionUUID = "22222222-2222-4222-8222-222222222222"
	commandDeliveryUUID = "33333333-3333-4333-8333-333333333333"
	commandAcceptanceID = "44444444-4444-4444-8444-444444444444"
	commandDefectUUID   = "55555555-5555-4555-8555-555555555555"
)

func validDeliveryCommand() ContractDeliveryInput {
	sequence := 1
	return ContractDeliveryInput{
		ContractID: strings.ToUpper(commandContractUUID), ExpectedRowVersion: 2,
		ContractRevisionID: commandRevisionUUID, DeliveryPlanSequence: &sequence,
		ItemID: commandContractUUID, BatchReference: "  BATCH-1  ", Quantity: "1.000000",
		ActualAt: " 2026-11-15T10:30:00+08:00 ", Location: " 仓库 ", Description: " 备注 ",
		Evidence: []clmcontract.MaterialRef{{
			MaterialID: "material-1", MaterialVersionID: "version-1",
			SHA256: strings.Repeat("a", 64), Purpose: "supporting",
		}},
		IdempotencyKey: "delivery-key-1",
	}
}

func validAcceptanceCommand() ContractAcceptanceInput {
	return ContractAcceptanceInput{
		ContractID: strings.ToUpper(commandContractUUID), ExpectedRowVersion: 3,
		ContractRevisionID: commandRevisionUUID, DeliveryID: commandDeliveryUUID,
		ScopeStart: "0", ScopeEnd: "1.000000", AcceptedQuantity: "0.500000",
		ActualAt: "2026-11-15T11:00:00Z", Standard: " 符合合同约定 ", InspectorNote: " 正常 ",
		Evidence:       []clmcontract.MaterialRef{{MaterialID: "material-1", MaterialVersionID: "version-1", SHA256: strings.Repeat("a", 64), Purpose: "acceptance"}},
		IdempotencyKey: "acceptance-key-1",
	}
}

func validRectificationCommand() ContractRectificationInput {
	return ContractRectificationInput{
		ContractID: commandContractUUID, ExpectedRowVersion: 4,
		ContractRevisionID: commandRevisionUUID, AcceptanceID: commandAcceptanceID, DefectID: commandDefectUUID,
		Description: " 更换部件 ", IdempotencyKey: "rectification-key-1",
	}
}

func requireCommandFault(t *testing.T, name string, err error, status int) {
	t.Helper()
	var fe *fault.Error
	if err == nil {
		t.Fatalf("%s was accepted", name)
	}
	if !errors.As(err, &fe) || fe.Status != status {
		t.Fatalf("%s returned %v", name, err)
	}
}

func TestPrepareDeliveryAcceptsCanonicalInput(t *testing.T) {
	input := validDeliveryCommand()
	prepared, err := PrepareDelivery(input)
	if err != nil {
		t.Fatalf("prepare delivery: %v", err)
	}
	if prepared.Input.ContractID != commandContractUUID || prepared.Input.BatchReference != "BATCH-1" ||
		prepared.Input.IdempotencyKey != "delivery-key-1" || len(prepared.RequestDigest) != 64 {
		t.Fatalf("prepared delivery is %+v", prepared)
	}
	want := time.Date(2026, 11, 15, 2, 30, 0, 0, time.UTC)
	if !prepared.ActualAt.Equal(want) {
		t.Fatalf("actual_at is %v", prepared.ActualAt)
	}
	replayed, err := PrepareDelivery(input)
	if err != nil || replayed.RequestDigest != prepared.RequestDigest {
		t.Fatalf("delivery digest is not canonical: %q %q (%v)", replayed.RequestDigest, prepared.RequestDigest, err)
	}
}

func TestPrepareDeliveryRejectsInvalidFields(t *testing.T) {
	cases := map[string]struct {
		mutate func(*ContractDeliveryInput)
		status int
	}{
		"invalid contract":      {func(in *ContractDeliveryInput) { in.ContractID = "contract-1" }, 404},
		"zero row version":      {func(in *ContractDeliveryInput) { in.ExpectedRowVersion = 0 }, 422},
		"invalid revision":      {func(in *ContractDeliveryInput) { in.ContractRevisionID = "revision-1" }, 422},
		"invalid item":          {func(in *ContractDeliveryInput) { in.ItemID = "item-1" }, 422},
		"empty batch":           {func(in *ContractDeliveryInput) { in.BatchReference = "  " }, 422},
		"batch too long":        {func(in *ContractDeliveryInput) { in.BatchReference = strings.Repeat("a", 201) }, 422},
		"invalid quantity":      {func(in *ContractDeliveryInput) { in.Quantity = "0" }, 422},
		"empty location":        {func(in *ContractDeliveryInput) { in.Location = " " }, 422},
		"location too long":     {func(in *ContractDeliveryInput) { in.Location = strings.Repeat("a", 1001) }, 422},
		"description too long":  {func(in *ContractDeliveryInput) { in.Description = strings.Repeat("a", 4001) }, 422},
		"actual time invalid":   {func(in *ContractDeliveryInput) { in.ActualAt = "2026-11-15" }, 422},
		"idempotency too short": {func(in *ContractDeliveryInput) { in.IdempotencyKey = "short" }, 422},
		"idempotency has space": {func(in *ContractDeliveryInput) { in.IdempotencyKey = "delivery key" }, 422},
	}
	for name, testCase := range cases {
		input := validDeliveryCommand()
		testCase.mutate(&input)
		if _, err := PrepareDelivery(input); err == nil {
			t.Fatalf("%s was accepted", name)
		} else {
			requireCommandFault(t, name, err, testCase.status)
		}
	}
}

func TestPrepareAcceptanceAcceptsCanonicalInput(t *testing.T) {
	input := validAcceptanceCommand()
	prepared, err := PrepareAcceptance(input)
	if err != nil {
		t.Fatalf("prepare acceptance: %v", err)
	}
	if prepared.Input.ContractID != commandContractUUID || prepared.Input.DeliveryID != commandDeliveryUUID ||
		len(prepared.RequestDigest) != 64 || prepared.ActualAt.Location() != time.UTC {
		t.Fatalf("prepared acceptance is %+v", prepared)
	}
	reinspection := validAcceptanceCommand()
	reinspection.ReinspectionOf = strings.ToUpper(commandDeliveryUUID)
	prepared, err = PrepareAcceptance(reinspection)
	if err != nil || prepared.Input.ReinspectionOf != commandDeliveryUUID {
		t.Fatalf("reinspection identity is %q (%v)", prepared.Input.ReinspectionOf, err)
	}
}

func TestPrepareAcceptanceRejectsInvalidFields(t *testing.T) {
	cases := map[string]struct {
		mutate func(*ContractAcceptanceInput)
		status int
	}{
		"invalid contract":     {func(in *ContractAcceptanceInput) { in.ContractID = "contract-1" }, 404},
		"zero row version":     {func(in *ContractAcceptanceInput) { in.ExpectedRowVersion = 0 }, 422},
		"invalid revision":     {func(in *ContractAcceptanceInput) { in.ContractRevisionID = "revision-1" }, 422},
		"invalid delivery":     {func(in *ContractAcceptanceInput) { in.DeliveryID = "delivery-1" }, 422},
		"invalid reinspection": {func(in *ContractAcceptanceInput) { in.ReinspectionOf = "parent-1" }, 422},
		"empty standard":       {func(in *ContractAcceptanceInput) { in.Standard = " " }, 422},
		"standard too long":    {func(in *ContractAcceptanceInput) { in.Standard = strings.Repeat("a", 4001) }, 422},
		"note too long":        {func(in *ContractAcceptanceInput) { in.InspectorNote = strings.Repeat("a", 4001) }, 422},
		"too many defects":     {func(in *ContractAcceptanceInput) { in.Defects = make([]ContractAcceptanceDefectInput, 101) }, 422},
		"invalid actual time":  {func(in *ContractAcceptanceInput) { in.ActualAt = "later" }, 422},
		"invalid scope start":  {func(in *ContractAcceptanceInput) { in.ScopeStart = "-1" }, 422},
		"zero scope end":       {func(in *ContractAcceptanceInput) { in.ScopeEnd = "0" }, 422},
		"invalid accepted":     {func(in *ContractAcceptanceInput) { in.AcceptedQuantity = "1.5.0" }, 422},
		"invalid idempotency":  {func(in *ContractAcceptanceInput) { in.IdempotencyKey = "short" }, 422},
	}
	for name, testCase := range cases {
		input := validAcceptanceCommand()
		testCase.mutate(&input)
		if _, err := PrepareAcceptance(input); err == nil {
			t.Fatalf("%s was accepted", name)
		} else {
			requireCommandFault(t, name, err, testCase.status)
		}
	}
}

func TestPrepareRectificationAcceptsCanonicalInput(t *testing.T) {
	prepared, err := PrepareRectification(validRectificationCommand())
	if err != nil || prepared.Input.ContractID != commandContractUUID || len(prepared.RequestDigest) != 64 {
		t.Fatalf("prepared rectification: %+v %v", prepared, err)
	}
}

func TestPrepareRectificationRejectsInvalidFields(t *testing.T) {
	cases := map[string]struct {
		mutate func(*ContractRectificationInput)
		status int
	}{
		"invalid contract":    {func(in *ContractRectificationInput) { in.ContractID = "contract-1" }, 404},
		"zero row version":    {func(in *ContractRectificationInput) { in.ExpectedRowVersion = 0 }, 422},
		"invalid revision":    {func(in *ContractRectificationInput) { in.ContractRevisionID = "revision-1" }, 422},
		"invalid acceptance":  {func(in *ContractRectificationInput) { in.AcceptanceID = "acceptance-1" }, 422},
		"invalid defect":      {func(in *ContractRectificationInput) { in.DefectID = "defect-1" }, 422},
		"empty description":   {func(in *ContractRectificationInput) { in.Description = " " }, 422},
		"long description":    {func(in *ContractRectificationInput) { in.Description = strings.Repeat("a", 4001) }, 422},
		"invalid idempotency": {func(in *ContractRectificationInput) { in.IdempotencyKey = "short" }, 422},
	}
	for name, testCase := range cases {
		input := validRectificationCommand()
		testCase.mutate(&input)
		if _, err := PrepareRectification(input); err == nil {
			t.Fatalf("%s was accepted", name)
		} else {
			requireCommandFault(t, name, err, testCase.status)
		}
	}
}
