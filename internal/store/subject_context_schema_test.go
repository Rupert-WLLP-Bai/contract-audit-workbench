package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"contractaudit/internal/accounts"
)

func TestSubjectWritesHonorContextWhileWaitingForSchemaLock(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	core, document := env.openRole(t, "core"), env.openRole(t, "document")
	caseID, err := core.CreateCase(admin, "请求取消主体更正", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	created, _, err := core.CreateCaseSubject(context.Background(), admin, caseID,
		CaseSubjectCreateInput{Role: SubjectRoleSupplier, Subject: SubjectCreateInput{DisplayName: "原始供应商"}}, "ctx-subject-setup")
	if err != nil {
		t.Fatal(err)
	}
	const materialText = "原始材料用于验证主体更正的请求取消行为。"
	upload, err := document.UploadMaterial(context.Background(), admin, caseID, "procurement", "ctx-source.txt", "text/plain", []byte(materialText), MaterialUploadOptions{IdempotencyKey: "ctx-source-upload"})
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
		t.Fatal("basis source document missing")
	}
	parsed, err := document.ensureParsed(context.Background(), admin, source, false)
	if err != nil || parsed.Status != "ready" {
		t.Fatalf("basis source parse: status=%s err=%v", parsed.Status, err)
	}
	quote := []rune(parsed.Text)
	digest := sha256.Sum256([]byte(string(quote)))
	basis, err := json.Marshal([]materialAnchorInput{{Kind: "material_anchor", MaterialID: upload.MaterialID, MaterialVersion: upload.Version,
		DocumentID: upload.DocumentID, ParsedDocumentID: parsed.ID, Start: 0, End: len(quote), QuoteDigest: hex.EncodeToString(digest[:])}})
	if err != nil {
		t.Fatal(err)
	}
	proposalInput := SubjectChangeCreateInput{Target: SubjectChangeTarget{Kind: "subject", SubjectID: created.SubjectID}, ExpectedSubjectVersion: 1,
		Patch: json.RawMessage(`{"display_name":"更正供应商"}`), BasisNote: "按材料更正名称", BasisRefs: basis}
	before, err := core.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}

	lockCtx, cancelLock := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelLock()
	lockTx, err := env.harness.pool.Begin(lockCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockTx.Rollback(context.Background())
	if _, err := lockTx.Exec(lockCtx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(), 0))`); err != nil {
		t.Fatal(err)
	}

	createCtx, cancelCreate := context.WithTimeout(context.Background(), 120*time.Millisecond)
	_, _, createErr := core.CreateUnboundSubject(createCtx, admin, SubjectCreateInput{DisplayName: "请求取消后不应留下的主体"}, "ctx-subject-timeout")
	cancelCreate()
	if !errors.Is(createErr, context.DeadlineExceeded) {
		t.Fatalf("subject write waiting on schema lock did not honor deadline: %v", createErr)
	}
	proposalCtx, cancelProposal := context.WithTimeout(context.Background(), 120*time.Millisecond)
	_, _, proposalErr := core.ProposeSubjectChange(proposalCtx, admin, caseID, proposalInput, "ctx-proposal-timeout")
	cancelProposal()
	if !errors.Is(proposalErr, context.DeadlineExceeded) {
		t.Fatalf("proposal write waiting on schema lock did not honor deadline: %v", proposalErr)
	}
	afterTimeout, err := core.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterTimeout.Subjects) != len(before.Subjects) || len(afterTimeout.SubjectVersions) != len(before.SubjectVersions) ||
		len(afterTimeout.SubjectChangeRequests) != len(before.SubjectChangeRequests) || len(afterTimeout.SubjectChangeDecisions) != len(before.SubjectChangeDecisions) ||
		len(afterTimeout.SubjectIdempotency) != len(before.SubjectIdempotency) {
		t.Fatalf("cancelled requests left subject/proposal/idempotency rows: before=(%d,%d,%d,%d,%d) after=(%d,%d,%d,%d,%d)",
			len(before.Subjects), len(before.SubjectVersions), len(before.SubjectChangeRequests), len(before.SubjectChangeDecisions), len(before.SubjectIdempotency),
			len(afterTimeout.Subjects), len(afterTimeout.SubjectVersions), len(afterTimeout.SubjectChangeRequests), len(afterTimeout.SubjectChangeDecisions), len(afterTimeout.SubjectIdempotency))
	}
	if err := lockTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, replayed, err := core.CreateUnboundSubject(context.Background(), admin, SubjectCreateInput{DisplayName: "请求取消后不应留下的主体"}, "ctx-subject-timeout")
	if err != nil || replayed {
		t.Fatalf("subject creation did not succeed after lock release: replayed=%v err=%v", replayed, err)
	}
	proposal, replayed, err := core.ProposeSubjectChange(context.Background(), admin, caseID, proposalInput, "ctx-proposal-timeout")
	if err != nil || replayed || proposal.RequestID == "" {
		t.Fatalf("proposal did not succeed after lock release: proposal=%+v replayed=%v err=%v", proposal, replayed, err)
	}
}
