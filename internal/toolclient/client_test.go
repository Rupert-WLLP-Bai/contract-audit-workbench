package toolclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func clientTestRequest() Request {
	return Request{
		Claim: Claim{
			TenantID: "tenant-test", OrgID: "org-test", JobID: "job-test", WorkerID: "worker-test",
			Generation: 1, AttemptID: "attempt-test",
		},
		CaseID: "case-test", RunID: "run-test", AttemptID: "attempt-test",
		CapabilityID: "dedicated_line.minimum_bandwidth", Deadline: time.Now().UTC().Add(30 * time.Second),
	}
}

func TestFrozenEvidenceReturnsBoundedSafeErrorsForBadGatewayResponses(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{
			name: "non-success HTTP response", statusCode: http.StatusBadGateway,
			body: "upstream postgres://database-user:private-secret@db.internal failed",
			want: "HTTP 502",
		},
		{
			name: "malformed JSON-RPC response", statusCode: http.StatusOK,
			body: `{"jsonrpc":"2.0","result":`, want: "invalid JSON-RPC response",
		},
		{
			name: "oversized response", statusCode: http.StatusOK,
			body: strings.Repeat("x", maxResponseBytes+1), want: "exceeds the size limit",
		},
		{
			name: "bounded JSON-RPC refusal", statusCode: http.StatusOK,
			body: `{"jsonrpc":"2.0","id":"worker-frozen-evidence","error":{"code":-32600,"message":"The current job scope is unavailable."}}`,
			want: "The current job scope is unavailable.",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.statusCode)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			client, err := New(server.URL, strings.Repeat("k", 40), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.FrozenEvidence(context.Background(), clientTestRequest())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("client error = %v, want it to contain %q", err, test.want)
			}
			if len(err.Error()) > 160 {
				t.Fatalf("client returned an unbounded error (%d bytes): %q", len(err.Error()), err.Error())
			}
			if strings.Contains(err.Error(), "private-secret") || strings.Contains(err.Error(), "db.internal") {
				t.Fatalf("client error exposed gateway response details: %q", err.Error())
			}
		})
	}
}
