//go:build browser && live

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/model"
	"contractaudit/internal/modelgateway"
	"contractaudit/internal/store"
	"github.com/jackc/pgx/v5"
)

// TestBrowserFunctionalLive uses a new PostgreSQL test schema, real durable
// jobs, the production Model Gateway and the explicitly configured live model.
// No test provider or browser route interception participates in this journey.
func TestBrowserFunctionalLive(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("AGENT_PLANNER_MODE", "live")
	t.Setenv("AGENT_PLANNER_TIMEOUT", "90s")
	cfg := model.FromEnv()
	if cfg.ChatModel != "codex/gpt-6-luna" || cfg.OCRModel != cfg.ChatModel {
		t.Fatal("functional live acceptance requires codex/gpt-6-luna for chat and OCR")
	}
	st, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	documents, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = documents.Close() })
	readOnly, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = readOnly.Close() })
	readOnly.UseReadOnly(true)

	gatewayConfig, err := modelgateway.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	gatewayConfig.Mode = "openai-compatible"
	gatewayConfig.UpstreamBaseURL, gatewayConfig.UpstreamAPIKey = cfg.BaseURL, cfg.APIKey
	gatewayConfig.ChatModel, gatewayConfig.OCRModel = cfg.ChatModel, cfg.OCRModel
	gatewayConfig.WorkerCredential = strings.Repeat("functional-worker-", 3)
	gatewayConfig.DocumentCredential = strings.Repeat("functional-document-", 3)
	// Image requests retain the gateway's conservative encoded-byte accounting.
	gatewayConfig.Budget.MaxInputTokenReservation = 1 << 20
	gatewayConfig.Budget.GlobalTokensPerMinute = 32 << 20
	gatewayConfig.Budget.TenantTokensPerMinute = 16 << 20
	gatewayConfig.Retries = 0
	gateway, err := modelgateway.New(st, gatewayConfig)
	if err != nil {
		t.Fatal(err)
	}
	gatewayHTTP := httptest.NewServer(gateway.Handler())
	t.Cleanup(gatewayHTTP.Close)
	workerConfig := cfg
	workerConfig.BaseURL, workerConfig.ProviderURL = gatewayHTTP.URL+"/v1", cfg.BaseURL
	workerConfig.Gateway, workerConfig.APIKey = true, gatewayConfig.WorkerCredential
	workerModels := model.New(workerConfig)
	documentConfig := workerConfig
	documentConfig.APIKey = gatewayConfig.DocumentCredential
	documentModels := model.New(documentConfig)
	st.UseModels(workerModels)
	documents.UseModels(documentModels)
	readOnly.UseModels(workerModels)
	st.UseDocumentPreparer(documents)

	clientPort, adminPort, portalPort := freePort(t), freePort(t), freePort(t)
	clientURL, adminURL, portalURL := "http://127.0.0.1:"+clientPort, "http://127.0.0.1:"+adminPort, "http://127.0.0.1:"+portalPort
	origins := []string{clientURL, adminURL, portalURL}
	coreHandler := httpapi.Handler(st, httpapi.Options{CORSOrigins: origins, Models: workerModels, Durable: true})
	documentHandler := httpapi.Handler(documents, httpapi.Options{CORSOrigins: origins, Models: documentModels, Durable: true})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/documents") || strings.HasSuffix(r.URL.Path, "/generations") {
			documentHandler.ServeHTTP(w, r)
			return
		}
		coreHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(api.Close)
	readOnlyHTTP := httptest.NewServer(httpapi.Handler(readOnly, httpapi.Options{Models: workerModels, Durable: true}))
	t.Cleanup(readOnlyHTTP.Close)
	missing, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = missing.Close() })
	missingHTTP := httptest.NewServer(httpapi.Handler(missing, httpapi.Options{}))
	t.Cleanup(missingHTTP.Close)

	ctx, cancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			claim, err := st.ClaimNextJob(ctx, "functional-live-worker", 3*time.Minute)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				if ctx.Err() == nil {
					t.Logf("claim: %v", err)
				}
				continue
			}
			if err := st.RunClaimedJob(ctx, claim); err != nil && ctx.Err() == nil {
				t.Logf("job %s: %v", claim.JobID, err)
			}
		}
	}()
	t.Cleanup(func() { cancel(); <-workerDone })

	webRoot := filepath.Clean(filepath.Join("..", "web"))
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal(err)
	}
	for _, site := range []struct{ mode, port string }{{"client", clientPort}, {"admin", adminPort}, {"portal", portalPort}} {
		vite := exec.Command(bun, "run", "dev", "--", "--mode", site.mode, "--port", site.port, "--strictPort", "--host", "127.0.0.1")
		vite.Dir = webRoot
		vite.Env = append(os.Environ(), "VITE_DEV_API_PROXY="+api.URL)
		vite.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := vite.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { stopGroup(vite) })
		entry := "/"
		if site.mode != "client" {
			entry = "/" + site.mode + "/" + site.mode + ".html"
		}
		waitHTTP(t, "http://127.0.0.1:"+site.port+entry)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("standard Playwright runner requires Node >=22.12")
	}
	started := time.Now()
	command := exec.Command(node, "node_modules/playwright/cli.js", "test", "--config", "playwright.functional.config.ts")
	if filter := strings.TrimSpace(os.Getenv("E2E_FUNCTIONAL_GREP")); filter != "" {
		command.Args = append(command.Args, "--grep", filter)
	}
	command.Dir = webRoot
	command.Env = append(os.Environ(), "E2E_WEB="+clientURL, "E2E_API="+api.URL,
		"E2E_ADMIN="+adminURL+"/admin/admin.html", "E2E_PORTAL="+portalURL+"/portal/portal.html",
		"E2E_READONLY_API="+readOnlyHTTP.URL, "E2E_MISSING_API="+missingHTTP.URL,
		"E2E_MODEL="+cfg.ChatModel, "E2E_PROVIDER="+cfg.BaseURL)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join("..", "artifacts", "documents-functional", "feature-outcomes.json")
	info, err := os.Stat(reportPath)
	if err != nil || info.ModTime().Before(started) {
		t.Fatal("Playwright did not produce a current feature result report")
	}
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Features []json.RawMessage `json:"features"`
		Counts   map[string]int    `json:"counts"`
	}
	if err := json.Unmarshal(reportBytes, &report); err != nil || len(report.Features) == 0 || report.Counts["PASS"] != len(report.Features) {
		t.Fatal("Playwright feature outcomes are empty, failed, or unexecuted")
	}
	// FromOCR is internal parsed provenance, intentionally absent from the
	// public workspace DTO. Read the same frozen artifacts through Store.
	expectedImages := 0
	for _, feature := range report.Features {
		if strings.Contains(string(feature), "Standalone PNG:") || strings.Contains(string(feature), "Standalone JPEG:") {
			expectedImages++
		}
	}
	if expectedImages > 0 {
		actor, ok := accounts.ByID("demo-admin")
		if !ok {
			t.Fatal("image provenance actor is unavailable")
		}
		cases, err := st.ListCasesChecked(actor)
		if err != nil {
			t.Fatal(err)
		}
		verifiedImages := 0
		for _, item := range cases {
			if !strings.HasPrefix(item.Case.Title, "独立PNG合成图片") && !strings.HasPrefix(item.Case.Title, "独立JPEG合成图片") {
				continue
			}
			if item.Run == nil {
				t.Fatal("standalone image has no frozen review")
			}
			workspace, err := st.Workspace(actor, item.Case.ID, item.Run.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, document := range workspace.Documents {
				if !strings.HasPrefix(document.Document.Filename, "standalone-contract.") {
					continue
				}
				if !document.FromOCR || document.ParsedDocument == nil || !document.ParsedDocument.FromOCR {
					t.Fatal("standalone image did not preserve real immutable FromOCR")
				}
				verifiedImages++
				t.Logf("verified persisted image FromOCR=true: %s", document.Document.Filename)
			}
		}
		if verifiedImages != expectedImages {
			t.Fatalf("verified %d image artifacts, want %d selected image cases", verifiedImages, expectedImages)
		}
	}
}
