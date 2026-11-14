// File metadata (manifest mirror):
// Purpose: Pins private approved identity planning, immutable prior fact bytes and rejection of missing approval or changed after-payloads.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/operator/model"
	"contractaudit/internal/rulepack"
)

func TestPlanServiceObjectIdentityChangeRequiresDecisionAndReusesFrozenFacts(t *testing.T) {
	now := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	head := model.ServiceObjectHead{ID: "svc-1", CurrentVersion: 4, CurrentAssociationVersion: 2}
	current := model.ServiceObjectVersion{ServiceObjectID: "svc-1", Version: 4, ObjectType: model.ServiceObjectDedicatedLine, DisplayName: "线路 A", State: model.ServiceObjectConfirmed, EffectiveFrom: now.Add(-24 * time.Hour), CreatedBy: "previous", CreatedAt: now.Add(-time.Hour)}
	current.PayloadSHA256, _ = model.CanonicalServiceObjectIdentitySHA256(current)
	association := validAssociationCandidate(4, 2, "material-1", 3, "doc-v3", "parse-8", "LINE-A").Association
	association.ServiceObjectID = "svc-1"
	association.AssociationVersion = 2
	association.ObjectVersion = 4
	association.FactsCanonicalJSON, association.FactsSHA256, _ = model.CanonicalServiceObjectFacts(association.FactRefs)
	proposed := model.ServiceObjectVersion{
		ServiceObjectID: "svc-1", ObjectType: model.ServiceObjectDedicatedLine, DisplayName: "线路 A 改名", State: model.ServiceObjectConfirmed,
		EffectiveFrom: now.Add(-12 * time.Hour), BasisRefs: json.RawMessage(`[{"kind":"material_anchor","document_id":"doc-v3"}]`),
	}

	if _, err := planServiceObjectIdentityChange(head, current, association, proposed, verifiedServiceObjectIdentityChange{}, "approver", now); err == nil {
		t.Fatal("identity change without an approved decision unexpectedly succeeded")
	}
	proposed, err := model.NormalizeServiceObjectVersion(proposed)
	if err != nil {
		t.Fatalf("normalize approved payload: %v", err)
	}
	afterHash, err := model.CanonicalServiceObjectIdentitySHA256(proposed)
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
	_, err = model.CanonicalServiceObjectIdentitySHA256(proposed)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planServiceObjectIdentityChange(head, current, association, proposed, approval, "approver", now)
	if err != nil {
		t.Fatalf("planServiceObjectIdentityChange() error = %v", err)
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
	if _, err := planServiceObjectIdentityChange(head, current, association, tampered, approval, "approver", now); err == nil {
		t.Fatal("identity payload changed after approval unexpectedly passed")
	}
}

func validAssociationCandidate(objectVersion, associationVersion int, materialID string, materialVersion int, documentID, parseID, objectRef string) model.ServiceObjectAssociationCandidate {
	f := rulepack.Fact{ID: "fact-1", RuleID: "bandwidth", Dimension: "minimum_bandwidth", ObjectRef: objectRef, Value: "100", Unit: "Mbps", SourceRole: "draft_contract", Citations: []rulepack.Citation{{SourceID: documentID, SourceVersion: parseID, TenantID: "tenant-1", CaseID: "case-1", SourceDigest: strings.Repeat("c", 64), Start: 10, End: 26, Quote: "minimum 100 Mbps"}}}
	hash, _ := model.CanonicalRuleFactSHA256(f)
	return model.ServiceObjectAssociationCandidate{Association: model.ServiceObjectContractAssociation{MaterialID: materialID, MaterialVersion: materialVersion, DocumentID: documentID, ParsedDocumentID: parseID, SourceRunID: "run-1", SourceAttemptID: "attempt-1", ObjectRef: objectRef, FactRefs: []model.ServiceObjectFactRef{{Fact: f, DocumentID: documentID, ParsedDocumentID: parseID, CanonicalFactSHA256: hash}}}}
}
