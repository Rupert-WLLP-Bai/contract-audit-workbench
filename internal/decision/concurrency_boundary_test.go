package decision

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDecisionDeadlineBeforeSendKeepsNetworkUnused(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	var calls atomic.Int32
	server := responseServer(t, input, choiceAnswer(input, "bandwidth", .8), &calls)
	defer server.Close()
	provider, err := NewProvider(Config{Mode: ModeShadow, Endpoint: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancel()
	result := provider.Evaluate(ctx, input)
	if result.Status != StatusUnavailable || result.Event.FailureReason != FailureTimeout || calls.Load() != 0 {
		t.Fatalf("expired deadline sent a request or lost timeout identity: %+v calls=%d", result, calls.Load())
	}
}

func TestDecisionResponseBodyHonorsDeadlineAndCancellation(t *testing.T) {
	for _, cancellation := range []bool{false, true} {
		name := "body-timeout"
		if cancellation {
			name = "body-canceled"
		}
		t.Run(name, func(t *testing.T) {
			headers := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				close(headers)
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			defer close(release)
			provider, err := NewProvider(Config{Mode: ModeShadow, Endpoint: server.URL + "/v1", Timeout: 100 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			results := make(chan Result, 1)
			go func() { results <- provider.Evaluate(ctx, inputFixture(CapabilityChoice)) }()
			<-headers
			want := FailureTimeout
			if cancellation {
				cancel()
				want = FailureCanceled
			}
			result := <-results
			if result.Status != StatusUnavailable || result.Event.FailureReason != want || result.Suggestion != nil || result.Event.LatencyMS <= 0 {
				t.Fatalf("incomplete response body produced a result: %+v want=%s", result, want)
			}
		})
	}
}

func TestDecisionConcurrentRequestsKeepCorrelationIsolated(t *testing.T) {
	base := inputFixture(CapabilityChoice)
	var calls atomic.Int32
	server := responseServer(t, base, choiceAnswer(base, "bandwidth", .8), &calls)
	defer server.Close()
	provider, err := NewProvider(Config{Mode: ModeShadow, Endpoint: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	const count = 12
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := range count {
		group.Add(1)
		go func() {
			defer group.Done()
			input := inputFixture(CapabilityChoice)
			input.RequestID = fmt.Sprintf("request_%d", index)
			input.DecisionID = fmt.Sprintf("decision_%d", index)
			input.RunID = fmt.Sprintf("run_%d", index)
			input.AttemptID = fmt.Sprintf("attempt_%d", index)
			input.TraceID = fmt.Sprintf("trace_%d", index)
			<-start
			result := provider.Evaluate(nil, input)
			if result.Status != StatusSuggested || result.Event.RequestID != input.RequestID ||
				result.Event.DecisionID != input.DecisionID || result.Event.RunID != input.RunID ||
				result.Event.AttemptID != input.AttemptID || result.Event.TraceID != input.TraceID || len(result.AcceptedRules) != 0 {
				t.Errorf("parallel request correlation changed: input=%s event=%+v", input.RequestID, result.Event)
			}
		}()
	}
	close(start)
	group.Wait()
	if calls.Load() != count {
		t.Fatalf("parallel requests sent %d calls, want %d", calls.Load(), count)
	}
}

func TestDecisionUnreachableLocalhostHasSafeFailure(t *testing.T) {
	input := inputFixture(CapabilityChoice)
	server := responseServer(t, input, choiceAnswer(input, "bandwidth", .8), nil)
	endpoint := strings.Replace(server.URL, "127.0.0.1", "localhost", 1) + "/v1"
	server.Close()
	result := runShadowAt(t, endpoint, input, 0)
	if result.Status != StatusUnavailable || result.Event.FailureReason != FailureTransport || result.Suggestion != nil {
		t.Fatalf("unreachable localhost returned a usable suggestion: %+v", result)
	}
}
