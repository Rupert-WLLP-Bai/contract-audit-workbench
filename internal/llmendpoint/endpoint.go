// Package llmendpoint validates and probes an operator-supplied,
// OpenAI-compatible model endpoint and seals its provider credential.
//
// The endpoint is platform control data, not case data. It never contains a
// human decision, and the provider key is never returned to the browser.
package llmendpoint

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// ProtocolOpenAICompatible is the only wire protocol the review pipeline
	// currently implements. Storing it explicitly keeps the choice visible and
	// lets an unsupported protocol be rejected instead of silently treated as
	// OpenAI.
	ProtocolOpenAICompatible = "openai-compatible"

	// EncryptionKeyEnv names the base64-encoded 32-byte AES key shared by the
	// core admin surface (seal/probe) and the model gateway (open). It is not
	// the provider key and must not be reused for any other purpose.
	EncryptionKeyEnv = "LLM_CONFIG_KEY"

	maxBaseURLBytes = 2048
	maxModels       = 512
	maxModelIDBytes = 200
	maxProbeBytes   = 1 << 20
	probeTimeout    = 12 * time.Second
	sealAAD         = "contract-audit/llm-endpoint/v1"
)

var (
	// ErrEncryptionKey means the deployment did not configure a usable
	// LLM_CONFIG_KEY. Callers may still probe with a key supplied in memory,
	// but cannot persist or reopen it.
	ErrEncryptionKey = errors.New("LLM_CONFIG_KEY must be base64 encoding of exactly 32 bytes")
	// ErrInvalidEndpoint means the URL or protocol cannot be used.
	ErrInvalidEndpoint = errors.New("invalid LLM endpoint")
	// ErrInvalidModels means the endpoint returned no usable model identity.
	ErrInvalidModels = errors.New("invalid LLM model list")
)

// Config is the active, non-secret endpoint identity. A Config is active only
// when both role selections are present in Models.
type Config struct {
	Name        string
	Protocol    string
	BaseURL     string
	Models      []string
	ReviewModel string
	OCRModel    string
}

// Active reports whether this configuration can drive review and OCR calls.
func (c Config) Active() bool {
	return c.Protocol == ProtocolOpenAICompatible && c.BaseURL != "" &&
		c.ReviewModel != "" && c.OCRModel != "" &&
		contains(c.Models, c.ReviewModel) && contains(c.Models, c.OCRModel)
}

// Normalize validates and canonicalizes an operator input. Empty role
// selections are allowed while an endpoint is being registered; such a
// configuration is stored but not active until both are chosen.
func Normalize(name, protocol, baseURL string, models []string, reviewModel, ocrModel string) (Config, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Config{}, fmt.Errorf("%w: name is required", ErrInvalidEndpoint)
	}
	if utf8.RuneCountInString(name) > 80 {
		return Config{}, fmt.Errorf("%w: name is too long", ErrInvalidEndpoint)
	}
	protocol = strings.TrimSpace(protocol)
	if protocol == "" {
		protocol = ProtocolOpenAICompatible
	}
	if protocol != ProtocolOpenAICompatible {
		return Config{}, fmt.Errorf("%w: unsupported protocol", ErrInvalidEndpoint)
	}
	baseURL, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return Config{}, err
	}
	normalized, err := normalizeModels(models)
	if err != nil {
		return Config{}, err
	}
	reviewModel = strings.TrimSpace(reviewModel)
	ocrModel = strings.TrimSpace(ocrModel)
	if reviewModel != "" && !contains(normalized, reviewModel) {
		return Config{}, fmt.Errorf("%w: review model is not in the endpoint model list", ErrInvalidEndpoint)
	}
	if ocrModel != "" && !contains(normalized, ocrModel) {
		return Config{}, fmt.Errorf("%w: OCR model is not in the endpoint model list", ErrInvalidEndpoint)
	}
	return Config{Name: name, Protocol: protocol, BaseURL: baseURL, Models: normalized, ReviewModel: reviewModel, OCRModel: ocrModel}, nil
}

// NormalizeBaseURL accepts only an absolute http(s) URL without userinfo,
// query, or fragment. Trailing slashes are removed so /models is appended once.
func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxBaseURLBytes {
		return "", fmt.Errorf("%w: base URL is missing or too long", ErrInvalidEndpoint)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil ||
		(u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%w: base URL must be an absolute http(s) URL without credentials, query, or fragment", ErrInvalidEndpoint)
	}
	for _, r := range u.Host {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", fmt.Errorf("%w: base URL host is invalid", ErrInvalidEndpoint)
		}
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func normalizeModels(models []string) ([]string, error) {
	if len(models) == 0 || len(models) > maxModels {
		return nil, fmt.Errorf("%w: model list must contain between 1 and %d ids", ErrInvalidModels, maxModels)
	}
	seen := make(map[string]bool, len(models))
	out := make([]string, 0, len(models))
	for _, raw := range models {
		id := strings.TrimSpace(raw)
		if id == "" || len(id) > maxModelIDBytes || !utf8.ValidString(id) {
			return nil, fmt.Errorf("%w: model id is empty, too long, or invalid", ErrInvalidModels)
		}
		for _, r := range id {
			if unicode.IsControl(r) || unicode.IsSpace(r) {
				return nil, fmt.Errorf("%w: model id contains whitespace or control characters", ErrInvalidModels)
			}
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: model list is empty", ErrInvalidModels)
	}
	return out, nil
}

// ProbeResult is the observable catalog returned by the provider.
type ProbeResult struct {
	Models    []string `json:"models"`
	LatencyMS int64    `json:"latency_ms"`
}

// Probe asks the endpoint for its model catalog. The provider key stays in
// memory and is never included in an error.
func Probe(ctx context.Context, protocol, baseURL, apiKey string) (ProbeResult, error) {
	protocol = strings.TrimSpace(protocol)
	if protocol == "" {
		protocol = ProtocolOpenAICompatible
	}
	if protocol != ProtocolOpenAICompatible {
		return ProbeResult{}, fmt.Errorf("%w: unsupported protocol", ErrInvalidEndpoint)
	}
	baseURL, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return ProbeResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("%w: cannot build probe request", ErrInvalidEndpoint)
	}
	request.Header.Set("Accept", "application/json")
	if key := strings.TrimSpace(apiKey); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: probeTimeout}
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("model endpoint is unreachable")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxProbeBytes+1))
	if err != nil || len(raw) > maxProbeBytes {
		return ProbeResult{}, fmt.Errorf("model endpoint response is unreadable or too large")
	}
	if response.StatusCode != http.StatusOK {
		return ProbeResult{}, fmt.Errorf("model endpoint returned HTTP %d", response.StatusCode)
	}
	ids, err := decodeCatalog(raw)
	if err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{Models: ids, LatencyMS: time.Since(started).Milliseconds()}, nil
}

func decodeCatalog(raw []byte) ([]string, error) {
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		// Some local servers expose the Ollama-style models array. Accept it
		// only as a fallback; the OpenAI-compatible data array wins.
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("%w: model catalog is not JSON", ErrInvalidModels)
	}
	ids := make([]string, 0, len(body.Data)+len(body.Models))
	for _, item := range body.Data {
		ids = append(ids, item.ID)
	}
	if len(ids) == 0 {
		for _, item := range body.Models {
			ids = append(ids, item.Name)
		}
	}
	normalized, err := normalizeModels(ids)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	return normalized, nil
}

// Seal encrypts a non-empty provider key with AES-256-GCM. An empty key stays
// empty, which is valid for a local endpoint that needs no credential.
func Seal(apiKey string) ([]byte, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, nil
	}
	key, err := EncryptionKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrEncryptionKey
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrEncryptionKey
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("cannot generate LLM credential nonce")
	}
	return aead.Seal(nonce, nonce, []byte(apiKey), []byte(sealAAD)), nil
}

// Open decrypts a sealed provider key. An empty ciphertext yields an empty key.
func Open(ciphertext []byte) (string, error) {
	if len(ciphertext) == 0 {
		return "", nil
	}
	key, err := EncryptionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", ErrEncryptionKey
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", ErrEncryptionKey
	}
	nonceSize := aead.NonceSize()
	if len(ciphertext) < nonceSize+aead.Overhead() {
		return "", fmt.Errorf("sealed LLM credential is malformed")
	}
	nonce, body := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := aead.Open(nil, nonce, body, []byte(sealAAD))
	if err != nil {
		return "", fmt.Errorf("sealed LLM credential cannot be opened")
	}
	return string(plaintext), nil
}

// EncryptionKey reads and validates LLM_CONFIG_KEY.
func EncryptionKey() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(EncryptionKeyEnv))
	if raw == "" {
		return nil, ErrEncryptionKey
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(raw)
	}
	if err != nil || len(key) != 32 {
		return nil, ErrEncryptionKey
	}
	return key, nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
