import { Button, Select } from "antd";
import type {
  CaseRelationInput,
  DecisionAction,
  Finding,
  ExecutionAttempt,
  ReviewDocument,
  ReviewRun,
  ReviewWorkspace,
} from "../api/types";
import type { ReviewLayout } from "../domain/review";
import { runLabel, runTone } from "../domain/review";
import type { StreamStatus } from "../hooks/use-review-events";
import { DocumentStage, type ReviewCaseContext } from "./document-stage";
import { ExecutionInspector } from "./execution-inspector";
import { FindingPanel } from "./finding-panel";
import { BusinessAgentReviewView } from "./business-agent-review";
import { StatusText } from "./status-text";

const STREAM_LABEL: Record<StreamStatus, string> = {
  connecting: "正在连接运行状态",
  live: "运行状态已连接",
  reconnecting: "连接中断，正在按游标重连",
  closed: "运行状态流已关闭，不会自动重连；请刷新页面或重新登录后再试。",
  inactive: "本次执行没有活动的事件流",
};

function isRetryableRun(run: ReviewRun): boolean {
  return (
    run.state === "failed" ||
    run.state === "partial" ||
    run.state === "cancelled" ||
    run.state === "interrupted"
  );
}

function citationMessage(anchors: Finding["evidence"], located: number): string {
  if (anchors.length === 0) return "未提供引用";
  if (located === 0) return "均未定位";
  if (located === anchors.length) return "全部定位";
  return "部分定位";
}

function WorkspaceCircuit({
  documents,
  findings,
}: {
  documents: ReviewDocument[];
  findings: Finding[];
}) {
  const anchors = findings.flatMap((finding) => finding.evidence);
  const locatedAnchors = anchors.filter(
    (anchor) => anchor.status === "located",
  ).length;
  const decidedFindings = findings.filter(
    (finding) => finding.decisions.length > 0,
  ).length;
  const allLocated = anchors.length > 0 && locatedAnchors === anchors.length;
  return (
    <ol className="workspace-circuit" aria-label="审查链路">
      <li data-state={documents.length > 0 ? "filled" : "open"}>
        <span className="workspace-circuit-mark" aria-hidden="true" />
        材料 <strong>{documents.length}</strong>
      </li>
      <li data-state={allLocated ? "filled" : "open"}>
        <span className="workspace-circuit-mark" aria-hidden="true" />
        可在原文中找到的引用{" "}
        <strong>
          {locatedAnchors}/{anchors.length}
        </strong>{" "}
        {citationMessage(anchors, locatedAnchors)}
      </li>
      <li data-state={findings.length > 0 ? "filled" : "open"}>
        <span className="workspace-circuit-mark" aria-hidden="true" />
        线索 <strong>{findings.length}</strong>
      </li>
      <li data-state={decidedFindings > 0 ? "filled" : "open"}>
        <span className="workspace-circuit-mark" aria-hidden="true" />
        审查判断{" "}
        <strong>
          {decidedFindings}/{findings.length}
        </strong>
      </li>
    </ol>
  );
}

function RetryButton({
  retrying,
  canRetry,
  onRetry,
}: {
  retrying: boolean;
  canRetry: boolean;
  onRetry: () => void;
}) {
  return (
    <Button
      aria-label={String(retrying ? "正在受理重试" : "重新执行未完成的审查")}
      type="text"
      htmlType="button"
      onClick={onRetry}
      disabled={retrying || !canRetry}
    >
      {retrying ? "正在受理重试" : "重新执行未完成的审查"}
    </Button>
  );
}

function WorkspaceBanner({
  run,
  documents,
  findings,
  streamStatus,
  retrying,
  canRetry,
  onRetry,
}: {
  run: ReviewRun;
  documents: ReviewDocument[];
  findings: Finding[];
  streamStatus: StreamStatus;
  retrying: boolean;
  canRetry: boolean;
  onRetry: () => void;
}) {
  return (
    <div className="workspace-banner">
      <StatusText tone={runTone(run.state)}>{runLabel(run.state)}</StatusText>
      <span>{STREAM_LABEL[streamStatus]}</span>
      {run.model_ref ? <span>模型 {run.model_ref}</span> : null}
      <a className="workspace-trace-link" href="#execution-inspector">
        查看运行轨迹
      </a>
      <WorkspaceCircuit documents={documents} findings={findings} />
      {isRetryableRun(run) ? (
        <RetryButton
          retrying={retrying}
          canRetry={canRetry}
          onRetry={onRetry}
        />
      ) : null}
    </div>
  );
}

function RunCallouts({ run }: { run: ReviewRun }) {
  return (
    <>
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
    </>
  );
}

function attemptOptions(attempts: ExecutionAttempt[]) {
  return [...attempts]
    .sort((left, right) => right.ordinal - left.ordinal)
    .map((item, index) => ({
      value: item.id,
      label: `第 ${item.ordinal} 次，${runLabel(item.state)}${index === 0 ? "，最新" : ""}`,
    }));
}

function MissingExecution({
  run,
  attempts,
  selected,
  onSelectAttempt,
  canCancel,
  cancelling,
  cancelError,
  onCancel,
}: {
  run: ReviewRun;
  attempts: ExecutionAttempt[];
  selected: ExecutionAttempt | null;
  onSelectAttempt: (attemptId: string) => void;
  canCancel: boolean;
  cancelling: boolean;
  cancelError: string | null;
  onCancel: () => void;
}) {
  return (
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
        <StatusText tone={runTone(run.state)}>{runLabel(run.state)}</StatusText>
      </header>
      {selected ? (
        <>
          <label className="field">
            查看执行记录
            <Select
              aria-label="查看执行记录"
              value={selected.id}
              onChange={onSelectAttempt}
              options={attemptOptions(attempts)}
            />
          </label>
          {selected.history_kind === "legacy_unfrozen" ? (
            <p className="callout">
              较早的审查没有保存当时使用的材料版本记录，无法补回当时的执行过程。
            </p>
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
  );
}

function attemptsOf(workspace: ReviewWorkspace): {
  selected: ExecutionAttempt | null;
  attempts: ExecutionAttempt[];
} {
  const selected = workspace.review_attempts?.attempt ?? null;
  if (!workspace.review_attempts) return { selected, attempts: [] };
  const saved = workspace.review_attempts.attempts;
  return {
    selected,
    attempts: selected && saved.some((item) => item.id === selected.id)
      ? saved
      : selected
        ? [selected, ...saved]
        : saved,
  };
}

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
  showDiagnostics?: boolean;
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
  const { selected, attempts } = attemptsOf(workspace);
  return (
    <div className="workspace">
      <WorkspaceBanner
        run={run}
        documents={workspace.documents}
        findings={workspace.findings}
        streamStatus={streamStatus}
        retrying={retrying}
        canRetry={canRetry}
        onRetry={onRetry}
      />
      <RunCallouts run={run} />
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
        <MissingExecution
          run={run}
          attempts={attempts}
          selected={selected}
          onSelectAttempt={onSelectAttempt}
          canCancel={canCancel}
          cancelling={cancelling}
          cancelError={cancelError}
          onCancel={onCancel}
        />
      )}
    </div>
  );
}
