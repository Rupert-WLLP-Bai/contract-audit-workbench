// Package clmoutbox relays Core-owned contract events without borrowing the
// Execution service's legacy Outbox tables, claims, or permissions.
package clmoutbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"contractaudit/internal/clmoutbox/consumer"
	"contractaudit/internal/outboxrelay"
	"contractaudit/internal/store"
)

const (
	DefaultStream  = "CONTRACT_AUDIT_CLM_OUTBOX"
	DefaultSubject = "contract-audit.clm-outbox.v1"
	defaultBatch   = 32
	defaultLease   = 30 * time.Second
	defaultPoll    = 500 * time.Millisecond
	defaultTimeout = 5 * time.Second
	defaultBackoff = time.Minute
	leaseMargin    = 500 * time.Millisecond
	reconnectDelay = time.Second
	maxReconnect   = 30 * time.Second
)

// Store is the transport-neutral Core service boundary. The implementation is
// owned by internal/store and is authorized independently from Execution.
type Store interface {
	ClaimCLMOutbox(context.Context, int, time.Duration) ([]store.CLMOutboxClaim, error)
	AckCLMOutbox(context.Context, store.CLMOutboxClaim) error
	ReleaseCLMOutbox(context.Context, store.CLMOutboxClaim, string, time.Duration) error
}

// MetricsRefresher only recomputes fixed-dimension scalar PostgreSQL metrics.
// It must not interpret broker data as a tenant, case, or user authorization.
type MetricsRefresher interface {
	RefreshCLMMetrics(context.Context, *time.Duration) error
}

// Broker combines the existing JetStream publish transport with an ephemeral
// broadcast wake subscription on the same authenticated connection.
type Broker interface {
	outboxrelay.Broker
	SubscribeWake(string) (outboxrelay.WakeSubscription, error)
}

type Dialer func(context.Context, outboxrelay.Config) (Broker, error)

type Relay struct {
	store   Store
	broker  outboxrelay.Broker
	config  outboxrelay.Config
	backoff time.Duration
}

func New(st Store, broker outboxrelay.Broker, config outboxrelay.Config) (*Relay, error) {
	if st == nil || broker == nil {
		return nil, errors.New("CLM outbox relay requires a Core store and broker")
	}
	if strings.TrimSpace(config.Stream) == "" || strings.TrimSpace(config.Subject) == "" {
		return nil, errors.New("CLM outbox stream and subject are required")
	}
	if config.Batch == 0 {
		config.Batch = defaultBatch
	}
	if config.Lease == 0 {
		config.Lease = defaultLease
	}
	if config.Poll == 0 {
		config.Poll = defaultPoll
	}
	if config.PublishTimeout == 0 {
		config.PublishTimeout = defaultTimeout
	}
	if config.MaxBackoff == 0 {
		config.MaxBackoff = defaultBackoff
	}
	if config.Replicas < 1 || config.Replicas > 5 || config.Batch < 1 || config.Batch > 512 ||
		config.Lease <= leaseMargin || config.Poll <= 0 || config.PublishTimeout <= 0 ||
		config.PublishTimeout > config.Lease-leaseMargin || config.MaxBackoff <= 0 {
		return nil, errors.New("CLM outbox relay configuration is invalid")
	}
	return &Relay{store: st, broker: broker, config: config, backoff: config.Poll}, nil
}

type notice struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
}

// Step claims one bounded batch, publishes only a short event identity notice,
// then acknowledges each row using its fenced Core claim token.
func (r *Relay) Step(ctx context.Context) (int, error) {
	claims, err := r.store.ClaimCLMOutbox(ctx, r.config.Batch, r.config.Lease)
	if err != nil {
		return 0, fmt.Errorf("claim CLM outbox: %w", err)
	}
	var delivered int
	var firstErr error
	for _, claim := range claims {
		if claim.EventID == "" || claim.EventType == "" || claim.MessageID == "" {
			if err := r.release(ctx, claim, "claim_invalid"); err != nil && firstErr == nil {
				firstErr = err
			}
			continue
		}
		remaining := time.Until(claim.LeaseUntil) - leaseMargin
		if remaining <= 0 {
			if err := r.releaseAfter(ctx, claim, "lease_window_exhausted", r.config.Poll); err != nil && firstErr == nil {
				firstErr = err
			}
			continue
		}
		if remaining > r.config.PublishTimeout {
			remaining = r.config.PublishTimeout
		}
		payload, _ := json.Marshal(notice{EventID: claim.EventID, EventType: claim.EventType})
		publishCtx, cancel := context.WithTimeout(ctx, remaining)
		publishErr := r.broker.Publish(publishCtx, r.config.Subject, claim.MessageID, payload)
		cancel()
		if publishErr != nil {
			if err := r.release(ctx, claim, "broker_unavailable"); err != nil && firstErr == nil {
				firstErr = errors.Join(publishErr, err)
			} else if firstErr == nil {
				firstErr = publishErr
			}
			continue
		}
		if err := r.store.AckCLMOutbox(ctx, claim); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("acknowledge CLM outbox event %s: %w", claim.EventID, err)
			}
			continue
		}
		delivered++
	}
	return delivered, firstErr
}

func (r *Relay) release(ctx context.Context, claim store.CLMOutboxClaim, reason string) error {
	return r.releaseAfter(ctx, claim, reason, r.nextBackoff())
}

func (r *Relay) releaseAfter(ctx context.Context, claim store.CLMOutboxClaim, reason string, delay time.Duration) error {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.config.PublishTimeout)
	defer cancel()
	return r.store.ReleaseCLMOutbox(releaseCtx, claim, reason, delay)
}

func (r *Relay) nextBackoff() time.Duration {
	delay := r.backoff
	if delay <= 0 {
		delay = r.config.Poll
	} else {
		delay *= 2
	}
	if delay > r.config.MaxBackoff {
		delay = r.config.MaxBackoff
	}
	r.backoff = delay
	return delay
}

func (r *Relay) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		count, err := r.Step(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return ctx.Err()
			}
			log.Printf("CLM outbox relay step failed: %v; retrying in %s", err, r.backoff)
			if err := wait(ctx, r.backoff); err != nil {
				return err
			}
			continue
		}
		r.backoff = r.config.Poll
		if count == 0 {
			if err := wait(ctx, r.config.Poll); err != nil {
				return err
			}
		}
	}
}

// Serve starts a periodic PG metrics refresh even if NATS is disabled or
// unavailable. With a broker, each publication wakes every replica's scalar
// refresh scheduler; durable PG polling remains the recovery path.
func Serve(ctx context.Context, st Store, config outboxrelay.Config, metrics MetricsRefresher, agedAfter *time.Duration, dial Dialer) error {
	if st == nil || metrics == nil {
		return errors.New("CLM outbox service requires Core storage and metrics refresh")
	}
	serviceCtx, stopService := context.WithCancel(ctx)
	consumerDone := make(chan struct{})
	wake := make(chan struct{}, 1)
	go func() {
		defer close(consumerDone)
		_ = consumer.Run(serviceCtx, metrics, agedAfter, consumer.DefaultRefreshInterval, wake)
	}()
	defer func() {
		stopService()
		<-consumerDone
	}()
	ctx = serviceCtx
	if config.URL == "" {
		<-ctx.Done()
		return ctx.Err()
	}
	if dial == nil {
		return errors.New("CLM outbox service requires a NATS dialer")
	}
	delay := reconnectDelay
	for ctx.Err() == nil {
		broker, err := dial(ctx, config)
		if err != nil {
			log.Printf("CLM outbox NATS unavailable: %v; PG metrics refresh remains active, retrying in %s", err, delay)
			if err := wait(ctx, delay); err != nil {
				return err
			}
			delay = growBackoff(delay, maxReconnect)
			continue
		}
		subscription, err := broker.SubscribeWake(config.Subject)
		if err != nil {
			_ = broker.Close()
			log.Printf("CLM outbox wake subscription unavailable: %v; retrying in %s", err, delay)
			if err := wait(ctx, delay); err != nil {
				return err
			}
			delay = growBackoff(delay, maxReconnect)
			continue
		}
		runCtx, cancel := context.WithCancel(ctx)
		go forwardWake(runCtx, subscription.Wake(), wake)
		relay, err := New(st, broker, config)
		if err != nil {
			cancel()
			_ = subscription.Close()
			_ = broker.Close()
			return err
		}
		delay = reconnectDelay
		err = relay.Run(runCtx)
		cancel()
		_ = subscription.Close()
		_ = broker.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			log.Printf("CLM outbox relay ended: %v; reconnecting in %s", err, delay)
		}
		if err := wait(ctx, delay); err != nil {
			return err
		}
		delay = growBackoff(delay, maxReconnect)
	}
	return ctx.Err()
}

func forwardWake(ctx context.Context, source <-chan struct{}, destination chan<- struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-source:
			if !ok {
				return
			}
			select {
			case destination <- struct{}{}:
			default:
			}
		}
	}
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func growBackoff(delay, maximum time.Duration) time.Duration {
	if delay *= 2; delay > maximum {
		return maximum
	}
	return delay
}
