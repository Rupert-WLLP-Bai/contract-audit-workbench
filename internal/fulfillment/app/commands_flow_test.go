// File metadata (manifest mirror):
// Purpose: Exercises fulfillment write orchestration over fakes: delivery, acceptance and rectification command flows, frozen-plan limits, evidence resolution, events, receipts and replay.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	clmcontract "contractaudit/internal/contract/model"
)

type fakeEventAppender struct {
	eventTypes []string
	versions   []int64
	err        error
}

func (a *fakeEventAppender) AppendContractEvent(_ context.Context, _, _, _ string, version int64, eventType string, _ []byte) error {
	if a.err != nil {
		return a.err
	}
	a.eventTypes = append(a.eventTypes, eventType)
	a.versions = append(a.versions, version)
	return nil
}

type fakeOutboxEnqueuer struct {
	eventIDs []string
	err      error
}

func (o *fakeOutboxEnqueuer) EnqueueContractEvent(_ context.Context, _, eventID string) error {
	if o.err != nil {
		return o.err
	}
	o.eventIDs = append(o.eventIDs, eventID)
	return nil
}

type fulfillmentFlow struct {
	principal  Principal
	record     clmcontract.Aggregate
	contracts  *fakeFulfillmentContracts
	repository *fakeFulfillmentRepository
	access     *fakeFulfillmentAccess
	events     *fakeEventAppender
	outbox     *fakeOutboxEnqueuer
}

func newFulfillmentFlow(t *testing.T) *fulfillmentFlow {
	t.Helper()
	revisionID := commandRevisionUUID
	snapshot, err := json.Marshal(map[string]any{
		"items": []any{map[string]any{
			"item_id": commandContractUUID, "name": "服务器", "quantity": "2.000000", "unit": "台",
		}},
		"delivery_plans": []any{map[string]any{
			"sequence": 1, "item_id": commandContractUUID, "planned_date": "2026-11-20", "quantity": "2.000000",
		}},
		"acceptance_plans": []any{map[string]any{
			"sequence": 1, "planned_date": "2026-11-25", "criteria": "符合合同约定", "item_ids": []any{commandContractUUID},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	record := clmcontract.Aggregate{
		TenantID: "tenant-a", ContractID: "contract-1", AuthorityMode: "native",
		LifecycleState: "registered", ValidityState: "active", RowVersion: 7,
		CurrentRevisionID: &revisionID,
	}
	advanced := record
	advanced.RowVersion = 8
	planned, unit := "2.000000", "台"
	contracts := &fakeFulfillmentContracts{
		contract: record,
		revisions: map[string]clmcontract.FrozenContractRevision{
			revisionID: {Snapshot: snapshot},
		},
		item:      clmcontract.RevisionPlanItem{ItemID: commandContractUUID, PlannedQuantity: &planned, Unit: &unit},
		itemFound: true, advanced: advanced, errs: map[string]error{},
	}
	insertedAt := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	repository := &fakeFulfillmentRepository{
		errs:            map[string]error{},
		sumDelivered:    "0",
		sumPlanDelivery: "0",
		insertedAt:      insertedAt,
		delivery: DeliveryFact{
			DeliveryID: commandDeliveryUUID, ContractRevisionID: revisionID, ItemID: commandContractUUID,
			Quantity: "1.000000", Unit: "台",
		},
		deliveryFound:    true,
		acceptanceResult: "partial",
		resultFound:      true,
		hasDefect:        true,
	}
	access := &fakeFulfillmentAccess{
		canRead: true, canReadManagement: true, canManage: true,
		readableCases: map[string]bool{"org-a/case-a": true},
	}
	return &fulfillmentFlow{
		principal: Principal{TenantID: "tenant-a", ActorID: "alice"},
		record:    record, contracts: contracts, repository: repository, access: access,
		events: &fakeEventAppender{}, outbox: &fakeOutboxEnqueuer{},
	}
}

func flowDeliveryInput() ContractDeliveryInput {
	input := validDeliveryCommand()
	input.ExpectedRowVersion = 7
	input.ContractRevisionID = commandRevisionUUID
	return input
}

func TestCreateContractDeliveryPersistsRunsEventsAndReplays(t *testing.T) {
	flow := newFulfillmentFlow(t)
	prepared, err := PrepareDelivery(flowDeliveryInput())
	if err != nil {
		t.Fatal(err)
	}
	result, err := CreateContractDelivery(context.Background(), flow.principal, prepared,
		flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
	if err != nil {
		t.Fatalf("create delivery: %v", err)
	}
	if result.ItemName != "服务器" || result.Unit != "台" || result.Quantity != "1.000000" ||
		result.Location != "仓库" || result.Description != "备注" || result.RecordedBy != "alice" ||
		len(result.Evidence) != 1 || result.Evidence[0].MaterialID != "material-1" ||
		!result.RecordedAt.Equal(flow.repository.insertedAt) {
		t.Fatalf("delivery result is %+v", result)
	}
	if flow.repository.insertedDelivery == nil || flow.repository.insertedDelivery.Unit != "台" ||
		flow.repository.insertedDelivery.BatchReference != "BATCH-1" ||
		flow.repository.insertedDelivery.Location != "仓库" {
		t.Fatalf("inserted delivery is %+v", flow.repository.insertedDelivery)
	}
	if len(flow.events.eventTypes) != 1 || flow.events.eventTypes[0] != DeliveryRecordedEvent || flow.events.versions[0] != 8 {
		t.Fatalf("delivery events are %+v %+v", flow.events.eventTypes, flow.events.versions)
	}
	if len(flow.outbox.eventIDs) != 1 {
		t.Fatalf("delivery outbox ids are %+v", flow.outbox.eventIDs)
	}
	if flow.contracts.receiptStatus != 201 || len(flow.contracts.insertedReceipt) == 0 {
		t.Fatalf("delivery receipt: %d %q", flow.contracts.receiptStatus, flow.contracts.insertedReceipt)
	}
	var saved ContractDelivery
	if err := json.Unmarshal(flow.contracts.insertedReceipt, &saved); err != nil || saved.DeliveryID != result.DeliveryID {
		t.Fatalf("stored delivery receipt: %+v %v", saved, err)
	}

	flow.contracts.found = true
	flow.contracts.receipt = clmcontract.WriteReceipt{
		ContractID: commandContractUUID, RequestDigest: prepared.RequestDigest,
		ResponseStatus: 201, Response: flow.contracts.insertedReceipt,
	}
	replayed, err := CreateContractDelivery(context.Background(), flow.principal, prepared,
		flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
	if err != nil || replayed.DeliveryID != result.DeliveryID {
		t.Fatalf("delivery replay: %+v %v", replayed, err)
	}
}

func TestCreateContractDeliveryEnforcesFrozenLimitsAndFailures(t *testing.T) {
	flow := newFulfillmentFlow(t)
	prepared, err := PrepareDelivery(flowDeliveryInput())
	if err != nil {
		t.Fatal(err)
	}
	create := func() error {
		_, err := CreateContractDelivery(context.Background(), flow.principal, prepared,
			flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
		return err
	}
	requireCommandFault(t, "missing revision item", func() error {
		flow.contracts.itemFound = false
		return create()
	}(), 422)
	flow.contracts.itemFound = true
	requireCommandFault(t, "missing planned quantity", func() error {
		flow.contracts.item.PlannedQuantity = nil
		return create()
	}(), 409)
	flow.contracts.item.PlannedQuantity = appStringPtr("2.000000")
	requireCommandFault(t, "over-delivered item", func() error {
		flow.repository.sumDelivered = "2.000000"
		return create()
	}(), 409)
	flow.repository.sumDelivered = "0"
	requireCommandFault(t, "over-delivered plan", func() error {
		flow.repository.sumPlanDelivery = "2.000000"
		return create()
	}(), 409)
	flow.repository.sumPlanDelivery = "0"
	requireCommandFault(t, "missing evidence", func() error {
		withoutEvidence := prepared
		withoutEvidence.Input.Evidence = nil
		_, err := CreateContractDelivery(context.Background(), flow.principal, withoutEvidence,
			flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
		return err
	}(), 422)
	requireCommandFault(t, "duplicate batch", func() error {
		flow.repository.errs["insert_delivery"] = errors.New("duplicate key clm_contract_deliveries")
		return create()
	}(), 409)
	delete(flow.repository.errs, "insert_delivery")
	if err := func() error {
		flow.events.err = errors.New("event store unavailable")
		return create()
	}(); err == nil {
		t.Fatal("delivery event failure was swallowed")
	}
	flow.events.err = nil
	if err := func() error {
		flow.contracts.errs["insert_receipt"] = errors.New("receipt store unavailable")
		return create()
	}(); err == nil {
		t.Fatal("delivery receipt failure was swallowed")
	}
	flow.contracts.errs["insert_receipt"] = nil
	requireCommandFault(t, "stale row version", func() error {
		stale := prepared
		stale.Input.ExpectedRowVersion = 6
		_, err := CreateContractDelivery(context.Background(), flow.principal, stale,
			flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
		return err
	}(), 409)
}

func flowAcceptanceInput() ContractAcceptanceInput {
	input := validAcceptanceCommand()
	input.ExpectedRowVersion = 7
	input.ContractRevisionID = commandRevisionUUID
	input.DeliveryID = commandDeliveryUUID
	sequence := 1
	input.AcceptancePlanSequence = &sequence
	input.Standard = "符合合同约定"
	input.Defects = []ContractAcceptanceDefectInput{{
		Description: "外观破损", Responsible: "供应商", RemediationDue: "2026-11-20", FailedQuantity: "0.500000",
	}}
	return input
}

func TestCreateContractAcceptancePersistsDefectsAndReplays(t *testing.T) {
	flow := newFulfillmentFlow(t)
	prepared, err := PrepareAcceptance(flowAcceptanceInput())
	if err != nil {
		t.Fatal(err)
	}
	result, err := CreateContractAcceptance(context.Background(), flow.principal, prepared,
		flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
	if err != nil {
		t.Fatalf("create acceptance: %v", err)
	}
	if result.ItemName != "服务器" || result.Unit != "台" || result.Quantity != "1.000000" ||
		result.FailedQuantity != "0.500000" || result.Result != "partial" || len(result.Defects) != 1 ||
		result.Defects[0].FailedQuantity != "0.500000" || result.Standard != "符合合同约定" {
		t.Fatalf("acceptance result is %+v", result)
	}
	if flow.repository.insertedAccept == nil || flow.repository.insertedAccept.Result != "partial" ||
		flow.repository.insertedAccept.Standard != "符合合同约定" {
		t.Fatalf("inserted acceptance is %+v", flow.repository.insertedAccept)
	}
	if len(flow.repository.insertedDefects) != 1 || flow.repository.insertedDefects[0].FailedQuantity != "0.500000" {
		t.Fatalf("inserted defects are %+v", flow.repository.insertedDefects)
	}
	if len(flow.events.eventTypes) != 1 || flow.events.eventTypes[0] != AcceptanceRecordedEvent || flow.events.versions[0] != 8 {
		t.Fatalf("acceptance events are %+v", flow.events.eventTypes)
	}
	if flow.contracts.receiptStatus != 201 {
		t.Fatalf("acceptance receipt status is %d", flow.contracts.receiptStatus)
	}
	flow.contracts.found = true
	flow.contracts.receipt = clmcontract.WriteReceipt{
		ContractID: commandContractUUID, RequestDigest: prepared.RequestDigest,
		ResponseStatus: 201, Response: flow.contracts.insertedReceipt,
	}
	replayed, err := CreateContractAcceptance(context.Background(), flow.principal, prepared,
		flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
	if err != nil || replayed.AcceptanceID != result.AcceptanceID {
		t.Fatalf("acceptance replay: %+v %v", replayed, err)
	}
}

func TestCreateContractAcceptanceEnforcesScopeAndDefectRules(t *testing.T) {
	flow := newFulfillmentFlow(t)
	prepared, err := PrepareAcceptance(flowAcceptanceInput())
	if err != nil {
		t.Fatal(err)
	}
	create := func() error {
		_, err := CreateContractAcceptance(context.Background(), flow.principal, prepared,
			flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
		return err
	}
	requireCommandFault(t, "missing delivery", func() error {
		flow.repository.deliveryFound = false
		return create()
	}(), 404)
	flow.repository.deliveryFound = true
	requireCommandFault(t, "zero span", func() error {
		invalid := prepared
		invalid.Input.ScopeEnd = "0"
		_, err := CreateContractAcceptance(context.Background(), flow.principal, invalid,
			flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
		return err
	}(), 422)
	requireCommandFault(t, "accepted beyond span", func() error {
		invalid := prepared
		invalid.Input.AcceptedQuantity = "2.000000"
		_, err := CreateContractAcceptance(context.Background(), flow.principal, invalid,
			flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
		return err
	}(), 422)
	requireCommandFault(t, "scope beyond delivery", func() error {
		invalid := prepared
		invalid.Input.ScopeEnd = "2.000000"
		_, err := CreateContractAcceptance(context.Background(), flow.principal, invalid,
			flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
		return err
	}(), 409)
	requireCommandFault(t, "overlapping acceptance", func() error {
		flow.repository.overlap = true
		return create()
	}(), 409)
	flow.repository.overlap = false
	requireCommandFault(t, "defect sum mismatch", func() error {
		invalid := prepared
		invalid.Input.Defects[0].FailedQuantity = "0.250000"
		_, err := CreateContractAcceptance(context.Background(), flow.principal, invalid,
			flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
		return err
	}(), 422)
	requireCommandFault(t, "partial without defects", func() error {
		invalid := prepared
		invalid.Input.Defects = nil
		_, err := CreateContractAcceptance(context.Background(), flow.principal, invalid,
			flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
		return err
	}(), 422)
	requireCommandFault(t, "prior defect on first acceptance", func() error {
		invalid := prepared
		invalid.Input.Defects[0].PriorDefectID = commandDefectUUID
		_, err := CreateContractAcceptance(context.Background(), flow.principal, invalid,
			flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
		return err
	}(), 422)
	requireCommandFault(t, "reinspection without tracked defects", func() error {
		flow.repository.hasDefect = false
		invalid := prepared
		invalid.Input.ReinspectionOf = commandDeliveryUUID
		invalid.Input.ScopeStart = "0.500000"
		invalid.Input.ScopeEnd = "1.000000"
		invalid.Input.AcceptedQuantity = "0"
		invalid.Input.Defects[0].PriorDefectID = commandDefectUUID
		flow.repository.acceptance = AcceptanceFact{
			AcceptanceID: commandDeliveryUUID, ContractRevisionID: commandRevisionUUID,
			DeliveryID: commandDeliveryUUID, ItemID: commandContractUUID,
			ScopeStart: "0", ScopeEnd: "1.000000", AcceptedQuantity: "0.500000", Result: "partial",
		}
		flow.repository.acceptanceFound = true
		_, err := CreateContractAcceptance(context.Background(), flow.principal, invalid,
			flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
		return err
	}(), 409)
}

func flowRectificationInput() ContractRectificationInput {
	input := validRectificationCommand()
	input.ExpectedRowVersion = 7
	input.ContractRevisionID = commandRevisionUUID
	input.AcceptanceID = commandAcceptanceID
	input.DefectID = commandDefectUUID
	input.Evidence = []clmcontract.MaterialRef{{
		MaterialID: "material-1", MaterialVersionID: "version-1",
		SHA256: strings.Repeat("a", 64), Purpose: "supporting",
	}}
	return input
}

func TestRecordContractRectificationPersistsEventsAndReplays(t *testing.T) {
	flow := newFulfillmentFlow(t)
	prepared, err := PrepareRectification(flowRectificationInput())
	if err != nil {
		t.Fatal(err)
	}
	result, err := RecordContractRectification(context.Background(), flow.principal, prepared,
		flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
	if err != nil {
		t.Fatalf("record rectification: %v", err)
	}
	if result.Description != "更换部件" || result.AcceptanceID != commandAcceptanceID ||
		result.DefectID != commandDefectUUID || len(result.Evidence) != 1 || result.RecordedBy != "alice" {
		t.Fatalf("rectification result is %+v", result)
	}
	if flow.repository.insertedRectify == nil || flow.repository.insertedRectify.Description != "更换部件" {
		t.Fatalf("inserted rectification is %+v", flow.repository.insertedRectify)
	}
	if len(flow.events.eventTypes) != 1 || flow.events.eventTypes[0] != RectificationRecordedEvent || flow.events.versions[0] != 8 {
		t.Fatalf("rectification events are %+v", flow.events.eventTypes)
	}
	if flow.contracts.receiptStatus != 201 {
		t.Fatalf("rectification receipt status is %d", flow.contracts.receiptStatus)
	}
	flow.contracts.found = true
	flow.contracts.receipt = clmcontract.WriteReceipt{
		ContractID: commandContractUUID, RequestDigest: prepared.RequestDigest,
		ResponseStatus: 201, Response: flow.contracts.insertedReceipt,
	}
	replayed, err := RecordContractRectification(context.Background(), flow.principal, prepared,
		flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
	if err != nil || replayed.RectificationID != result.RectificationID {
		t.Fatalf("rectification replay: %+v %v", replayed, err)
	}
}

func TestRecordContractRectificationRejectsClosedOrUnknownFacts(t *testing.T) {
	flow := newFulfillmentFlow(t)
	prepared, err := PrepareRectification(flowRectificationInput())
	if err != nil {
		t.Fatal(err)
	}
	record := func() error {
		_, err := RecordContractRectification(context.Background(), flow.principal, prepared,
			flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
		return err
	}
	requireCommandFault(t, "missing acceptance", func() error {
		flow.repository.resultFound = false
		return record()
	}(), 404)
	flow.repository.resultFound = true
	requireCommandFault(t, "passed acceptance", func() error {
		flow.repository.acceptanceResult = "passed"
		return record()
	}(), 409)
	flow.repository.acceptanceResult = "partial"
	requireCommandFault(t, "already reinspected", func() error {
		flow.repository.hasReinspection = true
		return record()
	}(), 409)
	flow.repository.hasReinspection = false
	requireCommandFault(t, "missing defect", func() error {
		flow.repository.hasDefect = false
		return record()
	}(), 404)
	flow.repository.hasDefect = true
	requireCommandFault(t, "without evidence", func() error {
		invalid := prepared
		invalid.Input.Evidence = nil
		_, err := RecordContractRectification(context.Background(), flow.principal, invalid,
			flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
		return err
	}(), 422)
	requireCommandFault(t, "stale revision", func() error {
		invalid := prepared
		invalid.Input.ContractRevisionID = commandDeliveryUUID
		_, err := RecordContractRectification(context.Background(), flow.principal, invalid,
			flow.contracts, flow.repository, flow.access, flow.events, flow.outbox)
		return err
	}(), 409)
	if err := func() error {
		flow.repository.errs["insert_rectification"] = errors.New("write failed")
		return record()
	}(); err == nil {
		t.Fatal("rectification write failure was swallowed")
	}
}
