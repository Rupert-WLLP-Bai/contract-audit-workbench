import { AimOutlined, ArrowRightOutlined } from "@ant-design/icons";
import {
  Button,
  Card,
  Empty,
  Input,
  Radio,
  Segmented,
  Select,
  Tabs,
  Tag,
} from "antd";
import { useId, useState, type FormEvent } from "react";
import type { DecisionAction, Finding, ReviewDocument } from "../api/types";
import type { FindingFilter } from "../domain/review";
import {
  DECISION_ACTIONS,
  confidenceLabel,
  decisionLabel,
  filterFindings,
  formatDateTime,
  latestDecision,
  originLabel,
  relationLabel,
  roleLabel,
  shortDigest,
  validateDecisionReason,
} from "../domain/review";
import {
  decisionSummary,
  domainLabel,
  findingLocation,
  summarizeFindings,
  verificationLabel,
} from "../domain/review-presentation";
import { evidenceSourceMatches, resolveAnchor } from "../domain/text";
import { palette } from "../theme";
import { ChartLegend, DonutChart, type DonutItem } from "./charts/donut-chart";
import { RiskTag, severityLabel } from "./ui";
import "./review-workspace.css";

const FILTERS: { id: FindingFilter; label: string }[] = [
  { id: "all", label: "全部" },
  { id: "conflict", label: "冲突" },
  { id: "insufficient", label: "信息不足" },
  { id: "undecided", label: "待处理" },
];

const SEVERITY_OPTIONS = [
  { value: "all", label: "全部等级" },
  { value: "high", label: "高风险" },
  { value: "medium", label: "中风险" },
  { value: "low", label: "低风险" },
  { value: "info", label: "提示" },
  { value: "unrated", label: "未评级" },
];

export const SEVERITY_COLORS: Record<string, string> = {
  high: palette.high,
  medium: palette.medium,
  low: palette.low,
  info: palette.info,
  unrated: palette.neutral,
};

export function originKindLabel(origin: string): string {
  if (origin === "rule") return "规则命中";
  if (origin === "ai" || origin === "model") return "模型建议";
  if (origin === "manual") return "人工记录";
  return originLabel(origin);
}

function originExplanation(origin: string): string {
  if (origin === "rule") {
    return "规则命中来自本次冻结的规则包，表示需要人工核对的条款，不等于已经确认违约。";
  }
  if (origin === "ai" || origin === "model") {
    return "模型建议只是待核验线索，必须逐条人工处理；没有可核对引用的内容不会变成结论。";
  }
  if (origin === "manual") return "这项内容由人工记录，处理历史按追加方式保留。";
  return "服务没有返回这项线索的来源说明。";
}

function verificationTone(status: string): string {
  if (status === "verified") return "success";
  if (status === "unverified") return "warning";
  return "default";
}

function severityItems(findings: Finding[]): DonutItem[] {
  const summary = summarizeFindings(findings);
  return [
    { key: "high", label: "高风险", value: summary.high, color: SEVERITY_COLORS.high! },
    {
      key: "medium",
      label: "中风险",
      value: summary.medium,
      color: SEVERITY_COLORS.medium!,
    },
    { key: "low", label: "低风险", value: summary.low, color: SEVERITY_COLORS.low! },
    { key: "info", label: "提示", value: summary.info, color: SEVERITY_COLORS.info! },
    {
      key: "unrated",
      label: "未评级",
      value: summary.unrated,
      color: SEVERITY_COLORS.unrated!,
    },
  ];
}

function EvidenceList({
  finding,
  documents,
}: {
  finding: Finding;
  documents: ReviewDocument[];
}) {
  if (finding.evidence.length === 0) {
    return <p>这条线索没有附带证据，不能视为已验证。</p>;
  }
  return (
    <div className="evidence-stack">
      {finding.evidence.map((anchor) => {
        const source =
          documents.find((document) =>
            evidenceSourceMatches(
              anchor,
              document.id,
              document.parsed_document,
            ),
          ) ??
          documents.find(
            (document) => document.id === anchor.document_version_id,
          );
        const resolved = resolveAnchor(
          source?.text ?? null,
          anchor,
          source?.parsed_document ?? null,
        );
        const located =
          anchor.status === "located" && resolved.kind === "highlight";
        const anchorLabel = located
          ? "已定位"
          : anchor.status === "unlocatable"
            ? "无法定位"
            : anchor.status === "anchor_mismatch"
              ? "定位失效"
              : anchor.status === "located"
                ? "未能在正文定位"
                : "来源缺失";
        return (
          <section key={anchor.id} className="evidence-card">
            <header>
              <strong>
                {source ? roleLabel(source.role) : "来源未对应到材料"}
              </strong>
              <Tag color={located ? "success" : "default"}>{anchorLabel}</Tag>
            </header>
            {source ? <p className="meta">{source.filename}</p> : null}
            <p
              className="meta"
              data-evidence-parsed-document-id={
                anchor.parsed_document_id ?? ""
              }
              data-evidence-normalized-digest={anchor.normalized_digest ?? ""}
              data-evidence-document-version-id={anchor.document_version_id}
            >
              {anchor.parsed_document_id === null &&
              anchor.normalized_digest === null ? (
                "历史来源未记录解析身份或正文摘要"
              ) : (
                <>
                  冻结解析{" "}
                  {anchor.parsed_document_id === null ? (
                    "未记录"
                  ) : (
                    <span title={anchor.parsed_document_id}>
                      {shortDigest(anchor.parsed_document_id)}
                    </span>
                  )}
                  {" · "}正文 SHA-256{" "}
                  {anchor.normalized_digest === null ? (
                    "未记录"
                  ) : (
                    <span title={anchor.normalized_digest}>
                      {shortDigest(anchor.normalized_digest)}
                    </span>
                  )}
                </>
              )}
            </p>
            {resolved.kind === "snapshot" && !anchor.mismatch_reason ? (
              <p className="review-doc-callout">{resolved.reason}</p>
            ) : null}
            {resolved.kind === "unavailable" ? (
              <p className="review-doc-callout">{resolved.reason}</p>
            ) : null}
            {located && anchor.page !== null ? (
              <p className="meta">第 {anchor.page} 页</p>
            ) : null}
            {anchor.mismatch_reason ? <p>{anchor.mismatch_reason}</p> : null}
            {anchor.quote ? (
              <blockquote>{anchor.quote}</blockquote>
            ) : (
              <p>没有引用快照。</p>
            )}
          </section>
        );
      })}
    </div>
  );
}

export function FindingPanel({
  findings,
  documents,
  selectedId,
  onSelect,
  pending,
  canDecide = true,
  decisionDisabledReason,
  conflictMessage,
  saveError,
  onDecide,
}: {
  findings: Finding[];
  documents: ReviewDocument[];
  selectedId: string | null;
  onSelect: (findingId: string) => void;
  pending: boolean;
  canDecide?: boolean;
  decisionDisabledReason?: string | null;
  conflictMessage: string | null;
  saveError: string | null;
  onDecide: (input: {
    findingId: string;
    action: DecisionAction;
    reason: string;
    expectedVersion: number;
  }) => Promise<boolean>;
}) {
  const [filter, setFilter] = useState<FindingFilter>("all");
  const [severityFilter, setSeverityFilter] = useState("all");
  const summary = summarizeFindings(findings);
  const byRelation = filterFindings(findings, filter);
  const visible =
    severityFilter === "all"
      ? byRelation
      : byRelation.filter(
          (finding) => (finding.severity ?? "unrated") === severityFilter,
        );
  const selected =
    findings.find((finding) => finding.id === selectedId) ?? null;
  const selectedHidden =
    selected !== null && !visible.some((finding) => finding.id === selected.id);
  const items = severityItems(findings);
  const headline =
    summary.total === 0
      ? "本次运行没有形成风险项"
      : summary.undecided > 0
        ? `${summary.undecided} 项待人工处理`
        : "全部风险项已有处理记录";
  const headlineNote =
    summary.total === 0
      ? "没有形成风险项不等于材料已经无风险。"
      : summary.decided > 0
        ? `已记录 ${summary.decided} 项人工决定，结论以人工决定为准。`
        : "模型建议不会自动结案，完成运行也不代表合同获批。";

  return (
    <aside className="col-findings review-analysis" aria-label="审查结论与风险">
      <Card
        className="review-analysis-overview"
        title="审查结论"
        extra={
          summary.total > 0 ? (
            <span className="meta">{summary.total} 项风险</span>
          ) : null
        }
      >
        <div className="review-overview">
          {summary.total > 0 ? (
            <DonutChart
              items={items}
              size={120}
              centerValue={summary.total}
              centerLabel="风险项"
              ariaLabel={`本次运行风险项 ${summary.total} 项，其中高风险 ${summary.high} 项，中风险 ${summary.medium} 项，低风险 ${summary.low} 项`}
            />
          ) : (
            <div className="review-overview-empty" role="status">
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description="没有风险项"
              />
            </div>
          )}
          <div className="review-overview__body">
            <span className="review-overview__eyebrow">当前尝试</span>
            <strong>{headline}</strong>
            <p>{headlineNote}</p>
            <ChartLegend items={items} total={summary.total} />
          </div>
        </div>
        <p className="review-summary-line" aria-label="审查结论统计">
          <span>
            待人工处理 <strong>{summary.undecided}</strong>
          </span>
          <span>
            已记录决定 <strong>{summary.decided}</strong>
          </span>
          <span>
            冲突 <strong>{summary.conflicts}</strong>
          </span>
          <span>
            信息不足 <strong>{summary.insufficient}</strong>
          </span>
          <span>
            已核验引用 <strong>{summary.verified}</strong>
          </span>
        </p>
      </Card>

      <Card
        className="review-risk-card"
        title="风险项"
        extra={<span className="meta">点击一项查看原文与证据</span>}
      >
        <div className="review-risk-filters">
          <Segmented
            aria-label="筛选线索"
            value={filter}
            onChange={(value) => setFilter(value as FindingFilter)}
            options={FILTERS.map((item) => ({
              value: item.id,
              label: item.label,
            }))}
          />
          <Select
            aria-label="风险等级筛选"
            value={severityFilter}
            onChange={setSeverityFilter}
            options={SEVERITY_OPTIONS}
          />
        </div>
        {findings.length === 0 ? (
          <p className="review-risk-empty">
            这次运行还没有形成风险线索。这不等于材料已经无风险。
          </p>
        ) : null}
        {findings.length > 0 && visible.length === 0 ? (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description="当前筛选没有风险项"
          />
        ) : null}
        <ul className="finding-list review-risk-list">
          {visible.map((finding, index) => {
            const decision = latestDecision(finding);
            const state = decisionSummary(finding);
            const location = findingLocation(finding, documents);
            return (
              <li key={finding.id}>
                <Button
                  htmlType="button"
                  className="finding-button review-risk-item"
                  data-relation={finding.relation}
                  data-severity={finding.severity ?? "unrated"}
                  aria-label={`选择线索：${finding.claim || "未命名线索"}`}
                  aria-current={finding.id === selectedId}
                  onClick={() => onSelect(finding.id)}
                >
                  <span className="review-risk-item__index" aria-hidden="true">
                    {String(index + 1).padStart(2, "0")}
                  </span>
                  <span className="review-risk-item__main">
                    <span className="finding-card-title">
                      {finding.claim || "未命名线索"}
                    </span>
                    <span className="meta review-risk-item__meta">
                      <AimOutlined aria-hidden="true" /> {location}
                    </span>
                  </span>
                  <span className="review-risk-item__tags">
                    <RiskTag severity={finding.severity} />
                    <span
                      className={
                        "review-decision-state" +
                        (state.pending ? " review-decision-state--pending" : "")
                      }
                    >
                      {decision
                        ? `已记录${decisionLabel(decision.action)}`
                        : state.label}
                    </span>
                  </span>
                  <ArrowRightOutlined
                    className="review-risk-item__chevron"
                    aria-hidden="true"
                  />
                </Button>
              </li>
            );
          })}
        </ul>
        {selectedHidden ? (
          <p className="meta">当前线索不在这个筛选结果里，详情仍保留它。</p>
        ) : null}
      </Card>

      {selected ? (
        <FindingDetail
          key={selected.id}
          finding={selected}
          documents={documents}
          pending={pending}
          canDecide={canDecide}
          decisionDisabledReason={decisionDisabledReason}
          conflictMessage={conflictMessage}
          saveError={saveError}
          onDecide={onDecide}
        />
      ) : (
        <Card className="review-analysis-empty">
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description="选择一项风险查看证据与人工处理记录"
          />
        </Card>
      )}
    </aside>
  );
}

function FindingDetail({
  finding,
  documents,
  pending,
  canDecide,
  decisionDisabledReason,
  conflictMessage,
  saveError,
  onDecide,
}: {
  finding: Finding;
  documents: ReviewDocument[];
  pending: boolean;
  canDecide: boolean;
  decisionDisabledReason?: string | null;
  conflictMessage: string | null;
  saveError: string | null;
  onDecide: (input: {
    findingId: string;
    action: DecisionAction;
    reason: string;
    expectedVersion: number;
  }) => Promise<boolean>;
}) {
  const reasonId = useId();
  const hintId = useId();
  const [action, setAction] = useState<DecisionAction | null>(null);
  const [reason, setReason] = useState("");
  const [attempted, setAttempted] = useState(false);
  const reasonError = attempted ? validateDecisionReason(reason) : null;
  const canSubmit =
    canDecide &&
    action !== null &&
    finding.version !== null &&
    validateDecisionReason(reason) === null &&
    !pending;
  const confidence = confidenceLabel(finding.confidence);
  const location = findingLocation(finding, documents);
  const severity = severityLabel(finding.severity);
  const source = originKindLabel(finding.origin);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setAttempted(true);
    if (
      !canDecide ||
      action === null ||
      finding.version === null ||
      validateDecisionReason(reason) !== null
    )
      return;
    const saved = await onDecide({
      findingId: finding.id,
      action,
      reason: reason.trim(),
      expectedVersion: finding.version,
    });
    if (saved) {
      setReason("");
      setAttempted(false);
    }
  }

  return (
    <article className="finding-detail review-analysis-detail" aria-live="polite">
      <header className="review-detail-head">
        <div className="review-detail-tags">
          <RiskTag severity={finding.severity} />
          <Tag color={finding.origin === "rule" ? "blue" : "gold"}>{source}</Tag>
          <Tag color={verificationTone(finding.verification_status)}>
            {verificationLabel(finding.verification_status)}
          </Tag>
        </div>
        <h3>{finding.claim || "未命名线索"}</h3>
        <p className="meta review-detail-location">
          <AimOutlined aria-hidden="true" /> {location}
          {domainLabel(finding.domain)
            ? ` · ${domainLabel(finding.domain)}`
            : ""}
        </p>
      </header>

      <Tabs
        className="review-detail-tabs"
        defaultActiveKey="description"
        items={[
          {
            key: "description",
            label: "问题描述",
            forceRender: true,
            children: (
              <div className="review-detail-tab">
                <p>{finding.claim || "服务没有返回这项风险的问题描述。"}</p>
                <p className="meta">{originExplanation(finding.origin)}</p>
                <dl className="review-detail-dl">
                  <div>
                    <dt>来源</dt>
                    <dd>{source}</dd>
                  </div>
                  <div>
                    <dt>风险关系</dt>
                    <dd>{relationLabel(finding.relation)}</dd>
                  </div>
                  <div>
                    <dt>风险等级</dt>
                    <dd>{severity}</dd>
                  </div>
                  <div>
                    <dt>核验状态</dt>
                    <dd>{verificationLabel(finding.verification_status)}</dd>
                  </div>
                  {confidence ? (
                    <div>
                      <dt>模型置信</dt>
                      <dd>{confidence}</dd>
                    </div>
                  ) : null}
                </dl>
                {finding.unverified_reason ? (
                  <p className="review-doc-callout">
                    尚未核验：{finding.unverified_reason}
                  </p>
                ) : null}
              </div>
            ),
          },
          {
            key: "analysis",
            label: "风险分析",
            forceRender: true,
            children: (
              <div className="review-detail-tab">
                {finding.relation === "conflict" ? (
                  <p>这份发现与材料中的其他约定存在冲突，需要人工核对适用条款。</p>
                ) : finding.relation === "insufficient" ? (
                  <p>现有材料不足以支持确定结论，需要补充信息或人工判断。</p>
                ) : finding.relation === "compatible" ? (
                  <p>这份发现记录为一致项，不等于合同已经获批。</p>
                ) : (
                  <p>服务返回了尚未识别的风险关系，未作推断。</p>
                )}
                {finding.severity_source ? (
                  <section className="review-severity-source">
                    <h4>分级依据</h4>
                    <p>
                      {finding.severity_source.policy_id} v
                      {finding.severity_source.policy_version} 第
                      {finding.severity_source.clause}条：
                      {finding.severity_source.reason}
                    </p>
                  </section>
                ) : (
                  <p className="meta">
                    服务未返回分级依据。风险等级来自审查结果，不替代法律判断。
                  </p>
                )}
                <p className="meta">
                  风险等级用于排序和筛选，不是风险可信度；是否成立由人工决定。
                </p>
              </div>
            ),
          },
          {
            key: "evidence",
            label: "证据与依据",
            forceRender: true,
            children: (
              <div className="review-detail-tab">
                <p className="meta">
                  合同原文、检索依据与人工决定分别保留；前面的页码只在引用能够
                  对回冻结正文时显示。
                </p>
                <EvidenceList finding={finding} documents={documents} />
              </div>
            ),
          },
          {
            key: "suggestion",
            label: "修改建议",
            forceRender: true,
            children: (
              <div className="review-detail-tab">
                <p>
                  接口没有返回逐条修改建议，本页不生成建议，也不把模型输出当作
                  已完成整改。
                </p>
                {finding.evidence.length > 0 ? (
                  <>
                    <h4>可供人工修改时对照的原文</h4>
                    <ul className="review-suggestion-quotes">
                      {finding.evidence.map((anchor) => (
                        <li key={anchor.id}>
                          <blockquote>
                            {anchor.quote || "这条证据没有引用快照。"}
                          </blockquote>
                          <span className="meta">
                            {anchor.document_version_id}
                            {anchor.page !== null
                              ? ` · 记录页码 ${anchor.page}`
                              : " · 页码未报告"}
                          </span>
                        </li>
                      ))}
                    </ul>
                  </>
                ) : (
                  <p className="meta">这条风险没有附带可对照的引用原文。</p>
                )}
              </div>
            ),
          },
          {
            key: "history",
            label: `人工处理 ${finding.decisions.length}`,
            forceRender: true,
            children: (
              <div className="review-detail-tab">
                {finding.decisions.length === 0 ? (
                  <p>还没有人工决定。模型建议不会自动结案。</p>
                ) : (
                  <ol className="history">
                    {finding.decisions.map((decision) => (
                      <li key={decision.id}>
                        <p>
                          <strong>{decisionLabel(decision.action)}</strong> ·{" "}
                          {decision.actor} ·{" "}
                          <time dateTime={decision.decided_at}>
                            {formatDateTime(decision.decided_at)}
                          </time>
                        </p>
                        <p>{decision.reason}</p>
                      </li>
                    ))}
                  </ol>
                )}
                <p className="meta">
                  人工决定只追加历史，不会覆盖旧记录。
                </p>
              </div>
            ),
          },
        ]}
      />

      <form className="review-decision-form" onSubmit={onSubmit}>
        {!canDecide ? (
          <p className="review-decision-disabled">
            {decisionDisabledReason ?? "当前角色不能提交人工决定。"}
          </p>
        ) : null}
        <Card
          role="group"
          aria-label={String(
            finding.decisions.length > 0 ? "更正决定" : "人工决定",
          )}
          title={
            <>
              {finding.decisions.length > 0 ? "更正决定" : "人工决定"}
            </>
          }
          extra={
            <span className="meta">
              结论以人工决定为准
            </span>
          }
        >
          {finding.decisions.length > 0 ? (
            <p className="meta">
              更正会追加一条新记录，不会抹掉原来的决定。
            </p>
          ) : null}
          <div className="review-decision-choices">
            {DECISION_ACTIONS.map((item) => (
              <div key={item.action} className="choice">
                <Radio
                  disabled={!canDecide}
                  name={`decision-${finding.id}`}
                  value={item.action}
                  checked={action === item.action}
                  onChange={() => setAction(item.action)}
                >
                  {item.label}
                </Radio>
              </div>
            ))}
          </div>
        </Card>
        <label className="field" htmlFor={reasonId}>
          理由
        </label>
        <Input.TextArea
          id={reasonId}
          value={reason}
          onChange={(event) => setReason(event.target.value)}
          aria-describedby={hintId}
          rows={3}
          disabled={!canDecide}
        />
        <p id={hintId} className="meta">
          四种决定都要写理由。保存后写入审查记录。
        </p>
        {finding.version === null ? (
          <p role="status">
            服务没有返回这条线索的版本号，暂不能保存，以免覆盖其他人的决定。
          </p>
        ) : null}
        {reasonError ? <p role="alert">{reasonError}</p> : null}
        {conflictMessage ? <p role="alert">{conflictMessage}</p> : null}
        {saveError ? <p role="alert">{saveError}</p> : null}
        <Button
          aria-label={String(pending ? "正在保存" : "保存决定")}
          type="primary"
          htmlType="submit"
          disabled={!canSubmit}
        >
          {pending ? "正在保存" : "保存决定"}
        </Button>
      </form>
    </article>
  );
}
