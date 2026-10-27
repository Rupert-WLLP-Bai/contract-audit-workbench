package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"contractaudit/internal/telemetry"
)

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
	protocol := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"))
	if protocol != "" && protocol != "http/json" {
		return nil, errors.New("the configured trace exporter requires OTLP http/json")
	}
	ratio := 1.0
	switch strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER")) {
	case "", "always_on":
	case "always_off":
		return nil, nil
	case "traceidratio":
		var err error
		ratio, err = strconv.ParseFloat(strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG")), 64)
		if err != nil {
			return nil, errors.New("OTEL_TRACES_SAMPLER_ARG must be a finite ratio between zero and one")
		}
	default:
		return nil, errors.New("unsupported OTEL_TRACES_SAMPLER")
	}
	if service == "" {
		service = "embedded"
	}
	return telemetry.New(telemetry.Config{
		Endpoint: endpoint, Service: "contract-audit." + service,
		InstanceID: telemetryInstanceID(), SampleRatio: ratio,
	})
}
