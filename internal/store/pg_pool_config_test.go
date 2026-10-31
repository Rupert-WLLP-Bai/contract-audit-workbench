package store

import (
	"testing"
	"time"
)

func TestPoolConfigPreservesExplicitOptions(t *testing.T) {
	t.Setenv("PGSERVICE", "")
	tests := []struct {
		name        string
		dsn         string
		fallback    int32
		max         int32
		min         int32
		minIdle     int32
		maxLifetime time.Duration
		jitter      time.Duration
		maxIdle     time.Duration
		health      time.Duration
	}{
		{
			name:     "url default",
			dsn:      "postgres://user@localhost/example?sslmode=disable",
			fallback: 4, max: 4,
			maxLifetime: time.Hour, maxIdle: 30 * time.Minute, health: time.Minute,
		},
		{
			name:     "keyword false positive",
			dsn:      "host=localhost user=user dbname=example sslmode=disable application_name='pool_max_conns=19'",
			fallback: 4, max: 4,
			maxLifetime: time.Hour, maxIdle: 30 * time.Minute, health: time.Minute,
		},
		{
			name:     "url explicit maximum and other settings",
			dsn:      "postgres://user@localhost/example?sslmode=disable&pool_max_conns=9&pool_min_conns=2&pool_min_idle_conns=3&pool_max_conn_lifetime=2h&pool_max_conn_lifetime_jitter=45s&pool_max_conn_idle_time=5m&pool_health_check_period=20s",
			fallback: 4, max: 9, min: 2, minIdle: 3,
			maxLifetime: 2 * time.Hour, jitter: 45 * time.Second, maxIdle: 5 * time.Minute, health: 20 * time.Second,
		},
		{
			name:     "keyword explicit maximum and other settings",
			dsn:      "host=localhost user=user dbname=example sslmode=disable pool_max_conns=7 pool_min_conns=1 pool_max_conn_lifetime=2h pool_max_conn_lifetime_jitter=30s pool_max_conn_idle_time=5m pool_health_check_period=20s",
			fallback: 4, max: 7, min: 1,
			maxLifetime: 2 * time.Hour, jitter: 30 * time.Second, maxIdle: 5 * time.Minute, health: 20 * time.Second,
		},
		{
			name:     "explicit minimum above default",
			dsn:      "postgres://user@localhost/example?sslmode=disable&pool_min_conns=6&pool_min_idle_conns=8",
			fallback: 4, max: 8, min: 6, minIdle: 8,
			maxLifetime: time.Hour, maxIdle: 30 * time.Minute, health: time.Minute,
		},
		{
			name:     "explicit maximum stays explicit",
			dsn:      "host=localhost user=user dbname=example sslmode=disable pool_max_conns=2 pool_min_conns=3",
			fallback: 4, max: 2, min: 3,
			maxLifetime: time.Hour, maxIdle: 30 * time.Minute, health: time.Minute,
		},
		{
			name:     "migrator default",
			dsn:      "postgres://user@localhost/example?sslmode=disable",
			fallback: 2, max: 2,
			maxLifetime: time.Hour, maxIdle: 30 * time.Minute, health: time.Minute,
		},
		{
			name:     "migrator explicit maximum",
			dsn:      "postgres://user@localhost/example?sslmode=disable&pool_max_conns=5",
			fallback: 2, max: 5,
			maxLifetime: time.Hour, maxIdle: 30 * time.Minute, health: time.Minute,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parsePoolConfig(tt.dsn, tt.fallback)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.MaxConns != tt.max || cfg.MinConns != tt.min || cfg.MinIdleConns != tt.minIdle {
				t.Fatalf("pool sizes max=%d min=%d idle=%d", cfg.MaxConns, cfg.MinConns, cfg.MinIdleConns)
			}
			if cfg.MaxConnLifetime != tt.maxLifetime || cfg.MaxConnLifetimeJitter != tt.jitter || cfg.MaxConnIdleTime != tt.maxIdle || cfg.HealthCheckPeriod != tt.health {
				t.Fatalf("pool durations lifetime=%s jitter=%s idle=%s health=%s", cfg.MaxConnLifetime, cfg.MaxConnLifetimeJitter, cfg.MaxConnIdleTime, cfg.HealthCheckPeriod)
			}
			for _, key := range []string{"pool_max_conns", "pool_min_conns", "pool_min_idle_conns", "pool_max_conn_lifetime", "pool_max_conn_lifetime_jitter", "pool_max_conn_idle_time", "pool_health_check_period"} {
				if _, ok := cfg.ConnConfig.RuntimeParams[key]; ok {
					t.Fatalf("pool option %s was passed to PostgreSQL", key)
				}
			}
		})
	}
}

func TestPoolConfigRejectsInvalidOptions(t *testing.T) {
	t.Setenv("PGSERVICE", "")
	for _, dsn := range []string{
		"postgres://user@localhost/example?sslmode=disable&pool_max_conns=0",
		"host=localhost user=user dbname=example sslmode=disable pool_max_conns=invalid",
		"postgres://user@localhost/example?sslmode=disable&pool_max_conn_lifetime=invalid",
	} {
		if _, err := parsePoolConfig(dsn, 4); err == nil {
			t.Fatal("invalid pool configuration was accepted")
		}
	}
}

func TestRuntimeTimeoutDefaultsWithParsedDSNs(t *testing.T) {
	t.Setenv("PGSERVICE", "")
	for _, tt := range []struct {
		dsn     string
		options string
	}{
		{
			dsn:     "postgres://user@localhost/example?sslmode=disable&statement_timeout=0&options=-c%20lock_timeout%3D750ms",
			options: "-c lock_timeout=10s -c idle_in_transaction_session_timeout=5min -c lock_timeout=750ms",
		},
		{
			dsn:     "host=localhost user=user dbname=example sslmode=disable statement_timeout=0 options='-c lock_timeout=750ms'",
			options: "-c lock_timeout=10s -c idle_in_transaction_session_timeout=5min -c lock_timeout=750ms",
		},
	} {
		cfg, err := parsePoolConfig(tt.dsn, 4)
		if err != nil {
			t.Fatal(err)
		}
		applyRuntimeTimeoutDefaults(cfg.ConnConfig.RuntimeParams)
		if cfg.ConnConfig.RuntimeParams["statement_timeout"] != "0" || cfg.ConnConfig.RuntimeParams["options"] != tt.options {
			t.Fatalf("explicit DSN settings were changed: %v", cfg.ConnConfig.RuntimeParams)
		}
	}
}

func TestRuntimeTimeoutDefaultsPreserveExplicitSettings(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]string
		options string
	}{
		{
			name:    "defaults",
			params:  map[string]string{},
			options: "-c statement_timeout=60s -c lock_timeout=10s -c idle_in_transaction_session_timeout=5min",
		},
		{
			name:    "direct settings and explicit zero",
			params:  map[string]string{"statement_timeout": "0", "lock_timeout": "3s"},
			options: "-c idle_in_transaction_session_timeout=5min",
		},
		{
			name:    "options override defaults",
			params:  map[string]string{"options": "-c statement_timeout=0 --lock_timeout=250ms"},
			options: "-c statement_timeout=60s -c lock_timeout=10s -c idle_in_transaction_session_timeout=5min -c statement_timeout=0 --lock_timeout=250ms",
		},
		{
			name:    "direct plus options",
			params:  map[string]string{"statement_timeout": "30s", "options": "-c lock_timeout=500ms"},
			options: "-c lock_timeout=10s -c idle_in_transaction_session_timeout=5min -c lock_timeout=500ms",
		},
		{
			name:    "all direct settings",
			params:  map[string]string{"statement_timeout": "0", "lock_timeout": "0", "idle_in_transaction_session_timeout": "0", "options": "-c application_name=keep"},
			options: "-c application_name=keep",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := make(map[string]string, len(tt.params))
			for key, value := range tt.params {
				original[key] = value
			}
			applyRuntimeTimeoutDefaults(tt.params)
			if tt.params["options"] != tt.options {
				t.Fatalf("options = %q, want %q", tt.params["options"], tt.options)
			}
			for key, value := range original {
				if key != "options" && tt.params[key] != value {
					t.Fatalf("%s changed from %q to %q", key, value, tt.params[key])
				}
			}
		})
	}
}
