package store

import (
	"context"
	"strings"
	"testing"
)

func TestReopenRejectsFrozenInputWithMissingConfigHash(t *testing.T) {
	assertReopenRejectsFrozenInputWithMissingProvenance(t, "config_hash")
}

func TestReopenRejectsFrozenInputWithMissingParsedDocumentID(t *testing.T) {
	assertReopenRejectsFrozenInputWithMissingProvenance(t, "parsed_document_id")
}

func assertReopenRejectsFrozenInputWithMissingProvenance(t *testing.T, field string) {
	t.Helper()
	st, admin := openExecutionStore(t)
	caseID := createExecutionCase(t, st, admin)
	runID, err := st.StartReview(context.Background(), admin, caseID)
	if err != nil {
		t.Fatal(err)
	}
	workspace := waitTerminalWorkspace(t, st, admin, caseID, runID)
	if workspace.Execution == nil || workspace.Execution.Attempt == nil {
		t.Fatal("completed C1 run has no attempt")
	}
	attemptID := workspace.Execution.Attempt.ID
	if workspace.Execution.Attempt.HistoryKind != "frozen" {
		t.Fatalf("test setup did not produce frozen C1 history: %+v", workspace.Execution.Attempt)
	}

	before := st.snapshot()
	input, ok := before.attemptInput(attemptID)
	if !ok || input.InputHash == "" || len(input.Documents) == 0 {
		t.Fatalf("test setup did not produce a complete frozen input: %+v", input)
	}
	snapshot := before.snapshotOf(runID)
	selections := before.parseSelectionsForAttempt(attemptID)
	if len(snapshot.ParsedDocumentIDs) != len(input.Documents) ||
		len(selections) != len(input.Documents) || !attemptSelectionsMatchFrozen(selections, input.Documents) {
		t.Fatalf("test setup did not retain complete C1 parse provenance: snapshot=%+v selections=%+v documents=%+v", snapshot, selections, input.Documents)
	}
	for _, document := range input.Documents {
		if !validParsedID(document.ParsedDocumentID) || document.ConfigHash == "" {
			t.Fatalf("test setup has incomplete C1 provenance: %+v", document)
		}
	}

	if field != "config_hash" && field != "parsed_document_id" {
		t.Fatalf("unsupported provenance field %q", field)
	}
	const disableTrigger = `ALTER TABLE review_attempt_inputs DISABLE TRIGGER review_attempt_inputs_append_only`
	const enableTrigger = `ALTER TABLE review_attempt_inputs ENABLE TRIGGER review_attempt_inputs_append_only`
	if _, err := st.pool.Exec(context.Background(), disableTrigger); err != nil {
		t.Fatal(err)
	}
	triggerDisabled := true
	defer func() {
		if triggerDisabled {
			if _, err := st.pool.Exec(context.Background(), enableTrigger); err != nil {
				t.Errorf("restore review_attempt_inputs append-only trigger: %v", err)
			}
		}
	}()
	result, err := st.pool.Exec(context.Background(), `UPDATE review_attempt_inputs
		SET documents = jsonb_set(documents::jsonb, ARRAY['0', $2]::text[], to_jsonb(''::text), false)::text
		WHERE attempt_id = $1`, attemptID, field)
	if err != nil {
		t.Fatal(err)
	}
	if result.RowsAffected() != 1 {
		t.Fatalf("tampered %d frozen input rows, want exactly one", result.RowsAffected())
	}
	if _, err := st.pool.Exec(context.Background(), enableTrigger); err != nil {
		t.Fatal(err)
	}
	triggerDisabled = false

	var storedHash, storedProvenance string
	if err := st.pool.QueryRow(context.Background(), `SELECT input_hash, documents::jsonb #>> ARRAY['0', $2]::text[]
		FROM review_attempt_inputs WHERE attempt_id = $1`, attemptID, field).Scan(&storedHash, &storedProvenance); err != nil {
		t.Fatal(err)
	}
	if storedHash != input.InputHash {
		t.Fatalf("tampering changed frozen input hash: got %q, want %q", storedHash, input.InputHash)
	}
	if storedProvenance != "" {
		t.Fatalf("test setup failed to remove exactly the %s provenance value: %q", field, storedProvenance)
	}

	reopened, err := st.Reopen()
	if err == nil {
		defer reopened.Close()
		migrated, found := reopened.snapshot().attemptByID(attemptID)
		if found && migrated.HistoryKind == "legacy_unfrozen" {
			legacyWorkspace, workspaceErr := reopened.Workspace(admin, caseID, runID, attemptID)
			if workspaceErr == nil {
				t.Fatalf("Reopen relabeled corrupted C1 %s as legacy_unfrozen and displayed %d documents", field, len(legacyWorkspace.Documents))
			}
			t.Fatalf("Reopen relabeled corrupted C1 %s as legacy_unfrozen: workspace error %v", field, workspaceErr)
		}
		t.Fatalf("Reopen accepted frozen C1 input with missing %s provenance (history_kind=%q)", field, migrated.HistoryKind)
	}
	if want := "frozen attempt " + attemptID + " input does not match its immutable selection"; !strings.Contains(err.Error(), want) {
		t.Fatalf("Reopen rejected corruption for an unexpected reason: got %v, want error containing %q", err, want)
	}
	var historyKind string
	if err := st.pool.QueryRow(context.Background(), `SELECT history_kind FROM review_attempts WHERE id = $1`, attemptID).Scan(&historyKind); err != nil {
		t.Fatal(err)
	}
	if historyKind != "frozen" {
		t.Fatalf("failed Reopen changed C1 history kind: got %q, want frozen", historyKind)
	}
}
