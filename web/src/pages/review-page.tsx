import { Button } from "antd";
import { useCallback, useEffect, useState } from "react";
import { useParams, useSearchParams } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import {
  errorMessage,
  isConflict,
  isForbidden,
  isUnauthenticated,
} from "../api/http";
import {
  clearIdentityQueries,
  queryKeys,
  useCancelReview,
  useCaseDetail,
  useCreateCaseRelation,
  useDeleteCaseRelation,
  useRetryReview,
  useReview,
  useSession,
  useSubmitDecision,
  useWorkbench,
} from "../api/queries";
import { paths } from "../api/paths";
import type { CaseRelationInput, DecisionAction } from "../api/types";
import { PageStatus } from "../components/page-status";
import { ReviewWorkspaceView } from "../components/review-workspace";
import type { ReviewCaseContext } from "../components/document-stage";
import { RunProvenance } from "../components/run-provenance";
import { AuditProjection } from "../components/audit-projection";
import {
  contractTypeLabel,
  isActiveAttemptState,
  isTerminalAttemptState,
  selectedFindingId,
} from "../domain/review";
import { useReviewEvents } from "../hooks/use-review-events";
import { useReviewLayout } from "../hooks/use-viewport-layout";

export function ReviewPage() {
  const { caseId = "", runId = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const selectedAttemptId = params.get("attempt_id");
  const review = useReview(caseId, runId, selectedAttemptId);
  const workbench = useWorkbench("week");
  const createRelation = useCreateCaseRelation(caseId);
  const deleteRelation = useDeleteCaseRelation(caseId);
  const caseDetail = useCaseDetail(caseId);
  const session = useSession();
  const retry = useRetryReview(caseId, runId);
  const cancel = useCancelReview(caseId, runId);
  const decide = useSubmitDecision(caseId, runId);
  const queryClient = useQueryClient();
  const layout = useReviewLayout();
  const [conflictMessage, setConflictMessage] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [conflictFinding, setConflictFinding] = useState<string | null>(null);

  const execution = review.data?.execution;
  const selectedAttempt = execution?.attempt ?? null;
  const latestAttempt = execution
    ? execution.attempts.reduce(
        (latest, attempt) =>
          attempt.ordinal > latest.ordinal ? attempt : latest,
        execution.attempt,
      )
    : null;
  const selectedAttemptIsLatest =
    execution === null || latestAttempt?.id === selectedAttempt?.id;
  const caseDetailReady = caseDetail.isSuccess && !caseDetail.isFetching;
  const caseArchived = caseDetailReady && Boolean(caseDetail.data.archived_at);
  // A background permission refresh disables writes without disconnecting SSE.
  const latestRun =
    caseDetail.isSuccess && caseDetail.data.latest_run?.id === runId;
  const roleCanManageRun =
    session.data?.role === "admin" ||
    session.data?.role === "reviewer" ||
    session.data?.role === "clerk";
  const roleCanDecide =
    session.data?.role === "admin" || session.data?.role === "reviewer";
  const decisionState =
    selectedAttempt?.state ?? review.data?.run.state ?? "unknown";
  const terminal = isTerminalAttemptState(decisionState);
  const canDecide =
    caseDetailReady &&
    !caseArchived &&
    latestRun &&
    roleCanDecide &&
    selectedAttemptIsLatest &&
    terminal;
  const retryableState =
    review.data !== undefined &&
    ["partial", "failed", "cancelled", "interrupted"].includes(
      review.data.run.state,
    );
  const canRetry =
    caseDetailReady &&
    !caseArchived &&
    latestRun &&
    roleCanManageRun &&
    selectedAttemptIsLatest &&
    terminal &&
    retryableState;
  const canCancel =
    caseDetailReady &&
    !caseArchived &&
    latestRun &&
    roleCanManageRun &&
    selectedAttempt !== null &&
    selectedAttemptIsLatest &&
    isActiveAttemptState(selectedAttempt.state);
  const decisionDisabledReason = canDecide
    ? null
    : !caseDetailReady
      ? caseDetail.isError
        ? "无法确认案件状态，历史审查只读。"
        : "正在确认案件状态，暂不能提交人工决定。"
      : caseArchived
        ? "案件已归档，历史审查只读。"
        : !latestRun
          ? "此运行不是案件当前最新运行，人工决定只读。"
          : !selectedAttemptIsLatest
            ? "当前选择为历史尝试，只能查看，不能提交人工决定。"
            : !terminal
              ? "当前尝试仍在执行，完成后才能提交人工决定。"
              : !roleCanDecide
                ? "当前角色不能提交人工决定。"
                : "当前运行状态不允许提交人工决定。";
  const refresh = useCallback(() => {
    void queryClient.invalidateQueries({
      queryKey: queryKeys.reviewPrefix(caseId, runId),
    });
    // Exact: the case key is a prefix of the review key, so a non-exact
    // invalidation would refetch the same workspace twice per event and let a
    // second, successful read mask a refused (403) one.
    void queryClient.invalidateQueries({
      queryKey: queryKeys.case(caseId),
      exact: true,
    });
  }, [caseId, queryClient, runId]);

  const streamScope = session.data
    ? `${session.data.id}\u0000${session.data.role}`
    : null;
  const streamActive = review.isSuccess && latestRun && selectedAttemptIsLatest;
  const streamStatus = useReviewEvents(
    paths.reviewEvents(caseId, runId),
    streamActive,
    refresh,
    streamScope,
  );

  useEffect(() => {
    if (streamStatus !== "closed") return;
    let active = true;
    void (async () => {
      await queryClient.invalidateQueries({
        queryKey: queryKeys.session,
        exact: true,
      });
      if (!active) return;
      if (
        isUnauthenticated(queryClient.getQueryState(queryKeys.session)?.error)
      ) {
        clearIdentityQueries(queryClient);
        return;
      }
      await queryClient.invalidateQueries({
        queryKey: queryKeys.reviewPrefix(caseId, runId),
      });
      await queryClient.invalidateQueries({
        queryKey: queryKeys.case(caseId),
        exact: true,
      });
    })();
    return () => {
      active = false;
    };
  }, [caseId, queryClient, runId, streamStatus]);

  if (review.isLoading)
    return <PageStatus kind="loading" title="正在加载审查工作区" />;
  if (review.isError) {
    if (isForbidden(review.error)) {
      return (
        <PageStatus
          kind="denied"
          title="没有权限查看这次审查"
          detail={errorMessage(review.error)}
          action={
            <Button
              aria-label={"返回案件"}
              type="primary"
              href={`/cases/${caseId}`}
            >
              返回案件
            </Button>
          }
        />
      );
    }
    return (
      <PageStatus
        kind="error"
        title="审查工作区没有加载"
        detail={errorMessage(review.error)}
        action={
          <Button
            aria-label={"重试"}
            type="primary"
            htmlType="button"
            onClick={() => void review.refetch()}
          >
            重试
          </Button>
        }
      />
    );
  }
  if (!review.data) return <PageStatus kind="empty" title="没有审查数据" />;

  const selectedId = selectedFindingId(
    review.data.findings,
    params.get("finding"),
  );
  const snapshot = workbench.data;
  const contextCase = snapshot?.cases.find((item) => item.id === caseId);
  const profile =
    contextCase?.profile ??
    snapshot?.profiles.find((item) => item.case_id === caseId) ??
    null;
  const memberById = new Map(
    (snapshot?.members ?? []).map((member) => [member.id, member]),
  );
  const collaboratorById = new Map<
    string,
    { id: string; name: string; role: string; responsibilities: string[] }
  >();
  const addCollaborator = (id: string, responsibility: string) => {
    const member = memberById.get(id);
    const existing = collaboratorById.get(id);
    if (existing) existing.responsibilities.push(responsibility);
    else
      collaboratorById.set(id, {
        id,
        name: member?.display_name ?? id,
        role: member?.role ?? "角色未报告",
        responsibilities: [responsibility],
      });
  };
  for (const member of caseDetail.data?.members ?? []) {
    memberById.set(member.id, member);
    addCollaborator(member.id, "案件成员");
  }
  if (profile?.owner_id) addCollaborator(profile.owner_id, "案件负责人");
  for (const task of snapshot?.tasks ?? [])
    if (task.case_id === caseId)
      addCollaborator(
        task.assignee_id,
        `任务责任人：${task.title}（${task.status}）`,
      );
  const currentRelations = (snapshot?.relations ?? []).filter(
    (relation) => relation.case_id === caseId,
  );
  const relatedIds = new Set(
    (snapshot?.relations ?? [])
      .filter(
        (relation) =>
          relation.case_id === caseId || relation.related_case_id === caseId,
      )
      .map((relation) =>
        relation.case_id === caseId
          ? relation.related_case_id
          : relation.case_id,
      ),
  );
  const caseContext: ReviewCaseContext | null = snapshot
    ? {
        caseId,
        title: caseDetail.data?.title ?? contextCase?.title ?? caseId,
        number: profile?.number ?? null,
        customer: profile?.customer ?? null,
        supplier: profile?.supplier ?? null,
        contractType: contractTypeLabel(
          contextCase?.contract_type ?? caseDetail.data?.contract_type ?? "",
        ),
        businessLine:
          contextCase?.business_line ??
          caseDetail.data?.business_line ??
          "未报告",
        scenario:
          contextCase?.scenario ?? caseDetail.data?.scenario ?? "未报告",
        status:
          contextCase?.business_status ??
          caseDetail.data?.business_status ??
          "未报告",
        ownerId: profile?.owner_id ?? null,
        ownerName: profile?.owner_id
          ? (memberById.get(profile.owner_id)?.display_name ?? null)
          : null,
        dueAt: profile?.due_at ?? null,
        collaborators: [...collaboratorById.values()],
        relatedCases: currentRelations.map((relation) => {
          const relatedCase = snapshot.cases.find(
            (item) => item.id === relation.related_case_id,
          );
          return {
            relationId: relation.id,
            id: relation.related_case_id,
            title: relatedCase?.title ?? relation.related_case_id,
            reason: relation.reason,
            createdAt: relation.created_at,
            runId: relatedCase?.latest_run?.id ?? null,
          };
        }),
        availableCases: snapshot.cases
          .filter((item) => item.id !== caseId && !relatedIds.has(item.id))
          .map((item) => ({ id: item.id, title: item.title })),
      }
    : null;
  const canEditRelations = caseDetailReady && !caseArchived && roleCanManageRun;
  const relationPending = createRelation.isPending || deleteRelation.isPending;
  const relationError = createRelation.isError
    ? errorMessage(createRelation.error)
    : deleteRelation.isError
      ? errorMessage(deleteRelation.error)
      : null;

  async function saveRelation(input: CaseRelationInput): Promise<void> {
    await createRelation.mutateAsync(input);
  }

  async function onDecide(input: {
    findingId: string;
    action: DecisionAction;
    reason: string;
    expectedVersion: number;
  }): Promise<boolean> {
    if (!canDecide) return false;
    setConflictMessage(null);
    setSaveError(null);
    try {
      await decide.mutateAsync({ findingId: input.findingId, decision: input });
      return true;
    } catch (error) {
      await review.refetch();
      setConflictFinding(input.findingId);
      if (isConflict(error)) {
        setConflictMessage("该事项已被他人更新，已载入最新内容，请重新确认。");
        setSaveError(null);
      } else {
        setConflictMessage(null);
        setSaveError(errorMessage(error));
      }
      return false;
    }
  }

  return (
    <section className="page page-review">
      <header className="page-head">
        <div>
          <h1>审查工作区</h1>
          <p>{caseDetail.data?.title ?? caseId}</p>
        </div>
        <Button
          aria-label={"返回案件"}
          href={`/cases/${encodeURIComponent(caseId)}`}
        >
          返回案件
        </Button>
      </header>
      {!caseDetailReady ? (
        <p className="callout">
          {caseDetail.isError
            ? "无法确认案件状态，当前仅可查看。"
            : "正在确认案件状态，写入操作暂不可用。"}
        </p>
      ) : caseArchived ? (
        <p className="callout">案件已归档，历史审查仅供查看。</p>
      ) : null}
      <ReviewWorkspaceView
        workspace={review.data}
        layout={layout}
        selectedId={selectedId}
        caseContext={caseContext}
        canEditRelations={canEditRelations}
        relationPending={relationPending}
        relationError={relationError}
        streamStatus={streamStatus}
        pending={decide.isPending}
        retrying={retry.isPending}
        canDecide={canDecide}
        decisionDisabledReason={decisionDisabledReason}
        canRetry={canRetry}
        canCancel={canCancel}
        cancelling={cancel.isPending}
        cancelError={cancel.isError ? errorMessage(cancel.error) : null}
        conflictMessage={
          conflictFinding === selectedId ? conflictMessage : null
        }
        saveError={conflictFinding === selectedId ? saveError : null}
        onSelect={(findingId) => {
          const next = new URLSearchParams(params);
          next.set("finding", findingId);
          setParams(next);
        }}
        onSelectAttempt={(attemptId) => {
          const next = new URLSearchParams(params);
          next.set("attempt_id", attemptId);
          setParams(next);
        }}
        onDecide={onDecide}
        onRetry={() => {
          if (!canRetry) return;
          retry.mutate(undefined, {
            onSuccess: (accepted) => {
              const next = new URLSearchParams(params);
              next.set("attempt_id", accepted.attempt_id);
              setParams(next);
            },
          });
        }}
        onCancel={() => {
          if (canCancel) cancel.mutate();
        }}
        onCreateRelation={saveRelation}
        onDeleteRelation={(relationId) => deleteRelation.mutate(relationId)}
      />
      <RunProvenance workspace={review.data} />
      <AuditProjection
        runId={review.data.run.id}
        events={review.data.audit_events}
      />
      {retry.isError ? <p role="alert">{errorMessage(retry.error)}</p> : null}
    </section>
  );
}
