// File metadata (manifest mirror):
// Purpose: Characterizes candidate identity checks before feedback/ancestor reads and denied exact reads returning the original error without restricted content.
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
	"errors"
	"reflect"
	"time"

	"contractaudit/internal/fault"
	"testing"

	"contractaudit/internal/knowledge/model"
)

type readAuthFacts struct {
	KnowledgeReadFacts
	candidate                          ExperienceCandidateFacts
	source, loads, feedback, ancestors int
	sourceErr                          error
}

func (f *readAuthFacts) AuthorizeReadSource(context.Context, model.GovernedKnowledgeContent) error {
	f.source++
	return f.sourceErr
}
func (f *readAuthFacts) LoadReadCandidate(context.Context, model.GovernedKnowledgeContent) (ExperienceCandidateFacts, error) {
	f.loads++
	return f.candidate, nil
}
func (f *readAuthFacts) CandidateFeedbackExists(context.Context, ExperienceCandidateFacts) (bool, error) {
	f.feedback++
	return true, nil
}
func (f *readAuthFacts) AuthorizeReadAncestors(context.Context, ExperienceCandidateFacts) error {
	f.ancestors++
	return nil
}
func TestReadAuthorizationChecksCandidateIdentityBeforeFeedbackAndAncestors(t *testing.T) {
	raw, _ := json.Marshal([]any{"experience-candidate-version/v1", "tenant", "candidate", "job", int64(1), "result"})
	candidate := ExperienceCandidateFacts{TenantID: "tenant", CandidateID: "candidate", JobID: "job", Generation: 1, ResultSHA256: "result", VersionID: model.SHA256(raw), ContentSHA256: model.SHA256([]byte("body"))}
	content := model.GovernedKnowledgeContent{Kind: "experience", SourceCaseID: "case", CandidateID: candidate.CandidateID, CandidateVersionID: candidate.VersionID, CandidateSHA256: candidate.ContentSHA256}
	f := &readAuthFacts{candidate: candidate}
	if err := authorizeKnowledgeRead(context.Background(), f, content); err != nil || f.source != 1 || f.loads != 1 || f.feedback != 1 || f.ancestors != 1 {
		t.Fatalf("facts=%+v err=%v", f, err)
	}
	content.CandidateSHA256 = model.SHA256([]byte("other"))
	f = &readAuthFacts{candidate: candidate}
	if err := authorizeKnowledgeRead(context.Background(), f, content); err == nil || f.feedback != 0 || f.ancestors != 0 {
		t.Fatalf("facts=%+v err=%v", f, err)
	}
}

type readVersionStub struct {
	raw   RawVersion
	calls int
}

func (r *readVersionStub) LoadVersion(context.Context, string, string, string) (RawVersion, error) {
	r.calls++
	return r.raw, nil
}

func TestGetVersionReturnsZeroOnSourceAuthorizationFailure(t *testing.T) {
	tenant, org, caseID := "tenant", "org", "case"
	digest := model.SHA256([]byte("pack"))
	content := model.GovernedKnowledgeContent{
		Encoding: model.ContentEncoding, KnowledgeID: "guide", Version: "1", Kind: "guidance", Title: "title", Body: "secret", Summary: "summary", SourceType: "reference", SourceReference: "source",
		Scope: model.KnowledgeScope{OrgID: org, CaseID: caseID, BusinessLine: "ENT", Scenario: "scenario", ContractType: "service"}, SourceOrgID: org, SourceCaseID: caseID,
		ValidFrom: "2020-01-01T00:00:00Z", ValidUntil: "2100-01-01T00:00:00Z",
		Binding: model.PolicyRuleBinding{PackID: "pack", PackVersion: "1", PackContentHash: digest, PackSnapshotDigest: digest, CompilerRef: model.RulePackCompilerRef, RuleIDs: []string{"rule"}, SourceSpans: []model.KnowledgeSourceSpan{{RuleID: "rule", Start: 0, End: 6, Quote: "secret"}}, CandidateDiff: "diff", ValidationEvidence: "evidence"},
	}
	body, hash, err := model.ContentDigest(content)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := json.Marshal(content.Binding)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := model.ScopeKey(content.Scope)
	if err != nil {
		t.Fatal(err)
	}
	versionID := model.VersionID(tenant, content.KnowledgeID, content.Version)
	row := RawVersion{Version: model.GovernedKnowledgeVersion{Ref: model.KnowledgeRef{KnowledgeID: content.KnowledgeID, VersionID: versionID, ContentSHA256: hash}, Status: "draft", Revision: 1, CreatedBy: "actor"}, ContentBytes: body, Binding: binding, ScopeKey: scope, Kind: content.Kind, DisplayVersion: content.Version, ScopeOrg: &org, ScopeCase: &caseID, SourceOrg: &org, SourceCase: &caseID, BindingPackID: "pack", BindingPackVersion: "1", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	if _, err := ValidateRawVersion(context.Background(), &readAuthFacts{}, tenant, row, nil); err != nil {
		t.Fatalf("fixture failed integrity: %v", err)
	}
	for _, denied := range []error{fault.NotFound("source denied"), fault.Forbidden("source forbidden")} {
		t.Run(denied.Error(), func(t *testing.T) {
			repo := &readVersionStub{raw: row}
			facts := &readAuthFacts{sourceErr: denied}
			out, err := GetGovernedKnowledgeVersion(context.Background(), repo, facts, tenant, content.KnowledgeID, versionID)
			if !errors.Is(err, denied) || facts.source != 1 || repo.calls != 1 || !reflect.DeepEqual(out, model.GovernedKnowledgeVersion{}) {
				t.Fatalf("out=%+v err=%v facts=%+v calls=%d", out, err, facts, repo.calls)
			}
		})
	}
}
