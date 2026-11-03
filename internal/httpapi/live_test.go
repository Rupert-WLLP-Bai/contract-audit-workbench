//go:build live

// File metadata (manifest mirror):
// Purpose: Exercises the `httpapi_test` package behavior through TestLiveReviewAndOCR; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-25
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi_test

import (
	"bytes"
	"context"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/modelgateway"
	"contractaudit/internal/store"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/model"
)

func TestLiveReviewAndOCR(t *testing.T) {
	// Only the explicit live build tag enables this test. Respect the operator's
	// endpoint and model configuration; never probe an unrelated hard-coded port.
	if strings.TrimSpace(os.Getenv("LLM_MODEL")) == "" || strings.TrimSpace(os.Getenv("OCR_MODEL")) == "" {
		t.Fatal("live acceptance requires explicit LLM_MODEL and OCR_MODEL")
	}
	t.Setenv("APP_ENV", "test")
	t.Setenv("AGENT_PLANNER_MODE", "live")
	t.Setenv("AGENT_PLANNER_TIMEOUT", "90s")
	cfg := model.FromEnv()
	seen := &statusCapture{base: http.DefaultTransport}
	api, schema := newLiveAPI(t, cfg, seen)
	api.login(t, "demo-admin", "admin-demo")
	status, session := api.do(t, http.MethodGet, "/api/session", nil)
	if status != http.StatusOK {
		t.Fatalf("read original live session: %d %v", status, session)
	}
	active, ok := session["active_context"].(map[string]any)
	if !ok {
		t.Fatal("live session omitted its original active context")
	}
	version, _ := active["context_version"].(string)
	csrf, _ := session["csrf_token"].(string)
	if version == "" || csrf == "" {
		t.Fatal("live session omitted command proof headers")
	}
	api.client.Transport = liveCommandHeaders{version: version, csrf: csrf}

	status, settings := api.do(t, http.MethodGet, "/api/settings", nil)
	if status != http.StatusOK {
		t.Fatalf("settings %d %v", status, settings)
	}
	modelView := settings["model"].(map[string]any)
	t.Logf("settings status=%v label=%v", modelView["status"], modelView["label"])
	if modelView["status"] != "connected" {
		t.Fatalf("settings %+v", modelView)
	}
	if key := os.Getenv("LLM_API_KEY"); key != "" {
		raw, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), key) {
			t.Fatal("settings leaked LLM_API_KEY")
		}
	}

	status, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "实机质保", "contract_type": "procurement_ict",
	})
	if status != http.StatusCreated {
		t.Fatalf("create live text case: %d %v", status, created)
	}
	caseID := created["id"].(string)
	api.upload(t, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件要求质保36个月。"))
	api.upload(t, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。"))
	status, review := liveStartReview(t, api, "/api/cases/"+caseID+"/reviews")
	if status != http.StatusAccepted {
		t.Fatalf("review %d %v", status, review)
	}
	ws := liveWaitReview(t, api, "/api/cases/"+caseID+"/reviews/"+review["id"].(string))
	seen.rememberFrozenTexts(ws)
	diagnostics := api.reviewDiagnostics(t, "/api/cases/"+caseID+"/reviews/"+review["id"].(string))
	snapshot, _ := diagnostics["snapshot"].(map[string]any)
	execution, _ := diagnostics["execution"].(map[string]any)
	attempt, _ := execution["attempt"].(map[string]any)
	t.Logf("live diagnostics model=%v attempt_error=%v partial=%v calls=%v", snapshot["model_ref"], attempt["error_code"], ws["run"].(map[string]any)["partial_reason"], seen.snapshot())
	run := ws["run"].(map[string]any)
	t.Logf("chat run state=%v model_ref=%v failure=%v", run["state"], run["model_ref"], run["failure_message"])
	if run["state"] == "failed" || run["state"] == "partial" {
		t.Fatalf("live review failed: %v", run["failure_message"])
	}
	if snapshot["model_ref"] != cfg.ChatModel {
		t.Fatalf("model_ref %+v", snapshot["model_ref"])
	}
	findings := ws["findings"].([]any)
	if len(findings) != 1 {
		t.Fatalf("findings %+v", findings)
	}
	finding := findings[0].(map[string]any)
	t.Logf("chat claim=%s relation=%v disposition=%v", truncate(finding["claim"]), finding["relation"], finding["disposition_suggestion"])
	if finding["origin"] != "rule" || finding["relation"] != "conflict" {
		t.Fatalf("finding %+v", finding)
	}
	located := 0
	for _, item := range finding["evidence"].([]any) {
		anchor := item.(map[string]any)
		t.Logf("quote status=%v quote=%s", anchor["status"], truncate(anchor["quote"]))
		if anchor["status"] == "located" && anchor["bbox"] == nil {
			located++
		}
	}
	if located == 0 {
		t.Fatal("rule evidence was not located in the texts")
	}

	status, created = api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "实机图片", "contract_type": "procurement_ict",
	})
	if status != http.StatusCreated {
		t.Fatalf("create live OCR case: %d %v", status, created)
	}
	imageCase := created["id"].(string)
	imageID := api.upload(t, imageCase, "procurement", "scan.png", "image/png", model.SampleWarrantyPNG)
	api.upload(t, imageCase, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保12个月。"))
	status, review = liveStartReview(t, api, "/api/cases/"+imageCase+"/reviews")
	if status != http.StatusAccepted {
		t.Fatalf("ocr review %d %v", status, review)
	}
	ws = liveWaitReview(t, api, "/api/cases/"+imageCase+"/reviews/"+review["id"].(string))
	seen.rememberFrozenTexts(ws)
	run = ws["run"].(map[string]any)
	var ocrText string
	for _, item := range ws["documents"].([]any) {
		doc := item.(map[string]any)
		if doc["id"] != imageID {
			continue
		}
		if doc["parse_status"] != "ready" {
			t.Fatalf("ocr document %+v", doc)
		}
		ocrText, _ = doc["text"].(string)
	}
	t.Logf("ocr text=%s", truncate(ocrText))
	if !strings.Contains(ocrText, "质保") || !strings.Contains(ocrText, "36") {
		t.Fatalf("ocr text %q", ocrText)
	}
	t.Logf("ocr run state=%v failure=%v", run["state"], run["failure_message"])
	if run["state"] == "failed" || run["state"] == "partial" {
		t.Fatalf("live ocr review failed: %v", run["failure_message"])
	}
	if ocrFindings := ws["findings"].([]any); len(ocrFindings) > 0 {
		item := ocrFindings[0].(map[string]any)
		t.Logf("ocr claim=%s relation=%v", truncate(item["claim"]), item["relation"])
	}

	for _, line := range seen.snapshot() {
		t.Logf("gateway %s", line)
	}
	var chatOK, listOK bool
	endpoint, err := url.Parse("http://gateway.invalid/v1")
	if err != nil {
		t.Fatal("invalid configured model endpoint")
	}
	basePath := strings.TrimRight(endpoint.Path, "/")
	for _, line := range seen.snapshot() {
		if strings.HasPrefix(line, "GET "+basePath+"/models 200") {
			listOK = true
		}
		if strings.HasPrefix(line, "POST "+basePath+"/chat/completions 200") {
			chatOK = true
		}
	}
	if !listOK || !chatOK {
		t.Fatalf("gateway calls %v", seen.snapshot())
	}
	liveAccounting(t, schema, caseID, imageCase, cfg.ChatModel, cfg.OCRModel)
	seen.requireFrozenModelQuotes(t, cfg.ChatModel)
}

func truncate(value any) string {
	text, _ := value.(string)
	rs := []rune(text)
	if len(rs) > 180 {
		return string(rs[:180]) + "…"
	}
	return text
}

type statusCapture struct {
	base          http.RoundTripper
	mu            sync.Mutex
	lines         []string
	quotes        [][]model.Quote
	chatResponses int
	frozenTexts   []map[string]string
}

// Preserve the accepted session proof rather than fabricating identity or
// automatically refreshing a rejected command onto a different context.
type liveCommandHeaders struct{ version, csrf string }

func (h liveCommandHeaders) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	if copy.Method != http.MethodGet && copy.Method != http.MethodHead && copy.Method != http.MethodOptions {
		copy.Header.Set("X-Context-Version", h.version)
		copy.Header.Set("X-CSRF-Token", h.csrf)
	}
	return http.DefaultTransport.RoundTrip(copy)
}

func (c *statusCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	base := c.base
	if base == nil {
		base = http.DefaultTransport
	}
	res, err := base.RoundTrip(req)
	if err == nil && res != nil && res.StatusCode == http.StatusOK && req.Method == http.MethodPost && req.GetBody != nil {
		body, bodyErr := req.GetBody()
		if bodyErr == nil {
			var request struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(body).Decode(&request)
			body.Close()
			if request.Model == model.GatewayID(os.Getenv("LLM_MODEL")) {
				raw, readErr := io.ReadAll(res.Body)
				res.Body.Close()
				res.Body = io.NopCloser(bytes.NewReader(raw))
				if readErr != nil {
					return res, readErr
				}
				var response struct {
					Choices []struct {
						Message struct {
							Content string `json:"content"`
						} `json:"message"`
					} `json:"choices"`
				}
				if json.Unmarshal(raw, &response) == nil && len(response.Choices) > 0 {
					answer, parseErr := model.ParseWarranty(response.Choices[0].Message.Content)
					if parseErr == nil {
						c.mu.Lock()
						c.chatResponses++
						c.quotes = append(c.quotes, answer.Quotes)
						c.mu.Unlock()
					}
				}
			}
		}
	}
	code := 0
	if res != nil {
		code = res.StatusCode
	}
	c.mu.Lock()
	c.lines = append(c.lines, req.Method+" "+req.URL.Path+" "+strconv.Itoa(code))
	c.mu.Unlock()
	return res, err
}

func (c *statusCapture) rememberFrozenTexts(workspace map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	texts := make(map[string]string)
	for _, raw := range workspace["documents"].([]any) {
		doc := raw.(map[string]any)
		id, _ := doc["id"].(string)
		text, _ := doc["text"].(string)
		texts[id] = text
	}
	c.frozenTexts = append(c.frozenTexts, texts)
}

func (c *statusCapture) requireFrozenModelQuotes(t *testing.T, requested string) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.chatResponses != 2 || len(c.frozenTexts) != 2 {
		t.Fatal("live model responses omitted verifiable quotes")
	}
	for i, quotes := range c.quotes {
		if len(quotes) == 0 {
			t.Fatal("live model response contained no quotes")
		}
		for _, q := range quotes {
			if q.Quote == "" || !strings.Contains(c.frozenTexts[i][q.DocumentID], q.Quote) {
				t.Fatalf("model quote does not match its frozen document %s", q.DocumentID)
			}
		}
	}
	t.Logf("verified model quotes for both frozen attempts with %s", requested)
}

func (c *statusCapture) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.lines))
	copy(out, c.lines)
	return out
}

func newLiveAPI(t *testing.T, cfg model.Config, seen *statusCapture) (*testAPI, string) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL required for isolated live accounting")
	}
	st, err := store.OpenTest(t.TempDir(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := st.SchemaName()
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	docs, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := docs.Close(); err != nil {
			t.Error(err)
		}
	})
	gatewayCfg, err := modelgateway.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	gatewayCfg.Mode = "openai-compatible"
	gatewayCfg.UpstreamBaseURL, gatewayCfg.UpstreamAPIKey = cfg.BaseURL, cfg.APIKey
	gatewayCfg.ChatModel, gatewayCfg.OCRModel = cfg.ChatModel, cfg.OCRModel
	gatewayCfg.WorkerCredential = strings.Repeat("functional-worker-", 3)
	gatewayCfg.DocumentCredential = strings.Repeat("functional-document-", 3)
	gatewayCfg.Budget.MaxInputTokenReservation = 1 << 20
	gatewayCfg.Budget.GlobalTokensPerMinute = 32 << 20
	gatewayCfg.Budget.TenantTokensPerMinute = 16 << 20
	gatewayCfg.Retries = 0
	gateway, err := modelgateway.New(st, gatewayCfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gateway.Handler())
	t.Cleanup(server.Close)
	workerCfg := cfg
	workerCfg.BaseURL, workerCfg.ProviderURL = server.URL+"/v1", cfg.BaseURL
	workerCfg.Gateway, workerCfg.APIKey = true, gatewayCfg.WorkerCredential
	worker := model.New(workerCfg).WithTransport(seen)
	docCfg := workerCfg
	docCfg.APIKey = gatewayCfg.DocumentCredential
	docModels := model.New(docCfg).WithTransport(seen)
	st.UseModels(worker)
	docs.UseModels(docModels)
	st.UseDocumentPreparer(docs)
	coreHandler := httpapi.Handler(st, httpapi.Options{Models: worker, Durable: true})
	docHandler := httpapi.Handler(docs, httpapi.Options{Models: docModels, Durable: true})
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/documents") || strings.HasSuffix(r.URL.Path, "/generations") {
			docHandler.ServeHTTP(w, r)
			return
		}
		coreHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(apiServer.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			claim, err := st.ClaimNextJob(ctx, "http-live-worker", 3*time.Minute)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				if ctx.Err() == nil {
					t.Logf("claim live job: %v", err)
				}
				continue
			}
			if err := st.RunClaimedJob(ctx, claim); err != nil && ctx.Err() == nil {
				t.Logf("live job %s: %v", claim.JobID, err)
			}
		}
	}()
	t.Cleanup(func() { stop(); <-done })
	return &testAPI{base: apiServer.URL, client: &http.Client{Jar: jar}}, schema
}

func liveStartReview(t *testing.T, api *testAPI, path string) (int, map[string]any) {
	t.Helper()
	status, raw := api.bytes(t, http.MethodPost, path, bytes.NewBufferString("{}"), "application/json")
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("start live review %d: %s", status, raw)
	}
	return status, result
}

func liveWaitReview(t *testing.T, api *testAPI, path string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	var last map[string]any
	for time.Now().Before(deadline) {
		ws, err := api.workspaceWithin(t, path, deadline)
		if err != nil {
			t.Fatalf("read live workspace: %v", err)
		}
		last = ws
		state := reviewState(ws)
		if state == "failed" || state == "partial" {
			t.Fatalf("live review %s: %v", state, ws["run"])
		}
		diagnostics := api.reviewDiagnostics(t, path)
		execution, _ := diagnostics["execution"].(map[string]any)
		attempt, _ := execution["attempt"].(map[string]any)
		switch attempt["state"] {
		case "completed":
			return ws
		case "failed", "partial", "cancelled":
			t.Fatalf("live attempt %v: %v", attempt["state"], attempt)
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("live review timed out after 5 minutes: %s; last=%v", path, last["run"])
	return nil
}

func liveAccounting(t *testing.T, schema, textCase, imageCase, chatModel, ocrModel string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	qualified := pgx.Identifier{schema}.Sanitize()
	rows, err := pool.Query(ctx, `SELECT caller_identity,model_ref,call_status,error_code,COALESCE(usage_source,''),charged_tokens,budget_released FROM `+qualified+`.model_usage WHERE case_id IN ($1,$2) ORDER BY occurred_at`, textCase, imageCase)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var caller, modelRef, status, errorCode, source string
		var charged *int64
		var released bool
		if err := rows.Scan(&caller, &modelRef, &status, &errorCode, &source, &charged, &released); err != nil {
			t.Fatal(err)
		}
		var chargedValue any
		if charged != nil {
			chargedValue = *charged
		}
		t.Logf("live ledger caller=%s model=%s status=%s error=%s usage=%s charged=%v released=%t", caller, modelRef, status, errorCode, source, chargedValue, released)
	}
	rows.Close()
	for _, check := range []struct{ caseID, caller, model string }{
		{textCase, "worker", model.GatewayID(chatModel)},
		// Frozen attempt preparation is executed under the worker claim.
		{imageCase, "worker", model.GatewayID(ocrModel)},
		{imageCase, "worker", model.GatewayID(chatModel)},
	} {
		var count int
		err := pool.QueryRow(ctx, `SELECT count(*) FROM `+qualified+`.model_usage WHERE case_id=$1 AND caller_identity=$2 AND model_ref=$3 AND call_status='completed' AND error_code='' AND charged_tokens IS NOT NULL`, check.caseID, check.caller, check.model).Scan(&count)
		if err != nil || count == 0 {
			t.Fatalf("settled live %s model %s for case %s: count=%d err=%v", check.caller, check.model, check.caseID, count, err)
		}
	}
	var leases int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM `+qualified+`.model_call_leases WHERE case_id IN ($1,$2)`, textCase, imageCase).Scan(&leases)
	if err != nil || leases != 0 {
		t.Fatalf("live reservations not settled: leases=%d err=%v", leases, err)
	}
	t.Logf("live gateway accounting settled for text and OCR cases")
}
