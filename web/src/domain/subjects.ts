// File metadata (manifest mirror):
// Purpose: Maps subject roles, binding states and verification outcomes to user-facing labels while leaving unknown server values explicit.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-10-31
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import type { SubjectBindingState, SubjectRole, SubjectVerificationScope, SubjectVerificationState } from "../api/types";

export function subjectRoleLabel(role: SubjectRole): string {
  switch (role) {
    case "customer": return "客户";
    case "supplier": return "供应商";
    case "contractor": return "承包方";
    case "service_provider": return "服务提供方";
    case "other": return "其他角色";
  }
}

export function subjectBindingStateLabel(state: SubjectBindingState): string {
  switch (state) {
    case "active": return "在办绑定";
    case "inactive": return "已停用";
    case "pending_confirmation": return "待确认绑定";
  }
}

export function subjectVerificationLabel(
  state: SubjectVerificationState | "inactive",
  scope: SubjectVerificationScope | undefined,
): string {
  if (state === "inactive") return "已停用";
  if (state === "pending_verification") return "待核验";
  if (scope === "synthetic_controlled") return "已对照核验（受控来源，合成演示）";
  if (scope === "official_controlled") return "已对照核验（受控来源）";
  return "已核验";
}
