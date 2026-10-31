package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

func requireIntakeFault(t *testing.T, err error, status int, code string) {
	t.Helper()
	var failure *fault.Error
	if !errors.As(err, &failure) || failure.Status != status || failure.Code != code {
		t.Fatalf("intake fault = %v, want %d %s", err, status, code)
	}
}

func intakeTestUser(t *testing.T, id string) accounts.User {
	t.Helper()
	user, ok := accounts.ByID(id)
	if !ok {
		t.Fatalf("test identity %q unavailable", id)
	}
	return user
}

func TestCaseIntakePendingOwnerResumeArchiveAndReceiptPostgres(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for intake persistence")
	}
	core, document := env.openRole(t, "core"), env.openRole(t, "document")
	owner := intakeTestUser(t, "demo-clerk")
	admin := intakeTestUser(t, "demo-admin")
	member := intakeTestUser(t, "demo-reviewer")
	var otherTenant accounts.User
	for _, user := range accounts.List() {
		if user.TenantID != owner.TenantID && user.Role == "admin" {
			otherTenant = user
			break
		}
	}
	if otherTenant.ID == "" {
		t.Fatal("second tenant administrator unavailable")
	}
	ctx := context.Background()
	firstInput := CaseIntakeInput{Title: "  第一份合同  ", ContractType: "service", BusinessLine: "ENT", Scenario: "cloud_services",
		Filename: "folder\\first.txt", MediaType: "text/plain; charset=utf-8", Body: []byte("first contract content"), IdempotencyKey: "pending-owner-first"}
	secondInput := CaseIntakeInput{Title: "第二份合同", ContractType: "service", BusinessLine: "ENT", Scenario: "cloud_services",
		Filename: "second.txt", MediaType: "text/plain", Body: []byte("second contract content"), IdempotencyKey: "pending-owner-second"}
	first, err := core.BeginCaseIntake(ctx, owner, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	second, err := core.BeginCaseIntake(ctx, owner, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if first.CaseID == second.CaseID || first.OperationID == second.OperationID {
		t.Fatal("distinct intake commands did not create distinct cases and operations")
	}
	// A second Core instance must recover the pending commands from PostgreSQL,
	// without relying on the first instance's in-memory projection.
	reopened := env.openRole(t, "core")
	for _, user := range []accounts.User{admin, member} {
		if err := core.AddMember(owner, first.CaseID, user.ID); err != nil {
			t.Fatalf("add case member %s: %v", user.ID, err)
		}
		if _, err := core.GetCaseIntake(ctx, user, first.OperationID); err != nil {
			t.Fatalf("case member cannot read ordinary pending receipt: %v", err)
		}
		_, err := core.GetCaseIntakeContinuation(ctx, user, first.OperationID)
		requireIntakeFault(t, err, 404, "not_found")
		_, err = core.ArchiveCaseIntake(ctx, user, first.OperationID)
		requireIntakeFault(t, err, 404, "not_found")
		page, err := core.ListPendingCaseIntakes(ctx, user, 10, "")
		if err != nil || len(page.Items) != 0 {
			t.Fatalf("case member saw another actor's pending operations: %+v %v", page, err)
		}
	}
	_, err = core.GetCaseIntakeContinuation(ctx, otherTenant, first.OperationID)
	requireIntakeFault(t, err, 404, "not_found")
	_, err = core.ArchiveCaseIntake(ctx, otherTenant, first.OperationID)
	requireIntakeFault(t, err, 404, "not_found")
	otherPage, err := core.ListPendingCaseIntakes(ctx, otherTenant, 10, "")
	if err != nil || len(otherPage.Items) != 0 {
		t.Fatalf("other tenant saw pending operations: %+v %v", otherPage, err)
	}
	forged := owner
	forged.TenantID = otherTenant.TenantID
	_, err = core.ListPendingCaseIntakes(ctx, forged, 10, "")
	requireIntakeFault(t, err, 401, "unauthenticated")

	page1, err := reopened.ListPendingCaseIntakes(ctx, accounts.User{ID: owner.ID}, 1, "")
	if err != nil || len(page1.Items) != 1 || page1.NextCursor == nil {
		t.Fatalf("first pending page: %+v %v", page1, err)
	}
	page2, err := reopened.ListPendingCaseIntakes(ctx, accounts.User{ID: owner.ID}, 1, *page1.NextCursor)
	if err != nil || len(page2.Items) != 1 || page2.NextCursor != nil || page1.Items[0].OperationID == page2.Items[0].OperationID {
		t.Fatalf("second pending page: %+v %v", page2, err)
	}
	_, err = core.ListPendingCaseIntakes(ctx, admin, 1, *page1.NextCursor)
	requireIntakeFault(t, err, 422, "invalid")
	seen := map[string]bool{page1.Items[0].OperationID: true, page2.Items[0].OperationID: true}
	if !seen[first.OperationID] || !seen[second.OperationID] {
		t.Fatalf("paging lost a pending operation: %+v %+v", page1, page2)
	}

	continuation, err := reopened.GetCaseIntakeContinuation(ctx, accounts.User{ID: owner.ID}, first.OperationID)
	if err != nil || !continuation.CanResume || !continuation.CanArchive || continuation.Request == nil || continuation.IdempotencyKey == nil {
		t.Fatalf("original continuation unavailable: %+v %v", continuation, err)
	}
	identity := *continuation.Request
	if *continuation.IdempotencyKey != firstInput.IdempotencyKey || identity.Title != "第一份合同" || identity.FileName != "first.txt" ||
		identity.MediaType != "text/plain" || identity.FileSize != int64(len(firstInput.Body)) || identity.FileSHA256 != caseIntakeDigest(firstInput.Body) {
		t.Fatalf("original normalized identity is incorrect: %+v", identity)
	}
	if err := core.updateCaseAs(owner, first.CaseID, func(st *state) error {
		for i := range st.Cases {
			if st.Cases[i].ID == first.CaseID {
				st.Cases[i].Title = "后来修改的案件标题"
				return nil
			}
		}
		return errors.New("intake case missing")
	}); err != nil {
		t.Fatal(err)
	}
	afterChange, err := core.GetCaseIntakeContinuation(ctx, owner, first.OperationID)
	if err != nil || afterChange.Request == nil || *afterChange.Request != identity {
		t.Fatalf("continuation was rebuilt from mutable case data: %+v %v", afterChange, err)
	}
	for name, change := range map[string]func(*CaseIntakeInput){
		"file bytes": func(input *CaseIntakeInput) { input.Body = []byte("different contract") },
		"file name":  func(input *CaseIntakeInput) { input.Filename = "different.txt" },
		"media type": func(input *CaseIntakeInput) { input.MediaType = "application/pdf" },
		"title":      func(input *CaseIntakeInput) { input.Title = "不同的建案请求" },
	} {
		changed := firstInput
		change(&changed)
		_, err := core.BeginCaseIntake(ctx, owner, changed)
		if !isConflictFault(err) {
			t.Fatalf("changed %s accepted for original key: %v", name, err)
		}
	}
	reselected := CaseIntakeInput{Title: identity.Title, ContractType: identity.ContractType, BusinessLine: identity.BusinessLine,
		Scenario: identity.Scenario, Filename: identity.FileName, MediaType: identity.MediaType, Body: firstInput.Body, IdempotencyKey: *continuation.IdempotencyKey}
	resumed, err := core.BeginCaseIntake(ctx, owner, reselected)
	if err != nil || resumed.OperationID != first.OperationID || resumed.CaseID != first.CaseID || resumed.UploadKey != first.UploadKey {
		t.Fatalf("resume did not use the original operation and case: %+v %v", resumed, err)
	}
	// The document service has already accepted this upload; delay Core's
	// completion until after the personal archive has committed.
	upload, err := NewDirectCaseIntakeUploader(document).UploadPrimaryContract(ctx, first.CaseID, PrimaryContractUploadRequest{
		ActorID: owner.ID, TenantID: owner.TenantID, OrgID: owner.OrgID, Filename: identity.FileName,
		MediaType: identity.MediaType, Body: firstInput.Body, IdempotencyKey: resumed.UploadKey,
	})
	if err != nil {
		t.Fatalf("document upload before archive: %v", err)
	}
	type archiveAnswer struct {
		result CaseIntakeArchiveResult
		err    error
	}
	answers := make(chan archiveAnswer, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := core.ArchiveCaseIntake(ctx, owner, first.OperationID)
			answers <- archiveAnswer{result, err}
		}()
	}
	group.Wait()
	close(answers)
	var archived CaseIntakeArchiveResult
	for answer := range answers {
		if answer.err != nil || answer.result.OperationID != first.OperationID || answer.result.CaseID != first.CaseID || answer.result.ArchivedAt == "" {
			t.Fatalf("archive did not return its fact: %+v %v", answer.result, answer.err)
		}
		if archived.ArchivedAt != "" && archived != answer.result {
			t.Fatalf("concurrent archives returned different facts: %+v %+v", archived, answer.result)
		}
		archived = answer.result
	}
	_, err = core.BeginCaseIntake(ctx, owner, reselected)
	requireIntakeFault(t, err, 409, "intake_archived")
	_, err = core.GetCaseIntake(ctx, owner, first.OperationID)
	requireIntakeFault(t, err, 409, "intake_archived")
	_, err = core.GetCaseIntakeByKey(ctx, owner, firstInput.IdempotencyKey)
	requireIntakeFault(t, err, 409, "intake_archived")
	archivedDetail, err := core.GetCaseIntakeContinuation(ctx, owner, first.OperationID)
	if err != nil || archivedDetail.CanResume || archivedDetail.CanArchive || archivedDetail.IdempotencyKey != nil || archivedDetail.ReasonCode != "archived" {
		t.Fatalf("archive exposed a retry key: %+v %v", archivedDetail, err)
	}
	remaining, err := core.ListPendingCaseIntakes(ctx, owner, 10, "")
	if err != nil || len(remaining.Items) != 1 || remaining.Items[0].OperationID != second.OperationID {
		t.Fatalf("archive did not hide exactly the selected operation: %+v %v", remaining, err)
	}
	completed, err := core.CompleteCaseIntake(ctx, owner, first.OperationID, upload)
	if err != nil || completed.Status != caseIntakeCompletedStatus || completed.HTTPStatus != 201 || len(completed.ReceiptBody) == 0 {
		t.Fatalf("already-started upload could not complete: %+v %v", completed, err)
	}
	byID, err := core.GetCaseIntake(ctx, owner, first.OperationID)
	if err != nil || !bytes.Equal(byID.ReceiptBody, completed.ReceiptBody) {
		t.Fatalf("completed receipt changed on ID lookup: %+v %v", byID, err)
	}
	byKey, err := core.GetCaseIntakeByKey(ctx, owner, firstInput.IdempotencyKey)
	if err != nil || !bytes.Equal(byKey.ReceiptBody, completed.ReceiptBody) {
		t.Fatalf("completed receipt changed on key lookup: %+v %v", byKey, err)
	}
	replayed, err := core.BeginCaseIntake(ctx, owner, reselected)
	if err != nil || !bytes.Equal(replayed.ReceiptBody, completed.ReceiptBody) {
		t.Fatalf("completed retry did not preserve the original 201 bytes: %+v %v", replayed, err)
	}
	recompleted, err := core.CompleteCaseIntake(ctx, owner, first.OperationID, upload)
	if err != nil || !bytes.Equal(recompleted.ReceiptBody, completed.ReceiptBody) {
		t.Fatalf("repeated completion changed the receipt: %+v %v", recompleted, err)
	}
	again, err := core.ArchiveCaseIntake(ctx, owner, first.OperationID)
	if err != nil || again != archived {
		t.Fatalf("completed archived operation lost its original archive fact: %+v %v", again, err)
	}
}

// Historical commands can predate the metadata table. A malformed descriptor
// is inserted independently, as an importer or damaged producer could have
// persisted it; neither fixture modifies an append-only row.
func seedIntakeRequestFixture(t *testing.T, core *Store, owner accounts.User, key string, withInvalidRequest bool) (string, string) {
	t.Helper()
	ctx := context.Background()
	caseID, err := core.CreateCaseWithBusiness(owner, "历史待续办", "service", "ENT", "cloud_services")
	if err != nil {
		t.Fatal(err)
	}
	operationID, err := newID("intake_")
	if err != nil {
		t.Fatal(err)
	}
	input := CaseIntakeInput{Title: "历史待续办", ContractType: "service", BusinessLine: "ENT", Scenario: "cloud_services",
		Filename: "legacy.txt", MediaType: "text/plain", Body: []byte("legacy body"), IdempotencyKey: key}
	_, _, identity, _, err := normalizeCaseIntakeInput(input)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := core.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(owner)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	// Digest of the pre-continuation canonical request for the fixture above.
	const historicalDigest = "7ba20d821431ca3194ed2ee3e766d157c0a63bd23fe6bd8b142ad367f5f29443"
	_, err = tx.Exec(ctx, `INSERT INTO case_intake_operations
		(tenant_id,org_id,operation_id,actor_id,idempotency_key,request_digest,case_id,upload_idempotency_key,status,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'pending',$9,$9)`, owner.TenantID, owner.OrgID, operationID, owner.ID, key, historicalDigest, caseID,
		"intake-upload-"+strings.TrimPrefix(operationID, "intake_"), now)
	if err != nil {
		t.Fatal(err)
	}
	if withInvalidRequest {
		identity.Title = "与原请求摘要不符"
		body, err := json.Marshal(identity)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO case_intake_requests (tenant_id,operation_id,schema_version,request_json)
			VALUES($1,$2,1,$3)`, owner.TenantID, operationID, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return operationID, caseID
}

func TestCaseIntakePendingLegacyAndCurrentCaseAuthorityPostgres(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for intake persistence")
	}
	core := env.openRole(t, "core")
	owner := intakeTestUser(t, "demo-clerk")
	admin := intakeTestUser(t, "demo-admin")
	ctx := context.Background()
	legacyID, legacyCase := seedIntakeRequestFixture(t, core, owner, "legacy-intake-key", false)
	invalidID, invalidCase := seedIntakeRequestFixture(t, core, owner, "invalid-intake-key", true)
	legacyReplay, err := core.BeginCaseIntake(ctx, owner, CaseIntakeInput{Title: "历史待续办", ContractType: "service", BusinessLine: "ENT",
		Scenario: "cloud_services", Filename: "legacy.txt", MediaType: "text/plain", Body: []byte("legacy body"), IdempotencyKey: "legacy-intake-key"})
	if err != nil || legacyReplay.OperationID != legacyID || legacyReplay.CaseID != legacyCase {
		t.Fatalf("historical request digest no longer replays its original case: %+v %v", legacyReplay, err)
	}
	validInput := CaseIntakeInput{Title: "继续办理", ContractType: "service", BusinessLine: "ENT",
		Scenario: "cloud_services", Filename: "current.txt", MediaType: "", Body: []byte("valid"), IdempotencyKey: "valid-intake-key"}
	valid, err := core.BeginCaseIntake(ctx, owner, validInput)
	if err != nil {
		t.Fatal(err)
	}
	for id, reason := range map[string]string{legacyID: "legacy_request_unavailable", invalidID: "request_identity_invalid"} {
		detail, err := core.GetCaseIntakeContinuation(ctx, owner, id)
		if err != nil || detail.CanResume || !detail.CanArchive || detail.Request != nil || detail.IdempotencyKey != nil || detail.ReasonCode != reason {
			t.Fatalf("unavailable original request %s was treated as resumable: %+v %v", id, detail, err)
		}
	}
	validDetail, err := core.GetCaseIntakeContinuation(ctx, owner, valid.OperationID)
	if err != nil || !validDetail.CanResume || validDetail.Request == nil || validDetail.IdempotencyKey == nil {
		t.Fatalf("request with empty browser media type cannot be resumed: %+v %v", validDetail, err)
	}
	validInput.MediaType = validDetail.Request.MediaType
	retry, err := core.BeginCaseIntake(ctx, owner, validInput)
	if err != nil || retry.OperationID != valid.OperationID || retry.CaseID != valid.CaseID {
		t.Fatalf("normalized media replay did not reuse the original case: %+v %v", retry, err)
	}
	core.UseReadOnly(true)
	readOnly, err := core.GetCaseIntakeContinuation(ctx, owner, valid.OperationID)
	if err != nil || readOnly.CanResume || readOnly.CanArchive || readOnly.IdempotencyKey != nil || readOnly.ReasonCode != "read_only" {
		t.Fatalf("read-only mode advertised a write: %+v %v", readOnly, err)
	}
	_, err = core.ArchiveCaseIntake(ctx, owner, valid.OperationID)
	requireIntakeFault(t, err, 503, "read_only")
	core.UseReadOnly(false)
	if err := core.updateCaseAs(owner, invalidCase, func(st *state) error {
		for i := range st.Cases {
			if st.Cases[i].ID == invalidCase {
				archivedAt := stamp()
				st.Cases[i].BusinessStatus, st.Cases[i].ArchivedAt = "archived", &archivedAt
				return nil
			}
		}
		return errors.New("invalid-request case missing")
	}); err != nil {
		t.Fatal(err)
	}
	caseArchived, err := core.GetCaseIntakeContinuation(ctx, owner, invalidID)
	if err != nil || caseArchived.CanResume || caseArchived.CanArchive || caseArchived.ReasonCode != "case_not_writable" {
		t.Fatalf("archived case advertised intake writes: %+v %v", caseArchived, err)
	}
	if err := core.AddMember(owner, legacyCase, admin.ID); err != nil {
		t.Fatal(err)
	}
	if err := core.updateCaseAs(owner, legacyCase, func(st *state) error {
		for i := range st.Cases {
			if st.Cases[i].ID == legacyCase {
				st.Cases[i].MemberIDs = []string{admin.ID}
				return nil
			}
		}
		return errors.New("legacy case missing")
	}); err != nil {
		t.Fatal(err)
	}
	_, err = core.GetCaseIntakeContinuation(ctx, owner, legacyID)
	requireIntakeFault(t, err, 404, "not_found")
	_, err = core.ArchiveCaseIntake(ctx, owner, legacyID)
	requireIntakeFault(t, err, 404, "not_found")
	page, err := core.ListPendingCaseIntakes(ctx, owner, 1, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].OperationID != valid.OperationID || page.NextCursor != nil {
		t.Fatalf("pending page exposed archived or unauthorized cases: %+v %v", page, err)
	}
}
