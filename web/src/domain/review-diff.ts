import type { BusinessAgentEvidence } from "../api/business-agent-review";
import type {
  ConstraintEvaluation,
  EvidenceAnchor,
  Finding,
  ReviewDocument,
  ReviewWorkspace,
} from "../api/types";
import { resolveAnchor } from "./text";

export type DifferenceState = "same" | "different" | "unknown";
export type ReviewDiffKind = "added" | "disappeared" | "changed" | "unchanged" | "unmatched";

export interface ReviewScopeIdentity {
  id: string;
  rule_set_id: string;
  rule_set_version: string;
  rule_set_hash: string;
}

export interface ReviewDiffSide {
  workspace: ReviewWorkspace;
  evaluation: ConstraintEvaluation | null;
  finding: Finding | null;
}

export interface ReviewDiffRow {
  kind: ReviewDiffKind;
  source: ReviewDiffSide | null;
  target: ReviewDiffSide | null;
  reason: string;
}

// The public scope projection is optional; older histories do not have it.
export function reviewScope(workspace: ReviewWorkspace): ReviewScopeIdentity | null {
  const value = (workspace.snapshot as (typeof workspace.snapshot & { review_scope?: unknown }))?.review_scope;
  if (typeof value !== "object" || value === null) return null;
  const row = value as Record<string, unknown>;
  if (typeof row.id !== "string" || !row.id ||
      typeof row.rule_set_id !== "string" || !row.rule_set_id ||
      typeof row.rule_set_version !== "string" || !row.rule_set_version ||
      typeof row.rule_set_hash !== "string" || !row.rule_set_hash) return null;
  return { id: row.id, rule_set_id: row.rule_set_id, rule_set_version: row.rule_set_version, rule_set_hash: row.rule_set_hash };
}

export function reviewScopeDescription(workspace: ReviewWorkspace): string {
  const scope = reviewScope(workspace);
  const agent = workspace.agent_review;
  const attempt = workspace.review_attempts?.attempt;
  const details = agent?.attempt_id === attempt?.id && agent?.scope?.length
    ? agent.scope.map((item) => `${item.domain}：${item.capability_ids?.join("、") || "能力未返回"}；来源 ${item.source_ids?.join("、") || "未返回"}`).join("；")
    : "执行范围明细未返回";
  return `${scope ? (scope.id === "single_contract_precheck" ? "单合同预审" : scope.id) : "审查范围身份未返回"}；${details}`;
}

function agentScopeKey(workspace: ReviewWorkspace): string | null {
  const agent = workspace.agent_review;
  if (!agent || agent.attempt_id !== workspace.review_attempts?.attempt.id || !agent.scope?.length) return null;
  const parts: string[] = [];
  for (const scope of agent.scope) {
    if (!scope.domain || !scope.capability_ids || !scope.source_ids ||
        scope.capability_ids.some((value) => !value) || scope.source_ids.some((value) => !value)) return null;
    parts.push(JSON.stringify([scope.domain, [...scope.capability_ids].sort(), [...scope.source_ids].sort()]));
  }
  return JSON.stringify(parts.sort());
}

export function scopeDifference(source: ReviewWorkspace, target: ReviewWorkspace): DifferenceState {
  const before = reviewScope(source);
  const after = reviewScope(target);
  const beforeAgent = agentScopeKey(source);
  const afterAgent = agentScopeKey(target);
  if ((before && after && before.id !== after.id) || (beforeAgent && afterAgent && beforeAgent !== afterAgent)) return "different";
  if (source.run.id === target.run.id) return "same";
  if (before || after) {
    if (!before || !after) return "unknown";
    if ((beforeAgent && !afterAgent) || (!beforeAgent && afterAgent)) return "unknown";
    return "same";
  }
  return beforeAgent && afterAgent ? "same" : "unknown";
}

export function ruleDifference(source: ReviewWorkspace, target: ReviewWorkspace): DifferenceState {
  const before = reviewScope(source)?.rule_set_hash || source.snapshot?.rule_set_hash;
  const after = reviewScope(target)?.rule_set_hash || target.snapshot?.rule_set_hash;
  if (before && after) return before === after ? "same" : "different";
  return source.run.id === target.run.id ? "same" : "unknown";
}

function hasUniqueStrings(values: string[]): boolean {
  return values.length > 0 && values.every((value) => value.trim() !== "") && new Set(values).size === values.length;
}

export function frozenWorkspaceProblem(workspace: ReviewWorkspace, caseId: string, runId: string, attemptId: string): string | null {
  const attempt = workspace.review_attempts?.attempt;
  if (workspace.run.case_id !== caseId || workspace.run.id !== runId ||
      !attempt || attempt.id !== attemptId || attempt.run_id !== runId) return "返回的案件、审查或执行身份不一致，不能比较。";
  if (attempt.history_kind !== "frozen") return "该执行没有可核对的冻结记录，不能比较。";
  if (!attempt.finished_at || (attempt.state !== "completed" && attempt.state !== "awaiting_human"))
    return "该执行尚未完整完成，不能比较；未执行或部分结果不能视为风险消除。";
  const documentIds = workspace.snapshot?.document_ids ?? [];
  const parsedIds = attempt.parsed_document_ids;
  if (!hasUniqueStrings(documentIds) || !hasUniqueStrings(parsedIds)) return "冻结材料或解析版本身份未完整返回，不能比较。";
  for (const documentId of documentIds) {
    const matches = workspace.documents.filter((doc) => doc.id === documentId && !!doc.parsed_document_id && parsedIds.includes(doc.parsed_document_id));
    if (matches.length !== 1 || matches[0]?.text === null) return "冻结材料或其解析原文缺失、重复或不可读取，不能比较。";
  }
  if (parsedIds.some((id) => workspace.documents.filter((doc) => documentIds.includes(doc.id) && doc.parsed_document_id === id).length !== 1))
    return "冻结解析身份与材料不一致，不能比较。";
  return null;
}

export function frozenDocument(workspace: ReviewWorkspace, anchor: EvidenceAnchor): ReviewDocument | null {
  const attempt = workspace.review_attempts?.attempt;
  if (!attempt || !anchor.parsed_document_id || !attempt.parsed_document_ids.includes(anchor.parsed_document_id) ||
      !workspace.snapshot?.document_ids.includes(anchor.document_version_id)) return null;
  const docs = workspace.documents.filter((doc) => doc.id === anchor.document_version_id && doc.parsed_document_id === anchor.parsed_document_id);
  return docs.length === 1 ? docs[0] ?? null : null;
}

function agentAnchor(item: BusinessAgentEvidence): EvidenceAnchor {
  const anchor = item.anchor;
  return {
    id: anchor.id,
    document_version_id: anchor.document_id,
    parsed_document_id: anchor.parsed_document_id,
    normalized_digest: null,
    quote: anchor.quote,
    status: anchor.status === "located" ? "located" : "unlocatable",
    page: anchor.page,
    text_offset: { start: anchor.start, end: anchor.end },
    bbox: null,
    mismatch_reason: anchor.reason,
  };
}

export function sideAnchors(side: ReviewDiffSide): EvidenceAnchor[] {
  const anchors = [...(side.finding?.evidence ?? [])];
  const evaluation = side.evaluation;
  const review = side.workspace.agent_review;
  if (!evaluation || !review || review.attempt_id !== side.workspace.review_attempts?.attempt.id) return anchors;
  const evidenceIds = new Set((review.domains ?? []).flatMap((domain) => domain.capabilities ?? [])
    .filter((capability) => capability.canonical_evaluation_ids?.includes(evaluation.id))
    .flatMap((capability) => capability.evidence_ids ?? []));
  for (const item of review.evidence ?? []) {
    if (evidenceIds.has(item.evidence_id)) anchors.push(agentAnchor(item));
  }
  return anchors;
}

// Object references are source-local. A shared frozen clause disambiguates
// multi-document inputs; object-only matching needs a single frozen source.
// The public evaluation does not expose a stable service_object_id.
function evaluationKey(side: ReviewDiffSide): string | null {
  const item = side.evaluation;
  if (!item || !item.domain.trim() || !item.rule_id.trim() || !item.capability_id.trim()) return null;
  const anchors = sideAnchors(side);
  if (!anchors.length) return null;
  const located = anchors.map((anchor) => {
    const document = frozenDocument(side.workspace, anchor);
    if (!document) return null;
    const resolved = resolveAnchor(document.text, anchor, document.parsed_document, document.parsed_document_id);
    return resolved.kind === "highlight" ? { document, resolved } : null;
  });
  if (located.some((item) => item === null)) return null;
  const records = located.filter((item): item is NonNullable<typeof item> => item !== null);
  const documents = new Set(records.map(({ document }) => JSON.stringify([document.id, document.parsed_document_id])));
  if (documents.size !== 1) return null;
  const documentKey = [...documents][0];
  if (item.object_refs.length && !hasUniqueStrings(item.object_refs)) return null;
  const objects = [...item.object_refs].sort();
  const clauses = records.flatMap(({ document, resolved }) => document.clauses
    .filter((clause) => clause.id && clause.text_offset.start <= resolved.start && clause.text_offset.end >= resolved.end)
    .map((clause) => clause.id));
  const uniqueClauses = new Set(clauses);
  if (uniqueClauses.size === 1 && clauses.length === records.length) {
    return JSON.stringify([item.domain, item.rule_id, item.capability_id, "clause", documentKey, [...uniqueClauses][0], objects]);
  }
  if (objects.length && side.workspace.snapshot?.document_ids.length === 1) {
    return JSON.stringify([item.domain, item.rule_id, item.capability_id, "object", documentKey, objects]);
  }
  return null;
}

function baseKey(side: ReviewDiffSide): string | null {
  const item = side.evaluation;
  return item && item.domain && item.rule_id && item.capability_id
    ? JSON.stringify([item.domain, item.rule_id, item.capability_id]) : null;
}

function evaluationSides(workspace: ReviewWorkspace): ReviewDiffSide[] {
  const evaluations = workspace.evaluations ?? [];
  const findings = new Map<string, Finding[]>();
  const references = new Map<string, number>();
  for (const finding of workspace.findings) findings.set(finding.id, [...(findings.get(finding.id) ?? []), finding]);
  for (const evaluation of evaluations) {
    if (evaluation.finding_id) references.set(evaluation.finding_id, (references.get(evaluation.finding_id) ?? 0) + 1);
  }
  return evaluations.map((evaluation) => {
    const matches = evaluation.finding_id ? findings.get(evaluation.finding_id) ?? [] : [];
    const candidate = matches[0];
    const finding = evaluation.finding_id && matches.length === 1 && references.get(evaluation.finding_id) === 1 && candidate?.origin === "rule" ? candidate : null;
    return { workspace, evaluation, finding };
  });
}

function validFindingAssociation(side: ReviewDiffSide): boolean {
  return !side.evaluation?.finding_id || side.finding !== null;
}

function pairedKind(source: ReviewDiffSide, target: ReviewDiffSide): ReviewDiffKind {
  if (!validFindingAssociation(source) || !validFindingAssociation(target)) return "unmatched";
  const before = source.evaluation;
  const after = target.evaluation;
  if (!before || !after) return "unmatched";
  if ((!source.finding && before.relation !== "compatible") || (!target.finding && after.relation !== "compatible")) return "unmatched";
  if (!source.finding && target.finding) return "added";
  if (source.finding && !target.finding) return "disappeared";
  if (before.relation !== after.relation || before.reason !== after.reason || source.finding?.claim !== target.finding?.claim) return "changed";
  return "unchanged";
}

export function compareReviewWorkspaces(source: ReviewWorkspace, target: ReviewWorkspace): ReviewDiffRow[] {
  const before = evaluationSides(source);
  const after = evaluationSides(target);
  const sameScope = scopeDifference(source, target) === "same";
  const index = (sides: ReviewDiffSide[]) => {
    const byKey = new Map<string, { side: ReviewDiffSide; index: number }[]>();
    const bases = new Set<string>();
    const keys = sides.map((side, position) => {
      const base = baseKey(side);
      if (base) bases.add(base);
      const key = sameScope && validFindingAssociation(side) ? evaluationKey(side) : null;
      if (key) byKey.set(key, [...(byKey.get(key) ?? []), { side, index: position }]);
      return key;
    });
    return { byKey, bases, keys };
  };
  const beforeIndex = index(before);
  const afterIndex = index(after);
  const usedAfter = new Set<number>();
  const rows: ReviewDiffRow[] = [];

  before.forEach((left, position) => {
    const key = beforeIndex.keys[position];
    const leftCount = key ? beforeIndex.byKey.get(key)?.length ?? 0 : 0;
    const matches = key ? afterIndex.byKey.get(key) ?? [] : [];
    const match = leftCount === 1 && matches.length === 1 ? matches[0] : undefined;
    if (match) {
      usedAfter.add(match.index);
      const kind = pairedKind(left, match.side);
      rows.push({ kind, source: left, target: match.side, reason: kind === "unmatched" ? "对应检查缺少可确认的发现或结论。" : "按规则、能力和同一冻结来源中的对象或条款唯一对应。" });
      return;
    }
    const base = baseKey(left);
    const canDescribeAbsence = sameScope && !!key && leftCount === 1 && !!base && !afterIndex.bases.has(base) && !!left.finding;
    rows.push({ kind: canDescribeAbsence ? "disappeared" : "unmatched", source: left, target: null,
      reason: canDescribeAbsence ? "目标未返回相同规则检查；不代表已执行或风险消除。" : "范围、规则、对象或条款缺失或不唯一，不能自动对应。" });
  });

  after.forEach((right, position) => {
    if (usedAfter.has(position)) return;
    const key = afterIndex.keys[position];
    const rightCount = key ? afterIndex.byKey.get(key)?.length ?? 0 : 0;
    const base = baseKey(right);
    const canDescribeAddition = sameScope && !!key && rightCount === 1 && !!base && !beforeIndex.bases.has(base) && !!right.finding;
    rows.push({ kind: canDescribeAddition ? "added" : "unmatched", source: null, target: right,
      reason: canDescribeAddition ? "来源未返回相同规则检查；不能推断此前已执行该检查。" : "范围、规则、对象或条款缺失或不唯一，不能自动对应。" });
  });

  for (const [workspace, location, sides] of [[source, "source", before], [target, "target", after]] as const) {
    const attached = new Set(sides.flatMap((side) => side.finding ? [side.finding.id] : []));
    for (const finding of workspace.findings) {
      if (finding.origin === "rule" && attached.has(finding.id)) continue;
      const side: ReviewDiffSide = { workspace, evaluation: null, finding };
      rows.push({ kind: "unmatched", source: location === "source" ? side : null, target: location === "target" ? side : null,
        reason: finding.origin === "manual" ? "人工发现及决定是独立追加记录，不作为模型关系变化。" : "该线索没有唯一的规则、对象或条款身份，不能自动对应。" });
    }
  }
  return rows;
}
