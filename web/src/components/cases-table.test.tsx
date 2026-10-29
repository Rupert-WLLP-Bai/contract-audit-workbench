import { fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithAntd as render } from "../test/antd";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import type { CaseSummary } from "../api/types";
import { CasesTable, sortFieldOf } from "./cases-table";

const item: CaseSummary = {
  id: "c1",
  title: "基站质保",
  contract_type: "network_engineering",
  document_count: 3,
  pending_decisions: 2,
  latest_run: {
    id: "r1",
    state: "awaiting_human",
    updated_at: "2026-10-21T01:00:00Z",
  },
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
    expect(screen.getByRole("link", { name: "基站质保" })).toHaveAttribute(
      "href",
      "/cases/c1",
    );
    expect(screen.getByRole("cell", { name: "待人工" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "2" })).toBeInTheDocument();
  });

  it("says when a case has not been reviewed instead of calling it clean", () => {
    render(
      <MemoryRouter>
        <CasesTable
          items={[{ ...item, latest_run: null, pending_decisions: 0 }]}
          {...tableProps}
        />
      </MemoryRouter>,
    );
    expect(screen.getByText("尚未审查")).toBeInTheDocument();
  });

  it("exposes sorting as a keyboard-operable table action", async () => {
    const onSort = vi.fn();
    render(
      <MemoryRouter>
        <CasesTable items={[item]} {...tableProps} onSort={onSort} />
      </MemoryRouter>,
    );
    const header = screen.getByRole("columnheader", { name: /案件/ });
    expect(header).toHaveAttribute("tabindex", "0");
    header.focus();
    fireEvent.keyDown(header, {
      key: "Enter",
      code: "Enter",
      keyCode: 13,
      which: 13,
    });
    expect(onSort).toHaveBeenCalledWith("title");
  });

  it("carries list filters, selected case and scroll position into the detail return state", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={["/cases?q=network&run=needs_human"]}>
        <Routes>
          <Route
            path="/cases"
            element={<CasesTable items={[item]} {...tableProps} />}
          />
          <Route path="/cases/:caseId" element={<LocationState />} />
        </Routes>
      </MemoryRouter>,
    );
    await user.click(screen.getByRole("link", { name: "基站质保" }));
    expect(await screen.findByText(/"selectedId":"c1"/)).toHaveTextContent(
      '"returnTo":"/cases?q=network&run=needs_human&selected=c1"',
    );
  });

  it("shows an explicit empty state instead of an empty table", () => {
    render(
      <MemoryRouter>
        <CasesTable items={[]} {...tableProps} />
      </MemoryRouter>,
    );
    expect(screen.getByText("没有符合条件的案件。")).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("describes business line, scenario, status and archive state", () => {
    render(
      <MemoryRouter>
        <CasesTable
          items={[
            {
              ...item,
              business_line: "企业业务",
              scenario: "专线",
              business_status: "active",
            },
            {
              ...item,
              id: "c2",
              title: "归档合同",
              business_line: "",
              scenario: undefined,
              business_status: "closed",
              archived_at: "2026-10-22T00:00:00Z",
            },
          ]}
          {...tableProps}
        />
      </MemoryRouter>,
    );
    expect(screen.getByRole("cell", { name: "企业业务，专线" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "进行中" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "未提供" })).toBeInTheDocument();
    expect(screen.getAllByText("已归档").length).toBeGreaterThan(0);
  });

  it("marks the selected row so the list and detail stay aligned", () => {
    const { container } = render(
      <MemoryRouter>
        <CasesTable items={[item]} {...tableProps} selectedId="c1" />
      </MemoryRouter>,
    );
    expect(container.querySelector(".cases-table-row-selected")).not.toBeNull();
  });

  it.each([
    ["材料", "document_count"],
    ["待决定", "pending_decisions"],
    ["更新时间", "updated_at"],
    ["案件", "title"],
  ])("reports sorting for the %s column", async (column, field) => {
    const onSort = vi.fn();
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <CasesTable items={[item]} {...tableProps} onSort={onSort} />
      </MemoryRouter>,
    );
    await user.click(screen.getByRole("columnheader", { name: new RegExp(column) }));
    expect(onSort).toHaveBeenCalledWith(field);
  });

  it("exposes the active sort direction and ignores unsortable columns", async () => {
    const onSort = vi.fn();
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <CasesTable
          items={[item]}
          {...tableProps}
          sort="title"
          direction="asc"
          onSort={onSort}
        />
      </MemoryRouter>,
    );
    expect(
      screen.getByRole("columnheader", { name: /案件/ }),
    ).toHaveAttribute("aria-sort", "ascending");
    await user.click(
      screen.getByRole("columnheader", { name: "业务线 / 场景" }),
    );
    expect(onSort).not.toHaveBeenCalled();
  });

  it("shows a descending marker for the active column", () => {
    render(
      <MemoryRouter>
        <CasesTable
          items={[item]}
          {...tableProps}
          sort="document_count"
          direction="desc"
        />
      </MemoryRouter>,
    );
    expect(
      screen.getByRole("columnheader", { name: /材料/ }),
    ).toHaveAttribute("aria-sort", "descending");
  });
});

describe("cases table sorting helper and scroll", () => {
  it("reads a single sorter, the first of an array, and rejects missing or unknown columns", () => {
    expect(sortFieldOf(undefined)).toBeNull();
    expect(sortFieldOf({ columnKey: "title" })).toBe("title");
    expect(
      sortFieldOf([
        { columnKey: "document_count" },
        { columnKey: "updated_at" },
      ]),
    ).toBe("document_count");
    expect(sortFieldOf({ columnKey: "business" })).toBeNull();
    expect(sortFieldOf({ columnKey: undefined })).toBeNull();
  });

  it("carries the real scroll position into the detail state", async () => {
    const user = userEvent.setup();
    Object.defineProperty(window, "scrollY", { value: 321, configurable: true });
    render(
      <MemoryRouter initialEntries={["/cases"]}>
        <Routes>
          <Route path="/cases" element={<CasesTable items={[item]} {...tableProps} />} />
          <Route path="/cases/:caseId" element={<LocationState />} />
        </Routes>
      </MemoryRouter>,
    );
    await user.click(screen.getByRole("link", { name: "基站质保" }));
    expect(await screen.findByText(/"scrollY":321/)).toBeInTheDocument();
    Object.defineProperty(window, "scrollY", { value: 0, configurable: true });
  });

  it("paginates large result sets instead of hiding rows", () => {
    const items = Array.from({ length: 12 }, (_, index) => ({
      ...item,
      id: `case-${index}`,
      title: `案件 ${index}`,
    }));
    render(
      <MemoryRouter>
        <CasesTable items={items} {...tableProps} />
      </MemoryRouter>,
    );
    expect(screen.getByText("案件 0")).toBeInTheDocument();
    expect(screen.getByText("案件 11")).toBeInTheDocument();
  });

  it("labels a missing business status without inventing a value", () => {
    render(
      <MemoryRouter>
        <CasesTable
          items={[{ ...item, business_status: undefined }]}
          {...tableProps}
        />
      </MemoryRouter>,
    );
    expect(screen.getByRole("cell", { name: "业务状态未提供" })).toBeInTheDocument();
  });
});
