package model

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestWarrantyOutputRejectsAmbiguousEnvelopeAndUnboundedFields(t *testing.T) {
	valid := `{"claim":"月数一致","relation":"compatible","quotes":[]}`
	for _, raw := range []string{
		valid + "trailer", "some explanation " + valid, valid + valid,
		`{"relation":"compatible","unknown":"ignored"}`,
		`{"relation":"compatible","quotes":[{"document_id":"a","quote":"x","bbox":[0,1]}]}`,
		"```json\n" + valid + "\n```trailer", "null", "[]",
		`{"relation":"compatible","claim":"` + strings.Repeat("x", 1001) + `"}`,
		`{"relation":"compatible","quotes":[{"document_id":"a","quote":"` + strings.Repeat("x", 10001) + `"}]}`,
	} {
		if _, err := ParseWarranty(raw); !errors.Is(err, ErrUnusable) {
			t.Fatalf("accepted ambiguous output: %q (%v)", raw[:min(len(raw), 120)], err)
		}
	}
	for _, raw := range []string{valid, "```json\n" + valid + "\n```", "```\n" + valid + "\n```", "```json\r\n" + valid + "\r\n```"} {
		if _, err := ParseWarranty(raw); err != nil {
			t.Fatalf("valid envelope: %v", err)
		}
	}
}

func TestModelCompletionResponseBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body string
		want error
	}{
		{"truncated", 200, `{"choices":[{"finish_reason":"length","message":{"content":"质保36个月"}}]}`, ErrUnusable},
		{"filtered", 200, `{"choices":[{"finish_reason":"content_filter","message":{"content":"text"}}]}`, ErrUnusable},
		{"empty", 200, `{"choices":[]}`, ErrUnusable},
		{"not-json", 200, "not json", ErrUnusable},
		{"oversized", 200, strings.Repeat(" ", (1<<20)+1), ErrUnusable},
		{"http503", 503, "offline", ErrRequest},
		{"structured-error", 401, `{"error":{"message":"key=secret-key rejected"}}`, ErrRequest},
		{"success", 200, `{"choices":[{"finish_reason":"stop","message":{"content":"质保36个月"}}]}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := New(Config{BaseURL: "http://model.example/v1", ChatModel: "panda-local/chat", OCRModel: "vision", APIKey: "secret-key"})
			client.WithTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer secret-key" {
					t.Fatalf("upstream request %v", r)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["model"] != "vision" {
					t.Fatalf("request body %v %v", body, err)
				}
				return response(tc.code, tc.body), nil
			}))
			text, err := client.Transcribe(context.Background(), "image/png", []byte{0x89, 'P', 'N', 'G'})
			if tc.want == nil {
				if err != nil || text != "质保36个月" {
					t.Fatalf("successful transcription %q %v", text, err)
				}
			} else if !errors.Is(err, tc.want) || strings.Contains(err.Error(), "secret-key") {
				t.Fatalf("wrong or unsafe error: %v", err)
			}
		})
	}
}

func TestModelLabelsDoNotExposeURLCredentialsOrQuerySecrets(t *testing.T) {
	client := New(Config{BaseURL: "https://user:password@model.example/v1?access_token=another-secret", ChatModel: "chat", OCRModel: "ocr", APIKey: "api-secret"})
	client.WithTransport(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(200, `{"data":[{"id":"chat"},{"id":"ocr"}]}`), nil
	}))
	status, label := client.Reachable(context.Background())
	if status != "connected" || !strings.Contains(label, "model.example/v1") {
		t.Fatalf("endpoint status %q %q", status, label)
	}
	for _, secret := range []string{"user", "password", "access_token", "another-secret", "api-secret"} {
		if strings.Contains(label, secret) {
			t.Fatalf("label exposes %q: %q", secret, label)
		}
	}
}

func TestModelAvailabilityIsNotInvented(t *testing.T) {
	for _, body := range []string{`{"data":[]}`, `{"data":[{"id":"chat"}]}`, `not-json`} {
		client := New(Config{BaseURL: "http://model.example/v1", ChatModel: "chat", OCRModel: "ocr"})
		client.WithTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Authorization") != "" || r.URL.Path != "/v1/models" {
				t.Fatalf("model list request %v", r)
			}
			return response(200, body), nil
		}))
		if status, _ := client.Reachable(context.Background()); status != "unreachable" {
			t.Fatalf("availability invented from %s", body)
		}
	}
	client := New(Config{})
	if _, err := client.Warranty(context.Background(), nil); !errors.Is(err, ErrRequest) {
		t.Fatalf("unconfigured client: %v", err)
	}
	if _, err := client.Transcribe(context.Background(), "image/png", nil); !errors.Is(err, ErrUnusable) {
		t.Fatalf("empty image: %v", err)
	}
	if status, _ := client.Reachable(context.Background()); status != "unreachable" {
		t.Fatalf("unconfigured endpoint status %s", status)
	}
}

func TestModelConfigurationUsesExplicitEnvironment(t *testing.T) {
	t.Setenv("LLM_BASE_URL", " http://localhost:9090/v1 ")
	t.Setenv("LLM_MODEL", " configured-chat ")
	t.Setenv("OCR_MODEL", " configured-vision ")
	t.Setenv("LLM_API_KEY", " configured-secret ")
	cfg := FromEnv()
	if cfg.BaseURL != "http://localhost:9090/v1" || cfg.ChatModel != "configured-chat" || cfg.OCRModel != "configured-vision" || cfg.APIKey != "configured-secret" {
		t.Fatal("environment configuration was not preserved")
	}
	if New(cfg).Name() != "configured-chat" {
		t.Fatal("model reference mismatch")
	}
	t.Setenv("LLM_MODEL", "")
	if FromEnv().ChatModel != DefaultChatModel {
		t.Fatal("default model changed")
	}
}
