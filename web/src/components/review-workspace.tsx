import { Button, Select } from "antd";
import type {
  CaseRelationInput,
  DecisionAction,
  ReviewWorkspace,
} from "../api/types";
import type { ReviewLayout } from "../domain/review";
import { runLabel, runTone } from "../domain/review";
import type { StreamStatus } from "../hooks/use-review-events";
import { DocumentStage, type ReviewCaseContext } from "./document-stage";
import { ExecutionInspector } from "./execution-inspector";
import { FindingPanel } from "./finding-panel";
import { BusinessAgentReviewView } from "./business-agent-review";
import { TrajectoryView } from "./trajectory-view";
import { StatusText } from "./status-text";

const STREAM_LABEL: Record<StreamStatus, string> = {
  connecting: "正在连接运行状态",
  live: "运行状态已连接",
  reconnecting: "连接中断，正在按游标重连",
  closed: "运行状态流已关闭，不会自动重连；请刷新页面或重新登录后再试。",
  inactive: "本次执行没有活动的事件流",
};

export function ReviewWorkspaceView({
  workspace,
  layout,
  selectedId,
  streamStatus,
  showDiagnostics = false,
  pending,
  retrying,
  canDecide = true,
  decisionDisabledReason = null,
  canRetry = true,
  canCancel = false,
  cancelling = false,
  cancelError = null,
  diagnosticsLoading = false,
  conflictMessage,
  saveError,
  onLoadDiagnostics,
  onSelect,
  onSelectAttempt,
  onDecide,
  onRetry,
  onCancel,
  caseContext,
  canEditRelations = false,
  relationPending = false,
  relationError = null,
  onCreateRelation,
  onDeleteRelation,
}: {
  workspace: ReviewWorkspace;
  layout: ReviewLayout;
  selectedId: string | null;
  streamStatus: StreamStatus;
  showDiagnostics?: boolean;
  pending: boolean;
  retrying: boolean;
  canDecide?: boolean;
  decisionDisabledReason?: string | null;
  canRetry?: boolean;
  canCancel?: boolean;
  cancelling?: boolean;
  cancelError?: string | null;
  diagnosticsLoading?: boolean;
  conflictMessage: string | null;
  saveError: string | null;
  onLoadDiagnostics?: () => void;
  onSelect: (findingId: string) => void;
  onSelectAttempt: (attemptId: string) => void;
  onDecide: (input: {
    findingId: string;
    action: DecisionAction;
    reason: string;
    expectedVersion: number;
  }) => Promise<boolean>;
  onRetry: () => void;
  onCancel: () => void;
  caseContext?: ReviewCaseContext | null;
  canEditRelations?: boolean;
  relationPending?: boolean;
  relationError?: string | null;
  onCreateRelation?: (input: CaseRelationInput) => Promise<void>;
  onDeleteRelation?: (relationId: string) => void;
}) {
  const { run } = workspace;
  const selected = workspace.review_attempts?.attempt;
  const attempts = workspace.review_attempts
    ? workspace.review_attempts.attempts.some((attempt) => attempt.id === selected?.id)
      ? workspace.review_attempts.attempts
      : [workspace.review_attempts.attempt, ...workspace.review_attempts.attempts]
    : [];
  const anchors = workspace.findings.flatMap((finding) => finding.evidence);
  const locatedAnchors = anchors.filter(
    (anchor) => anchor.status === "located",
  ).length;
  const decidedFindings = workspace.findings.filter(
    (finding) => finding.decisions.length > 0,
  ).length;
  const retryableState =
    run.state === "failed" ||
    run.state === "partial" ||
    run.state === "cancelled" ||
    run.state === "interrupted";
  return (
    <div className="workspace">
      <div className="workspace-banner">
        <StatusText tone={runTone(run.state)}>{runLabel(run.state)}</StatusText>
        <span>{STREAM_LABEL[streamStatus]}</span>
        {run.model_ref ? <span>模型 {run.model_ref}</span> : null}
        {workspace.execution ? (
          <a className="workspace-trace-link" href="#review-trajectory">
            查看运行轨迹
          </a>
        ) : onLoadDiagnostics ? (
          <Button
            type="text"
            htmlType="button"
            onClick={onLoadDiagnostics}
            disabled={diagnosticsLoading}
          >
            {diagnosticsLoading ? "正在加载运行轨迹" : "加载运行轨迹与诊断"}
          </Button>
        ) : (
          <a className="workspace-trace-link" href="#execution-inspector">
            查看执行记录
          </a>
        )}
        <ol className="workspace-circuit" aria-label="审查链路">
          <li data-state={workspace.documents.length > 0 ? "filled" : "open"}>
            <span className="workspace-circuit-mark" aria-hidden="true" />
            材料 <strong>{workspace.documents.length}</strong>
          </li>
          <li
            data-state={
              anchors.length > 0 && locatedAnchors === anchors.length
                ? "filled"
                : "open"
            }
          >
            <span className="workspace-circuit-mark" aria-hidden="true" />
            可在原文中找到的引用{" "}
            <strong>
              {locatedAnchors}/{anchors.length}
            </strong>{" "}
            {anchors.length === 0
              ? "未提供引用"
              : locatedAnchors === 0
                ? "均未定位"
                : locatedAnchors === anchors.length
                  ? "全部定位"
                  : "部分定位"}
          </li>
          <li data-state={workspace.findings.length > 0 ? "filled" : "open"}>
            <span className="workspace-circuit-mark" aria-hidden="true" />
            线索 <strong>{workspace.findings.length}</strong>
          </li>
          <li data-state={decidedFindings > 0 ? "filled" : "open"}>
            <span className="workspace-circuit-mark" aria-hidden="true" />
            审查判断{" "}
            <strong>
              {decidedFindings}/{workspace.findings.length}
            </strong>
          </li>
        </ol>
        {retryableState ? (
          <Button
            aria-label={String(
              retrying ? "正在受理重试" : "重新执行未完成的审查",
            )}
            type="text"
            htmlType="button"
            onClick={onRetry}
            disabled={retrying || !canRetry}
          >
            {retrying ? "正在受理重试" : "重新执行未完成的审查"}
          </Button>
        ) : null}
      </div>
      {run.state === "partial" ? (
        <p className="callout">
          {run.partial_reason ?? "部分结果已保留。"}
          模型步骤未完成。已经保存的审查判断不会因为重试被重复提交。
        </p>
      ) : null}
      {run.state === "failed" ? (
        <p className="callout">
          {run.failure_message ?? "本次审查执行失败，已保留失败前的记录。"}
        </p>
      ) : null}
      {run.state === "cancelled" ? (
        <p className="callout">
          审查已取消。取消前保存的规则发现和实际调用用量仍会保留。
        </p>
      ) : null}
      <div className={`workspace-grid layout-${layout}`}>
        <DocumentStage
          key={`${run.id}:${workspace.review_attempts?.attempt.id ?? "legacy"}`}
          documents={workspace.documents}
          finding={
            workspace.findings.find((item) => item.id === selectedId) ?? null
          }
          findings={workspace.findings}
          selectedFindingId={selectedId}
          onSelectFinding={onSelect}
          layout={layout}
          showDiagnostics={showDiagnostics}
          caseContext={caseContext}
          canEditRelations={canEditRelations}
          relationPending={relationPending}
          relationError={relationError}
          onCreateRelation={onCreateRelation}
          onDeleteRelation={onDeleteRelation}
        />
        <FindingPanel
          findings={workspace.findings}
          documents={workspace.documents}
          selectedId={selectedId}
          onSelect={onSelect}
          showDiagnostics={showDiagnostics}
          pending={pending}
          canDecide={canDecide}
          decisionDisabledReason={decisionDisabledReason}
          conflictMessage={conflictMessage}
          saveError={saveError}
          onDecide={onDecide}
        />
      </div>
      <BusinessAgentReviewView review={workspace.agent_review ?? null} />
      {workspace.execution ? (
        <TrajectoryView execution={workspace.execution} />
      ) : null}
      {workspace.execution ? (
        <ExecutionInspector
          execution={workspace.execution}
          caseId={run.case_id}
          findings={workspace.findings}
          evaluations={workspace.evaluations ?? []}
          selectedFindingId={selectedId}
          onSelectFinding={onSelect}
          onSelectAttempt={onSelectAttempt}
          canCancel={canCancel}
          cancelling={cancelling}
          cancelError={cancelError}
          onCancel={onCancel}
        />
      ) : (
        <section
          id="execution-inspector"
          className="execution-inspector"
          aria-labelledby="execution-inspector-title"
        >
          <header className="execution-inspector-head">
            <div>
              <h2 id="execution-inspector-title">执行检查器</h2>
              <p className="meta">
                {selected
                  ? "没有提供详细执行诊断；以下执行状态来自业务工作区。"
                  : "没有获取到本次审查的执行记录。未记录的阶段、耗时和用量不会显示为已知。"}
              </p>
            </div>
            <StatusText tone={runTone(run.state)}>
              {runLabel(run.state)}
            </StatusText>
          </header>
          {selected ? (
            <>
              <label className="field">
                查看执行记录
                <Select
                  aria-label="查看执行记录"
                  value={selected.id}
                  onChange={onSelectAttempt}
                  options={[...attempts]
                    .sort((left, right) => right.ordinal - left.ordinal)
                    .map((attempt, index) => ({
                      value: attempt.id,
                      label: `第 ${attempt.ordinal} 次，${runLabel(attempt.state)}${index === 0 ? "，最新" : ""}`,
                    }))}
                />
              </label>
              {selected.history_kind === "legacy_unfrozen" ? (
                <p className="callout">较早的审查没有保存当时使用的材料版本记录，无法补回当时的执行过程。</p>
              ) : null}
              {canCancel ? (
                <Button type="text" disabled={cancelling} onClick={onCancel}>
                  {cancelling ? "正在取消" : "取消本次执行"}
                </Button>
              ) : null}
              {cancelError ? <p role="alert">{cancelError}</p> : null}
            </>
          ) : null}
        </section>
      )}
    </div>
  );
}
