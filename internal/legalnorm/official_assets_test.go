package legalnorm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

type capturedSource struct {
	ID                     string `json:"id"`
	HTTPStatus             int    `json:"http_status"`
	ContentType            string `json:"content_type"`
	CapturedAt             string `json:"captured_at"`
	SnapshotPath           string `json:"snapshot_path"`
	SHA256                 string `json:"sha256_raw_bytes"`
	StatusCheckedAtCapture string `json:"status_checked_at_capture"`
}

type articleAsset struct {
	SourceID            string `json:"source_id"`
	SourceSnapshotID    string `json:"source_snapshot_id"`
	Title               string `json:"title"`
	VersionID           string `json:"version_id"`
	ArticleNumber       int    `json:"article_no"`
	ArticleLabel        string `json:"article_label"`
	FullText            string `json:"full_text"`
	PromulgatedOn       string `json:"promulgated_on"`
	EffectiveFrom       string `json:"effective_from"`
	EffectiveUntil      string `json:"effective_to"`
	CurrencyCheckedAt   string `json:"currency_checked_on"`
	CurrencyStatus      string `json:"currency_status"`
	StatusEvidenceID    string `json:"currency_status_evidence_id"`
	CanonicalTextSHA256 string `json:"canonical_text_sha256"`
	SourceTextStart     int    `json:"source_text_start"`
	SourceTextEnd       int    `json:"source_text_end"`
}

type canonicalManifest struct {
	SchemaVersion        string         `json:"schema_version"`
	ExtractorID          string         `json:"extractor_id"`
	ExtractorVersion     string         `json:"extractor_version"`
	CanonicalTextVersion string         `json:"canonical_text_version"`
	ArticleCount         int            `json:"article_count"`
	Articles             []articleAsset `json:"articles"`
}

func TestOfficialAssetsExtractAndMatchFirstFiveNorms(t *testing.T) {
	assetDir := os.Getenv("LEGALNORM_ASSET_DIR")
	if assetDir == "" {
		t.Skip("set LEGALNORM_ASSET_DIR to the external official legal-sources directory")
	}
	var articleManifest struct {
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
			CurrencyStatus   string `json:"currency_status"`
		} `json:"sources"`
		Articles []articleAsset `json:"articles"`
	}
	readJSON(t, filepath.Join(assetDir, "articles.json"), &articleManifest)
	canonicalPath := canonicalManifestPath(t, assetDir)
	var canonical canonicalManifest
	readJSON(t, canonicalPath, &canonical)
	if canonical.SchemaVersion != "p4-canonical-articles/v2" || canonical.ExtractorID != "legalnorm" ||
		canonical.ExtractorVersion != "1" || canonical.CanonicalTextVersion != CanonicalTextVersion {
		t.Fatalf("canonical article manifest identity/version is invalid")
	}
	var captures struct {
		Sources []capturedSource `json:"sources"`
	}
	readJSON(t, filepath.Join(assetDir, "capture-manifest.json"), &captures)
	statusCaptures := map[string]capturedSource{}
	for _, manifestName := range []string{"status-capture-manifest.json", "moj-status-capture-manifest.json"} {
		var statusManifest struct {
			Sources []capturedSource `json:"sources"`
		}
		readJSON(t, filepath.Join(assetDir, manifestName), &statusManifest)
		for _, source := range statusManifest.Sources {
			statusCaptures[source.ID] = source
		}
	}

	if len(articleManifest.Sources) != 5 {
		t.Fatalf("asset manifest has %d primary sources, want 5", len(articleManifest.Sources))
	}
	if len(articleManifest.Articles) != 25 || len(canonical.Articles) != 25 || canonical.ArticleCount != 25 {
		t.Fatalf("primary article counts legacy=%d canonical=%d declared=%d, want 25 each", len(articleManifest.Articles), len(canonical.Articles), canonical.ArticleCount)
	}
	articleCount := 0
	for _, source := range articleManifest.Sources {
		t.Run(source.SourceSnapshotID, func(t *testing.T) {
			adapter, err := adapterForSnapshot(source.SourceSnapshotID)
			if err != nil {
				t.Fatal(err)
			}
			captureManifest := findCapture(t, captures.Sources, source.SourceSnapshotID)
			statusManifest := findCapture(t, statusCapturesSlice(statusCaptures), source.StatusEvidenceID)
			if captureManifest.HTTPStatus != 200 || statusManifest.HTTPStatus != 200 {
				t.Fatalf("capture/status HTTP status %d/%d, want 200", captureManifest.HTTPStatus, statusManifest.HTTPStatus)
			}
			raw := readAsset(t, assetDir, source.SnapshotFile)
			statusRaw := readAsset(t, assetDir, statusManifest.SnapshotPath)
			if adapter.baselineRawSHA256 != source.RawSHA256 || digest(raw) != source.RawSHA256 {
				t.Fatalf("initial official response digest does not match pinned acceptance asset")
			}
			assertions := make([]ArticleRef, 0, 5)
			for _, article := range canonical.Articles {
				if article.SourceSnapshotID != source.SourceSnapshotID {
					continue
				}
				assertions = append(assertions, ArticleRef{
					SourceID: article.SourceID, SourceSnapshotID: article.SourceSnapshotID, VersionID: article.VersionID,
					ArticleNumber: article.ArticleNumber, ArticleLabel: article.ArticleLabel,
					FullText: article.FullText, TextSHA256: article.CanonicalTextSHA256,
					CitationSpan:  TextSpan{Start: 0, End: len([]rune(article.ArticleLabel)), Quote: article.ArticleLabel},
					PromulgatedOn: article.PromulgatedOn, EffectiveFrom: article.EffectiveFrom,
					EffectiveUntil: article.EffectiveUntil, CurrencyStatus: StatusEffective,
					StatusEvidenceID: article.StatusEvidenceID,
				})
			}
			statusObservedAt := statusManifest.CapturedAt
			input := CaptureInput{
				SourceID: source.SourceID, SourceSnapshotID: source.SourceSnapshotID, VersionID: source.VersionID,
				Title: source.Title, OfficialURL: source.OfficialURL, ContentType: captureManifest.ContentType,
				RawBytes: raw, ExpectedRawSHA256: source.RawSHA256, SourceCapturedAt: captureManifest.CapturedAt, PromulgatedOn: source.PromulgatedOn,
				EffectiveFrom: source.EffectiveFrom, CurrencyStatus: StatusEffective,
				StatusEvidenceID: source.StatusEvidenceID, StatusObservedAt: statusObservedAt,
				StatusEvidenceRaw: statusRaw, ExpectedStatusSHA256: statusManifest.SHA256, Articles: assertions,
			}
			verified, err := verifyCaptureWithAdapter(input, adapter)
			if err != nil {
				t.Fatalf("official source extraction failed: %v", err)
			}
			if len(verified.Articles) != 5 {
				t.Fatalf("verified %d article bodies, want 5", len(verified.Articles))
			}
			for _, actual := range verified.Articles {
				want := findArticleAsset(t, canonical.Articles, actual.Ref.SourceSnapshotID, actual.Ref.ArticleNumber)
				if actual.Start != want.SourceTextStart || actual.End != want.SourceTextEnd || actual.TextSHA256 != want.CanonicalTextSHA256 {
					t.Errorf("article %s source span/hash = [%d,%d)/%s, want [%d,%d)/%s", actual.Ref.ArticleLabel,
						actual.Start, actual.End, actual.TextSHA256, want.SourceTextStart, want.SourceTextEnd, want.CanonicalTextSHA256)
				}
				t.Logf("article PASS: %s %s/%s sha256=%s source_rune_span=[%d,%d) promulgated=%s effective_from=%s registry=%s evidence=%s",
					actual.Ref.ArticleLabel, actual.Ref.SourceID, actual.Ref.VersionID, actual.TextSHA256, actual.Start, actual.End,
					actual.Ref.PromulgatedOn, actual.Ref.EffectiveFrom, verified.CurrencyStatus, verified.StatusEvidenceID)
			}
			articleCount += len(verified.Articles)
			t.Logf("source response PASS: raw_sha256=%s extracted_text_sha256=%s verified_articles=%d/5 status=%s observed_at=%s extractor=%s/%s",
				verified.RawSHA256, verified.FullTextSHA256, len(verified.Articles), verified.CurrencyStatus,
				verified.StatusObservedAt, verified.ExtractorID, verified.ExtractorVersion)
		})
	}
	if articleCount != 25 {
		t.Fatalf("verified %d primary articles, want 25", articleCount)
	}
}

func TestLegacyCuratedTextDiffIsExactlySixDocumentedCorrections(t *testing.T) {
	assetDir := os.Getenv("LEGALNORM_ASSET_DIR")
	if assetDir == "" {
		t.Skip("set LEGALNORM_ASSET_DIR to the external official legal-sources directory")
	}
	var legacy struct {
		Articles []articleAsset `json:"articles"`
	}
	readJSON(t, filepath.Join(assetDir, "articles.json"), &legacy)
	var canonical canonicalManifest
	readJSON(t, canonicalManifestPath(t, assetDir), &canonical)
	wantChanges := map[string]struct{}{
		"S2_tender_law_2017/48":                     {},
		"S3_government_procurement_law_2014/50":     {},
		"S5_tender_implementing_regulation_2019/9":  {},
		"S5_tender_implementing_regulation_2019/26": {},
		"S5_tender_implementing_regulation_2019/57": {},
		"S5_tender_implementing_regulation_2019/58": {},
	}
	canonicalByRef := make(map[string]articleAsset, len(canonical.Articles))
	for _, article := range canonical.Articles {
		canonicalByRef[fmt.Sprintf("%s/%d", article.SourceSnapshotID, article.ArticleNumber)] = article
	}
	changed := map[string]struct{}{}
	for _, old := range legacy.Articles {
		key := fmt.Sprintf("%s/%d", old.SourceSnapshotID, old.ArticleNumber)
		current, ok := canonicalByRef[key]
		if !ok {
			t.Fatalf("canonical manifest is missing %s", key)
		}
		oldMeta, currentMeta := old, current
		oldMeta.FullText, currentMeta.FullText = "", ""
		oldMeta.CanonicalTextSHA256, oldMeta.SourceTextStart, oldMeta.SourceTextEnd = "", 0, 0
		currentMeta.CanonicalTextSHA256, currentMeta.SourceTextStart, currentMeta.SourceTextEnd = "", 0, 0
		if !reflect.DeepEqual(oldMeta, currentMeta) {
			t.Errorf("non-text article metadata changed for %s", key)
		}
		if NormalizeWhitespace(old.FullText) != current.FullText {
			changed[key] = struct{}{}
		}
	}
	if !reflect.DeepEqual(changed, wantChanges) {
		t.Fatalf("legacy-to-canonical text corrections = %v, want exactly %v", changed, wantChanges)
	}
	t.Logf("verified 19 unchanged curated article bodies and 6 documented source-derived text corrections")
}

func canonicalManifestPath(t *testing.T, assetDir string) string {
	t.Helper()
	if path := os.Getenv("LEGALNORM_CANONICAL_MANIFEST"); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(assetDir), "p4-canonical-articles-v2.json")
}

func findArticleAsset(t *testing.T, articles []articleAsset, snapshotID string, number int) articleAsset {
	t.Helper()
	for _, article := range articles {
		if article.SourceSnapshotID == snapshotID && article.ArticleNumber == number {
			return article
		}
	}
	t.Fatalf("canonical manifest has no article %s/%d", snapshotID, number)
	return articleAsset{}
}

func readJSON(t *testing.T, path string, target any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode %s: %v", filepath.Base(path), err)
	}
}

func readAsset(t *testing.T, assetDir, relative string) []byte {
	t.Helper()
	if filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == "." || relative == ".." || len(relative) >= 3 && relative[:3] == "../" {
		t.Fatalf("unsafe asset path %q", relative)
	}
	raw, err := os.ReadFile(filepath.Join(assetDir, relative))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func findCapture(t *testing.T, sources []capturedSource, id string) capturedSource {
	t.Helper()
	for _, source := range sources {
		if source.ID == id {
			return source
		}
	}
	t.Fatalf("capture manifest has no source %q", id)
	return capturedSource{}
}

func statusCapturesSlice(captures map[string]capturedSource) []capturedSource {
	ids := make([]string, 0, len(captures))
	for id := range captures {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]capturedSource, 0, len(ids))
	for _, id := range ids {
		out = append(out, captures[id])
	}
	return out
}
