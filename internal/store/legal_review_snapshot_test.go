package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"contractaudit/internal/rulepack"
)

func TestLegalReuseFingerprintIgnoresAcceptanceSampleButPinsExplicitCutoff(t *testing.T) {
	authority := frozenLegalAuthorityFixture()
	first := legalReviewSnapshotFixture("2026-10-31T01:00:00Z", "acceptance_time", "", authority)
	second := legalReviewSnapshotFixture("2026-10-31T01:00:03Z", "acceptance_time", "", authority)
	first, err := sealKnowledgeSnapshot(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err = sealKnowledgeSnapshot(second)
	if err != nil {
		t.Fatal(err)
	}
	if first.SnapshotSHA256 == second.SnapshotSHA256 || first.LegalReuseFingerprint != second.LegalReuseFingerprint || !sameKnowledgeSnapshotIdentity(&first, &second) {
		t.Fatalf("acceptance timestamp changed unchanged-dependency reuse identity: first=%+v second=%+v", first, second)
	}
	firstExplicit := legalReviewSnapshotFixture("2026-10-31T01:00:00Z", "explicit", "2026-10-31T01:00:00Z", authority)
	secondExplicit := legalReviewSnapshotFixture("2026-10-31T01:00:03Z", "explicit", "2026-10-31T01:00:03Z", authority)
	firstExplicit, err = sealKnowledgeSnapshot(firstExplicit)
	if err != nil {
		t.Fatal(err)
	}
	secondExplicit, err = sealKnowledgeSnapshot(secondExplicit)
	if err != nil {
		t.Fatal(err)
	}
	if firstExplicit.LegalReuseFingerprint == secondExplicit.LegalReuseFingerprint || sameKnowledgeSnapshotIdentity(&firstExplicit, &secondExplicit) {
		t.Fatal("a different explicit cutoff reused the prior legal snapshot")
	}
}

func TestFrozenLegalCitationIDIsServerDerivedAndPinsRuleContext(t *testing.T) {
	authority := frozenLegalAuthorityFixture()
	first, err := frozenLegalCitationID(authority)
	if err != nil || !isLegalCitationID(first) {
		t.Fatalf("citation identity is not a canonical lowercase SHA256: %q err=%v", first, err)
	}
	second, err := frozenLegalCitationID(authority)
	if err != nil || second != first {
		t.Fatalf("same frozen reference produced an unstable citation identity: first=%q second=%q err=%v", first, second, err)
	}
	changedRule := authority
	changedRule.RuleID += "-other"
	third, err := frozenLegalCitationID(changedRule)
	if err != nil || third == first {
		t.Fatalf("citation identity did not bind its normative rule context: first=%q third=%q err=%v", first, third, err)
	}
	changedReference := authority
	changedReference.ExactReference = cloneRuleLegalReference(authority.ExactReference)
	changedReference.ExactReference.QuoteSpan.End++
	fourth, err := frozenLegalCitationID(changedReference)
	if err != nil || fourth == first {
		t.Fatalf("citation identity did not bind its exact reference: first=%q fourth=%q err=%v", first, fourth, err)
	}
	if err := prepareLegalKnowledgeReuse(&ReviewKnowledgeSnapshot{EvidenceCutoffAt: "2026-10-31T01:00:00Z", EvidenceCutoffMode: "acceptance_time",
		LegalAuthorities: []FrozenLegalAuthority{authority}}); err != nil {
		t.Fatalf("seal did not derive citation identity for a new frozen item: %v", err)
	}
	for _, invalid := range []string{"", strings.Repeat("A", 64), strings.Repeat("a", 63), strings.Repeat("g", 64)} {
		if isLegalCitationID(invalid) {
			t.Fatalf("invalid citation ID %q passed the wire validation", invalid)
		}
	}
	authority.CitationID = strings.Repeat("0", 64)
	if _, err := sealKnowledgeSnapshot(legalReviewSnapshotFixture("2026-10-31T01:00:00Z", "acceptance_time", "", authority)); err == nil {
		t.Fatal("snapshot seal accepted a caller-selected citation identity")
	}
}

func TestCanonicalizeLegalCutoffInputNormalizesZoneAndMicrosecondPrecision(t *testing.T) {
	got, err := canonicalizeLegalCutoffInput("2026-10-31T09:00:00.123456789+08:00")
	if err != nil || got != "2026-10-31T01:00:00.123456Z" {
		t.Fatalf("explicit cutoff was not canonicalized to UTC microseconds: %q err=%v", got, err)
	}
	if got, err := canonicalizeLegalCutoffInput(""); err != nil || got != "" {
		t.Fatalf("omitted cutoff did not preserve server-sampled intent: %q err=%v", got, err)
	}
	if _, err := canonicalizeLegalCutoffInput("not-a-time"); err == nil {
		t.Fatal("invalid explicit cutoff was accepted")
	}
}

func TestLegalReuseFingerprintIntegrityAndBaseGovernanceDependencies(t *testing.T) {
	base, err := sealKnowledgeSnapshot(legalReviewSnapshotFixture("2026-10-31T01:00:00Z", "acceptance_time", "", frozenLegalAuthorityFixture()))
	if err != nil {
		t.Fatal(err)
	}
	if !validLegalReuseFingerprint(&base) {
		t.Fatal("freshly sealed legal reuse fingerprint did not verify")
	}
	tampered := base
	tampered.LegalAuthorities = append([]FrozenLegalAuthority(nil), base.LegalAuthorities...)
	article := *base.LegalAuthorities[0].Article
	article.CanonicalBody += "篡改"
	tampered.LegalAuthorities[0].Article = &article
	if _, err := sealKnowledgeSnapshot(tampered); err == nil {
		t.Fatal("snapshot seal silently replaced a stale semantic fingerprint")
	}
	changedPolicy := base
	changedPolicy.RequiredPolicies = []GovernedKnowledgeSelection{{Ref: KnowledgeRef{KnowledgeID: "policy-a", VersionID: hashFixture("v1"), ContentSHA256: hashFixture("body")}, Summary: "changed required policy"}}
	changedPolicy, err = sealKnowledgeSnapshot(changedPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if sameKnowledgeSnapshotIdentity(&base, &changedPolicy) {
		t.Fatal("equal legal fingerprint hid a changed mandatory governance dependency")
	}
	changedScope := base
	changedScope.Scope.CaseID = "case-other"
	changedScope, err = sealKnowledgeSnapshot(changedScope)
	if err != nil {
		t.Fatal(err)
	}
	if sameKnowledgeSnapshotIdentity(&base, &changedScope) {
		t.Fatal("equal legal fingerprint hid a changed governance scope")
	}
}

func TestLegacyGovernedSnapshotOmitsEveryAUTH03Field(t *testing.T) {
	snapshot := ReviewKnowledgeSnapshot{Encoding: "review-knowledge-snapshot/v1", BusinessAt: "2026-10-31T01:00:00Z",
		Scope:                 KnowledgeScope{BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service"},
		AuthorizationRevision: hashFixture("authorization"), RequiredPolicies: []GovernedKnowledgeSelection{},
		OptionalCandidates: []GovernedKnowledgeSelection{}, Excluded: []GovernedKnowledgeExclusion{}}
	sealed, err := sealKnowledgeSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(sealed)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"evidence_cutoff_at", "evidence_cutoff_mode", "explicit_evidence_cutoff_at", "legal_authorities", "legal_reuse_fingerprint"} {
		if strings.Contains(string(raw), field) {
			t.Fatalf("ordinary governed snapshot unexpectedly serialized AUTH03 field %q: %s", field, raw)
		}
	}
}

func TestLegacyFrozenAuthorityPathRequiresAnOldUnextendedNormativeSnapshot(t *testing.T) {
	var manifest map[string]any
	if err := json.Unmarshal(governedPackManifest(t, "tenant-a", "1", "100"), &manifest); err != nil {
		t.Fatal(err)
	}
	for _, item := range manifest["rules"].([]any) {
		rule := item.(map[string]any)
		rule["authority_sources"] = []any{map[string]any{"id": "old-norm", "version": "2020", "kind": "normative"}}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := rulepack.Parse(raw)
	if err != nil {
		t.Fatalf("parse old normative pack fixture: %v", err)
	}
	pack, err = pack.Validate()
	if err != nil {
		t.Fatal(err)
	}
	pack, err = pack.Approve("legacy-fixture-approver", pack.Hash())
	if err != nil {
		t.Fatal(err)
	}
	pack, err = pack.Publish()
	if err != nil {
		t.Fatal(err)
	}
	legacy := RunSnapshot{RunID: "run-old", GovernedKnowledge: &ReviewKnowledgeSnapshot{Encoding: "review-knowledge-snapshot/v1"}}
	if !legacyFrozenAuthoritySemantics(legacy, pack) {
		t.Fatal("exact old snapshot without AUTH03 extension did not select explicit legacy-frozen semantics")
	}
	newAccepted := legacy
	newAccepted.GovernedKnowledge = &ReviewKnowledgeSnapshot{LegalAuthorities: []FrozenLegalAuthority{{RuleID: "rule-a", AuthoritySourceID: "old-norm",
		AuthoritySourceVersion: "2020", LegacyRuleGap: true, Outcome: "unknown", OutcomeReason: "legal_reference_missing"}}}
	if legacyFrozenAuthoritySemantics(newAccepted, pack) {
		t.Fatal("new accepted normative gap was misclassified as a pre-AUTH03 attempt")
	}
	if legacyFrozenAuthoritySemantics(RunSnapshot{RunID: "run-missing-snapshot"}, pack) {
		t.Fatal("missing frozen run snapshot selected legacy-frozen semantics")
	}
}

func TestLegalAuthorityApplicabilityCombinesReferencesConjunctively(t *testing.T) {
	firstFact := legalFactVersion(1, "historical_interval", "2025-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "enterprise", legalTime("2025-01-01T00:00:00Z"), "2025-01-01T00:00:00Z")
	secondFact := legalFactVersion(1, "historical_interval", "2025-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "fiscal", legalTime("2025-01-01T00:00:00Z"), "2025-01-01T00:00:00Z")
	firstFact.Dimension, secondFact.Dimension = "buyer_kind", "funding_kind"
	resolved := LegalApplicabilityFactSet{Facts: []LegalApplicabilityResolvedFact{
		{Dimension: "buyer_kind", Status: "established", Value: "enterprise", Fact: &firstFact},
		{Dimension: "funding_kind", Status: "unknown", UnknownReason: "insufficient_evidence", Fact: &secondFact},
	}}
	refs := []rulepack.LegalAuthorityReference{
		{Applicability: rulepack.LegalApplicability{AllOf: []rulepack.LegalApplicabilityCondition{{Field: "buyer_kind", OneOf: []string{"enterprise"}}}}},
		{Applicability: rulepack.LegalApplicability{AllOf: []rulepack.LegalApplicabilityCondition{{Field: "funding_kind", OneOf: []string{"fiscal"}}}}},
	}
	evaluation, err := evaluateLegalAuthorityApplicability(refs, resolved)
	if err != nil || evaluation.Outcome != "unknown" || evaluation.OutcomeReason != "insufficient_evidence" || len(evaluation.SelectedFactVersions) != 2 || len(evaluation.MissingDependencies) != 1 {
		t.Fatalf("unknown condition was converted into an ordinary conclusion: evaluation=%+v err=%v", evaluation, err)
	}
	resolved.Facts[1] = LegalApplicabilityResolvedFact{Dimension: "funding_kind", Status: "established", Value: "private", Fact: &secondFact}
	evaluation, err = evaluateLegalAuthorityApplicability(refs, resolved)
	if err != nil || evaluation.Outcome != "not_applicable" || evaluation.OutcomeReason != "condition_not_satisfied" {
		t.Fatalf("a known failed all_of condition was not explicitly not_applicable: %+v err=%v", evaluation, err)
	}
	resolved.Facts = resolved.Facts[:1]
	evaluation, err = evaluateLegalAuthorityApplicability(refs, resolved)
	if err != nil || evaluation.Outcome != "unknown" || evaluation.OutcomeReason != "missing_fact" || len(evaluation.MissingDependencies) != 1 {
		t.Fatalf("missing fact was inferred instead of retained as a dependency gap: %+v err=%v", evaluation, err)
	}
}

func TestFrozenRulepackAuthorityGatesAggregateMultipleReferencesAsAllOf(t *testing.T) {
	first := frozenLegalAuthorityFixture()
	second := frozenLegalAuthorityFixture()
	second.ExactReference = cloneRuleLegalReference(first.ExactReference)
	second.ExactReference.AuthoritySourceID = first.AuthoritySourceID
	second.ExactReference.SourceID = first.AuthoritySourceID
	second.ExactReference.NormVersion = first.AuthoritySourceVersion
	second.ExactReference.QuoteSpan = rulepack.LegalAuthorityQuoteSpan{Start: 8, End: 12, Quote: "only"}
	second.Article = cloneFrozenLegalArticle(first.Article)
	second.Article.QuoteSpan = second.ExactReference.QuoteSpan
	second.RuleID = first.RuleID
	second.Outcome, second.OutcomeReason = "unknown", "missing_fact"
	makeSnapshot := func(secondOutcome string) ReviewKnowledgeSnapshot {
		candidate := legalReviewSnapshotFixture("2026-10-31T01:00:00Z", "acceptance_time", "", first)
		candidate.LegalAuthorities = append(candidate.LegalAuthorities, second)
		candidate.LegalAuthorities[1].Outcome = secondOutcome
		candidate.LegalAuthorities[1].OutcomeReason = "missing_fact"
		if secondOutcome == "not_applicable" {
			candidate.LegalAuthorities[1].OutcomeReason = "condition_not_satisfied"
		}
		return candidate
	}
	snapshot, err := sealKnowledgeSnapshot(makeSnapshot("unknown"))
	if err != nil {
		t.Fatal(err)
	}
	gates, err := rulepackAuthorityGates(&snapshot)
	if err != nil || len(gates) != 1 || gates[0].Outcome != "unknown" || gates[0].Reason != "missing_fact" {
		t.Fatalf("per-reference unknown did not block combined authority gate: gates=%+v err=%v", gates, err)
	}
	snapshot, err = sealKnowledgeSnapshot(makeSnapshot("not_applicable"))
	if err != nil {
		t.Fatal(err)
	}
	gates, err = rulepackAuthorityGates(&snapshot)
	if err != nil || len(gates) != 1 || gates[0].Outcome != "not_applicable" {
		t.Fatalf("failed all_of authority condition did not aggregate to not_applicable: gates=%+v err=%v", gates, err)
	}
	unknownFact, notApplicableFact := first, second
	unknownFact.Outcome, unknownFact.OutcomeReason = "unknown", "missing_fact"
	notApplicableFact.Outcome, notApplicableFact.OutcomeReason = "not_applicable", "condition_not_satisfied"
	for _, ordered := range [][]FrozenLegalAuthority{{unknownFact, notApplicableFact}, {notApplicableFact, unknownFact}} {
		candidate := legalReviewSnapshotFixture("2026-10-31T01:00:00Z", "acceptance_time", "", ordered[0])
		candidate.LegalAuthorities = append([]FrozenLegalAuthority(nil), ordered...)
		candidate, sealErr := sealKnowledgeSnapshot(candidate)
		if sealErr != nil {
			t.Fatal(sealErr)
		}
		got, gateErr := rulepackAuthorityGates(&candidate)
		if gateErr != nil || len(got) != 1 || got[0].Outcome != "not_applicable" {
			t.Fatalf("bound source facts did not aggregate all_of independent of item order: authorities=%+v gates=%+v err=%v", ordered, got, gateErr)
		}
	}
	missingReference := first
	missingReference.ExactReference, missingReference.Article = nil, nil
	missingReference.LegacyRuleGap = true
	missingReference.Outcome, missingReference.OutcomeReason = "unknown", "legal_reference_missing"
	missingReference.Conditions = []FrozenLegalApplicabilityCondition{}
	missingReference.SelectedFactVersions = []LegalApplicabilityFactVersion{}
	missingReference.MissingDependencies = []FrozenLegalDependencyGap{{Kind: "authority", Reason: "legal_reference_missing"}}
	makeMissingReferenceSnapshot := func(ordered []FrozenLegalAuthority) ReviewKnowledgeSnapshot {
		candidate := legalReviewSnapshotFixture("2026-10-31T01:00:00Z", "acceptance_time", "", ordered[0])
		candidate.LegalAuthorities = append([]FrozenLegalAuthority(nil), ordered...)
		return candidate
	}
	for _, ordered := range [][]FrozenLegalAuthority{{second, missingReference}, {missingReference, second}} {
		candidate, sealErr := sealKnowledgeSnapshot(makeMissingReferenceSnapshot(ordered))
		if sealErr != nil {
			t.Fatal(sealErr)
		}
		got, gateErr := rulepackAuthorityGates(&candidate)
		if gateErr != nil || len(got) != 1 || got[0].Outcome != "unknown" || !got[0].ReferenceMissing || got[0].Reason != "legal_reference_missing" {
			t.Fatalf("a missing citation was converted into a not-applicable legal rule: gates=%+v err=%v", got, gateErr)
		}
	}
}

func cloneRuleLegalReference(ref *rulepack.LegalAuthorityReference) *rulepack.LegalAuthorityReference {
	if ref == nil {
		return nil
	}
	copy := *ref
	copy.Applicability.AllOf = append([]rulepack.LegalApplicabilityCondition(nil), ref.Applicability.AllOf...)
	for i := range copy.Applicability.AllOf {
		copy.Applicability.AllOf[i].OneOf = append([]string(nil), ref.Applicability.AllOf[i].OneOf...)
	}
	return &copy
}

func cloneFrozenLegalArticle(article *FrozenLegalArticle) *FrozenLegalArticle {
	if article == nil {
		return nil
	}
	copy := *article
	return &copy
}

func legalReviewSnapshotFixture(cutoff, mode, explicit string, authority FrozenLegalAuthority) ReviewKnowledgeSnapshot {
	return ReviewKnowledgeSnapshot{Encoding: "review-knowledge-snapshot/v1", BusinessAt: "2025-06-01T00:00:00Z",
		Scope:                 KnowledgeScope{OrgID: "org-a", CaseID: "case-a", BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service"},
		AuthorizationRevision: hashFixture("authorization"), RulePack: GovernedRulePackRef{PackID: "pack-a", Version: "v1",
			ContentHash: hashFixture("pack"), SnapshotDigest: hashFixture("pack-snapshot"), CompilerRef: "compiler-v1", PointerRevision: 1},
		RequiredPolicies: []GovernedKnowledgeSelection{}, OptionalCandidates: []GovernedKnowledgeSelection{}, Excluded: []GovernedKnowledgeExclusion{},
		EvidenceCutoffAt: cutoff, EvidenceCutoffMode: mode, ExplicitEvidenceCutoffAt: explicit, LegalAuthorities: []FrozenLegalAuthority{authority}}
}

func frozenLegalAuthorityFixture() FrozenLegalAuthority {
	body := "fixture-only body; not a legal quotation"
	bodySum := sha256.Sum256([]byte(body))
	bodyHash := hex.EncodeToString(bodySum[:])
	contentHash := hashFixture("governed-content")
	ref := rulepack.LegalAuthorityReference{
		AuthoritySourceID: "AUTH-CIVIL", KnowledgeID: "legal-article-496", VersionID: hashFixture("version"), ContentSHA256: contentHash,
		SourceID: "AUTH-CIVIL", SourceSnapshotID: "capture-a", NormTitle: "法律结构 fixture", NormVersion: "v1", ArticleNumber: 496,
		DocumentType: "law", SourceSHA256: hashFixture("raw"), ArticleTextSHA256: bodyHash, EffectiveFrom: "2021-01-01",
		QuoteSpan:     rulepack.LegalAuthorityQuoteSpan{Start: 0, End: 7, Quote: "fixture"},
		Applicability: rulepack.LegalApplicability{AllOf: []rulepack.LegalApplicabilityCondition{{Field: "buyer_kind", OneOf: []string{"enterprise"}}}},
	}
	articleLabel := "条文 496（fixture）"
	source := FrozenLegalSourceProvenance{CaptureSnapshotID: "capture-a", Provenance: "fixture_only", Publisher: "fixture publisher", Issuer: "fixture issuer",
		OfficialURL: "https://fixture.invalid/source", RawResponseSHA256: hashFixture("raw"), ExtractedFullTextSHA256: hashFixture("norm-text"),
		ExtractorID: "fixture-adapter", ExtractorVersion: "v1", CanonicalizationVersion: "fixture-v1", CapturedAt: "2026-10-30T00:00:00Z", ResponseStatus: 200}
	status := FrozenLegalStatusProvenance{CaptureSnapshotID: "capture-a", CaptureRawResponseSHA256: hashFixture("raw"), ObservationID: "status-a", ObservedStatus: "effective",
		RegistryURL: "https://fixture.invalid/status", ObservedAt: "2026-10-30T00:00:00Z", ResponseSHA256: hashFixture("status"), ResponseStatus: 200}
	return FrozenLegalAuthority{RuleID: "rule-1", AuthoritySourceID: "AUTH-CIVIL", AuthoritySourceVersion: "v1", RulePackID: "pack-a",
		RulePackVersion: "v1", RulePackContentSHA256: hashFixture("pack"), ExactReference: &ref,
		Article: &FrozenLegalArticle{Ref: KnowledgeRef{KnowledgeID: ref.KnowledgeID, VersionID: ref.VersionID, ContentSHA256: contentHash},
			ContentSHA256: contentHash, SourceID: ref.SourceID, SourceSnapshotID: ref.SourceSnapshotID, NormTitle: ref.NormTitle, NormVersion: ref.NormVersion,
			ArticleNumber: ref.ArticleNumber, ArticleLabel: articleLabel, DocumentType: ref.DocumentType, CanonicalBody: body,
			ArticleTextSHA256: bodyHash, QuoteSpan: ref.QuoteSpan, EffectiveFrom: ref.EffectiveFrom, Source: source, Status: status},
		Outcome: "applicable", Conditions: []FrozenLegalApplicabilityCondition{{Field: "buyer_kind", OneOf: []string{"enterprise"}, Outcome: "satisfied"}},
		SelectedFactVersions: []LegalApplicabilityFactVersion{}, MissingDependencies: []FrozenLegalDependencyGap{}, LegalReviewStatus: "NOT_RUN"}
}

func hashFixture(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
