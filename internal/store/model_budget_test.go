package store

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openModelBudgetStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func modelBudgetTestLimits() ModelBudgetLimits {
	return ModelBudgetLimits{
		GlobalMaxInFlight: 16, TenantMaxInFlight: 8,
		GlobalCallsPerMinute: 1000, TenantCallsPerMinute: 1000,
		GlobalTokensPerMinute: 1_000_000, TenantTokensPerMinute: 1_000_000,
		MaxInputTokenReservation: 100_000, MaxOutputTokenReservation: 100_000,
		LeaseDuration: time.Minute,
	}
}

func modelBudgetTestLease(t *testing.T, tenant string) ModelCallLease {
	t.Helper()
	id, err := newID("ml_budget_")
	if err != nil {
		t.Fatal(err)
	}
	return ModelCallLease{
		ID: id, TenantID: tenant, ActorID: "budget-test-actor", CaseID: "budget-test-case",
		RunID: "budget-test-run", AttemptID: "budget-test-attempt", CallerIdentity: "worker",
		ModelRef: "budget-test-model", AttemptNumber: 1,
	}
}

type modelBudgetQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func reserveModelBudgetDirect(ctx context.Context, q modelBudgetQueryer, schema string, lease ModelCallLease, limits ModelBudgetLimits, input, output int64) (bool, error) {
	leaseMillis := limits.LeaseDuration.Milliseconds()
	if leaseMillis < 1 {
		leaseMillis = 1
	}
	query := `SELECT ` + pgx.Identifier{schema, "model_budget_reserve"}.Sanitize() + `($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`
	var reserved bool
	err := q.QueryRow(ctx, query,
		lease.ID, lease.TenantID, lease.ActorID, lease.CaseID, lease.RunID, lease.AttemptID,
		lease.CallerIdentity, lease.ModelRef, lease.AttemptNumber,
		limits.GlobalMaxInFlight, limits.TenantMaxInFlight, limits.GlobalCallsPerMinute, limits.TenantCallsPerMinute,
		limits.GlobalTokensPerMinute, limits.TenantTokensPerMinute, input, output, leaseMillis,
	).Scan(&reserved)
	return reserved, err
}

func settlePredispatch(t *testing.T, ctx context.Context, st *Store, lease ModelCallLease) {
	t.Helper()
	if err := st.FinishModelCall(ctx, lease, ModelCallResult{
		Status: "failed", ErrorCode: "proven_pre_dispatch_failure", PreDispatchFailure: true,
	}); err != nil {
		t.Fatalf("release pre-dispatch reservation: %v", err)
	}
}

func openModelBudgetRolePools(t *testing.T, ctx context.Context, st *Store) (*pgxpool.Pool, *pgxpool.Pool, string) {
	t.Helper()
	modelDSN := os.Getenv("MODEL_DATABASE_URL")
	if modelDSN == "" {
		t.Skip("MODEL_DATABASE_URL is required for dedicated model-role budget checks")
	}
	modelConfig, err := pgxpool.ParseConfig(modelDSN)
	if err != nil {
		t.Fatal(err)
	}
	var testDatabase string
	if err := st.pool.QueryRow(ctx, `SELECT current_database()`).Scan(&testDatabase); err != nil {
		t.Fatal(err)
	}
	if modelConfig.ConnConfig.Database != testDatabase {
		t.Skip("MODEL_DATABASE_URL points at a different database from TEST_DATABASE_URL")
	}
	modelRole := modelConfig.ConnConfig.User
	qualifiedSchema := pgx.Identifier{st.schema}.Sanitize()
	reserveFunction := qualifiedSchema + `.model_budget_reserve(text,text,text,text,text,text,text,text,integer,integer,integer,integer,integer,bigint,bigint,bigint,bigint,bigint)`
	finishFunction := qualifiedSchema + `.model_budget_finish(text,text,text,text,text,text,text,text,integer,text,text,text,bigint,bigint,bigint,timestamptz,boolean,text)`
	roleIdentifier := pgx.Identifier{modelRole}.Sanitize()
	if _, err := st.pool.Exec(ctx, `GRANT USAGE ON SCHEMA `+qualifiedSchema+` TO `+roleIdentifier+
		`; GRANT EXECUTE ON FUNCTION `+reserveFunction+` TO `+roleIdentifier+
		`; GRANT EXECUTE ON FUNCTION `+finishFunction+` TO `+roleIdentifier); err != nil {
		t.Fatal(err)
	}
	firstPool, err := newPool(ctx, modelDSN, st.schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(firstPool.Close)
	secondPool, err := newPool(ctx, modelDSN, st.schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(secondPool.Close)
	return firstPool, secondPool, modelRole
}

func TestModelBudgetGlobalAndTenantInflightAcrossPools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st := openModelBudgetStore(t)

	firstPool, secondPool, _ := openModelBudgetRolePools(t, ctx, st)
	modelPools := []*pgxpool.Pool{firstPool, secondPool}

	limits := modelBudgetTestLimits()
	limits.GlobalMaxInFlight = 1
	limits.TenantMaxInFlight = 1
	leases := []ModelCallLease{
		modelBudgetTestLease(t, "budget-tenant-a"),
		modelBudgetTestLease(t, "budget-tenant-b"),
	}
	start := make(chan struct{})
	type reserveResult struct {
		lease    ModelCallLease
		reserved bool
		err      error
	}
	results := make(chan reserveResult, 2)
	var wg sync.WaitGroup
	for i, pool := range modelPools {
		wg.Add(1)
		go func(lease ModelCallLease, q *pgxpool.Pool) {
			defer wg.Done()
			<-start
			reserved, err := reserveModelBudgetDirect(ctx, q, st.schema, lease, limits, 2, 2)
			results <- reserveResult{lease: lease, reserved: reserved, err: err}
		}(leases[i], pool)
	}
	close(start)
	wg.Wait()
	close(results)
	var winner *ModelCallLease
	admitted := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.reserved {
			admitted++
			copy := result.lease
			winner = &copy
		}
	}
	if admitted != 1 || winner == nil {
		t.Fatalf("global in-flight cap admitted %d leases across two tenants", admitted)
	}
	settlePredispatch(t, ctx, st, *winner)

	limits.GlobalMaxInFlight = 8
	limits.TenantMaxInFlight = 1
	first := modelBudgetTestLease(t, "budget-tenant-same")
	second := modelBudgetTestLease(t, "budget-tenant-same")
	otherTenant := modelBudgetTestLease(t, "budget-tenant-other")
	if ok, err := reserveModelBudgetDirect(ctx, firstPool, st.schema, first, limits, 1, 1); err != nil || !ok {
		t.Fatalf("first tenant lease: reserved=%t err=%v", ok, err)
	}
	finishQuery := `SELECT ` + pgx.Identifier{st.schema, "model_budget_finish"}.Sanitize() + `($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`
	var finished bool
	if err := firstPool.QueryRow(ctx, finishQuery,
		first.ID, first.TenantID, first.ActorID, first.CaseID, first.RunID, first.AttemptID, first.CallerIdentity, first.ModelRef,
		first.AttemptNumber, "completed", "", nil, nil, nil, nil, time.Now().UTC(), true, "invalid-pre-dispatch-status",
	).Scan(&finished); err == nil {
		t.Fatal("SQL settlement allowed a pre-dispatch refund for a completed call")
	}
	if ok, err := reserveModelBudgetDirect(ctx, secondPool, st.schema, second, limits, 1, 1); err != nil || ok {
		t.Fatalf("tenant in-flight cap did not reject second lease: reserved=%t err=%v", ok, err)
	}
	if ok, err := reserveModelBudgetDirect(ctx, secondPool, st.schema, otherTenant, limits, 1, 1); err != nil || !ok {
		t.Fatalf("tenant cap leaked into another tenant: reserved=%t err=%v", ok, err)
	}
	settlePredispatch(t, ctx, st, first)
	settlePredispatch(t, ctx, st, otherTenant)
}

func TestModelBudgetTokenCapsAndKnownUsageSettlement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st := openModelBudgetStore(t)
	limits := modelBudgetTestLimits()
	limits.GlobalTokensPerMinute = 5
	limits.TenantTokensPerMinute = 5
	first := modelBudgetTestLease(t, "budget-token-a")
	if ok, err := reserveModelBudgetDirect(ctx, st.pool, st.schema, first, limits, 4, 1); err != nil || !ok {
		t.Fatalf("initial token reservation: reserved=%t err=%v", ok, err)
	}
	otherTenant := modelBudgetTestLease(t, "budget-token-b")
	if ok, err := reserveModelBudgetDirect(ctx, st.pool, st.schema, otherTenant, limits, 1, 1); err != nil || ok {
		t.Fatalf("global token cap admitted an over-budget tenant: reserved=%t err=%v", ok, err)
	}
	input, output, total := int64(1), int64(1), int64(2)
	known := ModelCallResult{
		Status: "completed", Usage: &model.TokenUsage{
			Source: "test-provider", InputTokens: &input, OutputTokens: &output, TotalTokens: &total,
		},
	}
	if err := st.FinishModelCall(ctx, first, known); err != nil {
		t.Fatal(err)
	}
	if ok, err := reserveModelBudgetDirect(ctx, st.pool, st.schema, otherTenant, limits, 1, 2); err != nil || !ok {
		t.Fatalf("known provider usage did not release the unused reservation: reserved=%t err=%v", ok, err)
	}
	settlePredispatch(t, ctx, st, otherTenant)

	limits.GlobalTokensPerMinute = 100
	limits.TenantTokensPerMinute = 5
	tenantLease := modelBudgetTestLease(t, "budget-token-tenant-cap")
	if ok, err := reserveModelBudgetDirect(ctx, st.pool, st.schema, tenantLease, limits, 4, 1); err != nil || !ok {
		t.Fatalf("tenant token reservation: reserved=%t err=%v", ok, err)
	}
	if ok, err := reserveModelBudgetDirect(ctx, st.pool, st.schema, modelBudgetTestLease(t, tenantLease.TenantID), limits, 1, 1); err != nil || ok {
		t.Fatalf("tenant token cap admitted an over-budget lease: reserved=%t err=%v", ok, err)
	}
	otherTenant = modelBudgetTestLease(t, "budget-token-other-tenant")
	if ok, err := reserveModelBudgetDirect(ctx, st.pool, st.schema, otherTenant, limits, 4, 1); err != nil || !ok {
		t.Fatalf("tenant token cap leaked into another tenant: reserved=%t err=%v", ok, err)
	}
	settlePredispatch(t, ctx, st, tenantLease)
	settlePredispatch(t, ctx, st, otherTenant)

	var charged int64
	var gotInput, gotOutput, gotTotal *int64
	if err := st.pool.QueryRow(ctx, `SELECT input_tokens,output_tokens,total_tokens,charged_tokens
		FROM model_usage WHERE lease_id=$1`, first.ID).Scan(&gotInput, &gotOutput, &gotTotal, &charged); err != nil {
		t.Fatal(err)
	}
	if gotInput == nil || *gotInput != 1 || gotOutput == nil || *gotOutput != 1 || gotTotal == nil || *gotTotal != 2 || charged != 2 {
		t.Fatalf("known provider usage was not settled from the provider: input=%v output=%v total=%v charged=%d", gotInput, gotOutput, gotTotal, charged)
	}
}

func TestModelBudgetUnknownExpiryKnownDuplicateAndRollback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st := openModelBudgetStore(t)
	limits := modelBudgetTestLimits()

	unknown := modelBudgetTestLease(t, "budget-unknown-tenant")
	if ok, err := reserveModelBudgetDirect(ctx, st.pool, st.schema, unknown, limits, 4, 5); err != nil || !ok {
		t.Fatalf("unknown usage reservation: reserved=%t err=%v", ok, err)
	}
	unknownResult := ModelCallResult{Status: "failed", ErrorCode: "provider_timeout"}
	if err := st.FinishModelCall(ctx, unknown, unknownResult); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishModelCall(ctx, unknown, unknownResult); err != nil {
		t.Fatalf("identical settlement was not idempotent: %v", err)
	}
	conflict := ModelCallResult{Status: "completed", ErrorCode: "provider_timeout"}
	if err := st.FinishModelCall(ctx, unknown, conflict); !errors.Is(err, ErrModelCallSettlementConflict) {
		t.Fatalf("conflicting settlement was accepted: %v", err)
	}
	var gotInput, gotOutput, gotTotal *int64
	var charged int64
	var released bool
	if err := st.pool.QueryRow(ctx, `SELECT input_tokens,output_tokens,total_tokens,charged_tokens,budget_released
		FROM model_usage WHERE lease_id=$1`, unknown.ID).Scan(&gotInput, &gotOutput, &gotTotal, &charged, &released); err != nil {
		t.Fatal(err)
	}
	if gotInput != nil || gotOutput != nil || gotTotal != nil || charged != 9 || released {
		t.Fatalf("unknown dispatched result did not retain its upper bound: input=%v output=%v total=%v charged=%d released=%t", gotInput, gotOutput, gotTotal, charged, released)
	}
	if _, err := st.pool.Exec(ctx, `UPDATE model_usage SET error_code='rewritten' WHERE lease_id=$1`, unknown.ID); err == nil {
		t.Fatal("historical model usage was mutable")
	}

	preDispatch := modelBudgetTestLease(t, "budget-predispatch-tenant")
	if ok, err := reserveModelBudgetDirect(ctx, st.pool, st.schema, preDispatch, limits, 4, 6); err != nil || !ok {
		t.Fatalf("pre-dispatch reservation: reserved=%t err=%v", ok, err)
	}
	settlePredispatch(t, ctx, st, preDispatch)
	if err := st.pool.QueryRow(ctx, `SELECT charged_tokens,budget_released,input_tokens,output_tokens,total_tokens
		FROM model_usage WHERE lease_id=$1`, preDispatch.ID).Scan(&charged, &released, &gotInput, &gotOutput, &gotTotal); err != nil {
		t.Fatal(err)
	}
	if charged != 0 || !released || gotInput != nil || gotOutput != nil || gotTotal != nil {
		t.Fatalf("proven pre-dispatch failure was not refunded: charged=%d released=%t usage=%v/%v/%v", charged, released, gotInput, gotOutput, gotTotal)
	}

	expired := modelBudgetTestLease(t, "budget-expiry-tenant")
	expiryLimits := limits
	expiryLimits.GlobalMaxInFlight = 1
	expiryLimits.TenantMaxInFlight = 1
	expiryLimits.LeaseDuration = 15 * time.Millisecond
	if ok, err := reserveModelBudgetDirect(ctx, st.pool, st.schema, expired, expiryLimits, 3, 4); err != nil || !ok {
		t.Fatalf("expiring reservation: reserved=%t err=%v", ok, err)
	}
	time.Sleep(30 * time.Millisecond)
	afterExpiry := modelBudgetTestLease(t, "budget-expiry-next-tenant")
	expiryLimits.LeaseDuration = time.Minute
	if ok, err := reserveModelBudgetDirect(ctx, st.pool, st.schema, afterExpiry, expiryLimits, 1, 1); err != nil || !ok {
		t.Fatalf("expired concurrency lease was not released: reserved=%t err=%v", ok, err)
	}
	var active int
	if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM model_call_leases WHERE lease_id=$1`, expired.ID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("expired lease still holds concurrency: %d rows", active)
	}
	if err := st.pool.QueryRow(ctx, `SELECT input_tokens,output_tokens,total_tokens,charged_tokens
		FROM model_usage WHERE lease_id=$1`, expired.ID).Scan(&gotInput, &gotOutput, &gotTotal, &charged); err != nil {
		t.Fatal(err)
	}
	if gotInput != nil || gotOutput != nil || gotTotal != nil || charged != 7 {
		t.Fatalf("expired possible dispatch lost its unknown upper-bound charge: usage=%v/%v/%v charged=%d", gotInput, gotOutput, gotTotal, charged)
	}
	settlePredispatch(t, ctx, st, afterExpiry)

	rollbackLease := modelBudgetTestLease(t, "budget-rollback-tenant")
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := reserveModelBudgetDirect(ctx, tx, st.schema, rollbackLease, limits, 2, 2); err != nil || !ok {
		_ = tx.Rollback(ctx)
		t.Fatalf("transactional reservation before rollback: reserved=%t err=%v", ok, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM model_call_leases WHERE lease_id=$1`, rollbackLease.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rolled-back reservation persisted: %d rows", count)
	}
	committed := modelBudgetTestLease(t, rollbackLease.TenantID)
	if ok, err := reserveModelBudgetDirect(ctx, st.pool, st.schema, committed, limits, 2, 2); err != nil || !ok {
		t.Fatalf("rolled-back reservation consumed budget: reserved=%t err=%v", ok, err)
	}
	settlePredispatch(t, ctx, st, committed)
}

func TestModelBudgetCallRateCapAcrossTenants(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st := openModelBudgetStore(t)
	limits := modelBudgetTestLimits()
	limits.GlobalCallsPerMinute = 1
	limits.TenantCallsPerMinute = 1
	first := modelBudgetTestLease(t, "budget-rate-a")
	if ok, err := reserveModelBudgetDirect(ctx, st.pool, st.schema, first, limits, 1, 1); err != nil || !ok {
		t.Fatalf("first rate reservation: reserved=%t err=%v", ok, err)
	}
	input, output, total := int64(1), int64(1), int64(2)
	if err := st.FinishModelCall(ctx, first, ModelCallResult{Status: "completed", Usage: &model.TokenUsage{
		Source: "test-provider", InputTokens: &input, OutputTokens: &output, TotalTokens: &total,
	}}); err != nil {
		t.Fatal(err)
	}
	if ok, err := reserveModelBudgetDirect(ctx, st.pool, st.schema, modelBudgetTestLease(t, "budget-rate-b"), limits, 1, 1); err != nil || ok {
		t.Fatalf("global per-minute call cap admitted a second tenant: reserved=%t err=%v", ok, err)
	}
}

func TestModelBudgetRoleIsolationAndFunctionPrivileges(t *testing.T) {
	modelDSN := os.Getenv("MODEL_DATABASE_URL")
	if modelDSN == "" {
		t.Skip("MODEL_DATABASE_URL is required for the dedicated model-role check")
	}
	modelConfig, err := pgxpool.ParseConfig(modelDSN)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st := openModelBudgetStore(t)
	var testDatabase string
	if err := st.pool.QueryRow(ctx, `SELECT current_database()`).Scan(&testDatabase); err != nil {
		t.Fatal(err)
	}
	if modelConfig.ConnConfig.Database != testDatabase {
		t.Skip("MODEL_DATABASE_URL points at a different database from TEST_DATABASE_URL")
	}
	modelRole := modelConfig.ConnConfig.User
	var privileged bool
	if err := st.pool.QueryRow(ctx, `SELECT r.rolsuper OR r.rolbypassrls OR r.rolcreaterole OR r.rolcreatedb OR
		pg_has_role(r.oid,n.nspowner,'MEMBER') OR EXISTS (SELECT 1 FROM pg_class c WHERE c.relnamespace=n.oid AND pg_has_role(r.oid,c.relowner,'MEMBER'))
		FROM pg_roles r CROSS JOIN pg_namespace n WHERE r.rolname=$1 AND n.nspname=$2`, modelRole, st.schema).Scan(&privileged); err != nil {
		t.Fatal(err)
	}
	if privileged {
		t.Fatalf("model role %q is privileged or owns accounting relations", modelRole)
	}
	if err := st.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
		CROSS JOIN LATERAL aclexplode(COALESCE(p.proacl,acldefault('f',p.proowner))) a
		WHERE n.nspname=$1 AND p.proname IN ('model_budget_reserve','model_budget_finish')
			AND a.grantee=0 AND a.privilege_type='EXECUTE')`, st.schema).Scan(&privileged); err != nil {
		t.Fatal(err)
	}
	if privileged {
		t.Fatal("model budget function remains executable by PUBLIC")
	}

	caseIDs := []string{"budget-role-case-visible", "budget-role-case-hidden"}
	tenantIDs := []string{"budget-role-visible", "budget-role-hidden"}
	insertTx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, tenantID := range tenantIDs {
		if err := setLocalScope(ctx, insertTx, dbScope{ActorID: "budget-role-test", Role: "api", TenantID: tenantID, OrgID: "budget-role-org"}); err != nil {
			_ = insertTx.Rollback(ctx)
			t.Fatal(err)
		}
		if _, err := insertTx.Exec(ctx, `INSERT INTO cases
			(id,title,contract_type,owner_id,member_ids,created_at,updated_at,tenant_id,org_id)
			VALUES ($1,'Budget role isolation','general','budget-role-owner','[]',clock_timestamp()::text,clock_timestamp()::text,$2,'budget-role-org')`, caseIDs[i], tenantID); err != nil {
			_ = insertTx.Rollback(ctx)
			t.Fatal(err)
		}
	}
	if err := insertTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	qualifiedSchema := pgx.Identifier{st.schema}.Sanitize()
	qualifiedCases := pgx.Identifier{st.schema, "cases"}.Sanitize()
	qualifiedUsage := pgx.Identifier{st.schema, "model_usage"}.Sanitize()
	modelRoleIdentifier := pgx.Identifier{modelRole}.Sanitize()
	if _, err := st.pool.Exec(ctx, `GRANT USAGE ON SCHEMA `+qualifiedSchema+` TO `+modelRoleIdentifier+`; GRANT SELECT ON TABLE `+qualifiedCases+` TO `+modelRoleIdentifier); err != nil {
		t.Fatal(err)
	}
	var canReadUsage bool
	if err := st.pool.QueryRow(ctx, `SELECT has_table_privilege($1,$2,'SELECT')`, modelRole, qualifiedUsage).Scan(&canReadUsage); err != nil {
		t.Fatal(err)
	}
	if canReadUsage {
		t.Fatal("dedicated model role can directly read model accounting rows")
	}
	modelPool, err := newPool(ctx, modelDSN, st.schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(modelPool.Close)
	tx, err := modelPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "budget-role-test", Role: "model", TenantID: tenantIDs[0], OrgID: "budget-role-org"}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	var visible int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+qualifiedCases+` WHERE id=ANY($1::text[])`, caseIDs).Scan(&visible); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if visible != 1 {
		_ = tx.Rollback(ctx)
		t.Fatalf("dedicated model role saw %d of two tenants' business rows; expected only its tenant row", visible)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+qualifiedUsage).Scan(&visible); err == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("dedicated model role could read the budget accounting table directly")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}

// waitForBudgetBlocker observes an actual PostgreSQL lock wait, so the race
// tests advance only after the competing transaction reaches the intended seam.
func waitForBudgetBlocker(t *testing.T, ctx context.Context, st *Store, blocker int32) int32 {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var pid *int32
		if err := st.pool.QueryRow(ctx, `SELECT (SELECT pid FROM pg_stat_activity
			WHERE $1::int = ANY(pg_blocking_pids(pid)) LIMIT 1)`, blocker).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		if pid != nil {
			return *pid
		}
		select {
		case <-ctx.Done():
			t.Fatalf("transaction %d did not block a competing transaction: %v", blocker, ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestExperienceBudgetReservationSerializesWithReclamation(t *testing.T) {
	st, user, source, _ := feedbackFixture(t, "false_positive")
	feedback, err := st.SubmitExperienceFeedback(context.Background(), user, source)
	if err != nil {
		t.Fatal(err)
	}
	st.UseModels(&distillPolicyTestModel{frozenOCRModel: &frozenOCRModel{}})
	receipt, err := st.RequestExperienceDistillation(context.Background(), user, ExperienceDistillRequest{
		CaseID: source.CaseID, FeedbackID: feedback.FeedbackID, IdempotencyKey: "budget-fence-race",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.ClaimNextJob(context.Background(), "budget-first-worker", time.Minute)
	if err != nil || first.JobID != receipt.JobID {
		t.Fatalf("first claim: %+v, %v", first, err)
	}
	work, err := st.loadExperienceDistillWork(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	scope := model.DelegatedScope{Kind: "experience_distill", TenantID: first.TenantID, OrgID: first.OrgID,
		CaseID: first.CaseID, ActorID: first.ActorID, JobID: first.JobID, WorkerID: first.WorkerID,
		Generation: first.Generation, InputSHA256: work.InputSHA256}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Each parameter and both transaction scope settings are part of the lock
	// boundary. A mismatched identity must not lock some other job.
	mutations := []struct {
		name   string
		change func(*model.DelegatedScope, *dbScope)
	}{
		{"tenant", func(s *model.DelegatedScope, _ *dbScope) { s.TenantID += "-other" }},
		{"org", func(s *model.DelegatedScope, _ *dbScope) { s.OrgID += "-other" }},
		{"case", func(s *model.DelegatedScope, _ *dbScope) { s.CaseID += "-other" }},
		{"actor", func(s *model.DelegatedScope, _ *dbScope) { s.ActorID += "-other" }},
		{"job", func(s *model.DelegatedScope, _ *dbScope) { s.JobID += "-other" }},
		{"worker", func(s *model.DelegatedScope, _ *dbScope) { s.WorkerID += "-other" }},
		{"generation", func(s *model.DelegatedScope, _ *dbScope) { s.Generation++ }},
		{"input", func(s *model.DelegatedScope, _ *dbScope) { s.InputSHA256 = sha256Bytes([]byte("different input")) }},
		{"session tenant", func(_ *model.DelegatedScope, s *dbScope) { s.TenantID += "-other" }},
		{"session org", func(_ *model.DelegatedScope, s *dbScope) { s.OrgID += "-other" }},
		{"session role", func(_ *model.DelegatedScope, s *dbScope) { s.Role = "worker" }},
	}
	lockQuery := `SELECT ` + pgx.Identifier{st.schema, "model_budget_lock_experience_job"}.Sanitize() + `($1,$2,$3,$4,$5,$6,$7,$8)`
	for _, mutation := range mutations {
		t.Run("reject "+mutation.name, func(t *testing.T) {
			candidate := scope
			session := dbScope{ActorID: "model-gateway", Role: "model", TenantID: scope.TenantID, OrgID: scope.OrgID}
			mutation.change(&candidate, &session)
			tx, err := st.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if err := setLocalScope(ctx, tx, session); err != nil {
				t.Fatal(err)
			}
			var locked bool
			if err := tx.QueryRow(ctx, lockQuery, candidate.TenantID, candidate.OrgID, candidate.CaseID, candidate.ActorID,
				candidate.JobID, candidate.WorkerID, candidate.Generation, candidate.InputSHA256).Scan(&locked); err != nil || locked {
				t.Fatalf("mismatched identity acquired a job lock: locked=%t err=%v", locked, err)
			}
		})
	}

	budgetBlocker, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer budgetBlocker.Rollback(context.Background())
	var budgetPID int32
	if err := budgetBlocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&budgetPID); err != nil {
		t.Fatal(err)
	}
	if _, err := budgetBlocker.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, st.schema+":model-budget:global"); err != nil {
		t.Fatal(err)
	}
	type reservation struct {
		lease ModelCallLease
		err   error
	}
	reservationDone := make(chan reservation, 1)
	go func() {
		lease, err := st.ReserveModelCall(ctx, "worker", scope, "race-model", 1, 20, 20, modelBudgetTestLimits())
		reservationDone <- reservation{lease: lease, err: err}
	}()
	reservationPID := waitForBudgetBlocker(t, ctx, st, budgetPID)

	// The request has authorized and is waiting for the budget mutex. Its job
	// must already be locked, so a queue claimant cannot skip ahead of it.
	probe, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctx, probe, first.scope()); err != nil {
		_ = probe.Rollback(context.Background())
		t.Fatal(err)
	}
	var jobID string
	err = probe.QueryRow(ctx, `SELECT id FROM worker_jobs WHERE id=$1 FOR UPDATE SKIP LOCKED`, first.JobID).Scan(&jobID)
	_ = probe.Rollback(context.Background())
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("reclamation could lock the job before the reservation committed: id=%q err=%v", jobID, err)
	}

	expireDone := make(chan error, 1)
	go func() {
		tx, err := st.pool.Begin(ctx)
		if err != nil {
			expireDone <- err
			return
		}
		defer tx.Rollback(context.Background())
		if err := setLocalScope(ctx, tx, first.scope()); err != nil {
			expireDone <- err
			return
		}
		tag, err := tx.Exec(ctx, `UPDATE worker_jobs SET lease_until=clock_timestamp()-interval '1 second',
			ready_at=clock_timestamp()-interval '1 second' WHERE id=$1 AND generation=$2`, first.JobID, first.Generation)
		if err == nil && tag.RowsAffected() != 1 {
			err = errors.New("the original generation was not expired")
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		expireDone <- err
	}()
	waitForBudgetBlocker(t, ctx, st, reservationPID)
	if err := budgetBlocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var reserved reservation
	select {
	case reserved = <-reservationDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if reserved.err != nil {
		t.Fatalf("reservation failed before reclamation: %v", reserved.err)
	}
	select {
	case err := <-expireDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	second, err := st.ClaimNextJob(ctx, "budget-second-worker", time.Minute)
	if err != nil || second.JobID != first.JobID || second.Generation <= first.Generation {
		t.Fatalf("reclaimed job: %+v, %v", second, err)
	}
	recovery, err := st.recoverExperienceDistill(ctx, second, work)
	if err != nil || !recovery.ModelOutcomeUnknown {
		t.Fatalf("new generation missed the older possible dispatch: recovery=%+v err=%v", recovery, err)
	}

	// Reverse the ordering: a takeover owns the row while an old generation is
	// requesting a reservation. The waiter must recheck the committed identity.
	takeover, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer takeover.Rollback(context.Background())
	if err := setLocalScope(ctx, takeover, second.scope()); err != nil {
		t.Fatal(err)
	}
	var takeoverPID int32
	if err := takeover.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&takeoverPID); err != nil {
		t.Fatal(err)
	}
	tag, err := takeover.Exec(ctx, `UPDATE worker_jobs SET generation=generation+1,worker_id='budget-third-worker',
		lease_until=clock_timestamp()+interval '1 minute' WHERE id=$1 AND generation=$2`, second.JobID, second.Generation)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("prepare takeover: rows=%d err=%v", tag.RowsAffected(), err)
	}
	stale := scope
	stale.WorkerID, stale.Generation = second.WorkerID, second.Generation
	staleDone := make(chan reservation, 1)
	go func() {
		lease, err := st.ReserveModelCall(ctx, "worker", stale, "race-model", 1, 20, 20, modelBudgetTestLimits())
		staleDone <- reservation{lease: lease, err: err}
	}()
	waitForBudgetBlocker(t, ctx, st, takeoverPID)
	if err := takeover.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-staleDone:
		if !errors.Is(result.err, ErrModelScopeUnavailable) {
			t.Fatalf("superseded generation reserved a model call: %+v", result)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	// The exposed accounting function cannot bypass the fence either.
	direct, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Rollback(context.Background())
	if err := setLocalScope(ctx, direct, dbScope{ActorID: "model-gateway", Role: "model", TenantID: stale.TenantID,
		OrgID: stale.OrgID, WorkerID: stale.WorkerID}); err != nil {
		t.Fatal(err)
	}
	if err := setModelLedgerIdentity(ctx, direct, stale.Kind, stale.JobID, stale.Generation, stale.InputSHA256); err != nil {
		t.Fatal(err)
	}
	directLease := ModelCallLease{ID: "ml_stale_direct", TenantID: stale.TenantID, ActorID: stale.ActorID,
		CaseID: stale.CaseID, CallerIdentity: "worker", ModelRef: "race-model", AttemptNumber: 1}
	_, err = reserveModelBudgetDirect(ctx, direct, st.schema, directLease, modelBudgetTestLimits(), 20, 20)
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "P7M01" {
		t.Fatalf("direct stale SQL reservation was not fenced: %v", err)
	}
	if err := direct.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	check, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Rollback(context.Background())
	if err := setLocalScope(ctx, check, second.scope()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := check.QueryRow(ctx, `SELECT count(*) FROM model_call_leases WHERE tenant_id=$1 AND job_id=$2
		AND purpose='experience_distill'`, first.TenantID, first.JobID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected only the committed first-generation reservation, got %d", count)
	}
	var generation int64
	var inputHash string
	if err := check.QueryRow(ctx, `SELECT job_generation,input_sha256 FROM model_call_leases WHERE lease_id=$1`, reserved.lease.ID).
		Scan(&generation, &inputHash); err != nil || generation != first.Generation || inputHash != work.InputSHA256 {
		t.Fatalf("reservation lost its recovery identity: generation=%d input=%q err=%v", generation, inputHash, err)
	}
}

func TestExperienceBudgetFenceRolePrivileges(t *testing.T) {
	environment, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for dedicated service-role checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st := environment.harness
	function := pgx.Identifier{st.schema}.Sanitize() + `.model_budget_lock_experience_job(text,text,text,text,text,text,bigint,text)`
	jobs := pgx.Identifier{st.schema, "worker_jobs"}.Sanitize()
	var securityDefiner, public, fixedPath bool
	if err := st.pool.QueryRow(ctx, `SELECT p.prosecdef,
		EXISTS (SELECT 1 FROM aclexplode(COALESCE(p.proacl,acldefault('f',p.proowner))) a
			WHERE a.grantee=0 AND a.privilege_type='EXECUTE'),
		COALESCE(p.proconfig @> ARRAY['search_path=pg_catalog, pg_temp'],false)
		FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
		WHERE n.nspname=$1 AND p.proname='model_budget_lock_experience_job'`, st.schema).
		Scan(&securityDefiner, &public, &fixedPath); err != nil {
		t.Fatal(err)
	}
	if !securityDefiner || public || !fixedPath {
		t.Fatalf("job fence privilege settings: security definer=%t public=%t fixed path=%t", securityDefiner, public, fixedPath)
	}
	for _, service := range []string{"core", "document", "execution", "model", "tool"} {
		config, err := pgxpool.ParseConfig(environment.roleDSNs[service])
		if err != nil {
			t.Fatal(err)
		}
		var executable bool
		if err := st.pool.QueryRow(ctx, `SELECT has_function_privilege($1,$2,'EXECUTE')`, config.ConnConfig.User, function).Scan(&executable); err != nil {
			t.Fatal(err)
		}
		if executable != (service == "model") {
			t.Fatalf("service %s has unexpected fence execute privilege: %t", service, executable)
		}
	}
	modelConfig, err := pgxpool.ParseConfig(environment.roleDSNs["model"])
	if err != nil {
		t.Fatal(err)
	}
	var canUpdate bool
	if err := st.pool.QueryRow(ctx, `SELECT has_table_privilege($1,$2,'UPDATE')`, modelConfig.ConnConfig.User, jobs).Scan(&canUpdate); err != nil {
		t.Fatal(err)
	}
	if canUpdate {
		t.Fatal("model role has direct worker job UPDATE privilege")
	}
	modelPool, err := newPool(ctx, environment.roleDSNs["model"], st.schema)
	if err != nil {
		t.Fatal(err)
	}
	defer modelPool.Close()
	for _, statement := range []string{
		`UPDATE worker_jobs SET worker_id=worker_id WHERE false`,
		`SELECT id FROM worker_jobs WHERE false FOR UPDATE`,
	} {
		tx, err := modelPool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, statement)
		_ = tx.Rollback(context.Background())
		var state interface{ SQLState() string }
		if !errors.As(err, &state) || state.SQLState() != "42501" {
			t.Fatalf("model role unexpectedly obtained direct job write/lock access: statement=%q err=%v", statement, err)
		}
	}
	// Exercise the fence against an actual live job using the provisioned model
	// role. Its bounded definer lock must work without direct job privileges.
	owner := revisionAdmin(t)
	st.UseModels(&frozenOCRModel{})
	pack := publishSyntheticAcceptancePack(t, st, owner)
	publishSyntheticAcceptancePolicy(t, st, owner, pack)
	source := createGovernedFeedbackSource(t, st, owner, owner, "false_positive", "model-role-fence")
	feedback, err := st.SubmitExperienceFeedback(ctx, owner, source)
	if err != nil {
		t.Fatal(err)
	}
	st.UseModels(&distillPolicyTestModel{frozenOCRModel: &frozenOCRModel{}})
	receipt, err := st.RequestExperienceDistillation(ctx, owner, ExperienceDistillRequest{
		CaseID: source.CaseID, FeedbackID: feedback.FeedbackID, IdempotencyKey: "model-role-fence",
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := st.ClaimNextJob(ctx, "model-role-worker", time.Minute)
	if err != nil || claim.JobID != receipt.JobID {
		t.Fatalf("claim for model role: %+v, %v", claim, err)
	}
	work, err := st.loadExperienceDistillWork(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	scope := model.DelegatedScope{Kind: "experience_distill", TenantID: claim.TenantID, OrgID: claim.OrgID,
		CaseID: claim.CaseID, ActorID: claim.ActorID, JobID: claim.JobID, WorkerID: claim.WorkerID,
		Generation: claim.Generation, InputSHA256: work.InputSHA256}
	modelStore := &Store{pool: modelPool, schema: st.schema}
	lease, err := modelStore.ReserveModelCall(ctx, "worker", scope, "role-fence-model", 1, 20, 20, modelBudgetTestLimits())
	if err != nil {
		t.Fatalf("dedicated model role could not reserve its live job: %v", err)
	}
	settlePredispatch(t, ctx, modelStore, lease)
	tx, err := modelPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "model-gateway", Role: "model", TenantID: "missing-tenant", OrgID: "missing-org"}); err != nil {
		t.Fatal(err)
	}
	var locked bool
	query := `SELECT ` + pgx.Identifier{st.schema, "model_budget_lock_experience_job"}.Sanitize() + `($1,$2,$3,$4,$5,$6,$7,$8)`
	if err := tx.QueryRow(ctx, query, "missing-tenant", "missing-org", "missing-case", "missing-actor", "missing-job", "missing-worker", int64(1), sha256Bytes([]byte("missing"))).Scan(&locked); err != nil || locked {
		t.Fatalf("model role could not safely call the bounded lock: locked=%t err=%v", locked, err)
	}
}
