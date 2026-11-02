// File metadata (manifest mirror):
// Purpose: 实现 `SessionProjection` 等 Core 操作授权投影与客户端调用，校验内部票据和主体上下文。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

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
