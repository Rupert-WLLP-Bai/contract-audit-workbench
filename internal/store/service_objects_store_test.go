package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestServiceObjectFrozenAssociationLifecycleAndReceiptReplayInPostgres(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("MIGRATOR_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for service object persistence")
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
	core, document, execution := env.openRole(t, "core"), env.openRole(t, "document"), env.openRole(t, "execution")
	pack := publishSyntheticAcceptancePack(t, core, admin)
	publishSyntheticAcceptancePolicy(t, core, admin, pack)
	caseID, err := core.CreateCaseWithBusiness(admin, "服务对象冻结关联生命周期", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatalf("create telecom case: %v", err)
	}
	draft := []byte("拟签合同：电路 LINE-OBJ-001 最低保障带宽 100 Mbit/s。月可用率不低于 99.9%，故障恢复时间不超过 4 小时。")
	if _, err := document.AddDocument(ctx, admin, caseID, "draft_contract", "service-object-draft.txt", "text/plain", draft); err != nil {
		t.Fatalf("add draft contract: %v", err)
	}
	if _, err := document.AddDocument(ctx, admin, caseID, "procurement", "service-object-procurement.txt", "text/plain",
		[]byte("采购文件：电路 LINE-OBJ-001 最低保障带宽 100 Mbit/s。审批上限 20%。")); err != nil {
		t.Fatalf("add procurement source: %v", err)
	}
	execution.UseDocumentPreparer(document)
	execution.UseModels(&frozenOCRModel{})
	accepted, err := execution.StartReviewAsyncAt(ctx, admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatalf("accept review: %v", err)
	}
	claim, err := execution.ClaimNextJob(ctx, "service-object-worker", time.Minute)
	if err != nil || claim.JobID != accepted.JobID {
		t.Fatalf("claim frozen review job: claim=%+v err=%v", claim, err)
	}
	if err := execution.RunClaimedJob(ctx, claim); err != nil {
		t.Fatalf("run frozen review: %v", err)
	}
	state, err := core.snapshotScoped(ctx, userScope(admin))
	if err != nil {
		t.Fatal(err)
	}
	input, ok := state.attemptInput(claim.AttemptID)
	if !ok || input.RunID != accepted.RunID {
		t.Fatal("review attempt has no immutable frozen input")
	}
	var frozen FrozenDocument
	var procurementFrozen FrozenDocument
	for _, doc := range input.Documents {
		if doc.Document.Role == "draft_contract" && doc.Material != nil {
			frozen = doc
		}
		if doc.Document.Role == "procurement" && doc.Material != nil {
			procurementFrozen = doc
		}
	}
	if frozen.Document.ID == "" || frozen.ParsedDocumentID == "" || frozen.Material == nil {
		t.Fatal("frozen review did not pin the uploaded draft and parse")
	}
	if procurementFrozen.Document.ID == "" || procurementFrozen.ParsedDocumentID == "" || procurementFrozen.Material == nil {
		t.Fatal("frozen review did not pin the procurement document and parse")
	}
	materialVersion := 0
	for _, version := range state.MaterialVersions {
		if version.MaterialID == frozen.Material.MaterialID && version.DocumentID == frozen.Document.ID {
			materialVersion = version.Version
			break
		}
	}
	if materialVersion < 1 {
		t.Fatal("frozen draft has no immutable material version")
	}
	created, replayed, err := core.CreateServiceObject(ctx, admin, caseID, ServiceObjectCreateInput{
		ObjectType: ServiceObjectDedicatedLine, DisplayName: "专线 LINE-OBJ-001",
		Aliases: []ServiceObjectAlias{{Kind: "circuit", Value: "LINE-OBJ-001"}, {Kind: "customer_code", Value: "CUS-001"}},
	}, "service-object-create")
	if err != nil || replayed || created.Version != 1 || created.AssociationVersion != 0 {
		t.Fatalf("create pending object v1: result=%+v replayed=%v err=%v", created, replayed, err)
	}
	if replay, replayed, err := core.CreateServiceObject(ctx, admin, caseID, ServiceObjectCreateInput{
		ObjectType: ServiceObjectDedicatedLine, DisplayName: "专线 LINE-OBJ-001",
		Aliases: []ServiceObjectAlias{{Kind: "customer_code", Value: "CUS-001"}, {Kind: "circuit", Value: "LINE-OBJ-001"}},
	}, "service-object-create"); err != nil || !replayed || replay != created {
		t.Fatalf("create idempotency key did not replay exact original result: result=%+v replayed=%v err=%v", replay, replayed, err)
	}
	if _, _, err := core.CreateServiceObject(ctx, admin, caseID, ServiceObjectCreateInput{
		ObjectType: ServiceObjectService, DisplayName: "different payload",
	}, "service-object-create"); !isConflictFault(err) {
		t.Fatalf("create idempotency key accepted a different payload: %v", err)
	}
	source := ServiceObjectAssociationPreviewInput{RunID: accepted.RunID, AttemptID: claim.AttemptID,
		MaterialID: frozen.Material.MaterialID, MaterialVersion: materialVersion, DocumentID: frozen.Document.ID, ParsedDocumentID: frozen.ParsedDocumentID}
	preview, err := core.PreviewServiceObjectAssociation(ctx, admin, caseID, created.ServiceObjectID, source)
	if err != nil || len(preview.Options) == 0 {
		t.Fatalf("preview object refs from frozen attempt: options=%+v err=%v", preview.Options, err)
	}
	otherCaseID, err := core.CreateCaseWithBusiness(admin, "服务对象错案候选", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatal(err)
	}
	wrongSources := []struct {
		name   string
		caseID string
		input  ServiceObjectAssociationPreviewInput
	}{
		{"wrong source case", otherCaseID, source},
		{"wrong run", caseID, func() ServiceObjectAssociationPreviewInput {
			value := source
			value.RunID = "run-from-another-case"
			return value
		}()},
		{"wrong attempt", caseID, func() ServiceObjectAssociationPreviewInput {
			value := source
			value.AttemptID = "attempt-from-another-run"
			return value
		}()},
		{"wrong material ID", caseID, func() ServiceObjectAssociationPreviewInput {
			value := source
			value.MaterialID = "material-from-another-case"
			return value
		}()},
		{"wrong material version", caseID, func() ServiceObjectAssociationPreviewInput { value := source; value.MaterialVersion++; return value }()},
		{"wrong document", caseID, func() ServiceObjectAssociationPreviewInput {
			value := source
			value.DocumentID = "document-from-another-case"
			return value
		}()},
		{"wrong parse", caseID, func() ServiceObjectAssociationPreviewInput {
			value := source
			value.ParsedDocumentID = "parse-from-another-attempt"
			return value
		}()},
	}
	for _, test := range wrongSources {
		_, err := core.PreviewServiceObjectAssociation(ctx, admin, test.caseID, created.ServiceObjectID, test.input)
		if test.name == "wrong source case" {
			if !isServiceObjectFault(err, 404, "not_found") {
				t.Errorf("cross-case object lookup did not fail closed as not found: %v", err)
			}
		} else if !isConflictFault(err) {
			t.Errorf("%s candidate unexpectedly accepted: %v", test.name, err)
		}
	}
	var option *ServiceObjectAssociationOption
	for i := range preview.Options {
		if preview.Options[i].ObjectRef == "LINE-OBJ-001" {
			option = &preview.Options[i]
			break
		}
	}
	if option == nil {
		t.Fatalf("frozen text did not produce expected object reference: %+v", preview.Options)
	}
	confirm := ServiceObjectAssociationConfirmInput{Confirmed: true, ExpectedObjectVersion: 1, ExpectedAssociationVersion: 0,
		Source: source, ObjectRef: option.ObjectRef, FactsSHA256: option.FactsSHA256, CandidatesSHA256: preview.CandidatesSHA256}
	unconfirmed := confirm
	unconfirmed.Confirmed = false
	if _, _, err := core.ConfirmServiceObjectAssociation(ctx, admin, caseID, created.ServiceObjectID, "service-object-unconfirmed", unconfirmed); !isServiceObjectFault(err, 422, "invalid") {
		t.Fatalf("association endpoint accepted missing human-confirmation intent: %v", err)
	}
	changedCandidateSet := confirm
	changedCandidateSet.CandidatesSHA256 = strings.Repeat("0", 64)
	if _, _, err := core.ConfirmServiceObjectAssociation(ctx, admin, caseID, created.ServiceObjectID, "service-object-stale-candidates", changedCandidateSet); !isConflictFault(err) {
		t.Fatalf("association accepted a stale full candidate-set digest: %v", err)
	}
	first, replayed, err := core.ConfirmServiceObjectAssociation(ctx, admin, caseID, created.ServiceObjectID, "service-object-confirm-1", confirm)
	if err != nil || replayed || first.Version != 2 || first.AssociationVersion != 1 {
		t.Fatalf("confirm pending identity with frozen association: result=%+v replayed=%v err=%v", first, replayed, err)
	}
	history, err := core.GetServiceObjectHistory(ctx, admin, caseID, created.ServiceObjectID)
	if err != nil || len(history.Versions) != 2 || len(history.Associations) != 1 || history.Head.CurrentVersion != 2 || history.Head.CurrentAssociationVersion != 1 {
		t.Fatalf("read confirmed history: history=%+v err=%v", history, err)
	}
	frozenFacts := append([]byte(nil), history.Associations[0].FactsCanonicalJSON...)
	frozenFactsSHA := history.Associations[0].FactsSHA256
	if digest := sha256String(frozenFacts); digest != frozenFactsSHA {
		t.Fatalf("association facts SHA does not match stored frozen bytes: got=%s want=%s", digest, frozenFactsSHA)
	}
	appendOnlyTx, err := env.harness.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalScope(ctx, appendOnlyTx, userScope(admin)); err != nil {
		_ = appendOnlyTx.Rollback(ctx)
		t.Fatal(err)
	}
	_, err = appendOnlyTx.Exec(ctx, `UPDATE service_object_contract_associations SET object_ref=object_ref
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND service_object_id=$4 AND association_version=1`,
		accounts.Tenant(admin), admin.OrgID, caseID, created.ServiceObjectID)
	if !isPostgresError(err, "P0001", "append-only") {
		_ = appendOnlyTx.Rollback(ctx)
		t.Fatalf("contract association rewrite did not fail at append-only trigger: %v", err)
	}
	_ = appendOnlyTx.Rollback(ctx)
	assertAssociationTupleRejected := func(name string, constraints []string, mutate func(*Case, *ServiceObjectContractAssociation)) {
		t.Helper()
		badCase := Case{ID: caseID, OwnerOrgID: admin.OrgID}
		badAssociation := history.Associations[0]
		badAssociation.AssociationVersion = 99
		mutate(&badCase, &badAssociation)
		tx, err := core.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := setLocalScope(ctx, tx, userScope(admin)); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		err = insertServiceObjectAssociation(ctx, tx, admin, badCase, badAssociation)
		constraintMatched := false
		for _, constraint := range constraints {
			constraintMatched = constraintMatched || isPostgresConstraint(err, "23503", constraint)
		}
		if !constraintMatched {
			_ = tx.Rollback(ctx)
			t.Errorf("%s association tuple was not rejected by a composite foreign key: %v", name, err)
			return
		}
		_ = tx.Rollback(ctx)
	}
	assertAssociationTupleRejected("cross-case object", []string{"service_object_associations_object_version_fk", "service_object_associations_material_version_document_fk", "service_object_associations_source_run_case_fk"}, func(item *Case, _ *ServiceObjectContractAssociation) { item.ID = otherCaseID })
	assertAssociationTupleRejected("wrong object version", []string{"service_object_associations_object_version_fk"}, func(_ *Case, association *ServiceObjectContractAssociation) { association.ObjectVersion++ })
	assertAssociationTupleRejected("wrong run", []string{"service_object_associations_source_run_case_fk", "service_object_associations_source_attempt_run_fk", "service_object_associations_attempt_input_fk", "service_object_associations_parse_selection_fk"}, func(_ *Case, association *ServiceObjectContractAssociation) {
		association.SourceRunID = "run-from-another-case"
	})
	assertAssociationTupleRejected("wrong attempt", []string{"service_object_associations_source_attempt_run_fk", "service_object_associations_attempt_input_fk", "service_object_associations_parse_selection_fk"}, func(_ *Case, association *ServiceObjectContractAssociation) {
		association.SourceAttemptID = "attempt-from-another-run"
	})
	assertAssociationTupleRejected("wrong material", []string{"service_object_associations_material_version_document_fk"}, func(_ *Case, association *ServiceObjectContractAssociation) {
		association.MaterialID = "material-from-another-case"
	})
	assertAssociationTupleRejected("wrong material version", []string{"service_object_associations_material_version_document_fk"}, func(_ *Case, association *ServiceObjectContractAssociation) { association.MaterialVersion++ })
	assertAssociationTupleRejected("wrong document", []string{"service_object_associations_material_version_document_fk"}, func(_ *Case, association *ServiceObjectContractAssociation) {
		association.DocumentID = procurementFrozen.Document.ID
		association.ParsedDocumentID = procurementFrozen.ParsedDocumentID
	})
	assertAssociationTupleRejected("wrong parse", []string{"service_object_associations_parse_selection_fk"}, func(_ *Case, association *ServiceObjectContractAssociation) {
		association.ParsedDocumentID = "parse-from-another-attempt"
	})

	secondConfirm := confirm
	secondConfirm.ExpectedObjectVersion, secondConfirm.ExpectedAssociationVersion = 2, 1
	secondPreview, err := core.PreviewServiceObjectAssociation(ctx, admin, caseID, created.ServiceObjectID, source)
	if err != nil {
		t.Fatalf("refresh candidates after prior association change: %v", err)
	}
	secondConfirm.CandidatesSHA256 = secondPreview.CandidatesSHA256
	second, replayed, err := core.ConfirmServiceObjectAssociation(ctx, admin, caseID, created.ServiceObjectID, "service-object-confirm-2", secondConfirm)
	if err != nil || replayed || second.Version != 2 || second.AssociationVersion != 2 {
		t.Fatalf("reassociate without changing identity: result=%+v replayed=%v err=%v", second, replayed, err)
	}
	if replayedResult, replayed, err := core.ConfirmServiceObjectAssociation(ctx, admin, caseID, created.ServiceObjectID, "service-object-confirm-1", confirm); err != nil || !replayed || replayedResult != first {
		t.Fatalf("old idempotency key did not replay original immutable response: result=%+v replayed=%v err=%v", replayedResult, replayed, err)
	}
	changed := confirm
	changed.FactsSHA256 = strings.Repeat("0", 64)
	if _, _, err := core.ConfirmServiceObjectAssociation(ctx, admin, caseID, created.ServiceObjectID, "service-object-confirm-1", changed); !isConflictFault(err) {
		t.Fatalf("same idempotency key accepted changed body: %v", err)
	}
	otherObject, _, err := core.CreateServiceObject(ctx, admin, caseID, ServiceObjectCreateInput{
		ObjectType: ServiceObjectDedicatedLine, DisplayName: "另一个线路对象",
	}, "service-object-create-2")
	if err != nil {
		t.Fatalf("create second object for idempotency scope test: %v", err)
	}
	if _, _, err := core.ConfirmServiceObjectAssociation(ctx, admin, caseID, otherObject.ServiceObjectID, "service-object-confirm-1", confirm); !isConflictFault(err) {
		t.Fatalf("same idempotency key replayed across service object IDs: %v", err)
	}
	objectPage1, err := core.ListServiceObjects(ctx, admin, caseID, "", 1)
	if err != nil || len(objectPage1.Items) != 1 || objectPage1.NextCursor == nil {
		t.Fatalf("service object first summary page is not bounded/keyset paginated: page=%+v err=%v", objectPage1, err)
	}
	objectPage2, err := core.ListServiceObjects(ctx, admin, caseID, *objectPage1.NextCursor, 1)
	if err != nil || len(objectPage2.Items) != 1 || objectPage2.NextCursor != nil || objectPage1.Items[0].ServiceObjectID == objectPage2.Items[0].ServiceObjectID {
		t.Fatalf("service object next summary page is not stable: page=%+v err=%v", objectPage2, err)
	}
	versionPage1, err := core.ListServiceObjectVersions(ctx, admin, caseID, created.ServiceObjectID, "", 1)
	if err != nil || len(versionPage1.Items) != 1 || versionPage1.Items[0].Version != 2 || versionPage1.NextCursor == nil {
		t.Fatalf("service object version history first page is not bounded: page=%+v err=%v", versionPage1, err)
	}
	versionPage2, err := core.ListServiceObjectVersions(ctx, admin, caseID, created.ServiceObjectID, *versionPage1.NextCursor, 1)
	if err != nil || len(versionPage2.Items) != 1 || versionPage2.Items[0].Version != 1 || versionPage2.NextCursor != nil {
		t.Fatalf("service object version history cursor is unstable: page=%+v err=%v", versionPage2, err)
	}
	associationPage1, err := core.ListServiceObjectAssociations(ctx, admin, caseID, created.ServiceObjectID, "", 1)
	if err != nil || len(associationPage1.Items) != 1 || associationPage1.Items[0].AssociationVersion != 2 || associationPage1.NextCursor == nil {
		t.Fatalf("service object association history first page is not bounded: page=%+v err=%v", associationPage1, err)
	}
	associationPage2, err := core.ListServiceObjectAssociations(ctx, admin, caseID, created.ServiceObjectID, *associationPage1.NextCursor, 1)
	if err != nil || len(associationPage2.Items) != 1 || associationPage2.Items[0].AssociationVersion != 1 || associationPage2.NextCursor != nil {
		t.Fatalf("service object association history cursor is unstable: page=%+v err=%v", associationPage2, err)
	}
	currentDetail, err := core.GetServiceObject(ctx, admin, caseID, created.ServiceObjectID)
	if err != nil || currentDetail.CurrentVersion != 2 || currentDetail.Identity.Version != 2 || currentDetail.Association == nil || currentDetail.Association.AssociationVersion != 2 || !currentDetail.CanManage {
		t.Fatalf("service object current detail does not point to current identity and association: detail=%+v err=%v", currentDetail, err)
	}
	if exactVersion, err := core.GetServiceObjectVersion(ctx, admin, caseID, created.ServiceObjectID, 1); err != nil || exactVersion.Version != 1 {
		t.Fatalf("exact historical identity lookup failed: version=%+v err=%v", exactVersion, err)
	}
	if exactAssociation, err := core.GetServiceObjectAssociation(ctx, admin, caseID, created.ServiceObjectID, 1); err != nil || exactAssociation.AssociationVersion != 1 {
		t.Fatalf("exact historical association lookup failed: association=%+v err=%v", exactAssociation, err)
	}
	if _, _, err := core.ConfirmServiceObjectAssociation(ctx, admin, caseID, created.ServiceObjectID, "service-object-stale-cas", secondConfirm); !isConflictFault(err) {
		t.Fatalf("stale association CAS succeeded: %v", err)
	}
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := env.openRole(t, "core")
	reopenedHistory, err := reopened.GetServiceObjectHistory(ctx, admin, caseID, created.ServiceObjectID)
	if err != nil || len(reopenedHistory.Versions) != 2 || len(reopenedHistory.Associations) != 2 ||
		reopenedHistory.Head.CurrentVersion != 2 || reopenedHistory.Head.CurrentAssociationVersion != 2 ||
		!bytes.Equal(reopenedHistory.Associations[0].FactsCanonicalJSON, frozenFacts) || reopenedHistory.Associations[0].FactsSHA256 != frozenFactsSHA ||
		!bytes.Equal(reopenedHistory.Associations[1].FactsCanonicalJSON, frozenFacts) || reopenedHistory.Associations[1].FactsSHA256 != frozenFactsSHA {
		t.Fatalf("reopened store changed identity or pinned bytes: history=%+v err=%v", reopenedHistory, err)
	}
}

func sha256String(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func isPostgresConstraint(err error, code, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code && pgErr.ConstraintName == constraint
}

func isServiceObjectFault(err error, status int, code string) bool {
	var apiErr *fault.Error
	return errors.As(err, &apiErr) && apiErr.Status == status && apiErr.Code == code
}
