// File metadata (manifest mirror):
// Purpose: Verifies full technical values remain available for manual copy and clipboard success, denial, missing API, value switches and unmount do not report another request result.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-05
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  TechnicalDetail,
  TechnicalFieldGroup,
  technicalLabel,
} from "./technical-detail";

const originalClipboard = Object.getOwnPropertyDescriptor(navigator, "clipboard");
function clipboard(value: unknown) {
  Object.defineProperty(navigator, "clipboard", { configurable: true, value });
}
afterEach(() => {
  if (originalClipboard) Object.defineProperty(navigator, "clipboard", originalClipboard);
  else Reflect.deleteProperty(navigator, "clipboard");
  vi.useRealTimers();
});

describe("technical value copy", () => {
  it("reports success without hiding the complete selectable value", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    clipboard({ writeText });
    render(<TechnicalDetail value="full-case-identity" />);
    fireEvent.click(screen.getByText("技术详情"));
    fireEvent.click(screen.getByRole("button", { name: "复制完整值", hidden: true }));
    expect(await screen.findByRole("status", { hidden: true })).toHaveTextContent("已复制完整值");
    expect(writeText).toHaveBeenCalledWith("full-case-identity");
    expect(screen.getByText("full-case-identity")).toBeInTheDocument();
  });

  it.each([undefined, { writeText: () => Promise.reject(new Error("denied")) }])("reports unavailable or denied clipboard with manual fallback", async (api) => {
    clipboard(api);
    render(<TechnicalDetail value="manual-full-value" />);
    fireEvent.click(screen.getByText("技术详情"));
    fireEvent.click(screen.getByRole("button", { name: "复制完整值", hidden: true }));
    await waitFor(() => expect(screen.getByRole("status", { hidden: true })).toHaveTextContent("复制失败，请选中上方完整值手动复制。"));
    expect(screen.getByText("manual-full-value")).toBeInTheDocument();
  });

  it("ignores old requests after switching values and after unmount", async () => {
    let resolveOld!: () => void;
    const writeText = vi.fn().mockImplementationOnce(() => new Promise<void>((resolve) => { resolveOld = resolve; })).mockRejectedValueOnce(new Error("denied"));
    clipboard({ writeText });
    const view = render(<TechnicalDetail value="first-full-value" />);
    fireEvent.click(screen.getByText("技术详情"));
    fireEvent.click(screen.getByRole("button", { name: "复制完整值", hidden: true }));
    view.rerender(<TechnicalDetail value="second-full-value" />);
    fireEvent.click(screen.getByRole("button", { name: "复制完整值", hidden: true }));
    await waitFor(() => expect(screen.getByRole("status", { hidden: true })).toHaveTextContent("复制失败"));
    await act(async () => resolveOld());
    expect(screen.getByRole("status", { hidden: true })).toHaveTextContent("复制失败");
    view.unmount();
  });

  it("does not restore an old success when the same value is shown again", async () => {
    clipboard({ writeText: vi.fn().mockResolvedValue(undefined) });
    const view = render(<TechnicalDetail value="A" />);
    fireEvent.click(screen.getByText("技术详情"));
    fireEvent.click(screen.getByRole("button", { name: "复制完整值" }));
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("已复制"));
    view.rerender(<TechnicalDetail value="B" />);
    view.rerender(<TechnicalDetail value="A" />);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(technicalLabel("constructor_x")).toBe("记录");
  });

  it("ignores a pending result after unmount", async () => {
    let finish!: () => void;
    clipboard({ writeText: () => new Promise<void>((resolve) => { finish = resolve; }) });
    const view = render(<TechnicalDetail value="removed-value" />);
    fireEvent.click(screen.getByText("技术详情"));
    fireEvent.click(screen.getByRole("button", { name: "复制完整值" }));
    view.unmount();
    await act(async () => finish());
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });
});

describe("technical field group", () => {
  it("keeps stable identifiers out of the first screen until requested", () => {
    render(
      <TechnicalFieldGroup
        fields={[
          { label: "任务 ID", value: "task/server-created" },
          { label: "状态编码", value: "open" },
          { label: "责任人标识", value: null },
        ]}
      />,
    );
    expect(screen.getByText(/技术详情 · 完整标识与编码/)).toBeVisible();
    expect(screen.getByText("task/server-created")).not.toBeVisible();
    expect(screen.getByText("未报告")).not.toBeVisible();
    fireEvent.click(screen.getByText(/技术详情 · 完整标识与编码/));
    expect(screen.getByText("task/server-created")).toBeVisible();
    expect(screen.getByText("open")).toBeVisible();
    expect(screen.getByText("未报告")).toBeVisible();
  });

  it("copies one full value without collapsing the group", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    clipboard({ writeText });
    render(
      <TechnicalFieldGroup fields={[{ label: "案件标识", value: "case/full" }]} />,
    );
    fireEvent.click(screen.getByText(/技术详情 · 完整标识与编码/));
    fireEvent.click(screen.getByRole("button", { name: "复制完整值" }));
    expect(await screen.findByRole("status", { hidden: true })).toHaveTextContent("已复制完整值");
    expect(writeText).toHaveBeenCalledWith("case/full");
    expect(screen.getByText("case/full")).toBeVisible();
  });
});
