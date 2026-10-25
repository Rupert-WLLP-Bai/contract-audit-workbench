package httpapi_test

import (
	"context"
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
	attempt := preparing["execution"].(map[string]any)["attempt"].(map[string]any)
	if attempt["history_kind"] != "preparing" || attempt["input_hash"] != nil {
		t.Fatalf("unfinished OCR was advertised as a frozen input: %+v", attempt)
	}
	status, preparingCase := api.do(t, http.MethodGet, base, nil)
	if status != http.StatusOK || preparingCase["revision_pending"] != false {
		t.Fatalf("unchanged pending inputs were advertised as a material revision: %d %+v", status, preparingCase["revision_pending"])
	}
	parser.release <- struct{}{}
	finished := api.waitReview(t, path)
	attempt = finished["execution"].(map[string]any)["attempt"].(map[string]any)
	if attempt["history_kind"] != "frozen" || attempt["input_hash"] == nil || len(attempt["parsed_document_ids"].([]any)) != 2 {
		t.Fatalf("completed preparation did not freeze exact references: %+v", attempt)
	}
	if finished["run"].(map[string]any)["state"] != "partial" || len(finished["findings"].([]any)) != 1 || parser.calls.Load() != 1 {
		t.Fatalf("model failure lost rules or repeated OCR: state=%v calls=%d", finished["run"], parser.calls.Load())
	}
}
