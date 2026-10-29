import { Link, useLocation } from "react-router";
import { Empty, Table } from "antd";
import type { ColumnsType, TableProps } from "antd/es/table";
import { useMemo } from "react";
import type { CaseSummary } from "../api/types";
import type { CaseSortField, SortDirection } from "../domain/review";
import {
  businessStatusLabel,
  contractTypeLabel,
  formatDateTime,
  runLabel,
  runTone,
} from "../domain/review";
import { StatusText } from "./status-text";

const SORT_FIELDS: readonly CaseSortField[] = [
  "title",
  "document_count",
  "pending_decisions",
  "updated_at",
];

function sortOrderFor(
  field: CaseSortField,
  sort: CaseSortField,
  direction: SortDirection,
): "ascend" | "descend" | null {
  if (field !== sort) return null;
  return direction === "asc" ? "ascend" : "descend";
}

function sortFieldOf(
  sorter: Parameters<NonNullable<TableProps<CaseSummary>["onChange"]>>[2],
): CaseSortField | null {
  const selected = Array.isArray(sorter) ? sorter[0] : sorter;
  const field = selected?.columnKey;
  if (typeof field !== "string") return null;
  return SORT_FIELDS.includes(field as CaseSortField)
    ? (field as CaseSortField)
    : null;
}

function CaseTitleCell({
  item,
  location,
}: {
  item: CaseSummary;
  location: ReturnType<typeof useLocation>;
}) {
  const params = new URLSearchParams(location.search);
  params.set("selected", item.id);
  return (
    <>
      <Link
        to={`/cases/${encodeURIComponent(item.id)}`}
        state={{
          returnTo: `${location.pathname}?${params.toString()}`,
          scrollY: typeof window === "undefined" ? 0 : window.scrollY,
          selectedId: item.id,
        }}
      >
        {item.title}
      </Link>
      <div className="meta">
        {item.id}，{contractTypeLabel(item.contract_type)}
      </div>
    </>
  );
}

function businessSummary(item: CaseSummary): string {
  return (
    [item.business_line, item.scenario].filter(Boolean).join("，") || "未提供"
  );
}

function CaseStatusCell({ item }: { item: CaseSummary }) {
  return item.archived_at ? (
    <StatusText tone="neutral">已归档</StatusText>
  ) : (
    businessStatusLabel(item.business_status ?? "")
  );
}

function LatestRunCell({ item }: { item: CaseSummary }) {
  return item.latest_run ? (
    <StatusText tone={runTone(item.latest_run.state)}>
      {runLabel(item.latest_run.state)}
    </StatusText>
  ) : (
    "尚未审查"
  );
}

function caseColumns(
  location: ReturnType<typeof useLocation>,
  sort: CaseSortField,
  direction: SortDirection,
): ColumnsType<CaseSummary> {
  return [
    {
      title: "案件",
      key: "title",
      sorter: true,
      sortOrder: sortOrderFor("title", sort, direction),
      render: (_, item) => <CaseTitleCell item={item} location={location} />,
    },
    {
      title: "材料",
      dataIndex: "document_count",
      key: "document_count",
      sorter: true,
      sortOrder: sortOrderFor("document_count", sort, direction),
    },
    {
      title: "待决定",
      dataIndex: "pending_decisions",
      key: "pending_decisions",
      sorter: true,
      sortOrder: sortOrderFor("pending_decisions", sort, direction),
    },
    {
      title: "更新时间",
      dataIndex: "updated_at",
      key: "updated_at",
      sorter: true,
      sortOrder: sortOrderFor("updated_at", sort, direction),
      render: (value: string) => formatDateTime(value),
    },
    {
      title: "业务线 / 场景",
      key: "business",
      render: (_, item) => businessSummary(item),
    },
    {
      title: "业务状态",
      key: "status",
      render: (_, item) => <CaseStatusCell item={item} />,
    },
    {
      title: "最新运行",
      key: "run",
      render: (_, item) => <LatestRunCell item={item} />,
    },
  ];
}

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
  const columns = useMemo(
    () => caseColumns(location, sort, direction),
    [location, sort, direction],
  );
  if (items.length === 0) return <Empty description="没有符合条件的案件。" />;
  const onChange: NonNullable<TableProps<CaseSummary>["onChange"]> = (
    _pagination,
    _filters,
    sorter,
  ) => {
    const field = sortFieldOf(sorter);
    if (field) onSort(field);
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
