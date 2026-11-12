// File metadata (manifest mirror):
// Purpose: Renders the read-only Agent Studio for one fixed review attempt: capability relationships, a saved-event swimlane with node details, and a searchable event ledger, all projected from existing review and diagnostics APIs.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-06
// Updated: 2026-11-09
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import {
  ApiOutlined,
  BulbOutlined,
  FileSearchOutlined,
  ReadOutlined,
  RobotOutlined,
  ToolOutlined,
} from "@ant-design/icons";
import { Button, Input, Segmented } from "antd";
import { useMemo, useState, type ReactNode } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import type { BusinessAgentReview } from "../api/business-agent-review";
import { errorMessage, isForbidden, isUnauthenticated } from "../api/http";
import { useCaseDetail, useReview, useReviewDiagnostics } from "../api/queries";
import type {
  AgentReview,
  Execution,
  ExecutionEvent,
  ReviewWorkspace,
  RunSnapshot,
} from "../api/types";
import { PageStatus } from "../components/page-status";
import { ExecutionDiagram } from "../components/execution-diagram";
import { buildExecutionDiagram, isEinoToolEvent } from "../domain/execution-diagram";
import { StatusText } from "../components/status-text";
import {
  TechnicalDetail,
  TechnicalFieldGroup,
  type TechnicalField,
} from "../components/technical-detail";
import {
  elapsed,
  eventStatusLabel,
  eventTypeLabel,
  instant,
  phaseLabel,
  tokenCount,
} from "../domain/execution-labels";
import { runLabel, runTone, type StatusTone } from "../domain/review";
import { nodeKindLabel } from "../domain/swimlane";
import "./agent-studio-page.css";

export type StudioView = "capabilities" | "execution" | "events";

export type StudioObservationState = "observed" | "not_observed" | "unavailable";

export type StudioCapabilityKey = "model" | "tools" | "rules" | "evidence" | "experience";

const MODE_LABELS: Record<string, string> = {
  fixed_chain: "固定链",
  deterministic_planner: "确定性规划",
  model_fixture: "模型规划（fixture）",
  model_live: "模型规划（live）",
};

const AGENT_STATUS_LABELS: Record<string, string> = {
  queued: "排队中",
  planning: "规划中",
  running: "执行中",
  completed: "已完成",
  partial: "部分完成",
  blocked_live: "受阻（live）",
  cancelled: "已取消",
  failed: "失败",
  rejected: "已拒绝",
};

const CALL_STATUS_LABELS: Record<string, string> = {
  planned: "已计划",
  running: "执行中",
  completed: "已完成",
  failed: "失败",
  denied: "已拒绝",
  cancelled: "已取消",
  insufficient: "证据不足",
  unsupported: "不支持",
};

const HISTORY_KIND_LABELS: Record<string, string> = {
  frozen: "冻结记录",
  legacy_unfrozen: "历史未冻结",
};

const EVIDENCE_VERIFICATION_LABELS: Record<string, string> = {
  verified: "已核验",
  typed_semantic_and_frozen_quote_verified: "结构化语义与冻结引用已核验",
  unknown: "核验状态未知",
  rejected: "已拒绝",
};

const STATE_LABELS: Record<StudioObservationState, string> = {
  observed: "已观测",
  not_observed: "未观测",
  unavailable: "不可用",
};

const EVENT_KIND_LABELS: Record<StudioEventKind, string> = {
  model: "模型",
  tool: "工具",
  knowledge: "知识",
  stage: "阶段",
  event: "事件",
};

const GROUP_ICONS: Record<StudioCapabilityKey, ReactNode> = {
  model: <ApiOutlined />,
  tools: <ToolOutlined />,
  rules: <ReadOutlined />,
  evidence: <FileSearchOutlined />,
  experience: <BulbOutlined />,
};

export type StudioEventKind = "model" | "tool" | "knowledge" | "stage" | "event";

export interface StudioLane {
  id: string;
  label: string;
  events: ExecutionEvent[];
}

export interface StudioCapabilityFact {
  key: string;
  label: string;
  state: StudioObservationState;
  value: string;
  technical?: TechnicalField[];
}

export interface StudioCapabilityGroup {
  key: StudioCapabilityKey;
  title: string;
  description: string;
  state: StudioObservationState;
  facts: StudioCapabilityFact[];
}

export interface StudioFindingSource {
  id: string;
  claim: string;
  origin: string;
  anchorCount: number;
}

export interface StudioCapabilityInput {
  execution: Execution | null;
  agentReview: AgentReview | null;
  businessReview: BusinessAgentReview | null;
  snapshot: RunSnapshot | null;
  documentCount: number | null;
  diagnosticsLoaded: boolean;
  findings: StudioFindingSource[];
}

/** The backend mode is preserved as reported; fixture is never relabelled live. */
export function plannerModeOf(
  workspace: ReviewWorkspace | undefined,
  execution: Execution | null,
  snapshot?: RunSnapshot | null,
): string | null {
  if (execution?.agent_review?.mode) return execution.agent_review.mode;
  const businessReview = workspace?.agent_review as
    (BusinessAgentReview & { mode?: unknown }) | null | undefined;
  const businessMode = businessReview?.mode;
  if (typeof businessMode === "string" && businessMode !== "") return businessMode;
  const frozen = snapshot?.planner?.mode;
  return typeof frozen === "string" && frozen !== "" ? frozen : null;
}

export function planRoundsText(
  agentReview: AgentReview | null,
  plannerMode: string | null,
): string {
  if (agentReview) return `${agentReview.plan.rounds.length} 轮`;
  if (plannerMode) return "未执行";
  return "未观测";
}

export function agentCardSubtitle(
  agentReview: AgentReview | null,
  attemptState: string | null,
  plannerMode: string | null,
): string {
  if (agentReview) return `智能体审查 · ${agentStatusLabel(agentReview.status)}`;
  if (attemptState === "completed" && plannerMode) return "规则与模型审查已完成";
  return "智能体审查尚未写入";
}

export function plannerModeLabel(mode: string | null): string {
  if (mode === null) return "未知";
  return Object.hasOwn(MODE_LABELS, mode) ? MODE_LABELS[mode]! : mode;
}

export function agentStatusLabel(status: string | null): string {
  if (status === null || status === "") return "未观测";
  return Object.hasOwn(AGENT_STATUS_LABELS, status) ? AGENT_STATUS_LABELS[status]! : status;
}

export function historyKindLabel(value: string): string {
  return Object.hasOwn(HISTORY_KIND_LABELS, value) ? HISTORY_KIND_LABELS[value]! : value;
}

export function studioEventKind(event: ExecutionEvent): StudioEventKind {
  switch (event.node_kind) {
    case "model_call":
      return "model";
    case "tool_call":
      return "tool";
    case "agent_review":
      return event.event_type === "tool_evidence" ? "tool" : "knowledge";
    default:
      return event.event_type.startsWith("span_") || event.event_type.startsWith("node_")
        ? "stage"
        : "event";
  }
}

export function studioEventTitle(event: ExecutionEvent): string {
  const label = eventTypeLabel(event.event_type);
  return label === event.event_type ? nodeKindLabel(event.node_kind) : label;
}

export function studioEventTone(status: string | null): StatusTone {
  if (status === "failed" || status === "cancelled" || status === "interrupted") {
    return "bad";
  }
  if (status === "running" || status === "started" || status === "queued") {
    return "progress";
  }
  if (status === "completed" || status === "partial" || status === "accepted") {
    return "good";
  }
  return "neutral";
}

export function distinctEventCalls(events: ExecutionEvent[]): number {
  return new Set(events.map((event) => event.span_id ?? `seq-${event.seq}`)).size;
}

/** Group saved execution events by their actual phase; labels reuse the shared phase dictionary. */
export function buildStudioLanes(events: ExecutionEvent[]): StudioLane[] {
  const byLane = new Map<string, ExecutionEvent[]>();
  for (const event of [...events].sort((left, right) => left.seq - right.seq)) {
    const laneId = event.phase.trim() === "" ? "unknown" : event.phase.trim();
    const laneEvents = byLane.get(laneId);
    if (laneEvents) laneEvents.push(event);
    else byLane.set(laneId, [event]);
  }
  return [...byLane.entries()].map(([id, laneEvents]) => ({
    id,
    label: id === "unknown" ? "未知阶段" : phaseLabel(id),
    events: laneEvents,
  }));
}

export function filterStudioEvents(events: ExecutionEvent[], query: string): ExecutionEvent[] {
  const needle = query.trim().toLocaleLowerCase("zh-CN");
  if (needle === "") return events;
  return events.filter((event) => {
    const status = event.status ?? "";
    return [
      String(event.seq),
      event.phase,
      phaseLabel(event.phase),
      event.event_type,
      eventTypeLabel(event.event_type),
      event.node_kind,
      nodeKindLabel(event.node_kind),
      status,
      eventStatusLabel(status),
      event.model_ref ?? "",
      event.error_code ?? "",
      event.span_id ?? "",
      event.parent_span_id ?? "",
    ]
      .join("\n")
      .toLocaleLowerCase("zh-CN")
      .includes(needle);
  });
}

function fact(
  key: string,
  label: string,
  state: StudioObservationState,
  value: string,
  technical?: TechnicalField[],
): StudioCapabilityFact {
  return technical && technical.length > 0
    ? { key, label, state, value, technical }
    : { key, label, state, value };
}

function groupState(facts: StudioCapabilityFact[]): StudioObservationState {
  if (facts.some((item) => item.state === "observed")) return "observed";
  if (facts.some((item) => item.state === "unavailable")) return "unavailable";
  return "not_observed";
}

function capabilityGroup(
  key: StudioCapabilityKey,
  title: string,
  description: string,
  facts: StudioCapabilityFact[],
): StudioCapabilityGroup {
  return { key, title, description, state: groupState(facts), facts };
}

export function buildCapabilityGroups(input: StudioCapabilityInput): StudioCapabilityGroup[] {
  const {
    execution,
    agentReview,
    businessReview,
    snapshot,
    documentCount,
    diagnosticsLoaded,
    findings,
  } = input;
  const events = execution?.events ?? [];
  const modelEvents = events.filter((event) => event.node_kind === "model_call");
  const toolEvents = events.filter((event) => event.node_kind === "tool_call");
  const modelCalls = distinctEventCalls(modelEvents);
  const toolCalls = distinctEventCalls(toolEvents);
  const executionState: StudioObservationState = execution
    ? "observed"
    : diagnosticsLoaded
      ? "not_observed"
      : "unavailable";
  const agentState: StudioObservationState = agentReview
    ? "observed"
    : diagnosticsLoaded
      ? "not_observed"
      : "unavailable";
  /** A missing optional record inside an observed agent review is not "observed". */
  const optionalAgentState = (present: boolean): StudioObservationState =>
    present ? "observed" : agentReview === null ? agentState : "not_observed";
  const usage = execution?.usage ?? null;
  const frozenPlanner = snapshot?.planner ?? null;
  const plannerMode = agentReview?.mode || frozenPlanner?.mode || null;
  const modelConfigurationId =
    agentReview?.planner.model_configuration_id ?? frozenPlanner?.model_configuration_id ?? null;
  const plannerIdentityKnown = agentReview !== null || frozenPlanner !== null;
  const plannerOmitsModel =
    plannerMode === "deterministic_planner" || plannerMode === "fixed_chain";
  const plannedCalls = agentReview?.plan.calls ?? null;
  const observations = agentReview?.observations ?? null;
  const frozenKnowledge = snapshot?.governed_knowledge ?? null;
  const frozenRulepack = frozenKnowledge?.rulepack ?? null;
  const rulepack =
    agentReview?.rulepack ??
    businessReview?.rulepack ??
    (frozenRulepack
      ? {
          id: frozenRulepack.id,
          version: frozenRulepack.version,
          content_sha256: frozenRulepack.content_sha256,
        }
      : null);
  const rulepackContentHash: string | null =
    rulepack !== null && "content_sha256" in rulepack && typeof rulepack.content_sha256 === "string"
      ? rulepack.content_sha256
      : null;
  const knowledgeReads = agentReview?.knowledge_reads ?? null;
  const knowledgeSelection = agentReview?.knowledge_selection ?? null;
  const frozenSelections = frozenKnowledge?.selections ?? [];
  const frozenSelectionKnown = Boolean(
    frozenKnowledge && (frozenKnowledge.snapshot_sha256 !== "" || frozenSelections.length > 0),
  );
  const planRounds = agentReview?.plan.rounds ?? null;
  const sourceSnapshot = agentReview?.source_snapshot ?? null;
  const evidence = agentReview?.evidence ?? null;
  const parsedIDs = snapshot?.parsed_document_ids ?? execution?.attempt.parsed_document_ids ?? [];
  const hasParseIdentity =
    sourceSnapshot !== null ||
    parsedIDs.length > 0 ||
    Boolean(snapshot?.parse_selection_hash) ||
    Boolean(snapshot?.parser_configuration_id);
  const anchoredFindings = findings.filter((item) => item.anchorCount > 0);
  const missingState = (unavailableText: string, absentText: string): string =>
    agentState === "unavailable" ? unavailableText : absentText;

  const modelFacts = [
    fact(
      "model-events",
      "模型调用事件",
      execution === null ? executionState : modelCalls > 0 ? "observed" : "not_observed",
      execution === null
        ? "执行事件不可用"
        : modelCalls > 0
          ? `${modelCalls} 次真实调用`
          : "未观测到模型调用事件",
      modelEvents.length > 0
        ? modelEvents.map((event) => ({
            label: `事件 #${event.seq}`,
            value: `span ${event.span_id ?? "未记录"}`,
          }))
        : undefined,
    ),
    fact(
      "model-usage",
      "模型用量汇总",
      usage === null
        ? executionState === "unavailable"
          ? "unavailable"
          : "not_observed"
        : "observed",
      usage === null
        ? "用量汇总未报告"
        : `${usage.calls} 次已报告调用${usage.unknown_calls > 0 ? `，${usage.unknown_calls} 次用量未知` : ""}`,
      usage === null
        ? undefined
        : [
            { label: "输入 Token", value: tokenCount(usage.input_tokens) },
            { label: "输出 Token", value: tokenCount(usage.output_tokens) },
            { label: "合计 Token", value: tokenCount(usage.total_tokens) },
            { label: "用量来源", value: usage.sources.join("、") || "未记录" },
          ],
    ),
    fact(
      "planner-model",
      "规划模型配置",
      plannerIdentityKnown
        ? modelConfigurationId !== null || plannerOmitsModel
          ? "observed"
          : "not_observed"
        : agentState,
      !plannerIdentityKnown
        ? missingState("规划模型配置不可用", "没有规划模型配置")
        : modelConfigurationId !== null
          ? "已记录规划模型"
          : plannerOmitsModel
            ? "已记录规划配置，没有规划模型"
            : "没有规划模型配置",
      plannerIdentityKnown
        ? [
            { label: "规划模式", value: plannerModeLabel(plannerMode) },
            {
              label: "规划配置标识",
              value: agentReview?.planner.configuration_id ?? frozenPlanner?.configuration_id,
            },
            { label: "模型配置标识", value: modelConfigurationId ?? "无" },
            {
              label: "Prompt 版本",
              value: agentReview?.planner.prompt_version ?? frozenPlanner?.prompt_version,
            },
            {
              label: "运行时版本",
              value: agentReview?.planner.runtime_version ?? frozenPlanner?.runtime_version,
            },
            frozenPlanner
              ? { label: "配置上限", value: `${frozenPlanner.max_rounds} 轮` }
              : { label: "规划器标识", value: agentReview?.planner.identity },
          ]
        : undefined,
    ),
  ];

  const toolFacts = [
    fact(
      "planned-calls",
      "计划调用",
      optionalAgentState(plannedCalls !== null),
      plannedCalls === null
        ? missingState("计划调用不可用", "没有计划调用")
        : `${plannedCalls.length} 个计划调用`,
      plannedCalls?.map((call) => ({
        label: `调用 ${call.call_id}`,
        value: `${call.tool_id} · ${call.status}`,
      })),
    ),
    fact(
      "tool-observations",
      "工具观测",
      optionalAgentState(observations !== null),
      observations === null
        ? missingState("工具观测不可用", "没有工具观测")
        : `${observations.length} 条工具观测`,
    ),
    fact(
      "tool-events",
      "执行工具事件",
      execution === null ? executionState : toolCalls > 0 ? "observed" : "not_observed",
      execution === null
        ? "执行事件不可用"
        : toolCalls > 0
          ? `${toolCalls} 次真实调用`
          : "没有工具调用事件",
    ),
    fact("mcp-connections", "MCP 连接", "not_observed", "没有 MCP 连接"),
  ];

  const ruleFacts = [
    fact(
      "rulepack",
      "规则包",
      rulepack !== null ? "observed" : agentReview === null ? agentState : "not_observed",
      rulepack === null
        ? missingState("规则包不可用", "没有规则包身份")
        : `规则包已记录（版本 ${rulepack.version}）`,
      rulepack === null
        ? undefined
        : [
            { label: "规则包标识", value: rulepack.id },
            { label: "规则包版本", value: rulepack.version },
            {
              label: "内容摘要",
              value: rulepackContentHash ?? undefined,
            },
          ],
    ),
    fact(
      "knowledge-reads",
      "知识读取",
      optionalAgentState(knowledgeReads !== null),
      knowledgeReads === null
        ? missingState("知识读取不可用", "没有知识读取记录")
        : `${knowledgeReads.length} 次知识读取`,
    ),
    fact(
      "knowledge-selection",
      "知识选择",
      knowledgeSelection !== null || frozenSelectionKnown ? "observed" : agentState,
      knowledgeSelection !== null
        ? `知识选择已记录（${knowledgeSelection.policy_status}）`
        : frozenSelectionKnown
          ? frozenSelections.length > 0
            ? `已冻结 ${frozenSelections.length} 条知识选择`
            : "已冻结知识快照"
          : missingState("知识选择不可用", "没有知识选择记录"),
      knowledgeSelection !== null
        ? [
            {
              label: "快照摘要",
              value: knowledgeSelection.snapshot_sha256,
            },
            {
              label: "选择算法",
              value: knowledgeSelection.algorithm,
            },
          ]
        : frozenSelectionKnown
          ? [
              { label: "快照摘要", value: frozenKnowledge?.snapshot_sha256 },
              ...frozenSelections.map((item) => ({
                label: item.knowledge_id,
                value: `${item.kind || "未分类"} · ${item.selection_reason || "未记录原因"}`,
              })),
            ]
          : undefined,
    ),
    fact(
      "plan-rounds",
      "规划轮次",
      planRounds !== null ? "observed" : plannerMode ? "not_observed" : agentState,
      planRounds !== null
        ? `${planRounds.length} 个规划轮次`
        : plannerMode
          ? "未执行"
          : missingState("规划轮次不可用", "没有规划轮次记录"),
      plannerMode && planRounds === null && frozenPlanner
        ? [{ label: "配置上限", value: `${frozenPlanner.max_rounds} 轮` }]
        : undefined,
    ),
  ];

  const evidenceFacts = [
    fact(
      "source-snapshot",
      "输入快照",
      hasParseIdentity ? "observed" : agentState,
      !hasParseIdentity
        ? missingState("输入快照不可用", "没有输入快照")
        : `${sourceSnapshot?.document_ids.length ?? snapshot?.document_ids.length ?? 0} 份材料 / ${sourceSnapshot?.parsed_document_ids.length ?? parsedIDs.length} 份解析`,
      !hasParseIdentity
        ? undefined
        : [
            {
              label: "材料版本",
              value:
                (sourceSnapshot?.document_ids ?? snapshot?.document_ids ?? []).join("、") ||
                "未记录",
            },
            {
              label: "解析版本",
              value: (sourceSnapshot?.parsed_document_ids ?? parsedIDs).join("、") || "未记录",
            },
            {
              label: "解析选择摘要",
              value: sourceSnapshot?.parse_selection_hash ?? snapshot?.parse_selection_hash,
            },
            {
              label: "解析器配置",
              value: sourceSnapshot?.parser_configuration_id ?? snapshot?.parser_configuration_id,
            },
          ],
    ),
    fact(
      "evidence-items",
      "证据条目",
      evidence !== null || anchoredFindings.length > 0 ? "observed" : agentState,
      evidence !== null
        ? `${evidence.length} 条证据`
        : anchoredFindings.length > 0
          ? `${anchoredFindings.length} 条已锚定证据`
          : missingState("证据条目不可用", "没有已锚定的审查发现"),
    ),
    fact(
      "run-snapshot",
      "运行快照",
      snapshot !== null ? "observed" : diagnosticsLoaded ? "not_observed" : "unavailable",
      snapshot === null ? "未观测到运行快照" : `${snapshot.document_ids.length} 份材料已纳入快照`,
      snapshot === null
        ? undefined
        : [
            { label: "规则集摘要", value: snapshot.rule_set_hash },
            { label: "Playbook 摘要", value: snapshot.playbook_hash },
            { label: "模型", value: snapshot.model_ref },
          ],
    ),
    fact(
      "review-documents",
      "审查材料",
      documentCount === null ? "unavailable" : "observed",
      documentCount === null ? "审查材料不可用" : `${documentCount} 份材料版本`,
    ),
  ];

  const experienceFacts = [
    fact("experience-references", "经验引用", "not_observed", "没有经验引用"),
    fact("experience-versions", "已应用经验版本", "not_observed", "没有已应用经验版本"),
  ];

  return [
    capabilityGroup("model", "模型", "模型调用、已报告用量和冻结的规划配置。", modelFacts),
    capabilityGroup(
      "tools",
      "工具与 MCP",
      "计划调用、工具观测、执行工具事件和 MCP 连接。",
      toolFacts,
    ),
    capabilityGroup(
      "rules",
      "规则与知识",
      "规则包、冻结的知识选择、知识读取和规划轮次。",
      ruleFacts,
    ),
    capabilityGroup(
      "evidence",
      "输入证据",
      "冻结输入快照、证据条目、运行快照与已加载审查材料。",
      evidenceFacts,
    ),
    capabilityGroup("experience", "经验", "经验引用和已应用经验版本。", experienceFacts),
  ];
}

export function capabilitySummary(group: StudioCapabilityGroup): string {
  const observed = group.facts.filter((item) => item.state === "observed").length;
  if (group.state === "observed") {
    return `${observed} / ${group.facts.length} 项来源已观测`;
  }
  if (group.state === "unavailable") return "诊断数据不可用";
  return "本组来源均未观测";
}

function ObservationBadge({ state }: { state: StudioObservationState }) {
  return (
    <span className="studio-observation" data-state={state}>
      {STATE_LABELS[state]}
    </span>
  );
}

function agentStatusTone(status: string | null): StatusTone {
  if (status === null) return "neutral";
  if (status === "completed") return "good";
  if (
    status === "failed" ||
    status === "cancelled" ||
    status === "rejected" ||
    status === "blocked_live"
  ) {
    return "bad";
  }
  if (status === "queued" || status === "planning" || status === "running") {
    return "progress";
  }
  if (status === "partial") return "attention";
  return "neutral";
}

function callStatusTone(status: string): StatusTone {
  if (status === "completed") return "good";
  if (status === "failed" || status === "denied" || status === "cancelled") {
    return "bad";
  }
  if (status === "running" || status === "planned") return "progress";
  if (status === "insufficient" || status === "unsupported") {
    return "attention";
  }
  return "neutral";
}

function callStatusLabel(status: string): string {
  return Object.hasOwn(CALL_STATUS_LABELS, status) ? CALL_STATUS_LABELS[status]! : status;
}

function evidenceVerificationLabel(status: string): string {
  return Object.hasOwn(EVIDENCE_VERIFICATION_LABELS, status)
    ? EVIDENCE_VERIFICATION_LABELS[status]!
    : status;
}

function evidenceTone(status: string): StatusTone {
  if (status === "verified" || status === "typed_semantic_and_frozen_quote_verified") {
    return "good";
  }
  if (status === "rejected") return "bad";
  return "neutral";
}

function EventKindBadge({ kind }: { kind: StudioEventKind }) {
  return (
    <span className="studio-kind-badge" data-kind={kind}>
      {EVENT_KIND_LABELS[kind]}
    </span>
  );
}

function CapabilityCard({
  group,
  selected,
  onSelect,
}: {
  group: StudioCapabilityGroup;
  selected: boolean;
  onSelect: () => void;
}) {
  return (
    <button
      type="button"
      className="studio-capability-card"
      data-state={group.state}
      aria-pressed={selected}
      onClick={onSelect}
    >
      <span className="studio-capability-icon" aria-hidden="true">
        {GROUP_ICONS[group.key]}
      </span>
      <span className="studio-capability-copy">
        <span className="studio-capability-title">{group.title}</span>
        <span className="studio-capability-summary">{capabilitySummary(group)}</span>
      </span>
      <ObservationBadge state={group.state} />
    </button>
  );
}

function ModelRecords({ events }: { events: ExecutionEvent[] }) {
  const modelEvents = events.filter((event) => event.node_kind === "model_call");
  if (modelEvents.length === 0) {
    return <p className="studio-empty-text">未观测到模型调用事件。</p>;
  }
  return (
    <section className="studio-records" aria-labelledby="studio-model-records">
      <h3 id="studio-model-records">模型调用事件</h3>
      <ul>
        {modelEvents.map((event) => (
          <li key={event.seq}>
            <span className="studio-record-title">
              #{event.seq} {studioEventTitle(event)}
            </span>
            <StatusText tone={studioEventTone(event.status)}>
              {event.status ? eventStatusLabel(event.status) : "状态未报告"}
            </StatusText>
            <span className="studio-record-meta">{event.model_ref ?? "模型未报告"}</span>
            <TechnicalDetail
              label="调用技术详情"
              value={`seq=${event.seq} · span=${event.span_id ?? "none"}`}
            />
          </li>
        ))}
      </ul>
    </section>
  );
}

function ToolRecords({ agentReview }: { agentReview: AgentReview | null }) {
  if (agentReview === null) {
    return <p className="studio-empty-text">没有计划调用，也没有工具观测。没有 MCP 连接。</p>;
  }
  const observations = agentReview.observations ?? [];
  return (
    <section className="studio-records" aria-labelledby="studio-tool-records">
      <h3 id="studio-tool-records">计划调用与工具观测</h3>
      {agentReview.plan.calls.length === 0 ? (
        <p className="studio-empty-text">本次计划没有工具调用。</p>
      ) : (
        <ul>
          {agentReview.plan.calls.map((call) => (
            <li key={call.call_id}>
              <span className="studio-record-title">{call.tool_id}</span>
              <StatusText tone={callStatusTone(call.status)}>
                {callStatusLabel(call.status)}
              </StatusText>
              <span className="studio-record-meta">域 {call.domain}</span>
              <TechnicalDetail label="调用技术详情" value={call.call_id}>
                <span>
                  工具版本 {call.tool_version} · 能力 {call.capability_id} · 缺口 {call.gap_id}
                </span>
              </TechnicalDetail>
            </li>
          ))}
        </ul>
      )}
      {observations.length === 0 ? (
        <p className="studio-empty-text">没有工具观测。</p>
      ) : (
        <ul>
          {observations.map((observation) => (
            <li key={`${observation.round}-${observation.call_id}`}>
              <span className="studio-record-title">
                第 {observation.round} 轮 · {observation.tool_id}
              </span>
              <span className="studio-record-meta">
                报告关系 {observation.reported_relation} · 声明状态 {observation.claim_status}
              </span>
              <p className="studio-record-summary">{observation.summary}</p>
              <TechnicalDetail label="观测技术详情" value={observation.call_id} />
            </li>
          ))}
        </ul>
      )}
      <p className="studio-mcp-note">没有 MCP 连接。</p>
    </section>
  );
}

function KnowledgeRecords({
  agentReview,
  knowledge,
}: {
  agentReview: AgentReview | null;
  knowledge: RunSnapshot["governed_knowledge"];
}) {
  const reads = agentReview?.knowledge_reads ?? [];
  const selections = knowledge?.selections ?? [];
  if (reads.length === 0 && selections.length === 0) {
    return <p className="studio-empty-text">没有知识读取记录。</p>;
  }
  return (
    <>
      {selections.length > 0 ? (
        <section className="studio-records" aria-labelledby="studio-knowledge-selection-records">
          <h3 id="studio-knowledge-selection-records">冻结的知识选择</h3>
          <ul>
            {selections.map((item) => (
              <li key={`${item.knowledge_id}-${item.version_id}`}>
                <span className="studio-record-title">{item.knowledge_id}</span>
                <span className="studio-record-meta">
                  {item.kind || "未分类"}
                  {item.required ? " · 必需" : " · 可选"}
                </span>
                {item.selection_reason ? (
                  <p className="studio-record-summary">{item.selection_reason}</p>
                ) : null}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
      {reads.length === 0 ? (
        <p className="studio-empty-text">没有知识读取记录。</p>
      ) : (
        <section className="studio-records" aria-labelledby="studio-knowledge-records">
          <h3 id="studio-knowledge-records">知识读取片段</h3>
          <ul>
            {reads.map((read, index) => (
              <li key={`${read.knowledge_id}-${read.version_id}-${read.start}`}>
                <span className="studio-record-title">知识片段 {index + 1}</span>
                <span className="studio-record-meta">
                  区间 {read.start}–{read.end}
                </span>
                <TechnicalDetail
                  label="知识读取技术详情"
                  value={`${read.knowledge_id} · ${read.version_id}`}
                >
                  <span>文本摘要 {read.text_sha256}</span>
                </TechnicalDetail>
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  );
}

function EvidenceRecords({
  agentReview,
  findings,
}: {
  agentReview: AgentReview | null;
  findings: StudioFindingSource[];
}) {
  const evidence = agentReview?.evidence ?? [];
  const anchored = findings.filter((item) => item.anchorCount > 0);
  if (evidence.length === 0 && anchored.length === 0) {
    return <p className="studio-empty-text">没有已锚定的审查发现。</p>;
  }
  if (evidence.length === 0) {
    return (
      <section className="studio-records" aria-labelledby="studio-evidence-records">
        <h3 id="studio-evidence-records">已锚定的审查发现</h3>
        <ul>
          {anchored.map((item) => (
            <li key={item.id}>
              <span className="studio-record-title">{item.claim}</span>
              <span className="studio-record-meta">
                {item.origin} · {item.anchorCount} 个锚点
              </span>
            </li>
          ))}
        </ul>
      </section>
    );
  }
  return (
    <section className="studio-records" aria-labelledby="studio-evidence-records">
      <h3 id="studio-evidence-records">证据条目</h3>
      <ul>
        {evidence.map((item) => (
          <li key={item.evidence_id}>
            <span className="studio-record-title">{item.semantic_type}</span>
            <StatusText tone={evidenceTone(item.verification)}>
              {evidenceVerificationLabel(item.verification)}
            </StatusText>
            <span className="studio-record-meta">
              {item.value}
              {item.unit ?? ""}
              {item.currency ?? ""}
            </span>
            <TechnicalDetail label="证据技术详情" value={item.evidence_id}>
              <span>锚点状态 {item.anchor.status}</span>
            </TechnicalDetail>
          </li>
        ))}
      </ul>
    </section>
  );
}

function GroupRecords({
  group,
  agentReview,
  events,
  snapshot,
  findings,
}: {
  group: StudioCapabilityGroup;
  agentReview: AgentReview | null;
  events: ExecutionEvent[];
  snapshot: RunSnapshot | null;
  findings: StudioFindingSource[];
}) {
  switch (group.key) {
    case "model":
      return <ModelRecords events={events} />;
    case "tools":
      return <ToolRecords agentReview={agentReview} />;
    case "rules":
      return (
        <KnowledgeRecords agentReview={agentReview} knowledge={snapshot?.governed_knowledge} />
      );
    case "evidence":
      return <EvidenceRecords agentReview={agentReview} findings={findings} />;
    case "experience":
      return <p className="studio-empty-text">没有经验引用，也没有已应用经验版本。</p>;
  }
}

function AgentOverview({
  agentReview,
  attempt,
  execution,
  eventCount,
  modeText,
  roundsText,
  snapshot,
}: {
  agentReview: AgentReview | null;
  attempt: Execution["attempt"] | null;
  execution: Execution | null;
  eventCount: number;
  modeText: string;
  roundsText: string;
  snapshot: RunSnapshot | null;
}) {
  const planner = snapshot?.planner;
  const technicalFields: TechnicalField[] = [
    { label: "尝试标识", value: attempt?.id },
    { label: "规划器标识", value: agentReview?.planner.identity },
    {
      label: "规划配置标识",
      value: agentReview?.planner.configuration_id ?? planner?.configuration_id,
    },
    {
      label: "模型配置标识",
      value:
        agentReview?.planner.model_configuration_id ??
        planner?.model_configuration_id ??
        (planner ? "无" : undefined),
    },
    {
      label: "Prompt 版本",
      value: agentReview?.planner.prompt_version ?? planner?.prompt_version,
    },
    {
      label: "Schema 版本",
      value: agentReview?.planner.schema_version ?? planner?.schema_version,
    },
    {
      label: "运行时版本",
      value: agentReview?.planner.runtime_version ?? planner?.runtime_version,
    },
    {
      label: "工具目录版本",
      value: agentReview?.planner.tool_catalog_version ?? planner?.tool_catalog_version,
    },
    {
      label: "规划回退",
      value: agentReview
        ? `${agentReview.planner.fallback_used ? "是" : "否"}${
            agentReview.planner.fallback_code ? ` · ${agentReview.planner.fallback_code}` : ""
          }`
        : null,
    },
  ];
  return (
    <>
      <header className="studio-panel-head">
        <div>
          <h2>Agent 执行概览</h2>
          <p>
            {agentReview
              ? "这次尝试写入了智能体审查记录。"
              : planner
                ? "规划配置已冻结。五域规划循环没有写入记录。"
                : "刷新诊断后查看这次尝试保存的规划配置。"}
          </p>
        </div>
        <ObservationBadge state={agentReview === null ? "not_observed" : "observed"} />
      </header>
      <dl className="studio-detail-list">
        <div>
          <dt>审查状态</dt>
          <dd>
            <StatusText tone={agentStatusTone(agentReview?.status ?? null)}>
              {agentStatusLabel(agentReview?.status ?? null)}
            </StatusText>
          </dd>
        </div>
        <div>
          <dt>规划模式</dt>
          <dd>{modeText}</dd>
        </div>
        <div>
          <dt>尝试序号</dt>
          <dd>{attempt ? `第 ${attempt.ordinal} 次` : "未报告"}</dd>
        </div>
        <div>
          <dt>计划轮次</dt>
          <dd>{roundsText}</dd>
        </div>
        <div>
          <dt>未解决缺口</dt>
          <dd>
            {agentReview
              ? `${agentReview.unresolved_gap_ids.length} 个`
              : planner
                ? "没有缺口记录"
                : "未观测"}
          </dd>
        </div>
        <div>
          <dt>已保存事件</dt>
          <dd>{execution ? `${eventCount} 条` : "未观测"}</dd>
        </div>
      </dl>
      <TechnicalFieldGroup fields={technicalFields} label="智能体技术详情 · 标识与版本" />
    </>
  );
}

function GroupDetail({
  group,
  agentReview,
  events,
  snapshot,
  findings,
}: {
  group: StudioCapabilityGroup;
  agentReview: AgentReview | null;
  events: ExecutionEvent[];
  snapshot: RunSnapshot | null;
  findings: StudioFindingSource[];
}) {
  const technicalFields = group.facts.flatMap((item) => item.technical ?? []);
  return (
    <>
      <header className="studio-panel-head">
        <div>
          <h2>{group.title}</h2>
          <p>{group.description}</p>
        </div>
        <ObservationBadge state={group.state} />
      </header>
      <dl className="studio-fact-list" aria-label={`${group.title}来源状态`}>
        {group.facts.map((item) => (
          <div className="studio-fact" data-state={item.state} key={item.key}>
            <dt>{item.label}</dt>
            <dd>
              <ObservationBadge state={item.state} />
              <span>{item.value}</span>
            </dd>
          </div>
        ))}
      </dl>
      <GroupRecords
        group={group}
        agentReview={agentReview}
        events={events}
        snapshot={snapshot}
        findings={findings}
      />
      <TechnicalFieldGroup fields={technicalFields} label="能力来源技术详情" />
    </>
  );
}

function CapabilityView({
  groups,
  agentReview,
  attempt,
  execution,
  events,
  modeText,
  roundsText,
  agentSubtitle,
  snapshot,
  findings,
  selected,
  onSelect,
}: {
  groups: StudioCapabilityGroup[];
  agentReview: AgentReview | null;
  attempt: Execution["attempt"] | null;
  execution: Execution | null;
  events: ExecutionEvent[];
  modeText: string;
  roundsText: string;
  agentSubtitle: string;
  snapshot: RunSnapshot | null;
  findings: StudioFindingSource[];
  selected: string;
  onSelect: (key: string) => void;
}) {
  const selectedGroup = groups.find((group) => group.key === selected) ?? null;
  const leftGroups = groups.slice(0, 3);
  const rightGroups = groups.slice(3);
  return (
    <div className="studio-capability-view">
      <div className="studio-map" role="group" aria-label="Agent 能力关系图">
        <div className="studio-map-side studio-map-side-left">
          {leftGroups.map((group) => (
            <CapabilityCard
              key={group.key}
              group={group}
              selected={selected === group.key}
              onSelect={() => onSelect(group.key)}
            />
          ))}
        </div>
        <div className="studio-map-center">
          <button
            type="button"
            className="studio-agent-card"
            aria-pressed={selected === "agent"}
            onClick={() => onSelect("agent")}
          >
            <span className="studio-agent-glow" aria-hidden="true" />
            <span className="studio-agent-icon" aria-hidden="true">
              <RobotOutlined />
            </span>
            <span className="studio-agent-eyebrow">本次执行主体</span>
            <strong className="studio-agent-name">Agent</strong>
            <span className="studio-agent-sub">{agentSubtitle}</span>
            <span className="studio-agent-stats">
              <span className="studio-agent-stat">
                <span className="studio-agent-stat-label">规划模式</span>
                <span className="studio-agent-stat-value">{modeText}</span>
              </span>
              <span className="studio-agent-stat">
                <span className="studio-agent-stat-label">计划轮次</span>
                <span className="studio-agent-stat-value">{roundsText}</span>
              </span>
              <span className="studio-agent-stat">
                <span className="studio-agent-stat-label">已保存事件</span>
                <span className="studio-agent-stat-value">
                  {execution ? `${events.length} 条` : "未观测"}
                </span>
              </span>
            </span>
            <span className="studio-agent-hint">点击查看本次执行概览</span>
          </button>
        </div>
        <div className="studio-map-side studio-map-side-right">
          {rightGroups.map((group) => (
            <CapabilityCard
              key={group.key}
              group={group}
              selected={selected === group.key}
              onSelect={() => onSelect(group.key)}
            />
          ))}
          <div className="studio-legend">
            <h3>状态口径</h3>
            <dl>
              <div>
                <dt>
                  <ObservationBadge state="observed" /> 已观测
                </dt>
                <dd>这次尝试保存了对应来源。</dd>
              </div>
              <div>
                <dt>
                  <ObservationBadge state="not_observed" /> 未观测
                </dt>
                <dd>打开这张卡，看缺的是哪一项来源。</dd>
              </div>
              <div>
                <dt>
                  <ObservationBadge state="unavailable" /> 不可用
                </dt>
                <dd>诊断没有返回。刷新后再看。</dd>
              </div>
            </dl>
          </div>
        </div>
      </div>
      <aside className="studio-panel studio-capability-detail" aria-live="polite">
        {selected === "agent" ? (
          <AgentOverview
            agentReview={agentReview}
            attempt={attempt}
            execution={execution}
            eventCount={events.length}
            modeText={modeText}
            roundsText={roundsText}
            snapshot={snapshot}
          />
        ) : selectedGroup ? (
          <GroupDetail
            group={selectedGroup}
            agentReview={agentReview}
            events={events}
            snapshot={snapshot}
            findings={findings}
          />
        ) : (
          <p className="studio-empty-text">选择一张能力卡，查看这次尝试保存的来源。</p>
        )}
      </aside>
    </div>
  );
}

function EventDetailPanel({
  event,
  execution,
  runtimeVersion,
}: {
  event: ExecutionEvent | null;
  execution: Execution | null;
  runtimeVersion: string | null;
}) {
  return (
    <aside className="studio-panel studio-event-detail" aria-live="polite">
      <header className="studio-panel-head">
        <div>
          <h2>节点详情</h2>
          <p>字段来自该节点已保存的事件记录；没有报告的字段保持“未报告”。</p>
        </div>
        {event ? <EventKindBadge kind={studioEventKind(event)} /> : null}
      </header>
      {event === null ? (
        <p className="studio-empty-text">点击泳道节点或事件列表中的事件，查看保存的字段。</p>
      ) : (
        <>
          <div className="studio-detail-title">
            <span className="studio-detail-seq">#{event.seq}</span>
            <strong>{studioEventTitle(event)}</strong>
          </div>
          <dl className="studio-detail-list">
            <div>
              <dt>节点类型</dt>
              <dd>{nodeKindLabel(event.node_kind)}</dd>
            </div>
            <div>
              <dt>状态</dt>
              <dd>
                <StatusText tone={studioEventTone(event.status)}>
                  {event.status ? eventStatusLabel(event.status) : "状态未报告"}
                </StatusText>
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
              <dt>模型 / 工具</dt>
              <dd>{event.model_ref ?? "未报告"}</dd>
            </div>
            {isEinoToolEvent(event, runtimeVersion) ? (
              <div>
                <dt>调用方式</dt>
                <dd>Eino InvokableTool</dd>
              </div>
            ) : null}
            <div>
              <dt>调用层级</dt>
              <dd>
                {event.span_id ? (
                  <TechnicalDetail label="Span 技术详情" value={event.span_id}>
                    <span>父 Span {event.parent_span_id ?? "无已报告父节点"}</span>
                  </TechnicalDetail>
                ) : (
                  "未关联 Span"
                )}
              </dd>
            </div>
            <div>
              <dt>
                {event.event_type === "agent_review_event" &&
                event.status !== "failed" &&
                event.status !== "cancelled"
                  ? "结果代码"
                  : "错误代码"}
              </dt>
              <dd>{event.error_code ?? "未报告"}</dd>
            </div>
          </dl>
          <section className="studio-token-block" aria-labelledby="studio-token-title">
            <h3 id="studio-token-title">Token 用量</h3>
            {event.usage === null ? (
              <p className="studio-empty-text">本节点未报告 Token 用量，不推算。</p>
            ) : (
              <dl className="studio-token-grid">
                <div>
                  <dt>输入</dt>
                  <dd>{tokenCount(event.usage.input_tokens)}</dd>
                </div>
                <div>
                  <dt>输出</dt>
                  <dd>{tokenCount(event.usage.output_tokens)}</dd>
                </div>
                <div>
                  <dt>合计</dt>
                  <dd>{tokenCount(event.usage.total_tokens)}</dd>
                </div>
                <div>
                  <dt>来源</dt>
                  <dd>{event.usage.source || "未记录"}</dd>
                </div>
              </dl>
            )}
          </section>
          {event.trace ? (
            <section className="studio-token-block">
              <h3>模型输入与输出</h3>
              <TechnicalDetail
                label="查看已保存输入"
                value={JSON.stringify(event.trace.request_messages, null, 2)}
              />
              <TechnicalDetail
                label="查看已保存输出"
                value={event.trace.response_text || "未报告"}
              />
              {event.trace.request_truncated || event.trace.response_truncated ? (
                <p>记录已截断。</p>
              ) : null}
            </section>
          ) : null}
          <TechnicalDetail
            label="事件技术详情"
            value={JSON.stringify({
              attempt_id: event.attempt_id,
              seq: event.seq,
              phase: event.phase,
              event_type: event.event_type,
              node_kind: event.node_kind,
              status: event.status,
              span_id: event.span_id,
              parent_span_id: event.parent_span_id,
              error_code: event.error_code,
            })}
          >
            {execution ? <span>执行 Schema {execution.schema_version}</span> : null}
          </TechnicalDetail>
        </>
      )}
    </aside>
  );
}

function ExecutionSummary({
  execution,
  attempt,
  events,
}: {
  execution: Execution;
  attempt: Execution["attempt"] | null;
  events: ExecutionEvent[];
}) {
  const state = attempt?.state ?? execution.attempt.state;
  const modelCalls = distinctEventCalls(events.filter((event) => event.node_kind === "model_call"));
  const toolCalls = distinctEventCalls(events.filter((event) => event.node_kind === "tool_call"));
  return (
    <dl className="studio-summary-grid" aria-label="本次执行摘要">
      <div>
        <dt>执行状态</dt>
        <dd>
          <StatusText tone={runTone(state)}>{runLabel(state)}</StatusText>
        </dd>
      </div>
      <div>
        <dt>总时长</dt>
        <dd>{elapsed(execution.duration_ms)}</dd>
      </div>
      <div>
        <dt>已保存事件</dt>
        <dd>{events.length} 条</dd>
      </div>
      <div>
        <dt>模型调用</dt>
        <dd>{modelCalls} 次</dd>
      </div>
      <div>
        <dt>工具调用</dt>
        <dd>{toolCalls} 次</dd>
      </div>
      <div>
        <dt>Token 合计</dt>
        <dd>{tokenCount(execution.usage?.total_tokens ?? null)}</dd>
      </div>
    </dl>
  );
}

function ExecutionView({
  execution,
  attempt,
  events,
  selectedSeq,
  onSelectSeq,
  runtimeVersion,
}: {
  execution: Execution | null;
  attempt: Execution["attempt"] | null;
  events: ExecutionEvent[];
  selectedSeq: number | null;
  onSelectSeq: (seq: number) => void;
  runtimeVersion: string | null;
}) {
  if (execution === null) {
    return (
      <section className="studio-panel studio-empty-state" role="status">
        <h2>未观测到本次执行</h2>
        <p>诊断数据未返回执行明细，或执行记录不可用。本页不会用审查摘要反推事件、步骤或耗时。</p>
      </section>
    );
  }
  const selectedEvent =
    selectedSeq === null ? null : (events.find((event) => event.seq === selectedSeq) ?? null);
  return (
    <div className="studio-view studio-execution-view">
      <ExecutionSummary execution={execution} attempt={attempt} events={events} />
      <div className="studio-execution-layout">
        <section className="studio-panel studio-lanes-panel">
          <header className="studio-panel-head">
            <div>
              <h2>Agent 执行图</h2>
              <p>同一次调用按 Span 合并；框架包装省略，完整记录可在事件列表查看。</p>
            </div>
          </header>
          <ExecutionDiagram events={events} selectedSeq={selectedSeq} onSelectSeq={onSelectSeq} />
          {selectedEvent
            ? (() => {
                const node = buildExecutionDiagram(events).find((item) =>
                  item.events.includes(selectedEvent),
                );
                return node && node.events.length > 1 ? (
                  <p className="studio-lane-hint">
                    本调用的原始事件：{" "}
                    {node.events.map((event) => (
                      <button
                        type="button"
                        className="studio-event-link"
                        key={event.seq}
                        onClick={() => onSelectSeq(event.seq)}
                      >
                        #{event.seq} {eventTypeLabel(event.event_type)}{" "}
                      </button>
                    ))}
                  </p>
                ) : null;
              })()
            : null}
        </section>
        <EventDetailPanel
          event={selectedEvent}
          execution={execution}
          runtimeVersion={runtimeVersion}
        />
      </div>
    </div>
  );
}

function EventsView({
  events,
  execution,
  query,
  onQueryChange,
  selectedSeq,
  onSelectSeq,
  runtimeVersion,
}: {
  events: ExecutionEvent[];
  execution: Execution | null;
  query: string;
  onQueryChange: (value: string) => void;
  selectedSeq: number | null;
  onSelectSeq: (seq: number) => void;
  runtimeVersion: string | null;
}) {
  const filtered = filterStudioEvents(events, query);
  const selectedEvent =
    selectedSeq === null ? null : (events.find((event) => event.seq === selectedSeq) ?? null);
  return (
    <div className="studio-view studio-events-view">
      <div className="studio-execution-layout">
        <section className="studio-panel studio-events-panel">
          <header className="studio-panel-head">
            <div>
              <h2>事件列表</h2>
              <p>按权威序号排列；搜索只筛选本页已加载的事件。</p>
            </div>
            <span className="studio-count" role="status">
              {execution
                ? `当前匹配 ${filtered.length} / 已加载 ${events.length}`
                : "事件数据未加载"}
            </span>
          </header>
          <Input
            className="studio-event-search"
            allowClear
            aria-label="搜索事件"
            placeholder="搜索序号、阶段、事件类型、状态、模型或错误码"
            value={query}
            onChange={(event) => onQueryChange(event.target.value)}
          />
          {execution === null ? (
            <p className="studio-empty-text">未观测到执行事件：诊断数据不可用或未返回执行明细。</p>
          ) : events.length === 0 ? (
            <p className="studio-empty-text">本次执行没有保存事件。</p>
          ) : filtered.length === 0 ? (
            <p className="studio-empty-text">没有匹配的事件。</p>
          ) : (
            <div className="studio-table-scroll">
              <table className="studio-event-table">
                <caption className="sr-only">本次执行已保存的事件</caption>
                <thead>
                  <tr>
                    <th scope="col">序号</th>
                    <th scope="col">阶段</th>
                    <th scope="col">事件类型</th>
                    <th scope="col">状态</th>
                    <th scope="col">时间</th>
                  </tr>
                </thead>
                <tbody>
                  {filtered.map((event) => (
                    <tr
                      key={event.seq}
                      data-selected={selectedSeq === event.seq ? "true" : undefined}
                    >
                      <td className="studio-event-seq">#{event.seq}</td>
                      <td>{phaseLabel(event.phase)}</td>
                      <td>
                        <button
                          type="button"
                          className="studio-event-link"
                          aria-label={`查看事件 ${event.seq} ${eventTypeLabel(event.event_type)}`}
                          onClick={() => onSelectSeq(event.seq)}
                        >
                          <span>{eventTypeLabel(event.event_type)}</span>
                          <EventKindBadge kind={studioEventKind(event)} />
                        </button>
                      </td>
                      <td>
                        <StatusText tone={studioEventTone(event.status)}>
                          {event.status ? eventStatusLabel(event.status) : "状态未报告"}
                        </StatusText>
                      </td>
                      <td>
                        <time dateTime={event.at ?? undefined}>{instant(event.at)}</time>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
        <EventDetailPanel
          event={selectedEvent}
          execution={execution}
          runtimeVersion={runtimeVersion}
        />
      </div>
    </div>
  );
}

export function AgentStudioPage() {
  const { caseId = "", runId = "" } = useParams();
  const [params] = useSearchParams();
  const attemptParam = params.get("attempt") ?? params.get("attempt_id");
  const review = useReview(caseId, runId, attemptParam);
  const reviewAttempt = review.data?.review_attempts?.attempt ?? null;
  const effectiveAttemptId = attemptParam ?? reviewAttempt?.id ?? null;
  const diagnostics = useReviewDiagnostics(caseId, runId, effectiveAttemptId, true);
  const selectedAttempt = reviewAttempt ?? diagnostics.data?.execution?.attempt ?? null;
  const caseDetail = useCaseDetail(caseId);
  const [view, setView] = useState<StudioView>("execution");
  const [eventQuery, setEventQuery] = useState("");
  const [selectedSeq, setSelectedSeq] = useState<number | null>(null);
  const [selectedCapability, setSelectedCapability] = useState("agent");

  const diagnosticExecution = diagnostics.data?.execution ?? null;
  const diagnosticsLoaded = diagnostics.isSuccess && diagnostics.data !== undefined;
  const diagnosticsExecutionMatches =
    diagnosticExecution === null ||
    selectedAttempt === null ||
    diagnosticExecution.attempt.id === selectedAttempt.id;
  const reviewExecution = review.data?.execution ?? null;
  const reviewExecutionMatches =
    reviewExecution === null ||
    selectedAttempt === null ||
    reviewExecution.attempt.id === selectedAttempt.id;
  const execution =
    (diagnosticsExecutionMatches ? diagnosticExecution : null) ??
    (reviewExecutionMatches ? reviewExecution : null);
  const agentReview = execution?.agent_review ?? null;
  const businessReview = review.data?.agent_review ?? null;
  const snapshot = diagnostics.data?.snapshot ?? review.data?.snapshot ?? null;
  const documentCount = review.data?.documents.length ?? null;
  const events = useMemo(() => {
    const source = execution?.events ?? [];
    const attemptId = selectedAttempt?.id ?? null;
    const scoped =
      attemptId === null ? source : source.filter((event) => event.attempt_id === attemptId);
    return [...scoped].sort((left, right) => left.seq - right.seq);
  }, [execution, selectedAttempt?.id]);
  const studioFindings = useMemo(
    () =>
      (review.data?.findings ?? []).map((item) => ({
        id: item.id,
        claim: item.claim,
        origin: item.origin,
        anchorCount: item.evidence.length,
      })),
    [review.data?.findings],
  );
  const capabilityGroups = useMemo(
    () =>
      buildCapabilityGroups({
        execution,
        agentReview,
        businessReview,
        snapshot,
        documentCount,
        diagnosticsLoaded,
        findings: studioFindings,
      }),
    [
      execution,
      agentReview,
      businessReview,
      snapshot,
      documentCount,
      diagnosticsLoaded,
      studioFindings,
    ],
  );
  const plannerMode = plannerModeOf(review.data, execution, snapshot);
  const roundsText = planRoundsText(agentReview, plannerMode);
  const agentSubtitle = agentCardSubtitle(agentReview, selectedAttempt?.state ?? null, plannerMode);
  const modeText = plannerModeLabel(plannerMode);
  const runState = selectedAttempt?.state ?? review.data?.run.state ?? "unknown";
  const reviewHref = `/cases/${encodeURIComponent(caseId)}/reviews/${encodeURIComponent(runId)}${
    selectedAttempt?.id ? `?attempt_id=${encodeURIComponent(selectedAttempt.id)}` : ""
  }`;

  if (caseId === "" || runId === "") {
    return (
      <PageStatus
        kind="error"
        title="审查身份无法确认"
        detail="地址缺少案件或审查标识，请返回审查记录重新进入。"
        action={
          <Button type="primary" href="/review">
            返回审查列表
          </Button>
        }
      />
    );
  }
  if (review.isLoading) {
    return <PageStatus kind="loading" title="正在加载 Agent Studio" />;
  }
  if (review.isError) {
    if (isForbidden(review.error) || isUnauthenticated(review.error)) {
      return (
        <PageStatus
          kind="denied"
          title="没有权限查看这次审查"
          detail={errorMessage(review.error)}
        />
      );
    }
    return (
      <PageStatus
        kind="error"
        title="审查记录没有加载"
        detail={errorMessage(review.error)}
        action={
          <Button type="primary" onClick={() => void review.refetch()}>
            重试
          </Button>
        }
      />
    );
  }
  if (!review.data) {
    return <PageStatus kind="empty" title="没有审查数据" />;
  }
  if (
    review.data.run.id !== runId ||
    review.data.run.case_id !== caseId ||
    (attemptParam !== null && selectedAttempt?.id !== attemptParam)
  ) {
    return (
      <PageStatus
        kind="error"
        title="审查身份无法确认"
        detail="服务端返回的审查或尝试记录与当前地址不一致，请返回审查记录重新选择。"
      />
    );
  }
  if (
    caseDetail.isError &&
    (isForbidden(caseDetail.error) || isUnauthenticated(caseDetail.error))
  ) {
    return (
      <PageStatus
        kind="denied"
        title="没有权限查看这个案件"
        detail={errorMessage(caseDetail.error)}
      />
    );
  }

  const caseTitle = caseDetail.data?.title ?? "案件";
  const diagnosticsIdentityConflict = diagnosticExecution !== null && !diagnosticsExecutionMatches;
  const refreshing = review.isFetching || diagnostics.isFetching || caseDetail.isFetching;

  return (
    <section className="page page-agent-studio">
      <header className="page-head studio-head">
        <div className="studio-head-main">
          <div className="studio-head-topline">
            <Link className="studio-back" to={reviewHref}>
              ← 返回审查
            </Link>
            <span className="studio-kicker">Agent Studio · 只读</span>
          </div>
          <h1>{caseTitle}</h1>
          <div className="studio-head-meta">
            <StatusText tone={runTone(runState)}>{runLabel(runState)}</StatusText>
            <span className="studio-meta-item">
              规划模式 <strong>{modeText}</strong>
            </span>
            <span className="studio-meta-item">
              尝试{" "}
              <strong>{selectedAttempt ? `第 ${selectedAttempt.ordinal} 次` : "未报告"}</strong>
            </span>
            {selectedAttempt ? (
              <span className="studio-meta-item">
                历史类型 <strong>{historyKindLabel(selectedAttempt.history_kind)}</strong>
              </span>
            ) : null}
            <TechnicalDetail
              label="尝试技术详情"
              value={selectedAttempt?.id ?? effectiveAttemptId ?? undefined}
            >
              <dl className="studio-technical-inline">
                <div>
                  <dt>尝试序号</dt>
                  <dd>{selectedAttempt ? `第 ${selectedAttempt.ordinal} 次` : "未报告"}</dd>
                </div>
                <div>
                  <dt>重试来源</dt>
                  <dd>{selectedAttempt?.retry_of_attempt_id ?? "无"}</dd>
                </div>
                <div>
                  <dt>运行标识</dt>
                  <dd>{runId}</dd>
                </div>
              </dl>
            </TechnicalDetail>
          </div>
          {caseDetail.isError ? (
            <p className="studio-inline-note" role="status">
              案件名称暂不可用：{errorMessage(caseDetail.error)}
            </p>
          ) : null}
        </div>
        <div className="studio-head-actions">
          <Button
            type="primary"
            loading={refreshing}
            onClick={() => {
              void Promise.all([review.refetch(), diagnostics.refetch(), caseDetail.refetch()]);
            }}
          >
            刷新
          </Button>
        </div>
      </header>

      {diagnostics.isError ? (
        <p className="studio-notice studio-notice-warning" role="status">
          诊断数据不可用：{errorMessage(diagnostics.error)}。执行与能力来源将显示为未观测或不可用。
        </p>
      ) : null}
      {diagnostics.isFetching && diagnostics.data === undefined ? (
        <p className="studio-notice" role="status">
          正在读取执行诊断…
        </p>
      ) : null}
      {diagnostics.isSuccess && diagnostics.data.execution === null ? (
        <p className="studio-notice" role="status">
          诊断响应没有返回执行明细；以下使用审查响应中已返回的真实记录，缺失部分显示为未观测。
        </p>
      ) : null}
      {diagnosticsIdentityConflict ? (
        <p className="studio-notice studio-notice-warning" role="alert">
          诊断执行记录与当前尝试不一致，已忽略不匹配的诊断数据。
        </p>
      ) : null}

      <div className="studio-viewbar">
        <Segmented
          aria-label="Agent Studio 视图"
          value={view}
          onChange={(value) => setView(value as StudioView)}
          options={[
            { label: "能力关系", value: "capabilities" },
            { label: "本次执行", value: "execution" },
            { label: "事件列表", value: "events" },
          ]}
        />
        <span className="studio-readonly">只读视图 · 数据来自本次尝试已保存记录</span>
      </div>

      {view === "capabilities" ? (
        <CapabilityView
          groups={capabilityGroups}
          agentReview={agentReview}
          attempt={selectedAttempt}
          execution={execution}
          events={events}
          modeText={modeText}
          roundsText={roundsText}
          agentSubtitle={agentSubtitle}
          snapshot={snapshot}
          findings={studioFindings}
          selected={selectedCapability}
          onSelect={setSelectedCapability}
        />
      ) : view === "execution" ? (
        <ExecutionView
          execution={execution}
          attempt={selectedAttempt}
          events={events}
          selectedSeq={selectedSeq}
          onSelectSeq={setSelectedSeq}
          runtimeVersion={
            agentReview?.planner.runtime_version ?? snapshot?.planner?.runtime_version ?? null
          }
        />
      ) : (
        <EventsView
          events={events}
          execution={execution}
          query={eventQuery}
          onQueryChange={setEventQuery}
          selectedSeq={selectedSeq}
          onSelectSeq={setSelectedSeq}
          runtimeVersion={
            agentReview?.planner.runtime_version ?? snapshot?.planner?.runtime_version ?? null
          }
        />
      )}
    </section>
  );
}
