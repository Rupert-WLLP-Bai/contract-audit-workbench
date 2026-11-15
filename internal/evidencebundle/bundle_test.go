// File metadata (manifest mirror):
// Purpose: Exercises evidence signing-key loading, container path rules, identity validation and signed write/verify round trips including rejection paths.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package evidencebundle

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bundleTestKeys(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x42}, ed25519.SeedSize))
	return private, private.Public().(ed25519.PublicKey)
}

func writeTestFile(t *testing.T, content []byte) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(name, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestPrivateKeyFromFileAcceptsOnlyUnencryptedEd25519PKCS8(t *testing.T) {
	private, _ := bundleTestKeys(t)
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	validPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if key, err := PrivateKeyFromFile(writeTestFile(t, validPEM)); err != nil || !key.Equal(private) {
		t.Fatalf("valid key was rejected: %v", err)
	}
	if _, err := PrivateKeyFromFile(""); err == nil {
		t.Fatal("empty filename was accepted")
	}
	if _, err := PrivateKeyFromFile(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("missing key file was accepted")
	}
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaDER, err := x509.MarshalPKCS8PrivateKey(ecdsaKey)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"garbage":         []byte("not a pem"),
		"encrypted type":  pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: der}),
		"non-ed25519":     pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecdsaDER}),
		"trailing blocks": append(append([]byte{}, validPEM...), validPEM...),
	}
	for name, content := range cases {
		if _, err := PrivateKeyFromFile(writeTestFile(t, content)); err == nil {
			t.Fatalf("%s key material was accepted", name)
		}
	}
}

func TestPublicKeyFromFileAcceptsOnlyEd25519PKIX(t *testing.T) {
	_, public := bundleTestKeys(t)
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	validPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	if key, err := PublicKeyFromFile(writeTestFile(t, validPEM)); err != nil || !key.Equal(public) {
		t.Fatalf("valid public key was rejected: %v", err)
	}
	if _, err := PublicKeyFromFile(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("missing public key file was accepted")
	}
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaDER, err := x509.MarshalPKIXPublicKey(&ecdsaKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"garbage":      []byte("not a pem"),
		"private type": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}),
		"non-ed25519":  pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: ecdsaDER}),
	}
	for name, content := range cases {
		if _, err := PublicKeyFromFile(writeTestFile(t, content)); err == nil {
			t.Fatalf("%s key material was accepted", name)
		}
	}
}

func TestPublicKeyFingerprintRejectsWrongLength(t *testing.T) {
	if _, err := PublicKeyFingerprint(nil); err == nil {
		t.Fatal("empty public key was fingerprinted")
	}
	if _, err := PublicKeyFingerprint(make([]byte, ed25519.PublicKeySize-1)); err == nil {
		t.Fatal("short public key was fingerprinted")
	}
	_, public := bundleTestKeys(t)
	first, err := PublicKeyFingerprint(public)
	if err != nil || len(first) != sha256.Size*2 {
		t.Fatalf("fingerprint is %q (%v)", first, err)
	}
	second, err := PublicKeyFingerprint(public)
	if err != nil || second != first {
		t.Fatalf("fingerprint is not stable: %q %q (%v)", first, second, err)
	}
}

func TestAllowedPathAcceptsOnlyContainerNames(t *testing.T) {
	for _, name := range []string{ReportPath, PDFPath, ManualFindingsPath, OriginalsIndexPath, DecisionsPath, "originals/000001.bin", "originals/999999.bin"} {
		if !allowedPath(name) {
			t.Fatalf("%q was rejected", name)
		}
	}
	for _, name := range []string{
		"", "manifest.json", "manifest.ed25519", "originals/index.json.bak", "originals/00001.bin",
		"originals/0000011.bin", "originals/00000a.bin", "originals/../000001.bin", "report/other.json",
		"/originals/000001.bin", "originals\\000001.bin",
	} {
		if allowedPath(name) {
			t.Fatalf("%q was accepted", name)
		}
	}
}

func bundleTestIdentity(t *testing.T) Identity {
	t.Helper()
	decisions := []byte("[]")
	decisionHash := sha256.Sum256(decisions)
	return Identity{
		CaseID: "case-a", ReportID: "report-a", ReportVersion: 1, RunID: "run-a", AttemptID: "attempt-a",
		InputSHA256: fmt.Sprintf("%064x", 1), OutputSHA256: fmt.Sprintf("%064x", 2),
		DecisionSnapshotSHA256: hex.EncodeToString(decisionHash[:]),
		ExportedAt:             "2026-11-15T00:00:00Z", HistoryCutoff: "2026-11-14T23:59:00Z",
		Decisions: []HistoryRecord{}, ManualFindings: []HistoryRecord{},
	}
}

func bundleTestSources(id Identity) []Source {
	report := fmt.Appendf(nil, `{"schema_version":"case-report/v1","report_id":"report-a","report_version":1,"case":{"id":"case-a"},"run":{"id":"run-a","case_id":"case-a"},"attempt":{"id":"attempt-a","run_id":"run-a"},"input":{"attempt_id":"attempt-a","run_id":"run-a","input_hash":"%s","documents":[]},"input_hash":"%s","output_hash":"%s","decision_snapshot_hash":"%s","decisions":[],"rulepack":{"availability":"unavailable","snapshot":null,"snapshot_sha256":""}}`,
		id.InputSHA256, id.InputSHA256, id.OutputSHA256, id.DecisionSnapshotSHA256)
	history := []byte(`{"cutoff":"2026-11-14T23:59:00Z","scope":"case:case-a","records":[]}`)
	originals := []byte(`{"case_id":"case-a","run_id":"run-a","attempt_id":"attempt-a","entries":[]}`)
	return []Source{
		{Path: ReportPath, Data: report},
		{Path: PDFPath, Data: []byte("%PDF-1.7\nsynthetic fixture\n")},
		{Path: ManualFindingsPath, Data: history},
		{Path: OriginalsIndexPath, Data: originals},
		{Path: DecisionsPath, Data: history},
	}
}

func TestWriteAndVerifyRoundTrip(t *testing.T) {
	private, public := bundleTestKeys(t)
	id := bundleTestIdentity(t)
	var archive bytes.Buffer
	if err := Write(context.Background(), &archive, id, bundleTestSources(id), private); err != nil {
		t.Fatal(err)
	}
	manifest, err := Verify(bytes.NewReader(archive.Bytes()), int64(archive.Len()), public, "")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != SchemaV1 || manifest.Identity.ReportID != "report-a" || manifest.SignatureAlgorithm != "Ed25519" || len(manifest.Entries) != 5 {
		t.Fatalf("round trip lost the signed manifest: %+v", manifest)
	}
	for i := 1; i < len(manifest.Entries); i++ {
		if manifest.Entries[i-1].Path > manifest.Entries[i].Path {
			t.Fatalf("manifest entries are not sorted: %+v", manifest.Entries)
		}
	}
	fingerprint, err := PublicKeyFingerprint(public)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(bytes.NewReader(archive.Bytes()), int64(archive.Len()), nil, fingerprint); err != nil {
		t.Fatalf("fingerprint-only verification failed: %v", err)
	}
	wrongKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x43}, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	if _, err := Verify(bytes.NewReader(archive.Bytes()), int64(archive.Len()), wrongKey, ""); err == nil {
		t.Fatal("wrong trusted key verified")
	}
	if _, err := Verify(bytes.NewReader(archive.Bytes()), int64(archive.Len()), nil, ""); err == nil {
		t.Fatal("verification without trust material succeeded")
	}
	if _, err := Verify(bytes.NewReader(archive.Bytes()), int64(archive.Len()), nil, strings.Repeat("A", 64)); err == nil {
		t.Fatal("uppercase fingerprint was accepted")
	}
	if _, err := Verify(bytes.NewReader(archive.Bytes()[:len(archive.Bytes())/2]), int64(archive.Len()/2), public, ""); err == nil {
		t.Fatal("truncated archive verified")
	}
	if _, err := Verify(bytes.NewReader(archive.Bytes()), int64(archive.Len()), make([]byte, 3), ""); err == nil {
		t.Fatal("short trusted key verified")
	}
}

func TestWriteRejectsInvalidInputs(t *testing.T) {
	private, _ := bundleTestKeys(t)
	id := bundleTestIdentity(t)
	sources := bundleTestSources(id)
	write := func(ctx context.Context, key ed25519.PrivateKey, identity Identity, entries []Source) error {
		var archive bytes.Buffer
		return Write(ctx, &archive, identity, entries, key)
	}
	if err := write(context.Background(), nil, id, sources); err == nil {
		t.Fatal("nil signing key was accepted")
	}
	if err := write(context.Background(), private[:16], id, sources); err == nil {
		t.Fatal("short signing key was accepted")
	}
	invalidIdentity := id
	invalidIdentity.CaseID = ""
	if err := write(context.Background(), private, invalidIdentity, sources); err == nil {
		t.Fatal("identity without a case was accepted")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := write(cancelled, private, id, sources); err == nil {
		t.Fatal("cancelled context was accepted")
	}
	duplicate := append(append([]Source{}, sources...), sources[0])
	if err := write(context.Background(), private, id, duplicate); err == nil {
		t.Fatal("duplicate entry path was accepted")
	}
	invalidPath := append([]Source{}, sources...)
	invalidPath[0] = Source{Path: "report/other.json", Data: []byte("x")}
	if err := write(context.Background(), private, id, invalidPath); err == nil {
		t.Fatal("unexpected entry path was accepted")
	}
	missing := sources[1:]
	if err := write(context.Background(), private, id, missing); err == nil {
		t.Fatal("missing required entry was accepted")
	}
	tooMany := append([]Source{}, sources...)
	for i := 0; i <= maxEntries; i++ {
		tooMany = append(tooMany, Source{Path: fmt.Sprintf("originals/%06d.bin", i+1), Data: []byte("x")})
	}
	if err := write(context.Background(), private, id, tooMany); err == nil {
		t.Fatal("oversized entry list was accepted")
	}
	openFailure := append([]Source{}, sources...)
	openFailure[0] = Source{Path: ReportPath, Open: func() (io.ReadCloser, error) { return nil, os.ErrPermission }}
	if err := write(context.Background(), private, id, openFailure); err == nil {
		t.Fatal("source open failure was accepted")
	}
}

func TestValidIdentityRejectsUnusableHistory(t *testing.T) {
	valid := bundleTestIdentity(t)
	if !validIdentity(valid) {
		t.Fatal("valid identity was rejected")
	}
	mutations := []struct {
		name   string
		mutate func(*Identity)
	}{
		{"case", func(id *Identity) { id.CaseID = "" }},
		{"report", func(id *Identity) { id.ReportID = "" }},
		{"version", func(id *Identity) { id.ReportVersion = 0 }},
		{"run", func(id *Identity) { id.RunID = "" }},
		{"attempt", func(id *Identity) { id.AttemptID = "" }},
		{"input hash", func(id *Identity) { id.InputSHA256 = "ABC" }},
		{"output hash", func(id *Identity) { id.OutputSHA256 = strings.Repeat("z", 64) }},
		{"decision hash", func(id *Identity) { id.DecisionSnapshotSHA256 = "" }},
		{"exported at", func(id *Identity) { id.ExportedAt = "yesterday" }},
		{"cutoff", func(id *Identity) { id.HistoryCutoff = "later" }},
		{"nil decisions", func(id *Identity) { id.Decisions = nil }},
		{"nil manual findings", func(id *Identity) { id.ManualFindings = nil }},
		{"record id", func(id *Identity) { id.Decisions = []HistoryRecord{{ID: "", CreatedAt: "2026-11-14T00:00:00Z"}} }},
		{"record time", func(id *Identity) { id.Decisions = []HistoryRecord{{ID: "decision-1", CreatedAt: "bad"}} }},
		{"record after cutoff", func(id *Identity) {
			id.Decisions = []HistoryRecord{{ID: "decision-1", CreatedAt: "2026-11-15T00:00:00Z"}}
		}},
		{"duplicate record", func(id *Identity) {
			id.ManualFindings = []HistoryRecord{{ID: "manual-1", CreatedAt: "2026-11-14T00:00:00Z"}, {ID: "manual-1", CreatedAt: "2026-11-14T00:00:00Z"}}
		}},
	}
	for _, check := range mutations {
		identity := valid
		check.mutate(&identity)
		if validIdentity(identity) {
			t.Fatalf("identity with %s change was accepted", check.name)
		}
	}
	withHistory := valid
	withHistory.Decisions = []HistoryRecord{{ID: "decision-1", CreatedAt: "2026-11-14T00:00:00Z"}}
	withHistory.ManualFindings = []HistoryRecord{{ID: "manual-1", CreatedAt: "2026-11-14T01:00:00Z"}}
	if !validIdentity(withHistory) {
		t.Fatal("valid bounded history was rejected")
	}
}
