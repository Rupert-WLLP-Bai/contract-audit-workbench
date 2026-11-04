// File metadata (manifest mirror):
// Purpose: Renders ReviewReadinessPanel, the review readiness panel workbench UI, from the selected case/workspace projection and the module’s query/state inputs; it displays server-derived state and does not establish business authorization or evidence validity.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-04
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

import { Button, Radio } from "antd";
import type { ReviewReadiness, ReviewReadinessBlocker, ReviewReadinessBusinessScope, ReviewScope } from "../api/review-readiness";
import { errorMessage } from "../api/http";
import { businessScopeLabel, knownBusinessComparatorLabel, readinessCheckActionLabel } from "../domain/business-labels";
import { contractTypeLabel, roleLabel } from "../domain/review";
import { isSingleContractV1 } from "../features/review/single-contract-coverage";
import { SingleContractScopeCoverage } from "./single-contract-scope-coverage";
import { StatusText } from "./status-text";
import { TechnicalDetail } from "./technical-detail";

const parseStates: Record<string, string> = { ready: "已解析", pending: "待解析", failed: "解析失败", stale: "需要重新解析", not_required: "无需解析" };
const checkStates: Record<string, string> = { ready: "尚未执行", missing_basis: "缺少依据", not_executed: "尚未执行", not_applicable: "不适用" };
const checkLabels: Record<string, string> = {
  "single-payment-sum-v1": "主合同中可识别的付款比例合计",
  cross_material_comparison: "跨材料对照",
  formal_policy_review: "正式制度与法律依据审查",
};
function scopeLabel(scope: ReviewReadinessBusinessScope): string {
  const contractType = contractTypeLabel(scope.contract_type);
  return `${businessScopeLabel(scope.business_line, scope.scenario)} / ${contractType}`;
}
function governanceURL(): string {
  const base = import.meta.env.VITE_ADMIN_URL;
  return base ? new URL("/admin/rulepacks", base).href : "/admin/rulepacks";
}
function dependencyLabel(dependency: string): string {
  const [kind, ...parts] = dependency.split(":");
  const value = parts.join(":");
  if (kind === "material_role") return `缺少${roleLabel(value)}`;
  if (kind === "parsed_material_role") return `${roleLabel(value)}等待解析`;
  if (kind === "failed_or_stale_parse_role") return `${roleLabel(value)}需要重新解析`;
  if (kind === "legal_authority") return "需要核对对应法律依据";
  if (kind === "exact_legal_authority_gate") return "需要核对适用法律依据";
  if (kind === "comparison_materials") return "缺少采购、审批等对照材料";
  if (kind === "formal_policy_review") return "正式制度与法律依据尚未审查";
  if (kind === "valid_rule_pack") return "需要有效的规则包";
  return "需要核对所需材料与依据";
}
function repairLink(caseId: string, blocker: ReviewReadinessBlocker): { href: string; label: string } | null {
  if (blocker.code === "business_at_required") return { href: "#review-business-time", label: "填写业务时间" };
  if (["required_materials_missing", "material_selection_pending", "primary_contract_required", "primary_contract_ambiguous"].includes(blocker.code)) return { href: "#case-materials", label: "补齐或确认材料" };
  if (blocker.code === "governed_dependencies_incomplete") return { href: `/cases/${encodeURIComponent(caseId)}/legal-knowledge`, label: "查看依据与制度" };
  if (blocker.code === "case_not_writable") return { href: "#case-business-title", label: "查看案件状态" };
  return null;
}
export function ReviewReadinessPanel({ caseId, data, loading, error, onRefresh, selectedScope, onSelectScope, business }: {
  caseId: string; data: ReviewReadiness | undefined; loading: boolean; error: unknown; onRefresh: () => void;
  selectedScope: ReviewScope; onSelectScope: (scope: ReviewScope) => void; business?: ReviewReadinessBusinessScope;
}) {
  const selected = data?.scopes.find((item) => item.id === selectedScope);
  const pack = selectedScope === "full_review" ? selected?.rule_pack : null;
  const declaredScopes = pack?.applicable_scopes ?? [];
  const matchedScope = business && declaredScopes.find((scope) => scope.business_line === business.business_line &&
    scope.scenario === business.scenario && scope.contract_type === business.contract_type);
  const missingBusinessAt = selected?.blockers.some((blocker) => blocker.code === "business_at_required") ?? false;
  return (
    <section className="workflow-panel review-readiness-panel" aria-label="审查准备清单" id="review-readiness">
      <header className="workflow-panel-head">
        <h2>审查准备清单</h2>
        <Button htmlType="button" onClick={onRefresh} loading={loading}>刷新准备清单</Button>
      </header>
      <p className="meta">清单反映当前材料与依据。开始审查时，服务端会重新校验权限、材料版本和依赖。</p>
      <section aria-label="合同业务与规则匹配确认">
        <h3>合同业务与规则匹配确认</h3>
        <p>案件登记业务：{business ? scopeLabel(business) : "业务信息待核对"}。</p>
        <p>请对照合同内容确认登记分类。规则按登记的业务分类选择，系统不会从文件名或模型结果判断合同业务。</p>
        <p>如分类不符，请重新登记并选择适用业务场景，再发起新的审查；已有案件和审查历史保留。</p>
        <Button type="link" href="/#intake-primary-contract">重新登记并选择业务场景</Button>
        {business ? <TechnicalDetail label="登记业务技术详情" value={[business.business_line, business.scenario, business.contract_type].join(" / ")} /> : null}
      </section>
      <fieldset className="field">
        <legend>审查范围</legend>
        <Radio.Group name="review-scope" value={selectedScope} onChange={(event) => onSelectScope(event.target.value as ReviewScope)} disabled={loading || Boolean(error)}>
          <Radio value="full_review">完整审查</Radio>
          <Radio value="single_contract_precheck" disabled={!data?.scopes.some((item) => item.id === "single_contract_precheck")}>单合同预审</Radio>
        </Radio.Group>
      </fieldset>
      {selectedScope === "single_contract_precheck" ? <SingleContractScopeCoverage
        mode="readiness" scope={selected} disabled={loading || Boolean(error)} onSelectFullReview={() => onSelectScope("full_review")}
      /> : <p className="meta">核对完整审查准备状态后，<a href="#case-next-step-title">返回下一步操作</a>。</p>}
      {loading ? <p role="status">正在核对审查准备状态…</p> : null}
      {error ? <p role="alert">准备清单读取失败：{errorMessage(error)}。请刷新后再开始审查。</p> : null}
      {data ? <>
        <p><StatusText tone={loading ? "progress" : error || !selected?.can_start ? "attention" : "neutral"}>
          {loading ? "正在更新准备状态" : error ? "准备状态待重新核对" : selected?.can_start ? "所选范围可以开始" : "所选范围需要先补齐以下事项"}
        </StatusText></p>
        {data.scopes.filter((scope) => scope.id === selectedScope).map((scope) => <div key={scope.id}>
          <h3>{scope.id === "full_review" ? "完整审查" : "单合同预审"}</h3>
          {scope.id === "full_review" ? <section aria-label="已选规则包">
            <h4>本次适用规则包</h4>
            {pack ? <>
              <p>{matchedScope ? `适用于${scopeLabel(matchedScope)}的规则包` : "已选择的规则包"}（治理名称未登记） · 第 {pack.version} 版</p>
              {declaredScopes.length ? <>
                <p>规则包声明的业务范围：</p>
                <ul>{declaredScopes.map((declared) => <li key={`${declared.business_line}:${declared.scenario}:${declared.contract_type}`}>{scopeLabel(declared)}</li>)}</ul>
                <p>{matchedScope ? "其中一项与案件登记分类一致；仍需人工确认合同内容是否属于该分类。" : "未核对到与案件登记分类一致的规则范围，请联系规则治理人员核对。"}</p>
              </> : <p>规则包的声明范围未返回，请刷新清单或联系规则治理人员核对。</p>}
              <TechnicalDetail label="规则包技术详情" value={`${pack.pack_id} / ${pack.snapshot_digest}`}>
                {declaredScopes.map((declared) => <p key={`${declared.business_line}:${declared.scenario}:${declared.contract_type}`}>
                  <code>{[declared.business_line, declared.scenario, declared.contract_type].join(" / ")}</code>
                </p>)}
              </TechnicalDetail>
            </> : <p>{missingBusinessAt ? "填写业务时间后才能确定适用规则包。" : "尚未获得可确认的已发布规则包，请核对准备事项或联系规则治理人员。"}</p>}
            <Button type="link" href={governanceURL()}>联系规则治理人员核对规则包</Button>
          </section> : null}
          {scope.blockers.length ? <ul className="review-readiness-list" aria-label="开始审查前需要补齐的事项">
            {scope.blockers.map((blocker) => {
              const link = repairLink(caseId, blocker);
              return <li key={blocker.code}>
                <strong>{blocker.message}</strong>
                {blocker.next_action ? <p>{blocker.next_action}</p> : null}
                {link ? <Button type="link" href={link.href}>{link.label}</Button> : null}
              </li>;
            })}
          </ul> : null}
          {scope.checks.length && (scope.id !== "single_contract_precheck" || isSingleContractV1(scope.rule_set)) ? <>
            <h4>本次检查项目</h4>
            <p className="meta">材料已解析只表示原文可供检查；缺少规则所需事实或依据时仍需补充、核对，并不表示解析失败。</p>
            <ul className="review-readiness-list" aria-label="规则准备状态">
              {scope.checks.map((check) => <li key={check.rule_id}>
                <strong>{(Object.hasOwn(checkLabels, check.rule_id) ? checkLabels[check.rule_id] : undefined) ?? knownBusinessComparatorLabel(check.comparator) ?? (scope.id === "single_contract_precheck" ? "其他范围外检查" : "检查项目名称待核对")}</strong>{" "}<StatusText tone={check.status === "missing_basis" || check.status === "not_executed" ? "attention" : "neutral"}>{Object.hasOwn(checkStates, check.status) ? checkStates[check.status] : "状态待核对"}</StatusText>
                {check.source_roles?.length ? <p>规则声明的事实材料角色：{check.source_roles.map(roleLabel).join("、")}；对照依据和附件仍需按合同内容核对。</p> : null}
                {check.missing_dependencies.length ? <p>待补齐：{check.missing_dependencies.map(dependencyLabel).join("；")}</p> : null}
                {check.next_action ? <p>{readinessCheckActionLabel(check.next_action)}</p> : null}
                {check.next_action === "reparse_material" ? <Button type="link" href="#case-materials">到材料列表重新解析</Button> : null}
                {check.missing_dependencies.some((dependency) => dependency.startsWith("material_role:")) ? <Button type="link" href="#case-materials">补充所需材料</Button> : null}
                {check.missing_dependencies.some((dependency) => dependency.startsWith("legal_authority:") || dependency === "exact_legal_authority_gate") ? <Button type="link" href={`/cases/${encodeURIComponent(caseId)}/legal-knowledge`}>核对法律依据与制度</Button> : null}
                <TechnicalDetail label="检查项目技术详情" value={[check.rule_id, check.comparator, ...check.missing_dependencies, check.status, check.next_action, ...(check.source_roles ?? [])].filter(Boolean).join(" / ")} />
              </li>)}
            </ul>
          </> : null}
        </div>)}
        <h3>本次材料</h3>
        {data.materials.length ? <ul className="review-readiness-list" aria-label="当前审查材料与解析状态">
          {data.materials.map((item) => <li key={`${item.document_id}:${item.version}`}>
            <strong>{item.filename}</strong>{" "}<span>{roleLabel(item.role)} · {item.version > 0 ? `第 ${item.version} 版` : "旧材料身份"}</span>{" "}
            <StatusText tone={item.parse.status === "failed" || item.parse.status === "stale" ? "attention" : item.parse.status === "pending" ? "progress" : "neutral"}>{Object.hasOwn(parseStates, item.parse.status) ? parseStates[item.parse.status] : "状态待核对"}</StatusText>
            <TechnicalDetail label="材料状态技术详情" value={[item.role, item.parse.status, item.parse.next_action].filter(Boolean).join(" / ")} />
            {!item.selected ? <p>这份材料的审查选择尚未确认。</p> : null}
            {item.parse.next_action === "reparse_material" ? <p><Button type="link" href="#case-materials">到材料列表重新解析</Button></p> : null}
            {item.parse.status === "pending" ? <p className="meta">受理后处理解析；解析失败会保留缺证状态。</p> : null}
          </li>)}
        </ul> : <p>尚未选择审查材料，<a href="#case-materials">上传或确认材料</a>。</p>}
      </> : null}
    </section>
  );
}
