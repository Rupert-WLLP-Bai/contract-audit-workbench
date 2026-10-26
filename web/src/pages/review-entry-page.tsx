import { Button } from "antd";
import { errorMessage } from "../api/http";
import { useWorkbench } from "../api/queries";
import type { WorkbenchRun } from "../api/types";
import { PageStatus } from "../components/page-status";
import { StatusText } from "../components/status-text";
import { formatDateTime } from "../domain/review";
import "./review-entry-page.css";

function runTone(state: string): "good" | "attention" | "bad" | "neutral" {
  if (state === "completed") return "good";
  if (
    [
      "accepted",
      "queued",
      "started",
      "running",
      "awaiting_human",
      "partial",
    ].includes(state)
  )
    return "attention";
  if (["failed", "cancelled", "interrupted"].includes(state)) return "bad";
  return "neutral";
}

function latestRun(runs: WorkbenchRun[]): WorkbenchRun | null {
  return (
    [...runs].sort(
      (left, right) =>
        right.updated_at.localeCompare(left.updated_at) ||
        right.id.localeCompare(left.id),
    )[0] ?? null
  );
}

export function ReviewEntryPage() {
  const query = useWorkbench("week");
  if (query.isLoading)
    return <PageStatus kind="loading" title="正在读取审查工作区案件" />;
  if (query.isError || !query.data)
    return (
      <PageStatus
        kind="error"
        title="审查工作区没有加载"
        detail={errorMessage(query.error)}
        action={
          <Button
            aria-label={"重试"}
            type="primary"
            htmlType="button"
            onClick={() => void query.refetch()}
          >
            重试
          </Button>
        }
      />
    );

  const snapshot = query.data;
  return (
    <section className="page review-entry-page">
      <header className="page-head">
        <div>
          <h1>合同审查</h1>
          <p>选择当前身份可见的案件，进入其持久审查运行或发起新的审查。</p>
        </div>
        <Button aria-label={"案件列表"} type="text" href="/cases">
          案件列表
        </Button>
      </header>
      <div className="review-entry-summary" aria-label="审查工作区摘要">
        <div>
          <span>可见案件</span>
          <strong>{snapshot.cases.length}</strong>
        </div>
        <div>
          <span>本周运行</span>
          <strong>{snapshot.runs.length}</strong>
        </div>
        <div>
          <span>待人工处理</span>
          <strong>{snapshot.dashboard.pending_review_cases}</strong>
        </div>
      </div>
      {snapshot.cases.length === 0 ? (
        <PageStatus
          kind="empty"
          title="当前身份没有可访问案件"
          detail="案件列表按当前服务端授权返回。"
        />
      ) : (
        <ul className="review-entry-list">
          {snapshot.cases.map((item) => {
            const runs = snapshot.runs
              .filter((run) => run.case_id === item.id)
              .sort((left, right) =>
                right.updated_at.localeCompare(left.updated_at),
              );
            const latest = latestRun(runs);
            return (
              <li key={item.id}>
                <div className="review-entry-case">
                  <p>{item.id}</p>
                  <h2>{item.title}</h2>
                  <span>
                    {item.business_status ??
                      item.business_line ??
                      "业务状态未报告"}
                  </span>
                </div>
                <div className="review-entry-run">
                  {latest ? (
                    <>
                      <span>
                        最近运行 <strong>{latest.id}</strong>
                      </span>
                      <StatusText tone={runTone(latest.state)}>
                        {latest.state}
                      </StatusText>
                      <time dateTime={latest.updated_at}>
                        {formatDateTime(latest.updated_at)}
                      </time>
                      <span>
                        尝试 {latest.attempt_id ?? "未报告"} · 本周{" "}
                        {runs.length} 次运行
                      </span>
                    </>
                  ) : (
                    <p>尚无快照中的审查运行。进入案件可检查材料并启动审查。</p>
                  )}
                </div>
                <div className="review-entry-actions">
                  {latest ? (
                    <Button
                      aria-label={"打开审查"}
                      type="primary"
                      href={`/cases/${encodeURIComponent(item.id)}/reviews/${encodeURIComponent(latest.id)}${latest.attempt_id ? `?attempt_id=${encodeURIComponent(latest.attempt_id)}` : ""}`}
                    >
                      打开审查
                    </Button>
                  ) : null}
                  <Button
                    aria-label={String(latest ? "案件详情" : "进入案件")}
                    href={`/cases/${encodeURIComponent(item.id)}`}
                  >
                    {latest ? "案件详情" : "进入案件"}
                  </Button>
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
