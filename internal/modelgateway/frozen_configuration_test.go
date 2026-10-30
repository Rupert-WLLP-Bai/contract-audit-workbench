package modelgateway

import (
	"context"
	"net/http"
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
