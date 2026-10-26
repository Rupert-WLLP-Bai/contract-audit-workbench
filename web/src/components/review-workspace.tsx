import {
  BugOutlined,
  ReloadOutlined,
  StopOutlined,
} from "@ant-design/icons";
import { Alert, Button, Drawer, Segmented, Splitter, Tag } from "antd";
import { useState, type ReactNode } from "react";
import type {
  CaseRelationInput,
  DecisionAction,
  ReviewWorkspace,
} from "../api/types";
import type { ReviewLayout } from "../domain/review";
import { runLabel } from "../domain/review";
import { reviewStage } from "../domain/review-presentation";
import type { StreamStatus } from "../hooks/use-review-events";
import { DocumentStage, type ReviewCaseContext } from "./document-stage";
import { ExecutionInspector } from "./execution-inspector";
import { FindingPanel } from "./finding-panel";
import "./review-workspace.css";

const STREAM_LABEL: Record<StreamStatus, string> = {
  connecting: "正在连接运行状态",
  live: "运行状态已连接",
  reconnecting: "连接中断，正在按游标重连",
  closed: "运行状态流已关闭，不会自动重连；请刷新页面或重新登录后再试。",
  inactive: "当前尝试没有活动事件流",
};

function RunNotice({
  tone,
  title,
  children,
}: {
  tone: "warning" | "error" | "info";
  title: string;
  children: ReactNode;
}) {
  return (
    <div
      className={`review-run-notice review-run-notice--${tone}`}
      role="status"
    >
      <strong>{title}</strong>
      <p>{children}</p>
    </div>
  );
}

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
  const { run, execution } = workspace;
  const [pane, setPane] = useState<"doc" | "analysis">("doc");
  const [diagnosticsOpen, setDiagnosticsOpen] = useState(false);
  const retryableState =
    run.state === "failed" ||
    run.state === "partial" ||
    run.state === "cancelled" ||
    run.state === "interrupted";
  const attempt = execution?.attempt ?? null;
  const attemptEvents =
    execution?.events.filter((event) => event.attempt_id === attempt?.id) ?? [];
  const stage = reviewStage(run.state, attemptEvents, run);
  const showStageDetail =
    run.state !== "partial" &&
    run.state !== "failed" &&
    run.state !== "cancelled";
  const attemptLabel = attempt ? `第 ${attempt.ordinal} 次尝试` : "历史记录";

  const documentPane = (
    <DocumentStage
      key={`${run.id}:${attempt?.id ?? "legacy"}`}
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
  );

  const analysisPane = (
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
  );

  return (
    <div className="workspace review-workspace">
      <section className="review-runbar" aria-label="审查运行状态">
        <div className="review-runbar__main">
          <span
            className={`review-stage-dot review-stage-dot--${stage.tone}`}
            aria-hidden="true"
          />
          <div className="review-runbar__text">
            <div className="review-runbar__title">
              <strong>{stage.label}</strong>
              <Tag>{attemptLabel}</Tag>
              {run.model_ref ? (
                <span className="review-runbar__model" title="模型标识">
                  {run.model_ref}
                </span>
              ) : null}
            </div>
            {showStageDetail ? (
              <p className="review-runbar__detail">{stage.detail}</p>
            ) : null}
          </div>
        </div>
        <div className="review-runbar__actions">
          <span
            className={`review-stream-status review-stream-status--${streamStatus}`}
            title={STREAM_LABEL[streamStatus]}
          >
            {STREAM_LABEL[streamStatus]}
          </span>
          <Button
            icon={<BugOutlined />}
            htmlType="button"
            aria-label="执行诊断"
            onClick={() => setDiagnosticsOpen(true)}
          >
            执行诊断
          </Button>
          {retryableState ? (
            <Button
              icon={<ReloadOutlined />}
              aria-label={String(
                retrying ? "正在受理重试" : "重新尝试未完成运行",
              )}
              htmlType="button"
              onClick={onRetry}
              disabled={retrying || !canRetry}
            >
              {retrying ? "正在受理重试" : "重新尝试未完成运行"}
            </Button>
          ) : null}
          {canCancel ? (
            <Button
              danger
              icon={<StopOutlined />}
              aria-label={String(cancelling ? "正在取消" : "取消当前尝试")}
              htmlType="button"
              disabled={cancelling}
              onClick={onCancel}
            >
              {cancelling ? "正在取消" : "取消当前尝试"}
            </Button>
          ) : null}
        </div>
      </section>

      {cancelError ? (
        <Alert
          type="error"
          showIcon
          title="取消失败"
          description={cancelError}
        />
      ) : null}

      {run.state === "partial" ? (
        <RunNotice tone="warning" title="部分完成，结果待人工复核">
          {run.partial_reason ?? "部分结果已保留，未报告具体原因。"}
          已保存的发现和人工决定保持有效；重新尝试会追加新尝试，不会覆盖本次记录。
        </RunNotice>
      ) : null}
      {run.state === "failed" ? (
        <RunNotice tone="error" title="审查失败">
          {run.failure_message ?? "这次运行失败，已保留失败前的记录。"}
        </RunNotice>
      ) : null}
      {run.state === "cancelled" ? (
        <RunNotice tone="info" title="审查已取消">
          取消只终止本次尝试。取消前持久化的规则发现、人工决定与调用用量仍然保留。
        </RunNotice>
      ) : null}
      {execution === null ? (
        <RunNotice tone="warning" title="本次没有持久执行快照">
          历史记录未冻结（legacy_unfrozen），无法重放当时解析输入或补造执行阶段、耗时和用量。
        </RunNotice>
      ) : null}
      {attempt?.history_kind === "legacy_unfrozen" ? (
        <RunNotice tone="warning" title="选中的是历史尝试">
          旧运行未冻结原始输入，不能补造历史阶段。
        </RunNotice>
      ) : null}

      {layout === "split" ? (
        <Splitter
          className="review-split"
          classNames={{ panel: "review-split__panel" }}
        >
          <Splitter.Panel defaultSize="55%" min="38%" max="68%">
            {documentPane}
          </Splitter.Panel>
          <Splitter.Panel>{analysisPane}</Splitter.Panel>
        </Splitter>
      ) : (
        <>
          <div className="review-pane-switch">
            <Segmented
              block
              aria-label="切换原文或风险分析"
              value={pane}
              onChange={(value) => setPane(value as "doc" | "analysis")}
              options={[
                { label: "合同原文", value: "doc" },
                { label: "风险分析", value: "analysis" },
              ]}
            />
          </div>
          <div className={`review-pane-stack review-pane-stack--${pane}`}>
            <div className="review-pane-stack__doc">{documentPane}</div>
            <div className="review-pane-stack__analysis">{analysisPane}</div>
          </div>
        </>
      )}

      <Drawer
        title={
          <div className="review-diagnostics-title">
            <span>执行诊断</span>
            <small>{runLabel(run.state)}</small>
          </div>
        }
        size={960}
        open={diagnosticsOpen}
        onClose={() => setDiagnosticsOpen(false)}
        className="review-diagnostics-drawer"
      >
        {diagnosticsOpen ? (
          <ExecutionInspector
            execution={execution}
            caseId={run.case_id}
            runState={run.state}
            findings={workspace.findings}
            evaluations={workspace.evaluations ?? []}
            selectedFindingId={selectedId}
            onSelectFinding={onSelect}
            onSelectAttempt={onSelectAttempt}
            canCancel={false}
            cancelling={false}
            cancelError={null}
            onCancel={() => undefined}
          />
        ) : null}
      </Drawer>
    </div>
  );
}
