// File metadata (manifest mirror):
// Purpose: Defines frozen single-contract facts capability and source identities, binds model quotes to unique exact Unicode ranges, and validates advisory projections against immutable attempt evidence.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package agentflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"contractaudit/internal/model"
)

const (
	ContractFactsProjectionSchema = "contract-facts/v1"
	ContractFactsToolID           = "contract_facts.extract"
	ContractFactsToolVersion      = "1"
)

var contractFactFieldNames = []string{
	"parties", "contract_amount", "tax_treatment", "business_dates", "term", "payment_schedule", "subject_matter", "exceptions",
}

// ContractFactsConfiguration is frozen at run admission. A nil value on a
// historical PlannerConfiguration means the attempt never accepted this call.
type ContractFactsConfiguration struct {
	ConfigurationID      string `json:"configuration_id"`
	ModelConfigurationID string `json:"model_configuration_id"`
	ChatModel            string `json:"chat_model"`
	PromptVersion        string `json:"prompt_version"`
	SchemaVersion        string `json:"schema_version"`
	MaxInputCodepoints   int    `json:"max_input_codepoints"`
}

func ResolveContractFactsConfiguration(modelConfigurationID, chatModel, configurationID string) (*ContractFactsConfiguration, error) {
	if strings.TrimSpace(modelConfigurationID) == "" || strings.TrimSpace(chatModel) == "" ||
		configurationID == "" || model.ContractFactsConfigurationID(modelConfigurationID, chatModel) != configurationID {
		return nil, errors.New("contract facts configuration identity is invalid")
	}
	configuration := &ContractFactsConfiguration{
		ConfigurationID: configurationID, ModelConfigurationID: modelConfigurationID, ChatModel: chatModel,
		PromptVersion: model.ContractFactsPromptVersion, SchemaVersion: model.ContractFactsSchemaVersion,
		MaxInputCodepoints: model.ContractFactsMaxRunes,
	}
	return configuration, configuration.Validate()
}

// ResolvePlannerConfigurationWithContractFacts adds the optional facts
// capability to a newly accepted run without changing the legacy resolver.
func ResolvePlannerConfigurationWithContractFacts(modelConfigurationID, chatModel, factsConfigurationID string) (PlannerConfiguration, error) {
	configuration, err := ResolvePlannerConfiguration(modelConfigurationID)
	if err != nil {
		return PlannerConfiguration{}, err
	}
	facts, err := ResolveContractFactsConfiguration(modelConfigurationID, chatModel, factsConfigurationID)
	if err != nil {
		return PlannerConfiguration{}, err
	}
	configuration.ContractFacts = facts
	configuration.ConfigurationID, err = plannerConfigurationID(configuration)
	if err != nil {
		return PlannerConfiguration{}, err
	}
	if err := configuration.Validate(); err != nil {
		return PlannerConfiguration{}, err
	}
	return configuration, nil
}

func (configuration ContractFactsConfiguration) Validate() error {
	if configuration.ConfigurationID == "" || configuration.ModelConfigurationID == "" || configuration.ChatModel == "" ||
		configuration.PromptVersion != model.ContractFactsPromptVersion || configuration.SchemaVersion != model.ContractFactsSchemaVersion ||
		configuration.MaxInputCodepoints != model.ContractFactsMaxRunes ||
		model.ContractFactsConfigurationID(configuration.ModelConfigurationID, configuration.ChatModel) != configuration.ConfigurationID {
		return errors.New("contract facts configuration is invalid")
	}
	return nil
}

// ContractFactsSource binds a suggestion to the frozen original and parsed
// document identities. Digests are supplied by Store only after frozen-input
// verification; model output cannot choose them.
type ContractFactsSource struct {
	DocumentID       string `json:"document_id"`
	ParsedDocumentID string `json:"parsed_document_id"`
	OriginalDigest   string `json:"original_digest"`
	NormalizedDigest string `json:"normalized_digest"`
}

// ContractFactsMaterialCandidate is a Store-provided view of one interpreted
// material plus its frozen-input match. Agentflow owns selection of exactly one
// ready primary contract; Store owns construction of the authorized candidate.
type ContractFactsMaterialCandidate struct {
	Role   string
	Ready  bool
	Frozen bool
	Source ContractFactsSource
	Text   string
}

func SelectContractFactsPrimary(candidates []ContractFactsMaterialCandidate) (ContractFactsSource, string, error) {
	var primary *ContractFactsMaterialCandidate
	for i := range candidates {
		if candidates[i].Role != "draft_contract" {
			continue
		}
		if primary != nil {
			return ContractFactsSource{}, "", errors.New("frozen primary contract is ambiguous")
		}
		primary = &candidates[i]
	}
	if primary == nil || !primary.Ready || !primary.Frozen || primary.Text == "" || !utf8.ValidString(primary.Text) ||
		primary.Source.DocumentID == "" || primary.Source.ParsedDocumentID == "" ||
		!validContractFactsDigest(primary.Source.OriginalDigest) || !validContractFactsDigest(primary.Source.NormalizedDigest) {
		return ContractFactsSource{}, "", errors.New("frozen primary contract text is unavailable")
	}
	return primary.Source, primary.Text, nil
}

type ContractFactCitation struct {
	DocumentID       string `json:"document_id"`
	ParsedDocumentID string `json:"parsed_document_id"`
	OriginalDigest   string `json:"original_digest"`
	NormalizedDigest string `json:"normalized_digest"`
	Quote            string `json:"quote"`
	Start            int    `json:"start"`
	End              int    `json:"end"`
}

type ContractFact struct {
	Field     string                 `json:"field"`
	Status    string                 `json:"status"`
	Value     string                 `json:"value"`
	Citations []ContractFactCitation `json:"citations"`
}

// ContractFactsProjection is a facts-only Execution projection. It is never a
// five-domain review and contains no risk decision or Finding identity.
type ContractFactsProjection struct {
	SchemaVersion        string              `json:"schema_version"`
	AttemptID            string              `json:"attempt_id"`
	Status               string              `json:"status"`
	ModelConfigurationID string              `json:"model_configuration_id"`
	ConfigurationID      string              `json:"configuration_id"`
	PromptVersion        string              `json:"prompt_version"`
	Source               ContractFactsSource `json:"source"`
	InputCodepoints      int                 `json:"input_codepoints"`
	InputTruncated       bool                `json:"input_truncated"`
	FailureCode          string              `json:"failure_code,omitempty"`
	Fields               []ContractFact      `json:"fields"`
}

// BindContractFacts resolves model quotes only when each occurs exactly once
// in the supplied frozen prefix, computes Unicode codepoint spans, then binds
// citations to server-owned source identity. A bad quote makes only its field
// unknown; other independently verified fields remain available. Its shape
// checks are an application boundary for typed callers and recovered steps;
// the model package separately rejects ambiguous raw JSON before decoding.
func BindContractFacts(attemptID, fullText, suppliedText string, truncated bool, source ContractFactsSource,
	configuration ContractFactsConfiguration, suggestion model.ContractFactsSuggestion) (ContractFactsProjection, error) {
	if attemptID == "" || !utf8.ValidString(fullText) || !utf8.ValidString(suppliedText) || fullText == "" || suppliedText == "" {
		return ContractFactsProjection{}, errors.New("contract facts frozen input is invalid")
	}
	if err := configuration.Validate(); err != nil {
		return ContractFactsProjection{}, err
	}
	fullRunes, suppliedRunes := []rune(fullText), []rune(suppliedText)
	if len(suppliedRunes) > configuration.MaxInputCodepoints || len(suppliedRunes) > len(fullRunes) ||
		string(fullRunes[:len(suppliedRunes)]) != suppliedText || truncated != (len(suppliedRunes) < len(fullRunes)) {
		return ContractFactsProjection{}, errors.New("contract facts supplied text is not the bounded frozen prefix")
	}
	if source.DocumentID == "" || source.ParsedDocumentID == "" || !validContractFactsDigest(source.OriginalDigest) || !validContractFactsDigest(source.NormalizedDigest) {
		return ContractFactsProjection{}, errors.New("contract facts source identity is incomplete")
	}
	if len(suggestion.Fields) != len(contractFactFieldNames) {
		return ContractFactsProjection{}, errors.New("contract facts fields are incomplete")
	}
	projection := ContractFactsProjection{
		SchemaVersion: ContractFactsProjectionSchema, AttemptID: attemptID, Status: "suggested",
		ModelConfigurationID: configuration.ModelConfigurationID, ConfigurationID: configuration.ConfigurationID,
		PromptVersion: configuration.PromptVersion, Source: source, InputCodepoints: len(suppliedRunes),
		InputTruncated: truncated, Fields: make([]ContractFact, 0, len(suggestion.Fields)),
	}
	seen := make(map[string]bool, len(suggestion.Fields))
	for _, field := range suggestion.Fields {
		if !knownContractFactField(field.Field) || seen[field.Field] {
			return ContractFactsProjection{}, errors.New("contract facts field identity is invalid")
		}
		seen[field.Field] = true
		if !utf8.ValidString(field.Value) || len([]rune(field.Value)) > 1200 || len(field.Citations) > 3 {
			return ContractFactsProjection{}, errors.New("contract facts field exceeds its bounds")
		}
		if field.Status != "known" && field.Status != "unknown" && field.Status != "conflicting" {
			return ContractFactsProjection{}, errors.New("contract facts field status is invalid")
		}
		if (field.Status == "unknown" && (field.Value != "" || len(field.Citations) != 0)) ||
			(field.Status == "known" && (strings.TrimSpace(field.Value) == "" || len(field.Citations) == 0)) ||
			(field.Status == "conflicting" && (field.Value != "" || len(field.Citations) < 2)) {
			return ContractFactsProjection{}, errors.New("contract facts unknown or conflict semantics are invalid")
		}
		fact := ContractFact{Field: field.Field, Status: field.Status, Value: field.Value, Citations: make([]ContractFactCitation, 0, len(field.Citations))}
		if field.Status != "known" || truncated {
			projection.Status = "partial"
		}
		fieldHasUnboundQuote := false
		seenRanges := make(map[[2]int]bool, len(field.Citations))
		for _, citation := range field.Citations {
			start, end, located := uniqueContractFactsQuote(suppliedText, fullText, citation.Quote)
			span := [2]int{start, end}
			if !located || seenRanges[span] {
				fieldHasUnboundQuote = true
				continue
			}
			seenRanges[span] = true
			fact.Citations = append(fact.Citations, ContractFactCitation{
				DocumentID: source.DocumentID, ParsedDocumentID: source.ParsedDocumentID,
				OriginalDigest: source.OriginalDigest, NormalizedDigest: source.NormalizedDigest,
				Quote: citation.Quote, Start: start, End: end,
			})
		}
		if fieldHasUnboundQuote || (field.Status == "conflicting" && len(fact.Citations) < 2) ||
			(field.Status == "known" && len(fact.Citations) == 0) {
			fact.Status, fact.Value, fact.Citations = "unknown", "", []ContractFactCitation{}
			projection.Status = "partial"
		}
		projection.Fields = append(projection.Fields, fact)
	}
	for _, field := range contractFactFieldNames {
		if !seen[field] {
			return ContractFactsProjection{}, errors.New("contract facts required field is missing")
		}
	}
	return projection, nil
}

func uniqueContractFactsQuote(suppliedText, fullText, quote string) (int, int, bool) {
	if !utf8.ValidString(quote) || quote == "" || len([]rune(quote)) > 500 {
		return 0, 0, false
	}
	first := strings.Index(suppliedText, quote)
	if first < 0 || strings.Index(suppliedText[first+1:], quote) >= 0 {
		return 0, 0, false
	}
	start := utf8.RuneCountInString(suppliedText[:first])
	end := start + utf8.RuneCountInString(quote)
	fullRunes := []rune(fullText)
	if end > len(fullRunes) || string(fullRunes[start:end]) != quote {
		return 0, 0, false
	}
	return start, end, true
}

// ValidateContractFactsFrozenProjection is the pure domain-side read check for
// a persisted projection. Store supplies candidates only after authorization,
// attempt/input identity and snapshot verification have succeeded.
func ValidateContractFactsFrozenProjection(projection ContractFactsProjection, attemptID string,
	configuration ContractFactsConfiguration, candidates []ContractFactsMaterialCandidate) error {
	if err := projection.Validate(); err != nil {
		return err
	}
	if err := configuration.Validate(); err != nil {
		return err
	}
	if projection.AttemptID != attemptID || projection.ConfigurationID != configuration.ConfigurationID ||
		projection.ModelConfigurationID != configuration.ModelConfigurationID || projection.PromptVersion != configuration.PromptVersion {
		return errors.New("contract facts projection does not match its frozen attempt capability")
	}
	source, fullText, err := SelectContractFactsPrimary(candidates)
	if err != nil {
		return err
	}
	if source != projection.Source {
		return errors.New("contract facts projection is not bound to its frozen primary source")
	}
	fullRunes := []rune(fullText)
	inputCodepoints := len(fullRunes)
	if inputCodepoints > configuration.MaxInputCodepoints {
		inputCodepoints = configuration.MaxInputCodepoints
	}
	truncated := len(fullRunes) > configuration.MaxInputCodepoints
	if projection.InputCodepoints != inputCodepoints || projection.InputTruncated != truncated {
		return errors.New("contract facts projection has an invalid bounded input identity")
	}
	suppliedText := string(fullRunes[:inputCodepoints])
	for _, field := range projection.Fields {
		for _, citation := range field.Citations {
			start, end, located := uniqueContractFactsQuote(suppliedText, fullText, citation.Quote)
			if !located || start != citation.Start || end != citation.End {
				return errors.New("contract facts projection citation does not match its frozen source")
			}
		}
	}
	return nil
}

func EmptyContractFactsProjection(attemptID string, configuration ContractFactsConfiguration, source ContractFactsSource, inputCodepoints int, truncated bool, failureCode string) ContractFactsProjection {
	fields := make([]ContractFact, 0, len(contractFactFieldNames))
	for _, field := range contractFactFieldNames {
		fields = append(fields, ContractFact{Field: field, Status: "unknown", Value: "", Citations: []ContractFactCitation{}})
	}
	return ContractFactsProjection{
		SchemaVersion: ContractFactsProjectionSchema, AttemptID: attemptID, Status: "partial",
		ModelConfigurationID: configuration.ModelConfigurationID, ConfigurationID: configuration.ConfigurationID,
		PromptVersion: configuration.PromptVersion, Source: source, InputCodepoints: inputCodepoints,
		InputTruncated: truncated, FailureCode: failureCode, Fields: fields,
	}
}

func (projection ContractFactsProjection) Validate() error {
	if projection.SchemaVersion != ContractFactsProjectionSchema || projection.AttemptID == "" ||
		(projection.Status != "suggested" && projection.Status != "partial") || projection.ModelConfigurationID == "" ||
		!validContractFactsDigest(projection.ConfigurationID) || !validContractFactsDigest(projection.ModelConfigurationID) ||
		(projection.FailureCode != "" && !validContractFactsFailureCode(projection.FailureCode)) ||
		projection.PromptVersion != model.ContractFactsPromptVersion ||
		projection.InputCodepoints < 1 || projection.InputCodepoints > model.ContractFactsMaxRunes ||
		projection.Source.DocumentID == "" || projection.Source.ParsedDocumentID == "" ||
		!validContractFactsDigest(projection.Source.OriginalDigest) || !validContractFactsDigest(projection.Source.NormalizedDigest) ||
		len(projection.Fields) != len(contractFactFieldNames) {
		return errors.New("contract facts projection identity is invalid")
	}
	seen := make(map[string]bool, len(projection.Fields))
	partial := projection.InputTruncated || projection.FailureCode != ""
	for _, field := range projection.Fields {
		if !knownContractFactField(field.Field) || seen[field.Field] ||
			(field.Status != "known" && field.Status != "unknown" && field.Status != "conflicting") {
			return errors.New("contract facts projection field is invalid")
		}
		seen[field.Field] = true
		if !utf8.ValidString(field.Value) || len([]rune(field.Value)) > 1200 || len(field.Citations) > 3 {
			return errors.New("contract facts projection field exceeds its bounds")
		}
		if (field.Status == "unknown" && (field.Value != "" || len(field.Citations) != 0)) ||
			(field.Status == "known" && (strings.TrimSpace(field.Value) == "" || len(field.Citations) == 0)) ||
			(field.Status == "conflicting" && (field.Value != "" || len(field.Citations) < 2)) {
			return errors.New("contract facts projection status is invalid")
		}
		if field.Status != "known" {
			partial = true
		}
		for _, citation := range field.Citations {
			if citation.DocumentID != projection.Source.DocumentID || citation.ParsedDocumentID != projection.Source.ParsedDocumentID ||
				citation.OriginalDigest != projection.Source.OriginalDigest || citation.NormalizedDigest != projection.Source.NormalizedDigest ||
				citation.Start < 0 || citation.End <= citation.Start || citation.End > projection.InputCodepoints ||
				!utf8.ValidString(citation.Quote) || citation.End-citation.Start != len([]rune(citation.Quote)) ||
				len([]rune(citation.Quote)) > 500 || citation.Quote == "" {
				return errors.New("contract facts projection citation binding is invalid")
			}
		}
	}
	for _, field := range contractFactFieldNames {
		if !seen[field] {
			return errors.New("contract facts projection field is missing")
		}
	}
	if (projection.Status == "suggested" && partial) || (projection.Status == "partial" && !partial) {
		return errors.New("contract facts projection status does not match its coverage")
	}
	if projection.InputTruncated && projection.Status != "partial" {
		return errors.New("truncated contract facts projection cannot be complete")
	}
	return nil
}

func knownContractFactField(value string) bool {
	for _, field := range contractFactFieldNames {
		if field == value {
			return true
		}
	}
	return false
}

func validContractFactsFailureCode(value string) bool {
	switch value {
	case "contract_facts_output_invalid", "contract_facts_request_failed", "contract_facts_ambiguous",
		"contract_facts_step_failed", "contract_facts_unavailable", "model_configuration_changed":
		return true
	default:
		return false
	}
}

func validContractFactsDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size && hex.EncodeToString(raw) == value
}

func ContractFactsToolConfigurationSHA256(configuration ContractFactsConfiguration) string {
	encoded, _ := json.Marshal(configuration)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
