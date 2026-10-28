import { Collapse } from "antd";
import { Link } from "react-router";
import type { ReviewWorkspace } from "../api/types";
import { decisionLabel, relationLabel } from "../domain/review";

/** Presents existing API evidence. It does not claim frozen OCR or report exports. */
export function RunProvenance({ workspace, showDiagnostics = false }: { workspace: ReviewWorkspace; showDiagnostics?: boolean }) {
  const {
    snapshot,
    evaluations = [],
    revisions = [],
    previous_run_id: previous,
  } = workspace;
  return (
    <section className="review-provenance" aria-label="运行依据与复审">
      <Collapse
        items={[
          {
            key: "content",
            label: "运行依据与规则评估",
            children: (
              <>
                {snapshot && showDiagnostics ? (
                  <dl>
                    <dt>本次材料版本</dt>
                    <dd>{snapshot.document_ids.join("、") || "未返回"}</dd>
                    <dt>规则集摘要</dt>
                    <dd>{snapshot.rule_set_hash || "未返回"}</dd>
                    <dt>审查清单摘要</dt>
                    <dd>{snapshot.playbook_hash || "未返回"}</dd>
                    <dt>比较器</dt>
                    <dd>{snapshot.comparator_ref ?? "未返回"}</dd>
                    <dt>本次审查配置的模型</dt>
                    <dd>{snapshot.model_ref || "未记录"}</dd>
                  </dl>
                ) : snapshot ? (
                  <p>请核对本次审查的材料与引用。</p>
                ) : (
                  <p>未获取到本次审查的材料和规则记录。</p>
                )}
                <p className="meta">
                  材料与引用以本次审查记录为准。
                </p>
                {evaluations.length === 0 ? (
                  <p>没有返回规则评估记录。</p>
                ) : (
                  <ul className="history">
                    {evaluations.map((item) => (
                      <li key={item.id}>
                        <strong>
                          {item.domain}，{relationLabel(item.relation)}
                        </strong>
                        <p>{item.reason}</p>
                        {showDiagnostics ? (
                          <>
                        <p className="meta">
                          {item.rule_id || "规则身份未返回"}，
                          {item.rule_version || "版本未返回"}，
                          {item.capability_id || "能力身份未返回"}
                        </p>
                        <p className="meta">
                          业务对象：
                          {item.object_refs.length === 0
                            ? "未返回"
                            : item.object_refs.join("、")}，规则依据：{item.reason_code || "未返回"}，事实：
                          {item.fact_ids.length === 0
                            ? "未返回"
                            : item.fact_ids.join("、")}
                        </p>
                          </>
                        ) : null}
                      </li>
                    ))}
                  </ul>
                )}
              </>
            ),
          },
        ]}
      />
      {previous ? (
        <Collapse
          items={[
            {
              key: "content",
              label: "修改稿对照",
              children: (
                <>
                  <Link
                    to={`/cases/${workspace.run.case_id}/reviews/${previous}`}
                  >
                    查看上一轮审查
                  </Link>
                  <p className="meta">
                    对照使用规则要求和已核对的原文。之前的判断仅供参考，不会自动用于本次审查。
                  </p>
                  {workspace.revision_comparison_reason ? (
                    <p className="callout">
                      {workspace.revision_comparison_reason}
                    </p>
                  ) : null}
                  {revisions.length === 0 ? (
                    <p>没有返回可比较的线索。</p>
                  ) : (
                    <ul className="history">
                      {revisions.map((item, index) => (
                        <li
                          key={`${item.finding_id ?? item.previous_finding_id ?? "row"}-${index}`}
                        >
                          <strong>
                            {item.mark}，{item.domain}
                          </strong>
                          <p>{item.claim}</p>
                          {item.prior_decision ? (
                            <p>
                              先前决定：
                              {decisionLabel(item.prior_decision.action)}，
                              {item.prior_decision.actor}，
                              {item.prior_decision.reason}
                            </p>
                          ) : null}
                        </li>
                      ))}
                    </ul>
                  )}
                </>
              ),
            },
          ]}
        />
      ) : null}
    </section>
  );
}
