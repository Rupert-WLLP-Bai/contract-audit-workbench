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
import { reviewChoiceForKey, reviewChoiceLabel, reviewChoiceShortLabel } from "../domain/workflow";
import { preciseTime } from "../domain/time";
import { TechnicalDetail } from "./technical-detail";
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
  const selected = reviewChoiceForKey(choices, value);
  return (
    <div className="field">
      <label>
        {label}
        <Select
          aria-label={label}
          value={value || undefined}
          onChange={onChange}
          disabled={disabled}
          placeholder="选择已保存的审查和执行记录"
          virtual={false}
          labelRender={() => selected ? reviewChoiceShortLabel(selected) : "请选择"}
          optionRender={(option) => <div style={{ whiteSpace: "normal", overflowWrap: "anywhere" }}>{option.label}</div>}
          styles={{ popup: { listItem: { whiteSpace: "normal" } } }}
          options={choices.map((choice) => ({
            value: choice.key,
            label: reviewChoiceLabel(choice),
          }))}
        />
      </label>
      {selected ? <div className="meta caw-wrap">
        执行开始 {preciseTime(selected.attempt.started_at)} · 完成时间 {preciseTime(selected.attempt.finished_at)} · 审查更新时间 {preciseTime(selected.workspace.run.updated_at)}
        <TechnicalDetail value={selected.runId} label="审查标识" />
        <TechnicalDetail value={selected.attempt.id} label="执行标识" />
      </div> : null}
    </div>
  );
}
