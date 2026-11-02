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
  LLMEndpointInput,
  LLMModelSelectionInput,
  UploadDocumentInput,
  BindExistingSubjectInput,
  CaseRoleCreateInput,
  SubjectCreateInput,
  SubjectChangeDecisionInput,
  SubjectChangeProposalInput,
  SubjectChangeSummary,
  SubjectSourceSnapshotInput,
  SubjectSourceVerificationInput,
  MaterialParsedTextPage,
  MaterialParsedDocumentList,
  SubjectSourceSnapshotList,
  SubjectSourceSnapshotDetail,
  SubjectSourceDirectory,
} from "./types";
import type { WireSchemas } from "./wire.generated";
import { ApiError, jsonInit, requestJson, type CommandContext } from "./http";
import { newIdempotencyKey } from "./idempotency";
import {
  parseAcceptedReview,
  parseActor,
  parseArchiveRecord,
  parseCaseDetail,
  parseCaseList,
  parseEmpty,
  parseGenerationRecord,
  parseId,
  parseLLMEndpoint,
  parseLLMProbe,
  parseRemediation,
  parseReportRecord,
  parseReviewDiagnostics,
  parseReviewWorkspace,
  parseSettings,
  parseTemplateList,
  parseMaterialSelectionProjection,
  parseUploadDocumentResult,
  parseContextList,
} from "./parse";
import {
  parseCaseProfile,
  parseCaseRelation,
  parseKnowledge,
  parseWorkbenchSnapshot,
  parseWorkbenchTask,
} from "./workbench";
import { paths } from "./paths";
import {
  parseBindExistingSubjectResult,
  parseCaseRoleCreateResult,
  parseCaseSubjects,
  parseSubjectChangeDecisionResult,
  parseSubjectChangeDetail,
  parseSubjectChangeImpact,
  parseSubjectChangeList,
  parseSubjectChangeProposalResult,
  parseSubjectCreateResult,
  parseSubjectCaseLinks,
  parseSubjectDetail,
  parseSubjectHistory,
  parseSubjectList,
  parseSubjectSourceSnapshot,
  parseSubjectSourceSnapshotList,
  parseSubjectSourceSnapshotDetail,
  parseSubjectSourceDirectory,
  parseSubjectSourceVerificationResult,
  parseMaterialParsedTextPage,
  parseMaterialParsedDocumentList,
} from "./subjects-parse";

export function getSession(
  audience: SessionAudience = "client",
  signal?: AbortSignal,
) {
  return requestJson(
    paths.sessionFor(audience),
    { method: "GET", signal },
    parseActor,
  );
}

export function login(
  username: string,
  password: string,
  audience: SessionAudience = "client",
) {
  const body =
    audience === "admin"
      ? { username, password, audience }
      : { username, password };
  return requestJson(paths.session, jsonInit("POST", body), parseActor);
}

export function logout(audience: SessionAudience = "client") {
  return requestJson(
    paths.sessionFor(audience),
    { method: "DELETE" },
    parseEmpty,
  );
}

export function getContextChoices(audience: SessionAudience, signal?: AbortSignal) {
  return requestJson(paths.contexts(audience), { method: "GET", signal }, parseContextList);
}

export function switchContext(
  audience: SessionAudience,
  membershipId: string,
  context: CommandContext,
) {
  return requestJson(
    paths.switchContext(audience),
    {
      ...jsonInit("POST", {
        membership_id: membershipId,
        expected_context_version: context.contextVersion,
      }),
      commandContext: context,
    },
    parseActor,
  );
}

export function listCases(signal?: AbortSignal) {
  return requestJson(paths.cases, { method: "GET", signal }, parseCaseList);
}

export function createCase(input: {
  title: string;
  contract_type: string;
  business_line?: string;
  scenario?: string;
}, idempotencyKey?: string) {
  return requestJson(paths.cases, jsonInit("POST", input, idempotencyKey ? { "Idempotency-Key": idempotencyKey } : undefined), parseId);
}

export function getCase(caseId: string, signal?: AbortSignal) {
  return requestJson(
    paths.case(caseId),
    { method: "GET", signal },
    parseCaseDetail,
  );
}

export function listTemplates(signal?: AbortSignal) {
  return requestJson(
    paths.templates,
    { method: "GET", signal },
    parseTemplateList,
  );
}

export function createGeneration(
  caseId: string,
  input: GenerationInput,
  idempotencyKey: string,
) {
  return requestJson(
    paths.generations(caseId),
    jsonInit("POST", input, { "Idempotency-Key": idempotencyKey }),
    parseGenerationRecord,
  );
}

export function createRemediation(
  caseId: string,
  input: RemediationCreateInput,
) {
  return requestJson(
    paths.remediations(caseId),
    jsonInit("POST", input),
    parseRemediation,
  );
}

export function submitRemediation(
  caseId: string,
  itemId: string,
  input: RemediationSubmitInput,
) {
  return requestJson(
    paths.remediationSubmit(caseId, itemId),
    jsonInit("POST", input),
    parseRemediation,
  );
}

export function verifyRemediation(
  caseId: string,
  itemId: string,
  input: RemediationVerifyInput,
) {
  return requestJson(
    paths.remediationVerify(caseId, itemId),
    jsonInit("POST", input),
    parseRemediation,
  );
}

export function createReport(caseId: string, input: ReportCreateInput, idempotencyKey?: string) {
  return requestJson(
    paths.reports(caseId),
    jsonInit("POST", input, idempotencyKey ? { "Idempotency-Key": idempotencyKey } : undefined),
    parseReportRecord,
  );
}

export function archiveCase(caseId: string, input: ArchiveInput) {
  return requestJson(
    paths.archive(caseId),
    jsonInit("POST", input),
    parseArchiveRecord,
  );
}

export function uploadDocument(caseId: string, input: UploadDocumentInput) {
  const body = new FormData();
  body.set("file", input.file);
  body.set("role", input.role);
  body.set("intent", input.intent);
  if (input.purpose !== undefined) body.set("purpose", input.purpose);
  if (input.intent === "replace") {
    if (!input.material_id || !input.replaces_document_id)
      throw new Error("替换材料必须提供材料 ID 和当前文档 ID");
    body.set("material_id", input.material_id);
    body.set("replaces_document_id", input.replaces_document_id);
  }
  return requestJson(
    paths.documents(caseId),
    { method: "POST", body, headers: { "Idempotency-Key": input.idempotency_key } },
    parseUploadDocumentResult,
  );
}

export function confirmLegacyMaterialSelection(
  caseId: string,
  input: { version: number; document_ids: string[] },
) {
  return requestJson(
    paths.materialSelection(caseId),
    jsonInit(
      "POST",
      { version: input.version, document_ids: input.document_ids },
    ),
    parseMaterialSelectionProjection,
  );
}

export function listSubjects(
  caseId?: string,
  query?: string,
  cursor?: string | null,
  limit = 50,
  signal?: AbortSignal,
) {
  return requestJson(
    paths.subjects(caseId, query, cursor, limit),
    { method: "GET", signal },
    parseSubjectList,
  );
}

export function getSubject(subjectId: string, signal?: AbortSignal) {
  return requestJson(paths.subject(subjectId), { method: "GET", signal }, parseSubjectDetail);
}

export function getSubjectCaseLinks(subjectId: string, cursor: string | null = null, limit = 50, signal?: AbortSignal) {
  return requestJson(paths.subjectCaseLinks(subjectId, cursor, limit), { method: "GET", signal }, parseSubjectCaseLinks);
}

export function getSubjectHistory(subjectId: string, signal?: AbortSignal) {
  return requestJson(paths.subjectHistory(subjectId), { method: "GET", signal }, parseSubjectHistory);
}

export function getCaseSubjects(caseId: string, signal?: AbortSignal) {
  return requestJson(paths.caseSubjects(caseId), { method: "GET", signal }, parseCaseSubjects);
}

export function createSubject(input: SubjectCreateInput, idempotencyKey: string) {
  return requestJson(
    paths.subjects(),
    jsonInit("POST", input, { "Idempotency-Key": idempotencyKey }),
    parseSubjectCreateResult,
  );
}

export function createCaseRole(
  caseId: string,
  input: CaseRoleCreateInput,
  idempotencyKey: string,
) {
  return requestJson(
    paths.caseSubjects(caseId),
    jsonInit("POST", input, { "Idempotency-Key": idempotencyKey }),
    parseCaseRoleCreateResult,
  );
}

export function bindExistingSubject(
  caseId: string,
  input: BindExistingSubjectInput,
  idempotencyKey: string,
) {
  return requestJson(
    paths.caseSubjectsBindExisting(caseId),
    jsonInit("POST", input, { "Idempotency-Key": idempotencyKey }),
    parseBindExistingSubjectResult,
  );
}

export function listCaseSubjectChanges(
  caseId: string,
  cursor?: string | null,
  limit = 50,
  signal?: AbortSignal,
) {
  return requestJson(
    paths.caseSubjectChanges(caseId, cursor, limit),
    { method: "GET", signal },
    parseSubjectChangeList,
  );
}

export function getCaseSubjectChange(caseId: string, requestId: string, signal?: AbortSignal) {
  return requestJson(
    paths.caseSubjectChange(caseId, requestId),
    { method: "GET", signal },
    parseSubjectChangeDetail,
  );
}

export function getCaseSubjectChangeImpact(
  caseId: string,
  requestId: string,
  targetKind: SubjectChangeSummary["target_kind"],
  signal?: AbortSignal,
) {
  return requestJson(
    paths.caseSubjectChangeImpact(caseId, requestId),
    { method: "GET", signal },
    (value) => parseSubjectChangeImpact(value, targetKind),
  );
}

export function proposeSubjectChange(
  caseId: string,
  input: SubjectChangeProposalInput,
  idempotencyKey: string,
) {
  return requestJson(
    paths.caseSubjectChanges(caseId),
    jsonInit("POST", input, { "Idempotency-Key": idempotencyKey }),
    parseSubjectChangeProposalResult,
  );
}

export function decideSubjectChange(
  caseId: string,
  requestId: string,
  input: SubjectChangeDecisionInput,
  idempotencyKey: string,
) {
  return requestJson(
    paths.caseSubjectChangeDecisions(caseId, requestId),
    jsonInit("POST", input, { "Idempotency-Key": idempotencyKey }),
    parseSubjectChangeDecisionResult,
  );
}

export function createSubjectSourceSnapshot(
  caseId: string,
  input: SubjectSourceSnapshotInput,
  idempotencyKey: string,
) {
  return requestJson(
    paths.caseSubjectSourceSnapshots(caseId),
    jsonInit("POST", input, { "Idempotency-Key": idempotencyKey }),
    parseSubjectSourceSnapshot,
  );
}

export function listSubjectSourceSnapshots(caseId: string, cursor: string | null = null, limit = 50, signal?: AbortSignal): Promise<SubjectSourceSnapshotList> {
  return requestJson(paths.caseSubjectSourceSnapshots(caseId, cursor, limit), { method: "GET", signal }, parseSubjectSourceSnapshotList);
}

export function getSubjectSourceDirectory(caseId: string, signal?: AbortSignal): Promise<SubjectSourceDirectory> {
  return requestJson(paths.caseSubjectSources(caseId), { method: "GET", signal }, parseSubjectSourceDirectory);
}

export function getSubjectSourceSnapshot(caseId: string, snapshotId: string, signal?: AbortSignal): Promise<SubjectSourceSnapshotDetail> {
  return requestJson(paths.caseSubjectSourceSnapshot(caseId, snapshotId), { method: "GET", signal }, parseSubjectSourceSnapshotDetail);
}

export function getMaterialParsedTextPage(
  caseId: string,
  materialId: string,
  materialVersion: number,
  parsedDocumentId: string,
  offset = 0,
  limit = 32768,
  signal?: AbortSignal,
): Promise<MaterialParsedTextPage> {
  return requestJson(
    paths.materialParsedText(caseId, materialId, materialVersion, parsedDocumentId, offset, limit),
    { method: "GET", signal },
    parseMaterialParsedTextPage,
  );
}

export function listMaterialParsedDocuments(
  caseId: string,
  materialId: string,
  materialVersion: number,
  signal?: AbortSignal,
): Promise<MaterialParsedDocumentList> {
  return requestJson(
    paths.materialParsedDocuments(caseId, materialId, materialVersion),
    { method: "GET", signal },
    parseMaterialParsedDocumentList,
  );
}

export function verifySubjectAgainstSourceSnapshot(
  caseId: string,
  snapshotId: string,
  input: SubjectSourceVerificationInput,
  idempotencyKey: string,
) {
  return requestJson(
    paths.caseSubjectSourceSnapshotVerify(caseId, snapshotId),
    jsonInit("POST", input, { "Idempotency-Key": idempotencyKey }),
    parseSubjectSourceVerificationResult,
  );
}

export function reparseDocument(caseId: string, documentId: string) {
  return requestJson(
    paths.reparse(caseId, documentId),
    jsonInit("POST", {}),
    parseEmpty,
  );
}

export function startReview(caseId: string, input: { business_at?: string; scope?: "full_review" | "single_contract_precheck" } = {}, idempotencyKey?: string) {
  return requestJson(
    paths.reviews(caseId),
    jsonInit("POST", input, idempotencyKey ? { "Idempotency-Key": idempotencyKey } : undefined),
    parseAcceptedReview,
  );
}

export function getReview(
  caseId: string,
  runId: string,
  attemptId?: string | null,
  signal?: AbortSignal,
) {
  return requestJson(
    paths.review(caseId, runId, attemptId),
    { method: "GET", signal },
    parseReviewWorkspace,
  );
}

export async function getReviewDiagnostics(
  caseId: string,
  runId: string,
  attemptId?: string | null,
  signal?: AbortSignal,
) {
  const diagnostics = await requestJson(
    paths.reviewDiagnostics(caseId, runId, attemptId),
    { method: "GET", signal },
    parseReviewDiagnostics,
  );
  if (diagnostics.case_id !== caseId || diagnostics.run_id !== runId ||
      (attemptId && diagnostics.execution && diagnostics.execution.attempt.id !== attemptId)) {
    throw new ApiError("审查诊断身份不匹配", 500, "invalid_response", false, null);
  }
  return diagnostics;
}

export async function getReviewHistory(
  caseId: string,
  latestRunId: string,
  signal?: AbortSignal,
): Promise<ReviewHistoryEntry[]> {
  const entries: ReviewHistoryEntry[] = [];
  const seen = new Set<string>();
  let runId: string | null = latestRunId;
  while (runId !== null) {
    if (seen.has(runId))
      throw new ApiError(
        "运行历史包含循环引用",
        500,
        "invalid_response",
        false,
        null,
      );
    seen.add(runId);
    const workspace = await getReview(caseId, runId, null, signal);
    entries.push({ runId, workspace });
    runId = workspace.previous_run_id ?? null;
  }
  return entries;
}

export function retryReview(caseId: string, runId: string) {
  return requestJson(
    paths.retryReview(caseId, runId),
    jsonInit("POST", {}),
    parseAcceptedReview,
  );
}

export function cancelReview(caseId: string, runId: string) {
  return requestJson(
    paths.cancelReview(caseId, runId),
    jsonInit("POST", {}),
    parseEmpty,
  );
}

export function submitDecision(
  caseId: string,
  runId: string,
  findingId: string,
  input: DecisionInput,
) {
  return requestJson(
    paths.decision(caseId, runId, findingId),
    jsonInit(
      "POST",
      {
        action: input.action,
        reason: input.reason,
        expected_version: input.expectedVersion,
      } satisfies WireSchemas["DecisionRequest"],
      { "Idempotency-Key": newIdempotencyKey() },
    ),
    parseEmpty,
  );
}

export function getSettings(
  audience: SessionAudience = "client",
  signal?: AbortSignal,
) {
  const path = audience === "admin" ? paths.adminSettings : paths.settings;
  return requestJson(path, { method: "GET", signal }, parseSettings);
}

export function getLLMEndpoint(signal?: AbortSignal) {
  return requestJson(
    paths.llmEndpoint,
    { method: "GET", signal },
    parseLLMEndpoint,
  );
}

export function probeLLMEndpoint(input: {
  protocol: string;
  base_url: string;
  api_key?: string;
  clear_api_key?: boolean;
}) {
  return requestJson(
    paths.llmEndpointProbe,
    jsonInit("POST", input),
    parseLLMProbe,
  );
}

export function saveLLMEndpoint(input: LLMEndpointInput) {
  return requestJson(
    paths.llmEndpoint,
    jsonInit("PUT", input),
    parseLLMEndpoint,
  );
}

export function saveLLMModels(input: LLMModelSelectionInput) {
  return requestJson(
    paths.llmEndpointModels,
    jsonInit("PUT", input),
    parseLLMEndpoint,
  );
}
export function getWorkbench(period: WorkbenchPeriod, signal?: AbortSignal) {
  return requestJson(
    paths.workbench(period),
    { method: "GET", signal },
    (value) => parseWorkbenchSnapshot(value, period),
  );
}

export function updateCaseProfile(caseId: string, input: CaseProfileInput) {
  return requestJson(
    paths.caseProfile(caseId),
    jsonInit("PUT", input),
    parseCaseProfile,
  );
}

export function createWorkbenchTask(caseId: string, input: WorkbenchTaskInput) {
  return requestJson(
    paths.caseTasks(caseId),
    jsonInit("POST", input),
    parseWorkbenchTask,
  );
}

export function updateWorkbenchTask(
  caseId: string,
  taskId: string,
  input: WorkbenchTaskUpdateInput,
) {
  return requestJson(
    paths.caseTask(caseId, taskId),
    jsonInit("PUT", input),
    parseWorkbenchTask,
  );
}

export function createCaseRelation(caseId: string, input: CaseRelationInput) {
  return requestJson(
    paths.caseRelations(caseId),
    jsonInit("POST", input),
    parseCaseRelation,
  );
}

export function deleteCaseRelation(caseId: string, relationId: string) {
  return requestJson(
    paths.caseRelation(caseId, relationId),
    jsonInit("DELETE"),
    parseEmpty,
  );
}

export function createKnowledge(input: KnowledgeInput) {
  return requestJson(paths.knowledge, jsonInit("POST", input), parseKnowledge);
}

export function getKnowledge(
  knowledgeId: string,
  versionId: string,
  signal?: AbortSignal,
) {
  return requestJson(
    paths.knowledgeVersion(knowledgeId, versionId),
    { method: "GET", signal },
    (value) => {
      const knowledge = parseKnowledge(value);
      if (knowledge.id !== knowledgeId || knowledge.version_id !== versionId) {
        throw new ApiError("知识版本与请求不一致", 500, "invalid_response", false, null);
      }
      return knowledge;
    },
  );
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
