package bootstrap_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
	"github.com/jackc/pgx/v5"
)

// Exercise actual separate, non-superuser service Stores. The profile's stale
// tenant/role must not replace the admitted database membership at any edge.
func TestDistributedMembershipAdmissionAndModelSettlement(t *testing.T) {
	env := newServiceEnvironment(t, http.NotFoundHandler())
	profile := accounts.User{ID: "operation-enterprise-user", DisplayName: "Enterprise", Role: "viewer", TenantID: "untrusted-directory-tenant", OrgID: "untrusted-directory-org", Enabled: true, Issuer: "https://operation-issuer.invalid", Subject: "opaque-subject"}
	raw, _ := json.Marshal(map[string]any{"users": []accounts.User{profile}})
	path := filepath.Join(t.TempDir(), "directory.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
	defer accounts.ConfigureDirectory("")
	cfg, err := pgx.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	membership := "96666666-6666-4666-8666-666666666666"
	crosswalk := store.IdentityCrosswalk{Version: 1, Tenants: []store.IdentityTenantMap{{ID: "operation-tenant", DisplayName: "Operation tenant", Status: "active"}}, OrgUnits: []store.IdentityOrgUnitMap{{TenantID: "operation-tenant", ID: "operation-org", DisplayName: "Operation org", Status: "active"}}, Principals: []store.IdentityPrincipalMap{{ID: "95555555-5555-4555-8555-555555555555", ActorID: profile.ID, Issuer: profile.Issuer, Subject: profile.Subject, Status: "active"}}, Memberships: []store.IdentityMembershipMap{{ID: membership, ActorID: profile.ID, TenantID: "operation-tenant", OrgUnitID: "operation-org", RoleCode: "clerk", Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}, Status: "active"}}}
	if _, err = store.ImportIdentityCrosswalk(env.ctx, os.Getenv("TEST_DATABASE_URL"), env.schema, cfg.Database, crosswalk, true); err != nil {
		t.Fatal(err)
	}
	login, err := env.core.Store.ResolveVerifiedOIDC(env.ctx, profile, "client")
	if err != nil || login.SessionToken == "" || login.User.Role != "clerk" || login.User.TenantID != "operation-tenant" {
		t.Fatalf("database login failed: %v", err)
	}
	user := login.User
	ctx := store.WithAdmissionSessionToken(env.ctx, login.SessionToken)
	caseID, err := env.core.Store.CreateCase(user, "Membership operation", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = env.document.Store.AddDocument(ctx, user, caseID, "procurement", "requirements.txt", "text/plain", []byte("采购要求质保36个月。")); err != nil {
		t.Fatalf("enterprise Document upload: %v", err)
	}
	var imageBody bytes.Buffer
	if err = png.Encode(&imageBody, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	docID, err := env.document.Store.AddDocument(ctx, user, caseID, "draft_contract", "draft.png", "image/png", imageBody.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := httpapi.NewCoreSessionResolver(env.coreAuthorityURL, "", "test", true, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	edge := strings.Repeat("e", 40)
	handler, err := httpapi.ServiceHandler(env.execution.Store, httpapi.Options{Durable: true, Models: env.execution.Models, SessionAuthorityResolver: resolver}, "execution", edge)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	request, _ := http.NewRequestWithContext(env.ctx, "POST", server.URL+"/api/cases/"+caseID+"/reviews", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", server.URL)
	request.Header.Set("X-Contract-Service-Key", edge)
	request.AddCookie(&http.Cookie{Name: "session", Value: login.SessionToken})
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	response.Body.Close()
	if response.StatusCode != 202 {
		t.Fatalf("real Execution HTTP admission status=%d body=%s", response.StatusCode, body)
	}
	var accepted store.JobAcceptance
	if json.Unmarshal(body, &accepted) != nil || accepted.JobID == "" {
		t.Fatal("admission response missing job")
	}
	claim, err := env.worker.Store.ClaimNextJob(env.ctx, "operation-worker", time.Minute)
	if err != nil || claim.JobID != accepted.JobID {
		t.Fatalf("claim admitted job: %v", err)
	}
	if _, err = env.db.Exec(env.ctx, `UPDATE `+pgx.Identifier{env.schema, "review_runs"}.Sanitize()+` SET state='running' WHERE id=$1`, claim.RunID); err != nil {
		t.Fatal(err)
	}
	var selection store.AttemptParseSelection
	var selectionJSON []byte
	if err = env.db.QueryRow(env.ctx, `SELECT row_to_json(p) FROM `+pgx.Identifier{env.schema, "review_attempt_parse_selections"}.Sanitize()+` p WHERE p.attempt_id=$1 AND p.document_id=$2`, claim.AttemptID, docID).Scan(&selectionJSON); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(selectionJSON, &selection); err != nil {
		t.Fatal(err)
	}
	selectionJSON, _ = json.Marshal(selection)
	selectionHash := sha256.Sum256(selectionJSON)
	scope := model.DelegatedScope{Kind: "document", TenantID: claim.TenantID, OrgID: claim.OrgID, CaseID: claim.CaseID, ActorID: claim.ActorID, JobID: claim.JobID, WorkerID: claim.WorkerID, Generation: claim.Generation, RunID: claim.RunID, AttemptID: claim.AttemptID, DocumentID: docID, ParseSelectionID: selection.ParsedDocumentID, ParseSelectionSHA256: hex.EncodeToString(selectionHash[:])}
	limits := store.ModelBudgetLimits{GlobalMaxInFlight: 4, TenantMaxInFlight: 4, GlobalCallsPerMinute: 20, TenantCallsPerMinute: 20, GlobalTokensPerMinute: 10000, TenantTokensPerMinute: 10000, MaxInputTokenReservation: 1000, MaxOutputTokenReservation: 1000, LeaseDuration: time.Minute}
	lease, err := env.model.Store.ReserveModelCall(env.ctx, "document", scope, "controlled-chat", 1, 10, 10, limits)
	if err != nil {
		t.Fatalf("real Model authority/budget reserve: %v", err)
	}
	// Revocation denies new sends/adoption, but cannot erase a real incurred cost.
	crosswalk.Memberships[0].Status = "revoked"
	if _, err = store.ImportIdentityCrosswalk(env.ctx, os.Getenv("TEST_DATABASE_URL"), env.schema, cfg.Database, crosswalk, true); err != nil {
		t.Fatal(err)
	}
	if _, err = env.model.Store.ReserveModelCall(env.ctx, "document", scope, "controlled-chat", 2, 10, 10, limits); err == nil {
		t.Fatal("revoked membership obtained another Model reservation")
	}
	if err = env.model.Store.ReauthorizeModelScope(env.ctx, "document", scope); err == nil {
		t.Fatal("revoked membership adopted protected Model output")
	}
	result := store.ModelCallResult{Status: "completed"}
	if err = env.model.Store.FinishModelCall(env.ctx, lease, result); err != nil {
		t.Fatalf("incurred cost could not settle after revocation: %v", err)
	}
	if err = env.model.Store.FinishModelCall(env.ctx, lease, result); err != nil {
		t.Fatalf("settlement replay: %v", err)
	}
	var count int
	if err = env.db.QueryRow(env.ctx, `SELECT count(*) FROM `+pgx.Identifier{env.schema, "model_usage"}.Sanitize()+` WHERE lease_id=$1`, lease.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("settled cost count=%d err=%v", count, err)
	}
}
