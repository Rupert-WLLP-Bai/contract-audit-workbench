package legalnorm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupplementalNPCStatusMappingDoesNotTreatRepealAsEffective(t *testing.T) {
	adapter, err := adapterForSnapshot("S10_contract_law_history")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		code   int
		status string
	}{
		{1, StatusRepealed}, {3, StatusEffective}, {4, StatusNotYetEffective}, {2, ""},
	} {
		raw, err := json.Marshal(map[string]any{"rows": []any{map[string]any{
			"bbbs": adapter.statusRecordID, "title": adapter.registryTitle, "gbrq": adapter.promulgatedOn,
			"sxrq": adapter.statusEffectiveAt, "sxx": tc.code,
		}}})
		if err != nil {
			t.Fatal(err)
		}
		status, err := verifyStatusEvidence(CaptureInput{StatusEvidenceRaw: raw}, adapter)
		if tc.status == "" {
			if err == nil {
				t.Fatalf("status code %d should fail closed", tc.code)
			}
		} else if err != nil || status != tc.status {
			t.Fatalf("status code %d = %q, %v; want %q", tc.code, status, err, tc.status)
		}
	}
}

func TestHistoricalEditorialHeadingIsExactAndCannotChangeStatutoryText(t *testing.T) {
	adapter, err := adapterForSnapshot("S10_contract_law_history")
	if err != nil {
		t.Fatal(err)
	}
	input := CaptureInput{Articles: []ArticleRef{{ArticleNumber: 114}}}
	blocks := []string{"第一百一十四条 【违约金】正文;后文。", "第二段。"}
	if err := applyEditorialAnnotations(blocks, input, adapter); err != nil {
		t.Fatal(err)
	}
	if blocks[0] != "第一百一十四条 正文;后文。" {
		t.Fatalf("unexpected normalized source %q", blocks[0])
	}
	articles, err := extractArticles(blocks)
	if err != nil || len(articles) != 1 || articles[0].body != "第一百一十四条 正文;后文。 第二段。" {
		t.Fatalf("statutory text or ASCII punctuation changed: %+v %v", articles, err)
	}
	if err := applyEditorialAnnotations([]string{"第一百一十四条 【其他标题】正文;后文。"}, input, adapter); err == nil {
		t.Fatal("unrecognized publisher annotation was accepted")
	}
	if err := applyEditorialAnnotations(blocks, CaptureInput{Articles: []ArticleRef{{ArticleNumber: 115}}}, adapter); err == nil {
		t.Fatal("historical article without a documented annotation was accepted")
	}
}

// These checks require the separately preserved official responses; they do not
// fetch source material or turn a registry snapshot into a legal opinion.
func TestSupplementalOfficialCapturesAndIndependentCrosscheck(t *testing.T) {
	root := os.Getenv("LEGALNORM_SUPPLEMENTAL_ASSET_DIR")
	if root == "" {
		t.Skip("set LEGALNORM_SUPPLEMENTAL_ASSET_DIR to the external supplemental legal assets")
	}
	cases := []struct {
		id, version, status, until string
		count                      int
	}{
		{"CN-CONTRACT-LAW-HISTORY", "1999-03-15-original", StatusRepealed, "2021-01-01", 5},
		{"CN-SPC-TIME-EFFECT", "2020-12-29-fashi-2020-15", StatusEffective, "", 6},
		{"CN-SPC-CONTRACT-GENERAL-INTERPRETATION", "2023-12-04-fashi-2023-13", StatusEffective, "", 7},
	}
	var historical CaptureInput
	count := 0
	for _, tc := range cases {
		capture, err := LoadSupplementalCapture(root, tc.id)
		if err != nil {
			t.Fatalf("%s: %v", tc.id, err)
		}
		verified, err := VerifyBaseline(capture)
		if err != nil {
			t.Fatalf("%s baseline: %v", tc.id, err)
		}
		if capture.VersionID != tc.version || capture.CurrencyStatus != tc.status || capture.EffectiveUntil != tc.until || len(verified.Capture.Articles) != tc.count {
			t.Fatalf("%s edition/status/end/count mismatch: %+v", tc.id, capture)
		}
		full := []rune(verified.Capture.FullText)
		for _, article := range verified.Capture.Articles {
			if article.Start < 0 || article.End > len(full) || article.End <= article.Start || string(full[article.Start:article.End]) != article.FullText {
				t.Fatalf("%s/%d does not retain exact canonical source offsets", tc.id, article.Ref.ArticleNumber)
			}
		}
		count += len(verified.Capture.Articles)
		if tc.id == "CN-CONTRACT-LAW-HISTORY" {
			historical = capture
		}
		changed := capture
		changed.EffectiveUntil = "2099-01-01"
		if _, err := VerifyBaseline(changed); err == nil {
			t.Fatalf("%s accepted an unproven effective end", tc.id)
		}
		changed = capture
		changed.StatusEvidenceRaw = append([]byte(nil), capture.StatusEvidenceRaw...)
		changed.StatusEvidenceRaw[0] ^= 1
		if _, err := VerifyBaseline(changed); err == nil {
			t.Fatalf("%s accepted altered registry bytes", tc.id)
		}
	}
	if count != 18 {
		t.Fatalf("verified %d articles, want 18", count)
	}
	raw, err := os.ReadFile(filepath.Join(root, "raw", "S15_audit_official_contract_law_crosscheck.html"))
	if err != nil {
		t.Fatal(err)
	}
	if digest(raw) != "33807920b25020a2bcc37e58977e71bb368cf277b4e66f80be9b33ff81373d99" {
		t.Fatal("independent crosscheck bytes differ from the archived capture")
	}
	blocks, err := htmlTextBlocks(raw, "text/html; charset=utf-8")
	if err != nil {
		t.Fatal(err)
	}
	articles, err := extractArticles(blocks)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range historical.Articles {
		var matches []string
		for _, article := range articles {
			if article.number == ref.ArticleNumber {
				matches = append(matches, article.body)
			}
		}
		if len(matches) != 1 {
			t.Fatalf("crosscheck article %d has %d occurrences", ref.ArticleNumber, len(matches))
		}
		if ref.ArticleNumber != 114 {
			if matches[0] != ref.FullText {
				t.Fatalf("crosscheck article %d differs", ref.ArticleNumber)
			}
			continue
		}
		left, right := []rune(ref.FullText), []rune(matches[0])
		if len(left) != len(right) || len(left) <= 101 || firstRuneDifference(ref.FullText, matches[0]) != 101 || left[101] != ';' || right[101] != '；' ||
			strings.Replace(ref.FullText, ";", "；", 1) != matches[0] {
			t.Fatal("article 114 strict crosscheck did not retain the archived semicolon difference")
		}
	}
}
