package legalnorm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// These checks require the separately preserved official responses; they never
// mutate a baseline, capture row or file.
func TestStatusCheckComparesPinnedObservationsWithoutMutation(t *testing.T) {
	root := os.Getenv("LEGALNORM_ASSET_DIR")
	if root == "" {
		t.Skip("set LEGALNORM_ASSET_DIR to the external official legal-sources directory")
	}
	editions := SourceEditions()
	if len(editions) != 8 {
		t.Fatalf("registry exposes %d editions, want 8", len(editions))
	}
	for _, edition := range editions {
		pinned, err := PinnedStatus(edition)
		if err != nil {
			t.Fatalf("%s/%s: %v", edition.SourceID, edition.VersionID, err)
		}
		candidate := readPinnedStatusAsset(t, root, pinned.StatusEvidenceID)
		if digest(candidate) != pinned.StatusSHA256 {
			t.Fatalf("%s/%s pinned bytes differ from the registry digest", edition.SourceID, edition.VersionID)
		}
		check, err := CheckStatusResponse(edition, candidate)
		if err != nil || !check.Unchanged || !check.Verified || check.ObservedStatus == "" {
			t.Fatalf("%s/%s unchanged check: %+v %v", edition.SourceID, edition.VersionID, check, err)
		}
		if again, err := PinnedStatus(edition); err != nil || again.StatusSHA256 != pinned.StatusSHA256 {
			t.Fatalf("%s/%s baseline changed during a read-only check: %v", edition.SourceID, edition.VersionID, err)
		}
	}

	edition := SourceEdition{SourceID: "CN-CONTRACT-LAW-HISTORY", VersionID: "1999-03-15-original"}
	pinned, err := PinnedStatus(edition)
	if err != nil {
		t.Fatal(err)
	}
	raw := readPinnedStatusAsset(t, root, pinned.StatusEvidenceID)
	adapter, err := adapterForSource(edition.SourceID, edition.VersionID)
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
		if row["bbbs"] == adapter.statusRecordID {
			row["sxx"] = float64(3) // a fabricated "now effective" registry code
			mutated = true
		}
	}
	if !mutated {
		t.Fatal("pinned registry record missing from the status capture")
	}
	changed, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	check, err := CheckStatusResponse(edition, changed)
	if err != nil || check.Unchanged || !check.Verified || check.ObservedStatus != StatusEffective {
		t.Fatalf("changed official response was not reported as a new capture: %+v %v", check, err)
	}
	if digest(raw) != pinned.StatusSHA256 {
		t.Fatal("read-only check mutated the pinned bytes")
	}

	// A well-formed response that no longer matches the pinned identity stays
	// unverified instead of silently mapping a status.
	var renamed map[string]any
	if err := json.Unmarshal(raw, &renamed); err != nil {
		t.Fatal(err)
	}
	renamed["rows"] = []any{map[string]any{
		"bbbs": adapter.statusRecordID, "title": "另一个法律", "gbrq": adapter.promulgatedOn,
		"sxrq": adapter.statusEffectiveAt, "sxx": 1,
	}}
	unverifiedBytes, err := json.Marshal(renamed)
	if err != nil {
		t.Fatal(err)
	}
	unverified, err := CheckStatusResponse(edition, unverifiedBytes)
	if err != nil || unverified.Unchanged || unverified.Verified {
		t.Fatalf("mismatched identity was not reported as unverified: %+v %v", unverified, err)
	}

	for name, candidate := range map[string][]byte{
		"malformed-json": []byte("{not json"),
		"empty":          nil,
	} {
		if _, err := CheckStatusResponse(edition, candidate); err == nil {
			t.Fatalf("%s candidate was accepted", name)
		}
	}
}

func readPinnedStatusAsset(t *testing.T, root, statusEvidenceID string) []byte {
	t.Helper()
	for _, name := range []string{statusEvidenceID + ".json", statusEvidenceID + ".html"} {
		data, err := os.ReadFile(filepath.Join(root, "raw", name))
		if err == nil {
			return data
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	t.Fatalf("status evidence %q not found under %s/raw", statusEvidenceID, root)
	return nil
}
