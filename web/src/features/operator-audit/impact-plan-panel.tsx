import { Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import { StatusText } from "../../components/status-text";
import {
  DEFAULT_OPERATOR_AUDIT_FLAGS,
  type OperatorAuditFlags,
} from "./feature-flags";
import {
  dataClassLabel,
  dataClassTone,
  dependencyCompleteLabel,
  impactActionLabel,
  impactActionTone,
  impactFallbackLabel,
  impactModeLabel,
  impactModeTone,
  impactReasonLabel,
} from "./labels";
import type { ImpactCheck, ImpactPlan } from "./types";

export interface ImpactPlanPanelProps {
  plan: ImpactPlan;
  flags?: OperatorAuditFlags;
}

const checkColumns: ColumnsType<ImpactCheck> = [
  {
    title: "检查",
    key: "check",
    render: (_, check) => <span className="digest">{check.checkId}</span>,
  },
  {
    title: "动作",
    key: "action",
    render: (_, check) => (
      <>
        <StatusText tone={impactActionTone(check.action)}>
          {impactActionLabel(check.action)}
        </StatusText>
        {check.action === "reuse_candidate" ? (
          <div className="meta">仅候选，未跳过执行</div>
        ) : null}
      </>
    ),
  },
  {
    title: "原因",
    key: "reasons",
    render: (_, check) =>
      check.reasons.length > 0 ? (
        <ul>
          {check.reasons.map((reason, index) => (
            <li key={`${reason}-${index}`}>
              {impactReasonLabel(reason)}
              <span className="meta">（{reason}）</span>
            </li>
          ))}
        </ul>
      ) : (
        "原因未报告"
      ),
  },
];

/** Default off; explain/shadow must be enabled by the Lead's feature config. */
export function ImpactPlanPanel({
  plan,
  flags = DEFAULT_OPERATOR_AUDIT_FLAGS,
}: ImpactPlanPanelProps) {
  if (flags.reviewImpactMode === "off") return null;

  const sourceAnomaly = plan.businessResultSource !== "full";
  const warnings: string[] = [];
  if (plan.mode === "unknown")
    warnings.push("模式未报告；不视为可执行计划。");
  if (flags.reviewImpactMode === "explain" && plan.mode === "shadow")
    warnings.push(
      "当前开关只启用解释模式，服务端 shadow 结果仅作对照，不改变业务结果。",
    );
  if (plan.dependencyComplete === null)
    warnings.push("依赖完整性未报告，不能据此复用。");
  if (plan.dependencyComplete === false && plan.fallback === "none")
    warnings.push("依赖不完整但未报告回退策略，本计划不能用于跳过检查。");
  if (plan.fallback === "full")
    warnings.push("依赖不完整，按完整执行重新计算，不采用复用。");
  if (plan.fallback === "deny")
    warnings.push("依赖或授权无法证明，拒绝复用；请按完整执行或人工处理。");

  return (
    <section className="workflow-panel" aria-label="影响解释与影子对照">
      <header className="workflow-panel-head">
        <div>
          <h2>影响解释与影子对照</h2>
          <p className="meta">
            解释依赖变化对检查的影响；不改变业务结果，也不继承人工确认。
          </p>
        </div>
        <StatusText tone={impactModeTone(plan.mode)}>
          {impactModeLabel(plan.mode)}
        </StatusText>
      </header>

      <p className="callout">
        影响计划只用于解释与影子对照；业务结果始终来自完整执行（full）。
      </p>

      {sourceAnomaly ? (
        <p className="workflow-error" role="alert">
          业务结果来源异常（{plan.businessResultSource ?? "未报告"}
          ）；本计划不能作为业务结论。
        </p>
      ) : null}
      {warnings.map((warning, index) => (
        <p className="callout" key={`warning-${index}`}>
          {warning}
        </p>
      ))}

      <dl className="execution-dl">
        <div>
          <dt>基线评估</dt>
          <dd className="digest">{plan.baseEvaluationId ?? "未报告"}</dd>
        </div>
        <div>
          <dt>目标评估</dt>
          <dd className="digest">{plan.targetEvaluationId ?? "未报告"}</dd>
        </div>
        <div>
          <dt>依赖完整性</dt>
          <dd>{dependencyCompleteLabel(plan.dependencyComplete)}</dd>
        </div>
        <div>
          <dt>回退</dt>
          <dd>{impactFallbackLabel(plan.fallback)}</dd>
        </div>
        <div>
          <dt>回退原因</dt>
          <dd>{plan.fallbackReason ?? "未报告"}</dd>
        </div>
        <div>
          <dt>变更输入</dt>
          <dd>
            {plan.changedInputs.length > 0
              ? plan.changedInputs.join("、")
              : "没有报告变更输入"}
          </dd>
        </div>
        <div>
          <dt>业务结果来源</dt>
          <dd>
            {sourceAnomaly
              ? (plan.businessResultSource ?? "未报告")
              : "full（完整执行）"}
          </dd>
        </div>
        <div>
          <dt>数据分类</dt>
          <dd>
            <StatusText tone={dataClassTone(plan.dataClass)}>
              {dataClassLabel(plan.dataClass)}
            </StatusText>
          </dd>
        </div>
      </dl>

      <h3>受影响检查</h3>
      <Table<ImpactCheck>
        rowKey={(check) => check.checkId}
        dataSource={plan.checks}
        columns={checkColumns}
        pagination={false}
        size="small"
        locale={{ emptyText: "计划未列出任何检查（不代表没有影响）。" }}
      />

      <p className="meta">
        “预计可复用”只是候选，本次没有跳过任何检查；业务结果仍来自完整执行。
      </p>
      {!flags.incrementalReuseEnabled ? (
        <p className="meta">
          增量复用开关未启用，所有检查仍按完整执行计算。
        </p>
      ) : null}
      {plan.dataClass === "synthetic" ? (
        <p className="meta">
          合成 fixture 计划：仅用于演示与测试，不代表生产结果。
        </p>
      ) : null}
    </section>
  );
}
