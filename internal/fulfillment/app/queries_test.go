// File metadata (manifest mirror):
// Purpose: Exercises fulfillment query projections over fake repositories: delivery and acceptance histories, evidence redaction, revision metadata, summaries and accepted-total facts.
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

	contractapp "contractaudit/internal/contract/app"
	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

type fakeFulfillmentAccess struct {
	canRead           bool
	canReadManagement bool
	canManage         bool
	readableCases     map[string]bool
	caseErr           error
	resolveErr        error
}

func (a *fakeFulfillmentAccess) CanRead(context.Context, clmcontract.Aggregate) bool {
	return a.canRead
}

func (a *fakeFulfillmentAccess) CanReadForManagement(context.Context, clmcontract.Aggregate) bool {
	return a.canReadManagement
}

func (a *fakeFulfillmentAccess) CanManage(context.Context, clmcontract.Aggregate) bool {
	return a.canManage
}

func (a *fakeFulfillmentAccess) ResolveMaterial(_ context.Context, ref clmcontract.MaterialRef, purpose string) (clmcontract.ContractVersionMaterial, error) {
	if a.resolveErr != nil {
		return clmcontract.ContractVersionMaterial{}, a.resolveErr
	}
	return clmcontract.ContractVersionMaterial{
		MaterialID: ref.MaterialID, MaterialVersionID: ref.MaterialVersionID,
		Purpose: purpose, CaseOrgID: "org-a", CaseID: "case-a",
	}, nil
}

func (a *fakeFulfillmentAccess) CanReadCase(_ context.Context, orgID, caseID string) error {
	if a.caseErr != nil {
		return a.caseErr
	}
	if a.readableCases[orgID+"/"+caseID] {
		return nil
	}
	return fault.Forbidden("denied")
}

type fakeFulfillmentContracts struct {
	contract        clmcontract.Aggregate
	items           []contractapp.FulfillmentRevisionItem
	revisions       map[string]clmcontract.FrozenContractRevision
	item            clmcontract.RevisionPlanItem
	itemFound       bool
	advanced        clmcontract.Aggregate
	receipt         clmcontract.WriteReceipt
	found           bool
	insertedReceipt []byte
	receiptStatus   int
	errs            map[string]error
}

func (c *fakeFulfillmentContracts) LockWriteReceipt(context.Context, string, string, string, string) error {
	return c.errs["lock_receipt"]
}

func (c *fakeFulfillmentContracts) LoadWriteReceipt(context.Context, string, string, string, string) (clmcontract.WriteReceipt, bool, error) {
	return c.receipt, c.found, c.errs["load_receipt"]
}

func (c *fakeFulfillmentContracts) InsertWriteReceipt(_ context.Context, _, _, _, _, _, _ string, status int, response []byte) error {
	c.receiptStatus = status
	c.insertedReceipt = append([]byte(nil), response...)
	return c.errs["insert_receipt"]
}

func (c *fakeFulfillmentContracts) LockContract(context.Context, string) error { return c.errs["lock"] }

func (c *fakeFulfillmentContracts) LoadContract(context.Context, string, string) (clmcontract.Aggregate, error) {
	return c.contract, c.errs["load_contract"]
}

func (c *fakeFulfillmentContracts) LoadFulfillmentRevision(_ context.Context, _, _, revisionID string) (clmcontract.FrozenContractRevision, error) {
	if err := c.errs["load_revision"]; err != nil {
		return clmcontract.FrozenContractRevision{}, err
	}
	return c.revisions[revisionID], nil
}

func (c *fakeFulfillmentContracts) LoadFulfillmentRevisionItem(context.Context, string, string, string, string) (clmcontract.RevisionPlanItem, bool, error) {
	return c.item, c.itemFound, c.errs["load_item"]
}

func (c *fakeFulfillmentContracts) ListFulfillmentRevisionItems(context.Context, string, string) ([]contractapp.FulfillmentRevisionItem, error) {
	return c.items, c.errs["list_items"]
}

func (c *fakeFulfillmentContracts) AdvanceFulfillmentAggregate(context.Context, clmcontract.Aggregate, int64) (clmcontract.Aggregate, error) {
	return c.advanced, c.errs["advance"]
}

type fakeFulfillmentRepository struct {
	FulfillmentRepository
	deliveries       []DeliveryFact
	acceptances      []AcceptanceFact
	defects          map[string][]DefectFact
	rectifications   map[string][]RectificationFact
	rectificationIDs map[string][]string
	sumDelivered     string
	sumPlanDelivery  string
	insertedAt       time.Time
	delivery         DeliveryFact
	deliveryFound    bool
	overlap          bool
	acceptance       AcceptanceFact
	acceptanceFound  bool
	acceptanceResult string
	resultFound      bool
	hasReinspection  bool
	hasDefect        bool
	rectification    RectificationFact
	rectificationOK  bool
	insertedDelivery *DeliveryInsert
	insertedAccept   *AcceptanceInsert
	insertedDefects  []DefectInsert
	insertedRectify  *RectificationInsert
	errs             map[string]error
}

func (r *fakeFulfillmentRepository) ListDeliveries(context.Context, string, string) ([]DeliveryFact, error) {
	return r.deliveries, r.errs["list_deliveries"]
}

func (r *fakeFulfillmentRepository) ListAcceptances(context.Context, string, string) ([]AcceptanceFact, error) {
	return r.acceptances, r.errs["list_acceptances"]
}

func (r *fakeFulfillmentRepository) ListAcceptanceDefects(_ context.Context, _, _, acceptanceID string) ([]DefectFact, error) {
	if err := r.errs["list_defects"]; err != nil {
		return nil, err
	}
	return r.defects[acceptanceID], nil
}

func (r *fakeFulfillmentRepository) ListAcceptanceRectifications(_ context.Context, _, _, acceptanceID string) ([]RectificationFact, error) {
	if err := r.errs["list_rectifications"]; err != nil {
		return nil, err
	}
	return r.rectifications[acceptanceID], nil
}

func (r *fakeFulfillmentRepository) ListAcceptanceRectificationIDs(_ context.Context, _, _, acceptanceID string) ([]string, error) {
	if err := r.errs["list_rectification_ids"]; err != nil {
		return nil, err
	}
	return r.rectificationIDs[acceptanceID], nil
}

func fulfillmentQueryFixture(t *testing.T) (clmcontract.Aggregate, *fakeFulfillmentContracts, *fakeFulfillmentRepository, *fakeFulfillmentAccess) {
	t.Helper()
	revisionID := "revision-1"
	snapshot, err := json.Marshal(map[string]any{"items": []any{
		map[string]any{"item_id": commandContractUUID, "name": "服务器", "quantity": "2.000000", "unit": "台"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	deliveryEvidence, err := json.Marshal([]clmcontract.ContractVersionMaterial{
		{MaterialID: "material-a", CaseOrgID: "org-a", CaseID: "case-a"},
		{MaterialID: "material-b", CaseOrgID: "org-b", CaseID: "case-b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rectificationEvidence, err := json.Marshal([]clmcontract.ContractVersionMaterial{
		{MaterialID: "material-a", CaseOrgID: "org-a", CaseID: "case-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	planned := "2.000000"
	unit := "台"
	contracts := &fakeFulfillmentContracts{
		contract: clmcontract.Aggregate{
			TenantID: "tenant-a", ContractID: "contract-1", AuthorityMode: "native",
			LifecycleState: "registered", ValidityState: "active", RowVersion: 7,
		},
		items: []contractapp.FulfillmentRevisionItem{{
			RevisionID: revisionID,
			Item:       clmcontract.RevisionPlanItem{ItemID: commandContractUUID, PlannedQuantity: &planned, Unit: &unit},
		}},
		revisions: map[string]clmcontract.FrozenContractRevision{
			revisionID: {Snapshot: snapshot},
		},
		errs: map[string]error{},
	}
	repository := &fakeFulfillmentRepository{
		deliveries: []DeliveryFact{{
			DeliveryID: "delivery-1", ContractRevisionID: revisionID, ItemID: commandContractUUID,
			Quantity: "1.000000", Unit: "台", EvidenceSnapshot: deliveryEvidence,
			RecordedBy: "alice", RecordedAt: time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC),
		}},
		acceptances: []AcceptanceFact{{
			AcceptanceID: "acceptance-1", ContractRevisionID: revisionID, DeliveryID: "delivery-1",
			ItemID: commandContractUUID, ScopeStart: "0", ScopeEnd: "1.000000", AcceptedQuantity: "0.500000",
			Result: "partial", EvidenceSnapshot: []byte("[]"),
		}},
		defects: map[string][]DefectFact{
			"acceptance-1": {{
				DefectID: commandDefectUUID, Description: "外观破损", Responsible: "供应商",
				RemediationDue: "2026-11-20", FailedQuantity: "0.500000", RecordedBy: "alice",
			}},
		},
		rectifications: map[string][]RectificationFact{
			"acceptance-1": {{
				RectificationID: commandAcceptanceID, ContractRevisionID: revisionID, AcceptanceID: "acceptance-1",
				DefectID: commandDefectUUID, Description: "更换部件", EvidenceSnapshot: rectificationEvidence,
			}},
		},
		rectificationIDs: map[string][]string{"acceptance-1": {commandAcceptanceID}},
		errs:             map[string]error{},
	}
	access := &fakeFulfillmentAccess{readableCases: map[string]bool{"org-a/case-a": true}}
	return contracts.contract, contracts, repository, access
}

func TestListContractDeliveriesProjectsEvidenceAndSummary(t *testing.T) {
	record, contracts, repository, access := fulfillmentQueryFixture(t)
	list, err := ListContractDeliveries(context.Background(), record, contracts, repository, access)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if list.RowVersion != 7 || len(list.Items) != 1 {
		t.Fatalf("delivery list is %+v", list)
	}
	item := list.Items[0]
	if item.DeliveryID != "delivery-1" || item.ItemName != "服务器" || item.Quantity != "1.000000" || item.Unit != "台" ||
		len(item.Evidence) != 1 || item.Evidence[0].MaterialID != "material-a" || !item.EvidenceRedacted {
		t.Fatalf("delivery item is %+v", item)
	}
	if len(list.Summary) != 1 {
		t.Fatalf("delivery summary is %+v", list.Summary)
	}
	summary := list.Summary[0]
	if summary.ItemName != "服务器" || summary.PlannedQuantity != "2.000000" || summary.DeliveredQuantity != "1.000000" ||
		summary.QualifiedQuantity != "0.500000" || summary.UnallocatedQuantity != "0.000000" ||
		summary.PendingReinspectionQuantity != "0.500000" || summary.Unit != "台" {
		t.Fatalf("summary row is %+v", summary)
	}

	repository.errs["list_deliveries"] = errors.New("deliveries failed")
	if _, err := ListContractDeliveries(context.Background(), record, contracts, repository, access); err == nil {
		t.Fatal("delivery list ignored a repository failure")
	}
	delete(repository.errs, "list_deliveries")
	repository.errs["list_acceptances"] = errors.New("acceptances failed")
	if _, err := ListContractDeliveries(context.Background(), record, contracts, repository, access); err == nil {
		t.Fatal("delivery list ignored an acceptance failure")
	}
	delete(repository.errs, "list_acceptances")
	contracts.errs["list_items"] = errors.New("items failed")
	if _, err := ListContractDeliveries(context.Background(), record, contracts, repository, access); err == nil {
		t.Fatal("delivery list ignored a revision item failure")
	}
	delete(contracts.errs, "list_items")
	contracts.errs["load_revision"] = errors.New("revision failed")
	if _, err := ListContractDeliveries(context.Background(), record, contracts, repository, access); err == nil {
		t.Fatal("delivery list ignored a revision failure")
	}
}

func TestListContractDeliveriesRejectsUnreadableEvidence(t *testing.T) {
	record, contracts, repository, access := fulfillmentQueryFixture(t)
	originalEvidence := repository.deliveries[0].EvidenceSnapshot
	repository.deliveries[0].EvidenceSnapshot = []byte("not json")
	if _, err := ListContractDeliveries(context.Background(), record, contracts, repository, access); err == nil {
		t.Fatal("malformed evidence snapshot was accepted")
	}
	repository.deliveries[0].EvidenceSnapshot = originalEvidence
	access.caseErr = errors.New("authorization store unavailable")
	if _, err := ListContractDeliveries(context.Background(), record, contracts, repository, access); err == nil {
		t.Fatal("non-fault case read failure was swallowed")
	}
}

func TestListContractAcceptancesProjectsDefectsRectificationsAndUnits(t *testing.T) {
	record, contracts, repository, access := fulfillmentQueryFixture(t)
	list, err := ListContractAcceptances(context.Background(), record, contracts, repository, access)
	if err != nil {
		t.Fatalf("list acceptances: %v", err)
	}
	if len(list.Items) != 1 || list.RowVersion != 7 {
		t.Fatalf("acceptance list is %+v", list)
	}
	item := list.Items[0]
	if item.AcceptanceID != "acceptance-1" || item.ItemName != "服务器" || item.Quantity != "1.000000" ||
		item.FailedQuantity != "0.500000" || item.Unit != "台" || item.Result != "partial" {
		t.Fatalf("acceptance item is %+v", item)
	}
	if len(item.Defects) != 1 || item.Defects[0].DefectID != commandDefectUUID {
		t.Fatalf("acceptance defects are %+v", item.Defects)
	}
	if len(item.Rectifications) != 1 || item.Rectifications[0].RectificationID != commandAcceptanceID ||
		len(item.Rectifications[0].Evidence) != 1 {
		t.Fatalf("acceptance rectifications are %+v", item.Rectifications)
	}
	if len(item.RectificationIDs) != 1 || item.RectificationIDs[0] != commandAcceptanceID {
		t.Fatalf("acceptance rectification ids are %+v", item.RectificationIDs)
	}

	for name, key := range map[string]string{
		"deliveries":        "list_deliveries",
		"acceptances":       "list_acceptances",
		"defects":           "list_defects",
		"rectifications":    "list_rectifications",
		"rectification ids": "list_rectification_ids",
	} {
		repository.errs[key] = errors.New(name + " failed")
		if _, err := ListContractAcceptances(context.Background(), record, contracts, repository, access); err == nil {
			t.Fatalf("acceptance list ignored %s failure", name)
		}
		delete(repository.errs, key)
	}
}

func TestReadAcceptanceFactsSumsOnlyTheExactRevisionItem(t *testing.T) {
	repository := &fakeFulfillmentRepository{
		acceptances: []AcceptanceFact{
			{AcceptanceID: "a1", ContractRevisionID: "rev-1", ItemID: "item-1", AcceptedQuantity: "0.500000"},
			{AcceptanceID: "a2", ContractRevisionID: "rev-1", ItemID: "item-1", AcceptedQuantity: "0.250000"},
			{AcceptanceID: "a3", ContractRevisionID: "rev-2", ItemID: "item-1", AcceptedQuantity: "1.000000"},
			{AcceptanceID: "a4", ContractRevisionID: "rev-1", ItemID: "item-2", AcceptedQuantity: "1.000000"},
		},
		errs: map[string]error{},
	}
	facts, err := ReadAcceptanceFacts(context.Background(), "tenant-a", "contract-1", "rev-1", "item-1", repository)
	if err != nil || facts.AcceptedQuantity != "0.750000" || len(facts.Acceptances) != 2 ||
		facts.ContractRevisionID != "rev-1" || facts.ItemID != "item-1" {
		t.Fatalf("acceptance facts: %+v %v", facts, err)
	}
	empty, err := ReadAcceptanceFacts(context.Background(), "tenant-a", "contract-1", "rev-9", "item-9", repository)
	if err != nil || empty.AcceptedQuantity != "0" || empty.Acceptances == nil || len(empty.Acceptances) != 0 {
		t.Fatalf("empty acceptance facts: %+v %v", empty, err)
	}
	repository.acceptances[0].AcceptedQuantity = "bad"
	if _, err := ReadAcceptanceFacts(context.Background(), "tenant-a", "contract-1", "rev-1", "item-1", repository); err == nil {
		t.Fatal("malformed accepted quantity was summed")
	}
	repository.acceptances[0].AcceptedQuantity = "0.500000"
	repository.errs["list_acceptances"] = errors.New("read failed")
	if _, err := ReadAcceptanceFacts(context.Background(), "tenant-a", "contract-1", "rev-1", "item-1", repository); err == nil {
		t.Fatal("acceptance read failure was swallowed")
	}
}

func TestFulfillmentSummaryRejectsUnreadableFacts(t *testing.T) {
	record, contracts, repository, access := fulfillmentQueryFixture(t)
	repository.deliveries[0].Quantity = "bad"
	if _, err := ListContractDeliveries(context.Background(), record, contracts, repository, access); err == nil {
		t.Fatal("malformed delivery quantity was summarized")
	}
	repository.deliveries[0].Quantity = "1.000000"
	repository.acceptances[0].ScopeEnd = "bad"
	if _, err := ListContractAcceptances(context.Background(), record, contracts, repository, access); err == nil {
		t.Fatal("malformed acceptance range was summarized")
	}
}

func TestResolveAndProjectFulfillmentEvidenceBoundaries(t *testing.T) {
	access := &fakeFulfillmentAccess{readableCases: map[string]bool{"org-a/case-a": true}}
	ref := clmcontract.MaterialRef{MaterialID: "material-1", MaterialVersionID: "version-1", SHA256: strings.Repeat("a", 64), Purpose: "supporting"}
	resolved, err := ResolveEvidence(context.Background(), []clmcontract.MaterialRef{ref}, "acceptance", access)
	if err != nil || len(resolved) != 1 || resolved[0].MaterialID != "material-1" || resolved[0].Purpose != "acceptance" ||
		resolved[0].CaseOrgID != "org-a" {
		t.Fatalf("resolved evidence: %+v %v", resolved, err)
	}
	other := ref
	other.Purpose = "other"
	if _, err := ResolveEvidence(context.Background(), []clmcontract.MaterialRef{other}, "acceptance", access); err != nil {
		t.Fatalf("other purpose evidence: %v", err)
	}
	for name, refs := range map[string][]clmcontract.MaterialRef{
		"no evidence": nil,
		"too many":    make([]clmcontract.MaterialRef, 51),
		"duplicate":   {ref, ref},
		"wrong purpose": {
			{MaterialID: "material-1", MaterialVersionID: "version-1", SHA256: strings.Repeat("a", 64), Purpose: "draft"},
		},
	} {
		if _, err := ResolveEvidence(context.Background(), refs, "acceptance", access); err == nil {
			t.Fatalf("%s evidence was accepted", name)
		}
	}
	access.resolveErr = errors.New("material store unavailable")
	if _, err := ResolveEvidence(context.Background(), []clmcontract.MaterialRef{ref}, "acceptance", access); err == nil {
		t.Fatal("material resolution failure was swallowed")
	}
	access.resolveErr = nil

	raw, err := json.Marshal([]clmcontract.ContractVersionMaterial{
		{MaterialID: "readable", CaseOrgID: "org-a", CaseID: "case-a"},
		{MaterialID: "hidden", CaseOrgID: "org-b", CaseID: "case-b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	visible, redacted, err := ProjectEvidenceSnapshot(context.Background(), raw, access)
	if err != nil || !redacted || len(visible) != 1 || visible[0].MaterialID != "readable" {
		t.Fatalf("projected evidence: %+v %v %v", visible, redacted, err)
	}
	if _, _, err := ProjectEvidenceSnapshot(context.Background(), []byte("not json"), access); err == nil {
		t.Fatal("malformed projection input was accepted")
	}
	notFound := &fakeFulfillmentAccess{caseErr: fault.NotFound("hidden")}
	if visible, redacted, err := ProjectEvidenceSnapshot(context.Background(), raw, notFound); err != nil || !redacted || len(visible) != 0 {
		t.Fatalf("not-found evidence projection: %+v %v %v", visible, redacted, err)
	}
}

func TestRequireFulfillmentManageEnforcesCurrentAuthorization(t *testing.T) {
	record := clmcontract.Aggregate{AuthorityMode: "native", LifecycleState: "registered", ValidityState: "active"}
	ctx := context.Background()
	for name, testCase := range map[string]struct {
		access *fakeFulfillmentAccess
		record clmcontract.Aggregate
		status int
	}{
		"management read denied": {&fakeFulfillmentAccess{}, record, 404},
		"manage denied":          {&fakeFulfillmentAccess{canReadManagement: true}, record, 403},
		"external authority": {&fakeFulfillmentAccess{canReadManagement: true, canManage: true},
			clmcontract.Aggregate{AuthorityMode: "external", LifecycleState: "registered", ValidityState: "active"}, 403},
		"inactive validity": {&fakeFulfillmentAccess{canReadManagement: true, canManage: true},
			clmcontract.Aggregate{AuthorityMode: "native", LifecycleState: "registered", ValidityState: "inactive"}, 409},
		"draft lifecycle": {&fakeFulfillmentAccess{canReadManagement: true, canManage: true},
			clmcontract.Aggregate{AuthorityMode: "native", LifecycleState: "draft", ValidityState: "active"}, 409},
	} {
		err := RequireFulfillmentManage(ctx, testCase.record, testCase.access)
		requireCommandFault(t, name, err, testCase.status)
	}
	allowed := &fakeFulfillmentAccess{canReadManagement: true, canManage: true}
	if err := RequireFulfillmentManage(ctx, record, allowed); err != nil {
		t.Fatalf("allowed management: %v", err)
	}
	performing := record
	performing.LifecycleState = "performing"
	if err := RequireFulfillmentManage(ctx, performing, allowed); err != nil {
		t.Fatalf("performing management: %v", err)
	}
}

func TestLoadWritableContractAndReplayReceiptGuards(t *testing.T) {
	ctx := context.Background()
	_, contracts, _, access := fulfillmentQueryFixture(t)
	access.canReadManagement = true
	access.canManage = true
	access.canRead = true
	loaded, err := loadWritableContract(ctx, "tenant-a", "contract-1", 7, contracts, access)
	if err != nil || loaded.ContractID != "contract-1" {
		t.Fatalf("writable contract: %+v %v", loaded, err)
	}
	requireCommandFault(t, "stale row version", secondValue(loadWritableContract(ctx, "tenant-a", "contract-1", 6, contracts, access)), 409)
	contracts.errs["load_contract"] = errors.New("load failed")
	if _, err := loadWritableContract(ctx, "tenant-a", "contract-1", 7, contracts, access); err == nil {
		t.Fatal("writable contract load failure was swallowed")
	}
	delete(contracts.errs, "load_contract")

	principal := Principal{TenantID: "tenant-a", ActorID: "alice"}
	contracts.found = false
	receipt, found, err := loadReplayReceipt(ctx, principal, CreateDeliveryRoute, "delivery-key-1", "digest", "contract-1", contracts, access)
	if err != nil || found || receipt.RequestDigest != "" {
		t.Fatalf("missing receipt: %+v %v %v", receipt, found, err)
	}
	contracts.found = true
	contracts.receipt = clmcontract.WriteReceipt{
		ContractID: "contract-1", RequestDigest: "digest", ResponseStatus: 201, Response: []byte(`{}`),
	}
	receipt, found, err = loadReplayReceipt(ctx, principal, CreateDeliveryRoute, "delivery-key-1", "digest", "contract-1", contracts, access)
	if err != nil || !found || receipt.ResponseStatus != 201 {
		t.Fatalf("replayed receipt: %+v %v %v", receipt, found, err)
	}
	if _, _, err := loadReplayReceipt(ctx, principal, CreateDeliveryRoute, "delivery-key-1", "other", "contract-1", contracts, access); err == nil {
		t.Fatal("changed payload replay was accepted")
	} else {
		requireCommandFault(t, "changed payload", err, 409)
	}
	if _, _, err := loadReplayReceipt(ctx, principal, CreateDeliveryRoute, "delivery-key-1", "digest", "contract-2", contracts, access); err == nil {
		t.Fatal("cross-contract replay was accepted")
	} else {
		requireCommandFault(t, "other contract", err, 409)
	}
	// An unreadable receipt contract is reported as absent.
	access.canRead = false
	contracts.receipt.ContractID = "contract-1"
	if _, _, err := loadReplayReceipt(ctx, principal, CreateDeliveryRoute, "delivery-key-1", "digest", "contract-1", contracts, access); err == nil {
		t.Fatal("unreadable receipt contract was replayed")
	} else {
		requireCommandFault(t, "unreadable receipt contract", err, 404)
	}
	access.canRead = true
	for name, key := range map[string]string{"lock receipt": "lock_receipt", "load receipt": "load_receipt"} {
		contracts.errs[key] = errors.New(name + " failed")
		if _, _, err := loadReplayReceipt(ctx, principal, CreateDeliveryRoute, "delivery-key-1", "digest", "contract-1", contracts, access); err == nil {
			t.Fatalf("%s failure was swallowed", name)
		}
		delete(contracts.errs, key)
	}
	contracts.errs["load_contract"] = errors.New("load failed")
	if _, _, err := loadReplayReceipt(ctx, principal, CreateDeliveryRoute, "delivery-key-1", "digest", "contract-1", contracts, access); err == nil {
		t.Fatal("receipt contract load failure was swallowed")
	}
}

func (r *fakeFulfillmentRepository) SumDelivered(context.Context, string, string, string, string) (string, error) {
	return r.sumDelivered, r.errs["sum_delivered"]
}

func (r *fakeFulfillmentRepository) SumPlanDelivery(context.Context, string, string, string, string, int) (string, error) {
	return r.sumPlanDelivery, r.errs["sum_plan"]
}

func (r *fakeFulfillmentRepository) InsertDelivery(_ context.Context, insert DeliveryInsert) (time.Time, error) {
	r.insertedDelivery = &insert
	return r.insertedAt, r.errs["insert_delivery"]
}

func (r *fakeFulfillmentRepository) LoadDelivery(context.Context, string, string, string, string) (DeliveryFact, bool, error) {
	return r.delivery, r.deliveryFound, r.errs["load_delivery"]
}

func (r *fakeFulfillmentRepository) HasOverlappingRootAcceptance(context.Context, string, string, string, string, string) (bool, error) {
	return r.overlap, r.errs["overlap"]
}

func (r *fakeFulfillmentRepository) LoadAcceptance(context.Context, string, string, string) (AcceptanceFact, bool, error) {
	return r.acceptance, r.acceptanceFound, r.errs["load_acceptance"]
}

func (r *fakeFulfillmentRepository) HasReinspection(context.Context, string, string, string) (bool, error) {
	return r.hasReinspection, r.errs["has_reinspection"]
}

func (r *fakeFulfillmentRepository) HasDefect(context.Context, string, string, string, string) (bool, error) {
	return r.hasDefect, r.errs["has_defect"]
}

func (r *fakeFulfillmentRepository) InsertAcceptance(_ context.Context, insert AcceptanceInsert) (time.Time, error) {
	r.insertedAccept = &insert
	return r.insertedAt, r.errs["insert_acceptance"]
}

func (r *fakeFulfillmentRepository) LoadRectification(context.Context, string, string, string) (RectificationFact, bool, error) {
	return r.rectification, r.rectificationOK, r.errs["load_rectification"]
}

func (r *fakeFulfillmentRepository) InsertAcceptanceRectification(context.Context, AcceptanceRectificationInsert) error {
	return r.errs["insert_acceptance_rectification"]
}

func (r *fakeFulfillmentRepository) InsertDefect(_ context.Context, insert DefectInsert) (time.Time, error) {
	r.insertedDefects = append(r.insertedDefects, insert)
	return r.insertedAt, r.errs["insert_defect"]
}

func (r *fakeFulfillmentRepository) LoadAcceptanceResult(context.Context, string, string, string, string) (string, bool, error) {
	return r.acceptanceResult, r.resultFound, r.errs["load_result"]
}

func (r *fakeFulfillmentRepository) InsertRectification(_ context.Context, insert RectificationInsert) (time.Time, error) {
	r.insertedRectify = &insert
	return r.insertedAt, r.errs["insert_rectification"]
}

func TestValidateRectificationsForRetestRequiresFullAuthorizedCoverage(t *testing.T) {
	ctx := context.Background()
	rectification1 := "66666666-6666-4666-8666-666666666666"
	rectification2 := "77777777-7777-4777-8777-777777777777"
	rectification3 := "88888888-8888-4888-8888-888888888888"
	access := &fakeFulfillmentAccess{readableCases: map[string]bool{"org-a/case-a": true}}
	evidence, err := json.Marshal([]clmcontract.ContractVersionMaterial{{MaterialID: "m1", CaseOrgID: "org-a", CaseID: "case-a"}})
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeFulfillmentRepository{
		defects: map[string][]DefectFact{
			"parent": {{DefectID: "defect-1"}, {DefectID: "defect-2"}},
		},
		rectifications: map[string][]RectificationFact{
			"parent": {
				{RectificationID: rectification1, DefectID: "defect-1", EvidenceSnapshot: evidence},
				{RectificationID: rectification2, DefectID: "defect-2", EvidenceSnapshot: []byte("[]")},
				{RectificationID: rectification3, DefectID: "defect-1", EvidenceSnapshot: []byte("[]")},
			},
		},
		errs: map[string]error{},
	}
	if err := validateRectificationsForRetest(ctx, "tenant-a", "contract-a", "parent",
		[]string{rectification1, rectification2}, repository, access); err != nil {
		t.Fatalf("complete rectification coverage: %v", err)
	}
	requireRetest := func(name string, ids []string, status int) {
		t.Helper()
		err := validateRectificationsForRetest(ctx, "tenant-a", "contract-a", "parent", ids, repository, access)
		if err == nil {
			t.Fatalf("%s was accepted", name)
		}
		requireCommandFault(t, name, err, status)
	}
	requireRetest("missing rectification", []string{rectification1}, 409)
	requireRetest("extra rectification", []string{rectification1, rectification2, rectification3}, 422)
	requireRetest("duplicate rectification", []string{rectification1, rectification1, rectification2}, 422)
	requireRetest("invalid rectification id", []string{"bad id"}, 422)

	repository.defects["parent"] = nil
	requireRetest("no tracked defects", []string{rectification1}, 409)
	repository.defects["parent"] = []DefectFact{{DefectID: "defect-1"}, {DefectID: "defect-2"}}

	repository.rectifications["parent"][0].EvidenceSnapshot = []byte("not json")
	if err := validateRectificationsForRetest(ctx, "tenant-a", "contract-a", "parent",
		[]string{rectification1, rectification2}, repository, access); err == nil {
		t.Fatal("malformed rectification evidence was accepted")
	}
	repository.rectifications["parent"][0].EvidenceSnapshot = evidence
	access.readableCases = map[string]bool{}
	requireRetest("unreadable rectification evidence", []string{rectification1, rectification2}, 404)
	access.readableCases = map[string]bool{"org-a/case-a": true}

	repository.errs["list_defects"] = errors.New("defect read failed")
	if err := validateRectificationsForRetest(ctx, "tenant-a", "contract-a", "parent",
		[]string{rectification1, rectification2}, repository, access); err == nil {
		t.Fatal("defect read failure was swallowed")
	}
	delete(repository.errs, "list_defects")
	repository.errs["list_rectifications"] = errors.New("rectification read failed")
	if err := validateRectificationsForRetest(ctx, "tenant-a", "contract-a", "parent",
		[]string{rectification1, rectification2}, repository, access); err == nil {
		t.Fatal("rectification read failure was swallowed")
	}
}
