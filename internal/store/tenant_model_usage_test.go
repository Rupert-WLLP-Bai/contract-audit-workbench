package store

import (
	"context"
	"testing"

	"contractaudit/internal/accounts"
)

func TestTenantModelUsageWindowAndTrustedTenant(t *testing.T) {
	st := openModelBudgetStore(t)
	ctx := context.Background()
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	_, err := st.pool.Exec(ctx, `
        INSERT INTO model_usage (call_id,lease_id,tenant_id,actor_id,case_id,run_id,attempt_id,caller_identity,model_ref,attempt_number,call_status,total_tokens,charged_tokens,reserved_at,occurred_at,budget_released)
        VALUES
        ('known','known',$1,'actor','case','run','attempt','worker','model',1,'succeeded',20,20,now()-interval '10 seconds',now(),false),
        ('unknown','unknown',$1,'actor','case','run','attempt','worker','model',1,'failed',NULL,80,now()-interval '10 seconds',now(),false),
        ('released','released',$1,'actor','case','run','attempt','worker','model',1,'failed',NULL,9000,now()-interval '10 seconds',now(),true),
        ('old','old',$1,'actor','case','run','attempt','worker','model',1,'succeeded',7777,7777,now()-interval '2 minutes',now(),false),
        ('legacy','legacy',$1,'actor','case','run','attempt','worker','model',1,'succeeded',7,NULL,NULL,now(),false),
        ('foreign','foreign','other-tenant','actor','case','run','attempt','worker','model',1,'succeeded',90000,90000,now(),now(),false)`, admin.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.pool.Exec(ctx, `
        INSERT INTO model_call_leases (lease_id,tenant_id,actor_id,case_id,run_id,attempt_id,caller_identity,model_ref,attempt_number,reserved_input_tokens,reserved_output_tokens,acquired_at,expires_at)
        VALUES
        ('active',$1,'actor','case','run','attempt','worker','model',1,10,20,now()-interval '10 seconds',now()+interval '1 minute'),
        ('expired',$1,'actor','case','run','attempt','worker','model',1,15,25,now()-interval '20 seconds',now()-interval '1 second'),
        ('old-active',$1,'actor','case','run','attempt','worker','model',1,40000,40000,now()-interval '2 minutes',now()+interval '1 minute'),
        ('foreign-active','other-tenant','actor','case','run','attempt','worker','model',1,300000,300000,now(),now()+interval '1 minute')`, admin.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	assertUsage := func(user accounts.User) {
		t.Helper()
		usage, err := st.ReadTenantModelUsage(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		if usage.ActiveCalls != 2 || usage.WindowCalls != 5 || usage.ChargedTokens != 107 || usage.ReservedTokens != 70 || usage.UnknownUsageCalls != 1 {
			t.Fatalf("wrong tenant or budget window accounting: %+v", usage)
		}
	}
	assertUsage(admin)
	forged := admin
	forged.TenantID = "other-tenant"
	if _, err := st.ReadTenantModelUsage(ctx, forged); err == nil {
		t.Fatal("caller-supplied tenant changed the trusted identity")
	}
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("demo reviewer missing")
	}
	reviewer.Role = "admin"
	if _, err := st.ReadTenantModelUsage(ctx, reviewer); err == nil {
		t.Fatal("caller-supplied role promoted reviewer to tenant admin")
	}
}
