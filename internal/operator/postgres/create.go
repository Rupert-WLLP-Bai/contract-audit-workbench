// Package postgres contains caller-bound Operator persistence.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"contractaudit/internal/operator/app"
	"contractaudit/internal/operator/model"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

type Create struct{ tx platformpostgres.Executor }

func NewCreate(tx platformpostgres.Executor) *Create { return &Create{tx: tx} }
func (p *Create) ReadCreateReceipt(ctx context.Context, id app.CreateIdentity, key string) (app.Receipt, bool, error) {
	var v app.Receipt
	err := p.tx.QueryRow(ctx, `SELECT request_sha256,response_status,response_body FROM service_object_mutation_receipts
 WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND operation=$5 AND idempotency_key=$6`, id.TenantID, id.OrgID, id.CaseID, id.ActorID, "create_object", key).Scan(&v.RequestSHA, &v.Status, &v.Body)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}
func (p *Create) CountObjects(ctx context.Context, id app.CreateIdentity) (int, error) {
	var n int
	err := p.tx.QueryRow(ctx, `SELECT count(*) FROM service_objects WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3`, id.TenantID, id.OrgID, id.CaseID).Scan(&n)
	return n, err
}
func (p *Create) InsertCreateHead(ctx context.Context, id app.CreateIdentity, objectID string, now time.Time) error {
	_, err := p.tx.Exec(ctx, `INSERT INTO service_objects
 (tenant_id,org_id,case_id,service_object_id,current_version,created_by,created_at)
 VALUES ($1,$2,$3,$4,1,$5,$6)`, id.TenantID, id.OrgID, id.CaseID, objectID, id.ActorID, now)
	return err
}
func (p *Create) InsertCreateVersion(ctx context.Context, id app.CreateIdentity, v model.ServiceObjectVersion, aliases json.RawMessage) error {
	_, err := p.tx.Exec(ctx, `INSERT INTO service_object_versions
 (tenant_id,org_id,case_id,service_object_id,version,object_type,display_name,business_code,aliases,state,effective_from,basis_refs,basis_summary,payload_sha256,created_by,created_at)
 VALUES ($1,$2,$3,$4,1,$5,$6,NULLIF($7,''),$8::jsonb,'pending_confirmation',$9,'[]'::jsonb,NULLIF($10,''),$11,$12,$13)`, id.TenantID, id.OrgID, id.CaseID, v.ServiceObjectID, v.ObjectType, v.DisplayName, v.BusinessCode, aliases, v.EffectiveFrom, v.BasisSummary, v.PayloadSHA256, id.ActorID, v.CreatedAt)
	return err
}
func (p *Create) InsertCreateReceipt(ctx context.Context, id app.CreateIdentity, key, hash string, body []byte, objectID string, now time.Time) error {
	_, err := p.tx.Exec(ctx, `INSERT INTO service_object_mutation_receipts
 (tenant_id,org_id,case_id,actor_id,operation,idempotency_key,request_sha256,response_status,response_body,entity_id,created_at)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id.TenantID, id.OrgID, id.CaseID, id.ActorID, "create_object", key, hash, 201, body, objectID, now.UTC().Truncate(time.Microsecond))
	return err
}
