package store

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
)

func TestPartyContextSnapshotPersistsAcrossStoreReopen(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	core := env.openRole(t, "core")
	execution := env.openRole(t, "execution")
	caseID, err := core.CreateCase(admin, "主体上下文冻结持久化", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	validFrom := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	sourceTime := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	contextItems := []PartyContextItem{{BindingID: "binding-party-freeze", BindingVersion: 2, Role: SubjectRoleSupplier, BindingState: "pending_confirmation",
		SubjectID: "subject-party-freeze", SubjectVersion: 3, DisplayName: "演示冻结主体", VerificationState: "verified", VerificationScope: "synthetic_controlled",
		SourceCatalogID: syntheticSubjectSourceID, SourceSnapshotID: "snapshot-party-freeze", SourceDigest: strings.Repeat("c", 64), SourceTime: &sourceTime, SourceValidFrom: &validFrom}}
	contextBytes, err := json.Marshal(contextItems)
	if err != nil {
		t.Fatal(err)
	}
	contextHash := hashJSON(contextBytes)
	runID := "run_party_context_reopen"
	snapshot := RunSnapshot{RunID: runID, DocumentIDs: []string{"doc-party-freeze"}, ParsedDocumentIDs: []string{"parse-party-freeze"},
		RuleSetHash: "rules-party-freeze", PlaybookHash: "playbook-party-freeze", ComparatorRef: "comparator-party-freeze",
		PartyContext: contextItems, PartyContextHash: contextHash}
	if err := execution.updateAs(admin, func(st *state) error {
		now := stamp()
		st.Runs = append(st.Runs, Run{ID: runID, CaseID: caseID, State: "accepted", CreatedAt: now, UpdatedAt: now})
		return sealSnapshot(st, runID, snapshot)
	}); err != nil {
		t.Fatal(err)
	}
	before, err := execution.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	frozenBefore := before.snapshotOf(runID)
	inputHashBefore, err := frozenInputHash(frozenBefore, []FrozenDocument{})
	if err != nil {
		t.Fatal(err)
	}
	if err := execution.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := env.openRole(t, "execution")
	after, err := reopened.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	frozenAfter := after.snapshotOf(runID)
	inputHashAfter, err := frozenInputHash(frozenAfter, []FrozenDocument{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(frozenAfter.PartyContext, contextItems) || frozenAfter.PartyContextHash != contextHash || inputHashBefore != inputHashAfter {
		t.Fatalf("party context changed across store reopen: before=%+v after=%+v input hashes=%s/%s", frozenBefore, frozenAfter, inputHashBefore, inputHashAfter)
	}
	if got := hashJSON(mustJSON(t, frozenAfter.PartyContext)); got != frozenAfter.PartyContextHash {
		t.Fatalf("reopened party context payload/hash mismatch: %s vs %s", got, frozenAfter.PartyContextHash)
	}
	var storedPayload []byte
	var storedHash string
	if err := reopened.pool.QueryRow(context.Background(), `SELECT party_context,party_context_hash FROM review_run_snapshots WHERE run_id=$1`, runID).Scan(&storedPayload, &storedHash); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(storedPayload, contextBytes) || storedHash != contextHash {
		t.Fatalf("database snapshot columns do not preserve exact canonical context: payload=%s hash=%s", storedPayload, storedHash)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
