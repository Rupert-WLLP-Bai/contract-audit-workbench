import { FileSearchOutlined } from "@ant-design/icons";
import { Button, Drawer, Tag } from "antd";
import type { ReportDetail, ReportFinding } from "../api/reports";
import {
  dispositionLabel,
  findingDecisions,
  reportAnchorLocation,
  reportDocument,
} from "../domain/report";
import { domainLabel } from "../domain/review-presentation";
import {
  decisionLabel,
  formatDateTime,
  originLabel,
  relationLabel,
  roleLabel,
} from "../domain/review";
import { RiskTag } from "./ui";

export function ReportFindingDrawer({
  report,
  finding,
  onClose,
  onViewSource,
}: {
  report: ReportDetail;
  finding: ReportFinding | null;
  onClose: () => void;
  onViewSource: (findingId: string) => void;
}) {
  const decisions = finding ? findingDecisions(report, finding.id) : [];
  const open = finding !== null;

  return (
    <Drawer
      className="report-finding-drawer"
      size={560}
      open={open}
      onClose={onClose}
      title={finding ? finding.claim || finding.id : "发现详情"}
      destroyOnHidden
    >
      {finding ? (
        <div className="report-finding">
          <div className="report-finding__tags">
            <RiskTag severity={finding.severity} />
            <Tag>{originLabel(finding.origin)}</Tag>
            <Tag color="blue">{relationLabel(finding.relation)}</Tag>
            <Tag>{domainLabel(finding.domain) ?? "领域未报告"}</Tag>
          </div>

          <dl className="report-finding__fields">
            <div>
              <dt>发现 ID</dt>
              <dd>
                <code>{finding.id}</code>
              </dd>
            </div>
            <div>
              <dt>规则</dt>
              <dd>{finding.rule_id ?? "未报告规则"}</dd>
            </div>
            <div>
              <dt>能力项</dt>
              <dd>{finding.capability_id ?? "未报告能力项"}</dd>
            </div>
            <div>
              <dt>对象</dt>
              <dd>{finding.object_ref ?? "未报告"}</dd>
            </div>
            <div>
              <dt>发现版本</dt>
              <dd>{finding.version ?? "未报告"}</dd>
            </div>
          </dl>

          <section className="report-finding__section" aria-labelledby="report-finding-claim">
            <h3 id="report-finding-claim">问题描述</h3>
            <p>{finding.claim || "服务未返回问题描述。"}</p>
          </section>

          <section className="report-finding__section" aria-labelledby="report-finding-evidence">
            <h3 id="report-finding-evidence">证据与依据</h3>
            {finding.anchors.length === 0 ? (
              <p>这条发现没有附原文引用，不能当成已验证结论。</p>
            ) : (
              <ul className="report-finding__anchors">
                {finding.anchors.map((anchor) => {
                  const document = reportDocument(report, anchor.document_id);
                  const location = document
                    ? reportAnchorLocation(anchor, document)
                    : "找不到对应来源材料";
                  return (
                    <li key={anchor.id}>
                      <div className="report-finding__anchor-head">
                        <strong>
                          {document
                            ? `${roleLabel(document.role)} · ${document.filename}`
                            : anchor.document_id}
                        </strong>
                        <Tag
                          color={
                            location.startsWith("已在冻结正文") ? "success" : "warning"
                          }
                        >
                          {location}
                        </Tag>
                      </div>
                      <blockquote>“{anchor.quote || "服务未返回引用原文"}”</blockquote>
                      {anchor.mismatch_reason ? (
                        <p className="report-finding__note">
                          {anchor.mismatch_reason}
                        </p>
                      ) : null}
                    </li>
                  );
                })}
              </ul>
            )}
            <Button
              type="link"
              icon={<FileSearchOutlined />}
              onClick={() => onViewSource(finding.id)}
            >
              在冻结原文中查看
            </Button>
          </section>

          <section className="report-finding__section" aria-labelledby="report-finding-suggestion">
            <h3 id="report-finding-suggestion">处置建议</h3>
            <p>{dispositionLabel(finding.disposition_suggestion)}</p>
            <p className="report-finding__note">
              规则或模型建议不是人工结论；是否确认风险以冻结的人工处理记录为准。
            </p>
          </section>

          <section className="report-finding__section" aria-labelledby="report-finding-decisions">
            <h3 id="report-finding-decisions">人工处理记录</h3>
            {decisions.length === 0 ? (
              <p>尚无人工决定，这条发现仍待人工处理。</p>
            ) : (
              <ol className="report-finding__decisions">
                {decisions.map((decision) => (
                  <li key={decision.id}>
                    <div className="report-finding__decision-head">
                      <strong>{decisionLabel(decision.action)}</strong>
                      <time dateTime={decision.created_at}>
                        {formatDateTime(decision.created_at)}
                      </time>
                    </div>
                    <p>
                      {decision.actor_name || decision.actor_id || "记录人未报告"}
                      {decision.old_disposition || decision.new_disposition
                        ? ` · ${decision.old_disposition || "未报告"} → ${decision.new_disposition || "未报告"}`
                        : ""}
                    </p>
                    <p>{decision.reason || "决定理由未报告"}</p>
                  </li>
                ))}
              </ol>
            )}
            <p className="report-finding__note">
              人工决定按时间追加保存；这里只读取冻结报告中的记录，不提供改写。
            </p>
          </section>
        </div>
      ) : null}
    </Drawer>
  );
}
