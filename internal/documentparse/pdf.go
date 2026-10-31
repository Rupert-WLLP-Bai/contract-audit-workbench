package documentparse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"contractaudit/internal/docir"
)

const (
	pdfMaxBytes = 32 << 20
	// pdfMaxPages admits the frozen 92-page public scan (issue #15) and still
	// rejects unbounded documents.
	pdfMaxPages = 128
	// pdfMaxAllPixels keeps the historical per-page image allowance (600M for 64
	// pages) and follows the page ceiling, so the budget stays at 9,375,000
	// embedded pixels per page. The frozen 92-page scan measures 633,032,192
	// embedded pixels, while its 92 renders at 2400 points total 374,918,400.
	pdfMaxAllPixels   = 1_200_000_000
	pdfMaxTextBytes   = 8 << 20
	pdfMaxPageText    = 1 << 20
	pdfMaxPNGBytes    = 24 << 20
	pdfRenderSide     = 2400
	pdfMaxImagePixels = 40_000_000
	pdfMaxImages      = 4096
	pdfToolTimeout    = 30 * time.Second
)

var pdfPageSize = regexp.MustCompile(`^Page\s+([0-9]+)\s+size:\s+([0-9.eE+-]+) x ([0-9.eE+-]+) pts(?:\s|$)`)

// ParsePDF extracts native text and transcribes every image-bearing or textless
// page using a real complete-page rendering. Page numbers come from Poppler;
// the adapter never estimates geometric coordinates or table cell boundaries.
// Mixed pages keep native extraction followed by the complete-page transcript:
// removing similar-looking OCR text could discard different image evidence.
func ParsePDF(ctx context.Context, body []byte, ocr OCR) (Result, error) {
	if err := pdfValidateInput(ctx, body); err != nil {
		return Result{}, err
	}
	dir, err := pdfWorkspace(body)
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "original.pdf")
	pages, err := pdfInspectPages(ctx, path)
	if err != nil {
		return Result{}, err
	}
	images, err := pdfImageInventory(ctx, path, pages)
	if err != nil {
		return Result{}, err
	}
	segments, err := pdfTextSegments(ctx, path, pages)
	if err != nil {
		return Result{}, err
	}
	pageTexts, fromOCR, err := pdfPageTexts(ctx, path, pages, segments, images, ocr)
	if err != nil {
		return Result{}, err
	}
	ir := docir.Build(strings.Join(pageTexts, "\f") + "\f")
	ir.ExtractionVersion = PDFVersion
	return Result{IR: ir, FromOCR: fromOCR}, nil
}

// pdfValidateInput rejects canceled calls, oversized bodies and bodies that do
// not carry the PDF header and end marker.
func pdfValidateInput(ctx context.Context, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(body) > pdfMaxBytes {
		return fmt.Errorf("PDF exceeds %d-byte input limit", pdfMaxBytes)
	}
	if !bytes.HasPrefix(body, []byte("%PDF-")) || !bytes.Contains(body[len(body)-min(len(body), 1024):], []byte("%%EOF")) {
		return errors.New("PDF header or end marker is missing")
	}
	return nil
}

// pdfWorkspace writes the original body into a private temporary directory.
// A failed write removes the directory before returning.
func pdfWorkspace(body []byte) (string, error) {
	dir, err := os.MkdirTemp("", "contractaudit-pdf-")
	if err != nil {
		return "", fmt.Errorf("creating PDF workspace: %w", err)
	}
	path := filepath.Join(dir, "original.pdf")
	if err := os.WriteFile(path, body, 0600); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("writing PDF workspace: %w", err)
	}
	return dir, nil
}

// pdfInspectPages reads the page count and validates every page geometry.
func pdfInspectPages(ctx context.Context, path string) (int, error) {
	var pages int
	info, err := pdfCommand(ctx, 128<<10, "pdfinfo", path)
	if err != nil {
		return pages, err
	}
	pages, err = pdfPageCount(string(info))
	if err != nil {
		return pages, err
	}
	pageInfo, err := pdfCommand(ctx, 128<<10, "pdfinfo", "-f", "1", "-l", strconv.Itoa(pages), path)
	if err != nil {
		return pages, err
	}
	if err := pdfCheckPageSizes(string(pageInfo), pages); err != nil {
		return pages, err
	}
	return pages, nil
}

// pdfImageInventory reads the embedded-image inventory.
func pdfImageInventory(ctx context.Context, path string, pages int) ([]bool, error) {
	imageInfo, err := pdfCommand(ctx, 1<<20, "pdfimages", "-list", path)
	if err != nil {
		return nil, err
	}
	return pdfImagePages(string(imageInfo), pages)
}

// pdfTextSegments extracts native text and validates the page boundaries.
func pdfTextSegments(ctx context.Context, path string, pages int) ([]string, error) {
	text, untrusted, err := pdfRun(ctx, pdfMaxTextBytes, "pdftotext", "-layout", "-enc", "UTF-8", "-eol", "unix", path, "-")
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(text) {
		return nil, errors.New("PDF extraction returned invalid UTF-8")
	}
	segments := strings.Split(string(text), "\f")
	if len(segments) != pages+1 {
		return nil, errors.New("PDF text page boundaries do not match observed page count")
	}
	if strings.TrimSpace(segments[len(segments)-1]) != "" {
		return nil, errors.New("PDF text page boundaries do not match observed page count")
	}
	if untrusted {
		// Poppler read a font program that does not match its declared type, so
		// its glyph mapping is not reliable evidence for the visible page. Keep
		// the validated page boundaries and drop the text, exactly as for broken
		// font encodings, so each page is transcribed from its rendering.
		for i := range segments {
			segments[i] = ""
		}
	}
	return segments, nil
}

// pdfPageTexts builds one page text per observed page, transcribing pages that
// carry images or have no readable native text.
func pdfPageTexts(ctx context.Context, path string, pages int, segments []string, images []bool, ocr OCR) ([]string, bool, error) {
	pageTexts := make([]string, pages)
	fromOCR := false
	var total int
	for i := range pageTexts {
		if err := ctx.Err(); err != nil {
			return nil, fromOCR, err
		}
		native := strings.TrimSpace(segments[i])
		if pdfLimitExceeded(len(native), pdfMaxPageText) {
			return nil, fromOCR, fmt.Errorf("PDF page %d exceeds text limit", i+1)
		}
		native, usedOCR, err := pdfNativeOrTranscript(ctx, path, i, native, images[i], ocr)
		if err != nil {
			return nil, fromOCR, err
		}
		if usedOCR {
			fromOCR = true
		}
		total += len(native)
		if pdfLimitExceeded(total, pdfMaxTextBytes) {
			return nil, fromOCR, errors.New("PDF complete text exceeds output limit")
		}
		pageTexts[i] = native
	}
	return pageTexts, fromOCR, nil
}

// pdfNativeOrTranscript keeps readable native text and appends a complete-page
// transcript when the page carries an image.
func pdfNativeOrTranscript(ctx context.Context, path string, index int, native string, hasImage bool, ocr OCR) (nativeText string, usedOCR bool, err error) {
	page := index + 1
	readable := pdfReadableText(native)
	if !readable {
		// Broken font encodings must be transcribed from visible content.
		native = ""
	}
	if !hasImage && readable {
		return native, false, nil
	}
	if ocr == nil {
		return "", usedOCR, fmt.Errorf("PDF page %d requires an OCR callback", page)
	}
	transcript, err := pdfOCRPage(ctx, path, page, ocr)
	if err != nil {
		return "", usedOCR, err
	}
	if native == "" {
		native = transcript
	} else {
		native += "\n\n" + transcript
	}
	return native, true, nil
}

// pdfOCRPage renders one complete page and validates its transcript.
func pdfOCRPage(ctx context.Context, path string, page int, ocr OCR) (string, error) {
	pngBody, err := pdfRenderPage(ctx, path, page)
	if err != nil {
		return "", err
	}
	transcript, err := ocr(ctx, "image/png", pngBody)
	if err != nil {
		return "", fmt.Errorf("transcribing PDF page %d: %w", page, err)
	}
	return pdfValidateTranscript(ctx, page, transcript)
}

// pdfRenderPage renders one page as a bounded PNG.
func pdfRenderPage(ctx context.Context, path string, page int) ([]byte, error) {
	pngBody, err := pdfCommand(ctx, pdfMaxPNGBytes, "pdftoppm", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-singlefile", "-scale-to", strconv.Itoa(pdfRenderSide), "-png", path)
	if err != nil {
		return nil, fmt.Errorf("rendering PDF page %d: %w", page, err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(pngBody))
	if !pdfRenderValid(err, config.Width, config.Height) {
		return nil, fmt.Errorf("PDF page %d has an invalid or excessive render", page)
	}
	return pngBody, nil
}

// pdfRenderValid accepts only a decoded render inside the configured square.
func pdfRenderValid(err error, width, height int) bool {
	return err == nil && width >= 1 && height >= 1 && width <= pdfRenderSide && height <= pdfRenderSide
}

// pdfValidateTranscript rejects empty, unreadable, excessive or page-breaking
// transcripts.
func pdfValidateTranscript(ctx context.Context, page int, transcript string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	transcript = strings.TrimSpace(transcript)
	if pdfLimitExceeded(len(transcript), pdfMaxPageText) || !pdfReadableText(transcript) || strings.ContainsRune(transcript, '\f') {
		return "", fmt.Errorf("PDF page %d OCR returned empty, invalid, or excessive text", page)
	}
	return transcript, nil
}

// pdfLimitExceeded reports whether a size exceeds its configured limit.
func pdfLimitExceeded(size, limit int) bool {
	return size > limit
}

func pdfReadableText(text string) bool {
	if strings.TrimSpace(text) == "" || !utf8.ValidString(text) {
		return false
	}
	for _, r := range text {
		if r == utf8.RuneError || (unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t') {
			return false
		}
	}
	return true
}

// pdfPageCount validates the metadata fields and the observed page count.
func pdfPageCount(info string) (int, error) {
	fields, err := pdfMetadataFields(info)
	if err != nil {
		return 0, err
	}
	if err := pdfValidateMetadata(fields); err != nil {
		return 0, err
	}
	// strconv.Atoi reports a zero or clamped value on failure, both outside the
	// accepted range, so the range check covers parse failures too.
	pages, _ := strconv.Atoi(fields["Pages"])
	if pages < 1 || pages > pdfMaxPages {
		return 0, fmt.Errorf("PDF page count must be between 1 and %d", pdfMaxPages)
	}
	return pages, nil
}

// pdfMetadataFields collects the required metadata fields. A repeated required
// field is ambiguous and rejected.
func pdfMetadataFields(info string) (map[string]string, error) {
	fields := make(map[string]string, 4)
	for _, line := range strings.Split(info, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || (key != "Pages" && key != "Encrypted" && key != "Form" && key != "JavaScript") {
			continue
		}
		if _, exists := fields[key]; exists {
			return nil, errors.New("PDF metadata has ambiguous required fields")
		}
		fields[key] = strings.TrimSpace(value)
	}
	return fields, nil
}

// pdfValidateMetadata rejects encrypted, interactive or scripted documents.
func pdfValidateMetadata(fields map[string]string) error {
	if fields["Encrypted"] != "no" {
		return errors.New("encrypted PDFs are unsupported")
	}
	if fields["Form"] != "none" || fields["JavaScript"] != "no" {
		return errors.New("PDF forms or JavaScript require a flattened document")
	}
	return nil
}

// pdfCheckPageSizes requires one valid geometry per observed page.
func pdfCheckPageSizes(info string, pages int) error {
	seen := make([]bool, pages)
	for _, line := range strings.Split(info, "\n") {
		match := pdfPageSize.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		page, valid := pdfParsePageSize(match)
		if !valid || page < 1 || page > pages || seen[page-1] {
			return errors.New("PDF page geometry is invalid or exceeds 14400-point limit")
		}
		seen[page-1] = true
	}
	for _, ok := range seen {
		if !ok {
			return errors.New("PDF tool did not report every page size")
		}
	}
	return nil
}

// pdfParsePageSize parses one geometry line and rejects unparsable or
// out-of-range dimensions.
func pdfParsePageSize(match []string) (int, bool) {
	page, _ := strconv.Atoi(match[1])
	// ParseFloat yields zero for syntax errors and an infinity for range
	// errors; both fail the dimension check below.
	width, _ := strconv.ParseFloat(match[2], 64)
	height, _ := strconv.ParseFloat(match[3], 64)
	if !pdfValidPageDimensions(width, height) {
		return page, false
	}
	return page, true
}

// pdfValidPageDimensions checks the positive 14400-point geometry. ParseFloat
// turns syntax errors into zero and range errors into an infinity, so the range
// checks also reject values that could not be parsed.
func pdfValidPageDimensions(width, height float64) bool {
	if width <= 0 || height <= 0 || width > 14400 || height > 14400 {
		return false
	}
	return true
}

// pdfImagePages validates the image inventory header, every row and the total
// pixel budget.
func pdfImagePages(info string, pages int) ([]bool, error) {
	out := make([]bool, pages)
	lines, err := pdfImageInventoryLines(info)
	if err != nil {
		return nil, err
	}
	var allPixels int64
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return nil, errors.New("PDF image inventory is incomplete")
		}
		page, width, height, ok := pdfImageFields(fields, pages)
		if !ok {
			return nil, errors.New("PDF embedded image exceeds pixel limit or has invalid dimensions")
		}
		allPixels += width * height
		if allPixels > pdfMaxAllPixels {
			return nil, errors.New("PDF embedded images exceed total pixel limit")
		}
		out[page-1] = true
	}
	return out, nil
}

// pdfImageInventoryLines validates the inventory header and the row limit.
func pdfImageInventoryLines(info string) ([]string, error) {
	lines := strings.Split(strings.TrimSpace(info), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "page") || !strings.HasPrefix(lines[1], "---") {
		return nil, errors.New("PDF image inventory is invalid")
	}
	if len(lines)-2 > pdfMaxImages {
		return nil, errors.New("PDF image count exceeds resource limit")
	}
	return lines[2:], nil
}

// pdfImageFields parses and validates one image row. The strconv calls yield
// zero on syntax errors and a clamped value on range errors; the page and pixel
// checks reject both, so the parsed values are returned with ok=false.
func pdfImageFields(fields []string, pages int) (page int, width, height int64, ok bool) {
	page, _ = strconv.Atoi(fields[0])
	width, _ = strconv.ParseInt(fields[3], 10, 64)
	height, _ = strconv.ParseInt(fields[4], 10, 64)
	if page < 1 || page > pages {
		return
	}
	if !pdfImageDimensionsValid(width, height) {
		return
	}
	return page, width, height, true
}

// pdfImageDimensionsValid rejects non-positive dimensions and a pixel product
// above pdfMaxImagePixels.
func pdfImageDimensionsValid(width, height int64) bool {
	if width < 1 || height < 1 || width > pdfMaxImagePixels/height {
		return false
	}
	return true
}

// pdfBoundedOutput stops reading hostile tool output before it can grow memory
// without limit. CommandContext bounds even a child that ignores a closed pipe.
// Keep the buffer named: promoting bytes.Buffer.ReadFrom bypasses Write limits.
type pdfBoundedOutput struct {
	buffer bytes.Buffer
	limit  int
	full   bool
}

func (b *pdfBoundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.buffer.Len() {
		b.full = true
		return 0, errors.New("PDF tool output exceeds resource limit")
	}
	return b.buffer.Write(p)
}

// Poppler diagnostics observed on the frozen public originals. Every other line
// stays an explicit failure: only these four describe damage Poppler repaired
// without dropping output, because the callers still validate the page count,
// page geometry, image rows, text page boundaries and the decoded render.
const (
	pdfDiagFontWeight = "Syntax Warning: Invalid Font Weight"
	pdfDiagFontType   = "Syntax Warning: Mismatch between font type and embedded font file"
	// A raw J2K codestream declared as JPX: Poppler retries it as J2K, and every
	// observed page still rendered its scanned image.
	pdfDiagJPXDeclared = "Syntax Error: Malformed JP2 file format: first box must be JPEG 2000 signature box"
	pdfDiagJPXRetry    = "Syntax Warning: Did no succeed opening JPX Stream as JP2, trying as J2K."
	// Poppler prints this header complaint as part of the same JP2 attempt, so it
	// is accepted only after the declared-JPX line, never on its own.
	pdfDiagJPXHeader = "Syntax Warning: Unable to read header"
)

// pdfToleratedDiagnostics are the diagnostics that never fail a call on their
// own. The embedded font type mismatch also leaves the extracted glyph mapping
// unreliable, which pdfClassifyDiagnostics reports separately.
var pdfToleratedDiagnostics = []string{
	pdfDiagFontWeight,
	pdfDiagFontType,
	pdfDiagJPXDeclared,
	pdfDiagJPXRetry,
}

// pdfUnreliableTextDiagnostics mark diagnostics that leave the document
// renderable but make Poppler's glyph mapping unreliable evidence.
var pdfUnreliableTextDiagnostics = []string{
	pdfDiagFontType,
}

// pdfCommand runs one Poppler tool and reports the first untolerated diagnostic.
func pdfCommand(ctx context.Context, limit int, tool string, args ...string) ([]byte, error) {
	out, _, err := pdfRun(ctx, limit, tool, args...)
	return out, err
}

// pdfRun executes one Poppler tool and classifies its diagnostics. Poppler can
// repair damaged files and still exit zero, so every diagnostic line stays an
// explicit failure unless it is a tolerated font-metadata warning. The flag
// reports whether those warnings make extracted native text unreliable.
func pdfRun(ctx context.Context, limit int, tool string, args ...string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	toolCtx, cancel := context.WithTimeout(ctx, pdfToolTimeout)
	defer cancel()
	cmd := exec.CommandContext(toolCtx, tool, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	cmd.WaitDelay = time.Second
	stdout := &pdfBoundedOutput{limit: limit}
	stderr := &pdfBoundedOutput{limit: 16 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	if toolCtx.Err() != nil {
		return nil, false, fmt.Errorf("PDF tool %s: %w", tool, toolCtx.Err())
	}
	if stdout.full || stderr.full {
		return nil, false, fmt.Errorf("PDF tool %s output exceeds resource limit", tool)
	}
	if err != nil {
		return nil, false, fmt.Errorf("PDF tool %s failed: %w", tool, err)
	}
	untrustedText, err := pdfClassifyDiagnostics(stderr.buffer.String())
	if err != nil {
		return nil, false, fmt.Errorf("PDF tool %s %w", tool, err)
	}
	return stdout.buffer.Bytes(), untrustedText, nil
}

// pdfClassifyDiagnostics accepts only the enumerated diagnostics and reports
// whether they leave native text unreliable. Any other line keeps the documented
// refusal, because it can mean content Poppler could not read. The render-level
// checks stay the backstop: an image Poppler could not draw leaves the page
// without a transcript, which fails instead of returning a partial document.
func pdfClassifyDiagnostics(stderr string) (bool, error) {
	untrustedText := false
	jpxFallback := strings.Contains(stderr, pdfDiagJPXDeclared)
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if jpxFallback && strings.Contains(line, pdfDiagJPXHeader) {
			continue
		}
		if !pdfContainsDiagnostic(pdfToleratedDiagnostics, line) {
			return false, fmt.Errorf("reported malformed or unsupported content: %s", pdfDiagnosticLine(line))
		}
		if pdfContainsDiagnostic(pdfUnreliableTextDiagnostics, line) {
			untrustedText = true
		}
	}
	return untrustedText, nil
}

// pdfContainsDiagnostic reports whether a diagnostic line carries one of the
// expected messages. Poppler sometimes prefixes a message with a location.
func pdfContainsDiagnostic(messages []string, line string) bool {
	for _, message := range messages {
		if strings.Contains(line, message) {
			return true
		}
	}
	return false
}

// pdfDiagnosticLine bounds one diagnostic so it can travel in an error without
// carrying arbitrary tool output.
func pdfDiagnosticLine(line string) string {
	if len(line) > 200 {
		line = line[:200]
	}
	return strings.ToValidUTF8(line, "")
}
