// Package evidencebundle defines the signed, independently verifiable evidence
// container. A signature authenticates the canonical manifest and the manifest
// authenticates every other entry, including the PDF and original documents.
package evidencebundle

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

const (
	Schema             = "contract-audit-evidence/v1"
	OriginalsIndexPath = "originals/index.json"
	DecisionsPath      = "history/decisions.json"
	ManifestPath       = "manifest.json"
	SignaturePath      = "manifest.ed25519"
	ReportPath         = "report/report.json"
	PDFPath            = "report/report.pdf"
	ManualFindingsPath = "history/manual-findings.json"
	maxManifestBytes   = 4 << 20
	maxEntries         = 10000
)

// Identity identifies the immutable report and its exact frozen attempt.
// HistoryCutoff is the database snapshot boundary; ExportedAt is the service clock.
type Identity struct {
	CaseID                 string          `json:"case_id"`
	ReportID               string          `json:"report_id"`
	ReportVersion          int             `json:"report_version"`
	RunID                  string          `json:"run_id"`
	AttemptID              string          `json:"attempt_id"`
	InputSHA256            string          `json:"input_sha256"`
	OutputSHA256           string          `json:"output_sha256"`
	DecisionSnapshotSHA256 string          `json:"decision_snapshot_sha256"`
	ExportedAt             string          `json:"exported_at"`
	HistoryCutoff          string          `json:"history_cutoff"`
	Decisions              []HistoryRecord `json:"decisions"`
	ManualFindings         []HistoryRecord `json:"manual_findings"`
}

// HistoryRecord names a selected append-only row and its creation time.
type HistoryRecord struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
}

type Entry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	Schema             string   `json:"schema"`
	Identity           Identity `json:"identity"`
	SignatureAlgorithm string   `json:"signature_algorithm"`
	PublicKey          string   `json:"public_key_spki_base64"`
	PublicKeySHA256    string   `json:"public_key_spki_sha256"`
	Entries            []Entry  `json:"entries"`
}

// Source.Path must be one of the fixed container names, never an uploaded name.
type Source struct {
	Path string
	Data []byte
	// Open, when supplied, reads the source once; Data is used otherwise.
	Open func() (io.ReadCloser, error)
}

// PrivateKeyFromFile accepts only an unencrypted PEM-encoded Ed25519 PKCS#8 key.
// Configuration errors never include the file contents.
func PrivateKeyFromFile(filename string) (ed25519.PrivateKey, error) {
	if strings.TrimSpace(filename) == "" {
		return nil, errors.New("evidence signing key is not configured")
	}
	raw, err := os.ReadFile(filename)
	if err != nil {
		return nil, errors.New("cannot read evidence signing key")
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("evidence signing key must be an unencrypted PKCS#8 PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid evidence signing key")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("evidence signing key must be Ed25519")
	}
	return key, nil
}

// PublicKeyFromFile accepts a PEM-encoded PKIX/SPKI public key supplied by the
// verifier, not one obtained from the untrusted archive.
func PublicKeyFromFile(filename string) (ed25519.PublicKey, error) {
	raw, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read trusted public key: %w", err)
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("trusted public key must be a PKIX PUBLIC KEY PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid trusted public key")
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("trusted public key must be Ed25519")
	}
	return key, nil
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// PublicKeyFingerprint hashes the DER-encoded SubjectPublicKeyInfo, providing a
// stable fingerprint independent of PEM line wrapping.
func PublicKeyFingerprint(key ed25519.PublicKey) (string, error) {
	if len(key) != ed25519.PublicKeySize {
		return "", errors.New("invalid Ed25519 public key")
	}
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return "", err
	}
	return digest(der), nil
}

func allowedPath(name string) bool {
	if name == ReportPath || name == PDFPath || name == ManualFindingsPath || name == OriginalsIndexPath || name == DecisionsPath {
		return true
	}
	// Original names are sequence numbers, never filenames or document IDs.
	if len(name) != len("originals/000000.bin") || !strings.HasPrefix(name, "originals/") || !strings.HasSuffix(name, ".bin") {
		return false
	}
	for _, c := range name[len("originals/") : len("originals/")+6] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return path.Clean(name) == name
}

func validIdentity(id Identity) bool {
	if id.CaseID == "" || id.ReportID == "" || id.ReportVersion < 1 || id.RunID == "" || id.AttemptID == "" ||
		!validHash(id.InputSHA256) || !validHash(id.OutputSHA256) || !validHash(id.DecisionSnapshotSHA256) {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, id.ExportedAt)
	if err != nil {
		return false
	}
	cutoff, err := time.Parse(time.RFC3339Nano, id.HistoryCutoff)
	if err != nil || id.Decisions == nil || id.ManualFindings == nil {
		return false
	}
	for _, records := range [][]HistoryRecord{id.Decisions, id.ManualFindings} {
		seen := make(map[string]bool, len(records))
		for _, record := range records {
			created, err := time.Parse(time.RFC3339Nano, record.CreatedAt)
			if err != nil || created.After(cutoff) || record.ID == "" || seen[record.ID] {
				return false
			}
			seen[record.ID] = true
		}
	}
	return true
}

func validHash(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// Write signs the exact JSON encoding defined by Manifest's field order and
// Go's encoding/json representation. Entries are sorted by path; the manifest
// and signature are fixed container metadata rather than self-referential entries.
func Write(ctx context.Context, dst io.Writer, id Identity, sources []Source, key ed25519.PrivateKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(key) != ed25519.PrivateKeySize || !validIdentity(id) {
		return errors.New("invalid evidence signing key or identity")
	}
	if len(sources) > maxEntries {
		return errors.New("too many evidence entries")
	}
	pub := key.Public().(ed25519.PublicKey)
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return err
	}
	ordered := append([]Source(nil), sources...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	manifest := Manifest{Schema: Schema, Identity: id, SignatureAlgorithm: "Ed25519", PublicKey: base64.StdEncoding.EncodeToString(der), PublicKeySHA256: digest(der), Entries: make([]Entry, 0, len(ordered))}
	seen := make(map[string]bool, len(ordered))
	for _, source := range ordered {
		if !allowedPath(source.Path) || seen[source.Path] {
			return errors.New("invalid or duplicate evidence entry path")
		}
		seen[source.Path] = true
	}
	if !seen[ReportPath] || !seen[PDFPath] || !seen[ManualFindingsPath] || !seen[OriginalsIndexPath] || !seen[DecisionsPath] {
		return errors.New("evidence report, PDF, manual history or original inventory is missing")
	}
	zw := zip.NewWriter(dst)
	for _, source := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		reader := io.NopCloser(bytes.NewReader(source.Data))
		if source.Open != nil {
			reader, err = source.Open()
			if err != nil {
				return err
			}
		}
		writer, err := zw.Create(source.Path)
		if err != nil {
			reader.Close()
			return err
		}
		hash := sha256.New()
		size, copyErr := io.Copy(io.MultiWriter(writer, hash), &cancelReader{ctx: ctx, reader: reader})
		closeErr := reader.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		manifest.Entries = append(manifest.Entries, Entry{Path: source.Path, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if len(body) > maxManifestBytes {
		return errors.New("evidence manifest is too large")
	}
	if err := writeZipEntry(zw, ManifestPath, body); err != nil {
		return err
	}
	if err := writeZipEntry(zw, SignaturePath, ed25519.Sign(key, body)); err != nil {
		return err
	}
	return zw.Close()
}

type cancelReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *cancelReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func writeZipEntry(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// Verify rejects unexpected, missing, repeated or non-regular entries and never
// extracts a ZIP path. Trust must be explicitly supplied as a public key, a
// trusted SPKI fingerprint, or both. Entry contents are hashed while streaming.
func Verify(r io.ReaderAt, size int64, trustedKey ed25519.PublicKey, trustedFingerprint string) (Manifest, error) {
	var empty Manifest
	if len(trustedKey) == 0 && trustedFingerprint == "" {
		return empty, errors.New("a trusted public key or fingerprint is required")
	}
	if len(trustedKey) != 0 && len(trustedKey) != ed25519.PublicKeySize {
		return empty, errors.New("invalid trusted Ed25519 public key")
	}
	if trustedFingerprint != "" && !validHash(trustedFingerprint) {
		return empty, errors.New("fingerprint must be lowercase SHA-256 of DER SubjectPublicKeyInfo")
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return empty, fmt.Errorf("open ZIP: %w", err)
	}
	if len(zr.File) > maxEntries+2 {
		return empty, errors.New("too many ZIP entries")
	}
	files := make(map[string]*zip.File, len(zr.File))
	for _, file := range zr.File {
		if file.Name != ManifestPath && file.Name != SignaturePath && !allowedPath(file.Name) {
			return empty, errors.New("unexpected ZIP entry")
		}
		if files[file.Name] != nil || !file.Mode().IsRegular() {
			return empty, errors.New("duplicate or non-regular ZIP entry")
		}
		files[file.Name] = file
	}
	manifestBytes, err := readSmallEntry(files[ManifestPath], maxManifestBytes)
	if err != nil {
		return empty, fmt.Errorf("read manifest: %w", err)
	}
	signature, err := readSmallEntry(files[SignaturePath], ed25519.SignatureSize)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return empty, errors.New("invalid manifest signature entry")
	}
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return empty, errors.New("invalid evidence manifest JSON")
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || !bytes.Equal(canonical, manifestBytes) {
		return empty, errors.New("manifest is not canonical JSON")
	}
	if manifest.Schema != Schema || manifest.SignatureAlgorithm != "Ed25519" || !validIdentity(manifest.Identity) {
		return empty, errors.New("unsupported or invalid evidence manifest")
	}
	der, err := base64.StdEncoding.Strict().DecodeString(manifest.PublicKey)
	if err != nil || !validHash(manifest.PublicKeySHA256) || digest(der) != manifest.PublicKeySHA256 {
		return empty, errors.New("invalid manifest public key fingerprint")
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return empty, errors.New("invalid manifest public key")
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok || len(key) != ed25519.PublicKeySize {
		return empty, errors.New("manifest public key is not Ed25519")
	}
	if len(trustedKey) != 0 && !bytes.Equal(trustedKey, key) || trustedFingerprint != "" && trustedFingerprint != manifest.PublicKeySHA256 {
		return empty, errors.New("signing key is not trusted")
	}
	if !ed25519.Verify(key, manifestBytes, signature) {
		return empty, errors.New("manifest signature verification failed")
	}
	if len(manifest.Entries) != len(files)-2 || len(manifest.Entries) > maxEntries {
		return empty, errors.New("ZIP contents do not match the manifest")
	}
	previous := ""
	for _, entry := range manifest.Entries {
		if !allowedPath(entry.Path) || entry.Path <= previous || entry.Size < 0 || entry.Size == int64(^uint64(0)>>1) || !validHash(entry.SHA256) {
			return empty, errors.New("invalid manifest entry")
		}
		previous = entry.Path
		file := files[entry.Path]
		if file == nil || file.UncompressedSize64 != uint64(entry.Size) {
			return empty, errors.New("missing evidence entry or size mismatch")
		}
		reader, err := file.Open()
		if err != nil {
			return empty, err
		}
		hash := sha256.New()
		count, copyErr := io.Copy(hash, io.LimitReader(reader, entry.Size+1))
		closeErr := reader.Close()
		if copyErr != nil || closeErr != nil || count != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			return empty, errors.New("evidence entry integrity verification failed")
		}
	}
	if files[ReportPath] == nil || files[PDFPath] == nil || files[ManualFindingsPath] == nil || files[OriginalsIndexPath] == nil || files[DecisionsPath] == nil {
		return empty, errors.New("required evidence entry is missing")
	}
	if err := verifyContents(manifest, files); err != nil {
		return empty, err
	}
	return manifest, nil
}

func readSmallEntry(file *zip.File, limit int64) ([]byte, error) {
	if file == nil || file.UncompressedSize64 > uint64(limit) {
		return nil, errors.New("entry is missing or too large")
	}
	r, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, errors.New("entry could not be read within its limit")
	}
	return raw, nil
}
