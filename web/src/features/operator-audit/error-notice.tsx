// File metadata (manifest mirror):
// Purpose: Re-exports the shared workbench error surface as the operator-audit panel error component, keeping error rendering consistent across the operator audit screens.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-29
// Updated: 2026-10-30
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

/** The operator-audit panels share the workbench error surface. */
export { ErrorNotice as OperatorAuditError } from "../../components/error-notice";
