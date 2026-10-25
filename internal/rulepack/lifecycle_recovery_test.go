package rulepack

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestIntermediateSnapshotsResumeWithoutPrematureExecution(t *testing.T) {
	reload := func(pack *Pack) *Pack {
		t.Helper()
		body, err := pack.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		restored, err := Parse(body)
		if err != nil {
			t.Fatal(err)
		}
		if restored.Hash() != pack.Hash() {
			t.Fatal("serialization changed the content approved by the caller")
		}
		if _, err := runRequest(t, restored, demoRequest(t)); !errors.Is(err, ErrNotPublished) {
			t.Fatalf("intermediate snapshot was executable: %v", err)
		}
		return restored
	}
	pack := reload(demoDraft(t))
	validated, err := pack.Validate()
	if err != nil {
		t.Fatal(err)
	}
	validated = reload(validated)
	approved, err := validated.Approve("test-approver", validated.Hash())
	if err != nil {
		t.Fatal(err)
	}
	approved = reload(approved)
	published, err := approved.Publish()
	if err != nil {
		t.Fatal(err)
	}
	report, err := runRequest(t, published, demoRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, ruleID := range []string{"ENT-COM-ADV-001", "ENT-TECH-BW-001", "ENT-TECH-SLA-001"} {
		if got := resultFor(t, report, ruleID).Relation; got != RelationConflict {
			t.Fatalf("resumed publication changed %s: %s", ruleID, got)
		}
	}
}

func TestIntermediateSnapshotAttestationsCannotBeForged(t *testing.T) {
	draft := demoDraft(t)
	validated, err := draft.Validate()
	if err != nil {
		t.Fatal(err)
	}
	approved, err := validated.Approve("test-approver", validated.Hash())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		pack   *Pack
		mutate func(*Manifest)
	}{
		{"draft-with-validation", draft, func(m *Manifest) { m.Lifecycle.Verification = validated.Manifest().Lifecycle.Verification }},
		{"failed-validation", validated, func(m *Manifest) { m.Lifecycle.Verification.Passed = false }},
		{"validated-with-approval", validated, func(m *Manifest) { m.Lifecycle.Approval = approved.Manifest().Lifecycle.Approval }},
		{"approved-without-validation", approved, func(m *Manifest) { m.Lifecycle.Verification = nil }},
		{"approval-for-different-content", approved, func(m *Manifest) { m.Lifecycle.Approval.ManifestHash = "different-content" }},
		{"approval-without-actor", approved, func(m *Manifest) { m.Lifecycle.Approval.ActorID = "" }},
		{"unknown-lifecycle", draft, func(m *Manifest) { m.Lifecycle.State = "unrecognized" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			manifest := test.pack.Manifest()
			test.mutate(&manifest)
			body, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Parse(body); !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("forged publication authority accepted: %v", err)
			}
		})
	}
}
