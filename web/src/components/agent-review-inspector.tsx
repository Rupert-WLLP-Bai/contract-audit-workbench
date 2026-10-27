import { Button } from "antd";
import type {
  AgentReview,
  AgentReviewCallStatus,
  AgentReviewCapability,
  AgentReviewDomain,
  AgentReviewDomainStatus,
  ExecutionEvent,
} from "../api/types";
import { StatusText } from "./status-text";

const DOMAIN_LABELS: Record<AgentReviewDomain, string> = {
  compliance: "合规与授权",
  commercial: "商务与财务",
  technical: "技术与服务",
  fulfillment: "履约与变更",
  counterparty: "主体与关联",
};
const CAPABILITY_EXPRESSIONS: Record<string, string> = {
  authorization_basis:
    "未接入有版本的制度或审批来源；该结果保持缺证。审批原文抽取只提供事实，不作合规结论。",
  commercial_baseline:
    "只比较当前确定性执行器支持的金额与付款表达，不代表完整商务条款审查。",
  warranty_months: "比较受支持材料中的质保期表达；其他技术承诺保持未覆盖。",
  acceptance_schedule:
    "比较受支持的交付、进场与验收时间表达，不代表履约已完成。",
  counterparty_record:
    "未接入授权登记源；登记状态/关联查询保持缺证。主体原文抽取不推断资质或关联关系。",
  amount: "从冻结材料抽取金额原文事实；抽取结果本身不作合规或付款判断。",
  party: "从冻结材料抽取合同主体名称，不验证主体身份。",
  approval_authority: "从冻结材料抽取审批或授权原文，不代替有效制度核验。",
  approval_validity:
    "只比较冻结来源中明确提取并核验的审批依据生效日、失效日与合同引用日；任一日期缺失时保持缺证，不使用当前日期补值。",
  effective_scope: "从冻结材料抽取服务或项目范围原文。",
  scope: "从冻结材料抽取技术或服务范围原文，不判断是否已履约。",
  milestone: "从冻结材料抽取交付、验收、进场、开工、开通或上线等里程碑原文。",
  explicit_start_event: "从冻结材料抽取明确的启动时间表达，不推断缺失日期。",
  subject_id: "从冻结材料抽取统一社会信用代码，不查询登记状态。",
  signing_party:
    "从冻结材料抽取签署方或签约方原文；名称相同不足以确认签署身份，需核验稳定主体 ID 与角色关联。",
  payee:
    "从冻结材料抽取收款方原文；名称相同不足以确认收款资格，需核验稳定主体 ID 与收款角色关联。",
  responsibility: "从冻结材料抽取责任、义务原文，不判断履约完成。",
  "dedicated_line.minimum_bandwidth":
    "比较同一电路的最低保障带宽，要求可比单位与对象身份。",
  "dedicated_line.monthly_availability":
    "比较同口径自然月可用率，测量周期、时区、日历和故障口径需一致。",
  "payment.advance_ratio.ceiling":
    "比较预付款比例与已配置上限，并保留合同总额等计算基数口径。",
};

function capabilityExpression(capabilityId: string): string {
  const expression = Object.hasOwn(CAPABILITY_EXPRESSIONS, capabilityId)
    ? CAPABILITY_EXPRESSIONS[capabilityId]
    : undefined;
  return (
    expression ??
    "当前界面没有此能力的支持说明；以服务端状态与原因代码为准，未知能力不视为通过。"
  );
}

const MODE_LABELS = {
  fixed_chain: "固定链",
  deterministic_planner: "确定性规划器",
  model_fixture: "模型规划器配置：fixture",
  model_live: "模型规划器配置：live",
} as const;

const RELATION_LABELS = {
  compatible: "符合",
  conflict: "冲突",
  insufficient: "证据不足",
  not_applicable: "不适用",
} as const;

function reviewTone(
  status: AgentReview["status"],
): "neutral" | "attention" | "bad" {
  if (status === "failed" || status === "rejected") return "bad";
  if (
    status === "partial" ||
    status === "blocked_live" ||
    status === "cancelled"
  )
    return "attention";
  return "neutral";
}

function relationTone(
  capability: AgentReviewCapability,
  evidence: AgentReview["evidence"],
): "good" | "bad" | "attention" | "neutral" {
  if (
    capability.status === "unsupported" ||
    capability.status === "unknown" ||
    capability.status === "partial" ||
    capability.status === "insufficient"
  )
    return "attention";
  if (capability.relation === "conflict") return "bad";
  if (capability.relation === "insufficient" || capability.relation === null)
    return "attention";
  const verified =
    capability.evidence_ids.length > 0 &&
    capability.evidence_ids.every((id) =>
      evidence.some(
        (item) =>
          item.evidence_id === id &&
          item.anchor.status === "located" &&
          (item.verification === "verified" ||
            item.verification === "typed_semantic_and_frozen_quote_verified"),
      ),
    );
  if (
    capability.relation === "compatible" &&
    (capability.status === "supported" || capability.status === "completed") &&
    verified
  )
    return "good";
  return "neutral";
}

function domainTone(status: AgentReviewDomainStatus): "neutral" | "attention" {
  return status === "not_requested" ||
    status === "supported" ||
    status === "completed" ||
    status === "not_applicable"
    ? "neutral"
    : "attention";
}

function callTone(
  status: AgentReviewCallStatus,
): "neutral" | "attention" | "bad" {
  if (status === "denied" || status === "failed") return "bad";
  if (
    status === "cancelled" ||
    status === "insufficient" ||
    status === "unsupported"
  )
    return "attention";
  return "neutral";
}

function planTone(
  status: AgentReview["plan"]["status"],
): "neutral" | "attention" | "bad" {
  if (status === "guard_rejected") return "bad";
  if (status === "not_planned" || status === "partial" || status === "fallback")
    return "attention";
  return "neutral";
}

function eventRoundLabel(round: number): string {
  return round === 0 ? "准备阶段" : `第 ${round} 轮`;
}

function nullableCount(value: number | null): string {
  return value === null ? "未知" : value.toLocaleString("zh-CN");
}

function showIds(values: string[] | null): string {
  return values === null
    ? "未记录"
    : values.length === 0
      ? "无已报告 ID"
      : values.join("、");
}

export function AgentReviewInspector({
  agentReview,
  caseId,
  attemptId,
  executionEvents,
}: {
  agentReview: AgentReview | null;
  caseId: string;
  attemptId: string;
  executionEvents: ExecutionEvent[];
}) {
  if (!agentReview) {
    return (
      <section
        className="agent-review-panel"
        aria-labelledby="agent-review-title"
      >
        <h3 id="agent-review-title">五域检查与计划</h3>
        <p>
          此 attempt
          没有持久的五域计划记录。历史输入不会补造为当前结果，未报告范围保持未知。
        </p>
      </section>
    );
  }
  if (agentReview.attempt_id !== attemptId) {
    return (
      <section
        className="agent-review-panel"
        aria-labelledby="agent-review-title"
      >
        <h3 id="agent-review-title">五域检查与计划</h3>
        <p className="workflow-error" role="alert">
          五域记录的 attempt ID 与当前执行快照不一致；已隐藏该记录。
        </p>
      </section>
    );
  }

  const unresolved = new Set(agentReview.unresolved_gap_ids);
  const reportedGapIds = new Set(agentReview.gaps.map((gap) => gap.gap_id));
  const unreportedGapIds = [...new Set(agentReview.unresolved_gap_ids)].filter(
    (gapId) => !reportedGapIds.has(gapId),
  );
  const modelRefs = [
    ...new Set(
      executionEvents
        .map((event) => event.model_ref)
        .filter((value): value is string => value !== null),
    ),
  ];
  const modelSources = [
    ...new Set(
      executionEvents
        .map((event) => event.usage?.source ?? null)
        .filter((value): value is string => value !== null),
    ),
  ];

  return (
    <section
      className="agent-review-panel"
      aria-labelledby="agent-review-title"
    >
      <header className="agent-review-head">
        <div>
          <h3 id="agent-review-title">五域检查与计划</h3>
          <p className="meta">
            当前 attempt {agentReview.attempt_id}，schema{" "}
            {agentReview.schema_version}
          </p>
        </div>
        <StatusText tone={reviewTone(agentReview.status)}>
          {agentReview.status}
        </StatusText>
      </header>

      <section
        className="agent-review-section"
        aria-labelledby="agent-review-mode-title"
      >
        <h4 id="agent-review-mode-title">冻结配置与来源</h4>
        <dl className="agent-review-grid">
          <div>
            <dt>规划器模式</dt>
            <dd>{MODE_LABELS[agentReview.mode]}</dd>
          </div>
          <div>
            <dt>Planner configuration ID</dt>
            <dd>
              <code>{agentReview.planner.configuration_id}</code>
            </dd>
          </div>
          <div>
            <dt>模型配置 ID</dt>
            <dd>
              <code>
                {agentReview.planner.model_configuration_id ?? "未记录"}
              </code>
            </dd>
          </div>
          <div>
            <dt>实际模型来源</dt>
            <dd>未知；规划器配置和模型引用不证明实际调用来源。</dd>
          </div>
          <div>
            <dt>执行事件中的模型引用</dt>
            <dd>{showIds(modelRefs)}</dd>
          </div>
          <div>
            <dt>执行事件中的用量来源</dt>
            <dd>{showIds(modelSources)}</dd>
          </div>
          <div>
            <dt>回退</dt>
            <dd>
              {agentReview.planner.fallback_used
                ? `已回退：${agentReview.planner.fallback_code ?? "原因未报告"}`
                : "未报告回退"}
            </dd>
          </div>
          <div>
            <dt>计划限制</dt>
            <dd>
              {agentReview.planner.limits.max_rounds} 轮，
              {agentReview.planner.limits.max_calls} 次调用，
              {agentReview.planner.limits.max_duration_ms} ms，
              {agentReview.planner.limits.max_evidence_bytes.toLocaleString(
                "zh-CN",
              )}{" "}
              字节证据
            </dd>
          </div>
        </dl>
        {agentReview.source_snapshot ? (
          <p className="meta">
            冻结材料 {showIds(agentReview.source_snapshot.document_ids)}，
            解析版本 {showIds(agentReview.source_snapshot.parsed_document_ids)}{" "}
          ，解析选择摘要{" "}
            <code>{agentReview.source_snapshot.parse_selection_hash}</code>
          </p>
        ) : (
          <p className="callout">
            没有报告冻结来源快照；不能据此声称来源已核对。
          </p>
        )}
        {agentReview.rulepack ? (
          <p className="meta">
            冻结规则包 {agentReview.rulepack.id} v{agentReview.rulepack.version}{" "}
          ，SHA-256 <code>{agentReview.rulepack.content_sha256}</code>
          </p>
        ) : (
          <p className="meta">本次五域投影没有规则包身份。</p>
        )}
      </section>

      <section
        className="agent-review-section"
        aria-labelledby="agent-review-scope-title"
      >
        <h4 id="agent-review-scope-title">范围、支持表达与证据覆盖</h4>
        {agentReview.scope.length === 0 ? (
          <p>没有报告审查范围；不推断为五域均已检查。</p>
        ) : (
          <ul className="agent-review-grid-list">
            {agentReview.scope.map((scope) => {
              const coverage = agentReview.domains.find(
                (item) => item.domain === scope.domain,
              );
              return (
                <li className="agent-review-card" key={scope.domain}>
                  <header>
                    <strong>{DOMAIN_LABELS[scope.domain]}</strong>
                    <StatusText
                      tone={domainTone(coverage?.status ?? "unknown")}
                    >
                      {coverage?.status ?? "未报告"}
                    </StatusText>
                  </header>
                  <p>
                    <strong>支持能力：</strong>
                    {showIds(scope.capability_ids)}
                  </p>
                  <p>
                    <strong>来源证据 ID：</strong>
                    {showIds(scope.source_ids)}
                  </p>
                  {!coverage || coverage.capabilities.length === 0 ? (
                    <p>没有报告具体能力结果。</p>
                  ) : (
                    <ul className="agent-review-capabilities">
                      {coverage.capabilities.map((capability) => (
                        <li key={capability.capability_id}>
                          <header>
                            <code>{capability.capability_id}</code>
                            <StatusText
                              tone={relationTone(
                                capability,
                                agentReview.evidence,
                              )}
                            >
                              {capability.relation
                                ? RELATION_LABELS[capability.relation]
                                : capability.status}
                            </StatusText>
                          </header>
                          <p>
                            <strong>支持表达：</strong>
                            {capabilityExpression(capability.capability_id)}
                          </p>
                          <p>
                            状态：{capability.status}，原因：
                            {capability.reason_code || "未提供"}
                          </p>
                          <p>
                            证据 ID：{showIds(capability.evidence_ids)}，
                            专线检查 ID：
                            {showIds(capability.canonical_evaluation_ids)}
                          </p>
                          {capability.evidence_ids.length > 0 ? (
                            <ul
                              className="agent-review-capability-citations"
                              aria-label={`${capability.capability_id} 来源引用`}
                            >
                              {capability.evidence_ids.map((evidenceId) => {
                                const evidence = agentReview.evidence.find(
                                  (item) => item.evidence_id === evidenceId,
                                );
                                if (!evidence)
                                  return (
                                    <li key={evidenceId}>
                                      <code>{evidenceId}</code>，
                                      来源引用详情未报告
                                    </li>
                                  );
                                const anchor = evidence.anchor;
                                return (
                                  <li key={evidenceId}>
                                    <p>
                                      <strong>来源事实：</strong>
                                      <code>{evidenceId}</code>，
                                      {evidence.semantic_type}，
                                      {evidence.verification}
                                    </p>
                                    {anchor.status === "located" ? (
                                      <>
                                        <p>
                                          材料 {anchor.document_id}，
                                          码点半开区间 [{anchor.start},
                                          {anchor.end})
                                        </p>
                                        {anchor.quote ? (
                                          <blockquote>
                                            {anchor.quote}
                                          </blockquote>
                                        ) : (
                                          <p>没有报告原文引用。</p>
                                        )}
                                      </>
                                    ) : (
                                      <p>
                                        引用未定位：
                                        {anchor.reason ?? "未提供原因"}
                                      </p>
                                    )}
                                  </li>
                                );
                              })}
                            </ul>
                          ) : null}
                        </li>
                      ))}
                    </ul>
                  )}
                </li>
              );
            })}
          </ul>
        )}
        {agentReview.domains.some(
          (coverage) =>
            !agentReview.scope.some(
              (scope) => scope.domain === coverage.domain,
            ),
        ) ? (
          <p className="callout">
            有领域结果不在已报告 scope
            中；请按服务端范围记录复核，不将其并入声明覆盖。
          </p>
        ) : null}
      </section>

      <section
        className="agent-review-section"
        aria-labelledby="agent-review-gaps-title"
      >
        <h4 id="agent-review-gaps-title">未解决缺口与下一步</h4>
        {agentReview.gaps.length === 0 && unreportedGapIds.length === 0 ? (
          <p>没有报告缺口记录；这不等同于合同没有风险。</p>
        ) : (
          <ul className="agent-review-list">
            {agentReview.gaps.map((gap) => {
              const isUnresolved =
                unresolved.has(gap.gap_id) || gap.status !== "resolved";
              const unsupported = gap.status === "unsupported";
              return (
                <li key={gap.gap_id}>
                  <header>
                    <strong>
                      {DOMAIN_LABELS[gap.domain]}，{gap.capability_id}
                    </strong>
                    <StatusText tone={isUnresolved ? "attention" : "neutral"}>
                      {unsupported
                        ? "能力不支持"
                        : isUnresolved
                          ? "未解决"
                          : "已解决"}
                    </StatusText>
                  </header>
                  <p>
                    缺口 {gap.gap_id}，状态 {gap.status}，原因{" "}
                    {gap.reason_code}
                  </p>
                  <p>关联证据 ID：{showIds(gap.evidence_ids)}</p>
                  {isUnresolved && caseId ? (
                    <Button
                      aria-label={"查看案件材料并补证后新建审查"}
                      href={`/cases/${encodeURIComponent(caseId)}#case-materials`}
                    >
                      查看案件材料并补证后新建审查
                    </Button>
                  ) : null}
                </li>
              );
            })}
            {unreportedGapIds.map((gapId) => (
              <li key={gapId}>
                <header>
                  <strong>未报告缺口详情，{gapId}</strong>
                  <StatusText tone="attention">未解决</StatusText>
                </header>
                <p>
                  服务端报告了该缺口
                  ID，但未提供领域、能力、原因或证据详情；保持未知。
                </p>
                {caseId ? (
                  <Button
                    aria-label={"查看案件材料并补证后新建审查"}
                    href={`/cases/${encodeURIComponent(caseId)}#case-materials`}
                  >
                    查看案件材料并补证后新建审查
                  </Button>
                ) : null}
              </li>
            ))}
          </ul>
        )}
        <p className="meta">
          添加材料会触发新的冻结运行；本页仍显示当前 attempt 的原始输入和结果。
        </p>
      </section>

      <section
        className="agent-review-section"
        aria-labelledby="agent-review-plan-title"
      >
        <h4 id="agent-review-plan-title">计划、守卫与调用</h4>
        <p>
          计划状态：
          <StatusText tone={planTone(agentReview.plan.status)}>
            {agentReview.plan.status}
          </StatusText>
          {agentReview.plan.rejection_code
            ? `，拒绝原因 ${agentReview.plan.rejection_code}`
            : ""}
        </p>
        {agentReview.plan.calls.length === 0 ? (
          <p>没有计划调用记录；不把缺失调用解释为检查通过。</p>
        ) : (
          <ul className="agent-review-list">
            {agentReview.plan.calls.map((call) => (
              <li key={call.call_id}>
                <header>
                  <strong>
                    {call.tool_id} v{call.tool_version}
                  </strong>
                  <StatusText tone={callTone(call.status)}>
                    {call.status}
                  </StatusText>
                </header>
                <p>
                  {DOMAIN_LABELS[call.domain]}，{call.capability_id}，缺口{" "}
                  {call.gap_id}
                </p>
                <p>
                  守卫/工具状态 {call.status}，原因{" "}
                  {call.reason_code ?? "无已报告原因"}，证据 ID{" "}
                  {showIds(call.evidence_ids)}
                </p>
              </li>
            ))}
          </ul>
        )}
        {agentReview.plan.rounds.length > 0 ? (
          <ol className="agent-review-list">
            {agentReview.plan.rounds.map((round) => (
              <li key={round.round}>
                <header>
                  <strong>
                    第 {round.round} 轮，{round.status}
                  </strong>
                  <StatusText
                    tone={
                      round.status === "completed"
                        ? "neutral"
                        : round.status === "accepted"
                          ? "neutral"
                          : "attention"
                    }
                  >
                    {round.status}
                  </StatusText>
                </header>
                <p>
                  目标缺口：{showIds(round.gap_ids)}，新证据：
                  {showIds(round.new_evidence_ids)}，已解决缺口：
                  {showIds(round.resolved_gap_ids)}
                </p>
                {round.termination_code ? (
                  <p>停止原因：{round.termination_code}</p>
                ) : null}
              </li>
            ))}
          </ol>
        ) : (
          <p>没有计划轮次；运行保持未报告状态。</p>
        )}
        {agentReview.events.length > 0 ? (
          <p className="meta">
            规划事件：
            {agentReview.events
              .map(
                (item) =>
                  `${item.sequence}:${eventRoundLabel(item.round)}:${item.kind}/${item.code}`,
              )
              .join("，")}
          </p>
        ) : (
          <p className="meta">没有持久规划事件。</p>
        )}
      </section>

      <section
        className="agent-review-section"
        aria-labelledby="agent-review-evidence-title"
      >
        <h4 id="agent-review-evidence-title">经核验的新证据</h4>
        {agentReview.evidence.length === 0 ? (
          <p>没有报告新增语义事实；空结果保持未知，不推断为无风险。</p>
        ) : (
          <ul className="agent-review-list">
            {agentReview.evidence.map((item) => (
              <li key={item.evidence_id}>
                <header>
                  <strong>
                    {item.semantic_type}: {item.value}
                    {item.unit ? ` ${item.unit}` : ""}
                    {item.currency ? `，${item.currency}` : ""}
                  </strong>
                  <StatusText
                    tone={
                      item.anchor.status === "located" ? "neutral" : "attention"
                    }
                  >
                    {item.anchor.status}
                  </StatusText>
                </header>
                <p>
                  证据 {item.evidence_id}，{item.verification}
                </p>
                <p>
                  材料 {item.anchor.document_id}，解析版本{" "}
                  {item.anchor.parsed_document_id}，规范化摘要{" "}
                  <code>{item.anchor.normalized_digest}</code>
                </p>
                <p>
                  原文码点半开区间 [{item.anchor.start},{item.anchor.end})
                  {item.anchor.page === null ? "" : `，页 ${item.anchor.page}`}
                  {item.anchor.element_id
                    ? `，元素 ${item.anchor.element_id}`
                    : ""}
                </p>
                <blockquote>{item.anchor.quote}</blockquote>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section
        className="agent-review-section"
        aria-labelledby="agent-review-usage-title"
      >
        <h4 id="agent-review-usage-title">规划用量</h4>
        <dl className="agent-review-grid">
          <div>
            <dt>规划器调用</dt>
            <dd>{agentReview.usage.planner_calls.toLocaleString("zh-CN")}</dd>
          </div>
          <div>
            <dt>输入 token</dt>
            <dd>{nullableCount(agentReview.usage.input_tokens)}</dd>
          </div>
          <div>
            <dt>输出 token</dt>
            <dd>{nullableCount(agentReview.usage.output_tokens)}</dd>
          </div>
          <div>
            <dt>合计 token</dt>
            <dd>{nullableCount(agentReview.usage.total_tokens)}</dd>
          </div>
        </dl>
      </section>
    </section>
  );
}
