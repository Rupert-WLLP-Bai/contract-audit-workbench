package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"contractaudit/internal/config"
	"github.com/jackc/pgx/v5"
)

const serviceRoleSchemaSQL = `CREATE TABLE IF NOT EXISTS service_database_roles (
 role_name text PRIMARY KEY,
 service text NOT NULL CHECK (service IN ('core','document','execution','model','tool')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
)`

var serviceOwnedTables = map[string][]string{
	"core":      {"sessions", "cases", "case_intake_operations", "case_intake_requests", "case_intake_archives", "decisions", "idempotency", "activity", "business_remediations", "business_remediation_revisions", "business_reports", "business_archives", "business_run_events", "business_audit_events", "business_case_profiles", "business_workbench_tasks", "business_case_relations", "business_knowledge", "business_workbench_audit", "rule_pack_versions", "rule_pack_pointers", "rule_pack_history", "rule_pack_idempotency", "experience_feedback", "experience_candidate_versions", "governed_knowledge_versions", "governed_knowledge_state", "governed_knowledge_pointers", "governed_knowledge_history", "governed_knowledge_idempotency", "governed_policy_rule_bindings", "legal_source_captures", "evidence_requests", "evidence_receipts", "evidence_request_events", "operator_object_bindings", "operator_external_snapshot_sets", "operator_external_collection_claims", "operator_external_observations", "operator_fulfillment_evaluations", "operator_fulfillment_checks", "operator_fulfillment_evaluation_objects", "operator_impact_plans", "operator_mapping_lineages", "operator_mapping_change_requests", "operator_mapping_change_decisions", "service_objects", "service_object_versions", "service_object_contract_associations", "service_object_mutation_receipts", "subjects", "subject_versions", "subject_current_identifier_claims", "case_subject_roles", "case_subject_role_versions", "subject_change_requests", "subject_change_decisions", "subject_write_idempotency", "subject_source_snapshots", "case_legal_applicability_facts", "case_legal_applicability_fact_versions", "core_write_receipts", "manual_finding_records", "rule_pack_shadow_reports", "experience_finding_usage"},
	"document":  {"documents", "parsed_documents", "business_generations", "document_activity", "case_contract_chains", "material_entities", "material_versions", "document_upload_idempotency"},
	"execution": {"review_runs", "review_attempts", "review_attempt_inputs", "review_attempt_parse_selections", "execution_events", "findings", "constraint_evaluations", "run_events", "review_run_snapshots", "audit_events", "worker_jobs", "outbox_events", "execution_activity", "execution_queue_policies", "execution_queue_cursor", "execution_queue_tenants", "execution_agent_reviews", "advisory_judgments", "experience_distill_inputs", "experience_distill_results", "experience_distill_failures", "execution_write_receipts", "review_rulepack_inputs"},
	"model":     {"model_call_leases", "model_usage"},
	"tool":      {"tool_call_ledger"},
}

// activityTableFor returns the append-only activity projection an owner writes
// its timeline rows to. Each service owns its own table, so no service ever
// writes another service's authoritative activity rows; the read path unions
// them back into one timeline.
func activityTableFor(owner string) string {
	switch owner {
	case "document":
		return "document_activity"
	case "execution", "worker":
		return "execution_activity"
	default:
		return "activity"
	}
}

// runEventTableFor and auditTableFor are the same routing rule for the run event
// and audit streams: the core service keeps its own business projections, every
// other owner writes the execution-owned tables.
func runEventTableFor(owner string) string {
	if owner == "core" {
		return "business_run_events"
	}
	return "run_events"
}

func auditTableFor(owner string) string {
	if owner == "core" {
		return "business_audit_events"
	}
	return "audit_events"
}

// ownedTable reports whether an owner is granted write access to a table. The
// empty owner is the single-process deployment, which owns every table.
func ownedTable(owner, table string) bool {
	if owner == "" {
		return true
	}
	if owner == "worker" {
		owner = "execution"
	}
	for _, owned := range serviceOwnedTables[owner] {
		if owned == table {
			return true
		}
	}
	return false
}

func (s *Store) ValidateServiceDatabaseRole(ctx context.Context, service string) error {
	if service == "worker" {
		service = "execution"
	}
	var configured string
	if err := s.pool.QueryRow(ctx, `SELECT service FROM service_database_roles WHERE role_name=current_user`).Scan(&configured); err != nil || configured != service {
		return errors.New("database role is not bound to this service; run the independent migration job")
	}
	tables := serviceOwnedTables[service]
	if len(tables) == 0 {
		return errors.New("database role has no declared table ownership")
	}
	// Every table a service may write must carry forced tenant row level
	// security. Checking it here makes a missing policy (for example a table
	// added to the ownership map but never added to the migration's RLS list)
	// fail at startup instead of silently allowing a cross-tenant write.
	var uncovered int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM unnest($1::text[]) AS owned(name)
		LEFT JOIN pg_class c ON c.relname=owned.name AND c.relnamespace=current_schema()::regnamespace
		WHERE c.oid IS NULL OR NOT c.relrowsecurity OR NOT c.relforcerowsecurity
			OR NOT EXISTS (SELECT 1 FROM pg_policy p WHERE p.polrelid=c.oid AND p.polname='tenant_boundary')`, tables).Scan(&uncovered); err != nil {
		return errors.New("cannot verify service table row level security")
	}
	if uncovered != 0 {
		return errors.New("service owned tables are missing tenant row level security")
	}
	return nil
}

// MigrateServiceRoles provisions only explicitly configured, dedicated service
// roles. The schema migration must already have completed under the migrator.
func MigrateServiceRoles(ctx context.Context) error {
	selection, err := config.FromProcess()
	if err != nil {
		return err
	}
	dsn := strings.TrimSpace(os.Getenv("MIGRATOR_DATABASE_URL"))
	if dsn == "" {
		return errors.New("MIGRATOR_DATABASE_URL is required for service role setup")
	}
	identity, err := config.ParseDSN(dsn)
	if err != nil || identity != selection.Database {
		return errors.New("migrator must select the active environment database")
	}
	schema, err := applicationSchema()
	if err != nil {
		return err
	}
	conn, err := connectPoolDSN(ctx, dsn)
	if err != nil {
		return errors.New("cannot connect to the service role migrator")
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return errors.New("cannot start service role migration")
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('search_path',$1,true),set_config('standard_conforming_strings','on',true)`, schema); err != nil {
		return errors.New("cannot select service schema")
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(),0))`); err != nil {
		return err
	}
	legacyLedger := pgx.Identifier{schema, "model_budget_ledger"}.Sanitize()
	var legacyLedgerExists bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, legacyLedger).Scan(&legacyLedgerExists); err != nil {
		return errors.New("cannot inspect legacy model accounting relation")
	}

	for _, service := range []string{"core", "document", "execution", "model", "tool"} {
		key := strings.ToUpper(service) + "_DATABASE_URL"
		raw := strings.TrimSpace(os.Getenv(key))
		roleIdentity, parseErr := config.ParseDSN(raw)
		if parseErr != nil || roleIdentity != selection.Database {
			return fmt.Errorf("%s must select the active database", key)
		}
		cfg, parseErr := pgx.ParseConfig(raw)
		if parseErr != nil || !identRE.MatchString(cfg.User) || len(cfg.Password) < 32 {
			return fmt.Errorf("%s requires a dedicated role and private password", key)
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, cfg.User).Scan(&exists); err != nil {
			return err
		}
		quotedRole := pgx.Identifier{cfg.User}.Sanitize()
		if exists {
			var bound string
			if err := tx.QueryRow(ctx, `SELECT service FROM service_database_roles WHERE role_name=$1`, cfg.User).Scan(&bound); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("refusing to change existing database role %q without a service binding", cfg.User)
				}
				return fmt.Errorf("cannot verify service binding for existing database role %q: %w", cfg.User, err)
			}
			if bound != service {
				return fmt.Errorf("refusing to change database role %q bound to service %q for requested service %q", cfg.User, bound, service)
			}
			var privileged bool
			if err := tx.QueryRow(ctx, `SELECT r.rolsuper OR r.rolbypassrls OR r.rolcreaterole OR r.rolcreatedb OR EXISTS (SELECT 1 FROM pg_namespace n WHERE n.nspname=$2 AND pg_has_role(r.oid,n.nspowner,'MEMBER')) OR EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$2 AND pg_has_role(r.oid,c.relowner,'MEMBER')) FROM pg_roles r WHERE r.rolname=$1`, cfg.User, schema).Scan(&privileged); err != nil || privileged {
				return errors.New("refusing a privileged service database role")
			}
		} else {
			if _, err := tx.Exec(ctx, `CREATE ROLE `+quotedRole+` LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT`); err != nil {
				return errors.New("cannot create dedicated service role")
			}
		}
		password := "'" + strings.ReplaceAll(cfg.Password, "'", "''") + "'"
		if _, err := tx.Exec(ctx, `ALTER ROLE `+quotedRole+` PASSWORD `+password); err != nil {
			return errors.New("cannot configure service authentication")
		}
		qs := pgx.Identifier{schema}.Sanitize()
		if _, err := tx.Exec(ctx, `REVOKE ALL ON SCHEMA `+qs+` FROM `+quotedRole+`; GRANT USAGE ON SCHEMA `+qs+` TO `+quotedRole+`; REVOKE ALL ON ALL TABLES IN SCHEMA `+qs+` FROM `+quotedRole+`; REVOKE ALL ON ALL SEQUENCES IN SCHEMA `+qs+` FROM `+quotedRole+`; GRANT SELECT ON ALL TABLES IN SCHEMA `+qs+` TO `+quotedRole); err != nil {
			return errors.New("cannot configure read projections")
		}
		if legacyLedgerExists {
			if _, err := tx.Exec(ctx, `REVOKE ALL PRIVILEGES ON TABLE `+legacyLedger+` FROM `+quotedRole); err != nil {
				return errors.New("cannot restrict legacy model accounting relation access")
			}
		}
		if service == "model" {
			accountingTables := pgx.Identifier{schema, "model_call_leases"}.Sanitize() + `,` + pgx.Identifier{schema, "model_usage"}.Sanitize()
			if _, err := tx.Exec(ctx, `REVOKE ALL PRIVILEGES ON TABLE `+accountingTables+` FROM `+quotedRole); err != nil {
				return errors.New("cannot restrict model accounting relation access")
			}
		}
		for _, table := range serviceOwnedTables[service] {
			qualified := pgx.Identifier{schema, table}.Sanitize()
			if service != "model" {
				privileges := "INSERT,UPDATE,DELETE"
				switch table {
				case "case_intake_requests", "case_intake_archives":
					privileges = "INSERT"
				case "experience_feedback", "experience_candidate_versions", "experience_distill_inputs", "experience_distill_results", "experience_distill_failures", "advisory_judgments", "governed_knowledge_versions", "governed_knowledge_history", "legal_source_captures", "evidence_receipts", "evidence_request_events", "material_versions", "document_upload_idempotency", "subject_versions", "case_subject_role_versions", "subject_change_requests", "subject_change_decisions", "subject_write_idempotency", "subject_source_snapshots", "case_legal_applicability_fact_versions", "core_write_receipts", "execution_write_receipts", "manual_finding_records", "review_rulepack_inputs", "rule_pack_shadow_reports", "experience_finding_usage":
					privileges = "INSERT"
				case "evidence_requests", "case_intake_operations":
					privileges = "INSERT,UPDATE"
				case "service_object_versions", "service_object_contract_associations", "service_object_mutation_receipts", "operator_fulfillment_evaluation_objects":
					privileges = "INSERT"
				case "service_objects":
					privileges = "INSERT,UPDATE"
				case "operator_external_collection_claims":
					privileges = "INSERT,UPDATE"
				case "operator_mapping_lineages":
					privileges = "INSERT,UPDATE"
				case "operator_mapping_change_requests", "operator_mapping_change_decisions":
					privileges = "INSERT"
				}
				if _, err := tx.Exec(ctx, `GRANT `+privileges+` ON TABLE `+qualified+` TO `+quotedRole); err != nil {
					return fmt.Errorf("cannot configure %s table ownership", service)
				}
			}
			// A missing column raises undefined_column; an existing non-serial
			// column has no owned sequence and returns NULL.
			var sequence *string
			if err := tx.QueryRow(ctx, `SELECT CASE WHEN EXISTS (
				SELECT 1 FROM pg_attribute WHERE attrelid=$1::text::regclass
				AND attname='seq' AND attnum>0 AND NOT attisdropped
			) THEN pg_get_serial_sequence($1,'seq') ELSE NULL END`, qualified).Scan(&sequence); err != nil {
				return fmt.Errorf("cannot inspect %s sequence ownership", service)
			}
			if sequence != nil {
				if _, err := tx.Exec(ctx, `GRANT USAGE,SELECT ON SEQUENCE `+*sequence+` TO `+quotedRole); err != nil {
					return errors.New("cannot grant service sequence allocation")
				}
			}
		}
		if service == "core" {
			// The LLM endpoint registry is platform control data rather than a
			// tenant row, so it deliberately has no tenant_boundary policy. Every
			// role already has SELECT; only core may replace the row. The sealed
			// provider key is unreadable without LLM_CONFIG_KEY.
			llmTable := pgx.Identifier{schema, "llm_endpoint_settings"}.Sanitize()
			if _, err := tx.Exec(ctx, `GRANT INSERT,UPDATE ON TABLE `+llmTable+` TO `+quotedRole); err != nil {
				return errors.New("cannot grant LLM endpoint configuration ownership")
			}
			functions := []string{
				"experience_distill_lock_result(text,bigint,text,text)",
				"experience_distill_enqueue(text,text,text,text,text,text,text,text,text,text,bytea,text,text,integer,integer,integer,integer)",
			}
			for _, fn := range functions {
				if _, err := tx.Exec(ctx, `GRANT EXECUTE ON FUNCTION `+pgx.Identifier{schema}.Sanitize()+`.`+fn+` TO `+quotedRole); err != nil {
					return fmt.Errorf("cannot grant experience task function to core: %w", err)
				}
			}
		}
		if service == "model" {
			// Cross-tenant accounting is available only through the atomic budget
			// functions; the model role has no direct write access to these tables.
			functions := []string{
				"model_budget_reserve(text,text,text,text,text,text,text,text,integer,integer,integer,integer,integer,bigint,bigint,bigint,bigint,bigint)",
				"model_budget_finish(text,text,text,text,text,text,text,text,integer,text,text,text,bigint,bigint,bigint,timestamptz,boolean,text)",
				"model_budget_lock_experience_job(text,text,text,text,text,text,bigint,text)",
			}
			for _, fn := range functions {
				if _, err := tx.Exec(ctx, `GRANT EXECUTE ON FUNCTION `+pgx.Identifier{schema}.Sanitize()+`.`+fn+` TO `+quotedRole); err != nil {
					return fmt.Errorf("cannot grant model budget function %s to service role %q: %w", fn, cfg.User, err)
				}
			}
		}
		if service == "document" || service == "execution" {
			if _, err := tx.Exec(ctx, `GRANT USAGE,SELECT ON SEQUENCE `+pgx.Identifier{schema, "parsed_selection_order_seq"}.Sanitize()+` TO `+quotedRole); err != nil {
				return errors.New("cannot grant immutable parse identity allocation")
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO service_database_roles(role_name,service) VALUES($1,$2) ON CONFLICT(role_name) DO UPDATE SET service=EXCLUDED.service`, cfg.User, service); err != nil {
			return err
		}
		if service != "core" {
			if _, err := tx.Exec(ctx, `REVOKE ALL PRIVILEGES ON TABLE `+pgx.Identifier{schema, "case_intake_operations"}.Sanitize()+` FROM `+quotedRole); err != nil {
				return errors.New("cannot restrict case intake database access")
			}
			for _, table := range []string{"service_objects", "service_object_versions", "service_object_contract_associations", "service_object_mutation_receipts"} {
				qualified := pgx.Identifier{schema, table}.Sanitize()
				if _, err := tx.Exec(ctx, `REVOKE ALL PRIVILEGES ON TABLE `+qualified+` FROM `+quotedRole); err != nil {
					return errors.New("cannot restrict service object database access")
				}
				if service == "execution" && table != "service_object_mutation_receipts" {
					if _, err := tx.Exec(ctx, `GRANT SELECT ON TABLE `+qualified+` TO `+quotedRole); err != nil {
						return errors.New("cannot grant execution service object reads")
					}
				}
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("service role migration did not commit")
	}
	return nil
}
