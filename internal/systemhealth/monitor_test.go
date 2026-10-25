package systemhealth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInventoryUsesProjectScopeAndDistinguishesContainerStates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected Docker write: %s", r.Method)
		}
		if r.URL.Path == "/containers/json" {
			if !strings.Contains(r.URL.Query().Get("filters"), "com.docker.compose.project=ours") || r.URL.Query().Get("all") != "1" {
				t.Errorf("unscoped inventory: %s", r.URL)
			}
			json.NewEncoder(w).Encode([]any{
				map[string]any{"Id": "db-id", "Names": []string{"/ours-db-1"}, "Image": "postgres:18", "Labels": map[string]string{"com.docker.compose.project": "ours", "com.docker.compose.service": "db"}},
				map[string]any{"Id": "nats-id", "Names": []string{"/ours-nats-1"}, "Image": "nats:2", "Labels": map[string]string{"com.docker.compose.project": "ours", "com.docker.compose.service": "nats"}},
				map[string]any{"Id": "extra-id", "Names": []string{"/ours-search-1"}, "Image": "search:1", "Labels": map[string]string{"com.docker.compose.project": "ours", "com.docker.compose.service": "search"}},
				map[string]any{"Id": "foreign-id", "Names": []string{"/other-secret"}, "Labels": map[string]string{"com.docker.compose.project": "other", "com.docker.compose.service": "db"}},
			})
			return
		}
		switch r.URL.Path {
		case "/containers/db-id/json":
			json.NewEncoder(w).Encode(map[string]any{"State": map[string]any{"Status": "running", "StartedAt": "2026-10-25T10:00:00Z", "Health": map[string]any{"Status": "healthy"}}, "RestartCount": 2, "Config": map[string]any{"Env": []string{"PASSWORD=redaction-canary-docker-env"}}})
		case "/containers/nats-id/json":
			json.NewEncoder(w).Encode(map[string]any{"State": map[string]any{"Status": "exited", "ExitCode": 1, "Health": map[string]any{"Status": "unhealthy"}}})
		case "/containers/extra-id/json":
			json.NewEncoder(w).Encode(map[string]any{"State": map[string]any{"Status": "running"}})
		default:
			t.Errorf("unexpected Docker request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	snapshot := New(Config{Project: "ours", DockerURL: server.URL, Client: server.Client()}).Snapshot(context.Background())
	byID := map[string]Service{}
	for _, service := range snapshot.Services {
		byID[service.ID] = service
	}
	if byID["db"].Status != "ready" || byID["db"].Container.Restarts != 2 || byID["nats"].Status != "stopped" {
		t.Fatalf("container health lost: %+v", snapshot.Services)
	}
	architecture := map[string]ArchitectureEntry{}
	for _, entry := range snapshot.Architecture {
		architecture[entry.ID] = entry
	}
	if architecture["postgres-ha"].State != "observed" || !strings.Contains(architecture["postgres-ha"].Observed, "1 个") || !strings.Contains(architecture["postgres-ha"].Observed, "无法证明") {
		t.Fatalf("PostgreSQL inventory overclaimed HA or lost its count: %+v", architecture["postgres-ha"])
	}
	if architecture["kubernetes"].State != "not_connected" || !strings.Contains(architecture["kubernetes"].Observed, "没有 Kubernetes") || strings.Contains(architecture["kubernetes"].Observed, "kubectl") {
		t.Fatalf("missing Kubernetes source treated as connected or fabricated as a kubectl observation: %+v", architecture["kubernetes"])
	}
	if architecture["model-waypoint"].State != "not_connected" || len(architecture["model-waypoint"].ServiceIDs) != 1 || architecture["model-waypoint"].ServiceIDs[0] != "model-waypoint" {
		t.Fatalf("model gateway incorrectly counted as a waypoint: %+v", architecture["model-waypoint"])
	}
	if architecture["tool-waypoint"].State != "not_connected" || len(architecture["tool-waypoint"].ServiceIDs) != 1 || architecture["tool-waypoint"].ServiceIDs[0] != "tool-waypoint" {
		t.Fatalf("tool gateway incorrectly counted as a waypoint: %+v", architecture["tool-waypoint"])
	}
	if byID["search"].Status != "unknown" || byID["search"].Container.Name != "ours-search-1" {
		t.Fatalf("unlisted service hidden or asserted healthy without probe: %+v", byID["search"])
	}
	if byID["keycloak"].Status != "unconfigured" || byID["core-api"].Status != "unavailable" {
		t.Fatalf("missing required and optional containers conflated: %+v", byID)
	}
	raw, _ := json.Marshal(snapshot)
	if strings.Contains(string(raw), "redaction-canary-docker-env") || strings.Contains(string(raw), "other-secret") {
		t.Fatalf("private data leaked: %s", raw)
	}
}

func TestGatewayServicesDoNotImplyWaypointPresence(t *testing.T) {
	services := []Service{
		{ID: "model-gateway", Name: "模型网关", Status: "ready", Container: &Container{ID: "model"}},
		{ID: "tool-gateway", Name: "工具网关", Status: "ready", Container: &Container{ID: "tool"}},
	}
	entries := architectureInventory(services, "docker", nil)
	byID := make(map[string]ArchitectureEntry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	for _, item := range []struct{ id, serviceID string }{{"model-waypoint", "model-waypoint"}, {"tool-waypoint", "tool-waypoint"}} {
		entry := byID[item.id]
		if entry.State != "not_connected" || len(entry.ServiceIDs) != 1 || entry.ServiceIDs[0] != item.serviceID {
			t.Errorf("gateway service incorrectly established waypoint observation: %+v", entry)
		}
	}
}

func TestUnavailableDockerDoesNotFabricateReadiness(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	snapshot := New(Config{Project: "ours", DockerURL: server.URL, Client: server.Client()}).Snapshot(context.Background())
	for _, service := range snapshot.Services {
		if service.Status == "ready" {
			t.Fatalf("unobserved service marked ready: %+v", service)
		}
	}
	if snapshot.Source != "docker_unavailable" {
		t.Fatalf("missing observation failure: %+v", snapshot)
	}
	architecture := map[string]ArchitectureEntry{}
	for _, entry := range snapshot.Architecture {
		architecture[entry.ID] = entry
	}
	if architecture["postgres-ha"].State != "not_connected" || !strings.Contains(architecture["postgres-ha"].Observed, "Docker") {
		t.Fatalf("Docker observation failure omitted from architecture inventory: %+v", architecture["postgres-ha"])
	}
}
