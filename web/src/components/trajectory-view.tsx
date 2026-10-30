import { Button, Input } from "antd";
import { useMemo, useState } from "react";
import type { Execution } from "../api/types";
import { elapsed, instant, tokenCount } from "../domain/execution-labels";
import {
  buildTrajectory,
  type TrajectoryEntry,
  type TrajectoryKind,
  type TrajectorySection,
} from "../domain/trajectory";
import { StatusText } from "./status-text";

const KIND_LABELS: Record<TrajectoryKind, string> = {
  stage: "阶段",
  model: "模型",
  tool: "工具",
  knowledge: "知识",
  event: "事件",
};

const STATUS_LABELS: Record<string, string> = {
  completed: "已完成",
  failed: "失败",
  running: "进行中",
  cancelled: "已取消",
  verified: "已核验",
  accepted: "已受理",
  partial: "部分完成",
  no_statement: "无对应声明",
  skipped: "已跳过",
};

function statusLabel(status: string): string {
  return STATUS_LABELS[status] ?? status;
}

function statusTone(status: string): "good" | "bad" | "progress" | "neutral" {
  if (status === "failed" || status === "cancelled") return "bad";
  if (status === "running") return "progress";
  if (status === "completed" || status === "verified" || status === "accepted") return "good";
  return "neutral";
}

function sectionText(section: TrajectorySection) {
  if (section.code) {
    return <pre className="trajectory-code">{section.text}</pre>;
  }
  return <p className="trajectory-text">{section.text}</p>;
}

function EntryBody({ entry }: { entry: TrajectoryEntry }) {
  return (
    <div className="trajectory-body">
      {entry.fields.length > 0 ? (
        <dl className="trajectory-fields">
          {entry.fields.map((field, index) => (
            <div key={`${field.label}:${index}`}>
              <dt>{field.label}</dt>
              <dd>{field.value}</dd>
            </div>
          ))}
        </dl>
      ) : null}
      {entry.sections.map((section, index) => (
        <section key={`${section.label}:${index}`} className="trajectory-section">
          <h4>{section.label}</h4>
          {sectionText(section)}
        </section>
      ))}
      {entry.fields.length === 0 && entry.sections.length === 0 ? (
        <p className="trajectory-text">这条记录没有更多可展开的内容。</p>
      ) : null}
    </div>
  );
}

function Entry({
  entry,
  index,
  open,
  onToggle,
}: {
  entry: TrajectoryEntry;
  index: number;
  open: boolean;
  onToggle: () => void;
}) {
  const panelId = `trajectory-panel-${index}`;
  return (
    <li className="trajectory-entry" data-kind={entry.kind}>
      <button
        type="button"
        className="trajectory-entry-head"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={onToggle}
      >
        <span className="trajectory-entry-marker" aria-hidden="true" />
        <span className="trajectory-entry-main">
          <span className="trajectory-entry-title">
            <span className="trajectory-kind">{KIND_LABELS[entry.kind]}</span>
            {entry.title}
          </span>
          <span className="trajectory-entry-meta">
            {instant(entry.at)}
            {entry.durationMs !== null ? ` · ${elapsed(entry.durationMs)}` : ""}
            {entry.meta.length > 0 ? ` · ${entry.meta.join(" · ")}` : ""}
          </span>
        </span>
        <StatusText tone={statusTone(entry.status)}>
          {statusLabel(entry.status)}
        </StatusText>
        <span className="trajectory-disclosure" aria-hidden="true">
          {open ? "收起" : "展开"}
        </span>
      </button>
      <div id={panelId} className="trajectory-body-wrap" hidden={!open}>
        <EntryBody entry={entry} />
      </div>
    </li>
  );
}

export function TrajectoryView({ execution }: { execution: Execution }) {
  const trajectory = useMemo(() => buildTrajectory(execution), [execution]);
  const [query, setQuery] = useState("");
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const needle = query.trim().toLocaleLowerCase("zh-CN");
  const entries = needle === ""
    ? trajectory.entries
    : trajectory.entries.filter((entry) => entry.search.includes(needle));
  const toggle = (id: string) => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
  const expandAll = () => setExpanded(new Set(entries.map((entry) => entry.id)));
  const collapseAll = () => setExpanded(new Set());
  return (
    <section
      id="review-trajectory"
      className="trajectory-view"
      aria-labelledby="review-trajectory-title"
    >
      <header className="trajectory-head">
        <div>
          <h2 id="review-trajectory-title">运行轨迹</h2>
          <p className="meta">
            按时间列出本次执行实际发生的阶段、模型调用、工具调用和知识读取。
            模型输出和工具返回都是待核验的记录，不会成为已确认结论。
          </p>
        </div>
        <StatusText tone="neutral">
          {trajectory.entries.length} 条记录
        </StatusText>
      </header>

      <dl className="trajectory-summary" aria-label="轨迹汇总">
        <div>
          <dt>总耗时</dt>
          <dd>{elapsed(trajectory.durationMs)}</dd>
        </div>
        <div>
          <dt>模型调用</dt>
          <dd>{trajectory.modelCalls}</dd>
        </div>
        <div>
          <dt>工具调用</dt>
          <dd>{trajectory.toolCalls}</dd>
        </div>
        <div>
          <dt>知识读取</dt>
          <dd>{trajectory.knowledgeReads}</dd>
        </div>
        <div>
          <dt>输入 Token</dt>
          <dd>{tokenCount(trajectory.inputTokens)}</dd>
        </div>
        <div>
          <dt>输出 Token</dt>
          <dd>{tokenCount(trajectory.outputTokens)}</dd>
        </div>
      </dl>

      <div className="trajectory-controls">
        <Input.Search
          allowClear
          aria-label="搜索轨迹"
          placeholder="搜索工具、模型、知识、摘要或标识"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <Button type="text" onClick={expandAll}>
          展开全部
        </Button>
        <Button type="text" onClick={collapseAll}>
          收起全部
        </Button>
      </div>

      {needle !== "" ? (
        <p className="trajectory-filter" role="status">
          已按“{query.trim()}”筛选出 {entries.length} 条记录。
        </p>
      ) : null}

      {entries.length === 0 ? (
        <p className="trajectory-empty">
          {trajectory.entries.length === 0
            ? "本次执行没有保存可展示的轨迹记录。"
            : "没有匹配的轨迹记录。"}
        </p>
      ) : (
        <ol className="trajectory-list">
          {entries.map((entry, index) => (
            <Entry
              key={entry.id}
              entry={entry}
              index={index}
              open={expanded.has(entry.id)}
              onToggle={() => toggle(entry.id)}
            />
          ))}
        </ol>
      )}
    </section>
  );
}
