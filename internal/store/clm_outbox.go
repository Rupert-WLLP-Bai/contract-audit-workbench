package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrCLMOutboxClaimLost means the claim expired, was released, or was fenced
// by a later token before its broker acknowledgement or release.
var ErrCLMOutboxClaimLost = errors.New("CLM outbox claim is no longer held")

const (
	clmOutboxMinLease = time.Second
	clmOutboxMaxLease = 5 * time.Minute
	clmOutboxMaxBatch = 128
	clmOutboxMaxRetry = 10 * time.Minute
)

// CLMOutboxClaim is the transport-neutral, non-sensitive identity needed to
// publish a committed CLM domain event. The payload stays in PostgreSQL.
type CLMOutboxClaim struct {
	TenantID   string
	EventID    string
	EventType  string
	OccurredAt time.Time
	Token      int64
	LeaseUntil time.Time
	MessageID  string
}

// ClaimCLMOutbox uses the narrow Core-only maintenance function. The database
// commits token increments before any caller performs broker I/O.
func (s *Store) ClaimCLMOutbox(ctx context.Context, limit int, lease time.Duration) ([]CLMOutboxClaim, error) {
	if s == nil || s.pool == nil || ctx == nil || limit < 1 || limit > clmOutboxMaxBatch || lease < clmOutboxMinLease || lease > clmOutboxMaxLease {
		return nil, errors.New("CLM outbox claim request is invalid")
	}
	leaseSeconds := int64(lease / time.Second)
	if leaseSeconds < 1 || time.Duration(leaseSeconds)*time.Second != lease {
		return nil, errors.New("CLM outbox lease must use whole seconds")
	}
	rows, err := s.pool.Query(ctx, `SELECT tenant_id,event_id,event_type,occurred_at,claim_token,claimed_until,message_id
		FROM clm_outbox_claim($1,$2)`, limit, leaseSeconds)
	if err != nil {
		return nil, fmt.Errorf("claim CLM outbox rows: %w", err)
	}
	defer rows.Close()
	claims := make([]CLMOutboxClaim, 0, limit)
	for rows.Next() {
		var claim CLMOutboxClaim
		var eventID uuid.UUID
		var occurredAt *time.Time
		if err := rows.Scan(&claim.TenantID, &eventID, &claim.EventType, &occurredAt, &claim.Token, &claim.LeaseUntil, &claim.MessageID); err != nil {
			return nil, fmt.Errorf("scan CLM outbox claim: %w", err)
		}
		if occurredAt != nil {
			claim.OccurredAt = *occurredAt
		}
		claim.EventID = eventID.String()
		claims = append(claims, claim)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate CLM outbox claims: %w", err)
	}
	return claims, nil
}

// AckCLMOutbox records only broker durability. A repeated acknowledgement by
// the same fencing token is idempotent; a superseded or expired token fails.
func (s *Store) AckCLMOutbox(ctx context.Context, claim CLMOutboxClaim) error {
	eventID, err := validateCLMOutboxClaim(ctx, s, claim)
	if err != nil {
		return err
	}
	var accepted bool
	if err := s.pool.QueryRow(ctx, `SELECT clm_outbox_ack($1,$2,$3)`, claim.TenantID, eventID, claim.Token).Scan(&accepted); err != nil {
		return fmt.Errorf("acknowledge CLM outbox row: %w", err)
	}
	if !accepted {
		return ErrCLMOutboxClaimLost
	}
	return nil
}

// ReleaseCLMOutbox returns a failed publish to the bounded retry schedule.
// Reason codes intentionally exclude arbitrary broker error text and secrets.
func (s *Store) ReleaseCLMOutbox(ctx context.Context, claim CLMOutboxClaim, reasonCode string, retryAfter time.Duration) error {
	eventID, err := validateCLMOutboxClaim(ctx, s, claim)
	if err != nil {
		return err
	}
	reasonCode = strings.TrimSpace(reasonCode)
	if !validOutboxReason(reasonCode) {
		return errors.New("CLM outbox release reason is invalid")
	}
	if retryAfter < 0 {
		retryAfter = 0
	}
	if retryAfter > clmOutboxMaxRetry {
		retryAfter = clmOutboxMaxRetry
	}
	var released bool
	if err := s.pool.QueryRow(ctx, `SELECT clm_outbox_release($1,$2,$3,$4)`, claim.TenantID, eventID, claim.Token, retryAfter.Microseconds()).Scan(&released); err != nil {
		return fmt.Errorf("release CLM outbox row: %w", err)
	}
	if !released {
		return ErrCLMOutboxClaimLost
	}
	return nil
}

func validateCLMOutboxClaim(ctx context.Context, s *Store, claim CLMOutboxClaim) (uuid.UUID, error) {
	if s == nil || s.pool == nil || ctx == nil || strings.TrimSpace(claim.TenantID) == "" || claim.Token <= 0 {
		return uuid.Nil, errors.New("CLM outbox claim is invalid")
	}
	eventID, err := uuid.Parse(claim.EventID)
	if err != nil {
		return uuid.Nil, errors.New("CLM outbox claim event identity is invalid")
	}
	return eventID, nil
}
