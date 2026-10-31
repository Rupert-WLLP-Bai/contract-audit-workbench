package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"contractaudit/internal/accounts"
)

func TestInactiveSubjectBindingCanBeExplicitlyRestoredWithIndependentDecision(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("demo reviewer missing")
	}
	core, document := env.openRole(t, "core"), env.openRole(t, "document")
	ctx := context.Background()
	caseID, err := core.CreateCase(admin, "主体绑定恢复更正", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	if err := core.AddMember(admin, caseID, reviewer.ID); err != nil {
		t.Fatal(err)
	}
	otherCaseID, err := core.CreateCase(admin, "主体身份更正来源案件", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	if err := core.AddMember(admin, otherCaseID, reviewer.ID); err != nil {
		t.Fatal(err)
	}
	created, _, err := core.CreateCaseSubject(ctx, admin, caseID, CaseSubjectCreateInput{Role: SubjectRoleSupplier, Subject: SubjectCreateInput{DisplayName: "待恢复供应商"}}, "inactive-restore-subject")
	if err != nil {
		t.Fatal(err)
	}
	otherBinding, _, err := core.BindExistingSubject(ctx, admin, otherCaseID, BindExistingSubjectInput{Role: SubjectRoleSupplier, SubjectID: created.SubjectID, ExpectedSubjectVersion: 1}, "inactive-restore-other-binding")
	if err != nil {
		t.Fatal(err)
	}
	const text = "合同主体及许可信息供核对。"
	upload, err := document.UploadMaterial(ctx, admin, caseID, "procurement", "source.txt", "text/plain", []byte(text), MaterialUploadOptions{IdempotencyKey: "inactive-restore-source"})
	if err != nil {
		t.Fatal(err)
	}
	docs, err := document.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	var source Document
	for _, item := range docs.Documents {
		if item.ID == upload.DocumentID {
			source = item
			break
		}
	}
	if source.ID == "" {
		t.Fatal("uploaded source document missing")
	}
	parsed, err := document.ensureParsed(ctx, admin, source, false)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Status != "ready" {
		t.Fatalf("source parse not ready: %+v", parsed)
	}
	otherUpload, err := document.UploadMaterial(ctx, admin, otherCaseID, "procurement", "identity-source.txt", "text/plain", []byte(text), MaterialUploadOptions{IdempotencyKey: "inactive-restore-other-source"})
	if err != nil {
		t.Fatal(err)
	}
	otherState, err := document.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	var otherSource Document
	for _, item := range otherState.Documents {
		if item.ID == otherUpload.DocumentID {
			otherSource = item
			break
		}
	}
	if otherSource.ID == "" {
		t.Fatal("uploaded other-case document missing")
	}
	otherParsed, err := document.ensureParsed(ctx, admin, otherSource, false)
	if err != nil || otherParsed.Status != "ready" {
		t.Fatalf("other-case parse not ready: parsed=%+v err=%v", otherParsed, err)
	}
	if err := core.updateCaseAs(admin, caseID, func(st *state) error {
		for i := range st.CaseSubjectRoles {
			head := &st.CaseSubjectRoles[i]
			if head.BindingID != created.BindingID {
				continue
			}
			current := caseRoleCurrent(*st, *head)
			current.Version++
			current.BindingState = "inactive"
			current.CreatedByUser = admin.ID
			current.CreatedByIssuer = admin.Issuer
			current.CreatedBySubject = admin.Subject
			head.CurrentVersion = current.Version
			st.CaseSubjectRoleVersions = append(st.CaseSubjectRoleVersions, current)
			return nil
		}
		return errors.New("subject binding missing")
	}); err != nil {
		t.Fatal(err)
	}
	otherQuote := []rune(otherParsed.Text)
	otherDigest := sha256.Sum256([]byte(string(otherQuote)))
	otherBasis, _ := json.Marshal([]materialAnchorInput{{Kind: "material_anchor", MaterialID: otherUpload.MaterialID, MaterialVersion: otherUpload.Version, DocumentID: otherUpload.DocumentID, ParsedDocumentID: otherParsed.ID, Start: 0, End: len(otherQuote), QuoteDigest: hex.EncodeToString(otherDigest[:])}})
	identityInput := SubjectChangeCreateInput{Target: SubjectChangeTarget{Kind: "subject", SubjectID: created.SubjectID}, ExpectedSubjectVersion: 1, Patch: json.RawMessage(`{"legal_name":"更正供应商法定名","display_name":"更正供应商名稱"}`), BasisNote: "按另一在办案件材料更正主体名称", BasisRefs: otherBasis}
	identityProposal, replayed, err := core.ProposeSubjectChange(ctx, admin, otherCaseID, identityInput, "inactive-restore-identity-proposal")
	if err != nil || replayed || identityProposal.RequestID == "" {
		t.Fatalf("subject-wide correction proposal failed: result=%+v replayed=%v err=%v", identityProposal, replayed, err)
	}
	identityInput.Patch = json.RawMessage(`{"display_name":"更正供应商名稱","legal_name":"更正供应商法定名"}`)
	identityReplay, replayed, err := core.ProposeSubjectChange(ctx, admin, otherCaseID, identityInput, "inactive-restore-identity-proposal")
	if err != nil || !replayed || identityReplay.RequestID != identityProposal.RequestID {
		t.Fatalf("semantically identical property order did not replay the same proposal: result=%+v replay=%v err=%v", identityReplay, replayed, err)
	}
	identityImpact, err := core.GetSubjectChangeImpact(ctx, reviewer, otherCaseID, identityProposal.RequestID)
	if err != nil || !identityImpact.CanDecide {
		t.Fatalf("independent reviewer lacks subject correction scope: %+v err=%v", identityImpact, err)
	}
	identityDecision, _, err := core.DecideSubjectChange(ctx, reviewer, otherCaseID, identityProposal.RequestID, SubjectChangeDecisionInput{Decision: "approve", ExpectedSubjectVersion: 1, ExpectedPayloadHash: identityProposal.PayloadHash, ExpectedImpactETag: identityImpact.ImpactETag, Reason: "复核主体来源材料"}, "inactive-restore-identity-decision")
	if err != nil || identityDecision.AppliedSubjectVersion == nil || *identityDecision.AppliedSubjectVersion != 2 || identityDecision.BindingVersionsAppended != 1 {
		t.Fatalf("subject-wide correction did not append current binding version: result=%+v err=%v", identityDecision, err)
	}
	otherBindings, err := core.ListCaseSubjects(ctx, admin, otherCaseID)
	if err != nil || len(otherBindings.Items) != 1 || otherBindings.Items[0].Subject.Version != 2 || otherBindings.Items[0].BindingID != otherBinding.BindingID {
		t.Fatalf("other active case was not advanced to subject v2: %+v err=%v", otherBindings, err)
	}

	quote := []rune(parsed.Text)
	digest := sha256.Sum256([]byte(string(quote)))
	basis, _ := json.Marshal([]materialAnchorInput{{Kind: "material_anchor", MaterialID: upload.MaterialID, MaterialVersion: upload.Version, DocumentID: upload.DocumentID, ParsedDocumentID: parsed.ID, Start: 0, End: len(quote), QuoteDigest: hex.EncodeToString(digest[:])}})
	bindingVersion := int64(2)
	proposal, replayed, err := core.ProposeSubjectChange(ctx, admin, caseID, SubjectChangeCreateInput{Target: SubjectChangeTarget{Kind: "case_subject_role", SubjectID: created.SubjectID, BindingID: created.BindingID}, ExpectedSubjectVersion: 2, ExpectedBindingVersion: &bindingVersion, Patch: json.RawMessage(`{"binding_state":"active","role":"customer"}`), BasisNote: "按原始合同附件恢复关联并绑定主体当前版本", BasisRefs: basis}, "inactive-restore-proposal")
	if err != nil || replayed || proposal.RequestID == "" {
		t.Fatalf("inactive binding proposal failed: result=%+v replayed=%v err=%v", proposal, replayed, err)
	}
	impact, err := core.GetSubjectChangeImpact(ctx, reviewer, caseID, proposal.RequestID)
	beforeBinding, beforeOK := impact.Before.(CaseSubjectBindingProjection)
	afterBinding, afterOK := impact.After.(CaseSubjectBindingProjection)
	if err != nil || !impact.CanDecide || impact.VisibleAffectedCount != 1 || !beforeOK || !afterOK || beforeBinding.Subject.Version != 1 || afterBinding.Subject.Version != 2 || beforeBinding.BindingState != "inactive" || afterBinding.BindingState != "active" || beforeBinding.Role != SubjectRoleSupplier || afterBinding.Role != SubjectRoleCustomer {
		t.Fatalf("independent approver did not get exact impact scope: %+v err=%v", impact, err)
	}
	decision, replayed, err := core.DecideSubjectChange(ctx, reviewer, caseID, proposal.RequestID, SubjectChangeDecisionInput{Decision: "approve", ExpectedSubjectVersion: 2, ExpectedBindingVersion: &bindingVersion, ExpectedPayloadHash: proposal.PayloadHash, ExpectedImpactETag: impact.ImpactETag, Reason: "复核原始材料并恢复案件归属"}, "inactive-restore-decision")
	if err != nil || replayed || decision.Decision != "approved" || decision.BindingVersionsAppended != 1 || decision.AppliedSubjectVersion != nil {
		t.Fatalf("inactive binding restore decision failed: result=%+v replayed=%v err=%v", decision, replayed, err)
	}
	bindings, err := core.ListCaseSubjects(ctx, admin, caseID)
	if err != nil || len(bindings.Items) != 1 || bindings.Items[0].BindingState != "active" || bindings.Items[0].BindingVersion != 3 || bindings.Items[0].Subject.Version != 2 || bindings.Items[0].Role != SubjectRoleCustomer {
		t.Fatalf("restored binding was not appended as a new active version: %+v err=%v", bindings, err)
	}
	loaded, err := core.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	var oldInactiveVersion *CaseSubjectRoleVersion
	for i := range loaded.CaseSubjectRoleVersions {
		item := &loaded.CaseSubjectRoleVersions[i]
		if item.BindingID == created.BindingID && item.Version == 2 {
			oldInactiveVersion = item
		}
	}
	if oldInactiveVersion == nil || oldInactiveVersion.BindingState != "inactive" || oldInactiveVersion.SubjectVersion != 1 {
		t.Fatalf("old inactive binding pin was rewritten: %+v", oldInactiveVersion)
	}
	partyContext, _, err := partyContextForCase(loaded, caseID)
	if err != nil || len(partyContext) != 1 || partyContext[0].Role != SubjectRoleCustomer || partyContext[0].BindingVersion != 3 || partyContext[0].SubjectVersion != 2 {
		t.Fatalf("party context did not follow the approved binding version head: context=%+v err=%v", partyContext, err)
	}
}

func TestSubjectChangeProposalAndDecisionDoNotWriteInReadOnlyMode(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	core := env.openRole(t, "core")
	caseID, err := core.CreateCase(admin, "主体更正维护停写", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	before, err := core.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	core.UseReadOnly(true)
	if _, _, err := core.ProposeSubjectChange(context.Background(), admin, caseID, SubjectChangeCreateInput{}, "readonly-proposal"); !isReadOnlyFault(err) {
		t.Fatalf("proposal was not rejected as read-only: %v", err)
	}
	if _, _, err := core.DecideSubjectChange(context.Background(), admin, caseID, "missing-request", SubjectChangeDecisionInput{}, "readonly-decision"); !isReadOnlyFault(err) {
		t.Fatalf("decision was not rejected as read-only: %v", err)
	}
	core.UseReadOnly(false)
	after, err := core.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.SubjectChangeRequests) != len(before.SubjectChangeRequests) || len(after.SubjectChangeDecisions) != len(before.SubjectChangeDecisions) ||
		len(after.SubjectVersions) != len(before.SubjectVersions) || len(after.SubjectIdempotency) != len(before.SubjectIdempotency) {
		t.Fatalf("read-only subject write left rows behind: before=%+v after=%+v", before, after)
	}
}
