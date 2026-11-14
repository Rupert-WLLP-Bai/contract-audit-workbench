// File metadata (manifest mirror):
// Purpose: Pins both Legal replay phases and their ordering relative to baseline preparation, ID generation, batch validation, capture insertion, and draft writes.
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
	"encoding/json"
	"reflect"
	"testing"

	knowledgeModel "contractaudit/internal/knowledge/model"
	legalModel "contractaudit/internal/legal/model"
	"contractaudit/internal/legalnorm"
)

type importTestPorts struct {
	calls  []string
	replay CaptureReplay
	found  bool
	refs   []knowledgeModel.KnowledgeRef
}

func (p *importTestPorts) FindReplay(context.Context, string, string, string) (CaptureReplay, bool, error) {
	p.calls = append(p.calls, "replay")
	return p.replay, p.found, nil
}
func (p *importTestPorts) ReadSummary(context.Context, string, string) (CaptureSummary, error) {
	p.calls = append(p.calls, "summary")
	return CaptureSummary{CaptureID: "existing"}, nil
}
func (p *importTestPorts) InsertCapture(_ context.Context, record CaptureInsert) error {
	p.calls = append(p.calls, "insert:"+record.CaptureID)
	return nil
}
func (p *importTestPorts) NewCaptureID() (string, error) {
	p.calls = append(p.calls, "id")
	return "new", nil
}
func (p *importTestPorts) PrepareLegalDrafts(_ context.Context, contents []knowledgeModel.GovernedKnowledgeContent) ([]knowledgeModel.PreparedVerifiedLegalDraft, error) {
	p.calls = append(p.calls, "prepare")
	out := make([]knowledgeModel.PreparedVerifiedLegalDraft, len(contents))
	for i := range contents {
		out[i] = knowledgeModel.PreparedVerifiedLegalDraft{Ref: p.refs[i], Content: contents[i]}
	}
	return out, nil
}
func (p *importTestPorts) PersistLegalDrafts(_ context.Context, id string, _ []knowledgeModel.PreparedVerifiedLegalDraft) error {
	p.calls = append(p.calls, "persist:"+id)
	return nil
}
func (p *importTestPorts) ValidateImportedRefs(_ context.Context, id string, refs []knowledgeModel.KnowledgeRef) error {
	p.calls = append(p.calls, "refs:"+id)
	p.refs = refs
	return nil
}

func TestCommitImportReplayPrecedesIDAndBatch(t *testing.T) {
	refs := []knowledgeModel.KnowledgeRef{{KnowledgeID: "old", VersionID: "version", ContentSHA256: "hash"}}
	raw, _ := json.Marshal(refs)
	p := &importTestPorts{found: true, replay: CaptureReplay{RequestDigest: "digest", CaptureID: "existing", ImportedRefs: raw}}
	result, err := CommitImport(context.Background(), p, p, p, "tenant", "actor", "key", "digest", PreparedImport{})
	if err != nil || !reflect.DeepEqual(p.calls, []string{"replay", "summary", "refs:existing"}) || !reflect.DeepEqual(result.Articles, refs) {
		t.Fatalf("result=%+v calls=%v err=%v", result, p.calls, err)
	}
	p.calls = nil
	p.replay.ImportedRefs = []byte(`[]`)
	_, err = CommitImport(context.Background(), p, p, p, "tenant", "actor", "key", "digest", PreparedImport{})
	if err == nil || err.Error() != "法律来源幂等结果损坏" || !reflect.DeepEqual(p.calls, []string{"replay", "summary"}) {
		t.Fatalf("calls=%v err=%v", p.calls, err)
	}
}
func TestCommitImportPreparesBeforeCaptureAndPersistsAfter(t *testing.T) {
	ref := knowledgeModel.KnowledgeRef{KnowledgeID: "legal", VersionID: "v", ContentSHA256: "sha"}
	p := &importTestPorts{refs: []knowledgeModel.KnowledgeRef{ref}}
	prepared := PreparedImport{Versions: map[int]legalModel.LegalArticleVersionInput{1: {ArticleNumber: 1, KnowledgeID: "legal", DisplayVersion: "1"}}, Verified: legalnorm.VerifiedBaseline{Capture: legalnorm.VerifiedCapture{Articles: []legalnorm.VerifiedArticle{{Ref: legalnorm.ArticleRef{ArticleNumber: 1, ArticleLabel: "第一条"}, FullText: "正文"}}}}}
	result, err := CommitImport(context.Background(), p, p, p, "tenant", "actor", "key", "digest", prepared)
	if err != nil || !reflect.DeepEqual(p.calls, []string{"replay", "id", "prepare", "insert:new", "persist:new"}) || !reflect.DeepEqual(result.Articles, []knowledgeModel.KnowledgeRef{ref}) {
		t.Fatalf("result=%+v calls=%v err=%v", result, p.calls, err)
	}
}

func TestReadReplayDoesNotRequireInputReverification(t *testing.T) {
	refs := []knowledgeModel.KnowledgeRef{{KnowledgeID: "legal", VersionID: "v", ContentSHA256: "sha"}}
	raw, _ := json.Marshal(refs)
	p := &importTestPorts{found: true, replay: CaptureReplay{RequestDigest: "digest", CaptureID: "existing", ImportedRefs: raw}}
	result, found, err := ReplayImport(context.Background(), p, p, "tenant", "actor", "key", "digest")
	if err != nil || !found || !reflect.DeepEqual(result.Articles, refs) || !reflect.DeepEqual(p.calls, []string{"replay", "summary", "refs:existing"}) {
		t.Fatalf("result=%+v found=%v calls=%v err=%v", result, found, p.calls, err)
	}
	// This input would fail the miss path, but the matching first replay does not consume it.
	if _, err := PrepareImport(legalModel.LegalSourceImportInput{}); err == nil {
		t.Fatal("invalid miss-path input unexpectedly prepared")
	}
}
