// File metadata (manifest mirror):
// Purpose: Runs one frozen single-contract facts extraction through the existing durable step journal and validates the bounded result before the Store commits its projection.
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
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"contractaudit/internal/execution/steps"
	"contractaudit/internal/model"
)

const contractFactsStepRuntime = "single-contract-facts-runtime/v1"

// ContractFactsInput is assembled by the authorized Store bridge from one
// already-verified frozen primary material. It carries no caller-controlled
// identity or text outside that frozen input.
type ContractFactsInput struct {
	AttemptID              string
	AttemptInputSHA256     string
	PlannerConfigurationID string
	Source                 ContractFactsSource
	Text                   string
}

type contractFactsStepInput struct {
	AttemptInputSHA256 string `json:"attempt_input_sha256"`
	DocumentID         string `json:"document_id"`
	ParsedDocumentID   string `json:"parsed_document_id"`
	OriginalDigest     string `json:"original_digest"`
	NormalizedDigest   string `json:"normalized_digest"`
	FullTextSHA256     string `json:"full_text_sha256"`
	SuppliedTextSHA256 string `json:"supplied_text_sha256"`
	InputCodepoints    int    `json:"input_codepoints"`
	InputTruncated     bool   `json:"input_truncated"`
	ConfigurationID    string `json:"configuration_id"`
}

// ExtractContractFacts performs one bounded suggestion call, or revalidates a
// successful checkpoint from the same frozen attempt. Persistence of the final
// discriminator remains a separate Store transaction-boundary operation.
func ExtractContractFacts(ctx context.Context, input ContractFactsInput, configuration ContractFactsConfiguration,
	caller model.ContractFactsCaller, journal steps.Journal) (ContractFactsProjection, error) {
	if ctx == nil {
		return ContractFactsProjection{}, errors.New("contract facts context is nil")
	}
	if err := ctx.Err(); err != nil {
		return ContractFactsProjection{}, err
	}
	if input.AttemptID == "" || !validContractFactsDigest(input.AttemptInputSHA256) ||
		!validContractFactsDigest(input.PlannerConfigurationID) ||
		!utf8.ValidString(input.Text) || input.Text == "" || input.Source.DocumentID == "" ||
		input.Source.ParsedDocumentID == "" || !validContractFactsDigest(input.Source.OriginalDigest) ||
		!validContractFactsDigest(input.Source.NormalizedDigest) {
		return ContractFactsProjection{}, errors.New("contract facts input is incomplete")
	}
	if err := configuration.Validate(); err != nil {
		return ContractFactsProjection{}, err
	}
	if journal == nil {
		return ContractFactsProjection{}, errors.New("contract facts require the durable step journal")
	}
	suppliedText, truncated, err := boundContractFactsText(input.Text, configuration.MaxInputCodepoints)
	if err != nil {
		return ContractFactsProjection{}, err
	}
	stepInput := contractFactsStepInput{
		AttemptInputSHA256: input.AttemptInputSHA256, DocumentID: input.Source.DocumentID,
		ParsedDocumentID: input.Source.ParsedDocumentID, OriginalDigest: input.Source.OriginalDigest,
		NormalizedDigest: input.Source.NormalizedDigest, FullTextSHA256: digestBytes([]byte(input.Text)),
		SuppliedTextSHA256: digestBytes([]byte(suppliedText)), InputCodepoints: utf8.RuneCountInString(suppliedText),
		InputTruncated: truncated, ConfigurationID: configuration.ConfigurationID,
	}
	encodedInput, err := json.Marshal(stepInput)
	if err != nil {
		return ContractFactsProjection{}, err
	}
	toolCatalog, err := json.Marshal([]string{ContractFactsToolID, ContractFactsToolVersion, ContractFactsProjectionSchema})
	if err != nil {
		return ContractFactsProjection{}, err
	}
	identity := steps.Identity{
		StepID: "contract_facts/extract", Kind: "tool", Round: 1,
		RuntimeVersion: contractFactsStepRuntime, PlannerID: "single_contract_precheck",
		PlannerConfigurationID: input.PlannerConfigurationID, ModelConfigurationID: configuration.ModelConfigurationID,
		ToolCatalogVersion: "single-contract-facts-catalog/v1", ToolCatalogSHA256: digestBytes(toolCatalog),
		ToolID: ContractFactsToolID, ToolVersion: ContractFactsToolVersion,
		ToolConfigurationSHA256: ContractFactsToolConfigurationSHA256(configuration), InputSHA256: digestBytes(encodedInput),
	}
	if err := identity.Validate(); err != nil {
		return ContractFactsProjection{}, err
	}
	record, execute, err := journal.Begin(ctx, identity)
	if err != nil {
		if errors.Is(err, steps.ErrAmbiguous) || errors.Is(err, steps.ErrPriorFailure) {
			return emptyContractFactsFailure(input, configuration, stepInput, truncated, contractFactsStepFailure(err)), nil
		}
		return ContractFactsProjection{}, err
	}
	if err := record.Validate(); err != nil {
		return ContractFactsProjection{}, errors.Join(steps.ErrCorrupt, err)
	}
	if record.Identity != identity {
		return ContractFactsProjection{}, steps.ErrIdentityMismatch
	}
	var suggestion model.ContractFactsSuggestion
	var callErr error
	failureCode := ""
	if execute {
		if record.Status != steps.StatusStarted {
			return ContractFactsProjection{}, steps.ErrCorrupt
		}
		started := time.Now()
		if caller == nil || caller.ConfigurationID() != configuration.ModelConfigurationID || caller.Name() != configuration.ChatModel ||
			caller.ContractFactsConfigurationID() != configuration.ConfigurationID {
			callErr = errors.New("frozen contract facts model configuration changed")
		} else {
			suggestion, callErr = caller.ContractFacts(ctx, suppliedText, truncated)
			if callErr == nil {
				if _, bindErr := BindContractFacts(input.AttemptID, input.Text, suppliedText, truncated, input.Source, configuration, suggestion); bindErr != nil {
					callErr = fmt.Errorf("%w: %v", model.ErrUnusable, bindErr)
				}
			}
		}
		observedAt := time.Now().UTC()
		outcome := steps.Outcome{ObservedAt: observedAt, ElapsedMS: max(observedAt.Sub(started).Milliseconds(), 0),
			CallsConsumed: 1, Usage: steps.Usage{Status: steps.UsageUnknown}}
		if callErr != nil {
			outcome.Status = steps.StatusFailed
			outcome.FailureCode = contractFactsStepFailure(callErr)
			failureCode = outcome.FailureCode
		} else {
			outcome.Status = steps.StatusSucceeded
			outcome.Payload, err = json.Marshal(suggestion)
			if err != nil {
				return ContractFactsProjection{}, err
			}
		}
		if err := journal.Finish(ctx, identity, outcome); err != nil {
			return ContractFactsProjection{}, err
		}
	} else {
		switch record.Status {
		case steps.StatusSucceeded:
			suggestion, err = model.ParseContractFacts(string(record.Payload))
			if err != nil || record.ObservedAt == nil {
				return ContractFactsProjection{}, errors.Join(steps.ErrCorrupt, err)
			}
		case steps.StatusFailed:
			failureCode = record.FailureCode
		case steps.StatusAmbiguous:
			failureCode = "contract_facts_ambiguous"
		case steps.StatusStarted:
			return ContractFactsProjection{}, steps.ErrAmbiguous
		default:
			return ContractFactsProjection{}, steps.ErrCorrupt
		}
	}
	if callErr != nil || failureCode != "" {
		if failureCode == "" {
			failureCode = contractFactsStepFailure(callErr)
		}
		if !validContractFactsFailureCode(failureCode) {
			failureCode = "contract_facts_step_failed"
		}
		return emptyContractFactsFailure(input, configuration, stepInput, truncated, failureCode), nil
	}
	projection, err := BindContractFacts(input.AttemptID, input.Text, suppliedText, truncated, input.Source, configuration, suggestion)
	if err != nil || projection.Validate() != nil {
		return ContractFactsProjection{}, errors.Join(steps.ErrCorrupt, err)
	}
	return projection, nil
}

func boundContractFactsText(text string, maximum int) (string, bool, error) {
	if !utf8.ValidString(text) || text == "" || maximum < 1 {
		return "", false, errors.New("frozen contract facts text is invalid")
	}
	runes := []rune(text)
	if len(runes) <= maximum {
		return text, false, nil
	}
	return string(runes[:maximum]), true, nil
}

func emptyContractFactsFailure(input ContractFactsInput, configuration ContractFactsConfiguration, stepInput contractFactsStepInput,
	truncated bool, failureCode string) ContractFactsProjection {
	return EmptyContractFactsProjection(input.AttemptID, configuration, input.Source, stepInput.InputCodepoints, truncated, failureCode)
}

func contractFactsStepFailure(err error) string {
	switch {
	case errors.Is(err, model.ErrUnusable):
		return "contract_facts_output_invalid"
	case errors.Is(err, model.ErrRequest):
		return "contract_facts_request_failed"
	case errors.Is(err, steps.ErrAmbiguous):
		return "contract_facts_ambiguous"
	case errors.Is(err, steps.ErrPriorFailure):
		return "contract_facts_step_failed"
	case err != nil && err.Error() == "frozen contract facts model configuration changed":
		return "model_configuration_changed"
	default:
		return "contract_facts_unavailable"
	}
}
