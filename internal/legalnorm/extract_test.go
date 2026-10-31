package legalnorm

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func fixtureInput(t *testing.T) (CaptureInput, sourceAdapter) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "legalnorm", "fixture.html"))
	if err != nil {
		t.Fatal(err)
	}
	statusRaw := []byte(`{"status":"effective","observed_at":"2026-10-31T00:00:00+08:00"}`)
	input := CaptureInput{
		SourceID: "CN-LEGALNORM-FIXTURE", SourceSnapshotID: "fixture-v1", VersionID: "2026-01-01",
		Title: "中华人民共和国测试条例", OfficialURL: "https://example.invalid/law", ContentType: "text/html; charset=utf-8",
		RawBytes: raw, ExpectedRawSHA256: hashBytes(raw), SourceCapturedAt: "2026-10-30T23:00:00+08:00", PromulgatedOn: "2025-12-01", EffectiveFrom: "2026-01-01",
		CurrencyStatus: "effective", StatusEvidenceID: "fixture-status-v1", StatusObservedAt: "2026-10-31T00:00:00+08:00",
		StatusEvidenceRaw: statusRaw, ExpectedStatusSHA256: hashBytes(statusRaw),
		Articles: []ArticleRef{
			{SourceID: "CN-LEGALNORM-FIXTURE", SourceSnapshotID: "fixture-v1", VersionID: "2026-01-01", ArticleNumber: 1, ArticleLabel: "第一条", FullText: "第一条 本条例用于测试正文提取。\n本条例仅作为小型解析 fixture。", TextSHA256: hashText("第一条 本条例用于测试正文提取。 本条例仅作为小型解析 fixture。"), PromulgatedOn: "2025-12-01", EffectiveFrom: "2026-01-01", CurrencyStatus: "effective", StatusEvidenceID: "fixture-status-v1", CitationSpan: TextSpan{Start: 4, End: 13, Quote: "本条例用于测试正文"}},
			{SourceID: "CN-LEGALNORM-FIXTURE", SourceSnapshotID: "fixture-v1", VersionID: "2026-01-01", ArticleNumber: 2, ArticleLabel: "第二条", FullText: "第二条 本条例自2026年1月1日起施行。", TextSHA256: hashText("第二条 本条例自2026年1月1日起施行。"), PromulgatedOn: "2025-12-01", EffectiveFrom: "2026-01-01", CurrencyStatus: "effective", StatusEvidenceID: "fixture-status-v1"},
		},
	}
	adapter := sourceAdapter{
		sourceID: "CN-LEGALNORM-FIXTURE", versionID: "2026-01-01",
		title: input.Title, officialURL: input.OfficialURL, promulgatedOn: input.PromulgatedOn,
		effectiveFrom:     input.EffectiveFrom,
		statusEffectiveAt: input.EffectiveFrom,
		statusFormat:      statusFixture, extractorID: "fixture-html", extractorVersion: "1",
		canonicalizationVersion: CanonicalTextVersion, fixture: true,
	}
	return input, adapter
}

func TestVerifyCaptureExtractsCanonicalArticlesAndOffsets(t *testing.T) {
	input, adapter := fixtureInput(t)
	verified, err := verifyCaptureWithAdapter(input, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := verified.ExtractorID, "fixture-html"; got != want {
		t.Fatalf("extractor id = %q, want %q", got, want)
	}
	if got, want := verified.CanonicalTextVersion, CanonicalTextVersion; got != want {
		t.Fatalf("canonical text version = %q, want %q", got, want)
	}
	if len(verified.Articles) != 2 {
		t.Fatalf("got %d articles, want 2", len(verified.Articles))
	}
	if got, want := verified.Articles[1].FullText, "第二条 本条例自2026年1月1日起施行。"; got != want {
		t.Fatalf("final article included a following chapter/footer: got %q, want %q", got, want)
	}
	for _, article := range verified.Articles {
		if article.Start < 0 || article.End <= article.Start || article.End > len([]rune(verified.FullText)) {
			t.Fatalf("invalid source offsets for article %s: [%d,%d)", article.Ref.ArticleLabel, article.Start, article.End)
		}
		if got := string([]rune(verified.FullText)[article.Start:article.End]); got != article.FullText {
			t.Fatalf("source span for %s = %q, want its exact body %q", article.Ref.ArticleLabel, got, article.FullText)
		}
	}
}

func TestVerifyCaptureRejectsTamperedArticleAndSpan(t *testing.T) {
	t.Run("tampered article text", func(t *testing.T) {
		input, adapter := fixtureInput(t)
		input.Articles[0].FullText += "伪造"
		if _, err := verifyCaptureWithAdapter(input, adapter); err == nil {
			t.Fatal("expected tampered article to be rejected")
		}
	})
	t.Run("invalid citation span", func(t *testing.T) {
		input, adapter := fixtureInput(t)
		input.Articles[0].CitationSpan = TextSpan{Start: 0, End: 2, Quote: "错误"}
		if _, err := verifyCaptureWithAdapter(input, adapter); err == nil {
			t.Fatal("expected mismatched span to be rejected")
		}
	})
	t.Run("article digest mismatch", func(t *testing.T) {
		input, adapter := fixtureInput(t)
		input.Articles[0].TextSHA256 = hashText("different text")
		if _, err := verifyCaptureWithAdapter(input, adapter); err == nil {
			t.Fatal("expected mismatched article digest to be rejected")
		}
	})
}

func TestVerifyCaptureRejectsBadEncodingAndMissingArticle(t *testing.T) {
	t.Run("invalid utf8", func(t *testing.T) {
		input, adapter := fixtureInput(t)
		input.RawBytes = append(append([]byte(nil), input.RawBytes...), 0xff)
		input.ExpectedRawSHA256 = hashBytes(input.RawBytes)
		if _, err := verifyCaptureWithAdapter(input, adapter); err == nil {
			t.Fatal("expected invalid encoding to be rejected")
		}
	})
	t.Run("missing source article", func(t *testing.T) {
		input, adapter := fixtureInput(t)
		input.Articles[1].ArticleLabel = "第三条"
		input.Articles[1].ArticleNumber = 3
		if _, err := verifyCaptureWithAdapter(input, adapter); err == nil {
			t.Fatal("expected absent article to be rejected")
		}
	})
	t.Run("no article body in source", func(t *testing.T) {
		input, adapter := fixtureInput(t)
		input.RawBytes = []byte("<!doctype html><html><body><p>仅有标题，没有正文。</p></body></html>")
		input.ExpectedRawSHA256 = hashBytes(input.RawBytes)
		if _, err := verifyCaptureWithAdapter(input, adapter); err == nil {
			t.Fatal("expected source without a formal article body to be rejected")
		}
	})
}

func TestVerifyCaptureStopsFinalArticleBeforeSiteFooter(t *testing.T) {
	input, adapter := fixtureInput(t)
	input.RawBytes = []byte("<!doctype html><html><body><main><p>第一条　本条例正文。</p></main><footer><p>版权所有 测试机构</p><p>备案号 123</p></footer></body></html>")
	input.ExpectedRawSHA256 = hashBytes(input.RawBytes)
	input.Articles = []ArticleRef{input.Articles[0]}
	input.Articles[0].FullText = "第一条 本条例正文。"
	input.Articles[0].TextSHA256 = hashText(input.Articles[0].FullText)
	input.Articles[0].CitationSpan = TextSpan{Start: 4, End: 10, Quote: "本条例正文。"}
	verified, err := verifyCaptureWithAdapter(input, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := verified.Articles[0].FullText, "第一条 本条例正文。"; got != want {
		t.Fatalf("final article included page footer: got %q, want %q", got, want)
	}
}

func TestVerifyCaptureValidatesVersionDatesAndStatusProof(t *testing.T) {
	for name, mutate := range map[string]func(*CaptureInput){
		"bad-effective-date":            func(input *CaptureInput) { input.EffectiveFrom = "not-a-date" },
		"bad-source-capture-time":       func(input *CaptureInput) { input.SourceCapturedAt = "not-a-time" },
		"effective-before-promulgation": func(input *CaptureInput) { input.EffectiveFrom = "2025-01-01" },
		"unknown-status":                func(input *CaptureInput) { input.CurrencyStatus = "maybe" },
		"status-evidence-disagreement":  func(input *CaptureInput) { input.CurrencyStatus = StatusRepealed },
		"bad-status-digest":             func(input *CaptureInput) { input.ExpectedStatusSHA256 = hashText("wrong") },
		"missing-status-id":             func(input *CaptureInput) { input.StatusEvidenceID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			input, adapter := fixtureInput(t)
			mutate(&input)
			if _, err := verifyCaptureWithAdapter(input, adapter); err == nil {
				t.Fatal("expected invalid metadata/proof to be rejected")
			}
		})
	}
}

func TestVerifyCaptureRejectsRawDigestMismatch(t *testing.T) {
	input, adapter := fixtureInput(t)
	input.ExpectedRawSHA256 = hex.EncodeToString(make([]byte, sha256.Size))
	if _, err := verifyCaptureWithAdapter(input, adapter); err == nil {
		t.Fatal("expected mismatched raw digest to be rejected")
	}
}

func TestVerifyCaptureAllowsAppendOnlyNewCaptureAndStatusObservation(t *testing.T) {
	input, adapter := fixtureInput(t)
	input.SourceSnapshotID = "fixture-capture-2027"
	input.StatusEvidenceID = "fixture-status-2027"
	input.StatusObservedAt = "2027-01-01T12:00:00+08:00"
	input.StatusEvidenceRaw = []byte(`{"status":"effective","observed_at":"2027-01-01T12:00:00+08:00"}`)
	input.ExpectedStatusSHA256 = hashBytes(input.StatusEvidenceRaw)
	for i := range input.Articles {
		input.Articles[i].SourceSnapshotID = input.SourceSnapshotID
		input.Articles[i].StatusEvidenceID = input.StatusEvidenceID
	}
	verified, err := verifyCaptureWithAdapter(input, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if verified.SourceSnapshotID != input.SourceSnapshotID || verified.StatusEvidenceID != input.StatusEvidenceID ||
		verified.StatusObservedAt != input.StatusObservedAt {
		t.Fatalf("new capture/status identity not preserved in result: %+v", verified)
	}
}

func hashBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func hashText(value string) string { return hashBytes([]byte(value)) }
