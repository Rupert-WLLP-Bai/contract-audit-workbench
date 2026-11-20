// File metadata (manifest mirror):
// Purpose: Coordinates review of the selected execution, including judgment and report actions, readable history and conditional access to technical presentation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-20
// Revision: r19
// Copyright: See LICENSE
// End file metadata.

import { ReviewProgress } from "../components/review-progress";
import { useProfessionalMode } from "../components/app-shell-preferences";
import { Button, Drawer, Tabs } from "antd";
import {
  cloneElement,
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  Link,
  useLocation,
  useNavigate,
  useNavigationType,
  useParams,
  useSearchParams,
} from "react-router";
import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { listMaterialParsedDocuments } from "../api/endpoints";
import { frozenSourceProof } from "../domain/frozen-source-proof";
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
  useCreateReport,
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
import type {
  CaseRelationInput,
  DecisionAction,
  MaterialProjection,
  ReviewDocument,
} from "../api/types";
import type { FrozenQuoteScope, FrozenQuoteSelection } from "../domain/frozen-quote";
import { PageStatus } from "../components/page-status";
import { ReviewWorkspaceView } from "../components/review-workspace";
import { ReportPreflight, useReportPreflight } from "../components/report-preflight";
import { ReviewWorkItems } from "../components/review-work-items";
import { DirtyLeaveGuard } from "../components/dirty-leave-guard";

const DocxWorkspace = lazy(() => import("../components/docx-workspace"));
import {
  isReadingInteraction,
  readCaseListReturn,
  readCaseReadingPosition,
  readReviewReadingPosition,
  reviewPositionForPath,
  SectionNavigationProvider,
  type ReviewReadingPosition,
} from "../components/section-navigation";
import { ReviewDiff } from "../components/review-diff";
import { ReviewCaseContextPanel, type ReviewCaseContext } from "../components/document-stage";
import { RunProvenance } from "../components/run-provenance";
import { FrozenLegalAuthorities } from "../components/frozen-legal-authorities";
import { AuditProjection } from "../components/audit-projection";
import { EvidenceRequestPanel } from "../components/evidence-request-panel";
import {
  clearAllEvidenceDrafts,
  evidenceDraftScope,
  useEvidenceDraftDirty,
} from "../features/review/evidence-request-drafts";
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
  decisionLabel,
  type FindingDecisionInput,
  type FindingDecisionOutcome,
} from "../domain/review";
import { formatDateTime } from "../domain/time";
import { useReviewEvents, type ReviewEventBatch } from "../hooks/use-review-events";
import {
  DecisionDraftScopeProvider,
  decisionDraftUserPrefix,
  useDecisionDraftsDirty,
} from "../hooks/decision-drafts";
import { useReviewLayout } from "../hooks/use-viewport-layout";
import { OriginalSourceContext } from "../components/original-document-reader";
import { isLegacyPaymentCoverageFinding } from "../features/review/single-contract-coverage";
import {
  canRestartUserCancelled,
  hasTransientRetryCause,
  partialReportScope,
} from "../features/review/review-outcome";
import {
  projectReviewWork,
  readWorkReturn,
  workReturnState,
  type WorkFilter,
  type WorkTarget,
  type WorkTargetKind,
  legacyReviewDrawer,
  reviewRecordTabs,
  workTargetDrawer,
  writeReviewDrawer,
  type ReviewDrawer,
  type ReviewRecordTab,
} from "../features/review/work-items";
import "./review-page.css";

interface ContractDeskReturn {
  actorScope: string;
  returnTo: string;
  documentId: string;
  previousHref: string | null;
  nextHref: string | null;
}
function safeContractDeskHref(value: unknown): string | null {
  if (typeof value !== "string" || !value.startsWith("/") || value.startsWith("//")) return null;
  try {
    const target = new URL(value, "https://local.invalid");
    return target.origin === "https://local.invalid" && target.pathname === "/contract-desk"
      ? `${target.pathname}${target.search}${target.hash}`
      : null;
  } catch {
    return null;
  }
}
function readContractDeskReturn(
  state: unknown,
  actorScope: string,
  sourceDocument: string | null,
): ContractDeskReturn | null {
  if (!state || typeof state !== "object" || !("contractDesk" in state)) return null;
  const value = (state as { contractDesk?: unknown }).contractDesk;
  if (!value || typeof value !== "object") return null;
  const entry = value as Record<string, unknown>;
  const returnTo = safeContractDeskHref(entry.returnTo);
  if (
    !actorScope ||
    entry.actorScope !== actorScope ||
    !sourceDocument ||
    entry.documentId !== sourceDocument ||
    !returnTo
  )
    return null;
  return {
    actorScope,
    returnTo,
    documentId: sourceDocument,
    previousHref: safeContractDeskHref(entry.previousHref),
    nextHref: safeContractDeskHref(entry.nextHref),
  };
}

const readingPositions = new Map<string, ReviewReadingPosition>();
let previousReviewActor = "";

function reviewDocumentPanes(): NodeListOf<HTMLElement> {
  return document.querySelectorAll(".page-review [data-review-document-scroll]");
}

function findingQueue(): HTMLElement | null {
  return document.querySelector(".page-review .review-finding-queue");
}

function captureReviewPosition(
  caseId: string,
  runId: string,
  attemptId: string | null,
  actorScope: string,
): ReviewReadingPosition {
  const panes = reviewDocumentPanes();
  return {
    caseId,
    runId,
    attemptId,
    actorScope,
    scrollY: window.scrollY,
    documentScroll: panes[0]?.scrollTop ?? 0,
    secondDocumentScroll: panes[1]?.scrollTop,
    findingScroll: findingQueue()?.scrollTop ?? 0,
  };
}

const reviewSectionTargets = {
  document: "review-document-section",
  work: "review-work-section",
  coverage: "review-coverage-section",
  trace: "review-trajectory",
  diff: "review-diff",
  history: "review-history-section",
} as const;
type ReviewSectionKey = keyof typeof reviewSectionTargets;

function sectionForHash(hash: string): ReviewSectionKey | null {
  const id = hash.startsWith("#") ? hash.slice(1) : hash;
  if (id === "agent-review-gaps" || id.startsWith("agent-review")) return "coverage";
  if (id === "review-trajectory" || id === "execution-inspector") return "trace";
  if (id === "review-diff" || id === "review-diff-section") return "diff";
  if (id === "review-work-section") return "work";
  if (id === "review-history-section") return "history";
  if (id === "review-coverage-section") return "coverage";
  if (id === "review-document-section") return "document";
  return null;
}

const reviewDecisionLabels: Partial<Record<DecisionAction, string>> = {
  confirm_risk: "确认是风险",
  false_positive: "不构成风险",
  insufficient_evidence: "依据不够",
};

function isFindingCitationRejection(error: unknown): boolean {
  return (
    isConflict(error) && errorMessage(error).includes("模型建议的引用未全部对上本次审查留档原文")
  );
}

function drawerTargetPending(id: string) {
  return sectionForHash(`#${id}`) !== null;
}

export function ReviewPage() {
  const { caseId = "", runId = "" } = useParams();
  const location = useLocation();
  const navigate = useNavigate();
  const navigationType = useNavigationType();
  const [params, setParams] = useSearchParams();
  const { enabled: professional } = useProfessionalMode();
  const requestedDrawer = params.get("drawer");
  const requestedLegacySection =
    params.get("review_section") ??
    (requestedDrawer === null ? sectionForHash(location.hash) : null);
  const requestedTab = params.get("tab");
  const [legacyDrawer, legacyTab] = legacyReviewDrawer(requestedLegacySection);
  const requestedSurface =
    requestedDrawer === "record" || requestedDrawer === "execution"
      ? requestedDrawer
      : legacyDrawer;
  const concealedExecution = requestedSurface === "execution" && !professional;
  const drawer = concealedExecution ? "record" : requestedSurface;
  const recordTab = concealedExecution
    ? "review"
    : (reviewRecordTabs.find((tab) => tab === requestedTab) ?? legacyTab ?? "finding");
  function selectDrawer(nextDrawer: ReviewDrawer | null, tab?: ReviewRecordTab) {
    if (nextDrawer === "execution" && !professional) return;
    setParams((current) => writeReviewDrawer(new URLSearchParams(current), nextDrawer, tab), {
      state: location.state,
    });
  }
  useEffect(() => {
    if (requestedLegacySection === null && !concealedExecution) return;
    const next = writeReviewDrawer(
      new URLSearchParams(location.search),
      drawer,
      drawer === "record" ? recordTab : undefined,
    );
    const search = next.toString();
    void navigate(
      {
        pathname: location.pathname,
        search: search ? `?${search}` : "",
        hash: drawer && !concealedExecution ? location.hash : "",
      },
      { replace: true, state: location.state },
    );
  }, [
    concealedExecution,
    drawer,
    location.pathname,
    location.search,
    location.hash,
    location.state,
    navigate,
    recordTab,
    requestedLegacySection,
  ]);
  const selectedAttemptId = params.get("attempt_id");
  const requestedSourceDocument = params.get("source_document");
  const hasRequestedSourceDocument = params.has("source_document");
  const [streamLive, setStreamLive] = useState(false);
  const [streamReconnectKey, setStreamReconnectKey] = useState(0);
  const review = useReview(caseId, runId, selectedAttemptId, streamLive);
  const workbench = useWorkbench("week");
  const createRelation = useCreateCaseRelation(caseId);
  const createReport = useCreateReport(caseId);
  const [submittedReportScope, setSubmittedReportScope] = useState<string | null>(null);
  const deleteRelation = useDeleteCaseRelation(caseId);
  const caseDetail = useCaseDetail(caseId);
  const session = useSession();
  const requests = useQuery({
    queryKey: evidenceRequestsKey(caseId),
    queryFn: ({ signal }) => listEvidenceRequests(caseId, signal),
    enabled:
      Boolean(caseId) &&
      session.isSuccess &&
      !session.isFetching &&
      caseDetail.isSuccess &&
      !caseDetail.isFetching,
    retry: false,
  });

  const [navigationSerial, setNavigationSerial] = useState(0);
  const [copyStatus, setCopyStatus] = useState("");
  const focusJudgmentOnClose = useRef(false);
  const [coverageHost, setCoverageHost] = useState<HTMLDivElement | null>(null);
  const actorScope =
    session.isSuccess && !session.isFetching ? actorReadContextKey(session.data) : "";
  const navigationActorScope = session.data ? actorReadContextKey(session.data) : "";
  const previousNavigationScope = useRef(navigationActorScope);
  const restoredReviewVisit = useRef("");
  const readingContent = useRef<HTMLDivElement>(null);
  const savedBeforeNavigation = useRef(new Set<string>());
  const pendingReviewRestore = useRef<{ visit: string; position: ReviewReadingPosition } | null>(
    null,
  );
  const readingInteraction = useRef(0);
  const readingVisit = useRef<{ key: string; interaction: number }>({ key: "", interaction: 0 });
  const readingVisitKey = JSON.stringify([
    location.key,
    caseId,
    runId,
    selectedAttemptId,
    navigationActorScope,
  ]);
  const navigationActorChanged = Boolean(
    actorScope && previousNavigationScope.current && previousNavigationScope.current !== actorScope,
  );
  useEffect(() => {
    if (actorScope) previousNavigationScope.current = actorScope;
  }, [actorScope]);
  const listReturn = readCaseListReturn(location.state, caseId, actorScope);
  const caseReadingPosition = readCaseReadingPosition(location.state, caseId, actorScope);
  const locationState =
    location.state && typeof location.state === "object"
      ? (location.state as {
          actorScope?: unknown;
          workActorScope?: unknown;
          workReturn?: { actorScope?: unknown };
        })
      : null;
  const savedScope =
    locationState?.workActorScope ??
    locationState?.workReturn?.actorScope ??
    locationState?.actorScope;
  const scopeMismatch =
    navigationActorChanged ||
    Boolean(actorScope && typeof savedScope === "string" && savedScope !== actorScope);
  const selectedCitation = scopeMismatch ? null : params.get("citation");
  useEffect(() => {
    if (!scopeMismatch) return;
    setParams(
      (current) => {
        const next = new URLSearchParams(current);
        for (const key of [
          "work_kind",
          "work_item",
          "work_filter",
          "finding",
          "citation",
          "drawer",
          "tab",
        ])
          next.delete(key);
        return next;
      },
      { replace: true, state: null },
    );
  }, [scopeMismatch, setParams]);
  const requestedKind = scopeMismatch ? null : params.get("work_kind");
  const requestedItem = scopeMismatch ? null : params.get("work_item");
  const [diagnosticScope, setDiagnosticScope] = useState<string | null>(null);
  const currentDiagnosticScope =
    professional && session.data?.role === "admin"
      ? [caseId, runId, actorReadContextKey(session.data)].join("\u0000")
      : null;
  const diagnostics = useReviewDiagnostics(
    caseId,
    runId,
    selectedAttemptId,
    currentDiagnosticScope !== null &&
      diagnosticScope === currentDiagnosticScope &&
      review.isSuccess,
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
  const [docxSession, setDocxSession] = useState<{
    document: ReviewDocument;
    material: MaterialProjection;
    actorScope: string;
  } | null>(null);
  const [docxDirty, setDocxDirty] = useState(false);
  const onDocxDirty = useCallback((dirty: boolean) => setDocxDirty(dirty), []);
  useEffect(() => {
    if (docxSession && navigationActorScope !== docxSession.actorScope) {
      setDocxSession(null);
      setDocxDirty(false);
    }
  }, [docxSession, navigationActorScope]);
  const [conflictMessage, setConflictMessage] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [conflictFinding, setConflictFinding] = useState<string | null>(null);
  const [quoteSelection, setQuoteSelection] = useState<FrozenQuoteSelection | null>(null);

  const execution = review.data?.review_attempts;
  const selectedAttempt = execution?.attempt ?? null;
  const proofEligible = Boolean(
    actorScope &&
    caseDetail.isSuccess &&
    !caseDetail.isFetching &&
    caseDetail.data.id === caseId &&
    review.isSuccess &&
    !review.isFetching &&
    review.data.run.id === runId &&
    review.data.run.case_id === caseId &&
    selectedAttempt?.run_id === runId &&
    selectedAttempt.history_kind === "frozen" &&
    (!selectedAttemptId || selectedAttemptId === selectedAttempt.id),
  );
  const historyTargets = proofEligible
    ? (review.data?.documents ?? []).flatMap((document) => {
        const parsedId = document.parsed_document_id;
        const original = caseDetail.data?.documents.filter((item) => item.id === document.id) ?? [];
        if (
          !parsedId ||
          !selectedAttempt?.parsed_document_ids.includes(parsedId) ||
          original.length !== 1 ||
          original[0]?.parsed_document?.id === parsedId
        )
          return [];
        const versions = (caseDetail.data?.material_versions ?? []).filter(
          (item) => item.document_id === document.id,
        );
        return versions.length === 1 ? [{ documentId: document.id, ...versions[0]! }] : [];
      })
    : [];
  const historyQueries = useQueries({
    queries: historyTargets.map((target) => ({
      queryKey: [
        "frozen-source-proof",
        actorScope,
        caseId,
        runId,
        selectedAttempt?.id,
        target.material_id,
        target.version,
      ],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        listMaterialParsedDocuments(caseId, target.material_id, target.version, signal),
      retry: false,
    })),
  });
  const sourceProofs = proofEligible
    ? (review.data?.documents ?? []).flatMap((document) => {
        const targetIndex = historyTargets.findIndex((target) => target.documentId === document.id);
        const query = historyQueries[targetIndex];
        const history = query?.isSuccess && !query.isFetching ? query.data : undefined;
        const proof = frozenSourceProof(
          caseDetail.data,
          document,
          selectedAttempt?.parsed_document_ids ?? [],
          history,
        );
        return proof ? [proof] : [];
      })
    : [];
  const quoteScope: FrozenQuoteScope = {
    caseId,
    runId,
    attemptId: selectedAttempt?.id ?? null,
    isFrozenAttempt: proofEligible,
    parsedDocumentIds: sourceProofs.map((proof) => proof.parsedDocumentId),
    sourceProofs,
    sourceFailureMessages: Object.fromEntries(
      (review.data?.documents ?? []).flatMap((document) => {
        if (sourceProofs.some((proof) => proof.documentId === document.id)) return [];
        const targetIndex = historyTargets.findIndex((target) => target.documentId === document.id);
        const query = historyQueries[targetIndex];
        const reason =
          query?.isPending || query?.isFetching
            ? "正在核验送审材料的解析版本，请稍候。"
            : query?.isError
              ? "无法读取送审材料的解析版本，请检查材料访问权限后重试。"
              : "无法核验送审材料的解析版本、材料映射或原件摘要，不能定位。";
        return [[document.id, reason]];
      }),
    ),
  };
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
  const reportCommandScope = JSON.stringify([
    navigationActorScope,
    caseId,
    runId,
    selectedAttempt?.id,
  ]);
  const decisionDraftsDirty = useDecisionDraftsDirty(
    session.data === undefined
      ? null
      : decisionDraftUserPrefix(session.data.id, session.data.active_context?.context_version),
  );
  const evidenceDraftDirty = useEvidenceDraftDirty(
    evidenceDraftScope(
      session.data ? actorReadContextKey(session.data) : null,
      caseId,
      runId,
      selectedAttempt?.id ?? null,
    ),
  );
  const latestAttempt = execution
    ? execution.attempts.reduce(
        (latest, attempt) => (attempt.ordinal > latest.ordinal ? attempt : latest),
        execution.attempt,
      )
    : null;
  const selectedAttemptIsLatest = execution === null || latestAttempt?.id === selectedAttempt?.id;
  const caseDetailReady = caseDetail.isSuccess && !caseDetail.isFetching;
  const caseArchived = caseDetailReady && Boolean(caseDetail.data.archived_at);
  // A background permission refresh disables writes without disconnecting SSE.
  const latestRun = caseDetail.isSuccess && caseDetail.data.latest_run?.id === runId;
  const roleCanManageRun =
    session.data?.role === "admin" ||
    session.data?.role === "reviewer" ||
    session.data?.role === "clerk";
  const roleCanDecide = session.data?.role === "admin" || session.data?.role === "reviewer";
  const decisionState = selectedAttempt?.state ?? review.data?.run.state ?? "unknown";
  const terminal = isTerminalAttemptState(decisionState);
  const currentWorkContext =
    session.isSuccess &&
    !session.isFetching &&
    caseDetailReady &&
    !caseArchived &&
    latestRun &&
    review.isSuccess &&
    !review.isFetching &&
    review.data.run.id === runId &&
    review.data.run.case_id === caseId &&
    Boolean(caseId && runId) &&
    selectedAttempt !== null &&
    Boolean(selectedAttempt.id) &&
    selectedAttempt.run_id === runId &&
    selectedAttemptIsLatest &&
    (selectedAttemptId === null || selectedAttempt.id === selectedAttemptId) &&
    selectedAttempt.history_kind === "frozen" &&
    (isTerminalAttemptState(selectedAttempt.state) || isActiveAttemptState(selectedAttempt.state));
  const canDecide =
    currentWorkContext && roleCanDecide && selectedAttempt?.history_kind === "frozen" && terminal;
  const retryableState =
    review.data !== undefined &&
    review.data.run.id === runId &&
    review.data.run.case_id === caseId &&
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
  const reportPreflight = useReportPreflight(
    currentWorkContext && !scopeMismatch && !review.isFetching ? reportCommandScope : "",
  );
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
      try {
        assertCommandContextCurrent(streamCommandContext);
      } catch {
        return Promise.resolve();
      }
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
    try {
      assertCommandContextCurrent(streamCommandContext);
    } catch {
      return;
    }
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
  const streamContextMatchesSession =
    session.data !== undefined && streamCommandContext?.identity === streamSessionIdentity;
  const streamActive =
    review.isSuccess && latestRun && selectedAttemptIsLatest && streamContextMatchesSession;
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
      if (isUnauthenticated(queryClient.getQueryState(queryKeys.session)?.error)) {
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
    if (
      isForbidden(review.error) ||
      isUnauthenticated(review.error) ||
      isForbidden(caseDetail.error) ||
      isUnauthenticated(caseDetail.error) ||
      isForbidden(requests.error) ||
      isUnauthenticated(requests.error) ||
      (review.isSuccess &&
        (review.data.run.id !== runId ||
          review.data.run.case_id !== caseId ||
          (selectedAttemptId !== null && selectedAttempt?.id !== selectedAttemptId))) ||
      (caseDetail.isSuccess && caseDetail.data.id !== caseId)
    )
      clearAllEvidenceDrafts();
  }, [
    review.error,
    review.isSuccess,
    review.data,
    caseDetail.error,
    caseDetail.isSuccess,
    caseDetail.data,
    requests.error,
    caseId,
    runId,
    selectedAttemptId,
    selectedAttempt?.id,
  ]);

  useLayoutEffect(() => {
    if (
      (previousReviewActor && previousReviewActor !== navigationActorScope) ||
      isForbidden(review.error) ||
      isUnauthenticated(review.error) ||
      isForbidden(caseDetail.error) ||
      isUnauthenticated(caseDetail.error) ||
      isForbidden(requests.error) ||
      isUnauthenticated(requests.error)
    ) {
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
    if (
      !actorScope ||
      !review.isSuccess ||
      review.isFetching ||
      !caseDetail.isSuccess ||
      caseDetail.isFetching ||
      review.data.run.id !== runId ||
      review.data.run.case_id !== caseId ||
      caseDetail.data.id !== caseId ||
      (selectedAttemptId !== null && selectedAttempt?.id !== selectedAttemptId)
    )
      return;
    const attemptId = selectedAttempt?.id ?? null;
    const remembered = navigationType === "POP" ? readingPositions.get(location.key) : null;
    const fromState = readReviewReadingPosition(
      location.state,
      caseId,
      runId,
      attemptId,
      actorScope,
    );
    const position =
      remembered?.actorScope === actorScope &&
      remembered.caseId === caseId &&
      remembered.runId === runId &&
      remembered.attemptId === attemptId
        ? remembered
        : fromState;
    const visit = JSON.stringify([location.key, caseId, runId, attemptId, actorScope]);
    if (restoredReviewVisit.current !== visit) {
      restoredReviewVisit.current = visit;
      const uninterrupted =
        readingVisit.current.key === readingVisitKey &&
        readingVisit.current.interaction === readingInteraction.current;
      pendingReviewRestore.current =
        position && !location.hash && uninterrupted ? { visit, position } : null;
    }
    const restore =
      pendingReviewRestore.current?.visit === visit ? pendingReviewRestore.current : null;
    let frame = 0;
    if (restore) {
      frame = requestAnimationFrame(() => {
        frame = requestAnimationFrame(() => {
          if (pendingReviewRestore.current !== restore) return;
          window.scrollTo(0, restore.position.scrollY);
          const [documentPane, secondDocumentPane] = Array.from(reviewDocumentPanes());
          const findingPane = findingQueue();
          if (documentPane) documentPane.scrollTop = restore.position.documentScroll;
          if (secondDocumentPane && restore.position.secondDocumentScroll !== undefined)
            secondDocumentPane.scrollTop = restore.position.secondDocumentScroll;
          if (findingPane) findingPane.scrollTop = restore.position.findingScroll;
        });
      });
    }
    return () => {
      cancelAnimationFrame(frame);
      if (savedBeforeNavigation.current.delete(location.key)) return;
      if (pendingReviewRestore.current?.visit === visit) return;
      if (reviewDocumentPanes().length === 0) return;
      readingPositions.set(
        location.key,
        captureReviewPosition(caseId, runId, attemptId, actorScope),
      );
      if (readingPositions.size > 30)
        readingPositions.delete(readingPositions.keys().next().value!);
    };
  }, [
    actorScope,
    caseId,
    runId,
    selectedAttemptId,
    selectedAttempt?.id,
    location.key,
    location.hash,
    location.state,
    navigationType,
    readingVisitKey,
    review.isSuccess,
    review.isFetching,
    review.data,
    caseDetail.isSuccess,
    caseDetail.isFetching,
    caseDetail.data,
  ]);

  if (session.isLoading) return <PageStatus kind="loading" title="正在确认登录状态" />;
  if (!session.isSuccess) return <PageStatus kind="denied" title="无法确认当前登录身份" />;
  if (review.isLoading) return <PageStatus kind="loading" title="正在加载审查工作区" />;
  if (review.isError) {
    if (isForbidden(review.error)) {
      return (
        <PageStatus
          kind="denied"
          title="没有权限查看这次审查"
          detail={errorMessage(review.error)}
          action={
            <Button aria-label={"返回案件"} type="primary" href={`/cases/${caseId}`}>
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
  if (
    review.data.run.id !== runId ||
    review.data.run.case_id !== caseId ||
    (selectedAttemptId !== null && selectedAttempt?.id !== selectedAttemptId)
  ) {
    return (
      <PageStatus
        kind="error"
        title="审查身份无法确认"
        detail="服务端返回的审查或执行记录与当前页面不一致，请返回案件重新选择。"
      />
    );
  }
  if (
    caseDetail.isError &&
    (isForbidden(caseDetail.error) || isUnauthenticated(caseDetail.error))
  ) {
    return (
      <PageStatus
        kind="denied"
        title="没有权限查看这个案件"
        detail={errorMessage(caseDetail.error)}
      />
    );
  }

  const diagnosticData =
    currentDiagnosticScope !== null &&
    diagnosticScope === currentDiagnosticScope &&
    diagnostics.isSuccess &&
    !diagnostics.isFetching &&
    (!diagnostics.data.execution || diagnostics.data.execution.attempt.id === selectedAttempt?.id)
      ? diagnostics.data
      : null;
  const workspace = {
    ...review.data,
    documents: review.data.documents.map((document) => {
      const proof = sourceProofs.find(
        (item) =>
          item.documentId === document.id && item.parsedDocumentId === document.parsed_document_id,
      );
      return proof ? { ...document, digest: proof.originalDigest } : document;
    }),
    execution: diagnosticData?.execution ?? null,
    snapshot: diagnosticData?.snapshot ?? review.data.snapshot,
    audit_events: diagnosticData?.audit_events ?? review.data.audit_events,
  };

  const visibleFindings = workspace.findings.filter(
    (item) => !isLegacyPaymentCoverageFinding(workspace, item),
  );
  const pendingFindings = visibleFindings.filter((item) => item.decisions.length === 0);
  const requestedFinding =
    (scopeMismatch ? null : params.get("finding")) ??
    (requestedKind === "finding" ? requestedItem : null);
  const selectPending =
    pendingFindings.length > 0 &&
    !visibleFindings.some((finding) => finding.id === requestedFinding);
  const selectedId = selectedFindingId(
    selectPending ? pendingFindings : visibleFindings,
    requestedFinding,
  );
  const workFilter: WorkFilter =
    params.get("work_filter") === "evidence"
      ? "evidence"
      : params.get("work_filter") === "remediation"
        ? "remediation"
        : "judgment";
  const deniedRequests = isForbidden(requests.error) || isUnauthenticated(requests.error);
  const stateDocument =
    location.state &&
    typeof location.state === "object" &&
    "contractDesk" in location.state &&
    location.state.contractDesk &&
    typeof location.state.contractDesk === "object" &&
    "documentId" in location.state.contractDesk &&
    typeof location.state.contractDesk.documentId === "string"
      ? location.state.contractDesk.documentId
      : null;
  const retainedDocument =
    !hasRequestedSourceDocument &&
    proofEligible &&
    stateDocument &&
    sourceProofs.some((proof) => proof.documentId === stateDocument)
      ? stateDocument
      : null;
  const contractDesk = readContractDeskReturn(
    location.state,
    actorScope,
    requestedSourceDocument ?? retainedDocument,
  );
  const sourceMatches = requestedSourceDocument
    ? workspace.documents.filter((document) => document.id === requestedSourceDocument)
    : [];
  const sourceVerified = Boolean(
    requestedSourceDocument &&
    proofEligible &&
    sourceMatches.length === 1 &&
    sourceProofs.some(
      (proof) =>
        proof.documentId === requestedSourceDocument &&
        proof.parsedDocumentId === sourceMatches[0]?.parsed_document_id,
    ),
  );
  function editorTarget(documentId: string) {
    if (
      !caseDetailReady ||
      caseArchived ||
      !session.isSuccess ||
      session.isFetching ||
      (session.data?.role !== "admin" && session.data?.role !== "clerk")
    )
      return null;
    const documents = workspace.documents.filter((document) => document.id === documentId);
    const materials = (caseDetail.data.materials ?? []).filter(
      (material) => material.current_document_id === documentId,
    );
    const document = documents[0];
    if (
      documents.length !== 1 ||
      materials.length !== 1 ||
      !document ||
      !/\.docx$/i.test(document.filename) ||
      !sourceProofs.some(
        (proof) =>
          proof.documentId === documentId && proof.parsedDocumentId === document.parsed_document_id,
      )
    )
      return null;
    return { document, material: materials[0]!, actorScope: navigationActorScope };
  }
  const directEditorTarget =
    sourceVerified && requestedSourceDocument ? editorTarget(requestedSourceDocument) : null;
  const retainedEditorTarget = docxSession ? editorTarget(docxSession.document.id) : null;
  const sourcePending = Boolean(
    requestedSourceDocument &&
    (review.isFetching ||
      caseDetail.isFetching ||
      historyQueries.some((query) => query.isPending || query.isFetching)),
  );
  const activeReturn = readWorkReturn(location.state, caseId, actorScope);
  const activeReturnPosition = activeReturn
    ? reviewPositionForPath(location.state, activeReturn, caseId, actorScope)
    : null;
  const caseNavigationState = {
    ...(listReturn ?? {}),
    ...(caseReadingPosition ? { caseReadingPosition } : {}),
    ...(caseReadingPosition &&
    location.state &&
    typeof location.state === "object" &&
    "caseSection" in location.state &&
    typeof location.state.caseSection === "string"
      ? { caseSection: location.state.caseSection }
      : {}),
  };
  const navigationState = {
    ...caseNavigationState,
    ...(activeReturn ? workReturnState(activeReturn, caseId, actorScope) : null),
    ...(activeReturnPosition ? { reviewReadingPosition: activeReturnPosition } : null),
    workActorScope: actorScope,
    ...(contractDesk ? { contractDesk } : null),
  };
  const selectedFrozen = Boolean(
    caseId &&
    runId &&
    selectedAttempt?.id &&
    selectedAttempt.history_kind === "frozen" &&
    selectedAttempt.run_id === runId,
  );
  const targetKinds: WorkTargetKind[] = [
    "finding",
    "gap",
    "evaluation",
    "request-source",
    "request",
  ];
  const targetKind = targetKinds.find((kind) => kind === requestedKind);
  const matchingTarget =
    requestedItem &&
    targetKind &&
    !scopeMismatch &&
    !deniedRequests &&
    session.isSuccess &&
    !session.isFetching
      ? targetKind === "finding"
        ? selectedFrozen &&
          workspace.findings.filter((item) => item.id === requestedItem).length === 1
        : targetKind === "gap"
          ? selectedFrozen &&
            workspace.agent_review?.attempt_id === selectedAttempt?.id &&
            (workspace.agent_review?.gaps ?? []).filter((item) => item.gap_id === requestedItem)
              .length === 1
          : targetKind === "evaluation"
            ? selectedFrozen &&
              (workspace.evaluations ?? []).filter((item) => item.id === requestedItem).length === 1
            : targetKind === "request"
              ? requests.data?.filter(
                  (item) => item.case_id === caseId && item.id === requestedItem,
                ).length === 1
              : selectedFrozen &&
                requestedItem.startsWith("gap:") &&
                workspace.agent_review?.attempt_id === selectedAttempt?.id &&
                (workspace.agent_review?.gaps ?? []).filter(
                  (item) => item.gap_id === requestedItem.slice(4),
                ).length === 1
      : false;
  const workTarget: WorkTarget | null =
    matchingTarget && requestedItem && targetKind
      ? { kind: targetKind, id: requestedItem, runId, attemptId: selectedAttempt?.id ?? "" }
      : null;
  const workProjection = projectReviewWork(
    workspace,
    caseDetail.isSuccess ? caseDetail.data : undefined,
    deniedRequests ? undefined : requests.data,
  );
  const workError =
    requests.isError && !deniedRequests
      ? `补件请求读取失败：${errorMessage(requests.error)}`
      : caseDetail.isError
        ? `案件状态读取失败：${errorMessage(caseDetail.error)}`
        : null;
  const canPrepareRequest = currentWorkContext && roleCanManageRun && !deniedRequests;
  const canContinueRequest =
    session.isSuccess &&
    !session.isFetching &&
    caseDetailReady &&
    !caseArchived &&
    roleCanManageRun &&
    Boolean(caseDetail.data.latest_run) &&
    (!latestRun || !selectedAttemptIsLatest);

  function savedReturn(target: WorkTarget) {
    const next = new URLSearchParams(params);
    if (selectedAttempt?.id) next.set("attempt_id", selectedAttempt.id);
    next.set("work_kind", target.kind);
    next.set("work_item", target.id);
    if (target.kind === "finding") next.set("finding", target.id);
    if (target.kind === "remediation") writeReviewDrawer(next, "record", "review");
    else writeReviewDrawer(next, ...workTargetDrawer(target.kind));
    return {
      ...caseNavigationState,
      ...workReturnState(`${location.pathname}?${next}`, caseId, actorScope),
      reviewReadingPosition: captureReviewPosition(
        caseId,
        runId,
        selectedAttempt?.id ?? null,
        actorScope,
      ),
    };
  }

  function openWork(target: WorkTarget) {
    if (!actorScope || scopeMismatch) return;
    if (target.kind === "remediation") {
      const next = new URLSearchParams({ work_kind: "remediation", work_item: target.id });
      void navigate(`/cases/${encodeURIComponent(caseId)}?${next}`, { state: savedReturn(target) });
      return;
    }
    if (
      target.kind !== "request" &&
      (target.runId !== runId || target.attemptId !== selectedAttempt?.id)
    ) {
      const next = new URLSearchParams({
        attempt_id: target.attemptId,
        work_filter: "evidence",
        work_kind: target.kind,
        work_item: target.id,
      });
      writeReviewDrawer(next, ...workTargetDrawer(target.kind));
      void navigate(
        `/cases/${encodeURIComponent(caseId)}/reviews/${encodeURIComponent(target.runId)}?${next}`,
        { state: savedReturn(target) },
      );
      return;
    }
    setNavigationSerial((value) => value + 1);
    setParams(
      (current) => {
        const next = new URLSearchParams(current);
        next.set("work_kind", target.kind);
        next.set("work_item", target.id);
        if (target.kind === "finding") next.set("finding", target.id);
        writeReviewDrawer(next, ...workTargetDrawer(target.kind));
        return next;
      },
      { state: navigationState },
    );
  }

  function continueRequest(target: WorkTarget) {
    const latest = caseDetail.data?.latest_run;
    if (!canContinueRequest || !latest) return;
    const next = new URLSearchParams({
      work_filter: "evidence",
      work_kind: "request",
      work_item: target.id,
    });
    writeReviewDrawer(next, ...workTargetDrawer(target.kind));
    void navigate(`${caseHref(caseId, latest.id)}?${next}`, { state: savedReturn(target) });
  }
  const snapshot = workbench.data;
  const contextCase = snapshot?.cases.find((item) => item.id === caseId);
  const profile =
    contextCase?.profile ?? snapshot?.profiles.find((item) => item.case_id === caseId) ?? null;
  const memberById = new Map((snapshot?.members ?? []).map((member) => [member.id, member]));
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
      addCollaborator(task.assignee_id, `任务责任人：${task.title}（${task.status}）`);
  const currentRelations = (snapshot?.relations ?? []).filter(
    (relation) => relation.case_id === caseId,
  );
  const relatedIds = new Set(
    (snapshot?.relations ?? [])
      .filter((relation) => relation.case_id === caseId || relation.related_case_id === caseId)
      .map((relation) =>
        relation.case_id === caseId ? relation.related_case_id : relation.case_id,
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
        businessLine: contextCase?.business_line ?? caseDetail.data?.business_line ?? "未报告",
        scenario: contextCase?.scenario ?? caseDetail.data?.scenario ?? "未报告",
        status: contextCase?.business_status ?? caseDetail.data?.business_status ?? "未报告",
        ownerId: profile?.owner_id ?? null,
        ownerName: profile?.owner_id
          ? (memberById.get(profile.owner_id)?.display_name ?? null)
          : null,
        dueAt: profile?.due_at ?? null,
        collaborators: [...collaboratorById.values()],
        relatedCases: currentRelations.map((relation) => {
          const relatedCase = snapshot.cases.find((item) => item.id === relation.related_case_id);
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
      if (isConflict(error) && !isFindingCitationRejection(error)) {
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
      if (isConflict(error) && !isFindingCitationRejection(error)) {
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

  const partialScope = partialReportScope(workspace);

  function retryCurrentReview() {
    if (!canRetry || retry.isPending) return;
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
  }

  function selectAttempt(attemptId: string) {
    const next = new URLSearchParams(params);
    next.set("attempt_id", attemptId);
    // A citation belongs to its own attempt; never show the previous
    // attempt's evidence after switching.
    next.delete("citation");
    next.delete("finding");
    next.delete("work_kind");
    next.delete("work_item");
    setParams(next, { state: navigationState });
  }

  const workspaceView = (
    <ReviewWorkspaceView
      sourceDocumentId={sourceVerified ? requestedSourceDocument : null}
      surface="document"
      immersive
      coverageHost={coverageHost}
      onReturnToReading={() => selectDrawer(null)}
      workspace={workspace}
      caseDetail={
        caseDetail.isSuccess && !caseDetail.isFetching && caseDetail.data.id === caseId
          ? caseDetail.data
          : undefined
      }
      layout={layout}
      showDiagnostics={diagnosticData !== null}
      quoteScope={quoteScope}
      onQuoteSelectionChange={setQuoteSelection}
      selectedId={selectedId}
      explicitFindingId={scopeMismatch ? null : params.get("finding")}
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
      conflictMessage={conflictFinding === selectedId ? conflictMessage : null}
      saveError={conflictFinding === selectedId ? saveError : null}
      onSelect={(findingId) => {
        setParams(
          (current) => {
            const next = new URLSearchParams(current);
            next.set("finding", findingId);
            if (drawer === "execution") writeReviewDrawer(next, null);
            next.delete("source_document");
            if (next.get("work_kind") === "finding") next.set("work_item", findingId);
            return next;
          },
          { state: navigationState },
        );
      }}
      onSelectAttempt={selectAttempt}
      selectedEvidenceId={selectedCitation}
      onSelectEvidence={(evidenceId) => {
        if (actorScope) {
          readingPositions.set(
            location.key,
            captureReviewPosition(caseId, runId, selectedAttempt?.id ?? null, actorScope),
          );
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
      onRetry={retryCurrentReview}
      partialStatusManaged={partialScope !== null}
      emptyFindingMessage={partialScope?.emptyMessage}
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
  );
  const activeFinding = workspace.findings.find((item) => item.id === selectedId);
  const modelFinding = activeFinding?.origin === "model" || activeFinding?.origin === "ai";
  const remaining = pendingFindings.length;
  const canCreateReport = session.data?.role === "admin" || session.data?.role === "clerk";
  const reportReady =
    currentWorkContext &&
    !scopeMismatch &&
    canCreateReport &&
    (selectedAttempt?.state === "completed" || selectedAttempt?.state === "partial") &&
    Boolean(selectedAttempt.finished_at) &&
    selectedAttempt.frozen_result_available === true &&
    Boolean(workspace.snapshot?.document_ids.length) &&
    remaining === 0;
  const reportResultVisible =
    canCreateReport && !scopeMismatch && submittedReportScope === reportCommandScope;
  const reportRecord =
    reportResultVisible &&
    createReport.data?.attempt_id === selectedAttempt?.id &&
    createReport.data?.run_id === runId
      ? createReport.data
      : null;
  const reportError =
    reportResultVisible && createReport.isError ? errorMessage(createReport.error) : null;
  function submitReport() {
    if (!reportReady || !selectedAttempt || createReport.isPending) return;
    setSubmittedReportScope(reportCommandScope);
    createReport.mutate({ run_id: runId, attempt_id: selectedAttempt.id });
  }

  function requestReport() {
    if (!reportReady || createReport.isPending) return;
    if (selectedAttempt?.state === "partial") {
      if (partialScope) reportPreflight.show();
    } else {
      submitReport();
    }
  }

  const casePath = `/cases/${encodeURIComponent(caseId)}`;
  const prepareReview = (
    <Button
      type="primary"
      href={`${casePath}#review-readiness`}
      onClick={(event) => {
        event.preventDefault();
        reportPreflight.dismiss();
        void navigate(`${casePath}#review-readiness`, { state: caseNavigationState });
      }}
    >
      {partialScope?.precheck ? "查看完整审查准备" : "查看重新审查准备"}
    </Button>
  );
  const partialRecovery =
    hasTransientRetryCause(workspace) && !partialScope?.precheck ? (
      <Button
        type="primary"
        disabled={!canRetry || retry.isPending}
        onClick={() => {
          reportPreflight.dismiss();
          retryCurrentReview();
        }}
      >
        {retry.isPending ? "正在受理重试" : "重新执行智能审查"}
      </Button>
    ) : latestRun && selectedAttemptIsLatest && !caseArchived ? (
      prepareReview
    ) : null;

  const agentReview = workspace.agent_review;
  const agentRuleVersion =
    agentReview && selectedAttempt && agentReview.attempt_id === selectedAttempt.id
      ? agentReview.rulepack?.version
      : null;
  const reviewBasis = (
    <section className="review-record-basis" aria-label="本次审查依据">
      <h3>本次审查依据</h3>
      <dl>
        {(workspace.snapshot?.document_ids ?? []).map((documentId) => {
          const doc = workspace.documents.find((item) => item.id === documentId);
          const matches = (caseDetailReady ? (caseDetail.data.material_versions ?? []) : []).filter(
            (item) => item.document_id === documentId,
          );
          return (
            <div key={documentId}>
              <dt>{doc?.filename ?? "材料名称未记录"}</dt>
              <dd>{matches.length === 1 ? `第 ${matches[0]!.version} 版` : "版本未记录"}</dd>
            </div>
          );
        })}
        <div>
          <dt>审查规则</dt>
          <dd>
            {selectedAttempt?.review_scope?.rule_set_version ??
              agentRuleVersion ??
              workspace.snapshot?.review_scope?.rule_set_version ??
              workspace.snapshot?.governed_knowledge?.rulepack?.version ??
              "版本未记录"}
          </dd>
        </div>
        <div>
          <dt>审查编号</dt>
          <dd>
            <Button
              type="link"
              onClick={() =>
                void Promise.resolve()
                  .then(() => navigator.clipboard.writeText(runId))
                  .then(
                    () => setCopyStatus("已复制审查编号"),
                    () => setCopyStatus("无法复制审查编号"),
                  )
              }
            >
              复制编号
            </Button>
          </dd>
        </div>
      </dl>
      <span role="status">{copyStatus}</span>
    </section>
  );
  const findingRecord = (
    <div className="review-record-content">
      {activeFinding ? (
        <>
          <h3>{activeFinding.claim}</h3>
          <ol className="review-decision-timeline">
            <li className={modelFinding ? "review-timeline-model" : undefined}>
              <strong>{modelFinding ? "AI 建议" : "规则线索"}</strong>
              {activeFinding.evidence.map((anchor) => (
                <blockquote key={anchor.id}>{anchor.quote}</blockquote>
              ))}
            </li>
            {activeFinding.decisions.length === 0 && <li>还没有人工判断。</li>}
            {activeFinding.decisions.map((decision) => (
              <li key={decision.id}>
                <strong>
                  {decision.actor} ·{" "}
                  {reviewDecisionLabels[decision.action as DecisionAction] ??
                    decisionLabel(decision.action)}
                </strong>
                <time dateTime={decision.decided_at}>{formatDateTime(decision.decided_at)}</time>
                <p>{decision.reason}</p>
              </li>
            ))}
          </ol>
        </>
      ) : (
        <p>请选择一条线索查看记录。</p>
      )}
      {reviewBasis}
    </div>
  );
  const historyRecord =
    session.isSuccess && !session.isFetching ? (
      <div id="review-diff-section">
        {!professional && execution && execution.attempts.length > 1 ? (
          <label className="field">
            查看审查尝试
            <select
              value={selectedAttempt?.id}
              onChange={(event) => selectAttempt(event.target.value)}
            >
              {execution.attempts.map((attempt) => (
                <option key={attempt.id} value={attempt.id}>
                  第 {attempt.ordinal} 次审查
                  {attempt.accepted_at ? ` · ${formatDateTime(attempt.accepted_at)}` : null}
                </option>
              ))}
            </select>
          </label>
        ) : null}
        {!professional && selectedAttempt?.history_kind === "legacy_unfrozen" ? (
          <p>较早的审查没有保存当时使用的材料版本，无法核对当时的来源。</p>
        ) : null}
        <RunProvenance workspace={workspace} showDiagnostics={diagnosticData !== null} />
        <ReviewDiff
          caseId={caseId}
          latestRunId={caseDetail.data?.latest_run?.id ?? runId}
          actorScope={JSON.stringify([
            session.data.id,
            session.data.active_context?.tenant_id,
            session.data.active_context?.org_id,
            session.data.role,
            session.data.audience,
            session.data.active_context?.context_version,
          ])}
        />
      </div>
    ) : null;
  const supplementRecord = (
    <div id="review-coverage-section">
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
        canCreate={
          currentWorkContext && roleCanDecide && selectedAttempt?.history_kind === "frozen"
        }
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
        selectedSource={
          workTarget?.kind === "evaluation"
            ? `evaluation:${workTarget.id}`
            : workTarget?.kind === "request-source"
              ? workTarget.id
              : null
        }
        navigationSerial={navigationSerial}
        expanded={drawer === "record" && recordTab === "supplement"}
        evaluations={review.data.evaluations ?? []}
        agentReview={review.data.agent_review ?? null}
        documents={caseDetail.data?.documents ?? []}
        quoteScope={quoteScope}
        quoteSelection={quoteSelection}
      />
      <OperatorAuditEntry caseId={caseId} runId={runId} attemptId={selectedAttempt?.id ?? null} />
    </div>
  );
  const reviewRecord = (
    <div id="review-history-section">
      {partialScope ? (
        <section aria-label="本次审查状态">
          <h3>本次审查状态</h3>
          <p>{partialScope.reason}</p>
          <p>
            {partialScope.ruleCoverage}。未覆盖：{partialScope.uncovered.join("、")}。
          </p>
          {partialRecovery}
        </section>
      ) : null}
      {reviewBasis}
      {cloneElement(workspaceView, { surface: "coverage", immersive: false })}
      <div ref={setCoverageHost} />
      {caseContext && (
        <ReviewCaseContextPanel
          context={caseContext}
          caseReturnState={caseNavigationState}
          canEditRelations={canEditRelations}
          relationPending={relationPending}
          relationError={relationError}
          onCreateRelation={saveRelation}
          onDeleteRelation={(relationId) => deleteRelation.mutate(relationId)}
        />
      )}
      {session.isSuccess && !session.isFetching && !scopeMismatch && !deniedRequests ? (
        <div id="review-work-section">
          <ReviewWorkItems
            projection={workProjection}
            filter={workFilter}
            onFilter={(filter) =>
              setParams(
                (current) => {
                  const next = new URLSearchParams(current);
                  next.set("work_filter", filter);
                  return next;
                },
                { state: navigationState },
              )
            }
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
        </div>
      ) : deniedRequests ? (
        <p role="alert">无法读取补件请求，办理导航暂不可用。</p>
      ) : null}
      <ExperienceFeedbackPanel
        caseId={caseId}
        runId={runId}
        attemptId={selectedAttempt?.id ?? null}
        finding={review.data.findings.find((item) => item.id === selectedId) ?? null}
        documents={review.data.documents}
        canSubmit={
          currentWorkContext && roleCanDecide && selectedAttempt?.history_kind === "frozen"
        }
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
      <RunProvenance
        workspace={{ ...workspace, previous_run_id: null }}
        showDiagnostics={diagnosticData !== null}
      />
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
    </div>
  );
  return (
    <SectionNavigationProvider
      scope={`${caseId}:${runId}:${selectedAttempt?.id ?? selectedAttemptId ?? ""}:${navigationActorScope}`}
      actorScope={navigationActorScope}
      targetPending={drawerTargetPending}
      ready={Boolean(
        actorScope &&
        review.isSuccess &&
        !review.isFetching &&
        caseDetailReady &&
        review.data.run.id === runId &&
        review.data.run.case_id === caseId &&
        caseDetail.data.id === caseId,
      )}
    >
      <section className="page page-review">
        {session.isFetching ? <p role="status">正在重新确认登录状态，办理暂不可用。</p> : null}
        <div className="review-reading-content" ref={readingContent} hidden={session.isFetching}>
          <div className="review-page-bar">
            <header className="review-topbar">
              <div className="review-topbar-identity">
                <Link to={casePath} state={caseNavigationState} aria-label="返回案件">
                  ‹ 案件
                </Link>
                <h1 title={caseDetail.data?.title ?? "审查工作区"}>
                  {caseDetail.data?.title ?? "审查工作区"}
                </h1>
                {selectedAttempt && (
                  <span className="review-attempt-label">第 {selectedAttempt.ordinal} 次审查</span>
                )}
              </div>
              <ReviewProgress
                state={decisionState}
                hasDocuments={workspace.documents.length > 0}
                hasPendingJudgments={remaining > 0}
                hasFrozenAttempt={selectedAttempt?.history_kind === "frozen"}
              />
              <span className="review-finding-count">
                {partialScope
                  ? partialScope.precheck
                    ? `单合同预审 · ${visibleFindings.length} 条线索`
                    : `规则检查 ${partialScope.ruleFindings} 条 · ${partialScope.executionLabel}`
                  : `${visibleFindings.length} 条线索 · 还有 ${remaining} 条待判断`}
                {partialScope && partialScope.modelFindings > 0
                  ? ` · AI 建议 ${partialScope.modelFindings} 条`
                  : null}
                {partialScope && remaining > 0 ? ` · 还有 ${remaining} 条待判断` : null}
              </span>
              <nav className="review-topbar-actions" aria-label="审查操作">
                <Button
                  type="text"
                  aria-haspopup="dialog"
                  onClick={() => selectDrawer("record", "supplement")}
                >
                  补充材料
                </Button>
                <Button
                  type="text"
                  aria-haspopup="dialog"
                  onClick={() => selectDrawer("record", "finding")}
                >
                  审查记录
                </Button>
                {professional ? (
                  <Button
                    type="text"
                    aria-haspopup="dialog"
                    onClick={() => selectDrawer("execution")}
                  >
                    执行过程
                  </Button>
                ) : canCancel ? (
                  <Button type="text" loading={cancel.isPending} onClick={() => cancel.mutate()}>
                    取消本次执行
                  </Button>
                ) : null}
                {reportRecord ? (
                  <Button
                    type="primary"
                    href={paths.report(caseId, reportRecord.id, "html")}
                    target="_blank"
                    rel="noreferrer"
                  >
                    查看报告
                  </Button>
                ) : (
                  <Button
                    type="primary"
                    disabled={!reportReady}
                    loading={createReport.isPending}
                    onClick={requestReport}
                  >
                    {remaining ? "判断完再出报告" : "出报告"}
                  </Button>
                )}
              </nav>
            </header>
            {partialScope ? (
              <div className="review-partial-status">
                <span role="status">
                  {partialScope.precheck
                    ? "单合同预审已结束，仅保留预审范围内结果。"
                    : `部分完成 · ${partialScope.executionLabel}`}
                </span>
                {partialRecovery}
                <Button size="small" onClick={() => selectDrawer("record", "review")}>
                  查看原因
                </Button>
              </div>
            ) : null}
            {partialScope ? (
              <ReportPreflight
                summary={partialScope}
                open={reportReady && reportPreflight.open}
                onConfirm={() => reportPreflight.confirm(submitReport)}
                onCancel={reportPreflight.dismiss}
                recovery={partialRecovery}
              />
            ) : null}
            {reportError && (
              <p className="callout" role="alert">
                {reportError}
              </p>
            )}
            {!professional && cancel.isError ? (
              <p className="callout" role="alert">
                {errorMessage(cancel.error)}
              </p>
            ) : null}
            {reportRecord && (
              <p role="status" className="sr-only">
                报告已创建，版本 {reportRecord.version}。
              </p>
            )}
            <div className="review-reading-returns">
              {contractDesk ? (
                <nav className="contract-desk-return" aria-label="合同清单导航">
                  <Link to={contractDesk.returnTo}>返回合同清单</Link>
                  {contractDesk.previousHref ? (
                    <Link to={contractDesk.previousHref}>上一份</Link>
                  ) : null}
                  {contractDesk.nextHref ? <Link to={contractDesk.nextHref}>下一份</Link> : null}
                </nav>
              ) : null}
              {activeReturn ? (
                <Link
                  to={activeReturn}
                  state={{ ...caseNavigationState, reviewReadingPosition: activeReturnPosition }}
                >
                  返回人工办理
                </Link>
              ) : null}
              {directEditorTarget && (
                <Button onClick={() => setDocxSession(directEditorTarget)}>编辑合同修改稿</Button>
              )}
            </div>
          </div>
          {streamStatus === "closed" ? (
            <section className="callout" role="status" aria-live="polite">
              <span>实时进度连接已关闭，请刷新页面或重新登录。</span>
            </section>
          ) : null}
          {streamStatus === "exhausted" ? (
            <section className="callout" role="status" aria-live="polite">
              <span>实时进度连接已断开，请重新连接。</span>
              <Button
                type="primary"
                htmlType="button"
                onClick={() => setStreamReconnectKey((key) => key + 1)}
              >
                重新连接
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
          {requestedItem &&
          targetKind &&
          !workTarget &&
          !requests.isFetching &&
          !scopeMismatch &&
          !deniedRequests ? (
            <p role="alert">所选办理事项不属于当前执行或身份不唯一，请返回办理列表核对。</p>
          ) : null}
          <DecisionDraftScopeProvider scope={decisionDraftScope}>
            <OriginalSourceContext.Provider
              value={
                caseDetailReady && session.isSuccess && !session.isFetching
                  ? {
                      caseId,
                      editDocument: !hasRequestedSourceDocument
                        ? (id: string) => {
                            const target = editorTarget(id);
                            if (target) setDocxSession(target);
                          }
                        : undefined,
                      editableDocumentIds: !hasRequestedSourceDocument
                        ? new Set(
                            workspace.documents.flatMap((document) =>
                              editorTarget(document.id) ? [document.id] : [],
                            ),
                          )
                        : undefined,
                      contextKey: `${actorReadContextKey(session.data)}:${selectedAttempt?.id ?? "legacy"}`,
                      digests: new Map(
                        caseDetail.data.documents.map((document) => [document.id, document.digest]),
                      ),
                    }
                  : null
              }
            >
              <div id="review-document-section" role="region" aria-label="审查工作区">
                {hasRequestedSourceDocument && !sourceVerified ? (
                  <section className="callout" role={sourcePending ? "status" : "alert"}>
                    <p>
                      {sourcePending
                        ? "正在核验所选材料的送审版本。"
                        : "所选材料不属于当前可核验的送审版本。"}
                    </p>
                    {!sourcePending ? (
                      <Button
                        onClick={() =>
                          setParams(
                            (current) => {
                              const next = new URLSearchParams(current);
                              next.delete("source_document");
                              return next;
                            },
                            { state: navigationState },
                          )
                        }
                      >
                        查看本次材料
                      </Button>
                    ) : null}
                  </section>
                ) : (
                  workspaceView
                )}
              </div>
            </OriginalSourceContext.Provider>
          </DecisionDraftScopeProvider>
          <DirtyLeaveGuard dirty={decisionDraftsDirty || evidenceDraftDirty || docxDirty} />
          {docxSession && (
            <Suspense fallback={<p role="status">正在加载编辑器…</p>}>
              <DocxWorkspace
                caseId={caseId}
                documentId={docxSession.document.id}
                filename={docxSession.document.filename}
                digest={docxSession.document.digest}
                material={docxSession.material}
                canSave={Boolean(
                  retainedEditorTarget &&
                  retainedEditorTarget.document.digest === docxSession.document.digest &&
                  retainedEditorTarget.material.material_id === docxSession.material.material_id &&
                  navigationActorScope === docxSession.actorScope,
                )}
                onDirty={onDocxDirty}
                onClose={() => setDocxSession(null)}
              />
            </Suspense>
          )}
          <Drawer
            getContainer={false}
            destroyOnHidden
            title={drawer === "execution" ? "执行过程" : "审查记录"}
            open={drawer !== null && session.isSuccess && !session.isFetching && !scopeMismatch}
            size="min(520px, 100vw)"
            onClose={() => selectDrawer(null)}
            afterOpenChange={(open) => {
              if (open || !focusJudgmentOnClose.current) return;
              focusJudgmentOnClose.current = false;
              readingContent.current
                ?.querySelector<HTMLTextAreaElement>(".review-decision-form textarea")
                ?.focus();
            }}
            footer={
              drawer === "record" && recordTab === "finding" ? (
                <div className="review-record-actions">
                  {professional ? (
                    <Button onClick={() => selectDrawer("execution")}>查看执行过程</Button>
                  ) : null}
                  <Button
                    type="primary"
                    disabled={!canDecide || !activeFinding}
                    onClick={() => {
                      focusJudgmentOnClose.current = true;
                      selectDrawer(null);
                    }}
                  >
                    追加意见
                  </Button>
                </div>
              ) : undefined
            }
          >
            {!session.isSuccess || session.isFetching || scopeMismatch ? null : drawer ===
              "execution" ? (
              <>
                {cloneElement(workspaceView, {
                  surface: "trace",
                  immersive: false,
                })}
                <Link
                  to={`/cases/${encodeURIComponent(caseId)}/reviews/${encodeURIComponent(runId)}/agent${selectedAttempt ? `?attempt=${encodeURIComponent(selectedAttempt.id)}` : ""}`}
                >
                  Agent Studio
                </Link>
              </>
            ) : (
              <Tabs
                activeKey={recordTab}
                onChange={(tab) => {
                  const selectedTab = reviewRecordTabs.find((value) => value === tab);
                  if (selectedTab) selectDrawer("record", selectedTab);
                }}
                items={[
                  { key: "finding", label: "这条线索", children: findingRecord },
                  { key: "review", label: "本次审查", children: reviewRecord },
                  { key: "history", label: "历次审查", children: historyRecord },
                  { key: "supplement", label: "补充材料", children: supplementRecord },
                ]}
              />
            )}
          </Drawer>
          {currentDiagnosticScope !== null &&
          diagnosticScope === currentDiagnosticScope &&
          diagnostics.isError ? (
            <p role="status">审查诊断不可用：{errorMessage(diagnostics.error)}</p>
          ) : null}
          {retry.isError ? <p role="alert">{errorMessage(retry.error)}</p> : null}
        </div>
      </section>
    </SectionNavigationProvider>
  );
}
