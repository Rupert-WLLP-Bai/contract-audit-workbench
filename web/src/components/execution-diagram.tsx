// File metadata (manifest mirror):
// Purpose: Renders saved execution events through a read-only LogicFlow diagram and presents the selected recorded observations.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import LogicFlow, { RectNode, RectNodeModel } from "@logicflow/core";
import { useEffect, useMemo, useRef, useState } from "react";
import type { ExecutionEvent } from "../api/types";
import { elapsed, eventStatusLabel, tokenCount } from "../domain/execution-labels";
import { buildExecutionDiagram, type ExecutionDiagramNode } from "../domain/execution-diagram";
import logicFlowCss from "@logicflow/core/dist/index.css?raw";
import "./execution-diagram.css";

const LANES = ["材料与事实", "规划与工具", "模型", "核验与留痕"] as const;
function laneOf(node: ExecutionDiagramNode): number {
  if (node.kind === "model") return 2;
  if (node.kind === "tool") return 1;
  const phase = node.event.phase.toLowerCase();
  if (/input|fact|material|document|prepar/.test(phase)) return 0;
  if (/tool|plan|rule/.test(phase)) return 1;
  if (/model/.test(phase)) return 2;
  return 3;
}

class ExecutionNodeModel extends RectNodeModel {
  setAttributes() {
    this.width = 185;
    this.height = 76;
    this.radius = 10;
  }
  getNodeStyle() {
    const style = super.getNodeStyle();
    style.fill = this.properties.selected ? "var(--action-primary-soft)" : "var(--surface-raised)";
    style.stroke = this.properties.selected ? "var(--action-primary)" : "var(--border-default)";
    style.strokeWidth = this.properties.selected ? 2.5 : 1;
    return style;
  }
}
class LaneModel extends RectNodeModel {
  setAttributes() {
    this.width = 125;
    this.height = 72;
    this.radius = 8;
  }
  getNodeStyle() {
    const style = super.getNodeStyle();
    style.fill = "var(--surface-subtle)";
    style.stroke = "var(--border-default)";
    return style;
  }
}

export function ExecutionDiagram({
  events,
  selectedSeq,
  onSelectSeq,
}: {
  events: ExecutionEvent[];
  selectedSeq: number | null;
  onSelectSeq: (seq: number) => void;
}) {
  const nodes = useMemo(() => buildExecutionDiagram(events), [events]);
  const pageSize = 5;
  const [page, setPage] = useState(() => {
    const index = nodes.findIndex((node) => node.events.some((event) => event.seq === selectedSeq));
    return index < 0 ? 0 : Math.floor(index / pageSize);
  });
  const pageCount = Math.max(1, Math.ceil(nodes.length / pageSize));
  const currentPage = Math.min(page, pageCount - 1);
  const visibleNodes = useMemo(
    () => nodes.slice(currentPage * pageSize, (currentPage + 1) * pageSize),
    [nodes, currentPage],
  );
  const container = useRef<HTMLDivElement>(null);
  const flow = useRef<LogicFlow | null>(null);
  const select = useRef(onSelectSeq);
  select.current = onSelectSeq;

  useEffect(() => {
    if (!container.current || nodes.length === 0) return;
    const lf = new LogicFlow({
      container: container.current,
      grid: false,
      isSilentMode: true,
      stopScrollGraph: true,
    });
    flow.current = lf;
    lf.register({ type: "execution-node", view: RectNode, model: ExecutionNodeModel });
    lf.register({ type: "execution-lane", view: RectNode, model: LaneModel });
    lf.setTheme({
      nodeText: { fontSize: 13, color: "var(--text-primary)" },
      polyline: { stroke: "var(--text-secondary)", strokeWidth: 1.5 },
    });
    lf.on("node:click", ({ data }) => {
      const node = nodes.find((item) => item.id === data.id);
      if (node) select.current(node.event.seq);
    });
    lf.render({
      nodes: [
        ...LANES.map((lane, index) => ({
          id: `lane-${index}`,
          type: "execution-lane",
          x: 90,
          y: 70 + index * 125,
          text: lane,
        })),
        ...visibleNodes.map((node, position) => {
          const lane = laneOf(node);
          const last = [...node.events].reverse();
          const status = last.find((item) => item.status)?.status;
          const duration = last.find((item) => item.duration_ms !== null)?.duration_ms ?? null;
          const usage = last.find((item) => item.usage)?.usage;
          const title = node.title.length > 19 ? `${node.title.slice(0, 18)}…` : node.title;
          const metrics =
            node.kind === "model"
              ? ` · ${usage ? `${tokenCount(usage.total_tokens)} tokens` : "Token 未报告"}`
              : "";
          return {
            id: node.id,
            type: "execution-node",
            properties: { selected: false },
            x: 285 + position * 220,
            y: 70 + lane * 125,
            text: `#${node.events[0]!.seq}  ${title}\n${status ? eventStatusLabel(status) : "状态未报告"} · ${elapsed(duration)}${metrics}`,
          };
        }),
      ],
      edges: visibleNodes
        .slice(1)
        .map((node, index) => ({
          type: "polyline",
          sourceNodeId: visibleNodes[index]!.id,
          targetNodeId: node.id,
        })),
    });
    lf.fitView(30, 30);
    const observer =
      typeof ResizeObserver === "undefined"
        ? null
        : new ResizeObserver(() => {
            lf.resize();
          });
    observer?.observe(container.current);
    return () => {
      observer?.disconnect();
      lf.destroy();
      flow.current = null;
    };
  }, [nodes, visibleNodes]);

  // Selection changes can originate from the event ledger. Manual page changes
  // do not change selectedSeq, so they remain under the user's control.
  useEffect(() => {
    if (selectedSeq === null) return;
    const index = nodes.findIndex((node) => node.events.some((event) => event.seq === selectedSeq));
    if (index >= 0) setPage(Math.floor(index / pageSize));
  }, [nodes, selectedSeq]);

  useEffect(() => {
    if (!flow.current) return;
    for (const node of visibleNodes)
      flow.current.setProperties(node.id, {
        selected: node.events.some((event) => event.seq === selectedSeq),
      });
  }, [visibleNodes, selectedSeq]);

  if (nodes.length === 0) return <p>本次执行没有保存可展示的步骤。</p>;
  return (
    <div className="execution-diagram">
      <div className="execution-diagram-toolbar" aria-label="执行图控制">
        <span>{nodes.length} 个调用 / 阶段 · 箭头表示保存顺序，调用层级见节点详情</span>
        <div>
          <button type="button" onClick={() => flow.current?.zoom(true)} aria-label="放大执行图">
            ＋
          </button>
          <button type="button" onClick={() => flow.current?.zoom(false)} aria-label="缩小执行图">
            −
          </button>
          <button type="button" onClick={() => flow.current?.fitView(30, 30)}>
            复位
          </button>
          <button
            type="button"
            onClick={() => flow.current?.translate(-80, 0)}
            aria-label="向左平移执行图"
          >
            ←
          </button>
          <button
            type="button"
            onClick={() => flow.current?.translate(80, 0)}
            aria-label="向右平移执行图"
          >
            →
          </button>
          <button
            type="button"
            onClick={() => flow.current?.translate(0, -80)}
            aria-label="向上平移执行图"
          >
            ↑
          </button>
          <button
            type="button"
            onClick={() => flow.current?.translate(0, 80)}
            aria-label="向下平移执行图"
          >
            ↓
          </button>
        </div>
      </div>
      {pageCount > 1 ? (
        <div className="execution-diagram-pages" aria-label="执行图分段">
          <button
            type="button"
            disabled={currentPage === 0}
            onClick={() => setPage(currentPage - 1)}
          >
            上一段
          </button>
          <span>
            第 {currentPage + 1} / {pageCount} 段 · 步骤 {currentPage * pageSize + 1}–
            {Math.min((currentPage + 1) * pageSize, nodes.length)}，按保存顺序接续
          </span>
          <button
            type="button"
            disabled={currentPage === pageCount - 1}
            onClick={() => setPage(currentPage + 1)}
          >
            下一段
          </button>
        </div>
      ) : null}
      <style>{`@scope (.execution-diagram-canvas) { ${logicFlowCss} }`}</style>
      <div className="execution-diagram-canvas" ref={container} aria-hidden="true" />
      <details className="execution-diagram-index">
        <summary>按顺序选择节点（键盘可用）</summary>
        <div>
          {nodes.map((node) => (
            <button
              key={node.id}
              type="button"
              aria-pressed={node.events.some((event) => event.seq === selectedSeq)}
              onClick={() => {
                setPage(Math.floor(nodes.indexOf(node) / pageSize));
                onSelectSeq(node.event.seq);
              }}
            >
              {node.kind === "model" ? "模型" : node.kind === "tool" ? "工具" : "阶段"} ·{" "}
              {node.title} · #{node.event.seq}
            </button>
          ))}
        </div>
      </details>
    </div>
  );
}
