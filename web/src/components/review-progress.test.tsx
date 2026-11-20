import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ReviewProgress } from "./review-progress";

describe("review progress", () => {
  it.each([
    ["accepted", false, false, false, "材料", "active"],
    ["accepted", true, false, false, "准备", "active"],
    ["queued", true, false, false, "准备", "active"],
    ["partial", true, false, true, "原文审查（部分完成）", "warning"],
    ["completed", true, true, true, "原文审查", "active"],
    ["completed", true, false, true, "整改与报告", "active"],
    ["cancelled", true, false, false, "准备", "warning"],
    ["failed", true, false, true, "原文审查", "warning"],
  ])(
    "exposes exactly one current step for %s",
    (state, hasDocuments, hasPendingJudgments, hasFrozenAttempt, title, stage) => {
      render(
        <ReviewProgress {...{ state, hasDocuments, hasPendingJudgments, hasFrozenAttempt }} />,
      );
      const progress = screen.getByRole("list", { name: "审查进度" });
      const steps = within(progress).getAllByRole("listitem");
      const current = steps.filter((step) => step.getAttribute("aria-current") === "step");
      expect(current).toEqual([within(progress).getByText(title)]);
      expect(current[0]).toHaveAttribute("data-stage", stage);
      expect(
        steps.filter((step) => ["active", "warning"].includes(step.dataset.stage ?? "")),
      ).toEqual(current);
    },
  );
});
