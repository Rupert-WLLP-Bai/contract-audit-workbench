package store

import (
	"context"
	"crypto/sha256"
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

const (
	caseIntakeMaxFileBytes      = 32 << 20
	caseIntakePendingStatus     = "pending"
	caseIntakeCompletedStatus   = "completed"
	caseIntakeCompletedHTTP     = 201
	caseIntakePendingState      = "upload_pending"
	caseIntakeCompletedState    = "completed"
	caseIntakeUploadUnavailable = "document_upload_unavailable"
)

// CaseIntakeUploader performs the one initial primary-contract upload for an
// intake operation. The Document service owns this mutation; Core never writes
// document rows itself in a distributed deployment.
type CaseIntakeUploader interface {
	UploadPrimaryContract(context.Context, string, PrimaryContractUploadRequest) (DocumentUploadResult, error)
}

// PrimaryContractUploadRequest is the fixed add/draft_contract/primary_contract
// upload Core requests from Document. Identity comes from the current trusted
// session and is revalidated by Document before it commits.
type PrimaryContractUploadRequest struct {
	ActorID        string
	TenantID       string
	OrgID          string
	Filename       string
	MediaType      string
	Body           []byte
	IdempotencyKey string
}

// CaseIntakeInput is one idempotent home-page intake request.
type CaseIntakeInput struct {
	Title          string
	ContractType   string
	BusinessLine   string
	Scenario       string
	Filename       string
	MediaType      string
	Body           []byte
	IdempotencyKey string
}

// CaseIntakeOperation is the internal operation projection. ReceiptBody holds
// the exact frozen public 201 bytes and is never re-encoded for a replay.
type CaseIntakeOperation struct {
	OperationID string             `json:"operation_id"`
	CaseID      string             `json:"case_id"`
	Status      string             `json:"status"`
	UploadKey   string             `json:"-"`
	Receipt     *CaseIntakeReceipt `json:"receipt,omitempty"`
	ReceiptBody []byte             `json:"-"`
	HTTPStatus  int                `json:"-"`
	Digest      string             `json:"-"`
}

// CaseIntakeReceipt is the complete flat public completed response.
type CaseIntakeReceipt struct {
	OperationID     string `json:"operation_id"`
	CaseID          string `json:"case_id"`
	State           string `json:"state"`
	MaterialID      string `json:"material_id"`
	MaterialVersion int    `json:"material_version"`
	DocumentID      string `json:"document_id"`
	Retryable       bool   `json:"retryable"`
}

// CaseIntakePendingResponse is the complete flat public pending response. It
// contains no upstream error or secret, only the stable continuation identity.
type CaseIntakePendingResponse struct {
	OperationID string `json:"operation_id"`
	CaseID      string `json:"case_id"`
	State       string `json:"state"`
	Retryable   bool   `json:"retryable"`
	ErrorCode   string `json:"error_code"`
}

// PendingBody returns the exact bytes for one pending response.
func (operation CaseIntakeOperation) PendingBody() []byte {
	body, _ := json.Marshal(CaseIntakePendingResponse{
		OperationID: operation.OperationID,
		CaseID:      operation.CaseID,
		State:       caseIntakePendingState,
		Retryable:   true,
		ErrorCode:   caseIntakeUploadUnavailable,
	})
	return body
}

// DirectCaseIntakeUploader performs the Document-owned upload in the embedded
// single-process runtime where Core and Document share one store owner. It is
// never used by an independent Core process.
type DirectCaseIntakeUploader struct {
	store *Store
}

// NewDirectCaseIntakeUploader returns the embedded runtime uploader.
func NewDirectCaseIntakeUploader(st *Store) DirectCaseIntakeUploader {
	return DirectCaseIntakeUploader{store: st}
}

// UploadPrimaryContract applies the same fixed upload contract as the Document
// service's authenticated internal entry.
func (uploader DirectCaseIntakeUploader) UploadPrimaryContract(ctx context.Context, caseID string, request PrimaryContractUploadRequest) (DocumentUploadResult, error) {
	if uploader.store == nil {
		return DocumentUploadResult{}, errors.New("direct intake uploader has no store")
	}
	user := accounts.User{ID: request.ActorID, TenantID: request.TenantID, OrgID: request.OrgID}
	return uploader.store.UploadMaterial(ctx, user, caseID, "draft_contract", request.Filename, request.MediaType, request.Body,
		MaterialUploadOptions{Intent: "add", Purpose: "primary_contract", IdempotencyKey: request.IdempotencyKey})
}

type caseIntakeRecord struct {
	TenantID       string
	OrgID          string
	OperationID    string
	ActorID        string
	IdempotencyKey string
	RequestDigest  string
	CaseID         string
	UploadKey      string
	Status         string
	HTTPStatus     int
	ResponseBody   []byte
	ResponseDigest string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Archived       bool
}

func normalizeCaseIntakeInput(input CaseIntakeInput) (CaseIntakeInput, businessCaseInput, CaseIntakeRequestIdentity, string, error) {
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return CaseIntakeInput{}, businessCaseInput{}, CaseIntakeRequestIdentity{}, "", fault.Invalid(err.Error())
	}
	if len(input.Body) > caseIntakeMaxFileBytes {
		return CaseIntakeInput{}, businessCaseInput{}, CaseIntakeRequestIdentity{}, "", fault.TooLarge("文件超过 32 MiB")
	}
	caseInput, err := validateBusinessCaseInput(input.Title, input.ContractType, input.BusinessLine, input.Scenario)
	if err != nil {
		return CaseIntakeInput{}, businessCaseInput{}, CaseIntakeRequestIdentity{}, "", err
	}
	input.Title, input.ContractType = caseInput.title, caseInput.contractType
	input.BusinessLine, input.Scenario = caseInput.businessLine, caseInput.scenario
	input.Filename, input.MediaType = cleanName(input.Filename), cleanMedia(input.MediaType)
	if strings.TrimSpace(input.Filename) == "" {
		return CaseIntakeInput{}, businessCaseInput{}, CaseIntakeRequestIdentity{}, "", fault.Invalid("文件名不能为空")
	}
	for _, char := range input.Filename {
		if char < 0x20 || char == 0x7f {
			return CaseIntakeInput{}, businessCaseInput{}, CaseIntakeRequestIdentity{}, "", fault.Invalid("文件名包含无效字符")
		}
	}
	identity := CaseIntakeRequestIdentity{
		Title: input.Title, ContractType: input.ContractType, BusinessLine: input.BusinessLine, Scenario: input.Scenario,
		FileName: input.Filename, FileSize: int64(len(input.Body)), MediaType: input.MediaType, FileSHA256: caseIntakeDigest(input.Body),
	}
	return input, caseInput, identity, caseIntakeRequestDigest(identity), nil
}

func caseIntakeRequestDigest(identity CaseIntakeRequestIdentity) string {
	canonical := struct {
		Title        string `json:"title"`
		ContractType string `json:"contract_type"`
		BusinessLine string `json:"business_line"`
		Scenario     string `json:"scenario"`
		Intent       string `json:"intent"`
		Role         string `json:"role"`
		Purpose      string `json:"purpose"`
		Filename     string `json:"filename"`
		MediaType    string `json:"media_type"`
		FileSHA256   string `json:"file_sha256"`
	}{identity.Title, identity.ContractType, identity.BusinessLine, identity.Scenario, "add", "draft_contract", "primary_contract", identity.FileName, identity.MediaType, identity.FileSHA256}
	encoded, _ := json.Marshal(canonical)
	return caseIntakeDigest(encoded)
}

func caseIntakeDigest(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

// BeginCaseIntake atomically creates the operation, case, creator authorization
// and creation activity, or returns the existing operation for the same
// tenant+actor+key. The transaction commits before any Document/object I/O.
func (s *Store) BeginCaseIntake(ctx context.Context, user accounts.User, input CaseIntakeInput) (CaseIntakeOperation, error) {
	if s.ReadOnly() {
		return CaseIntakeOperation{}, fault.New(503, "read_only", "只读模式下不能创建案件")
	}
	current, err := trustedUser(user)
	if err != nil {
		return CaseIntakeOperation{}, err
	}
	if !accounts.CanCreate(current.Role) {
		return CaseIntakeOperation{}, fault.Forbidden("当前角色不能创建案件")
	}
	input, caseInput, identity, digest, err := normalizeCaseIntakeInput(input)
	if err != nil {
		return CaseIntakeOperation{}, err
	}
	caseID, err := newID("case_")
	if err != nil {
		return CaseIntakeOperation{}, err
	}
	operationID, err := newID("intake_")
	if err != nil {
		return CaseIntakeOperation{}, err
	}
	uploadKey := "intake-upload-" + strings.TrimPrefix(operationID, "intake_")
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	now := createdAt.Format(timeLayout)
	var result CaseIntakeOperation
	var prior *caseIntakeRecord

	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.updateStateWithLocks(ctx, userScope(current), "", nil, []string{caseID}, func(tx pgx.Tx) error {
		record, err := loadCaseIntakeByKey(ctx, tx, accounts.Tenant(current), current.ID, input.IdempotencyKey, true)
		if err == nil {
			if record.Status == caseIntakePendingStatus {
				record.Archived, err = caseIntakeArchived(ctx, tx, record.TenantID, record.OperationID)
				if err != nil {
					return err
				}
			}
			prior = &record
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO case_intake_operations
			(tenant_id,org_id,operation_id,actor_id,idempotency_key,request_digest,case_id,upload_idempotency_key,status,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,'pending',$9,$9)`, accounts.Tenant(current), current.OrgID, operationID,
			current.ID, input.IdempotencyKey, digest, caseID, uploadKey, createdAt)
		if err != nil {
			return err
		}
		requestJSON, err := json.Marshal(identity)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO case_intake_requests
			(tenant_id,operation_id,schema_version,request_json,created_at) VALUES($1,$2,1,$3,$4)`,
			accounts.Tenant(current), operationID, requestJSON, createdAt)
		return err
	}, func(next *state) error {
		if prior != nil {
			item, err := s.visibleCase(next, current, prior.CaseID)
			if err != nil {
				return fault.NotFound("建案操作不存在")
			}
			if !authorizeCase(item, current, access.Read) {
				return fault.NotFound("建案操作不存在")
			}
			if prior.Status == caseIntakePendingStatus && prior.Archived {
				return fault.New(409, "intake_archived", "此待续办事项已归档")
			}
			if prior.RequestDigest != digest {
				return fault.Conflict("Idempotency-Key已用于不同建案请求")
			}
			result, err = prior.operation()
			return err
		}
		if err := appendBusinessCase(next, current, caseInput, caseID, now); err != nil {
			return err
		}
		result = CaseIntakeOperation{OperationID: operationID, CaseID: caseID, Status: caseIntakePendingStatus, UploadKey: uploadKey}
		return nil
	})
	if err != nil {
		return CaseIntakeOperation{}, err
	}
	return result, nil
}

func loadCaseIntakeByKey(ctx context.Context, tx pgx.Tx, tenantID, actorID, key string, lock bool) (caseIntakeRecord, error) {
	query := `SELECT tenant_id,org_id,operation_id,actor_id,idempotency_key,request_digest,case_id,upload_idempotency_key,status,response_status,response_body,response_digest,created_at,updated_at
		FROM case_intake_operations WHERE tenant_id=$1 AND actor_id=$2 AND idempotency_key=$3`
	if lock {
		query += ` FOR UPDATE`
	}
	return scanCaseIntake(tx.QueryRow(ctx, query, tenantID, actorID, key))
}

func loadCaseIntakeByID(ctx context.Context, tx pgx.Tx, tenantID, operationID string, lock bool) (caseIntakeRecord, error) {
	query := `SELECT tenant_id,org_id,operation_id,actor_id,idempotency_key,request_digest,case_id,upload_idempotency_key,status,response_status,response_body,response_digest,created_at,updated_at
		FROM case_intake_operations WHERE tenant_id=$1 AND operation_id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	return scanCaseIntake(tx.QueryRow(ctx, query, tenantID, operationID))
}

type rowScanner interface{ Scan(...any) error }

func scanCaseIntake(row rowScanner) (caseIntakeRecord, error) {
	var record caseIntakeRecord
	var responseStatus *int
	var body *[]byte
	var responseDigest *string
	if err := row.Scan(&record.TenantID, &record.OrgID, &record.OperationID, &record.ActorID, &record.IdempotencyKey, &record.RequestDigest,
		&record.CaseID, &record.UploadKey, &record.Status, &responseStatus, &body, &responseDigest, &record.CreatedAt, &record.UpdatedAt); err != nil {
		return caseIntakeRecord{}, err
	}
	if responseStatus != nil {
		record.HTTPStatus = *responseStatus
	}
	if body != nil {
		record.ResponseBody = append([]byte(nil), (*body)...)
	}
	if responseDigest != nil {
		record.ResponseDigest = *responseDigest
	}
	return record, nil
}

func (record caseIntakeRecord) operation() (CaseIntakeOperation, error) {
	operation := CaseIntakeOperation{
		OperationID: record.OperationID,
		CaseID:      record.CaseID,
		Status:      record.Status,
		UploadKey:   record.UploadKey,
		ReceiptBody: append([]byte(nil), record.ResponseBody...),
		HTTPStatus:  record.HTTPStatus,
		Digest:      record.ResponseDigest,
	}
	switch record.Status {
	case caseIntakePendingStatus:
		if record.HTTPStatus != 0 || len(record.ResponseBody) != 0 || record.ResponseDigest != "" {
			return CaseIntakeOperation{}, errors.New("pending case intake operation has completed response fields")
		}
		return operation, nil
	case caseIntakeCompletedStatus:
		if record.HTTPStatus != caseIntakeCompletedHTTP || len(record.ResponseBody) == 0 || record.ResponseDigest == "" {
			return CaseIntakeOperation{}, errors.New("completed case intake operation lacks a frozen response")
		}
		if record.ResponseDigest != caseIntakeDigest(record.ResponseBody) {
			return CaseIntakeOperation{}, errors.New("case intake response digest does not match its frozen body")
		}
		var receipt CaseIntakeReceipt
		if err := json.Unmarshal(record.ResponseBody, &receipt); err != nil {
			return CaseIntakeOperation{}, errors.New("case intake response body is not a valid receipt")
		}
		if receipt.OperationID != record.OperationID || receipt.CaseID != record.CaseID || receipt.State != caseIntakeCompletedState ||
			!validResourceID(receipt.MaterialID) || !validResourceID(receipt.DocumentID) || receipt.MaterialVersion != 1 || receipt.Retryable {
			return CaseIntakeOperation{}, errors.New("case intake receipt does not match its operation")
		}
		operation.Receipt = &receipt
		return operation, nil
	default:
		return CaseIntakeOperation{}, errors.New("case intake operation has an unknown status")
	}
}

func (s *Store) readCaseIntake(ctx context.Context, user accounts.User, load func(context.Context, pgx.Tx, accounts.User) (caseIntakeRecord, error)) (CaseIntakeOperation, error) {
	current, err := trustedUser(user)
	if err != nil {
		return CaseIntakeOperation{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return CaseIntakeOperation{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return CaseIntakeOperation{}, err
	}
	record, err := load(ctx, tx, current)
	if errors.Is(err, pgx.ErrNoRows) {
		return CaseIntakeOperation{}, fault.NotFound("建案操作不存在")
	}
	if err != nil {
		return CaseIntakeOperation{}, err
	}
	st, err := loadState(ctx, tx, false)
	if err != nil {
		return CaseIntakeOperation{}, err
	}
	item, err := s.visibleCase(&st, current, record.CaseID)
	if err != nil {
		return CaseIntakeOperation{}, fault.NotFound("建案操作不存在")
	}
	if !authorizeCase(item, current, access.Read) {
		return CaseIntakeOperation{}, fault.NotFound("建案操作不存在")
	}
	if record.Status == caseIntakePendingStatus {
		record.Archived, err = caseIntakeArchived(ctx, tx, record.TenantID, record.OperationID)
		if err != nil {
			return CaseIntakeOperation{}, err
		}
		if record.Archived {
			return CaseIntakeOperation{}, fault.New(409, "intake_archived", "此待续办事项已归档")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return CaseIntakeOperation{}, err
	}
	return record.operation()
}

// GetCaseIntake returns one operation by ID and rechecks the current actor's
// case read access before exposing the frozen response.
func (s *Store) GetCaseIntake(ctx context.Context, user accounts.User, operationID string) (CaseIntakeOperation, error) {
	if !validCaseIntakeID(operationID) {
		return CaseIntakeOperation{}, fault.NotFound("建案操作不存在")
	}
	return s.readCaseIntake(ctx, user, func(ctx context.Context, tx pgx.Tx, current accounts.User) (caseIntakeRecord, error) {
		return loadCaseIntakeByID(ctx, tx, accounts.Tenant(current), operationID, false)
	})
}

// GetCaseIntakeByKey recovers the current trusted tenant+actor operation for an
// idempotency key without re-uploading the file.
func (s *Store) GetCaseIntakeByKey(ctx context.Context, user accounts.User, key string) (CaseIntakeOperation, error) {
	if err := validateIdempotencyKey(key); err != nil {
		return CaseIntakeOperation{}, fault.Invalid(err.Error())
	}
	return s.readCaseIntake(ctx, user, func(ctx context.Context, tx pgx.Tx, current accounts.User) (caseIntakeRecord, error) {
		return loadCaseIntakeByKey(ctx, tx, accounts.Tenant(current), current.ID, key, false)
	})
}

// CompleteCaseIntake conditionally freezes the exact public 201 response once.
// A retry after completion returns the frozen bytes; it never re-encodes them.
func (s *Store) CompleteCaseIntake(ctx context.Context, user accounts.User, operationID string, upload DocumentUploadResult) (CaseIntakeOperation, error) {
	if s.ReadOnly() {
		return CaseIntakeOperation{}, fault.New(503, "read_only", "只读模式下不能完成建案上传")
	}
	current, err := trustedUser(user)
	if err != nil {
		return CaseIntakeOperation{}, err
	}
	if !validCaseIntakeID(operationID) {
		return CaseIntakeOperation{}, fault.NotFound("建案操作不存在")
	}
	var result CaseIntakeOperation
	var tx pgx.Tx
	s.mu.Lock()
	defer s.mu.Unlock()
	// The hook runs inside the same transaction immediately before fn, so it
	// hands fn the transaction the state mutation will commit with.
	err = s.updateStateWithLocks(ctx, userScope(current), "", nil, nil, func(transaction pgx.Tx) error {
		tx = transaction
		return nil
	}, func(next *state) error {
		if tx == nil {
			return errors.New("case intake completion transaction is unavailable")
		}
		record, err := loadCaseIntakeByID(ctx, tx, accounts.Tenant(current), operationID, true)
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.NotFound("建案操作不存在")
		}
		if err != nil {
			return err
		}
		item, err := s.visibleCase(next, current, record.CaseID)
		if err != nil {
			return fault.NotFound("建案操作不存在")
		}
		if record.Status == caseIntakeCompletedStatus {
			if !authorizeCase(item, current, access.Read) {
				return fault.NotFound("建案操作不存在")
			}
			result, err = record.operation()
			return err
		}
		if record.Status != caseIntakePendingStatus {
			return errors.New("case intake operation has an unknown status")
		}
		if !authorizeCase(item, current, access.Manage) {
			return fault.Forbidden("当前身份无权完成建案上传")
		}
		if err := businessWritable(item); err != nil {
			return err
		}
		if err := validateCaseIntakeUpload(next, record.CaseID, upload); err != nil {
			return err
		}
		receipt := CaseIntakeReceipt{
			OperationID: record.OperationID, CaseID: record.CaseID, State: caseIntakeCompletedState,
			MaterialID: upload.MaterialID, MaterialVersion: upload.Version, DocumentID: upload.DocumentID, Retryable: false,
		}
		body, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		digest := caseIntakeDigest(body)
		commandTag, err := tx.Exec(ctx, `UPDATE case_intake_operations
			SET status='completed',response_status=201,response_body=$3,response_digest=$4,updated_at=clock_timestamp()
			WHERE tenant_id=$1 AND operation_id=$2 AND status='pending'`, accounts.Tenant(current), operationID, body, digest)
		if err != nil {
			return err
		}
		if commandTag.RowsAffected() != 1 {
			return fault.Conflict("建案操作状态已变化，请重新读取")
		}
		result = CaseIntakeOperation{OperationID: record.OperationID, CaseID: record.CaseID, Status: caseIntakeCompletedStatus,
			Receipt: &receipt, ReceiptBody: body, HTTPStatus: caseIntakeCompletedHTTP, Digest: digest}
		return nil
	})
	if err != nil {
		return CaseIntakeOperation{}, err
	}
	return result, nil
}

func validateCaseIntakeUpload(st *state, caseID string, upload DocumentUploadResult) error {
	if !validResourceID(upload.DocumentID) || !validResourceID(upload.MaterialID) || upload.Version != 1 {
		return errors.New("primary contract upload result is invalid")
	}
	var document *Document
	for i := range st.Documents {
		if st.Documents[i].ID == upload.DocumentID {
			document = &st.Documents[i]
			break
		}
	}
	if document == nil || document.CaseID != caseID || document.Role != "draft_contract" {
		return errors.New("primary contract upload document does not belong to the intake case")
	}
	var material *MaterialEntity
	for i := range st.Materials {
		if st.Materials[i].ID == upload.MaterialID {
			material = &st.Materials[i]
			break
		}
	}
	if material == nil || material.CaseID != caseID || material.Role != "draft_contract" || material.Purpose != "primary_contract" {
		return errors.New("primary contract upload material does not match the intake case")
	}
	for _, version := range st.MaterialVersions {
		if version.CaseID == caseID && version.MaterialID == upload.MaterialID && version.Version == upload.Version && version.DocumentID == upload.DocumentID {
			return nil
		}
	}
	return errors.New("primary contract material version is missing")
}

func validCaseIntakeID(value string) bool {
	return strings.HasPrefix(value, "intake_") && validResourceID(value)
}
