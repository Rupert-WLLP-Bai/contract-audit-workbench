import { Card } from "antd";
import { Button, Checkbox, Input, Radio } from "antd";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { errorMessage } from "../api/http";
import {
  useCreateRemediation,
  useReview,
  useSubmitRemediation,
  useVerifyRemediation,
} from "../api/queries";
import type {
  CaseDetail,
  ConstraintEvaluation,
  Remediation,
  ReviewHistoryEntry,
} from "../api/types";
import {
  formatDateTime,
  isTerminalAttemptState,
  relationLabel,
  runLabel,
  runTone,
} from "../domain/review";
import {
  reviewChoiceForKey,
  reviewChoices,
  type ReviewChoice,
} from "../domain/workflow";
import { ReviewChoiceSelect } from "./review-choice-select";
import { StatusText } from "./status-text";

const REMEDIATION_LABELS: Record<string, string> = {
  open: "待整改",
  submitted: "待复审",
  closed: "已关闭",
  reopened: "重新打开",
};
function uniqueObjectRefs(values: string[] | undefined): values is string[] {
  return (
    values !== undefined &&
    values.length > 0 &&
    values.every((value) => value.trim() !== "") &&
    new Set(values).size === values.length
  );
}

function hasCompatibleRuleEvaluations(
  findingIds: string[],
  sourceEvaluations: ConstraintEvaluation[],
  candidateEvaluations: ConstraintEvaluation[],
): boolean {
  return (
    findingIds.length > 0 &&
    findingIds.every((findingId) => {
      const sourceRows = sourceEvaluations.filter(
        (evaluation) => evaluation.finding_id === findingId,
      );
      if (sourceRows.length !== 1) return false;
      const source = sourceRows[0];
      if (
        !source ||
        !source.rule_id.trim() ||
        !source.rule_version.trim() ||
        !source.capability_id.trim() ||
        !uniqueObjectRefs(source.object_refs)
      ) {
        return false;
      }
      const matching = candidateEvaluations.filter(
        (evaluation) =>
          evaluation.rule_id === source.rule_id &&
          evaluation.rule_version === source.rule_version &&
          evaluation.capability_id === source.capability_id &&
          uniqueObjectRefs(evaluation.object_refs) &&
          evaluation.object_refs.length === source.object_refs.length &&
          evaluation.object_refs.every((objectRef) =>
            source.object_refs.includes(objectRef),
          ),
      );
      return matching.length === 1 && matching[0]?.relation === "compatible";
    })
  );
}

function RemediationCard({
  caseId,
  item,
  documents,
  members,
  archived,
  canWrite,
  choices,
}: {
  caseId: string;
  item: Remediation;
  members: CaseDetail["members"];
  documents: CaseDetail["documents"];
  archived: boolean;
  canWrite: boolean;
  choices: ReviewChoice[];
}) {
  const source = useReview(caseId, item.run_id, item.attempt_id);
  const submit = useSubmitRemediation(caseId, item.id);
  const verify = useVerifyRemediation(caseId, item.id);
  const candidateDefault =
    choices.find((choice) => choice.runId !== item.run_id)?.key ??
    choices[0]?.key ??
    "";
  const [candidateKey, setCandidateKey] = useState(candidateDefault);
  const [documentId, setDocumentId] = useState(item.document_ids[0] ?? "");
  const [submitReason, setSubmitReason] = useState("");
  const [verifyReason, setVerifyReason] = useState("");
  const submitErrorRef = useRef<HTMLParagraphElement>(null);
  const verifyErrorRef = useRef<HTMLParagraphElement>(null);
  const candidate = reviewChoiceForKey(choices, candidateKey);
  const candidateWorkspace = useReview(
    caseId,
    candidate?.runId ?? "",
    candidate?.attempt.id ?? null,
  );

  useEffect(() => {
    if (!candidateKey && choices.length > 0) setCandidateKey(candidateDefault);
  }, [candidateDefault, candidateKey, choices.length]);
  useEffect(() => {
    if (
      documentId &&
      !documents.some(
        (document) =>
          document.id === documentId &&
          !document.superseded &&
          document.role === "draft_contract",
      )
    ) {
      setDocumentId("");
    }
  }, [documentId, documents]);
  useEffect(() => {
    if (submit.error) submitErrorRef.current?.focus();
  }, [submit.error]);
  useEffect(() => {
    if (verify.error) verifyErrorRef.current?.focus();
  }, [verify.error]);

  const originalFindingIds = new Set(
    source.data?.findings.map((finding) => finding.id) ?? [],
  );
  const selectedAttempt = candidateWorkspace.data?.review_attempts?.attempt;
  const selectedRunIsExpected =
    candidateWorkspace.data?.run.id === candidate?.runId;
  const selectedAttemptIsExpected =
    selectedAttempt?.id === candidate?.attempt.id;
  const isNewAttempt =
    candidate !== null && candidate.attempt.id !== item.attempt_id;
  const comparesSourceRun =
    candidateWorkspace.data?.previous_run_id === item.run_id;
  const comparisonRows = item.finding_ids.map((findingId) => {
    const rows =
      candidateWorkspace.data?.revisions?.filter(
        (revision) => revision.previous_finding_id === findingId,
      ) ?? [];
    return {
      findingId,
      rows,
      finding: source.data?.findings.find((entry) => entry.id === findingId),
    };
  });
  const everyFindingCompatible =
    item.finding_ids.every((findingId) => originalFindingIds.has(findingId)) &&
    hasCompatibleRuleEvaluations(
      item.finding_ids,
      source.data?.evaluations ?? [],
      candidateWorkspace.data?.evaluations ?? [],
    );
  const comparisonReason = candidateWorkspace.data?.revision_comparison_reason;
  const verificationReady =
    item.status === "submitted" &&
    !archived &&
    canWrite &&
    source.isSuccess &&
    candidateWorkspace.isSuccess &&
    selectedRunIsExpected &&
    selectedAttemptIsExpected &&
    isNewAttempt &&
    isTerminalAttemptState(selectedAttempt?.state ?? "") &&
    comparesSourceRun &&
    !comparisonReason &&
    everyFindingCompatible;
  let verificationBlockReason =
    "当前复审没有返回与整改来源一致的兼容规则评估。";
  if (source.isLoading)
    verificationBlockReason = "正在读取整改绑定的原运行和尝试。";
  else if (source.isError)
    verificationBlockReason = `无法读取整改来源：${errorMessage(source.error)}`;
  else if (candidateWorkspace.isLoading)
    verificationBlockReason = "正在读取所选复核尝试。";
  else if (candidateWorkspace.isError)
    verificationBlockReason = `无法读取所选复核尝试：${errorMessage(candidateWorkspace.error)}`;
  else if (comparisonReason) verificationBlockReason = comparisonReason;
  else if (candidateWorkspace.data && !comparesSourceRun)
    verificationBlockReason = "所选复核并非针对这项整改的来源审查。";
  else if (candidateWorkspace.data && !everyFindingCompatible)
    verificationBlockReason =
      "复核未为每个整改发现返回唯一、同规则版本、同能力和同业务对象的兼容评估。";
  else if (candidateWorkspace.data && !isNewAttempt)
    verificationBlockReason = "关闭复核必须绑定不同于整改来源的新尝试。";
  else if (selectedAttempt && !isTerminalAttemptState(selectedAttempt.state))
    verificationBlockReason = "所选尝试尚未结束，不能作为复核依据。";

  function onSubmitChanges(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!documentId) return;
    submit.mutate(
      {
        document_ids: [documentId],
        reason: submitReason.trim(),
        expected_version: item.version,
      },
      {
        onSuccess: () => setSubmitReason(""),
      },
    );
  }

  function onVerify(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!candidate) return;
    verify.mutate(
      {
        run_id: candidate.runId,
        attempt_id: candidate.attempt.id,
        reason: verifyReason.trim(),
        expected_version: item.version,
      },
      { onSuccess: () => setVerifyReason("") },
    );
  }

  return (
    <li className="workflow-record">
      <header className="workflow-panel-head">
        <div>
          <h4>整改记录</h4>
          <p className="meta">
            负责人 {item.assignee_id
              ? members.find((member) => member.id === item.assignee_id)?.display_name ?? "未返回负责人信息"
              : "未分配"}，截止 {formatDateTime(item.due_at)}，
            版本 {item.version}
          </p>
        </div>
        <StatusText
          tone={
            item.status === "closed"
              ? "good"
              : item.status === "submitted"
                ? "attention"
                : "neutral"
          }
        >
          {REMEDIATION_LABELS[item.status] ?? item.status}
        </StatusText>
      </header>
      <dl className="execution-dl">
        <div>
          <dt>来源审查</dt>
          <dd>
            <Link
              to={`/cases/${encodeURIComponent(caseId)}/reviews/${encodeURIComponent(item.run_id)}?attempt_id=${encodeURIComponent(item.attempt_id)}`}
            >
              查看来源审查
            </Link>
          </dd>
        </div>
        <div>
          <dt>绑定发现</dt>
          <dd>
            {item.finding_ids.map((id) =>
              source.data?.findings.find((finding) => finding.id === id)?.claim || "发现详情未返回",
            ).join("、") || "未提供"}
          </dd>
        </div>
        <div>
          <dt>整改措施</dt>
          <dd>{item.measure}</dd>
        </div>
        <div>
          <dt>修改稿文档</dt>
          <dd>
            {item.document_ids.map((id) =>
              documents.find((document) => document.id === id)?.filename || "材料未返回",
            ).join("、") || "尚未绑定"}
          </dd>
        </div>
        <div>
          <dt>最近理由</dt>
          <dd>{item.reason || "未提供"}</dd>
        </div>
        <div>
          <dt>复核审查</dt>
          <dd>
            {item.verification_run_id && item.verification_attempt_id ? (
              <Link
                to={`/cases/${encodeURIComponent(caseId)}/reviews/${encodeURIComponent(item.verification_run_id)}?attempt_id=${encodeURIComponent(item.verification_attempt_id)}`}
              >
                查看复核审查
              </Link>
            ) : "未复核"}
          </dd>
        </div>
        <div>
          <dt>创建 / 更新</dt>
          <dd>
            {formatDateTime(item.created_at)}，
            {formatDateTime(item.updated_at)}
          </dd>
        </div>
      </dl>

      {item.status === "open" || item.status === "reopened" ? (
        <form className="workflow-form" onSubmit={onSubmitChanges}>
          <h5>提交修改稿</h5>
          <Card
            role="group"
            aria-label={"本案当前的拟签合同修改稿"}
            className="workflow-checks"
            title={<>本案当前的拟签合同修改稿</>}
          >
            {documents.filter(
              (document) =>
                !document.superseded && document.role === "draft_contract",
            ).length === 0 ? (
              <p>当前没有可绑定的拟签合同修改稿。</p>
            ) : null}
            {documents
              .filter(
                (document) =>
                  !document.superseded && document.role === "draft_contract",
              )
              .map((document) => (
                <div className="workflow-check" key={document.id}>
                  <Radio
                    name={`remediation-document-${item.id}`}
                    value={document.id}
                    checked={documentId === document.id}
                    disabled={!canWrite || archived || submit.isPending}
                    onChange={() => setDocumentId(document.id)}
                  >
                    {document.filename}，{formatDateTime(document.created_at)}
                  </Radio>
                </div>
              ))}
          </Card>
          <label className="field">
            提交理由
            <Input.TextArea
              required
              value={submitReason}
              onChange={(event) => setSubmitReason(event.target.value)}
              disabled={!canWrite || archived || submit.isPending}
            />
          </label>
          <p className="meta">
            提交会绑定当前所选材料和整改版本 {item.version}；仅上传材料不会关闭整改。
          </p>
          {submit.error ? (
            <p
              ref={submitErrorRef}
              className="workflow-error"
              role="alert"
              tabIndex={-1}
            >
              {errorMessage(submit.error)}
            </p>
          ) : null}
          <Button
            aria-label={String(
              submit.isPending ? "正在提交修改稿" : "提交修改稿",
            )}
            type="primary"
            htmlType="submit"
            disabled={!canWrite || archived || !documentId || submit.isPending}
          >
            {submit.isPending ? "正在提交修改稿" : "提交修改稿"}
          </Button>
        </form>
      ) : null}

      {item.status === "submitted" ? (
        <form className="workflow-form" onSubmit={onVerify}>
          <h5>新运行复核</h5>
          <ReviewChoiceSelect
            label="复核运行和尝试"
            choices={choices}
            value={candidateKey}
            onChange={setCandidateKey}
            disabled={!canWrite || archived || verify.isPending}
          />
          {candidateWorkspace.isLoading ? (
            <p role="status">正在读取复核依据。</p>
          ) : null}
          {candidateWorkspace.isError ? (
            <p className="workflow-error" role="alert">
              {errorMessage(candidateWorkspace.error)}
            </p>
          ) : null}
          {candidateWorkspace.data ? (
            <div className="verification-evidence">
              <p>
                复核状态{" "}
                <StatusText tone={runTone(candidateWorkspace.data.run.state)}>
                  {runLabel(candidateWorkspace.data.run.state)}
                </StatusText>
                {selectedAttempt ? `，第 ${selectedAttempt.ordinal} 次审查` : "，审查次数未返回"}
              </p>
              {candidateWorkspace.data.revision_comparison_reason ? (
                <p className="callout">
                  运行间差异明细未生成：
                  {candidateWorkspace.data.revision_comparison_reason}
                </p>
              ) : null}
              <ul className="workflow-record-list">
                {comparisonRows.map(({ findingId, rows, finding }) => (
                  <li key={findingId}>
                    <strong>{finding?.claim || "来源发现未返回"}</strong>
                    {rows.length === 0 ? (
                      <p>没有返回与该来源发现对应的比较记录。</p>
                    ) : (
                      rows.map((revision, index) => (
                        <p
                          className="meta"
                          key={`${revision.finding_id ?? findingId}-${index}`}
                        >
                          {revision.mark}，{revision.domain}，
                          {relationLabel(revision.relation)}，{revision.claim}
                        </p>
                      ))
                    )}
                  </li>
                ))}
              </ul>
              <p className="meta">
                资格依据是来源 finding
                对应的规则、版本、能力和对象引用，以及本轮唯一的 compatible
                规则评估；差异标记仅供查看，服务端会独立验证。
              </p>
            </div>
          ) : null}
          {!verificationReady ? (
            <p className="callout">{verificationBlockReason}</p>
          ) : null}
          <label className="field">
            复核理由
            <Input.TextArea
              required
              value={verifyReason}
              onChange={(event) => setVerifyReason(event.target.value)}
              disabled={
                !canWrite || archived || !verificationReady || verify.isPending
              }
            />
          </label>
          {verify.error ? (
            <p
              ref={verifyErrorRef}
              className="workflow-error"
              role="alert"
              tabIndex={-1}
            >
              {errorMessage(verify.error)}
            </p>
          ) : null}
          {verify.data ? (
            <p role="status">
              服务端复核结果：
              {REMEDIATION_LABELS[verify.data.status] ?? verify.data.status}，
              版本 {verify.data.version}
            </p>
          ) : null}
          <Button
            aria-label={String(verify.isPending ? "正在提交复核" : "提交复核")}
            type="primary"
            htmlType="submit"
            disabled={
              !canWrite || archived || !verificationReady || verify.isPending
            }
          >
            {verify.isPending ? "正在提交复核" : "提交复核"}
          </Button>
        </form>
      ) : null}
      {archived ? (
        <p className="callout">
          案件已归档，整改写入会被服务端拒绝（HTTP 409）。
        </p>
      ) : null}
    </li>
  );
}

export function RemediationPanel({
  caseId,
  detail,
  history,
  historyLoading,
  historyError,
  canWrite,
  archived,
}: {
  caseId: string;
  detail: CaseDetail;
  history: ReviewHistoryEntry[];
  historyLoading: boolean;
  historyError: unknown;
  canWrite: boolean;
  archived: boolean;
}) {
  const create = useCreateRemediation(caseId);
  const choices = reviewChoices(history);
  const [choiceKey, setChoiceKey] = useState("");
  const [findingIds, setFindingIds] = useState<string[]>([]);
  const [assigneeId, setAssigneeId] = useState("");
  const [dueAt, setDueAt] = useState("");
  const [measure, setMeasure] = useState("");
  const createErrorRef = useRef<HTMLParagraphElement>(null);
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
    if (create.error) createErrorRef.current?.focus();
  }, [create.error]);

  function onCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selectedChoice) return;
    create.mutate(
      {
        run_id: selectedChoice.runId,
        attempt_id: selectedChoice.attempt.id,
        finding_ids: findingIds,
        assignee_id: assigneeId.trim(),
        due_at: new Date(dueAt).toISOString(),
        measure: measure.trim(),
      },
      {
        onSuccess: () => {
          setFindingIds([]);
          setAssigneeId("");
          setDueAt("");
          setMeasure("");
        },
      },
    );
  }

  function toggleFinding(findingId: string, checked: boolean) {
    setFindingIds((current) =>
      checked
        ? current.includes(findingId)
          ? current
          : [...current, findingId]
        : current.filter((id) => id !== findingId),
    );
  }

  const findings = selectedReview.data?.findings ?? [];
  const selectedAttempt = selectedReview.data?.review_attempts?.attempt;
  const selectedAttemptMatches =
    selectedReview.data?.run.id === selectedChoice?.runId &&
    selectedAttempt?.id === selectedChoice?.attempt.id;
  const canCreate =
    canWrite &&
    !archived &&
    selectedReview.isSuccess &&
    selectedAttemptMatches &&
    isTerminalAttemptState(selectedAttempt?.state ?? "") &&
    findingIds.length > 0;

  return (
    <section className="workflow-panel" aria-labelledby="remediation-title">
      <header className="workflow-panel-head">
        <div>
          <h2 id="remediation-title">整改与复核</h2>
          <p className="meta">
            整改绑定来源审查和发现；关闭需单独提交新审查的真实兼容复核。
          </p>
        </div>
      </header>
      {!canWrite ? (
        <p className="callout">当前角色没有已定义的整改写入权限。</p>
      ) : null}
      {archived ? (
        <p className="callout">
          案件已归档，整改创建、提交和复核写入会被服务端拒绝（HTTP 409）。
        </p>
      ) : null}
      {historyLoading ? <p role="status">正在读取运行与尝试历史。</p> : null}
      {historyError ? (
        <p className="workflow-error" role="alert">
          运行历史没有加载：{errorMessage(historyError)}
        </p>
      ) : null}
      {canWrite && !archived ? (
        <form className="workflow-form" onSubmit={onCreate}>
          <h3>创建整改</h3>
          <ReviewChoiceSelect
            label="来源运行和尝试"
            choices={choices}
            value={choiceKey}
            onChange={(value) => {
              setChoiceKey(value);
              setFindingIds([]);
            }}
            disabled={choices.length === 0 || create.isPending}
          />
          {selectedReview.isLoading ? (
            <p role="status">正在读取所选尝试的发现。</p>
          ) : null}
          {selectedReview.isError ? (
            <p className="workflow-error" role="alert">
              所选尝试没有加载：{errorMessage(selectedReview.error)}
            </p>
          ) : null}
          {selectedReview.data && findings.length > 0 ? (
            <Card
              role="group"
              aria-label={"绑定发现（至少选择一项）"}
              className="workflow-checks"
              title={<>绑定发现（至少选择一项）</>}
            >
              {findings.map((finding) => (
                <div className="workflow-check" key={finding.id}>
                  <Checkbox
                    checked={findingIds.includes(finding.id)}
                    disabled={create.isPending}
                    onChange={(event) =>
                      toggleFinding(finding.id, event.target.checked)
                    }
                  >
                    {finding.claim}，{finding.domain}
                  </Checkbox>
                </div>
              ))}
            </Card>
          ) : null}
          {selectedReview.data && findings.length === 0 ? (
            <p>所选尝试没有返回可绑定的发现。</p>
          ) : null}
          {choices.length === 0 && !historyLoading && !historyError ? (
            <p>没有可选择的冻结运行尝试；旧的未冻结历史不能用于新建整改。</p>
          ) : null}
          <label className="field">
            负责人 ID
            <Input
              required
              value={assigneeId}
              onChange={(event) => setAssigneeId(event.target.value)}
              disabled={create.isPending}
            />
          </label>
          <label className="field">
            截止时间
            <Input
              type="datetime-local"
              required
              value={dueAt}
              onChange={(event) => setDueAt(event.target.value)}
              disabled={create.isPending}
            />
          </label>
          <label className="field">
            整改措施
            <Input.TextArea
              required
              value={measure}
              onChange={(event) => setMeasure(event.target.value)}
              disabled={create.isPending}
            />
          </label>
          {create.error ? (
            <p
              ref={createErrorRef}
              className="workflow-error"
              role="alert"
              tabIndex={-1}
            >
              {errorMessage(create.error)}
            </p>
          ) : null}
          <Button
            aria-label={String(create.isPending ? "正在创建整改" : "创建整改")}
            type="primary"
            htmlType="submit"
            disabled={!canCreate || create.isPending}
          >
            {create.isPending ? "正在创建整改" : "创建整改"}
          </Button>
        </form>
      ) : null}
      <section aria-labelledby="remediation-history-title">
        <h3 id="remediation-history-title">整改历史</h3>
        {detail.remediations.length === 0 ? (
          <p>没有已返回的整改记录。</p>
        ) : (
          <ul className="workflow-record-list">
            {detail.remediations.map((item) => (
              <RemediationCard
                key={item.id}
                caseId={caseId}
                item={item}
                documents={detail.documents}
                members={detail.members}
                archived={archived}
                canWrite={canWrite}
                choices={choices}
              />
            ))}
          </ul>
        )}
      </section>
    </section>
  );
}
