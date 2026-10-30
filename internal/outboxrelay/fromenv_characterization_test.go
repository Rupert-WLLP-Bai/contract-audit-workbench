package outboxrelay

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

var relayEnvironment = []string{
	"NATS_URL", "OUTBOX_RELAY_OWNER", "OUTBOX_RELAY_BATCH", "NATS_STREAM_REPLICAS",
	"OUTBOX_RELAY_LEASE", "OUTBOX_RELAY_POLL_INTERVAL", "OUTBOX_RELAY_PUBLISH_TIMEOUT", "OUTBOX_RELAY_MAX_BACKOFF",
}

func clearRelayEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range relayEnvironment {
		t.Setenv(key, "")
	}
}

func TestRelayFromEnvDefaultsStayDisabledWithoutABroker(t *testing.T) {
	clearRelayEnvironment(t)
	config, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if config.Enabled() || config.URL != "" {
		t.Fatalf("unset NATS_URL invented a broker: %+v", config)
	}
	if config.Batch != defaultBatch || config.Lease != defaultLease || config.Poll != defaultPoll ||
		config.PublishTimeout != defaultPublishTimeout || config.MaxBackoff != defaultMaxBackoff {
		t.Fatalf("defaults changed: %+v", config)
	}
	if config.Subject != Subject || config.Stream != Stream || config.Replicas != 1 {
		t.Fatalf("stream identity changed: %+v", config)
	}
	host, _ := os.Hostname()
	if config.Owner == "" || !strings.HasPrefix(config.Owner, host+"-") || !strings.HasSuffix(config.Owner, fmt.Sprintf("-%d", os.Getpid())) {
		t.Fatalf("owner fallback %q", config.Owner)
	}
}

func TestRelayFromEnvAcceptsEveryBoundedOverride(t *testing.T) {
	clearRelayEnvironment(t)
	t.Setenv("NATS_URL", " nats://127.0.0.1:4222 ")
	t.Setenv("OUTBOX_RELAY_OWNER", " relay-7 ")
	t.Setenv("OUTBOX_RELAY_BATCH", "512")
	t.Setenv("NATS_STREAM_REPLICAS", "5")
	t.Setenv("OUTBOX_RELAY_LEASE", "5m")
	t.Setenv("OUTBOX_RELAY_POLL_INTERVAL", "1m")
	t.Setenv("OUTBOX_RELAY_PUBLISH_TIMEOUT", "1m")
	t.Setenv("OUTBOX_RELAY_MAX_BACKOFF", "10m")
	config, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if !config.Enabled() || config.URL != "nats://127.0.0.1:4222" || config.Owner != "relay-7" || config.Batch != 512 ||
		config.Replicas != 5 || config.Lease != 5*time.Minute || config.Poll != time.Minute ||
		config.PublishTimeout != time.Minute || config.MaxBackoff != 10*time.Minute {
		t.Fatalf("overrides lost: %+v", config)
	}

	clearRelayEnvironment(t)
	t.Setenv("NATS_URL", "nats://127.0.0.1:4222")
	t.Setenv("OUTBOX_RELAY_LEASE", "501ms")
	t.Setenv("OUTBOX_RELAY_POLL_INTERVAL", "1ms")
	t.Setenv("OUTBOX_RELAY_PUBLISH_TIMEOUT", "1ms")
	t.Setenv("OUTBOX_RELAY_MAX_BACKOFF", "1ms")
	if _, err := FromEnv(); err != nil {
		t.Fatalf("minimum bounds rejected: %v", err)
	}
}

func TestRelayFromEnvRejectsOutOfRangeValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{"batch not an integer", "OUTBOX_RELAY_BATCH", "many", "OUTBOX_RELAY_BATCH must be an integer in 1..512"},
		{"batch zero", "OUTBOX_RELAY_BATCH", "0", "OUTBOX_RELAY_BATCH must be an integer in 1..512"},
		{"batch above maximum", "OUTBOX_RELAY_BATCH", "513", "OUTBOX_RELAY_BATCH must be an integer in 1..512"},
		{"replicas not an integer", "NATS_STREAM_REPLICAS", "many", "NATS_STREAM_REPLICAS must be an integer in 1..5"},
		{"replicas zero", "NATS_STREAM_REPLICAS", "0", "NATS_STREAM_REPLICAS must be an integer in 1..5"},
		{"replicas above maximum", "NATS_STREAM_REPLICAS", "6", "NATS_STREAM_REPLICAS must be an integer in 1..5"},
		{"lease below minimum", "OUTBOX_RELAY_LEASE", "500ms", "OUTBOX_RELAY_LEASE must be a duration in [501ms, 5m0s]"},
		{"lease above maximum", "OUTBOX_RELAY_LEASE", "5m1s", "OUTBOX_RELAY_LEASE must be a duration in [501ms, 5m0s]"},
		{"lease not a duration", "OUTBOX_RELAY_LEASE", "soon", "OUTBOX_RELAY_LEASE must be a duration in [501ms, 5m0s]"},
		{"poll below minimum", "OUTBOX_RELAY_POLL_INTERVAL", "0s", "OUTBOX_RELAY_POLL_INTERVAL must be a duration in [1ms, 1m0s]"},
		{"poll above maximum", "OUTBOX_RELAY_POLL_INTERVAL", "1m1s", "OUTBOX_RELAY_POLL_INTERVAL must be a duration in [1ms, 1m0s]"},
		{"poll not a duration", "OUTBOX_RELAY_POLL_INTERVAL", "fast", "OUTBOX_RELAY_POLL_INTERVAL must be a duration in [1ms, 1m0s]"},
		{"publish below minimum", "OUTBOX_RELAY_PUBLISH_TIMEOUT", "0s", "OUTBOX_RELAY_PUBLISH_TIMEOUT must be a duration in [1ms, 1m0s]"},
		{"publish above maximum", "OUTBOX_RELAY_PUBLISH_TIMEOUT", "1m1s", "OUTBOX_RELAY_PUBLISH_TIMEOUT must be a duration in [1ms, 1m0s]"},
		{"publish not a duration", "OUTBOX_RELAY_PUBLISH_TIMEOUT", "slow", "OUTBOX_RELAY_PUBLISH_TIMEOUT must be a duration in [1ms, 1m0s]"},
		{"backoff below minimum", "OUTBOX_RELAY_MAX_BACKOFF", "0s", "OUTBOX_RELAY_MAX_BACKOFF must be a duration in [1ms, 10m0s]"},
		{"backoff above maximum", "OUTBOX_RELAY_MAX_BACKOFF", "10m1s", "OUTBOX_RELAY_MAX_BACKOFF must be a duration in [1ms, 10m0s]"},
		{"backoff not a duration", "OUTBOX_RELAY_MAX_BACKOFF", "later", "OUTBOX_RELAY_MAX_BACKOFF must be a duration in [1ms, 10m0s]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearRelayEnvironment(t)
			t.Setenv("NATS_URL", "nats://127.0.0.1:4222")
			t.Setenv(test.key, test.value)
			config, err := FromEnv()
			if err == nil || config != (Config{}) {
				t.Fatalf("out-of-range value accepted: %+v", config)
			}
			if err.Error() != test.want {
				t.Fatalf("error %q, want %q", err.Error(), test.want)
			}
		})
	}
}

func TestRelayFromEnvRequiresAPublishWindowInsideTheLease(t *testing.T) {
	clearRelayEnvironment(t)
	t.Setenv("NATS_URL", "nats://127.0.0.1:4222")
	t.Setenv("OUTBOX_RELAY_LEASE", "30s")
	for _, publish := range []string{"30s", "29.501s"} {
		t.Setenv("OUTBOX_RELAY_PUBLISH_TIMEOUT", publish)
		if _, err := FromEnv(); err == nil || err.Error() != "OUTBOX_RELAY_PUBLISH_TIMEOUT must leave a publish window inside OUTBOX_RELAY_LEASE" {
			t.Fatalf("%s accepted: %v", publish, err)
		}
	}
	t.Setenv("OUTBOX_RELAY_PUBLISH_TIMEOUT", "29.5s")
	if _, err := FromEnv(); err != nil {
		t.Fatalf("boundary publish window rejected: %v", err)
	}
}

func TestRelayNewRejectsIncompleteConfiguration(t *testing.T) {
	valid := testConfig()
	tests := []struct {
		name   string
		store  Store
		broker Broker
		config func(*Config)
		want   string
	}{
		{name: "missing store", store: nil, broker: &fakeBroker{}, config: func(*Config) {}, want: "outbox relay requires a store and a broker"},
		{name: "missing broker", store: &fakeStore{}, broker: nil, config: func(*Config) {}, want: "outbox relay requires a store and a broker"},
		{name: "disabled", store: &fakeStore{}, broker: &fakeBroker{}, config: func(c *Config) { c.URL = "" }, want: "outbox relay requires a broker URL"},
		{name: "batch", store: &fakeStore{}, broker: &fakeBroker{}, config: func(c *Config) { c.Batch = 0 }, want: "outbox relay configuration is incomplete or invalid"},
		{name: "lease at the margin", store: &fakeStore{}, broker: &fakeBroker{}, config: func(c *Config) { c.Lease = leaseSafetyMargin }, want: "outbox relay configuration is incomplete or invalid"},
		{name: "lease below the margin", store: &fakeStore{}, broker: &fakeBroker{}, config: func(c *Config) { c.Lease = leaseSafetyMargin - time.Millisecond }, want: "outbox relay configuration is incomplete or invalid"},
		{name: "poll", store: &fakeStore{}, broker: &fakeBroker{}, config: func(c *Config) { c.Poll = 0 }, want: "outbox relay configuration is incomplete or invalid"},
		{name: "publish timeout", store: &fakeStore{}, broker: &fakeBroker{}, config: func(c *Config) { c.PublishTimeout = 0 }, want: "outbox relay configuration is incomplete or invalid"},
		{name: "max backoff", store: &fakeStore{}, broker: &fakeBroker{}, config: func(c *Config) { c.MaxBackoff = 0 }, want: "outbox relay configuration is incomplete or invalid"},
		{name: "publish timeout does not fit the lease", store: &fakeStore{}, broker: &fakeBroker{}, config: func(c *Config) { c.PublishTimeout = c.Lease - leaseSafetyMargin + time.Nanosecond }, want: "outbox relay configuration is incomplete or invalid"},
		{name: "subject", store: &fakeStore{}, broker: &fakeBroker{}, config: func(c *Config) { c.Subject = "" }, want: "outbox relay configuration is incomplete or invalid"},
		{name: "no replicas", store: &fakeStore{}, broker: &fakeBroker{}, config: func(c *Config) { c.Replicas = 0 }, want: "outbox relay configuration is incomplete or invalid"},
		{name: "too many replicas", store: &fakeStore{}, broker: &fakeBroker{}, config: func(c *Config) { c.Replicas = 6 }, want: "outbox relay configuration is incomplete or invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.config(&config)
			relay, err := New(test.store, test.broker, config)
			if err == nil || relay != nil {
				t.Fatalf("invalid relay configuration accepted: %+v", relay)
			}
			if err.Error() != test.want {
				t.Fatalf("error %q, want %q", err.Error(), test.want)
			}
		})
	}
	relay, err := New(&fakeStore{}, &fakeBroker{}, valid)
	if err != nil || relay == nil {
		t.Fatalf("valid configuration rejected: %v", err)
	}
	config := valid
	config.PublishTimeout = config.Lease - leaseSafetyMargin
	if _, err := New(&fakeStore{}, &fakeBroker{}, config); err != nil {
		t.Fatalf("boundary publish timeout rejected: %v", err)
	}
}
