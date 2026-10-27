package documentparse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"math"
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
	pdfMaxBytes       = 32 << 20
	pdfMaxPages       = 64
	pdfMaxTextBytes   = 8 << 20
	pdfMaxPageText    = 1 << 20
	pdfMaxPNGBytes    = 24 << 20
	pdfRenderSide     = 2400
	pdfMaxImagePixels = 40_000_000
	pdfMaxAllPixels   = 600_000_000
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
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if len(body) > pdfMaxBytes {
		return Result{}, fmt.Errorf("PDF exceeds %d-byte input limit", pdfMaxBytes)
	}
	if !bytes.HasPrefix(body, []byte("%PDF-")) || !bytes.Contains(body[max(0, len(body)-1024):], []byte("%%EOF")) {
		return Result{}, errors.New("PDF header or end marker is missing")
	}
	dir, err := os.MkdirTemp("", "contractaudit-pdf-")
	if err != nil {
		return Result{}, fmt.Errorf("creating PDF workspace: %w", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "original.pdf")
	if err := os.WriteFile(path, body, 0600); err != nil {
		return Result{}, fmt.Errorf("writing PDF workspace: %w", err)
	}
	info, err := pdfCommand(ctx, 128<<10, "pdfinfo", path)
	if err != nil {
		return Result{}, err
	}
	pages, err := pdfPageCount(string(info))
	if err != nil {
		return Result{}, err
	}
	pageInfo, err := pdfCommand(ctx, 128<<10, "pdfinfo", "-f", "1", "-l", strconv.Itoa(pages), path)
	if err != nil {
		return Result{}, err
	}
	if err := pdfCheckPageSizes(string(pageInfo), pages); err != nil {
		return Result{}, err
	}
	imageInfo, err := pdfCommand(ctx, 1<<20, "pdfimages", "-list", path)
	if err != nil {
		return Result{}, err
	}
	images, err := pdfImagePages(string(imageInfo), pages)
	if err != nil {
		return Result{}, err
	}
	text, err := pdfCommand(ctx, pdfMaxTextBytes, "pdftotext", "-layout", "-enc", "UTF-8", "-eol", "unix", path, "-")
	if err != nil {
		return Result{}, err
	}
	if !utf8.Valid(text) {
		return Result{}, errors.New("PDF extraction returned invalid UTF-8")
	}
	segments := strings.Split(string(text), "\f")
	if len(segments) != pages+1 || strings.TrimSpace(segments[len(segments)-1]) != "" {
		return Result{}, errors.New("PDF text page boundaries do not match observed page count")
	}
	pageTexts := make([]string, pages)
	fromOCR, total := false, 0
	for i := range pageTexts {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		native := strings.TrimSpace(segments[i])
		if len(native) > pdfMaxPageText {
			return Result{}, fmt.Errorf("PDF page %d exceeds text limit", i+1)
		}
		readable := pdfReadableText(native)
		if !readable && native != "" {
			// Broken font encodings must be transcribed from visible content.
			native = ""
		}
		if images[i] || !readable {
			if ocr == nil {
				return Result{}, fmt.Errorf("PDF page %d requires an OCR callback", i+1)
			}
			pngBody, err := pdfCommand(ctx, pdfMaxPNGBytes, "pdftoppm", "-f", strconv.Itoa(i+1), "-l", strconv.Itoa(i+1), "-singlefile", "-scale-to", strconv.Itoa(pdfRenderSide), "-png", path)
			if err != nil {
				return Result{}, fmt.Errorf("rendering PDF page %d: %w", i+1, err)
			}
			config, err := png.DecodeConfig(bytes.NewReader(pngBody))
			if err != nil || config.Width < 1 || config.Height < 1 || config.Width > pdfRenderSide || config.Height > pdfRenderSide {
				return Result{}, fmt.Errorf("PDF page %d has an invalid or excessive render", i+1)
			}
			transcript, err := ocr(ctx, "image/png", pngBody)
			if err != nil {
				return Result{}, fmt.Errorf("transcribing PDF page %d: %w", i+1, err)
			}
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			transcript = strings.TrimSpace(transcript)
			if len(transcript) > pdfMaxPageText || !pdfReadableText(transcript) || strings.ContainsRune(transcript, '\f') {
				return Result{}, fmt.Errorf("PDF page %d OCR returned empty, invalid, or excessive text", i+1)
			}
			if native == "" {
				native = transcript
			} else {
				native += "\n\n" + transcript
			}
			fromOCR = true
		}
		total += len(native)
		if total > pdfMaxTextBytes {
			return Result{}, errors.New("PDF complete text exceeds output limit")
		}
		pageTexts[i] = native
	}
	// The terminal observed break also gives a one-page PDF truthful page 1.
	ir := docir.Build(strings.Join(pageTexts, "\f") + "\f")
	ir.ExtractionVersion = PDFVersion
	return Result{IR: ir, FromOCR: fromOCR}, nil
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

func pdfPageCount(info string) (int, error) {
	fields := make(map[string]string, 4)
	for _, line := range strings.Split(info, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || (key != "Pages" && key != "Encrypted" && key != "Form" && key != "JavaScript") {
			continue
		}
		if _, exists := fields[key]; exists {
			return 0, errors.New("PDF metadata has ambiguous required fields")
		}
		fields[key] = strings.TrimSpace(value)
	}
	if fields["Encrypted"] != "no" {
		return 0, errors.New("encrypted PDFs are unsupported")
	}
	if fields["Form"] != "none" || fields["JavaScript"] != "no" {
		return 0, errors.New("PDF forms or JavaScript require a flattened document")
	}
	pages, err := strconv.Atoi(fields["Pages"])
	if err != nil || pages < 1 || pages > pdfMaxPages {
		return 0, fmt.Errorf("PDF page count must be between 1 and %d", pdfMaxPages)
	}
	return pages, nil
}

func pdfCheckPageSizes(info string, pages int) error {
	seen := make([]bool, pages)
	for _, line := range strings.Split(info, "\n") {
		match := pdfPageSize.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		page, _ := strconv.Atoi(match[1])
		width, e1 := strconv.ParseFloat(match[2], 64)
		height, e2 := strconv.ParseFloat(match[3], 64)
		if page < 1 || page > pages || seen[page-1] || e1 != nil || e2 != nil || math.IsInf(width, 0) || math.IsInf(height, 0) || math.IsNaN(width) || math.IsNaN(height) || width <= 0 || height <= 0 || width > 14400 || height > 14400 {
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

func pdfImagePages(info string, pages int) ([]bool, error) {
	out := make([]bool, pages)
	lines := strings.Split(strings.TrimSpace(info), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "page") || !strings.HasPrefix(lines[1], "---") {
		return nil, errors.New("PDF image inventory is invalid")
	}
	if len(lines)-2 > pdfMaxImages {
		return nil, errors.New("PDF image count exceeds resource limit")
	}
	var allPixels int64
	for _, line := range lines[2:] {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return nil, errors.New("PDF image inventory is incomplete")
		}
		page, ep := strconv.Atoi(fields[0])
		width, ew := strconv.ParseInt(fields[3], 10, 64)
		height, eh := strconv.ParseInt(fields[4], 10, 64)
		if ep != nil || ew != nil || eh != nil || page < 1 || page > pages || width < 1 || height < 1 || width > pdfMaxImagePixels || height > pdfMaxImagePixels || width > pdfMaxImagePixels/height {
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

func pdfCommand(ctx context.Context, limit int, tool string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
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
		return nil, ctx.Err()
	}
	if toolCtx.Err() != nil {
		return nil, fmt.Errorf("PDF tool %s: %w", tool, toolCtx.Err())
	}
	if stdout.full || stderr.full {
		return nil, fmt.Errorf("PDF tool %s output exceeds resource limit", tool)
	}
	if err != nil {
		return nil, fmt.Errorf("PDF tool %s failed: %w", tool, err)
	}
	// Poppler can repair damaged files and still exit zero. Such diagnostics
	// must not become a successful, apparently complete parse.
	if strings.TrimSpace(stderr.buffer.String()) != "" {
		return nil, fmt.Errorf("PDF tool %s reported malformed or unsupported content", tool)
	}
	return stdout.buffer.Bytes(), nil
}
