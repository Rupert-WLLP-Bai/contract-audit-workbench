// File metadata (manifest mirror):
// Purpose: OCR transcribes image bytes through the application's supplied model boundary. Related `documentparse` operations include OCR, Result.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package documentparse

import (
	"context"

	"contractaudit/internal/docir"
)

// OCR transcribes image bytes through the application's supplied model boundary.
type OCR func(context.Context, string, []byte) (string, error)

// Result is a complete document parse with observed text provenance.
type Result struct {
	IR      docir.IR
	FromOCR bool
}

const PDFVersion = "pdf-poppler-v1"
const DOCXVersion = "ooxml-docx-v2"

// OCRSource identifies a page or embedded image emitted by a trusted parser.
// Only this package can attach it to the callback context.
type OCRSource struct {
	Kind string
	Page int
	Part string
}
type ocrSourceKey struct{}

func OCRSourceFromContext(ctx context.Context) (OCRSource, bool) {
	source, ok := ctx.Value(ocrSourceKey{}).(OCRSource)
	return source, ok
}

func withOCRSource(ctx context.Context, source OCRSource) context.Context {
	return context.WithValue(ctx, ocrSourceKey{}, source)
}
