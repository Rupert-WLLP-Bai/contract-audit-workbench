package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"contractaudit/internal/systemhealth"
)

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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Addr: ":8092", Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 8 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
