import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { App } from "../app";
import ApiDocsPage from "./api-docs-page";
import { queryKeys } from "../api/queries";

const contract = {
  openapi: "3.1.1",
  info: { title: "合同审查 API", version: "0.3.0", description: "服务端 OpenAPI 测试契约" },
  tags: [{ name: "案件", description: "案件操作" }],
  components: { schemas: { CreateCaseInput: { type: "object", required: ["title"], properties: { title: { type: "string" } } } } },
  paths: {
    "/api/cases/{caseID}": {
      get: {
        operationId: "getCase", summary: "读取案件", description: "按授权读取案件详情。", tags: ["案件"],
        parameters: [{ name: "caseID", in: "path", required: true, description: "案件 ID", schema: { type: "string" } }],
        responses: { "200": { description: "案件详情" }, "403": { description: "权限不足" } },
      },
    },
    "/api/cases": {
      post: {
        operationId: "createCase", summary: "创建案件", tags: ["案件"],
        requestBody: { required: true, content: { "application/json": { schema: { $ref: "#/components/schemas/CreateCaseInput" } } } },
        responses: { "201": { description: "案件已创建" } },
      },
    },
    "/api/cases/{caseID}/documents": {
      post: {
        operationId: "uploadDocument", summary: "上传合同材料", tags: ["案件"],
        parameters: [{ name: "caseID", in: "path", required: true, schema: { type: "string" } }],
        requestBody: { required: true, content: { "multipart/form-data": { schema: { type: "object", required: ["role", "file"], properties: { role: { type: "string", enum: ["procurement", "award", "draft_contract", "attachment"] }, file: { type: "string", format: "binary" } } } } } },
        responses: { "201": { description: "原件已登记" } },
      },
    },
  },
};
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), {
  status, headers: { "Content-Type": "application/json" },
});

const clients: QueryClient[] = [];

function renderApiDocsPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  client.setQueryData(queryKeys.session, { id: "u1", display_name: "测试审查员", role: "clerk", tenant_id: "tenant-test", org_id: "org-test", audience: "client" });
  clients.push(client);
  return render(<QueryClientProvider client={client}><ApiDocsPage /></QueryClientProvider>);
}
beforeEach(() => vi.clearAllMocks());
afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  vi.unstubAllGlobals();
});

test("loads the local OpenAPI contract and displays real route metadata", async () => {
  const fetcher = vi.fn().mockResolvedValue(response(contract));
  vi.stubGlobal("fetch", fetcher);
  renderApiDocsPage();
  expect(screen.getByRole("status")).toHaveTextContent("正在读取服务端 OpenAPI 契约");
  expect(await screen.findByRole("heading", { name: "读取案件" })).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "文档" }));
  expect(screen.getByRole("link", { name: "下载服务端 OpenAPI JSON" })).toHaveAttribute("href", "/openapi.json");
  expect(fetcher).toHaveBeenCalledOnce();
  expect(fetcher).toHaveBeenCalledWith("/openapi.json", expect.objectContaining({ credentials: "same-origin", cache: "no-store" }));
});

test("sends a filled same-origin request and shows the unmodified response", async () => {
  const fetcher = vi.fn()
    .mockResolvedValueOnce(response(contract))
    .mockResolvedValueOnce(new Response('{"id":"c-17","title":"真实返回"}', { status: 200, statusText: "OK", headers: { "Content-Type": "application/json" } }));
  vi.stubGlobal("fetch", fetcher);
  renderApiDocsPage();
  await screen.findByRole("heading", { name: "读取案件" });
  fireEvent.change(screen.getByRole("textbox", { name: "caseID path" }), { target: { value: "c-17" } });
  fireEvent.click(screen.getByRole("button", { name: "发送真实请求" }));
  expect(await screen.findByText('{"id":"c-17","title":"真实返回"}')).toBeInTheDocument();
  expect(fetcher).toHaveBeenNthCalledWith(2, "/api/cases/c-17", expect.objectContaining({ method: "GET", credentials: "same-origin" }));
});
test("uploads the selected original file as a real multipart request", async () => {
  const fetcher = vi.fn()
    .mockResolvedValueOnce(response(contract))
    .mockResolvedValueOnce(response({ id: "doc-21" }, 201));
  vi.stubGlobal("fetch", fetcher);
  renderApiDocsPage();
  await screen.findByRole("heading", { name: "读取案件" });
  fireEvent.click(screen.getByRole("button", { name: /上传合同材料/ }));
  fireEvent.change(screen.getByRole("textbox", { name: "caseID path" }), { target: { value: "c-21" } });
  fireEvent.change(screen.getByRole("textbox", { name: "请求体 role" }), { target: { value: "draft_contract" } });
  const original = new File(["原件 bytes"], "合同原件.pdf", { type: "application/pdf" });
  await userEvent.upload(screen.getByLabelText("请求体 file"), original);
  fireEvent.click(screen.getByRole("button", { name: "发送真实请求" }));
  expect(await screen.findByText('{"id":"doc-21"}')).toBeInTheDocument();
  expect(fetcher).toHaveBeenNthCalledWith(2, "/api/cases/c-21/documents", expect.objectContaining({ method: "POST", credentials: "same-origin" }));
  const requestInit = fetcher.mock.calls[1]?.[1] as RequestInit | undefined;
  expect(requestInit?.body).toBeInstanceOf(FormData);
  if (!(requestInit?.body instanceof FormData)) throw new Error("请求体不是 multipart FormData");
  expect(requestInit.body.get("role")).toBe("draft_contract");
  const sentFile = requestInit.body.get("file");
  if (!(sentFile instanceof File)) throw new Error("上传请求未保留文件原件");
  expect(sentFile.name).toBe("合同原件.pdf");
  expect(await sentFile.text()).toBe("原件 bytes");
  expect(new Headers(requestInit.headers).has("Content-Type")).toBe(false);
});

test("validates the required JSON body before sending an operation", async () => {
  const fetcher = vi.fn().mockResolvedValueOnce(response(contract)).mockResolvedValueOnce(response({ id: "c-18" }, 201));
  vi.stubGlobal("fetch", fetcher);
  renderApiDocsPage();
  await screen.findByRole("heading", { name: "读取案件" });
  fireEvent.click(screen.getByRole("button", { name: /创建案件/ }));
  expect(screen.getByRole("button", { name: "发送真实请求" })).toBeDisabled();
  fireEvent.change(screen.getByRole("textbox", { name: "真实请求体" }), { target: { value: "{}" } });
  expect(screen.getByRole("alert")).toHaveTextContent("请求体.title 是必填字段");
  fireEvent.change(screen.getByRole("textbox", { name: "真实请求体" }), { target: { value: JSON.stringify({ title: "真实案件" }) } });
  fireEvent.click(screen.getByRole("button", { name: "发送真实请求" }));
  expect(await screen.findByText('{"id":"c-18"}')).toBeInTheDocument();
  expect(fetcher).toHaveBeenNthCalledWith(2, "/api/cases", expect.objectContaining({
    method: "POST", credentials: "same-origin", body: JSON.stringify({ title: "真实案件" }),
    headers: expect.objectContaining({ "Content-Type": "application/json" }),
  }));
});

test("HTTP error remains visible and explicit contract reload recovers", async () => {
  const fetcher = vi.fn().mockResolvedValueOnce(response({}, 503)).mockResolvedValueOnce(response(contract));
  vi.stubGlobal("fetch", fetcher);
  renderApiDocsPage();
  expect(await screen.findByText("接口契约加载失败（HTTP 503）")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "重新加载" }));
  expect(await screen.findByRole("heading", { name: "读取案件" })).toBeInTheDocument();
  expect(fetcher).toHaveBeenCalledTimes(2);
});

test.each([
  null,
  { ...contract, openapi: "2.0" },
  { ...contract, info: {} },
  { ...contract, paths: [] },
  { ...contract, paths: null },
  { ...contract, paths: { "/api/cases": { get: { operationId: "missingResponses" } } } },
])("rejects invalid contract data instead of rendering misleading routes (%j)", async (value) => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(value)));
  renderApiDocsPage();
  expect(await screen.findByRole("alert")).toHaveTextContent("格式不正确");
});

test.each([new Error("网络连接失败"), "network failure"])("reports network failure (%s)", async (failure) => {
  vi.stubGlobal("fetch", vi.fn().mockRejectedValue(failure));
  renderApiDocsPage();
  expect(await screen.findByRole("alert")).toHaveTextContent(failure instanceof Error ? failure.message : "接口契约加载失败");
});

test("aborts a pending contract read when leaving the page", async () => {
  const pending = Promise.withResolvers<Response>();
  const fetcher = vi.fn().mockReturnValue(pending.promise);
  vi.stubGlobal("fetch", fetcher);
  const mounted = renderApiDocsPage();
  const signal = fetcher.mock.calls[0]?.[1].signal as AbortSignal;
  mounted.unmount();
  expect(signal.aborted).toBe(true);
  await act(async () => { pending.resolve(response(contract)); await pending.promise; });
  expect(screen.queryByRole("heading", { name: "读取案件" })).not.toBeInTheDocument();
});

test("API docs route stays session protected and loads the service contract", async () => {
  const fetcher = vi.fn().mockImplementation((path: string) => Promise.resolve(response(
    path === "/api/session" ? { actor: { id: "u1", display_name: "只读用户", role: "viewer", tenant_id: "tenant-test", org_id: "org-test" }, audience: "client" } : contract,
  )));
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  clients.push(client);
  render(<QueryClientProvider client={client}><MemoryRouter initialEntries={["/api-docs"]}><App /></MemoryRouter></QueryClientProvider>);
  expect(await screen.findByRole("heading", { name: "读取案件" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "API 参考" })).toHaveAttribute("aria-current", "page");
  expect(fetcher).toHaveBeenCalledWith("/openapi.json", expect.objectContaining({ credentials: "same-origin" }));
});
