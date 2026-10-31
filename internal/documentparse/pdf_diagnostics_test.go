package documentparse

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPDFDiagnosticClassification only accepts complete observed diagnostics
// and requires each JPX header warning to belong to an explicit J2K retry.
func TestPDFDiagnosticClassification(t *testing.T) {
	const (
		fontWeight = "Syntax Warning: Invalid Font Weight"
		fontType   = "Syntax Warning: Mismatch between font type and embedded font file"
	)
	for _, tc := range []struct {
		name      string
		stderr    string
		untrusted bool
		fatal     bool
	}{
		{name: "no diagnostics"},
		{name: "blank lines", stderr: " \n\t\n"},
		{name: "font weight", stderr: fontWeight + "\n"},
		{name: "repeated font weight", stderr: strings.Repeat(fontWeight+"\n", 6)},
		{name: "embedded font type mismatch", stderr: fontType + "\n", untrusted: true},
		{name: "both corpus warnings", stderr: fontWeight + "\n" + fontType + "\n", untrusted: true},
		{name: "windows line endings", stderr: fontType + "\r\n" + fontWeight + "\r\n", untrusted: true},
		{name: "syntax error", stderr: "Syntax Error: Couldn't read xref table\n", fatal: true},
		{name: "declared jpx codestream", stderr: pdfDiagJPXDeclared + "\n" + pdfDiagJPXHeader + "\n" + pdfDiagJPXRetry + "\n"},
		{name: "jpx without header complaint", stderr: pdfDiagJPXDeclared + "\n" + pdfDiagJPXRetry + "\n"},
		{name: "repeated declared jpx codestream", stderr: strings.Repeat(pdfDiagJPXDeclared+"\n"+pdfDiagJPXHeader+"\n"+pdfDiagJPXRetry+"\n", 27)},
		{name: "jpx declaration alone", stderr: pdfDiagJPXDeclared + "\n", fatal: true},
		{name: "jpx retry alone", stderr: pdfDiagJPXRetry + "\n", fatal: true},
		{name: "jpx header alone", stderr: pdfDiagJPXHeader + "\n", fatal: true},
		{name: "jpx header without retry", stderr: pdfDiagJPXDeclared + "\n" + pdfDiagJPXHeader + "\n", fatal: true},
		{name: "jpx header without declaration", stderr: pdfDiagJPXHeader + "\n" + pdfDiagJPXRetry + "\n", fatal: true},
		{name: "jpx orphan header after retry", stderr: pdfDiagJPXDeclared + "\n" + pdfDiagJPXRetry + "\n" + pdfDiagJPXHeader + "\n", fatal: true},
		{name: "jpx duplicated header", stderr: pdfDiagJPXDeclared + "\n" + pdfDiagJPXHeader + "\n" + pdfDiagJPXHeader + "\n" + pdfDiagJPXRetry + "\n", fatal: true},
		{name: "jpx declaration without observed marker", stderr: strings.TrimSuffix(pdfDiagJPXDeclared, "<0a>") + "\n" + pdfDiagJPXRetry + "\n", fatal: true},
		{name: "known prefix with fatal suffix", stderr: fontType + " Syntax Error: Couldn't read xref table\n", fatal: true},
		{name: "fatal prefix with known suffix", stderr: "Syntax Error: Couldn't read xref table " + fontType + "\n", fatal: true},
		{name: "known line with extra whitespace", stderr: fontWeight + " \n", fatal: true},
		{name: "known line with location prefix", stderr: "Syntax Warning (123): " + fontWeight + "\n", fatal: true},
		{name: "jpx retry with extra error", stderr: pdfDiagJPXDeclared + "\n" + pdfDiagJPXHeader + "\n" + pdfDiagJPXRetry + " Syntax Error: decoder failed\n", fatal: true},
		{name: "jpx decoder failure after retry", stderr: pdfDiagJPXDeclared + "\n" + pdfDiagJPXHeader + "\n" + pdfDiagJPXRetry + "\nSyntax Error: Did no succeed opening JPX Stream.\n", fatal: true},
		{name: "syntax alert", stderr: "Syntax Alert: something the renderer could not read\n", fatal: true},
		{name: "command line error", stderr: "Command Line Error: bad option\n", fatal: true},
		{name: "io error", stderr: "I/O Error: Couldn't open file\n", fatal: true},
		{name: "tolerated plus fatal", stderr: fontType + "\nSyntax Error: Couldn't read xref table\n", fatal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			untrusted, err := pdfClassifyDiagnostics(tc.stderr)
			if tc.fatal {
				if err == nil || !strings.Contains(err.Error(), "malformed or unsupported content") {
					t.Fatalf("diagnostic %q must stay fatal: %v", tc.stderr, err)
				}
				if untrusted {
					t.Fatalf("fatal diagnostic %q reported untrusted text", tc.stderr)
				}
				return
			}
			if err != nil {
				t.Fatalf("diagnostic %q must be tolerated: %v", tc.stderr, err)
			}
			if untrusted != tc.untrusted {
				t.Fatalf("diagnostic %q untrusted=%v; want %v", tc.stderr, untrusted, tc.untrusted)
			}
		})
	}
}

// TestPDFDiagnosticMessageIsBounded keeps one hostile diagnostic line from
// travelling unbounded in a stored parse error.
func TestPDFDiagnosticMessageIsBounded(t *testing.T) {
	_, err := pdfClassifyDiagnostics(strings.Repeat("x", 4096))
	if err == nil {
		t.Fatal("unknown diagnostic must fail")
	}
	if len(err.Error()) > 260 || !strings.Contains(err.Error(), "malformed or unsupported content") {
		t.Fatalf("diagnostic message is not bounded: len=%d", len(err.Error()))
	}
	if strings.ToValidUTF8(err.Error(), "") != err.Error() {
		t.Fatal("diagnostic message is not valid UTF-8")
	}
}

// pdfDiagnosticStub writes a script that copies caller-provided stdout and
// stderr and always exits zero, so tolerance is decided by the diagnostics and
// never by the exit code.
func pdfDiagnosticStub(t *testing.T, stdout, stderr string) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "stdout.txt")
	errFile := filepath.Join(dir, "stderr.txt")
	if err := os.WriteFile(out, []byte(stdout), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(errFile, []byte(stderr), 0600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "poppler-stub")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat \"$1\"\ncat \"$2\" >&2\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return script, []string{out, errFile}
}

// TestPDFRunClassifiesToolDiagnostics proves the classification is applied to a
// real child process result, not only to a parsed string.
func TestPDFRunClassifiesToolDiagnostics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub is POSIX only")
	}
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		stderr    string
		untrusted bool
		fatal     bool
	}{
		{name: "font weight", stderr: "Syntax Warning: Invalid Font Weight\n"},
		{name: "embedded font mismatch", stderr: "Syntax Warning: Mismatch between font type and embedded font file\n", untrusted: true},
		{name: "syntax error", stderr: "Syntax Error: Couldn't read xref table\n", fatal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script, args := pdfDiagnosticStub(t, "payload", tc.stderr)
			out, untrusted, err := pdfRun(ctx, 1<<10, script, args...)
			if tc.fatal {
				if err == nil || !strings.Contains(err.Error(), "malformed or unsupported content") || out != nil {
					t.Fatalf("fatal diagnostic: out=%q err=%v", out, err)
				}
				return
			}
			if err != nil || string(out) != "payload" || untrusted != tc.untrusted {
				t.Fatalf("tolerated diagnostic: out=%q untrusted=%v err=%v", out, untrusted, err)
			}
		})
	}
}

// TestPDFFontMismatchTranscribesRenderedPages covers the observed corpus shape:
// Poppler repairs an embedded font mismatch, exits zero, and leaves a text
// layer that must not be presented as verified extraction.
func TestPDFFontMismatchTranscribesRenderedPages(t *testing.T) {
	pdfTools(t)
	body := pdfFixture(t, "font-mismatch.pdf")
	result, err := ParsePDF(context.Background(), body, func(context.Context, string, []byte) (string, error) {
		return "字体映射不可靠，按整页渲染转写。", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.FromOCR || len(result.IR.Pages) != 2 {
		t.Fatalf("font mismatch must be transcribed from renders: OCR=%v pages=%#v", result.FromOCR, result.IR.Pages)
	}
	if got := strings.Count(result.IR.NormalizedText, "字体映射不可靠"); got != 2 {
		t.Fatalf("want one render transcript per page, got %d: %q", got, result.IR.NormalizedText)
	}
	if strings.Contains(result.IR.NormalizedText, "合同") {
		t.Fatalf("untrusted native text leaked into the result: %q", result.IR.NormalizedText)
	}

	if _, err := ParsePDF(context.Background(), body, nil); err == nil || !strings.Contains(err.Error(), "OCR callback") {
		t.Fatalf("untrusted native text without OCR must refuse: %v", err)
	}
}

// TestPDFMixedRawJ2KRequiresACompleteRender exercises both a valid raw J2K
// fallback and a damaged image on a page that also contains native text.
func TestPDFMixedRawJ2KRequiresACompleteRender(t *testing.T) {
	pdfTools(t)
	body := pdfFixture(t, "declared-jpx-native-text.pdf")
	calls := 0
	result, err := ParsePDF(context.Background(), body, func(_ context.Context, mediaType string, body []byte) (string, error) {
		calls++
		if mediaType != "image/png" {
			t.Fatalf("OCR media type = %q", mediaType)
		}
		img, err := png.Decode(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("decode page render: %v", err)
		}
		bounds := img.Bounds()
		red := false
		for y := bounds.Min.Y; y < bounds.Max.Y && !red; y += 10 {
			for x := bounds.Min.X; x < bounds.Max.X; x += 10 {
				r, g, b, _ := img.At(x, y).RGBA()
				if r > 0x8000 && r > 2*g && r > 2*b {
					red = true
					break
				}
			}
		}
		if !red {
			t.Fatal("decoded page lost the red embedded image")
		}
		return "rendered image transcript", nil
	})
	if err != nil || calls != 1 || !result.FromOCR {
		t.Fatalf("valid J2K fallback: calls=%d fromOCR=%v err=%v", calls, result.FromOCR, err)
	}
	if !strings.Contains(result.IR.NormalizedText, "Native clause: warranty 36 months.") || !strings.Contains(result.IR.NormalizedText, "rendered image transcript") {
		t.Fatalf("valid mixed page lost text or transcript: %q", result.IR.NormalizedText)
	}

	// Keep all offsets and the image declaration intact, but corrupt the raw
	// codestream after its header. Poppler reports additional decoder errors;
	// native text must never make this damaged page a successful parse.
	start := bytes.Index(body, []byte{0xff, 0x4f, 0xff, 0x51})
	if start < 0 {
		t.Fatal("fixture has no raw J2K codestream")
	}
	end := bytes.Index(body[start:], []byte("\nendstream"))
	if end < 61 {
		t.Fatal("fixture has an incomplete image stream")
	}
	clear(body[start+60 : start+end])
	calls = 0
	result, err = ParsePDF(context.Background(), body, func(context.Context, string, []byte) (string, error) {
		calls++
		return "native text alone", nil
	})
	if err == nil || !strings.Contains(err.Error(), "malformed or unsupported content") || result.IR.NormalizedText != "" || result.FromOCR || calls != 0 {
		t.Fatalf("damaged image returned a partial parse: calls=%d fromOCR=%v err=%v text=%q", calls, result.FromOCR, err, result.IR.NormalizedText)
	}
}
