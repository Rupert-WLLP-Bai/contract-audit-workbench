import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { getFrozenLegalAuthority, type FrozenLegalAuthority, type FrozenLegalReviewProjection } from "../api/legal-citations";

function outcomeLabel(outcome: FrozenLegalAuthority["outcome"]): string {
  switch (outcome) {
    case "applicable": return "适用";
    case "not_applicable": return "不适用";
    case "unknown": return "适用性待核";
  }
}

function CitationReader({ caseId, projection, authority }: {
  caseId: string;
  projection: FrozenLegalReviewProjection;
  authority: FrozenLegalAuthority;
}) {
  const [open, setOpen] = useState(false);
  const citation = useQuery({
    queryKey: ["cases", caseId, "runs", projection.run_id, "attempts", projection.attempt_id, "legal-citations", authority.citation_id],
    queryFn: ({ signal }) => getFrozenLegalAuthority(caseId, projection.run_id, projection.attempt_id, authority.citation_id, signal),
    enabled: open,
    retry: false,
  });
  const article = citation.data?.article;
  return (
    <li>
      <h3>{authority.article?.norm_title ?? "历史规则依据缺口"}</h3>
      {authority.article ? <p>{authority.article.article_label} · {authority.article.norm_version}</p> : null}
      <p>适用状态：{outcomeLabel(authority.outcome)}{authority.outcome_reason ? `（${authority.outcome_reason}）` : ""}</p>
      <p>法务审核：未运行</p>
      {authority.missing_dependencies.length > 0 ? (
        <div>
          <strong>缺少的适用事实</strong>
          <ul>
            {authority.missing_dependencies.map((gap, index) => (
              <li key={`${gap.dimension ?? gap.kind}-${index}`}>
                {gap.dimension ? `${gap.dimension}：` : ""}{gap.reason}
              </li>
            ))}
          </ul>
        </div>
      ) : <p>没有返回缺失的适用事实。</p>}
      <button type="button" disabled={!authority.article || authority.legacy_rule_gap} onClick={() => setOpen((value) => !value)}>
        {open ? "收起冻结条文" : "读取本次冻结条文"}
      </button>
      {open && citation.isFetching ? <p role="status">正在读取本次冻结条文…</p> : null}
      {open && citation.isError ? (
        <div role="alert">
          <p>本次冻结条文读取失败：{citation.error instanceof Error ? citation.error.message : "未知错误"}</p>
          <button type="button" onClick={() => void citation.refetch()}>重试</button>
        </div>
      ) : null}
      {open && article ? (
        <div>
          <p>{article.source_provenance.issuer} · {article.source_provenance.publisher} · {article.norm_version}</p>
          <p>效力期间：{article.effective_from}{article.effective_until ? ` 至 ${article.effective_until}` : "起"}</p>
          <p>状态记录：{article.status_provenance.observed_status}（{article.status_provenance.observed_at}）</p>
          <p>法律审核状态为未运行；该条文按本次审查冻结的版本显示。</p>
          <blockquote>{article.canonical_full_article_body}</blockquote>
          <a href={article.source_provenance.official_url} target="_blank" rel="noreferrer">查看记录的官方来源</a>
        </div>
      ) : null}
    </li>
  );
}

export function FrozenLegalAuthorities({
  caseId,
  runId,
  attemptId,
  projection,
}: {
  caseId: string;
  runId: string;
  attemptId: string | null;
  projection: FrozenLegalReviewProjection | undefined;
}) {
  const exact = projection?.run_id === runId && projection.attempt_id === attemptId;
  return (
    <section aria-label="本次审查法律依据">
      <h2>本次审查法律依据</h2>
      {!projection ? <p>本次审查未返回独立法律依据快照；不会用当前规则或案件资料补填。</p> : null}
      {projection && !exact ? <p role="alert">法律依据快照与当前选择的审查或执行记录不匹配，未显示。</p> : null}
      {exact && projection.authorities.length === 0 ? <p>本次审查未返回法律依据条目。</p> : null}
      {exact && projection.authorities.length > 0 ? (
        <>
          <p>依据快照截止时间：{projection.evidence_cutoff_at}（{projection.evidence_cutoff_mode === "explicit" ? "指定时点" : "审查受理时点"}）</p>
          <ul>
            {projection.authorities.map((authority) => (
              <CitationReader key={authority.citation_id} caseId={caseId} projection={projection} authority={authority} />
            ))}
          </ul>
        </>
      ) : null}
    </section>
  );
}
