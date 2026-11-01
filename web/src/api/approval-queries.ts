import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import {
  decideApprovalTask,
  getApprovalTask,
  getContractApprovalRound,
  listApprovalInbox,
  listContractApprovalRounds,
  type ApprovalDecisionInput,
  type ApprovalDecisionResult,
  type ApprovalInboxPage,
  type ApprovalInboxView,
  type ApprovalRoundHistoryPage,
  type ApprovalRoundReadDetail,
  type ApprovalTaskReadDetail,
} from "./approvals";
import {
  clearPendingMutationKey,
  idempotencyActorScope,
  pendingMutationKey,
} from "./idempotency";
import { queryKeys } from "./queries";
import type { Actor } from "./types";

export const approvalQueryKeys = {
  all: ["approvals"] as const,
  inboxPrefix: ["approvals", "inbox"] as const,
  inbox: (actorScope: string, view: ApprovalInboxView) =>
    ["approvals", "inbox", actorScope, view] as const,
  task: (taskId: string) => ["approvals", "task", taskId] as const,
  rounds: (contractId: string) =>
    ["approvals", "rounds", contractId] as const,
  round: (contractId: string, roundId: string) =>
    ["approvals", "round", contractId, roundId] as const,
};

export function useApprovalInbox(
  view: ApprovalInboxView,
  actorScope: string,
  enabled: boolean,
) {
  return useInfiniteQuery({
    queryKey: approvalQueryKeys.inbox(actorScope, view),
    queryFn: ({ pageParam, signal }) =>
      listApprovalInbox(view, pageParam, 25, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage: ApprovalInboxPage) =>
      lastPage.has_more ? nextApprovalCursor(lastPage) : undefined,
    enabled,
  });
}

export function useApprovalTaskDetail(taskId: string | null) {
  return useQuery<ApprovalTaskReadDetail>({
    queryKey: approvalQueryKeys.task(taskId ?? ""),
    queryFn: ({ signal }) => getApprovalTask(taskId as string, signal),
    enabled: taskId !== null,
  });
}

export function useContractApprovalRounds(
  contractId: string | null,
  enabled: boolean,
) {
  return useInfiniteQuery({
    queryKey: approvalQueryKeys.rounds(contractId ?? ""),
    queryFn: ({ pageParam, signal }) =>
      listContractApprovalRounds(contractId as string, pageParam, 20, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage: ApprovalRoundHistoryPage) =>
      lastPage.has_more ? nextApprovalCursor(lastPage) : undefined,
    enabled: enabled && contractId !== null,
  });
}

export function useContractApprovalRound(
  contractId: string | null,
  roundId: string | null,
) {
  return useQuery<ApprovalRoundReadDetail>({
    queryKey: approvalQueryKeys.round(
      contractId ?? "",
      roundId ?? "",
    ),
    queryFn: ({ signal }) =>
      getContractApprovalRound(
        contractId as string,
        roundId as string,
        signal,
      ),
    enabled: contractId !== null && roundId !== null,
  });
}

function nextApprovalCursor(
  page: ApprovalInboxPage | ApprovalRoundHistoryPage,
): string | undefined {
  return page.next_cursor ?? undefined;
}

/**
 * A decision is only reported as committed after the server response. The
 * pending idempotency key is retained on failure so a network retry replays the
 * same command instead of creating a second decision; the query cache is
 * refreshed from the server, never optimistically marked approved.
 */
export function useDecideApprovalTask() {
  const queryClient = useQueryClient();
  return useMutation<ApprovalDecisionResult, Error, ApprovalDecisionInput>({
    mutationFn: async (input) => {
      const actor = queryClient.getQueryData<Actor>(queryKeys.session);
      if (!actor) throw new Error("登录状态不可用，请刷新后重试");
      const pending = await pendingMutationKey(
        "approval.decide",
        idempotencyActorScope(actor),
        input.task_id,
        input,
      );
      const result = await decideApprovalTask(input, pending.key);
      try {
        clearPendingMutationKey(pending.storageKey);
      } catch {
        // The response is already committed; storage cleanup is best effort.
      }
      return result;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: approvalQueryKeys.all });
    },
  });
}
