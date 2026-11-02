// File metadata (manifest mirror):
// Purpose: Resolves the session audience and actor from validated session state, applies audience-specific cookie attributes and keeps client/admin identity scopes separate.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package httpapi

import (
	"context"
	"errors"
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
		var failure *fault.Error
		if errors.As(err, &failure) && failure.Status == 401 {
			err = fault.New(401, "session_invalid", "会话已失效")
		}
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

// reauthenticateDownload bypasses the request cache immediately before a file
// is sent. The original session and context must still identify the same actor;
// the returned user carries freshly resolved roles and grants.
func (h *handler) reauthenticateDownload(r *http.Request, original accounts.User) (accounts.User, error) {
	cached, ok := r.Context().Value(authenticatedRequestKey{}).(authenticatedRequest)
	if !ok || cached.audience != "client" || cached.token == "" || audienceCookieValue(r, cached.audience) != cached.token {
		return accounts.User{}, fault.New(http.StatusUnauthorized, "session_invalid", "会话已失效")
	}
	oldProof, oldOK := original.CommandAuthority()
	if !oldOK || oldProof.SessionDigest == "" || oldProof.ContextVersion == "" {
		return accounts.User{}, fault.New(http.StatusUnauthorized, "session_invalid", "会话上下文不可用")
	}
	current, audience, err := h.authenticateSession(r.Context(), cached.token)
	if err != nil {
		return accounts.User{}, err
	}
	newProof, newOK := current.CommandAuthority()
	if !newOK || !current.Enabled || audience != cached.audience ||
		current.ID != original.ID || current.PrincipalID != original.PrincipalID || current.MembershipID != original.MembershipID ||
		current.TenantID != original.TenantID || current.OrgID != original.OrgID || current.Issuer != original.Issuer || current.Subject != original.Subject ||
		newProof.SessionDigest != oldProof.SessionDigest || newProof.ContextVersion != oldProof.ContextVersion {
		return accounts.User{}, fault.New(http.StatusForbidden, "access_denied", "下载期间会话或工作上下文已变更")
	}
	return current, nil
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
