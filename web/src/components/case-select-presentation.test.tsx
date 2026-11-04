import { Select } from "antd";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { CaseSummary } from "../api/types";
import { caseOptionLabel } from "../domain/case-identity";
import { renderWithAntd as render } from "../test/antd";
import { caseSelectPresentation } from "./case-select-presentation";

const base: CaseSummary = { id: "case-complete-alpha", title: "同名案件", contract_type: "service",
  document_count: 0, pending_decisions: 0, latest_run: null, updated_at: "2026-11-05T01:02:03Z",
  business_line: "ENT", scenario: "telecom_dedicated_line" };

describe("case selector identity", () => {
  it("wraps full secondary identity and selects the exact case for same-name legacy records", async () => {
    const onChange = vi.fn();
    const second = { ...base, id: "case-complete-beta", contract_type: "framework" };
    render(<Select {...caseSelectPresentation} aria-label="关联案件" options={[base, second].map((item) => ({ value: item.id, label: caseOptionLabel(item) }))} onChange={onChange} />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("combobox", { name: "关联案件" }));
    const option = screen.getByRole("option", { name: /case-complete-beta/ });
    expect(option).toHaveTextContent("业务编号未登记");
    expect(option).toHaveTextContent("框架协议");
    expect(option.querySelector('[style*="overflow-wrap"]')).toHaveStyle({ whiteSpace: "normal", overflowWrap: "anywhere" });
    await user.click(option);
    expect(onChange).toHaveBeenCalledWith("case-complete-beta", expect.anything());
  });

  it("allows a Chinese scene or full machine identity to filter a source selector", async () => {
    render(<Select {...caseSelectPresentation} showSearch={{ optionFilterProp: "label" }} aria-label="来源案件" options={[{ value: base.id, label: caseOptionLabel(base) }]} />);
    const user = userEvent.setup();
    const control = screen.getByRole("combobox", { name: "来源案件" });
    await user.type(control, "政企专线");
    expect(screen.getByRole("option", { name: /case-complete-alpha/ })).toBeInTheDocument();
    await user.clear(control);
    await user.type(control, "case-complete-alpha");
    expect(screen.getByRole("option", { name: /同名案件/ })).toBeInTheDocument();
  });
});
