import type { BusinessAgentEvidence, BusinessAgentReview } from "../api/business-agent-review";
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
  claim_requires_semantic_verification: "来源事实仍需语义核验",
  approval_period_dates_unverified: "审批有效期所需日期尚未核验",
  stable_party_role_linkage_missing: "主体与角色的关联尚未核验",
};

function label(labels: Record<string, string>, value: string, fallback: string): string {
  return Object.hasOwn(labels, value) ? labels[value] ?? fallback : fallback;
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
  if (value === "failed" || value === "rejected") return "bad";
  if (value === "queued" || value === "planning" || value === "running") return "progress";
  if (value === "completed" || value === "supported" || value === "resolved" || value === "not_applicable" || value === "not_requested") return "neutral";
  return "attention";
}

function Citation({ item }: { item: BusinessAgentEvidence | undefined }) {
  if (!item) return <span>关联引用详情不可用。</span>;
  if (item.verification === "rejected") return <span>引用未通过核验。</span>;
  if (item.verification !== "verified" && item.verification !== "typed_semantic_and_frozen_quote_verified") {
    return <span>引用尚未核验。</span>;
  }
  const anchor = item.anchor;
  if (anchor.status !== "located" || !anchor.quote.trim() || !anchor.document_id.trim() || !anchor.parsed_document_id.trim() || anchor.start < 0 || anchor.end <= anchor.start) {
    return <span>核验引用的信息不完整，需进一步核对。</span>;
  }
  return (
    <>
      <p>来源材料中的引用已核验{anchor.page !== null && anchor.page > 0 ? `，第 ${anchor.page} 页` : ""}</p>
      <blockquote>{anchor.quote}</blockquote>
    </>
  );
}

function Citations({ ids, evidence }: { ids: string[] | null; evidence: BusinessAgentEvidence[] | null }) {
  if (!ids || ids.length === 0) return null;
  return (
    <ul className="agent-review-capability-citations" aria-label="来源引用">
      {ids.map((id, index) => (
        <li key={`${id}-${index}`}><Citation item={evidence?.find((item) => item.evidence_id === id)} /></li>
      ))}
    </ul>
  );
}

export function BusinessAgentReviewView({ review }: { review: BusinessAgentReview | null }) {
  if (!review) {
    return (
      <section className="agent-review-panel" aria-label="五域审查结果">
        <h3>五域审查结果</h3>
        <p>本次暂无五域结果，不能据此推断已覆盖。</p>
      </section>
    );
  }

  const domains = review.domains ?? [];
  const scopesWithoutResults = (review.scope ?? []).filter((scope) => !domains.some((domain) => domain.domain === scope.domain));
  const unresolved = new Set(review.unresolved_gap_ids ?? []);
  const reportedGaps = new Set((review.gaps ?? []).map((gap) => gap.gap_id));
  const missingGaps = [...unresolved].filter((id) => !reportedGaps.has(id));
  const referencedEvidence = new Set([
    ...domains.flatMap((domain) => (domain.capabilities ?? []).flatMap((capability) => capability.evidence_ids ?? [])),
    ...(review.gaps ?? []).flatMap((gap) => gap.evidence_ids ?? []),
  ]);
  const otherEvidence = (review.evidence ?? []).filter((item) => !referencedEvidence.has(item.evidence_id));
  const missingCitations = domains.some((domain) => (domain.capabilities ?? []).some((capability) => !capability.evidence_ids?.length)) ||
    (review.gaps ?? []).some((gap) => !gap.evidence_ids?.length);
  if (domains.length === 0 && scopesWithoutResults.length === 0 && !review.gaps?.length && unresolved.size === 0 && !review.evidence?.length) {
    return (
      <section className="agent-review-panel" aria-label="五域审查结果">
        <header className="agent-review-head">
          <h3>五域审查结果</h3>
          <StatusText tone={statusTone(review.status)}>{statusLabel(review.status)}</StatusText>
        </header>
        <p>本次暂无领域、缺口和引用信息，不能据此推断已覆盖。</p>
      </section>
    );
  }

  return (
    <section className="agent-review-panel" aria-label="五域审查结果">
      <header className="agent-review-head">
        <h3>五域审查结果</h3>
        <StatusText tone={statusTone(review.status)}>{statusLabel(review.status)}</StatusText>
      </header>
      <p className="meta">流程状态和来源引用核验不等于风险已确认或合同合规，仍需人工判断。</p>
      {missingCitations ? <p className="meta">部分项目暂无引用，不能据此确认已核验。</p> : null}

      <section className="agent-review-section" aria-label="领域与检查项目">
        <h4>领域与检查项目</h4>
        {domains.length === 0 && scopesWithoutResults.length === 0 ? (
          <p>暂无领域审查结果，不能据此推断已覆盖。</p>
        ) : (
          <ul className="agent-review-grid-list">
            {domains.map((domain, domainIndex) => {
              const capabilities = domain.capabilities ?? [];
              const missingCapabilities = [...new Set((review.scope ?? [])
                .filter((scope) => scope.domain === domain.domain)
                .flatMap((scope) => scope.capability_ids ?? [])
                .filter((id) => !capabilities.some((capability) => capability.capability_id === id)))];
              return (
                <li className="agent-review-card" key={`${domain.domain}-${domainIndex}`}>
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
                          <header>
                            <strong>{capabilityLabel(capability.capability_id)}</strong>
                            <StatusText tone={statusTone(capability.status)}>{statusLabel(capability.status)}</StatusText>
                          </header>
                          <p>{capability.mandatory ? "必查项目" : "非必查项目"}；{capability.relation === null ? "比较结果未知" : label(RELATION_LABELS, capability.relation, "比较结果未知")}。</p>
                          {capability.reason_code ? <p>{label(REASON_LABELS, capability.reason_code, "原因待进一步核对")}。</p> : null}
                          <Citations ids={capability.evidence_ids} evidence={review.evidence} />
                        </li>
                      ))}
                      {missingCapabilities.map((id, index) => (
                        <li key={`missing-${id}-${index}`}>
                          <header><strong>{capabilityLabel(id)}</strong><StatusText tone="attention">结果未知</StatusText></header>
                          <p>是否必查：未知。</p>
                        </li>
                      ))}
                    </ul>
                  )}
                </li>
              );
            })}
            {scopesWithoutResults.map((scope, index) => (
              <li className="agent-review-card" key={`scope-${scope.domain}-${index}`}>
                <header><strong>{domainLabel(scope.domain)}</strong><StatusText tone="attention">结果未知</StatusText></header>
                <p>该领域已列入范围，检查结果和必查状态未知。</p>
                {scope.capability_ids && scope.capability_ids.length > 0 ? (
                  <p>范围中的项目：{scope.capability_ids.map(capabilityLabel).join("、")}。</p>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="agent-review-section" aria-label="审查缺口">
        <h4>审查缺口</h4>
        {review.gaps === null || review.gaps.length === 0 ? (
          <p>暂无缺口资料；这不等于不存在风险。</p>
        ) : (
          <ul className="agent-review-list">
            {review.gaps.map((gap, index) => {
              const reportedUnresolved = unresolved.has(gap.gap_id);
              const gapLabel = reportedUnresolved && gap.status === "resolved" ? "未解决（状态需核对）" : reportedUnresolved ? "未解决" : gap.status === "completed" ? "已处理，解决状态待核对" : statusLabel(gap.status);
              return (
                <li key={`${gap.gap_id}-${index}`}>
                  <header>
                    <strong>{domainLabel(gap.domain)}：{capabilityLabel(gap.capability_id)}</strong>
                    <StatusText tone={reportedUnresolved || gap.status === "completed" ? "attention" : statusTone(gap.status)}>{gapLabel}</StatusText>
                  </header>
                  <p>{gap.mandatory ? "必查项目" : "非必查项目"}{gap.reason_code ? `；${label(REASON_LABELS, gap.reason_code, "原因待进一步核对")}` : ""}。</p>
                  <Citations ids={gap.evidence_ids} evidence={review.evidence} />
                </li>
              );
            })}
          </ul>
        )}
        {review.unresolved_gap_ids === null && review.gaps?.length ? <p>缺口的解决清单不可用，请核对。</p> : null}
        {missingGaps.length > 0 ? <p>有 {missingGaps.length} 项未解决缺口缺少详情，请进一步核对。</p> : null}
      </section>

      {otherEvidence.length > 0 ? (
        <section className="agent-review-section" aria-label="其他来源引用">
          <h4>其他来源引用</h4>
          <ul className="agent-review-list">
            {otherEvidence.map((item, index) => <li key={`${item.evidence_id}-${index}`}><Citation item={item} /></li>)}
          </ul>
        </section>
      ) : null}
    </section>
  );
}
