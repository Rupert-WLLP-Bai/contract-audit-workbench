package modelgateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"contractaudit/internal/model"
)

// imageTokenBudget is an operator-supplied contract for one provider and OCR
// model. An unset contract retains byte-based reservations for every request.
type imageTokenBudget struct {
	providerURL string
	modelID     string
	imageTokens int64
}

func imageTokenBudgetFromEnv() (imageTokenBudget, error) {
	budget := imageTokenBudget{
		providerURL: strings.TrimSpace(os.Getenv("MODEL_OCR_IMAGE_PROVIDER_URL")),
		modelID:     model.GatewayID(os.Getenv("MODEL_OCR_IMAGE_MODEL")),
	}
	raw := strings.TrimSpace(os.Getenv("MODEL_OCR_IMAGE_TOKEN_UPPER_BOUND"))
	if raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			return imageTokenBudget{}, errors.New("MODEL_OCR_IMAGE_TOKEN_UPPER_BOUND must be a positive integer")
		}
		budget.imageTokens = value
	}
	if err := budget.validate(); err != nil {
		return imageTokenBudget{}, err
	}
	return budget, nil
}

func (budget imageTokenBudget) validate() error {
	if budget == (imageTokenBudget{}) {
		return nil
	}
	if budget.providerURL == "" || budget.modelID == "" || budget.imageTokens < 1 ||
		budget.imageTokens > math.MaxInt64-maxRequestBytes || validateUpstreamURL(budget.providerURL) != nil {
		return errors.New("OCR image token budget requires a provider URL, model ID, and positive bounded token upper limit")
	}
	return nil
}

// inputUpperBound only excludes the base64 data after the complete canonical
// one-image OCR request and image have been verified. Every other byte remains
// in the reservation, and unsupported shapes retain the original byte bound.
func (budget imageTokenBudget) inputUpperBound(body []byte, requested string, upstream upstreamConfig, limit int64) int64 {
	fallback := int64(len(body))
	if budget.imageTokens == 0 || budget.providerURL != upstream.ProviderURL ||
		budget.modelID != model.GatewayID(upstream.OCRModel) || budget.modelID != model.GatewayID(requested) {
		return fallback
	}
	const marker = `"url":"data:image/`
	start := bytes.Index(body, []byte(marker))
	if start < 0 {
		return fallback
	}
	start += len(marker)
	format := ""
	switch {
	case bytes.HasPrefix(body[start:], []byte("png;base64,")):
		format = "png"
		start += len("png;base64,")
	case bytes.HasPrefix(body[start:], []byte("jpeg;base64,")):
		format = "jpeg"
		start += len("jpeg;base64,")
	default:
		return fallback
	}
	endOffset := bytes.IndexByte(body[start:], '"')
	if endOffset <= 0 {
		return fallback
	}
	end := start + endOffset
	remaining := int64(len(body) - (end - start))
	bound := remaining + budget.imageTokens // validated against overflow at startup
	// Both possible accounting modes would reject; avoid copying a huge prompt.
	if bound > limit && fallback > limit {
		return fallback
	}
	withoutImage := make([]byte, 0, remaining)
	withoutImage = append(withoutImage, body[:start]...)
	withoutImage = append(withoutImage, body[end:]...)
	if !canonicalSingleImageOCR(withoutImage, format) || !validEmbeddedImage(body[start:end], format) {
		return fallback
	}
	return bound
}

type imageOCRText struct {
	Text string `json:"text"`
	Type string `json:"type"`
}

type imageOCRPart struct {
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
	Type string `json:"type"`
}

type imageOCRMessage struct {
	Role    string            `json:"role"`
	Content []json.RawMessage `json:"content"`
}

type imageOCRRequest struct {
	Model       string            `json:"model"`
	Temperature float64           `json:"temperature"`
	MaxTokens   int               `json:"max_tokens"`
	Messages    []imageOCRMessage `json:"messages"`
}

// Re-encoding the bounded non-image envelope rejects unknown fields, duplicate
// keys, alternative encodings and additional messages or image parts. It also
// avoids copying the potentially large base64 image into a JSON string.
func canonicalSingleImageOCR(body []byte, format string) bool {
	var request imageOCRRequest
	if err := json.Unmarshal(body, &request); err != nil || len(request.Messages) != 1 ||
		request.Messages[0].Role != "user" || len(request.Messages[0].Content) != 2 || request.MaxTokens < 1 {
		return false
	}
	var text imageOCRText
	var picture imageOCRPart
	if json.Unmarshal(request.Messages[0].Content[0], &text) != nil ||
		json.Unmarshal(request.Messages[0].Content[1], &picture) != nil ||
		text.Type != "text" || picture.Type != "image_url" || picture.ImageURL.URL != "data:image/"+format+";base64," {
		return false
	}
	canonical := struct {
		Model       string  `json:"model"`
		Temperature float64 `json:"temperature"`
		MaxTokens   int     `json:"max_tokens"`
		Messages    []struct {
			Role    string `json:"role"`
			Content []any  `json:"content"`
		} `json:"messages"`
	}{Model: request.Model, Temperature: request.Temperature, MaxTokens: request.MaxTokens}
	canonical.Messages = append(canonical.Messages, struct {
		Role    string `json:"role"`
		Content []any  `json:"content"`
	}{Role: "user", Content: []any{text, picture}})
	encoded, err := json.Marshal(canonical)
	return err == nil && bytes.Equal(body, encoded)
}

// Match the existing document-image pixel bound before decoding. Full decoding
// and a final base64 drain reject malformed images and trailing invalid data.
func validEmbeddedImage(encoded []byte, expectedFormat string) bool {
	const maxPixels = 40_000_000
	decoder := func() io.Reader {
		return base64.NewDecoder(base64.StdEncoding.Strict(), bytes.NewReader(encoded))
	}
	config, format, err := image.DecodeConfig(decoder())
	if err != nil || format != expectedFormat || config.Width < 1 || config.Height < 1 || config.Width > maxPixels/config.Height {
		return false
	}
	imageData := decoder()
	_, format, err = image.Decode(imageData)
	if err != nil || format != expectedFormat {
		return false
	}
	_, err = io.Copy(io.Discard, imageData)
	return err == nil
}
