package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var lifecycleDigestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func mapTenantProvisionError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "P7T01":
		return fault.New(409, "tenant_operation_idempotency_conflict", "幂等键已用于不同租户操作")
	case "P7T02":
		return fault.NotFound("没有找到对应的租户操作")
	case "P7T03", "P7T05", "P7T06":
		return fault.New(409, "tenant_operation_state_conflict", "租户操作当前状态或前置条件不允许此步骤")
	case "P7T04":
		return fault.New(409, "tenant_operation_revision_conflict", "租户操作已更新，请刷新后重试")
	case "22023":
		return fault.Invalid("租户操作输入无效")
	case "42501":
		return fault.Forbidden("当前身份未获平台租户控制授权")
	default:
		return err
	}
}

func (s *Store) beginPlatformTenantControl(ctx context.Context, user accounts.User, write bool) (pgx.Tx, accounts.User, error) {
	current, err := trustedUser(user)
	if err != nil {
		return nil, accounts.User{}, err
	}
	if current.Role != "admin" || !configuredAllowlistContains("CAW_PLATFORM_OPERATOR_ACTORS", current.ID) {
		return nil, accounts.User{}, fault.Forbidden("当前身份未获平台租户控制授权")
	}
	if write && s.ReadOnly() {
		return nil, accounts.User{}, fault.New(503, "read_only", "维护模式不允许修改平台租户控制")
	}
	ctx = commandContext(ctx, current)
	options := pgx.TxOptions{}
	if !write {
		options.AccessMode = pgx.ReadOnly
	}
	tx, err := s.beginContextTx(ctx, options)
	if err != nil {
		return nil, accounts.User{}, err
	}
	if current.TenantID != "" {
		if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
			_ = tx.Rollback(ctx)
			return nil, accounts.User{}, err
		}
	} else if _, err := tx.Exec(ctx, `SELECT set_config('app.actor_id',$1,true)`, current.ID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, accounts.User{}, err
	}
	var sessionUser, dbUser string
	if err := tx.QueryRow(ctx, `SELECT session_user,current_user`).Scan(&sessionUser, &dbUser); err != nil {
		_ = tx.Rollback(ctx)
		return nil, accounts.User{}, err
	}
	if sessionUser == "" || dbUser == "" || sessionUser != dbUser || !configuredAllowlistContains("CAW_PLATFORM_OPERATOR_DATABASE_PRINCIPALS", sessionUser) {
		_ = tx.Rollback(ctx)
		return nil, accounts.User{}, fault.Forbidden("当前 PostgreSQL 登录主体未获平台租户控制授权")
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.platform_operator_actor',$1,true)`, current.ID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, accounts.User{}, err
	}
	return tx, current, nil
}

func decodeTenantOperation(raw []byte) (TenantProvisionOperationResult, error) {
	var out TenantProvisionOperationResult
	if err := json.Unmarshal(raw, &out); err != nil || out.Operation.ID == "" {
		return TenantProvisionOperationResult{}, errors.New("tenant provision operation response is invalid")
	}
	return out, nil
}

func (s *Store) ListTenantProvisionOperations(ctx context.Context, user accounts.User, cursor string, limit int) (TenantProvisionOperationsView, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return TenantProvisionOperationsView{}, fault.Invalid("租户操作分页上限无效")
	}
	tx, current, err := s.beginPlatformTenantControl(ctx, user, false)
	if err != nil {
		return TenantProvisionOperationsView{}, err
	}
	defer tx.Rollback(ctx)
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT tenant_provision_operations_list($1,$2,$3)`, current.ID, cursor, limit).Scan(&raw); err != nil {
		return TenantProvisionOperationsView{}, err
	}
	var out TenantProvisionOperationsView
	if err := json.Unmarshal(raw, &out); err != nil || out.SchemaVersion != "tenant-provision-operations/v1" {
		return TenantProvisionOperationsView{}, errors.New("tenant provision operation list response is invalid")
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantProvisionOperationsView{}, err
	}
	return out, nil
}

func (s *Store) StartTenantProvisionOperation(ctx context.Context, user accounts.User, input TenantProvisionInput) (TenantProvisionOperationResult, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.TenantID = strings.TrimSpace(input.TenantID)
	input.TenantName = strings.TrimSpace(input.TenantName)
	input.JurisdictionID = strings.TrimSpace(input.JurisdictionID)
	input.AdminPrincipalID = strings.TrimSpace(input.AdminPrincipalID)
	input.AdminOrgUnitID = strings.TrimSpace(input.AdminOrgUnitID)
	input.AdminOrgUnitName = strings.TrimSpace(input.AdminOrgUnitName)
	input.TenantConfigVersion = strings.TrimSpace(input.TenantConfigVersion)
	if input.IdempotencyKey == "" || len(input.IdempotencyKey) > 160 || !oneOf(input.Kind, "provision", "deactivate", "reactivate") || input.TenantID == "" ||
		(input.Kind != "deactivate" && (input.TenantName == "" || input.JurisdictionID == "" || input.AdminPrincipalID == "" ||
			input.AdminOrgUnitID == "" || input.AdminOrgUnitName == "" || input.TenantConfigVersion == "")) {
		return TenantProvisionOperationResult{}, fault.Invalid("租户开通操作字段无效")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return TenantProvisionOperationResult{}, err
	}
	digest := sha256.Sum256(payload)
	tx, current, err := s.beginPlatformTenantControl(ctx, user, true)
	if err != nil {
		return TenantProvisionOperationResult{}, err
	}
	defer tx.Rollback(ctx)
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT tenant_provision_operation_start($1,$2,$3::jsonb,$4,$5)`, current.ID, input.IdempotencyKey, payload, hex.EncodeToString(digest[:]), current.ID).Scan(&raw)
	if err != nil {
		return TenantProvisionOperationResult{}, mapTenantProvisionError(err)
	}
	out, err := decodeTenantOperation(raw)
	if err != nil {
		return TenantProvisionOperationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantProvisionOperationResult{}, err
	}
	return out, nil
}

func (s *Store) ReadTenantProvisionOperation(ctx context.Context, user accounts.User, operationID string) (TenantProvisionOperationResult, error) {
	tx, current, err := s.beginPlatformTenantControl(ctx, user, false)
	if err != nil {
		return TenantProvisionOperationResult{}, err
	}
	defer tx.Rollback(ctx)
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT tenant_provision_operation_read($1,$2)`, current.ID, operationID).Scan(&raw); err != nil {
		return TenantProvisionOperationResult{}, mapTenantProvisionError(err)
	}
	out, err := decodeTenantOperation(raw)
	if err != nil {
		return TenantProvisionOperationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantProvisionOperationResult{}, err
	}
	return out, nil
}

func (s *Store) AdvanceTenantProvisionOperation(ctx context.Context, user accounts.User, operationID string, input TenantProvisionStepInput) (TenantProvisionOperationResult, error) {
	expected, err := strconv.ParseInt(input.ExpectedRevision, 10, 64)
	if err != nil || expected < 1 || !oneOf(input.State, "complete", "external_unknown", "failed", "reconciled") ||
		!oneOf(input.Reconciliation, "", "confirmed_complete", "confirmed_not_applied", "requires_operator") ||
		(input.ReceiptSHA256 != nil && !lifecycleDigestRE.MatchString(*input.ReceiptSHA256)) ||
		((input.State == "complete" || input.State == "reconciled") && input.ReceiptSHA256 == nil) {
		return TenantProvisionOperationResult{}, fault.Invalid("租户操作步骤字段无效")
	}
	tx, current, err := s.beginPlatformTenantControl(ctx, user, true)
	if err != nil {
		return TenantProvisionOperationResult{}, err
	}
	defer tx.Rollback(ctx)
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT tenant_provision_operation_advance($1,$2,$3,$4,$5,$6,$7)`, current.ID, operationID, expected, input.State, input.ReceiptSHA256, input.Reconciliation, current.ID).Scan(&raw); err != nil {
		return TenantProvisionOperationResult{}, mapTenantProvisionError(err)
	}
	out, err := decodeTenantOperation(raw)
	if err != nil {
		return TenantProvisionOperationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantProvisionOperationResult{}, err
	}
	return out, nil
}

// TenantProvisionOperation is the durable, operator-visible record for one
// tenant onboarding or recovery attempt. Step state is a receipt, not proof
// that an external system performed the claimed action.
type TenantProvisionOperation struct {
	ID                     string                    `json:"operation_id"`
	TenantID               string                    `json:"tenant_id"`
	Kind                   string                    `json:"kind"`
	Status                 string                    `json:"status"`
	CurrentStep            string                    `json:"current_step"`
	PayloadSHA256          string                    `json:"payload_sha256"`
	Revision               string                    `json:"revision"`
	ReconciliationRequired bool                      `json:"reconciliation_required"`
	Steps                  []TenantProvisionStepView `json:"steps"`
	CreatedAt              string                    `json:"created_at"`
	UpdatedAt              string                    `json:"updated_at"`
}

type TenantProvisionStepView struct {
	Name          string  `json:"name"`
	State         string  `json:"state"`
	ReceiptSHA256 *string `json:"receipt_sha256"`
	UpdatedAt     *string `json:"updated_at"`
}

type TenantProvisionOperationsView struct {
	SchemaVersion string                     `json:"schema_version"`
	Operations    []TenantProvisionOperation `json:"operations"`
	NextCursor    *string                    `json:"next_cursor"`
}

// TenantProvisionInput is explicit desired state. Principal identifiers must
// already resolve through the trusted identity directory; the API never
// creates credentials or passwords.
type TenantProvisionInput struct {
	IdempotencyKey      string `json:"idempotency_key"`
	Kind                string `json:"kind"`
	TenantID            string `json:"tenant_id"`
	TenantName          string `json:"tenant_name"`
	JurisdictionID      string `json:"jurisdiction_id"`
	AdminPrincipalID    string `json:"admin_principal_id"`
	AdminOrgUnitID      string `json:"admin_org_unit_id"`
	AdminOrgUnitName    string `json:"admin_org_unit_name"`
	TenantConfigVersion string `json:"tenant_config_version"`
}

type TenantProvisionOperationResult struct {
	SchemaVersion string                   `json:"schema_version"`
	Operation     TenantProvisionOperation `json:"operation"`
}

type TenantProvisionStepInput struct {
	ExpectedRevision string  `json:"expected_revision"`
	State            string  `json:"state"`
	ReceiptSHA256    *string `json:"receipt_sha256"`
	Reconciliation   string  `json:"reconciliation"`
}
