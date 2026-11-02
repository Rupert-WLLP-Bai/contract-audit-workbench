package store

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

// intakeContinuationRow contains only the operation, its original request,
// archive fact, and the case fields required by the existing access policy.
type intakeContinuationRow struct {
	operationID    string
	caseID         string
	status         string
	idempotencyKey string
	requestDigest  string
	createdAt      time.Time
	updatedAt      time.Time
	item           Case
	members        string
	schemaVersion  *int16
	requestJSON    []byte
	archivedAt     *time.Time
}

const intakeContinuationSelect = `SELECT o.operation_id,o.case_id,o.status,o.idempotency_key,o.request_digest,o.created_at,o.updated_at,
	c.id,c.tenant_id,c.org_id,c.owner_id,c.member_ids,c.business_status,c.archived_at,c.title,
	r.schema_version,r.request_json,a.archived_at
	FROM case_intake_operations o
	JOIN cases c ON c.tenant_id=o.tenant_id AND c.org_id=o.org_id AND c.id=o.case_id
	LEFT JOIN case_intake_requests r ON r.tenant_id=o.tenant_id AND r.operation_id=o.operation_id
	LEFT JOIN case_intake_archives a ON a.tenant_id=o.tenant_id AND a.operation_id=o.operation_id`

func scanIntakeContinuation(row rowScanner) (intakeContinuationRow, error) {
	var result intakeContinuationRow
	err := row.Scan(&result.operationID, &result.caseID, &result.status, &result.idempotencyKey, &result.requestDigest,
		&result.createdAt, &result.updatedAt, &result.item.ID, &result.item.TenantID, &result.item.OwnerOrgID,
		&result.item.OwnerID, &result.members, &result.item.BusinessStatus, &result.item.ArchivedAt, &result.item.Title,
		&result.schemaVersion, &result.requestJSON, &result.archivedAt)
	if err != nil {
		return intakeContinuationRow{}, err
	}
	if err := json.Unmarshal([]byte(result.members), &result.item.MemberIDs); err != nil {
		return intakeContinuationRow{}, err
	}
	return result, nil
}

func caseIntakeArchived(ctx context.Context, tx pgx.Tx, tenantID, operationID string) (bool, error) {
	var archived bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM case_intake_archives WHERE tenant_id=$1 AND operation_id=$2)`,
		tenantID, operationID).Scan(&archived)
	return archived, err
}

// Restoring replay input must never depend on the mutable case projection. A
// missing request is a legacy operation; malformed or mismatched metadata is
// deliberately unavailable even when the case itself remains readable.
func (row intakeContinuationRow) requestIdentity() (*CaseIntakeRequestIdentity, string) {
	if row.schemaVersion == nil {
		return nil, "legacy_request_unavailable"
	}
	if *row.schemaVersion != 1 || len(row.requestJSON) == 0 {
		return nil, "request_identity_invalid"
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(row.requestJSON, &fields) != nil || len(fields) != 8 {
		return nil, "request_identity_invalid"
	}
	for _, name := range []string{"title", "contract_type", "business_line", "scenario", "file_name", "file_size", "media_type", "file_sha256"} {
		value, ok := fields[name]
		if !ok || string(value) == "null" {
			return nil, "request_identity_invalid"
		}
	}
	var identity CaseIntakeRequestIdentity
	if json.Unmarshal(row.requestJSON, &identity) != nil || identity.FileSize < 0 || identity.FileSize > caseIntakeMaxFileBytes ||
		identity.FileName == "" || cleanName(identity.FileName) != identity.FileName || cleanMedia(identity.MediaType) != identity.MediaType ||
		len(identity.FileSHA256) != 64 || strings.ToLower(identity.FileSHA256) != identity.FileSHA256 || validateIdempotencyKey(row.idempotencyKey) != nil {
		return nil, "request_identity_invalid"
	}
	if _, err := hex.DecodeString(identity.FileSHA256); err != nil {
		return nil, "request_identity_invalid"
	}
	for _, char := range identity.FileName {
		if char < 0x20 || char == 0x7f {
			return nil, "request_identity_invalid"
		}
	}
	input, err := validateBusinessCaseInput(identity.Title, identity.ContractType, identity.BusinessLine, identity.Scenario)
	if err != nil || input.title != identity.Title || input.contractType != identity.ContractType ||
		input.businessLine != identity.BusinessLine || input.scenario != identity.Scenario ||
		caseIntakeRequestDigest(identity) != row.requestDigest {
		return nil, "request_identity_invalid"
	}
	return &identity, ""
}

func (s *Store) intakeActionFlags(current accounts.User, row intakeContinuationRow, identityReason string) (bool, bool, string) {
	if row.status == caseIntakeCompletedStatus {
		return false, false, "completed"
	}
	if row.archivedAt != nil {
		return false, false, "archived"
	}
	if row.status != caseIntakePendingStatus {
		return false, false, "request_identity_invalid"
	}
	if s.ReadOnly() {
		return false, false, "read_only"
	}
	if !accounts.CanCreate(current.Role) || !accounts.CanUpload(current.Role) || !authorizeCase(row.item, current, access.Manage) {
		return false, false, "forbidden"
	}
	if businessWritable(row.item) != nil {
		return false, false, "case_not_writable"
	}
	if identityReason != "" {
		return false, true, identityReason
	}
	return true, true, ""
}

type pendingIntakeCursor struct {
	Version     int       `json:"version"`
	TenantID    string    `json:"tenant_id"`
	ActorID     string    `json:"actor_id"`
	CreatedAt   time.Time `json:"created_at"`
	OperationID string    `json:"operation_id"`
}

func decodePendingIntakeCursor(value string, current accounts.User) (*pendingIntakeCursor, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > 2048 {
		return nil, fault.Invalid("待续办分页游标无效")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	var cursor pendingIntakeCursor
	if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Version != 1 || cursor.TenantID != accounts.Tenant(current) ||
		cursor.ActorID != current.ID || cursor.CreatedAt.IsZero() || !validCaseIntakeID(cursor.OperationID) {
		return nil, fault.Invalid("待续办分页游标无效")
	}
	return &cursor, nil
}

func encodePendingIntakeCursor(current accounts.User, createdAt time.Time, operationID string) string {
	raw, _ := json.Marshal(pendingIntakeCursor{Version: 1, TenantID: accounts.Tenant(current), ActorID: current.ID,
		CreatedAt: createdAt.UTC(), OperationID: operationID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// ListPendingCaseIntakes uses bounded keyset reads, applying the same current
// case policy used by the other case APIs before any row reaches the caller.
func (s *Store) ListPendingCaseIntakes(ctx context.Context, user accounts.User, limit int, cursor string) (PendingCaseIntakesPage, error) {
	ctx = commandContext(ctx, user)
	if limit == 0 {
		limit = 10
	}
	if limit < 1 || limit > 50 {
		return PendingCaseIntakesPage{}, fault.Invalid("待续办分页范围为1到50")
	}
	current, err := trustedUser(user)
	if err != nil {
		return PendingCaseIntakesPage{}, err
	}
	before, err := decodePendingIntakeCursor(cursor, current)
	if err != nil {
		return PendingCaseIntakesPage{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return PendingCaseIntakesPage{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return PendingCaseIntakesPage{}, err
	}
	page := PendingCaseIntakesPage{Items: make([]PendingCaseIntake, 0, limit)}
	var beforeTime *time.Time
	var beforeID string
	if before != nil {
		beforeTime, beforeID = &before.CreatedAt, before.OperationID
	}
	var lastVisibleTime time.Time
	var lastVisibleID string
	for {
		rows, err := tx.Query(ctx, intakeContinuationSelect+`
			WHERE o.tenant_id=$1 AND o.actor_id=$2 AND o.status='pending' AND a.operation_id IS NULL
				AND c.archived_at IS NULL AND c.business_status<>'archived'
				AND ($3::timestamptz IS NULL OR (o.created_at,o.operation_id)<($3::timestamptz,$4::text))
			ORDER BY o.created_at DESC,o.operation_id DESC LIMIT $5`, accounts.Tenant(current), current.ID, beforeTime, beforeID, limit+1)
		if err != nil {
			return PendingCaseIntakesPage{}, err
		}
		count := 0
		foundExtra := false
		for rows.Next() {
			row, scanErr := scanIntakeContinuation(rows)
			if scanErr != nil {
				rows.Close()
				return PendingCaseIntakesPage{}, scanErr
			}
			count++
			beforeTime, beforeID = &row.createdAt, row.operationID
			if !authorizeCase(row.item, current, access.Read) {
				continue
			}
			if len(page.Items) == limit {
				foundExtra = true
				break
			}
			identity, identityReason := row.requestIdentity()
			canResume, canArchive, reason := s.intakeActionFlags(current, row, identityReason)
			item := PendingCaseIntake{OperationID: row.operationID, CaseID: row.caseID, Title: row.item.Title,
				State: caseIntakePendingState, CreatedAt: row.createdAt.UTC().Format(timeLayout), UpdatedAt: row.updatedAt.UTC().Format(timeLayout),
				ErrorCode: caseIntakeUploadUnavailable, CanResume: canResume, CanArchive: canArchive, ReasonCode: reason}
			if identity != nil {
				item.Title, item.FileName, item.FileSize = identity.Title, identity.FileName, &identity.FileSize
			}
			page.Items = append(page.Items, item)
			lastVisibleTime, lastVisibleID = row.createdAt, row.operationID
		}
		rowsErr := rows.Err()
		rows.Close()
		if rowsErr != nil {
			return PendingCaseIntakesPage{}, rowsErr
		}
		if foundExtra {
			value := encodePendingIntakeCursor(current, lastVisibleTime, lastVisibleID)
			page.NextCursor = &value
			break
		}
		if count < limit+1 {
			break
		}
	}
	if _, err := trustedUser(current); err != nil {
		return PendingCaseIntakesPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PendingCaseIntakesPage{}, err
	}
	return page, nil
}

func loadOwnedIntakeContinuation(ctx context.Context, tx pgx.Tx, current accounts.User, operationID string, lock bool) (intakeContinuationRow, error) {
	query := intakeContinuationSelect + ` WHERE o.tenant_id=$1 AND o.actor_id=$2 AND o.operation_id=$3`
	if lock {
		query += ` FOR UPDATE OF o`
	}
	row, err := scanIntakeContinuation(tx.QueryRow(ctx, query, accounts.Tenant(current), current.ID, operationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return intakeContinuationRow{}, fault.NotFound("建案操作不存在")
	}
	if err != nil {
		return intakeContinuationRow{}, err
	}
	if !authorizeCase(row.item, current, access.Read) {
		return intakeContinuationRow{}, fault.NotFound("建案操作不存在")
	}
	return row, nil
}

// GetCaseIntakeContinuation releases the original replay identity only to its
// owner, after current case access and the stored request digest are checked.
func (s *Store) GetCaseIntakeContinuation(ctx context.Context, user accounts.User, operationID string) (CaseIntakeContinuation, error) {
	ctx = commandContext(ctx, user)
	current, err := trustedUser(user)
	if err != nil {
		return CaseIntakeContinuation{}, err
	}
	if !validCaseIntakeID(operationID) {
		return CaseIntakeContinuation{}, fault.NotFound("建案操作不存在")
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return CaseIntakeContinuation{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return CaseIntakeContinuation{}, err
	}
	row, err := loadOwnedIntakeContinuation(ctx, tx, current, operationID, false)
	if err != nil {
		return CaseIntakeContinuation{}, err
	}
	identity, identityReason := row.requestIdentity()
	canResume, canArchive, reason := s.intakeActionFlags(current, row, identityReason)
	state := caseIntakePendingState
	if row.status == caseIntakeCompletedStatus {
		state = caseIntakeCompletedState
	}
	result := CaseIntakeContinuation{OperationID: row.operationID, CaseID: row.caseID, State: state,
		Request: identity, CanResume: canResume, CanArchive: canArchive, ReasonCode: reason}
	if canResume {
		result.IdempotencyKey = &row.idempotencyKey
	}
	if _, err := trustedUser(current); err != nil {
		return CaseIntakeContinuation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CaseIntakeContinuation{}, err
	}
	return result, nil
}

// ArchiveCaseIntake appends one personal fact. The operation row lock orders
// new attempts and completion against the archive without cancelling an upload
// that has already passed BeginCaseIntake.
func (s *Store) ArchiveCaseIntake(ctx context.Context, user accounts.User, operationID string) (CaseIntakeArchiveResult, error) {
	ctx = commandContext(ctx, user)
	current, err := trustedUser(user)
	if err != nil {
		return CaseIntakeArchiveResult{}, err
	}
	if !validCaseIntakeID(operationID) {
		return CaseIntakeArchiveResult{}, fault.NotFound("建案操作不存在")
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CaseIntakeArchiveResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return CaseIntakeArchiveResult{}, err
	}
	// The case advisory lock precedes the operation lock, matching case writers.
	var caseID string
	err = tx.QueryRow(ctx, `SELECT case_id FROM case_intake_operations WHERE tenant_id=$1 AND actor_id=$2 AND operation_id=$3`,
		accounts.Tenant(current), current.ID, operationID).Scan(&caseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return CaseIntakeArchiveResult{}, fault.NotFound("建案操作不存在")
	}
	if err != nil {
		return CaseIntakeArchiveResult{}, err
	}
	if err := scopedCaseLock(ctx, tx, caseID); err != nil {
		return CaseIntakeArchiveResult{}, err
	}
	row, err := loadOwnedIntakeContinuation(ctx, tx, current, operationID, true)
	if err != nil {
		return CaseIntakeArchiveResult{}, err
	}
	// A concurrent archive may have committed while the operation row lock was
	// awaited. Read the fact in a fresh READ COMMITTED statement after locking.
	var archivedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT archived_at FROM case_intake_archives WHERE tenant_id=$1 AND operation_id=$2`,
		accounts.Tenant(current), operationID).Scan(&archivedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return CaseIntakeArchiveResult{}, err
	}
	row.archivedAt = archivedAt
	if row.archivedAt == nil {
		if s.ReadOnly() {
			return CaseIntakeArchiveResult{}, fault.New(503, "read_only", "只读模式下不能归档待续办事项")
		}
		if !accounts.CanCreate(current.Role) || !accounts.CanUpload(current.Role) || !authorizeCase(row.item, current, access.Manage) {
			return CaseIntakeArchiveResult{}, fault.Forbidden("当前身份无权归档待续办事项")
		}
		if err := businessWritable(row.item); err != nil {
			return CaseIntakeArchiveResult{}, err
		}
		if row.status != caseIntakePendingStatus {
			return CaseIntakeArchiveResult{}, fault.Conflict("建案操作已完成，不能归档待续办事项")
		}
		var archivedAt time.Time
		err = tx.QueryRow(ctx, `INSERT INTO case_intake_archives (tenant_id,operation_id,archived_by)
			VALUES($1,$2,$3) RETURNING archived_at`, accounts.Tenant(current), operationID, current.ID).Scan(&archivedAt)
		if err != nil {
			return CaseIntakeArchiveResult{}, err
		}
		row.archivedAt = &archivedAt
	}
	if _, err := trustedUser(current); err != nil || !authorizeCase(row.item, current, access.Read) {
		return CaseIntakeArchiveResult{}, fault.NotFound("建案操作不存在")
	}
	result := CaseIntakeArchiveResult{OperationID: row.operationID, CaseID: row.caseID, ArchivedAt: row.archivedAt.UTC().Format(timeLayout)}
	if err := tx.Commit(ctx); err != nil {
		return CaseIntakeArchiveResult{}, err
	}
	return result, nil
}
