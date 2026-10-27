package systemhealth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestEmbeddedDatabaseTargetShowsConfiguredURL(t *testing.T) {
	target := "postgres://db.internal:6543/contract_audit_dev"
	monitor := New(Config{DatabaseReady: func(context.Context) error { return nil }, DatabaseTarget: target})
	snapshot := monitor.Snapshot(context.Background())
	for _, service := range snapshot.Services {
		if service.ID != "db" {
			continue
		}
		if service.Target != target {
			t.Fatalf("database target %q", service.Target)
		}
		if service.Status != "ready" {
			t.Fatalf("database probe %q", service.Status)
		}
		return
	}
	t.Fatal("database service missing from snapshot")
}

func TestDockerInspectionIsBoundedAndKeepsPartialResults(t *testing.T) {
	const total = 16
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			items := make([]any, 0, total)
			for i := 0; i < total; i++ {
				id := "svc-" + strconv.Itoa(i)
				items = append(items, map[string]any{
					"Id": id + "-id", "Names": []string{"/ours-" + id + "-1"}, "Image": "image:" + id,
					"Labels": map[string]string{"com.docker.compose.project": "ours", "com.docker.compose.service": id},
				})
			}
			_ = json.NewEncoder(w).Encode(items)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json") {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/containers/"), "/json")
			delay := 200 * time.Millisecond
			if id == "svc-0-id" {
				delay = 2 * time.Second
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(delay):
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"State": map[string]any{"Status": "running", "Health": map[string]any{"Status": "healthy"}},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	started := time.Now()
	snapshot := New(Config{Project: "ours", DockerURL: server.URL, Client: server.Client()}).Snapshot(context.Background())
	elapsed := time.Since(started)
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("bounded inspection took %s; serialized inspects would exceed the budget", elapsed)
	}
	if snapshot.Source != "docker_partial" {
		t.Fatalf("partial inspection failure was not reported: %s", snapshot.Source)
	}
	byID := map[string]Service{}
	for _, service := range snapshot.Services {
		byID[service.LogicalID] = service
	}
	if len(byID) < total {
		t.Fatalf("missing instance observations: %d", len(byID))
	}
	slow := byID["svc-0"]
	if slow.Status != "unknown" || !strings.Contains(slow.Detail, "失败或超时") || slow.InstanceID != "svc-0-id" {
		t.Fatalf("slow probe result was lost instead of kept as partial: %+v", slow)
	}
	for i := 1; i < total; i++ {
		service := byID["svc-"+strconv.Itoa(i)]
		if service.Status != "ready" || service.Container == nil || service.InstanceID == "" || service.Workload == "" {
			t.Fatalf("fast probe %d was not preserved: %+v", i, service)
		}
	}
}
