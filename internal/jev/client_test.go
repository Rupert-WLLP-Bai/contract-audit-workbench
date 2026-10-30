package jev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testKey = "apikey_test_000000000000000000000000000000"

func testCriteria() map[string]string {
	return map[string]string{
		"bandwidth":       "专线承诺带宽",
		"monthly_sla":     "月度服务等级",
		"advance_payment": "预付款条件",
		"manual_review":   "人工复核",
	}
}

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, APIKey: testKey, Model: "jev-latest", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}

func answerBody(choice string, probabilities map[string]float64, confidence *float64) string {
	answer := map[string]any{"type": "choice", "choice": choice, "probabilities": probabilities}
	if confidence != nil {
		answer["confidence"] = *confidence
	}
	body := map[string]any{
		"model":   "jev-1.14.0",
		"answers": map[string]any{"review_focus": answer},
		"usage":   map[string]any{"input_tokens": 100, "output_tokens": 10},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func validProbabilities() map[string]float64 {
	return map[string]float64{
		"bandwidth":       0.91,
		"monthly_sla":     0.05,
		"advance_payment": 0.02,
		"manual_review":   0.02,
	}
}

func TestNewValidatesConfiguration(t *testing.T) {
	for _, test := range []struct {
		name   string
		config Config
	}{
		{name: "clear text remote endpoint", config: Config{BaseURL: "http://api.typesafe.ai", APIKey: testKey}},
		{name: "query string", config: Config{BaseURL: "https://api.typesafe.ai?next=1", APIKey: testKey}},
		{name: "credentials", config: Config{BaseURL: "https://user:pass@api.typesafe.ai", APIKey: testKey}},
		{name: "unsupported path", config: Config{BaseURL: "https://api.typesafe.ai/v2", APIKey: testKey}},
		{name: "unsupported scheme", config: Config{BaseURL: "ftp://api.typesafe.ai", APIKey: testKey}},
		{name: "missing key", config: Config{BaseURL: "https://api.typesafe.ai"}},
		{name: "short key", config: Config{BaseURL: "https://api.typesafe.ai", APIKey: "short"}},
		{name: "key with header injection", config: Config{BaseURL: "https://api.typesafe.ai", APIKey: testKey[:30] + "\r\n" + testKey}},
		{name: "invalid model", config: Config{BaseURL: "https://api.typesafe.ai", APIKey: testKey, Model: "bad model"}},
		{name: "negative timeout", config: Config{BaseURL: "https://api.typesafe.ai", APIKey: testKey, Timeout: -time.Second}},
		{name: "oversize timeout", config: Config{BaseURL: "https://api.typesafe.ai", APIKey: testKey, Timeout: MaxTimeout + time.Second}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.config); err == nil {
				t.Fatal("accepted an invalid configuration")
			}
		})
	}

	client, err := New(Config{APIKey: testKey})
	if err != nil {
		t.Fatalf("default endpoint was rejected: %v", err)
	}
	if client.endpoint != "https://api.typesafe.ai/v1/systemone" || client.Model() != DefaultModel {
		t.Fatalf("defaults = endpoint %q model %q", client.endpoint, client.Model())
	}
	for _, endpoint := range []string{"http://127.0.0.1:3425", "http://localhost:3425/v1", "http://[::1]:3425/v1"} {
		if _, err := New(Config{BaseURL: endpoint, APIKey: testKey}); err != nil {
			t.Fatalf("loopback endpoint %q was rejected: %v", endpoint, err)
		}
	}
	if _, err := New(Config{BaseURL: "https://example.com", APIKey: testKey}); err != nil {
		t.Fatalf("custom https endpoint was rejected: %v", err)
	}
}

func TestChooseSendsBoundedChoiceRequest(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			t.Errorf("authorization header = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		}
		var body struct {
			Model     string `json:"model"`
			State     string `json:"state"`
			Questions map[string]struct {
				Type         string            `json:"type"`
				Instructions string            `json:"instructions"`
				Criteria     map[string]string `json:"criteria"`
			} `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Model != "jev-latest" || body.State != "合成审核摘要" {
			t.Errorf("request identity = %+v", body)
		}
		question, ok := body.Questions["review_focus"]
		if !ok || question.Type != "choice" || question.Instructions == "" || len(question.Criteria) != 4 ||
			question.Criteria["bandwidth"] == "" {
			t.Errorf("request question = %+v", body.Questions)
		}
		_, _ = w.Write([]byte(answerBody("bandwidth", validProbabilities(), floatPointer(0.91))))
	})

	answer, err := client.Choose(context.Background(), "合成审核摘要", "review_focus", "选择核查重点", testCriteria())
	if err != nil {
		t.Fatalf("choose: %v", err)
	}
	if answer.Choice != "bandwidth" || answer.Confidence != 0.91 || answer.ConfidenceSource != "answer_confidence" ||
		answer.Model != "jev-1.14.0" || len(answer.Probabilities) != 4 ||
		answer.Usage.InputTokens == nil || *answer.Usage.InputTokens != 100 ||
		answer.Usage.OutputTokens == nil || *answer.Usage.OutputTokens != 10 {
		t.Fatalf("answer = %+v", answer)
	}
}

func TestChooseUsesMaximumProbabilityWithoutReportedConfidence(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(answerBody("bandwidth", validProbabilities(), nil)))
	})
	answer, err := client.Choose(context.Background(), "合成审核摘要", "review_focus", "选择核查重点", testCriteria())
	if err != nil {
		t.Fatalf("choose: %v", err)
	}
	if answer.Confidence != 0.91 || answer.ConfidenceSource != "probability_max" {
		t.Fatalf("confidence = %v from %q", answer.Confidence, answer.ConfidenceSource)
	}
}

func TestChooseRejectsInvalidInputBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(answerBody("bandwidth", validProbabilities(), nil)))
	})
	oversizedCriteria := make(map[string]string, MaxCriteria+1)
	for index := 0; index <= MaxCriteria; index++ {
		oversizedCriteria[fmt.Sprintf("c%02d", index)] = "候选"
	}
	for _, test := range []struct {
		name         string
		state        string
		questionID   string
		instructions string
		criteria     map[string]string
	}{
		{name: "empty state", state: "", questionID: "review_focus", instructions: "选择", criteria: testCriteria()},
		{name: "oversize state", state: strings.Repeat("x", MaxStateBytes+1), questionID: "review_focus", instructions: "选择", criteria: testCriteria()},
		{name: "invalid utf8 state", state: string([]byte{0xff}), questionID: "review_focus", instructions: "选择", criteria: testCriteria()},
		{name: "invalid question", state: "材料", questionID: "Review Focus", instructions: "选择", criteria: testCriteria()},
		{name: "empty instructions", state: "材料", questionID: "review_focus", instructions: "", criteria: testCriteria()},
		{name: "oversize instructions", state: "材料", questionID: "review_focus", instructions: strings.Repeat("x", MaxInstructionsBytes+1), criteria: testCriteria()},
		{name: "one criterion", state: "材料", questionID: "review_focus", instructions: "选择", criteria: map[string]string{"bandwidth": "带宽"}},
		{name: "too many criteria", state: "材料", questionID: "review_focus", instructions: "选择", criteria: oversizedCriteria},
		{name: "invalid criterion id", state: "材料", questionID: "review_focus", instructions: "选择", criteria: map[string]string{"Bandwidth": "带宽", "manual_review": "人工"}},
		{name: "empty description", state: "材料", questionID: "review_focus", instructions: "选择", criteria: map[string]string{"bandwidth": " ", "manual_review": "人工"}},
		{name: "oversize description", state: "材料", questionID: "review_focus", instructions: "选择", criteria: map[string]string{"bandwidth": strings.Repeat("x", MaxCandidateDescriptionSize+1), "manual_review": "人工"}},
		{name: "invalid utf8 description", state: "材料", questionID: "review_focus", instructions: "选择", criteria: map[string]string{"bandwidth": string([]byte{0xff}), "manual_review": "人工"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := client.Choose(context.Background(), test.state, test.questionID, test.instructions, test.criteria); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid input reached the network %d times", calls.Load())
	}
}

func TestChooseRejectsUnusableResponses(t *testing.T) {
	valid := answerBody("bandwidth", validProbabilities(), floatPointer(0.91))
	notMaximum := validProbabilities()
	notMaximum["monthly_sla"] = 0.9
	notMaximum["bandwidth"] = 0.06
	notMaximum["advance_payment"] = 0.02
	notMaximum["manual_review"] = 0.02
	badSum := map[string]float64{"bandwidth": 0.2, "monthly_sla": 0.1, "advance_payment": 0.1, "manual_review": 0.1}
	negative := map[string]float64{"bandwidth": -0.1, "monthly_sla": 0.5, "advance_payment": 0.3, "manual_review": 0.3}
	aboveOne := map[string]float64{"bandwidth": 1.1, "monthly_sla": -0.1, "advance_payment": 0.0, "manual_review": 0.0}
	incomplete := map[string]float64{"bandwidth": 0.91, "monthly_sla": 0.05, "advance_payment": 0.04}
	unexpectedKey := strings.Replace(valid, `"manual_review":0.02`, `"other":0.02`, 1)

	for _, test := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{name: "upstream status", status: http.StatusBadGateway, body: "{}", want: ErrHTTPStatus},
		{name: "redirect", status: http.StatusFound, body: "", want: ErrRedirect},
		{name: "oversize response", status: http.StatusOK, body: strings.Repeat("x", MaxResponseBytes+1), want: ErrOversizeResponse},
		{name: "malformed json", status: http.StatusOK, body: "{", want: ErrInvalidJSON},
		{name: "duplicate keys", status: http.StatusOK, body: `{"model":"jev-1.14.0","model":"jev-1.14.0","answers":{},"usage":{}}`, want: ErrInvalidJSON},
		{name: "duplicate key in nested array", status: http.StatusOK, body: `{"model":"jev-1.14.0","answers":{"review_focus":{"type":"choice","choice":"bandwidth","probabilities":{"bandwidth":0.91,"monthly_sla":0.05,"advance_payment":0.02,"manual_review":0.02}}},"usage":{},"extra":[{"key":1,"key":2}]}`, want: ErrInvalidJSON},
		{name: "trailing content", status: http.StatusOK, body: valid + `{}`, want: ErrInvalidJSON},
		{name: "missing answers", status: http.StatusOK, body: `{"model":"jev-1.14.0","answers":{},"usage":{}}`, want: ErrInvalidAnswer},
		{name: "multiple answers", status: http.StatusOK, body: `{"model":"jev-1.14.0","answers":{"review_focus":{"type":"choice","choice":"bandwidth","probabilities":{"bandwidth":1,"monthly_sla":0,"advance_payment":0,"manual_review":0}},"other":{"type":"choice","choice":"bandwidth"}},"usage":{}}`, want: ErrInvalidAnswer},
		{name: "unknown choice", status: http.StatusOK, body: strings.Replace(valid, `"choice":"bandwidth"`, `"choice":"other"`, 1), want: ErrInvalidAnswer},
		{name: "wrong answer type", status: http.StatusOK, body: strings.Replace(valid, `"type":"choice"`, `"type":"score"`, 1), want: ErrInvalidAnswer},
		{name: "invalid observed model", status: http.StatusOK, body: strings.Replace(valid, `"model":"jev-1.14.0"`, `"model":"bad model"`, 1), want: ErrInvalidAnswer},
		{name: "confidence above one", status: http.StatusOK, body: answerBody("bandwidth", validProbabilities(), floatPointer(1.1)), want: ErrInvalidAnswer},
		{name: "negative usage", status: http.StatusOK, body: strings.Replace(valid, `"input_tokens":100`, `"input_tokens":-1`, 1), want: ErrInvalidAnswer},
		{name: "incomplete probabilities", status: http.StatusOK, body: answerBody("bandwidth", incomplete, nil), want: ErrInvalidProbabilities},
		{name: "probability sum", status: http.StatusOK, body: answerBody("bandwidth", badSum, nil), want: ErrInvalidProbabilities},
		{name: "choice is not maximum", status: http.StatusOK, body: answerBody("bandwidth", notMaximum, nil), want: ErrInvalidProbabilities},
		{name: "negative probability", status: http.StatusOK, body: answerBody("bandwidth", negative, nil), want: ErrInvalidProbabilities},
		{name: "probability above one", status: http.StatusOK, body: answerBody("bandwidth", aboveOne, nil), want: ErrInvalidProbabilities},
		{name: "unexpected probability key", status: http.StatusOK, body: unexpectedKey, want: ErrInvalidProbabilities},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			})
			answer, err := client.Choose(context.Background(), "合成审核摘要", "review_focus", "选择核查重点", testCriteria())
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if answer.Choice != "" || len(answer.Probabilities) != 0 {
				t.Fatalf("failed response produced an answer: %+v", answer)
			}
		})
	}
}

func TestChooseMapsTimeoutAndCancellation(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		defer close(release)
		client := newTestClient(t, func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-release:
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := client.Choose(ctx, "合成审核摘要", "review_focus", "选择核查重点", testCriteria())
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want deadline exceeded", err)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		defer close(release)
		client := newTestClient(t, func(_ http.ResponseWriter, r *http.Request) {
			close(started)
			select {
			case <-r.Context().Done():
			case <-release:
			}
		})
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, err := client.Choose(ctx, "合成审核摘要", "review_focus", "选择核查重点", testCriteria())
			result <- err
		}()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("request did not reach the test server")
		}
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want canceled", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("canceled request did not return")
		}
	})
}

func TestChooseDoesNotLeakKeyInErrors(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream echoed " + testKey))
	})
	_, err := client.Choose(context.Background(), "合成审核摘要", "review_focus", "选择核查重点", testCriteria())
	if err == nil || strings.Contains(err.Error(), testKey) {
		t.Fatalf("error leaked the key: %v", err)
	}
}

func floatPointer(value float64) *float64 { return &value }

func testNoulQuestions() map[string]NoulQuestion {
	return map[string]NoulQuestion{
		"candidate_0_relevance": {
			Instructions: "该制度是否直接规范所述能力？",
			Criteria:     NoulCriteria{True: "制度直接规范该能力", False: "制度只涉及其他能力"},
		},
		"candidate_1_relevance": {
			Instructions: "该制度是否直接规范所述能力？",
			Criteria:     NoulCriteria{True: "制度直接规范该能力", False: "制度只涉及其他能力"},
		},
	}
}

func noulBody(values map[string]float64) string {
	answers := make(map[string]any, len(values))
	for id, value := range values {
		answers[id] = map[string]any{"type": "noul", "noul": value}
	}
	body := map[string]any{
		"model":   "jev-1.14.0",
		"answers": answers,
		"usage":   map[string]any{"input_tokens": 120, "output_tokens": 8},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func TestAskNoulsSendsBatchedQuestions(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			t.Errorf("authorization header = %q", r.Header.Get("Authorization"))
		}
		var body struct {
			Model     string `json:"model"`
			State     string `json:"state"`
			Questions map[string]struct {
				Type         string            `json:"type"`
				Instructions string            `json:"instructions"`
				Criteria     map[string]string `json:"criteria"`
			} `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Model != "jev-latest" || body.State != "合成审核摘要" || len(body.Questions) != 2 {
			t.Errorf("request identity = %+v", body)
		}
		question, ok := body.Questions["candidate_0_relevance"]
		if !ok || question.Type != "noul" || question.Instructions == "" ||
			question.Criteria["true"] == "" || question.Criteria["false"] == "" {
			t.Errorf("request question = %+v", body.Questions)
		}
		_, _ = w.Write([]byte(noulBody(map[string]float64{
			"candidate_0_relevance": 0.82,
			"candidate_1_relevance": 0.12,
		})))
	})

	answers, meta, err := client.AskNouls(context.Background(), "合成审核摘要", testNoulQuestions())
	if err != nil {
		t.Fatalf("ask nouls: %v", err)
	}
	if meta.Model != "jev-1.14.0" || meta.Usage.InputTokens == nil || *meta.Usage.InputTokens != 120 ||
		meta.Usage.OutputTokens == nil || *meta.Usage.OutputTokens != 8 {
		t.Fatalf("meta = %+v", meta)
	}
	if len(answers) != 2 || answers["candidate_0_relevance"].Noul != 0.82 ||
		answers["candidate_1_relevance"].Noul != 0.12 ||
		answers["candidate_0_relevance"].Model != "jev-1.14.0" {
		t.Fatalf("answers = %+v", answers)
	}
}

func TestAskNoulsRejectsInvalidInputBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(noulBody(map[string]float64{"candidate_0_relevance": 0.5})))
	})
	oversized := make(map[string]NoulQuestion, MaxQuestions+1)
	for index := 0; index <= MaxQuestions; index++ {
		oversized[fmt.Sprintf("candidate_%d_relevance", index)] = NoulQuestion{
			Instructions: "是否相关？",
			Criteria:     NoulCriteria{True: "相关", False: "不相关"},
		}
	}
	base := NoulQuestion{Instructions: "是否相关？", Criteria: NoulCriteria{True: "相关", False: "不相关"}}
	for _, test := range []struct {
		name      string
		state     string
		questions map[string]NoulQuestion
	}{
		{name: "empty state", state: "", questions: testNoulQuestions()},
		{name: "oversize state", state: strings.Repeat("x", MaxStateBytes+1), questions: testNoulQuestions()},
		{name: "invalid utf8 state", state: string([]byte{0xff}), questions: testNoulQuestions()},
		{name: "no questions", state: "材料", questions: nil},
		{name: "too many questions", state: "材料", questions: oversized},
		{name: "invalid question id", state: "材料", questions: map[string]NoulQuestion{"Candidate 0": base}},
		{name: "empty instructions", state: "材料", questions: map[string]NoulQuestion{"candidate_0_relevance": {Criteria: base.Criteria}}},
		{name: "oversize instructions", state: "材料", questions: map[string]NoulQuestion{"candidate_0_relevance": {Instructions: strings.Repeat("x", MaxInstructionsBytes+1), Criteria: base.Criteria}}},
		{name: "empty true criterion", state: "材料", questions: map[string]NoulQuestion{"candidate_0_relevance": {Instructions: "是否相关？", Criteria: NoulCriteria{True: " ", False: "不相关"}}}},
		{name: "empty false criterion", state: "材料", questions: map[string]NoulQuestion{"candidate_0_relevance": {Instructions: "是否相关？", Criteria: NoulCriteria{True: "相关"}}}},
		{name: "oversize criterion", state: "材料", questions: map[string]NoulQuestion{"candidate_0_relevance": {Instructions: "是否相关？", Criteria: NoulCriteria{True: strings.Repeat("x", MaxCandidateDescriptionSize+1), False: "不相关"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := client.AskNouls(context.Background(), test.state, test.questions); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid input reached the network %d times", calls.Load())
	}
}

func TestAskNoulsRejectsUnusableResponses(t *testing.T) {
	validValues := map[string]float64{"candidate_0_relevance": 0.82, "candidate_1_relevance": 0.12}
	valid := noulBody(validValues)
	for _, test := range []struct {
		name string
		body string
		want error
	}{
		{name: "malformed json", body: "{", want: ErrInvalidJSON},
		{name: "duplicate keys", body: `{"model":"jev-1.14.0","model":"jev-1.14.0","answers":{},"usage":{}}`, want: ErrInvalidJSON},
		{name: "trailing content", body: valid + `{}`, want: ErrInvalidJSON},
		{name: "missing answer", body: noulBody(map[string]float64{"candidate_0_relevance": 0.82}), want: ErrInvalidAnswer},
		{name: "extra answer", body: noulBody(map[string]float64{"candidate_0_relevance": 0.82, "candidate_1_relevance": 0.12, "candidate_2_relevance": 0.5}), want: ErrInvalidAnswer},
		{name: "wrong answer id", body: noulBody(map[string]float64{"candidate_0_relevance": 0.82, "other": 0.12}), want: ErrInvalidAnswer},
		{name: "wrong answer type", body: strings.Replace(valid, `"type":"noul"`, `"type":"choice"`, 1), want: ErrInvalidAnswer},
		{name: "missing probability", body: strings.Replace(valid, `"noul":0.82,`, "", 1), want: ErrInvalidAnswer},
		{name: "invalid observed model", body: strings.Replace(valid, `"model":"jev-1.14.0"`, `"model":"bad model"`, 1), want: ErrInvalidAnswer},
		{name: "negative usage", body: strings.Replace(valid, `"input_tokens":120`, `"input_tokens":-1`, 1), want: ErrInvalidAnswer},
		{name: "negative probability", body: noulBody(map[string]float64{"candidate_0_relevance": -0.01, "candidate_1_relevance": 1.01}), want: ErrInvalidProbabilities},
		{name: "probability above one", body: noulBody(map[string]float64{"candidate_0_relevance": 1.01, "candidate_1_relevance": 0.12}), want: ErrInvalidProbabilities},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			})
			answers, meta, err := client.AskNouls(context.Background(), "合成审核摘要", testNoulQuestions())
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if len(answers) != 0 || meta.Model != "" {
				t.Fatalf("failed response produced answers: %+v meta %+v", answers, meta)
			}
		})
	}
}
