package modelgateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/agentflow"
	"contractaudit/internal/jev"
	"contractaudit/internal/model"
	"contractaudit/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func gatewayJevConfig() JevConfig {
	return JevConfig{
		Mode: agentflow.AdvisoryModeFixture, Model: agentflow.AdvisoryFixtureModel,
		Features: []string{agentflow.AdvisoryFeatureKnowledgeRerank},
		Egress:   agentflow.AdvisoryEgressMinimized, Timeout: 3 * time.Second, MaxOutputTokens: 512,
	}
}

func gatewayJevBody(t *testing.T, modelID string) []byte {
	t.Helper()
	questions := map[string]any{
		"candidate_0_relevance": map[string]any{
			"type": "noul", "instructions": "Does the item directly regulate the gap capability?",
			"criteria": map[string]string{"true": "It directly applies.", "false": "It addresses another capability."},
		},
	}
	body, err := json.Marshal(map[string]any{
		"model":     modelID,
		"state":     `{"feature":"knowledge_rerank","scope":{"business_line":"telecom_enterprise"},"gaps":[{"domain":"compliance","capability":"warranty"}],"candidates":[{"id":"candidate_0","kind":"guidance","summary":"compare warranty"}]}`,
		"questions": questions,
	})
	if err != nil {
		t.Fatalf("marshal Jev request: %v", err)
	}
	return body
}

func markGatewayRunRunning(t *testing.T, claim store.JobClaim) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	schema := strings.TrimSpace(os.Getenv("DATABASE_SCHEMA"))
	if dsn == "" || schema == "" {
		t.Fatal("isolated gateway database scope is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to isolated gateway schema: %v", err)
	}
	defer pool.Close()
	qualified := pgx.Identifier{schema, "review_runs"}.Sanitize()
	tag, err := pool.Exec(ctx, `UPDATE `+qualified+`
		SET state='running', updated_at=clock_timestamp()
		WHERE id=$1 AND tenant_id=$2 AND org_id=$3 AND case_id=$4 AND state='accepted'`,
		claim.RunID, claim.TenantID, claim.OrgID, claim.CaseID)
	if err != nil {
		t.Fatalf("start isolated review run: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("started %d isolated review runs, want one", tag.RowsAffected())
	}
}

func postGatewayJev(t *testing.T, endpoint, token string, scope *model.DelegatedScope, feature, classification string, body []byte) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, endpoint+"/v1/systemone", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("build Jev request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Contract-Service-Key", token)
	if scope != nil {
		raw, err := json.Marshal(scope)
		if err != nil {
			t.Fatalf("marshal scope: %v", err)
		}
		request.Header.Set("X-Audit-Execution-Scope", string(raw))
	}
	if feature != "" {
		request.Header.Set("X-Contract-Jev-Feature", feature)
	}
	if classification != "" {
		request.Header.Set("X-Contract-Data-Classification", classification)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send Jev request: %v", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read Jev response: %v", err)
	}
	return response, payload
}

func TestGatewayJevFixtureSettlesOneBudgetedCall(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("TYPESAFE_JEV_MODE", "off")
	st, admin := openGatewayTestStore(t)
	config := gatewayTestConfig("fixture", "")
	config.FixtureOCRContent = "质保期为36个月。"
	config.Jev = gatewayJevConfig()
	_, gateway := startGatewayServer(t, st, config)

	caseID, _ := createGatewayCase(t, st, admin, "gateway Jev fixture call")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := st.StartReviewAsync(ctx, admin, caseID); err != nil {
		t.Fatalf("accept review for Jev scope: %v", err)
	}
	claim, err := st.ClaimNextJob(ctx, "jev-gateway-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim review for Jev scope: %v", err)
	}
	markGatewayRunRunning(t, claim)
	scope := executionGatewayScope(claim)
	response, payload := postGatewayJev(t, gateway.URL, config.WorkerCredential, &scope,
		agentflow.AdvisoryFeatureKnowledgeRerank, agentflow.AdvisoryEgressMinimized,
		gatewayJevBody(t, agentflow.AdvisoryFixtureModel))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Jev fixture route = %d: %s", response.StatusCode, payload)
	}
	if response.Header.Get("X-Model-Backend") != "local-fixture" {
		t.Fatalf("fixture response is not marked local: %q", response.Header.Get("X-Model-Backend"))
	}
	questions := map[string]jev.NoulQuestion{
		"candidate_0_relevance": {Instructions: "Does the item directly regulate the gap capability?"},
	}
	answers, meta, err := jev.ParseSystemOne(payload, questions)
	if err != nil {
		t.Fatalf("gateway response is not a valid provider envelope: %v", err)
	}
	if meta.Model != agentflow.AdvisoryFixtureModel || answers["candidate_0_relevance"].Noul != 0.82 {
		t.Fatalf("gateway response = %+v / %+v", meta, answers)
	}

	usage, active := readGatewayAccounting(t, caseID)
	if len(usage) != 1 || usage[0].Caller != "worker" || usage[0].Model != "typesafe/"+agentflow.AdvisoryFixtureModel || usage[0].Status != "completed" {
		t.Fatalf("Jev fixture call was not settled exactly once: %+v", usage)
	}
	if usage[0].ChargedTokens != 0 || !usage[0].UsageSource.Valid || usage[0].UsageSource.String != "local_fixture" {
		t.Fatalf("fixture usage settlement = %+v", usage[0])
	}
	assertNoActiveGatewayLeases(t, active)

	healthResponse, err := http.Get(gateway.URL + "/health/jev")
	if err != nil {
		t.Fatalf("read Jev health: %v", err)
	}
	defer healthResponse.Body.Close()
	var health map[string]any
	if err := json.NewDecoder(healthResponse.Body).Decode(&health); err != nil {
		t.Fatalf("decode Jev health: %v", err)
	}
	if health["mode"] != agentflow.AdvisoryModeFixture || health["status"] != "fixture" || health["model"] != agentflow.AdvisoryFixtureModel {
		t.Fatalf("Jev health = %+v", health)
	}
	readyResponse, err := http.Get(gateway.URL + "/health/ready")
	if err != nil {
		t.Fatalf("read readiness: %v", err)
	}
	defer readyResponse.Body.Close()
	if readyResponse.StatusCode != http.StatusOK {
		t.Fatalf("Jev fixture made chat readiness fail: %d", readyResponse.StatusCode)
	}
}

func TestGatewayJevRouteGatesIdentityScopeAndClassification(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("TYPESAFE_JEV_MODE", "off")
	st, admin := openGatewayTestStore(t)
	config := gatewayTestConfig("fixture", "")
	config.FixtureOCRContent = "质保期为36个月。"
	config.Jev = gatewayJevConfig()
	_, gateway := startGatewayServer(t, st, config)

	caseID, _ := createGatewayCase(t, st, admin, "gateway Jev gates")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := st.StartReviewAsync(ctx, admin, caseID); err != nil {
		t.Fatalf("accept review for Jev scope: %v", err)
	}
	claim, err := st.ClaimNextJob(ctx, "jev-gate-test-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim review for Jev scope: %v", err)
	}
	markGatewayRunRunning(t, claim)
	scope := executionGatewayScope(claim)
	tests := []struct {
		name           string
		token          string
		scope          *model.DelegatedScope
		feature        string
		classification string
		modelID        string
		want           int
	}{
		{"document identity", config.DocumentCredential, &scope, agentflow.AdvisoryFeatureKnowledgeRerank, agentflow.AdvisoryEgressMinimized, agentflow.AdvisoryFixtureModel, http.StatusForbidden},
		{"missing scope", config.WorkerCredential, nil, agentflow.AdvisoryFeatureKnowledgeRerank, agentflow.AdvisoryEgressMinimized, agentflow.AdvisoryFixtureModel, http.StatusBadRequest},
		{"missing feature", config.WorkerCredential, &scope, "", agentflow.AdvisoryEgressMinimized, agentflow.AdvisoryFixtureModel, http.StatusBadRequest},
		{"unknown feature", config.WorkerCredential, &scope, "citation_support", agentflow.AdvisoryEgressMinimized, agentflow.AdvisoryFixtureModel, http.StatusBadRequest},
		{"missing classification", config.WorkerCredential, &scope, agentflow.AdvisoryFeatureKnowledgeRerank, "", agentflow.AdvisoryFixtureModel, http.StatusBadRequest},
		{"approved text", config.WorkerCredential, &scope, agentflow.AdvisoryFeatureKnowledgeRerank, agentflow.AdvisoryEgressApprovedText, agentflow.AdvisoryFixtureModel, http.StatusForbidden},
		{"model drift", config.WorkerCredential, &scope, agentflow.AdvisoryFeatureKnowledgeRerank, agentflow.AdvisoryEgressMinimized, "jev-1.14.0", http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bodyModel := agentflow.AdvisoryFixtureModel
			if test.modelID != "" {
				bodyModel = test.modelID
			}
			response, payload := postGatewayJev(t, gateway.URL, test.token, test.scope,
				test.feature, test.classification, gatewayJevBody(t, bodyModel))
			if response.StatusCode != test.want {
				t.Fatalf("status = %d, want %d: %s", response.StatusCode, test.want, payload)
			}
		})
	}
	usage, _ := readGatewayAccounting(t, caseID)
	if len(usage) != 0 {
		t.Fatalf("a rejected gate spent provider budget: %+v", usage)
	}
}

func TestGatewayJevUnconfiguredDoesNotChangeChatReadiness(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("TYPESAFE_JEV_MODE", "off")
	st, _ := openGatewayTestStore(t)
	config := gatewayTestConfig("fixture", "")
	config.FixtureOCRContent = "质保期为36个月。"
	config.Jev = JevConfig{
		Mode: agentflow.AdvisoryModeAdvisory, Model: "jev-1.14.0",
		Features: []string{agentflow.AdvisoryFeatureKnowledgeRerank},
		Egress:   agentflow.AdvisoryEgressMinimized, Timeout: 3 * time.Second, MaxOutputTokens: 512,
	}
	_, gateway := startGatewayServer(t, st, config)

	readyResponse, err := http.Get(gateway.URL + "/health/ready")
	if err != nil {
		t.Fatalf("read readiness: %v", err)
	}
	defer readyResponse.Body.Close()
	if readyResponse.StatusCode != http.StatusOK {
		t.Fatalf("an unconfigured Jev route changed chat/OCR readiness: %d", readyResponse.StatusCode)
	}
	healthResponse, err := http.Get(gateway.URL + "/health/jev")
	if err != nil {
		t.Fatalf("read Jev health: %v", err)
	}
	defer healthResponse.Body.Close()
	var health map[string]any
	if err := json.NewDecoder(healthResponse.Body).Decode(&health); err != nil {
		t.Fatalf("decode Jev health: %v", err)
	}
	if health["status"] != "unconfigured" || health["reason"] != "api_key_missing" {
		t.Fatalf("Jev health = %+v", health)
	}
	if _, ok := health["api_key"]; ok {
		t.Fatalf("Jev health leaked a credential field: %+v", health)
	}
	response, payload := postGatewayJev(t, gateway.URL, config.WorkerCredential, &model.DelegatedScope{Kind: "execution"},
		agentflow.AdvisoryFeatureKnowledgeRerank, agentflow.AdvisoryEgressMinimized,
		gatewayJevBody(t, agentflow.AdvisoryFixtureModel))
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(payload), "jev_unconfigured") {
		t.Fatalf("unconfigured Jev route = %d: %s", response.StatusCode, payload)
	}
}
