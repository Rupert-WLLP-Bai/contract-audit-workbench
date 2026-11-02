//go:build browser

package e2e_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/identity"
	"contractaudit/internal/store"
	"contractaudit/internal/testsupport/oidcfixture"
	"github.com/jackc/pgx/v5"
)

// TestBrowserTenantRecoverySmoke uses the shared loopback OIDC provider, real
// PostgreSQL session/membership authority, and real UI mutations. The model is
// the existing deterministic browserModels fixture; no HTTP route is mocked.
// The pre-existing review.ts/#57 journey remains unchanged and runs separately.
func TestBrowserTenantRecoverySmoke(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required for PostgreSQL OIDC browser smoke")
	}
	delivery := strings.TrimSpace(os.Getenv("CAW_DELIVERY_DIR"))
	if !filepath.IsAbs(delivery) {
		t.Fatal("CAW_DELIVERY_DIR must be absolute")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	delivery, err = filepath.Abs(delivery)
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(root, delivery); err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		t.Fatal("CAW_DELIVERY_DIR must be outside checkout")
	}
	out, err := os.MkdirTemp(delivery, "tenant-recovery-smoke-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("external evidence: %s", out)
	st, err := store.OpenTest(t.TempDir(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	st.UseModels(&browserModels{})
	pgcfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	web := "http://127.0.0.1:" + port
	issuer := oidcfixture.New(t, oidcfixture.Config{ClientRedirectURL: web + "/api/auth/oidc/callback", AdminRedirectURL: web + "/api/auth/oidc/callback"})
	user := accounts.User{ID: "oidc-smoke-multi", DisplayName: "合成多成员身份", Role: "admin", TenantID: "stale-directory", OrgID: "stale-directory", Enabled: true, Issuer: issuer.URL(), Subject: "smoke-multi-subject"}
	directory := filepath.Join(out, "identity-directory.json")
	raw, _ := json.Marshal(struct {
		Users []accounts.User `json:"users"`
	}{[]accounts.User{user}})
	if err := os.WriteFile(directory, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := accounts.ConfigureDirectory(""); err != nil {
			t.Errorf("restore identity directory: %v", err)
		}
	})
	x := store.IdentityCrosswalk{Version: 1, Tenants: []store.IdentityTenantMap{{ID: "smoke-tenant-a", DisplayName: "合成租户 A", Status: "active"}, {ID: "smoke-tenant-b", DisplayName: "合成租户 B", Status: "active"}}, OrgUnits: []store.IdentityOrgUnitMap{{TenantID: "smoke-tenant-a", ID: "smoke-org-a", DisplayName: "合成组织 A", Status: "active"}, {TenantID: "smoke-tenant-b", ID: "smoke-org-b", DisplayName: "合成组织 B", Status: "active"}}, Principals: []store.IdentityPrincipalMap{{ID: "30000000-0000-4000-8000-000000000099", ActorID: user.ID, Issuer: user.Issuer, Subject: user.Subject, Status: "active"}}, Memberships: []store.IdentityMembershipMap{{ID: "40000000-0000-4000-8000-000000000091", ActorID: user.ID, TenantID: "smoke-tenant-a", OrgUnitID: "smoke-org-a", RoleCode: "clerk", Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}, Status: "active"}, {ID: "40000000-0000-4000-8000-000000000092", ActorID: user.ID, TenantID: "smoke-tenant-b", OrgUnitID: "smoke-org-b", RoleCode: "clerk", Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}, Status: "active"}}}
	if _, err := store.ImportIdentityCrosswalk(context.Background(), dsn, st.SchemaName(), pgcfg.Database, x, true); err != nil {
		t.Fatalf("seed OIDC membership fixture: %v", err)
	}
	manager, err := identity.NewManager(identity.Config{Issuer: issuer.URL(), ClientID: "oidc-client-id", ClientSecret: "oidc-client-secret", AdminClientID: "oidc-admin-client-id", AdminClientSecret: "oidc-admin-client-secret", RedirectURL: web + "/api/auth/oidc/callback", AdminRedirectURL: web + "/api/auth/oidc/callback", StateEncryptionKey: []byte(strings.Repeat("x", 32)), HTTPClient: issuer.HTTPClient()}, accounts.BySubject, st)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	api := &http.Server{Handler: httpapi.Handler(st, httpapi.Options{OIDC: manager, ContextSwitchEnabled: true, CORSOrigins: []string{web}, IntakeUploader: store.NewDirectCaseIntakeUploader(st)})}
	go func() { _ = api.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = api.Shutdown(ctx)
		_ = listener.Close()
	})
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal(err)
	}
	webRoot := filepath.Clean(filepath.Join("..", "web"))
	vite := exec.Command(bun, "run", "dev", "--", "--port", port, "--strictPort", "--host", "127.0.0.1")
	vite.Dir = webRoot
	vite.Env = append(os.Environ(), "VITE_DEV_API_PROXY=http://"+listener.Addr().String())
	vite.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	log, err := os.OpenFile(filepath.Join(out, "vite.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	vite.Stdout, vite.Stderr = log, log
	if err := vite.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopGroup(vite); _ = log.Close() })
	waitHTTP(t, web+"/")
	issuer.SetNextClaims("smoke-multi-subject", "")
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	env := append(os.Environ(), "CHROMIUM_BIN="+chromiumBinary(t), "E2E_WEB="+web, "CAW_DELIVERY_DIR="+out, "E2E_SCREENSHOT_DIR="+filepath.Join(out, "screenshots"))
	output, err := runBrowserScript(ctx, webRoot, env, "e2e/tenant-recovery-smoke.ts")
	_ = os.WriteFile(filepath.Join(out, "browser.log"), output, 0o600)
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
	verifyTenantRecoveryEvidence(t, dsn, st.SchemaName(), filepath.Join(out, "tenant-recovery-evidence.json"))
}

func verifyTenantRecoveryEvidence(t *testing.T, dsn, schema, evidencePath string) {
	t.Helper()
	var evidence struct {
		Status           string `json:"status"`
		TenantID         string `json:"tenant_id"`
		HomeIntakeCaseID string `json:"home_intake_case_id"`
		LegacyCaseID     string `json:"legacy_case_id"`
		CaseID           string `json:"case_id"`
		RunID            string `json:"run_id"`
		Attempts         struct {
			First struct {
				ID                string   `json:"id"`
				Ordinal           int      `json:"ordinal"`
				HistoryKind       string   `json:"history_kind"`
				FrozenResult      bool     `json:"frozen_result_available"`
				ParsedDocumentIDs []string `json:"parsed_document_ids"`
			} `json:"first"`
			Retry struct {
				ID                string   `json:"id"`
				Ordinal           int      `json:"ordinal"`
				RetryOfAttemptID  string   `json:"retry_of_attempt_id"`
				HistoryKind       string   `json:"history_kind"`
				FrozenResult      bool     `json:"frozen_result_available"`
				ParsedDocumentIDs []string `json:"parsed_document_ids"`
			} `json:"retry"`
		} `json:"attempts"`
	}
	raw, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatalf("read external browser evidence: %v", err)
	}
	if err := json.Unmarshal(raw, &evidence); err != nil {
		t.Fatalf("decode external browser evidence: %v", err)
	}
	if evidence.Status != "PASS" || evidence.TenantID != "smoke-tenant-b" || evidence.LegacyCaseID == "" || evidence.CaseID != evidence.LegacyCaseID || evidence.HomeIntakeCaseID == "" || evidence.HomeIntakeCaseID == evidence.LegacyCaseID || evidence.RunID == "" {
		t.Fatalf("browser evidence does not identify the two tenant B cases and legacy run: %+v", evidence)
	}
	if evidence.Attempts.First.ID == "" || evidence.Attempts.Retry.ID == "" || evidence.Attempts.First.ID == evidence.Attempts.Retry.ID || evidence.Attempts.First.Ordinal != 1 || evidence.Attempts.Retry.Ordinal != 2 || evidence.Attempts.Retry.RetryOfAttemptID != evidence.Attempts.First.ID || evidence.Attempts.First.HistoryKind != "frozen" || evidence.Attempts.Retry.HistoryKind != "frozen" || !evidence.Attempts.First.FrozenResult || !evidence.Attempts.Retry.FrozenResult || len(evidence.Attempts.First.ParsedDocumentIDs) == 0 || !equalSorted(evidence.Attempts.First.ParsedDocumentIDs, evidence.Attempts.Retry.ParsedDocumentIDs) {
		t.Fatalf("browser did not verify the retry preserved frozen parse references: %+v", evidence.Attempts)
	}

	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to test PostgreSQL for frozen hash verification: %v", err)
	}
	defer conn.Close(context.Background())
	qualified := pgx.Identifier{schema}.Sanitize()
	// The verifier uses the test DSN, which may or may not bypass RLS. Set the
	// tenant explicitly and still constrain every join and the expected org.
	if _, err := conn.Exec(context.Background(), `SELECT set_config('app.tenant_id',$1,false), set_config('app.org_id',$2,false)`, evidence.TenantID, "smoke-org-b"); err != nil {
		t.Fatalf("set PostgreSQL verification scope: %v", err)
	}
	var caseCount, staleCount int
	if err := conn.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE id=ANY($1::text[])), count(*) FILTER (WHERE title=$2)
		FROM `+qualified+`.cases WHERE tenant_id=$3 AND org_id=$4`, []string{evidence.HomeIntakeCaseID, evidence.LegacyCaseID}, "陈旧表单不得写入", evidence.TenantID, "smoke-org-b").Scan(&caseCount, &staleCount); err != nil {
		t.Fatalf("query scoped case evidence: %v", err)
	}
	if caseCount != 2 || staleCount != 0 {
		t.Fatalf("tenant B case evidence: cases=%d stale commands=%d", caseCount, staleCount)
	}
	query := `SELECT a.id, a.ordinal, COALESCE(a.retry_of_attempt_id,''), a.history_kind, a.state, COALESCE(a.input_hash,''), COALESCE(a.output_hash,''), i.input_hash, i.documents
		FROM ` + qualified + `.cases c
		JOIN ` + qualified + `.review_runs r ON r.tenant_id=c.tenant_id AND r.org_id=c.org_id AND r.case_id=c.id
		JOIN ` + qualified + `.review_attempts a ON a.tenant_id=r.tenant_id AND a.org_id=r.org_id AND a.run_id=r.id
		JOIN ` + qualified + `.review_attempt_inputs i ON i.tenant_id=a.tenant_id AND i.org_id=a.org_id AND i.run_id=a.run_id AND i.attempt_id=a.id
		WHERE r.id=$1 AND c.id=$2 AND c.tenant_id=$3 AND c.org_id=$4 AND a.id=ANY($5::text[])
		ORDER BY a.ordinal`
	rows, err := conn.Query(context.Background(), query, evidence.RunID, evidence.LegacyCaseID, evidence.TenantID, "smoke-org-b", []string{evidence.Attempts.First.ID, evidence.Attempts.Retry.ID})
	if err != nil {
		t.Fatalf("query frozen attempt hashes from scoped PostgreSQL schema: %v", err)
	}
	defer rows.Close()
	type frozenRow struct {
		id, retryOf, history, state, attemptHash, outputHash, inputHash string
		ordinal                                                         int
		inputJSON                                                       []byte
	}
	found := make([]frozenRow, 0, 2)
	for rows.Next() {
		var item frozenRow
		if err := rows.Scan(&item.id, &item.ordinal, &item.retryOf, &item.history, &item.state, &item.attemptHash, &item.outputHash, &item.inputHash, &item.inputJSON); err != nil {
			t.Fatalf("scan frozen attempt from PostgreSQL: %v", err)
		}
		found = append(found, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read frozen attempts from PostgreSQL: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("PostgreSQL returned %d scoped frozen attempts, want 2", len(found))
	}
	wantIDs := []string{evidence.Attempts.First.ID, evidence.Attempts.Retry.ID}
	wantParseIDs := append([]string(nil), evidence.Attempts.First.ParsedDocumentIDs...)
	var firstDocuments []store.FrozenDocument
	for index, item := range found {
		wantRetryOf := ""
		if index == 1 {
			wantRetryOf = wantIDs[0]
		}
		if item.id != wantIDs[index] || item.ordinal != index+1 || item.retryOf != wantRetryOf || item.history != "frozen" || !terminalAttemptState(item.state) || item.attemptHash == "" || item.outputHash == "" || item.inputHash == "" || item.attemptHash != item.inputHash {
			t.Fatalf("PostgreSQL frozen attempt hash/state mismatch: %+v", item)
		}
		if index > 0 && item.inputHash != found[0].inputHash {
			t.Fatalf("retry changed the immutable input hash: first=%s retry=%s", found[0].inputHash, item.inputHash)
		}
		var docs []store.FrozenDocument
		if err := json.Unmarshal(item.inputJSON, &docs); err != nil {
			t.Fatalf("decode frozen input documents for %s: %v", item.id, err)
		}
		parseIDs := make([]string, 0, len(docs))
		for _, doc := range docs {
			if doc.ParsedDocumentID != "" {
				parseIDs = append(parseIDs, doc.ParsedDocumentID)
			}
		}
		if !equalSorted(parseIDs, wantParseIDs) {
			t.Fatalf("PostgreSQL frozen document parses for %s differ from browser evidence: got=%v want=%v", item.id, parseIDs, wantParseIDs)
		}
		if index == 0 {
			firstDocuments = docs
		} else if !reflect.DeepEqual(firstDocuments, docs) {
			t.Fatal("retry changed the frozen input documents")
		}
	}
	t.Logf("PostgreSQL confirms two frozen attempts retain one input hash and parsed identities for tenant=%s case=%s run=%s", evidence.TenantID, evidence.LegacyCaseID, evidence.RunID)
}

func equalSorted(left, right []string) bool {
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	if len(leftCopy) != len(rightCopy) {
		return false
	}
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] {
			return false
		}
	}
	return true
}

func terminalAttemptState(state string) bool {
	switch state {
	case "awaiting_human", "completed", "partial", "failed", "cancelled":
		return true
	default:
		return false
	}
}
