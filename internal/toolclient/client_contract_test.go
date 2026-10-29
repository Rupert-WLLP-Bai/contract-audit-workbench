package toolclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const toolClientCredential = "tool-client-test-private-key-0001"

func TestNewRejectsUnsafeGatewayConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, endpoint, credential string
		timeout                    time.Duration
	}{
		{"missing scheme", "gateway.internal", toolClientCredential, time.Second},
		{"unsupported scheme", "ftp://gateway.internal", toolClientCredential, time.Second},
		{"embedded credentials", "http://user:password@gateway.internal", toolClientCredential, time.Second},
		{"endpoint query", "http://gateway.internal?target=unsafe", toolClientCredential, time.Second},
		{"endpoint fragment", "http://gateway.internal#mcp", toolClientCredential, time.Second},
		{"short service credential", "http://gateway.internal", "short", time.Second},
		{"nonpositive timeout", "http://gateway.internal", toolClientCredential, -time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.endpoint, test.credential, test.timeout); err == nil {
				t.Fatal("unsafe tool client configuration was accepted")
			}
		})
	}
}

func TestFrozenEvidenceRejectsIncompleteOrOversizedClaimBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	client, err := New(server.URL, toolClientCredential, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*Request)
	}{
		{"missing tenant", func(r *Request) { r.Claim.TenantID = "" }},
		{"missing organization", func(r *Request) { r.Claim.OrgID = "" }},
		{"missing job", func(r *Request) { r.Claim.JobID = "" }},
		{"missing worker", func(r *Request) { r.Claim.WorkerID = "" }},
		{"missing generation", func(r *Request) { r.Claim.Generation = 0 }},
		{"missing claimed attempt", func(r *Request) { r.Claim.AttemptID = "" }},
		{"mismatched attempt", func(r *Request) { r.AttemptID = "another-attempt" }},
		{"missing case", func(r *Request) { r.CaseID = "" }},
		{"missing run", func(r *Request) { r.RunID = "" }},
		{"missing capability", func(r *Request) { r.CapabilityID = "" }},
		{"missing deadline", func(r *Request) { r.Deadline = time.Time{} }},
		{"oversized claim", func(r *Request) { r.Claim.WorkerID = strings.Repeat("w", maxClaimBytes) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := clientTestRequest()
			test.mutate(&request)
			if _, err := client.FrozenEvidence(context.Background(), request); err == nil {
				t.Fatal("invalid delegated claim was sent to the gateway")
			}
		})
	}
	if _, err := client.FrozenEvidence(nil, clientTestRequest()); err == nil {
		t.Fatal("nil tool call context was accepted")
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid claims reached the tool gateway %d times", calls.Load())
	}
}

func TestFrozenEvidenceRejectsUnexpectedToolResultShapes(t *testing.T) {
	for _, test := range []struct {
		name, body string
	}{
		{"missing result", `{"jsonrpc":"2.0","id":"worker-frozen-evidence"}`},
		{"tool error result", `{"result":{"isError":true,"content":[{"type":"text","text":"denied"}]}}`},
		{"empty content", `{"result":{"content":[]}}`},
		{"multiple payloads", `{"result":{"content":[{"type":"text","text":"{}"},{"type":"text","text":"{}"}]}}`},
		{"nontext payload", `{"result":{"content":[{"type":"image","text":"{}"}]}}`},
		{"non-JSON evidence", `{"result":{"content":[{"type":"text","text":"not json"}]}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := New(server.URL, toolClientCredential, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := client.FrozenEvidence(context.Background(), clientTestRequest())
			if err == nil || len(payload) != 0 {
				t.Fatalf("invalid tool result was returned as evidence: payload=%q err=%v", payload, err)
			}
		})
	}
}

func TestFrozenEvidenceCancellationStopsTheGatewayRequest(t *testing.T) {
	entered := make(chan struct{}, 1)
	cancelled := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	client, err := New(server.URL, toolClientCredential, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := client.FrozenEvidence(ctx, clientTestRequest())
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("tool request did not reach the gateway")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled tool request returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("tool client did not stop after cancellation")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("caller cancellation did not cancel the gateway request")
	}
}

func TestFrozenEvidenceSendsCurrentClaimAndReturnsReadOnlyPayload(t *testing.T) {
	request := clientTestRequest()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/mcp" {
			t.Errorf("request = %s %s, want POST /api/mcp", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" ||
			r.Header.Get("Accept") != "application/json, text/event-stream" ||
			r.Header.Get("MCP-Protocol-Version") != ProtocolVersion ||
			r.Header.Get("Mcp-Method") != "tools/call" || r.Header.Get("Mcp-Name") != ToolName ||
			r.Header.Get("X-Contract-Service-Key") != toolClientCredential {
			t.Errorf("request headers = %#v", r.Header)
		}
		var job struct {
			TenantID   string `json:"tenant_id"`
			OrgID      string `json:"org_id"`
			JobID      string `json:"job_id"`
			WorkerID   string `json:"worker_id"`
			Generation int64  `json:"generation"`
			AttemptID  string `json:"attempt_id"`
		}
		if err := json.Unmarshal([]byte(r.Header.Get("X-Contract-Job-Context")), &job); err != nil {
			t.Errorf("decode claim header: %v", err)
		}
		if job.TenantID != request.Claim.TenantID || job.OrgID != request.Claim.OrgID || job.JobID != request.Claim.JobID ||
			job.WorkerID != request.Claim.WorkerID || job.Generation != request.Claim.Generation || job.AttemptID != request.Claim.AttemptID {
			t.Errorf("job context = %+v, want claim %+v", job, request.Claim)
		}
		var body struct {
			JSONRPC string `json:"jsonrpc"`
			ID      string `json:"id"`
			Method  string `json:"method"`
			Params  struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode tool request: %v", err)
		}
		if body.JSONRPC != "2.0" || body.ID != "worker-frozen-evidence" || body.Method != "tools/call" || body.Params.Name != ToolName {
			t.Errorf("tool request envelope = %+v", body)
		}
		if body.Params.Arguments["attempt_id"] != request.AttemptID || body.Params.Arguments["case_id"] != request.CaseID ||
			body.Params.Arguments["run_id"] != request.RunID || body.Params.Arguments["capability_id"] != request.CapabilityID ||
			body.Params.Arguments["deadline"] != request.Deadline.UTC().Format(time.RFC3339) {
			t.Errorf("tool arguments = %+v", body.Params.Arguments)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":"worker-frozen-evidence","result":{"isError":false,"content":[{"type":"text","text":"{\"frozen\":true}"}]}}`)
	}))
	defer server.Close()
	client, err := New(" "+server.URL+"/api/// ", toolClientCredential, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := client.FrozenEvidence(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"frozen":true}` || calls.Load() != 1 {
		t.Fatalf("payload=%s calls=%d", payload, calls.Load())
	}
}
