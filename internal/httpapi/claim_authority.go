package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"contractaudit/internal/operationauthority"
	"contractaudit/internal/store"
)

const maxClaimAuthorityRequest = 4 << 10

type claimAuthorityRequest = operationauthority.ClaimRequest
type claimAuthorityResponse = operationauthority.ClaimProjection

func claimAuthorityHandler(st *store.Store, credential, phase string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.RawQuery != "" || !claimAuthorityBearer(r.Header.Values("Authorization"), credential) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "claim authority unavailable", "code": "claim_unauthenticated"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxClaimAuthorityRequest)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request claimAuthorityRequest
		if err := decoder.Decode(&request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid claim authority request", "code": "claim_invalid"})
			return
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid claim authority request", "code": "claim_invalid"})
			return
		}
		authority, err := st.ResolveWorkerClaimAuthority(r.Context(), store.ToolInvocationContext{
			TenantID: request.TenantID, OrgID: request.OrgID, JobID: request.JobID, WorkerID: request.WorkerID,
			Generation: request.Generation, CaseID: request.CaseID, RunID: request.RunID, AttemptID: request.AttemptID,
		}, phase)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "current job authority is unavailable", "code": "claim_unavailable"})
			return
		}
		user := authority.User
		writeJSON(w, http.StatusOK, claimAuthorityResponse{
			JobID: authority.JobID, CaseID: authority.CaseID, RunID: authority.RunID, AttemptID: authority.AttemptID,
			Audience: "worker_claim", UserOrgID: user.OrgID, MembershipBound: user.HasResolvedMembership(), ActorID: user.ID,
			DisplayName: user.DisplayName, Role: user.Role, TenantID: authority.TenantID, OrgID: authority.OrgID,
			Enabled: user.Enabled, Issuer: user.Issuer, Subject: user.Subject,
			PrincipalID: user.PrincipalID, MembershipID: user.MembershipID, Grants: user.Grants,
		})
	}
}

func claimAuthorityBearer(values []string, expected string) bool {
	if len(values) != 1 || len(expected) < 32 {
		return false
	}
	parts := strings.SplitN(values[0], " ", 2)
	return len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && subtle.ConstantTimeCompare([]byte(parts[1]), []byte(expected)) == 1
}
