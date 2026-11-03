package store

import (
	"context"
	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	contract "contractaudit/internal/contract/lifecycle"
)

// assertLifecycleMatchesExisting compares real projections, including material
// redaction and all historical revisions, with the established read APIs.
func assertLifecycleMatchesExisting(t *testing.T, fixture durableCaseFixture, id string) contract.LifecycleOverview {
	t.Helper()
	ctx := context.Background()
	overview, err := fixture.core.ContractLifecycleOverview(ctx, fixture.admin, id)
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := fixture.core.ListContractDeliveries(ctx, fixture.admin, id)
	if err != nil {
		t.Fatal(err)
	}
	acceptances, err := fixture.core.ListContractAcceptances(ctx, fixture.admin, id)
	if err != nil {
		t.Fatal(err)
	}
	payments, err := fixture.core.ListContractPayments(ctx, fixture.admin, id)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := fixture.core.ListContractAssets(ctx, fixture.admin, id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(overview.Deliveries, deliveries) || !reflect.DeepEqual(overview.Acceptances, acceptances) || !reflect.DeepEqual(overview.Payments, payments) || !reflect.DeepEqual(overview.Assets, assets) {
		t.Fatalf("overview differs from existing reads: %+v", overview)
	}
	if overview.Contract.ContractID != id || overview.Contract.RowVersion != deliveries.RowVersion || overview.Contract.RowVersion != assets.RowVersion || overview.ObservedAt.IsZero() || overview.SourceObservedAt.Finance != nil || overview.SourceObservedAt.ERP != nil {
		t.Fatalf("inconsistent version/time: %+v", overview)
	}
	for _, item := range deliveries.Items {
		if item.ContractID != id {
			t.Fatalf("delivery lost its contract identity: %+v", item)
		}
	}
	for _, item := range acceptances.Items {
		if item.ContractID != id {
			t.Fatalf("acceptance lost its contract identity: %+v", item)
		}
	}
	if overview.Completeness.Disputes != "unknown" || overview.Completeness.Warranty != "unknown" || overview.Completeness.OtherObligations != "unknown" {
		t.Fatalf("unknown obligations invented: %+v", overview.Completeness)
	}
	encoded, err := json.Marshal(overview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "6222000000000001") || strings.Contains(string(encoded), testRestrictedBankAccount) || strings.Contains(string(encoded), `"bank_account":`) || strings.Contains(string(encoded), `"supplier":`) {
		t.Fatal("overview leaked restricted contract snapshot fields")
	}
	return overview
}

// Called after the signing/payment fixture has already created valid receipts.
// A real trusted receipt commits while the read is blocked at its first fact
// query, after contract metadata established the reader's database snapshot.
func assertLifecycleSnapshotUnderConcurrentReceipt(t *testing.T, fixture durableCaseFixture, contractID, revisionID string, source TrustedPaymentSource) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	before := assertLifecycleMatchesExisting(t, fixture, contractID)
	blocker, err := fixture.environment.harness.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackCLM(blocker)
	var blockerPID int32
	if err = blocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(ctx, `LOCK TABLE clm_contract_deliveries IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	type result struct {
		overview contract.LifecycleOverview
		err      error
	}
	done := make(chan result, 1)
	go func() {
		value, e := fixture.core.ContractLifecycleOverview(ctx, fixture.admin, contractID)
		done <- result{value, e}
	}()
	// Restrict by the actual blocker PID, so unrelated test sessions cannot
	// accidentally satisfy the synchronization condition.
	for {
		var blocked bool
		err = fixture.environment.harness.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks waiting JOIN pg_locks held ON held.relation=waiting.relation WHERE held.pid=$1 AND held.granted AND held.mode='AccessExclusiveLock' AND NOT waiting.granted AND waiting.mode='AccessShareLock')`, blockerPID).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case result := <-done:
			t.Fatalf("reader returned before blocking: %v", result.err)
		case <-ctx.Done():
			t.Fatal("reader never blocked after establishing its snapshot")
		case <-time.After(10 * time.Millisecond):
		}
	}
	marker := ContractPaymentReceiptInput{ContractID: contractID, RevisionID: revisionID, Sequence: 2, EventID: "lifecycle-concurrent-marker", EventType: "payment", Amount: "1.00", Currency: "CNY", OccurredAt: time.Now().UTC()}
	if _, replay, err := fixture.core.RecordTrustedContractPaymentReceipt(ctx, source, marker); err != nil || replay {
		t.Fatalf("commit concurrent receipt: replay=%v err=%v", replay, err)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var got result
	select {
	case got = <-done:
	case <-ctx.Done():
		t.Fatal("reader did not finish after lock release")
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.overview.Contract.RowVersion != before.Contract.RowVersion || !reflect.DeepEqual(got.overview.Deliveries, before.Deliveries) || !reflect.DeepEqual(got.overview.Acceptances, before.Acceptances) || !reflect.DeepEqual(got.overview.Payments, before.Payments) || !reflect.DeepEqual(got.overview.Assets, before.Assets) {
		t.Fatalf("mixed snapshot: before=%+v got=%+v", before, got.overview)
	}
	after := assertLifecycleMatchesExisting(t, fixture, contractID)
	if after.Contract.RowVersion != before.Contract.RowVersion+1 || reflect.DeepEqual(after.Payments, before.Payments) {
		t.Fatalf("new receipt/version not visible in subsequent snapshot: before=%+v after=%+v", before, after)
	}
	payments, ok := after.Payments.([]ContractPaymentSummary)
	if !ok {
		t.Fatalf("unexpected payment type %T", after.Payments)
	}
	found := false
	for _, payment := range payments {
		for _, receipt := range payment.Receipts {
			if receipt.SourceEventID == marker.EventID {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("subsequent overview missing concurrent receipt marker")
	}
}

// Grant the reader the contract only, then verify every evidence snapshot is
// redacted exactly as in the independent endpoints after source-case revocation.
func assertLifecycleMaterialRevocation(t *testing.T, fixture durableCaseFixture, contractID string) {
	t.Helper()
	ctx := context.Background()
	directory := filepath.Join(t.TempDir(), "lifecycle-directory.json")
	body := fmt.Sprintf(`{"users":[{"id":"demo-admin","display_name":"合同管理员","role":"admin","tenant_id":%q,"org_id":%q,"enabled":true,"issuer":%q,"subject":%q,"grants":[{"role":"case_owner","scope":"case","case_id":%q}]},{"id":"lifecycle-reader","display_name":"合同只读","role":"reviewer","tenant_id":%q,"org_id":%q,"enabled":true,"issuer":"lifecycle-test","subject":"reader","grants":[{"role":"case_owner","scope":"case","case_id":%q}]}]}`, fixture.admin.TenantID, fixture.admin.OrgID, fixture.admin.Issuer, fixture.admin.Subject, contractID, fixture.admin.TenantID, fixture.admin.OrgID, contractID)
	if err := os.WriteFile(directory, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ConfigureDirectory(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = accounts.ConfigureDirectory("") })
	grant := []access.Grant{{Role: "case_owner", Scope: "case", CaseID: contractID}}
	adminMembership := approvalIdentityCrosswalk().Memberships[0]
	adminMembership.Grants = grant
	importApprovalIdentity(t, fixture.core, IdentityCrosswalk{Version: 1, Principals: []IdentityPrincipalMap{{ID: "c5555555-5555-4555-8555-555555555555", ActorID: "lifecycle-reader", Issuer: "lifecycle-test", Subject: "reader", Status: "active"}}, Memberships: []IdentityMembershipMap{adminMembership, {ID: "d5555555-5555-4555-8555-555555555555", ActorID: "lifecycle-reader", TenantID: fixture.admin.TenantID, OrgUnitID: fixture.admin.OrgID, RoleCode: "reviewer", Grants: grant, Status: "active"}}})
	reader := approvalTestUser(t, "lifecycle-reader", fixture.core)
	overview, err := fixture.core.ContractLifecycleOverview(ctx, reader, contractID)
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := fixture.core.ListContractDeliveries(ctx, reader, contractID)
	if err != nil {
		t.Fatal(err)
	}
	acceptances, err := fixture.core.ListContractAcceptances(ctx, reader, contractID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(overview.Deliveries, deliveries) || !reflect.DeepEqual(overview.Acceptances, acceptances) {
		t.Fatal("lifecycle differs from material ACL of old endpoints")
	}
	if len(deliveries.Items) == 0 || !deliveries.Items[0].EvidenceRedacted || len(deliveries.Items[0].Evidence) != 0 {
		t.Fatalf("delivery evidence not redacted: %+v", deliveries)
	}
	if len(acceptances.Items) == 0 || !acceptances.Items[0].EvidenceRedacted || len(acceptances.Items[0].Evidence) != 0 {
		t.Fatalf("acceptance evidence not redacted: %+v", acceptances)
	}
}
