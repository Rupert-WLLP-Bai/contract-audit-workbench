import type { Actor } from "../../api/types";
import { browserIdentityKey } from "../../domain/browser-identity";

interface Visit {
  caseId: string;
  visitedAt: string;
}

const maximumVisits = 12;

function storageKey(actor: Actor | null | undefined) {
  const identity = browserIdentityKey(actor);
  return identity ? `caw.recent-cases:${identity}` : "";
}

export function recentCaseVisits(actor: Actor | null | undefined): Visit[] {
  const key = storageKey(actor);
  if (!key) return [];
  try {
    const rows: unknown = JSON.parse(localStorage.getItem(key) ?? "[]");
    if (!Array.isArray(rows)) return [];
    return rows
      .filter(
        (row): row is Visit =>
          row !== null &&
          typeof row === "object" &&
          typeof row.caseId === "string" &&
          row.caseId !== "" &&
          typeof row.visitedAt === "string" &&
          Number.isFinite(Date.parse(row.visitedAt)),
      )
      .slice(0, maximumVisits);
  } catch {
    return [];
  }
}

/** Call only after confirming this case's identity and current read access. */
export function recordRecentCaseVisit(actor: Actor | null | undefined, caseId: string): void {
  const key = storageKey(actor);
  if (!key || !caseId) return;
  const visits = recentCaseVisits(actor).filter((visit) => visit.caseId !== caseId);
  try {
    localStorage.setItem(
      key,
      JSON.stringify(
        [{ caseId, visitedAt: new Date().toISOString() }, ...visits].slice(0, maximumVisits),
      ),
    );
  } catch {
    // Case reading remains available when browser storage is disabled.
  }
}
