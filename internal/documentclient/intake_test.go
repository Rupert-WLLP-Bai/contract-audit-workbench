// File metadata (manifest mirror):
// Purpose: 实现 `TestIntakeUploaderSendsOpaqueTicketWithoutIdentityClaims` 等具体接口与边界逻辑，供 internal/documentclient 的调用方使用。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package documentclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"contractaudit/internal/store"
)

func TestIntakeUploaderSendsOpaqueTicketWithoutIdentityClaims(t *testing.T) {
	const credential = "document-intake-test-private-key-0001"
	const ticket = "iat_" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != intakeUploadEndpoint || r.Header.Get("X-Contract-Service-Key") != credential {
			t.Errorf("intake request path or pair credential is wrong")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart upload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for _, field := range []string{"actor_id", "tenant_id", "org_id", "idempotency_key"} {
			if _, exists := r.MultipartForm.Value[field]; exists {
				t.Errorf("caller-provided identity field %q was transmitted", field)
			}
		}
		if r.FormValue("authority_ticket") != ticket || r.FormValue("case_id") != "case-1" {
			t.Errorf("ticket/case selectors = %q/%q", r.FormValue("authority_ticket"), r.FormValue("case_id"))
		}
		files := r.MultipartForm.File["file"]
		if len(files) != 1 {
			t.Errorf("file parts=%d", len(files))
		} else if file, err := files[0].Open(); err != nil {
			t.Error(err)
		} else {
			data, readErr := io.ReadAll(file)
			_ = file.Close()
			if readErr != nil || string(data) != "frozen bytes" {
				t.Errorf("file bytes=%q err=%v", data, readErr)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"doc-1","material_id":"mat-1","version":1}`)
	}))
	defer server.Close()
	uploader, err := NewIntakeUploader(server.URL, credential, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request := store.PrimaryContractUploadRequest{
		AuthorityTicket: ticket, Filename: "contract.txt", MediaType: "text/plain", Body: []byte("frozen bytes"),
		IdempotencyKey: "should-remain-private",
		ActorID:        "should-not-transmit", TenantID: "tenant-claimed", OrgID: "org-claimed",
	}
	result, err := uploader.UploadPrimaryContract(context.Background(), "case-1", request)
	if err != nil || result.DocumentID != "doc-1" || result.MaterialID != "mat-1" || result.Version != 1 {
		t.Fatalf("upload result=%+v err=%v", result, err)
	}
}
