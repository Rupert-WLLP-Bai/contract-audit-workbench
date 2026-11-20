// File metadata (manifest mirror):
// Purpose: Checks frozen payment plan rendering, trusted receipt states, read and identity boundaries, and that payment eligibility checks use idempotent server-verified results without initiating payment.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import {
  checkContractPaymentEligibility,
  listContractPayments,
  type ContractDetail,
  type ContractPaymentEligibility,
  type ContractPaymentSummary,
} from "../api/contracts";
import { ApiError } from "../api/http";
import {
  clearPendingMutationKey,
  idempotencyActorScope,
  pendingMutationKey,
} from "../api/idempotency";
import type { Actor } from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { ContractPaymentsPanel } from "./contract-payments-panel";

vi.mock("../api/contracts", async (original) => ({
  ...(await original<typeof import("../api/contracts")>()),
  listContractPayments: vi.fn(),
  checkContractPaymentEligibility: vi.fn(),
}));

vi.mock("../api/idempotency", () => ({
  idempotencyActorScope: vi.fn(() => ({
    actorId: "clerk-1",
    tenantId: "tenant-1",
    orgId: "org-1",
    audience: "client",
  })),
  pendingMutationKey: vi.fn(),
  clearPendingMutationKey: vi.fn(),
}));

const actor: Actor = {
  id: "clerk-1",
  display_name: "经办人",
  role: "clerk",
  tenant_id: "tenant-1",
  org_id: "org-1",
  audience: "client",
  principal_id: null,
  csrf_token: "synthetic-csrf",
  context_switch_enabled: false,
  active_context: {
    context_version: "1",
    membership_id: null,
    tenant_id: "tenant-1",
    tenant_name: "本地租户",
    org_id: "org-1",
    org_name: "本地组织",
  },
};

const onRefresh = vi.fn(async () => {});

beforeEach(() => {
  vi.mocked(listContractPayments).mockReset();
  vi.mocked(checkContractPaymentEligibility).mockReset();
  vi.mocked(pendingMutationKey)
    .mockReset()
    .mockResolvedValue({ storageKey: "storage-1", key: "key-1" });
  vi.mocked(clearPendingMutationKey).mockReset();
  vi.mocked(idempotencyActorScope).mockClear();
  onRefresh.mockReset().mockResolvedValue(undefined);
});

function receipt(sequence: number, amount: string): ContractPaymentSummary["receipts"][number] {
  return {
    amount,
    contract_id: "contract-1",
    contract_revision_id: "revision-1",
    currency: "CNY",
    event_type: "payment",
    occurred_at: "2026-11-01T00:00:00Z",
    payload_digest: "a".repeat(64),
    payment_sequence: sequence,
    receipt_id: `receipt-${sequence}-${amount}`,
    recorded_at: "2026-11-02T00:00:00Z",
    source_event_id: `event-${sequence}-${amount}`,
    source_id: "bank",
    source_key_id: `key-${sequence}-${amount}`,
  };
}

function paymentSummary(
  overrides: Partial<ContractPaymentSummary> & { payment_sequence: number },
): ContractPaymentSummary {
  return {
    contract_id: "contract-1",
    contract_revision_id: "revision-1",
    currency: "CNY",
    net_paid: "0.00",
    planned_amount: "100.00",
    receipts: [],
    state: "unpaid",
    ...overrides,
  };
}

function eligibilityResult(
  overrides: Partial<ContractPaymentEligibility> = {},
): ContractPaymentEligibility {
  return {
    check_id: "check-1",
    checked_at: "2026-11-15T10:00:00Z",
    contract_id: "contract-1",
    contract_revision_id: "revision-1",
    contract_row_version: 3,
    evidence: ["冻结修订交付与验收事实"],
    initiates_payment: false,
    payment_sequence: 1,
    reasons: ["已到付款期且验收合格"],
    status: "eligible",
    ...overrides,
  };
}

function detail(overrides: Partial<ContractDetail> = {}): ContractDetail {
  return {
    summary: {
      contract_id: "contract-1",
      authority_mode: "native",
      name: "付款合同",
      number: "HT-1",
      amount: null,
      currency: null,
      contract_state: "performing",
      workflow_state: "completed",
      row_version: 3,
      created_at: "2026-11-01T00:00:00Z",
    },
    current_revision_id: "revision-1",
    active_round_id: null,
    allowed_commands: [],
    warnings: [],
    draft: {
      contract: {},
      payments: [
        {
          sequence: 1,
          description: "首付款",
          planned_date: "2026-12-01",
          amount: "40.00",
          advance_amount: "10.00",
          unit_amount: "5.00",
          fiscal_amount: "3.00",
        },
        { sequence: 2 },
      ],
    },
    ...overrides,
  };
}

/** Resolve the verification button inside the row for one frozen payment sequence. */
function rowButton(sequence: number): HTMLElement {
  const row = screen.getByText(`第 ${sequence} 期`).closest("tr");
  if (!row) throw new Error(`payment row ${sequence} was not rendered`);
  return within(row as HTMLElement).getByRole("button");
}

async function clickCheck(sequence: number) {
  await waitFor(() => expect(rowButton(sequence)).toBeEnabled());
  await userEvent.setup().click(rowButton(sequence));
}

it("renders frozen plans with trusted receipt states for the current revision", async () => {
  vi.mocked(listContractPayments).mockResolvedValue([
    paymentSummary({
      payment_sequence: 1,
      state: "partial",
      net_paid: "60.00",
      receipts: [receipt(1, "50.00"), receipt(1, "10.00")],
    }),
    paymentSummary({
      payment_sequence: 2,
      state: "paid",
      net_paid: "100.00",
      receipts: [receipt(2, "100.00")],
    }),
    paymentSummary({
      payment_sequence: 1,
      contract_id: "contract-2",
      state: "overpaid",
      net_paid: "999.00",
      receipts: [receipt(1, "999.00")],
    }),
  ]);
  render(<ContractPaymentsPanel detail={detail()} actor={actor} canCheck onRefresh={onRefresh} />);

  expect(await screen.findByText("已登记可信净额：60.00 CNY（2 条回执）")).toBeVisible();
  expect(screen.getByText("账本记录部分付款")).toBeVisible();
  expect(screen.getByText("账本记录达到计划金额")).toBeVisible();
  expect(
    screen.getByText(
      "计划金额 100.00；付款金额 40.00；预付款 10.00；单位支付 5.00；财政支付 3.00 CNY",
    ),
  ).toBeVisible();
  expect(screen.getByText("未填写付款说明")).toBeVisible();
  expect(screen.getByText("计划日期：未提供")).toBeVisible();
  expect(screen.getByText("计划金额 100.00 CNY")).toBeVisible();
  expect(screen.queryByText(/999.00/)).not.toBeInTheDocument();
  expect(screen.queryByText("账本记录超过计划金额")).not.toBeInTheDocument();
});

it("distinguishes unverified, missing and empty receipt states", async () => {
  vi.mocked(listContractPayments).mockResolvedValue([
    paymentSummary({
      payment_sequence: 1,
      state: "unknown",
      receipts: [receipt(1, "0.00")],
      currency: undefined,
    }),
    paymentSummary({ payment_sequence: 2, receipts: [], state: "unpaid" }),
    paymentSummary({
      payment_sequence: 3,
      state: "unpaid",
      receipts: [receipt(3, "0.00")],
    }),
  ]);
  render(
    <ContractPaymentsPanel
      detail={detail({
        draft: {
          contract: {},
          payments: [{ sequence: 1 }, { sequence: 2 }, { sequence: 3 }, { sequence: 4 }],
        },
      })}
      actor={actor}
      canCheck
      onRefresh={onRefresh}
    />,
  );

  expect(await screen.findByText("已有可信回执，付款状态仍未核验")).toBeVisible();
  expect(screen.getByText("尚无可信回执，付款状态未核验")).toBeVisible();
  expect(screen.getByText("可信回执净额为零")).toBeVisible();
  expect(screen.getByText("当前修订没有可信回执状态记录，付款情况未核验")).toBeVisible();
  expect(screen.getByText("已登记可信净额：0.00 币种未知（1 条回执）")).toBeVisible();
  expect(screen.getAllByText(/已登记可信净额/)).toHaveLength(2);
  expect(screen.getByText("计划金额未提供")).toBeVisible();
});

it("does not render for a non-performing contract and never reads receipts without a request", async () => {
  const view = render(
    <ContractPaymentsPanel
      detail={detail({ summary: { ...detail().summary, contract_state: "draft" } })}
      actor={actor}
      canCheck
      onRefresh={onRefresh}
    />,
  );
  expect(screen.queryByRole("heading", { name: "付款计划与资格核验" })).not.toBeInTheDocument();
  await act(async () => {});
  expect(listContractPayments).not.toHaveBeenCalled();

  view.rerender(
    <ContractPaymentsPanel detail={detail()} actor={null} canCheck onRefresh={onRefresh} />,
  );
  expect(screen.getByRole("heading", { name: "付款计划与资格核验" })).toBeVisible();
  expect(screen.getAllByText("访问身份不可用，付款状态未读取")).toHaveLength(2);
  expect(rowButton(1)).toBeDisabled();
  await act(async () => {});
  expect(listContractPayments).not.toHaveBeenCalled();
});

it("keeps plan rows and eligibility buttons bounded when the revision or permission is missing", async () => {
  vi.mocked(listContractPayments).mockResolvedValue([]);
  const view = render(
    <ContractPaymentsPanel
      detail={detail({ current_revision_id: null })}
      actor={actor}
      canCheck
      onRefresh={onRefresh}
    />,
  );
  expect(screen.getByText("暂无可核验的合同定稿。")).toBeVisible();
  expect(screen.getAllByText("付款状态正在读取")).toHaveLength(2);
  expect(rowButton(1)).toBeDisabled();
  await act(async () => {});
  expect(listContractPayments).not.toHaveBeenCalled();

  view.rerender(
    <ContractPaymentsPanel
      detail={detail()}
      actor={actor}
      canCheck={false}
      onRefresh={onRefresh}
    />,
  );
  expect(await screen.findAllByText("当前修订没有可信回执状态记录，付款情况未核验")).toHaveLength(
    2,
  );
  expect(rowButton(1)).toBeDisabled();

  view.rerender(
    <ContractPaymentsPanel detail={detail()} actor={actor} canCheck onRefresh={onRefresh} />,
  );
  expect(rowButton(1)).toBeEnabled();
});

it("shows the loading state, allows an empty payment plan, and surfaces read failures", async () => {
  let resolvePayments: (value: ContractPaymentSummary[]) => void = () => {};
  vi.mocked(listContractPayments).mockReturnValue(
    new Promise((resolve) => {
      resolvePayments = resolve;
    }),
  );
  const view = render(
    <ContractPaymentsPanel detail={detail()} actor={actor} canCheck onRefresh={onRefresh} />,
  );
  expect(screen.getByText("正在读取本修订的可信付款回执…")).toBeVisible();
  expect(screen.getAllByText("付款状态正在读取")).toHaveLength(2);
  await act(async () => {
    resolvePayments([]);
  });
  await waitFor(() =>
    expect(screen.queryByText("正在读取本修订的可信付款回执…")).not.toBeInTheDocument(),
  );
  expect(screen.getAllByText("当前修订没有可信回执状态记录，付款情况未核验")).toHaveLength(2);

  vi.mocked(listContractPayments).mockRejectedValue(
    new ApiError("付款回执读取失败", 500, "internal", false, null),
  );
  view.rerender(
    <ContractPaymentsPanel
      detail={detail({ current_revision_id: "revision-next" })}
      actor={actor}
      canCheck
      onRefresh={onRefresh}
    />,
  );
  expect(await screen.findByText("付款回执读取失败")).toBeVisible();
  expect(screen.getAllByText("付款状态未读取")).toHaveLength(2);

  view.rerender(
    <ContractPaymentsPanel
      detail={detail({
        current_revision_id: "revision-empty",
        draft: { contract: {}, payments: [] },
      })}
      actor={actor}
      canCheck
      onRefresh={onRefresh}
    />,
  );
  expect(screen.getByText("合同定稿中暂无付款计划。")).toBeVisible();
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
  expect(screen.queryByText("付款回执读取失败")).not.toBeInTheDocument();
});

it("records an eligible verification without initiating payment and refreshes the contract", async () => {
  vi.mocked(listContractPayments).mockResolvedValue([
    paymentSummary({ payment_sequence: 1, state: "partial", net_paid: "10.00", receipts: [] }),
  ]);
  vi.mocked(checkContractPaymentEligibility).mockResolvedValue(eligibilityResult());
  render(<ContractPaymentsPanel detail={detail()} actor={actor} canCheck onRefresh={onRefresh} />);
  await clickCheck(1);

  expect(await screen.findByText("第 1 期：当前核验符合付款条件")).toBeVisible();
  expect(screen.getByText("当前核验符合付款条件")).toBeVisible();
  expect(
    screen.getByText(/核验修订：revision-1；合同版本：3；时间：2026-11-15T10:00:00Z/),
  ).toBeVisible();
  expect(screen.getByText("已到付款期且验收合格")).toBeVisible();
  expect(screen.getByText("依据：冻结修订交付与验收事实")).toBeVisible();
  expect(screen.getByText("本次核验不会发起付款，符合条件后仍需查看付款回执。")).toBeVisible();
  expect(pendingMutationKey).toHaveBeenCalledWith(
    "contract-payment-eligibility-check",
    {
      actorId: "clerk-1",
      tenantId: "tenant-1",
      orgId: "org-1",
      audience: "client",
    },
    "contract-1:revision-1:1",
    { contract_revision_id: "revision-1", payment_sequence: 1, expected_row_version: 3 },
  );
  expect(checkContractPaymentEligibility).toHaveBeenCalledWith(
    "contract-1",
    { contract_revision_id: "revision-1", payment_sequence: 1, expected_row_version: 3 },
    "key-1",
  );
  expect(clearPendingMutationKey).toHaveBeenCalledWith("storage-1");
  expect(onRefresh).toHaveBeenCalledTimes(1);
});

it("reports blocked and unknown verification outcomes distinctly", async () => {
  vi.mocked(listContractPayments).mockResolvedValue([]);
  vi.mocked(checkContractPaymentEligibility)
    .mockResolvedValueOnce(eligibilityResult({ status: "blocked", reasons: ["验收尚未完成"] }))
    .mockResolvedValueOnce(
      eligibilityResult({
        check_id: "check-2",
        payment_sequence: 2,
        status: "unknown",
        reasons: [],
        evidence: [],
      }),
    );
  render(
    <ContractPaymentsPanel
      detail={detail({ draft: { contract: {}, payments: [{ sequence: 1 }, { sequence: 2 }] } })}
      actor={actor}
      canCheck
      onRefresh={onRefresh}
    />,
  );
  await clickCheck(1);
  expect(await screen.findByText("第 1 期：当前核验未满足付款条件")).toBeVisible();
  expect(screen.getByText("验收尚未完成")).toBeVisible();
  await clickCheck(2);
  expect(await screen.findByText("第 2 期：当前核验无法确定付款条件")).toBeVisible();
  expect(screen.getByText("第 1 期：当前核验未满足付款条件")).toBeVisible();
  expect(checkContractPaymentEligibility).toHaveBeenCalledTimes(2);
});

it("rejects mismatched verification responses and request failures without showing a result", async () => {
  vi.mocked(listContractPayments).mockResolvedValue([]);
  vi.mocked(checkContractPaymentEligibility)
    .mockResolvedValueOnce(eligibilityResult({ contract_id: "contract-2" }))
    .mockRejectedValueOnce(new ApiError("资格核验失败", 503, "unavailable", true, null));
  vi.mocked(pendingMutationKey)
    .mockResolvedValueOnce({ storageKey: "storage-1", key: "key-1" })
    .mockResolvedValueOnce({ storageKey: "storage-2", key: "key-2" })
    .mockRejectedValueOnce(new ApiError("幂等键生成失败", 0, "network", true, null));
  render(<ContractPaymentsPanel detail={detail()} actor={actor} canCheck onRefresh={onRefresh} />);

  await clickCheck(1);
  expect(await screen.findByText("请求没有完成")).toBeVisible();
  expect(clearPendingMutationKey).not.toHaveBeenCalled();
  expect(screen.queryByText("当前核验符合付款条件")).not.toBeInTheDocument();

  await clickCheck(1);
  expect(await screen.findByText("资格核验失败")).toBeVisible();
  expect(clearPendingMutationKey).not.toHaveBeenCalled();

  await clickCheck(1);
  expect(await screen.findByText("幂等键生成失败")).toBeVisible();
  expect(checkContractPaymentEligibility).toHaveBeenCalledTimes(2);
  expect(onRefresh).not.toHaveBeenCalled();
});

it("keeps the recorded verification visible when the contract refresh fails", async () => {
  vi.mocked(listContractPayments).mockResolvedValue([]);
  vi.mocked(checkContractPaymentEligibility).mockResolvedValue(eligibilityResult());
  onRefresh.mockRejectedValue(new ApiError("刷新失败", 500, "internal", false, null));
  render(<ContractPaymentsPanel detail={detail()} actor={actor} canCheck onRefresh={onRefresh} />);

  await clickCheck(1);
  expect(await screen.findByText("资格核验已记录，但合同版本刷新失败：刷新失败")).toBeVisible();
  expect(clearPendingMutationKey).toHaveBeenCalledWith("storage-1");
  expect(screen.queryByText("第 1 期：当前核验符合付款条件")).not.toBeInTheDocument();
  expect(screen.getAllByText("付款状态正在读取")).toHaveLength(2);
  expect(onRefresh).toHaveBeenCalledTimes(1);
});

it("ignores receipt responses for a superseded revision", async () => {
  let resolveFirst: (value: ContractPaymentSummary[]) => void = () => {};
  vi.mocked(listContractPayments)
    .mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFirst = resolve;
      }),
    )
    .mockResolvedValueOnce([
      paymentSummary({
        contract_revision_id: "revision-2",
        payment_sequence: 1,
        state: "partial",
        net_paid: "20.00",
        receipts: [receipt(1, "20.00")],
      }),
    ]);
  const view = render(
    <ContractPaymentsPanel detail={detail()} actor={actor} canCheck onRefresh={onRefresh} />,
  );
  view.rerender(
    <ContractPaymentsPanel
      detail={detail({ current_revision_id: "revision-2" })}
      actor={actor}
      canCheck
      onRefresh={onRefresh}
    />,
  );
  expect(await screen.findByText(/已登记可信净额：20.00 CNY/)).toBeVisible();
  await act(async () => {
    resolveFirst([
      paymentSummary({
        payment_sequence: 1,
        state: "paid",
        net_paid: "999.00",
        receipts: [receipt(1, "999.00")],
      }),
    ]);
  });
  expect(screen.queryByText(/999.00/)).not.toBeInTheDocument();
  expect(screen.queryByText("账本记录达到计划金额")).not.toBeInTheDocument();
  expect(listContractPayments).toHaveBeenCalledTimes(2);
});
