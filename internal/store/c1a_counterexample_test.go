package store

import (
	"context"
	"testing"

	"contractaudit/internal/model"
)

func TestReparseRequiresNewRunWithoutChangingOriginal(t *testing.T) {
	st, admin := openExecutionStore(t)
	models := &frozenOCRModel{transcript: "采购文件要求质保36个月。"}
	st.UseModels(models)
	caseID, err := st.CreateCase(admin, "重解析身份反例", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	docID, err := st.AddDocument(context.Background(), admin, caseID, "procurement", "purchase.png", "image/png", model.SampleWarrantyPNG)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDocument(context.Background(), admin, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。")); err != nil {
		t.Fatal(err)
	}
	firstID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	first := waitTerminalWorkspace(t, st, admin, caseID, firstID)
	original, originalBytes, err := st.Original(admin, caseID, docID)
	if err != nil {
		t.Fatal(err)
	}
	models.setTranscript("采购文件要求质保24个月。")
	if err := st.Reparse(context.Background(), admin, caseID, docID); err != nil {
		t.Fatal(err)
	}
	secondID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if secondID == firstID {
		t.Fatal("reparse changed OCR evidence but StartReview reused the old run identity")
	}
	second := waitTerminalWorkspace(t, st, admin, caseID, secondID)
	if got := findDocumentText(second.Documents, docID); got != "采购文件要求质保24个月。" {
		t.Fatalf("new run did not select new parse: %q", got)
	}
	historical, err := st.Workspace(admin, caseID, firstID, first.Execution.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := findDocumentText(historical.Documents, docID); got != "采购文件要求质保36个月。" {
		t.Fatalf("reparse changed historical evidence: %q", got)
	}
	current, currentBytes, err := st.Original(admin, caseID, docID)
	if err != nil || current.Digest != original.Digest || string(currentBytes) != string(originalBytes) {
		t.Fatalf("reparse changed original identity or bytes: %v", err)
	}
}
