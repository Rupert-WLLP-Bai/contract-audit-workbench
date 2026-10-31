// Command legalimport submits the three archived supplemental sources through
// the existing authenticated legal-source import endpoint. It never publishes
// the resulting draft articles. See the external legal-source delivery notes.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/legalnorm"
	"contractaudit/internal/store"
)

var sourceIDs = []string{
	"CN-CONTRACT-LAW-HISTORY",
	"CN-SPC-TIME-EFFECT",
	"CN-SPC-CONTRACT-GENERAL-INTERPRETATION",
}

func main() {
	assetDir := flag.String("asset-dir", "", "external legal-sources directory")
	baseURL := flag.String("base-url", "", "application base URL")
	sessionFile := flag.String("admin-session-file", "", "private file containing the admin_session cookie value")
	sourceID := flag.String("source", "all", "one supplemental source ID, or all")
	flag.Parse()
	if err := run(*assetDir, *baseURL, *sessionFile, *sourceID); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(assetDir, baseURL, sessionFile, sourceID string) error {
	if assetDir == "" || baseURL == "" || sessionFile == "" {
		return fmt.Errorf("asset-dir, base-url and admin-session-file are required")
	}
	u, err := url.Parse(baseURL)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("base-url must be an HTTP(S) application URL without credentials, query or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/admin/legal-sources/imports"
	u.RawPath = ""
	selected := sourceIDs
	if sourceID != "all" {
		selected = nil
		for _, candidate := range sourceIDs {
			if candidate == sourceID {
				selected = []string{candidate}
				break
			}
		}
		if len(selected) == 0 {
			return fmt.Errorf("unknown supplemental source %q", sourceID)
		}
	}
	sessionBytes, err := os.ReadFile(sessionFile)
	if err != nil {
		return fmt.Errorf("read admin session file: %w", err)
	}
	session := strings.TrimSpace(string(sessionBytes))
	if session == "" || strings.ContainsAny(session, "\r\n; ") {
		return fmt.Errorf("admin session file must contain only the cookie value")
	}
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	failed := false
	for _, id := range selected {
		if err := importSource(client, u.String(), assetDir, session, id); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", id, err)
			failed = true
		}
	}
	if failed {
		return fmt.Errorf("one or more supplemental sources were not imported; the other sources were attempted independently")
	}
	return nil
}

func importSource(client *http.Client, endpoint, assetDir, session, sourceID string) error {
	capture, err := legalnorm.LoadSupplementalCapture(assetDir, sourceID)
	if err != nil {
		return err
	}
	key := "supplemental-v1-" + strings.ToLower(sourceID)
	input := store.LegalSourceImportInput{Capture: capture, IdempotencyKey: key}
	for _, article := range capture.Articles {
		input.Articles = append(input.Articles, store.LegalArticleVersionInput{
			ArticleNumber:  article.ArticleNumber,
			KnowledgeID:    "legal-" + strings.ToLower(sourceID) + "-" + strconv.Itoa(article.ArticleNumber),
			DisplayVersion: capture.VersionID,
		})
	}
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode import request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create import request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", req.URL.Scheme+"://"+req.URL.Host)
	req.Header.Set("Idempotency-Key", key)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: session})
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("submit import: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		return fmt.Errorf("import returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var result store.LegalSourceImportResult
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return fmt.Errorf("import returned an unreadable receipt; retry with the same idempotency key: %w", err)
	}
	if result.SourceID != sourceID || result.NormVersion != capture.VersionID || result.RawSHA256 != capture.ExpectedRawSHA256 ||
		result.StatusSHA256 != capture.ExpectedStatusSHA256 || result.CaptureSnapshotID == "" || len(result.Articles) != len(capture.Articles) {
		return fmt.Errorf("import receipt identity does not match the submitted source; retry with the same idempotency key")
	}
	fmt.Printf("%s: capture=%s version=%s articles=%d raw_sha256=%s status_sha256=%s initial_status=draft\n",
		result.SourceID, result.CaptureSnapshotID, result.NormVersion, len(result.Articles), result.RawSHA256, result.StatusSHA256)
	return nil
}
