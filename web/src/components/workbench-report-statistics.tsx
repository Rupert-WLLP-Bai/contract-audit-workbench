// File metadata (manifest mirror):
// Purpose: Presents the report center's server-derived case, risk and review summaries and recent business records.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-20
// Updated: 2026-11-20
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { Segmented } from "antd";
import { DownOutlined, RightOutlined } from "@ant-design/icons";
import { Link } from "react-router";
import type { WorkbenchPeriod, WorkbenchSnapshot } from "../api/types";
import { businessActionLabel } from "../domain/business-labels";
import { caseHref } from "../domain/review";
import { compactTime } from "../domain/time";

const risks = {
  high: "高风险",
  medium: "中风险",
  low: "低风险",
  info: "提示",
  unrated: "等级待核对",
} as const;
const percent = (count: number, total: number) =>
  total > 0 ? `${Math.round((count / total) * 100)}%` : "—";

export function WorkbenchReportStatistics({
  snapshot,
  changePeriod,
  expanded,
  setExpanded,
}: {
  snapshot: WorkbenchSnapshot;
  changePeriod: (period: WorkbenchPeriod) => void;
  expanded: boolean;
  setExpanded: (expanded: boolean) => void;
}) {
  const { dashboard } = snapshot;
  const total = dashboard.progress.reduce((sum, row) => sum + row.count, 0);
  const riskCount = dashboard.risks.reduce((sum, row) => sum + row.count, 0);
  const counts = [
    ["可见案件", dashboard.visible_cases],
    ["待审查案件", dashboard.pending_review_cases],
    ["本周期完成审查", dashboard.completed_reviews],
    [
      "平均运行时长",
      dashboard.average_duration_ms === null
        ? "—"
        : `${(dashboard.average_duration_ms / 1000).toFixed(1)} 秒`,
    ],
    ["逾期任务", dashboard.overdue_tasks],
  ];
  return (
    <section className="workbench-report-statistics" aria-label="审查统计与业务记录">
      <div className="workbench-report-statistics-bar">
        <button
          type="button"
          className="workbench-report-toggle"
          aria-expanded={expanded}
          aria-controls="workbench-statistics-content"
          onClick={() => setExpanded(!expanded)}
        >
          {expanded ? <DownOutlined aria-hidden /> : <RightOutlined aria-hidden />}{" "}
          审查统计与业务记录
        </button>
        {expanded ? (
          <Segmented<WorkbenchPeriod>
            aria-label="统计周期"
            value={snapshot.period}
            onChange={changePeriod}
            options={[
              { value: "day", label: "今天" },
              { value: "week", label: "本周" },
              { value: "month", label: "本月" },
            ]}
          />
        ) : null}
      </div>
      {expanded ? (
        <div id="workbench-statistics-content" className="workbench-report-statistics-content">
          <dl className="workbench-report-counts">
            {counts.map(([label, value], i) => (
              <div key={label}>
                <dt>
                  {[1, 2, 4].includes(i) ? (
                    <span className={`stats-dot stats-kpi-${i}`} aria-hidden />
                  ) : null}
                  {label}
                </dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
          <div className="workbench-report-breakdowns">
            <section aria-label="可见案件状态">
              <header>
                <h2>案件状态构成</h2>
                <span>共 {dashboard.progress.length ? total : "—"} 件</span>
              </header>
              <div className="stats-stack" aria-hidden="true">
                {dashboard.progress.map((row) => (
                  <span
                    key={row.status}
                    className={`stats-color-${row.status === "active" || row.status === "archived" ? row.status : "unknown"}`}
                    style={{ width: total ? `${(row.count / total) * 100}%` : "0%" }}
                  />
                ))}
              </div>
              {dashboard.progress.length ? (
                <div className="stats-rows">
                  {dashboard.progress.map((row) => (
                    <div className="stats-legend" key={row.status}>
                      <span>
                        <i
                          className={`stats-dot stats-color-${row.status === "active" || row.status === "archived" ? row.status : "unknown"}`}
                        />
                        {row.status === "active"
                          ? "未归档"
                          : row.status === "archived"
                            ? "已归档"
                            : "状态未识别"}
                      </span>
                      <strong>{row.count}</strong>
                      <small>{percent(row.count, total)}</small>
                    </div>
                  ))}
                </div>
              ) : (
                <p>暂无案件状态记录。</p>
              )}
            </section>
            <section aria-label="审查发现分布">
              <header>
                <h2>审查发现分布</h2>
                <span>最新审查 · {dashboard.risks.length ? riskCount : "—"} 条</span>
              </header>
              <p>按可见案件的最新审查统计。</p>
              {dashboard.risks.length ? (
                <div className="stats-rows">
                  {dashboard.risks.map((row) => (
                    <div className="stats-risk" key={row.severity}>
                      <span>
                        <i className={`stats-dot stats-color-${row.severity}`} />
                        {risks[row.severity]}
                      </span>
                      <div className="stats-track" aria-hidden="true">
                        <div
                          className={`stats-color-${row.severity}`}
                          style={{ width: riskCount ? `${(row.count / riskCount) * 100}%` : "0%" }}
                        />
                      </div>
                      <strong>{row.count}</strong>
                      <small>{percent(row.count, riskCount)}</small>
                    </div>
                  ))}
                </div>
              ) : (
                <p>暂无审查发现记录。</p>
              )}
            </section>
            <section aria-label="最近业务记录">
              <header>
                <h2>最近业务动态</h2>
                <span>最新 {Math.min(snapshot.audit.length, 5)} 条</span>
              </header>
              {snapshot.audit.length ? (
                <ul className="stats-timeline">
                  {snapshot.audit.slice(0, 5).map((event) => (
                    <li key={event.id}>
                      <i
                        className={`stats-dot ${event.action === "report.created" ? "stats-color-archived" : event.action === "remediation.submitted" ? "stats-color-active" : event.action === "remediation.created" ? "stats-color-medium" : "stats-color-unknown"}`}
                      />
                      <span
                        title={[businessActionLabel(event.action), event.case_title, event.summary]
                          .filter(Boolean)
                          .join(" · ")}
                      >
                        <strong>{businessActionLabel(event.action)}</strong>
                        {event.case_id && event.case_title ? (
                          <>
                            {" "}
                            · <Link to={caseHref(event.case_id)}>{event.case_title}</Link>
                          </>
                        ) : null}
                        {event.summary ? <> · {event.summary}</> : null}
                      </span>
                      <time dateTime={event.at}>{compactTime(event.at)}</time>
                    </li>
                  ))}
                </ul>
              ) : (
                <p>暂无业务记录。</p>
              )}
            </section>
          </div>
        </div>
      ) : null}
    </section>
  );
}
