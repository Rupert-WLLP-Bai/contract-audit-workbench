package httpapi

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

func (h *handler) sessionJSON(user accounts.User, audience string) (map[string]any, error) {
	metadata, err := store.UserSessionContext(user)
	if err != nil {
		return nil, err
	}
	var principal any
	if user.PrincipalID != "" {
		principal = user.PrincipalID
	}
	return map[string]any{"principal_id": principal, "actor": actorJSON(user), "audience": audience, "active_context": metadata.ActiveContext, "csrf_token": metadata.CSRFToken, "context_switch_enabled": h.contextSwitchEnabled}, nil
}
func (h *handler) listSessionContexts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	audience, err := validAudience(r.URL.Query().Get("audience"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if _, ok := h.actorForAudience(w, r, audience); !ok {
		return
	}
	choices, err := h.store.ListSessionContexts(r.Context(), audienceCookieValue(r, audience))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memberships": choices, "context_switch_enabled": h.contextSwitchEnabled})
}
func (h *handler) switchSessionContext(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.contextSwitchEnabled {
		writeErr(w, fault.New(403, "context_switch_disabled", "上下文切换尚未开放"))
		return
	}
	audience, err := validAudience(r.URL.Query().Get("audience"))
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		MembershipID string `json:"membership_id"`
		Expected     string `json:"expected_context_version"`
	}
	if err = readJSON(w, r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if body.Expected != r.Header.Get("X-Context-Version") {
		writeErr(w, fault.New(409, "context_stale", "切换命令版本不一致"))
		return
	}
	token := audienceCookieValue(r, audience)
	if err = h.store.SwitchSessionContext(r.Context(), token, body.Expected, body.MembershipID); err != nil {
		writeErr(w, err)
		return
	}
	user, actual, err := h.authenticateSession(r.Context(), token)
	if err != nil {
		writeErr(w, fault.New(401, "session_invalid", "会话已失效"))
		return
	}
	result, err := h.sessionJSON(user, actual)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) contextCommands(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		// These are the only pre-session operations. OIDC pending selection has its
		// own exact state/nonce challenge and never authenticates an existing session.
		if r.Method == http.MethodPost && (r.URL.Path == "/api/session" || r.URL.Path == "/api/auth/oidc/memberships") {
			next.ServeHTTP(w, r)
			return
		}
		audience := "client"
		if strings.HasPrefix(r.URL.Path, "/api/admin/") || r.URL.Query().Get("audience") == "admin" {
			audience = "admin"
		}
		user, ok := h.actorForAudience(w, r, audience)
		if !ok {
			return
		}
		headers := r.Header.Values("X-Context-Version")
		if len(headers) != 1 || !store.ValidContextVersion(headers[0]) {
			writeErr(w, fault.New(428, "context_required", "写入命令需要原始上下文版本"))
			return
		}
		proof, ok := user.CommandAuthority()
		if !ok {
			writeErr(w, fault.New(401, "session_invalid", "会话上下文不可用"))
			return
		}
		if proof.ContextVersion != headers[0] {
			writeErr(w, fault.New(409, "context_stale", "上下文已变更，请重新读取会话"))
			return
		}
		csrf := r.Header.Values("X-CSRF-Token")
		if !h.commandOriginAllowed(r) || len(csrf) != 1 || proof.CSRFToken == "" || subtle.ConstantTimeCompare([]byte(csrf[0]), []byte(proof.CSRFToken)) != 1 {
			writeErr(w, fault.New(403, "csrf_invalid", "命令来源或会话校验无效"))
			return
		}
		*r = *r.WithContext(store.BindCommandContext(r.Context(), audienceCookieValue(r, audience), headers[0], proof.Revision))
		next.ServeHTTP(w, r)
	})
}
func (h *handler) commandOriginAllowed(r *http.Request) bool {
	trusted := func(raw string, referer bool) bool {
		origin, ok := browserOrigin(raw, referer)
		if !ok {
			return false
		}
		if own := directRequestOrigin(r, h.secureCookies); own != "" && origin == own {
			return true
		}
		for _, configured := range h.cors {
			if expected, ok := browserOrigin(configured, false); ok && expected == origin {
				return true
			}
		}
		return false
	}
	if origins := r.Header.Values("Origin"); len(origins) > 0 {
		return len(origins) == 1 && trusted(origins[0], false)
	}
	refs := r.Header.Values("Referer")
	return len(refs) == 1 && trusted(refs[0], true)
}
