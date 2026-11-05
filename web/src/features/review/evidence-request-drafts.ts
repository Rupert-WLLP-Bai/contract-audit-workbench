import { useSyncExternalStore } from "react";

// Only the supplement creation flow uses this bounded, tab-local store. Scope
// contains actorReadContextKey (never sessionIdentityFor), case, run and attempt.
export type EvidenceTaskRef = { id: string; version: number; caseId: string; assigneeId: string };
export type EvidenceDraft = {
  source: string;
  title: string;
  assignee: string;
  due: string;
  question: string;
  role: string;
  condition: "compatible" | "conflict";
  taskMode: "new" | "existing";
  existingTaskId: string;
  task: EvidenceTaskRef | null;
  phase: "editing" | "task-pending" | "task-unknown" | "linked" | "request-pending" | "request-unknown";
  operation: number;
  interrupted: boolean;
  needsVerification: boolean;
  requestKey: string | null;
  dirty: boolean;
};

const emptyDraft: EvidenceDraft = {
  source: "", title: "", assignee: "", due: "", question: "", role: "", condition: "compatible",
  taskMode: "new", existingTaskId: "", task: null, phase: "editing", operation: 0,
  interrupted: false, needsVerification: false, requestKey: null, dirty: false,
};
const MAX_DRAFTS = 20;
const MAX_AGE_MS = 60 * 60 * 1000;
const entries = new Map<string, { scope: string; draft: EvidenceDraft; updatedAt: number }>();
const activeSources = new Map<string, string>();
const listeners = new Set<() => void>();
let nextOperation = 0;
const key = (scope: string, source: string) => JSON.stringify([scope, source]);
export function evidenceDraftScope(actorScope: string | null, caseId: string, runId: string, attemptId: string | null): string | null {
  return actorScope && caseId && runId && attemptId ? JSON.stringify([actorScope, caseId, runId, attemptId]) : null;
}
function notify() { for (const listener of listeners) listener(); }
function subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; }
function entry(scope: string) {
  const result = entries.get(key(scope, activeSources.get(scope) ?? ""));
  return result && Date.now() - result.updatedAt < MAX_AGE_MS ? result : undefined;
}
export function readEvidenceDraft(scope: string | null): EvidenceDraft {
  return scope ? entry(scope)?.draft ?? emptyDraft : emptyDraft;
}
export function useEvidenceDraft(scope: string | null): EvidenceDraft {
  return useSyncExternalStore(subscribe, () => readEvidenceDraft(scope), () => emptyDraft);
}
export function updateEvidenceDraft(scope: string, update: (draft: EvidenceDraft) => EvidenceDraft): void {
  const previousSource = activeSources.get(scope) ?? "";
  const draft = update(entry(scope)?.draft ?? emptyDraft);
  const oldKey = key(scope, previousSource);
  const nextKey = key(scope, draft.source);
  if (oldKey !== nextKey) entries.delete(oldKey);
  entries.delete(nextKey);
  entries.set(nextKey, { scope, draft, updatedAt: Date.now() });
  activeSources.set(scope, draft.source);
  while (entries.size > MAX_DRAFTS) {
    const first = entries.keys().next().value;
    if (!first) break;
    entries.delete(first);
  }
  // Pointers contain identifiers only and must be bounded with the entries.
  for (const [owner, source] of activeSources) if (!entries.has(key(owner, source))) activeSources.delete(owner);
  notify();
}
export function activateEvidenceDraft(scope: string, source: string): void {
  if ((activeSources.get(scope) ?? "") === source) return;
  interruptEvidenceDraft(scope);
  activeSources.set(scope, source);
  if (!entry(scope)) updateEvidenceDraft(scope, (draft) => ({ ...draft, source }));
  else notify();
}
export function clearEvidenceDraft(scope: string): void {
  const source = activeSources.get(scope);
  if (source === undefined) return;
  entries.delete(key(scope, source));
  activeSources.delete(scope);
  notify();
}
export function clearAllEvidenceDrafts(): void {
  entries.clear();
  activeSources.clear();
  notify();
}
export function beginEvidenceOperation(scope: string, phase: "task-pending" | "request-pending"): number {
  const operation = ++nextOperation;
  updateEvidenceDraft(scope, (draft) => ({ ...draft, phase, operation, interrupted: false, dirty: true }));
  return operation;
}
export function evidenceOperationCurrent(scope: string, operation: number): boolean {
  const draft = entry(scope)?.draft;
  return !!draft && draft.operation === operation && !draft.interrupted;
}
export function updateEvidenceOperation(scope: string, operation: number, update: (draft: EvidenceDraft) => EvidenceDraft): boolean {
  const current = entry(scope)?.draft;
  if (!current || current.operation !== operation) return false;
  updateEvidenceDraft(scope, update);
  return true;
}
export function interruptEvidenceDraft(scope: string): void {
  const current = entry(scope)?.draft;
  if (!current) return;
  updateEvidenceDraft(scope, (draft) => ({
    ...draft,
    phase: draft.phase === "task-pending" ? "task-unknown" : draft.phase === "request-pending" ? "request-unknown" : draft.phase,
    interrupted: true,
    needsVerification: !!draft.task || draft.phase === "task-pending" || draft.phase === "task-unknown",
  }));
}
export function hasEvidenceDraft(scope: string | null): boolean {
  if (!scope) return false;
  for (const item of entries.values()) {
    const draft = item.draft;
    if (item.scope === scope && Date.now() - item.updatedAt < MAX_AGE_MS &&
        (draft.dirty || !!draft.task || draft.phase !== "editing")) return true;
  }
  return false;
}
export function useEvidenceDraftDirty(scope: string | null): boolean {
  return useSyncExternalStore(subscribe, () => hasEvidenceDraft(scope), () => false);
}
