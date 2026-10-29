import type {
  BusinessAgentCapability,
  BusinessAgentDomain,
  BusinessAgentEvidence,
  BusinessAgentGap,
  BusinessAgentReview,
  BusinessAgentScope,
} from "../api/business-agent-review";
import { StatusText } from "./status-text";

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

const REASON_LABELS: Record<string, string> = {
  required_source_or_field_missing: "缺少必要材料或字段",
  source_fact_only_not_a_decision: "仅提取了来源事实，尚无审查结论",
  capability_not_registered: "当前能力尚不支持此项检查",
  claim_requires_semantic_verification: "来源内容的含义仍需核对",
  approval_period_dates_unverified: "审批有效期所需日期尚未核验",
  stable_party_role_linkage_missing: "主体与角色的关联尚未核验",
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
  if (!item) return <span>关联引用详情不可用。</span>;
  if (item.verification === "rejected") return <span>引用未通过核验。</span>;
  if (!isVerifiedCitation(item)) {
    return <span>引用尚未核验。</span>;
  }
  if (!anchorIdentityComplete(item) || !anchorRangeValid(item)) {
    return <span>核验引用的信息不完整，需进一步核对。</span>;
  }
  return (
    <>
      <p>来源材料中的引用已核验{citationPage(item)}</p>
      <blockquote>{item.anchor.quote}</blockquote>
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
    <>
      <header><strong>{capabilityLabel(id)}</strong><StatusText tone="attention">结果未知</StatusText></header>
      <p>是否必查：未知。</p>
    </>
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
      <Citations ids={gap.evidence_ids} evidence={evidence} />
    </li>
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
    !review.evidence?.length
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
      <p>本次没有返回检查范围、待补充事项和原文依据，不能因此认为已经完成检查。</p>
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
      <DomainsSection
        review={review}
        domains={domains}
        scopesWithoutResults={scopesWithoutResults}
      />
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

export function BusinessAgentReviewView({ review }: { review: BusinessAgentReview | null }) {
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
