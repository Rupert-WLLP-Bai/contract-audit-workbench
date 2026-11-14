package postgres

import (
	"context"

	"contractaudit/internal/auditext"
	platformpostgres "contractaudit/internal/platform/postgres"
)

type Impact struct{ tx platformpostgres.Executor }

func NewImpact(tx platformpostgres.Executor) *Impact { return &Impact{tx: tx} }
func (p *Impact) Append(ctx context.Context, caseID, actor string, plan auditext.ImpactPlan, body []byte, sha string) error {
	_, err := p.tx.Exec(ctx, `INSERT INTO operator_impact_plans (tenant_id,org_id,case_id,base_evaluation_id,target_evaluation_id,mode,plan_json,plan_sha256,created_by,created_at) VALUES (current_setting('app.tenant_id',true),current_setting('app.org_id',true),$1,$2,$3,$4,$5,$6,$7,clock_timestamp()) ON CONFLICT DO NOTHING`, caseID, plan.BaseEvaluationID, plan.TargetEvaluationID, string(plan.Mode), body, sha, actor)
	return err
}
