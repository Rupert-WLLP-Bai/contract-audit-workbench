package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

const (
	coreWriteReceipts      = "core_write_receipts"
	executionWriteReceipts = "execution_write_receipts"
)

type writeReceipt struct {
	CaseID        string
	OrgID         string
	RequestDigest string
	Response      json.RawMessage
}

func optionalIdempotencyKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key != "" {
		if err := validateIdempotencyKey(key); err != nil {
			return "", err
		}
	}
	return key, nil
}

// The advisory key also serializes a first insertion, for which SELECT FOR
// UPDATE has no row to lock. It is taken inside the effect transaction.
func lockWriteReceipt(ctx context.Context, tx pgx.Tx, user accounts.User, route, key string) error {
	identity, err := canonicalHash([]string{accounts.Tenant(user), user.ID, route, key})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':write-receipt:' || $1, 0))`, identity)
	return err
}

func loadWriteReceipt(ctx context.Context, tx pgx.Tx, table string, user accounts.User, route, key string) (writeReceipt, bool, error) {
	var receipt writeReceipt
	err := tx.QueryRow(ctx, `SELECT org_id, case_id, request_digest, response_json FROM `+table+
		` WHERE tenant_id=$1 AND actor_id=$2 AND route=$3 AND idempotency_key=$4`,
		accounts.Tenant(user), user.ID, route, key).Scan(&receipt.OrgID, &receipt.CaseID, &receipt.RequestDigest, &receipt.Response)
	if errors.Is(err, pgx.ErrNoRows) {
		return writeReceipt{}, false, nil
	}
	return receipt, err == nil, err
}

func insertWriteReceipt(ctx context.Context, tx pgx.Tx, table string, user accounts.User, item Case, route, key, digest string, response any) error {
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO `+table+` (tenant_id,org_id,actor_id,route,idempotency_key,request_digest,case_id,response_json)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb)`, item.TenantID, item.OwnerOrgID, user.ID, route, key, digest, item.ID, string(body))
	return err
}

func checkWriteReceipt(receipt writeReceipt, item Case, digest string) error {
	if receipt.CaseID != item.ID || receipt.OrgID != item.OwnerOrgID {
		return errors.New("idempotency receipt case identity is inconsistent")
	}
	if receipt.RequestDigest != digest {
		return fault.Conflict("Idempotency-Key已用于不同请求")
	}
	return nil
}

func validateWriteReceiptTarget(ctx context.Context, tx pgx.Tx, table string, item Case, receipt writeReceipt) error {
	var exists bool
	var err error
	if table == executionWriteReceipts {
		accepted, decodeErr := decodeReviewWriteReceipt(receipt.Response)
		if decodeErr != nil {
			return decodeErr
		}
		err = tx.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM review_runs r JOIN review_attempts a
			ON a.tenant_id=r.tenant_id AND a.org_id=r.org_id AND a.run_id=r.id
			WHERE r.tenant_id=$1 AND r.org_id=$2 AND r.case_id=$3 AND r.id=$4 AND a.id=$5
			AND ($6='' OR EXISTS (SELECT 1 FROM worker_jobs j WHERE j.tenant_id=r.tenant_id AND j.org_id=r.org_id
			AND j.case_id=r.case_id AND j.run_id=r.id AND j.attempt_id=a.id AND j.id=$6)))`,
			item.TenantID, item.OwnerOrgID, item.ID, accepted.Acceptance.RunID, accepted.Acceptance.AttemptID, accepted.Acceptance.JobID).Scan(&exists)
	} else {
		var report ReportRecord
		if err := json.Unmarshal(receipt.Response, &report); err != nil || report.ID == "" {
			return errors.New("invalid report idempotency receipt")
		}
		err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM business_reports
			WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND id=$4 AND run_id=$5 AND attempt_id=$6 AND digest=$7 AND version=$8)`,
			item.TenantID, item.OwnerOrgID, item.ID, report.ID, report.RunID, report.AttemptID, report.Digest, report.Version).Scan(&exists)
	}
	if err != nil {
		return err
	}
	if !exists {
		return fault.NotFound("原操作对应的记录不存在")
	}
	return nil
}

// readWriteReceipt rechecks the current account and case grant while holding the
// case lock; old receipts never confer access to a revoked case. It is only an
// early replay path: the effect transaction checks again after its own locks.
func (s *Store) readWriteReceipt(ctx context.Context, user accounts.User, table, route, key, digest string, action access.Action) (writeReceipt, bool, error) {
	ctx = commandContext(ctx, user)
	if key == "" {
		return writeReceipt{}, false, nil
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return writeReceipt{}, false, err
	}
	defer tx.Rollback(context.Background())
	if err := setLocalScope(ctx, tx, userScope(user)); err != nil {
		return writeReceipt{}, false, err
	}
	receipt, found, err := loadWriteReceipt(ctx, tx, table, user, route, key)
	if err != nil || !found {
		return writeReceipt{}, found, err
	}
	if err := scopedCaseLock(ctx, tx, receipt.CaseID); err != nil {
		return writeReceipt{}, false, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return writeReceipt{}, false, err
	}
	item, err := loadKnowledgeCase(ctx, tx, current, receipt.CaseID)
	if err != nil {
		return writeReceipt{}, false, err
	}
	if err := requireCaseAction(item, current, action); err != nil {
		return writeReceipt{}, false, err
	}
	if action != access.Read {
		if err := businessWritable(item); err != nil {
			return writeReceipt{}, false, err
		}
	}
	if err := checkWriteReceipt(receipt, item, digest); err != nil {
		return writeReceipt{}, false, err
	}
	if err := validateWriteReceiptTarget(ctx, tx, table, item, receipt); err != nil {
		return writeReceipt{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return writeReceipt{}, false, err
	}
	return receipt, true, nil
}

type reviewIdempotencyContextKey struct{}

type reviewIdempotency struct {
	key        string
	acceptance JobAcceptance
	durable    bool
	present    bool
}

type reviewWriteReceipt struct {
	Acceptance JobAcceptance `json:"acceptance"`
	Durable    bool          `json:"durable"`
}

// WithReviewIdempotency binds an optional HTTP operation key to one admission.
// The store derives the requested scope from the actual admission arguments.
func WithReviewIdempotency(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, reviewIdempotencyContextKey{}, &reviewIdempotency{key: key})
}

// ReviewIdempotentAcceptance returns the receipt only after its transaction has
// committed, including when an existing accepted attempt was replayed.
func ReviewIdempotentAcceptance(ctx context.Context) (JobAcceptance, bool, bool) {
	value, _ := ctx.Value(reviewIdempotencyContextKey{}).(*reviewIdempotency)
	if value == nil || !value.present {
		return JobAcceptance{}, false, false
	}
	return value.acceptance, value.durable, true
}

func decodeReviewWriteReceipt(raw []byte) (reviewWriteReceipt, error) {
	var result reviewWriteReceipt
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	if result.Acceptance.RunID == "" || result.Acceptance.AttemptID == "" || (result.Durable && result.Acceptance.JobID == "") {
		return result, errors.New("invalid review idempotency receipt")
	}
	return result, nil
}
