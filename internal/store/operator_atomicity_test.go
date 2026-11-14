package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/auditext"
	operatorApp "contractaudit/internal/operator/app"
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
	tables := []string{"service_objects", "service_object_versions", "service_object_contract_associations", "service_object_mutation_receipts", "operator_mapping_lineages", "operator_object_bindings", "operator_external_collection_claims", "operator_external_snapshot_sets", "operator_external_observations"}
	out := make(map[string]string, len(tables))
	for _, table := range tables {
		var raw string
		query := `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM ` + table + ` t WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3`
		if err := tx.QueryRow(ctx, query, accounts.Tenant(actor), actor.OrgID, caseID).Scan(&raw); err != nil {
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

func operatorQualifiedTestIdent(t *testing.T, parts ...string) string {
	t.Helper()
	quoted := make([]string, len(parts))
	for i, part := range parts {
		value, err := quoteIdent(part)
		if err != nil {
			t.Fatal(err)
		}
		quoted[i] = value
	}
	return strings.Join(quoted, ".")
}

func operatorTrigger(t *testing.T, env *durableRoleEnvironment, ddl, drop string) {
	t.Helper()
	if _, err := env.harness.pool.Exec(context.Background(), ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = env.harness.pool.Exec(context.Background(), drop) })
}

type operatorFrozenConfirmData struct {
	CaseID   string
	Accepted JobAcceptance
	Frozen   FrozenDocument
	Object   ServiceObjectMutationResult
	Source   ServiceObjectAssociationPreviewInput
	Option   ServiceObjectAssociationOption
	Input    ServiceObjectAssociationConfirmInput
}

// operatorFrozenConfirmFixture is the minimal authenticated #309 frozen source
// shared by confirmation rollback and collection atomicity coverage.
func operatorFrozenConfirmFixture(t *testing.T, core, docs, execution *Store, ctx context.Context, actor accounts.User) operatorFrozenConfirmData {
	t.Helper()
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
	return operatorFrozenConfirmData{CaseID: caseID, Accepted: accepted, Frozen: frozen, Object: object, Source: source, Option: *option, Input: input}
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

type operatorAtomicCollectionClaim struct {
	TenantID            string
	OrgID               string
	CaseID              string
	ActorID             string
	IdempotencyKey      string
	CollectionID        string
	RequestSHA256       string
	Generation          int64
	LeaseOwner          *string
	LeaseUntil          *time.Time
	Status              string
	ResultSnapshotSetID *string
	ResponseStatus      *int32
	ResponseBody        []byte
	CreatedAt           time.Time
	UpdatedAt           time.Time
	LeaseValid          bool
}

func readOperatorAtomicCollectionClaim(t *testing.T, core *Store, actor accounts.User, caseID, key string) operatorAtomicCollectionClaim {
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
	var claim operatorAtomicCollectionClaim
	err = tx.QueryRow(ctx, `SELECT tenant_id,org_id,case_id,actor_id,idempotency_key,collection_id,request_sha256,generation,
		lease_owner,lease_until,status,result_snapshot_set_id,response_status,response_body,created_at,updated_at,
		COALESCE(lease_until > clock_timestamp(),false)
		FROM operator_external_collection_claims
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`,
		accounts.Tenant(actor), actor.OrgID, caseID, actor.ID, key).Scan(
		&claim.TenantID, &claim.OrgID, &claim.CaseID, &claim.ActorID, &claim.IdempotencyKey, &claim.CollectionID, &claim.RequestSHA256, &claim.Generation,
		&claim.LeaseOwner, &claim.LeaseUntil, &claim.Status, &claim.ResultSnapshotSetID, &claim.ResponseStatus, &claim.ResponseBody, &claim.CreatedAt, &claim.UpdatedAt, &claim.LeaseValid)
	if err != nil {
		t.Fatalf("read collection claim %q: %v", key, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return claim
}

func operatorAtomicCollectionCounts(t *testing.T, core *Store, actor accounts.User, caseID, key string) map[string]int64 {
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
		"claims":        `SELECT count(*) FROM operator_external_collection_claims WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`,
		"snapshot_sets": `SELECT count(*) FROM operator_external_snapshot_sets WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND actor_id=$4 AND idempotency_key=$5`,
		"observations":  `SELECT count(*) FROM operator_external_observations o JOIN operator_external_snapshot_sets s ON s.tenant_id=o.tenant_id AND s.org_id=o.org_id AND s.case_id=o.case_id AND s.snapshot_set_id=o.snapshot_set_id WHERE s.tenant_id=$1 AND s.org_id=$2 AND s.case_id=$3 AND s.actor_id=$4 AND s.idempotency_key=$5`,
	}
	out := make(map[string]int64, len(queries))
	for name, query := range queries {
		var count int64
		if err := tx.QueryRow(ctx, query, accounts.Tenant(actor), actor.OrgID, caseID, actor.ID, key).Scan(&count); err != nil {
			t.Fatalf("count collection %s for %q: %v", name, key, err)
		}
		out[name] = count
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return out
}

func operatorSequenceMarker(t *testing.T, harness *Store, qualifiedSequence string) (int64, bool) {
	t.Helper()
	var value int64
	var called bool
	if err := harness.pool.QueryRow(context.Background(), "SELECT last_value,is_called FROM "+qualifiedSequence).Scan(&value, &called); err != nil {
		t.Fatal(err)
	}
	return value, called
}

func operatorTimestampLiteral(value time.Time) string {
	return operatorSQLLiteral(value.UTC().Format(time.RFC3339Nano)) + "::timestamptz"
}

func operatorCollectionZeroRowFaultDDL(t *testing.T, schema string, claim operatorAtomicCollectionClaim, businessAt time.Time, binding OperatorObjectBinding, observation auditext.Observation, observationJSON, fieldsJSON []byte, observationSHA, setSHA string) (string, string, string, string) {
	t.Helper()
	const sequenceName = "c310_f_claim_marker_seq"
	const functionName = "c310_f_claim_zero_row_guard"
	const triggerName = "c310_f_claim_zero_row_guard"
	claimsTable := operatorQualifiedTestIdent(t, schema, "operator_external_collection_claims")
	snapshotsTable := operatorQualifiedTestIdent(t, schema, "operator_external_snapshot_sets")
	observationsTable := operatorQualifiedTestIdent(t, schema, "operator_external_observations")
	sequence := operatorQualifiedTestIdent(t, schema, sequenceName)
	function := operatorQualifiedTestIdent(t, schema, functionName)
	trigger := operatorQualifiedTestIdent(t, triggerName)
	sequenceRegclass := operatorSQLLiteral(schema+"."+sequenceName) + "::regclass"
	owner := *claim.LeaseOwner
	leaseUntil := operatorTimestampLiteral(*claim.LeaseUntil)
	createdAt := operatorTimestampLiteral(claim.CreatedAt)
	updatedAt := operatorTimestampLiteral(claim.UpdatedAt)
	businessAtSQL := operatorTimestampLiteral(businessAt)
	observedAtSQL := operatorTimestampLiteral(observation.ObservedAt.UTC().Truncate(time.Microsecond))
	sourceUpdatedAtSQL := operatorTimestampLiteral(observation.SourceUpdatedAt.UTC().Truncate(time.Microsecond))
	effectiveFromSQL := operatorTimestampLiteral(observation.EffectiveFrom.UTC().Truncate(time.Microsecond))
	effectiveUntilSQL := operatorTimestampLiteral(observation.EffectiveUntil.UTC().Truncate(time.Microsecond))
	ddl := fmt.Sprintf(`CREATE SEQUENCE %[1]s START WITH 1;
CREATE FUNCTION %[2]s() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE response_json jsonb;
BEGIN
 IF NEW.tenant_id IS DISTINCT FROM %[3]s OR NEW.org_id IS DISTINCT FROM %[4]s OR NEW.case_id IS DISTINCT FROM %[5]s OR NEW.actor_id IS DISTINCT FROM %[6]s OR NEW.idempotency_key IS DISTINCT FROM %[7]s THEN RETURN NEW; END IF;
 IF NEW.status IS DISTINCT FROM 'completed' THEN RETURN NEW; END IF;
 response_json := convert_from(NEW.response_body, 'UTF8')::jsonb;
 IF OLD.tenant_id IS DISTINCT FROM %[3]s OR OLD.org_id IS DISTINCT FROM %[4]s OR OLD.case_id IS DISTINCT FROM %[5]s OR OLD.actor_id IS DISTINCT FROM %[6]s OR OLD.idempotency_key IS DISTINCT FROM %[7]s
    OR OLD.collection_id IS DISTINCT FROM %[8]s OR OLD.request_sha256 IS DISTINCT FROM %[9]s OR OLD.generation IS DISTINCT FROM %[10]d
    OR OLD.lease_owner IS DISTINCT FROM %[11]s OR OLD.lease_until IS DISTINCT FROM %[12]s OR OLD.lease_until IS NULL OR OLD.lease_until <= clock_timestamp()
    OR OLD.status IS DISTINCT FROM 'in_progress' OR OLD.result_snapshot_set_id IS NOT NULL OR OLD.response_status IS NOT NULL OR OLD.response_body IS NOT NULL
    OR OLD.created_at IS DISTINCT FROM %[13]s OR OLD.updated_at IS DISTINCT FROM %[14]s
    OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.org_id IS DISTINCT FROM OLD.org_id OR NEW.case_id IS DISTINCT FROM OLD.case_id
    OR NEW.actor_id IS DISTINCT FROM OLD.actor_id OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
    OR NEW.collection_id IS DISTINCT FROM OLD.collection_id OR NEW.request_sha256 IS DISTINCT FROM OLD.request_sha256 OR NEW.generation IS DISTINCT FROM OLD.generation
    OR NEW.status IS DISTINCT FROM 'completed' OR NEW.lease_owner IS NOT NULL OR NEW.lease_until IS NOT NULL
    OR NEW.result_snapshot_set_id IS NULL OR NEW.result_snapshot_set_id = '' OR NEW.response_status IS DISTINCT FROM 201
    OR NEW.response_body IS NULL OR octet_length(NEW.response_body) = 0 OR NEW.created_at IS DISTINCT FROM OLD.created_at
    OR jsonb_typeof(response_json) IS DISTINCT FROM 'object'
    OR (CASE WHEN jsonb_typeof(response_json) = 'object' THEN (SELECT count(*) FROM jsonb_object_keys(response_json)) ELSE -1 END) IS DISTINCT FROM 12
    OR response_json->>'case_id' IS DISTINCT FROM %[5]s
    OR response_json->>'snapshot_set_id' IS DISTINCT FROM NEW.result_snapshot_set_id
    OR response_json->>'binding_version' IS DISTINCT FROM %[15]s
    OR response_json->>'connector_id' IS DISTINCT FROM %[16]s
    OR response_json->>'external_object_id' IS DISTINCT FROM %[17]s
    OR response_json->>'source_system' IS DISTINCT FROM %[18]s
    OR (response_json->>'business_at')::timestamptz IS DISTINCT FROM %[19]s
    OR response_json->>'status' IS DISTINCT FROM %[20]s
    OR response_json->>'reason_code' IS DISTINCT FROM %[21]s
    OR response_json->>'collected_by' IS DISTINCT FROM %[6]s
    OR (CASE WHEN jsonb_typeof(response_json->'observations') = 'array' THEN jsonb_array_length(response_json->'observations') ELSE -1 END) IS DISTINCT FROM 1
    OR response_json ? 'collection_id' OR response_json ? 'request_sha256'
    OR NOT EXISTS (
      SELECT 1 FROM %[22]s s JOIN %[23]s o ON o.tenant_id=s.tenant_id AND o.org_id=s.org_id AND o.case_id=s.case_id AND o.snapshot_set_id=s.snapshot_set_id
      WHERE s.tenant_id=OLD.tenant_id AND s.org_id=OLD.org_id AND s.case_id=OLD.case_id AND s.snapshot_set_id=NEW.result_snapshot_set_id
        AND s.binding_version=%[15]s AND s.connector_id=%[16]s AND s.external_object_id=%[17]s AND s.source_system=%[18]s
        AND s.business_at=%[19]s AND s.status=%[20]s AND s.reason_code=%[21]s AND s.collected_by=OLD.actor_id
        AND s.observation_count=1 AND s.set_sha256=%[24]s AND s.actor_id=OLD.actor_id AND s.idempotency_key=OLD.idempotency_key
        AND s.request_sha256=OLD.request_sha256 AND s.created_at=s.collected_at AND NEW.updated_at=s.collected_at
        AND (response_json->>'business_at')::timestamptz IS NOT DISTINCT FROM s.business_at
        AND (response_json->>'collected_at')::timestamptz IS NOT DISTINCT FROM s.collected_at
        AND response_json->>'case_id' IS NOT DISTINCT FROM s.case_id
        AND response_json->>'snapshot_set_id' IS NOT DISTINCT FROM s.snapshot_set_id
        AND response_json->>'binding_version' IS NOT DISTINCT FROM s.binding_version
        AND response_json->>'connector_id' IS NOT DISTINCT FROM s.connector_id
        AND response_json->>'external_object_id' IS NOT DISTINCT FROM s.external_object_id
        AND response_json->>'source_system' IS NOT DISTINCT FROM s.source_system
        AND response_json->>'status' IS NOT DISTINCT FROM s.status
        AND response_json->>'reason_code' IS NOT DISTINCT FROM s.reason_code
        AND response_json->>'collected_by' IS NOT DISTINCT FROM s.collected_by
        AND response_json->'observations'->0 IS NOT DISTINCT FROM o.observation_json
        AND (SELECT count(*) FROM %[23]s o2 WHERE o2.tenant_id=s.tenant_id AND o2.org_id=s.org_id AND o2.case_id=s.case_id AND o2.snapshot_set_id=s.snapshot_set_id)=1
        AND o.snapshot_id=%[25]s AND o.record_id=%[26]s AND o.record_version IS NOT DISTINCT FROM %[27]s
        AND o.observed_at=%[28]s AND o.source_updated_at IS NOT DISTINCT FROM %[29]s
        AND o.effective_from=%[30]s AND o.effective_until=%[31]s
        AND o.availability=%[32]s AND o.reason_code=%[33]s AND o.payload_ref IS NOT DISTINCT FROM %[34]s
        AND o.fields IS NOT DISTINCT FROM %[35]s::jsonb AND o.data_class=%[36]s
        AND o.observation_json IS NOT DISTINCT FROM %[37]s::jsonb AND o.observation_sha256=%[38]s
        AND o.created_at=s.collected_at
    )
 THEN RAISE EXCEPTION 'C310_F_PRECONDITION'; END IF;
 PERFORM nextval(%[39]s);
 RETURN NULL;
END $$;
CREATE TRIGGER %[40]s BEFORE UPDATE ON %[41]s FOR EACH ROW EXECUTE FUNCTION %[2]s()`,
		sequence, function,
		operatorSQLLiteral(claim.TenantID), operatorSQLLiteral(claim.OrgID), operatorSQLLiteral(claim.CaseID), operatorSQLLiteral(claim.ActorID), operatorSQLLiteral(claim.IdempotencyKey),
		operatorSQLLiteral(claim.CollectionID), operatorSQLLiteral(claim.RequestSHA256), claim.Generation, operatorSQLLiteral(owner), leaseUntil, createdAt, updatedAt,
		operatorSQLLiteral(binding.Version), operatorSQLLiteral(binding.ExternalObject.ConnectorID), operatorSQLLiteral(binding.ExternalObject.ExternalObjectID), operatorSQLLiteral(observation.SourceSystem),
		businessAtSQL, operatorSQLLiteral(string(observation.Availability)), operatorSQLLiteral(observation.ReasonCode), snapshotsTable, observationsTable, operatorSQLLiteral(setSHA),
		operatorSQLLiteral(observation.SnapshotID), operatorSQLLiteral(observation.RecordID), operatorSQLLiteral(*observation.RecordVersion), observedAtSQL, sourceUpdatedAtSQL,
		effectiveFromSQL, effectiveUntilSQL, operatorSQLLiteral(string(observation.Availability)), operatorSQLLiteral(observation.ReasonCode), operatorSQLLiteral(*observation.PayloadRef),
		operatorSQLLiteral(string(fieldsJSON)), operatorSQLLiteral(observation.DataClass), operatorSQLLiteral(string(observationJSON)), operatorSQLLiteral(observationSHA), sequenceRegclass, trigger, claimsTable)
	drop := fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s; DROP FUNCTION IF EXISTS %s(); DROP SEQUENCE IF EXISTS %s", trigger, claimsTable, function, sequence)
	return ddl, drop, sequenceName, sequence
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
	fixture := operatorFrozenConfirmFixture(t, core, docs, execution, ctx, actor)
	caseID, accepted, frozen, object := fixture.CaseID, fixture.Accepted, fixture.Frozen, fixture.Object
	source, option, input := fixture.Source, &fixture.Option, fixture.Input
	st, err := core.snapshotScoped(ctx, userScope(actor))
	if err != nil {
		t.Fatal(err)
	}
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

func TestOperatorCollectionClaimZeroRowRollbackAndRecovery(t *testing.T) {
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
	fixture := operatorFrozenConfirmFixture(t, core, docs, execution, ctx, actor)
	caseID := fixture.CaseID
	businessAt := time.Date(2026, 2, 4, 10, 11, 12, 987654321, time.FixedZone("C310+08", 8*60*60)).UTC().Truncate(time.Microsecond)
	confirmed, replayed, err := core.ConfirmServiceObjectAssociation(ctx, actor, caseID, fixture.Object.ServiceObjectID, "c310-f-confirm-association", fixture.Input)
	if err != nil || replayed || confirmed.Version != 2 || confirmed.AssociationVersion != 1 {
		t.Fatalf("confirm frozen association=%+v replay=%v err=%v", confirmed, replayed, err)
	}
	bindingInput := OperatorObjectBindingInput{
		ContractObject:  OperatorContractObject{ObjectType: "service", ObjectID: fixture.Option.ObjectRef, Label: strPtr("专线 LINE-OBJ-001")},
		ExternalObject:  OperatorExternalObject{ConnectorID: "fixture.oss", ExternalObjectID: "fixture-line-c310"},
		ServiceObjectID: fixture.Object.ServiceObjectID, ExpectedObjectVersion: confirmed.Version, ExpectedAssociationVersion: confirmed.AssociationVersion,
		EffectiveFrom: businessAt.Add(-24 * time.Hour), EffectiveUntil: businessAt.Add(24 * time.Hour), Confirmed: true,
	}
	binding, err := core.CreateOperatorObjectBinding(ctx, actor, caseID, bindingInput, "c310-f-binding")
	if err != nil || binding.Status != "confirmed" || binding.Version == "" || binding.ObjectVersion != confirmed.Version || binding.AssociationVersion != confirmed.AssociationVersion {
		t.Fatalf("create confirmed collection binding=%+v err=%v", binding, err)
	}
	if businessAt.Before(binding.EffectiveFrom) || !businessAt.Before(binding.EffectiveUntil) {
		t.Fatalf("businessAt %s is outside binding interval [%s,%s)", businessAt, binding.EffectiveFrom, binding.EffectiveUntil)
	}

	recordVersion := "c310-record-v1"
	payloadRef := "fixture://oss/snapshot-c310-target"
	observedAt := businessAt.Add(time.Minute + 123*time.Nanosecond)
	sourceUpdatedAt := businessAt.Add(45*time.Second + 456*time.Nanosecond)
	effectiveFrom := businessAt.Add(-24 * time.Hour)
	effectiveUntil := businessAt.Add(24 * time.Hour)
	observation := auditext.Observation{
		SchemaVersion: auditext.ExternalObservationSchema, SnapshotID: "snapshot-c310-target",
		TenantID: accounts.Tenant(actor), CaseID: caseID, ConnectorID: binding.ExternalObject.ConnectorID, SourceSystem: "fixture-oss-east",
		ObjectBindingVersion: binding.Version, ExternalObjectID: binding.ExternalObject.ExternalObjectID, RecordID: "record-c310-target",
		RecordVersion: &recordVersion, ObservedAt: observedAt, SourceUpdatedAt: &sourceUpdatedAt,
		EffectiveFrom: effectiveFrom, EffectiveUntil: effectiveUntil, Availability: auditext.AvailabilityAvailable,
		ReasonCode: "read_succeeded", PayloadRef: &payloadRef, DataClass: auditext.DataClassSynthetic,
		TemporalProof: &auditext.TemporalProof{Kind: auditext.TemporalProofHistoricalInterval, SourceCatalogID: "fixture-c310-catalog", DatasetVersion: "fixture-c310-v1", RecordVersion: recordVersion,
			SourceAsOf: &observedAt, ValidFrom: &effectiveFrom, ValidUntil: &effectiveUntil},
		Fields: []auditext.Field{{Name: "configured_bandwidth", Value: "80", Unit: "Mbit/s", SourcePointer: "/serviceCharacteristic/0/value", SemanticKind: "configured_value"}},
	}
	controlObservation := observation
	controlObservation.SnapshotID = "snapshot-c310-control"
	controlObservation.RecordID = "record-c310-control"
	controlPayload := "fixture://oss/snapshot-c310-control"
	controlObservation.PayloadRef = &controlPayload
	controlSet, err := core.CollectOperatorExternalSnapshot(ctx, actor, caseID, binding.Version, businessAt, binding.ExternalObject.ConnectorID,
		binding.ExternalObject.ExternalObjectID, operatorFixedConnector{observation: controlObservation}, "c310-f-control")
	if err != nil || controlSet.SnapshotSetID == "" || len(controlSet.Observations) != 1 {
		t.Fatalf("different-key control collection=%+v err=%v", controlSet, err)
	}

	const targetKey = "c310-f-zero-row"
	connector := &operatorBlockingConnector{observation: observation, started: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	releaseConnector := func() { releaseOnce.Do(func() { close(connector.release) }) }
	t.Cleanup(releaseConnector)
	type collectResult struct {
		set OperatorSnapshotSet
		err error
	}
	collectionDone := make(chan collectResult, 1)
	go func() {
		set, err := core.CollectOperatorExternalSnapshot(ctx, actor, caseID, binding.Version, businessAt, binding.ExternalObject.ConnectorID,
			binding.ExternalObject.ExternalObjectID, connector, targetKey)
		collectionDone <- collectResult{set: set, err: err}
	}()
	select {
	case <-connector.started:
	case <-time.After(3 * time.Second):
		releaseConnector()
		t.Fatal("target collection did not reach the blocking connector")
	}

	beforeRows := operatorAtomicRows(t, core, actor, caseID)
	beforeClaim := readOperatorAtomicCollectionClaim(t, core, actor, caseID, targetKey)
	beforeCounts := operatorAtomicCollectionCounts(t, core, actor, caseID, targetKey)
	if beforeCounts["claims"] != 1 || beforeCounts["snapshot_sets"] != 0 || beforeCounts["observations"] != 0 ||
		beforeClaim.Status != "in_progress" || beforeClaim.Generation != 1 || beforeClaim.CollectionID == "" || beforeClaim.RequestSHA256 == "" ||
		beforeClaim.LeaseOwner == nil || *beforeClaim.LeaseOwner == "" || beforeClaim.LeaseUntil == nil || !beforeClaim.LeaseValid ||
		beforeClaim.ResultSnapshotSetID != nil || beforeClaim.ResponseStatus != nil || beforeClaim.ResponseBody != nil {
		releaseConnector()
		t.Fatalf("transaction-A baseline is incomplete: claim=%+v counts=%v", beforeClaim, beforeCounts)
	}
	expectedRequestSHA, err := operatorApp.CollectionDigest(accounts.Tenant(actor), actor.OrgID, caseID, actor.ID, binding, businessAt)
	if err != nil || beforeClaim.RequestSHA256 != expectedRequestSHA {
		releaseConnector()
		t.Fatalf("request digest mismatch: stored=%s expected=%s err=%v", beforeClaim.RequestSHA256, expectedRequestSHA, err)
	}
	if controlCounts := operatorAtomicCollectionCounts(t, core, actor, caseID, "c310-f-control"); controlCounts["claims"] != 1 || controlCounts["snapshot_sets"] != 1 || controlCounts["observations"] != 1 {
		releaseConnector()
		t.Fatalf("successful different-key control rows are incomplete: %v", controlCounts)
	}

	var coreLogin string
	if err := core.pool.QueryRow(ctx, "SELECT current_user").Scan(&coreLogin); err != nil || coreLogin == "" {
		releaseConnector()
		t.Fatalf("read actual Core login: %v", err)
	}
	var coreSuperuser, coreBypassRLS bool
	if err := env.harness.pool.QueryRow(ctx, `SELECT rolsuper,rolbypassrls FROM pg_roles WHERE rolname=$1`, coreLogin).Scan(&coreSuperuser, &coreBypassRLS); err != nil || coreSuperuser || coreBypassRLS {
		releaseConnector()
		t.Fatalf("Core test login must not be superuser or bypass RLS: role=%q superuser=%v bypassrls=%v err=%v", coreLogin, coreSuperuser, coreBypassRLS, err)
	}

	observationJSON, err := json.Marshal(observation)
	if err != nil {
		releaseConnector()
		t.Fatal(err)
	}
	fieldsJSON, err := json.Marshal(observation.Fields)
	if err != nil {
		releaseConnector()
		t.Fatal(err)
	}
	observationDigest := sha256.Sum256(observationJSON)
	observationSHA := hex.EncodeToString(observationDigest[:])
	setDigestInput := append(append([]byte(nil), observationJSON...), []byte(beforeClaim.RequestSHA256)...)
	setDigest := sha256.Sum256(setDigestInput)
	setSHA := hex.EncodeToString(setDigest[:])
	ddl, drop, markerName, markerQualified := operatorCollectionZeroRowFaultDDL(t, env.schema, beforeClaim, businessAt, binding, observation,
		observationJSON, fieldsJSON, observationSHA, setSHA)
	operatorTrigger(t, env, ddl, drop)
	quotedCoreLogin := operatorQualifiedTestIdent(t, coreLogin)
	if _, err := env.harness.pool.Exec(ctx, "GRANT USAGE ON SEQUENCE "+markerQualified+" TO "+quotedCoreLogin); err != nil {
		releaseConnector()
		t.Fatalf("grant only Core usage on marker sequence: %v", err)
	}
	functionQualified := operatorQualifiedTestIdent(t, env.schema, "c310_f_claim_zero_row_guard")
	if _, err := env.harness.pool.Exec(ctx, "GRANT EXECUTE ON FUNCTION "+functionQualified+"() TO "+quotedCoreLogin); err != nil {
		releaseConnector()
		t.Fatalf("grant Core execution on invoker trigger function: %v", err)
	}
	var initializedMarker int64
	if err := env.harness.pool.QueryRow(ctx, "SELECT nextval($1::regclass)", env.schema+"."+markerName).Scan(&initializedMarker); err != nil || initializedMarker != 1 {
		releaseConnector()
		t.Fatalf("initialize nontransactional marker: value=%d err=%v", initializedMarker, err)
	}
	markerBefore, calledBefore := operatorSequenceMarker(t, env.harness, markerQualified)
	if markerBefore != 1 || !calledBefore {
		releaseConnector()
		t.Fatalf("unexpected initialized marker baseline: value=%d is_called=%v", markerBefore, calledBefore)
	}

	releaseConnector()
	var failed collectResult
	select {
	case failed = <-collectionDone:
	case <-time.After(5 * time.Second):
		t.Fatal("target collection did not finish after connector release")
	}
	if !isConflictFault(failed.err) || !strings.Contains(failed.err.Error(), "采集租约已被更新的请求接管") || strings.Contains(failed.err.Error(), "C310_F_PRECONDITION") {
		t.Fatalf("expected original collection CAS conflict after a matching zero-row UPDATE: result=%+v err=%v", failed.set, failed.err)
	}
	markerAfter, calledAfter := operatorSequenceMarker(t, env.harness, markerQualified)
	if !calledAfter || markerAfter != markerBefore+1 {
		t.Fatalf("completed-claim trigger did not reach RETURN NULL exactly once: before=%d after=%d called=%v", markerBefore, markerAfter, calledAfter)
	}
	if afterRows := operatorAtomicRows(t, core, actor, caseID); !reflect.DeepEqual(beforeRows, afterRows) {
		t.Fatalf("transaction-B zero-row rollback changed one or more complete Core rows: before=%v after=%v", beforeRows, afterRows)
	}
	afterClaim := readOperatorAtomicCollectionClaim(t, core, actor, caseID, targetKey)
	if !reflect.DeepEqual(beforeClaim, afterClaim) || !afterClaim.LeaseValid {
		t.Fatalf("zero-row rollback did not preserve transaction-A claim and live lease: before=%+v after=%+v", beforeClaim, afterClaim)
	}
	if afterCounts := operatorAtomicCollectionCounts(t, core, actor, caseID, targetKey); !reflect.DeepEqual(beforeCounts, afterCounts) {
		t.Fatalf("zero-row rollback changed target claim/set/observation counts: before=%v after=%v", beforeCounts, afterCounts)
	}
	if controlCounts := operatorAtomicCollectionCounts(t, core, actor, caseID, "c310-f-control"); controlCounts["claims"] != 1 || controlCounts["snapshot_sets"] != 1 || controlCounts["observations"] != 1 {
		t.Fatalf("successful different-key control history changed: %v", controlCounts)
	}

	if _, err := env.harness.pool.Exec(ctx, drop); err != nil {
		t.Fatal(err)
	}
	if err := core.releaseOperatorCollectionLease(ctx, actor, accounts.Tenant(actor), actor.OrgID, caseID, actor.ID, targetKey, beforeClaim.Generation, *beforeClaim.LeaseOwner); err != nil {
		t.Fatalf("explicitly release preserved transaction-A generation for recovery: %v", err)
	}
	releasedClaim := readOperatorAtomicCollectionClaim(t, core, actor, caseID, targetKey)
	if releasedClaim.Status != "retryable" || releasedClaim.Generation != beforeClaim.Generation || releasedClaim.CollectionID != beforeClaim.CollectionID ||
		releasedClaim.RequestSHA256 != beforeClaim.RequestSHA256 || releasedClaim.LeaseOwner != nil || releasedClaim.LeaseUntil != nil ||
		releasedClaim.ResultSnapshotSetID != nil || releasedClaim.ResponseStatus != nil || releasedClaim.ResponseBody != nil {
		t.Fatalf("test recovery did not release precisely the original claim token: %+v", releasedClaim)
	}

	retriedSet, err := core.CollectOperatorExternalSnapshot(ctx, actor, caseID, binding.Version, businessAt, binding.ExternalObject.ConnectorID,
		binding.ExternalObject.ExternalObjectID, operatorFixedConnector{observation: observation}, targetKey)
	if err != nil || retriedSet.SnapshotSetID == "" || len(retriedSet.Observations) != 1 {
		t.Fatalf("same-key generation-two recovery failed: set=%+v err=%v", retriedSet, err)
	}
	completedClaim := readOperatorAtomicCollectionClaim(t, core, actor, caseID, targetKey)
	if completedClaim.Status != "completed" || completedClaim.Generation != 2 || completedClaim.CollectionID != beforeClaim.CollectionID ||
		completedClaim.RequestSHA256 != beforeClaim.RequestSHA256 || completedClaim.ResultSnapshotSetID == nil || *completedClaim.ResultSnapshotSetID != retriedSet.SnapshotSetID ||
		completedClaim.ResponseStatus == nil || *completedClaim.ResponseStatus != 201 || len(completedClaim.ResponseBody) == 0 {
		t.Fatalf("recovered result did not complete the same collection at generation two: %+v", completedClaim)
	}
	if counts := operatorAtomicCollectionCounts(t, core, actor, caseID, targetKey); counts["claims"] != 1 || counts["snapshot_sets"] != 1 || counts["observations"] != 1 {
		t.Fatalf("recovered generation must leave one claim, snapshot, and observation: %v", counts)
	}
	rowsAfterRecovery := operatorAtomicRows(t, core, actor, caseID)
	if got := connector.reads.Load(); got != 1 {
		t.Fatalf("failed generation should have made exactly one external read: reads=%d", got)
	}
	replayedSet, err := core.CollectOperatorExternalSnapshot(ctx, actor, caseID, binding.Version, businessAt, binding.ExternalObject.ConnectorID,
		binding.ExternalObject.ExternalObjectID, connector, targetKey)
	firstPublicJSON, firstJSONErr := json.Marshal(retriedSet)
	replayPublicJSON, replayJSONErr := json.Marshal(replayedSet)
	if err != nil || firstJSONErr != nil || replayJSONErr != nil || replayedSet.SnapshotSetID != retriedSet.SnapshotSetID ||
		replayedSet.CollectionID != retriedSet.CollectionID || !bytes.Equal(firstPublicJSON, replayPublicJSON) || connector.reads.Load() != 1 {
		t.Fatalf("completed same-key replay changed public bytes or invoked connector: first=%+v replay=%+v reads=%d err=%v", retriedSet, replayedSet, connector.reads.Load(), err)
	}
	if afterReplayRows := operatorAtomicRows(t, core, actor, caseID); !reflect.DeepEqual(rowsAfterRecovery, afterReplayRows) {
		t.Fatalf("completed same-key replay changed durable collection history: before=%v after=%v", rowsAfterRecovery, afterReplayRows)
	}
}
