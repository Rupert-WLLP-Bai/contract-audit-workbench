import { Alert, Button, Card, Tag } from "antd";
import { useEffect, useRef, useState } from "react";
import {
  listContractAcceptances,
  listContractAssets,
  listContractDeliveries,
  listContractPayments,
  type ContractAcceptanceList,
  type ContractAssetList,
  type ContractDeliveryList,
  type ContractDetail,
  type ContractPaymentSummary,
} from "../api/contracts";
import { ApiError } from "../api/http";
import type { Actor } from "../api/types";

type SectionResult<T> = { state: "loading" | "ready" | "unavailable"; value?: T };
type OverviewState = {
  deliveries: SectionResult<ContractDeliveryList>;
  acceptances: SectionResult<ContractAcceptanceList>;
  payments: SectionResult<ContractPaymentSummary[]>;
  assets: SectionResult<ContractAssetList>;
  stale: boolean;
  denied: boolean;
};

const loadingState = (): OverviewState => ({
  deliveries: { state: "loading" },
  acceptances: { state: "loading" },
  payments: { state: "loading" },
  assets: { state: "loading" },
  stale: false,
  denied: false,
});

function unavailableState(denied = false): OverviewState {
  return {
    deliveries: { state: "unavailable" },
    acceptances: { state: "unavailable" },
    payments: { state: "unavailable" },
    assets: { state: "unavailable" },
    stale: false,
    denied,
  };
}

function isAccessDenied(error: unknown): boolean {
  return error instanceof ApiError && [401, 403, 404].includes(error.status);
}

function actorScope(actor: Actor | null): string {
  return actor === null
    ? "anonymous"
    : [actor.audience, actor.tenant_id, actor.org_id, actor.id, actor.role].join("\u0000");
}

function sectionLabel(state: SectionResult<unknown>["state"]): string {
  if (state === "loading") return "读取中";
  if (state === "unavailable") return "不可用或未授权";
  return "已读取；不代表共同快照";
}

function resultTag(result: "passed" | "partial" | "failed") {
  if (result === "passed") return <Tag color="green">通过</Tag>;
  if (result === "partial") return <Tag color="orange">部分通过</Tag>;
  return <Tag color="red">未通过</Tag>;
}

function paymentTag(summary: ContractPaymentSummary) {
  if (summary.receipts.length === 0 || summary.state === "unknown")
    return <Tag>付款状态未知</Tag>;
  if (summary.state === "paid") return <Tag color="green">账本记录已达到计划金额</Tag>;
  if (summary.state === "partial") return <Tag color="orange">账本记录部分付款</Tag>;
  if (summary.state === "overpaid") return <Tag color="red">账本记录超过计划金额</Tag>;
  return <Tag>可信回执净额为零</Tag>;
}

/**
 * Read-only lifecycle overview. Each source is authorized and read separately;
 * this component never treats the resulting sections as one atomic snapshot.
 */
export function ContractLifecycleOverviewPanel({ detail, actor, onAccessDenied }: {
  detail: ContractDetail;
  actor: Actor | null;
  onAccessDenied?: () => void;
}) {
  const contractId = detail.summary.contract_id;
  const revisionId = detail.current_revision_id;
  const rowVersion = detail.summary.row_version;
  const identity = actorScope(actor);
  const validIdentity = actor !== null && Boolean(actor.id.trim() && actor.tenant_id.trim() &&
    actor.org_id.trim() && (actor.audience === "client" || actor.audience === "admin"));
  const [reloadNonce, setReloadNonce] = useState(0);
  const onAccessDeniedRef = useRef(onAccessDenied);
  onAccessDeniedRef.current = onAccessDenied;
  const requestGeneration = useRef(0);
  const identityRef = useRef({ contractId, revisionId, rowVersion, identity });
  identityRef.current = { contractId, revisionId, rowVersion, identity };
  const requestKey = JSON.stringify([contractId, revisionId, rowVersion, identity, reloadNonce]);
  const [overview, setOverview] = useState<{ key: string; data: OverviewState }>({ key: "", data: loadingState() });
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
      return generation === requestGeneration.current && current.contractId === contractId &&
        current.revisionId === revisionId && current.rowVersion === rowVersion && current.identity === identity;
    };

    let denialHandled = false;
    const failClosed = <T,>(request: Promise<T>): Promise<T> => request.catch((error: unknown) => {
      if (!denialHandled && isCurrent() && isAccessDenied(error)) {
        denialHandled = true;
        controller.abort();
        setOverview({ key: requestKey, data: unavailableState(true) });
        onAccessDeniedRef.current?.();
      }
      throw error;
    });

    void Promise.allSettled([
      failClosed(listContractDeliveries(contractId, controller.signal)),
      failClosed(listContractAcceptances(contractId, controller.signal)),
      failClosed(listContractPayments(contractId, controller.signal)),
      failClosed(listContractAssets(contractId, controller.signal)),
    ]).then(([deliveryResult, acceptanceResult, paymentResult, assetResult]) => {
      if (!isCurrent() || denialHandled) return;

      let stale = false;
      const delivery: SectionResult<ContractDeliveryList> = deliveryResult.status === "fulfilled"
        ? deliveryResult.value.row_version === rowVersion && deliveryResult.value.items.every((entry) => entry.contract_id === contractId)
          ? { state: "ready", value: deliveryResult.value }
          : (stale = true, { state: "unavailable" })
        : { state: "unavailable" };
      const acceptance: SectionResult<ContractAcceptanceList> = acceptanceResult.status === "fulfilled"
        ? acceptanceResult.value.row_version === rowVersion && acceptanceResult.value.items.every((entry) => entry.contract_id === contractId)
          ? { state: "ready", value: acceptanceResult.value }
          : (stale = true, { state: "unavailable" })
        : { state: "unavailable" };
      const payments: SectionResult<ContractPaymentSummary[]> = paymentResult.status === "fulfilled"
        ? paymentResult.value.every((entry) => entry.contract_id === contractId)
          ? { state: "ready", value: paymentResult.value }
          : (stale = true, { state: "unavailable" })
        : { state: "unavailable" };
      const assets: SectionResult<ContractAssetList> = assetResult.status === "fulfilled"
        ? assetResult.value.contract_id === contractId && assetResult.value.row_version === rowVersion
          ? { state: "ready", value: assetResult.value }
          : (stale = true, { state: "unavailable" })
        : { state: "unavailable" };

      setOverview({ key: requestKey, data: { deliveries: delivery, acceptances: acceptance, payments, assets, stale, denied: false } });
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
      <Card title="履约生命周期概览">
        <Alert
          type="warning"
          showIcon
          title="概览不可用，已清除合同相关显示"
          description="当前身份无效或案件访问被拒绝。请重新确认登录和授权；父页面应清除合同详情及相关缓存。"
        />
      </Card>
    );
  }

  const deliveryRows = currentOverview.deliveries.value?.items ?? [];
  const acceptanceRows = currentOverview.acceptances.value?.items ?? [];
  const paymentRows = currentOverview.payments.value ?? [];
  const assetPlans = currentOverview.assets.value?.items ?? [];
  const revisionLabel = revisionId ?? "当前合同尚无冻结修订";

  const frozenPlanReadable = detail.draft !== undefined && revisionId !== null &&
    detail.summary.contract_state !== "draft" && detail.summary.authority_mode === "native";
  const reminders = frozenPlanReadable ? [
    ...(detail.draft?.delivery_plans ?? []).map((plan) => ({
      key: `delivery-${plan.sequence}`,
      kind: "交付计划",
      sequence: plan.sequence,
      date: plan.planned_date,
      observed: currentOverview.deliveries.state === "ready"
        ? deliveryRows.some((row) => row.contract_revision_id === revisionId && row.delivery_plan_sequence === plan.sequence)
          ? "已有对应交付记录"
          : "本次读取未见对应交付记录"
        : "事实未读取，无法判断",
    })),
    ...(detail.draft?.acceptance_plans ?? []).map((plan) => ({
      key: `acceptance-${plan.sequence}`,
      kind: "验收计划",
      sequence: plan.sequence,
      date: plan.planned_date,
      observed: currentOverview.acceptances.state === "ready"
        ? acceptanceRows.some((row) => row.contract_revision_id === revisionId && row.acceptance_plan_sequence === plan.sequence)
          ? "已有对应验收记录"
          : "本次读取未见对应验收记录"
        : "事实未读取，无法判断",
    })),
    ...(detail.draft?.payments ?? []).map((plan) => ({
      key: `payment-${plan.sequence}`,
      kind: "付款计划",
      sequence: plan.sequence,
      date: plan.planned_date,
      observed: currentOverview.payments.state === "ready"
        ? paymentRows.some((row) => row.contract_revision_id === revisionId && row.payment_sequence === plan.sequence && row.receipts.length > 0)
          ? "已有可信回执记录"
          : "本次读取未见可信回执"
        : "事实未读取，无法判断",
    })),
  ] : [];

  return (
    <Card title="履约生命周期概览" extra={<Button onClick={() => setReloadNonce((value) => value + 1)}>重新读取事实</Button>}>
      <Alert
        type="info"
        showIcon
        title="只读提示，不构成合同关闭结论"
        description="争议、质保及其他未完成义务没有本页面可用的可信结构化来源，状态均为未知并阻止确认可关闭。此处不推断无争议、不判断逾期，也不会自动关闭合同。"
      />
      <Alert
        style={{ marginTop: 12 }}
        type={currentOverview.denied || currentOverview.stale ? "warning" : "info"}
        showIcon
        title={currentOverview.denied ? "身份或案件访问被拒绝，已清除此合同的全部概览数据" : currentOverview.stale ? "合同版本已变化，请刷新合同详情后重新读取" : "各事实分组分别授权和读取"}
        description={currentOverview.denied
          ? "请重新确认登录身份和案件授权。父页面应清除合同详情及相关缓存；本面板不会保留其他分组的旧数据。"
          : "交付、验收、付款和资产数据来自独立请求；客户端接收时间不是数据库 as-of 时间。行版本相同也不证明它们属于同一事务快照。"}
      />

      <section aria-label="交付事实" style={{ marginTop: 20 }}>
        <h3>交付</h3>
        <Tag>{sectionLabel(currentOverview.deliveries.state)}</Tag>
        {currentOverview.deliveries.state === "ready" && deliveryRows.length === 0 && <p>暂无可验证交付记录；空列表不能证明交付完成。</p>}
        {deliveryRows.map((row) => (
          <p key={row.delivery_id}>
            {row.actual_at} · 修订 {row.contract_revision_id} · {row.item_name} · {row.quantity} {row.unit} · {row.location || "地点未提供"}
          </p>
        ))}
      </section>

      <section aria-label="验收事实" style={{ marginTop: 20 }}>
        <h3>验收</h3>
        <Tag>{sectionLabel(currentOverview.acceptances.state)}</Tag>
        {currentOverview.acceptances.state === "ready" && acceptanceRows.length === 0 && <p>暂无可验证验收记录；空列表不能证明验收完成。</p>}
        {acceptanceRows.map((row) => (
          <p key={row.acceptance_id}>
            {row.actual_at} · 修订 {row.contract_revision_id} · {row.item_name} · {resultTag(row.result)} · 通过 {row.accepted_quantity} / 失败 {row.failed_quantity} {row.unit}
          </p>
        ))}
      </section>

      <section aria-label="付款事实" style={{ marginTop: 20 }}>
        <h3>付款</h3>
        <Tag>{sectionLabel(currentOverview.payments.state)}</Tag>
        {currentOverview.payments.state === "ready" && paymentRows.length === 0 && <p>暂无可信付款回执或计划摘要；付款情况未知。</p>}
        {paymentRows.map((row) => (
          <p key={`${row.contract_revision_id}-${row.payment_sequence}`}>
            修订 {row.contract_revision_id} · 计划序号 {row.payment_sequence} · {paymentTag(row)}
          </p>
        ))}
      </section>

      <section aria-label="资产事实" style={{ marginTop: 20 }}>
        <h3>资产移交</h3>
        <Tag>{sectionLabel(currentOverview.assets.state)}</Tag>
        {currentOverview.assets.state === "ready" && (
          <p>
            冻结修订标记：{currentOverview.assets.value?.fixed_asset_status === "true" ? "固定资产" :
              currentOverview.assets.value?.fixed_asset_status === "not_applicable" ? "明确不适用" : "未知"}
          </p>
        )}
        {currentOverview.assets.state === "ready" && assetPlans.length === 0 && <p>暂无资产计划记录；空列表不能证明移交完成。</p>}
        {assetPlans.flatMap((plan) => plan.handovers.map((handover) => (
          <p key={handover.handover_id}>
            修订 {handover.contract_revision_id} · {plan.name ?? plan.item_id ?? "资产计划"} · 移交 {handover.quantity} {handover.unit} · ERP 状态 {handover.booking_status} · 部门核验 {handover.department_verification}
          </p>
        )))}
      </section>

      <section aria-label="计划日期提示" style={{ marginTop: 20 }}>
        <h3>计划日期提示</h3>
        {!frozenPlanReadable && <p>当前详情不是可确认的冻结计划视图（可能是可编辑草稿或外部合同），计划日期及对应提醒依据未知。</p>}
        {frozenPlanReadable && reminders.length === 0 && <p>当前冻结修订没有提供可用的计划日期。</p>}
        {reminders.map((reminder) => (
          <p key={reminder.key}>
            {reminder.kind} #{reminder.sequence} · {reminder.date || "无可用提醒依据"} · {reminder.date ? reminder.observed : "未提供计划日，不推算逾期"}
          </p>
        ))}
        <p>计划日期仅供查看；本页不发送提醒，也不据此认定逾期。</p>
      </section>

      <section aria-label="归档核对" style={{ marginTop: 20 }}>
        <h3>关闭前核对</h3>
        <p>争议状态：未知；质保义务：未知；其他未完成义务：未知。</p>
        <Tag color="orange">无法确认可关闭</Tag>
        <p>依据仅限当前可读取的分组事实（修订 {revisionLabel}）。这些独立读取不构成整体归档或法律结论。</p>
      </section>
    </Card>
  );
}
