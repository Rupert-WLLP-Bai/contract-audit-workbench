import { Button, Input, Select } from "antd";
import { Link } from "react-router";
import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type RefObject,
} from "react";
import { errorMessage } from "../api/http";
import { paths } from "../api/paths";
import { useArchiveCase, useCreateReport, useReview } from "../api/queries";
import type { CaseDetail, ReviewHistoryEntry } from "../api/types";
import {
  caseHref,
  formatDateTime,
  isTerminalAttemptState,
  runLabel,
  runTone,
} from "../domain/review";
import { reviewChoiceForKey, reviewChoices } from "../domain/workflow";
import { ReviewChoiceSelect } from "./review-choice-select";
import { StatusText } from "./status-text";

function choiceRunMatches(
  review: ReturnType<typeof useReview>,
  choice: ReturnType<typeof reviewChoiceForKey>,
): boolean {
  return review.data?.run.id === choice?.runId;
}

function choiceAttemptMatches(
  review: ReturnType<typeof useReview>,
  choice: ReturnType<typeof reviewChoiceForKey>,
): boolean {
  const attempt = review.data?.review_attempts?.attempt;
  return (
    attempt?.id === choice?.attempt.id &&
    isTerminalAttemptState(attempt?.state ?? "")
  );
}

function reviewMatchesChoice(
  review: ReturnType<typeof useReview>,
  choice: ReturnType<typeof reviewChoiceForKey>,
): boolean {
  return (
    review.isSuccess &&
    choiceRunMatches(review, choice) &&
    choiceAttemptMatches(review, choice)
  );
}

function reportPrepared(
  canCreateReport: boolean,
  archived: boolean,
  choice: ReturnType<typeof reviewChoiceForKey>,
  review: ReturnType<typeof useReview>,
): boolean {
  return (
    canCreateReport &&
    !archived &&
    choice !== null &&
    reviewMatchesChoice(review, choice)
  );
}

function documentName(
  review: ReturnType<typeof useReview>["data"],
  id: string,
): string {
  return (
    review?.documents.find((document) => document.id === id)?.filename ||
    "材料信息未返回"
  );
}

function snapshotDocumentNames(
  review: ReturnType<typeof useReview>["data"],
): string[] {
  return review?.snapshot?.document_ids.map((id) => documentName(review, id)) ?? [];
}

function useDefaultChoice(
  choices: ReturnType<typeof reviewChoices>,
  choiceKey: string,
  setChoiceKey: (value: string) => void,
) {
  useEffect(() => {
    if (!choiceKey && choices.length > 0) setChoiceKey(choices[0]?.key ?? "");
  }, [choiceKey, choices, setChoiceKey]);
}

function useDefaultArchiveReport(
  reports: CaseDetail["reports"],
  archiveReportId: string,
  setArchiveReportId: (value: string) => void,
) {
  useEffect(() => {
    if (reports.length > 0 && archiveReportId === "")
      setArchiveReportId(reports[reports.length - 1]?.id ?? "");
  }, [archiveReportId, reports, setArchiveReportId]);
}

function useFocusOnError(
  error: unknown,
  ref: RefObject<HTMLParagraphElement | null>,
) {
  useEffect(() => {
    if (error) ref.current?.focus();
  }, [error, ref]);
}

function reviewArgsOf(choice: ReturnType<typeof reviewChoiceForKey>): {
  runId: string;
  attemptId: string | null;
} {
  return {
    runId: choice?.runId ?? "",
    attemptId: choice?.attempt.id ?? null,
  };
}

function archivedAtOf(
  data: { archived_at: string } | undefined,
): string | null {
  return data?.archived_at ?? null;
}

function ArchivedBadge({
  archived,
  archivedAt,
}: {
  archived: boolean;
  archivedAt: string | null;
}) {
  if (!archived) return null;
  return (
    <StatusText tone="neutral">
      已归档，
      {archivedAt ? formatDateTime(archivedAt) : "时间未报告"}
    </StatusText>
  );
}

function ReviewChoiceSection({
  choices,
  choiceKey,
  onChange,
  disabled,
  historyLoading,
  historyError,
}: {
  choices: ReturnType<typeof reviewChoices>;
  choiceKey: string;
  onChange: (value: string) => void;
  disabled: boolean;
  historyLoading: boolean;
  historyError: unknown;
}) {
  return (
    <>
      <ReviewChoiceSelect
        label="选择审查和执行记录"
        choices={choices}
        value={choiceKey}
        onChange={onChange}
        disabled={choices.length === 0 || disabled}
      />
      {choices.length === 0 && !historyLoading && !historyError ? (
        <p>
          没有可用于生成报告的执行记录。较早的审查如果没有保存当时使用的材料版本记录，不能用于生成报告。
        </p>
      ) : null}
    </>
  );
}

function runStateOf(review: ReturnType<typeof useReview>): string {
  return (
    review.data?.review_attempts?.attempt.state ?? review.data?.run.state ?? ""
  );
}

function ReviewAttemptSummary({
  review,
}: {
  review: ReturnType<typeof useReview>;
}) {
  const attempt = review.data?.review_attempts?.attempt;
  const documents = snapshotDocumentNames(review.data);
  return (
    <dl className="execution-dl">
      <div>
        <dt>审查次数</dt>
        <dd>{attempt ? `第 ${attempt.ordinal} 次` : "未返回"}</dd>
      </div>
      <div>
        <dt>执行状态</dt>
        <dd>
          <StatusText tone={runTone(runStateOf(review))}>
            {runLabel(runStateOf(review))}
          </StatusText>
        </dd>
      </div>
      <div>
        <dt>材料版本</dt>
        <dd>{documents.join("、") || "未返回"}</dd>
      </div>
    </dl>
  );
}

function ReviewTechnicalLink({
  caseId,
  review,
  attempt,
}: {
  caseId: string;
  review: ReturnType<typeof useReview>;
  attempt: { id: string };
}) {
  if (!review.data) return null;
  return (
    <p className="meta">
      审查的技术信息可由管理员在{" "}
      <Link to={caseHref(caseId, review.data.run.id, attempt.id)}>
        审查工作区
      </Link>{" "}
      中按需读取。
    </p>
  );
}

function NonTerminalNotice({
  attempt,
}: {
  attempt: { state: string } | undefined;
}) {
  if (!attempt || isTerminalAttemptState(attempt.state)) return null;
  return (
    <p className="callout">所选尝试仍在执行，终态生成报告操作尚不可用。</p>
  );
}

function SelectedReviewSection({
  review,
  caseId,
}: {
  review: ReturnType<typeof useReview>;
  caseId: string;
}) {
  const attempt = review.data?.review_attempts?.attempt;
  return (
    <>
      {review.isLoading ? (
        <p role="status">正在读取所选执行记录。</p>
      ) : null}
      {review.isError ? (
        <p className="workflow-error" role="alert">
          所选尝试没有加载：{errorMessage(review.error)}
        </p>
      ) : null}
      {review.data ? <ReviewAttemptSummary review={review} /> : null}
      {attempt ? (
        <ReviewTechnicalLink caseId={caseId} review={review} attempt={attempt} />
      ) : null}
      <NonTerminalNotice attempt={attempt} />
    </>
  );
}

function ReportSubmit({ ready, pending }: { ready: boolean; pending: boolean }) {
  return (
    <Button
      aria-label={String(pending ? "正在生成报告" : "生成报告")}
      type="primary"
      htmlType="submit"
      disabled={!ready || pending}
    >
      {pending ? "正在生成报告" : "生成报告"}
    </Button>
  );
}

function ReportCreationForm({
  choices,
  choiceKey,
  onChoiceChange,
  choiceDisabled,
  historyLoading,
  historyError,
  review,
  caseId,
  ready,
  pending,
  error,
  data,
  errorRef,
  onSubmit,
}: {
  choices: ReturnType<typeof reviewChoices>;
  choiceKey: string;
  onChoiceChange: (value: string) => void;
  choiceDisabled: boolean;
  historyLoading: boolean;
  historyError: unknown;
  review: ReturnType<typeof useReview>;
  caseId: string;
  ready: boolean;
  pending: boolean;
  error: unknown;
  data: { version: number } | undefined;
  errorRef: RefObject<HTMLParagraphElement | null>;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
}) {
  return (
    <form className="workflow-form" onSubmit={onSubmit}>
      <h3>生成不可变报告版本</h3>
      <ReviewChoiceSection
        choices={choices}
        choiceKey={choiceKey}
        onChange={onChoiceChange}
        disabled={choiceDisabled}
        historyLoading={historyLoading}
        historyError={historyError}
      />
      <SelectedReviewSection review={review} caseId={caseId} />
      {error ? (
        <p ref={errorRef} className="workflow-error" role="alert" tabIndex={-1}>
          {errorMessage(error)}
        </p>
      ) : null}
      {data ? (
        <p role="status">报告已创建，版本 {data.version}。</p>
      ) : null}
      <ReportSubmit ready={ready} pending={pending} />
    </form>
  );
}

function ReportHistory({
  caseId,
  reports,
  canReadReports,
}: {
  caseId: string;
  reports: CaseDetail["reports"];
  canReadReports: boolean;
}) {
  return (
    <section aria-labelledby="report-history-title">
      <h3 id="report-history-title">报告历史</h3>
      {reports.length === 0 ? (
        <p>没有已返回的报告版本。</p>
      ) : (
        <ul className="workflow-record-list">
          {reports.map((report) => (
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
                    查看 HTML 报告
                  </a>
                  <a
                    type="text"
                    href={paths.report(caseId, report.id, "json")}
                    target="_blank"
                    rel="noreferrer"
                  >
                    查看 JSON 报告
                  </a>
                  <a href={paths.reportEvidenceBundle(caseId, report.id)}>
                    下载可验签证据包（含 PDF 和原件）
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
  );
}

function ArchiveSubmit({
  reportId,
  reason,
  reportCount,
  pending,
}: {
  reportId: string;
  reason: string;
  reportCount: number;
  pending: boolean;
}) {
  return (
    <Button
      aria-label={String(pending ? "正在归档" : "归档案件")}
      type="primary"
      htmlType="submit"
      disabled={!reportId || !reason.trim() || reportCount === 0 || pending}
    >
      {pending ? "正在归档" : "归档案件"}
    </Button>
  );
}

function ArchiveForm({
  reports,
  reportId,
  reason,
  pending,
  error,
  errorRef,
  onReportId,
  onReason,
  onSubmit,
}: {
  reports: CaseDetail["reports"];
  reportId: string;
  reason: string;
  pending: boolean;
  error: unknown;
  errorRef: RefObject<HTMLParagraphElement | null>;
  onReportId: (value: string) => void;
  onReason: (value: string) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
}) {
  return (
    <form className="workflow-form" onSubmit={onSubmit}>
      <h3>归档案件</h3>
      <label className="field">
        归档所依据的报告
        <Select
          aria-label="归档所依据的报告"
          value={reportId || undefined}
          onChange={onReportId}
          disabled={reports.length === 0 || pending}
          options={[
            { label: "选择报告版本", value: "" },
            ...reports.map((report) => ({
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
          value={reason}
          onChange={(event) => onReason(event.target.value)}
          disabled={pending}
        />
      </label>
      {reports.length === 0 ? (
        <p>先生成实际报告版本，再执行归档。</p>
      ) : null}
      {error ? (
        <p ref={errorRef} className="workflow-error" role="alert" tabIndex={-1}>
          {errorMessage(error)}
        </p>
      ) : null}
      <ArchiveSubmit
        reportId={reportId}
        reason={reason}
        reportCount={reports.length}
        pending={pending}
      />
    </form>
  );
}

function archiveReportVersion(
  reports: CaseDetail["reports"],
  reportId: string,
): string | number {
  return reports.find((report) => report.id === reportId)?.version ?? "未返回";
}

function ArchiveHistory({
  records,
  reports,
  title,
  archived,
  archivedAt,
}: {
  records: CaseDetail["archives"];
  reports: CaseDetail["reports"];
  title: string;
  archived: boolean;
  archivedAt: string | null;
}) {
  return (
    <section aria-labelledby="archive-history-title">
      <h3 id="archive-history-title">归档记录</h3>
      {records.length === 0 ? (
        archived ? (
          <p>
            案件已归档，归档时间{" "}
            {archivedAt ? formatDateTime(archivedAt) : "未报告"}
            ，但服务未返回归档记录。
          </p>
        ) : (
          <p>案件尚未归档。</p>
        )
      ) : (
        <ul className="workflow-record-list">
          {records.map((record) => (
            <li key={record.id}>
              <strong>归档记录</strong>
              <dl className="execution-dl">
                <div>
                  <dt>报告版本</dt>
                  <dd>{archiveReportVersion(reports, record.report_id)}</dd>
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
                  <dd>{title}</dd>
                </div>
              </dl>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function PanelStatusMessages({
  canCreateReport,
  archived,
  historyLoading,
  historyError,
  archivedAt,
}: {
  canCreateReport: boolean;
  archived: boolean;
  historyLoading: boolean;
  historyError: unknown;
  archivedAt: string | null;
}) {
  return (
    <>
      {!canCreateReport ? (
        <p className="callout">当前身份没有已定义的报告生成权限。</p>
      ) : null}
      {archived ? (
        <p className="callout">
          归档后的案件保留授权读取。所有后续业务写入会被服务端拒绝（HTTP 409）。
        </p>
      ) : null}
      {historyLoading ? (
        <p role="status">正在读取可用于生成报告的审查和执行记录。</p>
      ) : null}
      {historyError ? (
        <p className="workflow-error" role="alert">
          运行历史没有加载：{errorMessage(historyError)}
        </p>
      ) : null}
      {archivedAt ? (
        <p role="status">
          已保存归档记录：{formatDateTime(archivedAt)}。
        </p>
      ) : null}
    </>
  );
}

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
  const reviewArgs = reviewArgsOf(selectedChoice);
  const selectedReview = useReview(
    caseId,
    reviewArgs.runId,
    reviewArgs.attemptId,
  );
  const reportFormVisible = canCreateReport && !archived;
  const archiveFormVisible = !archived && canArchive;

  useDefaultChoice(choices, choiceKey, setChoiceKey);
  useDefaultArchiveReport(
    detail.reports,
    archiveReportId,
    setArchiveReportId,
  );
  useFocusOnError(createReport.error, reportErrorRef);
  useFocusOnError(archive.error, archiveErrorRef);

  const reportReady = reportPrepared(
    canCreateReport,
    archived,
    selectedChoice,
    selectedReview,
  );

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

  return (
    <section className="workflow-panel" aria-labelledby="report-archive-title">
      <header className="workflow-panel-head">
        <div>
          <h2 id="report-archive-title">审查报告与归档</h2>
          <p className="meta">
            报告保存所选审查当时的内容，生成后不会随材料更新而改变。
          </p>
        </div>
        <ArchivedBadge archived={archived} archivedAt={detail.archived_at} />
      </header>
      <PanelStatusMessages
        canCreateReport={canCreateReport}
        archived={archived}
        historyLoading={historyLoading}
        historyError={historyError}
        archivedAt={archivedAtOf(archive.data)}
      />
      {reportFormVisible ? (
        <ReportCreationForm
          choices={choices}
          choiceKey={choiceKey}
          onChoiceChange={setChoiceKey}
          choiceDisabled={createReport.isPending}
          historyLoading={historyLoading}
          historyError={historyError}
          review={selectedReview}
          caseId={caseId}
          ready={reportReady}
          pending={createReport.isPending}
          error={createReport.error}
          data={createReport.data}
          errorRef={reportErrorRef}
          onSubmit={onCreateReport}
        />
      ) : null}
      <ReportHistory
        caseId={caseId}
        reports={detail.reports}
        canReadReports={canReadReports}
      />
      {archiveFormVisible ? (
        <ArchiveForm
          reports={detail.reports}
          reportId={archiveReportId}
          reason={archiveReason}
          pending={archive.isPending}
          error={archive.error}
          errorRef={archiveErrorRef}
          onReportId={setArchiveReportId}
          onReason={setArchiveReason}
          onSubmit={onArchive}
        />
      ) : null}
      <ArchiveHistory
        records={detail.archives}
        reports={detail.reports}
        title={detail.title}
        archived={archived}
        archivedAt={detail.archived_at}
      />
    </section>
  );
}
