package store

import (
	"context"
	"testing"
	"time"

	"contractaudit/internal/auditext"
	"contractaudit/internal/fulfillment"
)

func TestOperatorObservationsForEvaluationRequiresCutoffAndTemporalProof(t *testing.T) {
	businessAt := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 10, 31, 8, 0, 0, 0, time.UTC)
	validFrom := businessAt.Add(-24 * time.Hour)
	validUntil := businessAt.Add(24 * time.Hour)
	sourceAsOf := businessAt.Add(-time.Hour)
	collectedAt := cutoff.Add(-time.Minute)
	payload := "fixture://snapshot/record"
	recordVersion := "record-v1"
	observation := auditext.Observation{
		SchemaVersion: auditext.ExternalObservationSchema, SnapshotID: "snapshot-001",
		TenantID: "tenant-a", CaseID: "case-001", ConnectorID: "fixture.oss", SourceSystem: "fixture-oss",
		ObjectBindingVersion: "binding-001", ExternalObjectID: "line-001", RecordID: "line-001",
		RecordVersion: &recordVersion, ObservedAt: sourceAsOf, EffectiveFrom: validFrom, EffectiveUntil: validUntil,
		Availability: auditext.AvailabilityAvailable, ReasonCode: "read_succeeded", PayloadRef: &payload,
		Fields: []auditext.Field{{Name: "configured_bandwidth", Value: "80", Unit: "Mbit/s",
			SourcePointer: "/bandwidth", SemanticKind: "configured_value"}},
		DataClass: auditext.DataClassSynthetic,
		TemporalProof: &auditext.TemporalProof{Kind: auditext.TemporalProofHistoricalInterval,
			SourceCatalogID: "catalog-oss", DatasetVersion: "dataset-v1", RecordVersion: recordVersion,
			SourceAsOf: &sourceAsOf, ValidFrom: &validFrom, ValidUntil: &validUntil},
	}
	snapshot := OperatorSnapshotSet{CollectedAt: collectedAt, Observations: []auditext.Observation{observation}}

	got := operatorObservationsForEvaluation(snapshot, businessAt, cutoff)
	if len(got) != 1 || got[0].Availability != auditext.AvailabilityAvailable {
		t.Fatalf("valid historical evidence rejected: %+v", got)
	}
	if snapshot.Observations[0].Availability != auditext.AvailabilityAvailable || snapshot.Observations[0].PayloadRef == nil {
		t.Fatal("evaluation projection mutated the immutable collection snapshot")
	}

	cases := []struct {
		name       string
		collected  time.Time
		proof      *auditext.TemporalProof
		wantReason string
	}{
		{"collected after cutoff", cutoff.Add(time.Microsecond), observation.TemporalProof, "evidence_after_cutoff"},
		{"legacy without proof", collectedAt, nil, "historical_proof_missing"},
		{"current state cannot prove prior business time", collectedAt, &auditext.TemporalProof{
			Kind: auditext.TemporalProofCurrentState, SourceAsOf: &cutoff}, "historical_proof_missing"},
		{"source as-of after cutoff", collectedAt, &auditext.TemporalProof{
			Kind: auditext.TemporalProofHistoricalInterval, SourceCatalogID: "catalog-oss", DatasetVersion: "dataset-v1",
			RecordVersion: recordVersion, SourceAsOf: timePointer(cutoff.Add(time.Microsecond)),
			ValidFrom: &validFrom, ValidUntil: &validUntil}, "temporal_proof_unavailable"},
		{"interval does not cover business time", collectedAt, &auditext.TemporalProof{
			Kind: auditext.TemporalProofHistoricalInterval, SourceCatalogID: "catalog-oss", DatasetVersion: "dataset-v1",
			RecordVersion: recordVersion, SourceAsOf: &sourceAsOf,
			ValidFrom: timePointer(businessAt.Add(time.Hour)), ValidUntil: &validUntil}, "historical_proof_unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := observation
			candidate.TemporalProof = tc.proof
			input := OperatorSnapshotSet{CollectedAt: tc.collected, Observations: []auditext.Observation{candidate}}
			result := operatorObservationsForEvaluation(input, businessAt, cutoff)
			if len(result) != 1 || result[0].Availability != auditext.AvailabilityRejected || result[0].ReasonCode != tc.wantReason ||
				result[0].PayloadRef != nil || len(result[0].Fields) != 0 || result[0].TemporalProof != nil {
				t.Fatalf("invalid temporal evidence not reduced to an insufficient view: %+v", result)
			}
			if input.Observations[0].Availability != auditext.AvailabilityAvailable || input.Observations[0].PayloadRef == nil {
				t.Fatal("derived insufficiency rewrote the immutable source observation")
			}
		})
	}
	futureObserved := observation
	futureObserved.ObservedAt = cutoff.Add(time.Microsecond)
	futureObservedResult := operatorObservationsForEvaluation(OperatorSnapshotSet{CollectedAt: collectedAt,
		Observations: []auditext.Observation{futureObserved}}, businessAt, cutoff)
	if len(futureObservedResult) != 1 || futureObservedResult[0].Availability != auditext.AvailabilityRejected ||
		futureObservedResult[0].ReasonCode != "evidence_after_cutoff" {
		t.Fatalf("source observation after evidence cutoff was accepted: %+v", futureObservedResult)
	}
	unavailable := observation
	unavailable.Availability = auditext.AvailabilityUnavailable
	unavailable.ReasonCode = "source_unavailable"
	unavailable.PayloadRef = nil
	unavailable.Fields = nil
	unavailable.TemporalProof = nil
	preserved := operatorObservationsForEvaluation(OperatorSnapshotSet{CollectedAt: cutoff.Add(time.Hour),
		Observations: []auditext.Observation{unavailable}}, businessAt, cutoff)
	if len(preserved) != 1 || preserved[0].Availability != auditext.AvailabilityUnavailable || preserved[0].ReasonCode != "source_unavailable" {
		t.Fatalf("temporal cutoff overwrote the connector's pre-existing read failure: %+v", preserved)
	}

	withoutProof := observation
	withoutProof.TemporalProof = nil
	projected := operatorObservationsForEvaluation(OperatorSnapshotSet{CollectedAt: collectedAt,
		Observations: []auditext.Observation{withoutProof}}, businessAt, cutoff)
	quote := "合成示例：本线路最低保障带宽100Mbit/s。"
	input := auditext.FrozenInput{
		Scope: auditext.AuthorizedScope{TenantID: "tenant-a", CaseID: "case-001", ActorID: "actor-1", GrantVersion: "grant-v1"},
		Pack:  auditext.PackRef{ID: "operator.dedicated_line", Version: "1.0.0"}, ContractVersion: "doc-v1",
		RulePackVersion: "1.0.0", BindingStatus: auditext.BindingConfirmed, BindingVersion: "binding-001",
		SnapshotSetID: "snapshot-001", ExternalObjectID: "line-001", SourceSystem: "fixture-oss",
		ModelConfigurationID: "model-v1", PlannerConfigurationID: "planner-v1", ParserConfigurationID: "parser-v1",
		BusinessAt: businessAt,
		Facts: []auditext.Fact{{Name: fulfillment.ContractFieldMinimumBandwidth,
			Value: auditext.ExactQuantity{Value: "100", Unit: "Mbit/s"}, SemanticKind: "configured_value",
			Evidence: []auditext.EvidenceRef{{Kind: auditext.EvidenceDocument, DocumentVersionID: "doc-v1",
				ParsedVersionID: "parse-v1", Start: 0, End: len([]rune(quote)), Quote: quote}}}},
	}
	checks, err := fulfillment.New().Evaluate(context.Background(), input, projected)
	if err != nil || len(checks) != 1 || checks[0].Relation != auditext.RelationInsufficient || checks[0].ReasonCode != "historical_proof_missing" {
		t.Fatalf("missing historical proof did not reach the user-facing insufficient result: checks=%+v err=%v", checks, err)
	}
}

func timePointer(value time.Time) *time.Time { return &value }
