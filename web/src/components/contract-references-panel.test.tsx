import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { emptyDraft } from "../api/contracts";
import { renderWithAntd as render } from "../test/antd";
import { ContractReferencesPanel } from "./contract-references-panel";

const mock = vi.hoisted(() => ({ cases: { isError: false, isSuccess: true, isLoading: false, data: [] as unknown[] }, check: vi.fn() }));
vi.mock("../api/queries", () => ({ useCases: () => mock.cases }));
vi.mock("../api/contracts", async (importOriginal) => ({
  ...await importOriginal<typeof import("../api/contracts")>(),
  checkContractReferences: mock.check,
}));

const record = { id: "case-full-1", title: "同名来源", contract_type: "service", document_count: 0,
  pending_decisions: 0, latest_run: null, updated_at: "2026-11-05T01:02:03Z", case_number: "BUS-1" };

beforeEach(() => {
  mock.cases = { isError: false, isSuccess: true, isLoading: false, data: [record] };
  mock.check.mockReset();
});

it("hides a selected source and its previous result when list access is revoked", async () => {
  mock.check.mockResolvedValue({ source_case_id: record.id, source: "draft", checked_at: "2026-11-05", subjects: [], materials: [], warnings: [] });
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="contract-1" draft={draft} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("combobox"));
  await user.click(screen.getByRole("option", { name: /BUS-1.*case-full-1/ }));
  await user.click(screen.getByRole("button", { name: "核对引用" }));
  expect(await screen.findByText(/来源案件 case-full-1/)).toBeVisible();
  mock.cases = { isError: false, isSuccess: true, isLoading: false, data: [] };
  view.rerender(<ContractReferencesPanel contractID="contract-1" draft={draft} />);
  await waitFor(() => expect(screen.queryByText(/来源案件 case-full-1/)).not.toBeInTheDocument());
  await user.click(screen.getByRole("combobox"));
  expect(screen.queryByRole("option", { name: /BUS-1/ })).not.toBeInTheDocument();
});
