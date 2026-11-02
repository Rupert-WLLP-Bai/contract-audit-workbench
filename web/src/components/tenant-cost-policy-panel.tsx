// File metadata (manifest mirror):
// Purpose: 实现 `isApiErrorCode`, `CapRow`, `formatUsdMicro`, `amount` 等界面组件或状态处理，呈现服务端返回的业务数据并提交受控用户操作。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Input, Table, type TableProps } from "antd";
import { captureCommandContext, errorMessage } from "../api/http";
import { updateTenantCostCaps, useTenantCostPolicy, type TenantCostPolicy } from "../api/tenant-cost-policy";
import type { Actor } from "../api/types";
import "./tenant-cost-policy-panel.css";

function isApiErrorCode(error: unknown, code: string): boolean {
  return typeof error === "object" && error !== null && "code" in error && error.code === code;
}

type CapRow = { key: string; label: string; amount: string | null };
function formatUsdMicro(value: string | null): string {
  if (value === null) return "未设置";
  const amount = BigInt(value);
  return `USD ${(amount / 1_000_000n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",")}.${(amount % 1_000_000n).toString().padStart(6, "0")}`;
}
function toUsdInput(value: string | null): string {
  if (value === null) return "";
  const amount = BigInt(value);
  const whole = (amount / 1_000_000n).toString();
  const fraction = (amount % 1_000_000n).toString().padStart(6, "0").replace(/0+$/, "");
  return fraction ? `${whole}.${fraction}` : whole;
}
function fromUsdInput(value: string): string | null {
  const normalized = value.trim();
  if (normalized === "") return null;
  const match = /^(0|[1-9][0-9]*)(?:\.([0-9]{1,6}))?$/.exec(normalized);
  if (!match) throw new Error("金额请输入非负 USD 数值，最多六位小数。");
  return (BigInt(match[1]!) * 1_000_000n + BigInt((match[2] ?? "").padEnd(6, "0") || "0")).toString();
}
function CapFacts({ data }: { data: TenantCostPolicy }) {
  const rows: CapRow[] = [
    { key: "day", label: "每日硬限额", amount: data.day_limit_minor },
    { key: "month", label: "每月硬限额", amount: data.month_limit_minor },
  ];
  const columns: TableProps<CapRow>["columns"] = [
    { title: "窗口", dataIndex: "label", rowScope: "row" },
    { title: "当前租户上限", dataIndex: "amount", render: (value: string | null) => formatUsdMicro(value) },
  ];
  return <Table<CapRow> rowKey="key" size="small" pagination={false} columns={columns} dataSource={rows} />;
}
function UsageFacts({ label, period, values, priceKnown }: { label: string; period: string; values: TenantCostPolicy["day"]; priceKnown: boolean }) {
  const reported = (amount: string) => priceKnown ? formatUsdMicro(amount) : "未确认";
  return <section className="tenant-policy-window" aria-label={`${label}费用用量`}>
    <h3>{label} · {period} UTC</h3>
    <dl>
      <div><dt>已结算</dt><dd>{reported(values.settled_minor)}</dd></div>
      <div><dt>预留</dt><dd>{reported(values.reserved_minor)}</dd></div>
      <div data-tone="attention"><dt>未知费用预留</dt><dd>{reported(values.unknown_hold_minor)}</dd></div>
      <div><dt>总暴露额</dt><dd>{reported(values.exposure_minor)}</dd></div>
    </dl>
  </section>;
}
export function TenantCostPolicyPanel({ actor }: { actor: Actor }) {
  const queryClient = useQueryClient();
  const allowed = actor.audience === "admin" && actor.role === "admin";
  const query = useTenantCostPolicy(actor.tenant_id, allowed);
  const [day, setDay] = useState("");
  const [month, setMonth] = useState("");
  const [notice, setNotice] = useState<string | null>(null);
  const [validationError, setValidationError] = useState<string | null>(null);
  const data = query.data;
  useEffect(() => {
    setDay(toUsdInput(data?.day_limit_minor ?? null));
    setMonth(toUsdInput(data?.month_limit_minor ?? null));
    setNotice(null);
    setValidationError(null);
  }, [data?.tenant_id, data?.revision]);
  const mutation = useMutation({
    mutationFn: ({ revision, dayCap, monthCap, commandContext }: { revision: string; dayCap: string | null; monthCap: string | null; commandContext: ReturnType<typeof captureCommandContext> }) =>
      updateTenantCostCaps(revision, dayCap, monthCap, commandContext),
    onSuccess: async (next) => {
      setNotice("本租户限额已更新。");
      await queryClient.setQueryData(["tenant-cost-policy", next.tenant_id], next);
      await queryClient.invalidateQueries({ queryKey: ["tenant-operations", "admin"] });
    },
    onError: async (error) => {
      setNotice(errorMessage(error));
      if (isApiErrorCode(error, "money_policy_revision_conflict") || isApiErrorCode(error, "context_stale")) {
        if (isApiErrorCode(error, "context_stale")) await queryClient.invalidateQueries({ queryKey: ["session", "admin"] });
        const refreshed = await query.refetch();
        if (refreshed.data) {
          setDay(toUsdInput(refreshed.data.day_limit_minor));
          setMonth(toUsdInput(refreshed.data.month_limit_minor));
        }
      }
    },
  });
  if (!allowed) return null;
  if (query.isLoading) return <section className="tenant-cost-policy-panel"><h2>模型费用限额</h2><p role="status">正在加载费用政策…</p></section>;
  if (query.isError || !data) return <section className="tenant-cost-policy-panel"><h2>模型费用限额</h2><Alert type="error" message="费用政策不可用" description={query.error ? errorMessage(query.error) : "服务没有返回费用政策。"} /><Button onClick={() => void query.refetch()}>重试</Button></section>;
  const dirty = day !== toUsdInput(data.day_limit_minor) || month !== toUsdInput(data.month_limit_minor);
  const save = () => {
    setValidationError(null);
    setNotice(null);
    try {
      const dayCap = fromUsdInput(day);
      const monthCap = fromUsdInput(month);
      const commandContext = captureCommandContext(actor);
      mutation.mutate({ revision: data.revision, dayCap, monthCap, commandContext });
    } catch (error) {
      setValidationError(error instanceof Error ? error.message : "限额输入无效。");
    }
  };
  return <section className="tenant-cost-policy-panel" aria-labelledby="tenant-cost-policy-title">
    <header><div><h2 id="tenant-cost-policy-title">模型费用限额</h2><p>平台价格与平台级限额由服务端按授权管理；本页只可调整当前租户的每日、每月硬限额。USD 金额最多六位小数。</p></div></header>
    <div className="tenant-policy-price" data-tone={data.price_state === "known" ? "good" : "attention"}>
      <strong>价格依据</strong>
      <span>{data.price_state === "known" ? "服务端报告价格已配置；当前接口不提供价格明细。" : "价格表未配置；费用未知项会继续单独保留，不按零计。"}</span>
    </div>
    <CapFacts data={data} />
    <div className="tenant-policy-editor">
      <label>本租户每日硬限额（USD）<Input inputMode="decimal" value={day} onChange={(event) => setDay(event.target.value)} placeholder="留空表示不单独设限" /></label>
      <label>本租户每月硬限额（USD）<Input inputMode="decimal" value={month} onChange={(event) => setMonth(event.target.value)} placeholder="留空表示不单独设限" /></label>
      <Button type="primary" disabled={!dirty} loading={mutation.isPending} onClick={save}>保存租户限额</Button>
    </div>
    {!data.configured ? <Alert type="warning" showIcon message="租户尚未设置金额限额" description="费用账本当前价格状态未知；仍可配置本租户的硬限额，未知费用不会按零处理。" /> : null}
    {validationError ? <Alert type="error" message={validationError} /> : null}
    {notice ? <p role="status">{notice}</p> : null}
    <div className="tenant-policy-periods"><UsageFacts label="每日" period={data.day.period_key} values={data.day} priceKnown={data.price_state === "known"} /><UsageFacts label="每月" period={data.month.period_key} values={data.month} priceKnown={data.price_state === "known"} /></div>
    <p className="meta">金额采用精确微美元整数口径。只有可信价格状态已确认时才展示窗口金额；当前价格状态未知，服务端提供的占位值不作为零费用展示。</p>
  </section>;
}
