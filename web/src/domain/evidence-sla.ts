import type {
  EvidenceReceipt,
  EvidenceRequest,
  EvidenceRequestEvent,
} from "../api/evidence-workflow";

const outstandingStatuses: Record<string, true> = { open: true, received: true };

const rfc3339Timestamp = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/;
function timestamp(value: string | null | undefined): number | null {
  if (!value || !rfc3339Timestamp.test(value)) return null;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : null;
}

export function isOutstandingEvidenceRequest(status: string): boolean {
  return Object.hasOwn(outstandingStatuses, status);
}

export function hasEvidenceDeadline(dueAt: string | null | undefined): boolean {
  return timestamp(dueAt) !== null;
}

export function isEvidenceRequestOverdue(
  request: Pick<EvidenceRequest, "status" | "due_at">,
  now = Date.now(),
): boolean {
  if (!isOutstandingEvidenceRequest(request.status)) return false;
  const dueAt = timestamp(request.due_at);
  return dueAt !== null && dueAt < now;
}

export function firstRegisteredEvidenceReceipt(
  events: readonly EvidenceRequestEvent[],
): EvidenceReceipt | null {
  let first: EvidenceReceipt | null = null;
  let firstAt = Number.POSITIVE_INFINITY;
  for (const event of events) {
    const receipt = event.receipt;
    if (!receipt) continue;
    const createdAt = timestamp(receipt.created_at);
    if (createdAt !== null && createdAt < firstAt) {
      first = receipt;
      firstAt = createdAt;
    }
  }
  return first;
}

export function firstResponseDurationMs(
  request: Pick<EvidenceRequest, "created_at">,
  receipt: Pick<EvidenceReceipt, "created_at"> | null,
): number | null {
  if (!receipt) return null;
  const requestCreatedAt = timestamp(request.created_at);
  const receiptCreatedAt = timestamp(receipt.created_at);
  if (requestCreatedAt === null || receiptCreatedAt === null) return null;
  const elapsed = receiptCreatedAt - requestCreatedAt;
  return elapsed >= 0 ? elapsed : null;
}

export function averageResponseDurationMs(
  durations: readonly (number | null)[],
): number | null {
  let total = 0;
  let count = 0;
  for (const duration of durations) {
    if (duration === null || !Number.isFinite(duration) || duration < 0) continue;
    total += duration;
    count++;
  }
  return count === 0 ? null : total / count;
}

export function formatElapsedDuration(durationMs: number): string {
  if (!Number.isFinite(durationMs) || durationMs < 0) return "时长未报告";
  if (durationMs < 60_000) return `${(durationMs / 1_000).toFixed(1)} 秒`;
  if (durationMs < 3_600_000) return `${(durationMs / 60_000).toFixed(1)} 分钟`;
  if (durationMs < 86_400_000) return `${(durationMs / 3_600_000).toFixed(1)} 小时`;
  return `${(durationMs / 86_400_000).toFixed(1)} 天`;
}

export function evidenceRequestStatusLabel(status: string): string {
  switch (status) {
    case "open":
      return "待回复";
    case "received":
      return "已回执，待核验";
    case "verified":
      return "已核验";
    case "cancelled":
      return "已取消";
    default:
      return `未知状态：${status}`;
  }
}
