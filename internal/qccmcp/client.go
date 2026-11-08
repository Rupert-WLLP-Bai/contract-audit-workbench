// File metadata (manifest mirror):
// Purpose: Implements a read-only MCP Streamable HTTP client for the Qichacha agent data platform, including JSON-RPC request/response handling and tool discovery without embedding credentials.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-08
// Updated: 2026-11-08
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package qccmcp adapts the Qichacha MCP servers to Eino tools.
package qccmcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const defaultProtocolVersion = "2025-03-26"

// Client calls one Qichacha MCP server over Streamable HTTP. Authorization is
// supplied by the caller and is never logged or persisted by this package.
type Client struct {
	Endpoint      string
	Authorization string
	HTTPClient    *http.Client
	nextID        atomic.Int64
	sessionID     string
}

// ServerInfo is the initialize result exposed by an MCP server.
type ServerInfo struct {
	Name         string `json:"name"`
	Title        string `json:"title"`
	Version      string `json:"version"`
	WebsiteURL   string `json:"websiteUrl"`
	Instructions string `json:"instructions,omitempty"`
}

// ToolDefinition is the subset of tools/list needed to build Eino tools.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// NewClient builds a client for one MCP endpoint. The authorization value must
// already include the Bearer prefix and should come from the process environment.
func NewClient(endpoint, authorization string) *Client {
	return &Client{
		Endpoint:      strings.TrimSpace(endpoint),
		Authorization: strings.TrimSpace(authorization),
		HTTPClient:    &http.Client{Timeout: 60 * time.Second},
	}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message)
}

// Initialize starts a protocol session and returns the server identity.
func (c *Client) Initialize(ctx context.Context) (ServerInfo, error) {
	raw, headers, err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": defaultProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "contract-audit-eino", "version": "0.1.0"},
	})
	if err != nil {
		return ServerInfo{}, err
	}
	var envelope struct {
		ProtocolVersion string     `json:"protocolVersion"`
		ServerInfo      ServerInfo `json:"serverInfo"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ServerInfo{}, fmt.Errorf("decode initialize result: %w", err)
	}
	if session := headers.Get("Mcp-Session-Id"); session != "" {
		c.sessionID = session
	}
	_, err = c.notify(ctx, "notifications/initialized", map[string]any{})
	if err != nil {
		return ServerInfo{}, err
	}
	return envelope.ServerInfo, nil
}

// ListTools discovers the tools exposed by the MCP server.
func (c *Client) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	raw, _, err := c.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Tools []ToolDefinition `json:"tools"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode tools/list result: %w", err)
	}
	return envelope.Tools, nil
}

// CallTool invokes one MCP tool and returns its text payload (normally JSON).
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (json.RawMessage, error) {
	raw, _, err := c.request(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode tools/call result: %w", err)
	}
	if envelope.IsError {
		return nil, errors.New("mcp tool returned an error result")
	}
	for _, item := range envelope.Content {
		if item.Type == "text" && strings.TrimSpace(item.Text) != "" {
			text := strings.TrimSpace(item.Text)
			if json.Valid([]byte(text)) {
				return json.RawMessage(text), nil
			}
			return json.Marshal(text)
		}
	}
	return json.RawMessage("null"), nil
}

func (c *Client) notify(ctx context.Context, method string, params any) (json.RawMessage, error) {
	_, _, err := c.request(ctx, method, params)
	return nil, err
}

func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, http.Header, error) {
	if c.Endpoint == "" {
		return nil, nil, errors.New("qcc mcp endpoint is empty")
	}
	if c.Authorization == "" {
		return nil, nil, errors.New("qcc mcp authorization is empty")
	}
	body := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	if method != "notifications/initialized" {
		body.ID = c.nextID.Add(1)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, nil, err
	}
	request.Header.Set("Authorization", c.Authorization)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if c.sessionID != "" {
		request.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, nil, fmt.Errorf("qcc mcp transport: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		text, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, response.Header, fmt.Errorf("qcc mcp HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(text)))
	}
	if method == "notifications/initialized" {
		return nil, response.Header, nil
	}
	raw, err := decodeResponse(response)
	if err != nil {
		return nil, response.Header, err
	}
	var decoded rpcResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, response.Header, fmt.Errorf("decode JSON-RPC response: %w", err)
	}
	if decoded.Error != nil {
		return nil, response.Header, decoded.Error
	}
	if len(decoded.Result) == 0 {
		return nil, response.Header, errors.New("qcc mcp response has no result")
	}
	return decoded.Result, response.Header, nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func decodeResponse(response *http.Response) (json.RawMessage, error) {
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/event-stream") {
		return decodeSSE(response.Body)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if !json.Valid(body) {
		return nil, errors.New("qcc mcp returned invalid JSON")
	}
	return json.RawMessage(body), nil
}

func decodeSSE(body io.Reader) (json.RawMessage, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	var data []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		if line == "" && len(data) > 0 {
			payload := strings.Join(data, "\n")
			if json.Valid([]byte(payload)) {
				return json.RawMessage(payload), nil
			}
			data = nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(data) > 0 {
		payload := strings.Join(data, "\n")
		if json.Valid([]byte(payload)) {
			return json.RawMessage(payload), nil
		}
	}
	return nil, errors.New("qcc mcp SSE response had no JSON data event")
}
