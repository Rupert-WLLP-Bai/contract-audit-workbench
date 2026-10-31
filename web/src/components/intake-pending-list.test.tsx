import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setSessionIdentity } from "../api/http";
import type { PendingIntake, ServerIntakeContinuation } from "../api/intakes";
import type { Actor } from "../api/types";
import { IntakePendingList } from "./intake-pending-list";
import type { IntakeContinuationSelection } from "../features/intake/intake-continuation";
import { renderWithAntd as render } from "../test/antd";

const actor: Actor = {
  id: "clerk-1",
  display_name: "经办人",
  role: "clerk",
  tenant_id: "tenant-a",
  org_id: "org-a",
  audience: "client",
};

function pendingItem(
  operationId: string,
  caseId: string,
  title: string,
  overrides: Partial<PendingIntake> = {},
): PendingIntake {
  return {
    operation_id: operationId,
    case_id: caseId,
    title,
    state: "upload_pending",
    file_name: `${title}.pdf`,
    file_size: 2048,
    created_at: "2026-10-30T10:00:00Z",
    updated_at: "2026-11-01T08:30:00Z",
    error_code: "document_upload_unavailable",
    can_resume: true,
    can_archive: true,
    reason_code: "",
    ...overrides,
  };
}

function continuation(operationId: string, caseId: string): ServerIntakeContinuation {
  return {
    operation_id: operationId,
    case_id: caseId,
    state: "upload_pending",
    request: {
      title: `合同 ${operationId}`,
      contract_type: "service",
      business_line: "ENT",
      scenario: "telecom_dedicated_line",
      file_name: `合同 ${operationId}.pdf`,
      file_size: 2048,
      media_type: "application/pdf",
      file_sha256: "a".repeat(64),
    },
    idempotency_key: `key-${operationId}`,
    can_resume: true,
    can_archive: true,
    reason_code: "",
  };
}

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

class PendingService {
  calls: { method: string; path: string }[] = [];
  items: PendingIntake[];
  archived = false;
  nextCursor: string | null = null;
  nextPageItems: PendingIntake[] = [];
  pendingFailures = 0;

  constructor(items: PendingIntake[]) {
    this.items = items;
  }

  fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://pending.test");
    const method = init?.method ?? "GET";
    this.calls.push({ method, path: `${url.pathname}${url.search}` });
    if (method === "GET" && url.pathname === "/api/intakes/pending") {
      if (this.pendingFailures > 0) {
        this.pendingFailures -= 1;
        return json({ error: { message: "列表服务暂时不可用" } }, 503);
      }
      const cursor = url.searchParams.get("cursor");
      return json({
        items: this.archived
          ? []
          : cursor === "cursor-page-2"
            ? this.nextPageItems
            : this.items,
        next_cursor: this.archived
          ? null
          : cursor === "cursor-page-2"
            ? null
            : this.nextCursor,
      });
    }
    const continuationMatch = url.pathname.match(
      /^\/api\/intakes\/([^/]+)\/continuation$/,
    );
    if (method === "GET" && continuationMatch !== null) {
      const operationId = decodeURIComponent(continuationMatch[1] ?? "");
      const item = this.items.find((entry) => entry.operation_id === operationId);
      if (item === undefined)
        return json({ error: { message: "operation not found" } }, 404);
      return json(continuation(operationId, item.case_id));
    }
    const archiveMatch = url.pathname.match(/^\/api\/intakes\/([^/]+)\/archive$/);
    if (method === "POST" && archiveMatch !== null) {
      const operationId = decodeURIComponent(archiveMatch[1] ?? "");
      const item = this.items.find((entry) => entry.operation_id === operationId);
      if (item === undefined)
        return json({ error: { message: "operation not found" } }, 404);
      this.archived = true;
      return json({
        operation_id: operationId,
        case_id: item.case_id,
        archived_at: "2026-11-01T09:00:00Z",
      });
    }
    return json({ error: { message: `unexpected ${method} ${url.pathname}` } }, 404);
  };
}

const clients: QueryClient[] = [];

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  localStorage.clear();
  setSessionIdentity(null);
  vi.unstubAllGlobals();
});

function renderList(
  service: PendingService,
  onSelectContinuation = vi.fn<(selection: IntakeContinuationSelection) => void>(),
  onArchived = vi.fn<(operationId: string) => void>(),
) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  clients.push(client);
  setSessionIdentity(actor);
  vi.stubGlobal("fetch", vi.fn(service.fetch));
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <IntakePendingList
          actor={actor}
          onSelectContinuation={onSelectContinuation}
          onArchived={onArchived}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { onSelectContinuation, onArchived, client };
}

describe("server-backed pending intake list", () => {
  it("finds multiple server operations after local continuation metadata is cleared", async () => {
    localStorage.clear();
    const service = new PendingService([
      pendingItem("op-1", "case-1", "第一份采购合同"),
      pendingItem("op-2", "case-2", "第二份采购合同"),
    ]);
    const { onSelectContinuation } = renderList(service);
    const user = userEvent.setup();

    expect(
      await screen.findByRole("heading", { name: "我的未完成办理" }),
    ).toBeInTheDocument();
    expect(await screen.findByText("第一份采购合同")).toBeInTheDocument();
    expect(screen.getByText("第二份采购合同")).toBeInTheDocument();
    expect(localStorage.length).toBe(0);

    const secondRow = screen
      .getByRole("heading", { name: "第二份采购合同" })
      .closest("li");
    expect(secondRow).not.toBeNull();
    await user.click(
      within(secondRow as HTMLElement).getByRole("button", {
        name: "继续办理",
      }),
    );

    await waitFor(() => expect(onSelectContinuation).toHaveBeenCalledTimes(1));
    expect(onSelectContinuation).toHaveBeenCalledWith(
      expect.objectContaining({
        detail: expect.objectContaining({
          operation_id: "op-2",
          case_id: "case-2",
          idempotency_key: "key-op-2",
          can_resume: true,
        }),
        error_code: "document_upload_unavailable",
        archived: false,
      }),
    );
    expect(service.calls).toContainEqual({
      method: "GET",
      path: "/api/intakes/op-2/continuation",
    });
    expect(service.calls.some((call) => call.method === "POST")).toBe(false);
    const firstRow = screen
      .getByRole("heading", { name: "第一份采购合同" })
      .closest("li");
    expect(firstRow).not.toBeNull();
    await user.click(
      within(firstRow as HTMLElement).getByRole("button", {
        name: "继续办理",
      }),
    );
    await waitFor(() => expect(onSelectContinuation).toHaveBeenCalledTimes(2));
    expect(onSelectContinuation).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({
        detail: expect.objectContaining({
          operation_id: "op-1",
          case_id: "case-1",
          idempotency_key: "key-op-1",
        }),
        archived: false,
      }),
    );
    expect(service.calls).toContainEqual({
      method: "GET",
      path: "/api/intakes/op-1/continuation",
    });
    expect(service.calls.some((call) => call.method === "POST")).toBe(false);
  });

  it("shows a server list error and retries on request", async () => {
    const service = new PendingService([
      pendingItem("op-retry", "case-retry", "重新读取合同"),
    ]);
    service.pendingFailures = 1;
    renderList(service);
    const user = userEvent.setup();

    expect(
      await screen.findByText("未完成办理列表暂不可用"),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("重新读取合同")).toBeInTheDocument();
    expect(
      service.calls.filter((call) => call.path.startsWith("/api/intakes/pending")),
    ).toHaveLength(2);
  });

  it("explains unavailable operations and keeps the original case entry", async () => {
    const service = new PendingService([
      pendingItem("legacy", "case-legacy", "旧办理", {
        can_resume: false,
        can_archive: false,
        reason_code: "legacy_request_unavailable",
      }),
      pendingItem("readonly", "case-readonly", "只读办理", {
        can_resume: false,
        can_archive: false,
        reason_code: "read_only",
      }),
      pendingItem("forbidden", "case-forbidden", "无权办理", {
        can_resume: false,
        can_archive: false,
        reason_code: "forbidden",
      }),
      pendingItem("completed", "case-completed", "已完成办理", {
        can_resume: false,
        can_archive: false,
        reason_code: "completed",
      }),
    ]);
    renderList(service);

    expect(
      await screen.findByText(
        "这是旧办理，服务器没有可验证的原始请求信息，无法安全续传。原案件仍可打开。",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("当前工作区处于只读状态，不能继续上传。"))
      .toBeInTheDocument();
    expect(
      screen.getByText(
        "当前身份无权继续该办理。请使用原经办身份登录，或在有权查看时打开原案件。",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText("服务器已确认该办理完成；请打开原案件查看。"),
    ).toBeInTheDocument();
    const oldRow = screen
      .getByRole("heading", { name: "旧办理" })
      .closest("li");
    expect(oldRow).not.toBeNull();
    expect(
      within(oldRow as HTMLElement).getByRole("link", { name: "打开原案件" }),
    ).toHaveAttribute("href", "/cases/case-legacy");
    expect(screen.queryByRole("button", { name: "继续办理" })).not.toBeInTheDocument();
  });


  it("moves through server cursor pages and returns to the previous page", async () => {
    const service = new PendingService([
      pendingItem("page-1", "case-page-1", "第一页办理"),
    ]);
    service.nextCursor = "cursor-page-2";
    service.nextPageItems = [
      pendingItem("page-2", "case-page-2", "第二页办理"),
    ];
    const user = userEvent.setup();
    renderList(service);

    expect(await screen.findByText("第一页办理")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "下一页" }));
    expect(await screen.findByText("第二页办理")).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "第一页办理" }),
    ).not.toBeInTheDocument();
    expect(
      service.calls.some((call) =>
        call.path.includes("cursor=cursor-page-2"),
      ),
    ).toBe(true);

    await user.click(screen.getByRole("button", { name: "上一页" }));
    expect(await screen.findByText("第一页办理")).toBeInTheDocument();
  });

  it("archives only after an explicit confirmation and refreshes the server list", async () => {
    const service = new PendingService([
      pendingItem("op-archive", "case-archive", "归档采购合同"),
    ]);
    const { onArchived } = renderList(service);
    const user = userEvent.setup();

    const rowTitle = await screen.findByRole("heading", {
      name: "归档采购合同",
    });
    const row = rowTitle.closest("li");
    expect(row).not.toBeNull();
    await user.click(
      within(row as HTMLElement).getByRole("button", { name: "归档办理" }),
    );
    const confirmation = screen.getByRole("group", {
      name: "确认归档 归档采购合同",
    });
    await user.click(
      within(confirmation).getByRole("button", { name: "确认归档" }),
    );

    await waitFor(() => expect(onArchived).toHaveBeenCalledWith("op-archive"));
    expect(
      await screen.findByText("当前没有未完成办理。"),
    ).toBeInTheDocument();
    expect(
      service.calls.some(
        (call) =>
          call.method === "POST" &&
          call.path === "/api/intakes/op-archive/archive",
      ),
    ).toBe(true);
    expect(service.calls.some((call) => call.path.startsWith("/api/cases/")))
      .toBe(false);
  });
});
