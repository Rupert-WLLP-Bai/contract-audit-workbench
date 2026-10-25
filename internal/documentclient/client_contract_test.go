package documentclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
