import { Button } from "antd";
import type {
  AgentReview,
  AgentReviewAdvisoryBand,
  AgentReviewAdvisoryMode,
  AgentReviewAdvisoryProvider,
  AgentReviewAdvisoryStatus,
  AgentReviewCallStatus,
  AgentReviewCapability,
  AgentReviewDomain,
  AgentReviewDomainCoverage,
  AgentReviewDomainStatus,
  AgentReviewEvidence,
  AgentReviewKnowledgeAdvisory,
  AgentReviewEvent,
  AgentReviewKnowledgeSelection,
  AgentReviewPlan,
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
    "尚未接入有版本记录的制度或审批材料，因此证据不足。提取审批原文只提供事实，不能直接得出合规结论。",
  commercial_baseline:
    "只比较当前规则支持的金额和付款表述，不代表已审查全部商务条款。",
  warranty_months: "比较支持的材料中关于质保期的表述；其他技术承诺尚未覆盖。",
  acceptance_schedule:
    "比较支持的交付、进场和验收时间表述，不代表已经完成履约。",
  counterparty_record:
    "尚未接入授权的登记信息来源，无法核实登记状态和关联关系。提取主体名称不会推断其资质或关联关系。",
  amount: "从本次审查使用的材料中提取金额原文事实；抽取结果本身不作合规或付款判断。",
  party: "从本次审查使用的材料中提取合同主体名称，不验证主体身份。",
  approval_authority: "从本次审查使用的材料中提取审批或授权原文，不代替有效制度核验。",
  approval_validity:
    "只比较本次审查材料中已提取并核实的审批依据生效日、失效日与合同引用日；缺少任何日期时，不能用当前日期代替。",
  effective_scope: "从本次审查使用的材料中提取服务或项目范围原文。",
  scope: "从本次审查使用的材料中提取技术或服务范围原文，不判断是否已履约。",
  milestone: "从本次审查使用的材料中提取交付、验收、进场、开工、开通或上线等里程碑原文。",
  explicit_start_event: "从本次审查使用的材料中提取明确的启动时间表达，不推断缺失日期。",
  subject_id: "从本次审查使用的材料中提取统一社会信用代码，不查询登记状态。",
  signing_party:
    "从本次审查使用的材料中提取签署方或签约方原文；名称相同不足以确认签署身份，需核验稳定主体 ID 与角色关联。",
  payee:
    "从本次审查使用的材料中提取收款方原文；名称相同不足以确认收款资格，需核验稳定主体 ID 与收款角色关联。",
  responsibility: "从本次审查使用的材料中提取责任、义务原文，不判断履约完成。",
  "dedicated_line.minimum_bandwidth":
    "比较同一电路的最低保障带宽；单位和比较对象必须对应。",
  "dedicated_line.monthly_availability":
    "比较同口径自然月可用率，测量周期、时区、日历和故障口径需一致。",
  "payment.advance_ratio.ceiling":
    "比较预付款比例与已配置上限，并保留合同总额等计算基数口径。",
};

function capabilityExpression(capabilityId: string): string {
  const expression = Object.hasOwn(CAPABILITY_EXPRESSIONS, capabilityId)
    ? CAPABILITY_EXPRESSIONS[capabilityId]
    : undefined;
  return expression === undefined
    ? "当前没有这一检查项目的说明，请查看系统返回的状态和原因。未知项目不能视为通过。"
    : expression;
}

const MODE_LABELS = {
  fixed_chain: "固定流程",
  deterministic_planner: "按固定规则制定计划",
  model_fixture: "模型计划（测试配置）",
  model_live: "模型计划（实际模型配置）",
} as const;

const RELATION_LABELS = {
  compatible: "符合",
  conflict: "冲突",
  insufficient: "证据不足",
  not_applicable: "不适用",
} as const;

const ADVISORY_STATUS_LABELS: Record<AgentReviewAdvisoryStatus, string> = {
  completed: "建议已生成",
  unavailable: "建议不可用",
  not_applicable: "无需建议",
  model_mismatch: "模型版本不一致",
  invalid_response: "响应无效",
  budget_exhausted: "模型预算不足",
  persistence_failed: "建议记录未写入",
  config_mismatch: "冻结配置不一致",
};

const ADVISORY_BAND_LABELS: Record<AgentReviewAdvisoryBand, string> = {
  relevant: "建议前列",
  uncertain: "建议不确定",
  not_relevant: "建议后置",
  unknown: "无可用建议",
};

const ADVISORY_MODE_LABELS: Record<AgentReviewAdvisoryMode, string> = {
  advisory: "默认建议模式",
  fixture: "本地 fixture",
};

const ADVISORY_PROVIDER_LABELS: Record<AgentReviewAdvisoryProvider, string> = {
  "model-gateway": "模型网关",
  "local-fixture": "本地 fixture",
  direct: "直接调用",
};

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

const UNRESOLVED_CAPABILITY_STATUSES = new Set([
  "unsupported",
  "unknown",
  "partial",
  "insufficient",
]);

function evidenceVerified(
  capability: AgentReviewCapability,
  evidence: AgentReview["evidence"],
): boolean {
  return (
    capability.evidence_ids.length > 0 &&
    capability.evidence_ids.every((id) =>
      evidence.some(
        (item) =>
          item.evidence_id === id &&
          item.anchor.status === "located" &&
          (item.verification === "verified" ||
            item.verification === "typed_semantic_and_frozen_quote_verified"),
      ),
    )
  );
}

function relationTone(
  capability: AgentReviewCapability,
  evidence: AgentReview["evidence"],
): "good" | "bad" | "attention" | "neutral" {
  if (UNRESOLVED_CAPABILITY_STATUSES.has(capability.status)) return "attention";
  if (capability.relation === "conflict") return "bad";
  if (capability.relation === "insufficient" || capability.relation === null)
    return "attention";
  if (
    capability.relation === "compatible" &&
    (capability.status === "supported" || capability.status === "completed") &&
    evidenceVerified(capability, evidence)
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

function knowledgeAdvisoryTone(
  status: AgentReviewAdvisoryStatus,
): "neutral" | "attention" {
  return status === "completed" || status === "not_applicable"
    ? "neutral"
    : "attention";
}

function AdvisoryCandidates({
  candidates,
}: {
  candidates: AgentReviewKnowledgeAdvisory["candidates"];
}) {
  if (candidates.length === 0) {
    return (
      <p className="meta">
        没有可选知识候选获得建议；排序保持确定性顺序。
      </p>
    );
  }
  return (
    <ul className="agent-review-list">
      {candidates.map((candidate) => (
        <li
          key={`${candidate.knowledge_id}\u0000${candidate.version_id}\u0000${candidate.content_sha256}`}
        >
          <header>
            <strong>
              <code>{candidate.knowledge_id}</code> v{candidate.version_id}
            </strong>
            <StatusText tone="neutral">
              {ADVISORY_BAND_LABELS[candidate.band]}
            </StatusText>
          </header>
          <p>
            建议概率{" "}
            {candidate.probability === null
              ? "未返回"
              : `${(candidate.probability * 100).toFixed(1)}%`}
            ，确定性排序第 {candidate.rank_before} 位，建议排序第{" "}
            {candidate.rank_after} 位
            {candidate.reason ? `，原因 ${candidate.reason}` : ""}
          </p>
        </li>
      ))}
    </ul>
  );
}

function KnowledgeAdvisoryDetails({
  advisory,
}: {
  advisory: AgentReviewKnowledgeAdvisory;
}) {
  const failed =
    advisory.status !== "completed" && advisory.status !== "not_applicable";
  return (
    <>
      <p className="callout">
        <strong>Jev 建议排序，不改变结论。</strong>
        建议只影响可选治理知识的先后；规则、引用、证据核验、缺口状态和人工决定仍由确定性流程负责。失败或不可用时按确定性顺序回退。
      </p>
      <dl className="agent-review-grid">
        <div>
          <dt>建议状态</dt>
          <dd>
            <StatusText tone={knowledgeAdvisoryTone(advisory.status)}>
              {ADVISORY_STATUS_LABELS[advisory.status]}
            </StatusText>
            <span className="meta">（{advisory.status}）</span>
          </dd>
        </div>
        <div>
          <dt>提供方与模式</dt>
          <dd>
            {ADVISORY_PROVIDER_LABELS[advisory.provider]}，{" "}
            {ADVISORY_MODE_LABELS[advisory.mode]}
          </dd>
        </div>
        <div>
          <dt>请求模型 / 观测模型</dt>
          <dd>
            <code>{advisory.model_requested === null ? "未请求" : advisory.model_requested}</code> /{" "}
            <code>{advisory.model_observed === null ? "未观测" : advisory.model_observed}</code>
          </dd>
        </div>
        <div>
          <dt>数据边界</dt>
          <dd>仅发送最小化元数据，不发送合同原文</dd>
        </div>
        <div>
          <dt>问题集版本</dt>
          <dd>
            <code>{advisory.question_set_version}</code>
          </dd>
        </div>
        {advisory.error_code ? (
          <div>
            <dt>失败原因</dt>
            <dd>
              <code>{advisory.error_code}</code>
            </dd>
          </div>
        ) : null}
        {advisory.truncated ? (
          <div>
            <dt>候选范围</dt>
            <dd>候选过多，本次仅评估了部分可选知识。</dd>
          </div>
        ) : null}
      </dl>
      <AdvisoryCandidates candidates={advisory.candidates} />
      <p className="meta">
        概率表示模型对相关性的判断，不是正确率，也不是风险结论；候选身份与概率来自冻结审查记录。
      </p>
      {failed ? (
        <p className="callout">
          建议不可用时，审查使用确定性排序继续完成；该失败本身不构成审查结论。
        </p>
      ) : null}
    </>
  );
}

function MissingReview() {
  return (
    <section className="agent-review-panel" aria-labelledby="agent-review-title">
      <h3 id="agent-review-title">五个方面的检查与计划</h3>
      <p>
        本次执行没有保存五个方面的检查计划。未记录的范围仍为未知，系统不会把旧输入补成当前结果。
      </p>
    </section>
  );
}

function MismatchedReview() {
  return (
    <section className="agent-review-panel" aria-labelledby="agent-review-title">
      <h3 id="agent-review-title">五个方面的检查与计划</h3>
      <p className="workflow-error" role="alert">
        检查记录所属的执行编号与当前选择的执行记录不一致，已隐藏该记录。
      </p>
    </section>
  );
}

function InspectorHeader({ review }: { review: AgentReview }) {
  return (
    <header className="agent-review-head">
      <div>
        <h3 id="agent-review-title">五个方面的检查与计划</h3>
        <p className="meta">
          执行编号 {review.attempt_id}，数据格式 {review.schema_version}
        </p>
      </div>
      <StatusText tone={reviewTone(review.status)}>{review.status}</StatusText>
    </header>
  );
}

function ConfigurationSection({
  review,
  modelRefs,
  modelSources,
}: {
  review: AgentReview;
  modelRefs: string[];
  modelSources: string[];
}) {
  return (
    <section
      className="agent-review-section"
      aria-labelledby="agent-review-mode-title"
    >
      <h4 id="agent-review-mode-title">本次使用的配置和材料来源</h4>
      <dl className="agent-review-grid">
        <div>
          <dt>计划生成方式</dt>
          <dd>{MODE_LABELS[review.mode]}</dd>
        </div>
        <div>
          <dt>计划配置 ID</dt>
          <dd>
            <code>{review.planner.configuration_id}</code>
          </dd>
        </div>
        <div>
          <dt>模型配置 ID</dt>
          <dd>
            <code>
              {review.planner.model_configuration_id === null
                ? "未记录"
                : review.planner.model_configuration_id}
            </code>
          </dd>
        </div>
        <div>
          <dt>实际模型来源</dt>
          <dd>未知；计划配置和模型编号不能证明实际调用了哪个模型。</dd>
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
            {review.planner.fallback_used
              ? `已回退：${review.planner.fallback_code === null ? "原因未报告" : review.planner.fallback_code}`
              : "未报告回退"}
          </dd>
        </div>
        <div>
          <dt>计划限制</dt>
          <dd>
            {review.planner.limits.max_rounds} 轮，
            {review.planner.limits.max_calls} 次调用，
            {review.planner.limits.max_duration_ms} ms，
            {review.planner.limits.max_evidence_bytes.toLocaleString("zh-CN")}{" "}
            字节证据
          </dd>
        </div>
      </dl>
      {review.source_snapshot ? (
        <p className="meta">
          本次使用的材料 {showIds(review.source_snapshot.document_ids)}，
          解析版本 {showIds(review.source_snapshot.parsed_document_ids)}{" "}
          ，解析选择摘要 <code>{review.source_snapshot.parse_selection_hash}</code>
        </p>
      ) : (
        <p className="callout">
          未提供本次审查的材料来源记录，无法确认来源已经核对。
        </p>
      )}
      {review.rulepack ? (
        <p className="meta">
          本次使用的规则包 {review.rulepack.id} v{review.rulepack.version} ，
          SHA-256 <code>{review.rulepack.content_sha256}</code>
        </p>
      ) : (
        <p className="meta">这份检查记录没有提供规则包编号。</p>
      )}
    </section>
  );
}

function KnowledgeSection({
  selection,
}: {
  selection: AgentReviewKnowledgeSelection | null | undefined;
}) {
  if (!selection) return null;
  return (
    <section
      className="agent-review-section"
      aria-labelledby="agent-review-knowledge-advisory-title"
    >
      <h4 id="agent-review-knowledge-advisory-title">治理知识与排序建议</h4>
      <p className="meta">
        治理知识策略状态：
        {selection.policy_status}
        {selection.policy_reason ? `（${selection.policy_reason}）` : ""}
        。该状态由确定性规则判定；模型建议不能把它改为可用。
      </p>
      {selection.advisory ? (
        <KnowledgeAdvisoryDetails advisory={selection.advisory} />
      ) : (
        <p>本次审查没有记录模型排序建议；治理知识按确定性规则排序。</p>
      )}
    </section>
  );
}

function EvidenceCitationRow({
  evidenceId,
  evidence,
}: {
  evidenceId: string;
  evidence: AgentReviewEvidence | undefined;
}) {
  if (!evidence) {
    return (
      <li>
        <code>{evidenceId}</code>，来源引用详情未报告
      </li>
    );
  }
  const anchor = evidence.anchor;
  return (
    <li>
      <p>
        <strong>来源事实：</strong>
        <code>{evidenceId}</code>，{evidence.semantic_type}，
        {evidence.verification}
      </p>
      {anchor.status === "located" ? (
        <>
          <p>
            材料 {anchor.document_id}，码点半开区间 [{anchor.start},{anchor.end})
          </p>
          {anchor.quote ? (
            <blockquote>{anchor.quote}</blockquote>
          ) : (
            <p>没有报告原文引用。</p>
          )}
        </>
      ) : (
        <p>
          引用未定位：{anchor.reason === null ? "未提供原因" : anchor.reason}
        </p>
      )}
    </li>
  );
}

function CoverageCapabilityRow({
  capability,
  review,
}: {
  capability: AgentReviewCapability;
  review: AgentReview;
}) {
  return (
    <li>
      <header>
        <code>{capability.capability_id}</code>
        <StatusText tone={relationTone(capability, review.evidence)}>
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
        证据 ID：{showIds(capability.evidence_ids)}，专线检查 ID：
        {showIds(capability.canonical_evaluation_ids)}
      </p>
      {capability.evidence_ids.length > 0 ? (
        <ul
          className="agent-review-capability-citations"
          aria-label={`${capability.capability_id} 来源引用`}
        >
          {capability.evidence_ids.map((evidenceId) => (
            <EvidenceCitationRow
              key={evidenceId}
              evidenceId={evidenceId}
              evidence={review.evidence.find(
                (item) => item.evidence_id === evidenceId,
              )}
            />
          ))}
        </ul>
      ) : null}
    </li>
  );
}

function coverageStatusCode(
  coverage: AgentReviewDomainCoverage | undefined,
): AgentReviewDomainStatus {
  return coverage === undefined ? "unknown" : coverage.status;
}

function coverageStatusLabel(
  coverage: AgentReviewDomainCoverage | undefined,
): string {
  return coverage === undefined ? "未报告" : coverage.status;
}

function ScopeCard({
  scope,
  review,
}: {
  scope: AgentReview["scope"][number];
  review: AgentReview;
}) {
  const coverage = review.domains.find(
    (item) => item.domain === scope.domain,
  );
  return (
    <li className="agent-review-card">
      <header>
        <strong>{DOMAIN_LABELS[scope.domain]}</strong>
        <StatusText tone={domainTone(coverageStatusCode(coverage))}>
          {coverageStatusLabel(coverage)}
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
            <CoverageCapabilityRow
              key={capability.capability_id}
              capability={capability}
              review={review}
            />
          ))}
        </ul>
      )}
    </li>
  );
}

function ScopeSection({ review }: { review: AgentReview }) {
  const hasOutOfScopeDomains = review.domains.some(
    (coverage) =>
      !review.scope.some((scope) => scope.domain === coverage.domain),
  );
  return (
    <section
      className="agent-review-section"
      aria-labelledby="agent-review-scope-title"
    >
      <h4 id="agent-review-scope-title">范围、支持表达与证据覆盖</h4>
      {review.scope.length === 0 ? (
        <p>没有报告审查范围；不推断为五域均已检查。</p>
      ) : (
        <ul className="agent-review-grid-list">
          {review.scope.map((scope) => (
            <ScopeCard key={scope.domain} scope={scope} review={review} />
          ))}
        </ul>
      )}
      {hasOutOfScopeDomains ? (
        <p className="callout">
          部分领域结果不在已报告的审查范围内。请根据系统记录复核，不要将其计入已声明的覆盖范围。
        </p>
      ) : null}
    </section>
  );
}

function GapRow({
  gap,
  unresolved,
  caseId,
}: {
  gap: AgentReview["gaps"][number];
  unresolved: Set<string>;
  caseId: string;
}) {
  const isUnresolved = unresolved.has(gap.gap_id) || gap.status !== "resolved";
  const unsupported = gap.status === "unsupported";
  return (
    <li>
      <header>
        <strong>
          {DOMAIN_LABELS[gap.domain]}，{gap.capability_id}
        </strong>
        <StatusText tone={isUnresolved ? "attention" : "neutral"}>
          {unsupported ? "能力不支持" : isUnresolved ? "未解决" : "已解决"}
        </StatusText>
      </header>
      <p>
        缺口 {gap.gap_id}，状态 {gap.status}，原因 {gap.reason_code}
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
}

function UnreportedGapRow({
  gapId,
  caseId,
}: {
  gapId: string;
  caseId: string;
}) {
  return (
    <li>
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
  );
}

function GapsSection({
  review,
  caseId,
  unresolved,
  unreportedGapIds,
}: {
  review: AgentReview;
  caseId: string;
  unresolved: Set<string>;
  unreportedGapIds: string[];
}) {
  return (
    <section
      className="agent-review-section"
      aria-labelledby="agent-review-gaps-title"
    >
      <h4 id="agent-review-gaps-title">未解决缺口与下一步</h4>
      {review.gaps.length === 0 && unreportedGapIds.length === 0 ? (
        <p>没有报告缺口记录；这不等同于合同没有风险。</p>
      ) : (
        <ul className="agent-review-list">
          {review.gaps.map((gap) => (
            <GapRow
              key={gap.gap_id}
              gap={gap}
              unresolved={unresolved}
              caseId={caseId}
            />
          ))}
          {unreportedGapIds.map((gapId) => (
            <UnreportedGapRow key={gapId} gapId={gapId} caseId={caseId} />
          ))}
        </ul>
      )}
      <p className="meta">
        补充材料后，需要发起新的审查；本页仍显示当前这次执行使用的材料和结果。
      </p>
    </section>
  );
}

function PlanCallRow({ call }: { call: AgentReviewPlan["calls"][number] }) {
  return (
    <li>
      <header>
        <strong>
          {call.tool_id} v{call.tool_version}
        </strong>
        <StatusText tone={callTone(call.status)}>{call.status}</StatusText>
      </header>
      <p>
        {DOMAIN_LABELS[call.domain]}，{call.capability_id}，缺口 {call.gap_id}
      </p>
      <p>
        守卫/工具状态 {call.status}，原因{" "}
        {call.reason_code === null ? "无已报告原因" : call.reason_code}，证据 ID{" "}
        {showIds(call.evidence_ids)}
      </p>
    </li>
  );
}

function PlanRoundRow({ round }: { round: AgentReviewPlan["rounds"][number] }) {
  return (
    <li>
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
  );
}

function PlanSection({
  plan,
  events,
}: {
  plan: AgentReviewPlan;
  events: AgentReviewEvent[];
}) {
  return (
    <section
      className="agent-review-section"
      aria-labelledby="agent-review-plan-title"
    >
      <h4 id="agent-review-plan-title">计划、守卫与调用</h4>
      <p>
        计划状态：
        <StatusText tone={planTone(plan.status)}>{plan.status}</StatusText>
        {plan.rejection_code ? `，拒绝原因 ${plan.rejection_code}` : ""}
      </p>
      {plan.calls.length === 0 ? (
        <p>没有计划调用记录；不把缺失调用解释为检查通过。</p>
      ) : (
        <ul className="agent-review-list">
          {plan.calls.map((call) => (
            <PlanCallRow key={call.call_id} call={call} />
          ))}
        </ul>
      )}
      {plan.rounds.length > 0 ? (
        <ol className="agent-review-list">
          {plan.rounds.map((round) => (
            <PlanRoundRow key={round.round} round={round} />
          ))}
        </ol>
      ) : (
        <p>没有计划轮次；运行保持未报告状态。</p>
      )}
      {events.length > 0 ? (
        <p className="meta">
          规划事件：
          {events
            .map(
              (item) =>
                `${item.sequence}:${eventRoundLabel(item.round)}:${item.kind}/${item.code}`,
            )
            .join("，")}
        </p>
      ) : (
        <p className="meta">没有保存计划制定过程的事件。</p>
      )}
    </section>
  );
}

function EvidenceItem({ item }: { item: AgentReviewEvidence }) {
  return (
    <li>
      <header>
        <strong>
          {item.semantic_type}: {item.value}
          {item.unit ? ` ${item.unit}` : ""}
          {item.currency ? `，${item.currency}` : ""}
        </strong>
        <StatusText tone={item.anchor.status === "located" ? "neutral" : "attention"}>
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
        {item.anchor.element_id ? `，元素 ${item.anchor.element_id}` : ""}
      </p>
      <blockquote>{item.anchor.quote}</blockquote>
    </li>
  );
}

function EvidenceSection({ evidence }: { evidence: AgentReviewEvidence[] }) {
  return (
    <section
      className="agent-review-section"
      aria-labelledby="agent-review-evidence-title"
    >
      <h4 id="agent-review-evidence-title">经核验的新证据</h4>
      {evidence.length === 0 ? (
        <p>没有返回新提取的事实；结果为空时仍需核实，不能视为没有风险。</p>
      ) : (
        <ul className="agent-review-list">
          {evidence.map((item) => (
            <EvidenceItem key={item.evidence_id} item={item} />
          ))}
        </ul>
      )}
    </section>
  );
}

function UsageSection({ usage }: { usage: AgentReview["usage"] }) {
  return (
    <section
      className="agent-review-section"
      aria-labelledby="agent-review-usage-title"
    >
      <h4 id="agent-review-usage-title">规划用量</h4>
      <dl className="agent-review-grid">
        <div>
          <dt>规划器调用</dt>
          <dd>{usage.planner_calls.toLocaleString("zh-CN")}</dd>
        </div>
        <div>
          <dt>输入 token</dt>
          <dd>{nullableCount(usage.input_tokens)}</dd>
        </div>
        <div>
          <dt>输出 token</dt>
          <dd>{nullableCount(usage.output_tokens)}</dd>
        </div>
        <div>
          <dt>合计 token</dt>
          <dd>{nullableCount(usage.total_tokens)}</dd>
        </div>
      </dl>
    </section>
  );
}

function unreportedGapIdsOf(review: AgentReview): string[] {
  const reportedGapIds = new Set(review.gaps.map((gap) => gap.gap_id));
  return [...new Set(review.unresolved_gap_ids)].filter(
    (gapId) => !reportedGapIds.has(gapId),
  );
}

function modelRefsOf(events: ExecutionEvent[]): string[] {
  return [
    ...new Set(
      events
        .map((event) => event.model_ref)
        .filter((value): value is string => value !== null),
    ),
  ];
}

function modelSourcesOf(events: ExecutionEvent[]): string[] {
  return [
    ...new Set(
      events
        .map((event) => (event.usage === null ? null : event.usage.source))
        .filter((value): value is string => value !== null),
    ),
  ];
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
  if (!agentReview) return <MissingReview />;
  if (agentReview.attempt_id !== attemptId) return <MismatchedReview />;

  const unresolved = new Set(agentReview.unresolved_gap_ids);
  return (
    <section className="agent-review-panel" aria-labelledby="agent-review-title">
      <InspectorHeader review={agentReview} />
      <ConfigurationSection
        review={agentReview}
        modelRefs={modelRefsOf(executionEvents)}
        modelSources={modelSourcesOf(executionEvents)}
      />
      <KnowledgeSection selection={agentReview.knowledge_selection} />
      <ScopeSection review={agentReview} />
      <GapsSection
        review={agentReview}
        caseId={caseId}
        unresolved={unresolved}
        unreportedGapIds={unreportedGapIdsOf(agentReview)}
      />
      <PlanSection plan={agentReview.plan} events={agentReview.events} />
      <EvidenceSection evidence={agentReview.evidence} />
      <UsageSection usage={agentReview.usage} />
    </section>
  );
}
