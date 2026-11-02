package store

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The executable classification is runtime source, not an observed DB report.
// Adding a table requires an explicit category; there is no name-prefix escape.
//
//go:embed table_catalog.json
var tableCatalogFiles embed.FS

type isolationRelation struct {
	Name           string `json:"name"`
	Classification string `json:"classification"`
	TenantColumn   bool   `json:"tenant_column"`
	ForceRLS       bool   `json:"force_rls"`
	Exception      string `json:"exception,omitempty"`
}
type isolationCatalog struct {
	Version       int                 `json:"version"`
	SchemaVersion int                 `json:"schema_version"`
	Relations     []isolationRelation `json:"relations"`
}

type isolationQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readIsolationCatalog() (map[string]isolationRelation, error) {
	raw, err := tableCatalogFiles.ReadFile("table_catalog.json")
	if err != nil {
		return nil, err
	}
	var catalog isolationCatalog
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&catalog); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("table classification has trailing data")
	}
	if catalog.Version != 1 || catalog.SchemaVersion != schemaMaxCompatibleVersion {
		return nil, errors.New("table classification does not match the application schema")
	}
	expected := map[string]isolationRelation{}
	for _, entry := range catalog.Relations {
		if !identRE.MatchString(entry.Name) {
			return nil, errors.New("invalid classified relation name")
		}
		if _, duplicate := expected[entry.Name]; duplicate {
			return nil, errors.New("duplicate relation classification")
		}
		switch entry.Classification {
		case "TENANT_DATA":
			if !entry.TenantColumn || !entry.ForceRLS || entry.Exception != "" {
				return nil, fmt.Errorf("tenant relation %q lacks its mandatory tenant/FORCE boundary", entry.Name)
			}
		case "AUTH_CONTROL", "QUEUE_CONTROL":
			if !entry.ForceRLS || strings.TrimSpace(entry.Exception) == "" {
				return nil, fmt.Errorf("control relation %q lacks an explicit narrow boundary", entry.Name)
			}
		case "PLATFORM_CONTROL":
			if entry.TenantColumn || entry.ForceRLS || strings.TrimSpace(entry.Exception) == "" {
				return nil, fmt.Errorf("platform relation %q has an inconsistent classification", entry.Name)
			}
		default:
			return nil, errors.New("unknown relation classification")
		}
		expected[entry.Name] = entry
	}
	// Reconcile the registry with frozen migration declarations, not a stale
	// hand-maintained count. schema_migrations is the one Go-created ledger.
	declared := map[string]bool{"schema_migrations": true}
	declaration := regexp.MustCompile(`(?i)\bCREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(?:\{\{SCHEMA\}\}\.)?([a-z_][a-z_0-9]*)`)
	migrations, err := migrationCatalog()
	if err != nil {
		return nil, err
	}
	for _, migration := range migrations {
		for _, match := range declaration.FindAllStringSubmatch(migration.sql, -1) {
			declared[match[1]] = true
		}
	}
	if len(expected) != len(declared) {
		return nil, errors.New("migration relation declarations and table classifications differ")
	}
	for name := range declared {
		if _, ok := expected[name]; !ok {
			return nil, fmt.Errorf("migration relation %q is unclassified", name)
		}
	}
	return expected, nil
}

func normalizedTenantPolicy(raw *string) string {
	if raw == nil {
		return ""
	}
	s := strings.ToLower(*raw)
	for _, part := range []string{" ", "\t", "\r", "\n", "(", ")", "::text"} {
		s = strings.ReplaceAll(s, part, "")
	}
	return s
}

// checkTableIsolationCatalog is called only when opening an application Store.
// Offline migrations retain their dedicated connection. This guard observes
// actual catalog state at startup; it does not replace A/B behavioral tests.
func checkTableIsolationCatalog(ctx context.Context, q isolationQueryer, schema string) error {
	expected, err := readIsolationCatalog()
	if err != nil {
		return err
	}
	var unsafe bool
	if err = q.QueryRow(ctx, `SELECT rolsuper OR rolbypassrls OR rolcreaterole OR rolcreatedb OR rolreplication OR rolinherit OR EXISTS(SELECT 1 FROM pg_auth_members m WHERE m.member=pg_roles.oid) FROM pg_roles WHERE rolname=current_user`).Scan(&unsafe); err != nil {
		return err
	}
	if unsafe {
		return errors.New("application isolation requires a dedicated non-superuser NOINHERIT login without role/database creation")
	}
	rows, err := q.Query(ctx, `SELECT c.relname,c.relrowsecurity,c.relforcerowsecurity,EXISTS(SELECT 1 FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attname='tenant_id' AND NOT a.attisdropped),p.polpermissive,pg_get_expr(p.polqual,p.polrelid),pg_get_expr(p.polwithcheck,p.polrelid)
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_policy p ON p.polrelid=c.oid AND p.polname='tenant_boundary'
 WHERE n.nspname=$1 AND c.relkind IN('r','p') AND NOT EXISTS(SELECT 1 FROM pg_depend d WHERE d.classid='pg_class'::regclass AND d.objid=c.oid AND d.deptype='e') ORDER BY c.relname`, schema)
	if err != nil {
		return err
	}
	observed := map[string]bool{}
	for rows.Next() {
		var name string
		var enabled, forced, tenant bool
		var permissive *bool
		var using, check *string
		if err = rows.Scan(&name, &enabled, &forced, &tenant, &permissive, &using, &check); err != nil {
			rows.Close()
			return err
		}
		entry, known := expected[name]
		if !known {
			rows.Close()
			return fmt.Errorf("application relation %q has no reviewed isolation classification", name)
		}
		observed[name] = true
		if tenant != entry.TenantColumn || entry.ForceRLS && (!enabled || !forced) {
			rows.Close()
			return fmt.Errorf("relation %q does not match its tenant/FORCE isolation classification", name)
		}
		if entry.Classification == "TENANT_DATA" {
			canonical := "tenant_id=current_setting'app.tenant_id',true"
			if permissive == nil || !*permissive || normalizedTenantPolicy(using) != canonical || normalizedTenantPolicy(check) != canonical {
				rows.Close()
				return fmt.Errorf("tenant relation %q lacks the exact USING/WITH CHECK tenant boundary", name)
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for name := range expected {
		if !observed[name] {
			return fmt.Errorf("classified application relation %q is missing", name)
		}
	}
	// Current identity/support additions must retain composite parent identity.
	// Historical references are not mechanically rewritten by this guard.
	links := []struct {
		child, parent       string
		columns, referenced []string
	}{
		{"identity_org_units", "identity_org_units", []string{"tenant_id", "parent_org_unit_id"}, []string{"tenant_id", "org_unit_id"}},
		{"identity_memberships", "identity_org_units", []string{"tenant_id", "org_unit_id"}, []string{"tenant_id", "org_unit_id"}},
		{"sessions", "identity_memberships", []string{"membership_id", "principal_id", "tenant_id", "org_id"}, []string{"membership_id", "principal_id", "tenant_id", "org_unit_id"}},
		{"worker_claim_identities", "identity_memberships", []string{"membership_id", "principal_id", "tenant_id", "membership_org_id"}, []string{"membership_id", "principal_id", "tenant_id", "org_unit_id"}},
		{"intake_authority_tickets", "identity_memberships", []string{"membership_id", "principal_id", "tenant_id", "org_id"}, []string{"membership_id", "principal_id", "tenant_id", "org_unit_id"}},
		{"operation_authority_tickets", "identity_memberships", []string{"membership_id", "principal_id", "tenant_id", "org_id"}, []string{"membership_id", "principal_id", "tenant_id", "org_unit_id"}},
		{"identity_support_grants", "cases", []string{"tenant_id", "org_id", "case_id"}, []string{"tenant_id", "org_id", "id"}},
	}
	for _, link := range links {
		var present bool
		if err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint c WHERE c.contype='f' AND c.convalidated AND c.conrelid=to_regclass(format('%I.%I',$1::text,$2::text)) AND c.confrelid=to_regclass(format('%I.%I',$1::text,$3::text))
   AND ARRAY(SELECT a.attname::text FROM unnest(c.conkey) WITH ORDINALITY k(num,ord) JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=k.num ORDER BY k.ord)=$4::text[]
   AND ARRAY(SELECT a.attname::text FROM unnest(c.confkey) WITH ORDINALITY k(num,ord) JOIN pg_attribute a ON a.attrelid=c.confrelid AND a.attnum=k.num ORDER BY k.ord)=$5::text[])`, schema, link.child, link.parent, link.columns, link.referenced).Scan(&present); err != nil {
			return err
		}
		if !present {
			return fmt.Errorf("relation %q lacks its reviewed composite parent identity", link.child)
		}
	}
	return nil
}
