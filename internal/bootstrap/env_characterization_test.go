package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/execution"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var bootstrapTelemetryEnvironment = []string{
	"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_TRACES_SAMPLER", "OTEL_TRACES_SAMPLER_ARG", "POD_UID", "APP_ENV",
}

func clearTelemetryEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range bootstrapTelemetryEnvironment {
		t.Setenv(key, "")
	}
}

func TestConfiguredTelemetrySelectsOnlyApprovedExporterSettings(t *testing.T) {
	clearTelemetryEnvironment(t)
	exporter, err := configuredTelemetry("worker")
	if exporter != nil || err != nil {
		t.Fatalf("unset endpoint must disable telemetry: %v %v", exporter, err)
	}

	clearTelemetryEnvironment(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
	if _, err := configuredTelemetry("worker"); err == nil || err.Error() != "the configured trace exporter requires OTLP http/json" {
		t.Fatalf("unsupported protocol accepted: %v", err)
	}

	clearTelemetryEnvironment(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/json")
	t.Setenv("OTEL_TRACES_SAMPLER", "always_off")
	if exporter, err := configuredTelemetry("worker"); exporter == nil || err != nil {
		t.Fatalf("always_off must leave metrics enabled: %v %v", exporter, err)
	} else {
		exporter.Close()
	}

	clearTelemetryEnvironment(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	t.Setenv("OTEL_TRACES_SAMPLER", "parentbased_traceidratio")
	if _, err := configuredTelemetry("worker"); err == nil || err.Error() != "unsupported OTEL_TRACES_SAMPLER" {
		t.Fatalf("unsupported sampler accepted: %v", err)
	}

	clearTelemetryEnvironment(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	t.Setenv("OTEL_TRACES_SAMPLER", "traceidratio")
	for _, arg := range []string{"abc", "NaN", "1.5", "-0.5"} {
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", arg)
		if _, err := configuredTelemetry("worker"); err == nil {
			t.Fatalf("sampler argument %q accepted", arg)
		}
	}
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.25")
	exporter, err = configuredTelemetry("worker")
	if err != nil || exporter == nil {
		t.Fatalf("valid ratio rejected: %v", err)
	}
	exporter.Close()

	clearTelemetryEnvironment(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "ftp://collector.example")
	if _, err := configuredTelemetry("worker"); err == nil {
		t.Fatal("unsupported collector endpoint accepted")
	}
}

func TestConfiguredTelemetryNamesTheServiceAndInstance(t *testing.T) {
	type resource struct {
		Service  string
		Instance string
	}
	capture := func(t *testing.T) (*httptest.Server, <-chan resource) {
		t.Helper()
		results := make(chan resource, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/traces" {
				body, _ := io.ReadAll(r.Body)
				var request struct {
					ResourceSpans []struct {
						Resource struct {
							Attributes []struct {
								Key   string `json:"key"`
								Value struct {
									String string `json:"stringValue"`
								} `json:"value"`
							} `json:"attributes"`
						} `json:"resource"`
					} `json:"resourceSpans"`
				}
				if err := json.Unmarshal(body, &request); err == nil && len(request.ResourceSpans) == 1 {
					values := map[string]string{}
					for _, item := range request.ResourceSpans[0].Resource.Attributes {
						values[item.Key] = item.Value.String
					}
					select {
					case results <- resource{Service: values["service.name"], Instance: values["service.instance.id"]}:
					default:
					}
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{}`)
		}))
		t.Cleanup(server.Close)
		return server, results
	}

	clearTelemetryEnvironment(t)
	server, results := capture(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
	t.Setenv("POD_UID", " pod-9 ")
	exporter, err := configuredTelemetry("")
	if err != nil || exporter == nil {
		t.Fatalf("configuredTelemetry: %v", err)
	}
	duration := int64(4)
	exporter.Record("run", "attempt", execution.Event{
		SpanID: "span", At: time.Unix(1780000000, 0), DurationMS: &duration, Status: "completed",
	})
	exporter.Close()
	select {
	case captured := <-results:
		if captured.Service != "contract-audit.embedded" || captured.Instance != "pod-9" {
			t.Fatalf("resource identity %+v", captured)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("telemetry export did not arrive")
	}

	clearTelemetryEnvironment(t)
	server, results = capture(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
	exporter, err = configuredTelemetry("worker")
	if err != nil || exporter == nil {
		t.Fatalf("configuredTelemetry: %v", err)
	}
	exporter.Record("run", "attempt", execution.Event{
		SpanID: "span", At: time.Unix(1780000000, 0), DurationMS: &duration, Status: "completed",
	})
	exporter.Close()
	select {
	case captured := <-results:
		host, _ := os.Hostname()
		if captured.Service != "contract-audit.worker" || !strings.HasPrefix(captured.Instance, host+"-") {
			t.Fatalf("resource identity %+v", captured)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("telemetry export did not arrive")
	}
}

func TestTelemetryInstanceIDPrefersThePodIdentity(t *testing.T) {
	t.Setenv("POD_UID", " pod-7 ")
	if got := InstanceID(); got != "pod-7" {
		t.Fatalf("pod identity %q", got)
	}
	t.Setenv("POD_UID", "")
	host, _ := os.Hostname()
	want := fmt.Sprintf("%s-%d", host, os.Getpid())
	if got := InstanceID(); got != want {
		t.Fatalf("instance identity %q, want %q", got, want)
	}
}

func TestConfiguredQueuePolicyAcceptsOnlyOneBoundedJSONObject(t *testing.T) {
	t.Setenv("EXECUTION_QUEUE_POLICY", "")
	policy, err := configuredQueuePolicy()
	if err != nil || !reflect.DeepEqual(policy, store.DefaultQueuePolicy()) {
		t.Fatalf("default queue policy %+v err=%v", policy, err)
	}

	t.Setenv("EXECUTION_QUEUE_POLICY", strings.Repeat("x", 32769))
	if _, err := configuredQueuePolicy(); err == nil || err.Error() != "execution queue policy is too large" {
		t.Fatalf("oversized policy accepted: %v", err)
	}

	for _, raw := range []string{"not json", `{"unknown_field":1}`, `{"global_inflight":"many"}`} {
		t.Setenv("EXECUTION_QUEUE_POLICY", raw)
		if _, err := configuredQueuePolicy(); err == nil || err.Error() != "invalid execution queue policy JSON" {
			t.Fatalf("invalid policy %q accepted: %v", raw, err)
		}
	}

	t.Setenv("EXECUTION_QUEUE_POLICY", `{"version":"drr-v1"} {}`)
	policy, err = configuredQueuePolicy()
	if err == nil || err.Error() != "execution queue policy must be one JSON object" || policy.Version != "drr-v1" {
		t.Fatalf("trailing policy %+v err=%v", policy, err)
	}

	t.Setenv("EXECUTION_QUEUE_POLICY", `{"version":"drr-v2","global_inflight":2,"tenant_inflight":1,"global_pending":10,"tenant_pending":4,"max_claims":2,"max_age_seconds":60,"backoff_base_ms":10,"backoff_max_ms":100,"weights":{"default":1}}`)
	policy, err = configuredQueuePolicy()
	if err != nil || policy.Version != "drr-v2" || policy.GlobalInFlight != 2 || policy.Weights["default"] != 1 {
		t.Fatalf("valid policy %+v err=%v", policy, err)
	}
}

var advisoryEnvironment = []string{
	"TYPESAFE_JEV_MODE", "TYPESAFE_JEV_EGRESS", "TYPESAFE_JEV_FEATURES", "TYPESAFE_JEV_PROVIDER",
	"TYPESAFE_JEV_MODEL", "TYPESAFE_JEV_TIMEOUT", "TYPESAFE_JEV_MAX_CANDIDATES", "TYPESAFE_JEV_BASE_URL", "TYPESAFE_API_KEY",
}

func clearAdvisoryEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range advisoryEnvironment {
		t.Setenv(key, "")
	}
	t.Setenv("APP_ENV", "test")
}

func providerIdentity(provider interface{ ProviderIdentity() string }) string {
	if provider == nil {
		return ""
	}
	return provider.ProviderIdentity()
}

// A wiring failure after the store opened must close that store and surface
// the configuration error instead of leaking the connection.
func TestOpenConfiguredWiringFailureClosesTheOpenedStore(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required for the bootstrap store-open scenario")
	}
	for _, key := range []string{
		"SERVICE_ROLE", "APP_DATABASE_MODE", "EXECUTION_MODE", "APP_READ_ONLY", "COOKIE_SECURE",
		"IDENTITY_DIRECTORY_FILE", "OIDC_ISSUER", "OIDC_STATE_KEY", "OIDC_CLIENT_SECRET", "OIDC_ADMIN_CLIENT_SECRET",
		"OIDC_CLIENT_ID", "OIDC_ADMIN_CLIENT_ID", "OIDC_REDIRECT_URL", "OIDC_ADMIN_REDIRECT_URL",
		"S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY", "RUSTFS_ACCESS_KEY", "RUSTFS_SECRET_KEY",
		"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_TRACES_SAMPLER", "OTEL_TRACES_SAMPLER_ARG",
		"MODEL_GATEWAY_URL", "DOCUMENT_SERVICE_URL", "WORKER_DOCUMENT_TOKEN", "CORE_DOCUMENT_TOKEN", "TOOL_GATEWAY_URL", "WORKER_TOOL_TOKEN",
		"WORKER_MODEL_TOKEN", "DOCUMENT_MODEL_TOKEN", "CORE_SERVICE_URL", "WORKER_CORE_TOKEN",
		"LLM_API_KEY", "LLM_BASE_URL", "LLM_MODEL", "OCR_MODEL", "DEV_DATABASE_URL", "PROD_DATABASE_URL",
	} {
		t.Setenv(key, "")
	}
	schema := fmt.Sprintf("it_b02_open_%d", time.Now().UnixNano())
	t.Setenv("APP_ENV", "test")
	t.Setenv("DATABASE_SCHEMA", schema)
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("EXECUTION_QUEUE_POLICY", "not json")
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pool, err := pgxpool.New(cleanup, dsn)
		if err != nil {
			t.Errorf("connect to drop isolated bootstrap schema: %v", err)
		} else {
			if _, err := pool.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
				t.Errorf("drop isolated bootstrap schema: %v", err)
			}
			pool.Close()
		}
		if err := accounts.ConfigureDirectory(""); err != nil {
			t.Error(err)
		}
	})
	runtime, err := Open(context.Background(), false)
	if err == nil || runtime != nil || !strings.Contains(err.Error(), "invalid execution queue policy JSON") {
		t.Fatalf("wiring failure surfaced: runtime=%v err=%v", runtime, err)
	}
}

func TestConfiguredAdvisoryProviderHonorsTheExecutionBoundary(t *testing.T) {
	clearAdvisoryEnvironment(t)
	if provider := configuredAdvisoryProvider("core", nil); provider != nil {
		t.Fatalf("non-worker service obtained an advisory provider: %v", provider)
	}
	clearAdvisoryEnvironment(t)
	if provider := configuredAdvisoryProvider("worker", nil); provider != nil {
		t.Fatalf("disabled advisory configuration built a provider: %v", provider)
	}
	clearAdvisoryEnvironment(t)
	t.Setenv("TYPESAFE_JEV_MODE", "sometimes")
	if provider := configuredAdvisoryProvider("worker", nil); provider != nil {
		t.Fatalf("invalid advisory mode built a provider: %v", provider)
	}

	clearAdvisoryEnvironment(t)
	t.Setenv("TYPESAFE_JEV_MODE", "fixture")
	provider := configuredAdvisoryProvider("worker", nil)
	if identity := providerIdentity(provider.(interface{ ProviderIdentity() string })); identity != "local-fixture" {
		t.Fatalf("fixture provider identity %q", identity)
	}

	clearAdvisoryEnvironment(t)
	t.Setenv("TYPESAFE_JEV_MODE", "advisory")
	t.Setenv("TYPESAFE_JEV_MODEL", "jev-1.14.0")
	models := model.New(model.Config{BaseURL: "http://127.0.0.1:3425/v1", ChatModel: "chat", OCRModel: "ocr"})
	provider = configuredAdvisoryProvider("worker", models)
	identified, ok := provider.(interface{ ProviderIdentity() string })
	if !ok || identified.ProviderIdentity() != "model-gateway" {
		t.Fatalf("gateway provider %v", provider)
	}
	if provider := configuredAdvisoryProvider("worker", nil); provider != nil {
		t.Fatalf("gateway provider without a model client: %v", provider)
	}

	clearAdvisoryEnvironment(t)
	t.Setenv("TYPESAFE_JEV_MODE", "advisory")
	t.Setenv("TYPESAFE_JEV_PROVIDER", "direct")
	t.Setenv("TYPESAFE_JEV_MODEL", "jev-1.14.0")
	t.Setenv("TYPESAFE_API_KEY", strings.Repeat("k", 32))
	provider = configuredAdvisoryProvider("", nil)
	identified, ok = provider.(interface{ ProviderIdentity() string })
	if !ok || identified.ProviderIdentity() != "direct" {
		t.Fatalf("direct provider %v", provider)
	}
	if provider := configuredAdvisoryProvider("worker", models); provider != nil {
		t.Fatalf("direct provider reached a service role: %v", provider)
	}
}
