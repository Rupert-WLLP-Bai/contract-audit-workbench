import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setSessionIdentity } from "../api/http";
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
    parsed_document_ids: ["parse-source-z", "parse-draft-z"],
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
    execution: null,
    snapshot: null,
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
    this.reviews.set(
      "run-a",
      review("run-a", [
        document("draft-a", "draft_contract", "最初合同质保12个月。", {
          superseded: true,
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
      const workspace = this.reviews.get(id);
      const summary = this.data.runs.find((item) => item.id === id);
      if (
        !workspace ||
        workspace.run.case_id !== decodeURIComponent(parts[3]!) ||
        (summary?.attempt_id ?? null) !== url.searchParams.get("attempt_id")
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
  return <div aria-label="路由查询">{location.search}</div>;
}

function open(service = new CompareService(), entry = "/compare") {
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
      <MemoryRouter initialEntries={[entry]}>
        <ComparePage />
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
    ).toHaveAttribute("href", "/cases/case-main/reviews/run-z");
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
    expect(
      screen.getByText(
        "旧发现 prior-finding · 新发现 finding-rule · 旧决定 confirm_risk",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText("旧发现 未报告 · 新发现 未报告 · 旧决定 未报告"),
    ).toBeInTheDocument();
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
      screen.getByText(/当前运行未将所选历史运行声明为前序/),
    ).toBeInTheDocument();
    await chooseRun(user, "当前版本", /run-z/);
    await user.click(
      await screen.findByRole("button", { name: /质保期限低于采购要求/ }),
    );
    expect(material("当前版本")).toHaveAttribute(
      "data-document-version-id",
      "source-z",
    );
    await chooseRun(user, "当前版本", /run-y/);
    await waitFor(() =>
      expect(material("当前版本")).toHaveAttribute(
        "data-document-version-id",
        "draft-y",
      ),
    );
    expect(
      screen.queryByRole("link", { name: "打开原文与人工决定" }),
    ).not.toBeInTheDocument();
    await chooseRun(user, "当前版本", /run-z/);
    await waitFor(() =>
      expect(material("当前版本")).toHaveAttribute(
        "data-document-version-id",
        "draft-z",
      ),
    );
    expect(
      screen.getByRole("button", { name: /质保期限低于采购要求/ }),
    ).toHaveAttribute("aria-pressed", "false");
    await chooseRun(user, "历史版本", /run-z/);
    await waitFor(() =>
      expect(material("历史版本")).toHaveAttribute(
        "data-document-version-id",
        "draft-z",
      ),
    );
    expect(
      screen.getByRole("link", { name: "打开审查与决定" }),
    ).toHaveAttribute("href", "/cases/case-main/reviews/run-z");
  });

  it("searches case numbers and resets run and material choices when another case is selected", async () => {
    const { user, service } = open(
      new CompareService(),
      "/compare?case_id=case-main&left_run_id=run-y&right_run_id=run-a",
    );
    await screen.findByLabelText("当前版本材料解析正文");
    const cases = within(
      screen.getByRole("complementary", { name: "对照案件列表" }),
    );
    await user.type(
      cases.getByRole("searchbox", { name: "搜索案件" }),
      " N-002 ",
    );
    expect(cases.getAllByRole("button")).toHaveLength(1);
    expect(cases.getByRole("button", { name: /专线合同修订/ })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await user.clear(cases.getByRole("searchbox", { name: "搜索案件" }));
    await user.type(
      cases.getByRole("searchbox", { name: "搜索案件" }),
      "没有匹配的编号",
    );
    expect(cases.queryAllByRole("button")).toHaveLength(0);
    await user.clear(cases.getByRole("searchbox", { name: "搜索案件" }));
    await user.click(cases.getByRole("button", { name: /暂无审查案件/ }));
    expect(screen.getByText("此案件尚无持久审查运行。")).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "打开案件并发起审查" }),
    ).toHaveAttribute("href", "/cases/case-empty");
    expect(screen.getByText("此案件尚无持久审查运行。")).toBeInTheDocument();
    expect(screen.getByLabelText("路由查询")).toHaveTextContent(
      "?case_id=case-empty",
    );
    expect(
      screen
        .getAllByRole("combobox", { name: "材料" })
        .every((select) => (select as HTMLSelectElement).disabled),
    ).toBe(true);
    expect(
      service.requests.some((path) => path.includes("case-empty/reviews")),
    ).toBe(false);
    await user.click(cases.getByRole("button", { name: /单次审查案件/ }));
    await screen.findByLabelText("当前版本材料解析正文");
    await screen.findByLabelText("历史版本材料解析正文");
    expect(material("当前版本")).toHaveTextContent("单次审查冻结文本。");
    expect(material("历史版本")).toHaveTextContent("单次审查冻结文本。");
    expect(material("当前版本")).toHaveTextContent("单次审查冻结文本。");
    expect(material("历史版本")).toHaveTextContent("单次审查冻结文本。");
    await user.click(cases.getByRole("button", { name: /专线合同修订/ }));
    await waitFor(() =>
      expect(material("当前版本")).toHaveAttribute(
        "data-document-version-id",
        "draft-z",
      ),
    );
    expect(material("历史版本")).toHaveAttribute(
      "data-document-version-id",
      "draft-y",
    );
  });

  it("falls back from absent URL case and run identifiers to available frozen records", async () => {
    open(
      new CompareService(),
      "/compare?case_id=removed-case&left_run_id=removed-run&right_run_id=removed-prior",
    );
    await screen.findByLabelText("当前版本材料解析正文");
    expect(material("当前版本")).toHaveAttribute(
      "data-document-version-id",
      "draft-z",
    );
    expect(material("历史版本")).toHaveAttribute(
      "data-document-version-id",
      "draft-y",
    );
    expect(screen.getByRole("link", { name: "案件详情" })).toHaveAttribute(
      "href",
      "/cases/case-main",
    );
  });
});

describe("ComparePage evidence and actual results", () => {
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
    expect(screen.getByText("warranty-policy v2 · 第4条")).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "打开原文与人工决定" }),
    ).toHaveAttribute(
      "href",
      "/cases/case-main/reviews/run-z?finding=finding-rule",
    );
    await user.click(within(pane).getByText("版本与来源 · ready"));
    expect(within(pane).getByText("parse-source-z")).toBeVisible();
    expect(within(pane).getByText("digest-source-z")).toBeVisible();
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
        screen.getByRole("link", { name: "打开原文与人工决定" }),
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

  it("shows rule, execution and usage facts for each selected side without inventing missing values", async () => {
    const { user } = open();
    await screen.findByText("质保修订继续存在");
    await user.click(screen.getByRole("tab", { name: "规则依据" }));
    expect(screen.getByRole("tab", { name: "规则依据" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByText("规则 WARRANTY-001 · v2.1")).toBeInTheDocument();
    expect(screen.getByText("合同质保低于采购要求")).toBeInTheDocument();
    expect(screen.getByText("此运行未返回规则评估记录。")).toBeInTheDocument();
    expect(screen.queryByText("质保修订继续存在")).not.toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "执行记录" }));
    expect(
      screen.getByText("运行 run-z · 当前尝试 attempt-z-2 · completed"),
    ).toBeInTheDocument();
    expect(screen.getByText("尝试数 2 · 事件数 1")).toBeInTheDocument();
    expect(
      screen.getByText("attempt-z-1 · 第 1 次 · failed · 结束时间未报告"),
    ).toBeInTheDocument();
    expect(screen.getByText("此运行没有返回执行记录。")).toBeInTheDocument();
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
    ).toEqual(["2", "120", "30", "150", "未知"]);
    expect(
      within(right)
        .getAllByRole("definition")
        .map((item) => item.textContent),
    ).toEqual(["未知", "未知", "未知", "未知", "未知"]);
    await user.click(screen.getByRole("tab", { name: "修订证据" }));
    expect(screen.getByText("质保修订继续存在")).toBeInTheDocument();
  });

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
});

describe("ComparePage unavailable data", () => {
  it("keeps an empty snapshot navigable without requesting nonexistent runs", async () => {
    const service = new CompareService();
    service.data.cases = [];
    service.data.runs = [];
    open(service);
    await screen.findByRole("heading", { name: /合同案件 0/ });
    expect(screen.getByRole("link", { name: "案件详情" })).toHaveAttribute(
      "href",
      "/cases",
    );
    expect(
      screen.getByRole("link", { name: "打开审查与决定" }),
    ).toHaveAttribute("href", "/review");
    expect(screen.getAllByText("选择实际运行后查看冻结材料。")).toHaveLength(2);
    expect(
      screen.getByText("此运行没有返回发现；不代表合同已批准。"),
    ).toBeInTheDocument();
    expect(service.requests).toEqual(["/api/workbench?period=week"]);
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
      service.requests.filter((path) => path === "/api/workbench?period=week"),
    ).toHaveLength(2);
  });

  it("allows the historical side to load while the current frozen workspace is still pending", async () => {
    const service = new CompareService();
    const gate = Promise.withResolvers<void>();
    service.reviewGates.set("run-z", gate);
    open(service);
    await screen.findByLabelText("历史版本材料解析正文");
    expect(screen.getByText("正在读取冻结工作区…")).toHaveAttribute(
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
      expect(screen.queryByText("正在读取冻结工作区…")).not.toBeInTheDocument(),
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
      /run-y/,
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
