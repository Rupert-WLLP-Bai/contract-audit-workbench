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
