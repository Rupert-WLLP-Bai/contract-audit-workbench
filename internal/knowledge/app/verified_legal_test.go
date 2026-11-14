package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"contractaudit/internal/knowledge/model"
	"contractaudit/internal/legalnorm"
)

type legalDraftTestPorts struct {
	calls      []string
	captureErr error
	digests    map[string]string
}

func (p *legalDraftTestPorts) VersionSlotOccupied(_ context.Context, id, version string) (bool, error) {
	p.calls = append(p.calls, "slot:"+id+":"+version)
	return false, nil
}
func (p *legalDraftTestPorts) GovernedFamilyHasOtherKind(_ context.Context, _, id, _ string) (bool, error) {
	p.calls = append(p.calls, "family:"+id)
	return false, nil
}
func (p *legalDraftTestPorts) LoadLegalCapture(_ context.Context, id string) (*LegalCaptureFacts, error) {
	p.calls = append(p.calls, "capture:"+id)
	return nil, p.captureErr
}
func (p *legalDraftTestPorts) LoadLegalArticleIndex(context.Context, *LegalCaptureFacts) ([]legalnorm.VerifiedArticle, error) {
	p.calls = append(p.calls, "index")
	return nil, nil
}
func (p *legalDraftTestPorts) InsertVersion(context.Context, CreateVersionRecord) (time.Time, error) {
	p.calls = append(p.calls, "version")
	return time.Time{}, nil
}
func (p *legalDraftTestPorts) InsertInitialState(context.Context, CreateDraftIdentity, string, string) error {
	p.calls = append(p.calls, "state")
	return nil
}
func (p *legalDraftTestPorts) AppendHistory(context.Context, CreateDraftIdentity, model.GovernedKnowledgeHistoryEntry) error {
	p.calls = append(p.calls, "history")
	return nil
}
func (p *legalDraftTestPorts) ImportedLegalContentSHA(_ context.Context, _, _ string, ref model.KnowledgeRef) (string, error) {
	p.calls = append(p.calls, "ref:"+ref.KnowledgeID)
	return p.digests[ref.KnowledgeID], nil
}

func legalDraftContent(id string) model.GovernedKnowledgeContent {
	body := "第一条正文"
	digest := model.SHA256([]byte(body))
	metadata := &model.LegalMetadata{SourceID: "source", NormTitle: "法律", NormVersion: "1", ArticleNumber: 1, ArticleLabel: "第一条", DocumentType: "law", Issuer: "issuer", Publisher: "publisher", OfficialURL: "https://example.org", PromulgatedOn: "2020-01-01", EffectiveFrom: "2020-01-01", OfficialStatusObservationID: "status", OfficialStatus: legalnorm.StatusEffective, StatusRegistryURL: "https://example.org/status", StatusObservedAt: "2020-01-01T00:00:00Z", StatusEvidenceSHA256: digest, StatusResponseStatus: 200, SourceSnapshotID: "capture", SourceProvenance: "verified_official_capture", SourceCapturedAt: "2020-01-01T00:00:00Z", SourceResponseStatus: 200, SourceSHA256: digest, NormTextSHA256: digest, CanonicalTextVersion: legalnorm.CanonicalTextVersion, SourceTextStart: 0, SourceTextEnd: len([]rune(body)), ExtractorID: "extractor", ExtractorVersion: "1", TextCheckStatus: "PASS", LegalReviewStatus: "NOT_RUN"}
	return model.GovernedKnowledgeContent{Encoding: model.ContentEncoding, KnowledgeID: id, Version: "1", Kind: "legal", Title: "法律 第一条", Body: body, Summary: "摘要", SourceType: "reference", SourceReference: metadata.OfficialURL, LegalMetadata: metadata}
}

func TestVerifiedLegalPrepareOrderingAndNoWritesBeforeCapture(t *testing.T) {
	p := &legalDraftTestPorts{}
	first, second := legalDraftContent("a"), legalDraftContent("b")
	drafts, err := PrepareVerifiedLegalDrafts(context.Background(), p, p, "tenant", []model.GovernedKnowledgeContent{first, second})
	if err != nil || len(drafts) != 2 || !reflect.DeepEqual(p.calls, []string{"slot:a:1", "family:a", "slot:b:1", "family:b"}) {
		t.Fatalf("drafts=%+v calls=%v err=%v", drafts, p.calls, err)
	}
	if drafts[0].Ref.ContentSHA256 != model.SHA256(drafts[0].ContentBytes) || drafts[0].Ref.VersionID != model.VersionID("tenant", "a", "1") {
		t.Fatalf("bad prepared ref: %+v", drafts[0])
	}
	p.calls = nil
	_, err = PrepareVerifiedLegalDrafts(context.Background(), p, p, "tenant", []model.GovernedKnowledgeContent{first, first})
	if err == nil || err.Error() != "法律条文版本编号重复" || !reflect.DeepEqual(p.calls, []string{"slot:a:1", "family:a"}) {
		t.Fatalf("duplicate calls=%v err=%v", p.calls, err)
	}
	sentinel := errors.New("capture read failed")
	p.captureErr = sentinel
	p.calls = nil
	err = PersistVerifiedLegalDrafts(context.Background(), p, p, "tenant", "actor", "capture", drafts)
	if !errors.Is(err, sentinel) || !reflect.DeepEqual(p.calls, []string{"capture:capture"}) {
		t.Fatalf("capture-before-write calls=%v err=%v", p.calls, err)
	}
}

func TestImportedLegalReplayStopsOnFirstMismatch(t *testing.T) {
	p := &legalDraftTestPorts{digests: map[string]string{"a": "one", "b": "changed", "c": "three"}}
	refs := []model.KnowledgeRef{{KnowledgeID: "a", ContentSHA256: "one"}, {KnowledgeID: "b", ContentSHA256: "two"}, {KnowledgeID: "c", ContentSHA256: "three"}}
	err := ValidateImportedLegalReferences(context.Background(), p, "tenant", "capture", refs)
	if err == nil || err.Error() != "法律来源幂等结果与已保存条文不一致" || !reflect.DeepEqual(p.calls, []string{"ref:a", "ref:b"}) {
		t.Fatalf("calls=%v err=%v", p.calls, err)
	}
}
