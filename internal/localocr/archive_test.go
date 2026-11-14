// File metadata (manifest mirror):
// Purpose: Rejects invalid local OCR protocol identity and optionally checks an externally saved real OCR result.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package localocr

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestExtractRejectsUnverifiedIdentity(t *testing.T) {
	var body bytes.Buffer
	z := zip.NewWriter(&body)
	file, err := z.Create("middle_json.json")
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.Write([]byte(`{"schema":"docvortex.middle","schema_version":"1.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = Extract(context.Background(), "/nonexistent", body.Bytes()); err == nil {
		t.Fatal("accepted unverified OCR protocol identity")
	}
}

func TestExtractSavedMineruFixture(t *testing.T) {
	path, python := os.Getenv("LOCAL_OCR_TEST_ARCHIVE"), os.Getenv("LOCAL_OCR_TEST_PYTHON")
	if path == "" || python == "" {
		t.Skip("saved MinerU fixture and Python are not configured")
	}
	archive, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text, err := Extract(context.Background(), python, archive)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "2025年12月17日") {
		t.Fatal("full renderer omitted the independently checked date")
	}
}
