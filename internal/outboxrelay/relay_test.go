package outboxrelay

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"contractaudit/internal/store"
)

type fakeStore struct {
	claims    []store.OutboxClaim
	claimErr  error
	acked     []string
	claimArgs []string
	ackReady  chan struct{}
	released  []struct {
		id     string
		reason string
		delay  time.Duration
	}
	ackErr     error
	releaseErr error
}

func (f *fakeStore) ClaimOutbox(_ context.Context, owner string, limit int, lease time.Duration) ([]store.OutboxClaim, error) {
	f.claimArgs = append(f.claimArgs, owner)
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	if limit > len(f.claims) {
		limit = len(f.claims)
	}
	return f.claims[:limit], nil
}

func (f *fakeStore) AckOutbox(_ context.Context, claim store.OutboxClaim) error {
	if f.ackErr != nil {
		return f.ackErr
	}
	f.acked = append(f.acked, claim.Event.ID)
	select {
	case f.ackReady <- struct{}{}:
	default:
	}
	return nil
}

func (f *fakeStore) ReleaseOutbox(_ context.Context, claim store.OutboxClaim, reasonCode string, retryAfter time.Duration) error {
	if f.releaseErr != nil {
		return f.releaseErr
	}
	f.released = append(f.released, struct {
		id     string
		reason string
		delay  time.Duration
	}{claim.Event.ID, reasonCode, retryAfter})
	return nil
}

type fakeBroker struct {
	published []struct {
		subject  string
		msgID    string
		payload  []byte
		deadline time.Time
		hasLimit bool
	}
	err error
}

func (f *fakeBroker) Publish(ctx context.Context, subject, messageID string, payload []byte) error {
	if f.err != nil {
		return f.err
	}
	deadline, hasLimit := ctx.Deadline()
	f.published = append(f.published, struct {
		subject  string
		msgID    string
		payload  []byte
		deadline time.Time
		hasLimit bool
	}{subject, messageID, payload, deadline, hasLimit})
	return nil
}

func (f *fakeBroker) Close() error { return nil }

func testConfig() Config {
	return Config{URL: "nats://127.0.0.1:4222", Owner: "relay-test", Batch: 4, Lease: 30 * time.Second,
		Poll: 50 * time.Millisecond, PublishTimeout: 2 * time.Second, MaxBackoff: time.Minute,
		Subject: Subject, Stream: Stream, Replicas: 1}
}

func claim(id string, lease time.Duration) store.OutboxClaim {
	return store.OutboxClaim{Event: store.OutboxRecord{ID: id}, LeaseUntil: time.Now().Add(lease)}
}

func TestStepPublishesWithStableIDThenAcknowledges(t *testing.T) {
	st := &fakeStore{claims: []store.OutboxClaim{claim("outbox_1", time.Minute), claim("outbox_2", time.Minute)}}
	broker := &fakeBroker{}
	relay, err := New(st, broker, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	published, err := relay.Step(context.Background())
	if err != nil || published != 2 {
		t.Fatalf("step published=%d err=%v", published, err)
	}
	if len(broker.published) != 2 || broker.published[0].msgID != "outbox_1" || broker.published[1].msgID != "outbox_2" {
		t.Fatalf("publish ids are not the stable outbox ids: %+v", broker.published)
	}
	if broker.published[0].subject != Subject {
		t.Fatalf("subject %q", broker.published[0].subject)
	}
	var payload map[string]any
	if err := json.Unmarshal(broker.published[0].payload, &payload); err != nil {
		t.Fatalf("payload is not the row notification: %v", err)
	}
	if len(st.acked) != 2 || len(st.released) != 0 {
		t.Fatalf("acked=%v released=%v", st.acked, st.released)
	}
	if len(st.claimArgs) != 1 || st.claimArgs[0] != "relay-test" {
		t.Fatalf("claim owner %+v", st.claimArgs)
	}
}

// A batch can outlive a single publish timeout: each publish is bounded by the
// claim's own remaining lease, and a claim without enough lease left is skipped
// instead of racing the takeover owner.
func TestStepClampsThePublishDeadlineToTheClaimLease(t *testing.T) {
	st := &fakeStore{claims: []store.OutboxClaim{
		claim("outbox_far", time.Minute),
		claim("outbox_near", 900*time.Millisecond),
		claim("outbox_expired", -time.Second),
	}}
	broker := &fakeBroker{}
	config := testConfig()
	config.PublishTimeout = 10 * time.Second
	relay, err := New(st, broker, config)
	if err != nil {
		t.Fatal(err)
	}
	published, err := relay.Step(context.Background())
	if err != nil || published != 2 {
		t.Fatalf("published=%d err=%v", published, err)
	}
	if len(broker.published) != 2 {
		t.Fatalf("expired claim was published: %+v", broker.published)
	}
	if !broker.published[0].hasLimit || time.Until(broker.published[0].deadline) > config.PublishTimeout {
		t.Fatalf("far claim deadline %v exceeded the publish timeout", time.Until(broker.published[0].deadline))
	}
	near := time.Until(broker.published[1].deadline)
	if near <= 0 || near > time.Second {
		t.Fatalf("near claim deadline %v was not clamped to its lease", near)
	}
	if len(st.acked) != 2 {
		t.Fatalf("acked=%v", st.acked)
	}
	for _, id := range st.acked {
		if id == "outbox_expired" {
			t.Fatal("an expired claim must never be acknowledged")
		}
	}
}

func TestStepReleasesWhenTheBrokerDoesNotAccept(t *testing.T) {
	st := &fakeStore{claims: []store.OutboxClaim{claim("outbox_9", time.Minute)}}
	broker := &fakeBroker{err: errors.New("broker down")}
	relay, err := New(st, broker, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	published, err := relay.Step(context.Background())
	if err != nil || published != 0 {
		t.Fatalf("step published=%d err=%v", published, err)
	}
	if len(st.acked) != 0 {
		t.Fatalf("a failed publish must not acknowledge: %+v", st.acked)
	}
	if len(st.released) != 1 || st.released[0].id != "outbox_9" || st.released[0].reason != "broker_unavailable" {
		t.Fatalf("release %+v", st.released)
	}
	if st.released[0].delay <= 0 || st.released[0].delay > time.Minute {
		t.Fatalf("retry delay %v is outside the bounded window", st.released[0].delay)
	}
	if _, err := relay.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(st.released) != 2 || st.released[1].delay <= st.released[0].delay {
		t.Fatalf("backoff did not grow: %+v", st.released)
	}
}

// An acknowledge failure is reported, never counted as a delivered row: the
// broker may hold the message, so the row stays pending and the stable id keeps
// the retry safe.
func TestAcknowledgeFailureIsReportedNotCounted(t *testing.T) {
	st := &fakeStore{claims: []store.OutboxClaim{claim("outbox_5", time.Minute)}, ackErr: errors.New("database unavailable")}
	relay, err := New(st, &fakeBroker{}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	published, err := relay.Step(context.Background())
	if err == nil {
		t.Fatal("an acknowledge failure must surface")
	}
	if published != 0 {
		t.Fatalf("a failed acknowledge was counted as delivered: %d", published)
	}
}

func TestLostClaimIsDroppedSilently(t *testing.T) {
	st := &fakeStore{claims: []store.OutboxClaim{claim("outbox_3", time.Minute)}, ackErr: store.ErrOutboxClaimLost}
	relay, err := New(st, &fakeBroker{}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	published, err := relay.Step(context.Background())
	if err != nil || published != 0 {
		t.Fatalf("a lost claim must be dropped silently: published=%d err=%v", published, err)
	}
	if len(st.released) != 0 {
		t.Fatalf("a lost claim must not be released again: %+v", st.released)
	}

	st2 := &fakeStore{claims: []store.OutboxClaim{claim("outbox_4", time.Minute)}, releaseErr: store.ErrOutboxClaimLost}
	relay2, err := New(st2, &fakeBroker{err: errors.New("broker down")}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := relay2.Step(context.Background()); err != nil {
		t.Fatalf("a lost claim during release must not surface: %v", err)
	}
}

func TestClaimFailureIsReported(t *testing.T) {
	st := &fakeStore{claimErr: errors.New("database unavailable")}
	relay, err := New(st, &fakeBroker{}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := relay.Step(context.Background()); err == nil {
		t.Fatal("a claim failure must be reported so the loop can log it")
	}
}

// Serve keeps retrying a broker that is not up yet, and stops with the context.
func TestServeRetriesUntilTheBrokerAppearsAndStopsWithTheContext(t *testing.T) {
	ackReady := make(chan struct{}, 1)
	st := &fakeStore{claims: []store.OutboxClaim{claim("outbox_after_recovery", time.Minute)}, ackReady: ackReady}
	broker := &fakeBroker{}
	attempts := 0
	dial := func(context.Context, Config) (Broker, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("connection refused")
		}
		return broker, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, st, testConfig(), dial) }()
	select {
	case <-ackReady:
	case <-time.After(10 * time.Second):
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop with the context")
	}
	if len(broker.published) == 0 || broker.published[0].msgID != "outbox_after_recovery" || len(st.acked) == 0 || st.acked[0] != "outbox_after_recovery" {
		t.Fatalf("the relay did not publish and acknowledge the recovered event: published=%+v acked=%v", broker.published, st.acked)
	}
	if attempts < 3 {
		t.Fatalf("serve did not retry the dial: %d attempts", attempts)
	}
}

func TestConfigValidation(t *testing.T) {
	if config, err := FromEnv(); err != nil || config.Enabled() {
		t.Fatalf("an unset NATS_URL must yield a disabled relay: %+v err=%v", config, err)
	}
	t.Setenv("NATS_URL", "nats://127.0.0.1:4222")
	t.Setenv("OUTBOX_RELAY_PUBLISH_TIMEOUT", "60s")
	t.Setenv("OUTBOX_RELAY_LEASE", "30s")
	if _, err := FromEnv(); err == nil {
		t.Fatal("a publish timeout at or above the lease must be refused")
	}
	t.Setenv("OUTBOX_RELAY_PUBLISH_TIMEOUT", "5s")
	config, err := FromEnv()
	if err != nil || !config.Enabled() || config.Owner == "" || config.PublishTimeout >= config.Lease {
		t.Fatalf("valid configuration rejected: %+v err=%v", config, err)
	}
	if _, err := New(&fakeStore{}, &fakeBroker{}, Config{}); err == nil {
		t.Fatal("a disabled configuration must not build a relay")
	}
	if _, err := New(nil, &fakeBroker{}, testConfig()); err == nil {
		t.Fatal("a missing store must be refused")
	}
}

func TestStreamReplicationFactorIsExplicit(t *testing.T) {
	config := testConfig()
	config.Replicas = 3
	if got := streamConfig(config).Replicas; got != 3 {
		t.Fatalf("stream replication factor lost: %d", got)
	}
	t.Setenv("NATS_URL", "nats://127.0.0.1:4222")
	t.Setenv("NATS_STREAM_REPLICAS", "3")
	parsed, err := FromEnv()
	if err != nil || parsed.Replicas != 3 {
		t.Fatalf("replication factor not parsed: %+v err=%v", parsed, err)
	}
	t.Setenv("NATS_STREAM_REPLICAS", "0")
	if _, err := FromEnv(); err == nil {
		t.Fatal("zero stream replication must be refused")
	}
}
