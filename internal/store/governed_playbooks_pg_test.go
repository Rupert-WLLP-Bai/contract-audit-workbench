package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/agentflow"
	approvalmodel "contractaudit/internal/approval/model"
	"contractaudit/internal/contracttemplate"
	knowledgemodel "contractaudit/internal/knowledge/model"
)

func TestGovernedPlaybookLifecycleWithRestrictedRoles(t *testing.T) {
	fixture, _ := newApprovalDecisionEnv(t)
	core, documents, actor := fixture.core, fixture.document, fixture.admin
	execution := fixture.environment.openRole(t, "execution")
	execution.UseOperationAuthority(durableTestOperationAuthority{core: core})
	execution.UseDocumentPreparer(documents)
	execution.UseModels(&frozenOCRModel{})
	pack := publishSyntheticAcceptancePack(t, core, actor)
	publishSyntheticAcceptancePolicy(t, core, actor, pack)
	caseID := acceptanceKnowledgeCase(t, core, documents, actor)
	fixture.caseID = caseID
	body := "合同相对方：{{party}}。"
	tmpl := GovernedKnowledgeContent{KnowledgeID: "playbook-test-template", Version: "1", Kind: "template", Title: "正文范本", Body: body,
		Summary: "正文范本", SourceType: "reference", SourceReference: "测试来源", Scope: KnowledgeScope{BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service"},
		ValidFrom: "2020-01-01T00:00:00Z", ValidUntil: "2100-01-01T00:00:00Z",
		Template: &contracttemplate.Content{Body: body, Fields: []contracttemplate.Field{{Key: "party", Label: "相对方", Type: "string", Required: true}}}}
	template := publishKnowledge(t, core, actor, createKnowledgeDraft(t, core, actor, tmpl, "pb-template-create"), "pb-template")
	content := knowledgeFixtureContent(pack, "test-playbook", "1", "playbook")
	content.Playbook = &knowledgemodel.PlaybookContent{Template: knowledgemodel.PlaybookTemplateRef{TemplateID: template.Ref.KnowledgeID, TemplateVersion: template.Ref.VersionID, TemplateDigest: template.Ref.ContentSHA256}, Clauses: []knowledgemodel.PlaybookClause{{RuleID: content.Binding.RuleIDs[0], StandardClause: "标准条款", AcceptableDeviation: "有限偏差", HardConstraint: "最低底线", ProposedWording: "候选文本", EscalationCondition: "超过底线时升级"}}}
	playbook := publishKnowledge(t, core, actor, createKnowledgeDraft(t, core, actor, content, "pb-create"), "pb")
	ctx := WithAdmissionSessionToken(context.Background(), fixture.sessionToken)
	bad := playbook.Ref
	bad.ContentSHA256 = strings.Repeat("0", 64)
	if _, err := execution.StartReviewAsyncScope(WithReviewPlaybook(ctx, bad), actor, caseID, acceptanceBusinessTime, "", ""); err == nil {
		t.Fatal("review accepted wrong playbook digest")
	}
	accepted, err := execution.StartReviewAsyncScope(WithReviewPlaybook(ctx, playbook.Ref), actor, caseID, acceptanceBusinessTime, "", "")
	if err != nil {
		t.Fatalf("adopt playbook: %v", err)
	}
	workspace := readWorkspace(t, core, actor, caseID, accepted.RunID)
	if workspace.Snapshot.GovernedKnowledge == nil || workspace.Snapshot.GovernedKnowledge.AdoptedPlaybook == nil || workspace.Snapshot.GovernedKnowledge.AdoptedPlaybook.Ref != playbook.Ref {
		t.Fatal("review did not freeze selected playbook")
	}
	frozenReview, err := json.Marshal(workspace.Snapshot.GovernedKnowledge.AdoptedPlaybook)
	if err != nil {
		t.Fatal(err)
	}
	viewer, ok := accounts.ByID("demo-viewer")
	if !ok {
		t.Fatal("read-only fixture unavailable")
	}
	viewerSession, err := core.ResolveVerifiedOIDC(ctx, viewer, "client")
	if err != nil {
		t.Fatalf("authenticate read-only actor: %v", err)
	}
	viewerContext := WithAdmissionSessionToken(context.Background(), viewerSession.SessionToken)
	if _, err := core.ListCasePlaybooks(viewerContext, viewerSession.User, caseID); err == nil {
		t.Fatal("actor without review permission could list adoptable playbooks")
	}
	materialBody := []byte("审批引用材料")
	upload, err := documents.UploadMaterial(ctx, actor, caseID, "attachment", "approval.txt", "text/plain", materialBody, MaterialUploadOptions{IdempotencyKey: "pb-approval-material"})
	if err != nil {
		t.Fatal(err)
	}
	materialHash := sha256.Sum256(materialBody)
	draft := freezeCompleteDraft()
	draft.Materials = []MaterialRef{{MaterialID: upload.MaterialID, MaterialVersionID: upload.DocumentID, SHA256: hex.EncodeToString(materialHash[:]), Purpose: "supporting"}}
	contract, err := core.CreateContractDraft(ctx, actor, CreateContractDraftInput{AuthorityMode: "native", Draft: draft, IdempotencyKey: "pb-contract"})
	if err != nil {
		t.Fatal(err)
	}
	contractID := contract.Summary.ContractID
	revision, err := core.FreezeContractVersion(ctx, actor, FreezeContractVersionInput{CaseID: caseID, ContractID: contractID, ExpectedRowVersion: contract.Summary.RowVersion, IdempotencyKey: "pb-freeze"})
	if err != nil {
		t.Fatal(err)
	}
	options, err := core.ListContractPlaybooks(ctx, actor, contractID)
	if err != nil || len(options.Items) != 1 || options.Items[0].Ref != playbook.Ref {
		t.Fatalf("verified contract scope did not expose its applicable playbook: %+v %v", options, err)
	}
	approval, err := core.SubmitContractApproval(ctx, actor, SubmitContractApprovalInput{ContractID: contractID, ExpectedRowVersion: revision.RowVersion, IdempotencyKey: "pb-approval", PlaybookRef: &approvalmodel.PlaybookRef{KnowledgeID: playbook.Ref.KnowledgeID, VersionID: playbook.Ref.VersionID, ContentSHA256: playbook.Ref.ContentSHA256}})
	if err != nil {
		t.Fatalf("submit approval with playbook: %v", err)
	}
	var routingBefore []byte
	query := `SELECT routing_snapshot FROM clm_approval_rounds WHERE tenant_id=$1 AND contract_id=$2 AND approval_round_id=$3`
	if err := fixture.environment.harness.pool.QueryRow(ctx, query, actor.TenantID, contractID, approval.Round.ApprovalRoundID).Scan(&routingBefore); err != nil {
		t.Fatal(err)
	}
	var routing struct {
		Adopted json.RawMessage `json:"adopted_playbook"`
	}
	if json.Unmarshal(routingBefore, &routing) != nil || len(routing.Adopted) == 0 {
		t.Fatal("approval snapshot lacks playbook")
	}
	advanceKnowledge(t, core, actor, playbook, "withdraw", "pb-withdraw")
	if _, err := execution.StartReviewAsyncScope(WithReviewPlaybook(ctx, playbook.Ref), actor, caseID, acceptanceBusinessTime, "", ""); err == nil {
		t.Fatal("withdrawn playbook adopted")
	}
	after := readWorkspace(t, core, actor, caseID, accepted.RunID)
	if after.Snapshot.GovernedKnowledge == nil || after.Snapshot.GovernedKnowledge.AdoptedPlaybook == nil {
		t.Fatal("old adopted playbook disappeared")
	}
	reviewAfter, err := json.Marshal(after.Snapshot.GovernedKnowledge.AdoptedPlaybook)
	if err != nil || string(reviewAfter) != string(frozenReview) {
		t.Fatal("old review playbook changed")
	}
	read, err := core.ReadFrozenGovernedKnowledge(ctx, actor, caseID, *after.Snapshot.GovernedKnowledge, playbook.Ref)
	if err != nil || read.Ref != playbook.Ref || read.Body != content.Body {
		t.Fatalf("authorized frozen playbook became unreadable after withdrawal: %v", err)
	}
	var routingAfter []byte
	if err := fixture.environment.harness.pool.QueryRow(ctx, query, actor.TenantID, contractID, approval.Round.ApprovalRoundID).Scan(&routingAfter); err != nil || string(routingBefore) != string(routingAfter) {
		t.Fatalf("approval routing changed after withdrawal: %v", err)
	}
}

// Boundary is measured on the exact serialized payload, including JSON keys.
func TestAdoptedPlaybookPlannerByteBoundary(t *testing.T) {
	chinese := &knowledgemodel.PlaybookContent{Clauses: []knowledgemodel.PlaybookClause{{StandardClause: strings.Repeat("条款", 100)}}}
	chineseSnapshot := &ReviewKnowledgeSnapshot{AdoptedPlaybook: &knowledgemodel.AdoptedPlaybook{Content: GovernedKnowledgeContent{Playbook: chinese}}}
	chineseRaw, err := json.Marshal(chinese)
	if err != nil {
		t.Fatal(err)
	}
	chineseRefs, _, err := governedPlannerKnowledge(chineseSnapshot, nil, nil, len(chineseRaw))
	if err != nil || len(chineseRefs) != 1 || chineseRefs[0].Summary != string(chineseRaw) {
		t.Fatalf("valid Chinese playbook was rejected or clipped: %v", err)
	}
	playbook := &knowledgemodel.PlaybookContent{Clauses: []knowledgemodel.PlaybookClause{{StandardClause: "x"}}}
	raw, err := json.Marshal(playbook)
	if err != nil {
		t.Fatal(err)
	}
	playbook.Clauses[0].StandardClause = strings.Repeat("x", 1+agentflow.AdoptedPlaybookFieldBytes-len(raw))
	snapshot := &ReviewKnowledgeSnapshot{AdoptedPlaybook: &knowledgemodel.AdoptedPlaybook{Content: GovernedKnowledgeContent{Playbook: playbook}}}
	refs, _, err := governedPlannerKnowledge(snapshot, nil, nil, agentflow.AdoptedPlaybookFieldBytes)
	if err != nil || len(refs) != 1 || len(refs[0].Summary) != agentflow.AdoptedPlaybookFieldBytes {
		t.Fatalf("exact boundary rejected or truncated: refs=%d err=%v", len(refs), err)
	}
	playbook.Clauses[0].StandardClause += "x"
	if _, _, err := governedPlannerKnowledge(snapshot, nil, nil, agentflow.AdoptedPlaybookFieldBytes+1); err == nil {
		t.Fatal("oversized playbook accepted")
	}
	playbook.Clauses[0].StandardClause = playbook.Clauses[0].StandardClause[:len(playbook.Clauses[0].StandardClause)-1]
	if _, _, err := governedPlannerKnowledge(snapshot, nil, nil, agentflow.AdoptedPlaybookFieldBytes-1); err == nil {
		t.Fatal("playbook exceeded remaining total budget")
	}
}
