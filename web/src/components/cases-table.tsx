// File metadata (manifest mirror):
// Purpose: Renders the cases workbench table with stable identity links, responsive summary details and locally scrollable columns; server-derived state does not establish business authorization or evidence validity.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-08
// Revision: r5
// Copyright: See LICENSE
// End file metadata.

import { Link, useLocation, useNavigate } from "react-router";
import { Empty, Table } from "antd";
import type { ColumnsType, TableProps } from "antd/es/table";
import { useMemo } from "react";
import type { CaseSummary } from "../api/types";
import type { CaseSortField, SortDirection } from "../domain/review";
import {
  businessStatusLabel,
  formatDateTime,
  runLabel,
  runTone,
} from "../domain/review";
import { StatusText } from "./status-text";
import { TechnicalDetail } from "./technical-detail";
import { InvalidTimeDetail } from "./invalid-time-detail";
import { businessScopeLabel } from "../domain/business-labels";
import { caseIdentityDetails } from "../domain/case-identity";

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

export function sortFieldOf(
  sorter: Parameters<NonNullable<TableProps<CaseSummary>["onChange"]>>[2],
): CaseSortField | null {
  const selected = Array.isArray(sorter) ? sorter[0] : sorter;
  if (!selected) return null;
  const field = selected.columnKey;
  if (typeof field !== "string") return null;
  return SORT_FIELDS.includes(field as CaseSortField)
    ? (field as CaseSortField)
    : null;
}

function CaseTitleCell({
  item,
  location,
  actorScope,
}: {
  item: CaseSummary;
  location: ReturnType<typeof useLocation>;
  actorScope: string;
}) {
  const navigate = useNavigate();
  const params = new URLSearchParams(location.search);
  params.set("selected", item.id);
  const href = `/cases/${encodeURIComponent(item.id)}`;
  const returnTo = `${location.pathname}?${params.toString()}`;
  return (
    <>
      <Link
        to={href}
        state={{
          returnTo,
          scrollY: window.scrollY,
          selectedId: item.id,
          actorScope,
        }}
        onClick={(event) => {
          if (event.button !== 0 || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
          event.preventDefault();
          void navigate(href, { state: { returnTo, scrollY: window.scrollY, selectedId: item.id, actorScope } });
        }}
      >
        {item.title}
      </Link>
      <div className="meta caw-wrap">
        {caseIdentityDetails(item)}
        <InvalidTimeDetail value={item.updated_at} label="案件更新时间" />
        <TechnicalDetail value={item.id} label="案件标识" />
      </div>
      <div className="cases-mobile-facts">
        <span>材料 {item.document_count}</span>
        <span>待决定 {item.pending_decisions}</span>
        <span>
          业务状态：{item.archived_at ? "已归档" : businessStatusLabel(item.business_status ?? "")}
        </span>
        <span>
          最新运行：{item.latest_run ? runLabel(item.latest_run.state) : "尚未审查"}
        </span>
      </div>
    </>
  );
}

function businessSummary(item: CaseSummary): string {
  return businessScopeLabel(item.business_line, item.scenario);
}

function CaseStatusCell({ item }: { item: CaseSummary }) {
  return item.archived_at ? (
    <StatusText tone="neutral">已归档</StatusText>
  ) : (
    businessStatusLabel(
      item.business_status === undefined ? "" : item.business_status,
    )
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
  actorScope: string,
): ColumnsType<CaseSummary> {
  return [
    {
      title: "案件",
      key: "title",
      width: 340,
      sorter: true,
      sortOrder: sortOrderFor("title", sort, direction),
      render: (_, item) => <CaseTitleCell item={item} location={location} actorScope={actorScope} />,
    },
    {
      title: "材料",
      dataIndex: "document_count",
      key: "document_count",
      width: 76,
      responsive: ["sm"],
      sorter: true,
      sortOrder: sortOrderFor("document_count", sort, direction),
    },
    {
      title: "待决定",
      dataIndex: "pending_decisions",
      key: "pending_decisions",
      width: 88,
      responsive: ["sm"],
      sorter: true,
      sortOrder: sortOrderFor("pending_decisions", sort, direction),
    },
    {
      title: "更新时间",
      dataIndex: "updated_at",
      key: "updated_at",
      width: 160,
      responsive: ["sm"],
      sorter: true,
      sortOrder: sortOrderFor("updated_at", sort, direction),
      render: (value: string) => formatDateTime(value),
    },
    {
      title: "业务线 / 场景",
      key: "business",
      width: 170,
      responsive: ["sm"],
      render: (_, item) => businessSummary(item),
    },
    {
      title: "业务状态",
      key: "status",
      width: 120,
      responsive: ["sm"],
      render: (_, item) => <CaseStatusCell item={item} />,
    },
    {
      title: "最新运行",
      key: "run",
      width: 160,
      responsive: ["sm"],
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
  actorScope = "",
}: {
  items: CaseSummary[];
  sort: CaseSortField;
  direction: SortDirection;
  selectedId: string | null;
  onSort: (field: CaseSortField) => void;
  actorScope?: string;
}) {
  const location = useLocation();
  const columns = useMemo(
    () => caseColumns(location, sort, direction, actorScope),
    [location, sort, direction, actorScope],
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
    <>
      <p className="cases-table-scroll-note">
        表格列较多时可横向滚动查看其余字段。
      </p>
      <Table
        scroll={{ x: "max-content" }}
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
    </>
  );
}
