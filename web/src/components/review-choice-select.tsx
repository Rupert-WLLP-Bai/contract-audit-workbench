// File metadata (manifest mirror):
// Purpose: Renders ReviewChoiceSelect, the review choice select workbench UI, from the selected case/workspace projection and the module’s query/state inputs; it displays server-derived state and does not establish business authorization or evidence validity.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-28
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import type { ReviewChoice } from "../domain/workflow";
import { formatDateTime, runLabel } from "../domain/review";
import { Select } from "antd";

export function ReviewChoiceSelect({
  label,
  choices,
  value,
  onChange,
  disabled = false,
}: {
  label: string;
  choices: ReviewChoice[];
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
}) {
  return (
    <label className="field">
      {label}
      <Select
        aria-label={label}
        value={value || undefined}
        onChange={onChange}
        disabled={disabled}
        placeholder="选择已保存的审查和执行记录"
        options={choices.map((choice) => ({
          value: choice.key,
          label: `审查记录 ${choice.runOrder + 1}，第 ${choice.attempt.ordinal} 次，${runLabel(choice.attempt.state)}，${formatDateTime(choice.workspace.run.updated_at)}`,
        }))}
      />
    </label>
  );
}
