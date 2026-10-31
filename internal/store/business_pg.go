package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// businessSchemaSQL is appended to the store schema migration after the core
// tenant-scoped tables and their candidate keys exist.
const businessSchemaSQL = `
CREATE UNIQUE INDEX IF NOT EXISTS documents_tenant_org_case_identity ON documents (tenant_id, org_id, case_id, id);
CREATE UNIQUE INDEX IF NOT EXISTS review_runs_tenant_org_case_identity ON review_runs (tenant_id, org_id, case_id, id);
CREATE UNIQUE INDEX IF NOT EXISTS review_attempts_tenant_org_run_identity ON review_attempts (tenant_id, org_id, run_id, id);

CREATE TABLE IF NOT EXISTS business_generations (
	seq bigserial PRIMARY KEY,
	id text NOT NULL UNIQUE,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	document_id text NOT NULL,
	template_id text NOT NULL,
	template_version text NOT NULL,
	digest text NOT NULL,
	created_at text NOT NULL,
	UNIQUE (tenant_id, org_id, case_id, id),
	FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id),
	FOREIGN KEY (tenant_id, org_id, case_id, document_id) REFERENCES documents (tenant_id, org_id, case_id, id)
);
CREATE INDEX IF NOT EXISTS business_generations_case_order ON business_generations (tenant_id, org_id, case_id, seq);

CREATE TABLE IF NOT EXISTS business_remediations (
	seq bigserial PRIMARY KEY,
	id text NOT NULL UNIQUE,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	run_id text NOT NULL,
	attempt_id text NOT NULL,
	finding_ids text[] NOT NULL DEFAULT '{}',
	assignee_id text NOT NULL DEFAULT '',
	due_at text NOT NULL DEFAULT '',
	measure text NOT NULL,
	status text NOT NULL,
	version integer NOT NULL CHECK (version > 0),
	document_ids text[] NOT NULL DEFAULT '{}',
	reason text NOT NULL,
	verification_run_id text,
	verification_attempt_id text,
	created_at text NOT NULL,
	updated_at text NOT NULL,
	UNIQUE (tenant_id, org_id, case_id, id),
	FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id),
	FOREIGN KEY (tenant_id, org_id, case_id, run_id) REFERENCES review_runs (tenant_id, org_id, case_id, id),
	FOREIGN KEY (tenant_id, org_id, run_id, attempt_id) REFERENCES review_attempts (tenant_id, org_id, run_id, id),
    FOREIGN KEY (tenant_id, org_id, case_id, verification_run_id) REFERENCES review_runs (tenant_id, org_id, case_id, id),
    CONSTRAINT business_remediations_verification_scope_fkey FOREIGN KEY (tenant_id, org_id, verification_run_id, verification_attempt_id) REFERENCES review_attempts (tenant_id, org_id, run_id, id),
    CHECK ((verification_run_id IS NULL) = (verification_attempt_id IS NULL))
);
CREATE INDEX IF NOT EXISTS business_remediations_case_order ON business_remediations (tenant_id, org_id, case_id, seq);
CREATE INDEX IF NOT EXISTS business_remediations_attempt ON business_remediations (tenant_id, org_id, case_id, run_id, attempt_id);
DO $$
DECLARE
	legacy text;
BEGIN
	SELECT conname INTO legacy FROM pg_constraint
	WHERE conrelid = 'business_remediations'::regclass AND contype = 'f'
		AND pg_get_constraintdef(oid) LIKE '%(tenant_id, org_id, run_id, verification_attempt_id)%';
	IF legacy IS NOT NULL THEN
		EXECUTE format('ALTER TABLE business_remediations DROP CONSTRAINT %I', legacy);
	END IF;
	IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='business_remediations_verification_scope_fkey' AND connamespace=current_schema()::regnamespace) THEN
		ALTER TABLE business_remediations ADD CONSTRAINT business_remediations_verification_scope_fkey
			FOREIGN KEY (tenant_id, org_id, verification_run_id, verification_attempt_id)
			REFERENCES review_attempts (tenant_id, org_id, run_id, id);
	END IF;
END $$;

CREATE TABLE IF NOT EXISTS business_remediation_revisions (
	seq bigserial PRIMARY KEY,
	id text NOT NULL,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	remediation_id text NOT NULL,
	version integer NOT NULL CHECK (version > 0),
	recorded_at text NOT NULL,
	record_json jsonb NOT NULL,
	UNIQUE (tenant_id, org_id, case_id, remediation_id, version),
	FOREIGN KEY (tenant_id, org_id, case_id, remediation_id) REFERENCES business_remediations (tenant_id, org_id, case_id, id),
	CHECK (id = remediation_id AND record_json->>'id' = remediation_id AND (record_json->>'version')::integer = version)
);
CREATE INDEX IF NOT EXISTS business_remediation_revisions_case_order ON business_remediation_revisions (tenant_id, org_id, case_id, seq);

CREATE TABLE IF NOT EXISTS business_reports (
	seq bigserial PRIMARY KEY,
	id text NOT NULL UNIQUE,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	run_id text NOT NULL,
	attempt_id text NOT NULL,
	version integer NOT NULL CHECK (version > 0),
	input_hash text NOT NULL,
	output_hash text NOT NULL,
	decision_snapshot_hash text NOT NULL,
	digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
	created_at text NOT NULL,
	json_bytes bytea NOT NULL,
	html_bytes bytea NOT NULL,
	UNIQUE (tenant_id, org_id, case_id, id),
	UNIQUE (tenant_id, org_id, case_id, version),
	FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id),
	FOREIGN KEY (tenant_id, org_id, case_id, run_id) REFERENCES review_runs (tenant_id, org_id, case_id, id),
	FOREIGN KEY (tenant_id, org_id, run_id, attempt_id) REFERENCES review_attempts (tenant_id, org_id, run_id, id)
);
CREATE INDEX IF NOT EXISTS business_reports_case_order ON business_reports (tenant_id, org_id, case_id, seq);
CREATE INDEX IF NOT EXISTS business_reports_attempt ON business_reports (tenant_id, org_id, case_id, run_id, attempt_id);

CREATE TABLE IF NOT EXISTS business_archives (
	seq bigserial PRIMARY KEY,
	id text NOT NULL UNIQUE,
	tenant_id text NOT NULL,
	org_id text NOT NULL,
	case_id text NOT NULL,
	report_id text NOT NULL,
	actor_id text NOT NULL,
	reason text NOT NULL,
	archived_at text NOT NULL,
	UNIQUE (tenant_id, org_id, case_id, id),
	FOREIGN KEY (tenant_id, org_id, case_id) REFERENCES cases (tenant_id, org_id, id),
	FOREIGN KEY (tenant_id, org_id, case_id, report_id) REFERENCES business_reports (tenant_id, org_id, case_id, id)
);
CREATE INDEX IF NOT EXISTS business_archives_case_order ON business_archives (tenant_id, org_id, case_id, seq);

CREATE OR REPLACE FUNCTION guard_business_remediation_update() RETURNS trigger
	LANGUAGE plpgsql AS $$
	BEGIN
		IF TG_OP = 'DELETE' THEN
			RAISE EXCEPTION 'append-only';
		END IF;
		IF NEW.id IS DISTINCT FROM OLD.id OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR
			NEW.org_id IS DISTINCT FROM OLD.org_id OR NEW.case_id IS DISTINCT FROM OLD.case_id OR
			NEW.run_id IS DISTINCT FROM OLD.run_id OR NEW.attempt_id IS DISTINCT FROM OLD.attempt_id OR
			NEW.created_at IS DISTINCT FROM OLD.created_at OR NEW.version <> OLD.version + 1 THEN
			RAISE EXCEPTION 'remediation identity or version is immutable';
		END IF;
		RETURN NEW;
	END;
	$$;
DROP TRIGGER IF EXISTS business_generations_append_only ON business_generations;
CREATE TRIGGER business_generations_append_only BEFORE UPDATE OR DELETE ON business_generations FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS business_remediations_update_guard ON business_remediations;
CREATE TRIGGER business_remediations_update_guard BEFORE UPDATE OR DELETE ON business_remediations FOR EACH ROW EXECUTE FUNCTION guard_business_remediation_update();
DROP TRIGGER IF EXISTS business_remediation_revisions_append_only ON business_remediation_revisions;
CREATE TRIGGER business_remediation_revisions_append_only BEFORE UPDATE OR DELETE ON business_remediation_revisions FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS business_reports_append_only ON business_reports;
CREATE TRIGGER business_reports_append_only BEFORE UPDATE OR DELETE ON business_reports FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS business_archives_append_only ON business_archives;
CREATE TRIGGER business_archives_append_only BEFORE UPDATE OR DELETE ON business_archives FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
`

func loadBusinessState(ctx context.Context, q queryer, st *state) error {
	var err error
	if st.Generations, err = loadBusinessGenerations(ctx, q); err != nil {
		return err
	}
	if st.Remediations, err = loadBusinessRemediations(ctx, q); err != nil {
		return err
	}
	if st.Reports, err = loadBusinessReports(ctx, q); err != nil {
		return err
	}
	if st.Archives, err = loadBusinessArchives(ctx, q); err != nil {
		return err
	}
	if err := loadWorkbenchState(ctx, q, st); err != nil {
		return err
	}
	return nil
}

func loadBusinessGenerations(ctx context.Context, q queryer, caseID ...string) ([]generationRecord, error) {
	rows, err := queryCaseRows(ctx, q, `SELECT g.id, g.tenant_id, g.org_id, g.case_id, g.document_id, g.template_id, g.template_version,
        g.digest, d.digest, g.created_at FROM business_generations g JOIN documents d
        ON d.tenant_id=g.tenant_id AND d.org_id=g.org_id AND d.case_id=g.case_id AND d.id=g.document_id`, `g.case_id=$1`, ` ORDER BY g.seq`, caseID...)
	if err != nil {
		return nil, fmt.Errorf("load business generations: %w", err)
	}
	defer rows.Close()
	var out []generationRecord
	for rows.Next() {
		var item generationRecord
		var documentDigest string
		if err := rows.Scan(&item.Record.ID, &item.TenantID, &item.OrgID, &item.CaseID,
			&item.Record.DocumentID, &item.Record.TemplateID, &item.Record.TemplateVersion, &item.Record.Digest, &documentDigest, &item.Record.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan business generation: %w", err)
		}
		if item.Record.Digest != documentDigest {
			return nil, fmt.Errorf("business generation %q document digest mismatch", item.Record.ID)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate business generations: %w", err)
	}
	return out, nil
}

func loadBusinessRemediations(ctx context.Context, q queryer, caseID ...string) ([]remediationRecord, error) {
	rows, err := queryCaseRows(ctx, q, `SELECT id, tenant_id, org_id, case_id, run_id, attempt_id, finding_ids, assignee_id, due_at,
		measure, status, version, document_ids, reason, verification_run_id, verification_attempt_id, created_at, updated_at
		FROM business_remediations`, `case_id=$1`, ` ORDER BY seq`, caseID...)
	if err != nil {
		return nil, fmt.Errorf("load business remediations: %w", err)
	}
	defer rows.Close()
	var out []remediationRecord
	byID := make(map[string]int)
	for rows.Next() {
		var item remediationRecord
		var verificationRunID, verificationAttemptID pgtype.Text
		if err := rows.Scan(&item.Record.ID, &item.TenantID, &item.OrgID, &item.Record.CaseID,
			&item.Record.RunID, &item.Record.AttemptID, &item.Record.FindingIDs, &item.Record.AssigneeID, &item.Record.DueAt,
			&item.Record.Measure, &item.Record.Status, &item.Record.Version, &item.Record.DocumentIDs, &item.Record.Reason,
			&verificationRunID, &verificationAttemptID, &item.Record.CreatedAt, &item.Record.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan business remediation: %w", err)
		}
		if verificationRunID.Valid {
			value := verificationRunID.String
			item.Record.VerificationRunID = &value
		}
		if verificationAttemptID.Valid {
			value := verificationAttemptID.String
			item.Record.VerificationAttemptID = &value
		}
		item.Record.FindingIDs = nonNilStrings(item.Record.FindingIDs)
		item.Record.DocumentIDs = nonNilStrings(item.Record.DocumentIDs)
		byID[item.Record.ID] = len(out)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate business remediations: %w", err)
	}
	rows.Close()

	revisions, err := queryCaseRows(ctx, q, `SELECT remediation_id, tenant_id, org_id, case_id, version, recorded_at, record_json::text
		FROM business_remediation_revisions`, `case_id=$1`, ` ORDER BY seq`, caseID...)
	if err != nil {
		return nil, fmt.Errorf("load business remediation revisions: %w", err)
	}
	defer revisions.Close()
	for revisions.Next() {
		var id, tenantID, orgID, caseID, recordedAt, raw string
		var version int
		if err := revisions.Scan(&id, &tenantID, &orgID, &caseID, &version, &recordedAt, &raw); err != nil {
			return nil, fmt.Errorf("scan business remediation revision: %w", err)
		}
		index, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("business remediation revision %q has no current record", id)
		}
		current := &out[index]
		if tenantID != current.TenantID || orgID != current.OrgID || caseID != current.Record.CaseID || version != len(current.History)+1 {
			return nil, fmt.Errorf("business remediation revision %q has inconsistent scope or version", id)
		}
		var record Remediation
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, fmt.Errorf("decode business remediation revision %q: %w", id, err)
		}
		// The current-record columns are NOT NULL DEFAULT '{}', so an absent
		// reference set is stored there as an empty array. Normalize the revision
		// payload the same way or the projection/history equality check below
		// would reject a valid reopen.
		record.FindingIDs = nonNilStrings(record.FindingIDs)
		record.DocumentIDs = nonNilStrings(record.DocumentIDs)
		if record.ID != id || record.CaseID != caseID || record.Version != version {
			return nil, fmt.Errorf("business remediation revision %q payload identity mismatch", id)
		}
		current.History = append(current.History, remediationRevision{Record: record, RecordedAt: recordedAt})
	}
	if err := revisions.Err(); err != nil {
		return nil, fmt.Errorf("iterate business remediation revisions: %w", err)
	}
	for _, item := range out {
		if len(item.History) == 0 || len(item.History) != item.Record.Version ||
			!reflect.DeepEqual(item.History[len(item.History)-1].Record, item.Record) {
			return nil, fmt.Errorf("business remediation %q current projection does not match revision history", item.Record.ID)
		}
	}
	if err := validateRemediationHistory(nil, out); err != nil {
		return nil, fmt.Errorf("validate loaded business remediation history: %w", err)
	}
	return out, nil
}

func loadBusinessReports(ctx context.Context, q queryer) ([]frozenReport, error) {
	rows, err := q.Query(ctx, `SELECT id, tenant_id, org_id, case_id, run_id, attempt_id, version, input_hash, output_hash,
		decision_snapshot_hash, digest, created_at, json_bytes, html_bytes FROM business_reports ORDER BY seq`)
	if err != nil {
		return nil, fmt.Errorf("load business reports: %w", err)
	}
	defer rows.Close()
	var out []frozenReport
	for rows.Next() {
		var item frozenReport
		if err := rows.Scan(&item.Record.ID, &item.TenantID, &item.OrgID, &item.CaseID,
			&item.Record.RunID, &item.Record.AttemptID, &item.Record.Version, &item.Record.InputHash, &item.Record.OutputHash,
			&item.Record.DecisionSnapshotHash, &item.Record.Digest, &item.Record.CreatedAt, &item.JSONBytes, &item.HTMLBytes); err != nil {
			return nil, fmt.Errorf("scan business report: %w", err)
		}
		if err := validateFrozenReport(item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate business reports: %w", err)
	}
	return out, nil
}

func validateFrozenReport(item frozenReport) error {
	if reportDigest(item.JSONBytes, item.HTMLBytes) != item.Record.Digest {
		return fmt.Errorf("business report %q payload digest mismatch", item.Record.ID)
	}
	var payload reportEnvelope
	if err := json.Unmarshal(item.JSONBytes, &payload); err != nil {
		return fmt.Errorf("decode business report %q payload: %w", item.Record.ID, err)
	}
	if !frozenReportIdentityMatches(item, payload) {
		return errors.New("business report row identity does not match its frozen JSON payload")
	}
	return validateReportAgentReview(payload)
}

func frozenReportIdentityMatches(item frozenReport, payload reportEnvelope) bool {
	return frozenReportHeaderMatches(item, payload) && frozenReportRunMatches(item.Record, payload) &&
		payload.InputHash == item.Record.InputHash && payload.OutputHash == item.Record.OutputHash &&
		payload.DecisionSnapshotHash == item.Record.DecisionSnapshotHash
}

func frozenReportHeaderMatches(item frozenReport, payload reportEnvelope) bool {
	return payload.SchemaVersion == businessReportSchema && payload.ReportID == item.Record.ID &&
		payload.ReportVersion == item.Record.Version && payload.CreatedAt == item.Record.CreatedAt && payload.Case.ID == item.CaseID
}

func frozenReportRunMatches(record ReportRecord, payload reportEnvelope) bool {
	return payload.Run.ID == record.RunID && payload.Attempt.ID == record.AttemptID && payload.Attempt.RunID == record.RunID
}

func validateReportAgentReview(payload reportEnvelope) error {
	if len(payload.AgentReview) == 0 || bytes.Equal(bytes.TrimSpace(payload.AgentReview), []byte("null")) {
		if len(payload.AgentReviewBytes) != 0 || payload.AgentReviewSHA256 != "" {
			return errors.New("business report has a digest or byte payload without an agent review")
		}
		return nil
	}
	if !validReportAgentReviewBytes(payload) {
		return errors.New("business report agent review bytes do not match their digest")
	}
	return reportAgentReviewMatchesBytes(payload.AgentReview, payload.AgentReviewBytes)
}

func validReportAgentReviewBytes(payload reportEnvelope) bool {
	return json.Valid(payload.AgentReview) && len(payload.AgentReviewBytes) != 0 &&
		json.Valid(payload.AgentReviewBytes) && sha256Bytes(payload.AgentReviewBytes) == payload.AgentReviewSHA256
}

func reportAgentReviewMatchesBytes(embeddedJSON, exactJSON []byte) error {
	var embedded, exact bytes.Buffer
	if err := json.Compact(&embedded, embeddedJSON); err != nil {
		return fmt.Errorf("compact business report agent review: %w", err)
	}
	if err := json.Compact(&exact, exactJSON); err != nil {
		return fmt.Errorf("compact business report agent review bytes: %w", err)
	}
	if !bytes.Equal(embedded.Bytes(), exact.Bytes()) {
		return errors.New("business report agent review projection differs from its exact bytes")
	}
	return nil
}

func loadBusinessArchives(ctx context.Context, q queryer) ([]archiveRecord, error) {
	rows, err := q.Query(ctx, `SELECT id, tenant_id, org_id, case_id, report_id, actor_id, reason, archived_at
		FROM business_archives ORDER BY seq`)
	if err != nil {
		return nil, fmt.Errorf("load business archives: %w", err)
	}
	defer rows.Close()
	var out []archiveRecord
	for rows.Next() {
		var item archiveRecord
		if err := rows.Scan(&item.Record.ID, &item.TenantID, &item.OrgID, &item.Record.CaseID,
			&item.Record.ReportID, &item.ActorID, &item.Record.Reason, &item.Record.ArchivedAt); err != nil {
			return nil, fmt.Errorf("scan business archive: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate business archives: %w", err)
	}
	return out, nil
}

func syncBusinessRows(ctx context.Context, tx pgx.Tx, previous, removed, changed state) error {
	if err := syncBusinessGenerations(ctx, tx, changed.Generations); err != nil {
		return err
	}
	if err := syncBusinessRemediations(ctx, tx, previous.Remediations, changed.Remediations); err != nil {
		return err
	}
	if err := syncBusinessReports(ctx, tx, changed.Reports); err != nil {
		return err
	}
	if err := syncBusinessArchives(ctx, tx, changed.Archives); err != nil {
		return err
	}
	return syncWorkbenchRows(ctx, tx, previous, removed, changed)
}

func syncBusinessGenerations(ctx context.Context, tx pgx.Tx, items []generationRecord) error {
	for _, item := range items {
		record := item.Record
		if _, err := tx.Exec(ctx, `INSERT INTO business_generations
			(id, tenant_id, org_id, case_id, document_id, template_id, template_version, digest, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			record.ID, item.TenantID, item.OrgID, item.CaseID, record.DocumentID, record.TemplateID,
			record.TemplateVersion, record.Digest, record.CreatedAt); err != nil {
			return fmt.Errorf("insert business generation %q: %w", record.ID, err)
		}
	}
	return nil
}

func syncBusinessReports(ctx context.Context, tx pgx.Tx, items []frozenReport) error {
	for _, item := range items {
		record := item.Record
		if err := validateFrozenReport(item); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO business_reports
			(id, tenant_id, org_id, case_id, run_id, attempt_id, version, input_hash, output_hash, decision_snapshot_hash, digest, created_at, json_bytes, html_bytes)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			record.ID, item.TenantID, item.OrgID, item.CaseID, record.RunID, record.AttemptID, record.Version,
			record.InputHash, record.OutputHash, record.DecisionSnapshotHash, record.Digest, record.CreatedAt,
			item.JSONBytes, item.HTMLBytes); err != nil {
			return fmt.Errorf("insert business report %q: %w", record.ID, err)
		}
	}
	return nil
}

func syncBusinessArchives(ctx context.Context, tx pgx.Tx, items []archiveRecord) error {
	for _, item := range items {
		record := item.Record
		if _, err := tx.Exec(ctx, `INSERT INTO business_archives
			(id, tenant_id, org_id, case_id, report_id, actor_id, reason, archived_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			record.ID, item.TenantID, item.OrgID, record.CaseID, record.ReportID, item.ActorID, record.Reason, record.ArchivedAt); err != nil {
			return fmt.Errorf("insert business archive %q: %w", record.ID, err)
		}
	}
	return nil
}

func syncBusinessRemediations(ctx context.Context, tx pgx.Tx, previous, changed []remediationRecord) error {
	previousByID := make(map[string]remediationRecord, len(previous))
	for _, item := range previous {
		previousByID[item.Record.ID] = item
	}
	for _, item := range changed {
		record := item.Record
		if err := validateRemediationSyncHistory(item); err != nil {
			return err
		}
		prior, exists := previousByID[record.ID]
		if exists {
			if err := updateBusinessRemediation(ctx, tx, prior, item); err != nil {
				return err
			}
		} else if err := insertBusinessRemediation(ctx, tx, item); err != nil {
			return err
		}
	}
	return nil
}

func validateRemediationSyncHistory(item remediationRecord) error {
	if item.Record.ID == "" || len(item.History) == 0 || len(item.History) != item.Record.Version ||
		!reflect.DeepEqual(item.History[len(item.History)-1].Record, item.Record) {
		return fmt.Errorf("business remediation %q has invalid revision history", item.Record.ID)
	}
	return nil
}

func updateBusinessRemediation(ctx context.Context, tx pgx.Tx, prior, item remediationRecord) error {
	record := item.Record
	if !businessRemediationIdentityAndHistoryMatch(prior, item) {
		return fmt.Errorf("business remediation %q changed immutable identity or history", record.ID)
	}
	result, err := tx.Exec(ctx, `UPDATE business_remediations SET finding_ids=$1, assignee_id=$2, due_at=$3,
		measure=$4, status=$5, version=$6, document_ids=$7, reason=$8, verification_run_id=$9,
		verification_attempt_id=$10, updated_at=$11
		WHERE id=$12 AND tenant_id=$13 AND org_id=$14 AND case_id=$15 AND version=$16`,
		nonNilStrings(record.FindingIDs), record.AssigneeID, record.DueAt, record.Measure, record.Status, record.Version,
		nonNilStrings(record.DocumentIDs), record.Reason, optionalString(record.VerificationRunID),
		optionalString(record.VerificationAttemptID), record.UpdatedAt, record.ID, item.TenantID, item.OrgID, record.CaseID, prior.Record.Version)
	if err != nil {
		return fmt.Errorf("update business remediation %q: %w", record.ID, err)
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("business remediation %q optimistic version conflict", record.ID)
	}
	for _, revision := range item.History[len(prior.History):] {
		if err := insertBusinessRemediationRevision(ctx, tx, item, revision); err != nil {
			return err
		}
	}
	return nil
}

func businessRemediationIdentityAndHistoryMatch(prior, item remediationRecord) bool {
	record := item.Record
	return item.TenantID == prior.TenantID && item.OrgID == prior.OrgID && record.CaseID == prior.Record.CaseID &&
		record.RunID == prior.Record.RunID && record.AttemptID == prior.Record.AttemptID &&
		appendOnlyEqual(prior.History, item.History) && record.Version == prior.Record.Version+1 &&
		len(item.History) == len(prior.History)+1
}

func insertBusinessRemediation(ctx context.Context, tx pgx.Tx, item remediationRecord) error {
	record := item.Record
	if record.Version != 1 || len(item.History) != 1 {
		return fmt.Errorf("new business remediation %q must begin at revision one", record.ID)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO business_remediations
		(id, tenant_id, org_id, case_id, run_id, attempt_id, finding_ids, assignee_id, due_at, measure, status, version,
		document_ids, reason, verification_run_id, verification_attempt_id, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		record.ID, item.TenantID, item.OrgID, record.CaseID, record.RunID, record.AttemptID,
		nonNilStrings(record.FindingIDs), record.AssigneeID, record.DueAt, record.Measure, record.Status, record.Version,
		nonNilStrings(record.DocumentIDs), record.Reason, optionalString(record.VerificationRunID),
		optionalString(record.VerificationAttemptID), record.CreatedAt, record.UpdatedAt); err != nil {
		return fmt.Errorf("insert business remediation %q: %w", record.ID, err)
	}
	return insertBusinessRemediationRevision(ctx, tx, item, item.History[0])
}

func insertBusinessRemediationRevision(ctx context.Context, tx pgx.Tx, item remediationRecord, revision remediationRevision) error {
	if revision.Record.ID != item.Record.ID || revision.Record.CaseID != item.Record.CaseID || revision.Record.Version != len(item.History) {
		return fmt.Errorf("business remediation %q has a revision with mismatched identity", item.Record.ID)
	}
	raw, err := json.Marshal(revision.Record)
	if err != nil {
		return fmt.Errorf("encode business remediation revision %q: %w", item.Record.ID, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO business_remediation_revisions
        (id, tenant_id, org_id, case_id, remediation_id, version, recorded_at, record_json)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, item.Record.ID, item.TenantID, item.OrgID, item.Record.CaseID,
		item.Record.ID, revision.Record.Version, revision.RecordedAt, json.RawMessage(raw)); err != nil {
		return fmt.Errorf("insert business remediation revision %q/%d: %w", item.Record.ID, revision.Record.Version, err)
	}
	return nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func optionalString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
