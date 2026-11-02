// File metadata (manifest mirror):
// Purpose: Exercises the `bootstrap` package behavior through TestConfiguredObjectStoreUsesPrimaryThenLegacyCredentials; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-30
// Updated: 2026-10-30
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const envFallbackTestSecret = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"

func TestConfiguredObjectStoreUsesPrimaryThenLegacyCredentials(t *testing.T) {
	for _, test := range []struct {
		name          string
		primaryAccess string
		legacyAccess  string
		primarySecret string
		legacySecret  string
		wantAccess    string
	}{
		{
			name:          "configured primary credentials take precedence",
			primaryAccess: "PRIMARYACCESS",
			legacyAccess:  "LEGACYACCESS",
			primarySecret: envFallbackTestSecret,
			legacySecret:  "LEGACYSECRETEXAMPLEKEY",
			wantAccess:    "PRIMARYACCESS",
		},
		{
			name:         "empty primary uses legacy credentials",
			legacyAccess: "LEGACYACCESS",
			legacySecret: "LEGACYSECRETEXAMPLEKEY",
			wantAccess:   "LEGACYACCESS",
		},
		{
			name:          "whitespace primary uses legacy credentials",
			primaryAccess: "   ",
			legacyAccess:  "LEGACYACCESS",
			primarySecret: "   ",
			legacySecret:  "LEGACYSECRETEXAMPLEKEY",
			wantAccess:    "LEGACYACCESS",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var authHeader string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				authHeader = r.Header.Get("Authorization")
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			for key, value := range map[string]string{
				"S3_ENDPOINT":       server.URL,
				"S3_BUCKET":         "private-bucket",
				"S3_REGION":         "us-east-1",
				"S3_ACCESS_KEY":     test.primaryAccess,
				"S3_SECRET_KEY":     test.primarySecret,
				"RUSTFS_ACCESS_KEY": test.legacyAccess,
				"RUSTFS_SECRET_KEY": test.legacySecret,
			} {
				t.Setenv(key, value)
			}

			objectStore, err := configuredObjectStore()
			if err != nil || objectStore == nil {
				t.Fatalf("configuredObjectStore() = (%v, %v), want a valid store", objectStore, err)
			}
			if err := objectStore.Put(context.Background(), "dedup/env-fallback", []byte("test")); err != nil {
				t.Fatalf("Put() = %v, want success", err)
			}
			if !strings.Contains(authHeader, "Credential="+test.wantAccess+"/") {
				t.Fatalf("request credential = %q, want access key %q", authHeader, test.wantAccess)
			}
		})
	}
}
