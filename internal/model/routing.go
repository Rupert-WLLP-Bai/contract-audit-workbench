package model

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const maxRouteAnswerBytes = 1024

// TokenUsage contains only counters observed in the provider's response.
// Nil counters remain unknown; an explicit provider zero stays zero.
type TokenUsage struct {
	Source       string `json:"source"`
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
	TotalTokens  *int64 `json:"total_tokens"`
}

type chatCompletion struct {
	Content string
	Model   string
	Usage   *TokenUsage
}

// ChoiceResult is a bounded routing suggestion, never an audit conclusion.
type ChoiceResult struct {
	Choice        string      `json:"choice"`
	ObservedModel string      `json:"observed_model"`
	Usage         *TokenUsage `json:"usage"`
}

// RouteChoice shares the existing chat transport and accepts only one of the
// caller's criteria. Callers retain authorization and mandatory-rule execution.
func (c *Client) RouteChoice(ctx context.Context, state string, criteria map[string]string, instructions string) (ChoiceResult, error) {
	if !utf8.ValidString(state) || len(state) > 32*1024 || len(criteria) < 2 || len(criteria) > 64 || len(instructions) == 0 || len(instructions) > 2048 {
		return ChoiceResult{}, fmt.Errorf("%w: invalid routing input", ErrUnusable)
	}
	for id, description := range criteria {
		if len(id) == 0 || len(id) > 80 || len(description) == 0 || len(description) > 512 || !utf8.ValidString(id) || !utf8.ValidString(description) {
			return ChoiceResult{}, fmt.Errorf("%w: invalid routing criteria", ErrUnusable)
		}
	}
	question := map[string]any{"state": state, "questions": map[string]any{"review_focus": map[string]any{
		"type": "choice", "instructions": instructions, "criteria": criteria,
	}}}
	body, err := json.Marshal(question)
	if err != nil {
		return ChoiceResult{}, err
	}
	completion, err := c.postChat(ctx, c.cfg.ChatModel, string(body)+"\n仅输出一个JSON对象，且只有choice字段，其值必须来自criteria的候选ID。", 256, "low")
	result := ChoiceResult{ObservedModel: completion.Model, Usage: completion.Usage}
	if err != nil {
		return result, err
	}
	choice, ok := parseRouteChoice(completion.Content)
	if !ok {
		return result, fmt.Errorf("%w: invalid routing answer", ErrUnusable)
	}
	if _, allowed := criteria[choice]; !allowed {
		return result, fmt.Errorf("%w: unknown routing choice", ErrUnusable)
	}
	result.Choice = choice
	return result, nil
}

func parseRouteChoice(content string) (string, bool) {
	if len(content) > maxRouteAnswerBytes {
		return "", false
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	token, err := decoder.Token()
	if err != nil {
		return "", false
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' || !decoder.More() {
		return "", false
	}
	token, err = decoder.Token()
	key, ok := token.(string)
	if err != nil || !ok || key != "choice" {
		return "", false
	}
	token, err = decoder.Token()
	choice, ok := token.(string)
	if err != nil || !ok || decoder.More() {
		return "", false
	}
	token, err = decoder.Token()
	if err != nil {
		return "", false
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '}' {
		return "", false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return "", false
	}
	return choice, true
}

func parseTokenUsage(raw json.RawMessage) *TokenUsage {
	var observed struct {
		Input  *int64 `json:"prompt_tokens"`
		Output *int64 `json:"completion_tokens"`
		Total  *int64 `json:"total_tokens"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &observed) != nil || observed.Input == nil && observed.Output == nil && observed.Total == nil {
		return nil
	}
	for _, value := range []*int64{observed.Input, observed.Output, observed.Total} {
		if value != nil && *value < 0 {
			return nil
		}
	}
	return &TokenUsage{Source: "openai_response.usage", InputTokens: observed.Input, OutputTokens: observed.Output, TotalTokens: observed.Total}
}
