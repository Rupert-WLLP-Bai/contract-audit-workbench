package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"contractaudit/internal/rulepack"
)

func TestServiceObjectWireFixtures(t *testing.T) {
	stamp := time.Date(2026, 10, 31, 8, 9, 10, 123456000, time.UTC)
	identity := ServiceObjectVersion{
		ServiceObjectID: "svc-wire-001", Version: 2, ObjectType: ServiceObjectDedicatedLine,
		DisplayName: "专线 A", BusinessCode: "LINE-A", Aliases: []ServiceObjectAlias{{Kind: "circuit", Value: "LINE-A"}},
		State: ServiceObjectConfirmed, BasisRefs: json.RawMessage(`[]`), BasisSummary: "合同已确认",
		CreatedBy: "demo-admin", CreatedAt: stamp, EffectiveFrom: stamp,
		PayloadSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	fact := rulepack.Fact{
		ID: "fact-wire-1", RuleID: "minimum-bandwidth", Dimension: "minimum_bandwidth", ObjectRef: "LINE-A",
		Value: "100", Unit: "Mbit/s", SourceRole: "draft_contract", MeasurementWindow: "calendar_month",
		TimeZone: "Asia/Shanghai", Calendar: "gregorian", OutageDefinition: "all_service_unavailability",
		Exclusions: []string{"planned_maintenance"}, Basis: "最低保障带宽为100 Mbit/s", Applicable: boolPtrWire(true),
		Citations: []rulepack.Citation{{SourceID: "doc-wire-1", SourceVersion: "parse-wire-1", TenantID: "local-demo",
			CaseID: "case-wire-1", SourceDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Start: 8, End: 28, Quote: "最低保障带宽 100 Mbit/s"}},
	}
	association := ServiceObjectContractAssociation{
		ServiceObjectID: "svc-wire-001", AssociationVersion: 1, ObjectVersion: 2,
		MaterialID: "material-wire-1", MaterialVersion: 1, DocumentID: "doc-wire-1", ParsedDocumentID: "parse-wire-1",
		SourceRunID: "run-wire-1", SourceAttemptID: "attempt-wire-1", ObjectRef: "LINE-A",
		FactRefs: []ServiceObjectFactRef{{Fact: fact, DocumentID: "doc-wire-1", ParsedDocumentID: "parse-wire-1",
			CanonicalFactSHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}},
		BasisRefs: json.RawMessage(`[]`), ConfirmedBy: "demo-admin", ConfirmedAt: stamp,
		FactsCanonicalJSON: []byte("[]"), FactsSHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		CreatedAt: stamp, PayloadSHA256: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
	}
	mutation := ServiceObjectMutationResult{ServiceObjectID: "svc-wire-001", Version: 2, AssociationVersion: 1}
	summaryPage := ServiceObjectListResult{Items: []ServiceObjectSummary{{
		ServiceObjectID: "svc-wire-001", ObjectType: ServiceObjectDedicatedLine, State: ServiceObjectConfirmed,
		CurrentVersion: 2, CurrentAssociationVersion: 1, DisplayName: "专线 A", BusinessCode: "LINE-A",
		Aliases: identity.Aliases,
		CurrentAssociation: &ServiceObjectAssociationPin{AssociationVersion: 1, ObjectVersion: 2, MaterialID: "material-wire-1",
			MaterialVersion: 1, DocumentID: "doc-wire-1", ParsedDocumentID: "parse-wire-1", SourceRunID: "run-wire-1",
			SourceAttemptID: "attempt-wire-1", ObjectRef: "LINE-A", FactsSHA256: association.FactsSHA256,
			ConfirmedBy: "demo-admin", ConfirmedAt: stamp}, CanManage: true, NeedsReassociation: false,
	}}, NextCursor: nil, CanManage: true}
	detail := ServiceObjectDetail{ServiceObjectID: "svc-wire-001", CurrentVersion: 2, Identity: identity,
		Association: &association, CanManage: true, NeedsReassociation: false}
	preview := ServiceObjectAssociationPreview{ServiceObjectID: "svc-wire-001", ExpectedObjectVersion: 2,
		ExpectedAssociationVersion: 1, RunID: "run-wire-2", AttemptID: "attempt-wire-2", MaterialID: "material-wire-2",
		MaterialVersion: 3, DocumentID: "doc-wire-2", ParsedDocumentID: "parse-wire-2",
		CandidatesSHA256: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		Options: []ServiceObjectAssociationOption{{ObjectRef: "LINE-A", Facts: []ServiceObjectFactRef{{Fact: fact,
			DocumentID: "doc-wire-2", ParsedDocumentID: "parse-wire-2", CanonicalFactSHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}},
			FactsSHA256: association.FactsSHA256, BasisRefs: json.RawMessage(`[]`)}},
	}
	wire := struct {
		MutationResult  ServiceObjectMutationResult     `json:"mutation_result"`
		SummaryPage     ServiceObjectListResult         `json:"summary_page"`
		Detail          ServiceObjectDetail             `json:"detail"`
		Preview         ServiceObjectAssociationPreview `json:"preview"`
		VersionPage     ServiceObjectVersionPage        `json:"version_page"`
		AssociationPage ServiceObjectAssociationPage    `json:"association_page"`
	}{mutation, summaryPage, detail, preview,
		ServiceObjectVersionPage{Items: []ServiceObjectVersion{identity}, NextCursor: nil},
		ServiceObjectAssociationPage{Items: []ServiceObjectContractAssociation{association}, NextCursor: nil}}
	got, err := json.MarshalIndent(wire, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("..", "..", "testdata", "contracts", "service-objects", "wire.json")
	if os.Getenv("UPDATE_SERVICE_OBJECT_WIRE_FIXTURE") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("service-object Go wire changed; update shared fixture only with an approved contract change\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func boolPtrWire(v bool) *bool { return &v }
