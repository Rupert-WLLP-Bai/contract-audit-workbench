package store

import (
	"context"
	"encoding/json"
	"testing"
)

func TestEvaluationProvenanceMigrationPreservesHistoricalRows(t *testing.T) {
	st, _ := openExecutionStore(t)
	ctx := context.Background()
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	// The temporary table shadows the application's table, allowing the actual
	// forward migration to run against a pre-migration row without changing any
	// persistent evaluation or report.
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE constraint_evaluations (id text PRIMARY KEY, record_json text NOT NULL, output_hash text NOT NULL, frozen_report text NOT NULL) ON COMMIT DROP`); err != nil {
		t.Fatal(err)
	}
	legacy := ConstraintEval{ID: "legacy", RunID: "run", AttemptID: "attempt", CaseID: "case", Domain: "technical",
		RuleVersion: "1", Relation: "compatible", Reason: "historical"}
	record, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	before := state{Attempts: []Attempt{{ID: legacy.AttemptID, RunID: legacy.RunID}}, Evaluations: []ConstraintEval{legacy}}
	hash, err := attemptOutputHash(before, legacy.RunID, legacy.AttemptID, "")
	if err != nil {
		t.Fatal(err)
	}
	const report = `{"historical":"immutable"}`
	if _, err := tx.Exec(ctx, `INSERT INTO constraint_evaluations(id,record_json,output_hash,frozen_report) VALUES ($1,$2,$3,$4)`, legacy.ID, string(record), hash, report); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := tx.Exec(ctx, EvaluationProvenanceSchemaSQL); err != nil {
			t.Fatal(err)
		}
	}
	var gotRecord, gotHash, gotReport string
	var noEvidence bool
	if err := tx.QueryRow(ctx, `SELECT record_json,output_hash,frozen_report,evidence IS NULL FROM constraint_evaluations WHERE id=$1`, legacy.ID).
		Scan(&gotRecord, &gotHash, &gotReport, &noEvidence); err != nil {
		t.Fatal(err)
	}
	if gotRecord != string(record) || gotHash != hash || gotReport != report || !noEvidence {
		t.Fatal("migration changed historical data or invented legacy evidence")
	}
}
