package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"contractaudit/internal/fault"
)

// DraftContent is the typed, strict contract content of one editable draft or
// frozen revision snapshot. The design pack (docs/04, OpenAPI DraftContent)
// allows incomplete drafts; only formats are enforced here while presence
// rules belong to the submission command. Unknown properties are rejected by
// the strict HTTP decoder, and no field is a free-form map, so a value cannot
// enter storage without matching this shape.
type DraftContent struct {
	Contract        *ContractBasics       `json:"contract"`
	Buyer           *Buyer                `json:"buyer,omitempty"`
	Supplier        *Supplier             `json:"supplier,omitempty"`
	Items           []DraftItem           `json:"items,omitempty"`
	Assets          []DraftAssetPlan      `json:"assets,omitempty"`
	Funding         []DraftFunding        `json:"funding,omitempty"`
	Payments        []DraftPaymentPlan    `json:"payments,omitempty"`
	DeliveryPlans   []DraftDeliveryPlan   `json:"delivery_plans,omitempty"`
	AcceptancePlans []DraftAcceptancePlan `json:"acceptance_plans,omitempty"`
	Materials       []MaterialRef         `json:"materials,omitempty"`
}

type ContractBasics struct {
	ProjectNumber            *string         `json:"project_number,omitempty"`
	ProjectType              string          `json:"project_type,omitempty"`
	Name                     string          `json:"name,omitempty"`
	Number                   *string         `json:"number,omitempty"`
	Classification           string          `json:"classification,omitempty"`
	Category                 string          `json:"category,omitempty"`
	Grade                    string          `json:"grade,omitempty"`
	DraftingParty            string          `json:"drafting_party,omitempty"`
	DepartmentID             string          `json:"department_id,omitempty"`
	HandlerUserID            string          `json:"handler_user_id,omitempty"`
	HandlerContact           string          `json:"handler_contact,omitempty"`
	RelatedParties           []PartyRef      `json:"related_parties,omitempty"`
	Amount                   string          `json:"amount,omitempty"`
	Currency                 string          `json:"currency,omitempty"`
	SettlementMode           string          `json:"settlement_mode,omitempty"`
	PricesIncludeTax         *bool           `json:"prices_include_tax,omitempty"`
	IsPPPProject             *bool           `json:"is_ppp_project,omitempty"`
	IsNDRCEngineeringProject *bool           `json:"is_ndrc_engineering_project,omitempty"`
	IsFixedAsset             *bool           `json:"is_fixed_asset,omitempty"`
	ProcurementResultPackage *ProcurementRef `json:"procurement_result_package,omitempty"`
	ProcurementMethod        string          `json:"procurement_method,omitempty"`
	SigningDate              string          `json:"signing_date,omitempty"`
	PerformanceStartDate     string          `json:"performance_start_date,omitempty"`
	PerformanceEndDate       string          `json:"performance_end_date,omitempty"`
	PerformanceLocation      string          `json:"performance_location,omitempty"`
	BusinessTermsVerified    *bool           `json:"business_terms_verified,omitempty"`
	PerformanceBondRequired  *bool           `json:"performance_bond_required,omitempty"`
	Remark                   string          `json:"remark,omitempty"`
}

type Buyer struct {
	PurchasingUnit *UnitRef `json:"purchasing_unit,omitempty"`
	ContactName    string   `json:"contact_name,omitempty"`
	ContactPhone   string   `json:"contact_phone,omitempty"`
}

type Supplier struct {
	EnterpriseType  string   `json:"enterprise_type,omitempty"`
	Name            string   `json:"name,omitempty"`
	CreditCode      string   `json:"credit_code,omitempty"`
	EnterpriseScale string   `json:"enterprise_scale,omitempty"`
	AcceptingEntity string   `json:"accepting_entity,omitempty"`
	Region          string   `json:"region,omitempty"`
	Address         string   `json:"address,omitempty"`
	SpecialNature   []string `json:"special_nature,omitempty"`
	PayeeName       string   `json:"payee_name,omitempty"`
	BankName        string   `json:"bank_name,omitempty"`
	BankAccount     string   `json:"bank_account,omitempty"`
	ContactName     string   `json:"contact_name,omitempty"`
	ContactPhone    string   `json:"contact_phone,omitempty"`
	TaxIdentifier   string   `json:"tax_identifier,omitempty"`
}

type DraftItem struct {
	ItemID        string      `json:"item_id"`
	Name          string      `json:"name,omitempty"`
	Catalog       string      `json:"catalog,omitempty"`
	Brand         string      `json:"brand,omitempty"`
	Specification string      `json:"specification,omitempty"`
	Attributes    []Attribute `json:"attributes,omitempty"`
	Quantity      string      `json:"quantity,omitempty"`
	Unit          string      `json:"unit,omitempty"`
	UnitPrice     string      `json:"unit_price,omitempty"`
	BudgetAmount  string      `json:"budget_amount,omitempty"`
	LineAmount    string      `json:"line_amount,omitempty"`
	TaxRate       string      `json:"tax_rate,omitempty"`
}

type DraftAssetPlan struct {
	AssetPlanID    string `json:"asset_plan_id"`
	ItemID         string `json:"item_id,omitempty"`
	Name           string `json:"name,omitempty"`
	Code           string `json:"code,omitempty"`
	Brand          string `json:"brand,omitempty"`
	Specification  string `json:"specification,omitempty"`
	Origin         string `json:"origin,omitempty"`
	Manufacturer   string `json:"manufacturer,omitempty"`
	Quantity       string `json:"quantity,omitempty"`
	UnitPrice      string `json:"unit_price,omitempty"`
	DepartmentCode string `json:"department_code,omitempty"`
	DepartmentName string `json:"department_name,omitempty"`
	CustodianUser  string `json:"custodian_user_id,omitempty"`
	Remark         string `json:"remark,omitempty"`
}

type DraftFunding struct {
	Nature      string `json:"nature,omitempty"`
	Channel     string `json:"channel,omitempty"`
	Amount      string `json:"amount,omitempty"`
	FundingYear *int   `json:"funding_year,omitempty"`
	CostCenter  string `json:"cost_center,omitempty"`
	QuotaNumber string `json:"quota_number,omitempty"`
}

type DraftPaymentPlan struct {
	Sequence      int               `json:"sequence"`
	IsAdvance     *bool             `json:"is_advance,omitempty"`
	Description   string            `json:"description,omitempty"`
	PlannedDate   string            `json:"planned_date,omitempty"`
	AdvanceAmount string            `json:"advance_amount,omitempty"`
	UnitAmount    string            `json:"unit_amount,omitempty"`
	FiscalAmount  string            `json:"fiscal_amount,omitempty"`
	Amount        string            `json:"amount,omitempty"`
	Ratio         string            `json:"ratio,omitempty"`
	RatioBase     string            `json:"ratio_base,omitempty"`
	Condition     *PaymentCondition `json:"condition,omitempty"`
}

type DraftDeliveryPlan struct {
	Sequence    int    `json:"sequence"`
	ItemID      string `json:"item_id,omitempty"`
	PlannedDate string `json:"planned_date,omitempty"`
	Description string `json:"description,omitempty"`
	Quantity    string `json:"quantity,omitempty"`
}

type DraftAcceptancePlan struct {
	Sequence    int      `json:"sequence"`
	PlannedDate string   `json:"planned_date,omitempty"`
	Description string   `json:"description,omitempty"`
	Criteria    string   `json:"criteria,omitempty"`
	ItemIDs     []string `json:"item_ids,omitempty"`
}

type PaymentCondition struct {
	Type                 string   `json:"type"`
	EvidenceRequirements []string `json:"evidence_requirements,omitempty"`
	DueRule              *DueRule `json:"due_rule,omitempty"`
	PolicyRef            string   `json:"policy_ref,omitempty"`
}

type DueRule struct {
	Trigger   string `json:"trigger"`
	DaysAfter *int   `json:"days_after,omitempty"`
	Calendar  string `json:"calendar,omitempty"`
	PolicyRef string `json:"policy_ref,omitempty"`
}

// PartyReferenceSnapshot is the server-captured binding of one contract party
// to the source case's subject chain. Clients may read it but never author it:
// draft writes strip it and strict resolution recomputes it from the source
// case. The pin makes a frozen contract keep pointing at the version that was
// verified even after the case binds the subject to a newer version.
type PartyReferenceSnapshot struct {
	BindingID          string    `json:"binding_id"`
	BindingVersion     int64     `json:"binding_version"`
	CaseRole           string    `json:"case_role"`
	BindingState       string    `json:"binding_state"`
	SubjectID          string    `json:"subject_id"`
	SubjectVersion     int64     `json:"subject_version"`
	SubjectPayloadHash string    `json:"subject_payload_hash"`
	VerificationState  string    `json:"verification_state"`
	DisplayName        string    `json:"display_name"`
	CapturedAt         time.Time `json:"captured_at"`
}

type PartyRef struct {
	PartyID      string `json:"party_id"`
	Role         string `json:"role"`
	NameSnapshot string `json:"name_snapshot"`
	// BindingID is an optional client hint selecting one case binding. It is
	// revalidated against the source case and canonicalized on freeze.
	BindingID string `json:"binding_id,omitempty"`
	// ReferenceSnapshot is server-managed. Draft and import writers clear any
	// client-supplied value before validation and sealing.
	ReferenceSnapshot *PartyReferenceSnapshot `json:"reference_snapshot,omitempty"`
}

type UnitRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ProcurementSourceSnapshot pins the material version that evidenced a
// procurement result. It intentionally stores no filename or media type:
// frozen contract content is readable by every contract viewer, while the
// document metadata stays behind the source-case read check.
type ProcurementSourceSnapshot struct {
	System            string    `json:"system"`
	ResultID          string    `json:"result_id"`
	PackageID         string    `json:"package_id"`
	SnapshotRef       string    `json:"snapshot_ref"`
	MaterialID        string    `json:"material_id"`
	MaterialVersionID string    `json:"material_version_id"`
	MaterialVersion   int       `json:"material_version"`
	MaterialRole      string    `json:"material_role"`
	SHA256            string    `json:"sha256"`
	CapturedAt        time.Time `json:"captured_at"`
}

type ProcurementRef struct {
	System      string `json:"system"`
	ResultID    string `json:"result_id"`
	PackageID   string `json:"package_id"`
	SnapshotRef string `json:"snapshot_ref"`
	// SourceSnapshot is server-managed. Draft and import writers clear any
	// client-supplied value before validation and sealing.
	SourceSnapshot *ProcurementSourceSnapshot `json:"source_snapshot,omitempty"`
}

type Attribute struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Unit  string `json:"unit,omitempty"`
}

// MaterialRef cites one existing material version by its document identity.
// material_version_id carries the material version's document ID, which the
// document service keeps globally unique; the digest is verified against
// documents.digest before any record may cite it.
type MaterialRef struct {
	MaterialID        string `json:"material_id"`
	MaterialVersionID string `json:"material_version_id"`
	SHA256            string `json:"sha256"`
	Purpose           string `json:"purpose"`
}

type SourceRef struct {
	System        string `json:"system"`
	ID            string `json:"id"`
	SourceVersion string `json:"source_version"`
}

var (
	moneyPattern        = regexp.MustCompile(`^(0|[1-9][0-9]{0,15})\.[0-9]{2}$`)
	unitPricePattern    = regexp.MustCompile(`^(0|[1-9][0-9]{0,13})\.[0-9]{4}$`)
	quantityPattern     = regexp.MustCompile(`^(0|[1-9][0-9]{0,11})\.[0-9]{6}$`)
	ratioPattern        = regexp.MustCompile(`^(0\.[0-9]{6}|1\.000000)$`)
	taxRatePattern      = regexp.MustCompile(`^(0\.[0-9]{4}|1\.0000)$`)
	currencyPattern     = regexp.MustCompile(`^[A-Z]{3}$`)
	digestPattern       = regexp.MustCompile(`^[a-f0-9]{64}$`)
	contractUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

var draftingPartyValues = map[string]bool{"buyer": true, "supplier": true, "joint": true, "other": true}
var partyRoleValues = map[string]bool{
	"buyer": true, "supplier": true, "guarantor": true, "consortium_member": true, "other": true,
}
var materialPurposeValues = map[string]bool{
	"draft": true, "signed_contract": true, "supporting": true,
	"acceptance": true, "payment": true, "other": true,
}
var paymentConditionTypeValues = map[string]bool{
	"signed_contract": true, "all_goods_accepted": true, "partial_acceptance": true,
	"service_period_verified": true, "custom_policy": true,
}
var dueRuleTriggerValues = map[string]bool{
	"fixed_date": true, "contract_signed": true, "acceptance_passed": true,
	"invoice_verified": true, "other_policy_event": true,
}
var dueRuleCalendarValues = map[string]bool{"calendar_days": true, "business_days": true}

// newContractUUID mints a random RFC 4122 version 4 identifier without adding
// a dependency to the module lock file.
func newContractUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func validContractUUID(value string) bool {
	return contractUUIDPattern.MatchString(value)
}

func normalizeContractUUID(value string) (string, error) {
	if !validContractUUID(value) {
		return "", fault.NotFound("没有找到对应记录")
	}
	return strings.ToLower(value), nil
}

func optionalText(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func validateDraftText(field, value string, max int) error {
	if runeCount(value) > max {
		return fault.Invalid(field + "超过最大长度")
	}
	return nil
}

func validateDraftDate(field, value string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return fault.Invalid(field + "必须是有效的日期")
	}
	return nil
}

func validateDraftMoney(field, value string) error {
	if value == "" {
		return nil
	}
	if !moneyPattern.MatchString(value) {
		return fault.Invalid(field + "必须是两位小数的非负金额字符串")
	}
	return nil
}

func validateDraftQuantity(field, value string) error {
	if value == "" {
		return nil
	}
	if !quantityPattern.MatchString(value) {
		return fault.Invalid(field + "必须是六位小数的数量字符串")
	}
	return nil
}

func validateDraftUnitPrice(field, value string) error {
	if value == "" {
		return nil
	}
	if !unitPricePattern.MatchString(value) {
		return fault.Invalid(field + "必须是四位小数的非负单价字符串")
	}
	return nil
}

// validateDraftContent enforces the draft-stage format contract. Missing
// fields stay missing; submit-time completeness is a separate command.
func validateDraftContent(draft *DraftContent) error {
	if draft == nil || draft.Contract == nil {
		return fault.Invalid("草稿必须包含 contract 对象")
	}
	basics := draft.Contract
	basics.Name = strings.TrimSpace(basics.Name)
	basics.Number = trimOptionalText(basics.Number)
	basics.ProjectNumber = trimOptionalText(basics.ProjectNumber)
	if err := validateDraftText("contract.name", basics.Name, 200); err != nil {
		return err
	}
	if basics.Number != nil {
		if err := validateDraftText("contract.number", *basics.Number, 100); err != nil {
			return err
		}
	}
	if basics.ProjectNumber != nil {
		if err := validateDraftText("contract.project_number", *basics.ProjectNumber, 100); err != nil {
			return err
		}
	}
	for _, check := range []struct {
		field, value string
		max          int
	}{
		{"contract.project_type", basics.ProjectType, 100},
		{"contract.classification", basics.Classification, 100},
		{"contract.category", basics.Category, 100},
		{"contract.grade", basics.Grade, 100},
		{"contract.department_id", basics.DepartmentID, 100},
		{"contract.handler_user_id", basics.HandlerUserID, 100},
		{"contract.handler_contact", basics.HandlerContact, 100},
		{"contract.procurement_method", basics.ProcurementMethod, 100},
		{"contract.performance_location", basics.PerformanceLocation, 1000},
		{"contract.remark", basics.Remark, 4000},
	} {
		if err := validateDraftText(check.field, check.value, check.max); err != nil {
			return err
		}
	}
	if basics.DraftingParty != "" && !draftingPartyValues[basics.DraftingParty] {
		return fault.Invalid("contract.drafting_party 不是有效取值")
	}
	if err := validateDraftMoney("contract.amount", basics.Amount); err != nil {
		return err
	}
	if basics.Currency != "" && !currencyPattern.MatchString(basics.Currency) {
		return fault.Invalid("contract.currency 必须是三位大写币种")
	}
	if err := validateDraftText("contract.settlement_mode", basics.SettlementMode, 100); err != nil {
		return err
	}
	if err := validateDraftDate("contract.signing_date", basics.SigningDate); err != nil {
		return err
	}
	if err := validateDraftDate("contract.performance_start_date", basics.PerformanceStartDate); err != nil {
		return err
	}
	if err := validateDraftDate("contract.performance_end_date", basics.PerformanceEndDate); err != nil {
		return err
	}
	if basics.PerformanceStartDate != "" && basics.PerformanceEndDate != "" &&
		basics.PerformanceEndDate < basics.PerformanceStartDate {
		return fault.Invalid("contract.performance_end_date 不能早于开始日期")
	}
	if len(basics.RelatedParties) > 50 {
		return fault.Invalid("contract.related_parties 最多 50 项")
	}
	for _, party := range basics.RelatedParties {
		if !validIdentifier(party.PartyID) || runeCount(party.PartyID) > 100 {
			return fault.Invalid("related_parties.party_id 无效")
		}
		if !partyRoleValues[party.Role] {
			return fault.Invalid("related_parties.role 不是有效取值")
		}
		if party.BindingID != "" && (!validIdentifier(party.BindingID) || runeCount(party.BindingID) > 100) {
			return fault.Invalid("related_parties.binding_id 无效")
		}
		if err := validateDraftText("related_parties.name_snapshot", party.NameSnapshot, 200); err != nil {
			return err
		}
	}
	if ref := basics.ProcurementResultPackage; ref != nil {
		for _, check := range []struct{ field, value string }{
			{"procurement_result_package.system", ref.System},
			{"procurement_result_package.result_id", ref.ResultID},
			{"procurement_result_package.package_id", ref.PackageID},
			{"procurement_result_package.snapshot_ref", ref.SnapshotRef},
		} {
			if err := validateDraftText(check.field, check.value, 100); err != nil {
				return err
			}
		}
	}
	if draft.Buyer != nil {
		if unit := draft.Buyer.PurchasingUnit; unit != nil {
			if err := validateDraftText("buyer.purchasing_unit.id", unit.ID, 100); err != nil {
				return err
			}
			if err := validateDraftText("buyer.purchasing_unit.name", unit.Name, 200); err != nil {
				return err
			}
		}
		if err := validateDraftText("buyer.contact_name", draft.Buyer.ContactName, 100); err != nil {
			return err
		}
		if err := validateDraftText("buyer.contact_phone", draft.Buyer.ContactPhone, 100); err != nil {
			return err
		}
	}
	if err := validateSupplier(draft.Supplier); err != nil {
		return err
	}
	if len(draft.Items) > 1000 {
		return fault.Invalid("items 最多 1000 行")
	}
	for index := range draft.Items {
		if err := validateDraftItem(&draft.Items[index]); err != nil {
			return err
		}
	}
	if len(draft.Assets) > 1000 {
		return fault.Invalid("assets 最多 1000 行")
	}
	for index := range draft.Assets {
		if err := validateDraftAssetPlan(&draft.Assets[index]); err != nil {
			return err
		}
	}
	if len(draft.Funding) > 100 {
		return fault.Invalid("funding 最多 100 行")
	}
	for index := range draft.Funding {
		if err := validateDraftFunding(&draft.Funding[index]); err != nil {
			return err
		}
	}
	if len(draft.Payments) > 1000 {
		return fault.Invalid("payments 最多 1000 行")
	}
	for index := range draft.Payments {
		if err := validateDraftPaymentPlan(&draft.Payments[index]); err != nil {
			return err
		}
	}
	if len(draft.DeliveryPlans) > 1000 {
		return fault.Invalid("delivery_plans 最多 1000 行")
	}
	for index := range draft.DeliveryPlans {
		if err := validateDraftDeliveryPlan(&draft.DeliveryPlans[index]); err != nil {
			return err
		}
	}
	if len(draft.AcceptancePlans) > 1000 {
		return fault.Invalid("acceptance_plans 最多 1000 行")
	}
	for index := range draft.AcceptancePlans {
		if err := validateDraftAcceptancePlan(&draft.AcceptancePlans[index]); err != nil {
			return err
		}
	}
	if len(draft.Materials) > 500 {
		return fault.Invalid("materials 最多 500 项")
	}
	for _, ref := range draft.Materials {
		if err := validateMaterialRef(&ref); err != nil {
			return err
		}
	}
	return nil
}

func trimOptionalText(value *string) *string {
	if value == nil {
		return nil
	}
	return optionalText(*value)
}

func validateSupplier(supplier *Supplier) error {
	if supplier == nil {
		return nil
	}
	for _, check := range []struct {
		field, value string
		max          int
	}{
		{"supplier.enterprise_type", supplier.EnterpriseType, 100},
		{"supplier.name", supplier.Name, 200},
		{"supplier.credit_code", supplier.CreditCode, 100},
		{"supplier.enterprise_scale", supplier.EnterpriseScale, 100},
		{"supplier.accepting_entity", supplier.AcceptingEntity, 200},
		{"supplier.region", supplier.Region, 200},
		{"supplier.address", supplier.Address, 1000},
		{"supplier.contact_name", supplier.ContactName, 100},
		{"supplier.contact_phone", supplier.ContactPhone, 100},
	} {
		if err := validateDraftText(check.field, check.value, check.max); err != nil {
			return err
		}
	}
	if len(supplier.SpecialNature) > 30 {
		return fault.Invalid("supplier.special_nature 最多 30 项")
	}
	for _, item := range supplier.SpecialNature {
		if err := validateDraftText("supplier.special_nature", item, 100); err != nil {
			return err
		}
	}
	// Restricted supplier fields are length-checked here and sealed before any
	// persistence; see contract_draft_sealed.go.
	for _, check := range []struct {
		field, value string
		max          int
	}{
		{"supplier.payee_name", supplier.PayeeName, 200},
		{"supplier.bank_name", supplier.BankName, 200},
		{"supplier.bank_account", supplier.BankAccount, 100},
		{"supplier.tax_identifier", supplier.TaxIdentifier, 100},
	} {
		if err := validateDraftText(check.field, check.value, check.max); err != nil {
			return err
		}
	}
	return nil
}

func validateDraftItem(item *DraftItem) error {
	if !validContractUUID(item.ItemID) {
		return fault.Invalid("items[].item_id 必须是 UUID")
	}
	if err := validateDraftText("items[].name", item.Name, 200); err != nil {
		return err
	}
	for _, check := range []struct {
		field, value string
		max          int
	}{
		{"items[].catalog", item.Catalog, 100},
		{"items[].brand", item.Brand, 100},
		{"items[].specification", item.Specification, 1000},
		{"items[].unit", item.Unit, 40},
	} {
		if err := validateDraftText(check.field, check.value, check.max); err != nil {
			return err
		}
	}
	if err := validateDraftQuantity("items[].quantity", item.Quantity); err != nil {
		return err
	}
	if err := validateDraftUnitPrice("items[].unit_price", item.UnitPrice); err != nil {
		return err
	}
	if err := validateDraftMoney("items[].budget_amount", item.BudgetAmount); err != nil {
		return err
	}
	if err := validateDraftMoney("items[].line_amount", item.LineAmount); err != nil {
		return err
	}
	if item.TaxRate != "" && !taxRatePattern.MatchString(item.TaxRate) {
		return fault.Invalid("items[].tax_rate 必须是 0 到 1 的比例")
	}
	if len(item.Attributes) > 50 {
		return fault.Invalid("items[].attributes 最多 50 项")
	}
	for _, attribute := range item.Attributes {
		if err := validateDraftText("items[].attributes.name", attribute.Name, 100); err != nil {
			return err
		}
		if err := validateDraftText("items[].attributes.value", attribute.Value, 1000); err != nil {
			return err
		}
		if err := validateDraftText("items[].attributes.unit", attribute.Unit, 40); err != nil {
			return err
		}
	}
	return nil
}

func validateDraftAssetPlan(asset *DraftAssetPlan) error {
	if !validContractUUID(asset.AssetPlanID) {
		return fault.Invalid("assets[].asset_plan_id 必须是 UUID")
	}
	if asset.ItemID != "" && !validContractUUID(asset.ItemID) {
		return fault.Invalid("assets[].item_id 必须是 UUID")
	}
	for _, check := range []struct {
		field, value string
		max          int
	}{
		{"assets[].name", asset.Name, 200},
		{"assets[].code", asset.Code, 100},
		{"assets[].brand", asset.Brand, 100},
		{"assets[].specification", asset.Specification, 1000},
		{"assets[].origin", asset.Origin, 200},
		{"assets[].manufacturer", asset.Manufacturer, 200},
		{"assets[].department_code", asset.DepartmentCode, 100},
		{"assets[].department_name", asset.DepartmentName, 200},
		{"assets[].custodian_user_id", asset.CustodianUser, 100},
		{"assets[].remark", asset.Remark, 2000},
	} {
		if err := validateDraftText(check.field, check.value, check.max); err != nil {
			return err
		}
	}
	if err := validateDraftQuantity("assets[].quantity", asset.Quantity); err != nil {
		return err
	}
	if err := validateDraftUnitPrice("assets[].unit_price", asset.UnitPrice); err != nil {
		return err
	}
	return nil
}

func validateDraftFunding(funding *DraftFunding) error {
	if err := validateDraftText("funding[].nature", funding.Nature, 100); err != nil {
		return err
	}
	if err := validateDraftText("funding[].channel", funding.Channel, 100); err != nil {
		return err
	}
	if err := validateDraftMoney("funding[].amount", funding.Amount); err != nil {
		return err
	}
	if funding.FundingYear != nil && (*funding.FundingYear < 1 || *funding.FundingYear > 9999) {
		return fault.Invalid("funding[].funding_year 超出范围")
	}
	if err := validateDraftText("funding[].cost_center", funding.CostCenter, 100); err != nil {
		return err
	}
	return validateDraftText("funding[].quota_number", funding.QuotaNumber, 100)
}

func validateDraftPaymentPlan(plan *DraftPaymentPlan) error {
	if plan.Sequence < 1 || plan.Sequence > 1000 {
		return fault.Invalid("payments[].sequence 必须在 1 到 1000 之间")
	}
	if err := validateDraftText("payments[].description", plan.Description, 2000); err != nil {
		return err
	}
	if err := validateDraftDate("payments[].planned_date", plan.PlannedDate); err != nil {
		return err
	}
	for _, check := range []struct {
		field, value string
	}{
		{"payments[].advance_amount", plan.AdvanceAmount},
		{"payments[].unit_amount", plan.UnitAmount},
		{"payments[].fiscal_amount", plan.FiscalAmount},
		{"payments[].amount", plan.Amount},
	} {
		if err := validateDraftMoney(check.field, check.value); err != nil {
			return err
		}
	}
	if plan.Ratio != "" && !ratioPattern.MatchString(plan.Ratio) {
		return fault.Invalid("payments[].ratio 必须是 0 到 1 的比例")
	}
	if err := validateDraftText("payments[].ratio_base", plan.RatioBase, 200); err != nil {
		return err
	}
	if plan.Condition == nil {
		return nil
	}
	if !paymentConditionTypeValues[plan.Condition.Type] {
		return fault.Invalid("payments[].condition.type 不是有效取值")
	}
	if len(plan.Condition.EvidenceRequirements) > 30 {
		return fault.Invalid("payments[].condition.evidence_requirements 最多 30 项")
	}
	for _, requirement := range plan.Condition.EvidenceRequirements {
		if err := validateDraftText("payments[].condition.evidence_requirements", requirement, 100); err != nil {
			return err
		}
	}
	if err := validateDraftText("payments[].condition.policy_ref", plan.Condition.PolicyRef, 100); err != nil {
		return err
	}
	return validateDueRule(plan.Condition.DueRule)
}

func validateDueRule(rule *DueRule) error {
	if rule == nil {
		return nil
	}
	if !dueRuleTriggerValues[rule.Trigger] {
		return fault.Invalid("payments[].condition.due_rule.trigger 不是有效取值")
	}
	if rule.DaysAfter != nil && (*rule.DaysAfter < 0 || *rule.DaysAfter > 36500) {
		return fault.Invalid("payments[].condition.due_rule.days_after 超出范围")
	}
	if rule.Calendar != "" && !dueRuleCalendarValues[rule.Calendar] {
		return fault.Invalid("payments[].condition.due_rule.calendar 不是有效取值")
	}
	return validateDraftText("payments[].condition.due_rule.policy_ref", rule.PolicyRef, 100)
}

func validateDraftDeliveryPlan(plan *DraftDeliveryPlan) error {
	if plan.Sequence < 1 {
		return fault.Invalid("delivery_plans[].sequence 必须大于 0")
	}
	if plan.ItemID != "" && !validContractUUID(plan.ItemID) {
		return fault.Invalid("delivery_plans[].item_id 必须是 UUID")
	}
	if err := validateDraftDate("delivery_plans[].planned_date", plan.PlannedDate); err != nil {
		return err
	}
	if err := validateDraftText("delivery_plans[].description", plan.Description, 2000); err != nil {
		return err
	}
	return validateDraftQuantity("delivery_plans[].quantity", plan.Quantity)
}

func validateDraftAcceptancePlan(plan *DraftAcceptancePlan) error {
	if plan.Sequence < 1 {
		return fault.Invalid("acceptance_plans[].sequence 必须大于 0")
	}
	if err := validateDraftDate("acceptance_plans[].planned_date", plan.PlannedDate); err != nil {
		return err
	}
	if err := validateDraftText("acceptance_plans[].description", plan.Description, 2000); err != nil {
		return err
	}
	if err := validateDraftText("acceptance_plans[].criteria", plan.Criteria, 4000); err != nil {
		return err
	}
	if len(plan.ItemIDs) > 1000 {
		return fault.Invalid("acceptance_plans[].item_ids 最多 1000 项")
	}
	for _, itemID := range plan.ItemIDs {
		if !validContractUUID(itemID) {
			return fault.Invalid("acceptance_plans[].item_ids 必须是 UUID")
		}
	}
	return nil
}

func validateMaterialRef(ref *MaterialRef) error {
	if !validIdentifier(ref.MaterialID) || runeCount(ref.MaterialID) > 100 {
		return fault.Invalid("材料引用的 material_id 无效")
	}
	if !validIdentifier(ref.MaterialVersionID) || runeCount(ref.MaterialVersionID) > 100 {
		return fault.Invalid("材料引用的 material_version_id 无效")
	}
	if !digestPattern.MatchString(ref.SHA256) {
		return fault.Invalid("材料引用的 sha256 必须是 64 位小写十六进制摘要")
	}
	if !materialPurposeValues[ref.Purpose] {
		return fault.Invalid("材料引用的 purpose 不是有效取值")
	}
	return nil
}

// sanitizeDraftReferences removes server-managed reference snapshots before a
// draft or import payload is validated, hashed or sealed. The client may still
// send binding_id as a selection hint, but never a claimed verification result.
// The slices are copied so sanitizing a command input cannot mutate a caller's
// backing array.
func sanitizeDraftReferences(draft *DraftContent) {
	if draft == nil || draft.Contract == nil {
		return
	}
	if len(draft.Contract.RelatedParties) > 0 {
		parties := make([]PartyRef, len(draft.Contract.RelatedParties))
		for i, party := range draft.Contract.RelatedParties {
			party.ReferenceSnapshot = nil
			parties[i] = party
		}
		draft.Contract.RelatedParties = parties
	}
	if draft.Contract.ProcurementResultPackage != nil {
		ref := *draft.Contract.ProcurementResultPackage
		ref.SourceSnapshot = nil
		draft.Contract.ProcurementResultPackage = &ref
	}
}

// caseRoleAllowsContractRole maps the contract's display role onto the case
// subject role prepared by the subject service. It is deliberately explicit:
// a buyer must be the case customer, a supplier must be the case supplier, and
// peripheral roles may use the generic case roles.
func caseRoleAllowsContractRole(contractRole, caseRole string) bool {
	switch contractRole {
	case "buyer":
		return caseRole == SubjectRoleCustomer
	case "supplier":
		return caseRole == SubjectRoleSupplier
	case "guarantor":
		return caseRole == SubjectRoleContractor || caseRole == SubjectRoleServiceProvider || caseRole == SubjectRoleOther
	case "consortium_member":
		return caseRole == SubjectRoleSupplier || caseRole == SubjectRoleContractor ||
			caseRole == SubjectRoleServiceProvider || caseRole == SubjectRoleOther
	case "other":
		return caseRole == SubjectRoleCustomer || caseRole == SubjectRoleSupplier ||
			caseRole == SubjectRoleContractor || caseRole == SubjectRoleServiceProvider || caseRole == SubjectRoleOther
	default:
		return false
	}
}

func procurementMaterialRole(role string) bool {
	return role == "procurement" || role == "award"
}

func validateSourceRef(ref *SourceRef) error {
	ref.System = strings.TrimSpace(ref.System)
	ref.ID = strings.TrimSpace(ref.ID)
	ref.SourceVersion = strings.TrimSpace(ref.SourceVersion)
	if ref.System == "" || runeCount(ref.System) > 100 {
		return fault.Invalid("来源系统的标识无效")
	}
	if ref.ID == "" || runeCount(ref.ID) > 200 {
		return fault.Invalid("来源合同标识无效")
	}
	if ref.SourceVersion == "" || runeCount(ref.SourceVersion) > 100 {
		return fault.Invalid("来源版本无效")
	}
	return nil
}

// draftSummaryFacts extracts only the unrestricted summary facts of a stored
// content document. It never selects restricted supplier fields from JSONB.
func draftSummaryFacts(contentJSON []byte) (name string, amount, currency *string, err error) {
	var draft DraftContent
	if err := unmarshalStoredDraft(contentJSON, &draft); err != nil {
		return "", nil, nil, err
	}
	if draft.Contract == nil {
		return "", nil, nil, fmt.Errorf("stored draft content has no contract object")
	}
	basics := draft.Contract
	if basics.Amount != "" {
		value := basics.Amount
		amount = &value
	}
	if basics.Currency != "" {
		value := basics.Currency
		currency = &value
	}
	return basics.Name, amount, currency, nil
}
