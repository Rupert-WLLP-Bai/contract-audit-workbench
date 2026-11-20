// File metadata (manifest mirror):
// Purpose: Presents personal assignments, visible team work, approvals and remediation in separate inbox views, alongside available continuations and identity-scoped recent cases.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-20
// Revision: r12
// Copyright: See LICENSE
// End file metadata.

import { useQueries } from "@tanstack/react-query";
import { Button, Tabs } from "antd";
import { useState } from "react";
import { Link } from "react-router";
import { useApprovalInbox } from "../api/approval-queries";
import { evidenceRequestsKey, listEvidenceRequests } from "../api/evidence-workflow";
import { errorMessage } from "../api/http";
import { useSession, useWorkbench } from "../api/queries";
import type { Actor } from "../api/types";
import { IntakePendingList } from "../components/intake-pending-list";
import { IntakePrimaryContract } from "../components/intake-primary-contract";
import { PageStatus } from "../components/page-status";
import { actorReadContextKey } from "../domain/actor-read-context";
import { caseHref } from "../domain/review";
import { compactTime, formatDateTime } from "../domain/time";
import { inboxRows, visibleCaseRows, type InboxKind } from "../features/home/inbox";
import { recentCaseVisits } from "../features/home/recent-cases";
import { withEvidenceReadSlot } from "../features/review/evidence-read-slot";
import type { IntakeContinuationSelection } from "../features/intake/intake-continuation";
import "./home-page.css";

const evidencePageSize = 8;
const maximumPreviewRows = 3;
const kinds: Record<InboxKind, string> = {
  decision: "风险判断",
  evidence: "补件",
  approval: "会签",
  task: "任务",
  team: "风险判断",
  remediation: "整改",
};

const viewNames = {
  assigned: "指派给我",
  team: "团队待处理",
  approval: "待我会签",
  remediation: "整改待确认",
};

type View = keyof typeof viewNames;
const views: View[] = ["assigned", "team", "approval", "remediation"];

function measuredCount(count: number, complete: boolean): string {
  if (complete) return String(count);
  return count > 0 ? `至少 ${count}` : "未确认";
}

function PersonalWorkspace({ actor, scope }: { actor: Actor; scope: string }) {
  const snapshot = useWorkbench("week", scope);
  const approvals = useApprovalInbox("todo", scope, true);
  const [caseLimit, setCaseLimit] = useState(evidencePageSize);
  const [chosenView, setChosenView] = useState<View | null>(null);
  const [showAll, setShowAll] = useState(false);
  const [selection, setSelection] = useState<IntakeContinuationSelection | null>(null);
  const currentSnapshot =
    snapshot.isSuccess && !snapshot.isError && !snapshot.isFetching ? snapshot.data : undefined;
  const currentCases = currentSnapshot?.cases ?? [];
  const openCases = [
    ...new Map(
      currentCases.filter((item) => !item.archived_at).map((item) => [item.id, item]),
    ).values(),
  ];
  const evidenceCases = openCases.slice(0, caseLimit);
  const requests = useQueries({
    queries: evidenceCases.map((item) => ({
      queryKey: [...evidenceRequestsKey(item.id), "home", scope] as const,
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        withEvidenceReadSlot(signal, async () => {
          const entries = await listEvidenceRequests(item.id, signal);
          if (entries.some((entry) => entry.case_id !== item.id))
            throw new Error("补件记录与当前案件不一致");
          return entries;
        }),
      retry: false,
      staleTime: 30_000,
    })),
  });
  const usableRequests = requests.flatMap((query) =>
    query.isSuccess && !query.isError && !query.isFetching ? query.data : [],
  );
  const readyEvidenceCases = requests.filter(
    (query) => query.isSuccess && !query.isError && !query.isFetching,
  ).length;
  const evidenceComplete = currentSnapshot !== undefined && readyEvidenceCases === openCases.length;
  const evidenceBusy = requests.some((query) => query.isPending || query.isFetching);
  const approvalLoaded = approvals.isSuccess && !approvals.isError && !approvals.isFetching;
  const approvalComplete =
    approvalLoaded &&
    !approvals.data.pages.some((page, index, pages) => index === pages.length - 1 && page.has_more);
  const rows = inboxRows(
    currentSnapshot,
    actor.id,
    usableRequests,
    approvalLoaded ? approvals.data.pages.flatMap((page) => page.items) : [],
  );
  const { team, remediation, teamComplete } = visibleCaseRows(currentSnapshot);
  const viewRows = {
    assigned: rows.filter((row) => row.kind !== "approval"),
    team,
    approval: rows.filter((row) => row.kind === "approval"),
    remediation,
  };
  const completed = {
    assigned: evidenceComplete,
    team: teamComplete,
    approval: approvalComplete,
    remediation: currentSnapshot !== undefined,
  };
  const view =
    chosenView ??
    (completed.assigned && !viewRows.assigned.length && team.length ? "team" : "assigned");
  const selectedRows = viewRows[view];
  const preview = showAll ? selectedRows : selectedRows.slice(0, maximumPreviewRows);
  const showMoreCases = view === "assigned" && caseLimit < openCases.length;
  const evidenceFailed = view === "assigned" && requests.some((query) => query.isError);
  const caseTitles = new Map(currentCases.map((item) => [item.id, item.title]));
  const selectView = (key: string) => {
    const next = views.find((item) => item === key);
    if (!next) return;
    setChosenView(next);
    setShowAll(false);
  };
  const visits = recentCaseVisits(actor)
    .filter((visit) => caseTitles.has(visit.caseId))
    .slice(0, 4);

  return (
    <section className="page home-page" aria-labelledby="home-title">
      <h1 id="home-title">我的待办</h1>
      <IntakePendingList
        actor={actor}
        compact
        hideWhenEmpty
        selectedOperationId={selection?.detail.operation_id ?? null}
        onSelectContinuation={setSelection}
        onArchived={(id) =>
          setSelection((current) =>
            current?.detail.operation_id === id ? { ...current, archived: true } : current,
          )
        }
      />
      {selection ? (
        <IntakePrimaryContract actor={actor} selectedContinuation={selection} compact />
      ) : null}
      <Tabs
        className="home-inbox-views"
        aria-label="待办视图"
        activeKey={view}
        destroyOnHidden
        classNames={{
          header: "home-view-header",
          item: "home-view-tab",
          indicator: "home-view-indicator",
        }}
        onTabClick={selectView}
        onChange={selectView}
        items={views.map((key) => ({
          key,
          label: (
            <>
              {viewNames[key]}{" "}
              <span className="home-view-count">
                {measuredCount(viewRows[key].length, completed[key])}
              </span>
            </>
          ),
          children:
            key === view ? (
              <div className="home-view-content">
                <div className="home-inbox-table">
                  <table aria-label={viewNames[view]}>
                    <thead>
                      <tr>
                        <th scope="col">事项</th>
                        <th scope="col">类型</th>
                        <th scope="col" aria-label="更新时间 / 截止">
                          <span className="home-date-heading">更新时间 / 截止</span>
                          <span className="home-date-heading-short" aria-hidden="true">
                            时间
                          </span>
                        </th>
                        <th scope="col">操作</th>
                      </tr>
                    </thead>
                    <tbody>
                      {preview.map((row) => {
                        const date =
                          row.kind === "team" || row.kind === "approval" ? row.updatedAt : row.due;
                        return (
                          <tr key={row.key}>
                            <td>
                              <strong>{row.title}</strong>
                              {row.caseTitle ? <span className="meta">{row.caseTitle}</span> : null}
                            </td>
                            <td>
                              <span className="home-inbox-kind" data-kind={row.kind}>
                                {kinds[row.kind]}
                              </span>
                            </td>
                            <td>{date ? <time dateTime={date}>{compactTime(date)}</time> : "—"}</td>
                            <td>
                              <Link to={row.href} aria-label={`${row.action}：${row.title}`}>
                                {row.action}
                              </Link>
                            </td>
                          </tr>
                        );
                      })}
                      {!preview.length ? (
                        <tr>
                          <td colSpan={4} className="home-view-empty">
                            {completed[view] ? "暂无事项" : "暂未确认有事项"}
                          </td>
                        </tr>
                      ) : null}
                    </tbody>
                  </table>
                </div>
                {snapshot.isError && view !== "approval" ? (
                  <p className="home-inbox-feedback" role="alert">
                    任务和可见案件暂未加载。
                    <Button size="small" onClick={() => void snapshot.refetch()}>
                      重试
                    </Button>
                  </p>
                ) : null}
                {approvals.isError && view === "approval" ? (
                  <p className="home-inbox-feedback" role="alert">
                    会签待办暂未加载。
                    <Button size="small" onClick={() => void approvals.refetch()}>
                      重试
                    </Button>
                  </p>
                ) : null}
                {evidenceFailed ? (
                  <p className="home-inbox-feedback" role="status">
                    部分案件的补件未能读取。
                  </p>
                ) : null}
                {showMoreCases ||
                selectedRows.length > maximumPreviewRows ||
                (view === "approval" && approvalLoaded && !approvalComplete) ? (
                  <div className="home-inbox-feedback">
                    {selectedRows.length > maximumPreviewRows ? (
                      <Button
                        size="small"
                        aria-expanded={showAll}
                        onClick={() => setShowAll(!showAll)}
                      >
                        {showAll ? "收起待办" : "查看全部待办"}
                      </Button>
                    ) : null}
                    {showMoreCases ? (
                      <Button
                        size="small"
                        disabled={evidenceBusy}
                        onClick={() => setCaseLimit(caseLimit + evidencePageSize)}
                      >
                        核对更多案件
                      </Button>
                    ) : null}
                    {view === "approval" && approvalLoaded && !approvalComplete ? (
                      <Link to="/workbench/approvals?view=todo">查看全部待会签</Link>
                    ) : null}
                  </div>
                ) : null}
              </div>
            ) : null,
        }))}
      />
      {visits.length ? (
        <section className="home-recent-visits" aria-labelledby="recent-visits-title">
          <h2 id="recent-visits-title">最近打开</h2>
          <ul>
            {visits.map((visit) => (
              <li key={visit.caseId}>
                <Link to={caseHref(visit.caseId)}>
                  <span>{caseTitles.get(visit.caseId)}</span>
                  <time dateTime={visit.visitedAt} aria-hidden="true">
                    {formatDateTime(visit.visitedAt).slice(-5)}
                  </time>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </section>
  );
}

export function HomePage() {
  const session = useSession();
  if (session.isLoading || session.isFetching)
    return <PageStatus kind="loading" title="正在确认当前身份" />;
  if (!session.data || session.isError)
    return (
      <PageStatus
        kind="error"
        title="当前身份没有加载"
        detail={errorMessage(session.error)}
        action={<Button onClick={() => void session.refetch()}>重新读取身份</Button>}
      />
    );
  const scope = actorReadContextKey(session.data);
  return <PersonalWorkspace key={scope} actor={session.data} scope={scope} />;
}
