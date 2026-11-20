// File metadata (manifest mirror):
// Purpose: Presents the selected review attempt's business progress with one accessible current step and warning states for unfinished execution.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-20
// Updated: 2026-11-20
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

export function ReviewProgress({
  state,
  hasDocuments,
  hasPendingJudgments,
  hasFrozenAttempt,
}: {
  state: string;
  hasDocuments: boolean;
  hasPendingJudgments: boolean;
  hasFrozenAttempt: boolean;
}) {
  const partial = state === "partial";
  const halted = ["failed", "cancelled", "interrupted"].includes(state);
  let current = hasDocuments ? 1 : 0;
  if (state === "completed" && !hasPendingJudgments) {
    current = 3;
  } else if (
    partial ||
    state === "completed" ||
    state === "awaiting_human" ||
    (halted && hasFrozenAttempt)
  ) {
    current = 2;
  }
  const warning = partial || halted;
  const steps = ["材料", "准备", partial ? "原文审查（部分完成）" : "原文审查", "整改与报告"];
  return (
    <ol className="review-progress" aria-label="审查进度">
      {steps.map((step, index) => (
        <li
          key={index}
          aria-current={index === current ? "step" : undefined}
          data-stage={
            index < current
              ? "done"
              : index === current
                ? warning
                  ? "warning"
                  : "active"
                : undefined
          }
        >
          {step}
        </li>
      ))}
    </ol>
  );
}
