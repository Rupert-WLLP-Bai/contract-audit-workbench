// File metadata (manifest mirror):
// Purpose: Renders the cases workbench table with stable identity links, responsive summary details and locally scrollable columns; server-derived state does not establish business authorization or evidence validity.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-15
// Revision: r6
// Copyright: See LICENSE
// End file metadata.

import { Link, useLocation, useNavigate } from "react-router";
import { Empty, Table } from "antd";
import type { ColumnsType, TableProps } from "antd/es/table";
import type { CaseSummary, WorkbenchSnapshot } from "../api/types";
import {
  caseHref,
  caseStagePresentation,
  type CaseSortField,
  type SortDirection,
} from "../domain/review";
import { caseIdentityDetails } from "../domain/case-identity";
import { compactTime } from "../domain/time";
import { writeReviewDrawer } from "../features/review/work-items";
import { StatusText } from "./status-text";
import { TechnicalDetail } from "./technical-detail";
import { InvalidTimeDetail } from "./invalid-time-detail";
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
  return SORT_FIELDS.includes(field as CaseSortField) ? (field as CaseSortField) : null;
}

function CaseActionLink({
  item,
  href,
  label,
  actorScope,
}: {
  item: CaseSummary;
  href: string;
  label: string;
  actorScope: string;
}) {
  const navigate = useNavigate();
  const location = useLocation();
  const params = new URLSearchParams(location.search);
  params.set("selected", item.id);
  const returnTo = `${location.pathname}?${params}`;
  const state = { returnTo, selectedId: item.id, actorScope };
  return (
    <Link
      className="case-list-action"
      to={href}
      state={{ ...state, scrollY: window.scrollY }}
      aria-label={`${label}：${item.title}`}
      onClick={(event) => {
        if (event.button !== 0 || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey)
          return;
        event.preventDefault();
        void navigate(href, { state: { ...state, scrollY: window.scrollY } });
      }}
    >
      {label}
    </Link>
  );
}

function caseRow(item: CaseSummary, snapshot: WorkbenchSnapshot | undefined, role: string) {
  const documents = snapshot?.documents.filter((entry) => entry.case_id === item.id);
  const snapshotCase = snapshot?.cases.find((entry) => entry.id === item.id);
  const matchingCase = snapshotCase?.updated_at === item.updated_at;
  const known = matchingCase && documents?.length === item.document_count;
  const current = known ? documents.filter((entry) => !entry.superseded) : undefined;
  const failures = current?.filter((entry) => entry.parse_status === "failed").length;
  const parsing = failures
    ? "failed"
    : current?.some((entry) => entry.parse_status === "pending")
      ? "pending"
      : current?.every((entry) => entry.parse_status === "ready")
        ? "ready"
        : "unknown";
  const canReview = role === "admin" || role === "reviewer";
  const canUpload = role === "admin" || role === "clerk";
  const matchingRun = snapshot?.runs.find(
    (entry) =>
      entry.case_id === item.id &&
      entry.id === item.latest_run?.id &&
      entry.updated_at === item.latest_run.updated_at,
  );
  const reports = matchingCase
    ? snapshot?.reports.filter((entry) => entry.case_id === item.id)
    : undefined;
  const remediation = matchingCase
    ? snapshot?.remediations.filter((entry) => entry.case_id === item.id)
    : undefined;
  const stage = caseStagePresentation({
    archived: Boolean(item.archived_at),
    documentCount: current?.length ?? item.document_count,
    parsing,
    runKnown: true,
    runState: item.latest_run?.state ?? null,
    pendingDecisions: item.pending_decisions,
    pendingRemediation:
      remediation?.some((entry) => ["open", "submitted", "reopened"].includes(entry.status)) ??
      null,
    reportAvailable: reports ? reports.length > 0 : null,
    canUpload,
    canStartReview: canReview,
    startEligible: false,
    preparationAvailable: item.latest_run?.state === "partial",
    retryEligible: false,
    canRetry: false,
    canReview,
    canRemediate: canReview,
    canCreateReport: false,
    reportEligible: false,
    canReadReport: role !== "",
  });
  const caseURL = caseHref(item.id);
  let href = caseURL;
  if (stage.action === "materials" || stage.action === "upload") href += "#case-materials";
  if (stage.action === "prepare") href += "#review-readiness";
  if (stage.action === "remediation") href += "#case-remediation";
  if (stage.action === "view-report") href += "#case-report-archive";
  if (
    item.latest_run &&
    (stage.action === "judge" ||
      stage.action === "progress" ||
      (stage.aiIncomplete && stage.action === "view"))
  ) {
    href = caseHref(item.id, item.latest_run.id, matchingRun?.attempt_id ?? null);
    if (stage.action === "view") {
      const [path, search] = href.split("?");
      const params = writeReviewDrawer(new URLSearchParams(search), "record", "review");
      href = `${path}?${params}`;
    }
  }
  const label =
    stage.aiIncomplete && stage.action === "view" && !canReview ? "查看" : stage.actionLabel;
  return { stage, href, label, failures };
}

const present = (entry: ReturnType<typeof caseRow>) =>
  entry.failures
    ? "bad"
    : entry.stage.aiIncomplete
      ? "attention"
      : entry.stage.action === "progress"
        ? "progress"
        : entry.stage.action === "judge" || entry.stage.action === "remediation"
          ? "attention"
          : "neutral";

export function CasesTable({
  items,
  sort,
  direction,
  selectedId,
  onSort,
  actorScope = "",
  snapshot,
  role = "",
}: {
  items: CaseSummary[];
  sort: CaseSortField;
  direction: SortDirection;
  selectedId: string | null;
  onSort: (field: CaseSortField) => void;
  actorScope?: string;
  snapshot?: WorkbenchSnapshot;
  role?: string;
}) {
  if (items.length === 0) return <Empty description="没有符合条件的案件。" />;
  const rows = new Map(items.map((item) => [item.id, caseRow(item, snapshot, role)]));
  const columns: ColumnsType<CaseSummary> = [
    {
      title: "案件",
      key: "title",
      sorter: true,
      sortOrder: sortOrderFor("title", sort, direction),
      render: (_, item) => {
        const identity = caseIdentityDetails(item);
        const row = rows.get(item.id)!;
        return (
          <>
            <strong className="caw-wrap">{item.title}</strong>
            {identity ? <div className="meta caw-wrap">{identity}</div> : null}
            <TechnicalDetail value={item.id} label="案件标识" />
            <div className="case-list-mobile-facts">
              <StatusText tone={present(row)}>{row.stage.label}</StatusText>
              {row.failures ? <StatusText tone="bad">{row.failures} 份解析失败</StatusText> : null}
            </div>
          </>
        );
      },
    },
    {
      title: "材料",
      key: "document_count",
      width: "12%",
      responsive: ["md"],
      sorter: true,
      sortOrder: sortOrderFor("document_count", sort, direction),
      render: (_, item) => {
        const row = rows.get(item.id)!;
        return (
          <>
            <div>{item.document_count} 份</div>
            {row.failures ? <StatusText tone="bad">{row.failures} 份解析失败</StatusText> : null}
          </>
        );
      },
    },
    {
      title: "当前状态",
      key: "pending_decisions",
      width: "17%",
      responsive: ["md"],
      sorter: true,
      sortOrder: sortOrderFor("pending_decisions", sort, direction),
      render: (_, item) => {
        const row = rows.get(item.id)!;
        return <StatusText tone={present(row)}>{row.stage.label}</StatusText>;
      },
    },
    {
      title: "更新时间",
      key: "updated_at",
      width: "14%",
      responsive: ["md"],
      sorter: true,
      sortOrder: sortOrderFor("updated_at", sort, direction),
      render: (_, item) => (
        <>
          {compactTime(item.updated_at)}
          <InvalidTimeDetail value={item.updated_at} label="案件更新时间" />
        </>
      ),
    },
    {
      title: "操作",
      key: "action",
      width: "clamp(6.5rem, 12vw, 10.5rem)",
      align: "right",
      render: (_, item) => {
        const { label, href } = rows.get(item.id)!;
        return <CaseActionLink item={item} href={href} label={label} actorScope={actorScope} />;
      },
    },
  ];
  return (
    <Table
      className="case-list-table"
      tableLayout="fixed"
      sortDirections={["ascend", "descend", "ascend"]}
      rowKey="id"
      dataSource={items}
      columns={columns}
      pagination={false}
      onChange={(_pagination, _filters, sorter) => {
        const field = sortFieldOf(sorter);
        if (field) onSort(field);
      }}
      rowClassName={(item) => (item.id === selectedId ? "cases-table-row-selected" : "")}
    />
  );
}
