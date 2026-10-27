import { Collapse } from "antd";
import { Button, Select } from "antd";
import { useRef, useState } from "react";
import type {
  ConstraintEvaluation,
  Execution,
  ExecutionCallUsage,
  ExecutionEvent,
  ExecutionUsage,
  Finding,
} from "../api/types";
import {
  isActiveAttemptState,
  relationLabel,
  runLabel,
  runTone,
} from "../domain/review";
import { StatusText } from "./status-text";
import { AgentReviewInspector } from "./agent-review-inspector";

interface ExecutionInspectorProps {
  execution: Execution;
  caseId: string;
  findings: Finding[];
  evaluations: ConstraintEvaluation[];
  selectedFindingId: string | null;
  onSelectFinding: (findingId: string) => void;
  onSelectAttempt: (attemptId: string) => void;
  canCancel: boolean;
  cancelling: boolean;
  cancelError: string | null;
  onCancel: () => void;
}

interface SpanGroup {
  key: string;
  spanId: string | null;
  parentSpanId: string | null;
  events: ExecutionEvent[];
  children: SpanGroup[];
}

function elapsed(value: number | null): string {
  if (value === null) return "未报告";
  if (value < 1_000) return `${value} 毫秒`;
  return `${(value / 1_000).toFixed(2)} 秒`;
}

function instant(value: string | null): string {
  if (value === null) return "未记录";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
  }).format(date);
}

function tokenCount(value: number | null): string {
  return value === null ? "未知" : value.toLocaleString("zh-CN");
}

const phaseLabels: Record<string, string> = {
  acceptance: "受理",
  audit_review_execution_v1: "审查执行",
  input_preparation: "输入准备",
  deterministic_rules: "确定性规则审查",
  model_review: "模型审查",
  model_request: "模型请求",
  citation_verification: "引用核验",
  verified_finding: "引用核验",
  terminal_persistence: "终态保存",
  cancellation: "取消尝试",
  restart_recovery: "重启恢复",
  evidence_tool: "证据工具",
  agent_review_result: "智能体审查结果",
};

const eventTypeLabels: Record<string, string> = {
  attempt_accepted: "尝试已受理",
  span_started: "开始",
  span_completed: "完成",
  span_failed: "失败",
  node_started: "节点开始",
  node_completed: "节点完成",
  node_failed: "节点失败",
  model_completed: "模型调用完成",
  model_failed: "模型调用失败",
  finding_verified: "引用核验记录",
  attempt_finished: "尝试结束",
  attempt_cancelled: "尝试已取消",
  attempt_interrupted: "尝试已中断",
  tool_evidence: "证据工具结果",
  agent_review_event: "智能体审查事件",
};

const eventStatusLabels: Record<string, string> = {
  accepted: "已受理",
  queued: "排队中",
  started: "已开始",
  running: "进行中",
  completed: "已完成",
  failed: "失败",
  partial: "部分完成",
  cancelled: "已取消",
  interrupted: "已中断",
};

function phaseLabel(phase: string): string {
  return Object.hasOwn(phaseLabels, phase)
    ? (phaseLabels[phase] ?? phase)
    : phase;
}

function eventTypeLabel(type: string): string {
  return Object.hasOwn(eventTypeLabels, type)
    ? (eventTypeLabels[type] ?? type)
    : type;
}

function eventStatusLabel(status: string): string {
  return Object.hasOwn(eventStatusLabels, status)
    ? (eventStatusLabels[status] ?? status)
    : status;
}

function usageDetails(usage: ExecutionUsage | null) {
  if (usage === null) return <p>未报告用量。</p>;
  return (
    <dl className="execution-dl execution-usage-details">
      <div>
        <dt>输入 Token</dt>
        <dd>{tokenCount(usage.input_tokens)}</dd>
      </div>
      <div>
        <dt>输出 Token</dt>
        <dd>{tokenCount(usage.output_tokens)}</dd>
      </div>
      <div>
        <dt>总 Token</dt>
        <dd>{tokenCount(usage.total_tokens)}</dd>
      </div>
      <div>
        <dt>物理调用</dt>
        <dd>{usage.calls}</dd>
      </div>
      <div>
        <dt>用量未知的调用</dt>
        <dd>{usage.unknown_calls}</dd>
      </div>
      <div>
        <dt>来源</dt>
        <dd>
          {usage.sources.length === 0 ? "未报告" : usage.sources.join("、")}
        </dd>
      </div>
      <div>
        <dt>已知小计 Token</dt>
        <dd>
          输入 {tokenCount(usage.known_subtotals.input_tokens)}，输出{" "}
          {tokenCount(usage.known_subtotals.output_tokens)}，合计{" "}
          {tokenCount(usage.known_subtotals.total_tokens)}
        </dd>
      </div>
      <div>
        <dt>成本</dt>
        <dd>未计算（没有版本化价格表）</dd>
      </div>
    </dl>
  );
}
function callUsageDetails(usage: ExecutionCallUsage | null) {
  if (usage === null) return <p>未报告调用用量。</p>;
  return (
    <dl className="execution-dl execution-usage-details">
      <div>
        <dt>来源</dt>
        <dd>{usage.source}</dd>
      </div>
      <div>
        <dt>输入 Token</dt>
        <dd>{tokenCount(usage.input_tokens)}</dd>
      </div>
      <div>
        <dt>输出 Token</dt>
        <dd>{tokenCount(usage.output_tokens)}</dd>
      </div>
      <div>
        <dt>总 Token</dt>
        <dd>{tokenCount(usage.total_tokens)}</dd>
      </div>
    </dl>
  );
}

function buildSpanTree(events: ExecutionEvent[]): SpanGroup[] {
  const nodes: SpanGroup[] = [];
  const bySpanId = new Map<string, SpanGroup>();
  for (const event of events) {
    if (event.span_id === null) {
      nodes.push({
        key: `event-${event.seq}`,
        spanId: null,
        parentSpanId: null,
        events: [event],
        children: [],
      });
      continue;
    }
    let node = bySpanId.get(event.span_id);
    if (!node) {
      node = {
        key: event.span_id,
        spanId: event.span_id,
        parentSpanId: event.parent_span_id,
        events: [],
        children: [],
      };
      bySpanId.set(event.span_id, node);
      nodes.push(node);
    }
    node.events.push(event);
    if (node.parentSpanId === null && event.parent_span_id !== null)
      node.parentSpanId = event.parent_span_id;
  }

  const roots: SpanGroup[] = [];
  for (const node of nodes) {
    const parent =
      node.parentSpanId === null ? undefined : bySpanId.get(node.parentSpanId);
    if (parent && parent !== node) parent.children.push(node);
    else roots.push(node);
  }
  const order = (left: SpanGroup, right: SpanGroup) =>
    (left.events[0]?.seq ?? 0) - (right.events[0]?.seq ?? 0);
  for (const node of nodes) {
    node.events.sort((left, right) => left.seq - right.seq);
    node.children.sort(order);
  }
  return roots.sort(order);
}

export function ExecutionInspector({
  execution,
  caseId,
  findings,
  evaluations,
  selectedFindingId,
  onSelectFinding,
  onSelectAttempt,
  canCancel,
  cancelling,
  cancelError,
  onCancel,
}: ExecutionInspectorProps) {
  const [view, setView] = useState<"tree" | "timeline">("timeline");
  const [selectedSeq, setSelectedSeq] = useState<number | null>(null);
  const eventDetailRef = useRef<HTMLElement>(null);

  const selected = execution.attempt;
  const attempts = execution.attempts.some(
    (attempt) => attempt.id === selected.id,
  )
    ? execution.attempts
    : [selected, ...execution.attempts];
  const latest = attempts.reduce(
    (current, attempt) =>
      attempt.ordinal > current.ordinal ? attempt : current,
    selected,
  );
  const events = execution.events
    .filter((event) => event.attempt_id === selected.id)
    .slice()
    .sort((left, right) => left.seq - right.seq);
  const event =
    events.find((item) => item.seq === selectedSeq) ?? events[0] ?? null;
  const locatedFindings = findings.filter(
    (finding) => finding.evidence.length > 0 && finding.evidence.every((anchor) => anchor.status === "located"),
  );
  const pendingCitations = findings.filter((finding) => !locatedFindings.includes(finding));
  const gaps = evaluations.filter(
    (evaluation) => evaluation.relation === "insufficient",
  );
  const trees = buildSpanTree(events);
  const duration =
    execution.duration_ms === null && isActiveAttemptState(selected.state)
      ? "运行中，尚无终态时长"
      : elapsed(execution.duration_ms);

  const latestEvent = events.at(-1);

  function eventButton(item: ExecutionEvent) {
    const typeLabel = eventTypeLabel(item.event_type);
    const statusLabel = item.status ? eventStatusLabel(item.status) : null;
    const label = `序号 ${item.seq}（#${item.seq}），${phaseLabel(item.phase)}，${typeLabel}${
      statusLabel ? `，${statusLabel}` : ""
    }`;
    return (
      <button
        aria-label={label}
        className="execution-event-select"
        type="button"
        data-status={item.status ?? undefined}
        aria-current={event?.seq === item.seq ? "true" : undefined}
        onClick={() => {
          setSelectedSeq(item.seq);
          if (window.matchMedia?.("(max-width: 1099px)").matches) {
            eventDetailRef.current?.scrollIntoView({ block: "start" });
          }
        }}
      >
        <span className="execution-event-phase">
          #{item.seq} {phaseLabel(item.phase)}
        </span>
        <span className="execution-event-description">
          {typeLabel}
          {statusLabel ? `，${statusLabel}` : ""}
        </span>
      </button>
    );
  }

  function renderTree(node: SpanGroup) {
    const titleEvent = node.events[0];
    const title = titleEvent
      ? `${phaseLabel(titleEvent.phase)}，${titleEvent.node_kind}`
      : "执行节点";
    return (
      <li key={node.key}>
        <Collapse
          ghost
          defaultActiveKey={["content"]}
          items={[
            {
              key: "content",
              label: (
                <>
                  {title}
                  {node.spanId ? (
                    <span className="meta">，span {node.spanId}</span>
                  ) : null}
                </>
              ),
              children: (
                <>
                  <ul className="execution-tree-events">
                    {node.events.map((item) => (
                      <li key={item.seq}>{eventButton(item)}</li>
                    ))}
                  </ul>
                  {node.children.length > 0 ? (
                    <ul>{node.children.map(renderTree)}</ul>
                  ) : null}
                </>
              ),
            },
          ]}
        />
      </li>
    );
  }

  return (
    <section
      id="execution-inspector"
      className="execution-inspector"
      aria-labelledby="execution-inspector-title"
    >
      <div className="execution-inspector-top">
        <Collapse
          className="execution-inspector-disclosure"
          ghost
          defaultActiveKey={["content"]}
          items={[
            {
              key: "content",
              label: (
                <div className="execution-inspector-summary">
                  <div>
                    <h2 id="execution-inspector-title">执行检查器</h2>
                    <p className="meta">
                      第 {selected.ordinal} 次尝试，最近持久阶段{" "}
                      {latestEvent ? phaseLabel(latestEvent.phase) : "未报告"}，
                      首个引用已定位的线索 {locatedFindings[0]?.claim || "未报告"}
                    </p>
                  </div>
                  <StatusText tone={runTone(selected.state)}>
                    {runLabel(selected.state)}
                  </StatusText>
                </div>
              ),
              children: (
                <>
                  <div className="execution-inspector-body">
                    <div className="execution-overview">
                      <div
                        className="execution-attempt-row"
                        data-attempt-id={selected.id}
                        data-attempt-ordinal={selected.ordinal}
                        data-attempt-count={attempts.length}
                        data-parsed-document-ids={JSON.stringify(
                          selected.parsed_document_ids,
                        )}
                      >
                        <label className="field">
                          查看尝试
                          <Select
                            aria-label="查看尝试"
                            value={selected.id}
                            onChange={(value) => {
                              setSelectedSeq(null);
                              onSelectAttempt(value);
                            }}
                            options={[...attempts]
                              .sort(
                                (left, right) => right.ordinal - left.ordinal,
                              )
                              .map((attempt) => ({
                                label: `第 ${attempt.ordinal} 次，${runLabel(attempt.state)}${attempt.id === latest.id ? "，最新" : ""}`,
                                value: attempt.id,
                              }))}
                          />
                        </label>
                        {selected.history_kind === "legacy_unfrozen" ? (
                          <p className="callout">
                            旧运行未冻结原始输入，不能补造历史阶段。
                          </p>
                        ) : null}
                      </div>
                      <dl className="execution-overview-stats">
                        <div>
                          <dt>状态</dt>
                          <dd>{runLabel(selected.state)}</dd>
                        </div>
                        <div>
                          <dt>总时长</dt>
                          <dd>{duration}</dd>
                        </div>
                        <div>
                          <dt>持久事件</dt>
                          <dd>{events.length} 条</dd>
                        </div>
                        <div>
                          <dt>模型调用</dt>
                          <dd>
                            {execution.usage === null
                              ? "未报告"
                              : `${execution.usage.calls} 次`}
                          </dd>
                        </div>
                      </dl>
                    </div>
                    <section
                      className="execution-events"
                      aria-labelledby="execution-events-title"
                    >
                      <header className="execution-events-head">
                        <div>
                          <h3 id="execution-events-title">实际执行事件</h3>
                          <p className="meta">
                            {events.length} 条事件，仅显示第 {selected.ordinal}{" "}
                            次尝试的持久记录
                          </p>
                        </div>
                        <div
                          className="execution-view-switch"
                          aria-label="执行事件视图"
                        >
                          <Button
                            aria-label={"事件树"}
                            htmlType="button"
                            aria-pressed={view === "tree"}
                            onClick={() => setView("tree")}
                          >
                            事件树
                          </Button>
                          <Button
                            aria-label={"时间线"}
                            htmlType="button"
                            aria-pressed={view === "timeline"}
                            onClick={() => setView("timeline")}
                          >
                            时间线
                          </Button>
                        </div>
                      </header>
                      {events.length === 0 ? (
                        <p>当前尝试尚无持久事件。</p>
                      ) : view === "tree" ? (
                        <ul className="execution-tree">
                          {trees.map(renderTree)}
                        </ul>
                      ) : (
                        <ol className="execution-timeline">
                          {events.map((item) => (
                            <li key={item.seq}>
                              <time dateTime={item.at ?? undefined}>
                                {instant(item.at)}
                              </time>
                              {eventButton(item)}
                              <span className="execution-event-duration">
                                {elapsed(item.duration_ms)}
                              </span>
                            </li>
                          ))}
                        </ol>
                      )}
                      <section
                        ref={eventDetailRef}
                        className="execution-event-detail"
                        aria-labelledby="execution-event-detail-title"
                      >
                        <h4 id="execution-event-detail-title">事件详情</h4>
                        {!event ? (
                          <p>没有可查看的事件详情。</p>
                        ) : (
                          <>
                            <dl className="execution-dl">
                              <div>
                                <dt>序号</dt>
                                <dd>{event.seq}</dd>
                              </div>
                              <div>
                                <dt>阶段</dt>
                                <dd>
                                  {phaseLabel(event.phase)}
                                  {phaseLabel(event.phase) !== event.phase ? (
                                    <span className="execution-event-code">
                                      {event.phase}
                                    </span>
                                  ) : null}
                                </dd>
                              </div>
                              <div>
                                <dt>节点类型</dt>
                                <dd>{event.node_kind}</dd>
                              </div>
                              <div>
                                <dt>事件类型</dt>
                                <dd>
                                  {eventTypeLabel(event.event_type)}
                                  {eventTypeLabel(event.event_type) !==
                                  event.event_type ? (
                                    <span className="execution-event-code">
                                      {event.event_type}
                                    </span>
                                  ) : null}
                                </dd>
                              </div>
                              <div>
                                <dt>时间</dt>
                                <dd>{instant(event.at)}</dd>
                              </div>
                              <div>
                                <dt>耗时</dt>
                                <dd>{elapsed(event.duration_ms)}</dd>
                              </div>
                              <div>
                                <dt>状态</dt>
                                <dd>
                                  {event.status
                                    ? eventStatusLabel(event.status)
                                    : "未报告"}
                                  {event.status &&
                                  eventStatusLabel(event.status) !== event.status ? (
                                    <span className="execution-event-code">
                                      {event.status}
                                    </span>
                                  ) : null}
                                </dd>
                              </div>
                              <div>
                                <dt>错误代码</dt>
                                <dd>{event.error_code ?? "未报告"}</dd>
                              </div>
                              <div>
                                <dt>Span</dt>
                                <dd>{event.span_id ?? "未关联"}</dd>
                              </div>
                              <div>
                                <dt>父 Span</dt>
                                <dd>
                                  {event.parent_span_id ?? "无已报告父节点"}
                                </dd>
                              </div>
                              <div>
                                <dt>模型版本</dt>
                                <dd>{event.model_ref ?? "未报告"}</dd>
                              </div>
                            </dl>
                            <h5>事件用量</h5>
                            {callUsageDetails(event.usage)}
                          </>
                        )}
                      </section>
                    </section>

                    <dl className="execution-dl execution-summary">
                      <div>
                        <dt>受理</dt>
                        <dd>{instant(selected.accepted_at)}</dd>
                      </div>
                      <div>
                        <dt>开始</dt>
                        <dd>{instant(selected.started_at)}</dd>
                      </div>
                      <div>
                        <dt>结束</dt>
                        <dd>{instant(selected.finished_at)}</dd>
                      </div>
                      <div>
                        <dt>总时长</dt>
                        <dd>{duration}</dd>
                      </div>
                      <div>
                        <dt>首次引用核验</dt>
                        <dd>{elapsed(execution.first_verified_finding_ms)}</dd>
                      </div>
                      <div>
                        <dt>排队等待</dt>
                        <dd>{elapsed(execution.queue_wait_ms)}</dd>
                      </div>
                      <div>
                        <dt>输入摘要</dt>
                        <dd>{selected.input_hash ?? "未记录"}</dd>
                      </div>
                      <div>
                        <dt>输出摘要</dt>
                        <dd>{selected.output_hash ?? "未记录"}</dd>
                      </div>
                      <div>
                        <dt>本次解析产物</dt>
                        <dd>
                          {selected.parsed_document_ids.length === 0
                            ? "未记录或尚未准备"
                            : selected.parsed_document_ids.join("、")}
                        </dd>
                      </div>
                      <div>
                        <dt>错误代码</dt>
                        <dd>{selected.error_code ?? "无已报告错误代码"}</dd>
                      </div>
                    </dl>

                    <div className="execution-results">
                      <section aria-labelledby="execution-verified-title">
                        <h3 id="execution-verified-title">引用已定位的线索</h3>
                        {locatedFindings.length === 0 ? (
                          <p>当前尝试没有全部引用已定位的线索。</p>
                        ) : (
                          <ul className="execution-finding-list">
                            {locatedFindings.map((finding) => (
                              <li key={finding.id}>
                                <Button
                                  aria-label={String(finding.claim)}
                                  htmlType="button"
                                  aria-current={
                                    selectedFindingId === finding.id
                                      ? "true"
                                      : undefined
                                  }
                                  onClick={() => onSelectFinding(finding.id)}
                                >
                                  {finding.claim}
                                </Button>
                                <span className="meta">
                                  {relationLabel(finding.relation)}，
                                  {finding.evidence.length} 条定位引用
                                </span>
                              </li>
                            ))}
                          </ul>
                        )}
                      </section>
                      <section aria-labelledby="execution-gaps-title">
                        <h3 id="execution-gaps-title">引用待定位与缺口</h3>
                        {pendingCitations.length === 0 &&
                        gaps.length === 0 ? (
                          <p>没有已报告的引用定位或规则缺口。</p>
                        ) : null}
                        {pendingCitations.length > 0 ? (
                          <ul className="execution-finding-list">
                            {pendingCitations.map((finding) => (
                              <li key={finding.id}>
                                <Button
                                  aria-label={String(finding.claim)}
                                  htmlType="button"
                                  aria-current={
                                    selectedFindingId === finding.id
                                      ? "true"
                                      : undefined
                                  }
                                  onClick={() => onSelectFinding(finding.id)}
                                >
                                  {finding.claim}
                                </Button>
                                <span className="meta">
                                  {finding.verification_status === "unverified"
                                    ? finding.unverified_reason
                                    : "引用尚未全部定位"}
                                </span>
                              </li>
                            ))}
                          </ul>
                        ) : null}
                        {gaps.length > 0 ? (
                          <ul className="execution-gap-list">
                            {gaps.map((gap) => (
                              <li key={gap.id}>
                                {gap.domain}，{gap.reason || "缺少可核验信息"}
                              </li>
                            ))}
                          </ul>
                        ) : null}
                      </section>
                    </div>
                    <AgentReviewInspector
                      agentReview={execution.agent_review}
                      caseId={caseId}
                      attemptId={selected.id}
                      executionEvents={events}
                    />

                    <div className="execution-usage-grid">
                      <section aria-labelledby="execution-attempt-usage-title">
                        <h3 id="execution-attempt-usage-title">本次尝试用量</h3>
                        {usageDetails(execution.usage)}
                      </section>
                      <section aria-labelledby="execution-all-usage-title">
                        <h3 id="execution-all-usage-title">全部尝试累计</h3>
                        {usageDetails(execution.all_attempts_usage)}
                      </section>
                    </div>
                  </div>
                </>
              ),
            },
          ]}
        />
        {canCancel ? (
          <Button
            aria-label={String(cancelling ? "正在取消" : "取消当前尝试")}
            type="text"
            htmlType="button"
            disabled={cancelling}
            onClick={onCancel}
          >
            {cancelling ? "正在取消" : "取消当前尝试"}
          </Button>
        ) : null}
      </div>
      <p
        className="sr-only"
        role="status"
        aria-live="polite"
        aria-atomic="true"
      >
        第 {selected.ordinal} 次尝试状态：{runLabel(selected.state)}。
      </p>
      {cancelError ? (
        <p className="execution-error" role="alert">
          {cancelError}
        </p>
      ) : null}
    </section>
  );
}
