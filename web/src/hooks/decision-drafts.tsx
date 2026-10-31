import {
  createContext,
  useContext,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from "react";
import type { DecisionAction } from "../api/types";

export type DecisionDraft = {
  action: DecisionAction | null;
  reason: string;
};

export type DecisionDraftScope = {
  userId: string;
  caseId: string;
  runId: string;
  attemptId: string | null;
};

const EMPTY_DECISION_DRAFT: DecisionDraft = { action: null, reason: "" };
const DECISION_DRAFT_FIELD_SEPARATOR = "\u0000";

// Drafts hold review reasoning, so they stay in tab memory only: nothing is
// written to storage that outlives the page or could be read by another
// account sharing the browser.
const draftStore = new Map<string, DecisionDraft>();
const draftListeners = new Set<() => void>();

function notifyDraftListeners(): void {
  for (const listener of draftListeners) listener();
}

export function decisionDraftKey(
  scope: DecisionDraftScope,
  findingId: string,
): string {
  return [
    scope.userId,
    scope.caseId,
    scope.runId,
    scope.attemptId ?? "",
    findingId,
  ].join(DECISION_DRAFT_FIELD_SEPARATOR);
}

export function decisionDraftUserPrefix(userId: string): string {
  return `${userId}${DECISION_DRAFT_FIELD_SEPARATOR}`;
}

export function writeDecisionDraft(
  key: string,
  update: (draft: DecisionDraft) => DecisionDraft,
): void {
  const next = update(draftStore.get(key) ?? EMPTY_DECISION_DRAFT);
  if (next.action === null && next.reason === "") draftStore.delete(key);
  else draftStore.set(key, { action: next.action, reason: next.reason });
  notifyDraftListeners();
}

export function clearDecisionDraft(key: string): void {
  if (!draftStore.delete(key)) return;
  notifyDraftListeners();
}

// Test suites share one module registry across cases; suites that mount the
// review workspace reset the store so drafts never leak between tests.
export function clearAllDecisionDrafts(): void {
  if (draftStore.size === 0) return;
  draftStore.clear();
  notifyDraftListeners();
}

function hasDecisionDrafts(prefix: string): boolean {
  for (const key of draftStore.keys()) {
    if (key.startsWith(prefix)) return true;
  }
  return false;
}

function subscribeDecisionDrafts(listener: () => void): () => void {
  draftListeners.add(listener);
  return () => {
    draftListeners.delete(listener);
  };
}

const DecisionDraftScopeContext = createContext<DecisionDraftScope | null>(
  null,
);

export function DecisionDraftScopeProvider({
  scope,
  children,
}: {
  scope: DecisionDraftScope | null;
  children: ReactNode;
}) {
  return (
    <DecisionDraftScopeContext.Provider value={scope}>
      {children}
    </DecisionDraftScopeContext.Provider>
  );
}

const noDraftsSnapshot = () => false;

export function useDecisionDraftsDirty(prefix: string | null): boolean {
  return useSyncExternalStore(
    subscribeDecisionDrafts,
    prefix === null ? noDraftsSnapshot : () => hasDecisionDrafts(prefix),
  );
}

export function useDecisionDraft(findingId: string): {
  action: DecisionAction | null;
  reason: string;
  update: (update: (draft: DecisionDraft) => DecisionDraft) => void;
  clear: () => void;
} {
  const scope = useContext(DecisionDraftScopeContext);
  const key = scope === null ? null : decisionDraftKey(scope, findingId);
  const [unscoped, setUnscoped] = useState<DecisionDraft>(EMPTY_DECISION_DRAFT);
  const stored = useSyncExternalStore(
    subscribeDecisionDrafts,
    () =>
      key === null
        ? EMPTY_DECISION_DRAFT
        : (draftStore.get(key) ?? EMPTY_DECISION_DRAFT),
  );
  if (key === null) {
    // Without a scope (tests, callers outside the review workspace) the form
    // keeps its original local-state behavior and never touches the store.
    return {
      action: unscoped.action,
      reason: unscoped.reason,
      update: (update) => setUnscoped(update),
      clear: () => setUnscoped(EMPTY_DECISION_DRAFT),
    };
  }
  return {
    action: stored.action,
    reason: stored.reason,
    update: (update) => writeDecisionDraft(key, update),
    clear: () => clearDecisionDraft(key),
  };
}
