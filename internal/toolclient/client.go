// Package toolclient calls the read-only tool gateway for one durable worker
// claim.
//
// The caller is a worker, never a browser: it presents its current job claim and
// a service-pair credential instead of a session token. The gateway resolves the
// actor, tenant, and case grant from PostgreSQL on both sides of the read, so a
// claim that was cancelled or re-claimed cannot deliver a result.
package toolclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// ProtocolVersion is the frozen MCP profile the tool gateway serves.
	ProtocolVersion = "2026-07-28"
	// ToolName is the one read-only capability this client calls.
	ToolName = "frozen_evidence_read"

	maxResponseBytes = 1 << 20
	maxClaimBytes    = 4 << 10
)

// Claim is the durable job identity a worker presents. TenantID and OrgID are
// routing hints taken from the worker's own job claim: a wrong hint hides rows
// (fail closed) and is never authority.
type Claim struct {
	TenantID   string
	OrgID      string
	JobID      string
	WorkerID   string
	Generation int64
	AttemptID  string
}

// Request is one frozen-evidence read for one immutable attempt.
type Request struct {
	Claim        Claim
	CaseID       string
	RunID        string
	AttemptID    string
	CapabilityID string
	Deadline     time.Time
}

// Client is a bounded JSON-RPC client for the tool gateway's MCP endpoint.
type Client struct {
	endpoint   string
	credential string
	http       *http.Client
}

// New creates a client for the internal tool gateway endpoint.
func New(endpoint, credential string, timeout time.Duration) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || !isHTTPOrHTTPS(parsed.Scheme) ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("tool gateway endpoint must be an HTTP(S) origin without credentials or query")
	}
	if len(credential) < 32 {
		return nil, errors.New("tool gateway credential must contain at least 32 bytes")
	}
	if timeout <= 0 {
		return nil, errors.New("tool gateway timeout must be positive")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/mcp"
	parsed.RawPath = ""
	return &Client{endpoint: parsed.String(), credential: credential, http: &http.Client{Timeout: timeout}}, nil
}

func isHTTPOrHTTPS(scheme string) bool {
	return scheme == "http" || scheme == "https"
}

// FrozenEvidence returns the frozen evidence payload of one attempt. The caller
// must hold the live claim; the gateway re-resolves it before returning the
// payload, so a stale or cancelled claim fails instead of delivering evidence.
func (c *Client) FrozenEvidence(ctx context.Context, request Request) (json.RawMessage, error) {
	if ctx == nil {
		return nil, errors.New("tool call context is required")
	}
	jobContext, err := encodeCurrentClaim(request)
	if err != nil {
		return nil, err
	}
	httpRequest, err := newToolRequest(ctx, c.endpoint, c.credential, request, jobContext)
	if err != nil {
		return nil, err
	}
	response, err := c.http.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("tool call failed: %w", err)
	}
	defer response.Body.Close()
	return readEvidenceResponse(response)
}

func encodeCurrentClaim(request Request) ([]byte, error) {
	if !validJobClaim(request.Claim) {
		return nil, errors.New("tool call requires the current job claim")
	}
	if !validToolRequest(request) {
		return nil, errors.New("tool call requires the case, run, attempt, capability, and deadline")
	}
	return encodeClaimContext(request.Claim)
}

func validJobClaim(claim Claim) bool {
	return strings.TrimSpace(claim.JobID) != "" && strings.TrimSpace(claim.WorkerID) != "" &&
		strings.TrimSpace(claim.AttemptID) != "" && claim.Generation >= 1 &&
		strings.TrimSpace(claim.TenantID) != "" && strings.TrimSpace(claim.OrgID) != ""
}

func validToolRequest(request Request) bool {
	return request.AttemptID != "" && request.AttemptID == request.Claim.AttemptID &&
		request.CaseID != "" && request.RunID != "" && request.CapabilityID != "" && !request.Deadline.IsZero()
}

func encodeClaimContext(claim Claim) ([]byte, error) {
	jobContext, err := json.Marshal(map[string]any{
		"tenant_id": claim.TenantID, "org_id": claim.OrgID,
		"job_id": claim.JobID, "worker_id": claim.WorkerID,
		"generation": claim.Generation, "attempt_id": claim.AttemptID,
	})
	if err != nil || len(jobContext) > maxClaimBytes {
		return nil, errors.New("tool call claim could not be encoded")
	}
	return jobContext, nil
}

func newToolRequest(ctx context.Context, endpoint, credential string, request Request, jobContext []byte) (*http.Request, error) {
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": "worker-frozen-evidence", "method": "tools/call",
		"params": map[string]any{
			"_meta": map[string]any{
				"io.modelcontextprotocol/protocolVersion":    ProtocolVersion,
				"io.modelcontextprotocol/clientCapabilities": map[string]any{},
				"io.modelcontextprotocol/clientInfo":         map[string]string{"name": "contract-audit-worker", "version": "1.0.0"},
			},
			"name": ToolName,
			"arguments": map[string]any{
				"case_id": request.CaseID, "run_id": request.RunID, "attempt_id": request.AttemptID,
				"capability_id": request.CapabilityID, "deadline": request.Deadline.UTC().Format(time.RFC3339),
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode tool call: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create tool call: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json, text/event-stream")
	httpRequest.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	httpRequest.Header.Set("Mcp-Method", "tools/call")
	httpRequest.Header.Set("Mcp-Name", ToolName)
	httpRequest.Header.Set("X-Contract-Service-Key", credential)
	httpRequest.Header.Set("X-Contract-Job-Context", string(jobContext))
	return httpRequest, nil
}

func readEvidenceResponse(response *http.Response) (json.RawMessage, error) {
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read tool response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, errors.New("tool response exceeds the size limit")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tool gateway answered HTTP %d", response.StatusCode)
	}
	return decodeEvidenceResponse(body)
}

func decodeEvidenceResponse(body []byte) (json.RawMessage, error) {
	var envelope struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Result *struct {
			IsError bool `json:"isError"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, errors.New("tool gateway answered an invalid JSON-RPC response")
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("tool call refused: %s (code %d)", envelope.Error.Message, envelope.Error.Code)
	}
	if envelope.Result == nil || envelope.Result.IsError || len(envelope.Result.Content) != 1 ||
		envelope.Result.Content[0].Type != "text" {
		return nil, errors.New("tool call did not return one evidence payload")
	}
	payloadText := envelope.Result.Content[0].Text
	if !json.Valid([]byte(payloadText)) {
		return nil, errors.New("tool call returned a non-JSON evidence payload")
	}
	return json.RawMessage(payloadText), nil
}
