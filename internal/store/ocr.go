package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"contractaudit/internal/docir"
	"contractaudit/internal/model"
)

const (
	kindText = iota
	kindImage
	kindPDF
	kindDOCX
	kindBinary
)

func fileKind(filename, media string) int {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".png", ".jpg", ".jpeg":
		return kindImage
	case ".pdf":
		return kindPDF
	case ".docx":
		return kindDOCX
	case ".doc", ".xlsx", ".xls", ".gif", ".zip", ".bin":
		return kindBinary
	}
	switch mediaBase(media) {
	case "image/png", "image/jpeg", "image/jpg":
		return kindImage
	case "application/pdf":
		return kindPDF
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return kindDOCX
	}
	return kindText
}

func mediaBase(media string) string {
	mt := strings.ToLower(strings.TrimSpace(media))
	if i := strings.Index(mt, ";"); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	return mt
}

func binaryReason(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".doc", ".xlsx", ".xls":
		return "本版仅支持 TXT/MD、PNG/JPEG、PDF 与 DOCX；旧版 Office 和电子表格不能解析。"
	default:
		return "这份材料不能解析成正文，不能编造坐标。"
	}
}

func (s *Store) interpretImage(ctx context.Context, doc Document, body []byte) Interpreted {
	return s.interpretImageWithModels(ctx, doc, body, s.models)
}

func (s *Store) interpretImageWithModels(ctx context.Context, doc Document, body []byte, models Models) Interpreted {
	text, err := transcribe(ctx, models, doc, body)
	if err != nil {
		msg := err.Error()
		if errors.Is(err, errNoOCR) {
			msg = "图片需要转写模型，当前没有配置。"
		}
		return Interpreted{Document: doc, ParseError: msg}
	}
	ir := docir.Build(text)
	if len(ir.Blocks) == 0 {
		return Interpreted{Document: doc, ParseError: "图片转写没有得到可定位的正文。"}
	}
	return Interpreted{Document: doc, Ready: true, FromOCR: true, Text: ir.NormalizedText, IR: ir}
}

var errNoOCR = errors.New("ocr model is not configured")

func transcribe(ctx context.Context, models Models, doc Document, body []byte) (string, error) {
	if models == nil {
		return "", errNoOCR
	}
	text, err := models.Transcribe(ctx, doc.MediaType, body)
	if err != nil {
		return "", errors.New(imageError(err))
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("图片转写没有得到可用正文。")
	}
	return text, nil
}

func imageError(err error) string {
	switch {
	case errors.Is(err, model.ErrUnusable):
		return "图片转写没有得到可用正文。"
	case errors.Is(err, model.ErrRequest):
		return "图片转写调用失败，没有编造正文。"
	default:
		return "图片转写失败，没有编造正文。"
	}
}
