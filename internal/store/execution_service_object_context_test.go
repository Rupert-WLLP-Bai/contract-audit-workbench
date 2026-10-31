package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	if _, err := execution.RetryAsync(ctx, reviewer, caseID, accepted.RunID); !isForbiddenFault(err) && !isNotFoundFault(err) {
		t.Fatalf("revoked actor retry was not rejected by authorization: %v", err)
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

// TestRetryRejectsLegitimateMappingVersionChangeAfterObjectContextFreeze
// proves a real, publicly approved mapping correction is detected by retry.
// The case is telecom because operator mappings are only legal there; the
// second review freezes the confirmed mapping, then a two-person mapping
// replacement advances its version. Retry must return 409 without rewriting
// the accepted attempt or snapshot bytes.
func TestRetryRejectsLegitimateMappingVersionChangeAfterObjectContextFreeze(t *testing.T) {
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
	pack := publishSyntheticAcceptancePack(t, core, admin)
	publishSyntheticAcceptancePolicy(t, core, admin, pack)
	caseID, err := core.CreateCaseWithBusiness(admin, "服务对象映射变更重试", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatalf("create telecom case: %v", err)
	}
	if _, err := document.Generate(ctx, admin, caseID, telecomTemplateID, validTelecomTemplateFields()); err != nil {
		t.Fatalf("generate telecom draft contract: %v", err)
	}
	if _, err := document.AddDocument(ctx, admin, caseID, "procurement", "procurement.txt", "text/plain",
		[]byte("采购文件：电路 LINE-001 最低保障带宽 100 Mbit/s。审批上限 20%。")); err != nil {
		t.Fatalf("add procurement material: %v", err)
	}
	if err := core.AddMember(admin, caseID, reviewer.ID); err != nil {
		t.Fatalf("authorize independent mapping reviewer: %v", err)
	}
	execution.UseDocumentPreparer(document)
	execution.UseModels(&frozenOCRModel{})

	// The first review establishes the frozen attempt the association must cite.
	first, err := execution.StartReviewAsyncAt(ctx, admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatalf("accept base review: %v", err)
	}
	firstClaim, err := execution.ClaimNextJob(ctx, "mapping-change-worker-1", time.Minute)
	if err != nil || firstClaim.JobID != first.JobID {
		t.Fatalf("claim base review: claim=%+v err=%v", firstClaim, err)
	}
	if err := execution.RunClaimedJob(ctx, firstClaim); err != nil {
		t.Fatalf("finish base review: %v", err)
	}
	firstState, err := execution.snapshotScoped(ctx, userScope(admin))
	if err != nil {
		t.Fatal(err)
	}
	firstInput, ok := firstState.attemptInput(first.AttemptID)
	if !ok {
		t.Fatal("base review attempt has no frozen input")
	}
	var frozenDraft FrozenDocument
	for i := range firstInput.Documents {
		if firstInput.Documents[i].Document.Role == "draft_contract" {
			frozenDraft = firstInput.Documents[i]
			break
		}
	}
	if frozenDraft.Document.ID == "" || frozenDraft.ParsedDocumentID == "" || frozenDraft.Material == nil {
		t.Fatalf("base review did not pin the draft material and parse: %+v", frozenDraft)
	}

	object, _, err := core.CreateServiceObject(ctx, admin, caseID, ServiceObjectCreateInput{
		ObjectType: ServiceObjectDedicatedLine, DisplayName: "专线 LINE-001",
		Aliases: []ServiceObjectAlias{{Kind: "circuit", Value: "LINE-001"}},
	}, "mapping-change-object")
	if err != nil {
		t.Fatalf("create service object: %v", err)
	}
	source := ServiceObjectAssociationPreviewInput{RunID: first.RunID, AttemptID: first.AttemptID,
		MaterialID: frozenDraft.Material.MaterialID, MaterialVersion: frozenDraft.Material.Version,
		DocumentID: frozenDraft.Document.ID, ParsedDocumentID: frozenDraft.ParsedDocumentID}
	preview, err := core.PreviewServiceObjectAssociation(ctx, admin, caseID, object.ServiceObjectID, source)
	if err != nil || len(preview.Options) == 0 {
		t.Fatalf("preview frozen association candidates: options=%+v err=%v", preview.Options, err)
	}
	var option *ServiceObjectAssociationOption
	for i := range preview.Options {
		if preview.Options[i].ObjectRef == "LINE-001" {
			option = &preview.Options[i]
			break
		}
	}
	if option == nil {
		t.Fatalf("frozen attempt did not resolve LINE-001: %+v", preview.Options)
	}
	confirmed, _, err := core.ConfirmServiceObjectAssociation(ctx, admin, caseID, object.ServiceObjectID, "mapping-change-association",
		ServiceObjectAssociationConfirmInput{Confirmed: true, ExpectedObjectVersion: preview.ExpectedObjectVersion,
			ExpectedAssociationVersion: preview.ExpectedAssociationVersion, Source: source, ObjectRef: option.ObjectRef,
			FactsSHA256: option.FactsSHA256, CandidatesSHA256: preview.CandidatesSHA256})
	if err != nil {
		t.Fatalf("confirm frozen association: %v", err)
	}
	businessAt, err := time.Parse(time.RFC3339Nano, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := core.CreateOperatorObjectBinding(ctx, admin, caseID, OperatorObjectBindingInput{
		ContractObject: OperatorContractObject{ObjectType: "service", ObjectID: "LINE-001"},
		ExternalObject: OperatorExternalObject{ConnectorID: "fixture.oss", ExternalObjectID: "fixture-line-001"},
		ServiceObjectID: object.ServiceObjectID, ExpectedObjectVersion: confirmed.Version,
		ExpectedAssociationVersion: confirmed.AssociationVersion,
		EffectiveFrom: businessAt.Add(-24 * time.Hour), EffectiveUntil: businessAt.Add(24 * time.Hour), Confirmed: true,
	}, "mapping-change-binding-1")
	if err != nil {
		t.Fatalf("create confirmed operator mapping: %v", err)
	}

	// The second review freezes the confirmed mapping in its object context.
	second, err := execution.StartReviewAsyncAt(ctx, admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatalf("accept review with frozen mapping: %v", err)
	}
	if second.RunID == first.RunID {
		t.Fatal("mapping-aware acceptance reused the pre-mapping run")
	}
	secondClaim, err := execution.ClaimNextJob(ctx, "mapping-change-worker-2", time.Minute)
	if err != nil || secondClaim.JobID != second.JobID {
		t.Fatalf("claim mapping-aware review: claim=%+v err=%v", secondClaim, err)
	}
	if err := execution.RunClaimedJob(ctx, secondClaim); err != nil {
		t.Fatalf("finish mapping-aware review: %v", err)
	}
	beforeState, err := execution.snapshotScoped(ctx, userScope(admin))
	if err != nil {
		t.Fatal(err)
	}
	before := beforeState.snapshotOf(second.RunID)
	if before.ServiceObjectContext == nil || before.ServiceObjectContextHash == "" {
		t.Fatalf("mapping-aware review did not freeze its object context: %+v", before.ServiceObjectContext)
	}
	frozenMapping := false
	for _, item := range before.ServiceObjectContext.Items {
		if item.ServiceObjectID == object.ServiceObjectID && item.MappingID == binding.MappingID && item.MappingVersion == binding.MappingVersion {
			frozenMapping = true
			break
		}
	}
	if !frozenMapping {
		t.Fatalf("frozen context omitted the confirmed mapping: %+v", before.ServiceObjectContext.Items)
	}

	// Approve a legitimate replacement through the public two-person workflow.
	quote := []rune(frozenDraft.Text)
	if len(quote) == 0 {
		t.Fatal("frozen draft has no text basis")
	}
	basisDigest := sha256.Sum256([]byte(string(quote)))
	basisRefs, err := json.Marshal([]materialAnchorInput{{Kind: "material_anchor", MaterialID: frozenDraft.Material.MaterialID,
		MaterialVersion: frozenDraft.Material.Version, DocumentID: frozenDraft.Document.ID,
		ParsedDocumentID: frozenDraft.ParsedDocumentID, Start: 0, End: len(quote), QuoteDigest: hex.EncodeToString(basisDigest[:])}})
	if err != nil {
		t.Fatal(err)
	}
	cas := OperatorMappingChangeCAS{ExpectedMappingVersion: binding.MappingVersion, ExpectedBindingVersion: binding.Version,
		ExpectedObjectVersion: binding.ObjectVersion, ExpectedAssociationVersion: binding.AssociationVersion}
	patch := OperatorMappingChangePatch{Kind: OperatorMappingReplace,
		ExternalObject: &OperatorExternalObject{ConnectorID: "fixture.oss", ExternalObjectID: "fixture-line-001-replacement"},
		EffectiveFrom:  binding.EffectiveFrom, EffectiveUntil: binding.EffectiveUntil}
	changePreview, err := core.PreviewOperatorMappingChange(ctx, admin, caseID, binding.MappingID, cas, patch, basisRefs)
	if err != nil {
		t.Fatalf("preview legitimate mapping replacement: %v", err)
	}
	proposal, _, err := core.ProposeOperatorMappingChange(ctx, admin, caseID, binding.MappingID, OperatorMappingChangeCreateInput{
		CAS: cas, Patch: patch, BasisRefs: basisRefs, ImpactSHA256: changePreview.ImpactSHA256,
	}, "mapping-change-proposal-1")
	if err != nil {
		t.Fatalf("propose legitimate mapping replacement: %v", err)
	}
	decision, _, err := core.DecideOperatorMappingChange(ctx, reviewer, caseID, proposal.RequestID,
		OperatorMappingChangeDecisionInput{Decision: "approve", ExpectedMappingVersion: cas.ExpectedMappingVersion,
			ExpectedBindingVersion: cas.ExpectedBindingVersion, ExpectedObjectVersion: cas.ExpectedObjectVersion,
			ExpectedAssociationVersion: cas.ExpectedAssociationVersion, ExpectedAfterSHA256: proposal.AfterSHA256,
			ExpectedImpactSHA256: proposal.ImpactSHA256, Reason: "依据冻结合同条款修正外部映射"}, "mapping-change-decision-1")
	if err != nil {
		t.Fatalf("approve legitimate mapping replacement: %v", err)
	}
	if decision.Decision != "approved" || decision.MappingVersion != binding.MappingVersion+1 {
		t.Fatalf("mapping replacement did not advance the terminal version: %+v", decision)
	}

	if _, err := execution.RetryAsync(ctx, admin, caseID, second.RunID); !isConflictFault(err) {
		t.Fatalf("retry adopted a legitimately changed mapping version: %v", err)
	}
	afterState, err := execution.snapshotScoped(ctx, userScope(admin))
	if err != nil {
		t.Fatal(err)
	}
	if count := serviceObjectContextAttemptCount(afterState, second.RunID); count != 1 {
		t.Fatalf("rejected mapping-change retry created an attempt: count=%d", count)
	}
	if latest, ok := afterState.latestAttempt(second.RunID); !ok || latest.ID != second.AttemptID {
		t.Fatalf("rejected mapping-change retry replaced the accepted attempt: %+v", latest)
	}
	if got := afterState.snapshotOf(second.RunID); !sameRunSignature(before, got) ||
		got.ServiceObjectContextHash != before.ServiceObjectContextHash ||
		!reflect.DeepEqual(got.ServiceObjectContext, before.ServiceObjectContext) {
		t.Fatalf("rejected mapping-change retry rewrote the frozen context: before=%+v after=%+v", before.ServiceObjectContext, got.ServiceObjectContext)
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
