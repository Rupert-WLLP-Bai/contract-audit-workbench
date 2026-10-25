package outboxrelay

import (
	"context"
	"encoding/json"
	"errors"
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
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        config.Stream,
		Subjects:    []string{config.Subject},
		Retention:   jetstream.LimitsPolicy,
		Storage:     jetstream.FileStorage,
		MaxAge:      24 * time.Hour,
		MaxMsgs:     100_000,
		Duplicates:  2 * time.Minute,
		Description: "contract-audit outbox notifications (advisory wake-ups; PostgreSQL is authoritative)",
	})
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &JetStreamBroker{conn: conn, js: js, stream: stream}, nil
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
