import { useState, type ComponentProps } from "react";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { MemoryRouter } from "react-router";
import type { ReviewDocument, ReviewWorkspace } from "../api/types";
import { ReviewWorkspaceView } from "./review-workspace";
import { DocumentStage } from "./document-stage";

const procurementText = "质保期为三十六个月，自验收合格之日起算。";
const contractText = "质保期为二十四个月。";

function documentOf(
  id: string,
  role: string,
  filename: string,
  text: string,
): ReviewDocument {
  return {
    id,
    role,
    filename,
    media_type: "text/plain",
    digest: "abc12345",
    size: text.length,
    parse_status: "ready",
    parse_error: null,
    created_at: "2026-10-21T01:00:00Z",
    parsed_document: null,
    parsed_document_id: null,
    text,
    clauses: [
      {
        id: `${id}-clause`,
        heading: "质保",
        text_offset: { start: 0, end: 4 },
        page: null,
      },
    ],
  };
}

function workspace(): ReviewWorkspace {
  return {
    run: {
      id: "r1",
      case_id: "c1",
      state: "awaiting_human",
      updated_at: "2026-10-21T01:00:00Z",
      started_at: "2026-10-21T00:50:00Z",
      model_ref: "fake",
      partial_reason: null,
      failure_message: null,
    },
    documents: [
      documentOf("d1", "procurement", "采购文件.txt", procurementText),
      documentOf("d2", "draft_contract", "拟签合同.txt", contractText),
    ],
    findings: [
      {
        id: "f1",
        origin: "ai",
        domain: "质保",
        claim: "采购文件与拟签合同的质保期不一致",
        relation: "conflict",
        confidence: "medium",
        severity: null,
        severity_source: null,
        verification_status: "unverified",
        unverified_reason: "引用尚未复核",
        version: 3,
        decisions: [],
        evidence: [
          {
            id: "e1",
            document_version_id: "d1",
            parsed_document_id: null,
            normalized_digest: null,
            quote: "三十六个月",
            status: "located",
            page: 2,
            text_offset: { start: 4, end: 9 },
            bbox: null,
            mismatch_reason: null,
          },
          {
            id: "e2",
            document_version_id: "d2",
            parsed_document_id: null,
            normalized_digest: null,
            quote: "二十四个月",
            status: "located",
            page: 5,
            text_offset: { start: 4, end: 9 },
            bbox: null,
            mismatch_reason: null,
          },
        ],
      },
      {
        id: "f2",
        origin: "rule",
        domain: "付款",
        claim: "付款节点缺少可定位原文",
        relation: "insufficient",
        confidence: null,
        severity: null,
        severity_source: null,
        unverified_reason: null,
        verification_status: "unknown",
        version: null,
        decisions: [
          {
            id: "dec-1",
            action: "insufficient_evidence",
            reason: "页码对不上",
            actor: "周审查",
            decided_at: "2026-10-21T02:00:00Z",
          },
        ],
        evidence: [
          {
            id: "e3",
            document_version_id: "d1",
            parsed_document_id: null,
            normalized_digest: null,
            quote: "付款按进度",
            status: "anchor_mismatch",
            page: 8,
            text_offset: { start: 0, end: 4 },
            bbox: null,
            mismatch_reason: "定位坐标与当前版本不匹配",
          },
        ],
      },
    ],
    review_attempts: null,
    execution: null,
    audit_events: [],
  };
}

type Decide = ComponentProps<typeof ReviewWorkspaceView>["onDecide"];

function WorkspaceHarness({
  layout,
  onDecide,
  workspaceValue,
  showDiagnostics = false,
}: {
  layout: "split" | "switch";
  onDecide: Decide;
  workspaceValue: ReviewWorkspace;
  showDiagnostics?: boolean;
}) {
  const [selectedId, setSelectedId] = useState("f1");
  return (
    <ReviewWorkspaceView
      workspace={workspaceValue}
      layout={layout}
      selectedId={selectedId}
      streamStatus="live"
      showDiagnostics={showDiagnostics}
      pending={false}
      retrying={false}
      conflictMessage={
        selectedId === "f1"
          ? "该事项已被他人更新，已载入最新内容，请重新确认。"
          : null
      }
      saveError={null}
      onSelect={setSelectedId}
      onDecide={onDecide}
      onRetry={vi.fn()}
      onSelectAttempt={vi.fn()}
      onCancel={vi.fn()}
    />
  );
}

function renderWorkspace(
  layout: "split" | "switch",
  workspaceValue = workspace(),
  showDiagnostics = false,
) {
  const onDecide = vi.fn().mockResolvedValue(true);
  const view = render(
    <WorkspaceHarness
      layout={layout}
      onDecide={onDecide}
      workspaceValue={workspaceValue}
      showDiagnostics={showDiagnostics}
    />,
  );
  return { ...view, onDecide };
}

describe("review workspace", () => {
  it.each([
    {
      scenario: "no citations",
      located: 0,
      total: 0,
      label: "未提供引用",
      state: "open",
    },
    {
      scenario: "none located",
      located: 0,
      total: 3,
      label: "均未定位",
      state: "open",
    },
    {
      scenario: "some located",
      located: 2,
      total: 3,
      label: "部分定位",
      state: "open",
    },
    {
      scenario: "all located",
      located: 3,
      total: 3,
      label: "全部定位",
      state: "filled",
    },
  ])(
    "reports $scenario without implying verification",
    ({ located, total, label, state }) => {
      const result = workspace();
      let index = 0;
      for (const finding of result.findings) {
        finding.evidence = finding.evidence.filter(() => index++ < total);
      }
      index = 0;
      for (const finding of result.findings) {
        for (const anchor of finding.evidence) {
          anchor.status = index++ < located ? "located" : "anchor_mismatch";
        }
      }
      renderWorkspace("split", result);
      const circuit = screen.getByRole("list", { name: "审查链路" });
      const citation = within(circuit).getByText(/可在原文中找到的引用/).closest("li");
      expect(citation).toHaveAttribute("data-state", state);
      expect(citation).toHaveTextContent(`${located}/${total}`);
      expect(citation).toHaveTextContent(label);
      expect(screen.getByText("尚未核验：引用尚未复核")).toBeInTheDocument();
    },
  );

  it("reports absent execution without inventing stages or proof", () => {
    const result = workspace();
    result.findings.forEach((finding) => {
      finding.evidence = [];
    });
    result.execution = null;
    renderWorkspace("split", result);
    const circuit = screen.getByRole("list", { name: "审查链路" });
    expect(within(circuit).getByText(/可在原文中找到的引用/).closest("li")).toHaveAttribute(
      "data-state",
      "open",
    );
    expect(screen.getByText(/没有获取到本次审查的执行记录/)).toBeInTheDocument();
    expect(
      screen.queryByText(/legacy_unfrozen|历史记录未冻结/),
    ).not.toBeInTheDocument();
  });

  it("shows both sources on request and does not treat the model claim as verified", async () => {
    const user = userEvent.setup();
    const { container } = renderWorkspace("split", workspace(), true);
    await user.click(screen.getByRole("button", { name: "双来源对照" }));
    expect(screen.getByRole("button", { name: "双来源对照" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    const marks = container.querySelectorAll("mark.hit-evidence");
    expect([...marks].map((mark) => mark.textContent)).toEqual([
      "三十六个月",
      "二十四个月",
    ]);
    expect(screen.getByText("尚未核验：引用尚未复核")).toBeInTheDocument();
    expect(
      screen.getByText("该事项已被他人更新，已载入最新内容，请重新确认。"),
    ).toBeInTheDocument();
    for (const disclosure of screen.getAllByRole("button", {
      name: "解析来源与摘要",
    })) {
      await user.click(disclosure);
    }
    const provenance = screen.getAllByLabelText("解析来源与摘要");
    expect(provenance).toHaveLength(2);
    expect(
      provenance.every((item) => {
        const text = item.textContent ?? "";
        return (
          text.includes("解析来源 未记录（历史记录）") &&
          text.includes("正文摘要 未记录")
        );
      }),
    ).toBe(true);
    expect(
      screen.getAllByText("历史来源未记录解析身份或正文摘要"),
    ).toHaveLength(2);
  });

  it("keeps citation identities hidden and does not claim the finding was verified", () => {
    const result = workspace();
    result.documents[0]!.parsed_document_id = "parsed_private_source";
    result.findings[0]!.evidence[0]!.parsed_document_id = "parsed_private_source";
    result.findings[0]!.verification_status = "verified";
    result.findings[0]!.unverified_reason = null;
    const { container } = renderWorkspace("split", result);
    expect(container.querySelector("mark.hit-evidence")).not.toBeNull();
    expect(screen.getByText("内容含义待核实")).toBeInTheDocument();
    expect(container).not.toHaveTextContent(/parsed_private_source|SHA-256|abc12345/);
    expect(screen.queryByText("已核验")).not.toBeInTheDocument();
  });

  it("keeps a same-document evidence quote unhighlighted for a different parse", () => {
    const finding = workspace().findings[0];
    if (!finding) throw new Error("test finding is missing");
    const anchor = finding.evidence[0];
    if (!anchor) throw new Error("test evidence is missing");
    anchor.parsed_document_id = "pd-old";
    anchor.normalized_digest = "text-old";
    const source = documentOf(
      "d1",
      "procurement",
      "采购文件.txt",
      procurementText,
    );
    source.parsed_document = {
      id: "pd-current",
      document_id: "d1",
      original_digest: "abc12345",
      parser_id: "text",
      parser_version: "1",
      config_hash: "config-current",
      normalization_version: "unicode-v1",
      normalized_digest: "text-current",
      ir_digest: "ir-current",
      artifact_digest: "artifact-current",
      created_at: "2026-10-21T03:00:00Z",
      status: "ready",
    };
    const { container } = render(
      <DocumentStage
        documents={[source]}
        finding={finding}
        findings={[finding]}
        selectedFindingId={finding.id}
        layout="switch"
        onSelectFinding={vi.fn()}
      />,
    );

    expect(container.querySelector("mark.hit-evidence")).toBeNull();
    expect(screen.getByText(/解析产物与当前正文不一致/)).toBeInTheDocument();
  });

  it("keeps the attempt input primary when an older parse finishes later", () => {
    const selected = documentOf(
      "d1",
      "procurement",
      "采购文件.txt",
      "当前审查采用的原文",
    );
    selected.parsed_document = {
      id: "pd-selected",
      document_id: "d1",
      original_digest: selected.digest,
      parser_id: "text",
      parser_version: "1",
      config_hash: "config-1",
      normalization_version: "unicode-v1",
      normalized_digest: "selected-text",
      ir_digest: "selected-ir",
      artifact_digest: "selected-artifact",
      created_at: "2026-10-21T03:00:00Z",
      status: "ready",
    };
    const historical = documentOf(
      "d1",
      "procurement",
      "采购文件.txt",
      "晚完成的旧任务原文",
    );
    historical.parsed_document = {
      ...selected.parsed_document,
      id: "pd-historical",
      normalized_digest: "historical-text",
      created_at: "2026-10-21T04:00:00Z",
    };
    const { container } = render(
      <DocumentStage
        documents={[selected, historical]}
        finding={null}
        findings={[]}
        selectedFindingId={null}
        layout="switch"
        onSelectFinding={vi.fn()}
      />,
    );

    expect(
      container
        .querySelector(".doc-pane")
        ?.getAttribute("data-parsed-document-id"),
    ).toBe("pd-selected");
    expect(screen.getByText("当前审查采用的原文")).toBeInTheDocument();
    expect(screen.queryByText("晚完成的旧任务原文")).not.toBeInTheDocument();
  });

  it("navigates from a material reference back to the linked finding", async () => {
    const user = userEvent.setup();
    const { container } = renderWorkspace("switch");
    await user.click(
      screen.getByRole("button", { name: "查看线索：付款节点缺少可定位原文" }),
    );

    expect(
      screen.getByRole("heading", { name: "付款节点缺少可定位原文" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "查看线索：付款节点缺少可定位原文" }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(
      container
        .querySelector(".doc-pane")
        ?.getAttribute("data-document-version-id"),
    ).toBe("d1");
  });

  it("switches evidence sources without dropping the finding, and refuses a bad anchor highlight", async () => {
    const user = userEvent.setup();
    const { container } = renderWorkspace("switch");
    expect(container.querySelector("mark.hit-evidence")?.textContent).toBe(
      "三十六个月",
    );
    await user.click(screen.getByRole("button", { name: "拟签合同" }));
    expect(container.querySelector("mark.hit-evidence")?.textContent).toBe(
      "二十四个月",
    );

    await user.click(
      screen.getByRole("button", { name: "选择线索：付款节点缺少可定位原文" }),
    );
    expect(container.querySelector("mark.hit-evidence")).toBeNull();
    expect(
      screen.getAllByText("定位坐标与当前版本不匹配").length,
    ).toBeGreaterThan(0);
    expect(screen.getAllByText("付款按进度").length).toBeGreaterThan(0);
  });

  it("saves a reasoned decision and blocks a finding that has no version", async () => {
    const user = userEvent.setup();
    const { onDecide } = renderWorkspace("switch");
    const save = screen.getByRole("button", { name: "保存决定" });
    expect(save).toBeDisabled();
    await user.click(screen.getByRole("radio", { name: "误报" }));
    await user.type(
      screen.getByRole("textbox", { name: "理由" }),
      "两处期限实际指向同一附件",
    );
    expect(save).toBeEnabled();
    await user.click(save);
    expect(onDecide).toHaveBeenCalledWith({
      findingId: "f1",
      action: "false_positive",
      reason: "两处期限实际指向同一附件",
      expectedVersion: 3,
    });

    await user.click(
      screen.getByRole("button", { name: "选择线索：付款节点缺少可定位原文" }),
    );
    expect(
      screen.getByText(
        "服务没有返回这条线索的版本号，暂不能保存，以免覆盖其他人的决定。",
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "保存决定" })).toBeDisabled();
    expect(screen.getByText(/周审查/)).toBeInTheDocument();
  });
  it("creates or removes saved case relations", async () => {
    const user = userEvent.setup();
    const onCreateRelation = vi.fn().mockResolvedValue(undefined);
    const onDeleteRelation = vi.fn();
    const caseContext = {
      caseId: "c1",
      title: "云服务协议",
      number: "CN-1",
      customer: "甲方",
      supplier: "乙方",
      contractType: "采购合同",
      businessLine: "ENT",
      scenario: "cloud_services",
      status: "审查中",
      ownerId: "m1",
      ownerName: "林晓",
      dueAt: null,
      collaborators: [
        {
          id: "m1",
          name: "林晓",
          role: "reviewer",
          responsibilities: ["案件负责人", "任务责任人：核对SLA（open）"],
        },
      ],
      relatedCases: [
        {
          relationId: "relation-3",
          id: "c3",
          title: "云平台服务协议",
          reason: "同供应商年度续约",
          createdAt: "2026-10-21T01:00:00Z",
          runId: "r3",
        },
      ],
      availableCases: [{ id: "c2", title: "云托管服务补充协议" }],
    };
    render(
      <MemoryRouter>
        <DocumentStage
          documents={[]}
          finding={null}
          findings={[]}
          selectedFindingId={null}
          layout="switch"
          onSelectFinding={vi.fn()}
          caseContext={caseContext}
          canEditRelations
          onCreateRelation={onCreateRelation}
          onDeleteRelation={onDeleteRelation}
        />
      </MemoryRouter>,
    );

    await user.click(screen.getByRole("tab", { name: /相关案件/ }));
    expect(
      screen.getByRole("link", { name: "云平台服务协议" }),
    ).toHaveAttribute("href", "/cases/c3/reviews/r3");
    await user.click(screen.getByRole("button", { name: "解除关联" }));
    expect(onDeleteRelation).toHaveBeenCalledWith("relation-3");
    await chooseOption(
      user,
      screen.getByRole("combobox", { name: "关联可见案件" }),
      "云托管服务补充协议",
    );
    await user.type(screen.getByLabelText("关联原因"), "同一云资源范围");
    await user.click(screen.getByRole("button", { name: "建立关联" }));
    expect(onCreateRelation).toHaveBeenCalledWith({
      related_case_id: "c2",
      reason: "同一云资源范围",
    });
  });
});

function renderView(
  overrides: Partial<ComponentProps<typeof ReviewWorkspaceView>> = {},
) {
  const onRetry = vi.fn();
  const onCancel = vi.fn();
  const onSelectAttempt = vi.fn();
  const props: ComponentProps<typeof ReviewWorkspaceView> = {
    workspace: workspace(),
    layout: "switch",
    selectedId: "f1",
    streamStatus: "live",
    pending: false,
    retrying: false,
    conflictMessage: null,
    saveError: null,
    onSelect: vi.fn(),
    onSelectAttempt,
    onDecide: vi.fn().mockResolvedValue(true),
    onRetry,
    onCancel,
    ...overrides,
  };
  const view = render(<MemoryRouter><ReviewWorkspaceView {...props} /></MemoryRouter>);
  return { ...view, onRetry, onCancel, onSelectAttempt };
}

const attempt = (id: string, ordinal: number, state: string) => ({
  id,
  run_id: "r1",
  ordinal,
  retry_of_attempt_id: null,
  state,
  accepted_at: "2026-10-21T00:00:00Z",
  started_at: "2026-10-21T00:00:00Z",
  finished_at: null,
  parsed_document_ids: [],
  history_kind: "frozen",
  input_hash: null,
  output_hash: null,
  error_code: null,
});

describe("review workspace lifecycle", () => {
  it.each([
    ["connecting", "正在连接运行状态"],
    ["live", "运行状态已连接"],
    ["reconnecting", "连接中断，正在按游标重连"],
    ["closed", "运行状态流已关闭，不会自动重连；请刷新页面或重新登录后再试。"],
    ["inactive", "本次执行没有活动的事件流"],
  ] as const)("reports the %s stream status", (streamStatus, label) => {
    renderView({ streamStatus });
    expect(screen.getByText(label)).toBeInTheDocument();
  });

  it("shows the configured model only when the run reports one", () => {
    const result = workspace();
    result.run.model_ref = "model-v1";
    const view = renderView({ workspace: result });
    expect(screen.getByText("模型 model-v1")).toBeInTheDocument();
    view.unmount();
    const without = workspace();
    without.run.model_ref = null;
    renderView({ workspace: without });
    expect(screen.queryByText(/^模型 /)).not.toBeInTheDocument();
  });

  it.each(["failed", "partial", "cancelled", "interrupted"])(
    "offers a retry for a %s run",
    async (state) => {
      const user = userEvent.setup();
      const result = workspace();
      result.run.state = state;
      const { onRetry } = renderView({ workspace: result });
      expect(
        screen.getByRole("button", { name: "重新执行未完成的审查" }),
      ).toBeEnabled();
      await user.click(
        screen.getByRole("button", { name: "重新执行未完成的审查" }),
      );
      expect(onRetry).toHaveBeenCalledTimes(1);
    },
  );

  it.each(["completed", "awaiting_human", "running"])(
    "does not offer a retry for a %s run",
    (state) => {
      const result = workspace();
      result.run.state = state;
      renderView({ workspace: result });
      expect(
        screen.queryByRole("button", { name: "重新执行未完成的审查" }),
      ).not.toBeInTheDocument();
    },
  );

  it("marks a retry as pending and respects the retry permission", () => {
    const result = workspace();
    result.run.state = "failed";
    const view = renderView({ workspace: result, retrying: true });
    expect(
      screen.getByRole("button", { name: "正在受理重试" }),
    ).toBeDisabled();
    view.unmount();
    renderView({ workspace: result, canRetry: false });
    expect(
      screen.getByRole("button", { name: "重新执行未完成的审查" }),
    ).toBeDisabled();
  });

  it("explains partial, failed and cancelled runs", () => {
    const partial = workspace();
    partial.run.state = "partial";
    partial.run.partial_reason = "模型输出超时";
    const view = renderView({ workspace: partial });
    expect(
      screen.getByText(/模型输出超时.*模型步骤未完成/),
    ).toBeInTheDocument();
    view.unmount();
    const failed = workspace();
    failed.run.state = "failed";
    failed.run.failure_message = "网关错误";
    const failedView = renderView({ workspace: failed });
    expect(screen.getByText("网关错误")).toBeInTheDocument();
    failedView.unmount();
    const cancelled = workspace();
    cancelled.run.state = "cancelled";
    renderView({ workspace: cancelled });
    expect(
      screen.getByText("审查已取消。取消前保存的规则发现和实际调用用量仍会保留。"),
    ).toBeInTheDocument();
  });

  it("falls back to neutral partial and failure wording", () => {
    const partial = workspace();
    partial.run.state = "partial";
    const view = renderView({ workspace: partial });
    expect(screen.getByText(/部分结果已保留。.*模型步骤未完成/)).toBeInTheDocument();
    view.unmount();
    const failed = workspace();
    failed.run.state = "failed";
    renderView({ workspace: failed });
    expect(
      screen.getByText("本次审查执行失败，已保留失败前的记录。"),
    ).toBeInTheDocument();
  });

  it("selects a saved attempt when no detailed execution is available", async () => {
    const user = userEvent.setup();
    const result = workspace();
    result.review_attempts = {
      attempt: attempt("attempt-2", 2, "completed"),
      attempts: [attempt("attempt-1", 1, "failed"), attempt("attempt-2", 2, "completed")],
    };
    const { onSelectAttempt } = renderView({ workspace: result });
    expect(
      screen.getByText("没有提供详细执行诊断；以下执行状态来自业务工作区。"),
    ).toBeInTheDocument();
    const select = screen.getByRole("combobox", { name: "查看执行记录" });
    await user.click(select);
    const options = await screen.findAllByRole("option");
    expect(options.map((option) => option.textContent)).toEqual([
      "第 2 次，执行完成，最新",
      "第 1 次，失败",
    ]);
    await user.click(options[1]!);
    expect(onSelectAttempt.mock.calls[0]?.[0]).toBe("attempt-1");
  });

  it("prepends an attempt missing from the saved attempt list", async () => {
    const result = workspace();
    result.review_attempts = {
      attempt: attempt("attempt-9", 9, "running"),
      attempts: [attempt("attempt-1", 1, "failed")],
    };
    renderView({ workspace: result });
    await userEvent.setup().click(
      screen.getByRole("combobox", { name: "查看执行记录" }),
    );
    const options = await screen.findAllByRole("option");
    expect(options.map((option) => option.textContent)).toEqual([
      "第 9 次，审查中，最新",
      "第 1 次，失败",
    ]);
  });

  it("warns about a legacy attempt and offers cancellation with its error", async () => {
    const user = userEvent.setup();
    const result = workspace();
    result.review_attempts = {
      attempt: { ...attempt("attempt-1", 1, "running"), history_kind: "legacy_unfrozen" },
      attempts: [],
    };
    const { onCancel } = renderView({
      workspace: result,
      canCancel: true,
      cancelError: "取消失败",
    });
    expect(
      screen.getByText("较早的审查没有保存当时使用的材料版本记录，无法补回当时的执行过程。"),
    ).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("取消失败");
    await user.click(screen.getByRole("button", { name: "取消本次执行" }));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("shows a pending cancellation label without allowing duplicates", () => {
    const result = workspace();
    result.review_attempts = {
      attempt: attempt("attempt-1", 1, "running"),
      attempts: [],
    };
    renderView({ workspace: result, canCancel: true, cancelling: true });
    expect(screen.getByRole("button", { name: "正在取消" })).toBeDisabled();
  });

  it("fills the circuit for materials, findings and decisions", () => {
    const result = workspace();
    result.findings[0]!.decisions = [
      {
        id: "dec-2",
        action: "confirm_risk",
        reason: "已确认",
        actor: "审查员",
        decided_at: "2026-10-21T02:00:00Z",
      },
    ];
    renderView({ workspace: result });
    const circuit = screen.getByRole("list", { name: "审查链路" });
    const items = within(circuit).getAllByRole("listitem");
    expect(items[0]).toHaveAttribute("data-state", "filled");
    expect(items[2]).toHaveAttribute("data-state", "filled");
    expect(items[3]).toHaveAttribute("data-state", "filled");
    expect(items[3]).toHaveTextContent("2/2");
  });

  it("marks an empty run as open and uses the layout class", () => {
    const result = workspace();
    result.documents = [];
    result.findings = [];
    const { container } = renderView({ workspace: result, layout: "split" });
    const circuit = screen.getByRole("list", { name: "审查链路" });
    for (const item of within(circuit).getAllByRole("listitem"))
      expect(item).toHaveAttribute("data-state", "open");
    expect(container.querySelector(".workspace-grid")).toHaveClass("layout-split");
  });
});
