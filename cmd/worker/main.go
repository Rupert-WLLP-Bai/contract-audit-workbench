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
	"log"
	"log/slog"
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
)

const (
	defaultLease     = 30 * time.Second
	defaultHeartbeat = 10 * time.Second
	defaultPoll      = 250 * time.Millisecond
	shutdownTimeout  = 30 * time.Second
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
	healthFailed := make(chan error, 1)
	go func() {
		err := process.Serve(runCtx, health, shutdownTimeout)
		if err != nil {
			abort()
		}
		healthFailed <- err
	}()

	log.Printf("worker %s claiming jobs from %s", workerID, app.Selection.Database.Name)
	claimErr := claimJobs(runCtx, app.Store, workerID, config.lease, config.heartbeat, config.poll, wake)

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
func claimJobs(ctx context.Context, st *store.Store, workerID string, lease, heartbeat, poll time.Duration, wake <-chan struct{}) error {
	for {
		claim, claimed, err := claimNextJob(ctx, st, workerID, lease, poll, wake)
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		if err := processClaim(ctx, st, claim, lease, heartbeat); err != nil {
			return err
		}
	}
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

func renewLease(ctx context.Context, cancel context.CancelFunc, st *store.Store, claim store.JobClaim, lease, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			updated, err := st.HeartbeatJob(ctx, claim, lease)
			if err != nil {
				cancel()
				return err
			}
			claim = updated
		}
	}
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
