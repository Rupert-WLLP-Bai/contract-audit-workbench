package model

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func readRequestJSON(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return body
}

func writeJSONResponse(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("write response: %v", err)
	}
}

func plannerMessages() []*schema.Message {
	return []*schema.Message{
		{Role: schema.System, Content: "system prompt"},
		{Role: schema.User, Content: "user prompt"},
	}
}

// Generate is the bounded Eino request surface used by ModelPlanner. The
// request identity, the two-message envelope and the provider usage mapping are
// pinned here because planner retries depend on them.
func TestGenerateSendsPlannerEnvelopeAndPinsProviderUsage(t *testing.T) {
	var observed map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer planner-key" {
			t.Errorf("authorization %q", got)
		}
		observed = readRequestJSON(t, r)
		writeJSONResponse(t, w, http.StatusOK, map[string]any{
			"model": "provider-reported-model",
			"usage": map[string]any{"prompt_tokens": 7, "completion_tokens": 3, "total_tokens": 10},
			"choices": []any{map[string]any{
				"finish_reason": "stop",
				"message":       map[string]any{"content": `{"plan":"bounded"}`},
			}},
		})
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL + "/v1/", ChatModel: "panda-local/codex/gpt-6-luna", APIKey: " planner-key "})
	message, err := client.Generate(context.Background(), plannerMessages())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if observed["model"] != "codex/gpt-6-luna" || observed["temperature"] != float64(0) || observed["max_tokens"] != float64(completionTokenLimit) {
		t.Fatalf("request identity %v", observed)
	}
	if _, present := observed["reasoning_effort"]; present {
		t.Fatalf("planner request set an unexpected reasoning effort: %v", observed)
	}
	messages, ok := observed["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("request messages %v", observed["messages"])
	}
	system, _ := messages[0].(map[string]any)
	user, _ := messages[1].(map[string]any)
	if system["role"] != "system" || system["content"] != "system prompt" || user["role"] != "user" || user["content"] != "user prompt" {
		t.Fatalf("message envelope %v", messages)
	}
	if message.Role != schema.Assistant || message.Content != `{"plan":"bounded"}` {
		t.Fatalf("completion %+v", message)
	}
	meta := message.ResponseMeta
	if meta == nil || meta.FinishReason != "stop" || meta.Usage == nil ||
		meta.Usage.PromptTokens != 7 || meta.Usage.CompletionTokens != 3 || meta.Usage.TotalTokens != 10 {
		t.Fatalf("response metadata %+v", meta)
	}
}

func TestGenerateOmitsResponseMetadataWhenProviderReportsNoUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONResponse(t, w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": "answer"}}},
		})
	}))
	defer server.Close()
	message, err := New(Config{BaseURL: server.URL, ChatModel: "chat-model"}).Generate(context.Background(), plannerMessages())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if message.ResponseMeta != nil {
		t.Fatalf("usage was invented: %+v", message.ResponseMeta)
	}
}

func TestGenerateRejectsInvalidPlannerRequestsBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeJSONResponse(t, w, http.StatusOK, map[string]any{"choices": []any{}})
	}))
	defer server.Close()
	client := New(Config{BaseURL: server.URL, ChatModel: "chat-model"})

	third := &schema.Message{Role: schema.User, Content: "extra"}
	tests := []struct {
		name     string
		ctx      context.Context
		messages []*schema.Message
	}{
		{"nil context", nil, plannerMessages()},
		{"one message", context.Background(), plannerMessages()[:1]},
		{"three messages", context.Background(), append(plannerMessages(), third)},
		{"nil system message", context.Background(), []*schema.Message{nil, plannerMessages()[1]}},
		{"nil user message", context.Background(), []*schema.Message{plannerMessages()[0], nil}},
		{"reversed roles", context.Background(), []*schema.Message{
			{Role: schema.User, Content: "user prompt"}, {Role: schema.System, Content: "system prompt"},
		}},
		{"assistant role", context.Background(), []*schema.Message{
			{Role: schema.System, Content: "system prompt"}, {Role: schema.Assistant, Content: "user prompt"},
		}},
		{"empty system content", context.Background(), []*schema.Message{
			{Role: schema.System}, {Role: schema.User, Content: "user prompt"},
		}},
		{"empty user content", context.Background(), []*schema.Message{
			{Role: schema.System, Content: "system prompt"}, {Role: schema.User},
		}},
		{"multimodal user content", context.Background(), []*schema.Message{
			{Role: schema.System, Content: "system prompt"},
			{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeText, Text: "user prompt"}}},
		}},
		{"tool call content", context.Background(), []*schema.Message{
			{Role: schema.System, Content: "system prompt"},
			{Role: schema.User, Content: "user prompt", ToolCalls: []schema.ToolCall{{ID: "call-1", Type: "function"}}},
		}},
		{"oversized prompt", context.Background(), []*schema.Message{
			{Role: schema.System, Content: strings.Repeat("x", 256<<10)},
			{Role: schema.User, Content: "y"},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := client.Generate(test.ctx, test.messages); !errors.Is(err, ErrUnusable) {
				t.Fatalf("invalid planner request accepted: %v", err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid planner request reached the provider: %d calls", calls.Load())
	}
}

// The chat transport maps every provider outcome onto exactly one of the two
// public error classes and never leaks the API key through a provider message.
func TestChatTransportMapsProviderOutcomesAndRedactsSecrets(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{"success", http.StatusOK, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"relation\":\"compatible\"}"}}]}`, nil},
		{"not json", http.StatusOK, `not json`, ErrUnusable},
		{"not json with failure status", http.StatusServiceUnavailable, `offline`, ErrRequest},
		{"empty choices", http.StatusOK, `{"choices":[]}`, ErrUnusable},
		{"blank content", http.StatusOK, `{"choices":[{"finish_reason":"stop","message":{"content":"   "}}]}`, ErrUnusable},
		{"incomplete completion", http.StatusOK, `{"choices":[{"finish_reason":"length","message":{"content":"cut"}}]}`, ErrUnusable},
		{"oversized response", http.StatusOK, strings.Repeat(" ", (1<<20)+1), ErrUnusable},
		{"structured provider error", http.StatusUnauthorized, `{"error":{"message":"key=secret-key rejected"}}`, ErrRequest},
		{"failure without message", http.StatusInternalServerError, `{"choices":[]}`, ErrRequest},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer secret-key" {
					t.Errorf("authorization header %q", r.Header.Get("Authorization"))
				}
				body := readRequestJSON(t, r)
				if body["model"] != "chat" {
					t.Errorf("model id %v", body["model"])
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client := New(Config{BaseURL: server.URL, ChatModel: "panda-local/chat", APIKey: "secret-key"})
			_, err := client.Warranty(context.Background(), nil)
			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("valid completion rejected: %v", err)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error %v, want %v", err, test.wantErr)
			}
			if strings.Contains(err.Error(), "secret-key") {
				t.Fatalf("error leaked the provider credential: %v", err)
			}
		})
	}
}

func TestChatTransportSurfacesTransportAndContextFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := strings.Replace(server.URL, "http://", "http://operator:transport-secret@", 1)
	server.Close()
	_, err := New(Config{BaseURL: address, ChatModel: "chat-model"}).Warranty(context.Background(), nil)
	if !errors.Is(err, ErrRequest) {
		t.Fatalf("refused connection error %v", err)
	}
	if strings.Contains(err.Error(), "transport-secret") {
		t.Fatalf("error exposed endpoint credentials: %v", err)
	}
	_, err = New(Config{BaseURL: "http://[::1", ChatModel: "chat-model"}).Warranty(context.Background(), nil)
	if !errors.Is(err, ErrRequest) {
		t.Fatalf("unbuildable request error %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = New(Config{BaseURL: "http://127.0.0.1:1/v1", ChatModel: "chat-model"}).Warranty(ctx, nil)
	if !errors.Is(err, ErrRequest) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request error %v", err)
	}
}

// A truncated response body is still a failed request, but any provider model
// and usage already read stay visible to the call observer instead of being
// invented or silently dropped.
func TestChatTransportKeepsPartialCompletionWhenTheBodyIsTruncated(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantModel string
		wantUsage bool
	}{
		{"valid partial envelope", `{"model":"observed-model","usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`, "observed-model", true},
		{"invalid partial envelope", `{`, "", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					t.Error("hijacking is not supported by the test server")
					return
				}
				connection, buffered, err := hijacker.Hijack()
				if err != nil {
					t.Errorf("hijack: %v", err)
					return
				}
				defer connection.Close()
				_, _ = fmt.Fprintf(buffered, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 1024\r\n\r\n%s", test.body)
				_ = buffered.Flush()
			}))
			defer server.Close()
			sink := &observationSink{}
			ctx := WithCallObserver(context.Background(), sink)
			_, err := New(Config{BaseURL: server.URL, ChatModel: "chat-model"}).Warranty(ctx, nil)
			if !errors.Is(err, ErrRequest) {
				t.Fatalf("truncated response error %v", err)
			}
			calls := sink.snapshot()
			if len(calls) != 1 || calls[0].Status != "failed" || calls[0].ModelRef != test.wantModel {
				t.Fatalf("partial completion observation %+v", calls)
			}
			if test.wantUsage {
				if calls[0].Usage == nil || calls[0].Usage.InputTokens == nil || *calls[0].Usage.InputTokens != 2 {
					t.Fatalf("partial usage lost: %+v", calls[0].Usage)
				}
			} else if calls[0].Usage != nil {
				t.Fatalf("usage invented from an invalid envelope: %+v", calls[0].Usage)
			}
		})
	}
}

// Gateway calls authenticate the service and carry the delegated scope; the
// client refuses to send a request when either boundary is missing.
func TestChatGatewayCarriesServiceCredentialAndDelegatedScope(t *testing.T) {
	var observed http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = r.Header.Clone()
		writeJSONResponse(t, w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": `{"relation":"compatible"}`}}},
		})
	}))
	defer server.Close()
	scope := DelegatedScope{Kind: "execution", TenantID: "tenant-a", OrgID: "org-a", CaseID: "case-a", ActorID: "actor-a", JobID: "job-a", RunID: "run-a", AttemptID: "attempt-a"}
	client := New(Config{BaseURL: server.URL, ChatModel: "panda-local/chat", APIKey: "service-key", Gateway: true})
	if _, err := client.Warranty(WithDelegatedScope(context.Background(), scope), nil); err != nil {
		t.Fatalf("gateway completion rejected: %v", err)
	}
	if got := observed.Get("X-Contract-Service-Key"); got != "service-key" {
		t.Fatalf("service key header %q", got)
	}
	if got := observed.Get("Authorization"); got != "" {
		t.Fatalf("gateway call forwarded a provider authorization: %q", got)
	}
	var carried DelegatedScope
	if err := json.Unmarshal([]byte(observed.Get("X-Audit-Execution-Scope")), &carried); err != nil || carried != scope {
		t.Fatalf("delegated scope header %q (%v)", observed.Get("X-Audit-Execution-Scope"), err)
	}

	var calls atomic.Int32
	refusing := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer refusing.Close()
	_, err := New(Config{BaseURL: refusing.URL, ChatModel: "chat", Gateway: true}).Warranty(context.Background(), nil)
	if !errors.Is(err, ErrRequest) || !strings.Contains(err.Error(), "credential is missing") {
		t.Fatalf("missing gateway credential error %v", err)
	}
	_, err = New(Config{BaseURL: refusing.URL, ChatModel: "chat", APIKey: "service-key", Gateway: true}).Warranty(context.Background(), nil)
	if !errors.Is(err, ErrRequest) || !strings.Contains(err.Error(), "delegated model scope is missing") {
		t.Fatalf("missing delegated scope error %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("incomplete gateway boundary reached the provider: %d calls", calls.Load())
	}
}

// Reachable reports only what GET /models proves: both configured models are
// listed. The label keeps the endpoint and key state without exposing secrets.
func TestReachableReportsListedModelsAndLabelBoundaries(t *testing.T) {
	cases := []struct {
		name       string
		config     Config
		body       string
		status     int
		wantStatus string
		wantLabel  string
		wantAuth   string
		wantKey    string
	}{
		{
			name: "both models listed", config: Config{ChatModel: "panda-local/chat", OCRModel: "ocr", APIKey: "api-key"},
			body: `{"data":[{"id":"chat"},{"id":"ocr"}]}`, status: http.StatusOK,
			wantStatus: "connected", wantLabel: "已配置密钥", wantAuth: "Bearer api-key",
		},
		{
			name: "missing model", config: Config{ChatModel: "chat", OCRModel: "ocr"},
			body: `{"data":[{"id":"chat"}]}`, status: http.StatusOK,
			wantStatus: "unreachable", wantLabel: "模型列表里没有配置的模型",
		},
		{
			name: "provider failure", config: Config{ChatModel: "chat", OCRModel: "ocr"},
			body: `{"data":[{"id":"chat"}]}`, status: http.StatusBadGateway,
			wantStatus: "unreachable", wantLabel: "端点不可达",
		},
		{
			name: "malformed list", config: Config{ChatModel: "chat", OCRModel: "ocr"},
			body: `not json`, status: http.StatusOK,
			wantStatus: "unreachable", wantLabel: "端点不可达",
		},
		{
			name: "empty list", config: Config{ChatModel: "chat", OCRModel: "ocr"},
			body: `{"data":[]}`, status: http.StatusOK,
			wantStatus: "unreachable", wantLabel: "端点不可达",
		},
		{
			name: "gateway list credential", config: Config{ChatModel: "chat", OCRModel: "ocr", APIKey: "service-key", Gateway: true},
			body: `{"data":[{"id":"chat"},{"id":"ocr"}]}`, status: http.StatusOK,
			wantStatus: "connected", wantLabel: "已配置密钥", wantKey: "service-key",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
					t.Errorf("list request %s %s", r.Method, r.URL.Path)
				}
				if got := r.Header.Get("Authorization"); got != test.wantAuth {
					t.Errorf("authorization %q, want %q", got, test.wantAuth)
				}
				if got := r.Header.Get("X-Contract-Service-Key"); got != test.wantKey {
					t.Errorf("service key header %q, want %q", got, test.wantKey)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			config := test.config
			config.BaseURL = server.URL + "/v1"
			status, label := New(config).Reachable(context.Background())
			if status != test.wantStatus || !strings.Contains(label, test.wantLabel) {
				t.Fatalf("status %q label %q, want %q containing %q", status, label, test.wantStatus, test.wantLabel)
			}
			if strings.Contains(label, "api-key") || strings.Contains(label, "service-key") {
				t.Fatalf("label exposed the credential: %q", label)
			}
		})
	}

	status, label := New(Config{ChatModel: "chat", OCRModel: "ocr"}).Reachable(context.Background())
	if status != "unreachable" || !strings.Contains(label, "未配置有效端点") || !strings.Contains(label, "未配置密钥") {
		t.Fatalf("unconfigured endpoint label %q %q", status, label)
	}
	status, label = New(Config{BaseURL: "not-a-url", ChatModel: "chat", OCRModel: "ocr"}).Reachable(context.Background())
	if status != "unreachable" || !strings.Contains(label, "未配置有效端点") {
		t.Fatalf("invalid endpoint label %q %q", status, label)
	}
}

// Transcribe always sends a data URI and selects the declared MIME type, or the
// magic-byte fallback when the caller supplies no usable type.
func TestTranscribeSelectsDeclaredOrDetectedImageMediaType(t *testing.T) {
	cases := []struct {
		name  string
		media string
		body  []byte
		want  string
	}{
		{"declared jpeg", "image/jpeg", []byte{0x01}, "image/jpeg"},
		{"declared jpg with parameters", "IMAGE/JPG; charset=binary", []byte{0x01}, "image/jpeg"},
		{"declared png", "image/png", []byte{0x01}, "image/png"},
		{"unknown type with jpeg magic", "application/octet-stream", []byte{0xff, 0xd8, 0xff}, "image/jpeg"},
		{"empty type with png bytes", "", []byte{0x89, 'P', 'N', 'G'}, "image/png"},
		{"unknown type without magic", "text/plain", []byte{0x01}, "image/png"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := readRequestJSON(t, r)
				messages, ok := body["messages"].([]any)
				if !ok || len(messages) != 1 {
					t.Fatalf("messages %v", body["messages"])
				}
				content, ok := messages[0].(map[string]any)["content"].([]any)
				if !ok || len(content) != 2 {
					t.Fatalf("content %v", messages[0].(map[string]any)["content"])
				}
				text, _ := content[0].(map[string]any)
				image, _ := content[1].(map[string]any)
				if text["type"] != "text" || text["text"] != transcriptionInstruction {
					t.Errorf("text part %v", text)
				}
				url, _ := image["image_url"].(map[string]any)["url"].(string)
				prefix := "data:" + test.want + ";base64,"
				if image["type"] != "image_url" || !strings.HasPrefix(url, prefix) {
					t.Errorf("image part %v", image)
				}
				if encoded := strings.TrimPrefix(url, prefix); encoded != base64.StdEncoding.EncodeToString(test.body) {
					t.Errorf("image bytes %q", encoded)
				}
				writeJSONResponse(t, w, http.StatusOK, map[string]any{
					"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": "  transcribed  "}}},
				})
			}))
			defer server.Close()
			text, err := New(Config{BaseURL: server.URL, OCRModel: "vision-model"}).Transcribe(context.Background(), test.media, test.body)
			if err != nil || text != "transcribed" {
				t.Fatalf("transcription %q %v", text, err)
			}
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONResponse(t, w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": "   "}}},
		})
	}))
	defer server.Close()
	if _, err := New(Config{BaseURL: server.URL, OCRModel: "vision-model"}).Transcribe(context.Background(), "image/png", []byte{0x01}); !errors.Is(err, ErrUnusable) {
		t.Fatalf("empty transcription accepted: %v", err)
	}
}

// The prompts are part of the frozen request contract: every material is
// identified, unparsed materials keep their reason, and the approved policy is
// reproduced verbatim for the cloud review.
func TestWarrantyAndCloudPromptsCarryFrozenMaterialIdentity(t *testing.T) {
	docs := []Material{
		{ID: "contract-1", Role: "main", Text: "质保期为36个月", Ready: true},
		{ID: "annex-1", Role: "annex", Ready: false, ParseError: "扫描件无法识别"},
		{ID: "annex-2", Role: "annex", Ready: false},
	}
	var prompts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readRequestJSON(t, r)
		messages := body["messages"].([]any)
		prompts = append(prompts, messages[0].(map[string]any)["content"].(string))
		answer := `{"relation":"compatible"}`
		if len(prompts) == 2 {
			answer = `{"findings":[]}`
		}
		writeJSONResponse(t, w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": answer}}},
		})
	}))
	defer server.Close()
	client := New(Config{BaseURL: server.URL, ChatModel: "chat"})
	if _, err := client.Warranty(context.Background(), docs); err != nil {
		t.Fatalf("Warranty: %v", err)
	}
	for _, want := range []string{"document_id=contract-1", "正文：质保期为36个月", "document_id=annex-1", "未能解析：扫描件无法识别", "document_id=annex-2", "未能解析：没有正文"} {
		if !strings.Contains(prompts[0], want) {
			t.Fatalf("warranty prompt lost %q: %s", want, prompts[0])
		}
	}

	policy := CloudPolicy{ID: "policy-1", Version: "v3", Body: "制度正文"}
	if _, err := client.CloudContract(context.Background(), policy, docs); err != nil {
		t.Fatalf("CloudContract: %v", err)
	}
	for _, want := range []string{"制度ID=policy-1 version=v3", "制度正文：\n制度正文", "document_id=contract-1"} {
		if !strings.Contains(prompts[1], want) {
			t.Fatalf("cloud prompt lost %q: %s", want, prompts[1])
		}
	}
}
