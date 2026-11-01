import { Button } from "antd";
import type { ReviewReadinessScope } from "../api/review-readiness";
import type { ReviewWorkspace } from "../api/types";
import { isSingleContractV1, singleContractCoverage } from "../features/review/single-contract-coverage";
import { StatusText } from "./status-text";
import "./single-contract-scope-coverage.css";

type Props =
  | { mode: "readiness"; scope: ReviewReadinessScope | undefined; onSelectFullReview: () => void; disabled: boolean }
  | { mode: "workspace"; workspace: ReviewWorkspace };

function PaymentExpressions() {
  return <p>
    仅匹配部分使用阿拉伯数字的固定表达，例如“预付款20%”“20%预付款”“预付款百分之20”，
    以及“验收通过后支付70%”“设备到货后70%”“到货后支付70%”“质保期满后10%”。这些节点须紧接“支付”或比例。
    其他写法可能无法识别；未识别不代表合同没有约定付款，证据不足时需要人工核对原文。
  </p>;
}

function ScopeLimits({ started }: { started: boolean }) {
  return <li>
    <div className="single-contract-coverage-row">
      <strong>跨材料对照、制度与法律依据及其他风险</strong>
      <StatusText tone="neutral">{started ? "范围外，未执行" : "范围外，不执行"}</StatusText>
    </div>
    <p>本次不比较采购、审批等材料，不建立适用制度和法律依据，也不覆盖其他合同风险。</p>
  </li>;
}

function ReadinessCoverage({ scope, onSelectFullReview, disabled }: Extract<Props, { mode: "readiness" }>) {
  if (!isSingleContractV1(scope?.rule_set)) {
    return <section className="single-contract-coverage" aria-label="单合同预审范围">
      <h3>单合同预审范围</h3>
      <p><StatusText tone="attention">范围身份待核对</StatusText> 服务端未返回可识别的规则身份，不能推断具体检查能力。</p>
    </section>;
  }
  const payment = scope?.checks.find((check) => check.rule_id === "single-payment-sum-v1");
  return <section className="single-contract-coverage" aria-label="单合同预审范围">
    <h3>单合同预审范围</h3>
    <p className="single-contract-coverage-summary">这一次新审查尚未开始，以下检查尚未执行。可否开始仍以服务端准备清单和权限校验为准。</p>
    <ul className="single-contract-coverage-list">
      <li>
        <div className="single-contract-coverage-row">
          <strong>主合同付款比例识别</strong>
          <StatusText tone={payment?.status === "ready" ? "neutral" : "attention"}>
            {payment?.status === "ready" ? "检查已准备，尚未执行" : "尚未执行，准备状态见清单"}
          </StatusText>
        </div>
        <PaymentExpressions />
      </li>
      <li>
        <div className="single-contract-coverage-row"><strong>付款比例的完整性与共同基数</strong><StatusText tone="attention">无法确认</StatusText></div>
        <p>不能确认是否遗漏付款条款、各比例是否使用共同基数；即使识别合计为100%，也不是一致或合规结论。</p>
      </li>
      <ScopeLimits started={false} />
    </ul>
    <p>需补齐采购、审批等对照材料，选择完整审查并按页面下一步提示发起新审查。</p>
    <div className="single-contract-coverage-actions">
      <a href="#case-materials">补齐或确认对照材料</a>
      <Button type="link" htmlType="button" onClick={onSelectFullReview} disabled={disabled}>查看完整审查准备</Button>
    </div>
  </section>;
}

function WorkspaceCoverage({ workspace }: Extract<Props, { mode: "workspace" }>) {
  const coverage = singleContractCoverage(workspace);
  if (coverage.kind === "unknown") {
    return <section className="single-contract-coverage single-contract-coverage-workspace" aria-label="本次审查范围">
      <h2>本次审查范围</h2>
      <p><StatusText tone="attention">范围身份未确认</StatusText> 所选执行记录没有可核对的范围身份或冻结记录，不能推断具体检查范围和执行情况。</p>
    </section>;
  }
  const labels = {
    pending: "尚未返回识别结果",
    failed: "执行失败或中断",
    cancelled: "执行已取消",
    unconfirmed: "识别状态待核对",
    not_executed: "未执行付款识别",
    insufficient: "已执行识别，信息不足",
  } as const;
  const descriptions = {
    pending: "付款识别结果尚未返回，请等待本次执行更新。",
    failed: "未返回付款识别结果，无法确认失败前是否执行。",
    cancelled: "未返回付款识别结果，无法确认取消前是否执行。",
    unconfirmed: "未返回完整且可对应的付款识别记录，不能推断是否执行或得到结果。",
    not_executed: "主合同未完成解析，付款识别未执行。",
    insufficient: "付款识别已执行，结果仍需结合冻结原文和证据人工核对。",
  } as const;
  const { payment } = coverage;
  return <section className="single-contract-coverage single-contract-coverage-workspace" aria-label="本次单合同预审范围">
    <h2>本次单合同预审范围</h2>
    <p className="single-contract-coverage-summary">以下内容对应所选的第 {coverage.attemptOrdinal} 次执行。之后补充材料或发起完整审查，不会改变本次预审的范围。</p>
    <ul className="single-contract-coverage-list">
      <li>
        <div className="single-contract-coverage-row">
          <strong>主合同付款比例识别</strong>
          <StatusText tone={payment.status === "pending" ? "progress" : "attention"}>{labels[payment.status]}</StatusText>
        </div>
        <p>{descriptions[payment.status]}</p>
        {payment.reason ? <p>{payment.reason}</p> : null}
        <PaymentExpressions />
      </li>
      <li>
        <div className="single-contract-coverage-row"><strong>付款比例的完整性与共同基数</strong><StatusText tone="attention">无法确认</StatusText></div>
        <p>不能确认是否遗漏付款条款、各比例是否使用共同基数；即使识别合计为100%，也不是一致或合规结论。</p>
      </li>
      <ScopeLimits started />
    </ul>
    <p>补齐采购、审批等对照材料后，在案件的审查准备清单选择完整审查，再按下一步提示发起新审查。</p>
    <div className="single-contract-coverage-actions"><a href={`/cases/${encodeURIComponent(workspace.run.case_id)}#case-materials`}>返回案件补齐对照材料</a></div>
  </section>;
}

export function SingleContractScopeCoverage(props: Props) {
  return props.mode === "readiness" ? <ReadinessCoverage {...props} /> : <WorkspaceCoverage {...props} />;
}
