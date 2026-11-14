// File metadata (manifest mirror):
// Purpose: Checks that distinct frozen plans can each receive reminders and that closed contracts retain readable reminder history.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import type { PropsWithChildren } from "react";
import { expect, it, vi } from "vitest";
import {
  listContractReminders,
  type ContractDetail,
  type ContractReminderList,
} from "../api/contracts";
import type { Actor } from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { ContractRemindersPanel } from "./contract-reminders-panel";

vi.mock("../api/contracts", async (original) => ({
  ...(await original<typeof import("../api/contracts")>()),
  listContractReminders: vi.fn(),
}));

it("allows a second distinct plan and keeps reminder history readable after the contract closes", async () => {
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
  const detail: ContractDetail = {
    summary: {
      contract_id: "contract-1",
      authority_mode: "native",
      name: "履约合同",
      number: "HT-1",
      amount: null,
      currency: null,
      contract_state: "performing",
      workflow_state: "completed",
      row_version: 3,
      created_at: "2026-11-14T00:00:00Z",
    },
    current_revision_id: "revision-1",
    active_round_id: null,
    allowed_commands: [],
    warnings: [],
  };
  const response: ContractReminderList = {
    contract_revision_id: "revision-1",
    contract_row_version: 3,
    plans: [
      { kind: "payment", sequence: 1, planned_date: "2027-01-10", description: "付款计划" },
      { kind: "delivery", sequence: 1, planned_date: "2027-01-09", description: "交付计划" },
    ],
    reminders: [
      {
        reminder_id: "reminder-1",
        contract_id: "contract-1",
        contract_revision_id: "revision-1",
        plan_kind: "payment",
        plan_sequence: 1,
        planned_date: "2027-01-10",
        timezone: "Asia/Shanghai",
        recipient_id: actor.id,
        lead_days: 7,
        due_at: "2027-01-03T01:00:00Z",
        status: "active",
        version: 1,
        confirmed_by: actor.id,
        confirmed_at: "2026-11-14T00:00:00Z",
      },
    ],
  };
  vi.mocked(listContractReminders).mockResolvedValue(response);
  const onRefresh = vi.fn(async () => {});
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  function QueryWrapper({ children }: PropsWithChildren) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  }
  const view = render(
    <ContractRemindersPanel detail={detail} actor={actor} canManage onRefresh={onRefresh} />,
    { wrapper: QueryWrapper },
  );
  expect(await screen.findByRole("button", { name: "确认提醒我" })).toBeVisible();
  expect(screen.getByRole("heading", { name: "提醒记录" })).toBeVisible();

  vi.mocked(listContractReminders).mockResolvedValue({
    ...response,
    contract_revision_id: "",
    plans: [],
    reminders: response.reminders.map((item) => ({ ...item, status: "stale" })),
  });
  view.rerender(
    <ContractRemindersPanel
      detail={{ ...detail, summary: { ...detail.summary, contract_state: "closed" } }}
      actor={actor}
      canManage
      onRefresh={onRefresh}
    />,
  );
  await waitFor(() => expect(screen.getByText("已失效")).toBeVisible());
  expect(screen.getByRole("heading", { name: "提醒记录" })).toBeVisible();
  expect(screen.queryByRole("button", { name: "确认提醒我" })).not.toBeInTheDocument();
  expect(screen.queryByText(/合同详情与提醒读取到的冻结修订不一致/)).not.toBeInTheDocument();
});
