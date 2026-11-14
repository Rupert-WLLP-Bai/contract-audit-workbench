package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"contractaudit/internal/fault"
)

func TestCoreSessionResolverInvalidTokenNeverCallsAuthority(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	resolver := &coreSessionResolver{endpoint: server.URL, client: server.Client()}
	for _, token := range []string{"", strings.Repeat("x", 4097), " ", "a\tb", "a\nb", "a\u3000b"} {
		_, _, err := resolver.AuthenticateSession(context.Background(), token)
		var problem *fault.Error
		if !errors.As(err, &problem) || problem.Status != http.StatusUnauthorized || problem.Code != "session_invalid" {
			t.Errorf("invalid token must return session_invalid 401, got %v", err)
		}
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("invalid tokens called authority %d times", got)
	}
	_, _, err := resolver.AuthenticateSession(context.Background(), "valid-token")
	var problem *fault.Error
	if !errors.As(err, &problem) || problem.Status != http.StatusUnauthorized || problem.Code != "session_invalid" || calls.Load() != 1 {
		t.Fatalf("valid token must reach authority and preserve its 401: calls=%d error=%v", calls.Load(), err)
	}
}
