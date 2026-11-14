// File metadata (manifest mirror):
// Purpose: Checks real Eino ToolsNode dispatch, trusted input isolation, argument rejection, tool errors and cancellation without bypassing runtime guards.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package agentflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

func TestEinoToolsNodeInvokesOnceWithBoundInputAndRejectsArguments(t *testing.T) {
	bound := ToolInput{
		TenantID: "tenant-a", CaseID: "case-a", ActorID: "actor-a",
		Gap:           Gap{ID: "gap-a", Domain: Technical, Capability: "warranty_months"},
		Evidence:      []Evidence{{ID: "evidence-a", TenantID: "tenant-a", CaseID: "case-a", Text: "frozen source"}},
		PriorFindings: []Finding{{RuleID: "prior-rule", Summary: "prior finding"}},
	}
	calls := 0
	wrapped := einoTool{spec: testEinoToolSpec(), inner: toolFunc{spec: testEinoToolSpec(), run: func(_ context.Context, in ToolInput) ([]Finding, error) {
		calls++
		if !reflect.DeepEqual(in, bound) {
			t.Fatalf("trusted input = %+v, want %+v", in, bound)
		}
		return []Finding{{Summary: "observed"}}, nil
	}}}
	node := newTestEinoToolsNode(t, wrapped)
	info, err := wrapped.Info(context.Background())
	if err != nil || info.Name != "test.tool" || info.ParamsOneOf == nil {
		t.Fatalf("info = %+v, %v", info, err)
	}
	params, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	rawSchema, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var schemaBody map[string]any
	if err := json.Unmarshal(rawSchema, &schemaBody); err != nil {
		t.Fatal(err)
	}
	if schemaBody["type"] != "object" || schemaBody["additionalProperties"] != false {
		t.Fatalf("unsafe parameters schema: %s", rawSchema)
	}
	if properties, ok := schemaBody["properties"].(map[string]any); ok && len(properties) != 0 {
		t.Fatalf("parameters must not expose bound identity or evidence: %s", rawSchema)
	}
	for _, arguments := range []string{
		`{"CaseID":"foreign"}`, `{"tenant_id":"foreign"}`, `{"unknown":1}`, `{} {}`, `null`, `[]`,
	} {
		if _, err := invokeTestEinoToolsNode(node, withEinoInvocation(context.Background(), einoInvocation{toolID: "test.tool", input: bound}), arguments); err == nil {
			t.Errorf("accepted %s", arguments)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid arguments invoked tool %d times", calls)
	}
	messages, err := invokeTestEinoToolsNode(node, withEinoInvocation(context.Background(), einoInvocation{toolID: "test.tool", input: bound}), `{}`)
	var findings []Finding
	if err != nil || len(messages) != 1 || messages[0].Role != schema.Tool || messages[0].ToolCallID != "test-call" ||
		json.Unmarshal([]byte(messages[0].Content), &findings) != nil || calls != 1 || len(findings) != 1 || findings[0].Summary != "observed" {
		t.Fatalf("messages = %+v, findings = %+v, calls = %d, err = %v", messages, findings, calls, err)
	}
	if _, err := wrapped.InvokableRun(context.Background(), `{}`); !errors.Is(err, ErrDenied) {
		t.Fatalf("unbound direct invocation error = %v", err)
	}
}

func TestEinoToolsNodePreservesExecutionErrorsAndCancellation(t *testing.T) {
	failure := errors.New("tool backend failed")
	wrapped := einoTool{spec: testEinoToolSpec(), inner: toolFunc{spec: testEinoToolSpec(), run: func(context.Context, ToolInput) ([]Finding, error) {
		return nil, fmt.Errorf("read-only query: %w", failure)
	}}}
	node := newTestEinoToolsNode(t, wrapped)
	ctx := withEinoInvocation(context.Background(), einoInvocation{toolID: "test.tool", input: ToolInput{CaseID: "case-a"}})
	if _, err := invokeTestEinoToolsNode(node, ctx, `{}`); !errors.Is(err, failure) {
		t.Fatalf("wrapped execution error lost its cause: %v", err)
	}

	cancelledTool := einoTool{spec: testEinoToolSpec(), inner: toolFunc{spec: testEinoToolSpec(), run: func(ctx context.Context, _ ToolInput) ([]Finding, error) {
		return nil, ctx.Err()
	}}}
	node = newTestEinoToolsNode(t, cancelledTool)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx = withEinoInvocation(ctx, einoInvocation{toolID: "test.tool", input: ToolInput{CaseID: "case-a"}})
	if _, err := invokeTestEinoToolsNode(node, ctx, `{}`); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error lost its cause: %v", err)
	}
}

func testEinoToolSpec() ToolSpec {
	return ToolSpec{ID: "test.tool", Domain: Technical, Version: "v1", Effect: "read", Capabilities: []string{"warranty_months"}}
}

func newTestEinoToolsNode(t *testing.T, wrapped einoTool) *compose.ToolsNode {
	t.Helper()
	node, err := compose.NewToolNode(context.Background(), &compose.ToolsNodeConfig{
		Tools: []tool.BaseTool{wrapped}, ExecuteSequentially: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func invokeTestEinoToolsNode(node *compose.ToolsNode, ctx context.Context, arguments string) ([]*schema.Message, error) {
	return node.Invoke(ctx, schema.AssistantMessage("", []schema.ToolCall{{
		ID: "test-call", Type: "function", Function: schema.FunctionCall{Name: "test.tool", Arguments: arguments},
	}}))
}
