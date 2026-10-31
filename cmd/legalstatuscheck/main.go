// Command legalstatuscheck compares official registry responses with the
// status evidence pinned in the normative source registry.
//
// Two modes exist:
//
//	-offline: -asset-dir <dir> reads already captured responses
//	-online:  -capture-dir <new dir> fetches the registered official endpoint
//	          for each source edition, stores the raw response, observation
//	          time and digest outside the repository, then checks it
//
// The command is a read-only validity check. It never writes a database row,
// capture identity, article version or frozen reference. Missing candidates,
// unreadable evidence and fetch failures are failures (exit 1); a changed
// official response only reports that a new capture/import is required
// (exit 2).
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"contractaudit/internal/legalnorm"
)

const (
	decisionUnchanged  = "UNCHANGED"
	decisionChanged    = "CHANGED_NEW_CAPTURE_REQUIRED"
	decisionUnverified = "CHANGED_UNVERIFIED"
	decisionMissing    = "CANDIDATE_MISSING"
	decisionInvalid    = "CANDIDATE_INVALID"
	decisionCaptured   = "CAPTURED"
	decisionFetchFail  = "CAPTURE_FAILED"

	maxResponseBytes = 8 << 20
)

type config struct {
	assetDir   string
	captureDir string
	sourceID   string
	timeout    time.Duration
}

type result struct {
	SourceID         string `json:"source_id"`
	VersionID        string `json:"version_id"`
	StatusEvidenceID string `json:"status_evidence_id"`
	Decision         string `json:"decision"`
	PinnedSHA256     string `json:"pinned_status_sha256"`
	ObservedSHA256   string `json:"observed_status_sha256,omitempty"`
	ObservedStatus   string `json:"observed_status,omitempty"`
}

type captureRecord struct {
	SourceID         string `json:"source_id"`
	VersionID        string `json:"version_id"`
	StatusEvidenceID string `json:"status_evidence_id"`
	RegistryURL      string `json:"registry_url"`
	RequestMethod    string `json:"request_method"`
	RequestSHA256    string `json:"request_body_sha256,omitempty"`
	ResponseStatus   int    `json:"response_status,omitempty"`
	MediaType        string `json:"media_type,omitempty"`
	ObservedAt       string `json:"observed_at"`
	Bytes            int    `json:"bytes,omitempty"`
	SHA256           string `json:"sha256,omitempty"`
	Snapshot         string `json:"snapshot,omitempty"`
	Error            string `json:"error,omitempty"`
}

func main() {
	assetDir := flag.String("asset-dir", "", "captured official responses (raw/<status_evidence_id>.json|.html)")
	captureDir := flag.String("capture-dir", "", "new directory receiving live official responses and capture-observation.json")
	sourceID := flag.String("source", "all", "one source ID, or all")
	timeout := flag.Duration("timeout", 30*time.Second, "per-request timeout for live capture")
	flag.Parse()
	code, err := run(config{assetDir: *assetDir, captureDir: *captureDir, sourceID: *sourceID, timeout: *timeout})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(code)
}

// run returns the process exit code: 0 unchanged, 2 changed (new capture
// required), 1 any failure (missing/invalid evidence, fetch error, I/O).
func run(cfg config) (int, error) {
	if cfg.assetDir == "" && cfg.captureDir == "" {
		return 1, fmt.Errorf("either -asset-dir or -capture-dir is required")
	}
	if cfg.assetDir != "" && cfg.captureDir != "" {
		return 1, fmt.Errorf("-asset-dir and -capture-dir are mutually exclusive")
	}
	editions, err := selectEditions(cfg.sourceID)
	if err != nil {
		return 1, err
	}
	scanDir := cfg.assetDir
	failures := 0
	unavailable := map[legalnorm.SourceEdition]bool{}
	if cfg.captureDir != "" {
		captured, failed, err := captureOfficialResponses(cfg.captureDir, editions, cfg.timeout)
		if err != nil {
			return 1, err
		}
		failures += len(failed)
		for _, edition := range failed {
			unavailable[edition] = true
		}
		fmt.Fprintf(os.Stderr, "capture: %d responses stored, %d failed under %s\n", captured, len(failed), cfg.captureDir)
		scanDir = cfg.captureDir
	}
	var changed, unverified int
	for _, edition := range editions {
		if unavailable[edition] {
			continue
		}
		pinned, err := legalnorm.PinnedStatus(edition)
		if err != nil {
			return 1, err
		}
		item := result{
			SourceID: edition.SourceID, VersionID: edition.VersionID,
			StatusEvidenceID: pinned.StatusEvidenceID, PinnedSHA256: pinned.StatusSHA256,
		}
		candidate, path, err := readCandidate(scanDir, pinned.StatusEvidenceID)
		if err != nil {
			return 1, err
		}
		if path == "" {
			item.Decision = decisionMissing
			failures++
			emit(item)
			continue
		}
		check, err := legalnorm.CheckStatusResponse(edition, candidate)
		if err != nil {
			item.Decision = decisionInvalid
			item.ObservedSHA256 = check.ObservedSHA256
			failures++
			emit(item)
			fmt.Fprintf(os.Stderr, "%s/%s: %v\n", edition.SourceID, edition.VersionID, err)
			continue
		}
		item.ObservedSHA256 = check.ObservedSHA256
		item.ObservedStatus = check.ObservedStatus
		switch {
		case check.Unchanged:
			item.Decision = decisionUnchanged
		case check.Verified:
			item.Decision = decisionChanged
			changed++
		default:
			item.Decision = decisionUnverified
			unverified++
		}
		emit(item)
	}
	switch {
	case failures > 0:
		fmt.Fprintf(os.Stderr, "result: check failed — %d missing/invalid/fetch failures, %d changed, %d changed but unverifiable; no stored capture, article version or frozen reference was modified\n", failures, changed, unverified)
		return 1, nil
	case changed > 0 || unverified > 0:
		fmt.Fprintf(os.Stderr, "result: %d changed (new capture required), %d changed but unverifiable; no stored capture, article version or frozen reference was modified\n", changed, unverified)
		return 2, nil
	default:
		fmt.Fprintln(os.Stderr, "result: all pinned official status observations unchanged; nothing was written to the application database")
		return 0, nil
	}
}

func selectEditions(sourceID string) ([]legalnorm.SourceEdition, error) {
	all := legalnorm.SourceEditions()
	if sourceID == "" || strings.EqualFold(sourceID, "all") {
		return all, nil
	}
	for _, edition := range all {
		if edition.SourceID == sourceID {
			return []legalnorm.SourceEdition{edition}, nil
		}
	}
	return nil, fmt.Errorf("unknown legal source %q", sourceID)
}

// readCandidate returns the captured response for one status evidence ID.
// The frozen layout stores NPC JSON and MOJ HTML responses under raw/.
func readCandidate(assetDir, statusEvidenceID string) ([]byte, string, error) {
	for _, name := range []string{statusEvidenceID + ".json", statusEvidenceID + ".html"} {
		path := filepath.Join(assetDir, "raw", name)
		data, err := os.ReadFile(path)
		if err == nil {
			return data, path, nil
		}
		if !os.IsNotExist(err) {
			return nil, "", fmt.Errorf("read %s: %w", path, err)
		}
	}
	return nil, "", nil
}

// captureOfficialResponses fetches only the pinned official registry endpoint
// of each edition and stores the raw bytes plus observation metadata. A failed
// fetch writes no response file; the attempt is recorded with its error and
// counted as a failure. Cross-host redirects are refused.
func captureOfficialResponses(dir string, editions []legalnorm.SourceEdition, timeout time.Duration) (int, []legalnorm.SourceEdition, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	rawDir := filepath.Join(dir, "raw")
	if err := os.Mkdir(dir, 0o755); err != nil {
		return 0, nil, fmt.Errorf("capture directory must be a new directory: %w", err)
	}
	if err := os.Mkdir(rawDir, 0o755); err != nil {
		return 0, nil, fmt.Errorf("create capture raw directory: %w", err)
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: officialRedirectPolicy}
	captured := 0
	var failed []legalnorm.SourceEdition
	records := make([]captureRecord, 0, len(editions))
	for _, edition := range editions {
		request, err := legalnorm.PinnedStatusRequestFor(edition)
		if err != nil {
			return 0, nil, err
		}
		pinned, err := legalnorm.PinnedStatus(edition)
		if err != nil {
			return 0, nil, err
		}
		record := captureRecord{
			SourceID: edition.SourceID, VersionID: edition.VersionID,
			StatusEvidenceID: pinned.StatusEvidenceID,
			RequestSHA256:    request.BodySHA256,
		}
		record.RegistryURL = request.URL
		record.RequestMethod = request.Method
		body, mediaType, status, fetchErr := fetch(client, request)
		record.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if fetchErr != nil {
			record.Error = fetchErr.Error()
			failed = append(failed, edition)
			records = append(records, record)
			emitCapture(record)
			continue
		}
		extension := ".json"
		if strings.Contains(strings.ToLower(mediaType), "html") {
			extension = ".html"
		}
		record.ResponseStatus = status
		record.MediaType = mediaType
		record.Bytes = len(body)
		record.SHA256 = digestHex(body)
		record.Snapshot = filepath.Join("raw", pinned.StatusEvidenceID+extension)
		if err := writeExclusive(filepath.Join(dir, record.Snapshot), body); err != nil {
			return 0, nil, fmt.Errorf("write %s: %w", record.Snapshot, err)
		}
		captured++
		records = append(records, record)
		emitCapture(record)
	}
	manifest := map[string]any{
		"captured_at": time.Now().UTC().Format(time.RFC3339Nano),
		"note":        "read-only official status observation; it never updates captures, article versions or frozen references",
		"entries":     records,
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return 0, nil, err
	}
	if err := writeExclusive(filepath.Join(dir, "capture-observation.json"), append(raw, '\n')); err != nil {
		return 0, nil, fmt.Errorf("write capture-observation.json: %w", err)
	}
	return captured, failed, nil
}

// officialRedirectPolicy accepts only same-host HTTPS redirects. A downgrade
// to plain HTTP or a redirect to another host is refused.
func officialRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("redirect to non-HTTPS URL %q refused", req.URL.String())
	}
	if req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("redirect to non-registered host %q refused", req.URL.Host)
	}
	if len(via) >= 3 {
		return errors.New("too many redirects")
	}
	return nil
}

// writeExclusive creates a new file and never truncates existing evidence.
func writeExclusive(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func fetch(client *http.Client, request legalnorm.PinnedStatusRequest) ([]byte, string, int, error) {
	var lastErr error
	for attempt := range 2 {
		var (
			req *http.Request
			err error
		)
		if request.Method == http.MethodPost {
			req, err = http.NewRequest(http.MethodPost, request.URL, bytes.NewReader(request.Body))
		} else {
			req, err = http.NewRequest(http.MethodGet, request.URL, nil)
		}
		if err != nil {
			return nil, "", 0, err
		}
		req.Header.Set("Accept", "application/json, text/html;q=0.9")
		req.Header.Set("User-Agent", "contract-audit-legalstatuscheck/1")
		if request.Method == http.MethodPost && request.ContentType != "" {
			req.Header.Set("Content-Type", request.ContentType)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
			mediaType := resp.Header.Get("Content-Type")
			_ = resp.Body.Close()
			if readErr != nil {
				lastErr = readErr
			} else if len(body) > maxResponseBytes {
				lastErr = fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
			} else if resp.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("official endpoint answered HTTP %d", resp.StatusCode)
			} else {
				return body, mediaType, resp.StatusCode, nil
			}
		}
		if attempt == 0 {
			time.Sleep(time.Second)
		}
	}
	return nil, "", 0, fmt.Errorf("fetch %s %s: %w", request.Method, request.URL, lastErr)
}

func emit(item result) {
	raw, err := json.Marshal(item)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal result: %v\n", err)
		return
	}
	fmt.Println(string(raw))
}

func emitCapture(record captureRecord) {
	decision := decisionCaptured
	if record.Error != "" {
		decision = decisionFetchFail
	}
	payload := map[string]any{
		"source_id": record.SourceID, "version_id": record.VersionID,
		"decision": decision, "registry_url": record.RegistryURL,
		"observed_at": record.ObservedAt, "sha256": record.SHA256, "bytes": record.Bytes,
	}
	if record.Error != "" {
		payload["error"] = record.Error
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal capture: %v\n", err)
		return
	}
	fmt.Println(string(raw))
}

func digestHex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
