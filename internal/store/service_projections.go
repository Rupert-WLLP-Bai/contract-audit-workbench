package store

// Core and document append-only projections preserve a single authoritative writer
// per table. Readers merge them with the unchanged historical execution rows.
const serviceProjectionSchemaSQL = `
CREATE TABLE IF NOT EXISTS business_run_events (
 seq bigserial PRIMARY KEY,
 run_id text NOT NULL,
 event_seq integer NOT NULL,
 event_type text NOT NULL,
 state text NOT NULL,
 tenant_id text NOT NULL DEFAULT current_setting('app.tenant_id',true),
 org_id text NOT NULL DEFAULT current_setting('app.org_id',true),
 UNIQUE (run_id,event_seq),
 FOREIGN KEY (tenant_id,org_id,run_id) REFERENCES review_runs(tenant_id,org_id,id)
);
CREATE TABLE IF NOT EXISTS business_audit_events (
 event_id text PRIMARY KEY,
 run_id text NOT NULL,
 actor text NOT NULL,
 seq integer NOT NULL,
 event_type text NOT NULL,
 prev_hash text NOT NULL DEFAULT '',
 hash text NOT NULL,
 correlation_ids text NOT NULL,
 tenant_id text NOT NULL DEFAULT current_setting('app.tenant_id',true),
 org_id text NOT NULL DEFAULT current_setting('app.org_id',true),
 UNIQUE (run_id,seq),
 FOREIGN KEY (tenant_id,org_id,run_id) REFERENCES review_runs(tenant_id,org_id,id)
);
CREATE TABLE IF NOT EXISTS document_activity (
 seq bigserial PRIMARY KEY,
 id text NOT NULL UNIQUE,
 case_id text NOT NULL,
 occurred_at text NOT NULL,
 summary text NOT NULL,
 tenant_id text NOT NULL DEFAULT current_setting('app.tenant_id',true),
 org_id text NOT NULL DEFAULT current_setting('app.org_id',true),
 FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases(tenant_id,org_id,id)
);
CREATE TABLE IF NOT EXISTS execution_activity (
 seq bigserial PRIMARY KEY,
 id text NOT NULL UNIQUE,
 case_id text NOT NULL,
 occurred_at text NOT NULL,
 summary text NOT NULL,
 tenant_id text NOT NULL DEFAULT current_setting('app.tenant_id',true),
 org_id text NOT NULL DEFAULT current_setting('app.org_id',true),
 FOREIGN KEY (tenant_id,org_id,case_id) REFERENCES cases(tenant_id,org_id,id)
);
DROP TRIGGER IF EXISTS business_run_events_append_only ON business_run_events;
CREATE TRIGGER business_run_events_append_only BEFORE UPDATE OR DELETE ON business_run_events FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS business_audit_events_append_only ON business_audit_events;
CREATE TRIGGER business_audit_events_append_only BEFORE UPDATE OR DELETE ON business_audit_events FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS document_activity_append_only ON document_activity;
CREATE TRIGGER document_activity_append_only BEFORE UPDATE OR DELETE ON document_activity FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
DROP TRIGGER IF EXISTS execution_activity_append_only ON execution_activity;
CREATE TRIGGER execution_activity_append_only BEFORE UPDATE OR DELETE ON execution_activity FOR EACH ROW EXECUTE FUNCTION forbid_append_only();
`

// applyReadProjections derives human versions and material freshness without a
// second service updating the authoritative Finding or Case row.
func applyReadProjections(st *state) {
	versions := make(map[string]int, len(st.Decisions))
	updated := make(map[string]string, len(st.Cases))
	for _, decision := range st.Decisions {
		versions[decision.FindingID]++
		if decision.CreatedAt > updated[decision.CaseID] {
			updated[decision.CaseID] = decision.CreatedAt
		}
	}
	for i := range st.Findings {
		version := versions[st.Findings[i].ID] + 1
		if version > st.Findings[i].Version {
			st.Findings[i].Version = version
		}
	}
	for _, doc := range st.Documents {
		if doc.CreatedAt > updated[doc.CaseID] {
			updated[doc.CaseID] = doc.CreatedAt
		}
	}
	// Parsed artifacts and generated drafts carry no case id of their own, so
	// they are attributed through the document they belong to. Without these two
	// loops a reparse or draft generation would leave Case.UpdatedAt stale, which
	// is why the document role used to write the core case row directly.
	docCases := make(map[string]string, len(st.Documents))
	for _, doc := range st.Documents {
		docCases[doc.ID] = doc.CaseID
	}
	for _, parsed := range st.ParsedDocuments {
		if caseID := docCases[parsed.DocumentID]; caseID != "" && parsed.CreatedAt > updated[caseID] {
			updated[caseID] = parsed.CreatedAt
		}
	}
	for _, generation := range st.Generations {
		if generation.CaseID != "" && generation.Record.CreatedAt > updated[generation.CaseID] {
			updated[generation.CaseID] = generation.Record.CreatedAt
		}
	}
	for _, activity := range st.Activity {
		if activity.At > updated[activity.CaseID] {
			updated[activity.CaseID] = activity.At
		}
	}
	for _, run := range st.Runs {
		if run.UpdatedAt > updated[run.CaseID] {
			updated[run.CaseID] = run.UpdatedAt
		}
	}
	for i := range st.Cases {
		if updated[st.Cases[i].ID] > st.Cases[i].UpdatedAt {
			st.Cases[i].UpdatedAt = updated[st.Cases[i].ID]
		}
	}
}
