import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderWithAntd as render } from "../test/antd";
import { OriginalDocumentReader } from "./original-document-reader";

describe("original document reader", () => {
  it("opens frozen text when the citation has no recorded page", () => {
    render(<OriginalDocumentReader scope={{ caseId: "case", contextKey: "actor", digests: new Map() }} documentId="doc" filename="contract.pdf" digest="digest" text="合同条款" quotes={[{ id: "ev", quote: "合同条款", start: 0, end: 4, page: null }]}>
      <div data-active-source="true"><mark className="hit-evidence">合同条款</mark></div>
    </OriginalDocumentReader>);
    expect(screen.getByText(/引用未记录原件页码/)).toBeInTheDocument();
    expect(screen.getByText("合同条款")).toBeVisible();
    expect(screen.getByRole("button", { name: "文字原文" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "原件标注" })).toBeEnabled();
  });
});
