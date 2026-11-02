package documentservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoreIntakeAuthorityClientRestrictsInsecureDevelopmentOrigins(t *testing.T) {
	for _, tc := range []struct {
		url, env string
		allow    bool
		wantErr  bool
	}{
		{"https://core.example", "production", false, false},
		{"http://core-api:8081", "development", true, false},
		{"http://127.0.0.1:8123", "test", true, false},
		{"http://core-api:8082", "development", true, true},
		{"http://api.internal:8081", "development", true, true},
		{"http://core-api:8081", "production", true, true},
		{"http://127.0.0.1:8123", "development", false, true},
		{"https://core.example/path", "production", false, true},
	} {
		t.Run(tc.url+"/"+tc.env, func(t *testing.T) {
			_, err := newCoreIntakeAuthorityClient(tc.url, "", tc.allow, tc.env,
				strings.Repeat("k", 32), time.Second)
			if (err != nil) != tc.wantErr {
				t.Fatalf("client error=%v, wantErr=%t", err, tc.wantErr)
			}
		})
	}
}

func TestCoreIntakeAuthorityClientUsesConfiguredTrustAndRejectsRedirects(t *testing.T) {
	body := []byte("合同字节")
	digest := sha256.Sum256(body)
	digestText := hex.EncodeToString(digest[:])
	var redirected atomic.Bool
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Store(true)
		_, _ = w.Write([]byte(`{"actor_id":"must-not-be-reached"}`))
	}))
	defer redirectTarget.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != coreDocumentIntakeAuthorityPath || r.Header.Get("X-Contract-Service-Key") != strings.Repeat("k", 32) {
			t.Errorf("unexpected authority request path or credential")
		}
		_, _ = w.Write([]byte(`{"actor_id":"actor","tenant_id":"tenant","org_id":"org","role":"clerk","case_id":"case","operation_id":"op","upload_key":"key","filename":"contract.txt","media_type":"text/plain","body_sha256":"` + digestText + `","action":"create_primary_contract","grants":[]}`))
	}))
	defer server.Close()
	certificate := server.Certificate()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	caFile := t.TempDir() + "/core-ca.pem"
	if err := os.WriteFile(caFile, ca, 0600); err != nil {
		t.Fatal(err)
	}
	client, err := newCoreIntakeAuthorityClient(server.URL, caFile, false, "production", strings.Repeat("k", 32), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if client.http.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("authority transport inherited an HTTP proxy")
	}
	got, err := client.Resolve(context.Background(), "iat_"+strings.Repeat("a", 64), digestText)
	if err != nil || got.ActorID != "actor" || got.BodySHA256 != digestText {
		t.Fatalf("trusted Core callback response=%+v err=%v", got, err)
	}

	redirectSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, nil, redirectTarget.URL, http.StatusTemporaryRedirect)
	}))
	defer redirectSource.Close()
	redirectClient, err := newCoreIntakeAuthorityClient(redirectSource.URL, "", true, "test", strings.Repeat("k", 32), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := redirectClient.Resolve(context.Background(), "iat_"+strings.Repeat("a", 64), digestText); err == nil || redirected.Load() {
		t.Fatalf("callback followed a redirect or accepted redirect response; err=%v followed=%t", err, redirected.Load())
	}
}
