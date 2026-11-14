package store

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/documentclient"
)

func TestClauseProposalRestrictedRoleLifecycle(t *testing.T) {
	fixture, _ := newApprovalDecisionEnv(t)
	core, document, actor := fixture.core, fixture.document, fixture.admin
	for _, table := range []string{"clm_clause_proposals", "clm_clause_proposal_decisions", "clm_clause_proposal_receipts"} {
		var canInsert, canUpdate, canDelete bool
		if err := core.pool.QueryRow(context.Background(), `SELECT has_table_privilege(current_user,$1,'INSERT'),has_table_privilege(current_user,$1,'UPDATE'),has_table_privilege(current_user,$1,'DELETE')`, table).Scan(&canInsert, &canUpdate, &canDelete); err != nil {
			t.Fatal(err)
		}
		if !canInsert || canUpdate || canDelete {
			t.Fatalf("core privileges on %s: insert=%t update=%t delete=%t", table, canInsert, canUpdate, canDelete)
		}
	}
	execution := fixture.environment.openRole(t, "execution")
	execution.UseOperationAuthority(durableTestOperationAuthority{core: core})
	execution.UseDocumentPreparer(document)
	execution.UseModels(&frozenOCRModel{})
	ctx := WithAdmissionSessionToken(context.Background(), fixture.sessionToken)
	accepted, err := execution.StartReviewAsync(ctx, actor, fixture.caseID)
	if err != nil {
		t.Fatalf("accept review: %v", err)
	}
	var documentsJSON []byte
	if err := fixture.environment.harness.pool.QueryRow(ctx, `SELECT documents FROM review_attempt_inputs WHERE tenant_id=$1 AND run_id=$2 AND attempt_id=$3`, actor.TenantID, accepted.RunID, accepted.AttemptID).Scan(&documentsJSON); err != nil {
		t.Fatal(err)
	}
	var frozen []FrozenDocument
	if err := json.Unmarshal(documentsJSON, &frozen); err != nil {
		t.Fatal(err)
	}
	var source *FrozenDocument
	for i := range frozen {
		if frozen[i].Document.Role == "draft_contract" {
			source = &frozen[i]
			break
		}
	}
	if source == nil || source.NormalizedHash == nil || !strings.Contains(source.Text, "质保24个月") {
		t.Fatal("frozen contract source unavailable")
	}
	prefix := strings.SplitN(source.Text, "质保24个月", 2)[0]
	start := len([]rune(prefix))
	evidence := &contract.ClauseEvidence{DocumentID: source.Document.ID, ParsedDocumentID: source.ParsedDocumentID, NormalizedDigest: *source.NormalizedHash, Start: start, End: start + len([]rune("质保24个月")), Quote: "质保24个月"}
	input := contract.ClauseProposalCreate{CaseID: fixture.caseID, RunID: accepted.RunID, AttemptID: accepted.AttemptID, Evidence: evidence, ProposedText: "质保36个月", Reason: "按采购要求修改", IdempotencyKey: "clause-create-1"}
	proposal, err := core.CreateClauseProposal(ctx, actor, input)
	if err != nil || !proposal.EvidenceVerified {
		t.Fatalf("verified proposal: %+v %v", proposal, err)
	}
	derived, err := core.DerivedClauseText(ctx, actor, fixture.caseID, proposal.ID, proposal.Version, nil)
	if err != nil || derived != strings.Replace(source.Text, "质保24个月", "质保36个月", 1) {
		t.Fatalf("derived text mismatch: %v", err)
	}
	var materialID string
	if err := fixture.environment.harness.pool.QueryRow(ctx, `SELECT material_id FROM material_versions WHERE tenant_id=$1 AND case_id=$2 AND document_id=$3`, actor.TenantID, fixture.caseID, source.Document.ID).Scan(&materialID); err != nil {
		t.Fatal(err)
	}
	draft := freezeCompleteDraft()
	draft.Materials = []MaterialRef{{MaterialID: materialID, MaterialVersionID: source.Document.ID, SHA256: source.Document.Digest, Purpose: "supporting"}}
	created, err := core.CreateContractDraft(ctx, actor, CreateContractDraftInput{AuthorityMode: "native", Draft: draft, IdempotencyKey: "clause-contract-1"})
	if err != nil {
		t.Fatal(err)
	}
	original, err := core.FreezeContractVersion(ctx, actor, FreezeContractVersionInput{ContractID: created.Summary.ContractID, CaseID: fixture.caseID, ExpectedRowVersion: created.Summary.RowVersion, IdempotencyKey: "clause-freeze-1"})
	if err != nil {
		t.Fatal(err)
	}
	upload, err := document.UploadMaterial(ctx, actor, fixture.caseID, "draft_contract", "revised.txt", "text/plain", []byte(derived), MaterialUploadOptions{Intent: "replace", MaterialID: materialID, ReplacesDocumentID: source.Document.ID, IdempotencyKey: "clause-upload-1"})
	if err != nil {
		t.Fatal(err)
	}
	// Exercise real HTTP and session authentication, while reading through the restricted Document Store.
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		cookie, e := r.Cookie("session")
		if e != nil || r.URL.Path != "/api/cases/"+fixture.caseID+"/documents/"+upload.DocumentID+"/original" {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		user, audience, e := core.AuthenticateSession(r.Context(), cookie.Value)
		if e != nil || audience != "client" {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		_, body, e := document.OriginalContext(r.Context(), user, fixture.caseID, upload.DocumentID)
		if e != nil {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()
	reader, err := documentclient.NewClauseOriginalReader(server.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	core.UseClauseOriginalReader(reader)
	ctx = WithClauseDocumentSession(ctx, fixture.sessionToken)
	decision := contract.ClauseProposalDecision{Action: "accept", Confirmed: true, ExpectedVersion: 1, ContractID: created.Summary.ContractID, ExpectedContractRowVersion: original.RowVersion, MaterialID: materialID, DocumentID: upload.DocumentID, SHA256: sha256Bytes([]byte(derived)), IdempotencyKey: "clause-decision-1"}
	var revisionsBefore, decisionsBefore int
	countSQL := `SELECT (SELECT count(*) FROM clm_contract_revisions WHERE tenant_id=$1 AND contract_id=$2), (SELECT count(*) FROM clm_clause_proposal_decisions WHERE tenant_id=$1 AND proposal_id=$3)`
	if err := fixture.environment.harness.pool.QueryRow(ctx, countSQL, actor.TenantID, created.Summary.ContractID, proposal.ID).Scan(&revisionsBefore, &decisionsBefore); err != nil {
		t.Fatal(err)
	}
	stale := decision
	stale.ExpectedContractRowVersion--
	if _, err := core.DecideClauseProposal(ctx, actor, fixture.caseID, proposal.ID, stale); err == nil {
		t.Fatal("stale contract CAS accepted")
	}
	staleProposal := decision
	staleProposal.ExpectedVersion++
	if _, err := core.DecideClauseProposal(ctx, actor, fixture.caseID, proposal.ID, staleProposal); err == nil {
		t.Fatal("stale proposal CAS accepted")
	}
	var revisionsAfter, decisionsAfter int
	if err := fixture.environment.harness.pool.QueryRow(ctx, countSQL, actor.TenantID, created.Summary.ContractID, proposal.ID).Scan(&revisionsAfter, &decisionsAfter); err != nil {
		t.Fatal(err)
	}
	if revisionsBefore != revisionsAfter || decisionsBefore != decisionsAfter {
		t.Fatal("failed CAS changed revision or decision history")
	}
	const contractStateSQL = `SELECT c.row_version,c.current_revision_id::text,d.content::text FROM clm_contracts c JOIN clm_contract_drafts d USING (tenant_id,contract_id) WHERE c.tenant_id=$1 AND c.contract_id=$2`
	var rowBefore, rowAfter int64
	var revisionBefore, revisionAfter, draftBefore, draftAfter string
	if err := fixture.environment.harness.pool.QueryRow(ctx, contractStateSQL, actor.TenantID, created.Summary.ContractID).Scan(&rowBefore, &revisionBefore, &draftBefore); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.environment.harness.pool.Exec(ctx, `CREATE FUNCTION clause_test_decision_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NOT EXISTS (SELECT 1 FROM clm_contracts c JOIN clm_contract_revisions r ON r.tenant_id=c.tenant_id AND r.contract_id=c.contract_id AND r.contract_revision_id=c.current_revision_id WHERE c.tenant_id=NEW.tenant_id AND c.contract_id=(NEW.decision_json->>'contract_id')::uuid AND r.contract_revision_id=(NEW.decision_json->>'revision_id')::uuid) THEN RAISE EXCEPTION 'test proposal staging missing'; END IF; RAISE EXCEPTION 'test proposal decision failure'; END $$`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.environment.harness.pool.Exec(ctx, `CREATE TRIGGER clause_test_decision_fail BEFORE INSERT ON clm_clause_proposal_decisions FOR EACH ROW EXECUTE FUNCTION clause_test_decision_fail()`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = core.DecideClauseProposal(ctx, actor, fixture.caseID, proposal.ID, decision)
	if err == nil || !strings.Contains(err.Error(), "test proposal decision failure") {
		t.Fatalf("expected post-freeze trigger failure, got %v", err)
	}
	if err := fixture.environment.harness.pool.QueryRow(ctx, countSQL, actor.TenantID, created.Summary.ContractID, proposal.ID).Scan(&revisionsAfter, &decisionsAfter); err != nil {
		t.Fatal(err)
	}
	if err := fixture.environment.harness.pool.QueryRow(ctx, contractStateSQL, actor.TenantID, created.Summary.ContractID).Scan(&rowAfter, &revisionAfter, &draftAfter); err != nil {
		t.Fatal(err)
	}
	if revisionsAfter != revisionsBefore || decisionsAfter != decisionsBefore || rowAfter != rowBefore || revisionAfter != revisionBefore || draftAfter != draftBefore {
		t.Fatal("post-freeze failure did not roll back revision, decision and contract draft")
	}
	if _, err := fixture.environment.harness.pool.Exec(ctx, `DROP TRIGGER clause_test_decision_fail ON clm_clause_proposal_decisions`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.environment.harness.pool.Exec(ctx, `DROP FUNCTION clause_test_decision_fail()`); err != nil {
		t.Fatal(err)
	}
	result, err := core.DecideClauseProposal(ctx, actor, fixture.caseID, proposal.ID, decision)
	if err != nil || len(result.History) != 1 || result.History[0].RevisionID == "" || calls == 0 {
		t.Fatalf("accept: %+v calls=%d err=%v", result, calls, err)
	}
	if err := fixture.environment.harness.pool.QueryRow(ctx, countSQL, actor.TenantID, created.Summary.ContractID, proposal.ID).Scan(&revisionsAfter, &decisionsAfter); err != nil {
		t.Fatal(err)
	}
	if revisionsAfter != revisionsBefore+1 || decisionsAfter != decisionsBefore+1 {
		t.Fatal("revision and decision were not committed together")
	}
	replay, err := core.DecideClauseProposal(ctx, actor, fixture.caseID, proposal.ID, decision)
	if err != nil || !reflect.DeepEqual(result, replay) {
		t.Fatalf("decision replay: %v", err)
	}
	originalCreate, err := core.CreateClauseProposal(ctx, actor, input)
	if err != nil || !reflect.DeepEqual(originalCreate, proposal) {
		t.Fatalf("create receipt changed: %v", err)
	}
	if _, err := reader.ReadClauseOriginal(ctx, fixture.caseID, upload.DocumentID, "invalid-session"); err == nil {
		t.Fatal("HTTP original accepted invalid session")
	}
	secondInput := input
	secondInput.IdempotencyKey = "clause-create-2"
	second, err := core.CreateClauseProposal(ctx, actor, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := core.DecideClauseProposal(ctx, actor, fixture.caseID, second.ID, contract.ClauseProposalDecision{Action: "reject", Confirmed: true, ExpectedVersion: 1, IdempotencyKey: "clause-reject-1"})
	if err != nil || len(rejected.History) != 1 || rejected.History[0].RevisionID != "" {
		t.Fatalf("reject: %+v %v", rejected, err)
	}
	if err := core.Logout(fixture.sessionToken); err != nil {
		t.Fatal(err)
	}
	if _, err := core.DecideClauseProposal(ctx, actor, fixture.caseID, proposal.ID, decision); err == nil {
		t.Fatal("replay after session revocation succeeded")
	}
}
