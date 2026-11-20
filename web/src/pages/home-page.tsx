// File metadata (manifest mirror):
// Purpose: Presents the signed-in person's assigned work, separately labeled visible case activity, contract intake and authorized recent visits.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-20
// Revision: r11
// Copyright: See LICENSE
// End file metadata.

import { useQueries } from "@tanstack/react-query";
import { Button } from "antd";
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
import { caseHref, formatDateTime } from "../domain/review";
import { inboxRows, type InboxKind } from "../features/home/inbox";
import { recentCaseVisits } from "../features/home/recent-cases";
import { withEvidenceReadSlot } from "../features/review/evidence-read-slot";
import type { IntakeContinuationSelection } from "../features/intake/intake-continuation";
import "./home-page.css";

const evidencePageSize = 8;
const maximumPreviewRows = 3;
const kinds: Record<InboxKind, string> = {
  decision: "等我判断",
  evidence: "要补材料",
  approval: "会签",
  task: "任务",
};

function measuredCount(count: number, known: boolean, complete: boolean): string {
  if (complete) return String(count);
  if (known && count > 0) return `至少 ${count}`;
  return "未确认";
}

function PersonalWorkspace({ actor, scope }: { actor: Actor; scope: string }) {
  const snapshot = useWorkbench("week", scope);
  const approvals = useApprovalInbox("todo", scope, true);
  const [caseLimit, setCaseLimit] = useState(evidencePageSize);
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
  const hasMoreApprovals = approvalLoaded && !approvalComplete;
  const showMoreCases = caseLimit < openCases.length;
  const previewLimit = showMoreCases ? maximumPreviewRows - 1 : maximumPreviewRows;
  const hasMoreWork = rows.length > previewLimit || hasMoreApprovals;
  const previewRows = showAll ? rows : rows.slice(0, hasMoreWork ? previewLimit - 1 : previewLimit);
  const approvalCount = rows.filter((row) => row.kind === "approval").length;
  const decisionCount = rows.filter((row) => row.kind === "decision").length;
  const evidenceCount = rows.filter((row) => row.kind === "evidence").length;
  const complete = currentSnapshot !== undefined && evidenceComplete && approvalComplete;
  const casePendingKnown = openCases.filter((item) => item.pending_decisions !== null);
  const visiblePending = casePendingKnown.reduce(
    (sum, item) => sum + (item.pending_decisions ?? 0),
    0,
  );
  const visibleCaseIds = new Set(openCases.map((item) => item.id));
  const submittedRemediations = new Set(
    (currentSnapshot?.remediations ?? [])
      .filter((item) => item.status === "submitted" && visibleCaseIds.has(item.case_id))
      .map((item) => JSON.stringify([item.case_id, item.id])),
  ).size;
  const caseTitles = new Map(currentCases.map((item) => [item.id, item.title]));
  const visits = recentCaseVisits(actor)
    .filter((visit) => caseTitles.has(visit.caseId))
    .slice(0, 3);

  return (
    <section className="page home-page" aria-labelledby="home-title">
      <header className="home-heading">
        <h1 id="home-title">我的待办</h1>
        <p>
          {actor.display_name}，你好。
          {complete
            ? rows.length
              ? `你有 ${rows.length} 件待办。`
              : "今天没有待办。"
            : rows.length
              ? `目前确认有 ${rows.length} 件待办。`
              : "待办数量尚未确认。"}
        </p>
      </header>

      <dl className="home-inbox-summary" aria-label="待办汇总">
        <div data-kind="decision">
          <dt>等我判断</dt>
          <dd title="已分派给我的判断任务">
            {measuredCount(decisionCount, currentSnapshot !== undefined, evidenceComplete)}
          </dd>
        </div>
        <div data-kind="evidence">
          <dt>要补材料</dt>
          <dd>{measuredCount(evidenceCount, readyEvidenceCases > 0, evidenceComplete)}</dd>
        </div>
        <div data-kind="approval">
          <dt>会签</dt>
          <dd>{measuredCount(approvalCount, approvalLoaded, approvalComplete)}</dd>
        </div>
        <div data-kind="remediation">
          <dt>整改待确认（可见案件）</dt>
          <dd>{currentSnapshot === undefined ? "未确认" : submittedRemediations}</dd>
        </div>
      </dl>

      <div className="home-inbox-layout">
        <div className="home-inbox-main">
          <section className="home-panel home-personal-work" aria-labelledby="personal-work-title">
            <header className="home-panel-head">
              <h2 id="personal-work-title">待办事项</h2>
              <span className="meta">按到期时间排序</span>
            </header>
            {snapshot.isError ? (
              <p className="home-panel-note" role="alert">
                任务和可见案件暂未加载。{" "}
                <Button size="small" onClick={() => void snapshot.refetch()}>
                  重试
                </Button>
              </p>
            ) : null}
            {approvals.isError ? (
              <p className="home-panel-note" role="alert">
                会签待办暂未加载。{" "}
                <Button size="small" onClick={() => void approvals.refetch()}>
                  重试会签
                </Button>
              </p>
            ) : null}
            {rows.length ? (
              <div className="home-inbox-table">
                <table>
                  <thead>
                    <tr>
                      <th scope="col">类型</th>
                      <th scope="col">事项</th>
                      <th scope="col">到期</th>
                      <th scope="col">操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {previewRows.map((row) => (
                      <tr key={row.key}>
                        <td>
                          <span className="home-inbox-kind" data-kind={row.kind}>
                            {kinds[row.kind]}
                          </span>
                        </td>
                        <td>
                          <strong>{row.title}</strong>
                          {row.caseTitle && <span className="meta">{row.caseTitle}</span>}
                        </td>
                        <td>
                          {row.due ? (
                            <time dateTime={row.due}>{formatDateTime(row.due).split(" ")[0]}</time>
                          ) : (
                            "未注明"
                          )}
                        </td>
                        <td>
                          <Link to={row.href} aria-label={`${row.action}：${row.title}`}>
                            {row.action}
                          </Link>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <p className="home-panel-note">
                {complete ? "没有待办事项。" : "尚未确认有待办事项。"}
              </p>
            )}
            {!evidenceComplete && currentSnapshot && (
              <p className="home-panel-note" role="status">
                补件已核对 {readyEvidenceCases} / {openCases.length} 个可见案件。
                {requests.some((request) => request.isError) ? " 部分案件的补件未能读取。" : null}
                {showMoreCases ? (
                  <Button
                    size="small"
                    disabled={evidenceBusy}
                    onClick={() => setCaseLimit((limit) => limit + evidencePageSize)}
                  >
                    核对更多案件
                  </Button>
                ) : null}
              </p>
            )}
            {hasMoreWork ? (
              <footer className="home-panel-note home-inbox-more">
                <Button size="small" aria-expanded={showAll} onClick={() => setShowAll(!showAll)}>
                  {showAll ? "收起待办" : "查看全部待办"}
                </Button>
                {showAll && hasMoreApprovals ? (
                  <Link to="/workbench/approvals?view=todo">查看待会签</Link>
                ) : null}
              </footer>
            ) : null}
          </section>

          <section className="home-visible-work" aria-labelledby="visible-work-title">
            <h2 id="visible-work-title">可见案件待处理</h2>
            <p>按当前身份可见的未归档案件统计。</p>
            <p>
              待判断{" "}
              {measuredCount(
                visiblePending,
                casePendingKnown.length > 0,
                currentSnapshot !== undefined && casePendingKnown.length === openCases.length,
              )}{" "}
              条
              {currentSnapshot && casePendingKnown.length < openCases.length
                ? ` · ${openCases.length - casePendingKnown.length} 个案件未返回数量`
                : null}
            </p>
          </section>
        </div>

        <aside className="home-inbox-side" aria-label="上传、续办与最近打开">
          <IntakePrimaryContract actor={actor} selectedContinuation={selection} compact />
          <IntakePendingList
            actor={actor}
            compact
            selectedOperationId={selection?.detail.operation_id ?? null}
            onSelectContinuation={setSelection}
            onArchived={(id) =>
              setSelection((current) =>
                current?.detail.operation_id === id ? { ...current, archived: true } : current,
              )
            }
          />
          <section className="home-panel home-recent-visits" aria-labelledby="recent-visits-title">
            <header className="home-panel-head">
              <h2 id="recent-visits-title">最近打开</h2>
            </header>
            {visits.length ? (
              <ul>
                {visits.map((visit) => (
                  <li key={visit.caseId}>
                    <Link to={caseHref(visit.caseId)}>{caseTitles.get(visit.caseId)}</Link>
                    <time dateTime={visit.visitedAt}>{formatDateTime(visit.visitedAt)}</time>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="home-panel-note">
                {currentSnapshot ? "本浏览器尚无可查看的访问记录。" : "正在确认可见案件。"}
              </p>
            )}
          </section>
        </aside>
      </div>
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
