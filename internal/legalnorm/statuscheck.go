package legalnorm

import (
	"encoding/json"
	"fmt"
	neturl "net/url"
	"sort"
	"strings"
)

// SourceEdition identifies one pinned normative source edition registered in
// officialAdapters. The registry is code-pinned on purpose: an official
// response that differs from the pinned bytes never becomes authoritative by
// itself.
type SourceEdition struct {
	SourceID  string `json:"source_id"`
	VersionID string `json:"version_id"`
}

// PinnedStatusEvidence identifies the immutable official registry response
// pinned for one source edition.
type PinnedStatusEvidence struct {
	SourceID         string `json:"source_id"`
	VersionID        string `json:"version_id"`
	StatusEvidenceID string `json:"status_evidence_id"`
	StatusSHA256     string `json:"pinned_status_sha256"`
	ObservedAt       string `json:"pinned_observed_at"`
}

// StatusCheck reports whether a candidate official registry response still
// matches the pinned baseline for one source edition. A check is read-only:
// it never rewrites baselines, captures, article versions or frozen references.
type StatusCheck struct {
	SourceID         string `json:"source_id"`
	VersionID        string `json:"version_id"`
	StatusEvidenceID string `json:"status_evidence_id"`
	PinnedSHA256     string `json:"pinned_status_sha256"`
	ObservedSHA256   string `json:"observed_status_sha256"`
	Unchanged        bool   `json:"unchanged"`
	Verified         bool   `json:"verified"`
	ObservedStatus   string `json:"observed_status,omitempty"`
}

// SourceEditions lists every pinned source edition in a stable order.
func SourceEditions() []SourceEdition {
	editions := make([]SourceEdition, 0, len(officialAdapters))
	for _, adapter := range officialAdapters {
		editions = append(editions, SourceEdition{SourceID: adapter.sourceID, VersionID: adapter.versionID})
	}
	sort.Slice(editions, func(i, j int) bool {
		if editions[i].SourceID != editions[j].SourceID {
			return editions[i].SourceID < editions[j].SourceID
		}
		return editions[i].VersionID < editions[j].VersionID
	})
	return editions
}

// PinnedStatus returns the pinned status observation for one source edition.
func PinnedStatus(edition SourceEdition) (PinnedStatusEvidence, error) {
	adapter, err := adapterForSource(edition.SourceID, edition.VersionID)
	if err != nil {
		return PinnedStatusEvidence{}, err
	}
	if adapter.baseline == nil || adapter.baseline.StatusSHA256 == "" || adapter.baseline.StatusEvidenceID == "" {
		return PinnedStatusEvidence{}, fmt.Errorf("source edition %s/%s has no pinned status baseline", edition.SourceID, edition.VersionID)
	}
	return PinnedStatusEvidence{
		SourceID: edition.SourceID, VersionID: edition.VersionID,
		StatusEvidenceID: adapter.baseline.StatusEvidenceID,
		StatusSHA256:     adapter.baseline.StatusSHA256,
		ObservedAt:       adapter.baseline.StatusObservedAt,
	}, nil
}

// PinnedStatusRequest is the exact official registry request registered for one
// source edition. A live check may only send this request to this endpoint.
type PinnedStatusRequest struct {
	URL         string `json:"url"`
	Method      string `json:"method"`
	Body        []byte `json:"-"`
	ContentType string `json:"content_type,omitempty"`
	BodySHA256  string `json:"body_sha256,omitempty"`
}

// PinnedStatusRequestFor resolves the registered official status request. The
// URL is code-pinned: callers cannot substitute another endpoint.
func PinnedStatusRequestFor(edition SourceEdition) (PinnedStatusRequest, error) {
	adapter, err := adapterForSource(edition.SourceID, edition.VersionID)
	if err != nil {
		return PinnedStatusRequest{}, err
	}
	if adapter.baseline == nil || adapter.baseline.StatusRegistryURL == "" {
		return PinnedStatusRequest{}, fmt.Errorf("source edition %s/%s has no pinned status registry endpoint", edition.SourceID, edition.VersionID)
	}
	parsed, err := neturl.Parse(adapter.baseline.StatusRegistryURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return PinnedStatusRequest{}, fmt.Errorf("pinned status registry endpoint for %s/%s is not a plain HTTPS URL", edition.SourceID, edition.VersionID)
	}
	method := strings.ToUpper(strings.TrimSpace(adapter.baseline.StatusRequestMethod))
	if method != "GET" && method != "POST" {
		return PinnedStatusRequest{}, fmt.Errorf("pinned status method %q for %s/%s is unsupported", method, edition.SourceID, edition.VersionID)
	}
	request := PinnedStatusRequest{
		URL: parsed.String(), Method: method, Body: append([]byte(nil), adapter.baseline.StatusRequestBody...),
		ContentType: adapter.baseline.StatusContentType,
	}
	if len(request.Body) > 0 {
		request.BodySHA256 = digest(request.Body)
	}
	return request, nil
}

// CheckStatusResponse compares one candidate official registry response with
// the pinned baseline. Equal bytes report Unchanged. Different bytes are never
// treated as authoritative: a verifiable response reports its documented
// official status for a new capture/import, and an unverifiable response stays
// Verified=false. Invalid evidence is an error. Nothing is stored or modified.
func CheckStatusResponse(edition SourceEdition, candidate []byte) (StatusCheck, error) {
	adapter, err := adapterForSource(edition.SourceID, edition.VersionID)
	if err != nil {
		return StatusCheck{}, err
	}
	pinned, err := PinnedStatus(edition)
	if err != nil {
		return StatusCheck{}, err
	}
	if len(candidate) == 0 {
		return StatusCheck{}, fmt.Errorf("candidate status evidence for %s/%s is empty", edition.SourceID, edition.VersionID)
	}
	check := StatusCheck{
		SourceID: edition.SourceID, VersionID: edition.VersionID,
		StatusEvidenceID: pinned.StatusEvidenceID, PinnedSHA256: pinned.StatusSHA256,
		ObservedSHA256: digest(candidate),
	}
	check.Unchanged = check.ObservedSHA256 == pinned.StatusSHA256
	if err := validateStatusCandidateShape(adapter, candidate); err != nil {
		return check, err
	}
	status, err := verifyStatusEvidence(CaptureInput{StatusEvidenceRaw: candidate}, adapter)
	if err != nil {
		// Well-formed bytes that no longer match the pinned identity or carry
		// no documented status still need a new capture; they are reported as
		// unverified so an operator reviews them instead of trusting a mapping.
		if check.Unchanged {
			return check, fmt.Errorf("pinned status evidence for %s/%s no longer verifies: %w", edition.SourceID, edition.VersionID, err)
		}
		return check, nil
	}
	check.Verified = true
	check.ObservedStatus = status
	return check, nil
}

// validateStatusCandidateShape rejects evidence that cannot be parsed at all,
// keeping "malformed input" distinct from "changed but unverifiable".
func validateStatusCandidateShape(adapter sourceAdapter, candidate []byte) error {
	switch adapter.statusFormat {
	case statusNPCJSON:
		if !json.Valid(candidate) {
			return fmt.Errorf("candidate status evidence is not valid JSON")
		}
	case statusMOJHTML:
		if _, err := htmlTextBlocks(candidate, "text/html; charset=utf-8"); err != nil {
			return fmt.Errorf("candidate status evidence is not valid HTML: %w", err)
		}
	}
	return nil
}
