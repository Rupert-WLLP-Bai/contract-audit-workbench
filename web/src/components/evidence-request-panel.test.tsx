import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { UserEvent } from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setSessionIdentity } from "../api/http";
import { queryKeys } from "../api/queries";
import type { EvidenceQuote } from "../api/evidence-workflow";
import type { BusinessAgentReview } from "../api/business-agent-review";
import type { Actor, CaseDetail, ConstraintEvaluation, ExecutionAttempt, ReviewDocument, ReviewWorkspace, WorkbenchTask } from "../api/types";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { EvidenceRequestPanel } from "./evidence-request-panel";

const at = "2026-10-21T06:00:00Z";
const clients: QueryClient[] = [];
const actor: Actor = { id: "owner", display_name: "审查人", role: "admin", tenant_id: "tenant", org_id: "org", audience: "client" };
const document: ReviewDocument = {
  id: "doc-2", role: "draft_contract", filename: "补充合同.txt", media_type: "text/plain", digest: "digest",
  size: 20, parse_status: "ready", parse_error: null, created_at: at, parsed_document: null,
  parsed_document_id: "parse-2", text: "😀质保36个月。", clauses: [], superseded: false,
};
const detail: CaseDetail = {
  id: "case-1", title: "测试案件", contract_type: "service", created_at: at, updated_at: at,
  documents: [document], members: [{ id: "owner", display_name: "审查人", role: "admin", tenant_id: "tenant", org_id: "org" }],
  latest_run: null, finding_counts: null, activity: [], business_line: "ENT", scenario: "telecom_dedicated_line",
  business_status: "active", generations: [], remediations: [], reports: [], archives: [], archived_at: null,
};
const missing: ConstraintEvaluation = {
  id: "eval-old", domain: "commercial", relation: "insufficient", rule_version: "1", reason: "缺少质保期限",
  rule_id: "warranty-rule", capability_id: "warranty_months", object_refs: [], finding_id: null, reason_code: "required_fact_missing", fact_ids: [],
};
const compatible: ConstraintEvaluation = { ...missing, id: "eval-new", relation: "compatible", reason: "质保期限一致" };

function makeAttempt(runId: string, id: string): ExecutionAttempt {
  return {
    id, run_id: runId, ordinal: 1, retry_of_attempt_id: null, state: "completed", accepted_at: at, started_at: at,
    finished_at: at, parsed_document_ids: ["parse-2"], input_hash: null, output_hash: null, error_code: null, history_kind: "frozen",
  };
}

function makeWorkspace(runId: string, attemptId: string, evaluations: ConstraintEvaluation[]): ReviewWorkspace {
  const attempt = makeAttempt(runId, attemptId);
  return {
    run: { id: runId, case_id: "case-1", state: "completed", updated_at: at, started_at: at, model_ref: null, partial_reason: null, failure_message: null },
    documents: [document], findings: [], review_attempts: { attempt, attempts: [attempt] }, execution: null, evaluations, audit_events: [],
  };
}

function rawRequest(status: string, version: number) {
  return {
    id: "request-1", case_id: "case-1", task_id: "task-1", task_version: 1, assignee_id: "owner", due_at: at,
    source_run_id: "run-old", source_attempt_id: "attempt-old", question: "请补充质保期限", material_role: "draft_contract",
    verification_condition: "compatible", status, version, created_by: "owner", created_at: at, updated_at: at,
    source_evaluation: { ...missing, run_id: "run-old", attempt_id: "attempt-old", case_id: "case-1" },
  };
}

const rawReceipt = { id: "receipt-1", request_id: "request-1", case_id: "case-1", document_id: "doc-2", submitted_by: "owner", created_at: at };
type ReceiptPayload = Omit<typeof rawReceipt, "document_id"> & { document_id?: string; answer?: string };

type Call = { pathname: string; method: string; init?: RequestInit };
type ResponseFor = (call: Call) => Response | Promise<Response>;

async function openActiveListbox(user: UserEvent, control: HTMLElement) {
  const existing = new Set(screen.queryAllByRole("listbox", { hidden: true }));
  await user.click(control);
  return waitFor(() => {
    expect(control).toHaveAttribute("aria-expanded", "true");
    const opened = screen.getAllByRole("listbox").filter((element) => !existing.has(element));
    expect(opened).toHaveLength(1);
    return opened[0]!;
  });
}

async function chooseActiveOption(user: UserEvent, control: HTMLElement, name: string | RegExp) {
  const listbox = await openActiveListbox(user, control);
  await user.click(await within(listbox).findByRole("option", { name }));
}

function Location() {
  const location = useLocation();
  return <output aria-label="当前位置">{location.pathname}{location.search}</output>;
}

function open({
  runId = "run-old", attemptId = "attempt-old", urlAttempt = attemptId, evaluations = [missing], agentReview = null,
  respond, canManage = true, tasks, sessionActor = actor, caseDetail = detail,
  workspace,
}: {
  runId?: string;
  attemptId?: string;
  urlAttempt?: string | null;
  evaluations?: ConstraintEvaluation[];
  agentReview?: BusinessAgentReview | null;
  respond: ResponseFor;
  canManage?: boolean;
  tasks?: WorkbenchTask[];
  sessionActor?: Actor | null;
  caseDetail?: CaseDetail | null;
  workspace?: ReviewWorkspace | null;
}) {
  const calls: Call[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const call = { pathname: new URL(String(input), "http://example.test").pathname, method: init?.method ?? "GET", init };
    calls.push(call);
    return respond(call);
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
  clients.push(client);
  if (caseDetail !== null) client.setQueryData(queryKeys.case("case-1"), caseDetail);
  if (sessionActor !== null) client.setQueryData(queryKeys.session, sessionActor);
  if (workspace !== null) client.setQueryData(queryKeys.review("case-1", runId, urlAttempt), workspace ?? makeWorkspace(runId, attemptId, evaluations));
  if (tasks) client.setQueryData(queryKeys.workbench("week"), { tasks });
  const path = `/cases/case-1/reviews/${runId}${urlAttempt ? `?attempt_id=${urlAttempt}` : ""}`;
  render(<QueryClientProvider client={client}><MemoryRouter initialEntries={[path]}>
    <EvidenceRequestPanel caseId="case-1" runId={runId} attemptId={attemptId} canManage={canManage}
      evaluations={evaluations} agentReview={agentReview} documents={caseDetail?.documents ?? []} />
    <Location />
  </MemoryRouter></QueryClientProvider>);
  return { calls, user: userEvent.setup(), client };
}

async function selectVerificationEvidence(user: UserEvent, receiptName: string | RegExp = /补充合同.txt/) {
  await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择提交回执" }), receiptName);
  await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择本次审查材料" }), /补充合同.txt/);
  const original = screen.getByLabelText("本次审查使用的原文") as HTMLTextAreaElement;
  await user.click(original);
  original.setSelectionRange(2, 8);
  fireEvent.select(original);
  await user.type(screen.getByLabelText("核验理由"), "核对质保");
}

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  setSessionIdentity(null);
  vi.unstubAllGlobals();
});

describe("补充材料请求", () => {
  it("creates a task from an observed missing evaluation and retries a failed request without duplicating the task", async () => {
    let creates = 0;
    const { calls, user } = open({ urlAttempt: null, respond(call) {
      if (call.pathname.endsWith("/tasks")) return Response.json({
        id: "task-1", case_id: "case-1", title: "补充质保", kind: "review", assignee_id: "owner", due_at: at,
        status: "open", priority: "high", finding_id: null, version: 1, created_by: "owner", created_at: at, updated_at: at,
      });
      if (call.pathname.endsWith("/evidence-requests") && call.method === "POST") {
        creates++;
        if (creates === 1) return Response.json({ error: { message: "请求暂时不可用", code: "unavailable", retryable: true } }, { status: 503 });
        return Response.json(rawRequest("open", 1));
      }
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: creates > 1 ? [rawRequest("open", 1)] : [] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await chooseOption(user, screen.getByRole("combobox", { name: "缺失检查" }), /缺少质保期限/);
    await user.type(screen.getByLabelText("任务名称"), "补充质保");
    await chooseOption(user, screen.getByRole("combobox", { name: "责任人" }), /审查人/);
    fireEvent.change(screen.getByLabelText("完成期限（本地时间）"), { target: { value: "2026-11-01T10:00" } });
    await user.type(screen.getByLabelText("需要补充的问题"), "请补充质保期限");
    await user.click(screen.getByRole("button", { name: "创建任务并发起请求" }));
    expect(await screen.findByText("请求暂时不可用")).toBeInTheDocument();
    expect(screen.getByText(/已关联任务/)).toBeInTheDocument();
    const retry = screen.getByRole("button", { name: "重试创建补充请求" });
    await waitFor(() => expect(retry).toBeEnabled());
    await user.click(retry);
    await screen.findByText("补充材料请求已创建。");
    const taskCalls = calls.filter((call) => call.pathname.endsWith("/tasks"));
    const requestCalls = calls.filter((call) => call.pathname.endsWith("/evidence-requests") && call.method === "POST");
    expect(taskCalls).toHaveLength(1);
    expect(requestCalls).toHaveLength(2);
    expect(JSON.parse(String(requestCalls[0]?.init?.body))).toMatchObject({ task_id: "task-1", task_version: 1, source_run_id: "run-old", source_attempt_id: "attempt-old", source_evaluation_id: "eval-old" });
    expect(JSON.parse(String(requestCalls[0]?.init?.body))).not.toHaveProperty("source_gap_id");
    expect(new Headers(requestCalls[0]?.init?.headers).get("Idempotency-Key")).toBe(new Headers(requestCalls[1]?.init?.headers).get("Idempotency-Key"));
    expect(calls.some((call) => call.pathname.includes("/reviews/"))).toBe(false);
  });

  it("binds an observed agent gap to an existing case task", async () => {
    const gap = { gap_id: "gap-observed", domain: "commercial", capability_id: "warranty_months", mandatory: true, status: "unknown", reason_code: "required_source_or_field_missing", evidence_ids: null };
    const review: BusinessAgentReview = { attempt_id: "attempt-old", status: "partial", rulepack: null, scope: null,
      domains: null, gaps: [gap], evidence: null, unresolved_gap_ids: [gap.gap_id] };
    const task: WorkbenchTask = { id: "existing-task", case_id: "case-1", title: "现有质保任务", kind: "review",
      assignee_id: "owner", due_at: at, status: "open", priority: "high", finding_id: null, version: 2,
      created_at: at, created_by: "owner", updated_at: at };
    let created = false;
    const { calls, user } = open({ evaluations: [], agentReview: review, tasks: [task], respond(call) {
      const request = { ...rawRequest("open", 1), task_id: task.id, task_version: task.version,
        source_evaluation: undefined, source_gap: gap, verification_condition: "resolved" };
      if (call.pathname.endsWith("/evidence-requests") && call.method === "POST") { created = true; return Response.json(request); }
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: created ? [request] : [] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await chooseOption(user, screen.getByRole("combobox", { name: "缺失检查" }), /审查缺口 1/);
    await chooseOption(user, screen.getByRole("combobox", { name: "关联任务" }), "选择已有任务");
    await chooseOption(user, screen.getByRole("combobox", { name: "选择已有任务" }), /现有质保任务/);
    await user.type(screen.getByLabelText("需要补充的问题"), "请补充质保期限");
    await user.click(screen.getByRole("button", { name: "发起补充请求" }));
    await screen.findByText("补充材料请求已创建。");
    const requestCall = calls.find((call) => call.pathname.endsWith("/evidence-requests") && call.method === "POST");
    const body = JSON.parse(String(requestCall?.init?.body)) as Record<string, unknown>;
    expect(body).toMatchObject({ task_id: "existing-task", task_version: 2, source_gap_id: "gap-observed", verification_condition: "resolved" });
    expect(body).not.toHaveProperty("source_evaluation_id");
    expect(calls.some((call) => call.pathname.endsWith("/tasks"))).toBe(false);
  });

  it("uploads and explicitly submits a receipt, then starts a new review with the chosen business time", async () => {
    let request = rawRequest("open", 1);
    let received = false;
    const { calls, user } = open({ respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: received ? [
        { type: "received", actor_id: "owner", occurred_at: at, request, receipt: rawReceipt },
      ] : [] });
      if (call.pathname.endsWith("/documents") && call.method === "POST") return Response.json({ id: "doc-2" });
      if (call.pathname.endsWith("/receipts") && call.method === "POST") {
        received = true;
        request = rawRequest("received", 2);
        return Response.json({ request, receipt: rawReceipt });
      }
      if (call.pathname.endsWith("/reviews") && call.method === "POST") return Response.json({ id: "run-new", attempt_id: "attempt-new" });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText("待提交");
    expect(screen.getByRole("button", { name: "上传补充文件" })).toBeDisabled();
    await user.upload(screen.getByLabelText("上传补充文件"), new File(["质保36个月"], "补充合同.txt", { type: "text/plain" }));
    await user.click(screen.getByRole("button", { name: "上传补充文件" }));
    await screen.findByText(/已上传 补充合同.txt/);
    const documentOptions = await openActiveListbox(user, screen.getByRole("combobox", { name: "选择案件材料" }));
    expect(within(documentOptions).getAllByRole("option", { name: /补充合同\.txt/ })).toHaveLength(1);
    await user.keyboard("{Escape}");
    expect(calls.filter((call) => call.pathname.endsWith("/receipts"))).toHaveLength(0);
    expect(screen.getByText("待提交")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "提交回执" }));
    await screen.findByText("已提交，待复核");
    const receiptCall = calls.find((call) => call.pathname.endsWith("/receipts"));
    expect(JSON.parse(String(receiptCall?.init?.body))).toEqual({ document_id: "doc-2", expected_version: 1 });
    expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(false);
    fireEvent.change(screen.getByLabelText("审查业务时间（本地时间）"), { target: { value: "2026-11-01T10:00" } });
    await user.click(screen.getByRole("button", { name: "发起后续审查" }));
    await waitFor(() => expect(screen.getByLabelText("当前位置")).toHaveTextContent("/cases/case-1/reviews/run-new?attempt_id=attempt-new"));
    const start = calls.find((call) => call.pathname.endsWith("/reviews") && call.method === "POST");
    expect(JSON.parse(String(start?.init?.body))).toEqual({ business_at: new Date("2026-11-01T10:00").toISOString() });
  });

  it("adds a newly uploaded document to receipt choices and submits its ID", async () => {
    const emptyCase = { ...detail, documents: [] };
    const { calls, user } = open({ caseDetail: emptyCase, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [rawRequest("open", 1)] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [] });
      if (call.pathname.endsWith("/documents") && call.method === "POST") return Response.json({ id: "doc-new" });
      if (call.pathname.endsWith("/receipts") && call.method === "POST") return Response.json({
        request: rawRequest("received", 2), receipt: { ...rawReceipt, document_id: "doc-new" },
      });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });

    await screen.findByText("待提交");
    expect(screen.getByRole("button", { name: "上传补充文件" })).toBeDisabled();
    expect(screen.getByText("当前没有符合材料类型的案件材料。")).toBeInTheDocument();
    await user.upload(screen.getByLabelText("上传补充文件"), new File(["新材料"], "新版本合同.txt", { type: "text/plain" }));
    await user.click(screen.getByRole("button", { name: "上传补充文件" }));
    await screen.findByText(/已上传 新版本合同.txt/);
    const options = await openActiveListbox(user, screen.getByRole("combobox", { name: "选择案件材料" }));
    expect(within(options).getByRole("option", { name: /新版本合同\.txt/ })).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", { name: "提交回执" }));
    const receiptCall = calls.find((call) => call.pathname.endsWith("/receipts"));
    expect(JSON.parse(String(receiptCall?.init?.body))).toEqual({ document_id: "doc-new", expected_version: 1 });
  });

  it("keeps file upload disabled until an untyped request has an upload role", async () => {
    const request = { ...rawRequest("open", 1), material_role: null };
    const { calls, user } = open({ respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });

    await screen.findByText("待提交");
    await user.upload(screen.getByLabelText("上传补充文件"), new File(["新材料"], "新材料.txt", { type: "text/plain" }));
    expect(screen.getByRole("button", { name: "上传补充文件" })).toBeDisabled();
    expect(calls.some((call) => call.pathname.endsWith("/documents"))).toBe(false);
  });

  it("submits a written answer as a receipt without treating it as verified", async () => {
    let received = false;
    const answerReceipt = { ...rawReceipt, document_id: undefined, answer: "质保36个月" };
    const { calls, user } = open({ respond(call) {
      const request = rawRequest(received ? "received" : "open", received ? 2 : 1);
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: received ? [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt: answerReceipt }] : [] });
      if (call.pathname.endsWith("/receipts") && call.method === "POST") {
        received = true;
        return Response.json({ request: rawRequest("received", 2), receipt: answerReceipt });
      }
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText("待提交");
    await chooseOption(user, screen.getByRole("combobox", { name: "提交方式" }), "文字答复");
    await user.type(screen.getByLabelText("文字答复"), "质保36个月");
    await user.click(screen.getByRole("button", { name: "提交回执" }));
    await screen.findByText("已提交，待复核");
    const receiptCall = calls.find((call) => call.pathname.endsWith("/receipts"));
    expect(JSON.parse(String(receiptCall?.init?.body))).toEqual({ answer: "质保36个月", expected_version: 1 });
    expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(false);
  });

  it("disables receipt submission for a missing document or blank answer", async () => {
    const { calls, user } = open({ respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [rawRequest("open", 1)] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });

    await screen.findByText("待提交");
    const submit = screen.getByRole("button", { name: "提交回执" });
    expect(submit).toBeDisabled();
    await chooseOption(user, screen.getByRole("combobox", { name: "提交方式" }), "文字答复");
    await user.type(screen.getByLabelText("文字答复"), "   ");
    expect(submit).toBeDisabled();
    expect(calls.some((call) => call.pathname.endsWith("/receipts"))).toBe(false);
  });

  it("shows missing material names without exposing their identifiers", async () => {
    open({ canManage: false, respond(call) {
      const request = rawRequest("received", 2);
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "unknown-actor", occurred_at: at,
        request, receipt: { ...rawReceipt, document_id: "unknown-document" } }] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    const panel = screen.getByRole("region", { name: "补充材料请求" });
    expect((await within(panel).findAllByText(/材料名称未报告/)).length).toBeGreaterThan(0);
    expect(within(panel).queryByText("处理记录中未报告回执。")).not.toBeInTheDocument();
    expect(within(panel).getByText("已提交，待复核").closest(".ant-tag")).toHaveClass("ant-tag-warning");
    expect(within(panel).getByText("下一步：提交回执后发起新审查，再根据新审查的检查结果和原文核验。上传或提交材料本身不代表已核验。")).toBeInTheDocument();
    expect(within(panel).queryByText(/unknown-document|unknown-actor|run-old|attempt-old/)).not.toBeInTheDocument();
    expect(within(panel).getByRole("link", { name: "查看来源审查" })).toHaveAttribute("href", "/cases/case-1/reviews/run-old?attempt_id=attempt-old");
  });

  it("offers only current documents of the requested material role", async () => {
    const validDocument = { ...document, id: "doc-valid", filename: "符合类型.txt", role: "draft_contract", superseded: false };
    const supersededDocument = { ...document, id: "doc-old", filename: "已替代.txt", role: "draft_contract", superseded: true };
    const wrongRoleDocument = { ...document, id: "doc-award", filename: "中标文件.txt", role: "award", superseded: false };
    const caseDetail = { ...detail, documents: [validDocument, supersededDocument, wrongRoleDocument] };
    const { user } = open({ canManage: false, caseDetail, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [{ ...rawRequest("open", 1), material_role: "draft_contract" }] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });

    await screen.findByText("待提交");
    const listbox = await openActiveListbox(user, screen.getByRole("combobox", { name: "选择案件材料" }));
    expect(within(listbox).getByRole("option", { name: /符合类型\.txt/ })).toBeInTheDocument();
    expect(within(listbox).queryByRole("option", { name: /已替代\.txt|中标文件\.txt/ })).not.toBeInTheDocument();
  });

  it("allows any current document when the request has no material role", async () => {
    const currentDraft = { ...document, id: "doc-draft", filename: "合同.txt", role: "draft_contract", superseded: false };
    const currentAward = { ...document, id: "doc-award", filename: "中标.txt", role: "award", superseded: false };
    const oldDocument = { ...document, id: "doc-old", filename: "旧版.txt", role: "draft_contract", superseded: true };
    const caseDetail = { ...detail, documents: [currentDraft, currentAward, oldDocument] };
    const { user } = open({ canManage: false, caseDetail, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [{ ...rawRequest("open", 1), material_role: null }] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });

    await screen.findByText("待提交");
    const listbox = await openActiveListbox(user, screen.getByRole("combobox", { name: "选择案件材料" }));
    expect(within(listbox).getByRole("option", { name: /合同\.txt/ })).toBeInTheDocument();
    expect(within(listbox).getByRole("option", { name: /中标\.txt/ })).toBeInTheDocument();
    expect(within(listbox).queryByRole("option", { name: /旧版\.txt/ })).not.toBeInTheDocument();
  });

  it("uses a selected frozen quotation and actual later check, and preserves a server refusal", async () => {
    let verified = false;
    let verificationAttempts = 0;
    const { calls, user } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [compatible], canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [verified ? {
        ...rawRequest("verified", 3), verification: { run_id: "run-new", attempt_id: "attempt-new", receipt_id: "receipt-1", evaluation_id: "eval-new",
          relation: "compatible", evidence: [{ document_id: "doc-2", parsed_document_id: "parse-2", quote: "质保36个月", start: 1, end: 7 }],
          reason: "核对质保", verified_by: "owner", verified_at: at },
      } : rawRequest("received", 2)] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request: rawRequest("received", 2), receipt: rawReceipt }] });
      if (call.pathname.endsWith("/verify")) {
        verificationAttempts++;
        if (verificationAttempts === 1) return Response.json({ error: { message: "复核检查没有使用本次提交的可定位材料或答复", code: "conflict" } }, { status: 409 });
        verified = true;
        return Response.json({ ...rawRequest("verified", 3), verification: { run_id: "run-new", attempt_id: "attempt-new", receipt_id: "receipt-1", evaluation_id: "eval-new", relation: "compatible", evidence: [{ document_id: "doc-2", parsed_document_id: "parse-2", quote: "质保36个月", start: 1, end: 7 }], reason: "核对质保", verified_by: "owner", verified_at: at } });
      }
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText("已提交，待复核");
    await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择提交回执" }), /补充合同.txt/);
    await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择本次审查材料" }), /补充合同.txt/);
    const original = screen.getByLabelText("本次审查使用的原文") as HTMLTextAreaElement;
    await user.click(original);
    original.setSelectionRange(2, 8);
    fireEvent.select(original);
    await user.type(screen.getByLabelText("核验理由"), "核对质保");
    await user.click(screen.getByRole("button", { name: "提交核验" }));
    expect(await screen.findByText("复核检查没有使用本次提交的可定位材料或答复")).toBeInTheDocument();
    expect(screen.getByText("已提交，待复核")).toBeInTheDocument();
    const retry = screen.getByRole("button", { name: "提交核验" });
    await waitFor(() => expect(retry).toBeEnabled());
    await user.click(retry);
    await screen.findByText("已核验");
    const verifications = calls.filter((call) => call.pathname.endsWith("/verify"));
    expect(verifications).toHaveLength(2);
    expect(JSON.parse(String(verifications[0]?.init?.body))).toEqual({ run_id: "run-new", attempt_id: "attempt-new", evaluation_id: "eval-new", evidence: [{ document_id: "doc-2", parsed_document_id: "parse-2", quote: "质保36个月", start: 1, end: 7 }], reason: "核对质保", expected_version: 2 });
    expect(new Headers(verifications[0]?.init?.headers).get("Idempotency-Key")).toBe(new Headers(verifications[1]?.init?.headers).get("Idempotency-Key"));
  });

  it("does not allow an unresolved gap to be verified", async () => {
    const gap = { gap_id: "gap-1", domain: "commercial", capability_id: "warranty_months", mandatory: true, status: "resolved", reason_code: "", evidence_ids: ["doc-2"] };
    const review: BusinessAgentReview = {
      attempt_id: "attempt-new", status: "partial", rulepack: null, scope: null,
      domains: [{ domain: "commercial", status: "completed", capabilities: [{ capability_id: "warranty_months", mandatory: true, status: "completed", relation: "compatible", reason_code: "", evidence_ids: ["doc-2"], canonical_evaluation_ids: null }] }],
      gaps: [gap], evidence: null, unresolved_gap_ids: ["gap-1"],
    };
    const { calls, user } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [], agentReview: review, canManage: false, respond(call) {
      const gapRequest = { ...rawRequest("received", 2), source_evaluation: undefined, source_gap: { ...gap, status: "unknown" }, verification_condition: "resolved" };
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [gapRequest] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request: gapRequest, receipt: rawReceipt }] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText(/没有唯一匹配且达到请求条件/);
    await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择提交回执" }), /补充合同.txt/);
    await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择本次审查材料" }), /补充合同.txt/);
    const original = screen.getByLabelText("本次审查使用的原文") as HTMLTextAreaElement;
    await user.click(original);
    original.setSelectionRange(2, 8);
    fireEvent.select(original);
    await user.type(screen.getByLabelText("核验理由"), "核对质保");
    expect(screen.getByRole("button", { name: "提交核验" })).toBeDisabled();
    expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(false);
  });

  it.each([
    ["without the source review", null],
    ["against another attempt", {
      attempt_id: "attempt-old", status: "completed", rulepack: null, scope: null,
      domains: [{ domain: "commercial", status: "completed", capabilities: [{ capability_id: "warranty_months", mandatory: true, status: "completed", relation: "compatible", reason_code: "", evidence_ids: ["doc-2"], canonical_evaluation_ids: null }] }],
      gaps: [{ gap_id: "gap-1", domain: "commercial", capability_id: "warranty_months", mandatory: true, status: "resolved", reason_code: "", evidence_ids: ["doc-2"] }], evidence: null, unresolved_gap_ids: [],
    }],
    ["with a duplicate source gap", {
      attempt_id: "attempt-new", status: "completed", rulepack: null, scope: null,
      domains: [{ domain: "commercial", status: "completed", capabilities: [{ capability_id: "warranty_months", mandatory: true, status: "completed", relation: "compatible", reason_code: "", evidence_ids: ["doc-2"], canonical_evaluation_ids: null }] }],
      gaps: [
        { gap_id: "gap-1", domain: "commercial", capability_id: "warranty_months", mandatory: true, status: "resolved", reason_code: "", evidence_ids: ["doc-2"] },
        { gap_id: "gap-1", domain: "commercial", capability_id: "warranty_months", mandatory: true, status: "resolved", reason_code: "", evidence_ids: ["doc-2"] },
      ], evidence: null, unresolved_gap_ids: [],
    }],
    ["without a matching capability", {
      attempt_id: "attempt-new", status: "completed", rulepack: null, scope: null,
      domains: [{ domain: "commercial", status: "completed", capabilities: [] }],
      gaps: [{ gap_id: "gap-1", domain: "commercial", capability_id: "warranty_months", mandatory: true, status: "resolved", reason_code: "", evidence_ids: ["doc-2"] }],
      evidence: null, unresolved_gap_ids: [],
    }],
    ["with duplicate capabilities", {
      attempt_id: "attempt-new", status: "completed", rulepack: null, scope: null,
      domains: [{ domain: "commercial", status: "completed", capabilities: [
        { capability_id: "warranty_months", mandatory: true, status: "completed", relation: "compatible", reason_code: "", evidence_ids: ["doc-2"], canonical_evaluation_ids: null },
        { capability_id: "warranty_months", mandatory: true, status: "completed", relation: "compatible", reason_code: "", evidence_ids: ["doc-2"], canonical_evaluation_ids: null },
      ] }],
      gaps: [{ gap_id: "gap-1", domain: "commercial", capability_id: "warranty_months", mandatory: true, status: "resolved", reason_code: "", evidence_ids: ["doc-2"] }],
      evidence: null, unresolved_gap_ids: [],
    }],
  ] as Array<[string, BusinessAgentReview | null]>) ("does not authorize a resolved gap %s", async (_caseName, agentReview) => {
    const gap = { gap_id: "gap-1", domain: "commercial", capability_id: "warranty_months", mandatory: true, status: "unknown", reason_code: "required_source_or_field_missing", evidence_ids: null };
    const request = { ...rawRequest("received", 2), source_evaluation: undefined, source_gap: gap, verification_condition: "resolved" };
    const { calls, user } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [], agentReview, canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt: rawReceipt }] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText("已提交，待复核");
    await selectVerificationEvidence(user);
    expect(screen.getByRole("button", { name: "提交核验" })).toBeDisabled();
    expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(false);
  });

  it.each([
    ["for the source attempt", { history_kind: "frozen", state: "completed", finished_at: at }, "attempt-new"],
    ["for a live attempt", { history_kind: "live", state: "completed", finished_at: at }, "attempt-old"],
    ["before it is finished", { history_kind: "frozen", state: "running", finished_at: null }, "attempt-old"],
    ["for another run", { history_kind: "frozen", run_id: "run-other", state: "completed", finished_at: at }, "attempt-old"],
  ] as Array<[string, Partial<ExecutionAttempt>, string]>) ("does not authorize verification %s", async (_caseName, attemptOverrides, sourceAttemptId) => {
    const workspace = makeWorkspace("run-new", "attempt-new", [compatible]);
    const current = workspace.review_attempts!.attempt!;
    const attempt = { ...current, ...attemptOverrides };
    workspace.review_attempts = { attempt, attempts: [attempt] };
    const request = { ...rawRequest("received", 2), source_attempt_id: sourceAttemptId };
    const { calls, user } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [compatible], workspace, canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt: rawReceipt }] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText("已提交，待复核");
    await selectVerificationEvidence(user);
    expect(screen.getByRole("button", { name: "提交核验" })).toBeDisabled();
    expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(false);
  });

  it.each([
    ["a different frozen document", { ...rawReceipt, document_id: "other-document" }, "draft_contract"],
    ["a document with the wrong material role", rawReceipt, "award"],
    ["an answer absent from frozen text", { ...rawReceipt, document_id: undefined, answer: "无关答复" }, "draft_contract"],
  ] as Array<[string, ReceiptPayload, string]>) ("does not offer frozen evidence for %s", async (_caseName, receipt, materialRole) => {
    const request = { ...rawRequest("received", 2), material_role: materialRole };
    const { calls, user } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [compatible], canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt }] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText("已提交，待复核");
    const receiptName = receipt.document_id === "other-document" ? /提交材料/ : receipt.answer ? /无关答复/ : /补充合同.txt/;
    await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择提交回执" }), receiptName);
    expect(screen.getByText("当前审查未包含可引用的回执材料或答复原文。" )).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "提交核验" })).toBeDisabled();
    expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(false);
  });

  it("does not match a later check that cites a different set of frozen objects", async () => {
    const request = {
      ...rawRequest("received", 2),
      source_evaluation: {
        ...missing,
        object_refs: ["object-a", "object-b"],
        run_id: "run-old",
        attempt_id: "attempt-old",
        case_id: "case-1",
      },
    };
    const laterEvaluation = {
      ...compatible,
      object_refs: ["object-a", "object-c"],
    };
    const { calls, user } = open({
      runId: "run-new",
      attemptId: "attempt-new",
      evaluations: [laterEvaluation],
      canManage: false,
      respond(call) {
        if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
        if (call.pathname.endsWith("/history")) return Response.json({ items: [
          { type: "received", actor_id: "owner", occurred_at: at, request, receipt: rawReceipt },
        ] });
        return Response.json({ error: { message: "unexpected" } }, { status: 404 });
      },
    });

    await screen.findByText("已提交，待复核");
    await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择提交回执" }), /补充合同.txt/);
    expect(screen.getByText("当前审查没有唯一匹配且达到请求条件的实际检查，仍需补充或复审。")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "提交核验" })).toBeDisabled();
    expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(false);
  });

  it("does not authorize a source evaluation with duplicate matching results", async () => {
    const request = rawRequest("received", 2);
    const duplicate = { ...compatible, id: "eval-new-duplicate" };
    const { calls, user } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [compatible, duplicate], canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt: rawReceipt }] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });

    await screen.findByText("已提交，待复核");
    await selectVerificationEvidence(user);
    expect(screen.getByText("当前审查没有唯一匹配且达到请求条件的实际检查，仍需补充或复审。")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "提交核验" })).toBeDisabled();
    expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(false);
  });

  it("keeps verification disabled when the review workspace is unavailable", async () => {
    const request = rawRequest("received", 2);
    const { calls } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [compatible], workspace: null, canManage: false, respond(call) {
      if (call.pathname.endsWith("/reviews/run-new")) return Response.json({ error: { message: "review unavailable" } }, { status: 404 });
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt: rawReceipt }] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });

    await screen.findByText("已提交，待复核");
    expect(screen.getByRole("button", { name: "提交核验" })).toBeDisabled();
    expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(false);
  });

  it("keeps verification disabled when the workspace has no attempt summary", async () => {
    const request = rawRequest("received", 2);
    const workspace = makeWorkspace("run-new", "attempt-new", [compatible]);
    workspace.review_attempts = null;
    const { calls } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [compatible], workspace, canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt: rawReceipt }] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });

    await screen.findByText("已提交，待复核");
    expect(screen.getByRole("button", { name: "提交核验" })).toBeDisabled();
    expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(false);
  });

  it("lets a clerk submit or upload but never authorise verification", async () => {
    const clerk: Actor = { ...actor, id: "clerk-1", display_name: "案件经办人", role: "clerk" };
    open({
      canManage: false,
      sessionActor: clerk,
      respond(call) {
        const request = { ...rawRequest("received", 2), material_role: null };
        if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
        if (call.pathname.endsWith("/history")) return Response.json({ items: [
          { type: "received", actor_id: clerk.id, occurred_at: at, request, receipt: rawReceipt },
        ] });
        return Response.json({ error: { message: "unexpected" } }, { status: 404 });
      },
    });

    await screen.findByText("已提交，待复核");
    expect(screen.getByLabelText("上传补充文件")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "上传材料类型" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "发起后续审查" })).toBeInTheDocument();
    expect(screen.getByText("当前身份不能提交核验。")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "提交核验" })).toBeDisabled();
    expect(screen.queryByText("如需上传新材料，请由有上传权限的案件成员登记后再选择。")).not.toBeInTheDocument();
  });

  it("does not offer submission or upload controls for an archived case", async () => {
    const archivedDetail = { ...detail, archived_at: "2026-10-22T06:00:00Z" };
    open({
      canManage: false,
      caseDetail: archivedDetail,
      respond(call) {
        if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [rawRequest("open", 1)] });
        if (call.pathname.endsWith("/history")) return Response.json({ items: [] });
        return Response.json({ error: { message: "unexpected" } }, { status: 404 });
      },
    });

    await screen.findByText("待提交");
    expect(screen.queryByRole("button", { name: "提交回执" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("上传补充文件")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "创建任务并发起请求" })).not.toBeInTheDocument();
  });

  it("fails closed when the session identity is unavailable", async () => {
    open({
      canManage: false,
      sessionActor: null,
      respond(call) {
        const request = rawRequest("received", 2);
        if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
        if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt: rawReceipt }] });
        return Response.json({ error: { message: "session unavailable" } }, { status: 401 });
      },
    });

    await screen.findByText("已提交，待复核");
    expect(screen.queryByRole("button", { name: "提交回执" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("上传补充文件")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "提交核验" })).toBeDisabled();
  });

  it("keeps case actions unavailable when case details cannot be loaded", async () => {
    const request = rawRequest("received", 2);
    open({
      canManage: false,
      caseDetail: null,
      respond(call) {
        if (call.pathname.endsWith("/cases/case-1")) return Response.json({ error: { message: "case unavailable" } }, { status: 404 });
        if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
        if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt: rawReceipt }] });
        return Response.json({ error: { message: "unexpected" } }, { status: 404 });
      },
    });

    await screen.findByText("已提交，待复核");
    expect(screen.queryByRole("button", { name: "提交回执" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("上传补充文件")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "提交核验" })).toBeDisabled();
  });

  it("lets a reviewer submit a receipt without exposing upload controls", async () => {
    const reviewer: Actor = { ...actor, id: "reviewer-1", display_name: "复核人", role: "reviewer" };
    open({
      canManage: false,
      sessionActor: reviewer,
      respond(call) {
        const request = rawRequest("open", 1);
        if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
        if (call.pathname.endsWith("/history")) return Response.json({ items: [] });
        return Response.json({ error: { message: "unexpected" } }, { status: 404 });
      },
    });

    await screen.findByText("待提交");
    expect(screen.getByRole("button", { name: "提交回执" })).toBeInTheDocument();
    expect(screen.queryByLabelText("上传补充文件")).not.toBeInTheDocument();
    expect(screen.getByText("如需上传新材料，请由有上传权限的案件成员登记后再选择。")).toBeInTheDocument();
  });

  it("shows safe fallbacks when assignee and source reason are missing", async () => {
    const request = {
      ...rawRequest("open", 1),
      assignee_id: "unlisted-assignee",
      source_evaluation: {
        ...missing,
        reason: "",
        run_id: "run-old",
        attempt_id: "attempt-old",
        case_id: "case-1",
      },
    };
    open({ canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });

    await screen.findByText("待提交");
    expect(screen.getByText("待提交").closest(".ant-tag")).toHaveClass("ant-tag-default");
    expect(screen.getByText("下一步：责任人或案件管理人提交材料或答复。")).toBeInTheDocument();
    expect(screen.getByText(/责任人：姓名未报告；期限：/)).toBeInTheDocument();
    expect(screen.getByText(/来源：规则检查，.*原因未报告。/)).toBeInTheDocument();
    expect(screen.queryByText("unlisted-assignee")).not.toBeInTheDocument();
  });

  it("shows verified evidence and the no-result fallback", async () => {
    const verification = {
      run_id: "run-old",
      attempt_id: "attempt-old",
      receipt_id: "receipt-1",
      evaluation_id: "eval-old",
      relation: null,
      gap_id: null,
      gap_status: null,
      evidence: [{ document_id: "doc-2", parsed_document_id: "parse-2", quote: "质保36个月", start: 1, end: 7 }],
      reason: "复核已完成",
      verified_by: "owner",
      verified_at: at,
    };
    const request = { ...rawRequest("verified", 3), verification };
    open({ canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });

    await screen.findByText("已核验");
    expect(screen.getByText("已核验").closest(".ant-tag")).toHaveClass("ant-tag-success");
    expect(screen.getByText("下一步：查看核验审查与处理记录。")).toBeInTheDocument();
    expect(await screen.findByText("处理记录中未报告回执。")).toBeInTheDocument();
    expect(screen.getByText("核验结果：未报告；复核已完成")).toBeInTheDocument();
    expect((await screen.findAllByText("质保36个月")).length).toBeGreaterThan(0);
    expect(screen.getByRole("link", { name: "查看核验审查" })).toHaveAttribute(
      "href",
      "/cases/case-1/reviews/run-old?attempt_id=attempt-old",
    );
  });

  it("preserves the received request and recovers its history after a read error", async () => {
    let historyReads = 0;
    const { calls, user } = open({ canManage: false, respond(call) {
      const request = rawRequest("received", 2);
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) {
        historyReads++;
        if (historyReads === 1)
          return Response.json({ error: { message: "回执历史暂不可用" } }, { status: 503 });
        return Response.json({ items: [
          { type: "received", actor_id: "owner", occurred_at: at, request, receipt: rawReceipt },
        ] });
      }
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });

    await screen.findByText("已提交，待复核");
    expect(await screen.findByText("回执历史暂不可用")).toBeInTheDocument();
    expect(screen.queryByText("当前审查未包含可引用的回执材料或答复原文。")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "重试读取记录" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试读取记录" }));
    await waitFor(() => expect(historyReads).toBe(2));
    await screen.findByText("处理记录");
    expect(screen.getByText("已提交，待复核")).toBeInTheDocument();
    expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(false);
  });

  it("shows history actors and fallbacks for missing receipt files and written answers", async () => {
    const request = rawRequest("received", 2);
    const unknownDocumentReceipt = { ...rawReceipt, id: "receipt-missing", document_id: "unknown-document" };
    const answerReceipt = { ...rawReceipt, id: "receipt-answer", document_id: undefined, answer: "质保36个月" };
    const { user } = open({ canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [
        { type: "created", actor_id: "system", occurred_at: at, request: { ...request, version: 1 }, receipt: null },
        { type: "received", actor_id: "unknown-actor", occurred_at: at, request: { ...request, version: 2 }, receipt: unknownDocumentReceipt },
        { type: "custom_event", actor_id: "owner", occurred_at: at, request: { ...request, version: 3 }, receipt: answerReceipt },
      ] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });

    await screen.findByText("已提交，待复核");
    await screen.findByText("已提交材料或答复：");
    expect((await screen.findAllByText(/材料名称未报告/)).length).toBeGreaterThan(0);
    await user.click(screen.getByText("处理记录"));
    expect(screen.getByText(/创建请求 · 系统/)).toBeInTheDocument();
    expect(screen.getByText(/提交回执 · 姓名未报告/)).toBeInTheDocument();
    expect(screen.getByText(/custom_event · 审查人/)).toBeInTheDocument();
    expect(screen.getByText("提交材料：材料名称未报告")).toBeInTheDocument();
    expect(screen.getByText("提交答复：质保36个月")).toBeInTheDocument();
  });

  it.each(["conflict", "resolved"] as const)("submits the actual %s check from a later review", async (condition) => {
    const gap = { gap_id: "gap-1", domain: "commercial", capability_id: "warranty_months", mandatory: true, status: "resolved", reason_code: "", evidence_ids: ["doc-2"] };
    const review: BusinessAgentReview = {
      attempt_id: "attempt-new", status: "completed", rulepack: null, scope: null,
      domains: [{ domain: "commercial", status: "completed", capabilities: [{ capability_id: "warranty_months", mandatory: true, status: "completed", relation: "conflict", reason_code: "", evidence_ids: ["doc-2"], canonical_evaluation_ids: null }] }],
      gaps: [gap], evidence: null, unresolved_gap_ids: [],
    };
    const received = condition === "resolved" ? {
      ...rawRequest("received", 2), source_evaluation: undefined,
      source_gap: { ...gap, status: "unknown" }, verification_condition: "resolved",
    } : { ...rawRequest("received", 2), verification_condition: "conflict" };
    const evaluation = { ...compatible, relation: "conflict" };
    let verified = false;
    const { calls, user } = open({
      runId: "run-new", attemptId: "attempt-new", evaluations: condition === "conflict" ? [evaluation] : [],
      agentReview: condition === "resolved" ? review : null, canManage: false, respond(call) {
        const verification = {
          run_id: "run-new", attempt_id: "attempt-new", receipt_id: "receipt-1",
          ...(condition === "resolved" ? { gap_id: "gap-1", gap_status: "resolved" } : { evaluation_id: "eval-new", relation: "conflict" }),
          evidence: [{ document_id: "doc-2", parsed_document_id: "parse-2", quote: "质保36个月", start: 1, end: 7 }],
          reason: "核对质保", verified_by: "owner", verified_at: at,
        };
        const request = verified ? { ...received, status: "verified", version: 3, verification } : received;
        if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
        if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request: received, receipt: rawReceipt }] });
        if (call.pathname.endsWith("/verify")) { verified = true; return Response.json({ ...received, status: "verified", version: 3, verification }); }
        return Response.json({ error: { message: "unexpected" } }, { status: 404 });
      },
    });
    await screen.findByText("已提交，待复核");
    await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择提交回执" }), /补充合同.txt/);
    await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择本次审查材料" }), /补充合同.txt/);
    const original = screen.getByLabelText("本次审查使用的原文") as HTMLTextAreaElement;
    await user.click(original);
    original.setSelectionRange(2, 8);
    fireEvent.select(original);
    await user.type(screen.getByLabelText("核验理由"), "核对质保");
    await user.click(screen.getByRole("button", { name: "提交核验" }));
    await screen.findByText("已核验");
    const verification = calls.find((call) => call.pathname.endsWith("/verify"));
    const body = JSON.parse(String(verification?.init?.body)) as Record<string, unknown>;
    expect(body).toMatchObject({ run_id: "run-new", attempt_id: "attempt-new", expected_version: 2 });
    if (condition === "resolved") {
      expect(body.gap_id).toBe("gap-1");
      expect(body).not.toHaveProperty("evaluation_id");
    } else {
      expect(body.evaluation_id).toBe("eval-new");
      expect(body).not.toHaveProperty("gap_id");
    }
  });

  it.each([
    ["a matching gap with a different domain", { ...rawRequest("received", 2), source_evaluation: undefined,
      source_gap: { gap_id: "gap-1", domain: "technical", capability_id: "warranty_months", mandatory: true, status: "unknown", reason_code: "required_source_or_field_missing", evidence_ids: null }, verification_condition: "resolved" }],
    ["a matching gap with a different capability", { ...rawRequest("received", 2), source_evaluation: undefined,
      source_gap: { gap_id: "gap-1", domain: "commercial", capability_id: "amount", mandatory: true, status: "unknown", reason_code: "required_source_or_field_missing", evidence_ids: null }, verification_condition: "resolved" }],
  ] as const)("does not verify %s", async (_caseName, request) => {
    const gap = { gap_id: "gap-1", domain: "commercial", capability_id: "warranty_months", mandatory: true, status: "resolved", reason_code: "", evidence_ids: ["doc-2"] };
    const review: BusinessAgentReview = {
      attempt_id: "attempt-new", status: "completed", rulepack: null, scope: null,
      domains: [{ domain: "commercial", status: "completed", capabilities: [{ capability_id: "warranty_months", mandatory: true, status: "completed", relation: "compatible", reason_code: "", evidence_ids: ["doc-2"], canonical_evaluation_ids: null }] }],
      gaps: [gap], evidence: null, unresolved_gap_ids: [],
    };
    const { calls, user } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [], agentReview: review, canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt: rawReceipt }] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText("已提交，待复核");
    await selectVerificationEvidence(user);
    expect(screen.getByRole("button", { name: "提交核验" })).toBeDisabled();
    expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(false);
  });

  it("accepts a completed compatible capability as a resolved gap", async () => {
    const gap = { gap_id: "gap-1", domain: "commercial", capability_id: "warranty_months", mandatory: true, status: "resolved", reason_code: "", evidence_ids: ["doc-2"] };
    const review: BusinessAgentReview = {
      attempt_id: "attempt-new", status: "completed", rulepack: null, scope: null,
      domains: [{ domain: "commercial", status: "completed", capabilities: [{ capability_id: "warranty_months", mandatory: true, status: "completed", relation: "compatible", reason_code: "", evidence_ids: ["doc-2"], canonical_evaluation_ids: null }] }],
      gaps: [gap], evidence: null, unresolved_gap_ids: [],
    };
    const request = { ...rawRequest("received", 2), source_evaluation: undefined,
      source_gap: { ...gap, status: "unknown" }, verification_condition: "resolved" };
    const { user } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [], agentReview: review, canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt: rawReceipt }] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText("已提交，待复核");
    await selectVerificationEvidence(user);
    expect(screen.getByText("当前审查的匹配检查：商务与财务：质保期限，缺口已解决。")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "提交核验" })).toBeEnabled();
  });

  it.each([
    ["an unfinished capability", "pending", "compatible"],
    ["an unsupported capability relation", "completed", "insufficient"],
  ] as const)("does not verify a resolved gap with %s", async (_caseName, capabilityStatus, relation) => {
    const gap = { gap_id: "gap-1", domain: "commercial", capability_id: "warranty_months", mandatory: true, status: "resolved", reason_code: "", evidence_ids: ["doc-2"] };
    const review: BusinessAgentReview = {
      attempt_id: "attempt-new", status: "completed", rulepack: null, scope: null,
      domains: [{ domain: "commercial", status: "completed", capabilities: [{ capability_id: "warranty_months", mandatory: true, status: capabilityStatus, relation, reason_code: "", evidence_ids: ["doc-2"], canonical_evaluation_ids: null }] }],
      gaps: [gap], evidence: null, unresolved_gap_ids: [],
    };
    const request = { ...rawRequest("received", 2), source_evaluation: undefined,
      source_gap: { ...gap, status: "unknown" }, verification_condition: "resolved" };
    const { calls, user } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [], agentReview: review, canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt: rawReceipt }] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText("已提交，待复核");
    await selectVerificationEvidence(user);
    expect(screen.getByRole("button", { name: "提交核验" })).toBeDisabled();
    expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(false);
  });

  it.each([
    ["a frozen partial attempt", "partial", at, true],
    ["a finished running attempt", "running", at, false],
  ] as const)("applies the state gate to %s", async (_caseName, state, finishedAt, ready) => {
    const workspace = makeWorkspace("run-new", "attempt-new", [compatible]);
    const current = workspace.review_attempts!.attempt!;
    const attempt = { ...current, state, finished_at: finishedAt };
    workspace.review_attempts = { attempt, attempts: [attempt] };
    const request = rawRequest("received", 2);
    const { user } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [compatible], workspace, canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt: rawReceipt }] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText("已提交，待复核");
    await selectVerificationEvidence(user);
    if (ready) expect(screen.getByRole("button", { name: "提交核验" })).toBeEnabled();
    else expect(screen.getByRole("button", { name: "提交核验" })).toBeDisabled();
  });

  it("requires and sends business time for cloud services", async () => {
    const cloudCase = { ...detail, scenario: "cloud_services" };
    const { calls, user } = open({ caseDetail: cloudCase, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [rawRequest("received", 2)] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [] });
      if (call.pathname.endsWith("/reviews") && call.method === "POST") return Response.json({ id: "run-next", attempt_id: "attempt-next" });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText("已提交，待复核");
    const start = screen.getByRole("button", { name: "发起后续审查" });
    expect(start).toBeDisabled();
    fireEvent.change(screen.getByLabelText("审查业务时间（本地时间）"), { target: { value: "2026-11-02T10:30" } });
    expect(start).toBeEnabled();
    await user.click(start);
    await waitFor(() => expect(screen.getByLabelText("当前位置")).toHaveTextContent("run-next"));
    const call = calls.find((entry) => entry.pathname.endsWith("/reviews") && entry.method === "POST");
    expect(JSON.parse(String(call?.init?.body))).toEqual({ business_at: new Date("2026-11-02T10:30").toISOString() });
  });

  it.each([
    ["a non-ENT case", { ...detail, business_line: "SMB", scenario: "standard", contract_type: "service" }],
    ["a cloud agreement outside the service contract branch", { ...detail, business_line: "ENT", scenario: "cloud_services", contract_type: "goods" }],
  ] as const)("does not require business time for %s", async (_caseName, caseDetail) => {
    const { calls, user } = open({ caseDetail, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [rawRequest("received", 2)] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [] });
      if (call.pathname.endsWith("/reviews") && call.method === "POST") return Response.json({ id: "run-next", attempt_id: "attempt-next" });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText("已提交，待复核");
    expect(screen.queryByLabelText("审查业务时间（本地时间）")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "发起后续审查" }));
    await waitFor(() => expect(screen.getByLabelText("当前位置")).toHaveTextContent("run-next"));
    const call = calls.find((entry) => entry.pathname.endsWith("/reviews") && entry.method === "POST");
    expect(JSON.parse(String(call?.init?.body))).toEqual({});
  });

  it.each([
    ["without a frozen parsing ID", { ...document, parsed_document_id: null }],
    ["without frozen text", { ...document, text: "" }],
  ] as const)("does not offer frozen quote material %s", async (_caseName, frozenDocument) => {
    const workspace = makeWorkspace("run-new", "attempt-new", [compatible]);
    workspace.documents = [frozenDocument];
    const request = rawRequest("received", 2);
    const { calls, user } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [compatible], workspace, canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt: rawReceipt }] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText("已提交，待复核");
    await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择提交回执" }), /补充合同.txt/);
    expect(screen.getByText("当前审查未包含可引用的回执材料或答复原文。")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "提交核验" })).toBeDisabled();
    expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(false);
  });

  it.each([[4096, true], [4097, false]] as const)("enforces the frozen quotation limit at %s characters", async (length, ready) => {
    const longDocument = { ...document, text: "x".repeat(length) };
    const workspace = makeWorkspace("run-new", "attempt-new", [compatible]);
    workspace.documents = [longDocument];
    const request = rawRequest("received", 2);
    const { user } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [compatible], workspace, canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt: rawReceipt }] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText("已提交，待复核");
    await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择提交回执" }), /补充合同.txt/);
    await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择本次审查材料" }), /补充合同.txt/);
    const original = screen.getByLabelText("本次审查使用的原文") as HTMLTextAreaElement;
    await user.click(original);
    original.setSelectionRange(0, length);
    fireEvent.select(original);
    await user.type(screen.getByLabelText("核验理由"), "核对合同");
    const submit = screen.getByRole("button", { name: "提交核验" });
    if (ready) expect(submit).toBeEnabled();
    else expect(submit).toBeDisabled();
  });

  it("does not authorize an empty quotation selection", async () => {
    const request = rawRequest("received", 2);
    const { calls, user } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [compatible], canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt: rawReceipt }] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText("已提交，待复核");
    await selectVerificationEvidence(user);
    const original = screen.getByLabelText("本次审查使用的原文") as HTMLTextAreaElement;
    await user.click(original);
    original.setSelectionRange(2, 2);
    fireEvent.select(original);
    expect(screen.getByRole("button", { name: "提交核验" })).toBeDisabled();
    expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(false);
  });

  it("submits code point offsets for a quote after an ASCII and emoji prefix", async () => {
    const quoteDocument = { ...document, id: "doc-unicode", parsed_document_id: "parse-unicode", filename: "Unicode合同.txt", text: "A😀质保36个月。" };
    const workspace = makeWorkspace("run-new", "attempt-new", [compatible]);
    workspace.documents = [quoteDocument];
    const receipt = { ...rawReceipt, document_id: quoteDocument.id };
    const request = rawRequest("received", 2);
    const { calls, user } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [compatible], workspace, canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt }] });
      if (call.pathname.endsWith("/verify")) {
        const body = JSON.parse(String(call.init?.body)) as { evidence: EvidenceQuote[]; reason: string; attempt_id: string; run_id: string };
        return Response.json({ ...request, status: "verified", version: 3, verification: {
          run_id: body.run_id, attempt_id: body.attempt_id, receipt_id: receipt.id, evaluation_id: "eval-new",
          relation: "compatible", gap_id: null, gap_status: null, evidence: body.evidence,
          reason: body.reason, verified_by: "owner", verified_at: at,
        } });
      }
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText("已提交，待复核");
    await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择提交回执" }), /提交材料/);
    await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择本次审查材料" }), /Unicode合同/);
    const original = screen.getByLabelText("本次审查使用的原文") as HTMLTextAreaElement;
    await user.click(original);
    original.setSelectionRange(3, 9);
    fireEvent.select(original);
    await user.type(screen.getByLabelText("核验理由"), "核对 Unicode 引用");
    await user.click(screen.getByRole("button", { name: "提交核验" }));
    await waitFor(() => expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(true));
    const verifyCall = calls.find((call) => call.pathname.endsWith("/verify"));
    expect(JSON.parse(String(verifyCall?.init?.body))).toMatchObject({
      evidence: [{ document_id: "doc-unicode", parsed_document_id: "parse-unicode", quote: "质保36个月", start: 2, end: 8 }],
    });
  });

  it.each([[2, 8, true], [2, 4, false]] as const)("requires the frozen quote to include the submitted answer (%s..%s)", async (start, end, ready) => {
    const request = rawRequest("received", 2);
    const answerReceipt = { ...rawReceipt, id: "receipt-answer", document_id: undefined, answer: "36个月" };
    const { calls, user } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [compatible], canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt: answerReceipt }] });
      if (call.pathname.endsWith("/verify")) {
        const body = JSON.parse(String(call.init?.body)) as { evidence: EvidenceQuote[]; reason: string; attempt_id: string; run_id: string };
        return Response.json({ ...request, status: "verified", version: 3, verification: {
          run_id: body.run_id, attempt_id: body.attempt_id, receipt_id: answerReceipt.id, evaluation_id: "eval-new",
          relation: "compatible", gap_id: null, gap_status: null, evidence: body.evidence,
          reason: body.reason, verified_by: "owner", verified_at: at,
        } });
      }
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });
    await screen.findByText("已提交，待复核");
    await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择提交回执" }), /答复：36个月/);
    await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择本次审查材料" }), /补充合同/);
    const original = screen.getByLabelText("本次审查使用的原文") as HTMLTextAreaElement;
    await user.click(original);
    original.setSelectionRange(start, end);
    fireEvent.select(original);
    await user.type(screen.getByLabelText("核验理由"), "核对文字答复");
    const submit = screen.getByRole("button", { name: "提交核验" });
    if (!ready) {
      expect(submit).toBeDisabled();
      expect(screen.getByText("引用过长，或未包含所提交的文字答复。")).toBeInTheDocument();
      expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(false);
      return;
    }
    expect(submit).toBeEnabled();
    expect(screen.queryByText("引用过长，或未包含所提交的文字答复。")).not.toBeInTheDocument();
    await user.click(submit);
    await waitFor(() => expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(true));
    const verifyCall = calls.find((call) => call.pathname.endsWith("/verify"));
    expect(JSON.parse(String(verifyCall?.init?.body))).toMatchObject({
      evidence: [{ quote: "质保36个月", start: 1, end: 7 }],
    });
  });

  it("drops quote readiness when a history refresh removes its selected receipt", async () => {
    const request = rawRequest("received", 2);
    const answerReceipt = { ...rawReceipt, document_id: undefined, answer: "36个月" };
    const { calls, client, user } = open({ runId: "run-new", attemptId: "attempt-new", evaluations: [compatible], canManage: false, respond(call) {
      if (call.pathname.endsWith("/evidence-requests")) return Response.json({ items: [request] });
      if (call.pathname.endsWith("/history")) return Response.json({ items: [{ type: "received", actor_id: "owner", occurred_at: at, request, receipt: answerReceipt }] });
      return Response.json({ error: { message: "unexpected" } }, { status: 404 });
    } });

    await screen.findByText("已提交，待复核");
    await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择提交回执" }), /答复：36个月/);
    await chooseActiveOption(user, screen.getByRole("combobox", { name: "选择本次审查材料" }), /补充合同/);
    const original = screen.getByLabelText("本次审查使用的原文") as HTMLTextAreaElement;
    await user.click(original);
    original.setSelectionRange(2, 8);
    fireEvent.select(original);
    await user.type(screen.getByLabelText("核验理由"), "核对答复");
    expect(screen.getByRole("button", { name: "提交核验" })).toBeEnabled();

    await act(async () => {
      client.setQueryData(["evidence-requests", "case-1", request.id, "history"], []);
    });
    expect(screen.getByText("已选引用：")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("button", { name: "提交核验" })).toBeDisabled());
    expect(calls.some((call) => call.pathname.endsWith("/verify"))).toBe(false);
  });
});
