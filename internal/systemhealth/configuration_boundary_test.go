package systemhealth

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestKubernetesEnvironmentRejectsIncompleteAndInvalidAddresses(t *testing.T) {
	for _, pair := range [][2]string{{"", "443"}, {"localhost", ""}, {"localhost", "0"}, {"localhost", "65536"}, {"localhost", "x"}, {"host/path", "443"}} {
		t.Run(pair[0]+":"+pair[1], func(t *testing.T) {
			t.Setenv("KUBERNETES_SERVICE_HOST", pair[0])
			t.Setenv("KUBERNETES_SERVICE_PORT", pair[1])
			config := KubernetesConfigFromEnv()
			if pair[0] == "" || pair[1] == "" {
				if config != nil {
					t.Fatal("incomplete environment enabled cluster observation")
				}
				return
			}
			if config == nil {
				t.Fatal("invalid configured cluster disappeared")
			}
			snapshot := New(Config{Kubernetes: config}).Snapshot(context.Background())
			if snapshot.Source != "kubernetes_unavailable" {
				t.Fatalf("invalid cluster marked connected: %+v", snapshot)
			}
		})
	}
}

// Use real service-account files and a real TLS endpoint to exercise the same
// initialization transaction as the environment entry point.
func TestServiceAccountInitializationTrustsOnlyItsCAAndKeepsCredentialsPrivate(t *testing.T) {
	const token = "private-health-token-canary"
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("service account token was not sent")
		}
		if r.URL.Path != "/api/v1/nodes" && !strings.Contains(r.URL.Path, "/namespaces/workspace/") {
			t.Errorf("unexpected metadata route: %s", r.URL.Path)
		}
		requests.Add(1)
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()
	host, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	for _, namespaceFromEnv := range []bool{false, true} {
		t.Run(map[bool]string{false: "file namespace", true: "environment namespace"}[namespaceFromEnv], func(t *testing.T) {
			directory := t.TempDir()
			for name, data := range map[string][]byte{"token": []byte("  " + token + "\n"), "namespace": []byte(" workspace\n"), "ca.crt": certificate} {
				if err := os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("POD_NAMESPACE", "")
			if namespaceFromEnv {
				t.Setenv("POD_NAMESPACE", " workspace ")
				if err := os.Remove(filepath.Join(directory, "namespace")); err != nil {
					t.Fatal(err)
				}
			}
			config := &KubernetesConfig{tokenPath: filepath.Join(directory, "token")}
			if err := config.initialize(host, port); err != nil {
				t.Fatal(err)
			}
			snapshot := New(Config{Kubernetes: config}).Snapshot(context.Background())
			if snapshot.Source != "kubernetes" || snapshot.Cluster.Namespace != "workspace" {
				t.Fatalf("trusted metadata unavailable: %+v", snapshot)
			}
			body, err := json.Marshal(snapshot)
			if err != nil || strings.Contains(string(body), token) {
				t.Fatal("snapshot exposed service credentials")
			}
		})
	}
	if requests.Load() != 6 {
		t.Fatalf("metadata requests = %d, want 6", requests.Load())
	}
}

func TestServiceAccountInitializationReportsFirstMissingPrerequisite(t *testing.T) {
	for _, test := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"missing token", map[string]string{}, "in-cluster service account token unavailable"},
		{"blank token", map[string]string{"token": " \n"}, "in-cluster service account token unavailable"},
		{"missing namespace", map[string]string{"token": "token"}, "in-cluster namespace unavailable"},
		{"blank namespace", map[string]string{"token": "token", "namespace": " "}, "in-cluster namespace unavailable"},
		{"missing CA", map[string]string{"token": "token", "namespace": "workspace"}, "in-cluster CA unavailable"},
		{"invalid CA", map[string]string{"token": "token", "namespace": "workspace", "ca.crt": "invalid"}, "invalid in-cluster CA"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("POD_NAMESPACE", "")
			directory := t.TempDir()
			for name, value := range test.files {
				if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			config := &KubernetesConfig{tokenPath: filepath.Join(directory, "token")}
			if err := config.initialize("localhost", "443"); err == nil || err.Error() != test.want {
				t.Fatalf("initialization error = %v, want %s", err, test.want)
			}
		})
	}
}

func TestReplicaDeclarationBoundariesAndReplacementOrder(t *testing.T) {
	identifier := strings.Repeat("a", 64)
	result, err := ParseExpectedReplicas(identifier + "=0,A-Z_1.0=99,duplicate=1,duplicate=2")
	if err != nil || result[identifier] != 0 || result["A-Z_1.0"] != 99 || result["duplicate"] != 2 {
		t.Fatalf("valid bounds or last declaration lost: %v %v", result, err)
	}
	for _, raw := range []string{identifier + "a=1", "模型=1", "invalid/id=1", "x=999999999999999999999999999999"} {
		if _, err := ParseExpectedReplicas(raw); err == nil {
			t.Fatalf("invalid declaration accepted: %q", raw)
		}
	}
	t.Setenv("SYSTEM_HEALTH_EXPECTED_REPLICAS", "core-api=2")
	result, err = ExpectedReplicasFromEnv()
	if err != nil || result["core-api"] != 2 {
		t.Fatalf("environment declaration lost: %v %v", result, err)
	}
}
