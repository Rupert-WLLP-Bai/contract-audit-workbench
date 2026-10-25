package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSystemOneAdapterRejectsUnboundedEndpointsAndTimeouts(t *testing.T) {
	for _, endpoint := range []string{
		"https://127.0.0.1/v1",
		"http://example.com/v1",
		"http://0.0.0.0/v1",
		"http://127.0.0.1/v2",
		"http://user:secret@127.0.0.1/v1",
		"http://127.0.0.1/v1?next=http://example.com",
		"http://127.0.0.1:0/v1",
		"http://127.0.0.1:70000/v1",
	} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := NewSystemOneAdapter(endpoint, time.Second, ""); err == nil {
				t.Fatalf("accepted non-loopback or invalid endpoint %q", endpoint)
			}
		})
	}
	for _, endpoint := range []string{
		"http://127.0.0.1:3425/v1", "http://[::1]:3425/v1", "http://localhost:3425/v1",
	} {
		if _, err := NewSystemOneAdapter(endpoint, 0, ""); err != nil {
			t.Fatalf("rejected loopback endpoint %q: %v", endpoint, err)
		}
	}
	if _, err := NewSystemOneAdapter("http://127.0.0.1/v1", -time.Second, ""); err == nil {
		t.Fatal("accepted negative timeout")
	}
	if _, err := NewSystemOneAdapter("http://127.0.0.1/v1", 31*time.Second, ""); err == nil {
		t.Fatal("accepted timeout above hard maximum")
	}
	if _, err := NewSystemOneAdapter("http://127.0.0.1/v1", time.Second, "bad\r\nheader"); err == nil {
		t.Fatal("accepted bearer token with header injection")
	}
}

func TestSystemOneAdapterTimeoutAndSafeHTTPFailures(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}))
		defer server.Close()
		defer close(release)
		provider, err := NewProvider(Config{Mode: ModeShadow, Endpoint: server.URL + "/v1", Timeout: 25 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		result := provider.Evaluate(context.Background(), input)
		if result.Status != StatusUnavailable || result.Event.FailureReason != FailureTimeout || result.Event.LatencyMS <= 0 {
			t.Fatalf("timeout result = %+v", result)
		}
	})

	t.Run("HTTP status and bearer token redaction", func(t *testing.T) {
		const token = "private-server-key"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Errorf("authorization header = %q", r.Header.Get("Authorization"))
			}
			http.Error(w, "backend secret private-server-key", http.StatusBadGateway)
		}))
		defer server.Close()
		provider, err := NewProvider(Config{Mode: ModeShadow, Endpoint: server.URL + "/v1", BearerToken: token})
		if err != nil {
			t.Fatal(err)
		}
		result := provider.Evaluate(context.Background(), input)
		encoded, _ := json.Marshal(result)
		if result.Event.FailureReason != FailureHTTPStatus || bytes.Contains(encoded, []byte(token)) ||
			bytes.Contains(encoded, []byte("backend secret")) {
			t.Fatalf("upstream status leaked data: %s", encoded)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{broken"))
		}))
		defer server.Close()
		result := runShadowAt(t, server.URL+"/v1", input, 0)
		if result.Event.FailureReason != FailureInvalidJSON {
			t.Fatalf("invalid JSON result = %+v", result)
		}
	})

	t.Run("oversize response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(bytes.Repeat([]byte("x"), MaxResponseBytes+1))
		}))
		defer server.Close()
		result := runShadowAt(t, server.URL+"/v1", input, 0)
		if result.Event.FailureReason != FailureOversizeResponse {
			t.Fatalf("oversize result = %+v", result)
		}
	})

	t.Run("redirect is not followed", func(t *testing.T) {
		var externalCalls atomic.Int32
		external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			externalCalls.Add(1)
			_, _ = w.Write([]byte("{}"))
		}))
		defer external.Close()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, external.URL+"/v1/systemone", http.StatusFound)
		}))
		defer server.Close()
		result := runShadowAt(t, server.URL+"/v1", input, 0)
		if result.Event.FailureReason != FailureRedirect || externalCalls.Load() != 0 {
			t.Fatalf("redirect result=%+v external calls=%d", result, externalCalls.Load())
		}
	})
}

func TestSystemOneAdapterRejectsInvalidProbabilityAndAnswerMaps(t *testing.T) {
	baseInput := inputFixture(CapabilityChoice)
	valid := choiceAnswer(baseInput, "bandwidth", .8)
	cases := []struct {
		name   string
		answer map[string]any
		mutate func(map[string]any)
		want   string
	}{
		{name: "missing probability", answer: valid, mutate: func(a map[string]any) { delete(a, "probabilities") }, want: FailureInvalidProbabilities},
		{name: "extra probability key", answer: valid, mutate: func(a map[string]any) {
			p := cloneProbabilities(a["probabilities"].(map[string]float64))
			p["unapproved"] = 0
			a["probabilities"] = p
		}, want: FailureInvalidProbabilities},
		{name: "negative probability", answer: valid, mutate: func(a map[string]any) {
			p := cloneProbabilities(a["probabilities"].(map[string]float64))
			p["manual_review"] = -.1
			a["probabilities"] = p
		}, want: FailureInvalidProbabilities},
		{name: "non-unit sum", answer: valid, mutate: func(a map[string]any) {
			p := cloneProbabilities(a["probabilities"].(map[string]float64))
			p["manual_review"] += .2
			a["probabilities"] = p
		}, want: FailureInvalidProbabilities},
		{name: "wrong argmax", answer: valid, mutate: func(a map[string]any) { a["choice"] = "monthly_sla" }, want: FailureInvalidProbabilities},
		{name: "confidence does not match", answer: valid, mutate: func(a map[string]any) { a["answer_confidence"] = .2 }, want: FailureInvalidProbabilities},
		{name: "unknown candidate", answer: valid, mutate: func(a map[string]any) { a["choice"] = "foreign_rule" }, want: FailureInvalidAnswer},
		{name: "wrong type", answer: valid, mutate: func(a map[string]any) { a["type"] = "score" }, want: FailureInvalidAnswer},
		{name: "unknown abstention state", answer: valid, mutate: func(a map[string]any) { a["abstention"] = "maybe" }, want: FailureInvalidAnswer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			answer := cloneAnswer(tc.answer)
			tc.mutate(answer)
			server := responseServer(t, baseInput, answer, nil)
			defer server.Close()
			result := runShadowAt(t, server.URL+"/v1", baseInput, 0)
			if result.Event.FailureReason != tc.want || len(result.AcceptedRules) != 0 {
				t.Fatalf("failure=%q want=%q result=%+v", result.Event.FailureReason, tc.want, result)
			}
		})
	}
}

func TestSystemOneAdapterRejectsDuplicateKeysAndExtraAnswers(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	t.Run("duplicate probability key", func(t *testing.T) {
		body := `{"model":"laya-rl-agent","answers":{"review_focus":{"type":"choice","choice":"bandwidth","probabilities":{"bandwidth":0.8,"bandwidth":0.8,"monthly_sla":0.06666666666666667,"advance_payment":0.06666666666666667,"manual_review":0.06666666666666667}}},"routing":{"model":"multilingual"}}`
		server := rawResponseServer(body)
		defer server.Close()
		result := runShadowAt(t, server.URL+"/v1", input, 0)
		if result.Event.FailureReason != FailureInvalidJSON {
			t.Fatalf("duplicate key result = %+v", result)
		}
	})
	t.Run("extra answer", func(t *testing.T) {
		answer := choiceAnswer(input, "bandwidth", .8)
		body, err := json.Marshal(map[string]any{
			"model": "laya-rl-agent", "answers": map[string]any{
				"review_focus": answer, "other_question": answer,
			},
			"routing": map[string]any{"model": ModelAlias},
		})
		if err != nil {
			t.Fatal(err)
		}
		server := rawResponseServer(string(body))
		defer server.Close()
		result := runShadowAt(t, server.URL+"/v1", input, 0)
		if result.Event.FailureReason != FailureInvalidAnswer {
			t.Fatalf("extra answer result = %+v", result)
		}
	})
	t.Run("wrong routed checkpoint", func(t *testing.T) {
		body, err := json.Marshal(map[string]any{
			"model": "laya-rl-agent", "answers": map[string]any{"review_focus": choiceAnswer(input, "bandwidth", .8)},
			"routing": map[string]any{"model": "english"},
		})
		if err != nil {
			t.Fatal(err)
		}
		server := rawResponseServer(string(body))
		defer server.Close()
		result := runShadowAt(t, server.URL+"/v1", input, 0)
		if result.Event.FailureReason != FailureInvalidAnswer {
			t.Fatalf("wrong route result = %+v", result)
		}
	})
}

func TestSystemOneStrictResponseWithoutRoutingRemainsUnknown(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	body, err := json.Marshal(map[string]any{
		"model": "laya-rl-agent", "answers": map[string]any{"review_focus": choiceAnswer(input, "bandwidth", .8)},
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := rawResponseServer(string(body))
	defer server.Close()
	result := runShadowAt(t, server.URL+"/v1", input, 0)
	if result.Status != StatusSuggested || result.Event.RoutedCheckpoint != "" ||
		result.Event.ObservedModelRevision != nil {
		t.Fatalf("missing runtime route was invented: %+v", result.Event)
	}
}

func TestSystemOneUsesProbabilityMaximumWhenAnswerConfidenceAbsent(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	answer := choiceAnswer(input, "bandwidth", .8)
	delete(answer, "answer_confidence")
	server := responseServer(t, input, answer, nil)
	defer server.Close()
	result := runShadowAt(t, server.URL+"/v1", input, 0)
	if result.Status != StatusSuggested || result.Suggestion == nil || result.Suggestion.Confidence == nil ||
		*result.Suggestion.Confidence != .8 || result.Suggestion.ConfidenceSource != "probability_max" {
		t.Fatalf("confidence did not come from raw distribution: %+v", result)
	}
}

func TestUpstreamAbstentionIsPreserved(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	answer := choiceAnswer(input, "bandwidth", .9)
	answer["abstention"] = "abstained"
	answer["low_confidence"] = true
	server := responseServer(t, input, answer, nil)
	defer server.Close()
	result := runShadowAt(t, server.URL+"/v1", input, 0)
	if result.Status != StatusAbstained || result.Event.Abstention != "abstained" || result.Suggestion == nil {
		t.Fatalf("upstream abstention lost: %+v", result)
	}
}

func TestContextCancellationDoesNotLeakRawTransportError(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	provider, err := NewProvider(Config{Mode: ModeShadow, Endpoint: server.URL + "/v1", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := provider.Evaluate(ctx, input)
	if result.Status != StatusUnavailable || result.Event.FailureReason != FailureCanceled || result.Event.LatencyMS != 0 {
		t.Fatalf("cancellation result = %+v", result)
	}
}

func TestLoopbackDialerRefusesResolvedNonLoopback(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	dial := loopbackDialer()
	if _, err := dial(context.Background(), "tcp", strings.Replace(address, "127.0.0.1", "192.0.2.1", 1)); err == nil {
		t.Fatal("dialer accepted a non-loopback IP")
	}
}

func runShadowAt(t *testing.T, endpoint string, input Input, timeout time.Duration) Result {
	t.Helper()
	provider, err := NewProvider(Config{Mode: ModeShadow, Endpoint: endpoint, Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	return provider.Evaluate(context.Background(), input)
}

func rawResponseServer(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
}

func cloneAnswer(answer map[string]any) map[string]any {
	copy := make(map[string]any, len(answer))
	for key, value := range answer {
		if probabilities, ok := value.(map[string]float64); ok {
			copy[key] = cloneProbabilities(probabilities)
		} else {
			copy[key] = value
		}
	}
	return copy
}

func cloneProbabilities(probabilities map[string]float64) map[string]float64 {
	copy := make(map[string]float64, len(probabilities))
	for key, value := range probabilities {
		copy[key] = value
	}
	return copy
}

func TestSystemOneAdapterCancellationAndTruncatedResponseAreSafe(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	t.Run("caller cancellation after send", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			close(started)
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}))
		defer server.Close()
		defer close(release)
		adapter, err := NewSystemOneAdapter(server.URL+"/v1", time.Second, "")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		resultCh := make(chan AdapterResult, 1)
		go func() { resultCh <- adapter.Query(ctx, input) }()
		<-started
		cancel()
		result := <-resultCh
		if result.FailureReason != FailureCanceled || result.Latency <= 0 {
			t.Fatalf("canceled query result = %+v", result)
		}
	})

	t.Run("truncated response body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("HTTP server does not support hijacking")
				return
			}
			connection, buffered, err := hijacker.Hijack()
			if err != nil {
				t.Errorf("hijack failed: %v", err)
				return
			}
			_, _ = buffered.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 100\r\nConnection: close\r\n\r\n{}")
			_ = buffered.Flush()
			_ = connection.Close()
		}))
		defer server.Close()
		result := runShadowAt(t, server.URL+"/v1", input, 0)
		if result.Status != StatusUnavailable || result.Event.FailureReason != FailureTransport ||
			result.Event.LatencyMS <= 0 {
			t.Fatalf("truncated response result = %+v", result)
		}
	})
}

func TestLayaFourDecimalProbabilityRoundingIsBounded(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	probabilityCases := map[string]map[string]float64{
		"sum rounds below one": {
			"bandwidth": .7, "monthly_sla": .1, "advance_payment": .1, "manual_review": .0999,
		},
		"sum rounds above one": {
			"bandwidth": .7, "monthly_sla": .1001, "advance_payment": .1, "manual_review": .1,
		},
	}
	for name, probabilities := range probabilityCases {
		t.Run(name, func(t *testing.T) {
			answer := choiceAnswer(input, "bandwidth", .7)
			answer["probabilities"] = probabilities
			server := responseServer(t, input, answer, nil)
			defer server.Close()
			result := runShadowAt(t, server.URL+"/v1", input, 0)
			if result.Status != StatusSuggested || result.Suggestion == nil || result.Event.FailureReason != FailureNone {
				t.Fatalf("legitimately rounded distribution rejected: %+v", result)
			}
		})
	}
}
