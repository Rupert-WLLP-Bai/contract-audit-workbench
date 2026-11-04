import type { ReviewWorkspace } from "../api/types";
import { runLabel } from "../domain/review";
import { agentStage, canRestartUserCancelled, hasFrozenParseFailure, hasTransientRetryCause, limitedPrecheck, selectedOutcome, unresolvedGapKinds } from "../features/review/review-outcome";
import { singleContractCoverage } from "../features/review/single-contract-coverage";

export function ReviewOutcomeSummary({ workspace }: { workspace: ReviewWorkspace }) {
  const coverage = singleContractCoverage(workspace);
  const outcome = selectedOutcome(workspace);
  const limited = limitedPrecheck(workspace);
  const stage = agentStage(workspace);
  const undecided = workspace.findings.filter((finding) => finding.decisions.length === 0).length;
  const gaps = workspace.agent_review?.attempt_id === workspace.review_attempts?.attempt.id
    ? workspace.agent_review?.unresolved_gap_ids : null;
  const gapKinds = unresolvedGapKinds(workspace);
  const parseFailed = hasFrozenParseFailure(workspace);
  const capabilities = workspace.agent_review?.attempt_id === workspace.review_attempts?.attempt.id
    ? workspace.agent_review?.domains?.flatMap((domain) => domain.capabilities ?? []) ?? [] : [];
  const completedCapabilities = capabilities.filter((item) => item.status === "completed").length;
  const payment = coverage.kind === "known" ? {
    insufficient: "已执行主合同付款比例识别，信息仍不足，需核对原文。",
    not_executed: "主合同未完成解析，付款比例识别未执行。",
    pending: "付款比例识别结果尚未返回。",
    failed: "未返回付款比例识别结果，无法确认失败前是否执行。",
    cancelled: "未返回付款比例识别结果，无法确认取消前是否执行。",
    unconfirmed: "付款比例识别状态待核对。",
  }[coverage.payment.status] : null;
  const agent = {
    completed: "智能审查阶段已返回结果，仍需核对其实际能力与证据。",
    skipped: "智能审查阶段未执行。",
    partial: "智能审查存在未完成的检查或能力缺口。",
    failed: "智能审查执行失败，已有规则结果仍需人工核对。",
    unknown: "智能审查阶段结果未能确认，请查看已有结果或联系管理员核对。",
  }[stage] ?? "智能审查阶段结果未能确认。";
  const casePath = `/cases/${encodeURIComponent(workspace.run.case_id)}`;
  const gapPath = `${casePath}/reviews/${encodeURIComponent(workspace.run.id)}?attempt_id=${encodeURIComponent(workspace.review_attempts?.attempt.id ?? "")}#agent-review-gaps`;
  return <section aria-label="审查结果、范围与人工进度" className="callout">
    <h2>审查结果、范围与人工进度</h2>
    <p><strong>执行结果：</strong>{limited ? "单合同预审已结束，执行记录保留为部分完成。" : `所选执行记录：${runLabel(workspace.run.state)}。`}</p>
    {outcome?.reason === "transient_failure" ? <p>{hasTransientRetryCause(workspace) ? "模型调用超时；服务恢复后可在原审查中尝试重试，仍需服务端核验。" : "模型调用超时；冻结材料或当前条件尚需核对，不能确认可否重试。"}</p> : null}
    {outcome?.reason === "material_unavailable" || parseFailed ? <p>有材料未完成解析，未执行的检查不能当作完整审查。</p> : null}
    {outcome?.reason === "dependency_unavailable" ? <p>审查依赖尚未就绪，请联系管理员核对。</p> : null}
    {outcome?.reason === "execution_failed" ? <p>执行发生故障，原因与恢复条件需要进一步核对。</p> : null}
    {outcome?.reason === "review_incomplete" ? <p>部分审查或所需证据尚未完成，请核对现有结果和缺项。</p> : null}
    {outcome?.reason === "cancelled_by_user" ? <p>{canRestartUserCancelled(workspace) ? "审查由用户主动取消；可由有权人员选择重新发起，服务端会核验冻结输入。" : "审查由用户主动取消；冻结输入尚不能核对，请查看审查准备。"}</p> : null}
    {outcome?.reason === "cancelled" ? <p>取消原因未确认，请核对当前身份权限及审查记录。</p> : null}
    {outcome?.reason === "authorization_revoked" ? <p>本次执行因授权变化而取消，请联系案件管理员核对权限。</p> : null}
    <p>{agent}</p>
    {gapKinds.materials ? <p>智能审查存在缺少材料或字段的未解决事项，请补充相关材料并核对原文。</p> : null}
    {gapKinds.budget ? <p>智能审查存在调用预算耗尽的未解决事项，请核对预算与范围。</p> : null}
    {gapKinds.unsupported ? <p>部分检查能力或来源表达暂不支持，请人工核对并查看适用规则。</p> : null}
    {gapKinds.budget || gapKinds.unsupported ? <p><a href={gapPath}>查看该次执行的审查缺口</a></p> : null}
    <p><strong>实际范围：</strong>{payment ?? "当前记录没有可核对的具体覆盖结果，请查看审查明细；不能推断未展示的项目已查。"}
      {coverage.kind === "known" ? "跨材料对照、制度与法律依据及其他风险不在本次单合同预审范围，未执行。" : null}</p>
    {capabilities.length > 0 ? <p>智能审查返回 {capabilities.length} 项能力状态，其中 {completedCapabilities} 项标记为完成；其他项目需逐项核对，不能推断已查。</p> : null}
    <p><strong>人工进度：</strong>{undecided > 0 ? `本次结果中 ${undecided} 项尚无人工判断。` : "本次结果中没有尚无人工判断的发现。"}
      {gaps === null || gaps === undefined ? "智能审查缺项状态未确认。" : gaps.length > 0 ? `智能审查另有 ${gaps.length} 项未解决的缺口。` : ""}
      人工进度仅反映已返回的发现；审查建议不能替代人工判断，未检查的范围不能据此认定合规。</p>
    {outcome?.reason === "material_unavailable" || parseFailed || gapKinds.materials ? <p><a href={`${casePath}#case-materials`}>返回案件补齐或处理材料</a></p> : null}
    {outcome?.reason === "cancelled_by_user" && !canRestartUserCancelled(workspace) ? <p><a href={`${casePath}#review-readiness`}>返回审查准备清单</a></p> : null}
    {limited ? <p><a href={`${casePath}#review-readiness`}>查看完整审查准备</a></p> : null}
  </section>;
}
