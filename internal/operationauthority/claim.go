// File metadata (manifest mirror):
// Purpose: 实现 `ClaimRequest`, `ClaimProjection` 等 Core 操作授权投影与客户端调用，校验内部票据和主体上下文。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package operationauthority

import "contractaudit/internal/access"

// ClaimRequest carries only the durable claim tuple; authority fields are Core output.
type ClaimRequest struct {
	TenantID   string `json:"tenant_id"`
	OrgID      string `json:"org_id"`
	JobID      string `json:"job_id"`
	WorkerID   string `json:"worker_id"`
	Generation int64  `json:"generation"`
	CaseID     string `json:"case_id"`
	RunID      string `json:"run_id"`
	AttemptID  string `json:"attempt_id"`
}

type ClaimProjection struct {
	JobID           string         `json:"job_id"`
	CaseID          string         `json:"case_id"`
	RunID           string         `json:"run_id"`
	AttemptID       string         `json:"attempt_id"`
	Audience        string         `json:"audience"`
	UserOrgID       string         `json:"user_org_id"`
	MembershipBound bool           `json:"membership_bound"`
	ActorID         string         `json:"actor_id"`
	DisplayName     string         `json:"display_name"`
	Role            string         `json:"role"`
	TenantID        string         `json:"tenant_id"`
	OrgID           string         `json:"org_id"`
	Enabled         bool           `json:"enabled"`
	Issuer          string         `json:"issuer,omitempty"`
	Subject         string         `json:"subject,omitempty"`
	PrincipalID     string         `json:"principal_id,omitempty"`
	MembershipID    string         `json:"membership_id,omitempty"`
	Grants          []access.Grant `json:"grants"`
}
