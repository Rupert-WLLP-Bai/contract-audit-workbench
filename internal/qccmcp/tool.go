// File metadata (manifest mirror):
// Purpose: Adapts discovered Qichacha MCP tools to Eino InvokableTool instances while preserving the server-provided JSON schema and read-only behavior.
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
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// EinoTool is an Eino InvokableTool backed by one Qichacha MCP tool.
type EinoTool struct {
	client *Client
	def    ToolDefinition
}

// Info returns the server-provided tool identity and input schema.
func (t *EinoTool) Info(context.Context) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{Name: t.def.Name, Desc: t.def.Description}
	if len(t.def.InputSchema) == 0 {
		return info, nil
	}
	var inputSchema jsonschema.Schema
	if err := json.Unmarshal(t.def.InputSchema, &inputSchema); err != nil {
		return nil, fmt.Errorf("decode %s input schema: %w", t.def.Name, err)
	}
	info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(&inputSchema)
	return info, nil
}

// InvokableRun forwards the model-generated JSON arguments to the MCP server.
func (t *EinoTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	arguments := map[string]any{}
	if text := trimSpace(argumentsInJSON); text != "" && text != "{}" {
		if err := json.Unmarshal([]byte(text), &arguments); err != nil {
			return "", fmt.Errorf("decode %s arguments: %w", t.def.Name, err)
		}
	}
	result, err := t.client.CallTool(ctx, t.def.Name, arguments)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

// Tools discovers all tools on the server and wraps them for Eino ToolsNode.
func (c *Client) Tools(ctx context.Context) ([]tool.BaseTool, []ToolDefinition, error) {
	definitions, err := c.ListTools(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(definitions) == 0 {
		return nil, nil, errors.New("qcc mcp server exposed no tools")
	}
	tools := make([]tool.BaseTool, 0, len(definitions))
	for _, definition := range definitions {
		if definition.Name == "" {
			return nil, nil, errors.New("qcc mcp tool has no name")
		}
		tools = append(tools, &EinoTool{client: c, def: definition})
	}
	return tools, definitions, nil
}

func trimSpace(value string) string {
	for len(value) > 0 {
		switch value[0] {
		case ' ', '\t', '\n', '\r':
			value = value[1:]
		default:
			return value
		}
	}
	return value
}
