// File metadata (manifest mirror):
// Purpose: Pins the distinct outer and transaction-bound frozen input error protocols without executing providers.
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
)

type absentEvaluationInput struct {
	EvaluationFrozenFacts[previewTestDocument]
}

func (absentEvaluationInput) Run(string) (executionmodel.Run, bool) {
	return executionmodel.Run{CaseID: "case"}, true
}
func (absentEvaluationInput) Attempt(string) (executionmodel.Attempt, bool) {
	finished, input, output := "done", "input", "output"
	return executionmodel.Attempt{RunID: "run", State: "completed", FinishedAt: &finished, InputHash: &input, OutputHash: &output, HistoryKind: "frozen"}, true
}
func (absentEvaluationInput) Input(string) (executionmodel.AttemptInput[previewTestDocument], bool) {
	return executionmodel.AttemptInput[previewTestDocument]{}, false
}
func TestFrozenEvaluationInputErrorsRetainBothProtocols(t *testing.T) {
	for _, tc := range []struct {
		inside  bool
		message string
	}{{false, "这次执行保存的输入不存在或编号不匹配"}, {true, "来源审查的冻结输入未通过校验"}} {
		_, _, _, _, err := ValidateEvaluationFrozen[previewTestDocument](context.Background(), absentEvaluationInput{}, PreviewScope{CaseID: "case"}, "run", "attempt", tc.inside)
		f, ok := err.(*fault.Error)
		if !ok || f.Message != tc.message {
			t.Fatalf("inside=%v err=%v", tc.inside, err)
		}
	}
}
