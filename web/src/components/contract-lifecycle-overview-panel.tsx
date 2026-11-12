// File metadata (manifest mirror):
// Purpose: 实现 `ContractAcceptanceList`, `ContractAssetList`, `ContractDeliveryList`, `ContractDetail` 等界面组件或状态处理，呈现服务端返回的业务数据并提交受控用户操作。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-08
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

import { Alert, Button, Card, Tag } from "antd";
import { useEffect, useRef, useState } from "react";
import {
  getContractLifecycleOverview,
  type ContractDetail,
  type ContractLifecycleOverview,
  type ContractPaymentSummary,
} from "../api/contracts";
import { ApiError } from "../api/http";
import { actorReadContextKey } from "../domain/actor-read-context";
import type { Actor } from "../api/types";

type OverviewState = {
  state: "loading" | "ready" | "unavailable";
  value?: ContractLifecycleOverview;
  stale: boolean;
  denied: boolean;
};

const loadingState = (): OverviewState => ({ state: "loading", stale: false, denied: false });

function unavailableState(denied = false): OverviewState {
  return { state: "unavailable", stale: false, denied };
}

function isAccessDenied(error: unknown): boolean {
  return error instanceof ApiError && [401, 403, 404].includes(error.status);
}

function completenessLabel(
  state: ContractLifecycleOverview["completeness"][keyof ContractLifecycleOverview["completeness"]],
  confirmedEmpty = false,
): string {
  if (state === "known") return confirmedEmpty ? "暂无" : "已读取";
  if (state === "not_applicable") return "明确不适用";
  return "尚未核实";
}

function resultTag(result: "passed" | "partial" | "failed") {
  if (result === "passed") return <Tag color="green">通过</Tag>;
  if (result === "partial") return <Tag color="orange">部分通过</Tag>;
  return <Tag color="red">未通过</Tag>;
}

function paymentTag(summary: ContractPaymentSummary) {
  if (summary.receipts.length === 0 || summary.state === "unknown") return <Tag>付款状态未知</Tag>;
  if (summary.state === "paid") return <Tag color="green">账本记录已达到计划金额</Tag>;
  if (summary.state === "partial") return <Tag color="orange">账本记录部分付款</Tag>;
  if (summary.state === "overpaid") return <Tag color="red">账本记录超过计划金额</Tag>;
  return <Tag>可信回执净额为零</Tag>;
}

/** Read-only lifecycle projection returned by one authorized server read. */
export function ContractLifecycleOverviewPanel({
  detail,
  actor,
  onAccessDenied,
}: {
  detail: ContractDetail;
  actor: Actor | null;
  onAccessDenied?: () => void;
}) {
  const contractId = detail.summary.contract_id;
  const revisionId = detail.current_revision_id;
  const rowVersion = detail.summary.row_version;
  const identity = actorReadContextKey(actor);
  const validIdentity =
    actor !== null &&
    Boolean(
      actor.id.trim() &&
      actor.tenant_id.trim() &&
      actor.org_id.trim() &&
      (actor.audience === "client" || actor.audience === "admin"),
    );
  const [reloadNonce, setReloadNonce] = useState(0);
  const onAccessDeniedRef = useRef(onAccessDenied);
  onAccessDeniedRef.current = onAccessDenied;
  const requestGeneration = useRef(0);
  const identityRef = useRef({ contractId, revisionId, rowVersion, identity });
  identityRef.current = { contractId, revisionId, rowVersion, identity };
  const requestKey = JSON.stringify([contractId, revisionId, rowVersion, identity, reloadNonce]);
  const [overview, setOverview] = useState<{ key: string; data: OverviewState }>({
    key: "",
    data: loadingState(),
  });
  // Do not render the previous identity's projection during the render before
  // the effect cleanup/reset runs (for example, after an account switch).
  const currentOverview = overview.key === requestKey ? overview.data : loadingState();

  useEffect(() => {
    const generation = ++requestGeneration.current;
    const controller = new AbortController();
    setOverview({ key: requestKey, data: loadingState() });

    if (!validIdentity || !contractId.trim()) {
      setOverview({ key: requestKey, data: unavailableState(true) });
      onAccessDeniedRef.current?.();
      return () => {
        controller.abort();
        requestGeneration.current++;
      };
    }

    const isCurrent = () => {
      const current = identityRef.current;
      return (
        generation === requestGeneration.current &&
        current.contractId === contractId &&
        current.revisionId === revisionId &&
        current.rowVersion === rowVersion &&
        current.identity === identity
      );
    };

    let denialHandled = false;
    const failClosed = <T,>(request: Promise<T>): Promise<T> =>
      request.catch((error: unknown) => {
        if (!denialHandled && isCurrent() && isAccessDenied(error)) {
          denialHandled = true;
          controller.abort();
          setOverview({ key: requestKey, data: unavailableState(true) });
          onAccessDeniedRef.current?.();
        }
        throw error;
      });

    void failClosed(getContractLifecycleOverview(contractId, controller.signal))
      .then((value) => {
        if (!isCurrent() || denialHandled) return;
        const stale =
          value.contract.contract_id !== contractId ||
          value.contract.current_revision_id !== revisionId ||
          value.contract.row_version !== rowVersion;
        setOverview({ key: requestKey, data: { state: "ready", value, stale, denied: false } });
      })
      .catch(() => {
        if (isCurrent() && !denialHandled)
          setOverview({ key: requestKey, data: unavailableState() });
      });
    return () => {
      controller.abort();
      requestGeneration.current++;
    };
  }, [contractId, identity, reloadNonce, requestKey, revisionId, rowVersion, validIdentity]);

  // Denied or invalid identity means no part of the detail projection is safe
  // to keep rendering, including synchronous frozen-plan/revision fields.
  if (!validIdentity || currentOverview.denied) {
    return (
      <Card title="履约进度">
        <Alert
          type="warning"
          showIcon
          title="当前无法查看这份概览"
          description="当前身份无效或访问权限已变化。请重新确认登录和授权后再试。"
        />
      </Card>
    );
  }

  const projection = currentOverview.stale ? undefined : currentOverview.value;
  const deliveryRows = projection?.deliveries.items ?? [];
  const acceptanceRows = projection?.acceptances.items ?? [];
  const paymentRows = projection?.payments ?? [];
  const assetPlans = projection?.assets.items ?? [];
  const revisionLabel = revisionId ?? "当前合同尚无冻结修订";
  const plans = projection?.completeness.plans === "known" ? projection.plans : null;
  const frozenPlanReadable = plans !== null && plans !== undefined;
  const reminders = frozenPlanReadable
    ? [
        ...(plans.delivery_plans ?? []).map((plan) => ({
          key: `delivery-${plan.sequence}`,
          kind: "交付计划",
          sequence: plan.sequence,
          date: plan.planned_date,
          observed:
            projection?.completeness.deliveries === "known"
              ? deliveryRows.some(
                  (row) =>
                    row.contract_revision_id === revisionId &&
                    row.delivery_plan_sequence === plan.sequence,
                )
                ? "已有对应交付记录"
                : "尚未交付"
              : "暂未读取",
        })),
        ...(plans.acceptance_plans ?? []).map((plan) => ({
          key: `acceptance-${plan.sequence}`,
          kind: "验收计划",
          sequence: plan.sequence,
          date: plan.planned_date,
          observed:
            projection?.completeness.acceptances === "known"
              ? acceptanceRows.some(
                  (row) =>
                    row.contract_revision_id === revisionId &&
                    row.acceptance_plan_sequence === plan.sequence,
                )
                ? "已有对应验收记录"
                : "尚未验收"
              : "暂未读取",
        })),
        ...(plans.payments ?? []).map((plan) => ({
          key: `payment-${plan.sequence}`,
          kind: "付款计划",
          sequence: plan.sequence,
          date: plan.planned_date,
          observed:
            projection?.completeness.payments === "known" && projection.payments !== null
              ? paymentRows.some(
                  (row) =>
                    row.contract_revision_id === revisionId &&
                    row.payment_sequence === plan.sequence &&
                    row.receipts.length > 0,
                )
                ? "已有可信回执记录"
                : "尚无回执"
              : "暂未读取",
        })),
      ]
    : [];

  return (
    <Card
      title="履约进度"
      extra={<Button onClick={() => setReloadNonce((value) => value + 1)}>刷新</Button>}
    >
      <Alert
        type="info"
        showIcon
        title="履约进度"
        description="争议、质保和其他未完事项若尚未登记，关闭前仍需人工核对。"
      />
      <Alert
        style={{ marginTop: 12 }}
        type={currentOverview.denied || currentOverview.stale ? "warning" : "info"}
        showIcon
        title={
          currentOverview.denied
            ? "当前无法查看这份概览"
            : currentOverview.stale
              ? "合同版本已更新，请刷新后再看"
              : currentOverview.state === "unavailable"
                ? "概览暂不可用"
                : "履约进度"
        }
        description={
          currentOverview.denied
            ? "请重新登录，或确认你仍有这份合同的权限。"
            : projection
              ? `本次读取时间：${projection.observed_at}。`
              : "正在读取履约进度。"
        }
      />

      <section aria-label="交付事实" style={{ marginTop: 20 }}>
        <h3>交付</h3>
        <Tag>
          {currentOverview.state === "loading"
            ? "读取中"
            : currentOverview.state === "unavailable"
              ? "暂不可用"
              : completenessLabel(
                  projection?.completeness.deliveries ?? "unknown",
                  deliveryRows.length === 0,
                )}
        </Tag>
        {projection?.completeness.deliveries === "known" && deliveryRows.length === 0 && (
          <p>还没有交付记录。</p>
        )}
        {deliveryRows.map((row) => (
          <p key={row.delivery_id}>
            {row.actual_at} · 修订 {row.contract_revision_id} · {row.item_name} · {row.quantity}{" "}
            {row.unit} · {row.location || "地点未提供"}
          </p>
        ))}
      </section>

      <section aria-label="验收事实" style={{ marginTop: 20 }}>
        <h3>验收</h3>
        <Tag>
          {currentOverview.state === "loading"
            ? "读取中"
            : currentOverview.state === "unavailable"
              ? "暂不可用"
              : completenessLabel(
                  projection?.completeness.acceptances ?? "unknown",
                  acceptanceRows.length === 0,
                )}
        </Tag>
        {projection?.completeness.acceptances === "known" && acceptanceRows.length === 0 && (
          <p>还没有验收记录。</p>
        )}
        {acceptanceRows.map((row) => (
          <p key={row.acceptance_id}>
            {row.actual_at} · 修订 {row.contract_revision_id} · {row.item_name} ·{" "}
            {resultTag(row.result)} · 通过 {row.accepted_quantity} / 失败 {row.failed_quantity}{" "}
            {row.unit}
          </p>
        ))}
      </section>

      <section aria-label="付款事实" style={{ marginTop: 20 }}>
        <h3>付款</h3>
        <Tag>
          {currentOverview.state === "loading"
            ? "读取中"
            : currentOverview.state === "unavailable"
              ? "暂不可用"
              : completenessLabel(
                  projection?.completeness.payments ?? "unknown",
                  projection?.payments !== null && paymentRows.length === 0,
                )}
        </Tag>
        {projection?.completeness.payments === "known" && projection.payments === null && (
          <p>暂时看不到付款记录。</p>
        )}
        {projection?.completeness.payments === "known" &&
          projection.payments !== null &&
          paymentRows.length === 0 && <p>还没有付款回执。</p>}
        {paymentRows.map((row) => (
          <p key={`${row.contract_revision_id}-${row.payment_sequence}`}>
            修订 {row.contract_revision_id} · 计划序号 {row.payment_sequence} · {paymentTag(row)}
          </p>
        ))}
      </section>

      <section aria-label="资产事实" style={{ marginTop: 20 }}>
        <h3>资产移交</h3>
        <Tag>
          {currentOverview.state === "loading"
            ? "读取中"
            : currentOverview.state === "unavailable"
              ? "暂不可用"
              : completenessLabel(
                  projection?.completeness.assets ?? "unknown",
                  assetPlans.length === 0,
                )}
        </Tag>
        {projection?.completeness.assets === "known" && (
          <p>
            冻结修订标记：
            {projection?.assets.fixed_asset_status === "true"
              ? "固定资产"
              : projection?.assets.fixed_asset_status === "not_applicable"
                ? "明确不适用"
                : "未知"}
          </p>
        )}
        {projection?.completeness.assets === "known" && assetPlans.length === 0 && (
          <p>还没有资产移交计划。</p>
        )}
        {assetPlans.flatMap((plan) =>
          plan.handovers.map((handover) => (
            <p key={handover.handover_id}>
              修订 {handover.contract_revision_id} · {plan.name ?? plan.item_id ?? "资产计划"} ·
              移交 {handover.quantity} {handover.unit} · ERP 状态 {handover.booking_status} ·
              部门核验 {handover.department_verification}
            </p>
          )),
        )}
      </section>

      <section aria-label="计划日期提示" style={{ marginTop: 20 }}>
        <h3>计划日期提示</h3>
        {!frozenPlanReadable && <p>计划日期尚未核实。</p>}
        {frozenPlanReadable &&
          (plans.delivery_plans === null ||
            plans.acceptance_plans === null ||
            plans.payments === null) && <p>部分计划信息尚未核实。</p>}
        {frozenPlanReadable &&
          reminders.length === 0 &&
          plans.delivery_plans !== null &&
          plans.acceptance_plans !== null &&
          plans.payments !== null && <p>还没有计划日期。</p>}
        {reminders.map((reminder) => (
          <p key={reminder.key}>
            {reminder.kind} #{reminder.sequence} · {reminder.date || "暂无计划日"} ·{" "}
            {reminder.date ? reminder.observed : "暂无计划日"}
          </p>
        ))}
        <p>这里只展示计划日期。</p>
      </section>

      <section aria-label="归档核对" style={{ marginTop: 20 }}>
        <h3>关闭前核对</h3>
        <p>
          争议状态：{completenessLabel(projection?.completeness.disputes ?? "unknown")}；质保义务：
          {completenessLabel(projection?.completeness.warranty ?? "unknown")}；其他未完成义务：
          {completenessLabel(projection?.completeness.other_obligations ?? "unknown")}。
        </p>
        <Tag color="orange">关闭前还需核对</Tag>
        <p>当前修订：{revisionLabel}</p>
      </section>
    </Card>
  );
}
