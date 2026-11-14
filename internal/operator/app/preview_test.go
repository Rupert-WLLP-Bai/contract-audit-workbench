package app

import (
	"context"
	"testing"

	executionmodel "contractaudit/internal/execution/model"
	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

type previewTestDocument struct{}

func (previewTestDocument) OriginalProof() executionmodel.OriginalDocumentProof {
	return executionmodel.OriginalDocumentProof{}
}

func TestPreviewRejectsMissingSelectionBeforeReadingFacts(t *testing.T) {
	_, err := Preview[previewTestDocument](context.Background(), nil, PreviewScope{}, model.ServiceObjectHead{}, model.ServiceObjectAssociationPreviewInput{})
	problem, ok := err.(*fault.Error)
	if !ok || problem.Status != 422 || problem.Message != "冻结审查及材料解析选择无效" {
		t.Fatalf("unexpected invalid selection result: %v", err)
	}
}
