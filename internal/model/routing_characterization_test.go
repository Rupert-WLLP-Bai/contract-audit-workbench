package model

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// RouteChoice is bounded to the caller's criteria. Every malformed input is
// refused before the provider is contacted, and every malformed answer is
// refused instead of becoming a route.
func TestRouteChoiceRejectsInvalidInputBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeJSONResponse(t, w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": `{"choice":"bandwidth"}`}}},
		})
	}))
	defer server.Close()
	client := New(Config{BaseURL: server.URL, ChatModel: "route-model"})
	criteria := map[string]string{"bandwidth": "带宽证据", "manual_review": "人工明确"}

	single := map[string]string{"bandwidth": "带宽证据"}
	tooMany := make(map[string]string, 65)
	for i := 0; i < 65; i++ {
		tooMany[strings.Repeat("k", 1)+string(rune('a'+i%26))+strings.Repeat("x", i)] = "描述"
	}
	tests := []struct {
		name         string
		state        string
		criteria     map[string]string
		instructions string
	}{
		{"nil criteria", "待审", nil, "选择重点"},
		{"single criterion", "待审", single, "选择重点"},
		{"too many criteria", "待审", tooMany, "选择重点"},
		{"empty instructions", "待审", criteria, ""},
		{"oversized instructions", "待审", criteria, strings.Repeat("x", 2049)},
		{"invalid utf8 state", string([]byte{0xff, 0xfe}), criteria, "选择重点"},
		{"empty criterion id", "待审", map[string]string{"": "描述", "manual_review": "人工"}, "选择重点"},
		{"oversized criterion id", "待审", map[string]string{strings.Repeat("k", 81): "描述", "manual_review": "人工"}, "选择重点"},
		{"empty criterion description", "待审", map[string]string{"bandwidth": "", "manual_review": "人工"}, "选择重点"},
		{"oversized criterion description", "待审", map[string]string{"bandwidth": strings.Repeat("x", 513), "manual_review": "人工"}, "选择重点"},
		{"invalid utf8 criterion id", "待审", map[string]string{string([]byte{0xff}): "描述", "manual_review": "人工"}, "选择重点"},
		{"invalid utf8 criterion description", "待审", map[string]string{"bandwidth": string([]byte{0xff}), "manual_review": "人工"}, "选择重点"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := client.RouteChoice(context.Background(), test.state, test.criteria, test.instructions); !errors.Is(err, ErrUnusable) {
				t.Fatalf("invalid routing input accepted: %v", err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid routing input reached the provider: %d calls", calls.Load())
	}
}

func TestRouteChoiceRefusesAnswersOutsideTheFrozenChoiceShape(t *testing.T) {
	criteria := map[string]string{"bandwidth": "带宽证据", "manual_review": "人工明确"}
	for _, answer := range []string{
		``, `{}`, `{"choice":""}`, `{"choice":1}`, `["choice"]`,
		`{"choice":"bandwidth","nested":{"a":1}}`, `{"choice":"bandwidth"`,
		`{"choice":"bandwidth"}x`, `{"choice":"bandwidth"} `,
		` {"choice":"bandwidth"}`, `{"choice":"bandwidth","choice":"manual_review"}`,
		`{"choice":"bandwidth","extra":true}`, `{"choice":"bandwidth","x":null}`,
	} {
		t.Run(answer, func(t *testing.T) {
			server := routingResponseServer(t, answer, nil)
			defer server.Close()
			result, err := New(Config{BaseURL: server.URL, ChatModel: "route-model"}).RouteChoice(
				context.Background(), "待核对材料", criteria, "选择重点")
			if answer == ` {"choice":"bandwidth"}` {
				if err != nil || result.Choice != "bandwidth" {
					t.Fatalf("leading whitespace answer rejected: %+v err=%v", result, err)
				}
				return
			}
			if answer == `{"choice":"bandwidth"} ` {
				if err != nil || result.Choice != "bandwidth" {
					t.Fatalf("trailing whitespace answer rejected: %+v err=%v", result, err)
				}
				return
			}
			if !errors.Is(err, ErrUnusable) || result.Choice != "" {
				t.Fatalf("untrusted answer became a route: %+v err=%v", result, err)
			}
		})
	}
}

func TestRouteChoiceRejectsKnownShapeWithUnknownChoice(t *testing.T) {
	server := routingResponseServer(t, `{"choice":"skip_rules"}`, nil)
	defer server.Close()
	result, err := New(Config{BaseURL: server.URL, ChatModel: "route-model"}).RouteChoice(
		context.Background(), "待核对材料", map[string]string{"bandwidth": "带宽", "manual_review": "人工"}, "选择重点")
	if !errors.Is(err, ErrUnusable) || result.Choice != "" {
		t.Fatalf("choice outside the caller criteria became a route: %+v err=%v", result, err)
	}
}

func TestRouteChoiceMarshalsQuestionAndBoundedOptions(t *testing.T) {
	var observed map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = readRequestJSON(t, r)
		writeJSONResponse(t, w, http.StatusOK, map[string]any{
			"model": "observed-model",
			"choices": []any{map[string]any{
				"finish_reason": "stop", "message": map[string]any{"content": `{"choice":"manual_review"}`},
			}},
		})
	}))
	defer server.Close()
	result, err := New(Config{BaseURL: server.URL, ChatModel: "route-model"}).RouteChoice(
		context.Background(), "待核对材料", map[string]string{"bandwidth": "带宽证据", "manual_review": "人工明确"}, "选择核查重点")
	if err != nil || result.Choice != "manual_review" || result.ObservedModel != "observed-model" {
		t.Fatalf("valid route lost: %+v err=%v", result, err)
	}
	if observed["model"] != "route-model" || observed["max_tokens"] != float64(256) || observed["reasoning_effort"] != "low" {
		t.Fatalf("routing request identity %v", observed)
	}
	messages := observed["messages"].([]any)
	content, _ := messages[0].(map[string]any)["content"].(string)
	if !strings.Contains(content, `"review_focus"`) || !strings.Contains(content, `"choice"`) || !strings.Contains(content, "仅输出一个JSON对象") {
		t.Fatalf("routing question %q", content)
	}
}
