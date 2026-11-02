package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
)

const modelBudgetSchemaSQL = `
CREATE TABLE IF NOT EXISTS model_call_leases (
	lease_id text PRIMARY KEY,
	tenant_id text NOT NULL,
	actor_id text NOT NULL,
	case_id text NOT NULL,
	run_id text NOT NULL,
	attempt_id text NOT NULL,
	caller_identity text NOT NULL CHECK (caller_identity IN ('worker','document')),
	model_ref text NOT NULL,
	attempt_number integer NOT NULL CHECK (attempt_number > 0),
	reserved_input_tokens bigint NOT NULL CHECK (reserved_input_tokens >= 0),
	reserved_output_tokens bigint NOT NULL CHECK (reserved_output_tokens >= 0),
	acquired_at timestamptz NOT NULL,
	expires_at timestamptz NOT NULL
);
ALTER TABLE model_call_leases ADD COLUMN IF NOT EXISTS attempt_number integer NOT NULL DEFAULT 1;
ALTER TABLE model_call_leases ADD COLUMN IF NOT EXISTS reserved_input_tokens bigint NOT NULL DEFAULT 0;
ALTER TABLE model_call_leases ADD COLUMN IF NOT EXISTS purpose text NOT NULL DEFAULT 'review';
ALTER TABLE model_call_leases ADD COLUMN IF NOT EXISTS job_id text NOT NULL DEFAULT '';
ALTER TABLE model_call_leases ADD COLUMN IF NOT EXISTS job_generation bigint NOT NULL DEFAULT 0;
ALTER TABLE model_call_leases ADD COLUMN IF NOT EXISTS input_sha256 text NOT NULL DEFAULT '';
ALTER TABLE model_call_leases ADD COLUMN IF NOT EXISTS reserved_output_tokens bigint NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS model_call_leases_expiry ON model_call_leases (expires_at);
CREATE INDEX IF NOT EXISTS model_call_leases_tenant_expiry ON model_call_leases (tenant_id, expires_at);
CREATE INDEX IF NOT EXISTS model_call_leases_acquired ON model_call_leases (acquired_at);
CREATE TABLE IF NOT EXISTS model_usage (
	call_id text PRIMARY KEY,
	lease_id text NOT NULL UNIQUE,
	tenant_id text NOT NULL,
	actor_id text NOT NULL,
	case_id text NOT NULL,
	run_id text NOT NULL,
	attempt_id text NOT NULL,
	caller_identity text NOT NULL CHECK (caller_identity IN ('worker','document')),
	model_ref text NOT NULL,
	attempt_number integer NOT NULL,
	call_status text NOT NULL,
	error_code text NOT NULL DEFAULT '',
	usage_source text,
	input_tokens bigint,
	output_tokens bigint,
	total_tokens bigint,
	occurred_at timestamptz NOT NULL,
	reserved_at timestamptz,
	reserved_input_tokens bigint,
	reserved_output_tokens bigint,
	charged_tokens bigint,
	budget_released boolean NOT NULL DEFAULT false,
	settlement_fingerprint text
);
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS reserved_at timestamptz;
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS reserved_input_tokens bigint;
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS reserved_output_tokens bigint;
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS charged_tokens bigint;
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS budget_released boolean NOT NULL DEFAULT false;
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS settlement_fingerprint text;
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS purpose text NOT NULL DEFAULT 'review';
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS job_id text NOT NULL DEFAULT '';
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS job_generation bigint NOT NULL DEFAULT 0;
ALTER TABLE model_usage ADD COLUMN IF NOT EXISTS input_sha256 text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS model_usage_occurred ON model_usage (occurred_at);
CREATE INDEX IF NOT EXISTS model_usage_tenant_occurred ON model_usage (tenant_id, occurred_at);
CREATE INDEX IF NOT EXISTS model_usage_reserved_at ON model_usage (reserved_at);
`

var ErrModelBudgetExceeded = errors.New("model call budget is exhausted")
var ErrModelScopeUnavailable = errors.New("current model execution scope is unavailable")
var ErrModelConfigurationChanged = errors.New("frozen model configuration no longer matches the provider")
var ErrModelCallSettlementConflict = errors.New("model call settlement conflicts with its first result")

type ModelBudgetLimits struct {
	GlobalMaxInFlight         int
	TenantMaxInFlight         int
	GlobalCallsPerMinute      int
	TenantCallsPerMinute      int
	GlobalTokensPerMinute     int64
	TenantTokensPerMinute     int64
	MaxInputTokenReservation  int64
	MaxOutputTokenReservation int64
	LeaseDuration             time.Duration
}

type ModelCallLease struct {
	ID             string
	TenantID       string
	ActorID        string
	CaseID         string
	RunID          string
	AttemptID      string
	CallerIdentity string
	ModelRef       string
	AttemptNumber  int
	Purpose        string
	JobID          string
	Generation     int64
	InputSHA256    string
}

type ModelCallResult struct {
	Status             string
	ErrorCode          string
	Usage              *model.TokenUsage
	PreDispatchFailure bool
	OccurredAt         time.Time
}

// ReserveModelCall validates the delegated identity and reserves one physical
// call, its conservative token upper bound, and global and tenant quotas in one
// PostgreSQL transaction.
func (s *Store) ReserveModelCall(ctx context.Context, caller string, scope model.DelegatedScope, modelRef string, attemptNumber int, inputUpperBound, outputUpperBound int64, limits ModelBudgetLimits) (ModelCallLease, error) {
	return s.reserveModelCall(ctx, caller, scope, modelRef, attemptNumber, inputUpperBound, outputUpperBound, limits, nil)
}

// ReserveModelChatCall binds a chat request to the server-selected provider.
// Unlike advisory calls, chat execution and distillation have an authoritative
// immutable configuration identity. Caller-supplied identity fields are not
// consulted. Standalone document reparses have no frozen configuration record.
func (s *Store) ReserveModelChatCall(ctx context.Context, caller string, scope model.DelegatedScope, modelRef string, attemptNumber int, inputUpperBound, outputUpperBound int64, limits ModelBudgetLimits, providerIdentity string) (ModelCallLease, error) {
	return s.reserveModelCall(ctx, caller, scope, modelRef, attemptNumber, inputUpperBound, outputUpperBound, limits, &providerIdentity)
}

func (s *Store) reserveModelCall(ctx context.Context, caller string, scope model.DelegatedScope, modelRef string, attemptNumber int, inputUpperBound, outputUpperBound int64, limits ModelBudgetLimits, providerIdentity *string) (ModelCallLease, error) {
	if ctx == nil || strings.TrimSpace(modelRef) == "" || attemptNumber < 1 || limits.GlobalMaxInFlight < 1 || limits.TenantMaxInFlight < 1 ||
		limits.GlobalCallsPerMinute < 1 || limits.TenantCallsPerMinute < 1 || limits.GlobalTokensPerMinute < 1 || limits.TenantTokensPerMinute < 1 ||
		limits.MaxInputTokenReservation < 1 || limits.MaxOutputTokenReservation < 1 || inputUpperBound < 1 || outputUpperBound < 1 ||
		inputUpperBound > limits.MaxInputTokenReservation || outputUpperBound > limits.MaxOutputTokenReservation || inputUpperBound > math.MaxInt64-outputUpperBound || limits.LeaseDuration <= 0 {
		return ModelCallLease{}, errors.New("model budget configuration or token reservation is invalid")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ModelCallLease{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "model-gateway", Role: "model", TenantID: scope.TenantID, OrgID: scope.OrgID, WorkerID: scope.WorkerID}); err != nil {
		return ModelCallLease{}, err
	}
	if err := setModelLedgerIdentity(ctx, tx, scope.Kind, scope.JobID, scope.Generation, scope.InputSHA256); err != nil {
		return ModelCallLease{}, err
	}
	if scope.Kind == "experience_distill" {
		if caller != "worker" || scope.TenantID == "" || scope.OrgID == "" || scope.CaseID == "" || scope.ActorID == "" ||
			scope.JobID == "" || scope.WorkerID == "" || scope.Generation <= 0 || scope.RunID != "" || scope.AttemptID != "" || len(scope.InputSHA256) != 64 {
			return ModelCallLease{}, ErrModelScopeUnavailable
		}
		// The job row is the outer lock: reclamation and recovery must see this
		// reservation before they can advance or inspect the next generation.
		query := `SELECT ` + pgx.Identifier{s.schema, "model_budget_lock_experience_job"}.Sanitize() + `($1,$2,$3,$4,$5,$6,$7,$8)`
		var locked bool
		if err := tx.QueryRow(ctx, query, scope.TenantID, scope.OrgID, scope.CaseID, scope.ActorID,
			scope.JobID, scope.WorkerID, scope.Generation, scope.InputSHA256).Scan(&locked); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ModelCallLease{}, ErrModelScopeUnavailable
			}
			return ModelCallLease{}, err
		}
		if !locked {
			return ModelCallLease{}, ErrModelScopeUnavailable
		}
	}
	if err := s.authorizeModelScope(ctx, tx, caller, scope); err != nil {
		return ModelCallLease{}, err
	}
	if providerIdentity != nil && (scope.Kind == "execution" || scope.Kind == "experience_distill" || (scope.Kind == "document" && scope.JobID != "")) {
		if err := authorizeFrozenModelConfiguration(ctx, tx, scope, *providerIdentity); err != nil {
			return ModelCallLease{}, err
		}
	}
	id, err := newID("ml_")
	if err != nil {
		return ModelCallLease{}, err
	}
	lease := ModelCallLease{
		ID: id, TenantID: scope.TenantID, ActorID: scope.ActorID, CaseID: scope.CaseID,
		RunID: scope.RunID, AttemptID: scope.AttemptID, CallerIdentity: caller, ModelRef: modelRef, AttemptNumber: attemptNumber,
		Purpose: scope.Kind, JobID: scope.JobID, Generation: scope.Generation, InputSHA256: scope.InputSHA256,
	}
	var reserved bool
	query := `SELECT ` + pgx.Identifier{s.schema, "model_budget_reserve"}.Sanitize() + `($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`
	if err := tx.QueryRow(ctx, query,
		lease.ID, lease.TenantID, lease.ActorID, lease.CaseID, lease.RunID, lease.AttemptID,
		lease.CallerIdentity, lease.ModelRef, lease.AttemptNumber,
		limits.GlobalMaxInFlight, limits.TenantMaxInFlight, limits.GlobalCallsPerMinute, limits.TenantCallsPerMinute,
		limits.GlobalTokensPerMinute, limits.TenantTokensPerMinute, inputUpperBound, outputUpperBound,
		int64(math.Ceil(float64(limits.LeaseDuration)/float64(time.Millisecond))),
	).Scan(&reserved); err != nil {
		var state interface{ SQLState() string }
		if scope.Kind == "experience_distill" && errors.As(err, &state) && state.SQLState() == "P7M01" {
			return ModelCallLease{}, ErrModelScopeUnavailable
		}
		return ModelCallLease{}, err
	}
	if !reserved {
		if err := tx.Commit(ctx); err != nil {
			return ModelCallLease{}, err
		}
		s.Telemetry().RecordQuotaRejection("model_budget")
		return ModelCallLease{}, ErrModelBudgetExceeded
	}
	if err := s.authorizeModelScope(ctx, tx, caller, scope); err != nil {
		return ModelCallLease{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ModelCallLease{}, err
	}
	return lease, nil
}

// FinishModelCall writes one immutable usage result and releases its inflight
// lease atomically. Repeating the identical result succeeds; a conflicting
// second result is rejected. A zero-token usage remains unknown and is charged
// at the reserved upper bound unless pre-dispatch failure is proven.
func (s *Store) FinishModelCall(ctx context.Context, lease ModelCallLease, result ModelCallResult) error {
	if ctx == nil || lease.ID == "" || result.Status != "completed" && result.Status != "failed" ||
		result.PreDispatchFailure && (result.Status != "failed" || result.Usage != nil) {
		return errors.New("model call result is invalid")
	}
	if result.Usage != nil {
		for _, value := range []*int64{result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.TotalTokens} {
			if value != nil && *value < 0 {
				return errors.New("model call usage cannot be negative")
			}
		}
	}
	occurredAt := result.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	var source any
	var input, output, total any
	if result.Usage != nil {
		if result.Usage.Source != "" {
			source = result.Usage.Source
		}
		if result.Usage.InputTokens != nil {
			input = *result.Usage.InputTokens
		}
		if result.Usage.OutputTokens != nil {
			output = *result.Usage.OutputTokens
		}
		if result.Usage.TotalTokens != nil {
			total = *result.Usage.TotalTokens
		}
	}
	fingerprintPayload, err := json.Marshal(struct {
		Status             string
		ErrorCode          string
		Usage              *model.TokenUsage
		PreDispatchFailure bool
	}{result.Status, result.ErrorCode, result.Usage, result.PreDispatchFailure})
	if err != nil {
		return err
	}
	fingerprintSum := sha256.Sum256(fingerprintPayload)
	fingerprint := hex.EncodeToString(fingerprintSum[:])
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "model-gateway", Role: "model", TenantID: lease.TenantID}); err != nil {
		return err
	}
	if err := setModelLedgerIdentity(ctx, tx, lease.Purpose, lease.JobID, lease.Generation, lease.InputSHA256); err != nil {
		return err
	}
	query := `SELECT ` + pgx.Identifier{s.schema, "model_budget_finish"}.Sanitize() + `($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`
	var finished bool
	if err := tx.QueryRow(ctx, query,
		lease.ID, lease.TenantID, lease.ActorID, lease.CaseID, lease.RunID, lease.AttemptID, lease.CallerIdentity, lease.ModelRef,
		lease.AttemptNumber, result.Status, result.ErrorCode, source, input, output, total, occurredAt, result.PreDispatchFailure, fingerprint,
	).Scan(&finished); err != nil {
		return err
	}
	if !finished {
		return ErrModelCallSettlementConflict
	}
	return tx.Commit(ctx)
}

func authorizeFrozenModelConfiguration(ctx context.Context, tx pgx.Tx, scope model.DelegatedScope, providerIdentity string) error {
	var frozen string
	switch scope.Kind {
	case "document":
		return authorizeFrozenDocumentConfiguration(ctx, tx, scope, providerIdentity)
	case "execution":
		err := tx.QueryRow(ctx, `SELECT s.model_configuration_id FROM review_run_snapshots s
			JOIN review_runs r ON r.id=s.run_id AND r.tenant_id=s.tenant_id AND r.org_id=s.org_id
			WHERE s.run_id=$1 AND r.case_id=$2 AND r.tenant_id=$3 AND r.org_id=$4`,
			scope.RunID, scope.CaseID, scope.TenantID, scope.OrgID).Scan(&frozen)
		if err != nil {
			return ErrModelScopeUnavailable
		}
	case "experience_distill":
		var raw []byte
		var digest string
		err := tx.QueryRow(ctx, `SELECT input_bytes,input_sha256 FROM experience_distill_inputs
			WHERE job_id=$1 AND tenant_id=$2 AND org_id=$3 AND case_id=$4 AND actor_id=$5`,
			scope.JobID, scope.TenantID, scope.OrgID, scope.CaseID, scope.ActorID).Scan(&raw, &digest)
		if err != nil {
			return ErrModelScopeUnavailable
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != digest || digest != scope.InputSHA256 {
			return ErrModelScopeUnavailable
		}
		var input ExperienceDistillInput
		if decodeExperienceJSON(raw, &input) != nil || input.SchemaVersion != "experience-distill-input/v1" ||
			input.TenantID != scope.TenantID || input.OrgID != scope.OrgID || input.CaseID != scope.CaseID || input.ApplicantID != scope.ActorID {
			return ErrModelScopeUnavailable
		}
		frozen = input.ModelConfigurationID
	default:
		return ErrModelScopeUnavailable
	}
	if frozen == "" || providerIdentity == "" || frozen != providerIdentity {
		return ErrModelConfigurationChanged
	}
	return nil
}

func authorizeModelScope(ctx context.Context, tx pgx.Tx, caller string, scope model.DelegatedScope, resolved ...accounts.User) error {
	if (caller == "worker" && scope.Kind != "execution" && scope.Kind != "experience_distill") || (caller == "document" && scope.Kind != "document") ||
		(caller != "worker" && caller != "document") || scope.TenantID == "" || scope.OrgID == "" || scope.CaseID == "" || scope.ActorID == "" {
		return ErrModelScopeUnavailable
	}
	var caseItem Case
	var memberJSON string
	if scope.Kind == "experience_distill" {
		if caller != "worker" || scope.JobID == "" || scope.WorkerID == "" || scope.Generation <= 0 ||
			scope.RunID != "" || scope.AttemptID != "" || len(scope.InputSHA256) != 64 {
			return ErrModelScopeUnavailable
		}
		err := tx.QueryRow(ctx, `SELECT c.id,c.tenant_id,c.org_id,c.owner_id,c.member_ids
			FROM worker_jobs j JOIN experience_distill_inputs i ON i.job_id=j.id
			JOIN experience_feedback f ON f.tenant_id=i.tenant_id AND f.org_id=i.org_id AND f.case_id=i.case_id AND f.feedback_id=i.feedback_id
			JOIN cases c ON c.id=j.case_id AND c.tenant_id=j.tenant_id AND c.org_id=j.org_id
			WHERE j.id=$1 AND j.kind='experience_distill' AND j.worker_id=$2 AND j.generation=$3
			AND j.tenant_id=$4 AND j.org_id=$5 AND j.case_id=$6 AND j.actor_id=$7
			AND j.state='running' AND j.lease_until>clock_timestamp() AND i.actor_id=j.actor_id
			AND i.tenant_id=j.tenant_id AND i.org_id=j.org_id AND i.case_id=j.case_id AND i.input_sha256=$8`,
			scope.JobID, scope.WorkerID, scope.Generation, scope.TenantID, scope.OrgID, scope.CaseID, scope.ActorID, scope.InputSHA256).
			Scan(&caseItem.ID, &caseItem.TenantID, &caseItem.OwnerOrgID, &caseItem.OwnerID, &memberJSON)
		if err != nil {
			return ErrModelScopeUnavailable
		}
	} else if scope.JobID != "" {
		if scope.RunID == "" || scope.AttemptID == "" || scope.WorkerID == "" || scope.Generation <= 0 ||
			(caller == "document" && (scope.DocumentID == "" || scope.ParseSelectionID == "")) {
			return ErrModelScopeUnavailable
		}
		query := `SELECT c.id,c.tenant_id,c.org_id,c.owner_id,c.member_ids
			FROM worker_jobs j
			JOIN review_runs r ON r.id=j.run_id
			JOIN review_attempts a ON a.id=j.attempt_id AND a.run_id=j.run_id
			JOIN cases c ON c.id=j.case_id
			WHERE j.id=$1 AND j.worker_id=$2 AND j.generation=$3 AND j.tenant_id=$4 AND j.org_id=$5 AND j.case_id=$6
			AND j.run_id=$7 AND j.attempt_id=$8 AND j.actor_id=$9 AND j.state='running' AND j.lease_until>clock_timestamp()
			AND r.case_id=j.case_id AND r.state='running' AND a.actor_id=j.actor_id AND a.state IN ('accepted','running')`
		args := []any{scope.JobID, scope.WorkerID, scope.Generation, scope.TenantID, scope.OrgID, scope.CaseID, scope.RunID, scope.AttemptID, scope.ActorID}
		if caller == "document" {
			query += ` AND EXISTS (SELECT 1 FROM review_attempt_parse_selections p
				JOIN documents d ON d.id=p.document_id AND d.case_id=c.id AND d.tenant_id=c.tenant_id AND d.org_id=c.org_id
				WHERE p.attempt_id=a.id AND p.run_id=r.id AND p.document_id=$10 AND p.parsed_document_id=$11)`
			args = append(args, scope.DocumentID, scope.ParseSelectionID)
		}
		if err := tx.QueryRow(ctx, query, args...).Scan(&caseItem.ID, &caseItem.TenantID, &caseItem.OwnerOrgID, &caseItem.OwnerID, &memberJSON); err != nil {
			return ErrModelScopeUnavailable
		}
	} else {
		if caller != "document" || scope.DocumentID == "" || scope.RunID != "" || scope.AttemptID != "" {
			return ErrModelScopeUnavailable
		}

		if len(resolved) == 1 && (scope.Action == "intake_parse" || scope.Action == "upload_parse") {
			if err := tx.QueryRow(ctx, `SELECT id,tenant_id,org_id,owner_id,member_ids FROM cases WHERE id=$1 AND tenant_id=$2 AND org_id=$3`, scope.CaseID, scope.TenantID, scope.OrgID).Scan(&caseItem.ID, &caseItem.TenantID, &caseItem.OwnerOrgID, &caseItem.OwnerID, &memberJSON); err != nil {
				return ErrModelScopeUnavailable
			}
		} else {
			if err := tx.QueryRow(ctx, `SELECT c.id,c.tenant_id,c.org_id,c.owner_id,c.member_ids
			FROM cases c JOIN documents d ON d.case_id=c.id
			WHERE c.id=$1 AND c.tenant_id=$2 AND c.org_id=$3 AND d.id=$4 AND d.tenant_id=c.tenant_id AND d.org_id=c.org_id`,
				scope.CaseID, scope.TenantID, scope.OrgID, scope.DocumentID).
				Scan(&caseItem.ID, &caseItem.TenantID, &caseItem.OwnerOrgID, &caseItem.OwnerID, &memberJSON); err != nil {
				return ErrModelScopeUnavailable
			}
		}
	}
	if caseItem.TenantID != scope.TenantID || caseItem.OwnerOrgID != scope.OrgID || caseItem.ID != scope.CaseID {
		return ErrModelScopeUnavailable
	}
	if err := json.Unmarshal([]byte(memberJSON), &caseItem.MemberIDs); err != nil {
		return ErrModelScopeUnavailable
	}
	user, err := trustedUser(accounts.User{ID: scope.ActorID})
	if len(resolved) == 1 {
		user = resolved[0]
		err = nil
		if user.ID != scope.ActorID {
			err = ErrModelScopeUnavailable
		}
	}
	if err != nil || accounts.Tenant(user) != scope.TenantID {
		return ErrModelScopeUnavailable
	}
	action := access.Review
	if scope.JobID == "" {
		action = access.Manage
	}
	if !authorizeCase(caseItem, user, action) {
		return ErrModelScopeUnavailable
	}
	return nil
}

func setModelLedgerIdentity(ctx context.Context, tx pgx.Tx, purpose, jobID string, generation int64, inputSHA256 string) error {
	_, err := tx.Exec(ctx, `SELECT set_config('app.model_purpose',$1,true),set_config('app.job_id',$2,true),
		set_config('app.job_generation',$3,true),set_config('app.model_input_sha256',$4,true)`,
		purpose, jobID, strconv.FormatInt(generation, 10), inputSHA256)
	return err
}

// ReauthorizeModelScope checks current case permission and, for durable jobs,
// the live lease. It is called only after the actual model charge is settled.
func (s *Store) ReauthorizeModelScope(ctx context.Context, caller string, scope model.DelegatedScope) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "model-gateway", Role: "model", TenantID: scope.TenantID, OrgID: scope.OrgID}); err != nil {
		return err
	}
	if err := s.authorizeModelScope(ctx, tx, caller, scope); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) authorizeModelScope(ctx context.Context, tx pgx.Tx, caller string, scope model.DelegatedScope) error {
	u, err := s.operationUser(ctx, scope)
	if err != nil {
		return ErrModelScopeUnavailable
	}
	return authorizeModelScope(ctx, tx, caller, scope, u)
}
