import { Collapse } from "antd";
import { Button, Select } from "antd";
import { useRef, useState } from "react";
import type {
  ConstraintEvaluation,
  Execution,
  ExecutionAttempt,
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
  cancellation: "取消执行",
  restart_recovery: "重启恢复",
  evidence_tool: "证据工具",
  agent_review_result: "智能体审查结果",
};

const eventTypeLabels: Record<string, string> = {
  attempt_accepted: "执行已受理",
  span_started: "开始",
  span_completed: "完成",
  span_failed: "失败",
  node_started: "节点开始",
  node_completed: "节点完成",
  node_failed: "节点失败",
  model_completed: "模型调用完成",
  model_failed: "模型调用失败",
  finding_verified: "引用核验记录",
  attempt_finished: "执行结束",
  attempt_cancelled: "执行已取消",
  attempt_interrupted: "执行已中断",
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

/** Unknown labels pass through; hasOwn keeps prototype keys from leaking in. */
function labelFrom(labels: Record<string, string>, value: string): string {
  return Object.hasOwn(labels, value) ? (labels[value] ?? value) : value;
}

const phaseLabel = (phase: string): string => labelFrom(phaseLabels, phase);
const eventTypeLabel = (type: string): string => labelFrom(eventTypeLabels, type);
const eventStatusLabel = (status: string): string =>
  labelFrom(eventStatusLabels, status);

function UsageDetails({ usage }: { usage: ExecutionUsage | null }) {
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

function CallUsageDetails({ usage }: { usage: ExecutionCallUsage | null }) {
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

function collectSpanNodes(events: ExecutionEvent[]): {
  nodes: SpanGroup[];
  bySpanId: Map<string, SpanGroup>;
} {
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
  return { nodes, bySpanId };
}

function linkSpanRoots(
  nodes: SpanGroup[],
  bySpanId: Map<string, SpanGroup>,
): SpanGroup[] {
  const roots: SpanGroup[] = [];
  for (const node of nodes) {
    const parent =
      node.parentSpanId === null ? undefined : bySpanId.get(node.parentSpanId);
    if (parent && parent !== node) parent.children.push(node);
    else roots.push(node);
  }
  return roots;
}

function spanOrder(left: SpanGroup, right: SpanGroup): number {
  return (left.events[0]?.seq ?? 0) - (right.events[0]?.seq ?? 0);
}

function sortSpanTree(nodes: SpanGroup[], roots: SpanGroup[]): SpanGroup[] {
  for (const node of nodes) {
    node.events.sort((left, right) => left.seq - right.seq);
    node.children.sort(spanOrder);
  }
  return roots.sort(spanOrder);
}

function buildSpanTree(events: ExecutionEvent[]): SpanGroup[] {
  const { nodes, bySpanId } = collectSpanNodes(events);
  const roots = linkSpanRoots(nodes, bySpanId);
  return sortSpanTree(nodes, roots);
}

function locatedFindingsOf(findings: Finding[]): Finding[] {
  return findings.filter(
    (finding) =>
      finding.evidence.length > 0 &&
      finding.evidence.every((anchor) => anchor.status === "located"),
  );
}

function pendingFindingsOf(findings: Finding[], located: Finding[]): Finding[] {
  return findings.filter((finding) => !located.includes(finding));
}

function attemptsOf(execution: Execution): ExecutionAttempt[] {
  return execution.attempts.some(
    (attempt) => attempt.id === execution.attempt.id,
  )
    ? execution.attempts
    : [execution.attempt, ...execution.attempts];
}

function latestAttemptOf(
  attempts: ExecutionAttempt[],
  selected: ExecutionAttempt,
): ExecutionAttempt {
  return attempts.reduce(
    (current, attempt) =>
      attempt.ordinal > current.ordinal ? attempt : current,
    selected,
  );
}

function selectedEventsOf(
  execution: Execution,
  selected: ExecutionAttempt,
): ExecutionEvent[] {
  return execution.events
    .filter((event) => event.attempt_id === selected.id)
    .slice()
    .sort((left, right) => left.seq - right.seq);
}

function attemptOptions(attempts: ExecutionAttempt[], latestId: string) {
  return [...attempts]
    .sort((left, right) => right.ordinal - left.ordinal)
    .map((attempt) => ({
      label: `第 ${attempt.ordinal} 次，${runLabel(attempt.state)}${attempt.id === latestId ? "，最新" : ""}`,
      value: attempt.id,
    }));
}

function durationLabel(
  execution: Execution,
  selected: ExecutionAttempt,
): string {
  if (execution.duration_ms === null && isActiveAttemptState(selected.state)) {
    return "运行中，尚无终态时长";
  }
  return elapsed(execution.duration_ms);
}

function EventButton({
  item,
  current,
  onSelect,
}: {
  item: ExecutionEvent;
  current: boolean;
  onSelect: (seq: number) => void;
}) {
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
      aria-current={current ? "true" : undefined}
      onClick={() => onSelect(item.seq)}
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

function SpanTreeNode({
  node,
  event,
  onSelect,
}: {
  node: SpanGroup;
  event: ExecutionEvent | null;
  onSelect: (seq: number) => void;
}) {
  const titleEvent = node.events[0];
  const title = titleEvent
    ? `${phaseLabel(titleEvent.phase)}，${titleEvent.node_kind}`
    : "执行节点";
  return (
    <li>
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
                    <li key={item.seq}>
                      <EventButton
                        item={item}
                        current={event?.seq === item.seq}
                        onSelect={onSelect}
                      />
                    </li>
                  ))}
                </ul>
                {node.children.length > 0 ? (
                  <ul>
                    {node.children.map((child) => (
                      <SpanTreeNode
                        key={child.key}
                        node={child}
                        event={event}
                        onSelect={onSelect}
                      />
                    ))}
                  </ul>
                ) : null}
              </>
            ),
          },
        ]}
      />
    </li>
  );
}

function CodePair({
  label,
  value,
  code,
}: {
  label: string;
  value: string;
  code?: string | null;
}) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>
        {value}
        {code ? <span className="execution-event-code">{code}</span> : null}
      </dd>
    </div>
  );
}

function eventStatusValue(status: string | null): string {
  return status ? eventStatusLabel(status) : "未报告";
}

function eventStatusCode(status: string | null): string | null {
  return status && eventStatusLabel(status) !== status ? status : null;
}

function fallbackText(value: string | null, fallback: string): string {
  return value ?? fallback;
}

function EventDetailDl({ event }: { event: ExecutionEvent }) {
  return (
    <dl className="execution-dl">
      <div>
        <dt>序号</dt>
        <dd>{event.seq}</dd>
      </div>
      <CodePair
        label="阶段"
        value={phaseLabel(event.phase)}
        code={phaseLabel(event.phase) !== event.phase ? event.phase : null}
      />
      <div>
        <dt>节点类型</dt>
        <dd>{event.node_kind}</dd>
      </div>
      <CodePair
        label="事件类型"
        value={eventTypeLabel(event.event_type)}
        code={
          eventTypeLabel(event.event_type) !== event.event_type
            ? event.event_type
            : null
        }
      />
      <div>
        <dt>时间</dt>
        <dd>{instant(event.at)}</dd>
      </div>
      <div>
        <dt>耗时</dt>
        <dd>{elapsed(event.duration_ms)}</dd>
      </div>
      <CodePair
        label="状态"
        value={eventStatusValue(event.status)}
        code={eventStatusCode(event.status)}
      />
      <div>
        <dt>错误代码</dt>
        <dd>{fallbackText(event.error_code, "未报告")}</dd>
      </div>
      <div>
        <dt>Span</dt>
        <dd>{fallbackText(event.span_id, "未关联")}</dd>
      </div>
      <div>
        <dt>父 Span</dt>
        <dd>{fallbackText(event.parent_span_id, "无已报告父节点")}</dd>
      </div>
      <div>
        <dt>模型版本</dt>
        <dd>{fallbackText(event.model_ref, "未报告")}</dd>
      </div>
    </dl>
  );
}

function EventDetailBody({ event }: { event: ExecutionEvent | null }) {
  return (
    <>
      <h4 id="execution-event-detail-title">事件详情</h4>
      {!event ? (
        <p>没有可查看的事件详情。</p>
      ) : (
        <>
          <EventDetailDl event={event} />
          <h5>事件用量</h5>
          <CallUsageDetails usage={event.usage} />
        </>
      )}
    </>
  );
}

function EventsPanel({
  events,
  trees,
  event,
  selectedOrdinal,
  onSelectSeq,
}: {
  events: ExecutionEvent[];
  trees: SpanGroup[];
  event: ExecutionEvent | null;
  selectedOrdinal: number;
  onSelectSeq: (seq: number) => void;
}) {
  const [view, setView] = useState<"tree" | "timeline">("timeline");
  const eventDetailRef = useRef<HTMLElement>(null);

  function selectSeq(seq: number) {
    onSelectSeq(seq);
    if (window.matchMedia?.("(max-width: 1099px)").matches) {
      eventDetailRef.current?.scrollIntoView({ block: "start" });
    }
  }

  return (
    <section className="execution-events" aria-labelledby="execution-events-title">
      <header className="execution-events-head">
        <div>
          <h3 id="execution-events-title">实际执行事件</h3>
          <p className="meta">
            {events.length} 条事件，仅显示第 {selectedOrdinal} 次执行保存的记录
          </p>
        </div>
        <div className="execution-view-switch" aria-label="执行事件视图">
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
        <p>本次执行尚无已保存的事件。</p>
      ) : view === "tree" ? (
        <ul className="execution-tree">
          {trees.map((node) => (
            <SpanTreeNode
              key={node.key}
              node={node}
              event={event}
              onSelect={selectSeq}
            />
          ))}
        </ul>
      ) : (
        <ol className="execution-timeline">
          {events.map((item) => (
            <li key={item.seq}>
              <time dateTime={item.at ?? undefined}>{instant(item.at)}</time>
              <EventButton
                item={item}
                current={event?.seq === item.seq}
                onSelect={selectSeq}
              />
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
        <EventDetailBody event={event} />
      </section>
    </section>
  );
}

function AttemptStats({
  execution,
  selected,
  duration,
  events,
}: {
  execution: Execution;
  selected: ExecutionAttempt;
  duration: string;
  events: ExecutionEvent[];
}) {
  return (
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
        <dt>已保存的事件</dt>
        <dd>{events.length} 条</dd>
      </div>
      <div>
        <dt>模型调用</dt>
        <dd>
          {execution.usage === null ? "未报告" : `${execution.usage.calls} 次`}
        </dd>
      </div>
    </dl>
  );
}

function AttemptSelector({
  attempts,
  latest,
  selected,
  onSelectAttempt,
}: {
  attempts: ExecutionAttempt[];
  latest: ExecutionAttempt;
  selected: ExecutionAttempt;
  onSelectAttempt: (attemptId: string) => void;
}) {
  return (
    <div
      className="execution-attempt-row"
      data-attempt-id={selected.id}
      data-attempt-ordinal={selected.ordinal}
      data-attempt-count={attempts.length}
      data-parsed-document-ids={JSON.stringify(selected.parsed_document_ids)}
    >
      <label className="field">
        查看执行记录
        <Select
          aria-label="查看执行记录"
          value={selected.id}
          onChange={(value) => {
            onSelectAttempt(value);
          }}
          options={attemptOptions(attempts, latest.id)}
        />
      </label>
      {selected.history_kind === "legacy_unfrozen" ? (
        <p className="callout">
          较早的审查没有保存当时使用的材料版本记录，无法补回当时的执行过程。
        </p>
      ) : null}
    </div>
  );
}

function AttemptSummary({
  execution,
  selected,
  duration,
}: {
  execution: Execution;
  selected: ExecutionAttempt;
  duration: string;
}) {
  return (
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
        <dt>本次解析结果</dt>
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
  );
}

function FindingButton({
  finding,
  selectedFindingId,
  onSelectFinding,
}: {
  finding: Finding;
  selectedFindingId: string | null;
  onSelectFinding: (findingId: string) => void;
}) {
  return (
    <Button
      aria-label={String(finding.claim)}
      htmlType="button"
      aria-current={
        selectedFindingId === finding.id ? "true" : undefined
      }
      onClick={() => onSelectFinding(finding.id)}
    >
      {finding.claim}
    </Button>
  );
}

function ResultsSection({
  findings,
  evaluations,
  selectedFindingId,
  onSelectFinding,
}: {
  findings: Finding[];
  evaluations: ConstraintEvaluation[];
  selectedFindingId: string | null;
  onSelectFinding: (findingId: string) => void;
}) {
  const locatedFindings = locatedFindingsOf(findings);
  const pendingCitations = pendingFindingsOf(findings, locatedFindings);
  const gaps = evaluations.filter(
    (evaluation) => evaluation.relation === "insufficient",
  );
  return (
    <div className="execution-results">
      <section aria-labelledby="execution-verified-title">
        <h3 id="execution-verified-title">引用已定位的线索</h3>
        {locatedFindings.length === 0 ? (
          <p>当前尝试没有全部引用已定位的线索。</p>
        ) : (
          <ul className="execution-finding-list">
            {locatedFindings.map((finding) => (
              <li key={finding.id}>
                <FindingButton
                  finding={finding}
                  selectedFindingId={selectedFindingId}
                  onSelectFinding={onSelectFinding}
                />
                <span className="meta">
                  {relationLabel(finding.relation)}，{finding.evidence.length} 条定位引用
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>
      <section aria-labelledby="execution-gaps-title">
        <h3 id="execution-gaps-title">引用待定位与缺口</h3>
        {pendingCitations.length === 0 && gaps.length === 0 ? (
          <p>没有已报告的引用定位或规则缺口。</p>
        ) : null}
        {pendingCitations.length > 0 ? (
          <ul className="execution-finding-list">
            {pendingCitations.map((finding) => (
              <li key={finding.id}>
                <FindingButton
                  finding={finding}
                  selectedFindingId={selectedFindingId}
                  onSelectFinding={onSelectFinding}
                />
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
  );
}

function UsagePanel({ execution }: { execution: Execution }) {
  return (
    <div className="execution-usage-grid">
      <section aria-labelledby="execution-attempt-usage-title">
        <h3 id="execution-attempt-usage-title">本次执行用量</h3>
        <UsageDetails usage={execution.usage} />
      </section>
      <section aria-labelledby="execution-all-usage-title">
        <h3 id="execution-all-usage-title">全部尝试累计</h3>
        <UsageDetails usage={execution.all_attempts_usage} />
      </section>
    </div>
  );
}

function ExecutionHeader({
  selected,
  latestEvent,
  locatedFindings,
}: {
  selected: ExecutionAttempt;
  latestEvent: ExecutionEvent | undefined;
  locatedFindings: Finding[];
}) {
  return (
    <div className="execution-inspector-summary">
      <div>
        <h2 id="execution-inspector-title">执行检查器</h2>
        <p className="meta">
          第 {selected.ordinal} 次执行，最近保存的阶段{" "}
          {latestEvent ? phaseLabel(latestEvent.phase) : "未报告"}，
          第一条能在原文中找到引用的线索 {locatedFindings[0]?.claim || "未报告"}
        </p>
      </div>
      <StatusText tone={runTone(selected.state)}>
        {runLabel(selected.state)}
      </StatusText>
    </div>
  );
}

function ExecutionBody({
  execution,
  caseId,
  findings,
  evaluations,
  selectedFindingId,
  onSelectFinding,
  onSelectAttempt,
}: {
  execution: Execution;
  caseId: string;
  findings: Finding[];
  evaluations: ConstraintEvaluation[];
  selectedFindingId: string | null;
  onSelectFinding: (findingId: string) => void;
  onSelectAttempt: (attemptId: string) => void;
}) {
  const selected = execution.attempt;
  const attempts = attemptsOf(execution);
  const latest = latestAttemptOf(attempts, selected);
  const events = selectedEventsOf(execution, selected);
  const duration = durationLabel(execution, selected);
  const [selectedSeq, setSelectedSeq] = useState<number | null>(null);
  const event = events.find((item) => item.seq === selectedSeq) ?? events[0] ?? null;
  const trees = buildSpanTree(events);
  return (
    <div className="execution-inspector-body">
      <div className="execution-overview">
        <AttemptSelector
          attempts={attempts}
          latest={latest}
          selected={selected}
          onSelectAttempt={(value) => {
            setSelectedSeq(null);
            onSelectAttempt(value);
          }}
        />
        <AttemptStats
          execution={execution}
          selected={selected}
          duration={duration}
          events={events}
        />
      </div>
      <EventsPanel
        events={events}
        trees={trees}
        event={event}
        selectedOrdinal={selected.ordinal}
        onSelectSeq={setSelectedSeq}
      />
      <AttemptSummary
        execution={execution}
        selected={selected}
        duration={duration}
      />
      <ResultsSection
        findings={findings}
        evaluations={evaluations}
        selectedFindingId={selectedFindingId}
        onSelectFinding={onSelectFinding}
      />
      <AgentReviewInspector
        agentReview={execution.agent_review}
        caseId={caseId}
        attemptId={selected.id}
        executionEvents={events}
      />
      <UsagePanel execution={execution} />
    </div>
  );
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
  const selected = execution.attempt;
  const events = selectedEventsOf(execution, selected);
  const latestEvent = events.at(-1);
  const locatedFindings = locatedFindingsOf(findings);
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
                <ExecutionHeader
                  selected={selected}
                  latestEvent={latestEvent}
                  locatedFindings={locatedFindings}
                />
              ),
              children: (
                <ExecutionBody
                  execution={execution}
                  caseId={caseId}
                  findings={findings}
                  evaluations={evaluations}
                  selectedFindingId={selectedFindingId}
                  onSelectFinding={onSelectFinding}
                  onSelectAttempt={onSelectAttempt}
                />
              ),
            },
          ]}
        />
        {canCancel ? (
          <Button
            aria-label={String(cancelling ? "正在取消" : "取消本次执行")}
            type="text"
            htmlType="button"
            disabled={cancelling}
            onClick={onCancel}
          >
            {cancelling ? "正在取消" : "取消本次执行"}
          </Button>
        ) : null}
      </div>
      <p className="sr-only" role="status" aria-live="polite" aria-atomic="true">
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
