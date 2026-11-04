// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “ComparePage frozen run selection, ComparePage evidence and actual results”; representative assertions cover defaults to a case with multiple runs, resolves timestamp ties and selects the active contract, honors URL run choices, changes each side independently and clears a previous finding selection, filters the comparison entry by case number and links each contract into its own comparison. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-04
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { act, cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, useLocation, useNavigate } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setSessionIdentity } from "../api/http";
import { queryKeys } from "../api/queries";
import type {
  EvidenceAnchor,
  ExecutionAttempt,
  Finding,
  ReviewDocument,
  ReviewWorkspace,
  WorkbenchCase,
  WorkbenchRun,
  WorkbenchSnapshot,
} from "../api/types";
import { ComparePage } from "./compare-page";
import { CompareEntryPage } from "./compare-entry-page";

const at = "2026-10-21T06:00:00Z";
const clients: QueryClient[] = [];

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function document(
  id: string,
  role: string,
  text: string | null,
  overrides: Partial<ReviewDocument> = {},
): ReviewDocument {
  return {
    id,
    role,
    filename: `${id}.txt`,
    media_type: "text/plain",
    digest: `digest-${id}`,
    size: 36,
    parse_status: "ready",
    parse_error: null,
    superseded: false,
    created_at: at,
    parsed_document_id: `parse-${id}`,
    text,
    clauses: [],
    parsed_document: {
      id: `parse-${id}`,
      document_id: id,
      original_digest: `digest-${id}`,
      parser_id: "text",
      parser_version: "1",
      config_hash: "config-1",
      normalization_version: "1",
      normalized_digest: `normalized-${id}`,
      ir_digest: null,
      artifact_digest: `artifact-${id}`,
      created_at: at,
      status: "ready",
    },
    ...overrides,
  };
}

function anchor(
  documentId = "source-z",
  overrides: Partial<EvidenceAnchor> = {},
): EvidenceAnchor {
  return {
    id: `evidence-${documentId}`,
    document_version_id: documentId,
    parsed_document_id: null,
    normalized_digest: null,
    quote: "36个月",
    status: "located",
    page: null,
    text_offset: { start: 4, end: 8 },
    bbox: null,
    mismatch_reason: null,
    ...overrides,
  };
}

function finding(overrides: Partial<Finding> = {}): Finding {
  return {
    id: "finding-rule",
    origin: "rule",
    domain: "质保",
    claim: "质保期限低于采购要求",
    relation: "conflict",
    confidence: null,
    verification_status: "verified",
    unverified_reason: null,
    version: 3,
    decisions: [],
    severity: "high",
    severity_source: {
      policy_id: "warranty-policy",
      policy_version: "2",
      clause: "第4条",
      reason: "低于采购基线",
    },
    evidence: [
      anchor("source-z", {
        parsed_document_id: "parse-source-z",
        normalized_digest: "normalized-source-z",
      }),
    ],
    ...overrides,
  };
}

function caseRecord(
  id: string,
  title: string,
  number: string | null,
): WorkbenchCase {
  return {
    id,
    title,
    contract_type: "procurement_ict",
    document_count: 3,
    pending_decisions: 1,
    updated_at: at,
    latest_run: null,
    severity: null,
    progress: null,
    profile:
      number === null
        ? null
        : {
            case_id: id,
            number,
            customer: "采购客户",
            supplier: "设备供应商",
            contract_category: "采购",
            owner_id: "reviewer",
            due_at: null,
            version: 1,
            updated_at: at,
          },
  };
}

function run(
  id: string,
  caseId = "case-main",
  overrides: Partial<WorkbenchRun> = {},
): WorkbenchRun {
  return {
    id,
    case_id: caseId,
    case_title: caseId === "case-main" ? "专线合同修订" : "单次审查案件",
    state: "completed",
    created_at: at,
    updated_at: at,
    attempt_id: null,
    started_at: at,
    finished_at: at,
    usage: null,
    ...overrides,
  };
}

function attempt(
  id: string,
  ordinal: number,
  overrides: Partial<ExecutionAttempt> = {},
): ExecutionAttempt {
  return {
    id,
    run_id: "run-z",
    ordinal,
    retry_of_attempt_id: ordinal === 1 ? null : "attempt-z-1",
    state: "completed",
    accepted_at: at,
    started_at: at,
    finished_at: at,
    parsed_document_ids: ["parse-source-z", "parse-draft-z-old", "parse-draft-z"],
    input_hash: "inputs",
    output_hash: "outputs",
    error_code: null,
    history_kind: "frozen",
    ...overrides,
  };
}

function review(
  id: string,
  documents: ReviewDocument[],
  caseId = "case-main",
): ReviewWorkspace {
  return {
    run: {
      id,
      case_id: caseId,
      state: "completed",
      updated_at: at,
      started_at: at,
      model_ref: null,
      partial_reason: null,
      failure_message: null,
    },
    documents,
    findings: [],
    review_attempts: null,
    execution: null,
    snapshot: { document_ids: documents.map((item) => item.id), rule_set_hash: "", playbook_hash: "", model_ref: null, comparator_ref: null },
    audit_events: [],
    evaluations: [],
    previous_run_id: null,
    revisions: [],
  };
}

class CompareService {
  data: WorkbenchSnapshot = {
    schema_version: "workbench/v1",
    period: "week",
    generated_at: at,
    dashboard: {
      visible_cases: 3,
      my_pending_tasks: 0,
      overdue_tasks: 0,
      pending_review_cases: 1,
      remediation_cases: 0,
      completed_reviews: 4,
      average_duration_ms: null,
      progress: [],
      risks: [],
    },
    cases: [
      caseRecord("case-single", "单次审查案件", "N-001"),
      caseRecord("case-main", "专线合同修订", "N-002"),
      caseRecord("case-empty", "暂无审查案件", null),
    ],
    runs: [
      run("run-y", "case-main", { attempt_id: "attempt-y" }),
      run("run-a", "case-main", { updated_at: "2026-10-19T06:00:00Z" }),
      run("run-single", "case-single"),
      run("run-z", "case-main", { attempt_id: "attempt-z-2" }),
    ],
    profiles: [],
    tasks: [],
    relations: [],
    knowledge: [],
    members: [],
    remediations: [],
    reports: [],
    documents: [],
    audit: [],
    templates: [],
    model: {
      name: "",
      ocr_name: "",
      status: "unconfigured",
      label: "规则模式",
    },
    tools: [],
  };
  reviews = new Map<string, ReviewWorkspace>();
  frozenAttempts = new Map<string, ReviewWorkspace>();
  requests: string[] = [];
  snapshotFailures: number[] = [];
  reviewFailures = new Map<string, number>();
  snapshotGate: ReturnType<typeof Promise.withResolvers<void>> | null = null;
  reviewGates = new Map<
    string,
    ReturnType<typeof Promise.withResolvers<void>>
  >();

  constructor() {
    const current = review("run-z", [
      document("source-z", "procurement", "质保期为36个月。"),
      document("draft-z-old", "draft_contract", "质保期为12个月。", {
        superseded: true,
      }),
      document("draft-z", "draft_contract", "质保期为24个月。"),
    ]);
    current.findings = [
      finding(),
      finding({
        id: "finding-model",
        origin: "model",
        claim: "模型建议复核付款安排",
        severity: "medium",
        severity_source: null,
        evidence: [anchor("draft-z", { quote: "24个月" })],
      }),
      finding({
        id: "finding-mismatch",
        claim: "历史引用坐标已失效",
        severity: "low",
        severity_source: null,
        evidence: [anchor("draft-z", { quote: "48个月" })],
      }),
      finding({
        id: "finding-info",
        claim: "提示补充签章记录",
        severity: "info",
        severity_source: null,
        evidence: [],
      }),
      finding({
        id: "finding-unrated",
        claim: "没有显式风险等级的发现",
        severity: null,
        severity_source: null,
        evidence: [],
      }),
    ];
    current.previous_run_id = "run-y";
    current.revision_comparison_source = {
      current: { case_id: "case-main", run_id: "run-z", attempt_id: "attempt-z-2" },
      previous: { case_id: "case-main", run_id: "run-y", attempt_id: "attempt-y" },
    };
    current.revisions = [
      {
        mark: "保留",
        previous_finding_id: "prior-finding",
        finding_id: "finding-rule",
        domain: "质保",
        relation: "conflict",
        claim: "质保修订继续存在",
        prior_decision: {
          id: "decision-prior",
          action: "confirm_risk",
          reason: "已核实",
          actor: "审查员",
          decided_at: at,
        },
      },
      {
        mark: "消除",
        previous_finding_id: null,
        finding_id: null,
        domain: "付款",
        relation: "compatible",
        claim: "付款修订已消除旧风险",
        prior_decision: null,
      },
    ];
    current.evaluations = [
      {
        id: "evaluation-z",
        domain: "质保",
        relation: "conflict",
        rule_id: "WARRANTY-001",
        rule_version: "2.1",
        reason: "合同质保低于采购要求",
        capability_id: "minimum_warranty",
        object_refs: [],
        finding_id: "finding-rule",
        reason_code: "below_minimum",
        fact_ids: [],
      },
    ];
    const first = attempt("attempt-z-1", 1, {
      state: "failed",
      finished_at: null,
    });
    const second = attempt("attempt-z-2", 2);
    const publicFirst = { ...first, input_hash: null, output_hash: null };
    const publicSecond = { ...second, input_hash: null, output_hash: null };
    current.review_attempts = {
      attempt: publicSecond,
      attempts: [publicFirst, publicSecond],
    };
    current.execution = {
      schema_version: "audit-execution/v1",
      attempt: second,
      attempts: [first, second],
      events: [
        {
          seq: 1,
          attempt_id: second.id,
          span_id: null,
          parent_span_id: null,
          node_kind: "rule",
          phase: "evaluate",
          event_type: "completed",
          at,
          duration_ms: 200,
          status: "completed",
          error_code: null,
          model_ref: null,
          usage: null,
        },
      ],
      agent_review: null,
      usage: {
        calls: 2,
        unknown_calls: 0,
        sources: ["model"],
        input_tokens: 120,
        output_tokens: 30,
        total_tokens: 150,
        known_subtotals: {
          input_tokens: 120,
          output_tokens: 30,
          total_tokens: 150,
        },
        cost: null,
      },
      all_attempts_usage: null,
      duration_ms: 1000,
      first_verified_finding_ms: null,
      queue_wait_ms: null,
    };
    this.reviews.set("run-z", current);
    this.reviews.set(
      "run-y",
      review("run-y", [
        document("draft-y", "draft_contract", "历史合同质保36个月。"),
        document("reference-y", "procurement", "历史采购说明。"),
      ]),
    );
    const priorAttempt = attempt("attempt-y", 1, { run_id: "run-y", parsed_document_ids: ["parse-draft-y", "parse-reference-y"] });
    this.reviews.get("run-y")!.review_attempts = { attempt: priorAttempt, attempts: [priorAttempt] };
    this.reviews.set(
      "run-a",
      review("run-a", [
        document("draft-a", "draft_contract", null, {
          superseded: true,
          parsed_document_id: null,
          parsed_document: null,
        }),
      ]),
    );
    this.reviews.set(
      "run-single",
      review(
        "run-single",
        [document("single-source", "procurement", "单次审查冻结文本。")],
        "case-single",
      ),
    );
    this.data.cases[1]!.latest_run = {
      id: "run-z",
      state: "completed",
      updated_at: at,
    };
  }

  fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = new URL(String(input), "http://compare.test");
    this.requests.push(`${url.pathname}${url.search}`);
    if (url.pathname === "/api/workbench") {
      if (this.snapshotGate) await this.snapshotGate.promise;
      const failure = this.snapshotFailures.shift();
      return failure
        ? json(
            { error: { message: "对照快照暂不可用", code: "unavailable" } },
            failure,
          )
        : json(this.data);
    }
    const parts = url.pathname.split("/");
    if (parts[4] === "reviews") {
      const id = decodeURIComponent(parts[5]!);
      const gate = this.reviewGates.get(id);
      if (gate) await gate.promise;
      const failure = this.reviewFailures.get(id);
      if (failure)
        return json(
          { error: { message: "冻结工作区访问被拒绝", code: "forbidden" } },
          failure,
        );
      const requestedAttempt = url.searchParams.get("attempt_id");
      const historical = this.frozenAttempts.get(`${id}:${requestedAttempt}`);
      const workspace = historical ?? this.reviews.get(id);
      const summary = this.data.runs.find((item) => item.id === id);
      if (
        !workspace ||
        workspace.run.case_id !== decodeURIComponent(parts[3]!) ||
        (!historical && requestedAttempt !== null && (summary?.attempt_id ?? null) !== requestedAttempt)
      ) {
        return json({ error: { message: "未找到对应冻结尝试" } }, 404);
      }
      return json(workspace);
    }
    return json({ error: { message: "未匹配的对照请求" } }, 404);
  };
}

function CurrentLocation() {
  const location = useLocation();
  const navigate = useNavigate();
  return <><div aria-label="路由查询">{location.search}</div><button onClick={() => navigate(-1)}>测试后退</button><button onClick={() => navigate(1)}>测试前进</button></>;
}

function open(
  service = new CompareService(),
  entry = "/compare?case_id=case-main",
  gcTime = 0,
) {
  vi.stubGlobal("fetch", vi.fn(service.fetch));
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime },
      mutations: { retry: false },
    },
  });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[entry]}>
        <ComparePage />
        <CurrentLocation />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { service, client, user: userEvent.setup() };
}

function openEntry(service = new CompareService()) {
  vi.stubGlobal("fetch", vi.fn(service.fetch));
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/compare"]}>
        <CompareEntryPage />
        <CurrentLocation />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { service, user: userEvent.setup() };
}

function material(side: "当前版本" | "历史版本") {
  return screen.getByLabelText(`${side}材料解析正文`);
}

async function chooseRun(
  user: ReturnType<typeof userEvent.setup>,
  side: "当前版本" | "历史版本",
  name: RegExp,
) {
  await user.click(screen.getByRole("combobox", { name: side }));
  const matches = await screen.findAllByRole("option", { name });
  await user.click(matches[side === "当前版本" ? 0 : matches.length - 1]!);
}

function riskPanel() {
  return screen.getByRole("heading", { name: "审查结果" }).closest("aside")!;
}

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  setSessionIdentity(null);
  vi.unstubAllGlobals();
});

describe("ComparePage frozen run selection", () => {
  it("pins both initial sides across a new run, reorder, retry and browser history", async () => {
    const { service, client, user } = open();
    await screen.findByLabelText("历史版本材料解析正文");
    await chooseRun(user, "当前版本", /审查标识 run-a/);
    service.frozenAttempts.set("run-y:attempt-y", service.reviews.get("run-y")!);
    const nextRuns = [run("run-c", "case-main", { updated_at: "2026-11-05T01:00:00Z", attempt_id: "attempt-c" }),
      ...service.data.runs.map((item) => item.id === "run-y" ? { ...item, attempt_id: "attempt-y-retry" } : item).reverse()];
    service.data = { ...service.data, runs: nextRuns };
    act(() => client.setQueryData(queryKeys.workbench("week"), service.data));
    const params = new URLSearchParams(screen.getByLabelText("路由查询").textContent ?? "");
    expect(params.get("case_id")).toBe("case-main");
    expect(params.get("left_run_id")).toBe("run-a");
    expect(params.get("right_run_id")).toBe("run-y");
    expect(params.get("right_attempt_id")).toBe("attempt-y");
    expect(within(screen.getByRole("combobox", { name: "历史版本" }).closest(".compare-run-select") as HTMLElement).getByTitle("attempt-y")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "测试后退" }));
    await waitFor(() => expect(new URLSearchParams(screen.getByLabelText("路由查询").textContent ?? "").get("left_run_id")).toBe("run-z"));
    await user.click(screen.getByRole("button", { name: "测试前进" }));
    await waitFor(() => expect(new URLSearchParams(screen.getByLabelText("路由查询").textContent ?? "").get("right_attempt_id")).toBe("attempt-y"));
    const saved = screen.getByLabelText("路由查询").textContent!;
    cleanup();
    client.clear();
    open(service, `/compare${saved}`);
    expect(await screen.findByLabelText("历史版本材料解析正文")).toHaveTextContent("历史合同质保36个月");
    expect(new URLSearchParams(screen.getByLabelText("路由查询").textContent ?? "").get("right_attempt_id")).toBe("attempt-y");
  });
  it("keeps the latest run identity in expandable technical details on the entry page", async () => {
    const { user } = openEntry();
    const row = (await screen.findByText("专线合同修订")).closest("tr")!;
    const id = within(row).getByText("run-z", { selector: "code" });
    expect(id).not.toBeVisible();
    await user.click(within(row).getByText("运行标识", { selector: "summary" }));
    expect(id).toBeVisible();
    expect(within(row).getByRole("link", { name: "打开审查" })).toHaveAttribute("href", "/cases/case-main/reviews/run-z?attempt_id=attempt-z-2");
  });

  it("retains an unrecognized latest run state for diagnosis", async () => {
    const service = new CompareService();
    service.data.runs.find((item) => item.id === "run-z")!.state = "completed·";
    service.data.cases[1]!.business_status = "archived（旧）";
    const { user } = openEntry(service);
    const row = (await screen.findByText("专线合同修订")).closest("tr")!;
    expect(within(row).getByText("状态未识别")).toBeVisible();
    const raw = within(row).getByText("completed·", { selector: "code" });
    expect(raw).not.toBeVisible();
    await user.click(within(row).getByText("运行状态编码", { selector: "summary" }));
    expect(raw).toBeVisible();
    expect(within(row).getByText("业务状态待核对")).toBeVisible();
    const businessState = within(row).getByText("archived（旧）", { selector: "code" });
    expect(businessState).not.toBeVisible();
    await user.click(within(row).getByText("业务状态编码", { selector: "summary" }));
    expect(businessState).toBeVisible();
    expect(within(row).getByRole("link", { name: "打开审查" })).toHaveAttribute("href", "/cases/case-main/reviews/run-z?attempt_id=attempt-z-2");
  });

  it("defaults to a case with multiple runs, resolves timestamp ties and selects the active contract", async () => {
    const { service } = open();
    await screen.findByLabelText("当前版本材料解析正文");
    await screen.findByLabelText("历史版本材料解析正文");
    expect(material("历史版本")).toHaveAttribute(
      "data-document-version-id",
      "draft-y",
    );
    expect(material("当前版本")).toHaveTextContent("质保期为24个月。");
    expect(material("历史版本")).toHaveTextContent("历史合同质保36个月。");
    expect(
      screen.getByRole("link", { name: "打开审查与决定" }),
    ).toHaveAttribute("href", "/cases/case-main/reviews/run-z?attempt_id=attempt-z-2");
    expect(service.requests).toContain(
      "/api/cases/case-main/reviews/run-z?attempt_id=attempt-z-2",
    );
    expect(service.requests).toContain(
      "/api/cases/case-main/reviews/run-y?attempt_id=attempt-y",
    );
    expect(within(riskPanel()).getByText("5 项实际发现")).toBeInTheDocument();
    for (const label of ["高风险", "中风险", "低风险", "提示"]) {
      expect(
        within(riskPanel()).getByText(label).parentElement,
      ).toHaveTextContent(`1${label}`);
    }
    expect(
      within(riskPanel())
        .getByRole("button", { name: /没有显式风险等级的发现/ })
        .closest("article"),
    ).toHaveAttribute("data-severity", "unrated");
    expect(screen.getByText("质保修订继续存在")).toBeInTheDocument();
    expect(screen.getByText(/旧决定\s*确认风险/)).toBeInTheDocument();
    expect(screen.getByText(/旧决定\s*未报告/)).toBeInTheDocument();
  });

  it("honors URL run choices, changes each side independently and clears a previous finding selection", async () => {
    const { user, service } = open(
      new CompareService(),
      "/compare?case_id=case-main&left_run_id=run-y&right_run_id=run-a",
    );
    await screen.findByLabelText("当前版本材料解析正文");
    await screen.findByLabelText("历史版本材料解析正文");
    expect(material("当前版本")).toHaveAttribute(
      "data-document-version-id",
      "draft-y",
    );
    expect(material("历史版本")).toHaveAttribute(
      "data-document-version-id",
      "draft-a",
    );
    expect(service.requests).toContain("/api/cases/case-main/reviews/run-a");
    expect(
      screen.getByText(/无法验证修订对照/),
    ).toBeInTheDocument();
    await chooseRun(user, "当前版本", /审查标识 run-z/);
    await user.click(
      await screen.findByRole("button", { name: /质保期限低于采购要求/ }),
    );
    expect(material("当前版本")).toHaveAttribute(
      "data-document-version-id",
      "source-z",
    );
    await chooseRun(user, "当前版本", /审查标识 run-y/);
    await waitFor(() =>
      expect(material("当前版本")).toHaveAttribute(
        "data-document-version-id",
        "draft-y",
      ),
    );
    expect(
      screen.queryByRole("link", { name: "打开原文与审查判断" }),
    ).not.toBeInTheDocument();
    await chooseRun(user, "当前版本", /审查标识 run-z/);
    await waitFor(() =>
      expect(material("当前版本")).toHaveAttribute(
        "data-document-version-id",
        "draft-z",
      ),
    );
    expect(
      screen.getByRole("button", { name: /质保期限低于采购要求/ }),
    ).toHaveAttribute("aria-pressed", "false");
    await chooseRun(user, "历史版本", /审查标识 run-z/);
    await waitFor(() =>
      expect(material("历史版本")).toHaveAttribute(
        "data-document-version-id",
        "draft-z",
      ),
    );
    expect(
      screen.getByRole("link", { name: "打开审查与决定" }),
    ).toHaveAttribute("href", "/cases/case-main/reviews/run-z?attempt_id=attempt-z-2");
  });

  it("filters the comparison entry by case number and links each contract into its own comparison", async () => {
    const { user, service } = openEntry(new CompareService());
    expect(await screen.findByText("专线合同修订")).toBeInTheDocument();
    expect(screen.getByText("可对照合同").closest("li")).toHaveTextContent(
      "可对照合同1",
    );
    const search = screen.getByRole("searchbox", { name: "搜索合同" });
    await user.type(search, " N-002 ");
    expect(screen.getByText("专线合同修订")).toBeInTheDocument();
    expect(screen.queryByText("暂无审查案件")).not.toBeInTheDocument();
    const mainRow = screen.getByText("专线合同修订").closest("tr")!;
    expect(within(mainRow).getByRole("link", { name: "开始对照" })).toHaveAttribute(
      "href",
      "/compare?case_id=case-main",
    );
    expect(within(mainRow).getByRole("link", { name: "打开审查" })).toHaveAttribute(
      "href",
      "/cases/case-main/reviews/run-z?attempt_id=attempt-z-2",
    );
    await user.clear(search);
    await user.type(search, "没有匹配的编号");
    expect(screen.getByText("没有匹配的合同")).toBeInTheDocument();
    await user.clear(search);
    const emptyRow = screen.getByText("暂无审查案件").closest("tr")!;
    expect(within(emptyRow).getByRole("link", { name: "开始对照" })).toHaveAttribute(
      "href",
      "/compare?case_id=case-empty",
    );
    expect(within(emptyRow).getByRole("link", { name: "案件详情" })).toHaveAttribute(
      "href",
      "/cases/case-empty",
    );
    expect(
      service.requests.some((path) => path.includes("case-empty/reviews")),
    ).toBe(false);
  });

  it("does not silently substitute another case for an unavailable URL identity", async () => {
    const { service } = open(
      new CompareService(),
      "/compare?case_id=removed-case&left_run_id=removed-run&right_run_id=removed-prior",
    );
    await screen.findByText("案件不可用");
    expect(service.requests.some((path) => path.includes("/reviews/"))).toBe(false);
    expect(screen.getByRole("link", { name: "案件详情" })).toHaveAttribute(
      "href",
      "/cases",
    );
  });

  it("keeps a URL-pinned attempt instead of silently switching to the run's newer attempt", async () => {
    const { service } = open(new CompareService(), "/compare?case_id=case-main&left_run_id=run-z&left_attempt_id=attempt-z-1");
    expect(await screen.findByRole("alert")).toHaveTextContent("无法读取此运行");
    expect(service.requests).toContain("/api/cases/case-main/reviews/run-z?attempt_id=attempt-z-1");
    expect(service.requests).not.toContain("/api/cases/case-main/reviews/run-z?attempt_id=attempt-z-2");
    expect(screen.queryByRole("link", { name: "打开审查与决定" })).not.toBeInTheDocument();
    expect(screen.getByLabelText("路由查询")).toHaveTextContent("left_attempt_id=attempt-z-1");
  });
});

describe("ComparePage evidence and actual results", () => {
  it("shows readable revision labels and keeps their exact identities available", async () => {
    const { user } = open();
    const row = (await screen.findByText("质保修订继续存在")).closest("li")!;
    expect(within(row).getByText("质保，冲突")).toBeVisible();
    const toggle = within(row).getByText("修订技术详情", { selector: "summary" });
    const raw = toggle.closest("details")!.querySelector("code")!;
    expect(raw).not.toBeVisible();
    await user.click(toggle);
    expect(raw).toBeVisible();
    expect(JSON.parse(raw.textContent!)).toMatchObject({ domain: "质保", relation: "conflict", finding_id: "finding-rule", previous_finding_id: "prior-finding" });
  });

  it("keeps rule codes out of the main explanation and preserves them in details", async () => {
    const { user } = open();
    await screen.findByText("质保修订继续存在");
    await user.click(screen.getByRole("tab", { name: "规则依据" }));
    const row = screen.getByText("合同质保低于采购要求").closest("li")!;
    expect(within(row).queryByText(/规则 WARRANTY-001/)).not.toBeInTheDocument();
    expect(within(row).getByText("质保，冲突")).toBeVisible();
    expect(within(row).getByText("审查项目待核对")).toBeVisible();
    const toggle = within(row).getByText("规则技术详情", { selector: "summary" });
    const raw = toggle.closest("details")!.querySelector("code")!;
    expect(raw).not.toBeVisible();
    await user.click(toggle);
    expect(raw).toBeVisible();
    expect(JSON.parse(raw.textContent!)).toMatchObject({ domain: "质保", relation: "conflict", rule_id: "WARRANTY-001", rule_version: "2.1", capability_id: "minimum_warranty" });
  });

  it("keeps unknown mixed codes diagnostic while preserving claims, reasons and known capabilities", async () => {
    const service = new CompareService();
    const current = service.reviews.get("run-z")!;
    Object.assign(current.revisions![0]!, { mark: "changed（旧）", domain: "warranty（旧）", relation: "conflict·", claim: "ENT（测试）股份有限公司原文" });
    const unknown = current.evaluations![0]!;
    Object.assign(unknown, { domain: "warranty（旧）", relation: "conflict·", capability_id: "dedicated_line.minimum_bandwidth（旧）", rule_id: "RULE（旧）", reason: "合同原文中的真实理由" });
    current.evaluations!.push({ ...unknown, id: "evaluation-known", domain: "technical", relation: "compatible", capability_id: "dedicated_line.minimum_bandwidth", rule_id: "RULE-KNOWN", reason: "供应商提供的原始说明" });
    const { user } = open(service);
    const revision = (await screen.findByText("ENT（测试）股份有限公司原文")).closest("li")!;
    expect(within(revision).getByText("修订类型待核对")).toBeVisible();
    expect(within(revision).getByText("领域待核对，关系未识别")).toBeVisible();
    const revisionToggle = within(revision).getByText("修订技术详情", { selector: "summary" });
    const revisionRaw = revisionToggle.closest("details")!.querySelector("code")!;
    expect(revisionRaw).not.toBeVisible();
    await user.click(revisionToggle);
    expect(revisionRaw).toBeVisible();
    expect(JSON.parse(revisionRaw.textContent!)).toMatchObject({ mark: "changed（旧）", domain: "warranty（旧）", relation: "conflict·" });
    await user.click(screen.getByRole("tab", { name: "规则依据" }));
    const rule = screen.getByText("合同原文中的真实理由").closest("li")!;
    expect(within(rule).getByText("领域待核对，关系未识别")).toBeVisible();
    expect(within(rule).getByText("审查项目待核对")).toBeVisible();
    const ruleToggle = within(rule).getByText("规则技术详情", { selector: "summary" });
    const ruleRaw = ruleToggle.closest("details")!.querySelector("code")!;
    expect(ruleRaw).not.toBeVisible();
    await user.click(ruleToggle);
    expect(ruleRaw).toBeVisible();
    expect(JSON.parse(ruleRaw.textContent!)).toMatchObject({ domain: "warranty（旧）", relation: "conflict·", capability_id: "dedicated_line.minimum_bandwidth（旧）", rule_id: "RULE（旧）" });
    const known = screen.getByText("供应商提供的原始说明").closest("li")!;
    expect(within(known).getByText("技术与服务，一致")).toBeVisible();
    expect(within(known).getByText("最低保障带宽")).toBeVisible();
  });

  it("distinguishes human and unknown finding origins without presenting them as model advice", async () => {
    const service = new CompareService();
    const findings = service.reviews.get("run-z")!.findings;
    findings[0]!.origin = "manual";
    findings[1]!.origin = "model（旧）";
    const { user } = open(service);
    expect(await screen.findByRole("button", { name: /质保期限低于采购要求，人工，冲突/ })).toBeVisible();
    const unknown = screen.getByRole("button", { name: /模型建议复核付款安排，来源待核对，冲突/ });
    await user.click(unknown);
    const toggle = within(riskPanel()).getByText("发现技术详情", { selector: "summary" });
    const raw = toggle.closest("details")!.querySelector("code")!;
    expect(raw).not.toBeVisible();
    await user.click(toggle);
    expect(raw).toBeVisible();
    expect(JSON.parse(raw.textContent!)).toMatchObject({ origin: "model（旧）", relation: "conflict", finding_id: "finding-model" });
  });

  it("keeps the unknown latest run state separate from the selected material state", async () => {
    const service = new CompareService();
    service.data.runs.find((item) => item.id === "run-z")!.state = "completed·";
    service.reviews.get("run-z")!.documents.find((item) => item.id === "draft-z")!.parse_status = "ready·";
    const { user } = open(service);
    await screen.findByText("质保修订继续存在");
    const runSelect = screen.getByRole("combobox", { name: "当前版本" }).closest(".compare-run-select")!;
    const runRaw = within(runSelect as HTMLElement).getByText("completed·", { selector: "code" });
    expect(runRaw).not.toBeVisible();
    await user.click(within(runSelect as HTMLElement).getByText("审查状态编码", { selector: "summary" }));
    expect(runRaw).toBeVisible();
    const pane = material("当前版本").closest("article")!;
    await user.click(within(pane).getByText(/版本与来源，\s*解析状态未识别/));
    const toggle = within(pane).getByText("材料技术详情", { selector: "summary" });
    const raw = toggle.closest("details")!.querySelector("code")!;
    expect(raw).not.toBeVisible();
    await user.click(toggle);
    expect(raw).toBeVisible();
    expect(JSON.parse(raw.textContent!)).toMatchObject({ document_version_id: "draft-z", parse_status: "ready·", run_state: "completed" });
  });

  it("opens the exact evidence source, highlights its frozen quote and exposes provenance and decision links", async () => {
    const { user } = open();
    await user.click(
      await screen.findByRole("button", { name: /质保期限低于采购要求/ }),
    );
    await screen.findByLabelText("历史版本材料解析正文");
    const body = material("当前版本");
    const pane = body.closest("article")!;
    expect(body).toHaveAttribute("data-document-version-id", "source-z");
    expect(
      within(body).getByText("36个月", { selector: "mark" }),
    ).toBeInTheDocument();
    expect(material("历史版本").querySelector("mark")).toBeNull();
    expect(screen.getByText(/依据版本 2，\s*第4条/)).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "打开原文与审查判断" }),
    ).toHaveAttribute(
      "href",
      "/cases/case-main/reviews/run-z?attempt_id=attempt-z-2&finding=finding-rule",
    );
    await user.click(within(pane).getByText(/版本与来源，\s*已解析/));
    expect(within(pane).getByText("材料名称").nextElementSibling).toHaveTextContent("source-z.txt");
    expect(within(pane).queryByText("parse-source-z")).not.toBeInTheDocument();
    expect(within(pane).queryByText("digest-source-z")).not.toBeInTheDocument();
    const materialDetails = within(pane).getByText("材料技术详情", { selector: "summary" });
    const materialRaw = materialDetails.closest("details")!.querySelector("code")!;
    expect(materialRaw).not.toBeVisible();
    await user.click(materialDetails);
    expect(materialRaw).toBeVisible();
    expect(JSON.parse(materialRaw.textContent!)).toMatchObject({ document_version_id: "source-z", document_digest: "digest-source-z", parsed_document_metadata_id: "parse-source-z", parse_status: "ready", run_state: "completed" });
    expect(
      within(pane).getByRole("link", { name: "读取原件" }),
    ).toHaveAttribute(
      "href",
      "/api/cases/case-main/documents/source-z/original",
    );
    await user.click(
      screen.getByRole("button", { name: /模型建议复核付款安排/ }),
    );
    expect(material("当前版本")).toHaveAttribute(
      "data-document-version-id",
      "draft-z",
    );
    expect(
      within(material("当前版本")).getByText("24个月", { selector: "mark" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /模型建议复核付款安排/ }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(
      screen.getByRole("button", { name: /质保期限低于采购要求/ }),
    ).toHaveAttribute("aria-pressed", "false");
  });

  it.each([
    ["matching", "parse-source-z", true],
    ["different", "parse-other", false],
  ] as const)(
    "uses the %s public parse identity without technical metadata",
    async (_label, parsedDocumentId, matches) => {
      const service = new CompareService();
      const workspace = service.reviews.get("run-z")!;
      const source = workspace.documents.find((item) => item.id === "source-z")!;
      source.parsed_document = null;
      source.parsed_document_id = parsedDocumentId;
      source.digest = "";
      workspace.findings[0]!.evidence[0]!.normalized_digest = null;
      const { user } = open(service);
      await user.click(
        await screen.findByRole("button", { name: /质保期限低于采购要求/ }),
      );
      const body = material("当前版本");
      expect(body).toHaveAttribute(
        "data-document-version-id",
        matches ? "source-z" : "draft-z",
      );
      if (matches) {
        expect(
          within(body).getByText("36个月", { selector: "mark" }),
        ).toBeInTheDocument();
        const pane = body.closest("article")!;
        await user.click(within(pane).getByText(/版本与来源，\s*已解析/));
        expect(within(pane).queryByText("parse-source-z")).not.toBeInTheDocument();
        expect(within(pane).getByText("材料名称").nextElementSibling).toHaveTextContent("source-z.txt");
        expect(within(pane).queryByText("原件摘要")).not.toBeInTheDocument();
      } else expect(body.querySelector("mark")).toBeNull();
    },
  );

  it("preserves mismatching quote snapshots and lets the reader choose other material on both sides", async () => {
    const { user } = open();
    await user.click(
      await screen.findByRole("button", { name: /历史引用坐标已失效/ }),
    );
    expect(material("当前版本").querySelector("mark")).toBeNull();
    expect(
      screen.getByText("引用原文与当前正文不一致，未跳转到高亮位置。"),
    ).toBeInTheDocument();
    expect(
      within(riskPanel()).getByText(/48个月/, { selector: "blockquote" }),
    ).toBeInTheDocument();
    await chooseOption(
      user,
      within(material("当前版本").closest("article")!).getByRole("combobox", {
        name: "材料",
      }),
      "draft-z-old.txt",
    );
    expect(material("当前版本")).toHaveTextContent("质保期为12个月。");
    expect(material("当前版本").querySelector("mark")).toBeNull();
    await chooseOption(
      user,
      within(material("历史版本").closest("article")!).getByRole("combobox", {
        name: "材料",
      }),
      "reference-y.txt",
    );
    expect(material("历史版本")).toHaveTextContent("历史采购说明。");
  });

  it.each(["missing document", "incompatible parse"])(
    "does not highlight or switch to an unrelated source for %s",
    async (source) => {
      const service = new CompareService();
      service.reviews.get("run-z")!.findings = [
        finding({
          evidence: [
            anchor(
              "source-z",
              source === "missing document"
                ? { document_version_id: "unavailable-version" }
                : {
                    parsed_document_id: "parse-from-another-attempt",
                    normalized_digest: "normalized-source-z",
                  },
            ),
          ],
        }),
      ];
      const { user } = open(service);
      await user.click(
        await screen.findByRole("button", { name: /质保期限低于采购要求/ }),
      );
      expect(material("当前版本")).toHaveAttribute(
        "data-document-version-id",
        "draft-z",
      );
      expect(material("当前版本").querySelector("mark")).toBeNull();
      expect(
        within(riskPanel()).getByText(/36个月/, { selector: "blockquote" }),
      ).toBeInTheDocument();
      expect(
        screen.getByRole("link", { name: "打开原文与审查判断" }),
      ).toBeInTheDocument();
    },
  );

  it.each([
    {
      kind: "missing",
      reason: "找不到对应来源，不能把这条引用当成已验证证据。",
    },
    { kind: "unlocatable", reason: "冻结来源没有可用坐标" },
    {
      kind: "no text",
      reason: "正文尚未返回。下面只显示保存下来的引用快照，没有在文档中定位。",
    },
  ])(
    "keeps saved evidence separate from a verified highlight when $kind",
    async ({ kind, reason }) => {
      const service = new CompareService();
      const current = service.reviews.get("run-z")!;
      current.findings = [
        finding({
          evidence: [
            anchor("draft-z", {
              quote: "24个月",
              status:
                kind === "missing"
                  ? "missing"
                  : kind === "unlocatable"
                    ? "unlocatable"
                    : "located",
              mismatch_reason:
                kind === "unlocatable" ? "冻结来源没有可用坐标" : null,
            }),
          ],
        }),
      ];
      if (kind === "no text")
        current.documents.find((item) => item.id === "draft-z")!.text = null;
      const { user } = open(service);
      await user.click(
        await screen.findByRole("button", { name: /质保期限低于采购要求/ }),
      );
      expect(material("当前版本").querySelector("mark")).toBeNull();
      expect(screen.getByText(reason)).toBeInTheDocument();
      expect(
        within(riskPanel()).getByText(/24个月/, { selector: "blockquote" }),
      ).toBeInTheDocument();
      if (kind === "no text")
        expect(
          within(material("当前版本")).getByText(
            "该运行没有可展示的解析正文。",
          ),
        ).toBeInTheDocument();
    },
  );

  it("shows rule and public attempt facts while keeping diagnostics unavailable", async () => {
    const { user } = open();
    await screen.findByText("质保修订继续存在");
    await user.click(screen.getByRole("tab", { name: "规则依据" }));
    expect(screen.getByRole("tab", { name: "规则依据" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByText("审查项目待核对")).toBeVisible();
    expect(screen.getByText("合同质保低于采购要求")).toBeInTheDocument();
    expect(screen.getByText("没有获取到这次审查的规则检查记录。")).toBeInTheDocument();
    expect(screen.queryByText("质保修订继续存在")).not.toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "执行记录" }));
    expect(
      screen.getByText(/当前第 2 次审查，\s*执行完成/),
    ).toBeInTheDocument();
    expect(screen.getByText(/尝试数 2，\s*事件数 不可用/)).toBeInTheDocument();
    expect(
      screen.getByText(/第 1 次，\s*失败，\s*结束时间未报告/),
    ).toBeInTheDocument();
    expect(screen.getByText(/当前第 1 次审查，\s*执行完成/)).toBeInTheDocument();
    expect(screen.getByTitle("run-z")).not.toBeVisible();
    expect(screen.getByTitle("attempt-z-2")).not.toBeVisible();
    await user.click(screen.getByRole("tab", { name: "模型用量" }));
    const left = screen
      .getByRole("heading", { name: "左侧运行" })
      .closest("section")!;
    const right = screen
      .getByRole("heading", { name: "右侧运行" })
      .closest("section")!;
    expect(
      within(left)
        .getAllByRole("definition")
        .map((item) => item.textContent),
    ).toEqual(["不可用", "不可用", "不可用", "不可用", "不可用"]);
    expect(
      within(right)
        .getAllByRole("definition")
        .map((item) => item.textContent),
    ).toEqual(["不可用", "不可用", "不可用", "不可用", "不可用"]);
    expect(within(left).getByRole("link", { name: "审查工作区" })).toHaveAttribute(
      "href",
      "/cases/case-main/reviews/run-z?attempt_id=attempt-z-2",
    );
    await user.click(screen.getByRole("tab", { name: "修订证据" }));
    expect(screen.getByText("质保修订继续存在")).toBeInTheDocument();
  });

  it.each(["unavailable", "for another attempt"] as const)(
    "shows public attempt history when diagnostics are %s",
    async (diagnostics) => {
      const service = new CompareService();
      const current = service.reviews.get("run-z")!;
      if (diagnostics === "unavailable") current.execution = null;
      else current.execution!.attempt = attempt("attempt-other", 3);
      const { user } = open(service);
      await screen.findByText("质保修订继续存在");
      await user.click(screen.getByRole("tab", { name: "执行记录" }));
      const left = screen
        .getByRole("heading", { name: "左侧运行" })
        .closest("section")!;
      expect(
        within(left).getByText(/当前第 2 次审查，\s*执行完成/),
      ).toBeInTheDocument();
      expect(
        within(left).getByText(/尝试数 2，\s*事件数 不可用/),
      ).toBeInTheDocument();
      expect(
        within(left).getByText(/第 1 次，\s*失败/),
      ).toBeInTheDocument();
      await user.click(screen.getByRole("tab", { name: "模型用量" }));
      const usage = screen
        .getByRole("heading", { name: "左侧运行" })
        .closest("section")!;
      expect(
        within(usage)
          .getAllByRole("definition")
          .map((item) => item.textContent),
      ).toEqual(["不可用", "不可用", "不可用", "不可用", "不可用"]);
    },
  );

  it.each([
    {
      reason: "解析配置不兼容",
      expected: "服务端没有生成修订对照：解析配置不兼容",
    },
    { reason: null, expected: "所选运行没有返回可比较的修订线索。" },
  ])(
    "explains missing linked comparisons with reason $reason",
    async ({ reason, expected }) => {
      const service = new CompareService();
      service.reviews.get("run-z")!.revision_comparison_reason = reason;
      service.reviews.get("run-z")!.revisions = [];
      open(service);
      expect(await screen.findByText(expected)).toBeInTheDocument();
      expect(screen.queryByText("质保修订继续存在")).not.toBeInTheDocument();
    },
  );

  it("does not attribute revisions or prior decisions to a different attempt of the same previous run", async () => {
    const service = new CompareService();
    const current = service.reviews.get("run-z")!;
    Object.assign(current, { revision_comparison_source: {
      current: { case_id: "case-main", run_id: "run-z", attempt_id: "attempt-z-2" },
      previous: { case_id: "case-main", run_id: "run-y", attempt_id: "attempt-y-retry" },
    } });
    service.frozenAttempts.set("run-y:attempt-y", service.reviews.get("run-y")!);
    service.data.runs.find((item) => item.id === "run-y")!.attempt_id = "attempt-y-retry";
    open(service, "/compare?case_id=case-main&left_run_id=run-z&left_attempt_id=attempt-z-2&right_run_id=run-y&right_attempt_id=attempt-y");
    await screen.findByText("历史合同质保36个月。");
    expect(screen.queryByText("质保修订继续存在")).not.toBeInTheDocument();
    expect(screen.queryByText(/旧决定 确认风险/)).not.toBeInTheDocument();
    expect(screen.getByText(/无法验证修订对照/)).toBeVisible();
  });

  it("keeps the original findings and documents when an older response has no revision source", async () => {
    const service = new CompareService();
    delete service.reviews.get("run-z")!.revision_comparison_source;
    open(service);
    expect(await screen.findByText("历史合同质保36个月。")).toBeVisible();
    expect(screen.getByText(/无法验证修订对照/)).toBeVisible();
    expect(screen.queryByText("质保修订继续存在")).not.toBeInTheDocument();
    expect(screen.getByText("质保期限低于采购要求")).toBeVisible();
  });
});

describe("ComparePage unavailable data", () => {
  it.each(["当前版本", "历史版本"] as const)("shows the selected attempt status on the %s side", async (side) => {
    const service = new CompareService();
    const workspace = service.reviews.get(side === "当前版本" ? "run-z" : "run-y")!;
    workspace.run.state = "failed";
    workspace.review_attempts!.attempt.state = "failed";
    open(service);
    const pane = (await screen.findByLabelText(`${side}材料解析正文`)).closest("article")!;
    expect(within(pane).getByText("失败")).toBeVisible();
    const chain = screen.getByRole("list", { name: "对照链路" });
    expect(within(chain).getByText(side).closest("li")).toHaveTextContent("失败");
    expect(screen.getByRole("combobox", { name: side }).closest(".compare-run-select")).toHaveTextContent("审查当前状态 执行完成");
  });

  it("does not infer completion from a conflicting selected attempt status", async () => {
    const service = new CompareService();
    service.reviews.get("run-z")!.review_attempts!.attempt.state = "failed";
    open(service);
    const pane = (await screen.findByLabelText("当前版本材料解析正文")).closest("article")!;
    expect(within(pane).getByText("状态未读取")).toBeVisible();
    const chain = screen.getByRole("list", { name: "对照链路" });
    expect(within(chain).getByText("当前版本").closest("li")).toHaveTextContent("状态未读取");
  });

  it.each([
    ["left", "missing"], ["left", "null"], ["left", "empty selected attempt"],
    ["right", "missing"], ["right", "null"], ["right", "empty selected attempt"],
  ] as const)("does not present an exact %s attempt when its response is %s", async (side, shape) => {
    const service = new CompareService();
    const workspace = service.reviews.get(side === "left" ? "run-z" : "run-y")!;
    if (shape === "missing") Reflect.deleteProperty(workspace, "review_attempts");
    else if (shape === "null") workspace.review_attempts = null;
    else Object.assign(workspace, { review_attempts: { attempt: null, attempts: [] } });
    open(service, "/compare?case_id=case-main&left_run_id=run-z&left_attempt_id=attempt-z-2&right_run_id=run-y&right_attempt_id=attempt-y");
    await screen.findByLabelText(`${side === "left" ? "历史版本" : "当前版本"}材料解析正文`);
    expect(await screen.findByText(/未报告执行身份|身份与所选记录不一致/)).toBeVisible();
    expect(screen.queryByLabelText(`${side === "left" ? "当前版本" : "历史版本"}材料解析正文`)).not.toBeInTheDocument();
    expect(within(screen.getByRole("list", { name: "对照链路" })).getByText(side === "left" ? "当前版本" : "历史版本").closest("li")).toHaveTextContent("状态未读取");
    if (side === "left") expect(screen.queryByRole("button", { name: /质保期限低于采购要求/ })).not.toBeInTheDocument();
  });

  it("does not display findings when an initial run-only response leaves the execution identity unknown", async () => {
    const service = new CompareService();
    service.data.runs.find((item) => item.id === "run-z")!.attempt_id = null;
    Reflect.deleteProperty(service.reviews.get("run-z")!, "review_attempts");
    open(service, "/compare?case_id=case-main&left_run_id=run-z&right_run_id=run-y&right_attempt_id=attempt-y");
    await screen.findByLabelText("历史版本材料解析正文");
    expect(await screen.findByText(/未报告执行身份/)).toBeVisible();
    expect(screen.queryByLabelText("当前版本材料解析正文")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /质保期限低于采购要求/ })).not.toBeInTheDocument();
    expect(screen.getByLabelText("路由查询")).not.toHaveTextContent("left_run_only=1");
  });

  it("keeps same-document parse identities distinct for selection, evidence, and later updates", async () => {
    const service = new CompareService();
    const workspace = service.reviews.get("run-z")!;
    const draft = workspace.documents.find((item) => item.id === "draft-z")!;
    draft.parsed_document_id = "parse-draft-z";
    draft.parsed_document = null;
    const first = document("shared", "procurement", "第一份解析正文", { parsed_document_id: "parse-shared-1", parsed_document: null });
    const second = document("shared", "procurement", "第二份解析正文", { parsed_document_id: "parse-shared-2", parsed_document: null });
    workspace.documents = [draft, first, second];
    workspace.snapshot = { document_ids: ["draft-z", "shared"], rule_set_hash: "", playbook_hash: "", model_ref: null, comparator_ref: null };
    workspace.review_attempts!.attempt.parsed_document_ids = ["parse-draft-z", "parse-shared-1", "parse-shared-2"];
    workspace.findings = [finding({ evidence: [anchor("shared", { parsed_document_id: "parse-shared-2", normalized_digest: null, quote: "第二份" })] })];
    const { user, client } = open(service);
    const pane = (await screen.findByLabelText("当前版本材料解析正文")).closest("article")!;
    const select = within(pane).getByRole("combobox", { name: "材料" });
    await chooseOption(user, select, /shared\.txt.*parse-shared-1/);
    expect(material("当前版本")).toHaveTextContent("第一份解析正文");
    await chooseOption(user, select, /shared\.txt.*parse-shared-2/);
    expect(material("当前版本")).toHaveTextContent("第二份解析正文");
    await chooseOption(user, select, /shared\.txt.*parse-shared-1/);
    await user.click(screen.getByRole("button", { name: /质保期限低于采购要求/ }));
    expect(material("当前版本")).toHaveTextContent("第二份解析正文");
    await chooseOption(user, select, /shared\.txt.*parse-shared-1/);
    const changed = { ...workspace, documents: [draft, second], review_attempts: {
      ...workspace.review_attempts!, attempt: { ...workspace.review_attempts!.attempt, parsed_document_ids: ["parse-draft-z", "parse-shared-2"] },
    } };
    act(() => client.setQueryData(queryKeys.review("case-main", "run-z", "attempt-z-2"), changed));
    await waitFor(() => expect(material("当前版本")).toHaveTextContent("质保期为24个月。"));
    act(() => client.setQueryData(queryKeys.review("case-main", "run-z", "attempt-z-2"), workspace));
    await waitFor(() => expect(material("当前版本")).toHaveTextContent("质保期为24个月。"));
  });

  it("excludes the other attempt's parse of the same document and does not follow its evidence", async () => {
    const service = new CompareService();
    const workspace = service.reviews.get("run-z")!;
    const draft = workspace.documents.find((item) => item.id === "draft-z")!;
    const current = document("shared", "procurement", "本次冻结解析", { parsed_document_id: "parse-current", parsed_document: null });
    const previous = document("shared", "procurement", "前序冻结解析", { parsed_document_id: "parse-previous", parsed_document: null });
    workspace.documents = [draft, current, previous];
    workspace.snapshot = { document_ids: ["draft-z", "shared"], rule_set_hash: "", playbook_hash: "", model_ref: null, comparator_ref: null };
    workspace.review_attempts!.attempt.parsed_document_ids = ["parse-draft-z", "parse-current"];
    workspace.findings = [finding({ evidence: [anchor("shared", { parsed_document_id: "parse-previous", normalized_digest: null, quote: "前序" })] })];
    const { user } = open(service);
    const pane = (await screen.findByLabelText("当前版本材料解析正文")).closest("article")!;
    const select = within(pane).getByRole("combobox", { name: "材料" });
    await chooseOption(user, select, "shared.txt");
    expect(material("当前版本")).toHaveTextContent("本次冻结解析");
    expect(material("当前版本")).toHaveAttribute("data-parsed-document-id", "parse-current");
    await user.click(screen.getByRole("button", { name: /质保期限低于采购要求/ }));
    expect(material("当前版本")).toHaveTextContent("本次冻结解析");
    expect(material("当前版本").querySelector("mark")).toBeNull();
    expect(screen.queryByText("前序冻结解析")).not.toBeInTheDocument();
  });

  it("does not guess which parse an ambiguous historical citation refers to", async () => {
    const service = new CompareService();
    const workspace = service.reviews.get("run-z")!;
    const draft = workspace.documents.find((item) => item.id === "draft-z")!;
    workspace.documents = [draft,
      document("shared", "procurement", "共同引用 第一版", { parsed_document_id: "parse-1", parsed_document: null }),
      document("shared", "procurement", "共同引用 第二版", { parsed_document_id: "parse-2", parsed_document: null })];
    workspace.snapshot = { document_ids: ["draft-z", "shared"], rule_set_hash: "", playbook_hash: "", model_ref: null, comparator_ref: null };
    workspace.review_attempts!.attempt.parsed_document_ids = ["parse-draft-z", "parse-1", "parse-2"];
    workspace.findings = [finding({ evidence: [anchor("shared", { parsed_document_id: null, normalized_digest: null, quote: "共同引用" })] })];
    const { user } = open(service);
    const pane = (await screen.findByLabelText("当前版本材料解析正文")).closest("article")!;
    await user.click(screen.getByRole("button", { name: /质保期限低于采购要求/ }));
    expect(material("当前版本")).toHaveAttribute("data-document-version-id", "draft-z");
    expect(within(riskPanel()).getByText(/引用未区分解析版本/)).toBeVisible();
    await chooseOption(user, within(pane).getByRole("combobox", { name: "材料" }), /shared\.txt.*parse-1/);
    expect(material("当前版本")).toHaveTextContent("共同引用 第一版");
    expect(material("当前版本").querySelector("mark")).toBeNull();
  });

  it("does not revive a material selection when the same side changes runs and returns", async () => {
    const { user } = open();
    const pane = (await screen.findByLabelText("当前版本材料解析正文")).closest("article")!;
    await chooseOption(user, within(pane).getByRole("combobox", { name: "材料" }), "source-z.txt");
    expect(material("当前版本")).toHaveAttribute("data-document-version-id", "source-z");
    await chooseRun(user, "当前版本", /审查标识 run-y/);
    await waitFor(() => expect(material("当前版本")).toHaveAttribute("data-document-version-id", "draft-y"));
    await user.click(screen.getByRole("button", { name: "测试后退" }));
    await waitFor(() => expect(material("当前版本")).toHaveAttribute("data-document-version-id", "draft-z"));
  });

  it("ignores a late response from the previously selected side", async () => {
    const service = new CompareService();
    const delayed = Promise.withResolvers<void>();
    service.reviewGates.set("run-y", delayed);
    const { user } = open(service);
    const pane = (await screen.findByLabelText("当前版本材料解析正文")).closest("article")!;
    await chooseOption(user, within(pane).getByRole("combobox", { name: "材料" }), "source-z.txt");
    await chooseRun(user, "当前版本", /审查标识 run-y/);
    await chooseRun(user, "当前版本", /审查标识 run-z/);
    await waitFor(() => expect(material("当前版本")).toHaveAttribute("data-document-version-id", "draft-z"));
    await act(async () => delayed.resolve());
    expect(await screen.findByLabelText("历史版本材料解析正文")).toHaveAttribute("data-document-version-id", "draft-y");
    expect(material("当前版本")).toHaveAttribute("data-document-version-id", "draft-z");
  });

  it("shows unavailable material identity when the snapshot is missing", async () => {
    const service = new CompareService();
    service.reviews.get("run-z")!.snapshot = null;
    open(service);
    expect(await screen.findByText(/未报告可验证的冻结材料身份/)).toBeVisible();
    expect(screen.queryByLabelText("当前版本材料解析正文")).not.toBeInTheDocument();
    expect(await screen.findByLabelText("历史版本材料解析正文")).toBeVisible();
  });

  it("exposes only the original when duplicate rows contradict the same frozen parse identity", async () => {
    const service = new CompareService();
    const workspace = service.reviews.get("run-z")!;
    const draft = workspace.documents.find((item) => item.id === "draft-z")!;
    workspace.documents.push({ ...draft, text: "矛盾的解析正文" });
    const { user } = open(service);
    expect(await screen.findByText(/部分材料的解析身份有冲突/)).toBeVisible();
    const pane = (await screen.findByLabelText("当前版本材料解析正文")).closest("article")!;
    await chooseOption(user, within(pane).getByRole("combobox", { name: "材料" }), /draft-z\.txt.*仅原件/);
    expect(material("当前版本")).not.toHaveTextContent("质保期为24个月。");
    expect(material("当前版本")).not.toHaveTextContent("矛盾的解析正文");
    expect(within(pane).getByText(/未能确认本次执行的解析身份/)).toBeVisible();
    await user.click(within(pane).getByText(/版本与来源/));
    expect(within(pane).getByRole("link", { name: "读取原件" })).toHaveAttribute("href", "/api/cases/case-main/documents/draft-z/original");
  });

  it("does not display parsed text when an attempt omits the parse identity list", async () => {
    const service = new CompareService();
    Reflect.deleteProperty(service.reviews.get("run-z")!.review_attempts!.attempt, "parsed_document_ids");
    open(service);
    const pane = (await screen.findByLabelText("当前版本材料解析正文")).closest("article")!;
    expect(within(pane).getByText(/未能确认本次执行的解析身份/)).toBeVisible();
    expect(within(pane).queryByText("质保期为24个月。")).not.toBeInTheDocument();
  });

  it.each(["当前版本", "历史版本"] as const)("does not offer a previous attempt's extra document on the %s side", async (side) => {
    const service = new CompareService();
    const workspace = service.reviews.get(side === "当前版本" ? "run-z" : "run-y")!;
    workspace.snapshot = { document_ids: workspace.documents.map((item) => item.id), rule_set_hash: "", playbook_hash: "", model_ref: null, comparator_ref: null };
    for (const item of workspace.documents) {
      item.parsed_document_id = item.parsed_document?.id ?? null;
      item.parsed_document = null;
      Reflect.deleteProperty(item, "digest");
    }
    workspace.review_attempts!.attempt.parsed_document_ids = workspace.documents.map((item) => item.parsed_document_id!);
    const extra = document(`extra-${side}`, "draft_contract", "前序附加材料");
    extra.parsed_document_id = extra.parsed_document!.id;
    extra.parsed_document = null;
    extra.superseded = true;
    workspace.documents.push(extra);
    const { user } = open(service);
    const pane = (await screen.findByLabelText(`${side}材料解析正文`)).closest("article")!;
    await user.click(within(pane).getByRole("combobox", { name: "材料" }));
    expect(screen.queryByRole("option", { name: `extra-${side}.txt` })).not.toBeInTheDocument();
    if (side === "当前版本") expect(screen.getByRole("option", { name: "draft-z-old.txt" })).toBeInTheDocument();
  });

  it.each(["left", "right"] as const)("does not treat an explicitly run-only %s side with missing attempt information as legacy", async (side) => {
    const service = new CompareService();
    Reflect.deleteProperty(service.reviews.get("run-a")!, "review_attempts");
    const entry = side === "left"
      ? "/compare?case_id=case-main&left_run_id=run-a&left_run_only=1&right_run_id=run-y&right_attempt_id=attempt-y"
      : "/compare?case_id=case-main&left_run_id=run-z&left_attempt_id=attempt-z-2&right_run_id=run-a&right_run_only=1";
    open(service, entry);
    await screen.findByLabelText(`${side === "left" ? "历史版本" : "当前版本"}材料解析正文`);
    await waitFor(() => expect(service.requests.some((request) => request.includes("/reviews/run-a"))).toBe(true));
    expect(screen.queryByLabelText(`${side === "left" ? "当前版本" : "历史版本"}材料解析正文`)).not.toBeInTheDocument();
    expect(screen.getByText(/未报告执行身份/)).toBeVisible();
  });

  it("does not treat an attempt object with an empty selected attempt as explicit legacy null", async () => {
    const service = new CompareService();
    Object.assign(service.reviews.get("run-a")!, { review_attempts: { attempt: null, attempts: [attempt("a", 1, {run_id: "run-a"})] } });
    open(service, "/compare?case_id=case-main&left_run_id=run-a&left_run_only=1&right_run_id=run-y&right_attempt_id=attempt-y");
    await screen.findByText("历史合同质保36个月。");
    expect(screen.queryByLabelText("当前版本材料解析正文")).not.toBeInTheDocument();
    expect(screen.getByText(/未报告执行身份/)).toBeVisible();
  });

  it("resolves a missing summary attempt once from the authorized workspace and pins the returned identity", async () => {
    const service = new CompareService();
    service.data.runs.find((item) => item.id === "run-z")!.attempt_id = null;
    service.frozenAttempts.set("run-z:attempt-z-2", service.reviews.get("run-z")!);
    open(service, "/compare?case_id=case-main&left_run_id=run-z&right_run_id=run-y&right_attempt_id=attempt-y");
    await waitFor(() => expect(screen.getByLabelText("路由查询")).toHaveTextContent("left_attempt_id=attempt-z-2"));
    expect(await screen.findByText("质保期为24个月。")).toBeVisible();
    const requests = service.requests.filter((request) => request.includes("/reviews/run-z"));
    expect(requests).toHaveLength(2);
    expect(requests[0]).not.toContain("attempt_id");
    expect(requests[1]).toContain("attempt_id=attempt-z-2");
  });

  it("retains a true run-only legacy record and refuses to follow a later retry", async () => {
    const service = new CompareService();
    const initial = "/compare?case_id=case-main&left_run_id=run-a&right_run_id=run-y&right_attempt_id=attempt-y";
    const { user } = open(service, initial);
    const pane = (await screen.findByLabelText("当前版本材料解析正文")).closest("article")!;
    expect(within(pane).getByText(/未能确认本次执行的解析身份/)).toBeVisible();
    await user.click(within(pane).getByText(/版本与来源/));
    expect(within(pane).getByRole("link", { name: "读取原件" })).toHaveAttribute("href", "/api/cases/case-main/documents/draft-a/original");
    await waitFor(() => expect(screen.getByLabelText("路由查询")).toHaveTextContent("left_run_only=1"));
    expect(screen.queryByRole("link", { name: "打开审查与决定" })).not.toBeInTheDocument();
    const persisted = screen.getByLabelText("路由查询").textContent!;
    cleanup();
    service.data.runs.find((item) => item.id === "run-a")!.attempt_id = "attempt-a-1";
    const newAttempt = attempt("attempt-a-1", 1, { run_id: "run-a" });
    service.reviews.get("run-a")!.review_attempts = { attempt: newAttempt, attempts: [newAttempt] };
    open(service, `/compare${persisted}`);
    expect(await screen.findByText(/当前返回了不同执行记录/)).toBeVisible();
    expect(screen.queryByLabelText("当前版本材料解析正文")).not.toBeInTheDocument();
    expect(screen.getByLabelText("路由查询")).not.toHaveTextContent("left_attempt_id=attempt-a-1");
  });

  it("keeps a confirmed run-only historical side's original readable", async () => {
    const service = new CompareService();
    const { user } = open(service, "/compare?case_id=case-main&left_run_id=run-z&left_attempt_id=attempt-z-2&right_run_id=run-a&right_run_only=1");
    const pane = (await screen.findByLabelText("历史版本材料解析正文")).closest("article")!;
    expect(within(pane).getByText(/未能确认本次执行的解析身份/)).toBeVisible();
    await user.click(within(pane).getByText(/版本与来源/));
    expect(within(pane).getByRole("link", { name: "读取原件" })).toHaveAttribute("href", "/api/cases/case-main/documents/draft-a/original");
  });

  it("does not infer a run-only legacy identity when the attempt field is absent", async () => {
    const service = new CompareService();
    Reflect.deleteProperty(service.reviews.get("run-a")!, "review_attempts");
    open(service, "/compare?case_id=case-main&left_run_id=run-a&right_run_id=run-y&right_attempt_id=attempt-y");
    expect(await screen.findByText(/未报告执行身份/)).toBeVisible();
    expect(screen.queryByLabelText("当前版本材料解析正文")).not.toBeInTheDocument();
    expect(screen.getByLabelText("路由查询")).not.toHaveTextContent("left_run_only=1");
    expect(screen.getByText(/无法验证修订对照/)).toBeVisible();
  });

  it("rechecks a cached run-only record when the same case becomes visible again", async () => {
    const service = new CompareService();
    const originalCases = service.data.cases;
    const { client } = open(service, "/compare?case_id=case-main&left_run_id=run-a&right_run_id=run-y&right_attempt_id=attempt-y", 300_000);
    expect(await screen.findByLabelText("当前版本材料解析正文")).toHaveAttribute("data-document-version-id", "draft-a");
    await waitFor(() => expect(screen.getByLabelText("路由查询")).toHaveTextContent("left_run_only=1"));
    act(() => client.setQueryData(queryKeys.workbench("week"), { ...service.data, cases: originalCases.filter((item) => item.id !== "case-main") }));
    await waitFor(() => expect(screen.queryByLabelText("当前版本材料解析正文")).not.toBeInTheDocument());
    service.reviewFailures.set("run-a", 403);
    act(() => client.setQueryData(queryKeys.workbench("week"), { ...service.data, cases: originalCases }));
    await waitFor(() => expect(service.requests.filter((request) => request.includes("/reviews/run-a")).length).toBeGreaterThan(1));
    expect(screen.queryByLabelText("当前版本材料解析正文")).not.toBeInTheDocument();
    expect(await screen.findByText(/冻结工作区访问被拒绝/)).toBeVisible();
  });

  it("hides a cached exact attempt while rechecking access and after denial", async () => {
    const service = new CompareService();
    const originalCases = service.data.cases;
    const { client } = open(service, "/compare?case_id=case-main&left_run_id=run-z&left_attempt_id=attempt-z-2&right_run_id=run-y&right_attempt_id=attempt-y", 300_000);
    expect(await screen.findByText("质保期为24个月。")).toBeVisible();
    act(() => client.setQueryData(queryKeys.workbench("week"), { ...service.data, cases: originalCases.filter((item) => item.id !== "case-main") }));
    await waitFor(() => expect(screen.queryByText("质保期为24个月。")).not.toBeInTheDocument());
    const gate = Promise.withResolvers<void>();
    service.reviewGates.set("run-z", gate);
    service.reviewFailures.set("run-z", 403);
    act(() => client.setQueryData(queryKeys.workbench("week"), { ...service.data, cases: originalCases }));
    await waitFor(() => expect(service.requests.filter((request) => request.includes("/reviews/run-z")).length).toBeGreaterThan(1));
    expect(screen.queryByText("质保期为24个月。")).not.toBeInTheDocument();
    await act(async () => gate.resolve());
    expect(await screen.findByText(/冻结工作区访问被拒绝/)).toBeVisible();
    expect(screen.queryByText("质保期为24个月。")).not.toBeInTheDocument();
  });

  it("rejects a response whose attempt does not match the fixed identity", async () => {
    const service = new CompareService();
    const current = service.reviews.get("run-z")!;
    current.review_attempts!.attempt = attempt("other-attempt", 3);
    open(service, "/compare?case_id=case-main&left_run_id=run-z&left_attempt_id=attempt-z-2&right_run_id=run-y&right_attempt_id=attempt-y");
    expect(await screen.findByText(/返回的审查或执行身份与所选记录不一致/)).toBeVisible();
    expect(screen.queryByText("质保期为24个月。")).not.toBeInTheDocument();
    expect(screen.queryByText("质保修订继续存在")).not.toBeInTheDocument();
  });

  it("treats an explicitly empty attempt as invalid instead of requesting the latest", async () => {
    const service = new CompareService();
    open(service, "/compare?case_id=case-main&left_run_id=run-z&left_attempt_id=&right_run_id=run-y&right_attempt_id=attempt-y");
    expect(await screen.findByText(/所选执行身份为空或相互冲突/)).toBeVisible();
    expect(screen.queryByRole("link", { name: "打开审查与决定" })).not.toBeInTheDocument();
    await screen.findByText("历史合同质保36个月。");
    expect(service.requests.some((request) => request.includes("/reviews/run-z"))).toBe(false);
  });

  it("shows malformed compare timestamps with the exact copyable value", async () => {
    const service = new CompareService();
    service.reviews.get("run-z")!.review_attempts!.attempts[1]!.finished_at = "bad-compare-time";
    const { user } = open(service);
    await screen.findByText("质保修订继续存在");
    await user.click(screen.getByRole("tab", { name: "执行记录" }));
    expect(screen.getByText(/时间格式异常/)).toBeVisible();
    await user.click(screen.getByText("执行结束时间原始值"));
    expect(screen.getByText("bad-compare-time")).toBeVisible();
  });

  it("keeps an empty snapshot navigable without requesting nonexistent runs", async () => {
    const service = new CompareService();
    service.data.cases = [];
    service.data.runs = [];
    open(service);
    expect(await screen.findByRole("link", { name: "全部合同" })).toHaveAttribute(
      "href",
      "/compare",
    );
    expect(screen.getByRole("link", { name: "案件详情" })).toHaveAttribute(
      "href",
      "/cases",
    );
    expect(
      screen.getByRole("link", { name: "打开审查与决定" }),
    ).toHaveAttribute("href", "/review");
    expect(screen.getAllByText("选择审查记录后，查看当时使用的材料。")).toHaveLength(2);
    expect(
      screen.getByText("审查身份未确认，无法读取发现。"),
    ).toBeInTheDocument();
    expect(service.requests).toEqual(["/api/workbench?period=week&task_capabilities=1"]);
  });

  it("shows initial loading, retries snapshot errors and then renders the comparison", async () => {
    const service = new CompareService();
    service.snapshotGate = Promise.withResolvers<void>();
    service.snapshotFailures.push(503);
    const { user } = open(service);
    expect(screen.getByRole("status")).toHaveTextContent(
      "正在读取可比较的真实审查记录",
    );
    service.snapshotGate.resolve();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "对照快照暂不可用",
    );
    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(
      await screen.findByLabelText("当前版本材料解析正文"),
    ).toHaveTextContent("质保期为24个月。");
    expect(
      service.requests.filter((path) => path === "/api/workbench?period=week&task_capabilities=1"),
    ).toHaveLength(2);
  });

  it("allows the historical side to load while the current frozen workspace is still pending", async () => {
    const service = new CompareService();
    const gate = Promise.withResolvers<void>();
    service.reviewGates.set("run-z", gate);
    open(service);
    await screen.findByLabelText("历史版本材料解析正文");
    expect(screen.getByText("正在读取所选审查的记录…")).toHaveAttribute(
      "role",
      "status",
    );
    expect(
      screen.queryByLabelText("当前版本材料解析正文"),
    ).not.toBeInTheDocument();
    gate.resolve();
    expect(
      await screen.findByLabelText("当前版本材料解析正文"),
    ).toHaveAttribute("data-document-version-id", "draft-z");
    await waitFor(() =>
      expect(screen.queryByText("正在读取所选审查的记录…")).not.toBeInTheDocument(),
    );
  });

  it("reports a failed run independently and recovers when the reader selects an accessible run", async () => {
    const service = new CompareService();
    service.reviewFailures.set("run-z", 403);
    const { user } = open(service);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "无法读取此运行：冻结工作区访问被拒绝",
    );
    expect(
      await screen.findByLabelText("历史版本材料解析正文"),
    ).toHaveTextContent("历史合同质保36个月。");
    expect(
      screen.queryByLabelText("当前版本材料解析正文"),
    ).not.toBeInTheDocument();
    await chooseOption(
      user,
      screen.getByRole("combobox", { name: "当前版本" }),
      /审查标识 run-y/,
    );
    expect(
      await screen.findByLabelText("当前版本材料解析正文"),
    ).toHaveTextContent("历史合同质保36个月。");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(
      screen.getByText("此运行没有返回发现；不代表合同已批准。"),
    ).toBeInTheDocument();
  });
});
