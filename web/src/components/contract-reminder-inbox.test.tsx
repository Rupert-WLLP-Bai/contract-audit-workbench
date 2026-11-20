import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import type { ContractReminderInboxItem } from "../api/contracts";
import type { Actor } from "../api/types";
import { actorReadContextKey } from "../domain/actor-read-context";
import { renderWithAntd } from "../test/antd";
import { ContractReminderInbox } from "./contract-reminder-inbox";

const actor: Actor = {
  id: "actor-1",
  display_name: "周审查",
  role: "reviewer",
  audience: "client",
  tenant_id: "tenant-1",
  org_id: "org-1",
  principal_id: "principal-1",
  active_context: {
    membership_id: "membership-1",
    tenant_id: "tenant-1",
    tenant_name: "租户甲",
    org_id: "org-1",
    org_name: "组织甲",
    context_version: "1",
  },
  csrf_token: "test-csrf",
  context_switch_enabled: true,
};

const reminder = (
  sequence: number,
  planKind: ContractReminderInboxItem["plan_kind"] = "payment",
): ContractReminderInboxItem => ({
  reminder_id: `reminder-${sequence}`,
  contract_id: `contract-${sequence}`,
  contract_revision_id: `revision-${sequence}`,
  plan_kind: planKind,
  plan_sequence: sequence,
  planned_date: "2026-11-22",
  timezone: "Asia/Shanghai",
  delivered_at: "2026-11-20T04:32:00Z",
});

const clients: QueryClient[] = [];

function show(fetch: typeof globalThis.fetch) {
  vi.stubGlobal("fetch", vi.fn(fetch));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  clients.push(client);
  const onOpenContract = vi.fn(async (_contractId: string) => undefined);
  const view = (current: Actor) => (
    <QueryClientProvider client={client}>
      <ContractReminderInbox actor={current} contracts={[]} onOpenContract={onOpenContract} />
    </QueryClientProvider>
  );
  const render = renderWithAntd(view(actor));
  return {
    client,
    changeActor: (current: Actor) => render.rerender(view(current)),
    onOpenContract,
    user: userEvent.setup(),
  };
}

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  vi.unstubAllGlobals();
});

it("reserves no ledger space when the authorized inbox is empty", async () => {
  const { client } = show(async () => Response.json({ items: [] }));
  await waitFor(() =>
    expect(
      client.getQueryCache().find({ queryKey: ["contracts", "reminder-inbox"], exact: false })
        ?.state.status,
    ).toBe("success"),
  );
  expect(screen.queryByRole("region", { name: "履约提醒" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "查看提醒" })).not.toBeInTheDocument();
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
});

it("retries a failed read without reporting zero or fabricating reminders", async () => {
  let failed = true;
  const { user } = show(async () => {
    const result = failed
      ? Response.json({ error: { code: "unavailable", message: "读取失败" } }, { status: 503 })
      : Response.json({ items: [reminder(1)] });
    failed = false;
    return result;
  });
  expect(await screen.findByRole("alert")).toHaveTextContent("提醒暂时无法读取");
  expect(screen.queryByRole("region", { name: "履约提醒" })).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "重试读取提醒" }));
  expect(await screen.findByRole("region", { name: "履约提醒" })).toHaveTextContent(
    "有 1 项履约或付款提醒",
  );
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("opens the read-only drawer, paginates all plan types, restores focus and passes the exact contract", async () => {
  const { onOpenContract, user } = show(async () =>
    Response.json({
      items: [
        reminder(1, "delivery"),
        reminder(2, "acceptance"),
        reminder(3),
        reminder(4),
        reminder(5),
        reminder(6),
      ],
    }),
  );
  const trigger = await screen.findByRole("button", { name: "查看提醒" });
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
  await user.click(trigger);
  let drawer = within(await screen.findByRole("dialog", { name: "我的履约提醒" }));
  expect(drawer.getByText("交付第 1 期")).toBeVisible();
  expect(drawer.getByText("验收第 2 期")).toBeVisible();
  expect(drawer.getByText("付款第 3 期")).toBeVisible();
  expect(
    drawer
      .getByText("付款第 3 期")
      .closest("tr")
      ?.querySelector("time[datetime='2026-11-20T04:32:00Z']"),
  ).toHaveTextContent("北京时间");
  expect(drawer.queryByText("付款第 6 期")).not.toBeInTheDocument();
  await user.click(drawer.getByRole("button", { name: "关闭" }));
  await waitFor(() => expect(trigger).toHaveFocus());

  await user.click(trigger);
  drawer = within(await screen.findByRole("dialog", { name: "我的履约提醒" }));
  await user.click(drawer.getByTitle("2"));
  const row = drawer.getByText("付款第 6 期").closest("tr");
  if (!row) throw new Error("Missing second-page reminder");
  await user.click(within(row).getByRole("button", { name: "查看合同" }));
  expect(onOpenContract).toHaveBeenCalledExactlyOnceWith("contract-6");
  await waitFor(() =>
    expect(screen.queryByRole("dialog", { name: "我的履约提醒" })).not.toBeInTheDocument(),
  );
});

it("hides the preceding identity's open drawer and reminders during a context change", async () => {
  let read = 0;
  const { changeActor, client, user } = show(async () =>
    Response.json({
      items: ++read === 1 ? [reminder(1, "delivery")] : [],
    }),
  );
  await user.click(await screen.findByRole("button", { name: "查看提醒" }));
  expect(await screen.findByRole("dialog", { name: "我的履约提醒" })).toBeVisible();
  const nextActor = {
    ...actor,
    active_context: { ...actor.active_context, context_version: "2" },
  };
  changeActor(nextActor);
  await waitFor(() =>
    expect(
      client.getQueryState(["contracts", "reminder-inbox", actorReadContextKey(nextActor)])?.status,
    ).toBe("success"),
  );
  expect(screen.queryByRole("dialog", { name: "我的履约提醒" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "查看提醒" })).not.toBeInTheDocument();
});
