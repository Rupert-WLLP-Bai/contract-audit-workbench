// Package evidencepdf renders the readable PDF that is authenticated as an
// entry in an evidence bundle. It embeds the configured Unicode TrueType font.
package evidencepdf

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/signintech/gopdf"
)

type Section struct {
	Title string
	Lines []string
}

// Render refuses to produce a PDF if the deployment font is missing, cannot
// be embedded, or lacks any printable character in the submitted content.
func Render(ctx context.Context, fontFile string, sections []Section) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(fontFile) == "" {
		return nil, errors.New("evidence PDF font is not configured")
	}
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	pdf.AddPage()
	if err := pdf.AddTTFFont("evidence", fontFile); err != nil {
		return nil, errors.New("cannot load the configured evidence PDF TrueType font")
	}
	if err := pdf.SetFont("evidence", "", 11); err != nil {
		return nil, errors.New("cannot use the configured evidence PDF font")
	}
	// Check the actual font cmap before the library can silently substitute a
	// space for a missing glyph. Cache checks by rune for long reports.
	checked := make(map[rune]bool)
	for _, section := range sections {
		texts := append([]string{section.Title}, section.Lines...)
		for _, text := range texts {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for _, r := range cleanText(text) {
				if r == '\n' || checked[r] {
					continue
				}
				ok, err := pdf.IsCurrFontContainGlyph(r)
				if err != nil || !ok {
					return nil, fmt.Errorf("configured evidence PDF font lacks a required glyph U+%04X", r)
				}
				checked[r] = true
			}
		}
	}
	y := 45.0
	for _, section := range sections {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := pdf.SetFont("evidence", "", 14); err != nil {
			return nil, err
		}
		if err := writeText(ctx, pdf, cleanText(section.Title), &y, 20); err != nil {
			return nil, err
		}
		if err := pdf.SetFont("evidence", "", 10.5); err != nil {
			return nil, err
		}
		for _, line := range section.Lines {
			if err := writeText(ctx, pdf, cleanText(line), &y, 16); err != nil {
				return nil, err
			}
		}
		y += 10
	}
	return pdf.GetBytesPdfReturnErr()
}

func cleanText(text string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\r':
			return -1
		case '\n':
			return r
		case '\t':
			return ' '
		}
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
}

func writeText(ctx context.Context, pdf *gopdf.GoPdf, text string, y *float64, lineHeight float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, paragraph := range strings.Split(text, "\n") {
		if paragraph == "" {
			*y += lineHeight
			continue
		}
		lines, err := pdf.SplitText(paragraph, 500)
		if err != nil {
			return err
		}
		for _, line := range lines {
			if err := ctx.Err(); err != nil {
				return err
			}
			if *y+lineHeight > 795 {
				pdf.AddPage()
				*y = 45
			}
			pdf.SetXY(45, *y)
			if err := pdf.Cell(&gopdf.Rect{W: 500, H: lineHeight}, line); err != nil {
				return err
			}
			*y += lineHeight
		}
	}
	return nil
}
