package store

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/telemetry"
)

// This exercises the actual worker role and the collector-facing counts, not a
// tenant-scoped projection or a substitute in-memory queue.
func TestRuntimeMetricsWorkerScopeAndCommittedReclaim(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	users := queueTestActors(t)
	core, document := env.openRole(t, "core"), env.openRole(t, "document")
	execution, worker := env.openRole(t, "execution"), env.openRole(t, "worker")
	policy := DefaultQueuePolicy()
	policy.GlobalInFlight = 2
	policy.TenantInFlight = 1
	policy.GlobalPending, policy.TenantPending = 4, 2
	for _, st := range []*Store{execution, worker} {
		if err := st.UseQueuePolicy(policy); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range users {
		pack := publishSyntheticAcceptancePack(t, core, user)
		publishSyntheticAcceptancePolicy(t, core, user, pack)
		for range 2 {
			if _, _, err := queueTestAcceptance(t, core, document, execution, user); err != nil {
				t.Fatal(err)
			}
		}
	}
	payloads := make(chan []byte, 16)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		payloads <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer collector.Close()
	exporter, err := telemetry.New(telemetry.Config{Endpoint: collector.URL, Service: "audit.worker", SampleRatio: 0})
	if err != nil {
		t.Fatal(err)
	}
	worker.UseTelemetry(exporter)
	admissionExporter, err := telemetry.New(telemetry.Config{Endpoint: collector.URL, Service: "audit.execution", SampleRatio: 0})
	if err != nil {
		t.Fatal(err)
	}
	execution.UseTelemetry(admissionExporter)
	_, _, rejected := queueTestAcceptance(t, core, document, execution, users[0])
	var capacity *fault.Error
	if !errors.As(rejected, &capacity) || capacity.Code != "execution_capacity" {
		t.Fatalf("full queue did not reject admission: %v", rejected)
	}
	execution.UseTelemetry(nil)
	ctx := context.Background()
	claim, err := worker.ClaimNextJob(ctx, "runtime-metrics-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.harness.pool.Exec(ctx, `UPDATE outbox_events SET created_at=(clock_timestamp()-interval '2 minutes')::text WHERE state='pending'`); err != nil {
		t.Fatal(err)
	}
	snapshot := worker.runtimeMetrics(ctx)
	if snapshot.QueueFailure != "" || snapshot.OutboxFailure != "" || !snapshot.QueueWaitAvailable || !snapshot.OutboxAgeAvailable || !snapshot.PoolAvailable || !snapshot.QueueObserved || snapshot.Queued != 3 || snapshot.Running != 1 || snapshot.Ready != 1 || snapshot.OutboxPending != 4 || snapshot.OutboxOldestMS < 60000 {
		t.Fatalf("unexpected worker queue snapshot: %+v", snapshot)
	}
	var legacyJob string
	if err := env.harness.pool.QueryRow(ctx, `UPDATE worker_jobs SET ready_at=to_timestamp(0)
		WHERE id=(SELECT id FROM worker_jobs WHERE state='queued' ORDER BY id LIMIT 1) RETURNING id`).Scan(&legacyJob); err != nil {
		t.Fatal(err)
	}
	if legacy := worker.runtimeMetrics(ctx); legacy.QueueFailure != "" || legacy.QueueWaitAvailable || legacy.Queued != 3 || legacy.Ready != 1 {
		t.Fatalf("legacy wait timestamp changed the queue count or fabricated an age: %+v", legacy)
	}
	if _, err := env.harness.pool.Exec(ctx, `UPDATE worker_jobs SET ready_at=clock_timestamp() WHERE id=$1`, legacyJob); err != nil {
		t.Fatal(err)
	}
	var outboxID string
	if err := env.harness.pool.QueryRow(ctx, `UPDATE outbox_events SET created_at='invalid'
		WHERE id=(SELECT id FROM outbox_events WHERE state='pending' ORDER BY id LIMIT 1) RETURNING id`).Scan(&outboxID); err != nil {
		t.Fatal(err)
	}
	if invalid := worker.runtimeMetrics(ctx); invalid.OutboxFailure != "" || invalid.OutboxAgeAvailable || invalid.OutboxPending != 4 || invalid.QueueFailure != "" {
		t.Fatalf("invalid outbox age hid the pending count or queue: %+v", invalid)
	}
	if _, err := env.harness.pool.Exec(ctx, `UPDATE outbox_events SET created_at=(clock_timestamp()-interval '2 minutes')::text WHERE id=$1`, outboxID); err != nil {
		t.Fatal(err)
	}
	if nonWorker := execution.runtimeMetrics(ctx); nonWorker.QueueObserved || !nonWorker.PoolAvailable {
		t.Fatalf("execution service obtained the worker queue scope: %+v", nonWorker)
	}
	if _, err := env.harness.pool.Exec(ctx, `UPDATE worker_jobs SET ready_at=clock_timestamp()+interval '1 hour' WHERE state='queued' AND tenant_id<>$1`, claim.TenantID); err != nil {
		t.Fatal(err)
	}
	claim, err = worker.HeartbeatJob(ctx, claim, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	snapshot = worker.runtimeMetrics(ctx)
	if snapshot.QueueFailure != "" || snapshot.Queued != 3 || snapshot.Running != 1 || snapshot.Ready != 0 {
		t.Fatalf("backoff or tenant capacity counted as ready: %+v", snapshot)
	}
	if _, err := env.harness.pool.Exec(ctx, `UPDATE worker_jobs SET ready_at=clock_timestamp()+interval '1 hour' WHERE state='queued'`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.harness.pool.Exec(ctx, `UPDATE worker_jobs SET lease_until=clock_timestamp()-interval '2 seconds',ready_at=clock_timestamp()-interval '1 second' WHERE id=$1`, claim.JobID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := worker.ClaimNextJob(ctx, "runtime-metrics-worker", time.Minute)
	if err != nil || reclaimed.JobID != claim.JobID || reclaimed.Generation <= claim.Generation {
		t.Fatalf("expired lease was not reclaimed: %+v, %v", reclaimed, err)
	}
	if err := worker.finishClaim(ctx, claim, "failed"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("superseded claim committed a terminal transition: %v", err)
	}
	if err := worker.finishClaim(ctx, reclaimed, "failed"); err != nil {
		t.Fatalf("live claim did not commit its terminal transition: %v", err)
	}
	if _, err := env.harness.pool.Exec(ctx, `UPDATE worker_jobs SET ready_at=to_timestamp(0) WHERE id=$1`, legacyJob); err != nil {
		t.Fatal(err)
	}
	legacyClaim, err := worker.ClaimNextJob(ctx, "runtime-metrics-worker", time.Minute)
	if err != nil || legacyClaim.JobID != legacyJob {
		t.Fatalf("legacy queued job was not claimed: %+v, %v", legacyClaim, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if unavailable := worker.runtimeMetrics(canceled); !unavailable.PoolAvailable || !unavailable.QueueObserved || unavailable.QueueFailure != "timeout" {
		t.Fatalf("failed queue read was reported as usable: %+v", unavailable)
	}
	// An application owner with the wrong database role must fail before the
	// cross-tenant worker scope is installed, even if its process is mislabeled.
	for _, role := range []struct {
		name  string
		store *Store
	}{
		{"core", core}, {"document", document}, {"model", env.openRole(t, "model")}, {"tool", env.openRole(t, "tool")},
	} {
		if err := role.store.UseServiceOwner("worker"); err != nil {
			t.Fatal(err)
		}
		role.store.metricWorkerID.Store("runtime-metrics-worker")
		if forbidden := role.store.runtimeMetrics(ctx); !forbidden.PoolAvailable || forbidden.QueueFailure != "role" {
			t.Fatalf("%s database role read the global queue: %+v", role.name, forbidden)
		}
	}
	worker.UseTelemetry(nil)
	found := false
	quotaFound := false
	for len(payloads) > 0 {
		var payload struct {
			Resources []struct {
				Scopes []struct {
					Metrics []struct {
						Name string `json:"name"`
						Unit string `json:"unit"`
						Sum  *struct {
							Points []struct {
								Value      string `json:"asInt"`
								Attributes []struct {
									Key   string `json:"key"`
									Value struct {
										String string `json:"stringValue"`
									} `json:"value"`
								} `json:"attributes"`
							} `json:"dataPoints"`
						} `json:"sum"`
						Histogram *struct {
							Temporality int `json:"aggregationTemporality"`
							Points      []struct {
								Count   string    `json:"count"`
								Sum     float64   `json:"sum"`
								Buckets []string  `json:"bucketCounts"`
								Bounds  []float64 `json:"explicitBounds"`
							} `json:"dataPoints"`
						} `json:"histogram"`
					} `json:"metrics"`
				} `json:"scopeMetrics"`
			} `json:"resourceMetrics"`
		}
		if err := json.Unmarshal(<-payloads, &payload); err != nil {
			t.Fatal(err)
		}
		values := map[string]string{}
		histograms := map[string]string{}
		for _, resource := range payload.Resources {
			for _, scope := range resource.Scopes {
				for _, metric := range scope.Metrics {
					if metric.Sum != nil && len(metric.Sum.Points) == 1 {
						values[metric.Name] = metric.Sum.Points[0].Value
					}
					if metric.Name == "audit.quota.rejections" && metric.Sum != nil {
						for _, point := range metric.Sum.Points {
							for _, attr := range point.Attributes {
								if attr.Key == "reason" && attr.Value.String == "queue_capacity" && point.Value == "1" {
									quotaFound = true
								}
							}
						}
					}
					if metric.Histogram != nil && len(metric.Histogram.Points) == 1 {
						point := metric.Histogram.Points[0]
						if metric.Unit != "ms" || metric.Histogram.Temporality != 2 || point.Sum < 0 || len(point.Bounds) != 20 || len(point.Buckets) != 21 {
							t.Fatalf("invalid duration histogram: %+v", metric)
						}
						var total uint64
						for _, bucket := range point.Buckets {
							value, err := strconv.ParseUint(bucket, 10, 64)
							if err != nil {
								t.Fatal(err)
							}
							total += value
						}
						if strconv.FormatUint(total, 10) != point.Count {
							t.Fatalf("histogram buckets differ from count: %+v", point)
						}
						histograms[metric.Name] = point.Count
					}
				}
			}
		}
		if values["audit.queue.claims"] == "3" && values["audit.queue.reclaims"] == "1" &&
			histograms["audit.queue.wait_duration_ms"] == "2" && histograms["audit.queue.execution_duration_ms"] == "1" {
			found = true
		}
	}
	if !found {
		t.Fatal("collector lost committed claims, reclaim, or fenced duration samples")
	}
	if !quotaFound {
		t.Fatal("collector did not observe the actual queue capacity rejection")
	}
}
