// File metadata (manifest mirror):
// Purpose: Pins complete capture integrity error precedence and preservation of stored status mapping bytes.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"testing"

	knowledgeModel "contractaudit/internal/knowledge/model"
	legalModel "contractaudit/internal/legal/model"
	"contractaudit/internal/legalnorm"
)

type captureReaderStub struct {
	raw   RawCapture
	found bool
	calls int
}

func (f *captureReaderStub) ReadCapture(context.Context, string, string) (RawCapture, bool, error) {
	f.calls++
	return f.raw, f.found, nil
}

func TestLoadCaptureKeepsIntegrityOrderAndRawMapping(t *testing.T) {
	body := "第一条"
	hash := knowledgeModel.SHA256([]byte(body))
	article := legalnorm.VerifiedArticle{Ref: legalnorm.ArticleRef{FullText: body, TextSHA256: hash}, FullText: body, TextSHA256: hash, Start: 0, End: len([]rune(body))}
	manifest, err := legalModel.ArticleManifest([]legalnorm.VerifiedArticle{article})
	if err != nil {
		t.Fatal(err)
	}
	raw := RawCapture{Capture: legalModel.LegalSourceCapture{RawResponse: []byte("raw"), RawSHA256: knowledgeModel.SHA256([]byte("raw")), ExtractedFullText: body, ExtractedSHA256: hash, StatusResponse: []byte("status"), StatusSHA256: knowledgeModel.SHA256([]byte("status")), RequestDigest: knowledgeModel.SHA256([]byte("request"))}, ArticleManifest: manifest, ArticleManifestSHA: knowledgeModel.SHA256(manifest), ImportedRefs: []byte(`[]`), StatusMapping: []byte(`{ "status": "effective" }`)}
	reader := &captureReaderStub{raw: raw, found: true}
	got, err := LoadCapture(context.Background(), reader, "tenant", "capture")
	if err != nil || reader.calls != 1 || string(got.StatusMapping) != string(raw.StatusMapping) || len(got.Articles) != 1 {
		t.Fatalf("valid capture=%+v calls=%d err=%v", got, reader.calls, err)
	}
	reader.raw.Capture.RawSHA256 = "bad"
	reader.raw.ArticleManifest = []byte(`bad`)
	if _, err := LoadCapture(context.Background(), reader, "tenant", "capture"); err == nil || err.Error() != "法律来源快照的摘要校验失败" {
		t.Fatalf("digest precedence: %v", err)
	}
	reader.raw = raw
	reader.raw.ImportedRefs, reader.raw.StatusMapping = []byte(`bad`), []byte(`bad`)
	if _, err := LoadCapture(context.Background(), reader, "tenant", "capture"); err == nil || err.Error() != "法律来源导入版本索引无效" {
		t.Fatalf("refs precedence: %v", err)
	}
	reader.raw = raw
	reader.raw.StatusMapping = []byte(`{}`)
	if _, err := LoadCapture(context.Background(), reader, "tenant", "capture"); err == nil || err.Error() != "法律效力状态映射证据无效" {
		t.Fatalf("mapping: %v", err)
	}
}
