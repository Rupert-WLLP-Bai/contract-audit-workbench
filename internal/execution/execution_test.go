package execution_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/execution"
	"github.com/cloudwego/eino/compose"
)

type invocationKey struct{}

type recorded struct {
	invocation string
	event      execution.Event
}

type eventRecorder struct {
	mu       sync.Mutex
	rows     []recorded
	failType string
	failure  error
}

func (r *eventRecorder) RecordExecutionEvent(ctx context.Context, event execution.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if event.EventType == r.failType {
		return r.failure
	}
	invocation, _ := ctx.Value(invocationKey{}).(string)
	r.rows = append(r.rows, recorded{invocation: invocation, event: event})
	return nil
}

func TestConcurrentEinoBranchesKeepInvocationAndParentIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	recorder := &eventRecorder{}
	handler := execution.NewCallbacks(recorder)
	graph := compose.NewGraph[map[string]string, map[string]string]()
	var entered atomic.Int32
	release := make(chan struct{})
	for _, branch := range []string{"left", "right"} {
		if err := graph.AddLambdaNode(branch, compose.InvokableLambda(func(ctx context.Context, _ map[string]string) (map[string]string, error) {
			if entered.Add(1) == 4 {
				close(release)
			}
			select {
			case <-release:
				return map[string]string{branch: "complete"}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}), compose.WithNodeName("same_component_name")); err != nil {
			t.Fatal(err)
		}
		if err := graph.AddEdge(compose.START, branch); err != nil {
			t.Fatal(err)
		}
		if err := graph.AddEdge(branch, compose.END); err != nil {
			t.Fatal(err)
		}
	}
	runnable, err := graph.Compile(ctx, compose.WithGraphName("parallel_audit"))
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, id := range []string{"attempt-a", "attempt-b"} {
		go func() {
			result, err := runnable.Invoke(context.WithValue(ctx, invocationKey{}, id), map[string]string{}, compose.WithCallbacks(handler.Handler()))
			if err == nil && (result["left"] != "complete" || result["right"] != "complete") {
				err = errors.New("parallel audit branch did not complete")
			}
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if err := handler.Err(); err != nil {
		t.Fatal(err)
	}

	roots := map[string]string{}
	starts := map[string]recorded{}
	ends := map[string]recorded{}
	for _, row := range recorder.rows {
		if row.invocation != "attempt-a" && row.invocation != "attempt-b" {
			t.Fatalf("lost invocation: %+v", row)
		}
		if row.event.EventType == "span_started" {
			if _, duplicate := starts[row.event.SpanID]; duplicate {
				t.Fatalf("shared span identity: %+v", row)
			}
			starts[row.event.SpanID] = row
			if row.event.ParentSpanID == "" {
				if roots[row.invocation] != "" {
					t.Fatalf("multiple roots: %+v", row)
				}
				roots[row.invocation] = row.event.SpanID
			}
		} else {
			if _, duplicate := ends[row.event.SpanID]; duplicate {
				t.Fatalf("duplicate terminal event: %+v", row)
			}
			ends[row.event.SpanID] = row
		}
	}
	if len(roots) != 2 || len(starts) != 6 || len(ends) != 6 {
		t.Fatalf("incomplete parallel trace: roots=%v starts=%d ends=%d", roots, len(starts), len(ends))
	}
	for span, start := range starts {
		end, found := ends[span]
		if !found || end.invocation != start.invocation || end.event.ParentSpanID != start.event.ParentSpanID || end.event.DurationMS == nil || *end.event.DurationMS < 0 || end.event.At.Before(start.event.At) || end.event.Status != "completed" {
			t.Fatalf("span terminal mismatch: start=%+v end=%+v", start, end)
		}
		if start.event.ParentSpanID != "" && (start.event.ParentSpanID != roots[start.invocation] || start.event.Phase != "same_component_name") {
			t.Fatalf("parallel callback attached to a foreign parent: %+v roots=%v", start, roots)
		}
	}
}

func TestBusinessRecorderFailureCannotProduceSuccessfulExecution(t *testing.T) {
	for _, eventType := range []string{"span_started", "node_completed"} {
		t.Run(eventType, func(t *testing.T) {
			failure := errors.New("business audit unavailable")
			recorder := &eventRecorder{failType: eventType, failure: failure}
			var calls int
			err := execution.Run(context.Background(), recorder, execution.Stage{Name: "verified_rules", Run: func(context.Context) error { calls++; return nil }})
			if !errors.Is(err, failure) {
				t.Fatalf("audit failure became success: %v", err)
			}
			if eventType == "span_started" && calls != 0 {
				t.Fatal("stage ran after its start could not be audited")
			}
			if eventType == "node_completed" && calls != 1 {
				t.Fatalf("completion failure did not exercise the real stage: %d", calls)
			}
		})
	}
}

func TestEinoCancellationDoesNotRunFollowingStage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recorder := &eventRecorder{}
	var following bool
	err := execution.Run(ctx, recorder,
		execution.Stage{Name: "cancelled_model", Run: func(ctx context.Context) error { cancel(); return ctx.Err() }},
		execution.Stage{Name: "publish_result", Run: func(context.Context) error { following = true; return nil }},
	)
	if !errors.Is(err, context.Canceled) || following {
		t.Fatalf("cancelled audit published a result: %v following=%v", err, following)
	}
	for _, row := range recorder.rows {
		if row.event.Phase == "cancelled_model" && row.event.Status == "completed" {
			t.Fatalf("cancelled model reported completion: %+v", row)
		}
	}
}

func TestConcurrentSpanFinishIsRecordedOnce(t *testing.T) {
	recorder := &eventRecorder{}
	ctx := execution.WithSpan(context.Background(), "attempt-root")
	_, finish, err := execution.StartSpan(ctx, recorder, "model_call", "physical_call", "synthetic-model")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if err := finish(execution.SpanCompletion{EventType: "model_failed", Status: "failed", ErrorCode: "cancelled", ModelRef: "observed-model"}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(recorder.rows) != 2 {
		t.Fatalf("one invocation was charged multiple completions: %+v", recorder.rows)
	}
	start, terminal := recorder.rows[0].event, recorder.rows[1].event
	if start.EventType != "span_started" || terminal.EventType != "model_failed" || terminal.SpanID != start.SpanID || terminal.ParentSpanID != "attempt-root" || terminal.Usage != nil || terminal.ModelRef != "observed-model" {
		t.Fatalf("invocation identity or unknown usage changed: %+v", recorder.rows)
	}
}
