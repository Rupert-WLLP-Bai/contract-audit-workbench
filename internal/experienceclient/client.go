// Package experienceclient delivers a fenced worker result to the Core owner.
package experienceclient

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

type Client struct {
	endpoint   string
	credential string
	http       *http.Client
}

func New(endpoint, credential string, timeout time.Duration) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || !isHTTPOrHTTPS(parsed.Scheme) ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("core service endpoint must be an HTTP(S) origin or base path")
	}
	if len(credential) < 32 || timeout <= 0 {
		return nil, errors.New("core result credential or timeout is invalid")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/internal/v1/experience-results"
	parsed.RawPath = ""
	return &Client{endpoint: parsed.String(), credential: credential, http: &http.Client{Timeout: timeout}}, nil
}

func isHTTPOrHTTPS(scheme string) bool {
	return scheme == "http" || scheme == "https"
}

func (c *Client) AcceptExperienceResult(ctx context.Context, delivery store.ExperienceResultDelivery) error {
	if ctx == nil {
		return errors.New("result delivery context is required")
	}
	body, err := json.Marshal(delivery)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Contract-Service-Key", c.credential)
	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("core result delivery failed: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("core result delivery returned HTTP %d", response.StatusCode)
	}
	return nil
}

var _ store.ExperienceResultAcceptor = (*Client)(nil)
