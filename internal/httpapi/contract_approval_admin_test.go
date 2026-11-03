// File metadata (manifest mirror):
// Purpose: Checks that workflow configuration uses the original administrator session and rejects client, stale-context and missing-CSRF requests before reaching storage.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-03
// Updated: 2026-11-03
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/identity"
	"contractaudit/internal/store"
)

// These commands must use the same admin session for middleware and storage.
func TestWorkflowAdminCommandsUseOIDCAdminSession(t *testing.T) {
	f := newOIDCHTTPFixture(t, false)
	admin := f.newClient(t)
	flow := f.startFlow(t, admin, identity.AudienceAdmin)
	if got := f.request(t, admin, http.MethodGet, flow.callbackURL, nil); got.status != http.StatusSeeOther {
		t.Fatalf("admin callback: %d %s", got.status, got.body)
	}
	session := f.request(t, admin, http.MethodGet, "/api/session?audience=admin", nil)
	var proof store.SessionContext
	if session.status != http.StatusOK || json.Unmarshal(session.body, &proof) != nil || proof.ActiveContext.ContextVersion == "" || proof.CSRFToken == "" {
		t.Fatalf("admin session: %d %s", session.status, session.body)
	}
	client := f.newClient(t)
	flow = f.startFlow(t, client, identity.AudienceClient)
	if got := f.request(t, client, http.MethodGet, flow.callbackURL, nil); got.status != http.StatusSeeOther {
		t.Fatalf("client callback: %d %s", got.status, got.body)
	}
	clientCookie := f.sessionCookie(t, client, "session")
	adminCookie := f.sessionCookie(t, admin, "admin_session")
	misnamedClient := *clientCookie
	misnamedClient.Name = "admin_session"

	nodes := []store.WorkflowTemplateNodeInput{
		{NodeCode: "department_head", Title: "Department", RoleCode: "role_department"},
		{NodeCode: "business_domain", Title: "Business", RoleCode: "role_business"},
		{NodeCode: "internal_control", Title: "Control", RoleCode: "role_control"},
		{NodeCode: "legal_counsel", Title: "Counsel", RoleCode: "role_counsel"},
		{NodeCode: "legal_officer", Title: "Legal", RoleCode: "role_legal"},
		{NodeCode: "business_leader", Title: "Leader", RoleCode: "role_leader"},
		{NodeCode: "handler_final_check", Title: "Final", RoleCode: "role_final", Final: true},
	}
	template, err := json.Marshal(store.PublishWorkflowTemplateInput{TemplateID: "issue190", TemplateVersion: "1", Name: "Approval", StartAction: "submit_contract", Execution: "sequential", Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	binding := []byte(`{"bindings":[]}`)
	routes := []struct {
		method, path string
		body         []byte
		success      int
	}{
		{http.MethodPost, "/api/admin/contract-workflow/templates", template, http.StatusCreated},
		{http.MethodPut, "/api/admin/contract-workflow/roles/role_legal", binding, http.StatusOK},
	}
	request := func(routeMethod, path string, body []byte, cookies []*http.Cookie, version, csrf string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(routeMethod, f.base+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", f.base)
		req.Header.Set("Content-Type", "application/json")
		if version != "" {
			req.Header.Set("X-Context-Version", version)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		res, err := f.newBareClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		payload, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, payload
	}
	versionNumber, err := strconv.ParseInt(proof.ActiveContext.ContextVersion, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range routes {
		t.Run(route.path, func(t *testing.T) {
			for _, tc := range []struct {
				name          string
				cookies       []*http.Cookie
				version, csrf string
				want          int
			}{
				{"client only", []*http.Cookie{clientCookie}, proof.ActiveContext.ContextVersion, proof.CSRFToken, http.StatusUnauthorized},
				{"nonadmin token", []*http.Cookie{&misnamedClient}, proof.ActiveContext.ContextVersion, proof.CSRFToken, http.StatusForbidden},
				{"missing context", []*http.Cookie{adminCookie}, "", proof.CSRFToken, http.StatusPreconditionRequired},
				{"stale context", []*http.Cookie{adminCookie}, strconv.FormatInt(versionNumber+1, 10), proof.CSRFToken, http.StatusConflict},
				{"missing csrf", []*http.Cookie{adminCookie}, proof.ActiveContext.ContextVersion, "", http.StatusForbidden},
			} {
				t.Run(tc.name, func(t *testing.T) {
					status, body := request(route.method, route.path, route.body, tc.cookies, tc.version, tc.csrf)
					if status != tc.want {
						t.Fatalf("status=%d want=%d body=%s", status, tc.want, body)
					}
				})
			}
			status, body := request(route.method, route.path, route.body, []*http.Cookie{adminCookie}, proof.ActiveContext.ContextVersion, proof.CSRFToken)
			if status != route.success {
				t.Fatalf("admin-only status=%d want=%d body=%s", status, route.success, body)
			}
			var result map[string]any
			if err := json.Unmarshal(body, &result); err != nil {
				t.Fatal(err)
			}
			if route.method == http.MethodPost && (result["template_id"] != "issue190" || result["published_by"] != "oidc-admin" || result["template_digest"] == "") {
				t.Fatalf("template response: %s", body)
			}
			if route.method == http.MethodPut && (result["role_code"] != "role_legal" || result["updated_by"] != "oidc-admin" || result["row_version"] != float64(1)) {
				t.Fatalf("binding response: %s", body)
			}
			status, body = request(route.method, route.path, route.body, []*http.Cookie{adminCookie, clientCookie}, proof.ActiveContext.ContextVersion, proof.CSRFToken)
			if status != route.success {
				t.Fatalf("independent client cookie changed admin command: %d %s", status, body)
			}
		})
	}
}

// An unavailable store still proves both commands reached their real execution
// path after resolving the same admin session as the context middleware.
type workflowTestSessionAuthority struct{}

func (workflowTestSessionAuthority) AuthenticateSession(_ context.Context, token string) (accounts.User, string, error) {
	if token != "admin-token" {
		return accounts.User{}, "", fault.New(http.StatusUnauthorized, "session_invalid", "invalid session")
	}
	user := accounts.WithCommandAuthority(accounts.User{ID: "admin", Role: "admin", TenantID: "tenant", Enabled: true}, accounts.CommandAuthority{ContextVersion: "1", CSRFToken: "csrf", Revision: "1"})
	return user, "admin", nil
}

func TestWorkflowAdminCommandsReachStoreWithAdminSession(t *testing.T) {
	api := httpapi.Handler(&store.Store{}, httpapi.Options{SessionAuthorityResolver: workflowTestSessionAuthority{}, CORSOrigins: []string{"http://example.com"}})
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/admin/contract-workflow/templates"},
		{http.MethodPut, "/api/admin/contract-workflow/roles/role_legal"},
	} {
		t.Run(route.path, func(t *testing.T) {
			req := httptest.NewRequest(route.method, "http://example.com"+route.path, bytes.NewBufferString(`{}`))
			req.AddCookie(&http.Cookie{Name: "admin_session", Value: "admin-token"})
			req.Header.Set("Origin", "http://example.com")
			req.Header.Set("X-Context-Version", "1")
			req.Header.Set("X-CSRF-Token", "csrf")
			rec := httptest.NewRecorder()
			api.ServeHTTP(rec, req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d, want database unavailable after admin authentication: %s", rec.Code, rec.Body.String())
			}
		})
	}
}
