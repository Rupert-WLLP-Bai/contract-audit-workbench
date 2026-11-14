// File metadata (manifest mirror):
// Purpose: Exercises bounded facts response shape, frozen plain/native request modes, model configuration drift and physical-call purpose tracking without relying on real-provider quality.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type factsPurposeObserver struct{ purpose string }

func (observer *factsPurposeObserver) StartModelCall(ctx context.Context, _ string) (context.Context, func(CallObservation)) {
	observer.purpose = CallPurpose(ctx)
	return ctx, func(CallObservation) {}
}

func TestParseContractFactsRequiresExactBoundedFields(t *testing.T) {
	fields := make([]ContractFactsField, 0, len(contractFactsFields))
	for _, name := range contractFactsFields {
		fields = append(fields, ContractFactsField{Field: name, Status: "unknown", Value: "", Citations: []ContractFactsCitation{}})
	}
	valid, err := json.Marshal(contractFactsResponse{Fields: fields})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseContractFacts(string(valid))
	if err != nil || len(parsed.Fields) != len(contractFactsFields) {
		t.Fatalf("valid unknown facts rejected: fields=%d error=%v", len(parsed.Fields), err)
	}

	withExtraKey := strings.TrimSuffix(string(valid), "}") + `,"risk_confirmed":true}`
	if _, err := ParseContractFacts(withExtraKey); !errors.Is(err, ErrUnusable) {
		t.Fatalf("unknown response key was accepted: %v", err)
	}
	fields[0].Value = "model claimed absent"
	invalidUnknown, _ := json.Marshal(contractFactsResponse{Fields: fields})
	if _, err := ParseContractFacts(string(invalidUnknown)); !errors.Is(err, ErrUnusable) {
		t.Fatalf("unknown field with a value was accepted: %v", err)
	}
	fields[0] = ContractFactsField{Field: "parties", Status: "known", Value: "甲方🌙", Citations: []ContractFactsCitation{{Quote: "🌙"}}}
	fields[1] = fields[0]
	duplicate, _ := json.Marshal(contractFactsResponse{Fields: fields})
	if _, err := ParseContractFacts(string(duplicate)); !errors.Is(err, ErrUnusable) {
		t.Fatalf("duplicate field identity was accepted: %v", err)
	}
	withModelOffsets := strings.Replace(string(duplicate), `"quote":"🌙"`, `"quote":"🌙","start":1,"end":2`, 1)
	if _, err := ParseContractFacts(withModelOffsets); !errors.Is(err, ErrUnusable) {
		t.Fatalf("model-computed citation offsets were accepted: %v", err)
	}
}

func TestContractFactsCapabilityIdentityIsSeparateAndModelSpecific(t *testing.T) {
	base := strings.Repeat("a", 64)
	identity := ContractFactsConfigurationID(base, "selected-chat")
	if identity == "" || len(identity) != 64 || identity == ContractFactsConfigurationID(base, "other-chat") ||
		identity == ContractFactsConfigurationID(strings.Repeat("b", 64), "selected-chat") {
		t.Fatal("facts capability identity does not pin the selected model and base configuration")
	}
	if ContractFactsConfigurationID("", "selected-chat") != "" || ContractFactsConfigurationID(base, " ") != "" {
		t.Fatal("facts capability identity accepted an incomplete frozen configuration")
	}
}

func TestContractFactsSupportsStrictPlainModeAndFreezesNativeSchemaMode(t *testing.T) {
	t.Setenv("CAW_STRUCTURED_OUTPUT_PROVIDER_URL", "https://provider.example/v1")
	t.Setenv("CAW_STRUCTURED_OUTPUT_REVIEW_MODEL", "selected-chat")
	cfg := Config{BaseURL: "https://provider.example/v1", ChatModel: "selected-chat", OCRModel: "selected-ocr"}
	ctx := context.WithValue(context.Background(), structuredReviewKey{}, "contract_facts")

	t.Setenv("CAW_STRUCTURED_OUTPUT_ENABLED", "false")
	plain := New(cfg)
	plainFactsID := plain.ContractFactsConfigurationID()
	if plainFactsID == "" {
		t.Fatal("a valid plain JSON model configuration could not freeze the strict facts capability")
	}
	_, plainPayload, err := plain.newChatRequest(ctx, cfg.ChatModel, []chatMessage{{Role: "user", Content: "facts"}}, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	var plainBody map[string]json.RawMessage
	if err := json.Unmarshal(plainPayload.prefix, &plainBody); err != nil {
		t.Fatal(err)
	}
	if _, found := plainBody["response_format"]; found {
		t.Fatal("plain facts mode sent an unconfigured native response schema")
	}

	t.Setenv("CAW_STRUCTURED_OUTPUT_ENABLED", "true")
	if plain.ContractFactsConfigurationID() != "" {
		t.Fatal("an existing plain-mode client accepted a changed native structured configuration")
	}
	native := New(cfg)
	nativeFactsID := native.ContractFactsConfigurationID()
	if nativeFactsID == "" || nativeFactsID == plainFactsID || native.ConfigurationID() == plain.ConfigurationID() {
		t.Fatal("changing native structured mode did not create a new frozen facts configuration")
	}
	_, nativePayload, err := native.newChatRequest(ctx, cfg.ChatModel, []chatMessage{{Role: "user", Content: "facts"}}, ContractFactsMaxOutputTokens, contractFactsReasoningEffort)
	if err != nil {
		t.Fatal(err)
	}
	var nativeBody map[string]json.RawMessage
	if err := json.Unmarshal(nativePayload.prefix, &nativeBody); err != nil {
		t.Fatal(err)
	}
	if _, found := nativeBody["response_format"]; !found {
		t.Fatal("native facts mode omitted the allowlisted response schema")
	}
	if string(nativeBody["response_format"]) != contractFactsResponseFormat || string(nativeBody["reasoning_effort"]) != `"high"` {
		t.Fatal("native facts mode changed the frozen response schema or reasoning effort")
	}
}

func TestContractFactsPlainModeSendsBoundedRequestAndTracksPurpose(t *testing.T) {
	t.Setenv("CAW_STRUCTURED_OUTPUT_ENABLED", "false")
	fields := make([]ContractFactsField, 0, len(contractFactsFields))
	for _, name := range contractFactsFields {
		fields = append(fields, ContractFactsField{Field: name, Status: "unknown", Value: "", Citations: []ContractFactsCitation{}})
	}
	completion, err := json.Marshal(contractFactsResponse{Fields: fields})
	if err != nil {
		t.Fatal(err)
	}
	var requestBody map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("read request: %v", readErr)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if decodeErr := json.Unmarshal(body, &requestBody); decodeErr != nil {
			t.Errorf("decode request: %v", decodeErr)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeJSONResponse(t, w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": string(completion)}}},
		})
	}))
	defer server.Close()
	client := New(Config{BaseURL: server.URL, ChatModel: "selected-chat", OCRModel: "selected-ocr"})
	observer := &factsPurposeObserver{}
	contractText := "甲方🌙合同正文"
	suggestion, err := client.ContractFacts(WithCallObserver(context.Background(), observer), contractText, false)
	if err != nil || len(suggestion.Fields) != len(contractFactsFields) {
		t.Fatalf("strict plain facts call failed: fields=%d error=%v", len(suggestion.Fields), err)
	}
	if observer.purpose != CallPurposeContractFacts || requestBody["response_format"] != nil || !strings.Contains(string(requestBody["messages"]), contractText) {
		t.Fatalf("facts request purpose or plain mode changed: purpose=%q body=%s", observer.purpose, requestBody)
	}
	var messages []chatMessage
	if err := json.Unmarshal(requestBody["messages"], &messages); err != nil || len(messages) != 2 || messages[0].Content != contractFactsSystemPrompt || !strings.Contains(messages[0].Content.(string), contractFactsResponseSchema) {
		t.Fatalf("plain facts request omitted the shared response schema: %v", err)
	}
	if string(requestBody["reasoning_effort"]) != `"high"` || string(requestBody["max_tokens"]) != "32000" {
		t.Fatal("facts request changed its frozen reasoning or token limit")
	}
}

func TestContractFactsResponseSchemaIsStrictAndAllowlisted(t *testing.T) {
	format := StructuredResponseFormat("contract_facts")
	if len(format) == 0 || StructuredResponseFormat("arbitrary") != nil {
		t.Fatal("facts schema was not allowlisted")
	}
	var request struct {
		JSONSchema struct {
			Strict bool `json:"strict"`
			Schema struct {
				AdditionalProperties bool `json:"additionalProperties"`
			} `json:"schema"`
		} `json:"json_schema"`
	}
	if err := json.Unmarshal(format, &request); err != nil || !request.JSONSchema.Strict || request.JSONSchema.Schema.AdditionalProperties {
		t.Fatalf("facts schema is not strict: %v", err)
	}
	if strings.Contains(string(format), `"start"`) || strings.Contains(string(format), `"end"`) {
		t.Fatal("model response schema still requires server-computed positions")
	}
}
