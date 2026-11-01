import { Alert, Button, Table, Tag } from "antd";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  checkContractPaymentEligibility,
  listContractPayments,
  type ContractDetail,
  type ContractPaymentEligibility,
  type ContractPaymentSummary,
  type DraftPaymentPlan,
} from "../api/contracts";
import { errorMessage } from "../api/http";
import { clearPendingMutationKey, idempotencyActorScope, pendingMutationKey } from "../api/idempotency";
import type { Actor } from "../api/types";

interface PaymentPlanRow {
  key: string;
  plan: DraftPaymentPlan;
  summary?: ContractPaymentSummary;
  check?: ContractPaymentEligibility;
}

const PAYMENT_STATES: Record<ContractPaymentSummary["state"], { label: string; color?: string }> = {
  unpaid: { label: "可信回执净额为零" },
  partial: { label: "账本记录部分付款", color: "orange" },
  paid: { label: "账本记录达到计划金额", color: "green" },
  overpaid: { label: "账本记录超过计划金额", color: "red" },
  unknown: { label: "付款状态未核验" },
};

const CHECK_STATES: Record<ContractPaymentEligibility["status"], { label: string; type: "info" | "warning" }> = {
  eligible: { label: "当前核验符合付款条件", type: "info" },
  blocked: { label: "当前核验未满足付款条件", type: "warning" },
  unknown: { label: "当前核验无法确定付款条件", type: "warning" },
};

function actorKey(actor: Actor | null): string {
  return actor === null
    ? "anonymous"
    : [actor.audience, actor.tenant_id, actor.org_id, actor.id, actor.role].join("\u0000");
}

function paymentStatus(
  summary: ContractPaymentSummary | undefined,
  readError: string | null,
  actorPresent: boolean,
  loading: boolean,
  paymentsLoaded: boolean,
): { label: string; color?: string } {
  if (readError) return { label: "付款状态未读取" };
  if (!actorPresent) return { label: "访问身份不可用，付款状态未读取" };
  if (loading || !paymentsLoaded) return { label: "付款状态正在读取" };
  if (!summary) return { label: "当前修订没有可信回执状态记录，付款情况未核验" };
  if (summary.receipts.length === 0)
    return { label: "尚无可信回执，付款状态未核验" };
  if (summary.state === "unknown")
    return { label: "已有可信回执，付款状态仍未核验" };
  return PAYMENT_STATES[summary.state];
}

function planAmounts(plan: DraftPaymentPlan, summary?: ContractPaymentSummary): string[] {
  const amounts = [
    summary?.planned_amount ? `计划金额 ${summary.planned_amount}` : null,
    plan.amount ? `付款金额 ${plan.amount}` : null,
    plan.advance_amount ? `预付款 ${plan.advance_amount}` : null,
    plan.unit_amount ? `单位支付 ${plan.unit_amount}` : null,
    plan.fiscal_amount ? `财政支付 ${plan.fiscal_amount}` : null,
  ].filter((value): value is string => value !== null);
  return amounts.length ? amounts : ["计划金额未提供"];
}

export function ContractPaymentsPanel({
  detail,
  actor,
  canCheck,
  onRefresh,
}: {
  detail: ContractDetail;
  actor: Actor | null;
  canCheck: boolean;
  onRefresh: () => Promise<void>;
}) {
  const contractId = detail.summary.contract_id;
  const revisionId = detail.current_revision_id;
  const rowVersion = detail.summary.row_version;
  const scopeKey = actorKey(actor);
  const active = detail.summary.contract_state === "registered" || detail.summary.contract_state === "performing";
  const plans = detail.draft?.payments ?? [];
  const [payments, setPayments] = useState<ContractPaymentSummary[] | null>(null);
  const [checks, setChecks] = useState<Record<number, ContractPaymentEligibility>>({});
  const [loading, setLoading] = useState(false);
  const [checkingSequence, setCheckingSequence] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [refreshWarning, setRefreshWarning] = useState<string | null>(null);
  const requestGeneration = useRef(0);
  const currentIdentity = useRef({ contractId, revisionId, scopeKey });
  currentIdentity.current = { contractId, revisionId, scopeKey };

  function isCurrent(generation: number): boolean {
    const current = currentIdentity.current;
    return generation === requestGeneration.current &&
      current.contractId === contractId &&
      current.revisionId === revisionId &&
      current.scopeKey === scopeKey;
  }

  useEffect(() => {
    const generation = ++requestGeneration.current;
    const controller = new AbortController();
    setPayments(null);
    setError(null);
    setRefreshWarning(null);
    setLoading(false);
    setCheckingSequence(null);
    setChecks((current) => Object.fromEntries(
      Object.entries(current).filter(([, result]) =>
        result.contract_id === contractId &&
        result.contract_revision_id === revisionId &&
        result.contract_row_version === rowVersion,
      ),
    ));
    if (!actor || !revisionId || !active || plans.length === 0) {
      return () => {
        controller.abort();
        requestGeneration.current++;
      };
    }

    setLoading(true);
    void listContractPayments(contractId, controller.signal).then((result) => {
      if (isCurrent(generation)) setPayments(result);
    }).catch((caught: unknown) => {
      if (!isCurrent(generation)) return;
      setPayments(null);
      setError(errorMessage(caught));
    }).finally(() => {
      if (isCurrent(generation)) setLoading(false);
    });
    return () => {
      controller.abort();
      requestGeneration.current++;
    };
  }, [contractId, revisionId, rowVersion, scopeKey, active, plans.length]);

  const summaries = useMemo(() => {
    const current = new Map<number, ContractPaymentSummary>();
    for (const payment of payments ?? []) {
      if (payment.contract_id === contractId && payment.contract_revision_id === revisionId)
        current.set(payment.payment_sequence, payment);
    }
    return current;
  }, [payments, contractId, revisionId]);

  const rows: PaymentPlanRow[] = plans.map((plan) => {
    const summary = summaries.get(plan.sequence);
    return {
      key: `${revisionId ?? "unknown"}:${plan.sequence}`,
      plan,
      summary,
      check: checks[plan.sequence]?.contract_id === contractId &&
        checks[plan.sequence]?.contract_revision_id === revisionId &&
        checks[plan.sequence]?.contract_row_version === rowVersion
        ? checks[plan.sequence]
        : undefined,
    };
  });

  async function checkEligibility(sequence: number) {
    if (!actor || !revisionId || !canCheck || checkingSequence !== null) return;
    const generation = requestGeneration.current;
    const input = {
      contract_revision_id: revisionId,
      payment_sequence: sequence,
      expected_row_version: rowVersion,
    };
    setCheckingSequence(sequence);
    setError(null);
    setRefreshWarning(null);
    let mutation: Awaited<ReturnType<typeof pendingMutationKey>> | null = null;
    try {
      mutation = await pendingMutationKey(
        "contract-payment-eligibility-check",
        idempotencyActorScope(actor),
        `${contractId}:${revisionId}:${sequence}`,
        input,
      );
      if (!isCurrent(generation)) {
        clearPendingMutationKey(mutation.storageKey);
        return;
      }
      const result = await checkContractPaymentEligibility(contractId, input, mutation.key);
      if (
        result.contract_id !== contractId ||
        result.contract_revision_id !== revisionId ||
        result.payment_sequence !== sequence ||
        result.initiates_payment !== false
      ) throw new Error("付款资格核验响应与当前合同修订不匹配");
      clearPendingMutationKey(mutation.storageKey);
      if (!isCurrent(generation)) return;
      setChecks((current) => ({ ...current, [sequence]: result }));
      try {
        await onRefresh();
      } catch (caught) {
        if (isCurrent(generation)) {
          setPayments(null);
          setChecks({});
          setRefreshWarning(`资格核验已记录，但合同版本刷新失败：${errorMessage(caught)}`);
        }
        return;
      }
    } catch (caught) {
      if (isCurrent(generation)) {
        setPayments(null);
        setChecks({});
        setError(errorMessage(caught));
      }
    } finally {
      if (isCurrent(generation)) setCheckingSequence(null);
    }
  }

  if (!active) return null;
  const columns = [
    {
      title: "期次",
      dataIndex: ["plan", "sequence"],
      key: "sequence",
      width: 76,
      render: (_value: number, row: PaymentPlanRow) => `第 ${row.plan.sequence} 期`,
    },
    {
      title: "冻结计划",
      key: "plan",
      render: (_value: unknown, row: PaymentPlanRow) => <div>
        <div>{row.plan.description?.trim() || "未填写付款说明"}</div>
        <div className="meta">计划日期：{row.plan.planned_date || "未提供"}</div>
        <div className="meta">{planAmounts(row.plan, row.summary).join("；")}{row.summary?.currency ? ` ${row.summary.currency}` : ""}</div>
      </div>,
    },
    {
      title: "可信回执状态",
      key: "payment_state",
      render: (_value: unknown, row: PaymentPlanRow) => {
        const status = paymentStatus(row.summary, error, actor !== null, loading, payments !== null);
        return <div>
          <Tag color={status.color}>{status.label}</Tag>
          {row.summary && row.summary.receipts.length > 0
            ? <div className="meta">已登记可信净额：{row.summary.net_paid} {row.summary.currency ?? "币种未知"}（{row.summary.receipts.length} 条回执）</div>
            : null}
        </div>;
      },
    },
    {
      title: "资格核验",
      key: "eligibility",
      render: (_value: unknown, row: PaymentPlanRow) => <div>
        <Button
          size="small"
          loading={checkingSequence === row.plan.sequence}
          disabled={!actor || !revisionId || !canCheck || checkingSequence !== null || loading}
          onClick={() => void checkEligibility(row.plan.sequence)}
        >
          核验付款条件
        </Button>
        {row.check ? <div className="meta">
          <Tag color={row.check.status === "eligible" ? "blue" : row.check.status === "blocked" ? "orange" : undefined}>
            {CHECK_STATES[row.check.status].label}
          </Tag>
        </div> : null}
      </div>,
    },
  ];

  return <section className="contracts-page-section" aria-label="冻结付款计划与资格核验">
    <h3>付款计划与资格核验</h3>
    <Alert type="info" showIcon title="计划取自当前冻结合同修订。资格核验只检查当前条件，不发起付款；有无本地可信回执也不能单独证明现实中是否已付款。" />
    {loading ? <p role="status">正在读取本修订的可信付款回执…</p> : null}
    {error ? <Alert type="error" showIcon title={error} /> : null}
    {refreshWarning ? <Alert type="warning" showIcon title={refreshWarning} /> : null}
    {!revisionId ? <Alert type="warning" showIcon title="当前没有可核验的冻结合同修订。" /> : null}
    {revisionId && plans.length === 0 ? <p className="meta">当前冻结修订没有付款计划。</p> : null}
    {plans.length > 0 ? <Table<PaymentPlanRow>
      rowKey="key"
      columns={columns}
      dataSource={rows}
      loading={loading}
      pagination={false}
      locale={{ emptyText: "当前修订没有可显示的付款计划" }}
      size="small"
    /> : null}
    {rows.some((row) => row.check) ? rows.filter((row) => row.check).map((row) => {
      const result = row.check!;
      const state = CHECK_STATES[result.status];
      return <Alert
        key={`${result.check_id}:${result.contract_row_version}`}
        type={state.type}
        showIcon
        title={`第 ${result.payment_sequence} 期：${state.label}`}
        description={<div>
          <p>核验修订：{result.contract_revision_id}；合同版本：{result.contract_row_version}；时间：{result.checked_at}</p>
          {result.reasons.map((reason, index) => <p key={`reason-${index}`}>{reason}</p>)}
          {result.evidence.map((evidence, index) => <p key={`evidence-${index}`}>依据：{evidence}</p>)}
          <p>本次核验不会发起付款，符合条件也不代表款项已支付。</p>
        </div>}
      />;
    }) : null}
  </section>;
}
