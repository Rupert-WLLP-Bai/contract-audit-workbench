package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"strings"

	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

// QueuePolicy is trusted deployment configuration, shared by all execution replicas.
// Weights buy fixed one-job execution units, never estimated model tokens.
type QueuePolicy struct {
	Version        string         `json:"version"`
	GlobalInFlight int            `json:"global_inflight"`
	TenantInFlight int            `json:"tenant_inflight"`
	GlobalPending  int            `json:"global_pending"`
	TenantPending  int            `json:"tenant_pending"`
	MaxClaims      int            `json:"max_claims"`
	MaxAgeSeconds  int            `json:"max_age_seconds"`
	BackoffBaseMS  int            `json:"backoff_base_ms"`
	BackoffMaxMS   int            `json:"backoff_max_ms"`
	Weights        map[string]int `json:"weights"`
}

type queueConfiguration struct {
	QueuePolicy
	encoded string
	hash    string
}

// DefaultQueuePolicy returns the bounded policy for a single deployment.
func DefaultQueuePolicy() QueuePolicy {
	return QueuePolicy{Version: "drr-v1", GlobalInFlight: 8, TenantInFlight: 2,
		GlobalPending: 256, TenantPending: 64, MaxClaims: 3, MaxAgeSeconds: 900,
		BackoffBaseMS: 1000, BackoffMaxMS: 30000}
}

var defaultQueueConfiguration = newQueueConfiguration(DefaultQueuePolicy())

func newQueueConfiguration(policy QueuePolicy) *queueConfiguration {
	policy.Weights = maps.Clone(policy.Weights)
	encoded, _ := json.Marshal(policy)
	digest := sha256.Sum256(encoded)
	return &queueConfiguration{QueuePolicy: policy, encoded: string(encoded), hash: hex.EncodeToString(digest[:])}
}

// UseQueuePolicy must be called during assembly, before serving or claiming jobs.
func (s *Store) UseQueuePolicy(policy QueuePolicy) error {
	if strings.TrimSpace(policy.Version) == "" || len(policy.Version) > 80 ||
		policy.GlobalInFlight < 1 || policy.GlobalInFlight > 1024 || policy.TenantInFlight < 1 || policy.TenantInFlight > policy.GlobalInFlight ||
		policy.GlobalPending < policy.GlobalInFlight || policy.GlobalPending > 100000 || policy.TenantPending < policy.TenantInFlight || policy.TenantPending > policy.GlobalPending ||
		policy.MaxClaims < 1 || policy.MaxClaims > 16 || policy.MaxAgeSeconds < 1 || policy.MaxAgeSeconds > 86400 ||
		policy.BackoffBaseMS < 1 || policy.BackoffMaxMS < policy.BackoffBaseMS || policy.BackoffMaxMS > 60000 || len(policy.Weights) > 1000 {
		return errors.New("invalid execution queue policy")
	}
	for tenant, weight := range policy.Weights {
		if strings.TrimSpace(tenant) != tenant || tenant == "" || len(tenant) > 200 || weight < 1 || weight > 100 {
			return errors.New("invalid execution tenant weight")
		}
	}
	s.queuePolicy = newQueueConfiguration(policy)
	return nil
}

func (s *Store) executionQueuePolicy() *queueConfiguration {
	if s.queuePolicy != nil {
		return s.queuePolicy
	}
	return defaultQueueConfiguration
}

const queueSchedulerSchemaSQL = `
CREATE TABLE IF NOT EXISTS execution_queue_policies (
	tenant_id text NOT NULL DEFAULT '__scheduler__',
	policy_hash text PRIMARY KEY,
	policy_json text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS execution_queue_cursor (
	tenant_id text PRIMARY KEY CHECK (tenant_id='__scheduler__'),
	policy_hash text NOT NULL REFERENCES execution_queue_policies(policy_hash),
	last_tenant text NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS execution_queue_tenants (
	tenant_id text PRIMARY KEY,
	deficit integer NOT NULL DEFAULT 0 CHECK (deficit >= 0)
);
ALTER TABLE worker_jobs ADD COLUMN IF NOT EXISTS policy_hash text NOT NULL DEFAULT '';
ALTER TABLE worker_jobs ADD COLUMN IF NOT EXISTS claim_count integer NOT NULL DEFAULT 0;
ALTER TABLE worker_jobs ADD COLUMN IF NOT EXISTS max_claims integer NOT NULL DEFAULT 3;
ALTER TABLE worker_jobs ADD COLUMN IF NOT EXISTS deadline_at timestamptz NOT NULL DEFAULT (clock_timestamp()+interval '15 minutes');
ALTER TABLE worker_jobs ADD COLUMN IF NOT EXISTS ready_at timestamptz NOT NULL DEFAULT to_timestamp(0);
CREATE INDEX IF NOT EXISTS worker_jobs_tenant_ready ON worker_jobs (tenant_id,ready_at,created_at,id) WHERE state IN ('queued','running');
`

// lockQueue serializes only short admission/claim transactions. It is never held
// during a tool/model call. Existing policy history is retained on a drained cutover.
func lockQueue(ctx context.Context, tx pgx.Tx, cfg *queueConfiguration) (string, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':execution-queue',0))`); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO execution_queue_policies(policy_hash,policy_json) VALUES($1,$2) ON CONFLICT DO NOTHING`, cfg.hash, cfg.encoded); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO execution_queue_cursor(tenant_id,policy_hash) VALUES('__scheduler__',$1) ON CONFLICT DO NOTHING`, cfg.hash); err != nil {
		return "", err
	}
	var hash, cursor string
	if err := tx.QueryRow(ctx, `SELECT policy_hash,last_tenant FROM execution_queue_cursor WHERE tenant_id='__scheduler__' FOR UPDATE`).Scan(&hash, &cursor); err != nil {
		return "", err
	}
	if hash != cfg.hash {
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM worker_jobs WHERE state IN ('queued','running'))`).Scan(&active); err != nil {
			return "", err
		}
		if active {
			return "", &fault.Error{Status: 503, Code: "queue_policy_mismatch", Message: "执行队列策略与活动任务不一致", Retryable: true}
		}
		if _, err := tx.Exec(ctx, `UPDATE execution_queue_cursor SET policy_hash=$1,last_tenant='' WHERE tenant_id='__scheduler__'`, cfg.hash); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE execution_queue_tenants SET deficit=0`); err != nil {
			return "", err
		}
		cursor = ""
	}
	return cursor, nil
}

// admitNewJobs runs after the job inserts but before their transaction commits.
// The cross-tenant scope is restricted to scheduler/job metadata and restored
// before returning; business reads keep their original tenant context.
func (s *Store) admitNewJobs(ctx context.Context, tx pgx.Tx, old, changed []Job) error {
	for _, item := range changed {
		existing := false
		for _, prior := range old {
			if prior.ID == item.ID {
				existing = true
				break
			}
		}
		if existing {
			continue
		}
		var role string
		if err := tx.QueryRow(ctx, `SELECT COALESCE(current_setting('app.actor_role',true),'')`).Scan(&role); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.actor_role','worker',true)`); err != nil {
			return err
		}
		cfg := s.executionQueuePolicy()
		if _, err := lockQueue(ctx, tx, cfg); err != nil {
			return err
		}
		var global, tenant int
		if err := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE tenant_id=$1) FROM worker_jobs WHERE state IN ('queued','running')`, item.TenantID).Scan(&global, &tenant); err != nil {
			return err
		}
		if global > cfg.GlobalPending || tenant > cfg.TenantPending {
			s.Telemetry().RecordQuotaRejection("queue_capacity")
			return &fault.Error{Status: 503, Code: "execution_capacity", Message: "审查队列容量已满，请稍后重试", Retryable: true}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO execution_queue_tenants(tenant_id) VALUES($1) ON CONFLICT DO NOTHING`, item.TenantID); err != nil {
			return err
		}
		// Give newly admitted review jobs a PG readiness anchor without changing
		// an explicit future schedule or backfilling existing jobs.
		if _, err := tx.Exec(ctx, `UPDATE worker_jobs SET policy_hash=$2,max_claims=$3,deadline_at=clock_timestamp()+($4::int*interval '1 second'),
			ready_at=CASE WHEN kind='review' AND ready_at=to_timestamp(0) THEN clock_timestamp() ELSE ready_at END WHERE id=$1`, item.ID, cfg.hash, cfg.MaxClaims, cfg.MaxAgeSeconds); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.actor_role',$1,true)`, role); err != nil {
			return err
		}
	}
	return nil
}
