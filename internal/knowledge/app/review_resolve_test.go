// File metadata (manifest mirror):
// Purpose: Pins current candidate validation and source-denial behavior together with Retry dependency comparison precedence.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/knowledge/model"
)

type reviewRepoStub struct {
	ReviewRepository
	raw   RawVersion
	loads int
}

func (r *reviewRepoStub) ActiveKnowledge(context.Context, string) ([]ActiveKnowledgeIdentity, error) {
	return []ActiveKnowledgeIdentity{{KnowledgeID: r.raw.Version.Ref.KnowledgeID, VersionID: r.raw.Version.Ref.VersionID}}, nil
}
func (r *reviewRepoStub) LoadVersion(context.Context, string, string, string) (RawVersion, error) {
	r.loads++
	return r.raw, nil
}

type reviewFactsStub struct {
	ReviewFacts
	sourceCalls int
}

func (f *reviewFactsStub) AuthorizeReadSource(context.Context, model.GovernedKnowledgeContent) error {
	f.sourceCalls++
	return fault.NotFound("source denied")
}
func (f *reviewFactsStub) AuthorizationRevision(context.Context) (string, error) {
	return "revision", nil
}
func reviewSourceDeniedFixture(t *testing.T, kind string) (RawVersion, ReviewSelectionInput) {
	t.Helper()
	tenant := "tenant"
	digest := model.SHA256([]byte("pack"))
	content := model.GovernedKnowledgeContent{Encoding: model.ContentEncoding, KnowledgeID: "guide", Version: "1", Kind: kind, Title: "title", Body: "secret", Summary: "summary", SourceType: "reference", SourceReference: "source", Scope: model.KnowledgeScope{BusinessLine: "ENT", Scenario: "line", ContractType: "service"}, ValidFrom: "2020-01-01T00:00:00Z", ValidUntil: "2100-01-01T00:00:00Z", Binding: model.PolicyRuleBinding{PackID: "pack", PackVersion: "1", PackContentHash: digest, PackSnapshotDigest: digest, CompilerRef: model.RulePackCompilerRef, RuleIDs: []string{"rule"}, SourceSpans: []model.KnowledgeSourceSpan{{RuleID: "rule", Start: 0, End: 6, Quote: "secret"}}, CandidateDiff: "diff", ValidationEvidence: "evidence"}}
	if kind == "policy" {
		content.Binding.ExtractionCompilerRef = model.PolicyThresholdCompilerRef
	}
	raw, hash, err := model.ContentDigest(content)
	if err != nil {
		t.Fatal(err)
	}
	binding, _ := json.Marshal(content.Binding)
	scope, _ := model.ScopeKey(content.Scope)
	version := model.VersionID(tenant, content.KnowledgeID, content.Version)
	row := RawVersion{Version: model.GovernedKnowledgeVersion{Ref: model.KnowledgeRef{KnowledgeID: content.KnowledgeID, VersionID: version, ContentSHA256: hash}, Status: "published", ApprovedDigest: hash, ApprovalActorID: "actor", ApprovalReason: "reason"}, ContentBytes: raw, Binding: binding, ScopeKey: scope, Kind: kind, DisplayVersion: content.Version, PointerID: version, PointerActive: true, BindingPackID: "pack", BindingPackVersion: "1"}
	in := ReviewSelectionInput{TenantID: tenant, Case: model.ReviewCaseFacts{OrgID: "org", CaseID: "case", BusinessLine: "ENT", Scenario: "line", ContractType: "service"}, BusinessAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), OptionalLimit: 1}
	return row, in
}
func TestResolveCurrentSourceDenialKeepsPolicyAndOptionalDistinction(t *testing.T) {
	for _, kind := range []string{"policy", "guidance"} {
		t.Run(kind, func(t *testing.T) {
			row, in := reviewSourceDeniedFixture(t, kind)
			repo := &reviewRepoStub{raw: row}
			facts := &reviewFactsStub{}
			out, err := ResolveCurrent(context.Background(), repo, facts, in)
			if repo.loads != 1 || facts.sourceCalls != 1 {
				t.Fatalf("did not reach authorization: loads=%d source=%d err=%v", repo.loads, facts.sourceCalls, err)
			}
			if kind == "policy" {
				if err == nil || err.Error() != "强制政策的来源授权不可用" {
					t.Fatalf("policy err=%v", err)
				}
			} else if err != nil || len(out.OptionalCandidates) != 0 {
				t.Fatalf("optional=%+v err=%v", out, err)
			}
		})
	}
}
func TestCompareRetrySelectionsKeepsFrozenOptionalAndLegalPriority(t *testing.T) {
	frozen := ReviewSnapshot{OptionalCandidates: []model.GovernedKnowledgeSelection{{Ref: model.KnowledgeRef{KnowledgeID: "frozen"}}}}
	current := frozen
	current.OptionalCandidates = nil
	if err := CompareRetrySelections(frozen, current, true, false); err == nil || err.Error() != "本次审查的法律来源或适用事实已改变，请发起新的审查" {
		t.Fatalf("legal precedence: %v", err)
	}
	if err := CompareRetrySelections(frozen, current, false, true); err == nil || err.Error() != "本次审查选择的可选制度已发生变化或被撤回" {
		t.Fatalf("optional: %v", err)
	}
}

func TestResolveCurrentRejectsCorruptActiveVersionBeforeSkipping(t *testing.T) {
	row, in := reviewSourceDeniedFixture(t, "guidance")
	row.ContentBytes = []byte(`{"kind":"legal"}`)
	repo := &reviewRepoStub{raw: row}
	facts := &reviewFactsStub{}
	_, err := ResolveCurrent(context.Background(), repo, facts, in)
	if repo.loads != 1 || facts.sourceCalls != 0 || err == nil || err.Error() != "无法解析生效制度的编号或内容" {
		t.Fatalf("loads=%d source=%d err=%v", repo.loads, facts.sourceCalls, err)
	}
}
