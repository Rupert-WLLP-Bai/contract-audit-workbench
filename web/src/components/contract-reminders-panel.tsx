// File metadata (manifest mirror):
// Purpose: Presents frozen contract plan dates and authorized creation and cancellation of fulfillment reminders.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { Alert, Button, Empty, Form, InputNumber, Select, Tag } from "antd";
import { useMemo, useState } from "react";
import {
  cancelContractReminder,
  createContractReminder,
  type ContractDetail,
  type ContractReminder,
  type ContractReminderCreateInput,
  type ContractReminderPlan,
  type ContractReminderPlanKind,
} from "../api/contracts";
import { ApiError, errorMessage } from "../api/http";
import {
  clearPendingMutationKey,
  idempotencyActorScope,
  pendingMutationKey,
} from "../api/idempotency";
import type { Actor } from "../api/types";
import { useContractRemindersQuery } from "../features/contracts/queries";

type ReminderFormValues = {
  planKey: string;
  timezone: string;
  leadDays: number;
};

const PLAN_LABELS: Record<ContractReminderPlanKind, string> = {
  delivery: "交付",
  acceptance: "验收",
  payment: "付款",
};

function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "Asia/Shanghai";
  } catch {
    return "Asia/Shanghai";
  }
}

function planKey(plan: Pick<ContractReminderPlan, "kind" | "sequence">): string {
  return `${plan.kind}:${plan.sequence}`;
}

function friendlyError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.code === "reminder_worker_unavailable")
      return "提醒服务尚未启用，请联系管理员；当前提醒未确认。";
    if (error.status === 401) return "登录状态已失效，请重新登录后再操作。";
    if (error.status === 403)
      return "当前身份没有管理此合同提醒的授权。请切换到有合同管理权限的组织或账号。";
    if (error.status === 404) return "合同已不可见或不在当前租户范围内；请返回台账重新打开。";
    if (error.status === 409) return "合同修订或提醒状态已变化，请刷新后核对冻结计划再重试。";
    if (error.status === 503) return "提醒服务暂不可用；当前提醒未确认，请联系管理员检查服务状态。";
  }
  return errorMessage(error);
}

function zonedInstant(value: string, timezone: string): string {
  const instant = new Date(value);
  if (Number.isNaN(instant.getTime())) return "时间无法识别";
  try {
    return new Intl.DateTimeFormat("zh-CN", {
      timeZone: timezone,
      dateStyle: "medium",
      timeStyle: "short",
      hourCycle: "h23",
    }).format(instant);
  } catch {
    return "时区无法识别";
  }
}

function statusView(
  reminder: ContractReminder,
  currentRevisionId: string | null,
): {
  label: string;
  color?: string;
  explanation: string;
} {
  if (reminder.status === "cancelled")
    return { label: "已取消", explanation: "取消记录保留在历史中。" };
  if (reminder.status === "delivered")
    return {
      label: "已送达",
      color: "success",
      explanation: "站内投递事实已保存；系统没有已读状态。",
    };
  if (reminder.status === "stale")
    return {
      label: "已失效",
      color: "warning",
      explanation: "投递时合同状态或冻结计划已变化，未按旧计划发送。请核对当前修订。",
    };
  if (reminder.status === "denied")
    return {
      label: "权限失效",
      color: "error",
      explanation: "投递时负责人已无合同读取权限。可取消旧项，再由当前有权成员创建提醒。",
    };
  if (reminder.status === "failed")
    return {
      label: "投递失败",
      color: "error",
      explanation: "提醒投递尝试已失败。可取消旧项并按仍有效的计划重新设置。",
    };
  if (currentRevisionId && reminder.contract_revision_id !== currentRevisionId) {
    return {
      label: "旧修订待核验",
      color: "warning",
      explanation:
        "此提醒绑定旧冻结修订；到期时服务端会复核并将不匹配项置为失效。若仍需提醒，请取消后按当前修订重设。",
    };
  }
  return {
    label: "等待投递",
    color: "processing",
    explanation: "到达设定时点后由站内任务投递；没有成功投递前不会出现在收件箱。",
  };
}

export function ContractRemindersPanel({
  detail,
  actor,
  canManage,
  onRefresh,
}: {
  detail: ContractDetail;
  actor: Actor | null;
  canManage: boolean;
  onRefresh: () => Promise<void>;
}) {
  const contractId = detail.summary.contract_id;
  const currentRevisionId = detail.current_revision_id;
  const active =
    detail.summary.contract_state === "registered" ||
    detail.summary.contract_state === "performing";
  const isNative = detail.summary.authority_mode === "native";
  const remindersQuery = useContractRemindersQuery(
    contractId,
    currentRevisionId,
    detail.summary.row_version,
    actor,
    active,
    isNative,
  );
  const reminders = remindersQuery.isError ? null : (remindersQuery.data ?? null);
  const loading = remindersQuery.isFetching;
  const readError = remindersQuery.error ? friendlyError(remindersQuery.error) : null;
  const canWrite = Boolean(actor && canManage);
  const [form] = Form.useForm<ReminderFormValues>();
  const [busyId, setBusyId] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const initialTimezone = useMemo(() => browserTimezone(), []);
  const error = actionError ?? readError;

  function retryRead() {
    setActionError(null);
    void remindersQuery.refetch();
  }

  const timezoneOptions = useMemo(
    () =>
      [
        ...new Set([
          initialTimezone,
          "Asia/Shanghai",
          "Asia/Hong_Kong",
          "Asia/Singapore",
          "Asia/Tokyo",
          "UTC",
          "Europe/London",
          "America/New_York",
        ]),
      ].map((timezone) => ({ value: timezone, label: timezone })),
    [initialTimezone],
  );
  const currentRevisionAligned = Boolean(
    currentRevisionId && reminders?.contract_revision_id === currentRevisionId,
  );
  const availablePlans = (reminders?.plans ?? []).filter(
    (plan) =>
      !(reminders?.reminders ?? []).some(
        (item) =>
          item.status !== "cancelled" &&
          item.contract_revision_id === currentRevisionId &&
          item.plan_kind === plan.kind &&
          item.plan_sequence === plan.sequence,
      ),
  );

  async function createReminder(values: ReminderFormValues) {
    if (
      !actor ||
      !canManage ||
      !active ||
      !reminders ||
      !currentRevisionId ||
      !currentRevisionAligned
    )
      return;
    const separator = values.planKey.lastIndexOf(":");
    const kind = values.planKey.slice(0, separator) as ContractReminderPlanKind;
    const sequence = Number(values.planKey.slice(separator + 1));
    const plan = reminders.plans.find((item) => item.kind === kind && item.sequence === sequence);
    if (
      !plan ||
      !Number.isInteger(values.leadDays) ||
      values.leadDays < 0 ||
      values.leadDays > 365
    ) {
      setActionError("请选择有效的冻结计划，并将提前天数设置为 0 到 365 的整数。");
      return;
    }
    const input: ContractReminderCreateInput = {
      contract_revision_id: currentRevisionId,
      plan_kind: plan.kind,
      plan_sequence: plan.sequence,
      timezone: values.timezone,
      recipient_id: actor.id,
      lead_days: values.leadDays,
      expected_contract_row_version: reminders.contract_row_version,
    };
    setBusyId("create");
    setActionError(null);
    setMessage(null);
    let pending: Awaited<ReturnType<typeof pendingMutationKey>> | null = null;
    try {
      pending = await pendingMutationKey(
        "clm-fulfillment-reminder-create",
        idempotencyActorScope(actor),
        contractId,
        input,
      );
      await createContractReminder(contractId, input, pending.key);
      clearPendingMutationKey(pending.storageKey);
      setMessage(
        `已为${actor.display_name}确认提醒。日期来自当前冻结修订，服务端会按所选时区计算提醒时刻。`,
      );
      await onRefresh();
      await remindersQuery.refetch();
    } catch (caught) {
      setActionError(friendlyError(caught));
    } finally {
      setBusyId(null);
    }
  }

  async function cancelReminder(reminder: ContractReminder) {
    if (!actor || !canManage) return;
    const input = { expected_version: reminder.version };
    setBusyId(reminder.reminder_id);
    setActionError(null);
    setMessage(null);
    let pending: Awaited<ReturnType<typeof pendingMutationKey>> | null = null;
    try {
      pending = await pendingMutationKey(
        "clm-fulfillment-reminder-cancel",
        idempotencyActorScope(actor),
        contractId,
        { reminder_id: reminder.reminder_id, ...input },
      );
      await cancelContractReminder(contractId, reminder.reminder_id, input, pending.key);
      clearPendingMutationKey(pending.storageKey);
      setMessage(
        "提醒已取消；原确认记录仍保留在履约历史中。需要更改时，请按当前冻结修订重新创建。 ",
      );
      await onRefresh();
      await remindersQuery.refetch();
    } catch (caught) {
      setActionError(friendlyError(caught));
    } finally {
      setBusyId(null);
    }
  }

  return (
    <section className="contracts-page-section" aria-label="履约提醒">
      <h4>履约提醒</h4>
      <Alert
        type="info"
        showIcon
        title="提醒我：仅从当前合同冻结修订中明确记录日期的交付、验收或付款计划创建；不会推测缺失日期。修改计划请取消旧项后按新修订重设，历史保留。"
      />
      {!active ? (
        <Alert type="info" showIcon title="合同当前不能新增履约提醒；以下保留历史记录。" />
      ) : null}
      {loading ? <p role="status">正在读取履约提醒…</p> : null}
      {error ? <Alert type="error" showIcon title={error} /> : null}
      {message ? <Alert type="success" showIcon title={message} /> : null}
      {!actor ? (
        <Alert type="warning" showIcon title="请先登录；提醒责任人固定为当前登录人。" />
      ) : null}
      {!isNative ? (
        <Alert type="info" showIcon title="此合同来自外部来源，只读模式不支持设置履约提醒。" />
      ) : null}
      {actor && !canManage ? (
        <Alert
          type="warning"
          showIcon
          title="当前身份只能查看已授权合同信息，不能设置或取消提醒。请切换到有合同管理权限的账号。"
        />
      ) : null}
      {error && error.includes("没有管理此合同提醒的授权") ? (
        <p className="meta">服务端已拒绝当前写入权限；切换授权上下文后重新打开合同。</p>
      ) : null}

      {actor && isNative && reminders ? (
        <>
          {active && currentRevisionId && !currentRevisionAligned ? (
            <Alert
              type="warning"
              showIcon
              title="合同详情与提醒读取到的冻结修订不一致，请刷新合同详情后再操作。"
            />
          ) : null}
          {!currentRevisionId ? (
            <Empty description="合同尚无当前冻结修订，不能确认履约提醒。" />
          ) : null}
          {currentRevisionAligned && reminders.plans.length === 0 ? (
            <Empty
              description={
                <span>
                  服务端未从当前冻结修订找到可用的明确计划日期。可能尚未录入计划、日期无效或期次编号有歧义；系统不会推测日期。
                </span>
              }
            />
          ) : null}
          {currentRevisionAligned && reminders.plans.length > 0 ? (
            <>
              <p className="meta">
                日期来源：当前冻结修订 <code>{reminders.contract_revision_id}</code>
                。提醒时刻为计划日期前所选天数的当地 09:00，服务端同时保存对应 UTC 时间。
              </p>
              <p className="meta">
                责任人：{actor.display_name}（当前登录人，{actor.active_context.org_name}
                ）。现阶段此入口只支持“提醒我”。
              </p>
              {canWrite && active && currentRevisionAligned && availablePlans.length > 0 ? (
                <Form<ReminderFormValues>
                  form={form}
                  layout="vertical"
                  initialValues={{ timezone: initialTimezone, leadDays: 7 }}
                  onFinish={(values) => void createReminder(values)}
                  scrollToFirstError={{ focus: true }}
                >
                  <Form.Item
                    name="planKey"
                    label="计划事项"
                    rules={[{ required: true, message: "请选择一项有明确日期的冻结计划。" }]}
                  >
                    <Select
                      placeholder="选择交付、验收或付款期次"
                      options={availablePlans.map((plan) => ({
                        value: planKey(plan),
                        label: `${PLAN_LABELS[plan.kind]}第 ${plan.sequence} 期 · ${plan.planned_date}${plan.description ? ` · ${plan.description}` : ""}`,
                        title: plan.description,
                      }))}
                    />
                  </Form.Item>
                  <Form.Item
                    name="timezone"
                    label="提醒时区"
                    extra="请选择合同履约所用的 IANA 时区；当前浏览器时区已作为默认值。"
                    rules={[{ required: true, message: "请选择提醒时区。" }]}
                  >
                    <Select options={timezoneOptions} showSearch={{ optionFilterProp: "label" }} />
                  </Form.Item>
                  <Form.Item
                    name="leadDays"
                    label="提前天数"
                    extra="0 表示计划日期当天当地 09:00；范围为 0 到 365 天。"
                    rules={[{ required: true, message: "请设置提醒提前天数。" }]}
                  >
                    <InputNumber
                      min={0}
                      max={365}
                      precision={0}
                      step={1}
                      style={{ width: "100%" }}
                    />
                  </Form.Item>
                  <Button
                    type="primary"
                    htmlType="submit"
                    loading={busyId === "create"}
                    disabled={Boolean(busyId) || !actor || !currentRevisionAligned}
                  >
                    确认提醒我
                  </Button>
                </Form>
              ) : null}
              {canWrite && active && availablePlans.length === 0 ? (
                <Alert
                  type="info"
                  showIcon
                  title="当前可用期次均已有提醒。请查看下方记录；未投递项如需调整，请先取消对应提醒。"
                />
              ) : null}
            </>
          ) : null}
          {reminders.reminders.length > 0 ? (
            <div aria-label="履约提醒历史">
              <h5>提醒记录</h5>
              {reminders.reminders.map((reminder) => {
                const status = statusView(reminder, currentRevisionId);
                const canCancel =
                  canWrite && reminder.status !== "cancelled" && reminder.status !== "delivered";
                const responsible =
                  reminder.recipient_id === actor.id
                    ? actor.display_name
                    : "原指派人员（姓名未提供）";
                return (
                  <article key={reminder.reminder_id} className="contracts-page-section">
                    <p>
                      <Tag color={status.color}>{status.label}</Tag>
                      <strong>
                        {PLAN_LABELS[reminder.plan_kind]}第 {reminder.plan_sequence} 期
                      </strong>
                      · 冻结计划日期 {reminder.planned_date}
                    </p>
                    <p>
                      责任人：{responsible} · 提前 {reminder.lead_days} 天 · {reminder.timezone}{" "}
                      当地 09:00
                    </p>
                    <p>
                      预计提醒时刻：{zonedInstant(reminder.due_at, reminder.timezone)}（
                      {reminder.timezone}）
                    </p>
                    <p>{status.explanation}</p>
                    {canCancel ? (
                      <Button
                        danger
                        loading={busyId === reminder.reminder_id}
                        disabled={Boolean(busyId)}
                        onClick={() => void cancelReminder(reminder)}
                      >
                        取消并保留历史
                      </Button>
                    ) : null}
                  </article>
                );
              })}
            </div>
          ) : null}
          {canWrite && !currentRevisionAligned ? (
            <Button onClick={retryRead} loading={loading}>
              刷新提醒与冻结修订
            </Button>
          ) : null}
        </>
      ) : null}
      {!loading && error && actor ? <Button onClick={retryRead}>重试读取提醒</Button> : null}
    </section>
  );
}
