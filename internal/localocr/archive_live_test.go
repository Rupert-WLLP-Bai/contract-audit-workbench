//go:build live

// File metadata (manifest mirror):
// Purpose: Checks an externally saved real MinerU OCR result through the local OCR extractor; opt-in with the live build tag and LOCAL_OCR_TEST_ARCHIVE/LOCAL_OCR_TEST_PYTHON so the deterministic suite never skips.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package localocr

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestExtractSavedMineruFixture(t *testing.T) {
	path, python := os.Getenv("LOCAL_OCR_TEST_ARCHIVE"), os.Getenv("LOCAL_OCR_TEST_PYTHON")
	if path == "" || python == "" {
		t.Fatal("LOCAL_OCR_TEST_ARCHIVE and LOCAL_OCR_TEST_PYTHON are required for the live OCR fixture check")
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
