// File metadata (manifest mirror):
// Purpose: Checks frozen-text navigation when page geometry is unavailable and resets reader selection on document scope changes.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithAntd as render } from "../test/antd";
import { OriginalDocumentReader } from "./original-document-reader";

vi.mock("./original-page-view", () => ({
  default: () => <div data-testid="mock-original-page-view" />,
}));

describe("original document reader", () => {
  it("opens frozen text when the citation has no recorded page", () => {
    render(
      <OriginalDocumentReader
        scope={{ caseId: "case", contextKey: "actor", digests: new Map() }}
        documentId="doc"
        filename="contract.pdf"
        digest="digest"
        text="合同条款"
        quotes={[{ id: "ev", quote: "合同条款", start: 0, end: 4, page: null }]}
      >
        <div data-active-source="true">
          <mark className="hit-evidence">合同条款</mark>
        </div>
      </OriginalDocumentReader>,
    );
    expect(screen.getByText(/引用未记录原件页码/)).toBeInTheDocument();
    expect(screen.getByText("合同条款")).toBeVisible();
    expect(screen.getByRole("button", { name: "文字原文" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByRole("button", { name: "原件标注" })).toBeEnabled();
  });

  it("keeps an unpaged selected key clause on frozen text until the selection is cleared", async () => {
    const user = userEvent.setup();
    const onClearKeyClause = vi.fn();
    const props = {
      scope: { caseId: "case", contextKey: "actor", digests: new Map<string, string>() },
      documentId: "doc",
      filename: "contract.pdf",
      digest: "digest",
      text: "第一条 付款条款。",
      quotes: [],
      keyClause: {
        id: "key-1",
        heading: "付款条款",
        sourceLabel: "正文片段/标题未识别",
        quote: "第一条 付款条款。",
        page: null,
      },
      onClearKeyClause,
    };
    const view = render(
      <OriginalDocumentReader {...props}>
        <div data-active-source="true">
          <mark className="hit-key-clause" data-selected-key-clause="true">
            第一条 付款条款。
          </mark>
        </div>
      </OriginalDocumentReader>,
    );

    expect(screen.getByText(/原件页码未记录，只定位冻结文字/)).toBeInTheDocument();
    expect(view.container.querySelector("mark.hit-key-clause")).toBeVisible();
    expect(screen.queryByTestId("mock-original-page-view")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "原件标注" }));
    expect(onClearKeyClause).toHaveBeenCalledOnce();
    view.rerender(
      <OriginalDocumentReader {...props} keyClause={null}>
        <div data-active-source="true">
          <mark className="hit-key-clause">第一条 付款条款。</mark>
        </div>
      </OriginalDocumentReader>,
    );
    expect(await screen.findByTestId("mock-original-page-view")).toBeInTheDocument();
  });
});
