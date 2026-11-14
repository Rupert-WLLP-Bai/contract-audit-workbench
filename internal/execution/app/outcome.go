// File metadata (manifest mirror):
// Purpose: Classifies standard review terminal outcomes and maps model errors to existing public messages with original precedence and partial-result semantics.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"errors"

	"contractaudit/internal/engine"
	"contractaudit/internal/model"
)

type Outcome struct{ State, PartialReason, ErrorCode string }
type OutcomeInput struct {
	PipelineError, RecorderError, ModelError error
	// ModelNotConfigured must be the sentinel used by the caller for this invocation.
	ModelNotConfigured        error
	RulesPublished            bool
	ReadyCount, DocumentCount int
}

const incompleteDocuments = "有材料解析失败，结果不能当成完整审查。"

// ClassifyOutcome retains the legacy precedence, including wrapped error ordering.
func ClassifyOutcome(in OutcomeInput) Outcome {
	out := Outcome{State: "completed"}
	if in.PipelineError != nil {
		if in.RecorderError != nil {
			return Outcome{State: "failed", ErrorCode: "execution_persistence_failed"}
		}
		if in.ModelError != nil && in.RulesPublished {
			out.State = "partial"
			switch {
			case errors.Is(in.ModelError, model.ErrRequest):
				out.ErrorCode = "model_request_failed"
			case errors.Is(in.ModelError, model.ErrUnusable):
				out.ErrorCode = "model_output_unusable"
			case errors.Is(in.ModelError, engine.ErrRejected):
				out.ErrorCode = "model_evidence_rejected"
			case in.ModelNotConfigured != nil && errors.Is(in.ModelError, in.ModelNotConfigured):
				out.ErrorCode = "model_not_configured"
			case errors.Is(in.ModelError, context.DeadlineExceeded):
				out.ErrorCode = "model_timeout"
			case errors.Is(in.ModelError, context.Canceled):
				out.ErrorCode = "model_cancelled"
			default:
				out.ErrorCode = "model_incomplete"
			}
			out.PartialReason = PublicModelError(in.ModelError, in.ModelNotConfigured)
			if in.ReadyCount < in.DocumentCount {
				out.PartialReason += " " + incompleteDocuments
			}
			return out
		}
		out.State = "failed"
		out.ErrorCode = "execution_failed"
		if in.RulesPublished {
			out.ErrorCode = "execution_persistence_failed"
		}
		return out
	}
	if in.ReadyCount < in.DocumentCount {
		out.State = "partial"
		out.PartialReason = incompleteDocuments
		out.ErrorCode = "document_parse_incomplete"
	}
	return out
}

// PublicModelError maps model errors to the existing public review message.
func PublicModelError(err, notConfigured error) string {
	switch {
	case errors.Is(err, model.ErrUnusable):
		return "模型输出无法解析成质保结论，已保留可定位的规则结果；模型审查尚未完成。"
	case errors.Is(err, engine.ErrRejected):
		return "模型输出不能对应到材料原文，已保留可定位的规则结果；模型审查尚未完成。"
	case notConfigured != nil && errors.Is(err, notConfigured):
		return "审查模型未配置，已保留可定位的规则结果；模型审查尚未完成。"
	case errors.Is(err, model.ErrRequest):
		return "调用审查模型失败，已保留可定位的规则结果；模型审查尚未完成。"
	default:
		return "审查模型没有完成质保核对，已保留可定位的规则结果。"
	}
}
