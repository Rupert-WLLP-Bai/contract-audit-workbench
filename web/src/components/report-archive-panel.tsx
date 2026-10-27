import { Button, Input, Select } from "antd";
import { Link } from "react-router";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { errorMessage } from "../api/http";
import { paths } from "../api/paths";
import { useArchiveCase, useCreateReport, useReview } from "../api/queries";
import type { CaseDetail, ReviewHistoryEntry } from "../api/types";
import {
  formatDateTime,
  isTerminalAttemptState,
  runLabel,
  runTone,
} from "../domain/review";
import { reviewChoiceForKey, reviewChoices } from "../domain/workflow";
import { ReviewChoiceSelect } from "./review-choice-select";
import { StatusText } from "./status-text";

export function ReportArchivePanel({
  caseId,
  detail,
  history,
  historyLoading,
  historyError,
  canCreateReport,
  canArchive,
  canReadReports,
  archived,
}: {
  caseId: string;
  detail: CaseDetail;
  history: ReviewHistoryEntry[];
  historyLoading: boolean;
  historyError: unknown;
  canCreateReport: boolean;
  canArchive: boolean;
  canReadReports: boolean;
  archived: boolean;
}) {
  const choices = reviewChoices(history);
  const [choiceKey, setChoiceKey] = useState("");
  const [archiveReportId, setArchiveReportId] = useState("");
  const [archiveReason, setArchiveReason] = useState("");
  const createReport = useCreateReport(caseId);
  const archive = useArchiveCase(caseId);
  const reportErrorRef = useRef<HTMLParagraphElement>(null);
  const archiveErrorRef = useRef<HTMLParagraphElement>(null);
  const selectedChoice = reviewChoiceForKey(choices, choiceKey);
  const selectedReview = useReview(
    caseId,
    selectedChoice?.runId ?? "",
    selectedChoice?.attempt.id ?? null,
  );

  useEffect(() => {
    if (!choiceKey && choices.length > 0) setChoiceKey(choices[0]?.key ?? "");
  }, [choiceKey, choices]);
  useEffect(() => {
    if (detail.reports.length > 0 && archiveReportId === "")
      setArchiveReportId(detail.reports[detail.reports.length - 1]?.id ?? "");
  }, [archiveReportId, detail.reports]);
  useEffect(() => {
    if (createReport.error) reportErrorRef.current?.focus();
  }, [createReport.error]);
  useEffect(() => {
    if (archive.error) archiveErrorRef.current?.focus();
  }, [archive.error]);

  function onCreateReport(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selectedChoice) return;
    createReport.mutate({
      run_id: selectedChoice.runId,
      attempt_id: selectedChoice.attempt.id,
    });
  }

  function onArchive(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    archive.mutate(
      { report_id: archiveReportId, reason: archiveReason.trim() },
      {
        onSuccess: () => setArchiveReason(""),
      },
    );
  }

  const selectedAttempt = selectedReview.data?.review_attempts?.attempt;
  const snapshotDocuments =
    selectedReview.data?.snapshot?.document_ids.map(
      (id) =>
        selectedReview.data?.documents.find((document) => document.id === id)
          ?.filename || "材料信息未返回",
    ) ?? [];
  const reportReady =
    canCreateReport &&
    !archived &&
    selectedChoice !== null &&
    selectedReview.isSuccess &&
    selectedReview.data?.run.id === selectedChoice.runId &&
    selectedAttempt?.id === selectedChoice.attempt.id &&
    isTerminalAttemptState(selectedAttempt?.state ?? "");
  const archiveRecords = detail.archives;

  return (
    <section className="workflow-panel" aria-labelledby="report-archive-title">
      <header className="workflow-panel-head">
        <div>
          <h2 id="report-archive-title">冻结报告与归档</h2>
          <p className="meta">
            报告读取所选审查的冻结内容；浏览器不重新拼装报告。
          </p>
        </div>
        {archived ? (
          <StatusText tone="neutral">
            已归档，
            {detail.archived_at
              ? formatDateTime(detail.archived_at)
              : "时间未报告"}
          </StatusText>
        ) : null}
      </header>
      {!canCreateReport ? (
        <p className="callout">当前身份没有已定义的报告生成权限。</p>
      ) : null}
      {archived ? (
        <p className="callout">
          归档后的案件保留授权读取。所有后续业务写入会被服务端拒绝（HTTP 409）。
        </p>
      ) : null}
      {historyLoading ? (
        <p role="status">正在读取可报告的运行与尝试。</p>
      ) : null}
      {historyError ? (
        <p className="workflow-error" role="alert">
          运行历史没有加载：{errorMessage(historyError)}
        </p>
      ) : null}
      {archive.data ? (
        <p role="status">
          归档记录已追加：{formatDateTime(archive.data.archived_at)}。
        </p>
      ) : null}
      {canCreateReport && !archived ? (
        <form className="workflow-form" onSubmit={onCreateReport}>
          <h3>生成不可变报告版本</h3>
          <ReviewChoiceSelect
            label="冻结运行和尝试"
            choices={choices}
            value={choiceKey}
            onChange={setChoiceKey}
            disabled={choices.length === 0 || createReport.isPending}
          />
          {choices.length === 0 && !historyLoading && !historyError ? (
            <p>没有可报告的冻结尝试；服务未提供的 legacy 输入不会被补造。</p>
          ) : null}
          {selectedReview.isLoading ? (
            <p role="status">正在读取所选尝试的冻结身份。</p>
          ) : null}
          {selectedReview.isError ? (
            <p className="workflow-error" role="alert">
              所选尝试没有加载：{errorMessage(selectedReview.error)}
            </p>
          ) : null}
          {selectedReview.data ? (
            <dl className="execution-dl">
              <div>
                <dt>审查次数</dt>
                <dd>
                  {selectedAttempt ? `第 ${selectedAttempt.ordinal} 次` : "未返回"}
                </dd>
              </div>
              <div>
                <dt>执行状态</dt>
                <dd>
                  <StatusText
                    tone={runTone(
                      selectedAttempt?.state ??
                        selectedReview.data.run.state,
                    )}
                  >
                    {runLabel(
                      selectedAttempt?.state ??
                        selectedReview.data.run.state,
                    )}
                  </StatusText>
                </dd>
              </div>
              <div>
                <dt>材料版本</dt>
                <dd>
                  {snapshotDocuments.join("、") || "未返回"}
                </dd>
              </div>
            </dl>
          ) : null}
          {selectedReview.data && selectedAttempt ? (
            <p className="meta">
              审查的技术信息可由管理员在{" "}
              <Link
                to={`/cases/${encodeURIComponent(caseId)}/reviews/${encodeURIComponent(selectedReview.data.run.id)}?attempt_id=${encodeURIComponent(selectedAttempt.id)}`}
              >
                审查工作区
              </Link>{" "}
              中按需读取。
            </p>
          ) : null}
          {selectedAttempt && !isTerminalAttemptState(selectedAttempt.state) ? (
            <p className="callout">
              所选尝试仍在执行，终态生成报告操作尚不可用。
            </p>
          ) : null}
          {createReport.error ? (
            <p
              ref={reportErrorRef}
              className="workflow-error"
              role="alert"
              tabIndex={-1}
            >
              {errorMessage(createReport.error)}
            </p>
          ) : null}
          {createReport.data ? (
            <p role="status">
              报告已创建，版本 {createReport.data.version}。
            </p>
          ) : null}
          <Button
            aria-label={String(
              createReport.isPending ? "正在生成报告" : "生成冻结报告",
            )}
            type="primary"
            htmlType="submit"
            disabled={!reportReady || createReport.isPending}
          >
            {createReport.isPending ? "正在生成报告" : "生成冻结报告"}
          </Button>
        </form>
      ) : null}

      <section aria-labelledby="report-history-title">
        <h3 id="report-history-title">报告历史</h3>
        {detail.reports.length === 0 ? (
          <p>没有已返回的报告版本。</p>
        ) : (
          <ul className="workflow-record-list">
            {detail.reports.map((report) => (
              <li key={report.id}>
                <header className="workflow-panel-head">
                  <div>
                    <strong>报告版本 {report.version}</strong>
                    <p className="meta">
                      创建于 {formatDateTime(report.created_at)}
                    </p>
                  </div>
                </header>
                {canReadReports ? (
                  <div className="workflow-links">
                    <a
                      type="text"
                      href={paths.report(caseId, report.id, "html")}
                      target="_blank"
                      rel="noreferrer"
                    >
                      读取冻结 HTML
                    </a>
                    <a
                      type="text"
                      href={paths.report(caseId, report.id, "json")}
                      target="_blank"
                      rel="noreferrer"
                    >
                      读取冻结 JSON
                    </a>
                  </div>
                ) : (
                  <p className="meta">当前身份没有报告读取权限。</p>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      {!archived && canArchive ? (
        <form className="workflow-form" onSubmit={onArchive}>
          <h3>归档案件</h3>
          <label className="field">
            归档所依据的报告
            <Select
              aria-label="归档所依据的报告"
              value={archiveReportId || undefined}
              onChange={setArchiveReportId}
              disabled={detail.reports.length === 0 || archive.isPending}
              options={[
                { label: "选择报告版本", value: "" },
                ...detail.reports.map((report) => ({
                  label: `版本 ${report.version}，创建于 ${formatDateTime(report.created_at)}`,
                  value: report.id,
                })),
              ]}
            />
          </label>
          <label className="field">
            归档理由
            <Input.TextArea
              required
              value={archiveReason}
              onChange={(event) => setArchiveReason(event.target.value)}
              disabled={archive.isPending}
            />
          </label>
          {detail.reports.length === 0 ? (
            <p>先生成实际报告版本，再执行归档。</p>
          ) : null}
          {archive.error ? (
            <p
              ref={archiveErrorRef}
              className="workflow-error"
              role="alert"
              tabIndex={-1}
            >
              {errorMessage(archive.error)}
            </p>
          ) : null}
          <Button
            aria-label={String(archive.isPending ? "正在归档" : "归档案件")}
            type="primary"
            htmlType="submit"
            disabled={
              !archiveReportId ||
              !archiveReason.trim() ||
              detail.reports.length === 0 ||
              archive.isPending
            }
          >
            {archive.isPending ? "正在归档" : "归档案件"}
          </Button>
        </form>
      ) : null}
      <section aria-labelledby="archive-history-title">
        <h3 id="archive-history-title">归档记录</h3>
        {archiveRecords.length === 0 ? (
          archived ? (
            <p>
              案件已归档，归档时间{" "}
              {detail.archived_at
                ? formatDateTime(detail.archived_at)
                : "未报告"}
             ，但服务未返回归档记录。
            </p>
          ) : (
            <p>案件尚未归档。</p>
          )
        ) : (
          <ul className="workflow-record-list">
            {archiveRecords.map((record) => (
              <li key={record.id}>
                <strong>归档记录</strong>
                <dl className="execution-dl">
                  <div>
                    <dt>报告版本</dt>
                    <dd>
                      {detail.reports.find((report) => report.id === record.report_id)?.version ?? "未返回"}
                    </dd>
                  </div>
                  <div>
                    <dt>归档理由</dt>
                    <dd>{record.reason}</dd>
                  </div>
                  <div>
                    <dt>归档时间</dt>
                    <dd>{formatDateTime(record.archived_at)}</dd>
                  </div>
                  <div>
                    <dt>案件</dt>
                    <dd>{detail.title}</dd>
                  </div>
                </dl>
              </li>
            ))}
          </ul>
        )}
      </section>
    </section>
  );
}
