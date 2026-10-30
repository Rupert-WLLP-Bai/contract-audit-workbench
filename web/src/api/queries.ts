import { useRef } from "react";
import {
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from "@tanstack/react-query";
import * as api from "./endpoints";
import { isForbidden, isUnauthenticated, setSessionIdentity } from "./http";
import type {
  Actor,
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
} from "./types";
import { isActiveAttemptState } from "../domain/review";
import type { RulePackManifestInput, RulePackTestInput } from "./rulepacks";

export const queryKeys = {
  session: ["session", "client"] as const,
  sessionFor: (audience: SessionAudience) =>
    audience === "admin" ? (["session", "admin"] as const) : queryKeys.session,
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
};
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

export function useUpdateCaseProfile(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: CaseProfileInput) =>
      api.updateCaseProfile(caseId, input),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: queryKeys.workbenchPrefix,
      });
      await queryClient.invalidateQueries({ queryKey: queryKeys.case(caseId) });
      await queryClient.invalidateQueries({ queryKey: queryKeys.cases });
    },
  });
}

export function useCreateWorkbenchTask(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: WorkbenchTaskInput) =>
      api.createWorkbenchTask(caseId, input),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: queryKeys.workbenchPrefix,
      });
    },
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
    onSuccess: async (_task, input) => {
      await queryClient.invalidateQueries({
        queryKey: queryKeys.workbenchPrefix,
      });
      await queryClient.invalidateQueries({
        queryKey: queryKeys.case(input.caseId),
      });
    },
  });
}

export function useCreateCaseRelation(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: CaseRelationInput) =>
      api.createCaseRelation(caseId, input),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: queryKeys.workbenchPrefix,
      });
    },
  });
}

export function useDeleteCaseRelation(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (relationId: string) =>
      api.deleteCaseRelation(caseId, relationId),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: queryKeys.workbenchPrefix,
      });
    },
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
    mutationFn: api.createCase,
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.cases });
    },
  });
}

export function useCaseDetail(caseId: string) {
  return useQuery({
    queryKey: queryKeys.case(caseId),
    queryFn: ({ signal }) => api.getCase(caseId, signal),
    enabled: caseId !== "",
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
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.case(caseId) });
      await queryClient.invalidateQueries({ queryKey: queryKeys.cases });
    },
  });
}

export function useConfirmLegacyMaterialSelection(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { version: number; document_ids: string[] }) =>
      api.confirmLegacyMaterialSelection(caseId, input),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.case(caseId) });
    },
  });
}

export function useReparse(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (documentId: string) => api.reparseDocument(caseId, documentId),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.case(caseId) });
    },
  });
}

export function useStartReview(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { business_at?: string }) => api.startReview(caseId, input),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.case(caseId) });
    },
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
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: queryKeys.reviewPrefix(caseId, runId),
      });
      await queryClient.invalidateQueries({ queryKey: queryKeys.case(caseId) });
    },
  });
}

export function useCancelReview(caseId: string, runId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.cancelReview(caseId, runId),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: queryKeys.reviewPrefix(caseId, runId),
      });
      await queryClient.invalidateQueries({ queryKey: queryKeys.case(caseId) });
    },
  });
}

export function useSubmitDecision(caseId: string, runId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { findingId: string; decision: DecisionInput }) =>
      api.submitDecision(caseId, runId, input.findingId, input.decision),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: queryKeys.reviewPrefix(caseId, runId),
      });
      await queryClient.invalidateQueries({ queryKey: queryKeys.case(caseId) });
      await queryClient.invalidateQueries({ queryKey: queryKeys.cases });
    },
  });
}
export function useCreateGeneration(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (command: { input: GenerationInput; idempotencyKey: string }) =>
      api.createGeneration(caseId, command.input, command.idempotencyKey),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.case(caseId) });
      await queryClient.invalidateQueries({ queryKey: queryKeys.cases });
    },
  });
}

export function useCreateRemediation(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: RemediationCreateInput) =>
      api.createRemediation(caseId, input),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.case(caseId) });
      await queryClient.invalidateQueries({ queryKey: queryKeys.cases });
    },
  });
}

export function useSubmitRemediation(caseId: string, itemId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: RemediationSubmitInput) =>
      api.submitRemediation(caseId, itemId, input),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.case(caseId) });
      await queryClient.invalidateQueries({ queryKey: queryKeys.cases });
    },
  });
}

export function useVerifyRemediation(caseId: string, itemId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: RemediationVerifyInput) =>
      api.verifyRemediation(caseId, itemId, input),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.case(caseId) });
      await queryClient.invalidateQueries({ queryKey: queryKeys.cases });
    },
  });
}

export function useCreateReport(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: ReportCreateInput) => api.createReport(caseId, input),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.case(caseId) });
      await queryClient.invalidateQueries({ queryKey: queryKeys.cases });
    },
  });
}

export function useArchiveCase(caseId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: ArchiveInput) => api.archiveCase(caseId, input),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.case(caseId) });
      await queryClient.invalidateQueries({ queryKey: queryKeys.cases });
    },
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
