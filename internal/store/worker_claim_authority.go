// File metadata (manifest mirror):
// Purpose: 实现 `ResolveWorkerClaimAuthority` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-03
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"encoding/json"
	"errors"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
	"contractaudit/internal/operationauthority"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ResolveWorkerClaimAuthority returns the current identity bound to one live
// review job and proves that identity can read the exact case. Callers choose a
// fixed phase; request data cannot weaken the SQL status checks.
func (s *Store) ResolveWorkerClaimAuthority(ctx context.Context, request ToolInvocationContext, phase string) (ToolAuthority, error) {
	return s.resolveWorkerClaimAuthority(ctx, request, phase, "")
}

func (s *Store) resolveWorkerClaimAuthority(ctx context.Context, request ToolInvocationContext, phase, expectedActor string) (ToolAuthority, error) {
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
	directory := accounts.LookupUserSnapshot(actorID.String)
	if directory.Status.State != "ready" && directory.Status.State != "local_demo" {
		return ToolAuthority{}, errors.New("trusted directory unavailable")
	}
	profile, exists := directory.User, directory.Found
	if !exists {
		// An enterprise claim cannot be judged against local demo identities,
		// nor a local claim against a different enterprise directory.
		modeMatches := principalID.Valid && membershipID.Valid && directory.Status.Mode == "enterprise" ||
			!principalID.Valid && !membershipID.Valid && directory.Status.Mode == "local_demo"
		if modeMatches && expectedActor != "" && actorID.String == expectedActor {
			return ToolAuthority{}, operationauthority.ErrConfirmedRevocation
		}
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
	scope := model.DelegatedScope{Kind: "execution", TenantID: request.TenantID, OrgID: request.OrgID, CaseID: request.CaseID, ActorID: user.ID, JobID: request.JobID, WorkerID: request.WorkerID, Generation: request.Generation, RunID: request.RunID, AttemptID: request.AttemptID}
	if request.RunID == "" {
		scope.Kind = "experience_distill"
	}
	encoded, _ := json.Marshal(scope)
	var digest, revision string
	var version int64
	var currentRole *string
	var currentGrants []byte
	if err = s.pool.QueryRow(ctx, `SELECT session_digest,context_version,authority_revision,role_code,grants FROM tenant_operation_info($1::jsonb,'')`, encoded).Scan(&digest, &version, &revision, &currentRole, &currentGrants); err != nil {
		return ToolAuthority{}, contextError(err)
	}
	if user.HasResolvedMembership() {
		var grants []access.Grant
		if currentRole == nil || *currentRole != user.Role || json.Unmarshal(currentGrants, &grants) != nil || !sameMembershipGrants(user.Grants, grants) {
			return ToolAuthority{}, toolScopeRefusal("membership_revision_changed")
		}
	}
	user = accounts.WithCommandAuthority(user, accounts.CommandAuthority{Revision: revision, OperationScope: encoded})
	return ToolAuthority{User: user, TenantID: tenantID, OrgID: orgID, CaseID: request.CaseID,
		RunID: request.RunID, AttemptID: request.AttemptID, JobID: request.JobID}, nil
}
