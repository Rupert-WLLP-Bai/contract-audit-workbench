package store

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"contractaudit/internal/contracttemplate"
)

// Delegate to the actual Core owner; Document must not read governance tables.
type templateTestAuthority struct{ durableTestOperationAuthority }

func (a templateTestAuthority) ResolveDocumentTemplates(ctx context.Context, in contracttemplate.ResolveRequest) ([]contracttemplate.Resolved, error) {
	return a.core.ResolveDocumentTemplates(ctx, in.SessionDigest, in.ContextVersion, in.CaseID, in.ID, in.Version, in.Digest)
}

func TestGovernedTemplateLifecycleWithDocumentRole(t *testing.T) {
	fixture, ok := newDurableCaseFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated service roles")
	}
	core, document, actor := fixture.core, fixture.document, fixture.admin
	document.UseOperationAuthority(templateTestAuthority{durableTestOperationAuthority{core: core}})
	ctx := WithAdmissionSessionToken(context.Background(), fixture.sessionToken)
	caseID, err := core.CreateCaseWithBusiness(actor, "正文范本生命周期", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	body := "合同相对方：{{party}}。"
	content := GovernedKnowledgeContent{
		KnowledgeID: "tenant-template", Version: "1", Kind: "template", Title: "测试正文范本", Body: body,
		Summary: "用于验证冻结来源", SourceType: "reference", SourceReference: "范本治理记录",
		Scope:     KnowledgeScope{BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service"},
		ValidFrom: "2020-01-01T00:00:00Z", ValidUntil: "2100-01-01T00:00:00Z",
		Template: &contracttemplate.Content{Body: body, Fields: []contracttemplate.Field{{Key: "party", Label: "相对方", Type: "string", Required: true}}},
	}
	v := createKnowledgeDraft(t, core, actor, content, "template-create")
	fields := map[string]string{"party": "甲公司"}
	generate := func(key string) (GenerationRecord, error) {
		return document.GenerateWithTemplateOptions(ctx, actor, caseID, v.Ref.KnowledgeID, v.Ref.VersionID, v.Ref.ContentSHA256, fields, MaterialUploadOptions{IdempotencyKey: key})
	}
	if _, err := generate("draft-attempt"); err == nil {
		t.Fatal("draft template was usable before publication")
	}
	v = publishKnowledge(t, core, actor, v, "template")
	if _, err := document.GenerateWithTemplateOptions(ctx, actor, caseID, v.Ref.KnowledgeID, v.Ref.VersionID, strings.Repeat("0", 64), fields, MaterialUploadOptions{}); err == nil {
		t.Fatal("generation accepted a wrong template digest")
	}
	otherCase, err := core.CreateCaseWithBusiness(actor, "不同场景", "service", "ENT", "cloud_services")
	if err != nil {
		t.Fatal(err)
	}
	otherItems, err := document.TemplatesForCase(ctx, actor, otherCase)
	if err != nil {
		t.Fatalf("list different scenario: %v", err)
	}
	for _, item := range otherItems {
		if item.ID == v.Ref.KnowledgeID {
			t.Fatal("template selectable in a different scenario")
		}
	}
	if _, err := document.GenerateWithTemplateOptions(ctx, actor, otherCase, v.Ref.KnowledgeID, v.Ref.VersionID, v.Ref.ContentSHA256, fields, MaterialUploadOptions{}); err == nil {
		t.Fatal("template usable in a different scenario")
	}
	first, err := generate("template-generation")
	if err != nil {
		t.Fatalf("generate from published template: %v", err)
	}
	if first.TemplateID != v.Ref.KnowledgeID || first.TemplateVersion != v.Ref.VersionID || first.TemplateDigest == nil || *first.TemplateDigest != v.Ref.ContentSHA256 {
		t.Fatalf("generation did not pin exact template identity: %+v", first)
	}
	_, before, err := document.Original(actor, caseID, first.DocumentID)
	if err != nil || string(before) != "合同相对方：甲公司。" {
		t.Fatalf("generated original = %q, %v", before, err)
	}
	v = advanceKnowledge(t, core, actor, v, "withdraw", "template-withdraw")
	if v.Active {
		t.Fatal("withdrawn template remains active")
	}
	replay, err := generate("template-generation")
	if err != nil || !replay.Replayed || replay.ID != first.ID || replay.DocumentID != first.DocumentID || replay.TemplateDigest == nil || *replay.TemplateDigest != *first.TemplateDigest {
		t.Fatalf("withdrawn template receipt replay changed: %+v, %v", replay, err)
	}
	if _, err := generate("new-generation"); err == nil {
		t.Fatal("withdrawn template was usable for a new generation")
	}
	items, err := document.TemplatesForCase(ctx, actor, caseID)
	if err != nil {
		t.Fatalf("list templates after withdrawal: %v", err)
	}
	foundBuiltin := false
	for _, item := range items {
		if item.ID == v.Ref.KnowledgeID {
			t.Fatal("withdrawn template remains selectable")
		}
		if item.ID == telecomTemplateID {
			foundBuiltin = true
		}
	}
	if !foundBuiltin {
		t.Fatal("built-in template unavailable after withdrawal")
	}
	if _, err := document.GenerateWithOptions(ctx, actor, caseID, telecomTemplateID, validTelecomTemplateFields(), MaterialUploadOptions{
		Intent: "replace", MaterialID: first.MaterialID, ReplacesDocumentID: first.DocumentID,
	}); err != nil {
		t.Fatalf("built-in generation after withdrawal: %v", err)
	}
	_, after, err := document.Original(actor, caseID, first.DocumentID)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("previous generated original changed: %v", err)
	}
	detail, err := core.GetCase(actor, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Generations) != 2 || detail.Generations[0].TemplateDigest == nil || *detail.Generations[0].TemplateDigest != v.Ref.ContentSHA256 || detail.Generations[0].DocumentID != first.DocumentID {
		t.Fatalf("previous generation identity changed: %+v", detail.Generations)
	}
}
