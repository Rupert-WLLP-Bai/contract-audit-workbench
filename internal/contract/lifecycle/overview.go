package lifecycle

import "time"

// LifecycleOverview is an observation of stored facts, never a closure decision.
type LifecycleOverview struct {
	Contract         LifecycleContract     `json:"contract"`
	ObservedAt       time.Time             `json:"observed_at"`
	Completeness     LifecycleCompleteness `json:"completeness"`
	SourceObservedAt LifecycleSourceTimes  `json:"source_observed_at"`
	Plans            *LifecyclePlans       `json:"plans"`
	Deliveries       any                   `json:"deliveries"`
	Acceptances      any                   `json:"acceptances"`
	Payments         any                   `json:"payments"`
	Assets           any                   `json:"assets"`
}
type LifecycleContract struct {
	ContractID        string  `json:"contract_id"`
	CurrentRevisionID *string `json:"current_revision_id"`
	RowVersion        int64   `json:"row_version"`
	AuthorityMode     string  `json:"authority_mode"`
}
type LifecycleCompleteness struct {
	Plans            string `json:"plans"`
	Deliveries       string `json:"deliveries"`
	Acceptances      string `json:"acceptances"`
	Payments         string `json:"payments"`
	Assets           string `json:"assets"`
	Disputes         string `json:"disputes"`
	Warranty         string `json:"warranty"`
	OtherObligations string `json:"other_obligations"`
}
type LifecycleSourceTimes struct {
	ContractRecordedAt *time.Time `json:"contract_recorded_at"`
	Finance            *time.Time `json:"finance"`
	ERP                *time.Time `json:"erp"`
}
type LifecyclePlans struct {
	DeliveryPlans   any `json:"delivery_plans"`
	AcceptancePlans any `json:"acceptance_plans"`
	Payments        any `json:"payments"`
	Assets          any `json:"assets"`
}
