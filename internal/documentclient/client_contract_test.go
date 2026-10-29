package documentclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/store"
)

const documentClientCredential = "document-client-test-private-key-0001"

func TestNewRejectsNonOriginEndpointsCredentialsAndTimeouts(t *testing.T) {
	for _, test := range []struct {
		name, endpoint, credential string
		timeout                    time.Duration
	}{
		{"missing scheme", "localhost:8080", documentClientCredential, time.Second},
		{"unsupported scheme", "ftp://document.example", documentClientCredential, time.Second},
		{"endpoint credentials", "http://user:password@document.example", documentClientCredential, time.Second},
		{"endpoint query", "http://document.example?target=internal", documentClientCredential, time.Second},
		{"endpoint fragment", "http://document.example#prepare", documentClientCredential, time.Second},
		{"short service credential", "http://document.example", "short", time.Second},
		{"zero timeout", "http://document.example", documentClientCredential, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.endpoint, test.credential, test.timeout); err == nil {
				t.Fatal("invalid document client configuration was accepted")
			}
		})
	}
}

func TestNewAcceptsCredentialAtExactMinimumLength(t *testing.T) {
	client, err := New("http://document.example/base", strings.Repeat("k", 32), time.Second)
	if err != nil {
		t.Fatalf("New rejected a 32-byte credential: %v", err)
	}
	if client.endpoint != "http://document.example/base/internal/v1/prepare" {
		t.Fatalf("endpoint = %q", client.endpoint)
	}
}

func TestPrepareAttemptDocumentRejectsUntrustedErrorAndMalformedResponses(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"authorization refusal", `{"error":"database password=secret"}`, http.StatusForbidden},
		{"malformed JSON", `{"ready":`, http.StatusOK},
		{"unknown response field", `{"ready":true,"admin":true}`, http.StatusOK},
		{"trailing response object", `{"ready":true} {}`, http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client, err := New(server.URL, documentClientCredential, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			frozen, err := client.PrepareAttemptDocument(context.Background(), store.PrepareDocumentRequest{})
			if err == nil || frozen.Ready || frozen.Text != "" {
				t.Fatalf("untrusted response produced a prepared document: ready=%v text=%q err=%v", frozen.Ready, frozen.Text, err)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "database password") {
				t.Fatalf("service response exposed secret material: %v", err)
			}
		})
	}
}

func TestPrepareAttemptDocumentPropagatesCallerCancellation(t *testing.T) {
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
	client, err := New(server.URL, documentClientCredential, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := client.PrepareAttemptDocument(ctx, store.PrepareDocumentRequest{})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("client request did not reach document service")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled preparation returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("client did not stop after caller cancellation")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("caller cancellation did not cancel the service request")
	}
}

func TestDocumentClientNormalizesEndpointAndReturnsFrozenSuccess(t *testing.T) {
	const credential = documentClientCredential
	request := store.PrepareDocumentRequest{
		TenantID: "tenant-a", OrgID: "sales", JobID: "job-1", WorkerID: "worker-1", Generation: 2,
		RunID: "run-1", AttemptID: "attempt-1", DocumentID: "doc-1",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/internal/v1/prepare" {
			t.Errorf("request = %s %s, want POST /api/internal/v1/prepare", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" ||
			r.Header.Get("X-Contract-Service-Key") != credential {
			t.Errorf("request headers = %#v", r.Header)
		}
		var got store.PrepareDocumentRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if !reflect.DeepEqual(got, request) {
			t.Errorf("request body = %+v, want %+v", got, request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ready":true,"text":"冻结文本","original_hash":"sha256:original","parser_ref":"parser-v1"}`)
	}))
	defer server.Close()
	client, err := New(" "+server.URL+"/api/// ", credential, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := client.PrepareAttemptDocument(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !frozen.Ready || frozen.Text != "冻结文本" || frozen.OriginalHash != "sha256:original" || frozen.ParserRef != "parser-v1" {
		t.Fatalf("frozen document = %+v", frozen)
	}
}

func TestPrepareAttemptDocumentRejectsOversizedAndNonSuccessResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"non-success status", http.StatusConflict, `{}`, "HTTP 409"},
		{"oversized body", http.StatusOK, strings.Repeat("x", maxPreparedDocumentBytes+1), "exceeds the size limit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client, err := New(server.URL, documentClientCredential, 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.PrepareAttemptDocument(context.Background(), store.PrepareDocumentRequest{}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("PrepareAttemptDocument error = %v, want %q", err, test.want)
			}
		})
	}
}
