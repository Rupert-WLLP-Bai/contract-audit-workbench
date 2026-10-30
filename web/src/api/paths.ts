/** Assumed HTTP paths. See web/API.md. Identifiers are encoded so they stay one path segment. */

function id(value: string): string {
  return encodeURIComponent(value);
}

export const paths = {
  session: "/api/session",
  adminSession: "/api/session?audience=admin",
  sessionFor: (audience: "client" | "admin") =>
    audience === "admin" ? "/api/session?audience=admin" : "/api/session",
  oidcLogin: (audience: "client" | "admin") =>
    `/api/auth/oidc/login?audience=${id(audience)}`,
  cases: "/api/cases",
  case: (caseId: string) => `/api/cases/${id(caseId)}`,
  templates: "/api/templates",
  workbench: (period: "day" | "week" | "month") =>
    `/api/workbench?period=${period}`,
  caseProfile: (caseId: string) => `/api/cases/${id(caseId)}/profile`,
  caseTasks: (caseId: string) => `/api/cases/${id(caseId)}/tasks`,
  caseTask: (caseId: string, taskId: string) =>
    `/api/cases/${id(caseId)}/tasks/${id(taskId)}`,
  caseRelations: (caseId: string) => `/api/cases/${id(caseId)}/relations`,
  caseRelation: (caseId: string, relationId: string) =>
    `/api/cases/${id(caseId)}/relations/${id(relationId)}`,
  knowledge: "/api/knowledge",
  knowledgeVersion: (knowledgeId: string, versionId: string) =>
    `/api/knowledge/${id(knowledgeId)}/versions/${id(versionId)}`,
  generations: (caseId: string) => `/api/cases/${id(caseId)}/generations`,
  remediations: (caseId: string) => `/api/cases/${id(caseId)}/remediations`,
  remediationSubmit: (caseId: string, itemId: string) =>
    `/api/cases/${id(caseId)}/remediations/${id(itemId)}/submit`,
  remediationVerify: (caseId: string, itemId: string) =>
    `/api/cases/${id(caseId)}/remediations/${id(itemId)}/verify`,
  reports: (caseId: string) => `/api/cases/${id(caseId)}/reports`,
  report: (caseId: string, reportId: string, format: "json" | "html") =>
    `/api/cases/${id(caseId)}/reports/${id(reportId)}?format=${format}`,
  archive: (caseId: string) => `/api/cases/${id(caseId)}/archive`,
  documents: (caseId: string) => `/api/cases/${id(caseId)}/documents`,
  documentOriginal: (caseId: string, documentId: string) =>
    `/api/cases/${id(caseId)}/documents/${id(documentId)}/original`,
  reparse: (caseId: string, documentId: string) =>
    `/api/cases/${id(caseId)}/documents/${id(documentId)}/parse`,
  reviews: (caseId: string) => `/api/cases/${id(caseId)}/reviews`,
  review: (caseId: string, runId: string, attemptId?: string | null) => {
    const path = `/api/cases/${id(caseId)}/reviews/${id(runId)}`;
    return attemptId === null || attemptId === undefined
      ? path
      : `${path}?attempt_id=${id(attemptId)}`;
  },
  reviewDiagnostics: (caseId: string, runId: string, attemptId?: string | null) => {
    const path = `/api/cases/${id(caseId)}/reviews/${id(runId)}/diagnostics`;
    return attemptId === null || attemptId === undefined
      ? path
      : `${path}?attempt_id=${id(attemptId)}`;
  },
  retryReview: (caseId: string, runId: string) =>
    `/api/cases/${id(caseId)}/reviews/${id(runId)}/retry`,
  cancelReview: (caseId: string, runId: string) =>
    `/api/cases/${id(caseId)}/reviews/${id(runId)}/cancel`,
  decision: (caseId: string, runId: string, findingId: string) =>
    `/api/cases/${id(caseId)}/reviews/${id(runId)}/findings/${id(findingId)}/decisions`,
  reviewEvents: (caseId: string, runId: string) =>
    `/api/cases/${id(caseId)}/reviews/${id(runId)}/events`,
  settings: "/api/settings",
  adminSettings: "/api/admin/settings",
  llmEndpoint: "/api/admin/llm-endpoint",
  llmEndpointProbe: "/api/admin/llm-endpoint/probe",
  llmEndpointModels: "/api/admin/llm-endpoint/models",
  rulePacks: "/api/admin/rulepacks",
  rulePack: (packId: string, version: string) =>
    `/api/admin/rulepacks/${id(packId)}/versions/${id(version)}`,
  rulePackHistory: (packId: string) =>
    `/api/admin/rulepacks/${id(packId)}/history`,
  rulePackAction: (
    packId: string,
    version: string,
    action: "validate" | "approve" | "publish" | "disable" | "test",
  ) => `/api/admin/rulepacks/${id(packId)}/versions/${id(version)}/${action}`,
  rulePackRollback: (packId: string) =>
    `/api/admin/rulepacks/${id(packId)}/rollback`,
} as const;
