package systemhealth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func jevServiceFromSnapshot(t *testing.T, snapshot Snapshot) Service {
	t.Helper()
	for _, service := range snapshot.Services {
		if service.ID == "jev-model" {
			return service
		}
	}
	t.Fatal("Jev service row is missing")
	return Service{}
}

func TestJevConfigurationIsReportedWithoutCredentialOrProbe(t *testing.T) {
	const secret = "apikey_health_secret_000000000000000000000000"
	snapshot := New(Config{Jev: JevConfig{
		Name: "jev-1.14.0", Endpoint: "https://health-user:password@api.typesafe.ai/v1?token=" + secret,
		Mode: "advisory", Provider: "model-gateway", Egress: "minimized", Features: []string{"knowledge_rerank"},
		KeyConfigured: true, Valid: true,
	}}).Snapshot(context.Background())
	service := jevServiceFromSnapshot(t, snapshot)
	if service.Status != "unknown" || service.Probe != nil ||
		!strings.Contains(service.Detail, "jev-1.14.0") ||
		!strings.Contains(service.Detail, "不发起付费推理") {
		t.Fatalf("Jev configuration was not reported honestly: %+v", service)
	}
	if service.Target != "https://api.typesafe.ai/v1" {
		t.Fatalf("Jev target was not sanitized: %q", service.Target)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "health-user") {
		t.Fatalf("health snapshot leaked Jev credentials: %s", raw)
	}
}

func TestJevConfigurationStatesRemainDistinct(t *testing.T) {
	for _, test := range []struct {
		name   string
		config JevConfig
		status string
		detail string
	}{
		{name: "missing direct key", config: JevConfig{Name: "jev-1.14.0", Endpoint: "https://api.typesafe.ai", Mode: "advisory", Provider: "direct", Egress: "minimized", Features: []string{"knowledge_rerank"}, Valid: true}, status: "unconfigured", detail: "TYPESAFE_API_KEY"},
		{name: "gateway missing key", config: JevConfig{Name: "jev-1.14.0", Endpoint: "https://api.typesafe.ai", Mode: "advisory", Provider: "model-gateway", Egress: "minimized", Features: []string{"knowledge_rerank"}, Valid: true, GatewayStatus: "unconfigured"}, status: "unconfigured", detail: "model-gateway"},
		{name: "invalid configuration", config: JevConfig{Name: "jev-latest", Endpoint: "http://api.typesafe.ai", Mode: "advisory", KeyConfigured: true, Valid: false, Reason: "model_alias_not_allowed"}, status: "unavailable", detail: "配置无效"},
		{name: "configured", config: JevConfig{Name: "jev-1.14.0", Endpoint: "https://api.typesafe.ai", Mode: "advisory", Provider: "model-gateway", Egress: "minimized", Features: []string{"knowledge_rerank"}, KeyConfigured: true, Valid: true}, status: "unknown", detail: "jev-1.14.0"},
		{name: "fixture", config: JevConfig{Name: "jev-fixture", Mode: "fixture", Provider: "local-fixture", Egress: "minimized", Features: []string{"knowledge_rerank"}, Valid: true}, status: "unknown", detail: "fixture"},
		{name: "off", config: JevConfig{Name: "jev-1.14.0", Mode: "off", Valid: true}, status: "unconfigured", detail: "已关闭"},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := New(Config{Jev: test.config}).Snapshot(context.Background())
			service := jevServiceFromSnapshot(t, snapshot)
			if service.Status != test.status || !strings.Contains(service.Detail, test.detail) {
				t.Fatalf("service = %+v", service)
			}
		})
	}
}

func TestJevGatewayStatusIsReadWithoutCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health/jev" || r.Method != http.MethodGet {
			t.Errorf("probe = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Contract-Service-Key") != "" {
			t.Errorf("health probe sent credentials: %+v", r.Header)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"mode": "advisory", "status": "unconfigured", "model": "jev-1.14.0",
			"features": []string{"knowledge_rerank"}, "egress": "minimized", "reason": "api_key_missing",
		})
	}))
	defer server.Close()
	snapshot := New(Config{Jev: JevConfig{
		Name: "jev-1.14.0", Endpoint: "https://api.typesafe.ai",
		Mode: "advisory", Provider: "model-gateway", Egress: "minimized", Features: []string{"knowledge_rerank"},
		Valid: true, GatewayURL: server.URL,
	}}).Snapshot(context.Background())
	service := jevServiceFromSnapshot(t, snapshot)
	if service.Status != "unconfigured" || !strings.Contains(service.Detail, "model-gateway") {
		t.Fatalf("gateway-reported status = %+v", service)
	}
}

func TestJevEnvironmentStatusKeepsTheKeyOutOfTheSnapshot(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("TYPESAFE_JEV_MODE", "advisory")
	t.Setenv("TYPESAFE_JEV_BASE_URL", "https://api.typesafe.ai")
	t.Setenv("TYPESAFE_JEV_MODEL", "jev-1.14.0")
	t.Setenv("TYPESAFE_JEV_PROVIDER", "model-gateway")
	t.Setenv("TYPESAFE_API_KEY", strings.Repeat("k", 32))
	t.Setenv("MODEL_GATEWAY_URL", "http://model-gateway:8084/v1/")
	config := JevFromEnv()
	if !config.KeyConfigured || !config.Valid || config.Name != "jev-1.14.0" || config.Endpoint != "https://api.typesafe.ai" ||
		config.Mode != "advisory" || config.Provider != "model-gateway" ||
		config.GatewayURL != "http://model-gateway:8084" {
		t.Fatalf("JevFromEnv = %+v", config)
	}
	snapshot := New(Config{Jev: config}).Snapshot(context.Background())
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if strings.Contains(string(raw), "apikey_environment_secret") {
		t.Fatalf("health snapshot leaked the environment key: %s", raw)
	}
}
