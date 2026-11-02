// File metadata (manifest mirror):
// Purpose: 实现 `TestPreparationDeltaAllowsStartAndRejectsUnrelatedChanges`, `TestPreparationGenericTransactionDenied`, `TestPreparationScopeExact`, `TestPreparationDeltaFreezesSelectedArtifact` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"testing"

	"contractaudit/internal/config"
	"contractaudit/internal/execution"
	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
)

func TestPreparationDeltaAllowsStartAndRejectsUnrelatedChanges(t *testing.T) {
	previous := state{Runs: []Run{{ID: "run", CaseID: "case", State: "accepted"}}, Attempts: []Attempt{{ID: "attempt", RunID: "run", State: "accepted", HistoryKind: "preparing"}}}
	previous.normalize()
	next, err := previous.clone()
	if err != nil {
		t.Fatal(err)
	}
	now := "2026-01-01T00:00:00Z"
	next.Attempts[0].State = "running"
	next.Attempts[0].StartedAt = &now
	next.Runs[0].State = "running"
	next.Runs[0].UpdatedAt = now
	appendRunEvent(&next, "run", "running")
	appendExecutionEvent(&next, ExecutionEvent{AttemptID: "attempt", NodeKind: "attempt", Phase: "execution", EventType: "attempt_started", At: now, Status: "running"})
	if err := validateEmbeddedPreparationDelta(previous, next, "run", "attempt"); err != nil {
		t.Fatalf("valid start: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*state)
	}{
		{"attempt identity", func(s *state) { s.Attempts[0].ActorID = "other" }},
		{"additional attempt", func(s *state) { s.Attempts = append(s.Attempts, Attempt{ID: "other"}) }},
		{"case", func(s *state) { s.Cases = append(s.Cases, Case{ID: "other"}) }},
		{"finding", func(s *state) { s.Findings = append(s.Findings, Finding{}) }},
		{"snapshot", func(s *state) { s.Snapshots = append(s.Snapshots, RunSnapshot{RunID: "run"}) }},
		{"selection", func(s *state) { s.AttemptParseSelections = append(s.AttemptParseSelections, AttemptParseSelection{}) }},
		{"model trace", func(s *state) { s.ExecutionEvents[0].Trace = &execution.ModelCallTrace{} }},
		{"conclusion event", func(s *state) { s.ExecutionEvents[0].EventType = "finding_verified" }},
		{"fabricated input", func(s *state) {
			s.AttemptInputs = append(s.AttemptInputs, AttemptInput{AttemptID: "attempt", RunID: "run", InputHash: "wrong"})
		}},
		{"completion", func(s *state) { s.Attempts[0].State = "completed" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate, err := next.clone()
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(&candidate)
			if validateEmbeddedPreparationDelta(previous, candidate, "run", "attempt") == nil {
				t.Fatal("unauthorized mutation accepted")
			}
		})
	}
}

func TestPreparationGenericTransactionDenied(t *testing.T) {
	ctx := model.WithDelegatedScope(context.Background(), model.DelegatedScope{Kind: "execution_prepare"})
	var s Store
	if _, err := s.beginContextTx(ctx, pgx.TxOptions{}); err == nil {
		t.Fatal("generic write accepted")
	}
}

func TestPreparationScopeExact(t *testing.T) {
	s := Store{sel: config.Selection{Mode: config.ModeDevelopment}}
	claim := JobClaim{TenantID: "t", OrgID: "o", CaseID: "c", ActorID: "u", RunID: "r", AttemptID: "a"}
	scope := model.DelegatedScope{Kind: "execution_prepare", Action: "embedded_prepare", OperationTicket: "ticket", AdmissionRevision: "revision", InputSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TenantID: "t", OrgID: "o", CaseID: "c", ActorID: "u", RunID: "r", AttemptID: "a"}
	if !s.validEmbeddedPreparationScope(scope, claim) {
		t.Fatal("valid scope rejected")
	}
	for _, mutate := range []func(*model.DelegatedScope){func(v *model.DelegatedScope) { v.JobID = "j" }, func(v *model.DelegatedScope) { v.AttemptID = "other" }, func(v *model.DelegatedScope) { v.ConfigurationID = "model" }, func(v *model.DelegatedScope) { v.OperationTicket = "" }, func(v *model.DelegatedScope) { v.InputSHA256 = "" }, func(v *model.DelegatedScope) { v.AdmissionRevision = "" }} {
		bad := scope
		mutate(&bad)
		if s.validEmbeddedPreparationScope(bad, claim) {
			t.Fatalf("accepted %+v", bad)
		}
	}
	s.sel.Mode = config.ModeProduction
	if s.validEmbeddedPreparationScope(scope, claim) {
		t.Fatal("production scope accepted")
	}
}

func TestPreparationDeltaFreezesSelectedArtifact(t *testing.T) {
	digest := digestBytes([]byte("original"))
	doc := Document{ID: "doc", CaseID: "case", Digest: digest}
	p := ParsedDocument{ID: "parsed", SelectionOrder: 1, DocumentID: "doc", OriginalDigest: digest, ParserID: "parser", ParserVersion: "1", ConfigHash: digestBytes([]byte("config")), NormalizationVersion: "text-normalization-v1", CreatedAt: "2026-01-01T00:00:00Z", Status: "failed", ParseError: "parse failed"}
	payload, err := p.canonicalPayload()
	if err != nil {
		t.Fatal(err)
	}
	p.ArtifactDigest = digestBytes(payload)
	frozen := frozenFromParsed(doc, p)
	snapshot := RunSnapshot{RunID: "run", DocumentIDs: []string{"doc"}, ParsedDocumentIDs: []string{"parsed"}, ParseSelectionHash: selectionHash([]FrozenDocument{frozen}), ParserConfigurationID: frozenConfigHash([]FrozenDocument{frozen})}
	selection := AttemptParseSelection{AttemptID: "attempt", RunID: "run", DocumentID: "doc", OriginalDigest: digest, ParsedDocumentID: p.ID, SelectionOrder: p.SelectionOrder, ParserID: p.ParserID, ParserVersion: p.ParserVersion, ConfigHash: p.ConfigHash, NormalizationVersion: p.NormalizationVersion}
	previous := state{Runs: []Run{{ID: "run", CaseID: "case", State: "running"}}, Attempts: []Attempt{{ID: "attempt", RunID: "run", State: "running", HistoryKind: "preparing"}}, Documents: []Document{doc}, Snapshots: []RunSnapshot{snapshot}, AttemptParseSelections: []AttemptParseSelection{selection}}
	started := "2026-01-01T00:00:00Z"
	previous.Attempts[0].StartedAt = &started
	previous.normalize()
	next, err := previous.clone()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := frozenInputHash(snapshot, []FrozenDocument{frozen})
	if err != nil {
		t.Fatal(err)
	}
	next.ParsedDocuments = append(next.ParsedDocuments, p)
	next.AttemptInputs = append(next.AttemptInputs, AttemptInput{AttemptID: "attempt", RunID: "run", InputHash: hash, FrozenAt: "2026-01-01T00:00:00Z", Documents: []FrozenDocument{frozen}})
	next.Attempts[0].InputHash = &hash
	next.Attempts[0].HistoryKind = "frozen"
	if err := validateEmbeddedPreparationDelta(previous, next, "run", "attempt"); err != nil {
		t.Fatalf("valid freeze: %v", err)
	}
	next.ParsedDocuments[0].ParseError = "tampered"
	if validateEmbeddedPreparationDelta(previous, next, "run", "attempt") == nil {
		t.Fatal("tampered artifact accepted")
	}
}

func TestPreparationContractScopeModes(t *testing.T) {
	claim := JobClaim{TenantID: "t", OrgID: "o", CaseID: "c", ActorID: "u", RunID: "r", AttemptID: "a"}
	scope := model.DelegatedScope{Kind: "execution_prepare", Action: "embedded_prepare", OperationTicket: "ticket", AdmissionRevision: "revision", InputSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TenantID: "t", OrgID: "o", CaseID: "c", ActorID: "u", RunID: "r", AttemptID: "a"}
	for _, mode := range []string{config.ModeDevelopment, config.ModeTest} {
		s := Store{sel: config.Selection{Mode: mode}}
		if !s.validEmbeddedPreparationScope(scope, claim) {
			t.Fatalf("mode %s rejected", mode)
		}
	}
	for _, mode := range []string{config.ModeProduction, "", "other"} {
		s := Store{sel: config.Selection{Mode: mode}}
		if s.validEmbeddedPreparationScope(scope, claim) {
			t.Fatalf("mode %s accepted", mode)
		}
	}
	s := Store{sel: config.Selection{Mode: config.ModeTest}}
	scope.Action = "embedded_review"
	if s.validEmbeddedPreparationScope(scope, claim) {
		t.Fatal("wrong action accepted")
	}
	scope.Action = "embedded_prepare"
	s.serviceOwner = "core"
	if s.validEmbeddedPreparationScope(scope, claim) {
		t.Fatal("service owner accepted")
	}
	s.serviceOwner = ""
	claim.JobID = "job"
	if s.validEmbeddedPreparationScope(scope, claim) {
		t.Fatal("job accepted")
	}
}

func TestPreparationContractMaterialIdentity(t *testing.T) {
	doc := Document{ID: "d", TenantID: "t", OrgID: "o", CaseID: "c"}
	parsed := ParsedDocument{ID: "p", DocumentID: "d"}
	st := state{Materials: []MaterialEntity{{TenantID: "t", OrgID: "o", CaseID: "c", ID: "m", Role: "draft_contract", Purpose: "primary_contract"}}, MaterialVersions: []MaterialVersion{{TenantID: "t", OrgID: "o", CaseID: "c", MaterialID: "m", DocumentID: "d", Version: 2}}}
	frozen := frozenFromParsed(doc, parsed)
	bindFrozenMaterialIdentity(st, &frozen)
	if !preparationFrozenMatches(st, doc, parsed, frozen) {
		t.Fatal("bound material rejected")
	}
	forged := frozen
	wrong := *frozen.Material
	wrong.Version++
	forged.Material = &wrong
	if preparationFrozenMatches(st, doc, parsed, forged) {
		t.Fatal("forged version accepted")
	}
	forged = frozen
	forged.Material = nil
	if preparationFrozenMatches(st, doc, parsed, forged) {
		t.Fatal("omitted identity accepted")
	}
	other := st
	other.Materials = append([]MaterialEntity(nil), st.Materials...)
	other.Materials[0].OrgID = "wrong"
	if preparationFrozenMatches(other, doc, parsed, frozen) {
		t.Fatal("other organization accepted")
	}
	st.MaterialVersions = append(st.MaterialVersions, st.MaterialVersions[0])
	if preparationFrozenMatches(st, doc, parsed, frozen) {
		t.Fatal("duplicate identity accepted")
	}
}

func TestPreparationContractTerminal(t *testing.T) {
	for _, status := range []string{"failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			started := "2026-01-01T00:00:00Z"
			previous := state{Runs: []Run{{ID: "run", CaseID: "case", State: "running"}}, Attempts: []Attempt{{ID: "attempt", RunID: "run", State: "running", StartedAt: &started, HistoryKind: "preparing"}}}
			previous.normalize()
			next, err := previous.clone()
			if err != nil {
				t.Fatal(err)
			}
			if err := applyEmbeddedPreparationTerminal(&next, "run", "attempt", status, "parse_failed"); err != nil {
				t.Fatal(err)
			}
			if next.Attempts[0].OutputHash != nil || len(next.Findings) != 0 {
				t.Fatal("terminal created review result")
			}
			if err := validateEmbeddedPreparationDelta(previous, next, "run", "attempt"); err != nil {
				t.Fatalf("valid terminal: %v", err)
			}
			hash := "fake"
			next.Attempts[0].OutputHash = &hash
			if validateEmbeddedPreparationDelta(previous, next, "run", "attempt") == nil {
				t.Fatal("fabricated output hash accepted")
			}
		})
	}
}
