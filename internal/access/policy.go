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
	if !subject.Enabled || subject.UserID == "" || subject.TenantID == "" ||
		resource.TenantID == "" || resource.CaseID == "" || now.IsZero() {
		result.Reason = "invalid_or_disabled_identity"
		return result
	}
	if subject.TenantID != resource.TenantID {
		result.Reason = "tenant_boundary"
		return result
	}
	for _, grant := range subject.Grants {
		if !grant.ExpiresAt.IsZero() && !now.Before(grant.ExpiresAt) {
			continue
		}
		if !roleAllows(grant.Role, action) {
			continue
		}
		match := false
		switch grant.Scope {
		case "self":
			match = resource.OwnerID == subject.UserID
		case "case":
			match = grant.CaseID != "" && grant.CaseID == resource.CaseID
		case "org":
			match = grant.OrgID != "" && grant.OrgID == resource.OrgID
		case "org_subtree":
			for _, id := range grant.ResolvedOrgIDs {
				match = match || (id != "" && id == resource.OrgID)
			}
		case "tenant":
			match = true
		}
		if match {
			result.Allowed, result.Reason = true, "explicit_role_and_scope"
			return result
		}
	}
	return result
}

func roleAllows(role string, action Action) bool {
	switch role {
	case "case_owner":
		return action == Read || action == Review || action == Decide || action == Export || action == Manage
	case "reviewer":
		return action == Read || action == Review || action == Decide
	case "viewer":
		return action == Read
	case "exporter":
		return action == Read || action == Export
	default:
		return false
	}
}
