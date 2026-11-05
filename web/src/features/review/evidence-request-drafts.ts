// File metadata (manifest mirror):
// Purpose: Keeps bounded, actor-scoped evidence-request creation drafts and revocable operation checkpoints in tab memory without storing credentials or creating task authority.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-05
// Updated: 2026-11-05
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

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
function entry(scope: string, source = activeSources.get(scope) ?? "") {
  const result = entries.get(key(scope, source));
  return result && Date.now() - result.updatedAt < MAX_AGE_MS ? result : undefined;
}
function save(scope: string, source: string, draft: EvidenceDraft): void {
  if (draft.source !== source) throw new Error("切换来源时必须选择对应的草稿");
  const entryKey = key(scope, source);
  entries.delete(entryKey);
  entries.set(entryKey, { scope, draft, updatedAt: Date.now() });
  while (entries.size > MAX_DRAFTS) {
    const first = entries.keys().next().value;
    if (!first) break;
    entries.delete(first);
  }
  for (const [owner, active] of activeSources) if (!entries.has(key(owner, active))) activeSources.delete(owner);
  notify();
}
export function activeEvidenceSource(scope: string): string | null {
  return activeSources.get(scope) ?? null;
}
export function readEvidenceDraft(scope: string | null, source?: string): EvidenceDraft {
  return scope ? entry(scope, source)?.draft ?? emptyDraft : emptyDraft;
}
export function useEvidenceDraft(scope: string | null): EvidenceDraft {
  return useSyncExternalStore(subscribe, () => readEvidenceDraft(scope), () => emptyDraft);
}
export function updateEvidenceDraft(scope: string, update: (draft: EvidenceDraft) => EvidenceDraft): void {
  const source = activeSources.get(scope) ?? "";
  const draft = update(entry(scope, source)?.draft ?? { ...emptyDraft, source });
  activeSources.set(scope, source);
  save(scope, source, draft);
}
export function activateEvidenceDraft(scope: string, source: string): void {
  if ((activeSources.get(scope) ?? "") === source) return;
  const previous = entry(scope)?.draft;
  interruptEvidenceDraft(scope);
  activeSources.set(scope, source);
  if (!entry(scope, source)) {
    const initial = previous?.source === "" ? { ...previous, source } : { ...emptyDraft, source };
    if (previous?.source === "") entries.delete(key(scope, ""));
    save(scope, source, initial);
  } else notify();
}
export function clearEvidenceDraft(scope: string, source = activeSources.get(scope)): void {
  if (source === undefined) return;
  entries.delete(key(scope, source));
  if (activeSources.get(scope) === source) activeSources.delete(scope);
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
export function evidenceOperationCurrent(scope: string, operation: number, source = activeSources.get(scope) ?? ""): boolean {
  const draft = entry(scope, source)?.draft;
  return activeSources.get(scope) === source && !!draft && draft.operation === operation && !draft.interrupted;
}
export function updateEvidenceOperation(scope: string, operation: number, update: (draft: EvidenceDraft) => EvidenceDraft, source = activeSources.get(scope) ?? ""): boolean {
  const current = entry(scope, source)?.draft;
  if (!current || current.operation !== operation) return false;
  save(scope, source, update(current));
  return true;
}
export function interruptEvidenceDraft(scope: string, source = activeSources.get(scope) ?? ""): void {
  const draft = entry(scope, source)?.draft;
  if (!draft) return;
  save(scope, source, {
    ...draft,
    phase: draft.phase === "task-pending" ? "task-unknown" : draft.phase === "request-pending" ? "request-unknown" : draft.phase,
    interrupted: true,
    needsVerification: !!draft.task || draft.phase === "task-pending" || draft.phase === "task-unknown",
  });
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
