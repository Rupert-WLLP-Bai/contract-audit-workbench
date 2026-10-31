import {
  CheckSquareOutlined,
  ClockCircleOutlined,
  FileSearchOutlined,
  FolderOpenOutlined,
} from "@ant-design/icons";
import { Tabs } from "antd";
import { Select } from "antd";
import { Button, Input, Table, type TableProps } from "antd";
import { useMemo, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { errorMessage } from "../api/http";
import {
  useCreateWorkbenchTask,
  useSession,
  useUpdateWorkbenchTask,
  useWorkbench,
} from "../api/queries";
import type { WorkbenchPeriod, WorkbenchTask } from "../api/types";
import { PageStatus } from "../components/page-status";
import { IntakePrimaryContract } from "../components/intake-primary-contract";
import { StatusText } from "../components/status-text";
import {
  caseHref,
  formatDateTime,
  runLabel,
  runTone,
  taskKindLabel,
} from "../domain/review";
import "./home-page.css";

function barWidth(count: number, total: number): number {
  return total === 0
    ? 0
    : Math.max(0, Math.min(100, Math.round((count / total) * 100)));
}
const BUSINESS_STATUS_LABELS: Record<string, string> = {
  active: "未归档",
  archived: "已归档",
};

function progressColor(status: string): string {
  if (status === "archived") return "var(--state-compatible)";
  return status === "active" ? "var(--action-primary)" : "var(--state-neutral)";
}

function ProgressDonut({
  rows,
  visibleCases,
}: {
  rows: { status: string; count: number }[];
  visibleCases: number;
}) {
  const total = rows.reduce((sum, row) => sum + row.count, 0);
  let offset = 0;
  const slices = rows
    .filter((row) => row.count > 0)
    .map((row) => {
      const start = offset;
      offset += row.count;
      return `${progressColor(row.status)} ${(start / total) * 100}% ${(offset / total) * 100}%`;
    });
  const background =
    total === 0 ? "var(--surface-subtle)" : `conic-gradient(${slices.join(", ")})`;
  const center = total === 0 && visibleCases > 0 ? "—" : String(total);
  const centerLabel = total === 0 && visibleCases > 0 ? "状态未报" : "案件";
  const accessibleLabel =
    rows.length === 0
      ? "打开案件中心；案件状态分布未报告"
      : `打开案件中心；${rows.map((row) => `${BUSINESS_STATUS_LABELS[row.status] ?? (row.status || "未报告")} ${row.count} 项`).join("，")}`;

  return (
    <div className="home-progress-content">
      <Link
        className="home-progress-ring"
        to="/cases"
        style={{ background }}
        aria-label={accessibleLabel}
      >
        <div aria-hidden="true">
          <strong>{center}</strong>
          <span>{centerLabel}</span>
        </div>
      </Link>
      <div className="home-progress-legend" aria-label="按案件业务状态统计">
        {rows.length === 0 ? (
          <p>
            {visibleCases === 0
              ? "当前身份没有可见案件。"
              : "案件状态统计未报告。"}
          </p>
        ) : (
          rows.map((row) => (
            <div key={row.status}>
              <span>
                <i style={{ background: progressColor(row.status) }} />
                {BUSINESS_STATUS_LABELS[row.status] ?? (row.status || "未报告")}
              </span>
              <strong>{row.count}</strong>
            </div>
          ))
        )}
        {rows.length > 0 && total !== visibleCases ? (
          <small>
            状态统计 {total} 项；可见案件 {visibleCases} 项。
          </small>
        ) : null}
      </div>
    </div>
  );
}

function ProgressList({
  rows,
  total,
  kind,
}: {
  rows: { label: string; count: number; key: string }[];
  total: number;
  kind: "risk";
}) {
  if (rows.length === 0)
    return <p className="home-empty">没有获取到这项统计数据。</p>;
  return (
    <ul className={`home-data-bars home-data-bars-${kind}`}>
      {rows.map((row) => (
        <li key={row.key}>
          <div className="home-data-bar-label">
            <span>{row.label}</span>
            <strong>{row.count}</strong>
          </div>
          <div
            className="home-data-bar-track"
            aria-label={`${row.label} ${row.count}，占 ${barWidth(row.count, total)}%`}
          >
            <span style={{ width: `${barWidth(row.count, total)}%` }} />
          </div>
        </li>
      ))}
    </ul>
  );
}

function CircuitStrip({
  openCount,
  nodes,
}: {
  openCount: number;
  nodes: readonly { label: string; count: number; tone: "info" | "pending" | "success" }[];
}) {
  return (
    <section className="circuit-panel" aria-label="审查链路">
      <div className="circuit-head">
        <h2>审查链路</h2>
        <span
          className={
            openCount > 0
              ? "circuit-open"
              : "circuit-open circuit-open-closed"
          }
        >
          {openCount > 0 ? `开路口 ${openCount}` : "链路闭合"}
        </span>
      </div>
      <ol className="circuit">
        {nodes.map((node) => (
          <li
            className="circuit-node"
            key={node.label}
            data-state={node.count > 0 ? "filled" : "empty"}
            data-tone={node.tone}
          >
            <span className="circuit-mark" aria-hidden="true" />
            <span className="circuit-label">{node.label}</span>
            <strong
              className={`circuit-count${node.count === 0 ? " circuit-count-empty" : ""}`}
            >
              {node.count}
            </strong>
          </li>
        ))}
      </ol>
    </section>
  );
}

function TaskRow({
  task,
  caseTitle,
  assignee,
  pending,
  onComplete,
}: {
  task: WorkbenchTask;
  caseTitle: string;
  assignee: string;
  pending: boolean;
  onComplete: () => void;
}) {
  return (
    <li className="home-task-row">
      <div className="home-task-copy">
        <Link className="home-task-title" to={caseHref(task.case_id, null)}>
          {task.title}
        </Link>
        <span className="home-task-meta">
          {caseTitle}，{assignee}，{taskKindLabel(task.kind)}
        </span>
      </div>
      <div className="home-task-state">
        <StatusText
          tone={
            task.priority === "high"
              ? "bad"
              : task.priority === "medium"
                ? "attention"
                : "neutral"
          }
        >
          {task.priority === "high"
            ? "高优先级"
            : task.priority === "medium"
              ? "中优先级"
              : "低优先级"}
        </StatusText>
        <time className="home-task-time" dateTime={task.due_at ?? undefined}>
          {task.due_at ? formatDateTime(task.due_at) : "未设期限"}
        </time>
      </div>
      {task.status === "completed" ? (
        <span className="home-task-complete">已完成</span>
      ) : (
        <Button
          aria-label={String(pending ? "正在保存" : "标记完成")}
          className="home-task-complete"
          htmlType="button"
          disabled={pending}
          onClick={onComplete}
        >
          {pending ? "正在保存" : "标记完成"}
        </Button>
      )}
    </li>
  );
}

export function HomePage() {
  const [period, setPeriod] = useState<WorkbenchPeriod>("week");
  const [taskCaseId, setTaskCaseId] = useState("");
  const [taskTitle, setTaskTitle] = useState("");
  const [taskAssignee, setTaskAssignee] = useState("");
  const [taskDue, setTaskDue] = useState("");
  const [showTaskForm, setShowTaskForm] = useState(false);
  const [taskFilter, setTaskFilter] = useState<"mine" | "all" | "completed">(
    "mine",
  );
  const snapshot = useWorkbench(period);
  const session = useSession();
  const createTask = useCreateWorkbenchTask(taskCaseId);
  const updateTask = useUpdateWorkbenchTask();
  const caseTitles = useMemo(
    () =>
      new Map(
        (snapshot.data?.cases ?? []).map((item) => [item.id, item.title]),
      ),
    [snapshot.data?.cases],
  );
  const members = useMemo(
    () =>
      new Map(
        (snapshot.data?.members ?? []).map((member) => [
          member.id,
          member.display_name,
        ]),
      ),
    [snapshot.data?.members],
  );

  if (snapshot.isLoading)
    return <PageStatus kind="loading" title="正在加载工作台" />;
  if (snapshot.isError || !snapshot.data) {
    return (
      <PageStatus
        kind="error"
        title="工作台数据没有加载"
        detail={errorMessage(snapshot.error)}
        action={
          <Button
            aria-label={"重试"}
            type="primary"
            htmlType="button"
            onClick={() => void snapshot.refetch()}
          >
            重试
          </Button>
        }
      />
    );
  }

  const data = snapshot.data;
  const dashboard = data.dashboard;
  const allTasks = [...data.tasks].sort((left, right) =>
    (left.due_at ?? "9999").localeCompare(right.due_at ?? "9999"),
  );
  const tasks = allTasks.filter((task) =>
    taskFilter === "completed"
      ? task.status === "completed"
      : taskFilter === "mine"
        ? task.assignee_id === session.data?.id && task.status !== "completed"
        : task.status !== "completed",
  );
  const activeTaskCount = allTasks.filter(
    (task) => task.status !== "completed",
  ).length;
  const cases = [...data.cases].sort((left, right) =>
    right.updated_at.localeCompare(left.updated_at),
  );
  const openCircuits = cases.filter(
    (item) =>
      item.latest_run !== null &&
      ["failed", "interrupted", "cancelled", "partial"].includes(
        item.latest_run.state,
      ),
  ).length;
  const circuitNodes = [
    { label: "材料", count: dashboard.visible_cases, tone: "info" },
    { label: "待审查", count: dashboard.pending_review_cases, tone: "pending" },
    { label: "待办", count: dashboard.my_pending_tasks, tone: "pending" },
    { label: "整改", count: dashboard.remediation_cases, tone: "pending" },
    { label: "完成", count: dashboard.completed_reviews, tone: "success" },
  ] as const;
  const recentCases = cases.slice(0, 8);
  const recentCaseColumns: TableProps<(typeof recentCases)[number]>["columns"] =
    [
      {
        title: "案件",
        key: "case",
        render: (_, item) => (
          <>
            <Link to={caseHref(item.id, item.latest_run?.id ?? null)}>
              {item.title}
            </Link>
            <span className="home-recent-id">{item.id}</span>
          </>
        ),
      },
      {
        title: "审查状态",
        key: "review-status",
        render: (_, item) =>
          item.latest_run ? (
            <StatusText tone={runTone(item.latest_run.state)}>
              {runLabel(item.latest_run.state)}
            </StatusText>
          ) : (
            <span className="home-no-run">尚未审查</span>
          ),
      },
      {
        title: "待决定",
        dataIndex: "pending_decisions",
        key: "pending_decisions",
      },
      {
        title: "业务线 / 场景",
        key: "business",
        render: (_, item) =>
          [item.business_line, item.scenario].filter(Boolean).join("，") ||
          "未设置",
      },
      {
        title: "更新时间",
        key: "updated_at",
        render: (_, item) => (
          <time dateTime={item.updated_at}>
            {formatDateTime(item.updated_at)}
          </time>
        ),
      },
      {
        title: "操作",
        key: "actions",
        render: (_, item) => (
          <Link
            className="home-recent-open"
            to={caseHref(item.id, item.latest_run?.id ?? null)}
          >
            打开
          </Link>
        ),
      },
    ];
  const audit = [...data.audit]
    .sort((left, right) => right.at.localeCompare(left.at))
    .slice(0, 6);
  const remediations = [...data.remediations]
    .sort((left, right) => right.updated_at.localeCompare(left.updated_at))
    .slice(0, 5);

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

  function submitTask(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    createTask.mutate(
      {
        title: taskTitle.trim(),
        kind: "general",
        assignee_id: taskAssignee,
        due_at: taskDue === "" ? null : `${taskDue}T23:59:59+08:00`,
        status: "open",
        priority: "medium",
        finding_id: null,
      },
      {
        onSuccess: () => {
          setTaskTitle("");
          setTaskDue("");
          setShowTaskForm(false);
        },
      },
    );
  }

  const metrics = [
    {
      key: "cases",
      label: "可见案件",
      value: dashboard.visible_cases,
      hint: "按当前身份权限统计",
      tone: "blue",
      icon: <FolderOpenOutlined />,
    },
    {
      key: "tasks",
      label: "我的待办",
      value: dashboard.my_pending_tasks,
      hint: "由责任人与任务状态计算",
      tone: dashboard.my_pending_tasks > 0 ? "amber" : "neutral",
      icon: <CheckSquareOutlined />,
    },
    {
      key: "late",
      label: "逾期任务",
      value: dashboard.overdue_tasks,
      hint: "按已保存期限统计",
      tone: dashboard.overdue_tasks > 0 ? "red" : "neutral",
      icon: <ClockCircleOutlined />,
    },
    {
      key: "reviews",
      label: "待审查案件",
      value: dashboard.pending_review_cases,
      hint: "等待审查或人工处理",
      tone: dashboard.pending_review_cases > 0 ? "amber" : "neutral",
      icon: <FileSearchOutlined />,
    },
  ] as const;

  return (
    <section className="page home-page">
      <header className="home-page-head">
        <div className="home-welcome">
          <h1>你好，{session.data?.display_name ?? "当前用户"}</h1>
          <p>这里展示你有权查看的案件、审查、整改和协作进度。</p>
        </div>
        <div className="home-head-actions">
          <label className="home-period-select">
            统计周期
            <Select<string>
              aria-label={"统计周期"}
              value={String(period)}
              onChange={(selectedValue) => {
                const value = selectedValue;
                setPeriod(
                  value === "day" || value === "month" ? value : "week",
                );
              }}
              options={[
                { value: "day", label: "今日" },
                { value: "week", label: "本周" },
                { value: "month", label: "本月" },
              ]}
            />
          </label>
          <Button
            aria-label={"打开案件中心"}
            type="primary"
            className="home-case-link"
            href="/cases"
          >
            打开案件中心
          </Button>
        </div>
      </header>

      <CircuitStrip openCount={openCircuits} nodes={circuitNodes} />

      <dl className="home-metrics" aria-label="工作台摘要">
        {metrics.map((metric) => (
          <div
            className={`home-metric home-metric-${metric.tone}`}
            key={metric.key}
          >
            <span className="home-metric-icon" aria-hidden="true">
              {metric.icon}
            </span>
            <div>
              <dt>{metric.label}</dt>
              <dd>{metric.value}</dd>
              <p>{metric.hint}</p>
            </div>
          </div>
        ))}
      </dl>

      <div className="home-content-grid workbench-content-grid">
        <IntakePrimaryContract actor={session.data ?? null} />
        <section
          className="home-panel home-tasks"
          aria-labelledby="home-tasks-title"
        >
          <header className="home-panel-head">
            <div className="home-section-title">
              <span aria-hidden="true" />
              <h2 id="home-tasks-title">任务与待办</h2>
              <strong className="home-figure-badge">{activeTaskCount}</strong>
            </div>
            <Button
              aria-label={String(showTaskForm ? "收起" : "新建任务")}
              type="text"
              htmlType="button"
              onClick={() => setShowTaskForm((current) => !current)}
            >
              {showTaskForm ? "收起" : "新建任务"}
            </Button>
          </header>
          <Tabs
            activeKey={taskFilter}
            onChange={(key) => {
              if (key === "mine" || key === "all" || key === "completed")
                setTaskFilter(key);
            }}
            items={[
              { key: "mine", label: `我的待办 ${dashboard.my_pending_tasks}` },
              { key: "all", label: `全部未完成 ${activeTaskCount}` },
              {
                key: "completed",
                label: `已完成 ${allTasks.length - activeTaskCount}`,
              },
            ]}
          />
          {showTaskForm ? (
            <form className="home-task-form" onSubmit={submitTask}>
              <label>
                关联案件
                <Select<string>
                  aria-label={"关联案件"}
                  value={String(taskCaseId)}
                  onChange={(selectedValue) => setTaskCaseId(selectedValue)}
                  options={[
                    { value: "", label: "请选择" },
                    ...data.cases.map((item) => ({
                      value: String(item.id),
                      label: String(item.title ?? ""),
                    })),
                  ]}
                />
              </label>
              <label>
                任务名称
                <Input
                  required
                  maxLength={240}
                  value={taskTitle}
                  onChange={(event) => setTaskTitle(event.target.value)}
                />
              </label>
              <label>
                责任人
                <Select<string>
                  aria-label={"责任人"}
                  value={String(taskAssignee)}
                  onChange={(selectedValue) => setTaskAssignee(selectedValue)}
                  options={[
                    { value: "", label: "请选择" },
                    ...data.members.map((member) => ({
                      value: String(member.id),
                      label: (
                        <>
                          {member.display_name}，{member.role}
                        </>
                      ),
                    })),
                  ]}
                />
              </label>
              <label>
                截止日期
                <Input
                  type="date"
                  value={taskDue}
                  onChange={(event) => setTaskDue(event.target.value)}
                />
              </label>
              {createTask.isError ? (
                <p role="alert">{errorMessage(createTask.error)}</p>
              ) : null}
              <Button
                aria-label={String(
                  createTask.isPending ? "正在保存" : "创建并保存任务",
                )}
                type="primary"
                htmlType="submit"
                disabled={
                  createTask.isPending ||
                  taskCaseId === "" ||
                  taskAssignee === ""
                }
              >
                {createTask.isPending ? "正在保存" : "创建并保存任务"}
              </Button>
            </form>
          ) : null}
          {tasks.length === 0 ? (
            <p className="home-empty" role="status">
              {taskFilter === "mine"
                ? "当前身份没有未完成的分派任务。"
                : "没有符合条件的任务记录。"}
            </p>
          ) : (
            <ul className="home-task-list">
              {tasks.slice(0, 8).map((task) => (
                <TaskRow
                  key={task.id}
                  task={task}
                  caseTitle={caseTitles.get(task.case_id) ?? task.case_id}
                  assignee={members.get(task.assignee_id) ?? task.assignee_id}
                  onComplete={() => completeTask(task)}
                  pending={
                    updateTask.isPending &&
                    updateTask.variables?.taskId === task.id
                  }
                />
              ))}
            </ul>
          )}
          {updateTask.isError ? (
            <p className="home-inline-error" role="alert">
              {errorMessage(updateTask.error)}
            </p>
          ) : null}
          {tasks.length > 8 ? (
            <Link className="home-view-all" to="/workbench/tasks">
              查看全部任务
            </Link>
          ) : null}
        </section>

        <aside
          className="home-summary-stack"
          aria-label="真实审查进展与风险分布"
        >
          <section className="home-panel" aria-labelledby="home-progress-title">
            <header className="home-panel-head">
              <div className="home-section-title">
                <span aria-hidden="true" />
                <h2 id="home-progress-title">审查进展</h2>
              </div>
              <span className="home-panel-meta">
                {period === "day"
                  ? "今日"
                  : period === "week"
                    ? "本周"
                    : "本月"}
              </span>
            </header>
            <ProgressDonut
              rows={dashboard.progress}
              visibleCases={dashboard.visible_cases}
            />
            <p className="home-summary-note">
              环图按可见案件的互斥业务状态统计；已完成审查{" "}
              {dashboard.completed_reviews} 项；平均运行时长{" "}
              {dashboard.average_duration_ms === null
                ? "未知"
                : `${(dashboard.average_duration_ms / 1000).toFixed(1)} 秒`}
              。
            </p>
          </section>
          <section className="home-panel" aria-labelledby="home-risk-title">
            <header className="home-panel-head">
              <div className="home-section-title">
                <span aria-hidden="true" />
                <h2 id="home-risk-title">审查发现分布</h2>
              </div>
              <span className="home-panel-meta">后端统计</span>
            </header>
            <ProgressList
              kind="risk"
              total={dashboard.risks.reduce(
                (total, row) => total + row.count,
                0,
              )}
              rows={dashboard.risks.map((row) => ({
                key: row.severity,
                label:
                  row.severity === "high"
                    ? "高风险"
                    : row.severity === "medium"
                      ? "中风险"
                      : row.severity === "low"
                        ? "低风险"
                        : row.severity === "info"
                          ? "提示"
                          : "未分级",
                count: row.count,
              }))}
            />
            <p className="home-summary-note">
              按可见案件最新审查的发现项分级统计；未分级事项保持未分级。
            </p>
          </section>
        </aside>

        <section
          className="home-panel home-recent"
          aria-labelledby="home-recent-title"
        >
          <header className="home-panel-head">
            <div className="home-section-title">
              <span aria-hidden="true" />
              <h2 id="home-recent-title">最近审查与案件</h2>
            </div>
            <Link className="home-view-all" to="/cases">
              查看全部案件
            </Link>
          </header>
          {cases.length === 0 ? (
            <p className="home-empty">当前身份没有可见案件。</p>
          ) : (
            <div className="home-table-scroll">
              <Table
                className="data-table home-recent-table"
                columns={recentCaseColumns}
                dataSource={recentCases}
                rowKey="id"
                pagination={false}
              />
            </div>
          )}
        </section>

        <section
          className="home-panel home-recent"
          aria-labelledby="home-remediation-title"
        >
          <header className="home-panel-head">
            <div className="home-section-title">
              <span aria-hidden="true" />
              <h2 id="home-remediation-title">整改跟踪</h2>
            </div>
            <Link className="home-view-all" to="/workbench/remediation">
              进入整改中心
            </Link>
          </header>
          {remediations.length === 0 ? (
            <p className="home-empty">没有获取到整改记录。</p>
          ) : (
            <ul className="home-record-list">
              {remediations.map((item) => (
                <li key={item.id}>
                  <Link to={caseHref(item.case_id, item.run_id)}>
                    <strong>{item.measure}</strong>
                    <span>
                      {item.case_title}，责任人{" "}
                      {members.get(item.assignee_id) ?? item.assignee_id}
                    </span>
                  </Link>
                  <span className="home-record-status">{item.status}</span>
                  <time dateTime={item.due_at}>
                    {formatDateTime(item.due_at)}
                  </time>
                </li>
              ))}
            </ul>
          )}
        </section>

        <section
          className="home-panel home-recent"
          aria-labelledby="home-audit-title"
        >
          <header className="home-panel-head">
            <div className="home-section-title">
              <span aria-hidden="true" />
              <h2 id="home-audit-title">最近业务记录</h2>
            </div>
            <span className="home-panel-meta">
              {data.audit.length} 条可见事件
            </span>
          </header>
          {audit.length === 0 ? (
            <p className="home-empty">没有获取到审计记录。</p>
          ) : (
            <ol className="home-audit-list">
              {audit.map((event) => (
                <li key={event.id}>
                  <time dateTime={event.at}>{formatDateTime(event.at)}</time>
                  <div>
                    {event.case_id ? (
                      <Link to={`/cases/${encodeURIComponent(event.case_id)}`}>
                        {event.case_title ?? event.case_id}
                      </Link>
                    ) : (
                      <span>平台级事件</span>
                    )}
                    <p>{event.summary}</p>
                    <small>
                      {event.actor_name}，{event.action}
                    </small>
                  </div>
                </li>
              ))}
            </ol>
          )}
        </section>
      </div>
      <p className="home-snapshot-time">
        数据生成于{" "}
        <time dateTime={data.generated_at}>
          {formatDateTime(data.generated_at)}
        </time>
      </p>
    </section>
  );
}
