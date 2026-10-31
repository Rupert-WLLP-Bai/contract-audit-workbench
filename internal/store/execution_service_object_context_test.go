package store

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"contractaudit/internal/accounts"
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
