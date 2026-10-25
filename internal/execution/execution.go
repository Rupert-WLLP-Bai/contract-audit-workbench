package execution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/compose"
)

// Usage contains only counters observed from a provider response.
type Usage struct {
	Source       string
	InputTokens  *int64
	OutputTokens *int64
	TotalTokens  *int64
}

// Event is one execution observation. Recorder assigns durable attempt and sequence identities.
type Event struct {
	SpanID       string
	ParentSpanID string
	NodeKind     string
	Phase        string
	EventType    string
	At           time.Time
	DurationMS   *int64
	Status       string
	ErrorCode    string
	ModelRef     string
	Usage        *Usage
}

// Recorder persists business events. Errors are retained and returned by Run.
type Recorder interface {
	RecordExecutionEvent(context.Context, Event) error
}

type Stage struct {
	Name string
	Run  func(context.Context) error
}

// Run executes local Eino callback instrumentation for this invocation only.
// Stages are named by the caller and are invoked sequentially in declaration order.
func Run(ctx context.Context, recorder Recorder, stages ...Stage) error {
	if len(stages) == 0 {
		return nil
	}
	handlers := NewCallbacks(recorder)
	chain := compose.NewChain[struct{}, struct{}]()
	for _, item := range stages {
		stage := item
		if stage.Name == "" || stage.Run == nil {
			return errors.New("invalid execution stage")
		}
		chain.AppendLambda(compose.InvokableLambda(func(ctx context.Context, _ struct{}) (struct{}, error) {
			if err := ctx.Err(); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, stage.Run(ctx)
		}), compose.WithNodeName(stage.Name))
	}
	runnable, err := chain.Compile(ctx, compose.WithGraphName("audit_review_execution_v1"), compose.WithMaxRunSteps(len(stages)+1))
	if err != nil {
		return err
	}
	_, invokeErr := runnable.Invoke(ctx, struct{}{}, compose.WithCallbacks(handlers.Handler()))
	return errors.Join(invokeErr, handlers.Err())
}

type callbackState struct {
	finish func(string, string, string, *Usage, string) error
}

type spanContextKey struct{}
type callbackStateContextKey struct{}

// WithSpan binds a span to the current invocation context for child callbacks.
func WithSpan(ctx context.Context, spanID string) context.Context {
	return context.WithValue(ctx, spanContextKey{}, spanID)
}

func ParentSpanID(ctx context.Context) string {
	spanID, _ := ctx.Value(spanContextKey{}).(string)
	return spanID
}

// StartSpan records a span start and returns its invocation-local child context and finisher.
func StartSpan(ctx context.Context, recorder Recorder, nodeKind, phase, modelRef string) (context.Context, func(string, string, string, *Usage, string) error, error) {
	spanID, err := newSpanID()
	if err != nil {
		return ctx, nil, err
	}
	started := time.Now()
	parentID := ParentSpanID(ctx)
	child, cancel := context.WithCancel(ctx)
	child = WithSpan(child, spanID)
	if recorder != nil {
		if err := recorder.RecordExecutionEvent(child, Event{
			SpanID: spanID, ParentSpanID: parentID, NodeKind: nodeKind,
			Phase: phase, EventType: "span_started", At: started.UTC(), Status: "running", ModelRef: modelRef,
		}); err != nil {
			cancel()
			return child, nil, err
		}
	}
	var once sync.Once
	var finishErr error
	finish := func(eventType, status, errorCode string, usage *Usage, reportedModelRef string) error {
		once.Do(func() {
			defer cancel()
			at := time.Now()
			elapsed := at.Sub(started).Milliseconds()
			if recorder != nil {
				finishErr = recorder.RecordExecutionEvent(child, Event{
					SpanID: spanID, ParentSpanID: parentID, NodeKind: nodeKind,
					Phase: phase, EventType: eventType, At: at.UTC(), DurationMS: &elapsed,
					Status: status, ErrorCode: errorCode, ModelRef: reportedModelRef, Usage: usage,
				})
			}
		})
		return finishErr
	}
	return child, finish, nil
}

// Callbacks owns one Eino handler and its invocation-local persistence error.
type Callbacks struct {
	recorder Recorder
	mu       sync.Mutex
	err      error
	handler  callbacks.Handler
}

func NewCallbacks(recorder Recorder) *Callbacks {
	c := &Callbacks{recorder: recorder}
	builder := callbacks.NewHandlerBuilder()
	builder.OnStartFn(c.onStart).OnEndFn(c.onEnd).OnErrorFn(c.onError)
	c.handler = builder.Build()
	return c
}

func (c *Callbacks) Handler() callbacks.Handler { return c.handler }

func (c *Callbacks) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Callbacks) remember(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.mu.Unlock()
}

func (c *Callbacks) onStart(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
	if c.recorder == nil {
		return ctx
	}
	name, kind := "eino_component", "eino"
	if info != nil {
		if info.Name != "" {
			name = info.Name
		}
		if info.Component != "" {
			kind = string(info.Component)
		}
	}
	child, finish, err := StartSpan(ctx, c.recorder, kind, name, "")
	if err != nil {
		c.remember(err)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		return cancelled
	}
	return context.WithValue(child, callbackStateContextKey{}, callbackState{finish: finish})
}
func (c *Callbacks) onEnd(ctx context.Context, _ *callbacks.RunInfo, _ callbacks.CallbackOutput) context.Context {
	c.finish(ctx, "node_completed", "completed", "")
	return ctx
}

func (c *Callbacks) onError(ctx context.Context, _ *callbacks.RunInfo, _ error) context.Context {
	c.finish(ctx, "node_failed", "failed", "eino_error")
	return ctx
}

func (c *Callbacks) finish(ctx context.Context, eventType, status, errorCode string) {
	state, _ := ctx.Value(callbackStateContextKey{}).(callbackState)
	if state.finish == nil || c.recorder == nil {
		return
	}
	if err := state.finish(eventType, status, errorCode, nil, ""); err != nil {
		c.remember(err)
	}
}

func newSpanID() (string, error) {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("create execution span identity: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}
