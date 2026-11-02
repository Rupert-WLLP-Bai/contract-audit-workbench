package httpapi

import (
	"context"
	"net/http"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

func validAudience(raw string) (string, error) {
	switch raw {
	case "", "client":
		return "client", nil
	case "admin":
		return "admin", nil
	default:
		return "", fault.Invalid("audience 必须为 client 或 admin")
	}
}

func audienceCookieValue(r *http.Request, audience string) string {
	name := sessionCookie
	if audience == "admin" {
		name = "admin_session"
	}
	cookie, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (h *handler) setSessionCookie(w http.ResponseWriter, r *http.Request, token, audience string, maxAge int) {
	cookie := sessionCookieOf(token, maxAge)
	if audience == "admin" {
		cookie.Name = "admin_session"
	}
	cookie.Secure = r.TLS != nil || h.secureCookies
	http.SetCookie(w, cookie)
}

type authenticatedRequestKey struct{}

type authenticatedRequest struct {
	user     accounts.User
	audience string
	token    string
}

func (h *handler) actorForAudience(w http.ResponseWriter, r *http.Request, audience string) (accounts.User, bool) {
	token := audienceCookieValue(r, audience)
	if cached, ok := r.Context().Value(authenticatedRequestKey{}).(authenticatedRequest); ok &&
		cached.audience == audience && cached.token == token {
		return cached.user, true
	}
	user, actual, err := h.authenticateSession(r.Context(), token)
	if err != nil {
		writeErr(w, err)
		return accounts.User{}, false
	}
	if actual != audience || (audience == "admin" && user.Role != "admin") {
		writeErr(w, fault.Forbidden("会话不具备此入口的授权"))
		return accounts.User{}, false
	}
	// The unexported value is tied to this request and cookie; it is never shared
	// across requests or used for periodic revalidation of a long-lived stream.
	*r = *r.WithContext(context.WithValue(r.Context(), authenticatedRequestKey{}, authenticatedRequest{
		user: user, audience: audience, token: token,
	}))
	*r = *r.WithContext(store.WithAdmissionSessionToken(r.Context(), audienceCookieValue(r, audience)))
	return user, true
}

func (h *handler) authenticateSession(ctx context.Context, token string) (accounts.User, string, error) {
	if h.sessionAuthority != nil {
		return h.sessionAuthority.AuthenticateSession(ctx, token)
	}
	return h.store.AuthenticateSession(ctx, token)
}

func (h *handler) adminSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	h.settingsForUser(w, r, user)
}
