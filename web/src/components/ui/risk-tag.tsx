import { Tag } from "antd";

const SEVERITY = {
  high: {
    label: "高风险",
    color: "#B91C1C",
    background: "#FEF2F2",
    border: "#FECACA",
  },
  medium: {
    label: "中风险",
    color: "#B45309",
    background: "#FFFBEB",
    border: "#FDE68A",
  },
  low: {
    label: "低风险",
    color: "#047857",
    background: "#ECFDF5",
    border: "#A7F3D0",
  },
  info: {
    label: "提示",
    color: "#1D4ED8",
    background: "#EFF4FF",
    border: "#DBE7FF",
  },
  unrated: {
    label: "未评级",
    color: "#475569",
    background: "#F1F5F9",
    border: "#E2E8F0",
  },
} as const;

export type RiskSeverity = keyof typeof SEVERITY;

export function severityLabel(severity: string | null | undefined): string {
  if (!severity) return SEVERITY.unrated.label;
  return (
    SEVERITY[severity as RiskSeverity]?.label ?? `未识别（${severity}）`
  );
}

export function RiskTag({
  severity,
  label,
}: {
  severity: string | null | undefined;
  label?: string;
}) {
  const key = (severity ?? "unrated") as RiskSeverity;
  const tone = SEVERITY[key] ?? SEVERITY.unrated;
  return (
    <Tag
      className="caw-risk-tag"
      style={{
        color: tone.color,
        background: tone.background,
        borderColor: tone.border,
      }}
    >
      <span className="caw-risk-tag__dot" aria-hidden="true" />
      {label ?? severityLabel(severity)}
    </Tag>
  );
}
