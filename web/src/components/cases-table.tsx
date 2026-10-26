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
import { RiskTag } from "./ui";
import { StatusText } from "./status-text";

export type CaseTableRow = CaseSummary & {
  /** Present only when the workbench snapshot reported this case. */
  riskKnown?: boolean;
  severity?: string | null;
  ownerName?: string | null;
};

function shortDateTime(value: string): string {
  if (value === "") return "时间未记录";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const sameYear = date.getFullYear() === new Date().getFullYear();
  return new Intl.DateTimeFormat(
    "zh-CN",
    sameYear
      ? {
          month: "2-digit",
          day: "2-digit",
          hour: "2-digit",
          minute: "2-digit",
          hourCycle: "h23",
        }
      : { year: "numeric", month: "2-digit", day: "2-digit" },
  ).format(date);
}

export function CasesTable({
  items,
  sort,
  direction,
  selectedId,
  onSort,
  workbenchLoading = false,
}: {
  items: CaseTableRow[];
  sort: CaseSortField;
  direction: SortDirection;
  selectedId: string | null;
  onSort: (field: CaseSortField) => void;
  workbenchLoading?: boolean;
}) {
  const location = useLocation();
  if (items.length === 0) return <Empty description="没有符合条件的案件。" />;
  const loadingText = workbenchLoading ? "加载中…" : "未报告";
  const columns: ColumnsType<CaseTableRow> = [
    {
      title: "案件",
      key: "title",
      width: 200,
      sorter: true,
      sortOrder:
        sort === "title" ? (direction === "asc" ? "ascend" : "descend") : null,
      render: (_, item) => (
        <div className="case-title-cell">
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
            title={item.title}
          >
            {item.title}
          </Link>
          <div className="meta" title={`${item.id} · ${contractTypeLabel(item.contract_type)}`}>
            {item.id} · {contractTypeLabel(item.contract_type)}
          </div>
        </div>
      ),
    },
    {
      title: "风险",
      key: "severity",
      width: 84,
      render: (_, item) =>
        item.riskKnown ? (
          <RiskTag severity={item.severity} />
        ) : (
          <span className="meta">{loadingText}</span>
        ),
    },
    {
      title: "审查状态",
      key: "run",
      width: 94,
      render: (_, item) =>
        item.latest_run ? (
          <StatusText tone={runTone(item.latest_run.state)}>
            {runLabel(item.latest_run.state)}
          </StatusText>
        ) : (
          <span className="meta">尚未审查</span>
        ),
    },
    {
      title: "负责人",
      key: "owner",
      width: 64,
      render: (_, item) => item.ownerName ?? <span className="meta">{loadingText}</span>,
    },
    {
      title: "材料",
      dataIndex: "document_count",
      key: "document_count",
      width: 64,
      align: "center",
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
      width: 78,
      align: "center",
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
      width: 100,
      sorter: true,
      sortOrder:
        sort === "updated_at"
          ? direction === "asc"
            ? "ascend"
            : "descend"
          : null,
      render: (value: string) => (
        <time
          className="cases-updated"
          dateTime={value}
          title={formatDateTime(value)}
        >
          {shortDateTime(value)}
        </time>
      ),
    },
    {
      title: "操作",
      key: "actions",
      width: 52,
      render: (_, item) => (
        <Link
          className="cases-row-action"
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
          查看
        </Link>
      ),
    },
  ];
  const onChange: NonNullable<TableProps<CaseTableRow>["onChange"]> = (
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
      size="middle"
      tableLayout="fixed"
      dataSource={items}
      columns={columns}
      pagination={false}
      scroll={{ x: 736 }}
      onChange={onChange}
      rowClassName={(item) =>
        item.id === selectedId ? "cases-table-row-selected" : ""
      }
    />
  );
}
