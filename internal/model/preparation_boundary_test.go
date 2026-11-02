// File metadata (manifest mirror):
// Purpose: 实现 `TestPreparationNativeModelCannotDispatch` 等具体接口与边界逻辑，供 internal/model 的调用方使用。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"context"
	"net/http"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestPreparationNativeModelCannotDispatch(t *testing.T) {
	calls := 0
	client := New(Config{BaseURL: "http://example.invalid", ChatModel: "chat", OCRModel: "vision"}).WithTransport(roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, nil }))
	messages := []*schema.Message{{Role: schema.System, Content: "system"}, {Role: schema.User, Content: "user"}}
	for _, scope := range []DelegatedScope{{Kind: "execution_prepare", Action: "embedded_prepare"}, {Kind: "document", Action: "embedded_prepare_parse"}, {Kind: "execution", Action: "embedded_review", AdmissionRevision: "original"}, {Kind: "document", Action: "upload_parse", OperationTicket: "original"}, {Kind: "document", Action: "reparse", OperationTicket: "original"}, {Kind: "document", JobID: "job", WorkerID: "worker", Generation: 1}} {
		ctx := WithDelegatedScope(context.Background(), scope)
		if _, err := client.Generate(ctx, messages); err == nil {
			t.Fatal("preparation accepted for general generation")
		}
		if _, err := client.Transcribe(ctx, "image/png", []byte{1}); err == nil {
			t.Fatal("native preparation accepted for unmetered OCR")
		}
		if scope.Kind == "execution_prepare" || scope.Action == "embedded_prepare_parse" {
			if _, err := client.list(ctx); err == nil {
				t.Fatal("preparation accepted for model listing")
			}
		}
	}
	if calls != 0 {
		t.Fatalf("sent %d unauthorized requests", calls)
	}
}
