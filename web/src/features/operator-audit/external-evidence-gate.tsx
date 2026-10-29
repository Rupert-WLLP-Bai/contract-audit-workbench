import type { ReactNode } from "react";
import {
  DEFAULT_OPERATOR_AUDIT_FLAGS,
  externalEvidenceEnabled,
  type OperatorAuditFlags,
} from "./feature-flags";

/**
 * Default off. The Lead mounts these panels only after wiring evidence
 * candidates or an object binding; without the flag the panel stays absent
 * rather than rendering an empty shell.
 */
export function ExternalEvidenceGate({
  flags,
  children,
}: {
  flags?: OperatorAuditFlags;
  children: ReactNode;
}) {
  if (!externalEvidenceEnabled(flags ?? DEFAULT_OPERATOR_AUDIT_FLAGS))
    return null;
  return <>{children}</>;
}
