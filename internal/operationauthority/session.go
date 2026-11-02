package operationauthority

import "contractaudit/internal/access"

type SessionProjection struct {
	Audience          string         `json:"audience"`
	MembershipBound   bool           `json:"membership_bound"`
	ActorID           string         `json:"actor_id"`
	DisplayName       string         `json:"display_name"`
	Role              string         `json:"role"`
	TenantID          string         `json:"tenant_id"`
	OrgID             string         `json:"org_id"`
	Enabled           bool           `json:"enabled"`
	Issuer            string         `json:"issuer,omitempty"`
	Subject           string         `json:"subject,omitempty"`
	PrincipalID       string         `json:"principal_id,omitempty"`
	MembershipID      string         `json:"membership_id,omitempty"`
	Grants            []access.Grant `json:"grants"`
	ContextVersion    string         `json:"context_version,omitempty"`
	CSRFToken         string         `json:"csrf_token,omitempty"`
	AuthorityRevision string         `json:"authority_revision,omitempty"`
	TenantName        string         `json:"tenant_name,omitempty"`
	OrgName           string         `json:"org_name,omitempty"`
}
