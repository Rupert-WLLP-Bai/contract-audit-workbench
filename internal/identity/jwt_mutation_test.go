package identity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func jwksServer(t *testing.T, document jwksDocument) (*Manager, string) {
	t.Helper()
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal jwks: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return &Manager{client: server.Client()}, server.URL
}

// fetchJWKS rejects an empty key set and more than 4096 keys, and accepts
// exactly 4096.
func TestFetchJWKSBounds(t *testing.T) {
	empty, emptyURL := jwksServer(t, jwksDocument{Keys: []jwk{}})
	if _, err := empty.fetchJWKS(context.Background(), emptyURL); err == nil {
		t.Fatal("empty key set accepted")
	}

	tooMany := make([]jwk, 4097)
	for index := range tooMany {
		tooMany[index] = jwk{KeyType: "RSA", KeyID: "key"}
	}
	oversized, oversizedURL := jwksServer(t, jwksDocument{Keys: tooMany})
	if _, err := oversized.fetchJWKS(context.Background(), oversizedURL); err == nil {
		t.Fatal("4097 keys accepted")
	}

	exact := make([]jwk, 4096)
	for index := range exact {
		exact[index] = jwk{KeyType: "RSA", KeyID: "key"}
	}
	document, documentURL := jwksServer(t, jwksDocument{Keys: exact})
	if _, err := document.fetchJWKS(context.Background(), documentURL); err != nil {
		t.Fatalf("4096 keys rejected: %v", err)
	}
}

func TestDecodeAudienceEdges(t *testing.T) {
	if _, err := decodeAudience(nil); err == nil || err.Error() != "missing audience" {
		t.Fatalf("missing audience error = %v", err)
	}
	if _, err := decodeAudience([]byte(" ")); err == nil || err.Error() != "invalid audience" {
		t.Fatalf("one-byte audience error = %v", err)
	}
	if _, err := decodeAudience([]byte("[]")); err == nil || err.Error() != "invalid audience" {
		t.Fatalf("empty audience array error = %v", err)
	}
	if audience, err := decodeAudience([]byte(`"client"`)); err != nil || len(audience) != 1 || audience[0] != "client" {
		t.Fatalf("single audience = %v %v", audience, err)
	}
	if audience, err := decodeAudience([]byte(`["a","b"]`)); err != nil || len(audience) != 2 {
		t.Fatalf("multiple audience = %v %v", audience, err)
	}
}

// decodeNumericDate returns zero on every error path and rejects underflow
// values that ParseFloat reports as a range error.
func TestDecodeNumericDateEdges(t *testing.T) {
	if value, err := decodeNumericDate(nil); err == nil || value != 0 {
		t.Fatalf("missing date = %v %v", value, err)
	}
	if _, err := decodeNumericDate([]byte(" ")); !errors.Is(err, io.EOF) {
		t.Fatalf("one-byte date error = %v, want EOF", err)
	}
	if value, err := decodeNumericDate([]byte("abc")); err == nil || value != 0 {
		t.Fatalf("non-numeric date = %v %v", value, err)
	}
	if _, err := decodeNumericDate([]byte("1e1000")); err == nil {
		t.Fatal("out-of-range numeric date accepted")
	}
	if value, err := decodeNumericDate([]byte("123.5")); err != nil || value != 123.5 {
		t.Fatalf("valid date = %v %v", value, err)
	}
}

// Identity checks are inclusive at 1024 subject bytes and reject each
// independently invalid field.
func TestValidTokenIdentityBoundaries(t *testing.T) {
	base := idTokenClaims{Issuer: "issuer", Subject: "subject", Audience: []string{"client"}, Nonce: "nonce"}
	if !validTokenIdentity(base, "issuer", "client") {
		t.Fatal("valid identity rejected")
	}
	cases := []struct {
		name   string
		mutate func(*idTokenClaims)
	}{
		{"issuer mismatch", func(c *idTokenClaims) { c.Issuer = "other" }},
		{"empty subject", func(c *idTokenClaims) { c.Subject = "" }},
		{"subject 1025 bytes", func(c *idTokenClaims) { c.Subject = strings.Repeat("s", 1025) }},
		{"empty nonce", func(c *idTokenClaims) { c.Nonce = "" }},
		{"authorized party mismatch", func(c *idTokenClaims) { c.AuthorizedParty = "other" }},
		{"audience mismatch", func(c *idTokenClaims) { c.Audience = []string{"other"} }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			claims := base
			test.mutate(&claims)
			if validTokenIdentity(claims, "issuer", "client") {
				t.Fatalf("invalid identity accepted: %+v", claims)
			}
		})
	}
	exact := base
	exact.Subject = strings.Repeat("s", 1024)
	if !validTokenIdentity(exact, "issuer", "client") {
		t.Fatal("1024-byte subject rejected")
	}
	withParty := base
	withParty.AuthorizedParty = "client"
	if !validTokenIdentity(withParty, "issuer", "client") {
		t.Fatal("matching authorized party rejected")
	}
}

// Time checks are exact at the boundaries: expiry at now is invalid, issued-at
// and not-before at now+60 are still valid, and issued-at must be positive.
func TestValidTokenTimesBoundaries(t *testing.T) {
	now := time.Unix(1000, 0)
	base := idTokenClaims{ExpiresAt: 2000, IssuedAt: 900}
	if !validTokenTimes(base, now) {
		t.Fatal("valid times rejected")
	}
	cases := []struct {
		name  string
		times idTokenClaims
		want  bool
	}{
		{"expiry at now", idTokenClaims{ExpiresAt: 1000, IssuedAt: 900}, false},
		{"issued-at at now+60", idTokenClaims{ExpiresAt: 2000, IssuedAt: 1060}, true},
		{"issued-at at one", idTokenClaims{ExpiresAt: 2000, IssuedAt: 1}, true},
		{"issued-at at epoch", idTokenClaims{ExpiresAt: 2000, IssuedAt: 0}, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := validTokenTimes(test.times, now); got != test.want {
				t.Fatalf("validTokenTimes(%+v) = %v, want %v", test.times, got, test.want)
			}
		})
	}
	notBefore := func(value float64) *float64 { return &value }
	if !validTokenTimes(idTokenClaims{ExpiresAt: 2000, IssuedAt: 900, NotBefore: notBefore(1060)}, now) {
		t.Fatal("not-before at now+60 rejected")
	}
	if validTokenTimes(idTokenClaims{ExpiresAt: 2000, IssuedAt: 900, NotBefore: notBefore(1061)}, now) {
		t.Fatal("not-before beyond now+60 accepted")
	}
}

func encodedModulus(size int) string {
	modulus := make([]byte, size)
	modulus[0] = 0x80
	return base64.RawURLEncoding.EncodeToString(modulus)
}

// Modulus encodings are accepted from 256 through 1024 bytes and the modulus
// bit length must stay within 2048..8192 inclusive.
func TestRSAPublicKeyBounds(t *testing.T) {
	const exponent = "AQAB"
	cases := []struct {
		name    string
		modulus string
		wantErr bool
	}{
		{"256-byte modulus", encodedModulus(256), false},
		{"1024-byte modulus", encodedModulus(1024), false},
		{"1025-byte modulus", func() string {
			modulus := make([]byte, 1025)
			modulus[1] = 0x80
			return base64.RawURLEncoding.EncodeToString(modulus)
		}(), true},
		{"invalid base64 after 300 bytes", encodedModulus(300) + "!", true},
		{"low bit length", func() string {
			modulus := make([]byte, 256)
			modulus[255] = 0x01
			return base64.RawURLEncoding.EncodeToString(modulus)
		}(), true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			key, err := rsaPublicKey(&jwk{Modulus: test.modulus, Exponent: exponent})
			if test.wantErr {
				if err == nil {
					t.Fatalf("invalid modulus accepted: %+v", key)
				}
				return
			}
			if err != nil || key == nil || key.E != 65537 {
				t.Fatalf("valid modulus rejected: %+v %v", key, err)
			}
		})
	}
}

// Exponent encodings are 1 to 4 bytes, odd, and at least three; every error
// path returns zero.
func TestRSAExponentBounds(t *testing.T) {
	encode := func(bytes ...byte) string { return base64.RawURLEncoding.EncodeToString(bytes) }
	cases := []struct {
		name    string
		encoded string
		want    int
		wantErr bool
	}{
		{"standard exponent", "AQAB", 65537, false},
		{"single byte three", encode(0x03), 3, false},
		{"four byte exponent", encode(0x00, 0x00, 0x01, 0x01), 257, false},
		{"empty", "", 0, true},
		{"invalid base64 after data", "AQAB!", 0, true},
		{"five bytes", encode(0x01, 0x00, 0x01, 0x00, 0x01), 0, true},
		{"exponent one", encode(0x01), 0, true},
		{"even exponent", encode(0x04), 0, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value, err := rsaExponent(test.encoded)
			if test.wantErr {
				if err == nil {
					t.Fatalf("invalid exponent accepted: %d", value)
				}
				if value != 0 {
					t.Fatalf("error path returned %d, want 0", value)
				}
				return
			}
			if err != nil || value != test.want {
				t.Fatalf("exponent = %d %v, want %d", value, err, test.want)
			}
		})
	}
}
