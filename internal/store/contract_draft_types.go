package store

import (
	"regexp"

	clmcontract "contractaudit/internal/contract/model"
)

// Contract draft models and pure invariants live in internal/contract. These
// aliases preserve the existing Store-facing API while callers migrate.
type DraftContent = clmcontract.DraftContent
type ContractBasics = clmcontract.ContractBasics
type Buyer = clmcontract.Buyer
type Supplier = clmcontract.Supplier
type DraftItem = clmcontract.DraftItem
type DraftAssetPlan = clmcontract.DraftAssetPlan
type DraftFunding = clmcontract.DraftFunding
type DraftPaymentPlan = clmcontract.DraftPaymentPlan
type DraftDeliveryPlan = clmcontract.DraftDeliveryPlan
type DraftAcceptancePlan = clmcontract.DraftAcceptancePlan
type PaymentCondition = clmcontract.PaymentCondition
type DueRule = clmcontract.DueRule
type PartyReferenceSnapshot = clmcontract.PartyReferenceSnapshot
type PartyRef = clmcontract.PartyRef
type UnitRef = clmcontract.UnitRef
type ProcurementSourceSnapshot = clmcontract.ProcurementSourceSnapshot
type ProcurementRef = clmcontract.ProcurementRef
type Attribute = clmcontract.Attribute
type MaterialRef = clmcontract.MaterialRef
type SourceRef = clmcontract.SourceRef

// These expressions are retained for existing Store files that share the
// money grammar without depending on the contract package's implementation.
var (
	moneyPattern     = regexp.MustCompile(`^(0|[1-9][0-9]{0,15})\.[0-9]{2}$`)
	unitPricePattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,13})\.[0-9]{4}$`)
	quantityPattern  = regexp.MustCompile(`^(0|[1-9][0-9]{0,11})\.[0-9]{6}$`)
	ratioPattern     = regexp.MustCompile(`^(0\.[0-9]{6}|1\.000000)$`)
	taxRatePattern   = regexp.MustCompile(`^(0\.[0-9]{4}|1\.0000)$`)
	currencyPattern  = regexp.MustCompile(`^[A-Z]{3}$`)
	digestPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

func newContractUUID() (string, error)    { return clmcontract.NewContractUUID() }
func validContractUUID(value string) bool { return clmcontract.ValidContractUUID(value) }
func normalizeContractUUID(value string) (string, error) {
	return clmcontract.NormalizeContractUUID(value)
}
func optionalText(value string) *string              { return clmcontract.OptionalText(value) }
func validateDraftContent(draft *DraftContent) error { return clmcontract.ValidateDraftContent(draft) }
func sanitizeDraftReferences(draft *DraftContent)    { clmcontract.SanitizeDraftReferences(draft) }
func validateDraftMoney(field, value string) error {
	return clmcontract.ValidateDraftMoney(field, value)
}
func validateDraftDate(field, value string) error { return clmcontract.ValidateDraftDate(field, value) }
func validateDraftText(field, value string, max int) error {
	return clmcontract.ValidateDraftText(field, value, max)
}
func validateMaterialRef(ref *MaterialRef) error { return clmcontract.ValidateMaterialRef(ref) }
func validateSourceRef(ref *SourceRef) error     { return clmcontract.ValidateSourceRef(ref) }
func caseRoleAllowsContractRole(contractRole, caseRole string) bool {
	return clmcontract.CaseRoleAllowsContractRole(contractRole, caseRole)
}
func procurementMaterialRole(role string) bool { return clmcontract.ProcurementMaterialRole(role) }
func draftSummaryFacts(raw []byte) (string, *string, *string, error) {
	return clmcontract.DraftSummaryFacts(raw)
}
