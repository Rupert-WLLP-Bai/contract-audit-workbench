package httpapi

import (
	"net/http"
	"time"

	"contractaudit/internal/identity"
)

const oidcStateCookie = "oidc_state"

func (h *handler) oidcLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
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
	token, audience, err := h.oidc.Complete(r.Context(), code, state, cookie.Value)
	if err != nil {
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	destination, ok := oidcDestination(audience)
	if !ok {
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	h.setSessionCookie(w, r, token, audience, 7*24*3600)
	http.Redirect(w, r, destination, http.StatusSeeOther)
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
