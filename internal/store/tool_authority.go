package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
)

// ErrToolScopeUnavailable reports that the job context a tool call presented is
// not, or is no longer, backed by the current durable job claim.
var ErrToolScopeUnavailable = errors.New("current tool job scope is unavailable")

// toolScopeRefusal wraps ErrToolScopeUnavailable with the exact branch that
// refused, so a caller can log which precondition failed (reason codes are
// stable and contain no identifiers or payload data).
func toolScopeRefusal(branch string) error {
	return fmt.Errorf("%w: %s", ErrToolScopeUnavailable, branch)
}

// ToolInvocationContext is the job context one worker tool call presents.
// TenantID and OrgID are routing hints taken from the worker's durable job
// claim; they are never authority. Everything this resolution reads must match
// them plus the exact job, worker, generation, case, run and attempt identities.
type ToolInvocationContext struct {
	TenantID   string
	OrgID      string
	JobID      string
	WorkerID   string
	Generation int64
	CaseID     string
	RunID      string
	AttemptID  string
}

// ToolAuthority is the trusted actor and immutable scope resolved from the
// database for one job-scoped tool call. User is the actor the read-only tool
// boundary must pass to its own case and budget checks; it comes from the
// trusted account directory, never from the request.
type ToolAuthority struct {
	User      accounts.User
	TenantID  string
	OrgID     string
	CaseID    string
	RunID     string
	AttemptID string
	JobID     string
}

// ToolEvidenceRequest is one post-input evidence read for a frozen attempt.
// TenantID, OrgID, JobID, WorkerID, Generation and AttemptID come from the
// worker's durable claim; CaseID, RunID, AttemptID, CapabilityID and Deadline
// come from the frozen attempt pipeline.
type ToolEvidenceRequest struct {
	TenantID     string
	OrgID        string
	JobID        string
	WorkerID     string
	Generation   int64
	CaseID       string
	RunID        string
	AttemptID    string
	CapabilityID string
	Deadline     time.Time
}

// ToolEvidenceReader returns the bounded raw evidence payload for one
// job-scoped tool call. Implementations must return tool failures as errors:
// evidence for a dead or foreign claim is never fabricated.
type ToolEvidenceReader interface {
	FrozenEvidence(ctx context.Context, in ToolEvidenceRequest) (json.RawMessage, error)
}

// UseToolEvidenceReader installs the authenticated tool reader the attempt
// pipeline consults under a frozen input.
func (s *Store) UseToolEvidenceReader(reader ToolEvidenceReader) error {
	if reader == nil {
		return errors.New("tool evidence reader is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolEvidence = reader
	return nil
}

func (s *Store) toolEvidenceReader() ToolEvidenceReader {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.toolEvidence
}

// AuthorizeToolResolution resolves the trusted actor, tenant and case scope for
// one job-scoped tool call. It runs in a single read-only transaction and takes
// no row locks: the tool role may read core and execution rows but cannot lock
// them. Call it both before the tool reads and again before it returns
// evidence, so a claim that was cancelled, re-claimed, or re-leased in between
// fails closed instead of returning evidence for a dead attempt.
func (s *Store) AuthorizeToolResolution(ctx context.Context, request ToolInvocationContext) (ToolAuthority, error) {
	if ctx == nil || request.TenantID == "" || request.OrgID == "" || request.JobID == "" || request.WorkerID == "" ||
		request.Generation <= 0 || request.CaseID == "" || request.RunID == "" || request.AttemptID == "" {
		return ToolAuthority{}, toolScopeRefusal("request_incomplete")
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ToolAuthority{}, err
	}
	defer tx.Rollback(ctx)
	// The lookup runs in the tool scope with the request hints; the tenant policy
	// alone must expose the row. The hints are then verified against the row's own
	// tenant/org (and their values reported by the branch diagnostic below) rather
	// than trusted for visibility.
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "tool-gateway", Role: "tool", TenantID: request.TenantID, OrgID: request.OrgID,
		JobID: request.JobID, WorkerID: request.WorkerID, Generation: request.Generation}); err != nil {
		return ToolAuthority{}, err
	}
	var actorID, ownerID, memberJSON, jobState, runState, attemptRunID, historyKind, attemptState string
	var rowTenant, rowOrg string
	var attemptInputHash sql.NullString
	var rowGeneration int64
	if err := tx.QueryRow(ctx, `SELECT j.tenant_id, j.org_id, j.actor_id, j.state, j.generation, c.owner_id, c.member_ids,
		r.state, a.run_id, a.history_kind, a.state, a.input_hash
		FROM worker_jobs AS j
		JOIN review_runs AS r ON r.tenant_id=j.tenant_id AND r.org_id=j.org_id AND r.case_id=j.case_id AND r.id=j.run_id
		JOIN review_attempts AS a ON a.tenant_id=r.tenant_id AND a.org_id=r.org_id AND a.run_id=r.id AND a.id=j.attempt_id
		JOIN cases AS c ON c.tenant_id=j.tenant_id AND c.org_id=j.org_id AND c.id=j.case_id
		WHERE j.id=$1 AND j.worker_id=$2 AND j.generation=$3 AND j.case_id=$4 AND j.run_id=$5 AND j.attempt_id=$6
			AND j.tenant_id=$7 AND j.org_id=$8 AND j.state='running' AND j.lease_until > clock_timestamp()`,
		request.JobID, request.WorkerID, request.Generation, request.CaseID, request.RunID, request.AttemptID,
		request.TenantID, request.OrgID).
		Scan(&rowTenant, &rowOrg, &actorID, &jobState, &rowGeneration, &ownerID, &memberJSON, &runState, &attemptRunID, &historyKind, &attemptState, &attemptInputHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Distinguish "the job row itself is invisible to this scope" (hint
			// values or session GUCs) from "the join over run/attempt/case failed"
			// without leaking identifiers: only counts are reported.
			var visibleJobs, visibleRuns int
			_ = tx.QueryRow(ctx, `SELECT count(*) FROM worker_jobs WHERE id=$1`, request.JobID).Scan(&visibleJobs)
			_ = tx.QueryRow(ctx, `SELECT count(*) FROM worker_jobs AS j JOIN review_runs AS r ON r.id=j.run_id WHERE j.id=$1`, request.JobID).Scan(&visibleRuns)
			var txTenant, txOrg, txRole string
			_ = tx.QueryRow(ctx, `SELECT coalesce(current_setting('app.tenant_id', true),''), coalesce(current_setting('app.org_id', true),''), coalesce(current_setting('app.actor_role', true),'')`).
				Scan(&txTenant, &txOrg, &txRole)
			return ToolAuthority{}, toolScopeRefusal(fmt.Sprintf(
				"claim_unavailable(visible_job=%d,visible_run=%d,tenant=%q,org=%q,actor_role=%q,hint_tenant=%q,hint_org=%q)",
				visibleJobs, visibleRuns, txTenant, txOrg, txRole, request.TenantID, request.OrgID))
		}
		return ToolAuthority{}, err
	}
	switch {
	case rowTenant != request.TenantID:
		return ToolAuthority{}, toolScopeRefusal("claim_tenant_mismatch")
	case rowOrg != request.OrgID:
		return ToolAuthority{}, toolScopeRefusal("claim_org_mismatch")
	case jobState != "running":
		return ToolAuthority{}, toolScopeRefusal("job_not_running")
	case rowGeneration != request.Generation:
		return ToolAuthority{}, toolScopeRefusal("generation_mismatch")
	case actorID == "":
		return ToolAuthority{}, toolScopeRefusal("actor_missing")
	case runState == "cancelled":
		return ToolAuthority{}, toolScopeRefusal("run_cancelled")
	case attemptRunID != request.RunID:
		return ToolAuthority{}, toolScopeRefusal("attempt_run_mismatch")
	case historyKind != "frozen":
		return ToolAuthority{}, toolScopeRefusal("attempt_not_frozen")
	case !attemptInputHash.Valid || attemptInputHash.String == "":
		return ToolAuthority{}, toolScopeRefusal("attempt_input_missing")
	case attemptState == "cancelled":
		return ToolAuthority{}, toolScopeRefusal("attempt_cancelled")
	}
	var memberIDs []string
	if err := json.Unmarshal([]byte(memberJSON), &memberIDs); err != nil {
		return ToolAuthority{}, toolScopeRefusal("case_members_unreadable")
	}
	user, err := s.operationUser(ctx, model.DelegatedScope{Kind: "execution", TenantID: request.TenantID, OrgID: request.OrgID, CaseID: request.CaseID, ActorID: actorID, JobID: request.JobID, WorkerID: request.WorkerID, Generation: request.Generation, RunID: request.RunID, AttemptID: request.AttemptID})
	if err != nil {
		return ToolAuthority{}, toolScopeRefusal("actor_untrusted")
	}
	if accounts.Tenant(user) != request.TenantID {
		return ToolAuthority{}, toolScopeRefusal("actor_scope_mismatch")
	}
	caseItem := Case{ID: request.CaseID, TenantID: request.TenantID, OwnerOrgID: request.OrgID, OwnerID: ownerID, MemberIDs: memberIDs}
	if !authorizeCase(caseItem, user, access.Read) {
		return ToolAuthority{}, toolScopeRefusal("case_grant_missing")
	}
	if err := tx.Commit(ctx); err != nil {
		return ToolAuthority{}, err
	}
	return ToolAuthority{
		User: user, TenantID: rowTenant, OrgID: rowOrg,
		CaseID: request.CaseID, RunID: request.RunID, AttemptID: request.AttemptID, JobID: request.JobID,
	}, nil
}
