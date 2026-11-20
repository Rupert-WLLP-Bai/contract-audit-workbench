// File metadata (manifest mirror):
// Purpose: Presents the authorized approval inbox, relevant record detail and server-backed processing or retries across the three approval views.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-20
// Revision: r6
// Copyright: See LICENSE
// End file metadata.

import { Alert, Button, Drawer, Input, Table, Tabs, type TableColumnsType } from "antd";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useSearchParams } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import {
  useApprovalInbox,
  useApprovalTaskDetail,
  useContractApprovalRound,
  useContractApprovalRounds,
  useDecideApprovalTask,
} from "../api/approval-queries";
import type {
  ApprovalDecisionAction,
  ApprovalInboxItem,
  ApprovalInboxView,
  ApprovalRoundReadDetail,
  ApprovalRoundReadSummary,
} from "../api/approvals";
import { errorMessage, isConflict } from "../api/http";
import { useSession } from "../api/queries";
import { DirtyLeaveGuard } from "../components/dirty-leave-guard";
import { actorReadContextKey } from "../domain/actor-read-context";
import { compactTime } from "../domain/time";
import { PageStatus } from "../components/page-status";
import { ListPage } from "../components/page-patterns";
import { StatusText } from "../components/status-text";
import { FrozenPlaybookPanel } from "../components/frozen-playbook-panel";
import {
  approvalActionLabel,
  approvalFieldValue,
  approvalNodeLabel,
  approvalProgressLabel,
  approvalRoundStateLabel,
  approvalRoundTone,
  approvalTaskStateLabel,
  approvalTaskTone,
} from "../domain/approval";
import { formatDateTime } from "../domain/review";
import "../components/workbench-side-peek.css";
import "./approval-tasks-page.css";

type Selection =
  { kind: "task"; taskId: string } | { kind: "round"; contractId: string; roundId: string } | null;

const VIEW_LABELS: Record<ApprovalInboxView, string> = {
  todo: "待我审批",
  done: "我已审批",
  initiated: "我发起的",
};

function parseView(value: string | null): ApprovalInboxView {
  return value === "done" || value === "initiated" ? value : "todo";
}

function selectionKey(selection: Selection): string {
  if (selection === null) return "none";
  return selection.kind === "task"
    ? `task:${selection.taskId}`
    : `round:${selection.contractId}:${selection.roundId}`;
}

function itemSelection(item: ApprovalInboxItem): Selection {
  if (item.view === "initiated") {
    return {
      kind: "round",
      contractId: item.contract_id,
      roundId: item.approval_round_id,
    };
  }
  return item.task_id === null
    ? {
        kind: "round",
        contractId: item.contract_id,
        roundId: item.approval_round_id,
      }
    : { kind: "task", taskId: item.task_id };
}

function itemStatus(item: ApprovalInboxItem) {
  if (item.view === "done" && item.decision_action !== null) {
    return (
      <StatusText tone={item.decision_action === "approve" ? "good" : "attention"}>
        {approvalActionLabel(item.decision_action)}
      </StatusText>
    );
  }
  if (item.view === "initiated") {
    return (
      <StatusText tone={approvalRoundTone(item.round_state)}>
        {approvalRoundStateLabel(item.round_state)}
      </StatusText>
    );
  }
  return item.task_state === null ? (
    <StatusText tone="neutral">状态未返回</StatusText>
  ) : (
    <StatusText tone={approvalTaskTone(item.task_state)}>
      {approvalTaskStateLabel(item.task_state)}
    </StatusText>
  );
}

const EMPTY_TEXT: Record<ApprovalInboxView, string> = {
  todo: "当前没有待你审批的合同",
  done: "暂无已审批记录",
  initiated: "暂无你发起的会签",
};

function itemTime(item: ApprovalInboxItem) {
  if (item.view === "todo") return { value: item.task_activated_at, label: "" };
  if (item.view === "done") return { value: item.decision_decided_at, label: "审批" };
  return { value: item.round_started_at, label: "发起" };
}

function ApprovalInboxList({
  view,
  items,
  loading,
  failed,
  hasNext,
  selected,
  loadingMore,
  onLoadMore,
  onOpen,
}: {
  view: ApprovalInboxView;
  items: ApprovalInboxItem[];
  loading: boolean;
  failed: boolean;
  hasNext: boolean;
  selected: Selection;
  loadingMore: boolean;
  onLoadMore: () => void;
  onOpen: (selection: Selection, trigger: HTMLElement | null) => void;
}) {
  const columns: TableColumnsType<ApprovalInboxItem> = [
    {
      title: "合同",
      key: "contract",
      width: "42%",
      render: (_: unknown, item) => (
        <div className="approval-contract-cell">
          <strong>{item.contract_name}</strong>
          {item.contract_number ? (
            <span className="approval-muted">{item.contract_number}</span>
          ) : null}
        </div>
      ),
    },
    {
      title: "当前节点",
      key: "round",
      render: (_: unknown, item) => (
        <span>
          第 {item.round_number} 轮
          {item.node_code === null ? "" : ` · ${approvalNodeLabel(item.node_code)}`}
        </span>
      ),
    },
    {
      title: "状态",
      key: "state",
      render: (_: unknown, item) => itemStatus(item),
    },
    {
      title: "送达时间",
      key: "time",
      render: (_: unknown, item) => {
        const { value, label } = itemTime(item);
        return value ? (
          <time dateTime={value}>
            {label ? `${label} · ` : ""}
            {compactTime(value)}
          </time>
        ) : (
          "—"
        );
      },
    },
    {
      title: "操作",
      key: "action",
      align: "right",
      render: (_: unknown, item) => {
        const label = item.view === "todo" && item.allowed_actions.length ? "审批" : "查看";
        return (
          <Button
            type="link"
            data-approval-row-action
            aria-haspopup="dialog"
            onClick={(event) => {
              event.stopPropagation();
              onOpen(itemSelection(item), event.currentTarget);
            }}
            aria-label={`${label} ${item.contract_name} 第 ${item.round_number} 轮`}
          >
            {label}
          </Button>
        );
      },
    },
  ];

  return (
    <>
      <Table<ApprovalInboxItem>
        rowKey={(item) => `${item.view}:${item.task_id ?? item.approval_round_id}`}
        columns={columns}
        dataSource={items}
        pagination={false}
        size="middle"
        loading={loading}
        locale={{
          emptyText:
            failed || loading ? null : <span className="approval-empty">{EMPTY_TEXT[view]}</span>,
        }}
        tableLayout="fixed"
        rowClassName={(item) =>
          selectionKey(selected) === selectionKey(itemSelection(item))
            ? "approval-row-selected"
            : ""
        }
        onRow={(item) => ({
          onClick: (event: React.MouseEvent<HTMLTableRowElement>) =>
            onOpen(
              itemSelection(item),
              event.currentTarget.querySelector<HTMLElement>("[data-approval-row-action]"),
            ),
        })}
      />
      {hasNext ? (
        <div className="approval-list-more">
          <Button onClick={onLoadMore} loading={loadingMore}>
            加载更多
          </Button>
        </div>
      ) : null}
    </>
  );
}

function RoundHistory({
  actorScope,
  contractId,
  currentRoundId,
  onSelect,
}: {
  actorScope: string;
  contractId: string;
  currentRoundId: string;
  onSelect: (contractId: string, round: ApprovalRoundReadSummary) => void;
}) {
  const history = useContractApprovalRounds(contractId, true, actorScope);
  const items = history.isError ? [] : (history.data?.pages.flatMap((page) => page.items) ?? []);
  return (
    <section className="approval-detail-section" aria-label="轮次历史">
      <h3>轮次历史</h3>
      {history.error ? (
        <Alert
          type="error"
          showIcon
          message="轮次历史没有加载"
          description={errorMessage(history.error)}
        />
      ) : null}
      {history.isLoading ? <p className="approval-muted">正在加载轮次历史…</p> : null}
      {!history.isError && !history.isLoading && items.length === 0 ? (
        <p className="approval-muted">暂无历史轮次。</p>
      ) : null}
      <ul className="approval-round-history">
        {items.map((item) => (
          <li key={item.round.approval_round_id}>
            <button
              type="button"
              className={item.round.approval_round_id === currentRoundId ? "is-current" : ""}
              onClick={() => onSelect(contractId, item.round)}
            >
              <span>
                第 {item.round.round_number} 轮 · {approvalRoundStateLabel(item.round.state)}
              </span>
              <span className="approval-muted">
                {formatDateTime(item.round.started_at)} · {item.decision_count}/{item.task_count}{" "}
                个节点已决定
              </span>
              {item.active_task ? (
                <span className="approval-muted">
                  当前节点：{approvalNodeLabel(item.active_task.node_code)} ·{" "}
                  {item.active_task.assignee_display}
                </span>
              ) : null}
            </button>
          </li>
        ))}
      </ul>
      {history.hasNextPage ? (
        <Button
          type="link"
          loading={history.isFetchingNextPage}
          onClick={() => void history.fetchNextPage()}
        >
          加载更早轮次
        </Button>
      ) : null}
    </section>
  );
}

function DecisionForm({
  detail,
  activeTask,
  opinion,
  onChange,
  onSuccess,
  footer,
  refreshing,
}: {
  detail: ApprovalRoundReadDetail;
  activeTask: ApprovalRoundReadDetail["tasks"][number];
  opinion: string;
  onChange: (opinion: string) => void;
  onSuccess: (notice: string) => void;
  footer: HTMLElement | null;
  refreshing: boolean;
}) {
  const queryClient = useQueryClient();
  const mutation = useDecideApprovalTask();
  const [notice, setNotice] = useState<string | null>(null);

  function submit(action: ApprovalDecisionAction) {
    if (refreshing || mutation.isPending || !detail.allowed_actions.includes(action)) return;
    const trimmed = opinion.trim();
    if (trimmed === "") {
      setNotice("请填写审批意见或退回理由。");
      return;
    }
    setNotice(null);
    mutation.mutate(
      {
        task_id: activeTask.task_id,
        expected_round_row_version: detail.round.row_version,
        decision: action,
        comment: trimmed,
      },
      {
        onSuccess: (result) => {
          onSuccess(
            `已保存审批决定：第 ${result.round.round_number} 轮 ${approvalRoundStateLabel(result.round.state)}，` +
              `当前节点 ${result.task.node_order} 为${approvalTaskStateLabel(result.task.state)}。`,
          );
          onChange("");
        },
        onError: (error) => {
          setNotice(
            isConflict(error)
              ? "当前任务或轮次已经变化，没有提交新决定。请核对最新状态后重试。"
              : `决定没有提交成功：${errorMessage(error)}`,
          );
          void queryClient.invalidateQueries({ queryKey: ["approvals"] });
        },
      },
    );
  }

  return (
    <section className="approval-decision" aria-label="提交审批决定">
      <label className="field">
        审批意见或退回理由
        <Input.TextArea
          aria-label="审批意见或退回理由"
          value={opinion}
          maxLength={2000}
          showCount
          autoSize={{ minRows: 3, maxRows: 8 }}
          onChange={(event) => onChange(event.target.value)}
        />
      </label>
      {notice ? <Alert type={mutation.isError ? "error" : "info"} title={notice} /> : null}
      {footer
        ? createPortal(
            <div className="approval-decision-actions">
              {detail.allowed_actions.includes("return") ? (
                <Button
                  danger
                  disabled={refreshing}
                  loading={mutation.isPending}
                  onClick={() => submit("return")}
                >
                  退回修改
                </Button>
              ) : null}
              {detail.allowed_actions.includes("approve") ? (
                <Button
                  type="primary"
                  disabled={refreshing}
                  loading={mutation.isPending}
                  onClick={() => submit("approve")}
                >
                  同意通过
                </Button>
              ) : null}
            </div>,
            footer,
          )
        : null}
    </section>
  );
}

function RevisionDiff({ detail }: { detail: ApprovalRoundReadDetail }) {
  if (detail.previous_round === null) {
    return (
      <section className="approval-detail-section" aria-label="轮次差异">
        <h3>轮次差异</h3>
        <p className="approval-muted">首轮会签，没有上一轮可比较。</p>
      </section>
    );
  }
  if (!detail.revision_changed || detail.revision_diff === null) {
    return (
      <section className="approval-detail-section" aria-label="轮次差异">
        <h3>轮次差异</h3>
        <p className="approval-muted">
          本轮与第 {detail.previous_round.round_number} 轮使用同一留档版本，正文没有变化。
        </p>
      </section>
    );
  }
  const diff = detail.revision_diff;
  return (
    <section className="approval-detail-section" aria-label="轮次差异">
      <h3>轮次差异</h3>
      <p className="approval-muted">
        第 {diff.from_revision_number ?? "—"} 版 → 第 {diff.to_revision_number} 版；
        {diff.changed ? "存在字段变化。" : "未返回字段变化。"}
        {diff.amount_terms.length > 0 ? ` 金额或标的相关变化 ${diff.amount_terms.length} 项。` : ""}
      </p>
      {diff.fields.length === 0 ? (
        <p className="approval-muted">没有返回普通字段差异。</p>
      ) : (
        <Table
          rowKey={(field) => field.path}
          size="small"
          pagination={false}
          dataSource={diff.fields}
          columns={[
            { title: "字段路径", dataIndex: "path", key: "path" },
            { title: "变化", dataIndex: "change", key: "change" },
            {
              title: "变化前",
              key: "before",
              render: (_: unknown, field) => approvalFieldValue(field.before),
            },
            {
              title: "变化后",
              key: "after",
              render: (_: unknown, field) => approvalFieldValue(field.after),
            },
          ]}
        />
      )}
      {diff.restricted_fields.length > 0 ? (
        <div className="approval-restricted">
          <h4>受限字段</h4>
          <ul>
            {diff.restricted_fields.map((field) => (
              <li key={field.path}>
                <span className="approval-mono">{field.path}</span>：{" "}
                {field.changed === null
                  ? `无法证明${field.reason ? `（${field.reason}）` : ""}`
                  : field.changed
                    ? "已变化（不返回明文）"
                    : "未变化"}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </section>
  );
}

function RoundTimeline({
  detail,
  selectedTaskId,
}: {
  detail: ApprovalRoundReadDetail;
  selectedTaskId: string | null;
}) {
  return (
    <section className="approval-detail-section" aria-label="节点与意见">
      <h3>节点与意见 · {approvalProgressLabel(detail.tasks.map((task) => task.state))}</h3>
      <ol className="approval-timeline">
        {detail.tasks.map((task) => {
          const decision = detail.decisions.find((item) => item.task_id === task.task_id);
          const current = task.task_id === detail.active_task_id;
          return (
            <li
              key={task.task_id}
              className={[
                current ? "is-current" : "",
                task.task_id === selectedTaskId ? "is-selected" : "",
              ]
                .filter(Boolean)
                .join(" ")}
            >
              <div className="approval-timeline-head">
                <strong>
                  {task.node_order}. {approvalNodeLabel(task.node_code)}
                </strong>
                <StatusText tone={approvalTaskTone(task.state)}>
                  {approvalTaskStateLabel(task.state)}
                </StatusText>
              </div>
              <div className="approval-muted">
                受理人：{task.assignee_display}
                {task.activated_at ? ` · 激活 ${formatDateTime(task.activated_at)}` : ""}
                {task.decided_at ? ` · 决定 ${formatDateTime(task.decided_at)}` : ""}
              </div>
              {decision ? (
                <div className="approval-opinion">
                  <span>
                    {decision.actor_display} · {approvalActionLabel(decision.action)}
                  </span>
                  <p>{decision.opinion}</p>
                </div>
              ) : null}
            </li>
          );
        })}
      </ol>
    </section>
  );
}

function ApprovalDetailBody({
  actorScope,
  detail,
  children,
  selectedTaskId,
  onSelectRound,
}: {
  actorScope: string;
  detail: ApprovalRoundReadDetail;
  children?: ReactNode;
  selectedTaskId: string | null;
  onSelectRound: (contractId: string, round: ApprovalRoundReadSummary) => void;
}) {
  return (
    <>
      <header className="approval-detail-head">
        <div>
          <h2>{detail.contract_name}</h2>
          <p className="approval-muted">
            {detail.contract_number ?? detail.contract_id} · 第 {detail.round.round_number} 轮 ·{" "}
            {approvalRoundStateLabel(detail.round.state)}
          </p>
        </div>
        <StatusText tone={approvalRoundTone(detail.round.state)}>
          {approvalRoundStateLabel(detail.round.state)}
        </StatusText>
      </header>
      <dl className="approval-facts">
        <div>
          <dt>合同状态</dt>
          <dd>{detail.contract_state}</dd>
        </div>
        <div>
          <dt>发起人</dt>
          <dd>{detail.round.initiated_by_display}</dd>
        </div>
        <div>
          <dt>开始时间</dt>
          <dd>{formatDateTime(detail.round.started_at)}</dd>
        </div>
        <div>
          <dt>结束时间</dt>
          <dd>{detail.round.ended_at ? formatDateTime(detail.round.ended_at) : "尚未结束"}</dd>
        </div>
        <div>
          <dt>流程模板</dt>
          <dd>
            {detail.round.workflow_template_id} / {detail.round.workflow_template_version}
          </dd>
        </div>
        <div>
          <dt>风险处置</dt>
          <dd>
            {detail.risk_disposition_state === "unknown"
              ? "未配置，不能视为已接受风险"
              : detail.risk_disposition_state}
            {detail.risk_disposition_reason ? `（${detail.risk_disposition_reason}）` : ""}
          </dd>
        </div>
        <div>
          <dt>留档版本</dt>
          <dd className="approval-mono">{detail.round.contract_revision_id}</dd>
        </div>
      </dl>
      {detail.adopted_playbook ? (
        <FrozenPlaybookPanel adoption={detail.adopted_playbook} heading="本轮采用的审查手册" />
      ) : null}
      <RoundTimeline detail={detail} selectedTaskId={selectedTaskId} />
      {children}
      <RevisionDiff detail={detail} />
      <RoundHistory
        actorScope={actorScope}
        contractId={detail.contract_id}
        currentRoundId={detail.round.approval_round_id}
        onSelect={onSelectRound}
      />
    </>
  );
}

function ApprovalDetailPanel({
  actorScope,
  view,
  selection,
  ready,
  onSelectRound,
  onClose,
}: {
  actorScope: string;
  view: ApprovalInboxView;
  selection: NonNullable<Selection>;
  ready: boolean;
  onSelectRound: (contractId: string, round: ApprovalRoundReadSummary) => void;
  onClose: () => void;
}) {
  const [opinion, setOpinion] = useState("");
  const [notice, setNotice] = useState<string | null>(null);
  const [footer, setFooter] = useState<HTMLDivElement | null>(null);
  const taskId = ready && selection.kind === "task" ? selection.taskId : null;
  const contractId = ready && selection.kind === "round" ? selection.contractId : null;
  const roundId = ready && selection.kind === "round" ? selection.roundId : null;
  const task = useApprovalTaskDetail(taskId, actorScope);
  const round = useContractApprovalRound(contractId, roundId, actorScope);
  const active = selection.kind === "task" ? task : round;
  const detail =
    selection.kind === "task"
      ? task.data?.selected_task_id === taskId
        ? task.data?.round_detail
        : undefined
      : round.data?.contract_id === contractId && round.data?.round.approval_round_id === roundId
        ? round.data
        : undefined;
  const activeTask =
    view === "todo" &&
    selection.kind === "task" &&
    detail?.round.state === "in_review" &&
    detail.active_task_id === taskId
      ? detail.tasks.find((entry) => entry.task_id === taskId && entry.state === "active")
      : undefined;
  const canAct = ready && !active.isError && Boolean(detail?.allowed_actions.length) && activeTask;
  return (
    <>
      <DirtyLeaveGuard
        dirty={opinion.trim() !== ""}
        blockSearchChange
        onDiscard={() => setOpinion("")}
        description="这条审批意见尚未提交。离开会丢失所填内容。"
      />
      <Drawer
        title="会签详情"
        size="min(520px, 100vw)"
        open={ready}
        onClose={onClose}
        focusable={{ focusTriggerAfterClose: false }}
        rootClassName="workbench-side-peek"
        classNames={{
          mask: "workbench-side-peek-mask",
          wrapper: "workbench-side-peek-wrapper",
          body: "approval-drawer-body",
        }}
        footer={canAct ? <div ref={setFooter} /> : undefined}
      >
        {active.isLoading ? <PageStatus kind="loading" title="正在读取会签详情" /> : null}
        {active.isError ? (
          <Alert
            type="error"
            showIcon
            title="会签详情没有加载"
            description={errorMessage(active.error)}
            action={<Button onClick={() => void active.refetch()}>重试</Button>}
          />
        ) : ready && detail ? (
          <>
            <ApprovalDetailBody
              actorScope={actorScope}
              detail={detail}
              selectedTaskId={taskId}
              onSelectRound={onSelectRound}
            >
              {notice ? <Alert type="info" title={notice} /> : null}
              {canAct ? (
                <DecisionForm
                  detail={detail}
                  activeTask={canAct}
                  opinion={opinion}
                  onChange={setOpinion}
                  onSuccess={setNotice}
                  footer={footer}
                  refreshing={active.isFetching}
                />
              ) : null}
            </ApprovalDetailBody>
          </>
        ) : ready && !active.isLoading ? (
          <Alert type="warning" title="无法确认这条会签记录" />
        ) : null}
      </Drawer>
    </>
  );
}

function readSelection(params: URLSearchParams): Selection {
  const taskId = params.get("task")?.trim();
  const contractId = params.get("contract")?.trim();
  const roundId = params.get("round")?.trim();
  if (taskId && !contractId && !roundId) return { kind: "task", taskId };
  if (!taskId && contractId && roundId) return { kind: "round", contractId, roundId };
  return null;
}

function clearSelection(params: URLSearchParams) {
  params.delete("task");
  params.delete("contract");
  params.delete("round");
  return params;
}

function ApprovalWorkspace({ scope, ready }: { scope: string; ready: boolean }) {
  const [params, setParams] = useSearchParams();
  const view = parseView(params.get("view"));
  const inbox = useApprovalInbox(view, scope, ready);
  const items =
    ready && !inbox.isError ? (inbox.data?.pages.flatMap((page) => page.items) ?? []) : [];
  const selection = readSelection(params);
  const opener = useRef<HTMLElement | null>(null);
  const list = useRef<HTMLDivElement>(null);
  const selected = useRef(false);
  useEffect(() => {
    if (selection) {
      selected.current = true;
      return;
    }
    if (!selected.current) return;
    selected.current = false;
    const element = opener.current?.isConnected ? opener.current : list.current;
    element?.focus({ preventScroll: true });
    opener.current = null;
  }, [selection]);

  function openSelection(next: Selection, trigger: HTMLElement | null = null) {
    const search = clearSelection(new URLSearchParams(params));
    if (next?.kind === "task") search.set("task", next.taskId);
    if (next?.kind === "round") {
      search.set("contract", next.contractId);
      search.set("round", next.roundId);
    }
    if (trigger) opener.current = trigger;
    setParams(search, { replace: next === null });
  }

  function changeView(next: string) {
    const search = clearSelection(new URLSearchParams(params));
    search.set("view", next);
    setParams(search, { replace: true });
  }

  return (
    <ListPage
      className="approval-page"
      standaloneSurface={false}
      title="会签"
      filters={
        <Tabs
          classNames={{
            header: "approval-view-header",
            item: "approval-view-tab",
            indicator: "approval-view-indicator",
          }}
          activeKey={view}
          onChange={changeView}
          items={(["todo", "done", "initiated"] as const).map((key) => ({
            key,
            label: VIEW_LABELS[key],
          }))}
        />
      }
    >
      {inbox.isError ? (
        <Alert
          type="error"
          showIcon
          title="会签列表没有加载"
          description={errorMessage(inbox.error)}
          action={<Button onClick={() => void inbox.refetch()}>重试</Button>}
        />
      ) : null}
      <section className="approval-inbox" aria-label={VIEW_LABELS[view]}>
        <div tabIndex={-1} ref={list} aria-label={VIEW_LABELS[view] + "列表"}>
          <ApprovalInboxList
            view={view}
            items={items}
            loading={!ready || inbox.isLoading}
            failed={inbox.isError}
            hasNext={ready && !inbox.isError && inbox.hasNextPage}
            loadingMore={inbox.isFetchingNextPage}
            onLoadMore={() => void inbox.fetchNextPage()}
            onOpen={openSelection}
            selected={selection}
          />
        </div>
      </section>
      {selection ? (
        <ApprovalDetailPanel
          key={selectionKey(selection)}
          actorScope={scope}
          view={view}
          selection={selection}
          ready={ready}
          onSelectRound={(contractId, round) =>
            openSelection({ kind: "round", contractId, roundId: round.approval_round_id })
          }
          onClose={() => openSelection(null)}
        />
      ) : null}
    </ListPage>
  );
}

export function ApprovalTasksPage() {
  const [, setParams] = useSearchParams();
  const session = useSession();
  const scope = actorReadContextKey(session.data ?? null);
  const lastScope = useRef("");
  const ready = session.isSuccess && !session.isFetching && !!scope;
  useEffect(() => {
    if (!ready) return;
    const previous = lastScope.current;
    lastScope.current = scope;
    if (previous && previous !== scope)
      setParams((params) => clearSelection(new URLSearchParams(params)), { replace: true });
  }, [ready, scope, setParams]);
  if (session.isError)
    return (
      <PageStatus
        kind="error"
        title="当前身份没有加载"
        detail={errorMessage(session.error)}
        action={<Button onClick={() => void session.refetch()}>重试</Button>}
      />
    );
  return <ApprovalWorkspace key={scope} scope={scope} ready={ready} />;
}
