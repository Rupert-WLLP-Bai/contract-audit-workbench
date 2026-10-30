package legalnorm

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyBaselinePinsBothOfficialResponsesAndObservationTimes(t *testing.T) {
	assetDir := os.Getenv("LEGALNORM_ASSET_DIR")
	if assetDir == "" {
		t.Skip("set LEGALNORM_ASSET_DIR to the external official legal-sources directory")
	}
	inputs := baselineInputs(t, assetDir)
	if len(inputs) != 5 {
		t.Fatalf("baseline inputs=%d, want 5", len(inputs))
	}
	for _, input := range inputs {
		t.Run(input.SourceID, func(t *testing.T) {
			verified, err := VerifyBaseline(input)
			if err != nil {
				t.Fatalf("verify captured baseline: %v", err)
			}
			if verified.Provenance != "verified_official_capture" || verified.SourceResponseStatus != 200 || verified.StatusResponseStatus != 200 ||
				verified.Capture.RawSHA256 != input.ExpectedRawSHA256 || verified.Capture.StatusEvidenceSHA256 != input.ExpectedStatusSHA256 {
				t.Fatalf("baseline identity was not server-pinned: %+v", verified)
			}
			if verified.Capture.SourceSnapshotID != input.SourceSnapshotID || len(verified.Capture.Articles) != 5 || verified.StatusRegistryURL == "" {
				t.Fatalf("baseline capture output incomplete: %+v", verified)
			}
		})
	}
}

func TestSMEPaymentBaselineSeparatesBodyPublisherFromStatusRegistry(t *testing.T) {
	assetDir := os.Getenv("LEGALNORM_ASSET_DIR")
	if assetDir == "" {
		t.Skip("set LEGALNORM_ASSET_DIR to the external official legal-sources directory")
	}
	var input *CaptureInput
	for _, candidate := range baselineInputs(t, assetDir) {
		if candidate.SourceSnapshotID == "S4_sme_payment_regulation_2025" {
			copy := candidate
			input = &copy
			break
		}
	}
	if input == nil {
		t.Fatal("official S4 captured baseline is missing")
	}
	if !bytes.Contains(input.RawBytes, []byte("2025年第10号国务院公报_中国政府网")) {
		t.Fatal("captured body bytes do not contain the independently checked government gazette page title")
	}
	verified, err := VerifyBaseline(*input)
	if err != nil {
		t.Fatalf("verify real captured S4 source and status responses: %v", err)
	}
	if verified.Publisher != "中国政府网（国务院公报）" || verified.Issuer != "国务院" ||
		verified.Capture.OfficialURL != "https://www.gov.cn/gongbao/2025/issue_11966/202504/content_7017465.html" {
		t.Fatalf("verified body attribution does not match the captured publisher: %+v", verified)
	}
	if verified.StatusRegistryURL != "https://xzfg.moj.gov.cn/SearchTitleFront?QueryAll=%E4%BF%9D%E9%9A%9C%E4%B8%AD%E5%B0%8F%E4%BC%81%E4%B8%9A%E6%AC%BE%E9%A1%B9%E6%94%AF%E4%BB%98%E6%9D%A1%E4%BE%8B" {
		t.Fatalf("verified status observation source is not the MOJ registry: %s", verified.StatusRegistryURL)
	}
}

func TestVerifyBaselineRejectsReassertedOrAlteredCaptureEvidence(t *testing.T) {
	assetDir := os.Getenv("LEGALNORM_ASSET_DIR")
	if assetDir == "" {
		t.Skip("set LEGALNORM_ASSET_DIR to the external official legal-sources directory")
	}
	input := baselineInputs(t, assetDir)[0]
	cases := []struct {
		name   string
		mutate func(*CaptureInput)
	}{
		{"source bytes", func(in *CaptureInput) { in.RawBytes = append(in.RawBytes, ' ') }},
		{"status bytes", func(in *CaptureInput) { in.StatusEvidenceRaw = append(in.StatusEvidenceRaw, ' ') }},
		{"source capture time", func(in *CaptureInput) { in.SourceCapturedAt = "2026-10-31T00:01:34+08:00" }},
		{"status observation time", func(in *CaptureInput) { in.StatusObservedAt = "2026-10-31T00:01:37+08:00" }},
		{"status evidence identity", func(in *CaptureInput) { in.StatusEvidenceID = "forged-status-id" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			altered := input
			altered.RawBytes = append([]byte(nil), input.RawBytes...)
			altered.StatusEvidenceRaw = append([]byte(nil), input.StatusEvidenceRaw...)
			tc.mutate(&altered)
			if _, err := VerifyBaseline(altered); err == nil {
				t.Fatal("altered or reasserted evidence was accepted as the captured official baseline")
			}
		})
	}
}

func baselineInputs(t *testing.T, assetDir string) []CaptureInput {
	t.Helper()
	var manifest struct {
		Sources []struct {
			SourceSnapshotID string `json:"source_snapshot_id"`
			SourceID         string `json:"source_id"`
			Title            string `json:"title"`
			VersionID        string `json:"version_id"`
			PromulgatedOn    string `json:"promulgated_on"`
			EffectiveFrom    string `json:"effective_from"`
			OfficialURL      string `json:"official_url"`
			SnapshotFile     string `json:"snapshot_file"`
			RawSHA256        string `json:"sha256_raw_bytes"`
			StatusEvidenceID string `json:"currency_status_evidence_id"`
		} `json:"sources"`
		Articles []articleAsset `json:"articles"`
	}
	readJSON(t, filepath.Join(assetDir, "articles.json"), &manifest)
	var canonical canonicalManifest
	readJSON(t, canonicalManifestPath(t, assetDir), &canonical)
	var sourceManifest struct {
		Sources []capturedSource `json:"sources"`
	}
	readJSON(t, filepath.Join(assetDir, "capture-manifest.json"), &sourceManifest)
	statusByID := map[string]capturedSource{}
	for _, name := range []string{"status-capture-manifest.json", "moj-status-capture-manifest.json"} {
		var statusManifest struct {
			Sources []capturedSource `json:"sources"`
		}
		readJSON(t, filepath.Join(assetDir, name), &statusManifest)
		for _, status := range statusManifest.Sources {
			statusByID[status.ID] = status
		}
	}
	inputs := make([]CaptureInput, 0, len(manifest.Sources))
	for _, source := range manifest.Sources {
		capture := findCapture(t, sourceManifest.Sources, source.SourceSnapshotID)
		statusCapture := statusByID[source.StatusEvidenceID]
		if statusCapture.ID == "" {
			t.Fatalf("missing status capture %q", source.StatusEvidenceID)
		}
		var assertions []ArticleRef
		for _, article := range canonical.Articles {
			if article.SourceSnapshotID != source.SourceSnapshotID {
				continue
			}
			assertions = append(assertions, ArticleRef{
				SourceID: source.SourceID, SourceSnapshotID: source.SourceSnapshotID, VersionID: source.VersionID,
				ArticleNumber: article.ArticleNumber, ArticleLabel: article.ArticleLabel, FullText: article.FullText,
				TextSHA256: article.CanonicalTextSHA256, CitationSpan: TextSpan{Start: 0, End: len([]rune(article.ArticleLabel)), Quote: article.ArticleLabel},
				PromulgatedOn: article.PromulgatedOn, EffectiveFrom: article.EffectiveFrom, EffectiveUntil: article.EffectiveUntil,
				CurrencyStatus: StatusEffective, StatusEvidenceID: article.StatusEvidenceID,
			})
		}
		raw := readAsset(t, assetDir, source.SnapshotFile)
		statusRaw := readAsset(t, assetDir, statusCapture.SnapshotPath)
		inputs = append(inputs, CaptureInput{
			SourceID: source.SourceID, SourceSnapshotID: source.SourceSnapshotID, VersionID: source.VersionID,
			Title: source.Title, OfficialURL: source.OfficialURL, ContentType: capture.ContentType,
			RawBytes: raw, ExpectedRawSHA256: source.RawSHA256, SourceCapturedAt: capture.CapturedAt,
			PromulgatedOn: source.PromulgatedOn, EffectiveFrom: source.EffectiveFrom, CurrencyStatus: StatusEffective,
			StatusEvidenceID: source.StatusEvidenceID, StatusObservedAt: statusCapture.CapturedAt,
			StatusEvidenceRaw: statusRaw, ExpectedStatusSHA256: statusCapture.SHA256, Articles: assertions,
		})
	}
	return inputs
}
