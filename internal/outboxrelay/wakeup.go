package outboxrelay

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// Wakeup delivers advisory "new outbox row" notifications to one worker replica.
//
// It is deliberately not authoritative: the worker's claim loop still takes work
// from PostgreSQL, so a missed, duplicated, or out-of-order notification can only
// delay a poll, never lose or duplicate a job. One notification per message, and a
// queue group so replicas share the stream.
type Wakeup struct {
	conn     *nats.Conn
	sub      *nats.Subscription
	wake     chan struct{}
	done     chan struct{}
	doneOnce sync.Once
}

// Subscribe connects to the broker and registers the wake-up subscription. A
// disabled configuration returns (nil, nil): callers keep running with the plain
// poll interval.
//
// The client reconnects and re-subscribes internally with unlimited retries, so a
// transient broker outage does not end this subscription; Done only closes when
// the connection is explicitly shut down.
func Subscribe(config Config) (*Wakeup, error) {
	if !config.Enabled() {
		return nil, nil
	}
	wakeup := &Wakeup{wake: make(chan struct{}, 1), done: make(chan struct{})}
	conn, err := nats.Connect(config.URL,
		nats.Name("contract-audit-worker-wakeup"),
		nats.Timeout(5*time.Second),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.ClosedHandler(func(*nats.Conn) { wakeup.signalDone() }),
	)
	if err != nil {
		return nil, err
	}
	wakeup.conn = conn
	subscription, err := conn.QueueSubscribe(config.Subject, ConsumerGroup, func(*nats.Msg) {
		select {
		case wakeup.wake <- struct{}{}:
		default: // a pending wake-up already covers this notification
		}
	})
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.FlushTimeout(5 * time.Second); err != nil {
		_ = subscription.Unsubscribe()
		conn.Close()
		return nil, err
	}
	wakeup.sub = subscription
	return wakeup, nil
}

func (w *Wakeup) signalDone() {
	w.doneOnce.Do(func() { close(w.done) })
}

// Wake is the notification channel. A nil channel (disabled relay) blocks
// forever, which keeps the claim loop on its plain poll interval.
func (w *Wakeup) Wake() <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.wake
}

// Done closes when the subscription can no longer deliver notifications (the
// connection was closed). It is the signal SubscribeLoop uses to reconnect.
func (w *Wakeup) Done() <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.done
}

// Close drains the subscription and closes the connection.
func (w *Wakeup) Close() error {
	if w == nil {
		return nil
	}
	if w.sub != nil {
		_ = w.sub.Drain()
	}
	if w.conn != nil {
		w.conn.Close()
	}
	w.signalDone()
	return nil
}

// SubscribeLoop returns a wake-up channel that survives a broker that is not up
// yet or that goes away: it keeps at most one connection and one subscription
// open, consumes that subscription for its whole lifetime (the NATS client
// reconnects internally, so a notification never causes a reconnect), closes it
// before reconnecting, and ends with the context.
func SubscribeLoop(ctx context.Context, config Config) <-chan struct{} {
	if !config.Enabled() {
		return nil
	}
	wake := make(chan struct{}, 1)
	go func() {
		delay := time.Second
		for {
			if ctx.Err() != nil {
				return
			}
			subscriber, err := Subscribe(config)
			if err != nil {
				log.Printf("worker wake-up subscription unavailable: %v (retrying in %s)", err, delay)
				if err := wait(ctx, delay); err != nil {
					return
				}
				if delay *= 2; delay > reconnectBackoff {
					delay = reconnectBackoff
				}
				continue
			}
			delay = time.Second
			for subscriber != nil {
				select {
				case <-ctx.Done():
					_ = subscriber.Close()
					return
				case <-subscriber.Done():
					// The connection was closed: drop this subscription before
					// opening another one, so notifications never accumulate
					// connections or land in an abandoned channel.
					_ = subscriber.Close()
					subscriber = nil
				case <-subscriber.Wake():
					select {
					case wake <- struct{}{}:
					default:
					}
				}
			}
		}
	}()
	return wake
}
