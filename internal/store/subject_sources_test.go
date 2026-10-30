package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSubjectSourceManifestConfigurationIsVersionedAndServerOwned(t *testing.T) {
	st := &Store{}
	manifest := `{"source_id":"synthetic.uscc.demo","source_version":1,"display_name":"演示受控主体目录","verification_scope":"synthetic_controlled","records":[{"record_id":"demo-a","display_name":"示例甲公司","uscc":"91350100m000100y43","source_as_of":"2026-10-30T00:00:00Z","valid_from":"2020-01-01T00:00:00Z","valid_until":null}]}`
	if err := st.ConfigureSubjectSourceManifest([]byte(manifest)); err != nil {
		t.Fatal(err)
	}
	configured := st.configuredSubjectSource()
	if configured == nil || configured.SourceID != syntheticSubjectSourceID || configured.Records[0].USCC != "91350100M000100Y43" {
		t.Fatalf("manifest was not normalized: %#v", configured)
	}
	manifestV2 := strings.ReplaceAll(manifest, "\"source_version\":1", "\"source_version\":2")
	if err := st.ConfigureSubjectSourceManifest([]byte(manifestV2)); err != nil {
		t.Fatalf("new immutable catalog version rejected: %v", err)
	}
	changedSameVersion := strings.ReplaceAll(manifestV2, "示例甲公司", "篡改目录")
	if err := st.ConfigureSubjectSourceManifest([]byte(changedSameVersion)); err == nil || !strings.Contains(err.Error(), "without a version increment") {
		t.Fatalf("same-version changed payload was accepted: %v", err)
	}
	manifestV3 := strings.ReplaceAll(manifestV2, "\"source_version\":2", "\"source_version\":3")
	manifestV3 = strings.Replace(manifestV3, `}]}`, `},{"record_id":"demo-b","display_name":"示例甲公司别名记录","uscc":"91350100m000100y43","source_as_of":"2026-10-30T00:00:00Z","valid_from":"2020-01-01T00:00:00Z","valid_until":null}]}`, 1)
	if err := st.ConfigureSubjectSourceManifest([]byte(manifestV3)); err != nil {
		t.Fatalf("duplicate identifier rows should be preserved for ambiguous-match handling: %v", err)
	}
	if configured := st.configuredSubjectSource(); configured == nil || len(configured.Records) != 2 {
		t.Fatalf("ambiguous source rows were dropped: %+v", configured)
	}
	if err := st.ConfigureSubjectSourceManifest([]byte(manifest)); err == nil || !strings.Contains(err.Error(), "backwards") {
		t.Fatalf("older catalog version was accepted: %v", err)
	}
	future := strings.ReplaceAll(manifestV3, "2026-10-30T00:00:00Z", "2030-01-01T00:00:00Z")
	future = strings.ReplaceAll(future, "\"source_version\":3", "\"source_version\":4")
	if err := st.ConfigureSubjectSourceManifest([]byte(future)); err == nil {
		t.Fatal("future source_as_of was accepted")
	}
	zeroValidity := strings.ReplaceAll(manifestV3, "\"source_version\":3", "\"source_version\":4")
	zeroValidity = strings.ReplaceAll(zeroValidity, "2020-01-01T00:00:00Z", "2026-10-30T00:00:00Z")
	zeroValidity = strings.ReplaceAll(zeroValidity, `"valid_until":null`, `"valid_until":"2026-10-30T00:00:00Z"`)
	if err := st.ConfigureSubjectSourceManifest([]byte(zeroValidity)); err == nil {
		t.Fatal("zero-length source validity interval was accepted")
	}
	unknown := strings.Replace(manifest, `"records":`, `"unexpected":true,"records":`, 1)
	if err := st.ConfigureSubjectSourceManifest([]byte(unknown)); err == nil {
		t.Fatal("unknown manifest field was accepted")
	}
	if err := st.ConfigureSubjectSourceManifest([]byte(manifest + ` {}`)); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
}

func TestSubjectSourceAndPinnedParseMarshalFixtures(t *testing.T) {
	asOf := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	captured := time.Date(2026, 10, 31, 2, 0, 0, 0, time.UTC)
	requestID := "sch_01"
	asOfPtr, validFromPtr := asOf, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	fixtures := map[string]any{
		"subject-sources.json":                SubjectSourceDirectoryResult{Items: []SubjectSourceDirectoryItem{{SourceID: syntheticSubjectSourceID, SourceVersion: 3, DisplayName: "演示受控主体目录", VerificationScope: "synthetic_controlled"}}},
		"subject-source-snapshots.json":       SubjectSourceSnapshotListResult{Items: []SubjectSourceSnapshotResult{{SnapshotID: "ssnap_01", SourceID: syntheticSubjectSourceID, SourceVersion: 3, SourceDigest: strings.Repeat("0", 64), SourceScope: "synthetic_controlled", CapturedAt: captured, RecordCount: 2, Label: "演示受控主体目录"}}},
		"subject-source-snapshot-detail.json": SubjectSourceSnapshotDetail{SubjectSourceSnapshotResult: SubjectSourceSnapshotResult{SnapshotID: "ssnap_01", SourceID: syntheticSubjectSourceID, SourceVersion: 3, SourceDigest: strings.Repeat("0", 64), SourceScope: "synthetic_controlled", CapturedAt: captured, RecordCount: 2, Label: "演示受控主体目录"}, CaseID: "case_01"},
		"subject-source-verify-match.json":    SubjectSourceVerifyResult{RequestID: &requestID, State: "pending_decision", MatchOutcome: "exact_match", SnapshotID: "ssnap_01", SourceCatalogID: syntheticSubjectSourceID, SourceVersion: 3, SourceDigest: strings.Repeat("0", 64), SourceScope: "synthetic_controlled", SourceAsOf: &asOfPtr, ValidFrom: &validFromPtr, MatchedRecord: &SubjectSourceMatchedRecord{DisplayName: "示例甲公司", MaskedIdentifier: "9135••••••••••0Y43"}},
		"material-parses.json":                MaterialParseListResult{MaterialID: "mat_01", MaterialVersion: 2, DocumentID: "doc_02", Items: []MaterialParseSummary{{ParsedDocumentID: "parse_03", Status: "ready", ParserID: "text", ParserVersion: "1", CreatedAt: "2026-10-31T01:00:00Z", OriginalDigest: strings.Repeat("a", 64), NormalizedDigest: strings.Repeat("b", 64)}}},
		"material-parsed-chunk.json":          MaterialParsedChunk{MaterialID: "mat_01", MaterialVersion: 2, DocumentID: "doc_02", ParsedDocumentID: "parse_03", Filename: "合同.txt", OriginalDigest: strings.Repeat("a", 64), ParseDigest: strings.Repeat("b", 64), TotalRunes: 3, Offset: 1, Text: "约", NextOffset: intPointer(2)},
	}
	for name, value := range fixtures {
		t.Run(name, func(t *testing.T) {
			got, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join("..", "..", "testdata", "contracts", "subjects", name))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != strings.TrimSpace(string(want)) {
				t.Fatalf("Go wire drifted from shared fixture\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

func intPointer(value int) *int { return &value }

func TestSubjectSourceDirectoryWireOmitsRecordIdentity(t *testing.T) {
	body, err := json.Marshal(SubjectSourceDirectoryResult{Items: []SubjectSourceDirectoryItem{{
		SourceID: syntheticSubjectSourceID, SourceVersion: 1, DisplayName: "演示受控主体目录", VerificationScope: "synthetic_controlled",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, required := range []string{`"items":[`, `"source_id":"synthetic.uscc.demo"`, `"source_version":1`, `"verification_scope":"synthetic_controlled"`} {
		if !strings.Contains(got, required) {
			t.Fatalf("wire output missing %s: %s", required, got)
		}
	}
	for _, forbidden := range []string{`"record_id":`, `"uscc":`, "91350100"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("wire output leaked %q: %s", forbidden, got)
		}
	}
}
