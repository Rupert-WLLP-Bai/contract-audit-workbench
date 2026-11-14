package localocr

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"unicode/utf8"
)

// Extract verifies the official default archive's single-page identity before
// exposing its text using the official full renderer.
func Extract(ctx context.Context, python string, archive []byte) (string, error) {
	z, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return "", errors.New("invalid local OCR archive")
	}
	files := make(map[string][]byte, 2)
	for _, f := range z.File {
		if f.Name != "markdown.md" && f.Name != "middle_json.json" {
			continue
		}
		if _, exists := files[f.Name]; exists || f.UncompressedSize64 > maxResultBytes {
			return "", errors.New("invalid local OCR archive entry")
		}
		r, err := f.Open()
		if err != nil {
			return "", errors.New("invalid local OCR archive entry")
		}
		b, readErr := io.ReadAll(io.LimitReader(r, maxResultBytes+1))
		closeErr := r.Close()
		if readErr != nil || closeErr != nil || len(b) > maxResultBytes {
			return "", errors.New("invalid local OCR archive entry")
		}
		files[f.Name] = b
	}
	var doc struct {
		Schema   string `json:"schema"`
		Version  string `json:"schema_version"`
		Full     bool   `json:"is_full_document"`
		Metadata struct {
			Producer struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"producer"`
		} `json:"metadata"`
		Extensions struct {
			Mineru struct {
				Tier      string `json:"tier"`
				ParseMode string `json:"parse_mode"`
			} `json:"mineru"`
		} `json:"extensions"`
		Pages []struct {
			Index  int `json:"page_idx"`
			Blocks []struct {
				Type string `json:"type"`
			} `json:"blocks"`
		} `json:"pages"`
	}
	if json.Unmarshal(files["middle_json.json"], &doc) != nil || doc.Schema != "docvortex.middle" || doc.Version != "2.0" || !doc.Full || doc.Metadata.Producer.Name != "mineru" || doc.Metadata.Producer.Version != "4.2.2" || doc.Extensions.Mineru.Tier != "flash" || doc.Extensions.Mineru.ParseMode != "ocr" || len(doc.Pages) != 1 || doc.Pages[0].Index != 0 {
		return "", errors.New("local OCR output identity mismatch")
	}
	// FULL uses the official SDK renderer, including auxiliary blocks that the
	// default task Markdown intentionally suppresses.
	cmd := exec.CommandContext(ctx, python, "-c", `import sys
from importlib.metadata import version
if version("mineru") != "4.2.2":
    raise RuntimeError("unsupported mineru version")
from mineru.parser.base import ParseResult
from mineru.render.contracts import RenderMode
sys.stdout.write(ParseResult.from_json(sys.stdin.read()).markdown(mode=RenderMode.FULL))`)
	cmd.Stdin = bytes.NewReader(files["middle_json.json"])
	var out boundedBuffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil || out.exceeded {
		return "", errors.New("local OCR full render failed")
	}
	text := out.buf.String()
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" {
		return "", errors.New("local OCR output is empty or invalid")
	}
	return text, nil
}

type boundedBuffer struct {
	buf      bytes.Buffer
	exceeded bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > maxResultBytes-b.buf.Len() {
		b.exceeded = true
		return 0, errors.New("local OCR rendered output exceeds limit")
	}
	return b.buf.Write(p)
}
