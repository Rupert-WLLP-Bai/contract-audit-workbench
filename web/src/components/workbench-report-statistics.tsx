import { Link } from "react-router";
import type { WorkbenchPeriod, WorkbenchSnapshot } from "../api/types";
import { businessActionLabel } from "../domain/business-labels";
import { caseHref, formatDateTime } from "../domain/review";

const risks = {
  high: "高风险",
  medium: "中风险",
  low: "低风险",
  info: "提示",
  unrated: "未分级",
} as const;

export function WorkbenchReportStatistics({
  snapshot,
  changePeriod,
}: {
  snapshot: WorkbenchSnapshot;
  changePeriod: (period: WorkbenchPeriod) => void;
}) {
  const { dashboard } = snapshot;
  const total = dashboard.progress.reduce((sum, row) => sum + row.count, 0);
  const open = dashboard.progress.find((row) => row.status === "active")?.count ?? 0;
  const closed = dashboard.progress.find((row) => row.status === "archived")?.count ?? 0;
  const openShare = total > 0 ? (open / total) * 100 : 0;
  const knownShare = total > 0 ? ((open + closed) / total) * 100 : 0;
  const breakdown = total
    ? `conic-gradient(var(--action-primary) 0 ${openShare}%, var(--state-compatible) ${openShare}% ${knownShare}%, var(--surface-subtle) ${knownShare}% 100%)`
    : "var(--surface-subtle)";
  const riskCount = dashboard.risks.reduce((sum, row) => sum + row.count, 0);
  return (
    <details className="workbench-report-statistics">
      <summary>审查统计与业务记录</summary>
      <div className="workbench-report-statistics-content">
        <label className="module-search">
          统计周期
          <select
            value={snapshot.period}
            onChange={(event) => {
              const period = event.target.value;
              if (period === "day" || period === "week" || period === "month") changePeriod(period);
            }}
          >
            <option value="day">今天</option>
            <option value="week">本周</option>
            <option value="month">本月</option>
          </select>
        </label>
        <dl className="workbench-report-counts">
          <div>
            <dt>可见案件</dt>
            <dd>{dashboard.visible_cases}</dd>
          </div>
          <div>
            <dt>待审查案件</dt>
            <dd>{dashboard.pending_review_cases}</dd>
          </div>
          <div>
            <dt>本周期完成审查</dt>
            <dd>{dashboard.completed_reviews}</dd>
          </div>
          <div>
            <dt>平均运行时长</dt>
            <dd>
              {dashboard.average_duration_ms === null
                ? "未报告"
                : `${(dashboard.average_duration_ms / 1000).toFixed(1)} 秒`}
            </dd>
          </div>
          <div>
            <dt>可见案件逾期任务</dt>
            <dd>{dashboard.overdue_tasks}</dd>
          </div>
        </dl>
        <div className="workbench-report-breakdowns">
          <section aria-label="可见案件状态">
            <h2>案件状态</h2>
            <div
              className="workbench-report-donut"
              style={{ background: breakdown }}
              aria-hidden="true"
            />
            <ul>
              {dashboard.progress.map((row) => (
                <li key={row.status}>
                  {row.status === "active"
                    ? "未归档"
                    : row.status === "archived"
                      ? "已归档"
                      : "状态未识别"}{" "}
                  {row.count}
                </li>
              ))}
            </ul>
            {total !== dashboard.visible_cases ? <p>已报告状态 {total} 项。</p> : null}
          </section>
          <section aria-label="审查发现分布">
            <h2>审查发现分布</h2>
            <p>按可见案件的最新审查统计。</p>
            <ul>
              {dashboard.risks.map((row) => (
                <li key={row.severity}>
                  <span>
                    {risks[row.severity]} {row.count}
                  </span>
                  {riskCount ? (
                    <meter
                      min={0}
                      max={riskCount}
                      value={row.count}
                      aria-label={risks[row.severity]}
                    />
                  ) : null}
                </li>
              ))}
            </ul>
          </section>
        </div>
        <section aria-label="最近业务记录">
          <h2>最近业务记录</h2>
          {snapshot.audit.length ? (
            <ul>
              {snapshot.audit.slice(0, 5).map((event) => (
                <li key={event.id}>
                  <time dateTime={event.at}>{formatDateTime(event.at)}</time>
                  {" · "}
                  {event.case_id && event.case_title ? (
                    <Link to={caseHref(event.case_id)}>{event.case_title}</Link>
                  ) : null}
                  {" · "}
                  {businessActionLabel(event.action)} · {event.summary}
                </li>
              ))}
            </ul>
          ) : (
            <p>没有已报告的业务记录。</p>
          )}
        </section>
      </div>
    </details>
  );
}
