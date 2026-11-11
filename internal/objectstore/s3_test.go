// File metadata (manifest mirror):
// Purpose: Exercises SDK-backed `objectstore` behavior through path-style signed Put/Get, digest verification, S3 status mapping, size and input boundaries, redaction, redirects, retries, cancellation, and deadlines.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-30
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testAccessKey = "AKIDEXAMPLE"
	testSecretKey = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
	testRegion    = "us-east-1"
	testBucket    = "private-bucket"
)

func testStore(t *testing.T, endpoint string) *S3 {
	t.Helper()
	store, err := New(Config{
		Endpoint: endpoint, Bucket: testBucket, Region: testRegion,
		AccessKey: testAccessKey, SecretKey: testSecretKey,
	})
	if err != nil {
		t.Fatalf("New returned an error for valid config: %v", err)
	}
	return store
}

func TestPutGetUsesSDKAndPreservesObjectBytes(t *testing.T) {
	const key = "tenants/tenant-1/cases/case-1/a b+雪.txt"
	original := []byte("original contract bytes\x00with a trailing byte")
	var stored []byte
	var storedDigest string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
			http.Error(w, "request failure", http.StatusBadRequest)
			return
		}
		if authorization := r.Header.Get("Authorization"); !strings.Contains(authorization, "Credential="+testAccessKey+"/") {
			t.Errorf("request was not authenticated with the configured access key: %q", authorization)
			http.Error(w, "missing credentials", http.StatusForbidden)
			return
		}
		authorization := r.Header.Get("Authorization")
		if !strings.Contains(authorization, "/"+testRegion+"/s3/aws4_request") {
			t.Errorf("credential scope = %q; want region/service scope for %s", authorization, testRegion)
		}
		signedHeaders := signedHeaderNames(authorization)
		if r.Method == http.MethodPut && !strings.Contains(";"+signedHeaders+";", ";x-amz-meta-sha256;") {
			t.Errorf("Put signature did not cover SHA-256 metadata: %q", signedHeaders)
		}
		if r.URL.EscapedPath() != "/gateway%20prefix/"+testBucket+"/tenants/tenant-1/cases/case-1/a%20b%2B%E9%9B%AA.txt" {
			t.Errorf("escaped request path = %q", r.URL.EscapedPath())
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodPut:
			digest := sha256.Sum256(body)
			storedDigest = hex.EncodeToString(digest[:])
			if got := r.Header.Get("X-Amz-Content-Sha256"); got != storedDigest {
				t.Errorf("signed payload digest = %q; want %q", got, storedDigest)
				http.Error(w, "invalid payload digest", http.StatusBadRequest)
				return
			}
			if r.Header.Get("X-Amz-Meta-Sha256") != storedDigest {
				t.Errorf("signed metadata digest = %q; want %q", r.Header.Get("X-Amz-Meta-Sha256"), storedDigest)
				http.Error(w, "invalid metadata", http.StatusBadRequest)
				return
			}
			if r.Header.Get("X-Amz-Sdk-Checksum-Algorithm") != "" || r.Header.Get("X-Amz-Checksum-CRC32") != "" {
				t.Errorf("Put added an unconfigured S3 flexible checksum header")
				http.Error(w, "unexpected checksum", http.StatusBadRequest)
				return
			}
			stored = append(stored[:0], body...)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			w.Header().Set("X-Amz-Meta-Sha256", storedDigest)
			_, _ = w.Write(stored)
		default:
			t.Errorf("unexpected method %q", r.Method)
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	store := testStore(t, server.URL+"/gateway%20prefix")
	if err := store.Put(context.Background(), key, original); err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	got, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("Get returned different bytes: got %q, want %q", got, original)
	}
}

func TestPutSignsPayloadOverTLS(t *testing.T) {
	type observation struct {
		payloadDigest  string
		metadataDigest string
		signedHeaders  string
		body           []byte
	}
	seen := make(chan observation, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
			http.Error(w, "request failure", http.StatusBadRequest)
			return
		}
		seen <- observation{
			payloadDigest:  r.Header.Get("X-Amz-Content-Sha256"),
			metadataDigest: r.Header.Get("X-Amz-Meta-Sha256"),
			signedHeaders:  signedHeaderNames(r.Header.Get("Authorization")),
			body:           body,
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	config := Config{
		Endpoint: server.URL, Bucket: testBucket, Region: testRegion,
		AccessKey: testAccessKey, SecretKey: testSecretKey,
	}
	endpoint, err := parseEndpoint(config.Endpoint)
	if err != nil {
		t.Fatalf("parse TLS endpoint: %v", err)
	}
	httpClient := server.Client()
	httpClient.Timeout = 30 * time.Second
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	store := &S3{
		bucket: config.Bucket,
		client: newSDKClient(config, endpoint, responseMetadataHTTPClient{next: httpClient}),
	}
	original := []byte("original TLS payload")
	if err := store.Put(context.Background(), "tenants/t1/cases/c1/source", original); err != nil {
		t.Fatalf("Put over TLS failed: %v", err)
	}
	got := <-seen
	digest := sha256.Sum256(original)
	wantDigest := hex.EncodeToString(digest[:])
	if !bytes.Equal(got.body, original) || got.payloadDigest != wantDigest || got.metadataDigest != wantDigest {
		t.Fatalf("TLS request body/hash mismatch: body=%q payload=%q metadata=%q", got.body, got.payloadDigest, got.metadataDigest)
	}
	if !strings.Contains(";"+got.signedHeaders+";", ";x-amz-meta-sha256;") {
		t.Fatalf("TLS signature did not cover SHA-256 metadata: %q", got.signedHeaders)
	}
}

func TestGetRejectsTamperedBodyAndMetadata(t *testing.T) {
	original := []byte("the stored original")
	tampered := []byte("a changed original")
	originalDigest := sha256.Sum256(original)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Amz-Meta-Sha256", hex.EncodeToString(originalDigest[:]))
		_, _ = w.Write(tampered)
	}))
	defer server.Close()

	_, err := testStore(t, server.URL).Get(context.Background(), "tenants/t1/cases/c1/source.txt")
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("Get error = %v; want ErrIntegrity", err)
	}
}

func TestGetMapsPublicServiceAndMetadataFailures(t *testing.T) {
	payload := []byte("verified object")
	digest := sha256.Sum256(payload)
	validDigest := hex.EncodeToString(digest[:])
	cases := []struct {
		name     string
		status   int
		metadata []string
		want     error
	}{
		{"not found", http.StatusNotFound, nil, ErrObjectNotFound},
		{"service error", http.StatusForbidden, []string{validDigest}, ErrRequestFailed},
		{"unexpected successful status", http.StatusNoContent, nil, ErrRequestFailed},
		{"missing digest", http.StatusOK, nil, ErrIntegrity},
		{"duplicate digest", http.StatusOK, []string{validDigest, validDigest}, ErrIntegrity},
		{"malformed digest", http.StatusOK, []string{"not-hex"}, ErrIntegrity},
		{"short digest", http.StatusOK, []string{"abcd"}, ErrIntegrity},
		{"wrong digest", http.StatusOK, []string{strings.Repeat("0", sha256.Size*2)}, ErrIntegrity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for _, value := range tc.metadata {
					w.Header().Add("X-Amz-Meta-Sha256", value)
				}
				w.WriteHeader(tc.status)
				if tc.status != http.StatusOK {
					return
				}
				_, _ = w.Write(payload)
			}))
			defer server.Close()
			_, err := testStore(t, server.URL).Get(context.Background(), "tenants/t1/cases/c1/source")
			if !errors.Is(err, tc.want) {
				t.Fatalf("Get error = %v; want %v", err, tc.want)
			}
		})
	}
}

func TestPutRejectsUnexpectedSuccessfulStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	if err := testStore(t, server.URL).Put(context.Background(), "tenants/t1/cases/c1/source", []byte("bytes")); !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("Put error = %v; want ErrRequestFailed", err)
	}
}

func TestPutDoesNotRetryServiceFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	if err := testStore(t, server.URL).Put(context.Background(), "tenants/t1/cases/c1/source", []byte("bytes")); !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("Put error = %v; want ErrRequestFailed", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("Put made %d attempts; want one configured SDK attempt", requests.Load())
	}
}

func signedHeaderNames(authorization string) string {
	for _, field := range strings.Split(authorization, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if ok && name == "SignedHeaders" {
			return value
		}
	}
	return ""
}

func TestNewRejectsInvalidConfigurationAndKeysBeforeRequest(t *testing.T) {
	base := Config{Endpoint: "http://127.0.0.1:9000", Bucket: testBucket, Region: testRegion, AccessKey: testAccessKey, SecretKey: testSecretKey}
	invalidConfigs := []struct {
		name string
		edit func(*Config)
	}{
		{"relative endpoint", func(c *Config) { c.Endpoint = "/s3" }},
		{"endpoint credentials", func(c *Config) { c.Endpoint = "http://user:pass@localhost:9000" }},
		{"endpoint query", func(c *Config) { c.Endpoint += "?prefix=private" }},
		{"endpoint fragment", func(c *Config) { c.Endpoint += "#bucket" }},
		{"empty bucket", func(c *Config) { c.Bucket = "" }},
		{"invalid bucket", func(c *Config) { c.Bucket = "Bad_Bucket" }},
		{"empty region", func(c *Config) { c.Region = "" }},
		{"empty access key", func(c *Config) { c.AccessKey = "" }},
		{"empty secret", func(c *Config) { c.SecretKey = "" }},
	}
	for _, test := range invalidConfigs {
		t.Run(test.name, func(t *testing.T) {
			config := base
			test.edit(&config)
			if _, err := New(config); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("New error = %v; want ErrInvalidConfig", err)
			}
		})
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1) }))
	defer server.Close()
	store := testStore(t, server.URL)
	for _, key := range []string{"", "/absolute", `\\absolute`, "../escape", "tenant/../escape", "tenant//case/file", "tenant/case/.", "tenant/case/file?query", "tenant/case/file#fragment", "tenant/case/file\x7f", "C:/absolute/file", "https://foreign.example/object", "mailto:foreign@example.invalid", strings.Repeat("k", 1025), string([]byte{0xff})} {
		if err := store.Put(context.Background(), key, []byte("bytes")); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Put key %q error = %v; want ErrInvalidKey", key, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid inputs sent %d requests", requests.Load())
	}
}

func TestNewAcceptsPublicConfigurationBoundaries(t *testing.T) {
	base := Config{Endpoint: "https://objects.example.invalid/private%20gateway/", Bucket: "abc", Region: testRegion, AccessKey: testAccessKey, SecretKey: testSecretKey}
	validConfigs := []struct {
		name string
		edit func(*Config)
	}{
		{"minimum bucket", func(c *Config) { c.Bucket = "abc" }},
		{"maximum bucket", func(c *Config) { c.Bucket = strings.Repeat("a", 63) }},
		{"maximum region", func(c *Config) { c.Region = strings.Repeat("a", 64) }},
		{"maximum access key", func(c *Config) { c.AccessKey = strings.Repeat("A", 128) }},
		{"maximum secret key", func(c *Config) { c.SecretKey = strings.Repeat("s", 1024) }},
		{"endpoint with a safe base path", func(c *Config) { c.Endpoint = "HTTPS://objects.example.invalid/gateway/v1/" }},
		{"endpoint with maximum port", func(c *Config) { c.Endpoint = "http://objects.example.invalid:65535" }},
	}
	for _, tc := range validConfigs {
		t.Run(tc.name, func(t *testing.T) {
			config := base
			tc.edit(&config)
			if _, err := New(config); err != nil {
				t.Fatalf("New rejected valid boundary config: %v", err)
			}
		})
	}
}

func TestNewRejectsConfigurationBoundaryViolations(t *testing.T) {
	base := Config{Endpoint: "http://objects.example.invalid", Bucket: testBucket, Region: testRegion, AccessKey: testAccessKey, SecretKey: testSecretKey}
	invalidConfigs := []struct {
		name string
		edit func(*Config)
	}{
		{"short bucket", func(c *Config) { c.Bucket = "ab" }},
		{"long bucket", func(c *Config) { c.Bucket = strings.Repeat("a", 64) }},
		{"dotted bucket", func(c *Config) { c.Bucket = "ab..cd" }},
		{"reserved bucket prefix", func(c *Config) { c.Bucket = "xn--bucket" }},
		{"IP bucket", func(c *Config) { c.Bucket = "192.0.2.1" }},
		{"long region", func(c *Config) { c.Region = strings.Repeat("a", 65) }},
		{"uppercase region", func(c *Config) { c.Region = "US-east-1" }},
		{"long access key", func(c *Config) { c.AccessKey = strings.Repeat("A", 129) }},
		{"invalid access key byte", func(c *Config) { c.AccessKey = "KEY:SUFFIX" }},
		{"long secret key", func(c *Config) { c.SecretKey = strings.Repeat("s", 1025) }},
		{"padded secret key", func(c *Config) { c.SecretKey = " secret " }},
		{"control in secret key", func(c *Config) { c.SecretKey = "secret\nkey" }},
		{"invalid UTF-8 secret key", func(c *Config) { c.SecretKey = string([]byte{0xff}) }},
		{"port zero", func(c *Config) { c.Endpoint = "http://objects.example.invalid:0" }},
		{"port too large", func(c *Config) { c.Endpoint = "http://objects.example.invalid:65536" }},
		{"encoded slash in endpoint path", func(c *Config) { c.Endpoint = "http://objects.example.invalid/a%2fb" }},
		{"encoded backslash in endpoint path", func(c *Config) { c.Endpoint = "http://objects.example.invalid/a%5Cb" }},
		{"empty endpoint segment", func(c *Config) { c.Endpoint = "http://objects.example.invalid/a//b" }},
		{"dot endpoint segment", func(c *Config) { c.Endpoint = "http://objects.example.invalid/a/../b" }},
		{"control endpoint segment", func(c *Config) { c.Endpoint = "http://objects.example.invalid/a/%0a" }},
	}
	for _, tc := range invalidConfigs {
		t.Run(tc.name, func(t *testing.T) {
			config := base
			tc.edit(&config)
			if _, err := New(config); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("New error = %v; want ErrInvalidConfig", err)
			}
		})
	}
}

func TestPutAcceptsColonAfterKeyPathSegment(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	if err := testStore(t, server.URL).Put(context.Background(), "tenant/case:part/source", []byte("bytes")); err != nil {
		t.Fatalf("Put rejected a colon after a path segment: %v", err)
	}
	if want := "/" + testBucket + "/tenant/case%3Apart/source"; gotPath != want {
		t.Fatalf("escaped path = %q; want %q", gotPath, want)
	}
}

func TestPutEnforcesPublicObjectKeyByteLimit(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	store := testStore(t, server.URL)
	if err := store.Put(context.Background(), strings.Repeat("k", 1024), []byte("bytes")); err != nil {
		t.Fatalf("Put rejected a key at the byte limit: %v", err)
	}
	if err := store.Put(context.Background(), strings.Repeat("k", 1025), []byte("bytes")); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("Put over the key byte limit error = %v; want ErrInvalidKey", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("boundary check made %d requests; want only the valid key request", requests.Load())
	}
}

func TestObjectAtLimitRoundTripsWithoutTruncation(t *testing.T) {
	original := make([]byte, MaxObjectBytes)
	original[0], original[len(original)-1] = 0x31, 0x7f
	var stored []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			var err error
			stored, err = io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read object: %v", err)
				http.Error(w, "read error", http.StatusBadRequest)
			}
		case http.MethodGet:
			digest := sha256.Sum256(stored)
			w.Header().Set("X-Amz-Meta-Sha256", hex.EncodeToString(digest[:]))
			_, _ = w.Write(stored)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	store := testStore(t, server.URL)
	const key = "example/source"
	if err := store.Put(context.Background(), key, original); err != nil {
		t.Fatalf("Put at limit: %v", err)
	}
	got, err := store.Get(context.Background(), key)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("round trip at limit: got %d bytes, err=%v", len(got), err)
	}
}

func TestPutRejectsOversizeInputWithoutRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1) }))
	defer server.Close()
	store := testStore(t, server.URL)
	content := make([]byte, MaxObjectBytes+1)
	if err := store.Put(context.Background(), "tenants/t1/cases/c1/source", content); !errors.Is(err, ErrObjectTooLarge) {
		t.Fatalf("Put error = %v; want ErrObjectTooLarge", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("oversize input sent %d requests", requests.Load())
	}
}

func TestGetRejectsOversizeResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, MaxObjectBytes+1))
	}))
	defer server.Close()
	_, err := testStore(t, server.URL).Get(context.Background(), "tenants/t1/cases/c1/source")
	if !errors.Is(err, ErrObjectTooLarge) {
		t.Fatalf("Get error = %v; want ErrObjectTooLarge", err)
	}
}

func TestServiceErrorsAreRedactedAndRedirectsAreNotFollowed(t *testing.T) {
	const supplierError = "upstream secret diagnostic"
	failureServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, supplierError+" "+testAccessKey+" "+testSecretKey)
	}))
	defer failureServer.Close()
	err := testStore(t, failureServer.URL).Put(context.Background(), "tenants/t1/cases/c1/source", []byte("bytes"))
	if !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("Put error = %v; want ErrRequestFailed", err)
	}
	for _, secret := range []string{supplierError, testAccessKey, testSecretKey} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("returned error leaked %q: %v", secret, err)
		}
	}

	var redirectedRequests atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectedRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer redirectTarget.Close()
	redirectSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer redirectSource.Close()
	err = testStore(t, redirectSource.URL).Put(context.Background(), "tenants/t1/cases/c1/source", []byte("bytes"))
	if !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("redirected Put error = %v; want ErrRequestFailed", err)
	}
	if redirectedRequests.Load() != 0 {
		t.Fatalf("client followed redirect to a second origin")
	}
}

func TestPutHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := testStore(t, server.URL)
	result := make(chan error, 1)
	go func() {
		result <- store.Put(ctx, "tenants/t1/cases/c1/source", []byte("bytes"))
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not receive the request")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Put error = %v; want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Put did not return after context cancellation")
	}
}

func TestGetHonorsContextDeadline(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	store := testStore(t, server.URL)
	result := make(chan error, 1)
	go func() {
		_, err := store.Get(ctx, "tenants/t1/cases/c1/source")
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not receive the request")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Get error = %v; want context.DeadlineExceeded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Get did not return after context deadline")
	}
}
