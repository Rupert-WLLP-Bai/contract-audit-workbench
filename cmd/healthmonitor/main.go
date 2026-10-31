package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"contractaudit/internal/process"
	"contractaudit/internal/bootstrap"
	"contractaudit/internal/systemhealth"
	"contractaudit/internal/telemetry"
)

// telemetryExporter builds the OTLP exporter for this monitor's HTTP boundary.
// It is nil, and the boundary still logs and recovers, when no collector is
// configured.
func telemetryExporter() (*telemetry.Exporter, error) {
	return bootstrap.HTTPExporter("healthmonitor")
}

func main() {
	kubernetes := systemhealth.KubernetesConfigFromEnv()
	project := strings.TrimSpace(os.Getenv("SYSTEM_HEALTH_PROJECT"))
	expected, err := systemhealth.ExpectedReplicasFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	config := systemhealth.Config{Kubernetes: kubernetes, DatabaseTarget: strings.TrimSpace(os.Getenv("SYSTEM_HEALTH_DATABASE_URL")), ModelName: os.Getenv("LLM_MODEL"), ModelEndpoint: os.Getenv("LLM_BASE_URL"), OCRName: os.Getenv("OCR_MODEL"), MinerUURL: os.Getenv("MINERU_URL"), Jev: systemhealth.JevFromEnv(), ExpectedReplicas: expected}
	if kubernetes == nil {
		if project == "" {
			log.Fatal("SYSTEM_HEALTH_PROJECT must select one Compose project outside Kubernetes")
		}
		socket := strings.TrimSpace(os.Getenv("DOCKER_SOCKET"))
		if socket == "" {
			socket = "/var/run/docker.sock"
		}
		config.Project = project
		config.DockerURL = "http://docker"
		config.Client = systemhealth.DockerClient(socket)
	}
	monitor := systemhealth.New(config)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /snapshot", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(monitor.Snapshot(r.Context()))
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	exporter, err := telemetryExporter()
	if err != nil {
		log.Fatal(err)
	}
	defer exporter.Close()
	ctx, stop := process.SignalContext()
	defer stop()
	server := &http.Server{Addr: ":8092", Handler: telemetry.HTTPBoundary(exporter, "healthmonitor", telemetry.HTTPOptions{}, mux), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 8 * time.Second, IdleTimeout: 30 * time.Second}
	if err := process.Serve(ctx, server, 5*time.Second); err != nil {
		log.Fatal(err)
	}
}
