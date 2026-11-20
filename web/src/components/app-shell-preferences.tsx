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
