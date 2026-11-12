// File metadata (manifest mirror):
// Purpose: CallObservation contains bounded completion metadata from one physical request. Related `model` operations include WithCallObserver, callObserverFromContext.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-30
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"context"

	"contractaudit/internal/execution"
)

type callObserverKey struct{}
type callPurposeKey struct{}

const (
	CallPurposePlanner       = "planner"
	CallPurposeContractFacts = "contract_facts"
	CallPurposeOCR           = "ocr"
)

// WithCallPurpose marks the actual use of one physical model request for the
// execution observer. It is telemetry only; it grants no model authority.
func WithCallPurpose(ctx context.Context, purpose string) context.Context {
	return context.WithValue(ctx, callPurposeKey{}, purpose)
}

func CallPurpose(ctx context.Context) string {
	purpose, _ := ctx.Value(callPurposeKey{}).(string)
	return purpose
}

// CallObservation contains bounded completion metadata from one physical request.
type CallObservation struct {
	ModelRef  string // Provider-reported identity; empty when the response did not report one.
	Usage     *TokenUsage
	Status    string
	ErrorCode string
	// Trace is the bounded request/response record. It is never used to decide
	// a finding; it only makes the physical call inspectable.
	Trace *execution.ModelCallTrace
}

// CallObserver observes one request from dispatch through its complete or failed response.
type CallObserver interface {
	StartModelCall(context.Context, string) (context.Context, func(CallObservation))
}

// WithCallObserver attaches an invocation-local observer. It is never process-global.
func WithCallObserver(ctx context.Context, observer CallObserver) context.Context {
	return context.WithValue(ctx, callObserverKey{}, observer)
}

func callObserverFromContext(ctx context.Context) CallObserver {
	observer, _ := ctx.Value(callObserverKey{}).(CallObserver)
	return observer
}
