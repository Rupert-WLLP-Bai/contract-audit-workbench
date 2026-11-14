// Package localocr provides bounded local document OCR without model-gateway usage.
package localocr

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
	Endpoint  string
	Python    string
	ProfileID string
}

var assetDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// FromEnv verifies the deployed manifest and each listed model asset. This is
// deployment evidence, not a cryptographic attestation from the HTTP service.
func FromEnv() (Config, bool, error) {
	switch strings.TrimSpace(os.Getenv("DOCUMENT_OCR_ENGINE")) {
	case "", "model":
		return Config{}, false, nil
	case "mineru":
	default:
		return Config{}, false, errors.New("unsupported document OCR engine")
	}
	endpoint := strings.TrimSpace(os.Getenv("DOCUMENT_MINERU_URL"))
	python := strings.TrimSpace(os.Getenv("DOCUMENT_MINERU_PYTHON"))
	manifestPath := strings.TrimSpace(os.Getenv("DOCUMENT_MINERU_MANIFEST"))
	assetRoot := strings.TrimSpace(os.Getenv("DOCUMENT_MINERU_ASSET_ROOT"))
	if !filepath.IsAbs(python) || !filepath.IsAbs(manifestPath) || !filepath.IsAbs(assetRoot) {
		return Config{}, false, errors.New("local OCR requires absolute Python and manifest paths")
	}
	if _, err := NewClient(endpoint); err != nil {
		return Config{}, false, err
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil || len(data) > 1<<20 {
		return Config{}, false, errors.New("local OCR manifest unavailable")
	}
	var manifest struct {
		Repository string `json:"repository"`
		Revision   string `json:"revision"`
		Assets     []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Repository != "opendatalab/MinerU-4_models_onnx" || manifest.Revision == "" || len(manifest.Assets) == 0 {
		return Config{}, false, errors.New("invalid local OCR manifest")
	}
	required := map[string]bool{
		"Layout/PP-DocLayoutV2/inference.onnx":           false,
		"Layout/PP-DocLayoutV2/inference.yml":            false,
		"OCR/paddleocr/ch_PP-OCRv6_tiny_det_infer.onnx":  false,
		"OCR/paddleocr/ch_PP-OCRv6_small_rec_infer.onnx": false,
	}
	seen := map[string]bool{}
	for _, asset := range manifest.Assets {
		relative := filepath.Clean(asset.Path)
		if filepath.IsAbs(relative) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return Config{}, false, errors.New("invalid local OCR manifest asset")
		}
		if _, ok := required[filepath.ToSlash(relative)]; ok {
			required[filepath.ToSlash(relative)] = true
		}
		path := filepath.Join(assetRoot, relative)
		if seen[path] || !assetDigestPattern.MatchString(asset.SHA256) {
			return Config{}, false, errors.New("invalid local OCR manifest asset")
		}
		seen[path] = true
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return Config{}, false, errors.New("local OCR asset unavailable")
		}
		f, err := os.Open(path)
		if err != nil {
			return Config{}, false, errors.New("local OCR asset unavailable")
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil || hex.EncodeToString(h.Sum(nil)) != asset.SHA256 {
			return Config{}, false, errors.New("local OCR asset digest mismatch")
		}
	}
	for _, present := range required {
		if !present {
			return Config{}, false, errors.New("local OCR manifest is incomplete")
		}
	}
	digest := sha256.Sum256(data)
	return Config{Endpoint: endpoint, Python: python, ProfileID: "mineru/4.2.2/flash/ocr/full/" + hex.EncodeToString(digest[:])}, true, nil
}
