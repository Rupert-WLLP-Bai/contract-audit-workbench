// File metadata (manifest mirror):
// Purpose: Exercises the `model` package behavior through TestConfigurationIdentitySeparatesProvidersAndOmitsCredentials; these checks pin observable state, error handling, or wire/database compatibility for this module.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-25
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigurationIdentitySeparatesProvidersAndOmitsCredentials(t *testing.T) {
	base := Config{BaseURL: "http://127.0.0.1:3425/v1", ChatModel: "route/chat", OCRModel: "route/ocr", APIKey: "private-key"}
	identity := New(base).ConfigurationID()
	rotated := base
	rotated.APIKey = "rotated-private-key"
	if New(rotated).ConfigurationID() != identity || strings.Contains(identity, base.APIKey) || len(identity) != 64 {
		t.Fatal("credential rotation changed semantic identity or exposed secret")
	}
	for _, next := range []Config{
		{BaseURL: "http://127.0.0.1:3426/v1", ChatModel: base.ChatModel, OCRModel: base.OCRModel},
		{BaseURL: base.BaseURL, ChatModel: "other-chat", OCRModel: base.OCRModel},
		{BaseURL: base.BaseURL, ChatModel: base.ChatModel, OCRModel: "other-ocr"},
	} {
		if New(next).ConfigurationID() == identity {
			t.Fatal("changed endpoint/chat/OCR route retained old configuration identity")
		}
	}
	normalized := base
	normalized.BaseURL = " " + base.BaseURL + "/ "
	if New(normalized).ConfigurationID() != identity {
		t.Fatal("equivalent normalized endpoint changed identity")
	}
}

func TestStructuredOutputContractIdentity(t *testing.T) {
	t.Setenv("CAW_STRUCTURED_OUTPUT_ENABLED", "")
	t.Setenv("CAW_STRUCTURED_OUTPUT_PROVIDER_URL", "")
	t.Setenv("CAW_STRUCTURED_OUTPUT_REVIEW_MODEL", "")
	old := ConfigurationIDFor("https://provider.example/v1", "review", "ocr")
	id, enabled, err := StructuredConfigurationIDFor("https://provider.example/v1", "review", "ocr")
	if err != nil || enabled || id != old {
		t.Fatalf("disabled: %q %t %v", id, enabled, err)
	}
	t.Setenv("CAW_STRUCTURED_OUTPUT_ENABLED", "true")
	if _, _, err = StructuredConfigurationIDFor("https://provider.example/v1", "review", "ocr"); err == nil {
		t.Fatal("missing binding accepted")
	}
	t.Setenv("CAW_STRUCTURED_OUTPUT_PROVIDER_URL", "https://provider.example/v1")
	t.Setenv("CAW_STRUCTURED_OUTPUT_REVIEW_MODEL", "review")
	id, enabled, err = StructuredConfigurationIDFor("https://provider.example/v1", "review", "ocr")
	if err != nil || !enabled || id == old {
		t.Fatalf("enabled: %q %t %v", id, enabled, err)
	}
	if _, _, err = StructuredConfigurationIDFor("https://other.example/v1", "review", "ocr"); err == nil {
		t.Fatal("mismatched provider accepted")
	}
	if _, _, err = StructuredConfigurationIDFor("https://provider.example/v1", "other", "ocr"); err == nil {
		t.Fatal("mismatched model accepted")
	}
}

func TestStructuredReviewRequestOnly(t *testing.T) {
	t.Setenv("CAW_STRUCTURED_OUTPUT_ENABLED", "true")
	t.Setenv("CAW_STRUCTURED_OUTPUT_PROVIDER_URL", "https://provider.example/v1")
	t.Setenv("CAW_STRUCTURED_OUTPUT_REVIEW_MODEL", "review")
	client := New(Config{BaseURL: "https://provider.example/v1", ChatModel: "review", OCRModel: "ocr"})
	for _, tt := range []struct {
		kind string
		want bool
	}{{"warranty", true}, {"cloud_contract", true}, {"", false}} {
		ctx := context.Background()
		if tt.kind != "" {
			ctx = context.WithValue(ctx, structuredReviewKey{}, tt.kind)
		}
		_, payload, err := client.newChatRequest(ctx, "review", []chatMessage{{Role: "user", Content: "text"}}, 100, "")
		if err != nil {
			t.Fatal(err)
		}
		var data map[string]json.RawMessage
		if err = json.Unmarshal(payload.prefix, &data); err != nil {
			t.Fatal(err)
		}
		_, found := data["response_format"]
		if found != tt.want {
			t.Fatalf("kind %q: %s", tt.kind, payload.prefix)
		}
	}
}

func TestStructuredClientFailsClosedOnConfigurationDrift(t *testing.T) {
	t.Setenv("CAW_STRUCTURED_OUTPUT_ENABLED", "")
	t.Setenv("CAW_STRUCTURED_OUTPUT_PROVIDER_URL", "https://provider.example/v1")
	t.Setenv("CAW_STRUCTURED_OUTPUT_REVIEW_MODEL", "review")
	cfg := Config{BaseURL: "https://provider.example/v1", ChatModel: "review", OCRModel: "ocr"}
	client := New(cfg)
	t.Setenv("CAW_STRUCTURED_OUTPUT_ENABLED", "true")
	if client.ConfigurationError() == nil {
		t.Fatal("changed contract accepted")
	}
	if _, _, err := client.newChatRequest(context.Background(), "review", []chatMessage{{Role: "user", Content: "x"}}, 10, ""); err == nil {
		t.Fatal("changed contract dispatched")
	}
	t.Setenv("CAW_STRUCTURED_OUTPUT_REVIEW_MODEL", "wrong")
	bad := New(cfg)
	if bad.ConfigurationError() == nil {
		t.Fatal("invalid initial contract accepted")
	}
}
