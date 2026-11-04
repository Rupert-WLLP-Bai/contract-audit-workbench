// File metadata (manifest mirror):
// Purpose: Exercises the `httpapi_test` package behavior through TestPendingImageIsAcceptedBeforeOCRAndDetailsDoNotParse; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-04
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"contractaudit/internal/model"
)

type heldImageParser struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (*heldImageParser) Name() string            { return "synthetic-held-ocr" }
func (*heldImageParser) ConfigurationID() string { return "synthetic-held-ocr/v1" }
func (*heldImageParser) Warranty(context.Context, []model.Material) (model.Warranty, error) {
	return model.Warranty{}, model.ErrUnusable
}
func (m *heldImageParser) Transcribe(ctx context.Context, _ string, _ []byte) (string, error) {
	m.calls.Add(1)
	m.entered <- struct{}{}
	select {
	case <-m.release:
		return "采购文件要求质保36个月。", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestPendingImageIsAcceptedBeforeOCRAndDetailsDoNotParse(t *testing.T) {
	parser := &heldImageParser{entered: make(chan struct{}, 1), release: make(chan struct{})}
	defer close(parser.release)
	api := newAPIWith(t, parser, nil)
	api.client.Timeout = 5 * time.Second
	api.login(t, "demo-admin", "admin-demo")
	status, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{"title": "异步图片冻结", "contract_type": "procurement_ict"})
	if status != http.StatusCreated {
		t.Fatalf("create %d: %v", status, created)
	}
	base := "/api/cases/" + created["id"].(string)
	imageID := api.upload(t, created["id"].(string), "procurement", "purchase.png", "image/png", model.SampleWarrantyPNG)
	api.upload(t, created["id"].(string), "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。"))
	status, detail := api.do(t, http.MethodGet, base, nil)
	if status != http.StatusOK || parser.calls.Load() != 0 {
		t.Fatalf("details triggered OCR: status=%d calls=%d", status, parser.calls.Load())
	}
	imageFound := false
	for _, raw := range detail["documents"].([]any) {
		doc := raw.(map[string]any)
		if doc["id"] == imageID {
			imageFound = true
		}
		if doc["id"] == imageID && (doc["parse_status"] != "pending" || doc["parsed_document"] != nil || doc["parse_error"] != nil) {
			t.Fatalf("unparsed image was treated as failure or legacy: %+v", doc)
		}
	}
	if !imageFound {
		t.Fatal("case details omitted the uploaded pending image")
	}
	// send does not poll completion: the response must arrive while OCR is held.
	status, accepted := api.send(t, http.MethodPost, base+"/reviews", map[string]any{}, "")
	if status != http.StatusCreated {
		t.Fatalf("acceptance waited for image parsing or failed: %d %+v", status, accepted)
	}
	select {
	case <-parser.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("accepted attempt did not enter background OCR")
	}
	path := base + "/reviews/" + accepted["id"].(string)
	status, preparing := api.do(t, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("preparing workspace %d: %+v", status, preparing)
	}
	attempt := preparing["review_attempts"].(map[string]any)["attempt"].(map[string]any)
	preparingDiagnostics := api.reviewDiagnostics(t, path)
	preparingDiagnosticAttempt := preparingDiagnostics["execution"].(map[string]any)["attempt"].(map[string]any)
	if attempt["history_kind"] != "preparing" || preparingDiagnosticAttempt["input_hash"] != nil {
		t.Fatalf("unfinished OCR was advertised as a frozen input: attempt=%+v diagnostics=%+v", attempt, preparingDiagnosticAttempt)
	}
	status, preparingCase := api.do(t, http.MethodGet, base, nil)
	if status != http.StatusOK || preparingCase["revision_pending"] != false {
		t.Fatalf("unchanged pending inputs were advertised as a material revision: %d %+v", status, preparingCase["revision_pending"])
	}
	parser.release <- struct{}{}
	finished := api.waitReview(t, path)
	attempt = finished["review_attempts"].(map[string]any)["attempt"].(map[string]any)
	finishedDiagnostics := api.reviewDiagnostics(t, path)
	finishedDiagnosticAttempt := finishedDiagnostics["execution"].(map[string]any)["attempt"].(map[string]any)
	if attempt["history_kind"] != "frozen" || finishedDiagnosticAttempt["input_hash"] == nil || len(attempt["parsed_document_ids"].([]any)) != 2 {
		t.Fatalf("completed preparation did not freeze exact references: attempt=%+v diagnostics=%+v", attempt, finishedDiagnosticAttempt)
	}
	if finished["run"].(map[string]any)["state"] != "partial" || len(finished["findings"].([]any)) != 1 || parser.calls.Load() != 1 {
		t.Fatalf("model failure lost rules or repeated OCR: state=%v calls=%d", finished["run"], parser.calls.Load())
	}
}

func TestCancelledPreparingReadmissionHTTP(t *testing.T) {
	parser := &heldImageParser{entered: make(chan struct{}, 2), release: make(chan struct{})}
	defer close(parser.release)
	api := newAPIWith(t, parser, nil)
	api.client.Timeout = 5 * time.Second
	api.login(t, "demo-admin", "admin-demo")
	status, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{"title": "取消后重新受理", "contract_type": "procurement_ict"})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, created)
	}
	caseID := created["id"].(string)
	base := "/api/cases/" + caseID
	api.upload(t, caseID, "procurement", "synthetic.png", "image/png", model.SampleWarrantyPNG)
	api.upload(t, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。"))
	status, first := api.send(t, http.MethodPost, base+"/reviews", map[string]any{}, "original-http")
	if status != http.StatusCreated {
		t.Fatalf("start: %d %v", status, first)
	}
	select {
	case <-parser.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("synthetic parser was not entered")
	}
	runID := first["id"].(string)
	status, workspace := api.do(t, http.MethodGet, base+"/reviews/"+runID, nil)
	if status != http.StatusOK {
		t.Fatalf("workspace: %d %v", status, workspace)
	}
	attempt := workspace["review_attempts"].(map[string]any)["attempt"].(map[string]any)
	attemptID := attempt["id"].(string)
	if attempt["history_kind"] != "preparing" {
		t.Fatalf("not preparing: %v", attempt)
	}
	status, _ = api.send(t, http.MethodPost, base+"/reviews/"+runID+"/cancel", map[string]any{}, "")
	if status != http.StatusOK {
		t.Fatalf("cancel: %d", status)
	}
	status, workspace = api.do(t, http.MethodGet, base+"/reviews/"+runID, nil)
	if status != http.StatusOK {
		t.Fatalf("cancelled workspace: %d %v", status, workspace)
	}
	attempt = workspace["review_attempts"].(map[string]any)["attempt"].(map[string]any)
	outcome := attempt["outcome"].(map[string]any)
	if attempt["history_kind"] != "preparing" || outcome["reason"] != "cancelled_by_user" || outcome["agent_stage"] != "unknown" {
		t.Fatalf("cancelled preparation projection: %v", attempt)
	}
	status, old := api.send(t, http.MethodPost, base+"/reviews", map[string]any{}, "original-http")
	if status != http.StatusCreated || old["id"] != runID || old["attempt_id"] != attemptID {
		t.Fatalf("original replay: %d %v", status, old)
	}
	body := map[string]any{"readmit_cancelled": map[string]any{"run_id": runID, "attempt_id": attemptID}}
	status, _ = api.send(t, http.MethodPost, base+"/reviews", body, "original-http")
	if status != http.StatusConflict {
		t.Fatalf("same-key changed command: %d", status)
	}
	status, _ = api.send(t, http.MethodPost, base+"/reviews", body, "")
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("unkeyed readmission: %d", status)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, api.base+base+"/reviews", bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", api.base)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "stale-context")
	api.addSessionProof(request)
	request.Header.Set("X-Context-Version", "999")
	response, err := api.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale session context readmission: %d", response.StatusCode)
	}
	status, next := api.send(t, http.MethodPost, base+"/reviews", body, "new-http")
	if status != http.StatusCreated || next["id"] == runID || next["attempt_id"] == attemptID {
		t.Fatalf("new command: %d %v", status, next)
	}
	status, replay := api.send(t, http.MethodPost, base+"/reviews", body, "new-http")
	if status != http.StatusCreated || replay["id"] != next["id"] || replay["attempt_id"] != next["attempt_id"] {
		t.Fatalf("new replay: %d %v", status, replay)
	}
}
