// File metadata (manifest mirror):
// Purpose: Pins facts-only journal execution, strict successful replay, and byte-stable failed-call projection semantics across a same-attempt takeover.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package agentflow

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"contractaudit/internal/model"
)

type factsAppFixtureCaller struct {
	calls      int
	suggestion model.ContractFactsSuggestion
	err        error
	modelID    string
	factsID    string
}

func (caller *factsAppFixtureCaller) Name() string                         { return "selected-chat" }
func (caller *factsAppFixtureCaller) ConfigurationID() string              { return caller.modelID }
func (caller *factsAppFixtureCaller) ContractFactsConfigurationID() string { return caller.factsID }
func (caller *factsAppFixtureCaller) ContractFacts(context.Context, string, bool) (model.ContractFactsSuggestion, error) {
	caller.calls++
	return caller.suggestion, caller.err
}

func factsAppFixture(t *testing.T, caller *factsAppFixtureCaller) (ContractFactsInput, ContractFactsConfiguration) {
	t.Helper()
	modelID := strings.Repeat("a", 64)
	factsID := model.ContractFactsConfigurationID(modelID, "selected-chat")
	configuration, err := ResolveContractFactsConfiguration(modelID, "selected-chat", factsID)
	if err != nil {
		t.Fatal(err)
	}
	caller.modelID, caller.factsID = modelID, factsID
	return ContractFactsInput{
		AttemptID: "attempt-a", AttemptInputSHA256: strings.Repeat("b", 64),
		PlannerConfigurationID: strings.Repeat("d", 64), Source: factsTestSource(), Text: "甲方🌙公司合同正文",
	}, *configuration
}

func TestExtractContractFactsReusesOnlyTheExactSuccessfulCheckpoint(t *testing.T) {
	caller := &factsAppFixtureCaller{suggestion: factsTestSuggestion(model.ContractFactsField{})}
	input, configuration := factsAppFixture(t, caller)
	journal := newMemoryStepJournal()
	first, err := ExtractContractFacts(context.Background(), input, configuration, caller, journal)
	if err != nil || first.Status != "partial" || caller.calls != 1 {
		t.Fatalf("first facts extraction failed: status=%s calls=%d error=%v", first.Status, caller.calls, err)
	}
	second, err := ExtractContractFacts(context.Background(), input, configuration, caller, journal)
	if err != nil || !reflect.DeepEqual(first, second) || caller.calls != 1 {
		t.Fatalf("successful checkpoint was not revalidated and reused: first=%#v second=%#v calls=%d error=%v", first, second, caller.calls, err)
	}
}

func TestExtractContractFactsFailedCheckpointKeepsItsProjectionFailureCode(t *testing.T) {
	caller := &factsAppFixtureCaller{err: model.ErrRequest}
	input, configuration := factsAppFixture(t, caller)
	journal := newMemoryStepJournal()
	first, err := ExtractContractFacts(context.Background(), input, configuration, caller, journal)
	if err != nil || first.Status != "partial" || first.FailureCode != "contract_facts_request_failed" || caller.calls != 1 {
		t.Fatalf("failed facts call did not produce a partial projection: projection=%#v calls=%d error=%v", first, caller.calls, err)
	}
	caller.err = errors.New("new worker must not call the model")
	second, err := ExtractContractFacts(context.Background(), input, configuration, caller, journal)
	if err != nil || !reflect.DeepEqual(first, second) || caller.calls != 1 {
		t.Fatalf("failed checkpoint changed its projection on takeover: first=%#v second=%#v calls=%d error=%v", first, second, caller.calls, err)
	}
}
