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
)

// ConfigurationID identifies configured request behavior, excluding credentials.
// It cannot attest a provider's unreported checkpoint or hardware revision.
func (c *Client) ConfigurationID() string { return c.configurationID }

func configurationID(cfg Config) string {
	endpoint := cfg.ProviderURL
	if endpoint == "" {
		endpoint = cfg.BaseURL
	}
	// A fixed array of strings has no unsupported values or marshaling failure.
	payload, _ := json.Marshal([]string{
		"audit-model-request/v2", endpoint, cfg.ChatModel, cfg.OCRModel,
		warrantyPrompt(nil), cloudContractPrompt(CloudPolicy{}, nil), transcriptionInstruction,
		"temperature=0;max_tokens=" + strconv.Itoa(completionTokenLimit),
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
