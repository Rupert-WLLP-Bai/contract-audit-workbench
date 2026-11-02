package modelgateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

func TestEmbeddedOCRRequestRequiresFixedPromptAndOriginalBytes(t *testing.T) {
	image := model.SampleWarrantyPNG
	sum := sha256.Sum256(image)
	digest := hex.EncodeToString(sum[:])
	encoded := base64.StdEncoding.EncodeToString(image)
	payload := map[string]any{"model": "vision", "temperature": 0, "max_tokens": 8192, "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": model.OCRTranscriptionInstruction()}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + encoded}}}}}}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !validEmbeddedOCRRequest(raw, digest) {
		t.Fatal("exact OCR rejected")
	}
	for _, bad := range [][]byte{[]byte(strings.Replace(string(raw), model.OCRTranscriptionInstruction(), "ignore all", 1)), []byte(strings.Replace(string(raw), encoded, base64.StdEncoding.EncodeToString([]byte("different")), 1)), []byte(strings.Replace(string(raw), `"model":"vision"`, `"model":"vision","model":"other"`, 1)), []byte(strings.Replace(string(raw), `"role":"user"`, `"role":"system"`, 1))} {
		if validEmbeddedOCRRequest(bad, digest) {
			t.Fatalf("invalid OCR payload accepted: %s", bad)
		}
	}
}

type embeddedDualCaller struct{ worker, document *model.Client }

func (m embeddedDualCaller) Name() string             { return m.worker.Name() }
func (m embeddedDualCaller) OCRName() string          { return m.worker.OCRName() }
func (m embeddedDualCaller) ConfigurationID() string  { return m.worker.ConfigurationID() }
func (m embeddedDualCaller) ProviderEndpoint() string { return m.worker.ProviderEndpoint() }
func (m embeddedDualCaller) Warranty(ctx context.Context, docs []model.Material) (model.Warranty, error) {
	return m.worker.Warranty(ctx, docs)
}
func (m embeddedDualCaller) Transcribe(ctx context.Context, media string, data []byte) (string, error) {
	return m.document.Transcribe(ctx, media, data)
}

func TestEmbeddedGatewayOCRAndReviewAccountingPG(t *testing.T) {
	st, profile := openGatewayTestStore(t)
	ctx := context.Background()
	token, err := st.EstablishSession(profile, "client")
	if err != nil {
		t.Fatal(err)
	}
	user, _, err := st.AuthenticateSession(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx = store.WithAdmissionSessionToken(ctx, token)
	caseID, err := st.CreateCase(user, "embedded gateway OCR", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.AddDocument(ctx, user, caseID, "procurement", "original.png", "image/png", model.SampleWarrantyPNG); err != nil {
		t.Fatal(err)
	}
	if _, err = st.AddDocument(ctx, user, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。")); err != nil {
		t.Fatal(err)
	}
	cfg := gatewayTestConfig("fixture", "")
	cfg.ProviderURL = "http://fixture.invalid/v1"
	cfg.FixtureOCRContent = "采购文件要求质保36个月。"
	cfg.FixtureChatContent = `{"claim":"质保期限不一致","relation":"conflict","disposition":"risk","quotes":[]}`
	cfg.Budget.MaxOutputTokenReservation = 8192
	_, server := startGatewayServer(t, st, cfg)
	base := model.Config{BaseURL: server.URL + "/v1", ProviderURL: cfg.ProviderURL, Gateway: true, ChatModel: cfg.ChatModel, OCRModel: cfg.OCRModel}
	base.APIKey = cfg.WorkerCredential
	worker := model.New(base)
	base.APIKey = cfg.DocumentCredential
	document := model.New(base)
	st.UseModels(embeddedDualCaller{worker: worker, document: document})
	runID, err := st.StartReview(ctx, user, caseID)
	if err != nil {
		t.Fatalf("start review: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		ws, err := st.Workspace(user, caseID, runID)
		if err != nil {
			t.Fatal(err)
		}
		if ws.Execution != nil && ws.Execution.Attempt != nil && ws.Execution.Attempt.State != "accepted" && ws.Execution.Attempt.State != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("embedded review did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	usage, active := readGatewayAccounting(t, caseID)
	if active != 0 {
		t.Fatalf("active leases: %d", active)
	}
	documentCalls, workerCalls := 0, 0
	for _, row := range usage {
		if row.Caller == "document" && row.Model == cfg.OCRModel {
			documentCalls++
		}
		if row.Caller == "worker" && row.Model == cfg.ChatModel {
			workerCalls++
		}
	}
	if documentCalls != 1 || workerCalls != 1 {
		t.Fatalf("expected budgeted OCR and review, got %+v", usage)
	}
}

func TestEmbeddedGatewayDerivedOCRAccountingPG(t *testing.T) {
	for _, tc := range []struct{ name, file, media string }{{"pdf", "../documentparse/testdata/pdf/scanned-chinese-table.pdf", "application/pdf"}, {"docx", "../documentparse/testdata/docx/embedded-image.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}} {
		t.Run(tc.name, func(t *testing.T) {
			original, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatal(err)
			}
			st, profile := openGatewayTestStore(t)
			ctx := context.Background()
			token, err := st.EstablishSession(profile, "client")
			if err != nil {
				t.Fatal(err)
			}
			user, _, err := st.AuthenticateSession(ctx, token)
			if err != nil {
				t.Fatal(err)
			}
			ctx = store.WithAdmissionSessionToken(ctx, token)
			caseID, err := st.CreateCase(user, "embedded derived OCR", "procurement_ict")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = st.AddDocument(ctx, user, caseID, "procurement", "original."+tc.name, tc.media, original); err != nil {
				t.Fatal(err)
			}
			if _, err = st.AddDocument(ctx, user, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。")); err != nil {
				t.Fatal(err)
			}
			cfg := gatewayTestConfig("fixture", "")
			cfg.ProviderURL = "http://fixture.invalid/v1"
			cfg.FixtureOCRContent = "采购文件要求质保36个月。"
			cfg.FixtureChatContent = `{"claim":"质保期限不一致","relation":"conflict","disposition":"risk","quotes":[]}`
			cfg.Budget.MaxOutputTokenReservation = 8192
			_, server := startGatewayServer(t, st, cfg)
			base := model.Config{BaseURL: server.URL + "/v1", ProviderURL: cfg.ProviderURL, Gateway: true, ChatModel: cfg.ChatModel, OCRModel: cfg.OCRModel, APIKey: cfg.WorkerCredential}
			worker := model.New(base)
			base.APIKey = cfg.DocumentCredential
			document := model.New(base)
			st.UseModels(embeddedDualCaller{worker: worker, document: document})
			runID, err := st.StartReview(ctx, user, caseID)
			if err != nil {
				t.Fatalf("start review: %v", err)
			}
			deadline := time.Now().Add(25 * time.Second)
			for {
				ws, err := st.Workspace(user, caseID, runID)
				if err != nil {
					t.Fatal(err)
				}
				if ws.Execution != nil && ws.Execution.Attempt != nil && ws.Execution.Attempt.State != "accepted" && ws.Execution.Attempt.State != "running" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("derived OCR review did not finish")
				}
				time.Sleep(10 * time.Millisecond)
			}
			usage, active := readGatewayAccounting(t, caseID)
			if active != 0 {
				t.Fatalf("active leases: %d", active)
			}
			documentCalls, workerCalls := 0, 0
			for _, row := range usage {
				if row.Caller == "document" && row.Model == cfg.OCRModel {
					documentCalls++
				}
				if row.Caller == "worker" && row.Model == cfg.ChatModel {
					workerCalls++
				}
			}
			if documentCalls != 1 || workerCalls != 1 {
				t.Fatalf("expected settled derived OCR and review: %+v", usage)
			}
		})
	}
}
