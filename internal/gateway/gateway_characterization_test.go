package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewPreservesSafeConfigurationFailureMessages(t *testing.T) {
	tooManyReplicas := make([]string, maxReplicasPerService+1)
	for i := range tooManyReplicas {
		tooManyReplicas[i] = "http://core.example.test"
	}

	for _, test := range []struct {
		name    string
		change  func(*Config)
		wantErr string
	}{
		{
			name:    "too many replicas",
			change:  func(config *Config) { config.CoreURLs = tooManyReplicas },
			wantErr: "core supports at most 16 upstream replicas",
		},
		{
			name:    "unsafe URL does not echo credentials",
			change:  func(config *Config) { config.CoreURLs = []string{"http://alice:secret@core.example.test"} },
			wantErr: "the core upstream URL is not an HTTP(S) base URL without credentials, query, or path prefix",
		},
		{
			name:    "unsupported URL scheme",
			change:  func(config *Config) { config.CoreURLs = []string{"ftp://core.example.test"} },
			wantErr: "the core upstream URL is not an HTTP(S) base URL without credentials, query, or path prefix",
		},
		{
			name: "invalid health interval precedes missing target",
			change: func(config *Config) {
				config.CoreURLs = nil
				config.HealthInterval = minHealthInterval - time.Nanosecond
			},
			wantErr: "GATEWAY_HEALTH_INTERVAL must be between 100ms and 30s",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := baseConfig()
			test.change(&config)
			_, err := New(config)
			if err == nil || err.Error() != test.wantErr {
				t.Fatalf("New() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestDocumentOriginalStreamOutlivesRequestTimeoutAndCancelsUpstream(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	cancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/cases/case-1/documents/doc-1/original" {
			t.Errorf("unexpected document request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = io.WriteString(w, "original-chunk")
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-release:
		}
	}))
	t.Cleanup(upstream.Close)

	config := baseConfig()
	config.DocumentURL = upstream.URL
	config.RequestTimeout = 40 * time.Millisecond
	edge := startGateway(t, config)

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		edge.URL+"/api/cases/case-1/documents/doc-1/original", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("document original request failed: %v", err)
	}
	first := make([]byte, len("original-chunk"))
	read := make(chan error, 1)
	go func() {
		_, readErr := io.ReadFull(response.Body, first)
		read <- readErr
	}()
	select {
	case readErr := <-read:
		if readErr != nil {
			t.Fatalf("document original stream read failed: %v", readErr)
		}
	case <-time.After(time.Second):
		t.Fatal("document original bytes were buffered until stream completion")
	}
	if string(first) != "original-chunk" {
		t.Fatalf("document original stream delivered %q", first)
	}
	select {
	case <-cancelled:
		t.Fatal("ordinary request timeout cancelled the document original stream")
	case <-time.After(3 * config.RequestTimeout):
	}

	cancel()
	_ = response.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("client cancellation did not cancel the document original request")
	}
}
