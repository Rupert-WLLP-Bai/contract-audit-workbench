package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/rulepack"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRulePackManifestWithExactLegalRefPassesStructuralParsing(t *testing.T) {
	content := validLegalContentFixture()
	article := GovernedKnowledgeVersion{Ref: KnowledgeRef{
		KnowledgeID: content.KnowledgeID, VersionID: strings.Repeat("a", 64), ContentSHA256: strings.Repeat("b", 64),
	}, Content: content}
	raw := rulePackManifestWithLegalRef(t, "tenant-fixture", "legal-structure-fixture", article, nil)
	if _, err := rulepack.Parse(raw); err != nil {
		t.Fatalf("generated exact legal citation fixture is not structurally valid: %v", err)
	}
}

func TestReopenPreservesAcceptedReviewWithItsQueuedDurableJob(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Fatal("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for application reopen verification")
	}
	core, document, execution := env.openRole(t, "core"), env.openRole(t, "document"), env.openRole(t, "execution")
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	pack := publishSyntheticAcceptancePack(t, core, admin)
	publishSyntheticAcceptancePolicy(t, core, admin, pack)
	caseID, err := core.CreateCaseWithBusiness(admin, "queued review after application reopen", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatalf("create reopen test case: %v", err)
	}
	for _, item := range []struct{ role, body string }{
		{"procurement", "采购文件要求电路 LINE-001 最低带宽为100 Mbit/s。"},
		{"draft_contract", "拟签合同约定电路 LINE-001 最低带宽为100 Mbit/s。"},
	} {
		if _, err := document.AddDocument(context.Background(), admin, caseID, item.role, item.role+".txt", "text/plain", []byte(item.body)); err != nil {
			t.Fatalf("add reopen test material %s: %v", item.role, err)
		}
	}
	accepted, err := execution.StartReviewAsyncAt(context.Background(), admin, caseID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("accept durable review before restart: %v", err)
	}
	if err := execution.Close(); err != nil {
		t.Fatalf("close old application execution store: %v", err)
	}
	reopened := env.openRole(t, "execution")
	workspace, err := reopened.Workspace(admin, caseID, accepted.RunID, accepted.AttemptID)
	if err != nil || workspace.Execution == nil || workspace.Execution.Attempt == nil || workspace.Execution.Attempt.State != "accepted" {
		t.Fatalf("application startup altered an accepted queued attempt: workspace=%+v err=%v", workspace.Execution, err)
	}
	claim, err := reopened.ClaimNextJob(context.Background(), "restart-compatibility-worker", time.Minute)
	if err != nil {
		t.Fatalf("restarted application worker could not claim queued review: %v", err)
	}
	if claim.JobID != accepted.JobID || claim.AttemptID != accepted.AttemptID {
		t.Fatalf("restarted worker could not claim the exact queued accepted attempt: claim=%+v err=%v", claim, err)
	}
}

func TestLegalReviewAcceptanceFreezesExactAuthorityAndReusesUnchangedCutoffIntentInPostgres(t *testing.T) {
	assetDir := os.Getenv("LEGALNORM_ASSET_DIR")
	if assetDir == "" {
		t.Fatal("LEGALNORM_ASSET_DIR is required for legal review acceptance")
	}
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Fatal("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for legal review acceptance")
	}
	core, document, execution := env.openRole(t, "core"), env.openRole(t, "document"), env.openRole(t, "execution")
	execution.UseModels(&frozenOCRModel{})
	execution.UseDocumentPreparer(document)
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	input := legalSourceImportFixture(t, assetDir, "S1_civil_code_contract")
	input.IdempotencyKey = "legal-review-acceptance-import"
	imported, err := core.ImportLegalSource(context.Background(), admin, input)
	if err != nil {
		t.Fatalf("import verified legal source: %v", err)
	}
	var article GovernedKnowledgeVersion
	for _, ref := range imported.Articles {
		version, err := core.GetGovernedKnowledgeVersion(admin, ref.KnowledgeID, ref.VersionID)
		if err != nil {
			t.Fatal(err)
		}
		if version.Content.LegalMetadata.ArticleNumber == 496 {
			article = version
			break
		}
	}
	if article.Ref.KnowledgeID == "" {
		t.Fatal("verified legal source omitted the selected article")
	}
	for _, action := range []string{"submit", "validate", "approve", "publish"} {
		command := knowledgeCommand(article, action, "legal-review-acceptance-article-"+action)
		command.CrossCaseReviewed = true
		article, err = core.GovernedKnowledgeCommand(admin, command)
		if err != nil {
			t.Fatalf("publish legal article for acceptance: %s: %v", action, err)
		}
	}
	manifest := rulePackManifestWithLegalRef(t, accounts.Tenant(admin), "telecom-governed", article, nil)
	var manifestValue map[string]any
	if err := json.Unmarshal(manifest, &manifestValue); err != nil {
		t.Fatal(err)
	}
	rules := manifestValue["rules"].([]any)
	first := rules[0].(map[string]any)
	for _, value := range rules {
		rule := value.(map[string]any)
		rule["authority_sources"] = first["authority_sources"]
		rule["legal_authorities"] = first["legal_authorities"]
	}
	manifest, err = json.Marshal(manifestValue)
	if err != nil {
		t.Fatal(err)
	}
	pack := publishGovernedPackStore(t, core, admin, "1", "100", manifest)
	caseID := acceptanceKnowledgeCase(t, core, document, admin)
	fact, err := core.RecordLegalApplicabilityFact(context.Background(), admin, caseID, LegalApplicabilityFactInput{
		ExpectedVersion: 0, IdempotencyKey: "legal-review-acceptance-transaction-fact", Dimension: "transaction_kind", Status: "unknown",
		UnknownReason: "insufficient_evidence", TemporalBasis: "current_state", BasisNote: "案件资料尚不足以确认交易性质", BasisRefs: json.RawMessage(`[]`),
	})
	if err != nil {
		t.Fatalf("record a genuine unknown applicability fact: %v", err)
	}
	businessAt := time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)
	firstAcceptance, err := execution.StartReviewAsyncAtWithEvidenceCutoff(context.Background(), admin, caseID, businessAt, "")
	if err != nil {
		t.Fatalf("accept review with server-sampled evidence cutoff: %v", err)
	}
	claim, err := execution.ClaimNextJob(context.Background(), "legal-authority-runtime-worker", time.Minute)
	if err != nil || claim.JobID != firstAcceptance.JobID {
		t.Fatalf("claim accepted legal review: claim=%+v err=%v", claim, err)
	}
	if err := execution.RunClaimedJob(context.Background(), claim); err != nil {
		t.Fatalf("execute accepted legal review: %v", err)
	}
	firstWorkspace := readWorkspace(t, execution, admin, caseID, firstAcceptance.RunID)
	frozen := firstWorkspace.Snapshot.GovernedKnowledge
	if frozen == nil || frozen.EvidenceCutoffMode != "acceptance_time" || frozen.EvidenceCutoffAt == "" || frozen.ExplicitEvidenceCutoffAt != "" ||
		len(frozen.LegalAuthorities) != 3 || !validLegalReuseFingerprint(frozen) {
		t.Fatalf("review did not freeze all normative sources with a sampled cutoff and reuse fingerprint: %+v", frozen)
	}
	if firstWorkspace.Execution == nil || firstWorkspace.Execution.Attempt == nil {
		t.Fatal("accepted review omitted its exact attempt identity")
	}
	if len(firstWorkspace.Evaluations) != 3 {
		var attemptError string
		if firstWorkspace.Execution.Attempt.ErrorCode != nil {
			attemptError = *firstWorkspace.Execution.Attempt.ErrorCode
		}
		var failures []string
		for _, event := range firstWorkspace.Execution.Events {
			if event.EventType != "node_failed" && event.EventType != "attempt_finished" {
				continue
			}
			code := ""
			if event.ErrorCode != nil {
				code = *event.ErrorCode
			}
			failures = append(failures, event.Phase+":"+event.Status+":"+code)
		}
		t.Fatalf("governed execution did not persist all three mandatory rule results: evaluations=%+v attempt=%+v events=%+v",
			firstWorkspace.Evaluations, struct {
				State     string
				ErrorCode string
			}{State: firstWorkspace.Execution.Attempt.State, ErrorCode: attemptError}, failures)
	}
	for _, evaluation := range firstWorkspace.Evaluations {
		if evaluation.Relation != string(rulepack.RelationInsufficient) || evaluation.ReasonCode != "legal_authority_unresolved" ||
			len(evaluation.FactIDs) != 0 || len(evaluation.Evidence) != 0 {
			t.Fatalf("missing legal applicability fact reached the comparator or acquired contract evidence: %+v", evaluation)
		}
	}
	report, err := core.CreateReport(admin, caseID, firstAcceptance.RunID, firstWorkspace.Execution.Attempt.ID)
	if err != nil {
		t.Fatalf("create report from the exact legal review attempt: %v", err)
	}
	readReport, reportJSON, err := core.ReadReport(admin, caseID, report.ID, "json")
	if err != nil || readReport.Digest != report.Digest {
		t.Fatalf("read the frozen legal review report: digestMatch=%v err=%v", err == nil && readReport.Digest == report.Digest, err)
	}
	var reportBody reportEnvelope
	if err := json.Unmarshal(reportJSON, &reportBody); err != nil {
		t.Fatalf("decode frozen legal report: %v", err)
	}
	if reportBody.LegalReview == nil || reportBody.LegalReview.EvidenceCutoffAt != frozen.EvidenceCutoffAt ||
		reportBody.LegalReview.EvidenceCutoffMode != "acceptance_time" || !reflect.DeepEqual(reportBody.LegalReview.Authorities, frozen.LegalAuthorities) {
		t.Fatalf("report did not include the exact frozen AUTH03 projection: %+v", reportBody.LegalReview)
	}
	firstAuthority := frozen.LegalAuthorities[0]
	frozenRead, err := execution.ReadFrozenLegalAuthority(context.Background(), admin, caseID, firstAcceptance.RunID, firstWorkspace.Execution.Attempt.ID, firstAuthority.CitationID)
	if err != nil || !reflect.DeepEqual(frozenRead, firstAuthority) {
		t.Fatalf("exact attempt reader did not return its immutable legal authority: same=%v err=%v", reflect.DeepEqual(frozenRead, firstAuthority), err)
	}
	if _, err := execution.ReadFrozenLegalAuthority(context.Background(), admin, caseID, firstAcceptance.RunID, "attempt-not-in-run", firstAuthority.CitationID); err == nil {
		t.Fatal("exact legal reader accepted an attempt outside the selected run")
	}
	for _, authority := range frozen.LegalAuthorities {
		if authority.RulePackID != pack.Pack.ID || authority.RulePackVersion != pack.Pack.Version || authority.RulePackContentSHA256 != pack.Pack.ContentHash ||
			authority.ExactReference == nil || authority.Article == nil || authority.Article.Ref != article.Ref || authority.Outcome != "unknown" ||
			authority.LegalReviewStatus != "NOT_RUN" || authority.Reviewer != nil || len(authority.SelectedFactVersions) != 1 ||
			authority.SelectedFactVersions[0].FactID != fact.FactID || authority.SelectedFactVersions[0].Version != fact.Version {
			t.Fatalf("frozen normative dependency lost exact article, fact, or review state: %+v", authority)
		}
	}
	defaultReplay, err := execution.StartReviewAsyncAtWithEvidenceCutoff(context.Background(), admin, caseID, businessAt, "")
	if err != nil || defaultReplay != firstAcceptance {
		t.Fatalf("unchanged legal dependencies with a new acceptance-time sample did not reuse the run: first=%+v replay=%+v err=%v", firstAcceptance, defaultReplay, err)
	}
	explicitCutoff := "2025-01-01T00:00:00Z"
	explicit, err := execution.StartReviewAsyncAtWithEvidenceCutoff(context.Background(), admin, caseID, businessAt, explicitCutoff)
	if err != nil || explicit.RunID == firstAcceptance.RunID {
		t.Fatalf("explicit evidence cutoff did not create a distinct frozen dependency set: first=%+v explicit=%+v err=%v", firstAcceptance, explicit, err)
	}
	explicitWorkspace := readWorkspace(t, execution, admin, caseID, explicit.RunID)
	if explicitWorkspace.Snapshot.GovernedKnowledge.EvidenceCutoffMode != "explicit" || explicitWorkspace.Snapshot.GovernedKnowledge.ExplicitEvidenceCutoffAt != explicitCutoff {
		t.Fatalf("explicit cutoff intent was not preserved in the frozen snapshot: %+v", explicitWorkspace.Snapshot.GovernedKnowledge)
	}
	explicitReplay, err := execution.StartReviewAsyncAtWithEvidenceCutoff(context.Background(), admin, caseID, businessAt, explicitCutoff)
	if err != nil || explicitReplay != explicit {
		t.Fatalf("same explicit cutoff and dependencies did not reuse the accepted run: first=%+v replay=%+v err=%v", explicit, explicitReplay, err)
	}

	newBasis := legalApplicabilityMaterialBasis(t, document, admin, caseID, "legal-review-later-evidence.txt", "本交易属于民事合同。", "legal-review-later-evidence-upload")
	businessTime, err := time.Parse(time.RFC3339Nano, businessAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.RecordLegalApplicabilityFact(context.Background(), admin, caseID, LegalApplicabilityFactInput{
		ExpectedVersion: 1, IdempotencyKey: "legal-review-acceptance-later-fact", Dimension: "transaction_kind", Status: "established", Value: "civil_contract",
		TemporalBasis: "historical_interval", ValidFrom: businessTime.Format(time.RFC3339Nano),
		ValidUntil: businessTime.Add(time.Minute).Format(time.RFC3339Nano), BasisNote: "后来取得的材料明确证明冻结业务时点的交易性质", BasisRefs: newBasis,
	}); err != nil {
		t.Fatalf("append later legal applicability evidence: %v", err)
	}
	if err := execution.ValidateFrozenGovernedKnowledgeSnapshot(context.Background(), admin, caseID, *frozen); err != nil {
		t.Fatalf("worker adoption re-resolved later facts instead of validating the accepted freeze: %v", err)
	}
	currentFacts, err := core.ResolveLegalApplicabilityFacts(context.Background(), admin, caseID, LegalApplicabilityResolutionInput{
		BusinessAt: businessAt, EvidenceCutoffAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("resolve current legal facts for retry comparison: %v", err)
	}
	var currentTransactionFact LegalApplicabilityResolvedFact
	for _, resolved := range currentFacts.Facts {
		if resolved.Dimension == "transaction_kind" {
			currentTransactionFact = resolved
			break
		}
	}
	if err := execution.RevalidateGovernedKnowledgeSnapshot(context.Background(), admin, caseID, *frozen); !isConflictFault(err) {
		version := int64(0)
		factID := ""
		if currentTransactionFact.Fact != nil {
			version = currentTransactionFact.Fact.Version
			factID = currentTransactionFact.Fact.FactID
		}
		t.Fatalf("retry accepted changed legal fact dependencies instead of requiring a new review: err=%v resolved=%s/%s version=%d fact=%s",
			err, currentTransactionFact.Status, currentTransactionFact.Value, version, factID)
	}
	afterLaterFact := readWorkspace(t, execution, admin, caseID, firstAcceptance.RunID)
	if afterLaterFact.Snapshot.GovernedKnowledge == nil || !reflect.DeepEqual(*afterLaterFact.Snapshot.GovernedKnowledge, *frozen) {
		t.Fatal("later legal facts rewrote the old accepted review snapshot")
	}
}

func TestRulePackRequiresExactApprovedCurrentLegalAuthorityInPostgres(t *testing.T) {
	assetDir := os.Getenv("LEGALNORM_ASSET_DIR")
	if assetDir == "" {
		t.Fatal("LEGALNORM_ASSET_DIR is required for the legal authority store test")
	}
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Fatal("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for the legal authority store test")
	}
	core := env.openRole(t, "core")
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	input := legalSourceImportFixture(t, assetDir, "S1_civil_code_contract")
	input.IdempotencyKey = "legal-authority-exact-import"
	imported, err := core.ImportLegalSource(context.Background(), admin, input)
	if err != nil {
		t.Fatalf("import exact official capture: %v", err)
	}
	var article GovernedKnowledgeVersion
	for _, ref := range imported.Articles {
		version, err := core.GetGovernedKnowledgeVersion(admin, ref.KnowledgeID, ref.VersionID)
		if err != nil {
			t.Fatal(err)
		}
		if version.Content.LegalMetadata.ArticleNumber == 496 {
			article = version
			break
		}
	}
	if article.Ref.KnowledgeID == "" {
		t.Fatal("verified import did not contain civil code article 496")
	}
	for _, action := range []string{"submit", "validate", "approve", "publish"} {
		command := knowledgeCommand(article, action, "legal-authority-article-"+action)
		command.CrossCaseReviewed = true
		article, err = core.GovernedKnowledgeCommand(admin, command)
		if err != nil {
			t.Fatalf("govern exact legal article %s: %v", action, err)
		}
	}
	if !isPublishableLegalAuthority(article) {
		t.Fatal("article did not retain its approved published identity")
	}

	goodManifest := rulePackManifestWithLegalRef(t, accounts.Tenant(admin), "legal-exact-good", article, nil)
	created, err := core.CreateRulePack(admin, goodManifest)
	if err != nil {
		t.Fatalf("create rulepack with exact approved citation: %v", err)
	}
	validated, err := core.ValidateRulePackVersion(admin, GovernanceInput{PackID: "legal-exact-good", Version: "1", ExpectedRevision: created.Pack.Revision})
	if err != nil {
		t.Fatalf("validate rulepack with exact approved citation: %v", err)
	}
	approved, err := core.ApproveRulePackVersion(admin, GovernanceInput{PackID: "legal-exact-good", Version: "1", ExpectedRevision: validated.Pack.Revision, ExpectedContentHash: validated.Pack.ContentHash})
	if err != nil {
		t.Fatalf("approve rulepack with exact approved citation: %v", err)
	}
	if _, err := core.PublishRulePackVersion(admin, GovernanceInput{PackID: "legal-exact-good", Version: "1", ExpectedRevision: approved.Pack.Revision, ExpectedPointerRevision: approved.Pack.ActivePointerRevision}); err != nil {
		t.Fatalf("publish rulepack with exact approved citation: %v", err)
	}

	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"wrong content digest", func(ref map[string]any) { ref["content_sha256"] = strings.Repeat("0", 64) }},
		{"wrong quote span", func(ref map[string]any) { ref["quote_span"] = map[string]any{"start": 0, "end": 2, "quote": "错误"} }},
		{"wrong source capture", func(ref map[string]any) { ref["source_snapshot_id"] = "different-captured-source" }},
		{"draft is not an execution authority", func(ref map[string]any) { ref["document_type"] = "draft" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packID := "legal-invalid-" + strings.ReplaceAll(strings.ReplaceAll(tc.name, " ", "-"), "'", "")
			manifest := rulePackManifestWithLegalRef(t, accounts.Tenant(admin), packID, article, tc.mutate)
			candidate, err := core.CreateRulePack(admin, manifest)
			if err != nil {
				t.Fatalf("create structurally valid test rulepack: %v", err)
			}
			_, err = core.ValidateRulePackVersion(admin, GovernanceInput{PackID: packID, Version: "1", ExpectedRevision: candidate.Pack.Revision})
			requireKnowledgeFault(t, err, 409)
		})
	}
	noReferenceManifest := rulePackManifestWithLegalRef(t, accounts.Tenant(admin), "legal-invalid-no-reference", article, nil)
	var noReference map[string]any
	if err := json.Unmarshal(noReferenceManifest, &noReference); err != nil {
		t.Fatal(err)
	}
	delete(noReference["rules"].([]any)[0].(map[string]any), "legal_authorities")
	noReferenceManifest, err = json.Marshal(noReference)
	if err != nil {
		t.Fatal(err)
	}
	withoutCitation, err := core.CreateRulePack(admin, noReferenceManifest)
	if err != nil {
		t.Fatalf("create normative rule without a citation to verify: %v", err)
	}
	_, err = core.ValidateRulePackVersion(admin, GovernanceInput{PackID: "legal-invalid-no-reference", Version: "1", ExpectedRevision: withoutCitation.Pack.Revision})
	requireKnowledgeFault(t, err, 409)

	// A direct attempt to remove approval evidence must be rejected by the
	// governance lifecycle trigger. Do not bypass the trigger to manufacture a
	// state that production writes cannot create.
	tx, err := env.harness.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(context.Background(), tx, userScope(admin)); err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatal(err)
	}
	_, err = tx.Exec(context.Background(), `UPDATE governed_knowledge_state SET approved_digest='' WHERE tenant_id=$1 AND knowledge_id=$2 AND version_id=$3`, accounts.Tenant(admin), article.Ref.KnowledgeID, article.Ref.VersionID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "P0001" {
		_ = tx.Rollback(context.Background())
		t.Fatalf("attempt to forge a published legal version without approval was not blocked by its lifecycle trigger: %v", err)
	}
	_ = tx.Rollback(context.Background())
	unchanged, err := core.GetGovernedKnowledgeVersion(admin, article.Ref.KnowledgeID, article.Ref.VersionID)
	if err != nil || !isPublishableLegalAuthority(unchanged) || !reflect.DeepEqual(unchanged, article) {
		t.Fatalf("rejected approval mutation changed the published legal version/fingerprint: publishable=%v unchanged=%v err=%v", isPublishableLegalAuthority(unchanged), reflect.DeepEqual(unchanged, article), err)
	}
}

func rulePackManifestWithLegalRef(t *testing.T, tenantID, packID string, article GovernedKnowledgeVersion, mutate func(map[string]any)) []byte {
	t.Helper()
	var manifest map[string]any
	if err := json.Unmarshal(governedPackManifest(t, tenantID, "1", "100"), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["id"] = packID
	rules := manifest["rules"].([]any)
	rule := rules[0].(map[string]any)
	metadata := article.Content.LegalMetadata
	quoteRunes := []rune(article.Content.Body)
	if len(quoteRunes) > 32 {
		quoteRunes = quoteRunes[:32]
	}
	quote := string(quoteRunes)
	ref := map[string]any{
		"authority_source_id": metadata.SourceID, "knowledge_id": article.Ref.KnowledgeID, "version_id": article.Ref.VersionID,
		"content_sha256": article.Ref.ContentSHA256, "source_id": metadata.SourceID, "source_snapshot_id": metadata.SourceSnapshotID,
		"norm_title": metadata.NormTitle, "norm_version": metadata.NormVersion, "article_number": metadata.ArticleNumber,
		"document_type": metadata.DocumentType, "source_sha256": metadata.SourceSHA256,
		"article_text_sha256": metadata.NormTextSHA256, "effective_from": metadata.EffectiveFrom, "effective_until": metadata.EffectiveUntil,
		"quote_span":    map[string]any{"start": 0, "end": len(quoteRunes), "quote": quote},
		"applicability": map[string]any{"all_of": []any{map[string]any{"field": "transaction_kind", "one_of": []string{"civil_contract"}}}},
	}
	if mutate != nil {
		mutate(ref)
	}
	rule["authority_sources"] = []any{map[string]any{"id": metadata.SourceID, "version": metadata.NormVersion, "kind": "normative"}}
	rule["legal_authorities"] = []any{ref}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
