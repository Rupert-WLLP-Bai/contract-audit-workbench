// File metadata (manifest mirror):
// Purpose: Lets a user choose distinct saved reviews of one authorized case and provides shortcuts for verified recent pairs.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-28
// Updated: 2026-11-20
// Revision: r7
// Copyright: See LICENSE
// End file metadata.

// Version selection uses only authorized review history; it never creates a review.
import { Button, Select } from "antd";
import { useQueries } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { getReview } from "../api/endpoints";
import { errorMessage, isForbidden, isUnauthenticated } from "../api/http";
import { useCaseReviewHistory, useSession, useWorkbench } from "../api/queries";
import type { ReviewWorkspace, WorkbenchRun } from "../api/types";
import { PageStatus } from "../components/page-status";
import { actorReadContextKey } from "../domain/actor-read-context";
import { caseIdentityDetails } from "../domain/case-identity";
import { compactTime } from "../domain/time";
import { runLabel } from "../domain/review";
import { frozenWorkspaceProblem } from "../domain/review-diff";
import "./compare-entry-page.css";

function frozenReview(
  caseId: string,
  runId: string,
  workspace: ReviewWorkspace,
  expectedAttempt?: string | null,
) {
  const attempt = workspace.review_attempts?.attempt;
  return Boolean(
    attempt?.id &&
    (!expectedAttempt || attempt.id === expectedAttempt) &&
    !frozenWorkspaceProblem(workspace, caseId, runId, attempt.id),
  );
}
function recentFirst(a: WorkbenchRun, b: WorkbenchRun) {
  return b.updated_at.localeCompare(a.updated_at) || b.id.localeCompare(a.id);
}

export function CompareEntryPage() {
  const session = useSession();
  const actorScope =
    session.isSuccess && !session.isFetching ? actorReadContextKey(session.data) : "";
  const query = useWorkbench("week", actorScope);
  const navigate = useNavigate();
  const [selection, setSelection] = useState({ scope: "", caseId: "", left: "", right: "" });
  const current =
    selection.scope === actorScope
      ? selection
      : { scope: actorScope, caseId: "", left: "", right: "" };
  const snapshot = query.isSuccess && !query.isFetching && actorScope ? query.data : undefined;
  const selected = snapshot?.cases.find((item) => item.id === current.caseId);
  const history = useCaseReviewHistory(selected?.id ?? "", selected?.latest_run?.id ?? null);
  const olderRequested = useRef("");
  const historyKey = JSON.stringify([actorScope, selected?.id, selected?.latest_run?.id]);
  useEffect(() => {
    if (
      selected &&
      history.data?.length === 1 &&
      history.hasOlder &&
      !history.isFetching &&
      !history.isLoadingOlder &&
      olderRequested.current !== historyKey
    ) {
      olderRequested.current = historyKey;
      void history.fetchOlder();
    }
  }, [
    selected,
    history.data,
    history.hasOlder,
    history.isFetching,
    history.isLoadingOlder,
    history.fetchOlder,
    historyKey,
  ]);
  const candidates = useMemo(() => {
    if (!snapshot) return [];
    return snapshot.cases
      .flatMap((item) => {
        const runs = snapshot.runs
          .filter((run) => run.case_id === item.id && run.attempt_id)
          .sort(recentFirst);
        return runs.length >= 2 ? [{ item, runs: runs.slice(0, 2) }] : [];
      })
      .slice(0, 6);
  }, [snapshot]);
  const checks = useQueries({
    queries: candidates.map(({ item, runs }) => ({
      queryKey: [
        "compare-entry-verified",
        actorScope,
        item.id,
        ...runs.flatMap((run) => [run.id, run.attempt_id]),
      ],
      enabled: Boolean(actorScope),
      retry: false,
      queryFn: async ({ signal }: { signal: AbortSignal }) => {
        const results = await Promise.all(
          runs.map((run) => getReview(item.id, run.id, run.attempt_id, signal)),
        );
        return runs.every((run, index) =>
          frozenReview(item.id, run.id, results[index]!, run.attempt_id),
        );
      },
    })),
  });
  const entries =
    history.data?.filter(
      ({ runId, workspace }) => selected && frozenReview(selected.id, runId, workspace),
    ) ?? [];
  const left = entries.find((entry) => entry.runId === current.left);
  const right = entries.find((entry) => entry.runId === current.right);
  function chooseCase(caseId: string) {
    setSelection({ scope: actorScope, caseId, left: "", right: "" });
  }
  function startComparison() {
    if (!selected || !left || !right || left.runId === right.runId) return;
    const leftAttempt = left.workspace.review_attempts?.attempt.id;
    const rightAttempt = right.workspace.review_attempts?.attempt.id;
    if (!leftAttempt || !rightAttempt) return;
    const params = new URLSearchParams({
      case_id: selected.id,
      left_run_id: left.runId,
      left_attempt_id: leftAttempt,
      right_run_id: right.runId,
      right_attempt_id: rightAttempt,
    });
    void navigate(`/compare?${params}`);
  }
  if (session.isLoading || query.isLoading || query.isFetching)
    return <PageStatus kind="loading" title="正在读取案件" />;
  if (session.isError || query.isError || !snapshot) {
    const error = session.error ?? query.error;
    return (
      <PageStatus
        kind={isForbidden(error) || isUnauthenticated(error) ? "denied" : "error"}
        title={
          isForbidden(error) || isUnauthenticated(error)
            ? "没有权限读取案件列表"
            : "版本对照数据没有加载"
        }
        detail={errorMessage(error)}
        action={
          <Button onClick={() => void Promise.all([session.refetch(), query.refetch()])}>
            重试
          </Button>
        }
      />
    );
  }
  return (
    <section className="page compare-entry-page">
      <h1>版本对照</h1>
      <section className="compare-selector-card" aria-label="选择对照版本">
        <label>
          选择案件
          <Select
            aria-label="选择案件"
            showSearch={{ optionFilterProp: "label" }}
            value={selected?.id}
            placeholder="选择案件"
            onChange={chooseCase}
            options={snapshot.cases.map((item) => ({
              value: item.id,
              label: [item.title, caseIdentityDetails(item)].filter(Boolean).join(" · "),
            }))}
          />
        </label>
        <label>
          当前版本
          <Select
            aria-label="当前版本"
            value={left?.runId}
            placeholder="选择审查记录"
            disabled={!selected || history.isFetching}
            onChange={(value) => setSelection({ ...current, left: value })}
            options={entries.map(({ runId, workspace }, index) => ({
              value: runId,
              label: `记录 ${index + 1} · ${runLabel(workspace.run.state)} · ${compactTime(workspace.run.updated_at)}`,
            }))}
          />
        </label>
        <label>
          原留档版本
          <Select
            aria-label="原留档版本"
            value={right?.runId}
            placeholder="选择审查记录"
            disabled={!selected || history.isFetching}
            onChange={(value) => setSelection({ ...current, right: value })}
            options={entries.map(({ runId, workspace }, index) => ({
              value: runId,
              label: `记录 ${index + 1} · ${runLabel(workspace.run.state)} · ${compactTime(workspace.run.updated_at)}`,
            }))}
          />
        </label>
        <Button
          type="primary"
          disabled={!left || !right || left.runId === right.runId}
          onClick={startComparison}
        >
          开始对照
        </Button>
        {selected && history.isLoading ? <p role="status">正在读取审查记录…</p> : null}
        {selected && history.isError ? (
          <p role="alert">
            审查记录暂时无法读取。<Button onClick={() => void history.refetch()}>重试</Button>
          </p>
        ) : null}
        {selected && history.olderError ? (
          <p role="alert">
            更早的审查记录暂时无法读取。
            <Button onClick={() => void history.fetchOlder()}>重试</Button>
          </p>
        ) : null}
        {selected && history.cycleDetected ? (
          <p role="alert">审查历史存在循环，不能继续读取更早记录。</p>
        ) : null}
        {selected &&
        !history.isLoading &&
        !history.isError &&
        !history.hasOlder &&
        entries.length < 2 ? (
          <p>还没有两次可选择的留档审查。</p>
        ) : null}
        {selected && history.hasOlder && entries.length >= 2 ? (
          <Button onClick={() => void history.fetchOlder()} loading={history.isLoadingOlder}>
            查看更早审查
          </Button>
        ) : null}
      </section>
      {candidates.some((_, i) => checks[i]?.data === true && !checks[i]?.isFetching) ? (
        <section className="compare-quick-cases" aria-label="可选择的案件">
          <h2>可选择的案件</h2>
          {candidates.map(({ item }, i) =>
            checks[i]?.data === true && !checks[i]?.isFetching ? (
              <div key={item.id}>
                <span>{item.title}</span>
                <Button onClick={() => chooseCase(item.id)}>选择对照</Button>
              </div>
            ) : null,
          )}
        </section>
      ) : null}
      {checks.some((item) => item.isError) ? (
        <p role="alert">
          部分审查记录暂时无法读取。
          <Button
            onClick={() =>
              checks.forEach((item) => {
                if (item.isError) void item.refetch();
              })
            }
          >
            重试
          </Button>
        </p>
      ) : null}
    </section>
  );
}
