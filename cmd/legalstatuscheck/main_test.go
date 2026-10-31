package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"contractaudit/internal/legalnorm"
)

func TestRunRequiresOneMode(t *testing.T) {
	if code, err := run(config{}); err == nil || code != 1 {
		t.Fatalf("empty config: code=%d err=%v, want 1", code, err)
	}
	if code, err := run(config{assetDir: t.TempDir(), captureDir: t.TempDir()}); err == nil || code != 1 {
		t.Fatalf("both modes: code=%d err=%v, want 1", code, err)
	}
}

func TestRunMissingCandidatesFail(t *testing.T) {
	code, err := run(config{assetDir: t.TempDir(), sourceID: "all"})
	if err != nil {
		t.Fatalf("missing candidates must be a reported failure, not a hard error: %v", err)
	}
	if code != 1 {
		t.Fatalf("missing candidates exit=%d, want 1", code)
	}
}

func TestRunInvalidCandidateFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "raw"), 0o755); err != nil {
		t.Fatal(err)
	}
	editions := legalnorm.SourceEditions()
	if len(editions) == 0 {
		t.Fatal("registry exposes no editions")
	}
	pinned, err := legalnorm.PinnedStatus(editions[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "raw", pinned.StatusEvidenceID+".json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, err := run(config{assetDir: dir, sourceID: editions[0].SourceID})
	if err != nil {
		t.Fatalf("invalid candidate must be a reported failure: %v", err)
	}
	if code != 1 {
		t.Fatalf("invalid candidate exit=%d, want 1", code)
	}
}

func TestRunUnchangedFromFrozenAssets(t *testing.T) {
	root := os.Getenv("LEGALNORM_ASSET_DIR")
	if root == "" {
		t.Skip("set LEGALNORM_ASSET_DIR to the external official legal-sources directory")
	}
	code, err := run(config{assetDir: root, sourceID: "all"})
	if err != nil {
		t.Fatalf("unchanged frozen assets: %v", err)
	}
	if code != 0 {
		t.Fatalf("unchanged frozen assets exit=%d, want 0", code)
	}
}

func TestRunChangedCandidateRequiresNewCapture(t *testing.T) {
	root := os.Getenv("LEGALNORM_ASSET_DIR")
	if root == "" {
		t.Skip("set LEGALNORM_ASSET_DIR to the external official legal-sources directory")
	}
	edition := legalnorm.SourceEdition{SourceID: "CN-CONTRACT-LAW-HISTORY", VersionID: "1999-03-15-original"}
	pinned, err := legalnorm.PinnedStatus(edition)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "raw", pinned.StatusEvidenceID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	mutated := false
	for _, row := range doc.Rows {
		if row["bbbs"] == "2c909fdd678bf17901678bf6053a0217" {
			row["sxx"] = float64(3) // synthetic: a fabricated "now effective" registry code
			mutated = true
		}
	}
	if !mutated {
		t.Fatal("pinned registry record missing from the frozen status capture")
	}
	changed, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "raw"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "raw", pinned.StatusEvidenceID+".json"), changed, 0o644); err != nil {
		t.Fatal(err)
	}
	code, err := run(config{assetDir: dir, sourceID: edition.SourceID})
	if err != nil {
		t.Fatalf("changed candidate: %v", err)
	}
	if code != 2 {
		t.Fatalf("changed candidate exit=%d, want 2", code)
	}
}

func TestCaptureRefusesExistingDirectoryAndKeepsBytes(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("previous evidence"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOfficialResponses(dir, legalnorm.SourceEditions(), time.Second); err == nil {
		t.Fatal("existing capture directory was accepted")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "previous evidence" {
		t.Fatalf("existing evidence changed: %q %v", data, err)
	}
}

func TestRedirectPolicyRefusesHTTPDowngrade(t *testing.T) {
	first, err := http.NewRequest(http.MethodGet, "https://flk.npc.gov.cn/law-search/search/list", nil)
	if err != nil {
		t.Fatal(err)
	}
	downgrade, err := http.NewRequest(http.MethodGet, "http://flk.npc.gov.cn/law-search/search/list", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := officialRedirectPolicy(downgrade, []*http.Request{first}); err == nil {
		t.Fatal("same-host HTTP downgrade was accepted")
	}
	secure, err := http.NewRequest(http.MethodGet, "https://flk.npc.gov.cn/law-search/other", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := officialRedirectPolicy(secure, []*http.Request{first}); err != nil {
		t.Fatalf("same-host HTTPS redirect was refused: %v", err)
	}
}
