import { Button, Radio } from "antd";
import type { ReviewReadiness, ReviewReadinessBlocker, ReviewScope } from "../api/review-readiness";
import { errorMessage } from "../api/http";
import { roleLabel } from "../domain/review";
import { StatusText } from "./status-text";

const parseStates: Record<string, string> = { ready: "已解析", pending: "待解析", failed: "解析失败", stale: "需要重新解析", not_required: "无需解析" };
const checkStates: Record<string, string> = { ready: "可执行", missing_basis: "缺少依据", not_executed: "尚未执行", not_applicable: "不适用" };
const checkActions: Record<string, string> = {
  start_review: "准备就绪，开始审查后执行本项检查",
  reparse_material: "请到材料列表重新解析对应材料",
  provide_missing_material_or_legal_basis: "补充下列材料或法律依据后重新核对",
  no_action: "本项不适用，无需补充材料",
};
const checkLabels: Record<string, string> = {
  "single-payment-sum-v1": "主合同中可识别的付款比例合计",
  cross_material_comparison: "跨材料对照",
  formal_policy_review: "正式制度与法律依据审查",
};
function dependencyLabel(dependency: string): string {
  const [kind, ...parts] = dependency.split(":");
  const value = parts.join(":");
  if (kind === "material_role") return `缺少${roleLabel(value)}`;
  if (kind === "parsed_material_role") return `${roleLabel(value)}等待解析`;
  if (kind === "failed_or_stale_parse_role") return `${roleLabel(value)}需要重新解析`;
  if (kind === "legal_authority") return `法律依据 ${value}`;
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
export function ReviewReadinessPanel({ caseId, data, loading, error, onRefresh, selectedScope, onSelectScope }: {
  caseId: string; data: ReviewReadiness | undefined; loading: boolean; error: unknown; onRefresh: () => void;
  selectedScope: ReviewScope; onSelectScope: (scope: ReviewScope) => void;
}) {
  const selected = data?.scopes.find((item) => item.id === selectedScope);
  return (
    <section className="workflow-panel review-readiness-panel" aria-label="审查准备清单" id="review-readiness">
      <header className="workflow-panel-head">
        <h2>审查准备清单</h2>
        <Button htmlType="button" onClick={onRefresh} loading={loading}>刷新准备清单</Button>
      </header>
      <p className="meta">清单反映当前材料与依据。开始审查时，服务端会重新校验权限、材料版本和依赖。</p>
      <fieldset className="field">
        <legend>审查范围</legend>
        <Radio.Group name="review-scope" value={selectedScope} onChange={(event) => onSelectScope(event.target.value as ReviewScope)} disabled={loading || Boolean(error)}>
          <Radio value="full_review">完整审查</Radio>
          <Radio value="single_contract_precheck" disabled={!data?.scopes.some((item) => item.id === "single_contract_precheck")}>单合同预审</Radio>
        </Radio.Group>
        {selectedScope === "single_contract_precheck" ? <span className="meta">只核对主合同中可识别的付款比例；跨材料、正式制度及其他风险未检查。补齐材料后请发起完整审查。</span> : null}
      </fieldset>
      {loading ? <p role="status">正在核对审查准备状态…</p> : null}
      {error ? <p role="alert">准备清单读取失败：{errorMessage(error)}。请刷新后再开始审查。</p> : null}
      {data ? <>
        <p><StatusText tone={loading ? "progress" : error || !selected?.can_start ? "attention" : "neutral"}>
          {loading ? "正在更新准备状态" : error ? "准备状态待重新核对" : selected?.can_start ? "所选范围可以开始" : "所选范围需要先补齐以下事项"}
        </StatusText></p>
        {data.scopes.filter((scope) => scope.id === selectedScope).map((scope) => <div key={scope.id}>
          <h3>{scope.id === "full_review" ? "完整审查" : "单合同预审"}</h3>
          {scope.rule_pack ? <p className="meta">规则包 {scope.rule_pack.pack_id} · 第 {scope.rule_pack.version} 版</p> : null}
          {scope.rule_set ? <p className="meta">检查规则 {scope.rule_set.rule_set_id} · 第 {scope.rule_set.rule_set_version} 版</p> : null}
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
          {scope.checks.length ? <>
            <h4>本次检查项目</h4>
            <ul className="review-readiness-list" aria-label="规则准备状态">
              {scope.checks.map((check) => <li key={check.rule_id}>
                <strong>{checkLabels[check.rule_id] ?? check.rule_id}</strong>{" "}<StatusText tone={check.status === "missing_basis" || check.status === "not_executed" ? "attention" : "neutral"}>{checkStates[check.status] ?? "状态待核对"}</StatusText>
                {check.missing_dependencies.length ? <p>待补齐：{check.missing_dependencies.map(dependencyLabel).join("；")}</p> : null}
                {check.next_action ? <p>{checkActions[check.next_action] ?? check.next_action}</p> : null}
                {check.next_action === "reparse_material" ? <Button type="link" href="#case-materials">到材料列表重新解析</Button> : null}
              </li>)}
            </ul>
          </> : null}
        </div>)}
        <h3>本次材料</h3>
        {data.materials.length ? <ul className="review-readiness-list" aria-label="当前审查材料与解析状态">
          {data.materials.map((item) => <li key={`${item.document_id}:${item.version}`}>
            <strong>{item.filename}</strong>{" "}<span>{roleLabel(item.role)} · {item.version > 0 ? `第 ${item.version} 版` : "旧材料身份"}</span>{" "}
            <StatusText tone={item.parse.status === "failed" || item.parse.status === "stale" ? "attention" : item.parse.status === "pending" ? "progress" : "neutral"}>{parseStates[item.parse.status] ?? "状态待核对"}</StatusText>
            {!item.selected ? <p>这份材料的审查选择尚未确认。</p> : null}
            {item.parse.next_action === "reparse_material" ? <p><Button type="link" href="#case-materials">到材料列表重新解析</Button></p> : null}
            {item.parse.status === "pending" ? <p className="meta">受理后处理解析；解析失败会保留缺证状态。</p> : null}
          </li>)}
        </ul> : <p>尚未选择审查材料，<a href="#case-materials">上传或确认材料</a>。</p>}
      </> : null}
    </section>
  );
}
