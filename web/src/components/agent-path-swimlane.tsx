// File metadata (manifest mirror):
// Purpose: Renders the saved Agent execution path as swimlanes, showing each step's lane, tool/model, span parent and reported token usage without inventing skill steps.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: unknown
// Updated: unknown
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { useMemo, useState } from "react";
import type { Execution } from "../api/types";
import { instant, tokenCount } from "../domain/execution-labels";
import { buildSwimlanes, type SwimlaneStep } from "../domain/swimlane";
import { StatusText } from "./status-text";

const KIND_LABELS: Record<SwimlaneStep["kind"], string> = {
  model: "模型",
  tool: "工具",
  knowledge: "知识",
  stage: "阶段",
  event: "事件",
};

function toneFor(status: string): "good" | "bad" | "progress" | "neutral" {
  if (status === "failed" || status === "cancelled" || status === "interrupted") return "bad";
  if (status === "running" || status === "started" || status === "queued") return "progress";
  if (status === "completed" || status === "partial") return "good";
  return "neutral";
}

function StepCard({ step }: { step: SwimlaneStep }) {
  return (
    <details className="caw-swim-step" data-kind={step.kind} open>
      <summary>
        <span className="caw-swim-seq" aria-label={`步骤 ${step.seq}`}>
          {step.seq}
        </span>
        <span className="caw-swim-title caw-wrap">
          <span className="caw-swim-kind">{KIND_LABELS[step.kind]}</span>
          {step.title}
        </span>
        <StatusText tone={toneFor(step.status)}>{step.status}</StatusText>
      </summary>
      <div className="caw-swim-detail">
        <dl>
          <div><dt>步骤</dt><dd>#{step.seq} · {KIND_LABELS[step.kind]}</dd></div>
          <div><dt>时间</dt><dd>{instant(step.at)}</dd></div>
          <div><dt>耗时</dt><dd>{step.durationMs === null ? "未报告" : `${step.durationMs} 毫秒`}</dd></div>
          {step.tool ? <div><dt>工具 / 对象</dt><dd className="caw-wrap">{step.tool}</dd></div> : null}
          {step.model ? <div><dt>模型</dt><dd className="caw-wrap">{step.model}</dd></div> : null}
          {step.spanId ? <div><dt>调用层级</dt><dd className="caw-wrap">span {step.spanId}{step.parentSpanId ? ` ← ${step.parentSpanId}` : ""}</dd></div> : null}
          {step.errorCode ? <div><dt>错误码</dt><dd className="caw-wrap">{step.errorCode}</dd></div> : null}
        </dl>
        <div className="caw-swim-usage" aria-label="本步 Token 用量">
          <span>输入 <strong>{tokenCount(step.inputTokens)}</strong></span>
          <span>输出 <strong>{tokenCount(step.outputTokens)}</strong></span>
          <span>合计 <strong>{tokenCount(step.totalTokens)}</strong></span>
          {step.usageKnown ? (
            <span className="caw-swim-usage-source caw-wrap">
              来源 {step.usageSource || "未记录"}
            </span>
          ) : (
            <span className="caw-swim-usage-unknown">本步用量未报告，不推算</span>
          )}
        </div>
      </div>
    </details>
  );
}

export function AgentPathSwimlane({ execution }: { execution: Execution }) {
  const model = useMemo(() => buildSwimlanes(execution), [execution]);
  const [showAll, setShowAll] = useState(false);
  const [selected, setSelected] = useState<SwimlaneStep | null>(null);
  const steps = showAll ? model.steps : model.steps.filter((step) => step.seq <= 40);
  const visible = new Set(steps.map((step) => step.id));
  const visibleLanes = model.lanes
    .map((lane) => ({ ...lane, steps: lane.steps.filter((step) => visible.has(step.id)) }))
    .filter((lane) => lane.steps.length > 0);
  return (
    <section className="caw-swimlane" aria-label="Agent 执行路径泳道图">
      <header className="caw-swimlane-head">
        <div>
          <h3>Agent 执行路径</h3>
          <p className="meta">
            按执行阶段分泳道、按保存顺序展示每一步；工具/模型步骤标注调用对象与 span 层级，
            Token 只显示本次执行实际报告的数值。当前执行事件没有 skill 类型，
            因此不绘制 invoke skill 泳道，也不臆造其消耗。
          </p>
        </div>
        <StatusText tone="neutral">
          {model.steps.length > steps.length
            ? `已显示 ${steps.length} / ${model.steps.length} 步 · ${visibleLanes.length} 泳道`
            : `${model.steps.length} 步 / ${model.lanes.length} 泳道`}
        </StatusText>
      </header>
      <dl className="caw-swimlane-summary">
        <div><dt>有用量报告的步骤</dt><dd>{model.reportedUsageSteps}</dd></div>
        <div><dt>已保存 skill 事件</dt><dd>{model.skillEvents}</dd></div>
        <div><dt>工具 / 模型步骤</dt><dd>{model.steps.filter((step) => step.kind === "tool" || step.kind === "model").length}</dd></div>
      </dl>
      {model.lanes.length === 0 ? (
        <p className="trajectory-empty">本次执行没有保存可展示的步骤。</p>
      ) : (
        <div className="caw-swimlane-scroll caw-table-scroll" role="group" aria-label="执行泳道">
          {visibleLanes.map((lane) => {
            return (
              <div className="caw-swimlane-lane" key={lane.id}>
                <div className="caw-swimlane-label caw-wrap" title={lane.id}>{lane.label}</div>
                <div className="caw-swimlane-track">
                  {lane.steps.map((step) => (
                    <button
                      type="button"
                      key={step.id}
                      className="caw-swimlane-node"
                      data-kind={step.kind}
                      aria-label={`${lane.label} 第 ${step.seq} 步 ${step.title}`}
                      onClick={() => setSelected(step)}
                    >
                      <span className="caw-swimlane-node-seq">#{step.seq}</span>
                      <span className="caw-swimlane-node-title caw-wrap">{step.title}</span>
                      <span className="caw-swimlane-node-usage">
                        {step.usageKnown ? `${tokenCount(step.totalTokens)} tok` : "用量未报告"}
                      </span>
                    </button>
                  ))}
                </div>
              </div>
            );
          })}
        </div>
      )}
      {model.steps.length > 40 ? (
        <button type="button" className="caw-swimlane-more" onClick={() => setShowAll((value) => !value)}>
          {showAll ? "仅显示前 40 步" : `显示全部 ${model.steps.length} 步`}
        </button>
      ) : null}
      {selected ? (
        <div className="caw-swimlane-selected">
          <button type="button" className="caw-swimlane-close" onClick={() => setSelected(null)}>收起步骤详情</button>
          <StepCard step={selected} />
        </div>
      ) : (
        <p className="meta">点击泳道中的步骤可查看工具/模型、调用层级与 Token 明细。</p>
      )}
    </section>
  );
}
