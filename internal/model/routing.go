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
	if err := validateRoutingInput(state, criteria, instructions); err != nil {
		return ChoiceResult{}, err
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
	return selectRouteChoice(result, completion.Content, criteria)
}

func validateRoutingInput(state string, criteria map[string]string, instructions string) error {
	if !routingInputInBounds(state, criteria, instructions) {
		return fmt.Errorf("%w: invalid routing input", ErrUnusable)
	}
	for id, description := range criteria {
		if !validRoutingCriterion(id, description) {
			return fmt.Errorf("%w: invalid routing criteria", ErrUnusable)
		}
	}
	return nil
}

func routingInputInBounds(state string, criteria map[string]string, instructions string) bool {
	return utf8.ValidString(state) && len(state) <= 32*1024 && len(criteria) >= 2 && len(criteria) <= 64 &&
		len(instructions) != 0 && len(instructions) <= 2048
}

func validRoutingCriterion(id, description string) bool {
	return len(id) != 0 && len(id) <= 80 && len(description) != 0 && len(description) <= 512 &&
		utf8.ValidString(id) && utf8.ValidString(description)
}

func selectRouteChoice(result ChoiceResult, content string, criteria map[string]string) (ChoiceResult, error) {
	choice, ok := parseRouteChoice(content)
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
	return readRouteChoice(content)
}

func readRouteChoice(content string) (string, bool) {
	decoder := json.NewDecoder(strings.NewReader(content))
	if !routeObjectStart(decoder) {
		return "", false
	}
	if !routeChoiceKey(decoder) {
		return "", false
	}
	choice, ok := routeChoiceValue(decoder)
	if !ok {
		return "", false
	}
	if !routeObjectEnd(decoder) {
		return "", false
	}
	return choice, true
}

func routeObjectStart(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delimiter, ok := token.(json.Delim)
	return ok && delimiter == '{' && decoder.More()
}

func routeChoiceKey(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	key, ok := token.(string)
	return err == nil && ok && key == "choice"
}

func routeChoiceValue(decoder *json.Decoder) (string, bool) {
	token, err := decoder.Token()
	choice, ok := token.(string)
	if err != nil || !ok || decoder.More() {
		return "", false
	}
	return choice, true
}

func routeObjectEnd(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '}' {
		return false
	}
	_, err = decoder.Token()
	return err == io.EOF
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
