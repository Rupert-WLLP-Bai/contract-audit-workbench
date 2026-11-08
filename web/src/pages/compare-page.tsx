// File metadata (manifest mirror):
// Purpose: Compares two selected frozen review runs and their primary contract/material snapshots, showing matching and changed findings with source identities rather than recomputing outcomes.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-08
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

import { Tabs } from "antd";
import { Collapse } from "antd";
import { Select } from "antd";
import { Button } from "antd";
import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { errorMessage } from "../api/http";
import { getReview } from "../api/endpoints";
import { paths } from "../api/paths";
import { useReview, useWorkbench } from "../api/queries";
import type { EvidenceAnchor, Finding, ReviewDocument, ReviewWorkspace, WorkbenchRun } from "../api/types";
import { PageStatus } from "../components/page-status";
import { TechnicalDetail } from "../components/technical-detail";
import { InvalidTimeDetail } from "../components/invalid-time-detail";
import { businessCapabilityLabel, businessDomainLabel } from "../domain/business-labels";
import { caseIdentityDetails } from "../domain/case-identity";
import { preciseTime } from "../domain/time";
import {
  caseHref,
  decisionLabel,
  originLabel,
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

// A missing attempt is resolved once from the authorized workspace. Exact
// attempts keep the normal review polling; legacy run-only reads never follow
// a later retry implicitly.
function useCompareReview(caseId: string, runId: string, attemptId: string | null, invalid: boolean) {
  const exact = useReview(caseId, !invalid && attemptId ? runId : "", attemptId);
  const runOnly = useQuery({
    queryKey: ["cases", caseId, "compare-run-only", runId],
    queryFn: ({ signal }) => getReview(caseId, runId, null, signal),
    enabled: !!caseId && !!runId && !attemptId && !invalid,
    staleTime: 0,
    refetchOnMount: "always",
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    retry: false,
  });
  return attemptId && !invalid ? {
    ...exact,
    data: exact.isError || !exact.isFetchedAfterMount ? undefined : exact.data,
  } : {
    ...runOnly,
    data: runOnly.isFetching || runOnly.isError ? undefined : runOnly.data,
  };
}

type MaterialChoice = { key: string; document: ReviewDocument; parsedId: string | null; originalOnly: boolean };

function materialKey(documentId: string, parsedId: string | null): string {
  return JSON.stringify([documentId, parsedId]);
}

// Workspace also contains previous-run documents for revision citations. Only
// the intersection of this run's frozen documents and this attempt's parse
// identities can supply a parsed body on this side.
function materialChoices(workspace: ReviewWorkspace | undefined): { items: MaterialChoice[]; warning: string | null } {
  if (!workspace) return { items: [], warning: null };
  const ids = workspace.snapshot?.document_ids ?? [];
  if (ids.length === 0) return { items: [], warning: workspace.documents.length ? "未报告可验证的冻结材料身份，无法确认本侧材料。" : null };
  const allowed = new Set(ids);
  const parsed = new Set(workspace.review_attempts?.attempt.parsed_document_ids ?? []);
  const byDocument = new Map<string, ReviewDocument[]>();
  for (const document of workspace.documents) {
    if (allowed.has(document.id)) byDocument.set(document.id, [...(byDocument.get(document.id) ?? []), document]);
  }
  const items: MaterialChoice[] = [];
  let conflict = false;
  for (const documents of byDocument.values()) {
    const verified = new Map<string, ReviewDocument[]>();
    for (const document of documents) {
      const metadata = document.parsed_document;
      const parsedId = document.parsed_document_id ?? metadata?.id ?? null;
      if (metadata && (metadata.document_id !== document.id || (document.parsed_document_id && metadata.id !== document.parsed_document_id))) {
        conflict = true;
        continue;
      }
      if (parsedId && parsed.has(parsedId)) verified.set(parsedId, [...(verified.get(parsedId) ?? []), document]);
    }
    let count = 0;
    for (const [parsedId, matches] of verified) {
      if (matches.length !== 1) { conflict = true; continue; }
      const document = matches[0]!;
      items.push({ key: materialKey(document.id, parsedId), document, parsedId, originalOnly: false });
      count += 1;
    }
    if (count === 0) {
      const document = documents[0]!;
      items.push({
        key: materialKey(document.id, null), parsedId: null, originalOnly: true,
        document: { ...document, parsed_document_id: null, parsed_document: null, text: null, clauses: [], parse_status: "unknown", parse_error: null },
      });
    }
  }
  return { items, warning: conflict ? "部分材料的解析身份有冲突，无法确认对应正文。" : null };
}

function matchesMaterialAnchor(choice: MaterialChoice, anchor: EvidenceAnchor): boolean {
  const document = choice.document;
  return evidenceSourceMatches(anchor, document.id, document.parsed_document, document.parsed_document_id);
}

function selectedRunState(workspace: ReviewWorkspace | undefined): string | null {
  if (!workspace) return null;
  const attempt = workspace.review_attempts?.attempt;
  if (!attempt) return workspace.review_attempts_explicit_null ? workspace.run.state : null;
  const runState = workspace.run.state;
  if (runState === attempt.state ||
      (["completed", "awaiting_human"].includes(runState) && ["completed", "awaiting_human"].includes(attempt.state))) return runState;
  return null;
}

function primaryContract(documents: MaterialChoice[]): MaterialChoice | null {
  let selected = documents[0] ?? null;
  for (const choice of documents) {
    if (choice.document.role !== "draft_contract") continue;
    if (!choice.document.superseded) return choice;
    selected = choice;
  }
  return selected;
}

function revisionMarkLabel(mark: string): string {
  return ["持续", "新增", "消除", "未能判断", "保留", "未分类"].includes(mark)
    ? mark
    : "修订类型待核对";
}

function findingOriginLabel(origin: string): string {
  return origin === "rule" ? "规则核对" : originLabel(origin);
}

function RunSelect({
  label,
  value,
  runs,
  attemptId,
  onChange,
}: {
  label: string;
  value: WorkbenchRun | null;
  runs: WorkbenchRun[];
  attemptId: string | null;
  onChange: (id: string) => void;
}) {
  return (
    <div className="field compare-run-select">
      <label>{label}
      <Select<string>
        aria-label={String(label)}
        value={String(value?.id ?? "")}
        labelRender={() =>
          value
            ? `审查当前状态 ${runLabel(value.state)} · 更新时间 ${preciseTime(value.updated_at)}`
            : "没有可选运行"
        }
        onChange={(selectedValue) => onChange(selectedValue)}
        virtual={false}
        optionRender={(option) => <div style={{ whiteSpace: "normal", overflowWrap: "anywhere" }}>{option.label}</div>}
        styles={{ popup: { listItem: { whiteSpace: "normal" } } }}
        options={[
          ...(runs.length === 0 ? [{ value: "", label: "没有可选运行" }] : []),
          ...runs.map((run) => ({
            value: String(run.id),
            label: `审查当前状态 ${runLabel(run.state)} · 审查更新时间 ${preciseTime(run.updated_at)} · 审查标识 ${run.id} · 最新执行标识 ${run.attempt_id ?? "未记录"}`,
          })),
        ]}
      />
      </label>
      {value ? <div className="meta caw-wrap">
        <InvalidTimeDetail label="审查更新时间" value={value.updated_at} />
        <TechnicalDetail label="审查标识" value={value.id} />
        <TechnicalDetail label="执行标识" value={attemptId} />
        <TechnicalDetail label="审查状态编码" value={value.state} />
      </div> : null}
    </div>
  );
}

function MaterialPane({
  label,
  caseId,
  run,
  state,
  documents,
  warning,
  selected,
  onSelect,
  finding,
  error,
  loading,
}: {
  label: string;
  caseId: string;
  run: WorkbenchRun | null;
  state: string | null;
  documents: MaterialChoice[];
  warning: string | null;
  selected: MaterialChoice | null;
  onSelect: (id: string) => void;
  finding: Finding | null;
  error: unknown;
  loading: boolean;
}) {
  const selectedDocument = selected?.document ?? null;
  const anchor = selected
    ? finding?.evidence.find((item) => matchesMaterialAnchor(selected, item) &&
        documents.filter((choice) => matchesMaterialAnchor(choice, item)).length === 1)
    : null;
  const ambiguous = selected && finding?.evidence.some((item) => matchesMaterialAnchor(selected, item) &&
    documents.filter((choice) => matchesMaterialAnchor(choice, item)).length > 1);
  const resolved =
    selectedDocument && anchor
      ? resolveAnchor(
          selectedDocument.text,
          anchor,
          selectedDocument.parsed_document,
          selectedDocument.parsed_document_id,
        )
      : null;
  const segments =
    selectedDocument?.text == null
      ? []
      : segmentText(
          selectedDocument.text,
          resolved?.kind === "highlight" ? resolved : null,
          [],
        );
  return (
    <article className="compare-material-pane">
      <header className="compare-material-head">
        <span>{label}</span>
        <span>{state ? runLabel(state) : run ? "状态未读取" : "未选择运行"}</span>
      </header>
      <label className="field">
        材料
        <Select<string>
          aria-label={"材料"}
          value={selected?.key ?? ""}
          onChange={(selectedValue) => onSelect(selectedValue)}
          disabled={documents.length === 0}
          options={[
            ...(documents.length === 0
              ? [{ value: "", label: "此运行没有返回材料" }]
              : []),
            ...documents.map((choice) => ({
              value: choice.key,
              label: `${choice.document.filename}${choice.originalOnly ? "（仅原件，解析身份未确认）" :
                documents.filter((other) => other.document.filename === choice.document.filename).length > 1
                  ? ` · 材料标识 ${choice.document.id} · 解析标识 ${choice.parsedId}` : ""}`,
            })),
          ]}
          virtual={false}
          optionRender={(option) => <div style={{ whiteSpace: "normal", overflowWrap: "anywhere" }}>{option.label}</div>}
          styles={{ popup: { listItem: { whiteSpace: "normal" } } }}
        />
      </label>
      {warning ? <p role="status" className="compare-unavailable">{warning}</p> : null}
      {selected && selectedDocument ? (
        <>
          <Collapse
            className="compare-source"
            items={[
              {
                key: "content",
                label: "版本与来源，" + parseLabel(selectedDocument.parse_status ?? ""),
                children: (
                  <>
                    <dl className="compare-material-meta">
                      <div>
                        <dt>材料名称</dt>
                        <dd>{selectedDocument.filename}</dd>
                      </div>
                      <div>
                        <dt>解析状态</dt>
                        <dd>{parseLabel(selectedDocument.parse_status)}</dd>
                      </div>
                      <div>
                        <dt>审查状态</dt>
                        <dd>{state ? runLabel(state) : "状态未读取"}</dd>
                      </div>
                      <div>
                        <dt>上传时间</dt>
                        <dd>{preciseTime(selectedDocument.created_at)}<InvalidTimeDetail value={selectedDocument.created_at} label="上传时间" /></dd>
                      </div>
                    </dl>
                    <TechnicalDetail
                      label="材料技术详情"
                      value={JSON.stringify({
                        document_version_id: selectedDocument.id,
                        document_digest: selectedDocument.digest,
                        parsed_document_id: selectedDocument.parsed_document_id,
                        parsed_document_metadata_id: selectedDocument.parsed_document?.id ?? null,
                        parse_status: selectedDocument.parse_status,
                        run_state: state,
                      })}
                    />
                    <a href={paths.documentOriginal(caseId, selectedDocument.id)}>
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
            data-document-version-id={selectedDocument.id}
            data-parsed-document-id={selected.parsedId ?? undefined}
          >
            <h2>{selectedDocument.filename.replace(/\.[^.]+$/, "")}</h2>
            {selectedDocument.text === null ? (
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
          {selected.originalOnly ? <p className="compare-unavailable">未能确认本次执行的解析身份，仅可读取材料原件。</p> : null}
          {ambiguous ? <p className="compare-unavailable">引用未区分解析版本，无法确定对应正文。</p> : null}
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
  const leftAttemptSelection = params.get("left_attempt_id");
  const rightAttemptSelection = params.get("right_attempt_id");
  const leftLegacy = params.get("left_run_only") === "1";
  const rightLegacy = params.get("right_run_only") === "1";
  const leftInvalid = (params.has("left_run_id") && !leftRunSelection) || (params.has("left_attempt_id") && !leftAttemptSelection) ||
    (params.has("left_run_only") && !leftLegacy) || (leftLegacy && params.has("left_attempt_id"));
  const rightInvalid = (params.has("right_run_id") && !rightRunSelection) || (params.has("right_attempt_id") && !rightAttemptSelection) ||
    (params.has("right_run_only") && !rightLegacy) || (rightLegacy && params.has("right_attempt_id"));
  const [leftDocumentSelection, setLeftDocumentSelection] = useState<{ scope: string; key: string } | null>(null);
  const [rightDocumentSelection, setRightDocumentSelection] = useState<{ scope: string; key: string } | null>(null);
  const [bottomTab, setBottomTab] = useState("修订证据");
  const [findingSelection, setFindingSelection] = useState("");

  const snapshot = snapshotQuery.data;
  const selectedCase = params.has("case_id")
    ? snapshot?.cases.find((item) => item.id === caseSelection) ?? null
    : snapshot?.cases.find((item) => snapshot.runs.filter((run) => run.case_id === item.id).length > 1) ?? snapshot?.cases[0] ?? null;
  const caseRuns =
    selectedCase && snapshot
      ? snapshot.runs
          .filter((run) => run.case_id === selectedCase.id)
          .sort(latestFirst)
      : [];
  const leftRun = params.has("left_run_id") ? caseRuns.find((run) => run.id === leftRunSelection) ?? null : caseRuns[0] ?? null;
  const rightRun = params.has("right_run_id") ? caseRuns.find((run) => run.id === rightRunSelection) ?? null : caseRuns[1] ?? caseRuns[0] ?? null;
  const leftAttemptId = leftAttemptSelection ?? (leftLegacy ? null : leftRun?.attempt_id ?? null);
  const rightAttemptId = rightAttemptSelection ?? (rightLegacy ? null : rightRun?.attempt_id ?? null);
  const leftReview = useCompareReview(
    selectedCase?.id ?? "",
    leftRun?.id ?? "",
    leftAttemptId,
    leftInvalid,
  );
  const rightReview = useCompareReview(
    selectedCase?.id ?? "",
    rightRun?.id ?? "",
    rightAttemptId,
    rightInvalid,
  );
  const leftLegacyChanged = leftLegacy && !!leftReview.data?.review_attempts;
  const rightLegacyChanged = rightLegacy && !!rightReview.data?.review_attempts;
  const leftIdentityUnknown = !!leftReview.data && !leftReview.data.review_attempts && !leftReview.data.review_attempts_explicit_null;
  const rightIdentityUnknown = !!rightReview.data && !rightReview.data.review_attempts && !rightReview.data.review_attempts_explicit_null;
  function identityMismatch(workspace: typeof leftReview.data, run: WorkbenchRun | null, attemptId: string | null) {
    if (!workspace) return false;
    const attempt = workspace.review_attempts?.attempt;
    return !run || workspace.run.id !== run.id || workspace.run.case_id !== selectedCase?.id ||
      (!!attempt && (attempt.run_id !== run.id || (!!attemptId && attempt.id !== attemptId))) ||
      (!!attemptId && !!workspace.review_attempts_explicit_null);
  }
  const leftMismatch = identityMismatch(leftReview.data, leftRun, leftAttemptId);
  const rightMismatch = identityMismatch(rightReview.data, rightRun, rightAttemptId);
  const leftData = leftInvalid || leftLegacyChanged || leftIdentityUnknown || leftMismatch ? undefined : leftReview.data;
  const rightData = rightInvalid || rightLegacyChanged || rightIdentityUnknown || rightMismatch ? undefined : rightReview.data;
  const leftState = selectedRunState(leftData);
  const rightState = selectedRunState(rightData);
  const leftScope = JSON.stringify([selectedCase?.id, leftRun?.id, leftAttemptId, leftLegacy]);
  const rightScope = JSON.stringify([selectedCase?.id, rightRun?.id, rightAttemptId, rightLegacy]);
  useEffect(() => {
    setLeftDocumentSelection((selection) => selection && selection.scope === leftScope &&
      materialChoices(leftData).items.some((item) => item.key === selection.key) ? selection : null);
  }, [leftScope, leftData]);
  useEffect(() => {
    setRightDocumentSelection((selection) => selection && selection.scope === rightScope &&
      materialChoices(rightData).items.some((item) => item.key === selection.key) ? selection : null);
  }, [rightScope, rightData]);
  useEffect(() => {
    if (!snapshot || snapshotQuery.isError) return;
    const next = new URLSearchParams(params);
    if (!next.has("case_id") && selectedCase) next.set("case_id", selectedCase.id);
    for (const side of [
      { prefix: "left", run: leftRun, result: leftReview, invalid: leftInvalid },
      { prefix: "right", run: rightRun, result: rightReview, invalid: rightInvalid },
    ] as const) {
      const runKey = `${side.prefix}_run_id`;
      const attemptKey = `${side.prefix}_attempt_id`;
      const legacyKey = `${side.prefix}_run_only`;
      if (!side.run || side.invalid) continue;
      if (!next.has(runKey)) next.set(runKey, side.run.id);
      if (next.has(attemptKey) || next.has(legacyKey)) continue;
      if (side.run.attempt_id) {
        next.set(attemptKey, side.run.attempt_id);
        continue;
      }
      const workspace = side.result.data;
      if (!side.result.isSuccess || !workspace || workspace.run.id !== side.run.id || workspace.run.case_id !== selectedCase?.id) continue;
      const attempt = workspace.review_attempts?.attempt;
      if (attempt && attempt.id && attempt.run_id === side.run.id) next.set(attemptKey, attempt.id);
      else if (workspace.review_attempts_explicit_null) next.set(legacyKey, "1");
    }
    if (next.toString() !== params.toString()) setParams(next, { replace: true });
  }, [snapshot, snapshotQuery.isError, params, setParams, selectedCase, leftRun, rightRun, leftReview.data, leftReview.isSuccess, rightReview.data, rightReview.isSuccess, leftInvalid, rightInvalid]);
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

  const leftHref = selectedCase && leftRun && leftAttemptId && leftData
    ? `/cases/${selectedCase.id}/reviews/${leftRun.id}?attempt_id=${encodeURIComponent(leftAttemptId)}` : null;
  const leftUnavailable = leftInvalid || leftMismatch || leftIdentityUnknown || leftLegacy || (params.has("left_run_id") && !leftRun) || (!!leftRun && !leftData);
  const leftMaterials = materialChoices(leftData);
  const rightMaterials = materialChoices(rightData);
  const leftDocuments = leftMaterials.items;
  const rightDocuments = rightMaterials.items;
  const leftDocument =
    leftDocuments.find((item) => leftDocumentSelection?.scope === leftScope && item.key === leftDocumentSelection.key) ??
    primaryContract(leftDocuments);
  const rightDocument =
    rightDocuments.find((item) => rightDocumentSelection?.scope === rightScope && item.key === rightDocumentSelection.key) ??
    primaryContract(rightDocuments);
  const revisions = leftData?.revisions ?? [];
  const source = leftData?.revision_comparison_source;
  const comparisonsLinked = Boolean(
    selectedCase && leftRun && rightRun && leftAttemptId && rightAttemptId && source &&
    leftData?.run.id === leftRun.id && leftData.run.case_id === selectedCase.id &&
    rightData?.run.id === rightRun.id && rightData.run.case_id === selectedCase.id &&
    leftData.review_attempts?.attempt.id === leftAttemptId &&
    leftData.review_attempts.attempt.run_id === leftRun.id &&
    rightData.review_attempts?.attempt.id === rightAttemptId && rightData.review_attempts.attempt.run_id === rightRun.id &&
    source.current.case_id === selectedCase.id && source.current.run_id === leftRun.id && source.current.attempt_id === leftAttemptId &&
    source.previous.case_id === selectedCase.id && source.previous.run_id === rightRun.id && source.previous.attempt_id === rightAttemptId &&
    leftData.previous_run_id === rightRun.id,
  );
  const bottomTabs = ["修订证据", "规则依据", "执行记录", "模型用量"];
  const findings = leftData?.findings ?? [];
  const finding = findings.find((item) => item.id === findingSelection) ?? null;
  const decidedCount = findings.filter(
    (item) => item.decisions.length > 0,
  ).length;
  function chooseRun(side: "left_run_id" | "right_run_id", id: string) {
    const next = new URLSearchParams(params);
    next.set(side, id);
    const attemptKey = side === "left_run_id" ? "left_attempt_id" : "right_attempt_id";
    const legacyKey = side === "left_run_id" ? "left_run_only" : "right_run_only";
    next.delete(legacyKey);
    const attempt = caseRuns.find((run) => run.id === id)?.attempt_id;
    if (attempt) next.set(attemptKey, attempt);
    else next.delete(attemptKey);
    setParams(next);
    if (side === "left_run_id") {
      setLeftDocumentSelection(null);
      setFindingSelection("");
    } else setRightDocumentSelection(null);
  }
  function chooseFinding(item: Finding) {
    setFindingSelection(item.id);
    for (const anchor of item.evidence) {
      const matches = leftDocuments.filter((choice) => matchesMaterialAnchor(choice, anchor));
      if (matches.length === 1) {
        setLeftDocumentSelection({ scope: leftScope, key: matches[0]!.key });
        break;
      }
    }
  }

  return (
    <section className="page compare-page">
      <header className="compare-page-head">
        <div>
          <h1>冻结执行对照</h1>
          <strong>{selectedCase?.title ?? "版本对照"}</strong>
          <span>{selectedCase ? caseIdentityDetails(selectedCase, snapshot.members) : "案件不可用"}</span>
          <InvalidTimeDetail value={selectedCase?.updated_at} label="案件更新时间" />
        </div>
        <nav className="compare-page-links" aria-label="版本对照导航">
          <Link to="/compare">全部合同</Link>
          <Link to={selectedCase ? `/cases/${selectedCase.id}` : "/cases"}>
            案件详情
          </Link>
        </nav>
      </header>
      <dl className="compare-facts" aria-label="冻结执行对照摘要">
        <div><dt>来源执行</dt><dd>{rightRun ? runLabel(rightRun.state) : "未选择"}</dd></div>
        <div><dt>目标执行</dt><dd>{leftRun ? runLabel(leftRun.state) : "未选择"}</dd></div>
        <div><dt>可比性</dt><dd>{comparisonsLinked ? "已关联同一案件冻结执行" : "不可比或身份未确认"}</dd></div>
        <div><dt>变化</dt><dd>{leftData ? `${revisions.length} 项修订` : "未读取"}</dd></div>
      </dl>
      <div className="compare-workspace">
        <aside className="compare-risk-panel" aria-label="审查结果">
          <header>
            <h2>审查结果</h2>
            <span>{leftData ? `${findings.length} 项实际发现` : "审查发现不可用"}</span>
          </header>
          <div className="compare-risk-counts">
            {(["high", "medium", "low", "info"] as const).map(
              (severity, index) => (
                <span key={severity} data-severity={severity}>
                  {
                    leftData ? findings.filter((item) => item.severity === severity)
                      .length : "—"
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
                  aria-label={`${item.claim}，${findingOriginLabel(item.origin)}，${relationLabel(item.relation)}`}
                  htmlType="button"
                  onClick={() => chooseFinding(item)}
                  aria-pressed={item.id === finding?.id}
                >
                  <strong>{item.claim}</strong>
                  <span>
                    {findingOriginLabel(item.origin)}，
                    {relationLabel(item.relation)}
                  </span>
                </Button>
                {item.id === finding?.id ? (
                  <div className="compare-risk-detail">
                    {item.evidence.map((anchor) => (
                      <blockquote key={anchor.id}>
                        {anchor.quote}
                        <small>
                          {leftDocuments.find((choice) => matchesMaterialAnchor(choice, anchor))?.document.filename ?? "引用来源未返回"}
                        </small>
                      </blockquote>
                    ))}
                    {item.evidence.some((anchor) => leftDocuments.filter((choice) => matchesMaterialAnchor(choice, anchor)).length > 1) ?
                      <p>引用未区分解析版本，无法确定对应正文。</p> : null}
                    {item.severity_source ? (
                      <p>
                        依据版本 {item.severity_source.policy_version}，
                        {item.severity_source.clause}
                      </p>
                    ) : null}
                    <TechnicalDetail
                      label="发现技术详情"
                      value={JSON.stringify({ finding_id: item.id, origin: item.origin, domain: item.domain, relation: item.relation })}
                    />
                    {leftHref ? <Link to={`${leftHref}&finding=${encodeURIComponent(item.id)}`}>
                      打开原文与审查判断
                    </Link> : <span>无法固定到这条执行记录。</span>}
                  </div>
                ) : null}
              </article>
            ))}
            {findings.length === 0 ? (
              <p className="compare-unavailable">
                {leftData ? "这次审查没有返回发现。" : "审查身份未确认，无法读取发现。"}
              </p>
            ) : null}
          </div>
        </aside>
        <div className="compare-reading-area">
          <div className="compare-reading-tabs">
            <strong>文档审查</strong>
            <span>双栏对比</span>
            {leftUnavailable ? <span>无法固定到这条执行记录。</span> :
              <Link to={leftHref ?? "/review"}>打开审查与决定</Link>}
          </div>
          <div className="compare-selectors">
            <RunSelect
              label="当前版本"
              value={leftRun}
              runs={caseRuns}
              attemptId={leftAttemptId}
              onChange={(id) => chooseRun("left_run_id", id)}
            />
            <RunSelect
              label="历史版本"
              value={rightRun}
              runs={caseRuns}
              attemptId={rightAttemptId}
              onChange={(id) => chooseRun("right_run_id", id)}
            />
          </div>
          {leftInvalid || rightInvalid ? <p role="alert">所选执行身份为空或相互冲突，无法读取该侧记录。</p> : null}
          {leftMismatch || rightMismatch ? <p role="alert">返回的审查或执行身份与所选记录不一致，无法读取该侧记录。</p> : null}
          {leftLegacyChanged || rightLegacyChanged ? <p role="alert">所选历史记录原无执行身份，当前返回了不同执行记录，无法确认是同一版本。</p> : null}
          {leftIdentityUnknown || rightIdentityUnknown ? <p role="alert">所选记录未报告执行身份或身份信息冲突，无法确认记录来源。</p> : null}
          {(leftLegacy && leftData?.review_attempts_explicit_null) || (rightLegacy && rightData?.review_attempts_explicit_null) ? <p>部分历史记录仅有审查身份，没有可固定的执行身份；派生对照无法验证。</p> : null}
          <ol className="compare-chain" aria-label="对照链路">
            <li data-state={leftState ? "filled" : "open"}>
              <span className="compare-chain-mark" aria-hidden="true" />
              <span className="compare-chain-label">当前版本</span>
              <strong>{leftState ? runLabel(leftState) : leftRun ? "状态未读取" : "未选择"}</strong>
            </li>
            <li data-state={rightState ? "filled" : "open"}>
              <span className="compare-chain-mark" aria-hidden="true" />
              <span className="compare-chain-label">历史版本</span>
              <strong>{rightState ? runLabel(rightState) : rightRun ? "状态未读取" : "未选择"}</strong>
            </li>
            <li data-state={findings.length > 0 ? "filled" : "open"}>
              <span className="compare-chain-mark" aria-hidden="true" />
              <span className="compare-chain-label">实际发现</span>
              <strong>{leftData ? findings.length : "不可用"}</strong>
            </li>
            <li data-state={decidedCount > 0 ? "filled" : "open"}>
              <span className="compare-chain-mark" aria-hidden="true" />
              <span className="compare-chain-label">已有审查判断</span>
              <strong>{leftData ? decidedCount : "不可用"}</strong>
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
              state={leftState}
              documents={leftDocuments}
              warning={leftMaterials.warning}
              selected={leftDocument}
              onSelect={(key) => setLeftDocumentSelection({ scope: leftScope, key })}
              finding={finding}
              error={leftReview.isError ? leftReview.error : null}
              loading={leftReview.isFetching}
            />
            <MaterialPane
              label="历史版本"
              caseId={selectedCase?.id ?? ""}
              run={rightRun}
              state={rightState}
              documents={rightDocuments}
              warning={rightMaterials.warning}
              selected={rightDocument}
              onSelect={(key) => setRightDocumentSelection({ scope: rightScope, key })}
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
                当前选择与修订来源不一致或没有来源身份，无法验证修订对照；两侧原文和发现仍可查看。
              </p>
            ) : leftData?.revision_comparison_reason ? (
              <p className="compare-unavailable">
                服务端没有生成修订对照：
                {leftData.revision_comparison_reason}
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
                    <span>{revisionMarkLabel(revision.mark)}</span>
                    <div>
                      <strong>
                        {businessDomainLabel(revision.domain)}，{relationLabel(revision.relation)}
                      </strong>
                      <p>{revision.claim}</p>
                      <small>
                        旧决定{" "}
                        {revision.prior_decision
                          ? decisionLabel(revision.prior_decision.action)
                          : "未报告"}
                      </small>
                      <TechnicalDetail
                        label="修订技术详情"
                        value={JSON.stringify({
                          mark: revision.mark,
                          domain: revision.domain,
                          relation: revision.relation,
                          finding_id: revision.finding_id,
                          previous_finding_id: revision.previous_finding_id,
                          prior_decision_id: revision.prior_decision?.id ?? null,
                          prior_decision_action: revision.prior_decision?.action ?? null,
                        })}
                      />
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
                evaluations: leftData?.evaluations ?? [],
              },
              {
                label: "右侧运行",
                evaluations: rightData?.evaluations ?? [],
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
                          {businessDomainLabel(item.domain)}，{relationLabel(item.relation)}
                        </strong>
                        <span>
                          {businessCapabilityLabel(item.capability_id)}
                        </span>
                        <p>{item.reason}</p>
                        <TechnicalDetail
                          label="规则技术详情"
                          value={JSON.stringify({
                            evaluation_id: item.id,
                            domain: item.domain,
                            relation: item.relation,
                            rule_id: item.rule_id,
                            rule_version: item.rule_version,
                            capability_id: item.capability_id,
                            reason_code: item.reason_code,
                            finding_id: item.finding_id,
                            fact_ids: item.fact_ids,
                            object_refs: item.object_refs,
                          })}
                        />
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
              { label: "左侧运行", review: leftData },
              { label: "右侧运行", review: rightData },
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
                            ? preciseTime(attempt.finished_at)
                            : "结束时间未报告"}
                          <InvalidTimeDetail value={attempt.finished_at} label="执行结束时间" />
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
              { label: "左侧运行", review: leftData },
              { label: "右侧运行", review: rightData },
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
