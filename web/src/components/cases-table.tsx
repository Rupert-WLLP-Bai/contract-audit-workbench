import { Link, useLocation } from "react-router";
import type { CaseSummary } from "../api/types";
import type { CaseSortField, SortDirection } from "../domain/review";
import { contractTypeLabel, formatDateTime, runLabel, runTone } from "../domain/review";
import { StatusText } from "./status-text";

const SORTABLE_COLUMNS: { field: CaseSortField; label: string }[] = [
  { field: "title", label: "案件" },
  { field: "document_count", label: "材料" },
  { field: "pending_decisions", label: "待决定" },
  { field: "updated_at", label: "更新时间" },
];

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
  if (items.length === 0) return <p role="status">没有符合条件的案件。</p>;
  return (
    <div className="data-table-scroll" role="region" aria-label="案件结果" tabIndex={0}>
      <table className="data-table cases-table">
        <caption className="sr-only">案件列表。可按案件、材料、待决定和更新时间排序。</caption>
        <thead>
          <tr>
            {SORTABLE_COLUMNS.map(({ field, label }) => (
              <th key={field} scope="col" aria-sort={sort === field ? direction === "asc" ? "ascending" : "descending" : "none"}>
                <button type="button" className="sort-button" onClick={() => onSort(field)}>
                  {label}{sort === field ? <span aria-hidden="true"> {direction === "asc" ? "↑" : "↓"}</span> : null}
                  <span className="sr-only">，按{label}排序</span>
                </button>
              </th>
            ))}
            <th scope="col">业务线 / 场景</th>
            <th scope="col">业务状态</th>
            <th scope="col">最新运行</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => {
            const returnParams = new URLSearchParams(location.search);
            returnParams.set("selected", item.id);
            const returnTo = `${location.pathname}?${returnParams.toString()}`;
            return (
              <tr key={item.id} data-selected={item.id === selectedId ? "true" : undefined} aria-current={item.id === selectedId ? "true" : undefined}>
                <th scope="row">
                  <Link
                    to={`/cases/${encodeURIComponent(item.id)}`}
                    state={{ returnTo, scrollY: typeof window === "undefined" ? 0 : window.scrollY, selectedId: item.id }}
                  >
                    {item.title}
                  </Link>
                  <div className="meta">{item.id} · {contractTypeLabel(item.contract_type)}</div>
                </th>
                <td>{item.document_count}</td>
                <td>{item.pending_decisions}</td>
                <td>{formatDateTime(item.updated_at)}</td>
                <td>{[item.business_line, item.scenario].filter(Boolean).join(" · ") || "未提供"}</td>
                <td>{item.archived_at ? <StatusText tone="neutral">已归档</StatusText> : item.business_status || "未提供"}</td>
                <td>{item.latest_run ? <StatusText tone={runTone(item.latest_run.state)}>{runLabel(item.latest_run.state)}</StatusText> : "尚未审查"}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
