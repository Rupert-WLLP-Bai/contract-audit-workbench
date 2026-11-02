package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"contractaudit/internal/model"
	"contractaudit/internal/operationauthority"
	"contractaudit/internal/store"
)

func operationAuthorityHandler(st *store.Store, credential, caller string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost || r.URL.RawQuery != "" || !claimAuthorityBearer(r.Header.Values("Authorization"), credential) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var scope model.DelegatedScope
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
		d.DisallowUnknownFields()
		if d.Decode(&scope) != nil || d.Decode(new(any)) != io.EOF {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		u, err := st.ResolveOperationAuthority(r.Context(), scope, caller)
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		revision := ""
		if proof, ok := u.CommandAuthority(); ok {
			revision = proof.Revision
		}
		writeJSON(w, http.StatusOK, operationauthority.Projection{AuthorityRevision: revision, Scope: scope, User: u, MembershipBound: u.HasResolvedMembership(), PrincipalID: u.PrincipalID, MembershipID: u.MembershipID})
	}
}

func sessionOperationTicketHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost || r.URL.RawQuery != "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		values := r.Header.Values("Authorization")
		if len(values) != 1 || len(values[0]) < 8 || values[0][:7] != "Bearer " {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		token := values[0][7:]
		u, aud, err := st.AuthenticateSession(r.Context(), token)
		if err != nil || aud != "client" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var scope model.DelegatedScope
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
		d.DisallowUnknownFields()
		if d.Decode(&scope) != nil || d.Decode(new(any)) != io.EOF {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ticket, err := st.CreateOperationTicket(store.WithAdmissionSessionToken(r.Context(), token), u, scope)
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ticket": ticket})
	}
}

func (r *coreSessionResolver) IssueOperationTicket(ctx context.Context, token string, scope model.DelegatedScope) (string, error) {
	body, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	endpoint := r.endpoint[:len(r.endpoint)-len("/internal/v1/session-authority")] + "/internal/v1/session-operation-ticket"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return "", errors.New("operation ticket unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", errors.New("operation ticket refused")
	}
	var result struct {
		Ticket string `json:"ticket"`
	}
	d := json.NewDecoder(io.LimitReader(response.Body, 16384))
	d.DisallowUnknownFields()
	if d.Decode(&result) != nil || d.Decode(new(any)) != io.EOF {
		return "", errors.New("invalid operation ticket")
	}
	return result.Ticket, nil
}

func documentOperationTicketHandler(st *store.Store, credential string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "POST" || r.URL.RawQuery != "" || !claimAuthorityBearer(r.Header.Values("Authorization"), credential) {
			w.WriteHeader(401)
			return
		}
		var in struct {
			Digest  string               `json:"session_digest"`
			Version string               `json:"context_version"`
			Scope   model.DelegatedScope `json:"scope"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
		d.DisallowUnknownFields()
		if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF {
			w.WriteHeader(400)
			return
		}
		ticket, err := st.IssueOperationTicketFromDigest(r.Context(), in.Digest, in.Version, in.Scope)
		if err != nil {
			w.WriteHeader(403)
			return
		}
		writeJSON(w, 200, map[string]string{"ticket": ticket})
	}
}
