import { Tabs } from "antd";
import { Collapse } from "antd";
import { Select } from "antd";
import { Button } from "antd";
import { useState } from "react";
import { Link, useSearchParams } from "react-router";
import { errorMessage } from "../api/http";
import { paths } from "../api/paths";
import { useReview, useWorkbench } from "../api/queries";
import type { Finding, ReviewDocument, WorkbenchRun } from "../api/types";
import { PageStatus } from "../components/page-status";
import {
  caseHref,
  decisionLabel,
  formatDateTime,
  parseLabel,
  relationLabel,
  runLabel,
} from "../domain/review";
import {
  evidenceSourceMatches,
  resolveAnchor,
  segmentText,
} from "../domain/text";
import "./compare-page.css";

function latestFirst(left: WorkbenchRun, right: WorkbenchRun): number {
  return (
    right.updated_at.localeCompare(left.updated_at) ||
    right.id.localeCompare(left.id)
  );
}

function primaryContract(documents: ReviewDocument[]): ReviewDocument | null {
  let selected = documents[0] ?? null;
  for (const document of documents) {
    if (document.role !== "draft_contract") continue;
    if (!document.superseded) return document;
    selected = document;
  }
  return selected;
}

function RunSelect({
  label,
  value,
  runs,
  onChange,
}: {
  label: string;
  value: WorkbenchRun | null;
  runs: WorkbenchRun[];
  onChange: (id: string) => void;
}) {
  return (
    <label className="field compare-run-select">
      {label}
      <Select<string>
        aria-label={String(label)}
        value={String(value?.id ?? "")}
        labelRender={() =>
          value
            ? `审查记录 ${runs.findIndex((run) => run.id === value.id) + 1}，${runLabel(value.state)}，${formatDateTime(value.updated_at)}`
            : "没有可选运行"
        }
        onChange={(selectedValue) => onChange(selectedValue)}
        options={[
          ...(runs.length === 0 ? [{ value: "", label: "没有可选运行" }] : []),
          ...runs.map((run, index) => ({
            value: String(run.id),
            label: `审查记录 ${index + 1}，${runLabel(run.state)}，${formatDateTime(run.updated_at)}`,
          })),
        ]}
      />
    </label>
  );
}

function MaterialPane({
  label,
  caseId,
  run,
  documents,
  selected,
  onSelect,
  finding,
  error,
  loading,
}: {
  label: string;
  caseId: string;
  run: WorkbenchRun | null;
  documents: ReviewDocument[];
  selected: ReviewDocument | null;
  onSelect: (id: string) => void;
  finding: Finding | null;
  error: unknown;
  loading: boolean;
}) {
  const anchor = selected
    ? finding?.evidence.find((item) =>
        evidenceSourceMatches(
          item,
          selected.id,
          selected.parsed_document,
          selected.parsed_document_id,
        ),
      )
    : null;
  const resolved =
    selected && anchor
      ? resolveAnchor(
          selected.text,
          anchor,
          selected.parsed_document,
          selected.parsed_document_id,
        )
      : null;
  const segments =
    selected?.text == null
      ? []
      : segmentText(
          selected.text,
          resolved?.kind === "highlight" ? resolved : null,
          [],
        );
  return (
    <article className="compare-material-pane">
      <header className="compare-material-head">
        <span>{label}</span>
        <span>{run ? runLabel(run.state) : "未选择运行"}</span>
      </header>
      <label className="field">
        材料
        <Select<string>
          aria-label={"材料"}
          value={String(selected?.id ?? "")}
          onChange={(selectedValue) => onSelect(selectedValue)}
          disabled={documents.length === 0}
          options={[
            ...(documents.length === 0
              ? [{ value: "", label: "此运行没有返回材料" }]
              : []),
            ...documents.map((document) => ({
              value: String(document.id),
              label: String(document.filename ?? ""),
            })),
          ]}
        />
      </label>
      {selected ? (
        <>
          <Collapse
            className="compare-source"
            items={[
              {
                key: "content",
                label: "版本与来源，" + parseLabel(selected.parse_status ?? ""),
                children: (
                  <>
                    <dl className="compare-material-meta">
                      <div>
                        <dt>材料名称</dt>
                        <dd>{selected.filename}</dd>
                      </div>
                      <div>
                        <dt>解析状态</dt>
                        <dd>{parseLabel(selected.parse_status)}</dd>
                      </div>
                      <div>
                        <dt>审查状态</dt>
                        <dd>{run ? runLabel(run.state) : "未报告"}</dd>
                      </div>
                      <div>
                        <dt>上传时间</dt>
                        <dd>{formatDateTime(selected.created_at)}</dd>
                      </div>
                    </dl>
                    <a href={paths.documentOriginal(caseId, selected.id)}>
                      读取原件
                    </a>
                  </>
                ),
              },
            ]}
          />
          <div
            className="compare-document-text"
            aria-label={`${label}材料解析正文`}
            data-document-version-id={selected.id}
          >
            <h2>{selected.filename.replace(/\.[^.]+$/, "")}</h2>
            {selected.text === null ? (
              <p className="compare-unavailable">
                该运行没有可展示的解析正文。
              </p>
            ) : (
              <pre>
                {segments.map((segment) =>
                  segment.highlighted === "evidence" ? (
                    <mark key={segment.start}>{segment.text}</mark>
                  ) : (
                    <span key={segment.start}>{segment.text}</span>
                  ),
                )}
              </pre>
            )}
          </div>
          {resolved && resolved.kind !== "highlight" ? (
            <p className="compare-unavailable">{resolved.reason}</p>
          ) : null}
        </>
      ) : (
        <p className="compare-unavailable">选择审查记录后，查看当时使用的材料。</p>
      )}
      {loading ? (
        <p role="status" className="compare-query-state">
          正在读取所选审查的记录…
        </p>
      ) : null}
      {error ? (
        <p role="alert" className="compare-query-error">
          无法读取此运行：{errorMessage(error)}
        </p>
      ) : null}
    </article>
  );
}

export function ComparePage() {
  const snapshotQuery = useWorkbench("week");
  const [params, setParams] = useSearchParams();
  const caseSelection = params.get("case_id") ?? "";
  const leftRunSelection = params.get("left_run_id") ?? "";
  const rightRunSelection = params.get("right_run_id") ?? "";
  const [leftDocumentSelection, setLeftDocumentSelection] = useState("");
  const [rightDocumentSelection, setRightDocumentSelection] = useState("");
  const [bottomTab, setBottomTab] = useState("修订证据");
  const [findingSelection, setFindingSelection] = useState("");

  const snapshot = snapshotQuery.data;
  const selectedCase =
    snapshot?.cases.find((item) => item.id === caseSelection) ??
    snapshot?.cases.find(
      (item) =>
        snapshot.runs.filter((run) => run.case_id === item.id).length > 1,
    ) ??
    snapshot?.cases[0] ??
    null;
  const caseRuns =
    selectedCase && snapshot
      ? snapshot.runs
          .filter((run) => run.case_id === selectedCase.id)
          .sort(latestFirst)
      : [];
  const leftRun =
    caseRuns.find((run) => run.id === leftRunSelection) ?? caseRuns[0] ?? null;
  const rightRun =
    caseRuns.find((run) => run.id === rightRunSelection) ??
    caseRuns[1] ??
    caseRuns[0] ??
    null;
  const leftReview = useReview(
    selectedCase?.id ?? "",
    leftRun?.id ?? "",
    leftRun?.attempt_id,
  );
  const rightReview = useReview(
    selectedCase?.id ?? "",
    rightRun?.id ?? "",
    rightRun?.attempt_id,
  );
  if (snapshotQuery.isLoading)
    return <PageStatus kind="loading" title="正在读取可比较的真实审查记录" />;
  if (snapshotQuery.isError || !snapshot)
    return (
      <PageStatus
        kind="error"
        title="版本对照数据没有加载"
        detail={errorMessage(snapshotQuery.error)}
        action={
          <Button
            aria-label={"重试"}
            type="primary"
            htmlType="button"
            onClick={() => void snapshotQuery.refetch()}
          >
            重试
          </Button>
        }
      />
    );

  const leftDocuments = leftReview.data?.documents ?? [];
  const rightDocuments = rightReview.data?.documents ?? [];
  const leftDocument =
    leftDocuments.find((item) => item.id === leftDocumentSelection) ??
    primaryContract(leftDocuments);
  const rightDocument =
    rightDocuments.find((item) => item.id === rightDocumentSelection) ??
    primaryContract(rightDocuments);
  const revisions = leftReview.data?.revisions ?? [];
  const comparisonsLinked = Boolean(
    rightRun && leftReview.data?.previous_run_id === rightRun.id,
  );
  const bottomTabs = ["修订证据", "规则依据", "执行记录", "模型用量"];
  const findings = leftReview.data?.findings ?? [];
  const finding = findings.find((item) => item.id === findingSelection) ?? null;
  const decidedCount = findings.filter(
    (item) => item.decisions.length > 0,
  ).length;
  function chooseRun(side: "left_run_id" | "right_run_id", id: string) {
    const next = new URLSearchParams(params);
    next.set(side, id);
    setParams(next);
    if (side === "left_run_id") {
      setLeftDocumentSelection("");
      setFindingSelection("");
    } else setRightDocumentSelection("");
  }
  function chooseFinding(item: Finding) {
    setFindingSelection(item.id);
    const source = item.evidence.find((anchor) =>
      leftDocuments.some((document) =>
        evidenceSourceMatches(
          anchor,
          document.id,
          document.parsed_document,
          document.parsed_document_id,
        ),
      ),
    );
    if (source) setLeftDocumentSelection(source.document_version_id);
  }

  return (
    <section className="page compare-page">
      <header className="compare-page-head">
        <div>
          <strong>{selectedCase?.title ?? "版本对照"}</strong>
          <span>{selectedCase?.profile?.number ?? "合同编号未提供"}</span>
        </div>
        <nav className="compare-page-links" aria-label="版本对照导航">
          <Link to="/compare">全部合同</Link>
          <Link to={selectedCase ? `/cases/${selectedCase.id}` : "/cases"}>
            案件详情
          </Link>
        </nav>
      </header>
      <div className="compare-workspace">
        <aside className="compare-risk-panel" aria-label="审查结果">
          <header>
            <h2>审查结果</h2>
            <span>{findings.length} 项实际发现</span>
          </header>
          <div className="compare-risk-counts">
            {(["high", "medium", "low", "info"] as const).map(
              (severity, index) => (
                <span key={severity} data-severity={severity}>
                  {
                    findings.filter((item) => item.severity === severity)
                      .length
                  }
                  <small>{["高风险", "中风险", "低风险", "提示"][index]}</small>
                </span>
              ),
            )}
          </div>
          <p className="compare-risk-note">
            请以当前版本使用的规则、模型建议和审查人员的判断为准。
          </p>
          <div className="compare-risk-records">
            {findings.map((item) => (
              <article
                key={item.id}
                data-severity={item.severity ?? "unrated"}
                data-selected={item.id === finding?.id}
              >
                <Button
                  aria-label={`${item.claim}，${item.origin === "rule" ? "规则核对" : "模型建议"}，${relationLabel(item.relation)}`}
                  htmlType="button"
                  onClick={() => chooseFinding(item)}
                  aria-pressed={item.id === finding?.id}
                >
                  <strong>{item.claim}</strong>
                  <span>
                    {item.origin === "rule" ? "规则核对" : "模型建议"}，
                    {relationLabel(item.relation)}
                  </span>
                </Button>
                {item.id === finding?.id ? (
                  <div className="compare-risk-detail">
                    {item.evidence.map((anchor) => (
                      <blockquote key={anchor.id}>
                        {anchor.quote}
                        <small>
                          {leftDocuments.find((document) => document.id === anchor.document_version_id)?.filename ?? "引用来源未返回"}
                        </small>
                      </blockquote>
                    ))}
                    {item.severity_source ? (
                      <p>
                        依据版本 {item.severity_source.policy_version}，
                        {item.severity_source.clause}
                      </p>
                    ) : null}
                    <Link
                      to={`/cases/${selectedCase?.id}/reviews/${leftRun?.id}?finding=${item.id}`}
                    >
                      打开原文与审查判断
                    </Link>
                  </div>
                ) : null}
              </article>
            ))}
            {findings.length === 0 ? (
              <p className="compare-unavailable">
                此运行没有返回发现；不代表合同已批准。
              </p>
            ) : null}
          </div>
        </aside>
        <div className="compare-reading-area">
          <div className="compare-reading-tabs">
            <strong>文档审查</strong>
            <span>双栏对比</span>
            <Link
              to={
                selectedCase && leftRun
                  ? `/cases/${selectedCase.id}/reviews/${leftRun.id}`
                  : "/review"
              }
            >
              打开审查与决定
            </Link>
          </div>
          <div className="compare-selectors">
            <RunSelect
              label="当前版本"
              value={leftRun}
              runs={caseRuns}
              onChange={(id) => chooseRun("left_run_id", id)}
            />
            <RunSelect
              label="历史版本"
              value={rightRun}
              runs={caseRuns}
              onChange={(id) => chooseRun("right_run_id", id)}
            />
          </div>
          <ol className="compare-chain" aria-label="对照链路">
            <li data-state={leftRun ? "filled" : "open"}>
              <span className="compare-chain-mark" aria-hidden="true" />
              <span className="compare-chain-label">当前版本</span>
              <strong>{leftRun ? runLabel(leftRun.state) : "未选择"}</strong>
            </li>
            <li data-state={rightRun ? "filled" : "open"}>
              <span className="compare-chain-mark" aria-hidden="true" />
              <span className="compare-chain-label">历史版本</span>
              <strong>{rightRun ? runLabel(rightRun.state) : "未选择"}</strong>
            </li>
            <li data-state={findings.length > 0 ? "filled" : "open"}>
              <span className="compare-chain-mark" aria-hidden="true" />
              <span className="compare-chain-label">实际发现</span>
              <strong>{findings.length}</strong>
            </li>
            <li data-state={decidedCount > 0 ? "filled" : "open"}>
              <span className="compare-chain-mark" aria-hidden="true" />
              <span className="compare-chain-label">已有审查判断</span>
              <strong>{decidedCount}</strong>
            </li>
          </ol>
          {selectedCase && caseRuns.length === 0 ? (
            <div className="compare-empty">
              <p>没有获取到该案件的审查记录。</p>
              <Link to={`/cases/${selectedCase.id}`}>打开案件并发起审查</Link>
            </div>
          ) : null}
          <div className="compare-materials">
            <MaterialPane
              label="当前版本"
              caseId={selectedCase?.id ?? ""}
              run={leftRun}
              documents={leftDocuments}
              selected={leftDocument}
              onSelect={setLeftDocumentSelection}
              finding={finding}
              error={leftReview.isError ? leftReview.error : null}
              loading={leftReview.isFetching}
            />
            <MaterialPane
              label="历史版本"
              caseId={selectedCase?.id ?? ""}
              run={rightRun}
              documents={rightDocuments}
              selected={rightDocument}
              onSelect={setRightDocumentSelection}
              finding={null}
              error={rightReview.isError ? rightReview.error : null}
              loading={rightReview.isFetching}
            />
          </div>
        </div>
      </div>
      <section
        className="compare-results"
        aria-labelledby="compare-results-title"
      >
        <header>
          <div>
            <h2 id="compare-results-title">审查与运行对照</h2>
          </div>
          <Tabs
            activeKey={bottomTab}
            onChange={(key) => {
              const selectedTab = bottomTabs.find((tab) => tab === key);
              if (selectedTab) setBottomTab(selectedTab);
            }}
            items={bottomTabs.map((label) => ({ key: label, label }))}
          />
        </header>
        {bottomTab === "修订证据" ? (
          <div className="compare-result-body">
            {!comparisonsLinked ? (
              <p className="compare-unavailable">
                当前运行未将所选历史运行声明为前序；两份材料仍可阅读，不能据此推断风险变化。
              </p>
            ) : leftReview.data?.revision_comparison_reason ? (
              <p className="compare-unavailable">
                服务端没有生成修订对照：
                {leftReview.data.revision_comparison_reason}
              </p>
            ) : revisions.length === 0 ? (
              <p className="compare-unavailable">
                所选运行没有返回可比较的修订线索。
              </p>
            ) : (
              <ul className="compare-revision-list">
                {revisions.map((revision, index) => (
                  <li
                    key={`${revision.previous_finding_id ?? "prior"}-${revision.finding_id ?? "current"}-${index}`}
                  >
                    <span>{revision.mark}</span>
                    <div>
                      <strong>
                        {revision.domain}，{revision.relation}
                      </strong>
                      <p>{revision.claim}</p>
                      <small>
                        旧决定{" "}
                        {revision.prior_decision
                          ? decisionLabel(revision.prior_decision.action)
                          : "未报告"}
                      </small>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </div>
        ) : bottomTab === "规则依据" ? (
          <div className="compare-result-columns">
            {[
              {
                label: "左侧运行",
                evaluations: leftReview.data?.evaluations ?? [],
              },
              {
                label: "右侧运行",
                evaluations: rightReview.data?.evaluations ?? [],
              },
            ].map((side) => (
              <section key={side.label}>
                <h3>{side.label}</h3>
                {side.evaluations.length === 0 ? (
                  <p className="compare-unavailable">
                    没有获取到这次审查的规则检查记录。
                  </p>
                ) : (
                  <ul className="compare-fact-list">
                    {side.evaluations.map((item) => (
                      <li key={item.id}>
                        <strong>
                          {item.domain}，{item.relation}
                        </strong>
                        <span>
                          规则 {item.rule_id}，v{item.rule_version}
                        </span>
                        <p>{item.reason}</p>
                      </li>
                    ))}
                  </ul>
                )}
              </section>
            ))}
          </div>
        ) : bottomTab === "执行记录" ? (
          <div className="compare-result-columns">
            {[
              { label: "左侧运行", review: leftReview.data },
              { label: "右侧运行", review: rightReview.data },
            ].map((side) => (
              <section key={side.label}>
                <h3>{side.label}</h3>
                {side.review?.review_attempts ? (
                  <>
                    <p>
                      当前第 {side.review.review_attempts.attempt.ordinal} 次审查，
                      {runLabel(side.review.review_attempts.attempt.state)}
                    </p>
                    <p>
                      尝试数 {side.review.review_attempts.attempts.length}，事件数{" "}
                      不可用
                    </p>
                    <ol className="compare-attempt-list">
                      {side.review.review_attempts.attempts.map((attempt) => (
                        <li key={attempt.id}>
                          第 {attempt.ordinal} 次，
                          {runLabel(attempt.state)}，
                          {attempt.finished_at
                            ? formatDateTime(attempt.finished_at)
                            : "结束时间未报告"}
                        </li>
                      ))}
                    </ol>
                    <p>
                      事件详情可由管理员在{" "}
                      <Link
                        to={caseHref(side.review.run.case_id, side.review.run.id, side.review.review_attempts.attempt.id)}
                      >
                        审查工作区
                      </Link>{" "}
                      中按需读取。
                    </p>
                  </>
                ) : (
                  <p className="compare-unavailable">
                    此运行没有返回执行记录。
                  </p>
                )}
              </section>
            ))}
          </div>
        ) : (
          <div className="compare-result-columns">
            {[
              { label: "左侧运行", review: leftReview.data },
              { label: "右侧运行", review: rightReview.data },
            ].map((side) => (
              <section key={side.label}>
                <h3>{side.label}</h3>
                <dl className="compare-usage-grid">
                  <div>
                    <dt>模型调用</dt>
                    <dd>不可用</dd>
                  </div>
                  <div>
                    <dt>输入 tokens</dt>
                    <dd>不可用</dd>
                  </div>
                  <div>
                    <dt>输出 tokens</dt>
                    <dd>不可用</dd>
                  </div>
                  <div>
                    <dt>总 tokens</dt>
                    <dd>不可用</dd>
                  </div>
                  <div>
                    <dt>费用</dt>
                    <dd>不可用</dd>
                  </div>
                </dl>
                {side.review?.review_attempts ? (
                  <p>
                    模型用量可由管理员在{" "}
                    <Link
                      to={caseHref(side.review.run.case_id, side.review.run.id, side.review.review_attempts.attempt.id)}
                    >
                      审查工作区
                    </Link>{" "}
                    中按需读取。
                  </p>
                ) : null}
              </section>
            ))}
          </div>
        )}
      </section>
    </section>
  );
}
