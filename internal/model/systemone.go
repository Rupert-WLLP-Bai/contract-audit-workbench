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
	"contractaudit/internal/telemetry"
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
	if err := c.validateSystemOneRequest(request); err != nil {
		return nil, jev.ResponseMeta{}, err
	}
	req, modelID, err := c.systemOneHTTPRequest(ctx, request)
	if err != nil {
		return nil, jev.ResponseMeta{}, err
	}

	callCtx := ctx
	observation := CallObservation{ModelRef: "typesafe/" + modelID, Status: "failed"}
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
		observation.ErrorCode, err = systemOneTransportFailure(callCtx, err, c)
		return nil, jev.ResponseMeta{}, err
	}
	defer res.Body.Close()
	answers, meta, failure, err := c.readSystemOneResponse(res, modelID, request.Questions)
	if err != nil {
		observation.ErrorCode = failure
		return nil, meta, err
	}
	observation.Status = "completed"
	observation.ModelRef = meta.Model
	observation.Usage = tokenUsageFromJev(meta.Usage)
	return answers, meta, nil
}

func (c *Client) validateSystemOneRequest(request SystemOneRequest) error {
	if !c.cfg.Gateway {
		return fmt.Errorf("%w: direct model client cannot call the System One gateway route", ErrRequest)
	}
	if strings.TrimSpace(request.Feature) == "" || strings.TrimSpace(request.Egress) == "" {
		return fmt.Errorf("%w: System One feature or classification is missing", ErrRequest)
	}
	// Phase 1 has no approved-text egress path. Refuse it here as well as in
	// the gateway so a caller cannot mistake a missing approval for a config
	// toggle.
	if request.Egress != "minimized" {
		return fmt.Errorf("%w: System One egress is not the minimized classification", ErrRequest)
	}
	if err := jev.ValidateNoulInput(request.State, request.Questions); err != nil {
		return fmt.Errorf("%w: %v", ErrUnusable, err)
	}
	return nil
}

func buildSystemOnePayload(request SystemOneRequest) (systemOnePayload, error) {
	payload := systemOnePayload{
		Model: strings.TrimSpace(request.Model), State: request.State,
		Questions: make(map[string]systemOneQuestion, len(request.Questions)),
	}
	if payload.Model == "" {
		return systemOnePayload{}, fmt.Errorf("%w: System One model is missing", ErrRequest)
	}
	for id, question := range request.Questions {
		item := systemOneQuestion{Type: "noul", Instructions: question.Instructions}
		item.Criteria.True = question.Criteria.True
		item.Criteria.False = question.Criteria.False
		payload.Questions[id] = item
	}
	return payload, nil
}

func (c *Client) systemOneHTTPRequest(ctx context.Context, request SystemOneRequest) (*http.Request, string, error) {
	payload, err := buildSystemOnePayload(request)
	if err != nil {
		return nil, "", err
	}
	body, err := marshalSystemOnePayload(payload)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("%w: System One request cannot be built", ErrRequest)
	}
	if err := applySystemOneHeaders(ctx, req, c.cfg.APIKey, request); err != nil {
		return nil, "", err
	}
	return req, payload.Model, nil
}

func marshalSystemOnePayload(payload systemOnePayload) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil || len(body) > maxSystemOneRequestBytes {
		return nil, fmt.Errorf("%w: System One request cannot be encoded within its bound", ErrUnusable)
	}
	return body, nil
}

func applySystemOneHeaders(ctx context.Context, req *http.Request, serviceKey string, request SystemOneRequest) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Contract-Service-Key", serviceKey)
	req.Header.Set("X-Contract-Jev-Feature", request.Feature)
	req.Header.Set("X-Contract-Data-Classification", request.Egress)
	scope, ok := DelegatedScopeFromContext(ctx)
	if !ok {
		return fmt.Errorf("%w: delegated model scope is missing", ErrRequest)
	}
	metadata, err := json.Marshal(scope)
	if err != nil {
		return fmt.Errorf("%w: execution scope cannot be encoded", ErrRequest)
	}
	req.Header.Set("X-Audit-Execution-Scope", string(metadata))
	// The advisory hop joins the attempt's deterministic trace and carries a
	// bounded request identity, so the model gateway's access log correlates
	// with the attempt that paid for the call.
	req.Header.Set("X-Request-Id", telemetry.NewRequestID())
	if traceparent := telemetry.AttemptTraceparent(scope.AttemptID); traceparent != "" {
		req.Header.Set("Traceparent", traceparent)
	}
	return nil
}

func systemOneTransportFailure(callCtx context.Context, err error, c *Client) (string, error) {
	if contextErr := callCtx.Err(); contextErr != nil {
		if errors.Is(contextErr, context.Canceled) {
			return "cancelled", fmt.Errorf("%w: %w", ErrRequest, contextErr)
		}
		return "timeout", fmt.Errorf("%w: %w", ErrRequest, contextErr)
	}
	return "request_failed", fmt.Errorf("%w: %s", ErrRequest, c.redact(err.Error()))
}

func (c *Client) readSystemOneResponse(res *http.Response, modelID string, questions map[string]jev.NoulQuestion) (map[string]jev.NoulAnswer, jev.ResponseMeta, string, error) {
	raw, readErr := io.ReadAll(io.LimitReader(res.Body, maxSystemOneResponseBytes+1))
	if readErr != nil {
		return nil, jev.ResponseMeta{}, "request_failed", fmt.Errorf("%w: System One response cannot be read", ErrRequest)
	}
	if len(raw) > maxSystemOneResponseBytes {
		return nil, jev.ResponseMeta{}, "invalid_response", fmt.Errorf("%w: System One response exceeds the size limit", ErrUnusable)
	}
	if res.StatusCode != http.StatusOK {
		return nil, jev.ResponseMeta{}, "request_failed", fmt.Errorf("%w: System One gateway HTTP %d", ErrRequest, res.StatusCode)
	}
	answers, meta, err := jev.ParseSystemOne(raw, questions)
	if err != nil {
		return nil, jev.ResponseMeta{}, "invalid_response", fmt.Errorf("%w: System One response is invalid", ErrUnusable)
	}
	if meta.Model != modelID {
		return nil, jev.ResponseMeta{}, "invalid_response", fmt.Errorf("%w: System One observed model differs from the pinned model", ErrUnusable)
	}
	return answers, meta, "", nil
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
