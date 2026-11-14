package postgres

import (
	"context"

	"contractaudit/internal/operator/app"
	"contractaudit/internal/operator/model"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

type Evaluation struct{ tx platformpostgres.Executor }

func NewEvaluation(tx platformpostgres.Executor) *Evaluation { return &Evaluation{tx: tx} }
func (p *Evaluation) EvaluationIDByKey(ctx context.Context, id app.CreateIdentity, key string) (string, error) {
	var value string
	err := p.tx.QueryRow(ctx, `SELECT evaluation_id FROM operator_fulfillment_evaluations WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`, id.TenantID, id.OrgID, id.CaseID, id.ActorID, key).Scan(&value)
	return value, err
}
func (p *Evaluation) EvaluationRaw(ctx context.Context, caseID, id string) (app.EvaluationRow, error) {
	var row app.EvaluationRow
	v := &row.Record.Value
	err := p.tx.QueryRow(ctx, `SELECT case_id,evaluation_id,pack_id,pack_version,source_run_id,source_attempt_id,COALESCE(contract_version_id,''),rule_pack_version,COALESCE(binding_version,''),COALESCE(snapshot_set_id,''),business_at,input_json,result_json,result_sha256,machine_status,created_at,request_sha256 FROM operator_fulfillment_evaluations WHERE tenant_id=current_setting('app.tenant_id',true) AND org_id=current_setting('app.org_id',true) AND case_id=$1 AND evaluation_id=$2`, caseID, id).Scan(&v.CaseID, &v.EvaluationID, &v.PackID, &v.PackVersion, &v.SourceReviewID, &v.SourceAttemptID, &v.ContractVersionID, &v.RulePackVersion, &v.ObjectBindingVersion, &v.SnapshotSetID, &v.BusinessAt, &row.Input, &row.Result, &v.ResultSHA256, &v.MachineStatus, &v.CreatedAt, &row.Record.RequestSHA256)
	return row, err
}
func (p *Evaluation) PreviousEvaluationID(ctx context.Context, caseID string, v model.OperatorEvaluation) (string, error) {
	var id string
	err := p.tx.QueryRow(ctx, `SELECT evaluation_id FROM operator_fulfillment_evaluations WHERE tenant_id=current_setting('app.tenant_id',true) AND org_id=current_setting('app.org_id',true) AND case_id=$1 AND pack_id=$2 AND pack_version=$3 AND binding_version=$4 AND created_at < $5 ORDER BY created_at DESC,evaluation_id DESC LIMIT 1`, caseID, v.PackID, v.PackVersion, v.ObjectBindingVersion, v.CreatedAt).Scan(&id)
	return id, err
}

type evaluationCheckRows struct{ pgx.Rows }

func (r evaluationCheckRows) ScanCheck() (app.CheckRow, error) {
	var v app.CheckRow
	err := r.Scan(&v.Result, &v.Dependencies)
	return v, err
}
func (p *Evaluation) Checks(ctx context.Context, caseID, id string) (app.CheckCursor, error) {
	rows, err := p.tx.Query(ctx, `SELECT result_json,dependencies FROM operator_fulfillment_checks WHERE tenant_id=current_setting('app.tenant_id',true) AND org_id=current_setting('app.org_id',true) AND case_id=$1 AND evaluation_id=$2 ORDER BY check_id`, caseID, id)
	if err != nil {
		return nil, err
	}
	return evaluationCheckRows{rows}, nil
}

type evaluationObjectRows struct{ pgx.Rows }

func (r evaluationObjectRows) ScanObject() (app.EvaluationObjectRow, error) {
	var v app.EvaluationObjectRow
	err := r.Scan(&v.ObjectRef, &v.Facts, &v.FactsSHA, &v.Result, &v.ResultSHA)
	return v, err
}
func (p *Evaluation) EvaluationObjects(ctx context.Context, caseID, id string) (app.EvaluationObjectCursor, error) {
	rows, err := p.tx.Query(ctx, `SELECT object_ref,facts_canonical_json,facts_sha256,result_json,result_sha256 FROM operator_fulfillment_evaluation_objects WHERE tenant_id=current_setting('app.tenant_id',true) AND org_id=current_setting('app.org_id',true) AND case_id=$1 AND evaluation_id=$2 ORDER BY ordinal`, caseID, id)
	if err != nil {
		return nil, err
	}
	return evaluationObjectRows{rows}, nil
}
