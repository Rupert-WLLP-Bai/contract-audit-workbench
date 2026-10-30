package store

import (
	"context"
	"errors"
	"fmt"

	"contractaudit/internal/config"
)

// UseServiceOwner restricts mutations made by one independently deployed process.
// Database grants enforce the same ownership even outside the state writer.
func (s *Store) UseServiceOwner(owner string) error {
	switch owner {
	case "core", "document", "execution", "worker", "model", "tool":
	default:
		return errors.New("unknown service data owner")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serviceOwner = owner
	s.requireRemoteDocuments = owner == "worker" || owner == "execution"
	return nil
}

// validateServiceDelta rejects a write whose routed destination table is not
// owned by the current service. Every delta slice is written to exactly one
// table, and that destination is a pure function of the owner (see
// activityTableFor, runEventTableFor and auditTableFor), so this is an exact
// ownership check. An earlier version approximated it by class and therefore
// conflated the shared Activity/Event read models with the tables storing them,
// which rejected a service writing its own activity projection.
func (s *Store) validateServiceDelta(old, changed state) error {
	if s.serviceOwner == "" {
		return nil
	}
	owner := s.serviceOwner
	targets := []struct {
		table   string
		touched bool
	}{
		{"sessions", len(old.Sessions)+len(changed.Sessions) > 0},
		{"cases", len(changed.Cases) > 0},
		{"decisions", len(changed.Decisions) > 0},
		{"idempotency", len(changed.Idempotency) > 0},
		{"business_remediations", len(changed.Remediations) > 0},
		{"business_reports", len(changed.Reports) > 0},
		{"business_archives", len(changed.Archives) > 0},
		{runEventTableFor(owner), len(changed.Events) > 0},
		{auditTableFor(owner), len(changed.Audits) > 0},
		{activityTableFor(owner), len(changed.Activity) > 0},
		{"documents", len(changed.Documents) > 0},
		{"material_entities", len(changed.Materials) > 0},
		{"material_versions", len(changed.MaterialVersions) > 0},
		{"case_contract_chains", len(changed.MaterialChains) > 0},
		{"document_upload_idempotency", len(changed.UploadIdempotency) > 0},
		{"parsed_documents", len(changed.ParsedDocuments) > 0},
		{"business_generations", len(changed.Generations) > 0},
		{"review_runs", len(changed.Runs) > 0},
		{"review_attempts", len(changed.Attempts) > 0},
		{"review_attempt_inputs", len(changed.AttemptInputs) > 0},
		{"review_attempt_parse_selections", len(changed.AttemptParseSelections) > 0},
		{"execution_events", len(changed.ExecutionEvents) > 0},
		{"findings", len(changed.Findings) > 0},
		{"constraint_evaluations", len(changed.Evaluations) > 0},
		{"review_run_snapshots", len(changed.Snapshots) > 0},
		{"worker_jobs", len(changed.Jobs) > 0},
		{"outbox_events", len(changed.Outbox) > 0},
	}
	for _, target := range targets {
		if target.touched && !ownedTable(owner, target.table) {
			return fmt.Errorf("service cannot modify another service's authoritative data: %s", target.table)
		}
	}
	return nil
}

// Ready verifies the shared database connection without mutating business data.
func (s *Store) Ready(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return errors.New("service database is unavailable")
	}
	return s.pool.Ping(ctx)
}

// OpenApplicationSelection opens an explicitly validated service connection.
// It never migrates, creates a database, or interrupts another worker's attempts.
func OpenApplicationSelection(dir string, selection config.Selection) (*Store, error) {
	verified, err := config.Select(config.Environ{
		AppEnv: selection.Mode, DevDatabaseURL: selection.DevDSN,
		TestDatabaseURL: selection.TestDSN, ProdDatabaseURL: selection.ProdDSN,
	})
	if err != nil {
		return nil, err
	}
	if verified.DSN != selection.DSN {
		return nil, config.ErrDatabaseURL
	}
	schema, err := applicationSchema()
	if err != nil {
		return nil, err
	}
	return openApplicationStore(dir, selection, schema)
}
