package modelgateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
)

// dispatchingAddress is the only thing a caller's frozen configuration id can
// name here: the fixture gateway never contacts a provider.
func frozenGatewayConfig() Config {
	config := gatewayTestConfig("fixture", "")
	config.FixtureOCRContent = "质保期为36个月。"
	return config
}

func frozenDocumentScope(admin accounts.User, caseID, documentID, configurationID string) model.DelegatedScope {
	scope := documentGatewayScope(admin, caseID, documentID)
	scope.ConfigurationID = configurationID
	return scope
}

// A caller that froze configuration A must not be served by endpoint B just
// because B still accepts the same model id.
func TestGatewayRejectsAFrozenConfigurationThatNoLongerMatches(t *testing.T) {
	config := frozenGatewayConfig()
	t.Setenv("APP_ENV", "test")
	st, admin := openGatewayTestStore(t)
	_, gateway := startGatewayServer(t, st, config)
	caseID, documents := createGatewayCase(t, st, admin, "gateway frozen configuration")
	client := &http.Client{Timeout: 5 * time.Second}
	endpoint := gateway.URL + "/v1/chat/completions"

	correct := model.ConfigurationIDFor(config.UpstreamBaseURL, config.ChatModel, config.OCRModel)
	response, body, err := gatewayChatRequest(context.Background(), client, endpoint,
		[]string{config.DocumentCredential},
		encodedGatewayScope(t, frozenDocumentScope(admin, caseID, documents[0], correct)),
		config.ChatModel, 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode == http.StatusConflict {
		t.Fatalf("the frozen configuration was rejected: %d %s", response.StatusCode, body)
	}

	other := model.ConfigurationIDFor("https://other.example/v1", config.ChatModel, config.OCRModel)
	response, body, err = gatewayChatRequest(context.Background(), client, endpoint,
		[]string{config.DocumentCredential},
		encodedGatewayScope(t, frozenDocumentScope(admin, caseID, documents[0], other)),
		config.ChatModel, 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusConflict || gatewayErrorCode(t, body) != "model_configuration_changed" {
		t.Fatalf("a changed endpoint accepted the frozen identity: %d %s", response.StatusCode, body)
	}
}

func TestGatewayProductionFrozenMismatchAfterAuthorization(t *testing.T) {
	st, admin := openGatewayTestStore(t)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer provider.Close()
	config := gatewayTestConfig("openai-compatible", provider.URL+"/v1")
	_, gateway := startGatewayServer(t, st, config)
	caseID, docs := createGatewayCase(t, st, admin, "frozen production endpoint")
	otherCase, _ := createGatewayCase(t, st, admin, "other case")
	gatewayURL := gateway.URL + "/v1"
	scope := frozenDocumentScope(admin, caseID, docs[0], model.ConfigurationIDFor(gatewayURL, config.ChatModel, config.OCRModel))
	scope.ProviderURL = gatewayURL
	client := &http.Client{Timeout: 5 * time.Second}
	request := func(scope model.DelegatedScope) (*http.Response, []byte, error) {
		return gatewayChatRequest(context.Background(), client, gatewayURL+"/chat/completions", []string{config.DocumentCredential}, encodedGatewayScope(t, scope), config.ChatModel, 8)
	}
	crossCase := scope
	crossCase.CaseID = otherCase
	response, body, err := request(crossCase)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden || gatewayErrorCode(t, body) != "model_scope_unavailable" {
		t.Fatalf("cross-case mismatch response = %d %s", response.StatusCode, body)
	}
	response, body, err = request(scope)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusConflict || gatewayErrorCode(t, body) != "model_configuration_changed" {
		t.Fatalf("production mismatch response = %d %s", response.StatusCode, body)
	}
	if calls.Load() != 0 {
		t.Fatal("mismatched request reached provider")
	}
	usage, active := readGatewayAccounting(t, caseID)
	if len(usage) != 1 || usage[0].ErrorCode != "model_configuration_changed" || !usage[0].BudgetReleased || usage[0].ChargedTokens != 0 {
		t.Fatalf("mismatched request was not released: %+v", usage)
	}
	assertNoActiveGatewayLeases(t, active)
}

// Fixture deployments and internal callers send no identity; they keep the
// model-name check only.
func TestGatewayAllowsCallersWithoutAFrozenIdentity(t *testing.T) {
	config := frozenGatewayConfig()
	t.Setenv("APP_ENV", "test")
	st, admin := openGatewayTestStore(t)
	_, gateway := startGatewayServer(t, st, config)
	caseID, documents := createGatewayCase(t, st, admin, "gateway unconstrained caller")
	client := &http.Client{Timeout: 5 * time.Second}

	response, body, err := gatewayChatRequest(context.Background(), client,
		gateway.URL+"/v1/chat/completions", []string{config.DocumentCredential},
		encodedGatewayScope(t, documentGatewayScope(admin, caseID, documents[0])), config.ChatModel, 8)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode == http.StatusConflict {
		t.Fatalf("an unidentified caller was constrained by a configuration id: %d %s", response.StatusCode, body)
	}
}
