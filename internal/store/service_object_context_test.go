package store

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/audit"
	"contractaudit/internal/rulepack"
)

func TestServiceObjectContextSnapshotCodecAndLegacyCompatibility(t *testing.T) {
	context, err := planServiceObjectReviewContext(time.Date(2026, 10, 31, 1, 2, 3, 456000, time.UTC), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := RunSnapshot{ServiceObjectContext: &context, ServiceObjectContextHash: context.SHA256}
	if err := validateServiceObjectReviewContext(snapshot); err != nil {
		t.Fatalf("valid context rejected: %v", err)
	}
	payload, err := encodeServiceObjectReviewContext(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeServiceObjectReviewContext(payload, context.SHA256)
	if err != nil || decoded == nil || decoded.SHA256 != context.SHA256 {
		t.Fatalf("context failed canonical roundtrip: decoded=%+v err=%v", decoded, err)
	}
	if got := snapshotValue(snapshot).ServiceObjectContextHash; got != context.SHA256 {
		t.Fatalf("audit seal omitted service-object context hash: %q", got)
	}
	inputHash, err := frozenInputHash(snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	otherContext := context
	otherContext.Items = append([]ServiceObjectReviewContextItem(nil), context.Items...)
	otherContext.Items[0].UnresolvedReason = "changed"
	_, otherDigest, err := canonicalServiceObjectReviewContext(otherContext)
	if err != nil {
		t.Fatal(err)
	}
	otherContext.SHA256 = otherDigest
	otherSnapshot := RunSnapshot{ServiceObjectContext: &otherContext, ServiceObjectContextHash: otherDigest}
	otherInputHash, err := frozenInputHash(otherSnapshot, nil)
	if err != nil || otherInputHash == inputHash {
		t.Fatalf("frozen attempt input hash ignored service-object context: first=%s second=%s err=%v", inputHash, otherInputHash, err)
	}
	changedContext := context
	changedContext.SHA256 = strings.Repeat("0", 64)
	if err := audit.RejectRewrite(snapshotValue(snapshot), snapshotValue(RunSnapshot{ServiceObjectContext: &changedContext, ServiceObjectContextHash: changedContext.SHA256})); err == nil {
		t.Fatal("audit accepted rewriting the frozen service-object context hash")
	}
	cloned, err := (state{Snapshots: []RunSnapshot{snapshot}}).clone()
	if err != nil || len(cloned.Snapshots) != 1 || cloned.Snapshots[0].ServiceObjectContext == nil ||
		cloned.Snapshots[0].ServiceObjectContextHash != context.SHA256 {
		t.Fatalf("state clone lost service-object context: %+v err=%v", cloned.Snapshots, err)
	}
	for _, malformed := range []RunSnapshot{
		{ServiceObjectContext: &context},
		{ServiceObjectContextHash: context.SHA256},
		{ServiceObjectContext: func() *ServiceObjectReviewContext { changed := context; changed.Complete = true; return &changed }(), ServiceObjectContextHash: context.SHA256},
	} {
		if err := validateServiceObjectReviewContext(malformed); err == nil {
			t.Errorf("malformed service-object context accepted: %+v", malformed)
		}
	}
	if _, err := decodeServiceObjectReviewContext(append(bytes.TrimSuffix(payload, []byte("}")), []byte(",\"extra\":true}")...), context.SHA256); err == nil {
		t.Fatal("noncanonical context bytes accepted")
	}
	legacy := RunSnapshot{RunID: "legacy-run"}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("service_object_context")) {
		t.Fatalf("legacy snapshot JSON gained optional service-object context: %s", raw)
	}
}

func TestServiceObjectCandidateEvidenceUsesExactFrozenDocumentAndParse(t *testing.T) {
	applicable := true
	facts := []rulepack.Fact{
		{ID: "fact-a", RuleID: "minimum-bandwidth", Dimension: "minimum_bandwidth", ObjectRef: "LINE-A", Value: "100", Unit: "Mbit/s",
			SourceRole: "draft_contract", Applicable: &applicable, Citations: []rulepack.Citation{
				{SourceID: "doc-a", SourceVersion: "parse-a", TenantID: "tenant-a", CaseID: "case-a", SourceDigest: strings.Repeat("a", 64), Start: 1, End: 2, Quote: "100 Mbit/s"},
				{SourceID: "doc-b", SourceVersion: "parse-b", TenantID: "tenant-a", CaseID: "case-a", SourceDigest: strings.Repeat("b", 64), Start: 1, End: 2, Quote: "100 Mbit/s"},
			}},
		{ID: "fact-b", RuleID: "minimum-bandwidth", Dimension: "minimum_bandwidth", ObjectRef: "LINE-B", Value: "200", Unit: "Mbit/s",
			SourceRole: "draft_contract", Citations: []rulepack.Citation{{SourceID: "doc-b", SourceVersion: "parse-b", TenantID: "tenant-a", CaseID: "case-a", SourceDigest: strings.Repeat("c", 64), Start: 3, End: 4, Quote: "200 Mbit/s"}}},
	}
	a, err := serviceObjectCandidatesFromFrozenFacts("mat-a", 3, "doc-a", "parse-a", "run-a", "attempt-a", facts)
	if err != nil || len(a) != 1 || a[0].ObjectRef != "LINE-A" {
		t.Fatalf("candidate extraction used an unfrozen source parse: candidates=%+v err=%v", a, err)
	}
	b, err := serviceObjectCandidatesFromFrozenFacts("mat-b", 1, "doc-b", "parse-b", "run-a", "attempt-a", facts)
	if err != nil || len(b) != 2 || b[0].ObjectRef != "LINE-A" || b[1].ObjectRef != "LINE-B" || a[0].FactsSHA256 == b[0].FactsSHA256 {
		t.Fatalf("candidate facts did not remain tied to exact document/parse/material identity: a=%+v b=%+v err=%v", a, b, err)
	}
	wrongParse, err := serviceObjectCandidatesFromFrozenFacts("mat-a", 3, "doc-a", "parse-other", "run-a", "attempt-a", facts)
	if err != nil || len(wrongParse) != 0 {
		t.Fatalf("facts from a different parse were accepted: candidates=%+v err=%v", wrongParse, err)
	}
}

func TestServiceObjectReviewContextCoversEveryRegisteredAndRecognizedObject(t *testing.T) {
	at := time.Date(2026, 10, 31, 2, 0, 0, 0, time.UTC)
	makeSource := func(id, objectRef string) serviceObjectReviewContextSource {
		head := ServiceObjectHead{ID: id, CurrentVersion: 1, CurrentAssociationVersion: 1}
		version := ServiceObjectVersion{ServiceObjectID: id, Version: 1, ObjectType: ServiceObjectDedicatedLine,
			DisplayName: id, State: ServiceObjectConfirmed}
		association := &ServiceObjectContractAssociation{ServiceObjectID: id, AssociationVersion: 1, ObjectVersion: 1,
			MaterialID: "material-" + id, MaterialVersion: 1, DocumentID: "document-" + id,
			ParsedDocumentID: "parse-" + id, SourceRunID: "run-1", SourceAttemptID: "attempt-1",
			ObjectRef: objectRef, FactsSHA256: strings.Repeat("a", 64)}
		mapping := &OperatorMappingVersion{MappingID: "mapping-" + id, MappingVersion: 1, ServiceObjectID: id,
			ObjectVersion: 1, AssociationVersion: 1, Binding: OperatorObjectBinding{Version: "binding-" + id,
				Status: "confirmed", EffectiveFrom: at.Add(-time.Hour), EffectiveUntil: at.Add(time.Hour)}}
		return serviceObjectReviewContextSource{Head: head, Version: version, Association: association, Mapping: mapping}
	}
	candidate := func(source serviceObjectReviewContextSource) ServiceObjectCandidateEvidence {
		a := source.Association
		return ServiceObjectCandidateEvidence{ObjectRef: a.ObjectRef, MaterialID: a.MaterialID, MaterialVersion: a.MaterialVersion,
			DocumentID: a.DocumentID, ParsedDocumentID: a.ParsedDocumentID, SourceRunID: a.SourceRunID,
			SourceAttemptID: a.SourceAttemptID, FactsSHA256: a.FactsSHA256}
	}
	a, b := makeSource("object-a", "LINE-A"), makeSource("object-b", "LINE-B")
	candidateC := ServiceObjectCandidateEvidence{ObjectRef: "LINE-C", MaterialID: "material-c", MaterialVersion: 1,
		DocumentID: "document-c", ParsedDocumentID: "parse-c", SourceRunID: "run-1", SourceAttemptID: "attempt-1", FactsSHA256: strings.Repeat("c", 64)}
	got, err := planServiceObjectReviewContext(at, []serviceObjectReviewContextSource{b, a}, []ServiceObjectCandidateEvidence{candidate(a), candidate(b), candidateC})
	if err != nil {
		t.Fatal(err)
	}
	if got.Complete || len(got.Items) != 3 || got.Items[0].ServiceObjectID != "object-a" ||
		got.Items[1].ServiceObjectID != "object-b" || got.Items[2].CandidateObjectRef != "LINE-C" ||
		got.Items[2].UnresolvedReason != "recognized_object_not_registered" {
		t.Fatalf("context omitted an unregistered recognized contract object: %+v", got)
	}
	reordered, err := planServiceObjectReviewContext(at.In(time.FixedZone("UTC+8", 8*60*60)),
		[]serviceObjectReviewContextSource{a, b}, []ServiceObjectCandidateEvidence{candidateC, candidate(b), candidate(a), candidate(a)})
	if err != nil || reordered.SHA256 != got.SHA256 {
		t.Fatalf("context hash must be stable across source and candidate order at the same instant: reordered=%+v err=%v", reordered, err)
	}
	duplicate := makeSource("object-z", "LINE-A")
	ambiguous, err := planServiceObjectReviewContext(at, []serviceObjectReviewContextSource{a, duplicate}, []ServiceObjectCandidateEvidence{candidate(a), candidate(duplicate)})
	if err != nil || ambiguous.Complete || len(ambiguous.Items) != 2 ||
		ambiguous.Items[0].UnresolvedReason != "ambiguous_object_ref_mapping" ||
		ambiguous.Items[1].UnresolvedReason != "ambiguous_object_ref_mapping" {
		t.Fatalf("duplicate frozen ObjectRef assignments were not marked ambiguous: context=%+v err=%v", ambiguous, err)
	}
}

func TestServiceObjectReviewContextKeepsMissingOrOutOfPeriodItemsUnresolved(t *testing.T) {
	at := time.Date(2026, 10, 31, 2, 0, 0, 0, time.UTC)
	head := ServiceObjectHead{ID: "object-a", CurrentVersion: 1, CurrentAssociationVersion: 0}
	version := ServiceObjectVersion{ServiceObjectID: "object-a", Version: 1, ObjectType: ServiceObjectDedicatedLine,
		State: ServiceObjectPendingConfirmation}
	missing, err := planServiceObjectReviewContext(at, []serviceObjectReviewContextSource{{Head: head, Version: version}}, nil)
	if err != nil || missing.Complete || len(missing.Items) != 1 || missing.Items[0].UnresolvedReason != "service_object_not_confirmed" {
		t.Fatalf("pending object was not retained as unresolved: context=%+v err=%v", missing, err)
	}
	empty, err := planServiceObjectReviewContext(at, nil, nil)
	if err != nil || empty.Complete || len(empty.Items) != 1 || empty.Items[0].UnresolvedReason != "object_inventory_unavailable" {
		t.Fatalf("empty inventory incorrectly appeared complete: context=%+v err=%v", empty, err)
	}

	head.CurrentAssociationVersion = 1
	version.State = ServiceObjectConfirmed
	association := &ServiceObjectContractAssociation{ServiceObjectID: "object-a", AssociationVersion: 1, ObjectVersion: 1,
		MaterialID: "material-a", MaterialVersion: 1, DocumentID: "document-a", ParsedDocumentID: "parse-a",
		SourceRunID: "run-a", SourceAttemptID: "attempt-a", ObjectRef: "LINE-A", FactsSHA256: strings.Repeat("b", 64)}
	mapping := &OperatorMappingVersion{MappingID: "mapping-a", MappingVersion: 1, ServiceObjectID: "object-a", ObjectVersion: 1,
		AssociationVersion: 1, Binding: OperatorObjectBinding{Version: "binding-a", Status: "confirmed", EffectiveFrom: at.Add(-time.Hour), EffectiveUntil: at}}
	candidate := ServiceObjectCandidateEvidence{ObjectRef: "LINE-A", MaterialID: "material-a", MaterialVersion: 1,
		DocumentID: "document-a", ParsedDocumentID: "parse-a", SourceRunID: "run-a", SourceAttemptID: "attempt-a", FactsSHA256: strings.Repeat("b", 64)}
	period, err := planServiceObjectReviewContext(at, []serviceObjectReviewContextSource{{Head: head, Version: version, Association: association, Mapping: mapping}}, []ServiceObjectCandidateEvidence{candidate})
	if err != nil || period.Complete || period.Items[0].UnresolvedReason != "operator_mapping_not_effective_at_business_time" {
		t.Fatalf("end-exclusive mapping interval did not remain unresolved: context=%+v err=%v", period, err)
	}
	mapping.Binding.EffectiveUntil = at.Add(time.Hour)
	matched, err := planServiceObjectReviewContext(at,
		[]serviceObjectReviewContextSource{{Head: head, Version: version, Association: association, Mapping: mapping}}, []ServiceObjectCandidateEvidence{candidate})
	if err != nil || !matched.Complete {
		t.Fatalf("exact association did not cover its frozen candidate: context=%+v err=%v", matched, err)
	}
	staleCandidate := candidate
	staleCandidate.DocumentID = "new-document-a"
	staleCandidate.ParsedDocumentID = "new-parse-a"
	stale, err := planServiceObjectReviewContext(at,
		[]serviceObjectReviewContextSource{{Head: head, Version: version, Association: association, Mapping: mapping}}, []ServiceObjectCandidateEvidence{staleCandidate})
	if err != nil || stale.Complete || stale.Items[0].UnresolvedReason != "association_not_pinned_to_frozen_candidate" || stale.SHA256 == matched.SHA256 {
		t.Fatalf("old association was treated as evidence from a newly frozen contract: context=%+v err=%v", stale, err)
	}
}
