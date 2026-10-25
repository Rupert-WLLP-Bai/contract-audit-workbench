package bootstrap

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"contractaudit/internal/accounts"
)

// A closed-on-accept TCP peer records database contact without reaching a real
// database. The control case proves that the remaining startup settings are valid.
func bootstrapStartupEnvironment(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	for _, key := range []string{
		"SERVICE_ROLE", "APP_ENV", "APP_DATABASE_MODE", "EXECUTION_MODE", "APP_READ_ONLY", "COOKIE_SECURE",
		"IDENTITY_DIRECTORY_FILE", "OIDC_ISSUER", "OIDC_STATE_KEY", "OIDC_CLIENT_SECRET", "OIDC_ADMIN_CLIENT_SECRET",
		"OIDC_CLIENT_ID", "OIDC_ADMIN_CLIENT_ID", "OIDC_REDIRECT_URL", "OIDC_ADMIN_REDIRECT_URL",
		"S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY", "RUSTFS_ACCESS_KEY", "RUSTFS_SECRET_KEY",
		"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_TRACES_SAMPLER", "OTEL_TRACES_SAMPLER_ARG",
		"EXECUTION_QUEUE_POLICY", "MODEL_GATEWAY_URL", "DOCUMENT_SERVICE_URL", "WORKER_DOCUMENT_TOKEN",
		"TOOL_GATEWAY_URL", "WORKER_TOOL_TOKEN", "WORKER_MODEL_TOKEN", "DOCUMENT_MODEL_TOKEN",
		"LLM_API_KEY", "LLM_BASE_URL", "LLM_MODEL", "OCR_MODEL", "DATA_DIR", "DATABASE_SCHEMA",
		"DEV_DATABASE_URL", "TEST_DATABASE_URL", "PROD_DATABASE_URL", "CORE_DATABASE_URL", "DOCUMENT_DATABASE_URL",
		"EXECUTION_DATABASE_URL", "MODEL_DATABASE_URL", "TOOL_DATABASE_URL",
	} {
		t.Setenv(key, "")
	}
	t.Cleanup(func() {
		if err := accounts.ConfigureDirectory(""); err != nil {
			t.Error(err)
		}
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var contacted atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			contacted.Add(1)
			_ = connection.Close()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	dsn := "postgres://unit:unit@" + listener.Addr().String() + "/contract_audit_bootstrap_unit?sslmode=disable"
	t.Setenv("APP_ENV", "test")
	t.Setenv("DATA_DIR", t.TempDir())
	for _, key := range []string{"CORE_DATABASE_URL", "DOCUMENT_DATABASE_URL", "EXECUTION_DATABASE_URL", "MODEL_DATABASE_URL", "TOOL_DATABASE_URL"} {
		t.Setenv(key, dsn)
	}
	return dsn, &contacted
}

func TestOpenServiceValidConfigurationReachesOnlyTheIsolatedDatabasePeer(t *testing.T) {
	_, contacted := bootstrapStartupEnvironment(t)
	runtime, err := OpenService(context.Background(), "core")
	if err == nil || runtime != nil || contacted.Load() == 0 {
		t.Fatalf("startup bypassed the isolated database prerequisite: runtime=%v err=%v contacts=%d", runtime != nil, err, contacted.Load())
	}
}

func TestOpenServiceRejectsInvalidConfigurationBeforeDatabaseContact(t *testing.T) {
	for _, test := range []struct {
		name, role string
		configure  func(*testing.T, string)
	}{
		{"unknown identity", "mystery", func(*testing.T, string) {}},
		{"executable identity mismatch", "core", func(t *testing.T, _ string) { t.Setenv("SERVICE_ROLE", "worker") }},
		{"missing service database", "core", func(t *testing.T, _ string) { t.Setenv("CORE_DATABASE_URL", " ") }},
		{"reserved environment database", "core", func(t *testing.T, dsn string) {
			t.Setenv("APP_ENV", "development")
			t.Setenv("CORE_DATABASE_URL", strings.Replace(dsn, "contract_audit_bootstrap_unit", "contract_audit_test", 1))
		}},
		{"unknown environment", "core", func(t *testing.T, _ string) { t.Setenv("APP_ENV", "staging") }},
		{"insecure production configuration", "core", func(t *testing.T, dsn string) {
			t.Setenv("APP_ENV", "production")
			t.Setenv("CORE_DATABASE_URL", strings.Replace(dsn, "contract_audit_bootstrap_unit", "contract_audit_bootstrap_prod", 1))
		}},
		{"invalid read-only setting", "core", func(t *testing.T, _ string) { t.Setenv("APP_READ_ONLY", "yes") }},
		{"invalid database mode", "core", func(t *testing.T, _ string) { t.Setenv("APP_DATABASE_MODE", "embedded-file") }},
		{"invalid execution mode", "execution", func(t *testing.T, _ string) { t.Setenv("EXECUTION_MODE", "volatile") }},
		{"missing enterprise directory", "core", func(t *testing.T, _ string) { t.Setenv("IDENTITY_DIRECTORY_FILE", t.TempDir()+"/missing.json") }},
		{"incomplete OIDC configuration", "core", func(t *testing.T, _ string) { t.Setenv("OIDC_ISSUER", "https://identity.example") }},
		{"incomplete object store configuration", "document", func(t *testing.T, _ string) { t.Setenv("S3_ENDPOINT", "https://objects.example") }},
		{"worker missing model gateway", "worker", func(*testing.T, string) {}},
		{"document missing model gateway", "document", func(*testing.T, string) {}},
		{"worker missing model credential", "worker", func(t *testing.T, _ string) { t.Setenv("MODEL_GATEWAY_URL", "http://model-gateway.invalid") }},
		{"document missing model credential", "document", func(t *testing.T, _ string) { t.Setenv("MODEL_GATEWAY_URL", "http://model-gateway.invalid") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			dsn, contacted := bootstrapStartupEnvironment(t)
			test.configure(t, dsn)
			runtime, err := OpenService(context.Background(), test.role)
			if err == nil || runtime != nil || contacted.Load() != 0 {
				t.Fatalf("invalid configuration reached startup dependencies: runtime=%v err=%v database_contacts=%d", runtime != nil, err, contacted.Load())
			}
		})
	}
}

func TestOpenServiceCanceledStartupPreservesCancellationBeforeDatabaseContact(t *testing.T) {
	for _, role := range []string{"core", "document", "execution", "worker", "model", "tool"} {
		t.Run(role, func(t *testing.T) {
			_, contacted := bootstrapStartupEnvironment(t)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			runtime, err := OpenService(ctx, role)
			if runtime != nil || !errors.Is(err, context.Canceled) || contacted.Load() != 0 {
				t.Fatalf("canceled startup lost its cause or contacted the database: runtime=%v err=%v database_contacts=%d", runtime != nil, err, contacted.Load())
			}
		})
	}
}
