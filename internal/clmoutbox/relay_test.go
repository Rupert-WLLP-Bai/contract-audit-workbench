package clmoutbox

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"contractaudit/internal/outboxrelay"
	"contractaudit/internal/store"
)

type fakeCoreStore struct {
	claim      store.CLMOutboxClaim
	ackCalls   int
	acked      bool
	releaseIDs []string
}

func (s *fakeCoreStore) ClaimCLMOutbox(context.Context, int, time.Duration) ([]store.CLMOutboxClaim, error) {
	if s.acked {
		return nil, nil
	}
	return []store.CLMOutboxClaim{s.claim}, nil
}

func (s *fakeCoreStore) AckCLMOutbox(_ context.Context, claim store.CLMOutboxClaim) error {
	s.ackCalls++
	if s.ackCalls == 1 {
		return errors.New("simulated lost database acknowledgement")
	}
	s.acked = true
	return nil
}

func (s *fakeCoreStore) ReleaseCLMOutbox(_ context.Context, claim store.CLMOutboxClaim, reason string, _ time.Duration) error {
	s.releaseIDs = append(s.releaseIDs, reason+":"+claim.EventID)
	return nil
}

type fakeWakeSubscription struct{ wake chan struct{} }

func (s *fakeWakeSubscription) Wake() <-chan struct{} { return s.wake }
func (s *fakeWakeSubscription) Close() error          { return nil }

type fakeBroker struct {
	ids      []string
	payloads [][]byte
}

func (b *fakeBroker) Publish(_ context.Context, subject, id string, payload []byte) error {
	if subject != DefaultSubject {
		return errors.New("unexpected subject")
	}
	b.ids = append(b.ids, id)
	b.payloads = append(b.payloads, append([]byte(nil), payload...))
	return nil
}
func (b *fakeBroker) Close() error { return nil }
func (b *fakeBroker) SubscribeWake(string) (outboxrelay.WakeSubscription, error) {
	return &fakeWakeSubscription{wake: make(chan struct{}, 1)}, nil
}

func TestRelayRedeliversStableIDAfterBrokerAckStateUncertain(t *testing.T) {
	claim := store.CLMOutboxClaim{
		TenantID: "tenant-secret-scope", EventID: "0df1202e-2b0a-4220-8ca8-12d3f3d390e1",
		EventType: "contract.approval_submitted.v1", Token: 4,
		LeaseUntil: time.Now().Add(time.Minute), MessageID: "tenant-secret-scope:0df1202e-2b0a-4220-8ca8-12d3f3d390e1",
	}
	core := &fakeCoreStore{claim: claim}
	broker := &fakeBroker{}
	config := outboxrelay.Config{
		Owner: "core-test", Batch: 1, Lease: 30 * time.Second, Poll: time.Millisecond,
		PublishTimeout: time.Second, MaxBackoff: time.Second, Subject: DefaultSubject,
		Stream: DefaultStream, Replicas: 1,
	}
	relay, err := New(core, broker, config)
	if err != nil {
		t.Fatal(err)
	}
	if delivered, err := relay.Step(context.Background()); err == nil || delivered != 0 {
		t.Fatalf("first unknown acknowledgement: delivered=%d err=%v", delivered, err)
	}
	if delivered, err := relay.Step(context.Background()); err != nil || delivered != 1 {
		t.Fatalf("redelivery after unknown acknowledgement: delivered=%d err=%v", delivered, err)
	}
	if len(broker.ids) != 2 || broker.ids[0] != claim.MessageID || broker.ids[1] != claim.MessageID {
		t.Fatalf("message id not stable on retry: %v", broker.ids)
	}
	var envelope map[string]any
	if err := json.Unmarshal(broker.payloads[0], &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["event_id"] != claim.EventID || envelope["event_type"] != claim.EventType ||
		len(envelope) != 2 || envelope["tenant_id"] != nil || envelope["case_id"] != nil || envelope["payload"] != nil {
		t.Fatalf("notification must contain only non-authorizing event identity: %#v", envelope)
	}
	if !core.acked || core.ackCalls != 2 {
		t.Fatalf("core store acknowledgement state: acked=%v calls=%d", core.acked, core.ackCalls)
	}
}
