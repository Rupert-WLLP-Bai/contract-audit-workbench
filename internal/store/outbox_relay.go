package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrOutboxClaimLost reports that the relay no longer holds the claim it tries
// to acknowledge or release: another relay took the event over after the lease
// expired, or the claim token was superseded.
var ErrOutboxClaimLost = errors.New("outbox claim is no longer held")

const (
	outboxMinLease     = time.Millisecond
	outboxMaxLease     = 5 * time.Minute
	outboxMaxRetryWait = 10 * time.Minute
	outboxMaxReason    = 64
)

// OutboxClaim is one durable outbox event a relay may publish. Event carries the
// existing immutable notification metadata only; Token is the monotonic fencing
// value of this claim and must be presented again to acknowledge or release it.
type OutboxClaim struct {
	Event      OutboxRecord
	Owner      string
	Token      int64
	LeaseUntil time.Time
}

// ClaimOutbox claims up to limit retry-eligible pending events for owner in one
// statement, so no transaction is ever held across a network publish. Rows
// already claimed by another live lease are skipped (SKIP LOCKED); a row whose
// lease expired or whose backoff elapsed becomes claimable again, and every
// claim increments the fencing token.
func (s *Store) ClaimOutbox(ctx context.Context, owner string, limit int, lease time.Duration) ([]OutboxClaim, error) {
	owner = strings.TrimSpace(owner)
	if ctx == nil || owner == "" || len(owner) > 128 || limit <= 0 || limit > 512 {
		return nil, errors.New("outbox claim request is invalid")
	}
	if lease < outboxMinLease {
		lease = outboxMinLease
	}
	if lease > outboxMaxLease {
		lease = outboxMaxLease
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// The execution/worker role drains the queue across tenants; the policy for
	// outbox_events explicitly grants that to the worker actor role.
	if err := setLocalScope(ctx, tx, dbScope{ActorID: owner, Role: "worker", WorkerID: owner}); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH candidate AS (
		SELECT id FROM outbox_events
		WHERE state='pending' AND next_attempt_at <= clock_timestamp()
			AND (claim_until IS NULL OR claim_until <= clock_timestamp())
		ORDER BY next_attempt_at, created_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT $2
	)
	UPDATE outbox_events AS event
	SET claim_owner=$1, claim_until=clock_timestamp()+($3::bigint*interval '1 microsecond'), attempts=event.attempts+1
	FROM candidate WHERE event.id=candidate.id
	RETURNING event.id, event.job_id, event.tenant_id, event.org_id, event.case_id, event.event_type,
		event.payload, event.state, event.created_at, event.delivered_at, event.attempts, event.claim_until`,
		owner, limit, lease.Microseconds())
	if err != nil {
		return nil, fmt.Errorf("claim outbox events: %w", err)
	}
	defer rows.Close()
	var claims []OutboxClaim
	for rows.Next() {
		var claim OutboxClaim
		var deliveredAt *string
		var leaseUntil *time.Time
		if err := rows.Scan(&claim.Event.ID, &claim.Event.JobID, &claim.Event.TenantID, &claim.Event.OrgID, &claim.Event.CaseID,
			&claim.Event.EventType, &claim.Event.Payload, &claim.Event.State, &claim.Event.CreatedAt, &deliveredAt,
			&claim.Token, &leaseUntil); err != nil {
			return nil, fmt.Errorf("scan outbox claim: %w", err)
		}
		claim.Event.DeliveredAt = deliveredAt
		claim.Owner = owner
		if leaseUntil != nil {
			claim.LeaseUntil = *leaseUntil
		}
		claims = append(claims, claim)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbox claims: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return claims, nil
}

// AckOutbox marks one claimed event delivered. Call it only after the broker
// confirmed durability: a crash between publish and ACK leaves the row pending,
// so the same stable event id is published again rather than lost.
func (s *Store) AckOutbox(ctx context.Context, claim OutboxClaim) error {
	if ctx == nil || claim.Event.ID == "" || strings.TrimSpace(claim.Owner) == "" || claim.Token <= 0 {
		return errors.New("outbox claim is invalid")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, dbScope{ActorID: claim.Owner, Role: "worker", WorkerID: claim.Owner}); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE outbox_events
		SET state='delivered', delivered_at=$4, claim_owner='', claim_until=NULL, last_error=''
		WHERE id=$1 AND state='pending' AND claim_owner=$2 AND attempts=$3 AND claim_until > clock_timestamp()`,
		claim.Event.ID, claim.Owner, claim.Token, stamp())
	if err != nil {
		return fmt.Errorf("acknowledge outbox event: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrOutboxClaimLost
	}
	return tx.Commit(ctx)
}

// ReleaseOutbox returns a claimed event to the pending scan after a publish
// failure. reasonCode must be a bounded stable code (no URLs, errors or
// secrets), and retryAfter is clamped into the retry window so a broken broker
// cannot turn into a hot loop.
func (s *Store) ReleaseOutbox(ctx context.Context, claim OutboxClaim, reasonCode string, retryAfter time.Duration) error {
	reasonCode = strings.TrimSpace(reasonCode)
	if ctx == nil || claim.Event.ID == "" || strings.TrimSpace(claim.Owner) == "" || claim.Token <= 0 || !validOutboxReason(reasonCode) {
		return errors.New("outbox release request is invalid")
	}
	if retryAfter < 0 {
		retryAfter = 0
	}
	if retryAfter > outboxMaxRetryWait {
		retryAfter = outboxMaxRetryWait
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, dbScope{ActorID: claim.Owner, Role: "worker", WorkerID: claim.Owner}); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE outbox_events
		SET claim_owner='', claim_until=NULL, last_error=$4,
			next_attempt_at=clock_timestamp()+($5::bigint*interval '1 microsecond')
		WHERE id=$1 AND state='pending' AND claim_owner=$2 AND attempts=$3 AND claim_until > clock_timestamp()`,
		claim.Event.ID, claim.Owner, claim.Token, reasonCode, retryAfter.Microseconds())
	if err != nil {
		return fmt.Errorf("release outbox event: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrOutboxClaimLost
	}
	return tx.Commit(ctx)
}

// validOutboxReason keeps stored retry reasons bounded and free of payload data:
// lowercase codes only, so an error string or URL can never be persisted.
func validOutboxReason(value string) bool {
	if value == "" || len(value) > outboxMaxReason {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.' {
			continue
		}
		return false
	}
	return true
}
