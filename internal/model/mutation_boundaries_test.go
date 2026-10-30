package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func encodeCloudReview(findings ...cloudFindingJSON) string {
	raw, err := json.Marshal(cloudReviewJSON{Findings: findings})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func validCloudFindingFor(domain string) cloudFindingJSON {
	return cloudFindingJSON{
		Domain: domain, PolicyClause: "制度条款", Claim: "缺口建议", DocumentID: "contract-1", Quote: "合同原文",
	}
}

func encodeWarranty(body warrantyJSON) string {
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// CloudContract must propagate the request failure instead of parsing an empty completion.
func TestCloudContractPropagatesRequestFailure(t *testing.T) {
	_, err := New(Config{}).CloudContract(context.Background(), CloudPolicy{}, nil)
	if !errors.Is(err, ErrRequest) {
		t.Fatalf("unconfigured cloud contract error = %v, want ErrRequest", err)
	}
}

// Findings must be present (null is not an empty review) and at most four.
func TestParseCloudContractFindingsBounds(t *testing.T) {
	for _, raw := range []string{`{}`, `{"findings":null}`} {
		if _, err := ParseCloudContract(raw); !errors.Is(err, ErrUnusable) {
			t.Fatalf("%s: accepted absent findings (%v)", raw, err)
		}
	}
	domains := []string{"liability", "data_security", "intellectual_property", "renewal"}
	four := make([]cloudFindingJSON, 0, len(domains))
	for _, domain := range domains {
		four = append(four, validCloudFindingFor(domain))
	}
	review, err := ParseCloudContract(encodeCloudReview(four...))
	if err != nil || len(review.Findings) != 4 {
		t.Fatalf("four findings rejected: %+v %v", review, err)
	}
	five := append(append([]cloudFindingJSON(nil), four...), validCloudFindingFor("liability"))
	if _, err := ParseCloudContract(encodeCloudReview(five...)); !errors.Is(err, ErrUnusable) {
		t.Fatalf("five findings accepted: %v", err)
	}
	if _, err := ParseCloudContract(`{"findings":[]}`); err != nil {
		t.Fatalf("empty findings array rejected: %v", err)
	}
}

// Each cloud finding field has an inclusive upper bound and a non-empty floor.
func TestParseCloudContractFindingFieldBoundaries(t *testing.T) {
	base := validCloudFindingFor("liability")
	cases := []struct {
		name    string
		finding cloudFindingJSON
		wantErr bool
	}{
		{"claim empty", func() cloudFindingJSON { f := base; f.Claim = ""; return f }(), true},
		{"document id empty", func() cloudFindingJSON { f := base; f.DocumentID = ""; return f }(), true},
		{"document id 128", func() cloudFindingJSON { f := base; f.DocumentID = strings.Repeat("d", 128); return f }(), false},
		{"document id 129", func() cloudFindingJSON { f := base; f.DocumentID = strings.Repeat("d", 129); return f }(), true},
		{"clause 1000 runes", func() cloudFindingJSON { f := base; f.PolicyClause = strings.Repeat("制", 1000); return f }(), false},
		{"clause 1001 runes", func() cloudFindingJSON { f := base; f.PolicyClause = strings.Repeat("制", 1001); return f }(), true},
		{"claim 1000 runes", func() cloudFindingJSON { f := base; f.Claim = strings.Repeat("缺", 1000); return f }(), false},
		{"claim 1001 runes", func() cloudFindingJSON { f := base; f.Claim = strings.Repeat("缺", 1001); return f }(), true},
		{"quote 10000 runes", func() cloudFindingJSON { f := base; f.Quote = strings.Repeat("原", 10000); return f }(), false},
		{"quote 10001 runes", func() cloudFindingJSON { f := base; f.Quote = strings.Repeat("原", 10001); return f }(), true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseCloudContract(encodeCloudReview(test.finding))
			if test.wantErr {
				if !errors.Is(err, ErrUnusable) {
					t.Fatalf("accepted invalid finding: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("rejected boundary finding: %v", err)
			}
		})
	}
}

// Output limits are inclusive: 1000 claim runes, 32 quotes, 128 document id
// bytes and 10000 quote runes are accepted; one more is rejected.
func TestWarrantyOutputBoundaries(t *testing.T) {
	compatible := func(claim string, quotes []warrantyQuoteJSON) warrantyJSON {
		return warrantyJSON{Claim: claim, Relation: "compatible", Quotes: quotes}
	}
	cases := []struct {
		name    string
		body    warrantyJSON
		wantErr bool
	}{
		{"claim 1000 runes", compatible(strings.Repeat("缺", 1000), nil), false},
		{"claim 1001 runes", compatible(strings.Repeat("缺", 1001), nil), true},
		{"32 quotes", compatible("存在缺口", repeatQuotes(32, "d", "原")), false},
		{"33 quotes", compatible("存在缺口", repeatQuotes(33, "d", "原")), true},
		{"quote id 128", compatible("存在缺口", []warrantyQuoteJSON{{DocumentID: strings.Repeat("d", 128), Quote: "原文"}}), false},
		{"quote id 129", compatible("存在缺口", []warrantyQuoteJSON{{DocumentID: strings.Repeat("d", 129), Quote: "原文"}}), true},
		{"quote id empty", compatible("存在缺口", []warrantyQuoteJSON{{DocumentID: "", Quote: "原文"}}), true},
		{"quote text empty", compatible("存在缺口", []warrantyQuoteJSON{{DocumentID: "d", Quote: ""}}), true},
		{"quote text 10000 runes", compatible("存在缺口", []warrantyQuoteJSON{{DocumentID: "d", Quote: strings.Repeat("原", 10000)}}), false},
		{"quote text 10001 runes", compatible("存在缺口", []warrantyQuoteJSON{{DocumentID: "d", Quote: strings.Repeat("原", 10001)}}), true},
		{"reported claim 1000 runes", warrantyJSON{
			Claim: strings.Repeat("缺", 1000), Relation: "conflict", Disposition: "confirm_risk",
			Quotes: []warrantyQuoteJSON{{DocumentID: "d", Quote: "原文"}},
		}, false},
		{"reported claim 1001 runes", warrantyJSON{
			Claim: strings.Repeat("缺", 1001), Relation: "conflict", Disposition: "confirm_risk",
			Quotes: []warrantyQuoteJSON{{DocumentID: "d", Quote: "原文"}},
		}, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseWarranty(encodeWarranty(test.body))
			if test.wantErr {
				if !errors.Is(err, ErrUnusable) {
					t.Fatalf("accepted invalid output: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("rejected boundary output: %v", err)
			}
		})
	}
}

func repeatQuotes(count int, documentID, text string) []warrantyQuoteJSON {
	quotes := make([]warrantyQuoteJSON, count)
	for index := range quotes {
		quotes[index] = warrantyQuoteJSON{DocumentID: documentID, Quote: text}
	}
	return quotes
}

// The planner request has exactly two plain messages and a combined content
// limit of 256 KiB, counted across both positions and inclusive at the limit.
func TestGeneratePlannerMessageBounds(t *testing.T) {
	var calls int
	client := New(Config{BaseURL: "http://model.example/v1", ChatModel: "chat"})
	client.WithTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return response(http.StatusOK, `{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`), nil
	}))

	plainSystem := &schema.Message{Role: schema.System, Content: "x"}
	plainUser := &schema.Message{Role: schema.User, Content: "user"}

	systemWithToolCalls := &schema.Message{
		Role: schema.System, Content: "system", ToolCalls: []schema.ToolCall{{ID: "call-1", Type: "function"}},
	}
	if _, err := client.Generate(context.Background(), []*schema.Message{systemWithToolCalls, plainUser}); !errors.Is(err, ErrUnusable) {
		t.Fatalf("system tool call accepted: %v", err)
	}
	if calls != 0 {
		t.Fatalf("invalid system message reached the provider")
	}

	oversizedUser := &schema.Message{Role: schema.User, Content: strings.Repeat("y", 256<<10)}
	if _, err := client.Generate(context.Background(), []*schema.Message{plainSystem, oversizedUser}); !errors.Is(err, ErrUnusable) {
		t.Fatalf("oversized second message accepted: %v", err)
	}
	if calls != 0 {
		t.Fatalf("oversized request reached the provider")
	}

	exactUser := &schema.Message{Role: schema.User, Content: strings.Repeat("y", (256<<10)-1)}
	if _, err := client.Generate(context.Background(), []*schema.Message{plainSystem, exactUser}); err != nil {
		t.Fatalf("exact content limit rejected: %v", err)
	}
	if calls != 1 {
		t.Fatalf("exact-limit request calls = %d, want 1", calls)
	}
}

// A response larger than 1 MiB is refused with the size message, and a valid
// response of exactly 1 MiB is still accepted.
func TestChatResponseSizeBoundaries(t *testing.T) {
	client := New(Config{BaseURL: "http://model.example/v1", OCRModel: "ocr"})
	client.WithTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, strings.Repeat(" ", (1<<20)+1)), nil
	}))
	if _, err := client.Transcribe(context.Background(), "image/png", []byte{0x89}); err == nil || !strings.Contains(err.Error(), "response too large") {
		t.Fatalf("oversized response error = %v", err)
	}

	prefix := `{"choices":[{"finish_reason":"stop","message":{"content":"`
	suffix := `"}}]}`
	padding := (1 << 20) - len(prefix) - len(suffix)
	if padding <= 0 {
		t.Fatalf("invalid fixture sizes")
	}
	body := prefix + strings.Repeat("a", padding) + suffix
	if len(body) != 1<<20 {
		t.Fatalf("fixture length = %d, want %d", len(body), 1<<20)
	}
	client = New(Config{BaseURL: "http://model.example/v1", OCRModel: "ocr"})
	client.WithTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, body), nil
	}))
	text, err := client.Transcribe(context.Background(), "image/png", []byte{0x89})
	if err != nil || len(text) != padding {
		t.Fatalf("exact 1 MiB response: len=%d err=%v", len(text), err)
	}
}

// The chat request requires a non-empty gateway id, endpoint and message list.
func TestNewChatRequestRequiresConfiguration(t *testing.T) {
	ctx := context.Background()
	messages := []chatMessage{{Role: "user", Content: "prompt"}}
	configured := New(Config{BaseURL: "http://model.example/v1", ChatModel: "chat"})
	if _, _, err := configured.newChatRequest(ctx, "chat", messages, 1, ""); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	if _, _, err := configured.newChatRequest(ctx, " ", messages, 1, ""); err == nil {
		t.Fatal("empty gateway id accepted")
	}
	unconfigured := New(Config{})
	if _, _, err := unconfigured.newChatRequest(ctx, "chat", messages, 1, ""); err == nil {
		t.Fatal("empty endpoint accepted")
	}
}

// The partial completion is parsed when the buffered body is exactly 1 MiB.
func TestPartialChatCompletionBoundary(t *testing.T) {
	prefix := `{"model":"m","usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3},"pad":"`
	suffix := `"}`
	padding := (1 << 20) - len(prefix) - len(suffix)
	raw := prefix + strings.Repeat("a", padding) + suffix
	if len(raw) != 1<<20 {
		t.Fatalf("fixture length = %d, want %d", len(raw), 1<<20)
	}
	completion := New(Config{}).partialChatCompletion([]byte(raw))
	if completion.Model != "m" {
		t.Fatalf("exact 1 MiB partial completion dropped: %+v", completion)
	}
}

// A structured provider error message is surfaced, after redaction.
func TestChatProviderErrorMessageIsSurfaced(t *testing.T) {
	client := New(Config{BaseURL: "http://model.example/v1", ChatModel: "chat"})
	client.WithTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusUnauthorized, `{"error":{"message":"provider rejected the request"}}`), nil
	}))
	_, err := client.Warranty(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "provider rejected the request") {
		t.Fatalf("provider message lost: %v", err)
	}
}

// Endpoint labels accept http and https only and require a real host.
func TestEndpointLabelSchemeAndHostBoundaries(t *testing.T) {
	cases := map[string]string{
		"https://model.example/v1": "https://model.example/v1",
		"http://model.example/v1":  "http://model.example/v1",
		"http:///path":             "未配置有效端点",
		"ftp://model.example/v1":   "未配置有效端点",
		"not-a-url":                "未配置有效端点",
	}
	for raw, want := range cases {
		if got := endpointLabel(raw); got != want {
			t.Fatalf("endpointLabel(%q) = %q, want %q", raw, got, want)
		}
	}
}

// Prompt rendering numbers documents from one and distinguishes ready from
// unparsed materials.
func TestPromptRenderingBoundaries(t *testing.T) {
	docs := []Material{
		{ID: "contract-1", Role: "main", Text: "质保期为36个月", Ready: true},
		{ID: "annex-1", Role: "annex", Ready: false, ParseError: "扫描失败"},
		{ID: "annex-2", Role: "annex", Ready: false},
	}
	warranty := warrantyPrompt(docs)
	for _, want := range []string{"1. document_id=contract-1", "正文：质保期为36个月", "2. document_id=annex-1", "未能解析：扫描失败", "3. document_id=annex-2", "未能解析：没有正文"} {
		if !strings.Contains(warranty, want) {
			t.Fatalf("warranty prompt lost %q: %s", want, warranty)
		}
	}
	cloud := cloudContractPrompt(CloudPolicy{ID: "policy-1", Version: "v1", Body: "制度正文"}, docs)
	for _, want := range []string{"1. document_id=contract-1", "正文：质保期为36个月", "2. document_id=annex-1", "未能解析：扫描失败", "3. document_id=annex-2", "未能解析：没有正文"} {
		if !strings.Contains(cloud, want) {
			t.Fatalf("cloud prompt lost %q: %s", want, cloud)
		}
	}
}

// listed must match the configured id, its trimmed gateway id, or the gateway
// id of a listed id, including catalog-prefixed and whitespace-padded entries.
func TestListedMatchesCatalogIdentities(t *testing.T) {
	cases := []struct {
		ids        []string
		configured string
		want       bool
	}{
		{[]string{"panda-local/chat"}, "panda-local/chat", true},
		{[]string{"chat"}, "panda-local/chat", true},
		{[]string{" panda-local/chat "}, "panda-local/chat", true},
		{[]string{"panda-local/x"}, "panda-local/panda-local/x", true},
		{[]string{"other"}, "panda-local/chat", false},
		{[]string{"chat"}, "", false},
	}
	for _, test := range cases {
		if got := listed(test.ids, test.configured); got != test.want {
			t.Fatalf("listed(%q, %q) = %v, want %v", test.ids, test.configured, got, test.want)
		}
	}
}

// Image media selection handles a semicolon at byte zero and a two-byte JPEG
// magic prefix.
func TestImageMIMEEdgeBoundaries(t *testing.T) {
	jpeg := []byte{0xff, 0xd8}
	cases := []struct {
		media string
		body  []byte
		want  string
	}{
		{"image/png;charset=binary", jpeg, "image/png"},
		{"", jpeg, "image/jpeg"},
		{"application/octet-stream", []byte{0xff, 0xd8, 0xff}, "image/jpeg"},
		{"image/png", jpeg, "image/png"},
	}
	for _, test := range cases {
		if got := imageMIME(test.media, test.body); got != test.want {
			t.Fatalf("imageMIME(%q, %x) = %q, want %q", test.media, test.body, got, test.want)
		}
	}
}

func TestBlankKeepsConfiguredValues(t *testing.T) {
	if got := blank("chat-model"); got != "chat-model" {
		t.Fatalf("blank configured value = %q", got)
	}
	if got := blank("   "); got != "未配置" {
		t.Fatalf("blank whitespace value = %q", got)
	}
}
