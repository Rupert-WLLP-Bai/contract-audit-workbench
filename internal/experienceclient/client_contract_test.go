package experienceclient

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

const experienceClientCredential = "experience-client-test-private-key-0001"

func TestNewRejectsUnsafeCoreConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint, credential string
		timeout                    time.Duration
	}{
		{"missing scheme", "core.internal", experienceClientCredential, time.Second},
		{"unsupported scheme", "ftp://core.internal", experienceClientCredential, time.Second},
		{"embedded credentials", "http://user:secret@core.internal", experienceClientCredential, time.Second},
		{"endpoint query", "http://core.internal?target=bad", experienceClientCredential, time.Second},
		{"endpoint fragment", "http://core.internal#results", experienceClientCredential, time.Second},
		{"short credential", "http://core.internal", "short", time.Second},
		{"nonpositive timeout", "http://core.internal", experienceClientCredential, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.endpoint, tc.credential, tc.timeout); err == nil {
				t.Fatal("unsafe core client configuration was accepted")
			}
		})
	}
}

func TestAcceptExperienceResultSendsFencedIdentityAndAcceptsNoContent(t *testing.T) {
	delivery := store.ExperienceResultDelivery{
		TenantID: "tenant-a", JobID: "job-1", Generation: 7,
		InputSHA256: strings.Repeat("a", 64), ResultSHA256: strings.Repeat("b", 64),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/internal/v1/experience-results" {
			t.Errorf("request = %s %s, want POST /api/internal/v1/experience-results", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Contract-Service-Key") != experienceClientCredential {
			t.Errorf("request headers = %#v", r.Header)
		}
		var got store.ExperienceResultDelivery
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode delivery: %v", err)
		}
		if !reflect.DeepEqual(got, delivery) {
			t.Errorf("delivery = %+v, want %+v", got, delivery)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := New(" "+server.URL+"/api/// ", experienceClientCredential, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.AcceptExperienceResult(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptExperienceResultRejectsNonNoContentResponseAndNilContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "private response detail")
	}))
	defer server.Close()
	client, err := New(server.URL, experienceClientCredential, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.AcceptExperienceResult(context.Background(), store.ExperienceResultDelivery{}); err == nil || err.Error() != "core result delivery returned HTTP 202" {
		t.Fatalf("AcceptExperienceResult error = %v", err)
	}
	if err := client.AcceptExperienceResult(nil, store.ExperienceResultDelivery{}); err == nil || err.Error() != "result delivery context is required" {
		t.Fatalf("nil context error = %v", err)
	}
}

func TestAcceptExperienceResultPropagatesCallerCancellation(t *testing.T) {
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
	client, err := New(server.URL, experienceClientCredential, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.AcceptExperienceResult(ctx, store.ExperienceResultDelivery{}) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("delivery request did not reach Core")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled delivery error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("client did not stop after caller cancellation")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("caller cancellation did not cancel the Core request")
	}
}
