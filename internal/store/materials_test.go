package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/docir"
)

func TestResolveMaterialPurposeRejectsPurposeForging(t *testing.T) {
	cases := []struct {
		role, purpose, want string
		ok                  bool
	}{
		{"procurement", "", "procurement", true},
		{"award", "", "award", true},
		{"draft_contract", "", "primary_contract", true},
		{"attachment", "supplement", "supplement", true},
		{"attachment", "", "other", true},
		{"attachment", "primary_contract", "", false},
		{"draft_contract", "supplement", "", false},
		{"unknown", "", "", false},
	}
	for _, tc := range cases {
		got, err := resolveMaterialPurpose(tc.role, tc.purpose)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("resolveMaterialPurpose(%q,%q)=(%q,%v), want (%q, ok=%v)", tc.role, tc.purpose, got, err, tc.want, tc.ok)
		}
	}
}

func TestValidateMaterialUploadOptionsRequiresCASForReplace(t *testing.T) {
	cases := []struct {
		options MaterialUploadOptions
		ok      bool
	}{
		{MaterialUploadOptions{}, true},
		{MaterialUploadOptions{Intent: "add", MaterialID: "m"}, false},
		{MaterialUploadOptions{Intent: "replace", MaterialID: "m"}, false},
		{MaterialUploadOptions{Intent: "replace", MaterialID: "m", ReplacesDocumentID: "d"}, true},
		{MaterialUploadOptions{Intent: "remove"}, false},
	}
	for _, tc := range cases {
		if err := validateMaterialUploadOptions(tc.options); (err == nil) != tc.ok {
			t.Errorf("validateMaterialUploadOptions(%+v)=%v, ok=%v", tc.options, err, tc.ok)
		}
	}
}

func TestMaterialVersionHistoryIsAppendOnly(t *testing.T) {
	old := MaterialVersion{TenantID: "t", OrgID: "o", CaseID: "c", MaterialID: "m", Version: 1, DocumentID: "d1"}
	changed := old
	changed.DocumentID = "d2"
	if err := validateAppendOnly(state{MaterialVersions: []MaterialVersion{old}}, state{MaterialVersions: []MaterialVersion{changed}}); err == nil {
		t.Fatal("material version rewrite was accepted")
	}
}

func TestIdempotencyHashSeparatesUploadAndGeneration(t *testing.T) {
	options := MaterialUploadOptions{Intent: "add"}
	upload := uploadRequestHash("upload", "actor", "case", "draft_contract", "primary_contract", "same.txt", "text/plain", []byte("same bytes"), options)
	generation := uploadRequestHash("generation", "actor", "case", "draft_contract", "primary_contract", "same.txt", "text/plain", []byte("same bytes"), options)
	if upload == generation {
		t.Fatal("upload and generation reused the same idempotency hash namespace")
	}
}

func TestMaterialStateCloneRetainsScopeAndTimestamps(t *testing.T) {
	now := time.Date(2026, 10, 31, 4, 5, 6, 123456789, time.UTC)
	primary := "mat-1"
	original := state{
		Materials:        []MaterialEntity{{TenantID: "tenant", OrgID: "org", CaseID: "case", ID: primary, Role: "draft_contract", Purpose: "primary_contract", LegacyStatus: "native", CurrentDocumentID: "doc-2", CurrentVersion: 2, CreatedBy: "actor", CreatedAt: now}},
		MaterialVersions: []MaterialVersion{{TenantID: "tenant", OrgID: "org", CaseID: "case", MaterialID: primary, Version: 2, DocumentID: "doc-2", PreviousDocument: "doc-1", CreatedBy: "actor", CreatedAt: now}},
		MaterialChains:   []CaseMaterialChain{{TenantID: "tenant", OrgID: "org", CaseID: "case", PrimaryMaterialID: &primary, LegacySelectionStatus: "confirmed", SelectedDocumentIDs: []string{"doc-2"}, SelectionVersion: 3, CreatedAt: now, UpdatedAt: now}},
	}
	cloned, err := original.clone()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original.Materials, cloned.Materials) || !reflect.DeepEqual(original.MaterialVersions, cloned.MaterialVersions) || !reflect.DeepEqual(original.MaterialChains, cloned.MaterialChains) {
		t.Fatalf("material scope or timestamps were lost by state clone: original=%+v cloned=%+v", original, cloned)
	}
}

func TestFrozenMaterialMetadataLeavesLegacyInputHashUnchanged(t *testing.T) {
	type legacyFrozenDocument struct {
		Document             Document `json:"document"`
		Ready                bool     `json:"ready"`
		FromOCR              bool     `json:"from_ocr"`
		ParseError           string   `json:"parse_error"`
		Text                 string   `json:"text"`
		IR                   docir.IR `json:"ir"`
		OriginalHash         string   `json:"original_hash"`
		NormalizedHash       *string  `json:"normalized_hash"`
		ParserRef            string   `json:"parser_ref"`
		ParsedDocumentID     string   `json:"parsed_document_id,omitempty"`
		SelectionOrder       int64    `json:"selection_order,omitempty"`
		ParsedArtifactDigest string   `json:"parsed_artifact_digest,omitempty"`
		ConfigHash           string   `json:"config_hash,omitempty"`
		ParserID             string   `json:"parser_id,omitempty"`
		ParserVersion        string   `json:"parser_version,omitempty"`
		NormalizationVersion string   `json:"normalization_version,omitempty"`
		IRDigest             string   `json:"ir_digest,omitempty"`
		ParsedCreatedAt      string   `json:"parsed_created_at,omitempty"`
		ParsedStatus         string   `json:"parsed_status,omitempty"`
	}
	document := Document{ID: "doc-legacy", TenantID: "tenant", OrgID: "org", CaseID: "case", Role: "draft_contract", Filename: "old.txt", MediaType: "text/plain", Digest: "sha256:old"}
	legacy := legacyFrozenDocument{Document: document, Ready: true, Text: "frozen text", OriginalHash: "sha256:old", ParserRef: "parser/1", ParsedDocumentID: "parsed-old", SelectionOrder: 1}
	current := FrozenDocument{Document: document, Ready: true, Text: "frozen text", OriginalHash: "sha256:old", ParserRef: "parser/1", ParsedDocumentID: "parsed-old", SelectionOrder: 1}
	snapshot := RunSnapshot{RunID: "run-old", DocumentIDs: []string{document.ID}, ParsedDocumentIDs: []string{"parsed-old"}}
	currentHash, err := frozenInputHash(snapshot, []FrozenDocument{current})
	if err != nil {
		t.Fatal(err)
	}
	snapshot.RunID = ""
	raw, err := json.Marshal(struct {
		Snapshot  RunSnapshot            `json:"snapshot"`
		Documents []legacyFrozenDocument `json:"documents"`
	}{Snapshot: snapshot, Documents: []legacyFrozenDocument{legacy}})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	legacyHash := hex.EncodeToString(digest[:])
	if currentHash != legacyHash || strings.Contains(string(raw), `"material"`) {
		t.Fatalf("nil material metadata changed legacy input bytes/hash: current=%s legacy=%s", currentHash, legacyHash)
	}
}

func TestFrozenMaterialIdentityUsesPinnedDocumentVersion(t *testing.T) {
	state := state{
		Materials: []MaterialEntity{{TenantID: "tenant", OrgID: "org", CaseID: "case", ID: "material", Role: "attachment", Purpose: "supplement", CurrentDocumentID: "doc-v2", CurrentVersion: 2}},
		MaterialVersions: []MaterialVersion{
			{TenantID: "tenant", OrgID: "org", CaseID: "case", MaterialID: "material", Version: 1, DocumentID: "doc-v1"},
			{TenantID: "tenant", OrgID: "org", CaseID: "case", MaterialID: "material", Version: 2, DocumentID: "doc-v2"},
		},
	}
	frozen := FrozenDocument{Document: Document{TenantID: "tenant", OrgID: "org", CaseID: "case", ID: "doc-v1"}}
	bindFrozenMaterialIdentity(state, &frozen)
	if frozen.Material == nil || frozen.Material.MaterialID != "material" || frozen.Material.Role != "attachment" || frozen.Material.Purpose != "supplement" || frozen.Material.Version != 1 {
		t.Fatalf("frozen input followed current entity instead of the pinned document version: %+v", frozen.Material)
	}
}
