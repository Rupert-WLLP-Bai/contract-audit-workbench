// The typed source mirrors the exported money DTO JSON surface. Keeping this
// leaf contract package independent avoids coupling the documentation server
// to Store's runtime dependencies.
package contracts

type TenantMoneyWindow struct {
	PeriodKey        string  `json:"period_key"`
	LimitMinor       *string `json:"limit_minor" openapi:"{\"nullable\":true}"`
	ReservedMinor    string  `json:"reserved_minor"`
	SettledMinor     string  `json:"settled_minor"`
	UnknownHoldMinor string  `json:"unknown_hold_minor"`
	ExposureMinor    string  `json:"exposure_minor"`
}
type TenantMoneyPolicy struct {
	SchemaVersion string            `json:"schema_version"`
	TenantID      string            `json:"tenant_id"`
	Revision      string            `json:"revision"`
	Configured    bool              `json:"configured"`
	Currency      string            `json:"currency"`
	MinorScale    int               `json:"minor_unit_scale"`
	Timezone      string            `json:"timezone"`
	PriceState    string            `json:"price_state"`
	DayLimit      *string           `json:"day_limit_minor" openapi:"{\"nullable\":true}"`
	MonthLimit    *string           `json:"month_limit_minor" openapi:"{\"nullable\":true}"`
	Day           TenantMoneyWindow `json:"day"`
	Month         TenantMoneyWindow `json:"month"`
}
type TenantMoneyPolicyInput struct {
	ExpectedRevision string  `json:"expected_revision"`
	DayLimitMinor    *string `json:"day_limit_minor" openapi:"{\"nullable\":true}"`
	MonthLimitMinor  *string `json:"month_limit_minor" openapi:"{\"nullable\":true}"`
}
type PlatformModelPriceInput struct {
	ProviderConfigID string `json:"provider_config_id"`
	ModelRef         string `json:"model_ref"`
	InputRate        string `json:"input_micro_usd_per_million"`
	OutputRate       string `json:"output_micro_usd_per_million"`
}
type PlatformModelMoneyPolicyInput struct {
	ExpectedRevision string                    `json:"expected_revision"`
	GlobalDayLimit   *string                   `json:"global_day_limit_minor" openapi:"{\"nullable\":true}"`
	GlobalMonthLimit *string                   `json:"global_month_limit_minor" openapi:"{\"nullable\":true}"`
	Prices           []PlatformModelPriceInput `json:"prices"`
}
type PlatformModelMoneyPolicyResult struct {
	SchemaVersion string `json:"schema_version"`
	Revision      string `json:"revision"`
	PricesAdded   int    `json:"prices_added"`
}
type PlatformModelMoneyPolicyView struct {
	SchemaVersion    string  `json:"schema_version"`
	Revision         string  `json:"revision"`
	GlobalDayLimit   *string `json:"global_day_limit_minor" openapi:"{\"nullable\":true}"`
	GlobalMonthLimit *string `json:"global_month_limit_minor" openapi:"{\"nullable\":true}"`
	PriceSnapshots   string  `json:"price_snapshot_count"`
}
type TenantMoneyAdjustmentInput struct {
	LeaseID        string `json:"lease_id"`
	Kind           string `json:"kind"`
	AmountMinor    string `json:"amount_minor"`
	IdempotencyKey string `json:"idempotency_key"`
	Reason         string `json:"reason"`
}
type TenantMoneyAdjustmentResult struct {
	EventID     string `json:"event_id"`
	LeaseID     string `json:"lease_id"`
	Kind        string `json:"kind"`
	AmountMinor string `json:"amount_minor"`
}
