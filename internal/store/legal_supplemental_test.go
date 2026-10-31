package store

import (
	"context"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/legalnorm"
)

func TestSupplementalImportsPreservePriorSourcesAndRemainUnreviewedDrafts(t *testing.T) {
	root := os.Getenv("LEGALNORM_SUPPLEMENTAL_ASSET_DIR")
	if root == "" {
		t.Skip("set LEGALNORM_SUPPLEMENTAL_ASSET_DIR to the external supplemental legal assets")
	}
	st, err := OpenTest(t.TempDir(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("open isolated PostgreSQL store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("test administrator missing")
	}
	ctx := context.Background()
	var before []GovernedKnowledgeVersion
	for _, snapshot := range []string{
		"S1_civil_code_contract", "S2_tender_law_2017", "S3_government_procurement_law_2014",
		"S4_sme_payment_regulation_2025", "S5_tender_implementing_regulation_2019",
	} {
		input := legalSourceImportFixture(t, root, snapshot)
		result, err := st.ImportLegalSource(ctx, admin, input)
		if err != nil {
			t.Fatalf("prepare existing source %s: %v", snapshot, err)
		}
		for _, ref := range result.Articles {
			version, err := st.GetGovernedKnowledgeVersion(admin, ref.KnowledgeID, ref.VersionID)
			if err != nil {
				t.Fatal(err)
			}
			before = append(before, version)
		}
	}
	if len(before) != 25 {
		t.Fatalf("prepared %d original articles, want 25", len(before))
	}
	cases := []struct {
		id, status, until string
		count             int
	}{
		{"CN-CONTRACT-LAW-HISTORY", legalnorm.StatusRepealed, "2021-01-01", 5},
		{"CN-SPC-TIME-EFFECT", legalnorm.StatusEffective, "", 6},
		{"CN-SPC-CONTRACT-GENERAL-INTERPRETATION", legalnorm.StatusEffective, "", 7},
	}
	seenCaptures := make(map[string]bool)
	seenArticles := make(map[string]bool)
	for _, tc := range cases {
		capture, err := legalnorm.LoadSupplementalCapture(root, tc.id)
		if err != nil {
			t.Fatal(err)
		}
		input := LegalSourceImportInput{Capture: capture, IdempotencyKey: "supplemental-v1-" + strings.ToLower(tc.id)}
		for _, article := range capture.Articles {
			input.Articles = append(input.Articles, LegalArticleVersionInput{
				ArticleNumber: article.ArticleNumber, KnowledgeID: "legal-" + strings.ToLower(tc.id) + "-" + strconv.Itoa(article.ArticleNumber),
				DisplayVersion: capture.VersionID,
			})
		}
		result, err := st.ImportLegalSource(ctx, admin, input)
		if err != nil {
			t.Fatalf("import %s: %v", tc.id, err)
		}
		if seenCaptures[result.CaptureSnapshotID] || result.SourceID != tc.id || len(result.Articles) != tc.count ||
			result.RawSHA256 != capture.ExpectedRawSHA256 || result.StatusSHA256 != capture.ExpectedStatusSHA256 {
			t.Fatalf("invalid independent receipt for %s: %+v", tc.id, result)
		}
		seenCaptures[result.CaptureSnapshotID] = true
		replay, err := st.ImportLegalSource(ctx, admin, input)
		if err != nil || !reflect.DeepEqual(replay, result) {
			t.Fatalf("%s did not replay idempotently: %v", tc.id, err)
		}
		storedCapture, err := st.GetLegalSourceCapture(ctx, admin, result.CaptureSnapshotID)
		if err != nil || storedCapture.Status != tc.status || storedCapture.EffectiveUntil != tc.until {
			t.Fatalf("%s stored capture/status: %+v %v", tc.id, storedCapture, err)
		}
		for _, ref := range result.Articles {
			version, err := st.GetGovernedKnowledgeVersion(admin, ref.KnowledgeID, ref.VersionID)
			if err != nil {
				t.Fatal(err)
			}
			metadata := version.Content.LegalMetadata
			if seenArticles[ref.KnowledgeID] || version.Status != "draft" || metadata == nil || metadata.SourceID != tc.id ||
				metadata.NormVersion != capture.VersionID || metadata.SourceSnapshotID != result.CaptureSnapshotID ||
				metadata.OfficialStatus != tc.status || metadata.EffectiveUntil != tc.until || metadata.LegalReviewStatus != "NOT_RUN" || metadata.Reviewer != nil {
				t.Fatalf("%s has incorrect draft/source/review metadata: %+v", tc.id, version)
			}
			seenArticles[ref.KnowledgeID] = true
			if !strings.Contains(version.Content.Summary, "适用") || !strings.Contains(version.Content.Summary, "NOT_RUN") {
				t.Fatalf("%s/%d is missing the unreviewed applicability context", tc.id, metadata.ArticleNumber)
			}
			if tc.id == "CN-CONTRACT-LAW-HISTORY" && metadata.ArticleNumber == 114 &&
				(!strings.Contains(version.Content.Summary, "FAIL") || !strings.Contains(version.Content.Body, "增加;约定")) {
				t.Fatal("article 114 lost the source punctuation or strict crosscheck failure")
			}
		}
	}
	if len(seenCaptures) != 3 || len(seenArticles) != 18 {
		t.Fatalf("imported %d captures and %d articles, want 3 and 18", len(seenCaptures), len(seenArticles))
	}
	for _, old := range before {
		after, err := st.GetGovernedKnowledgeVersion(admin, old.Ref.KnowledgeID, old.Ref.VersionID)
		if err != nil || !reflect.DeepEqual(old, after) {
			t.Fatalf("original article %s changed after supplemental import: %v", old.Ref.KnowledgeID, err)
		}
	}
}
