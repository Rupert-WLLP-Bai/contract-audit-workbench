import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fileSha256 } from "../api/idempotency";
import type { Actor } from "../api/types";
import {
  readIntakeContinuation,
  scopeOf,
  writeIntakeContinuation,
  type IntakeContinuation,
} from "../features/intake/intake-continuation";
import { renderWithAntd as render } from "../test/antd";
import { IntakePrimaryContract } from "./intake-primary-contract";

const clerk: Actor = {
  id: "user-clerk",
  display_name: "周登记",
  role: "clerk",
  tenant_id: "tenant-a",
  org_id: "org-a",
  audience: "client",
};

const otherTenant: Actor = {
  ...clerk,
  tenant_id: "tenant-b",
  org_id: "org-b",
};

const reviewer: Actor = {
  ...clerk,
  id: "user-reviewer",
  display_name: "审查员乙",
  role: "reviewer",
};

const SOURCE_BYTES = "contract bytes that must never reach local storage";

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function completedResult(
  caseId = "case_new",
  overrides: Record<string, unknown> = {},
) {
  return {
    operation_id: "intake_new",
    case_id: caseId,
    state: "completed",
    material_id: "mat_primary",
    material_version: 1,
    document_id: "doc_primary",
    retryable: false,
    ...overrides,
  };
}

function pendingResult(caseId = "case_pending") {
  return {
    operation_id: "intake_pending",
    case_id: caseId,
    state: "upload_pending",
    retryable: true,
    error_code: "document_upload_unavailable",
  };
}

interface Call {
  method: string;
  url: string;
  key: string | null;
  form: FormData | null;
  storageAtPost: Record<string, string> | null;
}

function storageSnapshot(): Record<string, string> {
  const snapshot: Record<string, string> = {};
  for (let index = 0; index < localStorage.length; index += 1) {
    const key = localStorage.key(index);
    if (key === null) continue;
    snapshot[key] = localStorage.getItem(key) ?? "";
  }
  return snapshot;
}

class IntakeService {
  calls: Call[] = [];
  post: () => Response | Promise<Response> = () => json(completedResult(), 201);
  getByKey: (key: string) => Response | Promise<Response> = () =>
    json(pendingResult(), 200);
  getById: (operationId: string) => Response | Promise<Response> = () =>
    json(pendingResult(), 200);
  fetch = async (
    input: RequestInfo | URL,
    init?: RequestInit,
  ): Promise<Response> => {
    const url = new URL(String(input), "http://intake.test");
    const method = init?.method ?? "GET";
    const key = new Headers(init?.headers).get("Idempotency-Key");
    this.calls.push({
      method,
      url: `${url.pathname}${url.search}`,
      key,
      form: init?.body instanceof FormData ? init.body : null,
      storageAtPost: method === "POST" ? storageSnapshot() : null,
    });
    if (method === "POST" && url.pathname === "/api/intakes") return this.post();
    if (method === "GET" && url.pathname === "/api/intakes")
      return this.getByKey(url.searchParams.get("key") ?? "");
    if (method === "GET" && url.pathname.startsWith("/api/intakes/"))
      return this.getById(
        decodeURIComponent(url.pathname.slice("/api/intakes/".length)),
      );
    return json(
      { error: { message: `unexpected ${method} ${url.pathname}` } },
      404,
    );
  };
}

function RouteProbe() {
  const location = useLocation();
  return <p data-testid="route">{location.pathname}</p>;
}

function openWithService(service: IntakeService) {
  vi.stubGlobal("fetch", vi.fn(service.fetch));
  return { user: userEvent.setup() };
}

function renderIntake(actor: Actor) {
  render(
    <MemoryRouter initialEntries={["/"]}>
      <Routes>
        <Route path="/" element={<IntakePrimaryContract actor={actor} />} />
        <Route path="/cases/:caseId" element={<RouteProbe />} />
      </Routes>
    </MemoryRouter>,
  );
}

async function seedContinuation(
  actor: Actor,
  {
    key = "key-pending-1",
    receipt,
    file,
    sha,
  }: {
    key?: string;
    receipt: IntakeContinuation["receipt"];
    file?: File;
    sha?: string;
  },
): Promise<{ file: File; sha: string }> {
  const chosen =
    file ??
    new File([SOURCE_BYTES], "专线采购合同.txt", { type: "text/plain" });
  const digest = sha ?? (await fileSha256(chosen));
  const continuation: IntakeContinuation = {
    version: 1,
    scope: scopeOf(actor),
    idempotency_key: key,
    request: {
      title: "专线采购合同",
      contract_type: "service",
      business_line: "ENT",
      scenario: "telecom_dedicated_line",
      file_name: chosen.name,
      file_size: chosen.size,
      media_type: chosen.type,
      file_sha256: digest,
    },
    receipt,
    updated_at: "2026-10-31T06:00:00Z",
  };
  await writeIntakeContinuation(continuation);
  return { file: chosen, sha: digest };
}

const pendingReceipt: IntakeContinuation["receipt"] = {
  operation_id: "intake_pending",
  case_id: "case_pending",
  state: "upload_pending",
  material_id: null,
  material_version: null,
  document_id: null,
  error_code: "document_upload_unavailable",
  retryable: true,
};

const completedReceipt: IntakeContinuation["receipt"] = {
  operation_id: "intake_done",
  case_id: "case_done",
  state: "completed",
  material_id: "mat_done",
  material_version: 1,
  document_id: "doc_done",
  error_code: null,
  retryable: false,
};

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.unstubAllGlobals();
});

describe("homepage primary-contract intake", () => {
  beforeEach(() => localStorage.clear());

  it("writes the scoped key and SHA-256 before POST and opens the created case", async () => {
    const service = new IntakeService();
    const { user } = openWithService(service);
    renderIntake(clerk);
    await user.type(
      await screen.findByRole("textbox", { name: "案件标题" }),
      "专线采购合同",
    );
    const file = new File([SOURCE_BYTES], "专线采购合同.txt", {
      type: "text/plain",
    });
    await user.upload(screen.getByLabelText("主合同文件"), file);
    await user.click(
      screen.getByRole("button", { name: "上传主合同并建立案件" }),
    );

    expect(await screen.findByTestId("route")).toHaveTextContent(
      "/cases/case_new",
    );
    const post = service.calls.find((call) => call.method === "POST");
    expect(post?.url).toBe("/api/intakes");
    expect(post?.key).toMatch(/^[0-9a-zA-Z-]{8,200}$/);
    const form = post?.form;
    expect(form?.get("title")).toBe("专线采购合同");
    expect(form?.get("contract_type")).toBe("service");
    expect(form?.get("business_line")).toBe("ENT");
    expect(form?.get("scenario")).toBe("telecom_dedicated_line");
    expect(form?.get("file")).toBe(file);
    expect(form?.has("intent")).toBe(false);
    expect(form?.has("role")).toBe(false);
    expect(form?.has("purpose")).toBe(false);

    expect(service.calls.filter((call) => call.method === "POST")).toHaveLength(
      1,
    );
    const storedKey = Object.keys(post?.storageAtPost ?? {}).find((key) =>
      key.startsWith("caw:intake-continuation:"),
    );
    expect(storedKey).toBeDefined();
    const duringPost = JSON.parse(
      post?.storageAtPost?.[storedKey!] ?? "{}",
    ) as IntakeContinuation;
    expect(duringPost.idempotency_key).toBe(post?.key);
    expect(duringPost.request.file_sha256).toBe(await fileSha256(file));
    expect(duringPost.receipt.state).toBeNull();
    expect(JSON.stringify(duringPost)).not.toContain(SOURCE_BYTES);

    const stored = await readIntakeContinuation(clerk);
    expect(stored?.receipt).toMatchObject({
      state: "completed",
      case_id: "case_new",
      document_id: "doc_primary",
      material_version: 1,
    });
    expect(stored?.idempotency_key).toBe(post?.key);
  });

  it("recovers a lost first response through the persisted idempotency key without a second case", async () => {
    const service = new IntakeService();
    service.post = () => {
      throw new TypeError("network down");
    };
    service.getByKey = () => json(pendingResult("case_lost"), 202);
    const { user } = openWithService(service);
    renderIntake(clerk);
    await user.type(
      await screen.findByRole("textbox", { name: "案件标题" }),
      "专线采购合同",
    );
    await user.upload(
      screen.getByLabelText("主合同文件"),
      new File([SOURCE_BYTES], "专线采购合同.txt", { type: "text/plain" }),
    );
    await user.click(
      screen.getByRole("button", { name: "上传主合同并建立案件" }),
    );

    expect(
      await screen.findByText("案件已建立，主合同上传尚未完成"),
    ).toBeInTheDocument();
    const post = service.calls.find((call) => call.method === "POST");
    const recovery = service.calls.find((call) => call.method === "GET");
    expect(recovery?.url).toBe(
      `/api/intakes?key=${encodeURIComponent(post?.key ?? "")}`,
    );
    expect(service.calls.filter((call) => call.method === "POST")).toHaveLength(
      1,
    );
    expect(screen.getByRole("link", { name: "case_lost" })).toHaveAttribute(
      "href",
      "/cases/case_lost",
    );
    const stored = await readIntakeContinuation(clerk);
    expect(stored?.receipt).toMatchObject({
      state: "upload_pending",
      case_id: "case_lost",
      error_code: "document_upload_unavailable",
    });
    expect(stored?.idempotency_key).toBe(post?.key);
  });

  it("resumes the same case by operation id and reposts the identical file with the same key", async () => {
    const { file } = await seedContinuation(clerk, {
      receipt: pendingReceipt,
    });
    const service = new IntakeService();
    service.getById = (operationId) => {
      expect(operationId).toBe("intake_pending");
      return json(pendingResult());
    };
    service.post = () => json(completedResult("case_pending"), 201);
    const { user } = openWithService(service);
    renderIntake(clerk);
    expect(
      await screen.findByText("案件已建立，主合同上传尚未完成"),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "继续上传同一文件" }),
      ).toBeEnabled(),
    );
    await user.upload(screen.getByLabelText("重新选择主合同文件"), file);
    await user.click(screen.getByRole("button", { name: "继续上传同一文件" }));

    expect(await screen.findByTestId("route")).toHaveTextContent(
      "/cases/case_pending",
    );
    expect(service.calls[0]?.url).toBe("/api/intakes/intake_pending");
    const post = service.calls.find((call) => call.method === "POST");
    expect(post?.key).toBe("key-pending-1");
    expect(post?.form?.get("file")).toBe(file);
    expect(post?.form?.get("title")).toBe("专线采购合同");
  });

  it("requires an explicit choice when a different file is selected for a pending operation", async () => {
    await seedContinuation(clerk, { receipt: pendingReceipt });
    const service = new IntakeService();
    const { user } = openWithService(service);
    renderIntake(clerk);
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "继续上传同一文件" }),
      ).toBeEnabled(),
    );
    const different = new File(["different contract"], "专线采购合同.txt", {
      type: "text/plain",
    });
    await user.upload(screen.getByLabelText("重新选择主合同文件"), different);
    await user.click(screen.getByRole("button", { name: "继续上传同一文件" }));

    expect(
      await screen.findByText("所选文件与最初提交的主合同不一致"),
    ).toBeInTheDocument();
    expect(service.calls.some((call) => call.method === "POST")).toBe(false);
    await user.click(screen.getByRole("button", { name: "另建一份主合同" }));
    expect(
      screen.getByText("另建一份主合同会清除本机续办记录"),
    ).toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "确认另建主合同" }),
    );

    expect(
      await screen.findByRole("textbox", { name: "案件标题" }),
    ).toHaveValue("");
    expect(await readIntakeContinuation(clerk)).toBeNull();
  });

  it("shows the server's existing operation on a key conflict and never creates a second case", async () => {
    const service = new IntakeService();
    service.post = () =>
      json(
        { error: { code: "conflict", message: "同一幂等键已经绑定其他文件" } },
        409,
      );
    service.getByKey = () => json(pendingResult("case_existing"), 200);
    const { user } = openWithService(service);
    renderIntake(clerk);
    await user.type(
      await screen.findByRole("textbox", { name: "案件标题" }),
      "专线采购合同",
    );
    await user.upload(
      screen.getByLabelText("主合同文件"),
      new File([SOURCE_BYTES], "专线采购合同.txt", { type: "text/plain" }),
    );
    await user.click(
      screen.getByRole("button", { name: "上传主合同并建立案件" }),
    );

    expect(
      await screen.findByText("服务器按幂等键拒绝了这次提交"),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/服务器已有该幂等键对应的操作/),
    ).toHaveTextContent("case_existing");
    expect(
      screen.getByRole("link", { name: "打开已有案件" }),
    ).toHaveAttribute("href", "/cases/case_existing");
    expect(service.calls.filter((call) => call.method === "POST")).toHaveLength(
      1,
    );
    expect(await readIntakeContinuation(clerk)).not.toBeNull();
  });

  it("restores a completed receipt by operation id after reload", async () => {
    await seedContinuation(clerk, { receipt: completedReceipt });
    const service = new IntakeService();
    service.getById = (operationId) => {
      expect(operationId).toBe("intake_done");
      return json(
        completedResult("case_done", {
          operation_id: "intake_done",
          document_id: "doc_done",
          material_id: "mat_done",
        }),
        200,
      );
    };
    openWithService(service);
    renderIntake(clerk);

    expect(
      await screen.findByText("主合同已上传，案件已建立"),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(service.calls[0]?.url).toBe("/api/intakes/intake_done"),
    );
    const receipt = within(screen.getByLabelText("主合同上传回执"));
    expect(receipt.getByText("intake_done")).toBeInTheDocument();
    expect(receipt.getByText("doc_done")).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "打开案件与准备清单" }),
    ).toHaveAttribute("href", "/cases/case_done");
  });

  it("never reads another actor's continuation record", async () => {
    await seedContinuation(clerk, { receipt: pendingReceipt });
    const service = new IntakeService();
    openWithService(service);
    renderIntake(otherTenant);

    expect(
      await screen.findByRole("textbox", { name: "案件标题" }),
    ).toBeInTheDocument();
    expect(service.calls).toHaveLength(0);
    expect(await readIntakeContinuation(clerk)).not.toBeNull();
  });

  it("keeps the local receipt and explains an authorisation loss when the case read is denied", async () => {
    await seedContinuation(clerk, { receipt: completedReceipt });
    const service = new IntakeService();
    service.getById = () =>
      json(
        { error: { code: "forbidden", message: "没有访问该内容的权限" } },
        403,
      );
    openWithService(service);
    renderIntake(clerk);

    expect(
      await screen.findByText("当前身份没有权限继续该操作"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("主合同已上传，案件已建立"),
    ).toBeInTheDocument();
    expect(await readIntakeContinuation(clerk)).not.toBeNull();
  });

  it("keeps the upload entry closed for roles without case-creation permission", async () => {
    const service = new IntakeService();
    openWithService(service);
    renderIntake(reviewer);

    expect(
      await screen.findByText(
        "当前身份可以查看案件，不能新建案件或上传主合同",
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("textbox", { name: "案件标题" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByLabelText("主合同文件")).not.toBeInTheDocument();
    expect(service.calls).toHaveLength(0);
  });
});
