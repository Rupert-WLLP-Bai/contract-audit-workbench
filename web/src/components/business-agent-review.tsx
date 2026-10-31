import { Alert, Button } from "antd";
import { createContext, useContext } from "react";
import type {
  BusinessAgentCapability,
  BusinessAgentDomain,
  BusinessAgentEvidence,
  BusinessAgentGap,
  BusinessAgentReview,
  BusinessAgentScope,
} from "../api/business-agent-review";
import { FrozenContractCitationReader } from "./contract-citation-reader";
import { StatusText } from "./status-text";

interface CitationTarget {
  caseId: string;
  runId: string;
  attemptId: string;
  selectedEvidenceId: string | null;
  onSelectEvidence: (evidenceId: string | null) => void;
}

const CitationTargetContext = createContext<CitationTarget | null>(null);

const DOMAIN_LABELS: Record<string, string> = {
  compliance: "合规与授权",
  commercial: "商务与财务",
  technical: "技术与服务",
  fulfillment: "履约与变更",
  counterparty: "主体与关联",
};

const CAPABILITY_LABELS: Record<string, string> = {
  authorization_basis: "授权依据",
  commercial_baseline: "商务条款",
  warranty_months: "质保期限",
  acceptance_schedule: "交付与验收时间",
  counterparty_record: "主体登记信息",
  amount: "金额",
  party: "合同主体",
  approval_authority: "审批或授权",
  approval_validity: "审批有效期",
  effective_scope: "服务范围",
  scope: "技术与服务范围",
  milestone: "履约里程碑",
  explicit_start_event: "启动时间",
  subject_id: "主体标识",
  signing_party: "签署方",
  payee: "收款方",
  responsibility: "责任与义务",
  "dedicated_line.minimum_bandwidth": "最低保障带宽",
  "dedicated_line.monthly_availability": "月可用率",
  "payment.advance_ratio.ceiling": "预付款比例上限",
};

const STATUS_LABELS: Record<string, string> = {
  queued: "等待执行",
  planning: "正在规划",
  running: "执行中",
  completed: "已完成",
  partial: "部分完成",
  blocked_live: "未能完成",
  cancelled: "已取消",
  failed: "执行失败",
  rejected: "未被接受",
  supported: "已支持",
  insufficient: "证据不足",
  unknown: "状态未知",
  unsupported: "暂不支持",
  not_applicable: "不适用",
  not_requested: "未要求",
  open: "未解决",
  resolved: "已解决",
};

const RELATION_LABELS: Record<string, string> = {
  compatible: "比较结果：一致",
  conflict: "比较结果：存在冲突",
  insufficient: "比较结果：证据不足",
  not_applicable: "比较结果：不适用",
};

const OBSERVED_RELATION_LABELS: Record<string, string> = {
  compatible: "一致",
  conflict: "冲突",
  insufficient: "证据不足",
  not_applicable: "不适用",
};

const CLAIM_STATUS_LABELS: Record<string, string> = {
  tool_reported_not_independently_verified: "工具报告，尚未独立核验",
};

const REASON_LABELS: Record<string, string> = {
  required_source_or_field_missing: "缺少必要材料或字段",
  required_authority_source_missing: "缺少授权依据来源",
  authorized_counterparty_source_missing: "缺少经授权的相对方来源",
  approval_validity_interval_missing: "缺少审批有效期区间",
  source_expression_not_supported: "当前工具不支持来源中的表达形式",
  multiple_source_values_require_review: "多个来源值需要人工核对",
  max_calls_reached: "达到本轮调用预算",
  verified_result: "已核验结果",
  source_fact_only_not_a_decision: "仅提取了来源事实，尚无审查结论",
  capability_not_registered: "当前能力尚不支持此项检查",
  claim_requires_semantic_verification: "来源内容的含义仍需核对",
  approval_period_dates_unverified: "审批有效期所需日期尚未核验",
  stable_party_role_linkage_missing: "主体与角色的关联尚未核验",
};

const REASON_NEXT_STEPS: Record<string, string> = {
  required_source_or_field_missing: "补充包含该字段的采购、合同或审批材料后重新审查。",
  required_authority_source_missing: "补充授权依据或审批文件。",
  authorized_counterparty_source_missing: "补充经授权的相对方登记材料。",
  approval_validity_interval_missing: "补充审批生效与失效日期。",
  source_expression_not_supported: "当前规则包与工具无法解析该表述；改用匹配业务的规则包，或由审查人员核对原文后记录判断。",
  multiple_source_values_require_review: "人工比对多个来源值并记录采用哪一个值。",
  max_calls_reached: "规划调用预算已用尽；提高预算或缩小审查范围后重新执行。",
  capability_not_registered: "当前版本没有注册这项能力，不能把“未检查”当作“已通过”。",
  claim_requires_semantic_verification: "回到冻结原文核对语义后再判断。",
  approval_period_dates_unverified: "核对审批日期后再判断。",
  stable_party_role_linkage_missing: "核对主体与角色的对应关系。",
  source_fact_only_not_a_decision: "这仍是来源事实，需要审查人员给出判断。",
};

const BAD_STATUSES = new Set(["failed", "rejected"]);
const PROGRESS_STATUSES = new Set(["queued", "planning", "running"]);
const NEUTRAL_STATUSES = new Set([
  "completed",
  "supported",
  "resolved",
  "not_applicable",
  "not_requested",
]);

function label(labels: Record<string, string>, value: string, fallback: string): string {
  return Object.hasOwn(labels, value) ? labels[value]! : fallback;
}

function arrayOr<T>(value: T[] | null | undefined): T[] {
  return value === null || value === undefined ? [] : value;
}

function gapGuidance(gaps: BusinessAgentGap[]): string {
  const counts = new Map<string, number>();
  for (const gap of gaps) {
    if (gap.reason_code) counts.set(gap.reason_code, (counts.get(gap.reason_code) ?? 0) + 1);
  }
  const ranked = [...counts.entries()]
    .sort((left, right) => right[1] - left[1] || left[0].localeCompare(right[0]))
    .slice(0, 3)
    .map(([code, count]) => `${label(REASON_LABELS, code, "原因待核对")} ${count} 项`);
  const lead = ranked.length > 0
    ? `本次 ${gaps.length} 项缺口中，${ranked.join("、")}。`
    : `本次共 ${gaps.length} 项缺口。`;
  return `${lead}未解决或证据不足表示尚未确认，不等于已通过；按每条缺口给出的下一步处理。`;
}

function domainLabel(value: string): string {
  return label(DOMAIN_LABELS, value, "未识别领域");
}

function capabilityLabel(value: string): string {
  return label(CAPABILITY_LABELS, value, "未识别审查项目");
}

function statusLabel(value: string | null): string {
  return value ? label(STATUS_LABELS, value, "状态未知") : "状态未知";
}

function statusTone(value: string | null): "neutral" | "attention" | "bad" | "progress" {
  if (value !== null && BAD_STATUSES.has(value)) return "bad";
  if (value !== null && PROGRESS_STATUSES.has(value)) return "progress";
  if (value !== null && NEUTRAL_STATUSES.has(value)) return "neutral";
  return "attention";
}

function isVerifiedCitation(item: BusinessAgentEvidence): boolean {
  return (
    item.verification === "verified" ||
    item.verification === "typed_semantic_and_frozen_quote_verified"
  );
}

function anchorIdentityComplete(item: BusinessAgentEvidence): boolean {
  const anchor = item.anchor;
  return (
    anchor.status === "located" &&
    anchor.quote.trim() !== "" &&
    anchor.document_id.trim() !== "" &&
    anchor.parsed_document_id.trim() !== ""
  );
}

function anchorRangeValid(item: BusinessAgentEvidence): boolean {
  return item.anchor.start >= 0 && item.anchor.end > item.anchor.start;
}

function citationPage(item: BusinessAgentEvidence): string {
  const page = item.anchor.page;
  return page !== null && page > 0 ? `，第 ${page} 页` : "";
}

function Citation({ item }: { item: BusinessAgentEvidence | undefined }) {
  const target = useContext(CitationTargetContext);
  if (!item) return <span>关联引用详情不可用。</span>;
  if (item.verification === "rejected") return <span>引用未通过核验。</span>;
  if (!isVerifiedCitation(item)) {
    return <span>引用尚未核验。</span>;
  }
  if (!anchorIdentityComplete(item) || !anchorRangeValid(item)) {
    return <span>核验引用的信息不完整，需进一步核对。</span>;
  }
  const reading = target !== null && target.selectedEvidenceId === item.evidence_id;
  return (
    <>
      <p>来源材料中的引用已核验{citationPage(item)}</p>
      <blockquote>{item.anchor.quote}</blockquote>
      {target === null ? null : (
        <Button
          type="link"
          size="small"
          aria-expanded={reading}
          onClick={() => target.onSelectEvidence(reading ? null : item.evidence_id)}
        >
          {reading ? "收起本次冻结原文" : "读取本次冻结原文"}
        </Button>
      )}
      {target !== null && reading ? (
        <FrozenContractCitationReader
          caseId={target.caseId}
          runId={target.runId}
          attemptId={target.attemptId}
          evidenceId={item.evidence_id}
          expected={item}
        />
      ) : null}
    </>
  );
}

function Citations({ ids, evidence }: { ids: string[] | null; evidence: BusinessAgentEvidence[] | null }) {
  if (!ids || ids.length === 0) return null;
  return (
    <ul className="agent-review-capability-citations" aria-label="来源引用">
      {ids.map((id, index) => (
        <li key={`${id}-${index}`}><Citation item={evidence === null ? undefined : evidence.find((item) => item.evidence_id === id)} /></li>
      ))}
    </ul>
  );
}

function missingCapabilityIds(
  review: BusinessAgentReview,
  domain: BusinessAgentDomain,
): string[] {
  const capabilities = arrayOr(domain.capabilities);
  return [...new Set(arrayOr(review.scope)
    .filter((scope) => scope.domain === domain.domain)
    .flatMap((scope) => arrayOr(scope.capability_ids))
    .filter((id) => !capabilities.some((capability) => capability.capability_id === id)))];
}

function CapabilityRow({
  capability,
  evidence,
}: {
  capability: BusinessAgentCapability;
  evidence: BusinessAgentEvidence[] | null;
}) {
  return (
    <>
      <header>
        <strong>{capabilityLabel(capability.capability_id)}</strong>
        <StatusText tone={statusTone(capability.status)}>{statusLabel(capability.status)}</StatusText>
      </header>
      <p>{capability.mandatory ? "必查项目" : "非必查项目"}；{capability.relation === null ? "比较结果未知" : label(RELATION_LABELS, capability.relation, "比较结果未知")}。</p>
      {capability.reason_code ? <p>{label(REASON_LABELS, capability.reason_code, "原因待进一步核对")}。</p> : null}
      <Citations ids={capability.evidence_ids} evidence={evidence} />
    </>
  );
}

function MissingCapabilityRow({ id }: { id: string }) {
  return (
    <li>
      <header><strong>{capabilityLabel(id)}</strong><StatusText tone="attention">结果未知</StatusText></header>
      <p>是否必查：未知。</p>
    </li>
  );
}

function DomainCard({
  domain,
  missingCapabilities,
  evidence,
}: {
  domain: BusinessAgentDomain;
  missingCapabilities: string[];
  evidence: BusinessAgentEvidence[] | null;
}) {
  const capabilities = arrayOr(domain.capabilities);
  return (
    <li className="agent-review-card">
      <header>
        <strong>{domainLabel(domain.domain)}</strong>
        <StatusText tone={statusTone(domain.status)}>{statusLabel(domain.status)}</StatusText>
      </header>
      {capabilities.length === 0 && missingCapabilities.length === 0 ? (
        <p>暂无检查明细。</p>
      ) : (
        <ul className="agent-review-capabilities">
          {capabilities.map((capability, index) => (
            <li key={`${capability.capability_id}-${index}`}>
              <CapabilityRow capability={capability} evidence={evidence} />
            </li>
          ))}
          {missingCapabilities.map((id, index) => (
            <MissingCapabilityRow key={`missing-${id}-${index}`} id={id} />
          ))}
        </ul>
      )}
    </li>
  );
}

function ScopeOnlyCard({ scope }: { scope: BusinessAgentScope }) {
  return (
    <li className="agent-review-card">
      <header><strong>{domainLabel(scope.domain)}</strong><StatusText tone="attention">结果未知</StatusText></header>
      <p>该领域已列入范围，检查结果和必查状态未知。</p>
      {scope.capability_ids && scope.capability_ids.length > 0 ? (
        <p>范围中的项目：{scope.capability_ids.map(capabilityLabel).join("、")}。</p>
      ) : null}
    </li>
  );
}

function gapLabel(gap: BusinessAgentGap, reportedUnresolved: boolean): string {
  if (reportedUnresolved && gap.status === "resolved") return "未解决（状态需核对）";
  if (reportedUnresolved) return "未解决";
  if (gap.status === "completed") return "已处理，解决状态待核对";
  return statusLabel(gap.status);
}

function gapTone(
  gap: BusinessAgentGap,
  reportedUnresolved: boolean,
): "neutral" | "attention" | "bad" | "progress" {
  return reportedUnresolved || gap.status === "completed"
    ? "attention"
    : statusTone(gap.status);
}

function GapRow({
  gap,
  unresolved,
  evidence,
}: {
  gap: BusinessAgentGap;
  unresolved: Set<string>;
  evidence: BusinessAgentEvidence[] | null;
}) {
  const reportedUnresolved = unresolved.has(gap.gap_id);
  return (
    <li>
      <header>
        <strong>{domainLabel(gap.domain)}：{capabilityLabel(gap.capability_id)}</strong>
        <StatusText tone={gapTone(gap, reportedUnresolved)}>{gapLabel(gap, reportedUnresolved)}</StatusText>
      </header>
      <p>{gap.mandatory ? "必查项目" : "非必查项目"}{gap.reason_code ? `；${label(REASON_LABELS, gap.reason_code, "原因待进一步核对")}` : ""}。</p>
      {gap.reason_code && REASON_NEXT_STEPS[gap.reason_code] ? (
        <p className="meta">下一步：{REASON_NEXT_STEPS[gap.reason_code]}</p>
      ) : null}
      <Citations ids={gap.evidence_ids} evidence={evidence} />
    </li>
  );
}

function ObservationsSection({ observations }: { observations: BusinessAgentReview["observations"] }) {
  if (!observations || observations.length === 0) return null;
  return (
    <section className="agent-review-section" aria-label="工具返回记录">
      <h4>工具返回记录</h4>
      <p className="meta">工具返回是待核验的记录，不是已确认结论；只有引用能对上冻结原文时才可能进入核验。</p>
      <ul className="agent-review-list">
        {observations.map((observation, index) => (
          <li key={`${observation.call_id}-${index}`}>
            <header>
              <strong>{observation.tool_id} · 缺口 {observation.gap_id}</strong>
              <StatusText tone="attention">
                {label(CLAIM_STATUS_LABELS, observation.claim_status, "工具报告的记录，尚未独立核验")}
              </StatusText>
            </header>
            <p>
              第 {observation.round} 轮；报告关系 {label(OBSERVED_RELATION_LABELS, observation.reported_relation, "未识别")}
              {observation.reason_code ? `；${label(REASON_LABELS, observation.reason_code, "原因待进一步核对")}` : ""}。
            </p>
            <p>{observation.summary}</p>
            {observation.located_evidence_ids && observation.located_evidence_ids.length > 0 ? (
              <p className="meta">已定位证据：{observation.located_evidence_ids.join("、")}</p>
            ) : null}
            {observation.truncated ? <p className="meta">该工具返回已被截断。</p> : null}
          </li>
        ))}
      </ul>
    </section>
  );
}

function GapList({
  gaps,
  unresolved,
  evidence,
}: {
  gaps: BusinessAgentGap[];
  unresolved: Set<string>;
  evidence: BusinessAgentEvidence[] | null;
}) {
  return (
    <ul className="agent-review-list">
      {gaps.map((gap, index) => (
        <GapRow key={`${gap.gap_id}-${index}`} gap={gap} unresolved={unresolved} evidence={evidence} />
      ))}
    </ul>
  );
}

function OtherEvidenceSection({ items }: { items: BusinessAgentEvidence[] }) {
  return (
    <section className="agent-review-section" aria-label="其他来源引用">
      <h4>其他来源引用</h4>
      <ul className="agent-review-list">
        {items.map((item, index) => <li key={`${item.evidence_id}-${index}`}><Citation item={item} /></li>)}
      </ul>
    </section>
  );
}

function referencedEvidenceOf(
  domains: BusinessAgentDomain[],
  gaps: BusinessAgentGap[] | null,
): Set<string> {
  return new Set([
    ...domains.flatMap((domain) => arrayOr(domain.capabilities).flatMap((capability) => arrayOr(capability.evidence_ids))),
    ...arrayOr(gaps).flatMap((gap) => arrayOr(gap.evidence_ids)),
  ]);
}

function hasMissingCitations(
  domains: BusinessAgentDomain[],
  gaps: BusinessAgentGap[] | null,
): boolean {
  return domains.some((domain) => arrayOr(domain.capabilities).some((capability) => !arrayOr(capability.evidence_ids).length)) ||
    arrayOr(gaps).some((gap) => !arrayOr(gap.evidence_ids).length);
}

function scopesWithoutResultsOf(
  scope: BusinessAgentScope[] | null,
  domains: BusinessAgentDomain[],
): BusinessAgentScope[] {
  return arrayOr(scope).filter((item) => !domains.some((domain) => domain.domain === item.domain));
}

function isEmptyReview(
  review: BusinessAgentReview,
  domains: BusinessAgentDomain[],
  scopesWithoutResults: BusinessAgentScope[],
): boolean {
  return (
    domains.length === 0 &&
    scopesWithoutResults.length === 0 &&
    !review.gaps?.length &&
    arrayOr(review.unresolved_gap_ids).length === 0 &&
    !review.evidence?.length &&
    !review.observations?.length
  );
}

function EmptyReview() {
  return (
    <section className="agent-review-panel" aria-label="五个方面的审查结果">
      <h3>五个方面的审查结果</h3>
      <p>本次没有返回五个方面的审查结果，不能因此认为已经全部检查。</p>
    </section>
  );
}

function EmptyReviewWithStatus({ review }: { review: BusinessAgentReview }) {
  return (
    <section className="agent-review-panel" aria-label="五个方面的审查结果">
      <header className="agent-review-head">
        <h3>五个方面的审查结果</h3>
        <StatusText tone={statusTone(review.status)}>{statusLabel(review.status)}</StatusText>
      </header>
      <p>本次没有返回检查范围、待补充事项、原文依据或工具记录，不能因此认为已经完成检查。</p>
    </section>
  );
}

function reviewFacts(review: BusinessAgentReview, domains: BusinessAgentDomain[]) {
  const unresolved = new Set(arrayOr(review.unresolved_gap_ids));
  const gaps = arrayOr(review.gaps);
  const reportedGaps = new Set(gaps.map((gap) => gap.gap_id));
  const missingGaps = [...unresolved].filter((id) => !reportedGaps.has(id));
  const referencedEvidence = referencedEvidenceOf(domains, review.gaps);
  const otherEvidence = arrayOr(review.evidence).filter(
    (item) => !referencedEvidence.has(item.evidence_id),
  );
  return {
    unresolved,
    gaps,
    missingGaps,
    otherEvidence,
    missingCitations: hasMissingCitations(domains, review.gaps),
  };
}

function DomainsSection({
  review,
  domains,
  scopesWithoutResults,
}: {
  review: BusinessAgentReview;
  domains: BusinessAgentDomain[];
  scopesWithoutResults: BusinessAgentScope[];
}) {
  return (
    <section className="agent-review-section" aria-label="领域与检查项目">
      <h4>领域与检查项目</h4>
      {domains.length === 0 && scopesWithoutResults.length === 0 ? (
        <p>没有返回各方面的审查结果，不能因此认为已经完成检查。</p>
      ) : (
        <ul className="agent-review-grid-list">
          {domains.map((domain, domainIndex) => (
            <DomainCard
              key={`${domain.domain}-${domainIndex}`}
              domain={domain}
              missingCapabilities={missingCapabilityIds(review, domain)}
              evidence={review.evidence}
            />
          ))}
          {scopesWithoutResults.map((scope, index) => (
            <ScopeOnlyCard key={`scope-${scope.domain}-${index}`} scope={scope} />
          ))}
        </ul>
      )}
    </section>
  );
}

function GapsSection({
  review,
  unresolved,
  gaps,
  missingGaps,
}: {
  review: BusinessAgentReview;
  unresolved: Set<string>;
  gaps: BusinessAgentGap[];
  missingGaps: string[];
}) {
  return (
    <section className="agent-review-section" aria-label="审查缺口">
      <h4>审查缺口</h4>
      {gaps.length === 0 ? (
        <p>暂无缺口资料；这不等于不存在风险。</p>
      ) : (
        <GapList gaps={gaps} unresolved={unresolved} evidence={review.evidence} />
      )}
      {review.unresolved_gap_ids === null && gaps.length ? <p>缺口的解决清单不可用，请核对。</p> : null}
      {missingGaps.length > 0 ? <p>有 {missingGaps.length} 项未解决缺口缺少详情，请进一步核对。</p> : null}
    </section>
  );
}

function ReviewSections({
  review,
  domains,
  scopesWithoutResults,
}: {
  review: BusinessAgentReview;
  domains: BusinessAgentDomain[];
  scopesWithoutResults: BusinessAgentScope[];
}) {
  const facts = reviewFacts(review, domains);
  return (
    <section className="agent-review-panel" aria-label="五个方面的审查结果">
      <header className="agent-review-head">
        <h3>五个方面的审查结果</h3>
        <StatusText tone={statusTone(review.status)}>{statusLabel(review.status)}</StatusText>
      </header>
      <p className="meta">流程完成或找到原文依据，并不代表风险已确认或合同已合规；仍需审查人员判断。</p>
      {facts.missingCitations ? <p className="meta">部分项目没有原文依据，不能确认这些项目已经核验。</p> : null}
      {facts.gaps.length > 0 ? (
        <p className="meta">{gapGuidance(facts.gaps)}</p>
      ) : null}
      <DomainsSection
        review={review}
        domains={domains}
        scopesWithoutResults={scopesWithoutResults}
      />
      <ObservationsSection observations={review.observations} />
      <GapsSection
        review={review}
        unresolved={facts.unresolved}
        gaps={facts.gaps}
        missingGaps={facts.missingGaps}
      />
      {facts.otherEvidence.length > 0 ? (
        <OtherEvidenceSection items={facts.otherEvidence} />
      ) : null}
    </section>
  );
}

function BusinessAgentReviewBody({ review }: { review: BusinessAgentReview | null }) {
  if (!review) return <EmptyReview />;
  const domains = arrayOr(review.domains);
  const scopesWithoutResults = scopesWithoutResultsOf(review.scope, domains);
  if (isEmptyReview(review, domains, scopesWithoutResults)) {
    return <EmptyReviewWithStatus review={review} />;
  }
  return (
    <ReviewSections
      review={review}
      domains={domains}
      scopesWithoutResults={scopesWithoutResults}
    />
  );
}

/**
 * Business agent review projection. When the page supplies the exact case,
 * run and attempt, every verified citation also opens its frozen text from
 * that attempt; the reader stays closed when the caller has no attempt scope.
 */
export function BusinessAgentReviewView({
  review,
  caseId,
  runId,
  attemptId,
  selectedEvidenceId = null,
  onSelectEvidence,
}: {
  review: BusinessAgentReview | null;
  caseId?: string;
  runId?: string;
  attemptId?: string | null;
  selectedEvidenceId?: string | null;
  onSelectEvidence?: (evidenceId: string | null) => void;
}) {
  const target: CitationTarget | null =
    caseId && runId && attemptId && onSelectEvidence
      ? { caseId, runId, attemptId, selectedEvidenceId, onSelectEvidence }
      : null;
  const citationUnknown = target !== null && selectedEvidenceId !== null && review !== null &&
    !arrayOr(review.evidence).some((item) => item.evidence_id === selectedEvidenceId);
  return (
    <CitationTargetContext.Provider value={target}>
      {citationUnknown ? (
        <Alert
          type="warning"
          showIcon
          title="URL 中的引用不属于当前执行记录"
          description="未显示其他执行记录的原文；请从当前执行记录的引用重新打开。"
        />
      ) : null}
      <BusinessAgentReviewBody review={review} />
    </CitationTargetContext.Provider>
  );
}
