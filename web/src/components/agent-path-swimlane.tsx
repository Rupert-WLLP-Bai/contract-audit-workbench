// File metadata (manifest mirror):
// Purpose: Renders the saved Agent execution path as swimlanes, showing each step's lane, tool/model, span parent and reported token usage without inventing skill steps.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-12
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { useState } from "react";
import type { Execution } from "../api/types";
import { elapsed, eventTypeLabel, tokenCount } from "../domain/execution-labels";
import { buildExecutionDiagram, isEinoToolEvent } from "../domain/execution-diagram";
import { ExecutionDiagram } from "./execution-diagram";
import { TechnicalDetail } from "./technical-detail";

export function AgentPathSwimlane({ execution }: { execution: Execution }) {
  const [selectedSeq, setSelectedSeq] = useState<number | null>(null);
  const event = execution.events.find((item) => item.seq === selectedSeq);
  const node = event
    ? buildExecutionDiagram(execution.events).find((item) => item.events.includes(event))
    : null;
  return (
    <section className="caw-swimlane" aria-label="Agent 执行路径泳道图">
      <header className="caw-swimlane-head">
        <div>
          <h3>Agent 执行路径</h3>
          <p className="meta">按调用 Span 合并已保存事件；Token 只显示报告值。</p>
        </div>
      </header>
      <ExecutionDiagram
        events={execution.events}
        selectedSeq={selectedSeq}
        onSelectSeq={setSelectedSeq}
      />
      {event ? (
        <div className="caw-swimlane-selected">
          <h4>
            {node?.title} · #{event.seq}
          </h4>
          <p>
            耗时 {elapsed(event.duration_ms)} · {event.status ?? "状态未报告"}
          </p>
          <p>
            输入 {tokenCount(event.usage?.input_tokens ?? null)} · 输出{" "}
            {tokenCount(event.usage?.output_tokens ?? null)} · 合计{" "}
            {tokenCount(event.usage?.total_tokens ?? null)} · 来源 {event.usage?.source || "未报告"}
          </p>
          {isEinoToolEvent(event, execution.agent_review?.planner.runtime_version) ? (
            <p>调用方式 Eino InvokableTool</p>
          ) : null}
          <p>
            Span {event.span_id ?? "未报告"} · 父 Span {event.parent_span_id ?? "未报告"}
          </p>
          {node && node.events.length > 1 ? (
            <p>
              原始事件：
              {node.events.map((item) => (
                <button type="button" key={item.seq} onClick={() => setSelectedSeq(item.seq)}>
                  #{item.seq} {eventTypeLabel(item.event_type)}
                </button>
              ))}
            </p>
          ) : null}
          {event.trace ? (
            <>
              <TechnicalDetail
                label="查看已保存输入"
                value={JSON.stringify(event.trace.request_messages, null, 2)}
              />
              <TechnicalDetail
                label="查看已保存输出"
                value={event.trace.response_text || "未报告"}
              />
            </>
          ) : null}
        </div>
      ) : (
        <p className="meta">选择节点查看保存的调用详情。</p>
      )}
    </section>
  );
}
