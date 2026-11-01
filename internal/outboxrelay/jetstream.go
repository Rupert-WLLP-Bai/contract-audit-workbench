package outboxrelay

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func jsonMarshal(value any) ([]byte, error) { return json.Marshal(value) }

// JetStreamBroker publishes notifications into a durable file-backed stream. The
// publish call returns only after the server has stored the message, which is
// the acknowledgement the relay must have before marking the outbox row
// delivered.
type JetStreamBroker struct {
	conn   *nats.Conn
	js     jetstream.JetStream
	stream jetstream.Stream
}

// WakeSubscription is a coalesced Core NATS notification channel. It deliberately
// exposes no message bytes: callers use notifications only to refresh durable
// PostgreSQL state and cannot treat a payload as authorization.
type WakeSubscription interface {
	Wake() <-chan struct{}
	Close() error
}

type wakeSubscription struct {
	sub  *nats.Subscription
	wake chan struct{}
	once sync.Once
	err  error
}

func (s *wakeSubscription) Wake() <-chan struct{} { return s.wake }

func (s *wakeSubscription) Close() error {
	s.once.Do(func() {
		if s.sub != nil {
			s.err = s.sub.Drain()
		}
	})
	return s.err
}

// SubscribeWake listens on a Core NATS subject using the same authenticated,
// TLS-configured connection that publishes JetStream messages. Duplicate or
// burst notifications are coalesced to one wake token; message bodies are
// discarded without parsing.
func (b *JetStreamBroker) SubscribeWake(subject string) (WakeSubscription, error) {
	if b == nil || b.conn == nil || b.conn.IsClosed() {
		return nil, errors.New("NATS wake subscription requires an open broker connection")
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return nil, errors.New("NATS wake subscription requires a subject")
	}
	wake := make(chan struct{}, 1)
	sub, err := b.conn.Subscribe(subject, func(*nats.Msg) {
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return nil, err
	}
	if err := b.conn.FlushTimeout(5 * time.Second); err != nil {
		_ = sub.Unsubscribe()
		return nil, err
	}
	return &wakeSubscription{sub: sub, wake: wake}, nil
}

// Dial connects to the broker and ensures the notification stream exists. It is
// idempotent and never creates a consumer: the stream only holds short-lived
// notifications, bounded by age and count, while PostgreSQL remains the
// authority for work.
func Dial(ctx context.Context, config Config) (*JetStreamBroker, error) {
	conn, err := nats.Connect(config.URL,
		nats.Name("contract-audit-outbox-relay"),
		nats.Timeout(5*time.Second),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, err
	}
	js, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	stream, err := js.CreateOrUpdateStream(ctx, streamConfig(config))
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &JetStreamBroker{conn: conn, js: js, stream: stream}, nil
}

// streamConfig keeps the stream's replication factor explicit. A three-server
// NATS cluster does not make the stream replicated by itself; Replicas must be
// set to 3 on a cluster with three peers.
func streamConfig(config Config) jetstream.StreamConfig {
	replicas := config.Replicas
	if replicas < 1 {
		replicas = 1
	}
	return jetstream.StreamConfig{
		Name:        config.Stream,
		Subjects:    []string{config.Subject},
		Retention:   jetstream.LimitsPolicy,
		Storage:     jetstream.FileStorage,
		Replicas:    replicas,
		MaxAge:      24 * time.Hour,
		MaxMsgs:     100_000,
		Duplicates:  2 * time.Minute,
		Description: "contract-audit outbox notifications (advisory wake-ups; PostgreSQL is authoritative)",
	}
}

// Publish stores one notification with the outbox row id as its message id, so
// a republished row is de-duplicated by the broker instead of delivered twice.
func (b *JetStreamBroker) Publish(ctx context.Context, subject, messageID string, payload []byte) error {
	if messageID == "" {
		return errors.New("outbox notification requires a stable message id")
	}
	_, err := b.js.Publish(ctx, subject, payload, jetstream.WithMsgID(messageID))
	return err
}

// Close drains pending publishes and closes the connection.
func (b *JetStreamBroker) Close() error {
	if b == nil || b.conn == nil {
		return nil
	}
	return b.conn.Drain()
}
