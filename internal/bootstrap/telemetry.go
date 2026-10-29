package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"contractaudit/internal/telemetry"
)

// InstanceID is the stable process identity used by telemetry and structured
// logs. It is exported so service entrypoints label their logs with the same
// identity their OTLP resource carries.
func InstanceID() string { return telemetryInstanceID() }

// telemetryInstanceID separates replicas that share one logical service name.
// Kubernetes sets POD_UID (stable across container restarts in that pod); other
// deployments use the runtime hostname plus the process id.
func telemetryInstanceID() string {
	if value := strings.TrimSpace(os.Getenv("POD_UID")); value != "" {
		return value
	}
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "process"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

func configuredTelemetry(service string) (*telemetry.Exporter, error) {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint == "" {
		return nil, nil
	}
	ratio, enabled, err := telemetrySampling()
	if err != nil || !enabled {
		return nil, err
	}
	if service == "" {
		service = "embedded"
	}
	return telemetry.New(telemetry.Config{
		Endpoint: endpoint, Service: "contract-audit." + service,
		InstanceID: telemetryInstanceID(), SampleRatio: ratio,
	})
}

func telemetrySampling() (float64, bool, error) {
	protocol := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"))
	if protocol != "" && protocol != "http/json" {
		return 0, false, errors.New("the configured trace exporter requires OTLP http/json")
	}
	return telemetrySampleRatio()
}

func telemetrySampleRatio() (float64, bool, error) {
	switch strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER")) {
	case "", "always_on":
		return 1.0, true, nil
	case "always_off":
		return 0, false, nil
	case "traceidratio":
		ratio, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG")), 64)
		if err != nil {
			return 0, false, errors.New("OTEL_TRACES_SAMPLER_ARG must be a finite ratio between zero and one")
		}
		return ratio, true, nil
	default:
		return 0, false, errors.New("unsupported OTEL_TRACES_SAMPLER")
	}
}
