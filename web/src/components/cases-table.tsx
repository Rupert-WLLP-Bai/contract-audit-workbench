import { Link, useLocation } from "react-router";
import { Empty, Table } from "antd";
import type { ColumnsType, TableProps } from "antd/es/table";
import type { CaseSummary } from "../api/types";
import type { CaseSortField, SortDirection } from "../domain/review";
import {
  contractTypeLabel,
  formatDateTime,
  runLabel,
  runTone,
} from "../domain/review";
import { StatusText } from "./status-text";

export function CasesTable({
  items,
  sort,
  direction,
  selectedId,
  onSort,
}: {
  items: CaseSummary[];
  sort: CaseSortField;
  direction: SortDirection;
  selectedId: string | null;
  onSort: (field: CaseSortField) => void;
}) {
  const location = useLocation();
  if (items.length === 0) return <Empty description="没有符合条件的案件。" />;
  const columns: ColumnsType<CaseSummary> = [
    {
      title: "案件",
      key: "title",
      sorter: true,
      sortOrder:
        sort === "title" ? (direction === "asc" ? "ascend" : "descend") : null,
      render: (_, item) => (
        <>
          <Link
            to={`/cases/${encodeURIComponent(item.id)}`}
            state={{
              returnTo: (() => {
                const p = new URLSearchParams(location.search);
                p.set("selected", item.id);
                return `${location.pathname}?${p.toString()}`;
              })(),
              scrollY: typeof window === "undefined" ? 0 : window.scrollY,
              selectedId: item.id,
            }}
          >
            {item.title}
          </Link>
          <div className="meta">
            {item.id} · {contractTypeLabel(item.contract_type)}
          </div>
        </>
      ),
    },
    {
      title: "材料",
      dataIndex: "document_count",
      key: "document_count",
      sorter: true,
      sortOrder:
        sort === "document_count"
          ? direction === "asc"
            ? "ascend"
            : "descend"
          : null,
    },
    {
      title: "待决定",
      dataIndex: "pending_decisions",
      key: "pending_decisions",
      sorter: true,
      sortOrder:
        sort === "pending_decisions"
          ? direction === "asc"
            ? "ascend"
            : "descend"
          : null,
    },
    {
      title: "更新时间",
      dataIndex: "updated_at",
      key: "updated_at",
      sorter: true,
      sortOrder:
        sort === "updated_at"
          ? direction === "asc"
            ? "ascend"
            : "descend"
          : null,
      render: (value: string) => formatDateTime(value),
    },
    {
      title: "业务线 / 场景",
      key: "business",
      render: (_, item) =>
        [item.business_line, item.scenario].filter(Boolean).join(" · ") ||
        "未提供",
    },
    {
      title: "业务状态",
      key: "status",
      render: (_, item) =>
        item.archived_at ? (
          <StatusText tone="neutral">已归档</StatusText>
        ) : (
          item.business_status || "未提供"
        ),
    },
    {
      title: "最新运行",
      key: "run",
      render: (_, item) =>
        item.latest_run ? (
          <StatusText tone={runTone(item.latest_run.state)}>
            {runLabel(item.latest_run.state)}
          </StatusText>
        ) : (
          "尚未审查"
        ),
    },
  ];
  const onChange: NonNullable<TableProps<CaseSummary>["onChange"]> = (
    _pagination,
    _filters,
    sorter,
  ) => {
    const selected = Array.isArray(sorter) ? sorter[0] : sorter;
    const field = selected?.columnKey;
    if (
      field === "title" ||
      field === "document_count" ||
      field === "pending_decisions" ||
      field === "updated_at"
    )
      onSort(field);
  };
  return (
    <Table
      sortDirections={["ascend", "descend", "ascend"]}
      rowKey="id"
      dataSource={items}
      columns={columns}
      pagination={false}
      onChange={onChange}
      rowClassName={(item) =>
        item.id === selectedId ? "cases-table-row-selected" : ""
      }
    />
  );
}
