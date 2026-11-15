// File metadata (manifest mirror):
// Purpose: Rejects invalid local OCR protocol identity; the externally saved real OCR check lives behind the live build tag so the deterministic suite never skips.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-15
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package localocr

import (
	"archive/zip"
	"bytes"
	"context"
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
