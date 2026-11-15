// File metadata (manifest mirror):
// Purpose: Exercises draft content format validation, amount reconciliation and canonical request helpers across complete, minimal and malformed contract drafts.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package contract

import (
	"errors"
	"strings"
	"testing"

	"contractaudit/internal/contractmoney"
	"contractaudit/internal/fault"
)

const draftItemUUID = "11111111-1111-4111-8111-111111111111"

const draftAssetUUID = "22222222-2222-4222-8222-222222222222"

func draftPtr[T any](value T) *T { return &value }

func completeDraftFixture() DraftContent {
	return DraftContent{
		Contract: &ContractBasics{
			Name: "采购合同", Number: draftPtr("HT-001"), ProjectNumber: draftPtr("PRJ-001"),
			ProjectType: "goods", Classification: "service", Category: "it", Grade: "a",
			DraftingParty: "buyer", DepartmentID: "dept-1", HandlerUserID: "user-1", HandlerContact: "010-1234",
			RelatedParties: []PartyRef{{PartyID: "party-1", Role: "supplier", NameSnapshot: "供应商"}},
			Amount:         "100.00", Currency: "CNY", SettlementMode: "fixed_total",
			PricesIncludeTax:     draftPtr(true),
			SigningDate:          "2026-01-02",
			PerformanceStartDate: "2026-01-03", PerformanceEndDate: "2026-01-04",
			PerformanceLocation: "机房", ProcurementMethod: "公开招标",
			ProcurementResultPackage: &ProcurementRef{System: "erp", ResultID: "r1", PackageID: "p1", SnapshotRef: "s1"},
			Remark:                   "备注",
		},
		Buyer: &Buyer{PurchasingUnit: &UnitRef{ID: "unit-1", Name: "采购单位"}, ContactName: "张三", ContactPhone: "123"},
		Supplier: &Supplier{
			Name: "供应商", EnterpriseType: "company", CreditCode: "91310000", EnterpriseScale: "small",
			AcceptingEntity: "供应商", Region: "上海", Address: "地址", SpecialNature: []string{"小微"},
			PayeeName: "收款人", BankName: "银行", BankAccount: "6222", ContactName: "李四", ContactPhone: "456",
			TaxIdentifier: "TAX-1",
		},
		Items: []DraftItem{{
			ItemID: draftItemUUID, Name: "服务器", Catalog: "server", Brand: "品牌", Specification: "规格", Unit: "台",
			Quantity: "1.000000", UnitPrice: "100.0000", BudgetAmount: "100.00", LineAmount: "100.00", TaxRate: "0.1300",
			Attributes: []Attribute{{Name: "型号", Value: "X", Unit: "台"}},
		}},
		Assets: []DraftAssetPlan{{
			AssetPlanID: draftAssetUUID, ItemID: draftItemUUID, Name: "资产", Code: "A-1", Brand: "品牌",
			Specification: "规格", Origin: "上海", Manufacturer: "厂商", Quantity: "1.000000", UnitPrice: "100.0000",
			DepartmentCode: "D1", DepartmentName: "部门", CustodianUser: "user-1", Remark: "备注",
		}},
		Funding: []DraftFunding{{
			Nature: "财政", Channel: "国库", Amount: "100.00", FundingYear: draftPtr(2026),
			CostCenter: "CC-1", QuotaNumber: "Q-1",
		}},
		Payments: []DraftPaymentPlan{{
			Sequence: 1, IsAdvance: draftPtr(false), Description: "首款", PlannedDate: "2026-02-01",
			AdvanceAmount: "0.00", UnitAmount: "100.00", FiscalAmount: "0.00", Amount: "100.00",
			Ratio: "1.000000", RatioBase: "100.00",
			Condition: &PaymentCondition{
				Type: "signed_contract", EvidenceRequirements: []string{"合同"}, PolicyRef: "policy-1",
				DueRule: &DueRule{Trigger: "contract_signed", DaysAfter: draftPtr(30), Calendar: "calendar_days", PolicyRef: "policy-2"},
			},
		}},
		DeliveryPlans: []DraftDeliveryPlan{{
			Sequence: 1, ItemID: draftItemUUID, PlannedDate: "2026-01-10", Description: "交付", Quantity: "1.000000",
		}},
		AcceptancePlans: []DraftAcceptancePlan{{
			Sequence: 1, PlannedDate: "2026-01-20", Description: "验收", Criteria: "合格", ItemIDs: []string{draftItemUUID},
		}},
		Materials: []MaterialRef{{
			MaterialID: "material-1", MaterialVersionID: "version-1", SHA256: strings.Repeat("a", 64), Purpose: "draft",
		}},
	}
}

func requireDraftInvalid(t *testing.T, name string, err error) {
	t.Helper()
	var fe *fault.Error
	if err == nil {
		t.Fatalf("%s was accepted", name)
	}
	if !errors.As(err, &fe) || fe.Status != 422 {
		t.Fatalf("%s returned %v", name, err)
	}
}

func TestValidateDraftContentAcceptsCompleteAndMinimalDrafts(t *testing.T) {
	draft := completeDraftFixture()
	draft.Contract.Name = "  采购合同  "
	draft.Contract.Number = draftPtr("  HT-001  ")
	draft.Contract.ProjectNumber = draftPtr("   ")
	if err := ValidateDraftContent(&draft); err != nil {
		t.Fatalf("complete draft: %v", err)
	}
	if draft.Contract.Name != "采购合同" || draft.Contract.Number == nil || *draft.Contract.Number != "HT-001" || draft.Contract.ProjectNumber != nil {
		t.Fatalf("draft basics were not normalized: %+v", draft.Contract)
	}
	minimal := DraftContent{Contract: &ContractBasics{}}
	if err := ValidateDraftContent(&minimal); err != nil {
		t.Fatalf("minimal draft: %v", err)
	}
	requireDraftInvalid(t, "nil draft", ValidateDraftContent(nil))
	requireDraftInvalid(t, "draft without contract", ValidateDraftContent(&DraftContent{}))
	noSupplierOrBuyer := DraftContent{Contract: &ContractBasics{Name: "合同"}}
	if err := ValidateDraftContent(&noSupplierOrBuyer); err != nil {
		t.Fatalf("draft without optional parties: %v", err)
	}
}

func TestValidateDraftContentRejectsInvalidSections(t *testing.T) {
	long := func(count int) string { return strings.Repeat("a", count) }
	cases := []struct {
		name   string
		mutate func(*DraftContent)
	}{
		{"contract.number too long", func(d *DraftContent) { d.Contract.Number = draftPtr(long(101)) }},
		{"contract.project_number too long", func(d *DraftContent) { d.Contract.ProjectNumber = draftPtr(long(101)) }},
		{"contract.name too long", func(d *DraftContent) { d.Contract.Name = long(201) }},
		{"contract.project_type too long", func(d *DraftContent) { d.Contract.ProjectType = long(101) }},
		{"contract.classification too long", func(d *DraftContent) { d.Contract.Classification = long(101) }},
		{"contract.category too long", func(d *DraftContent) { d.Contract.Category = long(101) }},
		{"contract.grade too long", func(d *DraftContent) { d.Contract.Grade = long(101) }},
		{"contract.department_id too long", func(d *DraftContent) { d.Contract.DepartmentID = long(101) }},
		{"contract.handler_user_id too long", func(d *DraftContent) { d.Contract.HandlerUserID = long(101) }},
		{"contract.handler_contact too long", func(d *DraftContent) { d.Contract.HandlerContact = long(101) }},
		{"contract.procurement_method too long", func(d *DraftContent) { d.Contract.ProcurementMethod = long(101) }},
		{"contract.performance_location too long", func(d *DraftContent) { d.Contract.PerformanceLocation = long(1001) }},
		{"contract.remark too long", func(d *DraftContent) { d.Contract.Remark = long(4001) }},
		{"contract.drafting_party invalid", func(d *DraftContent) { d.Contract.DraftingParty = "agent" }},
		{"contract.amount invalid", func(d *DraftContent) { d.Contract.Amount = "1.5" }},
		{"contract.currency invalid", func(d *DraftContent) { d.Contract.Currency = "cny" }},
		{"contract.settlement_mode too long", func(d *DraftContent) { d.Contract.SettlementMode = long(101) }},
		{"contract.signing_date invalid", func(d *DraftContent) { d.Contract.SigningDate = "2026-13-01" }},
		{"contract.performance_start_date invalid", func(d *DraftContent) { d.Contract.PerformanceStartDate = "2026-02-30" }},
		{"contract.performance_end_date invalid", func(d *DraftContent) { d.Contract.PerformanceEndDate = "not-a-date" }},
		{"contract.performance dates reversed", func(d *DraftContent) {
			d.Contract.PerformanceStartDate, d.Contract.PerformanceEndDate = "2026-03-02", "2026-03-01"
		}},
		{"contract.related_parties too many", func(d *DraftContent) {
			d.Contract.RelatedParties = make([]PartyRef, 51)
		}},
		{"related party id invalid", func(d *DraftContent) { d.Contract.RelatedParties[0].PartyID = "bad id" }},
		{"related party role invalid", func(d *DraftContent) { d.Contract.RelatedParties[0].Role = "consumer" }},
		{"related party binding invalid", func(d *DraftContent) { d.Contract.RelatedParties[0].BindingID = "bad id" }},
		{"related party name too long", func(d *DraftContent) { d.Contract.RelatedParties[0].NameSnapshot = long(201) }},
		{"procurement package system too long", func(d *DraftContent) {
			d.Contract.ProcurementResultPackage.System = long(101)
		}},
		{"procurement package result too long", func(d *DraftContent) {
			d.Contract.ProcurementResultPackage.ResultID = long(101)
		}},
		{"procurement package id too long", func(d *DraftContent) {
			d.Contract.ProcurementResultPackage.PackageID = long(101)
		}},
		{"procurement snapshot ref too long", func(d *DraftContent) {
			d.Contract.ProcurementResultPackage.SnapshotRef = long(101)
		}},
		{"buyer unit id too long", func(d *DraftContent) { d.Buyer.PurchasingUnit.ID = long(101) }},
		{"buyer unit name too long", func(d *DraftContent) { d.Buyer.PurchasingUnit.Name = long(201) }},
		{"buyer contact name too long", func(d *DraftContent) { d.Buyer.ContactName = long(101) }},
		{"buyer contact phone too long", func(d *DraftContent) { d.Buyer.ContactPhone = long(101) }},
		{"supplier enterprise type too long", func(d *DraftContent) { d.Supplier.EnterpriseType = long(101) }},
		{"supplier name too long", func(d *DraftContent) { d.Supplier.Name = long(201) }},
		{"supplier credit code too long", func(d *DraftContent) { d.Supplier.CreditCode = long(101) }},
		{"supplier scale too long", func(d *DraftContent) { d.Supplier.EnterpriseScale = long(101) }},
		{"supplier accepting entity too long", func(d *DraftContent) { d.Supplier.AcceptingEntity = long(201) }},
		{"supplier region too long", func(d *DraftContent) { d.Supplier.Region = long(201) }},
		{"supplier address too long", func(d *DraftContent) { d.Supplier.Address = long(1001) }},
		{"supplier contact name too long", func(d *DraftContent) { d.Supplier.ContactName = long(101) }},
		{"supplier contact phone too long", func(d *DraftContent) { d.Supplier.ContactPhone = long(101) }},
		{"supplier special nature too many", func(d *DraftContent) { d.Supplier.SpecialNature = make([]string, 31) }},
		{"supplier special nature item too long", func(d *DraftContent) { d.Supplier.SpecialNature = []string{long(101)} }},
		{"supplier payee too long", func(d *DraftContent) { d.Supplier.PayeeName = long(201) }},
		{"supplier bank name too long", func(d *DraftContent) { d.Supplier.BankName = long(201) }},
		{"supplier bank account too long", func(d *DraftContent) { d.Supplier.BankAccount = long(101) }},
		{"supplier tax identifier too long", func(d *DraftContent) { d.Supplier.TaxIdentifier = long(101) }},
		{"items too many", func(d *DraftContent) { d.Items = make([]DraftItem, 1001) }},
		{"item id invalid", func(d *DraftContent) { d.Items[0].ItemID = "item-1" }},
		{"item name too long", func(d *DraftContent) { d.Items[0].Name = long(201) }},
		{"item catalog too long", func(d *DraftContent) { d.Items[0].Catalog = long(101) }},
		{"item brand too long", func(d *DraftContent) { d.Items[0].Brand = long(101) }},
		{"item specification too long", func(d *DraftContent) { d.Items[0].Specification = long(1001) }},
		{"item unit too long", func(d *DraftContent) { d.Items[0].Unit = long(41) }},
		{"item quantity invalid", func(d *DraftContent) { d.Items[0].Quantity = "1.5" }},
		{"item unit price invalid", func(d *DraftContent) { d.Items[0].UnitPrice = "1.5" }},
		{"item budget amount invalid", func(d *DraftContent) { d.Items[0].BudgetAmount = "1.5" }},
		{"item line amount invalid", func(d *DraftContent) { d.Items[0].LineAmount = "1.5" }},
		{"item tax rate invalid", func(d *DraftContent) { d.Items[0].TaxRate = "13%" }},
		{"item attributes too many", func(d *DraftContent) { d.Items[0].Attributes = make([]Attribute, 51) }},
		{"item attribute name too long", func(d *DraftContent) { d.Items[0].Attributes[0].Name = long(101) }},
		{"item attribute value too long", func(d *DraftContent) { d.Items[0].Attributes[0].Value = long(1001) }},
		{"item attribute unit too long", func(d *DraftContent) { d.Items[0].Attributes[0].Unit = long(41) }},
		{"assets too many", func(d *DraftContent) { d.Assets = make([]DraftAssetPlan, 1001) }},
		{"asset plan id invalid", func(d *DraftContent) { d.Assets[0].AssetPlanID = "asset-1" }},
		{"asset item id invalid", func(d *DraftContent) { d.Assets[0].ItemID = "item-1" }},
		{"asset name too long", func(d *DraftContent) { d.Assets[0].Name = long(201) }},
		{"asset code too long", func(d *DraftContent) { d.Assets[0].Code = long(101) }},
		{"asset brand too long", func(d *DraftContent) { d.Assets[0].Brand = long(101) }},
		{"asset specification too long", func(d *DraftContent) { d.Assets[0].Specification = long(1001) }},
		{"asset origin too long", func(d *DraftContent) { d.Assets[0].Origin = long(201) }},
		{"asset manufacturer too long", func(d *DraftContent) { d.Assets[0].Manufacturer = long(201) }},
		{"asset department code too long", func(d *DraftContent) { d.Assets[0].DepartmentCode = long(101) }},
		{"asset department name too long", func(d *DraftContent) { d.Assets[0].DepartmentName = long(201) }},
		{"asset custodian too long", func(d *DraftContent) { d.Assets[0].CustodianUser = long(101) }},
		{"asset remark too long", func(d *DraftContent) { d.Assets[0].Remark = long(2001) }},
		{"asset quantity invalid", func(d *DraftContent) { d.Assets[0].Quantity = "1.5" }},
		{"asset unit price invalid", func(d *DraftContent) { d.Assets[0].UnitPrice = "1.5" }},
		{"funding too many", func(d *DraftContent) { d.Funding = make([]DraftFunding, 101) }},
		{"funding nature too long", func(d *DraftContent) { d.Funding[0].Nature = long(101) }},
		{"funding channel too long", func(d *DraftContent) { d.Funding[0].Channel = long(101) }},
		{"funding amount invalid", func(d *DraftContent) { d.Funding[0].Amount = "1.5" }},
		{"funding year too small", func(d *DraftContent) { d.Funding[0].FundingYear = draftPtr(0) }},
		{"funding year too large", func(d *DraftContent) { d.Funding[0].FundingYear = draftPtr(10000) }},
		{"funding cost center too long", func(d *DraftContent) { d.Funding[0].CostCenter = long(101) }},
		{"funding quota too long", func(d *DraftContent) { d.Funding[0].QuotaNumber = long(101) }},
		{"payments too many", func(d *DraftContent) { d.Payments = make([]DraftPaymentPlan, 1001) }},
		{"payment sequence zero", func(d *DraftContent) { d.Payments[0].Sequence = 0 }},
		{"payment sequence too large", func(d *DraftContent) { d.Payments[0].Sequence = 1001 }},
		{"payment description too long", func(d *DraftContent) { d.Payments[0].Description = long(2001) }},
		{"payment date invalid", func(d *DraftContent) { d.Payments[0].PlannedDate = "2026-02-31" }},
		{"payment advance amount invalid", func(d *DraftContent) { d.Payments[0].AdvanceAmount = "1.5" }},
		{"payment unit amount invalid", func(d *DraftContent) { d.Payments[0].UnitAmount = "1.5" }},
		{"payment fiscal amount invalid", func(d *DraftContent) { d.Payments[0].FiscalAmount = "1.5" }},
		{"payment amount invalid", func(d *DraftContent) { d.Payments[0].Amount = "1.5" }},
		{"payment ratio invalid", func(d *DraftContent) { d.Payments[0].Ratio = "1.5" }},
		{"payment ratio base too long", func(d *DraftContent) { d.Payments[0].RatioBase = long(201) }},
		{"payment condition type invalid", func(d *DraftContent) { d.Payments[0].Condition.Type = "on_delivery" }},
		{"payment evidence requirements too many", func(d *DraftContent) {
			d.Payments[0].Condition.EvidenceRequirements = make([]string, 31)
		}},
		{"payment evidence requirement too long", func(d *DraftContent) {
			d.Payments[0].Condition.EvidenceRequirements = []string{long(101)}
		}},
		{"payment condition policy ref too long", func(d *DraftContent) { d.Payments[0].Condition.PolicyRef = long(101) }},
		{"due rule trigger invalid", func(d *DraftContent) { d.Payments[0].Condition.DueRule.Trigger = "whenever" }},
		{"due rule days negative", func(d *DraftContent) { d.Payments[0].Condition.DueRule.DaysAfter = draftPtr(-1) }},
		{"due rule days too large", func(d *DraftContent) { d.Payments[0].Condition.DueRule.DaysAfter = draftPtr(36501) }},
		{"due rule calendar invalid", func(d *DraftContent) { d.Payments[0].Condition.DueRule.Calendar = "lunar_days" }},
		{"due rule policy ref too long", func(d *DraftContent) { d.Payments[0].Condition.DueRule.PolicyRef = long(101) }},
		{"delivery plans too many", func(d *DraftContent) { d.DeliveryPlans = make([]DraftDeliveryPlan, 1001) }},
		{"delivery sequence zero", func(d *DraftContent) { d.DeliveryPlans[0].Sequence = 0 }},
		{"delivery item id invalid", func(d *DraftContent) { d.DeliveryPlans[0].ItemID = "item-1" }},
		{"delivery date invalid", func(d *DraftContent) { d.DeliveryPlans[0].PlannedDate = "2026-13-01" }},
		{"delivery description too long", func(d *DraftContent) { d.DeliveryPlans[0].Description = long(2001) }},
		{"delivery quantity invalid", func(d *DraftContent) { d.DeliveryPlans[0].Quantity = "1.5" }},
		{"acceptance plans too many", func(d *DraftContent) { d.AcceptancePlans = make([]DraftAcceptancePlan, 1001) }},
		{"acceptance sequence zero", func(d *DraftContent) { d.AcceptancePlans[0].Sequence = 0 }},
		{"acceptance date invalid", func(d *DraftContent) { d.AcceptancePlans[0].PlannedDate = "tomorrow" }},
		{"acceptance description too long", func(d *DraftContent) { d.AcceptancePlans[0].Description = long(2001) }},
		{"acceptance criteria too long", func(d *DraftContent) { d.AcceptancePlans[0].Criteria = long(4001) }},
		{"acceptance item ids too many", func(d *DraftContent) { d.AcceptancePlans[0].ItemIDs = make([]string, 1001) }},
		{"acceptance item id invalid", func(d *DraftContent) { d.AcceptancePlans[0].ItemIDs = []string{"item-1"} }},
		{"materials too many", func(d *DraftContent) { d.Materials = make([]MaterialRef, 501) }},
		{"material id invalid", func(d *DraftContent) { d.Materials[0].MaterialID = "bad id" }},
		{"material version invalid", func(d *DraftContent) { d.Materials[0].MaterialVersionID = "" }},
		{"material digest invalid", func(d *DraftContent) { d.Materials[0].SHA256 = "ABC" }},
		{"material purpose invalid", func(d *DraftContent) { d.Materials[0].Purpose = "archive" }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			draft := completeDraftFixture()
			testCase.mutate(&draft)
			requireDraftInvalid(t, testCase.name, ValidateDraftContent(&draft))
		})
	}
}

func TestDraftFormatHelpersEnforceGrammar(t *testing.T) {
	if err := ValidateDraftText("field", strings.Repeat("汉", 4), 4); err != nil {
		t.Fatalf("text at the rune limit: %v", err)
	}
	requireDraftInvalid(t, "text over the rune limit", ValidateDraftText("field", strings.Repeat("汉", 5), 4))
	if err := ValidateDraftDate("field", "2026-02-28"); err != nil {
		t.Fatalf("valid date: %v", err)
	}
	if err := ValidateDraftDate("field", ""); err != nil {
		t.Fatalf("empty date: %v", err)
	}
	requireDraftInvalid(t, "date with time", ValidateDraftDate("field", "2026-02-28T00:00:00Z"))
	if err := ValidateDraftMoney("field", "0.00"); err != nil {
		t.Fatalf("valid money: %v", err)
	}
	if err := ValidateDraftMoney("field", ""); err != nil {
		t.Fatalf("empty money: %v", err)
	}
	requireDraftInvalid(t, "money without cents", ValidateDraftMoney("field", "1"))
	requireDraftInvalid(t, "negative money", ValidateDraftMoney("field", "-1.00"))

	raw, err := NewContractUUID()
	if err != nil || !ValidContractUUID(raw) {
		t.Fatalf("new UUID %q (%v)", raw, err)
	}
	if ValidContractUUID("11111111-1111-1111-1111-11111111111") {
		t.Fatal("truncated UUID was accepted")
	}
	normalized, err := NormalizeContractUUID(strings.ToUpper(draftItemUUID))
	if err != nil || normalized != draftItemUUID {
		t.Fatalf("normalize UUID: %q %v", normalized, err)
	}
	var fe *fault.Error
	if _, err := NormalizeContractUUID("item-1"); !errors.As(err, &fe) || fe.Status != 404 {
		t.Fatalf("invalid UUID returned %v", err)
	}
	if value := OptionalText("  "); value != nil {
		t.Fatalf("blank optional text kept %q", *value)
	}
	if value := OptionalText(" x "); value == nil || *value != "x" {
		t.Fatalf("optional text is %v", value)
	}
	if trimOptionalText(nil) != nil {
		t.Fatal("nil optional text was materialized")
	}
	for _, value := range []string{"", strings.Repeat("a", 129), ".abc", "a b", "a#b"} {
		if validDraftIdentifier(value) {
			t.Fatalf("identifier %q was accepted", value)
		}
	}
	for _, value := range []string{"a", "A1", "a.b", "a_b", "a:b", "a/b", "a-b", strings.Repeat("a", 128)} {
		if !validDraftIdentifier(value) {
			t.Fatalf("identifier %q was rejected", value)
		}
	}
	material := MaterialRef{MaterialID: "material-1", MaterialVersionID: "version-1", SHA256: strings.Repeat("a", 64), Purpose: "payment"}
	if err := ValidateMaterialRef(&material); err != nil {
		t.Fatalf("valid material reference: %v", err)
	}
	material.Purpose = "archive"
	requireDraftInvalid(t, "material reference purpose", ValidateMaterialRef(&material))
}

func TestSanitizeDraftReferencesStripsServerManagedSnapshots(t *testing.T) {
	SanitizeDraftReferences(nil)
	SanitizeDraftReferences(&DraftContent{})
	parties := []PartyRef{{PartyID: "party-1", Role: "buyer", ReferenceSnapshot: &PartyReferenceSnapshot{BindingID: "binding-1"}}}
	procurement := &ProcurementRef{System: "erp", SourceSnapshot: &ProcurementSourceSnapshot{MaterialID: "m1"}}
	draft := DraftContent{
		Contract: &ContractBasics{RelatedParties: parties, ProcurementResultPackage: procurement},
	}
	SanitizeDraftReferences(&draft)
	if draft.Contract.RelatedParties[0].ReferenceSnapshot != nil || draft.Contract.ProcurementResultPackage.SourceSnapshot != nil {
		t.Fatalf("server-managed snapshots survived sanitizing: %+v", draft.Contract)
	}
	if parties[0].ReferenceSnapshot == nil || procurement.SourceSnapshot == nil {
		t.Fatal("sanitizing mutated the caller's backing arrays")
	}
	if draft.Contract.RelatedParties[0].BindingID != "" {
		t.Fatal("sanitizing dropped the client binding hint")
	}
}

func TestContractRoleAndSourceReferenceRules(t *testing.T) {
	cases := map[string]struct {
		contractRole string
		allowed      []string
		denied       []string
	}{
		"buyer":             {"buyer", []string{"customer"}, []string{"supplier", "contractor"}},
		"supplier":          {"supplier", []string{"supplier"}, []string{"customer", "other"}},
		"guarantor":         {"guarantor", []string{"contractor", "service_provider", "other"}, []string{"customer", "supplier"}},
		"consortium member": {"consortium_member", []string{"supplier", "contractor", "service_provider", "other"}, []string{"customer"}},
		"other":             {"other", []string{"customer", "supplier", "contractor", "service_provider", "other"}, nil},
		"unknown":           {"agent", nil, []string{"customer", "supplier", "other"}},
	}
	for name, testCase := range cases {
		for _, role := range testCase.allowed {
			if !CaseRoleAllowsContractRole(testCase.contractRole, role) {
				t.Fatalf("%s contract role rejected case role %s", name, role)
			}
		}
		for _, role := range testCase.denied {
			if CaseRoleAllowsContractRole(testCase.contractRole, role) {
				t.Fatalf("%s contract role accepted case role %s", name, role)
			}
		}
	}
	if !ProcurementMaterialRole("procurement") || !ProcurementMaterialRole("award") || ProcurementMaterialRole("acceptance") {
		t.Fatal("procurement material roles are wrong")
	}

	source := SourceRef{System: "  erp  ", ID: "  C-1  ", SourceVersion: "  v1  "}
	if err := ValidateSourceRef(&source); err != nil || source.System != "erp" || source.ID != "C-1" || source.SourceVersion != "v1" {
		t.Fatalf("source ref: %+v %v", source, err)
	}
	for name, ref := range map[string]SourceRef{
		"empty system":    {},
		"system too long": {System: strings.Repeat("a", 101), ID: "C-1", SourceVersion: "v1"},
		"empty id":        {System: "erp", SourceVersion: "v1"},
		"id too long":     {System: "erp", ID: strings.Repeat("a", 201), SourceVersion: "v1"},
		"empty version":   {System: "erp", ID: "C-1"},
		"version long":    {System: "erp", ID: "C-1", SourceVersion: strings.Repeat("a", 101)},
	} {
		copy := ref
		requireDraftInvalid(t, name, ValidateSourceRef(&copy))
	}
}

func TestStoredDraftDecodingAndSummaryFacts(t *testing.T) {
	raw := []byte(`{"contract":{"name":"合同","amount":"10.00","currency":"CNY"},"items":[]}`)
	name, amount, currency, err := DraftSummaryFacts(raw)
	if err != nil || name != "合同" || amount == nil || *amount != "10.00" || currency == nil || *currency != "CNY" {
		t.Fatalf("summary facts: %q %v %v %v", name, amount, currency, err)
	}
	if _, _, _, err := DraftSummaryFacts([]byte(`{"items":[]}`)); err == nil {
		t.Fatal("summary facts accepted a draft without contract")
	}
	if _, _, _, err := DraftSummaryFacts([]byte(`not json`)); err == nil {
		t.Fatal("summary facts accepted malformed JSON")
	}
	if _, _, _, err := DraftSummaryFacts([]byte(`{"contract":{"name":"a"}} {}`)); err == nil {
		t.Fatal("summary facts accepted two stored objects")
	}
	var decoded DraftContent
	if err := UnmarshalStoredDraft(raw, &decoded); err != nil || decoded.Contract == nil || decoded.Contract.Name != "合同" {
		t.Fatalf("stored draft decode: %+v %v", decoded, err)
	}
	if err := UnmarshalStoredDraft([]byte(`{"contract":{"name":"a"},"unknown":1}`), &decoded); err == nil {
		t.Fatal("stored draft decode accepted an unknown field")
	}
}

func TestCLMRequestIdentityHelpersAreCanonical(t *testing.T) {
	for _, key := range []string{strings.Repeat("k", 8), strings.Repeat("k", 128), "key-1234"} {
		if err := ValidateCLMIdempotencyKey(key); err != nil {
			t.Fatalf("idempotency key %q: %v", key, err)
		}
	}
	for name, key := range map[string]string{
		"too short":   "short",
		"too long":    strings.Repeat("k", 129),
		"space":       "key with space",
		"non-ascii":   "键-12345678",
		"control tab": "key\t12345",
	} {
		requireDraftInvalid(t, "idempotency "+name, ValidateCLMIdempotencyKey(key))
	}
	payload := map[string]string{"b": "2", "a": "1"}
	first, err := RequestDigest("contract", "POST", "/api/contracts", 2, payload)
	if err != nil || len(first) != 64 {
		t.Fatalf("request digest %q %v", first, err)
	}
	second, err := RequestDigest("contract", "POST", "/api/contracts", 2, map[string]string{"a": "1", "b": "2"})
	if err != nil || second != first {
		t.Fatalf("canonical request digest %q vs %q (%v)", second, first, err)
	}
	changed, err := RequestDigest("contract", "POST", "/api/contracts", 3, payload)
	if err != nil || changed == first {
		t.Fatalf("row version did not change the digest: %q %v", changed, err)
	}
	if _, err := RequestDigest("contract", "POST", "/api/contracts", 2, func() {}); err == nil {
		t.Fatal("unmarshalable payload produced a digest")
	}
}

func finalAmountDraftFixture() DraftContent {
	return DraftContent{
		Contract: &ContractBasics{
			SettlementMode: "fixed_total", Currency: "CNY", Amount: "100.00", PricesIncludeTax: draftPtr(true),
		},
		Items: []DraftItem{{
			ItemID: draftItemUUID, Quantity: "1.000000", UnitPrice: "100.0000", LineAmount: "100.00",
		}},
		Funding: []DraftFunding{{Amount: "100.00", FundingYear: draftPtr(2026)}},
		Payments: []DraftPaymentPlan{{
			Sequence: 1, Amount: "100.00", AdvanceAmount: "0.00", UnitAmount: "100.00", FiscalAmount: "0.00",
		}},
	}
}

func TestValidateDraftAmountsReconcilesAtFinalAndKeepsDraftsPartial(t *testing.T) {
	draft := finalAmountDraftFixture()
	if err := ValidateDraftAmounts(&draft, contractmoney.StageFinal); err != nil {
		t.Fatalf("reconciled final draft: %v", err)
	}
	amounts, conflicting := ContractAmountsFromDraft(&draft, contractmoney.StageFinal)
	if conflicting || amounts.Mode != contractmoney.SettlementFixedTotal || amounts.Currency != "CNY" ||
		amounts.Amount != "100.00" || len(amounts.Items) != 1 || len(amounts.Funding) != 1 ||
		amounts.Funding[0].FiscalYear != "2026" || len(amounts.Payments) != 1 || amounts.Payments[0].IsAdvance {
		t.Fatalf("draft amounts mapping is %+v", amounts)
	}
	draft.Contract.SettlementMode = "framework"
	amounts, _ = ContractAmountsFromDraft(&draft, contractmoney.StageDraft)
	if amounts.Mode != contractmoney.SettlementFramework {
		t.Fatalf("framework mapping is %+v", amounts)
	}
	draft.Contract.SettlementMode = "by_quantity"
	amounts, _ = ContractAmountsFromDraft(&draft, contractmoney.StageDraft)
	if amounts.Mode != contractmoney.SettlementByQuantity {
		t.Fatalf("by-quantity mapping is %+v", amounts)
	}
	draft.Contract.SettlementMode = "unknown"
	amounts, _ = ContractAmountsFromDraft(&draft, contractmoney.StageDraft)
	if amounts.Mode != "" {
		t.Fatalf("unknown mode mapped to %q", amounts.Mode)
	}
	isAdvance := true
	draft.Payments[0].IsAdvance = &isAdvance
	amounts, _ = ContractAmountsFromDraft(&draft, contractmoney.StageDraft)
	if !amounts.Payments[0].IsAdvance {
		t.Fatal("advance classification was dropped")
	}
	empty, conflicting := ContractAmountsFromDraft(nil, contractmoney.StageDraft)
	if conflicting || empty.Mode != "" || empty.Amount != "" {
		t.Fatalf("nil draft mapped to %+v", empty)
	}

	invalid := finalAmountDraftFixture()
	invalid.Payments[0].Amount = "90.00"
	err := ValidateDraftAmounts(&invalid, contractmoney.StageFinal)
	requireDraftInvalid(t, "payment total mismatch", err)
	if status := amountFaultStatus(err); status != 422 {
		t.Fatalf("amount rejection status is %d", status)
	}
	if amountFaultStatus(errors.New("other")) != 0 {
		t.Fatal("non-fault error gained a status")
	}
	if err := ValidateDraftAmounts(&invalid, contractmoney.StageDraft); err != nil {
		t.Fatalf("partial draft must stay legal: %v", err)
	}

	conflictingBases := finalAmountDraftFixture()
	conflictingBases.Payments = []DraftPaymentPlan{
		{Sequence: 1, Amount: "40.00", AdvanceAmount: "0.00", UnitAmount: "40.00", FiscalAmount: "0.00", Ratio: "0.400000", RatioBase: "100.00"},
		{Sequence: 2, Amount: "60.00", AdvanceAmount: "0.00", UnitAmount: "60.00", FiscalAmount: "0.00", Ratio: "0.600000", RatioBase: "120.00"},
	}
	requireDraftInvalid(t, "distinct ratio bases at final", ValidateDraftAmounts(&conflictingBases, contractmoney.StageFinal))
	if err := ValidateDraftAmounts(&conflictingBases, contractmoney.StageDraft); err != nil {
		t.Fatalf("draft with free-form ratio bases must stay legal: %v", err)
	}
	malformedBase := finalAmountDraftFixture()
	malformedBase.Payments[0].RatioBase = "100.5"
	requireDraftInvalid(t, "malformed ratio base at final", ValidateDraftAmounts(&malformedBase, contractmoney.StageFinal))
	if err := ValidateDraftAmounts(&malformedBase, contractmoney.StageDraft); err != nil {
		t.Fatalf("draft with malformed ratio base must stay legal: %v", err)
	}

	many := finalAmountDraftFixture()
	many.Payments = make([]DraftPaymentPlan, 9)
	for index := range many.Payments {
		many.Payments[index].Sequence = 0
	}
	truncated := ValidateDraftAmounts(&many, contractmoney.StageFinal)
	requireDraftInvalid(t, "many amount violations", truncated)
	if !strings.Contains(truncated.Error(), "从略") {
		t.Fatalf("aggregated message was not truncated: %v", truncated)
	}
	if LegacyAmountsKnown(nil) || LegacyAmountsKnown(&DraftContent{}) ||
		LegacyAmountsKnown(&DraftContent{Contract: &ContractBasics{Amount: "1.00"}}) ||
		!LegacyAmountsKnown(&DraftContent{Contract: &ContractBasics{Amount: "1.00", Currency: "CNY"}}) {
		t.Fatal("legacy amount/currency pair detection is wrong")
	}
}
