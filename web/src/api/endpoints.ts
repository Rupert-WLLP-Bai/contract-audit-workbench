import type {
  ArchiveInput,
  DecisionInput,
  GenerationInput,
  RemediationCreateInput,
  RemediationSubmitInput,
  RemediationVerifyInput,
  ReportCreateInput,
  ReviewHistoryEntry,
  SessionAudience,
  CaseProfileInput,
  WorkbenchPeriod,
  WorkbenchTaskInput,
  WorkbenchTaskUpdateInput,
  CaseRelationInput,
  KnowledgeInput,
} from "./types";
import { ApiError, jsonInit, requestJson } from "./http";
import {
  parseAcceptedReview,
  parseActor,
  parseArchiveRecord,
  parseCaseDetail,
  parseCaseList,
  parseEmpty,
  parseGenerationRecord,
  parseId,
  parseRemediation,
  parseReportRecord,
  parseReviewWorkspace,
  parseSettings,
  parseTemplateList,
} from "./parse";
import { parseCaseProfile, parseCaseRelation, parseKnowledge, parseWorkbenchSnapshot, parseWorkbenchTask } from "./workbench";
import { paths } from "./paths";

export function getSession(audience: SessionAudience = "client", signal?: AbortSignal) {
  return requestJson(paths.sessionFor(audience), { method: "GET", signal }, parseActor);
}

export function login(username: string, password: string, audience: SessionAudience = "client") {
  const body = audience === "admin" ? { username, password, audience } : { username, password };
  return requestJson(paths.session, jsonInit("POST", body), parseActor);
}

export function logout(audience: SessionAudience = "client") {
  return requestJson(paths.sessionFor(audience), { method: "DELETE" }, parseEmpty);
}

export function listCases(signal?: AbortSignal) {
  return requestJson(paths.cases, { method: "GET", signal }, parseCaseList);
}

export function createCase(input: { title: string; contract_type: string; business_line?: string; scenario?: string }) {
  return requestJson(paths.cases, jsonInit("POST", input), parseId);
}

export function getCase(caseId: string, signal?: AbortSignal) {
  return requestJson(paths.case(caseId), { method: "GET", signal }, parseCaseDetail);
}

export function listTemplates(signal?: AbortSignal) {
  return requestJson(paths.templates, { method: "GET", signal }, parseTemplateList);
}

export function createGeneration(caseId: string, input: GenerationInput) {
  return requestJson(paths.generations(caseId), jsonInit("POST", input), parseGenerationRecord);
}

export function createRemediation(caseId: string, input: RemediationCreateInput) {
  return requestJson(paths.remediations(caseId), jsonInit("POST", input), parseRemediation);
}

export function submitRemediation(caseId: string, itemId: string, input: RemediationSubmitInput) {
  return requestJson(paths.remediationSubmit(caseId, itemId), jsonInit("POST", input), parseRemediation);
}

export function verifyRemediation(caseId: string, itemId: string, input: RemediationVerifyInput) {
  return requestJson(paths.remediationVerify(caseId, itemId), jsonInit("POST", input), parseRemediation);
}

export function createReport(caseId: string, input: ReportCreateInput) {
  return requestJson(paths.reports(caseId), jsonInit("POST", input), parseReportRecord);
}

export function archiveCase(caseId: string, input: ArchiveInput) {
  return requestJson(paths.archive(caseId), jsonInit("POST", input), parseArchiveRecord);
}

export function uploadDocument(caseId: string, file: File, role: string) {
  const body = new FormData();
  body.set("file", file);
  body.set("role", role);
  return requestJson(paths.documents(caseId), { method: "POST", body }, parseEmpty);
}

export function reparseDocument(caseId: string, documentId: string) {
  return requestJson(paths.reparse(caseId, documentId), jsonInit("POST", {}), parseEmpty);
}

export function startReview(caseId: string) {
  return requestJson(paths.reviews(caseId), jsonInit("POST", {}), parseAcceptedReview);
}

export function getReview(caseId: string, runId: string, attemptId?: string | null, signal?: AbortSignal) {
  return requestJson(paths.review(caseId, runId, attemptId), { method: "GET", signal }, parseReviewWorkspace);
}

export async function getReviewHistory(caseId: string, latestRunId: string, signal?: AbortSignal): Promise<ReviewHistoryEntry[]> {
  const entries: ReviewHistoryEntry[] = [];
  const seen = new Set<string>();
  let runId: string | null = latestRunId;
  while (runId !== null) {
    if (seen.has(runId)) throw new ApiError("运行历史包含循环引用", 500, "invalid_response", false, null);
    seen.add(runId);
    const workspace = await getReview(caseId, runId, null, signal);
    entries.push({ runId, workspace });
    runId = workspace.previous_run_id ?? null;
  }
  return entries;
}

export function retryReview(caseId: string, runId: string) {
  return requestJson(paths.retryReview(caseId, runId), jsonInit("POST", {}), parseAcceptedReview);
}

export function cancelReview(caseId: string, runId: string) {
  return requestJson(paths.cancelReview(caseId, runId), jsonInit("POST", {}), parseEmpty);
}

export function submitDecision(caseId: string, runId: string, findingId: string, input: DecisionInput) {
  return requestJson(
    paths.decision(caseId, runId, findingId),
    jsonInit(
      "POST",
      { action: input.action, reason: input.reason, expected_version: input.expectedVersion },
      { "Idempotency-Key": crypto.randomUUID() },
    ),
    parseEmpty,
  );
}

export function getSettings(audience: SessionAudience = "client", signal?: AbortSignal) {
  const path = audience === "admin" ? paths.adminSettings : paths.settings;
  return requestJson(path, { method: "GET", signal }, parseSettings);
}
export function getWorkbench(period: WorkbenchPeriod, signal?: AbortSignal) {
  return requestJson(paths.workbench(period), { method: "GET", signal }, (value) => parseWorkbenchSnapshot(value, period));
}

export function updateCaseProfile(caseId: string, input: CaseProfileInput) {
  return requestJson(paths.caseProfile(caseId), jsonInit("PUT", input), parseCaseProfile);
}

export function createWorkbenchTask(caseId: string, input: WorkbenchTaskInput) {
  return requestJson(paths.caseTasks(caseId), jsonInit("POST", input), parseWorkbenchTask);
}

export function updateWorkbenchTask(caseId: string, taskId: string, input: WorkbenchTaskUpdateInput) {
  return requestJson(paths.caseTask(caseId, taskId), jsonInit("PUT", input), parseWorkbenchTask);
}

export function createCaseRelation(caseId: string, input: CaseRelationInput) {
  return requestJson(paths.caseRelations(caseId), jsonInit("POST", input), parseCaseRelation);
}

export function deleteCaseRelation(caseId: string, relationId: string) {
  return requestJson(paths.caseRelation(caseId, relationId), jsonInit("DELETE"), parseEmpty);
}

export function createKnowledge(input: KnowledgeInput) {
  return requestJson(paths.knowledge, jsonInit("POST", input), parseKnowledge);
}

export function getKnowledge(knowledgeId: string, signal?: AbortSignal) {
  return requestJson(paths.knowledgeEntry(knowledgeId), { method: "GET", signal }, parseKnowledge);
}

export {
  approveRulePack,
  createRulePack,
  disableRulePack,
  editRulePack,
  getRulePack,
  getRulePackHistory,
  listRulePacks,
  publishRulePack,
  rollbackRulePack,
  testRulePack,
  validateRulePack,
} from "./rulepacks";
