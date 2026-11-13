package app

import (
	"context"
	"testing"

	"contractaudit/internal/fault"
	"contractaudit/internal/knowledge/model"
	legalModel "contractaudit/internal/legal/model"
)

func TestFrozenRequiredUsesRawPointerAndOptionalRechecksSource(t *testing.T) {
	row, in := reviewSourceDeniedFixture(t, "guidance")
	row.PointerID = "another-version"
	row.PointerActive = true
	repo := &reviewRepoStub{raw: row}
	facts := &reviewFactsStub{}
	loaded, err := LoadExactVersion(context.Background(), repo, facts, in.TenantID, row.Version.Ref.KnowledgeID, row.Version.Ref.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.PointerActive || loaded.Version.Active {
		t.Fatalf("fixture must distinguish raw and derived active: %+v", loaded)
	}
	selected := model.KnowledgeSelection(loaded.Version)
	snapshot := ReviewSnapshot{AuthorizationRevision: "revision", RequiredPolicies: []model.GovernedKnowledgeSelection{selected}}
	if err := ValidateFrozenDependencies(context.Background(), repo, facts, nil, in.TenantID, snapshot); err != nil {
		t.Fatalf("required raw pointer: %v", err)
	}
	if facts.sourceCalls != 0 {
		t.Fatalf("required unexpectedly checked source: %d", facts.sourceCalls)
	}
	if _, err := ReadFrozenContent(context.Background(), repo, facts, in.TenantID, snapshot, selected.Ref); err == nil || err.Error() != "本次审查使用的制度已撤回，或记录编号不一致" {
		t.Fatalf("read derived active: %v", err)
	}
	snapshot.RequiredPolicies = nil
	snapshot.OptionalCandidates = []model.GovernedKnowledgeSelection{selected}
	if err := ValidateFrozenDependencies(context.Background(), repo, facts, nil, in.TenantID, snapshot); err == nil || err.Error() != "本次审查可选制度的案件授权已改变" {
		t.Fatalf("optional source denial: %v", err)
	}
	if facts.sourceCalls != 1 {
		t.Fatalf("source checks=%d", facts.sourceCalls)
	}
}

type frozenSourceFacts struct {
	ReviewFacts
	sourceErr error
	calls     int
}

func (f *frozenSourceFacts) AuthorizationRevision(context.Context) (string, error) {
	return "revision", nil
}
func (f *frozenSourceFacts) AuthorizeReadSource(context.Context, model.GovernedKnowledgeContent) error {
	f.calls++
	return f.sourceErr
}

func TestFrozenOptionalInactiveAdoptionAndSourceDenial(t *testing.T) {
	row, in := reviewSourceDeniedFixture(t, "guidance")
	row.PointerActive = false
	repo := &reviewRepoStub{raw: row}
	facts := &frozenSourceFacts{}
	snapshot := ReviewSnapshot{AuthorizationRevision: "revision", OptionalCandidates: []model.GovernedKnowledgeSelection{{Ref: row.Version.Ref}}}
	if err := ValidateFrozenDependencies(context.Background(), repo, facts, nil, in.TenantID, snapshot); err != nil || repo.loads != 1 || facts.calls != 1 {
		t.Fatalf("inactive optional adoption: loads=%d source=%d err=%v", repo.loads, facts.calls, err)
	}
	if _, err := ReadFrozenContent(context.Background(), repo, facts, in.TenantID, snapshot, row.Version.Ref); err == nil || err.Error() != "本次审查使用的制度已撤回，或记录编号不一致" {
		t.Fatalf("inactive optional read: %v", err)
	}
	for _, denied := range []error{fault.Forbidden("denied"), fault.NotFound("missing")} {
		facts.sourceErr, facts.calls, repo.loads = denied, 0, 0
		err := ValidateFrozenDependencies(context.Background(), repo, facts, nil, in.TenantID, snapshot)
		if err == nil || err.Error() != "本次审查可选制度的案件授权已改变" || facts.calls != 1 || repo.loads != 1 {
			t.Fatalf("source denial: loads=%d calls=%d err=%v", repo.loads, facts.calls, err)
		}
	}
}

func TestFrozenLegalFailurePrecedesRequiredLoad(t *testing.T) {
	row, in := reviewSourceDeniedFixture(t, "guidance")
	repo := &reviewRepoStub{raw: row}
	facts := &frozenSourceFacts{}
	snapshot := ReviewSnapshot{AuthorizationRevision: "revision", RequiredPolicies: []model.GovernedKnowledgeSelection{{Ref: row.Version.Ref}}, LegalAuthorities: []legalModel.FrozenLegalAuthority{{}}}
	err := ValidateFrozenDependencies(context.Background(), repo, facts, nil, in.TenantID, snapshot)
	if err == nil || err.Error() != "冻结的规范性法律引用缺少精确条文" || repo.loads != 0 || facts.calls != 0 {
		t.Fatalf("legal precedence: loads=%d source=%d err=%v", repo.loads, facts.calls, err)
	}
}
