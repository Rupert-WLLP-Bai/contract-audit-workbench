import { Card } from "antd";
import { FulfillmentDiffPanel } from "./fulfillment-diff-panel";
import type { OperatorAuditFlags } from "./feature-flags";
import type { ServiceObjectSummary } from "./service-objects-api";
import type { ExternalObservation, FulfillmentComparison, FulfillmentEvaluation } from "./types";

const objectStatus: Record<string, string> = {
  evaluated: "已核验",
  insufficient: "证据不足",
  unresolved: "尚未确定",
  not_applicable: "不适用",
};

const reasons: Record<string, string> = {
  service_object_unregistered: "合同识别的对象尚未登记",
  service_object_identity_ambiguous: "多个登记对象对应同一合同对象",
  service_object_identity_unconfirmed: "对象身份尚未确认",
  service_object_contract_association_missing: "尚无合同事实关联",
  service_object_source_requires_reassociation: "合同或解析版本已变化，需要重新关联",
  service_object_source_unresolved: "无法解析所选对象的合同来源",
  contract_object_source_ambiguous: "对象的合同来源不明确",
  contract_object_source_evidence_missing: "合同对象缺少来源证据",
  contract_object_candidates_missing: "冻结合同中未识别出可核验对象",
  contract_source_not_ready: "合同或补充协议尚未形成可用的冻结解析",
  operator_mapping_missing: "缺少有效的外部对象映射",
  operator_mapping_ambiguous: "存在多个有效映射",
  external_snapshot_missing: "证据截止前没有该对象的快照",
  identity_evidence_after_cutoff: "对象或关联在证据截止后才确认",
  contract_evidence_after_cutoff: "合同或解析记录晚于证据截止时间，或取得时间无法核实",
  contract_source_integrity_error: "冻结合同来源未通过校验",
  evaluation_unavailable: "对象核验未能完成",
  evaluation_partial: "对象核验未完整完成",
};

export function beijingTime(value: string | null | undefined): string {
  if (!value) return "未报告";
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "未报告";
  return new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai", year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23",
  }).format(date) + "（北京时间）";
}

export function FulfillmentObjectsPanel({ evaluation, objects, snapshots, flags, humanReviewHref }: {
  evaluation: FulfillmentEvaluation;
  objects: ServiceObjectSummary[];
  snapshots: ExternalObservation[];
  flags: OperatorAuditFlags;
  humanReviewHref: (checkId: string) => string | null;
}) {
  const coverage = evaluation.coverage;
  const scope = evaluation.scope;
  return (
    <section aria-label="逐对象核验结果">
      <Card size="small" title="核验范围与覆盖情况">
        <p>
          范围：{scope?.kind === "whole_case" ? "整案" : scope?.kind === "explicit_subset" ? "明确选择的对象" : "未报告"}
          {scope?.kind === "explicit_subset" ? `（${scope.serviceObjectIds.join("、")}）` : ""}
        </p>
        <p>业务时间 {beijingTime(evaluation.businessAt)}；证据截止时间 {beijingTime(evaluation.evidenceCutoffAt)}</p>
        <p role="status">
          {coverage?.status === "complete" ? "覆盖完整" : coverage?.status === "partial" ? "覆盖不完整" : "覆盖状态未报告"}：
          候选 {coverage?.candidateCount ?? "未报告"}，已核验或不适用 {coverage?.evaluatedCount ?? "未报告"}，
          尚未确定 {coverage?.unresolvedCount ?? "未报告"}，证据不足 {coverage?.insufficientCount ?? "未报告"}。
        </p>
        {coverage?.reasonCode === "no_candidates" ? <p>没有可核验的对象，不能视为覆盖完整。</p> : null}
      </Card>
      <ol className="history">
        {(evaluation.objects ?? []).map((object, index) => {
          const name = objects.find((item) => item.service_object_id === object.serviceObjectId)?.display_name;
          const observation = snapshots.find((item) => item.snapshotSetId === object.snapshotSetId);
          const contractFacts = object.facts.filter((item) => item.name === "minimum_bandwidth");
          const contract = contractFacts.length === 1 ? contractFacts[0] : undefined;
          const configured = observation?.fields.find((field) => field.name === "configured_bandwidth");
          const comparisons: FulfillmentComparison[] = object.checks.map((check) => ({
            check,
            contractValue: contract?.value ?? null,
            configuredValue: configured ? { value: configured.value, unit: configured.unit } : null,
          }));
          return (
            <li key={`${object.serviceObjectId ?? object.objectRef}:${object.source?.documentId ?? "none"}:${index}`}>
              <Card size="small" title={`${name ?? object.objectRef} · ${objectStatus[object.status ?? ""] ?? "状态未报告"}`}>
                <p>合同对象 {object.objectRef}；登记对象 {object.serviceObjectId ?? "未登记"}，版本 {object.objectVersion ?? "未报告"}；关联版本 {object.associationVersion ?? "未报告"}。</p>
                <p>关联来源审查 {object.associationRunId ?? "未报告"} / {object.associationAttemptId ?? "未报告"}。</p>
                {object.reasonCode ? <p>{reasons[object.reasonCode] ?? "请查看该对象的核验详情"}（{object.reasonCode}）。</p> : null}
                <p>映射 {object.mappingId ?? "未报告"} / {object.mappingVersion ?? "未报告"}；绑定 {object.bindingVersion ?? "未报告"}；快照 {object.snapshotSetId ?? "未报告"}。</p>
                <p>证据取得时间 {beijingTime(object.snapshotCollectedAt)}；事实引用 {object.factRefs.length} 项；事实摘要 {object.factsSha256 ?? "未报告"}。</p>
                {object.source ? <p>材料 {object.source.materialId ?? "未报告"} / {object.source.materialVersion ?? "未报告"}（{object.source.purpose ?? "未报告"}）；文档 {object.source.documentId ?? "未报告"}；解析 {object.source.parsedDocumentId ?? "未报告"}。本次审查 {object.source.runId ?? "未报告"} / {object.source.attemptId ?? "未报告"}。</p> : null}
                {comparisons.length > 0 ? <FulfillmentDiffPanel items={comparisons} flags={flags} humanReviewHref={humanReviewHref} /> : null}
              </Card>
            </li>
          );
        })}
      </ol>
    </section>
  );
}
