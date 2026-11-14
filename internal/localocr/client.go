// File metadata (manifest mirror):
// Purpose: Calls the loopback MinerU file-parse API with bounded requests, stable polling identity and explicit cancellation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package localocr integrates the explicitly configured local OCR service.
package localocr

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxImageBytes = 24 << 20
const maxResultBytes = 32 << 20

// Client uses the official local file-parse API; its endpoint is restricted to
// a literal loopback address and never follows redirects.
type Client struct {
	endpoint string
	http     *http.Client
}

func NewClient(endpoint string) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") || u.Port() == "" {
		return nil, errors.New("local OCR endpoint must be a literal loopback HTTP origin")
	}
	return &Client{endpoint: strings.TrimRight(u.String(), "/"), http: &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// ParseImage submits one image once, and polls only the assigned request ID.
// Returned bytes are the bounded official archive, not yet a trusted transcript.
func (c *Client) ParseImage(ctx context.Context, media string, image []byte) (output []byte, resultErr error) {
	if ctx == nil || len(image) == 0 || len(image) > maxImageBytes {
		return nil, errors.New("invalid local OCR image")
	}
	if media != "image/png" && media != "image/jpeg" && media != "image/jpg" {
		media = http.DetectContentType(image)
	}
	extension := ""
	switch media {
	case "image/png":
		extension = ".png"
	case "image/jpeg", "image/jpg":
		extension = ".jpg"
	default:
		return nil, errors.New("unsupported local OCR image type")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, errors.New("cannot allocate local OCR request identity")
	}
	id := hex.EncodeToString(random[:])
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	defer func() {
		if err := ctx.Err(); err != nil {
			output, resultErr = nil, err
		}
		if resultErr != nil {
			resultErr = fmt.Errorf("local OCR request %s: %w", id, resultErr)
		}
	}()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "image"+extension)
	if err != nil {
		return nil, errors.New("cannot prepare local OCR request")
	}
	if _, err = part.Write(image); err != nil {
		return nil, errors.New("cannot prepare local OCR request")
	}
	for key, value := range map[string]string{"request_id": id, "tier": "flash", "ocr_mode": "ocr", "wait_timeout": "30", "output_formats": "default"} {
		if err = form.WriteField(key, value); err != nil {
			return nil, errors.New("cannot prepare local OCR request")
		}
	}
	if err = form.Close(); err != nil {
		return nil, errors.New("cannot prepare local OCR request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/file_parse", &body)
	if err != nil {
		return nil, errors.New("cannot prepare local OCR request")
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	response, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("local OCR submission outcome unknown")
	}
	result, status, err := readResponse(response)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return result, nil
	}
	if status != http.StatusAccepted {
		return nil, errors.New("local OCR submission refused")
	}
	for {
		var state struct {
			RequestID string `json:"request_id"`
			Status    string `json:"status"`
		}
		if json.Unmarshal(result, &state) != nil || state.RequestID != id {
			return nil, errors.New("invalid local OCR status")
		}
		switch state.Status {
		case "completed":
			req, err = http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/v1/file_parse/"+id+"/result", nil)
			if err != nil {
				return nil, errors.New("cannot request local OCR result")
			}
			response, err = c.http.Do(req)
			if err != nil {
				return nil, errors.New("cannot read local OCR result")
			}
			result, status, err = readResponse(response)
			if err != nil {
				return nil, err
			}
			if status != http.StatusOK {
				return nil, errors.New("local OCR result unavailable")
			}
			return result, nil
		case "failed", "canceled", "submission_unknown":
			return nil, errors.New("local OCR task did not complete")
		case "prepared", "submitting", "waiting-file", "pending", "running", "processing", "queued":
		default:
			return nil, errors.New("unknown local OCR status")
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/v1/file_parse/"+id, nil)
		if err != nil {
			return nil, errors.New("cannot request local OCR status")
		}
		response, err = c.http.Do(req)
		if err != nil {
			return nil, errors.New("cannot read local OCR status")
		}
		result, status, err = readResponse(response)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, errors.New("local OCR status unavailable")
		}
	}
}

func readResponse(response *http.Response) ([]byte, int, error) {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResultBytes+1))
	if err != nil || len(body) > maxResultBytes {
		return nil, response.StatusCode, errors.New("local OCR response unavailable or exceeds limit")
	}
	return body, response.StatusCode, nil
}

// Engine binds the verified deployment profile to the official local service.
type Engine struct {
	config Config
	client *Client
}

func NewEngine(config Config) (*Engine, error) {
	client, err := NewClient(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if config.ProfileID == "" || config.Python == "" {
		return nil, errors.New("local OCR profile is incomplete")
	}
	return &Engine{config: config, client: client}, nil
}

func (e *Engine) ProfileID() string { return e.config.ProfileID }
func (e *Engine) Transcribe(ctx context.Context, media string, body []byte) (string, error) {
	archive, err := e.client.ParseImage(ctx, media, body)
	if err != nil {
		return "", err
	}
	return Extract(ctx, e.config.Python, archive)
}
