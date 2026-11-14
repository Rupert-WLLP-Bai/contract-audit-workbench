// File metadata (manifest mirror):
// Purpose: Adapts authorized runtime tool input through an Eino ToolsNode using a private context and strict empty planner arguments.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package agentflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// einoTool reads input prepared by the authorized runtime from a private typed
// context. Planner arguments cannot override tenant, case, actor, evidence, or
// prior findings.
type einoTool struct {
	spec  ToolSpec
	inner Tool
}

type einoInvocation struct {
	toolID string
	input  ToolInput
}

type einoInvocationContextKey struct{}

func withEinoInvocation(ctx context.Context, invocation einoInvocation) context.Context {
	return context.WithValue(ctx, einoInvocationContextKey{}, invocation)
}

var _ tool.InvokableTool = einoTool{}

func (t einoTool) Info(context.Context) (*schema.ToolInfo, error) {
	var params jsonschema.Schema
	if err := json.Unmarshal([]byte(`{"type":"object","properties":{},"additionalProperties":false}`), &params); err != nil {
		return nil, err
	}
	return &schema.ToolInfo{Name: t.spec.ID, Desc: fmt.Sprintf("Read-only %s tool, version %s; input is bound by the authorized runtime", t.spec.Domain, t.spec.Version), ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&params)}, nil
}

func (t einoTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	raw := bytes.TrimSpace([]byte(arguments))
	if len(raw) == 0 || raw[0] != '{' {
		return "", fmt.Errorf("tool arguments must be an empty object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var args struct{}
	if err := decoder.Decode(&args); err != nil {
		return "", fmt.Errorf("decode tool arguments: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return "", fmt.Errorf("tool arguments contain trailing data")
	}
	invocation, ok := ctx.Value(einoInvocationContextKey{}).(einoInvocation)
	if !ok || invocation.toolID != t.spec.ID {
		return "", ErrDenied
	}
	findings, err := t.inner.Execute(ctx, invocation.input)
	if err != nil {
		return "", err
	}
	output, err := json.Marshal(findings)
	if err != nil {
		return "", fmt.Errorf("encode tool findings: %w", err)
	}
	return string(output), nil
}
