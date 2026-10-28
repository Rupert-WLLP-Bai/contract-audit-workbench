package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionStartupRejectsDemoAccountsBeforeOpeningStorage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "must-not-be-created")
	t.Setenv("APP_ENV", "production")
	t.Setenv("SERVICE_ROLE", "core")
	t.Setenv("APP_DATABASE_MODE", "application")
	t.Setenv("EXECUTION_MODE", "durable")
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("CORE_DATABASE_URL", "postgres://user:secret@127.0.0.1:1/contract_audit_prod?sslmode=disable")
	t.Setenv("DEV_DATABASE_URL", "")
	t.Setenv("TEST_DATABASE_URL", "")
	t.Setenv("DATA_DIR", dir)
	err := run()
	if err == nil || !strings.Contains(err.Error(), "demo accounts") {
		t.Fatalf("production did not stop at identity boundary: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("storage was opened before identity validation: %v", err)
	}
}

func TestProductionRequiresExplicitServiceRole(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "must-not-be-created")
	t.Setenv("APP_ENV", "production")
	t.Setenv("SERVICE_ROLE", "")
	t.Setenv("DATA_DIR", dir)
	err := run()
	if err == nil || !strings.Contains(err.Error(), "SERVICE_ROLE") {
		t.Fatalf("production without SERVICE_ROLE was not refused: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("refused startup opened storage: %v", err)
	}
}

func TestUnknownServiceRoleIsRefused(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("SERVICE_ROLE", "document")
	err := run()
	if err == nil || !strings.Contains(err.Error(), "SERVICE_ROLE") {
		t.Fatalf("unknown service role was not refused: %v", err)
	}
}

func TestServerEnvironmentDefaultIsLoopback(t *testing.T) {
	t.Setenv("ADDR", "")
	if addr := env("ADDR", "127.0.0.1:8080"); addr != "127.0.0.1:8080" {
		t.Fatalf("default address %q", addr)
	}
	t.Setenv("ADDR", " :8080 ")
	if addr := env("ADDR", "127.0.0.1:8080"); addr != ":8080" {
		t.Fatalf("explicit address %q", addr)
	}
}

func TestInvalidReadOnlySettingStopsBeforeOpeningStorage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "must-not-be-created")
	t.Setenv("APP_ENV", "test")
	t.Setenv("SERVICE_ROLE", "")
	t.Setenv("TEST_DATABASE_URL", "postgres://user:secret@127.0.0.1:1/contract_audit_test?sslmode=disable")
	t.Setenv("DEV_DATABASE_URL", "")
	t.Setenv("PROD_DATABASE_URL", "")
	t.Setenv("DATA_DIR", dir)
	t.Setenv("APP_READ_ONLY", "enabled")
	if err := run(); err == nil || !strings.Contains(err.Error(), "APP_READ_ONLY") {
		t.Fatalf("invalid read-only setting was not refused before storage: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("invalid rollback setting opened storage: %v", err)
	}
}
