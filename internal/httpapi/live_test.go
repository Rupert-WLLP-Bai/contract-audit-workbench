//go:build live

package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"contractaudit/internal/model"
)

func TestLiveReviewAndOCR(t *testing.T) {
	// Only the explicit live build tag enables this test. Respect the operator's
	// endpoint and model configuration; never probe an unrelated hard-coded port.
	cfg := model.FromEnv()
	seen := &statusCapture{base: http.DefaultTransport}
	client := model.New(cfg).WithTransport(seen)
	api := newAPIWith(t, client, client)
	api.login(t, "demo-admin", "admin-demo")

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

	_, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "实机质保", "contract_type": "procurement_ict",
	})
	caseID := created["id"].(string)
	api.upload(t, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件要求质保36个月。"))
	api.upload(t, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。"))
	status, review := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("review %d %v", status, review)
	}
	_, ws := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+review["id"].(string), nil)
	run := ws["run"].(map[string]any)
	t.Logf("chat run state=%v model_ref=%v failure=%v", run["state"], run["model_ref"], run["failure_message"])
	if run["state"] == "failed" || run["state"] == "partial" {
		t.Fatalf("live review failed: %v", run["failure_message"])
	}
	if run["model_ref"] != cfg.ChatModel {
		t.Fatalf("model_ref %+v", run["model_ref"])
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
		t.Fatal("model quotes were not located in the texts")
	}

	_, created = api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "实机图片", "contract_type": "procurement_ict",
	})
	imageCase := created["id"].(string)
	imageID := api.upload(t, imageCase, "procurement", "scan.png", "image/png", model.SampleWarrantyPNG)
	api.upload(t, imageCase, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保12个月。"))
	status, review = api.do(t, http.MethodPost, "/api/cases/"+imageCase+"/reviews", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("ocr review %d %v", status, review)
	}
	_, ws = api.do(t, http.MethodGet, "/api/cases/"+imageCase+"/reviews/"+review["id"].(string), nil)
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
		t.Logf("upstream %s", line)
	}
	var chatOK, listOK bool
	endpoint, err := url.Parse(cfg.BaseURL)
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
		t.Fatalf("upstream calls %v", seen.snapshot())
	}
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
	base  http.RoundTripper
	mu    sync.Mutex
	lines []string
}

func (c *statusCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	base := c.base
	if base == nil {
		base = http.DefaultTransport
	}
	res, err := base.RoundTrip(req)
	code := 0
	if res != nil {
		code = res.StatusCode
	}
	c.mu.Lock()
	c.lines = append(c.lines, req.Method+" "+req.URL.Path+" "+strconv.Itoa(code))
	c.mu.Unlock()
	return res, err
}

func (c *statusCapture) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.lines))
	copy(out, c.lines)
	return out
}
