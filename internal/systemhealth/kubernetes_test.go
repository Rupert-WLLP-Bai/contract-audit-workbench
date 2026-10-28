package systemhealth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestKubernetesSnapshotMapsReadyPodsAndAdditionalReplicas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("service account token was not sent as bearer auth")
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected Kubernetes write: %s", r.Method)
		}
		if r.URL.Path == "/api/v1/nodes" {
			_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"node-a"},"status":{"nodeInfo":{"kubeletVersion":"v1.31.2"},"conditions":[{"type":"Ready","status":"True"}]}},{"metadata":{"name":"node-b"},"status":{"nodeInfo":{"kubeletVersion":"v1.31.2"},"conditions":[{"type":"Ready","status":"False"}]}}]}`))
			return
		}
		if r.URL.Path == "/api/v1/namespaces/workspace/pods" {
			_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"core-api-a","namespace":"workspace","uid":"pod-a","labels":{"app.kubernetes.io/name":"core-api"}},"spec":{"nodeName":"node-a","containers":[{"name":"api"}]},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"api","image":"backend:v1","ready":true,"restartCount":1,"state":{"running":{"startedAt":"2026-10-01T10:00:00Z"}}}]}},{"metadata":{"name":"core-api-b","namespace":"workspace","uid":"pod-b","labels":{"app.kubernetes.io/name":"core-api"}},"spec":{"nodeName":"node-b","containers":[{"name":"api"}]},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"api","image":"backend:v1","ready":true,"restartCount":0,"state":{"running":{"startedAt":"2026-10-02T10:00:00Z"}}}]}}]}`))
			return
		}
		if r.URL.Path == "/apis/apps/v1/namespaces/workspace/deployments" {
			_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"core-api","labels":{"app.kubernetes.io/name":"core-api"}},"spec":{"replicas":2},"status":{"readyReplicas":2}}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	snapshot := New(Config{Kubernetes: &KubernetesConfig{APIURL: server.URL, Token: "test-token", Namespace: "workspace", Client: server.Client()}}).Snapshot(context.Background())
	if snapshot.Mode != "kubernetes" || snapshot.Source != "kubernetes" || snapshot.Cluster == nil || snapshot.Cluster.Status != "unavailable" || len(snapshot.Cluster.Nodes) != 2 || !strings.Contains(snapshot.Cluster.Detail, "1/2") {
		t.Fatalf("cluster observation missing: %+v", snapshot)
	}
	if snapshot.Cluster.Nodes[0].Status != "ready" || snapshot.Cluster.Nodes[1].Status != "unavailable" || snapshot.Cluster.Nodes[0].Version != "v1.31.2" {
		t.Fatalf("node readiness/version lost: %+v", snapshot.Cluster.Nodes)
	}
	count := 0
	replicas := map[string]int{"core-api": 1, "core-api/core-api-b": 0}
	for _, service := range snapshot.Services {
		if service.ID == "core-api" || strings.HasPrefix(service.ID, "core-api/") {
			count++
			if service.Status != "ready" || service.Container == nil || service.Container.Restarts != replicas[service.ID] ||
				service.Container.StartedAt == "" || service.Container.Image != "backend:v1" || service.Container.Health != "healthy" ||
				service.LogicalID != "core-api" || service.InstanceID == "" || service.Namespace != "workspace" ||
				service.NodeName == "" || service.Workload != "core-api" || service.ObservedAt == "" {
				t.Fatalf("pod metadata/readiness lost: %+v", service)
			}
			delete(replicas, service.ID)
		}
	}
	if count != 2 || len(replicas) != 0 {
		t.Fatalf("expected uniquely identified replicas, found %d (%v)", count, replicas)
	}
	groupFound := false
	for _, group := range snapshot.Groups {
		if group.ID != "core-api" {
			continue
		}
		groupFound = true
		if group.ExpectedInstances != 2 || group.ObservedInstances != 2 || group.ReadyInstances != 2 ||
			group.ExpectedSource != "deployment" || group.Status != "ready" {
			t.Fatalf("deployment replica expectation lost: %+v", group)
		}
	}
	if !groupFound {
		t.Fatal("core-api group missing")
	}
	for _, service := range snapshot.Services {
		if service.ID == "db" && service.Status != "unavailable" {
			t.Fatalf("expected absent configured service unavailable: %+v", service)
		}
	}
	if entry := architectureByID(snapshot.Architecture, "kubernetes"); entry.State != "observed" || entry.Verification != "NOT_RUN" || !strings.Contains(entry.Observed, "1") {
		t.Fatalf("Kubernetes architecture evidence overclaimed or omitted node count: %+v", entry)
	}
}

func TestKubernetesReadsRotatedServiceAccountTokenPerRequest(t *testing.T) {
	tokenPath := t.TempDir() + "/token"
	if err := os.WriteFile(tokenPath, []byte("token-one"), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var observed []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		observed = append(observed, r.Header.Get("Authorization"))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()
	config := &KubernetesConfig{APIURL: server.URL, Token: "injected-token", Namespace: "workspace", Client: server.Client(), tokenPath: tokenPath}
	monitor := New(Config{Kubernetes: config})
	monitor.Snapshot(context.Background())
	if err := os.WriteFile(tokenPath, []byte("token-two"), 0600); err != nil {
		t.Fatal(err)
	}
	monitor.Snapshot(context.Background())
	mu.Lock()
	captured := append([]string(nil), observed...)
	mu.Unlock()
	counts := map[string]int{}
	for _, header := range captured {
		counts[header]++
	}
	// Each snapshot reads nodes, pods and deployments; the requests are
	// concurrent, so order is not the contract but the token generation is.
	if len(captured) != 6 || counts["Bearer token-one"] != 3 || counts["Bearer token-two"] != 3 {
		t.Fatalf("API requests did not use the current projected token: %v", captured)
	}
}

func TestKubernetesLifecyclePodPhases(t *testing.T) {
	for _, tc := range []struct{ phase, want string }{{"Pending", "starting"}, {"Running", "unknown"}, {"Succeeded", "completed"}, {"Failed", "unavailable"}, {"Unknown", "unknown"}} {
		t.Run(tc.phase, func(t *testing.T) {
			pod := kubernetesPod{}
			pod.Status.Phase = tc.phase
			status, _ := podReadiness(pod)
			if status != tc.want {
				t.Fatalf("phase %s mapped to %s, want %s", tc.phase, status, tc.want)
			}
		})
	}
}

func TestKubernetesClusterReadyRequiresEveryNode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/nodes" {
			_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"node-a"},"status":{"conditions":[{"type":"Ready","status":"True"}]}},{"metadata":{"name":"node-b"},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()
	snapshot := New(Config{Kubernetes: &KubernetesConfig{APIURL: server.URL, Token: "token", Namespace: "workspace", Client: server.Client()}}).Snapshot(context.Background())
	if snapshot.Cluster.Status != "ready" || !strings.Contains(snapshot.Cluster.Detail, "2/2") {
		t.Fatalf("fully Ready node set was not reported: %+v", snapshot.Cluster)
	}
}

func TestKubernetesFailureIsUnknownAndRedactsCredentials(t *testing.T) {
	const secret = "secret-token-should-not-appear"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/nodes" {
			http.Error(w, secret, http.StatusForbidden)
			return
		}
		http.Error(w, secret, http.StatusForbidden)
	}))
	defer server.Close()

	snapshot := New(Config{Kubernetes: &KubernetesConfig{APIURL: server.URL, Token: secret, Namespace: "workspace", Client: server.Client()}}).Snapshot(context.Background())
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Mode != "kubernetes" || snapshot.Source != "kubernetes_unavailable" || snapshot.Cluster == nil || snapshot.Cluster.Status != "unavailable" {
		t.Fatalf("Kubernetes outage was not represented: %+v", snapshot)
	}
	for _, service := range snapshot.Services {
		if service.Status != "unknown" && service.Status != "unconfigured" {
			t.Fatalf("failed API asserted service state: %+v", service)
		}
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatal("Kubernetes credentials/API error leaked into snapshot")
	}
}

func TestKubernetesUnknownPodReadinessAndUnconfiguredCluster(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/nodes" {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"core-api-unknown","labels":{"app.kubernetes.io/name":"core-api"}},"spec":{"containers":[{"name":"api"}]},"status":{"phase":"Running","containerStatuses":[]}}]}`))
	}))
	defer server.Close()
	snapshot := New(Config{Kubernetes: &KubernetesConfig{APIURL: server.URL, Token: "t", Namespace: "workspace", Client: server.Client()}}).Snapshot(context.Background())
	if snapshot.Cluster.Status != "unavailable" {
		t.Fatalf("empty node list reported ready: %+v", snapshot.Cluster)
	}
	found := false
	for _, service := range snapshot.Services {
		if service.ID == "core-api" {
			found = true
			if service.Status != "unknown" {
				t.Fatalf("missing pod container status should remain unknown: %+v", service)
			}
		}
	}
	if !found {
		t.Fatal("expected service pod was not represented")
	}
	plain := New(Config{}).Snapshot(context.Background())
	if plain.Cluster != nil {
		t.Fatalf("cluster should be null without configured source: %+v", plain.Cluster)
	}
}

func architectureByID(entries []ArchitectureEntry, id string) ArchitectureEntry {
	for _, entry := range entries {
		if entry.ID == id {
			return entry
		}
	}
	return ArchitectureEntry{}
}

func TestKubernetesPartialNodeObservationKeepsPodsUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/nodes":
			_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"node-a"},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`))
		case "/api/v1/namespaces/workspace/pods":
			http.Error(w, "pods forbidden", http.StatusForbidden)
		case "/apis/apps/v1/namespaces/workspace/deployments":
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	snapshot := New(Config{Kubernetes: &KubernetesConfig{APIURL: server.URL, Token: "token", Namespace: "workspace", Client: server.Client()}}).Snapshot(context.Background())
	if snapshot.Source != "kubernetes_partial" || snapshot.Cluster == nil || len(snapshot.Cluster.Nodes) != 1 || snapshot.Cluster.Nodes[0].Status != "ready" {
		t.Fatalf("partial Kubernetes observation lost the readable node result: %+v", snapshot.Cluster)
	}
	for _, service := range snapshot.Services {
		if service.Status == "ready" {
			t.Fatalf("Pod failure fabricated readiness: %+v", service)
		}
	}
	for _, group := range snapshot.Groups {
		if group.ID == "core-api" && group.Status != "unknown" {
			t.Fatalf("missing Pod list must stay unknown, not become a fault: %+v", group)
		}
	}
}
