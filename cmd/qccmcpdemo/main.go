// File metadata (manifest mirror):
// Purpose: Runs a reproducible read-only Qichacha MCP catalog and one Eino ToolsNode skill call without a database or model, writing a redacted demo artifact outside the source repository.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-08
// Updated: 2026-11-08
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// qccmcpdemo lists Qichacha MCP servers/skills and executes one skill through an
// Eino ToolsNode. It is an independent read-only demo: it does not create cases,
// mutate business data, or call a language model.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"contractaudit/internal/qccmcp"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

var serverEndpoints = map[string]string{
	"company":    "https://agent.qcc.com/mcp/company/stream",
	"risk":       "https://agent.qcc.com/mcp/risk/stream",
	"ipr":        "https://agent.qcc.com/mcp/ipr/stream",
	"operation":  "https://agent.qcc.com/mcp/operation/stream",
	"history":    "https://agent.qcc.com/mcp/history/stream",
	"executive":  "https://agent.qcc.com/mcp/executive/stream",
	"regulation": "https://agent.qcc.com/mcp/regulation/stream",
	"case":       "https://agent.qcc.com/mcp/case/stream",
	"tender":     "https://agent.qcc.com/mcp/tender/stream",
	"document":   "https://agent.qcc.com/mcp/document/stream",
}

type catalogEntry struct {
	Server   string                  `json:"server"`
	Endpoint string                  `json:"endpoint"`
	Status   string                  `json:"status"`
	Info     qccmcp.ServerInfo       `json:"server_info,omitempty"`
	Skills   []qccmcp.ToolDefinition `json:"skills,omitempty"`
	Error    string                  `json:"error,omitempty"`
}

type output struct {
	SchemaVersion string         `json:"schema_version"`
	GeneratedAt   time.Time      `json:"generated_at"`
	Eino          map[string]any `json:"eino"`
	Catalog       []catalogEntry `json:"catalog"`
	Execution     map[string]any `json:"execution"`
	Limitations   []string       `json:"limitations"`
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("qccmcpdemo", flag.ContinueOnError)
	flags.SetOutput(stderr)
	servers := flags.String("servers", "company,risk,regulation", "comma-separated servers to catalog")
	server := flags.String("server", "company", "server used for the Eino skill call")
	tool := flags.String("tool", "get_company_registration_info", "skill/tool name to call")
	searchKey := flags.String("search-key", "企查查科技股份有限公司", "company name or unified social credit code")
	destination := flags.String("out", "artifacts/qcc-mcp-demo.json", "JSON artifact path")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return 2
	}
	if !qccmcp.EnabledFromEnvironment() {
		fmt.Fprintln(stderr, "QCC MCP is disabled by default; set QCC_MCP_ENABLED=true for a live demo run")
		return 2
	}
	authorization := qccmcp.AuthorizationFromEnvironment()
	if authorization == "" {
		fmt.Fprintln(stderr, "QCC_MCP_AUTHORIZATION is required (Bearer token; never pass it on the command line)")
		return 2
	}

	ctx := context.Background()
	result := output{
		SchemaVersion: "qcc-mcp-demo/v1",
		GeneratedAt:   time.Now().UTC(),
		Eino: map[string]any{
			"framework":       "github.com/cloudwego/eino",
			"runtime_version": "eino-0.9.22",
			"graph":           "qcc-mcp-catalog + ToolsNode skill call",
			"model_required":  false,
			"planner":         "deterministic explicit tool call for reproducible demo",
			"business_writes": false,
			"authorization":   "server-side Bearer token from environment; not persisted",
		},
		Limitations: []string{
			"Read-only external catalog/skill demo; not a business audit or authorization decision.",
			"Skills are listed from the provider's tools/list response; no synthetic tool names are added.",
			"Sample company data is third-party provider data and is stored outside the source repository.",
		},
	}

	for _, name := range splitList(*servers) {
		endpoint, ok := serverEndpoints[name]
		if !ok {
			result.Catalog = append(result.Catalog, catalogEntry{Server: name, Status: "UNKNOWN_SERVER"})
			continue
		}
		entry := catalogEntry{Server: name, Endpoint: endpoint}
		client := qccmcp.NewClient(endpoint, authorization)
		info, err := client.Initialize(ctx)
		if err != nil {
			entry.Status = "ERROR"
			entry.Error = err.Error()
			result.Catalog = append(result.Catalog, entry)
			continue
		}
		definitions, err := client.ListTools(ctx)
		if err != nil {
			entry.Status = "ERROR"
			entry.Error = err.Error()
			result.Catalog = append(result.Catalog, entry)
			continue
		}
		entry.Status = "RECOGNIZED"
		entry.Info = info
		entry.Skills = definitions
		result.Catalog = append(result.Catalog, entry)
	}

	execution := map[string]any{"server": *server, "tool": *tool, "arguments": map[string]any{"searchKey": *searchKey}}
	if endpoint, ok := serverEndpoints[*server]; !ok {
		execution["status"] = "ERROR"
		execution["error"] = "unknown server"
	} else {
		client := qccmcp.NewClient(endpoint, authorization)
		if _, err := client.Initialize(ctx); err != nil {
			execution["status"] = "ERROR"
			execution["error"] = err.Error()
		} else if tools, definitions, err := client.Tools(ctx); err != nil {
			execution["status"] = "ERROR"
			execution["error"] = err.Error()
		} else if !slices.ContainsFunc(definitions, func(item qccmcp.ToolDefinition) bool { return item.Name == *tool }) {
			execution["status"] = "ERROR"
			execution["error"] = "skill not present in tools/list"
		} else if node, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{Tools: tools}); err != nil {
			execution["status"] = "ERROR"
			execution["error"] = err.Error()
		} else if runnable, err := buildGraph(ctx, node, *tool, *searchKey); err != nil {
			execution["status"] = "ERROR"
			execution["error"] = err.Error()
		} else if messages, err := runnable.Invoke(ctx, []*schema.Message{}); err != nil {
			execution["status"] = "ERROR"
			execution["error"] = err.Error()
		} else {
			var payload any
			text := ""
			if len(messages) > 0 {
				text = messages[0].Content
			}
			if json.Unmarshal([]byte(text), &payload) == nil {
				execution["result"] = payload
			} else {
				execution["result_text"] = text
			}
			execution["status"] = "EXECUTED"
			execution["eino_tool_message_role"] = string(schema.Tool)
		}
	}
	result.Execution = execution

	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := os.MkdirAll(filepath.Dir(*destination), 0o755); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := os.WriteFile(*destination, append(encoded, '\n'), 0o644); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if _, err := fmt.Fprintf(stdout, "QCC MCP catalog: %d server(s); Eino skill call %s as %s\n", len(result.Catalog), *tool, execution["status"]); err != nil {
		return 2
	}
	return 0
}

func buildGraph(ctx context.Context, node *compose.ToolsNode, toolName, searchKey string) (compose.Runnable[[]*schema.Message, []*schema.Message], error) {
	graph := compose.NewGraph[[]*schema.Message, []*schema.Message]()
	arguments, err := json.Marshal(map[string]any{"searchKey": searchKey})
	if err != nil {
		return nil, err
	}
	if err := graph.AddLambdaNode("call", compose.InvokableLambda(func(context.Context, []*schema.Message) (*schema.Message, error) {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID:       "qcc-demo-call",
			Function: schema.FunctionCall{Name: toolName, Arguments: string(arguments)},
		}}), nil
	})); err != nil {
		return nil, err
	}
	if err := graph.AddToolsNode("tools", node); err != nil {
		return nil, err
	}
	for _, edge := range [][2]string{{compose.START, "call"}, {"call", "tools"}, {"tools", compose.END}} {
		if err := graph.AddEdge(edge[0], edge[1]); err != nil {
			return nil, err
		}
	}
	return graph.Compile(ctx, compose.WithGraphName("qcc-mcp-demo"))
}

func splitList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func main() {
	if code := run(os.Args[1:], os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}
