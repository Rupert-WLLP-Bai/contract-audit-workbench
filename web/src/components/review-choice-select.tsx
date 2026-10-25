import type { ReviewChoice } from "../domain/workflow";
import { runLabel } from "../domain/review";

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
      <select aria-label={label} value={value} onChange={(event) => onChange(event.target.value)} disabled={disabled} required>
        <option value="">选择已保存的运行和尝试</option>
        {choices.map((choice) => (
          <option key={choice.key} value={choice.key}>
            {choice.runId} · 第 {choice.attempt.ordinal} 次 · {runLabel(choice.attempt.state)}
          </option>
        ))}
      </select>
    </label>
  );
}
