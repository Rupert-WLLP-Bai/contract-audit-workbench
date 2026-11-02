package evidencebundle

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

func tenantBundleFixture(t *testing.T) ([]byte, ed25519.PublicKey, string) {
	t.Helper()
	seed := bytes.Repeat([]byte{0x37}, ed25519.SeedSize)
	private := ed25519.NewKeyFromSeed(seed)
	public := private.Public().(ed25519.PublicKey)
	decisions := []byte("[]")
	decisionHash := sha256.Sum256(decisions)
	inputHash, outputHash := fmt.Sprintf("%064x", 1), fmt.Sprintf("%064x", 2)
	id := Identity{
		CaseID: "case-a", ReportID: "report-a", ReportVersion: 1, RunID: "run-a", AttemptID: "attempt-a",
		InputSHA256: inputHash, OutputSHA256: outputHash, DecisionSnapshotSHA256: hex.EncodeToString(decisionHash[:]),
		ExportedAt: "2026-11-02T00:00:00Z", HistoryCutoff: "2026-11-01T23:59:00Z",
		Decisions: []HistoryRecord{}, ManualFindings: []HistoryRecord{}, TenantID: "tenant-a",
	}
	report := []byte(fmt.Sprintf(`{"schema_version":"case-report/v1","report_id":"report-a","report_version":1,"case":{"id":"case-a"},"run":{"id":"run-a","case_id":"case-a"},"attempt":{"id":"attempt-a","run_id":"run-a"},"input":{"attempt_id":"attempt-a","run_id":"run-a","input_hash":"%s","documents":[]},"input_hash":"%s","output_hash":"%s","decision_snapshot_hash":"%s","decisions":[],"rulepack":{"availability":"unavailable","snapshot":null,"snapshot_sha256":""}}`,
		inputHash, inputHash, outputHash, hex.EncodeToString(decisionHash[:])))
	history := []byte(`{"cutoff":"2026-11-01T23:59:00Z","scope":"case:case-a","records":[]}`)
	originals := []byte(`{"case_id":"case-a","run_id":"run-a","attempt_id":"attempt-a","entries":[]}`)
	sources := []Source{
		{Path: ReportPath, Data: report},
		{Path: PDFPath, Data: []byte("%PDF-1.7\nsynthetic fixture\n")},
		{Path: ManualFindingsPath, Data: history},
		{Path: OriginalsIndexPath, Data: originals},
		{Path: DecisionsPath, Data: history},
	}
	var archive bytes.Buffer
	if err := WriteV2(t.Context(), &archive, id, sources, private); err != nil {
		t.Fatalf("write signed v2 fixture: %v", err)
	}
	fingerprint, err := PublicKeyFingerprint(public)
	if err != nil {
		t.Fatalf("fingerprint test key: %v", err)
	}
	return archive.Bytes(), public, fingerprint
}

func TestVerifyForTenantRequiresSignedV2TenantAndTrustedSignature(t *testing.T) {
	archive, public, fingerprint := tenantBundleFixture(t)
	valid, err := VerifyForTenant(bytes.NewReader(archive), int64(len(archive)), public, fingerprint, "tenant-a")
	if err != nil || valid.Schema != SchemaV2 || valid.Identity.TenantID != "tenant-a" {
		t.Fatalf("valid v2 tenant bundle rejected: schema=%q tenant=%q err=%v", valid.Schema, valid.Identity.TenantID, err)
	}
	if _, err := VerifyForTenant(bytes.NewReader(archive), int64(len(archive)), public, fingerprint, "tenant-b"); err == nil {
		t.Fatal("valid signature for tenant-a must fail an expected-tenant assertion for tenant-b")
	}
	wrongKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x38}, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	if _, err := VerifyForTenant(bytes.NewReader(archive), int64(len(archive)), wrongKey, "", "tenant-a"); err == nil {
		t.Fatal("v2 tenant assertion must not bypass trusted-key signature verification")
	}
}
