// File metadata (manifest mirror):
// Purpose: Characterizes standard review port failures, recorder precedence, parent cancellation, bounded cleanup reads and terminal-write attempts.
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
	"testing"
	"time"

	"contractaudit/internal/engine"
)

type standardPortsStub struct {
	input                                                                  PreparedInput
	progress                                                               Progress
	writes, finishes, reads                                                int
	outcome                                                                Outcome
	prepareErr, writeErr, verifiedErr, recorderErr, progressErr, finishErr error
	verified                                                               int
	readContextErr                                                         error
	readHasDeadline                                                        bool
}

func (p *standardPortsStub) PrepareFrozenInput(context.Context, FrozenAttempt) (PreparedInput, error) {
	return p.input, p.prepareErr
}
func (p *standardPortsStub) RuleAttempt(_ context.Context, a FrozenAttempt) (FrozenAttempt, error) {
	return a, nil
}
func (p *standardPortsStub) AppendRuleResults(context.Context, FrozenAttempt, RuleResults) error {
	p.writes++
	return p.writeErr
}
func (p *standardPortsStub) MergeCitationDrafts(context.Context, FrozenAttempt, []Document, []engine.Draft) (CitationDelta, error) {
	return CitationDelta{}, nil
}
func (p *standardPortsStub) RecordFirstVerifiedFinding(context.Context, FrozenAttempt, bool) error {
	p.verified++
	return p.verifiedErr
}
func (p *standardPortsStub) RecorderError() error { return p.recorderErr }
func (p *standardPortsStub) ReadAttemptProgress(ctx context.Context, _ FrozenAttempt) (Progress, error) {
	p.reads++
	p.readContextErr = ctx.Err()
	_, p.readHasDeadline = ctx.Deadline()
	return p.progress, p.progressErr
}
func (p *standardPortsStub) FinishAttempt(_ context.Context, _ FrozenAttempt, outcome Outcome) error {
	p.finishes++
	p.outcome = outcome
	return p.finishErr
}

func TestRunStandardMissingModelRetainsRuleResults(t *testing.T) {
	p := &standardPortsStub{input: PreparedInput{Documents: []Document{{ID: "one", Ready: true}, {ID: "two", Ready: true}}}, progress: Progress{Active: true, RulesPublished: true}}
	missing := errors.New("model is not configured")
	RunStandard(context.Background(), context.Background(), FrozenAttempt{}, nil, missing, time.Second, p, nil, &testIDs{}, testClock{})
	if p.writes != 1 || p.reads != 1 || p.finishes != 1 || p.outcome.State != "partial" || p.outcome.ErrorCode != "model_not_configured" {
		t.Fatalf("unexpected result: %+v", p)
	}
}
func TestRunStandardDoesNotFinishAfterParentCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	p := &standardPortsStub{}
	RunStandard(parent, parent, FrozenAttempt{}, nil, errors.New("missing"), time.Second, p, nil, &testIDs{}, testClock{})
	if p.reads != 0 || p.finishes != 0 {
		t.Fatalf("cancelled parent mutated progress: %+v", p)
	}
}

func conflictInput() PreparedInput {
	return PreparedInput{Documents: []Document{
		{ID: "a", Role: "procurement", Text: "采购文件要求质保36个月。", Ready: true},
		{ID: "b", Role: "draft_contract", Text: "拟签合同约定质保24个月。", Ready: true},
	}}
}
func TestRunStandardFailureBoundaries(t *testing.T) {
	failure := errors.New("write failed")
	for _, tc := range []struct {
		name         string
		ports        standardPortsStub
		wantFinish   int
		wantCode     string
		wantVerified int
	}{
		{"rule write fails", standardPortsStub{input: conflictInput(), writeErr: failure, progress: Progress{Active: true}}, 1, "execution_failed", 0},
		{"verified fails with published rules", standardPortsStub{input: conflictInput(), verifiedErr: failure, progress: Progress{Active: true, RulesPublished: true}}, 1, "execution_persistence_failed", 1},
		{"recorder failure wins", standardPortsStub{input: conflictInput(), verifiedErr: failure, recorderErr: failure, progress: Progress{Active: true, RulesPublished: true}}, 1, "execution_persistence_failed", 1},
		{"progress fails", standardPortsStub{prepareErr: failure, progressErr: failure}, 1, "execution_persistence_failed", 0},
		{"terminal attempt", standardPortsStub{prepareErr: failure, progress: Progress{Active: false}}, 0, "", 0},
		{"finish failure is not retried", standardPortsStub{prepareErr: failure, progress: Progress{Active: true}, finishErr: failure}, 1, "execution_failed", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.ports
			RunStandard(context.Background(), context.Background(), FrozenAttempt{}, nil, errors.New("missing"), time.Second, &p, nil, &testIDs{}, testClock{})
			if p.finishes != tc.wantFinish || p.verified != tc.wantVerified || (tc.wantFinish > 0 && p.outcome.ErrorCode != tc.wantCode) {
				t.Fatalf("result=%+v", p)
			}
		})
	}
}
func TestRunStandardUsesLiveBoundedParentForExpiredWorkRead(t *testing.T) {
	parent := context.Background()
	work, cancel := context.WithCancel(parent)
	cancel()
	p := &standardPortsStub{progress: Progress{Active: true}}
	RunStandard(parent, work, FrozenAttempt{}, nil, errors.New("missing"), time.Second, p, nil, &testIDs{}, testClock{})
	if p.reads != 1 || p.readContextErr != nil || !p.readHasDeadline || p.finishes != 1 {
		t.Fatalf("cleanup result=%+v", p)
	}
}
