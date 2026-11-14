// File metadata (manifest mirror):
// Purpose: Displays the current user’s authorized delivered fulfillment reminders and links to their contracts.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { Alert, Button, Empty, Table, type TableColumnsType } from "antd";
import { useMemo } from "react";
import { type ContractLedgerItem, type ContractReminderInboxItem } from "../api/contracts";
import { ApiError, errorMessage } from "../api/http";
import type { Actor } from "../api/types";
import { useContractReminderInboxQuery } from "../features/contracts/queries";

function planLabel(item: ContractReminderInboxItem): string {
  const kind =
    item.plan_kind === "delivery" ? "交付" : item.plan_kind === "acceptance" ? "验收" : "付款";
  return `${kind}第 ${item.plan_sequence} 期`;
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

function inboxError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 401) return "登录状态已失效，请重新登录后查看。";
    if (error.status === 403) return "当前身份无权读取此收件箱；请检查登录账号和组织。";
    if (error.status === 503) return "站内提醒暂时无法读取，请联系管理员检查服务状态。";
  }
  return errorMessage(error);
}

export function ContractReminderInbox({
  actor,
  contracts,
  onOpenContract,
}: {
  actor: Actor | null;
  contracts: ContractLedgerItem[];
  onOpenContract: (contractId: string) => Promise<void>;
}) {
  const inbox = useContractReminderInboxQuery(actor);
  const items = inbox.isError ? [] : (inbox.data?.items ?? []);
  const loading = inbox.isFetching;
  const error = inbox.error ? inboxError(inbox.error) : null;
  const contractByID = useMemo(
    () => new Map(contracts.map((item) => [item.contract_id, item])),
    [contracts],
  );

  const columns = useMemo<TableColumnsType<ContractReminderInboxItem>>(
    () => [
      {
        title: "合同",
        key: "contract",
        render: (_value: unknown, item: ContractReminderInboxItem) => {
          const contract = contractByID.get(item.contract_id);
          const title = contract?.name?.trim() || "合同名称未在当前台账页加载";
          return (
            <div>
              <Button type="link" onClick={() => void onOpenContract(item.contract_id)}>
                {title}
              </Button>
              <div className="meta">
                {contract?.number ? `编号 ${contract.number}` : "合同编号未提供"}
              </div>
            </div>
          );
        },
      },
      {
        title: "计划事项",
        dataIndex: "plan_kind",
        key: "plan",
        render: (_value: unknown, item: ContractReminderInboxItem) => planLabel(item),
      },
      { title: "冻结计划日期", dataIndex: "planned_date", key: "planned_date" },
      {
        title: "站内送达",
        key: "delivered_at",
        render: (_value: unknown, item: ContractReminderInboxItem) => (
          <>
            {zonedInstant(item.delivered_at, item.timezone)}
            <div className="meta">{item.timezone} · 已送达（不提供已读状态）</div>
          </>
        ),
      },
    ],
    [contractByID, onOpenContract],
  );

  return (
    <section className="contracts-page-section" aria-label="我的履约提醒收件箱">
      <div className="contracts-ledger-head">
        <div>
          <h2>我的履约提醒</h2>
          <p>仅显示已成功保存的站内投递，并再次按当前身份和合同读取权限过滤；不表示已读。</p>
        </div>
        <Button onClick={() => void inbox.refetch()} loading={loading} disabled={!actor}>
          刷新提醒
        </Button>
      </div>
      {!actor ? (
        <Alert type="info" showIcon title="登录后可查看分配给当前登录人的站内提醒。" />
      ) : null}
      {error ? <Alert type="error" showIcon title={error} /> : null}
      {actor ? (
        <Table<ContractReminderInboxItem>
          rowKey="reminder_id"
          columns={columns}
          dataSource={items}
          loading={loading}
          pagination={{ pageSize: 5, hideOnSinglePage: true, showSizeChanger: false }}
          size="small"
          scroll={{ x: "max-content" }}
          locale={{
            emptyText: (
              <Empty
                description={error ? "收件箱暂不可用" : "当前没有已成功送达且仍可读取的站内提醒"}
              />
            ),
          }}
        />
      ) : null}
    </section>
  );
}
