package store

import (
	"context"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/auditext"
	"contractaudit/internal/capabilitypack"
	"contractaudit/internal/fulfillment"
	"contractaudit/internal/reviewimpact"
)

type operatorFixedConnector struct {
	observation auditext.Observation
}

func (c operatorFixedConnector) ID() string { return c.observation.ConnectorID }

func (c operatorFixedConnector) Read(context.Context, auditext.ReadRequest) (auditext.Observation, error) {
	return c.observation, nil
}

func TestDurableOperatorAuditFlow(t *testing.T) {
	environment, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required for the operator audit flow")
	}
	admin, found := accounts.ByID("demo-admin")
	if !found {
		t.Fatal("demo admin missing")
	}
	ctx := context.Background()
	core := environment.openRole(t, "core")
	document := environment.openRole(t, "document")
	execution := environment.openRole(t, "execution")

	pack := publishSyntheticAcceptancePack(t, core, admin)
	publishSyntheticAcceptancePolicy(t, core, admin, pack)
	caseID, err := core.CreateCaseWithBusiness(admin, "专线跨系统核验", "service", "ENT", "telecom_dedicated_line")
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if _, err := document.AddDocument(ctx, admin, caseID, "draft_contract", "draft.txt", "text/plain",
		[]byte("拟签合同：电路 LINE-001 最低保障带宽 100 Mbit/s。")); err != nil {
		t.Fatalf("add draft contract: %v", err)
	}
	if _, err := document.AddDocument(ctx, admin, caseID, "procurement", "award.txt", "text/plain",
		[]byte("采购文件：电路 LINE-001 最低保障带宽 100 Mbit/s。审批上限 20%。")); err != nil {
		t.Fatalf("add procurement source: %v", err)
	}
	execution.UseDocumentPreparer(document)
	execution.UseModels(&frozenOCRModel{})
	businessAt, err := time.Parse(time.RFC3339Nano, acceptanceBusinessTime)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := execution.StartReviewAsyncAt(ctx, admin, caseID, acceptanceBusinessTime)
	if err != nil {
		t.Fatalf("accept review: %v", err)
	}
	claim, err := execution.ClaimNextJob(ctx, "operator-audit-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if claim.JobID != accepted.JobID {
		t.Fatalf("claimed %q, want %q", claim.JobID, accepted.JobID)
	}
	if err := execution.RunClaimedJob(ctx, claim); err != nil {
		t.Fatalf("run frozen review: %v", err)
	}
	detail, err := core.GetCase(admin, caseID)
	if err != nil {
		t.Fatalf("read case: %v", err)
	}
	contractID := ""
	for _, doc := range detail.Documents {
		if doc.Document.Role == "draft_contract" {
			contractID = doc.Document.ID
		}
	}
	if contractID == "" {
		t.Fatal("draft contract identity missing")
	}

	binding, err := core.CreateOperatorObjectBinding(ctx, admin, caseID, OperatorObjectBindingInput{
		ContractObject: OperatorContractObject{ObjectType: "service", ObjectID: "LINE-001", Label: strPtr("专线 LINE-001")},
		ExternalObject: OperatorExternalObject{ConnectorID: "fixture.oss", ExternalObjectID: "fixture-line-001"},
		EffectiveFrom:  businessAt.Add(-24 * time.Hour), EffectiveUntil: businessAt.Add(24 * time.Hour), Confirmed: true,
	}, "operator-binding-1")
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	if binding.Status != "confirmed" || binding.Version == "" {
		t.Fatalf("unexpected binding: %+v", binding)
	}

	payloadRef := "fixture://oss/snapshot-operator-1"
	observation := auditext.Observation{
		SchemaVersion: auditext.ExternalObservationSchema, SnapshotID: "snapshot-operator-1",
		TenantID: admin.TenantID, CaseID: caseID, ConnectorID: "fixture.oss", SourceSystem: "fixture-oss-east",
		ObjectBindingVersion: binding.Version, ExternalObjectID: "fixture-line-001", RecordID: "fixture-line-001",
		ObservedAt: businessAt.Add(-time.Hour), EffectiveFrom: businessAt.Add(-24 * time.Hour),
		EffectiveUntil: businessAt.Add(24 * time.Hour), Availability: auditext.AvailabilityAvailable,
		ReasonCode: "read_succeeded", PayloadRef: &payloadRef, DataClass: auditext.DataClassSynthetic,
		Fields: []auditext.Field{{
			Name: "configured_bandwidth", Value: "80", Unit: "Mbit/s",
			SourcePointer: "/serviceCharacteristic/0/value", SemanticKind: "configured_value",
		}},
	}
	set, err := core.CollectOperatorExternalSnapshot(ctx, admin, caseID, binding.Version, businessAt,
		"fixture.oss", "fixture-line-001", operatorFixedConnector{observation: observation}, "operator-collect-1")
	if err != nil {
		t.Fatalf("collect snapshot: %v", err)
	}
	if len(set.Observations) != 1 || set.SnapshotSetID == "" {
		t.Fatalf("unexpected snapshot set: %+v", set)
	}

	manifest := operatorManifestForRulePack(t, pack.Pack.ID, pack.Pack.Version)
	evaluation, err := core.CreateOperatorFulfillmentEvaluation(ctx, admin, caseID, OperatorEvaluationInput{
		PackID: manifest.PackID, PackVersion: manifest.Version, SourceReviewID: accepted.RunID,
		SourceAttemptID: accepted.AttemptID, ContractVersionID: contractID,
		ObjectBindingVersion: binding.Version, SnapshotSetID: set.SnapshotSetID, BusinessAt: businessAt,
	}, manifest, fulfillment.New(), "operator-evaluation-1")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(evaluation.Checks) != 1 || evaluation.Checks[0].Relation != auditext.RelationConflict {
		t.Fatalf("unexpected evaluation: %+v", evaluation.Checks)
	}
	if evaluation.Checks[0].ReasonCode != "configured_bandwidth_below_contract" || !evaluation.Checks[0].RequiresHumanReview {
		t.Fatalf("unexpected relation/review flag: %+v", evaluation.Checks[0])
	}
	var documentEvidence, snapshotEvidence bool
	for _, evidence := range evaluation.Checks[0].Evidence {
		switch evidence.Kind {
		case auditext.EvidenceDocument:
			documentEvidence = true
		case auditext.EvidenceExternalSnapshot:
			snapshotEvidence = true
		}
	}
	if !documentEvidence || !snapshotEvidence {
		t.Fatalf("evidence kinds were not kept separate: %+v", evaluation.Checks[0].Evidence)
	}

	plan, err := core.GetOperatorImpactPlan(ctx, admin, caseID, evaluation.EvaluationID,
		auditext.ImpactExplain, reviewimpact.New())
	if err != nil {
		t.Fatalf("impact plan: %v", err)
	}
	if plan.Fallback != auditext.FallbackFull || plan.DependencyComplete {
		t.Fatalf("baseline-less plan must fall back to full: %+v", plan)
	}
	for _, check := range plan.Checks {
		if check.Action != auditext.ImpactRecompute {
			t.Fatalf("baseline-less plan offered reuse: %+v", check)
		}
	}

	// A second identical evaluation is idempotent by key and returns the same
	// persisted result without creating a second evaluation row.
	replay, err := core.CreateOperatorFulfillmentEvaluation(ctx, admin, caseID, OperatorEvaluationInput{
		PackID: manifest.PackID, PackVersion: manifest.Version, SourceReviewID: accepted.RunID,
		SourceAttemptID: accepted.AttemptID, ContractVersionID: contractID,
		ObjectBindingVersion: binding.Version, SnapshotSetID: set.SnapshotSetID, BusinessAt: businessAt,
	}, manifest, fulfillment.New(), "operator-evaluation-1")
	if err != nil || replay.EvaluationID != evaluation.EvaluationID {
		t.Fatalf("idempotent replay: id=%q err=%v", replay.EvaluationID, err)
	}

	// A new evaluation over the same frozen inputs may offer a reuse candidate;
	// the business result still comes from the full path.
	time.Sleep(5 * time.Millisecond)
	evaluation2, err := core.CreateOperatorFulfillmentEvaluation(ctx, admin, caseID, OperatorEvaluationInput{
		PackID: manifest.PackID, PackVersion: manifest.Version, SourceReviewID: accepted.RunID,
		SourceAttemptID: accepted.AttemptID, ContractVersionID: contractID,
		ObjectBindingVersion: binding.Version, SnapshotSetID: set.SnapshotSetID, BusinessAt: businessAt,
	}, manifest, fulfillment.New(), "operator-evaluation-2")
	if err != nil {
		t.Fatalf("second evaluation: %v", err)
	}
	plan2, err := core.GetOperatorImpactPlan(ctx, admin, caseID, evaluation2.EvaluationID,
		auditext.ImpactShadow, reviewimpact.New())
	if err != nil {
		t.Fatalf("second impact plan: %v", err)
	}
	if plan2.Fallback != auditext.FallbackNone || !plan2.DependencyComplete || plan2.BusinessResultSource != "full" {
		t.Fatalf("identical input plan must not fall back: %+v", plan2)
	}
	if len(plan2.Checks) != 1 || plan2.Checks[0].Action != auditext.ImpactReuseCandidate {
		t.Fatalf("identical input plan did not offer a candidate: %+v", plan2.Checks)
	}
}

func operatorManifestForRulePack(t *testing.T, rulePackID, rulePackVersion string) capabilitypack.Manifest {
	t.Helper()
	connector := "fixture.oss"
	externalField := "configured_bandwidth"
	manifest := capabilitypack.Manifest{
		SchemaVersion:       auditext.CapabilityPackSchemaVersion,
		PackID:              "operator.dedicated_line",
		Version:             "1.0.0",
		HostContractVersion: auditext.HostContractVersion,
		DisplayName:         "专线配置核验（测试）",
		Scenario:            capabilitypack.ScenarioTelecomDedicatedLine,
		RequiredMaterialRoles: []string{
			"draft_contract", "procurement",
		},
		RulePackRef: capabilitypack.RulePackRef{ID: rulePackID, Version: rulePackVersion},
		Capabilities: []capabilitypack.Capability{{
			ID: "operator.oss.read_config", Effect: capabilitypack.EffectRead, ConnectorRef: &connector,
		}},
		Checks: []capabilitypack.Check{{
			ID: "line.configured_bandwidth", ComparatorRef: "builtin.configured_bandwidth_at_least/v1",
			ContractField: "minimum_bandwidth", ExternalField: &externalField,
		}},
		UI: capabilitypack.UI{
			View: capabilitypack.ViewFieldComparison,
			Fields: []string{
				"minimum_bandwidth", "configured_bandwidth", "observed_at",
			},
		},
		FixtureSuite: "operatoraudit.dedicated_line.v1",
		DataClass:    auditext.DataClassSynthetic,
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("test capability manifest: %v", err)
	}
	return manifest
}

func strPtr(value string) *string { return &value }
