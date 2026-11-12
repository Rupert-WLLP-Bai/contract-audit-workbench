package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
	platform "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DraftCommands persists one draft command through the caller's already
// authorized, non-owning transaction handle.
type DraftCommands struct{ tx platform.Executor }

func NewDraftCommands(tx platform.Executor) *DraftCommands { return &DraftCommands{tx: tx} }

func (r *DraftCommands) LockWriteReceipt(ctx context.Context, tenantID, actorID, route, key string) error {
	identityBytes, err := json.Marshal([]string{tenantID, actorID, route, key})
	if err != nil {
		return err
	}
	identityHash := sha256.Sum256(identityBytes)
	identity := hex.EncodeToString(identityHash[:])
	_, err = r.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':write-receipt:' || $1, 0))`, identity)
	return err
}

func (r *DraftCommands) LoadWriteReceipt(ctx context.Context, tenantID, actorID, route, key string) (clmcontract.WriteReceipt, bool, error) {
	var receipt clmcontract.WriteReceipt
	err := r.tx.QueryRow(ctx, `SELECT request_digest,contract_id,response_status,response_json
		FROM clm_contract_write_receipts
		WHERE tenant_id=$1 AND actor_id=$2 AND route_scope=$3 AND idempotency_key=$4`,
		tenantID, actorID, route, key).Scan(&receipt.RequestDigest, &receipt.ContractID, &receipt.ResponseStatus, &receipt.Response)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmcontract.WriteReceipt{}, false, nil
	}
	if err != nil {
		return clmcontract.WriteReceipt{}, false, err
	}
	return receipt, true, nil
}

func (r *DraftCommands) LoadContract(ctx context.Context, tenantID, contractID string) (clmcontract.Aggregate, error) {
	return scanContractAggregate(r.tx.QueryRow(ctx, `SELECT tenant_id,contract_id,owner_org_id,authority_mode,contract_number,
		source_system,external_contract_id,source_version,lifecycle_state,validity_state,
		current_revision_id,row_version,created_by,created_at,updated_at
		FROM clm_contracts WHERE tenant_id=$1 AND contract_id=$2`, tenantID, contractID))
}

func (r *DraftCommands) LockContract(ctx context.Context, contractID string) error {
	_, err := r.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':clm-contract:' || $1, 0))`, contractID)
	return err
}

// ReplaceDraft advances the aggregate with the existing row-version CAS and
// replaces its working content in the same caller-owned transaction.
func (r *DraftCommands) ReplaceDraft(ctx context.Context, record clmcontract.Aggregate,
	expectedRowVersion int64, content []byte, actorID string) (clmcontract.Aggregate, error) {
	err := r.tx.QueryRow(ctx, `UPDATE clm_contracts SET row_version=row_version+1, updated_at=clock_timestamp()
		WHERE tenant_id=$1 AND contract_id=$2 AND row_version=$3
			AND authority_mode='native' AND lifecycle_state='draft' AND validity_state='active'
		RETURNING row_version, updated_at`,
		record.TenantID, record.ContractID, expectedRowVersion).Scan(&record.RowVersion, &record.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmcontract.Aggregate{}, fault.Conflict("合同内容已被他人修改，请刷新后确认")
	}
	if err != nil {
		return clmcontract.Aggregate{}, err
	}
	if _, err := r.tx.Exec(ctx, `INSERT INTO clm_contract_drafts (tenant_id,contract_id,content,updated_by)
		VALUES ($1,$2,$3::jsonb,$4)
		ON CONFLICT (tenant_id,contract_id) DO UPDATE
		SET content=EXCLUDED.content, updated_by=EXCLUDED.updated_by, updated_at=clock_timestamp()`,
		record.TenantID, record.ContractID, string(content), actorID); err != nil {
		return clmcontract.Aggregate{}, err
	}
	return record, nil
}

func (r *DraftCommands) CreateDraftAggregate(ctx context.Context, record clmcontract.Aggregate, content []byte, actorID string) (time.Time, error) {
	var createdAt time.Time
	err := r.tx.QueryRow(ctx, `INSERT INTO clm_contracts
		(tenant_id,contract_id,owner_org_id,authority_mode,contract_number,lifecycle_state,row_version,created_by)
		VALUES ($1,$2,$3,'native',$4,'draft',1,$5) RETURNING created_at`,
		record.TenantID, record.ContractID, record.OwnerOrgID, record.ContractNumber, actorID).Scan(&createdAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return time.Time{}, fault.Conflict("合同编号已被使用")
		}
		return time.Time{}, err
	}
	if _, err := r.tx.Exec(ctx, `INSERT INTO clm_contract_drafts (tenant_id,contract_id,content,updated_by)
		VALUES ($1,$2,$3::jsonb,$4)`, record.TenantID, record.ContractID, string(content), actorID); err != nil {
		return time.Time{}, err
	}
	return createdAt, nil
}

func (r *DraftCommands) InsertWriteReceipt(ctx context.Context, tenantID, actorID, route, key, digest, contractID string, status int, response []byte) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO clm_contract_write_receipts
		(tenant_id,actor_id,route_scope,idempotency_key,request_digest,contract_id,response_status,response_json)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb)`,
		tenantID, actorID, route, key, digest, contractID, status, string(response))
	return err
}

func scanContractAggregate(row pgx.Row) (clmcontract.Aggregate, error) {
	var record clmcontract.Aggregate
	err := row.Scan(&record.TenantID, &record.ContractID, &record.OwnerOrgID, &record.AuthorityMode,
		&record.ContractNumber, &record.SourceSystem, &record.ExternalContractID, &record.SourceVersion,
		&record.LifecycleState, &record.ValidityState, &record.CurrentRevisionID, &record.RowVersion,
		&record.CreatedBy, &record.CreatedAt, &record.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmcontract.Aggregate{}, fault.NotFound("没有找到对应记录")
	}
	return record, err
}
