package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"contractaudit/internal/contract/ledger"
	"contractaudit/internal/fault"
	platform "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// SavedViewCommands persists only Contract-owned personal ledger preferences.
// The caller owns identity, RLS scope, transaction lifetime and commit.
type SavedViewCommands struct{ tx platform.Executor }

// NewSavedViewCommands binds saved-view SQL to the caller's existing transaction.
func NewSavedViewCommands(tx platform.Executor) *SavedViewCommands {
	return &SavedViewCommands{tx: tx}
}

func (r *SavedViewCommands) List(ctx context.Context, identity ledger.SavedViewIdentity) ([]ledger.SavedView, error) {
	rows, err := r.tx.Query(ctx, `SELECT view_id,name,filter,columns,row_version,created_at,updated_at
		FROM clm_contract_saved_views WHERE tenant_id=$1 AND owner_user_id=$2
		ORDER BY updated_at DESC,view_id DESC`, identity.TenantID, identity.ActorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ledger.SavedView{}
	for rows.Next() {
		item, err := scanSavedView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *SavedViewCommands) Create(ctx context.Context, identity ledger.SavedViewIdentity,
	view ledger.SavedView) (ledger.SavedView, error) {
	filterJSON, err := json.Marshal(view.Filter)
	if err != nil {
		return ledger.SavedView{}, err
	}
	columnsJSON, err := json.Marshal(view.Columns)
	if err != nil {
		return ledger.SavedView{}, err
	}
	item := view
	err = r.tx.QueryRow(ctx, `INSERT INTO clm_contract_saved_views
		(tenant_id,view_id,owner_user_id,name,filter,columns)
		VALUES ($1,$2,$3,$4,$5::jsonb,$6::jsonb)
		RETURNING row_version,created_at,updated_at`, identity.TenantID, item.ViewID,
		identity.ActorID, item.Name, string(filterJSON), string(columnsJSON)).Scan(
		&item.RowVersion, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return ledger.SavedView{}, savedViewUniqueViolation(err)
	}
	return item, nil
}

func (r *SavedViewCommands) Replace(ctx context.Context, identity ledger.SavedViewIdentity, viewID string,
	expectedRowVersion int64, replacement ledger.SavedView) (ledger.SavedView, error) {
	filterJSON, err := json.Marshal(replacement.Filter)
	if err != nil {
		return ledger.SavedView{}, err
	}
	columnsJSON, err := json.Marshal(replacement.Columns)
	if err != nil {
		return ledger.SavedView{}, err
	}
	item := replacement
	item.ViewID = viewID
	err = r.tx.QueryRow(ctx, `UPDATE clm_contract_saved_views
		SET name=$4,filter=$5::jsonb,columns=$6::jsonb,row_version=row_version+1,updated_at=clock_timestamp()
		WHERE tenant_id=$1 AND view_id=$2 AND owner_user_id=$3 AND row_version=$7
		RETURNING row_version,created_at,updated_at`, identity.TenantID, viewID, identity.ActorID,
		item.Name, string(filterJSON), string(columnsJSON), expectedRowVersion).Scan(
		&item.RowVersion, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ledger.SavedView{}, r.savedViewMutationMiss(ctx, identity, viewID)
	}
	if err != nil {
		return ledger.SavedView{}, savedViewUniqueViolation(err)
	}
	return item, nil
}

func (r *SavedViewCommands) Delete(ctx context.Context, identity ledger.SavedViewIdentity, viewID string,
	expectedRowVersion int64) error {
	tag, err := r.tx.Exec(ctx, `DELETE FROM clm_contract_saved_views
		WHERE tenant_id=$1 AND view_id=$2 AND owner_user_id=$3 AND row_version=$4`,
		identity.TenantID, viewID, identity.ActorID, expectedRowVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return r.savedViewMutationMiss(ctx, identity, viewID)
	}
	return nil
}

type savedViewRowScanner interface {
	Scan(dest ...any) error
}

func scanSavedView(row savedViewRowScanner) (ledger.SavedView, error) {
	var item ledger.SavedView
	var filterJSON, columnsJSON []byte
	if err := row.Scan(&item.ViewID, &item.Name, &filterJSON, &columnsJSON, &item.RowVersion,
		&item.CreatedAt, &item.UpdatedAt); err != nil {
		return ledger.SavedView{}, err
	}
	filter, columns, err := ledger.DecodeStoredSavedView(filterJSON, columnsJSON)
	if err != nil {
		return ledger.SavedView{}, err
	}
	item.Filter, item.Columns = filter, columns
	return item, nil
}

func (r *SavedViewCommands) savedViewMutationMiss(ctx context.Context, identity ledger.SavedViewIdentity, viewID string) error {
	var liveVersion int64
	err := r.tx.QueryRow(ctx, `SELECT row_version FROM clm_contract_saved_views
		WHERE tenant_id=$1 AND view_id=$2 AND owner_user_id=$3`,
		identity.TenantID, viewID, identity.ActorID).Scan(&liveVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.NotFound("没有找到对应的保存视图")
	}
	if err != nil {
		return err
	}
	return fault.Conflict("保存视图已被修改，请刷新后重试")
}

func savedViewUniqueViolation(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fault.Conflict("同一名称的保存视图已存在")
	}
	return err
}
