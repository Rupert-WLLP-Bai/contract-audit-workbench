package systemhealth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type healthTransportFunc func(*http.Request) (*http.Response, error)

func (f healthTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestKubernetesPodObservationDistinguishesIncompleteAndUnreadyContainers(t *testing.T) {
	for _, test := range []struct {
		name                   string
		state                  map[string]any
		ready                  bool
		status, containerState string
	}{
		{"waiting", map[string]any{"waiting": map[string]any{}}, true, "unavailable", "waiting"},
		{"terminated", map[string]any{"terminated": map[string]any{}}, true, "unavailable", "terminated"},
		{"missing runtime state", map[string]any{}, true, "unavailable", "Running"},
		{"not ready", map[string]any{"running": map[string]any{}}, false, "unavailable", "running"},
		{"ready", map[string]any{"running": map[string]any{}}, true, "ready", "running"},
		{"mixed runtime states", map[string]any{"running": map[string]any{}, "waiting": map[string]any{}}, true, "unavailable", "running"},
	} {
		t.Run(test.name, func(t *testing.T) {
			pod := map[string]any{"metadata": map[string]any{"name": "observed-pod", "labels": map[string]any{"app.kubernetes.io/name": "core-api"}},
				"spec": map[string]any{"containers": []any{map[string]any{"name": "api"}}},
				"status": map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
					"containerStatuses": []any{map[string]any{"name": "api", "ready": test.ready, "state": test.state}}}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/pods") {
					_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{pod}})
					return
				}
				_, _ = w.Write([]byte(`{"items":[]}`))
			}))
			defer server.Close()
			snapshot := New(Config{Kubernetes: &KubernetesConfig{APIURL: server.URL, Namespace: "fallback-namespace", Client: server.Client()}}).Snapshot(context.Background())
			for _, service := range snapshot.Services {
				if service.ID != "core-api" {
					continue
				}
				if service.Status != test.status || service.Container == nil || service.Container.State != test.containerState || service.Namespace != "fallback-namespace" || service.InstanceID != "observed-pod" {
					t.Fatalf("pod observation or fallback identity lost: %+v", service)
				}
				return
			}
			t.Fatal("observed pod disappeared")
		})
	}
}

func TestDockerReadinessUsesTheEndpointResultWithoutInference(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			_, _ = w.Write([]byte(`[{"Id":"grafana-id","Labels":{"com.docker.compose.project":"ours","com.docker.compose.service":"grafana"}}]`))
			return
		}
		_, _ = w.Write([]byte(`{"State":{"Status":"running"}}`))
	}))
	defer server.Close()
	for _, code := range []int{200, 503, 0} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			previous := endpointClient
			t.Cleanup(func() { endpointClient = previous })
			calls := 0
			endpointClient = &http.Client{Transport: healthTransportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" || r.URL.String() != "http://grafana:3000/api/health" {
					t.Errorf("unexpected probe: %s %s", r.Method, r.URL)
				}
				if code == 0 {
					return nil, errors.New("probe unavailable")
				}
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
			})}
			snapshot := New(Config{Project: "ours", DockerURL: server.URL, Client: server.Client()}).Snapshot(context.Background())
			for _, service := range snapshot.Services {
				if service.ID != "grafana" {
					continue
				}
				want := "unavailable"
				if code == 200 {
					want = "ready"
				}
				if calls != 1 || service.Status != want || service.Probe == nil || service.Probe.Status != want {
					t.Fatalf("probe observation lost: calls=%d service=%+v", calls, service)
				}
				return
			}
			t.Fatal("observed grafana disappeared")
		})
	}
}

func TestKubernetesRejectsMalformedAndOversizedMetadataWithoutLosingOtherSources(t *testing.T) {
	for _, body := range []string{"{invalid", strings.Repeat(" ", kubernetesBodyLimit+1)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/pods") {
				_, _ = io.WriteString(w, body)
				return
			}
			_, _ = w.Write([]byte(`{"items":[]}`))
		}))
		snapshot := New(Config{Kubernetes: &KubernetesConfig{APIURL: server.URL, Namespace: "workspace", Client: server.Client()}}).Snapshot(context.Background())
		server.Close()
		if snapshot.Source != "kubernetes_partial" || snapshot.Cluster == nil {
			t.Fatalf("partial observation lost: %+v", snapshot)
		}
		for _, service := range snapshot.Services {
			if service.ID == "core-api" && service.Status != "unknown" {
				t.Fatalf("bad Pod response became authoritative: %+v", service)
			}
		}
	}
}
