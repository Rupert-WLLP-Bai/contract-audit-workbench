package operationauthority

import (
	"context"
	"contractaudit/internal/model"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOnlyExplicitAuthenticatedCoreRevocationSignalsConfirmed(t *testing.T) {
	status, body := http.StatusForbidden, `{"code":"operation_principal_revoked"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); _, _ = w.Write([]byte(body)) }))
	defer srv.Close()
	c, err := NewOperationClient(srv.URL, "worker", "private-worker-authority-credential-000000", "", "test", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		status  int
		body    string
		revoked bool
	}{
		{403, `{"code":"operation_principal_revoked"}`, true},
		{403, `{"code":"scope_mismatch"}`, false},
		{401, `{"code":"operation_principal_revoked"}`, false},
		{503, `{"code":"operation_principal_revoked"}`, false},
		{403, `{`, false},
	} {
		status, body = tc.status, tc.body
		_, err := c.ResolveOperation(context.Background(), model.DelegatedScope{})
		if errors.Is(err, ErrConfirmedRevocation) != tc.revoked {
			t.Fatalf("status=%d body=%q revoked=%v err=%v", status, body, tc.revoked, err)
		}
	}
	srv.Close()
	_, err = c.ResolveOperation(context.Background(), model.DelegatedScope{})
	if err == nil || errors.Is(err, ErrConfirmedRevocation) {
		t.Fatalf("closed Core classified as revocation: %v", err)
	}
}
