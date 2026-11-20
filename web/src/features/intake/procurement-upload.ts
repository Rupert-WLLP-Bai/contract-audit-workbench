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
