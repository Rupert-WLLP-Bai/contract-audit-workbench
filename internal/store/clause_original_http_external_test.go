// File metadata (manifest mirror):
// Purpose: Checks the real Document service handler with paired service credentials, current user session, and refusal on other routes.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/documentclient"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/store"
)

func TestClauseOriginalProductionDocumentService(t *testing.T) {
	fixture := store.NewClauseOriginalHTTPFixture(t)
	gatewayKey, documentKey := strings.Repeat("g", 40), strings.Repeat("d", 40)
	handler, err := httpapi.ServiceHandler(fixture.Document, httpapi.Options{Durable: true, SessionAuthorityResolver: fixture.Core, ClauseOriginalCredential: documentKey}, "document", gatewayKey)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	reader, err := documentclient.NewClauseOriginalReader(server.URL, documentKey, time.Second*10)
	if err != nil {
		t.Fatal(err)
	}
	body, err := reader.ReadClauseOriginal(context.Background(), fixture.CaseID, fixture.DocumentID, fixture.Token)
	if err != nil || !bytes.Equal(body, fixture.Body) {
		t.Fatalf("authorized original mismatch: %v", err)
	}
	if _, err := reader.ReadClauseOriginal(context.Background(), fixture.CaseID, fixture.DocumentID, "invalid-session"); err == nil {
		t.Fatal("invalid user session accepted")
	}
	originalPath := "/api/cases/" + fixture.CaseID + "/documents/" + fixture.DocumentID + "/original"
	for _, tc := range []struct{ name, method, path, key string }{
		{"missing service key", http.MethodGet, originalPath, ""},
		{"wrong service key", http.MethodGet, originalPath, strings.Repeat("x", 40)},
		{"key on upload route", http.MethodPost, "/api/cases/" + fixture.CaseID + "/documents", documentKey},
		{"key on HEAD original", http.MethodHead, originalPath, documentKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, server.URL+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.AddCookie(&http.Cookie{Name: "session", Value: fixture.Token})
			if tc.key != "" {
				req.Header.Set("X-Contract-Service-Key", tc.key)
			}
			response, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", response.StatusCode)
			}
		})
	}
}
