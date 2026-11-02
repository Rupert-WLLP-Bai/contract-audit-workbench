package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
)

// Runs inside the ordinary-Core/Execution scenario so the session, membership
// and admission are genuine rather than hand-asserted trusted User claims.
func assertOperationAuthorityScopes(t *testing.T, ctx context.Context, admin *Store, user accounts.User, sessionToken, jobID, runID, attemptID string) {
	t.Helper()
	dsn, role := tenantIdentityTestCore(t, ctx, admin)
	pool, err := newStorePool(ctx, dsn, admin.schema, true)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var sessionUser, currentUser string
	var super, bypass bool
	if err = pool.QueryRow(ctx, `SELECT session_user,current_user,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&sessionUser, &currentUser, &super, &bypass); err != nil || sessionUser != role || currentUser != role || super || bypass {
		t.Fatalf("operation Core role proof failed: %v", err)
	}
	for _, table := range []string{"worker_jobs", "cases", "review_runs", "review_attempts", "documents", "review_attempt_parse_selections", "experience_distill_inputs", "experience_feedback"} {
		if _, err = admin.pool.Exec(ctx, `GRANT SELECT ON `+pgx.Identifier{admin.schema, table}.Sanitize()+` TO `+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = admin.pool.Exec(ctx, `GRANT SELECT,INSERT ON `+pgx.Identifier{admin.schema, "operation_authority_tickets"}.Sanitize()+` TO `+pgx.Identifier{role}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.pool.Exec(ctx, `GRANT EXECUTE ON FUNCTION `+pgx.Identifier{admin.schema}.Sanitize()+`.tenant_identity_claim_user(text,text,text,text,bigint,text,text,text,text) TO `+pgx.Identifier{role}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	core := &Store{pool: pool, schema: admin.schema, serviceOwner: "core"}
	if _, err = admin.pool.Exec(ctx, `UPDATE worker_jobs SET state='running',worker_id='operation-worker',generation=1,lease_until=clock_timestamp()+interval '2 minutes' WHERE id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.pool.Exec(ctx, `UPDATE review_runs SET state='running' WHERE id=$1`, runID); err != nil {
		t.Fatal(err)
	}
	scope := model.DelegatedScope{Kind: "execution", TenantID: user.TenantID, OrgID: user.OrgID, CaseID: "case-a", ActorID: user.ID, JobID: jobID, WorkerID: "operation-worker", Generation: 1, RunID: runID, AttemptID: attemptID}
	resolved, err := core.ResolveOperationAuthority(ctx, scope, "worker")
	if err != nil || !resolved.HasResolvedMembership() || resolved.MembershipID != user.MembershipID {
		t.Fatalf("exact admitted Model authority: %v", err)
	}
	for _, bad := range []model.DelegatedScope{func() model.DelegatedScope { v := scope; v.Generation++; return v }(), func() model.DelegatedScope { v := scope; v.ActorID = "another-actor"; return v }(), func() model.DelegatedScope { v := scope; v.CaseID = "another-case"; return v }()} {
		if _, err = core.ResolveOperationAuthority(ctx, bad, "model"); err == nil {
			t.Fatal("forged operation scope accepted")
		}
	}
	assertCrossOrganizationMembershipScope(t, ctx, admin, core, user, runID)
	// Current membership grants are refreshed from PG, not the stale directory.
	oldGrants, err := json.Marshal(user.Grants)
	if err != nil {
		t.Fatal(err)
	}
	grants, _ := json.Marshal([]access.Grant{{Role: "case_owner", Scope: "case", CaseID: "case-a"}})
	if _, err = admin.pool.Exec(ctx, `UPDATE identity_memberships SET grants=$1::jsonb,role_code='clerk' WHERE membership_id=$2::uuid`, grants, user.MembershipID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = admin.pool.Exec(ctx, `UPDATE identity_memberships SET grants=$1::jsonb,status='active',role_code=$3 WHERE membership_id=$2::uuid`, oldGrants, user.MembershipID, user.Role)
	}()
	scope = model.DelegatedScope{Kind: "document", Action: "upload_parse", TenantID: user.TenantID, OrgID: user.OrgID, CaseID: "case-a", ActorID: user.ID, DocumentID: "future-upload-document", InputSHA256: strings.Repeat("a", 64)}
	ticket, err := core.CreateOperationTicket(WithAdmissionSessionToken(ctx, sessionToken), user, scope)
	if err != nil || ticket == "" {
		t.Fatalf("exact session upload operation ticket: %v", err)
	}
	scope.OperationTicket = ticket
	if _, err = core.ResolveOperationAuthority(ctx, scope, "model"); err != nil {
		t.Fatalf("valid bounded nonjob operation: %v", err)
	}
	for _, change := range []func(*model.DelegatedScope){func(s *model.DelegatedScope) { s.Action = "reparse" }, func(s *model.DelegatedScope) { s.DocumentID = "other-document" }, func(s *model.DelegatedScope) { s.InputSHA256 = strings.Repeat("b", 64) }} {
		bad := scope
		change(&bad)
		if _, err = core.ResolveOperationAuthority(ctx, bad, "model"); err == nil {
			t.Fatal("cross-action/document/body ticket reuse accepted")
		}
	}
	if _, err = admin.pool.Exec(ctx, `UPDATE identity_memberships SET status='revoked' WHERE membership_id=$1::uuid`, user.MembershipID); err != nil {
		t.Fatal(err)
	}
	if _, err = core.ResolveOperationAuthority(ctx, scope, "model"); err == nil {
		t.Fatal("revoked membership retained nonjob operation authority")
	}
}

func assertCrossOrganizationMembershipScope(t *testing.T, ctx context.Context, admin, core *Store, user accounts.User, runID string) {
	t.Helper()
	memberID := "97777777-7777-4777-8777-777777777777"
	if _, err := admin.pool.Exec(ctx, `INSERT INTO identity_org_units(tenant_id,org_unit_id,display_name,status) VALUES($1,'member-other-org','Other member org','active')`, user.TenantID); err != nil {
		t.Fatal(err)
	}
	grants, _ := json.Marshal([]access.Grant{{Role: "reviewer", Scope: "tenant"}})
	if _, err := admin.pool.Exec(ctx, `INSERT INTO identity_memberships(membership_id,principal_id,tenant_id,org_unit_id,role_code,grants,status) VALUES($1::uuid,$2::uuid,$3,'member-other-org','reviewer',$4::jsonb,'active')`, memberID, user.PrincipalID, user.TenantID, grants); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = admin.pool.Exec(ctx, `UPDATE identity_memberships SET status='revoked' WHERE membership_id=$1::uuid`, memberID)
	}()
	profile, ok := accounts.ByID(user.ID)
	if !ok {
		t.Fatal("cross-org profile missing")
	}
	pending, err := core.ResolveVerifiedOIDC(ctx, profile, "client")
	if err != nil || pending.PendingToken == "" {
		t.Fatalf("cross-org pending login: %v", err)
	}
	login, err := core.ConsumePendingMembership(ctx, pending.PendingToken, memberID, "client")
	if err != nil {
		t.Fatal(err)
	}
	if login.User.OrgID != "member-other-org" {
		t.Fatal("membership organization was replaced by resource organization")
	}
	now := "2026-11-02T00:00:00Z"
	if _, err = admin.pool.Exec(ctx, `INSERT INTO review_attempts(id,run_id,ordinal,state,accepted_at,history_kind,actor_id,tenant_id,org_id,input_hash) VALUES('cross-org-attempt',$1,2,'running',$2,'frozen',$3,$4,$5,'fixed-cross-org-input')`, runID, now, user.ID, user.TenantID, user.OrgID); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.pool.Exec(ctx, `INSERT INTO worker_jobs(id,tenant_id,org_id,case_id,run_id,attempt_id,actor_id,kind,state,generation,worker_id,lease_until,created_at,updated_at) VALUES('cross-org-job',$1,$2,'case-a',$3,'cross-org-attempt',$4,'review','running',1,'cross-org-worker',clock_timestamp()+interval '2 minutes',$5,$5)`, user.TenantID, user.OrgID, runID, user.ID, now); err != nil {
		t.Fatal(err)
	}
	dsn, role := tenantIdentityTestExecution(t, ctx, admin)
	pool, err := newStorePool(ctx, dsn, admin.schema, true)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var actor string
	var super, bypass bool
	if err = pool.QueryRow(ctx, `SELECT current_user,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&actor, &super, &bypass); err != nil || actor != role || super || bypass {
		t.Fatal("cross-org binder not ordinary Execution")
	}
	if _, err = pool.Exec(ctx, `SELECT tenant_identity_bind_admission($1,'cross-org-job')`, sessionTokenDigest(login.SessionToken)); err != nil {
		t.Fatalf("authorized cross-org admission rejected: %v", err)
	}
	scope := model.DelegatedScope{Kind: "execution", TenantID: user.TenantID, OrgID: user.OrgID, CaseID: "case-a", ActorID: user.ID, JobID: "cross-org-job", WorkerID: "cross-org-worker", Generation: 1, RunID: runID, AttemptID: "cross-org-attempt"}
	resolved, err := core.ResolveOperationAuthority(ctx, scope, "model")
	if err != nil || resolved.OrgID != "member-other-org" || resolved.MembershipID != memberID {
		t.Fatalf("cross-org authority lost exact membership: %v", err)
	}
	if _, err = admin.pool.Exec(ctx, `UPDATE identity_memberships SET grants='[]' WHERE membership_id=$1::uuid`, memberID); err != nil {
		t.Fatal(err)
	}
	if _, err = core.ResolveOperationAuthority(ctx, scope, "model"); err == nil {
		t.Fatal("different organization default-allowed without a case grant")
	}
	bad := scope
	bad.TenantID = "foreign-tenant"
	if _, err = core.ResolveOperationAuthority(ctx, bad, "model"); err == nil {
		t.Fatal("cross-tenant scope accepted")
	}
}
