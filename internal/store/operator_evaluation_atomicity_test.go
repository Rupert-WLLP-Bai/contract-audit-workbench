package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/auditext"
	"contractaudit/internal/capabilitypack"
	"contractaudit/internal/fault"
	"contractaudit/internal/fulfillment"
	operatorApp "contractaudit/internal/operator/app"
	"contractaudit/internal/reviewimpact"
	"contractaudit/internal/rulepack"
	"contractaudit/internal/telecom"
	"github.com/jackc/pgx/v5"
)

func operatorEvaluationRows(t *testing.T, core *Store, actor accounts.User, caseID string) map[string]string {
	t.Helper()
	ctx := context.Background()
	tx, err := core.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	scope := userScope(actor)
	scope.CaseID = caseID
	if err = setLocalScope(ctx, tx, scope); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, table := range []string{"operator_fulfillment_evaluations", "operator_fulfillment_evaluation_objects", "operator_fulfillment_checks", "operator_impact_plans"} {
		var raw string
		q := `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM ` + table + ` t WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3`
		if err = tx.QueryRow(ctx, q, accounts.Tenant(actor), actor.OrgID, caseID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		out[table] = raw
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOperatorAggregateObjectFailureRollsBack(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	core, docs, execution := env.openRole(t, "core"), env.openRole(t, "document"), env.openRole(t, "execution")
	actor, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("actor missing")
	}
	ctx, actor := operatorAtomicSession(t, core, docs, execution, actor)
	fixture := operatorFrozenConfirmFixture(t, core, docs, execution, ctx, actor)
	second, replayed, err := core.CreateServiceObject(ctx, actor, fixture.CaseID, ServiceObjectCreateInput{ObjectType: ServiceObjectDedicatedLine, DisplayName: "第二未确认对象"}, "c311-object-two")
	if err != nil || replayed {
		t.Fatalf("second=%+v replay=%v err=%v", second, replayed, err)
	}
	st, err := core.snapshotScoped(ctx, userScope(actor))
	if err != nil {
		t.Fatal(err)
	}
	attempt, ok := st.attemptByID(fixture.Accepted.AttemptID)
	if !ok || attempt.RulePackID == "" || attempt.RulePackVersion == "" {
		t.Fatalf("missing governed attempt: %+v", attempt)
	}
	manifest := operatorManifestForRulePack(t, attempt.RulePackID, attempt.RulePackVersion)
	ids := []string{fixture.Object.ServiceObjectID, second.ServiceObjectID}
	sort.Strings(ids)
	at := time.Now().UTC().Truncate(time.Microsecond)
	input := OperatorEvaluationInput{PackID: manifest.PackID, PackVersion: manifest.Version, SourceReviewID: fixture.Accepted.RunID, SourceAttemptID: fixture.Accepted.AttemptID, BusinessAt: at, EvidenceCutoffAt: &at, Scope: &OperatorEvaluationScope{Kind: "explicit_subset", ServiceObjectIDs: ids}}
	baseline, err := core.CreateOperatorFulfillmentEvaluation(ctx, actor, fixture.CaseID, input, manifest, fulfillment.New(), "c311-baseline")
	if err != nil || len(baseline.Objects) != 2 || baseline.Objects[0].ServiceObjectID == nil || *baseline.Objects[0].ServiceObjectID != ids[0] || baseline.Objects[1].ServiceObjectID == nil || *baseline.Objects[1].ServiceObjectID != ids[1] {
		t.Fatalf("baseline=%+v err=%v", baseline, err)
	}
	before := operatorEvaluationRows(t, core, actor, fixture.CaseID)
	frozen := operatorFrozenRows(t, execution, actor, fixture.CaseID, fixture.Accepted.RunID)
	if before["operator_fulfillment_evaluations"] == "[]" || before["operator_fulfillment_evaluation_objects"] == "[]" || frozen["input"] == "[]" {
		t.Fatal("missing baseline")
	}
	digest, err := operatorApp.EvaluationRequestSHA(input)
	if err != nil {
		t.Fatal(err)
	}
	q := operatorSQLLiteral
	emptyFacts := sha256.Sum256([]byte("[]"))
	factsSHA := hex.EncodeToString(emptyFacts[:])
	firstJSON, err := json.Marshal(baseline.Objects[0])
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(baseline.Objects[1])
	if err != nil {
		t.Fatal(err)
	}
	firstSHA, secondSHA := sha256.Sum256(firstJSON), sha256.Sum256(secondJSON)
	ddl := fmt.Sprintf(`CREATE FUNCTION c311_eval_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.ordinal=2 AND EXISTS (SELECT 1 FROM operator_fulfillment_evaluations e WHERE e.tenant_id=NEW.tenant_id AND e.org_id=NEW.org_id AND e.case_id=NEW.case_id AND e.evaluation_id=NEW.evaluation_id AND e.idempotency_key='c311-fail') THEN IF NOT EXISTS (SELECT 1 FROM operator_fulfillment_evaluations e WHERE e.tenant_id=NEW.tenant_id AND e.org_id=NEW.org_id AND e.case_id=NEW.case_id AND e.evaluation_id=NEW.evaluation_id AND e.actor_id=%s AND e.idempotency_key='c311-fail' AND e.request_sha256=%s AND e.source_run_id=%s AND e.source_attempt_id=%s AND e.pack_id=%s AND e.pack_version=%s AND e.business_at=%s::timestamptz AND e.input_json->'evaluation_scope'->>'kind'='explicit_subset' AND e.input_json->'evaluation_scope'->'service_object_ids'=jsonb_build_array(%s::text,%s::text) AND e.result_json='[]'::jsonb AND e.machine_status='partial' AND e.result_sha256=%s) OR NOT EXISTS (SELECT 1 FROM operator_fulfillment_evaluation_objects o WHERE o.tenant_id=NEW.tenant_id AND o.org_id=NEW.org_id AND o.case_id=NEW.case_id AND o.evaluation_id=NEW.evaluation_id AND o.ordinal=1 AND o.service_object_id=%s AND o.object_ref=%s AND o.status='unresolved' AND o.reason_code='service_object_identity_unconfirmed' AND o.facts_canonical_json=convert_to('[]','UTF8') AND o.facts_sha256=%s AND o.result_json=convert_to(%s,'UTF8') AND o.result_sha256=%s) OR NEW.service_object_id<>%s OR NEW.object_ref<>%s OR NEW.reason_code<>'service_object_identity_unconfirmed' OR NEW.result_json<>convert_to(%s,'UTF8') OR NEW.result_sha256<>%s THEN RAISE EXCEPTION 'C311_EVAL_PRECONDITION'; END IF; RAISE EXCEPTION 'C311_EVAL_FORCED'; END IF; RETURN NEW; END $$; CREATE TRIGGER c311_eval_failure BEFORE INSERT ON operator_fulfillment_evaluation_objects FOR EACH ROW EXECUTE FUNCTION c311_eval_fail()`, q(actor.ID), q(digest), q(fixture.Accepted.RunID), q(fixture.Accepted.AttemptID), q(manifest.PackID), q(manifest.Version), q(at.Format(time.RFC3339Nano)), q(ids[0]), q(ids[1]), q(baseline.ResultSHA256), q(ids[0]), q("service_object:"+ids[0]), q(factsSHA), q(string(firstJSON)), q(hex.EncodeToString(firstSHA[:])), q(ids[1]), q("service_object:"+ids[1]), q(string(secondJSON)), q(hex.EncodeToString(secondSHA[:])))
	drop := `DROP TRIGGER IF EXISTS c311_eval_failure ON operator_fulfillment_evaluation_objects; DROP FUNCTION IF EXISTS c311_eval_fail()`
	operatorTrigger(t, env, ddl, drop)
	_, err = core.CreateOperatorFulfillmentEvaluation(ctx, actor, fixture.CaseID, input, manifest, fulfillment.New(), "c311-fail")
	if err == nil || !strings.Contains(err.Error(), "C311_EVAL_FORCED") || strings.Contains(err.Error(), "C311_EVAL_PRECONDITION") {
		t.Fatalf("unexpected error: %v", err)
	}
	if after := operatorEvaluationRows(t, core, actor, fixture.CaseID); !reflect.DeepEqual(before, after) {
		t.Fatalf("evaluation rows changed: %v %v", before, after)
	}
	if after := operatorFrozenRows(t, execution, actor, fixture.CaseID, fixture.Accepted.RunID); !reflect.DeepEqual(frozen, after) {
		t.Fatalf("frozen rows changed: %v %v", frozen, after)
	}
	if _, err = env.harness.pool.Exec(context.Background(), drop); err != nil {
		t.Fatal(err)
	}
	got, err := core.CreateOperatorFulfillmentEvaluation(ctx, actor, fixture.CaseID, input, manifest, fulfillment.New(), "c311-fail")
	if err != nil || len(got.Objects) < 2 {
		t.Fatalf("recovery=%+v err=%v", got, err)
	}
	again, err := core.CreateOperatorFulfillmentEvaluation(ctx, actor, fixture.CaseID, input, manifest, fulfillment.New(), "c311-fail")
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatalf("replay=%+v err=%v", again, err)
	}
}

type operatorSingleEvaluationFixture struct {
	source        operatorFrozenConfirmData
	input         OperatorEvaluationInput
	manifest      capabilitypack.Manifest
	first, second OperatorEvaluation
}

func newOperatorSingleEvaluationFixture(t *testing.T, core, docs, execution *Store, ctx context.Context, actor accounts.User) operatorSingleEvaluationFixture {
	t.Helper()
	f := operatorFrozenConfirmFixture(t, core, docs, execution, ctx, actor)
	return completeOperatorSingleEvaluationFixture(t, core, ctx, actor, f)
}
func completeOperatorSingleEvaluationFixture(t *testing.T, core *Store, ctx context.Context, actor accounts.User, f operatorFrozenConfirmData) operatorSingleEvaluationFixture {
	t.Helper()
	confirmed, replayed, err := core.ConfirmServiceObjectAssociation(ctx, actor, f.CaseID, f.Object.ServiceObjectID, "c311-confirm", f.Input)
	if err != nil || replayed {
		t.Fatalf("confirm=%+v replay=%v err=%v", confirmed, replayed, err)
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	from, until := at.Add(-time.Hour), at.Add(time.Hour)
	binding, err := core.CreateOperatorObjectBinding(ctx, actor, f.CaseID, OperatorObjectBindingInput{ContractObject: OperatorContractObject{ObjectType: "service", ObjectID: f.Option.ObjectRef, Label: strPtr("专线")}, ExternalObject: OperatorExternalObject{ConnectorID: "fixture.oss", ExternalObjectID: "fixture-c311"}, ServiceObjectID: f.Object.ServiceObjectID, ExpectedObjectVersion: confirmed.Version, ExpectedAssociationVersion: confirmed.AssociationVersion, EffectiveFrom: from, EffectiveUntil: until, Confirmed: true}, "c311-binding")
	if err != nil {
		t.Fatal(err)
	}
	rec := "c311-record-v1"
	payload := "fixture://c311/record"
	observation := auditext.Observation{SchemaVersion: auditext.ExternalObservationSchema, SnapshotID: "c311-snapshot", TenantID: accounts.Tenant(actor), CaseID: f.CaseID, ConnectorID: binding.ExternalObject.ConnectorID, SourceSystem: "fixture-c311", ObjectBindingVersion: binding.Version, ExternalObjectID: binding.ExternalObject.ExternalObjectID, RecordID: "c311-record", RecordVersion: &rec, ObservedAt: at, SourceUpdatedAt: &at, EffectiveFrom: from, EffectiveUntil: until, Availability: auditext.AvailabilityAvailable, ReasonCode: "read_succeeded", PayloadRef: &payload, DataClass: auditext.DataClassSynthetic, TemporalProof: &auditext.TemporalProof{Kind: auditext.TemporalProofHistoricalInterval, SourceCatalogID: "fixture-c311", DatasetVersion: "v1", RecordVersion: rec, SourceAsOf: &at, ValidFrom: &from, ValidUntil: &until}, Fields: []auditext.Field{{Name: "configured_bandwidth", Value: "80", Unit: "Mbit/s", SourcePointer: "/bandwidth", SemanticKind: "configured_value"}}}
	set, err := core.CollectOperatorExternalSnapshot(ctx, actor, f.CaseID, binding.Version, at, binding.ExternalObject.ConnectorID, binding.ExternalObject.ExternalObjectID, operatorFixedConnector{observation: observation}, "c311-collect")
	if err != nil {
		t.Fatal(err)
	}
	st, err := core.snapshotScoped(ctx, userScope(actor))
	if err != nil {
		t.Fatal(err)
	}
	attempt, ok := st.attemptByID(f.Accepted.AttemptID)
	if !ok || attempt.RulePackID == "" || attempt.RulePackVersion == "" {
		t.Fatalf("missing pack: %+v", attempt)
	}
	manifest := operatorManifestForRulePack(t, attempt.RulePackID, attempt.RulePackVersion)
	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	input := OperatorEvaluationInput{PackID: manifest.PackID, PackVersion: manifest.Version, SourceReviewID: f.Accepted.RunID, SourceAttemptID: f.Accepted.AttemptID, ContractVersionID: f.Frozen.Document.ID, ObjectBindingVersion: binding.Version, SnapshotSetID: set.SnapshotSetID, BusinessAt: at, EvidenceCutoffAt: &cutoff, Scope: &OperatorEvaluationScope{Kind: "explicit_subset", ServiceObjectIDs: []string{f.Object.ServiceObjectID}}}
	first, err := core.CreateOperatorFulfillmentEvaluation(ctx, actor, f.CaseID, input, manifest, fulfillment.New(), "c311-single-first")
	if err != nil || len(first.Checks) == 0 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := core.CreateOperatorFulfillmentEvaluation(ctx, actor, f.CaseID, input, manifest, fulfillment.New(), "c311-single-second")
	if err != nil || !second.CreatedAt.After(first.CreatedAt) {
		t.Fatalf("second=%+v first=%+v err=%v", second, first, err)
	}
	return operatorSingleEvaluationFixture{source: f, input: input, manifest: manifest, first: first, second: second}
}

type revokeImpactPlanner struct {
	t     *testing.T
	run   func()
	calls int
}

func (p *revokeImpactPlanner) Plan(ctx context.Context, request auditext.ImpactRequest) (auditext.ImpactPlan, error) {
	p.calls++
	plan, err := reviewimpact.New().Plan(ctx, request)
	if err != nil {
		return plan, err
	}
	if err = plan.Validate(); err != nil {
		return plan, err
	}
	p.run()
	return plan, nil
}
func TestOperatorImpactMembershipRevocationFencesSecondTransaction(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	core, docs, execution := env.openRole(t, "core"), env.openRole(t, "document"), env.openRole(t, "execution")
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("admin missing")
	}
	ctx, admin := operatorAtomicSession(t, core, docs, execution, admin)
	fixture := newOperatorSingleEvaluationFixture(t, core, docs, execution, ctx, admin)
	users := accounts.List()
	for i := range users {
		if users[i].Issuer == "" {
			users[i].Issuer = "https://c311.example"
		}
		if users[i].Subject == "" {
			users[i].Subject = "subject:" + users[i].ID
		}
		if users[i].ID == admin.ID {
			users[i].Grants = []access.Grant{{Role: "case_owner", Scope: "case", CaseID: fixture.source.CaseID}}
		}
	}
	profile := accounts.User{ID: "c311-reviewer", DisplayName: "c311 reviewer", Role: "reviewer", TenantID: "directory", OrgID: "directory", Enabled: true, Issuer: "https://c311.example", Subject: "reviewer"}
	users = append(users, profile)
	raw, err := json.Marshal(struct {
		Users []accounts.User `json:"users"`
	}{users})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "directory.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = accounts.ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := accounts.ConfigureDirectory(""); err != nil {
			t.Error(err)
		}
	})
	const principal = "31100000-0000-4000-8000-000000000001"
	const membership = "31100000-0000-4000-8000-000000000002"
	mapping := IdentityCrosswalk{Version: 1, Tenants: []IdentityTenantMap{{ID: admin.TenantID, DisplayName: "c311 tenant", Status: "active"}}, OrgUnits: []IdentityOrgUnitMap{{TenantID: admin.TenantID, ID: admin.OrgID, DisplayName: "c311 org", Status: "active"}}, Principals: []IdentityPrincipalMap{{ID: principal, ActorID: profile.ID, Issuer: profile.Issuer, Subject: profile.Subject, Status: "active"}}, Memberships: []IdentityMembershipMap{{ID: membership, ActorID: profile.ID, TenantID: admin.TenantID, OrgUnitID: admin.OrgID, RoleCode: "reviewer", Grants: []access.Grant{{Role: "reviewer", Scope: "case", CaseID: fixture.source.CaseID}}, Status: "active"}}}
	cfg, err := pgx.ParseConfig(env.harness.sel.TestDSN)
	if err != nil {
		t.Fatal(err)
	}
	importIdentity := func() {
		t.Helper()
		if _, err := ImportIdentityCrosswalk(context.Background(), env.harness.sel.TestDSN, env.harness.schema, cfg.Database, mapping, true); err != nil {
			t.Fatal(err)
		}
	}
	importIdentity()
	login, err := core.ResolveVerifiedOIDC(context.Background(), profile, "client")
	if err != nil {
		t.Fatal(err)
	}
	if login.PendingToken != "" {
		login, err = core.ConsumePendingMembership(context.Background(), login.PendingToken, membership, "client")
		if err != nil {
			t.Fatal(err)
		}
	}
	token := login.SessionToken
	reviewer, _, err := core.AuthenticateSession(context.Background(), token)
	if err != nil || reviewer.MembershipID != membership {
		t.Fatalf("session=%+v err=%v", reviewer, err)
	}
	oldProof, ok := reviewer.CommandAuthority()
	if !ok {
		t.Fatal("missing original proof")
	}
	reviewCtx := WithAdmissionSessionToken(context.Background(), token)
	if _, _, _, _, _, err = core.operatorCaseState(reviewCtx, reviewer, fixture.source.CaseID, access.Read); err != nil {
		t.Fatal(err)
	}
	before := operatorEvaluationRows(t, core, admin, fixture.source.CaseID)
	revoked := false
	planner := &revokeImpactPlanner{t: t}
	planner.run = func() {
		mapping.Memberships[0].Grants = []access.Grant{}
		importIdentity()
		fresh, _, e := core.AuthenticateSession(context.Background(), token)
		if e != nil || fresh.MembershipID != membership || fresh.Role != reviewer.Role || fresh.TenantID != reviewer.TenantID || fresh.OrgID != reviewer.OrgID || !fresh.Enabled {
			t.Fatalf("fresh actor=%+v err=%v", fresh, e)
		}
		proof, ok := fresh.CommandAuthority()
		if !ok || proof.Revision == oldProof.Revision || proof.ContextVersion != oldProof.ContextVersion || len(fresh.Grants) != 0 {
			t.Fatalf("proof did not change: %+v", proof)
		}
		_, _, _, _, _, e = core.operatorCaseState(WithAdmissionSessionToken(context.Background(), token), fresh, fixture.source.CaseID, access.Read)
		if !isForbiddenFault(e) && !isNotFoundFault(e) {
			t.Fatalf("fresh case read still allowed: %v", e)
		}
		revoked = true
	}
	plan, err := core.GetOperatorImpactPlan(reviewCtx, reviewer, fixture.source.CaseID, fixture.second.EvaluationID, auditext.ImpactExplain, planner)
	var f *fault.Error
	if !errors.As(err, &f) || f.Code != "context_stale" || f.Status != 409 || planner.calls != 1 || !revoked || !reflect.DeepEqual(plan, auditext.ImpactPlan{}) {
		t.Fatalf("plan=%+v calls=%d revoked=%v err=%v", plan, planner.calls, revoked, err)
	}
	after := operatorEvaluationRows(t, core, admin, fixture.source.CaseID)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("impact rows changed: %v %v", before, after)
	}
}

func TestOperatorEvaluationUsesFrozenParseAfterDifferentReparse(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	core, docs, execution := env.openRole(t, "core"), env.openRole(t, "document"), env.openRole(t, "execution")
	actor, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("admin missing")
	}
	ctx, actor := operatorAtomicSession(t, core, docs, execution, actor)
	pack := publishSyntheticAcceptancePack(t, core, actor)
	publishSyntheticAcceptancePolicy(t, core, actor, pack)
	caseID, err := core.CreateCaseWithBusiness(actor, "重解析保持冻结事实", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	documentID, err := docs.AddDocument(ctx, actor, caseID, "draft_contract", "contract.png", "image/png", []byte("synthetic image"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = docs.AddDocument(ctx, actor, caseID, "procurement", "procurement.txt", "text/plain", []byte("采购文件：电路 LINE-OBJ-001 最低带宽为 100 Mbit/s。")); err != nil {
		t.Fatal(err)
	}
	models := &frozenOCRModel{}
	models.setTranscript("拟签合同：电路 LINE-OBJ-001 最低保障带宽 100 Mbit/s。月可用率不低于 99.9%。")
	docs.UseModels(models)
	execution.UseModels(models)
	execution.UseDocumentPreparer(docs)
	accepted, err := execution.StartReviewAsyncAt(ctx, actor, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := execution.ClaimNextJob(ctx, "c311-parse-worker", time.Minute)
	if err != nil || claim.JobID != accepted.JobID {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	if err = execution.RunClaimedJob(ctx, claim); err != nil {
		t.Fatal(err)
	}
	stateBefore, err := core.snapshotScoped(ctx, userScope(actor))
	if err != nil {
		t.Fatal(err)
	}
	input, ok := stateBefore.attemptInput(accepted.AttemptID)
	if !ok {
		t.Fatal("missing input")
	}
	var frozen FrozenDocument
	for _, d := range input.Documents {
		if d.Document.ID == documentID {
			frozen = d
			break
		}
	}
	if frozen.Material == nil || frozen.ParsedDocumentID == "" || !strings.Contains(frozen.Text, "100 Mbit/s") {
		t.Fatalf("missing original frozen parse: %+v", frozen)
	}
	object, replayed, err := core.CreateServiceObject(ctx, actor, caseID, ServiceObjectCreateInput{ObjectType: ServiceObjectDedicatedLine, DisplayName: "冻结解析专线"}, "c311-parse-object")
	if err != nil || replayed {
		t.Fatalf("object=%+v err=%v", object, err)
	}
	source := ServiceObjectAssociationPreviewInput{RunID: accepted.RunID, AttemptID: accepted.AttemptID, MaterialID: frozen.Material.MaterialID, MaterialVersion: frozen.Material.Version, DocumentID: documentID, ParsedDocumentID: frozen.ParsedDocumentID}
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
		t.Fatalf("no old facts: %+v", preview.Options)
	}
	fixture := operatorFrozenConfirmData{CaseID: caseID, Accepted: accepted, Frozen: frozen, Object: object, Source: source, Option: *option, Input: ServiceObjectAssociationConfirmInput{Confirmed: true, ExpectedObjectVersion: 1, ExpectedAssociationVersion: 0, Source: source, ObjectRef: option.ObjectRef, FactsSHA256: option.FactsSHA256, CandidatesSHA256: preview.CandidatesSHA256}}
	single := completeOperatorSingleEvaluationFixture(t, core, ctx, actor, fixture)
	beforeFrozen := operatorFrozenRows(t, execution, actor, caseID, accepted.RunID)
	beforeObjects := operatorAtomicRows(t, core, actor, caseID)
	models.setTranscript("拟签合同：电路 LINE-OBJ-001 最低保障带宽 200 Mbit/s。月可用率不低于 99.9%。")
	if err = docs.Reparse(ctx, actor, caseID, documentID); err != nil {
		t.Fatal(err)
	}
	afterState, err := core.snapshotScoped(ctx, userScope(actor))
	if err != nil {
		t.Fatal(err)
	}
	latest, ok := afterState.latestParsed(documentID, "")
	if !ok || latest.ID == frozen.ParsedDocumentID || frozen.NormalizedHash == nil || latest.NormalizedDigest == *frozen.NormalizedHash || !strings.Contains(latest.Text, "200 Mbit/s") {
		t.Fatalf("missing different reparse: %+v", latest)
	}
	var originalFound bool
	for _, doc := range afterState.documents(caseID) {
		if doc.ID == documentID {
			originalFound = true
			if doc.Digest != frozen.Document.Digest {
				t.Fatalf("original digest changed: %s %s", doc.Digest, frozen.Document.Digest)
			}
		}
	}
	if !originalFound {
		t.Fatal("original document missing")
	}
	interpreted := Interpreted{Document: frozen.Document, Ready: true, FromOCR: latest.FromOCR, ParseError: latest.ParseError, Text: latest.Text, IR: latest.IR, ParsedDocument: &latest}
	sources, err := telecomSources([]Interpreted{interpreted})
	if err != nil || len(sources) != 1 {
		t.Fatalf("new source invalid: %v", err)
	}
	newFacts, err := telecom.ExtractContractMaterial(rulepack.Request{BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service"}, sources[0])
	if err != nil {
		t.Fatal(err)
	}
	has200 := false
	for _, fact := range newFacts {
		if fact.ObjectRef == "LINE-OBJ-001" && fact.Dimension == "minimum_bandwidth" && fact.Value == "200" {
			has200 = true
		}
	}
	if !has200 {
		t.Fatalf("new parse did not produce changed fact: %+v", newFacts)
	}
	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	single.input.EvidenceCutoffAt = &cutoff
	result, err := core.CreateOperatorFulfillmentEvaluation(ctx, actor, caseID, single.input, single.manifest, fulfillment.New(), "c311-after-reparse")
	if err != nil || len(result.Objects) != 1 || result.Objects[0].Source == nil || result.Objects[0].Source.ParsedDocumentID != frozen.ParsedDocumentID || result.Objects[0].FactsSHA256 != option.FactsSHA256 {
		t.Fatalf("old evidence not used: %+v err=%v", result, err)
	}
	has100 := false
	oldCitation := false
	for _, fact := range result.Objects[0].Facts {
		for _, evidence := range fact.Evidence {
			if evidence.Kind == auditext.EvidenceDocument {
				if evidence.DocumentVersionID != documentID || evidence.ParsedVersionID != frozen.ParsedDocumentID {
					t.Fatalf("wrong frozen citation: %+v", evidence)
				}
				oldCitation = true
			}
		}
		if fact.ObjectRef == "LINE-OBJ-001" && fact.Dimension == "minimum_bandwidth" && fact.Value.Value == "100" {
			has100 = true
		}
		if fact.Dimension == "minimum_bandwidth" && fact.Value.Value == "200" {
			t.Fatalf("new fact leaked into old attempt: %+v", fact)
		}
	}
	if !has100 || !oldCitation {
		t.Fatalf("old fact or citation missing: %+v", result.Objects[0].Facts)
	}
	if after := operatorFrozenRows(t, execution, actor, caseID, accepted.RunID); !reflect.DeepEqual(beforeFrozen, after) {
		t.Fatalf("old input changed: %v %v", beforeFrozen, after)
	}
	if after := operatorAtomicRows(t, core, actor, caseID); !reflect.DeepEqual(beforeObjects, after) {
		t.Fatalf("old association changed: %v %v", beforeObjects, after)
	}
}
