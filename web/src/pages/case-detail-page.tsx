import { useState, type FormEvent } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router";
import { errorMessage, isForbidden } from "../api/http";
import { useCaseDetail, useCaseReviewHistory, useReparse, useSession, useStartReview, useUploadDocument } from "../api/queries";
import { PageStatus } from "../components/page-status";
import { ParsedDocumentSummary } from "../components/parsed-document-summary";
import { ReportArchivePanel } from "../components/report-archive-panel";
import { RemediationPanel } from "../components/remediation-panel";
import { StatusText } from "../components/status-text";
import { TemplateGenerationPanel } from "../components/template-generation-panel";
import {
  contractTypeLabel,
  formatBytes,
  formatDateTime,
  parseLabel,
  parseTone,
  readyDocumentCount,
  roleLabel,
  runLabel,
  runTone,
} from "../domain/review";

const ROLES = [
  { value: "procurement", label: "采购文件" },
  { value: "award", label: "中标文件" },
  { value: "draft_contract", label: "拟签合同" },
  { value: "attachment", label: "技术附件" },
];

export function CaseDetailPage() {
  const { caseId = "" } = useParams();
  const navigate = useNavigate();
  const location = useLocation();
  const returnState = location.state as { returnTo?: unknown; scrollY?: unknown } | null;
  const returnTo = typeof returnState?.returnTo === "string" && returnState.returnTo.startsWith("/cases") ? returnState.returnTo : "/cases";
  const returnScrollY = typeof returnState?.scrollY === "number" ? returnState.scrollY : 0;
  const detail = useCaseDetail(caseId);
  const session = useSession();
  const history = useCaseReviewHistory(caseId, detail.data?.latest_run?.id ?? null);
  const upload = useUploadDocument(caseId);
  const reparse = useReparse(caseId);
  const start = useStartReview(caseId);
  const [role, setRole] = useState("procurement");
  const [file, setFile] = useState<File | null>(null);
  const [uploadKey, setUploadKey] = useState(0);

  if (detail.isLoading) return <PageStatus kind="loading" title="正在加载案件" />;
  if (detail.isError) {
    if (isForbidden(detail.error)) {
      return (
        <PageStatus
          kind="denied"
          title="没有权限查看这个案件"
          detail={errorMessage(detail.error)}
          action={<Link className="button button-primary" to="/cases">返回案件列表</Link>}
        />
      );
    }
    return (
      <PageStatus
        kind="error"
        title="案件没有加载"
        detail={errorMessage(detail.error)}
        action={
          <button className="button button-primary" type="button" onClick={() => void detail.refetch()}>
            重试
          </button>
        }
      />
    );
  }
  if (!detail.data) return <PageStatus kind="empty" title="没有案件数据" />;

  const item = detail.data;
  const current = item.documents.filter((document) => !document.superseded);
  const ready = readyDocumentCount(current.map((document) => document.parse_status));
  const pendingParse = current.filter((document) => document.parse_status === "pending").length;
  const preparable = ready + pendingParse;
  const failed = current.some((document) => document.parse_status === "failed");
  const run = item.latest_run;
  const archived = item.archived_at !== null;
  const needsNewReview = item.revision_pending || item.configuration_pending;
  const atLimit = current.length >= 4 && !current.some((document) => document.role === role);
  const actorRole = session.data?.role ?? "";
  const canUpload = !archived && (actorRole === "admin" || actorRole === "clerk");
  const canWriteRemediation = actorRole === "admin" || actorRole === "reviewer";
  const canCreateReport = actorRole === "admin" || actorRole === "clerk";
  const canArchive = actorRole === "admin";

  function onUpload(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!file) return;
    upload.mutate(
      { file, role },
      {
        onSuccess: () => {
          setFile(null);
          setUploadKey((value) => value + 1);
        },
      },
    );
  }

  return (
    <section className="page">
      <p>
        <Link to={returnTo} state={{ restoreScrollY: returnScrollY }}>返回案件列表</Link>
      </p>
      <header className="page-head">
        <div>
          <h1>{item.title}</h1>
          <p>
            {[item.business_line, item.scenario, item.business_status, contractTypeLabel(item.contract_type)].filter(Boolean).join(" · ")} · 更新于 {formatDateTime(item.updated_at)}
          </p>
        </div>
        {run ? (
          <Link className="button button-primary" to={`/cases/${item.id}/reviews/${run.id}`}>
            打开审查工作区
          </Link>
        ) : null}
        {!run || needsNewReview ? (
          <button
            className="button button-primary"
            type="button"
            disabled={(!canUpload && actorRole !== "reviewer") || preparable < 2 || start.isPending || archived}
            onClick={() => {
              start.mutate(undefined, {
                onSuccess: (accepted) => {
                  const query = new URLSearchParams({ attempt_id: accepted.attempt_id });
                  void navigate(`/cases/${item.id}/reviews/${accepted.id}?${query}`);
                },
              });
            }}
          >
            {start.isPending ? "正在发起" : run ? "开始复审" : "开始审查"}
          </button>
        ) : null}
      </header>
      {archived ? <p className="callout">案件已归档，只能读取案件、运行历史和已有报告。服务端会以 HTTP 409 拒绝所有业务写入。</p> : null}
      {preparable < 2 && !run ? <p className="callout">至少需要两份可审查的材料；解析失败的材料需要先重新解析。</p> : null}
      {pendingParse > 0 ? <p className="meta">待解析材料将在审查受理后处理，解析失败会保留失败状态。</p> : null}
      {failed ? <p className="callout">有材料解析失败。文件仍保留在案件里，不会被当成无风险。</p> : null}
      {item.revision_pending ? <p className="callout">材料版本已更新。开始复审会建立新运行，保留旧运行和人工决定。</p> : null}
      {item.configuration_pending ? <p className="callout">规则或模型配置已更新。开始复审会记录当前配置，保留历史运行。</p> : null}
      {run && !needsNewReview && (run.state === "failed" || run.state === "partial") ? (
        <p className="meta">失败或部分完成时，在工作区里重试同一次运行，避免另开一条运行把已有决定拆开。</p>
      ) : null}
      {run && run.state !== "queued" && run.state !== "running" ? (
        <p className="meta">新上传的材料不会自动进入已经开始的审查。</p>
      ) : null}
      {start.isError ? <p role="alert">{errorMessage(start.error)}</p> : null}

      <section id="case-materials">
        <h2>材料</h2>
        {item.documents.length === 0 ? <p>还没有材料。上传采购文件、中标文件或拟签合同。</p> : null}
        <ul className="document-list">
          {item.documents.map((document) => (
            <li key={document.id} data-document-id={document.id}>
              <div className="document-main">
                <strong>{document.filename}</strong>
                {document.superseded ? <span className="meta"> · 历史版本</span> : null}
                <p className="meta">{roleLabel(document.role)} · {formatBytes(document.size)}</p>
                <ParsedDocumentSummary document={document} />
                {document.parse_status === "failed" && document.parse_error ? <p>{document.parse_error}</p> : null}
              </div>
              <div className="document-actions">
                <StatusText tone={parseTone(document.parse_status)}>{parseLabel(document.parse_status)}</StatusText>
                {!document.superseded ? (
                  <button
                    className="button button-quiet"
                    type="button"
                    aria-label={`${reparse.isPending && reparse.variables === document.id ? "正在重新解析" : "重新解析"} ${document.filename}`}
                    disabled={!canUpload || reparse.isPending}
                    onClick={() => reparse.mutate(document.id)}
                  >
                    {reparse.isPending && reparse.variables === document.id ? "正在重新解析" : "重新解析"}
                  </button>
                ) : null}
              </div>
            </li>
          ))}
        </ul>
        {reparse.isError ? <p role="alert">{errorMessage(reparse.error)}</p> : null}
        <form className="upload-form" onSubmit={onUpload}>
          <h3>登记材料</h3>
          <p className="meta">每案最多 4 类当前材料；同角色上传形成新版本并保留历史。单文件不超过 20 MiB。支持 TXT/MD、PNG/JPEG、PDF 与 DOCX 正文解析；扫描页和内嵌图片通过已配置模型转写，最终以解析状态为准。</p>
          {!canUpload ? <p>当前角色只能查看材料，不能上传或重新解析。</p> : null}
          <label className="field">
            材料角色
            <select aria-label="材料角色" value={role} onChange={(event) => setRole(event.target.value)} disabled={!canUpload}>
              {ROLES.map((itemRole) => (
                <option key={itemRole.value} value={itemRole.value}>
                  {itemRole.label}
                </option>
              ))}
            </select>
          </label>
          <label className="field">
            文件
            <input
              key={uploadKey}
              type="file"
              accept=".txt,.md,.png,.jpg,.jpeg,.pdf,.docx,text/plain,text/markdown,image/png,image/jpeg,application/pdf"
              disabled={!canUpload || atLimit}
              onChange={(event) => setFile(event.target.files?.[0] ?? null)}
            />
          </label>
          {atLimit ? <p>已有 4 类当前材料，请选择已有角色登记新版本。</p> : null}
          {upload.isError ? <p role="alert">{errorMessage(upload.error)}</p> : null}
          <button className="button button-primary" type="submit" disabled={!canUpload || atLimit || !file || upload.isPending}>
            {upload.isPending ? "正在上传" : "上传"}
          </button>
        </form>
      </section>

      <section className="case-business-overview" aria-labelledby="case-business-title">
        <h2 id="case-business-title">业务信息</h2>
        <dl className="execution-dl">
          <div><dt>业务条线</dt><dd>{item.business_line || "未提供"}</dd></div>
          <div><dt>场景</dt><dd>{item.scenario || "未提供"}</dd></div>
          <div><dt>业务状态</dt><dd>{item.business_status || "未提供"}</dd></div>
          <div><dt>归档状态</dt><dd>{archived ? `已归档 · ${formatDateTime(item.archived_at ?? "")}` : "未归档"}</dd></div>
        </dl>
      </section>
      <TemplateGenerationPanel caseId={caseId} records={item.generations} canWrite={actorRole === "admin" || actorRole === "clerk"} archived={archived} />
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
            最新运行 <StatusText tone={runTone(run.state)}>{runLabel(run.state)}</StatusText>
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
        {item.finding_counts?.total === 0 && run ? <p>这次运行没有形成风险线索。这不等于材料无风险。</p> : null}
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
