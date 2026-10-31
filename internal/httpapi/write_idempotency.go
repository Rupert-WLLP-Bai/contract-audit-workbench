package httpapi

import (
	"net/http"

	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

// Optional keys preserve legacy callers while rejecting ambiguous repeated headers.
func writeIdempotencyKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) > 1 {
		return "", fault.Invalid("Idempotency-Key必须是唯一的请求头")
	}
	if len(values) == 0 {
		return "", nil
	}
	return values[0], nil
}

func writeAcceptedReviewReceipt(w http.ResponseWriter, accepted store.JobAcceptance, durable bool) {
	if durable {
		writeJSON(w, http.StatusAccepted, accepted)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": accepted.RunID, "attempt_id": accepted.AttemptID})
}
