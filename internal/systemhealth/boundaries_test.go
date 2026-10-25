package systemhealth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalIntegrationsKeepConfigurationDistinctFromReachability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			t.Errorf("unexpected inference or model request %s", r.URL)
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()
	for _, failure := range []bool{false, true} {
		monitor := New(Config{DatabaseReady: func(context.Context) error {
			if failure {
				return errors.New("postgres://monitor-user:redaction-canary-7a62@db")
			}
			return nil
		}, ModelName: "configured-model", ModelEndpoint: "https://monitor-user:redaction-canary-7a62@models.example/v1?api_key=redaction-canary-7a62", MinerUURL: server.URL})
		snapshot := monitor.Snapshot(context.Background())
		byID := map[string]Service{}
		for _, service := range snapshot.Services {
			byID[service.ID] = service
		}
		expected := "ready"
		if failure {
			expected = "unavailable"
		}
		if byID["db"].Status != expected || byID["review-model"].Status != "unknown" || byID["ocr-model"].Status != "unconfigured" || byID["mineru"].Status != "ready" {
			t.Fatalf("configuration asserted as observation: %+v", byID)
		}
		raw, _ := json.Marshal(snapshot)
		if strings.Contains(string(raw), "redaction-canary-7a62") || strings.Contains(string(raw), "monitor-user") {
			t.Fatalf("credentials exposed: %s", raw)
		}
	}
}

func TestDockerLifecycleStatesAndAdditionalReplicasRemainVisible(t *testing.T) {
	for _, state := range []struct {
		process, health string
		exit            int
		want            string
	}{{"running", "unhealthy", 0, "unavailable"}, {"running", "starting", 0, "starting"}, {"created", "", 0, "starting"}, {"restarting", "", 0, "starting"}, {"paused", "", 0, "stopped"}, {"exited", "", 0, "completed"}} {
		t.Run(state.process+"_"+state.health, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/containers/json" {
					_ = json.NewEncoder(w).Encode([]any{map[string]any{"Id": "first", "Labels": map[string]string{"com.docker.compose.project": "ours", "com.docker.compose.service": "migrate"}}, map[string]any{"Id": "second", "Labels": map[string]string{"com.docker.compose.project": "ours", "com.docker.compose.service": "migrate"}}})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"State": map[string]any{"Status": state.process, "ExitCode": state.exit, "Health": map[string]any{"Status": state.health}}})
			}))
			defer server.Close()
			snapshot := New(Config{Project: "ours", DockerURL: server.URL, Client: server.Client()}).Snapshot(context.Background())
			found := 0
			for _, service := range snapshot.Services {
				if service.Container != nil {
					found++
					if service.Status != state.want {
						t.Fatalf("%s/%s mapped to %s", state.process, state.health, service.Status)
					}
				}
			}
			if found != 2 {
				t.Fatalf("replica hidden: %d", found)
			}
		})
	}
}

func TestRemoteFailureIsAnObservationFailureRatherThanEmptySuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	snapshot := (&Remote{URL: server.URL, Client: server.Client()}).Snapshot(context.Background())
	if snapshot.Source != "monitor_unavailable" || len(snapshot.Services) != 1 || snapshot.Services[0].Status != "unavailable" {
		t.Fatalf("lost monitoring outage: %+v", snapshot)
	}
}
