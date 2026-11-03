// File metadata (manifest mirror):
// Purpose: Exercises the `httpapi_test` package behavior through TestReviewDiagnosticsRequiresRoleAndCurrentCaseAccess, TestReviewDiagnosticsRechecksDirectoryGrantsAndRole, TestReviewDiagnosticsServiceOwnership; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-28
// Updated: 2026-10-28
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/store"
	"github.com/jackc/pgx/v5"
)

func TestReviewDiagnosticsRequiresRoleAndCurrentCaseAccess(t *testing.T) {
	api := newAPI(t)
	if status, _ := api.do(t, http.MethodGet, "/api/cases/absent/reviews/absent/diagnostics", nil); status != http.StatusUnauthorized {
		t.Fatalf("anonymous diagnostics status = %d", status)
	}
	if status, body := api.do(t, http.MethodPost, "/api/session", map[string]any{
		"username": "demo-admin", "password": "admin-demo", "audience": "admin",
	}); status != http.StatusOK {
		t.Fatalf("admin-audience login: %d %v", status, body)
	}
	if status, _ := api.do(t, http.MethodGet, "/api/cases/absent/reviews/absent/diagnostics", nil); status != http.StatusUnauthorized {
		t.Fatalf("admin-audience session alone authorized case diagnostics: %d", status)
	}

	api.login(t, "demo-admin", "admin-demo")
	caseID, runID, firstAttempt := createDiagnosticsReview(t, api, "受保护的诊断")
	for _, member := range []string{"demo-clerk", "demo-reviewer", "demo-viewer"} {
		if status, body := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/members", map[string]any{"actor_id": member}); status != http.StatusOK {
			t.Fatalf("add %s to case: %d %v", member, status, body)
		}
	}
	otherCase, otherRun, otherAttempt := createDiagnosticsReview(t, api, "另一案件")
	path := "/api/cases/" + caseID + "/reviews/" + runID
	diagnosticPath := path + "/diagnostics"

	if status, body := api.do(t, http.MethodGet, diagnosticPath+"?attempt_id="+firstAttempt, nil); status != http.StatusOK ||
		body["execution"].(map[string]any)["attempt"].(map[string]any)["id"] != firstAttempt ||
		body["case_id"] != caseID || body["run_id"] != runID {
		t.Fatalf("case member administrator could not read selected diagnostics: %d %v", status, body)
	}
	res, err := api.client.Get(api.base + diagnosticPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatal(err)
	}
	cacheControl := res.Header.Values("Cache-Control")
	noStore := false
	for _, value := range cacheControl {
		for _, directive := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(directive), "no-store") {
				noStore = true
			}
		}
	}
	if res.StatusCode != http.StatusOK || !noStore {
		t.Fatalf("diagnostics response cache policy: %d %q", res.StatusCode, cacheControl)
	}
	for _, invalid := range []string{
		"/api/cases/" + caseID + "/reviews/" + otherRun + "/diagnostics",
		"/api/cases/" + otherCase + "/reviews/" + runID + "/diagnostics",
		diagnosticPath + "?attempt_id=" + otherAttempt,
		diagnosticPath + "?attempt_id=missing-attempt",
	} {
		if status, _ := api.do(t, http.MethodGet, invalid, nil); status != http.StatusNotFound {
			t.Fatalf("mixed case/run/attempt diagnostics %s returned %d", invalid, status)
		}
	}
	status, retry := api.do(t, http.MethodPost, path+"/retry", map[string]any{})
	if status != http.StatusOK || retry["attempt_id"] == firstAttempt {
		t.Fatalf("retry did not create distinct history: %d %v", status, retry)
	}
	latestAttempt := retry["attempt_id"].(string)
	for _, selected := range []string{firstAttempt, latestAttempt} {
		status, body := api.do(t, http.MethodGet, diagnosticPath+"?attempt_id="+selected, nil)
		if status != http.StatusOK || body["execution"].(map[string]any)["attempt"].(map[string]any)["id"] != selected {
			t.Fatalf("historical diagnostic selection %s: %d %v", selected, status, body)
		}
	}

	for _, actor := range []struct{ username, password string }{
		{"demo-clerk", "clerk-demo"}, {"demo-reviewer", "reviewer-demo"}, {"demo-viewer", "viewer-demo"},
	} {
		api.login(t, actor.username, actor.password)
		if status, body := api.do(t, http.MethodGet, path, nil); status != http.StatusOK {
			t.Fatalf("case member %s cannot read business workspace: %d %v", actor.username, status, body)
		}
		if status, _ := api.do(t, http.MethodGet, diagnosticPath, nil); status != http.StatusForbidden {
			t.Fatalf("case member %s accessed diagnostics: %d", actor.username, status)
		}
	}

	api.login(t, "demo-clerk", "clerk-demo")
	privateCase, privateRun, _ := createDiagnosticsReview(t, api, "管理员未获授权的案件")
	api.login(t, "demo-admin", "admin-demo")
	if status, _ := api.do(t, http.MethodGet, "/api/cases/"+privateCase+"/reviews/"+privateRun+"/diagnostics", nil); status != http.StatusNotFound {
		t.Fatalf("administrator role bypassed case membership: %d", status)
	}
	api.login(t, "demo-tenant2-admin", "tenant2-admin-demo")
	if status, _ := api.do(t, http.MethodGet, diagnosticPath, nil); status != http.StatusNotFound {
		t.Fatalf("cross-tenant administrator accessed diagnostics: %d", status)
	}
}

func TestReviewDiagnosticsRechecksMembershipGrantsAndRole(t *testing.T) {
	user := accounts.User{
		ID: "diagnostic-admin", DisplayName: "Diagnostic Admin", Role: "admin", Enabled: true,
		TenantID: "diagnostic-tenant", OrgID: "diagnostic-org", Issuer: "https://identity.example.test", Subject: "diagnostic-admin",
		Grants: []access.Grant{{Role: "case_owner", Scope: "self"}},
	}
	directory := filepath.Join(t.TempDir(), "directory.json")
	writeDirectory := func() {
		t.Helper()
		content, err := json.Marshal(struct {
			Users []accounts.User `json:"users"`
		}{Users: []accounts.User{user}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(directory, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeDirectory()
	if err := accounts.ConfigureDirectory(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := accounts.ConfigureDirectory(""); err != nil {
			t.Errorf("restore identity directory: %v", err)
		}
	})
	testDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if testDSN == "" {
		t.Skip("TEST_DATABASE_URL is required for membership-backed diagnostics")
	}
	config, err := pgx.ParseConfig(testDSN)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenTest(t.TempDir(), testDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	st.UseModels(scriptModels{})
	crosswalk := store.IdentityCrosswalk{
		Version:     1,
		Tenants:     []store.IdentityTenantMap{{ID: user.TenantID, DisplayName: "Diagnostic tenant", Status: "active"}},
		OrgUnits:    []store.IdentityOrgUnitMap{{TenantID: user.TenantID, ID: user.OrgID, DisplayName: "Diagnostic org", Status: "active"}},
		Principals:  []store.IdentityPrincipalMap{{ID: "30000000-0000-4000-8000-000000000001", ActorID: user.ID, Issuer: user.Issuer, Subject: user.Subject, Status: "active"}},
		Memberships: []store.IdentityMembershipMap{{ID: "40000000-0000-4000-8000-000000000001", ActorID: user.ID, TenantID: user.TenantID, OrgUnitID: user.OrgID, RoleCode: "admin", Grants: []access.Grant{{Role: "case_owner", Scope: "self"}}, Status: "active"}},
	}
	applyMembership := func() {
		t.Helper()
		if _, err := store.ImportIdentityCrosswalk(context.Background(), testDSN, st.SchemaName(), config.Database, crosswalk, true); err != nil {
			t.Fatalf("apply diagnostic membership: %v", err)
		}
	}
	applyMembership()
	// Resolve the verified issuer/subject through the same database membership
	// path used by OIDC, then keep the resulting session and original proof.
	login, err := st.ResolveVerifiedOIDC(context.Background(), user, "client")
	if err != nil || login.SessionToken == "" {
		t.Fatalf("resolve diagnostic membership: %v", err)
	}
	metadata, err := store.UserSessionContext(login.User)
	if err != nil || metadata.ActiveContext.ContextVersion == "" || metadata.CSRFToken == "" {
		t.Fatalf("diagnostic session proof: %v", err)
	}
	server := httptest.NewServer(httpapi.Handler(st, httpapi.Options{}))
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(baseURL, []*http.Cookie{{Name: "session", Value: login.SessionToken, Path: "/"}})
	api := &testAPI{base: server.URL, client: &http.Client{Jar: jar}, proofs: map[string]sessionProof{"client": {version: metadata.ActiveContext.ContextVersion, csrf: metadata.CSRFToken}}}
	caseID, runID, _ := createDiagnosticsReview(t, api, "可撤销授权")
	path := "/api/cases/" + caseID + "/reviews/" + runID + "/diagnostics"
	if status, _ := api.do(t, http.MethodGet, path, nil); status != http.StatusOK {
		t.Fatalf("initial authorized diagnostic read: %d", status)
	}

	crosswalk.Memberships[0].Grants = []access.Grant{}
	applyMembership()
	if status, _ := api.do(t, http.MethodGet, path, nil); status != http.StatusNotFound {
		t.Fatalf("revoked case grant remained valid for the existing session: %d", status)
	}
	crosswalk.Memberships[0].RoleCode = "viewer"
	crosswalk.Memberships[0].Grants = []access.Grant{{Role: "viewer", Scope: "self"}}
	applyMembership()
	if status, _ := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+runID, nil); status != http.StatusOK {
		t.Fatalf("viewer with restored case grant cannot read business workspace: %d", status)
	}
	if status, _ := api.do(t, http.MethodGet, path, nil); status != http.StatusForbidden {
		t.Fatalf("role change was not enforced for the existing session: %d", status)
	}
	user.Enabled = false
	writeDirectory()
	if status, _ := api.do(t, http.MethodGet, path, nil); status != http.StatusUnauthorized {
		t.Fatalf("disabled identity retained diagnostic access: %d", status)
	}
}

func TestReviewDiagnosticsServiceOwnership(t *testing.T) {
	path := "/api/cases/case-1/reviews/run-1/diagnostics"
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		for _, role := range []string{"core", "document", "execution"} {
			if got := httpapi.OperationOwnedBy(role, method, path); got != (role == "execution") {
				t.Errorf("%s diagnostics ownership for %s = %t", method, role, got)
			}
		}
	}
	if httpapi.OperationOwnedBy("execution", http.MethodPost, path) {
		t.Fatal("diagnostics unexpectedly accepts a write method")
	}
}

func createDiagnosticsReview(t *testing.T, api *testAPI, title string) (string, string, string) {
	t.Helper()
	status, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{"title": title, "contract_type": "procurement_ict"})
	if status != http.StatusCreated {
		t.Fatalf("create case: %d %v", status, created)
	}
	caseID := created["id"].(string)
	api.upload(t, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件质保36个月。"))
	api.upload(t, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同质保24个月。"))
	status, review := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("start review: %d %v", status, review)
	}
	return caseID, review["id"].(string), review["attempt_id"].(string)
}
