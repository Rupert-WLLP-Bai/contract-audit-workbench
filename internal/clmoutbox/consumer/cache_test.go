package consumer

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type countingRefresher struct {
	calls chan struct{}
}

func (r countingRefresher) RefreshCLMMetrics(context.Context, *time.Duration) error {
	r.calls <- struct{}{}
	return nil
}

func TestRunRefreshesOnRepeatedWakeups(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	wake := make(chan struct{}, 1)
	refresher := countingRefresher{calls: make(chan struct{}, 4)}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, refresher, nil, time.Hour, wake) }()
	select {
	case <-refresher.calls:
	case <-time.After(time.Second):
		t.Fatal("initial periodic snapshot did not run")
	}
	wake <- struct{}{}
	select {
	case <-refresher.calls:
	case <-time.After(time.Second):
		t.Fatal("first advisory wake did not refresh")
	}
	wake <- struct{}{}
	select {
	case <-refresher.calls:
	case <-time.After(time.Second):
		t.Fatal("duplicate advisory wake did not refresh idempotently")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumer did not stop on cancellation")
	}
}

func TestRunUsesPeriodicRefreshWithoutNATS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	refresher := refresherFunc(func(context.Context, *time.Duration) error {
		calls.Add(1)
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- Run(ctx, refresher, nil, 10*time.Millisecond, nil) }()
	deadline := time.After(time.Second)
	for calls.Load() < 2 {
		select {
		case <-deadline:
			cancel()
			t.Fatal("periodic PG refresh did not recover without a wake source")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	<-done
}

type refresherFunc func(context.Context, *time.Duration) error

func (f refresherFunc) RefreshCLMMetrics(ctx context.Context, age *time.Duration) error {
	return f(ctx, age)
}
