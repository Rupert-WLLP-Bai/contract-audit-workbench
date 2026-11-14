package store

import (
	"context"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
)

// Reads complete persisted rows through the actual Core role, not the harness.
func legalImportAtomicSnapshot(t *testing.T, core *Store, actor accounts.User) map[string]string {
	t.Helper()
	ctx := context.Background()
	tx, err := core.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, governanceScope(actor)); err != nil {
		t.Fatal(err)
	}
	queries := map[string]string{
		"captures": `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM legal_source_captures t WHERE tenant_id=$1`,
		"versions": `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM governed_knowledge_versions t WHERE tenant_id=$1`,
		"state":    `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM governed_knowledge_state t WHERE tenant_id=$1`,
		"history":  `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM governed_knowledge_history t WHERE tenant_id=$1`,
		"binding":  `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM governed_policy_rule_bindings t WHERE tenant_id=$1`,
		"pointer":  `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM governed_knowledge_pointers t WHERE tenant_id=$1`,
		"receipt":  `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM governed_knowledge_idempotency t WHERE tenant_id=$1`,
	}
	out := make(map[string]string, len(queries))
	for name, query := range queries {
		var raw string
		if err := tx.QueryRow(ctx, query, accounts.Tenant(actor)).Scan(&raw); err != nil {
			t.Fatalf("snapshot %s: %v", name, err)
		}
		out[name] = raw
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestVerifiedLegalImportDeferredHistoryRollbackCoreRole(t *testing.T) {
	assets := os.Getenv("LEGALNORM_ASSET_DIR")
	if assets == "" {
		t.Fatal("LEGALNORM_ASSET_DIR must point to the external official legal-sources directory")
	}
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated role checks")
	}
	core := env.openRole(t, "core")
	actor, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo administrator missing")
	}
	ctx := context.Background()
	original := legalSourceImportFixture(t, assets, "S1_civil_code_contract")
	original.IdempotencyKey = "c306-original"
	baseline, err := core.ImportLegalSource(ctx, actor, original)
	if err != nil || len(baseline.Articles) != 5 {
		t.Fatalf("valid baseline import=%+v err=%v", baseline, err)
	}
	input := legalSourceImportFixture(t, assets, "S1_civil_code_contract")
	input.IdempotencyKey = "c306-failure"
	for i := range input.Articles {
		input.Articles[i].KnowledgeID = "c306-new-" + strconv.Itoa(i+1)
	}
	before := legalImportAtomicSnapshot(t, core, actor)
	for _, name := range []string{"captures", "versions", "state", "history"} {
		if before[name] == "[]" {
			t.Fatalf("baseline %s is empty", name)
		}
	}
	const ddl = `CREATE FUNCTION c306_fail_deferred() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.knowledge_id NOT LIKE 'c306-new-%' THEN RETURN NEW; END IF;
 IF NOT EXISTS (SELECT 1 FROM legal_source_captures c WHERE c.tenant_id=NEW.tenant_id AND c.created_by=NEW.actor_id AND c.idempotency_key='c306-failure' AND jsonb_array_length(c.imported_article_refs)=5
 AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(c.imported_article_refs) ref WHERE NOT EXISTS
  (SELECT 1 FROM governed_knowledge_versions v JOIN governed_knowledge_state s USING (tenant_id,knowledge_id,version_id) JOIN governed_knowledge_history h USING (tenant_id,knowledge_id,version_id)
   WHERE v.tenant_id=c.tenant_id AND v.knowledge_id=ref->>'knowledge_id' AND v.version_id=ref->>'version_id' AND v.content_sha256=ref->>'content_sha256' AND v.kind='legal' AND v.legal_capture_snapshot_id=c.capture_snapshot_id AND v.created_by=c.created_by
   AND convert_from(v.content,'UTF8')::jsonb->'legal_metadata'->>'source_snapshot_id'=c.capture_snapshot_id AND convert_from(v.content,'UTF8')::jsonb->'legal_metadata'->>'legal_review_status'='NOT_RUN'
   AND s.status='draft' AND s.revision=1 AND h.action='create' AND h.revision=1 AND h.pointer_revision=0 AND h.actor_id=c.created_by AND h.reason='导入已核验的官方法律来源' AND h.content_sha256=v.content_sha256)))
 THEN RAISE EXCEPTION 'C306_IMPORT_PRECONDITION'; END IF;
 RAISE EXCEPTION 'C306_IMPORT_FORCED'; END $$;
 CREATE CONSTRAINT TRIGGER c306_import_deferred AFTER INSERT ON governed_knowledge_history DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION c306_fail_deferred()`
	if _, err := env.harness.pool.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.harness.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS c306_import_deferred ON governed_knowledge_history; DROP FUNCTION IF EXISTS c306_fail_deferred()`)
	})
	if _, err := core.ImportLegalSource(ctx, actor, input); err == nil || !strings.Contains(err.Error(), "C306_IMPORT_FORCED") {
		t.Fatalf("expected C306_IMPORT_FORCED, got %v", err)
	}
	after := legalImportAtomicSnapshot(t, core, actor)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("partial import: before=%v after=%v", before, after)
	}
	if _, err := env.harness.pool.Exec(ctx, `DROP TRIGGER c306_import_deferred ON governed_knowledge_history; DROP FUNCTION c306_fail_deferred()`); err != nil {
		t.Fatal(err)
	}
	created, err := core.ImportLegalSource(ctx, actor, input)
	if err != nil || len(created.Articles) != 5 || created.CaptureSnapshotID == baseline.CaptureSnapshotID {
		t.Fatalf("retry import=%+v err=%v", created, err)
	}
	replay, err := core.ImportLegalSource(ctx, actor, input)
	if err != nil || !reflect.DeepEqual(replay, created) {
		t.Fatalf("same key replay=%+v err=%v", replay, err)
	}
}
