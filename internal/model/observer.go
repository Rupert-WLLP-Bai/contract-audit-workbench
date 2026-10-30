package model

import (
	"context"

	"contractaudit/internal/execution"
)

type callObserverKey struct{}

// CallObservation contains bounded completion metadata from one physical request.
type CallObservation struct {
	ModelRef  string // Provider-reported identity; empty when the response did not report one.
	Usage     *TokenUsage
	Status    string
	ErrorCode string
	// Trace is the bounded request/response record. It is never used to decide
	// a finding; it only makes the physical call inspectable.
	Trace *execution.ModelCallTrace
}

// CallObserver observes one request from dispatch through its complete or failed response.
type CallObserver interface {
	StartModelCall(context.Context, string) (context.Context, func(CallObservation))
}

// WithCallObserver attaches an invocation-local observer. It is never process-global.
func WithCallObserver(ctx context.Context, observer CallObserver) context.Context {
	return context.WithValue(ctx, callObserverKey{}, observer)
}

func callObserverFromContext(ctx context.Context) CallObserver {
	observer, _ := ctx.Value(callObserverKey{}).(CallObserver)
	return observer
}
