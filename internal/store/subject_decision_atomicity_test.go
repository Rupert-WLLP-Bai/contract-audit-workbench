package store

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	subjectApp "contractaudit/internal/subject/app"
	subjectModel "contractaudit/internal/subject/model"
)

func subjectDecisionDurableRows(t *testing.T, core *Store, actor accounts.User) map[string]string {
	t.Helper()
	ctx := context.Background()
	tx, err := core.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(actor)); err != nil {
		t.Fatal(err)
	}
	tables := []string{"subjects", "subject_versions", "subject_current_identifier_claims", "case_subject_roles", "case_subject_role_versions", "subject_change_requests", "subject_change_decisions", "subject_write_idempotency", "subject_source_snapshots"}
	result := make(map[string]string, len(tables))
	for _, table := range tables {
		query := `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM ` + table + ` t WHERE tenant_id=$1`
		var rows string
		if err := tx.QueryRow(ctx, query, accounts.Tenant(actor)).Scan(&rows); err != nil {
			t.Fatalf("read %s: %v", table, err)
		}
		result[table] = rows
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSubjectDecisionDeferredRollbackCoreRole(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	core := env.openRole(t, "core")
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("demo reviewer missing")
	}
	ctx := context.Background()
	caseA, err := core.CreateCase(admin, "主体原子性来源案", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	caseB, err := core.CreateCase(admin, "主体原子性关联案", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{caseA, caseB} {
		if err := core.AddMember(admin, id, reviewer.ID); err != nil {
			t.Fatal(err)
		}
	}
	const manifest = `{"source_id":"synthetic.uscc.demo","source_version":1,"display_name":"原子性受控目录","verification_scope":"synthetic_controlled","records":[{"record_id":"c308-record","display_name":"原子性主体","uscc":"91350100M000100Y43","source_as_of":"2026-10-30T00:00:00Z","valid_from":"2020-01-01T00:00:00Z","valid_until":null}]}`
	if err := core.ConfigureSubjectSourceManifest([]byte(manifest)); err != nil {
		t.Fatal(err)
	}
	input := CaseSubjectCreateInput{Role: SubjectRoleSupplier, Subject: SubjectCreateInput{DisplayName: "原子性主体", Identifiers: []SubjectIdentityClaimInput{{Namespace: "cn.gov.samr.uscc", IdentifierType: "unified_social_credit_code", Value: "91350100M000100Y43"}}}}
	first, replayed, err := core.CreateCaseSubject(ctx, admin, caseA, input, "c308-create")
	if err != nil || replayed || first.BindingState != "pending_confirmation" {
		t.Fatalf("create: %+v replay=%v err=%v", first, replayed, err)
	}
	second, replayed, err := core.BindExistingSubject(ctx, admin, caseB, BindExistingSubjectInput{Role: SubjectRoleSupplier, SubjectID: first.SubjectID, ExpectedSubjectVersion: 1}, "c308-bind")
	if err != nil || replayed || second.BindingState != "active" {
		t.Fatalf("bind: %+v replay=%v err=%v", second, replayed, err)
	}
	other, replayed, err := core.CreateCaseSubject(ctx, admin, caseA, CaseSubjectCreateInput{Role: SubjectRoleCustomer, Subject: SubjectCreateInput{DisplayName: "不相关主体", Identifiers: []SubjectIdentityClaimInput{{Namespace: "cn.gov.samr.uscc", IdentifierType: "unified_social_credit_code", Value: "91350100M000100Y44"}}}}, "c308-other")
	if err != nil || replayed {
		t.Fatalf("other: %+v replay=%v err=%v", other, replayed, err)
	}
	sourceInput := SubjectSourceSnapshotCreateInput{SourceID: syntheticSubjectSourceID, ExpectedSourceVersion: 1}
	source, replayed, err := core.CreateSubjectSourceSnapshot(ctx, admin, caseA, sourceInput, "c308-source")
	if err != nil || replayed || source.SnapshotID == "" || source.RecordCount != 1 {
		t.Fatalf("source: %+v replay=%v err=%v", source, replayed, err)
	}
	verifyInput := SubjectSourceVerifyInput{SubjectID: first.SubjectID, ExpectedSubjectVersion: 1}
	proposal, replayed, err := core.VerifySubjectFromControlledSource(ctx, admin, caseA, source.SnapshotID, verifyInput, "c308-verify")
	if err != nil || replayed || proposal.State != "pending_decision" || proposal.MatchOutcome != "exact_match" || proposal.RequestID == nil || proposal.SourceDigest != source.SourceDigest {
		t.Fatalf("verify: %+v replay=%v err=%v", proposal, replayed, err)
	}
	requestID := *proposal.RequestID
	impact, err := core.GetSubjectChangeImpact(ctx, reviewer, caseA, requestID)
	if err != nil || !impact.CanDecide || impact.VisibleAffectedCount != 2 {
		t.Fatalf("impact: %+v err=%v", impact, err)
	}
	decisionInput := SubjectChangeDecisionInput{Decision: "approve", ExpectedSubjectVersion: 1, ExpectedPayloadHash: impact.PayloadHash, ExpectedImpactETag: impact.ImpactETag, Reason: "复核两个案件的受控来源"}
	digest, err := subjectApp.DecisionRequestHash(caseA, requestID, decisionInput)
	if err != nil {
		t.Fatal(err)
	}
	stateBefore, err := core.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	var req SubjectChangeRequest
	for _, item := range stateBefore.SubjectChangeRequests {
		if item.RequestID == requestID {
			req = item
			break
		}
	}
	var sourceRow SubjectSourceSnapshot
	for _, item := range stateBefore.SubjectSourceSnapshots {
		if item.SnapshotID == source.SnapshotID {
			sourceRow = item
			break
		}
	}
	if sourceRow.SnapshotID != source.SnapshotID || sourceRow.SourceDigest != source.SourceDigest {
		t.Fatal("source fixture not persisted")
	}
	requestJSON, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var candidate subjectModel.SubjectAfterPayload
	if err := json.Unmarshal(req.CanonicalPayload, &candidate); err != nil {
		t.Fatal(err)
	}
	v := candidate.SubjectVersion
	if req.RequestID != requestID || req.CanonicalPayloadHash != impact.PayloadHash || v.SubjectID != first.SubjectID || v.Version != 2 || v.PayloadHash != subjectModel.HashSubjectVersion(v) || v.CreatedByUser != admin.ID || v.CreatedAt.IsZero() || len(v.IdentityClaims) != 1 {
		t.Fatalf("invalid prepared request or candidate: %+v %+v", req, v)
	}
	claim := v.IdentityClaims[0]
	if claim.SourceTime == nil || claim.SourceValidFrom == nil || claim.SourceValidUntil != nil || claim.SourceSnapshotID != source.SnapshotID || claim.SourceDigest != source.SourceDigest {
		t.Fatalf("invalid source claim: %+v", claim)
	}
	before := subjectDecisionDurableRows(t, core, admin)
	for table, value := range before {
		if table != "subject_change_decisions" && value == "[]" {
			t.Fatalf("missing baseline %s", table)
		}
	}
	if !strings.Contains(before["subject_current_identifier_claims"], other.SubjectID) || !strings.Contains(before["subject_current_identifier_claims"], `pending_verification`) || strings.Contains(before["subject_write_idempotency"], "c308-decision") || strings.Contains(before["subject_change_decisions"], requestID) {
		t.Fatalf("invalid baseline: %v", before)
	}
	// Literal values come exclusively from this test fixture and server-generated identities.
	lit := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	// pgx encodes timestamptz at microsecond precision; payload JSON keeps the original time.
	vars := map[string]string{"TENANT": accounts.Tenant(admin), "ORG": req.OrgID, "SUBJECT": first.SubjectID, "OTHER": other.SubjectID, "A": caseA, "B": caseB, "BIND_A": first.BindingID, "BIND_B": second.BindingID, "REQUEST": requestID, "SNAPSHOT": source.SnapshotID, "DIGEST": source.SourceDigest, "PAYLOAD": req.CanonicalPayloadHash, "AFFECTED": req.ExpectedBindingsHash, "REQUEST_HASH": digest, "ACTOR": reviewer.ID, "ISSUER": reviewer.Issuer, "ACTOR_SUBJECT": reviewer.Subject, "CREATOR": v.CreatedByUser, "CREATOR_ISSUER": v.CreatedByIssuer, "CREATOR_SUBJECT": v.CreatedBySubject, "VERSION_HASH": v.PayloadHash, "REQUEST_ROW": string(requestJSON), "SOURCE_PAYLOAD": sourceRow.SourcePayload, "CREATED_DB": v.CreatedAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)}
	ddl := `CREATE FUNCTION c308_decision_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.tenant_id <> @TENANT@ OR NEW.actor_user_id <> @ACTOR@ OR NEW.operation <> 'decide_subject_change' OR NEW.target_id <> @REQUEST@ OR NEW.idempotency_key <> 'c308-decision' THEN RETURN NEW; END IF;
 IF NEW.payload_hash <> @REQUEST_HASH@ OR NEW.http_status <> 200 OR NEW.response <> jsonb_build_object('request_id',@REQUEST@,'decision','approved','applied_subject_version',2,'binding_versions_appended',2)
 OR NOT EXISTS (SELECT 1 FROM subjects h JOIN subject_versions v ON v.tenant_id=h.tenant_id AND v.subject_id=h.subject_id AND v.version=2 JOIN subject_versions old ON old.tenant_id=h.tenant_id AND old.subject_id=h.subject_id AND old.version=1 JOIN subject_change_requests r ON r.tenant_id=h.tenant_id AND r.request_id=@REQUEST@ WHERE h.tenant_id=@TENANT@ AND h.subject_id=@SUBJECT@ AND h.current_version=2 AND h.status='verified' AND v.payload_hash=@VERSION_HASH@ AND v.payload=r.payload->'canonical_payload'->'subject_version' AND v.payload->>'created_by_user_id'=@CREATOR@ AND v.payload->>'created_by_issuer'=@CREATOR_ISSUER@ AND v.payload->>'created_by_subject'=@CREATOR_SUBJECT@ AND v.created_at=@CREATED_DB@::timestamptz AND old.version=1 AND r.org_id=@ORG@ AND r.source_case_id=@A@ AND r.target_subject_id=@SUBJECT@ AND r.target_kind='subject' AND r.expected_subject_version=1 AND r.payload_hash=@PAYLOAD@ AND r.affected_bindings_hash=@AFFECTED@ AND r.payload=@REQUEST_ROW@::jsonb)
 OR NOT EXISTS (SELECT 1 FROM subject_current_identifier_claims c WHERE c.tenant_id=@TENANT@ AND c.subject_id=@SUBJECT@ AND c.namespace='cn.gov.samr.uscc' AND c.identifier_type='unified_social_credit_code' AND c.value='91350100M000100Y43' AND c.verification_state='verified' AND c.verification_scope='synthetic_controlled' AND c.source_catalog_id='synthetic.uscc.demo' AND c.source_snapshot_id=@SNAPSHOT@ AND c.source_digest=@DIGEST@ AND c.source_time='2026-10-30T00:00:00Z'::timestamptz AND c.source_valid_from='2020-01-01T00:00:00Z'::timestamptz AND c.source_valid_until IS NULL)
 OR (SELECT count(*) FROM subject_current_identifier_claims c WHERE c.tenant_id=@TENANT@ AND c.subject_id=@SUBJECT@) <> 1
 OR NOT EXISTS (SELECT 1 FROM subject_current_identifier_claims c WHERE c.tenant_id=@TENANT@ AND c.subject_id=@OTHER@ AND c.value='91350100M000100Y44' AND c.verification_state='pending_verification')
 OR NOT EXISTS (SELECT 1 FROM case_subject_roles h JOIN case_subject_role_versions v ON v.tenant_id=h.tenant_id AND v.org_id=h.org_id AND v.case_id=h.case_id AND v.binding_id=h.binding_id AND v.version=2 JOIN case_subject_role_versions old ON old.tenant_id=h.tenant_id AND old.org_id=h.org_id AND old.case_id=h.case_id AND old.binding_id=h.binding_id AND old.version=1 WHERE h.tenant_id=@TENANT@ AND h.org_id=@ORG@ AND h.case_id=@A@ AND h.binding_id=@BIND_A@ AND h.current_version=2 AND v.subject_id=@SUBJECT@ AND v.subject_version=2 AND v.role='supplier' AND v.binding_state='pending_confirmation' AND old.subject_id=@SUBJECT@ AND old.subject_version=1 AND old.role=v.role AND old.binding_state=v.binding_state AND v.payload->>'created_by_user_id'=@ACTOR@ AND v.payload->>'created_by_issuer'=@ISSUER@ AND v.payload->>'created_by_subject'=@ACTOR_SUBJECT@)
 OR NOT EXISTS (SELECT 1 FROM case_subject_roles h JOIN case_subject_role_versions v ON v.tenant_id=h.tenant_id AND v.org_id=h.org_id AND v.case_id=h.case_id AND v.binding_id=h.binding_id AND v.version=2 JOIN case_subject_role_versions old ON old.tenant_id=h.tenant_id AND old.org_id=h.org_id AND old.case_id=h.case_id AND old.binding_id=h.binding_id AND old.version=1 WHERE h.tenant_id=@TENANT@ AND h.org_id=@ORG@ AND h.case_id=@B@ AND h.binding_id=@BIND_B@ AND h.current_version=2 AND v.subject_id=@SUBJECT@ AND v.subject_version=2 AND v.role='supplier' AND v.binding_state='active' AND old.subject_id=@SUBJECT@ AND old.subject_version=1 AND old.role=v.role AND old.binding_state=v.binding_state AND v.payload->>'created_by_user_id'=@ACTOR@ AND v.payload->>'created_by_issuer'=@ISSUER@ AND v.payload->>'created_by_subject'=@ACTOR_SUBJECT@)
 OR NOT EXISTS (SELECT 1 FROM subject_change_decisions d WHERE d.tenant_id=@TENANT@ AND d.request_id=@REQUEST@ AND d.decision='approve' AND d.actor_user_id=@ACTOR@ AND d.actor_issuer=@ISSUER@ AND d.actor_subject=@ACTOR_SUBJECT@ AND d.payload->>'canonical_payload_hash'=@PAYLOAD@ AND d.payload->>'affected_bindings_hash'=@AFFECTED@ AND d.payload->>'applied_subject_version'='2' AND d.payload->>'binding_versions_appended'='2')
 OR NOT EXISTS (SELECT 1 FROM subject_source_snapshots s JOIN subject_change_requests r ON r.tenant_id=s.tenant_id AND r.org_id=s.org_id AND r.source_case_id=s.case_id WHERE s.tenant_id=@TENANT@ AND s.org_id=@ORG@ AND s.case_id=@A@ AND s.snapshot_id=@SNAPSHOT@ AND s.source_catalog_id='synthetic.uscc.demo' AND s.source_version=1 AND s.source_payload=@SOURCE_PAYLOAD@ AND s.source_digest=@DIGEST@ AND s.source_scope='synthetic_controlled' AND r.request_id=@REQUEST@ AND r.payload->'basis_refs'->0->>'snapshot_id'=@SNAPSHOT@ AND r.payload->'basis_refs'->0->>'source_digest'=@DIGEST@)
 THEN RAISE EXCEPTION 'C308_DECISION_PRECONDITION'; END IF;
 RAISE EXCEPTION 'C308_DECISION_FORCED'; END $$;
 CREATE CONSTRAINT TRIGGER c308_decision_deferred AFTER INSERT ON subject_write_idempotency DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION c308_decision_fail()`
	for name, value := range vars {
		ddl = strings.ReplaceAll(ddl, "@"+name+"@", lit(value))
	}
	// Placeholders have explicit delimiters so similarly named fields cannot collide.
	if strings.Contains(ddl, "@") {
		t.Fatal("unsubstituted trigger fixture")
	}
	if _, err := env.harness.pool.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.harness.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS c308_decision_deferred ON subject_write_idempotency; DROP FUNCTION IF EXISTS c308_decision_fail()`)
	})
	if _, _, err := core.DecideSubjectChange(ctx, reviewer, caseA, requestID, decisionInput, "c308-decision"); err == nil || !strings.Contains(err.Error(), "C308_DECISION_FORCED") {
		t.Fatalf("expected C308_DECISION_FORCED: %v", err)
	}
	after := subjectDecisionDurableRows(t, core, admin)
	if !reflect.DeepEqual(before, after) || strings.Contains(after["subject_write_idempotency"], "c308-decision") || strings.Contains(after["subject_change_decisions"], requestID) {
		t.Fatalf("partial commit: before=%v after=%v", before, after)
	}
	if _, err := env.harness.pool.Exec(ctx, `DROP TRIGGER c308_decision_deferred ON subject_write_idempotency; DROP FUNCTION c308_decision_fail()`); err != nil {
		t.Fatal(err)
	}
	result, replayed, err := core.DecideSubjectChange(ctx, reviewer, caseA, requestID, decisionInput, "c308-decision")
	if err != nil || replayed || result.Decision != "approved" || result.AppliedSubjectVersion == nil || *result.AppliedSubjectVersion != 2 || result.BindingVersionsAppended != 2 {
		t.Fatalf("retry: %+v replay=%v err=%v", result, replayed, err)
	}
	again, replayed, err := core.DecideSubjectChange(ctx, reviewer, caseA, requestID, decisionInput, "c308-decision")
	if err != nil || !replayed || !reflect.DeepEqual(result, again) {
		t.Fatalf("decision replay: %+v replay=%v err=%v", again, replayed, err)
	}
	sourceAgain, replayed, err := core.CreateSubjectSourceSnapshot(ctx, admin, caseA, sourceInput, "c308-source")
	if err != nil || !replayed || !reflect.DeepEqual(source, sourceAgain) {
		t.Fatalf("source replay: %+v replay=%v err=%v", sourceAgain, replayed, err)
	}
	proposalAgain, replayed, err := core.VerifySubjectFromControlledSource(ctx, admin, caseA, source.SnapshotID, verifyInput, "c308-verify")
	if err != nil || !replayed || !reflect.DeepEqual(proposal, proposalAgain) {
		t.Fatalf("verify replay: %+v replay=%v err=%v", proposalAgain, replayed, err)
	}
}
