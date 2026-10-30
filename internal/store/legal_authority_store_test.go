package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

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
