import { Button } from "antd";
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
import { StatusText } from "./status-text";

const STREAM_LABEL: Record<StreamStatus, string> = {
  connecting: "正在连接运行状态",
  live: "运行状态已连接",
  reconnecting: "连接中断，正在按游标重连",
  closed: "运行状态流已关闭，不会自动重连；请刷新页面或重新登录后再试。",
  inactive: "当前尝试没有活动事件流",
};

export function ReviewWorkspaceView({
  workspace,
  layout,
  selectedId,
  streamStatus,
  pending,
  retrying,
  canDecide = true,
  decisionDisabledReason = null,
  canRetry = true,
  canCancel = false,
  cancelling = false,
  cancelError = null,
  conflictMessage,
  saveError,
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
  pending: boolean;
  retrying: boolean;
  canDecide?: boolean;
  decisionDisabledReason?: string | null;
  canRetry?: boolean;
  canCancel?: boolean;
  cancelling?: boolean;
  cancelError?: string | null;
  conflictMessage: string | null;
  saveError: string | null;
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
        <a className="workspace-trace-link" href="#execution-inspector">
          查看运行轨迹
        </a>
        <ol className="workspace-circuit" aria-label="审查链路">
          <li data-state={workspace.documents.length > 0 ? "filled" : "open"}>
            <span className="workspace-circuit-mark" aria-hidden="true" />
            材料 <strong>{workspace.documents.length}</strong>
          </li>
          <li data-state={locatedAnchors === anchors.length ? "filled" : "open"}>
            <span className="workspace-circuit-mark" aria-hidden="true" />
            可定位引用{" "}
            <strong>
              {locatedAnchors}/{anchors.length}
            </strong>
          </li>
          <li data-state={workspace.findings.length > 0 ? "filled" : "open"}>
            <span className="workspace-circuit-mark" aria-hidden="true" />
            线索 <strong>{workspace.findings.length}</strong>
          </li>
          <li data-state={decidedFindings > 0 ? "filled" : "open"}>
            <span className="workspace-circuit-mark" aria-hidden="true" />
            人工决定{" "}
            <strong>
              {decidedFindings}/{workspace.findings.length}
            </strong>
          </li>
        </ol>
        {retryableState ? (
          <Button
            aria-label={String(
              retrying ? "正在受理重试" : "重新尝试未完成运行",
            )}
            type="text"
            htmlType="button"
            onClick={onRetry}
            disabled={retrying || !canRetry}
          >
            {retrying ? "正在受理重试" : "重新尝试未完成运行"}
          </Button>
        ) : null}
      </div>
      {run.state === "partial" ? (
        <p className="callout">
          {run.partial_reason ?? "部分结果已保留。"}
          模型步骤未完成。已经保存的人工决定不会因为重试被重复提交。
        </p>
      ) : null}
      {run.state === "failed" ? (
        <p className="callout">
          {run.failure_message ?? "这次运行失败，已保留失败前的记录。"}
        </p>
      ) : null}
      {run.state === "cancelled" ? (
        <p className="callout">
          运行已取消。取消前持久化的规则发现与实际调用用量仍保留。
        </p>
      ) : null}
      <div className={`workspace-grid layout-${layout}`}>
        <DocumentStage
          key={`${run.id}:${workspace.execution?.attempt.id ?? "legacy"}`}
          documents={workspace.documents}
          finding={
            workspace.findings.find((item) => item.id === selectedId) ?? null
          }
          findings={workspace.findings}
          selectedFindingId={selectedId}
          onSelectFinding={onSelect}
          layout={layout}
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
          pending={pending}
          canDecide={canDecide}
          decisionDisabledReason={decisionDisabledReason}
          conflictMessage={conflictMessage}
          saveError={saveError}
          onDecide={onDecide}
        />
      </div>
      <ExecutionInspector
        execution={workspace.execution}
        caseId={run.case_id}
        runState={run.state}
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
    </div>
  );
}
