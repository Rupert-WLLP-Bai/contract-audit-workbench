// File metadata (manifest mirror):
// Purpose: 实现 `TenantMoneyWindow`, `TenantMoneyPolicy`, `TenantMoneyPolicyInput`, `PlatformModelPriceInput` 等具体接口与边界逻辑，供 internal/apidocs/contracts 的调用方使用。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

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
