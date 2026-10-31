package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCaseIntakeDigestPinsNormalizedMetadataAndFileBytes(t *testing.T) {
	base := CaseIntakeInput{Title: "合同", ContractType: "service", BusinessLine: "ENT", Scenario: "cloud_services",
		Filename: "draft.pdf", MediaType: "application/pdf", Body: []byte("contract bytes"), IdempotencyKey: "key-1"}
	normalized, _, _, first, err := normalizeCaseIntakeInput(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Title = "  合同  "
	base.Filename = "folder\\draft.pdf"
	base.MediaType = "application/pdf; charset=binary"
	_, _, _, normalizedDigest, err := normalizeCaseIntakeInput(base)
	if err != nil {
		t.Fatal(err)
	}
	if first != normalizedDigest {
		t.Fatalf("normalized equal input digest changed: %s != %s", first, normalizedDigest)
	}
	if normalized.Filename != "draft.pdf" || normalized.MediaType != "application/pdf" {
		t.Fatalf("unexpected normalized upload metadata: %+v", normalized)
	}

	for name, mutate := range map[string]func(*CaseIntakeInput){
		"filename": func(in *CaseIntakeInput) { in.Filename = "another.pdf" },
		"media":    func(in *CaseIntakeInput) { in.MediaType = "application/octet-stream" },
		"body":     func(in *CaseIntakeInput) { in.Body = []byte("changed") },
		"title":    func(in *CaseIntakeInput) { in.Title = "别的合同" },
		"scenario": func(in *CaseIntakeInput) { in.Scenario = "telecom_dedicated_line" },
	} {
		t.Run(name, func(t *testing.T) {
			input := base
			input.Title, input.Filename, input.MediaType = "合同", "draft.pdf", "application/pdf"
			mutate(&input)
			_, _, _, got, err := normalizeCaseIntakeInput(input)
			if err != nil {
				t.Fatal(err)
			}
			if got == first {
				t.Fatal("changed intake payload retained the original digest")
			}
		})
	}
}

func TestCaseIntakeInputRequiresExactFixedUploadContract(t *testing.T) {
	valid := CaseIntakeInput{Title: "合同", ContractType: "service", BusinessLine: "ENT", Scenario: "cloud_services",
		Filename: "draft.pdf", MediaType: "application/pdf", Body: []byte("x"), IdempotencyKey: "visible-ASCII_09"}
	if _, _, _, _, err := normalizeCaseIntakeInput(valid); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "has space", strings.Repeat("a", 201), "bad\nkey"} {
		input := valid
		input.IdempotencyKey = key
		if _, _, _, _, err := normalizeCaseIntakeInput(input); err == nil {
			t.Errorf("Idempotency-Key %q unexpectedly accepted", key)
		}
	}
	tooLarge := valid
	tooLarge.Body = make([]byte, caseIntakeMaxFileBytes+1)
	if _, _, _, _, err := normalizeCaseIntakeInput(tooLarge); err == nil {
		t.Fatal("oversized file unexpectedly accepted")
	}
	controlName := valid
	controlName.Filename = "draft\r\nInjected.txt"
	if _, _, _, _, err := normalizeCaseIntakeInput(controlName); err == nil {
		t.Fatal("control characters in a filename were accepted")
	}
}

func TestCaseIntakeCompletedOperationVerifiesFrozenBody(t *testing.T) {
	receipt := CaseIntakeReceipt{
		OperationID: "intake_0123456789abcdef", CaseID: "case_0123456789abcdef", State: "completed",
		MaterialID: "mat_0123456789abcdef", MaterialVersion: 1, DocumentID: "doc_0123456789abcdef", Retryable: false,
	}
	body, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	record := caseIntakeRecord{
		OperationID: receipt.OperationID, CaseID: receipt.CaseID, Status: caseIntakeCompletedStatus,
		HTTPStatus: caseIntakeCompletedHTTP, ResponseBody: body, ResponseDigest: caseIntakeDigest(body),
	}
	operation, err := record.operation()
	if err != nil {
		t.Fatal(err)
	}
	if operation.Receipt == nil || operation.Receipt.MaterialVersion != 1 || string(operation.ReceiptBody) != string(body) {
		t.Fatalf("frozen completion was not returned verbatim: %+v", operation)
	}

	tampered := append([]byte(nil), body...)
	tampered[len(tampered)-2] = 'x'
	record.ResponseBody = tampered
	if _, err := record.operation(); err == nil {
		t.Fatal("a tampered completed body was accepted")
	}
	record.ResponseBody = body
	record.ResponseDigest = strings.Repeat("0", 64)
	if _, err := record.operation(); err == nil {
		t.Fatal("a mismatched completion digest was accepted")
	}
}

func TestCaseIntakePendingBodyIsStableAndSafe(t *testing.T) {
	operation := CaseIntakeOperation{OperationID: "intake_0123456789abcdef", CaseID: "case_0123456789abcdef", Status: caseIntakePendingStatus}
	body := string(operation.PendingBody())
	want := `{"operation_id":"intake_0123456789abcdef","case_id":"case_0123456789abcdef","state":"upload_pending","retryable":true,"error_code":"document_upload_unavailable"}`
	if body != want {
		t.Fatalf("pending body = %s, want %s", body, want)
	}
	if strings.Contains(body, "://") || strings.Contains(body, "message") {
		t.Fatalf("pending body exposed an upstream location or message: %s", body)
	}
}
