import { Alert, Button, Tag } from "antd";
import { useQuery } from "@tanstack/react-query";
import type { FrozenContractCitation } from "../api/contract-citations";
import { getFrozenContractCitation } from "../api/contract-citations";
import type { BusinessAgentEvidence } from "../api/business-agent-review";
import { errorMessage } from "../api/http";
import { offsetAgrees, segmentText, sliceByCodePoints } from "../domain/text";

/** The exact citation the workspace projection showed, used to reject a
 * payload that answers a different document, parse or range. */
function identityProblem(
  citation: FrozenContractCitation,
  ids: { caseId: string; runId: string; attemptId: string; evidenceId: string },
  expected: BusinessAgentEvidence,
): string | null {
  if (
    citation.case_id !== ids.caseId ||
    citation.run_id !== ids.runId ||
    citation.attempt_id !== ids.attemptId ||
    citation.evidence_id !== ids.evidenceId
  ) {
    return "返回的引用不属于当前案件、审查或执行记录，未显示正文。";
  }
  const anchor = expected.anchor;
  if (
    citation.document_id !== anchor.document_id ||
    citation.parsed_document_id !== anchor.parsed_document_id ||
    citation.start !== anchor.start ||
    citation.end !== anchor.end ||
    citation.quote !== anchor.quote
  ) {
    return "返回的原文身份与当前引用不一致，未显示正文。";
  }
  return null;
}

function FrozenText({ citation }: { citation: FrozenContractCitation }) {
  const hit = sliceByCodePoints(citation.text, citation.start, citation.end);
  if (hit === null || !offsetAgrees(hit, citation.quote)) {
    return (
      <Alert
        type="error"
        showIcon
        title="引用区间与返回的冻结原文不一致"
        description="为避免误导，本次不显示高亮；请重新读取或联系管理员核对冻结原文。"
      />
    );
  }
  const segments = segmentText(
    citation.text,
    { start: citation.start, end: citation.end },
    [],
  );
  return (
    <pre
      className="frozen-citation-text"
      tabIndex={0}
      aria-label="本次冻结原文，引用区间已高亮"
    >
      {segments.map((segment) =>
        segment.highlighted === "evidence" ? (
          <mark key={segment.start} data-offset={segment.start} className="hit-evidence">
            {segment.text}
          </mark>
        ) : (
          <span key={segment.start} data-offset={segment.start}>
            {segment.text}
          </span>
        ),
      )}
    </pre>
  );
}

function CitationBody({
  citation,
  expected,
  ids,
}: {
  citation: FrozenContractCitation;
  expected: BusinessAgentEvidence;
  ids: { caseId: string; runId: string; attemptId: string; evidenceId: string };
}) {
  const problem = identityProblem(citation, ids, expected);
  if (problem !== null) {
    return <Alert type="error" showIcon title={problem} />;
  }
  return (
    <>
      <p>
        <strong>{citation.filename}</strong>{" "}
        {citation.material_id !== null && citation.material_version !== null ? (
          <Tag>{`材料 ${citation.material_id} · 第 ${citation.material_version} 版`}</Tag>
        ) : (
          <Tag>材料版本未记录</Tag>
        )}
        <Tag>{`字符 ${citation.start + 1}–${citation.end}（Unicode 码点）`}</Tag>
      </p>
      <p>
        <code>{`原文摘要 ${citation.original_digest}`}</code>
        <br />
        <code>{`解析摘要 ${citation.parse_digest}`}</code>
        <br />
        <code>{`冻结解析 ${citation.parsed_document_id}`}</code>
      </p>
      <FrozenText citation={citation} />
    </>
  );
}

/**
 * Reads one verified agent citation from its exact attempt. The page URL names
 * the attempt and the evidence, so a refresh or a new login restores the same
 * frozen text, and a different attempt never inherits this citation.
 */
export function FrozenContractCitationReader({
  caseId,
  runId,
  attemptId,
  evidenceId,
  expected,
}: {
  caseId: string;
  runId: string;
  attemptId: string;
  evidenceId: string;
  expected: BusinessAgentEvidence;
}) {
  const citation = useQuery({
    queryKey: [
      "cases", caseId, "runs", runId, "attempts", attemptId,
      "contract-citations", evidenceId,
    ],
    queryFn: ({ signal }) =>
      getFrozenContractCitation(caseId, runId, attemptId, evidenceId, signal),
    retry: false,
  });
  const ids = { caseId, runId, attemptId, evidenceId };
  return (
    <section className="frozen-citation-reader" aria-label="本次冻结原文">
      {citation.isFetching ? <p role="status">正在读取本次冻结原文…</p> : null}
      {citation.isError ? (
        <Alert
          type="error"
          showIcon
          title="本次冻结原文暂时无法读取，不能把这条引用当成已确认结论"
          description={errorMessage(citation.error)}
          action={<Button onClick={() => void citation.refetch()}>重试</Button>}
        />
      ) : null}
      {citation.data ? (
        <CitationBody citation={citation.data} expected={expected} ids={ids} />
      ) : null}
    </section>
  );
}
