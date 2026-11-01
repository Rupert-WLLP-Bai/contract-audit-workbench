import { useRef } from "react";
import {
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from "@tanstack/react-query";
import * as api from "./endpoints";
import { isForbidden, isUnauthenticated, setSessionIdentity } from "./http";
import { clearPendingMutationKey, idempotencyActorScope, newIdempotencyKey, pendingMutationKey } from "./idempotency";
import type {
  Actor,
  CaseDetail,
  ArchiveInput,
  DecisionInput,
  GenerationInput,
  RemediationCreateInput,
  RemediationSubmitInput,
  RemediationVerifyInput,
  ReportCreateInput,
  SessionAudience,
  WorkbenchPeriod,
  CaseProfileInput,
  WorkbenchTaskInput,
  WorkbenchTaskUpdateInput,
  CaseRelationInput,
  KnowledgeInput,
  LLMEndpointInput,
  LLMModelSelectionInput,
  ReviewWorkspace,
  UploadDocumentInput,
  BindExistingSubjectInput,
  CaseRoleCreateInput,
  SubjectChangeDecisionInput,
  SubjectChangeProposalInput,
  SubjectChangeSummary,
  SubjectChangeDetail,
  SubjectChangeImpact,
  SubjectCreateInput,
  SubjectSourceSnapshotInput,
  SubjectSourceVerificationInput,
} from "./types";
import { isActiveAttemptState } from "../domain/review";
import type { RulePackManifestInput, RulePackTestInput } from "./rulepacks";

export const queryKeys = {
  session: ["session", "client"] as const,
  sessionFor: (audience: SessionAudience) =>
    audience === "admin" ? (["session", "admin"] as const) : queryKeys.session,
  pendingIntakesPrefix: ["intakes", "pending"] as const,
  contractDraftsPrefix: ["contracts", "drafts"] as const,
  contractDrafts: (actorScope: string) =>
    ["contracts", "drafts", actorScope] as const,
  pendingIntakes: (actorScope: string, cursor: string | null) =>
    ["intakes", "pending", actorScope, cursor] as const,
  cases: ["cases"] as const,
  case: (caseId: string) => ["cases", caseId] as const,
  reviewPrefix: (caseId: string, runId: string) =>
    ["cases", caseId, "reviews", runId] as const,
  review: (caseId: string, runId: string, attemptId?: string | null) =>
    [...queryKeys.reviewPrefix(caseId, runId), attemptId ?? null] as const,
  reviewDiagnostics: (caseId: string, runId: string, attemptId?: string | null) =>
    [...queryKeys.reviewPrefix(caseId, runId), "diagnostics", attemptId ?? null] as const,
  reviewHistory: (caseId: string, latestRunId: string) =>
    ["cases", caseId, "review-history", latestRunId] as const,
  templates: ["templates"] as const,
  settings: ["settings", "client"] as const,
  settingsFor: (audience: SessionAudience) =>
    audience === "admin"
      ? (["settings", "admin"] as const)
      : queryKeys.settings,
  llmEndpoint: ["admin", "llm-endpoint"] as const,
  rulePacks: ["admin", "rulepacks"] as const,
  rulePackList: ["admin", "rulepacks", "list"] as const,
  rulePack: (packId: string, version: string) =>
    ["admin", "rulepacks", "detail", packId, version] as const,
  rulePackHistory: (packId: string) =>
    ["admin", "rulepacks", "history", packId] as const,
  workbench: (period: WorkbenchPeriod) => ["workbench", period] as const,
  workbenchPrefix: ["workbench"] as const,
  subjectsPrefix: ["subjects"] as const,
  subjects: (query: string, cursor: string | null, caseId?: string) =>
    ["subjects", "list", caseId ?? null, query, cursor] as const,
  subject: (subjectId: string) => ["subjects", "detail", subjectId] as const,
  subjectHistory: (subjectId: string) => ["subjects", "history", subjectId] as const,
  subjectCaseLinks: (subjectId: string, cursor: string | null) => ["subjects", "case-links", subjectId, cursor] as const,
  caseSubjects: (caseId: string) => ["cases", caseId, "subjects"] as const,
  caseSubjectChanges: (caseId: string, cursor: string | null) =>
    ["cases", caseId, "subject-changes", cursor] as const,
  caseSubjectChange: (caseId: string, requestId: string) =>
    ["cases", caseId, "subject-changes", requestId] as const,
  caseSubjectChangeImpact: (caseId: string, requestId: string) =>
    ["cases", caseId, "subject-changes", requestId, "impact"] as const,
  caseSubjectSourceSnapshots: (caseId: string, cursor: string | null) => ["cases", caseId, "subject-source-snapshots", cursor] as const,
  caseSubjectSources: (caseId: string) => ["cases", caseId, "subject-sources"] as const,
  caseSubjectSourceSnapshot: (caseId: string, snapshotId: string) => ["cases", caseId, "subject-source-snapshots", snapshotId] as const,
  materialParsedDocuments: (caseId: string, materialId: string, version: number) => ["cases", caseId, "materials", materialId, "versions", version, "parsed-documents"] as const,
  materialParsedText: (caseId: string, materialId: string, version: number, parsedDocumentId: string, offset: number) => ["cases", caseId, "materials", materialId, "versions", version, "parsed-documents", parsedDocumentId, offset] as const,
};

async function keyedMutation<T>(client: QueryClient, operation: string, target: string, input: unknown, send: (key: string) => Promise<T>): Promise<T> {
  const actor = client.getQueryData<Actor>(queryKeys.session);
  if (!actor) throw new Error("登录状态不可用，请刷新后重试");
  const pending = await pendingMutationKey(operation, idempotencyActorScope(actor), target, input);
  const result = await send(pending.key);
  try {
    clearPendingMutationKey(pending.storageKey);
  } catch {
    try { sessionStorage.setItem(pending.storageKey, newIdempotencyKey()); } catch { /* response already succeeded */ }
  }
  return result;
}
export function clearIdentityQueries(queryClient: QueryClient): void {
  const filter = {
    predicate: (query: { queryKey: readonly unknown[] }) =>
      query.queryKey[0] !== queryKeys.session[0],
  };
  void queryClient.cancelQueries(filter);
  queryClient.removeQueries(filter);
}

export function useSession(audience: SessionAudience = "client") {
  return useQuery({
    queryKey: queryKeys.sessionFor(audience),
    queryFn: ({ signal }) => api.getSession(audience, signal),
    staleTime: 60_000,
    retry: false,
  });
}

export function useLogin(audience: SessionAudience = "client") {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { username: string; password: string }) =>
      api.login(input.username, input.password, audience),
    onSuccess: (actor: Actor) => {
      if (setSessionIdentity(actor)) clearIdentityQueries(queryClient);
      queryClient.setQueryData(queryKeys.sessionFor(audience), actor);
    },
  });
}

export function useLogout(audience: SessionAudience = "client") {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.logout(audience),
    onSettled: () => {
      setSessionIdentity(null);
      clearIdentityQueries(queryClient);
      void queryClient.resetQueries({
        queryKey: queryKeys.sessionFor(audience),
      });
    },
  });
}

export function useCases() {
  return useQuery({
    queryKey: queryKeys.cases,
    queryFn: ({ signal }) => api.listCases(signal),
  });
}
export function useWorkbench(period: WorkbenchPeriod = "week") {
  return useQuery({
    queryKey: queryKeys.workbench(period),
    queryFn: ({ signal }) => api.getWorkbench(period, signal),
    staleTime: 30_000,
  });
}
function invalidateCaseViews(
  queryClient: QueryClient,
  caseId: string,
  options: { caseList?: boolean; workbench?: boolean } = {},
): Promise<void[]> {
  const invalidations: Promise<void>[] = [
    queryClient.invalidateQueries({
      queryKey: queryKeys.case(caseId),
      exact: true,
    }),
  ];
  if (options.caseList) {
    invalidations.push(
      queryClient.invalidateQueries({
        queryKey: queryKeys.cases,
        exact: true,
      }),
    );
  }
  if (options.workbench) {
    invalidations.push(
      queryClient.invalidateQueries({ queryKey: queryKeys.workbenchPrefix }),
    );
  }
  return Promise.all(invalidations);
}

function invalidateReviewHistory(
  queryClient: QueryClient,
  caseId: string,
  runId: string,
): Promise<void> {
  return queryClient.invalidateQueries({
    queryKey: queryKeys.reviewHistory(caseId, runId),
    exact: true,
  });
}

export async function invalidateReviewMutationViews(
  queryClient: QueryClient,
  caseId: string,
  runId: string,
): Promise<void> {
  await Promise.all([
    queryClient.invalidateQueries({
      queryKey: queryKeys.reviewPrefix(caseId, runId),
    }),
    invalidateReviewHistory(queryClient, caseId, runId),
    invalidateCaseViews(queryClient, caseId, {
      caseList: true,
      workbench: true,
    }),
  ]);
}

function invalidateSubjectChangeViews(
  queryClient: QueryClient,
  caseId: string,
  requestId: string,
  approved: boolean,
): Promise<void[]> {
  const detail = queryClient.getQueryData<SubjectChangeDetail>(
    queryKeys.caseSubjectChange(caseId, requestId),
  );
  const impact = queryClient.getQueryData<SubjectChangeImpact>(
    queryKeys.caseSubjectChangeImpact(caseId, requestId),
  );
  const invalidations: Promise<void>[] = [
    queryClient.invalidateQueries({
      queryKey: queryKeys.caseSubjectChange(caseId, requestId),
      exact: true,
    }),
    queryClient.invalidateQueries({
      queryKey: queryKeys.caseSubjectChangeImpact(caseId, requestId),
      exact: true,
    }),
    queryClient.invalidateQueries({
      queryKey: ["cases", caseId, "subject-changes"],
    }),
    queryClient.invalidateQueries({
      queryKey: queryKeys.case(caseId),
      exact: true,
    }),
    queryClient.invalidateQueries({
      queryKey: queryKeys.cases,
      exact: true,
    }),
  ];
  if (!approved) return Promise.all(invalidations);

  invalidations.push(
    queryClient.invalidateQueries({
      queryKey: queryKeys.caseSubjects(caseId),
      exact: true,
    }),
    queryClient.invalidateQueries({ queryKey: ["subjects", "list"] }),
  );
  const subjectId =
    detail?.summary.target_subject_id ??
    (impact
      ? "subject_id" in impact.after
        ? impact.after.subject_id
        : impact.after.subject.subject_id
      : undefined);
  if (subjectId) {
    invalidations.push(
      queryClient.invalidateQueries({
        queryKey: queryKeys.subject(subjectId),
        exact: true,
      }),
      queryClient.invalidateQueries({
        queryKey: queryKeys.subjectHistory(subjectId),
        exact: true,
      }),
      queryClient.invalidateQueries({
        queryKey: ["subjects", "case-links", subjectId],
      }),
    );
  }
  const affectedCaseIds = new Set([
    ...(impact?.authorized_impact_cases.map((item) => item.case_id) ?? []),
  ]);
  for (const affectedCaseId of affectedCaseIds) {
    if (affectedCaseId === caseId) continue;
    invalidations.push(
      queryClient.invalidateQueries({
        queryKey: queryKeys.case(affectedCaseId),
        exact: true,
      }),
      queryClient.invalidateQueries({
        queryKey: queryKeys.caseSubjects(affectedCaseId),
        exact: true,
      }),
    );
  }
  return Promise.all(invalidations);
}

export function useUpdateCaseProfile(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: CaseProfileInput) =>
      api.updateCaseProfile(caseId, input),
    onSuccess: () =>
      invalidateCaseViews(queryClient, caseId, {
        caseList: true,
        workbench: true,
      }),
  });
}

export function useCreateWorkbenchTask(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: WorkbenchTaskInput) =>
      api.createWorkbenchTask(caseId, input),
    onSuccess: () =>
      invalidateCaseViews(queryClient, caseId, { workbench: true }),
  });
}

export function useUpdateWorkbenchTask() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: {
      caseId: string;
      taskId: string;
      update: WorkbenchTaskUpdateInput;
    }) => api.updateWorkbenchTask(input.caseId, input.taskId, input.update),
    onSuccess: (_task, input) =>
      invalidateCaseViews(queryClient, input.caseId, { workbench: true }),
  });
}

export function useCreateCaseRelation(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: CaseRelationInput) =>
      api.createCaseRelation(caseId, input),
    onSuccess: () =>
      invalidateCaseViews(queryClient, caseId, {
        caseList: true,
        workbench: true,
      }),
  });
}

export function useDeleteCaseRelation(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (relationId: string) =>
      api.deleteCaseRelation(caseId, relationId),
    onSuccess: () =>
      invalidateCaseViews(queryClient, caseId, {
        caseList: true,
        workbench: true,
      }),
  });
}

export function useCreateKnowledge() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: KnowledgeInput) => api.createKnowledge(input),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: queryKeys.workbenchPrefix,
      });
    },
  });
}

export function useKnowledge(knowledgeId: string, versionId: string) {
  return useQuery({
    queryKey: ["knowledge", knowledgeId, versionId] as const,
    queryFn: ({ signal }) => api.getKnowledge(knowledgeId, versionId, signal),
    enabled: knowledgeId !== "" && versionId !== "",
  });
}

export function useCreateCase() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: Parameters<typeof api.createCase>[0]) =>
      keyedMutation(queryClient, "case.create", "new", input, (key) => api.createCase(input, key)),
    onSuccess: () =>
      Promise.all([
        queryClient.invalidateQueries({
          queryKey: queryKeys.cases,
          exact: true,
        }),
        queryClient.invalidateQueries({
          queryKey: queryKeys.workbenchPrefix,
        }),
      ]),
  });
}

export function useCaseDetail(caseId: string) {
  return useQuery({
    queryKey: queryKeys.case(caseId),
    queryFn: ({ signal }) => api.getCase(caseId, signal),
    enabled: caseId !== "",
  });
}

export function useSubjects(query: string, cursor: string | null, caseId?: string) {
  return useQuery({
    queryKey: queryKeys.subjects(query, cursor, caseId),
    queryFn: ({ signal }) => api.listSubjects(caseId, query, cursor, 50, signal),
    enabled: caseId === undefined || caseId !== "",
  });
}

export function useSubject(subjectId: string) {
  return useQuery({
    queryKey: queryKeys.subject(subjectId),
    queryFn: ({ signal }) => api.getSubject(subjectId, signal),
    enabled: subjectId !== "",
  });
}

export function useSubjectCaseLinks(subjectId: string, cursor: string | null = null) {
  return useQuery({
    queryKey: queryKeys.subjectCaseLinks(subjectId, cursor),
    queryFn: ({ signal }) => api.getSubjectCaseLinks(subjectId, cursor, 50, signal),
    enabled: subjectId !== "",
  });
}

export function useMaterialParsedDocuments(caseId: string, materialId: string | undefined, version: number | undefined) {
  return useQuery({
    queryKey: queryKeys.materialParsedDocuments(caseId, materialId ?? "", version ?? 0),
    queryFn: ({ signal }) => {
      if (!materialId || !version) throw new Error("缺少材料版本");
      return api.listMaterialParsedDocuments(caseId, materialId, version, signal);
    },
    enabled: caseId !== "" && materialId !== undefined && version !== undefined,
  });
}

export function useMaterialParsedTextPage(caseId: string, materialId: string | undefined, version: number | undefined, parsedDocumentId: string | undefined, offset: number) {
  return useQuery({
    queryKey: queryKeys.materialParsedText(caseId, materialId ?? "", version ?? 0, parsedDocumentId ?? "", offset),
    queryFn: ({ signal }) => {
      if (!materialId || !version || !parsedDocumentId) throw new Error("缺少固定解析版本");
      return api.getMaterialParsedTextPage(caseId, materialId, version, parsedDocumentId, offset, 32768, signal);
    },
    enabled: caseId !== "" && materialId !== undefined && version !== undefined && parsedDocumentId !== undefined,
  });
}

export function useSubjectHistory(subjectId: string) {
  return useQuery({
    queryKey: queryKeys.subjectHistory(subjectId),
    queryFn: ({ signal }) => api.getSubjectHistory(subjectId, signal),
    enabled: subjectId !== "",
  });
}

export function useCaseSubjects(caseId: string) {
  return useQuery({
    queryKey: queryKeys.caseSubjects(caseId),
    queryFn: ({ signal }) => api.getCaseSubjects(caseId, signal),
    enabled: caseId !== "",
  });
}

export function useCreateSubject() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { subject: SubjectCreateInput; idempotencyKey: string }) =>
      api.createSubject(input.subject, input.idempotencyKey),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: ["subjects", "list", null],
      }),
  });
}

export function useCreateCaseRole(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { value: CaseRoleCreateInput; idempotencyKey: string }) =>
      api.createCaseRole(caseId, input.value, input.idempotencyKey),
    onSuccess: async (result) => {
      const subjectId = result.subject_id;
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: queryKeys.caseSubjects(caseId),
          exact: true,
        }),
        queryClient.invalidateQueries({
          queryKey: queryKeys.case(caseId),
          exact: true,
        }),
        queryClient.invalidateQueries({
          queryKey: queryKeys.cases,
          exact: true,
        }),
        queryClient.invalidateQueries({
          queryKey: ["subjects", "list", null],
        }),
        queryClient.invalidateQueries({
          queryKey: ["subjects", "list", caseId],
        }),
        queryClient.invalidateQueries({
          queryKey: queryKeys.subject(subjectId),
          exact: true,
        }),
        queryClient.invalidateQueries({
          queryKey: queryKeys.subjectHistory(subjectId),
          exact: true,
        }),
        queryClient.invalidateQueries({
          queryKey: ["subjects", "case-links", subjectId],
        }),
      ]);
    },
  });
}

export function useBindExistingSubject(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { value: BindExistingSubjectInput; idempotencyKey: string }) =>
      api.bindExistingSubject(caseId, input.value, input.idempotencyKey),
    onSuccess: async (_result, input) => {
      const subjectId = input.value.subject_id;
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: queryKeys.caseSubjects(caseId),
          exact: true,
        }),
        queryClient.invalidateQueries({
          queryKey: queryKeys.case(caseId),
          exact: true,
        }),
        queryClient.invalidateQueries({
          queryKey: queryKeys.cases,
          exact: true,
        }),
        queryClient.invalidateQueries({
          queryKey: ["subjects", "list", caseId],
        }),
        queryClient.invalidateQueries({
          queryKey: ["subjects", "case-links", subjectId],
        }),
      ]);
    },
  });
}

export function useCaseSubjectChanges(caseId: string, cursor: string | null = null) {
  return useQuery({
    queryKey: queryKeys.caseSubjectChanges(caseId, cursor),
    queryFn: ({ signal }) => api.listCaseSubjectChanges(caseId, cursor, 50, signal),
    enabled: caseId !== "",
  });
}

export function useCaseSubjectChange(caseId: string, requestId: string) {
  return useQuery({
    queryKey: queryKeys.caseSubjectChange(caseId, requestId),
    queryFn: ({ signal }) => api.getCaseSubjectChange(caseId, requestId, signal),
    enabled: caseId !== "" && requestId !== "",
  });
}

export function useCaseSubjectChangeImpact(
  caseId: string,
  requestId: string,
  targetKind: SubjectChangeSummary["target_kind"] | null,
) {
  return useQuery({
    queryKey: queryKeys.caseSubjectChangeImpact(caseId, requestId),
    queryFn: ({ signal }) => {
      if (targetKind === null) throw new Error("缺少更正目标类型");
      return api.getCaseSubjectChangeImpact(caseId, requestId, targetKind, signal);
    },
    enabled: caseId !== "" && requestId !== "" && targetKind !== null,
  });
}

export function useProposeSubjectChange(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { value: SubjectChangeProposalInput; idempotencyKey: string }) =>
      api.proposeSubjectChange(caseId, input.value, input.idempotencyKey),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.caseSubjectChanges(caseId, null),
        exact: true,
      }),
  });
}

export function useDecideSubjectChange(caseId: string, requestId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { value: SubjectChangeDecisionInput; idempotencyKey: string }) =>
      api.decideSubjectChange(caseId, requestId, input.value, input.idempotencyKey),
    onSuccess: (result) =>
      invalidateSubjectChangeViews(
        queryClient,
        caseId,
        requestId,
        result.decision === "approved",
      ),
  });
}

export function useCreateSubjectSourceSnapshot(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { value: SubjectSourceSnapshotInput; idempotencyKey: string }) =>
      api.createSubjectSourceSnapshot(caseId, input.value, input.idempotencyKey),
    onSuccess: async (snapshot) => {
      await queryClient.invalidateQueries({
        queryKey: queryKeys.caseSubjectSourceSnapshots(caseId, null),
        exact: true,
      });
      await queryClient.invalidateQueries({ queryKey: queryKeys.caseSubjectSourceSnapshot(caseId, snapshot.snapshot_id), exact: true });
    },
  });
}

export function useSubjectSourceSnapshots(caseId: string, cursor: string | null = null) {
  return useQuery({
    queryKey: queryKeys.caseSubjectSourceSnapshots(caseId, cursor),
    queryFn: ({ signal }) => api.listSubjectSourceSnapshots(caseId, cursor, 50, signal),
    enabled: caseId !== "",
  });
}

export function useSubjectSourceDirectory(caseId: string) {
  return useQuery({
    queryKey: queryKeys.caseSubjectSources(caseId),
    queryFn: ({ signal }) => api.getSubjectSourceDirectory(caseId, signal),
    enabled: caseId !== "",
  });
}

export function useSubjectSourceSnapshot(caseId: string, snapshotId: string) {
  return useQuery({
    queryKey: queryKeys.caseSubjectSourceSnapshot(caseId, snapshotId),
    queryFn: ({ signal }) => api.getSubjectSourceSnapshot(caseId, snapshotId, signal),
    enabled: caseId !== "" && snapshotId !== "",
  });
}

export function useVerifySubjectAgainstSourceSnapshot(caseId: string, snapshotId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { value: SubjectSourceVerificationInput; idempotencyKey: string }) =>
      api.verifySubjectAgainstSourceSnapshot(caseId, snapshotId, input.value, input.idempotencyKey),
    onSuccess: async (result) => {
      if (result.request_id !== null) {
        await queryClient.invalidateQueries({
          queryKey: queryKeys.caseSubjectChanges(caseId, null),
          exact: true,
        });
      }
    },
  });
}

export function useTemplates(enabled = true) {
  return useQuery({
    queryKey: queryKeys.templates,
    queryFn: ({ signal }) => api.listTemplates(signal),
    enabled,
  });
}

export function useCaseReviewHistory(
  caseId: string,
  latestRunId: string | null,
) {
  return useQuery({
    queryKey: queryKeys.reviewHistory(caseId, latestRunId ?? ""),
    queryFn: ({ signal }) =>
      api.getReviewHistory(caseId, latestRunId ?? "", signal),
    enabled: caseId !== "" && latestRunId !== null,
  });
}

export function useUploadDocument(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: UploadDocumentInput) =>
      api.uploadDocument(caseId, input),
    onSuccess: () =>
      invalidateCaseViews(queryClient, caseId, {
        caseList: true,
        workbench: true,
      }),
  });
}

export function useConfirmLegacyMaterialSelection(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { version: number; document_ids: string[] }) =>
      api.confirmLegacyMaterialSelection(caseId, input),
    onSuccess: () => invalidateCaseViews(queryClient, caseId),
  });
}

export function useReparse(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (documentId: string) => api.reparseDocument(caseId, documentId),
    onSuccess: async (_result, documentId) => {
      const detail = queryClient.getQueryData<CaseDetail>(
        queryKeys.case(caseId),
      );
      const materialVersion = detail?.material_versions?.find(
        (version) => version.document_id === documentId,
      );
      const parsedDocumentsKey = materialVersion
        ? queryKeys.materialParsedDocuments(
            caseId,
            materialVersion.material_id,
            materialVersion.version,
          )
        : (["cases", caseId, "materials"] as const);
      await Promise.all([
        invalidateCaseViews(queryClient, caseId, { workbench: true }),
        queryClient.invalidateQueries({ queryKey: parsedDocumentsKey }),
      ]);
    },
  });
}

export function useStartReview(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { business_at?: string; scope?: "full_review" | "single_contract_precheck" }) =>
      keyedMutation(queryClient, "review.start", caseId, input, (key) => api.startReview(caseId, input, key)),
    onSuccess: () =>
      invalidateCaseViews(queryClient, caseId, {
        caseList: true,
        workbench: true,
      }),
  });
}

interface ReviewPolling {
  scope: string;
  data: ReviewWorkspace | undefined;
  updateCount: number;
  unchanged: number;
  wasLive: boolean;
}

interface ReviewPollQuery {
  state: {
    data: ReviewWorkspace | undefined;
    dataUpdateCount: number;
    error: unknown;
  };
}

function reviewScope(
  caseId: string,
  runId: string,
  attemptId: string | null | undefined,
): string {
  return JSON.stringify([caseId, runId, attemptId ?? null]);
}

function activeReviewState(data: ReviewWorkspace | undefined): string | undefined {
  return data?.review_attempts?.attempt.state ?? data?.run.state;
}

function advanceReviewPolling(
  previous: ReviewPolling,
  data: ReviewWorkspace | undefined,
  updateCount: number,
  scope: string,
): ReviewPolling {
  if (previous.wasLive) {
    return { scope, data, updateCount, unchanged: 0, wasLive: false };
  }
  if (previous.updateCount !== updateCount) {
    // React Query retains the data reference for structurally identical reads.
    // Only completed unchanged reads advance the bounded 2/4/8s backoff.
    return {
      scope,
      data,
      updateCount,
      unchanged: previous.data === data ? Math.min(previous.unchanged + 1, 2) : 0,
      wasLive: false,
    };
  }
  return previous;
}

function reviewRefetchInterval(
  query: ReviewPollQuery,
  polling: { current: ReviewPolling },
  caseId: string,
  runId: string,
  attemptId: string | null | undefined,
  streamLive: boolean,
): number | false {
  if (isForbidden(query.state.error) || isUnauthenticated(query.state.error)) return false;
  const data = query.state.data;
  const state = activeReviewState(data);
  if (!state || !isActiveAttemptState(state)) return false;
  const scope = reviewScope(caseId, runId, attemptId);
  if (polling.current.scope !== scope) {
    polling.current = { scope, data: undefined, updateCount: 0, unchanged: 0, wasLive: false };
  }
  if (streamLive) {
    polling.current = { scope, data, updateCount: query.state.dataUpdateCount, unchanged: 0, wasLive: true };
    // A live stream drives immediate refreshes; this is only a safety read.
    return 30_000;
  }
  polling.current = advanceReviewPolling(
    polling.current,
    data,
    query.state.dataUpdateCount,
    scope,
  );
  return 2_000 * 2 ** polling.current.unchanged;
}

export function useReview(
  caseId: string,
  runId: string,
  attemptId?: string | null,
  streamLive = false,
) {
  const polling = useRef<ReviewPolling>({
    scope: "",
    data: undefined,
    updateCount: 0,
    unchanged: 0,
    wasLive: false,
  });
  return useQuery({
    queryKey: queryKeys.review(caseId, runId, attemptId),
    queryFn: ({ signal }) => api.getReview(caseId, runId, attemptId, signal),
    enabled: caseId !== "" && runId !== "",
    refetchInterval: (query) =>
      reviewRefetchInterval(
        query,
        polling,
        caseId,
        runId,
        attemptId,
        streamLive,
      ),
  });
}

export function useReviewDiagnostics(
  caseId: string,
  runId: string,
  attemptId: string | null,
  enabled: boolean,
) {
  return useQuery({
    queryKey: queryKeys.reviewDiagnostics(caseId, runId, attemptId),
    queryFn: ({ signal }) => api.getReviewDiagnostics(caseId, runId, attemptId, signal),
    enabled: enabled && caseId !== "" && runId !== "",
    retry: false,
  });
}

export function useRetryReview(caseId: string, runId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.retryReview(caseId, runId),
    onSuccess: () =>
      invalidateReviewMutationViews(queryClient, caseId, runId),
  });
}

export function useCancelReview(caseId: string, runId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.cancelReview(caseId, runId),
    onSuccess: () =>
      invalidateReviewMutationViews(queryClient, caseId, runId),
  });
}

export function useSubmitDecision(caseId: string, runId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { findingId: string; decision: DecisionInput }) =>
      api.submitDecision(caseId, runId, input.findingId, input.decision),
    onSuccess: () =>
      invalidateReviewMutationViews(queryClient, caseId, runId),
  });
}
export function useCreateGeneration(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (command: { input: GenerationInput; idempotencyKey: string }) =>
      api.createGeneration(caseId, command.input, command.idempotencyKey),
    onSuccess: () =>
      invalidateCaseViews(queryClient, caseId, {
        caseList: true,
        workbench: true,
      }),
  });
}

export function useCreateRemediation(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: RemediationCreateInput) =>
      api.createRemediation(caseId, input),
    onSuccess: () =>
      invalidateCaseViews(queryClient, caseId, {
        caseList: true,
        workbench: true,
      }),
  });
}

export function useSubmitRemediation(caseId: string, itemId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: RemediationSubmitInput) =>
      api.submitRemediation(caseId, itemId, input),
    onSuccess: () =>
      invalidateCaseViews(queryClient, caseId, {
        caseList: true,
        workbench: true,
      }),
  });
}

export function useVerifyRemediation(caseId: string, itemId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: RemediationVerifyInput) =>
      api.verifyRemediation(caseId, itemId, input),
    onSuccess: () =>
      invalidateCaseViews(queryClient, caseId, {
        caseList: true,
        workbench: true,
      }),
  });
}

export function useCreateReport(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: ReportCreateInput) =>
      keyedMutation(queryClient, "report.create", caseId, input, (key) => api.createReport(caseId, input, key)),
    onSuccess: () =>
      invalidateCaseViews(queryClient, caseId, {
        caseList: true,
        workbench: true,
      }),
  });
}

export function useArchiveCase(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: ArchiveInput) => api.archiveCase(caseId, input),
    onSuccess: () =>
      invalidateCaseViews(queryClient, caseId, {
        caseList: true,
        workbench: true,
      }),
  });
}

export function useSettings(audience: SessionAudience = "client") {
  return useQuery({
    queryKey: queryKeys.settingsFor(audience),
    queryFn: ({ signal }) => api.getSettings(audience, signal),
  });
}

export function useLLMEndpoint(enabled = true) {
  return useQuery({
    queryKey: queryKeys.llmEndpoint,
    queryFn: ({ signal }) => api.getLLMEndpoint(signal),
    enabled,
    retry: false,
  });
}

function useRefreshLLMEndpoint() {
  const queryClient = useQueryClient();
  return async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: queryKeys.llmEndpoint }),
      queryClient.invalidateQueries({ queryKey: queryKeys.settingsFor("admin") }),
    ]);
  };
}

export function useProbeLLMEndpoint() {
  return useMutation({
    mutationFn: (input: {
      protocol: string;
      base_url: string;
      api_key?: string;
      clear_api_key?: boolean;
    }) => api.probeLLMEndpoint(input),
    retry: false,
  });
}

export function useSaveLLMEndpoint() {
  const refresh = useRefreshLLMEndpoint();
  return useMutation({
    mutationFn: (input: LLMEndpointInput) => api.saveLLMEndpoint(input),
    onSuccess: refresh,
    retry: false,
  });
}

export function useSaveLLMModels() {
  const refresh = useRefreshLLMEndpoint();
  return useMutation({
    mutationFn: (input: LLMModelSelectionInput) => api.saveLLMModels(input),
    onSuccess: refresh,
    retry: false,
  });
}
export function useRulePacks() {
  return useQuery({
    queryKey: queryKeys.rulePackList,
    queryFn: ({ signal }) => api.listRulePacks(signal),
    retry: false,
  });
}

export function useRulePack(packId: string, version: string) {
  return useQuery({
    queryKey: queryKeys.rulePack(packId, version),
    queryFn: ({ signal }) => api.getRulePack(packId, version, signal),
    enabled: packId !== "" && version !== "",
    retry: false,
  });
}

export function useRulePackHistory(packId: string) {
  return useQuery({
    queryKey: queryKeys.rulePackHistory(packId),
    queryFn: ({ signal }) => api.getRulePackHistory(packId, signal),
    enabled: packId !== "",
    retry: false,
  });
}

function useRulePackMutation<TInput, TOutput>(
  mutationFn: (input: TInput) => Promise<TOutput>,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.rulePacks });
    },
    retry: false,
  });
}

export function useCreateRulePack() {
  return useRulePackMutation((manifest: RulePackManifestInput) =>
    api.createRulePack(manifest),
  );
}

export function useEditRulePack(packId: string, version: string) {
  return useRulePackMutation(
    (input: { expectedRevision: number; manifest: RulePackManifestInput }) =>
      api.editRulePack(packId, version, input.expectedRevision, input.manifest),
  );
}

export function useValidateRulePack(packId: string, version: string) {
  return useRulePackMutation((expectedRevision: number) =>
    api.validateRulePack(packId, version, expectedRevision),
  );
}

export function useApproveRulePack(packId: string, version: string) {
  return useRulePackMutation(
    (input: { expectedRevision: number; expectedContentHash: string }) =>
      api.approveRulePack(
        packId,
        version,
        input.expectedRevision,
        input.expectedContentHash,
      ),
  );
}

export function usePublishRulePack(packId: string, version: string) {
  return useRulePackMutation(
    (input: {
      expectedRevision: number;
      expectedPointerRevision: number;
      idempotencyKey: string;
    }) =>
      api.publishRulePack(
        packId,
        version,
        input.expectedRevision,
        input.expectedPointerRevision,
        input.idempotencyKey,
      ),
  );
}

export function useDisableRulePack(packId: string, version: string) {
  return useRulePackMutation(
    (input: { expectedRevision: number; expectedPointerRevision: number }) =>
      api.disableRulePack(
        packId,
        version,
        input.expectedRevision,
        input.expectedPointerRevision,
      ),
  );
}

export function useRollbackRulePack(packId: string) {
  return useRulePackMutation(
    (input: {
      version: string;
      expectedPointerRevision: number;
      idempotencyKey: string;
    }) =>
      api.rollbackRulePack(
        packId,
        input.version,
        input.expectedPointerRevision,
        input.idempotencyKey,
      ),
  );
}

export function useTestRulePack(packId: string, version: string) {
  return useMutation({
    mutationFn: (input: RulePackTestInput) =>
      api.testRulePack(packId, version, input),
    retry: false,
  });
}
