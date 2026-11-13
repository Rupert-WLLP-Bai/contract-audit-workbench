package app

import (
	"context"
	"time"

	clmcontract "contractaudit/internal/contract/model"
)

const (
	CreateDeliveryRoute        = "clm.create_delivery"
	CreateAcceptanceRoute      = "clm.create_acceptance"
	CreateRectificationRoute   = "clm.create_rectification"
	DeliveryRecordedEvent      = "contract.delivery_recorded.v1"
	AcceptanceRecordedEvent    = "contract.acceptance_recorded.v1"
	RectificationRecordedEvent = "contract.rectification_recorded.v1"
)

// These public command and response types preserve the Store wire shape. The
// Store keeps aliases so existing callers and keyed literals remain valid.
type ContractDeliveryInput struct {
	ContractID           string                    `json:"-"`
	ExpectedRowVersion   int64                     `json:"expected_row_version"`
	ContractRevisionID   string                    `json:"contract_revision_id"`
	DeliveryPlanSequence *int                      `json:"delivery_plan_sequence,omitempty"`
	ItemID               string                    `json:"item_id"`
	BatchReference       string                    `json:"batch_reference"`
	Quantity             string                    `json:"quantity"`
	ActualAt             string                    `json:"actual_at"`
	Location             string                    `json:"location"`
	Description          string                    `json:"description,omitempty"`
	Evidence             []clmcontract.MaterialRef `json:"evidence"`
	IdempotencyKey       string                    `json:"-"`
}

type ContractDelivery struct {
	DeliveryID           string                                `json:"delivery_id"`
	ContractID           string                                `json:"contract_id"`
	ContractRevisionID   string                                `json:"contract_revision_id"`
	DeliveryPlanSequence *int                                  `json:"delivery_plan_sequence,omitempty"`
	ItemID               string                                `json:"item_id"`
	ItemName             string                                `json:"item_name"`
	BatchReference       string                                `json:"batch_reference"`
	Quantity             string                                `json:"quantity"`
	Unit                 string                                `json:"unit"`
	ActualAt             time.Time                             `json:"actual_at"`
	Location             string                                `json:"location"`
	Description          string                                `json:"description"`
	Evidence             []clmcontract.ContractVersionMaterial `json:"evidence"`
	EvidenceRedacted     bool                                  `json:"evidence_redacted,omitempty"`
	RecordedBy           string                                `json:"recorded_by"`
	RecordedAt           time.Time                             `json:"recorded_at"`
}

type ContractFulfillmentItemSummary struct {
	ContractRevisionID          string `json:"contract_revision_id"`
	ItemID                      string `json:"item_id"`
	ItemName                    string `json:"item_name"`
	Unit                        string `json:"unit,omitempty"`
	PlannedQuantity             string `json:"planned_quantity,omitempty"`
	DeliveredQuantity           string `json:"delivered_quantity"`
	QualifiedQuantity           string `json:"qualified_quantity"`
	UnallocatedQuantity         string `json:"unallocated_quantity"`
	PendingReinspectionQuantity string `json:"pending_reinspection_quantity"`
}

type ContractDeliveryList struct {
	Items      []ContractDelivery               `json:"items"`
	Summary    []ContractFulfillmentItemSummary `json:"summary"`
	RowVersion int64                            `json:"row_version"`
}

type ContractAcceptanceDefectInput struct {
	PriorDefectID  string `json:"prior_defect_id,omitempty"`
	Description    string `json:"description"`
	Responsible    string `json:"responsible"`
	RemediationDue string `json:"remediation_due"`
	FailedQuantity string `json:"failed_quantity"`
}

type ContractAcceptanceDefect struct {
	DefectID       string    `json:"defect_id"`
	PriorDefectID  string    `json:"prior_defect_id,omitempty"`
	Description    string    `json:"description"`
	Responsible    string    `json:"responsible"`
	RemediationDue string    `json:"remediation_due"`
	FailedQuantity string    `json:"failed_quantity"`
	RecordedBy     string    `json:"recorded_by"`
	RecordedAt     time.Time `json:"recorded_at"`
}

type ContractAcceptanceInput struct {
	ContractID             string                          `json:"-"`
	ExpectedRowVersion     int64                           `json:"expected_row_version"`
	ContractRevisionID     string                          `json:"contract_revision_id"`
	DeliveryID             string                          `json:"delivery_id"`
	AcceptancePlanSequence *int                            `json:"acceptance_plan_sequence,omitempty"`
	ScopeStart             string                          `json:"scope_start"`
	ScopeEnd               string                          `json:"scope_end"`
	AcceptedQuantity       string                          `json:"accepted_quantity"`
	ReinspectionOf         string                          `json:"reinspection_of,omitempty"`
	RectificationIDs       []string                        `json:"rectification_ids,omitempty"`
	ActualAt               string                          `json:"actual_at"`
	Standard               string                          `json:"standard"`
	InspectorNote          string                          `json:"inspector_note,omitempty"`
	Defects                []ContractAcceptanceDefectInput `json:"defects,omitempty"`
	Evidence               []clmcontract.MaterialRef       `json:"evidence"`
	IdempotencyKey         string                          `json:"-"`
}

type ContractAcceptance struct {
	AcceptanceID           string                                `json:"acceptance_id"`
	ContractID             string                                `json:"contract_id"`
	ContractRevisionID     string                                `json:"contract_revision_id"`
	DeliveryID             string                                `json:"delivery_id"`
	ItemID                 string                                `json:"item_id"`
	ItemName               string                                `json:"item_name"`
	AcceptancePlanSequence *int                                  `json:"acceptance_plan_sequence,omitempty"`
	ScopeStart             string                                `json:"scope_start"`
	ScopeEnd               string                                `json:"scope_end"`
	Quantity               string                                `json:"quantity"`
	AcceptedQuantity       string                                `json:"accepted_quantity"`
	FailedQuantity         string                                `json:"failed_quantity"`
	Unit                   string                                `json:"unit"`
	Result                 string                                `json:"result"`
	ReinspectionOf         string                                `json:"reinspection_of,omitempty"`
	RectificationIDs       []string                              `json:"rectification_ids,omitempty"`
	ActualAt               time.Time                             `json:"actual_at"`
	Standard               string                                `json:"standard"`
	InspectorNote          string                                `json:"inspector_note"`
	Evidence               []clmcontract.ContractVersionMaterial `json:"evidence"`
	EvidenceRedacted       bool                                  `json:"evidence_redacted,omitempty"`
	Defects                []ContractAcceptanceDefect            `json:"defects"`
	Rectifications         []ContractRectification               `json:"rectifications"`
	RecordedBy             string                                `json:"recorded_by"`
	RecordedAt             time.Time                             `json:"recorded_at"`
}

type ContractAcceptanceList struct {
	Items      []ContractAcceptance             `json:"items"`
	Summary    []ContractFulfillmentItemSummary `json:"summary"`
	RowVersion int64                            `json:"row_version"`
}

type ContractRectificationInput struct {
	ContractID         string                    `json:"-"`
	ExpectedRowVersion int64                     `json:"expected_row_version"`
	ContractRevisionID string                    `json:"contract_revision_id"`
	AcceptanceID       string                    `json:"acceptance_id"`
	DefectID           string                    `json:"defect_id"`
	Description        string                    `json:"description"`
	Evidence           []clmcontract.MaterialRef `json:"evidence"`
	IdempotencyKey     string                    `json:"-"`
}

type ContractRectification struct {
	RectificationID    string                                `json:"rectification_id"`
	ContractID         string                                `json:"contract_id"`
	ContractRevisionID string                                `json:"contract_revision_id"`
	AcceptanceID       string                                `json:"acceptance_id"`
	DefectID           string                                `json:"defect_id"`
	Description        string                                `json:"description"`
	Evidence           []clmcontract.ContractVersionMaterial `json:"evidence"`
	EvidenceRedacted   bool                                  `json:"evidence_redacted,omitempty"`
	RecordedBy         string                                `json:"recorded_by"`
	RecordedAt         time.Time                             `json:"recorded_at"`
}

type ContractRectificationList struct {
	Items []ContractRectification `json:"items"`
}

type Principal struct {
	TenantID string
	ActorID  string
}

type FulfillmentAccess interface {
	CanRead(context.Context, clmcontract.Aggregate) bool
	CanReadForManagement(context.Context, clmcontract.Aggregate) bool
	CanManage(context.Context, clmcontract.Aggregate) bool
	ResolveMaterial(context.Context, clmcontract.MaterialRef, string) (clmcontract.ContractVersionMaterial, error)
	CanReadCase(context.Context, string, string) error
}

type DeliveryFact struct {
	DeliveryID           string
	ContractRevisionID   string
	DeliveryPlanSequence *int
	ItemID               string
	BatchReference       string
	Quantity             string
	Unit                 string
	ActualAt             time.Time
	Location             string
	Description          string
	EvidenceSnapshot     []byte
	RecordedBy           string
	RecordedAt           time.Time
}

type AcceptanceFact struct {
	AcceptanceID           string
	ContractRevisionID     string
	DeliveryID             string
	ItemID                 string
	AcceptancePlanSequence *int
	ScopeStart             string
	ScopeEnd               string
	AcceptedQuantity       string
	Result                 string
	ReinspectionOf         string
	ActualAt               time.Time
	Standard               string
	InspectorNote          string
	EvidenceSnapshot       []byte
	RecordedBy             string
	RecordedAt             time.Time
}

type DefectFact struct {
	DefectID       string
	PriorDefectID  string
	Description    string
	Responsible    string
	RemediationDue string
	FailedQuantity string
	RecordedBy     string
	RecordedAt     time.Time
}

type RectificationFact struct {
	RectificationID    string
	ContractRevisionID string
	AcceptanceID       string
	DefectID           string
	Description        string
	EvidenceSnapshot   []byte
	RecordedBy         string
	RecordedAt         time.Time
}

type DeliveryInsert struct {
	TenantID             string
	ContractID           string
	ContractRevisionID   string
	DeliveryID           string
	DeliveryPlanSequence *int
	ItemID               string
	BatchReference       string
	Quantity             string
	Unit                 string
	ActualAt             time.Time
	Location             string
	Description          string
	EvidenceSnapshot     []byte
	RecordedBy           string
}

type AcceptanceInsert struct {
	TenantID           string
	ContractID         string
	ContractRevisionID string
	AcceptanceID       string
	DeliveryID         string
	ItemID             string
	PlanSequence       *int
	ScopeStart         string
	ScopeEnd           string
	AcceptedQuantity   string
	Result             string
	ReinspectionOf     string
	ActualAt           time.Time
	Standard           string
	InspectorNote      string
	EvidenceSnapshot   []byte
	RecordedBy         string
}

type DefectInsert struct {
	TenantID           string
	ContractID         string
	ContractRevisionID string
	AcceptanceID       string
	DefectID           string
	PriorAcceptanceID  string
	PriorDefectID      string
	Description        string
	Responsible        string
	RemediationDue     string
	FailedQuantity     string
	RecordedBy         string
}

type RectificationInsert struct {
	TenantID           string
	ContractID         string
	ContractRevisionID string
	RectificationID    string
	AcceptanceID       string
	DefectID           string
	Description        string
	EvidenceSnapshot   []byte
	RecordedBy         string
}

type AcceptanceRectificationInsert struct {
	TenantID           string
	ContractID         string
	AcceptanceID       string
	RevisionID         string
	DeliveryID         string
	ItemID             string
	RectificationID    string
	DefectAcceptanceID string
	DefectID           string
}

type FulfillmentRepository interface {
	SumDelivered(context.Context, string, string, string, string) (string, error)
	SumPlanDelivery(context.Context, string, string, string, string, int) (string, error)
	InsertDelivery(context.Context, DeliveryInsert) (time.Time, error)
	LoadDelivery(context.Context, string, string, string, string) (DeliveryFact, bool, error)
	HasOverlappingRootAcceptance(context.Context, string, string, string, string, string) (bool, error)
	LoadAcceptance(context.Context, string, string, string) (AcceptanceFact, bool, error)
	ListAcceptanceDefects(context.Context, string, string, string) ([]DefectFact, error)
	ListAcceptanceRectifications(context.Context, string, string, string) ([]RectificationFact, error)
	HasReinspection(context.Context, string, string, string) (bool, error)
	HasDefect(context.Context, string, string, string, string) (bool, error)
	InsertAcceptance(context.Context, AcceptanceInsert) (time.Time, error)
	LoadRectification(context.Context, string, string, string) (RectificationFact, bool, error)
	InsertAcceptanceRectification(context.Context, AcceptanceRectificationInsert) error
	InsertDefect(context.Context, DefectInsert) (time.Time, error)
	LoadAcceptanceResult(context.Context, string, string, string, string) (string, bool, error)
	InsertRectification(context.Context, RectificationInsert) (time.Time, error)
	ListDeliveries(context.Context, string, string) ([]DeliveryFact, error)
	ListAcceptances(context.Context, string, string) ([]AcceptanceFact, error)
	ListAcceptanceRectificationIDs(context.Context, string, string, string) ([]string, error)
}

type PreparedDelivery struct {
	Input         ContractDeliveryInput
	ActualAt      time.Time
	RequestDigest string
}

type PreparedAcceptance struct {
	Input         ContractAcceptanceInput
	ActualAt      time.Time
	RequestDigest string
}

type PreparedRectification struct {
	Input         ContractRectificationInput
	RequestDigest string
}

type AcceptanceFacts struct {
	ContractRevisionID string
	ItemID             string
	AcceptedQuantity   string
	Acceptances        []AcceptanceFact
}
