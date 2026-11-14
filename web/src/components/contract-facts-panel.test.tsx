import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import type { ContractFactsCitation, ContractFactsProjection } from "../api/contract-facts";
import { renderWithAntd as render } from "../test/antd";
import { ContractFactsPanel } from "./contract-facts-panel";

it("keeps uncertainty visible while collapsed and allows keyboard access to exact frozen citations", async () => {
  const user = userEvent.setup();
  const citation: ContractFactsCitation = {
    document_id: "doc-1",
    parsed_document_id: "parse-1",
    original_digest: "a".repeat(64),
    normalized_digest: "b".repeat(64),
    quote: "付款期限为三十日",
    start: 0,
    end: 8,
  };
  const facts: ContractFactsProjection = {
    schema_version: "contract-facts/v1",
    attempt_id: "attempt-1",
    status: "partial",
    structured_output_status: "valid",
    source: citation,
    input_codepoints: 8,
    input_truncated: false,
    fields: [
      { field: "payment_schedule", status: "known", value: "三十日内付款", citations: [citation] },
      { field: "business_dates", status: "unknown", value: "", citations: [] },
    ],
  };
  const locate = vi.fn();
  render(<ContractFactsPanel facts={facts} onLocate={locate} />);
  const toggle = screen.getByRole("button", { name: /合同事实建议/ });
  expect(toggle).toHaveAttribute("aria-expanded", "false");
  expect(toggle).toHaveTextContent("1 项未知或待核对");
  expect(toggle).toHaveTextContent("需人工核对");
  expect(screen.getByText("未完整覆盖")).toBeVisible();
  expect(screen.queryByText(citation.quote)).not.toBeInTheDocument();

  toggle.focus();
  await user.keyboard("{Enter}");
  expect(toggle).toHaveAttribute("aria-expanded", "true");
  expect(screen.getByText(citation.quote)).toBeVisible();
  await user.click(screen.getByRole("button", { name: "定位冻结原文" }));
  expect(locate).toHaveBeenCalledWith(citation, "payment_schedule-0");
});
