package modelgateway

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

func tokenFor(role string) string { return "private-" + role + "-credential-0123456789" }

func TestDecodeRequestAcceptsEncodedOCRAndRejectsOversize(t *testing.T) {
	const prefix = `{"model":"ocr","max_tokens":1,"image":"`
	const suffix = `"}`
	for _, test := range []struct {
		name      string
		size      int
		wantError bool
	}{
		{"encoded maximum PNG", (24 << 20) * 4 / 3, false},
		{"at request limit", maxRequestBytes - len(prefix) - len(suffix), false},
		{"over request limit", maxRequestBytes - len(prefix) - len(suffix) + 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := prefix + strings.Repeat("A", test.size) + suffix
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
			req.Header.Set("X-Audit-Execution-Scope", "{}")
			got, _, _, _, err := decodeRequest(httptest.NewRecorder(), req)
			if (err != nil) != test.wantError || (!test.wantError && len(got) != len(body)) {
				t.Fatalf("decode length=%d err=%v", len(got), err)
			}
		})
	}
}

func contentOf(t *testing.T, response []byte) string {
	t.Helper()
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(response, &parsed); err != nil {
		t.Fatalf("fixture response is not a chat completion: %v", err)
	}
	if len(parsed.Choices) != 1 {
		t.Fatalf("fixture response has %d choices", len(parsed.Choices))
	}
	return parsed.Choices[0].Message.Content
}

func fixtureService() *Service {
	return &Service{config: Config{
		Mode: "fixture", ChatModel: "fixture-chat", OCRModel: "fixture-ocr",
		FixtureOCRContent: "质保期为36个月。",
	}}
}

func TestFixturePlannerSelectsOnlyRequestedRegisteredTools(t *testing.T) {
	user, err := json.Marshal(map[string]any{
		"scope": []string{"compliance"},
		"gaps":  []map[string]string{{"id": "gap-amount", "domain": "compliance", "capability": "amount"}},
		"tools": []map[string]any{{"id": "baseline.compliance", "domain": "compliance", "capabilities": []string{"authorization_basis"}},
			{"id": "source.compliance", "domain": "compliance", "capabilities": []string{"amount"}}},
		"previously_called": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"model": "fixture-chat", "messages": []map[string]string{
		{"role": "system", "content": "Select only registered read-only tools."},
		{"role": "user", "content": string(user)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	content := contentOf(t, fixtureService().fixtureResponse("fixture-chat", body))
	var plan struct {
		Calls []struct {
			ToolID string `json:"tool_id"`
			Domain string `json:"domain"`
			GapID  string `json:"gap_id"`
		} `json:"calls"`
	}
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		t.Fatalf("fixture did not return a plan: %v", err)
	}
	if len(plan.Calls) != 1 || plan.Calls[0].ToolID != "source.compliance" ||
		plan.Calls[0].Domain != "compliance" || plan.Calls[0].GapID != "gap-amount" {
		t.Fatalf("fixture plan exceeded its registered request: %+v", plan)
	}
}

// The fixture is a stub, not a model: it may cite only material spans the review
// prompt itself carries, and it must not answer a prompt that carries none.
func TestFixtureChatAnswerCitesOnlyPromptSpans(t *testing.T) {
	service := fixtureService()
	prompt := `{"messages":[{"content":"document_id=doc-a 采购文件\n正文：采购文件要求质保36个月。\ndocument_id=doc-b 合同草稿\n正文：拟签合同约定质保24个月。"}]}`

	content := contentOf(t, service.fixtureResponse("fixture-chat", []byte(prompt)))
	warranty, err := model.ParseWarranty(content)
	if err != nil {
		t.Fatalf("the derived fixture answer is not a usable warranty answer: %v", err)
	}
	if warranty.Relation != "conflict" || warranty.Disposition == "" {
		t.Fatalf("derived answer %+v", warranty)
	}
	if len(warranty.Quotes) != 2 {
		t.Fatalf("derived %d quotes, want the two prompt spans", len(warranty.Quotes))
	}
	if warranty.Quotes[0].DocumentID != "doc-a" || warranty.Quotes[0].Quote != "采购文件要求质保36个月。" ||
		warranty.Quotes[1].DocumentID != "doc-b" || warranty.Quotes[1].Quote != "拟签合同约定质保24个月。" {
		t.Fatalf("derived quotes are not the prompt spans: %+v", warranty.Quotes)
	}

	if answer, ok := fixtureWarrantyAnswer([]byte(`{"messages":[{"content":"请判断质保期是否一致"}]}`)); ok {
		t.Fatalf("the fixture answered a prompt without material spans: %q", answer)
	}

	service.config.FixtureChatContent = `{"claim":"固定输出","relation":"compatible"}`
	if explicit := contentOf(t, service.fixtureResponse("fixture-chat", []byte(prompt))); explicit != service.config.FixtureChatContent {
		t.Fatalf("explicit fixture content was replaced: %q", explicit)
	}

	if ocr := contentOf(t, service.fixtureResponse("fixture-ocr", []byte(prompt))); ocr != "质保期为36个月。" {
		t.Fatalf("the OCR model answered with %q", ocr)
	}
}

// A fixture without distinct model identifiers cannot dispatch an answer
// unambiguously, and one without an OCR transcription is refused.
func TestFixtureRequiresDistinctIdentifiersAndOCRContent(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	for _, config := range []Config{
		{Mode: "fixture", ChatModel: "same", OCRModel: "same", FixtureOCRContent: "text"},
		{Mode: "fixture", ChatModel: "fixture-chat", OCRModel: "fixture-ocr"},
	} {
		config.WorkerCredential = tokenFor("worker")
		config.DocumentCredential = tokenFor("document")
		config.MaxConcurrency, config.QueueCapacity = 1, 1
		config.RequestTimeout, config.CircuitCooldown = time.Minute, time.Minute
		config.CircuitFailureThreshold = 1
		config.Budget = store.ModelBudgetLimits{LeaseDuration: time.Minute}
		if _, err := New(&store.Store{}, config); err == nil {
			t.Fatalf("incomplete fixture configuration was accepted: %+v", config)
		}
	}
	valid, err := New(&store.Store{}, Config{
		Mode: "fixture", ChatModel: "fixture-chat", OCRModel: "fixture-ocr", FixtureOCRContent: "text",
		WorkerCredential: tokenFor("worker"), DocumentCredential: tokenFor("document"),
		MaxConcurrency: 1, QueueCapacity: 1, RequestTimeout: time.Minute,
		CircuitFailureThreshold: 1, CircuitCooldown: time.Minute,
		Budget: store.ModelBudgetLimits{LeaseDuration: time.Minute},
	})
	if err != nil || valid == nil {
		t.Fatalf("a complete fixture configuration was refused: %v", err)
	}
}
