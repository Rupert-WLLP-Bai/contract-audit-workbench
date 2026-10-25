import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import type { CaseSummary } from "../api/types";
import { CasesTable } from "./cases-table";

const item: CaseSummary = {
  id: "c1",
  title: "基站质保",
  contract_type: "network_engineering",
  document_count: 3,
  pending_decisions: 2,
  latest_run: { id: "r1", state: "awaiting_human", updated_at: "2026-10-21T01:00:00Z" },
  updated_at: "2026-10-21T01:00:00Z",
};

const tableProps = {
  sort: "updated_at" as const,
  direction: "desc" as const,
  selectedId: null,
  onSort: vi.fn(),
};

function LocationState() {
  const location = useLocation();
  return <output>{JSON.stringify(location.state)}</output>;
}

describe("cases table", () => {
  it("opens a case from the row title and shows the run in words", () => {
    render(
      <MemoryRouter initialEntries={["/cases?q=network&run=needs_human"]}>
        <CasesTable items={[item]} {...tableProps} />
      </MemoryRouter>,
    );
    expect(screen.getByRole("link", { name: "基站质保" })).toHaveAttribute("href", "/cases/c1");
    expect(screen.getByRole("cell", { name: "待人工" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "2" })).toBeInTheDocument();
  });

  it("says when a case has not been reviewed instead of calling it clean", () => {
    render(
      <MemoryRouter>
        <CasesTable items={[{ ...item, latest_run: null, pending_decisions: 0 }]} {...tableProps} />
      </MemoryRouter>,
    );
    expect(screen.getByText("尚未审查")).toBeInTheDocument();
  });

  it("exposes sorting as a keyboard-operable table action", async () => {
    const user = userEvent.setup();
    const onSort = vi.fn();
    render(<MemoryRouter><CasesTable items={[item]} {...tableProps} onSort={onSort} /></MemoryRouter>);
    const sortButton = screen.getByRole("button", { name: /按案件排序/ });
    sortButton.focus();
    await user.keyboard("{Enter}");
    expect(onSort).toHaveBeenCalledWith("title");
  });

  it("carries list filters, selected case and scroll position into the detail return state", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={["/cases?q=network&run=needs_human"]}>
        <Routes>
          <Route path="/cases" element={<CasesTable items={[item]} {...tableProps} />} />
          <Route path="/cases/:caseId" element={<LocationState />} />
        </Routes>
      </MemoryRouter>,
    );
    await user.click(screen.getByRole("link", { name: "基站质保" }));
    expect(await screen.findByText(/"selectedId":"c1"/)).toHaveTextContent('"returnTo":"/cases?q=network&run=needs_human&selected=c1"');
  });
});
