// File metadata (manifest mirror):
// Purpose: Loads the selected run and immutable attempt into the review workspace, including findings, evidence citations, event stream and manual decisions; client rendering does not turn model advice into confirmed findings.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-05
// Revision: r8
// Copyright: See LICENSE
// End file metadata.

import { Button } from "antd";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Link, useLocation, useNavigate, useNavigationType, useParams, useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  errorMessage,
  isConflict,
  assertCommandContextCurrent,
  captureCommandContext,
  sessionIdentityFor,
  type CommandContext,
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
import { evidenceRequestsKey, listEvidenceRequests } from "../api/evidence-workflow";
import { caseOptionLabel } from "../domain/case-identity";
import type { CaseRelationInput, DecisionAction } from "../api/types";
import type { FrozenQuoteScope, FrozenQuoteSelection } from "../domain/frozen-quote";
import { PageStatus } from "../components/page-status";
import { ReviewWorkspaceView } from "../components/review-workspace";
import { ReviewWorkItems } from "../components/review-work-items";
import { DirtyLeaveGuard } from "../components/dirty-leave-guard";
import { isReadingInteraction, readCaseListReturn, readCaseReadingPosition, readReviewReadingPosition, reviewPositionForPath, SectionNavigationProvider, type ReviewReadingPosition } from "../components/section-navigation";
import { ReviewDiff } from "../components/review-diff";
import type { ReviewCaseContext } from "../components/document-stage";
import { RunProvenance } from "../components/run-provenance";
import { FrozenLegalAuthorities } from "../components/frozen-legal-authorities";
import { AuditProjection } from "../components/audit-projection";
import { EvidenceRequestPanel } from "../components/evidence-request-panel";
import { clearAllEvidenceDrafts, evidenceDraftScope, useEvidenceDraftDirty } from "../features/review/evidence-request-drafts";
import { actorReadContextKey } from "../domain/actor-read-context";
import { EvidenceSLADashboard } from "../components/evidence-sla-dashboard";
import { ExperienceFeedbackPanel } from "../components/experience-feedback-panel";
import { ManualFindingsPanel } from "../features/review/manual-findings";
import { ExperienceUsagePanel } from "../components/experience-usage-panel";
import { OperatorAuditEntry } from "../features/operator-audit/operator-audit-entry";
import {
  contractTypeLabel,
  caseHref,
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
import { OriginalSourceContext } from "../components/original-document-reader";
import { canRestartUserCancelled, hasTransientRetryCause } from "../features/review/review-outcome";
import { projectReviewWork, readWorkReturn, workReturnState, type WorkFilter, type WorkTarget, type WorkTargetKind } from "../features/review/work-items";

const readingPositions = new Map<string, ReviewReadingPosition>();
let previousReviewActor = "";

function captureReviewPosition(caseId: string, runId: string, attemptId: string | null, actorScope: string): ReviewReadingPosition {
  return {
    caseId, runId, attemptId, actorScope, scrollY: window.scrollY,
    documentScroll: document.querySelector<HTMLElement>(".page-review .col-doc")?.scrollTop ?? 0,
    findingScroll: document.querySelector<HTMLElement>(".page-review .col-findings")?.scrollTop ?? 0,
  };
}

export function ReviewPage() {
  const { caseId = "", runId = "" } = useParams();
  const location = useLocation();
  const navigate = useNavigate();
  const navigationType = useNavigationType();
  const [params, setParams] = useSearchParams();
  const selectedAttemptId = params.get("attempt_id");
  const [streamLive, setStreamLive] = useState(false);
  const [streamReconnectKey, setStreamReconnectKey] = useState(0);
  const review = useReview(caseId, runId, selectedAttemptId, streamLive);
  const workbench = useWorkbench("week");
  const createRelation = useCreateCaseRelation(caseId);
  const deleteRelation = useDeleteCaseRelation(caseId);
  const caseDetail = useCaseDetail(caseId);
  const session = useSession();
  const requests = useQuery({
    queryKey: evidenceRequestsKey(caseId),
    queryFn: ({ signal }) => listEvidenceRequests(caseId, signal),
    enabled: Boolean(caseId) && session.isSuccess && !session.isFetching && caseDetail.isSuccess && !caseDetail.isFetching,
    retry: false,
  });
  const [supplementOpen, setSupplementOpen] = useState(false);
  const [navigationSerial, setNavigationSerial] = useState(0);
  const actorScope = session.isSuccess && !session.isFetching ? actorReadContextKey(session.data) : "";
  const navigationActorScope = session.data ? actorReadContextKey(session.data) : "";
  const previousNavigationScope = useRef(navigationActorScope);
  const restoredReviewVisit = useRef("");
  const readingContent = useRef<HTMLDivElement>(null);
  const savedBeforeNavigation = useRef(new Set<string>());
  const pendingReviewRestore = useRef<{ visit: string; position: ReviewReadingPosition } | null>(null);
  const readingInteraction = useRef(0);
  const readingVisit = useRef<{ key: string; interaction: number }>({ key: "", interaction: 0 });
  const readingVisitKey = JSON.stringify([location.key, caseId, runId, selectedAttemptId, navigationActorScope]);
  const navigationActorChanged = Boolean(actorScope && previousNavigationScope.current && previousNavigationScope.current !== actorScope);
  useEffect(() => { if (actorScope) previousNavigationScope.current = actorScope; }, [actorScope]);
  const listReturn = readCaseListReturn(location.state, caseId, actorScope);
  const caseReadingPosition = readCaseReadingPosition(location.state, caseId, actorScope);
  const locationState = location.state && typeof location.state === "object" ? location.state as { actorScope?: unknown; workActorScope?: unknown; workReturn?: { actorScope?: unknown } } : null;
  const savedScope = locationState?.workActorScope ?? locationState?.workReturn?.actorScope ?? locationState?.actorScope;
  const scopeMismatch = navigationActorChanged || Boolean(actorScope && typeof savedScope === "string" && savedScope !== actorScope);
  const selectedCitation = scopeMismatch ? null : params.get("citation");
  useEffect(() => {
    if (!scopeMismatch) return;
    setParams((current) => {
      const next = new URLSearchParams(current);
      for (const key of ["work_kind", "work_item", "work_filter", "finding", "citation"]) next.delete(key);
      return next;
    }, { replace: true, state: null });
  }, [scopeMismatch, setParams]);
  const requestedKind = scopeMismatch ? null : params.get("work_kind");
  const requestedItem = scopeMismatch ? null : params.get("work_item");
  useEffect(() => {
    if (requestedItem && ["request", "evaluation", "request-source"].includes(requestedKind ?? "")) setSupplementOpen(true);
  }, [requestedItem, requestedKind, navigationSerial]);
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
  const streamSessionIdentity = session.data ? sessionIdentityFor(session.data) : null;
  const [streamCommandContext, setStreamCommandContext] = useState<CommandContext | null>(null);
  useEffect(() => {
    if (!session.data) {
      setStreamCommandContext(null);
      return;
    }
    try {
      setStreamCommandContext(captureCommandContext(session.data));
    } catch {
      setStreamCommandContext(null);
    }
  }, [session.data]);
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
            tenantId: session.data.active_context?.tenant_id,
            orgId: session.data.active_context?.org_id,
            contextVersion: session.data.active_context?.context_version,
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
      : decisionDraftUserPrefix(session.data.id, session.data.active_context?.context_version),
  );
  const evidenceDraftDirty = useEvidenceDraftDirty(evidenceDraftScope(
    session.data ? actorReadContextKey(session.data) : null, caseId, runId, selectedAttempt?.id ?? null,
  ));
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
  const currentWorkContext = session.isSuccess && !session.isFetching && caseDetailReady && !caseArchived && latestRun &&
    review.isSuccess && !review.isFetching && review.data.run.id === runId && review.data.run.case_id === caseId &&
    Boolean(caseId && runId) && selectedAttempt !== null && Boolean(selectedAttempt.id) && selectedAttempt.run_id === runId && selectedAttemptIsLatest &&
    (selectedAttemptId === null || selectedAttempt.id === selectedAttemptId) &&
    selectedAttempt.history_kind === "frozen" &&
    (isTerminalAttemptState(selectedAttempt.state) || isActiveAttemptState(selectedAttempt.state));
  const canDecide =
    currentWorkContext &&
    roleCanDecide &&
    selectedAttempt?.history_kind === "frozen" &&
    terminal;
  const retryableState =
    review.data !== undefined &&
    review.data.run.id === runId && review.data.run.case_id === caseId &&
    (selectedAttemptId === null || selectedAttempt?.id === selectedAttemptId) &&
    (hasTransientRetryCause(review.data) || canRestartUserCancelled(review.data));
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
      if (!streamCommandContext) return Promise.resolve();
      try { assertCommandContextCurrent(streamCommandContext); } catch { return Promise.resolve(); }
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
    [caseId, queryClient, runId, selectedAttemptId, streamCommandContext],
  );

  // Reconcile persisted state after recovery; a manually created EventSource
  // starts without the previous Last-Event-ID.
  const reconcileAfterReconnect = useCallback(() => {
    if (!streamCommandContext) return;
    try { assertCommandContextCurrent(streamCommandContext); } catch { return; }
    void queryClient.invalidateQueries({
      queryKey: queryKeys.review(caseId, runId, selectedAttemptId),
      exact: true,
    });
    void queryClient.invalidateQueries({
      queryKey: queryKeys.case(caseId),
      exact: true,
    });
  }, [caseId, queryClient, runId, selectedAttemptId, streamCommandContext]);

  const streamScope = session.data
    ? JSON.stringify([
        session.data.audience,
        session.data.id,
        session.data.role,
        session.data.tenant_id,
        session.data.org_id,
        session.data.active_context?.context_version,
        streamCommandContext?.epoch ?? null,
      ])
    : null;
  const streamContextMatchesSession = session.data !== undefined &&
    streamCommandContext?.identity === streamSessionIdentity;
  const streamActive = review.isSuccess && latestRun && selectedAttemptIsLatest && streamContextMatchesSession;
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

  useLayoutEffect(() => {
    if (isForbidden(review.error) || isUnauthenticated(review.error) ||
        isForbidden(caseDetail.error) || isUnauthenticated(caseDetail.error) ||
        isForbidden(requests.error) || isUnauthenticated(requests.error) ||
        (review.isSuccess && (review.data.run.id !== runId || review.data.run.case_id !== caseId ||
          (selectedAttemptId !== null && selectedAttempt?.id !== selectedAttemptId))) ||
        (caseDetail.isSuccess && caseDetail.data.id !== caseId)) clearAllEvidenceDrafts();
  }, [review.error, review.isSuccess, review.data, caseDetail.error, caseDetail.isSuccess, caseDetail.data,
    requests.error, caseId, runId, selectedAttemptId, selectedAttempt?.id]);

  useLayoutEffect(() => {
    if ((previousReviewActor && previousReviewActor !== navigationActorScope) || isForbidden(review.error) || isUnauthenticated(review.error) ||
        isForbidden(caseDetail.error) || isUnauthenticated(caseDetail.error) || isForbidden(requests.error) || isUnauthenticated(requests.error)) {
      readingPositions.clear();
      savedBeforeNavigation.current.clear();
      pendingReviewRestore.current = null;
    }
    previousReviewActor = navigationActorScope;
  }, [navigationActorScope, review.error, caseDetail.error, requests.error]);

  useEffect(() => {
    const stopRestoring = (event: Event) => {
      if (!isReadingInteraction(event, readingContent.current)) return;
      readingInteraction.current += 1;
      pendingReviewRestore.current = null;
    };
    document.addEventListener("pointerdown", stopRestoring, true);
    document.addEventListener("keydown", stopRestoring, true);
    document.addEventListener("wheel", stopRestoring, true);
    document.addEventListener("touchstart", stopRestoring, true);
    return () => {
      document.removeEventListener("pointerdown", stopRestoring, true);
      document.removeEventListener("keydown", stopRestoring, true);
      document.removeEventListener("wheel", stopRestoring, true);
      document.removeEventListener("touchstart", stopRestoring, true);
    };
  }, []);

  useLayoutEffect(() => {
    if (readingVisit.current.key !== readingVisitKey) {
      readingVisit.current = { key: readingVisitKey, interaction: readingInteraction.current };
    }
  }, [readingVisitKey]);

  useLayoutEffect(() => {
    if (!actorScope || !review.isSuccess || review.isFetching || !caseDetail.isSuccess || caseDetail.isFetching ||
        review.data.run.id !== runId || review.data.run.case_id !== caseId || caseDetail.data.id !== caseId ||
        (selectedAttemptId !== null && selectedAttempt?.id !== selectedAttemptId)) return;
    const attemptId = selectedAttempt?.id ?? null;
    const remembered = navigationType === "POP" ? readingPositions.get(location.key) : null;
    const fromState = readReviewReadingPosition(location.state, caseId, runId, attemptId, actorScope);
    const position = remembered?.actorScope === actorScope && remembered.caseId === caseId && remembered.runId === runId && remembered.attemptId === attemptId
      ? remembered : fromState;
    const visit = JSON.stringify([location.key, caseId, runId, attemptId, actorScope]);
    if (restoredReviewVisit.current !== visit) {
      restoredReviewVisit.current = visit;
      const uninterrupted = readingVisit.current.key === readingVisitKey && readingVisit.current.interaction === readingInteraction.current;
      pendingReviewRestore.current = position && !location.hash && uninterrupted ? { visit, position } : null;
    }
    const restore = pendingReviewRestore.current?.visit === visit ? pendingReviewRestore.current : null;
    let frame = 0;
    if (restore) {
      frame = requestAnimationFrame(() => {
        frame = requestAnimationFrame(() => {
          if (pendingReviewRestore.current !== restore) return;
          window.scrollTo(0, restore.position.scrollY);
          const documentPane = document.querySelector<HTMLElement>(".page-review .col-doc");
          const findingPane = document.querySelector<HTMLElement>(".page-review .col-findings");
          if (documentPane) documentPane.scrollTop = restore.position.documentScroll;
          if (findingPane) findingPane.scrollTop = restore.position.findingScroll;
        });
      });
    }
    return () => {
      cancelAnimationFrame(frame);
      if (savedBeforeNavigation.current.delete(location.key)) return;
      if (pendingReviewRestore.current?.visit === visit) return;
      if (!document.querySelector(".page-review .col-doc")) return;
      readingPositions.set(location.key, captureReviewPosition(caseId, runId, attemptId, actorScope));
      if (readingPositions.size > 30) readingPositions.delete(readingPositions.keys().next().value!);
    };
  }, [actorScope, caseId, runId, selectedAttemptId, selectedAttempt?.id, location.key, location.hash, location.state, navigationType, readingVisitKey,
    review.isSuccess, review.isFetching, review.data, caseDetail.isSuccess, caseDetail.isFetching, caseDetail.data]);

  if (session.isLoading) return <PageStatus kind="loading" title="正在确认登录状态" />;
  if (!session.isSuccess) return <PageStatus kind="denied" title="无法确认当前登录身份" />;
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
  if (review.data.run.id !== runId || review.data.run.case_id !== caseId ||
      (selectedAttemptId !== null && selectedAttempt?.id !== selectedAttemptId)) {
    return <PageStatus kind="error" title="审查身份无法确认" detail="服务端返回的审查或执行记录与当前页面不一致，请返回案件重新选择。" />;
  }
  if (caseDetail.isError && (isForbidden(caseDetail.error) || isUnauthenticated(caseDetail.error))) {
    return <PageStatus kind="denied" title="没有权限查看这个案件" detail={errorMessage(caseDetail.error)} />;
  }

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
    (scopeMismatch ? null : params.get("finding")) ?? (requestedKind === "finding" ? requestedItem : null),
  );
  const workFilter: WorkFilter = params.get("work_filter") === "evidence" ? "evidence"
    : params.get("work_filter") === "remediation" ? "remediation" : "judgment";
  const deniedRequests = isForbidden(requests.error) || isUnauthenticated(requests.error);
  const activeReturn = readWorkReturn(location.state, caseId, actorScope);
  const activeReturnPosition = activeReturn ? reviewPositionForPath(location.state, activeReturn, caseId, actorScope) : null;
  const caseNavigationState = {
    ...(listReturn ?? {}),
    ...(caseReadingPosition ? { caseReadingPosition } : {}),
  };
  const navigationState = {
    ...caseNavigationState,
    ...(activeReturn ? workReturnState(activeReturn, caseId, actorScope) : null),
    ...(activeReturnPosition ? { reviewReadingPosition: activeReturnPosition } : null),
    workActorScope: actorScope,
  };
  const selectedFrozen = Boolean(caseId && runId && selectedAttempt?.id && selectedAttempt.history_kind === "frozen" && selectedAttempt.run_id === runId);
  const targetKinds: WorkTargetKind[] = ["finding", "gap", "evaluation", "request-source", "request"];
  const targetKind = targetKinds.find((kind) => kind === requestedKind);
  const matchingTarget = requestedItem && targetKind && !scopeMismatch && !deniedRequests && session.isSuccess && !session.isFetching
    ? targetKind === "finding" ? selectedFrozen && workspace.findings.filter((item) => item.id === requestedItem).length === 1
      : targetKind === "gap" ? selectedFrozen && workspace.agent_review?.attempt_id === selectedAttempt?.id &&
        (workspace.agent_review?.gaps ?? []).filter((item) => item.gap_id === requestedItem).length === 1
        : targetKind === "evaluation" ? selectedFrozen && (workspace.evaluations ?? []).filter((item) => item.id === requestedItem).length === 1
          : targetKind === "request" ? requests.data?.filter((item) => item.case_id === caseId && item.id === requestedItem).length === 1
            : selectedFrozen && requestedItem.startsWith("gap:") && workspace.agent_review?.attempt_id === selectedAttempt?.id &&
              (workspace.agent_review?.gaps ?? []).filter((item) => item.gap_id === requestedItem.slice(4)).length === 1
    : false;
  const workTarget: WorkTarget | null = matchingTarget && requestedItem && targetKind
    ? { kind: targetKind, id: requestedItem, runId, attemptId: selectedAttempt?.id ?? "" } : null;
  const workProjection = projectReviewWork(workspace, caseDetail.isSuccess ? caseDetail.data : undefined, deniedRequests ? undefined : requests.data);
  const workError = requests.isError && !deniedRequests ? `补件请求读取失败：${errorMessage(requests.error)}`
    : caseDetail.isError ? `案件状态读取失败：${errorMessage(caseDetail.error)}` : null;
  const canPrepareRequest = currentWorkContext && roleCanManageRun && !deniedRequests;
  const canContinueRequest = session.isSuccess && !session.isFetching && caseDetailReady && !caseArchived &&
    roleCanManageRun && Boolean(caseDetail.data.latest_run) && (!latestRun || !selectedAttemptIsLatest);

  function savedReturn(target: WorkTarget) {
    const next = new URLSearchParams(params);
    if (selectedAttempt?.id) next.set("attempt_id", selectedAttempt.id);
    next.set("work_kind", target.kind);
    next.set("work_item", target.id);
    if (target.kind === "finding") next.set("finding", target.id);
    return {
      ...caseNavigationState,
      ...workReturnState(`${location.pathname}?${next}`, caseId, actorScope),
      reviewReadingPosition: captureReviewPosition(caseId, runId, selectedAttempt?.id ?? null, actorScope),
    };
  }

  function openWork(target: WorkTarget) {
    if (!actorScope || scopeMismatch) return;
    if (target.kind === "remediation") {
      const next = new URLSearchParams({ work_kind: "remediation", work_item: target.id });
      void navigate(`/cases/${encodeURIComponent(caseId)}?${next}`, { state: savedReturn(target) });
      return;
    }
    if (target.kind !== "request" && (target.runId !== runId || target.attemptId !== selectedAttempt?.id)) {
      const next = new URLSearchParams({ attempt_id: target.attemptId, work_filter: "evidence", work_kind: target.kind, work_item: target.id });
      void navigate(`/cases/${encodeURIComponent(caseId)}/reviews/${encodeURIComponent(target.runId)}?${next}`, { state: savedReturn(target) });
      return;
    }
    if (target.kind === "request" || target.kind === "evaluation" || target.kind === "request-source") setSupplementOpen(true);
    setNavigationSerial((value) => value + 1);
    setParams((current) => {
      const next = new URLSearchParams(current);
      next.set("work_kind", target.kind);
      next.set("work_item", target.id);
      if (target.kind === "finding") next.set("finding", target.id);
      return next;
    }, { state: navigationState });
  }

  function continueRequest(target: WorkTarget) {
    const latest = caseDetail.data?.latest_run;
    if (!canContinueRequest || !latest) return;
    const next = new URLSearchParams({ work_filter: "evidence", work_kind: "request", work_item: target.id });
    void navigate(`${caseHref(caseId, latest.id)}?${next}`, { state: savedReturn(target) });
  }
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
          .map((item) => ({ id: item.id, title: caseOptionLabel(item, snapshot.members) })),
      }
    : null;
  const canEditRelations = currentWorkContext && roleCanManageRun;
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
    <SectionNavigationProvider scope={`${caseId}:${runId}:${selectedAttempt?.id ?? selectedAttemptId ?? ""}:${navigationActorScope}`} actorScope={navigationActorScope} ready={Boolean(actorScope && review.isSuccess && !review.isFetching && caseDetailReady && review.data.run.id === runId && review.data.run.case_id === caseId && caseDetail.data.id === caseId)}>
    <section className="page page-review">
      {session.isFetching ? <p role="status">正在重新确认登录状态，办理暂不可用。</p> : null}
      <div ref={readingContent} hidden={session.isFetching}>
      <header className="page-head">
        <div>
          <h1>审查工作区</h1>
          <p>{caseDetail.data?.title ?? "案件"}</p>
        </div>
        {activeReturn ? <Link to={activeReturn} state={{ ...caseNavigationState, reviewReadingPosition: activeReturnPosition }}>返回人工办理</Link> : null}
        <Button
          aria-label={"Agent Studio"}
          href={`/cases/${encodeURIComponent(caseId)}/reviews/${encodeURIComponent(runId)}/agent${
            selectedAttempt?.id
              ? `?attempt=${encodeURIComponent(selectedAttempt.id)}`
              : selectedAttemptId
                ? `?attempt=${encodeURIComponent(selectedAttemptId)}`
                : ""
          }`}
        >
          Agent Studio
        </Button>
        <Button
          aria-label={"返回案件"}
          href={`/cases/${encodeURIComponent(caseId)}`}
          onClick={(event) => {
            if (event.button !== 0 || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
            event.preventDefault();
            void navigate(`/cases/${encodeURIComponent(caseId)}`, { state: caseNavigationState });
          }}
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
          actorScope={JSON.stringify([session.data.id, session.data.active_context?.tenant_id, session.data.active_context?.org_id, session.data.role, session.data.audience, session.data.active_context?.context_version])}
        />
      ) : null}
      {session.isSuccess && !session.isFetching && !scopeMismatch && !deniedRequests ? (
        <ReviewWorkItems
          projection={workProjection}
          filter={workFilter}
          onFilter={(filter) => setParams((current) => {
            const next = new URLSearchParams(current);
            next.set("work_filter", filter);
            return next;
          }, { state: navigationState })}
          onOpen={openWork}
          onContinueRequest={canContinueRequest ? continueRequest : undefined}
          refreshing={requests.isFetching || caseDetail.isFetching || review.isFetching}
          error={workError}
          canPrepareRequest={canPrepareRequest}
          currentRunId={runId}
          currentAttemptId={selectedAttempt?.id ?? null}
          currentOrdinal={selectedAttempt?.ordinal ?? null}
          selectedKind={requestedKind}
          selectedId={requestedItem}
        />
      ) : deniedRequests ? <p role="alert">无法读取补件请求，办理导航暂不可用。</p> : null}
      {requestedItem && targetKind && !workTarget && !requests.isFetching && !scopeMismatch && !deniedRequests ?
        <p role="alert">所选办理事项不属于当前执行或身份不唯一，请返回办理列表核对。</p> : null}
      <DecisionDraftScopeProvider scope={decisionDraftScope}>
        <OriginalSourceContext.Provider value={caseDetailReady && session.isSuccess && !session.isFetching ? {
          caseId,
          contextKey: `${actorReadContextKey(session.data)}:${selectedAttempt?.id ?? "legacy"}`,
          digests: new Map(caseDetail.data.documents.map((document) => [document.id, document.digest])),
        } : null}>
        <ReviewWorkspaceView
          workspace={workspace}
          layout={layout}
          showDiagnostics={diagnosticData !== null}
          quoteScope={quoteScope}
          onQuoteSelectionChange={setQuoteSelection}
          selectedId={selectedId}
          workTarget={workTarget}
          navigationSerial={navigationSerial}
          caseContext={caseContext}
          caseReturnState={caseNavigationState}
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
              if (next.get("work_kind") === "finding") next.set("work_item", findingId);
              return next;
            }, { state: navigationState });
          }}
          onSelectAttempt={(attemptId) => {
            const next = new URLSearchParams(params);
            next.set("attempt_id", attemptId);
            // A citation belongs to its own attempt; never show the previous
            // attempt's evidence after switching.
            next.delete("citation");
            next.delete("finding");
            next.delete("work_kind");
            next.delete("work_item");
            setParams(next, { state: navigationState });
          }}
          selectedEvidenceId={selectedCitation}
          onSelectEvidence={(evidenceId) => {
            if (actorScope) {
              readingPositions.set(location.key, captureReviewPosition(caseId, runId, selectedAttempt?.id ?? null, actorScope));
              savedBeforeNavigation.current.add(location.key);
            }
            const next = new URLSearchParams(params);
            if (selectedAttempt?.id) next.set("attempt_id", selectedAttempt.id);
            if (evidenceId === null) {
              next.delete("citation");
            } else {
              next.set("citation", evidenceId);
            }
            setParams(next, { state: navigationState });
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
                next.delete("work_kind");
                next.delete("work_item");
                setParams(next, { state: navigationState });
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
        </OriginalSourceContext.Provider>
      </DecisionDraftScopeProvider>
      <DirtyLeaveGuard dirty={decisionDraftsDirty || evidenceDraftDirty} />
      <details className="review-secondary-section" open={supplementOpen} onToggle={(event) => setSupplementOpen(event.currentTarget.open)}>
        <summary>补充材料与人工处理</summary>
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
          canCreate={currentWorkContext && roleCanDecide && selectedAttempt?.history_kind === "frozen"}
        />
        <EvidenceSLADashboard
          mode="reminder"
          cases={[{ id: caseId, title: caseDetail.data?.title ?? caseId }]}
        />
        <EvidenceRequestPanel
          caseId={caseId}
          runId={runId}
          attemptId={selectedAttempt?.id ?? null}
          canManage={canPrepareRequest}
          readOnly={!currentWorkContext}
          selectedRequestId={workTarget?.kind === "request" ? workTarget.id : null}
          selectedSource={workTarget?.kind === "evaluation" ? `evaluation:${workTarget.id}` : workTarget?.kind === "request-source" ? workTarget.id : null}
          navigationSerial={navigationSerial}
          expanded={supplementOpen}
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
      </details>
      <details className="review-secondary-section">
        <summary>经验、审查依据与历史</summary>
        <ExperienceFeedbackPanel
          caseId={caseId}
          runId={runId}
          attemptId={selectedAttempt?.id ?? null}
          finding={review.data.findings.find((item) => item.id === selectedId) ?? null}
          documents={review.data.documents}
          canSubmit={currentWorkContext && roleCanDecide && selectedAttempt?.history_kind === "frozen"}
          contextScope={streamCommandContext?.contextVersion}
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
      </details>
      {currentDiagnosticScope !== null && diagnosticScope === currentDiagnosticScope && diagnostics.isError ? (
        <p role="status">审查诊断不可用：{errorMessage(diagnostics.error)}</p>
      ) : null}
      {retry.isError ? <p role="alert">{errorMessage(retry.error)}</p> : null}
      </div>
    </section>
    </SectionNavigationProvider>
  );
}
