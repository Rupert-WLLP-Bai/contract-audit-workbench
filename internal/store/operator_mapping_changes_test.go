package store

import (
	"testing"
	"time"
)

func TestPlanOperatorMappingChangePinsIdentityAndAdvancesOneBindingVersion(t *testing.T) {
	now := time.Date(2026, 10, 31, 9, 10, 11, 123456789, time.FixedZone("CST", 8*60*60))
	from := time.Date(2026, 10, 1, 0, 0, 0, 999, time.FixedZone("CST", 8*60*60))
	until := time.Date(2027, 10, 1, 0, 0, 0, 999, time.FixedZone("CST", 8*60*60))
	head := OperatorMappingHead{CaseID: "case-a", MappingID: "mapping-a", ServiceObjectID: "service-a",
		CurrentVersion: 1, CurrentBindingVersion: "binding-v1", CreatedBy: "creator", CreatedAt: from.UTC(), UpdatedAt: from.UTC()}
	current := OperatorMappingVersion{MappingID: head.MappingID, MappingVersion: 1, ServiceObjectID: head.ServiceObjectID,
		ObjectVersion: 2, AssociationVersion: 3, Binding: OperatorObjectBinding{
			CaseID: head.CaseID, BindingID: "binding-v1", Version: "binding-v1", Status: "confirmed",
			ContractObject: OperatorContractObject{ObjectType: "dedicated_line", ObjectID: "LINE-A"},
			ExternalObject: OperatorExternalObject{ConnectorID: "connector-old", ExternalObjectID: "external-old"},
			ConfirmedBy:    "creator", ConfirmedAt: from.UTC(), EffectiveFrom: from.UTC(), EffectiveUntil: until.UTC(),
		}}
	cas := OperatorMappingChangeCAS{ExpectedMappingVersion: 1, ExpectedBindingVersion: "binding-v1", ExpectedObjectVersion: 2, ExpectedAssociationVersion: 3}
	plan, err := PlanOperatorMappingChange(head, current, cas, OperatorMappingChangePatch{
		Kind: OperatorMappingReplace, ExternalObject: &OperatorExternalObject{ConnectorID: "connector-new", ExternalObjectID: "external-new"},
		EffectiveFrom: from, EffectiveUntil: until,
	}, "binding-v2", "approver", now)
	if err != nil {
		t.Fatal(err)
	}
	if plan.NextHead.CurrentVersion != 2 || plan.NextHead.CurrentBindingVersion != "binding-v2" || plan.NextHead.UpdatedAt.Nanosecond()%1000 != 0 {
		t.Fatalf("lineage pointer did not advance exactly once at UTC microsecond precision: %+v", plan.NextHead)
	}
	next := plan.NextVersion
	if next.MappingVersion != 2 || next.Binding.Version != "binding-v2" || next.Binding.ExternalObject.ExternalObjectID != "external-new" ||
		next.Binding.ContractObject.ObjectID != "LINE-A" || next.ServiceObjectID != "service-a" || next.ObjectVersion != 2 || next.AssociationVersion != 3 {
		t.Fatalf("mapping replacement did not preserve the pinned contract identity: %+v", next)
	}
	if next.Binding.ConfirmedBy != "approver" || !next.Binding.ConfirmedAt.Equal(now.UTC().Truncate(time.Microsecond)) ||
		next.Binding.EffectiveFrom.Nanosecond()%1000 != 0 || next.Binding.EffectiveUntil.Nanosecond()%1000 != 0 {
		t.Fatalf("new immutable binding did not use trusted confirmer and canonical times: %+v", next.Binding)
	}
	if plan.BeforeSHA256 == plan.AfterSHA256 || len(plan.PatchSHA256) != 64 {
		t.Fatalf("mapping change hashes do not bind before/after/patch: %+v", plan)
	}
}

func TestPlanOperatorMappingIdentityCorrectionPinsNewIdentityAndAssociation(t *testing.T) {
	current, cas := operatorMappingTestCurrent()
	patch := OperatorMappingChangePatch{Kind: OperatorMappingCorrectIdentityAndMapping,
		ExternalObject: &OperatorExternalObject{ConnectorID: "connector-b", ExternalObjectID: "external-b"},
		Identity: &OperatorServiceIdentityPatch{ObjectType: ServiceObjectDedicatedLine, DisplayName: "LINE-A corrected",
			BusinessCode: "LINE-A-2", Aliases: []ServiceObjectAlias{{Kind: "legacy", Value: "LINE-A"}},
			State: ServiceObjectConfirmed, BasisRefs: []byte(`[]`), BasisSummary: "pinned contract correction"},
		EffectiveFrom:  time.Date(2026, 4, 1, 0, 0, 0, 123456789, time.FixedZone("CST", 8*60*60)),
		EffectiveUntil: time.Date(2027, 4, 1, 0, 0, 0, 0, time.UTC)}
	plan, err := PlanOperatorMappingChange(operatorMappingTestHead(), current, cas, patch, "proposal-placeholder", "proposer", time.Date(2026, 10, 31, 0, 0, 0, 999, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if plan.NextVersion.ObjectVersion != current.ObjectVersion+1 || plan.NextVersion.AssociationVersion != current.AssociationVersion+1 ||
		plan.NextVersion.Binding.ExternalObject.ExternalObjectID != "external-b" || plan.AfterSHA256 == plan.BeforeSHA256 {
		t.Fatalf("identity/mapping correction did not bind the new versions: %+v", plan)
	}
	proposed, err := proposedOperatorIdentityVersion(operatorMappingTestHead().ServiceObjectID, current.ObjectVersion, *patch.Identity, patch.EffectiveFrom)
	if err != nil || proposed.EffectiveFrom.Nanosecond()%1000 != 0 || proposed.EffectiveFrom.Location() != time.UTC ||
		proposed.State != ServiceObjectConfirmed || proposed.BusinessCode != "LINE-A-2" {
		t.Fatalf("approved identity proposal lost canonical payload/effective instant: %+v err=%v", proposed, err)
	}
}

func TestPlanOperatorMappingChangeRetirementAndReactivationAreSODVersions(t *testing.T) {
	current, cas := operatorMappingTestCurrent()
	retired, err := PlanOperatorMappingChange(operatorMappingTestHead(), current, cas, OperatorMappingChangePatch{
		Kind: OperatorMappingRetire, EffectiveFrom: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC),
		EffectiveUntil: time.Date(2027, 10, 31, 0, 0, 0, 0, time.UTC),
	}, "binding-v2", "reviewer", time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if retired.NextVersion.Binding.Status != "inactive" || retired.NextVersion.Binding.ExternalObject.ExternalObjectID != "external-a" {
		t.Fatalf("retirement changed external identity or failed to append inactive version: %+v", retired.NextVersion)
	}
	retiredHead := retired.NextHead
	currentRetired := retired.NextVersion
	reactivated, err := PlanOperatorMappingChange(retiredHead, currentRetired,
		OperatorMappingChangeCAS{ExpectedMappingVersion: 2, ExpectedBindingVersion: "binding-v2", ExpectedObjectVersion: 2, ExpectedAssociationVersion: 3},
		OperatorMappingChangePatch{Kind: OperatorMappingReactivate,
			EffectiveFrom: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), EffectiveUntil: time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)},
		"binding-v3", "reviewer-2", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if reactivated.NextVersion.MappingVersion != 3 || reactivated.NextVersion.Binding.Status != "confirmed" ||
		reactivated.NextVersion.Binding.ExternalObject.ExternalObjectID != "external-a" {
		t.Fatalf("reactivation did not append a confirmed binding version: %+v", reactivated.NextVersion)
	}
}

func TestPlanOperatorMappingChangeRejectsStaleAndInvalidTuples(t *testing.T) {
	head := operatorMappingTestHead()
	current, cas := operatorMappingTestCurrent()
	for _, test := range []struct {
		name string
		edit func(*OperatorMappingHead, *OperatorMappingVersion, *OperatorMappingChangeCAS, *OperatorMappingChangePatch)
	}{
		{"stale head", func(h *OperatorMappingHead, _ *OperatorMappingVersion, _ *OperatorMappingChangeCAS, _ *OperatorMappingChangePatch) {
			h.CurrentVersion++
		}},
		{"wrong service object", func(_ *OperatorMappingHead, v *OperatorMappingVersion, _ *OperatorMappingChangeCAS, _ *OperatorMappingChangePatch) {
			v.ServiceObjectID = "other"
		}},
		{"wrong object association pin", func(_ *OperatorMappingHead, _ *OperatorMappingVersion, c *OperatorMappingChangeCAS, _ *OperatorMappingChangePatch) {
			c.ExpectedAssociationVersion++
		}},
		{"invalid connector", func(_ *OperatorMappingHead, _ *OperatorMappingVersion, _ *OperatorMappingChangeCAS, p *OperatorMappingChangePatch) {
			p.ExternalObject = &OperatorExternalObject{ConnectorID: "bad id", ExternalObjectID: "external-b"}
		}},
		{"empty interval", func(_ *OperatorMappingHead, _ *OperatorMappingVersion, _ *OperatorMappingChangeCAS, p *OperatorMappingChangePatch) {
			p.EffectiveUntil = p.EffectiveFrom
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			localHead, localVersion, localCAS := head, current, cas
			patch := OperatorMappingChangePatch{Kind: OperatorMappingReplace,
				ExternalObject: &OperatorExternalObject{ConnectorID: "connector-b", ExternalObjectID: "external-b"},
				EffectiveFrom:  time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), EffectiveUntil: time.Date(2027, 10, 31, 0, 0, 0, 0, time.UTC)}
			test.edit(&localHead, &localVersion, &localCAS, &patch)
			if _, err := PlanOperatorMappingChange(localHead, localVersion, localCAS, patch, "binding-v2", "reviewer", time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)); err == nil {
				t.Fatal("invalid or stale mapping change was accepted")
			}
		})
	}
}

func TestOperatorMappingChangeDecisionRequiresDifferentTrustedPrincipal(t *testing.T) {
	request := OperatorMappingChangeRequest{ProposedBy: TrustedPrincipalProjection{UserID: "proposer", Issuer: "https://directory.example", Subject: "principal-7"}}
	for _, decider := range []TrustedPrincipalProjection{
		{UserID: "proposer", Issuer: "https://directory.example", Subject: "principal-7"},
		{UserID: "proposer", Issuer: "https://other-directory.example", Subject: "different-principal"},
		{UserID: "alias-user", Issuer: "https://directory.example", Subject: "principal-7"},
	} {
		if operatorMappingDecisionIndependent(request, decider) {
			t.Fatalf("same trusted principal was permitted to decide: %+v", decider)
		}
	}
	if !operatorMappingDecisionIndependent(request, TrustedPrincipalProjection{UserID: "reviewer", Issuer: "https://directory.example", Subject: "principal-8"}) {
		t.Fatal("different trusted principal was rejected")
	}
}

func operatorMappingTestHead() OperatorMappingHead {
	return OperatorMappingHead{CaseID: "case-a", MappingID: "mapping-a", ServiceObjectID: "service-a", CurrentVersion: 1,
		CurrentBindingVersion: "binding-v1", CreatedBy: "creator", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func operatorMappingTestCurrent() (OperatorMappingVersion, OperatorMappingChangeCAS) {
	head := operatorMappingTestHead()
	current := OperatorMappingVersion{MappingID: head.MappingID, MappingVersion: 1, ServiceObjectID: head.ServiceObjectID,
		ObjectVersion: 2, AssociationVersion: 3, Binding: OperatorObjectBinding{CaseID: head.CaseID, BindingID: "binding-v1", Version: "binding-v1",
			Status: "confirmed", ContractObject: OperatorContractObject{ObjectType: "dedicated_line", ObjectID: "LINE-A"},
			ExternalObject: OperatorExternalObject{ConnectorID: "connector-a", ExternalObjectID: "external-a"},
			EffectiveFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EffectiveUntil: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}}
	cas := OperatorMappingChangeCAS{ExpectedMappingVersion: 1, ExpectedBindingVersion: "binding-v1", ExpectedObjectVersion: 2, ExpectedAssociationVersion: 3}
	return current, cas
}
