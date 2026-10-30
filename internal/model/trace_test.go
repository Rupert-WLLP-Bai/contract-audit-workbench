package model

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestBuildModelCallTraceKeepsDigestsAndBoundsPayloads(t *testing.T) {
	long := strings.Repeat("审", traceMessageMaxBytes)
	response := strings.Repeat("界", traceResponseMaxBytes)
	payload := []byte(`{"model":"configured-model"}`)
	started := time.Date(2026, 10, 30, 1, 2, 3, 4, time.UTC)
	finished := started.Add(2 * time.Second)
	trace := buildModelCallTrace(callTraceInput{
		RequestedModel: "configured-model", ReportedModel: "observed-model",
		Payload: payload,
		Messages: []chatMessage{
			{Role: "system", Content: "只读审查规则"},
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": strings.Repeat("审", 100)},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,c2VjcmV0"}},
			}},
			{Role: "user", Content: long},
			{Role: "user", Content: long},
		},
		StartedAt: started, FinishedAt: finished,
		ResponseText: response, Status: "completed",
	})
	if trace == nil {
		t.Fatal("expected a trace")
	}
	requestSum := sha256.Sum256(payload)
	if trace.RequestSHA256 != hex.EncodeToString(requestSum[:]) || trace.RequestBytes != len(payload) {
		t.Fatalf("request identity was not retained: %+v", trace)
	}
	if trace.StartedAt != started.Format(time.RFC3339Nano) || trace.FinishedAt != finished.Format(time.RFC3339Nano) {
		t.Fatalf("call interval was not retained: %+v", trace)
	}
	if !trace.RequestTruncated || len(trace.RequestMessages) != 4 {
		t.Fatalf("bounded request messages were not recorded: %+v", trace.RequestMessages)
	}
	joined := ""
	for _, message := range trace.RequestMessages {
		joined += message.Content
	}
	if strings.Contains(joined, "c2VjcmV0") {
		t.Fatal("image payload was retained in the trace")
	}
	if !strings.Contains(joined, traceImagePlaceholder) {
		t.Fatal("image omission was not explicit")
	}
	responseSum := sha256.Sum256([]byte(response))
	if trace.ResponseSHA256 != hex.EncodeToString(responseSum[:]) || trace.ResponseBytes != len(response) {
		t.Fatalf("response identity was not retained: %+v", trace)
	}
	if !trace.ResponseTruncated || len(trace.ResponseText) > traceResponseMaxBytes || !utf8.ValidString(trace.ResponseText) {
		t.Fatalf("response preview was not bounded: %d bytes", len(trace.ResponseText))
	}
}

func TestTraceRequestMessagesStopsAtBudget(t *testing.T) {
	messages := []chatMessage{
		{Role: "user", Content: strings.Repeat("a", traceRequestMaxBytes/2)},
		{Role: "user", Content: strings.Repeat("b", traceRequestMaxBytes/2)},
		{Role: "user", Content: "c"},
	}
	out, truncated := traceRequestMessages(messages)
	if !truncated || len(out) != 3 {
		t.Fatalf("over-budget messages were not recorded: %+v", out)
	}
	if last := out[2]; last.Content != "" || !last.Truncated {
		t.Fatalf("over-budget message was not omitted explicitly: %+v", last)
	}
}

func TestBuildModelCallTraceRecordsFailedCallWithoutResponse(t *testing.T) {
	trace := buildModelCallTrace(callTraceInput{
		RequestedModel: "configured-model", Payload: []byte("{}"),
		StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(),
		Status: "failed", ErrorCode: "request_failed",
	})
	if trace.ResponseSHA256 != "" || trace.ResponseBytes != 0 || trace.ResponseText != "" || trace.ResponseTruncated {
		t.Fatalf("an absent response was invented: %+v", trace)
	}
	if trace.Status != "failed" || trace.ErrorCode != "request_failed" {
		t.Fatalf("failure identity was lost: %+v", trace)
	}
}

func TestTraceContentOmitsUnrepresentableAndImageParts(t *testing.T) {
	if content, truncated := traceContent(make(chan int)); content != "" || !truncated {
		t.Fatalf("unencodable content must be marked: %q %v", content, truncated)
	}
	content, truncated := traceContent([]any{
		make(chan int),
		map[string]any{"type": "image_url"},
		map[string]any{"type": "text", "text": "可读片段"},
		map[string]any{"note": "结构化"},
	})
	if !truncated || !strings.Contains(content, traceImagePlaceholder) || !strings.Contains(content, "可读片段") || !strings.Contains(content, "结构化") {
		t.Fatalf("mixed content was not recorded honestly: %q %v", content, truncated)
	}
	if content, truncated := traceContent(map[string]any{"note": "结构化"}); content != `{"note":"结构化"}` || truncated {
		t.Fatalf("structured content was not encoded: %q %v", content, truncated)
	}
}

func TestTruncateTraceStringKeepsWholeRunes(t *testing.T) {
	for _, limit := range []int{-1, 0} {
		if value, truncated := truncateTraceString("abc", limit); value != "" || !truncated {
			t.Fatalf("limit %d: %q %v", limit, value, truncated)
		}
	}
	if value, truncated := truncateTraceString("审", 2); value != "" || !truncated {
		t.Fatalf("a split rune escaped truncation: %q %v", value, truncated)
	}
	if value, truncated := truncateTraceString("abc", 3); value != "abc" || truncated {
		t.Fatalf("an in-budget value was truncated: %q %v", value, truncated)
	}
}
