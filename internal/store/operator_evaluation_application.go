package store

import (
	"context"
	"time"

	"contractaudit/internal/accounts"
	operatorApp "contractaudit/internal/operator/app"
	operatorPostgres "contractaudit/internal/operator/postgres"
	platformpostgres "contractaudit/internal/platform/postgres"
	"contractaudit/internal/rulepack"

	"github.com/jackc/pgx/v5"
)

type outerEvaluationFacts struct{ operatorPreviewFacts }

func (f outerEvaluationFacts) AgentDigest(_ context.Context, id string) (string, error) {
	return f.store.agentReviewDigestFor(f.item.TenantID, f.item.OwnerOrgID, id)
}

type evaluationAggregateRepo struct {
	*operatorPostgres.Mapping
	*operatorPostgres.Collection
	*operatorPostgres.Evaluation
}

func newEvaluationAggregateRepo(tx pgx.Tx) evaluationAggregateRepo {
	bound := platformpostgres.Bind(tx)
	return evaluationAggregateRepo{operatorPostgres.NewMapping(bound), operatorPostgres.NewCollection(bound), operatorPostgres.NewEvaluation(bound)}
}

type aggregateEvidenceFacts struct{ state state }

func (f aggregateEvidenceFacts) AttemptDocuments(id string) (string, []FrozenDocument, bool) {
	input, ok := f.state.attemptInput(id)
	return input.RunID, input.Documents, ok
}
func (aggregateEvidenceFacts) VerifySource(_ context.Context, doc FrozenDocument) error {
	_, err := telecomSources(interpretedFromFrozen([]FrozenDocument{doc}))
	return err
}

func (aggregateEvidenceFacts) Sources(_ context.Context, doc FrozenDocument) ([]rulepack.Source, error) {
	return telecomSources(interpretedFromFrozen([]FrozenDocument{doc}))
}

type evaluationClock struct{}

func (evaluationClock) Now() time.Time                   { return time.Now() }
func (evaluationClock) NewEvaluationID() (string, error) { return newID("eval_") }

type singleEvaluationFacts struct {
	outerEvaluationFacts
	user   accounts.User
	caseID string
}

func (f singleEvaluationFacts) ReadBinding(ctx context.Context, id string) (OperatorObjectBinding, error) {
	return f.store.operatorReadBinding(ctx, f.user, f.caseID, id)
}
func (f singleEvaluationFacts) ReadSnapshot(ctx context.Context, id string) (OperatorSnapshotSet, error) {
	return f.store.operatorReadSnapshotSet(ctx, f.user, f.caseID, id)
}
func (f singleEvaluationFacts) ReadAssociation(ctx context.Context, id string, version int) (ServiceObjectContractAssociation, error) {
	return f.store.GetServiceObjectAssociation(ctx, f.user, f.caseID, id, version)
}
func (f singleEvaluationFacts) RunConfiguration(runID string) operatorApp.AggregateConfiguration {
	v := f.state.snapshotOf(runID)
	return operatorApp.AggregateConfiguration{Model: v.ModelConfigurationID, Planner: v.PlannerConfigurationID, Parser: v.ParserConfigurationID}
}
func (f singleEvaluationFacts) Sources(ctx context.Context, doc FrozenDocument) ([]rulepack.Source, error) {
	return aggregateEvidenceFacts{}.Sources(ctx, doc)
}
func (aggregateEvidenceFacts) SourcesBatch(_ context.Context, docs []FrozenDocument) ([]rulepack.Source, error) {
	return telecomSources(interpretedFromFrozen(docs))
}

type outerAggregateFacts struct {
	outerEvaluationFacts
	aggregateEvidenceFacts
}
