package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/legalnorm"
)

func TestLegalWireContractFixtures(t *testing.T) {
	metadata := legalWireFixtureMetadata()
	ref := KnowledgeRef{
		KnowledgeID:   "legal-wire-fixture-article-1",
		VersionID:     "1111111111111111111111111111111111111111111111111111111111111111",
		ContentSHA256: "2222222222222222222222222222222222222222222222222222222222222222",
	}
	hit := LegalKnowledgeHit{
		Ref: ref, Title: "法律知识 wire fixture（非真实条文）", Body: "fixture-only body; not a statutory quotation",
		Snippet: "fixture-only body", Summary: "wire shape fixture", Status: "published", Active: true,
		Legal: metadata,
	}
	adminSearch := LegalKnowledgePage{Items: []LegalKnowledgeHit{hit}}
	caseHit := hit
	caseHit.Applicability = "unknown"
	caseSearch := LegalKnowledgePage{Items: []LegalKnowledgeHit{caseHit}}
	legalVersion := GovernedKnowledgeVersion{
		Ref: ref,
		Content: GovernedKnowledgeContent{
			Encoding: knowledgeContentEncoding, KnowledgeID: ref.KnowledgeID, Version: "wire-fixture-v1", Kind: "legal",
			Title: hit.Title, Body: hit.Body, Summary: hit.Summary, SourceType: "official_normative_fixture",
			SourceReference: "fixture://legal-wire/source-capture-1",
			Scope:           KnowledgeScope{BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service"},
			ValidFrom:       "2021-01-01", Applicability: []KnowledgeCondition{}, Exclusions: []KnowledgeCondition{},
			Binding: PolicyRuleBinding{}, LegalMetadata: &metadata,
		},
		Status: "published", Revision: 4, ApprovedDigest: "3333333333333333333333333333333333333333333333333333333333333333",
		ApprovalActorID: "fixture-admin", ApprovalReason: "wire fixture only", CrossCaseReviewed: true,
		Active: true, PointerRevision: 2, CreatedBy: "fixture-admin",
		CreatedAt: "2026-10-31T00:00:00Z",
	}
	capture := LegalSourceCapture{
		CaptureSnapshotID: "lsc_wire_fixture_1", SourceID: "FIXTURE-LEGAL-SOURCE", NormVersion: "fixture-v1",
		NormTitle: "法律知识 wire fixture（非真实条文）", DocumentType: "law", Publisher: "fixture publisher",
		Issuer: "fixture issuer", OfficialURL: "https://fixture.invalid/legal-source", Provenance: "fixture_only",
		PromulgatedOn: "2020-01-01", EffectiveFrom: "2021-01-01", SourceCapturedAt: "2026-10-30T00:00:00Z",
		ResponseStatus: 200, MediaType: "text/html", RawResponse: []byte("<html>fixture-only</html>"),
		RawSHA256:         "4444444444444444444444444444444444444444444444444444444444444444",
		ExtractedFullText: "fixture-only body; not a statutory quotation",
		ExtractedSHA256:   "2222222222222222222222222222222222222222222222222222222222222222",
		ExtractorID:       "fixture-adapter", ExtractorVersion: "v1", CanonicalVersion: legalnorm.CanonicalTextVersion,
		StatusID: "fixture-status-1", Status: "effective", StatusRegistryURL: "https://fixture.invalid/status",
		StatusMethod: "GET", StatusObservedAt: "2026-10-30T00:00:00Z", StatusHTTPStatus: 200,
		StatusMediaType: "application/json", StatusResponse: []byte(`{"status":"fixture"}`),
		StatusSHA256:  "5555555555555555555555555555555555555555555555555555555555555555",
		StatusMapping: json.RawMessage(`{"status":"effective"}`),
		Articles: []legalnorm.VerifiedArticle{{
			Ref: legalnorm.ArticleRef{SourceID: "FIXTURE-LEGAL-SOURCE", SourceSnapshotID: "fixture-source-snapshot-1",
				VersionID: "fixture-v1", ArticleNumber: 1, ArticleLabel: "条文 1（fixture）",
				PromulgatedOn: "2020-01-01", EffectiveFrom: "2021-01-01", CurrencyStatus: legalnorm.StatusEffective,
				StatusEvidenceID: "fixture-status-1"},
			FullText:   "fixture-only body; not a statutory quotation",
			TextSHA256: "2222222222222222222222222222222222222222222222222222222222222222", Start: 0, End: 44,
		}},
		ImportedArticles: []KnowledgeRef{ref}, RequestDigest: "6666666666666666666666666666666666666666666666666666666666666666",
		IdempotencyKey: "legal-wire-fixture-idem", CreatedBy: "fixture-admin", CreatedAt: "2026-10-31T00:00:00Z",
	}
	applicabilitySet := legalApplicabilityWireFixture()
	applicabilityRecord := legalApplicabilityRecordWireFixture()
	frozenLegalKnowledge, err := sealKnowledgeSnapshot(legalReviewSnapshotFixture("2026-10-31T01:00:00Z", "acceptance_time", "", frozenLegalAuthorityFixture()))
	if err != nil {
		t.Fatal(err)
	}
	frozenLegalProjection, err := frozenLegalReportProjection(RunSnapshot{RunID: "run_wire_fixture", GovernedKnowledge: &frozenLegalKnowledge}, "run_wire_fixture", "attempt_wire_fixture")
	if err != nil {
		t.Fatal(err)
	}
	applicabilityBuyerFact := *applicabilitySet.Facts[0].Fact
	historyCursor := "cursor_wire_fixture_opaque"
	applicabilityHistory := LegalApplicabilityFactHistory{CaseID: "case_wire_fixture", Dimension: "buyer_kind", CurrentVersion: 2,
		Items: []LegalApplicabilityFactVersion{applicabilityBuyerFact}, NextCursor: &historyCursor, CanManage: true}
	recordRequest := struct {
		Method  string                      `json:"method"`
		Path    string                      `json:"path"`
		Headers map[string]string           `json:"headers"`
		Body    LegalApplicabilityFactInput `json:"body"`
	}{
		Method: "PUT", Path: "/api/cases/case_wire_fixture/legal-applicability-facts/buyer_kind",
		Headers: map[string]string{"Idempotency-Key": "wire-fact-buyer-1"},
		Body: LegalApplicabilityFactInput{
			ExpectedVersion: 0, IdempotencyKey: "wire-fact-buyer-1", Dimension: "buyer_kind", Status: "established",
			Value: "enterprise", TemporalBasis: "historical_interval", ValidFrom: "2025-05-01T00:00:00.123456789+08:00",
			ValidUntil: "2025-06-01T00:00:00.123456789+08:00", BasisNote: "fixture-only evidence; not a legal quotation",
			BasisRefs: json.RawMessage(legalApplicabilityWireBasis()),
			Party:     LegalApplicabilityPartyPin{Side: "buyer", BindingID: "binding_fixture", BindingVersion: 2, SubjectID: "subject_fixture", SubjectVersion: 3, ScopeNote: "fixture buyer role"},
		},
	}
	fixtures := []struct {
		name  string
		value any
	}{
		{name: "admin-search.json", value: adminSearch},
		{name: "case-search.json", value: caseSearch},
		{name: "legal-version.json", value: legalVersion},
		{name: "source-capture.json", value: capture},
		{name: "applicability-fact-set.json", value: applicabilitySet},
		{name: "applicability-record-success.json", value: applicabilityRecord},
		{name: "applicability-record-replay.json", value: applicabilityRecord},
		{name: "applicability-record-request.json", value: recordRequest},
		{name: "applicability-history.json", value: applicabilityHistory},
		{name: "frozen-legal-authorities.json", value: frozenLegalProjection},
	}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			encoded, err := json.MarshalIndent(fixture.value, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			encoded = append(encoded, '\n')
			path := filepath.Join("../../testdata/contracts/legal", fixture.name)
			if os.Getenv("UPDATE_LEGAL_WIRE_FIXTURES") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, encoded, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			actual, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, encoded) {
				t.Errorf("fixture %s differs from Go wire serialization", fixture.name)
			}
		})
	}

	versionJSON, err := json.Marshal(legalVersion.Content)
	if err != nil {
		t.Fatal(err)
	}
	var versionFields map[string]json.RawMessage
	if err := json.Unmarshal(versionJSON, &versionFields); err != nil {
		t.Fatal(err)
	}
	if string(versionFields["legal_metadata"]) == "null" || string(versionFields["binding"]) == "null" {
		t.Fatalf("legal metadata and legacy zero binding must remain explicit objects: %s", versionJSON)
	}
	if _, exists := versionFields["policy_refs"]; exists {
		t.Fatalf("nil policy_refs must remain omitted for legal wire content: %s", versionJSON)
	}
	var metadataFields map[string]json.RawMessage
	if err := json.Unmarshal(versionFields["legal_metadata"], &metadataFields); err != nil {
		t.Fatal(err)
	}
	if string(metadataFields["reviewer"]) != "null" {
		t.Fatalf("nullable reviewer wire field must serialize as null: %s", versionFields["legal_metadata"])
	}
	if _, exists := metadataFields["effective_until"]; exists {
		t.Fatalf("empty effective_until must remain omitted: %s", versionFields["legal_metadata"])
	}
}

func legalApplicabilityWireBasis() json.RawMessage {
	const quote = "fixture evidence text"
	quoteDigest := sha256.Sum256([]byte(quote))
	items := []map[string]any{{
		"kind": "material_anchor", "material_id": "material_fixture", "material_version": 1,
		"document_id": "document_fixture", "parsed_document_id": "parsed_fixture", "filename": "wire-fixture.txt",
		"start": 0, "end": len([]rune(quote)), "quote": quote, "quote_digest": hex.EncodeToString(quoteDigest[:]),
	}}
	encoded, err := json.Marshal(items)
	if err != nil {
		panic(err)
	}
	canonical, err := canonicalRawJSON(encoded)
	if err != nil {
		panic(err)
	}
	return canonical
}

func legalApplicabilityWireFixture() LegalApplicabilityFactSet {
	created := time.Date(2026, 10, 31, 0, 0, 0, 123456000, time.UTC)
	validFrom := time.Date(2025, 5, 1, 0, 0, 0, 123456000, time.UTC)
	validUntil := time.Date(2025, 6, 1, 0, 0, 0, 123456000, time.UTC)
	obtained := time.Date(2025, 5, 2, 0, 0, 0, 0, time.UTC)
	basis := legalApplicabilityWireBasis()
	basisDigest := sha256Bytes(basis)
	makeFact := func(id, dimension, value string, version int64) *LegalApplicabilityFactVersion {
		return &LegalApplicabilityFactVersion{
			TenantID: "tenant_wire_fixture", OrgID: "org_wire_fixture", CaseID: "case_wire_fixture",
			FactID: id, Dimension: dimension, Version: version, Status: "established", Value: value,
			TemporalBasis: "historical_interval", ValidFrom: validFrom, ValidUntil: &validUntil,
			BasisObtainedAt: &obtained, BasisNote: "fixture-only evidence; not a legal quotation",
			BasisRefs: append(json.RawMessage(nil), basis...), BasisSHA256: basisDigest, CreatedAt: created,
		}
	}
	partyFact := makeFact("laf_wire_buyer_kind", "buyer_kind", "enterprise", 2)
	partyFact.Party = LegalApplicabilityPartyPin{
		Side: "buyer", BindingID: "binding_fixture", BindingVersion: 2,
		SubjectID: "subject_fixture", SubjectVersion: 3, ScopeNote: "fixture buyer role",
	}
	nonpartyFact := makeFact("laf_wire_project_kind", "project_kind", "standalone_services", 1)
	return LegalApplicabilityFactSet{
		BusinessAt:       time.Date(2025, 5, 20, 12, 0, 0, 0, time.UTC),
		EvidenceCutoffAt: time.Date(2026, 10, 31, 0, 0, 0, 123456000, time.UTC),
		Facts: []LegalApplicabilityResolvedFact{
			{Dimension: "buyer_kind", Status: "established", Value: "enterprise", Fact: partyFact},
			{Dimension: "project_kind", Status: "established", Value: "standalone_services", Fact: nonpartyFact},
			{Dimension: "seller_kind", Status: "unknown", UnknownReason: "missing_fact"},
		},
	}
}

func legalApplicabilityRecordWireFixture() LegalApplicabilityFactVersion {
	created := time.Date(2026, 10, 31, 0, 0, 0, 123456000, time.UTC)
	validFrom := time.Date(2025, 4, 30, 16, 0, 0, 123456000, time.UTC)
	validUntil := time.Date(2025, 5, 31, 16, 0, 0, 123456000, time.UTC)
	obtained := time.Date(2025, 5, 2, 0, 0, 0, 0, time.UTC)
	return LegalApplicabilityFactVersion{
		TenantID: "tenant_wire_fixture", OrgID: "org_wire_fixture", CaseID: "case_wire_fixture",
		FactID: "laf_wire_buyer_kind", Dimension: "buyer_kind", Version: 1,
		Status: "established", Value: "enterprise", TemporalBasis: "historical_interval",
		ValidFrom: validFrom, ValidUntil: &validUntil, BasisObtainedAt: &obtained,
		BasisNote: "fixture-only evidence; not a legal quotation", BasisRefs: legalApplicabilityWireBasis(),
		BasisSHA256: sha256Bytes(legalApplicabilityWireBasis()), Party: LegalApplicabilityPartyPin{
			Side: "buyer", BindingID: "binding_fixture", BindingVersion: 2,
			SubjectID: "subject_fixture", SubjectVersion: 3, ScopeNote: "fixture buyer role",
		},
		RequestDigest: strings.Repeat("9", 64), IdempotencyKey: "wire-fact-buyer-1",
		ActorUserID: "fixture-user", ActorIssuer: "fixture-issuer", ActorSubject: "fixture-subject", CreatedAt: created,
	}
}

func legalWireFixtureMetadata() LegalMetadata {
	return LegalMetadata{
		SourceID: "FIXTURE-LEGAL-SOURCE", NormTitle: "法律知识 wire fixture（非真实条文）", DocumentType: "law",
		NormVersion: "fixture-v1", ArticleNumber: 1, ArticleLabel: "条文 1（fixture）", Issuer: "fixture issuer",
		Publisher: "fixture publisher", OfficialURL: "https://fixture.invalid/legal-source",
		PromulgatedOn: "2020-01-01", EffectiveFrom: "2021-01-01", OfficialStatusObservationID: "fixture-status-1",
		OfficialStatus: "effective", StatusRegistryURL: "https://fixture.invalid/status",
		StatusObservedAt: "2026-10-30T00:00:00Z", StatusEvidenceSHA256: "5555555555555555555555555555555555555555555555555555555555555555",
		StatusResponseStatus: 200, StatusContentType: "application/json", SourceSnapshotID: "lsc_wire_fixture_1",
		SourceProvenance: "fixture_only", SourceCapturedAt: "2026-10-30T00:00:00Z", SourceResponseStatus: 200,
		SourceContentType: "text/html", SourceSHA256: "4444444444444444444444444444444444444444444444444444444444444444",
		NormTextSHA256:       "2222222222222222222222222222222222222222222222222222222222222222",
		CanonicalTextVersion: legalnorm.CanonicalTextVersion, SourceTextStart: 0, SourceTextEnd: 44,
		ExtractorID: "fixture-adapter", ExtractorVersion: "v1", TextCheckStatus: "PASS",
		LegalReviewStatus: "NOT_RUN", Reviewer: nil,
	}
}
