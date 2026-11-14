// File metadata (manifest mirror):
// Purpose: Verifies template authority HTTP calls preserve exact request identity and distinguish authorization, unavailable-template and conflict refusals from infrastructure failure.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package operationauthority

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"contractaudit/internal/contracttemplate"
	"contractaudit/internal/fault"
)

func TestTemplateAuthorityHTTPFailuresKeepTheirMeaning(t *testing.T) {
	const credential = "private-document-authority-credential-000000"
	input := contracttemplate.ResolveRequest{SessionDigest: "digest", ContextVersion: "context", CaseID: "case", ID: "template", Version: "version", Digest: "hash"}
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{
		{"authorization denied", http.StatusForbidden, "forbidden"},
		{"template unavailable", http.StatusNotFound, "not_found"},
		{"template changed", http.StatusConflict, "conflict"},
		{"server failure", http.StatusInternalServerError, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/internal/v1/document/templates" || r.Header.Get("Authorization") != "Bearer "+credential {
					t.Errorf("unexpected request method, path or service authorization")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				var got contracttemplate.ResolveRequest
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got != input {
					t.Errorf("template identity not preserved: %+v, %v", got, err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			client, err := NewOperationClient(server.URL, "document", credential, "", "test", true)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.ResolveDocumentTemplates(context.Background(), input)
			if err == nil || calls != 1 {
				t.Fatalf("expected one failed request, calls=%d err=%v", calls, err)
			}
			var problem *fault.Error
			if tc.code == "" {
				if errors.As(err, &problem) || errors.Is(err, ErrDenied) {
					t.Fatalf("server failure misclassified as user refusal: %v", err)
				}
			} else if !errors.As(err, &problem) || problem.Status != tc.status || problem.Code != tc.code {
				t.Fatalf("error = %v, want status %d code %s", err, tc.status, tc.code)
			}
		})
	}
}

func TestTemplateAuthorityRequiresServiceCredential(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewOperationClient(server.URL, "document", "", "", "test", true)
	if err == nil || client != nil || calls != 0 {
		t.Fatalf("missing credential accepted: client=%v calls=%d err=%v", client, calls, err)
	}
}
