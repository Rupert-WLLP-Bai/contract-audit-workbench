// File metadata (manifest mirror):
// Purpose: Reads original material through the existing Document authorization endpoint with bounded responses and no credential-bearing redirects.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package documentclient

import (
	"context"
	"contractaudit/internal/fault"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ClauseOriginalReader uses the existing Document user-authorized original
// route; redirects are refused so session credentials cannot leave the origin.
type ClauseOriginalReader struct {
	endpoint   *url.URL
	credential string
	client     *http.Client
}

func NewClauseOriginalReader(endpoint, credential string, timeout time.Duration) (*ClauseOriginalReader, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Host == "" || !isHTTPOrHTTPS(u.Scheme) || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(credential) < 32 || strings.TrimSpace(credential) != credential || timeout <= 0 {
		return nil, errors.New("invalid document original endpoint")
	}
	return &ClauseOriginalReader{endpoint: u, credential: credential, client: &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *ClauseOriginalReader) ReadClauseOriginal(ctx context.Context, caseID, documentID, session string) ([]byte, error) {
	u := *c.endpoint
	escaped := strings.TrimRight(u.EscapedPath(), "/") + "/api/cases/" + url.PathEscape(caseID) + "/documents/" + url.PathEscape(documentID) + "/original"
	path, err := url.PathUnescape(escaped)
	if err != nil {
		return nil, err
	}
	u.Path, u.RawPath = path, escaped
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: session})
	req.Header.Set("X-Contract-Service-Key", c.credential)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return nil, fault.Unauthenticated("会话已失效，请重新登录")
		case http.StatusForbidden, http.StatusNotFound:
			return nil, fault.NotFound("没有找到对应记录")
		case http.StatusConflict:
			return nil, fault.Conflict("修改稿原件不可核验")
		default:
			return nil, fault.New(503, "document_unavailable", "材料服务暂不可用")
		}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (32<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 32<<20 {
		return nil, errors.New("document original exceeds size limit")
	}
	return body, nil
}
