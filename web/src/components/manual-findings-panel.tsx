import { Button, Input, Select } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { createManualFinding, listManualFindings, type ManualFindingInput, type ManualFindingSeverity } from "../api/manual-findings";
import { errorMessage } from "../api/http";
import { clearPendingMutationKey, idempotencyActorScope, pendingMutationKey } from "../api/idempotency";
import type { Actor, ReviewDocument } from "../api/types";
import { frozenQuoteMatchesDocument, type FrozenQuoteSelection } from "../domain/frozen-quote";
import { formatDateTime } from "../domain/review";

const severities: Record<ManualFindingSeverity, string> = {
  high: "高", medium: "中", low: "低", info: "提示",
};

interface ManualFindingsPanelProps {
  caseId: string;
  isFrozenAttempt: boolean;
  parsedDocumentIds: readonly string[];
  runId: string;
  attemptId: string | null;
  selection: FrozenQuoteSelection | null;
  documents: ReviewDocument[];
  actor: Actor | null;
  canCreate: boolean;
}

function sourceFor(selection: FrozenQuoteSelection | null, props: ManualFindingsPanelProps): ReviewDocument | null {
  const scope = { caseId: props.caseId, runId: props.runId, attemptId: props.attemptId,
    isFrozenAttempt: props.isFrozenAttempt, parsedDocumentIds: props.parsedDocumentIds };
  const matching = props.documents.filter((document) => frozenQuoteMatchesDocument(selection, scope, document));
  return matching.length === 1 ? matching[0]! : null;
}

export function ManualFindingsPanel(props: ManualFindingsPanelProps) {
  const { caseId, runId, attemptId, actor, canCreate } = props;
  const client = useQueryClient();
  const key = ["cases", caseId, "manual-findings", runId, attemptId, actor?.tenant_id, actor?.org_id, actor?.id, actor?.role] as const;
  const findings = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => listManualFindings(caseId, runId, attemptId!, signal),
    enabled: Boolean(caseId && runId && attemptId && actor),
    retry: false,
  });
  const [selected, setSelected] = useState<FrozenQuoteSelection | null>(null);
  const [claim, setClaim] = useState("");
  const [severity, setSeverity] = useState<ManualFindingSeverity>("medium");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const currentSource = sourceFor(props.selection, props);
  const selectedSource = sourceFor(selected, props);
  const claimLength = Array.from(claim.trim()).length;

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!canCreate || !actor || !attemptId || !selected || !selectedSource || pending || claimLength < 2 || claimLength > 1000) return;
    const input: ManualFindingInput = {
      claim: claim.trim(), severity,
      evidence: { document_id: selected.documentId, parsed_document_id: selected.parsedDocumentId,
        start: selected.start, end: selected.end, quote: selected.quote },
    };
    setPending(true);
    setError(null);
    setNotice(null);
    try {
      const mutation = await pendingMutationKey("manual-finding.create", idempotencyActorScope(actor), `${caseId}:${runId}:${attemptId}`, input);
      await createManualFinding(caseId, runId, attemptId, input, mutation.key);
      setClaim("");
      setSelected(null);
      setNotice("人工发现已记录，可在下方查看。它不会改写原审查结论或已保存的报告。");
      try {
        clearPendingMutationKey(mutation.storageKey);
      } catch {
        setError("记录已保存，但浏览器无法清理安全重试标识，请勿重复提交。");
      }
      void client.invalidateQueries({ queryKey: key, exact: true });
    } catch (err) {
      setError(`保存未完成：${errorMessage(err)}`);
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="workflow-panel" aria-labelledby="manual-findings-heading">
      <h2 id="manual-findings-heading">人工发现</h2>
      <p>人工记录会保留作者和本次执行的原文依据，独立于自动发现与已有审查判断。</p>
      {attemptId && canCreate ? (
        <form onSubmit={(event) => void submit(event)}>
          <p>在上方冻结正文中选择文字，再用于新增人工发现。</p>
          <Button htmlType="button" disabled={!currentSource || pending} onClick={() => {
            setSelected(props.selection);
            setError(null);
          }}>将所选原文用于人工发现</Button>
          {selected && selectedSource ? <blockquote><strong>{selectedSource.filename}</strong><p>{selected.quote}</p></blockquote>
            : <p>{selected ? "原文已变化，请重新选择。" : "尚未选择原文。"}</p>}
          <p><label htmlFor="manual-finding-claim">发现内容</label></p>
          <Input.TextArea id="manual-finding-claim" value={claim} maxLength={2000} disabled={pending} onChange={(event) => setClaim(event.target.value)} />
          <p>请输入 2 至 1000 个字符。</p>
          <p><label htmlFor="manual-finding-severity">严重程度</label></p>
          <Select id="manual-finding-severity" value={severity} disabled={pending} onChange={(value: ManualFindingSeverity) => setSeverity(value)}
            options={Object.entries(severities).map(([value, label]) => ({ value, label }))} />
          <p><Button type="primary" htmlType="submit" loading={pending} disabled={!selectedSource || claimLength < 2 || claimLength > 1000}>保存人工发现</Button></p>
        </form>
      ) : <p>当前执行或身份不支持新增人工发现。</p>}
      {error ? <p role="alert">{error}</p> : null}
      {notice ? <p role="status">{notice}</p> : null}
      {!attemptId ? <p>请选择一条执行记录。</p> : !actor ? <p>正在确认访问权限。</p> : findings.isLoading ? <p>正在读取人工发现。</p> : findings.isError ? (
        <p role="alert">无法读取人工发现：{errorMessage(findings.error)} <Button htmlType="button" onClick={() => void findings.refetch()}>重试</Button></p>
      ) : findings.data?.length ? (
        <ul>
          {findings.data.map((item) => {
            const doc = props.documents.find((candidate) => candidate.id === item.evidence.document_id && candidate.parsed_document_id === item.evidence.parsed_document_id);
            return <li key={item.id}>
              <article>
                <h3>{item.claim}</h3>
                <p>人工记录 · 严重程度：{severities[item.severity]} · 作者：{item.actor_name}（{item.actor_id}） · {formatDateTime(item.created_at)}</p>
                <p>原文依据：{doc?.filename ?? item.evidence.document_id}，位置 {item.evidence.start}–{item.evidence.end}</p>
                <blockquote>{item.evidence.quote}</blockquote>
              </article>
            </li>;
          })}
        </ul>
      ) : <p>本次执行还没有人工发现。</p>}
    </section>
  );
}
