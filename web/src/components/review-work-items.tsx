// File metadata (manifest mirror):
// Purpose: Renders the manual-work navigation, server-derived category counts, readable provenance and links to existing finding, evidence and remediation controls.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-05
// Updated: 2026-11-05
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { Button } from "antd";
import type { WorkFilter, WorkItem, WorkProjection, WorkTarget } from "../features/review/work-items";
import { TechnicalDetail } from "./technical-detail";
import { StatusText } from "./status-text";
import "./review-work-items.css";

const labels: Record<WorkFilter, string> = {
  judgment: "待判断",
  evidence: "待补件",
  remediation: "待整改",
};

function targetLabel(item: WorkItem): string {
  if (!item.target) return "";
  switch (item.target.kind) {
    case "finding": return "查看判断与原文";
    case "gap": return "查看缺口与引用";
    case "evaluation": return "查看补件来源";
    case "request-source": return "查看补件来源";
    case "request": return "查看补件请求";
    case "remediation": return "查看整改记录";
  }
}

export function ReviewWorkItems({ projection, filter, onFilter, onOpen, onContinueRequest, refreshing, error, canPrepareRequest, currentRunId, currentAttemptId, currentOrdinal, selectedKind = null, selectedId = null }: {
  projection: WorkProjection;
  filter: WorkFilter;
  onFilter: (filter: WorkFilter) => void;
  onOpen: (target: WorkTarget) => void;
  onContinueRequest?: (target: WorkTarget) => void;
  refreshing: boolean;
  error: string | null;
  canPrepareRequest: boolean;
  currentRunId: string;
  currentAttemptId: string | null;
  currentOrdinal: number | null;
  selectedKind?: string | null;
  selectedId?: string | null;
}) {
  const visible = projection.items.filter((item) => item.category === filter);
  function summary(kind: WorkFilter) {
    const known = kind === "judgment" ? projection.frozenScope
      : kind === "evidence" ? projection.sourcesKnown && projection.requestsKnown
        : projection.caseKnown;
    if (!known || error || refreshing) return "待核对";
    const pending = projection.items.filter((item) => item.category === kind && !item.needsCheck).length;
    const uncertain = projection.items.filter((item) => item.category === kind && item.needsCheck).length;
    return uncertain ? `${pending}，另有 ${uncertain} 项待核对` : String(pending);
  }
  return (
    <section className="workflow-panel review-work-items" aria-labelledby="review-work-items-title">
      <h2 id="review-work-items-title">人工办理</h2>
      <p className="meta">显示所选冻结执行的待判断发现和尚无请求的检查来源，以及本案件已建的未完成补件、整改；其他历史执行中尚未建立请求的来源不在此汇总。</p>
      <p className="meta">待补件也包括预算、能力或来源内容需要核对的事项；不代表已经确认缺少材料或存在风险。</p>
      {projection.agentSkipped ? <p className="meta">本次执行明确未进行智能体审查；这里只汇总已返回的规则来源。</p> : null}
      <div className="filter-row" role="group" aria-label="人工办理分类">
        {(Object.keys(labels) as WorkFilter[]).map((kind) => (
          <Button key={kind} className="review-work-items-filter" htmlType="button" aria-pressed={filter === kind} onClick={() => onFilter(kind)}>
            {labels[kind]}：{summary(kind)}
          </Button>
        ))}
      </div>
      {refreshing ? <p role="status">正在刷新办理状态，数量待核对。</p> : null}
      {error ? <p role="alert">{error}；数量待重新核对。</p> : null}
      {!projection.frozenScope ? <p>当前执行的冻结身份不可确认，发现和来源不计入数量。</p> : null}
      {visible.length === 0 ? (
        <p>{error || refreshing || !projection.frozenScope || (filter === "evidence" && (!projection.requestsKnown || !projection.sourcesKnown)) || (filter === "remediation" && !projection.caseKnown)
          ? "当前分类的状态尚未确认。" : "当前分类没有未完成事项。"}</p>
      ) : (
        <ul className="workflow-record-list review-work-items-list" aria-label={`${labels[filter]}事项`}>
          {visible.map((item) => (
            <li key={item.key} aria-current={item.target?.kind === selectedKind && item.target.id === selectedId ? "true" : undefined}>
              <strong>{item.title}</strong>
              <p>{item.origin} · <StatusText tone={item.needsCheck ? "attention" : "progress"}>{item.status}</StatusText></p>
              <p>{item.verification}</p>
              <p className="meta">来源冻结审查：{!item.sourceRunId || !item.sourceAttemptId ? "身份未返回，需核对"
                : item.sourceRunId === currentRunId && item.sourceAttemptId === currentAttemptId
                  ? currentOrdinal !== null ? `当前第 ${currentOrdinal} 次执行` : "当前选中执行"
                  : "其他来源执行"}</p>
              {item.sourceRunId && item.sourceAttemptId ? <TechnicalDetail label="来源执行技术详情" value={`${item.sourceRunId} / ${item.sourceAttemptId}`} /> : null}
              {item.target ? <Button htmlType="button" onClick={() => onOpen(item.target!)}>{targetLabel(item)}</Button> : null}
              {item.target?.kind === "request" && onContinueRequest ? <Button htmlType="button" onClick={() => onContinueRequest(item.target!)}>前往当前办理环境</Button> : null}
              {item.sourceTarget ? <Button htmlType="button" onClick={() => onOpen(item.sourceTarget!)}>查看来源审查与引用</Button> : null}
              {item.requestSourceTarget && canPrepareRequest ? <Button htmlType="button" onClick={() => onOpen(item.requestSourceTarget!)}>打开补件请求入口</Button> : null}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
