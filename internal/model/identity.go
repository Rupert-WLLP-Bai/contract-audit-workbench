package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
)

const (
	transcriptionInstruction = "逐行转录图片中的全部文字，保持原有顺序。只输出转录，不要解释。"
	completionTokenLimit     = 2048
	transcriptionTokenLimit  = 8192
)

// ConfigurationID identifies configured request behavior, excluding credentials.
// It cannot attest a provider's unreported checkpoint or hardware revision.
func (c *Client) ConfigurationID() string { return c.configurationID }

// ProviderEndpoint is the provider address the configuration identity names. It
// never carries a credential, so it may travel with a delegated scope.
func (c *Client) ProviderEndpoint() string { return providerEndpointOf(c.cfg) }

func configurationID(cfg Config) string {
	return ConfigurationIDFor(providerEndpointOf(cfg), cfg.ChatModel, cfg.OCRModel)
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
