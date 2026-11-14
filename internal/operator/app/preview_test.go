// File metadata (manifest mirror):
// Purpose: Pins rejection of incomplete frozen preview selections before any proof or source read.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

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
