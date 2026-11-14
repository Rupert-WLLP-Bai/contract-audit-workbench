// File metadata (manifest mirror):
// Purpose: Verifies immutable report receipt replay across client sessions with a restricted Core PostgreSQL role.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-14
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReportReceiptReplayAfterNewSessionPostgres(t *testing.T) {
	fixture, ok := newDurableTelecomFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	tokenA, err := fixture.core.EstablishSession(fixture.admin, "client")
	if err != nil {
		t.Fatalf("session A login: %v", err)
	}
	actorA, audience, err := fixture.core.AuthenticateSession(context.Background(), tokenA)
	if err != nil || audience != "client" || actorA.ID != fixture.admin.ID {
		t.Fatalf("session A identity: audience=%q actor=%+v err=%v", audience, actorA, err)
	}
	ctxA := WithAdmissionSessionToken(context.Background(), tokenA)
	if err := fixture.execution.RunClaimedJob(ctxA, fixture.claim); err != nil {
		t.Fatalf("complete frozen report attempt: %v", err)
	}
	workspace := waitTerminalWorkspace(t, fixture.execution, fixture.admin, fixture.caseID, fixture.runID)
	if workspace.Execution == nil || workspace.Execution.Attempt == nil || workspace.Execution.Attempt.ID != fixture.attemptID ||
		attemptActive(workspace.Execution.Attempt.State) || workspace.Execution.Attempt.InputHash == nil || workspace.Execution.Attempt.OutputHash == nil {
		var state string
		if workspace.Execution != nil && workspace.Execution.Attempt != nil {
			state = workspace.Execution.Attempt.State
		}
		t.Fatalf("report fixture attempt is not a frozen terminal attempt: state=%q", state)
	}

	const key = "report-lost-response"
	original, err := fixture.core.CreateReportWithIdempotency(ctxA, actorA, fixture.caseID, fixture.runID, fixture.attemptID, key)
	if err != nil {
		t.Fatalf("session A report creation: %v", err)
	}
	_, originalJSON, err := fixture.core.ReadReport(actorA, fixture.caseID, original.ID, "json")
	if err != nil {
		t.Fatal(err)
	}
	_, originalHTML, err := fixture.core.ReadReport(actorA, fixture.caseID, original.ID, "html")
	if err != nil {
		t.Fatal(err)
	}
	jsonHash, htmlHash := sha256.Sum256(originalJSON), sha256.Sum256(originalHTML)
	if got := reportDigest(originalJSON, originalHTML); got != original.Digest {
		t.Fatalf("stored report digest does not bind original bytes: record=%s bytes=%s", original.Digest, got)
	}
	var originalResponse string
	if err := fixture.environment.harness.pool.QueryRow(ctxA, `SELECT response_json::text FROM core_write_receipts
		WHERE case_id=$1 AND route=$2 AND idempotency_key=$3`, fixture.caseID,
		"POST /api/cases/"+fixture.caseID+"/reports", key).Scan(&originalResponse); err != nil {
		t.Fatal(err)
	}
	var originalAuditFacts int
	if err := fixture.environment.harness.pool.QueryRow(ctxA, `SELECT count(*) FROM business_audit_events
		WHERE run_id IN (SELECT id FROM review_runs WHERE case_id=$1)`, fixture.caseID).Scan(&originalAuditFacts); err != nil {
		t.Fatal(err)
	}
	if err := fixture.core.Logout(tokenA); err != nil {
		t.Fatalf("session A logout: %v", err)
	}
	tokenB, err := fixture.core.EstablishSession(fixture.admin, "client")
	if err != nil {
		t.Fatalf("session B login: %v", err)
	}
	actorB, audience, err := fixture.core.AuthenticateSession(context.Background(), tokenB)
	if err != nil || audience != "client" || actorB.ID != actorA.ID || actorB.TenantID != actorA.TenantID || actorB.OrgID != actorA.OrgID {
		t.Fatalf("session B identity: audience=%q actor=%+v err=%v", audience, actorB, err)
	}
	ctxB := WithAdmissionSessionToken(context.Background(), tokenB)
	replayed, err := fixture.core.CreateReportWithIdempotency(ctxB, actorB, fixture.caseID, fixture.runID, fixture.attemptID, key)
	if err != nil || replayed.ID != original.ID || replayed.Version != original.Version || replayed.Digest != original.Digest || replayed.RunID != original.RunID || replayed.AttemptID != original.AttemptID {
		t.Fatalf("session B should return the original report: original=%+v replay=%+v err=%v", original, replayed, err)
	}
	if _, err := fixture.core.CreateReportWithIdempotency(ctxB, actorB, fixture.caseID, fixture.runID+"-changed", fixture.attemptID, key); !isConflictFault(err) {
		t.Fatalf("same key with a changed run must conflict: %v", err)
	}
	if _, err := fixture.core.CreateReportWithIdempotency(ctxB, actorB, fixture.caseID, fixture.runID, fixture.attemptID+"-changed", key); !isConflictFault(err) {
		t.Fatalf("same key with a changed attempt must conflict: %v", err)
	}
	_, replayJSON, err := fixture.core.ReadReport(actorB, fixture.caseID, replayed.ID, "json")
	if err != nil {
		t.Fatal(err)
	}
	_, replayHTML, err := fixture.core.ReadReport(actorB, fixture.caseID, replayed.ID, "html")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(replayJSON, originalJSON) || !bytes.Equal(replayHTML, originalHTML) ||
		sha256.Sum256(replayJSON) != jsonHash || sha256.Sum256(replayHTML) != htmlHash {
		t.Fatal("report replay changed frozen JSON or HTML bytes")
	}

	var reports, receipts, auditFacts int
	var stamp string
	var version int64
	var originOrg, responseAfter string
	err = fixture.environment.harness.pool.QueryRow(ctxB, `SELECT command_session,command_version,
		(SELECT count(*) FROM business_reports WHERE case_id=$1),
		(SELECT count(*) FROM core_write_receipts WHERE case_id=$1 AND route=$2 AND idempotency_key=$3),
		(SELECT count(*) FROM business_audit_events WHERE run_id IN (SELECT id FROM review_runs WHERE case_id=$1)), command_origin_org,response_json::text
		FROM core_write_receipts WHERE case_id=$1 AND route=$2 AND idempotency_key=$3`, fixture.caseID,
		"POST /api/cases/"+fixture.caseID+"/reports", key).Scan(&stamp, &version, &reports, &receipts, &auditFacts, &originOrg, &responseAfter)
	if err != nil || stamp != sessionTokenDigest(tokenA) || version != 1 || reports != 1 || receipts != 1 || auditFacts != originalAuditFacts || originOrg != actorA.OrgID || responseAfter != originalResponse {
		t.Fatalf("replay changed original stamp/response/origin or duplicated business facts: stamp=%q version=%d reports=%d receipts=%d audit_facts=%d origin_org=%q err=%v", stamp, version, reports, receipts, auditFacts, originOrg, err)
	}
	legacyKey := "legacy-report-origin"
	legacyBody, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	legacyDigest, err := canonicalHash([]string{fixture.caseID, fixture.runID, fixture.attemptID})
	if err != nil {
		t.Fatal(err)
	}
	legacyTx, err := fixture.core.pool.Begin(ctxB)
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctxB, legacyTx, userScope(actorB)); err != nil {
		_ = legacyTx.Rollback(ctxB)
		t.Fatal(err)
	}
	_, err = legacyTx.Exec(ctxB, `INSERT INTO core_write_receipts
		(tenant_id,org_id,actor_id,route,idempotency_key,request_digest,case_id,response_json)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb)`, actorB.TenantID, actorB.OrgID, actorB.ID,
		"POST /api/cases/"+fixture.caseID+"/reports", legacyKey, legacyDigest, fixture.caseID, string(legacyBody))
	if err != nil {
		_ = legacyTx.Rollback(ctxB)
		t.Fatalf("create isolated historical no-origin receipt: %v", err)
	}
	if err := legacyTx.Commit(ctxB); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.core.CreateReportWithIdempotency(ctxB, actorB, fixture.caseID, fixture.runID, fixture.attemptID, legacyKey); !isConflictFault(err) {
		t.Fatalf("receipt without trusted origin must return a controlled conflict: %v", err)
	}
	_, legacyTargetJSON, err := fixture.core.ReadReport(actorB, fixture.caseID, original.ID, "json")
	if err != nil || !bytes.Equal(legacyTargetJSON, originalJSON) {
		t.Fatalf("unreplayable legacy key changed or hid its original report: err=%v", err)
	}
	fixture.core.UseReadOnly(true)
	_, readOnlyErr := fixture.core.CreateReportWithIdempotency(ctxB, actorB, fixture.caseID, fixture.runID, fixture.attemptID, key)
	fixture.core.UseReadOnly(false)
	var readOnlyFault *fault.Error
	if !errors.As(readOnlyErr, &readOnlyFault) || readOnlyFault.Status != 503 || readOnlyFault.Code != "read_only" {
		t.Fatalf("read-only must reject otherwise replayable report with 503/read_only: err=%v", readOnlyErr)
	}
	if err := fixture.core.Logout(tokenB); err != nil {
		t.Fatal(err)
	}
	_, loggedOutErr := fixture.core.CreateReportWithIdempotency(ctxB, actorB, fixture.caseID, fixture.runID, fixture.attemptID, key)
	var loggedOutFault *fault.Error
	if !errors.As(loggedOutErr, &loggedOutFault) || loggedOutFault.Status != 401 || loggedOutFault.Code != "session_invalid" {
		t.Fatalf("logged-out session must be rejected as 401/session_invalid: err=%v", loggedOutErr)
	}
	tokenC, err := fixture.core.EstablishSession(fixture.admin, "client")
	if err != nil {
		t.Fatalf("session C login for independent archive check: %v", err)
	}
	actorC, audience, err := fixture.core.AuthenticateSession(context.Background(), tokenC)
	if err != nil || audience != "client" || actorC.ID != actorA.ID {
		t.Fatalf("session C identity: audience=%q actor=%+v err=%v", audience, actorC, err)
	}
	ctxC := WithAdmissionSessionToken(context.Background(), tokenC)
	if _, err := fixture.core.Archive(actorC, fixture.caseID, original.ID, "冻结报告回执测试后归档"); err != nil {
		t.Fatalf("archive report fixture: %v", err)
	}
	if _, err := fixture.core.CreateReportWithIdempotency(ctxC, actorC, fixture.caseID, fixture.runID, fixture.attemptID, key); !isConflictFault(err) {
		t.Fatalf("archived case must reject report receipt replay: %v", err)
	}
}

func TestReportReceiptReplayPreservesSessionOriginOrganizationPostgres(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated durable role checks")
	}
	core, document, execution := env.openRole(t, "core"), env.openRole(t, "document"), env.openRole(t, "execution")
	authority := durableTestOperationAuthority{core: core}
	document.UseOperationAuthority(authority)
	execution.UseOperationAuthority(authority)
	execution.UseDocumentPreparer(document)
	execution.UseModels(&frozenOCRModel{})
	profile := accounts.User{ID: "report-origin-owner", DisplayName: "Report origin owner", Role: "admin", TenantID: "directory", OrgID: "directory", Enabled: true, Issuer: "https://report-identity.invalid", Subject: "owner"}
	reviewerProfile := accounts.User{ID: "report-origin-reviewer", DisplayName: "Report origin reviewer", Role: "viewer", TenantID: "directory", OrgID: "directory", Enabled: true, Issuer: "https://report-identity.invalid", Subject: "reviewer"}
	raw, err := json.Marshal(map[string]any{"users": []accounts.User{profile, reviewerProfile}})
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "directory.json")
	if err := os.WriteFile(directory, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = accounts.ConfigureDirectory("") })
	const principal = "43800000-0000-4000-8000-000000000001"
	const memberA = "43800000-0000-4000-8000-000000000002"
	const memberB = "43800000-0000-4000-8000-000000000003"
	const memberBReviewer = "43800000-0000-4000-8000-000000000004"
	const reviewerPrincipal = "43800000-0000-4000-8000-000000000005"
	mapping := IdentityCrosswalk{
		Version:    1,
		Tenants:    []IdentityTenantMap{{ID: "report-tenant", DisplayName: "Report tenant", Status: "active"}},
		OrgUnits:   []IdentityOrgUnitMap{{TenantID: "report-tenant", ID: "case-org", DisplayName: "Case org", Status: "active"}, {TenantID: "report-tenant", ID: "origin-org", DisplayName: "Origin org", Status: "active"}},
		Principals: []IdentityPrincipalMap{{ID: principal, ActorID: profile.ID, Issuer: profile.Issuer, Subject: profile.Subject, Status: "active"}, {ID: reviewerPrincipal, ActorID: reviewerProfile.ID, Issuer: reviewerProfile.Issuer, Subject: reviewerProfile.Subject, Status: "active"}},
		Memberships: []IdentityMembershipMap{
			{ID: memberA, ActorID: profile.ID, TenantID: "report-tenant", OrgUnitID: "case-org", RoleCode: "admin", Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}, Status: "active"},
			{ID: memberB, ActorID: profile.ID, TenantID: "report-tenant", OrgUnitID: "origin-org", RoleCode: "viewer", Grants: []access.Grant{{Role: "exporter", Scope: "tenant"}}, Status: "active"},
			{ID: memberBReviewer, ActorID: reviewerProfile.ID, TenantID: "report-tenant", OrgUnitID: "origin-org", RoleCode: "viewer", Grants: []access.Grant{{Role: "reviewer", Scope: "tenant"}}, Status: "active"},
		},
	}
	cfg, err := pgx.ParseConfig(env.harness.sel.TestDSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ImportIdentityCrosswalk(context.Background(), env.harness.sel.TestDSN, env.harness.schema, cfg.Database, mapping, true); err != nil {
		t.Fatal(err)
	}
	loginAs := func(person accounts.User, member string) (context.Context, accounts.User, string) {
		t.Helper()
		result, err := core.ResolveVerifiedOIDC(context.Background(), person, "client")
		if err != nil {
			t.Fatal(err)
		}
		if result.PendingToken != "" {
			result, err = core.ConsumePendingMembership(context.Background(), result.PendingToken, member, "client")
			if err != nil {
				t.Fatal(err)
			}
		}
		actor, audience, err := core.AuthenticateSession(context.Background(), result.SessionToken)
		if err != nil || audience != "client" || actor.MembershipID != member {
			t.Fatalf("login membership %s: audience=%q err=%v", member, audience, err)
		}
		return WithAdmissionSessionToken(context.Background(), result.SessionToken), actor, result.SessionToken
	}
	login := func(member string) (context.Context, accounts.User, string) { return loginAs(profile, member) }
	ctxA, actorA, _ := login(memberA)
	pack := publishSyntheticAcceptancePack(t, core, actorA)
	publishSyntheticAcceptancePolicy(t, core, actorA, pack)
	caseID, err := core.CreateCaseWithBusiness(actorA, "报告会话来源组织", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := document.Generate(ctxA, actorA, caseID, telecomTemplateID, validTelecomTemplateFields()); err != nil {
		t.Fatal(err)
	}
	if _, err := document.AddDocument(ctxA, actorA, caseID, "procurement", "procurement.txt", "text/plain", []byte("采购文件未提供任何合同阈值或审批依据。")); err != nil {
		t.Fatal(err)
	}
	accepted, err := execution.StartReviewAsyncAt(ctxA, actorA, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := execution.ClaimNextJob(ctxA, "report-origin-worker", time.Minute)
	if err != nil || claim.JobID != accepted.JobID {
		t.Fatalf("claim report run: claim=%+v err=%v", claim, err)
	}
	if err := execution.RunClaimedJob(ctxA, claim); err != nil {
		t.Fatal(err)
	}
	ctxB, exporter, tokenB := login(memberB)
	if exporter.Role != "viewer" || !authorizeCase(Case{TenantID: exporter.TenantID, OwnerOrgID: "case-org", ID: caseID}, exporter, access.Export) {
		t.Fatal("viewer membership with explicit exporter grant was not authorized for the tenant case")
	}
	const key = "report-cross-org-origin"
	original, err := core.CreateReportWithIdempotency(ctxB, exporter, caseID, accepted.RunID, accepted.AttemptID, key)
	if err != nil {
		t.Fatalf("create report from exporter membership: %v", err)
	}
	var oldStamp, oldResponse, oldRequestDigest string
	var oldVersion int64
	if err := env.harness.pool.QueryRow(ctxB, `SELECT command_session,command_version,request_digest,response_json::text FROM core_write_receipts WHERE case_id=$1 AND idempotency_key=$2`, caseID, key).Scan(&oldStamp, &oldVersion, &oldRequestDigest, &oldResponse); err != nil {
		t.Fatal(err)
	}
	var caseOrg, commandOrigin string
	if err := env.harness.pool.QueryRow(ctxB, `SELECT org_id,command_origin_org FROM core_write_receipts WHERE case_id=$1 AND idempotency_key=$2`, caseID, key).Scan(&caseOrg, &commandOrigin); err != nil || caseOrg != "case-org" || commandOrigin != "origin-org" {
		t.Fatalf("receipt confused case org with command source org: case_org=%q source_org=%q err=%v", caseOrg, commandOrigin, err)
	}
	if err := core.Logout(tokenB); err != nil {
		t.Fatal(err)
	}
	ctxB2, exporterAgain, _ := login(memberB)
	replayed, err := core.CreateReportWithIdempotency(ctxB2, exporterAgain, caseID, accepted.RunID, accepted.AttemptID, key)
	if err != nil || replayed.ID != original.ID || replayed.Version != original.Version || replayed.Digest != original.Digest {
		t.Fatalf("same-origin exporter membership did not recover original report: original=%+v replay=%+v err=%v", original, replayed, err)
	}
	restoreReportReceiptSchema43ForTest(t, ctxB2, env.harness.pool, env.harness.schema)
	if err := MigrateServiceRoles(context.Background()); err != nil {
		t.Fatalf("restore runtime ACL after schema 43 to 44: %v", err)
	}
	var afterStamp, afterResponse, afterRequestDigest string
	var afterVersion int64
	var legacyOrigin *string
	if err := env.harness.pool.QueryRow(ctxB2, `SELECT command_session,command_version,request_digest,response_json::text,command_origin_org FROM core_write_receipts WHERE case_id=$1 AND idempotency_key=$2`, caseID, key).Scan(&afterStamp, &afterVersion, &afterRequestDigest, &afterResponse, &legacyOrigin); err != nil {
		t.Fatal(err)
	}
	if afterStamp != oldStamp || afterVersion != oldVersion || afterRequestDigest != oldRequestDigest || afterResponse != oldResponse || legacyOrigin != nil {
		t.Fatalf("43 to 44 migration rewrote the old command or inferred origin: stamp=%q/%q version=%d/%d digest_equal=%v response_equal=%v origin=%v", oldStamp, afterStamp, oldVersion, afterVersion, oldRequestDigest == afterRequestDigest, oldResponse == afterResponse, legacyOrigin)
	}
	if _, _, err := core.ReadReport(exporterAgain, caseID, original.ID, "json"); err != nil {
		t.Fatalf("old report remains readable after 43 to 44 upgrade: %v", err)
	}
	if _, err := core.CreateReportWithIdempotency(ctxB2, exporterAgain, caseID, accepted.RunID, accepted.AttemptID, key); !isConflictFault(err) {
		t.Fatalf("legacy receipt without a trusted source must conflict after upgrade: %v", err)
	}
	missingTarget := ReportRecord{ID: "report_missing_target", Version: original.Version + 1, RunID: accepted.RunID, AttemptID: accepted.AttemptID, Digest: strings.Repeat("a", 64)}
	requestDigest, err := canonicalHash([]string{caseID, accepted.RunID, accepted.AttemptID})
	if err != nil {
		t.Fatal(err)
	}
	missingTargetResponse, err := json.Marshal(missingTarget)
	if err != nil {
		t.Fatal(err)
	}
	missingTx, err := core.beginContextTx(ctxB2, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctxB2, missingTx, userScope(exporterAgain)); err != nil {
		_ = missingTx.Rollback(ctxB2)
		t.Fatal(err)
	}
	caseItem := Case{ID: caseID, TenantID: exporterAgain.TenantID, OwnerOrgID: "case-org"}
	missingReceipt := writeReceipt{CaseID: caseID, OrgID: "case-org", RequestDigest: requestDigest, Response: missingTargetResponse}
	if err := validateWriteReceiptTarget(ctxB2, missingTx, coreWriteReceipts, caseItem, missingReceipt); faultStatus(err) != 404 {
		_ = missingTx.Rollback(ctxB2)
		t.Fatalf("trusted receipt with missing report target must fail closed: %v", err)
	}
	_ = missingTx.Rollback(ctxB2)
	ctxB3, exporterThird, _ := login(memberB)
	var concurrent [2]ReportRecord
	var concurrentErr [2]error
	var wg sync.WaitGroup
	wg.Add(2)
	for i, pair := range []struct {
		ctx   context.Context
		actor accounts.User
	}{{ctxB2, exporterAgain}, {ctxB3, exporterThird}} {
		go func(i int, ctx context.Context, actor accounts.User) {
			defer wg.Done()
			concurrent[i], concurrentErr[i] = core.CreateReportWithIdempotency(ctx, actor, caseID, accepted.RunID, accepted.AttemptID, "report-concurrent-first")
		}(i, pair.ctx, pair.actor)
	}
	wg.Wait()
	if concurrentErr[0] != nil || concurrentErr[1] != nil || concurrent[0].ID == "" || concurrent[0].ID != concurrent[1].ID || concurrent[0].Version != concurrent[1].Version {
		t.Fatalf("concurrent first insert did not converge on one report: results=%+v errors=%v", concurrent, concurrentErr)
	}
	var reportCount, concurrentReceiptCount int
	if err := env.harness.pool.QueryRow(context.Background(), `SELECT count(*) FROM business_reports WHERE case_id=$1`, caseID).Scan(&reportCount); err != nil {
		t.Fatal(err)
	}
	if err := env.harness.pool.QueryRow(context.Background(), `SELECT count(*) FROM core_write_receipts WHERE case_id=$1 AND idempotency_key='report-concurrent-first'`, caseID).Scan(&concurrentReceiptCount); err != nil {
		t.Fatal(err)
	}
	if reportCount != 2 || concurrentReceiptCount != 1 {
		t.Fatalf("concurrent first insert duplicated reports or receipts: reports=%d receipts=%d", reportCount, concurrentReceiptCount)
	}
	const trustedConcurrentKey = "report-concurrent-first"
	var concurrentOrigin string
	if err := env.harness.pool.QueryRow(context.Background(), `SELECT command_origin_org FROM core_write_receipts WHERE case_id=$1 AND idempotency_key=$2`, caseID, trustedConcurrentKey).Scan(&concurrentOrigin); err != nil || concurrentOrigin != "origin-org" {
		t.Fatalf("negative authorization checks require a trusted origin receipt: origin=%q err=%v", concurrentOrigin, err)
	}
	ctxA2, ownerA, _ := login(memberA)
	if _, err := core.CreateReportWithIdempotency(ctxA2, ownerA, caseID, accepted.RunID, accepted.AttemptID, trustedConcurrentKey); faultStatus(err) != 409 {
		t.Fatalf("same actor with case-org session must conflict against trusted origin-org receipt: status=%d err=%v", faultStatus(err), err)
	}
	ctxReviewer, reviewer, _ := loginAs(reviewerProfile, memberBReviewer)
	if _, err := core.CreateReportWithIdempotency(ctxReviewer, reviewer, caseID, accepted.RunID, accepted.AttemptID, trustedConcurrentKey); faultStatus(err) != 403 {
		t.Fatalf("reviewer grant must not substitute for Export: status=%d err=%v", faultStatus(err), err)
	}
	expiredGrant, err := json.Marshal([]access.Grant{{Role: "exporter", Scope: "tenant", ExpiresAt: time.Now().Add(-time.Minute).UTC()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.harness.pool.Exec(context.Background(), `UPDATE identity_memberships SET grants=$1::jsonb WHERE membership_id=$2::uuid`, string(expiredGrant), memberB); err != nil {
		t.Fatal(err)
	}
	ctxExpired, expiredExporter, _ := login(memberB)
	if _, err := core.CreateReportWithIdempotency(ctxExpired, expiredExporter, caseID, accepted.RunID, accepted.AttemptID, trustedConcurrentKey); faultStatus(err) != 404 {
		t.Fatalf("expired Export grant must be denied by current authorization: status=%d err=%v", faultStatus(err), err)
	}
	reviewerGrant, err := json.Marshal([]access.Grant{{Role: "reviewer", Scope: "tenant"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.harness.pool.Exec(context.Background(), `UPDATE identity_memberships SET grants=$1::jsonb WHERE membership_id=$2::uuid`, string(reviewerGrant), memberB); err != nil {
		t.Fatal(err)
	}
	ctxRevoked, revokedExporter, _ := login(memberB)
	if _, err := core.CreateReportWithIdempotency(ctxRevoked, revokedExporter, caseID, accepted.RunID, accepted.AttemptID, trustedConcurrentKey); faultStatus(err) != 403 {
		t.Fatalf("revoked Export with only reviewer grant must be denied: status=%d err=%v", faultStatus(err), err)
	}
}

// Reconstruct the applied schema-43 receipt state only in this isolated test schema.
func restoreReportReceiptSchema43ForTest(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema string) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var database string
	if err := tx.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	owner := pgx.Identifier{tenantIdentityOwnerName(database, schema)}.Sanitize()
	table := pgx.Identifier{schema, "core_write_receipts"}.Sanitize()
	qualified := pgx.Identifier{schema}.Sanitize()
	if _, err := tx.Exec(ctx, `ALTER POLICY command_receipt_context ON `+table+`
		USING (NULLIF(current_setting('app.command_digest',true),'') IS NULL
		  OR command_session=current_setting('app.command_digest',true) AND command_version=NULLIF(current_setting('app.command_version',true),'')::bigint)
		WITH CHECK (NULLIF(current_setting('app.command_digest',true),'') IS NULL
		  OR command_session=current_setting('app.command_digest',true) AND command_version=NULLIF(current_setting('app.command_version',true),'')::bigint);
		DROP TRIGGER report_receipt_origin ON `+table+`;
		DROP FUNCTION `+qualified+`.tenant_report_receipt_key_conflict(text,text,text,text,text,text);
		DROP FUNCTION `+qualified+`.tenant_report_receipt_recovery_allowed(text,text,text,text,text,text,text);
		DROP FUNCTION `+qualified+`.stamp_report_receipt_origin()`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `REVOKE SELECT (tenant_id,org_id,case_id,actor_id,route,idempotency_key,command_session,command_version,command_origin_org) ON `+table+` FROM `+owner); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE `+table+` DROP COLUMN command_origin_org; DELETE FROM schema_migrations WHERE version=44`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// Reapply only the receipt migration: later independent migrations remain
	// installed in this isolated schema and must not be replayed or removed.
	catalog, err := migrationCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var receiptMigration schemaMigration
	for _, migration := range catalog {
		if migration.version == 44 {
			receiptMigration = migration
			break
		}
	}
	if receiptMigration.version != 44 {
		t.Fatal("schema 44 receipt migration is missing")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if err := applyMigration(ctx, conn, schema, receiptMigration, false); err != nil {
		t.Fatalf("reapply schema 44 to existing schema 43 receipt rows: %v", err)
	}
}
