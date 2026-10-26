import {
  BookOutlined,
  CheckCircleOutlined,
  ClockCircleOutlined,
  FileDoneOutlined,
  FileSearchOutlined,
  FolderOpenOutlined,
  PlusOutlined,
  ReloadOutlined,
  WarningOutlined,
} from "@ant-design/icons";
import { Alert, Button, Select, Table, Tooltip, type TableProps } from "antd";
import { useMemo } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { errorMessage, isForbidden } from "../api/http";
import {
  useSession,
  useUpdateWorkbenchTask,
  useWorkbench,
} from "../api/queries";
import type {
  WorkbenchCase,
  WorkbenchPeriod,
  WorkbenchTask,
} from "../api/types";
import { ChartLegend, DonutChart, type DonutItem } from "../components/charts/donut-chart";
import { TrendChart } from "../components/charts/trend-chart";
import { StatusText } from "../components/status-text";
import {
  EmptyState,
  ErrorState,
  KpiStat,
  LoadingBlock,
  QuickActions,
  RetryAction,
  RiskTag,
  SectionCard,
} from "../components/ui";
import {
  buildTrend,
  caseLink,
  formatDuration,
  highRiskCount,
  isOverdue,
  myOpenTasks,
  PERIOD_LABELS,
  PERIOD_SCOPE,
  relativeDate,
  RISK_ROWS,
  taskKindLabel,
  taskPriorityLabel,
  trendHasData,
} from "../domain/dashboard";
import {
  contractTypeLabel,
  formatDateTime,
  runLabel,
  runTone,
} from "../domain/review";
import "./dashboard-page.css";

function parsePeriod(value: string | null): WorkbenchPeriod {
  return value === "day" || value === "month" ? value : "week";
}

function relativeParty(item: WorkbenchCase): string {
  const customer = item.profile?.customer?.trim();
  const supplier = item.profile?.supplier?.trim();
  if (customer && supplier) return `${customer} / ${supplier}`;
  return customer || supplier || "未报告";
}

export function DashboardPage() {
  const [params, setParams] = useSearchParams();
  const period = parsePeriod(params.get("period"));
  const snapshot = useWorkbench(period);
  const session = useSession();
  const updateTask = useUpdateWorkbenchTask();
  const navigate = useNavigate();

  const data = snapshot.data;
  const now = useMemo(
    () =>
      data?.generated_at ? new Date(data.generated_at) : new Date(),
    [data?.generated_at],
  );
  const trend = useMemo(
    () => (data ? buildTrend(data.runs, now) : []),
    [data, now],
  );
  const tasks = useMemo(
    () => myOpenTasks(data?.tasks ?? [], session.data?.id),
    [data?.tasks, session.data?.id],
  );
  const caseTitles = useMemo(
    () => new Map((data?.cases ?? []).map((item) => [item.id, item.title])),
    [data?.cases],
  );

  if (snapshot.isLoading) {
    return (
      <section className="dashboard" aria-busy="true">
        <LoadingBlock label="正在加载工作台快照" />
      </section>
    );
  }

  if (snapshot.isError || !data) {
    const forbidden = isForbidden(snapshot.error);
    return (
      <section className="dashboard">
        <ErrorState
          title={forbidden ? "没有权限查看工作台" : "工作台快照没有加载"}
          detail={
            forbidden
              ? "当前身份没有可见的案件范围。请联系管理员确认组织、角色与案件授权。"
              : errorMessage(snapshot.error)
          }
          action={
            forbidden ? undefined : (
              <RetryAction onRetry={() => void snapshot.refetch()} />
            )
          }
        />
      </section>
    );
  }

  const dashboard = data.dashboard;
  const riskItems: DonutItem[] = RISK_ROWS.map((row) => ({
    ...row,
    value:
      dashboard.risks.find((item) => item.severity === row.key)?.count ?? 0,
  }));
  const riskTotal = riskItems.reduce((sum, item) => sum + item.value, 0);
  const visibleHighRisk = highRiskCount(data.cases);
  const recentCases = [...data.cases]
    .sort((left, right) => right.updated_at.localeCompare(left.updated_at))
    .slice(0, 6);
  const dayLabel = new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "long",
    day: "numeric",
    weekday: "long",
  }).format(now);

  const overdueCopy =
    dashboard.overdue_tasks > 0
      ? `可见范围内另有 ${dashboard.overdue_tasks} 项任务逾期。`
      : "可见范围内没有逾期任务。";
  const highRiskCopy =
    visibleHighRisk > 0
      ? `当前有 ${visibleHighRisk} 个高风险案件需要优先关注。`
      : "当前可见案件中没有高风险等级。";

  const caseColumns: TableProps<WorkbenchCase>["columns"] = [
    {
      title: "案件",
      key: "case",
      width: 200,
      render: (_, item) => (
        <div className="dashboard-case-cell">
          <Link to={caseLink(item)} title={item.title}>
            {item.title}
          </Link>
        </div>
      ),
    },
    {
      title: "相对方",
      key: "party",
      width: 78,
      render: (_, item) => (
        <span className="dashboard-party">{relativeParty(item)}</span>
      ),
    },
    {
      title: "合同类型",
      key: "contract_type",
      width: 80,
      render: (_, item) => contractTypeLabel(item.contract_type),
    },
    {
      title: "风险",
      key: "severity",
      width: 82,
      render: (_, item) => <RiskTag severity={item.severity} />,
    },
    {
      title: "审查状态",
      key: "state",
      width: 84,
      render: (_, item) =>
        item.latest_run ? (
          <StatusText tone={runTone(item.latest_run.state)}>
            {runLabel(item.latest_run.state)}
          </StatusText>
        ) : (
          <span className="dashboard-case-meta">尚未审查</span>
        ),
    },
    {
      title: "更新时间",
      key: "updated_at",
      width: 80,
      render: (_, item) => (
        <time dateTime={item.updated_at} title={formatDateTime(item.updated_at)}>
          {relativeDate(item.updated_at, now)}
        </time>
      ),
    },
    {
      title: "操作",
      key: "actions",
      width: 52,
      render: (_, item) => (
        <Link className="dashboard-table-link" to={caseLink(item)}>
          查看
        </Link>
      ),
    },
  ];

  function completeTask(task: WorkbenchTask) {
    updateTask.mutate({
      caseId: task.case_id,
      taskId: task.id,
      update: {
        title: task.title,
        kind: task.kind,
        assignee_id: task.assignee_id,
        due_at: task.due_at,
        status: "completed",
        priority: task.priority,
        finding_id: task.finding_id,
        expected_version: task.version,
      },
    });
  }

  return (
    <section className="dashboard">
      <header className="dashboard-greeting">
        <div>
          <span className="dashboard-greeting__eyebrow">{dayLabel}</span>
          <h1>你好，{session.data?.display_name ?? "当前用户"}</h1>
          <p>
            当前有 {dashboard.my_pending_tasks} 项我的待办。{overdueCopy}
            {highRiskCopy}
          </p>
        </div>
        <div className="dashboard-greeting__side">
          <Select<WorkbenchPeriod>
            aria-label="统计周期"
            value={period}
            style={{ width: 104 }}
            options={(Object.keys(PERIOD_LABELS) as WorkbenchPeriod[]).map(
              (key) => ({ value: key, label: PERIOD_LABELS[key] }),
            )}
            onChange={(next) => {
              const updated = new URLSearchParams(params);
              if (next === "week") updated.delete("period");
              else updated.set("period", next);
              setParams(updated, { replace: true });
            }}
          />
          <Tooltip title="刷新工作台快照">
            <Button
              aria-label="刷新工作台数据"
              icon={<ReloadOutlined />}
              loading={snapshot.isFetching}
              onClick={() => void snapshot.refetch()}
            />
          </Tooltip>
          <Button
            type="primary"
            icon={<FolderOpenOutlined />}
            aria-label="打开案件中心"
            onClick={() => navigate("/cases")}
          >
            打开案件中心
          </Button>
        </div>
      </header>

      <section className="dashboard-kpis" aria-label="工作台关键指标">
        <KpiStat
          label="待处理审查"
          value={dashboard.pending_review_cases}
          hint="当前等待审查或人工处理的案件"
          icon={<FileSearchOutlined />}
        />
        <KpiStat
          label="高风险合同"
          value={visibleHighRisk}
          hint="按当前可见案件的最新审查结论"
          icon={<WarningOutlined />}
          tone="high"
        />
        <KpiStat
          label="已完成审查"
          value={dashboard.completed_reviews}
          hint={PERIOD_SCOPE[period]}
          icon={<CheckCircleOutlined />}
          tone="low"
        />
        <KpiStat
          label="平均审查时长"
          value={formatDuration(dashboard.average_duration_ms)}
          hint={
            dashboard.average_duration_ms === null
              ? "当前周期没有可计算的已完成审查"
              : PERIOD_SCOPE[period]
          }
          icon={<ClockCircleOutlined />}
          tone="medium"
        />
      </section>

      <div className="dashboard-grid">
        <SectionCard
          className="dashboard-trend"
          ariaLabel="审查工作量趋势"
          title={
            <span className="dashboard-card-title">审查工作量趋势</span>
          }
          extra={<span className="dashboard-scope">近 14 天 · 全部可见运行</span>}
        >
          {trendHasData(trend) ? (
            <TrendChart points={trend} />
          ) : (
            <EmptyState
              title="近 14 天还没有可统计的审查运行"
              detail="趋势按运行的开始与完成时间聚合；当前快照没有返回这段期间的运行记录。"
            />
          )}
        </SectionCard>

        <SectionCard
          ariaLabel="风险项分布"
          title={<span className="dashboard-card-title">风险项分布</span>}
          extra={<span className="dashboard-scope">最新审查结论</span>}
        >
          {riskTotal === 0 ? (
            <EmptyState
              title="还没有已报告的发现项"
              detail="这里按最新审查结论的发现项统计，不是合同数量。"
            />
          ) : (
            <>
              <div className="dashboard-risk">
                <DonutChart
                  items={riskItems}
                  centerValue={riskTotal}
                  centerLabel="发现项"
                  size={160}
                  ariaLabel={`最新审查结论发现项分布：${riskItems
                    .map((item) => `${item.label} ${item.value} 项`)
                    .join("，")}`}
                />
                <ChartLegend items={riskItems} total={riskTotal} />
              </div>
              <p className="dashboard-risk__note">
                按最新审查结论中的发现项分级统计，不是合同数量；未分级的发现项保持未评级。
              </p>
            </>
          )}
        </SectionCard>
      </div>

      <div className="dashboard-grid">
        <SectionCard
          ariaLabel="最近案件"
          title={<span className="dashboard-card-title">最近案件</span>}
          extra={
            <Link className="dashboard-table-link" to="/cases">
              查看全部案件 →
            </Link>
          }
        >
          {recentCases.length === 0 ? (
            <EmptyState
              title="当前身份没有可见案件"
              detail="新建案件或确认案件授权后，最近案件会显示在这里。"
              action={
                <Button
                  type="primary"
                  onClick={() => navigate("/cases?new=1")}
                >
                  新建案件
                </Button>
              }
            />
          ) : (
            <Table<WorkbenchCase>
              rowKey="id"
              size="small"
              tableLayout="fixed"
              columns={caseColumns}
              dataSource={recentCases}
              pagination={false}
              scroll={{ x: 656 }}
            />
          )}
        </SectionCard>

        <div className="dashboard-stack">
          <SectionCard
            ariaLabel="我的待办"
            title={<span className="dashboard-card-title">我的待办</span>}
            extra={
              <Link className="dashboard-table-link" to="/workbench/tasks">
                任务中心 →
              </Link>
            }
          >
            {tasks.length === 0 ? (
              <EmptyState
                title="没有未完成的分派任务"
                detail="任务按截止时间排序；当前身份没有尚未完成的分派任务。"
              />
            ) : (
              <ul className="dashboard-tasks" aria-label="我的未完成任务">
                {tasks.slice(0, 5).map((task) => {
                  const pending =
                    updateTask.isPending &&
                    updateTask.variables?.taskId === task.id;
                  return (
                    <li className="dashboard-task" key={task.id}>
                      <span
                        className={`dashboard-task__dot dashboard-task__dot--${task.priority}`}
                        aria-hidden="true"
                      />
                      <div className="dashboard-task__main">
                        <Link to={caseLink({ id: task.case_id, latest_run: null })}>
                          {task.title}
                        </Link>
                        <div className="dashboard-task__meta">
                          <span>{caseTitles.get(task.case_id) ?? task.case_id}</span>
                          <span>{taskKindLabel(task.kind)}</span>
                          <span>{taskPriorityLabel(task.priority)}</span>
                          <time
                            dateTime={task.due_at ?? undefined}
                            className={
                              isOverdue(task, now)
                                ? "dashboard-task__overdue"
                                : undefined
                            }
                          >
                            {task.due_at
                              ? `${isOverdue(task, now) ? "已逾期 · " : "截止 "}${formatDateTime(task.due_at)}`
                              : "未设期限"}
                          </time>
                        </div>
                      </div>
                      <Button
                        type="link"
                        size="small"
                        aria-label={`将 ${task.title} 标记完成`}
                        disabled={pending}
                        onClick={() => completeTask(task)}
                      >
                        {pending ? "保存中" : "完成"}
                      </Button>
                    </li>
                  );
                })}
              </ul>
            )}
            {updateTask.isError ? (
              <Alert
                type="error"
                showIcon
                title="任务状态没有保存"
                description={errorMessage(updateTask.error)}
              />
            ) : null}
          </SectionCard>

          <SectionCard
            ariaLabel="快捷操作"
            title={<span className="dashboard-card-title">快捷操作</span>}
          >
            <QuickActions
              items={[
                {
                  to: "/cases?new=1",
                  icon: <PlusOutlined />,
                  title: "新建案件",
                  description: "登记合同并上传材料",
                },
                {
                  to: "/review",
                  icon: <FileSearchOutlined />,
                  title: "发起审查",
                  description: "选择案件启动规则与模型审查",
                },
                {
                  to: "/workbench/reports",
                  icon: <FileDoneOutlined />,
                  title: "报告中心",
                  description: "查看冻结报告与导出",
                },
                {
                  to: "/workbench/knowledge",
                  icon: <BookOutlined />,
                  title: "知识库",
                  description: "维护审查依据与制度",
                },
              ]}
            />
          </SectionCard>
        </div>
      </div>

      <p className="dashboard-scope">
        数据生成于{" "}
        <time dateTime={data.generated_at}>
          {formatDateTime(data.generated_at)}
        </time>
        。统计口径：待处理审查与高风险合同为当前状态；已完成审查与平均时长为
        {PERIOD_LABELS[period]}口径；趋势为近 14 天全部可见运行。
      </p>
    </section>
  );
}
