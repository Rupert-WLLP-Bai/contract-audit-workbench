package documentparse

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPDFDiagnosticClassification keeps the Poppler refusal explicit for every
// diagnostic except the enumerated font-metadata warnings.
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
		{name: "declared jpx codestream", stderr: pdfDiagJPXDeclared + "\n" + pdfDiagJPXRetry + "\n" + pdfDiagJPXHeader + "\n"},
		{name: "repeated declared jpx codestream", stderr: strings.Repeat(pdfDiagJPXDeclared+"\n"+pdfDiagJPXRetry+"\n"+pdfDiagJPXHeader+"\n", 27)},
		{name: "declared jpx codestream with poppler newline marker", stderr: pdfDiagJPXDeclared + "<0a>\n" + pdfDiagJPXRetry + "\n" + pdfDiagJPXHeader + "\n"},
		{name: "jpx header complaint without declaration", stderr: pdfDiagJPXHeader + "\n", fatal: true},
		{name: "jpx header complaint after other damage", stderr: "Syntax Error: Couldn't read xref table\n" + pdfDiagJPXHeader + "\n", fatal: true},
		{name: "syntax alert", stderr: "Syntax Alert: something the renderer could not read\n", fatal: true},
		{name: "command line error", stderr: "Command Line Error: Incorrect password\n", fatal: true},
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
