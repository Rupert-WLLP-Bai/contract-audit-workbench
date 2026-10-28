import { Button, Card } from "antd";
import { PageStatus } from "../../components/page-status";
import { StatusText } from "../../components/status-text";
import {
  DEFAULT_OPERATOR_AUDIT_FLAGS,
  type OperatorAuditFlags,
} from "./feature-flags";
import {
  machineStatusLabel,
  machineStatusTone,
  quantityLabel,
  relationLabelFor,
  relationTone,
  timeLabel,
} from "./labels";
import type {
  CheckResult,
  DocumentEvidence,
  ExternalSnapshotEvidence,
  FulfillmentComparison,
  Relation,
  UnknownEvidence,
} from "./types";

export interface FulfillmentDiffPanelProps {
  items: FulfillmentComparison[];
  flags?: OperatorAuditFlags;
  /** Point at the existing human-review flow; wiring is a Lead decision. */
  humanReviewHref?: (checkId: string) => string | null;
  onOpenHumanReview?: (checkId: string) => void;
}

function observedAtOf(check: CheckResult): string | null {
  for (const evidence of check.evidence) {
    if (evidence.kind === "external_snapshot" && evidence.observedAt !== null)
      return evidence.observedAt;
  }
  return null;
}

function relationNote(relation: Relation): string {
  switch (relation) {
    case "conflict":
      return "配置记录与合同约定存在差异；需要人工复核，不能直接认定为实际吞吐违约。";
    case "insufficient":
      return "证据不足（可能因映射缺失、多义或快照不可用）；不产生确定性结论。";
    case "not_applicable":
      return "不适用需要可验证的范围证据；缺失、超时或失败不会被当作不适用。";
    case "compatible":
      return "配置记录与合同约定一致；仍不代表实际吞吐已经验收。";
    default:
      return "关系未报告，不能作为结论。";
  }
}

function DocumentEvidenceList({ entries }: { entries: DocumentEvidence[] }) {
  if (entries.length === 0)
    return <p className="meta">本次检查没有原文证据。</p>;
  return (
    <ul>
      {entries.map((entry, index) => {
        const complete =
          entry.quote !== null &&
          entry.documentVersionId !== null &&
          entry.parsedVersionId !== null &&
          entry.start !== null &&
          entry.end !== null;
        return (
          <li
            key={`${entry.documentVersionId ?? "doc"}-${entry.start ?? "x"}-${index}`}
          >
            <blockquote>{entry.quote ?? "引用未报告"}</blockquote>
            <p className="meta">
              文档版本 {entry.documentVersionId ?? "未报告"} · 解析版本{" "}
              {entry.parsedVersionId ?? "未报告"} · 区间{" "}
              {entry.start ?? "未报告"}–{entry.end ?? "未报告"}
            </p>
            {!complete ? (
              <p className="callout">引用不完整，不能作为已确认引用。</p>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}

function SnapshotEvidenceList({
  entries,
}: {
  entries: ExternalSnapshotEvidence[];
}) {
  if (entries.length === 0)
    return <p className="meta">本次检查没有外部快照证据。</p>;
  return (
    <ul>
      {entries.map((entry, index) => (
        <li
          key={`${entry.snapshotId ?? "snapshot"}-${entry.fieldPointer ?? "x"}-${index}`}
        >
          <p>
            快照 <span className="digest">{entry.snapshotId ?? "未报告"}</span>{" "}
            / 记录 {entry.recordId ?? "未报告"}
          </p>
          <p>
            字段 <span className="digest">{entry.fieldPointer ?? "未报告"}</span>
            ：<span>{entry.displayValue ?? "展示值未报告"}</span>
          </p>
          <p className="meta">观测时间 {timeLabel(entry.observedAt)}</p>
        </li>
      ))}
    </ul>
  );
}

function UnknownEvidenceList({ entries }: { entries: UnknownEvidence[] }) {
  return (
    <ul>
      {entries.map((entry, index) => (
        <li key={`unknown-${index}`}>
          证据类型 {entry.rawKind ?? "未报告"}；该证据不会归入原文或快照。
        </li>
      ))}
    </ul>
  );
}

function ComparisonCard({
  item,
  humanReviewHref,
  onOpenHumanReview,
}: {
  item: FulfillmentComparison;
  humanReviewHref?: (checkId: string) => string | null;
  onOpenHumanReview?: (checkId: string) => void;
}) {
  const { check, contractValue, configuredValue } = item;
  const documents = check.evidence.filter(
    (entry): entry is DocumentEvidence => entry.kind === "document",
  );
  const snapshots = check.evidence.filter(
    (entry): entry is ExternalSnapshotEvidence =>
      entry.kind === "external_snapshot",
  );
  const unknown = check.evidence.filter(
    (entry): entry is UnknownEvidence => entry.kind === "unknown",
  );
  const href = humanReviewHref?.(check.checkId) ?? null;

  return (
    <Card
      size="small"
      title={
        <div className="workflow-panel-head">
          <div>
            <h3>{check.checkId}</h3>
            <p className="meta">
              原因代码 {check.reasonCode ?? "未报告"} ·{" "}
              {machineStatusLabel(check.machineStatus)}
            </p>
          </div>
          <StatusText tone={relationTone(check.relation)}>
            {relationLabelFor(check.relation)}
          </StatusText>
        </div>
      }
    >
      <dl className="execution-dl">
        <div>
          <dt>合同值</dt>
          <dd>{quantityLabel(contractValue)}</dd>
        </div>
        <div>
          <dt>配置值</dt>
          <dd>{quantityLabel(configuredValue)}</dd>
        </div>
        <div>
          <dt>单位</dt>
          <dd>
            合同 {contractValue?.unit ?? "未报告"}；配置{" "}
            {configuredValue?.unit ?? "未报告"}
          </dd>
        </div>
        <div>
          <dt>观测时间</dt>
          <dd>{timeLabel(observedAtOf(check))}</dd>
        </div>
        <div>
          <dt>执行状态</dt>
          <dd>
            <StatusText tone={machineStatusTone(check.machineStatus)}>
              {machineStatusLabel(check.machineStatus)}
            </StatusText>
          </dd>
        </div>
        <div>
          <dt>人工复核</dt>
          <dd>
            <StatusText
              tone={check.requiresHumanReview === true ? "attention" : "neutral"}
            >
              {check.requiresHumanReview === true
                ? "需要人工复核"
                : check.requiresHumanReview === false
                  ? "未标记需要人工复核"
                  : "是否需人工复核未报告"}
            </StatusText>
          </dd>
        </div>
      </dl>

      <p className="callout">{relationNote(check.relation)}</p>

      <div className="execution-results">
        <section aria-label="合同原文证据">
          <h4>合同原文证据（document）</h4>
          <DocumentEvidenceList entries={documents} />
        </section>
        <section aria-label="外部快照证据">
          <h4>外部快照证据（external_snapshot）</h4>
          <SnapshotEvidenceList entries={snapshots} />
        </section>
      </div>
      {unknown.length > 0 ? (
        <section aria-label="其他证据">
          <h4>其他证据（类型未识别）</h4>
          <UnknownEvidenceList entries={unknown} />
        </section>
      ) : null}

      <h4>限制</h4>
      {check.limitations.length > 0 ? (
        <ul>
          {check.limitations.map((limitation, index) => (
            <li key={`${check.checkId}-limitation-${index}`}>{limitation}</li>
          ))}
        </ul>
      ) : (
        <p className="meta">限制未报告。</p>
      )}

      <h4>人工复核</h4>
      {onOpenHumanReview ? (
        <Button
          htmlType="button"
          onClick={() => onOpenHumanReview(check.checkId)}
        >
          进入人工复核
        </Button>
      ) : href !== null ? (
        <a href={href}>进入人工复核</a>
      ) : (
        <p className="meta">
          人工复核入口尚未在当前页面接线；机器结果不是最终结论。
        </p>
      )}
    </Card>
  );
}

/** Default off; the Lead decides where the human-review entry points. */
export function FulfillmentDiffPanel({
  items,
  flags = DEFAULT_OPERATOR_AUDIT_FLAGS,
  humanReviewHref,
  onOpenHumanReview,
}: FulfillmentDiffPanelProps) {
  if (!flags.fulfillmentChecksEnabled) return null;
  return (
    <section className="workflow-panel" aria-label="履约差异对照">
      <header className="workflow-panel-head">
        <div>
          <h2>合同与配置履约对照</h2>
          <p className="meta">
            同屏对照合同值、配置值与证据；机器结果不替代人工决定。
          </p>
        </div>
      </header>
      <p className="callout">
        配置差异不等于实际吞吐违约。机器结果只说明合同约定与配置记录的关系，最终结论仍需人工复核。
      </p>
      {items.length === 0 ? (
        <PageStatus
          kind="empty"
          title="当前评估没有可展示的检查结果"
          detail="空结果不代表审查通过。"
        />
      ) : (
        <div className="evidence-stack">
          {items.map((item, index) => (
            <ComparisonCard
              key={`${item.check.checkId}-${index}`}
              item={item}
              humanReviewHref={humanReviewHref}
              onOpenHumanReview={onOpenHumanReview}
            />
          ))}
        </div>
      )}
    </section>
  );
}
