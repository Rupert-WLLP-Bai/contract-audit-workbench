import { Button } from "antd";
import { useCallback, useEffect, useMemo, useState } from "react";
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
  useReviewDiagnostics,
  useSession,
  useSubmitDecision,
  useWorkbench,
} from "../api/queries";
import { paths } from "../api/paths";
import type { CaseRelationInput, DecisionAction } from "../api/types";
import type { FrozenQuoteScope, FrozenQuoteSelection } from "../domain/frozen-quote";
import { PageStatus } from "../components/page-status";
import { ReviewWorkspaceView } from "../components/review-workspace";
import { DirtyLeaveGuard } from "../components/dirty-leave-guard";
import { ReviewDiff } from "../components/review-diff";
import type { ReviewCaseContext } from "../components/document-stage";
import { RunProvenance } from "../components/run-provenance";
import { FrozenLegalAuthorities } from "../components/frozen-legal-authorities";
import { AuditProjection } from "../components/audit-projection";
import { EvidenceRequestPanel } from "../components/evidence-request-panel";
import { EvidenceSLADashboard } from "../components/evidence-sla-dashboard";
import { ExperienceFeedbackPanel } from "../components/experience-feedback-panel";
import { ManualFindingsPanel } from "../components/manual-findings-panel";
import { ExperienceUsagePanel } from "../components/experience-usage-panel";
import { OperatorAuditEntry } from "../features/operator-audit/operator-audit-entry";
import {
  contractTypeLabel,
  isActiveAttemptState,
  isTerminalAttemptState,
  selectedFindingId,
  type FindingDecisionInput,
  type FindingDecisionOutcome,
} from "../domain/review";
import {
  useReviewEvents,
  type ReviewEventBatch,
} from "../hooks/use-review-events";
import {
  DecisionDraftScopeProvider,
  decisionDraftUserPrefix,
  useDecisionDraftsDirty,
} from "../hooks/decision-drafts";
import { useReviewLayout } from "../hooks/use-viewport-layout";

export function ReviewPage() {
  const { caseId = "", runId = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const selectedAttemptId = params.get("attempt_id");
  const selectedCitation = params.get("citation");
  const [streamLive, setStreamLive] = useState(false);
  const [streamReconnectKey, setStreamReconnectKey] = useState(0);
  const review = useReview(caseId, runId, selectedAttemptId, streamLive);
  const workbench = useWorkbench("week");
  const createRelation = useCreateCaseRelation(caseId);
  const deleteRelation = useDeleteCaseRelation(caseId);
  const caseDetail = useCaseDetail(caseId);
  const session = useSession();
  const [diagnosticScope, setDiagnosticScope] = useState<string | null>(null);
  const currentDiagnosticScope = session.data?.role === "admin"
    ? [caseId, runId, session.data.id, session.data.tenant_id, session.data.org_id].join("\u0000")
    : null;
  const diagnostics = useReviewDiagnostics(
    caseId,
    runId,
    selectedAttemptId,
    currentDiagnosticScope !== null && diagnosticScope === currentDiagnosticScope && review.isSuccess,
  );
  const retry = useRetryReview(caseId, runId);
  const cancel = useCancelReview(caseId, runId);
  const decide = useSubmitDecision(caseId, runId);
  const queryClient = useQueryClient();
  const layout = useReviewLayout();
  const [conflictMessage, setConflictMessage] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [conflictFinding, setConflictFinding] = useState<string | null>(null);
  const [quoteSelection, setQuoteSelection] = useState<FrozenQuoteSelection | null>(null);

  const execution = review.data?.review_attempts;
  const selectedAttempt = execution?.attempt ?? null;
  const quoteScope = useMemo<FrozenQuoteScope>(
    () => ({
      caseId,
      runId,
      attemptId: selectedAttempt?.id ?? null,
      isFrozenAttempt: selectedAttempt?.history_kind === "frozen",
      parsedDocumentIds: (review.data?.documents ?? []).flatMap((document) =>
        document.parsed_document_id ? [document.parsed_document_id] : [],
      ),
    }),
    [caseId, runId, selectedAttempt?.history_kind, selectedAttempt?.id, review.data?.documents],
  );
  useEffect(() => setQuoteSelection(null), [caseId, runId, session.data?.id, selectedAttempt?.id]);
  const decisionDraftScope = useMemo(
    () =>
      session.data
        ? {
            userId: session.data.id,
            caseId,
            runId,
            attemptId: selectedAttempt?.id ?? null,
          }
        : null,
    [session.data, caseId, runId, selectedAttempt?.id],
  );
  const decisionDraftsDirty = useDecisionDraftsDirty(
    session.data === undefined
      ? null
      : decisionDraftUserPrefix(session.data.id),
  );
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
        : "正在确认案件状态，暂时不能提交审查判断。"
      : caseArchived
        ? "案件已归档，历史审查只读。"
        : !latestRun
          ? "这不是该案件最新的审查记录，只能查看已有判断。"
          : !selectedAttemptIsLatest
            ? "当前选择的是之前的执行记录，只能查看，不能提交审查判断。"
            : !terminal
              ? "本次审查仍在执行，完成后才能提交审查判断。"
              : !roleCanDecide
                ? "当前角色不能提交审查判断。"
                : "当前审查状态不允许提交审查判断。";
  const refresh = useCallback(
    (batch: ReviewEventBatch) => {
      const requests = [
        queryClient.invalidateQueries({
          queryKey: queryKeys.review(caseId, runId, selectedAttemptId),
          exact: true,
        }),
      ];
      // The stream carries run state and decision events. Re-read the case
      // summary at a terminal boundary; case/material mutations use their own
      // targeted invalidations.
      if (batch.terminalState !== null) {
        requests.push(
          queryClient.invalidateQueries({
            queryKey: queryKeys.case(caseId),
            exact: true,
          }),
        );
      }
      return Promise.all(requests).then(() => undefined);
    },
    [caseId, queryClient, runId, selectedAttemptId],
  );

  // Reconcile persisted state after recovery; a manually created EventSource
  // starts without the previous Last-Event-ID.
  const reconcileAfterReconnect = useCallback(() => {
    void queryClient.invalidateQueries({
      queryKey: queryKeys.review(caseId, runId, selectedAttemptId),
      exact: true,
    });
    void queryClient.invalidateQueries({
      queryKey: queryKeys.case(caseId),
      exact: true,
    });
  }, [caseId, queryClient, runId, selectedAttemptId]);

  const streamScope = session.data
    ? JSON.stringify([
        session.data.audience,
        session.data.id,
        session.data.role,
        session.data.tenant_id,
        session.data.org_id,
      ])
    : null;
  const streamActive = review.isSuccess && latestRun && selectedAttemptIsLatest;
  const streamStatus = useReviewEvents(
    paths.reviewEvents(caseId, runId),
    streamActive,
    refresh,
    streamScope,
    streamReconnectKey,
    reconcileAfterReconnect,
  );

  useEffect(() => {
    setStreamLive(streamStatus === "live");
  }, [streamStatus]);

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
        queryKey: queryKeys.review(caseId, runId, selectedAttemptId),
        exact: true,
      });
      await queryClient.invalidateQueries({
        queryKey: queryKeys.case(caseId),
        exact: true,
      });
    })();
    return () => {
      active = false;
    };
  }, [caseId, queryClient, runId, selectedAttemptId, streamStatus]);

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

  const diagnosticData = currentDiagnosticScope !== null && diagnosticScope === currentDiagnosticScope && diagnostics.isSuccess && !diagnostics.isFetching &&
    (!diagnostics.data.execution || diagnostics.data.execution.attempt.id === selectedAttempt?.id)
    ? diagnostics.data
    : null;
  const workspace = {
    ...review.data,
    execution: diagnosticData?.execution ?? null,
    snapshot: diagnosticData?.snapshot ?? review.data.snapshot,
    audit_events: diagnosticData?.audit_events ?? review.data.audit_events,
  };

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
        name: member?.display_name ?? "姓名未返回",
        role: member?.role ?? "",
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
        title: caseDetail.data?.title ?? contextCase?.title ?? "案件",
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
            title: relatedCase?.title ?? "关联案件",
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

  async function onBatchDecide(input: FindingDecisionInput): Promise<FindingDecisionOutcome> {
    if (!canDecide) return { status: "failed", message: "当前审查状态不允许提交。", stop: true };
    try {
      await decide.mutateAsync({ findingId: input.findingId, decision: input });
      return { status: "success", message: "决定已保存。" };
    } catch (error) {
      let refreshFailed = false;
      try {
        refreshFailed = (await review.refetch()).isError;
      } catch {
        refreshFailed = true;
      }
      if (isConflict(error)) {
        return {
          status: "conflict",
          message: refreshFailed
            ? `${errorMessage(error)}；最新内容刷新失败，请刷新后再确认。`
            : `${errorMessage(error)}；请查看最新内容后重新确认。`,
          stop: refreshFailed,
        };
      }
      return {
        status: "failed",
        message: refreshFailed ? `${errorMessage(error)}；最新内容刷新失败。` : errorMessage(error),
        stop: refreshFailed || isUnauthenticated(error) || isForbidden(error),
      };
    }
  }

  return (
    <section className="page page-review">
      <header className="page-head">
        <div>
          <h1>审查工作区</h1>
          <p>{caseDetail.data?.title ?? "案件"}</p>
        </div>
        <Button
          aria-label={"返回案件"}
          href={`/cases/${encodeURIComponent(caseId)}`}
        >
          返回案件
        </Button>
      </header>
      {streamStatus === "exhausted" || streamStatus === "closed" ? (
        <section className="callout" role="status" aria-live="polite">
          <span>运行状态更新已停止，可以重新连接以同步最新审查结果。</span>
          <Button
            type="primary"
            htmlType="button"
            onClick={() => setStreamReconnectKey((key) => key + 1)}
          >
            重新连接运行状态
          </Button>
        </section>
      ) : null}
      {!caseDetailReady ? (
        <p className="callout">
          {caseDetail.isError
            ? "无法确认案件状态，当前仅可查看。"
            : "正在确认案件状态，写入操作暂不可用。"}
        </p>
      ) : caseArchived ? (
        <p className="callout">案件已归档，历史审查仅供查看。</p>
      ) : null}
      {session.isSuccess && !session.isFetching ? (
        <ReviewDiff
          caseId={caseId}
          latestRunId={caseDetail.data?.latest_run?.id ?? runId}
          actorScope={JSON.stringify([session.data.id, session.data.tenant_id, session.data.org_id, session.data.role, session.data.audience])}
        />
      ) : null}
      <DecisionDraftScopeProvider scope={decisionDraftScope}>
        <ReviewWorkspaceView
          workspace={workspace}
          layout={layout}
          showDiagnostics={diagnosticData !== null}
          quoteScope={quoteScope}
          onQuoteSelectionChange={setQuoteSelection}
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
            setParams((current) => {
              const next = new URLSearchParams(current);
              next.set("finding", findingId);
              return next;
            });
          }}
          onSelectAttempt={(attemptId) => {
            const next = new URLSearchParams(params);
            next.set("attempt_id", attemptId);
            // A citation belongs to its own attempt; never show the previous
            // attempt's evidence after switching.
            next.delete("citation");
            setParams(next);
          }}
          selectedEvidenceId={selectedCitation}
          onSelectEvidence={(evidenceId) => {
            const next = new URLSearchParams(params);
            if (selectedAttempt?.id) next.set("attempt_id", selectedAttempt.id);
            if (evidenceId === null) {
              next.delete("citation");
            } else {
              next.set("citation", evidenceId);
            }
            setParams(next);
          }}
          onDecide={onDecide}
          onBatchDecide={onBatchDecide}
          onRetry={() => {
            if (!canRetry) return;
            retry.mutate(undefined, {
              onSuccess: (accepted) => {
                const next = new URLSearchParams(params);
                next.set("attempt_id", accepted.attempt_id);
                next.delete("citation");
                setParams(next);
              },
            });
          }}
          onCancel={() => {
            if (canCancel) cancel.mutate();
          }}
          onCreateRelation={saveRelation}
          onDeleteRelation={(relationId) => deleteRelation.mutate(relationId)}
          diagnosticsLoading={diagnostics.isFetching}
          onLoadDiagnostics={
            currentDiagnosticScope && diagnosticScope !== currentDiagnosticScope
              ? () => setDiagnosticScope(currentDiagnosticScope)
              : undefined
          }
        />
      </DecisionDraftScopeProvider>
      <DirtyLeaveGuard dirty={decisionDraftsDirty} />
      <ManualFindingsPanel
        key={`${caseId}:${runId}:${selectedAttempt?.id ?? "none"}:${session.data?.tenant_id ?? ""}:${session.data?.org_id ?? ""}:${session.data?.id ?? ""}:${session.data?.role ?? ""}`}
        caseId={caseId}
        runId={runId}
        attemptId={selectedAttempt?.id ?? null}
        isFrozenAttempt={selectedAttempt?.history_kind === "frozen"}
        parsedDocumentIds={selectedAttempt?.parsed_document_ids ?? []}
        selection={quoteSelection}
        documents={review.data.documents}
        actor={session.data ?? null}
        canCreate={caseDetailReady && !caseArchived && roleCanDecide && selectedAttempt?.history_kind === "frozen"}
      />
      <EvidenceSLADashboard
        mode="reminder"
        cases={[{ id: caseId, title: caseDetail.data?.title ?? caseId }]}
      />
      <EvidenceRequestPanel
        caseId={caseId}
        runId={runId}
        attemptId={selectedAttempt?.id ?? null}
        canManage={caseDetailReady && !caseArchived && roleCanManageRun}
        evaluations={review.data.evaluations ?? []}
        agentReview={review.data.agent_review ?? null}
        documents={caseDetail.data?.documents ?? []}
        quoteScope={quoteScope}
        quoteSelection={quoteSelection}
      />
      <OperatorAuditEntry
        caseId={caseId}
        runId={runId}
        attemptId={selectedAttempt?.id ?? null}
      />
      <ExperienceFeedbackPanel
        caseId={caseId}
        runId={runId}
        attemptId={selectedAttempt?.id ?? null}
        finding={review.data.findings.find((item) => item.id === selectedId) ?? null}
        documents={review.data.documents}
        canSubmit={roleCanDecide && selectedAttempt?.history_kind === "frozen"}
        quoteScope={quoteScope}
        quoteSelection={quoteSelection}
      />
      <ExperienceUsagePanel
        caseId={caseId}
        runId={runId}
        attemptId={selectedAttempt?.history_kind === "frozen" ? selectedAttempt.id : null}
        findingId={selectedId}
        canDecide={canDecide}
      />
      <RunProvenance workspace={workspace} showDiagnostics={diagnosticData !== null} />
      <FrozenLegalAuthorities
        key={`${caseId}:${runId}:${selectedAttempt?.id ?? "none"}`}
        caseId={caseId}
        runId={runId}
        attemptId={selectedAttempt?.id ?? null}
        projection={review.data.legal_authorities}
      />
      <AuditProjection
        runId={review.data.run.id}
        events={diagnosticData?.audit_events ?? review.data.business_events ?? []}
        showDiagnostics={diagnosticData !== null}
      />
      {currentDiagnosticScope !== null && diagnosticScope === currentDiagnosticScope && diagnostics.isError ? (
        <p role="status">审查诊断不可用：{errorMessage(diagnostics.error)}</p>
      ) : null}
      {retry.isError ? <p role="alert">{errorMessage(retry.error)}</p> : null}
    </section>
  );
}
