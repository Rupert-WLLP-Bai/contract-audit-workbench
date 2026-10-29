package outboxrelay

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/store"
)

type closeTrackingBroker struct {
	closes atomic.Int32
}

func (b *closeTrackingBroker) Publish(context.Context, string, string, []byte) error { return nil }
func (b *closeTrackingBroker) Close() error {
	b.closes.Add(1)
	return nil
}

type countingClaimStore struct {
	*fakeStore
	attempts atomic.Int32
}

func (s *countingClaimStore) ClaimOutbox(context.Context, string, int, time.Duration) ([]store.OutboxClaim, error) {
	s.attempts.Add(1)
	return nil, errors.New("database unavailable")
}

type cancelOnClaimStore struct {
	*fakeStore
	cancel context.CancelFunc
}

func (s *cancelOnClaimStore) ClaimOutbox(context.Context, string, int, time.Duration) ([]store.OutboxClaim, error) {
	s.cancel()
	return nil, context.Canceled
}

// Run ends exactly with its context and keeps the PG scan path alive across a
// step failure.
func TestRelayRunStopsWithItsContextAndSurvivesStepFailures(t *testing.T) {
	t.Run("already cancelled", func(t *testing.T) {
		st := &fakeStore{claims: []store.OutboxClaim{claim("outbox_1", time.Minute)}}
		relay, err := New(st, &fakeBroker{}, testConfig())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := relay.Run(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("run error %v", err)
		}
		if len(st.claimArgs) != 0 {
			t.Fatalf("cancelled run still claimed rows: %+v", st.claimArgs)
		}
	})

	t.Run("step failure does not stop the loop", func(t *testing.T) {
		st := &countingClaimStore{fakeStore: &fakeStore{}}
		relay, err := New(st, &fakeBroker{}, testConfig())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- relay.Run(ctx) }()
		deadline := time.After(5 * time.Second)
		for {
			if st.attempts.Load() >= 2 {
				break
			}
			select {
			case <-deadline:
				t.Fatalf("run stopped after a step failure: %d claim attempts", st.attempts.Load())
			case <-time.After(time.Millisecond):
			}
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("run error %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("run did not stop with the context")
		}
	})

	t.Run("step context error ends the run", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		st := &cancelOnClaimStore{fakeStore: &fakeStore{}, cancel: cancel}
		relay, err := New(st, &fakeBroker{}, testConfig())
		if err != nil {
			t.Fatal(err)
		}
		if err := relay.Run(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("run error %v", err)
		}
	})

	t.Run("successful step then cancellation", func(t *testing.T) {
		st := &fakeStore{claims: []store.OutboxClaim{claim("outbox_1", time.Minute)}, ackReady: make(chan struct{}, 1)}
		broker := &fakeBroker{}
		relay, err := New(st, broker, testConfig())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- relay.Run(ctx) }()
		select {
		case <-st.ackReady:
		case <-time.After(5 * time.Second):
			t.Fatal("relay did not publish and acknowledge the pending row")
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("run error %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("run did not stop with the context")
		}
		if len(st.acked) == 0 || len(broker.published) == 0 {
			t.Fatalf("acked=%v published=%+v", st.acked, broker.published)
		}
		for _, id := range st.acked {
			if id != "outbox_1" {
				t.Fatalf("acknowledged an unexpected row: %v", st.acked)
			}
		}
	})
}

// Serve reconnects with bounded backoff, closes every broker it opens, and
// returns the first configuration error instead of looping on it.
func TestServeBoundsReconnectsAndClosesEveryBroker(t *testing.T) {
	t.Run("cancelled before the first dial", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var dials atomic.Int32
		err := Serve(ctx, &fakeStore{}, testConfig(), func(context.Context, Config) (Broker, error) {
			dials.Add(1)
			return nil, errors.New("unexpected dial")
		})
		if !errors.Is(err, context.Canceled) || dials.Load() != 0 {
			t.Fatalf("cancelled serve dialed: err=%v dials=%d", err, dials.Load())
		}
	})

	t.Run("dial failure retries until cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var dials atomic.Int32
		done := make(chan error, 1)
		go func() {
			done <- Serve(ctx, &fakeStore{}, testConfig(), func(context.Context, Config) (Broker, error) {
				dials.Add(1)
				return nil, errors.New("connection refused")
			})
		}()
		deadline := time.After(5 * time.Second)
		for dials.Load() == 0 {
			select {
			case <-deadline:
				t.Fatal("serve did not attempt a dial")
			case <-time.After(time.Millisecond):
			}
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("serve error %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("serve did not stop after a failed dial")
		}
	})

	t.Run("invalid configuration closes the dialed broker", func(t *testing.T) {
		config := testConfig()
		config.Batch = 0
		broker := &closeTrackingBroker{}
		err := Serve(context.Background(), &fakeStore{}, config, func(context.Context, Config) (Broker, error) {
			return broker, nil
		})
		if err == nil || err.Error() != "outbox relay configuration is incomplete or invalid" {
			t.Fatalf("serve error %v", err)
		}
		if broker.closes.Load() != 1 {
			t.Fatalf("broker close count %d", broker.closes.Load())
		}
	})

	t.Run("cancellation closes the running broker", func(t *testing.T) {
		broker := &closeTrackingBroker{}
		ctx, cancel := context.WithCancel(context.Background())
		st := &fakeStore{claims: []store.OutboxClaim{claim("outbox_1", time.Minute)}, ackReady: make(chan struct{}, 1)}
		done := make(chan error, 1)
		go func() {
			done <- Serve(ctx, st, testConfig(), func(context.Context, Config) (Broker, error) {
				return broker, nil
			})
		}()
		select {
		case <-st.ackReady:
		case <-time.After(5 * time.Second):
			t.Fatal("serve did not drain the pending row")
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("serve error %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("serve did not stop with the context")
		}
		if broker.closes.Load() != 1 {
			t.Fatalf("broker close count %d", broker.closes.Load())
		}
	})
}

// Dial fails closed for unusable broker addresses; a reachable peer that does
// not answer the stream API is still an error, never a half-built broker.
func TestJetStreamDialRejectsUnusableBrokers(t *testing.T) {
	if _, err := Dial(context.Background(), Config{URL: "://not-a-url", Stream: Stream, Subject: Subject}); err == nil {
		t.Fatal("an invalid broker URL was accepted")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	if _, err := Dial(context.Background(), Config{URL: "nats://" + address, Stream: Stream, Subject: Subject, Replicas: 1}); err == nil {
		t.Fatal("a closed broker connection was accepted")
	}

	broker := newWakeupProtocolBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := Dial(ctx, Config{URL: broker.url(), Stream: Stream, Subject: Subject, Replicas: 1}); err == nil {
		t.Fatal("a peer without a stream API produced a broker")
	}
}

func TestJetStreamBrokerRefusesUnstableMessageIDsAndClosesSafely(t *testing.T) {
	var nilBroker *JetStreamBroker
	if err := nilBroker.Close(); err != nil {
		t.Fatalf("nil broker close: %v", err)
	}
	if err := (&JetStreamBroker{}).Close(); err != nil {
		t.Fatalf("empty broker close: %v", err)
	}
	if err := (&JetStreamBroker{}).Publish(context.Background(), Subject, "", []byte("{}")); err == nil ||
		err.Error() != "outbox notification requires a stable message id" {
		t.Fatalf("unstable message id accepted: %v", err)
	}
}

func TestWakeupHelpersFailClosedWhenDisabled(t *testing.T) {
	if wakeup, err := Subscribe(Config{}); wakeup != nil || err != nil {
		t.Fatalf("disabled subscribe: %v %v", wakeup, err)
	}
	if wakeup, err := Subscribe(Config{URL: "://bad", Subject: Subject}); wakeup != nil || err == nil {
		t.Fatalf("invalid subscribe URL accepted: %v %v", wakeup, err)
	}
	if wake := SubscribeLoop(context.Background(), Config{}); wake != nil {
		t.Fatalf("disabled subscribe loop returned a channel: %v", wake)
	}
	var nilWakeup *Wakeup
	if nilWakeup.Wake() != nil || nilWakeup.Done() != nil {
		t.Fatal("nil wakeup exposed a channel")
	}
	if err := nilWakeup.Close(); err != nil {
		t.Fatalf("nil wakeup close: %v", err)
	}
}

func TestSubscribeLoopRetriesAnUnusableBrokerUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	wake := SubscribeLoop(ctx, Config{URL: "://bad", Subject: Subject})
	if wake == nil {
		t.Fatal("enabled subscribe loop returned no channel")
	}
	time.AfterFunc(1300*time.Millisecond, cancel)
	select {
	case <-wake:
		t.Fatal("an unusable broker produced a wake-up")
	case <-ctx.Done():
	}
	select {
	case <-wake:
		t.Fatal("an unusable broker produced a late wake-up")
	default:
	}
}
