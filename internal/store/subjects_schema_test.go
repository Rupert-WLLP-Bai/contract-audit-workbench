package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

func TestSubjectTablesEnforceTenantScopeAppendOnlyAndSyntheticClaimUniqueness(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	core := env.openRole(t, "core")
	execution := env.openRole(t, "execution")
	caseID, err := core.CreateCase(admin, "主体台账数据库边界", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	const uscc = "91350100M000100Y43"
	firstSubject := insertTestSubject(t, core, admin, "subject-p2-a", uscc)

	// A duplicate verified synthetic identity in the same catalog is rejected
	// by PostgreSQL's partial unique index, not an in-memory pre-check.
	secondSubject := insertTestSubject(t, core, admin, "subject-p2-b", "91350100M000100Y44")
	deleteTestSubjectClaim(t, core, admin, secondSubject)
	if firstSubject == secondSubject {
		t.Fatal("test subjects unexpectedly share an ID")
	}
	duplicateTx, err := core.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(context.Background(), duplicateTx, userScope(admin)); err != nil {
		t.Fatal(err)
	}
	_, err = duplicateTx.Exec(context.Background(), `INSERT INTO subject_current_identifier_claims
		(tenant_id,subject_id,namespace,identifier_type,value,verification_state,verification_scope,source_catalog_id,source_snapshot_id,source_digest)
		VALUES ($1,$2,'cn.gov.samr.uscc','unified_social_credit_code',$3,'verified','synthetic_controlled','synthetic.uscc.demo','snapshot-p2','digest-p2')`, admin.TenantID, secondSubject, uscc)
	if !isPostgresError(err, "23505", "subject_synthetic_identifier_unique") {
		_ = duplicateTx.Rollback(context.Background())
		t.Fatalf("duplicate synthetic identity did not hit catalog-scoped unique index: %v", err)
	}
	_ = duplicateTx.Rollback(context.Background())

	// Foreign tenant context cannot insert even a row whose tenant/case IDs
	// otherwise refer to real owner rows.
	foreignTx, err := core.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	foreignScope := userScope(admin)
	foreignScope.TenantID = "different-tenant"
	if err := setLocalScope(context.Background(), foreignTx, foreignScope); err != nil {
		t.Fatal(err)
	}
	_, err = foreignTx.Exec(context.Background(), `INSERT INTO subject_source_snapshots
		(tenant_id,org_id,case_id,snapshot_id,source_catalog_id,source_version,source_payload,source_digest,source_scope,created_by_user_id,created_by_issuer,created_by_subject)
		VALUES ($1,$2,$3,'snapshot-foreign','synthetic.uscc.demo',1,'{}','digest','synthetic_controlled',$4,'issuer','subject')`, admin.TenantID, admin.OrgID, caseID, admin.ID)
	if !isPostgresError(err, "42501", "row-level security") {
		_ = foreignTx.Rollback(context.Background())
		t.Fatalf("foreign scope insert did not fail RLS: %v", err)
	}
	_ = foreignTx.Rollback(context.Background())

	// The snapshot row is append-only even for the owning core service role.
	snapshotTx, err := core.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(context.Background(), snapshotTx, userScope(admin)); err != nil {
		t.Fatal(err)
	}
	_, err = snapshotTx.Exec(context.Background(), `INSERT INTO subject_source_snapshots
		(tenant_id,org_id,case_id,snapshot_id,source_catalog_id,source_version,source_payload,source_digest,source_scope,created_by_user_id,created_by_issuer,created_by_subject)
		VALUES ($1,$2,$3,'snapshot-p2','synthetic.uscc.demo',1,'{}','digest','synthetic_controlled',$4,'issuer','subject')`, admin.TenantID, admin.OrgID, caseID, admin.ID)
	if err != nil {
		_ = snapshotTx.Rollback(context.Background())
		t.Fatal(err)
	}
	if err := snapshotTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	mutateTx, err := env.harness.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer mutateTx.Rollback(context.Background())
	if err := setLocalScope(context.Background(), mutateTx, userScope(admin)); err != nil {
		t.Fatal(err)
	}
	_, err = mutateTx.Exec(context.Background(), `UPDATE subject_source_snapshots SET source_payload=source_payload WHERE snapshot_id='snapshot-p2'`)
	if !isPostgresError(err, "P0001", "append-only") {
		t.Fatalf("snapshot rewrite did not hit append-only trigger: %v", err)
	}

	if _, err := execution.pool.Exec(context.Background(), `SELECT set_config('app.tenant_id',$1,false)`, admin.TenantID); err != nil {
		t.Fatal(err)
	}
	var canInsert, canUpdate, canDelete bool
	if err := execution.pool.QueryRow(context.Background(), `SELECT has_table_privilege(current_user,'subject_versions','INSERT'), has_table_privilege(current_user,'subject_versions','UPDATE'), has_table_privilege(current_user,'subject_versions','DELETE')`).Scan(&canInsert, &canUpdate, &canDelete); err != nil {
		t.Fatal(err)
	}
	if canInsert || canUpdate || canDelete {
		t.Fatalf("execution role can write core subject versions: insert=%v update=%v delete=%v", canInsert, canUpdate, canDelete)
	}
	executionTx, err := execution.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(context.Background(), executionTx, userScope(admin)); err != nil {
		t.Fatal(err)
	}
	_, err = executionTx.Exec(context.Background(), `UPDATE subject_versions SET payload_hash=payload_hash WHERE subject_id='subject-p2-a'`)
	if !isPostgresError(err, "42501", "permission denied") {
		_ = executionTx.Rollback(context.Background())
		t.Fatalf("execution role update did not fail at SQL privileges: %v", err)
	}
	_ = executionTx.Rollback(context.Background())
}

func TestSubjectRecordsPersistAndReloadThroughCoreState(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	core := env.openRole(t, "core")
	caseID, err := core.CreateCase(admin, "主体记录持久化", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	version := SubjectVersion{
		TenantID: admin.TenantID, SubjectID: "subject-roundtrip", Version: 1, DisplayName: "演示甲方",
		VerificationState: "verified", VerificationScope: "synthetic_controlled", SourceKind: "authorized_correction",
		IdentityClaims: []SubjectIdentityClaim{{Namespace: "cn.gov.samr.uscc", IdentifierType: "unified_social_credit_code", Value: "91350100M000100Y43", VerificationState: "verified", VerificationScope: "synthetic_controlled", SourceKind: "synthetic_controlled_snapshot", SourceCatalogID: "synthetic.uscc.demo", SourceSnapshotID: "snapshot-roundtrip", SourceDigest: "digest-roundtrip", SourceTime: &now, SourceValidFrom: &now}},
		Aliases:        []SubjectAlias{}, CreatedByUser: admin.ID, CreatedByIssuer: admin.Issuer, CreatedBySubject: admin.Subject, CreatedAt: now,
	}
	claim := version.IdentityClaims[0]
	version.IdentityClaims[0].PayloadHash = hashSubjectClaim(claim)
	role := CaseSubjectRole{TenantID: admin.TenantID, OrgID: admin.OrgID, CaseID: caseID, BindingID: "binding-roundtrip", CurrentVersion: 1, CreatedByUser: admin.ID, CreatedAt: now}
	roleVersion := CaseSubjectRoleVersion{TenantID: admin.TenantID, OrgID: admin.OrgID, CaseID: caseID, BindingID: role.BindingID, Version: 1, SubjectID: version.SubjectID, SubjectVersion: 1, Role: SubjectRoleCustomer, BindingState: "active", SourceKind: "user_asserted", CreatedByUser: admin.ID, CreatedByIssuer: admin.Issuer, CreatedBySubject: admin.Subject, CreatedAt: now}
	if err := core.updateAs(admin, func(st *state) error {
		st.Subjects = append(st.Subjects, Subject{TenantID: admin.TenantID, ID: version.SubjectID, CurrentVersion: 1, Status: "verified", CreatedByUser: admin.ID, CreatedByIssuer: admin.Issuer, CreatedBySubject: admin.Subject, CreatedAt: now})
		st.SubjectVersions = append(st.SubjectVersions, version)
		st.CaseSubjectRoles = append(st.CaseSubjectRoles, role)
		st.CaseSubjectRoleVersions = append(st.CaseSubjectRoleVersions, roleVersion)
		st.SubjectSourceSnapshots = append(st.SubjectSourceSnapshots, SubjectSourceSnapshot{TenantID: admin.TenantID, OrgID: admin.OrgID, CaseID: caseID, SnapshotID: "snapshot-roundtrip", SourceCatalogID: "synthetic.uscc.demo", SourceVersion: 1, SourcePayload: `{"records":[]}`, SourceDigest: "digest-roundtrip", SourceScope: "synthetic_controlled", CapturedAt: now, CreatedByUser: admin.ID, CreatedByIssuer: admin.Issuer, CreatedBySubject: admin.Subject})
		st.SubjectIdempotency = append(st.SubjectIdempotency, SubjectIdempotencyRecord{TenantID: admin.TenantID, ActorUserID: admin.ID, Operation: "create", TargetID: version.SubjectID, Key: "subject-create-1", PayloadHash: "payload-hash", Response: json.RawMessage(`{"subject_id":"subject-roundtrip"}`), HTTPStatus: 201, CreatedAt: now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := core.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Subjects) != 1 || reloaded.Subjects[0].ID != version.SubjectID || len(reloaded.SubjectVersions) != 1 || reloaded.SubjectVersions[0].IdentityClaims[0].SourceCatalogID != "synthetic.uscc.demo" || len(reloaded.CaseSubjectRoles) != 1 || len(reloaded.CaseSubjectRoleVersions) != 1 || reloaded.CaseSubjectRoleVersions[0].Role != SubjectRoleCustomer || len(reloaded.SubjectSourceSnapshots) != 1 || reloaded.SubjectSourceSnapshots[0].SourcePayload != `{"records":[]}` || len(reloaded.SubjectIdempotency) != 1 {
		t.Fatalf("P2 state did not round-trip through PostgreSQL: %+v", reloaded)
	}
}

func TestSubjectCreationBindingAndIdempotencyThroughCoreService(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	core := env.openRole(t, "core")
	input := SubjectCreateInput{DisplayName: "  测试供应商  ", LegalName: "测试供应商有限公司", Identifiers: []SubjectIdentityClaimInput{{Namespace: "cn.gov.samr.uscc", IdentifierType: "unified_social_credit_code", Value: " 91350100m000100y43 "}}, Aliases: []SubjectAliasInput{{Value: "供应商甲", Kind: "trade"}}}
	created, replayed, err := core.CreateUnboundSubject(context.Background(), admin, input, "subject-create-key-1")
	if err != nil || replayed || created.SubjectID == "" || created.CurrentVersion != 1 || created.SubjectVersion.VerificationState != "pending_verification" || created.SubjectVersion.IdentityClaims[0].MaskedValue != "9135••••••••••0Y43" {
		t.Fatalf("unbound subject creation failed or exposed raw identity: result=%+v replayed=%v err=%v", created, replayed, err)
	}
	retry, replayed, err := core.CreateUnboundSubject(context.Background(), admin, input, "subject-create-key-1")
	if err != nil || !replayed || retry.SubjectID != created.SubjectID || retry.SubjectVersion.PayloadHash != created.SubjectVersion.PayloadHash {
		t.Fatalf("same-key create did not replay the original result: result=%+v replayed=%v err=%v", retry, replayed, err)
	}
	changed := input
	changed.DisplayName = "不同内容"
	_, _, err = core.CreateUnboundSubject(context.Background(), admin, changed, "subject-create-key-1")
	var apiErr *fault.Error
	if !errors.As(err, &apiErr) || apiErr.Status != 409 || apiErr.Code != "conflict" {
		t.Fatalf("changed payload did not return a typed 409: %v", err)
	}
	caseID, err := core.CreateCase(admin, "主体绑定场景", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	first, replayed, err := core.CreateCaseSubject(context.Background(), admin, caseID, CaseSubjectCreateInput{Role: SubjectRoleSupplier, Subject: input}, "case-subject-key-1")
	if err != nil || replayed || first.BindingID == "" || first.BindingState != "pending_confirmation" || first.SourceKind != "user_asserted" || first.LegacyLabel != nil {
		t.Fatalf("case-bound pending subject failed: %+v replayed=%v err=%v", first, replayed, err)
	}
	second, _, err := core.CreateCaseSubject(context.Background(), admin, caseID, CaseSubjectCreateInput{Role: SubjectRoleSupplier, Subject: SubjectCreateInput{DisplayName: "另一家供应商"}}, "case-subject-key-2")
	if err != nil || second.BindingID == first.BindingID {
		t.Fatalf("same-role additive binding was rejected: %+v err=%v", second, err)
	}
	duplicateName, replayed, err := core.CreateCaseSubject(context.Background(), admin, caseID, CaseSubjectCreateInput{Role: SubjectRoleSupplier, Subject: SubjectCreateInput{DisplayName: "测试供应商", LegalName: "测试供应商有限公司"}}, "case-subject-key-3")
	if err != nil || replayed || duplicateName.SubjectID == first.SubjectID || duplicateName.BindingID == first.BindingID {
		t.Fatalf("same-name subject was silently merged instead of creating a separate identity: first=%+v duplicate=%+v replayed=%v err=%v", first, duplicateName, replayed, err)
	}
	caseSubjects, err := core.ListCaseSubjects(context.Background(), admin, caseID)
	if err != nil || len(caseSubjects.Items) != 3 || caseSubjects.Items[0].Subject.Version != 1 || caseSubjects.LegacyProfile != nil || !caseSubjects.CanManage {
		t.Fatalf("case subject projection did not preserve both same-role bindings: %+v err=%v", caseSubjects, err)
	}
	viewer, ok := accounts.ByID("demo-viewer")
	if !ok {
		t.Fatal("demo viewer missing")
	}
	if err := core.AddMember(admin, caseID, viewer.ID); err != nil {
		t.Fatal(err)
	}
	viewerSubjects, err := core.ListCaseSubjects(context.Background(), viewer, caseID)
	if err != nil || viewerSubjects.CanManage || len(viewerSubjects.Items) != len(caseSubjects.Items) {
		t.Fatalf("read-authorized viewer received a write affordance or lost read access: %+v err=%v", viewerSubjects, err)
	}
	core.UseReadOnly(true)
	readOnlySubjects, err := core.ListCaseSubjects(context.Background(), admin, caseID)
	core.UseReadOnly(false)
	if err != nil || readOnlySubjects.CanManage {
		t.Fatalf("maintenance read-only state still advertised case subject writes: %+v err=%v", readOnlySubjects, err)
	}
	listed, err := core.ListSubjects(context.Background(), admin, caseID, "供应商", "", 1)
	if err != nil || len(listed.Items) != 1 || listed.NextCursor == nil {
		t.Fatalf("case-scoped subject search/paging mismatch: %+v err=%v", listed, err)
	}
	page2, err := core.ListSubjects(context.Background(), admin, caseID, "供应商", *listed.NextCursor, 1)
	if err != nil || len(page2.Items) != 1 || page2.NextCursor == nil || page2.Items[0].SubjectID == listed.Items[0].SubjectID {
		t.Fatalf("subject cursor did not continue after ACL-filtered page: %+v err=%v", page2, err)
	}
	page3, err := core.ListSubjects(context.Background(), admin, caseID, "供应商", *page2.NextCursor, 1)
	if err != nil || len(page3.Items) != 1 || page3.NextCursor == nil || page3.Items[0].SubjectID == page2.Items[0].SubjectID {
		t.Fatalf("third cursor page did not continue through visible candidates: %+v err=%v", page3, err)
	}
	page4, err := core.ListSubjects(context.Background(), admin, caseID, "供应商", *page3.NextCursor, 1)
	if err != nil || len(page4.Items) != 1 || page4.NextCursor != nil || page4.Items[0].SubjectID == page3.Items[0].SubjectID {
		t.Fatalf("last cursor page did not end at visible candidates: %+v err=%v", page4, err)
	}
	links, err := core.ListSubjectCaseLinks(context.Background(), admin, first.SubjectID, "", 50)
	if err != nil || links.VisibleCount != 1 || len(links.Items) != 1 || links.Items[0].CaseID != caseID {
		t.Fatalf("authorized case links projection mismatch: %+v err=%v", links, err)
	}
}

func TestSubjectCandidatesUseManageContextWithoutWideningVisibility(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	core := env.openRole(t, "core")
	caseA, err := core.CreateCase(admin, "主体候选目标案件", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	caseB, err := core.CreateCase(admin, "主体候选有权案件", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	caseC, err := core.CreateCase(admin, "主体候选无权案件", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	fromB, _, err := core.CreateCaseSubject(context.Background(), admin, caseB, CaseSubjectCreateInput{Role: SubjectRoleSupplier, Subject: SubjectCreateInput{DisplayName: "案件B主体"}}, "candidate-case-b")
	if err != nil {
		t.Fatal(err)
	}
	fromC, _, err := core.CreateCaseSubject(context.Background(), admin, caseC, CaseSubjectCreateInput{Role: SubjectRoleSupplier, Subject: SubjectCreateInput{DisplayName: "案件C隐藏主体"}}, "candidate-case-c")
	if err != nil {
		t.Fatal(err)
	}
	actor := accounts.User{ID: "candidate-actor", DisplayName: "候选经办", Role: "clerk", TenantID: admin.TenantID, OrgID: admin.OrgID, Enabled: true,
		Issuer: "https://directory.invalid", Subject: "candidate-actor", Grants: []access.Grant{
			{Role: "case_owner", Scope: "case", CaseID: caseA},
			{Role: "reviewer", Scope: "case", CaseID: caseB},
		}}
	admin.Issuer = "https://directory.invalid"
	admin.Subject = "candidate-admin"
	body, err := json.Marshal(map[string]any{"users": []accounts.User{admin, actor}})
	if err != nil {
		t.Fatal(err)
	}
	directoryPath := filepath.Join(t.TempDir(), "directory.json")
	if err := os.WriteFile(directoryPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(directoryPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := accounts.ConfigureDirectory(""); err != nil {
			t.Error(err)
		}
	})
	unbound, _, err := core.CreateUnboundSubject(context.Background(), actor, SubjectCreateInput{DisplayName: "创建人待绑定主体"}, "candidate-unbound")
	if err != nil {
		t.Fatal(err)
	}
	page, err := core.ListSubjects(context.Background(), actor, caseA, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range page.Items {
		seen[item.SubjectID] = true
	}
	if !seen[fromB.SubjectID] || !seen[unbound.SubjectID] || seen[fromC.SubjectID] {
		t.Fatalf("case context widened/narrowed candidate visibility: seen=%v B=%s unbound=%s hiddenC=%s", seen, fromB.SubjectID, unbound.SubjectID, fromC.SubjectID)
	}
	_, err = core.ListSubjects(context.Background(), actor, caseC, "", "", 100)
	var apiErr *fault.Error
	if !errors.As(err, &apiErr) || apiErr.Status != 404 {
		t.Fatalf("invisible case must not be used as candidate context or disclosed: %v", err)
	}
}

func insertTestSubject(t *testing.T, core *Store, user accounts.User, subjectID, uscc string) string {
	t.Helper()
	ctx := context.Background()
	tx, err := core.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(user)); err != nil {
		t.Fatal(err)
	}
	version := SubjectVersion{
		TenantID: user.TenantID, SubjectID: subjectID, Version: 1, DisplayName: subjectID,
		VerificationState: "verified", VerificationScope: "synthetic_controlled", SourceKind: "authorized_correction",
		IdentityClaims: []SubjectIdentityClaim{{Namespace: "cn.gov.samr.uscc", IdentifierType: "unified_social_credit_code", Value: uscc, VerificationState: "verified", VerificationScope: "synthetic_controlled", SourceKind: "synthetic_controlled_snapshot", SourceCatalogID: "synthetic.uscc.demo"}},
		Aliases:        []SubjectAlias{},
	}
	payload, err := json.Marshal(version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO subjects (tenant_id,subject_id,current_version,status,created_by_user_id,created_by_issuer,created_by_subject)
		VALUES ($1,$2,1,'verified',$3,'issuer','principal')`, user.TenantID, subjectID, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO subject_versions (tenant_id,subject_id,version,payload,payload_hash)
		VALUES ($1,$2,1,$3,'hash')`, user.TenantID, subjectID, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO subject_current_identifier_claims
		(tenant_id,subject_id,namespace,identifier_type,value,verification_state,verification_scope,source_catalog_id,source_snapshot_id,source_digest)
		VALUES ($1,$2,'cn.gov.samr.uscc','unified_social_credit_code',$3,'verified','synthetic_controlled','synthetic.uscc.demo','snapshot-p2','digest-p2')`, user.TenantID, subjectID, uscc); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return subjectID
}

func deleteTestSubjectClaim(t *testing.T, core *Store, user accounts.User, subjectID string) {
	t.Helper()
	ctx := context.Background()
	tx, err := core.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(user)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM subject_current_identifier_claims WHERE tenant_id=$1 AND subject_id=$2`, user.TenantID, subjectID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
