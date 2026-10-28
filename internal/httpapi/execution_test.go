package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/model"
)

type heldReviewModel struct {
	started chan struct{}
	release chan struct{}
	start   sync.Once
	finish  sync.Once
}

func (m *heldReviewModel) Name() string            { return "synthetic-held-model" }
func (m *heldReviewModel) ConfigurationID() string { return m.Name() }
func (m *heldReviewModel) Warranty(context.Context, []model.Material) (model.Warranty, error) {
	m.start.Do(func() { close(m.started) })
	<-m.release // Deliberately simulate an upstream that ignores cancellation.
	return model.Warranty{}, model.ErrUnusable
}
func (m *heldReviewModel) Transcribe(context.Context, string, []byte) (string, error) {
	return "", model.ErrUnusable
}
func (m *heldReviewModel) finishCall() { m.finish.Do(func() { close(m.release) }) }

func TestAcceptedExecutionCancellationRetryAndHistoricalAuthorizationHTTP(t *testing.T) {
	models := &heldReviewModel{started: make(chan struct{}), release: make(chan struct{})}
	defer models.finishCall()
	api := newAPIWith(t, models, nil)
	api.login(t, "demo-admin", "admin-demo")
	_, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{"title": "真实受理与旧尝试", "contract_type": "procurement_ict"})
	caseID := created["id"].(string)
	base := "/api/cases/" + caseID
	api.upload(t, caseID, "procurement", "a.txt", "text/plain", []byte("采购文件质保36个月。"))
	api.upload(t, caseID, "draft_contract", "b.txt", "text/plain", []byte("拟签合同质保24个月。"))
	type receipt struct {
		status int
		body   []byte
	}
	accepted := make(chan receipt, 1)
	go func() {
		status, body := api.bytes(t, http.MethodPost, base+"/reviews", strings.NewReader("{}"), "application/json")
		accepted <- receipt{status, body}
	}()
	var response receipt
	select {
	case response = <-accepted:
	case <-time.After(2 * time.Second):
		models.finishCall()
		t.Fatal("HTTP admission waited for the blocked model")
	}
	if response.status != http.StatusCreated {
		t.Fatalf("admission %d %s", response.status, response.body)
	}
	var ids map[string]any
	if err := json.Unmarshal(response.body, &ids); err != nil {
		t.Fatal(err)
	}
	runID, attemptID := ids["id"].(string), ids["attempt_id"].(string)
	path := base + "/reviews/" + runID
	select {
	case <-models.started:
	case <-time.After(2 * time.Second):
		t.Fatal("accepted execution did not start")
	}
	status, live := api.do(t, http.MethodGet, path, nil)
	if status != http.StatusOK || live["run"].(map[string]any)["state"] != "running" {
		t.Fatalf("live workspace %d %v", status, live)
	}
	findings := live["findings"].([]any)
	if len(findings) != 1 || findings[0].(map[string]any)["relation"] != "conflict" {
		t.Fatalf("verified rules unavailable during model call: %v", findings)
	}
	status, diagnostics := api.do(t, http.MethodGet, path+"/diagnostics?attempt_id="+attemptID, nil)
	if status != http.StatusOK || diagnostics["case_id"] != caseID || diagnostics["run_id"] != runID {
		t.Fatalf("live diagnostics %d %v", status, diagnostics)
	}
	execution := diagnostics["execution"].(map[string]any)
	if execution["attempt"].(map[string]any)["id"] != attemptID || execution["queue_wait_ms"] != nil {
		t.Fatalf("execution identity/timing %v", execution)
	}
	status, _ = api.do(t, http.MethodPost, path+"/cancel", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("cancel %d", status)
	}
	models.finishCall()
	cancelled := api.waitReview(t, path)
	if cancelled["run"].(map[string]any)["state"] != "cancelled" {
		t.Fatalf("late model overwrote cancellation: %v", cancelled["run"])
	}
	status, cancelledDiagnostics := api.do(t, http.MethodGet, path+"/diagnostics?attempt_id="+attemptID, nil)
	if status != http.StatusOK {
		t.Fatalf("cancelled diagnostics %d %v", status, cancelledDiagnostics)
	}
	cancelledAttempt := cancelledDiagnostics["execution"].(map[string]any)["attempt"].(map[string]any)
	if cancelledAttempt["id"] != attemptID || cancelledAttempt["state"] != "cancelled" {
		t.Fatalf("cancelled attempt identity %v", cancelledAttempt)
	}
	status, retried := api.do(t, http.MethodPost, path+"/retry", map[string]any{})
	if status != http.StatusOK || retried["attempt_id"] == attemptID {
		t.Fatalf("retry did not append identity %d %v", status, retried)
	}
	status, historical := api.do(t, http.MethodGet, path+"/diagnostics?attempt_id="+attemptID, nil)
	if status != http.StatusOK || historical["case_id"] != caseID || historical["run_id"] != runID {
		t.Fatalf("historical diagnostics %d %v", status, historical)
	}
	old := historical["execution"].(map[string]any)["attempt"].(map[string]any)
	if old["id"] != attemptID || old["state"] != "cancelled" {
		t.Fatalf("historical attempt overwritten: %v", old)
	}
	if !reflect.DeepEqual(old, cancelledAttempt) {
		t.Fatalf("retry rewrote the cancelled attempt: before %v after %v", cancelledAttempt, old)
	}
	status, _ = api.do(t, http.MethodGet, path+"?attempt_id=not-from-this-run", nil)
	if status != http.StatusNotFound {
		t.Fatalf("mixed attempt accepted: %d", status)
	}
	status, _ = api.do(t, http.MethodGet, path+"/diagnostics?attempt_id=not-from-this-run", nil)
	if status != http.StatusNotFound {
		t.Fatalf("mixed diagnostic attempt accepted: %d", status)
	}
	api.login(t, "demo-clerk", "clerk-demo")
	status, _ = api.do(t, http.MethodGet, path+"?attempt_id="+attemptID, nil)
	if status != http.StatusNotFound {
		t.Fatalf("nonmember history visible: %d", status)
	}
	status, _ = api.do(t, http.MethodGet, path+"/diagnostics?attempt_id="+attemptID, nil)
	if status != http.StatusForbidden {
		t.Fatalf("nonadmin diagnostic history visible: %d", status)
	}
	_, settings := api.do(t, http.MethodGet, "/api/settings", nil)
	if _, visible := settings["execution"]; visible {
		t.Fatal("nonadmin operational metadata exposed")
	}
	api.login(t, "demo-admin", "admin-demo")
	_, settings = api.do(t, http.MethodGet, "/api/settings", nil)
	if settings["execution"].(map[string]any)["total_attempts"].(float64) < 2 || settings["queue"].(map[string]any)["depth"] != nil {
		t.Fatalf("operational metadata %v", settings)
	}
}

func TestIncompleteProviderUsageSurvivesHTTPRetry(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"observed-synthetic-model","usage":{"prompt_tokens":77,"completion_tokens":9,"total_tokens":86},"choices":[{"finish_reason":"length","message":{"content":"incomplete"}}]}`))
	}))
	defer upstream.Close()
	api := newAPIWith(t, model.New(model.Config{BaseURL: upstream.URL + "/v1", ChatModel: "synthetic-usage-model"}), nil)
	api.login(t, "demo-admin", "admin-demo")
	_, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{"title": "失败调用计费来源", "contract_type": "procurement_ict"})
	caseID := created["id"].(string)
	api.upload(t, caseID, "procurement", "a.txt", "text/plain", []byte("采购文件质保36个月。"))
	api.upload(t, caseID, "draft_contract", "b.txt", "text/plain", []byte("拟签合同质保24个月。"))
	_, accepted := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	path := "/api/cases/" + caseID + "/reviews/" + accepted["id"].(string)
	_, _ = api.do(t, http.MethodPost, path+"/retry", map[string]any{})
	_, workspace := api.do(t, http.MethodGet, path, nil)
	if workspace["run"].(map[string]any)["state"] != "partial" {
		t.Fatalf("incomplete model was not partial: %v", workspace["run"])
	}
	status, diagnostics := api.do(t, http.MethodGet, path+"/diagnostics", nil)
	if status != http.StatusOK {
		t.Fatalf("usage diagnostics %d %v", status, diagnostics)
	}
	execution := diagnostics["execution"].(map[string]any)
	total := execution["all_attempts_usage"].(map[string]any)
	if total["calls"] != 2.0 || total["unknown_calls"] != 0.0 || total["input_tokens"] != 154.0 || total["output_tokens"] != 18.0 || total["total_tokens"] != 172.0 || total["cost"] != nil {
		t.Fatalf("failed invocation usage was lost or invented: %v", total)
	}
	if len(total["sources"].([]any)) == 0 {
		t.Fatal("usage has no response source")
	}
	assertWireSchema(t, openAPI(t), "Workspace", workspace)
	assertWireSchema(t, openAPI(t), "ReviewDiagnostics", diagnostics)
}

func TestAdministratorMetadataDoesNotAuthorizeContractBody(t *testing.T) {
	api := newAPI(t)
	api.login(t, "demo-clerk", "clerk-demo")
	_, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{"title": "仅成员正文", "contract_type": "procurement_ict"})
	api.login(t, "demo-admin", "admin-demo")
	status, _ := api.do(t, http.MethodGet, "/api/cases/"+created["id"].(string), nil)
	if status != http.StatusNotFound {
		t.Fatalf("administrator role implicitly granted body: %d", status)
	}
	status, settings := api.do(t, http.MethodGet, "/api/settings", nil)
	if status != http.StatusOK || settings["execution"] == nil {
		t.Fatalf("authorized operational view unavailable %d %v", status, settings)
	}
}
