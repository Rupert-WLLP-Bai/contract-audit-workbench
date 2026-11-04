package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"contractaudit/internal/httpapi"
	"contractaudit/internal/store"
)

type lostIntakeUploadResponse struct {
	delegate store.CaseIntakeUploader
	lost     bool
	first    store.DocumentUploadResult
}

func (u *lostIntakeUploadResponse) UploadPrimaryContract(ctx context.Context, caseID string, request store.PrimaryContractUploadRequest) (store.DocumentUploadResult, error) {
	result, err := u.delegate.UploadPrimaryContract(ctx, caseID, request)
	if err == nil && !u.lost {
		u.lost, u.first = true, result
		return store.DocumentUploadResult{}, errors.New("simulated lost Document response after commit")
	}
	return result, err
}

func TestDistributedIntakeRecoversLostDocumentResponseAcrossSessions(t *testing.T) {
	trace := &intakeDiagnostics{}
	env := newServiceEnvironment(t, http.NotFoundHandler(), trace)
	uploader := &lostIntakeUploadResponse{delegate: env.intakeUploader}
	api := httptest.NewServer(httpapi.Handler(env.core.Store, httpapi.Options{IntakeUploader: uploader}))
	defer api.Close()
	post := func(token string) (int, []byte) {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for key, value := range map[string]string{"title": "跨会话恢复", "contract_type": "service", "business_line": "ENT", "scenario": "cloud_services"} {
			if err := writer.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
		disposition := mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": "recovery.txt"})
		part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {disposition}, "Content-Type": {"text/plain"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte("one original contract")); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPost, api.URL+"/api/intakes", &body)
		if err != nil {
			t.Fatal(err)
		}
		session, err := env.core.Store.SessionContext(env.ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", writer.FormDataContentType())
		request.Header.Set("Idempotency-Key", "distributed-session-recovery")
		request.Header.Set("Origin", api.URL)
		request.Header.Set("X-Context-Version", session.ActiveContext.ContextVersion)
		request.Header.Set("X-CSRF-Token", session.CSRFToken)
		request.AddCookie(&http.Cookie{Name: "session", Value: token})
		response, err := api.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, raw
	}
	tokenA, err := env.core.Store.EstablishSession(env.actor, "client")
	if err != nil {
		t.Fatal(err)
	}
	status, pendingBody := post(tokenA)
	var pending store.CaseIntakePendingResponse
	if status != http.StatusAccepted || json.Unmarshal(pendingBody, &pending) != nil || pending.OperationID == "" || uploader.first.DocumentID == "" {
		t.Fatalf("lost response did not preserve pending operation: status=%d boundaries=%v", status, trace.snapshot())
	}
	tokenB, err := env.core.Store.EstablishSession(env.actor, "client")
	if err != nil {
		t.Fatal(err)
	}
	status, receiptBody := post(tokenB)
	var receipt store.CaseIntakeReceipt
	if status != http.StatusCreated || json.Unmarshal(receiptBody, &receipt) != nil || receipt.OperationID != pending.OperationID ||
		receipt.CaseID != pending.CaseID || receipt.DocumentID != uploader.first.DocumentID || receipt.MaterialID != uploader.first.MaterialID {
		t.Fatalf("cross-session recovery failed: status=%d boundaries=%v", status, trace.snapshot())
	}
	status, replayBody := post(tokenB)
	if status != http.StatusCreated || !bytes.Equal(receiptBody, replayBody) {
		t.Fatalf("frozen response changed on replay: status=%d boundaries=%v", status, trace.snapshot())
	}
}
