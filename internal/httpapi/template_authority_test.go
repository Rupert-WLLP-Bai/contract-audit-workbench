// File metadata (manifest mirror):
// Purpose: Verifies the Document-to-Core template authority rejects untrusted service credentials, client-supplied actors, malformed identities and oversized bodies before accessing storage.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDocumentTemplateAuthorityRejectsUntrustedRequests(t *testing.T) {
	credential := strings.Repeat("t", 40)
	handler := documentTemplatesHandler(nil, credential)
	for _, tc := range []struct {
		name, body string
		auth       []string
		want       int
	}{
		{"missing service credential", `{}`, nil, http.StatusUnauthorized},
		{"wrong service credential", `{}`, []string{"Bearer wrong"}, http.StatusUnauthorized},
		{"duplicate credential", `{}`, []string{"Bearer " + credential, "Bearer " + credential}, http.StatusUnauthorized},
		{"client supplied actor", `{"case_id":"case-1","actor_id":"admin"}`, []string{"Bearer " + credential}, http.StatusBadRequest},
		{"missing case", `{}`, []string{"Bearer " + credential}, http.StatusBadRequest},
		{"partial template identity", `{"case_id":"case-1","id":"template-1"}`, []string{"Bearer " + credential}, http.StatusBadRequest},
		{"trailing body", `{"case_id":"case-1"} {}`, []string{"Bearer " + credential}, http.StatusBadRequest},
		{"oversized body", `{"case_id":"` + strings.Repeat("x", 16384) + `"}`, []string{"Bearer " + credential}, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/internal/v1/document/templates", strings.NewReader(tc.body))
			for _, auth := range tc.auth {
				request.Header.Add("Authorization", auth)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("status = %d, want %d", response.Code, tc.want)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("template authority response must not be cached")
			}
		})
	}
}
