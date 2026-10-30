// Package outboxrelay publishes committed outbox rows to a JetStream broker and
// prompts idle workers.
//
// PostgreSQL stays the only authority. The relay claims rows through the
// existing outbox API, publishes each row with its stable id as the broker
// message id, and only acknowledges the row after the broker has durably
// acknowledged the publish; a crash between publish and acknowledge leaves the
// row pending, so the same id is republished rather than lost. Workers still
// claim work through PostgreSQL — a wake-up only shortens an idle poll, and a
// missing notification is never a lost job. With NATS unconfigured the relay
// stays disabled and the PG scan is the whole recovery path, which is logged
// explicitly rather than silently.
package outboxrelay

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/store"
)

const (
	// Subject carries one notification per published outbox row.
	Subject = "contract-audit.outbox.v1"
	// Stream is the durable JetStream stream that holds the notifications.
	Stream = "CONTRACT_AUDIT_OUTBOX"
	// ConsumerGroup lets exactly one worker replica wake per notification.
	ConsumerGroup = "audit-workers"

	defaultBatch          = 32
	defaultLease          = 30 * time.Second
	defaultPoll           = 500 * time.Millisecond
	defaultPublishTimeout = 5 * time.Second
	defaultMaxBackoff     = time.Minute
	// leaseSafetyMargin keeps a publish strictly inside the claim's lease.
	leaseSafetyMargin = 500 * time.Millisecond
	// reconnectBackoff bounds how often a relay retries a missing broker.
	reconnectBackoff = 30 * time.Second
)

// Config is the relay's bounded runtime configuration. An empty URL disables the
// relay as well as the worker wake-up subscription.
type Config struct {
	URL            string
	Owner          string
	Batch          int
	Lease          time.Duration
	Poll           time.Duration
	PublishTimeout time.Duration
	MaxBackoff     time.Duration
	Subject        string
	Stream         string
	Replicas       int
}

// FromEnv reads the relay configuration. It returns a disabled configuration
// (Enabled false) when NATS_URL is unset and never invents a broker address.
func FromEnv() (Config, error) {
	config := Config{
		URL:   strings.TrimSpace(os.Getenv("NATS_URL")),
		Owner: strings.TrimSpace(os.Getenv("OUTBOX_RELAY_OWNER")),
		Batch: defaultBatch, Lease: defaultLease, Poll: defaultPoll,
		PublishTimeout: defaultPublishTimeout, MaxBackoff: defaultMaxBackoff,
		Subject: Subject, Stream: Stream, Replicas: 1,
	}
	if config.Owner == "" {
		owner, err := defaultRelayOwner()
		if err != nil {
			return Config{}, err
		}
		config.Owner = owner
	}
	if err := config.applyBoundedInteger("OUTBOX_RELAY_BATCH", 1, 512, &config.Batch); err != nil {
		return Config{}, err
	}
	// Replicas is the JetStream stream replication factor, not the number of
	// broker servers. Leave it at 1 for a single-node local broker and set 3
	// only after the peer set exists.
	if err := config.applyBoundedInteger("NATS_STREAM_REPLICAS", 1, 5, &config.Replicas); err != nil {
		return Config{}, err
	}
	if err := config.applyDurations(); err != nil {
		return Config{}, err
	}
	// The publish must finish inside the lease, otherwise another relay could
	// claim the same row while this one is still publishing it — and the lease
	// must still leave a usable window after the safety margin, or every claim
	// would be skipped for lack of time.
	if config.PublishTimeout > config.Lease-leaseSafetyMargin {
		return Config{}, errors.New("OUTBOX_RELAY_PUBLISH_TIMEOUT must leave a publish window inside OUTBOX_RELAY_LEASE")
	}
	return config, nil
}

func defaultRelayOwner() (string, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid()), nil
}

func (c *Config) applyBoundedInteger(name string, minimum, maximum int, target *int) error {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return fmt.Errorf("%s must be an integer in %d..%d", name, minimum, maximum)
	}
	*target = value
	return nil
}

func (c *Config) applyDurations() error {
	for _, item := range []struct {
		name   string
		target *time.Duration
		max    time.Duration
		min    time.Duration
	}{
		{"OUTBOX_RELAY_LEASE", &c.Lease, 5 * time.Minute, leaseSafetyMargin + time.Millisecond},
		{"OUTBOX_RELAY_POLL_INTERVAL", &c.Poll, time.Minute, time.Millisecond},
		{"OUTBOX_RELAY_PUBLISH_TIMEOUT", &c.PublishTimeout, time.Minute, time.Millisecond},
		{"OUTBOX_RELAY_MAX_BACKOFF", &c.MaxBackoff, 10 * time.Minute, time.Millisecond},
	} {
		raw := strings.TrimSpace(os.Getenv(item.name))
		if raw == "" {
			continue
		}
		value, err := time.ParseDuration(raw)
		if err != nil || value < item.min || value > item.max {
			return fmt.Errorf("%s must be a duration in [%s, %s]", item.name, item.min, item.max)
		}
		*item.target = value
	}
	return nil
}

// Enabled reports whether the relay and the wake-up subscription should run.
func (c Config) Enabled() bool { return c.URL != "" }

// Store is the frozen outbox surface the relay depends on.
type Store interface {
	ClaimOutbox(ctx context.Context, owner string, limit int, lease time.Duration) ([]store.OutboxClaim, error)
	AckOutbox(ctx context.Context, claim store.OutboxClaim) error
	ReleaseOutbox(ctx context.Context, claim store.OutboxClaim, reasonCode string, retryAfter time.Duration) error
}

// Broker publishes one message and returns only after the broker has durably
// stored it.
type Broker interface {
	Publish(ctx context.Context, subject, messageID string, payload []byte) error
	Close() error
}

// Relay drains pending outbox rows into the broker, one bounded batch per step.
type Relay struct {
	store   Store
	broker  Broker
	config  Config
	backoff map[string]time.Duration
}

// New validates the relay configuration and its dependencies.
func New(st Store, broker Broker, config Config) (*Relay, error) {
	if st == nil || broker == nil {
		return nil, errors.New("outbox relay requires a store and a broker")
	}
	if !config.Enabled() {
		return nil, errors.New("outbox relay requires a broker URL")
	}
	if !config.valid() {
		return nil, errors.New("outbox relay configuration is incomplete or invalid")
	}
	return &Relay{store: st, broker: broker, config: config, backoff: map[string]time.Duration{}}, nil
}

func (c Config) valid() bool {
	return c.validBatchAndLease() && c.validTimeouts() && c.validRouting()
}

func (c Config) validBatchAndLease() bool {
	return c.Batch >= 1 && c.Lease > leaseSafetyMargin && c.Poll > 0
}

func (c Config) validTimeouts() bool {
	return c.PublishTimeout > 0 && c.MaxBackoff > 0 && c.PublishTimeout <= c.Lease-leaseSafetyMargin
}

func (c Config) validRouting() bool {
	return c.Subject != "" && c.Replicas >= 1 && c.Replicas <= 5
}

// Dialer connects to the broker; the relay keeps retrying it.
type Dialer func(ctx context.Context, config Config) (Broker, error)

// Serve owns the relay's whole lifecycle: it connects with bounded backoff,
// keeps publishing while the broker is up, survives outages without a service
// restart, and closes the broker when the context ends. A broker that appears
// long after startup therefore still drains the pending backlog.
func Serve(ctx context.Context, st Store, config Config, dial Dialer) error {
	delay := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		broker, err := dial(ctx, config)
		if err != nil {
			log.Printf("outbox relay broker unavailable: %v (retrying in %s; PostgreSQL scanning remains the recovery path)", err, delay)
			if err := wait(ctx, delay); err != nil {
				return err
			}
			delay = growRelayBackoff(delay, reconnectBackoff)
			continue
		}
		delay = time.Second
		if err := serveBroker(ctx, st, config, broker); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := wait(ctx, delay); err != nil {
			return err
		}
	}
}

func serveBroker(ctx context.Context, st Store, config Config, broker Broker) error {
	relay, err := New(st, broker, config)
	if err != nil {
		_ = broker.Close()
		return err
	}
	runErr := relay.Run(ctx)
	_ = broker.Close()
	if runErr != nil {
		log.Printf("outbox relay stopped: %v (reconnecting)", runErr)
	}
	return nil
}

func growRelayBackoff(delay, maximum time.Duration) time.Duration {
	if delay *= 2; delay > maximum {
		return maximum
	}
	return delay
}

// Run publishes until the context ends. Every step is independent: a broker
// outage releases the claimed rows with a bounded retry delay and never
// disables the PostgreSQL scan path.
func (r *Relay) Run(ctx context.Context) error {
	log.Printf("outbox relay %s publishing %s to %s", r.config.Owner, r.config.Subject, r.config.Stream)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		published, err := r.Step(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return ctx.Err()
			}
			log.Printf("outbox relay step failed: %v", err)
		}
		if published == 0 {
			if err := wait(ctx, r.config.Poll); err != nil {
				return err
			}
		}
	}
}

// Step publishes at most one claimed batch and reports how many rows the broker
// durably accepted and were then acknowledged.
//
// Every claim's publish deadline is clamped to that claim's own remaining lease:
// a batch can outlive a single timeout, and publishing a row whose lease has
// expired would race the takeover owner. A claim without enough lease left is
// skipped and left to expire; an acknowledge failure is returned instead of
// being counted as a delivered row.
func (r *Relay) Step(ctx context.Context) (int, error) {
	claims, err := r.store.ClaimOutbox(ctx, r.config.Owner, r.config.Batch, r.config.Lease)
	if err != nil {
		return 0, fmt.Errorf("claim outbox: %w", err)
	}
	published := 0
	for _, claim := range claims {
		deadline := r.publishDeadline(claim)
		if deadline <= 0 {
			// Too little lease left to publish safely: leave the row pending for
			// the next lease window instead of racing the takeover owner.
			continue
		}
		payload, err := encodeClaim(claim)
		if err != nil {
			r.release(ctx, claim, "payload_encode_failed")
			continue
		}
		publishCtx, cancel := context.WithTimeout(ctx, deadline)
		// A publish error does not prove the broker stored nothing: the
		// acknowledgement itself can be lost. The stable message id plus the
		// broker's duplicate window keep at-least-once delivery safe, so the row
		// stays pending and is retried with a bounded delay.
		publishErr := r.broker.Publish(publishCtx, r.config.Subject, claim.Event.ID, payload)
		cancel()
		if publishErr != nil {
			r.release(ctx, claim, "broker_unavailable")
			continue
		}
		// The broker acknowledged durably; only now may the row be delivered.
		if err := r.store.AckOutbox(ctx, claim); err != nil {
			if errors.Is(err, store.ErrOutboxClaimLost) {
				continue // another relay owns it; drop the claim silently
			}
			return published, fmt.Errorf("acknowledge outbox %s: %w", claim.Event.ID, err)
		}
		published++
	}
	return published, nil
}

// publishDeadline bounds one publish inside the claim's own lease, leaving a
// safety margin so the row is never published after its lease elapsed.
func (r *Relay) publishDeadline(claim store.OutboxClaim) time.Duration {
	remaining := time.Until(claim.LeaseUntil) - leaseSafetyMargin
	if remaining <= 0 {
		return 0
	}
	if remaining < r.config.PublishTimeout {
		return remaining
	}
	return r.config.PublishTimeout
}

func (r *Relay) release(ctx context.Context, claim store.OutboxClaim, reason string) {
	delay := r.nextBackoff(reason)
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.config.PublishTimeout)
	defer cancel()
	if err := r.store.ReleaseOutbox(releaseCtx, claim, reason, delay); err != nil && !errors.Is(err, store.ErrOutboxClaimLost) {
		log.Printf("outbox relay release failed for %s: %v", claim.Event.ID, err)
	}
}

// nextBackoff doubles per reason up to the configured ceiling, so a broker
// outage backs off while a transient single-row failure retries promptly.
func (r *Relay) nextBackoff(reason string) time.Duration {
	delay := r.backoff[reason]
	if delay == 0 {
		delay = r.config.Poll
	} else {
		delay *= 2
	}
	if delay > r.config.MaxBackoff {
		delay = r.config.MaxBackoff
	}
	r.backoff[reason] = delay
	return delay
}

func encodeClaim(claim store.OutboxClaim) ([]byte, error) {
	// The payload is a notification, never authority: it repeats only the row's
	// own identity and content for the wake-up reader.
	return jsonMarshal(claim.Event)
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
