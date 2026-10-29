package fulfillment_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"contractaudit/internal/auditext"
	"contractaudit/internal/fulfillment"
)

var businessAt = time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)

func validScope() auditext.AuthorizedScope {
	return auditext.AuthorizedScope{
		TenantID:     "tenant-a",
		CaseID:       "case-001",
		ActorID:      "actor-1",
		GrantVersion: "grant-v1",
	}
}

func strPtr(value string) *string { return &value }

func documentEvidence(value string) auditext.EvidenceRef {
	return auditext.EvidenceRef{
		Kind:              auditext.EvidenceDocument,
		DocumentVersionID: "doc-v1",
		ParsedVersionID:   "parse-v1",
		Start:             0,
		End:               40,
		Quote:             "合成示例：本线路最低保障带宽" + value + "Mbit/s。",
	}
}

func validInput() auditext.FrozenInput {
	return auditext.FrozenInput{
		Scope:                  validScope(),
		Pack:                   auditext.PackRef{ID: "operator.dedicated_line", Version: "1.0.0"},
		ContractVersion:        "doc-v1",
		RulePackVersion:        "1.0.0",
		BindingStatus:          auditext.BindingConfirmed,
		BindingVersion:         "binding-v1",
		SnapshotSetID:          "set-v1",
		ExternalObjectID:       "line-001",
		SourceSystem:           "oss-east",
		ModelConfigurationID:   "model-v1",
		PlannerConfigurationID: "planner-v1",
		ParserConfigurationID:  "parser-v1",
		BusinessAt:             businessAt,
		Facts: []auditext.Fact{{
			Name:         fulfillment.ContractFieldMinimumBandwidth,
			Value:        auditext.ExactQuantity{Value: "100", Unit: "Mbit/s"},
			Evidence:     []auditext.EvidenceRef{documentEvidence("100")},
			SemanticKind: "configured_value",
		}},
	}
}

func validObservation() auditext.Observation {
	return auditext.Observation{
		SchemaVersion:        auditext.ExternalObservationSchema,
		SnapshotID:           "snap-001",
		TenantID:             "tenant-a",
		CaseID:               "case-001",
		ConnectorID:          "connector-1",
		SourceSystem:         "oss-east",
		ObjectBindingVersion: "binding-v1",
		ExternalObjectID:     "line-001",
		RecordID:             "record-001",
		RecordVersion:        strPtr("rev-1"),
		ObservedAt:           businessAt,
		EffectiveFrom:        time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		EffectiveUntil:       time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		Availability:         auditext.AvailabilityAvailable,
		ReasonCode:           "read_succeeded",
		PayloadRef:           strPtr("fixture://oss/record-001.json"),
		Fields: []auditext.Field{{
			Name:          fulfillment.ExternalFieldConfiguredBandwidth,
			Value:         "80",
			Unit:          "Mbit/s",
			SourcePointer: "/serviceCharacteristic/0/value",
			SemanticKind:  "configured_value",
		}},
		DataClass: auditext.DataClassSynthetic,
	}
}

func defaultObservations() []auditext.Observation {
	return []auditext.Observation{validObservation()}
}

func requireResult(t *testing.T, results []auditext.CheckResult, err error, relation auditext.Relation, reason string) auditext.CheckResult {
	t.Helper()
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	result := results[0]
	if result.CheckID != fulfillment.CheckConfiguredBandwidth {
		t.Fatalf("check id = %q", result.CheckID)
	}
	if result.Relation != relation || result.ReasonCode != reason {
		t.Fatalf("got relation=%q reason=%q, want relation=%q reason=%q", result.Relation, result.ReasonCode, relation, reason)
	}
	if result.MachineStatus != auditext.MachineCompleted {
		t.Fatalf("machine status = %q, want completed", result.MachineStatus)
	}
	if !result.RequiresHumanReview {
		t.Fatal("configuration comparison must always require human review")
	}
	if !result.Dependencies.Complete {
		t.Fatal("dependency set must be declared complete")
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("check result does not validate: %v", err)
	}
	return result
}

func TestEvaluateGoldenCases(t *testing.T) {
	tests := []struct {
		id           string
		mutateInput  func(*auditext.FrozenInput)
		observations func() []auditext.Observation
		wantRelation auditext.Relation
		wantReason   string
	}{
		{
			id:           "G01-config-below-contract",
			wantRelation: auditext.RelationConflict,
			wantReason:   "configured_bandwidth_below_contract",
		},
		{
			id: "G02-config-equals-contract",
			observations: func() []auditext.Observation {
				o := validObservation()
				o.Fields[0].Value = "100"
				return []auditext.Observation{o}
			},
			wantRelation: auditext.RelationCompatible,
			wantReason:   "configured_bandwidth_meets_contract",
		},
		{
			id: "G03-unit-normalization",
			observations: func() []auditext.Observation {
				o := validObservation()
				o.Fields[0].Value = "0.1"
				o.Fields[0].Unit = "Gbit/s"
				return []auditext.Observation{o}
			},
			wantRelation: auditext.RelationCompatible,
			wantReason:   "configured_bandwidth_meets_contract",
		},
		{
			id: "G04-binding-missing",
			mutateInput: func(input *auditext.FrozenInput) {
				input.BindingStatus = auditext.BindingMissing
				input.BindingVersion = ""
				input.ExternalObjectID = ""
				input.SourceSystem = ""
			},
			wantRelation: auditext.RelationInsufficient,
			wantReason:   "object_binding_missing",
		},
		{
			id: "G05-binding-ambiguous",
			mutateInput: func(input *auditext.FrozenInput) {
				input.BindingStatus = auditext.BindingAmbiguous
				input.BindingVersion = ""
				input.ExternalObjectID = ""
				input.SourceSystem = ""
			},
			wantRelation: auditext.RelationInsufficient,
			wantReason:   "object_binding_ambiguous",
		},
		{
			id: "G06-source-stale",
			observations: func() []auditext.Observation {
				o := validObservation()
				o.Availability = auditext.AvailabilityStale
				o.ReasonCode = "snapshot_too_old"
				return []auditext.Observation{o}
			},
			wantRelation: auditext.RelationInsufficient,
			wantReason:   "source_stale",
		},
		{
			id: "G07-source-unavailable",
			observations: func() []auditext.Observation {
				o := validObservation()
				o.Availability = auditext.AvailabilityUnavailable
				o.ReasonCode = "connector_timeout"
				o.PayloadRef = nil
				o.Fields = nil
				return []auditext.Observation{o}
			},
			wantRelation: auditext.RelationInsufficient,
			wantReason:   "source_unavailable",
		},
		{
			id: "G08-required-field-missing",
			observations: func() []auditext.Observation {
				o := validObservation()
				o.Fields = []auditext.Field{{
					Name:          "unrelated_field",
					Value:         "1",
					Unit:          "Mbit/s",
					SourcePointer: "/serviceCharacteristic/1/value",
					SemanticKind:  "configured_value",
				}}
				return []auditext.Observation{o}
			},
			wantRelation: auditext.RelationInsufficient,
			wantReason:   "required_field_missing",
		},
		{
			id: "G09-business-time-outside-interval",
			mutateInput: func(input *auditext.FrozenInput) {
				input.BusinessAt = time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)
			},
			wantRelation: auditext.RelationInsufficient,
			wantReason:   "effective_time_mismatch",
		},
		{
			id: "G10-contract-fact-uncertain",
			mutateInput: func(input *auditext.FrozenInput) {
				input.Facts[0].Uncertain = true
			},
			wantRelation: auditext.RelationInsufficient,
			wantReason:   "contract_fact_uncertain",
		},
		{
			id: "G12a-contract-fact-measurement-semantics",
			mutateInput: func(input *auditext.FrozenInput) {
				input.Facts[0].SemanticKind = "measured_throughput"
			},
			wantRelation: auditext.RelationInsufficient,
			wantReason:   "measurement_semantics_mismatch",
		},
		{
			id: "G12b-external-field-measurement-semantics",
			observations: func() []auditext.Observation {
				o := validObservation()
				o.Fields[0].SemanticKind = "measured_throughput"
				return []auditext.Observation{o}
			},
			wantRelation: auditext.RelationInsufficient,
			wantReason:   "measurement_semantics_mismatch",
		},
		{
			id: "G13-external-object-mismatch",
			observations: func() []auditext.Observation {
				o := validObservation()
				o.ExternalObjectID = "line-002"
				return []auditext.Observation{o}
			},
			wantRelation: auditext.RelationInsufficient,
			wantReason:   "external_object_mismatch",
		},
		{
			id: "G14-evidence-field-mismatch",
			mutateInput: func(input *auditext.FrozenInput) {
				input.Facts[0].Evidence = []auditext.EvidenceRef{documentEvidence("200")}
			},
			wantRelation: auditext.RelationInsufficient,
			wantReason:   "evidence_field_mismatch",
		},
		{
			id: "G15-future-observation",
			observations: func() []auditext.Observation {
				o := validObservation()
				o.ObservedAt = businessAt.Add(time.Hour)
				return []auditext.Observation{o}
			},
			wantRelation: auditext.RelationInsufficient,
			wantReason:   "observation_time_mismatch",
		},
		{
			id: "G16-proven-out-of-scope",
			mutateInput: func(input *auditext.FrozenInput) {
				input.Facts[0].OutOfScope = true
			},
			wantRelation: auditext.RelationNotApplicable,
			wantReason:   "proven_out_of_scope",
		},
	}

	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			input := validInput()
			if test.mutateInput != nil {
				test.mutateInput(&input)
			}
			observations := defaultObservations()
			if test.observations != nil {
				observations = test.observations()
			}
			results, err := fulfillment.New().Evaluate(context.Background(), input, observations)
			requireResult(t, results, err, test.wantRelation, test.wantReason)
		})
	}
}

func TestEvaluateAdditionalInsufficientReasons(t *testing.T) {
	tests := []struct {
		id           string
		mutateInput  func(*auditext.FrozenInput)
		observations func() []auditext.Observation
		wantReason   string
	}{
		{
			id: "contract-fact-missing",
			mutateInput: func(input *auditext.FrozenInput) {
				input.Facts = nil
			},
			wantReason: "contract_fact_missing",
		},
		{
			id: "contract-evidence-missing",
			mutateInput: func(input *auditext.FrozenInput) {
				input.Facts[0].Evidence = nil
			},
			wantReason: "contract_evidence_missing",
		},
		{
			id:           "observation-missing",
			observations: func() []auditext.Observation { return nil },
			wantReason:   "observation_missing",
		},
		{
			id: "observation-ambiguous",
			observations: func() []auditext.Observation {
				first := validObservation()
				second := validObservation()
				second.SnapshotID = "snap-002"
				second.RecordID = "record-002"
				return []auditext.Observation{first, second}
			},
			wantReason: "observation_ambiguous",
		},
		{
			id: "object-binding-version-mismatch",
			observations: func() []auditext.Observation {
				o := validObservation()
				o.ObjectBindingVersion = "binding-v2"
				return []auditext.Observation{o}
			},
			wantReason: "object_binding_mismatch",
		},
		{
			id: "source-system-mismatch",
			observations: func() []auditext.Observation {
				o := validObservation()
				o.SourceSystem = "oss-west"
				return []auditext.Observation{o}
			},
			wantReason: "source_system_mismatch",
		},
		{
			id: "required-field-invalid-value",
			observations: func() []auditext.Observation {
				o := validObservation()
				o.Fields[0].Value = "not-a-number"
				return []auditext.Observation{o}
			},
			wantReason: "required_field_invalid",
		},
		{
			id: "required-field-invalid-unit",
			observations: func() []auditext.Observation {
				o := validObservation()
				o.Fields[0].Unit = "Kbit/s"
				return []auditext.Observation{o}
			},
			wantReason: "required_field_invalid",
		},
		{
			id: "contract-unit-invalid",
			mutateInput: func(input *auditext.FrozenInput) {
				input.Facts[0].Value.Unit = "Mbps"
			},
			wantReason: "required_field_invalid",
		},
		{
			id: "rejected-observation-keeps-source-reason",
			observations: func() []auditext.Observation {
				o := validObservation()
				o.Availability = auditext.AvailabilityRejected
				o.ReasonCode = "source_rejected"
				o.Fields = nil
				return []auditext.Observation{o}
			},
			wantReason: "source_rejected",
		},
	}

	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			input := validInput()
			if test.mutateInput != nil {
				test.mutateInput(&input)
			}
			observations := defaultObservations()
			if test.observations != nil {
				observations = test.observations()
			}
			results, err := fulfillment.New().Evaluate(context.Background(), input, observations)
			requireResult(t, results, err, auditext.RelationInsufficient, test.wantReason)
		})
	}
}

func TestEvaluateExactDecimalComparison(t *testing.T) {
	tests := []struct {
		name         string
		value        string
		unit         string
		wantRelation auditext.Relation
	}{
		{name: "equal-trailing-zeros", value: "100.000", unit: "Mbit/s", wantRelation: auditext.RelationCompatible},
		{name: "one-thousandth-below", value: "99.999", unit: "Mbit/s", wantRelation: auditext.RelationConflict},
		{name: "gbit-exact", value: "0.1", unit: "Gbit/s", wantRelation: auditext.RelationCompatible},
		{name: "gbit-below", value: "0.099", unit: "Gbit/s", wantRelation: auditext.RelationConflict},
		{name: "gbit-three-decimals", value: "0.100", unit: "Gbit/s", wantRelation: auditext.RelationCompatible},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validInput()
			observation := validObservation()
			observation.Fields[0].Value = test.value
			observation.Fields[0].Unit = test.unit
			results, err := fulfillment.New().Evaluate(context.Background(), input, []auditext.Observation{observation})
			reason := "configured_bandwidth_below_contract"
			if test.wantRelation == auditext.RelationCompatible {
				reason = "configured_bandwidth_meets_contract"
			}
			requireResult(t, results, err, test.wantRelation, reason)
		})
	}
}

func TestEvaluateGigabitContractFact(t *testing.T) {
	input := validInput()
	input.Facts[0].Value = auditext.ExactQuantity{Value: "0.1", Unit: "Gbit/s"}
	input.Facts[0].Evidence = []auditext.EvidenceRef{documentEvidence("0.1")}
	observation := validObservation()
	observation.Fields[0].Value = "100"

	results, err := fulfillment.New().Evaluate(context.Background(), input, []auditext.Observation{observation})
	result := requireResult(t, results, err, auditext.RelationCompatible, "configured_bandwidth_meets_contract")
	if len(result.Evidence) != 2 {
		t.Fatalf("evidence count = %d, want 2", len(result.Evidence))
	}
}

func TestEvaluateEvidenceKeepsDocumentAndSnapshotSeparate(t *testing.T) {
	results, err := fulfillment.New().Evaluate(context.Background(), validInput(), defaultObservations())
	result := requireResult(t, results, err, auditext.RelationConflict, "configured_bandwidth_below_contract")

	if len(result.Evidence) != 2 {
		t.Fatalf("evidence count = %d, want 2", len(result.Evidence))
	}
	document := result.Evidence[0]
	if document.Kind != auditext.EvidenceDocument {
		t.Fatalf("first evidence kind = %q, want document", document.Kind)
	}
	if document.DocumentVersionID != "doc-v1" || document.Quote == "" {
		t.Fatalf("document evidence lost its real citation: %+v", document)
	}

	snapshot := result.Evidence[1]
	if snapshot.Kind != auditext.EvidenceExternalSnapshot {
		t.Fatalf("second evidence kind = %q, want external_snapshot", snapshot.Kind)
	}
	if snapshot.SnapshotID != "snap-001" || snapshot.RecordID != "record-001" {
		t.Fatalf("snapshot evidence identity mismatch: %+v", snapshot)
	}
	if snapshot.FieldPointer != "/serviceCharacteristic/0/value" {
		t.Fatalf("field pointer = %q", snapshot.FieldPointer)
	}
	if snapshot.DisplayValue != "80 Mbit/s" {
		t.Fatalf("display value = %q", snapshot.DisplayValue)
	}
	if !snapshot.ObservedAt.Equal(businessAt) {
		t.Fatalf("observed at = %v", snapshot.ObservedAt)
	}
	if snapshot.Quote != "" || snapshot.DocumentVersionID != "" || snapshot.ParsedVersionID != "" {
		t.Fatal("external snapshot evidence must not carry a synthetic document citation")
	}
}

func mustDependency(t *testing.T, set auditext.DependencySet, kind auditext.DependencyKind, ref string) auditext.Dependency {
	t.Helper()
	for _, item := range set.Items {
		if item.Kind == kind && item.Ref == ref {
			return item
		}
	}
	t.Fatalf("dependency %s/%s not declared in %+v", kind, ref, set.Items)
	return auditext.Dependency{}
}

func TestEvaluateDeclaresDependencies(t *testing.T) {
	results, err := fulfillment.New().Evaluate(context.Background(), validInput(), defaultObservations())
	result := requireResult(t, results, err, auditext.RelationConflict, "configured_bandwidth_below_contract")

	dependencies := result.Dependencies
	if !dependencies.Complete {
		t.Fatal("dependencies must be complete")
	}
	if len(dependencies.Items) != 9 {
		t.Fatalf("dependency count = %d, want 9", len(dependencies.Items))
	}
	if err := auditext.ValidateDependencySet(dependencies); err != nil {
		t.Fatalf("dependency set does not validate: %v", err)
	}

	field := mustDependency(t, dependencies, auditext.DependencyField, "external.configured_bandwidth")
	if !field.Present || field.Version != "rev-1" {
		t.Fatalf("field dependency = %+v, want present with record version rev-1", field)
	}
	recordSet := mustDependency(t, dependencies, auditext.DependencyRecordSet, "external.records")
	if recordSet.Version != "set-v1" {
		t.Fatalf("record set version = %q", recordSet.Version)
	}
	mapping := mustDependency(t, dependencies, auditext.DependencyMapping, "object.binding")
	if !mapping.Present || mapping.Version != "binding-v1" {
		t.Fatalf("mapping dependency = %+v", mapping)
	}
	rulePack := mustDependency(t, dependencies, auditext.DependencyRulePack, "rule_pack")
	if rulePack.Version != "1.0.0" {
		t.Fatalf("rule pack version = %q", rulePack.Version)
	}
	capabilityPack := mustDependency(t, dependencies, auditext.DependencyCapabilityPack, "capability_pack")
	if capabilityPack.Version != "1.0.0" {
		t.Fatalf("capability pack version = %q", capabilityPack.Version)
	}
	document := mustDependency(t, dependencies, auditext.DependencyDocument, "doc-v1")
	if !document.Present {
		t.Fatalf("document dependency = %+v", document)
	}
	parser := mustDependency(t, dependencies, auditext.DependencyParser, "parser")
	if parser.Version != "parser-v1" {
		t.Fatalf("parser version = %q", parser.Version)
	}
	model := mustDependency(t, dependencies, auditext.DependencyModel, "model")
	if model.Version != "model-v1" {
		t.Fatalf("model version = %q", model.Version)
	}
	policy := mustDependency(t, dependencies, auditext.DependencyPolicy, "policy")
	if policy.Version != "planner-v1" {
		t.Fatalf("policy version = %q", policy.Version)
	}
}

func TestEvaluateMissingFieldStillDeclaresNegativeRead(t *testing.T) {
	observation := validObservation()
	observation.Fields = []auditext.Field{{
		Name:          "unrelated_field",
		Value:         "1",
		Unit:          "Mbit/s",
		SourcePointer: "/serviceCharacteristic/1/value",
		SemanticKind:  "configured_value",
	}}
	results, err := fulfillment.New().Evaluate(context.Background(), validInput(), []auditext.Observation{observation})
	result := requireResult(t, results, err, auditext.RelationInsufficient, "required_field_missing")

	field := mustDependency(t, result.Dependencies, auditext.DependencyField, "external.configured_bandwidth")
	if field.Present {
		t.Fatal("missing field must be recorded as an absent read")
	}
	if field.Version != "rev-1" {
		t.Fatalf("record version = %q, want rev-1", field.Version)
	}
}

func TestEvaluateNoObservationStillDeclaresDependencies(t *testing.T) {
	results, err := fulfillment.New().Evaluate(context.Background(), validInput(), nil)
	result := requireResult(t, results, err, auditext.RelationInsufficient, "observation_missing")

	if len(result.Dependencies.Items) != 9 || !result.Dependencies.Complete {
		t.Fatalf("dependencies = %+v", result.Dependencies)
	}
	field := mustDependency(t, result.Dependencies, auditext.DependencyField, "external.configured_bandwidth")
	if field.Present || field.Version != "" {
		t.Fatalf("field dependency = %+v, want absent without a record version", field)
	}
}

func TestEvaluateErrors(t *testing.T) {
	t.Run("unsupported pack", func(t *testing.T) {
		input := validInput()
		input.Pack.ID = "operator.cloud_services"
		_, err := fulfillment.New().Evaluate(context.Background(), input, defaultObservations())
		if !errors.Is(err, auditext.ErrUnsupported) {
			t.Fatalf("error = %v, want ErrUnsupported", err)
		}
	})

	t.Run("nil context", func(t *testing.T) {
		_, err := fulfillment.New().Evaluate(nil, validInput(), defaultObservations())
		if !errors.Is(err, auditext.ErrInvalidRequest) {
			t.Fatalf("error = %v, want ErrInvalidRequest", err)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := fulfillment.New().Evaluate(ctx, validInput(), defaultObservations())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})

	t.Run("invalid input", func(t *testing.T) {
		input := validInput()
		input.BusinessAt = time.Time{}
		_, err := fulfillment.New().Evaluate(context.Background(), input, defaultObservations())
		if !errors.Is(err, auditext.ErrInvalidRequest) {
			t.Fatalf("error = %v, want ErrInvalidRequest", err)
		}
	})

	t.Run("invalid scope", func(t *testing.T) {
		input := validInput()
		input.Scope.TenantID = ""
		_, err := fulfillment.New().Evaluate(context.Background(), input, defaultObservations())
		if !errors.Is(err, auditext.ErrUnauthorized) {
			t.Fatalf("error = %v, want ErrUnauthorized", err)
		}
	})

	t.Run("observation tenant mismatch", func(t *testing.T) {
		observation := validObservation()
		observation.TenantID = "tenant-b"
		_, err := fulfillment.New().Evaluate(context.Background(), validInput(), []auditext.Observation{observation})
		if !errors.Is(err, auditext.ErrUnauthorized) {
			t.Fatalf("error = %v, want ErrUnauthorized", err)
		}
	})

	t.Run("observation case mismatch", func(t *testing.T) {
		observation := validObservation()
		observation.CaseID = "case-002"
		_, err := fulfillment.New().Evaluate(context.Background(), validInput(), []auditext.Observation{observation})
		if !errors.Is(err, auditext.ErrUnauthorized) {
			t.Fatalf("error = %v, want ErrUnauthorized", err)
		}
	})
}

func TestEvaluateInvalidAvailabilityIsRejected(t *testing.T) {
	observation := validObservation()
	observation.Availability = auditext.Availability("unknown")
	_, err := fulfillment.New().Evaluate(context.Background(), validInput(), []auditext.Observation{observation})
	if !errors.Is(err, auditext.ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
}

func TestEvaluateMultipleNonMatchingObservationsAreAmbiguous(t *testing.T) {
	first := validObservation()
	first.SnapshotID = "snap-002"
	first.RecordID = "record-002"
	first.ExternalObjectID = "line-002"
	second := validObservation()
	second.SnapshotID = "snap-003"
	second.RecordID = "record-003"
	second.ExternalObjectID = "line-003"

	results, err := fulfillment.New().Evaluate(context.Background(), validInput(), []auditext.Observation{first, second})
	requireResult(t, results, err, auditext.RelationInsufficient, "observation_ambiguous")
}

func TestEvaluateEmptyFieldValueIsInvalid(t *testing.T) {
	observation := validObservation()
	observation.Fields[0].Value = ""
	results, err := fulfillment.New().Evaluate(context.Background(), validInput(), []auditext.Observation{observation})
	requireResult(t, results, err, auditext.RelationInsufficient, "required_field_invalid")
}

func TestEvaluateEvidenceLimitKeepsSnapshotEvidence(t *testing.T) {
	input := validInput()
	evidence := make([]auditext.EvidenceRef, 16)
	for index := range evidence {
		evidence[index] = documentEvidence("100")
		evidence[index].Start = index
		evidence[index].End = index + 1
	}
	input.Facts[0].Evidence = evidence

	results, err := fulfillment.New().Evaluate(context.Background(), input, defaultObservations())
	result := requireResult(t, results, err, auditext.RelationConflict, "configured_bandwidth_below_contract")

	if len(result.Evidence) != 16 {
		t.Fatalf("evidence count = %d, want 16", len(result.Evidence))
	}
	last := result.Evidence[len(result.Evidence)-1]
	if last.Kind != auditext.EvidenceExternalSnapshot {
		t.Fatalf("last evidence kind = %q, want external_snapshot", last.Kind)
	}
}

func TestEvaluateNeverAutoConfirmsRisk(t *testing.T) {
	results, err := fulfillment.New().Evaluate(context.Background(), validInput(), defaultObservations())
	result := requireResult(t, results, err, auditext.RelationConflict, "configured_bandwidth_below_contract")
	if !result.RequiresHumanReview {
		t.Fatal("a below-contract configuration must require human review")
	}
	if len(result.Limitations) == 0 {
		t.Fatal("limitations must describe what the comparison does not prove")
	}
}
