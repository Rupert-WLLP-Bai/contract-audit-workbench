// File metadata (manifest mirror):
// Purpose: Exercises local OCR deployment configuration, complete model-asset verification and engine binding, including rejected manifests and incomplete profiles.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package localocr

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

var mineruRequiredAssets = []string{
	"Layout/PP-DocLayoutV2/inference.onnx",
	"Layout/PP-DocLayoutV2/inference.yml",
	"OCR/paddleocr/ch_PP-OCRv6_tiny_det_infer.onnx",
	"OCR/paddleocr/ch_PP-OCRv6_small_rec_infer.onnx",
}

func mineruAssetRoot(t *testing.T, overrides map[string][]byte) string {
	t.Helper()
	root := t.TempDir()
	for _, relative := range mineruRequiredAssets {
		body := []byte("asset:" + relative)
		if override, ok := overrides[relative]; ok {
			body = override
		}
		name := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func mineruManifest(t *testing.T, root string, adjust func(map[string]any)) string {
	t.Helper()
	files := make([]any, 0, len(mineruRequiredAssets))
	for _, relative := range mineruRequiredAssets {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		files = append(files, map[string]any{"path": relative, "sha256": hex.EncodeToString(sum[:])})
	}
	manifest := map[string]any{"repository": "opendatalab/MinerU-4_models_onnx", "revision": "4.2.2", "files": files}
	if adjust != nil {
		adjust(manifest)
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(name, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func mineruEnvironment(t *testing.T, root, manifest string) {
	t.Helper()
	python := filepath.Join(root, "python")
	if err := os.WriteFile(python, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCUMENT_OCR_ENGINE", "mineru")
	t.Setenv("DOCUMENT_MINERU_URL", "http://127.0.0.1:18080")
	t.Setenv("DOCUMENT_MINERU_PYTHON", python)
	t.Setenv("DOCUMENT_MINERU_MANIFEST", manifest)
	t.Setenv("DOCUMENT_MINERU_ASSET_ROOT", root)
}

func TestFromEnvAcceptsOnlyCompleteVerifiedMineruDeployments(t *testing.T) {
	t.Setenv("DOCUMENT_OCR_ENGINE", "")
	if config, enabled, err := FromEnv(); err != nil || enabled || config != (Config{}) {
		t.Fatalf("unset engine returned %+v %v %v", config, enabled, err)
	}
	t.Setenv("DOCUMENT_OCR_ENGINE", "model")
	if _, enabled, err := FromEnv(); err != nil || enabled {
		t.Fatalf("model engine returned %v %v", enabled, err)
	}
	t.Setenv("DOCUMENT_OCR_ENGINE", "other")
	if _, _, err := FromEnv(); err == nil {
		t.Fatal("unsupported engine was accepted")
	}

	root := mineruAssetRoot(t, nil)
	manifest := mineruManifest(t, root, nil)
	mineruEnvironment(t, root, manifest)
	config, enabled, err := FromEnv()
	if err != nil || !enabled {
		t.Fatalf("verified deployment was rejected: %v", err)
	}
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	wantProfile := "mineru/4.2.2/flash/ocr/full/" + hex.EncodeToString(sum[:])
	if config.Endpoint != "http://127.0.0.1:18080" || config.Python == "" || config.ProfileID != wantProfile {
		t.Fatalf("config is %+v, want profile %q", config, wantProfile)
	}
}

func TestFromEnvRejectsIncompleteOrUnsafeDeployments(t *testing.T) {
	root := mineruAssetRoot(t, nil)
	manifest := mineruManifest(t, root, nil)
	mineruEnvironment(t, root, manifest)

	t.Setenv("DOCUMENT_MINERU_PYTHON", "python3")
	if _, _, err := FromEnv(); err == nil {
		t.Fatal("relative Python path was accepted")
	}
	mineruEnvironment(t, root, manifest)
	t.Setenv("DOCUMENT_MINERU_MANIFEST", "manifest.json")
	if _, _, err := FromEnv(); err == nil {
		t.Fatal("relative manifest path was accepted")
	}
	mineruEnvironment(t, root, manifest)
	t.Setenv("DOCUMENT_MINERU_ASSET_ROOT", "assets")
	if _, _, err := FromEnv(); err == nil {
		t.Fatal("relative asset root was accepted")
	}
	mineruEnvironment(t, root, manifest)
	t.Setenv("DOCUMENT_MINERU_URL", "http://localhost:18080")
	if _, _, err := FromEnv(); err == nil {
		t.Fatal("non-literal loopback endpoint was accepted")
	}

	for name, adjust := range map[string]func(map[string]any){
		"wrong repository": func(manifest map[string]any) { manifest["repository"] = "other/repo" },
		"missing revision": func(manifest map[string]any) { manifest["revision"] = "" },
		"no assets":        func(manifest map[string]any) { manifest["files"] = []any{} },
		"absolute asset": func(manifest map[string]any) {
			manifest["files"].([]any)[0].(map[string]any)["path"] = "/etc/passwd"
		},
		"parent asset": func(manifest map[string]any) {
			manifest["files"].([]any)[0].(map[string]any)["path"] = "../outside.onnx"
		},
		"duplicate asset": func(manifest map[string]any) {
			files := manifest["files"].([]any)
			manifest["files"] = append(files, files[0])
		},
		"invalid digest": func(manifest map[string]any) {
			manifest["files"].([]any)[0].(map[string]any)["sha256"] = "not-a-digest"
		},
		"missing asset": func(manifest map[string]any) {
			manifest["files"] = manifest["files"].([]any)[1:]
		},
		"digest mismatch": func(manifest map[string]any) {
			manifest["files"].([]any)[0].(map[string]any)["sha256"] = hex.EncodeToString(make([]byte, 32))
		},
	} {
		t.Run(name, func(t *testing.T) {
			mineruEnvironment(t, root, mineruManifest(t, root, adjust))
			if _, _, err := FromEnv(); err == nil {
				t.Fatalf("%s manifest was accepted", name)
			}
		})
	}

	mineruEnvironment(t, root, filepath.Join(t.TempDir(), "absent.json"))
	if _, _, err := FromEnv(); err == nil {
		t.Fatal("missing manifest file was accepted")
	}
	mineruEnvironment(t, root, manifest)
	t.Setenv("DOCUMENT_MINERU_URL", "http://127.0.0.1")
	if _, _, err := FromEnv(); err == nil {
		t.Fatal("loopback endpoint without a port was accepted")
	}
	missing := mineruAssetRoot(t, nil)
	if err := os.Remove(filepath.Join(missing, "Layout", "PP-DocLayoutV2", "inference.onnx")); err != nil {
		t.Fatal(err)
	}
	mineruEnvironment(t, missing, mineruManifest(t, mineruAssetRoot(t, nil), nil))
	if _, _, err := FromEnv(); err == nil {
		t.Fatal("missing model asset was accepted")
	}
}

func TestNewEngineBindsOnlyCompleteProfiles(t *testing.T) {
	if _, err := NewEngine(Config{Endpoint: "http://example.com:8000", ProfileID: "profile", Python: "/usr/bin/python"}); err == nil {
		t.Fatal("non-loopback engine was accepted")
	}
	if _, err := NewEngine(Config{Endpoint: "http://127.0.0.1:18080", Python: "/usr/bin/python"}); err == nil {
		t.Fatal("engine without a profile was accepted")
	}
	if _, err := NewEngine(Config{Endpoint: "http://127.0.0.1:18080", ProfileID: "profile"}); err == nil {
		t.Fatal("engine without Python was accepted")
	}
	engine, err := NewEngine(Config{Endpoint: "http://127.0.0.1:18080", ProfileID: "mineru/4.2.2/flash/ocr/full/abc", Python: "/usr/bin/python"})
	if err != nil {
		t.Fatal(err)
	}
	if engine.ProfileID() != "mineru/4.2.2/flash/ocr/full/abc" {
		t.Fatalf("engine profile is %q", engine.ProfileID())
	}
	badArchive, err := engine.Transcribe(t.Context(), "image/png", nil)
	if err == nil || badArchive != "" {
		t.Fatalf("transcription without an image returned %q (%v)", badArchive, err)
	}
}
