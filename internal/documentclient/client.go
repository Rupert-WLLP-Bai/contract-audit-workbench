package documentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"contractaudit/internal/store"
)

const maxPreparedDocumentBytes = 64 << 20

// Client calls the document owner's delegated preparation endpoint.
type Client struct {
	endpoint   string
	credential string
	http       *http.Client
}

// New creates a bounded client for the document-service internal endpoint.
func New(endpoint, credential string, timeout time.Duration) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("document-service endpoint must be an HTTP(S) origin or base path")
	}
	if len(credential) < 32 {
		return nil, errors.New("document-service credential must contain at least 32 bytes")
	}
	if timeout <= 0 {
		return nil, errors.New("document-service timeout must be positive")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/internal/v1/prepare"
	parsed.RawPath = ""
	return &Client{
		endpoint: parsed.String(), credential: credential,
		http: &http.Client{Timeout: timeout},
	}, nil
}

// PrepareAttemptDocument requests only a document selected by the authoritative
// job and attempt. The service resolves tenant and actor from PostgreSQL.
func (c *Client) PrepareAttemptDocument(ctx context.Context, request store.PrepareDocumentRequest) (store.FrozenDocument, error) {
	if ctx == nil {
		return store.FrozenDocument{}, errors.New("document preparation context is required")
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return store.FrozenDocument{}, fmt.Errorf("encode document preparation request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return store.FrozenDocument{}, fmt.Errorf("create document preparation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Contract-Service-Key", c.credential)
	response, err := c.http.Do(req)
	if err != nil {
		return store.FrozenDocument{}, fmt.Errorf("document preparation request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxPreparedDocumentBytes+1))
	if err != nil {
		return store.FrozenDocument{}, fmt.Errorf("read document preparation response: %w", err)
	}
	if len(body) > maxPreparedDocumentBytes {
		return store.FrozenDocument{}, errors.New("document preparation response exceeds the size limit")
	}
	if response.StatusCode != http.StatusOK {
		return store.FrozenDocument{}, fmt.Errorf("document preparation service returned HTTP %d", response.StatusCode)
	}
	var frozen store.FrozenDocument
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&frozen); err != nil {
		return store.FrozenDocument{}, fmt.Errorf("decode document preparation response: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return store.FrozenDocument{}, errors.New("document preparation response contains trailing data")
	}
	return frozen, nil
}

var _ store.DocumentPreparer = (*Client)(nil)
