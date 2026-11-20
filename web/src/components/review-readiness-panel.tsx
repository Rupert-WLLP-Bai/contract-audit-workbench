// File metadata (manifest mirror):
// Purpose: Presents scope readiness and its source, rule and context prerequisites without authorizing review or reducing frozen-input checks.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-20
// Revision: r8
// Copyright: See LICENSE
// End file metadata.

import { Button, Radio, Tag } from "antd";
import type { ReactNode } from "react";
import type {
  ReviewReadiness,
  ReviewReadinessBlocker,
  ReviewReadinessBusinessScope,
  ReviewReadinessScope,
  ReviewScope,
} from "../api/review-readiness";
import { errorMessage } from "../api/http";
import { knownBusinessComparatorLabel, readinessCheckActionLabel } from "../domain/business-labels";
import { roleLabel } from "../domain/review";
import { singleContractVersion } from "../features/review/single-contract-coverage";
import { SingleContractScopeCoverage } from "./single-contract-scope-coverage";
import { TechnicalDetail, type TechnicalField, TechnicalFieldGroup } from "./technical-detail";
import "./review-readiness-panel.css";

type Repair = { href: string; label: string };
type Check = ReviewReadinessScope["checks"][number];

const materialsLink = { href: "#case-materials", label: "补齐或确认材料" };
const businessTimeLink = { href: "#review-business-time", label: "填写业务时间" };
const legalLink = (caseId: string) => ({
  href: `/cases/${encodeURIComponent(caseId)}/legal-knowledge`,
  label: "核对法律依据与制度",
});
const checkLabels: Record<string, string> = {
  "single-payment-sum-v1": "主合同中可识别的付款比例合计",
  "single-payment-terms-v2": "付款与结算原文定位",
  rule_pack: "规则包",
  cross_material_comparison: "跨材料对照",
  formal_policy_review: "正式制度与法律依据审查",
};

function governanceURL(): string {
  const base = import.meta.env.VITE_ADMIN_URL;
  return base ? new URL("/admin/rulepacks", base).href : "/admin/rulepacks";
}

function dependencyLabel(dependency: string): string {
  const [kind, ...parts] = dependency.split(":");
  const value = parts.join(":");
  if (kind === "business_at") return "尚未填写业务时间";
  if (kind === "review_materials") return "缺少送审材料";
  if (kind === "material_role") return `缺少${roleLabel(value)}`;
  if (kind === "parsed_material_role") return `${roleLabel(value)}等待解析`;
  if (kind === "failed_or_stale_parse_role") return `${roleLabel(value)}需要重新解析`;
  if (kind === "legal_authority" || kind === "exact_legal_authority_gate") return "适用依据待确认";
  if (kind === "comparison_materials") return "缺少采购、审批等对照材料";
  if (kind === "formal_policy_review") return "正式制度与法律依据尚未审查";
  if (kind === "valid_rule_pack" || kind === "published_rule_pack") return "核对已发布规则包";
  if (kind === "required_governed_knowledge") return "核对适用制度";
  return "需要核对所需材料与依据";
}

function dependencyRepair(caseId: string, dependencies: string[], action: string): Repair | null {
  if (action === "reparse_material") return { ...materialsLink, label: "到材料列表重新解析" };
  if (
    dependencies.some((value) =>
      [
        "material_role",
        "parsed_material_role",
        "failed_or_stale_parse_role",
        "comparison_materials",
      ].includes(value.split(":")[0]!),
    )
  )
    return materialsLink;
  if (
    dependencies.some(
      (value) =>
        value.startsWith("legal_authority:") ||
        value === "exact_legal_authority_gate" ||
        value === "formal_policy_review" ||
        value === "required_governed_knowledge",
    )
  )
    return legalLink(caseId);
  if (dependencies.some((value) => value === "published_rule_pack" || value === "valid_rule_pack"))
    return { href: governanceURL(), label: "核对规则包" };
  return null;
}

function blockerRepair(caseId: string, blocker: ReviewReadinessBlocker): Repair | null {
  if (blocker.code === "business_at_required") return businessTimeLink;
  if (
    [
      "required_materials_missing",
      "material_selection_pending",
      "primary_contract_required",
      "primary_contract_ambiguous",
    ].includes(blocker.code)
  )
    return materialsLink;
  if (blocker.code === "case_not_writable")
    return { href: "#case-business-title", label: "查看案件状态" };
  return (
    dependencyRepair(caseId, blocker.missing_dependencies, blocker.next_action) ??
    (blocker.code === "governed_dependencies_incomplete" ? legalLink(caseId) : null)
  );
}

function checkLabel(check: Check): string {
  return (
    (Object.hasOwn(checkLabels, check.rule_id) ? checkLabels[check.rule_id] : undefined) ??
    knownBusinessComparatorLabel(check.comparator) ??
    "检查项目名称待核对"
  );
}

function ReadinessIssue({
  title,
  reason,
  repair,
}: {
  title: string;
  reason?: string;
  repair: Repair | null;
}) {
  return (
    <li className="review-preflight-row review-preflight-issue">
      <span className="review-preflight-dot" aria-hidden="true" />
      <span className="review-preflight-text">
        <strong>{title}</strong>
        {reason && reason !== title ? <span>{reason}</span> : null}
      </span>
      {repair ? (
        <a href={repair.href}>
          {repair.label} <span aria-hidden="true">→</span>
        </a>
      ) : null}
    </li>
  );
}

export function ReviewReadinessPanel({
  caseId,
  data,
  loading,
  error,
  onRefresh,
  selectedScope,
  onSelectScope,
  business,
  businessTime,
  footer,
}: {
  caseId: string;
  data: ReviewReadiness | undefined;
  loading: boolean;
  error: unknown;
  onRefresh: () => void;
  selectedScope: ReviewScope;
  onSelectScope: (scope: ReviewScope) => void;
  business?: ReviewReadinessBusinessScope;
  businessTime?: ReactNode;
  footer?: ReactNode;
}) {
  const trusted = data?.case_id === caseId ? data : undefined;
  const scope = trusted?.scopes.find((item) => item.id === selectedScope);
  const full = selectedScope === "full_review";
  const pack = full ? scope?.rule_pack : null;
  const packMatches =
    business &&
    pack?.applicable_scopes?.some(
      (item) =>
        item.business_line === business.business_line &&
        item.scenario === business.scenario &&
        item.contract_type === business.contract_type,
    );
  const checks =
    full || singleContractVersion(scope?.rule_set) !== null ? (scope?.checks ?? []) : [];
  const readyChecks = checks.filter(
    (check) => check.status === "ready" && !check.missing_dependencies.length,
  );
  const waitingChecks = checks.filter(
    (check) => check.status === "missing_basis" || check.missing_dependencies.length,
  );
  const materials = trusted?.materials ?? [];
  const readyMaterials = materials.filter((item) => item.selected && item.parse.status === "ready");
  const pendingMaterials = materials.filter(
    (item) => !item.selected || !["ready", "not_required"].includes(item.parse.status),
  );
  const readyCount = readyChecks.length + readyMaterials.length;
  const readyLabel = [
    readyChecks.map(checkLabel).join("、"),
    readyMaterials.length ? `${readyMaterials.length} 份已解析送审材料` : "",
  ]
    .filter(Boolean)
    .join("及");
  const trace: TechnicalField[] = [];
  if (business) trace.push({ label: "登记业务来源", value: Object.values(business).join(" / ") });
  if (pack) trace.push({ label: "规则包版本", value: `${pack.pack_id} / ${pack.snapshot_digest}` });
  for (const check of checks) {
    trace.push({
      label: checkLabel(check),
      value: [
        check.rule_id,
        check.comparator,
        check.status,
        check.next_action,
        ...check.missing_dependencies,
        ...(check.source_roles ?? []),
      ]
        .filter(Boolean)
        .join(" / "),
    });
  }

  return (
    <section className="review-readiness-panel" aria-label="审查准备清单" id="review-readiness">
      <header className="review-preflight-head">
        <h2>审查准备</h2>
        {full && scope ? (
          packMatches ? (
            <Tag>规则包 v{pack?.version.replace(/^v/i, "")}</Tag>
          ) : (
            <Tag color="orange">
              {pack ? "规则范围未确认" : "规则包待匹配"} · <a href={governanceURL()}>核对规则包</a>
            </Tag>
          )
        ) : null}
        <div className="review-preflight-tools">
          <Radio.Group
            name="review-scope"
            aria-label="审查范围"
            optionType="button"
            size="small"
            value={selectedScope}
            onChange={(event) => onSelectScope(event.target.value)}
            disabled={loading || Boolean(error)}
            options={[
              { label: "完整审查", value: "full_review" },
              {
                label: "单合同预审",
                value: "single_contract_precheck",
                disabled: !trusted?.scopes.some((item) => item.id === "single_contract_precheck"),
              },
            ]}
          />
          <Button type="text" aria-label="刷新" onClick={onRefresh} loading={loading}>
            刷新
          </Button>
        </div>
      </header>
      {businessTime}
      {loading ? (
        <p role="status" className="review-preflight-message">
          正在核对审查准备状态…
        </p>
      ) : null}
      {error ? (
        <p role="alert" className="review-preflight-message">
          审查准备未能读取：{errorMessage(error)}。请刷新后再试。
        </p>
      ) : null}
      {!loading && !error && scope ? (
        <>
          {scope.blockers.length || waitingChecks.length || pendingMaterials.length ? (
            <ul className="review-preflight-issues" aria-label="待处理事项">
              {scope.blockers.map((blocker) => (
                <ReadinessIssue
                  key={blocker.code}
                  title={blocker.message}
                  reason={
                    Array.from(new Set(blocker.missing_dependencies.map(dependencyLabel))).join(
                      "、",
                    ) || blocker.next_action
                  }
                  repair={blockerRepair(caseId, blocker)}
                />
              ))}
              {waitingChecks.map((check) => (
                <ReadinessIssue
                  key={check.rule_id}
                  title={checkLabel(check)}
                  reason={
                    Array.from(new Set(check.missing_dependencies.map(dependencyLabel))).join(
                      "、",
                    ) ||
                    readinessCheckActionLabel(check.next_action) ||
                    "缺少依据"
                  }
                  repair={dependencyRepair(caseId, check.missing_dependencies, check.next_action)}
                />
              ))}
              {pendingMaterials.map((item) => (
                <ReadinessIssue
                  key={`${item.document_id}:${item.version}`}
                  title={item.filename}
                  reason={
                    !item.selected
                      ? "送审选择待确认"
                      : item.parse.status === "failed"
                        ? "解析失败"
                        : item.parse.status === "stale"
                          ? "需要重新解析"
                          : item.parse.status === "pending"
                            ? "等待解析"
                            : "解析状态待核对"
                  }
                  repair={
                    item.parse.next_action === "reparse_material"
                      ? { ...materialsLink, label: "到材料列表重新解析" }
                      : materialsLink
                  }
                />
              ))}
            </ul>
          ) : null}
          {!full ? (
            <SingleContractScopeCoverage
              mode="readiness"
              scope={scope}
              disabled={loading || Boolean(error)}
              onSelectFullReview={() => onSelectScope("full_review")}
            />
          ) : null}
          {readyCount > 0 ? (
            <details className="review-preflight-ready">
              <summary className="review-preflight-row">
                <span className="review-preflight-dot" aria-hidden="true" />
                <span className="review-preflight-text">{readyLabel}</span>
                <span className="review-preflight-caption">
                  {readyCount} 项已就绪 · <span className="review-preflight-expand" />
                </span>
              </summary>
              <ul aria-label="已就绪明细">
                {readyChecks.map((check) => (
                  <li key={check.rule_id}>{checkLabel(check)} · 检查已准备，尚未执行</li>
                ))}
                {readyMaterials.map((item) => (
                  <li key={item.document_id}>{item.filename} · 已解析</li>
                ))}
              </ul>
            </details>
          ) : null}
          {!scope.blockers.length && !scope.can_start ? (
            <p className="review-preflight-message">所选范围尚未就绪。</p>
          ) : null}
          <TechnicalFieldGroup fields={trace} />
          {pendingMaterials.map((item) => (
            <TechnicalDetail
              key={item.document_id}
              label="材料状态编码"
              value={[item.role, item.parse.status, item.parse.next_action]
                .filter(Boolean)
                .join(" / ")}
            />
          ))}
        </>
      ) : null}
      {footer ? <footer className="review-preflight-foot">{footer}</footer> : null}
    </section>
  );
}
