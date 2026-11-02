package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"

	"github.com/jackc/pgx/v5"
)

// IdentityCrosswalk is an explicit offline mapping. Every source and target
// identifier is supplied directly; display names, email, and historical case
// ownership are never used to infer membership.
type IdentityCrosswalk struct {
	Version       int                       `json:"version"`
	Jurisdictions []IdentityJurisdictionMap `json:"jurisdictions"`
	Tenants       []IdentityTenantMap       `json:"tenants"`
	OrgUnits      []IdentityOrgUnitMap      `json:"org_units"`
	Principals    []IdentityPrincipalMap    `json:"principals"`
	Memberships   []IdentityMembershipMap   `json:"memberships"`
}

type IdentityJurisdictionMap struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
}

type IdentityTenantMap struct {
	ID             string  `json:"id"`
	DisplayName    string  `json:"display_name"`
	JurisdictionID *string `json:"jurisdiction_id,omitempty"`
	Status         string  `json:"status"`
}

type IdentityOrgUnitMap struct {
	TenantID    string  `json:"tenant_id"`
	ID          string  `json:"id"`
	ParentOrgID *string `json:"parent_org_id,omitempty"`
	DisplayName string  `json:"display_name"`
	Status      string  `json:"status"`
}

type IdentityPrincipalMap struct {
	ID      string `json:"id"`
	ActorID string `json:"actor_id"`
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
	Status  string `json:"status"`
}

type IdentityMembershipMap struct {
	ID             string         `json:"id"`
	ActorID        string         `json:"actor_id"`
	TenantID       string         `json:"tenant_id"`
	OrgUnitID      string         `json:"org_unit_id"`
	RoleCode       string         `json:"role_code"`
	Grants         []access.Grant `json:"grants"`
	Status         string         `json:"status"`
	EffectiveFrom  *time.Time     `json:"effective_from,omitempty"`
	EffectiveUntil *time.Time     `json:"effective_until,omitempty"`
}

type IdentityImportSummary struct {
	Jurisdictions int  `json:"jurisdictions"`
	Tenants       int  `json:"tenants"`
	OrgUnits      int  `json:"org_units"`
	Principals    int  `json:"principals"`
	Memberships   int  `json:"memberships"`
	Applied       bool `json:"applied"`
}

// DecodeIdentityCrosswalk rejects unknown fields and trailing JSON so an
// operator cannot unknowingly apply a partially understood mapping.
func DecodeIdentityCrosswalk(raw []byte) (IdentityCrosswalk, error) {
	var document IdentityCrosswalk
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return IdentityCrosswalk{}, fmt.Errorf("invalid identity crosswalk: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return IdentityCrosswalk{}, errors.New("identity crosswalk must contain exactly one JSON object")
	}
	if err := validateIdentityCrosswalk(document); err != nil {
		return IdentityCrosswalk{}, err
	}
	return document, nil
}

func validateIdentityCrosswalk(document IdentityCrosswalk) error {
	if document.Version != 1 {
		return errors.New("identity crosswalk version must be 1")
	}
	seenJurisdictions := map[string]struct{}{}
	for _, item := range document.Jurisdictions {
		if !nonblank(item.ID) || !nonblank(item.DisplayName) || !oneOf(item.Status, "active", "inactive") {
			return errors.New("identity crosswalk has an invalid jurisdiction")
		}
		if _, exists := seenJurisdictions[item.ID]; exists {
			return errors.New("identity crosswalk repeats a jurisdiction ID")
		}
		seenJurisdictions[item.ID] = struct{}{}
	}
	seenTenants := map[string]struct{}{}
	for _, item := range document.Tenants {
		if !nonblank(item.ID) || !nonblank(item.DisplayName) || !oneOf(item.Status, "active", "suspended", "closed") {
			return errors.New("identity crosswalk has an invalid tenant")
		}
		if _, exists := seenTenants[item.ID]; exists {
			return errors.New("identity crosswalk repeats a tenant ID")
		}
		seenTenants[item.ID] = struct{}{}
	}
	seenOrgUnits := map[string]struct{}{}
	for _, item := range document.OrgUnits {
		if !nonblank(item.TenantID) || !nonblank(item.ID) || !nonblank(item.DisplayName) || !oneOf(item.Status, "active", "inactive") {
			return errors.New("identity crosswalk has an invalid organization unit")
		}
		key := item.TenantID + "\x00" + item.ID
		if _, exists := seenOrgUnits[key]; exists {
			return errors.New("identity crosswalk repeats a tenant organization unit")
		}
		seenOrgUnits[key] = struct{}{}
	}
	seenActors, seenSubjects := map[string]struct{}{}, map[string]struct{}{}
	for _, item := range document.Principals {
		if !uuidLike(item.ID) || !nonblank(item.ActorID) || !nonblank(item.Issuer) || !nonblank(item.Subject) || !oneOf(item.Status, "active", "disabled") {
			return errors.New("identity crosswalk has an invalid principal mapping")
		}
		subjectKey := item.Issuer + "\x00" + item.Subject
		if _, exists := seenActors[item.ActorID]; exists {
			return errors.New("identity crosswalk repeats an actor ID")
		}
		if _, exists := seenSubjects[subjectKey]; exists {
			return errors.New("identity crosswalk repeats an issuer/subject pair")
		}
		seenActors[item.ActorID], seenSubjects[subjectKey] = struct{}{}, struct{}{}
	}
	seenMemberships, seenMembershipScopes := map[string]struct{}{}, map[string]struct{}{}
	for _, item := range document.Memberships {
		if !uuidLike(item.ID) || !nonblank(item.ActorID) || !nonblank(item.TenantID) || !nonblank(item.OrgUnitID) ||
			!oneOf(item.RoleCode, "admin", "clerk", "reviewer", "viewer") || !oneOf(item.Status, "active", "suspended", "revoked") {
			return errors.New("identity crosswalk has an invalid membership")
		}
		scopeKey := item.ActorID + "\x00" + item.TenantID + "\x00" + item.OrgUnitID
		if _, exists := seenMemberships[item.ID]; exists {
			return errors.New("identity crosswalk repeats a membership ID")
		}
		if _, exists := seenMembershipScopes[scopeKey]; exists {
			return errors.New("identity crosswalk repeats a principal/tenant/organization membership")
		}
		seenMemberships[item.ID], seenMembershipScopes[scopeKey] = struct{}{}, struct{}{}
		if item.EffectiveFrom != nil && item.EffectiveUntil != nil && !item.EffectiveUntil.After(*item.EffectiveFrom) {
			return errors.New("identity crosswalk membership effective_until must follow effective_from")
		}
		for _, grant := range item.Grants {
			if !oneOf(grant.Role, "case_owner", "reviewer", "viewer", "exporter") ||
				!oneOf(grant.Scope, "self", "case", "org", "org_subtree", "tenant") {
				return errors.New("identity crosswalk has an unknown role or grant scope")
			}
			switch grant.Scope {
			case "case":
				if !nonblank(grant.CaseID) {
					return errors.New("case grant must name an exact case ID")
				}
			case "org":
				if !nonblank(grant.OrgID) {
					return errors.New("organization grant must name an exact organization ID")
				}
			case "org_subtree":
				if len(grant.ResolvedOrgIDs) == 0 {
					return errors.New("organization subtree grant must list exact resolved organization IDs")
				}
			}
		}
	}
	return nil
}

// ImportIdentityCrosswalk applies a fully explicit mapping in one transaction.
// A dry run executes the same writes and constraints, then rolls the transaction
// back. The connection must be a trusted offline database administrator; Core
// and other runtime roles cannot invoke this operation.
func ImportIdentityCrosswalk(ctx context.Context, adminDSN, schema, expectedDatabase string, document IdentityCrosswalk, apply bool) (IdentityImportSummary, error) {
	if err := validateIdentityCrosswalk(document); err != nil {
		return IdentityImportSummary{}, err
	}
	if _, err := quoteIdent(schema); err != nil {
		return IdentityImportSummary{}, fmt.Errorf("invalid identity import schema: %w", err)
	}
	conn, err := connectPoolDSN(ctx, adminDSN)
	if err != nil {
		return IdentityImportSummary{}, errors.New("cannot connect using the identity import administrator")
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return IdentityImportSummary{}, errors.New("cannot begin identity crosswalk import")
	}
	defer tx.Rollback(ctx)
	var database, administrator string
	if err := tx.QueryRow(ctx, `SELECT current_database(),current_user`).Scan(&database, &administrator); err != nil {
		return IdentityImportSummary{}, err
	}
	var isSuperuser bool
	if err := tx.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname=$1`, administrator).Scan(&isSuperuser); err != nil || !isSuperuser {
		return IdentityImportSummary{}, errors.New("identity crosswalk import requires a trusted superuser administrator connection")
	}
	if database != expectedDatabase {
		return IdentityImportSummary{}, errors.New("identity import administrator selected a different APP_ENV database")
	}
	qualifiedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, schema+":tenant-identity"); err != nil {
		return IdentityImportSummary{}, err
	}
	var schemaExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, schema).Scan(&schemaExists); err != nil || !schemaExists {
		return IdentityImportSummary{}, errors.New("identity import schema does not exist")
	}
	var migrationVersion int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version),0) FROM `+qualifiedSchema+`.schema_migrations`).Scan(&migrationVersion); err != nil || migrationVersion < 33 {
		return IdentityImportSummary{}, errors.New("identity import requires completed tenant membership schema 0033")
	}
	for _, item := range document.Principals {
		actor, ok := accounts.ByID(item.ActorID)
		if !ok || actor.Issuer != item.Issuer || actor.Subject != item.Subject {
			return IdentityImportSummary{}, fmt.Errorf("identity crosswalk actor %q does not exactly match the configured identity directory issuer and subject", item.ActorID)
		}
		if item.Status == "active" && !actor.Enabled {
			return IdentityImportSummary{}, fmt.Errorf("identity crosswalk cannot activate disabled directory actor %q", item.ActorID)
		}
	}
	// Identity mutation uses the same tenant -> membership order as commands.
	// Include every existing membership of touched principals and organizations,
	// not only rows explicitly repeated in this import document.
	tenantIDs := []string{}
	principalIDs := []string{}
	for _, v := range document.Tenants {
		tenantIDs = append(tenantIDs, v.ID)
	}
	for _, v := range document.OrgUnits {
		tenantIDs = append(tenantIDs, v.TenantID)
	}
	for _, v := range document.Memberships {
		tenantIDs = append(tenantIDs, v.TenantID)
		principalIDs = append(principalIDs, v.ActorID)
	}
	for _, v := range document.Principals {
		principalIDs = append(principalIDs, v.ActorID)
	}
	if _, err := tx.Exec(ctx, `SELECT t.tenant_id FROM `+qualifiedSchema+`.identity_tenants t WHERE t.tenant_id=ANY($1::text[]) OR t.tenant_id IN(SELECT m.tenant_id FROM `+qualifiedSchema+`.identity_memberships m JOIN `+qualifiedSchema+`.identity_principals p USING(principal_id) WHERE p.actor_id=ANY($2::text[])) ORDER BY t.tenant_id FOR UPDATE`, tenantIDs, principalIDs); err != nil {
		return IdentityImportSummary{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT m.membership_id FROM `+qualifiedSchema+`.identity_memberships m JOIN `+qualifiedSchema+`.identity_principals p USING(principal_id) WHERE m.tenant_id=ANY($1::text[]) OR p.actor_id=ANY($2::text[]) ORDER BY m.membership_id FOR UPDATE OF m`, tenantIDs, principalIDs); err != nil {
		return IdentityImportSummary{}, err
	}
	// Touch the locked tenant tuples even for principal/org-only changes. A
	// waiting Repeatable Read writer must see an MVCC update conflict rather
	// than continuing with an older principal/org snapshot.
	if _, err := tx.Exec(ctx, `UPDATE `+qualifiedSchema+`.identity_tenants t SET revision=t.revision WHERE t.tenant_id=ANY($1::text[]) OR t.tenant_id IN(SELECT m.tenant_id FROM `+qualifiedSchema+`.identity_memberships m JOIN `+qualifiedSchema+`.identity_principals p USING(principal_id) WHERE p.actor_id=ANY($2::text[]))`, tenantIDs, principalIDs); err != nil {
		return IdentityImportSummary{}, err
	}
	if err := importIdentityDirectoryRows(ctx, tx, qualifiedSchema, document); err != nil {
		return IdentityImportSummary{}, err
	}
	summary := IdentityImportSummary{Jurisdictions: len(document.Jurisdictions), Tenants: len(document.Tenants),
		OrgUnits: len(document.OrgUnits), Principals: len(document.Principals), Memberships: len(document.Memberships), Applied: apply}
	if apply {
		if err := tx.Commit(ctx); err != nil {
			return IdentityImportSummary{}, err
		}
	}
	return summary, nil
}

func importIdentityDirectoryRows(ctx context.Context, tx pgx.Tx, schema string, document IdentityCrosswalk) error {
	for _, item := range document.Jurisdictions {
		if _, err := tx.Exec(ctx, `INSERT INTO `+schema+`.identity_jurisdictions(jurisdiction_id,display_name,status)
			VALUES($1,$2,$3) ON CONFLICT(jurisdiction_id) DO UPDATE SET display_name=EXCLUDED.display_name,status=EXCLUDED.status,
			revision=CASE WHEN identity_jurisdictions.display_name IS DISTINCT FROM EXCLUDED.display_name OR identity_jurisdictions.status IS DISTINCT FROM EXCLUDED.status THEN identity_jurisdictions.revision+1 ELSE identity_jurisdictions.revision END,
			updated_at=CASE WHEN identity_jurisdictions.display_name IS DISTINCT FROM EXCLUDED.display_name OR identity_jurisdictions.status IS DISTINCT FROM EXCLUDED.status THEN clock_timestamp() ELSE identity_jurisdictions.updated_at END`, item.ID, item.DisplayName, item.Status); err != nil {
			return fmt.Errorf("import explicit jurisdiction %q: %w", item.ID, err)
		}
	}
	for _, item := range document.Tenants {
		if _, err := tx.Exec(ctx, `INSERT INTO `+schema+`.identity_tenants(tenant_id,display_name,jurisdiction_id,status)
			VALUES($1,$2,$3,$4) ON CONFLICT(tenant_id) DO UPDATE SET display_name=EXCLUDED.display_name,jurisdiction_id=EXCLUDED.jurisdiction_id,status=EXCLUDED.status,
			revision=CASE WHEN identity_tenants.display_name IS DISTINCT FROM EXCLUDED.display_name OR identity_tenants.jurisdiction_id IS DISTINCT FROM EXCLUDED.jurisdiction_id OR identity_tenants.status IS DISTINCT FROM EXCLUDED.status THEN identity_tenants.revision+1 ELSE identity_tenants.revision END,
			updated_at=CASE WHEN identity_tenants.display_name IS DISTINCT FROM EXCLUDED.display_name OR identity_tenants.jurisdiction_id IS DISTINCT FROM EXCLUDED.jurisdiction_id OR identity_tenants.status IS DISTINCT FROM EXCLUDED.status THEN clock_timestamp() ELSE identity_tenants.updated_at END`, item.ID, item.DisplayName, item.JurisdictionID, item.Status); err != nil {
			return fmt.Errorf("import explicit tenant %q: %w", item.ID, err)
		}
	}
	for _, item := range document.OrgUnits {
		if _, err := tx.Exec(ctx, `INSERT INTO `+schema+`.identity_org_units(tenant_id,org_unit_id,parent_org_unit_id,display_name,status)
			VALUES($1,$2,$3,$4,$5) ON CONFLICT(tenant_id,org_unit_id) DO UPDATE SET parent_org_unit_id=EXCLUDED.parent_org_unit_id,display_name=EXCLUDED.display_name,status=EXCLUDED.status,
			revision=identity_org_units.revision+CASE WHEN identity_org_units.parent_org_unit_id IS DISTINCT FROM EXCLUDED.parent_org_unit_id OR identity_org_units.display_name IS DISTINCT FROM EXCLUDED.display_name OR identity_org_units.status IS DISTINCT FROM EXCLUDED.status THEN 1 ELSE 0 END`, item.TenantID, item.ID, item.ParentOrgID, item.DisplayName, item.Status); err != nil {
			return fmt.Errorf("import explicit organization unit %q: %w", item.ID, err)
		}
	}
	for _, item := range document.Principals {
		var conflicting bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+schema+`.identity_principals
			WHERE (actor_id=$1 AND (principal_id<>$4::uuid OR issuer<>$2 OR subject<>$3))
			OR (issuer=$2 AND subject=$3 AND actor_id<>$1) OR (principal_id=$4::uuid AND actor_id<>$1))`,
			item.ActorID, item.Issuer, item.Subject, item.ID).Scan(&conflicting); err != nil {
			return fmt.Errorf("inspect explicit principal mapping: %w", err)
		}
		if conflicting {
			return fmt.Errorf("identity crosswalk actor %q conflicts with an existing issuer/subject binding", item.ActorID)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+schema+`.identity_principals(principal_id,actor_id,issuer,subject,status)
			VALUES($1::uuid,$2,$3,$4,$5) ON CONFLICT(actor_id) DO UPDATE SET status=EXCLUDED.status,
			revision=CASE WHEN identity_principals.status IS DISTINCT FROM EXCLUDED.status THEN identity_principals.revision+1 ELSE identity_principals.revision END,
			updated_at=CASE WHEN identity_principals.status IS DISTINCT FROM EXCLUDED.status THEN clock_timestamp() ELSE identity_principals.updated_at END`, item.ID, item.ActorID, item.Issuer, item.Subject, item.Status); err != nil {
			return fmt.Errorf("import explicit principal %q: %w", item.ActorID, err)
		}
	}
	for _, item := range document.Memberships {
		var principalID string
		if err := tx.QueryRow(ctx, `SELECT principal_id FROM `+schema+`.identity_principals WHERE actor_id=$1`, item.ActorID).Scan(&principalID); err != nil {
			return fmt.Errorf("membership actor %q has no exact principal mapping", item.ActorID)
		}
		var conflict bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+schema+`.identity_memberships
			WHERE (membership_id=$1::uuid AND (principal_id<>$2::uuid OR tenant_id<>$3 OR org_unit_id<>$4))
			OR (principal_id=$2::uuid AND tenant_id=$3 AND org_unit_id=$4 AND membership_id<>$1::uuid))`, item.ID, principalID, item.TenantID, item.OrgUnitID).Scan(&conflict); err != nil {
			return fmt.Errorf("inspect membership identity conflict: %w", err)
		}
		if conflict {
			return fmt.Errorf("identity crosswalk membership %q conflicts with an existing principal/tenant/org binding", item.ID)
		}
		grants, err := json.Marshal(item.Grants)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+schema+`.identity_memberships(membership_id,principal_id,tenant_id,org_unit_id,role_code,grants,status,effective_from,effective_until)
			VALUES($1::uuid,$2::uuid,$3,$4,$5,$6::jsonb,$7,$8,$9)
			ON CONFLICT(membership_id) DO UPDATE SET role_code=EXCLUDED.role_code,grants=EXCLUDED.grants,status=EXCLUDED.status,
			effective_from=EXCLUDED.effective_from,effective_until=EXCLUDED.effective_until,
			revision=identity_memberships.revision+CASE WHEN identity_memberships.role_code IS DISTINCT FROM EXCLUDED.role_code OR identity_memberships.grants IS DISTINCT FROM EXCLUDED.grants OR identity_memberships.status IS DISTINCT FROM EXCLUDED.status OR identity_memberships.effective_from IS DISTINCT FROM EXCLUDED.effective_from OR identity_memberships.effective_until IS DISTINCT FROM EXCLUDED.effective_until THEN 1 ELSE 0 END,
			updated_at=CASE WHEN identity_memberships.role_code IS DISTINCT FROM EXCLUDED.role_code OR identity_memberships.grants IS DISTINCT FROM EXCLUDED.grants OR identity_memberships.status IS DISTINCT FROM EXCLUDED.status OR identity_memberships.effective_from IS DISTINCT FROM EXCLUDED.effective_from OR identity_memberships.effective_until IS DISTINCT FROM EXCLUDED.effective_until THEN clock_timestamp() ELSE identity_memberships.updated_at END`,
			item.ID, principalID, item.TenantID, item.OrgUnitID, item.RoleCode, string(grants), item.Status, item.EffectiveFrom, item.EffectiveUntil); err != nil {
			return fmt.Errorf("import explicit membership %q: %w", item.ID, err)
		}
	}
	return nil
}

func nonblank(value string) bool { return strings.TrimSpace(value) != "" }

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func uuidLike(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
		} else if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
