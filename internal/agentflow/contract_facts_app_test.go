// File metadata (manifest mirror):
// Purpose: Exercises exact checkpoint reuse, non-repeated model dispatch and stable failed-step projections in the single-contract facts application.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r2
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

func TestHistoricalFactsProjectionAndCheckpointRemainFrozen(t *testing.T) {
	caller := &factsAppFixtureCaller{suggestion: factsTestSuggestion(model.ContractFactsField{})}
	input, current := factsAppFixture(t, caller)
	historical := current
	historical.PromptVersion = model.LegacyContractFactsPromptVersion
	historical.ConfigurationID = model.ContractFactsConfigurationIDForVersion(historical.ModelConfigurationID, historical.ChatModel, historical.PromptVersion)
	if historical.ConfigurationID == current.ConfigurationID || historical.Validate() != nil {
		t.Fatal("historical configuration must remain valid and distinct")
	}
	caller.factsID = historical.ConfigurationID
	journal := newMemoryStepJournal()
	projection, err := ExtractContractFacts(context.Background(), input, historical, caller, journal)
	if err != nil || caller.calls != 1 || projection.Validate() != nil {
		t.Fatalf("historical projection failed: calls=%d err=%v", caller.calls, err)
	}
	caller.factsID = current.ConfigurationID
	replayed, err := ExtractContractFacts(context.Background(), input, historical, caller, journal)
	if err != nil || caller.calls != 1 || !reflect.DeepEqual(projection, replayed) {
		t.Fatalf("historical checkpoint was not reused: calls=%d err=%v", caller.calls, err)
	}
	freshCaller := &factsAppFixtureCaller{modelID: current.ModelConfigurationID, factsID: current.ConfigurationID}
	_, err = ExtractContractFacts(context.Background(), input, historical, freshCaller, newMemoryStepJournal())
	if err != nil || freshCaller.calls != 0 {
		t.Fatalf("current client executed historical prompt: calls=%d err=%v", freshCaller.calls, err)
	}
	historical.PromptVersion = current.PromptVersion
	if historical.Validate() == nil {
		t.Fatal("historical identity accepted with current prompt version")
	}
}
