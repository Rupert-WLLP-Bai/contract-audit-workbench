package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/accounts"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCaseIntakeRecoversAcrossBoundSessionsPostgres(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required")
	}
	core := env.openRole(t, "core")
	document := env.openRole(t, "document")
	document.UseOperationAuthority(durableTestOperationAuthority{core: core})
	owner := intakeTestUser(t, "demo-clerk")
	ctx := context.Background()
	login := func() (context.Context, string) {
		t.Helper()
		token, err := core.EstablishSession(owner, "client")
		if err != nil {
			t.Fatal(err)
		}
		return WithAdmissionSessionToken(ctx, token), token
	}
	ctxA, tokenA := login()
	userA, _, err := core.AuthenticateSession(ctxA, tokenA)
	if err != nil {
		t.Fatal(err)
	}
	input := CaseIntakeInput{Title: "跨会话续办", ContractType: "service", BusinessLine: "ENT", Scenario: "cloud_services", Filename: "contract.txt", MediaType: "text/plain", Body: []byte("same original contract"), IdempotencyKey: "session-recovery"}
	first, err := core.BeginCaseIntake(ctxA, userA, input)
	if err != nil {
		t.Fatalf("begin in session A: %v", err)
	}
	_, _, _, requestDigestExpected, err := normalizeCaseIntakeInput(input)
	if err != nil {
		t.Fatal(err)
	}
	var originalRequest []byte
	if err := env.harness.pool.QueryRow(ctx, `SELECT request_json FROM case_intake_requests WHERE operation_id=$1`, first.OperationID).Scan(&originalRequest); err != nil {
		t.Fatal(err)
	}
	uploader := NewDirectCaseIntakeUploader(document)
	uploadA, err := uploader.UploadPrimaryContract(ctxA, first.CaseID, PrimaryContractUploadRequest{
		User: userA, Filename: input.Filename, MediaType: input.MediaType, Body: input.Body, IdempotencyKey: first.UploadKey,
	})
	if err != nil {
		t.Fatalf("document committed in session A: %v", err)
	}
	ctxB, tokenB := login()
	userB, _, err := core.AuthenticateSession(ctxB, tokenB)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := core.BeginCaseIntake(ctxB, userB, input)
	if err != nil || resumed.OperationID != first.OperationID {
		t.Fatalf("resume same original operation in session B: %+v, %v", resumed, err)
	}
	ticket, err := core.CreateDocumentIntakeTicket(ctxB, userB, tokenB, first.OperationID, first.CaseID, first.UploadKey, input.Body)
	if err != nil {
		t.Fatalf("issue current-session ticket: %v", err)
	}
	if _, err := core.ResolveDocumentIntakeTicket(ctxB, ticket, caseIntakeDigest(input.Body)); err != nil {
		t.Fatalf("redeem current-session ticket: %v", err)
	}
	uploadB, err := uploader.UploadPrimaryContract(ctxB, first.CaseID, PrimaryContractUploadRequest{
		User: userB, Filename: input.Filename, MediaType: input.MediaType, Body: input.Body, IdempotencyKey: first.UploadKey,
	})
	if err != nil || !uploadB.Replayed || uploadB.DocumentID != uploadA.DocumentID || uploadB.MaterialID != uploadA.MaterialID {
		t.Fatalf("recover original document receipt: %+v, %v", uploadB, err)
	}
	complete, err := core.CompleteCaseIntake(ctxB, userB, first.OperationID, uploadB)
	if err != nil || complete.Status != caseIntakeCompletedStatus {
		t.Fatalf("complete in current session: %+v, %v", complete, err)
	}
	replayed, err := core.BeginCaseIntake(ctxB, userB, input)
	if err != nil || !bytes.Equal(replayed.ReceiptBody, complete.ReceiptBody) {
		t.Fatalf("replay frozen completion: %+v, %v", replayed, err)
	}
	var originalSession, completingSession, requestDigest, responseDigest string
	var originalVersion, completingVersion int64
	var currentRequest []byte
	err = env.harness.pool.QueryRow(ctx, `SELECT o.command_session,o.command_version,e.session_digest,e.context_version,e.request_digest,e.response_digest
		FROM case_intake_operations o JOIN case_intake_completion_events e USING (tenant_id,operation_id) WHERE o.operation_id=$1`, first.OperationID).
		Scan(&originalSession, &originalVersion, &completingSession, &completingVersion, &requestDigest, &responseDigest)
	if err != nil || originalSession != sessionTokenDigest(tokenA) || originalVersion != 1 || completingSession != sessionTokenDigest(tokenB) || completingVersion != 1 || responseDigest != complete.Digest || requestDigest != requestDigestExpected {
		t.Fatalf("original stamp or atomic current-session audit changed: err=%v", err)
	}
	if err := env.harness.pool.QueryRow(ctx, `SELECT request_json FROM case_intake_requests WHERE operation_id=$1`, first.OperationID).Scan(&currentRequest); err != nil || !bytes.Equal(originalRequest, currentRequest) {
		t.Fatalf("original request changed during recovery: %v", err)
	}
}

// PostgreSQL checks EXECUTE for policy expressions before CASE evaluation.
// This records why read-projection consumers need the function ACL even though
// the function never returns a recovery row to those service identities.
func TestIntakeRecoveryPolicyProjectionExecuteRequirement(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required")
	}
	execution := env.openRole(t, "execution")
	cfg, err := pgx.ParseConfig(env.roleDSNs["execution"])
	if err != nil {
		t.Fatal(err)
	}
	qualified := pgx.Identifier{env.harness.schema}.Sanitize() + `.tenant_intake_upload_recovery_allowed(text,text,text,text,text)`
	role := pgx.Identifier{cfg.User}.Sanitize()
	ctx := context.Background()
	if _, err := env.harness.pool.Exec(ctx, `REVOKE EXECUTE ON FUNCTION `+qualified+` FROM `+role); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := env.harness.pool.Exec(ctx, `GRANT EXECUTE ON FUNCTION `+qualified+` TO `+role); err != nil {
			t.Errorf("restore isolated policy ACL: %v", err)
		}
	})
	var count int
	err = execution.pool.QueryRow(ctx, `SELECT count(*) FROM document_upload_idempotency`).Scan(&count)
	var failure *pgconn.PgError
	if !errors.As(err, &failure) || failure.Code != "42501" {
		t.Fatalf("expected permission failure without policy EXECUTE, got %v", err)
	}
	t.Log("existing Execution projection without policy EXECUTE: SQLSTATE 42501")
}

func TestCaseIntakeSessionRecoveryBoundariesPostgres(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required")
	}
	core, secondCore := env.openRole(t, "core"), env.openRole(t, "core")
	document, secondDocument := env.openRole(t, "document"), env.openRole(t, "document")
	execution, model, tool := env.openRole(t, "execution"), env.openRole(t, "model"), env.openRole(t, "tool")
	for _, service := range []*Store{core, document} {
		var sessionUser, currentUser string
		var login, super, bypass, createRole, createDB bool
		if err := service.pool.QueryRow(context.Background(), `SELECT session_user,current_user,rolcanlogin,rolsuper,rolbypassrls,rolcreaterole,rolcreatedb FROM pg_roles WHERE rolname=current_user`).Scan(&sessionUser, &currentUser, &login, &super, &bypass, &createRole, &createDB); err != nil || sessionUser != currentUser || !login || super || bypass || createRole || createDB {
			t.Fatalf("%s is not an ordinary RLS-bound service role: %v", service.serviceOwner, err)
		}
		t.Logf("%s: LOGIN, NOSUPERUSER, NOBYPASSRLS, NOCREATEROLE, NOCREATEDB", service.serviceOwner)
	}
	document.UseOperationAuthority(durableTestOperationAuthority{core: core})
	secondDocument.UseOperationAuthority(durableTestOperationAuthority{core: core})
	base := context.Background()
	owner := intakeTestUser(t, "demo-clerk")
	login := func(profile accounts.User) (context.Context, accounts.User, string) {
		t.Helper()
		token, err := core.EstablishSession(profile, "client")
		if err != nil {
			t.Fatal(err)
		}
		user, _, err := core.AuthenticateSession(base, token)
		if err != nil {
			t.Fatal(err)
		}
		return WithAdmissionSessionToken(base, token), user, token
	}
	ctxA, userA, _ := login(owner)
	ctxB, userB, tokenB := login(owner)
	ctxC, userC, _ := login(owner)
	input := func(key string) CaseIntakeInput {
		return CaseIntakeInput{Title: "恢复边界", ContractType: "service", BusinessLine: "ENT", Scenario: "cloud_services", Filename: "original.txt", MediaType: "text/plain", Body: []byte("original contents"), IdempotencyKey: key}
	}
	begin := func(value CaseIntakeInput) CaseIntakeOperation {
		t.Helper()
		operation, err := core.BeginCaseIntake(ctxA, userA, value)
		if err != nil {
			t.Fatal(err)
		}
		return operation
	}
	upload := func(st *Store, ctx context.Context, user accounts.User, operation CaseIntakeOperation, value CaseIntakeInput) (DocumentUploadResult, error) {
		return NewDirectCaseIntakeUploader(st).UploadPrimaryContract(ctx, operation.CaseID, PrimaryContractUploadRequest{User: user, Filename: value.Filename, MediaType: value.MediaType, Body: value.Body, IdempotencyKey: operation.UploadKey})
	}

	value := input("recovery-boundaries-concurrent")
	operation := begin(value)
	for name, change := range map[string]func(*CaseIntakeInput){
		"filename": func(in *CaseIntakeInput) { in.Filename = "another.txt" },
		"content":  func(in *CaseIntakeInput) { in.Body = []byte("different content") },
		"size":     func(in *CaseIntakeInput) { in.Body = append(in.Body, '!') },
	} {
		changed := value
		changed.Body = append([]byte(nil), value.Body...)
		change(&changed)
		if _, err := core.BeginCaseIntake(ctxB, userB, changed); !isConflictFault(err) {
			t.Fatalf("changed %s was accepted: %v", name, err)
		}
	}
	if _, err := core.CreateDocumentIntakeTicket(ctxB, userB, tokenB, operation.OperationID, operation.CaseID, operation.UploadKey, []byte("changed contents!")); err == nil {
		t.Fatal("changed bytes received an intake Document ticket")
	}
	firstUpload, err := upload(document, ctxA, userA, operation, value)
	if err != nil {
		t.Fatal(err)
	}
	function := pgx.Identifier{env.harness.schema}.Sanitize() + `.tenant_intake_upload_recovery_allowed(text,text,text,text,text)`
	for _, name := range []string{"core", "document", "execution", "model", "tool"} {
		cfg, err := pgx.ParseConfig(env.roleDSNs[name])
		if err != nil {
			t.Fatal(err)
		}
		var allowed bool
		if err := env.harness.pool.QueryRow(base, `SELECT has_function_privilege($1,$2,'EXECUTE')`, cfg.User, function).Scan(&allowed); err != nil || !allowed {
			t.Fatalf("%s recovery function ACL: allowed=%t err=%v", name, allowed, err)
		}
		var auditRead, auditInsert bool
		if err := env.harness.pool.QueryRow(base, `SELECT has_table_privilege($1,$2,'SELECT'),has_table_privilege($1,$2,'INSERT')`, cfg.User, pgx.Identifier{env.harness.schema, "case_intake_completion_events"}.Sanitize()).Scan(&auditRead, &auditInsert); err != nil || auditRead || auditInsert {
			t.Fatalf("%s has direct completion audit access: read=%t insert=%t err=%v", name, auditRead, auditInsert, err)
		}
	}
	var publicExecute bool
	if err := env.harness.pool.QueryRow(base, `SELECT EXISTS (SELECT 1 FROM pg_proc p CROSS JOIN LATERAL aclexplode(COALESCE(p.proacl,acldefault('f',p.proowner))) a WHERE p.oid=$1::regprocedure AND a.grantee=0 AND a.privilege_type='EXECUTE')`, function).Scan(&publicExecute); err != nil || publicExecute {
		t.Fatalf("PUBLIC can execute intake recovery function: %t %v", publicExecute, err)
	}
	for _, service := range []*Store{execution, model, tool} {
		workerTx, err := service.beginContextTx(commandContext(ctxB, userB), pgx.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := setLocalScope(ctxB, workerTx, userScope(userB)); err != nil {
			t.Fatal(err)
		}
		var visible int
		if err := workerTx.QueryRow(ctxB, `SELECT count(*) FROM document_upload_idempotency WHERE case_id=$1`, operation.CaseID).Scan(&visible); err != nil || visible != 0 {
			t.Fatalf("%s ordinary receipt query: count=%d err=%v", service.serviceOwner, visible, err)
		}
		if err := setIntakeRecoverySelector(ctxB, workerTx, operation.OperationID, "", operation.UploadKey); err != nil {
			t.Fatal(err)
		}
		if err := workerTx.QueryRow(ctxB, `SELECT count(*) FROM document_upload_idempotency WHERE case_id=$1`, operation.CaseID).Scan(&visible); err != nil || visible != 0 {
			t.Fatalf("%s forged selector crossed the intake boundary: count=%d err=%v", service.serviceOwner, visible, err)
		}
		if _, err := workerTx.Exec(ctxB, `INSERT INTO case_intake_completion_events(tenant_id,operation_id,actor_id,request_digest,response_digest) VALUES($1,$2,$3,$4,$4)`, owner.TenantID, operation.OperationID, owner.ID, caseIntakeDigest(nil)); err == nil {
			t.Fatalf("%s wrote a completion audit", service.serviceOwner)
		}
		_ = workerTx.Rollback(ctxB)
	}
	// A caller cannot widen the RLS exception merely by knowing a key: the
	// selector, original operation, current owner and current session must agree.
	tx, err := core.beginContextTx(commandContext(ctxB, userB), pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctxB, tx, userScope(userB)); err != nil {
		t.Fatal(err)
	}
	checkUploadVisible := func(op, key string, want int) {
		t.Helper()
		if err := setIntakeRecoverySelector(ctxB, tx, op, "", key); err != nil {
			t.Fatal(err)
		}
		var got int
		if err := tx.QueryRow(ctxB, `SELECT count(*) FROM document_upload_idempotency WHERE case_id=$1 AND idempotency_key=$2`, operation.CaseID, operation.UploadKey).Scan(&got); err != nil || got != want {
			t.Fatalf("upload recovery selector visibility = %d, want %d: %v", got, want, err)
		}
	}
	checkUploadVisible("", "", 0)
	checkUploadVisible("intake_0123456789abcdef", operation.UploadKey, 0)
	checkUploadVisible(operation.OperationID, "intake-upload-0123456789abcdef", 0)
	checkUploadVisible(operation.OperationID, operation.UploadKey, 1)
	_ = tx.Rollback(ctxB)

	receiptBody, err := json.Marshal(CaseIntakeReceipt{OperationID: operation.OperationID, CaseID: operation.CaseID, State: caseIntakeCompletedState, DocumentID: firstUpload.DocumentID, MaterialID: firstUpload.MaterialID, MaterialVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	for name, mutation := range map[string]string{
		"actor":   `actor_id='another-actor',`,
		"tenant":  `tenant_id='another-tenant',`,
		"org":     `org_id='another-org',`,
		"request": `request_digest=repeat('0',64),`,
		"key":     `idempotency_key='another-key',`,
		"stamp":   `command_session='sha256:'||repeat('0',64),`,
	} {
		tx, err := core.beginContextTx(commandContext(ctxB, userB), pgx.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := setLocalScope(ctxB, tx, userScope(userB)); err != nil {
			t.Fatal(err)
		}
		if err := setIntakeRecoverySelector(ctxB, tx, operation.OperationID, "", operation.UploadKey); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctxB, `UPDATE case_intake_operations SET `+mutation+` status='completed',response_status=201,response_body=$2,response_digest=$3 WHERE operation_id=$1`, operation.OperationID, receiptBody, caseIntakeDigest(receiptBody))
		_ = tx.Rollback(ctxB)
		if err == nil {
			t.Fatalf("completion changed immutable %s", name)
		}
	}
	forgedBody, _ := json.Marshal(CaseIntakeReceipt{OperationID: operation.OperationID, CaseID: operation.CaseID, State: caseIntakeCompletedState, DocumentID: "doc_0123456789abcdef", MaterialID: firstUpload.MaterialID, MaterialVersion: 1})
	tx, err = core.beginContextTx(commandContext(ctxB, userB), pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctxB, tx, userScope(userB)); err != nil {
		t.Fatal(err)
	}
	if err := setIntakeRecoverySelector(ctxB, tx, operation.OperationID, "", operation.UploadKey); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctxB, `UPDATE case_intake_operations SET status='completed',response_status=201,response_body=$2,response_digest=$3 WHERE operation_id=$1`, operation.OperationID, forgedBody, caseIntakeDigest(forgedBody))
	_ = tx.Rollback(ctxB)
	if err == nil {
		t.Fatal("completion accepted a fabricated Document result")
	}
	var prematureEvents int
	if err := env.harness.pool.QueryRow(base, `SELECT count(*) FROM case_intake_completion_events WHERE operation_id=$1`, operation.OperationID).Scan(&prematureEvents); err != nil || prematureEvents != 0 {
		t.Fatalf("rejected updates left completion events: %d %v", prematureEvents, err)
	}

	ordinaryOptions := MaterialUploadOptions{Intent: "add", Purpose: "other", IdempotencyKey: "ordinary-upload-key"}
	if _, err := document.UploadMaterial(ctxA, userA, operation.CaseID, "attachment", "other.txt", "text/plain", []byte("ordinary"), ordinaryOptions); err != nil {
		t.Fatalf("ordinary upload fixture: %v", err)
	}
	ordinaryHash := uploadRequestHash("upload", owner.ID, operation.CaseID, "attachment", "other", "other.txt", "text/plain", []byte("ordinary"), ordinaryOptions)
	if _, found, err := secondDocument.readUploadReceipt(ctxB, userB, operation.CaseID, ordinaryOptions.IdempotencyKey, ordinaryHash); err != nil || found {
		t.Fatalf("ordinary upload crossed the original session boundary: found=%t err=%v", found, err)
	}
	adminCtx, admin, _ := login(intakeTestUser(t, "demo-admin"))
	if err := core.AddMember(userA, operation.CaseID, admin.ID); err != nil {
		t.Fatal(err)
	}
	adminCase, err := core.snapshotCaseDetail(adminCtx, admin, operation.CaseID)
	if err != nil {
		t.Fatal(err)
	}
	item, err := core.visibleCase(&adminCase, admin, operation.CaseID)
	if err != nil || !authorizeCase(item, admin, "case.manage") {
		t.Fatalf("test administrator lacks Manage: %v", err)
	}
	if _, err := core.GetCaseIntake(adminCtx, admin, operation.OperationID); err != nil {
		t.Fatalf("case member lost access to ordinary pending receipt: %v", err)
	}
	if _, err := core.GetCaseIntakeContinuation(adminCtx, admin, operation.OperationID); err == nil {
		t.Fatal("case member read another actor's original continuation request")
	}
	viewerCtx, viewer, _ := login(intakeTestUser(t, "demo-viewer"))
	if err := core.AddMember(userA, operation.CaseID, viewer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := core.GetCaseIntake(viewerCtx, viewer, operation.OperationID); err != nil {
		t.Fatalf("viewer lost ordinary case receipt access: %v", err)
	}
	// Legacy/demo rows must not turn the case owner or a read-only member into
	// upload authority merely because the actor ID matches a pending operation.
	viewerOperation, err := newID("intake_")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.harness.pool.Exec(base, `INSERT INTO case_intake_operations(tenant_id,org_id,operation_id,actor_id,idempotency_key,request_digest,case_id,upload_idempotency_key,status,created_at,updated_at)
		VALUES($1,$2,$3,$4,'viewer-fixture',$5,$6,$7,'pending',clock_timestamp(),clock_timestamp())`, viewer.TenantID, viewer.OrgID, viewerOperation, viewer.ID, caseIntakeDigest(nil), operation.CaseID, "intake-upload-viewer-fixture"); err != nil {
		t.Fatal(err)
	}
	viewerTx, err := core.beginContextTx(commandContext(viewerCtx, viewer), pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(viewerCtx, viewerTx, userScope(viewer)); err != nil {
		t.Fatal(err)
	}
	if err := setIntakeRecoverySelector(viewerCtx, viewerTx, viewerOperation, "", ""); err != nil {
		t.Fatal(err)
	}
	var viewerCanRecover bool
	if err := viewerTx.QueryRow(viewerCtx, `SELECT tenant_intake_recovery_allowed($1,$2,$3,$4,$5,$6)`, viewer.TenantID, viewer.OrgID, operation.CaseID, viewer.ID, viewerOperation, "viewer-fixture").Scan(&viewerCanRecover); err != nil || viewerCanRecover {
		t.Fatalf("demo viewer acquired upload authority: allowed=%t err=%v", viewerCanRecover, err)
	}
	_ = viewerTx.Rollback(viewerCtx)
	if _, err := core.CompleteCaseIntake(adminCtx, admin, operation.OperationID, firstUpload); err == nil {
		t.Fatal("a different actor with Manage completed the original owner's pending intake")
	}
	readResults := make(chan error, 8)
	var reads sync.WaitGroup
	for range 8 {
		reads.Add(1)
		go func() {
			defer reads.Done()
			readCtx, cancel := context.WithTimeout(ctxB, 5*time.Second)
			defer cancel()
			_, err := core.ListPendingCaseIntakes(readCtx, userB, 10, "")
			readResults <- err
		}()
	}
	reads.Wait()
	close(readResults)
	for err := range readResults {
		if err != nil {
			t.Fatalf("concurrent fenced reads exhausted the service pool: %v", err)
		}
	}
	for _, foreign := range []string{"demo-otherorg-clerk", "demo-tenant2-admin"} {
		foreignCtx, foreignUser, _ := login(intakeTestUser(t, foreign))
		if _, err := core.GetCaseIntakeContinuation(foreignCtx, foreignUser, operation.OperationID); err == nil {
			t.Fatalf("%s read another owner's continuation", foreign)
		}
		if _, err := core.CompleteCaseIntake(foreignCtx, foreignUser, operation.OperationID, firstUpload); err == nil {
			t.Fatalf("%s completed another owner's continuation", foreign)
		}
	}
	proof, ok := userB.CommandAuthority()
	if !ok {
		t.Fatal("current session did not carry command proof")
	}
	stale := BindCommandUser(userB, tokenB, "999", proof.Revision)
	if _, err := core.BeginCaseIntake(ctxB, stale, value); err == nil {
		t.Fatal("old context version entered intake recovery")
	}
	type answer struct {
		operation CaseIntakeOperation
		err       error
	}
	answers := make(chan answer, 2)
	var group sync.WaitGroup
	for _, actor := range []struct {
		core *Store
		doc  *Store
		ctx  context.Context
		user accounts.User
	}{{core, document, ctxB, userB}, {secondCore, secondDocument, ctxC, userC}} {
		group.Add(1)
		go func() {
			defer group.Done()
			op, err := actor.core.BeginCaseIntake(actor.ctx, actor.user, value)
			if err == nil && op.Status != caseIntakeCompletedStatus {
				var uploaded DocumentUploadResult
				uploaded, err = upload(actor.doc, actor.ctx, actor.user, op, value)
				if err == nil {
					op, err = actor.core.CompleteCaseIntake(actor.ctx, actor.user, op.OperationID, uploaded)
				}
			}
			answers <- answer{op, err}
		}()
	}
	group.Wait()
	close(answers)
	var receipt []byte
	for answer := range answers {
		if answer.err != nil || answer.operation.Status != caseIntakeCompletedStatus || answer.operation.Receipt.DocumentID != firstUpload.DocumentID {
			t.Fatalf("concurrent recovery failed: %+v %v", answer.operation, answer.err)
		}
		if receipt != nil && !bytes.Equal(receipt, answer.operation.ReceiptBody) {
			t.Fatal("concurrent recoveries returned different frozen receipts")
		}
		receipt = answer.operation.ReceiptBody
	}
	if memberReceipt, err := core.GetCaseIntake(adminCtx, admin, operation.OperationID); err != nil || !bytes.Equal(memberReceipt.ReceiptBody, receipt) {
		t.Fatalf("case member lost access to the ordinary completed receipt: %v", err)
	}
	var documents, versions, events int
	if err := env.harness.pool.QueryRow(base, `SELECT (SELECT count(*) FROM documents WHERE case_id=$1 AND role='draft_contract'),(SELECT count(*) FROM material_versions v JOIN material_entities m USING (tenant_id,org_id,case_id,material_id) WHERE v.case_id=$1 AND m.purpose='primary_contract'),(SELECT count(*) FROM case_intake_completion_events WHERE operation_id=$2)`, operation.CaseID, operation.OperationID).Scan(&documents, &versions, &events); err != nil || documents != 1 || versions != 1 || events != 1 {
		t.Fatalf("concurrent recovery duplicated facts: documents=%d versions=%d events=%d err=%v", documents, versions, events, err)
	}

	firstRaceInput := input("recovery-concurrent-first-upload")
	firstRace := begin(firstRaceInput)
	firstRaceAnswers := make(chan answer, 2)
	group = sync.WaitGroup{}
	for _, actor := range []struct {
		core *Store
		doc  *Store
		ctx  context.Context
		user accounts.User
	}{{core, document, ctxB, userB}, {secondCore, secondDocument, ctxC, userC}} {
		group.Add(1)
		go func() {
			defer group.Done()
			op, err := actor.core.BeginCaseIntake(actor.ctx, actor.user, firstRaceInput)
			if err == nil && op.Status != caseIntakeCompletedStatus {
				var uploaded DocumentUploadResult
				uploaded, err = upload(actor.doc, actor.ctx, actor.user, op, firstRaceInput)
				if err == nil {
					op, err = actor.core.CompleteCaseIntake(actor.ctx, actor.user, op.OperationID, uploaded)
				}
			}
			firstRaceAnswers <- answer{op, err}
		}()
	}
	group.Wait()
	close(firstRaceAnswers)
	var firstRaceReceipt []byte
	for answer := range firstRaceAnswers {
		if answer.err != nil || answer.operation.Status != caseIntakeCompletedStatus {
			t.Fatalf("concurrent initial upload failed: %+v %v", answer.operation, answer.err)
		}
		if firstRaceReceipt != nil && !bytes.Equal(firstRaceReceipt, answer.operation.ReceiptBody) {
			t.Fatal("concurrent initial uploads returned different frozen receipts")
		}
		firstRaceReceipt = answer.operation.ReceiptBody
	}
	if err := env.harness.pool.QueryRow(base, `SELECT (SELECT count(*) FROM documents WHERE case_id=$1),(SELECT count(*) FROM material_versions WHERE case_id=$1),(SELECT count(*) FROM case_intake_completion_events WHERE operation_id=$2)`, firstRace.CaseID, firstRace.OperationID).Scan(&documents, &versions, &events); err != nil || documents != 1 || versions != 1 || events != 1 {
		t.Fatalf("concurrent initial upload duplicated facts: documents=%d versions=%d events=%d err=%v", documents, versions, events, err)
	}

	archivedInput := input("recovery-personal-archive")
	archived := begin(archivedInput)
	started, err := upload(document, ctxA, userA, archived, archivedInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ArchiveCaseIntake(ctxB, userB, archived.OperationID); err != nil {
		t.Fatalf("cross-session personal archive: %v", err)
	}
	if _, err := core.BeginCaseIntake(ctxB, userB, archivedInput); err == nil {
		t.Fatal("personal archive allowed a new retry")
	}
	if done, err := core.CompleteCaseIntake(ctxB, userB, archived.OperationID, started); err != nil || done.Status != caseIntakeCompletedStatus {
		t.Fatalf("personal archive blocked an already-started upload: %+v %v", done, err)
	}
	caseArchivedInput := input("recovery-archived-case")
	caseArchived := begin(caseArchivedInput)
	if err := core.updateCaseAs(userA, caseArchived.CaseID, func(st *state) error {
		for i := range st.Cases {
			if st.Cases[i].ID == caseArchived.CaseID {
				archivedAt := stamp()
				st.Cases[i].BusinessStatus, st.Cases[i].ArchivedAt = "archived", &archivedAt
				return nil
			}
		}
		return errors.New("archived case fixture missing")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := core.BeginCaseIntake(ctxB, userB, caseArchivedInput); err == nil {
		t.Fatal("archived case allowed a new intake retry")
	}
	if _, err := core.CreateDocumentIntakeTicket(ctxB, userB, tokenB, caseArchived.OperationID, caseArchived.CaseID, caseArchived.UploadKey, caseArchivedInput.Body); err == nil {
		t.Fatal("archived case allowed Document authority")
	}

	revokedInput := input("recovery-revoked")
	revoked := begin(revokedInput)
	if err := core.updateCaseAs(userA, revoked.CaseID, func(st *state) error {
		for i := range st.Cases {
			if st.Cases[i].ID == revoked.CaseID {
				st.Cases[i].MemberIDs = nil
				return nil
			}
		}
		return errors.New("revocation fixture missing")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := core.BeginCaseIntake(ctxB, userB, revokedInput); err == nil {
		t.Fatal("revoked case member resumed upload")
	}
	if _, err := core.CreateDocumentIntakeTicket(ctxB, userB, tokenB, revoked.OperationID, revoked.CaseID, revoked.UploadKey, revokedInput.Body); err == nil {
		t.Fatal("revoked case member received Document authority")
	}
	revokedTx, err := core.beginContextTx(commandContext(ctxB, userB), pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctxB, revokedTx, userScope(userB)); err != nil {
		t.Fatal(err)
	}
	if err := setIntakeRecoverySelector(ctxB, revokedTx, revoked.OperationID, "", ""); err != nil {
		t.Fatal(err)
	}
	var revokedCanRecover bool
	if err := revokedTx.QueryRow(ctxB, `SELECT tenant_intake_recovery_allowed($1,$2,$3,$4,$5,$6)`, owner.TenantID, owner.OrgID, revoked.CaseID, owner.ID, revoked.OperationID, revokedInput.IdempotencyKey).Scan(&revokedCanRecover); err != nil || revokedCanRecover {
		t.Fatalf("case owner retained SQL recovery authority after membership removal: allowed=%t err=%v", revokedCanRecover, err)
	}
	_ = revokedTx.Rollback(ctxB)
}
