// File metadata (manifest mirror):
// Purpose: Sends the selected procurement file to an authorized case using its captured command context and idempotency key.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-20
// Updated: 2026-11-20
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { uploadDocument } from "../../api/endpoints";
import { assertCommandContextCurrent, type CommandContext } from "../../api/http";
import {
  clearPendingMutationKey,
  fileSha256,
  idempotencyActorScope,
  pendingMutationKey,
} from "../../api/idempotency";
import type { Actor } from "../../api/types";

export async function uploadIntakeProcurement(
  actor: Actor,
  caseId: string,
  file: File,
  context: CommandContext,
) {
  const digest = await fileSha256(file);
  const { storageKey, key } = await pendingMutationKey(
    "intake.procurement",
    idempotencyActorScope(actor),
    caseId,
    { name: file.name, size: file.size, type: file.type, digest },
    context,
  );
  assertCommandContextCurrent(context);
  await uploadDocument(caseId, { file, role: "procurement", intent: "add", idempotency_key: key });
  assertCommandContextCurrent(context);
  try {
    clearPendingMutationKey(storageKey);
  } catch {
    // A retained key safely replays the same upload.
  }
}
