package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
)

func operatorAtomicRows(t *testing.T, core *Store, actor accounts.User, caseID string) map[string]string {
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
	tables := []string{"service_objects", "service_object_versions", "service_object_contract_associations", "service_object_mutation_receipts", "operator_mapping_lineages", "operator_object_bindings"}
	out := make(map[string]string, len(tables))
	for _, table := range tables {
		var raw string
		query := `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM ` + table + ` t WHERE tenant_id=$1 AND case_id=$2`
		if err := tx.QueryRow(ctx, query, accounts.Tenant(actor), caseID).Scan(&raw); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		out[table] = raw
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return out
}
func operatorSQLLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func operatorTrigger(t *testing.T, env *durableRoleEnvironment, ddl, drop string) {
	t.Helper()
	if _, err := env.harness.pool.Exec(context.Background(), ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = env.harness.pool.Exec(context.Background(), drop) })
}

func operatorAtomicSession(t *testing.T, core, docs, execution *Store, actor accounts.User) (context.Context, accounts.User) {
	t.Helper()
	ctx := context.Background()
	token, err := core.EstablishSession(actor, "client")
	if err != nil {
		t.Fatal(err)
	}
	current, audience, err := core.AuthenticateSession(ctx, token)
	if err != nil || audience != "client" || current.ID != actor.ID {
		t.Fatalf("authenticate fixture: audience=%q err=%v", audience, err)
	}
	authority := durableTestOperationAuthority{core: core}
	docs.UseOperationAuthority(authority)
	execution.UseOperationAuthority(authority)
	return WithAdmissionSessionToken(ctx, token), current
}

func TestOperatorContextDeferredOutboxRollback(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	core, docs, execution := env.openRole(t, "core"), env.openRole(t, "document"), env.openRole(t, "execution")
	actor, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	ctx, actor := operatorAtomicSession(t, core, docs, execution, actor)
	pack := publishSyntheticAcceptancePack(t, core, actor)
	publishSyntheticAcceptancePolicy(t, core, actor, pack)
	caseID := governanceTestCase(t, core, actor)
	for _, doc := range []struct{ role, body string }{{"procurement", "采购文件要求电路 LINE-001 的最低带宽为100 Mbit/s。"}, {"draft_contract", "拟签合同约定电路 LINE-001 的最低带宽为100 Mbit/s。"}} {
		if _, err := docs.AddDocument(ctx, actor, caseID, doc.role, doc.role+".txt", "text/plain", []byte(doc.body)); err != nil {
			t.Fatal(err)
		}
	}
	object, replayed, err := core.CreateServiceObject(ctx, actor, caseID, ServiceObjectCreateInput{ObjectType: ServiceObjectDedicatedLine, DisplayName: "冻结故障对象"}, "c309-context-object")
	if err != nil || replayed || object.Version != 1 {
		t.Fatalf("object=%+v replay=%v err=%v", object, replayed, err)
	}
	execution.UseDocumentPreparer(docs)
	execution.UseModels(&frozenOCRModel{})
	const firstKey = "c309-context-first"
	const nextKey = "c309-context-next"
	const nextTime = "2026-02-03T01:05:07.987654321Z"
	first, err := execution.StartReviewAsyncAt(WithReviewIdempotency(ctx, firstKey), actor, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	receipt, found := acceptanceAtomicReceipt(t, execution, actor, caseID, firstKey)
	if !found || !receipt.Durable || receipt.Acceptance != first {
		t.Fatalf("first receipt=%+v found=%v", receipt, found)
	}
	st, err := execution.snapshotScoped(ctx, userScope(actor))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := st.snapshotOf(first.RunID)
	if snapshot.ServiceObjectContext == nil || snapshot.ServiceObjectContextHash == "" || snapshot.ServiceObjectContext.Complete || validateServiceObjectReviewContext(snapshot) != nil {
		t.Fatalf("bad initial context: %+v", snapshot.ServiceObjectContext)
	}
	foundObject, foundPending := false, false
	for _, item := range snapshot.ServiceObjectContext.Items {
		if item.ServiceObjectID == object.ServiceObjectID && item.ObjectVersion == 1 && item.ObjectState == ServiceObjectPendingConfirmation {
			foundObject = true
		}
		if item.UnresolvedReason == "recognized_object_inventory_pending_review" {
			foundPending = true
		}
	}
	if !foundObject || !foundPending {
		t.Fatal("missing object or pending inventory")
	}
	firstReplay, err := execution.StartReviewAsyncAt(WithReviewIdempotency(ctx, firstKey), actor, caseID, acceptanceBusinessTime)
	if err != nil || firstReplay != first {
		t.Fatalf("first replay=%+v err=%v", firstReplay, err)
	}
	expected := *snapshot.ServiceObjectContext
	at, err := time.Parse(time.RFC3339Nano, nextTime)
	if err != nil {
		t.Fatal(err)
	}
	expected.BusinessAt = at.UTC().Truncate(time.Microsecond)
	_, hash, err := canonicalServiceObjectReviewContext(expected)
	if err != nil {
		t.Fatal(err)
	}
	expected.SHA256 = hash
	contextBytes, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	requestHash, err := canonicalHash([]string{caseID, nextTime, "", ReviewScopeFull})
	if err != nil {
		t.Fatal(err)
	}
	before := acceptanceAtomicSnapshot(t, execution, actor, caseID)
	operatorBefore := operatorAtomicRows(t, core, actor, caseID)
	ddl := fmt.Sprintf(`CREATE FUNCTION c309_context_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.case_id <> %s THEN RETURN NEW; END IF;
 IF NOT EXISTS (SELECT 1 FROM outbox_events o JOIN worker_jobs j ON j.id=o.job_id AND j.tenant_id=o.tenant_id AND j.case_id=o.case_id JOIN review_runs r ON r.id=j.run_id AND r.case_id=j.case_id JOIN review_attempts a ON a.id=j.attempt_id AND a.run_id=r.id JOIN review_run_snapshots s ON s.run_id=r.id JOIN execution_write_receipts q ON q.tenant_id=o.tenant_id AND q.org_id=o.org_id AND q.case_id=o.case_id AND q.actor_id=j.actor_id
 WHERE o.id=NEW.id AND o.case_id=%s AND j.state='queued' AND j.actor_id=%s AND r.id<>%s AND s.service_object_context=convert_to(%s,'UTF8') AND s.service_object_context_hash=%s AND convert_from(s.service_object_context,'UTF8')::jsonb->>'sha256'=%s AND convert_from(s.service_object_context,'UTF8')::jsonb->>'business_at'='2026-02-03T01:05:07.987654Z' AND convert_from(s.service_object_context,'UTF8')::jsonb->>'complete'='false' AND EXISTS (SELECT 1 FROM jsonb_array_elements(convert_from(s.service_object_context,'UTF8')::jsonb->'items') x WHERE x->>'service_object_id'=%s AND x->>'object_version'='1' AND x->>'object_state'='pending_confirmation') AND EXISTS (SELECT 1 FROM jsonb_array_elements(convert_from(s.service_object_context,'UTF8')::jsonb->'items') x WHERE x->>'unresolved_reason'='recognized_object_inventory_pending_review') AND q.route='POST /api/cases/'||NEW.case_id||'/reviews' AND q.idempotency_key='c309-context-next' AND q.request_digest=%s AND q.response_json->>'durable'='true' AND q.response_json->'acceptance'->>'id'=r.id AND q.response_json->'acceptance'->>'attempt_id'=a.id AND q.response_json->'acceptance'->>'job_id'=j.id) THEN RAISE EXCEPTION 'C309_CONTEXT_PRECONDITION'; END IF;
 RAISE EXCEPTION 'C309_CONTEXT_FORCED'; END $$; CREATE CONSTRAINT TRIGGER c309_context_deferred AFTER INSERT ON outbox_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION c309_context_fail()`, operatorSQLLiteral(caseID), operatorSQLLiteral(caseID), operatorSQLLiteral(actor.ID), operatorSQLLiteral(first.RunID), operatorSQLLiteral(string(contextBytes)), operatorSQLLiteral(hash), operatorSQLLiteral(hash), operatorSQLLiteral(object.ServiceObjectID), operatorSQLLiteral(requestHash))
	drop := `DROP TRIGGER IF EXISTS c309_context_deferred ON outbox_events; DROP FUNCTION IF EXISTS c309_context_fail()`
	operatorTrigger(t, env, ddl, drop)
	if _, err := execution.StartReviewAsyncAt(WithReviewIdempotency(ctx, nextKey), actor, caseID, nextTime); err == nil || !strings.Contains(err.Error(), "C309_CONTEXT_FORCED") {
		t.Fatalf("expected C309_CONTEXT_FORCED: %v", err)
	}
	if after := acceptanceAtomicSnapshot(t, execution, actor, caseID); !reflect.DeepEqual(before, after) {
		t.Fatalf("execution rows changed: before=%v after=%v", before, after)
	}
	if after := operatorAtomicRows(t, core, actor, caseID); !reflect.DeepEqual(operatorBefore, after) {
		t.Fatalf("operator rows changed: before=%v after=%v", operatorBefore, after)
	}
	if _, found := acceptanceAtomicReceipt(t, execution, actor, caseID, nextKey); found {
		t.Fatal("failed receipt persisted")
	}
	if _, err := env.harness.pool.Exec(ctx, drop); err != nil {
		t.Fatal(err)
	}
	active, err := execution.RetryAsync(ctx, actor, caseID, first.RunID)
	if err != nil || active != first {
		t.Fatalf("active retry=%+v err=%v", active, err)
	}
	next, err := execution.StartReviewAsyncAt(WithReviewIdempotency(ctx, nextKey), actor, caseID, nextTime)
	if err != nil || next.RunID == first.RunID {
		t.Fatalf("retry start=%+v err=%v", next, err)
	}
	nextReceipt, found := acceptanceAtomicReceipt(t, execution, actor, caseID, nextKey)
	if !found || !nextReceipt.Durable || nextReceipt.Acceptance != next {
		t.Fatalf("next receipt=%+v found=%v", nextReceipt, found)
	}
	for _, v := range []struct {
		key, at string
		want    JobAcceptance
	}{{firstKey, acceptanceBusinessTime, first}, {nextKey, nextTime, next}} {
		got, err := execution.StartReviewAsyncAt(WithReviewIdempotency(ctx, v.key), actor, caseID, v.at)
		if err != nil || got != v.want {
			t.Fatalf("replay %s=%+v %v", v.key, got, err)
		}
	}
}

func operatorFrozenRows(t *testing.T, execution *Store, actor accounts.User, caseID, runID string) map[string]string {
	t.Helper()
	ctx := context.Background()
	tx, err := execution.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	scope := userScope(actor)
	scope.CaseID = caseID
	if err := setLocalScope(ctx, tx, scope); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for name, query := range map[string]string{"input": `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM review_attempt_inputs t WHERE run_id=$1`, "selections": `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM review_attempt_parse_selections t WHERE run_id=$1`} {
		var value string
		if err := tx.QueryRow(ctx, query, runID).Scan(&value); err != nil {
			t.Fatal(err)
		}
		out[name] = value
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestOperatorConfirmDeferredReceiptRollback(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	core, docs, execution := env.openRole(t, "core"), env.openRole(t, "document"), env.openRole(t, "execution")
	actor, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	ctx, actor := operatorAtomicSession(t, core, docs, execution, actor)
	pack := publishSyntheticAcceptancePack(t, core, actor)
	publishSyntheticAcceptancePolicy(t, core, actor, pack)
	caseID, err := core.CreateCaseWithBusiness(actor, "确认冻结故障", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range []struct{ role, text string }{{"draft_contract", "拟签合同：电路 LINE-OBJ-001 最低保障带宽 100 Mbit/s。月可用率不低于 99.9%，故障恢复时间不超过 4 小时。"}, {"procurement", "采购文件：电路 LINE-OBJ-001 最低保障带宽 100 Mbit/s。审批上限 20%。"}} {
		if _, err := docs.AddDocument(ctx, actor, caseID, doc.role, doc.role+".txt", "text/plain", []byte(doc.text)); err != nil {
			t.Fatal(err)
		}
	}
	execution.UseDocumentPreparer(docs)
	execution.UseModels(&frozenOCRModel{})
	accepted, err := execution.StartReviewAsyncAt(ctx, actor, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := execution.ClaimNextJob(ctx, "c309-confirm-worker", time.Minute)
	if err != nil || claim.JobID != accepted.JobID {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	if err := execution.RunClaimedJob(ctx, claim); err != nil {
		t.Fatal(err)
	}
	st, err := core.snapshotScoped(ctx, userScope(actor))
	if err != nil {
		t.Fatal(err)
	}
	frozenInput, ok := st.attemptInput(accepted.AttemptID)
	if !ok {
		t.Fatal("missing frozen input")
	}
	var frozen FrozenDocument
	for _, v := range frozenInput.Documents {
		if v.Document.Role == "draft_contract" && v.Material != nil {
			frozen = v
			break
		}
	}
	if frozen.Document.ID == "" || frozen.ParsedDocumentID == "" || frozen.Material == nil {
		t.Fatal("missing frozen draft")
	}
	object, replayed, err := core.CreateServiceObject(ctx, actor, caseID, ServiceObjectCreateInput{ObjectType: ServiceObjectDedicatedLine, DisplayName: "专线 LINE-OBJ-001"}, "c309-confirm-object")
	if err != nil || replayed || object.Version != 1 || object.AssociationVersion != 0 {
		t.Fatalf("object=%+v replay=%v err=%v", object, replayed, err)
	}
	source := ServiceObjectAssociationPreviewInput{RunID: accepted.RunID, AttemptID: accepted.AttemptID, MaterialID: frozen.Material.MaterialID, MaterialVersion: frozen.Material.Version, DocumentID: frozen.Document.ID, ParsedDocumentID: frozen.ParsedDocumentID}
	preview, err := core.PreviewServiceObjectAssociation(ctx, actor, caseID, object.ServiceObjectID, source)
	if err != nil {
		t.Fatal(err)
	}
	var option *ServiceObjectAssociationOption
	for i := range preview.Options {
		if preview.Options[i].ObjectRef == "LINE-OBJ-001" {
			option = &preview.Options[i]
			break
		}
	}
	if option == nil || len(option.Facts) == 0 {
		t.Fatalf("missing exact option: %+v", preview.Options)
	}
	input := ServiceObjectAssociationConfirmInput{Confirmed: true, ExpectedObjectVersion: 1, ExpectedAssociationVersion: 0, Source: source, ObjectRef: option.ObjectRef, FactsSHA256: option.FactsSHA256, CandidatesSHA256: preview.CandidatesSHA256}
	caseItem, ok := st.caseByID(caseID)
	if !ok {
		t.Fatal("missing case")
	}
	requestHash, err := serviceObjectRequestSHA256(accounts.Tenant(actor), caseItem.OwnerOrgID, caseID, actor.ID, "confirm_association", struct {
		ServiceObjectID string                               `json:"service_object_id"`
		Input           ServiceObjectAssociationConfirmInput `json:"input"`
	}{object.ServiceObjectID, input})
	if err != nil {
		t.Fatal(err)
	}
	factsBytes, factsHash, err := canonicalServiceObjectFacts(option.Facts)
	if err != nil || factsHash != option.FactsSHA256 {
		t.Fatalf("facts digest %v", err)
	}
	before := operatorAtomicRows(t, core, actor, caseID)
	execBefore := acceptanceAtomicSnapshot(t, execution, actor, caseID)
	frozenBefore := operatorFrozenRows(t, execution, actor, caseID, accepted.RunID)
	if frozenBefore["input"] == "[]" || frozenBefore["selections"] == "[]" {
		t.Fatal("missing frozen durable source")
	}
	q := operatorSQLLiteral
	ddl := fmt.Sprintf(`CREATE FUNCTION c309_confirm_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.case_id<>%s OR NEW.operation<>'confirm_association' OR NEW.idempotency_key<>'c309-confirm-fail' THEN RETURN NEW; END IF;
 IF NEW.tenant_id<>%s OR NEW.org_id<>%s OR NEW.actor_id<>%s OR NEW.request_sha256<>%s OR NEW.response_status<>201 OR NEW.entity_id<>%s OR convert_from(NEW.response_body,'UTF8')::jsonb<>jsonb_build_object('service_object_id',%s,'version',2,'association_version',1)
 OR NOT EXISTS (SELECT 1 FROM service_objects h JOIN service_object_versions v ON v.tenant_id=h.tenant_id AND v.org_id=h.org_id AND v.case_id=h.case_id AND v.service_object_id=h.service_object_id AND v.version=2 JOIN service_object_contract_associations a ON a.tenant_id=h.tenant_id AND a.org_id=h.org_id AND a.case_id=h.case_id AND a.service_object_id=h.service_object_id AND a.association_version=1 WHERE h.tenant_id=NEW.tenant_id AND h.org_id=NEW.org_id AND h.case_id=NEW.case_id AND h.service_object_id=NEW.entity_id AND h.current_version=2 AND h.current_association_version=1 AND v.state='confirmed' AND a.object_version=2 AND a.source_run_id=%s AND a.source_attempt_id=%s AND a.material_id=%s AND a.material_version=%d AND a.document_id=%s AND a.parsed_document_id=%s AND a.object_ref=%s AND a.facts_sha256=%s AND a.facts_canonical_json=convert_to(%s,'UTF8'))
 OR NOT EXISTS (SELECT 1 FROM review_runs r JOIN review_attempts a ON a.run_id=r.id JOIN review_attempt_inputs i ON i.run_id=r.id AND i.attempt_id=a.id JOIN review_attempt_parse_selections p ON p.run_id=r.id AND p.attempt_id=a.id JOIN review_run_snapshots s ON s.run_id=r.id WHERE r.case_id=NEW.case_id AND r.id=%s AND a.id=%s AND a.state IN ('completed','partial') AND a.finished_at IS NOT NULL AND a.history_kind='frozen' AND a.input_hash<>'' AND a.input_hash=i.input_hash AND a.output_hash<>'' AND EXISTS (SELECT 1 FROM jsonb_array_elements(i.documents::jsonb) d WHERE d->'document'->>'id'=%s AND d->'document'->>'digest'=%s AND d->>'parsed_document_id'=%s AND d->'material'->>'material_id'=%s AND (d->'material'->>'version')::int=%d) AND p.document_id=%s AND p.parsed_document_id=%s AND p.original_digest=%s AND a.rule_pack_compiler_ref<>'' AND a.rule_pack_hash<>'' AND a.rule_pack_snapshot_digest<>'' AND a.rule_pack_snapshot IS NOT NULL AND a.rule_pack_compiler_ref=s.rule_pack_compiler_ref AND a.rule_pack_hash=s.rule_pack_hash AND a.rule_pack_snapshot_digest=s.rule_pack_digest AND a.rule_pack_snapshot=s.rule_pack_snapshot AND a.rule_pack_id=s.rule_pack_id AND a.rule_pack_version=s.rule_pack_version)
 THEN RAISE EXCEPTION 'C309_CONFIRM_PRECONDITION'; END IF;
 RAISE EXCEPTION 'C309_CONFIRM_FORCED'; END $$; CREATE CONSTRAINT TRIGGER c309_confirm_deferred AFTER INSERT ON service_object_mutation_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION c309_confirm_fail()`, q(caseID), q(accounts.Tenant(actor)), q(caseItem.OwnerOrgID), q(actor.ID), q(requestHash), q(object.ServiceObjectID), q(object.ServiceObjectID), q(source.RunID), q(source.AttemptID), q(source.MaterialID), source.MaterialVersion, q(source.DocumentID), q(source.ParsedDocumentID), q(option.ObjectRef), q(option.FactsSHA256), q(string(factsBytes)), q(source.RunID), q(source.AttemptID), q(source.DocumentID), q(frozen.Document.Digest), q(source.ParsedDocumentID), q(source.MaterialID), source.MaterialVersion, q(source.DocumentID), q(source.ParsedDocumentID), q(frozen.Document.Digest))
	drop := `DROP TRIGGER IF EXISTS c309_confirm_deferred ON service_object_mutation_receipts; DROP FUNCTION IF EXISTS c309_confirm_fail()`
	operatorTrigger(t, env, ddl, drop)
	if _, _, err := core.ConfirmServiceObjectAssociation(ctx, actor, caseID, object.ServiceObjectID, "c309-confirm-fail", input); err == nil || !strings.Contains(err.Error(), "C309_CONFIRM_FORCED") {
		t.Fatalf("expected C309_CONFIRM_FORCED: %v", err)
	}
	if after := operatorAtomicRows(t, core, actor, caseID); !reflect.DeepEqual(before, after) {
		t.Fatalf("operator partial commit: %v %v", before, after)
	}
	if after := acceptanceAtomicSnapshot(t, execution, actor, caseID); !reflect.DeepEqual(execBefore, after) {
		t.Fatalf("execution changed: %v %v", execBefore, after)
	}
	if after := operatorFrozenRows(t, execution, actor, caseID, accepted.RunID); !reflect.DeepEqual(frozenBefore, after) {
		t.Fatalf("frozen changed: %v %v", frozenBefore, after)
	}
	if _, err := env.harness.pool.Exec(ctx, drop); err != nil {
		t.Fatal(err)
	}
	result, replayed, err := core.ConfirmServiceObjectAssociation(ctx, actor, caseID, object.ServiceObjectID, "c309-confirm-fail", input)
	if err != nil || replayed || result.Version != 2 || result.AssociationVersion != 1 {
		t.Fatalf("confirm=%+v replay=%v err=%v", result, replayed, err)
	}
	again, replayed, err := core.ConfirmServiceObjectAssociation(ctx, actor, caseID, object.ServiceObjectID, "c309-confirm-fail", input)
	if err != nil || !replayed || again != result {
		t.Fatalf("replay=%+v replay=%v err=%v", again, replayed, err)
	}
}
