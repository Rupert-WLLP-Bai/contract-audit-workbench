package legalnorm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type supplementalSource struct {
	snapshot        string
	originalVersion string
	status          string
	articleNumbers  []int
}

var supplementalSources = map[string]supplementalSource{
	"CN-CONTRACT-LAW-HISTORY": {
		snapshot: "S10_contract_law_history", originalVersion: "1999-03-15-original",
		status: StatusRepealed, articleNumbers: []int{39, 40, 41, 107, 114},
	},
	"CN-SPC-TIME-EFFECT": {
		snapshot: "S12_sc_time_effect_2020_15", originalVersion: "法释〔2020〕15号",
		status: StatusEffective, articleNumbers: []int{1, 2, 3, 4, 9, 20},
	},
	"CN-SPC-CONTRACT-GENERAL-INTERPRETATION": {
		snapshot: "S13_sc_contract_general_2023_13", originalVersion: "法释〔2023〕13号",
		status: StatusEffective, articleNumbers: []int{1, 4, 9, 10, 60, 64, 65},
	},
}

// LoadSupplementalCapture builds an assertion-bearing import from the frozen
// external assets. Source bytes, registry bytes and every complete article are
// verified by the same baseline verifier used by the formal import endpoint.
// Each source is independent: a missing source need not prevent the others.
func LoadSupplementalCapture(assetDir, sourceID string) (CaptureInput, error) {
	source, ok := supplementalSources[sourceID]
	if !ok {
		return CaptureInput{}, fmt.Errorf("unsupported supplemental source %q", sourceID)
	}
	adapter, err := adapterForSnapshot(source.snapshot)
	if err != nil {
		return CaptureInput{}, err
	}
	baseline := adapter.baseline
	if baseline == nil || adapter.sourceID != sourceID {
		return CaptureInput{}, fmt.Errorf("supplemental source has no registered official baseline")
	}
	readFile := func(name string) ([]byte, error) {
		data, err := os.ReadFile(filepath.Join(assetDir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		return data, nil
	}
	raw, err := readFile(filepath.Join("raw", source.snapshot+".html"))
	if err != nil {
		return CaptureInput{}, err
	}
	statusRaw, err := readFile(filepath.Join("raw", baseline.StatusEvidenceID+".json"))
	if err != nil {
		return CaptureInput{}, err
	}
	articleBytes, err := readFile("supplemental_articles.json")
	if err != nil {
		return CaptureInput{}, err
	}
	var manifest struct {
		Articles []struct {
			SourceID      string `json:"source_id"`
			Snapshot      string `json:"source_snapshot_id"`
			Version       string `json:"version_id"`
			Number        int    `json:"article_no"`
			Label         string `json:"article_label"`
			FullText      string `json:"full_text"`
			StatutoryText string `json:"statutory_text"`
		} `json:"articles"`
	}
	if err := json.Unmarshal(articleBytes, &manifest); err != nil {
		return CaptureInput{}, fmt.Errorf("decode supplemental article manifest: %w", err)
	}
	capture := CaptureInput{
		SourceID: adapter.sourceID, SourceSnapshotID: source.snapshot, VersionID: adapter.versionID,
		Title: adapter.title, OfficialURL: adapter.officialURL, ContentType: baseline.SourceContentType,
		RawBytes: raw, ExpectedRawSHA256: baseline.RawSHA256, SourceCapturedAt: baseline.SourceCapturedAt,
		PromulgatedOn: adapter.promulgatedOn, EffectiveFrom: adapter.effectiveFrom, EffectiveUntil: adapter.effectiveUntil,
		CurrencyStatus: source.status, StatusEvidenceID: baseline.StatusEvidenceID, StatusObservedAt: baseline.StatusObservedAt,
		StatusEvidenceRaw: statusRaw, ExpectedStatusSHA256: baseline.StatusSHA256,
	}
	wanted := make(map[int]bool, len(source.articleNumbers))
	for _, number := range source.articleNumbers {
		wanted[number] = false
	}
	for _, article := range manifest.Articles {
		if article.SourceID != sourceID && article.Snapshot != source.snapshot {
			continue
		}
		seen, expected := wanted[article.Number]
		if !expected || seen || article.SourceID != sourceID || article.Snapshot != source.snapshot || article.Version != source.originalVersion {
			return CaptureInput{}, fmt.Errorf("supplemental article identity or number is invalid for %s/%d", sourceID, article.Number)
		}
		text := NormalizeWhitespace(article.FullText)
		if sourceID == "CN-CONTRACT-LAW-HISTORY" && (article.StatutoryText == "" || NormalizeWhitespace(article.StatutoryText) != text) {
			return CaptureInput{}, fmt.Errorf("historical article %d statutory text is inconsistent", article.Number)
		}
		capture.Articles = append(capture.Articles, ArticleRef{
			SourceID: sourceID, SourceSnapshotID: source.snapshot, VersionID: adapter.versionID,
			ArticleNumber: article.Number, ArticleLabel: article.Label, FullText: text, TextSHA256: digest([]byte(text)),
			CitationSpan: TextSpan{Start: 0, End: len([]rune(article.Label)), Quote: article.Label},
			PromulgatedOn: adapter.promulgatedOn, EffectiveFrom: adapter.effectiveFrom, EffectiveUntil: adapter.effectiveUntil,
			CurrencyStatus: source.status, StatusEvidenceID: baseline.StatusEvidenceID,
		})
		wanted[article.Number] = true
	}
	for _, number := range source.articleNumbers {
		if !wanted[number] {
			return CaptureInput{}, fmt.Errorf("supplemental source %s lacks article %d", sourceID, number)
		}
	}
	sort.Slice(capture.Articles, func(i, j int) bool { return capture.Articles[i].ArticleNumber < capture.Articles[j].ArticleNumber })
	if _, err := VerifyBaseline(capture); err != nil {
		return CaptureInput{}, fmt.Errorf("supplemental source %s failed official baseline verification: %w", sourceID, err)
	}
	return capture, nil
}
