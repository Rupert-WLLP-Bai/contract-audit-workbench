import {
  Alert,
  Button,
  Empty,
  Input,
  Table,
  Tabs,
  type TableColumnsType,
} from "antd";
import { useMemo, useState } from "react";
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
import type { Actor } from "../api/types";
import { PageStatus } from "../components/page-status";
import { StatusText } from "../components/status-text";
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
import "./approval-tasks-page.css";

type Selection =
  | { kind: "task"; taskId: string }
  | { kind: "round"; contractId: string; roundId: string }
  | null;

const VIEW_LABELS: Record<ApprovalInboxView, string> = {
  todo: "我的待办",
  done: "我已审批",
  initiated: "我发起的",
};

function parseView(value: string | null): ApprovalInboxView {
  return value === "done" || value === "initiated" ? value : "todo";
}

function actorScope(actor: Actor | null): string {
  return actor === null
    ? ""
    : [actor.audience, actor.tenant_id, actor.org_id, actor.id].join("\u0000");
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
      <StatusText
        tone={item.decision_action === "approve" ? "good" : "attention"}
      >
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

function itemTime(item: ApprovalInboxItem): string {
  const value =
    item.view === "done"
      ? item.decision_decided_at
      : item.view === "initiated"
        ? item.round_started_at
        : item.task_activated_at;
  return value === null ? "时间未记录" : formatDateTime(value);
}

function ApprovalInboxList({
  view,
  items,
  loading,
  hasNext,
  loadingMore,
  onLoadMore,
  onOpen,
}: {
  view: ApprovalInboxView;
  items: ApprovalInboxItem[];
  loading: boolean;
  hasNext: boolean;
  loadingMore: boolean;
  onLoadMore: () => void;
  onOpen: (selection: Selection) => void;
}) {
  const columns: TableColumnsType<ApprovalInboxItem> = useMemo(
    () => [
      {
        title: "合同",
        key: "contract",
        render: (_: unknown, item) => (
          <div className="approval-contract-cell">
            <strong>{item.contract_name}</strong>
            <span className="approval-mono">
              {item.contract_number ?? item.contract_id}
            </span>
          </div>
        ),
      },
      {
        title: "轮次与节点",
        key: "round",
        render: (_: unknown, item) => (
          <div className="approval-round-cell">
            <span>第 {item.round_number} 轮</span>
            <span className="approval-muted">
              {item.node_code === null
                ? "未返回节点"
                : approvalNodeLabel(item.node_code)}
            </span>
          </div>
        ),
      },
      {
        title: "状态",
        key: "state",
        render: (_: unknown, item) => itemStatus(item),
      },
      {
        title: "时间",
        key: "time",
        render: (_: unknown, item) => itemTime(item),
      },
      {
        title: "",
        key: "action",
        render: (_: unknown, item) => (
          <Button
            type="link"
            onClick={() => onOpen(itemSelection(item))}
            aria-label={`查看 ${item.contract_name} 第 ${item.round_number} 轮`}
          >
            查看
          </Button>
        ),
      },
    ],
    [onOpen],
  );

  if (loading) {
    return <PageStatus kind="loading" title="正在加载会签列表" />;
  }
  if (items.length === 0) {
    return (
      <PageStatus
        kind="empty"
        title="当前视图没有记录"
        detail={
          view === "todo"
            ? "只有仍在审轮次中由当前身份受理的活动任务会出现在这里。"
            : view === "done"
              ? "这里只显示当前身份实际作出的通过或退回决定。"
              : "这里只显示当前身份发起的会签轮次。"
        }
      />
    );
  }
  return (
    <div className="approval-list">
      <Table<ApprovalInboxItem>
        rowKey={(item) =>
          `${item.view}:${item.task_id ?? item.approval_round_id}`
        }
        columns={columns}
        dataSource={items}
        pagination={false}
        size="middle"
        scroll={{ x: 760 }}
      />
      {hasNext ? (
        <div className="approval-list-more">
          <Button onClick={onLoadMore} loading={loadingMore}>
            加载更多
          </Button>
        </div>
      ) : null}
    </div>
  );
}

function RoundHistory({
  contractId,
  currentRoundId,
  onSelect,
}: {
  contractId: string;
  currentRoundId: string;
  onSelect: (contractId: string, round: ApprovalRoundReadSummary) => void;
}) {
  const history = useContractApprovalRounds(contractId, true);
  const items = history.data?.pages.flatMap((page) => page.items) ?? [];
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
      {!history.isLoading && items.length === 0 ? (
        <p className="approval-muted">没有返回历史轮次。</p>
      ) : null}
      <ul className="approval-round-history">
        {items.map((item) => (
          <li key={item.round.approval_round_id}>
            <button
              type="button"
              className={
                item.round.approval_round_id === currentRoundId
                  ? "is-current"
                  : ""
              }
              onClick={() => onSelect(contractId, item.round)}
            >
              <span>
                第 {item.round.round_number} 轮 ·{" "}
                {approvalRoundStateLabel(item.round.state)}
              </span>
              <span className="approval-muted">
                {formatDateTime(item.round.started_at)} ·{" "}
                {item.decision_count}/{item.task_count} 个节点已决定
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
}: {
  detail: ApprovalRoundReadDetail;
  activeTask: ApprovalRoundReadDetail["tasks"][number] | undefined;
}) {
  const queryClient = useQueryClient();
  const mutation = useDecideApprovalTask();
  const [opinion, setOpinion] = useState("");
  const [notice, setNotice] = useState<string | null>(null);

  if (activeTask === undefined || detail.allowed_actions.length === 0) {
    return null;
  }

  function submit(action: ApprovalDecisionAction) {
    if (activeTask === undefined) return;
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
          setNotice(
            `服务端已确认：第 ${result.round.round_number} 轮 ${approvalRoundStateLabel(result.round.state)}，` +
              `当前节点 ${result.task.node_order} 为${approvalTaskStateLabel(result.task.state)}。`,
          );
          setOpinion("");
        },
        onError: (error) => {
          setNotice(
            isConflict(error)
              ? "当前任务或轮次已经变化，没有提交新决定；请刷新后重新确认。"
              : `决定没有提交成功：${errorMessage(error)}。使用同一意见重试会复用同一幂等键。`,
          );
          void queryClient.invalidateQueries({ queryKey: ["approvals"] });
        },
      },
    );
  }

  return (
    <section className="approval-decision" aria-label="提交审批决定">
      <h3>当前节点：{approvalNodeLabel(activeTask.node_code)}</h3>
      <p className="approval-muted">
        决定由服务端重新检查活动任务、角色与职责分离；页面不会在响应前显示已通过。
      </p>
      <label className="field">
        审批意见或退回理由
        <Input.TextArea
          aria-label="审批意见或退回理由"
          value={opinion}
          maxLength={2000}
          showCount
          autoSize={{ minRows: 3, maxRows: 8 }}
          onChange={(event) => setOpinion(event.target.value)}
        />
      </label>
      {notice !== null ? (
        <Alert
          type={mutation.isError ? "error" : "info"}
          showIcon
          message={notice}
        />
      ) : null}
      <div className="approval-decision-actions">
        <Button
          type="primary"
          loading={mutation.isPending}
          onClick={() => submit("approve")}
        >
          通过
        </Button>
        <Button
          danger
          loading={mutation.isPending}
          onClick={() => submit("return")}
        >
          退回修改
        </Button>
      </div>
    </section>
  );
}

function RevisionDiff({
  detail,
}: {
  detail: ApprovalRoundReadDetail;
}) {
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
          本轮与第 {detail.previous_round.round_number}{" "}
          轮使用同一冻结版本，正文没有变化。
        </p>
      </section>
    );
  }
  const diff = detail.revision_diff;
  return (
    <section className="approval-detail-section" aria-label="轮次差异">
      <h3>轮次差异</h3>
      <p className="approval-muted">
        第 {diff.from_revision_number ?? "—"} 版 → 第 {diff.to_revision_number}{" "}
        版；{diff.changed ? "存在字段变化。" : "未返回字段变化。"}
        {diff.amount_terms.length > 0
          ? ` 金额或标的相关变化 ${diff.amount_terms.length} 项。`
          : ""}
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
          const decision = detail.decisions.find(
            (item) => item.task_id === task.task_id,
          );
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
                {task.activated_at
                  ? ` · 激活 ${formatDateTime(task.activated_at)}`
                  : ""}
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
  detail,
  selectedTaskId,
  onSelectRound,
}: {
  detail: ApprovalRoundReadDetail;
  selectedTaskId: string | null;
  onSelectRound: (contractId: string, round: ApprovalRoundReadSummary) => void;
}) {
  const activeTask = detail.tasks.find(
    (task) => task.task_id === detail.active_task_id,
  );
  return (
    <>
      <header className="approval-detail-head">
        <div>
          <h2>{detail.contract_name}</h2>
          <p className="approval-muted">
            {detail.contract_number ?? detail.contract_id} · 第{" "}
            {detail.round.round_number} 轮 ·{" "}
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
          <dd>
            {detail.round.ended_at
              ? formatDateTime(detail.round.ended_at)
              : "尚未结束"}
          </dd>
        </div>
        <div>
          <dt>流程模板</dt>
          <dd>
            {detail.round.workflow_template_id} /{" "}
            {detail.round.workflow_template_version}
          </dd>
        </div>
        <div>
          <dt>风险处置</dt>
          <dd>
            {detail.risk_disposition_state === "unknown"
              ? "未配置，不能视为已接受风险"
              : detail.risk_disposition_state}
            {detail.risk_disposition_reason
              ? `（${detail.risk_disposition_reason}）`
              : ""}
          </dd>
        </div>
        <div>
          <dt>冻结版本</dt>
          <dd className="approval-mono">{detail.round.contract_revision_id}</dd>
        </div>
      </dl>
      <RoundTimeline detail={detail} selectedTaskId={selectedTaskId} />
      <DecisionForm detail={detail} activeTask={activeTask} />
      <RevisionDiff detail={detail} />
      <RoundHistory
        contractId={detail.contract_id}
        currentRoundId={detail.round.approval_round_id}
        onSelect={onSelectRound}
      />
    </>
  );
}

function ApprovalDetailPanel({
  selection,
  onSelectRound,
  onClose,
}: {
  selection: Selection;
  onSelectRound: (contractId: string, round: ApprovalRoundReadSummary) => void;
  onClose: () => void;
}) {
  const taskId = selection?.kind === "task" ? selection.taskId : null;
  const roundContractId =
    selection?.kind === "round" ? selection.contractId : null;
  const roundId = selection?.kind === "round" ? selection.roundId : null;
  const task = useApprovalTaskDetail(taskId);
  const round = useContractApprovalRound(roundContractId, roundId);
  const active = selection?.kind === "task" ? task : round;
  if (selection === null) {
    return (
      <aside className="approval-detail" aria-label="会签详情">
        <PageStatus
          kind="empty"
          title="选择一条记录查看详情"
          detail="待办、已办和轮次可以在这里查看节点意见与冻结版本差异。"
        />
      </aside>
    );
  }
  if (active.isLoading) {
    return (
      <aside className="approval-detail" aria-label="会签详情">
        <PageStatus kind="loading" title="正在加载会签详情" />
      </aside>
    );
  }
  if (active.error) {
    return (
      <aside className="approval-detail" aria-label="会签详情">
        <Alert
          type="error"
          showIcon
          message="会签详情没有加载"
          description={errorMessage(active.error)}
        />
        <Button type="link" onClick={onClose}>
          关闭
        </Button>
      </aside>
    );
  }
  const detail: ApprovalRoundReadDetail | null =
    selection.kind === "task"
      ? (task.data?.round_detail ?? null)
      : (round.data ?? null);
  const selectedTaskId =
    selection.kind === "task"
      ? (task.data?.selected_task_id ?? null)
      : null;
  if (detail === null) {
    return (
      <aside className="approval-detail" aria-label="会签详情">
        <Empty description="没有返回会签详情" />
      </aside>
    );
  }
  return (
    <aside className="approval-detail" aria-label="会签详情">
      <div className="approval-detail-close">
        <Button type="link" onClick={onClose}>
          关闭
        </Button>
      </div>
      <ApprovalDetailBody
        detail={detail}
        selectedTaskId={selectedTaskId}
        onSelectRound={onSelectRound}
      />
    </aside>
  );
}

export function ApprovalTasksPage() {
  const [params, setParams] = useSearchParams();
  const view = parseView(params.get("view"));
  const session = useSession();
  const actor = session.data ?? null;
  const inbox = useApprovalInbox(view, actorScope(actor), actor !== null);
  const items = useMemo(
    () => inbox.data?.pages.flatMap((page) => page.items) ?? [],
    [inbox.data],
  );
  const [selection, setSelection] = useState<Selection>(null);

  function changeView(next: string) {
    const scope = new URLSearchParams(params);
    scope.set("view", next);
    setParams(scope, { replace: true });
    setSelection(null);
  }

  function selectRound(contractId: string, round: ApprovalRoundReadSummary) {
    setSelection({
      kind: "round",
      contractId,
      roundId: round.approval_round_id,
    });
  }

  return (
    <section className="page approval-page">
      <header className="page-head">
        <div>
          <h1>合同会签</h1>
          <p>
            待办只显示服务端仍标记为活动的任务。通过和退回只有在服务端确认后才会改变状态；失败重试复用同一幂等键。
          </p>
        </div>
      </header>
      <div className="approval-summary" aria-label="会签视图摘要">
        <span>
          当前视图：<strong>{VIEW_LABELS[view]}</strong>
        </span>
        <span>
          已加载 <strong>{items.length}</strong> 项
        </span>
      </div>
      <Tabs
        activeKey={view}
        onChange={changeView}
        items={(["todo", "done", "initiated"] as const).map((key) => ({
          key,
          label: VIEW_LABELS[key],
        }))}
      />
      {inbox.error ? (
        <Alert
          type="error"
          showIcon
          message="会签列表没有加载"
          description={errorMessage(inbox.error)}
        />
      ) : null}
      <div className="approval-layout">
        <section className="approval-inbox" aria-label={VIEW_LABELS[view]}>
          <ApprovalInboxList
            view={view}
            items={items}
            loading={inbox.isLoading}
            hasNext={inbox.hasNextPage}
            loadingMore={inbox.isFetchingNextPage}
            onLoadMore={() => void inbox.fetchNextPage()}
            onOpen={setSelection}
          />
        </section>
        <ApprovalDetailPanel
          key={selectionKey(selection)}
          selection={selection}
          onSelectRound={selectRound}
          onClose={() => setSelection(null)}
        />
      </div>
    </section>
  );
}
