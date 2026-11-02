package httpapi

import (
	"net/http"
	"time"

	"contractaudit/internal/identity"
)

const oidcStateCookie = "oidc_state"
const oidcPendingCookie = "oidc_pending"

func (h *handler) oidcLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	clearPendingCookie(w, r, h.secureCookies)
	if h.oidc == nil {
		http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
		return
	}
	query := r.URL.Query()
	audiences, hasAudience := query["audience"]
	if !hasAudience || len(audiences) != 1 || (audiences[0] != identity.AudienceClient && audiences[0] != identity.AudienceAdmin) {
		http.Error(w, "invalid authentication audience", http.StatusBadRequest)
		return
	}
	audience := audiences[0]
	authorizationURL, stateCookie, err := h.oidc.Begin(r.Context(), audience)
	if err != nil {
		http.Error(w, "authentication unavailable", http.StatusBadGateway)
		return
	}
	secure := r.TLS != nil || h.secureCookies
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookie, Value: stateCookie, Path: "/api/auth/oidc",
		MaxAge: int(identity.StateCookieLifetime.Seconds()), Expires: time.Now().Add(identity.StateCookieLifetime),
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, authorizationURL, http.StatusFound)
}

func (h *handler) oidcCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	secure := r.TLS != nil || h.secureCookies
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookie, Value: "", Path: "/api/auth/oidc",
		MaxAge: -1, Expires: time.Unix(1, 0),
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
	if h.oidc == nil {
		http.Error(w, "authentication failed", http.StatusServiceUnavailable)
		return
	}
	code, state, cookie, ok := oidcCallbackValues(r)
	if !ok {
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	result, audience, err := h.oidc.CompleteWithMembership(r.Context(), code, state, cookie.Value)
	if err != nil {
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	if result.PendingToken != "" {
		http.SetCookie(w, &http.Cookie{Name: oidcPendingCookie, Value: result.PendingToken,
			Path: "/api/auth/oidc/memberships", MaxAge: int(identity.StateCookieLifetime.Seconds()),
			Expires: time.Now().Add(identity.StateCookieLifetime), HttpOnly: true, Secure: secure,
			SameSite: http.SameSiteStrictMode})
		destination, ok := oidcPendingDestination(audience)
		if !ok {
			http.Error(w, "authentication failed", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, destination, http.StatusSeeOther)
		return
	}
	destination, ok := oidcDestination(audience)
	if !ok {
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	h.setSessionCookie(w, r, result.SessionToken, audience, 7*24*3600)
	http.Redirect(w, r, destination, http.StatusSeeOther)
}

func oidcPendingDestination(audience string) (string, bool) {
	switch audience {
	case identity.AudienceClient:
		return "/login/membership", true
	case identity.AudienceAdmin:
		return "/admin/login/membership", true
	default:
		return "", false
	}
}

func (h *handler) pendingMemberships(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	cookie, err := r.Cookie(oidcPendingCookie)
	if err != nil {
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	audiences := r.URL.Query()["audience"]
	if len(audiences) != 1 || (audiences[0] != identity.AudienceClient && audiences[0] != identity.AudienceAdmin) {
		http.Error(w, "invalid authentication audience", http.StatusBadRequest)
		return
	}
	choices, err := h.store.ListPendingMemberships(r.Context(), cookie.Value, audiences[0])
	if err != nil {
		clearPendingCookie(w, r, h.secureCookies)
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memberships": choices})
}

func clearPendingCookie(w http.ResponseWriter, r *http.Request, secureCookies bool) {
	http.SetCookie(w, &http.Cookie{Name: oidcPendingCookie, Value: "", Path: "/api/auth/oidc/memberships",
		MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true, Secure: r.TLS != nil || secureCookies,
		SameSite: http.SameSiteStrictMode})
}

func (h *handler) selectPendingMembership(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !pendingChoiceOriginAllowed(r, h.cors, h.secureCookies) {
		http.Error(w, "request origin is not allowed", http.StatusForbidden)
		return
	}
	cookie, err := r.Cookie(oidcPendingCookie)
	if err != nil {
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	var request struct {
		MembershipID string `json:"membership_id"`
		Audience     string `json:"audience"`
	}
	if err := readJSON(w, r, &request); err != nil || request.MembershipID == "" || len(request.MembershipID) > 64 ||
		(request.Audience != identity.AudienceClient && request.Audience != identity.AudienceAdmin) {
		http.Error(w, "invalid membership selection", http.StatusBadRequest)
		return
	}
	result, err := h.store.ConsumePendingMembership(r.Context(), cookie.Value, request.MembershipID, request.Audience)
	if err != nil {
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	secure := r.TLS != nil || h.secureCookies
	http.SetCookie(w, &http.Cookie{Name: oidcPendingCookie, Value: "", Path: "/api/auth/oidc/memberships",
		MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
	h.setSessionCookie(w, r, result.SessionToken, request.Audience, 7*24*3600)
	w.WriteHeader(http.StatusNoContent)
}

func pendingChoiceOriginAllowed(r *http.Request, configured []string, secureCookies bool) bool {
	values := r.Header.Values("Origin")
	if len(values) != 1 {
		return false
	}
	origin, ok := browserOrigin(values[0], false)
	if !ok {
		return false
	}
	if own := directRequestOrigin(r, secureCookies); own != "" && own == origin {
		return true
	}
	for _, value := range configured {
		if candidate, valid := browserOrigin(value, false); valid && candidate == origin {
			return true
		}
	}
	return false
}

func oidcCallbackValues(r *http.Request) (string, string, *http.Cookie, bool) {
	query := r.URL.Query()
	if _, hasError := query["error"]; hasError {
		return "", "", nil, false
	}
	codes, hasCode := query["code"]
	states, hasState := query["state"]
	cookie, err := r.Cookie(oidcStateCookie)
	if err != nil || !hasCode || len(codes) != 1 || !hasState || len(states) != 1 ||
		len(codes[0]) == 0 || len(codes[0]) > 4096 || len(states[0]) == 0 || len(states[0]) > 256 {
		return "", "", nil, false
	}
	return codes[0], states[0], cookie, true
}

func oidcDestination(audience string) (string, bool) {
	switch audience {
	case identity.AudienceClient:
		return "/", true
	case identity.AudienceAdmin:
		return "/admin", true
	default:
		return "", false
	}
}
