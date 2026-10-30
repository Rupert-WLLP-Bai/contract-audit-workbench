package modelgateway

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const gatewayProviderToken = "private-gateway-upstream-token-0123456789"

type gatewayUsageRow struct {
	LeaseID        string
	Caller         string
	Model          string
	Attempt        int
	Status         string
	ErrorCode      string
	UsageSource    pgtype.Text
	InputTokens    pgtype.Int8
	OutputTokens   pgtype.Int8
	TotalTokens    pgtype.Int8
	ReservedInput  pgtype.Int8
	ReservedOutput pgtype.Int8
	ChargedTokens  int64
	BudgetReleased bool
}

func openGatewayTestStore(t *testing.T) (*store.Store, accounts.User) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required for model gateway PostgreSQL scenarios")
	}
	schema := "it_gateway_" + strings.ToLower(rand.Text())
	t.Setenv("APP_ENV", "test")
	t.Setenv("DATABASE_SCHEMA", schema)
	var st *store.Store
	t.Cleanup(func() {
		if st != nil {
			if err := st.Close(); err != nil {
				t.Errorf("close isolated model gateway schema: %v", err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Errorf("connect to clean isolated gateway schema: %v", err)
			return
		}
		defer pool.Close()
		if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("drop isolated gateway schema: %v", err)
		}
	})
	var err error
	st, err = store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open isolated model gateway schema: %v", err)
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin is unavailable")
	}
	return st, admin
}

func gatewayTestConfig(mode, upstream string) Config {
	return Config{
		Mode: mode, UpstreamBaseURL: upstream, UpstreamAPIKey: gatewayProviderToken,
		ChatModel: "fixture-chat", OCRModel: "fixture-ocr",
		WorkerCredential: tokenFor("worker"), DocumentCredential: tokenFor("document"),
		MaxConcurrency: 4, QueueCapacity: 8, RequestTimeout: 3 * time.Second,
		CircuitFailureThreshold: 10, CircuitCooldown: time.Minute,
		Budget: store.ModelBudgetLimits{
			GlobalMaxInFlight: 8, TenantMaxInFlight: 8,
			GlobalCallsPerMinute: 100, TenantCallsPerMinute: 100,
			GlobalTokensPerMinute: 4_000_000, TenantTokensPerMinute: 4_000_000,
			MaxInputTokenReservation: 1 << 20, MaxOutputTokenReservation: 4096,
			LeaseDuration: time.Minute,
		},
	}
}

func startGatewayServer(t *testing.T, st *store.Store, config Config) (*Service, *httptest.Server) {
	t.Helper()
	service, err := New(st, config)
	if err != nil {
		t.Fatalf("construct model gateway: %v", err)
	}
	server := httptest.NewServer(service.Handler())
	t.Cleanup(server.Close)
	return service, server
}

func createGatewayCase(t *testing.T, st *store.Store, user accounts.User, title string) (string, []string) {
	t.Helper()
	caseID, err := st.CreateCase(user, title, "procurement_ict")
	if err != nil {
		t.Fatalf("create gateway authorization case: %v", err)
	}
	documents := make([]string, 0, 2)
	for _, item := range []struct {
		role string
		text string
	}{
		{role: "procurement", text: "采购文件要求质保36个月。合同总价为100万元。"},
		{role: "draft_contract", text: "拟签合同约定质保24个月。合同总价为120万元。"},
	} {
		documentID, err := st.AddDocument(context.Background(), user, caseID, item.role, item.role+".txt", "text/plain", []byte(item.text))
		if err != nil {
			t.Fatalf("add gateway authorization document: %v", err)
		}
		documents = append(documents, documentID)
	}
	return caseID, documents
}

func documentGatewayScope(user accounts.User, caseID, documentID string) model.DelegatedScope {
	return model.DelegatedScope{
		Kind: "document", TenantID: user.TenantID, OrgID: user.OrgID,
		CaseID: caseID, ActorID: user.ID, DocumentID: documentID,
	}
}

func executionGatewayScope(claim store.JobClaim) model.DelegatedScope {
	return model.DelegatedScope{
		Kind: "execution", TenantID: claim.TenantID, OrgID: claim.OrgID,
		CaseID: claim.CaseID, ActorID: claim.ActorID, JobID: claim.JobID,
		RunID: claim.RunID, AttemptID: claim.AttemptID, WorkerID: claim.WorkerID,
		Generation: claim.Generation,
	}
}

func gatewayChatRequest(ctx context.Context, client *http.Client, endpoint string, serviceKeys []string, rawScope, modelID string, maxTokens int) (*http.Response, []byte, error) {
	payload, err := json.Marshal(map[string]any{
		"model": modelID, "max_tokens": maxTokens,
		"messages": []map[string]string{{"role": "user", "content": "gateway integration request"}},
	})
	if err != nil {
		return nil, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return nil, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	for _, key := range serviceKeys {
		request.Header.Add("X-Contract-Service-Key", key)
	}
	if rawScope != "" {
		request.Header.Set("X-Audit-Execution-Scope", rawScope)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, err
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		return response, nil, readErr
	}
	if closeErr != nil {
		return response, nil, closeErr
	}
	return response, body, nil
}

func encodedGatewayScope(t *testing.T, scope model.DelegatedScope) string {
	t.Helper()
	encoded, err := json.Marshal(scope)
	if err != nil {
		t.Fatalf("encode delegated model scope: %v", err)
	}
	return string(encoded)
}

func gatewayErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var response struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("gateway error was not JSON: %v; body=%q", err, body)
	}
	return response.Code
}

func readGatewayAccounting(t *testing.T, caseID string) ([]gatewayUsageRow, int) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	schema := os.Getenv("DATABASE_SCHEMA")
	if dsn == "" || !strings.HasPrefix(schema, "it_gateway_") {
		t.Fatal("gateway accounting requires its fixture's isolated test schema")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to isolated PostgreSQL test database: %v", err)
	}
	defer pool.Close()
	qualified := pgx.Identifier{schema}.Sanitize()
	query := `SELECT lease_id,caller_identity,model_ref,attempt_number,call_status,error_code,usage_source,
		input_tokens,output_tokens,total_tokens,reserved_input_tokens,reserved_output_tokens,charged_tokens,budget_released
		FROM ` + qualified + `.model_usage WHERE case_id=$1 ORDER BY attempt_number,call_id`
	rows, err := pool.Query(ctx, query, caseID)
	if err != nil {
		t.Fatalf("read settled model calls: %v", err)
	}
	defer rows.Close()
	var result []gatewayUsageRow
	for rows.Next() {
		var row gatewayUsageRow
		if err := rows.Scan(&row.LeaseID, &row.Caller, &row.Model, &row.Attempt, &row.Status, &row.ErrorCode,
			&row.UsageSource, &row.InputTokens, &row.OutputTokens, &row.TotalTokens,
			&row.ReservedInput, &row.ReservedOutput, &row.ChargedTokens, &row.BudgetReleased); err != nil {
			t.Fatalf("scan settled model call: %v", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate settled model calls: %v", err)
	}
	var activeLeases int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+qualified+`.model_call_leases WHERE case_id=$1`, caseID).Scan(&activeLeases); err != nil {
		t.Fatalf("count unsettled model reservations: %v", err)
	}
	return result, activeLeases
}

func assertUnknownUsageConservativelyCharged(t *testing.T, row gatewayUsageRow) {
	t.Helper()
	if row.UsageSource.Valid || row.InputTokens.Valid || row.OutputTokens.Valid || row.TotalTokens.Valid {
		t.Fatalf("unknown provider usage was converted into reported usage: %+v", row)
	}
	if !row.ReservedInput.Valid || !row.ReservedOutput.Valid || row.ChargedTokens != row.ReservedInput.Int64+row.ReservedOutput.Int64 {
		t.Fatalf("unknown usage was not charged at the reserved upper bound: %+v", row)
	}
	if row.BudgetReleased {
		t.Fatalf("dispatched model call released its token budget: %+v", row)
	}
}

func assertNoActiveGatewayLeases(t *testing.T, active int) {
	t.Helper()
	if active != 0 {
		t.Fatalf("model reservations remain after request settlement: %d", active)
	}
}

func expireGatewayJobClaim(t *testing.T, claim store.JobClaim) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	schema := os.Getenv("DATABASE_SCHEMA")
	if dsn == "" || !strings.HasPrefix(schema, "it_gateway_") {
		t.Fatal("gateway claim expiry requires its fixture's isolated test schema")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to isolated PostgreSQL test database: %v", err)
	}
	defer pool.Close()
	// Other packages create and drop it_* schemas in the same database. A catalog
	// scan can observe one of those schemas after it has already been removed.
	qualified := pgx.Identifier{schema}.Sanitize()
	tag, err := pool.Exec(ctx, `UPDATE `+qualified+`.worker_jobs
		SET lease_until=clock_timestamp()-interval '1 second', ready_at=clock_timestamp()-interval '1 second', updated_at=clock_timestamp()::text
		WHERE id=$1 AND tenant_id=$2 AND org_id=$3 AND case_id=$4 AND run_id=$5 AND attempt_id=$6
			AND worker_id=$7 AND generation=$8 AND state='running'`,
		claim.JobID, claim.TenantID, claim.OrgID, claim.CaseID, claim.RunID, claim.AttemptID, claim.WorkerID, claim.Generation)
	if err != nil {
		t.Fatalf("expire isolated worker job %q in schema %s: %v", claim.JobID, schema, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expired %d isolated worker jobs for claim %q in schema %s, want one", tag.RowsAffected(), claim.JobID, schema)
	}
}

func TestGatewayWorkerAndDocumentFixtureConsumersSettleUnknownUsage(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	st, admin := openGatewayTestStore(t)
	config := gatewayTestConfig("fixture", "")
	config.ProviderURL = "https://fixture-provider.example/v1"
	config.FixtureOCRContent = "质保期为36个月。"
	_, gateway := startGatewayServer(t, st, config)

	workerClient := model.New(model.Config{
		BaseURL: gateway.URL + "/v1", ProviderURL: config.ProviderURL, Gateway: true, APIKey: config.WorkerCredential,
		ChatModel: config.ChatModel, OCRModel: config.OCRModel,
	})
	if status, _ := workerClient.Reachable(context.Background()); status != "connected" {
		t.Fatalf("worker could not discover the configured fixture models: %s", status)
	}
	st.UseModels(workerClient)
	caseID, documents := createGatewayCase(t, st, admin, "gateway worker fixture call")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	accepted, err := st.StartReviewAsync(ctx, admin, caseID)
	if err != nil {
		t.Fatalf("accept fixture-backed worker review: %v", err)
	}
	claim, err := st.ClaimNextJob(ctx, "model-gateway-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim fixture-backed worker review: %v", err)
	}
	if claim.JobID != accepted.JobID || claim.AttemptID != accepted.AttemptID {
		t.Fatalf("worker claimed a different review: claim=%+v accepted=%+v", claim, accepted)
	}
	if err := st.RunClaimedJob(ctx, claim); err != nil {
		t.Fatalf("run fixture-backed worker review: %v", err)
	}
	usage, active := readGatewayAccounting(t, caseID)
	if len(usage) != 1 || usage[0].Caller != "worker" || usage[0].Model != "fixture-chat" || usage[0].Status != "completed" {
		t.Fatalf("worker fixture call was not settled exactly once: usage=%+v", usage)
	}
	assertUnknownUsageConservativelyCharged(t, usage[0])
	assertNoActiveGatewayLeases(t, active)

	documentClient := model.New(model.Config{
		BaseURL: gateway.URL + "/v1", Gateway: true, APIKey: config.DocumentCredential,
		ChatModel: config.ChatModel, OCRModel: config.OCRModel,
	})
	documentScope := documentGatewayScope(admin, caseID, documents[0])
	text, err := documentClient.Transcribe(model.WithDelegatedScope(context.Background(), documentScope), "image/png", []byte("deterministic image bytes"))
	if err != nil || text != config.FixtureOCRContent {
		t.Fatalf("document fixture transcription = %q, err=%v", text, err)
	}
	usage, active = readGatewayAccounting(t, caseID)
	if len(usage) != 2 {
		t.Fatalf("worker and document fixture calls produced %d settlements, want 2: %+v", len(usage), usage)
	}
	var documentRow *gatewayUsageRow
	for i := range usage {
		if usage[i].Caller == "document" {
			documentRow = &usage[i]
		}
	}
	if documentRow == nil || documentRow.Model != "fixture-ocr" || documentRow.Status != "completed" {
		t.Fatalf("document fixture call was not settled exactly once: usage=%+v", usage)
	}
	assertUnknownUsageConservativelyCharged(t, *documentRow)
	if usage[0].LeaseID == usage[1].LeaseID {
		t.Fatalf("separate worker and document calls reused a settlement lease: %+v", usage)
	}
	assertNoActiveGatewayLeases(t, active)

	response, err := http.Get(gateway.URL + "/health/ready")
	if err != nil {
		t.Fatalf("read fixture gateway readiness: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("fixture gateway readiness status = %d", response.StatusCode)
	}
}

func TestExplicitImageReparseUsesAuthorizedDocumentScopeBeforeReview(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	st, admin := openGatewayTestStore(t)
	config := gatewayTestConfig("fixture", "")
	config.ProviderURL = "https://fixture-provider.example/v1"
	config.FixtureOCRContent = "采购文件要求质保36个月。"
	_, gateway := startGatewayServer(t, st, config)
	st.UseModels(model.New(model.Config{
		BaseURL: gateway.URL + "/v1", ProviderURL: config.ProviderURL, Gateway: true, APIKey: config.DocumentCredential,
		ChatModel: config.ChatModel, OCRModel: config.OCRModel,
	}))
	caseID, err := st.CreateCase(admin, "独立图片重解析授权", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	documentID, err := st.AddDocument(context.Background(), admin, caseID, "procurement", "purchase.png", "image/png", model.SampleWarrantyPNG)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reparse(context.Background(), admin, caseID, documentID); err != nil {
		t.Fatal(err)
	}
	detail, err := st.GetCase(admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Documents) != 1 || !detail.Documents[0].Ready || detail.Documents[0].Text != config.FixtureOCRContent {
		t.Fatalf("authorized image reparse did not persist the provider transcript: %+v", detail.Documents)
	}
	usage, active := readGatewayAccounting(t, caseID)
	if len(usage) != 1 || usage[0].Caller != "document" || usage[0].Status != "completed" {
		t.Fatalf("explicit reparse did not settle its document call: %+v", usage)
	}
	assertUnknownUsageConservativelyCharged(t, usage[0])
	assertNoActiveGatewayLeases(t, active)
}

func TestGatewayRejectsCredentialsModelsAndCrossCaseDocumentScopes(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"provider answer"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	config := gatewayTestConfig("openai-compatible", upstream.URL+"/v1")
	_, gateway := startGatewayServer(t, st, config)
	caseA, docsA := createGatewayCase(t, st, admin, "gateway scope case A")
	caseB, _ := createGatewayCase(t, st, admin, "gateway scope case B")
	client := &http.Client{Timeout: 5 * time.Second}
	endpoint := gateway.URL + "/v1/chat/completions"
	validScope := documentGatewayScope(admin, caseA, docsA[0])

	response, body, err := gatewayChatRequest(context.Background(), client, endpoint, []string{"wrong-private-key"}, encodedGatewayScope(t, validScope), "fixture-chat", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized || gatewayErrorCode(t, body) != "service_unauthenticated" {
		t.Fatalf("bad service credential response = %d %s", response.StatusCode, body)
	}

	request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(`{"model":"fixture-chat","messages":[],"max_tokens":8}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Add("X-Contract-Service-Key", config.WorkerCredential)
	request.Header.Add("X-Contract-Service-Key", config.DocumentCredential)
	request.Header.Set("X-Audit-Execution-Scope", encodedGatewayScope(t, validScope))
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized || gatewayErrorCode(t, body) != "service_unauthenticated" {
		t.Fatalf("ambiguous service credentials response = %d %s", response.StatusCode, body)
	}

	response, body, err = gatewayChatRequest(context.Background(), client, endpoint, []string{config.DocumentCredential}, encodedGatewayScope(t, validScope), "not-configured", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || gatewayErrorCode(t, body) != "model_not_configured" {
		t.Fatalf("unconfigured model response = %d %s", response.StatusCode, body)
	}

	crossCase := documentGatewayScope(admin, caseB, docsA[0])
	response, body, err = gatewayChatRequest(context.Background(), client, endpoint, []string{config.DocumentCredential}, encodedGatewayScope(t, crossCase), "fixture-chat", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden || gatewayErrorCode(t, body) != "model_scope_unavailable" {
		t.Fatalf("cross-case document response = %d %s", response.StatusCode, body)
	}

	wrongActor := validScope
	wrongActor.ActorID = "demo-viewer"
	response, body, err = gatewayChatRequest(context.Background(), client, endpoint, []string{config.DocumentCredential}, encodedGatewayScope(t, wrongActor), "fixture-chat", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden || gatewayErrorCode(t, body) != "model_scope_unavailable" {
		t.Fatalf("unauthorized document actor response = %d %s", response.StatusCode, body)
	}

	response, body, err = gatewayChatRequest(context.Background(), client, endpoint, []string{config.DocumentCredential}, `{"kind":"document","unexpected":true}`, "fixture-chat", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown delegated-scope field response = %d %s", response.StatusCode, body)
	}

	response, body, err = gatewayChatRequest(context.Background(), client, endpoint, []string{config.DocumentCredential}, encodedGatewayScope(t, validScope), "fixture-chat", int(config.Budget.MaxOutputTokenReservation+1))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || gatewayErrorCode(t, body) != "model_token_bound_exceeded" {
		t.Fatalf("excessive output reservation response = %d %s", response.StatusCode, body)
	}
	if calls := upstreamCalls.Load(); calls != 0 {
		t.Fatalf("invalid gateway requests reached the provider %d times", calls)
	}
	usageA, leasesA := readGatewayAccounting(t, caseA)
	usageB, leasesB := readGatewayAccounting(t, caseB)
	if len(usageA) != 0 || len(usageB) != 0 {
		t.Fatalf("refused gateway requests created usage rows: case A=%+v case B=%+v", usageA, usageB)
	}
	assertNoActiveGatewayLeases(t, leasesA+leasesB)
}

type gatewayBarrierModels struct {
	entered chan model.DelegatedScope
	release <-chan struct{}
}

func (m *gatewayBarrierModels) Name() string { return "gateway-scope-barrier" }
func (m *gatewayBarrierModels) ConfigurationID() string {
	return model.ConfigurationIDFor("https://gateway-scope.example/v1", "fixture-chat", "fixture-ocr")
}
func (m *gatewayBarrierModels) Transcribe(context.Context, string, []byte) (string, error) {
	return "unused", nil
}
func (m *gatewayBarrierModels) Warranty(ctx context.Context, _ []model.Material) (model.Warranty, error) {
	scope, ok := model.DelegatedScopeFromContext(ctx)
	if !ok {
		return model.Warranty{}, errors.New("worker execution scope was not propagated")
	}
	select {
	case m.entered <- scope:
	case <-ctx.Done():
		return model.Warranty{}, ctx.Err()
	}
	select {
	case <-m.release:
		return model.Warranty{Relation: "compatible"}, nil
	case <-ctx.Done():
		return model.Warranty{}, ctx.Err()
	}
}

func TestGatewayFencesStaleWorkerClaimsAndRejectsCrossCaseClaims(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	policy := store.DefaultQueuePolicy()
	policy.BackoffBaseMS, policy.BackoffMaxMS = 1, 1
	if err := st.UseQueuePolicy(policy); err != nil {
		t.Fatalf("configure short deterministic takeover backoff: %v", err)
	}
	release := make(chan struct{})
	var releaseOnce sync.Once
	models := &gatewayBarrierModels{entered: make(chan model.DelegatedScope, 1), release: release}
	st.UseModels(models)
	caseID, _ := createGatewayCase(t, st, admin, "gateway worker claim fencing")
	foreignCaseID, _ := createGatewayCase(t, st, admin, "gateway foreign worker scope")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	accepted, err := st.StartReviewAsync(ctx, admin, caseID)
	if err != nil {
		t.Fatalf("accept worker claim fencing review: %v", err)
	}
	oldClaim, err := st.ClaimNextJob(ctx, "gateway-original-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim worker fencing review: %v", err)
	}
	if oldClaim.JobID != accepted.JobID {
		t.Fatalf("claimed job %q, want %q", oldClaim.JobID, accepted.JobID)
	}
	runDone := make(chan struct{})
	var runErr error
	go func() {
		runErr = st.RunClaimedJob(ctx, oldClaim)
		close(runDone)
	}()
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		select {
		case <-runDone:
		case <-time.After(6 * time.Second):
			t.Error("blocked worker execution did not stop during cleanup")
		}
	})
	var captured model.DelegatedScope
	select {
	case captured = <-models.entered:
	case <-runDone:
		t.Fatalf("worker exited before reaching the model boundary: %v", runErr)
	case <-ctx.Done():
		t.Fatalf("worker did not reach the model boundary: %v", ctx.Err())
	}
	if captured.Kind != "execution" || captured.JobID != oldClaim.JobID || captured.Generation != oldClaim.Generation || captured.WorkerID != oldClaim.WorkerID {
		t.Fatalf("worker propagated a different active claim: got=%+v claim=%+v", captured, oldClaim)
	}

	expireGatewayJobClaim(t, oldClaim)
	var newClaim store.JobClaim
	claimDeadline := time.Now().Add(3 * time.Second)
	for {
		newClaim, err = st.ClaimNextJob(ctx, "gateway-takeover-worker", time.Minute)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("reclaim expired worker lease: %v", err)
		}
		if time.Now().After(claimDeadline) {
			t.Fatal("expired worker job did not become claimable within the bound")
		}
		select {
		case <-time.After(2 * time.Millisecond):
		case <-ctx.Done():
			t.Fatalf("waiting for reclaimed worker job: %v", ctx.Err())
		}
	}
	if newClaim.JobID != oldClaim.JobID || newClaim.Generation != oldClaim.Generation+1 || newClaim.WorkerID == oldClaim.WorkerID {
		t.Fatalf("job takeover did not fence the original claim: old=%+v new=%+v", oldClaim, newClaim)
	}

	var providerCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"provider answer"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	config := gatewayTestConfig("openai-compatible", upstream.URL+"/v1")
	config.ProviderURL = "https://gateway-scope.example/v1"
	_, gateway := startGatewayServer(t, st, config)
	client := &http.Client{Timeout: 5 * time.Second}
	endpoint := gateway.URL + "/v1/chat/completions"

	response, body, err := gatewayChatRequest(ctx, client, endpoint, []string{config.WorkerCredential}, encodedGatewayScope(t, captured), "fixture-chat", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden || gatewayErrorCode(t, body) != "model_scope_unavailable" {
		t.Fatalf("expired worker claim response = %d %s", response.StatusCode, body)
	}

	crossCase := executionGatewayScope(newClaim)
	crossCase.CaseID = foreignCaseID
	response, body, err = gatewayChatRequest(ctx, client, endpoint, []string{config.WorkerCredential}, encodedGatewayScope(t, crossCase), "fixture-chat", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden || gatewayErrorCode(t, body) != "model_scope_unavailable" {
		t.Fatalf("cross-case worker claim response = %d %s", response.StatusCode, body)
	}

	currentScope := executionGatewayScope(newClaim)
	response, body, err = gatewayChatRequest(ctx, client, endpoint, []string{config.WorkerCredential}, encodedGatewayScope(t, currentScope), "fixture-chat", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("current worker claim response = %d %s", response.StatusCode, body)
	}
	if calls := providerCalls.Load(); calls != 1 {
		t.Fatalf("stale or cross-case claims reached provider; provider calls=%d", calls)
	}
	usage, active := readGatewayAccounting(t, caseID)
	if len(usage) != 1 || usage[0].Caller != "worker" || usage[0].Status != "completed" {
		t.Fatalf("current worker claim did not settle one usage row: %+v", usage)
	}
	assertUnknownUsageConservativelyCharged(t, usage[0])
	assertNoActiveGatewayLeases(t, active)

	releaseOnce.Do(func() { close(release) })
	select {
	case <-runDone:
	case <-ctx.Done():
		t.Fatalf("worker did not finish after releasing model barrier: %v", ctx.Err())
	}

}

func TestGatewayConcurrentQuotaSettlesUnknownUsageAndReleasesReservations(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	caseID, documents := createGatewayCase(t, st, admin, "gateway concurrent model budget")
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var providerCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
			return
		case <-release:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"provider answer"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(upstream.Close)
	config := gatewayTestConfig("openai-compatible", upstream.URL+"/v1")
	config.MaxConcurrency = 2
	config.QueueCapacity = 4
	config.Budget.GlobalMaxInFlight = 1
	config.Budget.TenantMaxInFlight = 1
	_, gateway := startGatewayServer(t, st, config)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	client := &http.Client{Timeout: 6 * time.Second}
	endpoint := gateway.URL + "/v1/chat/completions"
	scope := encodedGatewayScope(t, documentGatewayScope(admin, caseID, documents[0]))
	type callResult struct {
		status int
		body   []byte
		err    error
	}
	firstResult := make(chan callResult, 1)
	go func() {
		response, body, err := gatewayChatRequest(context.Background(), client, endpoint, []string{config.DocumentCredential}, scope, "fixture-chat", 8)
		if response == nil {
			firstResult <- callResult{body: body, err: err}
			return
		}
		firstResult <- callResult{status: response.StatusCode, body: body, err: err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first provider request did not reach the deterministic barrier")
	}
	rows, active := readGatewayAccounting(t, caseID)
	if len(rows) != 0 || active != 1 {
		t.Fatalf("first request did not hold exactly one live reservation: rows=%+v active=%d", rows, active)
	}

	response, body, err := gatewayChatRequest(context.Background(), client, endpoint, []string{config.DocumentCredential}, scope, "fixture-chat", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusTooManyRequests || gatewayErrorCode(t, body) != "model_budget_exhausted" {
		t.Fatalf("concurrent over-quota request response = %d %s", response.StatusCode, body)
	}
	if calls := providerCalls.Load(); calls != 1 {
		t.Fatalf("over-quota call reached provider; calls=%d", calls)
	}

	releaseOnce.Do(func() { close(release) })
	first := <-firstResult
	if first.err != nil || first.status != http.StatusOK {
		t.Fatalf("first provider request result = %d %q, err=%v", first.status, first.body, first.err)
	}
	response, body, err = gatewayChatRequest(context.Background(), client, endpoint, []string{config.DocumentCredential}, scope, "fixture-chat", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("request after settlement was not admitted: %d %s", response.StatusCode, body)
	}
	rows, active = readGatewayAccounting(t, caseID)
	if len(rows) != 2 {
		t.Fatalf("two dispatched requests produced %d settlements, want 2: %+v", len(rows), rows)
	}
	if rows[0].LeaseID == rows[1].LeaseID {
		t.Fatalf("distinct physical calls shared one settlement identity: %+v", rows)
	}
	for _, row := range rows {
		if row.Caller != "document" || row.Status != "completed" {
			t.Fatalf("unexpected settled concurrent call: %+v", row)
		}
		assertUnknownUsageConservativelyCharged(t, row)
	}
	assertNoActiveGatewayLeases(t, active)
	if calls := providerCalls.Load(); calls != 2 {
		t.Fatalf("provider received %d requests after the quota retry, want 2", calls)
	}
}

func TestGatewayRetriesProviderFailureAndSettlesEachPhysicalCall(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	caseID, documents := createGatewayCase(t, st, admin, "gateway retry accounting")
	var providerCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+gatewayProviderToken {
			t.Errorf("provider authorization = %q", got)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("provider path = %q", r.URL.Path)
		}
		if providerCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "temporary provider diagnostic")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"observed-model","usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18},"choices":[{"message":{"content":"provider answer"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	config := gatewayTestConfig("openai-compatible", upstream.URL+"/v1")
	config.Retries = 1
	_, gateway := startGatewayServer(t, st, config)
	response, body, err := gatewayChatRequest(context.Background(), &http.Client{Timeout: 6 * time.Second}, gateway.URL+"/v1/chat/completions",
		[]string{config.DocumentCredential}, encodedGatewayScope(t, documentGatewayScope(admin, caseID, documents[0])), "fixture-chat", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || strings.Contains(string(body), "temporary provider diagnostic") {
		t.Fatalf("retried provider call response = %d %s", response.StatusCode, body)
	}
	if calls := providerCalls.Load(); calls != 2 {
		t.Fatalf("provider retry count = %d, want 2", calls)
	}
	usage, active := readGatewayAccounting(t, caseID)
	if len(usage) != 2 || usage[0].Attempt != 1 || usage[1].Attempt != 2 {
		t.Fatalf("each physical retry was not recorded under its attempt number: %+v", usage)
	}
	if usage[0].Status != "failed" || usage[0].ErrorCode != "provider_http_503" {
		t.Fatalf("first provider failure settlement = %+v", usage[0])
	}
	assertUnknownUsageConservativelyCharged(t, usage[0])
	if usage[1].Status != "completed" || !usage[1].UsageSource.Valid || usage[1].UsageSource.String != "openai_response.usage" ||
		!usage[1].InputTokens.Valid || usage[1].InputTokens.Int64 != 11 || !usage[1].OutputTokens.Valid || usage[1].OutputTokens.Int64 != 7 ||
		!usage[1].TotalTokens.Valid || usage[1].TotalTokens.Int64 != 18 || usage[1].ChargedTokens != 18 {
		t.Fatalf("reported upstream usage was not preserved exactly: %+v", usage[1])
	}
	assertNoActiveGatewayLeases(t, active)
}

func TestGatewaySanitizesProviderErrorsAndOpensCircuit(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	caseID, documents := createGatewayCase(t, st, admin, "gateway circuit failure")
	var providerCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "private provider diagnostic and credential")
	}))
	defer upstream.Close()
	config := gatewayTestConfig("openai-compatible", upstream.URL+"/v1")
	config.CircuitFailureThreshold = 1
	_, gateway := startGatewayServer(t, st, config)
	client := &http.Client{Timeout: 5 * time.Second}
	endpoint := gateway.URL + "/v1/chat/completions"
	scope := encodedGatewayScope(t, documentGatewayScope(admin, caseID, documents[0]))
	response, body, err := gatewayChatRequest(context.Background(), client, endpoint, []string{config.DocumentCredential}, scope, "fixture-chat", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadGateway || gatewayErrorCode(t, body) != "model_provider_failed" || strings.Contains(string(body), "private provider diagnostic") {
		t.Fatalf("provider failure response leaked details or changed protocol: %d %s", response.StatusCode, body)
	}
	response, body, err = gatewayChatRequest(context.Background(), client, endpoint, []string{config.DocumentCredential}, scope, "fixture-chat", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusServiceUnavailable || gatewayErrorCode(t, body) != "model_circuit_open" {
		t.Fatalf("open circuit response = %d %s", response.StatusCode, body)
	}
	if calls := providerCalls.Load(); calls != 1 {
		t.Fatalf("open circuit forwarded another provider request: %d calls", calls)
	}
	usage, active := readGatewayAccounting(t, caseID)
	if len(usage) != 1 || usage[0].Status != "failed" || usage[0].ErrorCode != "provider_http_503" {
		t.Fatalf("provider failure settlement = %+v", usage)
	}
	assertUnknownUsageConservativelyCharged(t, usage[0])
	assertNoActiveGatewayLeases(t, active)
}

func TestGatewayReleasesCooldownProbeAfterRefusedRequests(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	caseID, documents := createGatewayCase(t, st, admin, "gateway cooldown probe recovery")
	var providerCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		call := providerCalls.Add(1)
		if call == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "temporary provider diagnostic")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"observed-model","usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18},"choices":[{"message":{"content":"provider answer"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	config := gatewayTestConfig("openai-compatible", upstream.URL+"/v1")
	config.CircuitFailureThreshold = 1
	config.CircuitCooldown = 10 * time.Millisecond
	service, gateway := startGatewayServer(t, st, config)
	client := &http.Client{Timeout: 5 * time.Second}
	endpoint := gateway.URL + "/v1/chat/completions"
	scope := encodedGatewayScope(t, documentGatewayScope(admin, caseID, documents[0]))

	response, body, err := gatewayChatRequest(context.Background(), client, endpoint, []string{config.DocumentCredential}, scope, "fixture-chat", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadGateway || gatewayErrorCode(t, body) != "model_provider_failed" {
		t.Fatalf("initial provider failure response = %d %s", response.StatusCode, body)
	}
	if calls := providerCalls.Load(); calls != 1 {
		t.Fatalf("initial provider call count = %d, want 1", calls)
	}

	service.circuit.mu.Lock()
	opened, openedAt, cooldown := service.circuit.open, service.circuit.openedAt, service.circuit.cooldown
	service.circuit.mu.Unlock()
	if !opened {
		t.Fatal("provider failure did not open the circuit")
	}
	if remaining := time.Until(openedAt.Add(cooldown)); remaining > 0 {
		time.Sleep(remaining)
	}

	sendRaw := func(payload string) (*http.Response, []byte, error) {
		request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(payload))
		if err != nil {
			return nil, nil, err
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Contract-Service-Key", config.DocumentCredential)
		request.Header.Set("X-Audit-Execution-Scope", scope)
		response, err := client.Do(request)
		if err != nil {
			return nil, nil, err
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil {
			return response, nil, readErr
		}
		if closeErr != nil {
			return response, nil, closeErr
		}
		return response, body, nil
	}

	response, body, err = sendRaw("{")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed cooldown probe response = %d %s", response.StatusCode, body)
	}
	response, body, err = gatewayChatRequest(context.Background(), client, endpoint, []string{config.DocumentCredential}, scope, "not-configured", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || gatewayErrorCode(t, body) != "model_not_configured" {
		t.Fatalf("unconfigured cooldown probe response = %d %s", response.StatusCode, body)
	}
	response, body, err = gatewayChatRequest(context.Background(), client, endpoint, []string{config.DocumentCredential}, scope, "fixture-chat", int(config.Budget.MaxOutputTokenReservation+1))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || gatewayErrorCode(t, body) != "model_token_bound_exceeded" {
		t.Fatalf("excessive token bound cooldown probe response = %d %s", response.StatusCode, body)
	}

	wrongActor := documentGatewayScope(admin, caseID, documents[0])
	wrongActor.ActorID = "demo-viewer"
	response, body, err = gatewayChatRequest(context.Background(), client, endpoint, []string{config.DocumentCredential}, encodedGatewayScope(t, wrongActor), "fixture-chat", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden || gatewayErrorCode(t, body) != "model_scope_unavailable" {
		t.Fatalf("unauthorized cooldown probe response = %d %s", response.StatusCode, body)
	}
	if calls := providerCalls.Load(); calls != 1 {
		t.Fatalf("refused cooldown probes changed provider call count to %d, want 1", calls)
	}

	response, body, err = gatewayChatRequest(context.Background(), client, endpoint, []string{config.DocumentCredential}, scope, "fixture-chat", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("valid cooldown recovery response = %d %s", response.StatusCode, body)
	}
	if calls := providerCalls.Load(); calls != 2 {
		t.Fatalf("provider call count after cooldown recovery = %d, want 2", calls)
	}
	usage, active := readGatewayAccounting(t, caseID)
	if len(usage) != 2 {
		t.Fatalf("failed and recovered provider settlements = %+v", usage)
	}
	var failed, recovered *gatewayUsageRow
	for index := range usage {
		row := &usage[index]
		switch row.Status {
		case "failed":
			failed = row
		case "completed":
			recovered = row
		}
	}
	if failed == nil || recovered == nil || failed.ErrorCode != "provider_http_503" ||
		!recovered.InputTokens.Valid || recovered.InputTokens.Int64 != 11 ||
		!recovered.OutputTokens.Valid || recovered.OutputTokens.Int64 != 7 || recovered.ChargedTokens != 18 {
		t.Fatalf("failed and recovered provider settlements = %+v", usage)
	}
	assertUnknownUsageConservativelyCharged(t, *failed)
	assertNoActiveGatewayLeases(t, active)
}

func TestGatewayTimeoutAndCallerCancellationSettleReservations(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	cancelledCase, cancelledDocs := createGatewayCase(t, st, admin, "gateway caller cancellation")
	timeoutCase, timeoutDocs := createGatewayCase(t, st, admin, "gateway request timeout")
	type providerEvent struct {
		started   chan struct{}
		cancelled chan struct{}
	}
	events := []providerEvent{{started: make(chan struct{}, 1), cancelled: make(chan struct{}, 1)}, {started: make(chan struct{}, 1), cancelled: make(chan struct{}, 1)}}
	var providerCalls atomic.Int32
	providerRelease := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		index := int(providerCalls.Add(1)) - 1
		if index >= len(events) {
			return
		}
		if _, err := io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20)); err != nil {
			return
		}
		select {
		case events[index].started <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
			select {
			case events[index].cancelled <- struct{}{}:
			default:
			}
		case <-providerRelease:
		}
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(func() { close(providerRelease) })

	cancelConfig := gatewayTestConfig("openai-compatible", upstream.URL+"/v1")
	cancelConfig.RequestTimeout = 3 * time.Second
	cancelConfig.Budget.LeaseDuration = time.Minute
	cancelService, _ := startGatewayServer(t, st, cancelConfig)
	cancelCtx, cancelRequest := context.WithCancel(context.Background())
	cancelRequestHTTP, err := http.NewRequestWithContext(cancelCtx, http.MethodPost, "http://gateway.invalid/v1/chat/completions",
		strings.NewReader(`{"model":"fixture-chat","max_tokens":8,"messages":[{"role":"user","content":"cancel"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	cancelRequestHTTP.Header.Set("Content-Type", "application/json")
	cancelRequestHTTP.Header.Set("X-Contract-Service-Key", cancelConfig.DocumentCredential)
	cancelRequestHTTP.Header.Set("X-Audit-Execution-Scope", encodedGatewayScope(t, documentGatewayScope(admin, cancelledCase, cancelledDocs[0])))
	cancelRecorder := httptest.NewRecorder()
	cancelDone := make(chan struct{})
	go func() {
		cancelService.Handler().ServeHTTP(cancelRecorder, cancelRequestHTTP)
		close(cancelDone)
	}()
	t.Cleanup(func() {
		cancelRequest()
		select {
		case <-cancelDone:
		case <-time.After(5 * time.Second):
			t.Error("cancelled gateway handler did not stop during cleanup")
		}
	})
	select {
	case <-events[0].started:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled request did not reach the provider")
	}
	cancelRequest()
	select {
	case <-cancelDone:
	case <-time.After(5 * time.Second):
		t.Fatal("gateway handler did not stop after caller cancellation")
	}
	select {
	case <-events[0].cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("caller cancellation did not reach the provider request")
	}
	if cancelRecorder.Code != http.StatusServiceUnavailable || gatewayErrorCode(t, cancelRecorder.Body.Bytes()) != "model_cancelled" {
		t.Fatalf("caller cancellation response = %d %s", cancelRecorder.Code, cancelRecorder.Body.String())
	}

	timeoutConfig := gatewayTestConfig("openai-compatible", upstream.URL+"/v1")
	timeoutConfig.RequestTimeout = 250 * time.Millisecond
	timeoutConfig.Budget.LeaseDuration = time.Minute
	timeoutService, _ := startGatewayServer(t, st, timeoutConfig)
	timeoutContext, cancelTimeoutRequest := context.WithCancel(context.Background())
	timeoutRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"fixture-chat","max_tokens":8,"messages":[{"role":"user","content":"timeout"}]}`)).WithContext(timeoutContext)
	timeoutRequest.Header.Set("Content-Type", "application/json")
	timeoutRequest.Header.Set("X-Contract-Service-Key", timeoutConfig.DocumentCredential)
	timeoutRequest.Header.Set("X-Audit-Execution-Scope", encodedGatewayScope(t, documentGatewayScope(admin, timeoutCase, timeoutDocs[0])))
	timeoutRecorder := httptest.NewRecorder()
	timeoutDone := make(chan struct{})
	go func() {
		timeoutService.Handler().ServeHTTP(timeoutRecorder, timeoutRequest)
		close(timeoutDone)
	}()
	t.Cleanup(func() {
		cancelTimeoutRequest()
		select {
		case <-timeoutDone:
		case <-time.After(5 * time.Second):
			t.Error("timed gateway handler did not stop during cleanup")
		}
	})
	select {
	case <-events[1].started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed request did not reach the provider")
	}
	select {
	case <-timeoutDone:
	case <-time.After(5 * time.Second):
		t.Fatal("gateway handler did not stop at its request timeout")
	}
	select {
	case <-events[1].cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("gateway timeout did not cancel the provider request")
	}
	if timeoutRecorder.Code != http.StatusGatewayTimeout || gatewayErrorCode(t, timeoutRecorder.Body.Bytes()) != "model_timeout" {
		t.Fatalf("gateway timeout response = %d %s", timeoutRecorder.Code, timeoutRecorder.Body.String())
	}

	cancelUsage, cancelLeases := readGatewayAccounting(t, cancelledCase)
	timeoutUsage, timeoutLeases := readGatewayAccounting(t, timeoutCase)
	if len(cancelUsage) != 1 || cancelUsage[0].Status != "failed" || cancelUsage[0].ErrorCode != "provider_transport_error" {
		t.Fatalf("caller cancellation settlement = %+v", cancelUsage)
	}
	if len(timeoutUsage) != 1 || timeoutUsage[0].Status != "failed" || timeoutUsage[0].ErrorCode != "provider_timeout" {
		t.Fatalf("request timeout settlement = %+v", timeoutUsage)
	}
	assertUnknownUsageConservativelyCharged(t, cancelUsage[0])
	assertUnknownUsageConservativelyCharged(t, timeoutUsage[0])
	assertNoActiveGatewayLeases(t, cancelLeases+timeoutLeases)
}

func TestGatewayPreDispatchFailureReleasesReservation(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	caseID, documents := createGatewayCase(t, st, admin, "gateway pre-dispatch release")
	config := gatewayTestConfig("openai-compatible", "")
	_, gateway := startGatewayServer(t, st, config)
	response, body, err := gatewayChatRequest(context.Background(), &http.Client{Timeout: 5 * time.Second}, gateway.URL+"/v1/chat/completions",
		[]string{config.DocumentCredential}, encodedGatewayScope(t, documentGatewayScope(admin, caseID, documents[0])), "fixture-chat", 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadGateway || gatewayErrorCode(t, body) != "model_provider_failed" {
		t.Fatalf("missing provider endpoint response = %d %s", response.StatusCode, body)
	}
	usage, active := readGatewayAccounting(t, caseID)
	if len(usage) != 1 || usage[0].Status != "failed" || !usage[0].BudgetReleased || usage[0].ChargedTokens != 0 ||
		usage[0].InputTokens.Valid || usage[0].OutputTokens.Valid || usage[0].TotalTokens.Valid {
		t.Fatalf("proven pre-dispatch failure did not release its reservation: %+v", usage)
	}
	assertNoActiveGatewayLeases(t, active)
}

func TestGatewayCatalogReadinessAndMethodProtocol(t *testing.T) {
	st, _ := openGatewayTestStore(t)
	var catalogCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		catalogCalls.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer "+gatewayProviderToken {
			t.Errorf("catalog provider authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"fixture-chat"},{"id":"fixture-ocr"}]}`)
	}))
	defer upstream.Close()
	config := gatewayTestConfig("openai-compatible", upstream.URL+"/v1")
	_, gateway := startGatewayServer(t, st, config)
	client := model.New(model.Config{
		BaseURL: gateway.URL + "/v1", Gateway: true, APIKey: config.WorkerCredential,
		ChatModel: config.ChatModel, OCRModel: config.OCRModel,
	})
	if status, _ := client.Reachable(context.Background()); status != "connected" {
		t.Fatalf("provider model catalog was not reachable through gateway: %s", status)
	}
	response, err := http.Get(gateway.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated model catalog status = %d body=%s", response.StatusCode, body)
	}
	request, err := http.NewRequest(http.MethodGet, gateway.URL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Contract-Service-Key", config.DocumentCredential)
	response, err = (&http.Client{}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"fixture-chat"`) || !strings.Contains(string(body), `"fixture-ocr"`) {
		t.Fatalf("authenticated model catalog response = %d %s", response.StatusCode, body)
	}
	response, err = http.Get(gateway.URL + "/health/ready")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("ready endpoint status = %d", response.StatusCode)
	}
	for _, path := range []string{"/health", "/health/live"} {
		response, err = http.Get(gateway.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("live endpoint %s status = %d", path, response.StatusCode)
		}
	}
	if calls := catalogCalls.Load(); calls != 2 {
		t.Fatalf("model catalog probe count = %d, want one cached lookup and one readiness probe", calls)
	}

	methodCases := []struct {
		method string
		path   string
		allow  string
	}{
		{method: http.MethodPost, path: "/v1/models", allow: http.MethodGet},
		{method: http.MethodGet, path: "/v1/chat/completions", allow: http.MethodPost},
		{method: http.MethodPost, path: "/health/ready"},
		{method: http.MethodPost, path: "/health/live"},
	}
	for _, item := range methodCases {
		request, err := http.NewRequest(item.method, gateway.URL+item.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusMethodNotAllowed || (item.allow != "" && response.Header.Get("Allow") != item.allow) {
			t.Errorf("%s %s response = %d allow=%q", item.method, item.path, response.StatusCode, response.Header.Get("Allow"))
		}
	}
}

func TestGatewayCatalogAndReadinessFailClosedWhenProviderUnavailable(t *testing.T) {
	st, _ := openGatewayTestStore(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private provider diagnostic", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()
	config := gatewayTestConfig("openai-compatible", upstream.URL+"/v1")
	_, gateway := startGatewayServer(t, st, config)
	request, err := http.NewRequest(http.MethodGet, gateway.URL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Contract-Service-Key", config.WorkerCredential)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusServiceUnavailable || strings.Contains(string(body), "private provider diagnostic") {
		t.Fatalf("provider catalog error was not sanitized: %d %s", response.StatusCode, body)
	}
	response, err = http.Get(gateway.URL + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), `"dependency":"provider"`) {
		t.Fatalf("provider readiness failure = %d %s", response.StatusCode, body)
	}
}

func TestGatewayFromEnvOverridesAndRejectsInvalidValues(t *testing.T) {
	for _, key := range []string{
		"MODEL_GATEWAY_MODE", "LLM_BASE_URL", "LLM_API_KEY", "LLM_MODEL", "OCR_MODEL",
		"MODEL_GATEWAY_FIXTURE_CHAT_CONTENT", "MODEL_GATEWAY_FIXTURE_OCR_CONTENT", "WORKER_MODEL_TOKEN", "DOCUMENT_MODEL_TOKEN",
		"MODEL_MAX_CONCURRENCY", "MODEL_QUEUE_CAPACITY", "MODEL_RETRIES", "MODEL_CIRCUIT_FAILURES",
		"MODEL_GLOBAL_MAX_INFLIGHT", "MODEL_TENANT_MAX_INFLIGHT", "MODEL_GLOBAL_CALLS_PER_MINUTE", "MODEL_TENANT_CALLS_PER_MINUTE",
		"MODEL_GLOBAL_TOKENS_PER_MINUTE", "MODEL_TENANT_TOKENS_PER_MINUTE", "MODEL_MAX_INPUT_TOKEN_RESERVATION", "MODEL_MAX_OUTPUT_TOKEN_RESERVATION",
		"MODEL_REQUEST_TIMEOUT", "MODEL_CIRCUIT_COOLDOWN",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("WORKER_MODEL_TOKEN", tokenFor("worker"))
	t.Setenv("DOCUMENT_MODEL_TOKEN", tokenFor("document"))

	t.Setenv("MODEL_GATEWAY_MODE", "fixture")
	t.Setenv("LLM_BASE_URL", "http://127.0.0.1:9999/v1/")
	t.Setenv("LLM_API_KEY", "provider-secret")
	t.Setenv("LLM_MODEL", "custom-chat")
	t.Setenv("OCR_MODEL", "custom-ocr")
	t.Setenv("MODEL_GATEWAY_FIXTURE_CHAT_CONTENT", "fixture answer")
	t.Setenv("MODEL_GATEWAY_FIXTURE_OCR_CONTENT", "fixture transcription")
	t.Setenv("MODEL_MAX_CONCURRENCY", "3")
	t.Setenv("MODEL_QUEUE_CAPACITY", "11")
	t.Setenv("MODEL_RETRIES", "2")
	t.Setenv("MODEL_CIRCUIT_FAILURES", "4")
	t.Setenv("MODEL_GLOBAL_MAX_INFLIGHT", "19")
	t.Setenv("MODEL_TENANT_MAX_INFLIGHT", "7")
	t.Setenv("MODEL_GLOBAL_CALLS_PER_MINUTE", "700")
	t.Setenv("MODEL_TENANT_CALLS_PER_MINUTE", "70")
	t.Setenv("MODEL_GLOBAL_TOKENS_PER_MINUTE", "900000")
	t.Setenv("MODEL_TENANT_TOKENS_PER_MINUTE", "90000")
	t.Setenv("MODEL_MAX_INPUT_TOKEN_RESERVATION", "12000")
	t.Setenv("MODEL_MAX_OUTPUT_TOKEN_RESERVATION", "6000")
	t.Setenv("MODEL_REQUEST_TIMEOUT", "45s")
	t.Setenv("MODEL_CIRCUIT_COOLDOWN", "17s")
	overrides, err := FromEnv()
	if err != nil {
		t.Fatalf("read overridden model gateway configuration: %v", err)
	}
	if overrides.Mode != "fixture" || overrides.UpstreamBaseURL != "http://127.0.0.1:9999/v1" || overrides.UpstreamAPIKey != "provider-secret" ||
		overrides.ChatModel != "custom-chat" || overrides.OCRModel != "custom-ocr" || overrides.FixtureChatContent != "fixture answer" ||
		overrides.FixtureOCRContent != "fixture transcription" || overrides.MaxConcurrency != 3 || overrides.QueueCapacity != 11 ||
		overrides.Retries != 2 || overrides.CircuitFailureThreshold != 4 || overrides.Budget.GlobalMaxInFlight != 19 ||
		overrides.Budget.TenantMaxInFlight != 7 || overrides.Budget.GlobalCallsPerMinute != 700 || overrides.Budget.TenantCallsPerMinute != 70 ||
		overrides.Budget.GlobalTokensPerMinute != 900000 || overrides.Budget.TenantTokensPerMinute != 90000 ||
		overrides.Budget.MaxInputTokenReservation != 12000 || overrides.Budget.MaxOutputTokenReservation != 6000 ||
		overrides.RequestTimeout != 45*time.Second || overrides.CircuitCooldown != 17*time.Second || overrides.Budget.LeaseDuration != 75*time.Second {
		t.Fatalf("model gateway environment overrides were not applied: %+v", overrides)
	}

	for _, item := range []struct {
		name  string
		value string
	}{
		{name: "MODEL_MAX_CONCURRENCY", value: "0"},
		{name: "MODEL_RETRIES", value: "4"},
		{name: "MODEL_GLOBAL_TOKENS_PER_MINUTE", value: "not-a-number"},
		{name: "MODEL_REQUEST_TIMEOUT", value: "0s"},
	} {
		t.Run(item.name, func(t *testing.T) {
			t.Setenv(item.name, item.value)
			if _, err := FromEnv(); err == nil {
				t.Fatalf("invalid %s=%q was accepted", item.name, item.value)
			}
		})
	}
}

func TestGatewayNewRejectsInvalidProviderAndFixtureConfiguration(t *testing.T) {
	st := &store.Store{}
	base := gatewayTestConfig("openai-compatible", "")
	for _, item := range []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "missing store", mutate: func(c *Config) {}},
		{name: "duplicate credentials", mutate: func(c *Config) { c.DocumentCredential = c.WorkerCredential }},
		{name: "short credential", mutate: func(c *Config) { c.WorkerCredential = "short" }},
		{name: "zero concurrency", mutate: func(c *Config) { c.MaxConcurrency = 0 }},
		{name: "empty queue", mutate: func(c *Config) { c.QueueCapacity = 0 }},
		{name: "invalid provider scheme", mutate: func(c *Config) { c.UpstreamBaseURL = "ftp://provider.example/v1" }},
		{name: "provider credentials in URL", mutate: func(c *Config) { c.UpstreamBaseURL = "https://user:pass@provider.example/v1" }},
		{name: "provider query", mutate: func(c *Config) { c.UpstreamBaseURL = "https://provider.example/v1?token=x" }},
		{name: "fixture outside test", mutate: func(c *Config) { c.Mode = "fixture"; c.FixtureOCRContent = "text" }},
	} {
		t.Run(item.name, func(t *testing.T) {
			config := base
			item.mutate(&config)
			var target *store.Store = st
			if item.name == "missing store" {
				target = nil
			}
			if item.name == "fixture outside test" {
				t.Setenv("APP_ENV", "production")
			}
			if _, err := New(target, config); err == nil {
				t.Fatalf("invalid gateway configuration %q was accepted", item.name)
			}
		})
	}
}

func TestGatewayRejectsMalformedRequestsAndOversizedInput(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	caseID, documents := createGatewayCase(t, st, admin, "gateway protocol validation")
	var providerCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	config := gatewayTestConfig("openai-compatible", upstream.URL+"/v1")
	config.Budget.MaxInputTokenReservation = 16
	_, gateway := startGatewayServer(t, st, config)
	endpoint := gateway.URL + "/v1/chat/completions"
	client := &http.Client{Timeout: 5 * time.Second}
	scope := encodedGatewayScope(t, documentGatewayScope(admin, caseID, documents[0]))

	for _, item := range []struct {
		name string
		body string
	}{
		{name: "invalid JSON", body: "{"},
		{name: "missing token bound", body: `{"model":"fixture-chat","messages":[]}`},
		{name: "streaming", body: `{"model":"fixture-chat","messages":[],"max_tokens":8,"stream":true}`},
	} {
		t.Run(item.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(item.body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Contract-Service-Key", config.DocumentCredential)
			request.Header.Set("X-Audit-Execution-Scope", scope)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("malformed request status = %d body=%s", response.StatusCode, body)
			}
		})
	}

	const prefix = `{"model":"fixture-chat","messages":[],"max_tokens":8,"padding":"`
	const suffix = `"}`
	largeBody := io.MultiReader(
		strings.NewReader(prefix),
		io.LimitReader(gatewayPaddingReader(strings.Repeat("x", 32<<10)), maxRequestBytes),
		strings.NewReader(suffix),
	)
	request, err := http.NewRequest(http.MethodPost, endpoint, largeBody)
	if err != nil {
		t.Fatal(err)
	}
	request.ContentLength = int64(len(prefix) + maxRequestBytes + len(suffix))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Contract-Service-Key", config.DocumentCredential)
	request.Header.Set("X-Audit-Execution-Scope", scope)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized request status = %d body=%s", response.StatusCode, body)
	}
	if calls := providerCalls.Load(); calls != 0 {
		t.Fatalf("malformed or oversized requests reached provider %d times", calls)
	}
	usage, active := readGatewayAccounting(t, caseID)
	if len(usage) != 0 {
		t.Fatalf("rejected request bodies created accounting rows: %+v", usage)
	}
	assertNoActiveGatewayLeases(t, active)
}

func TestGatewayQueueAndConcurrencyCapacityBoundaries(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	caseID, documents := createGatewayCase(t, st, admin, "gateway local capacity boundary")
	config := gatewayTestConfig("openai-compatible", "")
	config.MaxConcurrency = 1
	config.QueueCapacity = 1
	config.RequestTimeout = 100 * time.Millisecond
	service, _ := startGatewayServer(t, st, config)
	requestFor := func() *http.Request {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", http.NoBody)
		request.Header.Set("X-Contract-Service-Key", config.DocumentCredential)
		request.Header.Set("X-Audit-Execution-Scope", encodedGatewayScope(t, documentGatewayScope(admin, caseID, documents[0])))
		return request
	}

	service.queue <- struct{}{}
	queueResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(queueResponse, requestFor())
	<-service.queue
	if queueResponse.Code != http.StatusServiceUnavailable || gatewayErrorCode(t, queueResponse.Body.Bytes()) != "model_queue_full" {
		t.Fatalf("full gateway queue response = %d %s", queueResponse.Code, queueResponse.Body.String())
	}

	service.concurrency <- struct{}{}
	concurrencyResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(concurrencyResponse, requestFor())
	<-service.concurrency
	if concurrencyResponse.Code != http.StatusGatewayTimeout || gatewayErrorCode(t, concurrencyResponse.Body.Bytes()) != "model_timeout" {
		t.Fatalf("request waiting for a concurrency slot response = %d %s", concurrencyResponse.Code, concurrencyResponse.Body.String())
	}
	usage, active := readGatewayAccounting(t, caseID)
	if len(usage) != 0 {
		t.Fatalf("requests refused before dispatch created usage rows: %+v", usage)
	}
	assertNoActiveGatewayLeases(t, active)
}

func TestCircuitBreakerAllowsOneCooldownProbeAndResetsOnSuccess(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	breaker := circuitBreaker{threshold: 2, cooldown: time.Second}
	if allowed, probe := breaker.allow(now); !allowed || probe != 0 {
		t.Fatalf("closed circuit admission = allowed %t probe %d", allowed, probe)
	}
	breaker.record(0, false, true, now)
	breaker.record(0, false, true, now)
	if allowed, probe := breaker.allow(now.Add(time.Second - time.Nanosecond)); allowed || probe != 0 {
		t.Fatalf("open circuit admitted before cooldown: allowed %t probe %d", allowed, probe)
	}
	probeAt := now.Add(time.Second)
	allowed, probe := breaker.allow(probeAt)
	if !allowed || probe == 0 {
		t.Fatalf("cooldown admission = allowed %t probe %d", allowed, probe)
	}
	if allowed, nextProbe := breaker.allow(probeAt); allowed || nextProbe != 0 {
		t.Fatalf("circuit admitted a second concurrent probe: allowed %t probe %d", allowed, nextProbe)
	}
	breaker.record(probe, true, false, probeAt)
	if allowed, nextProbe := breaker.allow(probeAt); !allowed || nextProbe != 0 {
		t.Fatalf("successful probe did not close circuit: allowed %t probe %d", allowed, nextProbe)
	}
}

func TestCircuitBreakerStaleProbeReleaseCannotClearNewerProbe(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	breaker := circuitBreaker{threshold: 1, cooldown: time.Second}
	breaker.record(0, false, true, now)
	probeAt := now.Add(time.Second)
	allowed, firstProbe := breaker.allow(probeAt)
	if !allowed || firstProbe == 0 {
		t.Fatalf("first cooldown admission = allowed %t probe %d", allowed, firstProbe)
	}
	breaker.release(firstProbe)
	allowed, secondProbe := breaker.allow(probeAt)
	if !allowed || secondProbe == 0 || secondProbe == firstProbe {
		t.Fatalf("second cooldown admission = allowed %t probe %d after %d", allowed, secondProbe, firstProbe)
	}
	breaker.release(firstProbe)
	if allowed, probe := breaker.allow(probeAt); allowed || probe != 0 {
		t.Fatalf("stale release cleared the newer probe: allowed %t probe %d", allowed, probe)
	}
	breaker.record(secondProbe, true, false, probeAt)
	if allowed, probe := breaker.allow(probeAt); !allowed || probe != 0 {
		t.Fatalf("successful second probe did not close circuit: allowed %t probe %d", allowed, probe)
	}
}
