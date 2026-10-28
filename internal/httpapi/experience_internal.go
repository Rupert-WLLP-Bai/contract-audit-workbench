package httpapi

import (
	"crypto/subtle"
	"net/http"

	"contractaudit/internal/store"
)

// experienceResultHandler is reachable only on the Core service's internal
// listener. The worker sends identities, never a user or candidate body.
func experienceResultHandler(st *store.Store, credential string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		values := r.Header.Values("X-Contract-Service-Key")
		if len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), []byte(credential)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "service authentication required", "code": "service_unauthenticated"})
			return
		}
		var delivery store.ExperienceResultDelivery
		if err := readJSON(w, r, &delivery); err != nil {
			writeErr(w, err)
			return
		}
		if err := st.AcceptExperienceResult(r.Context(), delivery); err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
