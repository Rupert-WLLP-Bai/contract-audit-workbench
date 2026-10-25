package httpapi

import (
	"net/http"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
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

func (h *handler) actorForAudience(w http.ResponseWriter, r *http.Request, audience string) (accounts.User, bool) {
	token := audienceCookieValue(r, audience)
	user, err := h.store.User(token)
	if err != nil {
		writeErr(w, err)
		return accounts.User{}, false
	}
	actual, err := h.store.SessionAudience(token)
	if err != nil {
		writeErr(w, err)
		return accounts.User{}, false
	}
	if actual != audience || (audience == "admin" && user.Role != "admin") {
		writeErr(w, fault.Forbidden("会话不具备此入口的授权"))
		return accounts.User{}, false
	}
	return user, true
}

func (h *handler) adminSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actorForAudience(w, r, "admin")
	if !ok {
		return
	}
	h.settingsForUser(w, r, user)
}
