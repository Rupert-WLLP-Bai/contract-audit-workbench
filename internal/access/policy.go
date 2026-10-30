// Package access defines a pure, deny-by-default case authorization policy.
// C0 consumers must supply trusted server-resolved claims; this package is not
// wired into the legacy HTTP application and is not an authentication service.
package access

import "time"

const PolicyVersion = "case-scope-v1"

type Action string

const (
	Read   Action = "case.read"
	Review Action = "case.review"
	Decide Action = "case.decide"
	Export Action = "case.export"
	Manage Action = "case.manage"
)

type Grant struct {
	Role           string    `json:"role"`
	Scope          string    `json:"scope"`
	OrgID          string    `json:"org_id,omitempty"`
	CaseID         string    `json:"case_id,omitempty"`
	ResolvedOrgIDs []string  `json:"resolved_org_ids,omitempty"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
}

type Principal struct {
	UserID   string  `json:"user_id"`
	TenantID string  `json:"tenant_id"`
	Enabled  bool    `json:"enabled"`
	Grants   []Grant `json:"grants"`
}

type Resource struct {
	TenantID string `json:"tenant_id"`
	CaseID   string `json:"case_id"`
	OrgID    string `json:"org_id"`
	OwnerID  string `json:"owner_id"`
}

type Decision struct {
	Allowed       bool   `json:"allowed"`
	Reason        string `json:"reason"`
	PolicyVersion string `json:"policy_version"`
}

// Check separates functional roles from their data scope. org_subtree requires
// an explicit, server-resolved set; IDs are never treated as path prefixes.
// Tenant/platform administrators have no implicit case-content permission.
func Check(action Action, subject Principal, resource Resource, now time.Time) Decision {
	result := Decision{Reason: "no_matching_grant", PolicyVersion: PolicyVersion}
	if !identityValid(subject, resource, now) {
		result.Reason = "invalid_or_disabled_identity"
		return result
	}
	if subject.TenantID != resource.TenantID {
		result.Reason = "tenant_boundary"
		return result
	}
	for _, grant := range subject.Grants {
		if !grantActive(grant, now) {
			continue
		}
		if !roleAllows(grant.Role, action) {
			continue
		}
		if grantScopeMatches(grant, subject, resource) {
			result.Allowed, result.Reason = true, "explicit_role_and_scope"
			return result
		}
	}
	return result
}

// identityValid requires an enabled, fully identified principal and resource
// plus a trusted clock.
func identityValid(subject Principal, resource Resource, now time.Time) bool {
	return subject.Enabled && subject.UserID != "" && subject.TenantID != "" &&
		resource.TenantID != "" && resource.CaseID != "" && !now.IsZero()
}

// grantActive accepts a grant with no expiry or one that expires in the future.
func grantActive(grant Grant, now time.Time) bool {
	return grant.ExpiresAt.IsZero() || now.Before(grant.ExpiresAt)
}

// grantScopeMatches checks one grant's data scope. An unknown scope never
// matches.
func grantScopeMatches(grant Grant, subject Principal, resource Resource) bool {
	switch grant.Scope {
	case "self":
		return resource.OwnerID == subject.UserID
	case "case":
		return grant.CaseID != "" && grant.CaseID == resource.CaseID
	case "org":
		return grant.OrgID != "" && grant.OrgID == resource.OrgID
	case "org_subtree":
		return subtreeMatches(grant.ResolvedOrgIDs, resource.OrgID)
	case "tenant":
		return true
	default:
		return false
	}
}

// subtreeMatches accepts only exact IDs from the server-resolved set.
func subtreeMatches(ids []string, orgID string) bool {
	match := false
	for _, id := range ids {
		match = match || (id != "" && id == orgID)
	}
	return match
}

func roleAllows(role string, action Action) bool {
	switch role {
	case "case_owner":
		return caseOwnerAllows(action)
	case "reviewer":
		return reviewerAllows(action)
	case "viewer":
		return action == Read
	case "exporter":
		return exporterAllows(action)
	default:
		return false
	}
}

// caseOwnerAllows is the full functional role.
func caseOwnerAllows(action Action) bool {
	return action == Read || action == Review || action == Decide || action == Export || action == Manage
}

// reviewerAllows excludes export and manage.
func reviewerAllows(action Action) bool {
	return action == Read || action == Review || action == Decide
}

// exporterAllows excludes review, decide and manage.
func exporterAllows(action Action) bool {
	return action == Read || action == Export
}
