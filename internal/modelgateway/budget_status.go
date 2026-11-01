package modelgateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"contractaudit/internal/systemhealth"
)

// Configuration is immutable for this gateway process; encode and hash once.
func encodeModelBudgetStatus(config Config) []byte {
	limits := config.Budget
	status := systemhealth.ModelBudgetGatewayStatus{
		SchemaVersion: systemhealth.ModelBudgetSchemaVersion,
		Mode:          config.Mode,
		WindowSeconds: 60,
		Tenant: systemhealth.ModelBudgetCapacity{
			MaxInFlight: int64(limits.TenantMaxInFlight), CallsPerMinute: int64(limits.TenantCallsPerMinute), TokensPerMinute: limits.TenantTokensPerMinute,
		},
		Global: systemhealth.ModelBudgetCapacity{
			MaxInFlight: int64(limits.GlobalMaxInFlight), CallsPerMinute: int64(limits.GlobalCallsPerMinute), TokensPerMinute: limits.GlobalTokensPerMinute,
		},
	}
	body, _ := json.Marshal(limits)
	digest := sha256.Sum256(body)
	status.Revision = hex.EncodeToString(digest[:])
	body, _ = json.Marshal(status)
	return body
}

func (s *Service) modelBudgetHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(s.budgetStatus)
}
