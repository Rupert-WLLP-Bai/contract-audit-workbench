// File metadata (manifest mirror):
// Purpose: Checks frozen text and original preview navigation, reading category selection, search and citations, and scroll behavior when switching source documents.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-30
// Updated: 2026-11-20
// Revision: r11
// Copyright: See LICENSE
// End file metadata.

import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import type { ContractFactsCitation, ContractFactsProjection } from "../api/contract-facts";
import type { Finding, ReviewDocument } from "../api/types";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { DocumentStage, type ReviewCaseContext } from "./document-stage";
import { OriginalSourceContext } from "./original-document-reader";
import type { SourceQuote } from "../domain/source-annotation";

vi.mock("./original-page-view", () => ({
  default: ({ quotes }: { quotes: readonly SourceQuote[] }) => (
    <div data-testid="original-page-quotes">{quotes.map((quote) => quote.quote).join("；")}</div>
  ),
}));

const at = "2026-10-21T06:00:00Z";

it("projects parser markers without changing exact raw selection offsets", async () => {
  const raw = "# 😀标题\n付款三十日。\n![](images/a.jpg)\n目录……… 2";
  const doc = documentOf("doc-projection", "draft_contract", "合同.pdf", raw, {
    parsed_document_id: "parsed-projection",
    clauses: [],
  });
  const scope = {
    caseId: "case-1",
    contextKey: "attempt-1",
    digests: new Map([[doc.id, doc.digest]]),
  };
  const quoteScope = {
    caseId: "case-1",
    runId: "run-1",
    attemptId: "attempt-1",
    isFrozenAttempt: true,
    parsedDocumentIds: ["parsed-projection"],
    sourceProofs: [
      { documentId: doc.id, parsedDocumentId: "parsed-projection", originalDigest: doc.digest },
    ],
  };
  const onQuote = vi.fn();
  const view = render(
    <MemoryRouter>
      <OriginalSourceContext.Provider value={scope}>
        <DocumentStage
          documents={[doc]}
          finding={null}
          findings={[]}
          selectedFindingId={null}
          onSelectFinding={vi.fn()}
          layout="switch"
          quoteScope={quoteScope}
          onQuoteSelectionChange={onQuote}
        />
      </OriginalSourceContext.Provider>
    </MemoryRouter>,
  );
  await userEvent.setup().click(screen.getByRole("button", { name: "文字原文" }));
  const content = view.container.querySelector<HTMLElement>(".doc-text")!;
  expect(content.textContent).toBe(raw);
  expect(view.container.querySelector(".doc-projection-heading")?.textContent).toContain("😀标题");
  expect(view.container.querySelectorAll("[data-projection-hidden]").length).toBeGreaterThanOrEqual(
    3,
  );
  const walker = window.document.createTreeWalker(content, NodeFilter.SHOW_TEXT);
  let payment: Node | null = null;
  while (walker.nextNode())
    if (walker.currentNode.textContent?.includes("付款三十日。")) {
      payment = walker.currentNode;
      break;
    }
  expect(payment).toBeTruthy();
  const selection = window.getSelection()!,
    range = window.document.createRange();
  const offset = payment!.textContent!.indexOf("付款三十日。");
  range.setStart(payment!, offset);
  range.setEnd(payment!, offset + 6);
  selection.removeAllRanges();
  selection.addRange(range);
  fireEvent.mouseUp(content);
  expect(onQuote).toHaveBeenLastCalledWith(
    expect.objectContaining({ quote: "付款三十日。", start: 6, end: 12 }),
  );
  const hiddenImage = content.querySelector<HTMLElement>("[data-projection-hidden]")!;
  const across = window.document.createRange();
  across.setStart(hiddenImage.firstChild!, 0);
  across.setEnd(payment!, offset + 2);
  selection.removeAllRanges();
  selection.addRange(across);
  fireEvent.mouseUp(content);
  expect(onQuote).toHaveBeenLastCalledWith(null);
  await userEvent.setup().click(screen.getByRole("button", { name: "查看原件中的图片" }));
  expect(await screen.findByTestId("original-page-quotes")).toBeVisible();
});

it("keeps a finding marker at a hidden heading prefix", () => {
  const text = "# 标题\n正文";
  const doc = documentOf("doc-hidden", "draft_contract", "合同.txt", text, {
    clauses: [],
    parsed_document_id: "pc",
  });
  const finding = findingWith([
    { ...locatedAnchor(doc.id, "# 标题", 0, 4), parsed_document_id: "pc" },
  ]);
  const choose = vi.fn();
  const view = renderStage({
    documents: [doc],
    finding,
    findings: [finding],
    selectedFindingId: finding.id,
    onSelectFinding: choose,
    immersive: true,
    quoteScope: {
      caseId: "case-1",
      runId: "run-1",
      attemptId: "attempt-1",
      isFrozenAttempt: true,
      parsedDocumentIds: ["pc"],
      sourceProofs: [{ documentId: doc.id, parsedDocumentId: "pc", originalDigest: doc.digest }],
    },
  });
  const marker = view.container.querySelector<HTMLElement>('[data-finding-number="1"]');
  expect(marker).toBeTruthy();
  expect(view.container.querySelector('[data-offset="0"]')).toContainElement(marker);
  fireEvent.click(marker!);
  expect(choose).toHaveBeenCalledWith(finding.id);
});

it("connects a frozen finding to original-page annotations and keeps unfrozen history on the text reader", async () => {
  const document = documentOf("doc-pdf", "draft_contract", "合同.pdf", "甲方支付100万元。", {
    parsed_document_id: "parsed-pdf",
    media_type: "application/pdf",
  });
  const finding = findingWith([locatedAnchor(document.id, "100万元", 4, 9)]);
  const scope = {
    caseId: "case-1",
    contextKey: "reviewer:attempt-1",
    digests: new Map([[document.id, document.digest]]),
  };
  const quoteScope = {
    caseId: "case-1",
    runId: "run-1",
    attemptId: "attempt-1",
    isFrozenAttempt: true,
    parsedDocumentIds: ["parsed-pdf"],
  };
  const element = (frozen: boolean) => (
    <MemoryRouter>
      <OriginalSourceContext.Provider value={scope}>
        <DocumentStage
          documents={[document]}
          finding={finding}
          findings={[finding]}
          selectedFindingId={finding.id}
          onSelectFinding={() => undefined}
          layout="switch"
          quoteScope={{ ...quoteScope, isFrozenAttempt: frozen }}
        />
      </OriginalSourceContext.Provider>
    </MemoryRouter>
  );
  const view = render(element(true));
  expect(await screen.findByTestId("original-page-quotes")).toHaveTextContent("100万元");
  await userEvent.setup().click(screen.getByRole("button", { name: "文字原文" }));
  expect(view.container.querySelector("mark.hit-evidence")?.textContent).toBe("100万元");
  view.rerender(element(false));
  expect(screen.queryByRole("button", { name: "原件标注" })).not.toBeInTheDocument();
});

it("opens all verified PDF positions as frozen text before any finding is selected", async () => {
  const user = userEvent.setup();
  const text = "😀付款后交付。质保三年。争议提交仲裁。";
  const document = documentOf("doc-pdf", "draft_contract", "合同.pdf", text, {
    parsed_document_id: "parsed-pdf",
    media_type: "application/pdf",
    clauses: [],
  });
  const scope = {
    caseId: "case-1",
    contextKey: "attempt-1",
    digests: new Map([[document.id, document.digest]]),
  };
  const quoteScope = {
    caseId: "case-1",
    runId: "run-1",
    attemptId: "attempt-1",
    isFrozenAttempt: true,
    parsedDocumentIds: ["parsed-pdf"],
    sourceProofs: [
      { documentId: document.id, parsedDocumentId: "parsed-pdf", originalDigest: document.digest },
    ],
  };
  const view = render(
    <MemoryRouter>
      <OriginalSourceContext.Provider value={scope}>
        <DocumentStage
          documents={[document]}
          finding={null}
          findings={[]}
          selectedFindingId={null}
          onSelectFinding={vi.fn()}
          layout="switch"
          quoteScope={quoteScope}
        />
      </OriginalSourceContext.Provider>
    </MemoryRouter>,
  );
  expect(view.container.querySelectorAll(".doc-text .hit-key-clause")).toHaveLength(3);
  expect(view.container.querySelector("[data-selected-key-clause='true']")).toBeNull();
  await user.click(screen.getByRole("button", { name: "查看重点" }));
  expect(view.container.querySelector("[data-selected-key-clause='true']")?.textContent).toContain(
    "付款",
  );
  await user.click(screen.getByRole("button", { name: "下一个重点" }));
  expect(view.container.querySelector("[data-selected-key-clause='true']")?.textContent).toContain(
    "质保",
  );
  await user.click(screen.getByRole("button", { name: "上一个重点" }));
  expect(view.container.querySelector("[data-selected-key-clause='true']")?.textContent).toContain(
    "付款",
  );
  expect(screen.getByRole("button", { name: "文字原文" })).toHaveAttribute("aria-pressed", "true");
  await user.click(screen.getByRole("button", { name: "原件标注" }));
  expect(await screen.findByTestId("original-page-quotes")).toBeVisible();
  await user.click(screen.getByRole("button", { name: /正文片段\/标题未识别.*争议解决/ }));
  expect(view.container.querySelector("[data-selected-key-clause='true']")?.textContent).toContain(
    "仲裁",
  );
});

it("distinguishes a selected clause, a search match, and overlapping evidence", async () => {
  const user = userEvent.setup();
  const text = "付款三十日内支付。";
  const document = documentOf("doc-overlap", "draft_contract", "合同.txt", text, {
    parsed_document_id: "parse-overlap",
    clauses: [
      {
        id: "payment",
        heading: "付款",
        text_offset: { start: 0, end: Array.from(text).length },
        page: 1,
      },
    ],
  });
  const finding = findingWith([locatedAnchor(document.id, "三十日", 2, 5)]);
  const { container } = renderStage({
    documents: [document],
    finding,
    findings: [finding],
    selectedFindingId: finding.id,
    quoteScope: {
      caseId: "case-1",
      runId: "run-1",
      attemptId: "attempt-1",
      isFrozenAttempt: true,
      parsedDocumentIds: ["parse-overlap"],
      sourceProofs: [
        {
          documentId: document.id,
          parsedDocumentId: "parse-overlap",
          originalDigest: document.digest,
        },
      ],
    },
  });
  await user.click(screen.getByRole("button", { name: /付款.*起始页第 1 页/ }));
  await user.type(screen.getByRole("textbox", { name: "文内搜索" }), "三十日");
  const mark = container.querySelector(".hit-evidence");
  expect(mark).toHaveClass("key-clause-overlap", "key-clause-selected", "evidence-find");
  expect(mark).toHaveAttribute("data-selected-key-clause", "true");
});

it("filters the three reading categories without changing the frozen text", async () => {
  const user = userEvent.setup();
  const text =
    "甲方应在验收后10个工作日内支付首期款。乙方应在10个工作日内交付服务成果。甲方有权要求乙方赔偿实际损失。";
  const document = documentOf("doc-groups", "draft_contract", "合同.txt", text, {
    parsed_document_id: "parse-groups",
    clauses: [],
  });
  const scope = {
    caseId: "case-1",
    runId: "run-1",
    attemptId: "attempt-1",
    isFrozenAttempt: true,
    parsedDocumentIds: ["parse-groups"],
    sourceProofs: [
      {
        documentId: document.id,
        parsedDocumentId: "parse-groups",
        originalDigest: document.digest,
      },
    ],
  };
  const { container } = renderStage({ documents: [document], quoteScope: scope });
  const textNode = container.querySelector(".doc-text");
  expect(textNode?.textContent).toBe(text);
  expect(container.querySelectorAll(".reading-money")).toHaveLength(1);
  expect(container.querySelectorAll(".reading-performance")).toHaveLength(1);
  expect(container.querySelectorAll(".reading-rights")).toHaveLength(1);
  const filters = screen.getByRole("group", { name: "阅读分类" });
  await user.click(within(filters).getByRole("button", { name: "金额与付款" }));
  expect(textNode?.textContent).toBe(text);
  expect(container.querySelectorAll(".reading-money")).toHaveLength(1);
  expect(container.querySelectorAll(".reading-performance, .reading-rights")).toHaveLength(0);
  await user.click(within(filters).getByRole("button", { name: "权利与责任" }));
  expect(container.querySelectorAll(".reading-rights")).toHaveLength(1);
  await user.click(within(filters).getByRole("button", { name: "全部" }));
  expect(container.querySelectorAll(".hit-key-clause")).toHaveLength(3);
});

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
  return container.querySelector(".doc-pane")?.getAttribute("data-document-version-id") ?? null;
}

function renderStage(overrides: Partial<Parameters<typeof DocumentStage>[0]> = {}) {
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
    expect(screen.getByRole("link", { name: "案件详情" })).toHaveAttribute("href", "/cases/case-1");
    expect(screen.getByText("甲方")).toBeInTheDocument();
    expect(screen.getByText("乙方")).toBeInTheDocument();
    expect(screen.getByText("采购合同")).toBeInTheDocument();
    expect(screen.getByText("政企业务 / 云服务采购")).toBeInTheDocument();
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
    expect(screen.getAllByText("未报告").length).toBeGreaterThan(0);
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
    expect(screen.getByText("当前没有可显示的案件成员。")).toBeInTheDocument();
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
    await chooseOption(user, screen.getByRole("combobox", { name: "关联可见案件" }), "续签协议");
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
    expect(screen.getByText("暂无可添加的案件。")).toBeInTheDocument();
    second.unmount();
    renderStage({ caseContext });
    await user.click(screen.getByRole("tab", { name: /相关案件/ }));
    expect(screen.getByText("当前案件关系为只读。")).toBeInTheDocument();
  });
});

describe("document stage materials and search", () => {
  it("locates a facts citation by Unicode codepoint in its matching frozen source", async () => {
    const text = "甲方应支付金额💰元。";
    const document = documentOf("doc-facts", "draft_contract", "合同.txt", text, {
      digest: "",
      parsed_document_id: "parsed-facts",
      parsed_document: null,
    });
    const citation: ContractFactsCitation = {
      document_id: document.id,
      parsed_document_id: "parsed-facts",
      original_digest: "a".repeat(64),
      normalized_digest: "b".repeat(64),
      quote: "金额💰",
      start: 5,
      end: 8,
    };
    const projection: ContractFactsProjection = {
      schema_version: "contract-facts/v1",
      attempt_id: "attempt-facts",
      status: "partial",
      structured_output_status: "valid",
      source: {
        document_id: document.id,
        parsed_document_id: "parsed-facts",
        original_digest: "a".repeat(64),
        normalized_digest: "b".repeat(64),
      },
      input_codepoints: Array.from(text).length,
      input_truncated: false,
      fields: [
        {
          field: "contract_amount",
          status: "known",
          value: "金额💰",
          citations: [citation],
        },
        ...(
          [
            "parties",
            "tax_treatment",
            "business_dates",
            "term",
            "payment_schedule",
            "subject_matter",
            "exceptions",
          ] as const
        ).map((field) => ({ field, status: "unknown" as const, value: "", citations: [] })),
      ],
    };
    const { container } = renderStage({
      documents: [document],
      contractFactsCitation: citation,
      contractFactsLocationScope: {
        caseId: "case-facts",
        runId: "run-facts",
        attemptId: "attempt-facts",
        historyKind: "frozen",
        parsedDocumentIds: ["parsed-facts"],
        projection,
      },
    });

    const mark = await waitFor(() => {
      const selected = container.querySelector(".hit-find");
      expect(selected).not.toBeNull();
      return selected;
    });
    expect(mark?.textContent).toBe("金额💰");
    expect(mark?.getAttribute("data-offset")).toBe("5");
    expect(container.querySelector(".doc-pane")?.getAttribute("data-document-version-id")).toBe(
      "doc-facts",
    );
  });

  it("does not highlight facts when the parsed identity is outside the selected frozen attempt", () => {
    const text = "甲方应支付金额💰元。";
    const document = documentOf("doc-facts", "draft_contract", "合同.txt", text, {
      digest: "",
      parsed_document_id: "parsed-facts",
      parsed_document: null,
    });
    const citation: ContractFactsCitation = {
      document_id: document.id,
      parsed_document_id: "parsed-facts",
      original_digest: "a".repeat(64),
      normalized_digest: "b".repeat(64),
      quote: "金额💰",
      start: 5,
      end: 8,
    };
    const projection: ContractFactsProjection = {
      schema_version: "contract-facts/v1",
      attempt_id: "attempt-facts",
      status: "partial",
      structured_output_status: "valid",
      source: {
        document_id: document.id,
        parsed_document_id: "parsed-facts",
        original_digest: citation.original_digest,
        normalized_digest: citation.normalized_digest,
      },
      input_codepoints: Array.from(text).length,
      input_truncated: false,
      fields: [
        { field: "contract_amount", status: "known", value: "金额💰", citations: [citation] },
        ...(
          [
            "parties",
            "tax_treatment",
            "business_dates",
            "term",
            "payment_schedule",
            "subject_matter",
            "exceptions",
          ] as const
        ).map((field) => ({ field, status: "unknown" as const, value: "", citations: [] })),
      ],
    };
    const { container } = renderStage({
      documents: [document],
      contractFactsCitation: citation,
      contractFactsLocationScope: {
        caseId: "case-facts",
        runId: "run-facts",
        attemptId: "attempt-facts",
        historyKind: "frozen",
        parsedDocumentIds: ["another-parsed-document"],
        projection,
      },
    });
    expect(container.querySelector("mark.hit-find")).toBeNull();
  });

  it("navigates to frozen key clauses without a finding and marks them separately", async () => {
    const user = userEvent.setup();
    const text = "😀第一条 付款约定：甲方应支付预付款。";
    const quote = "第一条 付款约定：甲方应支付预付款。";
    const points = Array.from(text);
    const start = points.findIndex((_, index) =>
      Array.from(quote).every((character, quoteIndex) => points[index + quoteIndex] === character),
    );
    const document = documentOf("doc-key", "procurement", "合同.txt", text, {
      parsed_document_id: "parsed-key",
      parsed_document: {
        id: "parsed-key",
        document_id: "doc-key",
        original_digest: "abc12345",
        parser_id: "parser",
        parser_version: "1",
        config_hash: "config",
        normalization_version: "v1",
        normalized_digest: "normalized",
        ir_digest: "ir",
        artifact_digest: "artifact",
        created_at: at,
        status: "ready",
      },
      clauses: [
        {
          id: "clause-payment",
          heading: "付款约定",
          text_offset: { start, end: start + Array.from(quote).length },
          page: null,
        },
      ],
    });
    const { container, onSelectFinding } = renderStage({
      documents: [document],
      quoteScope: {
        caseId: "case-1",
        runId: "run-1",
        attemptId: "attempt-1",
        isFrozenAttempt: true,
        parsedDocumentIds: ["parsed-key"],
      },
    });

    await user.click(screen.getByRole("button", { name: "显示更多条款" }));
    expect(screen.getByRole("heading", { name: "阅读重点" })).toBeInTheDocument();
    expect(screen.getByText(/选择后跳到原文/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /付款约定.*页码未记录/ }));

    const mark = container.querySelector("mark.hit-key-clause[data-selected-key-clause='true']");
    expect(mark?.textContent).toBe(quote);
    expect(mark).not.toHaveClass("hit-evidence");
    expect(screen.getByText(/未记录页码/)).toBeInTheDocument();
    expect(onSelectFinding).not.toHaveBeenCalled();
  });

  it("shows unstructured body fragments as text-only candidates when clauses are absent", async () => {
    const user = userEvent.setup();
    const text = "😀前言说明。\n甲方付款约定：乙方应支付预付款。";
    const document = documentOf("doc-body", "procurement", "扫描件.txt", text, {
      parsed_document_id: "parsed-body",
      parsed_document: {
        id: "parsed-body",
        document_id: "doc-body",
        original_digest: "abc12345",
        parser_id: "parser",
        parser_version: "1",
        config_hash: "config",
        normalization_version: "v1",
        normalized_digest: "normalized",
        ir_digest: "ir",
        artifact_digest: "artifact",
        created_at: at,
        status: "ready",
      },
      clauses: [],
    });
    const { container } = renderStage({
      documents: [document],
      quoteScope: {
        caseId: "case-1",
        runId: "run-1",
        attemptId: "attempt-1",
        isFrozenAttempt: true,
        parsedDocumentIds: ["parsed-body"],
      },
    });
    await user.click(screen.getByRole("button", { name: "显示更多条款" }));
    const choice = screen.getByRole("button", {
      name: /正文片段\/标题未识别.*付款.*页码未记录/,
    });

    expect(choice).toBeInTheDocument();
    await user.click(choice);

    const mark = container.querySelector("mark.hit-key-clause[data-selected-key-clause='true']");
    expect(mark?.textContent).toContain("支付预付款");
    expect(mark).not.toHaveClass("hit-evidence");
    expect(screen.getByText(/未记录页码/)).toBeInTheDocument();
  });

  it("clears an auxiliary clause highlight when the selected material changes", async () => {
    const user = userEvent.setup();
    const firstText = "第一条 付款约定：甲方支付预付款。";
    const firstQuote = firstText;
    const first = documentOf("doc-key", "procurement", "合同.txt", firstText, {
      parsed_document_id: "parsed-key",
      parsed_document: {
        id: "parsed-key",
        document_id: "doc-key",
        original_digest: "abc12345",
        parser_id: "parser",
        parser_version: "1",
        config_hash: "config",
        normalization_version: "v1",
        normalized_digest: "normalized",
        ir_digest: "ir",
        artifact_digest: "artifact",
        created_at: at,
        status: "ready",
      },
      clauses: [
        {
          id: "clause-payment",
          heading: "付款约定",
          text_offset: { start: 0, end: Array.from(firstQuote).length },
          page: 1,
        },
      ],
    });
    const second = documentOf("doc-plain", "attachment", "附件.txt", "仅供阅读的说明文本", {
      parsed_document_id: "parsed-plain",
      parsed_document: {
        id: "parsed-plain",
        document_id: "doc-plain",
        original_digest: "abc12345",
        parser_id: "parser",
        parser_version: "1",
        config_hash: "config",
        normalization_version: "v1",
        normalized_digest: "normalized",
        ir_digest: "ir",
        artifact_digest: "artifact",
        created_at: at,
        status: "ready",
      },
      clauses: [],
    });
    const { container } = renderStage({
      documents: [first, second],
      quoteScope: {
        caseId: "case-1",
        runId: "run-1",
        attemptId: "attempt-1",
        isFrozenAttempt: true,
        parsedDocumentIds: ["parsed-key", "parsed-plain"],
      },
    });
    await user.click(screen.getByRole("button", { name: "显示更多条款" }));
    await user.click(screen.getByRole("button", { name: /付款约定.*第 1 页/ }));
    expect(container.querySelector(".hit-key-clause")).not.toBeNull();

    await user.click(screen.getByRole("tab", { name: "技术附件" }));
    expect(container.querySelector(".hit-key-clause")).toBeNull();
    expect(screen.getAllByText("当前材料中没有可定位的关键条款。")[0]).toBeInTheDocument();
  });

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
        documentOf("doc-1", "procurement", "采购文件.txt", "质保三十六个月。质保二十四个月。"),
      ],
    });
    await user.type(screen.getByLabelText("文内搜索"), "质保");
    expect(screen.getByText("正文匹配 2 处")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "搜索下一处" }));
    expect(container.querySelectorAll(".hit-find").length).toBe(1);
    expect(screen.getByRole("button", { name: "起始页第 2 页，第一条 质保" })).toBeInTheDocument();
    await user.clear(screen.getByLabelText("文内搜索"));
    expect(screen.getByText("输入后在当前材料中查找")).toBeInTheDocument();
  });

  it("reports empty materials and an empty clause list", () => {
    renderStage({ documents: [] });
    expect(screen.getByText("本次运行没有带上材料。")).toBeInTheDocument();
    expect(screen.getByText("这次运行没有带上材料。")).toBeInTheDocument();
    expect(screen.queryByLabelText("文内搜索")).not.toBeInTheDocument();
  });

  it("filters clause headings without a match", async () => {
    const user = userEvent.setup();
    renderStage({
      documents: [documentOf("doc-1", "procurement", "采购文件.txt", "正文")],
    });
    await user.type(screen.getByLabelText("文内搜索"), "没有此条款");
    expect(screen.getByText("这份材料没有可在原文中找到的已保存条款。")).toBeInTheDocument();
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
    expect(screen.getByText("这份材料解析失败，请先处理解析问题再继续审查。")).toBeInTheDocument();
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
      screen.getByText("正文尚未返回。下面只显示保存下来的引用快照，没有在文档中定位。"),
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
    expect(screen.getByText("找不到对应来源，不能把这条引用当成已验证证据。")).toBeInTheDocument();
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
    expect(second.container.querySelector(".doc-text")?.textContent).toBe("一段正文");
  });

  it("shows a legacy parsed id in data attributes", () => {
    const { container } = renderStage({
      documents: [
        documentOf("doc-1", "procurement", "采购文件.txt", "正文", {
          parsed_document_id: "legacy-parsed",
        }),
      ],
    });
    expect(container.querySelector(".doc-pane")?.getAttribute("data-parsed-document-id")).toBe(
      "legacy-parsed",
    );
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
    expect(screen.getAllByText("质保三十六个月").length).toBeGreaterThan(0);
    expect(screen.getByText("质保二十四个月")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "单篇阅读" }));
    expect(container.querySelectorAll(".doc-pane")).toHaveLength(1);
  });

  it("independently highlights both frozen sources in comparison", async () => {
    const user = userEvent.setup();
    const firstDoc = { ...first, parsed_document_id: "parse-1", clauses: [] };
    const secondDoc = { ...second, parsed_document_id: "parse-2", clauses: [] };
    const finding = findingWith([
      locatedAnchor("doc-1", "质保三十六个月", 0, 7),
      locatedAnchor("doc-2", "质保二十四个月", 0, 7),
    ]);
    const { container } = renderStage({
      documents: [firstDoc, secondDoc],
      finding,
      findings: [finding],
      layout: "split",
      quoteScope: {
        caseId: "case-1",
        runId: "run-1",
        attemptId: "attempt-1",
        isFrozenAttempt: true,
        parsedDocumentIds: ["parse-1", "parse-2"],
        sourceProofs: [firstDoc, secondDoc].map((doc) => ({
          documentId: doc.id,
          parsedDocumentId: doc.parsed_document_id!,
          originalDigest: doc.digest,
        })),
      },
    });
    await user.click(screen.getByRole("button", { name: "双来源对照" }));
    const panes = container.querySelectorAll(".doc-pane");
    expect(panes).toHaveLength(2);
    expect(panes[0]?.querySelector(".key-clause-overlap")).not.toBeNull();
    expect(panes[1]?.querySelector(".key-clause-overlap")).not.toBeNull();
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
    expect(screen.queryByRole("button", { name: "双来源对照" })).not.toBeInTheDocument();
    const group = screen.getByRole("group", { name: "证据来源" });
    expect(within(group).getAllByRole("button")).toHaveLength(2);
    await user.click(within(group).getByRole("button", { name: "拟签合同" }));
    expect(container.querySelector("mark.hit-evidence")?.textContent).toBe("质保二十四个月");
  });

  it("does not offer comparison with a single evidence source", () => {
    const finding = findingWith([locatedAnchor("doc-1", "质保三十六个月", 4, 11)]);
    renderStage({
      documents: [first, documentOf("doc-3", "attachment", "附件.txt", "无关正文")],
      finding,
      findings: [finding],
      layout: "split",
    });
    expect(screen.queryByRole("button", { name: "双来源对照" })).not.toBeInTheDocument();
  });

  it("moves to the evidence source when the selected finding changes", async () => {
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

describe("immersive document stage", () => {
  it("shows both roles with their matched versions and leaves attachments separate", () => {
    const procurement = documentOf("p", "procurement", "采购.txt", "采购原文");
    const contract = documentOf("c", "draft_contract", "合同.txt", "合同原文");
    const attachment = documentOf("a", "attachment", "附件.txt", "附件原文");
    const { container } = renderStage({
      immersive: true,
      documents: [procurement, contract, attachment],
      materialVersions: [
        {
          material_id: "m",
          version: 3,
          document_id: "c",
          previous_document_id: null,
          created_at: at,
        },
      ],
    });
    const columns = container.querySelectorAll(".immersive-document-column");
    expect(columns).toHaveLength(2);
    expect(within(columns[0] as HTMLElement).getByText("版本未记录")).toBeInTheDocument();
    expect(within(columns[1] as HTMLElement).getByText("第 3 版")).toBeInTheDocument();
    expect(screen.getByText("其他材料（1）")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "采购文件正文" })).toHaveAttribute("tabindex", "0");
    expect(screen.getByRole("region", { name: "拟签合同正文" })).toHaveAttribute("tabindex", "0");
  });

  it("activates the cited material and keeps both reading positions mounted", async () => {
    const first = documentOf("p", "procurement", "采购.txt", "采购原文");
    const second = documentOf("c", "draft_contract", "合同.txt", "合同原文");
    const props = {
      immersive: true,
      documents: [first, second],
      finding: null,
      findings: [],
      selectedFindingId: null,
      layout: "switch" as const,
      onSelectFinding: vi.fn(),
    };
    const view = render(
      <MemoryRouter>
        <DocumentStage {...props} />
      </MemoryRouter>,
    );
    const procurement = screen.getByRole("region", { name: "采购文件正文" });
    procurement.scrollTop = 240;

    view.rerender(
      <MemoryRouter>
        <DocumentStage {...props} sourceDocumentId="c" />
      </MemoryRouter>,
    );

    const switcher = screen.getByRole("group", { name: "切换对照材料", hidden: true });
    expect(
      within(switcher).getByRole("button", { name: "拟签合同", hidden: true }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("region", { name: "采购文件正文" })).toBe(procurement);
    expect(procurement.scrollTop).toBe(240);
  });

  it("marks only an exact finding quote and preserves manual scrolling across role switches", async () => {
    const user = userEvent.setup();
    const procurement = documentOf("p", "procurement", "采购.txt", "质保三十六个月。", {
      parsed_document_id: "pp",
    });
    const contract = documentOf("c", "draft_contract", "合同.txt", "质保二十四个月。", {
      parsed_document_id: "pc",
    });
    const anchor = { ...locatedAnchor("c", "二十四", 2, 5), parsed_document_id: "pc" };
    const finding = findingWith([anchor]);
    const { container } = renderStage({
      immersive: true,
      documents: [procurement, contract],
      finding,
      findings: [finding],
      selectedFindingId: finding.id,
      quoteScope: {
        caseId: "case-1",
        runId: "run-1",
        attemptId: "attempt-1",
        isFrozenAttempt: true,
        parsedDocumentIds: ["pp", "pc"],
        sourceProofs: [
          { documentId: "p", parsedDocumentId: "pp", originalDigest: procurement.digest },
          { documentId: "c", parsedDocumentId: "pc", originalDigest: contract.digest },
        ],
      },
    });
    expect(container.querySelector('[data-source-id="p"] .hit-evidence')).toBeNull();
    expect(container.querySelector('[data-source-id="c"] .hit-evidence')).toHaveTextContent(
      "二十四",
    );
    expect(container.querySelector('[data-source-id="c"] .hit-evidence')).toHaveAttribute(
      "data-finding-number",
      "1",
    );

    const contractText = screen.getByRole("region", { name: "拟签合同正文" });
    Object.defineProperty(contractText, "clientHeight", { value: 100 });
    const mark = contractText.querySelector(".hit-evidence")!;
    vi.spyOn(mark, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 300, 40, 20));
    const switcher = screen.getByRole("group", { name: "切换对照材料", hidden: true });
    const { getByRole } = within(switcher);
    await user.click(getByRole("button", { name: "拟签合同", hidden: true }));
    expect(contractText.scrollTop).toBe(250);
    contractText.scrollTop = 240;
    await user.click(getByRole("button", { name: "采购文件", hidden: true }));
    await user.click(getByRole("button", { name: "拟签合同", hidden: true }));
    expect(contractText.scrollTop).toBe(240);
  });

  it("shows distinct verified finding markers and selects the requested finding", async () => {
    const user = userEvent.setup();
    const document = documentOf("c", "draft_contract", "合同.txt", "质保二十四个月。付款三十日。", {
      parsed_document_id: "pc",
    });
    const first = findingWith([
      { ...locatedAnchor("c", "二十四", 2, 5), parsed_document_id: "pc" },
    ]);
    const second = {
      ...findingWith([{ ...locatedAnchor("c", "三十日", 10, 13), parsed_document_id: "pc" }]),
      id: "finding-2",
    };
    const { onSelectFinding } = renderStage({
      immersive: true,
      documents: [document],
      findings: [first, second],
      quoteScope: {
        caseId: "case-1",
        runId: "run-1",
        attemptId: "attempt-1",
        isFrozenAttempt: true,
        parsedDocumentIds: ["pc"],
        sourceProofs: [
          { documentId: "c", parsedDocumentId: "pc", originalDigest: document.digest },
        ],
      },
    });
    expect(screen.getByRole("button", { name: "查看线索 1：二十四" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "查看线索 2：三十日" }));
    expect(onSelectFinding).toHaveBeenCalledWith("finding-2");
    expect(screen.getByRole("checkbox", { name: "按条款联动" })).toBeChecked();
    await user.click(screen.getByRole("checkbox", { name: "按条款联动" }));
    expect(screen.getByRole("checkbox", { name: "按条款联动" })).not.toBeChecked();
  });

  it("announces a topic count from verified finding anchors", () => {
    const text = "设备的免费质保期不少于36个月，自验收合格之日起计算。";
    const doc = documentOf("c", "draft_contract", "合同.txt", text, {
      parsed_document_id: "pc",
      clauses: [],
    });
    const start = Array.from(text).indexOf("3");
    const finding = findingWith([
      { ...locatedAnchor("c", "36个月", start, start + 4), parsed_document_id: "pc" },
    ]);
    renderStage({
      immersive: true,
      documents: [doc],
      findings: [finding],
      quoteScope: {
        caseId: "case-1",
        runId: "run-1",
        attemptId: "attempt-1",
        isFrozenAttempt: true,
        parsedDocumentIds: ["pc"],
        sourceProofs: [{ documentId: "c", parsedDocumentId: "pc", originalDigest: doc.digest }],
      },
    });
    expect(screen.getByRole("button", { name: "质保，1 条线索" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    fireEvent.click(screen.getByRole("button", { name: "质保，1 条线索" }));
    expect(screen.getByRole("button", { name: "质保，1 条线索" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("locates a topic in the sole contract when linking is off", async () => {
    const user = userEvent.setup();
    const document = documentOf(
      "c",
      "draft_contract",
      "合同.txt",
      "设备的免费质保期不少于36个月，自验收合格之日起计算。",
      {
        parsed_document_id: "pc",
        clauses: [],
      },
    );
    const { container } = renderStage({
      immersive: true,
      documents: [document],
      quoteScope: {
        caseId: "case-1",
        runId: "run-1",
        attemptId: "attempt-1",
        isFrozenAttempt: true,
        parsedDocumentIds: ["pc"],
        sourceProofs: [
          { documentId: "c", parsedDocumentId: "pc", originalDigest: document.digest },
        ],
      },
    });
    await user.click(screen.getByRole("checkbox", { name: "按条款联动" }));
    await user.click(screen.getByRole("button", { name: "质保，0 条线索" }));
    expect(container.querySelector('[data-source-id="c"] .hit-find')).toHaveTextContent("质保期");
    expect(screen.queryByRole("group", { name: "切换对照材料", hidden: true })).toBeNull();
  });

  it("opens the exact requested source when a deep link changes between same-role versions", async () => {
    const first = documentOf("p1", "procurement", "采购一.txt", "原文一");
    const second = documentOf("p2", "procurement", "采购二.txt", "原文二");
    const props = {
      immersive: true,
      documents: [first, second],
      finding: null,
      findings: [],
      selectedFindingId: null,
      layout: "switch" as const,
      onSelectFinding: vi.fn(),
    };
    const view = render(
      <MemoryRouter>
        <DocumentStage {...props} sourceDocumentId="p1" />
      </MemoryRouter>,
    );
    expect(view.container.querySelector(".immersive-document-column")).toHaveAttribute(
      "data-source-id",
      "p1",
    );
    view.rerender(
      <MemoryRouter>
        <DocumentStage {...props} sourceDocumentId="p2" />
      </MemoryRouter>,
    );
    await waitFor(() =>
      expect(view.container.querySelector(".immersive-document-column")).toHaveAttribute(
        "data-source-id",
        "p2",
      ),
    );
  });

  it("requires a choice when several documents share a role", async () => {
    const user = userEvent.setup();
    const first = documentOf("p1", "procurement", "采购一.txt", "原文一");
    const second = documentOf("p2", "procurement", "采购二.txt", "原文二");
    const { container } = renderStage({ immersive: true, documents: [first, second] });
    expect(container.querySelectorAll(".immersive-document-column")).toHaveLength(0);
    await user.selectOptions(screen.getByLabelText("采购文件"), "p2");
    expect(
      container.querySelector(".immersive-document-column")?.getAttribute("data-source-id"),
    ).toBe("p2");
  });
});
