package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/auditext"
	"contractaudit/internal/rulepack"
)

func TestServiceObjectAssociationCandidateDigestBindsObjectCASSourceAndCandidateSet(t *testing.T) {
	base := serviceObjectCandidateDigestInput{
		ServiceObjectID: "svc-1", ExpectedObjectVersion: 2, ExpectedAssociationVersion: 1,
		RunID: "run-1", AttemptID: "attempt-1", MaterialID: "material-1", MaterialVersion: 3,
		DocumentID: "doc-3", ParsedDocumentID: "parse-3",
		Candidates: []serviceObjectCandidateDigestPin{{ObjectRef: "line-A", FactsSHA256: strings.Repeat("a", 64)}},
	}
	baseDigest, err := serviceObjectCandidatesSHA256(base)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name string
		edit func(*serviceObjectCandidateDigestInput)
	}{
		{"object", func(p *serviceObjectCandidateDigestInput) { p.ServiceObjectID = "svc-2" }},
		{"identity CAS", func(p *serviceObjectCandidateDigestInput) { p.ExpectedObjectVersion++ }},
		{"association CAS", func(p *serviceObjectCandidateDigestInput) { p.ExpectedAssociationVersion++ }},
		{"run", func(p *serviceObjectCandidateDigestInput) { p.RunID = "run-2" }},
		{"attempt", func(p *serviceObjectCandidateDigestInput) { p.AttemptID = "attempt-2" }},
		{"material", func(p *serviceObjectCandidateDigestInput) { p.MaterialID = "material-2" }},
		{"material version", func(p *serviceObjectCandidateDigestInput) { p.MaterialVersion++ }},
		{"document", func(p *serviceObjectCandidateDigestInput) { p.DocumentID = "doc-4" }},
		{"parse", func(p *serviceObjectCandidateDigestInput) { p.ParsedDocumentID = "parse-4" }},
		{"candidate ref", func(p *serviceObjectCandidateDigestInput) { p.Candidates[0].ObjectRef = "line-B" }},
		{"candidate facts", func(p *serviceObjectCandidateDigestInput) { p.Candidates[0].FactsSHA256 = strings.Repeat("b", 64) }},
		{"candidate set", func(p *serviceObjectCandidateDigestInput) {
			p.Candidates = append(p.Candidates, serviceObjectCandidateDigestPin{ObjectRef: "line-B", FactsSHA256: strings.Repeat("b", 64)})
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			changed := base
			changed.Candidates = append([]serviceObjectCandidateDigestPin(nil), base.Candidates...)
			mutation.edit(&changed)
			got, err := serviceObjectCandidatesSHA256(changed)
			if err != nil {
				t.Fatal(err)
			}
			if got == baseDigest {
				t.Fatal("candidate digest did not bind changed ", mutation.name)
			}
		})
	}
}

func TestOperatorFactsForAssociationPreservesExactPinnedObjectAndMeasurementSemantics(t *testing.T) {
	applicable := true
	digest := strings.Repeat("a", 64)
	association := ServiceObjectContractAssociation{
		ServiceObjectID: "service-line-a", AssociationVersion: 2, ObjectVersion: 3,
		MaterialID: "material-a", MaterialVersion: 4, DocumentID: "doc-a-v4", ParsedDocumentID: "parse-a-v4",
		ObjectRef: "LINE-A", FactsSHA256: "",
	}
	association.FactRefs = []ServiceObjectFactRef{
		{Fact: rulepack.Fact{ID: "fact-a-bandwidth", RuleID: "rule-bandwidth", Dimension: "minimum_bandwidth",
			ObjectRef: "LINE-A", Value: "100", Unit: "Mbit/s", SourceRole: "draft_contract",
			Applicable: &applicable, Basis: "contract_total", Citations: []rulepack.Citation{{SourceID: "doc-a-v4",
				SourceVersion: "parse-a-v4", SourceDigest: digest, Start: 10, End: 13, Quote: "100"}}},
			DocumentID: "doc-a-v4", ParsedDocumentID: "parse-a-v4"},
		{Fact: rulepack.Fact{ID: "fact-a-sla", RuleID: "rule-sla", Dimension: "monthly_availability",
			ObjectRef: "LINE-A", Value: "99.9", Unit: "percent", SourceRole: "draft_contract",
			MeasurementWindow: "calendar_month", TimeZone: "Asia/Shanghai", Calendar: "gregorian",
			OutageDefinition: "all_service_unavailability", Exclusions: []string{"planned_maintenance"},
			Applicable: &applicable, Citations: []rulepack.Citation{{SourceID: "doc-a-v4", SourceVersion: "parse-a-v4",
				SourceDigest: digest, Start: 20, End: 24, Quote: "99.9"}}},
			DocumentID: "doc-a-v4", ParsedDocumentID: "parse-a-v4"},
	}
	for index := range association.FactRefs {
		factDigest, err := canonicalRuleFactSHA256(association.FactRefs[index].Fact)
		if err != nil {
			t.Fatal(err)
		}
		association.FactRefs[index].CanonicalFactSHA256 = factDigest
	}
	canonical, hash, err := canonicalServiceObjectFacts(association.FactRefs)
	if err != nil {
		t.Fatal(err)
	}
	association.FactsCanonicalJSON, association.FactsSHA256 = canonical, hash

	facts, err := operatorFactsForAssociation(association)
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range facts {
		if err := fact.Validate(); err != nil {
			t.Fatalf("projected fact failed frozen-input validation: %+v: %v", fact, err)
		}
	}
	if len(facts) != 2 || facts[0].ObjectRef != "LINE-A" || facts[0].FactID != "fact-a-bandwidth" ||
		facts[0].Evidence[0].DocumentVersionID != "doc-a-v4" || facts[0].Evidence[0].ParsedVersionID != "parse-a-v4" ||
		facts[0].Evidence[0].SourceDigest != digest || facts[0].Basis != "contract_total" || facts[0].Applicable == nil || !*facts[0].Applicable {
		t.Fatalf("evaluation projection lost pinned object/fact/citation identity: %+v", facts)
	}
	if facts[1].Dimension != "monthly_availability" || facts[1].MeasurementWindow != "calendar_month" ||
		facts[1].TimeZone != "Asia/Shanghai" || facts[1].Calendar != "gregorian" ||
		facts[1].OutageDefinition != "all_service_unavailability" || !reflect.DeepEqual(facts[1].Exclusions, []string{"planned_maintenance"}) {
		t.Fatalf("evaluation projection lost measurement semantics or exceptions: %+v", facts[1])
	}

	// Object B's rulepack facts cannot be attached to A's immutable association.
	association.FactRefs[1].Fact.ObjectRef = "LINE-B"
	if _, err := operatorFactsForAssociation(association); err == nil {
		t.Fatal("foreign object fact was accepted by A's pinned association")
	}
}

func TestAuditextFactNewObjectFieldsStayOmittedForLegacyPayload(t *testing.T) {
	legacy := auditext.Fact{Name: "minimum_bandwidth", Value: auditext.ExactQuantity{Value: "100", Unit: "Mbit/s"},
		Evidence: []auditext.EvidenceRef{{Kind: auditext.EvidenceDocument, DocumentVersionID: "doc-a", ParsedVersionID: "parse-a",
			Start: 0, End: 3, Quote: "100"}}, SemanticKind: "configured_value"}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	for _, omitted := range []string{`"fact_id"`, `"rule_id"`, `"dimension"`, `"object_ref"`, `"applicable"`, `"basis"`, `"source_digest"`} {
		if strings.Contains(string(raw), omitted) {
			t.Fatalf("optional P3 field %s changed a legacy fact's JSON: %s", omitted, raw)
		}
	}
}

func TestServiceObjectAssociationReceiptUsesCandidateSHA256WireName(t *testing.T) {
	value := ServiceObjectAssociationConfirmInput{Confirmed: true, CandidatesSHA256: strings.Repeat("a", 64)}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["candidate_sha256"]; !ok {
		t.Fatalf("candidate hash wire field missing: %s", encoded)
	}
	if _, ok := fields["candidates_sha256"]; ok {
		t.Fatalf("plural candidate hash field leaked: %s", encoded)
	}
}

func TestPlanServiceObjectAssociationConfirmsPendingIdentityAtomically(t *testing.T) {
	now := time.Date(2026, 10, 31, 8, 0, 0, 0, time.UTC)
	head := ServiceObjectHead{ID: "svc-1", CurrentVersion: 1}
	identity := ServiceObjectVersion{
		ServiceObjectID: "svc-1", Version: 1, ObjectType: ServiceObjectDedicatedLine,
		DisplayName: "  专线 A  ", State: ServiceObjectPendingConfirmation,
		CreatedBy: "creator", CreatedAt: now,
	}
	candidate := ServiceObjectAssociationCandidate{
		ExpectedObjectVersion:      1,
		ExpectedAssociationVersion: 0,
		Confirmed:                  true,
		Association: ServiceObjectContractAssociation{
			MaterialID: "material-1", MaterialVersion: 2, DocumentID: "doc-v2",
			ParsedDocumentID: "parse-7", SourceRunID: "run-1", SourceAttemptID: "attempt-1",
			ObjectRef: "LINE-A", FactRefs: []ServiceObjectFactRef{{Fact: rulepack.Fact{ID: "fact-1", RuleID: "bandwidth", Dimension: "minimum_bandwidth", ObjectRef: "LINE-A", Value: "100", Unit: "Mbps", SourceRole: "draft_contract", MeasurementWindow: "calendar_month", TimeZone: "Asia/Shanghai", Calendar: "gregorian", OutageDefinition: "all_service_unavailability", Exclusions: []string{"planned"}, Basis: "约定最低带宽", Applicable: boolPointer(false), Citations: []rulepack.Citation{{SourceID: "doc-v2", SourceVersion: "parse-7", TenantID: "tenant-1", CaseID: "case-1", SourceDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", Start: 10, End: 23, Quote: "最低带宽 100 Mbps"}}}, DocumentID: "doc-v2", ParsedDocumentID: "parse-7", CanonicalFactSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
		},
	}
	candidate.Association.FactRefs[0].CanonicalFactSHA256, _ = canonicalRuleFactSHA256(candidate.Association.FactRefs[0].Fact)

	plan, err := PlanServiceObjectAssociation(head, identity, candidate, "confirmer", now)
	if err != nil {
		t.Fatalf("PlanServiceObjectAssociation() error = %v", err)
	}
	if plan.ObjectVersion.Version != 2 || plan.ObjectVersion.State != ServiceObjectConfirmed {
		t.Fatalf("first confirmed association must append confirmed identity version 2, got %+v", plan.ObjectVersion)
	}
	if plan.Association.AssociationVersion != 1 || plan.Association.ObjectVersion != 2 {
		t.Fatalf("association must pin the newly confirmed identity version: %+v", plan.Association)
	}
	wantFact := candidate.Association.FactRefs[0].Fact
	if !reflect.DeepEqual(plan.Association.FactRefs[0].Fact, wantFact) {
		t.Fatalf("association changed typed frozen fact semantics:\n got: %+v\nwant: %+v", plan.Association.FactRefs[0].Fact, wantFact)
	}
	candidate.Association.FactRefs[0].Fact.Exclusions[0] = "mutated"
	candidate.Association.FactRefs[0].Fact.Citations[0].Quote = "mutated"
	*candidate.Association.FactRefs[0].Fact.Applicable = true
	if got := plan.Association.FactRefs[0].Fact; got.Exclusions[0] != "planned" || got.Citations[0].Quote != "最低带宽 100 Mbps" || got.Applicable == nil || *got.Applicable {
		t.Fatalf("planned frozen fact aliases mutable caller memory: %+v", got)
	}
	if plan.ObjectVersion.CreatedBy != "confirmer" || plan.Association.ConfirmedBy != "confirmer" || !plan.Association.ConfirmedAt.Equal(now) {
		t.Fatalf("confirmation provenance must use the confirmer and server time: object=%+v association=%+v", plan.ObjectVersion, plan.Association)
	}
	if plan.Head.CurrentVersion != 2 || plan.Head.CurrentAssociationVersion != 1 {
		t.Fatalf("planned heads = %+v, want version 2 / association 1", plan.Head)
	}
	if identity.Version != 1 || identity.State != ServiceObjectPendingConfirmation {
		t.Fatalf("planner mutated old identity version: %+v", identity)
	}
	if plan.ObjectVersion.DisplayName != "专线 A" {
		t.Fatalf("display name was not normalized: %q", plan.ObjectVersion.DisplayName)
	}
}

func TestPlanServiceObjectAssociationDoesNotChangeConfirmedIdentity(t *testing.T) {
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	head := ServiceObjectHead{ID: "svc-1", CurrentVersion: 4, CurrentAssociationVersion: 2}
	identity := ServiceObjectVersion{ServiceObjectID: "svc-1", Version: 4, ObjectType: ServiceObjectDedicatedLine, DisplayName: "线路 A", State: ServiceObjectConfirmed, CreatedAt: now}
	candidate := validAssociationCandidate(4, 2, "material-1", 3, "doc-v3", "parse-8", "LINE-A")

	plan, err := PlanServiceObjectAssociation(head, identity, candidate, "confirmer", now)
	if err != nil {
		t.Fatalf("PlanServiceObjectAssociation() error = %v", err)
	}
	if plan.ObjectVersion.Version != 4 || plan.ObjectVersion.State != ServiceObjectConfirmed {
		t.Fatalf("association change must preserve identity version/state, got %+v", plan.ObjectVersion)
	}
	if plan.Association.AssociationVersion != 3 || plan.Association.ObjectVersion != 4 {
		t.Fatalf("new association pin = %+v, want association 3 / object version 4", plan.Association)
	}
	if plan.Head.CurrentVersion != 4 || plan.Head.CurrentAssociationVersion != 3 {
		t.Fatalf("planned heads = %+v", plan.Head)
	}
}

func TestPlanServiceObjectAssociationNormalizesIdentityWithoutMutatingPriorVersion(t *testing.T) {
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	head := ServiceObjectHead{ID: "svc-1", CurrentVersion: 1}
	identity := ServiceObjectVersion{
		ServiceObjectID: "svc-1", Version: 1, ObjectType: ServiceObjectDedicatedLine,
		DisplayName: "线路 A", BusinessCode: " CODE-1 ", State: ServiceObjectPendingConfirmation,
		Aliases: []ServiceObjectAlias{{Kind: " serial ", Value: " A-1 "}}, CreatedAt: now,
	}
	candidate := validAssociationCandidate(1, 0, "material-1", 3, "doc-v3", "parse-8", "LINE-A")

	plan, err := PlanServiceObjectAssociation(head, identity, candidate, "confirmer", now)
	if err != nil {
		t.Fatalf("PlanServiceObjectAssociation() error = %v", err)
	}
	got := plan.ObjectVersion
	if got.DisplayName != "线路 A" || got.BusinessCode != "CODE-1" || len(got.Aliases) != 1 || got.Aliases[0] != (ServiceObjectAlias{Kind: "serial", Value: "A-1"}) {
		t.Fatalf("identity normalization = %+v", got)
	}
	if identity.BusinessCode != " CODE-1 " || identity.Aliases[0] != (ServiceObjectAlias{Kind: " serial ", Value: " A-1 "}) {
		t.Fatalf("normalization mutated old identity version: %+v", identity)
	}

	duplicate := identity
	duplicate.Aliases = []ServiceObjectAlias{{Kind: "serial", Value: "A-1"}, {Kind: " serial ", Value: " A-1 "}}
	if _, err := PlanServiceObjectAssociation(head, duplicate, candidate, "confirmer", now); err == nil {
		t.Fatal("duplicate normalized aliases unexpectedly entered a confirmed identity version")
	}
}

func TestPlanServiceObjectAssociationRejectsStaleAndCrossObjectEvidence(t *testing.T) {
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	head := ServiceObjectHead{ID: "svc-1", CurrentVersion: 4, CurrentAssociationVersion: 2}
	identity := ServiceObjectVersion{ServiceObjectID: "svc-1", Version: 4, ObjectType: ServiceObjectDedicatedLine, DisplayName: "线路 A", State: ServiceObjectConfirmed, CreatedAt: now}

	stale := validAssociationCandidate(3, 2, "material-1", 3, "doc-v3", "parse-8", "LINE-A")
	if _, err := PlanServiceObjectAssociation(head, identity, stale, "confirmer", now); err == nil {
		t.Fatal("stale object-version CAS unexpectedly succeeded")
	}

	wrongObjectFact := validAssociationCandidate(4, 2, "material-1", 3, "doc-v3", "parse-8", "LINE-A")
	wrongObjectFact.Association.FactRefs[0].Fact.ObjectRef = "LINE-B"
	if _, err := PlanServiceObjectAssociation(head, identity, wrongObjectFact, "confirmer", now); err == nil {
		t.Fatal("fact from a different ObjectRef unexpectedly entered this association")
	}
	badAnchor := validAssociationCandidate(4, 2, "material-1", 3, "doc-v3", "parse-8", "LINE-A")
	badAnchor.Association.FactRefs[0].Fact.Citations[0].End = badAnchor.Association.FactRefs[0].Fact.Citations[0].Start
	if _, err := PlanServiceObjectAssociation(head, identity, badAnchor, "confirmer", now); err == nil {
		t.Fatal("empty source anchor span unexpectedly entered the association")
	}

	unconfirmed := validAssociationCandidate(4, 2, "material-1", 3, "doc-v3", "parse-8", "LINE-A")
	unconfirmed.Confirmed = false
	if _, err := PlanServiceObjectAssociation(head, identity, unconfirmed, "confirmer", now); err == nil {
		t.Fatal("unconfirmed association unexpectedly advanced the current head")
	}
}

func TestPlanServiceObjectIdentityChangeRequiresDecisionAndReusesFrozenFacts(t *testing.T) {
	now := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	head := ServiceObjectHead{ID: "svc-1", CurrentVersion: 4, CurrentAssociationVersion: 2}
	current := ServiceObjectVersion{ServiceObjectID: "svc-1", Version: 4, ObjectType: ServiceObjectDedicatedLine, DisplayName: "线路 A", State: ServiceObjectConfirmed, EffectiveFrom: now.Add(-24 * time.Hour), CreatedBy: "previous", CreatedAt: now.Add(-time.Hour)}
	current.PayloadSHA256, _ = canonicalServiceObjectIdentitySHA256(current)
	association := validAssociationCandidate(4, 2, "material-1", 3, "doc-v3", "parse-8", "LINE-A").Association
	association.ServiceObjectID = "svc-1"
	association.AssociationVersion = 2
	association.ObjectVersion = 4
	association.FactsCanonicalJSON, association.FactsSHA256, _ = canonicalServiceObjectFacts(association.FactRefs)
	proposed := ServiceObjectVersion{
		ServiceObjectID: "svc-1", ObjectType: ServiceObjectDedicatedLine, DisplayName: "线路 A 改名", State: ServiceObjectConfirmed,
		EffectiveFrom: now.Add(-12 * time.Hour), BasisRefs: json.RawMessage(`[{"kind":"material_anchor","document_id":"doc-v3"}]`),
	}

	if _, err := PlanServiceObjectIdentityChange(head, current, association, proposed, verifiedServiceObjectIdentityChange{}, "approver", now); err == nil {
		t.Fatal("identity change without an approved decision unexpectedly succeeded")
	}
	proposed, err := normalizeServiceObjectVersion(proposed)
	if err != nil {
		t.Fatalf("normalize approved payload: %v", err)
	}
	afterHash, err := canonicalServiceObjectIdentitySHA256(proposed)
	if err != nil {
		t.Fatal(err)
	}
	approval := verifiedServiceObjectIdentityChange{
		decisionID: "decision-9", expectedObjectVersion: 4, expectedAssociation: 2,
		beforePayloadSHA256: current.PayloadSHA256, afterPayloadSHA256: afterHash,
		effectiveFrom: proposed.EffectiveFrom, basisRefs: append(json.RawMessage(nil), proposed.BasisRefs...),
		validatedByDecisionSvc: true,
	}
	proposed.PayloadSHA256 = afterHash
	_, err = canonicalServiceObjectIdentitySHA256(proposed)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanServiceObjectIdentityChange(head, current, association, proposed, approval, "approver", now)
	if err != nil {
		t.Fatalf("PlanServiceObjectIdentityChange() error = %v", err)
	}
	if plan.ObjectVersion.Version != 5 || plan.ObjectVersion.ChangeDecisionID != "decision-9" || plan.ObjectVersion.CreatedBy != "approver" || !plan.ObjectVersion.EffectiveFrom.Equal(proposed.EffectiveFrom) {
		t.Fatalf("approved identity version does not pin the decision and approver: %+v", plan.ObjectVersion)
	}
	if plan.Association.AssociationVersion != 3 || plan.Association.ObjectVersion != 5 || plan.Association.IdentityChangeDecisionID != "decision-9" {
		t.Fatalf("identity change did not append a new association pinned to the approved version: %+v", plan.Association)
	}
	if string(plan.Association.FactsCanonicalJSON) != string(association.FactsCanonicalJSON) || plan.Association.FactsSHA256 != association.FactsSHA256 {
		t.Fatalf("identity change rewrote frozen fact bytes/digest: bytes=%q hash=%q", plan.Association.FactsCanonicalJSON, plan.Association.FactsSHA256)
	}
	originalFacts := append([]byte(nil), association.FactsCanonicalJSON...)
	plan.Association.FactsCanonicalJSON[0] = 'X'
	if string(association.FactsCanonicalJSON) != string(originalFacts) {
		t.Fatal("planned association aliases the prior immutable fact payload")
	}
	tampered := proposed
	tampered.DisplayName = "未审批内容"
	if _, err := PlanServiceObjectIdentityChange(head, current, association, tampered, approval, "approver", now); err == nil {
		t.Fatal("identity payload changed after approval unexpectedly passed")
	}
}

func TestPlanServiceObjectAssociationRequiresInitialHeadVersionOne(t *testing.T) {
	now := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	identity := ServiceObjectVersion{ServiceObjectID: "svc-1", Version: 3, ObjectType: ServiceObjectDedicatedLine, DisplayName: "线路 A", State: ServiceObjectPendingConfirmation, CreatedAt: now}
	candidate := validAssociationCandidate(3, 0, "material-1", 1, "doc-1", "parse-1", "LINE-A")
	if _, err := PlanServiceObjectAssociation(ServiceObjectHead{ID: "svc-1", CurrentVersion: 3}, identity, candidate, "approver", now); err == nil {
		t.Fatal("first association unexpectedly accepted a skipped initial head version")
	}
}

func TestServiceObjectIdentityHashCanonicalizesBasisAndPostgresTimePrecision(t *testing.T) {
	left := ServiceObjectVersion{ServiceObjectID: "svc", ObjectType: ServiceObjectService, DisplayName: "服务 A", State: ServiceObjectPendingConfirmation,
		EffectiveFrom: time.Date(2026, 10, 31, 10, 0, 0, 1234, time.FixedZone("offset", 8*60*60)),
		BasisRefs:     json.RawMessage(`[{"z":"last","a":"first"}]`)}
	right := left
	right.EffectiveFrom = time.Date(2026, 10, 31, 2, 0, 0, 1234, time.UTC)
	right.BasisRefs = json.RawMessage(`[ { "a" : "first", "z" : "last" } ]`)
	leftHash, err := canonicalServiceObjectIdentitySHA256(left)
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := canonicalServiceObjectIdentitySHA256(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftHash != rightHash {
		t.Fatalf("equivalent identity payloads hashed differently: left=%s right=%s", leftHash, rightHash)
	}
}

func TestServiceObjectIdentityHashTreatsAliasesAsAnUnorderedSet(t *testing.T) {
	base := ServiceObjectVersion{ServiceObjectID: "svc", ObjectType: ServiceObjectService, DisplayName: "服务 A", State: ServiceObjectPendingConfirmation,
		Aliases: []ServiceObjectAlias{{Kind: "circuit", Value: "LINE-2"}, {Kind: "customer_code", Value: "C-1"}}}
	permuted := base
	permuted.Aliases = []ServiceObjectAlias{{Kind: "customer_code", Value: "C-1"}, {Kind: "circuit", Value: "LINE-2"}}
	leftHash, err := canonicalServiceObjectIdentitySHA256(base)
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := canonicalServiceObjectIdentitySHA256(permuted)
	if err != nil {
		t.Fatal(err)
	}
	if leftHash != rightHash {
		t.Fatalf("permuting identity aliases changed canonical hash: %s != %s", leftHash, rightHash)
	}
}

func validAssociationCandidate(objectVersion, associationVersion int, materialID string, materialVersion int, documentID, parseID, objectRef string) ServiceObjectAssociationCandidate {
	candidate := ServiceObjectAssociationCandidate{
		ExpectedObjectVersion:      objectVersion,
		ExpectedAssociationVersion: associationVersion,
		Confirmed:                  true,
		Association: ServiceObjectContractAssociation{
			MaterialID: materialID, MaterialVersion: materialVersion, DocumentID: documentID,
			ParsedDocumentID: parseID, SourceRunID: "run-1", SourceAttemptID: "attempt-1",
			ObjectRef: objectRef,
			FactRefs:  []ServiceObjectFactRef{{Fact: rulepack.Fact{ID: "fact-1", RuleID: "bandwidth", Dimension: "minimum_bandwidth", ObjectRef: objectRef, Value: "100", Unit: "Mbps", SourceRole: "draft_contract", Citations: []rulepack.Citation{{SourceID: documentID, SourceVersion: parseID, TenantID: "tenant-1", CaseID: "case-1", SourceDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", Start: 10, End: 26, Quote: "minimum 100 Mbps"}}}, DocumentID: documentID, ParsedDocumentID: parseID, CanonicalFactSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
		},
	}
	candidate.Association.FactRefs[0].CanonicalFactSHA256, _ = canonicalRuleFactSHA256(candidate.Association.FactRefs[0].Fact)
	return candidate
}

func boolPointer(value bool) *bool { return &value }
