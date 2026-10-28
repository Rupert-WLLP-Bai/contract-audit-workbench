package auditext

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func validScope() AuthorizedScope {
	return AuthorizedScope{TenantID: "tenant-a", CaseID: "case-001", ActorID: "user-001", GrantVersion: "grant-001"}
}

func validObservation(scope AuthorizedScope) Observation {
	payload := "fixture://oss/snapshot-001"
	return Observation{
		SchemaVersion:        ExternalObservationSchema,
		SnapshotID:           "snapshot-001",
		TenantID:             scope.TenantID,
		CaseID:               scope.CaseID,
		ConnectorID:          "fixture.oss",
		SourceSystem:         "fixture-oss-east",
		ObjectBindingVersion: "binding-001",
		ExternalObjectID:     "line-001",
		RecordID:             "line-001",
		ObservedAt:           time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC),
		EffectiveFrom:        time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		EffectiveUntil:       time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		Availability:         AvailabilityAvailable,
		ReasonCode:           "read_succeeded",
		PayloadRef:           &payload,
		Fields: []Field{{
			Name:          "configured_bandwidth",
			Value:         "80",
			Unit:          "Mbit/s",
			SourcePointer: "/serviceCharacteristic/0/value",
			SemanticKind:  "configured_value",
		}},
		DataClass: DataClassSynthetic,
	}
}

func TestObservationValidate(t *testing.T) {
	scope := validScope()
	observation := validObservation(scope)
	if err := observation.Validate(scope); err != nil {
		t.Fatalf("valid observation rejected: %v", err)
	}

	otherTenant := observation
	otherTenant.TenantID = "tenant-b"
	if err := otherTenant.Validate(scope); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("tenant mismatch must be unauthorized, got %v", err)
	}

	missingPayload := observation
	missingPayload.PayloadRef = nil
	if err := missingPayload.Validate(scope); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("available observation without payload must be invalid, got %v", err)
	}

	stale := observation
	stale.Availability = AvailabilityStale
	stale.ReasonCode = "source_stale"
	stale.Fields = nil
	stale.PayloadRef = nil
	if err := stale.Validate(scope); err != nil {
		t.Fatalf("stale observation without fields must remain valid: %v", err)
	}

	mixedEvidence := observation
	mixedEvidence.Fields = []Field{{
		Name:          "configured_bandwidth",
		Value:         "80",
		Unit:          "Mbit/s",
		SourcePointer: "serviceCharacteristic/0/value",
		SemanticKind:  "configured_value",
	}}
	if err := mixedEvidence.Validate(scope); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("pointer without leading slash must be invalid, got %v", err)
	}
}

func TestFrozenInputValidate(t *testing.T) {
	fact := Fact{
		Name:         "minimum_bandwidth",
		Value:        ExactQuantity{Value: "100", Unit: "Mbit/s"},
		SemanticKind: "configured_value",
		Evidence: []EvidenceRef{{
			Kind:              EvidenceDocument,
			DocumentVersionID: "doc-001",
			ParsedVersionID:   "parse-001",
			Start:             0,
			End:               24,
			Quote:             "本线路最低保障带宽100Mbit/s。",
		}},
	}
	input := FrozenInput{
		Scope:                  validScope(),
		Pack:                   PackRef{ID: "operator.dedicated_line", Version: "1.0.0"},
		ContractVersion:        "doc-001",
		RulePackVersion:        "5",
		BindingStatus:          BindingConfirmed,
		BindingVersion:         "binding-001",
		SnapshotSetID:          "set-001",
		ExternalObjectID:       "line-001",
		SourceSystem:           "fixture-oss-east",
		ModelConfigurationID:   "model-001",
		PlannerConfigurationID: "planner-001",
		ParserConfigurationID:  "parser-001",
		BusinessAt:             time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC),
		Facts:                  []Fact{fact},
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("valid frozen input rejected: %v", err)
	}

	missingBinding := input
	missingBinding.BindingStatus = BindingMissing
	missingBinding.BindingVersion = ""
	missingBinding.ExternalObjectID = ""
	missingBinding.SourceSystem = ""
	if err := missingBinding.Validate(); err != nil {
		t.Fatalf("missing binding input rejected: %v", err)
	}

	confirmedWithoutObject := input
	confirmedWithoutObject.ExternalObjectID = ""
	if err := confirmedWithoutObject.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("confirmed binding without object must be invalid, got %v", err)
	}

	duplicateFact := input
	duplicateFact.Facts = append(duplicateFact.Facts, fact)
	if err := duplicateFact.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("duplicate fact must be invalid, got %v", err)
	}
}

func TestEvidenceRefMarshalJSON(t *testing.T) {
	document := EvidenceRef{
		Kind:              EvidenceDocument,
		DocumentVersionID: "doc-001",
		ParsedVersionID:   "parse-001",
		Start:             0,
		End:               24,
		Quote:             "本线路最低保障带宽100Mbit/s。",
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal document evidence: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode document evidence: %v", err)
	}
	for _, key := range []string{"kind", "document_version_id", "parsed_version_id", "start", "end", "quote"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("document evidence missing %q: %s", key, raw)
		}
	}
	for _, key := range []string{"snapshot_id", "record_id", "field_pointer", "display_value", "observed_at"} {
		if _, ok := fields[key]; ok {
			t.Fatalf("document evidence unexpectedly contains %q: %s", key, raw)
		}
	}

	snapshot := EvidenceRef{
		Kind:         EvidenceExternalSnapshot,
		SnapshotID:   "snapshot-001",
		RecordID:     "line-001",
		FieldPointer: "/serviceCharacteristic/0/value",
		DisplayValue: "80 Mbit/s",
		ObservedAt:   time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC),
	}
	raw, err = json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot evidence: %v", err)
	}
	fields = nil
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode snapshot evidence: %v", err)
	}
	for _, key := range []string{"kind", "snapshot_id", "record_id", "field_pointer", "display_value", "observed_at"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("snapshot evidence missing %q: %s", key, raw)
		}
	}
	for _, key := range []string{"document_version_id", "parsed_version_id", "start", "end", "quote"} {
		if _, ok := fields[key]; ok {
			t.Fatalf("snapshot evidence unexpectedly contains %q: %s", key, raw)
		}
	}

	if _, err := json.Marshal(EvidenceRef{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty evidence kind must be rejected, got %v", err)
	}
}

func TestImpactPlanValidate(t *testing.T) {
	plan := ImpactPlan{
		SchemaVersion:        ImpactPlanSchema,
		BaseEvaluationID:     "eval-001",
		TargetEvaluationID:   "eval-002",
		Mode:                 ImpactExplain,
		DependencyComplete:   true,
		Fallback:             FallbackNone,
		ChangedInputs:        []string{"external.configured_bandwidth"},
		Checks:               []ImpactCheck{{CheckID: "line.configured_bandwidth", Action: ImpactRecompute, Reasons: []string{"dependent_field_changed"}}},
		BusinessResultSource: "full",
		DataClass:            DataClassSynthetic,
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("valid impact plan rejected: %v", err)
	}

	incompleteWithoutFallback := plan
	incompleteWithoutFallback.DependencyComplete = false
	if err := incompleteWithoutFallback.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("incomplete plan without fallback must be invalid, got %v", err)
	}

	badSource := plan
	badSource.BusinessResultSource = "shadow"
	if err := badSource.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("business result source other than full must be invalid, got %v", err)
	}
}
