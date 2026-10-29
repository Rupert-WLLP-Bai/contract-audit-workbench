package config_test

import (
	"errors"
	"strings"
	"testing"

	"contractaudit/internal/config"
)

func TestDevelopmentDefaultIsNotTestOrProduction(t *testing.T) {
	sel, err := config.Select(config.Environ{})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Mode != config.ModeDevelopment {
		t.Fatalf("mode %s", sel.Mode)
	}
	if sel.Database.Name != "contract_audit_dev" {
		t.Fatalf("database %s", sel.Database.Name)
	}
	if sel.Database.Host != "127.0.0.1" || sel.Database.Port != "5432" {
		t.Fatalf("address %+v", sel.Database)
	}
	if sel.Database.Name == "contract_audit_test" || sel.Database.Name == "contract_audit_prod" {
		t.Fatalf("default selected %s", sel.Database.Name)
	}
	if sel.DSN == "" || sel.DevDSN == "" || sel.TestDSN == "" {
		t.Fatalf("dsn missing %+v", sel)
	}
	if sel.ProdDSN != "" {
		t.Fatal("production DSN must stay empty unless configured")
	}
}

func TestDatabaseIdentityCannotBeOverriddenByURLQuery(t *testing.T) {
	for _, query := range []string{
		"dbname=contract_audit_prod", "database=contract_audit_dev", "db%6Eame=contract_audit_prod",
		"host=other-host", "hostaddr=127.0.0.1", "port=5433", "service=production", "servicefile=/tmp/production",
	} {
		_, err := config.Select(config.Environ{AppEnv: "test", TestDatabaseURL: "postgres://user:secret@127.0.0.1/contract_audit_test?" + query})
		if !errors.Is(err, config.ErrDatabaseURL) {
			t.Fatalf("query %q: %v", query, err)
		}
	}
}

func TestDatabaseURLValidationDoesNotDiscloseCredentials(t *testing.T) {
	for _, dsn := range []string{
		"http://user:very-secret@localhost/test", "postgres://user:very-secret@localhost:bad/test",
		"postgres://user:very-secret@localhost/", "postgres://user:very-secret@localhost/a/b",
		"postgres://user:very-secret@localhost/test?sslmode=%zz", "postgres://user:very-secret@localhost/test#ignored",
	} {
		_, err := config.ParseDSN(dsn)
		if !errors.Is(err, config.ErrDatabaseURL) || strings.Contains(err.Error(), "very-secret") {
			t.Fatalf("unsafe validation: %v", err)
		}
	}
	for _, scheme := range []string{"postgres", "postgresql"} {
		id, err := config.ParseDSN(scheme + "://user:secret@[::1]:55432/contract_audit_test?sslmode=disable&connect_timeout=5")
		if err != nil || id.Name != "contract_audit_test" || id.Host != "::1" || id.Port != "55432" {
			t.Fatalf("valid URL: %+v %v", id, err)
		}
	}
}

func TestProductionIsNeverTheDefault(t *testing.T) {
	_, err := config.Select(config.Environ{AppEnv: "production"})
	if !errors.Is(err, config.ErrProductionUnset) {
		t.Fatalf("err %v", err)
	}
	sel, err := config.Select(config.Environ{
		AppEnv:          "production",
		ProdDatabaseURL: "postgres://contractaudit:contractaudit@127.0.0.1:5432/contract_audit_prod?sslmode=disable",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Mode != config.ModeProduction || sel.Database.Name != "contract_audit_prod" {
		t.Fatalf("%+v", sel)
	}
	if sel.DSN == sel.DevDSN || sel.DSN == sel.TestDSN {
		t.Fatal("production DSN collapsed onto another environment")
	}
}

func TestTestRefusesDevOrProductionIdentity(t *testing.T) {
	cases := []string{
		"postgres://contractaudit:contractaudit@127.0.0.1:5432/contract_audit_dev?sslmode=disable",
		"postgres://contractaudit:contractaudit@127.0.0.1:5432/contract_audit_prod?sslmode=disable",
		"postgres://contractaudit:contractaudit@10.0.0.8:5432/contract_audit_dev?sslmode=disable",
	}
	for _, dsn := range cases {
		_, err := config.Select(config.Environ{AppEnv: "test", TestDatabaseURL: dsn})
		if !errors.Is(err, config.ErrRefusedDatabase) {
			t.Fatalf("dsn %s err %v", dsn, err)
		}
	}
	sel, err := config.Select(config.Environ{AppEnv: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Database.Name != "contract_audit_test" || sel.Database.Host != "127.0.0.1" {
		t.Fatalf("%+v", sel.Database)
	}
	if sel.Database.Name == sel.DevDatabase.Name || sel.Database.Name == "contract_audit_prod" {
		t.Fatal("test database is not separate")
	}
}

func TestDatabaseIdentityURLIsCredentialFree(t *testing.T) {
	// Build the authority in pieces so the test never stores a literal secret.
	raw := "postgres://service" + "-user" + ":example" + "-token" + "@db.internal:6543/contract_audit_dev?sslmode=disable"
	sel, err := config.Select(config.Environ{DevDatabaseURL: raw})
	if err != nil {
		t.Fatal(err)
	}
	got := sel.Database.URL()
	if got != "postgres://db.internal:6543/contract_audit_dev" {
		t.Fatalf("url %q", got)
	}
	if strings.Contains(got, "service-user") || strings.Contains(got, "example-token") || strings.Contains(got, "sslmode") {
		t.Fatalf("credentials or query exposed: %q", got)
	}
}

func TestEnvironmentSelectionKeepsEachIdentityAndRefusesSharedNames(t *testing.T) {
	for _, mode := range []string{"dev", "test", "prod"} {
		t.Run(mode, func(t *testing.T) {
			env := config.Environ{AppEnv: " " + strings.ToUpper(mode) + " ",
				DevDatabaseURL: "postgres://dev/isolated_dev", TestDatabaseURL: "postgres://test/isolated_test", ProdDatabaseURL: "postgres://prod/isolated_prod"}
			selected, err := config.Select(env)
			if err != nil || selected.Database.Name != "isolated_"+mode || selected.Database.Host != mode || selected.Database.Port != "5432" {
				t.Fatalf("selected identity: %+v %v", selected.Database, err)
			}
			for _, other := range []string{"dev", "test", "prod"} {
				if mode == other {
					continue
				}
				collision := "postgres://different-host/isolated_" + other
				shared := env
				switch mode {
				case "dev":
					shared.DevDatabaseURL = collision
				case "test":
					shared.TestDatabaseURL = collision
				case "prod":
					shared.ProdDatabaseURL = collision
				}
				if _, err := config.Select(shared); !errors.Is(err, config.ErrRefusedDatabase) {
					t.Fatalf("%s shared %s name across hosts: %v", mode, other, err)
				}
			}
		})
	}
}

func TestEnvironmentSelectionValidatesUnselectedURLsWithoutSecrets(t *testing.T) {
	for _, which := range []string{"development", "test", "production"} {
		env := config.Environ{AppEnv: "test"}
		bad := "postgres://user:private-token@/database"
		switch which {
		case "development":
			env.DevDatabaseURL = bad
		case "test":
			env.TestDatabaseURL = bad
		case "production":
			env.ProdDatabaseURL = bad
		}
		if _, err := config.Select(env); !errors.Is(err, config.ErrDatabaseURL) || strings.Contains(err.Error(), "private-token") {
			t.Fatalf("unselected %s URL was accepted or disclosed: %v", which, err)
		}
	}
	if _, err := config.Select(config.Environ{AppEnv: "staging", DevDatabaseURL: "invalid"}); !errors.Is(err, config.ErrUnknownMode) {
		t.Fatalf("mode validation priority changed: %v", err)
	}
	for _, raw := range []string{"postgres://host1,host2/database", "postgres://host/database?HOST=elsewhere"} {
		if _, err := config.ParseDSN(raw); !errors.Is(err, config.ErrDatabaseURL) {
			t.Fatalf("ambiguous identity accepted: %v", err)
		}
	}
}

func TestFromProcessAndCredentialFreeIdentityDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("DEV_DATABASE_URL", "")
	t.Setenv("TEST_DATABASE_URL", "postgres://testing:6543/isolated_test")
	t.Setenv("PROD_DATABASE_URL", "")
	selection, err := config.FromProcess()
	if err != nil || selection.DSN != "postgres://testing:6543/isolated_test" || selection.Database.URL() != "postgres://testing:6543/isolated_test" {
		t.Fatalf("process environment: %+v %v", selection.Database, err)
	}
	for _, id := range []config.Identity{{}, {Host: "host"}, {Name: "db"}} {
		if id.URL() != "" {
			t.Fatal("incomplete identity produced a URL")
		}
	}
	if got := (config.Identity{Host: "::1", Name: "db"}).URL(); got != "postgres://[::1]:5432/db" {
		t.Fatalf("default authority: %s", got)
	}
}
