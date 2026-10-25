package model

import (
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
