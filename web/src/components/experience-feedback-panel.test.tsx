import { cleanup, fireEvent, render as renderRaw, screen, waitFor } from "@testing-library/react";
import { StyleProvider } from "@ant-design/cssinjs";
import { ConfigProvider } from "antd";
import zhCN from "antd/locale/zh_CN";
import userEvent, { type UserEvent } from "@testing-library/user-event";
import type { ReactElement, ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setSessionIdentity } from "../api/http";
import type { Finding, ReviewDocument } from "../api/types";
import { chooseOption } from "../test/antd";
import { ExperienceFeedbackPanel, type ExperienceFeedbackPanelProps } from "./experience-feedback-panel";

function TestProvider({ children }: { children: ReactNode }) {
  return <StyleProvider layer><ConfigProvider virtual={false} locale={zhCN} button={{ autoInsertSpace: false }}>{children}</ConfigProvider></StyleProvider>;
}

function render(element: ReactElement) {
  return renderRaw(element, { wrapper: TestProvider });
}

const at = "2026-10-27T00:00:00Z";
const document: ReviewDocument = {
  id: "document-1", role: "draft_contract", filename: "合同.txt", media_type: "text/plain",
  digest: "", size: 8, parse_status: "ready", parse_error: null, created_at: at,
  parsed_document: null, parsed_document_id: "parse-1", text: "甲😀证据合同", clauses: [],
};
const finding: Finding = {
  id: "finding-1", origin: "rule", domain: "履约", claim: "期限差异", relation: "conflict",
  confidence: null, verification_status: "verified", evidence: [], unverified_reason: null,
  version: 2, severity: null, severity_source: null,
  decisions: [{ id: "decision-1", action: "confirm_risk", reason: "已核实", actor: "审查员", decided_at: at }],
};
const quoteScope = {
  caseId: "case-1", runId: "run-1", attemptId: "attempt-1", isFrozenAttempt: true,
  parsedDocumentIds: ["parse-1"],
};
const props: ExperienceFeedbackPanelProps = {
  caseId: "case-1", runId: "run-1", attemptId: "attempt-1", finding, documents: [document], canSubmit: true, quoteScope,
};
const savedFeedback = {
  feedback_id: "feedback-1", tenant_id: "tenant", org_id: "org", case_id: "case-1", run_id: "run-1",
  attempt_id: "attempt-1", finding_id: "finding-1", decision_id: "decision-1", decision_action: "confirm_risk",
  decision_disposition: "confirmed", reason: "核对期限", applicant_id: "reviewer", created_at: at,
  evidence_refs: [{ document_id: "document-1", parsed_document_id: "parse-1", quote: "证据", start: 2, end: 4 }],
};
const candidate = {
  case_id: "case-1", feedback_id: "feedback-1", job_id: "job-1", candidate_id: "candidate-1",
  version_id: "version-1", content_sha256: "candidate-digest", created_at: at,
  content: { schema_version: "experience/v1", applicability: ["专线合同"], exclusions: ["不适用例外"],
    check_action: "核对约定期限", minimum_evidence: ["合同原文"], counterexamples: ["无期限条款"],
    valid_from: "2026-01-01", valid_until: "2027-01-01", policy_refs: [{ knowledge_id: "policy-1", version_id: "policy-version-1", content_sha256: "policy-digest" }], review_diff: "复核差异" },
};
const trackedKey = `experience-feedback:${JSON.stringify(["case-1", "run-1", "attempt-1", "finding-1"])}`;

function parsedMetadata() {
  return {
    id: "parse-1",
    document_id: "document-1",
    original_digest: "original",
    parser_id: "parser",
    parser_version: "1",
    config_hash: "config",
    normalization_version: "v1",
    normalized_digest: "norm",
    ir_digest: "ir",
    artifact_digest: "artifact",
    created_at: at,
    status: "ready" as const,
  };
}

type Request = { path: string; init: RequestInit };
function installServer(options: { failFeedbackOnce?: boolean; failDistillationOnce?: boolean; failStatusOnce?: boolean; failCandidateOnce?: boolean; state?: "queued" | "failed" | "completed"; feedback?: typeof savedFeedback; candidate?: unknown } = {}) {
  const requests: Request[] = [];
  let feedbackCalls = 0;
  let distillCalls = 0;
  let statusCalls = 0;
  let candidateCalls = 0;
  const serverFeedback = options.feedback ?? savedFeedback;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const path = new URL(String(input), "http://workbench.test").pathname;
    requests.push({ path, init });
    if (path === "/api/cases/case-1/experience-feedback" && init.method === "POST") {
      feedbackCalls += 1;
      if (options.failFeedbackOnce && feedbackCalls === 1) {
        return Response.json({ error: { code: "temporary_failure", message: "反馈未能确认", retryable: true } }, { status: 503 });
      }
      return Response.json({ ...serverFeedback, ...JSON.parse(String(init.body)) }, { status: 201 });
    }
    if (path === "/api/cases/case-1/experience-feedback/feedback-1" && init.method === "GET") return Response.json(serverFeedback);
    if (path === "/api/cases/case-1/experience-feedback/feedback-1/distillations" && init.method === "POST") {
      distillCalls += 1;
      if (options.failDistillationOnce && distillCalls === 1) {
        return Response.json({ error: { code: "policy_missing", message: "来源缺少制度快照", details: { required: "policy" } } }, { status: 409 });
      }
      return Response.json({ job_id: "job-1" }, { status: 202 });
    }
    if (path === "/api/cases/case-1/experience-feedback/feedback-1/distillations/job-1" && init.method === "GET") {
      statusCalls += 1;
      if (options.failStatusOnce && statusCalls === 1) {
        return Response.json({ error: { code: "status_failed", message: "状态读取失败" } }, { status: 500 });
      }
      const state = options.state ?? "completed";
      return Response.json({ job_id: "job-1", feedback_id: "feedback-1", state,
        ...(state === "completed" ? { candidate_id: "candidate-1", version_id: "version-1" } : {}),
        ...(state === "failed" ? { failure_code: "experience_model_unavailable" } : {}) });
    }
    if (path === "/api/cases/case-1/experience-candidates/candidate-1/versions/version-1" && init.method === "GET") {
      candidateCalls += 1;
      if (options.failCandidateOnce && candidateCalls === 1) {
        return Response.json({ error: { code: "candidate_failed", message: "候选读取失败" } }, { status: 500 });
      }
      return Response.json(options.candidate ?? candidate);
    }
    return Response.json({ error: { code: "unexpected", message: "请求不匹配" } }, { status: 404 });
  }));
  return requests;
}

async function prepareSuggestion(user: UserEvent) {
  await chooseOption(user, screen.getByRole("combobox", { name: "审查判断" }), /确认风险.*审查员/);
  await chooseOption(user, screen.getByRole("combobox", { name: "本次审查材料" }), /合同.txt/);
  const text = screen.getByRole("textbox", { name: "本次审查使用的原文" }) as HTMLTextAreaElement;
  await user.click(text);
  text.setSelectionRange(3, 5);
  fireEvent.select(text);
  fireEvent.keyUp(text, { key: "Shift" });
  await user.click(screen.getByRole("button", { name: "选用选中原文" }));
  await user.type(screen.getByRole("textbox", { name: "经验建议理由" }), "核对期限");
}

afterEach(() => {
  cleanup();
  sessionStorage.clear();
  setSessionIdentity(null);
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

describe("experience feedback panel", () => {
  it("requires a real decision, a frozen parse, an explicit quote, and submit permission", async () => {
    const requests = installServer();
    const user = userEvent.setup();
    const view = render(<ExperienceFeedbackPanel {...props} canSubmit={false} />);
    expect(screen.getByRole("button", { name: "提交经验建议并请求提炼" })).toBeDisabled();
    view.rerender(<ExperienceFeedbackPanel {...props} finding={{ ...finding, decisions: [] }} />);
    expect(screen.getByText("当前线索没有可用于反馈的审查判断。")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "提交经验建议并请求提炼" })).toBeDisabled();
    view.rerender(<ExperienceFeedbackPanel {...props} documents={[{ ...document, parsed_document_id: null }]} />);
    expect(screen.getByText("本次执行没有可供选择的已解析原文。")).toBeInTheDocument();
    view.rerender(<ExperienceFeedbackPanel {...props} />);
    await chooseOption(user, screen.getByRole("combobox", { name: "审查判断" }), /确认风险.*审查员/);
    await user.type(screen.getByRole("textbox", { name: "经验建议理由" }), "核对期限");
    expect(screen.getByRole("button", { name: "提交经验建议并请求提炼" })).toBeDisabled();
    expect(requests).toHaveLength(0);
  });

  it("distinguishes duplicate business labels without exposing source IDs", async () => {
    const requests = installServer({ state: "queued" });
    const user = userEvent.setup();
    render(<ExperienceFeedbackPanel {...props}
      finding={{ ...finding, decisions: [...finding.decisions, { ...finding.decisions[0]!, id: "decision-2" }] }}
      quoteScope={{ ...quoteScope, parsedDocumentIds: ["parse-1", "parse-2"] }}
      documents={[document, { ...document, id: "document-2", parsed_document_id: "parse-2" }]} />);
    await user.click(screen.getByRole("combobox", { name: "审查判断" }));
    const decision = await screen.findByRole("option", { name: /第 2 条.*确认风险.*审查员/ });
    expect(decision).not.toHaveTextContent(/decision-2/);
    await user.click(decision);
    await user.click(screen.getByRole("combobox", { name: "本次审查材料" }));
    const first = await screen.findByRole("option", { name: /第 1 份.*合同.txt/ });
    const second = await screen.findByRole("option", { name: /第 2 份.*合同.txt/ });
    expect(first).not.toHaveTextContent(/document-1|parse-1/);
    expect(second).not.toHaveTextContent(/document-2|parse-2/);
    await user.click(second);
    const text = screen.getByRole("textbox", { name: "本次审查使用的原文" }) as HTMLTextAreaElement;
    await user.click(text);
    text.setSelectionRange(3, 5);
    fireEvent.select(text);
    fireEvent.keyUp(text, { key: "Shift" });
    await user.click(screen.getByRole("button", { name: "选用选中原文" }));
    await user.type(screen.getByRole("textbox", { name: "经验建议理由" }), "核对期限");
    await user.click(screen.getByRole("button", { name: "提交经验建议并请求提炼" }));
    await screen.findByText(/任务状态：排队中/);
    const post = requests.find((request) => request.path.endsWith("experience-feedback") && request.init.method === "POST")!;
    expect(JSON.parse(String(post.init.body))).toMatchObject({
      decision_id: "decision-2",
      evidence_refs: [{ document_id: "document-2", parsed_document_id: "parse-2", quote: "证据", start: 2, end: 4 }],
    });
  });


  it("submits the chosen real quote with Unicode code-point offsets and displays the actual candidate", async () => {
    vi.stubEnv("VITE_ADMIN_URL", "http://admin.example.test/");
    const requests = installServer();
    const user = userEvent.setup();
    render(<ExperienceFeedbackPanel {...props} />);
    await prepareSuggestion(user);
    await user.click(screen.getByRole("button", { name: "提交经验建议并请求提炼" }));
    const candidateContent = await screen.findByRole("region", { name: "经验候选内容" });
    expect(candidateContent).toHaveTextContent("核对约定期限");
    expect(screen.getByText(/任务状态：提炼完成/)).toBeInTheDocument();
    expect(screen.getByText(/候选仅供后续治理审核，不代表批准或发布/)).toBeInTheDocument();
    const post = requests.find((request) => request.path.endsWith("experience-feedback") && request.init.method === "POST")!;
    expect(JSON.parse(String(post.init.body))).toEqual({
      run_id: "run-1", attempt_id: "attempt-1", finding_id: "finding-1", decision_id: "decision-1", reason: "核对期限",
      evidence_refs: [{ document_id: "document-1", parsed_document_id: "parse-1", quote: "证据", start: 2, end: 4 }],
    });
    expect(new Headers(post.init.headers).get("Idempotency-Key")).toBeTruthy();
    expect(requests.filter((request) => request.init.method === "POST")).toHaveLength(2);
    const handoff = new URL(screen.getByRole("link", { name: "在知识治理中审阅此候选" }).getAttribute("href")!);
    expect(handoff.origin).toBe("http://admin.example.test");
    expect(handoff.pathname).toBe("/admin/governed-knowledge/new");
    expect(Object.fromEntries(handoff.searchParams)).toEqual({ source_case: "case-1", candidate_id: "candidate-1", candidate_version_id: "version-1", candidate_content_sha256: "candidate-digest" });
    expect(screen.getByText("已引用 1 项制度版本")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "经验反馈进度" })).not.toHaveTextContent(/case-1|run-1|attempt-1|document-1|parse-1|decision-1|candidate-1|version-1|feedback-1|job-1|policy-1|policy-digest|candidate-digest/);
    const recoveryIds = screen.getByText(/反馈编号：feedback-1/);
    expect(recoveryIds).not.toBeVisible();
    await user.click(screen.getByText("用反馈编号和任务编号恢复状态"));
    expect(recoveryIds).toBeVisible();
    expect(screen.queryByText("candidate-digest")).not.toBeInTheDocument();
  });

  it("maps selections across CRLF to the original code-point span and rejects half of an emoji", async () => {
    const requests = installServer({ state: "queued" });
    const user = userEvent.setup();
    render(<ExperienceFeedbackPanel {...props} documents={[{ ...document, text: "甲😀\r\n证据合同" }]} />);
    await chooseOption(user, screen.getByRole("combobox", { name: "审查判断" }), /确认风险.*审查员/);
    await chooseOption(user, screen.getByRole("combobox", { name: "本次审查材料" }), /合同.txt/);
    const text = screen.getByRole("textbox", { name: "本次审查使用的原文" }) as HTMLTextAreaElement;
    await user.click(text);
    text.setSelectionRange(1, 2);
    fireEvent.select(text);
    fireEvent.keyUp(text, { key: "Shift" });
    expect(screen.getByRole("button", { name: "选用选中原文" })).toBeDisabled();
    text.setSelectionRange(4, 6);
    fireEvent.select(text);
    fireEvent.keyUp(text, { key: "Shift" });
    await user.click(screen.getByRole("button", { name: "选用选中原文" }));
    await user.type(screen.getByRole("textbox", { name: "经验建议理由" }), "核对期限");
    await user.click(screen.getByRole("button", { name: "提交经验建议并请求提炼" }));
    await screen.findByText(/任务状态：排队中/);
    const post = requests.find((request) => request.path.endsWith("experience-feedback") && request.init.method === "POST")!;
    expect(JSON.parse(String(post.init.body)).evidence_refs).toEqual([
      { document_id: "document-1", parsed_document_id: "parse-1", quote: "证据", start: 4, end: 6 },
    ]);
  });

  it("replays a failed feedback request with its original identity and body", async () => {
    const requests = installServer({ failFeedbackOnce: true, state: "queued" });
    const user = userEvent.setup();
    render(<ExperienceFeedbackPanel {...props} />);
    await prepareSuggestion(user);
    await user.click(screen.getByRole("button", { name: "提交经验建议并请求提炼" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "提交经验建议并请求提炼" })).toBeEnabled());
    await user.click(screen.getByRole("button", { name: "提交经验建议并请求提炼" }));
    expect(await screen.findByText(/任务状态：排队中/)).toBeInTheDocument();
    const attempts = requests.filter((request) => request.path.endsWith("experience-feedback") && request.init.method === "POST");
    expect(attempts).toHaveLength(2);
    expect(attempts[0]!.init.body).toBe(attempts[1]!.init.body);
    expect(new Headers(attempts[0]!.init.headers).get("Idempotency-Key")).toBe(new Headers(attempts[1]!.init.headers).get("Idempotency-Key"));
  });



  it("keeps saved feedback and the same request key when distillation fails", async () => {
    const requests = installServer({ failDistillationOnce: true, state: "queued" });
    const user = userEvent.setup();
    render(<ExperienceFeedbackPanel {...props} />);
    await prepareSuggestion(user);
    await user.click(screen.getByRole("button", { name: "提交经验建议并请求提炼" }));
    expect(await screen.findByRole("heading", { name: "反馈已记录" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "请求提炼" }));
    expect(await screen.findByText(/任务状态：排队中/)).toBeInTheDocument();
    const attempts = requests.filter((request) => request.path.endsWith("/distillations") && request.init.method === "POST");
    expect(attempts).toHaveLength(2);
    expect(new Headers(attempts[0]!.init.headers).get("Idempotency-Key")).toBe(new Headers(attempts[1]!.init.headers).get("Idempotency-Key"));
    expect(requests.filter((request) => request.path.endsWith("experience-feedback") && request.init.method === "POST")).toHaveLength(1);
    expect(screen.queryByRole("link", { name: "在知识治理中审阅此候选" })).not.toBeInTheDocument();
  });

  it("recovers an exact saved feedback and failed task from the server on reload", async () => {
    sessionStorage.setItem(trackedKey, JSON.stringify({ feedbackId: "feedback-1", jobId: "job-1", distillKey: "distill-key" }));
    const requests = installServer({ state: "failed" });
    render(<ExperienceFeedbackPanel {...props} />);
    expect(await screen.findByText(/任务状态：提炼失败，失败代码：experience_model_unavailable/)).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "反馈已记录" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "重新请求提炼" })).toBeEnabled();
    expect(requests.map((request) => request.init.method)).toEqual(["GET", "GET"]);
    expect(screen.queryByText("已生成经验候选")).not.toBeInTheDocument();
  });

  it("reloads the candidate only after the server reports it on a refreshed task", async () => {
    sessionStorage.setItem(trackedKey, JSON.stringify({ feedbackId: "feedback-1", jobId: "job-1", distillKey: "distill-key" }));
    const options: { state: "queued" | "completed" } = { state: "queued" };
    const requests = installServer(options);
    const user = userEvent.setup();
    render(<ExperienceFeedbackPanel {...props} />);
    expect(await screen.findByText(/任务状态：排队中/)).toBeInTheDocument();
    expect(requests.some((request) => request.path.includes("experience-candidates"))).toBe(false);
    options.state = "completed";
    await user.click(screen.getByRole("button", { name: "刷新任务状态" }));
    expect(await screen.findByText("核对约定期限")).toBeInTheDocument();
    expect(screen.getByText(/任务状态：提炼完成/)).toBeInTheDocument();
    expect(requests.some((request) => request.path.endsWith("/experience-candidates/candidate-1/versions/version-1"))).toBe(true);
  });


  it("recovers by explicit identities without inventing a candidate", async () => {
    const requests = installServer({ state: "queued" });
    const user = userEvent.setup();
    render(<ExperienceFeedbackPanel {...props} canSubmit={false} />);
    await user.click(screen.getByText("用反馈编号和任务编号恢复状态"));
    await user.type(screen.getByRole("textbox", { name: "反馈编号" }), "feedback-1");
    await user.type(screen.getByRole("textbox", { name: "任务编号" }), "job-1");
    await user.click(screen.getByRole("button", { name: "读取反馈和任务" }));
    expect(await screen.findByText(/任务状态：排队中/)).toBeInTheDocument();
    expect(requests.map((request) => request.init.method)).toEqual(["GET", "GET"]);
    expect(screen.queryByText("已生成经验候选")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "请求提炼" })).not.toBeInTheDocument();
  });


  it("rejects a restored feedback from a different attempt before loading its task", async () => {
    sessionStorage.setItem(trackedKey, JSON.stringify({ feedbackId: "feedback-1", jobId: "job-1", distillKey: "key" }));
    const requests = installServer({ feedback: { ...savedFeedback, attempt_id: "other-attempt" } });
    render(<ExperienceFeedbackPanel {...props} />);
    expect(await screen.findByText("该反馈不属于当前审查执行记录和线索")).toBeInTheDocument();
    expect(requests).toHaveLength(1);
    expect(screen.queryByText(/任务状态：/)).not.toBeInTheDocument();
  });

  it("does not carry a recovered candidate into another finding", async () => {
    sessionStorage.setItem(trackedKey, JSON.stringify({ feedbackId: "feedback-1", jobId: "job-1", distillKey: "key" }));
    const requests = installServer();
    const view = render(<ExperienceFeedbackPanel {...props} />);
    expect(await screen.findByText("核对约定期限")).toBeInTheDocument();
    view.rerender(<ExperienceFeedbackPanel {...props} finding={{ ...finding, id: "finding-2", decisions: [] }} />);
    expect(screen.queryByText("核对约定期限")).not.toBeInTheDocument();
    expect(screen.getByText("当前线索没有可用于反馈的审查判断。")).toBeInTheDocument();
    expect(requests.map((request) => request.init.method)).toEqual(["GET", "GET", "GET"]);
  });


  it("does not submit stale text after the source document changes", async () => {
    const requests = installServer();
    const user = userEvent.setup();
    const view = render(<ExperienceFeedbackPanel {...props} />);
    await prepareSuggestion(user);
    expect(screen.getByRole("button", { name: "提交经验建议并请求提炼" })).toBeEnabled();
    view.rerender(<ExperienceFeedbackPanel {...props} documents={[{ ...document, text: "甲😀不同合同" }]} />);
    await waitFor(() => expect(screen.getByRole("button", { name: "提交经验建议并请求提炼" })).toBeDisabled());
    expect(requests).toHaveLength(0);
  });
});

describe("experience feedback storage and source guards", () => {
  it.each([
    "not-json",
    "[]",
    '"text"',
    "{}",
    '{"feedbackId":"   "}',
    '{"feedbackId":null}',
  ])("ignores unusable saved tracking: %s", async (raw) => {
    sessionStorage.setItem(trackedKey, raw);
    const requests = installServer();
    const view = render(<ExperienceFeedbackPanel {...props} />);
    expect(
      screen.getByRole("combobox", { name: "审查判断" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(requests).toHaveLength(0);
    view.unmount();
  });

  it("restores a saved feedback without inventing a job identity", async () => {
    sessionStorage.setItem(
      trackedKey,
      JSON.stringify({ feedbackId: "feedback-1", jobId: "", distillKey: "" }),
    );
    const requests = installServer();
    render(<ExperienceFeedbackPanel {...props} />);
    expect(
      await screen.findByRole("heading", { name: "反馈已记录" }),
    ).toBeInTheDocument();
    expect(screen.queryByText(/任务状态：/)).not.toBeInTheDocument();
    expect(requests.map((request) => request.init.method)).toEqual(["GET"]);
  });

  it("offers only fully frozen parsed sources", async () => {
    const user = userEvent.setup();
    installServer();
    const valid = document;
    const variants: ReviewDocument[] = [
      valid,
      { ...document, id: "pending", parse_status: "pending" },
      { ...document, id: "no-text", text: null },
      { ...document, id: "empty-text", text: "" },
      { ...document, id: "   " },
      { ...document, id: "no-parse", parsed_document_id: null },
      { ...document, id: "blank-parse", parsed_document_id: "   " },
      {
        ...document,
        id: "failed-parse",
        parsed_document: { ...parsedMetadata(), status: "failed" },
      },
      {
        ...document,
        id: "wrong-document",
        parsed_document: { ...parsedMetadata(), document_id: "other" },
      },
      {
        ...document,
        id: "wrong-parse",
        parsed_document: { ...parsedMetadata(), id: "other" },
      },
    ];
    render(<ExperienceFeedbackPanel {...props} documents={variants} />);
    await user.click(screen.getByRole("combobox", { name: "本次审查材料" }));
    const options = await screen.findAllByRole("option");
    expect(options).toHaveLength(1);
    expect(options[0]).toHaveTextContent(/第 1 份 · 合同.txt · 拟签合同/);
  });

  it("shows sparse candidate content without inventing fields", async () => {
    sessionStorage.setItem(
      trackedKey,
      JSON.stringify({ feedbackId: "feedback-1", jobId: "job-1", distillKey: "key" }),
    );
    installServer({
      candidate: {
        ...candidate,
        content: {
          schema_version: "experience/v1",
          applicability: null,
          exclusions: [],
          check_action: "",
          minimum_evidence: [],
          counterexamples: null,
          valid_from: "",
          valid_until: "",
          policy_refs: null,
          review_diff: "",
        },
      },
    });
    render(<ExperienceFeedbackPanel {...props} />);
    expect(await screen.findByText("已生成经验候选")).toBeInTheDocument();
    expect(screen.getAllByText("未提供").length).toBeGreaterThan(0);
    expect(screen.getAllByText("无").length).toBeGreaterThan(0);
    expect(screen.getByText(/未提供 至 未提供/)).toBeInTheDocument();
    expect(screen.getByText("在知识治理中审阅此候选")).toBeInTheDocument();
  });

  it("reports an empty policy reference list as none", async () => {
    sessionStorage.setItem(
      trackedKey,
      JSON.stringify({ feedbackId: "feedback-1", jobId: "job-1", distillKey: "key" }),
    );
    installServer({
      candidate: {
        ...candidate,
        content: { ...candidate.content, policy_refs: [] },
      },
    });
    render(<ExperienceFeedbackPanel {...props} />);
    expect(await screen.findByText("已生成经验候选")).toBeInTheDocument();
    expect(screen.getByText("无")).toBeInTheDocument();
  });
});

describe("experience feedback distillation errors", () => {
  it("shows a status read failure and recovers after a refresh", async () => {
    sessionStorage.setItem(
      trackedKey,
      JSON.stringify({ feedbackId: "feedback-1", jobId: "job-1", distillKey: "key" }),
    );
    installServer({ failStatusOnce: true });
    const user = userEvent.setup();
    render(<ExperienceFeedbackPanel {...props} />);
    expect(await screen.findByText("状态读取失败")).toBeInTheDocument();
    expect(screen.queryByText(/任务状态：/)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "刷新任务状态" }));
    expect(await screen.findByText(/任务状态：提炼完成/)).toBeInTheDocument();
    expect(screen.getByText("已生成经验候选")).toBeInTheDocument();
  });

  it("shows a candidate read failure without dropping the task status", async () => {
    sessionStorage.setItem(
      trackedKey,
      JSON.stringify({ feedbackId: "feedback-1", jobId: "job-1", distillKey: "key" }),
    );
    installServer({ failCandidateOnce: true });
    const user = userEvent.setup();
    render(<ExperienceFeedbackPanel {...props} />);
    expect(await screen.findByText("候选读取失败")).toBeInTheDocument();
    expect(screen.getByText(/任务状态：提炼完成/)).toBeInTheDocument();
    expect(screen.queryByText("已生成经验候选")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "刷新任务状态" }));
    expect(await screen.findByText("已生成经验候选")).toBeInTheDocument();
  });
});
