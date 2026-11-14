package model

import (
	"contractaudit/internal/legalnorm"
	"testing"
)

func TestRequestDigestScrubsSnapshotIDsButRetainsArticleOrder(t *testing.T) {
	a := LegalSourceImportInput{Capture: legalnorm.CaptureInput{SourceID: "source", SourceSnapshotID: "one", Articles: []legalnorm.ArticleRef{{ArticleNumber: 1, SourceSnapshotID: "one"}, {ArticleNumber: 2, SourceSnapshotID: "one"}}}, Articles: []LegalArticleVersionInput{{ArticleNumber: 1, KnowledgeID: "a", DisplayVersion: "1"}, {ArticleNumber: 2, KnowledgeID: "b", DisplayVersion: "1"}}, IdempotencyKey: "key-one"}
	first, err := RequestDigest(a)
	if err != nil {
		t.Fatal(err)
	}
	b := a
	b.Capture.Articles = append([]legalnorm.ArticleRef(nil), a.Capture.Articles...)
	b.Capture.SourceSnapshotID, b.IdempotencyKey = "another", "key-two"
	for i := range b.Capture.Articles {
		b.Capture.Articles[i].SourceSnapshotID = "another"
	}
	second, err := RequestDigest(b)
	if err != nil || second != first {
		t.Fatalf("snapshot/key affected digest: %s %v", second, err)
	}
	b.Articles = append([]LegalArticleVersionInput(nil), a.Articles...)
	b.Articles[0], b.Articles[1] = b.Articles[1], b.Articles[0]
	changed, err := RequestDigest(b)
	if err != nil || changed == first {
		t.Fatalf("article order missing from digest: %s %v", changed, err)
	}
	if a.Capture.Articles[0].SourceSnapshotID != "one" {
		t.Fatal("digest mutated input")
	}
}
