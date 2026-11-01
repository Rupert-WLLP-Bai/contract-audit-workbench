// Package consumer refreshes the process-local projection of PostgreSQL's
// global CLM scalar metrics. Broker notifications are coalesced hints only.
package consumer

import (
	"context"
	"errors"
	"log"
	"time"
)

const (
	DefaultRefreshInterval = 30 * time.Second
	refreshTimeout         = 5 * time.Second
)

// MetricsRefresher is implemented by the Core Store. The scalar snapshot and
// its availability state remain owned by Store, not duplicated in this package.
type MetricsRefresher interface {
	RefreshCLMMetrics(context.Context, *time.Duration) error
}

// Run refreshes immediately, then on coalesced broker wakeups and a periodic
// timer. Nil wakeups are valid and preserve periodic PostgreSQL recovery when
// NATS is disabled or disconnected. Each wake carries no trusted payload data.
func Run(ctx context.Context, refresher MetricsRefresher, agedAfter *time.Duration, interval time.Duration, wakeups <-chan struct{}) error {
	if refresher == nil {
		return errors.New("CLM metrics consumer requires a refresher")
	}
	if interval <= 0 {
		interval = DefaultRefreshInterval
	}
	var threshold *time.Duration
	if agedAfter != nil {
		value := *agedAfter
		threshold = &value
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	refresh := func() {
		refreshCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
		defer cancel()
		if err := refresher.RefreshCLMMetrics(refreshCtx, threshold); err != nil && ctx.Err() == nil {
			log.Printf("CLM metrics snapshot refresh failed: %v", err)
		}
	}
	refresh()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-wakeups:
			if wakeups == nil {
				wakeups = nil
				continue
			}
			if !ok {
				wakeups = nil
				continue
			}
			refresh()
		case <-ticker.C:
			refresh()
		}
	}
}
