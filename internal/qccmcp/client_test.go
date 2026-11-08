// File metadata (manifest mirror):
// Purpose: Tests the Qichacha MCP JSON-RPC/SSE client and its Eino ToolsNode adapter against local HTTP fixtures without using a real API key or network.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-08
// Updated: 2026-11-08
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package qccmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

func sse(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "text/event-stream")
	encoded, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", encoded)
}

func TestClientInitializesListsAndCallsTools(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization header = %q", got)
		}
		var request struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		calls = append(calls, request.Method)
		switch request.Method {
		case "initialize":
			sse(w, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{
				"protocolVersion": "2025-03-26",
				"serverInfo":      map[string]any{"name": "qcc-mcp", "title": "QCC MCP", "version": "1.0.0"},
			}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			sse(w, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"tools": []any{map[string]any{
				"name": "get_company_registration_info", "title": "企业工商信息", "description": "查询工商登记",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"searchKey": map[string]any{"type": "string"}}, "required": []string{"searchKey"}},
			}}}})
		case "tools/call":
			if request.Params["name"] != "get_company_registration_info" {
				t.Errorf("tool name = %v", request.Params["name"])
			}
			sse(w, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"content": []any{
				map[string]any{"type": "text", "text": `{"企业名称":"测试企业","统一社会信用代码":"91320000TEST"}`},
			}}})
		default:
			t.Errorf("unexpected method %s", request.Method)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "Bearer test-key")
	info, err := client.Initialize(context.Background())
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if info.Name != "qcc-mcp" {
		t.Fatalf("server info = %+v", info)
	}
	definitions, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(definitions) != 1 || definitions[0].Name != "get_company_registration_info" {
		t.Fatalf("definitions = %+v", definitions)
	}
	result, err := client.CallTool(context.Background(), "get_company_registration_info", map[string]any{"searchKey": "测试企业"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !strings.Contains(string(result), "测试企业") {
		t.Fatalf("result = %s", result)
	}
	want := []string{"initialize", "notifications/initialized", "tools/list", "tools/call"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("methods = %v", calls)
	}
}

func TestEinoToolsNodeInvokesQCCMCPTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		switch request.Method {
		case "tools/list":
			sse(w, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"tools": []any{map[string]any{
				"name": "get_company_registration_info", "description": "查询工商登记",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"searchKey": map[string]any{"type": "string"}}},
			}}}})
		case "tools/call":
			sse(w, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"content": []any{
				map[string]any{"type": "text", "text": `{"企业名称":"测试企业","登记状态":"在业"}`},
			}}})
		default:
			t.Errorf("unexpected method %s", request.Method)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	client := NewClient(server.URL, "Bearer test-key")
	tools, definitions, err := client.Tools(ctx)
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	if len(tools) != 1 || definitions[0].Title != "" {
		t.Fatalf("unexpected tool discovery: %+v", definitions)
	}
	node, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{Tools: tools})
	if err != nil {
		t.Fatalf("tool node: %v", err)
	}
	graph := compose.NewGraph[[]*schema.Message, []*schema.Message]()
	if err := graph.AddLambdaNode("call", compose.InvokableLambda(func(context.Context, []*schema.Message) (*schema.Message, error) {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID:       "call-1",
			Function: schema.FunctionCall{Name: "get_company_registration_info", Arguments: `{"searchKey":"测试企业"}`},
		}}), nil
	})); err != nil {
		t.Fatalf("add lambda: %v", err)
	}
	if err := graph.AddToolsNode("tools", node); err != nil {
		t.Fatalf("add tools: %v", err)
	}
	if err := graph.AddEdge(compose.START, "call"); err != nil {
		t.Fatalf("start edge: %v", err)
	}
	if err := graph.AddEdge("call", "tools"); err != nil {
		t.Fatalf("call edge: %v", err)
	}
	if err := graph.AddEdge("tools", compose.END); err != nil {
		t.Fatalf("end edge: %v", err)
	}
	runnable, err := graph.Compile(ctx, compose.WithGraphName("qcc-mcp-test"))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	output, err := runnable.Invoke(ctx, []*schema.Message{})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if len(output) != 1 || !strings.Contains(output[0].Content, "测试企业") || output[0].Role != schema.Tool {
		t.Fatalf("output = %+v", output)
	}
}
