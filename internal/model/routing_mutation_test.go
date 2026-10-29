package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Routing input budgets are inclusive at the upper bound: 32 KiB of state,
// 64 criteria and 2048 bytes of instructions are accepted.
func TestRoutingInputBoundsAreInclusive(t *testing.T) {
	criteria := make(map[string]string, 64)
	for index := 0; index < 64; index++ {
		criteria[fmt.Sprintf("criterion-%d", index)] = "description"
	}
	if !routingInputInBounds(strings.Repeat("s", 32<<10), criteria, "instructions") {
		t.Fatal("state at 32 KiB with 64 criteria rejected")
	}
	if !routingInputInBounds("state", criteria, strings.Repeat("i", 2048)) {
		t.Fatal("instructions at 2048 bytes rejected")
	}
	if routingInputInBounds(strings.Repeat("s", (32<<10)+1), criteria, "instructions") {
		t.Fatal("state above 32 KiB accepted")
	}
	over := make(map[string]string, 65)
	for index := 0; index < 65; index++ {
		over[fmt.Sprintf("criterion-%d", index)] = "description"
	}
	if routingInputInBounds("state", over, "instructions") {
		t.Fatal("more than 64 criteria accepted")
	}
	if routingInputInBounds("state", criteria, strings.Repeat("i", 2049)) {
		t.Fatal("instructions above 2048 bytes accepted")
	}
}

// A criterion identifier may be 80 bytes and a description 512 bytes.
func TestRoutingCriterionBoundsAreInclusive(t *testing.T) {
	if !validRoutingCriterion(strings.Repeat("i", 80), "d") {
		t.Fatal("80-byte criterion id rejected")
	}
	if !validRoutingCriterion("i", strings.Repeat("d", 512)) {
		t.Fatal("512-byte criterion description rejected")
	}
	if validRoutingCriterion(strings.Repeat("i", 81), "d") {
		t.Fatal("81-byte criterion id accepted")
	}
	if validRoutingCriterion("i", strings.Repeat("d", 513)) {
		t.Fatal("513-byte criterion description accepted")
	}
}

// The routing answer is accepted at exactly 1024 bytes and rejected above it.
func TestParseRouteChoiceSizeBoundary(t *testing.T) {
	answer := `{"choice":"x"}`
	exact := answer + strings.Repeat(" ", maxRouteAnswerBytes-len(answer))
	if choice, ok := parseRouteChoice(exact); !ok || choice != "x" {
		t.Fatalf("answer at the size boundary rejected: %q %v", choice, ok)
	}
	if _, ok := parseRouteChoice(strings.Repeat(" ", maxRouteAnswerBytes+1)); ok {
		t.Fatal("oversized answer accepted")
	}
}

// Route answers must be one object with a string "choice" and no trailing data.
func TestReadRouteChoiceRejectsInvalidEnvelopes(t *testing.T) {
	for _, content := range []string{
		"[]",
		`{"other":"x"}`,
		`{"choice":1}`,
		`{"choice":"x"}x`,
	} {
		if choice, ok := readRouteChoice(content); ok {
			t.Fatalf("invalid route answer accepted: %q -> %q", content, choice)
		}
	}
	if choice, ok := readRouteChoice(`{"choice":"x"}`); !ok || choice != "x" {
		t.Fatalf("valid route answer rejected: %q %v", choice, ok)
	}
}

// The route object must start with '{', not another delimiter.
func TestRouteObjectStartRequiresAnObject(t *testing.T) {
	if routeObjectStart(json.NewDecoder(strings.NewReader(`["x"]`))) {
		t.Fatal("array accepted as route object start")
	}
	if !routeObjectStart(json.NewDecoder(strings.NewReader(`{"choice":"x"}`))) {
		t.Fatal("object rejected as route object start")
	}
}

// The route object must end with '}', not another delimiter.
func TestRouteObjectEndRequiresAClosingBrace(t *testing.T) {
	if routeObjectEnd(json.NewDecoder(strings.NewReader("["))) {
		t.Fatal("non-closing delimiter accepted as route object end")
	}
	opened := json.NewDecoder(strings.NewReader("{}"))
	if _, err := opened.Token(); err != nil {
		t.Fatalf("consume object start: %v", err)
	}
	if !routeObjectEnd(opened) {
		t.Fatal("closing brace rejected")
	}
}

// At least one counter must be observed; a single observed counter is enough.
func TestParseTokenUsageRequiresAtLeastOneObservedCounter(t *testing.T) {
	if usage := parseTokenUsage([]byte(`{}`)); usage != nil {
		t.Fatalf("empty usage object produced %+v", usage)
	}
	if usage := parseTokenUsage([]byte(`{"total_tokens":3}`)); usage == nil || usage.TotalTokens == nil || *usage.TotalTokens != 3 {
		t.Fatalf("total-only usage = %+v", usage)
	}
	if usage := parseTokenUsage([]byte(`{"completion_tokens":2}`)); usage == nil || usage.OutputTokens == nil || *usage.OutputTokens != 2 {
		t.Fatalf("output-only usage = %+v", usage)
	}
}
