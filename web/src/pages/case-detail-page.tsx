// File metadata (manifest mirror):
// Purpose: Presents a case with one property row, a permission-aware next step and four sections for materials, reviews, reports or remediation, and subjects or activity, with stable navigation to existing records.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-20
// Revision: r16
// Copyright: See LICENSE
// End file metadata.

import { Button, Dropdown, Input, Tabs } from "antd";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Link, useLocation, useNavigate, useParams, useSearchParams } from "react-router";
import { useIsFetching, useQuery, useQueryClient } from "@tanstack/react-query";
import { getReviewReadiness, type ReviewScope } from "../api/review-readiness";
import type { PlaybookRef } from "../api/playbooks";
import { beijingInputToUtc } from "../components/beijing-datetime";
import { ReviewReadinessPanel } from "../components/review-readiness-panel";
import { errorMessage, isForbidden, isUnauthenticated } from "../api/http";
import { useCaseDetail, useCaseReviewHistory, useSession, useStartReview } from "../api/queries";
import { PageStatus } from "../components/page-status";
import { ObjectDetailPage } from "../components/page-patterns";
import { CollapsibleSection } from "../components/collapsible-section";
import {
  isReadingInteraction,
  readCaseListReturn,
  readCaseReadingPosition,
  reviewPositionForPath,
  SectionNavigationProvider,
  type CaseReadingPosition,
} from "../components/section-navigation";
import { CaseNextStep } from "../components/case-next-step";
import { PlaybookPicker } from "../components/playbook-picker";
import { MaterialCollectionPanel } from "../components/material-collection-panel";
import { CaseSubjectRolesPanel } from "../components/case-subject-roles-panel";
import { CaseSubjectCorrectionsPanel } from "../components/case-subject-corrections-panel";
import { CaseLegalApplicabilityPanel } from "../components/case-legal-applicability-panel";
import { ReportArchivePanel } from "../components/report-archive-panel";
import { EvidenceSLADashboard } from "../components/evidence-sla-dashboard";
import { RemediationPanel } from "../components/remediation-panel";
import { TemplateGenerationPanel } from "../components/template-generation-panel";
import { StatusText } from "../components/status-text";
import { scenarioLabel } from "../domain/business-labels";
import { CaseSupportGrantPanel } from "./support-grant-pages";
import { canReadmitCancelledPreparation } from "../features/review/review-outcome";
import { readWorkReturn } from "../features/review/work-items";
import { actorReadContextKey } from "../domain/actor-read-context";
import { recordRecentCaseVisit } from "../features/home/recent-cases";
import {
  caseHref,
  contractTypeLabel,
  roleLabel,
  formatDateTime,
  readyDocumentCount,
  runLabel,
  runTone,
} from "../domain/review";
import { compactTime } from "../domain/time";
import "./case-detail-page.css";
import { contractDeskReturnHref } from "./contract-desk-page";

const navigationStores = new WeakMap<
  object,
  {
    positions: Map<string, CaseReadingPosition>;
    sections: Map<string, { caseId: string; actorScope: string; section: CaseSection }>;
    previousActor: string;
  }
>();
type CaseSection = "materials" | "reviews" | "work" | "subjects";

function restoredCaseSection(value: unknown, defaultSection: CaseSection): CaseSection | null {
  switch (value) {
    case "materials":
    case "reviews":
    case "work":
    case "subjects":
      return value;
    case "legal":
      return "subjects";
    case "collaboration":
      return "work";
    case "overview":
      return defaultSection;
    default:
      return null;
  }
}

export function CaseDetailPage() {
  const { caseId = "" } = useParams();
  const [actionTarget, setActionTarget] = useState<HTMLDivElement | null>(null);
  const [stageTarget, setStageTarget] = useState<HTMLSpanElement | null>(null);
  const [preparationActionTarget, setPreparationActionTarget] = useState<HTMLDivElement | null>(
    null,
  );
  const navigate = useNavigate();
  const location = useLocation();
  const [searchParams, setSearchParams] = useSearchParams();
  const detail = useCaseDetail(caseId);
  const session = useSession();
  const workActorScope =
    session.isSuccess && !session.isFetching ? actorReadContextKey(session.data) : "";
  const navigationScope = session.data ? actorReadContextKey(session.data) : "";
  const restoredCaseVisit = useRef("");
  const readingContent = useRef<HTMLElement>(null);
  const readingInteraction = useRef(0);
  const readingVisit = useRef<{ key: string; interaction: number }>({ key: "", interaction: 0 });
  const readingVisitKey = JSON.stringify([location.key, caseId]);
  const previousNavigationScope = useRef(navigationScope);
  const navigationActorChanged = Boolean(
    workActorScope &&
    previousNavigationScope.current &&
    previousNavigationScope.current !== workActorScope,
  );
  useEffect(() => {
    if (
      session.isSuccess &&
      !session.isFetching &&
      detail.isSuccess &&
      !detail.isFetching &&
      workActorScope &&
      !navigationActorChanged &&
      detail.data.id === caseId
    ) {
      recordRecentCaseVisit(session.data, caseId);
    }
  }, [
    session.data,
    session.isSuccess,
    session.isFetching,
    detail.data,
    detail.isSuccess,
    detail.isFetching,
    workActorScope,
    navigationActorChanged,
    caseId,
  ]);
  useEffect(() => {
    if (workActorScope) previousNavigationScope.current = workActorScope;
  }, [workActorScope]);
  const listReturn = readCaseListReturn(location.state, caseId, workActorScope);
  const navigationListReturn = readCaseListReturn(location.state, caseId, navigationScope);
  const caseReadingPosition = readCaseReadingPosition(location.state, caseId, workActorScope);
  const queryClient = useQueryClient();
  const navigationStore = navigationStores.get(queryClient) ?? {
    positions: new Map(),
    sections: new Map(),
    previousActor: "",
  };
  navigationStores.set(queryClient, navigationStore);
  const casePositions = navigationStore.positions;
  const caseSections = navigationStore.sections;
  const legalQueryFilter = {
    queryKey: ["cases", caseId, "legal-applicability-facts"],
    predicate: (query: { queryKey: readonly unknown[] }) => query.queryKey[5] === workActorScope,
  };
  const legalFetching = useIsFetching(legalQueryFilter);
  const targetPending = useCallback(
    (id: string) =>
      id.startsWith("legal-fact-") &&
      (legalFetching > 0 || queryClient.getQueryCache().findAll(legalQueryFilter).length === 0),
    [caseId, workActorScope, legalFetching, queryClient],
  );
  const incomingWork =
    location.state && typeof location.state === "object" && "workReturn" in location.state
      ? location.state.workReturn
      : null;
  const incomingScope =
    incomingWork && typeof incomingWork === "object" && "actorScope" in incomingWork
      ? incomingWork.actorScope
      : null;
  const workScopeMismatch =
    navigationActorChanged ||
    Boolean(
      workActorScope && typeof incomingScope === "string" && incomingScope !== workActorScope,
    );
  const selectedRemediationId =
    !workScopeMismatch && searchParams.get("work_kind") === "remediation"
      ? searchParams.get("work_item")
      : null;
  const workReturn = readWorkReturn(location.state, caseId, workActorScope);
  const savedSection =
    location.state && typeof location.state === "object" && "caseSection" in location.state
      ? location.state.caseSection
      : null;
  const defaultSection = detail.data?.latest_run ? "reviews" : "materials";
  const validSavedSection = caseReadingPosition
    ? restoredCaseSection(savedSection, defaultSection)
    : null;
  const [sectionSelection, setSectionSelection] = useState<{
    visit: string;
    section: CaseSection;
  } | null>(null);
  const sectionVisit = JSON.stringify([location.key, caseId, navigationScope]);
  const remembered = caseSections.get(location.key);
  const rememberedSection =
    remembered?.caseId === caseId && remembered.actorScope === workActorScope
      ? remembered.section
      : null;
  const activeSection =
    (sectionSelection?.visit === sectionVisit ? sectionSelection.section : null) ??
    (!location.hash
      ? selectedRemediationId
        ? "work"
        : (validSavedSection ?? rememberedSection)
      : null) ??
    defaultSection;
  const revealTarget = useCallback(
    (id: string, target: HTMLElement | null) => {
      const owner = target?.closest<HTMLElement>("[data-case-section]")?.dataset.caseSection as
        CaseSection | undefined;
      const section =
        owner ??
        (id.startsWith("legal-fact-")
          ? "subjects"
          : id === "review-business-time"
            ? "reviews"
            : undefined);
      if (!section) return true;
      if (activeSection !== section) {
        setSectionSelection({ visit: sectionVisit, section });
        return false;
      }
      return !target || !target.closest("[hidden]");
    },
    [activeSection, sectionVisit],
  );
  useEffect(() => {
    if (!workActorScope || !detail.isSuccess || detail.isFetching || detail.data.id !== caseId)
      return;
    caseSections.set(location.key, { caseId, actorScope: workActorScope, section: activeSection });
    if (caseSections.size > 30) caseSections.delete(caseSections.keys().next().value!);
  }, [
    location.key,
    caseId,
    workActorScope,
    activeSection,
    detail.isSuccess,
    detail.isFetching,
    detail.data,
    caseSections,
  ]);
  const reviewReadingPosition = workReturn
    ? reviewPositionForPath(location.state, workReturn, caseId, workActorScope)
    : null;
  useEffect(() => {
    if (!workScopeMismatch) return;
    setSearchParams(
      (current) => {
        const next = new URLSearchParams(current);
        next.delete("work_kind");
        next.delete("work_item");
        return next;
      },
      { replace: true, state: null },
    );
  }, [workScopeMismatch, setSearchParams]);
  const actorScope = navigationScope;
  const previousActorScope = useRef(actorScope);
  const sourceCaseParam = searchParams.get("source_case");
  useEffect(() => {
    const actorChanged =
      previousActorScope.current !== "" && previousActorScope.current !== actorScope;
    const caseChanged = sourceCaseParam !== null && sourceCaseParam !== caseId;
    previousActorScope.current = actorScope;
    if (!actorChanged && !caseChanged) return;
    const next = new URLSearchParams(searchParams);
    next.delete("source_case");
    next.delete("source_snapshot");
    setSearchParams(next, { replace: true });
  }, [actorScope, caseId, searchParams, setSearchParams, sourceCaseParam]);
  useLayoutEffect(() => {
    const stopRestoring = (event: Event) => {
      if (isReadingInteraction(event, readingContent.current)) readingInteraction.current += 1;
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
      (navigationStore.previousActor && navigationStore.previousActor !== navigationScope) ||
      isForbidden(detail.error) ||
      isUnauthenticated(detail.error)
    ) {
      casePositions.clear();
      caseSections.clear();
    }
    navigationStore.previousActor = navigationScope;
    if (!detail.isSuccess || detail.isFetching || !workActorScope || detail.data.id !== caseId)
      return;
    const remembered = casePositions.get(location.key);
    const position =
      caseReadingPosition ??
      (remembered?.caseId === caseId && remembered.actorScope === workActorScope
        ? remembered
        : null);
    const visit = JSON.stringify([location.key, caseId, workActorScope]);
    const uninterrupted =
      readingVisit.current.key === readingVisitKey &&
      readingVisit.current.interaction === readingInteraction.current;
    const frame =
      restoredCaseVisit.current !== visit &&
      position &&
      !location.hash &&
      !selectedRemediationId &&
      uninterrupted
        ? requestAnimationFrame(() => {
            if (
              readingVisit.current.key !== readingVisitKey ||
              readingVisit.current.interaction !== readingInteraction.current
            )
              return;
            restoredCaseVisit.current = visit;
            window.scrollTo(0, position.scrollY);
          })
        : 0;
    if (!position || location.hash || selectedRemediationId || !uninterrupted)
      restoredCaseVisit.current = visit;
    return () => {
      cancelAnimationFrame(frame);
      casePositions.set(location.key, {
        caseId,
        actorScope: workActorScope,
        scrollY: window.scrollY,
      });
      if (casePositions.size > 30) casePositions.delete(casePositions.keys().next().value!);
    };
  }, [
    location.key,
    location.hash,
    caseReadingPosition?.scrollY,
    selectedRemediationId,
    detail.isSuccess,
    detail.isFetching,
    detail.data,
    detail.error,
    workActorScope,
    navigationScope,
    caseId,
    readingVisitKey,
  ]);
  const history = useCaseReviewHistory(caseId, detail.data?.latest_run?.id ?? null);
  const start = useStartReview(caseId);
  const [businessAt, setBusinessAt] = useState("");
  const [reviewScope, setReviewScope] = useState<ReviewScope>("full_review");
  const [playbookRef, setPlaybookRef] = useState<PlaybookRef>();
  const [playbookRefAvailable, setPlaybookRefAvailable] = useState(true);
  const businessAtValue = beijingInputToUtc(businessAt);
  const validBusinessAt = businessAtValue !== "";
  useEffect(() => {
    setBusinessAt("");
    setReviewScope("full_review");
    setPlaybookRef(undefined);
    setPlaybookRefAvailable(true);
  }, [caseId, actorScope]);
  const readiness = useQuery({
    queryKey: [
      "cases",
      caseId,
      "review-readiness",
      actorScope,
      businessAtValue,
      detail.data?.updated_at,
      detail.data?.material_selection,
      detail.data?.documents.map((document) => [document.id, document.parse_status]),
    ],
    queryFn: ({ signal }) => getReviewReadiness(caseId, businessAtValue, signal),
    enabled: detail.isSuccess && session.isSuccess && (businessAt === "" || validBusinessAt),
    retry: false,
  });

  if (detail.isLoading) return <PageStatus kind="loading" title="正在加载案件" />;
  if (detail.isError) {
    if (isForbidden(detail.error)) {
      return (
        <PageStatus
          kind="denied"
          title="没有权限查看这个案件"
          detail={errorMessage(detail.error)}
          action={
            <Button aria-label={"返回案件列表"} type="primary" href="/cases">
              返回案件列表
            </Button>
          }
        />
      );
    }
    return (
      <PageStatus
        kind="error"
        title="案件没有加载"
        detail={errorMessage(detail.error)}
        action={
          <Button
            aria-label={"重试"}
            type="primary"
            htmlType="button"
            onClick={() => void detail.refetch()}
          >
            重试
          </Button>
        }
      />
    );
  }
  if (!detail.data) return <PageStatus kind="empty" title="没有案件数据" />;

  const item = detail.data;
  const hasMaterialProjection = Boolean(
    item.materials && item.material_versions && item.material_selection,
  );
  const materialCountsKnown =
    hasMaterialProjection && item.material_selection?.status !== "pending_confirmation";
  const confirmedDocumentIds = new Set(
    materialCountsKnown ? (item.material_selection?.document_ids ?? []) : [],
  );
  const current = hasMaterialProjection
    ? item.documents.filter((document) => confirmedDocumentIds.has(document.id))
    : [];
  const ready = readyDocumentCount(current.map((document) => document.parse_status));
  const pendingParse = current.filter((document) => document.parse_status === "pending").length;
  const failedCount = current.filter((document) => document.parse_status === "failed").length;
  const archived = item.archived_at !== null;
  const actorRole = session.data?.role ?? "";
  const canUpload = !archived && (actorRole === "admin" || actorRole === "clerk");
  const canWriteRemediation =
    session.isSuccess &&
    !session.isFetching &&
    !detail.isFetching &&
    (actorRole === "admin" || actorRole === "reviewer");
  const canCreateReport = actorRole === "admin" || actorRole === "clerk";
  const canArchive = actorRole === "admin";
  const requiresBusinessAt =
    businessAt !== "" ||
    readiness.data?.scopes.some((scope) =>
      scope.blockers.some((blocker) => blocker.code === "business_at_required"),
    ) === true;
  const latestWorkspace =
    !history.isFetching && !history.isError
      ? history.data?.find((entry) => entry.runId === item.latest_run?.id)?.workspace
      : undefined;
  const readmit =
    latestWorkspace &&
    latestWorkspace.run.id === item.latest_run?.id &&
    latestWorkspace.run.case_id === item.id &&
    latestWorkspace.run.state === item.latest_run.state &&
    canReadmitCancelledPreparation(latestWorkspace)
      ? latestWorkspace.review_attempts?.attempt
      : undefined;
  const updated = compactTime(item.updated_at);
  const scenario = item.scenario?.trim() ? scenarioLabel(item.scenario) : "";
  const contractType =
    item.contract_type && item.contract_type !== "unspecified"
      ? contractTypeLabel(item.contract_type)
      : "";
  const properties = [
    { label: "类型", value: contractType === "合同类型待核对" ? "" : contractType },
    { label: "场景", value: scenario === "—" || scenario.endsWith("待核对") ? "" : scenario },
    {
      label: "材料",
      value: materialCountsKnown
        ? failedCount > 0
          ? `${failedCount} 份解析失败`
          : pendingParse > 0
            ? `${pendingParse} 份待解析`
            : ready > 0
              ? `${ready} 份已解析`
              : ""
        : "",
    },
    { label: "更新", value: updated === "—" ? "" : updated },
    { label: "编号", value: item.case_number?.trim() },
    { label: "负责人", value: item.owner_name?.trim() },
  ].filter((property) => property.value);
  const loadedReviews = (history.data ?? []).filter(
    ({ workspace }) => workspace.run.case_id === item.id,
  );
  const historyComplete = !history.hasOlder && !history.olderError && !history.cycleDetected;

  function onStartReview() {
    if (playbookRef && !playbookRefAvailable) return;
    start.mutate(
      {
        ...(validBusinessAt ? { business_at: businessAtValue } : {}),
        ...(reviewScope === "single_contract_precheck" ? { scope: reviewScope } : {}),
        ...(playbookRef ? { playbook_ref: playbookRef } : {}),
        ...(readmit
          ? { readmit_cancelled: { run_id: readmit.run_id, attempt_id: readmit.id } }
          : {}),
      },
      {
        onSuccess: (accepted) => {
          const query = new URLSearchParams({ attempt_id: accepted.attempt_id });
          void navigate(`/cases/${item.id}/reviews/${accepted.id}?${query}`, {
            state: reviewNavigationState(),
          });
        },
      },
    );
  }

  function reviewNavigationState() {
    return {
      ...(navigationListReturn ?? {}),
      caseReadingPosition: { caseId, actorScope: navigationScope, scrollY: window.scrollY },
      caseSection: activeSection,
    };
  }

  return (
    <SectionNavigationProvider
      scope={`${caseId}:${navigationScope}`}
      actorScope={navigationScope}
      ready={Boolean(
        workActorScope && detail.isSuccess && !detail.isFetching && detail.data.id === caseId,
      )}
      targetPending={targetPending}
      revealTarget={revealTarget}
    >
      <ObjectDetailPage
        contentRef={readingContent}
        className="case-detail-page"
        before={
          location.hash === "#case-material-source" || workReturn ? (
            <nav className="page-back" aria-label="案件导航">
              {location.hash === "#case-material-source" ? (
                <Link to={contractDeskReturnHref(location.state, workActorScope)}>
                  返回合同清单
                </Link>
              ) : null}
              {workReturn ? (
                <Link
                  to={workReturn}
                  state={{ ...(listReturn ?? {}), caseReadingPosition, reviewReadingPosition }}
                >
                  返回人工办理
                </Link>
              ) : null}
            </nav>
          ) : null
        }
        title={item.title}
        status={<span ref={setStageTarget} />}
        actions={
          <>
            <Dropdown
              menu={{
                items: [
                  {
                    key: "compare",
                    label: (
                      <Link to={`/compare?${new URLSearchParams({ case_id: item.id })}`}>
                        版本对照
                      </Link>
                    ),
                  },
                  {
                    key: "legal",
                    label: (
                      <Link to={`/cases/${encodeURIComponent(item.id)}/legal-knowledge`}>
                        法律知识
                      </Link>
                    ),
                  },
                  {
                    key: "support",
                    label: (
                      <Link
                        to={{ hash: "#case-support-grants", search: location.search }}
                        state={location.state}
                      >
                        只读支援
                      </Link>
                    ),
                  },
                  ...(canArchive
                    ? [
                        {
                          key: "archive",
                          label: (
                            <Link
                              to={{ hash: "#case-report-archive", search: location.search }}
                              state={location.state}
                            >
                              归档
                            </Link>
                          ),
                        },
                      ]
                    : []),
                ],
              }}
              trigger={["click"]}
            >
              <Button aria-label="更多案件操作">…</Button>
            </Dropdown>
            <div ref={setActionTarget} />
          </>
        }
        nextStep={
          <>
            <dl className="case-detail-properties" id="case-business-title" aria-label="案件信息">
              {properties.length ? (
                properties.map(({ label, value }) => (
                  <div key={label}>
                    <dt>{label}</dt>
                    <dd>{value}</dd>
                  </div>
                ))
              ) : (
                <div>
                  <dt>案件信息</dt>
                  <dd>—</dd>
                </div>
              )}
            </dl>
            <CaseNextStep
              compact
              actionTarget={actionTarget}
              stageTarget={stageTarget}
              preparationActionTarget={preparationActionTarget}
              caseDetail={item}
              actorRole={actorRole}
              sessionReady={session.isSuccess}
              sessionLoading={session.isLoading}
              sessionError={session.isError ? session.error : null}
              readiness={readiness.data}
              readinessLoading={readiness.isFetching}
              readinessError={readiness.isError ? readiness.error : null}
              selectedScope={reviewScope}
              businessAtInput={businessAt}
              validBusinessAt={validBusinessAt}
              historyLoading={history.isLoading}
              historyError={history.isError ? history.error : null}
              latestWorkspace={latestWorkspace}
              startPending={start.isPending}
              startError={start.isError ? start.error : null}
              startDisabled={Boolean(playbookRef && !playbookRefAvailable)}
              startControls={
                !archived ? (
                  <PlaybookPicker
                    compact
                    caseId={item.id}
                    value={playbookRef}
                    onChange={setPlaybookRef}
                    onAvailabilityChange={setPlaybookRefAvailable}
                    disabled={start.isPending}
                  />
                ) : null
              }
              onRetrySession={() => void session.refetch()}
              onRefreshReadiness={() => {
                start.reset();
                void Promise.all([
                  readiness.refetch(),
                  detail.refetch(),
                  ...(item.latest_run ? [history.refetch()] : []),
                ]);
              }}
              onRefreshHistory={() => void history.refetch()}
              onStartReview={onStartReview}
              onNavigateReview={(href) => void navigate(href, { state: reviewNavigationState() })}
            />
          </>
        }
        navigation={
          <div className="case-detail-content">
            <nav className="case-detail-sections" aria-label="案件详情分区">
              <Tabs
                activeKey={activeSection}
                classNames={{ header: "case-detail-tab-header", item: "case-detail-tab" }}
                destroyOnHidden={false}
                tabBarGutter={12}
                tabBarExtraContent={
                  activeSection === "reviews" &&
                  !archived &&
                  ["admin", "clerk", "reviewer"].includes(actorRole) ? (
                    <a className="case-new-review" href="#review-readiness">
                      发起新审查
                    </a>
                  ) : undefined
                }
                items={[
                  {
                    key: "materials",
                    label: "材料",
                    forceRender: true,
                    children: (
                      <section
                        data-case-section="materials"
                        aria-label="材料"
                        hidden={activeSection !== "materials"}
                      >
                        <MaterialCollectionPanel
                          key={`materials:${item.id}:${actorScope}`}
                          caseDetail={item}
                          canWrite={canUpload}
                          actorId={session.data?.id ?? ""}
                          source={
                            location.hash === "#case-material-source"
                              ? {
                                  documentId: searchParams.get("source_document") ?? "",
                                  visit: location.key,
                                }
                              : undefined
                          }
                        />
                        <CollapsibleSection
                          id="case-template-generation"
                          title="模板生成"
                          summaryLabel="展开模板生成"
                        >
                          <TemplateGenerationPanel
                            key={`generation:${item.id}:${session.data?.id ?? ""}`}
                            caseId={caseId}
                            records={item.generations}
                            materials={item.materials}
                            documents={item.documents}
                            materialSelection={item.material_selection}
                            actorId={session.data?.id ?? ""}
                            canWrite={actorRole === "admin" || actorRole === "clerk"}
                            archived={archived}
                          />
                        </CollapsibleSection>
                      </section>
                    ),
                  },
                  {
                    key: "reviews",
                    label: "审查记录",
                    forceRender: true,
                    children: (
                      <section
                        data-case-section="reviews"
                        aria-label="审查记录"
                        hidden={activeSection !== "reviews"}
                      >
                        {item.latest_run ? (
                          <>
                            {history.isLoading ? <p role="status">正在读取审查记录…</p> : null}
                            {history.isError ? (
                              <p role="alert">审查记录未能读取：{errorMessage(history.error)}</p>
                            ) : null}
                            {loadedReviews.length ? (
                              <table className="case-review-history" aria-label="历次审查">
                                <thead>
                                  <tr>
                                    <th scope="col">审查次序</th>
                                    <th scope="col">纳入材料</th>
                                    <th scope="col">结论进度</th>
                                    <th scope="col">发起时间</th>
                                    <th scope="col">操作</th>
                                  </tr>
                                </thead>
                                <tbody>
                                  {loadedReviews.map(({ runId, workspace }, index) => {
                                    const recordedAttempt = workspace.review_attempts?.attempt;
                                    const attempt =
                                      recordedAttempt?.run_id === runId ? recordedAttempt : null;
                                    const stage = attempt?.state ?? workspace.run.state;
                                    const roles = [
                                      ...new Set(
                                        workspace.documents.map((document) =>
                                          roleLabel(document.role),
                                        ),
                                      ),
                                    ];
                                    const label = historyComplete
                                      ? `第 ${loadedReviews.length - index} 次审查`
                                      : index === 0
                                        ? "最新审查"
                                        : "较早审查";
                                    const href = caseHref(item.id, runId, attempt?.id ?? null);
                                    return (
                                      <tr key={runId}>
                                        <th scope="row">{label}</th>
                                        <td>{roles.length ? roles.join(" · ") : "—"}</td>
                                        <td>
                                          <StatusText
                                            tone={
                                              stage === "partial" ? "attention" : runTone(stage)
                                            }
                                          >
                                            {runLabel(stage)}
                                          </StatusText>
                                        </td>
                                        <td>
                                          {compactTime(
                                            attempt?.accepted_at ?? workspace.run.started_at,
                                          )}
                                        </td>
                                        <td>
                                          <Link
                                            to={href}
                                            aria-label={`进入工作区：${label}`}
                                            state={reviewNavigationState()}
                                            onClick={(event) => {
                                              if (
                                                event.button !== 0 ||
                                                event.metaKey ||
                                                event.ctrlKey ||
                                                event.altKey ||
                                                event.shiftKey
                                              )
                                                return;
                                              event.preventDefault();
                                              void navigate(href, {
                                                state: reviewNavigationState(),
                                              });
                                            }}
                                          >
                                            进入工作区
                                          </Link>
                                        </td>
                                      </tr>
                                    );
                                  })}
                                </tbody>
                              </table>
                            ) : null}
                            {history.cycleDetected ? (
                              <p role="alert">审查记录之间存在循环引用，无法继续读取更早的记录。</p>
                            ) : null}
                            {history.olderError ? (
                              <p role="alert">
                                更早的审查记录未能读取：{errorMessage(history.olderError)}
                              </p>
                            ) : null}
                            {history.hasOlder ? (
                              <Button
                                htmlType="button"
                                loading={history.isLoadingOlder}
                                onClick={() => void history.fetchOlder()}
                              >
                                读取更早审查记录
                              </Button>
                            ) : null}
                          </>
                        ) : (
                          <p>尚未发起审查。</p>
                        )}
                        <CollapsibleSection
                          key={`${item.id}:${Boolean(loadedReviews.length)}`}
                          id="case-review-preparation"
                          title="审查准备"
                          defaultOpen={
                            !loadedReviews.length ||
                            ["#review-readiness", "#review-business-time"].includes(location.hash)
                          }
                        >
                          <ReviewReadinessPanel
                            caseId={item.id}
                            data={readiness.data}
                            loading={readiness.isFetching}
                            error={readiness.isError ? readiness.error : null}
                            onRefresh={() => {
                              start.reset();
                              void Promise.all([
                                readiness.refetch(),
                                detail.refetch(),
                                ...(item.latest_run ? [history.refetch()] : []),
                              ]);
                            }}
                            selectedScope={reviewScope}
                            onSelectScope={setReviewScope}
                            business={{
                              business_line: item.business_line,
                              scenario: item.scenario,
                              contract_type: item.contract_type,
                            }}
                            businessTime={
                              requiresBusinessAt ? (
                                <label className="review-preflight-time" id="review-business-time">
                                  审查业务时间（北京时间）
                                  <Input
                                    aria-label="审查业务时间（北京时间）"
                                    type="datetime-local"
                                    required
                                    disabled={archived}
                                    value={businessAt}
                                    onChange={(event) => setBusinessAt(event.target.value)}
                                  />
                                  {businessAt !== "" && !validBusinessAt ? (
                                    <span role="alert">请输入有效的北京时间。</span>
                                  ) : null}
                                </label>
                              ) : null
                            }
                            footer={
                              !archived && ["admin", "clerk", "reviewer"].includes(actorRole) ? (
                                <div
                                  className="review-preflight-actions"
                                  ref={setPreparationActionTarget}
                                />
                              ) : null
                            }
                          />
                        </CollapsibleSection>
                      </section>
                    ),
                  },
                  {
                    key: "work",
                    label: "整改与报告",
                    forceRender: true,
                    children: (
                      <section
                        data-case-section="work"
                        aria-label="整改与报告"
                        hidden={activeSection !== "work"}
                      >
                        <CollapsibleSection
                          id="case-remediation"
                          title="整改与复核"
                          summaryLabel="展开整改与复核"
                          defaultOpen={Boolean(selectedRemediationId)}
                          openRequest={
                            session.isSuccess &&
                            !session.isFetching &&
                            detail.isSuccess &&
                            !detail.isFetching
                              ? (selectedRemediationId ?? undefined)
                              : undefined
                          }
                        >
                          <RemediationPanel
                            caseId={caseId}
                            detail={item}
                            history={history.data ?? []}
                            historyLoading={history.isLoading}
                            historyError={history.isError ? history.error : null}
                            historyHasOlder={history.hasOlder}
                            canWrite={canWriteRemediation}
                            archived={archived}
                            selectedRemediationId={
                              session.isSuccess && !session.isFetching && !detail.isFetching
                                ? selectedRemediationId
                                : null
                            }
                          />
                        </CollapsibleSection>
                        <CollapsibleSection
                          id="case-report-archive"
                          title="审查报告与归档"
                          summaryLabel="展开审查报告与归档"
                        >
                          <ReportArchivePanel
                            caseId={caseId}
                            detail={item}
                            history={history.data ?? []}
                            historyLoading={history.isLoading}
                            historyError={history.isError ? history.error : null}
                            historyHasOlder={history.hasOlder}
                            canCreateReport={canCreateReport}
                            canArchive={canArchive}
                            canReadReports={canCreateReport}
                            archived={archived}
                          />
                        </CollapsibleSection>
                      </section>
                    ),
                  },
                  {
                    key: "subjects",
                    label: "主体与活动",
                    forceRender: true,
                    children: (
                      <section
                        data-case-section="subjects"
                        aria-label="主体与活动"
                        hidden={activeSection !== "subjects"}
                      >
                        <div id="case-subjects">
                          <CollapsibleSection id="case-subject-roles" title="签约主体" defaultOpen>
                            <CaseSubjectRolesPanel
                              key={`subjects:${item.id}:${actorScope}`}
                              caseId={item.id}
                            />
                          </CollapsibleSection>
                        </div>
                        <div id="case-subject-corrections">
                          <CollapsibleSection
                            id="case-subject-corrections-section"
                            title="主体信息更正"
                            summaryLabel="展开主体信息更正"
                          >
                            <CaseSubjectCorrectionsPanel
                              key={`subject-corrections:${item.id}:${actorScope}`}
                              caseId={item.id}
                            />
                          </CollapsibleSection>
                        </div>
                        <CollapsibleSection
                          id="case-legal-applicability"
                          title="适用依据"
                          summaryLabel="展开适用依据"
                        >
                          <CaseLegalApplicabilityPanel
                            key={`legal-applicability:${item.id}:${actorScope}`}
                            caseId={item.id}
                          />
                        </CollapsibleSection>
                        <CollapsibleSection
                          id="case-support-grants"
                          title="只读支援"
                          summaryLabel="展开只读支援"
                        >
                          <CaseSupportGrantPanel
                            key={`support:${item.id}:${actorScope}`}
                            caseId={item.id}
                          />
                          <EvidenceSLADashboard
                            mode="reminder"
                            cases={[{ id: item.id, title: item.title }]}
                          />
                        </CollapsibleSection>
                        <CollapsibleSection
                          id="case-recent-activity"
                          title="最近活动"
                          defaultOpen
                          summaryLabel="展开最近活动"
                          count={item.activity.length}
                        >
                          {item.activity.length === 0 ? <p>还没有活动记录。</p> : null}
                          <ol className="history">
                            {item.activity.map((entry) => (
                              <li key={entry.id}>
                                <time dateTime={entry.at}>{formatDateTime(entry.at)}</time>
                                <p>{entry.summary}</p>
                              </li>
                            ))}
                          </ol>
                        </CollapsibleSection>
                      </section>
                    ),
                  },
                ]}
                onChange={(key) => {
                  setSectionSelection({ visit: sectionVisit, section: key as CaseSection });
                }}
              />
            </nav>
          </div>
        }
      />
    </SectionNavigationProvider>
  );
}
