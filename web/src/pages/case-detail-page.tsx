import { Button, Input } from "antd";
import { useEffect, useRef, useState } from "react";
import { Link, useLocation, useNavigate, useParams, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { getReviewReadiness, type ReviewScope } from "../api/review-readiness";
import { beijingInputToUtc } from "../components/beijing-datetime";
import { ReviewReadinessPanel } from "../components/review-readiness-panel";
import { errorMessage, isForbidden } from "../api/http";
import {
  useCaseDetail,
  useCaseReviewHistory,
  useSession,
  useStartReview,
} from "../api/queries";
import { PageStatus } from "../components/page-status";
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
import { CaseSupportGrantPanel } from "./support-grant-pages";
import {
  businessStatusLabel,
  contractTypeLabel,
  formatDateTime,
  readyDocumentCount,
  runLabel,
  runTone,
} from "../domain/review";

export function CaseDetailPage() {
  const { caseId = "" } = useParams();
  const navigate = useNavigate();
  const location = useLocation();
  const [searchParams, setSearchParams] = useSearchParams();
  const returnState = location.state as {
    returnTo?: unknown;
    scrollY?: unknown;
  } | null;
  const returnTo =
    typeof returnState?.returnTo === "string" &&
    returnState.returnTo.startsWith("/cases")
      ? returnState.returnTo
      : "/cases";
  const returnScrollY =
    typeof returnState?.scrollY === "number" ? returnState.scrollY : 0;
  const detail = useCaseDetail(caseId);
  const session = useSession();
  const actorScope = [session.data?.id ?? "", session.data?.tenant_id ?? "", session.data?.org_id ?? "", session.data?.audience ?? ""].join(":");
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
  const needsNewReview = item.revision_pending || item.configuration_pending || item.subject_context_changed_after_latest_run === true;
  const actorRole = session.data?.role ?? "";
  const canUpload =
    !archived && (actorRole === "admin" || actorRole === "clerk");
  const canWriteRemediation = actorRole === "admin" || actorRole === "reviewer";
  const canCreateReport = actorRole === "admin" || actorRole === "clerk";
  const canArchive = actorRole === "admin";
  const requiresBusinessAt = businessAt !== "" ||
    readiness.data?.scopes.some((scope) => scope.blockers.some((blocker) => blocker.code === "business_at_required")) === true;
  function onStartReview() {
    start.mutate(
      {
        ...(validBusinessAt ? { business_at: businessAtValue } : {}),
        ...(reviewScope === "single_contract_precheck" ? { scope: reviewScope } : {}),
      },
      {
        onSuccess: (accepted) => {
          const query = new URLSearchParams({ attempt_id: accepted.attempt_id });
          void navigate(`/cases/${item.id}/reviews/${accepted.id}?${query}`);
        },
      },
    );
  }

  return (
    <section className="page">
      <nav className="page-back" aria-label="案件导航">
        <Link to={returnTo} state={{ restoreScrollY: returnScrollY }}>
          返回案件列表
        </Link>
      </nav>
      <header className="page-head">
        <div>
          <h1>{item.title}</h1>
          <p>
            {[
              item.business_line,
              item.scenario,
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
        startPending={start.isPending}
        startError={start.isError ? start.error : null}
        onRetrySession={() => void session.refetch()}
        onRefreshReadiness={() => void readiness.refetch()}
        onRefreshHistory={() => void history.refetch()}
        onStartReview={onStartReview}
      />
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
          案件已归档，只能读取案件、运行历史和已有报告。服务端会以 HTTP 409
          拒绝所有业务写入。
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
      {run &&
      !needsNewReview &&
      (run.state === "failed" || run.state === "partial") ? (
        <p className="meta">
          失败或部分完成时，在工作区里重试同一次运行，避免另开一条运行把已有决定拆开。
        </p>
      ) : null}
      {run && run.state !== "queued" && run.state !== "running" ? (
        <p className="meta">新上传的材料不会自动进入已经开始的审查。</p>
      ) : null}

      <ReviewReadinessPanel caseId={item.id} data={readiness.data} loading={readiness.isFetching}
        error={readiness.isError ? readiness.error : null} onRefresh={() => void readiness.refetch()}
        selectedScope={reviewScope} onSelectScope={setReviewScope} />
      <div id="case-materials">
        <MaterialCollectionPanel key={`materials:${item.id}:${actorScope}`} caseDetail={item} canWrite={canUpload} actorId={session.data?.id ?? ""} />
      </div>
      <div id="case-subjects">
        <CaseSubjectRolesPanel key={`subjects:${item.id}:${actorScope}`} caseId={item.id} />
      </div>
      <div id="case-subject-corrections">
        <CaseSubjectCorrectionsPanel key={`subject-corrections:${item.id}:${actorScope}`} caseId={item.id} />
      </div>
      <CaseLegalApplicabilityPanel key={`legal-applicability:${item.id}:${actorScope}`} caseId={item.id} />

      <section
        className="case-business-overview"
        aria-labelledby="case-business-title"
      >
        <h2 id="case-business-title">业务信息</h2>
        <dl className="execution-dl">
          <div>
            <dt>业务条线</dt>
            <dd>{item.business_line || "未提供"}</dd>
          </div>
          <div>
            <dt>场景</dt>
            <dd>{item.scenario || "未提供"}</dd>
          </div>
          <div>
            <dt>业务状态</dt>
            <dd>{item.business_status || "未提供"}</dd>
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
      </section>
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
      <RemediationPanel
        caseId={caseId}
        detail={item}
        history={history.data ?? []}
        historyLoading={history.isLoading}
        historyError={history.isError ? history.error : null}
        canWrite={canWriteRemediation}
        archived={archived}
      />
      <ReportArchivePanel
        caseId={caseId}
        detail={item}
        history={history.data ?? []}
        historyLoading={history.isLoading}
        historyError={history.isError ? history.error : null}
        canCreateReport={canCreateReport}
        canArchive={canArchive}
        canReadReports={canCreateReport}
        archived={archived}
      />

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

      <section>
        <h2>最近活动</h2>
        {item.activity.length === 0 ? <p>还没有活动记录。</p> : null}
        <ol className="history">
          {item.activity.map((entry) => (
            <li key={entry.id}>
              <time dateTime={entry.at}>{formatDateTime(entry.at)}</time>
              <p>{entry.summary}</p>
            </li>
          ))}
        </ol>
      </section>
    </section>
  );
}
