// File metadata (manifest mirror):
// Purpose: Pins frozen identity wire compatibility, exact selection fields and historical artifact rules, and input immutability of pure hash operations.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

type hashTestParty struct {
	Party string `json:"party"`
}

type hashTestKnowledge struct {
	IDs []string `json:"ids"`
}

type hashTestOperator struct {
	Mode string `json:"mode"`
}

type hashTestOriginal struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

func (d hashTestOriginal) OriginalProof() OriginalDocumentProof {
	return OriginalDocumentProof{ID: d.ID, Digest: d.Digest}
}

func hashTestFixedJSON(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func TestFrozenInputHashPreservesV1AndV2LiteralWire(t *testing.T) {
	configDigest := "0000000000000000000000000000000000000000000000000000000000000000"
	snapshot := RunSnapshot[hashTestParty, hashTestKnowledge, hashTestOperator]{
		RunID:                        "run-original",
		DocumentIDs:                  []string{"doc-b", "doc-a"},
		PartyContext:                 []hashTestParty{{Party: "party-1"}},
		PartyContextHash:             "party-hash",
		ServiceObjectContext:         &hashTestOperator{Mode: "operator"},
		ServiceObjectContextHash:     "operator-hash",
		ParsedDocumentIDs:            []string{"parse-b", "parse-a"},
		ParseSelectionHash:           "selection-hash",
		ParserConfigurationID:        "config-hash",
		RuleSetHash:                  "rules-hash",
		PlaybookHash:                 "playbook-hash",
		ModelRef:                     "model-ref",
		ComparatorRef:                "compare-ref",
		CloudPolicySnapshot:          json.RawMessage(`{"revision":2}`),
		CloudPolicyDigest:            "policy-hash",
		GovernedKnowledge:            &hashTestKnowledge{IDs: []string{"knowledge-1"}},
		AdoptedTenantConfigVersionID: "cfg-v7",
		AdoptedTenantConfigDigest:    configDigest,
		InputHashVersion:             1,
	}
	documents := []FrozenDocument[hashTestOriginal]{}

	const v1Wire = `{"snapshot":{"run_id":"","document_ids":["doc-a","doc-b"],"party_context":[{"party":"party-1"}],"party_context_hash":"party-hash","service_object_context":{"mode":"operator"},"service_object_context_hash":"operator-hash","parsed_document_ids":["parse-a","parse-b"],"parse_selection_hash":"selection-hash","parser_configuration_id":"config-hash","rule_set_hash":"rules-hash","playbook_hash":"playbook-hash","model_ref":"model-ref","comparator_ref":"compare-ref","cloud_policy_snapshot":{"revision":2},"cloud_policy_digest":"policy-hash","governed_knowledge":{"ids":["knowledge-1"]}},"documents":[]}`
	gotV1, err := FrozenInputHash(snapshot, documents)
	if err != nil || gotV1 != hashTestFixedJSON(v1Wire) {
		t.Fatalf("v1 frozen input wire changed: hash=%q err=%v", gotV1, err)
	}
	if snapshot.RunID != "run-original" || snapshot.DocumentIDs[0] != "doc-b" || snapshot.ParsedDocumentIDs[0] != "parse-b" {
		t.Fatal("v1 hashing changed the caller's run identity or shared ID slices")
	}

	legacy := snapshot
	legacy.InputHashVersion = 0
	gotLegacy, err := FrozenInputHash(legacy, documents)
	if err != nil || gotLegacy != hashTestFixedJSON(v1Wire) {
		t.Fatalf("unversioned historical input stopped using v1: hash=%q err=%v", gotLegacy, err)
	}

	snapshot.InputHashVersion = 2
	const v2Wire = `{"domain":"review-frozen-input/v2","config_version_id":"cfg-v7","config_digest":"0000000000000000000000000000000000000000000000000000000000000000","snapshot":{"run_id":"","document_ids":["doc-a","doc-b"],"party_context":[{"party":"party-1"}],"party_context_hash":"party-hash","service_object_context":{"mode":"operator"},"service_object_context_hash":"operator-hash","parsed_document_ids":["parse-a","parse-b"],"parse_selection_hash":"selection-hash","parser_configuration_id":"config-hash","rule_set_hash":"rules-hash","playbook_hash":"playbook-hash","model_ref":"model-ref","comparator_ref":"compare-ref","cloud_policy_snapshot":{"revision":2},"cloud_policy_digest":"policy-hash","governed_knowledge":{"ids":["knowledge-1"]}},"documents":[]}`
	gotV2, err := FrozenInputHash(snapshot, documents)
	if err != nil || gotV2 != hashTestFixedJSON(v2Wire) {
		t.Fatalf("v2 frozen input wire changed: hash=%q err=%v", gotV2, err)
	}
	if snapshot.RunID != "run-original" || snapshot.DocumentIDs[0] != "doc-b" || snapshot.ParsedDocumentIDs[0] != "parse-b" {
		t.Fatal("v2 hashing changed the caller's run identity or shared ID slices")
	}
}

func TestFrozenInputHashPreservesNilAndEmptySliceWire(t *testing.T) {
	type frozenSnapshot = RunSnapshot[struct{}, struct{}, struct{}]
	type frozenDocuments = []FrozenDocument[hashTestOriginal]
	tests := []struct {
		name        string
		documentIDs []string
		documents   frozenDocuments
		wire        string
	}{
		{
			name: "nil slices",
			wire: `{"snapshot":{"run_id":"","document_ids":null,"rule_set_hash":"","playbook_hash":"","model_ref":"","comparator_ref":""},"documents":null}`,
		},
		{
			name:        "empty document IDs",
			documentIDs: []string{},
			wire:        `{"snapshot":{"run_id":"","document_ids":[],"rule_set_hash":"","playbook_hash":"","model_ref":"","comparator_ref":""},"documents":null}`,
		},
		{
			name:      "empty documents",
			documents: frozenDocuments{},
			wire:      `{"snapshot":{"run_id":"","document_ids":null,"rule_set_hash":"","playbook_hash":"","model_ref":"","comparator_ref":""},"documents":[]}`,
		},
		{
			name:        "both empty",
			documentIDs: []string{},
			documents:   frozenDocuments{},
			wire:        `{"snapshot":{"run_id":"","document_ids":[],"rule_set_hash":"","playbook_hash":"","model_ref":"","comparator_ref":""},"documents":[]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := frozenSnapshot{DocumentIDs: test.documentIDs}
			got, err := FrozenInputHash(snapshot, test.documents)
			if err != nil || got != hashTestFixedJSON(test.wire) {
				t.Fatalf("frozen input nil/empty wire changed: hash=%q err=%v", got, err)
			}
		})
	}
}

func TestFrozenInputHashLeavesDocumentOrderAndValuesUntouched(t *testing.T) {
	snapshot := RunSnapshot[struct{}, struct{}, struct{}]{
		RunID: "run", DocumentIDs: []string{"doc-b", "doc-a"}, ParsedDocumentIDs: []string{"parse-b", "parse-a"},
	}
	documents := []FrozenDocument[hashTestOriginal]{
		{Document: hashTestOriginal{ID: "doc-b", Digest: "digest-b"}, ParsedDocumentID: "parse-b"},
		{Document: hashTestOriginal{ID: "doc-a", Digest: "digest-a"}, ParsedDocumentID: "parse-a"},
	}
	first, err := FrozenInputHash(snapshot, documents)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RunID != "run" || snapshot.DocumentIDs[0] != "doc-b" || snapshot.ParsedDocumentIDs[0] != "parse-b" ||
		documents[0].Document.ID != "doc-b" || documents[1].Document.ID != "doc-a" {
		t.Fatal("pure frozen hashing mutated caller-owned snapshot slices or documents")
	}

	reversed := []FrozenDocument[hashTestOriginal]{documents[1], documents[0]}
	second, err := FrozenInputHash(snapshot, reversed)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("frozen document order stopped contributing to the v1 input identity")
	}
}

func TestSelectionHashUsesExactStablePairsAndConfigHashDeduplicates(t *testing.T) {
	documents := []FrozenDocument[hashTestOriginal]{
		{Document: hashTestOriginal{ID: "doc-z"}, ParsedDocumentID: "parse-z", SelectionOrder: 4,
			ConfigHash: "config-z", ParserID: "parser-z", ParserVersion: "v3", NormalizationVersion: "norm-z"},
		{Document: hashTestOriginal{ID: "doc-a"}, ParsedDocumentID: "parse-a-1", SelectionOrder: 1,
			ConfigHash: "config-a", ParserID: "parser-a", ParserVersion: "v1", NormalizationVersion: "norm-a"},
		{Document: hashTestOriginal{ID: "doc-a"}, ParsedDocumentID: "parse-a-2", SelectionOrder: 2,
			ConfigHash: "config-a", ParserID: "parser-b", ParserVersion: "v2", NormalizationVersion: "norm-a"},
	}
	const selectionWire = `[["doc-a","parse-a-1","1","config-a","parser-a","v1","norm-a"],["doc-a","parse-a-2","2","config-a","parser-b","v2","norm-a"],["doc-z","parse-z","4","config-z","parser-z","v3","norm-z"]]`
	wantSelection := hashTestFixedJSON(selectionWire)
	if got := SelectionHash(documents); got != wantSelection {
		t.Fatalf("selection pair fields/order changed: got=%q want=%q", got, wantSelection)
	}
	if got := SelectionHash([]FrozenDocument[hashTestOriginal]{documents[1], documents[0], documents[2]}); got != wantSelection {
		t.Fatal("selection hash stopped sorting by original document ID")
	}
	if got := SelectionHash([]FrozenDocument[hashTestOriginal]{documents[2], documents[1], documents[0]}); got == wantSelection {
		t.Fatal("equal-ID selection pairs no longer retain stable input order")
	}

	const configWire = `["config-a","config-z"]`
	if got := FrozenConfigHash(documents); got != hashTestFixedJSON(configWire) {
		t.Fatalf("configuration hashes stopped sorting and deduplicating: %q", got)
	}
}

func TestFrozenSelectionMatchingKeepsLegacyArtifactCompatibility(t *testing.T) {
	documents := []FrozenDocument[hashTestOriginal]{
		{Document: hashTestOriginal{ID: "doc-a", Digest: "original-a"}, ParsedDocumentID: "parse-a",
			SelectionOrder: 3, ConfigHash: "config-a", ParserID: "parser-a", ParserVersion: "v1",
			NormalizationVersion: "text-normalization-v1", ParsedArtifactDigest: "artifact-a"},
	}
	selection := AttemptParseSelection{
		DocumentID: "doc-a", OriginalDigest: "original-a", ParsedDocumentID: "parse-a", SelectionOrder: 3,
		ParserID: "parser-a", ParserVersion: "v1", ConfigHash: "config-a", NormalizationVersion: "text-normalization-v1",
	}
	if !AttemptSelectionsMatchFrozen([]AttemptParseSelection{selection}, documents) {
		t.Fatal("historical selection without an artifact digest was rejected")
	}
	selection.ArtifactDigest = "artifact-a"
	if !AttemptSelectionsMatchFrozen([]AttemptParseSelection{selection}, documents) {
		t.Fatal("exact frozen artifact digest was rejected")
	}
	selection.ArtifactDigest = "artifact-other"
	if AttemptSelectionsMatchFrozen([]AttemptParseSelection{selection}, documents) {
		t.Fatal("non-empty artifact digest mismatch was accepted")
	}

	twoDocuments := append(documents, FrozenDocument[hashTestOriginal]{
		Document: hashTestOriginal{ID: "doc-b", Digest: "original-b"}, ParsedDocumentID: "parse-b",
	})
	if AttemptSelectionsMatchFrozen([]AttemptParseSelection{selection, selection}, twoDocuments) {
		t.Fatal("duplicate document selection IDs were accepted")
	}

	snapshot := RunSnapshot[struct{}, struct{}, struct{}]{
		ParsedDocumentIDs: []string{"parse-a"}, ParseSelectionHash: SelectionHash(documents),
		ParserConfigurationID: FrozenConfigHash(documents),
	}
	if !FrozenInputMatchesSnapshot(snapshot, documents) {
		t.Fatal("exact frozen selection identities did not match their snapshot")
	}
	snapshot.ParsedDocumentIDs = []string{"parse-other"}
	if FrozenInputMatchesSnapshot(snapshot, documents) {
		t.Fatal("a different parsed-document set matched the frozen snapshot")
	}
}

func TestAttemptOutputHashPinsPackAndAgentAndExcludesHumanVersion(t *testing.T) {
	findings := []Finding{{ID: "finding-1", Version: 17}}
	evaluations := []ConstraintEval{{
		ID: "eval-1", RunID: "run-1", AttemptID: "attempt-1", CaseID: "case-1",
		Domain: "service", RuleVersion: "v1", Relation: "equal", Reason: "observed",
	}}
	const outputWire = `{"findings":[{"id":"finding-1","run_id":"","attempt_id":"","case_id":"","origin":"","domain":"","claim":"","relation":"","severity":null,"severity_source":null,"version":0,"anchors":null,"created_at":""}],"evaluations":[{"id":"eval-1","run_id":"run-1","attempt_id":"attempt-1","case_id":"case-1","domain":"service","rule_version":"v1","relation":"equal","reason":"observed"}],"rulepack_hash":"pack-hash","rulepack_snapshot_digest":"snapshot-digest","rulepack_compiler_ref":"compiler-ref","agent_review_sha256":"agent-digest"}`
	got, err := AttemptOutputHash(findings, evaluations, "pack-hash", "snapshot-digest", "compiler-ref", "agent-digest")
	if err != nil || got != hashTestFixedJSON(outputWire) {
		t.Fatalf("output hash fields or wire order changed: hash=%q err=%v", got, err)
	}
	if findings[0].Version != 17 {
		t.Fatal("output hash modified the caller's human concurrency version")
	}
	findings[0].Version = 2
	withoutHumanVersion, err := AttemptOutputHash(findings, evaluations, "pack-hash", "snapshot-digest", "compiler-ref", "agent-digest")
	if err != nil || withoutHumanVersion != got {
		t.Fatal("finding Version became part of frozen output identity")
	}
	if findings[0].Version != 2 {
		t.Fatal("output hash did not preserve a second caller-owned Version")
	}

	changedPack, err := AttemptOutputHash(findings, evaluations, "other-pack", "snapshot-digest", "compiler-ref", "agent-digest")
	if err != nil || changedPack == got {
		t.Fatal("output hash stopped binding the frozen RulePack identity")
	}
	changedAgent, err := AttemptOutputHash(findings, evaluations, "pack-hash", "snapshot-digest", "compiler-ref", "other-agent")
	if err != nil || changedAgent == got {
		t.Fatal("output hash stopped binding the exact agent-review digest")
	}
}

func TestAttemptOutputHashPreservesNilAndEmptySlices(t *testing.T) {
	const nilWire = `{"findings":null,"evaluations":null}`
	nilHash, err := AttemptOutputHash(nil, nil, "", "", "", "")
	if err != nil || nilHash != hashTestFixedJSON(nilWire) {
		t.Fatalf("nil output slices changed wire identity: hash=%q err=%v", nilHash, err)
	}

	const emptyWire = `{"findings":[],"evaluations":[]}`
	emptyHash, err := AttemptOutputHash([]Finding{}, []ConstraintEval{}, "", "", "", "")
	if err != nil || emptyHash != hashTestFixedJSON(emptyWire) {
		t.Fatalf("non-nil empty output slices changed wire identity: hash=%q err=%v", emptyHash, err)
	}
	if nilHash == emptyHash {
		t.Fatal("nil and empty output slices collapsed to one frozen identity")
	}
}
