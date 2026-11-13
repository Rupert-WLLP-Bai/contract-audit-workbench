package store

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
)

// governanceAtomicSnapshot reads committed rows through the actual Core role.
// It compares rows, not the separately consumed bigserial sequence value.
func governanceAtomicSnapshot(t *testing.T, core *Store, admin accounts.User, knowledgeID string) map[string]string {
	t.Helper()
	ctx := context.Background()
	tx, err := core.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = setLocalScope(ctx, tx, governanceScope(admin)); err != nil {
		t.Fatal(err)
	}
	queries := map[string]string{
		"versions": `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM governed_knowledge_versions t WHERE tenant_id=$1 AND knowledge_id=$2`,
		"state":    `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM governed_knowledge_state t WHERE tenant_id=$1 AND knowledge_id=$2`,
		"pointer":  `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM governed_knowledge_pointers t WHERE tenant_id=$1 AND knowledge_id=$2`,
		"history":  `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM governed_knowledge_history t WHERE tenant_id=$1 AND knowledge_id=$2`,
		"binding":  `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM governed_policy_rule_bindings t WHERE tenant_id=$1 AND knowledge_id=$2`,
		"receipt":  `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM governed_knowledge_idempotency t WHERE tenant_id=$1 AND actor_id=$2`,
	}
	out := make(map[string]string, len(queries))
	for name, query := range queries {
		id := knowledgeID
		if name == "receipt" {
			id = admin.ID
		}
		var raw string
		if err := tx.QueryRow(ctx, query, accounts.Tenant(admin), id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		out[name] = raw
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return out
}

// atomicPackManifest keeps this test's policy authority exact for each version.
func atomicPackManifest(t *testing.T, tenant, version string) []byte {
	t.Helper()
	var manifest map[string]any
	if err := json.Unmarshal(governedPackManifest(t, tenant, version, "100"), &manifest); err != nil {
		t.Fatal(err)
	}
	for _, item := range manifest["rules"].([]any) {
		rule := item.(map[string]any)
		for _, source := range rule["authority_sources"].([]any) {
			authority := source.(map[string]any)
			authority["id"], authority["version"] = "c304-atomic", version
		}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestGovernedKnowledgePublishAtomicFailuresCoreRole(t *testing.T) {
	for _, tc := range []struct{ name, marker, ddl, drop string }{
		{"history", "C304_HISTORY_FORCED", `CREATE FUNCTION c304_fail_history() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
   IF NEW.knowledge_id <> 'c304-atomic' OR NEW.action <> 'publish' THEN RETURN NEW; END IF;
   IF NOT EXISTS (SELECT 1 FROM governed_knowledge_state s WHERE s.tenant_id=NEW.tenant_id AND s.knowledge_id=NEW.knowledge_id AND s.version_id=NEW.version_id AND s.status='published' AND s.revision=NEW.revision AND s.approved_digest=NEW.content_sha256)
   OR NOT EXISTS (SELECT 1 FROM governed_knowledge_pointers p WHERE p.tenant_id=NEW.tenant_id AND p.knowledge_id=NEW.knowledge_id AND p.version_id=NEW.resulting_version_id AND p.version_id=NEW.version_id AND p.active AND p.pointer_revision=NEW.pointer_revision AND NEW.previous_active AND NEW.previous_version_id<>'' AND NEW.resulting_active) THEN RAISE EXCEPTION 'C304_HISTORY_PRECONDITION'; END IF;
   RAISE EXCEPTION 'C304_HISTORY_FORCED'; END $$;
   CREATE TRIGGER c304_history BEFORE INSERT ON governed_knowledge_history FOR EACH ROW EXECUTE FUNCTION c304_fail_history()`, `DROP TRIGGER c304_history ON governed_knowledge_history`},
		{"deferred receipt", "C304_COMMIT_FORCED", `CREATE FUNCTION c304_fail_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
   IF NEW.idem_key <> 'c304-publish-failure' THEN RETURN NEW; END IF;
   IF NOT EXISTS (SELECT 1 FROM governed_knowledge_idempotency i WHERE i.tenant_id=NEW.tenant_id AND i.actor_id=NEW.actor_id AND i.idem_key=NEW.idem_key AND i.request_digest=NEW.request_digest AND i.response=NEW.response)
   OR NOT EXISTS (SELECT 1 FROM governed_knowledge_state s JOIN governed_knowledge_versions v USING (tenant_id,knowledge_id,version_id) JOIN governed_knowledge_pointers p ON p.tenant_id=s.tenant_id AND p.knowledge_id=s.knowledge_id AND p.version_id=s.version_id JOIN governed_knowledge_history h ON h.tenant_id=s.tenant_id AND h.knowledge_id=s.knowledge_id AND h.version_id=s.version_id WHERE s.tenant_id=NEW.tenant_id AND s.knowledge_id='c304-atomic' AND v.display_version='2' AND s.status='published' AND s.approved_digest=v.content_sha256 AND p.active AND p.pointer_revision=2 AND h.action='publish' AND h.revision=s.revision AND h.pointer_revision=p.pointer_revision AND h.actor_id=NEW.actor_id AND h.content_sha256=v.content_sha256 AND h.previous_active AND h.previous_version_id<>'' AND h.resulting_active AND h.resulting_version_id=s.version_id) THEN RAISE EXCEPTION 'C304_COMMIT_PRECONDITION'; END IF;
   RAISE EXCEPTION 'C304_COMMIT_FORCED'; END $$;
   CREATE CONSTRAINT TRIGGER c304_commit AFTER INSERT ON governed_knowledge_idempotency DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION c304_fail_commit()`, `DROP TRIGGER c304_commit ON governed_knowledge_idempotency`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, ok := newDurableRoleEnvironment(t)
			if !ok {
				t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for isolated role checks")
			}
			core := env.openRole(t, "core")
			admin, ok := accounts.ByID("demo-admin")
			if !ok {
				t.Fatal("demo admin missing")
			}
			pack := publishGovernedPackStore(t, core, admin, "1", "100", atomicPackManifest(t, accounts.Tenant(admin), "1"))
			first := createKnowledgeDraft(t, core, admin, knowledgeFixtureContent(pack, "c304-atomic", "1", "policy"), "c304-first-create")
			publishKnowledge(t, core, admin, first, "c304-first")
			pack2 := publishGovernedPackStore(t, core, admin, "2", "100", atomicPackManifest(t, accounts.Tenant(admin), "2"))
			second := createKnowledgeDraft(t, core, admin, knowledgeFixtureContent(pack2, "c304-atomic", "2", "policy"), "c304-second-create")
			for _, action := range []string{"submit", "validate", "approve"} {
				second = advanceKnowledge(t, core, admin, second, action, "c304-second-"+action)
			}
			before := governanceAtomicSnapshot(t, core, admin, "c304-atomic")
			if _, err := env.harness.pool.Exec(context.Background(), tc.ddl); err != nil {
				t.Fatal(err)
			}
			request := knowledgeCommand(second, "publish", "c304-publish-failure")
			if _, err := core.GovernedKnowledgeCommand(admin, request); err == nil || !strings.Contains(err.Error(), tc.marker) {
				t.Fatalf("expected marker %s, got %v", tc.marker, err)
			}
			after := governanceAtomicSnapshot(t, core, admin, "c304-atomic")
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("partial commit: before=%v after=%v", before, after)
			}
			if _, err := env.harness.pool.Exec(context.Background(), tc.drop); err != nil {
				t.Fatal(err)
			}
			if _, err := core.GovernedKnowledgeCommand(admin, request); err != nil {
				t.Fatalf("retry after rollback: %v", err)
			}
		})
	}
}
