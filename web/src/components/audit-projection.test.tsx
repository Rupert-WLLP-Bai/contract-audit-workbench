import { screen, within } from "@testing-library/react";
import userEvent, { type UserEvent } from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { AuditEvent, BusinessAuditEvent } from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { AuditProjection } from "./audit-projection";

const businessEvent: BusinessAuditEvent = {
  event_id: "event-1",
  run_id: "run-1",
  actor: "审查员甲",
  seq: 1,
  event_type: "review_started",
  correlation_ids: [],
};

const hashEvent: AuditEvent = {
  ...businessEvent,
  event_id: "event-2",
  seq: 2,
  event_type: "human_decision",
  prev_hash: "prev-hash",
  hash: "current-hash",
  correlation_ids: ["finding-1", "decision-1"],
};

async function openPanel(user: UserEvent, count: number) {
  await user.click(
    screen.getByRole("button", { name: `查看审计记录（${count}）` }),
  );
}

async function openEvent(user: UserEvent, seq: number, eventType: string) {
  await user.click(
    screen.getByRole("button", {
      name: new RegExp(`序号 ${seq}，${eventType}，`),
    }),
  );
}

describe("audit projection", () => {
  it("explains an empty audit history without claiming it is complete", async () => {
    const user = userEvent.setup();
    render(<AuditProjection runId="run-1" events={[]} />);
    expect(screen.getByText("业务审计")).toBeInTheDocument();
    expect(
      screen.getByText("这里记录本次审查的各次执行和人工操作，按系统保存的顺序显示。"),
    ).toBeInTheDocument();
    await openPanel(user, 0);
    expect(screen.getByText("尚未获取本次审查的审计记录。")).toBeInTheDocument();
  });

  it("does not treat prototype keys as known audit labels", async () => {
    const user = userEvent.setup();
    render(
      <AuditProjection
        runId="run-1"
        events={[{ ...businessEvent, event_type: "constructor" }]}
      />,
    );
    await openPanel(user, 1);
    expect(screen.getByText("记录 1，其他审查记录")).toBeInTheDocument();
  });

  it("lists business event labels with a neutral fallback", async () => {
    const user = userEvent.setup();
    render(
      <AuditProjection
        runId="run-1"
        events={[{ ...businessEvent, seq: 9, event_type: "unknown_event" }]}
      />,
    );
    await openPanel(user, 1);
    expect(screen.getByText("记录 9，其他审查记录")).toBeInTheDocument();
  });

  it.each([
    ["review_started", "审查已发起"],
    ["review_retried", "已重新执行审查"],
    ["review_cancelled", "审查已取消"],
    ["attempt_input_frozen", "已保存本次审查材料"],
    ["human_decision", "已记录审查判断"],
  ])("maps %s to %s", async (eventType, label) => {
    const user = userEvent.setup();
    render(
      <AuditProjection
        runId="run-1"
        events={[{ ...businessEvent, event_type: eventType }]}
      />,
    );
    await openPanel(user, 1);
    expect(screen.getByText(`记录 1，${label}`)).toBeInTheDocument();
  });

  it("hides the run identity and hashes without diagnostics", async () => {
    const user = userEvent.setup();
    render(<AuditProjection runId="run-1" events={[hashEvent]} />);
    await openPanel(user, 1);
    expect(screen.queryByText(/运行：/)).not.toBeInTheDocument();
    expect(screen.queryByText("current-hash")).not.toBeInTheDocument();
    expect(screen.queryByText("prev-hash")).not.toBeInTheDocument();
    expect(screen.queryByText("审查员甲")).not.toBeInTheDocument();
    expect(screen.getByText("记录 2，已记录审查判断")).toBeInTheDocument();
  });

  it("shows diagnostic identities, hashes and correlation ids when expanded", async () => {
    const user = userEvent.setup();
    render(
      <AuditProjection runId="run-1" events={[hashEvent]} showDiagnostics />,
    );
    await openPanel(user, 1);
    expect(screen.getByText("运行：run-1")).toBeInTheDocument();
    await openEvent(user, 2, "human_decision");
    const detail = screen.getByText("事件编号").closest("dl")!;
    expect(within(detail).getByText("event-2")).toBeInTheDocument();
    expect(within(detail).getByText("run-1")).toBeInTheDocument();
    expect(within(detail).getByText("current-hash")).toBeInTheDocument();
    expect(within(detail).getByText("prev-hash")).toBeInTheDocument();
    expect(within(detail).getByText("finding-1、decision-1")).toBeInTheDocument();
  });

  it("marks a first record without a previous hash and an unprovided correlation", async () => {
    const user = userEvent.setup();
    render(
      <AuditProjection
        runId="run-1"
        events={[{ ...hashEvent, prev_hash: "", correlation_ids: [] }]}
        showDiagnostics
      />,
    );
    await openPanel(user, 1);
    await openEvent(user, 2, "human_decision");
    expect(screen.getByText("起始记录")).toBeInTheDocument();
    expect(screen.getByText("未提供")).toBeInTheDocument();
  });

  it("keeps a business event free of hash rows even with diagnostics", async () => {
    const user = userEvent.setup();
    render(
      <AuditProjection runId="run-1" events={[businessEvent]} showDiagnostics />,
    );
    await openPanel(user, 1);
    await openEvent(user, 1, "review_started");
    expect(screen.queryByText("记录摘要")).not.toBeInTheDocument();
    expect(screen.queryByText("前序摘要")).not.toBeInTheDocument();
    expect(screen.getByText("未提供")).toBeInTheDocument();
  });
});
