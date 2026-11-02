// File metadata (manifest mirror):
// Purpose: 实现 `TenantProvisionOperation`, `TenantProvisionStepView`, `TenantProvisionOperationsView`, `TenantProvisionInput` 等具体接口与边界逻辑，供 internal/apidocs/contracts 的调用方使用。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Lifecycle and jurisdiction contracts alias the DTOs used by Core handlers.
package contracts

import "contractaudit/internal/store"

type TenantProvisionOperation = store.TenantProvisionOperation
type TenantProvisionStepView = store.TenantProvisionStepView
type TenantProvisionOperationsView = store.TenantProvisionOperationsView
type TenantProvisionInput = store.TenantProvisionInput
type TenantProvisionOperationResult = store.TenantProvisionOperationResult
type TenantProvisionStepInput = store.TenantProvisionStepInput
type JurisdictionOperationsView = store.JurisdictionOperationsView
type JurisdictionOperationsSummary = store.JurisdictionOperationsSummary
type TenantOperationsSummary = store.TenantOperationsSummary
type OperationsMetricSummary = store.OperationsMetricSummary
type OperationsCostView = store.OperationsCostView
type OperationsQuotaView = store.OperationsQuotaView
type OperationsPoolView = store.OperationsPoolView

// Pointer fields in these DTOs serialize as explicit JSON nulls. These tags
// supplement existing Store DTOs without changing their public JSON shape.
var NullableFields = map[string]bool{
	"TenantMoneyWindow.LimitMinor":                    true,
	"TenantMoneyPolicy.DayLimit":                      true,
	"TenantMoneyPolicy.MonthLimit":                    true,
	"PlatformMoneyPolicyInput.GlobalDayLimit":         true,
	"PlatformMoneyPolicyInput.GlobalMonthLimit":       true,
	"PlatformMoneyPolicyView.GlobalDayLimit":          true,
	"PlatformMoneyPolicyView.GlobalMonthLimit":        true,
	"OperationsMetricSummary.Money":                   true,
	"OperationsMetricSummary.LogicalQuota":            true,
	"OperationsMetricSummary.SharedPool":              true,
	"TenantProvisionStepView.ReceiptSHA256":           true,
	"TenantProvisionStepView.UpdatedAt":               true,
	"TenantProvisionOperationsView.NextCursor":        true,
	"TenantProvisionStepInput.ReceiptSHA256":          true,
	"JurisdictionOperationsView.Watermark":            true,
	"JurisdictionOperationsView.SourceSnapshotToken":  true,
	"JurisdictionOperationsView.BuiltAt":              true,
	"JurisdictionOperationsView.DirtySince":           true,
	"JurisdictionOperationsView.ProjectionAgeSeconds": true,
	"OperationsMetricSummary.CasesCreated":            true,
	"OperationsMetricSummary.RunsCompleted":           true,
	"OperationsMetricSummary.AttemptsCompleted":       true,
	"OperationsCostView.SettledMinor":                 true,
	"OperationsCostView.UnknownHoldMinor":             true,
	"OperationsQuotaView.Calls":                       true,
	"OperationsQuotaView.ReservedTokens":              true,
	"OperationsQuotaView.UnknownUsageCalls":           true,
	"OperationsPoolView.Capacity":                     true,
	"OperationsPoolView.InUse":                        true,
}
