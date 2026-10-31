package store

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

func TestDurableReviewFreezesServiceObjectContextAndRejectsChangedRetryAfterReopen(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("MIGRATOR_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for service object snapshot persistence")
	}
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated PostgreSQL test environment is unavailable")
	}
	admin, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo administrator missing")
	}
	ctx := context.Background()
	core := env.openRole(t, "core")
	document := env.openRole(t, "document")
	execution := env.openRole(t, "execution")
	pack := publishSyntheticAcceptancePack(t, core, admin)
	publishSyntheticAcceptancePolicy(t, core, admin, pack)
	caseID, err := core.CreateCaseWithBusiness(admin, "服务对象审查快照", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatalf("create telecom case: %v", err)
	}
	for _, item := range []struct{ role, body string }{
		{"draft_contract", "拟签合同：电路 LINE-OBJ-001 最低保障带宽 100 Mbit/s；月可用率不低于 99.9%；故障恢复时间不超过 4 小时。"},
		{"procurement", "采购文件：电路 LINE-OBJ-001 最低保障带宽 100 Mbit/s；审批上限 20%。"},
	} {
		if _, err := document.AddDocument(ctx, admin, caseID, item.role, item.role+".txt", "text/plain", []byte(item.body)); err != nil {
			t.Fatalf("add %s material: %v", item.role, err)
		}
	}
	if _, _, err := core.CreateServiceObject(ctx, admin, caseID, ServiceObjectCreateInput{
		ObjectType: ServiceObjectDedicatedLine, DisplayName: "专线 LINE-OBJ-001",
		Aliases: []ServiceObjectAlias{{Kind: "circuit", Value: "LINE-OBJ-001"}},
	}, "review-context-object-1"); err != nil {
		t.Fatalf("create service object before acceptance: %v", err)
	}
	execution.UseDocumentPreparer(document)
	execution.UseModels(&frozenOCRModel{})
	accepted, err := execution.StartReviewAsyncAt(ctx, admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatalf("accept review: %v", err)
	}
	claim, err := execution.ClaimNextJob(ctx, "service-object-context-worker", time.Minute)
	if err != nil || claim.JobID != accepted.JobID {
		t.Fatalf("claim accepted review: claim=%+v err=%v", claim, err)
	}
	if err := execution.RunClaimedJob(ctx, claim); err != nil {
		t.Fatalf("finish accepted review: %v", err)
	}
	beforeState, err := execution.snapshotScoped(ctx, userScope(admin))
	if err != nil {
		t.Fatal(err)
	}
	before := beforeState.snapshotOf(accepted.RunID)
	if before.ServiceObjectContext == nil || before.ServiceObjectContextHash == "" ||
		before.ServiceObjectContextHash != before.ServiceObjectContext.SHA256 || before.ServiceObjectContext.Complete {
		t.Fatalf("accepted review did not freeze unresolved P3 inventory: %+v", before.ServiceObjectContext)
	}
	if err := validateServiceObjectReviewContext(before); err != nil {
		t.Fatalf("accepted service object context failed integrity validation: %v", err)
	}

	// Reopen through the execution application role to prove the acceptance
	// snapshot and digest are persisted by PostgreSQL, not process memory.
	if err := execution.Close(); err != nil {
		t.Fatal(err)
	}
	for i, store := range env.stores {
		if store == execution {
			env.stores = append(env.stores[:i], env.stores[i+1:]...)
			break
		}
	}
	execution = env.openRole(t, "execution")
	reopenedState, err := execution.snapshotScoped(ctx, userScope(admin))
	if err != nil {
		t.Fatalf("reload accepted review after reopen: %v", err)
	}
	reopened := reopenedState.snapshotOf(accepted.RunID)
	if !sameRunSignature(before, reopened) || reopened.ServiceObjectContextHash != before.ServiceObjectContextHash ||
		!reflect.DeepEqual(reopened.ServiceObjectContext, before.ServiceObjectContext) {
		t.Fatalf("service object context changed across close/reopen: before=%+v reopened=%+v", before.ServiceObjectContext, reopened.ServiceObjectContext)
	}

	if _, _, err := core.CreateServiceObject(ctx, admin, caseID, ServiceObjectCreateInput{
		ObjectType: ServiceObjectService, DisplayName: "线路保障服务",
		Aliases: []ServiceObjectAlias{{Kind: "service_code", Value: "LINE-SVC-001"}},
	}, "review-context-object-2"); err != nil {
		t.Fatalf("append changed service-object inventory: %v", err)
	}
	if _, err := execution.RetryAsync(ctx, admin, caseID, accepted.RunID); !isConflictFault(err) {
		t.Fatalf("retry adopted changed service-object context instead of preserving the accepted snapshot: %v", err)
	}
	afterState, err := execution.snapshotScoped(ctx, userScope(admin))
	if err != nil {
		t.Fatal(err)
	}
	after := afterState.snapshotOf(accepted.RunID)
	if !sameRunSignature(before, after) || after.ServiceObjectContextHash != before.ServiceObjectContextHash ||
		!reflect.DeepEqual(after.ServiceObjectContext, before.ServiceObjectContext) {
		t.Fatalf("rejected retry rewrote the prior frozen context: before=%+v after=%+v", before.ServiceObjectContext, after.ServiceObjectContext)
	}
}

// TestUngovernedCaseWithServiceObjectRequiresBusinessTimeAndFreezesContext
// proves the P3 freeze no longer depends on a governed pack. The ungoverned
// case must name its business instant, freeze the registered object inventory,
// stay idempotent for an active duplicate that has no AttemptInput yet, and
// reject a real retry after the inventory or the actor authorization changed.
func TestUngovernedCaseWithServiceObjectRequiresBusinessTimeAndFreezesContext(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("MIGRATOR_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for service object snapshot persistence")
	}
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated PostgreSQL test environment is unavailable")
	}
	admin, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo administrator missing")
	}
	reviewer, found := accounts.ByID("demo-reviewer")
	if !found {
		t.Fatal("demo reviewer missing")
	}
	ctx := context.Background()
	core := env.openRole(t, "core")
	document := env.openRole(t, "document")
	execution := env.openRole(t, "execution")
	caseID, err := core.CreateCaseWithBusiness(admin, "无治理服务对象冻结", "service", "", "")
	if err != nil {
		t.Fatalf("create ungoverned case: %v", err)
	}
	for _, item := range []struct{ role, body string }{
		{"draft_contract", "拟签合同：机房服务可用率不低于 99.9%，故障恢复时间不超过 4 小时。"},
		{"procurement", "采购文件：机房服务可用率不低于 99.9%，审批上限 20%。"},
	} {
		if _, err := document.AddDocument(ctx, admin, caseID, item.role, item.role+".txt", "text/plain", []byte(item.body)); err != nil {
			t.Fatalf("add %s material: %v", item.role, err)
		}
	}
	if _, _, err := core.CreateServiceObject(ctx, admin, caseID, ServiceObjectCreateInput{
		ObjectType: ServiceObjectService, DisplayName: "机房保障服务",
		Aliases: []ServiceObjectAlias{{Kind: "service_code", Value: "SVC-UNGOVERNED-1"}},
	}, "ungoverned-context-object-1"); err != nil {
		t.Fatalf("create service object before acceptance: %v", err)
	}
	execution.UseDocumentPreparer(document)
	execution.UseModels(&frozenOCRModel{})
	if _, err := execution.StartReviewAsync(ctx, admin, caseID); !isInvalidServiceObjectFault(err) {
		t.Fatalf("acceptance without an explicit business time was not rejected: %v", err)
	}
	rejectedState, err := execution.snapshotScoped(ctx, userScope(admin))
	if err != nil {
		t.Fatal(err)
	}
	if len(rejectedState.Runs) != 0 {
		t.Fatalf("rejected acceptance committed runs: %+v", rejectedState.Runs)
	}

	accepted, err := execution.StartReviewAsyncAt(ctx, admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatalf("accept ungoverned review with a service object: %v", err)
	}
	frozenState, err := execution.snapshotScoped(ctx, userScope(admin))
	if err != nil {
		t.Fatal(err)
	}
	frozen := frozenState.snapshotOf(accepted.RunID)
	if frozen.GovernedKnowledge != nil {
		t.Fatalf("ungoverned case unexpectedly froze governed knowledge: %+v", frozen.GovernedKnowledge)
	}
	if frozen.ServiceObjectContext == nil || frozen.ServiceObjectContextHash == "" ||
		frozen.ServiceObjectContextHash != frozen.ServiceObjectContext.SHA256 {
		t.Fatalf("ungoverned case with a service object did not freeze its context: %+v", frozen.ServiceObjectContext)
	}
	businessAt, err := time.Parse(time.RFC3339Nano, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	if want := businessAt.UTC().Truncate(time.Microsecond); !frozen.ServiceObjectContext.BusinessAt.Equal(want) {
		t.Fatalf("frozen context did not keep the explicit business instant: got=%s want=%s", frozen.ServiceObjectContext.BusinessAt, want)
	}

	// The accepted attempt is active and has no AttemptInput yet. A repeated
	// retry must remain idempotent instead of failing on the missing input.
	repeated, err := execution.RetryAsync(ctx, admin, caseID, accepted.RunID)
	if err != nil {
		t.Fatalf("idempotent retry before AttemptInput preparation failed: %v", err)
	}
	if repeated != accepted {
		t.Fatalf("active retry changed the accepted attempt: got=%+v want=%+v", repeated, accepted)
	}
	repeatedState, err := execution.snapshotScoped(ctx, userScope(admin))
	if err != nil {
		t.Fatal(err)
	}
	if count := serviceObjectContextAttemptCount(repeatedState, accepted.RunID); count != 1 {
		t.Fatalf("active retry created a new attempt: count=%d", count)
	}
	if _, hasInput := repeatedState.attemptInput(accepted.AttemptID); hasInput {
		t.Fatal("test expected AttemptInput to remain unprepared for the active attempt")
	}
	claim, err := execution.ClaimNextJob(ctx, "ungoverned-service-object-worker", time.Minute)
	if err != nil || claim.JobID != accepted.JobID {
		t.Fatalf("claim accepted review: claim=%+v err=%v", claim, err)
	}
	if err := execution.RunClaimedJob(ctx, claim); err != nil {
		t.Fatalf("finish accepted review: %v", err)
	}

	// A changed object inventory rejects a real retry and leaves both the
	// accepted attempt and the frozen context untouched.
	if _, _, err := core.CreateServiceObject(ctx, admin, caseID, ServiceObjectCreateInput{
		ObjectType: ServiceObjectService, DisplayName: "机房保障服务（新增）",
		Aliases: []ServiceObjectAlias{{Kind: "service_code", Value: "SVC-UNGOVERNED-2"}},
	}, "ungoverned-context-object-2"); err != nil {
		t.Fatalf("append changed service-object inventory: %v", err)
	}
	if _, err := execution.RetryAsync(ctx, admin, caseID, accepted.RunID); !isConflictFault(err) {
		t.Fatalf("retry adopted a changed service-object inventory: %v", err)
	}
	afterChange, err := execution.snapshotScoped(ctx, userScope(admin))
	if err != nil {
		t.Fatal(err)
	}
	if count := serviceObjectContextAttemptCount(afterChange, accepted.RunID); count != 1 {
		t.Fatalf("rejected retry created or replaced an attempt: count=%d", count)
	}
	if got := afterChange.snapshotOf(accepted.RunID); !sameRunSignature(frozen, got) ||
		got.ServiceObjectContextHash != frozen.ServiceObjectContextHash ||
		!reflect.DeepEqual(got.ServiceObjectContext, frozen.ServiceObjectContext) {
		t.Fatalf("rejected retry rewrote the frozen context: before=%+v after=%+v", frozen.ServiceObjectContext, got.ServiceObjectContext)
	}

	// Revoking the actor's case membership rejects the retry before object
	// dependencies are read; no new attempt may appear.
	if err := core.AddMember(admin, caseID, reviewer.ID); err != nil {
		t.Fatalf("authorize reviewer for case: %v", err)
	}
	if _, err := core.pool.Exec(ctx, `UPDATE cases SET member_ids='[]' WHERE tenant_id=$1 AND id=$2`, admin.TenantID, caseID); err != nil {
		t.Fatalf("revoke case membership: %v", err)
	}
	if _, err := execution.RetryAsync(ctx, reviewer, caseID, accepted.RunID); err == nil {
		t.Fatal("revoked actor retried the review")
	}
	afterRevoke, err := execution.snapshotScoped(ctx, userScope(admin))
	if err != nil {
		t.Fatal(err)
	}
	if count := serviceObjectContextAttemptCount(afterRevoke, accepted.RunID); count != 1 {
		t.Fatalf("revoked retry created an attempt: count=%d", count)
	}
}

// TestLegacyUngovernedRunWithoutServiceObjectsKeepsNilContextOnRetry proves
// that the historical nil extension is preserved. A case with no service
// object must not gain a context on reads or on a genuine retry.
func TestLegacyUngovernedRunWithoutServiceObjectsKeepsNilContextOnRetry(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("MIGRATOR_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for service object snapshot persistence")
	}
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated PostgreSQL test environment is unavailable")
	}
	admin, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo administrator missing")
	}
	ctx := context.Background()
	core := env.openRole(t, "core")
	document := env.openRole(t, "document")
	execution := env.openRole(t, "execution")
	caseID, err := core.CreateCaseWithBusiness(admin, "无对象历史审查", "service", "", "")
	if err != nil {
		t.Fatalf("create ungoverned case: %v", err)
	}
	for _, item := range []struct{ role, body string }{
		{"draft_contract", "拟签合同：机房服务可用率不低于 99.9%。"},
		{"procurement", "采购文件：机房服务可用率不低于 99.9%。"},
	} {
		if _, err := document.AddDocument(ctx, admin, caseID, item.role, item.role+".txt", "text/plain", []byte(item.body)); err != nil {
			t.Fatalf("add %s material: %v", item.role, err)
		}
	}
	execution.UseDocumentPreparer(document)
	execution.UseModels(&frozenOCRModel{})
	accepted, err := execution.StartReviewAsync(ctx, admin, caseID)
	if err != nil {
		t.Fatalf("legacy accept without a business time failed for an object-free case: %v", err)
	}
	state, err := execution.snapshotScoped(ctx, userScope(admin))
	if err != nil {
		t.Fatal(err)
	}
	first := state.snapshotOf(accepted.RunID)
	if first.ServiceObjectContext != nil || first.ServiceObjectContextHash != "" {
		t.Fatalf("object-free ungoverned acceptance gained a service-object context: %+v", first.ServiceObjectContext)
	}
	claim, err := execution.ClaimNextJob(ctx, "legacy-service-object-worker", time.Minute)
	if err != nil || claim.JobID != accepted.JobID {
		t.Fatalf("claim accepted review: claim=%+v err=%v", claim, err)
	}
	if err := execution.RunClaimedJob(ctx, claim); err != nil {
		t.Fatalf("finish accepted review: %v", err)
	}
	retried, err := execution.RetryAsync(ctx, admin, caseID, accepted.RunID)
	if err != nil {
		t.Fatalf("retry of an object-free legacy run failed: %v", err)
	}
	if retried.AttemptID == accepted.AttemptID {
		t.Fatalf("retry reused the accepted attempt: %+v", retried)
	}
	after, err := execution.snapshotScoped(ctx, userScope(admin))
	if err != nil {
		t.Fatal(err)
	}
	if count := serviceObjectContextAttemptCount(after, accepted.RunID); count != 2 {
		t.Fatalf("retry did not append exactly one attempt: count=%d", count)
	}
	if got := after.snapshotOf(accepted.RunID); got.ServiceObjectContext != nil || got.ServiceObjectContextHash != "" {
		t.Fatalf("retry backfilled a service-object context into the historical snapshot: %+v", got.ServiceObjectContext)
	}
}

func serviceObjectContextAttemptCount(st state, runID string) int {
	count := 0
	for _, attempt := range st.Attempts {
		if attempt.RunID == runID {
			count++
		}
	}
	return count
}

func isInvalidServiceObjectFault(err error) bool {
	var apiErr *fault.Error
	return errors.As(err, &apiErr) && apiErr.Status == 422 && apiErr.Code == "invalid" && strings.Contains(apiErr.Message, "业务时间")
}
