package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"contractaudit/internal/jev"
)

const maxSystemOneResponseBytes = 128 << 10
const maxSystemOneRequestBytes = 64 << 10

// SystemOneRequest is one bounded batch sent through the model gateway. The
// gateway, not this client, owns the provider credential and the budget ledger.
type SystemOneRequest struct {
	Model     string
	State     string
	Questions map[string]jev.NoulQuestion
	Feature   string
	Egress    string
}

type systemOneQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     struct {
		True  string `json:"true"`
		False string `json:"false"`
	} `json:"criteria"`
}

type systemOnePayload struct {
	Model     string                       `json:"model"`
	State     string                       `json:"state"`
	Questions map[string]systemOneQuestion `json:"questions"`
}

// SystemOne posts one batched Noul request to the model gateway. It reuses the
// same service authentication, delegated scope and call observer as chat/OCR
// calls, but uses a separate gateway route so a Jev failure cannot mark the
// review model unavailable. The response is parsed strictly before it is
// returned; an observed model other than the requested one is an error.
func (c *Client) SystemOne(ctx context.Context, request SystemOneRequest) (map[string]jev.NoulAnswer, jev.ResponseMeta, error) {
	if !c.cfg.Gateway {
		return nil, jev.ResponseMeta{}, fmt.Errorf("%w: direct model client cannot call the System One gateway route", ErrRequest)
	}
	if strings.TrimSpace(request.Feature) == "" || strings.TrimSpace(request.Egress) == "" {
		return nil, jev.ResponseMeta{}, fmt.Errorf("%w: System One feature or classification is missing", ErrRequest)
	}
	// Phase 1 has no approved-text egress path. Refuse it here as well as in
	// the gateway so a caller cannot mistake a missing approval for a config
	// toggle.
	if request.Egress != "minimized" {
		return nil, jev.ResponseMeta{}, fmt.Errorf("%w: System One egress is not the minimized classification", ErrRequest)
	}
	if err := jev.ValidateNoulInput(request.State, request.Questions); err != nil {
		return nil, jev.ResponseMeta{}, fmt.Errorf("%w: %v", ErrUnusable, err)
	}
	payload := systemOnePayload{
		Model: strings.TrimSpace(request.Model), State: request.State,
		Questions: make(map[string]systemOneQuestion, len(request.Questions)),
	}
	if payload.Model == "" {
		return nil, jev.ResponseMeta{}, fmt.Errorf("%w: System One model is missing", ErrRequest)
	}
	for id, question := range request.Questions {
		item := systemOneQuestion{Type: "noul", Instructions: question.Instructions}
		item.Criteria.True = question.Criteria.True
		item.Criteria.False = question.Criteria.False
		payload.Questions[id] = item
	}
	body, err := json.Marshal(payload)
	if err != nil || len(body) > maxSystemOneRequestBytes {
		return nil, jev.ResponseMeta{}, fmt.Errorf("%w: System One request cannot be encoded within its bound", ErrUnusable)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, jev.ResponseMeta{}, fmt.Errorf("%w: System One request cannot be built", ErrRequest)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Contract-Service-Key", c.cfg.APIKey)
	for _, item := range []struct{ name, value string }{
		{"X-Contract-Jev-Feature", request.Feature},
		{"X-Contract-Data-Classification", request.Egress},
	} {
		if item.value == "" {
			return nil, jev.ResponseMeta{}, fmt.Errorf("%w: System One boundary header is missing", ErrRequest)
		}
		req.Header.Set(item.name, item.value)
	}
	scope, ok := DelegatedScopeFromContext(ctx)
	if !ok {
		return nil, jev.ResponseMeta{}, fmt.Errorf("%w: delegated model scope is missing", ErrRequest)
	}
	metadata, err := json.Marshal(scope)
	if err != nil {
		return nil, jev.ResponseMeta{}, fmt.Errorf("%w: execution scope cannot be encoded", ErrRequest)
	}
	req.Header.Set("X-Audit-Execution-Scope", string(metadata))

	callCtx := ctx
	observation := CallObservation{ModelRef: "typesafe/" + payload.Model, Status: "failed"}
	if observer := callObserverFromContext(ctx); observer != nil {
		var finish func(CallObservation)
		callCtx, finish = observer.StartModelCall(ctx, observation.ModelRef)
		defer func() {
			if finish != nil {
				finish(observation)
			}
		}()
	}
	res, err := c.http.Do(req.WithContext(callCtx))
	if err != nil {
		if contextErr := callCtx.Err(); contextErr != nil {
			observation.ErrorCode = "timeout"
			if errors.Is(contextErr, context.Canceled) {
				observation.ErrorCode = "cancelled"
			}
			return nil, jev.ResponseMeta{}, fmt.Errorf("%w: %w", ErrRequest, contextErr)
		}
		observation.ErrorCode = "request_failed"
		return nil, jev.ResponseMeta{}, fmt.Errorf("%w: %s", ErrRequest, c.redact(err.Error()))
	}
	defer res.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(res.Body, maxSystemOneResponseBytes+1))
	if readErr != nil {
		observation.ErrorCode = "request_failed"
		return nil, jev.ResponseMeta{}, fmt.Errorf("%w: System One response cannot be read", ErrRequest)
	}
	if len(raw) > maxSystemOneResponseBytes {
		observation.ErrorCode = "invalid_response"
		return nil, jev.ResponseMeta{}, fmt.Errorf("%w: System One response exceeds the size limit", ErrUnusable)
	}
	if res.StatusCode != http.StatusOK {
		observation.ErrorCode = "request_failed"
		return nil, jev.ResponseMeta{}, fmt.Errorf("%w: System One gateway HTTP %d", ErrRequest, res.StatusCode)
	}
	answers, meta, err := jev.ParseSystemOne(raw, request.Questions)
	if err != nil {
		observation.ErrorCode = "invalid_response"
		return nil, jev.ResponseMeta{}, fmt.Errorf("%w: System One response is invalid", ErrUnusable)
	}
	if meta.Model != payload.Model {
		observation.ErrorCode = "invalid_response"
		return nil, jev.ResponseMeta{}, fmt.Errorf("%w: System One observed model differs from the pinned model", ErrUnusable)
	}
	observation.Status = "completed"
	observation.ModelRef = meta.Model
	observation.Usage = tokenUsageFromJev(meta.Usage)
	return answers, meta, nil
}

func tokenUsageFromJev(usage jev.Usage) *TokenUsage {
	if usage.InputTokens == nil && usage.OutputTokens == nil {
		return nil
	}
	result := &TokenUsage{Source: "typesafe_response.usage", InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens}
	if usage.InputTokens != nil && usage.OutputTokens != nil {
		total := *usage.InputTokens + *usage.OutputTokens
		result.TotalTokens = &total
	}
	return result
}
