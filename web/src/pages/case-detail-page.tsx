// File metadata (manifest mirror):
// Purpose: Composes one case workspace from the case profile, material versions, review readiness, pending work and review history; all mutations remain routed through case-scoped API commands.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-06
// Revision: r9
// Copyright: See LICENSE
// End file metadata.

import { Button, Input } from "antd";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Link, useLocation, useNavigate, useParams, useSearchParams } from "react-router";
import { useIsFetching, useQuery, useQueryClient } from "@tanstack/react-query";
import { getReviewReadiness, type ReviewScope } from "../api/review-readiness";
import { beijingInputToUtc } from "../components/beijing-datetime";
import { ReviewReadinessPanel } from "../components/review-readiness-panel";
import { errorMessage, isForbidden, isUnauthenticated } from "../api/http";
import {
  useCaseDetail,
  useCaseReviewHistory,
  useSession,
  useStartReview,
} from "../api/queries";
import { PageStatus } from "../components/page-status";
import { CollapsibleSection, SectionAnchorNav } from "../components/collapsible-section";
import { isReadingInteraction, readCaseListReturn, readCaseReadingPosition, reviewPositionForPath, SectionNavigationProvider, type CaseReadingPosition } from "../components/section-navigation";
import { CaseNextStep } from "../components/case-next-step";
import { MaterialCollectionPanel } from "../components/material-collection-panel";
import { CaseSubjectRolesPanel } from "../components/case-subject-roles-panel";
import { CaseSubjectCorrectionsPanel } from "../components/case-subject-corrections-panel";
import { CaseLegalApplicabilityPanel } from "../components/case-legal-applicability-panel";
import { ReportArchivePanel } from "../components/report-archive-panel";
import { EvidenceSLADashboard } from "../components/evidence-sla-dashboard";
import { RemediationPanel } from "../components/remediation-panel";
import { TemplateGenerationPanel } from "../components/template-generation-panel";
import { StatusText } from "../components/status-text";
import { TechnicalDetail } from "../components/technical-detail";
import { businessLineLabel, businessScopeLabel, scenarioLabel } from "../domain/business-labels";
import { CaseSupportGrantPanel } from "./support-grant-pages";
import { canReadmitCancelledPreparation } from "../features/review/review-outcome";
import { readWorkReturn } from "../features/review/work-items";
import { actorReadContextKey } from "../domain/actor-read-context";
import {
  businessStatusLabel,
  contractTypeLabel,
  formatDateTime,
  readyDocumentCount,
  runLabel,
  runTone,
} from "../domain/review";

const casePositions = new Map<string, CaseReadingPosition>();
let previousCaseActor = "";

export function CaseDetailPage() {
  const { caseId = "" } = useParams();
  const navigate = useNavigate();
  const location = useLocation();
  const [searchParams, setSearchParams] = useSearchParams();
  const detail = useCaseDetail(caseId);
  const session = useSession();
  const workActorScope = session.isSuccess && !session.isFetching ? actorReadContextKey(session.data) : "";
  const navigationScope = session.data ? actorReadContextKey(session.data) : "";
  const restoredCaseVisit = useRef("");
  const readingContent = useRef<HTMLElement>(null);
  const readingInteraction = useRef(0);
  const readingVisit = useRef<{ key: string; interaction: number }>({ key: "", interaction: 0 });
  const readingVisitKey = JSON.stringify([location.key, caseId]);
  const previousNavigationScope = useRef(navigationScope);
  const navigationActorChanged = Boolean(workActorScope && previousNavigationScope.current && previousNavigationScope.current !== workActorScope);
  useEffect(() => { if (workActorScope) previousNavigationScope.current = workActorScope; }, [workActorScope]);
  const listReturn = readCaseListReturn(location.state, caseId, workActorScope);
  const navigationListReturn = readCaseListReturn(location.state, caseId, navigationScope);
  const caseReadingPosition = readCaseReadingPosition(location.state, caseId, workActorScope);
  const queryClient = useQueryClient();
  const legalFetching = useIsFetching({ queryKey: ["cases", caseId, "legal-applicability-facts"] });
  const targetPending = useCallback((id: string) => id.startsWith("legal-fact-") &&
    (legalFetching > 0 || queryClient.getQueryCache().findAll({ queryKey: ["cases", caseId, "legal-applicability-facts"] }).length === 0),
  [caseId, legalFetching, queryClient]);
  const incomingWork = location.state && typeof location.state === "object" && "workReturn" in location.state
    ? location.state.workReturn : null;
  const incomingScope = incomingWork && typeof incomingWork === "object" && "actorScope" in incomingWork ? incomingWork.actorScope : null;
  const workScopeMismatch = navigationActorChanged || Boolean(workActorScope && typeof incomingScope === "string" && incomingScope !== workActorScope);
  const selectedRemediationId = !workScopeMismatch && searchParams.get("work_kind") === "remediation" ? searchParams.get("work_item") : null;
  const workReturn = readWorkReturn(location.state, caseId, workActorScope);
  const reviewReadingPosition = workReturn ? reviewPositionForPath(location.state, workReturn, caseId, workActorScope) : null;
  useEffect(() => {
    if (!workScopeMismatch) return;
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      next.delete("work_kind");
      next.delete("work_item");
      return next;
    }, { replace: true, state: null });
  }, [workScopeMismatch, setSearchParams]);
  useEffect(() => {
    if (!selectedRemediationId || !session.isSuccess || session.isFetching || !detail.isSuccess || detail.isFetching) return;
    const section = document.getElementById("case-remediation");
    if (section instanceof HTMLDetailsElement) section.open = true;
  }, [selectedRemediationId, session.isSuccess, session.isFetching, detail.isSuccess, detail.isFetching]);
  const actorScope = navigationScope;
  const previousActorScope = useRef(actorScope);
  const sourceCaseParam = searchParams.get("source_case");
  useEffect(() => {
    const actorChanged = previousActorScope.current !== "" && previousActorScope.current !== actorScope;
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
    if ((previousCaseActor && previousCaseActor !== navigationScope) || isForbidden(detail.error) || isUnauthenticated(detail.error)) casePositions.clear();
    previousCaseActor = navigationScope;
    if (!detail.isSuccess || detail.isFetching || !workActorScope || detail.data.id !== caseId) return;
    const remembered = casePositions.get(location.key);
    const position = caseReadingPosition ?? (remembered?.caseId === caseId && remembered.actorScope === workActorScope ? remembered : null);
    const visit = JSON.stringify([location.key, caseId, workActorScope]);
    const uninterrupted = readingVisit.current.key === readingVisitKey && readingVisit.current.interaction === readingInteraction.current;
    const frame = restoredCaseVisit.current !== visit && position && !location.hash && !selectedRemediationId && uninterrupted
      ? requestAnimationFrame(() => {
        if (readingVisit.current.key !== readingVisitKey || readingVisit.current.interaction !== readingInteraction.current) return;
        restoredCaseVisit.current = visit;
        window.scrollTo(0, position.scrollY);
      }) : 0;
    if (!position || location.hash || selectedRemediationId || !uninterrupted) restoredCaseVisit.current = visit;
    return () => {
      cancelAnimationFrame(frame);
      casePositions.set(location.key, { caseId, actorScope: workActorScope, scrollY: window.scrollY });
      if (casePositions.size > 30) casePositions.delete(casePositions.keys().next().value!);
    };
  }, [location.key, location.hash, caseReadingPosition?.scrollY, selectedRemediationId, detail.isSuccess, detail.isFetching, detail.data, detail.error, workActorScope, navigationScope, caseId, readingVisitKey]);
  const history = useCaseReviewHistory(
    caseId,
    detail.data?.latest_run?.id ?? null,
  );
  const start = useStartReview(caseId);
  const [businessAt, setBusinessAt] = useState("");
  const [reviewScope, setReviewScope] = useState<ReviewScope>("full_review");
  const businessAtValue = beijingInputToUtc(businessAt);
  const validBusinessAt = businessAtValue !== "";
  useEffect(() => { setBusinessAt(""); setReviewScope("full_review"); }, [caseId, actorScope]);
  const readiness = useQuery({
    queryKey: ["cases", caseId, "review-readiness", actorScope, businessAtValue,
      detail.data?.updated_at, detail.data?.material_selection,
      detail.data?.documents.map((document) => [document.id, document.parse_status])],
    queryFn: ({ signal }) => getReviewReadiness(caseId, businessAtValue, signal),
    enabled: detail.isSuccess && session.isSuccess && (businessAt === "" || validBusinessAt),
    retry: false,
  });

  if (detail.isLoading)
    return <PageStatus kind="loading" title="正在加载案件" />;
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
  const confirmedDocumentIds = new Set(
    hasMaterialProjection && item.material_selection?.status !== "pending_confirmation"
      ? item.material_selection?.document_ids ?? []
      : [],
  );
  const current = hasMaterialProjection
    ? item.documents.filter((document) => confirmedDocumentIds.has(document.id))
    : [];
  const ready = readyDocumentCount(
    current.map((document) => document.parse_status),
  );
  const pendingParse = current.filter(
    (document) => document.parse_status === "pending",
  ).length;
  const failedCount = current.filter(
    (document) => document.parse_status === "failed",
  ).length;
  const failed = failedCount > 0;
  const run = item.latest_run;
  const archived = item.archived_at !== null;
  const subjectContextUnknown = run !== null && item.subject_context_changed_after_latest_run === undefined;
  const actorRole = session.data?.role ?? "";
  const canUpload =
    !archived && (actorRole === "admin" || actorRole === "clerk");
  const canWriteRemediation = session.isSuccess && !session.isFetching && !detail.isFetching && (actorRole === "admin" || actorRole === "reviewer");
  const canCreateReport = actorRole === "admin" || actorRole === "clerk";
  const canArchive = actorRole === "admin";
  const requiresBusinessAt = businessAt !== "" ||
    readiness.data?.scopes.some((scope) => scope.blockers.some((blocker) => blocker.code === "business_at_required")) === true;
  const latestWorkspace = !history.isFetching && !history.isError
    ? history.data?.find((entry) => entry.runId === item.latest_run?.id)?.workspace
    : undefined;
  const readmit = latestWorkspace && latestWorkspace.run.id === item.latest_run?.id &&
    latestWorkspace.run.case_id === item.id && latestWorkspace.run.state === item.latest_run.state &&
    canReadmitCancelledPreparation(latestWorkspace) ? latestWorkspace.review_attempts?.attempt : undefined;
  function onStartReview() {
    start.mutate(
      {
        ...(validBusinessAt ? { business_at: businessAtValue } : {}),
        ...(reviewScope === "single_contract_precheck" ? { scope: reviewScope } : {}),
        ...(readmit ? { readmit_cancelled: { run_id: readmit.run_id, attempt_id: readmit.id } } : {}),
      },
      {
        onSuccess: (accepted) => {
          const query = new URLSearchParams({ attempt_id: accepted.attempt_id });
          void navigate(`/cases/${item.id}/reviews/${accepted.id}?${query}`, { state: reviewNavigationState() });
        },
      },
    );
  }

  function reviewNavigationState() {
    return {
      ...(navigationListReturn ?? {}),
      caseReadingPosition: { caseId, actorScope: navigationScope, scrollY: window.scrollY },
    };
  }

  return (
    <SectionNavigationProvider scope={`${caseId}:${navigationScope}`} actorScope={navigationScope} ready={Boolean(workActorScope && detail.isSuccess && !detail.isFetching && detail.data.id === caseId)} targetPending={targetPending}>
    <section ref={readingContent} className="page">
      <nav className="page-back" aria-label="案件导航">
        {workReturn ? <Link to={workReturn} state={{ ...(listReturn ?? {}), caseReadingPosition, reviewReadingPosition }}>返回人工办理</Link> : null}
        <Link to={navigationListReturn?.returnTo ?? "/cases"} state={{ restoreScrollY: navigationListReturn?.scrollY ?? 0, actorScope: navigationScope }}>
          返回案件列表
        </Link>
      </nav>
      <header className="page-head">
        <div>
          <h1>{item.title}</h1>
          <p>
            {[
              businessScopeLabel(item.business_line, item.scenario),
              businessStatusLabel(item.business_status),
              contractTypeLabel(item.contract_type),
            ]
              .filter(Boolean)
              .join("，")}{" "}
          ，更新于 {formatDateTime(item.updated_at)}
          </p>
        </div>
        <EvidenceSLADashboard
          mode="reminder"
          cases={[{ id: item.id, title: item.title }]}
        />
        <Button href={`/cases/${encodeURIComponent(item.id)}/legal-knowledge`}>法律知识</Button>
      </header>
      <CaseNextStep
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
        onRetrySession={() => void session.refetch()}
        onRefreshReadiness={() => {
          start.reset();
          void Promise.all([readiness.refetch(), detail.refetch(), ...(item.latest_run ? [history.refetch()] : [])]);
        }}
        onRefreshHistory={() => void history.refetch()}
        onStartReview={onStartReview}
        onNavigateReview={(href) => void navigate(href, { state: reviewNavigationState() })}
      />
      {item.latest_run ? (
        <section className="workflow-panel" aria-label="审查历史加载范围">
          <p className="meta">
            默认先读取最新审查记录；更早记录按需读取，未读取的旧记录不会出现在整改或报告选择中。
          </p>
          {history.cycleDetected ? (
            <p role="alert">审查历史存在循环引用，已保留当前可读取记录，不能继续读取更早记录。</p>
          ) : null}
          {history.olderError ? (
            <p className="workflow-error" role="alert">
              更早的审查记录没有加载：{errorMessage(history.olderError)}
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
        </section>
      ) : null}
      <SectionAnchorNav items={[
        { id: "review-readiness", label: "审查准备" },
        { id: "case-materials", label: "案件材料" },
        { id: "case-subjects", label: "案件主体" },
        { id: "case-legal-applicability", label: "法律适用事实" },
        { id: "case-business-title", label: "业务信息" },
        { id: "remediation-title", label: "整改与复核" },
        { id: "report-archive-title", label: "审查报告与归档" },
        { id: "case-recent-activity", label: "最近活动", count: item.activity.length },
      ]} />
      <CaseSupportGrantPanel caseId={item.id} />
      {requiresBusinessAt ? (
        <label className="field" id="review-business-time">
          审查业务时间（北京时间）
          <Input
            aria-label="审查业务时间（北京时间）"
            type="datetime-local"
            required
            disabled={archived}
            value={businessAt}
            onChange={(event) => setBusinessAt(event.target.value)}
          />
          <span className="meta">按北京时间（UTC+08:00）选择适用的业务时点，不以服务器当前时间代替。</span>
          {businessAt !== "" && !validBusinessAt ? <span role="alert">请输入有效的北京时间。</span> : null}
        </label>
      ) : null}
      <ol className="case-chain" aria-label="案件材料链路">
        <li data-state={current.length > 0 ? "filled" : "open"}>
          <span className="case-chain-mark" aria-hidden="true" />
          <span className="case-chain-label">材料</span>
          <strong>{current.length}</strong>
        </li>
        <li data-state={ready > 0 ? "filled" : "open"}>
          <span className="case-chain-mark" aria-hidden="true" />
          <span className="case-chain-label">已解析</span>
          <strong>{ready}</strong>
        </li>
        <li data-state={pendingParse > 0 ? "open" : "filled"}>
          <span className="case-chain-mark" aria-hidden="true" />
          <span className="case-chain-label">待解析</span>
          <strong>{pendingParse}</strong>
        </li>
        <li data-state={failed ? "open" : "filled"}>
          <span className="case-chain-mark" aria-hidden="true" />
          <span className="case-chain-label">解析失败</span>
          <strong>{failedCount}</strong>
        </li>
        <li data-state={run ? "filled" : "open"}>
          <span className="case-chain-mark" aria-hidden="true" />
          <span className="case-chain-label">最新运行</span>
          <strong>{run ? runLabel(run.state) : "尚无运行"}</strong>
        </li>
      </ol>
      {archived ? (
        <p className="callout">
          案件已归档，目前仅可查看案件、审查历史和已有报告。
        </p>
      ) : null}
      {pendingParse > 0 ? (
        <p className="meta">
          待解析材料将在审查受理后处理，解析失败会保留失败状态。
        </p>
      ) : null}
      {failed ? (
        <p className="callout">
          有材料解析失败。文件仍保留在案件里，不会被当成无风险。
        </p>
      ) : null}
      {item.revision_pending ? (
        <p className="callout">
          材料已更新。开始复审会创建新的审查记录，之前的审查和人工判断都会保留。
        </p>
      ) : null}
      {item.configuration_pending ? (
        <p className="callout">
          规则或模型配置已更新。开始复审会记录当前配置，保留历史运行。
        </p>
      ) : null}
      {item.subject_context_changed_after_latest_run === true ? (
        <p className="callout">主体信息已变更，需发起新审查。新审查会冻结当前主体信息，之前的审查和报告保持不变。</p>
      ) : null}
      {subjectContextUnknown ? (
        <p className="callout">服务未提供主体信息变化状态，无法确认当前主体信息是否与最近审查一致。请刷新或联系管理员。</p>
      ) : null}
      {item.material_selection?.status === "pending_confirmation" ? (
        <p className="callout">请先确认后续审查使用的旧材料集合。确认前不会按角色推断选中文档，也不会启动新审查。</p>
      ) : null}
      {!hasMaterialProjection ? (
        <p className="callout">服务未提供材料集合与版本信息，当前不能启动新审查。</p>
      ) : null}
      {run && run.state !== "queued" && run.state !== "running" ? (
        <p className="meta">新上传的材料不会自动进入已经开始的审查。</p>
      ) : null}

      <ReviewReadinessPanel caseId={item.id} data={readiness.data} loading={readiness.isFetching}
        error={readiness.isError ? readiness.error : null} onRefresh={() => void readiness.refetch()}
        selectedScope={reviewScope} onSelectScope={setReviewScope}
        business={{ business_line: item.business_line, scenario: item.scenario, contract_type: item.contract_type }} />
      <div id="case-materials">
        <MaterialCollectionPanel key={`materials:${item.id}:${actorScope}`} caseDetail={item} canWrite={canUpload} actorId={session.data?.id ?? ""} />
      </div>
      <div id="case-subjects">
        <CollapsibleSection id="case-subject-roles" title="案件主体与角色" summaryLabel="展开案件主体与角色">
          <CaseSubjectRolesPanel key={`subjects:${item.id}:${actorScope}`} caseId={item.id} />
        </CollapsibleSection>
      </div>
      <div id="case-subject-corrections">
        <CollapsibleSection id="case-subject-corrections-section" title="主体更正与复核" summaryLabel="展开主体更正与复核">
          <CaseSubjectCorrectionsPanel key={`subject-corrections:${item.id}:${actorScope}`} caseId={item.id} />
        </CollapsibleSection>
      </div>
      <div id="case-legal-applicability"><CaseLegalApplicabilityPanel key={`legal-applicability:${item.id}:${actorScope}`} caseId={item.id} /></div>

      <section
        className="case-business-overview"
        aria-labelledby="case-business-title"
      >
        <h2 id="case-business-title">业务信息</h2>
        <dl className="execution-dl">
          <div>
            <dt>业务条线</dt>
            <dd>{businessLineLabel(item.business_line)}</dd>
          </div>
          <div>
            <dt>场景</dt>
            <dd>{scenarioLabel(item.scenario)}</dd>
          </div>
          <div>
            <dt>业务状态</dt>
            <dd>{businessStatusLabel(item.business_status)}</dd>
          </div>
          <div>
            <dt>归档状态</dt>
            <dd>
              {archived
                ? `已归档，${formatDateTime(item.archived_at ?? "")}`
                : "未归档"}
            </dd>
          </div>
        </dl>
        <TechnicalDetail label="业务范围编码" value={[item.business_line, item.scenario, item.contract_type, item.business_status].filter(Boolean).join(" / ")} />
      </section>
      <CollapsibleSection id="case-template-generation" title="模板生成" summaryLabel="展开模板生成">
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
      <CollapsibleSection id="case-remediation" title="整改与复核" summaryLabel="展开整改与复核" defaultOpen={Boolean(selectedRemediationId)}>
        <RemediationPanel
          caseId={caseId}
          detail={item}
          history={history.data ?? []}
          historyLoading={history.isLoading}
          historyError={history.isError ? history.error : null}
          historyHasOlder={history.hasOlder}
          canWrite={canWriteRemediation}
          archived={archived}
          selectedRemediationId={session.isSuccess && !session.isFetching && !detail.isFetching ? selectedRemediationId : null}
        />
      </CollapsibleSection>
      <CollapsibleSection id="case-report-archive" title="审查报告与归档" summaryLabel="展开审查报告与归档">
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

      <section>
        <h2>问题摘要</h2>
        {run ? (
          <p>
            最新运行{" "}
            <StatusText tone={runTone(run.state)}>
              {runLabel(run.state)}
            </StatusText>
          </p>
        ) : (
          <p>尚未审查。没有运行结果，不能写成无风险。</p>
        )}
        {item.finding_counts ? (
          <ul className="count-list">
            <li>线索 {item.finding_counts.total}</li>
            <li>冲突 {item.finding_counts.conflict}</li>
            <li>信息不足 {item.finding_counts.insufficient}</li>
            <li>待裁决 {item.finding_counts.undecided}</li>
          </ul>
        ) : (
          <p>服务没有返回问题摘要。</p>
        )}
        {item.finding_counts?.total === 0 && run ? (
          <p>这次运行没有形成风险线索。这不等于材料无风险。</p>
        ) : null}
      </section>

      <CollapsibleSection id="case-recent-activity" title="最近活动" summaryLabel="展开最近活动" count={item.activity.length}>
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
    </SectionNavigationProvider>
  );
}
