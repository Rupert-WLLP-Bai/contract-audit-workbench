package app

import (
	clmcontract "contractaudit/internal/contract/model"
	"time"
)

type ContractAssetHandoverInput struct {
	ContractID         string                    `json:"-"`
	ExpectedRowVersion int64                     `json:"expected_row_version"`
	ContractRevisionID string                    `json:"contract_revision_id"`
	AssetPlanID        string                    `json:"asset_plan_id"`
	DeliveryID         string                    `json:"delivery_id"`
	AcceptanceID       string                    `json:"acceptance_id"`
	ScopeStart         string                    `json:"scope_start"`
	ScopeEnd           string                    `json:"scope_end"`
	HandedOverAt       string                    `json:"handed_over_at"`
	Location           string                    `json:"location"`
	TransferNote       string                    `json:"transfer_note"`
	Evidence           []clmcontract.MaterialRef `json:"evidence"`
	IdempotencyKey     string                    `json:"-"`
}

type ContractAssetSource struct{ TenantID, SourceID, KeyID string }

type ContractAssetReceiptInput struct {
	ContractID      string    `json:"contract_id"`
	HandoverID      string    `json:"handover_id"`
	EventID         string    `json:"event_id"`
	EventType       string    `json:"event_type"`
	OriginalEventID string    `json:"original_event_id,omitempty"`
	AssetCode       string    `json:"asset_code"`
	Quantity        string    `json:"quantity"`
	Unit            string    `json:"unit"`
	OccurredAt      time.Time `json:"occurred_at"`
}

type ContractAssetReceipt struct {
	ReceiptID             string    `json:"receipt_id"`
	ContractID            string    `json:"contract_id"`
	HandoverID            string    `json:"handover_id"`
	SourceID              string    `json:"source_id"`
	SourceKeyID           string    `json:"source_key_id"`
	SourceEventID         string    `json:"source_event_id"`
	EventType             string    `json:"event_type"`
	OriginalSourceEventID string    `json:"original_source_event_id,omitempty"`
	AssetCode             string    `json:"asset_code"`
	Quantity              string    `json:"quantity"`
	Unit                  string    `json:"unit"`
	OccurredAt            time.Time `json:"occurred_at"`
	RecordedAt            time.Time `json:"recorded_at"`
	PayloadDigest         string    `json:"payload_digest"`
}

type ContractAssetHandover struct {
	HandoverID                 string                                `json:"handover_id"`
	ContractID                 string                                `json:"contract_id"`
	ContractRevisionID         string                                `json:"contract_revision_id"`
	AssetPlanID                string                                `json:"asset_plan_id"`
	DeliveryID                 string                                `json:"delivery_id"`
	ItemID                     string                                `json:"item_id"`
	AcceptanceID               string                                `json:"acceptance_id"`
	ScopeStart                 string                                `json:"scope_start"`
	ScopeEnd                   string                                `json:"scope_end"`
	Quantity                   string                                `json:"quantity"`
	Unit                       string                                `json:"unit"`
	HandedOverAt               time.Time                             `json:"handed_over_at"`
	Location                   string                                `json:"location"`
	TransferNote               string                                `json:"transfer_note"`
	Evidence                   []clmcontract.ContractVersionMaterial `json:"evidence"`
	EvidenceRedacted           bool                                  `json:"evidence_redacted,omitempty"`
	DepartmentCodeClaim        string                                `json:"department_code_claim,omitempty"`
	DepartmentNameClaim        string                                `json:"department_name_claim,omitempty"`
	DepartmentVerification     string                                `json:"department_verification"`
	CustodianUserID            string                                `json:"custodian_user_id,omitempty"`
	CustodianDisplayName       string                                `json:"custodian_display_name,omitempty"`
	CustodianOrgID             string                                `json:"custodian_org_id,omitempty"`
	CustodianDirectorySource   string                                `json:"custodian_directory_source,omitempty"`
	CustodianDirectoryRevision string                                `json:"custodian_directory_revision,omitempty"`
	CustodianDirectoryChecked  *time.Time                            `json:"custodian_directory_checked_at,omitempty"`
	CustodianVerification      string                                `json:"custodian_verification"`
	AssignmentVerification     string                                `json:"assignment_verification"`
	BookingStatus              string                                `json:"booking_status"`
	RecordedBy                 string                                `json:"recorded_by"`
	RecordedAt                 time.Time                             `json:"recorded_at"`
	ContractRowVersion         int64                                 `json:"contract_row_version"`
	Receipts                   []ContractAssetReceipt                `json:"receipts"`
}

type ContractAssetPlanSummary struct {
	ContractRevisionID string                  `json:"contract_revision_id"`
	AssetPlanID        string                  `json:"asset_plan_id"`
	ItemID             string                  `json:"item_id,omitempty"`
	Name               string                  `json:"name,omitempty"`
	PlannedQuantity    string                  `json:"planned_quantity,omitempty"`
	Unit               string                  `json:"unit,omitempty"`
	DepartmentCode     string                  `json:"department_code,omitempty"`
	DepartmentName     string                  `json:"department_name,omitempty"`
	DepartmentStatus   string                  `json:"department_verification"`
	CustodianUserID    string                  `json:"custodian_user_id,omitempty"`
	FixedAssetStatus   string                  `json:"fixed_asset_status"`
	PlanStatus         string                  `json:"plan_status"`
	Handovers          []ContractAssetHandover `json:"handovers"`
}

type ContractAssetList struct {
	ContractID       string                     `json:"contract_id"`
	RowVersion       int64                      `json:"row_version"`
	FixedAssetStatus string                     `json:"fixed_asset_status"`
	Items            []ContractAssetPlanSummary `json:"items"`
}

type ProjectedPlan struct {
	clmcontract.DraftAssetPlan
	Unit string
}
