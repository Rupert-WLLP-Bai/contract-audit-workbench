package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/legalnorm"
)

func validLegalContentFixture() GovernedKnowledgeContent {
	body := "甲方与乙方应当依照本法约定履行义务。"
	return GovernedKnowledgeContent{
		Encoding: knowledgeContentEncoding, KnowledgeID: "legal-civil-code-496", Version: "2020-05-28",
		Kind: "legal", Title: "中华人民共和国民法典（第三编 合同） 第四百九十六条",
		Body: body, Summary: "第四百九十六条", SourceType: "reference",
		SourceReference: "https://www.cac.gov.cn/2020-06/01/c_15925617772683192.htm",
		LegalMetadata: &LegalMetadata{
			SourceID: "CN-CIVIL-CODE", NormTitle: "中华人民共和国民法典（第三编 合同）", DocumentType: "law",
			NormVersion: "2020-05-28", ArticleNumber: 496, ArticleLabel: "第四百九十六条",
			Issuer: "全国人民代表大会", Publisher: "中国网信网", OfficialURL: "https://www.cac.gov.cn/2020-06/01/c_15925617772683192.htm",
			PromulgatedOn: "2020-05-28", EffectiveFrom: "2021-01-01", OfficialStatusObservationID: "S8A_npc_civil_code_status",
			OfficialStatus: "effective", StatusRegistryURL: "https://flk.npc.gov.cn/law-search/search/list",
			StatusObservedAt: "2026-10-31T00:01:34+08:00", StatusEvidenceSHA256: "7deedafcc53209809cd5d94de0c57b544baffb1b947ba8152cf2b6df61e3d6e4",
			StatusResponseStatus: 200, StatusContentType: "application/json", SourceSnapshotID: "lsc_test_capture",
			SourceProvenance: "verified_official_capture", SourceCapturedAt: "2026-10-30T23:47:25+08:00",
			SourceResponseStatus: 200, SourceContentType: "text/html; charset=utf-8",
			SourceSHA256:   "1253542d80bd1276c4eb237c7ab223589b770f8e6e0463b878d2a9d362393f6b",
			NormTextSHA256: sha256Bytes([]byte(body)), CanonicalTextVersion: "legalnorm-whitespace-v1",
			SourceTextStart: 100, SourceTextEnd: 100 + len([]rune(body)), ExtractorID: "cac-civil-code-html", ExtractorVersion: "1",
			TextCheckStatus: "PASS", LegalReviewStatus: "NOT_RUN", Reviewer: nil,
		},
	}
}

func TestLegalKnowledgeContentRequiresServerVerifiedMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*GovernedKnowledgeContent)
		valid  bool
	}{
		{name: "complete source-linked article", valid: true},
		{name: "missing metadata", mutate: func(content *GovernedKnowledgeContent) { content.LegalMetadata = nil }},
		{name: "body digest mismatch", mutate: func(content *GovernedKnowledgeContent) {
			content.LegalMetadata.NormTextSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
		}},
		{name: "invented legal reviewer", mutate: func(content *GovernedKnowledgeContent) {
			reviewer := "demo-admin"
			content.LegalMetadata.Reviewer = &reviewer
		}},
		{name: "review status cannot be changed", mutate: func(content *GovernedKnowledgeContent) { content.LegalMetadata.LegalReviewStatus = "PASS" }},
		{name: "cannot have a policy binding", mutate: func(content *GovernedKnowledgeContent) { content.Binding.PackID = "some-pack" }},
		{name: "legal metadata rejected for policy", mutate: func(content *GovernedKnowledgeContent) { content.Kind = "policy" }},
		{name: "source reference cannot differ", mutate: func(content *GovernedKnowledgeContent) { content.SourceReference = "https://example.invalid/law" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := validLegalContentFixture()
			content.Encoding = knowledgeContentEncoding
			if test.mutate != nil {
				test.mutate(&content)
			}
			err := validateKnowledgeContent(content)
			if test.valid && err != nil {
				t.Fatalf("valid legal article rejected: %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("invalid legal article accepted")
			}
		})
	}
}

func TestLegalMetadataDoesNotInventOpenEndedEffectiveUntil(t *testing.T) {
	content := validLegalContentFixture()
	if content.LegalMetadata.EffectiveUntil != "" {
		t.Fatal("baseline article should not invent an effective-until date")
	}
	if _, err := time.Parse("2006-01-02", content.LegalMetadata.EffectiveFrom); err != nil {
		t.Fatalf("effective date should be date precision: %v", err)
	}
}

func TestLegalReadWithoutPostgreSQLReturnsServiceUnavailable(t *testing.T) {
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("test administrator missing")
	}
	var st *Store
	_, err := st.SearchLegalKnowledgeAdmin(context.Background(), admin, LegalSearch{})
	var problem *fault.Error
	if !errors.As(err, &problem) || problem.Status != 503 || problem.Code != "legal_knowledge_unavailable" {
		t.Fatalf("legal read without PostgreSQL should return a service error, got %v", err)
	}
	_, err = st.ImportLegalSource(context.Background(), admin, LegalSourceImportInput{})
	if !errors.As(err, &problem) || problem.Status != 503 || problem.Code != "legal_knowledge_unavailable" {
		t.Fatalf("legal import without PostgreSQL should not claim persistence or panic, got %v", err)
	}
}

func TestLegalBusinessDateUsesShanghaiMidnightAndHalfOpenInterval(t *testing.T) {
	tests := []struct {
		name string
		at   string
		date string
	}{
		{"UTC instant is already next Shanghai date", "2025-05-31T16:00:00Z", "2025-06-01"},
		{"Shanghai inclusive start", "2025-06-01T00:00:00+08:00", "2025-06-01"},
		{"instant before Shanghai start", "2025-05-31T23:59:59.999999999+08:00", "2025-05-31"},
		{"Shanghai exclusive end", "2025-07-01T00:00:00+08:00", "2025-07-01"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			date, err := legalBusinessDate(test.at)
			if err != nil || date != test.date {
				t.Fatalf("legal business date = %q, %v; want %q", date, err, test.date)
			}
		})
	}
	meta := LegalMetadata{EffectiveFrom: "2025-06-01", EffectiveUntil: "2025-07-01"}
	if !legalEffectiveOnDate(meta, "2025-06-01") || !legalEffectiveOnDate(meta, "2025-06-30") {
		t.Fatal("effective interval must include its start and the preceding calendar day")
	}
	if legalEffectiveOnDate(meta, "2025-05-31") || legalEffectiveOnDate(meta, "2025-07-01") {
		t.Fatal("effective interval must exclude the date before start and the exclusive end")
	}
}

func TestLegalSourceImportPersistsOneOfficialCaptureAndFiveDrafts(t *testing.T) {
	assetDir := os.Getenv("LEGALNORM_ASSET_DIR")
	if assetDir == "" {
		t.Fatal("LEGALNORM_ASSET_DIR must point to the external official legal-sources directory")
	}
	st, err := OpenTest(t.TempDir(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("isolated PostgreSQL test database unavailable: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("test administrator missing")
	}
	input := legalSourceImportFixture(t, assetDir, "S1_civil_code_contract")
	result, err := st.ImportLegalSource(context.Background(), admin, input)
	if err != nil {
		t.Fatalf("import official legal capture: %v", err)
	}
	if result.Provenance != "verified_official_capture" || len(result.Articles) != 5 {
		t.Fatalf("import result incomplete: %+v", result)
	}
	replayed, err := st.ImportLegalSource(context.Background(), admin, input)
	if err != nil || !reflect.DeepEqual(replayed, result) {
		t.Fatalf("same idempotency key did not replay the original result: equal=%v err=%v", reflect.DeepEqual(replayed, result), err)
	}
	tamperedSource := input
	tamperedSource.Capture.RawBytes = append([]byte(nil), input.Capture.RawBytes...)
	tamperedSource.Capture.RawBytes[0] ^= 1
	_, err = st.ImportLegalSource(context.Background(), admin, tamperedSource)
	requireKnowledgeFault(t, err, 409)
	tamperedStatus := input
	tamperedStatus.Capture.StatusEvidenceRaw = append([]byte(nil), input.Capture.StatusEvidenceRaw...)
	tamperedStatus.Capture.StatusEvidenceRaw[0] ^= 1
	_, err = st.ImportLegalSource(context.Background(), admin, tamperedStatus)
	requireKnowledgeFault(t, err, 409)
	capture, err := st.GetLegalSourceCapture(context.Background(), admin, result.CaptureSnapshotID)
	if err != nil {
		t.Fatalf("read exact imported source capture: %v", err)
	}
	if capture.CaptureSnapshotID != result.CaptureSnapshotID || capture.RawSHA256 != result.RawSHA256 || capture.StatusSHA256 != result.StatusSHA256 {
		t.Fatalf("exact capture identity/digests differ from import: %+v", capture)
	}
	forged := validLegalContentFixture()
	forged.KnowledgeID, forged.Version = "legal-forged-copy", "1"
	forged.LegalMetadata.SourceSnapshotID = "lsc_client_forged"
	if _, err := st.CreateGovernedKnowledge(admin, CreateGovernedKnowledgeInput{Content: forged, IdempotencyKey: "forged-legal-create"}); err == nil {
		t.Fatal("generic governance accepted fabricated legal metadata without a persisted verified capture")
	}
	var imported GovernedKnowledgeVersion
	for _, ref := range result.Articles {
		version, err := st.GetGovernedKnowledgeVersion(admin, ref.KnowledgeID, ref.VersionID)
		if err != nil {
			t.Fatalf("read imported legal version: %v", err)
		}
		if version.Content.LegalMetadata.ArticleNumber == 496 {
			imported = version
			break
		}
	}
	if imported.Ref.KnowledgeID == "" {
		t.Fatal("import did not contain article 496")
	}
	copyContent := imported.Content
	copyContent.KnowledgeID, copyContent.Version = "legal-civil-code-496-generic-copy", "reference-1"
	copyContent.Title += "（治理复制）"
	generic, err := st.CreateGovernedKnowledge(admin, CreateGovernedKnowledgeInput{Content: copyContent, IdempotencyKey: "verified-legal-generic-create"})
	if err != nil {
		t.Fatalf("generic governance rejected a body tied to the persisted verified capture: %v", err)
	}
	for _, action := range []string{"submit", "validate", "approve", "publish"} {
		command := knowledgeCommand(generic, action, "verified-legal-generic-"+action)
		command.CrossCaseReviewed = true
		generic, err = st.GovernedKnowledgeCommand(admin, command)
		if err != nil {
			t.Fatalf("legal governance %s: %v", action, err)
		}
	}
	if generic.Status != "published" || generic.Content.LegalMetadata.LegalReviewStatus != "NOT_RUN" || generic.Content.LegalMetadata.Reviewer != nil {
		t.Fatalf("publishing legal content invented legal review: status=%s metadata=%+v", generic.Status, generic.Content.LegalMetadata)
	}
	var genericBindings int
	if err := st.pool.QueryRow(context.Background(), `SELECT count(*) FROM governed_policy_rule_bindings WHERE tenant_id=$1 AND knowledge_id=$2`, accounts.Tenant(admin), generic.Ref.KnowledgeID).Scan(&genericBindings); err != nil {
		t.Fatal(err)
	}
	if genericBindings != 0 {
		t.Fatalf("legal governance inserted %d fictitious policy bindings", genericBindings)
	}
	st.dropSchema = false
	if err := st.Close(); err != nil {
		t.Fatalf("close store before exact capture reload: %v", err)
	}
	reopened, err := st.Reopen()
	if err != nil {
		t.Fatalf("reopen store over same persisted schema after close: %v", err)
	}
	reopened.dropSchema = true
	t.Cleanup(func() { _ = reopened.Close() })
	reloaded, err := reopened.GetLegalSourceCapture(context.Background(), admin, result.CaptureSnapshotID)
	if err != nil || reloaded.RawSHA256 != capture.RawSHA256 || reloaded.StatusSHA256 != capture.StatusSHA256 {
		t.Fatalf("capture did not persist across store reopen: raw=%q status=%q err=%v", reloaded.RawSHA256, reloaded.StatusSHA256, err)
	}
	var captureCount, draftCount, bindingCount int
	if err := reopened.pool.QueryRow(context.Background(), `SELECT count(*) FROM legal_source_captures WHERE tenant_id=$1 AND capture_snapshot_id=$2`, admin.TenantID, result.CaptureSnapshotID).Scan(&captureCount); err != nil {
		t.Fatal(err)
	}
	if err := reopened.pool.QueryRow(context.Background(), `SELECT count(*) FROM governed_knowledge_versions v JOIN governed_knowledge_state s USING (tenant_id,knowledge_id,version_id) WHERE v.tenant_id=$1 AND v.legal_capture_snapshot_id=$2 AND v.kind='legal' AND s.status='draft'`, admin.TenantID, result.CaptureSnapshotID).Scan(&draftCount); err != nil {
		t.Fatal(err)
	}
	if err := reopened.pool.QueryRow(context.Background(), `SELECT count(*) FROM governed_policy_rule_bindings WHERE tenant_id=$1 AND knowledge_id LIKE 'legal-civil-code-%'`, admin.TenantID).Scan(&bindingCount); err != nil {
		t.Fatal(err)
	}
	if captureCount != 1 || draftCount != 5 || bindingCount != 0 {
		t.Fatalf("capture/draft/binding rows=%d/%d/%d, want 1/5/0", captureCount, draftCount, bindingCount)
	}
}

func TestLegalImportRejectsChangingExistingKnowledgeFamilyKindAtomically(t *testing.T) {
	assetDir := os.Getenv("LEGALNORM_ASSET_DIR")
	if assetDir == "" {
		t.Fatal("LEGALNORM_ASSET_DIR must point to the external official legal-sources directory")
	}
	for _, existingKind := range []string{"policy", "guidance"} {
		t.Run(existingKind, func(t *testing.T) {
			st, err := OpenTest(t.TempDir(), os.Getenv("TEST_DATABASE_URL"))
			if err != nil {
				t.Fatalf("isolated PostgreSQL test database unavailable: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			admin, ok := accounts.ByID("demo-admin")
			if !ok {
				t.Fatal("test administrator missing")
			}
			manifest := governedPackManifest(t, accounts.Tenant(admin), "91", "200")
			pack, err := st.CreateRulePack(admin, manifest)
			if err != nil {
				t.Fatalf("create test rule pack: %v", err)
			}
			prior := knowledgeFixtureContent(pack, "legal-civil-code-496", "legacy-policy-v1", existingKind)
			if _, err := st.CreateGovernedKnowledge(admin, CreateGovernedKnowledgeInput{Content: prior, IdempotencyKey: "prior-" + existingKind}); err != nil {
				t.Fatalf("create existing %s family member: %v", existingKind, err)
			}
			input := legalSourceImportFixture(t, assetDir, "S1_civil_code_contract")
			input.IdempotencyKey = "conflict-" + existingKind
			_, err = st.ImportLegalSource(context.Background(), admin, input)
			requireKnowledgeFault(t, err, 409)
			var captures, legalVersions int
			if err := st.pool.QueryRow(context.Background(), `SELECT count(*) FROM legal_source_captures WHERE tenant_id=$1 AND idempotency_key=$2`, accounts.Tenant(admin), input.IdempotencyKey).Scan(&captures); err != nil {
				t.Fatal(err)
			}
			if err := st.pool.QueryRow(context.Background(), `SELECT count(*) FROM governed_knowledge_versions WHERE tenant_id=$1 AND legal_capture_snapshot_id IS NOT NULL AND knowledge_id LIKE 'legal-civil-code-%'`, accounts.Tenant(admin)).Scan(&legalVersions); err != nil {
				t.Fatal(err)
			}
			if captures != 0 || legalVersions != 0 {
				t.Fatalf("conflicting import left capture/version rows: %d/%d", captures, legalVersions)
			}
		})
	}
}

func TestLegalOfficialBaselineImportsAndPublishesAllTwentyFiveArticles(t *testing.T) {
	assetDir := os.Getenv("LEGALNORM_ASSET_DIR")
	if assetDir == "" {
		t.Fatal("LEGALNORM_ASSET_DIR must point to the external official legal-sources directory")
	}
	st, err := OpenTest(t.TempDir(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("isolated PostgreSQL test database unavailable: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("test administrator missing")
	}
	snapshotIDs := []string{
		"S1_civil_code_contract", "S2_tender_law_2017", "S3_government_procurement_law_2014",
		"S4_sme_payment_regulation_2025", "S5_tender_implementing_regulation_2019",
	}
	var imported []LegalSourceImportResult
	articleCount := 0
	for _, snapshotID := range snapshotIDs {
		input := legalSourceImportFixture(t, assetDir, snapshotID)
		result, err := st.ImportLegalSource(context.Background(), admin, input)
		if err != nil {
			t.Fatalf("import official source %s: %v", snapshotID, err)
		}
		if result.Provenance != "verified_official_capture" || len(result.Articles) != len(input.Articles) || len(result.Articles) == 0 {
			t.Fatalf("source %s import incomplete: result articles=%d input=%d", snapshotID, len(result.Articles), len(input.Articles))
		}
		capture, err := st.GetLegalSourceCapture(context.Background(), admin, result.CaptureSnapshotID)
		if err != nil {
			t.Fatalf("read exact capture %s: %v", snapshotID, err)
		}
		if capture.SourceID != input.Capture.SourceID || capture.NormVersion != input.Capture.VersionID ||
			capture.RawSHA256 != result.RawSHA256 || capture.StatusSHA256 != result.StatusSHA256 {
			t.Fatalf("capture identity or response hashes differ for %s", snapshotID)
		}
		for _, ref := range result.Articles {
			version, err := st.GetGovernedKnowledgeVersion(admin, ref.KnowledgeID, ref.VersionID)
			if err != nil {
				t.Fatalf("read exact legal article %s: %v", ref.KnowledgeID, err)
			}
			if version.Content.Kind != "legal" || version.Status != "draft" || version.Content.LegalMetadata.SourceSnapshotID != result.CaptureSnapshotID {
				t.Fatalf("imported article not bound to its immutable source capture: %+v", version)
			}
			if version.Ref.ContentSHA256 != ref.ContentSHA256 {
				t.Fatalf("article %s canonical content hash changed after import", ref.KnowledgeID)
			}
			for _, action := range []string{"submit", "validate", "approve", "publish"} {
				command := knowledgeCommand(version, action, snapshotID+"-"+ref.KnowledgeID+"-"+action)
				command.CrossCaseReviewed = true
				version, err = st.GovernedKnowledgeCommand(admin, command)
				if err != nil {
					t.Fatalf("govern %s article %s: %v", action, ref.KnowledgeID, err)
				}
			}
			if version.Status != "published" || version.Content.LegalMetadata.LegalReviewStatus != "NOT_RUN" || version.Content.LegalMetadata.Reviewer != nil {
				t.Fatalf("governance fabricated legal review for %s: %+v", ref.KnowledgeID, version.Content.LegalMetadata)
			}
			articleCount++
		}
		imported = append(imported, result)
	}
	if articleCount != 25 {
		t.Fatalf("published %d official legal articles, want 25", articleCount)
	}
	var boundCaptures, bindingRows int
	if err := st.pool.QueryRow(context.Background(), `SELECT count(DISTINCT legal_capture_snapshot_id) FROM governed_knowledge_versions WHERE tenant_id=$1 AND kind='legal'`, accounts.Tenant(admin)).Scan(&boundCaptures); err != nil {
		t.Fatal(err)
	}
	if err := st.pool.QueryRow(context.Background(), `SELECT count(*) FROM governed_policy_rule_bindings b JOIN governed_knowledge_versions v USING (tenant_id,knowledge_id,version_id) WHERE v.tenant_id=$1 AND v.kind='legal'`, accounts.Tenant(admin)).Scan(&bindingRows); err != nil {
		t.Fatal(err)
	}
	if boundCaptures != 5 || bindingRows != 0 {
		t.Fatalf("legal capture/binding relation counts=%d/%d, want 5/0", boundCaptures, bindingRows)
	}

	// Search by source, article number, name, and body under a concrete
	// Asia/Shanghai business instant; results remain unknown in applicability.
	first := imported[1]
	article, err := st.GetGovernedKnowledgeVersion(admin, first.Articles[0].KnowledgeID, first.Articles[0].VersionID)
	if err != nil {
		t.Fatal(err)
	}
	metadata := article.Content.LegalMetadata
	search, err := st.SearchLegalKnowledgeAdmin(context.Background(), admin, LegalSearch{
		Query: metadata.ArticleLabel, SourceID: metadata.SourceID, ArticleNumber: metadata.ArticleNumber,
		BusinessAt: "2025-05-31T16:00:00Z", Limit: 10,
	})
	if err != nil || len(search.Items) != 1 || search.Items[0].Ref != article.Ref {
		t.Fatalf("admin legal search by name/source/article/business date failed: items=%d err=%v", len(search.Items), err)
	}
	bodyQuery := string([]rune(article.Content.Body)[:min(8, len([]rune(article.Content.Body)))])
	bodySearch, err := st.SearchLegalKnowledgeAdmin(context.Background(), admin, LegalSearch{Query: bodyQuery, SourceID: metadata.SourceID})
	if err != nil || len(bodySearch.Items) == 0 {
		t.Fatalf("admin legal full-text body search failed: items=%d err=%v", len(bodySearch.Items), err)
	}

	caseID, err := st.CreateCaseWithBusiness(admin, "法律依据检索验收", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatalf("create isolated legal search case: %v", err)
	}
	caseSearch, err := st.SearchLegalKnowledgeForCase(context.Background(), admin, caseID, LegalSearch{SourceID: metadata.SourceID, ArticleNumber: metadata.ArticleNumber, BusinessAt: "2025-06-01T00:00:00+08:00"})
	if err != nil || len(caseSearch.Items) != 1 || caseSearch.Items[0].Applicability != "unknown" || caseSearch.Items[0].Ref != article.Ref {
		t.Fatalf("case Read legal search did not return exact article as applicability unknown: %+v err=%v", caseSearch, err)
	}
	caseArticle, err := st.GetLegalKnowledgeForCase(context.Background(), admin, caseID, article.Ref.KnowledgeID, article.Ref.VersionID)
	if err != nil || caseArticle.Ref != article.Ref {
		t.Fatalf("case Read exact legal version failed: %+v err=%v", caseArticle.Ref, err)
	}
	caseCapture, err := st.GetLegalSourceCaptureForCase(context.Background(), admin, caseID, metadata.SourceSnapshotID)
	if err != nil || caseCapture.CaptureSnapshotID != metadata.SourceSnapshotID {
		t.Fatalf("case Read exact source snapshot failed: id=%s err=%v", caseCapture.CaptureSnapshotID, err)
	}
	if _, err := st.GetLegalKnowledgeForCase(context.Background(), admin, caseID, article.Ref.KnowledgeID, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Fatal("case legal version lookup fell back from an unknown exact version")
	}
	otherAdmin, ok := accounts.ByID("demo-tenant2-admin")
	if !ok {
		t.Fatal("second-tenant administrator missing")
	}
	if _, err := st.GetLegalSourceCapture(context.Background(), otherAdmin, metadata.SourceSnapshotID); err == nil {
		t.Fatal("foreign tenant admin read another tenant's exact legal capture")
	}
	if _, err := st.SearchLegalKnowledgeForCase(context.Background(), otherAdmin, caseID, LegalSearch{SourceID: metadata.SourceID}); err == nil {
		t.Fatal("foreign tenant user searched another tenant's case legal sources")
	}
}

func legalSourceImportFixture(t *testing.T, assetDir, snapshotID string) LegalSourceImportInput {
	t.Helper()
	type sourceRecord struct {
		SourceSnapshotID string `json:"source_snapshot_id"`
		SourceID         string `json:"source_id"`
		Title            string `json:"title"`
		VersionID        string `json:"version_id"`
		PromulgatedOn    string `json:"promulgated_on"`
		EffectiveFrom    string `json:"effective_from"`
		OfficialURL      string `json:"official_url"`
		SnapshotFile     string `json:"snapshot_file"`
		RawSHA256        string `json:"sha256_raw_bytes"`
		StatusEvidenceID string `json:"currency_status_evidence_id"`
	}
	var sources struct {
		Sources []sourceRecord `json:"sources"`
	}
	readLegalJSON(t, filepath.Join(assetDir, "articles.json"), &sources)
	var canonical struct {
		Articles []struct {
			SourceID         string `json:"source_id"`
			SourceSnapshotID string `json:"source_snapshot_id"`
			VersionID        string `json:"version_id"`
			ArticleNumber    int    `json:"article_no"`
			ArticleLabel     string `json:"article_label"`
			FullText         string `json:"full_text"`
			PromulgatedOn    string `json:"promulgated_on"`
			EffectiveFrom    string `json:"effective_from"`
			EffectiveUntil   string `json:"effective_to"`
			StatusEvidenceID string `json:"currency_status_evidence_id"`
			SHA256           string `json:"canonical_text_sha256"`
		} `json:"articles"`
	}
	canonicalPath := filepath.Join(filepath.Dir(assetDir), "p4-canonical-articles-v2.json")
	readLegalJSON(t, canonicalPath, &canonical)
	var source *sourceRecord
	for i := range sources.Sources {
		if sources.Sources[i].SourceSnapshotID == snapshotID {
			source = &sources.Sources[i]
			break
		}
	}
	if source == nil {
		t.Fatalf("source %q not found", snapshotID)
	}
	var captureManifest struct {
		Sources []struct {
			ID          string `json:"id"`
			ContentType string `json:"content_type"`
			CapturedAt  string `json:"captured_at"`
		} `json:"sources"`
	}
	readLegalJSON(t, filepath.Join(assetDir, "capture-manifest.json"), &captureManifest)
	var statusManifest struct {
		Sources []struct {
			ID         string `json:"id"`
			CapturedAt string `json:"captured_at"`
			Snapshot   string `json:"snapshot_path"`
			SHA256     string `json:"sha256_raw_bytes"`
		} `json:"sources"`
	}
	for _, name := range []string{"status-capture-manifest.json", "moj-status-capture-manifest.json"} {
		var current struct {
			Sources []struct {
				ID         string `json:"id"`
				CapturedAt string `json:"captured_at"`
				Snapshot   string `json:"snapshot_path"`
				SHA256     string `json:"sha256_raw_bytes"`
			} `json:"sources"`
		}
		readLegalJSON(t, filepath.Join(assetDir, name), &current)
		statusManifest.Sources = append(statusManifest.Sources, current.Sources...)
	}
	var sourceCapture struct{ ContentType, CapturedAt string }
	var statusCapture struct{ CapturedAt, Snapshot, SHA256 string }
	for _, item := range captureManifest.Sources {
		if item.ID == snapshotID {
			sourceCapture.ContentType = item.ContentType
			sourceCapture.CapturedAt = item.CapturedAt
		}
	}
	for _, item := range statusManifest.Sources {
		if item.ID == source.StatusEvidenceID {
			statusCapture.CapturedAt = item.CapturedAt
			statusCapture.Snapshot = item.Snapshot
			statusCapture.SHA256 = item.SHA256
		}
	}
	if sourceCapture.ContentType == "" || sourceCapture.CapturedAt == "" || statusCapture.Snapshot == "" || statusCapture.SHA256 == "" {
		t.Fatalf("source/status capture manifest incomplete for %q", snapshotID)
	}
	var articleRefs []legalnorm.ArticleRef
	var versionInputs []LegalArticleVersionInput
	for _, article := range canonical.Articles {
		if article.SourceSnapshotID != snapshotID {
			continue
		}
		articleRefs = append(articleRefs, legalnorm.ArticleRef{
			SourceID: article.SourceID, SourceSnapshotID: snapshotID, VersionID: article.VersionID,
			ArticleNumber: article.ArticleNumber, ArticleLabel: article.ArticleLabel, FullText: article.FullText,
			TextSHA256: article.SHA256, CitationSpan: legalnorm.TextSpan{Start: 0, End: len([]rune(article.ArticleLabel)), Quote: article.ArticleLabel},
			PromulgatedOn: article.PromulgatedOn, EffectiveFrom: article.EffectiveFrom, EffectiveUntil: article.EffectiveUntil,
			CurrencyStatus: legalnorm.StatusEffective, StatusEvidenceID: article.StatusEvidenceID,
		})
		versionInputs = append(versionInputs, LegalArticleVersionInput{
			ArticleNumber: article.ArticleNumber, KnowledgeID: legalKnowledgeTestID(source.SourceID, article.ArticleNumber), DisplayVersion: article.VersionID,
		})
	}
	return LegalSourceImportInput{Capture: legalnorm.CaptureInput{
		SourceID: source.SourceID, SourceSnapshotID: snapshotID, VersionID: source.VersionID, Title: source.Title,
		OfficialURL: source.OfficialURL, ContentType: sourceCapture.ContentType,
		RawBytes: readLegalBytes(t, filepath.Join(assetDir, source.SnapshotFile)), ExpectedRawSHA256: source.RawSHA256,
		SourceCapturedAt: sourceCapture.CapturedAt, PromulgatedOn: source.PromulgatedOn, EffectiveFrom: source.EffectiveFrom,
		CurrencyStatus: legalnorm.StatusEffective, StatusEvidenceID: source.StatusEvidenceID, StatusObservedAt: statusCapture.CapturedAt,
		StatusEvidenceRaw: readLegalBytes(t, filepath.Join(assetDir, statusCapture.Snapshot)), ExpectedStatusSHA256: statusCapture.SHA256,
		Articles: articleRefs,
	}, Articles: versionInputs, IdempotencyKey: "legal-baseline-" + source.SourceID}
}

func legalKnowledgeTestID(sourceID string, articleNumber int) string {
	prefixes := map[string]string{
		"CN-CIVIL-CODE":              "legal-civil-code",
		"CN-TENDER-LAW":              "legal-tender-law",
		"CN-GOV-PROCUREMENT-LAW":     "legal-gov-procurement-law",
		"CN-SME-PAYMENT-REG":         "legal-sme-payment-reg",
		"CN-TENDER-IMPLEMENTING-REG": "legal-tender-implementing-reg",
	}
	prefix := prefixes[sourceID]
	if prefix == "" {
		return "legal-unknown-source-" + strconv.Itoa(articleNumber)
	}
	return prefix + "-" + strconv.Itoa(articleNumber)
}

func readLegalJSON(t *testing.T, path string, target any) {
	t.Helper()
	raw := readLegalBytes(t, path)
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode %s: %v", filepath.Base(path), err)
	}
}

func readLegalBytes(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}
	return raw
}
