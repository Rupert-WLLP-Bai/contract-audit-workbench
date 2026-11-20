// File metadata (manifest mirror):
// Purpose: Displays the current user’s authorized delivered fulfillment reminders and links to their contracts.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-20
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

import { Alert, Button, Drawer, Table, type TableColumnsType } from "antd";
import { useEffect, useRef, useState } from "react";
import { type ContractLedgerItem, type ContractReminderInboxItem } from "../api/contracts";
import { ApiError, errorMessage } from "../api/http";
import type { Actor } from "../api/types";
import { actorReadContextKey } from "../domain/actor-read-context";
import { useContractReminderInboxQuery } from "../features/contracts/queries";
import "./contract-reminder-inbox.css";

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
    if (error.status === 403) return "当前身份无权查看提醒，请核对账号和组织。";
    if (error.status === 503) return "提醒暂时无法读取。";
  }
  return errorMessage(error);
}

interface Props {
  actor: Actor | null;
  contracts: ContractLedgerItem[];
  onOpenContract: (contractId: string) => Promise<void>;
}

export function ContractReminderInbox({ actor, ...props }: Props) {
  const scope = actorReadContextKey(actor);
  if (!actor || !scope) return null;
  return <ScopedReminderInbox key={scope} actor={actor} {...props} />;
}

function ScopedReminderInbox({ actor, contracts, onOpenContract }: Props) {
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement | HTMLAnchorElement>(null);
  const restoreFocus = useRef(false);
  const close = () => {
    restoreFocus.current = true;
    setOpen(false);
  };
  useEffect(() => {
    if (open || !restoreFocus.current) return;
    restoreFocus.current = false;
    trigger.current?.focus({ preventScroll: true });
  }, [open]);
  const inbox = useContractReminderInboxQuery(actor);
  const items = inbox.isError ? [] : (inbox.data?.items ?? []);
  const error = inbox.isError ? inboxError(inbox.error) : null;
  if (!open && !items.length && !error) return null;

  const contractByID = new Map(contracts.map((item) => [item.contract_id, item]));
  const columns: TableColumnsType<ContractReminderInboxItem> = [
    {
      title: "合同",
      key: "contract",
      width: "37%",
      render: (_value: unknown, item: ContractReminderInboxItem) => {
        const contract = contractByID.get(item.contract_id);
        return (
          <>
            <Button
              className="contract-reminder-open-contract"
              type="link"
              onClick={() => {
                close();
                void onOpenContract(item.contract_id);
              }}
            >
              {contract?.name.trim() || "查看合同"}
            </Button>
            {contract?.number ? (
              <span className="contract-reminder-caption">{contract.number}</span>
            ) : null}
          </>
        );
      },
    },
    {
      title: "计划事项",
      key: "plan",
      width: "19%",
      render: (_value: unknown, item: ContractReminderInboxItem) => planLabel(item),
    },
    {
      title: "计划日期",
      dataIndex: "planned_date",
      key: "planned_date",
      width: "21%",
      render: (date: string) => <time dateTime={date}>{date}</time>,
    },
    {
      title: "送达时间",
      key: "delivered_at",
      width: "23%",
      render: (_value: unknown, item: ContractReminderInboxItem) => (
        <time dateTime={item.delivered_at}>
          {zonedInstant(item.delivered_at, item.timezone)}
          <span className="contract-reminder-caption">
            {item.timezone === "Asia/Shanghai" ? "北京时间" : item.timezone}
          </span>
        </time>
      ),
    },
  ];

  const refresh = (
    <Button onClick={() => void inbox.refetch()} loading={inbox.isFetching}>
      {error ? "重试读取提醒" : "刷新提醒"}
    </Button>
  );

  return (
    <>
      {items.length > 0 ? (
        <section className="contract-reminder-notice" aria-label="履约提醒">
          <span>
            有 <strong>{items.length}</strong> 项履约或付款提醒
          </span>
          <Button ref={trigger} type="link" aria-haspopup="dialog" onClick={() => setOpen(true)}>
            查看提醒
          </Button>
        </section>
      ) : error && !open ? (
        <Alert className="contract-reminder-notice" type="error" title={error} action={refresh} />
      ) : null}
      <Drawer
        title="我的履约提醒"
        size="min(520px, 100vw)"
        open={open}
        onClose={close}
        focusable={{ focusTriggerAfterClose: false }}
        extra={refresh}
        destroyOnHidden
      >
        {error ? (
          <Alert type="error" title={error} />
        ) : (
          <Table<ContractReminderInboxItem>
            className="contract-reminder-table"
            rowKey="reminder_id"
            columns={columns}
            dataSource={items}
            loading={inbox.isFetching}
            pagination={{ pageSize: 5, hideOnSinglePage: true, showSizeChanger: false }}
            size="small"
            tableLayout="fixed"
            locale={{ emptyText: "暂无提醒" }}
          />
        )}
      </Drawer>
    </>
  );
}
