package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"contractaudit/internal/execution"
)

const (
	traceRequestMaxBytes  = 64 << 10
	traceMessageMaxBytes  = 32 << 10
	traceResponseMaxBytes = 128 << 10
	traceImagePlaceholder = "[图片输入已省略]"
)

type callTraceInput struct {
	RequestedModel string
	ReportedModel  string
	Payload        []byte
	Messages       []chatMessage
	StartedAt      time.Time
	FinishedAt     time.Time
	ResponseText   string
	Status         string
	ErrorCode      string
}

// buildModelCallTrace records bounded request and response previews plus the
// exact digests and byte counts of the full bodies. Images and over-budget
// text are omitted; the flags and sizes keep that omission explicit.
func buildModelCallTrace(input callTraceInput) *execution.ModelCallTrace {
	requestSum := sha256.Sum256(input.Payload)
	messages, requestTruncated := traceRequestMessages(input.Messages)
	trace := &execution.ModelCallTrace{
		RequestedModel:    input.RequestedModel,
		ReportedModel:     input.ReportedModel,
		Status:            input.Status,
		ErrorCode:         input.ErrorCode,
		StartedAt:         input.StartedAt.UTC().Format(time.RFC3339Nano),
		FinishedAt:        input.FinishedAt.UTC().Format(time.RFC3339Nano),
		RequestSHA256:     hex.EncodeToString(requestSum[:]),
		RequestBytes:      len(input.Payload),
		RequestMessages:   messages,
		RequestTruncated:  requestTruncated,
		ResponseBytes:     len(input.ResponseText),
		ResponseTruncated: false,
	}
	if input.ResponseText == "" {
		return trace
	}
	responseSum := sha256.Sum256([]byte(input.ResponseText))
	trace.ResponseSHA256 = hex.EncodeToString(responseSum[:])
	trace.ResponseText, trace.ResponseTruncated = truncateTraceString(input.ResponseText, traceResponseMaxBytes)
	return trace
}

func traceRequestMessages(messages []chatMessage) ([]execution.TraceMessage, bool) {
	out := make([]execution.TraceMessage, 0, len(messages))
	remaining := traceRequestMaxBytes
	truncated := false
	for _, message := range messages {
		if remaining <= 0 {
			out = append(out, execution.TraceMessage{Role: message.Role, Truncated: true})
			truncated = true
			continue
		}
		content, contentTruncated := traceContent(message.Content)
		if len(content) > remaining {
			content, _ = truncateTraceString(content, remaining)
			contentTruncated = true
		}
		remaining -= len(content)
		truncated = truncated || contentTruncated
		out = append(out, execution.TraceMessage{Role: message.Role, Content: content, Truncated: contentTruncated})
	}
	return out, truncated
}

func traceContent(content any) (string, bool) {
	switch value := content.(type) {
	case string:
		return truncateTraceString(value, traceMessageMaxBytes)
	case []any:
		parts := make([]string, 0, len(value))
		truncated := false
		for _, raw := range value {
			item, ok := raw.(map[string]any)
			if ok {
				if item["type"] == "image_url" {
					parts = append(parts, traceImagePlaceholder)
					truncated = true
					continue
				}
				if text, ok := item["text"].(string); ok {
					clipped, cut := truncateTraceString(text, traceMessageMaxBytes)
					parts = append(parts, clipped)
					truncated = truncated || cut
					continue
				}
			}
			encoded, err := json.Marshal(raw)
			if err != nil {
				truncated = true
				continue
			}
			clipped, cut := truncateTraceString(string(encoded), traceMessageMaxBytes)
			parts = append(parts, clipped)
			truncated = truncated || cut
		}
		joined, cut := truncateTraceString(strings.Join(parts, "\n"), traceMessageMaxBytes)
		return joined, truncated || cut
	default:
		encoded, err := json.Marshal(content)
		if err != nil {
			return "", true
		}
		return truncateTraceString(string(encoded), traceMessageMaxBytes)
	}
}

func truncateTraceString(value string, limit int) (string, bool) {
	if limit < 0 {
		limit = 0
	}
	if len(value) <= limit {
		return value, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut], true
}
