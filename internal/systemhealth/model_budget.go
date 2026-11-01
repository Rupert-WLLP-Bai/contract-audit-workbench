package systemhealth

import (
	"context"
	"net/http"
	"os"
)

const ModelBudgetSchemaVersion = "model-budget/v1"

// ModelBudgetCapacity describes limits enforced by the gateway's PG ledger.
type ModelBudgetCapacity struct {
	MaxInFlight     int64 `json:"max_in_flight"`
	CallsPerMinute  int64 `json:"calls_per_minute"`
	TokensPerMinute int64 `json:"tokens_per_minute"`
}

// ModelBudgetGatewayStatus contains configuration only, never tenant usage or keys.
type ModelBudgetGatewayStatus struct {
	SchemaVersion string              `json:"schema_version"`
	Revision      string              `json:"revision"`
	Mode          string              `json:"mode"`
	WindowSeconds int64               `json:"window_seconds"`
	Tenant        ModelBudgetCapacity `json:"tenant"`
	Global        ModelBudgetCapacity `json:"global"`
}

func ModelGatewayBaseURLFromEnv() string {
	return gatewayBaseURL(os.Getenv("MODEL_GATEWAY_URL"))
}

func FetchModelBudgetGatewayStatus(ctx context.Context, endpoint string) (ModelBudgetGatewayStatus, bool) {
	endpoint = gatewayBaseURL(endpoint)
	if endpoint == "" {
		return ModelBudgetGatewayStatus{}, false
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/health/model-budget", nil)
	if err != nil {
		return ModelBudgetGatewayStatus{}, false
	}
	var status ModelBudgetGatewayStatus
	if err := getJSON(endpointClient, request, &status); err != nil {
		return ModelBudgetGatewayStatus{}, false
	}
	if status.SchemaVersion != ModelBudgetSchemaVersion || len(status.Revision) != 64 || status.WindowSeconds != 60 ||
		status.Tenant.MaxInFlight < 1 || status.Tenant.CallsPerMinute < 1 || status.Tenant.TokensPerMinute < 1 ||
		status.Global.MaxInFlight < 1 || status.Global.CallsPerMinute < 1 || status.Global.TokensPerMinute < 1 {
		return ModelBudgetGatewayStatus{}, false
	}
	return status, true
}
