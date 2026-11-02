// File metadata (manifest mirror):
// Purpose: 实现 `TestOCRSourceComesFromParser` 等具体接口与边界逻辑，供 internal/documentparse 的调用方使用。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package documentparse

import (
	"context"
	"testing"
)

func TestOCRSourceComesFromParser(t *testing.T) {
	if _, ok := OCRSourceFromContext(context.Background()); ok {
		t.Fatal("ordinary context claimed parser provenance")
	}
	for _, tc := range []struct {
		name, kind, part string
		page             int
		parse            func(context.Context, []byte, OCR) (Result, error)
		body             func(*testing.T, string) []byte
	}{
		{"scanned-chinese-table.pdf", "pdf", "", 1, ParsePDF, pdfFixture},
		{"embedded-image.docx", "docx", "word/media/bandwidth.png", 0, ParseDOCX, docxFixture},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.kind == "pdf" {
				pdfTools(t)
			}
			calls := 0
			_, err := tc.parse(context.Background(), tc.body(t, tc.name), func(ctx context.Context, _ string, _ []byte) (string, error) {
				source, ok := OCRSourceFromContext(ctx)
				if !ok || source.Kind != tc.kind || source.Page != tc.page || (tc.part != "" && source.Part != tc.part) {
					t.Fatalf("wrong parser source: %+v", source)
				}
				calls++
				return "合成文本", nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if calls == 0 {
				t.Fatal("parser did not request OCR")
			}
		})
	}
}
