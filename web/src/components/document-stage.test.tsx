import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import type { Finding, ReviewDocument } from "../api/types";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { DocumentStage, type ReviewCaseContext } from "./document-stage";

const at = "2026-10-21T06:00:00Z";

function documentOf(
  id: string,
  role: string,
  filename: string,
  text: string | null,
  overrides: Partial<ReviewDocument> = {},
): ReviewDocument {
  return {
    id,
    role,
    filename,
    media_type: "text/plain",
    digest: "abc12345",
    size: 10,
    parse_status: "ready",
    parse_error: null,
    created_at: at,
    parsed_document: null,
    parsed_document_id: null,
    text,
    clauses: [
      {
        id: `${id}-clause`,
        heading: "第一条 质保",
        text_offset: { start: 0, end: 4 },
        page: 2,
      },
    ],
    ...overrides,
  };
}

function locatedAnchor(documentId: string, quote: string, start: number, end: number) {
  return {
    id: `anchor-${documentId}-${start}`,
    document_version_id: documentId,
    parsed_document_id: null,
    normalized_digest: null,
    quote,
    status: "located" as const,
    page: 2,
    text_offset: { start, end },
    bbox: null,
    mismatch_reason: null,
  };
}

function findingWith(anchors: Finding["evidence"], claim = "质保差异"): Finding {
  return {
    id: "finding-1",
    origin: "rule",
    domain: "质保",
    claim,
    relation: "conflict",
    confidence: null,
    verification_status: "verified",
    evidence: anchors,
    unverified_reason: null,
    version: 1,
    severity: null,
    severity_source: null,
    decisions: [],
  };
}

const caseContext: ReviewCaseContext = {
  caseId: "case-1",
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
      responsibilities: ["案件负责人"],
    },
  ],
  relatedCases: [
    {
      relationId: "relation-1",
      id: "case-2",
      title: "补充协议",
      reason: "同一项目",
      createdAt: at,
      runId: "run-2",
    },
  ],
  availableCases: [{ id: "case-3", title: "续签协议" }],
};

function containerDocumentId(container: HTMLElement): string | null {
  return (
    container
      .querySelector(".doc-pane")
      ?.getAttribute("data-document-version-id") ?? null
  );
}

function renderStage(
  overrides: Partial<Parameters<typeof DocumentStage>[0]> = {},
) {
  const onSelectFinding = vi.fn();
  const onCreateRelation = vi.fn().mockResolvedValue(undefined);
  const onDeleteRelation = vi.fn();
  const view = render(
    <MemoryRouter>
      <DocumentStage
        documents={[documentOf("doc-1", "procurement", "采购文件.txt", "第一条 质保三十六个月。")]}
        finding={null}
        findings={[]}
        selectedFindingId={null}
        layout="switch"
        onSelectFinding={onSelectFinding}
        onCreateRelation={onCreateRelation}
        onDeleteRelation={onDeleteRelation}
        {...overrides}
      />
    </MemoryRouter>,
  );
  return { ...view, onSelectFinding, onCreateRelation, onDeleteRelation };
}

describe("document stage case context", () => {
  it("shows the case summary with fallbacks for optional fields", () => {
    renderStage({ caseContext });
    expect(screen.getByText("案件上下文")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "云服务协议" })).toBeInTheDocument();
    expect(screen.getByText("CN-1")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "案件详情" })).toHaveAttribute(
      "href",
      "/cases/case-1",
    );
    expect(screen.getByText("甲方")).toBeInTheDocument();
    expect(screen.getByText("乙方")).toBeInTheDocument();
    expect(screen.getByText("采购合同")).toBeInTheDocument();
    expect(screen.getByText("ENT / cloud_services")).toBeInTheDocument();
    expect(screen.getByText("林晓")).toBeInTheDocument();
    expect(screen.getByText(/未设期限/)).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "审查成员 1" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "相关案件 1" })).toBeInTheDocument();
  });

  it("falls back for unset or unreported context values", () => {
    renderStage({
      caseContext: {
        ...caseContext,
        number: null,
        customer: null,
        supplier: null,
        contractType: "",
        businessLine: "未报告",
        scenario: "",
        ownerId: "m2",
        ownerName: null,
        status: "",
      },
    });
    expect(screen.getAllByText("未设置").length).toBeGreaterThan(0);
    expect(screen.getByText("未报告")).toBeInTheDocument();
    expect(screen.getByText("姓名未返回")).toBeInTheDocument();
  });

  it("shows collaborators and their responsibilities", async () => {
    const user = userEvent.setup();
    const view = renderStage({ caseContext });
    await user.click(screen.getByRole("tab", { name: /审查成员/ }));
    const region = screen.getByRole("region", { name: "案件协作人员" });
    expect(within(region).getByText("林晓")).toBeInTheDocument();
    expect(within(region).getByText("审查员")).toBeInTheDocument();
    expect(within(region).getByText("案件负责人")).toBeInTheDocument();
    view.unmount();
    renderStage({ caseContext: { ...caseContext, collaborators: [] } });
    await user.click(screen.getByRole("tab", { name: /审查成员/ }));
    expect(
      screen.getByText("当前没有可显示的案件成员。"),
    ).toBeInTheDocument();
  });

  it("lists related cases and edits relations", async () => {
    const user = userEvent.setup();
    const { onDeleteRelation, onCreateRelation } = renderStage({
      caseContext,
      canEditRelations: true,
      relationError: "关联失败",
    });
    await user.click(screen.getByRole("tab", { name: /相关案件/ }));
    expect(screen.getByRole("link", { name: "补充协议" })).toHaveAttribute(
      "href",
      "/cases/case-2/reviews/run-2",
    );
    expect(screen.getByText("同一项目")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("关联失败");
    await user.click(screen.getByRole("button", { name: "解除关联" }));
    expect(onDeleteRelation).toHaveBeenCalledWith("relation-1");
    await chooseOption(
      user,
      screen.getByRole("combobox", { name: "关联可见案件" }),
      "续签协议",
    );
    await user.type(screen.getByLabelText("关联原因"), "同一客户");
    await user.click(screen.getByRole("button", { name: "建立关联" }));
    expect(onCreateRelation).toHaveBeenCalledWith({
      related_case_id: "case-3",
      reason: "同一客户",
    });
  });

  it("explains empty relations, no available cases and read-only mode", async () => {
    const user = userEvent.setup();
    const first = renderStage({
      caseContext: { ...caseContext, relatedCases: [] },
      canEditRelations: true,
    });
    await user.click(screen.getByRole("tab", { name: /相关案件/ }));
    expect(screen.getByText("当前案件没有已保存的关系。")).toBeInTheDocument();
    first.unmount();
    const second = renderStage({
      caseContext: { ...caseContext, availableCases: [] },
      canEditRelations: true,
    });
    await user.click(screen.getByRole("tab", { name: /相关案件/ }));
    expect(
      screen.getByText("当前身份可见范围内没有可新增的案件。"),
    ).toBeInTheDocument();
    second.unmount();
    renderStage({ caseContext });
    await user.click(screen.getByRole("tab", { name: /相关案件/ }));
    expect(screen.getByText("当前案件关系为只读。")).toBeInTheDocument();
  });
});

describe("document stage materials and search", () => {
  it("labels duplicate material versions and linked findings", async () => {
    const user = userEvent.setup();
    const finding = findingWith([locatedAnchor("doc-1", "质保三十六个月", 4, 11)]);
    const first = documentOf("doc-1", "procurement", "采购文件.txt", "第一条 质保三十六个月。");
    const second = documentOf("doc-1", "procurement", "采购文件.txt", "另一份解析", {
      parsed_document_id: "parsed-2",
    });
    const { onSelectFinding } = renderStage({
      documents: [first, second],
      finding,
      findings: [finding],
      selectedFindingId: "finding-1",
      layout: "switch",
    });
    expect(screen.getAllByLabelText("采购文件，来源 1").length).toBeGreaterThan(0);
    expect(screen.getAllByLabelText("采购文件，来源 2").length).toBeGreaterThan(0);
    const linked = screen.getByRole("button", {
      name: "查看线索：质保差异",
    });
    expect(linked).toHaveAttribute("aria-pressed", "true");
    await user.click(linked);
    expect(onSelectFinding).toHaveBeenCalledWith("finding-1");
  });

  it("searches clauses and moves through matches", async () => {
    const user = userEvent.setup();
    const { container } = renderStage({
      documents: [
        documentOf(
          "doc-1",
          "procurement",
          "采购文件.txt",
          "质保三十六个月。质保二十四个月。",
        ),
      ],
    });
    await user.type(screen.getByLabelText("文内搜索"), "质保");
    expect(screen.getByText("正文匹配 2 处")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "下一处" }));
    expect(container.querySelectorAll(".hit-find").length).toBe(1);
    expect(
      screen.getByRole("button", { name: "第 2 页，第一条 质保" }),
    ).toBeInTheDocument();
    await user.clear(screen.getByLabelText("文内搜索"));
    expect(screen.getByText("输入后在当前材料中查找")).toBeInTheDocument();
  });

  it("reports empty materials and an empty clause list", () => {
    renderStage({ documents: [] });
    expect(screen.getByText("本次运行没有带上材料。")).toBeInTheDocument();
    expect(screen.getByText("这次运行没有带上材料。")).toBeInTheDocument();
    expect(
      screen.queryByLabelText("文内搜索"),
    ).not.toBeInTheDocument();
  });

  it("filters clause headings without a match", async () => {
    const user = userEvent.setup();
    renderStage({
      documents: [documentOf("doc-1", "procurement", "采购文件.txt", "正文")],
    });
    await user.type(screen.getByLabelText("文内搜索"), "没有此条款");
    expect(
      screen.getByText("这份材料没有可在原文中找到的已保存条款。"),
    ).toBeInTheDocument();
  });
});

describe("document stage rendering states", () => {
  it("renders a failed parse with its preserved snapshot", () => {
    const finding = findingWith([locatedAnchor("doc-1", "保存的引用", 0, 5)]);
    renderStage({
      documents: [
        documentOf("doc-1", "procurement", "采购文件.txt", null, {
          parse_status: "failed",
          parse_error: "解析器失败",
        }),
      ],
      finding,
      findings: [finding],
    });
    expect(screen.getByText("解析失败")).toBeInTheDocument();
    expect(
      screen.getByText("这份材料解析失败，不能据此给出无风险结论。"),
    ).toBeInTheDocument();
    expect(screen.getByText("解析器失败")).toBeInTheDocument();
    expect(screen.getByText("保存的引用")).toBeInTheDocument();
  });

  it("shows diagnostics with the parsed document summary", async () => {
    const user = userEvent.setup();
    renderStage({
      documents: [
        documentOf("doc-1", "procurement", "采购文件.txt", "正文", {
          parsed_document: {
            id: "parsed-1",
            document_id: "doc-1",
            original_digest: "original",
            parser_id: "parser",
            parser_version: "1",
            config_hash: "config",
            normalization_version: "v1",
            normalized_digest: "norm",
            ir_digest: "ir",
            artifact_digest: "artifact",
            created_at: at,
            status: "ready",
          },
        }),
      ],
      showDiagnostics: true,
    });
    await user.click(screen.getByRole("button", { name: "解析来源与摘要" }));
    expect(screen.getByLabelText("解析来源与摘要")).toHaveAttribute(
      "data-parsed-document-id",
      "parsed-1",
    );
  });

  it("keeps a saved quote when the body text is unavailable", () => {
    const finding = findingWith([locatedAnchor("doc-1", "历史引用", 0, 4)]);
    renderStage({
      documents: [documentOf("doc-1", "procurement", "采购文件.txt", null)],
      finding,
      findings: [finding],
    });
    expect(
      screen.getByText(
        "正文尚未返回。下面只显示保存下来的引用快照，没有在文档中定位。",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("历史引用")).toBeInTheDocument();
  });

  it("explains a missing reference without a quote", () => {
    const finding = findingWith([
      {
        ...locatedAnchor("doc-1", "", 0, 0),
        status: "missing",
      },
    ]);
    renderStage({
      documents: [documentOf("doc-1", "procurement", "采购文件.txt", "正文")],
      finding,
      findings: [finding],
    });
    expect(
      screen.getByText(
        "找不到对应来源，不能把这条引用当成已验证证据。",
      ),
    ).toBeInTheDocument();
  });

  it("highlights a located quote and otherwise renders readable text offsets", () => {
    const finding = findingWith([locatedAnchor("doc-1", "质保三十六个月", 4, 11)]);
    const { container } = renderStage({
      documents: [documentOf("doc-1", "procurement", "采购文件.txt", "第一条 质保三十六个月。")],
      finding,
      findings: [finding],
    });
    const mark = container.querySelector("mark.hit-evidence");
    expect(mark?.textContent).toBe("质保三十六个月");
    expect(mark?.getAttribute("data-offset")).toBe("4");

    const second = renderStage({
      documents: [documentOf("doc-2", "attachment", "附件.txt", "一段正文")],
    });
    expect(second.container.querySelector(".doc-text")?.textContent).toBe(
      "一段正文",
    );
  });

  it("shows a legacy parsed id in data attributes", () => {
    const { container } = renderStage({
      documents: [
        documentOf("doc-1", "procurement", "采购文件.txt", "正文", {
          parsed_document_id: "legacy-parsed",
        }),
      ],
    });
    expect(
      container.querySelector(".doc-pane")?.getAttribute("data-parsed-document-id"),
    ).toBe("legacy-parsed");
  });
});

describe("document stage comparison modes", () => {
  const first = documentOf("doc-1", "procurement", "采购文件.txt", "质保三十六个月");
  const second = documentOf("doc-2", "draft_contract", "拟签合同.txt", "质保二十四个月");

  it("offers both reading modes only for two evidence sources in split layout", async () => {
    const user = userEvent.setup();
    const finding = findingWith([
      locatedAnchor("doc-1", "质保三十六个月", 4, 11),
      locatedAnchor("doc-2", "质保二十四个月", 0, 7),
    ]);
    const { container } = renderStage({
      documents: [first, second],
      finding,
      findings: [finding],
      layout: "split",
    });
    await user.click(screen.getByRole("button", { name: "双来源对照" }));
    expect(screen.getAllByText("合同与证据原文").length).toBeGreaterThan(0);
    expect(container.querySelectorAll(".doc-pane")).toHaveLength(2);
    expect(screen.getByText("质保三十六个月")).toBeInTheDocument();
    expect(screen.getByText("质保二十四个月")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "单篇阅读" }));
    expect(container.querySelectorAll(".doc-pane")).toHaveLength(1);
  });

  it("switches evidence sources in switch layout", async () => {
    const user = userEvent.setup();
    const finding = findingWith([
      locatedAnchor("doc-1", "质保三十六个月", 4, 11),
      locatedAnchor("doc-2", "质保二十四个月", 0, 7),
    ]);
    const { container } = renderStage({
      documents: [first, second],
      finding,
      findings: [finding],
      layout: "switch",
    });
    expect(
      screen.queryByRole("button", { name: "双来源对照" }),
    ).not.toBeInTheDocument();
    const group = screen.getByRole("group", { name: "证据来源" });
    expect(within(group).getAllByRole("button")).toHaveLength(2);
    await user.click(within(group).getByRole("button", { name: "拟签合同" }));
    expect(container.querySelector("mark.hit-evidence")?.textContent).toBe(
      "质保二十四个月",
    );
  });

  it("does not offer comparison with a single evidence source", () => {
    const finding = findingWith([locatedAnchor("doc-1", "质保三十六个月", 4, 11)]);
    renderStage({
      documents: [first, documentOf("doc-3", "attachment", "附件.txt", "无关正文")],
      finding,
      findings: [finding],
      layout: "split",
    });
    expect(
      screen.queryByRole("button", { name: "双来源对照" }),
    ).not.toBeInTheDocument();
  });

  it("moves to the evidence source when the selected finding changes", async () => {
    const user = userEvent.setup();
    const finding = findingWith([locatedAnchor("doc-2", "质保二十四个月", 0, 7)]);
    const view = renderStage({
      documents: [first, second],
      finding: null,
      findings: [finding],
      layout: "switch",
    });
    expect(containerDocumentId(view.container)).toBe("doc-1");
    view.rerender(
      <MemoryRouter>
        <DocumentStage
          documents={[first, second]}
          finding={finding}
          findings={[finding]}
          selectedFindingId={finding.id}
          layout="switch"
          onSelectFinding={vi.fn()}
        />
      </MemoryRouter>,
    );
    await waitFor(() => expect(containerDocumentId(view.container)).toBe("doc-2"));
    expect(containerDocumentId(view.container)).toBe("doc-2");
  });
});
