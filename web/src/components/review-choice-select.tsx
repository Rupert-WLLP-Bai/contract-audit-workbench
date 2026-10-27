import type { ReviewChoice } from "../domain/workflow";
import { runLabel } from "../domain/review";
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
        placeholder="选择已保存的运行和尝试"
        options={choices.map((choice) => ({
          value: choice.key,
          label: `${choice.runId}，第 ${choice.attempt.ordinal} 次，${runLabel(choice.attempt.state)}`,
        }))}
      />
    </label>
  );
}
