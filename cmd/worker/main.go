// Command worker runs the independently deployed audit worker.
//
// The worker owns the execution database identity through the execution
// service role and claims persistent review jobs from PostgreSQL. It holds the
// model gateway and document service caller credentials because it performs the
// attempt, while the health surface stays local: liveness reports the process
// itself, readiness reports the database plus whether this replica is still
// accepting work, and no probe calls a model or the document service. An idle
// worker that can still accept jobs is ready.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"contractaudit/internal/bootstrap"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/outboxrelay"
	"contractaudit/internal/store"

	"contractaudit/internal/process"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	defaultLease     = 30 * time.Second
	defaultHeartbeat = 10 * time.Second
	defaultPoll      = 250 * time.Millisecond
	shutdownTimeout  = 30 * time.Second
	// Claim failures retry on a bounded exponential delay: a PostgreSQL outage
	// must not turn every replica into a hot loop, while a broker wake-up still
	// cuts the wait short. A successful claim resets the delay.
	claimRetryFloor = time.Second
	claimRetryCeil  = 30 * time.Second
	// heartbeatCallFloor keeps one heartbeat round-trip viable no matter how
	// short the pacing interval is configured.
	heartbeatCallFloor = 2 * time.Second
)

type workerRuntimeConfig struct {
	lease     time.Duration
	heartbeat time.Duration
	poll      time.Duration
	relay     outboxrelay.Config
}

func main() {
	process.Main(run)
}

func run() error {
	ctx, stop := process.SignalContext()
	defer stop()
	runCtx, abort := context.WithCancel(ctx)
	defer abort()

	healthAddr, err := requiredEnv("HEALTH_ADDR")
	if err != nil {
		return err
	}
	app, err := bootstrap.OpenService(ctx, "worker")
	if err != nil {
		return err
	}
	defer app.Close()
	if !app.Durable {
		return errors.New("the worker requires durable execution")
	}
	workerID, err := configuredWorkerID()
	if err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})).With(
		"service", "audit-worker", "service.instance.id", bootstrap.InstanceID(), "worker_id", workerID,
	))
	slog.Info("worker starting", "database", app.Selection.Database.Name)
	config, err := workerRuntimeConfigFromEnv()
	if err != nil {
		return err
	}
	return serveWorker(runCtx, abort, app, healthAddr, workerID, config)
}

func workerRuntimeConfigFromEnv() (workerRuntimeConfig, error) {
	lease, err := durationEnv("WORKER_LEASE_DURATION", defaultLease)
	if err != nil {
		return workerRuntimeConfig{}, err
	}
	heartbeat, err := durationEnv("WORKER_HEARTBEAT_INTERVAL", defaultHeartbeat)
	if err != nil || heartbeat >= lease {
		return workerRuntimeConfig{}, errors.New("WORKER_HEARTBEAT_INTERVAL must be positive and shorter than WORKER_LEASE_DURATION")
	}
	poll, err := durationEnv("WORKER_POLL_INTERVAL", defaultPoll)
	if err != nil {
		return workerRuntimeConfig{}, err
	}
	relay, err := outboxrelay.FromEnv()
	if err != nil {
		return workerRuntimeConfig{}, err
	}
	return workerRuntimeConfig{lease: lease, heartbeat: heartbeat, poll: poll, relay: relay}, nil
}

func serveWorker(runCtx context.Context, abort context.CancelFunc, app *bootstrap.Runtime, healthAddr, workerID string, config workerRuntimeConfig) error {
	// NATS only wakes an idle worker. Jobs are always claimed from PostgreSQL,
	// so a missed notification cannot lose work.
	// SubscribeLoop keeps retrying the broker in the background; a nil channel
	// means wake-ups are disabled and the claim loop polls PostgreSQL.
	wake := outboxrelay.SubscribeLoop(runCtx, config.relay)
	if wake == nil {
		log.Printf("worker wake-up disabled: NATS_URL is not configured; polling PostgreSQL directly")
	}

	// Readiness flips to false the moment a shutdown starts, so the orchestrator
	// removes this replica from rotation before the claim loop stops.
	accepting := &atomic.Bool{}
	accepting.Store(true)
	context.AfterFunc(runCtx, func() { accepting.Store(false) })
	mux := http.NewServeMux()
	httpapi.MountHealth(mux, func(ctx context.Context) error {
		if !accepting.Load() {
			return errors.New("worker is shutting down")
		}
		return app.Store.Ready(ctx)
	})
	health := &http.Server{
		Addr: healthAddr, Handler: mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	// The probe surface is the only way an orchestrator can observe this
	// process, so a listener failure must end the process instead of leaving an
	// unobservable worker running.
	healthCtx, stopHealth := context.WithCancel(runCtx)
	defer stopHealth()
	healthFailed := make(chan error, 1)
	go func() {
		err := process.Serve(healthCtx, health, shutdownTimeout)
		if err != nil {
			abort()
		}
		healthFailed <- err
	}()

	log.Printf("worker %s claiming jobs from %s", workerID, app.Selection.Database.Name)
	claimErr := claimJobs(runCtx, app.Store, workerID, config.lease, config.heartbeat, config.poll, wake)
	stopHealth()

	if healthErr := <-healthFailed; healthErr != nil {
		return fmt.Errorf("worker health server: %w", healthErr)
	}
	if claimErr != nil && !errors.Is(claimErr, context.Canceled) {
		return claimErr
	}
	return nil
}

// claimJobs processes persistent review jobs until ctx is canceled. A canceled
// attempt is left to the lease: another replica reclaims the job after the
// lease expires, and the lease generation rejects a late commit from this one.
// A transient claim failure — a PostgreSQL restart, a dropped connection, a
// saturated pool — backs off with bounded jitter and retries instead of ending
// the process; configuration and constraint errors still stop the worker so an
// operator sees them.
func claimJobs(ctx context.Context, st *store.Store, workerID string, lease, heartbeat, poll time.Duration, wake <-chan struct{}) error {
	retry := time.Duration(0)
	for {
		claim, claimed, err := claimNextJob(ctx, st, workerID, lease, poll, wake)
		if err != nil {
			if ctx.Err() != nil || !transientClaimError(err) {
				return err
			}
			retry = growClaimRetry(retry)
			slog.Warn("worker claim failed; retrying", "error", err, "retry_in", retry)
			// The wake channel also interrupts the retry delay: new work must
			// not wait out a backoff that a database blip started.
			if err := waitFor(ctx, retry, wake); err != nil {
				return err
			}
			continue
		}
		retry = 0
		if !claimed {
			continue
		}
		if err := processClaim(ctx, st, claim, lease, heartbeat); err != nil {
			return err
		}
	}
}

// growClaimRetry doubles the retry delay inside [claimRetryFloor,
// claimRetryCeil] and jitters the upper half, so replicas recovering from the
// same database outage do not retry in a synchronized wave. The delay only
// resets on a successful claim, never on a wake-up.
func growClaimRetry(previous time.Duration) time.Duration {
	delay := max(2*previous, claimRetryFloor)
	delay = min(delay, claimRetryCeil)
	return delay/2 + rand.N(delay/2+1)
}

// transientClaimError reports whether a failed claim can be retried without
// losing work: connection loss, server-side capacity and serialization errors
// recover on their own, while anything else stops the worker so a permanent
// misconfiguration cannot hide behind retries.
func transientClaimError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if pgconn.SafeToRetry(err) || pgconn.Timeout(err) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && len(pgErr.Code) >= 2 {
		switch pgErr.Code[:2] {
		case "08", "40", "53", "57":
			// Connection loss, serialization and deadlock retries, insufficient
			// resources, and operator intervention. A query cancellation that
			// this process did not request is a server-side timeout.
			return true
		}
		return false
	}
	var netErr net.Error
	return errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary())
}

func claimNextJob(ctx context.Context, st *store.Store, workerID string, lease, poll time.Duration, wake <-chan struct{}) (store.JobClaim, bool, error) {
	claim, err := st.ClaimNextJob(ctx, workerID, lease)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := waitFor(ctx, poll, wake); err != nil {
			return store.JobClaim{}, false, err
		}
		return store.JobClaim{}, false, nil
	}
	if err != nil {
		return store.JobClaim{}, false, err
	}
	slog.Info("review job claimed", "job_id", claim.JobID, "run_id", claim.RunID,
		"attempt_id", claim.AttemptID, "generation", claim.Generation)
	return claim, true, nil
}

func processClaim(ctx context.Context, st *store.Store, claim store.JobClaim, lease, heartbeat time.Duration) error {
	jobCtx, cancel := context.WithCancel(ctx)
	heartbeatDone := make(chan error, 1)
	go func() { heartbeatDone <- renewLease(jobCtx, cancel, st, claim, lease, heartbeat) }()
	runErr := st.RunClaimedJob(jobCtx, claim)
	cancel()
	heartbeatErr := <-heartbeatDone
	if ctx.Err() != nil {
		return ctx.Err()
	}
	logClaimOutcome(claim, runErr, heartbeatErr)
	return nil
}

func logClaimOutcome(claim store.JobClaim, runErr, heartbeatErr error) {
	if unexpectedWorkerError(runErr) {
		slog.Error("review job failed", "job_id", claim.JobID, "run_id", claim.RunID,
			"attempt_id", claim.AttemptID, "generation", claim.Generation, "error", runErr)
	} else {
		slog.Info("review job finished", "job_id", claim.JobID, "run_id", claim.RunID,
			"attempt_id", claim.AttemptID, "generation", claim.Generation)
	}
	if unexpectedWorkerError(heartbeatErr) {
		slog.Error("review job heartbeat failed", "job_id", claim.JobID, "run_id", claim.RunID,
			"attempt_id", claim.AttemptID, "generation", claim.Generation, "error", heartbeatErr)
	}
}

func unexpectedWorkerError(err error) bool {
	return err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, store.ErrLeaseLost)
}

// renewLease keeps the claim's lease alive until the job context ends. The
// PostgreSQL row stays the write authority: a heartbeat that lost the lease
// cancels the job immediately, and a transient heartbeat failure only retries
// while the lease is still valid — the moment not enough lease remains for the
// next heartbeat, the job stops so it can never commit against an expired
// lease.
func renewLease(ctx context.Context, cancel context.CancelFunc, st *store.Store, claim store.JobClaim, lease, interval time.Duration) error {
	margin := leaseHeartbeatMargin(interval)
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		// Once the remaining lease cannot cover the next heartbeat, the job
		// stops so it can never commit against an expired lease. A claim
		// without a lease deadline cannot be clocked locally; the heartbeat
		// result and the PostgreSQL generation check stay the only authority.
		hasClock := !claim.LeaseUntil.IsZero()
		var remaining time.Duration
		if hasClock {
			remaining = time.Until(claim.LeaseUntil)
			if remaining <= margin {
				cancel()
				return fmt.Errorf("job lease cannot be renewed before it expires (remaining %s)", remaining)
			}
		}
		// The call floor keeps a real database round-trip viable even when the
		// pacing interval is far shorter: a hung heartbeat must still return so
		// the margin check below can stop the job, and PostgreSQL rejects any
		// commit that arrives after the lease lapsed anyway.
		callBudget := interval
		if hasClock {
			callBudget = min(interval, remaining-margin)
		}
		callCtx, cancelCall := context.WithTimeout(ctx, max(callBudget, heartbeatCallFloor))
		updated, err := st.HeartbeatJob(callCtx, claim, lease)
		cancelCall()
		if err == nil {
			claim = updated
			resetRenewTimer(timer, interval)
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, store.ErrLeaseLost) {
			cancel()
			return err
		}
		// Transient failure: the lease is still valid, so retry sooner than a
		// full interval but always before the job must stop.
		retry := interval
		if hasClock {
			retry = max(min(interval, remaining-margin), time.Millisecond)
		}
		slog.Warn("review job heartbeat failed; retrying within the lease",
			"job_id", claim.JobID, "run_id", claim.RunID, "attempt_id", claim.AttemptID,
			"generation", claim.Generation, "lease_remaining", remaining, "retry_in", retry, "error", err)
		resetRenewTimer(timer, retry)
	}
}

func resetRenewTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}

// leaseHeartbeatMargin is how much unexpired lease the job keeps in reserve
// before it stops: it covers local clock skew against the PostgreSQL clock and
// the tail of the final commit. It stays a fraction of the heartbeat interval,
// floored so a millisecond interval cannot spin, and capped so a healthy lease
// keeps almost all of its window.
func leaseHeartbeatMargin(interval time.Duration) time.Duration {
	margin := min(interval/2, 2*time.Second)
	return max(margin, 100*time.Millisecond)
}

func configuredWorkerID() (string, error) {
	if id := strings.TrimSpace(os.Getenv("WORKER_ID")); id != "" {
		return id, nil
	}
	host, err := os.Hostname()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid()), nil
}

func requiredEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", errors.New(name + " is required to expose the worker health surface")
	}
	return value, nil
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", name)
	}
	return value, nil
}

// waitFor sleeps for the poll interval or until a broker wake-up arrives. The
// notification is advisory: the next iteration still claims through PostgreSQL,
// and a nil wake channel keeps the plain poll interval.
func waitFor(ctx context.Context, duration time.Duration, wake <-chan struct{}) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	case <-wake:
		return nil
	}
}
