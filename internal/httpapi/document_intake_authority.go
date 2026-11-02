package httpapi

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"contractaudit/internal/store"
)

const documentIntakeAuthorityPath = "/internal/v1/document/intake-authority"

type documentIntakeAuthorityRequest struct {
	Ticket     string `json:"ticket"`
	BodySHA256 string `json:"body_sha256"`
}

func documentIntakeAuthorityHandler(st *store.Store, credential string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
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
		var request documentIntakeAuthorityRequest
		if err := readJSON(w, r, &request); err != nil {
			writeErr(w, err)
			return
		}
		if len(request.Ticket) != 68 || !strings.HasPrefix(request.Ticket, "iat_") || len(request.BodySHA256) != 64 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "intake authority is unavailable", "code": "intake_unauthenticated"})
			return
		}
		authority, err := st.ResolveDocumentIntakeTicket(r.Context(), request.Ticket, request.BodySHA256)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, authority)
	}
}
