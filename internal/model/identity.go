// File metadata (manifest mirror):
// Purpose: ConfigurationID identifies configured request behavior, excluding credentials. It cannot attest a provider's unreported checkpoint or hardware revision. Related `model` operations include ConfigurationID, ProviderEndpoint, configurationID, providerEndpointOf.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-12
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
)

const (
	transcriptionInstruction = "逐行转录图片中的全部文字，保持原有顺序。只输出转录，不要解释。"
	completionTokenLimit     = 2048
	transcriptionTokenLimit  = 8192
)

// ConfigurationID identifies configured request behavior, excluding credentials.
// It cannot attest a provider's unreported checkpoint or hardware revision.
func (c *Client) ConfigurationID() string { return c.configurationID }

// ConfigurationError reports an invalid or changed contract before admission
// or dispatch; it never substitutes the disabled-model identity.
func (c *Client) ConfigurationError() error {
	id, _, err := StructuredConfigurationIDFor(providerEndpointOf(c.cfg), c.cfg.ChatModel, c.cfg.OCRModel)
	if err != nil || c.configurationID == "" || id != c.configurationID {
		return errors.New("model configuration is invalid or has changed")
	}
	return nil
}

// ProviderEndpoint is the provider address the configuration identity names. It
// never carries a credential, so it may travel with a delegated scope.
func (c *Client) ProviderEndpoint() string { return providerEndpointOf(c.cfg) }

func configurationID(cfg Config) string {
	id, _, _ := StructuredConfigurationIDFor(providerEndpointOf(cfg), cfg.ChatModel, cfg.OCRModel)
	return id
}

// StructuredResponseFormat returns one of the server-owned response schemas.
func StructuredResponseFormat(kind string) json.RawMessage {
	switch kind {
	case "warranty":
		return json.RawMessage(warrantyResponseFormat)
	case "cloud_contract":
		return json.RawMessage(cloudResponseFormat)
	case "contract_facts":
		return json.RawMessage(contractFactsResponseFormat)
	default:
		return nil
	}
}

const warrantyResponseFormat = `{"type":"json_schema","json_schema":{"name":"warranty_review","strict":true,"schema":{"type":"object","properties":{"claim":{"type":"string"},"relation":{"type":"string"},"disposition":{"type":"string"},"quotes":{"type":"array","items":{"type":"object","properties":{"document_id":{"type":"string"},"quote":{"type":"string"}},"required":["document_id","quote"],"additionalProperties":false}}},"required":["claim","relation","disposition","quotes"],"additionalProperties":false}}}`
const cloudResponseFormat = `{"type":"json_schema","json_schema":{"name":"cloud_contract_review","strict":true,"schema":{"type":"object","properties":{"findings":{"type":"array","items":{"type":"object","properties":{"domain":{"type":"string"},"policy_clause":{"type":"string"},"claim":{"type":"string"},"document_id":{"type":"string"},"quote":{"type":"string"}},"required":["domain","policy_clause","claim","document_id","quote"],"additionalProperties":false}}},"required":["findings"],"additionalProperties":false}}}`
const contractFactsResponseSchema = `{"type":"object","properties":{"fields":{"type":"array","items":{"type":"object","properties":{"field":{"type":"string","enum":["parties","contract_amount","tax_treatment","business_dates","term","payment_schedule","subject_matter","exceptions"]},"status":{"type":"string","enum":["known","unknown","conflicting"]},"value":{"type":"string"},"citations":{"type":"array","items":{"type":"object","properties":{"quote":{"type":"string"}},"required":["quote"],"additionalProperties":false}}},"required":["field","status","value","citations"],"additionalProperties":false}}},"required":["fields"],"additionalProperties":false}`
const contractFactsResponseFormat = `{"type":"json_schema","json_schema":{"name":"single_contract_facts","strict":true,"schema":` + contractFactsResponseSchema + `}}`

// StructuredConfigurationIDFor binds the optional fixed response contract to
// the selected provider, without changing the legacy identity when disabled.
func StructuredConfigurationIDFor(endpoint, chatModel, ocrModel string) (string, bool, error) {
	enabled := strings.TrimSpace(os.Getenv("CAW_STRUCTURED_OUTPUT_ENABLED"))
	if enabled == "" || enabled == "false" {
		return ConfigurationIDFor(endpoint, chatModel, ocrModel), false, nil
	}
	if enabled != "true" || endpoint == "" || chatModel == "" || endpoint != strings.TrimSpace(os.Getenv("CAW_STRUCTURED_OUTPUT_PROVIDER_URL")) || chatModel != strings.TrimSpace(os.Getenv("CAW_STRUCTURED_OUTPUT_REVIEW_MODEL")) {
		return "", false, errors.New("structured output contract does not match the configured provider and review model")
	}
	payload, _ := json.Marshal([]string{"audit-model-request/structured/v1", ConfigurationIDFor(endpoint, chatModel, ocrModel), warrantyResponseFormat, cloudResponseFormat, "strict-parser-and-semantic-validation/v1"})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), true, nil
}

func providerEndpointOf(cfg Config) string {
	if cfg.ProviderURL != "" {
		return cfg.ProviderURL
	}
	return cfg.BaseURL
}

// ConfigurationIDFor computes the same identity from a provider address and
// model ids. The model gateway uses it to prove that the endpoint it is about
// to call is the one the requester froze, instead of the latest configured one.
func ConfigurationIDFor(endpoint, chatModel, ocrModel string) string {
	// A fixed array of strings has no unsupported values or marshaling failure.
	payload, _ := json.Marshal([]string{
		"audit-model-request/v3", endpoint, chatModel, ocrModel,
		warrantyPrompt(nil), cloudContractPrompt(CloudPolicy{}, nil), transcriptionInstruction,
		"temperature=0;max_tokens=" + strconv.Itoa(completionTokenLimit) + ";ocr_max_tokens=" + strconv.Itoa(transcriptionTokenLimit),
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
