package store

import (
	"context"
	"encoding/json"
	"errors"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ResolveWorkerClaimAuthority returns the current identity bound to one live
// review job and proves that identity can read the exact case. Callers choose a
// fixed phase; request data cannot weaken the SQL status checks.
func (s *Store) ResolveWorkerClaimAuthority(ctx context.Context, request ToolInvocationContext, phase string) (ToolAuthority, error) {
	if s == nil || s.pool == nil || phase != "worker_start" && phase != "tool_read" ||
		request.TenantID == "" || request.OrgID == "" || request.JobID == "" || request.WorkerID == "" ||
		request.Generation <= 0 || request.CaseID == "" || (request.RunID == "") != (request.AttemptID == "") {
		return ToolAuthority{}, toolScopeRefusal("request_incomplete")
	}
	var principalID, membershipID pgtype.UUID
	var actorID, issuer, subject, role, membershipOrg pgtype.Text
	var tenantID, orgID, ownerID string
	var grantsJSON, memberIDsJSON []byte
	err := s.pool.QueryRow(ctx, `SELECT principal_id,actor_id,issuer,subject,membership_id,tenant_id,org_id,membership_org_id,role_code,grants,
		case_owner_id,case_member_ids FROM tenant_identity_claim_user($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		request.TenantID, request.OrgID, request.JobID, request.WorkerID, request.Generation,
		request.CaseID, request.RunID, request.AttemptID, phase).Scan(
		&principalID, &actorID, &issuer, &subject, &membershipID, &tenantID, &orgID, &membershipOrg, &role, &grantsJSON, &ownerID, &memberIDsJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ToolAuthority{}, toolScopeRefusal("claim_or_membership_unavailable")
		}
		return ToolAuthority{}, err
	}
	if !actorID.Valid || tenantID != request.TenantID || orgID != request.OrgID {
		return ToolAuthority{}, toolScopeRefusal("claim_identity_mismatch")
	}
	profile, exists := accounts.ByID(actorID.String)
	if !exists || !profile.Enabled {
		return ToolAuthority{}, toolScopeRefusal("actor_untrusted")
	}
	var user accounts.User
	if principalID.Valid {
		if !membershipOrg.Valid || membershipOrg.String == "" || !membershipID.Valid || !issuer.Valid || !subject.Valid || !role.Valid || issuer.String == "" || subject.String == "" ||
			profile.Issuer != issuer.String || profile.Subject != subject.String {
			return ToolAuthority{}, toolScopeRefusal("membership_binding_unavailable")
		}
		var grants []access.Grant
		if err := json.Unmarshal(grantsJSON, &grants); err != nil {
			return ToolAuthority{}, toolScopeRefusal("membership_grants_unreadable")
		}
		user = accounts.MembershipUser(actorID.String, profile.DisplayName, role.String, tenantID, membershipOrg.String,
			issuer.String, subject.String, grants)
		user.PrincipalID, user.MembershipID = principalID.String(), membershipID.String()
	} else {
		// Only new locally captured demo jobs may have an explicit unbound
		// identity row. Pre-migration jobs have no row and never reach this path.
		if membershipID.Valid || issuer.Valid && issuer.String != "" || subject.Valid && subject.String != "" ||
			profile.Issuer != "" || profile.Subject != "" || profile.TenantID != tenantID ||
			(tenantID != accounts.LocalDemoTenant && tenantID != accounts.LocalDemoTenant2) {
			return ToolAuthority{}, toolScopeRefusal("legacy_identity_unavailable")
		}
		user = profile
	}
	var memberIDs []string
	if err := json.Unmarshal(memberIDsJSON, &memberIDs); err != nil {
		return ToolAuthority{}, toolScopeRefusal("case_members_unreadable")
	}
	caseItem := Case{ID: request.CaseID, TenantID: tenantID, OwnerOrgID: orgID, OwnerID: ownerID, MemberIDs: memberIDs}
	if !authorizeCase(caseItem, user, access.Read) {
		return ToolAuthority{}, toolScopeRefusal("case_grant_missing")
	}
	return ToolAuthority{User: user, TenantID: tenantID, OrgID: orgID, CaseID: request.CaseID,
		RunID: request.RunID, AttemptID: request.AttemptID, JobID: request.JobID}, nil
}
