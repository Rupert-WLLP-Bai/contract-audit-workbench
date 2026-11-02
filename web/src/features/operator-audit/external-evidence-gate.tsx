// File metadata (manifest mirror):
// Purpose: Renders the operator-audit external evidence gate extension from its typed case/run projections and explicit feature flags; any command uses the draft extension client and remains disabled unless the server and deployment enable that surface.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-30
// Updated: 2026-10-30
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

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
