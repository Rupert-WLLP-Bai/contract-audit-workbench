package httpapi

import (
	"net/http"
	"strings"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/store"
)

type coreSessionAuthorityResponse struct {
	Audience        string         `json:"audience"`
	MembershipBound bool           `json:"membership_bound"`
	ActorID         string         `json:"actor_id"`
	DisplayName     string         `json:"display_name"`
	Role            string         `json:"role"`
	TenantID        string         `json:"tenant_id"`
	OrgID           string         `json:"org_id"`
	Enabled         bool           `json:"enabled"`
	Issuer          string         `json:"issuer,omitempty"`
	Subject         string         `json:"subject,omitempty"`
	PrincipalID     string         `json:"principal_id,omitempty"`
	MembershipID    string         `json:"membership_id,omitempty"`
	Grants          []access.Grant `json:"grants"`
}

func coreSessionAuthorityHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		values := r.Header.Values("Authorization")
		if len(values) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session authentication required", "code": "session_unauthenticated"})
			return
		}
		parts := strings.SplitN(values[0], " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" ||
			strings.ContainsAny(parts[1], " \t\r\n") || len(parts[1]) > 4096 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session authentication required", "code": "session_unauthenticated"})
			return
		}
		user, audience, err := st.AuthenticateSession(r.Context(), parts[1])
		if err != nil || (audience != "client" && audience != "admin") || !user.Enabled || (audience == "admin" && user.Role != "admin") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session authentication required", "code": "session_unauthenticated"})
			return
		}
		response := coreSessionAuthorityResponse{
			Audience: audience, MembershipBound: user.HasResolvedMembership(), ActorID: user.ID,
			DisplayName: user.DisplayName, Role: user.Role, TenantID: accounts.Tenant(user), OrgID: user.OrgID,
			Enabled: user.Enabled, Issuer: user.Issuer, Subject: user.Subject,
			PrincipalID: user.PrincipalID, MembershipID: user.MembershipID,
		}
		response.Grants = append([]access.Grant(nil), user.Grants...)
		writeJSON(w, http.StatusOK, response)
	}
}
