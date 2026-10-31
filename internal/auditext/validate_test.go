package auditext

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
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
	if string(raw) != `{"kind":"document","document_version_id":"doc-001","parsed_version_id":"parse-001","start":0,"end":24,"quote":"本线路最低保障带宽100Mbit/s。"}` {
		t.Fatalf("legacy document evidence JSON bytes changed without an optional source digest: %s", raw)
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
	document.SourceDigest = strings.Repeat("a", 64)
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal document evidence with source digest: %v", err)
	}
	var digestProjection EvidenceRef
	if err := json.Unmarshal(raw, &digestProjection); err != nil || digestProjection.SourceDigest != document.SourceDigest {
		t.Fatalf("document source digest did not round-trip: got=%+v err=%v", digestProjection, err)
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
	snapshotWithDocumentDigest := snapshot
	snapshotWithDocumentDigest.SourceDigest = strings.Repeat("a", 64)
	if _, err := json.Marshal(snapshotWithDocumentDigest); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("snapshot evidence carrying a document digest must be rejected, got %v", err)
	}
	if err := json.Unmarshal([]byte(`{"kind":"external_snapshot","snapshot_id":"snapshot-001","record_id":"line-001","source_digest":"`+strings.Repeat("a", 64)+`"}`), &digestProjection); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("snapshot JSON carrying a document digest must be rejected, got %v", err)
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

func TestObservationTemporalProofRequiresExactImmutableHistoricalInterval(t *testing.T) {
	scope := validScope()
	base := validObservation(scope)
	recordVersion := "record-v3"
	base.RecordVersion = &recordVersion
	validFrom := base.EffectiveFrom
	validUntil := base.EffectiveUntil
	sourceAsOf := base.ObservedAt
	base.TemporalProof = &TemporalProof{Kind: TemporalProofHistoricalInterval, SourceCatalogID: "catalog.line",
		DatasetVersion: "dataset-2026-10", RecordVersion: recordVersion, SourceAsOf: &sourceAsOf,
		ValidFrom: &validFrom, ValidUntil: &validUntil}
	if err := base.Validate(scope); err != nil {
		t.Fatalf("exact connector interval proof rejected: %v", err)
	}
	for name, mutate := range map[string]func(*Observation){
		"record version mismatch": func(o *Observation) { o.TemporalProof.RecordVersion = "record-other" },
		"validity does not match observation": func(o *Observation) {
			until := o.TemporalProof.ValidUntil.Add(time.Hour)
			o.TemporalProof.ValidUntil = &until
		},
		"missing source catalog":  func(o *Observation) { o.TemporalProof.SourceCatalogID = "" },
		"missing dataset version": func(o *Observation) { o.TemporalProof.DatasetVersion = "" },
		"missing source as-of":    func(o *Observation) { o.TemporalProof.SourceAsOf = nil },
		"empty interval":          func(o *Observation) { until := *o.TemporalProof.ValidFrom; o.TemporalProof.ValidUntil = &until },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			proof := *base.TemporalProof
			candidate.TemporalProof = &proof
			mutate(&candidate)
			if err := candidate.Validate(scope); err == nil {
				t.Fatal("invalid historical proof was accepted")
			}
		})
	}
	current := validObservation(scope)
	current.TemporalProof = &TemporalProof{Kind: TemporalProofCurrentState, SourceAsOf: &sourceAsOf}
	if err := current.Validate(scope); err != nil {
		t.Fatalf("current-state proof was rejected: %v", err)
	}
}

func TestObservationTemporalProofIsOmittedForLegacySnapshots(t *testing.T) {
	observation := validObservation(validScope())
	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"temporal_proof"`)) {
		t.Fatalf("legacy observation JSON unexpectedly gained an empty proof field: %s", encoded)
	}
}

func TestFrozenInputEvidenceCutoffIsOptionalForLegacyWire(t *testing.T) {
	legacy, err := json.Marshal(validFrozenInput())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(legacy, []byte(`"evidence_cutoff_at"`)) {
		t.Fatalf("legacy frozen input wire unexpectedly gained an empty cutoff: %s", legacy)
	}
	cutoff := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	current := validFrozenInput()
	current.EvidenceCutoffAt = &cutoff
	encoded, err := json.Marshal(current)
	if err != nil || !bytes.Contains(encoded, []byte(`"evidence_cutoff_at":"2026-10-31T09:00:00Z"`)) {
		t.Fatalf("frozen input cutoff did not use the stable optional wire field: json=%s err=%v", encoded, err)
	}
}
