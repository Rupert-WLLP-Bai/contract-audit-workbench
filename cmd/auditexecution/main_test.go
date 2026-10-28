package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWaitRelayWaitsForBackgroundPublisher(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		time.Sleep(30 * time.Millisecond)
		done <- nil
	}()
	started := time.Now()
	if err := waitRelay(done, time.Second); err != nil {
		t.Fatalf("completed relay reported an error: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Fatalf("waitRelay did not wait for the relay to complete: %s", elapsed)
	}
}

func TestWaitRelayTreatsCancellationAsNormalShutdown(t *testing.T) {
	done := make(chan error, 1)
	done <- context.Canceled
	if err := waitRelay(done, time.Second); err != nil {
		t.Fatalf("context cancellation surfaced as a shutdown failure: %v", err)
	}
}

func TestWaitRelaySurfacesUnexpectedFailure(t *testing.T) {
	done := make(chan error, 1)
	done <- errors.New("broker configuration rejected")
	err := waitRelay(done, time.Second)
	if err == nil || !strings.Contains(err.Error(), "outbox relay") {
		t.Fatalf("unexpected relay failure was swallowed: %v", err)
	}
}

func TestWaitRelayBoundsAnUnresponsiveRelay(t *testing.T) {
	done := make(chan error, 1)
	started := time.Now()
	err := waitRelay(done, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "shutdown timeout") {
		t.Fatalf("unresponsive relay was not bounded: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("relay timeout waited too long: %s", elapsed)
	}
}
