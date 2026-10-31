package legalnorm

import "fmt"

// VerifiedBaseline combines parser verification with the fixed provenance of
// the first official captures. VerifyCapture alone proves only that the
// supplied bytes and client assertions are internally consistent.
type VerifiedBaseline struct {
	Capture              VerifiedCapture `json:"capture"`
	SourceResponseStatus int             `json:"source_response_status"`
	StatusResponseStatus int             `json:"status_response_status"`
	StatusRegistryURL    string          `json:"status_registry_url"`
	StatusRequestMethod  string          `json:"status_request_method"`
	StatusRequestBody    []byte          `json:"status_request_body,omitempty"`
	StatusContentType    string          `json:"status_content_type"`
	Publisher            string          `json:"publisher"`
	Issuer               string          `json:"issuer"`
	DocumentType         string          `json:"document_type"`
	Provenance           string          `json:"provenance"`
}

// VerifyBaseline accepts only the source and point-in-time status response
// bytes captured in the reviewed initial official-source manifests. Capture
// and observation times are pinned with those bytes; callers cannot reuse old
// status evidence to assert a later observation.
func VerifyBaseline(input CaptureInput) (VerifiedBaseline, error) {
	adapter, err := adapterForSource(input.SourceID, input.VersionID)
	if err != nil {
		return VerifiedBaseline{}, err
	}
	baseline := adapter.baseline
	if baseline == nil {
		return VerifiedBaseline{}, fmt.Errorf("no reviewed official baseline for %q/%q", input.SourceID, input.VersionID)
	}
	if input.SourceCapturedAt != baseline.SourceCapturedAt || input.ExpectedRawSHA256 != baseline.RawSHA256 ||
		input.StatusEvidenceID != baseline.StatusEvidenceID || input.StatusObservedAt != baseline.StatusObservedAt ||
		input.ExpectedStatusSHA256 != baseline.StatusSHA256 || input.OfficialURL != baseline.OfficialURL ||
		input.ContentType != baseline.SourceContentType {
		return VerifiedBaseline{}, fmt.Errorf("source or status capture metadata differs from the reviewed official baseline")
	}
	verified, err := VerifyCapture(input)
	if err != nil {
		return VerifiedBaseline{}, err
	}
	if verified.RawSHA256 != baseline.RawSHA256 || verified.StatusEvidenceSHA256 != baseline.StatusSHA256 {
		return VerifiedBaseline{}, fmt.Errorf("source or status response bytes differ from the reviewed official baseline")
	}
	return VerifiedBaseline{
		Capture: verified, SourceResponseStatus: baseline.SourceResponseStatus,
		StatusResponseStatus: baseline.StatusResponseStatus, StatusRegistryURL: baseline.StatusRegistryURL,
		StatusRequestMethod: baseline.StatusRequestMethod, StatusRequestBody: append([]byte(nil), baseline.StatusRequestBody...),
		StatusContentType: baseline.StatusContentType, Publisher: baseline.Publisher, Issuer: baseline.Issuer,
		DocumentType: baseline.DocumentType, Provenance: "verified_official_capture",
	}, nil
}
