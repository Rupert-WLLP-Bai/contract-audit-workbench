import {
  ArrowLeftOutlined,
  CheckCircleOutlined,
  DownloadOutlined,
  ExclamationCircleOutlined,
  FileDoneOutlined,
  FileSearchOutlined,
  FileTextOutlined,
  InfoCircleOutlined,
  QuestionCircleOutlined,
  SafetyCertificateOutlined,
  WarningOutlined,
} from "@ant-design/icons";
import { Alert, Button, Select, Table, Tabs, Tag, type TableProps } from "antd";
import { useMemo, useState, type ReactNode } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { ApiError, errorMessage, isForbidden } from "../api/http";
import { useReport } from "../api/queries";
import type { ReportDetail, ReportFinding } from "../api/reports";
import { paths } from "../api/paths";
import { ChartLegend, DonutChart, type DonutItem } from "../components/charts/donut-chart";
import { PageStatus } from "../components/page-status";
import { ReportFindingDrawer } from "../components/report-finding-drawer";
import { ReportSourceView } from "../components/report-source-view";
import { StatusText } from "../components/status-text";
import {
  EmptyState,
  KpiStat,
  PageHeader,
  RiskTag,
  SectionCard,
} from "../components/ui";
import { formatDuration } from "../domain/dashboard";
import {
  latestFindingDecision,
  reportConclusion,
  reportDomainCounts,
  reportFindings,
  reportFindingLocation,
  reportSummary,
  type ReportSummary,
} from "../domain/report";
import { domainLabel, scenarioLabel } from "../domain/review-presentation";
import {
  contractTypeLabel,
  decisionLabel,
  formatDateTime,
  parseLabel,
  roleLabel,
  runLabel,
  runTone,
} from "../domain/review";
import { palette } from "../theme";
import "./report-detail-page.css";

const TABS = [
  { key: "overview", label: "审查报告" },
  { key: "findings", label: "问题清单" },
  { key: "sources", label: "原文依据" },
  { key: "provenance", label: "版本与过程" },
] as const;

type ReportTab = (typeof TABS)[number]["key"];
type SeverityFilter = "all" | "high" | "medium" | "low" | "info" | "unrated";
type DecisionFilter = "all" | "pending" | "decided";

const SEVERITY_ROWS = [
  { key: "high", label: "高风险", color: palette.high, tone: "high" as const },
  { key: "medium", label: "中风险", color: palette.medium, tone: "medium" as const },
  { key: "low", label: "低风险", color: palette.low, tone: "low" as const },
  { key: "info", label: "提示", color: palette.info, tone: "primary" as const },
  { key: "unrated", label: "未评级", color: "#CBD5E1", tone: "neutral" as const },
];

function parseTab(value: string | null): ReportTab {
  return TABS.some((tab) => tab.key === value) ? (value as ReportTab) : "overview";
}

function valueOr(value: string | null | undefined, fallback = "未报告"): string {
  return value && value.trim() !== "" ? value : fallback;
}

function percent(count: number, total: number): number {
  return total === 0 ? 0 : Math.round((count / total) * 100);
}

function overallSeverity(report: ReportDetail): string | null {
  for (const severity of ["high", "medium", "low", "info"]) {
    if (report.findings.some((finding) => finding.severity === severity))
      return severity;
  }
  return null;
}

function InfoRow({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{children ?? "未报告"}</dd>
    </div>
  );
}

function severityIcon(key: string) {
  if (key === "high") return <ExclamationCircleOutlined />;
  if (key === "medium") return <WarningOutlined />;
  if (key === "low") return <CheckCircleOutlined />;
  if (key === "info") return <InfoCircleOutlined />;
  return <QuestionCircleOutlined />;
}

function RiskStats({ summary }: { summary: ReportSummary }) {
  return (
    <div className="report-risk-stats">
      {SEVERITY_ROWS.map((row) => {
        const count = summary[row.key as keyof ReportSummary] as number;
        return (
          <KpiStat
            key={row.key}
            label={`${row.label}问题`}
            value={count}
            hint={`占全部 ${percent(count, summary.total)}%`}
            icon={severityIcon(row.key)}
            tone={row.tone}
          />
        );
      })}
    </div>
  );
}

function RiskDistribution({ report, summary }: { report: ReportDetail; summary: ReportSummary }) {
  const items: DonutItem[] = SEVERITY_ROWS.map((row) => ({
    key: row.key,
    label: row.label,
    value: summary[row.key as keyof ReportSummary] as number,
    color: row.color,
  }));
  const domains = reportDomainCounts(reportFindings(report)).slice(0, 5);
  const maximum = Math.max(1, ...domains.map((item) => item.count));
  return (
    <div className="report-risk-distribution">
      <div className="report-risk-chart">
        <DonutChart
          items={items}
          centerValue={summary.total}
          centerLabel="问题总数"
          ariaLabel={`风险等级分布，共 ${summary.total} 项发现`}
          size={170}
        />
        <ChartLegend items={items} total={summary.total} />
      </div>
      <div className="report-domains">
        <h4>主要风险领域</h4>
        {domains.length === 0 ? (
          <p className="report-muted">报告没有返回发现领域。</p>
        ) : (
          <ul>
            {domains.map((item) => (
              <li key={item.domain}>
                <span className="report-domain__label" title={item.domain}>
                  {item.label}
                </span>
                <span className="report-domain__track" aria-hidden="true">
                  <span
                    className="report-domain__bar"
                    style={{ width: `${Math.max(4, (item.count / maximum) * 100)}%` }}
                  />
                </span>
                <span className="report-domain__count">{item.count}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

function FindingTable({
  report,
  findings,
  onOpenFinding,
}: {
  report: ReportDetail;
  findings: ReportFinding[];
  onOpenFinding: (findingId: string) => void;
}) {
  const columns: TableProps<ReportFinding>["columns"] = [
    {
      title: "序号",
      key: "index",
      width: 52,
      render: (_, __, index) => (
        <span className="report-index">{String(index + 1).padStart(2, "0")}</span>
      ),
    },
    {
      title: "风险等级",
      key: "severity",
      width: 88,
      render: (_, finding) => <RiskTag severity={finding.severity} />,
    },
    {
      title: "问题描述",
      key: "claim",
      render: (_, finding) => (
        <span className="report-claim" title={finding.claim}>
          {finding.claim || "服务未返回问题描述"}
        </span>
      ),
    },
    {
      title: "条款 / 规则",
      key: "domain",
      width: 130,
      render: (_, finding) => (
        <span className="report-cell-stack">
          <span>{domainLabel(finding.domain) ?? "领域未报告"}</span>
          <code title={finding.rule_id ?? undefined}>
            {finding.rule_id ?? "规则未报告"}
          </code>
        </span>
      ),
    },
    {
      title: "原文位置",
      key: "evidence",
      width: 136,
      render: (_, finding) => {
        const location = reportFindingLocation(report, finding);
        return (
          <span className="report-cell-muted report-cell-clamp" title={location}>
            {location}
            {finding.anchors.length > 0 ? ` · ${finding.anchors.length} 条` : ""}
          </span>
        );
      },
    },
    {
      title: "人工处理",
      key: "decision",
      width: 98,
      render: (_, finding) => {
        const decision = latestFindingDecision(report, finding.id);
        return decision ? (
          <Tag color="success">{decisionLabel(decision.action)}</Tag>
        ) : (
          <Tag>待处理</Tag>
        );
      },
    },
    {
      title: "操作",
      key: "actions",
      width: 80,
      render: (_, finding) => (
        <Button type="link" onClick={() => onOpenFinding(finding.id)}>
          查看详情
        </Button>
      ),
    },
  ];
  return (
    <Table<ReportFinding>
      className="report-table"
      rowKey="id"
      tableLayout="fixed"
      columns={columns}
      dataSource={findings}
      pagination={false}
      size="middle"
      scroll={{ x: 760 }}
      locale={{ emptyText: "没有符合当前筛选条件的发现项" }}
    />
  );
}

function ReportInfoSidebar({
  report,
  summary,
}: {
  report: ReportDetail;
  summary: ReportSummary;
}) {
  const html = paths.report(report.case.id, report.report_id, "html");
  const json = paths.report(report.case.id, report.report_id, "json");
  return (
    <aside className="report-side" aria-label="报告信息与操作">
      <SectionCard title="报告信息">
        <dl className="report-side-fields">
          <InfoRow label="报告编号">
            <code>{report.report_id}</code>
          </InfoRow>
          <InfoRow label="报告版本">v{report.report_version}</InfoRow>
          <InfoRow label="生成时间">{formatDateTime(report.created_at)}</InfoRow>
          <InfoRow label="审查状态">
            <StatusText tone={runTone(report.run.state)}>
              {runLabel(report.run.state)}
            </StatusText>
          </InfoRow>
          <InfoRow label="人工复核">
            {summary.undecided === 0 ? (
              <Tag color="success">所有发现已有记录</Tag>
            ) : (
              <Tag color="warning">待处理 {summary.undecided} 项</Tag>
            )}
          </InfoRow>
          <InfoRow label="模型">
            {report.run.model_ref ?? "未报告模型"}
          </InfoRow>
          <InfoRow label="关联案件">
            <Link to={`/cases/${encodeURIComponent(report.case.id)}`}>
              {report.case.title}
            </Link>
            <small className="report-side-sub">{report.case.id}</small>
          </InfoRow>
        </dl>
      </SectionCard>

      <SectionCard title="审查范围">
        <dl className="report-side-fields">
          <InfoRow label="材料数量">{report.documents.length} 份</InfoRow>
          <InfoRow label="合同类型">
            {contractTypeLabel(report.case.contract_type)}
          </InfoRow>
          <InfoRow label="业务场景">
            {scenarioLabel(report.case.scenario) ??
              valueOr(report.case.scenario)}
          </InfoRow>
        </dl>
        {report.documents.length === 0 ? (
          <p className="report-side-note">报告没有返回审查材料。</p>
        ) : (
          <ul className="report-side-docs">
            {report.documents.map((document) => (
              <li key={document.id}>
                <FileTextOutlined aria-hidden="true" />
                <span>
                  <strong>{roleLabel(document.role)}</strong>
                  <small>
                    {document.filename} · {parseLabel(document.parsed_document.status)}
                  </small>
                </span>
              </li>
            ))}
          </ul>
        )}
      </SectionCard>

      <SectionCard title="导出与关联">
        <div className="report-export-actions">
          <Button
            type="primary"
            icon={<DownloadOutlined />}
            href={html}
            target="_blank"
            rel="noreferrer"
          >
            导出 HTML
          </Button>
          <Button href={json} target="_blank" rel="noreferrer">
            导出 JSON
          </Button>
        </div>
        <p className="report-side-note">
          后端只提供冻结 HTML 与 JSON。PDF、Word、分享链接或发送复核不在接口能力内，页面不生成这些入口。
        </p>
        <div className="report-related-actions">
          <Link to={`/cases/${encodeURIComponent(report.case.id)}`}>
            <ArrowLeftOutlined aria-hidden="true" /> 返回案件
          </Link>
          <Link
            to={`/cases/${encodeURIComponent(report.case.id)}/reviews/${encodeURIComponent(report.run.id)}`}
          >
            <FileSearchOutlined aria-hidden="true" /> 查看审查工作区
          </Link>
          <Link to="/workbench/reports">
            <FileDoneOutlined aria-hidden="true" /> 报告中心
          </Link>
        </div>
      </SectionCard>
    </aside>
  );
}

function Provenance({ report }: { report: ReportDetail }) {
  const usage = report.execution.usage;
  return (
    <div className="report-provenance">
      <div className="report-provenance__grid">
        <SectionCard title="报告与运行">
          <dl className="report-provenance-fields">
            <InfoRow label="报告格式">{report.schema_version}</InfoRow>
            <InfoRow label="报告编号">
              <code>{report.report_id}</code>
            </InfoRow>
            <InfoRow label="报告版本">v{report.report_version}</InfoRow>
            <InfoRow label="生成时间">{formatDateTime(report.created_at)}</InfoRow>
            <InfoRow label="运行 ID">
              <code>{report.run.id}</code>
            </InfoRow>
            <InfoRow label="运行状态">
              <StatusText tone={runTone(report.run.state)}>
                {runLabel(report.run.state)}
              </StatusText>
            </InfoRow>
            <InfoRow label="尝试 ID">
              <code>{report.attempt.id}</code>
            </InfoRow>
            <InfoRow label="尝试状态">{report.attempt.state}</InfoRow>
            <InfoRow label="模型">{report.run.model_ref ?? "未报告"}</InfoRow>
            {report.run.partial_reason ? (
              <InfoRow label="部分完成原因">{report.run.partial_reason}</InfoRow>
            ) : null}
            {report.run.failure_message ? (
              <InfoRow label="失败原因">{report.run.failure_message}</InfoRow>
            ) : null}
            {report.attempt.error_code ? (
              <InfoRow label="尝试错误码">{report.attempt.error_code}</InfoRow>
            ) : null}
            <InfoRow label="开始时间">
              {formatDateTime(report.attempt.started_at ?? "")}
            </InfoRow>
            <InfoRow label="结束时间">
              {formatDateTime(report.attempt.finished_at ?? "")}
            </InfoRow>
          </dl>
        </SectionCard>

        <SectionCard title="执行与模型">
          <dl className="report-provenance-fields">
            <InfoRow label="执行事件">
              {report.execution.events.length} 条
            </InfoRow>
            <InfoRow label="执行耗时">
              {formatDuration(report.execution.duration_ms)}
            </InfoRow>
            <InfoRow label="排队等待">
              {formatDuration(report.execution.queue_wait_ms)}
            </InfoRow>
            <InfoRow label="首条已核验发现">
              {formatDuration(report.execution.first_verified_finding_ms)}
            </InfoRow>
            <InfoRow label="模型调用">
              {usage ? `${usage.calls} 次` : "未报告"}
            </InfoRow>
            <InfoRow label="Token 总量">
              {usage?.total_tokens ?? "未报告"}
            </InfoRow>
            <InfoRow label="未知用量调用">
              {usage?.unknown_calls ?? "未报告"}
            </InfoRow>
            <InfoRow label="模型审查状态">
              {report.agent_review?.status ?? "报告未返回模型审查记录"}
            </InfoRow>
            <InfoRow label="模型审查模式">
              {report.agent_review?.mode ?? "未报告"}
            </InfoRow>
            <InfoRow label="覆盖领域">
              {report.agent_review
                ? `${report.agent_review.domain_count} 个 · ${report.agent_review.gap_count} 项缺口`
                : "未报告"}
            </InfoRow>
            <p className="report-provenance-note">
              模型输出仅为建议；没有匹配冻结原文引用的内容不会成为人工结论。
            </p>
          </dl>
        </SectionCard>

        <SectionCard title="规则包与审查依据">
          <dl className="report-provenance-fields">
            <InfoRow label="规则包状态">{report.rulepack.availability}</InfoRow>
            <InfoRow label="规则包 ID">
              {report.rulepack.snapshot_id ?? "未报告"}
            </InfoRow>
            <InfoRow label="规则包版本">
              {report.rulepack.snapshot_version ?? "未报告"}
            </InfoRow>
            <InfoRow label="规则数量">
              {report.rulepack.rule_count ?? "未报告"}
            </InfoRow>
            <InfoRow label="编译引用">
              {report.rulepack.compiler_ref || "未报告"}
            </InfoRow>
            <InfoRow label="清单摘要">
              <code className="report-hash">{report.rulepack.manifest_hash || "未报告"}</code>
            </InfoRow>
            <InfoRow label="快照摘要">
              <code className="report-hash">{report.rulepack.snapshot_sha256 || "未报告"}</code>
            </InfoRow>
            {report.rulepack.unavailable_reason ? (
              <InfoRow label="规则包不可用原因">
                {report.rulepack.unavailable_reason}
              </InfoRow>
            ) : null}
          </dl>
        </SectionCard>

        <SectionCard title="冻结完整性">
          <dl className="report-provenance-fields">
            <InfoRow label="输入摘要">
              <code className="report-hash">{report.input_hash || "未报告"}</code>
            </InfoRow>
            <InfoRow label="输出摘要">
              <code className="report-hash">{report.output_hash || "未报告"}</code>
            </InfoRow>
            <InfoRow label="决定快照摘要">
              <code className="report-hash">
                {report.decision_snapshot_hash || "未报告"}
              </code>
            </InfoRow>
            <InfoRow label="模型审查摘要">
              <code className="report-hash">
                {report.agent_review_sha256 || "未报告"}
              </code>
            </InfoRow>
          </dl>
          <p className="report-provenance-note">
            上述摘要来自服务端冻结报告。后续材料更新、重新解析或索引不会改写这份报告。
          </p>
        </SectionCard>
      </div>

      <SectionCard title="审查材料快照">
        {report.documents.length === 0 ? (
          <EmptyState title="报告没有返回审查材料" />
        ) : (
          <Table
            className="report-table"
            rowKey="id"
            pagination={false}
            size="small"
            scroll={{ x: 760 }}
            dataSource={report.documents}
            columns={[
              {
                title: "材料",
                key: "filename",
                render: (_, document) => (
                  <span className="report-doc-name">
                    <strong>{document.filename}</strong>
                    <small>{document.id}</small>
                  </span>
                ),
              },
              {
                title: "角色",
                key: "role",
                width: 110,
                render: (_, document) => roleLabel(document.role),
              },
              {
                title: "解析",
                key: "parsed",
                width: 100,
                render: (_, document) =>
                  parseLabel(document.parsed_document.status),
              },
              {
                title: "引用",
                key: "anchors",
                width: 80,
                render: (_, document) =>
                  report.findings.reduce(
                    (count, finding) =>
                      count +
                      finding.anchors.filter(
                        (anchor) => anchor.document_id === document.id,
                      ).length,
                    0,
                  ),
              },
              {
                title: "原始摘要",
                key: "digest",
                render: (_, document) => (
                  <code className="report-hash">{document.digest || "未报告"}</code>
                ),
              },
            ]}
          />
        )}
      </SectionCard>

      <p className="report-notice">
        {report.notice || "本报告记录技术审查快照，不构成法律意见或业务审批。"}
      </p>
    </div>
  );
}

export function ReportDetailPage() {
  const { caseId = "", reportId = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const reportQuery = useReport(caseId, reportId);
  const [severityFilter, setSeverityFilter] = useState<SeverityFilter>("all");
  const [decisionFilter, setDecisionFilter] = useState<DecisionFilter>("all");

  const report = reportQuery.data;
  const tab = parseTab(params.get("tab"));
  const requestedFinding = params.get("finding");
  const selectedFinding =
    report?.findings.find((finding) => finding.id === requestedFinding) ?? null;
  const drawerOpen = params.get("drawer") === "1" && selectedFinding !== null;

  const summary = useMemo(
    () => (report ? reportSummary(report) : null),
    [report],
  );
  const sortedFindings = useMemo(
    () => (report ? reportFindings(report) : []),
    [report],
  );

  function updateParams(next: {
    tab?: ReportTab | null;
    finding?: string | null;
    drawer?: string | null;
  }) {
    const search = new URLSearchParams(params);
    for (const [key, value] of Object.entries(next)) {
      if (value === null || value === undefined || value === "") search.delete(key);
      else search.set(key, value);
    }
    setParams(search, { replace: true });
  }

  function openFinding(findingId: string) {
    updateParams({ finding: findingId, drawer: "1" });
  }

  function closeFinding() {
    updateParams({ drawer: null });
  }

  function viewSource(findingId: string) {
    updateParams({ tab: "sources", finding: findingId, drawer: null });
  }

  if (reportQuery.isLoading)
    return <PageStatus kind="loading" title="正在读取冻结报告" />;

  if (reportQuery.isError || !report) {
    const forbidden = isForbidden(reportQuery.error);
    const notFound =
      reportQuery.error instanceof ApiError && reportQuery.error.status === 404;
    return (
      <PageStatus
        kind={forbidden ? "denied" : "error"}
        title={
          forbidden
            ? "没有权限读取这个报告"
            : notFound
              ? "没有找到冻结报告"
              : "冻结报告没有加载"
        }
        detail={
          forbidden
            ? `${errorMessage(reportQuery.error)}。报告读取受服务端导出权限控制。`
            : notFound
              ? "报告可能已被移除，或不在当前授权范围。"
              : errorMessage(reportQuery.error)
        }
        action={
          forbidden || notFound ? (
            <Button type="primary" href={`/cases/${encodeURIComponent(caseId)}`}>
              返回案件
            </Button>
          ) : (
            <Button type="primary" onClick={() => void reportQuery.refetch()}>
              重试
            </Button>
          )
        }
      />
    );
  }

  const filteredFindings = sortedFindings.filter((finding) => {
    if (
      severityFilter !== "all" &&
      (finding.severity ?? "unrated") !== severityFilter
    )
      return false;
    const decided = latestFindingDecision(report, finding.id) !== null;
    if (decisionFilter === "pending" && decided) return false;
    if (decisionFilter === "decided" && !decided) return false;
    return true;
  });
  const conclusion = reportConclusion(summary!);
  const severity = overallSeverity(report);
  const html = paths.report(report.case.id, report.report_id, "html");
  const json = paths.report(report.case.id, report.report_id, "json");
  const descriptionParts = [
    contractTypeLabel(report.case.contract_type),
    scenarioLabel(report.case.scenario),
    report.case.business_line ? `业务线 ${report.case.business_line}` : null,
    `报告 ${report.report_id}`,
    `v${report.report_version}`,
  ].filter(Boolean);

  return (
    <section className="report-page" aria-busy={reportQuery.isFetching}>
      <PageHeader
        eyebrow={
          <span className="report-eyebrow">
            <Tag color="blue">冻结报告</Tag>
            <span>{report.schema_version}</span>
          </span>
        }
        title={
          <span className="report-title">
            {report.case.title}
            {summary!.total === 0 ? (
              <Tag>未记录发现</Tag>
            ) : (
              <RiskTag severity={severity} />
            )}
          </span>
        }
        description={descriptionParts.join(" · ")}
        extra={
          <div className="report-header-actions">
            <Button
              type="primary"
              icon={<DownloadOutlined />}
              href={html}
              target="_blank"
              rel="noreferrer"
            >
              导出 HTML
            </Button>
            <Button href={json} target="_blank" rel="noreferrer">
              导出 JSON
            </Button>
            <Button
              icon={<ArrowLeftOutlined />}
              href={`/cases/${encodeURIComponent(report.case.id)}`}
            >
              返回案件
            </Button>
          </div>
        }
      />

      <Tabs
        className="report-tabs"
        activeKey={tab}
        onChange={(key) =>
          updateParams({ tab: key as ReportTab, drawer: null })
        }
        items={TABS.map((item) => ({
          key: item.key,
          label: item.label,
        }))}
      />

      <div className="report-layout">
        <main className="report-main">
          {tab === "overview" ? (
            <article className="report-sheet" aria-label="合同审查报告">
              <header className="report-sheet__head">
                <div>
                  <h2>合同审查报告</h2>
                  <p>
                    报告编号 {report.report_id} · 生成时间{" "}
                    {formatDateTime(report.created_at)}
                  </p>
                </div>
                <span className="report-sheet__brand" aria-hidden="true">
                  <SafetyCertificateOutlined />
                  合同智能审计
                </span>
              </header>

              <section className="report-sheet__section" aria-labelledby="report-summary-title">
                <h3 id="report-summary-title">一、审查结论与摘要</h3>
                <Alert
                  className="report-conclusion"
                  type={conclusion.tone}
                  showIcon
                  title={<strong>{conclusion.title}</strong>}
                  description={conclusion.detail}
                />
                <RiskStats summary={summary!} />
                <p className="report-sheet__note">
                  统计口径：冻结报告中的 {summary!.total} 项发现，按服务返回的
                  severity 分类；未返回等级时计为“未评级”，不推断风险高低。
                  人工处理状态来自冻结决定记录，不等于合同获批。
                </p>
              </section>

              <section className="report-sheet__section" aria-labelledby="report-distribution-title">
                <h3 id="report-distribution-title">二、风险分布</h3>
                <RiskDistribution report={report} summary={summary!} />
              </section>

              <section className="report-sheet__section" aria-labelledby="report-issues-title">
                <h3 id="report-issues-title">三、关键问题与建议</h3>
                {sortedFindings.length === 0 ? (
                  <EmptyState
                    title="冻结报告没有记录发现项"
                    detail="这不代表合同获批；仍需人工确认审查范围。"
                  />
                ) : (
                  <>
                    <FindingTable
                      report={report}
                      findings={sortedFindings.slice(0, 5)}
                      onOpenFinding={openFinding}
                    />
                    {sortedFindings.length > 5 ? (
                      <div className="report-sheet__more">
                        <Button
                          type="link"
                          onClick={() =>
                            updateParams({ tab: "findings", drawer: null })
                          }
                        >
                          查看全部问题（共 {sortedFindings.length} 项）
                        </Button>
                      </div>
                    ) : null}
                  </>
                )}
              </section>
            </article>
          ) : null}

          {tab === "findings" ? (
            <SectionCard
              title="问题清单"
              extra={<span className="report-muted">共 {sortedFindings.length} 项发现</span>}
            >
              <div className="report-filters">
                <label>
                  风险等级
                  <Select
                    aria-label="筛选风险等级"
                    value={severityFilter}
                    onChange={setSeverityFilter}
                    options={[
                      { value: "all", label: "全部等级" },
                      ...SEVERITY_ROWS.map((row) => ({
                        value: row.key,
                        label: row.label,
                      })),
                    ]}
                  />
                </label>
                <label>
                  人工处理
                  <Select
                    aria-label="筛选人工处理状态"
                    value={decisionFilter}
                    onChange={setDecisionFilter}
                    options={[
                      { value: "all", label: "全部状态" },
                      { value: "pending", label: "待人工处理" },
                      { value: "decided", label: "已有处理记录" },
                    ]}
                  />
                </label>
                <span className="report-muted">
                  当前显示 {filteredFindings.length} 项
                </span>
              </div>
              <FindingTable
                report={report}
                findings={filteredFindings}
                onOpenFinding={openFinding}
              />
            </SectionCard>
          ) : null}

          {tab === "sources" ? (
            <SectionCard title="原文与依据">
              <p className="report-tab-note">
                只高亮冻结正文中引用与字符偏移一致的片段。没有偏移或引用不一致时保留引用快照，不猜测页码、坐标或定位结果。
              </p>
              <ReportSourceView
                report={report}
                selectedFindingId={selectedFinding?.id ?? null}
                onOpenFinding={openFinding}
              />
            </SectionCard>
          ) : null}

          {tab === "provenance" ? <Provenance report={report} /> : null}
        </main>

        <ReportInfoSidebar report={report} summary={summary!} />
      </div>

      <ReportFindingDrawer
        report={report}
        finding={drawerOpen ? selectedFinding : null}
        onClose={closeFinding}
        onViewSource={viewSource}
      />
    </section>
  );
}
