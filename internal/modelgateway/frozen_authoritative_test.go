// File metadata (manifest mirror):
// Purpose: Exercises the `modelgateway` package behavior through TestChatUsesServerFrozenIdentityDespiteOmittedOrForgedScope; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-30
// Updated: 2026-11-04
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

package modelgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type frozenBarrier struct {
	*gatewayBarrierModels
	identity string
}

func TestStructuredReviewRejectsFrozenLegacySnapshot(t *testing.T) {
	t.Setenv("CAW_STRUCTURED_OUTPUT_ENABLED", "")
	st, admin := openGatewayTestStore(t)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusOK) }))
	defer provider.Close()
	config := gatewayTestConfig("openai-compatible", provider.URL+"/v1")
	legacy := model.ConfigurationIDFor(config.UpstreamBaseURL, config.ChatModel, config.OCRModel)
	release := make(chan struct{})
	models := &frozenBarrier{gatewayBarrierModels: &gatewayBarrierModels{entered: make(chan model.DelegatedScope, 1), release: release}, identity: legacy}
	st.UseModels(models)
	caseID, _ := createGatewayCase(t, st, admin, "legacy frozen review")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	acceptance, err := st.StartReviewAsync(ctx, admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := st.ClaimNextJob(ctx, "legacy-structured-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = st.RunClaimedJob(ctx, claim) }()
	defer func() { close(release); <-done }()
	var scope model.DelegatedScope
	select {
	case scope = <-models.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	query := `SELECT model_configuration_id, row_to_json(s)::text FROM ` + pgx.Identifier{st.SchemaName(), "review_run_snapshots"}.Sanitize() + ` s WHERE run_id=$1`
	var beforeID, before string
	if err := pool.QueryRow(ctx, query, acceptance.RunID).Scan(&beforeID, &before); err != nil || beforeID != legacy {
		t.Fatalf("legacy snapshot %q: %v", beforeID, err)
	}
	t.Setenv("CAW_STRUCTURED_OUTPUT_ENABLED", "true")
	t.Setenv("CAW_STRUCTURED_OUTPUT_PROVIDER_URL", config.UpstreamBaseURL)
	t.Setenv("CAW_STRUCTURED_OUTPUT_REVIEW_MODEL", config.ChatModel)
	v4, _, err := model.StructuredConfigurationIDFor(config.UpstreamBaseURL, config.ChatModel, config.OCRModel)
	if err != nil {
		t.Fatal(err)
	}
	_, gateway := startGatewayServer(t, st, config)
	scope.ConfigurationID, scope.ProviderURL = v4, config.UpstreamBaseURL
	client := model.New(model.Config{BaseURL: gateway.URL + "/v1", ProviderURL: config.UpstreamBaseURL, Gateway: true, APIKey: config.WorkerCredential, ChatModel: config.ChatModel, OCRModel: config.OCRModel})
	if _, err := client.Warranty(model.WithDelegatedScope(ctx, scope), nil); err == nil {
		t.Fatal("legacy snapshot accepted new structured configuration")
	}
	if calls.Load() != 0 {
		t.Fatalf("legacy snapshot reached provider: %d", calls.Load())
	}
	var afterID, after string
	if err := pool.QueryRow(ctx, query, acceptance.RunID).Scan(&afterID, &after); err != nil || afterID != beforeID || after != before {
		t.Fatalf("legacy snapshot changed: %v", err)
	}
}

func TestStructuredReviewUsesFrozenIdentityAndSettles(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var payload struct {
			ResponseFormat json.RawMessage `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || !bytes.Equal(payload.ResponseFormat, model.StructuredResponseFormat("warranty")) {
			t.Errorf("provider schema mismatch: %s %v", payload.ResponseFormat, err)
		}
		_, _ = io.WriteString(w, `{"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18},"choices":[{"message":{"content":"{\"claim\":\"\",\"relation\":\"compatible\",\"disposition\":\"false_positive\",\"quotes\":[]}"},"finish_reason":"stop"}]}`)
	}))
	defer provider.Close()
	config := gatewayTestConfig("openai-compatible", provider.URL+"/v1")
	t.Setenv("CAW_STRUCTURED_OUTPUT_ENABLED", "true")
	t.Setenv("CAW_STRUCTURED_OUTPUT_PROVIDER_URL", config.UpstreamBaseURL)
	t.Setenv("CAW_STRUCTURED_OUTPUT_REVIEW_MODEL", config.ChatModel)
	id, _, err := model.StructuredConfigurationIDFor(config.UpstreamBaseURL, config.ChatModel, config.OCRModel)
	if err != nil {
		t.Fatal(err)
	}
	_, gateway := startGatewayServer(t, st, config)
	release := make(chan struct{})
	models := &frozenBarrier{gatewayBarrierModels: &gatewayBarrierModels{entered: make(chan model.DelegatedScope, 1), release: release}, identity: id}
	st.UseModels(models)
	caseID, _ := createGatewayCase(t, st, admin, "structured frozen review")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if _, err := st.StartReviewAsync(ctx, admin, caseID); err != nil {
		t.Fatal(err)
	}
	claim, err := st.ClaimNextJob(ctx, "structured-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = st.RunClaimedJob(ctx, claim) }()
	defer func() { close(release); <-done }()
	var scope model.DelegatedScope
	select {
	case scope = <-models.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	scope.ConfigurationID = id
	scope.ProviderURL = config.UpstreamBaseURL
	client := model.New(model.Config{BaseURL: gateway.URL + "/v1", ProviderURL: config.UpstreamBaseURL, Gateway: true, APIKey: config.WorkerCredential, ChatModel: config.ChatModel, OCRModel: config.OCRModel})
	if _, err := client.Warranty(model.WithDelegatedScope(ctx, scope), nil); err != nil {
		t.Fatal(err)
	}
	post := func(declared model.DelegatedScope, format json.RawMessage) int {
		payload, _ := json.Marshal(map[string]any{"model": config.ChatModel, "max_tokens": 8, "messages": []map[string]string{{"role": "user", "content": "test"}}, "response_format": format})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Contract-Service-Key", config.WorkerCredential)
		req.Header.Set("X-Audit-Execution-Scope", encodedGatewayScope(t, declared))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode
	}
	old := scope
	old.ConfigurationID = model.ConfigurationIDFor(config.UpstreamBaseURL, config.ChatModel, config.OCRModel)
	if status := post(old, model.StructuredResponseFormat("warranty")); status < 400 {
		t.Fatalf("old identity accepted: %d", status)
	}
	if status := post(scope, json.RawMessage(`{"type":"json_schema"}`)); status < 400 {
		t.Fatalf("arbitrary schema accepted: %d", status)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls=%d", calls.Load())
	}
	usage, active := readGatewayAccounting(t, caseID)
	if len(usage) != 1 || usage[0].Status != "completed" || !usage[0].TotalTokens.Valid || usage[0].TotalTokens.Int64 != 18 {
		t.Fatalf("settlement %+v", usage)
	}
	assertNoActiveGatewayLeases(t, active)
}

func (m *frozenBarrier) ConfigurationID() string { return m.identity }

func TestChatUsesServerFrozenIdentityDespiteOmittedOrForgedScope(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()
	config := gatewayTestConfig("openai-compatible", provider.URL+"/v1")
	_, gateway := startGatewayServer(t, st, config)
	release := make(chan struct{})
	models := &frozenBarrier{gatewayBarrierModels: &gatewayBarrierModels{entered: make(chan model.DelegatedScope, 1), release: release}, identity: model.ConfigurationIDFor(config.UpstreamBaseURL, config.ChatModel, config.OCRModel)}
	st.UseModels(models)
	caseID, _ := createGatewayCase(t, st, admin, "authoritative frozen model")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	_, err := st.StartReviewAsync(ctx, admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := st.ClaimNextJob(ctx, "frozen-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = st.RunClaimedJob(ctx, claim) }()
	defer func() { close(release); <-done }()
	var scope model.DelegatedScope
	select {
	case scope = <-models.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	client := &http.Client{Timeout: 5 * time.Second}
	request := func(scope model.DelegatedScope) (int, []byte) {
		t.Helper()
		response, body, err := gatewayChatRequest(ctx, client, gateway.URL+"/v1/chat/completions", []string{config.WorkerCredential}, encodedGatewayScope(t, scope), config.ChatModel, 8)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, body
	}
	scope.ConfigurationID = ""
	scope.ProviderURL = ""
	status, body := request(scope)
	if status != http.StatusOK {
		t.Fatalf("correct frozen provider with omitted declaration: %d %s", status, body)
	}
	// Change the server-selected upstream without altering the frozen snapshot.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(http.StatusOK) }))
	defer other.Close()
	otherConfig := gatewayTestConfig("openai-compatible", other.URL+"/v1")
	_, otherGateway := startGatewayServer(t, st, otherConfig)
	for _, declared := range []string{"", model.ConfigurationIDFor(otherConfig.UpstreamBaseURL, otherConfig.ChatModel, otherConfig.OCRModel)} {
		scope.ConfigurationID = declared
		response, body, err := gatewayChatRequest(ctx, client, otherGateway.URL+"/v1/chat/completions", []string{otherConfig.WorkerCredential}, encodedGatewayScope(t, scope), otherConfig.ChatModel, 8)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusConflict || gatewayErrorCode(t, body) != "model_configuration_changed" {
			t.Fatalf("forged declaration %q: %d %s", declared, response.StatusCode, body)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("wrong provider dispatched; calls=%d", calls.Load())
	}
	scope.CaseID = "foreign-case"
	response, body, err := gatewayChatRequest(ctx, client, otherGateway.URL+"/v1/chat/completions", []string{otherConfig.WorkerCredential}, encodedGatewayScope(t, scope), otherConfig.ChatModel, 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("unauthorized request: %d %s", response.StatusCode, body)
	}
}

func TestStructuredContractDriftAfterReservationReleasesLease(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusOK) }))
	defer provider.Close()
	config := gatewayTestConfig("openai-compatible", provider.URL+"/v1")
	t.Setenv("CAW_STRUCTURED_OUTPUT_ENABLED", "true")
	t.Setenv("CAW_STRUCTURED_OUTPUT_PROVIDER_URL", config.UpstreamBaseURL)
	t.Setenv("CAW_STRUCTURED_OUTPUT_REVIEW_MODEL", config.ChatModel)
	id, _, err := model.StructuredConfigurationIDFor(config.UpstreamBaseURL, config.ChatModel, config.OCRModel)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	priceTable := pgx.Identifier{st.SchemaName(), "model_price_snapshots"}.Sanitize()
	if _, err := pool.Exec(context.Background(), `INSERT INTO `+priceTable+`(snapshot_id,provider_config_id,model_ref,currency,minor_unit_scale,input_micro_usd_per_million,output_micro_usd_per_million,published_by,payload_sha256) VALUES('drift-price',$1,$2,'USD',6,1000000,1000000,'fixture',repeat('a',64))`, id, config.ChatModel); err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	models := &frozenBarrier{gatewayBarrierModels: &gatewayBarrierModels{entered: make(chan model.DelegatedScope, 1), release: release}, identity: id}
	st.UseModels(models)
	caseID, _ := createGatewayCase(t, st, admin, "structured drift after reserve")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if _, err := st.StartReviewAsync(ctx, admin, caseID); err != nil {
		t.Fatal(err)
	}
	claim, err := st.ClaimNextJob(ctx, "structured-drift-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = st.RunClaimedJob(ctx, claim) }()
	defer func() { close(release); <-done }()
	var scope model.DelegatedScope
	select {
	case scope = <-models.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	scope.ConfigurationID, scope.ProviderURL = id, config.UpstreamBaseURL
	service, gateway := startGatewayServer(t, st, config)
	_ = gateway
	payload, err := json.Marshal(map[string]any{"model": config.ChatModel, "max_tokens": 8, "messages": []map[string]string{{"role": "user", "content": "test"}}, "response_format": model.StructuredResponseFormat("warranty")})
	if err != nil {
		t.Fatal(err)
	}
	raw := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(payload))
	raw.Header.Set("X-Audit-Execution-Scope", encodedGatewayScope(t, scope))
	recorder := httptest.NewRecorder()
	request, ok := service.readChatRequest(recorder, raw, "worker")
	if !ok {
		t.Fatalf("read: %d %s", recorder.Code, recorder.Body)
	}
	lease, ok := service.reserveChatAttempt(recorder, ctx, request, 1)
	if !ok {
		t.Fatalf("reserve: %d %s", recorder.Code, recorder.Body)
	}
	rows, active := readGatewayAccounting(t, caseID)
	if len(rows) != 0 || active != 1 {
		t.Fatalf("reservation absent: rows=%+v active=%d", rows, active)
	}
	ledgerTable := pgx.Identifier{st.SchemaName(), "model_money_ledger"}.Sanitize()
	var reserveCount int
	var reserveAmount string
	if err := pool.QueryRow(ctx, `SELECT count(*),coalesce(sum(amount_minor),0)::text FROM `+ledgerTable+` WHERE lease_id=$1 AND event_kind='reserve'`, lease.ID).Scan(&reserveCount, &reserveAmount); err != nil || reserveCount != 1 || reserveAmount == "0" {
		t.Fatalf("money reservation count=%d amount=%s err=%v", reserveCount, reserveAmount, err)
	}
	t.Setenv("CAW_STRUCTURED_OUTPUT_REVIEW_MODEL", "different-review")
	recorder = httptest.NewRecorder()
	if service.runReservedChatAttempt(recorder, ctx, request, 0, 1, lease) {
		t.Fatal("drift caused retry")
	}
	if recorder.Code != http.StatusConflict || gatewayErrorCode(t, recorder.Body.Bytes()) != "model_configuration_changed" {
		t.Fatalf("response: %d %s", recorder.Code, recorder.Body)
	}
	if calls.Load() != 0 {
		t.Fatalf("provider calls=%d", calls.Load())
	}
	rows, active = readGatewayAccounting(t, caseID)
	if len(rows) != 1 || rows[0].Status != "failed" || rows[0].ErrorCode != "model_configuration_changed" || !rows[0].BudgetReleased || rows[0].ChargedTokens != 0 || rows[0].InputTokens.Valid || rows[0].OutputTokens.Valid || rows[0].TotalTokens.Valid {
		t.Fatalf("settlement: %+v", rows)
	}
	assertNoActiveGatewayLeases(t, active)
	var count, releaseCount int
	var balance, costState, cost string
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE event_kind='release'),coalesce(sum(amount_minor),0)::text FROM `+ledgerTable+` WHERE lease_id=$1`, lease.ID).Scan(&count, &releaseCount, &balance); err != nil || count != 2 || releaseCount != 1 || balance != "0" {
		t.Fatalf("money ledger count=%d release=%d balance=%s err=%v", count, releaseCount, balance, err)
	}
	usageTable := pgx.Identifier{st.SchemaName(), "model_usage"}.Sanitize()
	if err := pool.QueryRow(ctx, `SELECT money_cost_state,money_cost_minor::text FROM `+usageTable+` WHERE lease_id=$1`, lease.ID).Scan(&costState, &cost); err != nil || costState != "pre_dispatch_free" || cost != "0" {
		t.Fatalf("money settlement state=%s cost=%s err=%v", costState, cost, err)
	}
}

func TestStructuredProviderRejectionSettlesWithoutRetry(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		status := http.StatusBadRequest
		if calls.Add(1) == 2 {
			status = http.StatusUnprocessableEntity
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "private provider diagnostic")
	}))
	defer provider.Close()
	config := gatewayTestConfig("openai-compatible", provider.URL+"/v1")
	config.Retries = 1
	t.Setenv("CAW_STRUCTURED_OUTPUT_ENABLED", "true")
	t.Setenv("CAW_STRUCTURED_OUTPUT_PROVIDER_URL", config.UpstreamBaseURL)
	t.Setenv("CAW_STRUCTURED_OUTPUT_REVIEW_MODEL", config.ChatModel)
	id, _, err := model.StructuredConfigurationIDFor(config.UpstreamBaseURL, config.ChatModel, config.OCRModel)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	models := &frozenBarrier{gatewayBarrierModels: &gatewayBarrierModels{entered: make(chan model.DelegatedScope, 1), release: release}, identity: id}
	st.UseModels(models)
	caseID, _ := createGatewayCase(t, st, admin, "structured rejected")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if _, err := st.StartReviewAsync(ctx, admin, caseID); err != nil {
		t.Fatal(err)
	}
	claim, err := st.ClaimNextJob(ctx, "structured-rejected-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = st.RunClaimedJob(ctx, claim) }()
	defer func() { close(release); <-done }()
	var scope model.DelegatedScope
	select {
	case scope = <-models.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	scope.ConfigurationID, scope.ProviderURL = id, config.UpstreamBaseURL
	_, gateway := startGatewayServer(t, st, config)
	for range 2 {
		payload, _ := json.Marshal(map[string]any{"model": config.ChatModel, "max_tokens": 8, "messages": []map[string]string{{"role": "user", "content": "test"}}, "response_format": model.StructuredResponseFormat("warranty")})
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Contract-Service-Key", config.WorkerCredential)
		request.Header.Set("X-Audit-Execution-Scope", encodedGatewayScope(t, scope))
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusBadGateway || gatewayErrorCode(t, body) != "model_structured_request_rejected" || bytes.Contains(body, []byte("private provider diagnostic")) {
			t.Fatalf("response %d %s", response.StatusCode, body)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("provider calls=%d, want two without retry", calls.Load())
	}
	rows, active := readGatewayAccounting(t, caseID)
	if len(rows) != 2 {
		t.Fatalf("settlements %+v", rows)
	}
	codes := map[string]int{}
	for _, row := range rows {
		if row.Status != "failed" || (row.ErrorCode != "provider_http_400" && row.ErrorCode != "provider_http_422") {
			t.Fatalf("settlement %+v", row)
		}
		codes[row.ErrorCode]++
		assertUnknownUsageConservativelyCharged(t, row)
	}
	if codes["provider_http_400"] != 1 || codes["provider_http_422"] != 1 {
		t.Fatalf("expected one settlement per provider status, got %v", codes)
	}
	assertNoActiveGatewayLeases(t, active)
}
