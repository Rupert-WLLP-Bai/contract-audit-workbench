// Money DTOs reuse the exact response/input structs used by Core. The one
// unexported HTTP request shape is retained here as a typed public contract.
package contracts

import "contractaudit/internal/store"

type TenantMoneyWindow = store.TenantMoneyWindow
type TenantMoneyPolicy = store.TenantMoneyPolicy
type TenantMoneyPolicyInput struct {
	ExpectedRevision string  `json:"expected_revision"`
	DayLimitMinor    *string `json:"day_limit_minor" openapi:"{\"nullable\":true}"`
	MonthLimitMinor  *string `json:"month_limit_minor" openapi:"{\"nullable\":true}"`
}
type PlatformModelPriceInput = store.TrustedModelPriceInput
type PlatformModelMoneyPolicyInput = store.PlatformMoneyPolicyInput
type PlatformModelMoneyPolicyResult = store.PlatformMoneyPolicyResult
type PlatformModelMoneyPolicyView = store.PlatformMoneyPolicyView
type TenantMoneyAdjustmentInput = store.TenantMoneyAdjustmentInput
type TenantMoneyAdjustmentResult = store.TenantMoneyAdjustmentResult
