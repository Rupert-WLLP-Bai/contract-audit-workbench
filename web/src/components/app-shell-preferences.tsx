// File metadata (manifest mirror):
// Purpose: Stores the client professional-mode display preference by browser identity and keeps administrative technical details independent of it.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-20
// Updated: 2026-11-20
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { createContext, useContext, useState, type ReactNode } from "react";
import type { Actor } from "../api/types";
import { browserIdentityKey } from "../domain/browser-identity";

const storagePrefix = "caw:professional-mode:";
const disabledPreference = { enabled: false, setEnabled: (_enabled: boolean) => {} };
const ProfessionalMode = createContext(disabledPreference);

function readPreference(scope: string): boolean {
  if (!scope) return false;
  try {
    return localStorage.getItem(storagePrefix + scope) === "true";
  } catch {
    return false;
  }
}

export function ProfessionalModeProvider({
  actor,
  children,
}: {
  actor: Actor | null | undefined;
  children: ReactNode;
}) {
  const scope = browserIdentityKey(actor);
  const [current, setCurrent] = useState(() => ({ scope, enabled: readPreference(scope) }));
  const preference = scope === current.scope ? current : { scope, enabled: readPreference(scope) };
  if (preference !== current) setCurrent(preference);

  return (
    <ProfessionalMode.Provider
      value={{
        enabled: preference.enabled,
        setEnabled(value) {
          if (!scope) return;
          try {
            localStorage.setItem(storagePrefix + scope, String(value));
          } catch {
            // Keep the preference for this view even when storage is unavailable.
          }
          setCurrent({ scope, enabled: value });
        },
      }}
    >
      {children}
    </ProfessionalMode.Provider>
  );
}

/** Administration keeps its existing technical view independently of the client preference. */
export function AdministrativeTechnicalDetails({ children }: { children: ReactNode }) {
  return (
    <ProfessionalMode.Provider value={{ ...disabledPreference, enabled: true }}>
      {children}
    </ProfessionalMode.Provider>
  );
}

export function useProfessionalMode() {
  return useContext(ProfessionalMode);
}
