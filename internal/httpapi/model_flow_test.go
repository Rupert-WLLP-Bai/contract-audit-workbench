package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"contractaudit/internal/model"
)

func TestMalformedModelOutputRetainsRulesAsPartialRun(t *testing.T) {
	var gotModel string
	var sawAuth bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sawAuth = true
		}
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"不是 JSON"}}]}`))
	}))
	defer upstream.Close()

	client := model.New(model.Config{
		BaseURL:   upstream.URL + "/v1",
		ChatModel: model.DefaultChatModel,
		OCRModel:  model.DefaultOCRModel,
	})
	api := newAPIWith(t, client, nil)
	api.login(t, "demo-admin", "admin-demo")
	_, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "坏输出", "contract_type": "procurement_ict",
	})
	caseID := created["id"].(string)
	api.upload(t, caseID, "procurement", "a.txt", "text/plain", []byte("采购文件要求质保36个月。"))
	api.upload(t, caseID, "draft_contract", "b.txt", "text/plain", []byte("拟签合同约定质保24个月。"))
	status, review := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("start %d %v", status, review)
	}
	_, ws := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+review["id"].(string), nil)
	run := ws["run"].(map[string]any)
	if run["state"] != "partial" {
		t.Fatalf("run %+v", run)
	}
	message, _ := run["partial_reason"].(string)
	if !strings.Contains(message, "无法解析") {
		t.Fatalf("failure %q", message)
	}
	if findings := ws["findings"].([]any); len(findings) != 1 || findings[0].(map[string]any)["origin"] != "rule" || findings[0].(map[string]any)["relation"] != "conflict" {
		t.Fatalf("deterministic findings were lost: %+v", findings)
	}
	if gotModel != model.GatewayID(model.DefaultChatModel) {
		t.Fatalf("gateway model %s", gotModel)
	}
	if sawAuth {
		t.Fatal("empty key sent Authorization")
	}
	if run["model_ref"] != model.DefaultChatModel {
		t.Fatalf("model_ref %+v", run["model_ref"])
	}
}

func TestImageOCRFeedsReview(t *testing.T) {
	var chatModel, ocrModel string
	var sawImageURL bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content any `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(raw), "image_url") {
			ocrModel = body.Model
			sawImageURL = true
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"质保期为36个月。"}}]}`))
			return
		}
		chatModel = body.Model
		prompt := ""
		if len(body.Messages) > 0 {
			if text, ok := body.Messages[0].Content.(string); ok {
				prompt = text
			}
		}
		payload, _ := json.Marshal(map[string]any{
			"claim": "图片与合同的质保月数不一致", "relation": "conflict", "disposition": "confirm_risk",
			"quotes": quotesFromPrompt(prompt),
		})
		response, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": string(payload)}}},
		})
		_, _ = w.Write(response)
	}))
	defer upstream.Close()

	client := model.New(model.Config{
		BaseURL: upstream.URL + "/v1", ChatModel: model.DefaultChatModel, OCRModel: model.DefaultOCRModel,
	})
	api := newAPIWith(t, client, nil)
	api.login(t, "demo-admin", "admin-demo")
	_, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{
		"title": "图片", "contract_type": "procurement_ict",
	})
	caseID := created["id"].(string)
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	docA := api.upload(t, caseID, "procurement", "scan.png", "image/png", png)
	api.upload(t, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。"))

	status, review := api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("start %d %v", status, review)
	}
	_, ws := api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+review["id"].(string), nil)
	var imageReady bool
	for _, item := range ws["documents"].([]any) {
		doc := item.(map[string]any)
		if doc["id"] == docA {
			imageReady = doc["parse_status"] == "ready" && doc["parse_error"] == nil
		}
	}
	if !imageReady || !sawImageURL || ocrModel != model.GatewayID(model.DefaultOCRModel) {
		t.Fatalf("ocr ready=%v url=%v model=%s docs=%v", imageReady, sawImageURL, ocrModel, ws["documents"])
	}
	run := ws["run"].(map[string]any)
	if run["state"] != "awaiting_human" || chatModel != model.GatewayID(model.DefaultChatModel) {
		t.Fatalf("run %+v chat=%s", run, chatModel)
	}
	var sawText bool
	var located int
	for _, item := range ws["documents"].([]any) {
		doc := item.(map[string]any)
		if doc["id"] == docA && doc["text"] == "质保期为36个月。" {
			sawText = true
		}
	}
	finding := ws["findings"].([]any)[0].(map[string]any)
	if finding["origin"] != "rule" || finding["disposition_suggestion"] != "confirm_risk" {
		t.Fatalf("finding %+v", finding)
	}
	for _, item := range finding["evidence"].([]any) {
		anchor := item.(map[string]any)
		if anchor["status"] == "located" && anchor["bbox"] == nil && anchor["text_offset"] != nil {
			located++
		}
	}
	if !sawText || located != 2 {
		t.Fatalf("text=%v located=%d ws=%v", sawText, located, ws)
	}
}

func quotesFromPrompt(prompt string) []map[string]string {
	var quotes []map[string]string
	var id string
	for _, line := range strings.Split(prompt, "\n") {
		if _, rest, ok := strings.Cut(line, "document_id="); ok {
			id, _, _ = strings.Cut(rest, " ")
			id = strings.TrimSpace(id)
		}
		if text, ok := strings.CutPrefix(line, "正文："); ok && id != "" {
			quotes = append(quotes, map[string]string{"document_id": id, "quote": text})
		}
	}
	return quotes
}

func TestSettingsReportsConfiguredModels(t *testing.T) {
	key := "settings-key-not-for-logs"
	var auth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": model.DefaultChatModel}, {"id": model.DefaultOCRModel}}})
	}))
	defer upstream.Close()
	client := model.New(model.Config{
		BaseURL:   upstream.URL + "/v1",
		ChatModel: model.DefaultChatModel,
		OCRModel:  model.DefaultOCRModel,
		APIKey:    key,
	})
	api := newAPIWith(t, scriptModels{}, client)
	api.login(t, "demo-admin", "admin-demo")
	status, raw := api.bytes(t, http.MethodGet, "/api/settings", nil, "")
	if status != http.StatusOK {
		t.Fatalf("settings %d %s", status, raw)
	}
	if strings.Contains(string(raw), key) {
		t.Fatal("settings response leaked the key")
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	modelView := body["model"].(map[string]any)
	if modelView["status"] != "connected" {
		t.Fatalf("status %+v", modelView)
	}
	label, _ := modelView["label"].(string)
	if !strings.Contains(label, model.DefaultChatModel) || !strings.Contains(label, model.DefaultOCRModel) || !strings.Contains(label, "已配置密钥") {
		t.Fatalf("label %s", label)
	}
	if auth != "Bearer "+key {
		t.Fatal("authorization header was not the configured bearer")
	}

	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	down := model.New(model.Config{
		BaseURL: closedURL + "/v1", ChatModel: model.DefaultChatModel, OCRModel: model.DefaultOCRModel,
	})
	api = newAPIWith(t, scriptModels{}, down)
	api.login(t, "demo-admin", "admin-demo")
	_, raw = api.bytes(t, http.MethodGet, "/api/settings", nil, "")
	if strings.Contains(string(raw), key) {
		t.Fatal("unreachable settings leaked a key")
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	modelView = body["model"].(map[string]any)
	if modelView["status"] != "unreachable" {
		t.Fatalf("down %+v", modelView)
	}
	if label, _ = modelView["label"].(string); !strings.Contains(label, "端点不可达") {
		t.Fatalf("down label %s", label)
	}
}
