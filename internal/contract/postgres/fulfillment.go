package postgres

import (
	"context"
	"errors"

	contractapp "contractaudit/internal/contract/app"
	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
	platform "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

// FulfillmentContracts adapts the Contract-owned revision and aggregate
// boundary for Fulfillment through the caller's existing transaction.
type FulfillmentContracts struct {
	tx       platform.Executor
	commands *DraftCommands
	queries  *VersionQueries
}

func NewFulfillmentContracts(tx platform.Executor) *FulfillmentContracts {
	return &FulfillmentContracts{
		tx: tx, commands: NewDraftCommands(tx), queries: NewVersionQueries(tx),
	}
}

func (r *FulfillmentContracts) LockWriteReceipt(ctx context.Context, tenantID, actorID, route, key string) error {
	return r.commands.LockWriteReceipt(ctx, tenantID, actorID, route, key)
}

func (r *FulfillmentContracts) LoadWriteReceipt(ctx context.Context, tenantID, actorID, route, key string) (clmcontract.WriteReceipt, bool, error) {
	return r.commands.LoadWriteReceipt(ctx, tenantID, actorID, route, key)
}

func (r *FulfillmentContracts) InsertWriteReceipt(ctx context.Context, tenantID, actorID, route, key, digest, contractID string, status int, response []byte) error {
	return r.commands.InsertWriteReceipt(ctx, tenantID, actorID, route, key, digest, contractID, status, response)
}

func (r *FulfillmentContracts) LockContract(ctx context.Context, contractID string) error {
	return r.commands.LockContract(ctx, contractID)
}

func (r *FulfillmentContracts) LoadContract(ctx context.Context, tenantID, contractID string) (clmcontract.Aggregate, error) {
	return r.commands.LoadContract(ctx, tenantID, contractID)
}

func (r *FulfillmentContracts) LoadFulfillmentRevision(ctx context.Context, tenantID, contractID, revisionID string) (clmcontract.FrozenContractRevision, error) {
	revision, err := r.queries.LoadFrozenRevision(ctx, tenantID, contractID, revisionID)
	if err == nil {
		return revision, nil
	}
	var known *fault.Error
	if errors.As(err, &known) && known.Status == 404 {
		return clmcontract.FrozenContractRevision{}, fault.Conflict("合同冻结修订不存在")
	}
	return clmcontract.FrozenContractRevision{}, err
}

func (r *FulfillmentContracts) LoadFulfillmentRevisionItem(ctx context.Context, tenantID, contractID, revisionID, itemID string) (clmcontract.RevisionPlanItem, bool, error) {
	var item clmcontract.RevisionPlanItem
	var planned, unit *string
	err := r.tx.QueryRow(ctx, `SELECT item_id::text,planned_quantity::text,unit
		FROM clm_contract_revision_items
		WHERE tenant_id=$1 AND contract_id=$2 AND contract_revision_id=$3 AND item_id=$4`,
		tenantID, contractID, revisionID, itemID).Scan(&item.ItemID, &planned, &unit)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmcontract.RevisionPlanItem{}, false, nil
	}
	if err != nil {
		return clmcontract.RevisionPlanItem{}, false, err
	}
	item.PlannedQuantity, item.Unit = planned, unit
	return item, true, nil
}

func (r *FulfillmentContracts) ListFulfillmentRevisionItems(ctx context.Context, tenantID, contractID string) ([]contractapp.FulfillmentRevisionItem, error) {
	rows, err := r.tx.Query(ctx, `SELECT contract_revision_id::text,item_id::text,planned_quantity::text,unit
		FROM clm_contract_revision_items
		WHERE tenant_id=$1 AND contract_id=$2
		ORDER BY contract_revision_id,item_id`, tenantID, contractID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []contractapp.FulfillmentRevisionItem{}
	for rows.Next() {
		var item contractapp.FulfillmentRevisionItem
		if err := rows.Scan(&item.RevisionID, &item.Item.ItemID, &item.Item.PlannedQuantity, &item.Item.Unit); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *FulfillmentContracts) AdvanceFulfillmentAggregate(ctx context.Context, record clmcontract.Aggregate, expected int64) (clmcontract.Aggregate, error) {
	err := r.tx.QueryRow(ctx, `UPDATE clm_contracts SET row_version=row_version+1,
		lifecycle_state=CASE WHEN lifecycle_state='registered' THEN 'performing' ELSE lifecycle_state END,
		updated_at=clock_timestamp()
		WHERE tenant_id=$1 AND contract_id=$2 AND row_version=$3
		  AND authority_mode='native' AND validity_state='active'
		RETURNING row_version,updated_at`, record.TenantID, record.ContractID, expected).
		Scan(&record.RowVersion, &record.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return clmcontract.Aggregate{}, fault.Conflict("合同已被修改，请刷新后重新确认")
	}
	if err != nil {
		return clmcontract.Aggregate{}, err
	}
	if record.LifecycleState == "registered" {
		record.LifecycleState = "performing"
	}
	return record, nil
}
