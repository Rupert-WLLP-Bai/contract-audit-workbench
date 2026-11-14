package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
)

func legalFactAtomicSnapshot(t *testing.T, core *Store, actor accounts.User, caseID, orgID string) map[string]string {
	t.Helper()
	ctx := context.Background()
	tx, err := core.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	scope := userScope(actor)
	scope.CaseID = caseID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		t.Fatal(err)
	}
	queries := map[string]string{
		"heads":    `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM case_legal_applicability_facts t WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3`,
		"versions": `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM case_legal_applicability_fact_versions t WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3`,
	}
	out := make(map[string]string, len(queries))
	for name, query := range queries {
		var raw string
		if err := tx.QueryRow(ctx, query, accounts.Tenant(actor), orgID, caseID).Scan(&raw); err != nil {
			t.Fatalf("snapshot %s: %v", name, err)
		}
		out[name] = raw
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestLegalFactDeferredVersionRollbackCoreRole(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated role checks")
	}
	core := env.openRole(t, "core")
	actor, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	ctx := context.Background()
	caseID, err := core.CreateCase(actor, "法律事实原子性", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	initial := LegalApplicabilityFactInput{ExpectedVersion: 0, IdempotencyKey: "c307-original", Dimension: "transaction_kind", Status: "unknown", UnknownReason: "insufficient_evidence", TemporalBasis: "current_state", BasisNote: "原始依据不足", BasisRefs: json.RawMessage("[]")}
	first, err := core.RecordLegalApplicabilityFact(ctx, actor, caseID, initial)
	if err != nil || first.Version != 1 {
		t.Fatalf("valid initial fact=%+v err=%v", first, err)
	}
	next := initial
	next.ExpectedVersion = 1
	next.IdempotencyKey = "c307-failure"
	next.BasisNote = "后续依据仍不足"
	digest, err := legalApplicabilityRequestDigest(accounts.Tenant(actor), first.OrgID, caseID, actor.ID, next.IdempotencyKey, next)
	if err != nil {
		t.Fatal(err)
	}
	before := legalFactAtomicSnapshot(t, core, actor, caseID, first.OrgID)
	if before["heads"] == "[]" || before["versions"] == "[]" || !strings.Contains(before["versions"], "c307-original") {
		t.Fatalf("baseline missing head/version/receipt: %v", before)
	}
	ddl := fmt.Sprintf(`CREATE FUNCTION c307_fail_deferred() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.idempotency_key <> 'c307-failure' THEN RETURN NEW; END IF;
 IF NOT EXISTS (SELECT 1 FROM case_legal_applicability_facts h JOIN case_legal_applicability_fact_versions v ON v.tenant_id=h.tenant_id AND v.org_id=h.org_id AND v.case_id=h.case_id AND v.fact_id=h.fact_id AND v.dimension=h.dimension JOIN case_legal_applicability_fact_versions old ON old.tenant_id=h.tenant_id AND old.org_id=h.org_id AND old.case_id=h.case_id AND old.fact_id=h.fact_id AND old.dimension=h.dimension AND old.version=1
 WHERE h.tenant_id=NEW.tenant_id AND h.org_id=NEW.org_id AND h.case_id=NEW.case_id AND h.fact_id=NEW.fact_id AND h.dimension='transaction_kind' AND h.current_version=2 AND v.version=2 AND v.actor_user_id=NEW.actor_user_id AND v.idempotency_key='c307-failure' AND v.request_digest='%s' AND v.basis_refs='[]'::jsonb AND v.basis_sha256='%s' AND v.status='unknown' AND v.unknown_reason='insufficient_evidence' AND v.temporal_basis='current_state' AND v.basis_note='后续依据仍不足' AND v.basis_obtained_at IS NULL AND old.actor_user_id=NEW.actor_user_id AND old.idempotency_key='c307-original' AND old.request_digest='%s' AND old.basis_refs='[]'::jsonb AND old.basis_sha256='%s' AND old.status='unknown' AND old.unknown_reason='insufficient_evidence' AND old.basis_note='原始依据不足') THEN RAISE EXCEPTION 'C307_RECORD_PRECONDITION'; END IF;
 RAISE EXCEPTION 'C307_RECORD_FORCED'; END $$;
 CREATE CONSTRAINT TRIGGER c307_record_deferred AFTER INSERT ON case_legal_applicability_fact_versions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION c307_fail_deferred()`, digest, sha256Bytes([]byte("[]")), first.RequestDigest, sha256Bytes([]byte("[]")))
	if _, err := env.harness.pool.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.harness.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS c307_record_deferred ON case_legal_applicability_fact_versions; DROP FUNCTION IF EXISTS c307_fail_deferred()`)
	})
	if _, err := core.RecordLegalApplicabilityFact(ctx, actor, caseID, next); err == nil || !strings.Contains(err.Error(), "C307_RECORD_FORCED") {
		t.Fatalf("expected C307_RECORD_FORCED, got %v", err)
	}
	after := legalFactAtomicSnapshot(t, core, actor, caseID, first.OrgID)
	if !reflect.DeepEqual(before, after) || strings.Contains(after["versions"], "c307-failure") {
		t.Fatalf("partial commit: before=%v after=%v", before, after)
	}
	if _, err := env.harness.pool.Exec(ctx, `DROP TRIGGER c307_record_deferred ON case_legal_applicability_fact_versions; DROP FUNCTION c307_fail_deferred()`); err != nil {
		t.Fatal(err)
	}
	oldReplay, err := core.RecordLegalApplicabilityFact(ctx, actor, caseID, initial)
	if err != nil || !sameLegalFactWire(oldReplay, first) {
		t.Fatalf("old replay=%+v err=%v", oldReplay, err)
	}
	second, err := core.RecordLegalApplicabilityFact(ctx, actor, caseID, next)
	if err != nil || second.Version != 2 || second.FactID != first.FactID || second.RequestDigest != digest {
		t.Fatalf("retry=%+v err=%v", second, err)
	}
	replay, err := core.RecordLegalApplicabilityFact(ctx, actor, caseID, next)
	if err != nil || !sameLegalFactWire(replay, second) {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
}
